package mcpserver

// Guided mode (1.1.0): nrp_plan asks the person for missing details with a short form in
// the AI app (MCP elicitation), and nrp_guide tells the assistant the one next step from
// the person's real state. Both are deterministic code: no model calls.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/inspect"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/kube"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/ops"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/plan"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/results"
	nrpsetup "github.com/UCR-Research-Computing/nrp-mcp/internal/setup"
)

// planFormKey names the details form in InputRequests.
const planFormKey = "details"

// Form field names. Each maps to one nrp_plan input.
const (
	fProject = "project"
	fGoal    = "goal"
	fFile    = "file"
	fGPU     = "gpu"
	fCount   = "count"
	fResults = "results"
)

// field is one question in the details form.
type field struct {
	name     string
	schema   map[string]any
	optional bool
}

func choice(title, desc string, opts ...[2]string) map[string]any {
	var one []map[string]any
	for _, o := range opts {
		one = append(one, map[string]any{"const": o[0], "title": o[1]})
	}
	return map[string]any{"type": "string", "title": title, "description": desc, "oneOf": one}
}

// planQuestions works out which details nrp_plan still needs and, when the AI app can
// show a form, asks the person for all of them at once. It returns:
//   - ask != nil: return it from the tool; the app shows the form and calls again.
//   - answered: one plain line per answer, added to the plan's decisions.
//
// When the app cannot show forms, or the person declines, nothing is asked and nrp_plan
// carries on as before (its refusals and `next` say what is missing).
func (s *Server) planQuestions(ctx context.Context, req *mcp.CallToolRequest, in *planIn) (*mcp.CallToolResult, []string, error) {
	if req == nil || req.Params == nil {
		return nil, nil, nil
	}
	if len(req.Params.InputResponses) > 0 {
		if _, ok := req.Params.InputResponses[planFormKey]; ok {
			if !formAsked.take(req.Params.RequestState, formKey(in)) {
				return nil, nil, errors.New("that answer does not match a question nrp-mcp asked, so nothing was planned. Call nrp_plan again without it")
			}
			return nil, applyAnswers(in, req.Params.InputResponses[planFormKey]), nil
		}
		// Other responses (an approval answer) are not ours.
		return nil, nil, nil
	}
	if !canAsk(req.Session) {
		return nil, nil, nil
	}
	fields := s.missingDetails(in)
	if len(fields) == 0 {
		return nil, nil, nil
	}
	props := map[string]any{}
	var required []string
	var lines []string
	for _, f := range fields {
		props[f.name] = f.schema
		if !f.optional {
			required = append(required, f.name)
		}
		lines = append(lines, "- "+fmt.Sprint(f.schema["title"]))
	}
	msg := "nrp-mcp needs a few details before it can plan this. Nothing runs yet: you will see the plan first.\n\n" + strings.Join(lines, "\n")
	schema := map[string]any{"type": "object", "properties": props, "required": required}
	st := formAsked.issue(formKey(in))
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{planFormKey: &mcp.ElicitParams{Message: msg, RequestedSchema: schema}}, RequestState: st}, nil, nil
}

// formKey binds a form to the request that asked it (project + goal as given).
func formKey(in *planIn) approval {
	return approval{PlanID: "plan|" + in.Project + "|" + in.Goal, Action: "details"}
}

// formAsked tracks issued detail forms, like approval questions (single use, 10 minutes).
var formAsked = &questions{m: map[string]askedQ{}}

// missingDetails lists the questions to ask, in order. It reads the project folder but
// never changes anything.
func (s *Server) missingDetails(in *planIn) []field {
	var out []field
	if in.Project == "" && in.Goal != plan.GoalVolume && in.Goal != plan.GoalPull && in.Goal != plan.GoalSession && in.Image == "" {
		out = append(out, field{name: fProject, schema: map[string]any{"type": "string", "title": "Which folder (or script) on this computer holds your code?",
			"description": "The full path, for example /Users/you/projects/my-analysis. nrp-mcp reads it here; nothing is uploaded until you approve a plan.", "minLength": 1}})
	}
	var f *inspect.Facts
	if in.Project != "" {
		if got, err := inspect.Dir(in.Project); err == nil {
			f = got
		}
	}
	goal := in.Goal
	if goal == "" && f != nil && f.WebApp != "" {
		goal = plan.GoalWeb // a web app is planned as one unless the person says otherwise
	}
	if goal == "" {
		out = append(out, field{name: fGoal, schema: choice("What do you want to do?", "Pick the closest one.",
			[2]string{plan.GoalJob, "Run it once (a job)"},
			[2]string{plan.GoalSweep, "Run it many times with different settings (a sweep)"},
			[2]string{plan.GoalWeb, "Publish it as a public web app"},
			[2]string{plan.GoalSession, "Open an interactive Jupyter notebook"})})
	}
	if in.Command == "" && f != nil && f.Entry == "" && len(f.Candidates) > 1 && goal != plan.GoalSession {
		var opts [][2]string
		for _, c := range f.Candidates {
			if plan.CommandForFile(c) != "" {
				opts = append(opts, [2]string{c, c})
			}
		}
		if len(opts) > 1 {
			out = append(out, field{name: fFile, schema: choice("Which file starts it?", "nrp-mcp found several scripts at the top of the folder.", opts...)})
		}
	}
	if in.GPU == nil && goal != plan.GoalWeb && goal != plan.GoalVolume && goal != plan.GoalPull && in.Image == "" {
		if f == nil || !f.UsesGPU {
			out = append(out, field{name: fGPU, schema: choice("Does it need a GPU?", "Only say yes if your code really uses one: idle GPUs get accounts suspended on Nautilus.",
				[2]string{"no", "No, CPU is fine"},
				[2]string{"yes", "Yes, one GPU"})})
		}
	}
	if (goal == plan.GoalSweep || goal == "") && in.Count <= 0 {
		title := "How many tasks (runs) in the sweep?"
		if goal == "" {
			title = "If you picked a sweep: how many tasks (runs)?"
		}
		out = append(out, field{name: fCount, optional: goal == "", schema: map[string]any{"type": "integer", "title": title,
			"description": "Each task gets its own number in $JOB_COMPLETION_INDEX (0, 1, 2, ...) to pick its settings.", "minimum": 1, "maximum": 10000}})
	}
	if (goal == "" || goal == plan.GoalJob || goal == plan.GoalSweep) && !in.Discard && in.Image == "" {
		if _, err := results.LoadKeys(s.Cfg.S3KeysFile); err != nil {
			out = append(out, field{name: fResults, schema: choice("Saving results is not set up yet. What should happen to files the job writes?",
				"To keep them, stop here and run `nrp-mcp s3 setup` in a terminal once (keys from https://nrp.ai/s3token/), then ask again.",
				[2]string{"setup", "Stop: I will set up saving results first"},
				[2]string{"discard", "Run anyway and discard them (it only prints to the logs)"})})
		}
	}
	return out
}

// applyAnswers copies the person's form answers into the plan inputs and returns one
// plain line per answer.
func applyAnswers(in *planIn, resp mcp.InputResponse) []string {
	er, _ := resp.(*mcp.ElicitResult)
	if er == nil || er.Action != "accept" {
		return []string{"The person skipped the details form; planned with what was given."}
	}
	var lines []string
	if in.answers == nil {
		in.answers = map[string]any{}
	}
	str := func(k string) string { v, _ := er.Content[k].(string); return strings.TrimSpace(v) }
	if v := str(fProject); v != "" {
		in.Project = expandHome(v)
		in.answers["project"] = in.Project
		lines = append(lines, "Folder (the person's answer): "+in.Project+".")
	}
	if v := str(fGoal); v != "" {
		in.Goal = v
		in.answers["goal"] = v
		lines = append(lines, "Goal (the person's answer): "+v+".")
	}
	if v := str(fFile); v != "" {
		if c := plan.CommandForFile(v); c != "" {
			in.Command = c
			in.answers["command"] = c
			lines = append(lines, "Starts with "+v+" (the person's answer).")
		}
	}
	switch str(fGPU) {
	case "no":
		zero := 0
		in.GPU = &zero
		in.answers["gpu"] = 0
		lines = append(lines, "CPU only (the person's answer).")
	case "yes":
		one := 1
		in.GPU = &one
		in.answers["gpu"] = 1
		lines = append(lines, "One GPU (the person's answer).")
	}
	if n, ok := er.Content[fCount].(float64); ok && n > 0 {
		in.Count = int(n)
		in.answers["count"] = in.Count
		lines = append(lines, fmt.Sprintf("%d tasks (the person's answer).", in.Count))
	}
	switch str(fResults) {
	case "discard":
		in.Discard = true
		in.answers["discard_results"] = true
		lines = append(lines, "The person said yes, discard results.")
	case "setup":
		lines = append(lines, "The person will set up saving results first (`nrp-mcp s3 setup`), so this plan is not runnable yet.")
	}
	return lines
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// ---- nrp_guide ----

type guideIn struct {
	Namespace string `json:"namespace,omitempty"`
	Project   string `json:"project,omitempty" jsonschema:"the project folder the person is working on, if known"`
	Run       string `json:"run,omitempty" jsonschema:"the run id the person is working on, if any"`
}

// GuideStep is one item of the checklist.
type GuideStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // done, todo, now, skipped
	Detail string `json:"detail,omitempty"`
}

type guideOut struct {
	Summary  string      `json:"summary"`
	Stage    string      `json:"stage"`
	Steps    []GuideStep `json:"steps"`
	Ask      string      `json:"ask_the_person"`
	Next     string      `json:"next"`
	Terminal string      `json:"terminal_command,omitempty" jsonschema:"a command the PERSON runs in their own terminal (never run it for them)"`
}

// guide stages, in order.
const (
	stageLaptop    = "laptop"
	stageSignIn    = "sign-in"
	stageNamespace = "namespace"
	stageResults   = "results"
	stageProject   = "project"
	stageRunning   = "running"
	stageFailed    = "failed"
	stageFinished  = "finished"
	stageCleanup   = "cleanup"
)

func (s *Server) guideTool(ctx context.Context, _ *mcp.CallToolRequest, in guideIn) (*mcp.CallToolResult, *guideOut, error) {
	out := &guideOut{}
	steps := []GuideStep{
		{ID: stageLaptop, Title: "This computer has kubectl, the sign-in plugin and the NRP config"},
		{ID: stageSignIn, Title: "Signed in to Nautilus"},
		{ID: stageNamespace, Title: "In a namespace"},
		{ID: stageResults, Title: "Saving results set up (nrp-mcp s3 setup)"},
		{ID: stageProject, Title: "Plan and start a run"},
		{ID: "watch", Title: "Watch it and get the results"},
		{ID: stageCleanup, Title: "Clean up when finished"},
	}
	mark := func(id, status, detail string) {
		for i := range steps {
			if steps[i].ID == id {
				steps[i].Status, steps[i].Detail = status, detail
			}
		}
	}
	done := func(stage, summary, ask, next, term string) (*mcp.CallToolResult, *guideOut, error) {
		seen := false
		for i := range steps {
			switch {
			case steps[i].ID == stage || (stage == stageRunning || stage == stageFailed || stage == stageFinished) && steps[i].ID == "watch":
				steps[i].Status, seen = "now", true
			case steps[i].Status != "":
			case !seen:
				steps[i].Status = "done"
			default:
				steps[i].Status = "todo"
			}
		}
		out.Stage, out.Summary, out.Ask, out.Next, out.Terminal, out.Steps = stage, summary, ask, next, term, steps
		return nil, out, nil
	}

	// 1. Laptop: kubectl, kubelogin, NRP config (read-only check, no sign-in).
	var e *nrpsetup.Env
	if s.Setup != nil {
		e = s.Setup()
	} else {
		e = nrpsetup.DefaultEnv(s.Cfg.Kubeconfig, s.Cfg.Context, s.Cfg.Kubectl)
	}
	if rep := e.Inspect(ctx, false); rep != nil {
		var missing []string
		for _, c := range rep.Checks {
			if c.Status != nrpsetup.OK && c.Status != nrpsetup.Note && c.ID != "signin" {
				missing = append(missing, c.Title)
			}
		}
		if len(missing) > 0 {
			return done(stageLaptop,
				"This computer is not ready for Nautilus yet: "+strings.Join(missing, ", ")+" missing.",
				"Shall I set up this computer for Nautilus? It installs kubectl and the sign-in plugin into your user folder (no admin rights, checksums verified) and puts your NRP config in place. If you have not downloaded the config yet, get it first at https://nrp.ai/config (sign in, then Get Config).",
				"If the person agrees, call nrp_setup with fix=true (it asks for their approval). Then call nrp_guide again.",
				"nrp-mcp setup")
		}
	}

	// 2. Sign-in and 3. namespace.
	w, err := s.Ops.K.WhoAmI(ctx)
	if err != nil {
		k := kube.Kind(err)
		if k == kube.KindSignedOut || k == kube.KindOther || k == kube.KindTimeout {
			return done(stageSignIn,
				"Not signed in to Nautilus (or the sign-in expired).",
				"Please sign in to Nautilus: run the command below in your own terminal. A browser tab opens for the UCR sign-in. Tell me when it says you are signed in.",
				"Wait for the person to say they signed in, then call nrp_guide again. Never run the command for them.",
				"nrp-mcp kubectl auth whoami")
		}
		return nil, nil, ops.SignInHint(err)
	}
	ns := s.nsCtx(ctx, in.Namespace)
	if ns == "" && len(w.Namespaces) > 0 {
		ns = w.Namespaces[0]
	}
	if ns == "" {
		return done(stageNamespace,
			"Signed in, but not in any namespace yet.",
			"You are signed in, but not in a namespace. Students: send your PI the email shown on https://nrp.ai and ask them to add you at https://nrp.ai/namespaces. Faculty, staff and postdocs: request a namespace there. Tell me when you have been added.",
			"Wait for the person; then call nrp_guide again.", "")
	}
	mark(stageNamespace, "done", ns)

	// 4. Results.
	if _, err := results.LoadKeys(s.Cfg.S3KeysFile); err != nil {
		mark(stageResults, "now", "")
		return done(stageResults,
			"Ready to run in "+ns+", but saving results is not set up: files a job writes would be lost when it ends.",
			"One more setup step, about 2 minutes: open https://nrp.ai/s3token/ to get your S3 access key and secret key, then run the command below in your own terminal and paste them (the secret is hidden). Tell me when it is done. Or, if your job only prints to the screen, we can skip this.",
			"Wait for the person. If they did it, call nrp_guide again. If they want to skip it, go on to planning: nrp_plan will ask them to confirm discarding results.",
			"nrp-mcp s3 setup")
	}
	mark(stageResults, "done", "")

	// 5-7. Runs.
	sc, _, err := s.cleanupScope(ctx, ns, false)
	if err != nil {
		return nil, nil, err
	}
	runs, err := s.Ops.CleanupList(ctx, ns, "", sc)
	if err != nil {
		return nil, nil, err
	}
	var focus *ops.Workload
	var all []ops.Workload
	for i := range runs {
		if runs[i].Kind != "Job" && runs[i].Kind != "Deployment" {
			continue
		}
		all = append(all, runs[i])
		if in.Run != "" && runs[i].Run == in.Run {
			focus = &runs[i]
		}
	}
	if in.Run == "" && len(all) == 1 {
		focus = &all[0]
	}
	if focus == nil && len(all) > 1 {
		var names []string
		for _, r := range all {
			names = append(names, fmt.Sprintf("%s (%s, run %s)", r.Name, strings.TrimSpace(r.Status), r.Run))
		}
		return done(stageRunning,
			fmt.Sprintf("You have %d runs in %s: %s.", len(all), ns, strings.Join(names, "; ")),
			"Which run do you want to look at, or do you want to start something new?",
			"Call nrp_guide again with run=<id> for the one they pick, or go to planning (nrp_plan) for something new.", "")
	}
	if focus == nil {
		mark(stageProject, "now", "")
		ask := "Everything is set up. What would you like to run on Nautilus? Tell me the folder your code is in and what it should do (run once, run many times with different settings, publish a web app, or open a notebook)."
		if in.Project != "" {
			ask = "Everything is set up. Shall I plan " + in.Project + " for Nautilus? You will see the plan before anything starts."
		}
		return done(stageProject,
			"Ready: signed in as you in "+ns+", results are saved, nothing running.",
			ask,
			"Call nrp_plan with the folder (it asks the person for anything missing), explain the plan in plain words, and only after their yes call nrp_run.", "")
	}
	st := strings.ToLower(focus.Status)
	switch {
	case focus.Kind == "Job" && strings.HasPrefix(st, "done"):
		return done(stageFinished,
			fmt.Sprintf("Run %s (%s) finished: %s.", focus.Run, focus.Name, focus.Status),
			"Your run finished. Shall I download its results to your computer, and then clean it up?",
			fmt.Sprintf("If yes: nrp_data action=down run=%s, tell the person where the files are, then nrp_cleanup run=%s (deleting asks for their approval).", focus.Run, focus.Run), "")
	case focus.Kind == "Job" && strings.HasPrefix(st, "failed"):
		return done(stageFailed,
			fmt.Sprintf("Run %s (%s) failed: %s.", focus.Run, focus.Name, focus.Status),
			"Your run failed. Shall I look at why and suggest a fix?",
			fmt.Sprintf("Call nrp_watch target=%s, explain the diagnosis in plain words, offer the fixed plan (nrp_plan), and offer nrp_cleanup run=%s.", focus.Run, focus.Run), "")
	case focus.Kind == "Deployment":
		return done(stageRunning,
			fmt.Sprintf("%s (run %s) is running: %s.", focus.Name, focus.Run, focus.Status),
			"Your app is up. Do you want to check on it, update it, or take it down?",
			fmt.Sprintf("Check: nrp_watch target=%s. Update: nrp_plan again with the same name. Take down: nrp_cleanup run=%s.", focus.Run, focus.Run), "")
	}
	return done(stageRunning,
		fmt.Sprintf("Run %s (%s) is still going: %s.", focus.Run, focus.Name, focus.Status),
		"Your run is still going. Shall I check how it is doing?",
		fmt.Sprintf("Call nrp_watch target=%s and tell the person the state in plain words. Waiting for an image download is normal (up to 10-20 minutes the first time).", focus.Run), "")
}
