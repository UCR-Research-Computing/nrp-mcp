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
	// Consent says how risky steps are approved: "ask" (default) asks the person in the
	// AI app when it can show a question, else falls back to "code"; "code" always shows a
	// 6-character approval code to the person only; "token" is the old 0.8 behavior with no
	// human check, for scripts and CI.
	Consent string
	// WebDays is how long a public web app stays up before nrp_status calls it expired.
	WebDays int
	// S3 results storage (all optional; see SPEC section 5.4).
	S3Endpoint   string // outside endpoint, default https://s3-west.nrp-nautilus.io
	S3Internal   string // inside endpoint pods use, default http://rook-ceph-rgw-nautiluss3.rook
	S3Bucket     string // default: nrp-results-<first 10 hex of your owner id>
	S3KeysFile   string // default ~/.config/nrp-mcp/s3.env
	S3SecretName string // Kubernetes Secret with your S3 keys, default nrp-s3-<owner short>
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
	c := Config{Context: "nautilus", Kubectl: "kubectl", PodsPerRun: 50, GPUsPerRun: 4, HoursPerRun: 48, TasksPerRun: 10000, UploadGB: 50, Path: path,
		Consent: "ask", WebDays: 14, S3Endpoint: "https://s3-west.nrp-nautilus.io", S3Internal: "http://rook-ceph-rgw-nautiluss3.rook"}
	c.S3KeysFile = filepath.Join(filepath.Dir(path), "s3.env") // missing file = results not set up
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
			case "consent":
				c.Consent = strings.ToLower(v)
			case "web_days":
				c.WebDays = n
			case "s3_endpoint":
				c.S3Endpoint = v
			case "s3_internal_endpoint":
				c.S3Internal = v
			case "s3_bucket":
				c.S3Bucket = v
			case "s3_keys_file":
				c.S3KeysFile = expand(v)
			case "s3_secret":
				c.S3SecretName = v
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
	if v := os.Getenv("NRP_CONSENT"); v != "" {
		c.Consent = strings.ToLower(v)
	}
	if c.Consent != "code" && c.Consent != "token" {
		c.Consent = "ask"
	}
	if c.WebDays <= 0 || c.WebDays > 14 {
		c.WebDays = 14
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
# how risky steps (public web, GPUs, deleting, uploads, installs) are approved:
#   ask   = your AI app asks you directly if it can; otherwise a code (default)
#   code  = a 6-character code is shown to you only; type it into the chat
#   token = no human check (0.8 behavior); only for scripts and CI
consent: ask
web_days: 14                  # public web apps expire after this many days (max 14)
# results storage (NRP S3). Keys: https://nrp.ai/s3 -> put them in s3_keys_file
# s3_endpoint: https://s3-west.nrp-nautilus.io
# s3_bucket: nrp-results-<your id>
# s3_keys_file: ~/.config/nrp-mcp/s3.env
`
