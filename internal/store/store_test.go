package store

import (
	"errors"
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
