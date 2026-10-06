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

// Run runs kubectl with args in namespace ns (empty: none) and optional stdin.
func (r *Runner) Run(ctx context.Context, ns string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	full := append(r.base(ns), args...)
	ex := r.Exec
	if ex == nil {
		ex = osExec
	}
	out, errb, err := ex(ctx, r.Cfg.Kubectl, full, stdin)
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
