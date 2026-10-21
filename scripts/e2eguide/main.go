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
	yesno := flag.Bool("yesno", false, "act like Hermes Agent: show questions as plain yes/no and answer accept with nothing filled in; approvals then use the code (read from approve.txt, as the person)")
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

	c1 := call("nrp_cleanup", map[string]any{"run": run})
	c2 := call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"]})
	if *yesno {
		if c2["waiting_for_approval"] != true {
			fail("yes/no app: cleanup should wait for a code, got %v", c2)
		}
		fmt.Println("CLEANUP waiting for the person's code (as the person, read approve.txt)")
		home, _ := os.UserHomeDir()
		b, err := os.ReadFile(filepath.Join(home, ".local", "state", "nrp-mcp", "approve.txt"))
		must(err)
		code := ""
		if l, _, ok := strings.Cut(string(b), "\n"); ok {
			code = strings.TrimSpace(strings.TrimPrefix(l, "nrp-mcp approval code:"))
		}
		if code == "" {
			fail("no code in approve.txt")
		}
		c2 = call("nrp_cleanup", map[string]any{"plan_id": c1["plan_id"], "approval_code": code})
	}
	fmt.Println("CLEANUP:", c2["summary"], c2["deleted"])
	if !*yesno && approvals != 1 {
		fail("expected one approval question for cleanup, got %d", approvals)
	}
	if *yesno && approvals != 0 {
		// The empty form answer already told nrp-mcp this app cannot fill in questions,
		// so cleanup goes straight to a code.
		fail("yes/no app: expected no approval question after the empty form answer, got %d", approvals)
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
