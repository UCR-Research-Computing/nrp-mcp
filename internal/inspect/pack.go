package inspect

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Packing limits: one file and the whole ConfigMap.
const (
	MaxPackFile  = 200 * 1024
	MaxPackTotal = 900 * 1024
)

// Skip is one file that was left out of the pack, with a plain reason.
type Skip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Pack is what goes into the pod: file contents by relative path (always "/").
type Pack struct {
	Files     map[string]string `json:"-"`
	Names     []string          `json:"packed"`
	Skipped   []Skip            `json:"skipped"`
	DataFiles []string          `json:"data_files,omitempty"` // packed only because data_is_p1 was set
	NeedsP1   []string          `json:"needs_p1,omitempty"`   // data files held back until data_is_p1
	Bytes     int               `json:"bytes"`
}

// PackOptions changes what Pack takes.
type PackOptions struct {
	DataIsP1 bool   // the person confirmed the data is non-sensitive: data files may go in
	Only     string // single-file project: pack only this relative path
	WebOnly  bool   // static site: only web assets (html, css, js, images, fonts)
}

// skipDirNames are never packed, compared in lower case.
var skipDirNames = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	".ipynb_checkpoints": true, ".nrp": true, "renv": true, ".tox": true, "dist": true, "build": true, "target": true,
	"data": true, "datasets": true, "results": true, "output": true, "outputs": true, "checkpoints": true, "wandb": true, "mlruns": true}

// denyNameRe matches file names that hold keys or credentials (lower case).
var denyNameRe = regexp.MustCompile(`(^\.env|\.env$|^\.netrc$|^\.pgpass$|^id_(rsa|dsa|ecdsa|ed25519)|\.(pem|key|p12|pfx|jks|keystore|kdbx|ppk|gpg|asc)$|credential|service[-_]?account|kubeconfig|^secrets?\.|^\.?htpasswd$|private[-_]?key|^token(s)?(\.|$)|apikey|api[-_]key)`)

// contentDenyRe finds secrets inside otherwise normal files.
var contentDenyRe = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----|(?m)^\s*client-key-data:|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|glpat-[A-Za-z0-9_-]{20}|xox[baprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{32,}|"private_key"\s*:\s*"-----BEGIN`)

// codeExt are source and small text/config files that may be packed.
var codeExt = map[string]bool{".py": true, ".r": true, ".jl": true, ".sh": true, ".bash": true, ".txt": true, ".toml": true,
	".yml": true, ".yaml": true, ".json": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".html": true, ".htm": true, ".css": true,
	".md": true, ".rmd": true, ".qmd": true, ".cfg": true, ".ini": true, ".lock": true, ".sql": true, ".m": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".cu": true, ".f90": true, ".f": true, ".go": true, ".rs": true, ".java": true, ".scala": true, ".ipynb": true,
	".svg": true, ".slurm": true, ".sbatch": true, ".tex": true, ".bib": true, ".xml": true, ".csl": true}

// webExt are what a static site may serve.
var webExt = map[string]bool{".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true, ".json": true, ".svg": true,
	".txt": true, ".xml": true, ".webmanifest": true, ".map": true, ".md": true}

// dataExt are data files: they need the person's P1 confirmation.
var dataExt = map[string]bool{".csv": true, ".tsv": true, ".xlsx": true, ".xls": true, ".parquet": true, ".feather": true,
	".sav": true, ".dta": true, ".sas7bdat": true, ".rds": true, ".rdata": true, ".h5": true, ".hdf5": true, ".npy": true, ".npz": true,
	".pkl": true, ".pickle": true, ".db": true, ".sqlite": true, ".fasta": true, ".fastq": true, ".vcf": true, ".bam": true, ".nii": true}

// plainNames are extensionless files that are safe and useful.
var plainNames = map[string]bool{"makefile": true, "dockerfile": true, "procfile": true, "license": true, "readme": true, "description": true, "namespace": true, "gemfile": true}

// keyRe is the ConfigMap key charset (after "/" becomes "__SLASH__").
var keyRe = regexp.MustCompile(`^[-._a-zA-Z0-9/]+$`)

// PackDir decides which project files go into the pod. It never follows symlinks and
// never reads outside root. It returns an error only when the allowed files are too big.
func PackDir(root string, o PackOptions) (*Pack, error) {
	p := &Pack{Files: map[string]string{}, Names: []string{}, Skipped: []Skip{}}
	skip := func(rel, why string) { p.Skipped = append(p.Skipped, Skip{Path: rel, Reason: why}) }
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		low := strings.ToLower(d.Name())
		if d.Type()&fs.ModeSymlink != 0 {
			skip(rel, "a link to somewhere else (links are never followed)")
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(low, ".") || skipDirNames[low] {
				skip(rel+"/", "folder left out (hidden, installed packages, data or results)")
				return filepath.SkipDir
			}
			return nil
		}
		if o.Only != "" && rel != filepath.ToSlash(o.Only) {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		ext := filepath.Ext(low)
		switch {
		case denyNameRe.MatchString(low):
			skip(rel, "looks like a key, password or credential file")
			return nil
		case strings.HasPrefix(low, "."):
			skip(rel, "hidden file")
			return nil
		case !keyRe.MatchString(rel):
			skip(rel, "name has spaces or special characters (rename it to letters, digits, - _ .)")
			return nil
		}
		isData := dataExt[ext]
		switch {
		case o.WebOnly && !webExt[ext] && !isData:
			skip(rel, "not a web page file (a public site serves only html, css, js, json, svg, txt)")
			return nil
		case o.WebOnly && isData:
			skip(rel, "data file: a public site never serves data files")
			return nil
		case isData && !o.DataIsP1:
			p.NeedsP1 = append(p.NeedsP1, rel)
			skip(rel, "data file: needs data_is_p1=true (only non-sensitive P1 data may go to Nautilus)")
			return nil
		case !isData && !codeExt[ext] && !(ext == "" && plainNames[low]):
			skip(rel, "not a source or small text file")
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		if info.Size() > MaxPackFile {
			skip(rel, fmt.Sprintf("bigger than %d KB (use git, an image from nrp_build, or a volume)", MaxPackFile/1024))
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			skip(rel, "could not be read")
			return nil
		}
		if bytes.IndexByte(b, 0) >= 0 {
			skip(rel, "binary file")
			return nil
		}
		if contentDenyRe.Match(b) {
			skip(rel, "contains what looks like a private key or access token")
			return nil
		}
		if p.Bytes+len(b) > MaxPackTotal {
			return fmt.Errorf("the project's files are over %d KB together; use nrp_build (an image) or put the code in git", MaxPackTotal/1024)
		}
		p.Bytes += len(b)
		p.Files[rel] = string(b)
		p.Names = append(p.Names, rel)
		if isData {
			p.DataFiles = append(p.DataFiles, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(p.Names)
	sort.Slice(p.Skipped, func(i, j int) bool { return p.Skipped[i].Path < p.Skipped[j].Path })
	return p, nil
}
