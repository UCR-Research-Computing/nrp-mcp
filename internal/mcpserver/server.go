// Package mcpserver exposes nrp-mcp's nine tools, resources and prompts over MCP.
package mcpserver

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/jsonschema-go/jsonschema"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/config"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/inspect"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/knowledge"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/kube"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/ops"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/plan"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/rules"
	nrpsetup "github.com/UCR-Research-Computing/nrp-mcp/internal/setup"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/store"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/version"
)

// Instructions tell a model how to use the server.
const Instructions = `nrp: run research work on the National Research Platform's Nautilus cluster, as the
signed-in researcher, safely.

Always: start with nrp_status (if it says kubectl, the sign-in plugin or the NRP config is
missing, use nrp_setup: check first, then fix=true after the user agrees). Before anything runs, call nrp_plan and SHOW THE USER the
summary, decisions and any refusals in plain words. Only call nrp_run (or nrp_cleanup)
after the user says yes, passing the plan_id and confirm_token from nrp_plan. For web
goals, nrp_plan suggests names; let the user pick, and pass public_ack equal to the exact
public URL only after the user agrees it may be public.

Rules nrp enforces (see resource nrp://policy): non-sensitive (P1) data only; non-commercial;
Jobs for batch work (never sleep); limits within 20% of requests; special GPUs (A100/H100/
H200/GH200) need quota or opportunistic; Deployments are removed after 2 weeks and cannot
hold GPUs; nothing public without public_ack. Explain refusals and offer the fixed plan.

When something fails, call nrp_watch: it explains the cause and suggests a fix. Offer the
kubectl equivalent so the user learns. Guides: resource nrp://kb.`

// Server holds what the tools need.
type Server struct {
	Setup func() *nrpsetup.Env // nil: nrpsetup.DefaultEnv from Cfg
	Cfg   config.Config
	Ops   *ops.Ops
	Store *store.Store
	HTTP  func(url string) (int, error)
	Now   func() time.Time
}

func readOnly(title string) *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &f}
}

func writes(title string, destructive bool) *mcp.ToolAnnotations {
	t := true
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: &destructive, OpenWorldHint: &t}
}

// ---- inputs and outputs ----

type statusIn struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace to look at (default: your configured or first namespace)"`
}

type planIn struct {
	Project     string   `json:"project,omitempty" jsonschema:"path to the project folder (or a single script) on this computer"`
	Goal        string   `json:"goal,omitempty" jsonschema:"job (run once), sweep (many tasks), web (publish an app), session (Jupyter/VS Code), llm-batch, volume (shared storage), pull (download files into a volume); inferred if empty"`
	Command     string   `json:"command,omitempty" jsonschema:"what to run, e.g. python train.py --epochs 10 (inferred if empty)"`
	Namespace   string   `json:"namespace,omitempty"`
	Name        string   `json:"name,omitempty" jsonschema:"name for the workload or web host; nrp suggests names if empty"`
	Image       string   `json:"image,omitempty" jsonschema:"container image to use instead of nrp's choice"`
	GPU         int      `json:"gpu,omitempty" jsonschema:"number of GPUs (0 = none; default 1 if the code uses a GPU)"`
	GPUType     string   `json:"gpu_type,omitempty" jsonschema:"a40, rtxa6000, a100, ... (default: any standard GPU)"`
	CPU         string   `json:"cpu,omitempty" jsonschema:"CPU cores, e.g. 4"`
	Memory      string   `json:"memory,omitempty" jsonschema:"memory, e.g. 16Gi"`
	Hours       int      `json:"hours,omitempty" jsonschema:"time limit in hours (default 24)"`
	Count       int      `json:"count,omitempty" jsonschema:"sweep: number of tasks"`
	Parallel    int      `json:"parallel,omitempty" jsonschema:"sweep: tasks at a time"`
	Host        string   `json:"host,omitempty" jsonschema:"web: host name (one of the suggestions or your own)"`
	Port        int      `json:"port,omitempty" jsonschema:"web: port the app listens on (detected for Shiny, Streamlit, Flask, FastAPI, Dash, Gradio)"`
	DataVolume  string   `json:"data_volume,omitempty" jsonschema:"existing volume to mount at /data"`
	Session     string   `json:"session,omitempty" jsonschema:"session: jupyter (default) or vscode"`
	Opportunist bool     `json:"opportunistic,omitempty" jsonschema:"use priorityClassName opportunistic (special GPUs without quota; can be preempted)"`
	Size        string   `json:"size,omitempty" jsonschema:"volume: size, e.g. 50Gi"`
	URLs        []string `json:"urls,omitempty" jsonschema:"pull: http(s) links to download"`
	Subdir      string   `json:"subdir,omitempty" jsonschema:"pull: folder inside the volume"`
	DataIsP1    bool     `json:"data_is_p1,omitempty" jsonschema:"volume/pull: the user confirms the data is non-sensitive (P1)"`
	LLMSecret   string   `json:"llm_secret,omitempty" jsonschema:"llm-batch: name of the Secret holding the LLM token (key token)"`
}

type planOut struct {
	Summary      string              `json:"summary"`
	Runnable     bool                `json:"runnable"`
	PlanID       string              `json:"plan_id"`
	ConfirmToken string              `json:"confirm_token,omitempty"`
	TokenExpires string              `json:"token_expires,omitempty"`
	PublicURL    string              `json:"public_url,omitempty"`
	PublicAck    string              `json:"public_ack_required,omitempty"`
	Suggested    []string            `json:"suggested_names,omitempty"`
	Decisions    []string            `json:"decisions"`
	Refusals     []rules.Finding     `json:"refusals"`
	Warnings     []rules.Finding     `json:"warnings"`
	Image        string              `json:"image,omitempty"`
	NeedsBuild   bool                `json:"needs_build,omitempty"`
	Slurm        []plan.SlurmMapping `json:"slurm_mapping,omitempty"`
	CodeFiles    []string            `json:"code_files,omitempty"`
	Manifests    map[string]any      `json:"manifests" jsonschema:"a Kubernetes List of every object the plan would create"`
	Kubectl      []string            `json:"kubectl_equivalent"`
	Next         string              `json:"next"`
}

type runIn struct {
	PlanID       string `json:"plan_id"`
	ConfirmToken string `json:"confirm_token"`
	PublicAck    string `json:"public_ack,omitempty" jsonschema:"web only: the exact public URL from the plan, after the user agreed"`
}

type runOut struct {
	Summary string `json:"summary"`
	RunID   string `json:"run_id"`
	Applied string `json:"applied"`
	URL     string `json:"url,omitempty"`
	RunCard string `json:"run_card,omitempty"`
	Next    string `json:"next"`
	Cleanup string `json:"cleanup"`
}

type watchIn struct {
	Target    string `json:"target" jsonschema:"run id from nrp_run, or a Job, Deployment or pod name"`
	Namespace string `json:"namespace,omitempty"`
	Tail      int    `json:"tail,omitempty" jsonschema:"log lines (default 40)"`
	Grep      string `json:"grep,omitempty" jsonschema:"only log lines containing this"`
}

type cleanupIn struct {
	Namespace    string `json:"namespace,omitempty"`
	Run          string `json:"run,omitempty" jsonschema:"run id to remove; empty lists everything nrp-mcp made"`
	PlanID       string `json:"plan_id,omitempty" jsonschema:"step 2: the cleanup plan id"`
	ConfirmToken string `json:"confirm_token,omitempty" jsonschema:"step 2: the token from step 1"`
	Everyone     bool   `json:"everyone,omitempty" jsonschema:"namespace admins only: include runs made by other people in this namespace"`
}

type cleanupOut struct {
	Summary      string         `json:"summary"`
	Objects      []ops.Workload `json:"objects"`
	PlanID       string         `json:"plan_id,omitempty"`
	ConfirmToken string         `json:"confirm_token,omitempty"`
	Deleted      string         `json:"deleted,omitempty"`
	Next         string         `json:"next,omitempty"`
}

type sessionIn struct {
	Run       string `json:"run" jsonschema:"run id of a session started with nrp_plan goal session + nrp_run"`
	Namespace string `json:"namespace,omitempty"`
}

type sessionOut struct {
	Summary string `json:"summary"`
	Command string `json:"command"`
	URL     string `json:"url"`
	Token   string `json:"token,omitempty"`
	Ready   bool   `json:"ready"`
}

type dataIn struct {
	Action    string `json:"action" jsonschema:"list (files in a volume), up (copy a small local file or folder into a volume), down (copy from a volume to this computer)"`
	Volume    string `json:"volume" jsonschema:"volume (PVC) name"`
	Path      string `json:"path,omitempty" jsonschema:"path inside the volume (default /)"`
	Local     string `json:"local,omitempty" jsonschema:"up/down: local file or folder"`
	Namespace string `json:"namespace,omitempty"`
	DataIsP1  bool   `json:"data_is_p1,omitempty" jsonschema:"up: the user confirms the data is non-sensitive (P1)"`
}

type dataOut struct {
	Summary string `json:"summary"`
	Output  string `json:"output,omitempty"`
	Advice  string `json:"advice,omitempty"`
}

type buildIn struct {
	Project string `json:"project" jsonschema:"project folder"`
}

type buildOut struct {
	Summary    string   `json:"summary"`
	Dockerfile string   `json:"dockerfile_suggestion"`
	GitLabCI   string   `json:"gitlab_ci_suggestion"`
	Steps      []string `json:"steps"`
}

type setupIn struct {
	Fix    bool `json:"fix,omitempty" jsonschema:"install or update kubectl and kubelogin (official releases, SHA256-checked, into a user folder, no admin rights) and put a downloaded NRP config in place with a backup; ask the user first"`
	SignIn bool `json:"sign_in,omitempty" jsonschema:"also check the Nautilus sign-in (opens the browser the first time)"`
}

// ---- server ----

// nsCtx resolves the namespace the same way for every tool: the argument, then the
// configured default, then the first namespace the signed-in user belongs to.
func (s *Server) nsCtx(ctx context.Context, in string) string {
	if in != "" {
		return in
	}
	if s.Cfg.Namespace != "" {
		return s.Cfg.Namespace
	}
	if s.Ops != nil && s.Ops.K != nil {
		if w, err := s.Ops.K.WhoAmI(ctx); err == nil && len(w.Namespaces) > 0 {
			return w.Namespaces[0]
		}
	}
	return ""
}

func (s *Server) ruleCtx(ctx context.Context, ns string, p *plan.Plan) rules.Context {
	rc := rules.Context{PodsPerRun: s.Cfg.PodsPerRun, GPUsPerRun: s.Cfg.GPUsPerRun, HoursPerRun: s.Cfg.HoursPerRun, Session: p.Goal == plan.GoalSession}
	if p.Goal == plan.GoalSweep {
		if v, ok := p.Request["Count"].(float64); ok {
			rc.SweepCount = int(v)
		}
	}
	if qs, err := s.Ops.K.Quotas(ctx, ns); err == nil {
		rc.GPUQuota = kube.GPUQuota(qs)
	}
	return rc
}

// New builds the MCP server.
func New(s *Server) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "nrp", Title: "NRP Nautilus for researchers", Version: version.Version},
		&mcp.ServerOptions{Instructions: Instructions})

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_status", Description: "Where do I stand on Nautilus: who I am, my namespaces, GPU quotas, what is running and what it asks for, volumes, public URLs, and policy warnings (2-week Deployment limit, 6-hour bare pods, quota-gated GPUs). Read-only. Start every session here.", Annotations: readOnly("Status")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, *ops.Status, error) {
			st, err := s.Ops.StatusOf(ctx, s.nsCtx(ctx, in.Namespace))
			return nil, st, err
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_plan", Description: "Plan work on Nautilus from a project folder and a goal (job, sweep, web, session, llm-batch, volume, pull). Inspects the code (language, GPU use, entry point, web app, Slurm script), picks an image, sizes it, applies every NRP rule, and returns a plain-language summary, decisions, refusals, manifests and, if runnable, a single-use confirm_token for nrp_run. Creates nothing. For web, suggests names.", Annotations: readOnly("Plan"), InputSchema: singleTypeSchema[planIn]()}, s.planTool)

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_run", Description: "Run an approved plan: needs plan_id and the confirm_token from nrp_plan (single use, 10 minutes, bound to the exact plan). Web plans also need public_ack equal to the plan's public URL. Validates with a server dry run, applies, labels everything for cleanup, writes a run card in the project's .nrp/runs/.", Annotations: writes("Run", false)}, s.runTool)

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_watch", Description: "How is it going, why did it fail: pods, progress, recent logs (tail/grep) and a plain-language diagnosis with a fix (quota, no free GPU, out of memory, image pull, crash exit codes, volume mount). Checks a web app's public URL. Read-only.", Annotations: readOnly("Watch")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in watchIn) (*mcp.CallToolResult, *ops.WatchResult, error) {
			if in.Target == "" {
				return nil, nil, errors.New("target is required (a run id from nrp_run, or a Job/Deployment/pod name)")
			}
			r, err := s.Ops.Watch(ctx, s.nsCtx(ctx, in.Namespace), in.Target, in.Tail, in.Grep, s.HTTP)
			return nil, r, err
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_cleanup", Description: "Remove what nrp-mcp created. Step 1 (no token): lists objects for a run (or all nrp-mcp objects) and returns a cleanup plan_id + confirm_token. Step 2: call again with plan_id and confirm_token to delete. Never touches objects nrp-mcp did not create.", Annotations: writes("Clean up", true)}, s.cleanupTool)

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_session", Description: "Connect to an interactive Jupyter or VS Code session started with nrp_plan(goal=session) + nrp_run: reports readiness, gives the kubectl port-forward command and the local URL with its sign-in token. Sessions are never public. For no install, the NRP's hosted JupyterHub is https://jupyterhub-west.nrp-nautilus.io.", Annotations: readOnly("Session")}, s.sessionTool)

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_data", Description: "Move data the right way for its size. list: files in a volume. up: copy a small local file/folder (under 100 MB) into a volume (needs data_is_p1). down: copy results from a volume to this computer. For big downloads use nrp_plan goal=pull (in-cluster), and for datasets in/out at scale use NRP S3 (keys from the NRP portal).", Annotations: writes("Data", false)}, s.dataTool)

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_build", Description: "Containerize a project: suggests a Dockerfile from what the code uses and a .gitlab-ci.yml that builds it on NRP GitLab with kaniko into the NRP registry, plus the steps. Writes nothing; nrp_plan then uses the image.", Annotations: readOnly("Build")}, s.buildTool)

	mcp.AddTool(srv, &mcp.Tool{Name: "nrp_setup", Description: "Is this computer ready for Nautilus? Checks kubectl (and its version against the cluster), the kubelogin sign-in plugin, the NRP config file and, with sign_in=true, the sign-in. Read-only unless fix=true: then it downloads the official kubectl and kubelogin into a user folder (no admin rights), verifies their published SHA256, keeps any old copy as a backup, and puts a downloaded NRP config in place (backing up an existing one). Ask the user before fix=true. Use this first if nrp_status says kubectl or the sign-in is missing.", Annotations: writes("Setup", false)}, s.setupTool)

	addResources(srv)
	addPrompts(srv)
	return srv
}

// ToolNames lists every tool, for docs and tests.
func ToolNames() []string {
	return []string{"nrp_setup", "nrp_status", "nrp_plan", "nrp_run", "nrp_watch", "nrp_cleanup", "nrp_session", "nrp_data", "nrp_build"}
}

func (s *Server) planTool(ctx context.Context, _ *mcp.CallToolRequest, in planIn) (*mcp.CallToolResult, *planOut, error) {
	ns := s.nsCtx(ctx, in.Namespace)
	var facts *inspect.Facts
	project := ""
	if in.Project != "" {
		f, err := inspect.Dir(in.Project)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read project %s: %w", in.Project, err)
		}
		facts = f
		project = f.Root
	}
	owner := ""
	if w, err := s.Ops.K.WhoAmI(ctx); err == nil {
		owner = w.Username
		if ns == "" && len(w.Namespaces) > 0 {
			ns = w.Namespaces[0]
		}
	}
	req := plan.Request{Goal: in.Goal, Namespace: ns, Name: in.Name, Command: in.Command, Image: in.Image, GPU: in.GPU, GPUType: in.GPUType,
		CPU: in.CPU, Memory: in.Memory, Hours: in.Hours, Count: in.Count, Parallel: in.Parallel, Host: in.Host, Port: in.Port,
		DataPVC: in.DataVolume, Owner: owner, Session: in.Session, LLMSecret: in.LLMSecret, Opportun: in.Opportunist,
		Size: in.Size, URLs: in.URLs, Subdir: in.Subdir, DataIsP1: in.DataIsP1}
	p, err := plan.Build(facts, req)
	if err != nil {
		return nil, nil, err
	}
	var codeFiles []string
	if project != "" && facts != nil && (p.Goal == plan.GoalJob || p.Goal == plan.GoalSweep || p.Goal == plan.GoalWeb || p.Goal == plan.GoalLLMBatch) && !p.NeedsBuild {
		cm, files, err := ops.CodeConfigMap(project, p.Name+"-code-"+shortRun(p.Run), ns, copyMap(p.Labels))
		if err != nil {
			return nil, nil, err
		}
		if cm != nil && len(files) > 0 {
			ops.AttachCode(p.Objects, cm.Metadata.Name)
			p.Objects = append([]k8s.Object{*cm}, p.Objects...)
			codeFiles = files
			p.Decisions = append(p.Decisions, fmt.Sprintf("Copies %d project file(s) into the pod with a ConfigMap (text files under 200 KB; no data, secrets or hidden folders).", len(files)))
		}
	}
	if p.Goal == plan.GoalWeb && s.Ops != nil {
		p.Suggested = s.availableHosts(ctx, p.Suggested)
	}
	p.Hash = plan.Hash(p)
	findings := rules.Check(p.Objects, s.ruleCtx(ctx, ns, p))
	out := &planOut{Summary: p.Summary, PlanID: p.ID, PublicURL: p.PublicURL, Suggested: p.Suggested, Decisions: p.Decisions,
		Refusals: []rules.Finding{}, Warnings: []rules.Finding{}, Image: p.Image, NeedsBuild: p.NeedsBuild, Slurm: p.Slurm, CodeFiles: codeFiles, Kubectl: p.Kubectl}
	for _, f := range findings {
		if f.Severity == rules.Refuse {
			out.Refusals = append(out.Refusals, f)
		} else {
			out.Warnings = append(out.Warnings, f)
		}
	}
	if b, err := k8s.List(p.Objects); err == nil {
		_ = json.Unmarshal(b, &out.Manifests)
	}
	p.Extra["project"] = project
	if facts != nil {
		p.Extra["git_commit"] = facts.GitCommit
		p.Extra["git_remote"] = facts.GitRemote
	}
	if err := s.Store.SavePlan(p); err != nil {
		return nil, nil, err
	}
	switch {
	case len(out.Refusals) > 0:
		out.Next = "Not runnable: fix the refusals (each has a fix) and call nrp_plan again."
	case p.NeedsBuild:
		out.Next = "Build the image first (nrp_build), then plan again with image=<the pushed image>."
	default:
		tok, exp, err := s.Store.Issue(p, "run")
		if err != nil {
			return nil, nil, err
		}
		out.Runnable, out.ConfirmToken, out.TokenExpires = true, tok, exp.Format(time.RFC3339)
		out.Next = "Show the user the summary and decisions. If they agree, call nrp_run with plan_id and confirm_token."
		if p.Goal == plan.GoalWeb {
			out.PublicAck = p.PublicURL
			out.Next = "Show the user the summary, the suggested names and that " + p.PublicURL + " will be PUBLIC. If they agree, call nrp_run with plan_id, confirm_token and public_ack=" + p.PublicURL + ". To use another suggested name, plan again with host=<name>."
		}
	}
	s.Store.Audit(map[string]any{"tool": "nrp_plan", "plan_id": p.ID, "goal": p.Goal, "namespace": ns, "runnable": out.Runnable, "refusals": len(out.Refusals)})
	return nil, out, nil
}

func shortRun(run string) string {
	if i := strings.LastIndex(run, "-"); i >= 0 {
		return run[i+1:]
	}
	return run
}

func copyMap(m map[string]string) map[string]string {
	o := map[string]string{}
	for k, v := range m {
		o[k] = v
	}
	return o
}

// availableHosts drops suggested hosts that already answer (in use by someone).
func (s *Server) availableHosts(ctx context.Context, hosts []string) []string {
	if s.HTTP == nil {
		return hosts
	}
	var out []string
	for _, h := range hosts {
		code, err := s.HTTP("https://" + h + "/")
		if err == nil && code != 404 && code != 503 {
			continue // something already serves there
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		return hosts
	}
	return out
}

func (s *Server) runTool(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, *runOut, error) {
	p, err := s.Store.LoadPlan(in.PlanID)
	if err != nil {
		return nil, nil, err
	}
	if p.Goal == plan.GoalCleanup {
		return nil, nil, errors.New("that is a cleanup plan; use nrp_cleanup")
	}
	if p.Goal == plan.GoalWeb && strings.TrimSpace(in.PublicAck) != p.PublicURL {
		return nil, nil, fmt.Errorf("this plan publishes %s to the internet. Only after the user agrees, pass public_ack=%q exactly", p.PublicURL, p.PublicURL)
	}
	if fs := rules.Check(p.Objects, s.ruleCtx(ctx, p.Namespace, p)); rules.Blocking(fs) {
		return nil, nil, fmt.Errorf("the plan no longer passes the NRP rules (%s: %s); plan again", fs[0].ID, fs[0].Message)
	}
	if err := s.Store.Redeem(in.ConfirmToken, p, "run"); err != nil {
		return nil, nil, err
	}
	applied, err := s.Ops.Apply(ctx, p.Namespace, p.Objects)
	s.Store.Audit(map[string]any{"tool": "nrp_run", "plan_id": p.ID, "run_id": p.Run, "namespace": p.Namespace, "goal": p.Goal, "ok": err == nil, "public_url": p.PublicURL})
	if err != nil {
		return nil, nil, err
	}
	out := &runOut{RunID: p.Run, Applied: applied, Cleanup: "nrp_cleanup run=" + p.Run}
	project, _ := p.Extra["project"].(string)
	card := map[string]any{"run_id": p.Run, "plan_id": p.ID, "goal": p.Goal, "namespace": p.Namespace, "image": p.Image, "summary": p.Summary,
		"decisions": p.Decisions, "public_url": p.PublicURL, "git_commit": p.Extra["git_commit"], "git_remote": p.Extra["git_remote"],
		"started": time.Now().UTC().Format(time.RFC3339), "nrp_mcp": version.Version, "objects": p.Objects}
	if path, err := store.RunCard(project, card); err == nil {
		out.RunCard = path
	}
	switch p.Goal {
	case plan.GoalWeb:
		out.URL = p.PublicURL
		out.Summary = "Started. " + p.PublicURL + " should answer within a few minutes once the pod is ready and the certificate is issued."
		out.Next = "Check it with nrp_watch target=" + p.Run + " (it tests the URL). The NRP removes Deployments after 2 weeks unless your namespace has an exception."
	case plan.GoalSession:
		out.Summary = "Session starting. Call nrp_session run=" + p.Run + " for the local link and sign-in token."
		out.Next = "nrp_session run=" + p.Run
	case plan.GoalVolume:
		out.Summary = "Volume created. Use data_volume=" + p.Name + " in nrp_plan, nrp_data to copy small files, or goal=pull for big downloads."
		out.Next = "nrp_status shows it as Bound when ready."
	default:
		out.Summary = "Started run " + p.Run + "."
		out.Next = "Follow it with nrp_watch target=" + p.Run + "."
	}
	return nil, out, nil
}

func (s *Server) setupTool(ctx context.Context, _ *mcp.CallToolRequest, in setupIn) (*mcp.CallToolResult, *nrpsetup.Report, error) {
	var e *nrpsetup.Env
	if s.Setup != nil {
		e = s.Setup()
	} else {
		e = nrpsetup.DefaultEnv(s.Cfg.Kubeconfig, s.Cfg.Context, s.Cfg.Kubectl)
	}
	if !in.Fix {
		return nil, e.Inspect(ctx, in.SignIn), nil
	}
	r, err := e.Fix(ctx)
	s.Store.Audit(map[string]any{"tool": "nrp_setup", "fix": true, "ok": err == nil, "actions": reportActions(r)})
	if err != nil {
		return nil, nil, err
	}
	// Point the kubectl runner at the installed kubectl if the configured one is absent.
	if k := filepath.Join(e.BinDir, exeName("kubectl")); s.Ops != nil && s.Ops.K != nil {
		if _, lookErr := exec.LookPath(s.Ops.K.Cfg.Kubectl); lookErr != nil {
			if _, statErr := os.Stat(k); statErr == nil {
				s.Ops.K.Cfg.Kubectl = k
			}
		}
	}
	if in.SignIn && r.Ready == false {
		r2 := e.Inspect(ctx, true)
		r2.Actions = r.Actions
		r = r2
	}
	return nil, r, nil
}

func reportActions(r *nrpsetup.Report) []string {
	if r == nil {
		return nil
	}
	return r.Actions
}

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

func (s *Server) cleanupTool(ctx context.Context, _ *mcp.CallToolRequest, in cleanupIn) (*mcp.CallToolResult, *cleanupOut, error) {
	ns := s.nsCtx(ctx, in.Namespace)
	if in.PlanID != "" || in.ConfirmToken != "" {
		p, err := s.Store.LoadPlan(in.PlanID)
		if err != nil {
			return nil, nil, err
		}
		if p.Goal != plan.GoalCleanup {
			return nil, nil, errors.New("that plan is not a cleanup plan")
		}
		if err := s.Store.Redeem(in.ConfirmToken, p, "cleanup"); err != nil {
			return nil, nil, err
		}
		sc := ops.Scope{}
		if o, _ := p.Extra["owner_scope"].(string); o != "" {
			sc.Owner = o
		}
		out, err := s.Ops.Delete(ctx, p.Namespace, p.Run, sc)
		s.Store.Audit(map[string]any{"tool": "nrp_cleanup", "plan_id": p.ID, "run_id": p.Run, "namespace": p.Namespace, "ok": err == nil})
		if err != nil {
			return nil, nil, err
		}
		return nil, &cleanupOut{Summary: "Deleted the objects of run " + p.Run + ".", Deleted: out, Objects: []ops.Workload{}}, nil
	}
	sc, scopeNote, err := s.cleanupScope(ctx, ns, in.Everyone)
	if err != nil {
		return nil, nil, err
	}
	objs, err := s.Ops.CleanupList(ctx, ns, in.Run, sc)
	if err != nil {
		return nil, nil, err
	}
	if objs == nil {
		objs = []ops.Workload{}
	}
	out := &cleanupOut{Objects: objs}
	if in.Run == "" {
		runs := map[string]int{}
		for _, o := range objs {
			runs[o.Run]++
		}
		var parts []string
		for r, n := range runs {
			parts = append(parts, fmt.Sprintf("%s (%d objects)", r, n))
		}
		out.Summary = fmt.Sprintf("%d nrp-mcp object(s) in %s across %d run(s)%s: %s.", len(objs), ns, len(runs), scopeNote, strings.Join(parts, ", "))
		out.Next = "Call nrp_cleanup with run=<run id> to remove one run."
		return nil, out, nil
	}
	if len(objs) == 0 {
		out.Summary = "Nothing from run " + in.Run + scopeNote + " is left in " + ns + "."
		return nil, out, nil
	}
	var names []string
	for _, o := range objs {
		names = append(names, o.Kind+"/"+o.Name)
	}
	cp := plan.Cleanup(ns, in.Run, names)
	cp.Extra["owner_scope"] = sc.Owner
	cp.Hash = plan.Hash(cp)
	if err := s.Store.SavePlan(cp); err != nil {
		return nil, nil, err
	}
	tok, _, err := s.Store.Issue(cp, "cleanup")
	if err != nil {
		return nil, nil, err
	}
	out.Summary = fmt.Sprintf("Run %s has %d object(s) in %s: %s. Volumes listed here are deleted with their data; copy results down first (nrp_data action=down).", in.Run, len(objs), ns, strings.Join(names, ", "))
	out.PlanID, out.ConfirmToken = cp.ID, tok
	out.Next = "If the user agrees, call nrp_cleanup with plan_id and confirm_token."
	return nil, out, nil
}

// cleanupScope limits cleanup to the caller's own runs; a namespace admin may ask for
// everyone's.
func (s *Server) cleanupScope(ctx context.Context, ns string, everyone bool) (ops.Scope, string, error) {
	w, err := s.Ops.K.WhoAmI(ctx)
	if err != nil {
		return ops.Scope{}, "", err
	}
	if everyone {
		for _, a := range w.Admin {
			if a == ns {
				return ops.Scope{}, " (everyone's, as namespace admin)", nil
			}
		}
		return ops.Scope{}, "", fmt.Errorf("only an admin of %s can clean up other people's runs; leave out everyone to see your own", ns)
	}
	return ops.Scope{Owner: plan.OwnerID(w.Username)}, " (yours)", nil
}

func (s *Server) sessionTool(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, *sessionOut, error) {
	ns := s.nsCtx(ctx, in.Namespace)
	var dl struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := s.Ops.K.JSON(ctx, ns, &dl, "get", "deployment", "-l", "nrp-mcp/run="+in.Run+",nrp-mcp/session=true"); err != nil {
		return nil, nil, err
	}
	if len(dl.Items) == 0 {
		return nil, nil, fmt.Errorf("no session for run %s in %s (start one with nrp_plan goal=session, then nrp_run)", in.Run, ns)
	}
	d := dl.Items[0]
	var sec struct {
		Data map[string]string `json:"data"`
	}
	tok := ""
	if err := s.Ops.K.JSON(ctx, ns, &sec, "get", "secret", d.Metadata.Name+"-session"); err == nil {
		tok = decodeB64(sec.Data["token"])
	}
	port := 8888
	path := "/lab?token=" + tok
	if strings.Contains(d.Metadata.Name, "vscode") {
		port, path = 8080, "/"
	}
	var dep struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Ports []struct {
							ContainerPort int `json:"containerPort"`
						} `json:"ports"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if s.Ops.K.JSON(ctx, ns, &dep, "get", "deployment", d.Metadata.Name) == nil && len(dep.Spec.Template.Spec.Containers) > 0 && len(dep.Spec.Template.Spec.Containers[0].Ports) > 0 {
		port = dep.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort
		if port == 8080 {
			path = "/"
		}
	}
	out := &sessionOut{Ready: d.Status.ReadyReplicas > 0, Token: tok}
	out.Command = fmt.Sprintf("kubectl --context %s -n %s port-forward deploy/%s %d:%d", s.Cfg.Context, ns, d.Metadata.Name, port, port)
	out.URL = fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	if out.Ready {
		out.Summary = "Ready. Run the command in a terminal (leave it open), then open the URL. VS Code asks for the token as its password."
	} else {
		out.Summary = "Not ready yet (the image may still be downloading). Check again in a minute, or nrp_watch target=" + in.Run + "."
	}
	return nil, out, nil
}

func decodeB64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Server) dataTool(ctx context.Context, _ *mcp.CallToolRequest, in dataIn) (*mcp.CallToolResult, *dataOut, error) {
	ns := s.nsCtx(ctx, in.Namespace)
	if in.Volume == "" {
		return nil, nil, errors.New("volume is required")
	}
	path := "/data/" + strings.TrimPrefix(in.Path, "/")
	if strings.Contains(in.Path, "..") {
		return nil, nil, errors.New("path may not contain ..")
	}
	switch in.Action {
	case "list":
		pod, err := s.helperPod(ctx, ns, in.Volume)
		if err != nil {
			return nil, nil, err
		}
		out, err := s.Ops.K.Run(ctx, ns, nil, "exec", pod, "--", "sh", "-c", "du -sh "+shellQuote(path)+" 2>/dev/null; ls -la "+shellQuote(path)+" | head -100")
		if err != nil {
			return nil, nil, err
		}
		return nil, &dataOut{Summary: "Contents of " + in.Volume + ":" + strings.TrimPrefix(path, "/data"), Output: string(out),
			Advice: "The helper pod nrp-data-" + in.Volume + " stays for 6 hours at most; nrp_cleanup removes it."}, nil
	case "up":
		if !in.DataIsP1 {
			return nil, nil, errors.New("Nautilus is for non-sensitive (P1) data only. Confirm with data_is_p1=true that the files are not HIPAA, FERPA, PII, CUI or under a restrictive data-use agreement")
		}
		size, err := dirSize(in.Local)
		if err != nil {
			return nil, nil, err
		}
		if size > 100<<20 {
			return nil, nil, fmt.Errorf("%s is %d MB; kubectl cp goes through the Kubernetes API and the NRP asks not to send large data that way. Put it on a web server or S3 and use nrp_plan goal=pull, or upload to NRP S3 with rclone (KB027)", in.Local, size>>20)
		}
		pod, err := s.helperPod(ctx, ns, in.Volume)
		if err != nil {
			return nil, nil, err
		}
		dst := pod + ":" + path
		out, err := s.Ops.K.Run(ctx, ns, nil, "cp", in.Local, dst)
		s.Store.Audit(map[string]any{"tool": "nrp_data", "action": "up", "namespace": ns, "volume": in.Volume, "bytes": size, "ok": err == nil})
		if err != nil {
			return nil, nil, err
		}
		return nil, &dataOut{Summary: fmt.Sprintf("Copied %s (%d KB) to %s:%s.", in.Local, size>>10, in.Volume, strings.TrimPrefix(path, "/data")), Output: string(out)}, nil
	case "down":
		if in.Local == "" {
			return nil, nil, errors.New("local (where to save on this computer) is required")
		}
		pod, err := s.helperPod(ctx, ns, in.Volume)
		if err != nil {
			return nil, nil, err
		}
		out, err := s.Ops.K.Run(ctx, ns, nil, "cp", pod+":"+path, in.Local)
		if err != nil {
			return nil, nil, err
		}
		return nil, &dataOut{Summary: "Copied " + in.Volume + ":" + strings.TrimPrefix(path, "/data") + " to " + in.Local + ".", Output: string(out),
			Advice: "For many GB, copy to NRP S3 from inside the cluster and download with rclone instead (KB027)."}, nil
	}
	return nil, nil, errors.New("action must be list, up or down")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func dirSize(p string) (int64, error) {
	var n int64
	err := filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n, err
}

// helperPod returns a running helper pod that mounts the volume (a small bare pod,
// labelled for cleanup; bare pods are removed by the NRP after 6 hours anyway).
func (s *Server) helperPod(ctx context.Context, ns, pvc string) (string, error) {
	name := "nrp-data-" + plan.DNSName(pvc)
	var p struct {
		Status struct{ Phase string } `json:"status"`
	}
	if err := s.Ops.K.JSON(ctx, ns, &p, "get", "pod", name); err == nil && p.Status.Phase == "Running" {
		return name, nil
	}
	// The helper joins the volume's run, so cleaning up the volume removes it too.
	run := "data-" + plan.DNSName(pvc)
	var pv struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := s.Ops.K.JSON(ctx, ns, &pv, "get", "pvc", pvc); err != nil {
		return "", fmt.Errorf("no volume %q in %s (nrp_status lists volumes; make one with nrp_plan goal=volume): %w", pvc, ns, err)
	}
	if r := pv.Metadata.Labels["nrp-mcp/run"]; r != "" {
		run = r
	}
	helperOwner := pv.Metadata.Labels[plan.OwnerLabel]
	if helperOwner == "" {
		if w, err := s.Ops.K.WhoAmI(ctx); err == nil {
			helperOwner = plan.OwnerID(w.Username)
		}
	}
	obj := k8s.Object{APIVersion: "v1", Kind: "Pod", Metadata: k8s.Meta{Name: name, Namespace: ns,
		Labels: map[string]string{"app.kubernetes.io/managed-by": plan.ManagedBy, "nrp-mcp/run": run, "nrp-mcp/name": name, plan.OwnerLabel: helperOwner}},
		PodSpec: &k8s.PodSpec{RestartPolicy: "Never", Containers: []k8s.Container{{Name: "data", Image: "busybox:1.36",
			Command:      []string{"sh", "-c", "sleep 3600"},
			Resources:    k8s.Resources{Requests: map[string]string{"cpu": "100m", "memory": "128Mi"}, Limits: map[string]string{"cpu": "100m", "memory": "128Mi"}},
			VolumeMounts: []k8s.Mount{{Name: "data", MountPath: "/data"}}}},
			Volumes: []k8s.Volume{{Name: "data", PersistentVolumeClaim: &k8s.PVCSource{ClaimName: pvc}}}}}
	b, _ := k8s.List([]k8s.Object{obj})
	if _, err := s.Ops.K.Apply(ctx, ns, b, ""); err != nil {
		return "", err
	}
	if _, err := s.Ops.K.Run(ctx, ns, nil, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=120s"); err != nil {
		return "", fmt.Errorf("helper pod for %s did not start (is the volume Bound? nrp_status shows it): %w", pvc, err)
	}
	return name, nil
}

func (s *Server) buildTool(ctx context.Context, _ *mcp.CallToolRequest, in buildIn) (*mcp.CallToolResult, *buildOut, error) {
	f, err := inspect.Dir(in.Project)
	if err != nil {
		return nil, nil, err
	}
	p, err := plan.Build(f, plan.Request{Namespace: "x", Command: "true"})
	base := "python:3.12-slim"
	if err == nil && p.Image != "" && !p.NeedsBuild {
		base = p.Image
	}
	var df strings.Builder
	fmt.Fprintf(&df, "FROM %s\nWORKDIR /work\n", base)
	for _, d := range f.DepFiles {
		switch strings.ToLower(filepath.Base(d)) {
		case "requirements.txt":
			fmt.Fprintf(&df, "COPY %s /tmp/requirements.txt\nRUN pip install --no-cache-dir -r /tmp/requirements.txt\n", d)
		case "environment.yml", "environment.yaml":
			fmt.Fprintf(&df, "# conda env: consider a micromamba base image\nCOPY %s /tmp/environment.yml\n", d)
		case "renv.lock":
			fmt.Fprintf(&df, "COPY %s renv.lock\nRUN R -e \"install.packages('renv'); renv::restore()\"\n", d)
		case "package.json":
			df.WriteString("COPY package*.json ./\nRUN npm install --omit=dev\n")
		}
	}
	df.WriteString("COPY . /work\n")
	if f.Entry != "" {
		switch {
		case strings.HasSuffix(f.Entry, ".py"):
			fmt.Fprintf(&df, "CMD [\"python\", \"%s\"]\n", f.Entry)
		case strings.HasSuffix(strings.ToLower(f.Entry), ".r"):
			fmt.Fprintf(&df, "CMD [\"Rscript\", \"%s\"]\n", f.Entry)
		}
	}
	ci := `# .gitlab-ci.yml for https://gitlab.nrp-nautilus.io (runners are already set up)
build:
  image:
    name: ghcr.io/osscontainertools/kaniko:debug
    entrypoint: [""]
  variables:
    GODEBUG: "http2client=0"   # NRP: kaniko pushes to GitLab are very slow without this
  script:
    - echo "{\"auths\":{\"$CI_REGISTRY\":{\"username\":\"$CI_REGISTRY_USER\",\"password\":\"$CI_REGISTRY_PASSWORD\"}}}" > /kaniko/.docker/config.json
    - /kaniko/executor --cache=true --push-retry=10 --context $CI_PROJECT_DIR --dockerfile $CI_PROJECT_DIR/Dockerfile --destination $CI_REGISTRY_IMAGE:$CI_COMMIT_SHORT_SHA --destination $CI_REGISTRY_IMAGE:latest
`
	out := &buildOut{Dockerfile: df.String(), GitLabCI: ci,
		Summary: fmt.Sprintf("Suggested Dockerfile for %s (base %s) and a GitLab CI file that builds it on NRP GitLab with kaniko. Nothing was written.", f.Name, base),
		Steps: []string{
			"Review the Dockerfile; save it as Dockerfile in the project.",
			"Create a project at https://gitlab.nrp-nautilus.io (sign in with your institution) and push the code with the Dockerfile and .gitlab-ci.yml.",
			"The pipeline builds and pushes gitlab-registry.nrp-nautilus.io/<group>/<project>:latest (Deploy, Container Registry shows it).",
			"Make the registry project public, or add an image pull secret in your namespace for a private one.",
			"Call nrp_plan with image=gitlab-registry.nrp-nautilus.io/<group>/<project>:latest.",
		}}
	if f.Dockerfile {
		out.Summary = "Your project already has a Dockerfile; use the GitLab CI file to build it on NRP GitLab. " + out.Summary
	}
	return nil, out, nil
}

// HTTPCheck returns an HTTP status code for url (used for host checks and web watch).
func HTTPCheck(url string) (int, error) {
	c := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	r.Body.Close()
	return r.StatusCode, nil
}

func addResources(srv *mcp.Server) {
	res := []struct{ uri, name, title, text string }{
		{"nrp://policy", "policy", "NRP rules nrp enforces", knowledge.Policy},
		{"nrp://gpus", "gpus", "GPU resources on Nautilus", knowledge.GPUs},
		{"nrp://storage", "storage", "Storage chooser", knowledge.Storage},
		{"nrp://kb", "kb", "UCR guides to Nautilus", knowledge.KB},
		{"nrp://llm", "llm", "NRP hosted LLMs", knowledge.LLM},
	}
	for _, r := range res {
		r := r
		srv.AddResource(&mcp.Resource{URI: r.uri, Name: r.name, Title: r.title, MIMEType: "text/markdown"},
			func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: r.uri, MIMEType: "text/markdown", Text: r.text}}}, nil
			})
	}
}

func addPrompts(srv *mcp.Server) {
	prompts := []struct{ name, title, text string }{
		{"first_run", "Run my code on Nautilus for the first time", "Call nrp_status. Then ask me for my project folder and what I want to run. Call nrp_plan, explain the plan and every decision in plain words, and wait for my yes before nrp_run. Then follow it with nrp_watch and offer to copy results back."},
		{"port_slurm_script", "Move a Slurm job to Nautilus", "Ask for the folder that holds my sbatch script. Call nrp_plan on it, show the slurm_mapping table (what each #SBATCH line became and what has no equivalent), and explain the differences from the HPCC before I run anything."},
		{"parameter_sweep", "Run many tasks with different parameters", "Help me turn my script into a sweep: it must read $JOB_COMPLETION_INDEX to pick its parameters. Show the change, then nrp_plan goal=sweep with count and parallel, and wait for my yes."},
		{"my_job_failed", "Why did my job fail?", "Call nrp_watch on my run or job, explain the cause in plain words, and offer the fixed plan with nrp_plan."},
		{"host_web_tool", "Put my lab's web tool on the web", "Call nrp_plan goal=web on my project. Show me the suggested names and the public URL, remind me that only non-sensitive data may be served and that the NRP removes Deployments after 2 weeks without an exception. Only after I agree, nrp_run with public_ack."},
		{"teach_workshop", "Plan a class or workshop", "Read resource nrp://kb (KB030). Help me choose between the hosted JupyterHub, Coder and hands-on Kubernetes for my class, and draft the timeline and a namespace plan. Do not create anything."},
	}
	for _, p := range prompts {
		p := p
		srv.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.title},
			func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Description: p.title, Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text}}}}, nil
			})
	}
}

// singleTypeSchema infers the input schema for T and rewrites every nullable union
// ("type": ["null", "array"], which jsonschema-go emits for Go slices and maps) to the
// single non-null type. Gemini's function declarations accept one type per field and
// reject the union outright ("field predicate failed: $type == Type.ARRAY"), so a client
// such as OpenCode on a Gemini model fails before the first call. An omitted field
// already means "not set", so nothing is lost.
func singleTypeSchema[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	collapseNullable(s)
	return s
}

func collapseNullable(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if len(s.Types) > 0 {
		var keep []string
		for _, t := range s.Types {
			if t != "null" {
				keep = append(keep, t)
			}
		}
		if len(keep) == 1 {
			s.Type, s.Types = keep[0], nil
		}
	}
	collapseNullable(s.Items)
	for _, p := range s.Properties {
		collapseNullable(p)
	}
	for _, sub := range s.AnyOf {
		collapseNullable(sub)
	}
}
