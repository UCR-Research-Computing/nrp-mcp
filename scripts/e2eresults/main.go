// Command e2eresults drives a real nrp-mcp over stdio against Nautilus to test 0.9.0 job
// results: a tiny CPU job writes results/hello.txt, the sidecar uploads it to the person's
// NRP S3 bucket, and nrp_data action=down run=<id> brings it back. Needs `nrp-mcp s3 setup`
// first. Runs with NRP_CONSENT=token (no approval needed for a CPU job anyway).
// Run: go run ./scripts/e2eresults -bin /path/to/nrp-mcp -ns NAMESPACE
// Never creates GPU pods or public endpoints.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2eresults", Version: "0"}, nil)
	cmd := exec.Command(*bin, "serve", "--namespace", *ns)
	cmd.Env = append(os.Environ(), "NRP_CONSENT=token")
	cs, err := cl.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	must(err)
	defer cs.Close()

	call := func(name string, args map[string]any) map[string]any {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		must(err)
		if r.IsError {
			var b strings.Builder
			for _, c := range r.Content {
				if t, ok := c.(*mcp.TextContent); ok {
					b.WriteString(t.Text)
				}
			}
			fail("%s error: %s", name, b.String())
		}
		var m map[string]any
		bb, _ := json.Marshal(r.StructuredContent)
		_ = json.Unmarshal(bb, &m)
		return m
	}

	dir, _ := os.MkdirTemp("", "nrp-e2eresults-")
	defer os.RemoveAll(dir)
	must(os.WriteFile(filepath.Join(dir, "run.py"), []byte(`import os
os.makedirs(os.environ["RESULTS_DIR"], exist_ok=True)
open(os.path.join(os.environ["RESULTS_DIR"], "hello.txt"), "w").write("results ok\n")
print("wrote results")
`), 0o644))

	fmt.Println("STATUS:", call("nrp_status", nil)["summary"])
	p := call("nrp_plan", map[string]any{"project": dir, "cpu": "1", "memory": "1Gi", "hours": 1, "name": "e2e-results"})
	fmt.Println("PLAN:", p["summary"])
	if p["runnable"] != true {
		fail("not runnable: %v", p["refusals"])
	}
	saves := false
	for _, d := range p["decisions"].([]any) {
		if strings.HasPrefix(fmt.Sprint(d), "Saves results") {
			saves = true
			fmt.Println("  -", d)
		}
	}
	if !saves {
		fail("plan has no \"Saves results\" decision (is `nrp-mcp s3 setup` done?)")
	}
	r := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "confirm_token": p["confirm_token"]})
	run := fmt.Sprint(r["run_id"])
	fmt.Println("RUN:", r["summary"])

	cleanup := func() {
		c1 := call("nrp_cleanup", map[string]any{"run": run})
		if c1["confirm_token"] != nil {
			c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "confirm_token": c1["confirm_token"]})
			fmt.Println("CLEANUP:", c2["summary"])
		}
	}
	start := time.Now()
	var w map[string]any
	done := false
	for time.Since(start) < 15*time.Minute {
		time.Sleep(20 * time.Second)
		w = call("nrp_watch", map[string]any{"target": run, "tail": 20})
		fmt.Printf("WATCH +%3.0fs: %v\n", time.Since(start).Seconds(), w["summary"])
		pr := fmt.Sprint(w["progress"])
		if strings.Contains(pr, "Failed") {
			fmt.Println("LOGS:\n" + fmt.Sprint(w["untrusted_logs"]))
			cleanup()
			fail("job failed")
		}
		if strings.Contains(pr, "Succeeded") {
			done = true
			break
		}
	}
	if !done {
		cleanup()
		fail("job did not finish in 15 minutes")
	}
	fmt.Println("LOGS:\n" + fmt.Sprint(w["untrusted_logs"]))
	time.Sleep(60 * time.Second)
	out := filepath.Join(dir, "out")
	d := call("nrp_data", map[string]any{"action": "down", "run": run, "local": out})
	fmt.Println("DOWN:", d["summary"])
	b, err := os.ReadFile(filepath.Join(out, "hello.txt"))
	if err != nil || string(b) != "results ok\n" {
		cleanup()
		fail("downloaded hello.txt = %q (%v); look for \"nrp-results: FAILED\" in the job log", string(b), err)
	}
	cleanup()
	fmt.Println("STATUS:", call("nrp_status", nil)["summary"])
	fmt.Println("E2E RESULTS OK")
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+f+"\n", a...)
	os.Exit(1)
}
