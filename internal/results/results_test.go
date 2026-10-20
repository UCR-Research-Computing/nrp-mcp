package results

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if _, _, err := c.Download(context.Background(), "runs/r1/", dir); err == nil || !strings.Contains(err.Error(), "not overwriting") {
		t.Fatalf("overwrote: %v", err)
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
	ps := &k8s.PodSpec{Containers: []k8s.Container{{Name: "main", Command: []string{"bash", "-c", "set -e; cd /work; python run.py"}}}}
	AttachUploader(ps, "job-results")
	if len(ps.InitContainers) != 1 || ps.InitContainers[0].RestartPolicy != "Always" || ps.InitContainers[0].Image != UploaderImage {
		t.Fatalf("sidecar %+v", ps.InitContainers)
	}
	if !strings.HasPrefix(ps.Containers[0].Command[2], "mkdir -p /work/results; ") {
		t.Fatalf("main command %q", ps.Containers[0].Command[2])
	}
	if ps.TerminationGracePeriodSeconds == nil || *ps.TerminationGracePeriodSeconds != 300 {
		t.Fatal("grace period")
	}
	for _, e := range ps.InitContainers[0].Env {
		if e.Value != "" && strings.Contains(e.Value, "SK") {
			t.Fatal("secret in env literal")
		}
	}
}

func TestBucketName(t *testing.T) {
	if BucketName("0123456789abcdef") != "nrp-results-0123456789ab" {
		t.Fatal(BucketName("0123456789abcdef"))
	}
}
