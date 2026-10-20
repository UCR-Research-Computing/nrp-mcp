package results

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/k8s"
)

func TestKeysFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s3.env")
	if err := SaveKeys(p, Keys{Access: "AK", Secret: "SK"}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	k, err := LoadKeys(p)
	if err != nil || k.Access != "AK" || k.Secret != "SK" {
		t.Fatalf("%v %+v", err, k)
	}
	_ = os.Chmod(p, 0o644)
	if _, err := LoadKeys(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("world-readable keys accepted: %v", err)
	}
	if _, err := LoadKeys(filepath.Join(t.TempDir(), "none")); err == nil || !strings.Contains(err.Error(), "nrp-mcp s3 setup") {
		t.Fatalf("missing keys: %v", err)
	}
}

func TestDownloadStaysInsideAndNeverOverwrites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(403)
			return
		}
		if r.URL.Query().Get("list-type") == "2" {
			fmt.Fprint(w, `<ListBucketResult><Contents><Key>runs/r1/out.txt</Key><Size>2</Size></Contents><Contents><Key>runs/r1/task-0/a.csv</Key><Size>2</Size></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Bucket: "b", Keys: Keys{"AK", "SK"}}
	dir := filepath.Join(t.TempDir(), "results", "r1")
	n, _, err := c.Download(context.Background(), "runs/r1/", dir)
	if err != nil || n != 2 {
		t.Fatalf("download: %d %v", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "task-0", "a.csv")); string(b) != "ok" {
		t.Fatalf("file %q", b)
	}
	// A second download (after an interruption) skips files that are already there.
	if n, _, err := c.Download(context.Background(), "runs/r1/", dir); err != nil || n != 0 {
		t.Fatalf("resume: %d %v", n, err)
	}
	// A local file with different content is never overwritten.
	_ = os.WriteFile(filepath.Join(dir, "out.txt"), []byte("mine, edited"), 0o644)
	if _, _, err := c.Download(context.Background(), "runs/r1/", dir); err == nil || !strings.Contains(err.Error(), "not overwriting") {
		t.Fatalf("overwrote: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "mine, edited" {
		t.Fatalf("local file changed: %q", b)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(m) != 0 {
		t.Fatalf("partial files left: %v", m)
	}
}

func TestDownloadRefusesEscape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<ListBucketResult><Contents><Key>runs/r1/../../evil.sh</Key><Size>1</Size></Contents></ListBucketResult>`)
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Bucket: "b", Keys: Keys{"AK", "SK"}}
	if _, _, err := c.Download(context.Background(), "runs/r1/", t.TempDir()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("escape not refused: %v", err)
	}
}

func TestAttachUploader(t *testing.T) {
	// Default Python/curl images upload from the main container's shell at exit, with no sidecar image pull.
	ps := &k8s.PodSpec{Containers: []k8s.Container{{Name: "main", Image: "python:3.12-slim-bookworm", Command: []string{"bash", "-c", "set -e; cd /work; python run.py"}}}}
	AttachUploader(ps, "job-results")
	if len(ps.InitContainers) != 0 {
		t.Fatalf("inline upload must not add a sidecar initContainer: %+v", ps.InitContainers)
	}
	if !strings.Contains(ps.Containers[0].Command[2], "trap _nrp_up EXIT") || !strings.Contains(ps.Containers[0].Command[2], "mkdir -p /work/results; ") {
		t.Fatalf("main command %q", ps.Containers[0].Command[2])
	}
	if ps.TerminationGracePeriodSeconds == nil || *ps.TerminationGracePeriodSeconds != 300 {
		t.Fatal("grace period")
	}
	for _, e := range ps.Containers[0].Env {
		if e.Value != "" && strings.Contains(e.Value, "SK") {
			t.Fatal("secret in env literal")
		}
	}
	// Unknown custom images fall back to the native sidecar.
	ps2 := &k8s.PodSpec{Containers: []k8s.Container{{Name: "main", Image: "custom/tool:1.0", Command: []string{"bash", "-c", "set -e; cd /work; ./tool"}}}}
	AttachUploader(ps2, "job-results")
	if len(ps2.InitContainers) != 1 || ps2.InitContainers[0].RestartPolicy != "Always" || ps2.InitContainers[0].Image != UploaderImage {
		t.Fatalf("sidecar fallback %+v", ps2.InitContainers)
	}
}

func TestBucketName(t *testing.T) {
	if BucketName("0123456789abcdef") != "nrp-results-0123456789ab" {
		t.Fatal(BucketName("0123456789abcdef"))
	}
}

func TestUploaderShareComesOutOfMain(t *testing.T) {
	ps := &k8s.PodSpec{Containers: []k8s.Container{{Name: "main", Command: []string{"bash", "-c", "python run.py"},
		Resources: k8s.Resources{Requests: map[string]string{"cpu": "1", "memory": "2Gi"}, Limits: map[string]string{"cpu": "1", "memory": "2Gi"}}}}}
	AttachUploader(ps, "job-results")
	var cpu, mem int64
	for _, c := range append(append([]k8s.Container{}, ps.Containers...), ps.InitContainers...) {
		if c.RestartPolicy != "Always" && c.Name != "main" {
			continue
		}
		x, _ := k8s.ParseCPU(c.Resources.Requests["cpu"])
		y, _ := k8s.ParseBytes(c.Resources.Requests["memory"])
		cpu, mem = cpu+x, mem+y
	}
	if cpu != 1000 || mem != 2<<30 {
		t.Fatalf("pod total = %dm CPU, %d bytes; want 1000m and 2Gi (inside the NRP exemption)", cpu, mem)
	}
}

func TestUploaderScriptHandlesOddNamesAndFailures(t *testing.T) {
	for _, tool := range []string{"sh", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	var mu sync.Mutex
	got := map[string]string{}
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(400)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(400)
			return
		}
		b, _ := io.ReadAll(f)
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(403)
			return
		}
		got[r.FormValue("key")] = string(b)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	run := func() (string, error) {
		work := t.TempDir()
		if err := os.MkdirAll(filepath.Join(work, "results", "sub dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(work, "results", "data;filter=1,x.csv"), []byte("a,b\n"), 0o644)
		_ = os.WriteFile(filepath.Join(work, "results", "sub dir", "plot.png"), []byte("png"), 0o644)
		script := strings.ReplaceAll(uploaderScript, "/work", work)
		script = strings.Replace(script, "trap up TERM INT", "up", 1)
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "S3_URL="+srv.URL, "S3_PREFIX=runs/r1/", "S3_POLICY=p", "S3_ALG=a", "S3_CRED=c;x", "S3_DATE=d", "S3_SIG=s")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run()
	if err != nil || got["runs/r1/data;filter=1,x.csv"] != "a,b\n" || got["runs/r1/sub dir/plot.png"] != "png" {
		t.Fatalf("upload with odd names failed: err=%v got=%v out=%s", err, got, out)
	}
	mu.Lock()
	fail = true
	mu.Unlock()
	out, err = run()
	if err == nil || !strings.Contains(out, "FAILED") {
		t.Fatalf("a refused upload must be reported and exit non-zero: err=%v out=%s", err, out)
	}
}

func TestExitUploadBodyCurlAndPython(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(400)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(400)
			return
		}
		b, _ := io.ReadAll(f)
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(403)
			return
		}
		got[r.FormValue("key")] = string(b)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	runExit := func(pyOnly bool) (string, error) {
		work := t.TempDir()
		if err := os.MkdirAll(filepath.Join(work, "results", "sub dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(work, "results", "data;filter=1,x.csv"), []byte("a,b\n"), 0o644)
		_ = os.WriteFile(filepath.Join(work, "results", "sub dir", "plot.png"), []byte("png"), 0o644)
		body := strings.ReplaceAll(exitUploadBody, "/work", work)
		if pyOnly {
			body = strings.Replace(body, "if command -v curl >/dev/null 2>&1; then", "if false; then", 1)
		}
		cmd := exec.Command("bash", "-c", body+"echo job-done")
		env := []string{}
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "PYTHONPATH=") {
				env = append(env, kv)
			}
		}
		cmd.Env = append(env, "S3_URL="+srv.URL, "S3_PREFIX=runs/r1/", "S3_POLICY=p", "S3_ALG=a", "S3_CRED=c;x", "S3_DATE=d", "S3_SIG=s")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, pyOnly := range []bool{false, true} {
		if pyOnly {
			if _, err := exec.LookPath("python3"); err != nil {
				continue
			}
		}
		mu.Lock()
		got = map[string]string{}
		fail = false
		mu.Unlock()
		out, err := runExit(pyOnly)
		if err != nil || got["runs/r1/data;filter=1,x.csv"] != "a,b\n" || got["runs/r1/sub dir/plot.png"] != "png" {
			t.Fatalf("pyOnly=%v failed: err=%v got=%v out=%s", pyOnly, err, got, out)
		}
	}
}
