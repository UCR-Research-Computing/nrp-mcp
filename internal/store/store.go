// Package store keeps plans, single-use confirm tokens, run cards and the audit log on
// the user's disk. Tokens are stored only as sha256 hashes.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/plan"
)

// TokenTTL is how long a confirm token is valid.
const TokenTTL = 10 * time.Minute

// Store is rooted at a state directory (~/.local/state/nrp-mcp).
type Store struct {
	Dir string
	Now func() time.Time
	mu  sync.Mutex
}

// New creates the directory tree (0700).
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "plans"), 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir, Now: time.Now}, nil
}

// DefaultDir is ~/.local/state/nrp-mcp (or $XDG_STATE_HOME/nrp-mcp).
func DefaultDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "nrp-mcp")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "state", "nrp-mcp")
}

// SavePlan writes a plan.
func (s *Store) SavePlan(p *plan.Plan) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "plans", p.ID+".json"), b, 0o600)
}

// LoadPlan reads a plan by id.
func (s *Store) LoadPlan(id string) (*plan.Plan, error) {
	if !validID(id) {
		return nil, fmt.Errorf("bad plan id %q", id)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "plans", id+".json"))
	if err != nil {
		return nil, fmt.Errorf("no plan %s (plans are kept on this computer; make a new one with nrp_plan)", id)
	}
	var p plan.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

type tokenRec struct {
	Hash     string    `json:"token_sha256"`
	PlanID   string    `json:"plan_id"`
	PlanHash string    `json:"plan_hash"`
	Action   string    `json:"action"`
	Expires  time.Time `json:"expires"`
	Used     bool      `json:"used"`
}

func (s *Store) tokPath() string { return filepath.Join(s.Dir, "tokens.json") }

func (s *Store) loadTokens() []tokenRec {
	var t []tokenRec
	b, err := os.ReadFile(s.tokPath())
	if err == nil {
		_ = json.Unmarshal(b, &t)
	}
	return t
}

func (s *Store) saveTokens(t []tokenRec) error {
	now := s.Now()
	var keep []tokenRec
	for _, r := range t {
		if now.Before(r.Expires.Add(24 * time.Hour)) {
			keep = append(keep, r)
		}
	}
	b, _ := json.MarshalIndent(keep, "", "  ")
	return os.WriteFile(s.tokPath(), b, 0o600)
}

func sum(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// Issue makes a single-use token for (plan, action), bound to the plan hash.
func (s *Store) Issue(p *plan.Plan, action string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	tok := "nrp-" + base64.RawURLEncoding.EncodeToString(raw)
	exp := s.Now().Add(TokenTTL)
	t := s.loadTokens()
	t = append(t, tokenRec{Hash: sum(tok), PlanID: p.ID, PlanHash: p.Hash, Action: action, Expires: exp})
	return tok, exp, s.saveTokens(t)
}

// Errors from Redeem.
var (
	ErrToken   = errors.New("that confirm token is not valid for this plan and action (make or re-run nrp_plan to get a fresh one)")
	ErrUsed    = errors.New("that confirm token was already used; tokens are single use")
	ErrExpired = errors.New("that confirm token has expired (10 minutes); run nrp_plan again")
	ErrChanged = errors.New("the plan changed after the token was issued; review the new plan and confirm again")
)

// Redeem checks and consumes a token for (plan, action). It recomputes the plan hash.
func (s *Store) Redeem(tok string, p *plan.Plan, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.loadTokens()
	h := sum(tok)
	for i := range t {
		r := &t[i]
		if r.Hash != h {
			continue
		}
		if r.PlanID != p.ID || r.Action != action {
			return ErrToken
		}
		if r.Used {
			return ErrUsed
		}
		if s.Now().After(r.Expires) {
			return ErrExpired
		}
		if plan.Hash(p) != r.PlanHash || p.Hash != r.PlanHash {
			return ErrChanged
		}
		r.Used = true
		return s.saveTokens(t)
	}
	return ErrToken
}

// Audit appends one JSON line describing a change.
func (s *Store) Audit(entry map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry["time"] = s.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(entry)
	f, err := os.OpenFile(filepath.Join(s.Dir, "audit.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// RunCard writes <project>/.nrp/runs/<run>.json (if a project folder is known).
func RunCard(project string, card map[string]any) (string, error) {
	if project == "" {
		return "", nil
	}
	dir := filepath.Join(project, ".nrp", "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	run, _ := card["run_id"].(string)
	if run == "" {
		run = time.Now().UTC().Format("20060102-150405")
	}
	p := filepath.Join(dir, run+".json")
	b, _ := json.MarshalIndent(card, "", "  ")
	return p, os.WriteFile(p, b, 0o644)
}
