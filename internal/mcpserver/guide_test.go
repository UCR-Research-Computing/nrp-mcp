package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/config"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/kube"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/ops"
	nrpsetup "github.com/UCR-Research-Computing/nrp-mcp/internal/setup"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/store"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/version"
)

// guideServer builds a server with a fake kubectl, an optional S3 keys file, a fake
// laptop (setup env) and, if elicit is set, a client that can show forms.
func guideServer(t *testing.T, fk *fakeKube, withKeys bool, laptopReady bool, elicit func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)) *mcp.ClientSession {
	t.Helper()
	k := kube.New(kube.Config{Context: "nautilus"})
	k.Exec = fk.exec
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(t.TempDir(), "s3.env")
	if withKeys {
		if err := os.WriteFile(keys, []byte("AWS_ACCESS_KEY_ID=a\nAWS_SECRET_ACCESS_KEY=b\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{Cfg: config.Config{Context: "nautilus", PodsPerRun: 50, GPUsPerRun: 4, HoursPerRun: 48, Consent: "ask", S3KeysFile: keys},
		Ops: &ops.Ops{K: k}, Store: st, Codes: testCodes(t), HTTP: func(string) (int, error) { return 404, nil }, Now: time.Now,
		VersionChecker: &version.Checker{Current: version.Version, HTTP: func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }},
		Setup:          func() *nrpsetup.Env { return fakeLaptop(t, laptopReady) }}
	ct, stt := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := New(s).Connect(ctx, stt, nil); err != nil {
		t.Fatal(err)
	}
	var opts *mcp.ClientOptions
	if elicit != nil {
		opts = &mcp.ClientOptions{ElicitationHandler: elicit}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, opts).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// fakeLaptop is a setup env where kubectl, kubelogin and the NRP config are present
// (ready) or nothing is installed (not ready). It never touches the real computer.
func fakeLaptop(t *testing.T, ready bool) *nrpsetup.Env {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	e := &nrpsetup.Env{GOOS: "linux", GOARCH: "amd64", Home: home, BinDir: bin, PathEnv: "/usr/bin", Now: time.Now,
		Downloads: []string{filepath.Join(home, "Downloads")},
		HTTP:      func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") },
		LookPath:  func(string) (string, error) { return "", errors.New("not found") }}
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		j := strings.Join(args, " ")
		switch {
		case strings.HasSuffix(name, "kubectl") && strings.Contains(j, "version --client"):
			return []byte(`{"clientVersion":{"gitVersion":"v1.37.1"}}`), nil
		case strings.HasSuffix(name, "kubectl-oidc_login") && j == "--version":
			return []byte("kubelogin version v1.36.4"), nil
		}
		return nil, errors.New("not in test")
	}
	if ready {
		_ = os.MkdirAll(bin, 0o755)
		for _, f := range []string{"kubectl", "kubectl-oidc_login"} {
			_ = os.WriteFile(filepath.Join(bin, f), []byte("x"), 0o755)
		}
		e.PathEnv = bin + ":/usr/bin"
		_ = os.MkdirAll(filepath.Join(home, ".kube"), 0o700)
		_ = os.WriteFile(filepath.Join(home, ".kube", "config"), []byte(`apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: AAAA
    server: https://67.58.53.148:443
  name: nautilus
contexts:
- context:
    cluster: nautilus
    user: oidc
  name: nautilus
current-context: nautilus
kind: Config
users:
- name: oidc
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: kubectl
      args: [oidc-login, get-token, --oidc-issuer-url=https://authentik.nrp-nautilus.io/application/o/k8s/]
`), 0o600)
	}
	return e
}

func TestPlanAsksForMissingDetailsInAForm(t *testing.T) {
	asked := 0
	var schema string
	fk := &fakeKube{}
	dir := project(t, map[string]string{"prep.py": "print(1)\n", "fit.py": "print(2)\n"})
	cs := guideServer(t, fk, false, true, func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked++
		schema = fmt.Sprint(req.Params.RequestedSchema)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"goal": "job", "file": "fit.py", "gpu": "no", "results": "discard"}}, nil
	})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir})
	if isErr {
		t.Fatal(text)
	}
	if asked != 1 {
		t.Fatalf("want one form, got %d", asked)
	}
	for _, f := range []string{"goal", "file", "gpu", "results"} {
		if !strings.Contains(schema, f) {
			t.Errorf("form lacks %s: %s", f, schema)
		}
	}
	if m["runnable"] != true || !strings.Contains(fmt.Sprint(m["summary"]), "fit.py") {
		t.Fatalf("answers not applied: %v", m)
	}
	if !strings.Contains(fmt.Sprint(m["decisions"]), "discard results") || !strings.Contains(fmt.Sprint(m["decisions"]), "CPU only") {
		t.Fatalf("answers not recorded: %v", m["decisions"])
	}
	ans, _ := m["answers_to_reuse"].(map[string]any)
	if ans["command"] != "python fit.py" || ans["discard_results"] != true || fmt.Sprint(ans["gpu"]) != "0" {
		t.Fatalf("answers_to_reuse %v", ans)
	}
	if fk.applied() != 0 {
		t.Fatal("planning applied something")
	}
}

func TestPlanWithoutFormSupportKeepsTextQuestion(t *testing.T) {
	fk := &fakeKube{}
	dir := project(t, map[string]string{"prep.py": "print(1)\n", "fit.py": "print(2)\n"})
	cs := guideServer(t, fk, true, true, nil)
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir})
	if isErr {
		t.Fatal(text)
	}
	if !strings.Contains(fmt.Sprint(m["next"]), "Which file") || m["runnable"] == true {
		t.Fatalf("want the text question in next: %v", m)
	}
}

func TestPlanAsksNothingWhenComplete(t *testing.T) {
	asked := 0
	fk := &fakeKube{}
	dir := project(t, map[string]string{"train.py": "print(2)\n"})
	cs := guideServer(t, fk, true, true, func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked++
		return &mcp.ElicitResult{Action: "decline"}, nil
	})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir, "goal": "job", "gpu": 0})
	if isErr || asked != 0 || m["runnable"] != true {
		t.Fatalf("asked=%d %v %s", asked, m, text)
	}
}

func TestPlanFormDeclinedFallsBack(t *testing.T) {
	fk := &fakeKube{}
	dir := project(t, map[string]string{"prep.py": "print(1)\n", "fit.py": "print(2)\n"})
	cs := guideServer(t, fk, true, true, func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "decline"}, nil
	})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir})
	if isErr || m["runnable"] == true || !strings.Contains(fmt.Sprint(m["decisions"]), "skipped the details form") {
		t.Fatalf("%v %s", m, text)
	}
}

func TestExplicitGPUZeroMeansCPUOnly(t *testing.T) {
	cs, _ := setup(t)
	dir := project(t, map[string]string{"train.py": "import torch\nx=torch.zeros(1).cuda()\n", "requirements.txt": "torch\n"})
	m, isErr, text := call(t, cs, "nrp_plan", map[string]any{"project": dir, "gpu": 0, "discard_results": true})
	if isErr {
		t.Fatal(text)
	}
	if m["needs_person_approval"] == true || strings.Contains(fmt.Sprint(m["resources"]), "gpu:1") {
		t.Fatalf("gpu=0 still planned a GPU: %v", m)
	}
	m, _, _ = call(t, cs, "nrp_plan", map[string]any{"project": dir, "discard_results": true})
	if m["needs_person_approval"] != true {
		t.Fatalf("GPU code without gpu= should still get 1 GPU: %v", m)
	}
}

func guideCall(t *testing.T, cs *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	m, isErr, text := call(t, cs, "nrp_guide", args)
	if isErr {
		t.Fatal(text)
	}
	return m
}

func TestGuideStages(t *testing.T) {
	t.Run("laptop", func(t *testing.T) {
		m := guideCall(t, guideServer(t, &fakeKube{}, true, false, nil), nil)
		if m["stage"] != "laptop" || m["terminal_command"] != "nrp-mcp setup" || !strings.Contains(fmt.Sprint(m["next"]), "fix=true") {
			t.Fatalf("%v", m)
		}
	})
	t.Run("sign-in", func(t *testing.T) {
		fk := &fakeKube{fail: map[string]string{"auth whoami": "error: You must be logged in to the server (Unauthorized)"}}
		m := guideCall(t, guideServer(t, fk, true, true, nil), nil)
		if m["stage"] != "sign-in" || !strings.Contains(fmt.Sprint(m["terminal_command"]), "auth whoami") {
			t.Fatalf("%v", m)
		}
	})
	t.Run("namespace", func(t *testing.T) {
		fk := &fakeKube{out: map[string]string{"auth whoami": `{"status":{"userInfo":{"username":"https://cilogon.org/serverE/users/1","groups":["system:authenticated"]}}}`}}
		m := guideCall(t, guideServer(t, fk, true, true, nil), nil)
		if m["stage"] != "namespace" || !strings.Contains(fmt.Sprint(m["ask_the_person"]), "nrp.ai/namespaces") {
			t.Fatalf("%v", m)
		}
	})
	t.Run("results", func(t *testing.T) {
		m := guideCall(t, guideServer(t, &fakeKube{}, false, true, nil), nil)
		if m["stage"] != "results" || m["terminal_command"] != "nrp-mcp s3 setup" {
			t.Fatalf("%v", m)
		}
	})
	t.Run("project", func(t *testing.T) {
		fk := &fakeKube{out: map[string]string{"get job -l": `{"items":[]}`}}
		m := guideCall(t, guideServer(t, fk, true, true, nil), map[string]any{"project": "/tmp/x"})
		if m["stage"] != "project" || !strings.Contains(fmt.Sprint(m["ask_the_person"]), "/tmp/x") {
			t.Fatalf("%v", m)
		}
		steps := fmt.Sprint(m["steps"])
		if !strings.Contains(steps, "status:done") || !strings.Contains(steps, "status:now") || !strings.Contains(steps, "status:todo") {
			t.Fatalf("steps %s", steps)
		}
	})
	for _, tc := range []struct{ name, status, stage, want string }{
		{"finished", `"succeeded":1`, "finished", "nrp_data action=down run=r-1"},
		{"failed", `"failed":3`, "failed", "nrp_watch target=r-1"},
		{"running", `"active":1`, "running", "nrp_watch target=r-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fk := &fakeKube{out: map[string]string{"get job -l": `{"items":[{"metadata":{"name":"train","labels":{"app.kubernetes.io/managed-by":"nrp-mcp","nrp-mcp/run":"r-1"},"creationTimestamp":"2026-10-06T10:00:00Z"},"status":{` + tc.status + `}}]}`}}
			m := guideCall(t, guideServer(t, fk, true, true, nil), nil)
			if m["stage"] != tc.stage || !strings.Contains(fmt.Sprint(m["next"]), tc.want) {
				t.Fatalf("%v", m)
			}
		})
	}
}

func TestWalkMeThroughPrompt(t *testing.T) {
	cs, _ := setup(t)
	r, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "walk_me_through"})
	if err != nil {
		t.Fatal(err)
	}
	txt := r.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(txt, "nrp_guide") || !strings.Contains(txt, "one question at a time") {
		t.Fatal(txt)
	}
}
