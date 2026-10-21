package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const nrpConfig = `apiVersion: v1
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
`

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func zipWith(name string, body []byte) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create(name)
	_, _ = f.Write(body)
	_ = w.Close()
	return buf.Bytes()
}

// fakeEnv: an empty home, fake downloads, and fake binaries that answer --version.
func fakeEnv(t *testing.T, tamper bool) (*Env, *[]string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	kubectlBin := []byte("#!/bin/sh\necho kubectl\n")
	klZip := zipWith("kubelogin", []byte("#!/bin/sh\necho kubelogin\n"))
	origSHA := KubeloginSHA256["linux/amd64"]
	KubeloginSHA256["linux/amd64"] = sum(klZip)
	t.Cleanup(func() { KubeloginSHA256["linux/amd64"] = origSHA })
	files := map[string][]byte{
		KubectlBase + "/stable.txt":                                       []byte("v1.37.1\n"),
		KubectlBase + "/v1.37.1/bin/linux/amd64/kubectl":                  kubectlBin,
		KubectlBase + "/v1.37.1/bin/linux/amd64/kubectl.sha256":           []byte(sum(kubectlBin)),
		KubeloginBase + "/" + KubeloginTag + "/kubelogin_linux_amd64.zip": klZip,
	}
	if tamper {
		files[KubectlBase+"/v1.37.1/bin/linux/amd64/kubectl"] = []byte("evil")
	}
	var fetched []string
	e := &Env{GOOS: "linux", GOARCH: "amd64", Home: home, BinDir: bin, PathEnv: "/usr/bin", Now: func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) },
		Downloads: []string{filepath.Join(home, "Downloads")}}
	e.HTTP = func(_ context.Context, url string) ([]byte, error) {
		fetched = append(fetched, url)
		if b, ok := files[url]; ok {
			return b, nil
		}
		return nil, fmt.Errorf("404 %s", url)
	}
	e.LookPath = func(name string) (string, error) { return "", errors.New("not found") }
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		j := strings.Join(args, " ")
		switch {
		case strings.HasSuffix(name, "kubectl") && strings.Contains(j, "version --client"):
			return []byte(`{"clientVersion":{"gitVersion":"v1.37.1"}}`), nil
		case strings.HasSuffix(name, "kubectl-oidc_login") && j == "--version":
			return []byte("kubelogin version v1.36.4"), nil
		case strings.Contains(j, "config view"):
			return nil, errors.New("no server in test")
		case strings.Contains(j, "auth whoami"):
			return []byte("http://cilogon.org/serverB/users/1"), nil
		}
		return nil, fmt.Errorf("unexpected %s %s", name, j)
	}
	return e, &fetched
}

func status(r *Report, id string) string {
	for _, c := range r.Checks {
		if c.ID == id {
			return c.Status
		}
	}
	return ""
}

func TestInspectEmptyLaptop(t *testing.T) {
	e, fetched := fakeEnv(t, false)
	r := e.Inspect(context.Background(), false)
	if r.Ready || status(r, "kubectl") != Missing || status(r, "kubelogin") != Missing || status(r, "config") != Manual {
		t.Fatalf("%+v", r.Checks)
	}
	if len(*fetched) != 0 {
		t.Fatalf("inspect must not download: %v", *fetched)
	}
	if _, err := os.Stat(e.BinDir); err == nil {
		t.Fatal("inspect must not create folders")
	}
}

func TestFixInstallsVerifiedBinaries(t *testing.T) {
	e, _ := fakeEnv(t, false)
	r, err := e.Fix(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"kubectl", "kubectl-oidc_login"} {
		info, err := os.Stat(filepath.Join(e.BinDir, f))
		if err != nil || info.Mode()&0o100 == 0 {
			t.Fatalf("%s not installed executable: %v", f, err)
		}
	}
	if status(r, "kubectl") != OK || status(r, "kubelogin") != OK {
		t.Fatalf("after fix: %+v", r.Checks)
	}
	if status(r, "config") != Manual {
		t.Fatalf("config cannot be fixed without a download: %+v", r.Checks)
	}
	if len(r.Actions) != 2 || !strings.Contains(r.Actions[0], "SHA256 verified") {
		t.Fatalf("actions %v", r.Actions)
	}
}

func TestFixRefusesBadChecksum(t *testing.T) {
	e, _ := fakeEnv(t, true)
	_, err := e.Fix(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("want mismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.BinDir, "kubectl")); err == nil {
		t.Fatal("tampered kubectl was installed")
	}
}

func TestUpdateKeepsBackup(t *testing.T) {
	e, _ := fakeEnv(t, false)
	_ = os.MkdirAll(e.BinDir, 0o755)
	old := filepath.Join(e.BinDir, "kubectl")
	_ = os.WriteFile(old, []byte("old kubectl"), 0o755)
	msg, err := e.installKubectl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "previous copy kept") {
		t.Fatal(msg)
	}
	b, _ := os.ReadFile(old + ".bak-20261006-120000")
	if string(b) != "old kubectl" {
		t.Fatalf("backup content %q", b)
	}
}

func TestPlacesDownloadedConfig(t *testing.T) {
	e, _ := fakeEnv(t, false)
	dl := filepath.Join(e.Home, "Downloads")
	_ = os.MkdirAll(dl, 0o755)
	_ = os.WriteFile(filepath.Join(dl, "config"), []byte(nrpConfig), 0o644)
	_ = os.WriteFile(filepath.Join(dl, "config-other"), []byte("apiVersion: v1\nclusters: []\n"), 0o644)
	r := e.Inspect(context.Background(), false)
	var c Check
	for _, x := range r.Checks {
		if x.ID == "config" {
			c = x
		}
	}
	if !c.Auto || !strings.Contains(c.Detail, filepath.Join(dl, "config")) {
		t.Fatalf("%+v", c)
	}
	msg, err := e.placeConfig()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(e.Home, ".kube", "config"))
	info, _ := os.Stat(filepath.Join(e.Home, ".kube", "config"))
	if string(b) != nrpConfig || info.Mode().Perm() != 0o600 {
		t.Fatalf("config not placed with 0600: %s %v", msg, info.Mode())
	}
	// A second run leaves an NRP config alone.
	msg, _ = e.placeConfig()
	if !strings.Contains(msg, "unchanged") {
		t.Fatal(msg)
	}
}

func TestReadyAndSignIn(t *testing.T) {
	e, _ := fakeEnv(t, false)
	_ = os.MkdirAll(e.BinDir, 0o755)
	for _, f := range []string{"kubectl", "kubectl-oidc_login"} {
		_ = os.WriteFile(filepath.Join(e.BinDir, f), []byte("x"), 0o755)
	}
	e.PathEnv = e.BinDir + ":/usr/bin"
	_ = os.MkdirAll(filepath.Join(e.Home, ".kube"), 0o700)
	_ = os.WriteFile(filepath.Join(e.Home, ".kube", "config"), []byte(nrpConfig), 0o600)
	r := e.Inspect(context.Background(), false)
	if r.Ready || status(r, "signin") != Manual {
		t.Fatalf("without sign_in the report must not claim ready: %+v", r.Checks)
	}
	r = e.Inspect(context.Background(), true)
	if !r.Ready || status(r, "signin") != OK {
		t.Fatalf("%+v", r.Checks)
	}
}

func TestVersionSkew(t *testing.T) {
	for _, c := range []struct {
		v          string
		maj, minor int
	}{{"v1.37.1", 1, 37}, {"kubectl v1.34.11", 1, 34}, {"junk", 0, 0}} {
		a, b := minor(c.v)
		if a != c.maj || b != c.minor {
			t.Errorf("%s -> %d.%d", c.v, a, b)
		}
	}
}

func TestVerify(t *testing.T) {
	d := []byte("hello")
	if err := verify(d, []byte(sum(d)+"  hello.zip\n")); err != nil {
		t.Fatal(err)
	}
	if err := verify(d, []byte("nothex")); err == nil {
		t.Fatal("accepted a bad checksum file")
	}
}

func TestFixMatchesClusterVersionOnFreshLaptop(t *testing.T) {
	e, fetched := fakeEnv(t, false)
	dl := filepath.Join(e.Home, "Downloads")
	_ = os.MkdirAll(dl, 0o755)
	_ = os.WriteFile(filepath.Join(dl, "config"), []byte(nrpConfig), 0o644)
	// Once a kubectl exists and the config is in place, the cluster reports 1.34.
	k134 := []byte("kubectl 1.34")
	e.cluster = func() string { return "v1.34.11" }
	oldHTTP := e.HTTP
	e.HTTP = func(ctx context.Context, url string) ([]byte, error) {
		switch url {
		case KubectlBase + "/stable-1.34.txt":
			return []byte("v1.34.12"), nil
		case KubectlBase + "/v1.34.12/bin/linux/amd64/kubectl":
			return k134, nil
		case KubectlBase + "/v1.34.12/bin/linux/amd64/kubectl.sha256":
			return []byte(sum(k134)), nil
		}
		return oldHTTP(ctx, url)
	}
	oldRun := e.Run
	e.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "version --client") {
			b, _ := os.ReadFile(name)
			if string(b) == "kubectl 1.34" {
				return []byte(`{"clientVersion":{"gitVersion":"v1.34.12"}}`), nil
			}
		}
		return oldRun(ctx, name, args...)
	}
	r, err := e.Fix(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(e.BinDir, "kubectl"))
	if string(b) != "kubectl 1.34" || status(r, "kubectl") != OK {
		t.Fatalf("kubectl not matched to the cluster: %q %+v %v", b, r.Checks, *fetched)
	}
	if !strings.Contains(strings.Join(r.Actions, "|"), "copied the NRP config") {
		t.Fatalf("actions %v", r.Actions)
	}
}

func TestClusterFromConfig(t *testing.T) {
	s, ca := clusterFromConfig(nrpConfig, "nautilus")
	if s != "https://67.58.53.148:443" || ca != "AAAA" {
		t.Fatalf("%q %q", s, ca)
	}
	multi := "apiVersion: v1\nclusters:\n- cluster:\n    server: https://other:6443\n  name: other\n- cluster:\n    certificate-authority-data: BBBB\n    server: https://67.58.53.148:443\n  name: nrp\ncontexts:\n- context:\n    cluster: nrp\n    user: oidc\n  name: nautilus\nkind: Config\n"
	s, ca = clusterFromConfig(multi, "nautilus")
	if s != "https://67.58.53.148:443" || ca != "BBBB" {
		t.Fatalf("multi: %q %q", s, ca)
	}
}

func TestNewerKubectlIsANoteNotABlocker(t *testing.T) {
	e, _ := fakeEnv(t, false)
	_ = os.MkdirAll(e.BinDir, 0o755)
	for _, f := range []string{"kubectl", "kubectl-oidc_login"} {
		_ = os.WriteFile(filepath.Join(e.BinDir, f), []byte("x"), 0o755)
	}
	_ = os.MkdirAll(filepath.Join(e.Home, ".kube"), 0o700)
	_ = os.WriteFile(filepath.Join(e.Home, ".kube", "config"), []byte(nrpConfig), 0o600)
	e.cluster = func() string { return "v1.34.11" } // fake kubectl reports v1.37.1
	r := e.Inspect(context.Background(), true)
	if status(r, "kubectl") != Note || status(r, "path") != Note || !r.Ready {
		t.Fatalf("%+v", r.Checks)
	}
	e.cluster = func() string { return "v1.40.0" } // kubectl 3 minors older: must update
	r = e.Inspect(context.Background(), true)
	if status(r, "kubectl") != Old || r.Ready {
		t.Fatalf("old kubectl should block: %+v", r.Checks)
	}
}

func TestKubeloginVersionFallbackAndBroken(t *testing.T) {
	e, _ := fakeEnv(t, false)
	_ = os.MkdirAll(e.BinDir, 0o755)
	_ = os.WriteFile(filepath.Join(e.BinDir, "kubectl-oidc_login"), []byte("x"), 0o755)
	base := e.Run
	// Windows behaviour: --version is rejected, the version subcommand works.
	e.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if strings.HasSuffix(name, "kubectl-oidc_login") {
			switch strings.Join(args, " ") {
			case "--version":
				return nil, errors.New("unknown flag: --version")
			case "version":
				return []byte("kubelogin version v1.36.4"), nil
			}
		}
		return base(ctx, name, args...)
	}
	r := e.Inspect(context.Background(), false)
	for _, c := range r.Checks {
		if c.ID == "kubelogin" && (c.Status != OK || !strings.Contains(c.Detail, "v1.36.4")) {
			t.Fatalf("fallback: %+v", c)
		}
	}
	// A binary that runs nothing is a problem, not ok.
	e.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if strings.HasSuffix(name, "kubectl-oidc_login") {
			return nil, errors.New("exec format error")
		}
		return base(ctx, name, args...)
	}
	if status(e.Inspect(context.Background(), false), "kubelogin") != Problem {
		t.Fatal("broken kubelogin reported ok")
	}
}

func TestKubeloginPinnedSHA256EmbeddedAtBuildTime(t *testing.T) {
	if !strings.HasPrefix(KubeloginTag, "v1.") {
		t.Fatalf("KubeloginTag = %q, want pinned v1.x.y", KubeloginTag)
	}
	for _, target := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"} {
		sha, ok := KubeloginSHA256[target]
		if !ok || len(sha) != 64 {
			t.Fatalf("missing or invalid embedded SHA256 for %s: %q", target, sha)
		}
	}
	e, fetched := fakeEnv(t, false)
	_ = os.MkdirAll(e.BinDir, 0o755)
	if _, err := e.installKubelogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, u := range *fetched {
		if strings.HasSuffix(u, ".sha256") || strings.Contains(u, "api.github.com") || strings.Contains(u, "/releases/latest") {
			t.Fatalf("installKubelogin must use embedded SHA256 and pinned tag without querying latest or .sha256: fetched %s", u)
		}
	}
	// Tampering with the kubelogin zip must fail against the embedded SHA256.
	eBad, _ := fakeEnv(t, false)
	KubeloginSHA256["linux/amd64"] = strings.Repeat("0", 64)
	if _, err := eBad.installKubelogin(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("expected SHA256 mismatch for tampered kubelogin, got %v", err)
	}
}

func TestPathHintsAndSignInFixAvoidBareKubectlAndSetx(t *testing.T) {
	win := &Env{GOOS: "windows", BinDir: `C:\Users\jane\AppData\Local\Programs\nrp-mcp\bin`}
	wh := pathHint(win)
	if strings.Contains(strings.ToLower(wh), "setx") || !strings.Contains(wh, `[Environment]::SetEnvironmentVariable("Path"`) {
		t.Fatalf("windows pathHint must use SetEnvironmentVariable, never setx: %s", wh)
	}
	mac := &Env{GOOS: "darwin", BinDir: "/Users/jane/.local/bin"}
	if mh := pathHint(mac); !strings.Contains(mh, "~/.zprofile") {
		t.Fatalf("darwin pathHint must default to ~/.zprofile: %s", mh)
	}
	lin := &Env{GOOS: "linux", BinDir: "/home/jane/.local/bin"}
	if lh := pathHint(lin); !strings.Contains(lh, "~/.bashrc") {
		t.Fatalf("linux pathHint must default to ~/.bashrc: %s", lh)
	}
	e, _ := fakeEnv(t, false)
	_ = os.MkdirAll(e.BinDir, 0o755)
	for _, f := range []string{"kubectl", "kubectl-oidc_login"} {
		_ = os.WriteFile(filepath.Join(e.BinDir, f), []byte("x"), 0o755)
	}
	_ = os.MkdirAll(filepath.Join(e.Home, ".kube"), 0o700)
	_ = os.WriteFile(filepath.Join(e.Home, ".kube", "config"), []byte(nrpConfig), 0o600)
	r := e.Inspect(context.Background(), false)
	for _, c := range r.Checks {
		if c.ID == "signin" {
			if !strings.Contains(c.Fix, filepath.Join(e.BinDir, "kubectl")) && !strings.Contains(c.Fix, "nrp-mcp kubectl") {
				t.Fatalf("signin fix must use full kubectl path or nrp-mcp kubectl: %q", c.Fix)
			}
		}
	}
}
