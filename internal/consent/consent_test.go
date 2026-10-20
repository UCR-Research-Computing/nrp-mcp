package consent

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func newTest(t *testing.T) (*Codes, *[]string) {
	t.Helper()
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var shown []string
	c.Notify = func(title, body string) { shown = append(shown, title+" | "+body) }
	c.Log = func(string) {}
	return c, &shown
}

func TestCodeIsShownOnlyToThePerson(t *testing.T) {
	c, shown := newTest(t)
	code, _, err := c.Issue("p1", "h1", "run", "publish https://x.nrp-nautilus.io/")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 || strings.Trim(code, alphabet) != "" {
		t.Fatalf("code %q", code)
	}
	dashed := code[:3] + "-" + code[3:]
	if len(*shown) != 1 || !strings.Contains((*shown)[0], dashed) {
		t.Fatalf("notification %v", *shown)
	}
	if !strings.Contains(c.Latest(), dashed) || !strings.Contains(c.Latest(), "publish https://x.nrp-nautilus.io/") {
		t.Fatalf("approve.txt %q", c.Latest())
	}
	st, err := os.Stat(c.File())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("approve.txt mode: %v %v", err, st.Mode().Perm())
	}
	raw, _ := os.ReadFile(c.path())
	if strings.Contains(string(raw), code) {
		t.Fatal("approvals.json holds the plain code")
	}
}

func TestCheckLifecycle(t *testing.T) {
	c, _ := newTest(t)
	code, _, err := c.Issue("p1", "h1", "run", "x")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ code, plan, hash, action string }{
		{"ZZZZZZ", "p1", "h1", "run"},
		{code, "p2", "h1", "run"},
		{code, "p1", "h2", "run"},
		{code, "p1", "h1", "cleanup"},
	} {
		if err := c.Check(bad.code, bad.plan, bad.hash, bad.action); !errors.Is(err, ErrWrong) {
			t.Fatalf("%+v: want ErrWrong, got %v", bad, err)
		}
	}
	typed := " " + strings.ToLower(code[:3]) + "-" + code[3:] + " "
	if err := c.Check(typed, "p1", "h1", "run"); err != nil {
		t.Fatalf("typed with dash, spaces and lower case: %v", err)
	}
	if err := c.Check(code, "p1", "h1", "run"); !errors.Is(err, ErrUsed) {
		t.Fatalf("reuse: %v", err)
	}
}

func TestExpiry(t *testing.T) {
	c, _ := newTest(t)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	code, _, err := c.Issue("p1", "h1", "run", "x")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(TTL + time.Second)
	if err := c.Check(code, "p1", "h1", "run"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestEmptyCodeRefused(t *testing.T) {
	c, _ := newTest(t)
	if err := c.Check(" - ", "p1", "h1", "run"); !errors.Is(err, ErrWrong) {
		t.Fatalf("empty: %v", err)
	}
}
