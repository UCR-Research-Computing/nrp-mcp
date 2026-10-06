package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/config"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/kube"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/ops"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/store"
)

// fakeKube records kubectl calls and returns canned JSON.
type fakeKube struct {
	mu     sync.Mutex
	calls  [][]string
	stdins [][]byte
}

func (f *fakeKube) exec(_ context.Context, _ string, args []string, stdin []byte) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	f.stdins = append(f.stdins, stdin)
	f.mu.Unlock()
	j := strings.Join(args, " ")
	switch {
	case strings.Contains(j, "auth whoami"):
		return []byte(`{"status":{"userInfo":{"username":"https://cilogon.org/serverE/users/1","groups":["oidcgroup:ucr-test:admin","system:authenticated"]}}}`), nil, nil
	case strings.Contains(j, "get resourcequota"):
		return []byte(`{"items":[{"metadata":{"name":"gpu"},"spec":{"hard":{"requests.nvidia.com/a100":"0"}},"status":{"used":{}}}]}`), nil, nil
	case strings.Contains(j, "apply"):
		return []byte("job.batch/x created\n"), nil, nil
	case strings.Contains(j, "delete"):
		return []byte("job.batch \"x\" deleted\n"), nil, nil
	case strings.Contains(j, "get pods -l nrp-mcp/run=web-1"):
		return []byte(`{"items":[{"metadata":{"name":"web-abc","creationTimestamp":"2026-10-06T10:00:00Z"},"spec":{"nodeName":"n1"},"status":{"phase":"Running","containerStatuses":[{"name":"web","ready":true,"state":{"running":{}}}]}}]}`), nil, nil
	case strings.Contains(j, "get ingress -l nrp-mcp/run=web-1"):
		return []byte(`{"items":[{"metadata":{"name":"leafy"},"spec":{"rules":[{"host":"leafy.nrp-nautilus.io"}]}}]}`), nil, nil
	case strings.Contains(j, "logs"):
		return []byte("started\n"), nil, nil
	case strings.Contains(j, "get job") && strings.Contains(j, "-l"):
		return []byte(`{"items":[{"metadata":{"name":"train","labels":{"app.kubernetes.io/managed-by":"nrp-mcp","nrp-mcp/run":"r-1"},"creationTimestamp":"2026-10-06T10:00:00Z"}}]}`), nil, nil
	case strings.Contains(j, " get "):
		return []byte(`{"items":[]}`), nil, nil
	}
	return []byte("{}"), nil, nil
}

func (f *fakeKube) applied() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "apply") && !strings.Contains(j, "--dry-run") {
			n++
		}
	}
	return n
}

func (f *fakeKube) deleted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if len(c) > 0 && strings.Contains(strings.Join(c, " "), " delete ") {
			n++
		}
	}
	return n
}

func setup(t *testing.T) (*mcp.ClientSession, *fakeKube) {
	t.Helper()
	fk := &fakeKube{}
	k := kube.New(kube.Config{Context: "nautilus"})
	k.Exec = fk.exec
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: config.Config{Context: "nautilus", PodsPerRun: 50, GPUsPerRun: 4, HoursPerRun: 48}, Ops: &ops.Ops{K: k}, Store: st,
		HTTP: func(string) (int, error) { return 404, nil }, Now: time.Now}
	srv := New(s)
	ct, stt := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, stt, nil); err != nil {
		t.Fatal(err)
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, fk
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, bool, string) {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	var m map[string]any
	if r.StructuredContent != nil {
		b, _ := json.Marshal(r.StructuredContent)
		_ = json.Unmarshal(b, &m)
	}
	return m, r.IsError, text
}

func project(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for n, b := range files {
		p := filepath.Join(dir, n)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEightTools(t *testing.T) {
	cs, _ := setup(t)
	r, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tl := range r.Tools {
		got[tl.Name] = true
		if !strings.HasPrefix(tl.Name, "nrp_") {
			t.Errorf("tool %s lacks prefix", tl.Name)
		}
	}
	for _, n := range ToolNames() {
		if !got[n] {
			t.Errorf("missing %s", n)
		}
	}
	if len(r.Tools) != 9 {
		t.Errorf("want 9 tools, got %d", len(r.Tools))
	}
	res, _ := cs.ListResources(context.Background(), nil)
	if len(res.Resources) != 5 {
		t.Errorf("want 5 resources, got %d", len(res.Resources))
	}
	pr, _ := cs.ListPrompts(context.Background(), nil)
	if len(pr.Prompts) != 6 {
		t.Errorf("want 6 prompts, got %d", len(pr.Prompts))
	}
}

func TestStatus(t *testing.T) {
	cs, _ := setup(t)
	m, isErr, text := call(t, cs, "nrp_status", nil)
	if isErr {
		t.Fatal(text)
	}
	if m["namespace"] != "ucr-test" || !strings.Contains(m["summary"].(string), "ucr-test") {
		t.Fatalf("%v", m)
	}
}

func TestPlanRunJobFlow(t *testing.T) {
	cs, fk := setup(t)
	dir := project(t, map[string]string{"train.py": "import torch\nx=torch.zeros(1).cuda()\n", "requirements.txt": "torch\n"})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir})
	if isErr {
		t.Fatal(text)
	}
	if m["runnable"] != true {
		t.Fatalf("not runnable: %v", m["refusals"])
	}
	if fk.applied() != 0 {
		t.Fatal("nrp_plan must not create anything")
	}
	// Wrong token refused, nothing applied.
	_, isErr, text = call(t, cs, "nrp_run", map[string]any{"plan_id": m["plan_id"], "confirm_token": "nope"})
	if !isErr || fk.applied() != 0 {
		t.Fatalf("bad token accepted: %s", text)
	}
	out, isErr, text := call(t, cs, "nrp_run", map[string]any{"plan_id": m["plan_id"], "confirm_token": m["confirm_token"]})
	if isErr {
		t.Fatal(text)
	}
	if fk.applied() != 1 || out["run_id"] == "" {
		t.Fatalf("apply count %d, %v", fk.applied(), out)
	}
	// Token is single use.
	_, isErr, _ = call(t, cs, "nrp_run", map[string]any{"plan_id": m["plan_id"], "confirm_token": m["confirm_token"]})
	if !isErr || fk.applied() != 1 {
		t.Fatal("token reused")
	}
	// The applied manifest carries the code ConfigMap, labels and a GPU request.
	var last []byte
	for i, c := range fk.calls {
		if strings.Contains(strings.Join(c, " "), "apply") {
			last = fk.stdins[i]
		}
	}
	s := string(last)
	for _, want := range []string{`"ConfigMap"`, `"nvidia.com/gpu"`, `"app.kubernetes.io/managed-by"`, `"unpack-code"`} {
		if !strings.Contains(s, want) {
			t.Errorf("applied manifest missing %s", want)
		}
	}
}

func TestWebNeedsPublicAck(t *testing.T) {
	cs, fk := setup(t)
	dir := project(t, map[string]string{"app.py": "import streamlit as st\nst.write('hi')\n", "requirements.txt": "streamlit\n"})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir, "goal": "web"})
	if isErr {
		t.Fatal(text)
	}
	sug, _ := m["suggested_names"].([]any)
	if len(sug) < 2 {
		t.Fatalf("want suggested names, got %v", m["suggested_names"])
	}
	url, _ := m["public_url"].(string)
	if !strings.HasPrefix(url, "https://") || m["public_ack_required"] != url {
		t.Fatalf("public url %q ack %v", url, m["public_ack_required"])
	}
	_, isErr, text = call(t, cs, "nrp_run", map[string]any{"plan_id": m["plan_id"], "confirm_token": m["confirm_token"]})
	if !isErr || !strings.Contains(text, "public_ack") || fk.applied() != 0 {
		t.Fatalf("published without ack: %s", text)
	}
	_, isErr, text = call(t, cs, "nrp_run", map[string]any{"plan_id": m["plan_id"], "confirm_token": m["confirm_token"], "public_ack": "https://other.nrp-nautilus.io/"})
	if !isErr || fk.applied() != 0 {
		t.Fatalf("wrong ack accepted: %s", text)
	}
	// The token survived the failed acks (no redeem before the ack check).
	out, isErr, text := call(t, cs, "nrp_run", map[string]any{"plan_id": m["plan_id"], "confirm_token": m["confirm_token"], "public_ack": url})
	if isErr {
		t.Fatal(text)
	}
	if out["url"] != url || fk.applied() != 1 {
		t.Fatalf("%v applied=%d", out, fk.applied())
	}
}

func TestPlanRefusesSpecialGPUWithoutQuota(t *testing.T) {
	cs, _ := setup(t)
	dir := project(t, map[string]string{"train.py": "import torch\n"})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir, "gpu": 1, "gpu_type": "a100"})
	if isErr {
		t.Fatal(text)
	}
	if m["runnable"] == true || m["confirm_token"] != nil {
		t.Fatalf("A100 with quota 0 should not be runnable: %v", m)
	}
}

func TestCleanupTwoStep(t *testing.T) {
	cs, fk := setup(t)
	m, isErr, text := call(t, cs, "nrp_cleanup", map[string]any{"run": "r-1"})
	if isErr {
		t.Fatal(text)
	}
	if fk.deleted() != 0 || m["confirm_token"] == nil {
		t.Fatalf("step 1 must only list: %v", m)
	}
	_, isErr, text = call(t, cs, "nrp_cleanup", map[string]any{"plan_id": m["plan_id"], "confirm_token": m["confirm_token"]})
	if isErr || fk.deleted() != 2 { // pods first, then the rest
		t.Fatalf("delete: %s (%d)", text, fk.deleted())
	}
	for _, c := range fk.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, " delete ") && (!strings.Contains(j, "app.kubernetes.io/managed-by=nrp-mcp") || !strings.Contains(j, "nrp-mcp/owner-id=")) {
			t.Fatalf("delete without managed-by and owner selectors: %s", j)
		}
	}
}

func TestVolumeNeedsP1(t *testing.T) {
	cs, _ := setup(t)
	_, isErr, text := call(t, cs, "nrp_plan", map[string]any{"goal": "volume", "size": "10Gi"})
	if !isErr || !strings.Contains(text, "P1") {
		t.Fatalf("volume without data_is_p1 should be refused: %s", text)
	}
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"goal": "volume", "size": "10Gi", "data_is_p1": true})
	if isErr || m["runnable"] != true {
		t.Fatalf("%s %v", text, m)
	}
}

func TestBuildSuggests(t *testing.T) {
	cs, _ := setup(t)
	dir := project(t, map[string]string{"main.py": "import pandas\n", "requirements.txt": "pandas\n"})
	m, isErr, text := call(t, cs, "nrp_build", map[string]any{"project": dir})
	if isErr {
		t.Fatal(text)
	}
	df, _ := m["dockerfile_suggestion"].(string)
	if !strings.Contains(df, "pip install") || !strings.Contains(m["gitlab_ci_suggestion"].(string), "kaniko") {
		t.Fatalf("%v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err == nil {
		t.Fatal("nrp_build must not write files")
	}
}

func TestWatchChecksPublicURLByRun(t *testing.T) {
	cs, _ := setup(t)
	m, isErr, text := call(t, cs, "nrp_watch", map[string]any{"target": "web-1"})
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(fmt.Sprint(m["url_check"]), "leafy.nrp-nautilus.io") {
		t.Fatalf("url_check missing: %v", m)
	}
}

func TestSetupToolIsReadOnlyByDefault(t *testing.T) {
	cs, _ := setup(t)
	m, isErr, text := call(t, cs, "nrp_setup", nil)
	if isErr {
		t.Fatal(text)
	}
	checks, _ := m["checks"].([]any)
	if len(checks) < 3 || m["summary"] == "" {
		t.Fatalf("%v", m)
	}
	if m["actions"] != nil {
		t.Fatalf("check must not act: %v", m["actions"])
	}
}

func TestCleanupEveryoneNeedsAdmin(t *testing.T) {
	cs, fk := setup(t)
	// The fake user is admin of ucr-test only.
	_, isErr, text := call(t, cs, "nrp_cleanup", map[string]any{"namespace": "other-ns", "everyone": true})
	if !isErr || !strings.Contains(text, "only an admin") {
		t.Fatalf("non-admin everyone allowed: %s", text)
	}
	m, isErr, text := call(t, cs, "nrp_cleanup", map[string]any{"everyone": true})
	if isErr || !strings.Contains(fmt.Sprint(m["summary"]), "everyone") {
		t.Fatalf("admin everyone: %s %v", text, m)
	}
	for _, c := range fk.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "get job -l") && strings.Contains(j, "owner-id") && strings.Contains(j, "other-ns") {
			t.Fatalf("listed other namespace: %s", j)
		}
	}
}

func TestPlanLabelsOwner(t *testing.T) {
	cs, _ := setup(t)
	dir := project(t, map[string]string{"run.py": "print(1)\n"})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir})
	if isErr {
		t.Fatal(text)
	}
	b, _ := json.Marshal(m["manifests"])
	if !strings.Contains(string(b), "nrp-mcp/owner-id") {
		t.Fatal("objects carry no owner label")
	}
	if strings.Contains(string(b), `"nrp-mcp/owner-id":"https`) {
		t.Fatal("owner label holds the raw username")
	}
}
