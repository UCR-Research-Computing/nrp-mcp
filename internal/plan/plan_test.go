package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/inspect"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
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
	// --array=1-50%10: Slurm ids 1..50, ten at a time, and the script keeps reading
	// $SLURM_ARRAY_TASK_ID (now set per task).
	if !strings.Contains(c.Command[2], "SLURM_ARRAY_TASK_ID=$((1 + JOB_COMPLETION_INDEX))") || !strings.HasSuffix(c.Command[2], "python fold.py $SLURM_ARRAY_TASK_ID") {
		t.Fatalf("command %v", c.Command)
	}
	if *js.Parallelism != 10 {
		t.Fatalf("parallelism %d, want 10 from %%10", *js.Parallelism)
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

func TestArrayIDs(t *testing.T) {
	cases := []struct {
		in       string
		want     []int
		throttle int
	}{
		{"0-9", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 0},
		{"1-5%2", []int{1, 2, 3, 4, 5}, 2},
		{"0-20:5", []int{0, 5, 10, 15, 20}, 0},
		{"1,3,7", []int{1, 3, 7}, 0},
		{"2,4-6", []int{2, 4, 5, 6}, 0},
		{"5", []int{5}, 0},
		{"9-1", nil, 0},
	}
	for _, c := range cases {
		got, th := arrayIDs(c.in)
		if fmt.Sprint(got) != fmt.Sprint(c.want) || th != c.throttle {
			t.Errorf("%s: got %v %%%d, want %v %%%d", c.in, got, th, c.want, c.throttle)
		}
	}
	if ids, _ := arrayIDs("0-999999"); len(ids) != maxArrayTasks {
		t.Errorf("cap: %d", len(ids))
	}
}

// Run the exact shell prefix nrp puts in the pod, once per task index, and check that
// every task sees a different SLURM_ARRAY_TASK_ID matching the Slurm ids.
func TestArrayEnvGivesEachTaskItsOwnID(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	for _, spec := range []string{"0-9", "1-5", "0-20:5", "1,3,7", "2,4-6"} {
		ids, _ := arrayIDs(spec)
		pre := arrayEnv(ids, len(ids))
		for i, want := range ids {
			cmd := exec.Command(bash, "-c", "set -e; "+pre+"echo $SLURM_ARRAY_TASK_ID $SLURM_ARRAY_TASK_COUNT")
			cmd.Env = []string{"JOB_COMPLETION_INDEX=" + strconv.Itoa(i), "PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s task %d: %v %s", spec, i, err, out)
			}
			if got := strings.TrimSpace(string(out)); got != fmt.Sprintf("%d %d", want, len(ids)) {
				t.Errorf("%s task %d: got %q, want id %d", spec, i, got, want)
			}
		}
	}
}

// A sweep made from count alone (no Slurm script) still gets SLURM_ARRAY_TASK_ID = index.
func TestPlainSweepSetsArrayID(t *testing.T) {
	f := proj(t, map[string]string{"task.py": "import os\nprint(os.environ['SLURM_ARRAY_TASK_ID'])\n"})
	p, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalSweep, Count: 4})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Objects[len(p.Objects)-1].JobSpec.Template.Spec.Containers[0]
	if !strings.Contains(c.Command[2], "export SLURM_ARRAY_TASK_ID=$JOB_COMPLETION_INDEX SLURM_ARRAY_TASK_COUNT=4") {
		t.Fatalf("command %v", c.Command)
	}
}

func TestPrebuiltWebImage(t *testing.T) {
	f := proj(t, map[string]string{
		"app.py":     "from flask import Flask\napp = Flask(__name__)\n",
		"Dockerfile": "FROM python:3.12-slim\nEXPOSE 8000\nCMD [\"gunicorn\",\"-b\",\"0.0.0.0:8000\",\"app:app\"]\n",
	})
	p, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb, Image: "ghcr.io/some-lab/spice-web:abc123", PullSecret: "ghcr-pull"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Prebuilt || p.NeedsBuild {
		t.Fatalf("prebuilt=%v needs_build=%v", p.Prebuilt, p.NeedsBuild)
	}
	ps := p.Objects[0].PodSpecOf()
	c := ps.Containers[0]
	if len(c.Command) != 0 || len(c.VolumeMounts) != 0 || len(ps.Volumes) != 0 {
		t.Fatalf("prebuilt image must run its own CMD with no code volume: %+v %+v", c, ps.Volumes)
	}
	if c.Ports[0].ContainerPort != 8000 || c.ReadinessProbe.HTTPGet.Port != 8000 {
		t.Fatalf("port from EXPOSE: %+v", c.Ports)
	}
	if len(ps.ImagePullSecrets) != 1 || ps.ImagePullSecrets[0].Name != "ghcr-pull" {
		t.Fatalf("pull secret: %+v", ps.ImagePullSecrets)
	}
	if rules.Blocking(rules.Check(p.Objects, ctx())) {
		t.Fatalf("refused: %+v", rules.Check(p.Objects, ctx()))
	}
}

func TestDockerfileWithoutImageNeedsBuildOnGHCR(t *testing.T) {
	f := proj(t, map[string]string{"app.py": "from flask import Flask\napp = Flask(__name__)\n", "Dockerfile": "FROM python:3.12-slim\n"})
	p, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb})
	if err != nil {
		t.Fatal(err)
	}
	if !p.NeedsBuild || !strings.HasPrefix(p.Image, "ghcr.io/") {
		t.Fatalf("needs_build=%v image=%s", p.NeedsBuild, p.Image)
	}
}

func TestPrebuiltJobKeepsData(t *testing.T) {
	p, err := Build(nil, Request{Namespace: "ucr-example", Goal: GoalJob, Image: "ghcr.io/lab/tool:1", Command: "tool --run", DataPVC: "lab-data"})
	if err != nil {
		t.Fatal(err)
	}
	ps := p.Objects[0].PodSpecOf()
	c := ps.Containers[0]
	if c.Command[2] != "tool --run" || len(c.VolumeMounts) != 1 || c.VolumeMounts[0].MountPath != "/data" || len(ps.Volumes) != 1 {
		t.Fatalf("job: %+v %+v", c, ps.Volumes)
	}
}

// avoidFacts is a small web project used by the avoid_node tests.
func avoidFacts(t *testing.T) *inspect.Facts {
	return proj(t, map[string]string{"app.py": "import streamlit as st\nst.title('Genome Map Viewer')\n", "requirements.txt": "streamlit\n"})
}

// manifestDigest is the sha256 of the plan objects with the random plan id masked,
// so two builds of the same request can be compared byte for byte.
func manifestDigest(t *testing.T, p *Plan) string {
	t.Helper()
	b, err := json.Marshal(p.Objects)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), p.ID, "PLANID")
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func hostnameNotIn(t *testing.T, ps *k8s.PodSpec) []string {
	t.Helper()
	if ps.Affinity == nil || ps.Affinity.NodeAffinity == nil || ps.Affinity.NodeAffinity.Required == nil {
		t.Fatalf("no required node affinity: %+v", ps.Affinity)
	}
	terms := ps.Affinity.NodeAffinity.Required.NodeSelectorTerms
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 {
		t.Fatalf("terms %+v", terms)
	}
	e := terms[0].MatchExpressions[0]
	if e.Key != "kubernetes.io/hostname" || e.Operator != "NotIn" {
		t.Fatalf("expression %+v", e)
	}
	return e.Values
}

func TestAvoidNodesWeb(t *testing.T) {
	p, err := Build(avoidFacts(t), Request{Namespace: "ucr-example", Goal: GoalWeb, AvoidNodes: []string{" K8S-haosu-20.sdsc.optiputer.net ", "node-b", "node-b"}})
	if err != nil {
		t.Fatal(err)
	}
	got := hostnameNotIn(t, p.Objects[0].PodSpecOf())
	if strings.Join(got, ",") != "k8s-haosu-20.sdsc.optiputer.net,node-b" {
		t.Fatalf("values %v", got)
	}
	found := false
	for _, d := range p.Decisions {
		if d == "Keeps the pod off k8s-haosu-20.sdsc.optiputer.net, node-b (you asked to avoid them)." {
			found = true
		}
	}
	if !found {
		t.Fatalf("no avoid decision: %v", p.Decisions)
	}
	if rules.Blocking(rules.Check(p.Objects, ctx())) {
		t.Fatalf("refused: %+v", rules.Check(p.Objects, ctx()))
	}
}

func TestAvoidNodesJobAndSession(t *testing.T) {
	f := proj(t, map[string]string{"run.py": "print(1)\n"})
	p, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalSweep, Count: 4, AvoidNodes: []string{"node-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hostnameNotIn(t, p.Objects[0].PodSpecOf()); len(got) != 1 || got[0] != "node-a" {
		t.Fatalf("job values %v", got)
	}
	p, err = Build(nil, Request{Namespace: "ucr-example", Goal: GoalSession, AvoidNodes: []string{"node-a"}})
	if err != nil {
		t.Fatal(err)
	}
	var dep *k8s.Object
	for i := range p.Objects {
		if p.Objects[i].Kind == "Deployment" {
			dep = &p.Objects[i]
		}
	}
	if got := hostnameNotIn(t, dep.PodSpecOf()); len(got) != 1 || got[0] != "node-a" {
		t.Fatalf("session values %v", got)
	}
}

// goldenWebDigest is the masked manifest digest of the web plan below (fixed clock).
// Plans without avoid_node must not change. 0.9.0 re-pinned it on purpose: web pods now
// carry automountServiceAccountToken false and the nrp-mcp/expires annotation.
const goldenWebDigest = "ebacc7ac13537842ccdb15b6cbe3aeb733c0da6eeba079c720a880cec11ee9ed"

func TestNoAvoidNodesManifestsUnchanged(t *testing.T) {
	f := avoidFacts(t)
	r := Request{Namespace: "ucr-example", Goal: GoalWeb, Run: "0101-0000-test", Now: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	p, err := Build(f, r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p.Objects)
	if strings.Contains(string(b), "affinity") {
		t.Fatalf("affinity without avoid_node: %s", b)
	}
	if d := manifestDigest(t, p); d != goldenWebDigest {
		t.Fatalf("manifests changed without avoid_node: digest %s", d)
	}
	r.AvoidNodes = []string{"node-a"}
	q, err := Build(f, r)
	if err != nil {
		t.Fatal(err)
	}
	if manifestDigest(t, q) == manifestDigest(t, p) || q.Hash == p.Hash {
		t.Fatal("avoid_node did not change the manifests or the plan hash")
	}
}

func TestAvoidNodesRefusesBadInput(t *testing.T) {
	f := avoidFacts(t)
	for _, bad := range [][]string{
		{"node_a"},
		{"node a"},
		{"node/../x"},
		{"   "},
		{strings.Repeat("a", 254)},
		{"n1", "n2", "n3", "n4", "n5", "n6"},
	} {
		_, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb, AvoidNodes: bad})
		if err == nil || !strings.Contains(err.Error(), "avoid_node") {
			t.Errorf("%q: want avoid_node error, got %v", bad, err)
		}
	}
	if _, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb, AvoidNodes: []string{"n1", "n2", "n3", "n4", "n5"}}); err != nil {
		t.Fatalf("5 nodes refused: %v", err)
	}
}

func TestSessionTokenNotInPlan(t *testing.T) {
	p, err := Build(nil, Request{Namespace: "ucr-example", Goal: GoalSession})
	if err != nil {
		t.Fatal(err)
	}
	if p.Objects[0].Kind != "Secret" || p.Objects[0].StringData["token"] != SessionTokenPlaceholder {
		t.Fatalf("session Secret must hold only the placeholder: %+v", p.Objects[0].StringData)
	}
	f1, f2 := FillSessionToken(p.Objects), FillSessionToken(p.Objects)
	t1, t2 := f1[0].StringData["token"], f2[0].StringData["token"]
	if len(t1) != 32 || t1 == t2 || t1 == SessionTokenPlaceholder {
		t.Fatalf("tokens %q %q", t1, t2)
	}
	if p.Objects[0].StringData["token"] != SessionTokenPlaceholder {
		t.Fatal("FillSessionToken changed the plan itself")
	}
}

func TestIDsAreLongerAndRandom(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := newID("r")
		if len(id) != len("r1018-2037-")+6 || seen[id] {
			t.Fatalf("id %q", id)
		}
		seen[id] = true
	}
}

func TestWebPlanHardening(t *testing.T) {
	f := avoidFacts(t)
	p, err := Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb, WebDays: 3, Now: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var dep *k8s.Object
	for i := range p.Objects {
		if p.Objects[i].Kind == "Deployment" {
			dep = &p.Objects[i]
		}
	}
	if dep.Metadata.Annotations[ExpiresAnnotation] != "2026-10-23" {
		t.Fatalf("expires %q", dep.Metadata.Annotations[ExpiresAnnotation])
	}
	if a := dep.PodSpecOf().AutomountServiceAccountToken; a == nil || *a {
		t.Fatal("web pod mounts the service account token")
	}
	if !strings.Contains(strings.Join(p.Decisions, " "), "no login") {
		t.Fatalf("decisions do not say there is no login: %v", p.Decisions)
	}
	p, _ = Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb, Image: "quay.io/jupyter/scipy-notebook:latest", Command: "start-notebook.py --IdentityProvider.token=''"})
	if fs := rules.Check(p.Objects, ctx()); !hasRule(fs, "R20") {
		t.Fatalf("public notebook not refused: %+v", fs)
	}
	p, _ = Build(f, Request{Namespace: "ucr-example", Goal: GoalWeb, Host: "login"})
	if fs := rules.Check(p.Objects, ctx()); !hasRule(fs, "R21") {
		t.Fatalf("reserved host not refused: %+v", fs)
	}
	for _, g := range []string{GoalJob, GoalSession} {
		q, err := Build(f, Request{Namespace: "ucr-example", Goal: g, Command: "python app.py"})
		if err != nil {
			t.Fatal(err)
		}
		for i := range q.Objects {
			if ps := q.Objects[i].PodSpecOf(); ps != nil && (ps.AutomountServiceAccountToken == nil || *ps.AutomountServiceAccountToken) {
				t.Fatalf("%s pod mounts the service account token", g)
			}
		}
	}
}

func hasRule(fs []rules.Finding, id string) bool {
	for _, f := range fs {
		if f.ID == id && f.Severity == rules.Refuse {
			return true
		}
	}
	return false
}
