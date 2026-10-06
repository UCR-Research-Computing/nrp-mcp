// Package setup checks and readies a researcher's laptop for Nautilus: kubectl, the
// kubelogin plugin, the NRP kubeconfig and sign-in. Checks are read-only. Fixes download
// the official release binaries into a user folder (no admin rights), verify their
// published SHA256, and never overwrite anything without a backup.
package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Status of one check.
const (
	OK      = "ok"
	Missing = "missing"
	Old     = "update"
	Problem = "problem"
	Manual  = "manual"
	Note    = "note" // advisory; does not block "ready"
)

// Check is one line of the readiness report.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	Auto   bool   `json:"can_fix_automatically"`
}

// Report is the result of Check or Fix.
type Report struct {
	Summary  string   `json:"summary"`
	Ready    bool     `json:"ready"`
	OS       string   `json:"os"`
	BinDir   string   `json:"bin_dir"`
	Checks   []Check  `json:"checks"`
	Actions  []string `json:"actions,omitempty"`
	Next     string   `json:"next"`
	PathHint string   `json:"path_hint,omitempty"`
}

// Env holds everything Setup touches, so tests can fake it.
type Env struct {
	GOOS, GOARCH string
	Home         string
	Kubeconfig   string // empty: $KUBECONFIG or ~/.kube/config
	Context      string // kubeconfig context to check (default nautilus)
	BinDir       string // where fixes install binaries
	Kubectl      string // configured kubectl (name or path)
	PathEnv      string
	HTTP         func(ctx context.Context, url string) ([]byte, error)
	Run          func(ctx context.Context, name string, args ...string) ([]byte, error)
	LookPath     func(string) (string, error)
	Now          func() time.Time
	Downloads    []string      // folders to look for a downloaded NRP config
	cluster      func() string // tests: cluster version instead of asking the server
}

// Defaults for the NRP.
const (
	NRPServerHint  = "nrp-nautilus.io"
	NRPConfigURL   = "https://nrp.ai/config"
	KubectlBase    = "https://dl.k8s.io/release"
	KubeloginAPI   = "https://api.github.com/repos/int128/kubelogin/releases/latest"
	KubeloginBase  = "https://github.com/int128/kubelogin/releases/download"
	maxDownload    = 200 << 20
	defaultContext = "nautilus"
)

// DefaultEnv builds an Env for this machine.
func DefaultEnv(kubeconfig, kubectx, kubectl string) *Env {
	home, _ := os.UserHomeDir()
	e := &Env{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Home: home, Kubeconfig: kubeconfig, Context: kubectx, Kubectl: kubectl,
		PathEnv: os.Getenv("PATH"), LookPath: exec.LookPath, Now: time.Now}
	e.BinDir = filepath.Join(home, ".local", "bin")
	if e.GOOS == "windows" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			e.BinDir = filepath.Join(la, "Programs", "nrp-mcp", "bin")
		}
	}
	e.Downloads = []string{filepath.Join(home, "Downloads")}
	e.HTTP = httpGet
	e.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		if err != nil && se.Len() > 0 {
			return so.Bytes(), fmt.Errorf("%v: %s", err, strings.TrimSpace(se.String()))
		}
		return so.Bytes(), err
	}
	return e
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nrp-mcp-setup")
	c := &http.Client{Timeout: 5 * time.Minute}
	r, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, r.StatusCode)
	}
	return io.ReadAll(io.LimitReader(r.Body, maxDownload))
}

func (e *Env) exe(name string) string {
	if e.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func (e *Env) kubeconfigPath() string {
	if e.Kubeconfig != "" {
		return e.Kubeconfig
	}
	if k := os.Getenv("KUBECONFIG"); k != "" {
		return strings.Split(k, string(os.PathListSeparator))[0]
	}
	return filepath.Join(e.Home, ".kube", "config")
}

func (e *Env) ctxName() string {
	if e.Context != "" {
		return e.Context
	}
	return defaultContext
}

// findKubectl returns the kubectl to use: configured path, then PATH, then BinDir.
func (e *Env) findKubectl() string {
	if e.Kubectl != "" && e.Kubectl != "kubectl" {
		if _, err := os.Stat(e.Kubectl); err == nil {
			return e.Kubectl
		}
	}
	if p, err := e.LookPath(e.exe("kubectl")); err == nil {
		return p
	}
	p := filepath.Join(e.BinDir, e.exe("kubectl"))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func (e *Env) findKubelogin() string {
	if p, err := e.LookPath(e.exe("kubectl-oidc_login")); err == nil {
		return p
	}
	p := filepath.Join(e.BinDir, e.exe("kubectl-oidc_login"))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func (e *Env) ensureOnProcessPath() {
	if e.BinDir == "" || e.inPath(e.BinDir) && pathHas(os.Getenv("PATH"), e.BinDir) {
		return
	}
	if _, err := os.Stat(e.BinDir); err != nil {
		return
	}
	if !pathHas(os.Getenv("PATH"), e.BinDir) {
		_ = os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+e.BinDir)
	}
}

func pathHas(path, dir string) bool {
	for _, d := range filepath.SplitList(path) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func (e *Env) inPath(dir string) bool {
	for _, d := range filepath.SplitList(e.PathEnv) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

var verRe = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

// minor returns (major, minor) of a version string, or 0,0.
func minor(v string) (int, int) {
	m := verRe.FindStringSubmatch(v)
	if m == nil {
		return 0, 0
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a, b
}

// serverInfo reads the API server URL and CA from the kubeconfig (no kubectl needed, so
// a fresh laptop gets the right kubectl version on the first download) and asks the
// public /version endpoint. The NRP config holds no secrets; the sign-in token lives in
// kubelogin's cache, which nrp-mcp never reads.
func (e *Env) serverInfo(ctx context.Context, _ string) (server, version string) {
	if e.cluster != nil {
		if _, err := os.Stat(e.kubeconfigPath()); err != nil {
			return "", ""
		}
		return "test", e.cluster()
	}
	b, err := os.ReadFile(e.kubeconfigPath())
	if err != nil || !isNRPConfig(b) {
		return "", ""
	}
	server, caData := clusterFromConfig(string(b), e.ctxName())
	if server == "" {
		return "", ""
	}
	pool := x509.NewCertPool()
	if pem, err := base64.StdEncoding.DecodeString(caData); err == nil {
		pool.AppendCertsFromPEM(pem)
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(server, "/")+"/version", nil)
	if err != nil {
		return server, ""
	}
	r, err := c.Do(req)
	if err != nil {
		return server, ""
	}
	defer r.Body.Close()
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&v)
	return server, v.GitVersion
}

// clusterFromConfig finds the server and CA of the cluster used by context ctxName in a
// kubeconfig, with a small line scanner (kubeconfigs from the NRP are simple YAML).
func clusterFromConfig(cfg, ctxName string) (server, caData string) {
	type cl struct{ server, ca string }
	clusters := map[string]cl{}
	ctxCluster := map[string]string{}
	var section, curName, curServer, curCA, curCtxCluster string
	flush := func() {
		switch section {
		case "clusters":
			if curName != "" {
				clusters[curName] = cl{curServer, curCA}
			}
		case "contexts":
			if curName != "" {
				ctxCluster[curName] = curCtxCluster
			}
		}
		curName, curServer, curCA, curCtxCluster = "", "", "", ""
	}
	for _, raw := range strings.Split(cfg, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line != "" && line[0] != ' ' && line[0] != '-' {
			flush()
			section = strings.TrimSuffix(strings.Fields(line)[0], ":")
			continue
		}
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "- ") {
			flush()
			s = strings.TrimSpace(s[2:])
		}
		k, v, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "name":
			curName = v
		case "server":
			curServer = v
		case "certificate-authority-data":
			curCA = v
		case "cluster":
			if section == "contexts" && v != "" {
				curCtxCluster = v
			}
		}
	}
	flush()
	name := ctxCluster[ctxName]
	if name == "" {
		name = ctxName
	}
	if c, ok := clusters[name]; ok {
		return c.server, c.ca
	}
	for _, c := range clusters {
		if strings.Contains(c.server, NRPServerHint) || strings.Contains(c.server, "67.58.53.148") {
			return c.server, c.ca
		}
	}
	return "", ""
}

// Inspect runs every check (read-only; the sign-in check may open a browser only if
// signIn is true).
func (e *Env) Inspect(ctx context.Context, signIn bool) *Report {
	rep := &Report{OS: e.GOOS + "/" + e.GOARCH, BinDir: e.BinDir}
	// The NRP config's sign-in runs `kubectl oidc-login`, looked up on PATH; make the
	// install folder visible to everything nrp-mcp starts (not to the user's own shell).
	e.ensureOnProcessPath()
	add := func(c Check) { rep.Checks = append(rep.Checks, c) }

	// 1. kubectl
	kubectl := e.findKubectl()
	serverVer := ""
	if kubectl == "" {
		add(Check{ID: "kubectl", Title: "kubectl (the Kubernetes command line)", Status: Missing, Detail: "not found on PATH or in " + e.BinDir,
			Fix: "install the official kubectl into " + e.BinDir, Auto: true})
	} else {
		cv := ""
		if out, err := e.Run(ctx, kubectl, "version", "--client", "-o", "json"); err == nil {
			var v struct {
				ClientVersion struct {
					GitVersion string `json:"gitVersion"`
				} `json:"clientVersion"`
			}
			_ = json.Unmarshal(out, &v)
			cv = v.ClientVersion.GitVersion
		}
		_, serverVer = e.serverInfo(ctx, kubectl)
		c := Check{ID: "kubectl", Title: "kubectl (the Kubernetes command line)", Status: OK, Detail: fmt.Sprintf("%s at %s", cv, kubectl)}
		if serverVer != "" {
			c.Detail += ", cluster " + serverVer
			_, cm := minor(cv)
			_, sm := minor(serverVer)
			if cm > 0 && sm > 0 && (cm < sm-1 || cm > sm+1) {
				c.Status, c.Auto = Note, true
				dir := "older"
				if cm > sm {
					dir = "newer"
				}
				c.Fix = fmt.Sprintf("kubectl %s is %s than the cluster (%s) by more than one minor version, outside Kubernetes' supported skew; it usually still works. nrp_setup fix=true installs a matching kubectl into %s (your current one is kept as a backup)", cv, dir, serverVer, e.BinDir)
				if cm < sm-1 {
					c.Status = Old
				}
			}
		}
		if cv == "" {
			c.Status, c.Detail, c.Auto = Problem, "found "+kubectl+" but it did not report a version", true
			c.Fix = "install the official kubectl into " + e.BinDir
		}
		add(c)
	}

	// 2. kubelogin
	kl := e.findKubelogin()
	if kl == "" {
		add(Check{ID: "kubelogin", Title: "kubelogin (the sign-in plugin, kubectl-oidc_login)", Status: Missing, Detail: "not found on PATH or in " + e.BinDir,
			Fix: "install the official kubelogin into " + e.BinDir + " as " + e.exe("kubectl-oidc_login"), Auto: true})
	} else {
		v := ""
		if out, err := e.Run(ctx, kl, "--version"); err == nil {
			v = strings.TrimSpace(string(out))
		}
		add(Check{ID: "kubelogin", Title: "kubelogin (the sign-in plugin, kubectl-oidc_login)", Status: OK, Detail: strings.TrimSpace(v + " at " + kl)})
	}

	// 3. PATH
	if !e.inPath(e.BinDir) && (kubectl == "" || strings.HasPrefix(kubectl, e.BinDir) || kl == "" || strings.HasPrefix(kl, e.BinDir)) {
		add(Check{ID: "path", Title: "Install folder on PATH", Status: Note, Detail: e.BinDir + " is not on your PATH (nrp-mcp does not need it)",
			Fix: "nrp-mcp uses the full path itself; for your own terminal: " + pathHint(e), Auto: false})
		rep.PathHint = pathHint(e)
	}

	// 4. NRP config
	kc := e.kubeconfigPath()
	b, err := os.ReadFile(kc)
	switch {
	case err != nil:
		c := Check{ID: "config", Title: "NRP config file", Status: Manual, Detail: kc + " does not exist",
			Fix: "sign in at " + NRPConfigURL + " (top right: Get Config), save the file; nrp_setup can then put it in place", Auto: false}
		if dl := e.findDownloadedConfig(); dl != "" {
			c.Detail += "; found a downloaded NRP config at " + dl
			c.Fix = "copy " + dl + " to " + kc
			c.Auto = true
		}
		add(c)
	case !isNRPConfig(b):
		c := Check{ID: "config", Title: "NRP config file", Status: Manual, Detail: kc + " exists but has no Nautilus cluster (it may be for another cluster)",
			Fix: "download the NRP config from " + NRPConfigURL + "; nrp_setup can merge it in with a backup (or point nrp-mcp at it with kubeconfig:)"}
		if dl := e.findDownloadedConfig(); dl != "" {
			c.Detail += "; found a downloaded NRP config at " + dl
			c.Fix = "merge " + dl + " into " + kc + " (backup first)"
			c.Auto = true
		}
		add(c)
	default:
		c := Check{ID: "config", Title: "NRP config file", Status: OK, Detail: kc + " has the Nautilus cluster"}
		if !strings.Contains(string(b), "oidc-login") {
			c.Status, c.Detail = Problem, kc+" has Nautilus but not the oidc-login sign-in; download a fresh config from "+NRPConfigURL
		}
		add(c)
	}

	// 5. Sign-in
	ready := allOK(rep.Checks)
	if ready && kubectl != "" {
		if !signIn {
			add(Check{ID: "signin", Title: "Signed in to Nautilus", Status: Manual, Detail: "not checked yet (it opens your browser the first time)",
				Fix: "run nrp_setup with sign_in=true, or `kubectl auth whoami` in a terminal"})
		} else {
			args := []string{"--context", e.ctxName(), "auth", "whoami", "-o", "jsonpath={.status.userInfo.username}"}
			if e.Kubeconfig != "" {
				args = append([]string{"--kubeconfig", e.Kubeconfig}, args...)
			}
			cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			out, err := e.Run(cctx, kubectl, args...)
			cancel()
			if err != nil {
				add(Check{ID: "signin", Title: "Signed in to Nautilus", Status: Problem, Detail: firstLine(err.Error()),
					Fix: "finish the CILogon sign-in in the browser window; first time only, accept the NRP policy at https://nrp.ai"})
			} else {
				add(Check{ID: "signin", Title: "Signed in to Nautilus", Status: OK, Detail: strings.TrimSpace(string(out))})
			}
		}
	}

	rep.Ready = allOK(rep.Checks)
	var todo []string
	var notes []string
	for _, c := range rep.Checks {
		switch c.Status {
		case OK:
		case Note:
			notes = append(notes, c.ID)
		default:
			todo = append(todo, c.ID)
		}
	}
	switch {
	case rep.Ready:
		rep.Summary = "This computer is ready for Nautilus."
		if len(notes) > 0 {
			rep.Summary += " Notes: " + strings.Join(notes, ", ") + "."
		}
		rep.Next = "Call nrp_status to see your namespaces."
	default:
		rep.Summary = fmt.Sprintf("Not ready yet: %s.", strings.Join(todo, ", "))
		rep.Next = "Call nrp_setup with fix=true to install or update what it can (it downloads official releases and checks their SHA256), then follow the manual steps."
	}
	return rep
}

func allOK(cs []Check) bool {
	for _, c := range cs {
		if c.Status != OK && c.Status != Note {
			return false
		}
	}
	return true
}

// onUserPath reports whether dir is on the PATH the user's own shell starts with
// (nrp-mcp adds BinDir to its own process PATH, which the user's terminal does not see).
func onUserPath(dir string) bool {
	for _, d := range filepath.SplitList(origPath) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

var origPath = os.Getenv("PATH")

func pathHint(e *Env) string {
	if e.GOOS == "windows" {
		return `setx PATH "%PATH%;` + e.BinDir + `"  (then open a new terminal)`
	}
	return `echo 'export PATH="` + e.BinDir + `:$PATH"' >> ~/.bashrc   (zsh: ~/.zshrc), then open a new terminal`
}

func isNRPConfig(b []byte) bool {
	s := string(b)
	return strings.Contains(s, NRPServerHint) || strings.Contains(s, "67.58.53.148") || (strings.Contains(s, "name: nautilus") && strings.Contains(s, "oidc-login"))
}

func (e *Env) findDownloadedConfig() string {
	var best string
	var bestT time.Time
	for _, d := range e.Downloads {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, en := range ents {
			n := strings.ToLower(en.Name())
			if en.IsDir() || !(strings.HasPrefix(n, "config") || strings.Contains(n, "kube")) {
				continue
			}
			info, err := en.Info()
			if err != nil || info.Size() > 1<<20 {
				continue
			}
			p := filepath.Join(d, en.Name())
			b, err := os.ReadFile(p)
			if err != nil || !isNRPConfig(b) || !strings.Contains(string(b), "apiVersion") {
				continue
			}
			if info.ModTime().After(bestT) {
				best, bestT = p, info.ModTime()
			}
		}
	}
	return best
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// Fix installs or updates what it can, then re-inspects. Order matters: the NRP config
// goes in first so the kubectl version can match the cluster; a second pass picks up
// anything the first one made fixable (for example a version mismatch that only shows
// once the config is in place).
func (e *Env) Fix(ctx context.Context) (*Report, error) {
	var actions []string
	if err := os.MkdirAll(e.BinDir, 0o755); err != nil {
		return nil, err
	}
	done := map[string]bool{}
	for pass := 0; pass < 2; pass++ {
		rep := e.Inspect(ctx, false)
		byID := map[string]Check{}
		for _, c := range rep.Checks {
			byID[c.ID] = c
		}
		for _, id := range []string{"kubelogin", "config", "kubectl"} {
			c, ok := byID[id]
			if !ok || c.Status == OK || !c.Auto || done[id+c.Status] {
				continue
			}
			var msg string
			var err error
			switch id {
			case "kubectl":
				msg, err = e.installKubectl(ctx)
			case "kubelogin":
				msg, err = e.installKubelogin(ctx)
			case "config":
				msg, err = e.placeConfig()
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w (nothing else was changed)", id, err)
			}
			done[id+c.Status] = true
			actions = append(actions, msg)
		}
		// Make the new binaries visible to this process (and kubectl's plugin lookup).
		if !e.inPath(e.BinDir) {
			e.PathEnv = e.PathEnv + string(os.PathListSeparator) + e.BinDir
			_ = os.Setenv("PATH", e.PathEnv)
		}
	}
	after := e.Inspect(ctx, false)
	if !onUserPath(e.BinDir) {
		after.PathHint = pathHint(e)
	}
	after.Actions = actions
	if len(actions) == 0 {
		after.Actions = []string{"nothing to install automatically"}
	}
	return after, nil
}

// kubectlVersion picks the kubectl to install: the cluster's minor line if known, else
// the latest stable.
func (e *Env) kubectlVersion(ctx context.Context) (string, error) {
	url := KubectlBase + "/stable.txt"
	if _, sv := e.serverInfo(ctx, ""); sv != "" {
		if a, b := minor(sv); a > 0 {
			url = fmt.Sprintf("%s/stable-%d.%d.txt", KubectlBase, a, b)
		}
	}
	b, err := e.HTTP(ctx, url)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if !verRe.MatchString(v) {
		return "", fmt.Errorf("unexpected version %q from %s", v, url)
	}
	return v, nil
}

func (e *Env) installKubectl(ctx context.Context) (string, error) {
	v, err := e.kubectlVersion(ctx)
	if err != nil {
		return "", err
	}
	name := e.exe("kubectl")
	url := fmt.Sprintf("%s/%s/bin/%s/%s/%s", KubectlBase, v, e.GOOS, e.GOARCH, name)
	bin, err := e.HTTP(ctx, url)
	if err != nil {
		return "", err
	}
	sumFile, err := e.HTTP(ctx, url+".sha256")
	if err != nil {
		return "", err
	}
	if err := verify(bin, sumFile); err != nil {
		return "", fmt.Errorf("kubectl %s: %w", v, err)
	}
	dst := filepath.Join(e.BinDir, name)
	bk, err := install(dst, bin, e.Now())
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("installed kubectl %s to %s (SHA256 verified)", v, dst)
	if bk != "" {
		msg += "; previous copy kept at " + bk
	}
	return msg, nil
}

func (e *Env) installKubelogin(ctx context.Context) (string, error) {
	b, err := e.HTTP(ctx, KubeloginAPI)
	if err != nil {
		return "", err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &rel); err != nil || !verRe.MatchString(rel.Tag) {
		return "", fmt.Errorf("could not read the latest kubelogin release")
	}
	asset := fmt.Sprintf("kubelogin_%s_%s.zip", e.GOOS, e.GOARCH)
	url := fmt.Sprintf("%s/%s/%s", KubeloginBase, rel.Tag, asset)
	z, err := e.HTTP(ctx, url)
	if err != nil {
		return "", err
	}
	sumFile, err := e.HTTP(ctx, url+".sha256")
	if err != nil {
		return "", err
	}
	if err := verify(z, sumFile); err != nil {
		return "", fmt.Errorf("kubelogin %s: %w", rel.Tag, err)
	}
	bin, err := unzipOne(z, e.exe("kubelogin"))
	if err != nil {
		return "", err
	}
	dst := filepath.Join(e.BinDir, e.exe("kubectl-oidc_login"))
	bk, err := install(dst, bin, e.Now())
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("installed kubelogin %s to %s (SHA256 verified)", rel.Tag, dst)
	if bk != "" {
		msg += "; previous copy kept at " + bk
	}
	return msg, nil
}

func (e *Env) placeConfig() (string, error) {
	src := e.findDownloadedConfig()
	if src == "" {
		return "", errors.New("no downloaded NRP config found; get it from " + NRPConfigURL)
	}
	nb, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	dst := e.kubeconfigPath()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	old, err := os.ReadFile(dst)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(dst, nb, 0o600); err != nil {
			return "", err
		}
		return fmt.Sprintf("copied the NRP config from %s to %s", src, dst), nil
	}
	if err != nil {
		return "", err
	}
	if isNRPConfig(old) {
		return "config already has Nautilus; left unchanged", nil
	}
	// An existing config for another cluster: back it up and merge with kubectl.
	bk := fmt.Sprintf("%s.bak-nrp-%s", dst, e.Now().Format("20060102-150405"))
	if err := os.WriteFile(bk, old, 0o600); err != nil {
		return "", err
	}
	kubectl := e.findKubectl()
	if kubectl == "" {
		return "", errors.New("kubectl is needed to merge configs; install it first")
	}
	sep := string(os.PathListSeparator)
	cmd := exec.Command(kubectl, "config", "view", "--flatten")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+dst+sep+src)
	merged, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("merge failed (your config is unchanged, backup at %s): %w", bk, err)
	}
	if err := os.WriteFile(dst, merged, 0o600); err != nil {
		return "", err
	}
	return fmt.Sprintf("merged the NRP config from %s into %s; your previous config is backed up at %s (current context unchanged)", src, dst, bk), nil
}

// verify checks data against a published .sha256 file ("<hex>" or "<hex>  name").
func verify(data, sumFile []byte) error {
	f := strings.Fields(string(sumFile))
	if len(f) == 0 || len(f[0]) != 64 {
		return errors.New("checksum file is not a SHA256")
	}
	want := strings.ToLower(f[0])
	h := sha256.Sum256(data)
	if got := hex.EncodeToString(h[:]); got != want {
		return fmt.Errorf("SHA256 mismatch (got %s, published %s); not installed", got[:12], want[:12])
	}
	return nil
}

func unzipOne(z []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if filepath.Base(f.Name) == name && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxDownload))
		}
	}
	return nil, fmt.Errorf("%s not found in the archive", name)
}

// install writes bin to dst atomically (temp file + rename), keeping any existing file as
// a dated backup. Returns the backup path.
func install(dst string, bin []byte, now time.Time) (string, error) {
	tmp := dst + ".nrp-tmp"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", err
	}
	bk := ""
	if _, err := os.Stat(dst); err == nil {
		bk = fmt.Sprintf("%s.bak-%s", dst, now.Format("20060102-150405"))
		if err := os.Rename(dst, bk); err != nil {
			os.Remove(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return bk, nil
}
