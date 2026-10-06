package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/inspect"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/rules"
)

func proj(t *testing.T, files map[string]string) *inspect.Facts {
	t.Helper()
	d := filepath.Join(t.TempDir(), "genome-map")
	for n, b := range files {
		p := filepath.Join(d, n)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := inspect.Dir(d)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func ctx() rules.Context { return rules.Context{PodsPerRun: 50, GPUsPerRun: 4, HoursPerRun: 48} }

func TestGPUJobPlanPassesRules(t *testing.T) {
	f := proj(t, map[string]string{"train.py": "import torch\nif __name__ == '__main__': pass\n", "requirements.txt": "torch\n"})
	p, err := Build(f, Request{Namespace: "ucr-example"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Goal != GoalJob || p.Image != "pytorch/pytorch:latest" || len(p.Objects) != 1 {
		t.Fatalf("plan %+v", p)
	}
	c := p.Objects[0].JobSpec.Template.Spec.Containers[0]
	if c.Resources.Requests["nvidia.com/gpu"] != "1" || c.Resources.Limits["memory"] != "16Gi" {
		t.Fatalf("resources %+v", c.Resources)
	}
	if !strings.Contains(c.Command[2], "pip install") || !strings.Contains(c.Command[2], "python train.py") {
		t.Fatalf("command %v", c.Command)
	}
	if fs := rules.Check(p.Objects, ctx()); rules.Blocking(fs) {
		t.Fatalf("generated plan refused: %+v", fs)
	}
	if p.Hash == "" || p.Hash != Hash(p) {
		t.Fatal("hash not stable")
	}
}

func TestSweepPlan(t *testing.T) {
	f := proj(t, map[string]string{"run.py": "print(1)\n"})
	p, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalSweep, Count: 20, Parallel: 5})
	if err != nil {
		t.Fatal(err)
	}
	js := p.Objects[0].JobSpec
	if *js.Completions != 20 || *js.Parallelism != 5 || js.CompletionMode != "Indexed" {
		t.Fatalf("job %+v", js)
	}
	if rules.Blocking(rules.Check(p.Objects, ctx())) {
		t.Fatal("sweep refused")
	}
}

func TestWebPlanSuggestsNamesAndPassesRules(t *testing.T) {
	f := proj(t, map[string]string{"app.py": "import streamlit as st\nst.title('Genome Map Viewer')\n", "requirements.txt": "streamlit\n"})
	p, err := Build(f, Request{Namespace: "ucr-example"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Goal != GoalWeb || len(p.Objects) != 3 {
		t.Fatalf("plan %+v", p)
	}
	if p.PublicURL != "https://genome-map-viewer.nrp-nautilus.io/" {
		t.Fatalf("url %s", p.PublicURL)
	}
	if len(p.Suggested) < 2 || !strings.HasSuffix(p.Suggested[1], ".nrp-nautilus.io") {
		t.Fatalf("suggestions %v", p.Suggested)
	}
	for _, s := range p.Suggested {
		if strings.Contains(s, "lab") {
			t.Fatalf("suggestion invents a lab name: %s", s)
		}
	}
	if rules.Blocking(rules.Check(p.Objects, ctx())) {
		t.Fatalf("web refused: %+v", rules.Check(p.Objects, ctx()))
	}
	if p.Extra["public_ack_required"] != p.PublicURL {
		t.Fatal("public ack not required")
	}
}

func TestWebNeverGetsGPU(t *testing.T) {
	f := proj(t, map[string]string{"app.py": "import torch\nimport gradio as gr\n"})
	p, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb, GPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rules.Blocking(rules.Check(p.Objects, ctx())) {
		t.Fatal("web with ignored gpu refused")
	}
	for _, o := range p.Objects {
		if ps := o.PodSpecOf(); ps != nil && ps.Containers[0].Resources.Requests["nvidia.com/gpu"] != "" {
			t.Fatal("web deployment got a GPU")
		}
	}
}

func TestSlurmTranslation(t *testing.T) {
	f := proj(t, map[string]string{"job.sbatch": "#!/bin/bash\n#SBATCH --job-name=Fold_X\n#SBATCH --gres=gpu:2\n#SBATCH --cpus-per-task=8\n#SBATCH --mem=32G\n#SBATCH --time=1-02:30:00\n#SBATCH --array=1-50%10\n#SBATCH -p gpu\nmodule load cuda\npython fold.py $SLURM_ARRAY_TASK_ID\n"})
	p, err := Build(f, Request{Namespace: "ucr-example"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Goal != GoalSweep || p.Name != "fold-x" {
		t.Fatalf("goal %s name %s", p.Goal, p.Name)
	}
	js := p.Objects[0].JobSpec
	c := js.Template.Spec.Containers[0]
	if *js.Completions != 50 || c.Resources.Requests["nvidia.com/gpu"] != "2" || c.Resources.Requests["cpu"] != "8" || c.Resources.Requests["memory"] != "32Gi" {
		t.Fatalf("job %+v %+v", js, c.Resources)
	}
	if *js.ActiveDeadlineSeconds != 27*3600 {
		t.Fatalf("deadline %d", *js.ActiveDeadlineSeconds)
	}
	if !strings.Contains(c.Command[2], "fold.py $JOB_COMPLETION_INDEX") {
		t.Fatalf("command %v", c.Command)
	}
	if len(p.Slurm) < 7 {
		t.Fatalf("mapping %v", p.Slurm)
	}
	cx := ctx()
	cx.GPUsPerRun = 100
	if fs := rules.Check(p.Objects, cx); rules.Blocking(fs) {
		t.Fatalf("translated plan refused: %+v", fs)
	}
}

func TestSessionPlan(t *testing.T) {
	p, err := Build(nil, Request{Namespace: "ucr-example", Goal: GoalSession, GPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Objects) != 2 || p.Objects[0].Kind != "Secret" {
		t.Fatalf("objects %+v", p.Objects)
	}
	cx := ctx()
	cx.Session = true
	if rules.Blocking(rules.Check(p.Objects, cx)) {
		t.Fatalf("session refused: %+v", rules.Check(p.Objects, cx))
	}
	if _, err := Build(nil, Request{Namespace: "x", Goal: GoalSession, GPU: 3}); err == nil {
		t.Fatal("3-GPU session allowed")
	}
}

func TestNoNamespace(t *testing.T) {
	if _, err := Build(nil, Request{Command: "echo hi"}); err == nil {
		t.Fatal("missing namespace allowed")
	}
}

func TestDNSName(t *testing.T) {
	for in, want := range map[string]string{"Genome Map Viewer": "genome-map-viewer", "my_tool!!": "my-tool", "2fast": "app-2fast", "": "app"} {
		if got := DNSName(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
}
