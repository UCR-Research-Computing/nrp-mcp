// Package ops does the cluster work behind the tools: status, watch, run, cleanup,
// session and data moves. All calls go through kube.Runner as the signed-in user.
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/diagnose"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/kube"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/plan"
)

// Ops wraps a kubectl runner.
type Ops struct {
	K *kube.Runner
}

// Workload is one thing running (or done) in a namespace.
type Workload struct {
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Managed   bool              `json:"managed_by_nrp_mcp"`
	Run       string            `json:"run_id,omitempty"`
	Status    string            `json:"status"`
	Age       string            `json:"age"`
	Requests  map[string]string `json:"requests,omitempty"`
	PublicURL string            `json:"public_url,omitempty"`
	Warning   string            `json:"warning,omitempty"`
}

// Status is the nrp_status result.
type Status struct {
	Summary    string            `json:"summary"`
	User       string            `json:"user"`
	Namespaces []string          `json:"namespaces"`
	AdminOf    []string          `json:"admin_of,omitempty"`
	Namespace  string            `json:"namespace"`
	GPUQuota   map[string]int    `json:"special_gpu_quota"`
	Defaults   map[string]string `json:"container_defaults,omitempty"`
	Workloads  []Workload        `json:"workloads"`
	Volumes    []Workload        `json:"volumes"`
	PublicURLs []string          `json:"public_urls"`
	Warnings   []string          `json:"warnings"`
	Kubectl    []string          `json:"kubectl_equivalent"`
}

type objList struct {
	Items []json.RawMessage `json:"items"`
}

type metaOnly struct {
	Metadata struct {
		Name              string            `json:"name"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
	} `json:"metadata"`
}

func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// StatusOf gathers nrp_status for ns (empty: the first namespace the user belongs to).
func (o *Ops) StatusOf(ctx context.Context, ns string) (*Status, error) {
	w, err := o.K.WhoAmI(ctx)
	if err != nil {
		return nil, signInHint(err)
	}
	s := &Status{User: w.Username, Namespaces: w.Namespaces, AdminOf: w.Admin, GPUQuota: map[string]int{}, Workloads: []Workload{}, Volumes: []Workload{}, PublicURLs: []string{}, Warnings: []string{}}
	if ns == "" && len(w.Namespaces) > 0 {
		ns = w.Namespaces[0]
	}
	s.Namespace = ns
	if ns == "" {
		s.Summary = "You are signed in to Nautilus but are not in any namespace yet. Ask your PI or namespace admin to add you (students), or request a namespace at https://nrp.ai/namespaces (faculty, staff, postdocs). See KB024."
		return s, nil
	}
	if qs, err := o.K.Quotas(ctx, ns); err == nil {
		s.GPUQuota = kube.GPUQuota(qs)
	}
	var lr struct {
		Items []struct {
			Spec struct {
				Limits []struct {
					Type           string            `json:"type"`
					Default        map[string]string `json:"default"`
					DefaultRequest map[string]string `json:"defaultRequest"`
				} `json:"limits"`
			} `json:"spec"`
		} `json:"items"`
	}
	if o.K.JSON(ctx, ns, &lr, "get", "limitrange") == nil {
		s.Defaults = map[string]string{}
		for _, i := range lr.Items {
			for _, l := range i.Spec.Limits {
				if l.Type == "Container" {
					for k, v := range l.DefaultRequest {
						s.Defaults["request."+k] = v
					}
					for k, v := range l.Default {
						s.Defaults["limit."+k] = v
					}
				}
			}
		}
	}
	var jobs, deps, pods, ings, pvcs struct {
		Items []map[string]any `json:"items"`
	}
	_ = o.K.JSON(ctx, ns, &jobs, "get", "jobs")
	_ = o.K.JSON(ctx, ns, &deps, "get", "deployments")
	_ = o.K.JSON(ctx, ns, &pods, "get", "pods")
	_ = o.K.JSON(ctx, ns, &ings, "get", "ingress")
	_ = o.K.JSON(ctx, ns, &pvcs, "get", "pvc")
	owned := map[string]bool{}
	for _, j := range jobs.Items {
		wl := wlFrom("Job", j)
		st := mapGet(j, "status")
		switch {
		case num(st, "succeeded") > 0 && num(st, "active") == 0:
			wl.Status = fmt.Sprintf("done (%d succeeded)", int(num(st, "succeeded")))
		case num(st, "failed") > 0 && num(st, "active") == 0:
			wl.Status = fmt.Sprintf("failed (%d failed)", int(num(st, "failed")))
		default:
			wl.Status = fmt.Sprintf("running (%d active, %d done)", int(num(st, "active")), int(num(st, "succeeded")))
		}
		wl.Requests = podRequests(mapGet(mapGet(mapGet(mapGet(j, "spec"), "template"), "spec"), ""))
		s.Workloads = append(s.Workloads, wl)
		owned[wl.Name] = true
	}
	for _, d := range deps.Items {
		wl := wlFrom("Deployment", d)
		st := mapGet(d, "status")
		wl.Status = fmt.Sprintf("%d/%d ready", int(num(st, "readyReplicas")), int(num(mapGet(d, "spec"), "replicas")))
		if t := created(d); !t.IsZero() {
			left := 14*24*time.Hour - time.Since(t)
			if left < 3*24*time.Hour {
				wl.Warning = fmt.Sprintf("the NRP removes Deployments after 2 weeks: about %d days left unless your namespace has an exception", int(left.Hours()/24))
				s.Warnings = append(s.Warnings, "Deployment "+wl.Name+": "+wl.Warning)
			}
		}
		s.Workloads = append(s.Workloads, wl)
	}
	for _, p := range pods.Items {
		md := mapGet(p, "metadata")
		if refs, ok := md["ownerReferences"].([]any); ok && len(refs) > 0 {
			continue
		}
		wl := wlFrom("Pod", p)
		wl.Status, _ = mapGet(p, "status")["phase"].(string)
		if t := created(p); !t.IsZero() && time.Since(t) > 5*time.Hour {
			wl.Warning = "bare pods are deleted after 6 hours"
			s.Warnings = append(s.Warnings, "Pod "+wl.Name+": "+wl.Warning)
		}
		s.Workloads = append(s.Workloads, wl)
	}
	for _, i := range ings.Items {
		rules, _ := mapGet(i, "spec")["rules"].([]any)
		for _, r := range rules {
			if rm, ok := r.(map[string]any); ok {
				if h, _ := rm["host"].(string); h != "" {
					s.PublicURLs = append(s.PublicURLs, "https://"+h+"/")
				}
			}
		}
	}
	for _, v := range pvcs.Items {
		wl := wlFrom("PersistentVolumeClaim", v)
		wl.Status, _ = mapGet(v, "status")["phase"].(string)
		sp := mapGet(v, "spec")
		sc, _ := sp["storageClassName"].(string)
		wl.Requests = map[string]string{"storage": fmt.Sprint(mapGet(mapGet(sp, "resources"), "requests")["storage"]), "class": sc}
		s.Volumes = append(s.Volumes, wl)
	}
	if q, ok := s.GPUQuota["nvidia.com/a100"]; ok && q == 0 {
		s.Warnings = append(s.Warnings, "A100/H100/H200/GH200 quota is 0 here: use nvidia.com/gpu (any standard GPU) or priorityClassName opportunistic.")
	}
	sort.Strings(s.PublicURLs)
	running := 0
	for _, wl := range s.Workloads {
		if strings.HasPrefix(wl.Status, "running") || strings.Contains(wl.Status, "ready") || wl.Status == "Running" {
			running++
		}
	}
	s.Summary = fmt.Sprintf("Signed in as %s. Namespace %s (you are in %d: %s). %d workloads (%d active), %d volumes, %d public URLs, %d warnings.",
		shortUser(w.Username), ns, len(w.Namespaces), strings.Join(w.Namespaces, ", "), len(s.Workloads), running, len(s.Volumes), len(s.PublicURLs), len(s.Warnings))
	s.Kubectl = []string{"kubectl auth whoami", "kubectl -n " + ns + " get resourcequota,limitrange", "kubectl -n " + ns + " get jobs,deployments,pods,pvc,ingress"}
	return s, nil
}

func shortUser(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return "CILogon user " + u[i+1:]
	}
	return u
}

func signInHint(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "executable file not found") || strings.Contains(msg, "no such file"):
		return fmt.Errorf("kubectl is not installed or not on PATH; nrp_setup can install it (or `nrp-mcp setup` in a terminal): %w", err)
	case strings.Contains(msg, "oidc-login") || strings.Contains(msg, "unknown command"):
		return fmt.Errorf("the kubelogin sign-in plugin (kubectl-oidc_login) is missing; nrp_setup can install it: %w", err)
	case strings.Contains(msg, "context") && strings.Contains(msg, "does not exist"):
		return fmt.Errorf("your kubeconfig has no Nautilus context; download the NRP config from https://nrp.ai/config and run nrp_setup: %w", err)
	}
	return fmt.Errorf("could not reach Nautilus as you (run `kubectl auth whoami` in a terminal once to sign in in the browser): %w", err)
}

func mapGet(m map[string]any, k string) map[string]any {
	if k == "" {
		return m
	}
	if m == nil {
		return nil
	}
	v, _ := m[k].(map[string]any)
	return v
}

func num(m map[string]any, k string) float64 {
	if m == nil {
		return 0
	}
	f, _ := m[k].(float64)
	return f
}

func created(m map[string]any) time.Time {
	s, _ := mapGet(m, "metadata")["creationTimestamp"].(string)
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func wlFrom(kind string, m map[string]any) Workload {
	md := mapGet(m, "metadata")
	name, _ := md["name"].(string)
	wl := Workload{Kind: kind, Name: name, Age: age(created(m))}
	if l, ok := md["labels"].(map[string]any); ok {
		if v, _ := l["app.kubernetes.io/managed-by"].(string); v == plan.ManagedBy {
			wl.Managed = true
		}
		wl.Run, _ = l["nrp-mcp/run"].(string)
	}
	return wl
}

func podRequests(spec map[string]any) map[string]string {
	out := map[string]string{}
	cs, _ := spec["containers"].([]any)
	for _, c := range cs {
		cm, _ := c.(map[string]any)
		rq := mapGet(mapGet(cm, "resources"), "requests")
		for k, v := range rq {
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

// WatchResult is the nrp_watch result.
type WatchResult struct {
	Summary  string             `json:"summary"`
	Target   string             `json:"target"`
	Progress string             `json:"progress,omitempty"`
	Pods     []diagnose.Finding `json:"pods"`
	Logs     string             `json:"logs,omitempty"`
	URLCheck string             `json:"url_check,omitempty"`
	Kubectl  []string           `json:"kubectl_equivalent"`
}

// Watch reports on a run id, a Job/Deployment name, or a pod name.
func (o *Ops) Watch(ctx context.Context, ns, target string, tail int, grep string, httpGet func(string) (int, error)) (*WatchResult, error) {
	if tail <= 0 {
		tail = 40
	}
	res := &WatchResult{Target: target, Pods: []diagnose.Finding{}}
	selector := ""
	var podsJSON struct {
		Items []map[string]any `json:"items"`
	}
	// Resolve target: run id label, job, deployment, or pod.
	tries := [][]string{
		{"get", "pods", "-l", "nrp-mcp/run=" + target},
		{"get", "pods", "-l", "job-name=" + target},
		{"get", "pods", "-l", "nrp-mcp/name=" + target},
	}
	for _, t := range tries {
		if err := o.K.JSON(ctx, ns, &podsJSON, t...); err == nil && len(podsJSON.Items) > 0 {
			selector = t[3]
			break
		}
	}
	if selector == "" {
		var one map[string]any
		if err := o.K.JSON(ctx, ns, &one, "get", "pod", target); err == nil {
			podsJSON.Items = []map[string]any{one}
		}
	}
	if len(podsJSON.Items) == 0 {
		var j map[string]any
		if err := o.K.JSON(ctx, ns, &j, "get", "job", target); err == nil {
			res.Summary = "The Job exists but has no pods yet (or they were cleaned up after finishing)."
			return res, nil
		}
		return nil, fmt.Errorf("nothing called %q in %s (give a run id from nrp_run, a Job or Deployment name, or a pod name; nrp_status lists them)", target, ns)
	}
	var evs struct {
		Items []map[string]any `json:"items"`
	}
	_ = o.K.JSON(ctx, ns, &evs, "get", "events")
	counts := map[string]int{}
	var newest string
	var newestT time.Time
	for _, pm := range podsJSON.Items {
		p := toDiagPod(pm, evs.Items)
		f := diagnose.Explain(p)
		res.Pods = append(res.Pods, f)
		counts[f.State]++
		if t := created(pm); t.After(newestT) || newest == "" {
			newest, newestT = p.Name, t
		}
	}
	// Prefer logs from a failing pod.
	logPod := newest
	for _, f := range res.Pods {
		if f.State == "Failed" || f.State == "OOMKilled" {
			logPod = f.Pod
			break
		}
	}
	if out, err := o.K.Run(ctx, ns, nil, "logs", logPod, "--all-containers", fmt.Sprintf("--tail=%d", tail)); err == nil {
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if grep != "" {
			var keep []string
			for _, l := range lines {
				if strings.Contains(strings.ToLower(l), strings.ToLower(grep)) {
					keep = append(keep, l)
				}
			}
			lines = keep
		}
		res.Logs = "[" + logPod + "]\n" + strings.Join(lines, "\n")
	}
	var parts []string
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	res.Progress = strings.Join(parts, ", ")
	first := res.Pods[0]
	for _, f := range res.Pods {
		if f.Fix != "" {
			first = f
			break
		}
	}
	res.Summary = fmt.Sprintf("%d pod(s): %s. %s", len(res.Pods), res.Progress, first.Explain)
	if first.Fix != "" {
		res.Summary += " Fix: " + first.Fix
	}
	// Public URL check for web workloads: find Ingresses by the same label selector
	// (a run id, a Job name or a workload name all resolve), or by name.
	if httpGet != nil && selector != "" {
		var ings struct {
			Items []map[string]any `json:"items"`
		}
		_ = o.K.JSON(ctx, ns, &ings, "get", "ingress", "-l", selector)
		if len(ings.Items) == 0 {
			var one map[string]any
			if o.K.JSON(ctx, ns, &one, "get", "ingress", target) == nil {
				ings.Items = []map[string]any{one}
			}
		}
		var checks []string
		for _, ing := range ings.Items {
			rules, _ := mapGet(ing, "spec")["rules"].([]any)
			for _, r := range rules {
				rm, _ := r.(map[string]any)
				h, _ := rm["host"].(string)
				if h == "" {
					continue
				}
				if code, err := httpGet("https://" + h + "/"); err == nil {
					checks = append(checks, fmt.Sprintf("https://%s/ answered HTTP %d", h, code))
				} else {
					checks = append(checks, fmt.Sprintf("https://%s/ did not answer yet: %v", h, err))
				}
			}
		}
		res.URLCheck = strings.Join(checks, "; ")
		if res.URLCheck != "" {
			res.Summary += " Public URL: " + res.URLCheck + "."
		}
	}
	res.Kubectl = []string{fmt.Sprintf("kubectl -n %s get pods -l %s", ns, selector), fmt.Sprintf("kubectl -n %s describe pod %s", ns, logPod), fmt.Sprintf("kubectl -n %s logs %s --tail=%d", ns, logPod, tail)}
	return res, nil
}

func toDiagPod(pm map[string]any, events []map[string]any) diagnose.Pod {
	md := mapGet(pm, "metadata")
	st := mapGet(pm, "status")
	p := diagnose.Pod{}
	p.Name, _ = md["name"].(string)
	p.Phase, _ = st["phase"].(string)
	p.Reason, _ = st["reason"].(string)
	p.Message, _ = st["message"].(string)
	p.Node, _ = mapGet(pm, "spec")["nodeName"].(string)
	cs, _ := st["containerStatuses"].([]any)
	ics, _ := st["initContainerStatuses"].([]any)
	for _, c := range append(ics, cs...) {
		cm, _ := c.(map[string]any)
		dc := diagnose.Container{}
		dc.Name, _ = cm["name"].(string)
		dc.Ready, _ = cm["ready"].(bool)
		dc.Restarts = int(num(cm, "restartCount"))
		state := mapGet(cm, "state")
		if w := mapGet(state, "waiting"); w != nil {
			dc.Waiting, _ = w["reason"].(string)
			dc.WaitingMsg, _ = w["message"].(string)
		}
		if t := mapGet(state, "terminated"); t != nil {
			dc.Terminated, _ = t["reason"].(string)
			dc.ExitCode = int(num(t, "exitCode"))
		}
		if t := mapGet(mapGet(cm, "lastState"), "terminated"); t != nil {
			dc.LastTermRsn, _ = t["reason"].(string)
			dc.LastExitCode = int(num(t, "exitCode"))
		}
		p.Containers = append(p.Containers, dc)
	}
	for _, e := range events {
		io := mapGet(e, "involvedObject")
		if n, _ := io["name"].(string); n != p.Name {
			continue
		}
		ev := diagnose.Event{}
		ev.Type, _ = e["type"].(string)
		ev.Reason, _ = e["reason"].(string)
		ev.Message, _ = e["message"].(string)
		p.Events = append(p.Events, ev)
	}
	return p
}

// CodeConfigMap packs small project text files into a ConfigMap (max ~900 KB) and
// returns it with the init step that unpacks it into /work.
func CodeConfigMap(project, name, ns string, labels map[string]string) (*k8s.Object, []string, error) {
	if project == "" {
		return nil, nil, nil
	}
	data := map[string]string{}
	var files []string
	total := 0
	err := filepath.WalkDir(project, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(project, p)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "__pycache__" || d.Name() == "venv" || d.Name() == "data" || d.Name() == "results") {
				return filepath.SkipDir
			}
			return nil
		}
		low := strings.ToLower(d.Name())
		if strings.HasPrefix(low, ".env") || strings.Contains(low, "secret") || strings.Contains(low, "token") || strings.HasSuffix(low, ".pem") || strings.HasSuffix(low, ".key") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(low))
		okExt := map[string]bool{".py": true, ".r": true, ".jl": true, ".sh": true, ".txt": true, ".toml": true, ".yml": true, ".yaml": true,
			".json": true, ".js": true, ".ts": true, ".html": true, ".css": true, ".md": true, ".cfg": true, ".ini": true, ".csv": true, "": true, ".lock": true}
		if !okExt[ext] {
			return nil
		}
		info, e := d.Info()
		if e != nil || info.Size() > 200*1024 {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		total += len(b)
		if total > 900*1024 {
			return fmt.Errorf("project text files exceed 900 KB; use nrp_build (an image) or put the code in git")
		}
		key := strings.ReplaceAll(rel, string(os.PathSeparator), "__SLASH__")
		key = strings.ReplaceAll(key, "/", "__SLASH__")
		data[key] = string(b)
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	cm := &k8s.Object{APIVersion: "v1", Kind: "ConfigMap", Metadata: k8s.Meta{Name: name, Namespace: ns, Labels: labels}, Data: data}
	return cm, files, nil
}

// unpackScript is the init container script that rebuilds the project tree in /work.
const unpackScript = `set -e; cd /code; for f in *; do d="/work/$(echo "$f" | sed 's/__SLASH__/\//g')"; mkdir -p "$(dirname "$d")"; cp "$f" "$d"; done; ls -R /work | head -50`

// AttachCode adds the ConfigMap volume and an init container that unpacks it into the
// existing /work volume of every pod spec in objs.
func AttachCode(objs []k8s.Object, cmName string) {
	for i := range objs {
		ps := objs[i].PodSpecOf()
		if ps == nil {
			continue
		}
		hasWork := false
		for _, v := range ps.Volumes {
			if v.Name == "code" {
				hasWork = true
			}
		}
		if !hasWork {
			continue
		}
		ps.Volumes = append(ps.Volumes, k8s.Volume{Name: "code-src", ConfigMap: &k8s.ConfigMapSource{Name: cmName}})
		ps.InitContainers = append(ps.InitContainers, k8s.Container{Name: "unpack-code", Image: "busybox:1.36",
			Command:      []string{"sh", "-c", unpackScript},
			Resources:    k8s.Resources{Requests: map[string]string{"cpu": "100m", "memory": "64Mi"}, Limits: map[string]string{"cpu": "100m", "memory": "64Mi"}},
			VolumeMounts: []k8s.Mount{{Name: "code-src", MountPath: "/code", ReadOnly: true}, {Name: "code", MountPath: "/work"}}})
	}
}

// Apply applies objects, dry-run first; returns kubectl's output lines.
func (o *Ops) Apply(ctx context.Context, ns string, objs []k8s.Object) (string, error) {
	b, err := k8s.List(objs)
	if err != nil {
		return "", err
	}
	if out, err := o.K.Apply(ctx, ns, b, "server"); err != nil {
		return out, fmt.Errorf("Nautilus rejected the plan in a dry run (nothing was created): %w", err)
	}
	return o.K.Apply(ctx, ns, b, "")
}

// Scope says whose objects cleanup may see: one owner (OwnerID) or, for a namespace
// admin who asks, everyone's ("" = all).
type Scope struct {
	Owner string
}

func (s Scope) selector(base string) string {
	if s.Owner == "" {
		return base
	}
	return base + "," + plan.OwnerLabel + "=" + s.Owner
}

// CleanupList lists nrp-mcp objects (all or one run) that can be removed, limited to
// the scope's owner.
func (o *Ops) CleanupList(ctx context.Context, ns, run string, sc Scope) ([]Workload, error) {
	sel := sc.selector("app.kubernetes.io/managed-by=" + plan.ManagedBy)
	if run != "" {
		sel = sc.selector("nrp-mcp/run=" + run)
	}
	var out []Workload
	for _, kind := range []string{"job", "deployment", "service", "ingress", "configmap", "secret", "pod", "pvc"} {
		var l struct {
			Items []map[string]any `json:"items"`
		}
		if err := o.K.JSON(ctx, ns, &l, "get", kind, "-l", sel); err != nil {
			continue
		}
		for _, it := range l.Items {
			if kind == "pod" {
				// Job and Deployment pods go with their owner; list only bare pods (data helpers).
				if refs, ok := mapGet(it, "metadata")["ownerReferences"].([]any); ok && len(refs) > 0 {
					continue
				}
			}
			wl := wlFrom(strings.ToUpper(kind[:1])+kind[1:], it)
			if !wl.Managed && run == "" {
				continue
			}
			out = append(out, wl)
		}
	}
	return out, nil
}

// Delete removes objects by label selector (never cluster-wide, never unlabeled).
func (o *Ops) Delete(ctx context.Context, ns, run string, sc Scope) (string, error) {
	if run == "" {
		return "", fmt.Errorf("a run id is required")
	}
	sel := sc.selector("nrp-mcp/run=" + run + ",app.kubernetes.io/managed-by=" + plan.ManagedBy)
	// Pods first (data helpers hold volumes), then everything else.
	out1, _ := o.K.Run(ctx, ns, nil, "delete", "pod", "-l", sel, "--wait=false")
	out, err := o.K.Run(ctx, ns, nil, "delete", "job,deployment,service,ingress,configmap,secret,pvc", "-l", sel, "--wait=false")
	out = append(out1, out...)
	return strings.TrimSpace(string(out)), err
}
