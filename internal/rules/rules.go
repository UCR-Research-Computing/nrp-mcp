// Package rules checks manifests against the NRP Nautilus cluster policies. It is a pure
// function of the manifests and a little context, so every rule has a unit test. Each
// finding says what is wrong, why (the NRP source), and how to fix it.
package rules

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
)

// Severity of a finding.
type Severity string

const (
	// Refuse blocks the plan (no confirm token is issued).
	Refuse Severity = "refuse"
	// Warn is shown but does not block.
	Warn Severity = "warn"
)

// Finding is one rule result.
type Finding struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Object   string   `json:"object,omitempty"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Source   string   `json:"source"`
}

// Context is what the rules need besides the manifests.
type Context struct {
	// GPUQuota is the namespace hard quota per special GPU resource, e.g.
	// {"nvidia.com/a100": 0}. Missing means no quota applies.
	GPUQuota map[string]int
	// Caps from the user's config.
	PodsPerRun  int
	GPUsPerRun  int
	HoursPerRun int
	// SweepCount is the number of tasks for an Indexed Job plan (0 if none).
	SweepCount int
	// Session is true for nrp_session plans (an interactive Deployment that may hold a GPU).
	Session bool
}

// Sources (NRP documentation pages).
const (
	srcPolicies  = "https://nrp.ai/documentation/userdocs/start/policies/"
	srcGPU       = "https://nrp.ai/documentation/userdocs/running/gpu-pods/"
	srcPriority  = "https://nrp.ai/documentation/userdocs/running/priority-classes/"
	srcLongIdle  = "https://nrp.ai/documentation/userdocs/running/long-idle/"
	srcScheduler = "https://nrp.ai/documentation/userdocs/running/scheduling/"
	srcIngress   = "https://nrp.ai/documentation/userdocs/running/ingress/"
	srcStorage   = "https://nrp.ai/documentation/userdocs/storage/intro/"
	srcTested    = "UCR test 2026-10-05: ceph-rbd PVCs never bind"
	srcConfig    = "nrp-mcp config caps (~/.config/nrp-mcp/config.yaml)"
)

// SpecialGPUs are quota-gated GPU resources.
var SpecialGPUs = []string{"nvidia.com/a100", "nvidia.com/h100", "nvidia.com/h200", "nvidia.com/gh200"}

// GPUResources are all GPU resource names a pod may request.
var GPUResources = append([]string{"nvidia.com/gpu", "nvidia.com/a40", "nvidia.com/rtxa6000", "nvidia.com/rtx8000",
	"nvidia.com/rtx6000bw", "nvidia.com/mig-small"}, SpecialGPUs...)

var allowedPriority = map[string]bool{"": true, "armada-default": true, "owner-no-preempt": true, "opportunistic": true, "opportunistic2": true}

var sleepRe = regexp.MustCompile(`(^|[;&|]\s*|\s)(sleep\s+(infinity|inf|\d{4,})|tail\s+-f\s+/dev/null)\s*$`)

var secretNameRe = regexp.MustCompile(`(?i)(pass(word)?|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)`)
var secretValueRe = regexp.MustCompile(`^(sk-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{16,}|[A-Za-z0-9+/_-]{32,}={0,2})$`)

// Check runs every rule over objs.
func Check(objs []k8s.Object, c Context) []Finding {
	var fs []Finding
	add := func(f Finding) { fs = append(fs, f) }
	totalGPU := 0
	totalPods := 0
	for i := range objs {
		o := &objs[i]
		ref := o.Kind + "/" + o.Metadata.Name
		if o.Kind == "Pod" {
			add(Finding{ID: "R9", Severity: Refuse, Object: ref, Source: srcPolicies,
				Message: "A bare Pod is treated as interactive: it is deleted after 6 hours and does not come back if its node fails.",
				Fix:     "Run batch work as a Job (goal job or sweep), or use nrp_session for interactive work."})
		}
		if o.PVCSpec != nil && o.PVCSpec.StorageClassName == "ceph-rbd" {
			add(Finding{ID: "R10", Severity: Refuse, Object: ref, Source: srcTested,
				Message: "Storage class ceph-rbd never provisions a volume on Nautilus; the claim stays Pending.",
				Fix:     "Use rook-cephfs-central (shared, ReadWriteMany) or rook-ceph-block-central (single pod)."})
		}
		if o.IngressSpec != nil {
			fs = append(fs, checkIngress(o, ref)...)
		}
		ps := o.PodSpecOf()
		if ps == nil {
			continue
		}
		pods := 1
		if o.JobSpec != nil && o.JobSpec.Completions != nil {
			pods = *o.JobSpec.Completions
		}
		if o.DeploymentSpec != nil {
			pods = o.DeploymentSpec.Replicas
		}
		totalPods += pods
		if !allowedPriority[ps.PriorityClassName] {
			add(Finding{ID: "R5", Severity: Refuse, Object: ref, Source: srcPriority,
				Message: fmt.Sprintf("priorityClassName %q is banned in user namespaces; the pod would be rejected.", ps.PriorityClassName),
				Fix:     "Remove priorityClassName (the default is right in most cases), or use opportunistic for special GPUs without quota."})
		}
		podGPU := 0
		for _, ctr := range append(append([]k8s.Container{}, ps.InitContainers...), ps.Containers...) {
			cref := ref + " container " + ctr.Name
			fs = append(fs, checkResources(ctr, cref, c, pods)...)
			for _, g := range GPUResources {
				podGPU += k8s.ParseCount(ctr.Resources.Requests[g])
				if n := k8s.ParseCount(ctr.Resources.Limits[g]); n > k8s.ParseCount(ctr.Resources.Requests[g]) {
					podGPU += n - k8s.ParseCount(ctr.Resources.Requests[g])
				}
			}
			for _, sg := range SpecialGPUs {
				want := k8s.ParseCount(ctr.Resources.Requests[sg]) + k8s.ParseCount(ctr.Resources.Limits[sg])
				if want == 0 {
					continue
				}
				q, limited := c.GPUQuota[sg]
				if limited && q == 0 && !strings.HasPrefix(ps.PriorityClassName, "opportunistic") {
					add(Finding{ID: "R6", Severity: Refuse, Object: cref, Source: srcGPU,
						Message: fmt.Sprintf("%s is quota-gated and your namespace's quota is 0.", sg),
						Fix:     "Use nvidia.com/gpu (any standard GPU), or set priorityClassName: opportunistic (can be preempted at any time; checkpoint), or ask NRP for A100 access (H100/H200/GH200 are not requestable)."})
				}
			}
			if o.JobSpec != nil {
				if isSleep(ctr) {
					add(Finding{ID: "R1", Severity: Refuse, Object: cref, Source: srcPolicies,
						Message: "A Job whose command is (or ends with) sleep holds resources doing nothing; the NRP bans users for this.",
						Fix:     "Make the command your actual computation so the Job ends when the work does. For an idle shell use nrp_session."})
				}
			}
			for _, e := range ctr.Env {
				if e.Value == "" || e.ValueFrom != nil {
					continue
				}
				if secretNameRe.MatchString(e.Name) || secretValueRe.MatchString(e.Value) {
					add(Finding{ID: "R11", Severity: Refuse, Object: cref, Source: srcScheduler,
						Message: fmt.Sprintf("Env var %s looks like a secret written into the spec; job specs can be visible to other cluster users.", e.Name),
						Fix:     "Put the value in a Kubernetes Secret and reference it with valueFrom.secretKeyRef (nrp_run does this for keys you name)."})
				}
			}
		}
		maxGPU := 8
		if o.Kind == "Pod" {
			maxGPU = 2
		}
		if podGPU > maxGPU {
			add(Finding{ID: "R7", Severity: Refuse, Object: ref, Source: srcGPU,
				Message: fmt.Sprintf("%d GPUs in one pod; the NRP allows up to %d here.", podGPU, maxGPU),
				Fix:     "Split the work across pods (an Indexed Job), or request fewer GPUs."})
		}
		if o.DeploymentSpec != nil && podGPU > 0 && !c.Session {
			add(Finding{ID: "R8", Severity: Refuse, Object: ref, Source: srcLongIdle,
				Message: "Long-running Deployments (web tools, idle services) cannot request GPUs on Nautilus.",
				Fix:     "Remove the GPU, or run the GPU part as a Job."})
		}
		if o.DeploymentSpec != nil {
			add(Finding{ID: "R17", Severity: Warn, Object: ref, Source: srcLongIdle,
				Message: "Deployments are removed after 2 weeks unless the namespace is on the NRP exceptions list.",
				Fix:     "For a long-lived service, ask in Nautilus Support for an exception; you can pause it any time with replicas 0."})
		}
		totalGPU += podGPU * pods
		if o.JobSpec != nil && o.JobSpec.ActiveDeadlineSeconds != nil && c.HoursPerRun > 0 && *o.JobSpec.ActiveDeadlineSeconds > c.HoursPerRun*3600 {
			add(Finding{ID: "R14", Severity: Refuse, Object: ref, Source: srcConfig,
				Message: fmt.Sprintf("Runtime limit %d h is over your cap of %d h per run.", *o.JobSpec.ActiveDeadlineSeconds/3600, c.HoursPerRun),
				Fix:     "Lower hours, or raise caps.hours_per_run in your nrp-mcp config if you mean it."})
		}
		if o.JobSpec != nil && (o.JobSpec.TTLSecondsAfterFinished == nil || o.JobSpec.BackoffLimit == nil) {
			add(Finding{ID: "R15", Severity: Warn, Object: ref, Source: srcPolicies,
				Message: "Jobs without ttlSecondsAfterFinished and backoffLimit linger or retry forever.",
				Fix:     "nrp_plan sets both (24 h TTL, backoffLimit 2)."})
		}
	}
	if c.PodsPerRun > 0 && totalPods > c.PodsPerRun {
		add(Finding{ID: "R14", Severity: Refuse, Source: srcConfig,
			Message: fmt.Sprintf("%d pods in this run is over your cap of %d.", totalPods, c.PodsPerRun),
			Fix:     "Lower count, or raise caps.pods_per_run in your nrp-mcp config."})
	}
	if c.GPUsPerRun >= 0 && totalGPU > c.GPUsPerRun && c.GPUsPerRun > 0 {
		add(Finding{ID: "R14", Severity: Refuse, Source: srcConfig,
			Message: fmt.Sprintf("%d GPUs in this run is over your cap of %d.", totalGPU, c.GPUsPerRun),
			Fix:     "Use fewer GPUs per task or fewer parallel tasks, or raise caps.gpus_per_run."})
	}
	return fs
}

func isSleep(c k8s.Container) bool {
	line := strings.TrimSpace(strings.Join(append(append([]string{}, c.Command...), c.Args...), " "))
	if line == "" {
		return false
	}
	return sleepRe.MatchString(line)
}

func checkResources(ctr k8s.Container, ref string, c Context, pods int) []Finding {
	var fs []Finding
	r := ctr.Resources
	for _, k := range []string{"cpu", "memory"} {
		if r.Requests[k] == "" || r.Limits[k] == "" {
			fs = append(fs, Finding{ID: "R2", Severity: Refuse, Object: ref, Source: srcPolicies,
				Message: fmt.Sprintf("%s request and limit must both be set (missing on %s).", k, ctr.Name),
				Fix:     "Set requests close to typical use and limits a little above the peak; nrp_plan does this."})
		}
	}
	if len(fs) > 0 {
		return fs
	}
	big := pods > 100 || c.SweepCount > 100
	for _, k := range []string{"cpu", "memory", "ephemeral-storage"} {
		rq, lm := r.Requests[k], r.Limits[k]
		if rq == "" || lm == "" {
			continue
		}
		var a, b int64
		var e1, e2 error
		if k == "cpu" {
			a, e1 = k8s.ParseCPU(rq)
			b, e2 = k8s.ParseCPU(lm)
		} else {
			a, e1 = k8s.ParseBytes(rq)
			b, e2 = k8s.ParseBytes(lm)
		}
		if e1 != nil || e2 != nil || a <= 0 {
			continue
		}
		if b < a {
			fs = append(fs, Finding{ID: "R3", Severity: Refuse, Object: ref, Source: srcPolicies,
				Message: fmt.Sprintf("%s limit (%s) is below the request (%s).", k, lm, rq),
				Fix:     "Make the limit at least the request."})
			continue
		}
		if big && b != a {
			fs = append(fs, Finding{ID: "R4", Severity: Refuse, Object: ref, Source: srcPolicies,
				Message: fmt.Sprintf("With more than 100 pods the NRP asks for limit = request (%s: request %s, limit %s).", k, rq, lm),
				Fix:     "Set the limit equal to the request."})
			continue
		}
		if float64(b) > float64(a)*1.2+0.5 {
			fs = append(fs, Finding{ID: "R3", Severity: Refuse, Object: ref, Source: srcPolicies,
				Message: fmt.Sprintf("%s limit %s is more than 20%% above the request %s.", k, lm, rq),
				Fix:     "Raise the request or lower the limit so the limit is within 20% of the request."})
		}
	}
	cpu, _ := k8s.ParseCPU(r.Requests["cpu"])
	mem, _ := k8s.ParseBytes(r.Requests["memory"])
	if cpu > 1000 || mem > 2<<30 {
		fs = append(fs, Finding{ID: "R16", Severity: Warn, Object: ref, Source: srcPolicies,
			Message: "Above 1 CPU / 2 GiB the NRP checks real use: GPUs under 40%, CPU outside 20-200% or memory outside 20-150% of the request count as violations, and more than 4 violating pods gets the namespace flagged.",
			Fix:     "Size requests to what the program really uses; nrp_watch shows usage once it runs."})
	}
	return fs
}

func checkIngress(o *k8s.Object, ref string) []Finding {
	var fs []Finding
	s := o.IngressSpec
	if s.IngressClassName != "haproxy" {
		fs = append(fs, Finding{ID: "R12", Severity: Refuse, Object: ref, Source: srcIngress,
			Message: "Nautilus Ingresses use ingressClassName haproxy.", Fix: "Set ingressClassName: haproxy."})
	}
	tls := map[string]bool{}
	for _, t := range s.TLS {
		for _, h := range t.Hosts {
			tls[h] = true
		}
	}
	for _, r := range s.Rules {
		if r.Host == "" {
			fs = append(fs, Finding{ID: "R12", Severity: Refuse, Object: ref, Source: srcIngress,
				Message: "Every Ingress rule needs a host.", Fix: "Use <name>.nrp-nautilus.io or your own domain."})
			continue
		}
		if !tls[r.Host] {
			fs = append(fs, Finding{ID: "R12", Severity: Refuse, Object: ref, Source: srcIngress,
				Message: fmt.Sprintf("Host %s has no TLS entry; nrp-mcp only publishes over HTTPS.", r.Host),
				Fix:     "List the host under tls.hosts (nrp-nautilus.io hosts get a certificate automatically)."})
		}
	}
	return fs
}

// Blocking reports whether any finding refuses the plan.
func Blocking(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == Refuse {
			return true
		}
	}
	return false
}
