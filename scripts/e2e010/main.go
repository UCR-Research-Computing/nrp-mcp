// Command e2e010 drives a real nrp-mcp 0.10.0 binary over stdio against Nautilus:
// (1) 20-task CPU sweep with opportunistic=true,
// (2) an R script with install.R,
// (3) a plain http.server web app with NO command= on host nrp010-plain-test (public; check HTTP 200; remove),
// (4) one 1-GPU session (standard nvidia.com/gpu) with a 1 h deadline, ended with nrp_cleanup once nrp_session reports ready.
// Run: go run ./scripts/e2e010 -bin /path/to/nrp-mcp -ns NAMESPACE [-step all|sweep|r|web|gpu|status]
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
	ns := flag.String("ns", "ucr-research-computing", "namespace")
	step := flag.String("step", "all", "step to run: all|sweep|r|web|gpu|status")
	host := flag.String("host", "nrp010-plain-test", "web host name")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2e010", Version: "0"}, nil)
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

	cleanupRun := func(run string) {
		if run == "" {
			return
		}
		c1 := call("nrp_cleanup", map[string]any{"run": run})
		fmt.Println("CLEANUP 1:", c1["summary"])
		if c1["plan_id"] == nil || fmt.Sprint(c1["plan_id"]) == "" {
			return
		}
		c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"]})
		if c2["waiting_for_approval"] == true {
			code := readCode()
			fmt.Println("CLEANUP 2: approving with code from approve.txt (not printed)")
			c3 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "approval_code": code})
			fmt.Println("CLEANUP 3:", c3["summary"])
		} else {
			fmt.Println("CLEANUP 2:", c2["summary"])
		}
	}

	st := call("nrp_status", nil)
	fmt.Println("STATUS:", st["summary"])
	if *step == "status" {
		return
	}

	if *step == "all" || *step == "sweep" {
		fmt.Println("=== (1) 20-task CPU sweep with opportunistic ===")
		sd, _ := os.MkdirTemp("", "nrp-e2e010-sweep-")
		defer os.RemoveAll(sd)
		must(os.WriteFile(filepath.Join(sd, "task.py"), []byte(`import os, platform
idx = int(os.environ["JOB_COMPLETION_INDEX"])
print("sweep task", idx, "on", platform.node())
`), 0o644))
		p := call("nrp_plan", map[string]any{
			"project":         sd,
			"goal":            "sweep",
			"count":           20,
			"parallel":        10,
			"cpu":             "1",
			"memory":          "1Gi",
			"hours":           1,
			"opportunistic":   true,
			"name":            "e2e010-sweep",
			"discard_results": true,
			"show_manifests":  true,
		})
		fmt.Println("SWEEP PLAN:", p["summary"])
		for _, d := range p["decisions"].([]any) {
			fmt.Println("  -", d)
		}
		if p["runnable"] != true {
			fail("sweep not runnable: %v", p["refusals"])
		}
		mb, _ := json.Marshal(p["manifests"])
		if !strings.Contains(string(mb), "nvidia.com/gpu.present") || !strings.Contains(string(mb), "opportunistic") || !strings.Contains(string(mb), "10Gi") {
			fail("sweep manifests missing expected 0.10.0 fields (anti-GPU affinity, opportunistic, 10Gi ephemeral-storage)")
		}
		r := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "confirm_token": p["confirm_token"]})
		run := fmt.Sprint(r["run_id"])
		fmt.Println("SWEEP RUN:", r["summary"])
		start := time.Now()
		var w map[string]any
		ok := false
		for time.Since(start) < 15*time.Minute {
			time.Sleep(15 * time.Second)
			w = call("nrp_watch", map[string]any{"target": run, "tail": 5})
			pr := fmt.Sprint(w["progress"])
			fmt.Printf("SWEEP WATCH +%3.0fs: %s\n", time.Since(start).Seconds(), w["summary"])
			if strings.Contains(pr, "20 Succeeded") {
				ok = true
				break
			}
			if strings.Contains(pr, "Failed") {
				break
			}
		}
		cleanupRun(run)
		if !ok {
			fail("20-task sweep did not reach 20 Succeeded: %v", w["progress"])
		}
		fmt.Println("SWEEP 20 OK")
	}

	if *step == "all" || *step == "r" {
		fmt.Println("=== (2) R script with install.R ===")
		rd, _ := os.MkdirTemp("", "nrp-e2e010-r-")
		defer os.RemoveAll(rd)
		must(os.WriteFile(filepath.Join(rd, "install.R"), []byte(`install.packages("jsonlite", repos="https://cloud.r-project.org")
`), 0o644))
		must(os.WriteFile(filepath.Join(rd, "analysis.R"), []byte(`library(jsonlite)
cat("R jsonlite ok:", toJSON(list(status="ok", n=42), auto_unbox=TRUE), "\n")
`), 0o644))
		p := call("nrp_plan", map[string]any{
			"project":         rd,
			"goal":            "job",
			"cpu":             "1",
			"memory":          "2Gi",
			"hours":           1,
			"name":            "e2e010-r",
			"discard_results": true,
		})
		fmt.Println("R PLAN:", p["summary"])
		hasInstallR := false
		for _, d := range p["decisions"].([]any) {
			fmt.Println("  -", d)
			if strings.Contains(fmt.Sprint(d), "install.R") {
				hasInstallR = true
			}
		}
		if p["runnable"] != true || !hasInstallR {
			fail("R plan not runnable or missing install.R decision: runnable=%v refusals=%v", p["runnable"], p["refusals"])
		}
		r := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "confirm_token": p["confirm_token"]})
		run := fmt.Sprint(r["run_id"])
		fmt.Println("R RUN:", r["summary"])
		start := time.Now()
		var w map[string]any
		ok := false
		for time.Since(start) < 20*time.Minute {
			time.Sleep(20 * time.Second)
			w = call("nrp_watch", map[string]any{"target": run, "tail": 25})
			pr := fmt.Sprint(w["progress"])
			fmt.Printf("R WATCH +%3.0fs: %s\n", time.Since(start).Seconds(), w["summary"])
			if strings.Contains(pr, "Succeeded") {
				ok = true
				break
			}
			if strings.Contains(pr, "Failed") {
				fmt.Println("R LOGS:\n" + fmt.Sprint(w["untrusted_logs"]))
				break
			}
		}
		logs := fmt.Sprint(w["untrusted_logs"])
		fmt.Println("R LOGS:\n" + logs)
		cleanupRun(run)
		if !ok || !strings.Contains(logs, "R jsonlite ok:") {
			fail("R job did not succeed with expected output: ok=%v", ok)
		}
		fmt.Println("R INSTALL.R OK")
	}

	if *step == "all" || *step == "web" {
		fmt.Println("=== (3) Plain http.server web app with NO command= on host " + *host + " ===")
		wd, _ := os.MkdirTemp("", "nrp-e2e010-web-")
		defer os.RemoveAll(wd)
		must(os.WriteFile(filepath.Join(wd, "server.py"), []byte(`from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.end_headers()
        self.wfile.write(b"nrp-mcp 0.10.0 plain http.server ok\n")

if __name__ == "__main__":
    HTTPServer(("0.0.0.0", 8080), H).serve_forever()
`), 0o644))
		// Do NOT pass command= or port= — 0.10.0 must detect both from server.py!
		p := call("nrp_plan", map[string]any{
			"project": wd,
			"goal":    "web",
			"cpu":     "1",
			"memory":  "512Mi",
			"host":    *host,
		})
		rawPlan, _ := json.Marshal(p)
		fmt.Printf("WEB PLAN (%d bytes,manifests=%v): %s\n", len(rawPlan), p["manifests"] != nil, p["summary"])
		for _, d := range p["decisions"].([]any) {
			fmt.Println("  -", d)
		}
		if len(rawPlan) >= 6*1024 || p["manifests"] != nil {
			fail("web plan output should be < 6 KB with no manifests by default; got %d bytes", len(rawPlan))
		}
		if p["runnable"] != true || p["needs_person_approval"] != true {
			fail("web plan not runnable or missing needs_person_approval: %v", p["refusals"])
		}
		r1 := call("nrp_run", map[string]any{"plan_id": p["plan_id"]})
		if r1["waiting_for_approval"] != true {
			fail("web nrp_run step 1 should wait for approval: %v", r1)
		}
		code := readCode()
		fmt.Println("WEB RUN: approving with code from approve.txt (not printed)")
		r2 := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "approval_code": code})
		if r2["started"] != true {
			fail("web nrp_run step 2 did not start: %v", r2)
		}
		run := fmt.Sprint(r2["run_id"])
		fmt.Println("WEB STARTED:", r2["summary"], "URL:", p["public_url"])
		start := time.Now()
		up := false
		for time.Since(start) < 15*time.Minute {
			time.Sleep(15 * time.Second)
			w := call("nrp_watch", map[string]any{"target": run, "tail": 10})
			fmt.Printf("WEB WATCH +%3.0fs: %s\n", time.Since(start).Seconds(), w["summary"])
			if strings.Contains(fmt.Sprint(w["url_check"]), "HTTP 200") {
				up = true
				break
			}
		}
		cleanupRun(run)
		if !up {
			fail("plain web app did not answer HTTP 200 within 15m")
		}
		fmt.Println("PLAIN WEB OK")
	}

	if *step == "all" || *step == "gpu" {
		fmt.Println("=== (4) 1-GPU session (standard nvidia.com/gpu, 1 h deadline) ===")
		p := call("nrp_plan", map[string]any{
			"goal":           "session",
			"gpu":            1,
			"hours":          1,
			"name":           "e2e010-gpusess",
			"show_manifests": true,
		})
		fmt.Println("GPU SESSION PLAN:", p["summary"])
		for _, d := range p["decisions"].([]any) {
			fmt.Println("  -", d)
		}
		if p["runnable"] != true || p["needs_person_approval"] != true {
			fail("GPU session plan not runnable or missing needs_person_approval: %v", p["refusals"])
		}
		mb, _ := json.Marshal(p["manifests"])
		if !strings.Contains(string(mb), `"kind":"Job"`) || !strings.Contains(string(mb), `"activeDeadlineSeconds":3600`) {
			fail("GPU session manifests should be a Job with activeDeadlineSeconds=3600: %s", string(mb))
		}
		r1 := call("nrp_run", map[string]any{"plan_id": p["plan_id"]})
		if r1["waiting_for_approval"] != true {
			fail("GPU session nrp_run step 1 should wait for approval: %v", r1)
		}
		code := readCode()
		fmt.Println("GPU SESSION RUN: approving with code from approve.txt (not printed)")
		r2 := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "approval_code": code})
		if r2["started"] != true {
			fail("GPU session nrp_run step 2 did not start: %v", r2)
		}
		run := fmt.Sprint(r2["run_id"])
		fmt.Println("GPU SESSION STARTED:", r2["summary"])
		start := time.Now()
		ready := false
		var ss map[string]any
		for time.Since(start) < 20*time.Minute {
			time.Sleep(20 * time.Second)
			ss = call("nrp_session", map[string]any{"run": run})
			w := call("nrp_watch", map[string]any{"target": run, "tail": 5})
			fmt.Printf("GPU SESSION +%3.0fs: ready=%v | %s\n", time.Since(start).Seconds(), ss["ready"], w["summary"])
			if ss["ready"] == true {
				ready = true
				break
			}
		}
		fmt.Println("GPU SESSION INFO:", ss["summary"], "command:", ss["command"])
		cleanupRun(run)
		if !ready || !strings.Contains(fmt.Sprint(ss["command"]), "svc/") {
			fail("GPU session did not become ready with svc port-forward: ready=%v cmd=%v", ready, ss["command"])
		}
		fmt.Println("GPU SESSION OK")
	}

	time.Sleep(10 * time.Second)
	stEnd := call("nrp_status", nil)
	fmt.Println("FINAL STATUS:", stEnd["summary"])
	if !strings.Contains(fmt.Sprint(stEnd["summary"]), "0 workloads") {
		fail("workloads remaining after cleanup: %v", stEnd["summary"])
	}
	fmt.Println("E2E 0.10.0 OK")
}

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
