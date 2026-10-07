// Package inspect reads a local project folder to work out what it is: language, GPU
// need, entry point, web app type, dependency files, a Slurm script. It only reads small
// text files, never uploads anything, and skips secrets, VCS, dependencies and data.
package inspect

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Facts is what inspection found.
type Facts struct {
	Root        string   `json:"root"`
	Name        string   `json:"name"`
	Languages   []string `json:"languages"`
	Files       int      `json:"files_scanned"`
	SizeBytes   int64    `json:"size_bytes"`
	Dockerfile  bool     `json:"dockerfile"`
	DepFiles    []string `json:"dependency_files"`
	Packages    []string `json:"packages,omitempty"`
	UsesGPU     bool     `json:"uses_gpu"`
	GPUReason   string   `json:"gpu_reason,omitempty"`
	Framework   string   `json:"framework,omitempty"` // pytorch, tensorflow, jax
	WebApp      string   `json:"web_app,omitempty"`   // shiny, streamlit, fastapi, flask, static, node, dash, gradio
	WebPort     int      `json:"web_port,omitempty"`
	Entry       string   `json:"entry,omitempty"` // main script, relative
	EntryReason string   `json:"entry_reason,omitempty"`
	Notebooks   []string `json:"notebooks,omitempty"`
	Slurm       []string `json:"slurm_scripts,omitempty"`
	GitRemote   string   `json:"git_remote,omitempty"`
	GitHubRepo  string   `json:"github_repo,omitempty"` // owner/repo when the remote is on github.com
	DockerPort  int      `json:"docker_port,omitempty"` // EXPOSE in the Dockerfile
	Makefile    bool     `json:"makefile,omitempty"`
	GitCommit   string   `json:"git_commit,omitempty"`
	Skipped     []string `json:"skipped_sensitive,omitempty"`
	Title       string   `json:"title,omitempty"`
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	".ipynb_checkpoints": true, ".nrp": true, "renv": true, ".tox": true, "dist": true, "build": true, "target": true}

var sensitiveRe = regexp.MustCompile(`(?i)(^\.env|\.env$|secret|credential|private[_-]?key|id_rsa|\.pem$|\.key$|kubeconfig|token)`)

const maxFiles = 4000
const maxRead = 512 * 1024

// Dir inspects a folder.
func Dir(root string) (*Facts, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	f := &Facts{Root: root, Name: filepath.Base(root)}
	if !st.IsDir() {
		// A single script.
		f.Root = filepath.Dir(root)
		f.Name = strings.TrimSuffix(filepath.Base(root), filepath.Ext(root))
		scanFile(f, root, filepath.Base(root))
		f.Entry = filepath.Base(root)
		f.EntryReason = "the file you gave"
		finish(f)
		return f, nil
	}
	langs := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if f.Files >= maxFiles {
			return filepath.SkipAll
		}
		f.Files++
		if info, e := d.Info(); e == nil {
			f.SizeBytes += info.Size()
		}
		if sensitiveRe.MatchString(d.Name()) {
			f.Skipped = append(f.Skipped, rel)
			return nil
		}
		switch ext := strings.ToLower(filepath.Ext(d.Name())); ext {
		case ".py":
			langs["python"] = true
		case ".r":
			langs["r"] = true
		case ".jl":
			langs["julia"] = true
		case ".ipynb":
			langs["python"] = true
			f.Notebooks = append(f.Notebooks, rel)
		case ".js", ".ts":
			langs["node"] = true
		case ".c", ".cpp", ".cu", ".f90":
			langs["compiled"] = true
		case ".m":
			langs["matlab"] = true
		}
		scanFile(f, p, rel)
		return nil
	})
	for l := range langs {
		f.Languages = append(f.Languages, l)
	}
	sort.Strings(f.Languages)
	readGit(f)
	finish(f)
	return f, nil
}

var (
	gpuRe     = regexp.MustCompile(`(?m)(^\s*(import|from)\s+(torch|tensorflow|jax|cupy|transformers|vllm|diffusers|accelerate)\b|\.cuda\(|device\s*=\s*['"]cuda|torch\.cuda|tf\.config\.list_physical_devices\(['"]GPU|#SBATCH\s+--gres=gpu|nvidia-smi)`)
	pkgLineRe = regexp.MustCompile(`^\s*([A-Za-z0-9_.\-]+)`)
	sbatchRe  = regexp.MustCompile(`(?m)^#SBATCH\s`)
	exposeRe  = regexp.MustCompile(`(?mi)^\s*EXPOSE\s+(\d+)`)
	githubRe  = regexp.MustCompile(`github\.com[^:/]*[:/]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)
	mainRe    = regexp.MustCompile(`(?m)^if\s+__name__\s*==\s*['"]__main__['"]`)
	titleRe   = regexp.MustCompile(`(?mi)(?:st\.title|st\.set_page_config\(\s*page_title\s*=|titlePanel|<title>|page_title\s*=)\(?\s*['"]([^'"]{3,60})['"]`)
)

func scanFile(f *Facts, p, rel string) {
	base := strings.ToLower(filepath.Base(rel))
	switch base {
	case "dockerfile", "containerfile":
		f.Dockerfile = true
		if m := exposeRe.FindStringSubmatch(readSmall(p)); m != nil {
			f.DockerPort, _ = strconv.Atoi(m[1])
		}
	case "makefile":
		f.Makefile = true
	case "requirements.txt", "pyproject.toml", "environment.yml", "environment.yaml", "setup.py", "renv.lock",
		"description", "package.json", "project.toml", "pixi.toml":
		f.DepFiles = append(f.DepFiles, rel)
	}
	ext := strings.ToLower(filepath.Ext(base))
	text := map[string]bool{".py": true, ".r": true, ".jl": true, ".sh": true, ".slurm": true, ".sbatch": true, ".txt": true,
		".toml": true, ".yml": true, ".yaml": true, ".json": true, ".js": true, ".ts": true, ".html": true, ".ipynb": true, "": true}
	if !text[ext] {
		return
	}
	b := readSmall(p)
	if b == "" {
		return
	}
	if !f.UsesGPU {
		if m := gpuRe.FindString(b); m != "" {
			f.UsesGPU = true
			f.GPUReason = strings.TrimSpace(m) + " in " + rel
		}
	}
	switch {
	case strings.Contains(b, "import torch") || strings.Contains(b, "from torch"):
		setFW(f, "pytorch")
	case strings.Contains(b, "import tensorflow") || strings.Contains(b, "from tensorflow"):
		setFW(f, "tensorflow")
	case strings.Contains(b, "import jax"):
		setFW(f, "jax")
	}
	if base == "requirements.txt" {
		sc := bufio.NewScanner(strings.NewReader(b))
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "-") {
				continue
			}
			if m := pkgLineRe.FindStringSubmatch(l); m != nil {
				f.Packages = append(f.Packages, strings.ToLower(m[1]))
			}
		}
	}
	if (ext == ".sh" || ext == ".slurm" || ext == ".sbatch" || ext == "") && sbatchRe.MatchString(b) {
		f.Slurm = append(f.Slurm, rel)
	}
	detectWeb(f, rel, base, b)
	if f.Title == "" {
		if m := titleRe.FindStringSubmatch(b); m != nil {
			f.Title = m[1]
		}
	}
	if ext == ".py" && f.Entry == "" && mainRe.MatchString(b) && f.WebApp == "" {
		f.Entry, f.EntryReason = rel, "has if __name__ == '__main__'"
	}
}

func setFW(f *Facts, fw string) {
	if f.Framework == "" {
		f.Framework = fw
	}
}

func detectWeb(f *Facts, rel, base, b string) {
	set := func(app string, port int, entry string) {
		if f.WebApp == "" {
			f.WebApp, f.WebPort = app, port
			if entry != "" {
				f.Entry, f.EntryReason = entry, app+" app"
			}
		}
	}
	switch {
	case base == "app.r" || base == "server.r" || base == "ui.r" || strings.Contains(b, "shinyApp(") || strings.Contains(b, "library(shiny)"):
		if strings.HasSuffix(base, ".r") {
			set("shiny", 3838, rel)
		}
	case strings.Contains(b, "import streamlit") && strings.HasSuffix(base, ".py"):
		set("streamlit", 8501, rel)
	case strings.Contains(b, "import gradio") && strings.HasSuffix(base, ".py"):
		set("gradio", 7860, rel)
	case (strings.Contains(b, "from dash import") || strings.Contains(b, "import dash")) && strings.HasSuffix(base, ".py") &&
		!strings.Contains(b, "Flask(__name__)"):
		// Dash mounted on a Flask server (Dash(server=...)) is served as a Flask app below.
		set("dash", 8050, rel)
	case strings.Contains(b, "FastAPI(") && strings.HasSuffix(base, ".py"):
		set("fastapi", 8000, rel)
	case strings.Contains(b, "Flask(__name__)") && strings.HasSuffix(base, ".py"):
		set("flask", 5000, rel)
	case base == "package.json" && (strings.Contains(b, `"express"`) || strings.Contains(b, `"next"`) || strings.Contains(b, `"start"`)):
		set("node", 3000, "")
	case base == "index.html" && !strings.Contains(rel, "/"):
		set("static", 80, "")
	}
}

func readSmall(p string) string {
	fh, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer fh.Close()
	buf := make([]byte, maxRead)
	n, _ := fh.Read(buf)
	return string(buf[:n])
}

func readGit(f *Facts) {
	head := readSmall(filepath.Join(f.Root, ".git", "HEAD"))
	if strings.HasPrefix(head, "ref: ") {
		ref := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
		f.GitCommit = strings.TrimSpace(readSmall(filepath.Join(f.Root, ".git", ref)))
	} else {
		f.GitCommit = strings.TrimSpace(head)
	}
	if len(f.GitCommit) > 12 {
		f.GitCommit = f.GitCommit[:12]
	}
	cfg := readSmall(filepath.Join(f.Root, ".git", "config"))
	if m := regexp.MustCompile(`(?m)^\s*url\s*=\s*(\S+)`).FindStringSubmatch(cfg); m != nil {
		f.GitRemote = m[1]
		if g := githubRe.FindStringSubmatch(f.GitRemote); g != nil {
			f.GitHubRepo = strings.ToLower(g[1] + "/" + g[2])
		}
	}
}

func finish(f *Facts) {
	if f.Entry == "" && f.WebApp == "" {
		for _, c := range []string{"main.py", "train.py", "run.py", "analysis.py", "main.R", "run.sh", "main.jl"} {
			if _, err := os.Stat(filepath.Join(f.Root, c)); err == nil {
				f.Entry, f.EntryReason = c, "conventional name"
				break
			}
		}
	}
	if f.Entry == "" && f.WebApp == "" {
		// Exactly one runnable script at the top level: that is the entry point.
		var only []string
		if ents, err := os.ReadDir(f.Root); err == nil {
			for _, e := range ents {
				switch strings.ToLower(filepath.Ext(e.Name())) {
				case ".py", ".r", ".jl", ".sh":
					if !e.IsDir() && !strings.HasPrefix(e.Name(), "test_") && e.Name() != "setup.py" {
						only = append(only, e.Name())
					}
				}
			}
		}
		if len(only) == 1 {
			f.Entry, f.EntryReason = only[0], "the only script in the folder"
		}
	}
	sort.Strings(f.DepFiles)
	sort.Strings(f.Packages)
	for _, p := range f.Packages {
		switch p {
		case "torch", "tensorflow", "jax", "cupy", "vllm", "transformers", "accelerate", "diffusers":
			if !f.UsesGPU {
				f.UsesGPU, f.GPUReason = true, p+" in requirements.txt"
			}
		}
		if p == "torch" {
			setFW(f, "pytorch")
		}
		if p == "tensorflow" {
			setFW(f, "tensorflow")
		}
	}
}
