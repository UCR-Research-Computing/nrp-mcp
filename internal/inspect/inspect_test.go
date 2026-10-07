package inspect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyScriptIsEntry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "hello.py", "print('hi')\n")
	write(t, dir, "notes.md", "x\n")
	f, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.Entry != "hello.py" {
		t.Fatalf("entry %q (%s)", f.Entry, f.EntryReason)
	}
	write(t, dir, "other.py", "print('x')\n")
	f, _ = Dir(dir)
	if f.Entry != "" {
		t.Fatalf("two scripts should not pick one, got %q", f.Entry)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPyTorchProject(t *testing.T) {
	d := t.TempDir()
	write(t, d, "train.py", "import torch\nif __name__ == '__main__':\n    print(torch.cuda.is_available())\n")
	write(t, d, "requirements.txt", "torch==2.4\nnumpy\n# comment\n")
	write(t, d, ".env", "SECRET=1")
	write(t, d, "node_modules/x.js", "x")
	f, err := Dir(d)
	if err != nil {
		t.Fatal(err)
	}
	if !f.UsesGPU || f.Framework != "pytorch" || f.Entry != "train.py" {
		t.Fatalf("got %+v", f)
	}
	if len(f.Packages) != 2 || f.Packages[1] != "torch" {
		t.Fatalf("packages %v", f.Packages)
	}
	if len(f.Skipped) == 0 {
		t.Fatal(".env not marked skipped")
	}
}

func TestStreamlitApp(t *testing.T) {
	d := t.TempDir()
	write(t, d, "app.py", "import streamlit as st\nst.title('Genome Map Viewer')\n")
	f, _ := Dir(d)
	if f.WebApp != "streamlit" || f.WebPort != 8501 || f.Entry != "app.py" || f.Title != "Genome Map Viewer" || f.UsesGPU {
		t.Fatalf("got %+v", f)
	}
}

func TestShinyApp(t *testing.T) {
	d := t.TempDir()
	write(t, d, "app.R", "library(shiny)\nui <- fluidPage(titlePanel('Survey Explorer'))\nshinyApp(ui, server)\n")
	f, _ := Dir(d)
	if f.WebApp != "shiny" || f.WebPort != 3838 || f.Title != "Survey Explorer" {
		t.Fatalf("got %+v", f)
	}
}

func TestSlurmScript(t *testing.T) {
	d := t.TempDir()
	write(t, d, "job.sh", "#!/bin/bash\n#SBATCH --gres=gpu:1\n#SBATCH --time=02:00:00\npython run.py\n")
	write(t, d, "run.py", "print(1)\n")
	f, _ := Dir(d)
	if len(f.Slurm) != 1 || !f.UsesGPU {
		t.Fatalf("got %+v", f)
	}
}

func TestFlaskWithDashIsFlask(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "app.py"), []byte("from flask import Flask\nfrom dash import Dash\nserver = Flask(__name__)\napp = server\nDash(__name__, server=server)\n"), 0o644)
	f, err := Dir(d)
	if err != nil {
		t.Fatal(err)
	}
	if f.WebApp != "flask" || f.WebPort != 5000 {
		t.Fatalf("got %s:%d, want flask:5000", f.WebApp, f.WebPort)
	}
}
