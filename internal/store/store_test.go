package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/plan"
)

func mkPlan(t *testing.T) *plan.Plan {
	p, err := plan.Build(nil, plan.Request{Namespace: "ucr-example", Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTokenLifecycle(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := mkPlan(t)
	if err := s.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	q, err := s.LoadPlan(p.ID)
	if err != nil || q.Hash != p.Hash || plan.Hash(q) != p.Hash {
		t.Fatalf("reload changed the plan hash: %v", err)
	}
	tok, _, err := s.Issue(p, "run")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Redeem(tok, q, "cleanup"); !errors.Is(err, ErrToken) {
		t.Fatalf("wrong action accepted: %v", err)
	}
	if err := s.Redeem(tok, q, "run"); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if err := s.Redeem(tok, q, "run"); !errors.Is(err, ErrUsed) {
		t.Fatalf("reuse accepted: %v", err)
	}
}

func TestTokenExpiryAndChange(t *testing.T) {
	s, _ := New(t.TempDir())
	now := time.Now()
	s.Now = func() time.Time { return now }
	p := mkPlan(t)
	tok, _, _ := s.Issue(p, "run")
	s.Now = func() time.Time { return now.Add(11 * time.Minute) }
	if err := s.Redeem(tok, p, "run"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired accepted: %v", err)
	}
	s.Now = func() time.Time { return now }
	tok2, _, _ := s.Issue(p, "run")
	p.Objects[0].JobSpec.Template.Spec.Containers[0].Image = "evil:latest"
	if err := s.Redeem(tok2, p, "run"); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed plan accepted: %v", err)
	}
	if err := s.Redeem("nrp-bogus", p, "run"); !errors.Is(err, ErrToken) {
		t.Fatalf("bogus accepted: %v", err)
	}
}

func TestBadPlanID(t *testing.T) {
	s, _ := New(t.TempDir())
	if _, err := s.LoadPlan("../../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestRunCardPrivate(t *testing.T) {
	dir := t.TempDir()
	p, err := RunCard(dir, map[string]any{"run_id": "r1"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("run card mode %v", st.Mode().Perm())
	}
	b, err := os.ReadFile(filepath.Join(dir, ".nrp", ".gitignore"))
	if err != nil || !strings.Contains(string(b), "*") {
		t.Fatalf(".nrp/.gitignore: %v %q", err, b)
	}
}

func TestTokensSurviveTwoStoresAtOnce(t *testing.T) {
	// Two Store values on one folder stand in for two nrp-mcp processes (two AI apps).
	dir := t.TempDir()
	a, _ := New(dir)
	b, _ := New(dir)
	p := &plan.Plan{ID: "p1", Goal: "job"}
	p.Hash = plan.Hash(p)
	const n = 20
	toks := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := a
			if i%2 == 1 {
				st = b
			}
			tok, _, err := st.Issue(p, "run")
			if err != nil {
				t.Error(err)
			}
			toks[i] = tok
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := b
			if i%2 == 1 {
				st = a
			}
			if err := st.Redeem(toks[i], p, "run"); err != nil {
				t.Errorf("token %d lost: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if err := a.Redeem(toks[i], p, "run"); err != ErrUsed {
			t.Fatalf("token %d: used flag lost (got %v)", i, err)
		}
	}
}

func TestOldPlansArePruned(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	p := &plan.Plan{ID: "pold"}
	if err := s.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "plans", "pold.json")
	old := time.Now().Add(-8 * 24 * time.Hour)
	_ = os.Chtimes(f, old, old)
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("a plan older than 7 days was kept")
	}
}
