package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/config"
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
