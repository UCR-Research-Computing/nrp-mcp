// Package kube runs kubectl as the signed-in user. nrp-mcp never handles the NRP
// token: kubectl and its oidc-login exec plugin do the sign-in, exactly as the user
// set them up (KB024).
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Config says which kubectl, kubeconfig and context to use.
type Config struct {
	Kubectl    string
	Kubeconfig string
	Context    string
}

// Runner runs kubectl commands. Tests replace Exec.
type Runner struct {
	Cfg     Config
	Timeout time.Duration
	// Exec runs the command; nil means os/exec.
	Exec func(ctx context.Context, name string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

// New returns a Runner with defaults.
func New(cfg Config) *Runner {
	if cfg.Kubectl == "" {
		cfg.Kubectl = "kubectl"
	}
	// nrp_setup installs into a user folder that may not be on PATH yet (fresh laptops,
	// MCP clients started from a desktop launcher). Add it so kubectl and its
	// kubectl-oidc_login plugin are both found.
	if dir := UserBinDir(); dir != "" {
		addToPath(dir)
		if _, err := exec.LookPath(cfg.Kubectl); err != nil && cfg.Kubectl == "kubectl" {
			if p, err := exec.LookPath(filepath.Join(dir, exe("kubectl"))); err == nil {
				cfg.Kubectl = p
			}
		}
	}
	return &Runner{Cfg: cfg, Timeout: 60 * time.Second}
}

// UserBinDir is where nrp_setup installs kubectl and kubelogin.
func UserBinDir() string {
	if runtime.GOOS == "windows" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, "Programs", "nrp-mcp", "bin")
		}
		return ""
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".local", "bin")
}

func exe(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

func addToPath(dir string) {
	cur := os.Getenv("PATH")
	for _, d := range filepath.SplitList(cur) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return
		}
	}
	_ = os.Setenv("PATH", cur+string(os.PathListSeparator)+dir)
}

func (r *Runner) base(ns string) []string {
	var a []string
	if r.Cfg.Kubeconfig != "" {
		a = append(a, "--kubeconfig", r.Cfg.Kubeconfig)
	}
	if r.Cfg.Context != "" {
		a = append(a, "--context", r.Cfg.Context)
	}
	if ns != "" {
		a = append(a, "-n", ns)
	}
	return a
}

// Run runs kubectl with args in namespace ns (empty: none) and optional stdin, with a
// time limit that fits the command (see LimitFor).
func (r *Runner) Run(ctx context.Context, ns string, stdin []byte, args ...string) ([]byte, error) {
	return r.RunTimeout(ctx, r.LimitFor(args), ns, stdin, args...)
}

// LimitFor is the time limit for a kubectl command: sign-in may open a browser and wait
// for the person (5 min), `wait --timeout=X` gets X + 30 s, apply and cp get 2 and 5 min,
// everything else the Runner's default (60 s).
func (r *Runner) LimitFor(args []string) time.Duration {
	d := r.Timeout
	if d <= 0 {
		d = 60 * time.Second
	}
	scale := func(x time.Duration) time.Duration {
		return time.Duration(float64(x) * float64(d) / float64(60*time.Second))
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "whoami" {
		return scale(5 * time.Minute)
	}
	if len(args) > 0 {
		switch args[0] {
		case "wait":
			for _, a := range args {
				if v, ok := strings.CutPrefix(a, "--timeout="); ok {
					if w, err := time.ParseDuration(v); err == nil {
						return scale(w + 30*time.Second)
					}
				}
			}
			return scale(150 * time.Second)
		case "apply":
			return scale(2 * time.Minute)
		case "cp":
			return scale(5 * time.Minute)
		}
	}
	return d
}

// RunTimeout is Run with an explicit time limit.
func (r *Runner) RunTimeout(ctx context.Context, limit time.Duration, ns string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	full := append(r.base(ns), args...)
	ex := r.Exec
	if ex == nil {
		ex = osExec
	}
	out, errb, err := ex(ctx, r.Cfg.Kubectl, full, stdin)
	if err != nil && ctx.Err() != nil {
		return out, fmt.Errorf("kubectl %s: no answer within %s: %w", strings.Join(args, " "), limit.Round(time.Second), context.DeadlineExceeded)
	}
	if err != nil && authRetryable(string(errb)) {
		// The OIDC token was refreshed mid-call (seen on Nautilus); one retry is enough.
		out, errb, err = ex(ctx, r.Cfg.Kubectl, full, stdin)
	}
	if err != nil {
		msg := strings.TrimSpace(string(errb))
		if msg == "" {
			msg = err.Error()
		}
		return out, &Error{Args: args, Msg: msg}
	}
	return out, nil
}

func authRetryable(stderr string) bool {
	return strings.Contains(stderr, "provide credentials") || strings.Contains(stderr, "Unauthorized")
}

// JSON runs kubectl ... -o json and decodes into v.
func (r *Runner) JSON(ctx context.Context, ns string, v any, args ...string) error {
	out, err := r.Run(ctx, ns, nil, append(args, "-o", "json")...)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

// Error is a kubectl failure with its stderr.
type Error struct {
	Args []string
	Msg  string
}

func (e *Error) Error() string { return "kubectl " + strings.Join(e.Args, " ") + ": " + e.Msg }

// Kinds of kubectl failure, from Kind.
const (
	KindSignedOut = "signed-out"
	KindForbidden = "forbidden"
	KindTimeout   = "timeout"
	KindNotFound  = "not-found"
	KindOther     = "other"
)

// Kind sorts a kubectl failure so callers can tell "nothing there" (not-found) from a
// failure that must be reported (signed out, forbidden, timeout, anything else).
func Kind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTimeout
	}
	msg := err.Error()
	var ke *Error
	if errors.As(err, &ke) {
		msg = ke.Msg
	}
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "Unauthorized") || strings.Contains(low, "provide credentials") || strings.Contains(low, "oidc") ||
		strings.Contains(low, "token has expired") || strings.Contains(low, "refresh token"):
		return KindSignedOut
	case strings.Contains(low, "forbidden"):
		return KindForbidden
	case strings.Contains(low, "deadline exceeded") || strings.Contains(low, "i/o timeout") || strings.Contains(low, "signal: killed") ||
		strings.Contains(low, "timed out") || strings.Contains(low, "timeout"):
		return KindTimeout
	case strings.Contains(msg, "NotFound") || strings.Contains(low, "not found") || strings.Contains(low, "the server doesn't have a resource type"):
		return KindNotFound
	}
	return KindOther
}

// Explain turns a kubectl failure into one plain sentence with what to do.
func Explain(what string, err error) error {
	switch Kind(err) {
	case KindSignedOut:
		return fmt.Errorf("could not read %s: your Nautilus sign-in has expired or is missing. Run `kubectl auth whoami` in a terminal to sign in again, then retry (nothing was changed): %w", what, err)
	case KindForbidden:
		return fmt.Errorf("could not read %s: Nautilus says you are not allowed to (check the namespace, or ask its admin to add you): %w", what, err)
	case KindTimeout:
		return fmt.Errorf("could not read %s: Nautilus did not answer in time. Retry in a minute (nothing was changed): %w", what, err)
	}
	return fmt.Errorf("could not read %s: %w", what, err)
}

// IsForbidden reports an RBAC refusal.
func IsForbidden(err error) bool {
	var ke *Error
	return errors.As(err, &ke) && strings.Contains(ke.Msg, "forbidden")
}

// IsNotFound reports a missing object.
func IsNotFound(err error) bool {
	var ke *Error
	return errors.As(err, &ke) && (strings.Contains(ke.Msg, "NotFound") || strings.Contains(ke.Msg, "not found"))
}

func osExec(ctx context.Context, name string, args []string, stdin []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// If kubectl leaves a child (the sign-in plugin) holding its output, stop waiting
	// shortly after the time limit instead of hanging.
	cmd.WaitDelay = 5 * time.Second
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	return so.Bytes(), se.Bytes(), err
}

// Who is the cluster's view of the signed-in user.
type Who struct {
	Username   string   `json:"username"`
	Groups     []string `json:"groups"`
	Namespaces []string `json:"namespaces"`
	Admin      []string `json:"admin_of"`
}

// WhoAmI runs `kubectl auth whoami` and derives namespaces from oidcgroup:<ns>:<role>.
func (r *Runner) WhoAmI(ctx context.Context) (*Who, error) {
	var v struct {
		Status struct {
			UserInfo struct {
				Username string   `json:"username"`
				Groups   []string `json:"groups"`
			} `json:"userInfo"`
		} `json:"status"`
	}
	if err := r.JSON(ctx, "", &v, "auth", "whoami"); err != nil {
		return nil, err
	}
	w := &Who{Username: v.Status.UserInfo.Username, Groups: v.Status.UserInfo.Groups}
	w.Namespaces, w.Admin = NamespacesFromGroups(w.Groups)
	return w, nil
}

// NamespacesFromGroups parses NRP groups like oidcgroup:<ns>:admin.
func NamespacesFromGroups(groups []string) (all, admin []string) {
	seen := map[string]bool{}
	for _, g := range groups {
		p := strings.Split(g, ":")
		if len(p) < 3 || p[0] != "oidcgroup" {
			continue
		}
		ns := p[1]
		if !seen[ns] {
			seen[ns] = true
			all = append(all, ns)
		}
		if p[len(p)-1] == "admin" {
			admin = append(admin, ns)
		}
	}
	return all, admin
}

// Quota is one hard quota line.
type Quota struct {
	Name string            `json:"name"`
	Hard map[string]string `json:"hard"`
	Used map[string]string `json:"used,omitempty"`
}

// Quotas lists resource quotas in ns.
func (r *Runner) Quotas(ctx context.Context, ns string) ([]Quota, error) {
	var v struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Spec     struct {
				Hard map[string]string `json:"hard"`
			} `json:"spec"`
			Status struct {
				Used map[string]string `json:"used"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := r.JSON(ctx, ns, &v, "get", "resourcequota"); err != nil {
		return nil, err
	}
	out := []Quota{}
	for _, i := range v.Items {
		out = append(out, Quota{Name: i.Metadata.Name, Hard: i.Spec.Hard, Used: i.Status.Used})
	}
	return out, nil
}

// GPUQuota returns the hard request quota for special GPU resources, e.g.
// {"nvidia.com/a100": 0}. Missing means not limited by quota.
func GPUQuota(qs []Quota) map[string]int {
	m := map[string]int{}
	for _, q := range qs {
		for k, v := range q.Hard {
			if strings.HasPrefix(k, "requests.nvidia.com/") {
				n := 0
				fmt.Sscanf(v, "%d", &n)
				m[strings.TrimPrefix(k, "requests.")] = n
			}
		}
	}
	return m
}

// Apply applies manifests (YAML or JSON) from stdin; dryRun "server" validates only.
func (r *Runner) Apply(ctx context.Context, ns string, manifests []byte, dryRun string) (string, error) {
	args := []string{"apply", "-f", "-"}
	if dryRun != "" {
		args = append(args, "--dry-run="+dryRun)
	}
	out, err := r.Run(ctx, ns, manifests, args...)
	return strings.TrimSpace(string(out)), err
}
