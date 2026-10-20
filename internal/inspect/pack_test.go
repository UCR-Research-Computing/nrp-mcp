package inspect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packNames(p *Pack) string { return strings.Join(p.Names, ",") }

func skipped(p *Pack, rel string) string {
	for _, s := range p.Skipped {
		if s.Path == rel {
			return s.Reason
		}
	}
	return ""
}

func TestPackLeavesOutSecretsAndData(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "train.py", "print(1)\n")
	write(t, dir, "tokenizer.py", "def tok(): pass\n")
	write(t, dir, "id_rsa", "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n")
	write(t, dir, "credentials.json", "{}\n")
	write(t, dir, "service-account-key.json", "{}\n")
	write(t, dir, "config.yaml", "clusters: []\nusers:\n- user:\n    client-key-data: abc\n")
	write(t, dir, "settings.py", "AWS = 'AKIAABCDEFGHIJKLMNOP'\n")
	write(t, dir, "Data/raw.csv", "a,b\n")
	write(t, dir, "participants.csv", "name,dob\n")
	write(t, dir, "my notes.txt", "x\n")
	write(t, dir, ".env.local", "X=1\n")
	write(t, dir, "Results/out.txt", "x\n")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	p, err := PackDir(dir, PackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := packNames(p); got != "tokenizer.py,train.py" {
		t.Fatalf("packed %q; skipped %+v", got, p.Skipped)
	}
	for rel, want := range map[string]string{"id_rsa": "key", "credentials.json": "credential", "service-account-key.json": "credential",
		"config.yaml": "private key", "settings.py": "access token", "Data/": "folder", "participants.csv": "data_is_p1",
		"my notes.txt": "spaces", ".env.local": "key", "Results/": "folder", "link.txt": "link"} {
		if r := skipped(p, rel); !strings.Contains(r, want) {
			t.Errorf("%s: reason %q, want it to mention %q", rel, r, want)
		}
	}
	if len(p.NeedsP1) != 1 || p.NeedsP1[0] != "participants.csv" {
		t.Fatalf("needs_p1 %v", p.NeedsP1)
	}
}

func TestPackDataWithP1(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "run.py", "print(1)\n")
	write(t, dir, "params.csv", "a,b\n1,2\n")
	p, err := PackDir(dir, PackOptions{DataIsP1: true})
	if err != nil {
		t.Fatal(err)
	}
	if packNames(p) != "params.csv,run.py" || len(p.DataFiles) != 1 {
		t.Fatalf("%v %v", p.Names, p.DataFiles)
	}
}

func TestPackSingleFileOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "one.py", "print(1)\n")
	write(t, dir, "other.py", "print(2)\n")
	p, err := PackDir(dir, PackOptions{Only: "one.py"})
	if err != nil {
		t.Fatal(err)
	}
	if packNames(p) != "one.py" {
		t.Fatalf("single file packed %v", p.Names)
	}
}

func TestPackWebOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.html", "<html></html>\n")
	write(t, dir, "style.css", "body{}\n")
	write(t, dir, "build.py", "print(1)\n")
	write(t, dir, "table.csv", "a\n")
	p, err := PackDir(dir, PackOptions{WebOnly: true, DataIsP1: true})
	if err != nil {
		t.Fatal(err)
	}
	if packNames(p) != "index.html,style.css" {
		t.Fatalf("web packed %v", p.Names)
	}
	if !strings.Contains(skipped(p, "table.csv"), "never serves data") {
		t.Fatalf("csv reason %q", skipped(p, "table.csv"))
	}
}

func TestPackTooBig(t *testing.T) {
	dir := t.TempDir()
	chunk := strings.Repeat("x", 190*1024) + "\n"
	for _, n := range []string{"a.py", "b.py", "c.py", "d.py", "e.py"} {
		write(t, dir, n, chunk)
	}
	if _, err := PackDir(dir, PackOptions{}); err == nil || !strings.Contains(err.Error(), "nrp_build") {
		t.Fatalf("want too-big error, got %v", err)
	}
}

func TestPackKeepsNestedCodeFoldersNamedData(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "train.py", "from src.data import loader\n")
	write(t, dir, "src/data/loader.py", "def load(): pass\n")
	write(t, dir, "src/results/report.py", "def report(): pass\n")
	write(t, dir, "data/big.csv", "a,b\n")
	write(t, dir, "results/old.txt", "x\n")
	p, err := PackDir(dir, PackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	names := packNames(p)
	if !strings.Contains(names, "src/data/loader.py") || !strings.Contains(names, "src/results/report.py") {
		t.Fatalf("nested code folders named data/results must be packed: %s", names)
	}
	if strings.Contains(names, "big.csv") || strings.Contains(names, "old.txt") || skipped(p, "data/") == "" || skipped(p, "results/") == "" {
		t.Fatalf("top-level data/ and results/ must still be left out: %s %v", names, p.Skipped)
	}
}
