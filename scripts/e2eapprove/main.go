// Command e2eapprove drives a real nrp-mcp over stdio against Nautilus to test the 0.9.0
// person-approval flow on a public web app: the plan says needs_person_approval and has no
// confirm token, the first nrp_run waits for approval, the run with the code starts, the URL
// answers 200, cleanup step 2 waits for its own code, and the namespace ends with 0 workloads.
// The server runs with NRP_CONSENT=code; in a live test this script plays the person and reads
// the code from approve.txt (an assistant must never do that).
// Run: go run ./scripts/e2eapprove -bin /path/to/nrp-mcp -ns NAMESPACE -project DIR -host NAME
// Creates a PUBLIC endpoint for a few minutes: only with the namespace admin's go.
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
	project := flag.String("project", "", "web project folder")
	host := flag.String("host", "", "web host name")
	port := flag.Int("port", 8080, "app port")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2eapprove", Version: "0"}, nil)
	cmd := exec.Command(*bin, "serve", "--namespace", *ns)
	cmd.Env = append(os.Environ(), "NRP_CONSENT=code")
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

	fmt.Println("STATUS:", call("nrp_status", nil)["summary"])
	p := call("nrp_plan", map[string]any{"project": *project, "goal": "web", "command": "python app.py",
		"cpu": "1", "memory": "512Mi", "gpu": 0, "port": *port, "host": *host})
	fmt.Println("PLAN:", p["summary"])
	if p["runnable"] != true {
		fail("not runnable: %v", p["refusals"])
	}
	if p["needs_person_approval"] != true {
		fail("web plan should say needs_person_approval")
	}
	if p["confirm_token"] != nil {
		fail("web plan must not return a confirm_token under consent=code")
	}
	fmt.Println("URL:", p["public_url"])

	r1 := call("nrp_run", map[string]any{"plan_id": p["plan_id"]})
	fmt.Println("RUN 1:", r1["summary"])
	if r1["waiting_for_approval"] != true || r1["started"] == true {
		fail("first nrp_run should wait for approval and start nothing")
	}
	code := readCode()
	fmt.Println("RUN 1: got an approval code from approve.txt (not printed)")
	r2 := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "approval_code": code})
	if r2["started"] != true {
		fail("nrp_run with the code did not start: %v", r2["summary"])
	}
	run := fmt.Sprint(r2["run_id"])
	fmt.Println("RUN 2:", r2["summary"])

	start := time.Now()
	last := ""
	up := false
	for time.Since(start) < 15*time.Minute {
		w := call("nrp_watch", map[string]any{"target": run, "tail": 5})
		line := fmt.Sprint(w["summary"])
		if line != last {
			fmt.Printf("WATCH +%3.0fs: %s\n", time.Since(start).Seconds(), line)
			last = line
		}
		if strings.Contains(fmt.Sprint(w["url_check"]), "HTTP 200") {
			up = true
			break
		}
		time.Sleep(20 * time.Second)
	}
	if !up {
		fail("not up after 15 minutes (run %s still exists; clean it up)", run)
	}
	fmt.Println("UP:", p["public_url"])

	c1 := call("nrp_cleanup", map[string]any{"run": run})
	fmt.Println("CLEANUP 1:", c1["summary"])
	if c1["confirm_token"] != nil {
		fail("cleanup step 1 must not return a token under consent=code")
	}
	c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"]})
	fmt.Println("CLEANUP 2:", c2["summary"])
	if c2["waiting_for_approval"] != true {
		fail("cleanup step 2 should wait for approval")
	}
	c3 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "approval_code": readCode()})
	fmt.Println("CLEANUP 3:", c3["summary"])
	time.Sleep(10 * time.Second)
	st := call("nrp_status", nil)
	fmt.Println("STATUS:", st["summary"])
	if !strings.Contains(fmt.Sprint(st["summary"]), "0 workloads") {
		fail("workloads left after cleanup")
	}
	fmt.Println("E2E APPROVE OK")
}

// readCode reads the newest approval code the server wrote for the person.
func readCode() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		h, _ := os.UserHomeDir()
		dir = filepath.Join(h, ".local", "state")
	}
	b, err := os.ReadFile(filepath.Join(dir, "nrp-mcp", "approve.txt"))
	must(err)
	const pre = "nrp-mcp approval code: "
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, pre) {
			c := strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(l, pre)), "-", "")
			if len(c) == 6 {
				return c
			}
		}
	}
	fail("no approval code in approve.txt")
	return ""
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
