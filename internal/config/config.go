// Package config reads ~/.config/nrp-mcp/config.yaml (a tiny flat YAML subset) and env.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the user's settings.
type Config struct {
	Kubeconfig  string
	Context     string
	Namespace   string
	Kubectl     string
	PodsPerRun  int
	GPUsPerRun  int
	HoursPerRun int
	TasksPerRun int
	UploadGB    int
	Path        string
}

// DefaultPath is ~/.config/nrp-mcp/config.yaml (or $XDG_CONFIG_HOME).
func DefaultPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "nrp-mcp", "config.yaml")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "nrp-mcp", "config.yaml")
}

// Load reads the file (missing is fine) then applies env overrides.
func Load(path string) Config {
	c := Config{Context: "nautilus", Kubectl: "kubectl", PodsPerRun: 50, GPUsPerRun: 4, HoursPerRun: 48, TasksPerRun: 10000, UploadGB: 50, Path: path}
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
			k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
			n, _ := strconv.Atoi(v)
			switch k {
			case "kubeconfig":
				c.Kubeconfig = expand(v)
			case "context":
				c.Context = v
			case "namespace":
				c.Namespace = v
			case "kubectl":
				c.Kubectl = expand(v)
			case "pods_per_run":
				c.PodsPerRun = n
			case "gpus_per_run":
				c.GPUsPerRun = n
			case "hours_per_run":
				c.HoursPerRun = n
			case "tasks_per_run":
				c.TasksPerRun = n
			case "upload_gb_per_run":
				c.UploadGB = n
			}
		}
	}
	if v := os.Getenv("NRP_KUBECONFIG"); v != "" {
		c.Kubeconfig = expand(v)
	}
	if v := os.Getenv("NRP_CONTEXT"); v != "" {
		c.Context = v
	}
	if v := os.Getenv("NRP_NAMESPACE"); v != "" {
		c.Namespace = v
	}
	if v := os.Getenv("NRP_KUBECTL"); v != "" {
		c.Kubectl = expand(v)
	}
	return c
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

// Example is written by `nrp-mcp init`.
const Example = `# nrp-mcp settings. All optional.
kubeconfig: ~/.kube/config    # the NRP config from https://nrp.ai/config
context: nautilus
namespace:                    # your default namespace (nrp_status lists yours)
kubectl: kubectl              # full path if kubectl is not on PATH
# caps: plans over these are refused
pods_per_run: 50              # pods running at the same time (a sweep's parallel)
gpus_per_run: 4               # GPUs in use at the same time
tasks_per_run: 10000          # total tasks in one sweep
hours_per_run: 48
upload_gb_per_run: 50
`
