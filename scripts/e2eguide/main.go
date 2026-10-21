// Command e2eguide drives a real nrp-mcp over stdio against Nautilus to test 1.1.0 guided
// mode: nrp_guide reports the stage, nrp_plan asks for missing details in a form (this
// client answers it like a person would: which file, CPU only), the CPU job runs, nrp_guide
// sees it finish, results come back, and cleanup is approved through the question.
// CPU only, nothing public. Needs `nrp-mcp s3 setup` first.
// Run: go run ./scripts/e2eguide -bin /path/to/nrp-mcp -ns NAMESPACE
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
	yesno := flag.Bool("yesno", false, "act like Hermes Agent: show questions as plain yes/no and answer accept with nothing filled in (the person pressed yes in the app's prompt)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	forms, approvals := 0, 0
	elicit := func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		b, _ := json.Marshal(req.Params.RequestedSchema)
		if *yesno {
			if strings.Contains(string(b), `"approve"`) {
				approvals++
			} else {
				forms++
			}
			fmt.Println("YES/NO PROMPT (answered accept, no content):", firstLine(req.Params.Message))
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}, nil
		}
		if strings.Contains(string(b), `"approve"`) {
			approvals++
			fmt.Println("QUESTION (approval):", firstLine(req.Params.Message))
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
		}
		forms++
		fmt.Println("FORM:", strings.ReplaceAll(req.Params.Message, "\n", " | "))
		fmt.Println("FORM FIELDS:", string(b))
		ans := map[string]any{}
		if strings.Contains(string(b), `"file"`) {
			ans["file"] = "fit.py"
		}
		if strings.Contains(string(b), `"gpu"`) {
			ans["gpu"] = "no"
		}
		if strings.Contains(string(b), `"goal"`) {
			ans["goal"] = "job"
		}
		if strings.Contains(string(b), `"results"`) {
			ans["results"] = "discard"
		}
		fmt.Println("FORM ANSWER:", ans)
		return &mcp.ElicitResult{Action: "accept", Content: ans}, nil
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "e2eguide", Version: "0"}, &mcp.ClientOptions{ElicitationHandler: elicit})
	cmd := exec.Command(*bin, "serve", "--namespace", *ns)
	cmd.Env = append(os.Environ(), "NRP_CONSENT=ask")
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

	g := call("nrp_guide", nil)
	fmt.Printf("GUIDE 1: stage=%v | %v\n  ask: %v\n", g["stage"], g["summary"], g["ask_the_person"])
	if g["stage"] != "project" {
		fail("expected stage project with nothing running, got %v", g["stage"])
	}

	dir, _ := os.MkdirTemp("", "nrp-e2eguide-")
	defer os.RemoveAll(dir)
	must(os.WriteFile(filepath.Join(dir, "prep.py"), []byte("print('prep')\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "fit.py"), []byte(`import os
os.makedirs(os.environ["RESULTS_DIR"], exist_ok=True)
open(os.path.join(os.environ["RESULTS_DIR"], "fit.txt"), "w").write("guided ok\n")
print("fit done")
`), 0o644))

	p := call("nrp_plan", map[string]any{"project": dir, "cpu": "1", "memory": "1Gi", "hours": 1, "name": "e2e-guide"})
	fmt.Println("PLAN:", p["summary"])
	if *yesno {
		fmt.Println("NEXT (chat question):", p["next"])
		if p["runnable"] == true || !strings.Contains(fmt.Sprint(p["next"]), "Which file") {
			fail("yes/no app: expected the chat question, got %v", p)
		}
		// The person answers in the chat: fit.py, CPU only.
		p = call("nrp_plan", map[string]any{"project": dir, "cpu": "1", "memory": "1Gi", "hours": 1, "name": "e2e-guide", "command": "python fit.py", "gpu": 0, "goal": "job"})
		fmt.Println("PLAN (after chat answer):", p["summary"])
		forms = 1
	}
	if forms != 1 {
		fail("expected exactly one details form, got %d", forms)
	}
	if p["runnable"] != true {
		fail("not runnable: %v | next: %v", p["refusals"], p["next"])
	}
	for _, d := range p["decisions"].([]any) {
		if s := fmt.Sprint(d); strings.Contains(s, "person's answer") {
			fmt.Println("  -", s)
		}
	}
	fmt.Println("ANSWERS TO REUSE:", p["answers_to_reuse"])
	if !strings.Contains(fmt.Sprint(p["summary"]), "fit.py") || !strings.Contains(fmt.Sprint(p["summary"]), "no GPU") {
		fail("form answers not applied: %v", p["summary"])
	}

	r := call("nrp_run", map[string]any{"plan_id": p["plan_id"], "confirm_token": p["confirm_token"]})
	run := fmt.Sprint(r["run_id"])
	fmt.Println("RUN:", r["summary"])

	deadline := time.Now().Add(15 * time.Minute)
	for {
		g = call("nrp_guide", map[string]any{"run": run})
		fmt.Printf("GUIDE: stage=%v | %v\n", g["stage"], g["summary"])
		if g["stage"] == "finished" || g["stage"] == "failed" {
			break
		}
		if time.Now().After(deadline) {
			fail("run did not finish in 15 minutes")
		}
		time.Sleep(20 * time.Second)
	}
	fmt.Println("  ask:", g["ask_the_person"])
	fmt.Println("  next:", g["next"])
	if g["stage"] != "finished" {
		w := call("nrp_watch", map[string]any{"target": run})
		fail("run failed: %v", w["summary"])
	}

	down := filepath.Join(dir, "out")
	time.Sleep(10 * time.Second)
	d := call("nrp_data", map[string]any{"action": "down", "run": run, "local": down})
	fmt.Println("DOWN:", d["summary"])
	if b, err := os.ReadFile(filepath.Join(down, "fit.txt")); err != nil || !strings.Contains(string(b), "guided ok") {
		fail("results not downloaded: %v", err)
	}

	// 1.1.2: a finished run with no volumes loses nothing, so cleanup needs only the
	// person's yes in the chat (a confirm token), no approval question or code.
	c1 := call("nrp_cleanup", map[string]any{"run": run})
	fmt.Println("CLEANUP STEP 1:", c1["summary"])
	if c1["confirm_token"] == nil || c1["needs_person_approval"] == true {
		fail("finished run should be a low-risk cleanup with a confirm token, got %v", c1)
	}
	c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "confirm_token": c1["confirm_token"]})
	fmt.Println("CLEANUP (chat yes):", c2["summary"])
	if approvals != 0 {
		fail("a finished-run cleanup must not ask an approval question, got %d", approvals)
	}

	// A still-running job is not low risk: removing it needs the person's approval in the
	// app's own prompt. A yes/no app answers accept with nothing filled in; that is the
	// person's yes (1.1.2).
	must(os.WriteFile(filepath.Join(dir, "fit.py"), []byte("import time\nprint('waiting')\ntime.sleep(600)\n"), 0o644))
	sp := call("nrp_plan", map[string]any{"project": dir, "cpu": "1", "memory": "1Gi", "hours": 1, "name": "e2e-guide-run", "command": "python fit.py", "gpu": 0, "goal": "job"})
	if sp["runnable"] != true {
		fail("sleep plan not runnable: %v | %v", sp["refusals"], sp["next"])
	}
	sr := call("nrp_run", map[string]any{"plan_id": sp["plan_id"], "confirm_token": sp["confirm_token"]})
	srun := fmt.Sprint(sr["run_id"])
	fmt.Println("RUN 2 (keeps running):", sr["summary"])
	time.Sleep(15 * time.Second)
	d1 := call("nrp_cleanup", map[string]any{"run": srun})
	fmt.Println("CLEANUP 2 STEP 1:", d1["summary"])
	if d1["confirm_token"] != nil || d1["needs_person_approval"] != true {
		fail("a running job must need approval, got %v", d1)
	}
	d2 := call("nrp_cleanup", map[string]any{"plan_id": d1["plan_id"]})
	fmt.Println("CLEANUP 2:", d2["summary"])
	if approvals != 1 || d2["waiting_for_approval"] == true || d2["deleted"] == nil {
		fail("expected one approval question answered in the app and the run deleted: approvals=%d %v", approvals, d2)
	}
	g = call("nrp_guide", nil)
	fmt.Printf("GUIDE END: stage=%v | %v\n", g["stage"], g["summary"])
	fmt.Println("STATUS:", call("nrp_status", nil)["summary"])
	fmt.Println("E2E GUIDE OK")
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "nrp-mcp asks") {
			return l
		}
	}
	return s
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
