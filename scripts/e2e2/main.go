// Command e2e2 live-tests the data and session tools against Nautilus: make a small
// volume, upload a file, list it, download it, start a CPU-only Jupyter session, reach it
// through port-forward, then clean everything up. No GPU, nothing public.
// Run: go run ./scripts/e2e2 -bin /path/to/nrp-mcp -ns NAMESPACE
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	bin := flag.String("bin", "nrp-mcp", "nrp-mcp binary")
	ns := flag.String("ns", "", "namespace")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2e2", Version: "0"}, nil)
	cs, err := cl.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(*bin, "serve", "--namespace", *ns)}, nil)
	must(err)
	defer cs.Close()

	failed := false
	defer func() {
		if failed {
			os.Exit(1)
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			failed = true
		}
	}()
	var runs []string
	defer func() {
		// Always clean up, even after a failure.
		for _, r := range runs {
			c1, e := callE(ctx, cs, "nrp_cleanup", map[string]any{"run": r})
			if e != nil || c1["confirm_token"] == nil {
				continue
			}
			c2, _ := callE(ctx, cs, "nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "confirm_token": c1["confirm_token"]})
			fmt.Println("CLEANUP", r, ":", c2["summary"])
		}
	}()
	call := func(name string, args map[string]any) map[string]any {
		m, err := callE(ctx, cs, name, args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "E2E FAIL:", err)
			panic("fail")
		}
		return m
	}

	// 1. Volume.
	vp := call("nrp_plan", map[string]any{"goal": "volume", "size": "1Gi", "name": "e2e-vol", "data_is_p1": true})
	fmt.Println("VOLUME PLAN:", vp["summary"])
	vr := call("nrp_run", map[string]any{"plan_id": vp["plan_id"], "confirm_token": vp["confirm_token"]})
	runs = append(runs, vr["run_id"].(string))
	fmt.Println("VOLUME RUN:", vr["summary"])
	for i := 0; i < 30; i++ {
		st := call("nrp_status", nil)
		vols, _ := st["volumes"].([]any)
		bound := false
		for _, v := range vols {
			if vm := v.(map[string]any); vm["name"] == "e2e-vol" && vm["status"] == "Bound" {
				bound = true
			}
		}
		if bound {
			fmt.Println("VOLUME: Bound")
			break
		}
		time.Sleep(5 * time.Second)
	}

	// 2. Data up / list / down.
	tmp, _ := os.MkdirTemp("", "nrp-e2e2-")
	defer os.RemoveAll(tmp)
	src := filepath.Join(tmp, "sample.csv")
	_ = os.WriteFile(src, []byte("site,leaf_area\nA,12.5\nB,9.1\n"), 0o644)
	_, err = callE(ctx, cs, "nrp_data", map[string]any{"action": "up", "volume": "e2e-vol", "local": src})
	if err == nil || !strings.Contains(err.Error(), "P1") {
		fmt.Fprintln(os.Stderr, "E2E FAIL: upload without data_is_p1 was not refused:", err)
		panic("fail")
	}
	fmt.Println("DATA up without data_is_p1: refused ok")
	up := call("nrp_data", map[string]any{"action": "up", "volume": "e2e-vol", "local": src, "path": "sample.csv", "data_is_p1": true})
	fmt.Println("DATA UP:", up["summary"])
	ls := call("nrp_data", map[string]any{"action": "list", "volume": "e2e-vol"})
	fmt.Println("DATA LIST:\n" + fmt.Sprint(ls["output"]))
	dst := filepath.Join(tmp, "back.csv")
	dn := call("nrp_data", map[string]any{"action": "down", "volume": "e2e-vol", "path": "sample.csv", "local": dst})
	fmt.Println("DATA DOWN:", dn["summary"])
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(dst)
	if string(a) != string(b) {
		fmt.Fprintf(os.Stderr, "E2E FAIL: round trip differs: %q vs %q\n", a, b)
		panic("fail")
	}
	fmt.Println("DATA round trip identical: ok")

	// 3. CPU-only Jupyter session with the volume.
	sp := call("nrp_plan", map[string]any{"goal": "session", "gpu": 0, "cpu": "1", "memory": "2Gi", "data_volume": "e2e-vol", "name": "e2e-nb"})
	fmt.Println("SESSION PLAN:", sp["summary"], "runnable:", sp["runnable"])
	if sp["runnable"] != true {
		fmt.Fprintln(os.Stderr, "E2E FAIL: session not runnable:", sp["refusals"])
		panic("fail")
	}
	sr := call("nrp_run", map[string]any{"plan_id": sp["plan_id"], "confirm_token": sp["confirm_token"]})
	srun := sr["run_id"].(string)
	runs = append([]string{srun}, runs...)
	fmt.Println("SESSION RUN:", sr["summary"])
	var ss map[string]any
	for i := 0; i < 60; i++ {
		time.Sleep(10 * time.Second)
		ss = call("nrp_session", map[string]any{"run": srun})
		if ss["ready"] == true {
			break
		}
		if i%3 == 0 {
			w := call("nrp_watch", map[string]any{"target": srun, "tail": 5})
			fmt.Println("  waiting:", w["summary"])
		}
	}
	fmt.Println("SESSION:", ss["summary"])
	fmt.Println("  command:", ss["command"])
	if ss["ready"] != true {
		fmt.Fprintln(os.Stderr, "E2E FAIL: session never ready")
		panic("fail")
	}
	// Run the port-forward and fetch the URL.
	pf := exec.Command("bash", "-c", fmt.Sprint(ss["command"]))
	pf.Stdout, pf.Stderr = nil, nil
	must(pf.Start())
	defer func() { _ = pf.Process.Kill() }()
	url := fmt.Sprint(ss["url"])
	ok := false
	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Second)
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			fmt.Println("SESSION URL via port-forward: HTTP", resp.StatusCode)
			ok = resp.StatusCode == 200
			break
		}
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "E2E FAIL: session URL did not answer 200")
		panic("fail")
	}
	// Without the token Jupyter must not let us in.
	base := url[:strings.Index(url, "?")]
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := c.Get(strings.Replace(base, "/lab", "/api/contents", 1)); err == nil {
		resp.Body.Close()
		fmt.Println("SESSION API without token: HTTP", resp.StatusCode, "(want 403)")
		if resp.StatusCode != 403 {
			fmt.Fprintln(os.Stderr, "E2E FAIL: session API open without token")
			panic("fail")
		}
	}
	fmt.Println("E2E2 OK")
}

func callE(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, error) {
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if r.IsError {
		var b strings.Builder
		for _, c := range r.Content {
			if t, ok := c.(*mcp.TextContent); ok {
				b.WriteString(t.Text)
			}
		}
		return nil, fmt.Errorf("%s: %s", name, b.String())
	}
	var m map[string]any
	bb, _ := json.Marshal(r.StructuredContent)
	_ = json.Unmarshal(bb, &m)
	return m, nil
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "E2E FAIL:", err)
		os.Exit(1)
	}
}
