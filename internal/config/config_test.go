package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults090(t *testing.T) {
	t.Setenv("NRP_CONSENT", "")
	c := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if c.Consent != "ask" || c.WebDays != 14 {
		t.Fatalf("consent %q web_days %d", c.Consent, c.WebDays)
	}
	if c.S3Endpoint != "https://s3-west.nrp-nautilus.io" || c.S3Internal != "http://rook-ceph-rgw-nautiluss3.rook" {
		t.Fatalf("s3 endpoints %q %q", c.S3Endpoint, c.S3Internal)
	}
	if filepath.Base(c.S3KeysFile) != "s3.env" {
		t.Fatalf("keys file %q", c.S3KeysFile)
	}
}

func TestConsentAndWebDaysFromFile(t *testing.T) {
	t.Setenv("NRP_CONSENT", "")
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "consent: token\nweb_days: 3\ns3_bucket: lab-results\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Load(p)
	if c.Consent != "token" || c.WebDays != 3 || c.S3Bucket != "lab-results" {
		t.Fatalf("%+v", c)
	}
}

func TestBadValuesFallBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("consent: maybe\nweb_days: 90\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NRP_CONSENT", "")
	c := Load(p)
	if c.Consent != "ask" || c.WebDays != 14 {
		t.Fatalf("consent %q web_days %d", c.Consent, c.WebDays)
	}
	t.Setenv("NRP_CONSENT", "token")
	if c := Load(p); c.Consent != "token" {
		t.Fatalf("env override: %q", c.Consent)
	}
}

func TestGPUSessionHoursConfig(t *testing.T) {
	c := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if c.GPUSessionHours != 6 {
		t.Fatalf("default gpu_session_hours %d, want 6", c.GPUSessionHours)
	}
	if c.NamespaceGPUs != 8 {
		t.Fatalf("default namespace_gpus %d, want 8", c.NamespaceGPUs)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(p, []byte("gpu_session_hours: 10\nnamespace_gpus: 12\n"), 0o600)
	c = Load(p)
	if c.GPUSessionHours != 10 || c.NamespaceGPUs != 12 {
		t.Fatalf("got hours=%d namespace_gpus=%d, want 10 and 12", c.GPUSessionHours, c.NamespaceGPUs)
	}
	_ = os.WriteFile(p, []byte("gpu_session_hours: 20\nnamespace_gpus: 0\n"), 0o600)
	c = Load(p)
	if c.GPUSessionHours != 12 || c.NamespaceGPUs != 8 {
		t.Fatalf("got hours=%d namespace_gpus=%d, want 12 and 8", c.GPUSessionHours, c.NamespaceGPUs)
	}
}
