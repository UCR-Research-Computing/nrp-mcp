package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/client"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/config"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/results"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/version"
)

func TestRunKubectlPassthrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub test runs on unix")
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeKubectl := filepath.Join(binDir, "kubectl")
	script := "#!/bin/sh\necho \"KUBECTL_ARGS:$*\"\necho \"PATH_HAS_BIN:$(echo \"$PATH\" | grep -F -c \"" + binDir + "\")\"\n"
	if err := os.WriteFile(fakeKubectl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")

	cfg := config.Config{Kubectl: "kubectl", Kubeconfig: "/tmp/nrp-kc", Context: "nautilus", Namespace: "ucr-test"}

	// Capture stdout from runKubectl.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut := os.Stdout
	os.Stdout = w
	code := runKubectl(cfg, []string{"auth", "whoami"})
	_ = w.Close()
	os.Stdout = oldOut
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if code != 0 {
		t.Fatalf("runKubectl returned %d, out=%s", code, out)
	}
	want := "KUBECTL_ARGS:--kubeconfig /tmp/nrp-kc --context nautilus -n ucr-test auth whoami"
	if !strings.Contains(out, want) {
		t.Fatalf("got %q, want %q", out, want)
	}
	if !strings.Contains(out, "PATH_HAS_BIN:1") {
		t.Fatalf("expected user bin dir on PATH, got %q", out)
	}

	// Explicit --context and -n from caller must not be duplicated.
	r2, w2, _ := os.Pipe()
	os.Stdout = w2
	code = runKubectl(cfg, []string{"--context", "other", "-n", "other-ns", "get", "pods"})
	_ = w2.Close()
	os.Stdout = oldOut
	n2, _ := r2.Read(buf)
	out2 := string(buf[:n2])
	if code != 0 || !strings.Contains(out2, "KUBECTL_ARGS:--kubeconfig /tmp/nrp-kc --context other -n other-ns get pods") {
		t.Fatalf("explicit flags not respected: code=%d out=%q", code, out2)
	}
}

func TestInstallShZshAndBashAndChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for Linux and macOS")
	}
	osName := runtime.GOOS
	arch := runtime.GOARCH
	ver := "v0.10.0"
	name := "nrp-mcp_0.10.0_" + osName + "_" + arch

	dist := t.TempDir()
	pkgDir := filepath.Join(dist, name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBin := []byte("#!/bin/sh\necho \"nrp-mcp 0.10.0\"\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "nrp-mcp"), fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(dist, name+".tar.gz")
	if out, err := exec.Command("tar", "-C", dist, "-czf", tarPath, name).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, out)
	}
	tb, err := os.ReadFile(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(tb)
	sumHex := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(sumHex+"  "+name+".tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scriptPath, err := filepath.Abs("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}

	// 1. zsh (macOS default): writes ~/.zprofile when NRP_MCP_YES=1 and prints full binary path.
	homeZsh := t.TempDir()
	cmd := exec.Command("sh", scriptPath)
	cmd.Env = []string{
		"HOME=" + homeZsh,
		"SHELL=/bin/zsh",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"NRP_MCP_VERSION=" + ver,
		"NRP_MCP_BASE=file://" + dist,
		"NRP_MCP_YES=1",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh (zsh): %v\n%s", err, out)
	}
	wantBin := filepath.Join(homeZsh, ".local", "bin", "nrp-mcp")
	if !strings.Contains(string(out), "Binary path: "+wantBin) || !strings.Contains(string(out), wantBin+" setup") {
		t.Fatalf("output missing full binary path %s:\n%s", wantBin, out)
	}
	zp, err := os.ReadFile(filepath.Join(homeZsh, ".zprofile"))
	if err != nil || !strings.Contains(string(zp), filepath.Join(homeZsh, ".local", "bin")) {
		t.Fatalf(".zprofile not updated: err=%v body=%q", err, zp)
	}

	// 2. bash without approval (NRP_MCP_YES=no): does not create ~/.bashrc, still installs and prints full path.
	homeBash := t.TempDir()
	cmd2 := exec.Command("sh", scriptPath)
	cmd2.Env = []string{
		"HOME=" + homeBash,
		"SHELL=/bin/bash",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"NRP_MCP_VERSION=" + ver,
		"NRP_MCP_BASE=file://" + dist,
		"NRP_MCP_YES=no",
	}
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh (bash no): %v\n%s", err, out2)
	}
	if _, err := os.Stat(filepath.Join(homeBash, ".bashrc")); err == nil {
		t.Fatal(".bashrc must not be written when user did not approve")
	}
	if !strings.Contains(string(out2), filepath.Join(homeBash, ".bashrc")) {
		t.Fatalf("expected .bashrc hint in output:\n%s", out2)
	}

	// 3. Bad checksum must fail and not install.
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  "+name+".tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	homeBad := t.TempDir()
	cmd3 := exec.Command("sh", scriptPath)
	cmd3.Env = []string{
		"HOME=" + homeBad,
		"SHELL=/bin/zsh",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"NRP_MCP_VERSION=" + ver,
		"NRP_MCP_BASE=file://" + dist,
		"NRP_MCP_YES=1",
	}
	if out3, err := cmd3.CombinedOutput(); err == nil || !strings.Contains(string(out3), "SHA256 mismatch") {
		t.Fatalf("expected SHA256 mismatch failure, got err=%v out=%s", err, out3)
	}
	if _, err := os.Stat(filepath.Join(homeBad, ".local", "bin", "nrp-mcp")); err == nil {
		t.Fatal("tampered archive was installed")
	}
}

func TestInstallPs1Structure(t *testing.T) {
	b, err := os.ReadFile("../../scripts/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`Programs\nrp-mcp`,
		`[Environment]::SetEnvironmentVariable("Path"`,
		`"User"`,
		`Unblock-File`,
		`Get-FileHash`,
		`SHA256SUMS`,
		`Binary path:`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install.ps1 missing %q", want)
		}
	}
	// Strip comments and check that setx is never invoked.
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(strings.ToLower(trimmed), "setx") {
			t.Fatalf("install.ps1 must never use setx: %s", line)
		}
	}
}

func TestRunClientCLIOutputAndBackup(t *testing.T) {
	home := t.TempDir()
	env := &client.Env{
		GOOS:    "darwin",
		Home:    home,
		BinPath: "/opt/nrp/bin/nrp-mcp",
	}
	wantRestart := map[string]string{
		"claude-desktop": "restart Claude Desktop",
		"claude-code":    "restart Claude Code",
		"vscode":         "restart VS Code",
		"cursor":         "restart Cursor",
		"gemini":         "restart Gemini CLI",
		"opencode":       "restart OpenCode",
		"hermes":         "restart Hermes Agent",
	}

	oldOut := os.Stdout
	defer func() { os.Stdout = oldOut }()

	for _, name := range client.All {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		code := runClientWithEnv(env, []string{name})
		_ = w.Close()
		os.Stdout = oldOut
		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		out := string(buf[:n])
		if code != 0 {
			t.Fatalf("runClientWithEnv(%s) code=%d out=%s", name, code, out)
		}
		if !strings.Contains(out, wantRestart[name]) {
			t.Fatalf("output for %s missing %q: %s", name, wantRestart[name], out)
		}
		if !strings.Contains(out, `["serve"]`) {
			t.Fatalf("output for %s missing [\"serve\"]: %s", name, out)
		}
		// Run a second time to verify .bak is created and mentioned.
		r2, w2, _ := os.Pipe()
		os.Stdout = w2
		code2 := runClientWithEnv(env, []string{name})
		_ = w2.Close()
		os.Stdout = oldOut
		n2, _ := r2.Read(buf)
		out2 := string(buf[:n2])
		if code2 != 0 || !strings.Contains(out2, ".bak") {
			t.Fatalf("second run for %s did not report .bak: code=%d out=%s", name, code2, out2)
		}
	}
}

func captureStdout(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := fn()
	_ = w.Close()
	os.Stdout = old
	buf := make([]byte, 16384)
	n, _ := r.Read(buf)
	return code, string(buf[:n])
}

func TestDoctorZeroNamespacesMultipleKubeconfigsS3ConsentAndRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub test runs on unix")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))

	// KUBECONFIG with two files: first is another cluster, second is NRP.
	kcOther := filepath.Join(home, "other-kubeconfig")
	kcNRP := filepath.Join(home, "nrp-kubeconfig")
	if err := os.WriteFile(kcOther, []byte("apiVersion: v1\nclusters:\n- name: kind\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kcNRP, []byte("apiVersion: v1\nclusters:\n- cluster:\n    server: https://67.58.53.148:443\n  name: nautilus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	multiKC := kcOther + string(os.PathListSeparator) + kcNRP

	// Fake kubectl that returns 0 namespaces on first test, then 1 namespace on second test.
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeKubectl := filepath.Join(binDir, "kubectl")
	writeKubectl := func(groups string) {
		script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in\n  *version*) echo '{\"clientVersion\":{\"gitVersion\":\"v1.34.1\"}}' ;;\n  *whoami*) echo '{\"status\":{\"userInfo\":{\"username\":\"https://cilogon.org/serverA/users/9\",\"groups\":%s}}}' ;;\n  *resourcequota*) echo '{\"items\":[]}' ;;\n  *) echo '{}' ;;\nesac\n", groups)
		if err := os.WriteFile(fakeKubectl, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Save S3 keys with distinctive secret value that must NEVER appear in doctor output.
	s3File := filepath.Join(home, "s3.env")
	secretVal := "SUPER_SECRET_S3_VALUE_NEVER_PRINT_999"
	if err := results.SaveKeys(s3File, results.Keys{Access: "MY_ACCESS_KEY_888", Secret: secretVal}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Kubectl:    fakeKubectl,
		Kubeconfig: multiKC,
		Context:    "nautilus",
		Consent:    "code",
		S3KeysFile: s3File,
	}

	// 1. Zero namespaces -> exit 1 (not ready), every FAIL line has "-> ..." with who to ask or command to run.
	writeKubectl(`["system:authenticated"]`)
	vcNewer := &version.Checker{
		Current: "0.10.0",
		HTTP: func(context.Context, string) ([]byte, error) {
			return []byte(`{"tag_name":"v1.1.0"}`), nil
		},
	}
	code, out := captureStdout(t, func() int {
		return runDoctorWith(cfg, vcNewer)
	})
	if code == 0 {
		t.Fatalf("expected non-zero exit when user has 0 namespaces, got 0:\n%s", out)
	}
	if !strings.Contains(out, "FAIL namespaces") || !strings.Contains(out, "https://nrp.ai/namespaces") {
		t.Fatalf("expected FAIL namespaces with https://nrp.ai/namespaces hint:\n%s", out)
	}
	if !strings.Contains(out, "ok   kubeconfig") {
		t.Fatalf("expected multi-file KUBECONFIG to pass:\n%s", out)
	}
	if !strings.Contains(out, "ok   consent      code") {
		t.Fatalf("expected consent mode in doctor output:\n%s", out)
	}
	if !strings.Contains(out, "ok   s3-keys      yes") {
		t.Fatalf("expected s3-keys yes in doctor output:\n%s", out)
	}
	if strings.Contains(out, secretVal) || strings.Contains(out, "MY_ACCESS_KEY_888") {
		t.Fatalf("doctor leaked S3 key values:\n%s", out)
	}
	if !strings.Contains(out, "newest is v1.1.0") {
		t.Fatalf("expected newest release v1.1.0 in doctor output:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "  FAIL ") && !strings.Contains(line, " -> ") {
			t.Fatalf("FAIL line missing ' -> <next command or person to ask>': %s", line)
		}
	}

	// 2. With 1 namespace and offline release check -> exit 0, release check skipped offline, s3-keys no when missing.
	writeKubectl(`["oidcgroup:ucr-research-computing:user"]`)
	cfg.S3KeysFile = filepath.Join(home, "missing-s3.env")
	vcOffline := &version.Checker{
		Current: "0.10.0",
		HTTP: func(context.Context, string) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	}
	code2, out2 := captureStdout(t, func() int {
		return runDoctorWith(cfg, vcOffline)
	})
	if code2 != 0 {
		t.Fatalf("expected exit 0 with namespace, got %d:\n%s", code2, out2)
	}
	if !strings.Contains(out2, "ok   s3-keys      no") {
		t.Fatalf("expected s3-keys no when file missing:\n%s", out2)
	}
	if !strings.Contains(out2, "skipped offline") {
		t.Fatalf("expected offline release check note:\n%s", out2)
	}

	// 3. Missing kubeconfig and failing sign-in -> every FAIL line ends with " -> ...".
	cfgFail := config.Config{
		Kubectl:    fakeKubectl,
		Kubeconfig: filepath.Join(home, "does-not-exist"),
		Context:    "nautilus",
		Consent:    "ask",
		S3KeysFile: filepath.Join(home, "missing-s3.env"),
	}
	if err := os.WriteFile(fakeKubectl, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code3, out3 := captureStdout(t, func() int {
		return runDoctorWith(cfgFail, vcOffline)
	})
	if code3 == 0 {
		t.Fatalf("expected non-zero exit when kubeconfig and sign-in fail:\n%s", out3)
	}
	failCount := 0
	for _, line := range strings.Split(out3, "\n") {
		if strings.Contains(line, "  FAIL ") {
			failCount++
			if !strings.Contains(line, " -> ") {
				t.Fatalf("FAIL line missing ' -> ': %s", line)
			}
		}
	}
	if failCount < 2 {
		t.Fatalf("expected at least 2 FAIL lines, got %d:\n%s", failCount, out3)
	}
}

func TestVersionCLIReportsNewerRelease(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	vc := &version.Checker{
		Current:  "0.10.0",
		CacheDir: t.TempDir(),
		Now:      func() time.Time { return now },
		HTTP: func(context.Context, string) ([]byte, error) {
			return []byte(`{"tag_name":"v1.2.0"}`), nil
		},
	}
	code, out := captureStdout(t, func() int {
		return runVersionWithChecker(vc)
	})
	if code != 0 || !strings.Contains(out, "nrp-mcp 0.10.0") || !strings.Contains(out, "v1.2.0") {
		t.Fatalf("runVersionWithChecker: code=%d out=%q", code, out)
	}
}

func TestWorkflowsPinCommitSHAsAndAttestProvenance(t *testing.T) {
	usesRe := regexp.MustCompile(`^\s*-?\s*uses:\s*([^\s#]+)`)
	shaPinRe := regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+@[0-9a-f]{40}$`)

	for _, wf := range []string{"ci.yml", "release.yml", "setup-e2e.yml"} {
		p := filepath.Join("../../.github/workflows", wf)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			m := usesRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if !shaPinRe.MatchString(m[1]) {
				t.Errorf("%s:%d action %q is not pinned to a 40-char commit SHA", wf, i+1, m[1])
			}
		}
	}

	ciBytes, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ciBytes), "permissions:\n  contents: read") {
		t.Fatalf("ci.yml missing permissions: contents: read:\n%s", ciBytes)
	}

	relBytes, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	relStr := string(relBytes)
	for _, want := range []string{
		"actions/attest-build-provenance@",
		"attestations: write",
		"id-token: write",
		"SHA256SUMS",
	} {
		if !strings.Contains(relStr, want) {
			t.Errorf("release.yml missing %q", want)
		}
	}
}

func TestStartHereJargonAndExamples10(t *testing.T) {
	startBytes, err := os.ReadFile("../../docs/START-HERE.md")
	if err != nil {
		t.Fatal(err)
	}
	startStr := string(startBytes)
	for _, want := range []string{
		"https://nrp.ai/namespaces",
		"darwin_arm64",
		"darwin_amd64",
		"windows_amd64",
		"windows_arm64",
		"linux_amd64",
		"linux_arm64",
		"com.apple.quarantine",
		"SmartScreen",
		"Unblock-File",
		"nrp-mcp client",
		"nrp-mcp approve",
		"nrp-mcp s3 setup",
		"https://nrp.ai/s3token/",
		"discard_results=true",
		"show_manifests=true",
		"nrp_cleanup",
		"14 days",
		"| **pod** |",
		"| **namespace** |",
		"| **volume**",
		"| **public URL**",
		"| **P1**",
	} {
		if !strings.Contains(startStr, want) {
			t.Errorf("docs/START-HERE.md missing %q", want)
		}
	}

	readmeBytes, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readmeLines := strings.Split(string(readmeBytes), "\n")
	if len(readmeLines) < 20 {
		t.Fatal("README.md unexpectedly short")
	}
	top := strings.Join(readmeLines[:20], "\n")
	if !strings.Contains(top, "docs/START-HERE.md") {
		t.Errorf("README.md top 20 lines do not link docs/START-HERE.md:\n%s", top)
	}

	entries, err := filepath.Glob("../../docs/examples/[0-9][0-9]-*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Fatalf("expected 20 numbered docs/examples files, got %d", len(entries))
	}
	for _, p := range entries {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, stale := range []string{
			"type the exact URL back",
			"type the URL back",
			"typed-back URL",
			"has not yet tested S3",
			"is not yet tested by UCR RC",
			"untested by UCR RC",
			"have not been tested by UCR RC",
		} {
			if strings.Contains(s, stale) {
				t.Errorf("%s still contains pre-1.0 text %q", filepath.Base(p), stale)
			}
		}
		for _, want := range []string{
			"approval_code",
			"discard_results=true",
			"show_manifests=true",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s missing 1.0 behavior %q", filepath.Base(p), want)
			}
		}
	}
}
