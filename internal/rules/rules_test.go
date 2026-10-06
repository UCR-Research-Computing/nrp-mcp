package rules

import (
	"fmt"
	"strings"
	"testing"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
)

func ctr(cmd ...string) k8s.Container {
	return k8s.Container{Name: "main", Image: "python:3.12-slim", Command: cmd,
		Resources: k8s.Resources{
			Requests: map[string]string{"cpu": "1", "memory": "2Gi"},
			Limits:   map[string]string{"cpu": "1", "memory": "2Gi"},
		}}
}

func job(c k8s.Container) k8s.Object {
	return k8s.Object{APIVersion: "batch/v1", Kind: "Job", Metadata: k8s.Meta{Name: "j"},
		JobSpec: &k8s.JobSpec{BackoffLimit: k8s.IntPtr(2), TTLSecondsAfterFinished: k8s.IntPtr(86400),
			Template: k8s.PodTemplate{Spec: k8s.PodSpec{RestartPolicy: "Never", Containers: []k8s.Container{c}}}}}
}

func has(fs []Finding, id string, sev Severity) bool {
	for _, f := range fs {
		if f.ID == id && f.Severity == sev {
			return true
		}
	}
	return false
}

func base() Context {
	return Context{PodsPerRun: 50, GPUsPerRun: 4, HoursPerRun: 48, TasksPerRun: 10000}
}

func TestCleanJobPasses(t *testing.T) {
	fs := Check([]k8s.Object{job(ctr("python", "train.py"))}, base())
	if Blocking(fs) {
		t.Fatalf("clean job refused: %+v", fs)
	}
}

func TestR1Sleep(t *testing.T) {
	for _, cmd := range [][]string{{"sleep", "infinity"}, {"bash", "-c", "python a.py; sleep 100000"}, {"tail", "-f", "/dev/null"}} {
		if !has(Check([]k8s.Object{job(ctr(cmd...))}, base()), "R1", Refuse) {
			t.Errorf("sleep not refused: %v", cmd)
		}
	}
	if has(Check([]k8s.Object{job(ctr("bash", "-c", "sleep 5 && python a.py"))}, base()), "R1", Refuse) {
		t.Error("short sleep before work should pass")
	}
}

func TestR2MissingResources(t *testing.T) {
	c := ctr("python", "a.py")
	delete(c.Resources.Limits, "memory")
	if !has(Check([]k8s.Object{job(c)}, base()), "R2", Refuse) {
		t.Error("missing memory limit not refused")
	}
}

func TestR3LimitWithin20(t *testing.T) {
	c := ctr("python", "a.py")
	c.Resources.Limits["memory"] = "4Gi"
	if !has(Check([]k8s.Object{job(c)}, base()), "R3", Refuse) {
		t.Error("limit 2x request not refused")
	}
	c.Resources.Limits["memory"] = "2400Mi" // 17% above
	if has(Check([]k8s.Object{job(c)}, base()), "R3", Refuse) {
		t.Error("limit within 20% refused")
	}
	c.Resources.Limits["cpu"] = "500m"
	if !has(Check([]k8s.Object{job(c)}, base()), "R3", Refuse) {
		t.Error("limit below request not refused")
	}
}

func TestR4BigSweep(t *testing.T) {
	c := ctr("python", "a.py")
	c.Resources.Limits["memory"] = "2200Mi"
	j := job(c)
	j.JobSpec.Completions = k8s.IntPtr(200)
	cx := base()
	cx.PodsPerRun = 500
	if !has(Check([]k8s.Object{j}, cx), "R4", Refuse) {
		t.Error(">100 pods with limit != request not refused")
	}
}

func TestR5Priority(t *testing.T) {
	j := job(ctr("python", "a.py"))
	j.JobSpec.Template.Spec.PriorityClassName = "system-cluster-critical"
	if !has(Check([]k8s.Object{j}, base()), "R5", Refuse) {
		t.Error("banned priority not refused")
	}
	j.JobSpec.Template.Spec.PriorityClassName = "opportunistic"
	if has(Check([]k8s.Object{j}, base()), "R5", Refuse) {
		t.Error("opportunistic refused")
	}
}

func TestR6SpecialGPUQuota(t *testing.T) {
	c := ctr("python", "a.py")
	c.Resources.Requests["nvidia.com/a100"] = "1"
	c.Resources.Limits["nvidia.com/a100"] = "1"
	cx := base()
	cx.GPUQuota = map[string]int{"nvidia.com/a100": 0}
	j := job(c)
	if !has(Check([]k8s.Object{j}, cx), "R6", Refuse) {
		t.Error("a100 with quota 0 not refused")
	}
	j.JobSpec.Template.Spec.PriorityClassName = "opportunistic"
	if has(Check([]k8s.Object{j}, cx), "R6", Refuse) {
		t.Error("opportunistic a100 refused")
	}
}

func TestR7TooManyGPUs(t *testing.T) {
	c := ctr("python", "a.py")
	c.Resources.Requests["nvidia.com/gpu"] = "9"
	c.Resources.Limits["nvidia.com/gpu"] = "9"
	cx := base()
	cx.GPUsPerRun = 16
	if !has(Check([]k8s.Object{job(c)}, cx), "R7", Refuse) {
		t.Error("9 GPUs not refused")
	}
}

func TestR8DeploymentGPU(t *testing.T) {
	c := ctr("python", "app.py")
	c.Resources.Requests["nvidia.com/gpu"] = "1"
	c.Resources.Limits["nvidia.com/gpu"] = "1"
	d := k8s.Object{APIVersion: "apps/v1", Kind: "Deployment", Metadata: k8s.Meta{Name: "d"},
		DeploymentSpec: &k8s.DeploymentSpec{Replicas: 1, Template: k8s.PodTemplate{Spec: k8s.PodSpec{Containers: []k8s.Container{c}}}}}
	if !has(Check([]k8s.Object{d}, base()), "R8", Refuse) {
		t.Error("GPU deployment not refused")
	}
	cx := base()
	cx.Session = true
	if has(Check([]k8s.Object{d}, cx), "R8", Refuse) {
		t.Error("session GPU refused")
	}
	if !has(Check([]k8s.Object{d}, cx), "R17", Warn) {
		t.Error("2-week warning missing")
	}
}

func TestR9BarePod(t *testing.T) {
	p := k8s.Object{APIVersion: "v1", Kind: "Pod", Metadata: k8s.Meta{Name: "p"}, PodSpec: &k8s.PodSpec{Containers: []k8s.Container{ctr("python", "a.py")}}}
	if !has(Check([]k8s.Object{p}, base()), "R9", Refuse) {
		t.Error("bare pod not refused")
	}
}

func TestR10CephRBD(t *testing.T) {
	pvc := k8s.Object{APIVersion: "v1", Kind: "PersistentVolumeClaim", Metadata: k8s.Meta{Name: "v"},
		PVCSpec: &k8s.PVCSpec{AccessModes: []string{"ReadWriteOnce"}, StorageClassName: "ceph-rbd"}}
	if !has(Check([]k8s.Object{pvc}, base()), "R10", Refuse) {
		t.Error("ceph-rbd not refused")
	}
}

func TestR11SecretInEnv(t *testing.T) {
	c := ctr("python", "a.py")
	c.Env = []k8s.EnvVar{{Name: "OPENAI_API_KEY", Value: "sk-abcdefghijklmnopqrstuvwxyz123456"}}
	if !has(Check([]k8s.Object{job(c)}, base()), "R11", Refuse) {
		t.Error("literal api key not refused")
	}
	c.Env = []k8s.EnvVar{{Name: "OPENAI_API_KEY", ValueFrom: &k8s.EnvVarSource{SecretKeyRef: &k8s.KeyRef{Name: "s", Key: "k"}}}, {Name: "MODE", Value: "fast"}}
	if has(Check([]k8s.Object{job(c)}, base()), "R11", Refuse) {
		t.Error("secretKeyRef refused")
	}
}

func TestR12Ingress(t *testing.T) {
	ing := k8s.Object{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Metadata: k8s.Meta{Name: "i"},
		IngressSpec: &k8s.IngressSpec{IngressClassName: "nginx", Rules: []k8s.IngressRule{{Host: "a.nrp-nautilus.io"}}}}
	fs := Check([]k8s.Object{ing}, base())
	if !has(fs, "R12", Refuse) {
		t.Error("bad ingress not refused")
	}
	ing.IngressSpec.IngressClassName = "haproxy"
	ing.IngressSpec.TLS = []k8s.IngressTLS{{Hosts: []string{"a.nrp-nautilus.io"}}}
	if has(Check([]k8s.Object{ing}, base()), "R12", Refuse) {
		t.Error("good ingress refused")
	}
}

func TestR14Caps(t *testing.T) {
	c := ctr("python", "a.py")
	c.Resources.Requests["nvidia.com/gpu"] = "1"
	c.Resources.Limits["nvidia.com/gpu"] = "1"
	j := job(c)
	j.JobSpec.Completions = k8s.IntPtr(10)
	j.JobSpec.Parallelism = k8s.IntPtr(10)
	if !has(Check([]k8s.Object{j}, base()), "R14", Refuse) {
		t.Error("10 GPUs over cap 4 not refused")
	}
	j.JobSpec.ActiveDeadlineSeconds = k8s.IntPtr(100 * 3600)
	c2 := ctr("python", "a.py")
	j2 := job(c2)
	j2.JobSpec.ActiveDeadlineSeconds = k8s.IntPtr(100 * 3600)
	if !has(Check([]k8s.Object{j2}, base()), "R14", Refuse) {
		t.Error("100 h over cap 48 not refused")
	}
}

func TestR16UsageWarning(t *testing.T) {
	c := ctr("python", "a.py")
	c.Resources.Requests["cpu"], c.Resources.Limits["cpu"] = "4", "4"
	if !has(Check([]k8s.Object{job(c)}, base()), "R16", Warn) {
		t.Error("usage warning missing above 1 cpu")
	}
}

// The pod and GPU caps limit what runs at the same time, not a sweep's total: 1,000 tasks
// at parallel 50 is fine under pods_per_run 50; parallel 51 is not. tasks_per_run caps
// the total.
func TestR14CapsCountConcurrency(t *testing.T) {
	sweep := func(completions, parallel, gpu int) k8s.Object {
		c := ctr("python", "a.py")
		if gpu > 0 {
			g := fmt.Sprint(gpu)
			c.Resources.Requests["nvidia.com/gpu"], c.Resources.Limits["nvidia.com/gpu"] = g, g
		}
		j := job(c)
		j.JobSpec.Completions, j.JobSpec.Parallelism = k8s.IntPtr(completions), k8s.IntPtr(parallel)
		j.JobSpec.CompletionMode = "Indexed"
		return j
	}
	cases := []struct {
		name          string
		obj           k8s.Object
		refused       bool
		wantInMessage string
	}{
		{"1000 tasks at 50", sweep(1000, 50, 0), false, ""},
		{"1000 tasks at 51", sweep(1000, 51, 0), true, "51 pods at the same time"},
		{"parallel above completions counts completions", sweep(3, 100, 0), false, ""},
		{"200 GPU tasks, 4 at a time, 1 GPU each", sweep(200, 4, 1), false, ""},
		{"200 GPU tasks, 5 at a time", sweep(200, 5, 1), true, "5 GPUs at the same time"},
		{"2 at a time with 2 GPUs each = 4", sweep(50, 2, 2), false, ""},
		{"20,000 tasks over tasks_per_run", sweep(20000, 10, 0), true, "20000 tasks in this run"},
	}
	for _, tc := range cases {
		fs := Check([]k8s.Object{tc.obj}, base())
		got := has(fs, "R14", Refuse)
		if got != tc.refused {
			t.Errorf("%s: refused=%v want %v (%v)", tc.name, got, tc.refused, fs)
			continue
		}
		if tc.wantInMessage != "" {
			found := false
			for _, f := range fs {
				if f.ID == "R14" && strings.Contains(f.Message, tc.wantInMessage) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no R14 message containing %q in %v", tc.name, tc.wantInMessage, fs)
			}
		}
	}
	// A Job without parallelism runs one pod at a time (the Kubernetes default).
	j := sweep(500, 1, 0)
	j.JobSpec.Parallelism = nil
	if has(Check([]k8s.Object{j}, base()), "R14", Refuse) {
		t.Error("500 tasks one at a time refused")
	}
}
