// Command e2e drives a real nrp-mcp over stdio against Nautilus: status, plan, run a
// tiny CPU job, watch until done, cleanup in two steps, confirm the namespace is clean.
// Run: go run ./scripts/e2e -bin /path/to/nrp-mcp -ns NAMESPACE
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil)
	cs, err := cl.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(*bin, "serve", "--namespace", *ns)}, nil)
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

	st := call("nrp_status", nil)
	fmt.Println("STATUS:", st["summary"])

	dir, _ := os.MkdirTemp("", "nrp-e2e-")
	defer os.RemoveAll(dir)
	_ = os.WriteFile(filepath.Join(dir, "hello.py"), []byte(`import platform, os
total = sum(i*i for i in range(2_000_000))
print("hello from", platform.node(), "run", os.environ.get("NRP_RUN"), "sum", total)
`), 0o644)

	p := call("nrp_plan", map[string]any{"project": dir, "cpu": "1", "memory": "1Gi", "hours": 1, "name": "e2e-hello"})
	fmt.Println("PLAN:", p["summary"])
	fmt.Println("  runnable:", p["runnable"], " image:", p["image"])
	for _, d := range p["decisions"].([]any) {
		fmt.Println("  -", d)
	}
	if w, ok := p["warnings"].([]any); ok {
		for _, x := range w {
			fmt.Println("  WARN", x.(map[string]any)["id"], x.(map[string]any)["message"])
		}
	}
	if p["runnable"] != true {
		fail("not runnable: %v", p["refusals"])
	}

	r := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "confirm_token": p["confirm_token"]})
	run := r["run_id"].(string)
	fmt.Println("RUN:", r["summary"], "\n  applied:", strings.ReplaceAll(fmt.Sprint(r["applied"]), "\n", "; "), "\n  run card:", r["run_card"])

	var w map[string]any
	for i := 0; i < 40; i++ {
		time.Sleep(10 * time.Second)
		w = call("nrp_watch", map[string]any{"target": run, "tail": 10})
		fmt.Printf("WATCH %d: %v\n", i, w["summary"])
		if strings.Contains(fmt.Sprint(w["progress"]), "Succeeded") || strings.Contains(fmt.Sprint(w["progress"]), "Failed") {
			break
		}
	}
	fmt.Println("LOGS:\n" + fmt.Sprint(w["logs"]))

	c1 := call("nrp_cleanup", map[string]any{"run": run})
	fmt.Println("CLEANUP 1:", c1["summary"])
	if c1["confirm_token"] == nil {
		fail("no cleanup token")
	}
	c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "confirm_token": c1["confirm_token"]})
	fmt.Println("CLEANUP 2:", c2["summary"], "|", strings.ReplaceAll(fmt.Sprint(c2["deleted"]), "\n", "; "))

	time.Sleep(5 * time.Second)
	c3 := call("nrp_cleanup", map[string]any{"run": run})
	fmt.Println("AFTER:", c3["summary"])
	// Sweep: 3 tasks, 3 at a time, CPU only.
	sd, _ := os.MkdirTemp("", "nrp-e2e-sweep-")
	defer os.RemoveAll(sd)
	_ = os.WriteFile(filepath.Join(sd, "task.py"), []byte("import os\ni=int(os.environ['JOB_COMPLETION_INDEX'])\nprint('task', i, 'alpha', [0.1,0.5,0.9][i])\n"), 0o644)
	swp := call("nrp_plan", map[string]any{"project": sd, "goal": "sweep", "count": 3, "parallel": 3, "cpu": "1", "memory": "1Gi", "hours": 1, "name": "e2e-sweep"})
	fmt.Println("SWEEP PLAN:", swp["summary"], "runnable:", swp["runnable"])
	if swp["runnable"] != true {
		fail("sweep not runnable: %v", swp["refusals"])
	}
	swr := call("nrp_run", map[string]any{"plan_id": swp["plan_id"], "confirm_token": swp["confirm_token"]})
	srun := swr["run_id"].(string)
	var sw map[string]any
	for i := 0; i < 40; i++ {
		time.Sleep(10 * time.Second)
		sw = call("nrp_watch", map[string]any{"target": srun, "tail": 5})
		if strings.Contains(fmt.Sprint(sw["progress"]), "3 Succeeded") || strings.Contains(fmt.Sprint(sw["progress"]), "Failed") {
			break
		}
	}
	fmt.Println("SWEEP WATCH:", sw["summary"])
	sc1 := call("nrp_cleanup", map[string]any{"run": srun})
	sc2 := call("nrp_cleanup", map[string]any{"plan_id": sc1["plan_id"], "confirm_token": sc1["confirm_token"]})
	fmt.Println("SWEEP CLEANUP:", sc2["summary"])
	if !strings.Contains(fmt.Sprint(sw["progress"]), "3 Succeeded") {
		fail("sweep did not finish 3 tasks: %v", sw["progress"])
	}

	// Web: plan only. Check suggestions and that nrp_run refuses without public_ack.
	wd, _ := os.MkdirTemp("", "nrp-e2e-web-")
	defer os.RemoveAll(wd)
	_ = os.WriteFile(filepath.Join(wd, "app.py"), []byte("import streamlit as st\nst.title('Leaf area explorer')\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wd, "requirements.txt"), []byte("streamlit\n"), 0o644)
	wp := call("nrp_plan", map[string]any{"project": wd, "goal": "web"})
	fmt.Println("WEB PLAN:", wp["summary"])
	fmt.Println("  suggested:", wp["suggested_names"], " public_url:", wp["public_url"])
	r2, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "nrp_run", Arguments: map[string]any{"plan_id": wp["plan_id"], "confirm_token": wp["confirm_token"]}})
	must(err)
	if !r2.IsError {
		fail("web run without public_ack was accepted")
	}
	fmt.Println("  run without public_ack refused: ok")
	fmt.Println("E2E OK")
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "E2E FAIL: "+f+"\n", a...)
	os.Exit(1)
}
