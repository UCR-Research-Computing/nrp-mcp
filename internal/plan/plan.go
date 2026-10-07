// Package plan turns project facts and a goal into Kubernetes manifests for Nautilus,
// with the reasoning spelled out. It is pure (no cluster calls), so plans are testable.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/inspect"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
)

// Goals.
const (
	GoalJob      = "job"
	GoalSweep    = "sweep"
	GoalWeb      = "web"
	GoalSession  = "session"
	GoalLLMBatch = "llm-batch"
	GoalVolume   = "volume"
	GoalPull     = "pull"
	GoalCleanup  = "cleanup"
)

// Request is what the user asked for.
type Request struct {
	Goal       string
	Namespace  string
	Name       string // optional; suggested if empty
	Command    string // shell command; inferred if empty
	Image      string // override
	GPU        int
	GPUType    string // e.g. a100, a40; empty = any standard GPU
	CPU        string
	Memory     string
	Hours      int
	Count      int // sweep tasks
	Parallel   int // sweep parallelism
	Host       string
	Port       int
	DataPVC    string // existing PVC to mount at /data
	Owner      string
	Session    string // jupyter | vscode
	LLMSecret  string // Secret holding the LLM token (key "token")
	Opportun   bool
	Size       string   // volume size, e.g. 50Gi
	URLs       []string // pull: files to download
	Subdir     string   // pull: folder inside the volume
	DataIsP1   bool     // pull/volume: user confirmed the data is P1 (non-sensitive)
	ArrayIDs   []int    // sweep from a Slurm --array: the task ids, in order
	PullSecret string   // docker-registry Secret for a private image
	Run        string   // reuse a run id (update a running web app in place)
}

// Plan is the reviewed result.
type Plan struct {
	ID         string            `json:"plan_id"`
	Created    time.Time         `json:"created"`
	Goal       string            `json:"goal"`
	Namespace  string            `json:"namespace"`
	Name       string            `json:"name"`
	Summary    string            `json:"summary"`
	Image      string            `json:"image"`
	ImageWhy   string            `json:"image_reason"`
	NeedsBuild bool              `json:"needs_build"`
	Prebuilt   bool              `json:"prebuilt,omitempty"` // runs a built image as is: no code copy, no install
	Decisions  []string          `json:"decisions"`
	PublicURL  string            `json:"public_url,omitempty"`
	Suggested  []string          `json:"suggested_names,omitempty"`
	Objects    []k8s.Object      `json:"objects"`
	Facts      *inspect.Facts    `json:"facts,omitempty"`
	Slurm      []SlurmMapping    `json:"slurm_mapping,omitempty"`
	Kubectl    []string          `json:"kubectl_equivalent"`
	Extra      map[string]any    `json:"extra,omitempty"`
	Hash       string            `json:"plan_hash"`
	Run        string            `json:"run_id"`
	Request    map[string]any    `json:"request"`
	Cleanup    string            `json:"cleanup"`
	Labels     map[string]string `json:"labels"`
}

// SlurmMapping shows how one #SBATCH line was translated.
type SlurmMapping struct {
	Slurm   string `json:"slurm"`
	Becomes string `json:"becomes"`
}

// ManagedBy is the label value on everything nrp-mcp creates.
const ManagedBy = "nrp-mcp"

var nameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// DNSName makes a valid Kubernetes/DNS label from s (max 40 chars).
func DNSName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = nameRe.ReplaceAllString(s, "")
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		s = "app-" + s
		s = strings.Trim(s, "-")
	}
	return s
}

// SuggestNames returns up to three names from the project (title, folder, entry), no
// person or lab names are invented. Uniqueness is checked by the caller.
func SuggestNames(f *inspect.Facts, goal string) []string {
	var cands []string
	add := func(s string) {
		s = DNSName(s)
		if s == "" || s == "app" {
			return
		}
		for _, c := range cands {
			if c == s {
				return
			}
		}
		cands = append(cands, s)
	}
	if f != nil {
		if f.Title != "" {
			add(f.Title)
		}
		add(f.Name)
		if f.Entry != "" {
			base := strings.TrimSuffix(strings.TrimSuffix(f.Entry, ".py"), ".R")
			if base != "app" && base != "main" {
				add(f.Name + "-" + base)
			}
		}
		if f.WebApp != "" && f.Name != "" {
			add(f.Name + "-" + map[string]string{"shiny": "app", "streamlit": "viewer", "dash": "dashboard", "gradio": "demo",
				"fastapi": "api", "flask": "web", "node": "web", "static": "site"}[f.WebApp])
		}
	}
	if len(cands) == 0 {
		add(goal + "-" + strconv.Itoa(1000+rand.Intn(9000)))
	}
	if len(cands) > 3 {
		cands = cands[:3]
	}
	return cands
}

func newID(prefix string) string {
	b := make([]byte, 4)
	for i := range b {
		b[i] = "abcdefghjkmnpqrstuvwxyz23456789"[rand.Intn(31)]
	}
	return prefix + time.Now().UTC().Format("0102-1504") + "-" + string(b)
}

// Build makes a plan. It never calls the cluster.
func Build(f *inspect.Facts, r Request) (*Plan, error) {
	if r.Namespace == "" {
		return nil, fmt.Errorf("no namespace: pass namespace, or set one in ~/.config/nrp-mcp/config.yaml (nrp_status lists yours)")
	}
	var mappings []SlurmMapping
	if f != nil && len(f.Slurm) > 0 && r.Goal != GoalWeb && r.Goal != GoalSession {
		mappings = translateSlurm(f, &r)
	}
	if r.Goal == "" {
		r.Goal = GoalJob
		if f != nil && f.WebApp != "" {
			r.Goal = GoalWeb
		}
	}
	p := &Plan{ID: newID("p"), Created: time.Now().UTC(), Goal: r.Goal, Namespace: r.Namespace, Facts: f, Extra: map[string]any{}, Slurm: mappings}
	p.Run = strings.TrimPrefix(newID("r"), "r")
	if r.Run != "" {
		p.Run = r.Run
	}
	sug := SuggestNames(f, r.Goal)
	p.Suggested = sug
	if r.Name != "" {
		p.Name = DNSName(r.Name)
	} else {
		p.Name = sug[0]
	}
	p.Labels = map[string]string{"app.kubernetes.io/managed-by": ManagedBy, "nrp-mcp/run": p.Run, "nrp-mcp/plan": p.ID, "nrp-mcp/name": p.Name}
	if r.Owner != "" {
		p.Labels[OwnerLabel] = OwnerID(r.Owner)
	}
	pickImage(f, &r, p)
	var err error
	switch r.Goal {
	case GoalJob, GoalSweep, GoalLLMBatch:
		err = buildJob(f, r, p)
	case GoalWeb:
		err = buildWeb(f, r, p)
	case GoalSession:
		err = buildSession(r, p)
	case GoalVolume:
		err = buildVolume(r, p)
	case GoalPull:
		err = buildPull(r, p)
	default:
		err = fmt.Errorf("unknown goal %q (job, sweep, web, session, llm-batch, volume, pull)", r.Goal)
	}
	if err != nil {
		return nil, err
	}
	req, _ := json.Marshal(r)
	_ = json.Unmarshal(req, &p.Request)
	p.Cleanup = fmt.Sprintf("kubectl -n %s delete all,pvc,secret,ingress -l nrp-mcp/run=%s", r.Namespace, p.Run)
	p.Hash = Hash(p)
	return p, nil
}

// Hash is the sha256 of what would be applied (objects + goal + public URL).
func Hash(p *Plan) string {
	b, _ := json.Marshal(struct {
		O []k8s.Object
		G string
		U string
		N string
		R string
	}{p.Objects, p.Goal, p.PublicURL, p.Namespace, p.Run})
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func pickImage(f *inspect.Facts, r *Request, p *Plan) {
	switch {
	case r.Image != "":
		p.Image, p.ImageWhy = r.Image, "the image you named"
		if f == nil || f.Dockerfile {
			p.Prebuilt = true
			p.ImageWhy = "the image you named, run as built (its own start command; no code copied in)"
		}
	case f != nil && f.Dockerfile:
		p.Image = "ghcr.io/<you>/" + p.Name + ":latest"
		if f.GitHubRepo != "" {
			p.Image = "ghcr.io/" + f.GitHubRepo + ":latest"
		}
		p.ImageWhy = "your project has a Dockerfile; build it first (nrp_build gives a GitHub Actions file), then plan again with image="
		p.NeedsBuild = true
	case r.Goal == GoalSession && r.Session == "vscode":
		p.Image, p.ImageWhy = "codercom/code-server:latest", "VS Code in the browser (code-server)"
	case r.Goal == GoalSession:
		if r.GPU > 0 {
			p.Image, p.ImageWhy = "quay.io/jupyter/pytorch-notebook:cuda12-latest", "JupyterLab with PyTorch + CUDA (tested on Nautilus 2026-10-06)"
		} else {
			p.Image, p.ImageWhy = "quay.io/jupyter/scipy-notebook:latest", "JupyterLab with the scientific Python stack"
		}
	case f != nil && f.WebApp == "shiny":
		p.Image, p.ImageWhy = "rocker/shiny:latest", "R Shiny Server (serves /srv/shiny-server on 3838)"
	case f != nil && f.WebApp == "static":
		p.Image, p.ImageWhy = "nginxinc/nginx-unprivileged:stable", "static site served by nginx"
	case f != nil && f.WebApp == "node":
		p.Image, p.ImageWhy = "node:22-slim", "Node.js app"
	case f != nil && f.Framework == "pytorch":
		p.Image, p.ImageWhy = "pytorch/pytorch:latest", "PyTorch with CUDA (torch import found)"
	case f != nil && f.Framework == "tensorflow":
		p.Image, p.ImageWhy = "tensorflow/tensorflow:latest-gpu", "TensorFlow with GPU support (tensorflow import found)"
	case f != nil && f.Framework == "jax":
		p.Image, p.ImageWhy = "nvcr.io/nvidia/jax:latest", "JAX with CUDA"
	case f != nil && contains(f.Languages, "r"):
		p.Image, p.ImageWhy = "rocker/r-ver:latest", "R (rocker); packages install at start"
	case f != nil && contains(f.Languages, "julia"):
		p.Image, p.ImageWhy = "julia:latest", "Julia"
	default:
		p.Image, p.ImageWhy = "python:3.12-slim", "plain Python"
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func gpuResource(r Request) string {
	if r.GPUType == "" || r.GPUType == "any" || r.GPUType == "gpu" {
		return "nvidia.com/gpu"
	}
	return "nvidia.com/" + strings.TrimPrefix(strings.ToLower(r.GPUType), "nvidia.com/")
}

// sizing returns requests = limits (always within the 20% rule, and right for >100 pods).
func sizing(r Request, f *inspect.Facts, gpu int) (cpu, mem string, why string) {
	cpu, mem = r.CPU, r.Memory
	switch {
	case cpu != "" && mem != "":
		why = "the CPU and memory you asked for"
	case gpu > 0:
		if cpu == "" {
			cpu = strconv.Itoa(4 * gpu)
		}
		if mem == "" {
			mem = strconv.Itoa(16*gpu) + "Gi"
		}
		why = fmt.Sprintf("%s CPU and %s per the GPU count (4 cores, 16 GiB per GPU feeds a GPU well)", cpu, mem)
	default:
		if cpu == "" {
			cpu = "1"
		}
		if mem == "" {
			mem = "2Gi"
		}
		why = "1 CPU and 2 GiB: enough for most scripts and exempt from the NRP usage-violation checks"
	}
	return cpu, mem, why
}

func installStep(f *inspect.Facts, p *Plan) string {
	if f == nil || p.NeedsBuild || p.Prebuilt {
		return ""
	}
	for _, d := range f.DepFiles {
		switch strings.ToLower(d) {
		case "requirements.txt":
			return "pip install --quiet --no-cache-dir -r requirements.txt && "
		}
	}
	return ""
}

func inferCommand(f *inspect.Facts, r Request) (string, string) {
	if r.Command != "" {
		return r.Command, "the command you gave"
	}
	if f == nil || f.Entry == "" {
		return "", ""
	}
	switch {
	case strings.HasSuffix(f.Entry, ".py"):
		return "python " + f.Entry, "entry point " + f.Entry + " (" + f.EntryReason + ")"
	case strings.HasSuffix(strings.ToLower(f.Entry), ".r"):
		return "Rscript " + f.Entry, "entry point " + f.Entry
	case strings.HasSuffix(f.Entry, ".jl"):
		return "julia " + f.Entry, "entry point " + f.Entry
	case strings.HasSuffix(f.Entry, ".sh"):
		return "bash " + f.Entry, "entry point " + f.Entry
	}
	return "", ""
}

func buildJob(f *inspect.Facts, r Request, p *Plan) error {
	cmd, why := inferCommand(f, r)
	if p.Prebuilt {
		cmd, why = r.Command, "the command you gave"
	}
	if cmd == "" && !p.Prebuilt {
		return fmt.Errorf("I could not tell what to run: pass command (for example \"python train.py --epochs 10\")")
	}
	if cmd != "" {
		p.Decisions = append(p.Decisions, "Runs: "+cmd+" ("+why+").")
	} else {
		p.Decisions = append(p.Decisions, "Runs the image's own start command (CMD/ENTRYPOINT).")
	}
	gpu := r.GPU
	if gpu == 0 && f != nil && f.UsesGPU && r.Goal != GoalLLMBatch {
		gpu = 1
		p.Decisions = append(p.Decisions, "1 GPU because the code uses one ("+f.GPUReason+"). Pass gpu=0 to run on CPU only.")
	}
	cpu, mem, swhy := sizing(r, f, gpu)
	p.Decisions = append(p.Decisions, "Size: "+swhy+"; limits equal requests (the NRP asks for limits within 20%).")
	res := k8s.Resources{Requests: map[string]string{"cpu": cpu, "memory": mem}, Limits: map[string]string{"cpu": cpu, "memory": mem}}
	if gpu > 0 {
		g := gpuResource(r)
		res.Requests[g], res.Limits[g] = strconv.Itoa(gpu), strconv.Itoa(gpu)
		if g == "nvidia.com/gpu" {
			p.Decisions = append(p.Decisions, fmt.Sprintf("%d standard GPU(s): Kubernetes picks any free one (in our tests an RTX A4000 at SDSC).", gpu))
		} else {
			p.Decisions = append(p.Decisions, fmt.Sprintf("%d x %s requested by type.", gpu, g))
		}
	}
	install := installStep(f, p)
	if install != "" {
		p.Decisions = append(p.Decisions, "Installs requirements.txt at start (fine for small projects; for big ones build an image with nrp_build).")
	}
	script := "set -e; cd /work; " + install + cmd
	if r.Goal == GoalSweep && r.Count > 0 {
		script = "set -e; cd /work; " + arrayEnv(r.ArrayIDs, r.Count) + install + cmd
	}
	ctr := k8s.Container{Name: "main", Image: p.Image, Command: []string{"bash", "-c", script}, WorkingDir: "/work", Resources: res,
		VolumeMounts: []k8s.Mount{{Name: "code", MountPath: "/work"}}}
	vols := []k8s.Volume{{Name: "code", EmptyDir: &k8s.EmptyDir{}}}
	if gpu > 0 {
		ctr.VolumeMounts = append(ctr.VolumeMounts, k8s.Mount{Name: "dshm", MountPath: "/dev/shm"})
		vols = append(vols, k8s.Volume{Name: "dshm", EmptyDir: &k8s.EmptyDir{Medium: "Memory", SizeLimit: "8Gi"}})
		p.Decisions = append(p.Decisions, "Adds an 8 GiB /dev/shm so PyTorch data loaders do not crash.")
	}
	if r.DataPVC != "" {
		ctr.VolumeMounts = append(ctr.VolumeMounts, k8s.Mount{Name: "data", MountPath: "/data"})
		vols = append(vols, k8s.Volume{Name: "data", PersistentVolumeClaim: &k8s.PVCSource{ClaimName: r.DataPVC}})
		p.Decisions = append(p.Decisions, "Mounts your volume "+r.DataPVC+" at /data.")
	}
	if r.Goal == GoalLLMBatch {
		sec := r.LLMSecret
		if sec == "" {
			sec = "nrp-llm-token"
		}
		ctr.Env = append(ctr.Env, k8s.EnvVar{Name: "OPENAI_API_KEY", ValueFrom: &k8s.EnvVarSource{SecretKeyRef: &k8s.KeyRef{Name: sec, Key: "token"}}},
			k8s.EnvVar{Name: "OPENAI_BASE_URL", Value: "https://ellm.nrp-nautilus.io/v1"})
		p.Decisions = append(p.Decisions, "LLM token comes from the Secret "+sec+" (key token), never the spec; base URL https://ellm.nrp-nautilus.io/v1. Retry with backoff in your script (fair use: 200K output tokens/min per token and model).")
	}
	ctr.Env = append(ctr.Env, k8s.EnvVar{Name: "NRP_RUN", Value: p.Run})
	if p.Prebuilt {
		ctr.WorkingDir = ""
		ctr.VolumeMounts = ctr.VolumeMounts[1:] // drop /work; keep /dev/shm and /data
		vols = vols[1:]
		ctr.Command = nil
		if cmd != "" {
			ctr.Command = []string{"sh", "-c", cmd}
		}
		if r.Goal == GoalSweep && r.Count > 0 {
			p.Decisions = append(p.Decisions, "Sweep with a prebuilt image: read the task number from $JOB_COMPLETION_INDEX.")
		}
	}
	pod := k8s.PodSpec{RestartPolicy: "Never", Containers: []k8s.Container{ctr}, Volumes: vols, ImagePullSecrets: pullSecrets(r, p)}
	if r.Opportun || (gpu > 0 && isSpecial(gpuResource(r)) && r.Opportun) {
		pod.PriorityClassName = "opportunistic"
		p.Decisions = append(p.Decisions, "priorityClassName opportunistic: bypasses the special-GPU quota, but the pod can be preempted at any time. Checkpoint your work.")
	}
	hours := r.Hours
	if hours == 0 {
		hours = 24
	}
	js := &k8s.JobSpec{BackoffLimit: k8s.IntPtr(2), TTLSecondsAfterFinished: k8s.IntPtr(86400), ActiveDeadlineSeconds: k8s.IntPtr(hours * 3600),
		Template: k8s.PodTemplate{Metadata: k8s.Meta{Labels: copyLabels(p.Labels)}, Spec: pod}}
	p.Decisions = append(p.Decisions, fmt.Sprintf("A Job (not a bare pod): it runs to completion, retries twice, stops after %d h, and is deleted 24 h after it finishes.", hours))
	if r.Goal == GoalSweep {
		n := r.Count
		if n <= 0 {
			return fmt.Errorf("a sweep needs count (how many tasks)")
		}
		par := r.Parallel
		if par <= 0 {
			par = min(n, 10)
		}
		js.Completions, js.Parallelism, js.CompletionMode = k8s.IntPtr(n), k8s.IntPtr(par), "Indexed"
		js.BackoffLimit = k8s.IntPtr(max(2, n/10))
		ids := r.ArrayIDs
		if len(ids) != n {
			ids = nil
		}
		task := fmt.Sprintf("0..%d", n-1)
		if ids != nil {
			task = idRange(ids)
		}
		p.Decisions = append(p.Decisions, fmt.Sprintf("Indexed Job: %d tasks, %d at a time. Each task gets its own number in $SLURM_ARRAY_TASK_ID (%s) and $JOB_COMPLETION_INDEX (0..%d); seed or pick parameters from it.", n, par, task, n-1))
	}
	p.Objects = append(p.Objects, k8s.Object{APIVersion: "batch/v1", Kind: "Job", Metadata: k8s.Meta{Name: p.Name + "-" + shortRun(p.Run), Namespace: r.Namespace, Labels: copyLabels(p.Labels), Annotations: owner(r)}, JobSpec: js})
	p.Extra["code_delivery"] = "nrp_run copies your project (small text files, no data or secrets) into the Job with a ConfigMap; large projects should use nrp_build or git"
	p.Kubectl = []string{fmt.Sprintf("kubectl -n %s apply -f plan.json", r.Namespace), fmt.Sprintf("kubectl -n %s logs -f job/%s-%s", r.Namespace, p.Name, shortRun(p.Run))}
	what := "job"
	if r.Goal == GoalSweep {
		what = fmt.Sprintf("sweep of %d tasks", r.Count)
	}
	gs := "no GPU"
	if gpu > 0 {
		gs = fmt.Sprintf("%d GPU", gpu)
	}
	p.Summary = fmt.Sprintf("A %s in %s running `%s` on %s with %s CPU, %s memory, %s. Image %s (%s).", what, r.Namespace, cmd, "Nautilus", cpu, mem, gs, p.Image, p.ImageWhy)
	return nil
}

func isSpecial(g string) bool {
	for _, s := range []string{"nvidia.com/a100", "nvidia.com/h100", "nvidia.com/h200", "nvidia.com/gh200"} {
		if g == s {
			return true
		}
	}
	return false
}

func shortRun(run string) string {
	if i := strings.LastIndex(run, "-"); i >= 0 {
		return run[i+1:]
	}
	return run
}

func copyLabels(m map[string]string) map[string]string {
	o := map[string]string{}
	for k, v := range m {
		o[k] = v
	}
	return o
}

// OwnerLabel holds a short hash of the cluster user who made an object, so several
// people can share a namespace (a workshop) and each cleans up only their own runs.
// Label values cannot hold the CILogon URL, and a hash keeps it out of plain sight.
const OwnerLabel = "nrp-mcp/owner-id"

// OwnerID is the label value for a cluster username.
func OwnerID(user string) string {
	h := sha256.Sum256([]byte(user))
	return hex.EncodeToString(h[:])[:16]
}

func owner(r Request) map[string]string {
	if r.Owner == "" {
		return nil
	}
	return map[string]string{"nrp-mcp/owner": r.Owner}
}

func buildWeb(f *inspect.Facts, r Request, p *Plan) error {
	app := ""
	port := r.Port
	if f != nil {
		app = f.WebApp
		if port == 0 && p.Prebuilt && f.DockerPort > 0 {
			port = f.DockerPort
			p.Decisions = append(p.Decisions, fmt.Sprintf("Port %d from EXPOSE in your Dockerfile.", port))
		}
		if port == 0 {
			port = f.WebPort
		}
	}
	if port == 0 {
		port = 8080
		if p.Prebuilt {
			p.Decisions = append(p.Decisions, "Port 8080 assumed: pass port= if your image listens on another one.")
		}
	}
	if r.GPU > 0 {
		p.Decisions = append(p.Decisions, "Ignoring gpu: long-running web Deployments cannot hold GPUs on Nautilus.")
	}
	cmd := r.Command
	switch {
	case p.Prebuilt:
	case cmd != "":
	case app == "streamlit":
		cmd = fmt.Sprintf("streamlit run %s --server.port %d --server.address 0.0.0.0 --server.headless true", f.Entry, port)
	case app == "gradio":
		cmd = "GRADIO_SERVER_NAME=0.0.0.0 GRADIO_SERVER_PORT=" + strconv.Itoa(port) + " python " + f.Entry
	case app == "dash":
		cmd = "python " + f.Entry
	case app == "fastapi":
		mod := strings.TrimSuffix(strings.ReplaceAll(f.Entry, "/", "."), ".py")
		cmd = fmt.Sprintf("pip install --quiet uvicorn && uvicorn %s:app --host 0.0.0.0 --port %d", mod, port)
	case app == "flask":
		mod := strings.TrimSuffix(strings.ReplaceAll(f.Entry, "/", "."), ".py")
		cmd = fmt.Sprintf("pip install --quiet gunicorn && gunicorn -b 0.0.0.0:%d %s:app", port, mod)
	case app == "shiny":
		cmd = "cp -r /work/. /srv/shiny-server/ && exec /init"
	case app == "static":
		cmd = "cp -r /work/. /usr/share/nginx/html/ && exec nginx -g 'daemon off;'"
		port = 8080
	case app == "node":
		cmd = "npm install --omit=dev && npm start"
	default:
		return fmt.Errorf("I could not tell how to start the web app: pass command (for example \"python app.py\") and port")
	}
	if app == "static" {
		p.Decisions = append(p.Decisions, "nginx-unprivileged listens on 8080.")
	}
	install := installStep(f, p)
	if app == "shiny" || app == "static" || app == "node" {
		install = ""
	}
	cpu, mem := r.CPU, r.Memory
	if cpu == "" {
		cpu = "1"
	}
	if mem == "" {
		mem = "2Gi"
	}
	p.Decisions = append(p.Decisions, fmt.Sprintf("A Deployment (1 replica) with %s CPU and %s, limits equal to requests (inside the NRP usage-check exemption at 1 CPU / 2 GiB).", cpu, mem))
	host := r.Host
	if host == "" {
		host = p.Name + ".nrp-nautilus.io"
	}
	if !strings.Contains(host, ".") {
		host += ".nrp-nautilus.io"
	}
	p.PublicURL = "https://" + host + "/"
	var hosts []string
	for _, s := range p.Suggested {
		hosts = append(hosts, s+".nrp-nautilus.io")
	}
	p.Suggested = hosts
	p.Decisions = append(p.Decisions, "Public address: "+p.PublicURL+" (TLS). Other suggestions: "+strings.Join(hosts, ", ")+". Pick one with host=, or type your own.")
	if !strings.HasSuffix(host, ".nrp-nautilus.io") {
		p.Decisions = append(p.Decisions, "Own domain: create a CNAME from "+host+" to nrp-nautilus.io and a certificate (cert-manager); see KB029.")
	}
	labels := copyLabels(p.Labels)
	sel := map[string]string{"nrp-mcp/name": p.Name, "nrp-mcp/run": p.Run}
	ctr := k8s.Container{Name: "web", Image: p.Image, Command: []string{"bash", "-c", "set -e; cd /work; " + install + cmd}, WorkingDir: "/work",
		Ports:          []k8s.Port{{ContainerPort: port, Name: "http"}},
		Resources:      k8s.Resources{Requests: map[string]string{"cpu": cpu, "memory": mem}, Limits: map[string]string{"cpu": cpu, "memory": mem}},
		VolumeMounts:   []k8s.Mount{{Name: "code", MountPath: "/work"}},
		ReadinessProbe: &k8s.Probe{HTTPGet: &k8s.HTTPGet{Path: "/", Port: port}, InitialDelaySeconds: 10, PeriodSeconds: 10}}
	if app == "static" {
		ctr.Command = []string{"sh", "-c", "cp -r /work/. /usr/share/nginx/html/ && exec nginx -g 'daemon off;'"}
	}
	if app == "shiny" {
		ctr.Command = []string{"sh", "-c", "cp -r /work/. /srv/shiny-server/ && exec /init"}
	}
	vols := []k8s.Volume{{Name: "code", EmptyDir: &k8s.EmptyDir{}}}
	if p.Prebuilt {
		ctr.Command, ctr.WorkingDir, ctr.VolumeMounts, vols = nil, "", nil, nil
		if cmd != "" {
			ctr.Command = []string{"sh", "-c", cmd}
			p.Decisions = append(p.Decisions, "Starts with: "+cmd+" (instead of the image's own command).")
		} else {
			p.Decisions = append(p.Decisions, "Runs the image's own start command (CMD/ENTRYPOINT); no project files are copied in.")
		}
	}
	ctr.Env = append(ctr.Env, k8s.EnvVar{Name: "NRP_RUN", Value: p.Run})
	dep := k8s.Object{APIVersion: "apps/v1", Kind: "Deployment", Metadata: k8s.Meta{Name: p.Name, Namespace: r.Namespace, Labels: labels, Annotations: owner(r)},
		DeploymentSpec: &k8s.DeploymentSpec{Replicas: 1, Selector: k8s.LabelSelector{MatchLabels: sel},
			Template: k8s.PodTemplate{Metadata: k8s.Meta{Labels: labels}, Spec: k8s.PodSpec{Containers: []k8s.Container{ctr},
				Volumes: vols, ImagePullSecrets: pullSecrets(r, p)}}}}
	svc := k8s.Object{APIVersion: "v1", Kind: "Service", Metadata: k8s.Meta{Name: p.Name, Namespace: r.Namespace, Labels: copyLabels(labels)},
		ServiceSpec: &k8s.ServiceSpec{Selector: sel, Ports: []k8s.ServicePort{{Port: 80, TargetPort: port}}, Type: "ClusterIP"}}
	tls := k8s.IngressTLS{Hosts: []string{host}}
	if !strings.HasSuffix(host, ".nrp-nautilus.io") {
		tls.SecretName = p.Name + "-tls"
	}
	ing := k8s.Object{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Metadata: k8s.Meta{Name: p.Name, Namespace: r.Namespace, Labels: copyLabels(labels)},
		IngressSpec: &k8s.IngressSpec{IngressClassName: "haproxy", TLS: []k8s.IngressTLS{tls},
			Rules: []k8s.IngressRule{{Host: host, HTTP: k8s.IngressRuleValue{Paths: []k8s.IngressPath{{Path: "/", PathType: "Prefix",
				Backend: k8s.IngressBackend{Service: k8s.IngressServiceBackend{Name: p.Name, Port: k8s.IngressServicePort{Number: 80}}}}}}}}}}
	p.Objects = []k8s.Object{dep, svc, ing}
	p.Extra["public_ack_required"] = p.PublicURL
	p.Extra["code_delivery"] = "nrp_run copies your project files into the pod with a ConfigMap (small projects); use nrp_build for anything larger"
	p.Decisions = append(p.Decisions, "Going public needs nrp_run with public_ack set to exactly "+p.PublicURL+". Only P1 (non-sensitive) data may be served. The NRP removes Deployments after 2 weeks unless your namespace is on its exceptions list.")
	p.Kubectl = []string{fmt.Sprintf("kubectl -n %s apply -f plan.json", r.Namespace), fmt.Sprintf("kubectl -n %s port-forward svc/%s 8080:80   # private test before going public", r.Namespace, p.Name)}
	appName := app
	if appName == "" {
		appName = "web"
	}
	p.Summary = fmt.Sprintf("A %s web service %q in %s at %s: Deployment + Service + Ingress, %s CPU, %s, no GPU. Image %s (%s).", appName, p.Name, r.Namespace, p.PublicURL, cpu, mem, p.Image, p.ImageWhy)
	return nil
}

func buildSession(r Request, p *Plan) error {
	gpu := r.GPU
	if gpu > 2 {
		return fmt.Errorf("interactive sessions can have at most 2 GPUs on Nautilus")
	}
	cpu, mem := r.CPU, r.Memory
	if cpu == "" {
		cpu = "2"
		if gpu > 0 {
			cpu = strconv.Itoa(4 * gpu)
		}
	}
	if mem == "" {
		mem = "8Gi"
		if gpu > 0 {
			mem = strconv.Itoa(16*gpu) + "Gi"
		}
	}
	tok := randHex(16)
	port := 8888
	var cmd []string
	if r.Session == "vscode" {
		port = 8080
		cmd = []string{"code-server", "--bind-addr", "0.0.0.0:8080", "--auth", "password"}
	} else {
		cmd = []string{"start-notebook.py", "--IdentityProvider.token=$(NRP_SESSION_TOKEN)"}
	}
	res := k8s.Resources{Requests: map[string]string{"cpu": cpu, "memory": mem}, Limits: map[string]string{"cpu": cpu, "memory": mem}}
	if gpu > 0 {
		res.Requests["nvidia.com/gpu"], res.Limits["nvidia.com/gpu"] = strconv.Itoa(gpu), strconv.Itoa(gpu)
	}
	ctr := k8s.Container{Name: "session", Image: p.Image, Command: cmd, Ports: []k8s.Port{{ContainerPort: port}}, Resources: res,
		Env: []k8s.EnvVar{{Name: "NRP_SESSION_TOKEN", ValueFrom: &k8s.EnvVarSource{SecretKeyRef: &k8s.KeyRef{Name: p.Name + "-session", Key: "token"}}},
			{Name: "PASSWORD", ValueFrom: &k8s.EnvVarSource{SecretKeyRef: &k8s.KeyRef{Name: p.Name + "-session", Key: "token"}}}}}
	if r.DataPVC != "" {
		ctr.VolumeMounts = append(ctr.VolumeMounts, k8s.Mount{Name: "data", MountPath: "/home/jovyan/data"})
	}
	spec := k8s.PodSpec{Containers: []k8s.Container{ctr}}
	if r.DataPVC != "" {
		spec.Volumes = []k8s.Volume{{Name: "data", PersistentVolumeClaim: &k8s.PVCSource{ClaimName: r.DataPVC}}}
	}
	labels := copyLabels(p.Labels)
	labels["nrp-mcp/session"] = "true"
	sec := k8s.Object{APIVersion: "v1", Kind: "Secret", Metadata: k8s.Meta{Name: p.Name + "-session", Namespace: r.Namespace, Labels: copyLabels(labels)}, StringData: map[string]string{"token": tok}}
	dep := k8s.Object{APIVersion: "apps/v1", Kind: "Deployment", Metadata: k8s.Meta{Name: p.Name, Namespace: r.Namespace, Labels: labels, Annotations: owner(r)},
		DeploymentSpec: &k8s.DeploymentSpec{Replicas: 1, Selector: k8s.LabelSelector{MatchLabels: map[string]string{"nrp-mcp/name": p.Name, "nrp-mcp/run": p.Run}},
			Template: k8s.PodTemplate{Metadata: k8s.Meta{Labels: labels}, Spec: spec}}}
	p.Objects = []k8s.Object{sec, dep}
	p.Extra["session_port"] = port
	p.Extra["session_token_secret"] = p.Name + "-session"
	kind := "Jupyter"
	if r.Session == "vscode" {
		kind = "VS Code"
	}
	gs := "no GPU"
	if gpu > 0 {
		gs = fmt.Sprintf("%d GPU", gpu)
	}
	p.Decisions = append(p.Decisions,
		fmt.Sprintf("%s session as a 1-replica Deployment (%s CPU, %s, %s); reached only through kubectl port-forward on your machine, never public.", kind, cpu, mem, gs),
		"Its sign-in token lives in a Secret and is shown to you once by nrp_session.",
		"Stop it with nrp_cleanup when you are done: an idle GPU blocks others, and idle sessions count against your namespace.",
		"For a no-install option, the NRP's hosted JupyterHub is https://jupyterhub-west.nrp-nautilus.io (shuts down 1 h after you close the tab).")
	p.Summary = fmt.Sprintf("A private %s session in %s with %s CPU, %s memory, %s, image %s.", kind, r.Namespace, cpu, mem, gs, p.Image)
	p.Kubectl = []string{fmt.Sprintf("kubectl -n %s port-forward deploy/%s %d:%d", r.Namespace, p.Name, port, port)}
	return nil
}

func randHex(n int) string {
	const h = "0123456789abcdef"
	b := make([]byte, n*2)
	for i := range b {
		b[i] = h[rand.Intn(16)]
	}
	return string(b)
}

var sbatchLine = regexp.MustCompile(`^#SBATCH\s+(--?[A-Za-z-]+)(?:[= ]\s*(\S+))?`)

// translateSlurm reads the first Slurm script and maps its directives into r.
func translateSlurm(f *inspect.Facts, r *Request) []SlurmMapping {
	body := readFile(f.Root, f.Slurm[0])
	var out []SlurmMapping
	var cmds []string
	for _, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if m := sbatchLine.FindStringSubmatch(t); m != nil {
			opt, val := m[1], m[2]
			switch opt {
			case "--gres", "-G", "--gpus":
				n := 1
				if i := strings.LastIndex(val, ":"); i >= 0 {
					n, _ = strconv.Atoi(val[i+1:])
				} else if v, err := strconv.Atoi(val); err == nil {
					n = v
				}
				if r.GPU == 0 {
					r.GPU = n
				}
				out = append(out, SlurmMapping{t, fmt.Sprintf("nvidia.com/gpu: %d", n)})
			case "--cpus-per-task", "-c":
				if r.CPU == "" {
					r.CPU = val
				}
				out = append(out, SlurmMapping{t, "cpu request/limit " + val})
			case "--mem":
				if r.Memory == "" {
					r.Memory = slurmMem(val)
				}
				out = append(out, SlurmMapping{t, "memory request/limit " + slurmMem(val)})
			case "--time", "-t":
				h := slurmHours(val)
				if r.Hours == 0 && h > 0 {
					r.Hours = h
				}
				out = append(out, SlurmMapping{t, fmt.Sprintf("activeDeadlineSeconds %d (%d h)", h*3600, h)})
			case "--array", "-a":
				ids, throttle := arrayIDs(val)
				n := len(ids)
				if n > 0 && r.Count == 0 {
					r.Count = n
					r.ArrayIDs = ids
					if r.Goal == "" || r.Goal == GoalJob {
						r.Goal = GoalSweep
					}
				}
				if throttle > 0 && r.Parallel == 0 {
					r.Parallel = throttle
				}
				msg := fmt.Sprintf("Indexed Job with %d tasks; each task gets its own $SLURM_ARRAY_TASK_ID (%s), set from $JOB_COMPLETION_INDEX", n, idRange(ids))
				if throttle > 0 {
					msg += fmt.Sprintf("; %%%d becomes parallelism %d", throttle, throttle)
				}
				out = append(out, SlurmMapping{t, msg})
			case "--partition", "-p", "--account", "-A", "--qos":
				out = append(out, SlurmMapping{t, "no equivalent: Kubernetes picks a node that fits the request"})
			case "--job-name", "-J":
				if r.Name == "" {
					r.Name = val
				}
				out = append(out, SlurmMapping{t, "object name " + DNSName(val)})
			case "--nodes", "-N", "--ntasks", "-n":
				out = append(out, SlurmMapping{t, "multi-node MPI is better on the HPCC; on Nautilus use an Indexed Job for independent tasks"})
			default:
				out = append(out, SlurmMapping{t, "ignored"})
			}
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "module ") || strings.HasPrefix(t, "source activate") || strings.HasPrefix(t, "conda activate") {
			out = append(out, SlurmMapping{t, "replaced by the container image"})
			continue
		}
		cmds = append(cmds, t)
	}
	if r.Command == "" && len(cmds) > 0 {
		r.Command = strings.Join(cmds, " && ")
	}
	return out
}

func slurmMem(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	switch {
	case strings.HasSuffix(v, "G"):
		return strings.TrimSuffix(v, "G") + "Gi"
	case strings.HasSuffix(v, "M"):
		return strings.TrimSuffix(v, "M") + "Mi"
	case strings.HasSuffix(v, "T"):
		return strings.TrimSuffix(v, "T") + "Ti"
	}
	return v + "Mi"
}

func slurmHours(v string) int {
	days := 0
	if i := strings.Index(v, "-"); i >= 0 {
		days, _ = strconv.Atoi(v[:i])
		v = v[i+1:]
	}
	parts := strings.Split(v, ":")
	h := 0
	switch len(parts) {
	case 3:
		h, _ = strconv.Atoi(parts[0])
		if m, _ := strconv.Atoi(parts[1]); m > 0 {
			h++
		}
	case 2:
		m, _ := strconv.Atoi(parts[0])
		h = (m + 59) / 60
	case 1:
		m, _ := strconv.Atoi(parts[0])
		h = (m + 59) / 60
	}
	return days*24 + h
}

// arrayIDs expands a Slurm --array value ("0-9", "1-50%10", "1,3,7", "0-20:2") into its
// task ids, in order, and returns the "%N" concurrency limit (0 if none). Ids are capped
// at maxArrayTasks so a typo cannot plan a million pods.
func arrayIDs(v string) ([]int, int) {
	throttle := 0
	if i := strings.Index(v, "%"); i >= 0 {
		throttle, _ = strconv.Atoi(v[i+1:])
		v = v[:i]
	}
	var ids []int
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		step := 1
		if i := strings.Index(part, ":"); i >= 0 {
			if s, err := strconv.Atoi(part[i+1:]); err == nil && s > 0 {
				step = s
			}
			part = part[:i]
		}
		a, b := part, part
		if i := strings.Index(part, "-"); i > 0 {
			a, b = part[:i], part[i+1:]
		}
		lo, err1 := strconv.Atoi(a)
		hi, err2 := strconv.Atoi(b)
		if err1 != nil || err2 != nil || hi < lo {
			continue
		}
		for x := lo; x <= hi && len(ids) < maxArrayTasks; x += step {
			ids = append(ids, x)
		}
	}
	return ids, throttle
}

const maxArrayTasks = 10000

// idRange describes ids for people: "0..9", "1..50", "0..20 step 2", or "1, 3, 7".
func idRange(ids []int) string {
	if len(ids) == 0 {
		return "none"
	}
	if lo, step, ok := arithmetic(ids); ok {
		hi := ids[len(ids)-1]
		if step == 1 {
			return fmt.Sprintf("%d..%d", lo, hi)
		}
		return fmt.Sprintf("%d..%d step %d", lo, hi, step)
	}
	s := make([]string, len(ids))
	for i, x := range ids {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ", ")
}

// arithmetic reports whether ids is lo, lo+step, lo+2*step, ...
func arithmetic(ids []int) (lo, step int, ok bool) {
	if len(ids) == 0 {
		return 0, 0, false
	}
	lo, step = ids[0], 1
	if len(ids) > 1 {
		step = ids[1] - ids[0]
	}
	if step <= 0 {
		return 0, 0, false
	}
	for i, x := range ids {
		if x != lo+i*step {
			return 0, 0, false
		}
	}
	return lo, step, true
}

// arrayEnv is the shell prefix that gives each sweep task Slurm's array variables, so a
// script written for Slurm (reading $SLURM_ARRAY_TASK_ID) gets a different id per task
// instead of the same empty value. Kubernetes numbers tasks 0..n-1 in
// $JOB_COMPLETION_INDEX; Slurm ids can start at 1, skip by a step, or be a list.
func arrayEnv(ids []int, n int) string {
	if len(ids) != n {
		ids = make([]int, n)
		for i := range ids {
			ids[i] = i
		}
	}
	var id string
	if lo, step, ok := arithmetic(ids); ok {
		switch {
		case lo == 0 && step == 1:
			id = "$JOB_COMPLETION_INDEX"
		case step == 1:
			id = fmt.Sprintf("$((%d + JOB_COMPLETION_INDEX))", lo)
		default:
			id = fmt.Sprintf("$((%d + JOB_COMPLETION_INDEX * %d))", lo, step)
		}
	} else {
		s := make([]string, len(ids))
		for i, x := range ids {
			s[i] = strconv.Itoa(x)
		}
		id = "$(set -- " + strings.Join(s, " ") + "; shift $JOB_COMPLETION_INDEX; echo $1)"
	}
	mn, mx := ids[0], ids[0]
	for _, x := range ids {
		mn, mx = min(mn, x), max(mx, x)
	}
	return fmt.Sprintf("export SLURM_ARRAY_TASK_ID=%s SLURM_ARRAY_TASK_COUNT=%d SLURM_ARRAY_TASK_MIN=%d SLURM_ARRAY_TASK_MAX=%d; ", id, n, mn, mx)
}

func buildVolume(r Request, p *Plan) error {
	if !r.DataIsP1 {
		return fmt.Errorf("Nautilus storage is for non-sensitive (P1) data only. Confirm with data_is_p1=true that nothing in it is HIPAA, FERPA, PII, CUI or under a restrictive data-use agreement")
	}
	size := r.Size
	if size == "" {
		size = "20Gi"
	}
	if _, err := k8s.ParseBytes(size); err != nil {
		return fmt.Errorf("bad size %q (use for example 50Gi)", size)
	}
	if r.Name == "" {
		p.Name = "data-" + shortRun(p.Run)
	}
	p.Labels["nrp-mcp/name"] = p.Name
	p.Objects = []k8s.Object{{APIVersion: "v1", Kind: "PersistentVolumeClaim", Metadata: k8s.Meta{Name: p.Name, Namespace: r.Namespace, Labels: copyLabels(p.Labels), Annotations: owner(r)},
		PVCSpec: &k8s.PVCSpec{AccessModes: []string{"ReadWriteMany"}, StorageClassName: "rook-cephfs-central", Resources: k8s.Resources{Requests: map[string]string{"storage": size}}}}}
	p.Image, p.ImageWhy = "", ""
	p.Decisions = append(p.Decisions,
		"A shared CephFS volume (rook-cephfs-central, ReadWriteMany) so Jobs, sweeps and sessions can all mount it at once (about 86 MiB/s per writer in a UCR test).",
		"Not for pip/conda installs or tens of thousands of small files (CephFS metadata is slow); pack small files into archives.",
		"Volumes not accessed for 6 months can be purged without notice; keep the master copy elsewhere (CephRDS, HPCC storage).",
		"Use it with data_volume="+p.Name+" in nrp_plan.")
	p.Summary = fmt.Sprintf("A %s shared volume %q in %s (CephFS, central region).", size, p.Name, r.Namespace)
	p.Kubectl = []string{fmt.Sprintf("kubectl -n %s apply -f plan.json", r.Namespace), fmt.Sprintf("kubectl -n %s get pvc %s", r.Namespace, p.Name)}
	return nil
}

var urlRe = regexp.MustCompile(`^https?://[^\s'"]+$`)

func buildPull(r Request, p *Plan) error {
	if !r.DataIsP1 {
		return fmt.Errorf("Nautilus storage is for non-sensitive (P1) data only. Confirm with data_is_p1=true that the files are not HIPAA, FERPA, PII, CUI or under a restrictive data-use agreement")
	}
	if len(r.URLs) == 0 {
		return fmt.Errorf("pull needs urls (http or https links to the files)")
	}
	if r.DataPVC == "" {
		return fmt.Errorf("pull needs data_volume (an existing volume; make one with goal volume)")
	}
	for _, u := range r.URLs {
		if !urlRe.MatchString(u) {
			return fmt.Errorf("not a plain http(s) URL: %q", u)
		}
	}
	dir := "/data"
	if r.Subdir != "" {
		sd := DNSName(strings.ReplaceAll(r.Subdir, "/", "-"))
		dir += "/" + sd
	}
	var b strings.Builder
	b.WriteString("set -e; mkdir -p " + dir + "; cd " + dir + "; ")
	for i, u := range r.URLs {
		fmt.Fprintf(&b, "echo 'file %d/%d'; curl -fL --retry 5 --retry-delay 5 -O '%s'; ", i+1, len(r.URLs), u)
	}
	b.WriteString("ls -la " + dir)
	if r.Name == "" {
		p.Name = "pull-" + shortRun(p.Run)
	}
	p.Labels["nrp-mcp/name"] = p.Name
	ctr := k8s.Container{Name: "pull", Image: "curlimages/curl:latest", Command: []string{"sh", "-c", b.String()},
		Resources:    k8s.Resources{Requests: map[string]string{"cpu": "1", "memory": "2Gi"}, Limits: map[string]string{"cpu": "1", "memory": "2Gi"}},
		VolumeMounts: []k8s.Mount{{Name: "data", MountPath: "/data"}}}
	hours := r.Hours
	if hours == 0 {
		hours = 12
	}
	js := &k8s.JobSpec{BackoffLimit: k8s.IntPtr(2), TTLSecondsAfterFinished: k8s.IntPtr(86400), ActiveDeadlineSeconds: k8s.IntPtr(hours * 3600),
		Template: k8s.PodTemplate{Metadata: k8s.Meta{Labels: copyLabels(p.Labels)}, Spec: k8s.PodSpec{RestartPolicy: "Never", Containers: []k8s.Container{ctr},
			Volumes: []k8s.Volume{{Name: "data", PersistentVolumeClaim: &k8s.PVCSource{ClaimName: r.DataPVC}}}}}}
	p.Objects = []k8s.Object{{APIVersion: "batch/v1", Kind: "Job", Metadata: k8s.Meta{Name: p.Name, Namespace: r.Namespace, Labels: copyLabels(p.Labels), Annotations: owner(r)}, JobSpec: js}}
	p.Image, p.ImageWhy = "curlimages/curl:latest", "small image with curl"
	p.Decisions = append(p.Decisions,
		fmt.Sprintf("Downloads %d file(s) inside the cluster straight into volume %s at %s, so nothing large passes through your laptop or the Kubernetes API.", len(r.URLs), r.DataPVC, dir),
		"Retries each file 5 times; stops after "+strconv.Itoa(hours)+" h.")
	p.Summary = fmt.Sprintf("A download Job pulling %d file(s) into %s:%s.", len(r.URLs), r.DataPVC, strings.TrimPrefix(dir, "/data"))
	p.Kubectl = []string{fmt.Sprintf("kubectl -n %s logs -f job/%s", r.Namespace, p.Name)}
	return nil
}

// Cleanup makes a cleanup plan for a run (or "all" nrp-mcp objects).
func Cleanup(ns, run string, objs []string) *Plan {
	p := &Plan{ID: newID("c"), Created: time.Now().UTC(), Goal: GoalCleanup, Namespace: ns, Run: run, Extra: map[string]any{"targets": objs}}
	p.Summary = fmt.Sprintf("Delete %d nrp-mcp object(s) in %s (run %s).", len(objs), ns, run)
	p.Hash = Hash(p)
	return p
}

// pullSecrets returns imagePullSecrets for a private image and explains the choice.
func pullSecrets(r Request, p *Plan) []k8s.NameRef {
	if r.PullSecret != "" {
		p.Decisions = append(p.Decisions, "Pulls the image with the Secret "+r.PullSecret+" (a read-only registry token kept in your namespace, never in the spec).")
		return []k8s.NameRef{{Name: r.PullSecret}}
	}
	if strings.HasPrefix(p.Image, "ghcr.io/") || strings.HasPrefix(p.Image, "gitlab-registry.nrp-nautilus.io/") {
		p.Decisions = append(p.Decisions, "If the image is private, make the package public or pass pull_secret=<secret> (nrp_build explains both); otherwise the pod stops at ImagePullBackOff.")
	}
	return nil
}
