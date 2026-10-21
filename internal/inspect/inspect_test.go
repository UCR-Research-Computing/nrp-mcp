package inspect

import (
	"os"
	"path/filepath"
	"strings"
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

func TestPlainWebAndCandidateDetectionTable(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string]string
		single    string
		wantApp   string
		wantPort  int
		wantEntry string
		wantCands []string
		wantRPkgs []string
		wantDeps  []string
	}{
		{
			name:      "httpserver_8080",
			files:     map[string]string{"app.py": "from http.server import HTTPServer, BaseHTTPRequestHandler\nHTTPServer(('0.0.0.0', 8080), BaseHTTPRequestHandler).serve_forever()\n"},
			wantApp:   "python",
			wantPort:  8080,
			wantEntry: "app.py",
		},
		{
			name:      "serve_forever_8000",
			files:     map[string]string{"server.py": "import socketserver\nPORT = 8000\nwith socketserver.TCPServer(('', PORT), None) as httpd:\n    httpd.serve_forever()\n"},
			wantApp:   "python",
			wantPort:  8000,
			wantEntry: "server.py",
		},
		{
			name:      "app_run_port_kw",
			files:     map[string]string{"web.py": "from bottle import Bottle\napp = Bottle()\napp.run(host='0.0.0.0', port=8080)\n"},
			wantApp:   "python",
			wantPort:  8080,
			wantEntry: "web.py",
		},
		{
			name:      "single_file_httpserver",
			files:     map[string]string{"app.py": "from http.server import HTTPServer\nHTTPServer(('', 8000), None).serve_forever()\n", "other.py": "print(1)\n"},
			single:    "app.py",
			wantApp:   "python",
			wantPort:  8000,
			wantEntry: "app.py",
		},
		{
			name:      "two_python_scripts_candidates",
			files:     map[string]string{"a.py": "if __name__ == '__main__':\n    print(1)\n", "b.py": "if __name__ == '__main__':\n    print(2)\n"},
			wantCands: []string{"a.py", "b.py"},
		},
		{
			name:      "multi_r_scripts_with_install_r",
			files:     map[string]string{"model.R": "print(1)\n", "helpers.R": "x <- 1\n", "install.R": "install.packages('sandwich')\n"},
			wantCands: []string{"helpers.R", "model.R"},
			wantDeps:  []string{"install.R"},
		},
		{
			name:      "single_r_script_with_install_r",
			files:     map[string]string{"sim.R": "library(sandwich)\n", "install.R": "install.packages('sandwich')\n"},
			wantEntry: "sim.R",
			wantDeps:  []string{"install.R"},
		},
		{
			name: "r_description_imports",
			files: map[string]string{
				"main.R":      "library(dplyr)\n",
				"DESCRIPTION": "Package: myproj\nVersion: 0.1.0\nImports:\n    dplyr (>= 1.1.0),\n    sandwich,\n    ggplot2\nSuggests:\n    testthat\n",
			},
			wantEntry: "main.R",
			wantRPkgs: []string{"dplyr", "ggplot2", "sandwich"},
			wantDeps:  []string{"DESCRIPTION"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			for k, v := range tc.files {
				write(t, d, k, v)
			}
			target := d
			if tc.single != "" {
				target = filepath.Join(d, tc.single)
			}
			f, err := Dir(target)
			if err != nil {
				t.Fatal(err)
			}
			if f.WebApp != tc.wantApp || f.WebPort != tc.wantPort || f.Entry != tc.wantEntry {
				t.Fatalf("got app=%q port=%d entry=%q, want app=%q port=%d entry=%q",
					f.WebApp, f.WebPort, f.Entry, tc.wantApp, tc.wantPort, tc.wantEntry)
			}
			if strings.Join(f.Candidates, ",") != strings.Join(tc.wantCands, ",") {
				t.Fatalf("candidates got %v, want %v", f.Candidates, tc.wantCands)
			}
			if strings.Join(f.RPackages, ",") != strings.Join(tc.wantRPkgs, ",") {
				t.Fatalf("r_packages got %v, want %v", f.RPackages, tc.wantRPkgs)
			}
			if len(tc.wantDeps) > 0 && strings.Join(f.DepFiles, ",") != strings.Join(tc.wantDeps, ",") {
				t.Fatalf("dep_files got %v, want %v", f.DepFiles, tc.wantDeps)
			}
		})
	}
}
