// Command nrp-mcp is a local MCP server for running research work on the NRP
// Nautilus cluster as the signed-in researcher.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/client"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/config"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/consent"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/kube"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/mcpserver"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/ops"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/results"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/setup"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/store"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/version"
)

const usage = `nrp-mcp %s - run research work on NRP Nautilus from your AI assistant.

Usage:
  nrp-mcp serve      run the MCP server on stdio (what your MCP client starts)
  nrp-mcp setup      get this computer ready: checks kubectl, kubelogin, the NRP config and
                     sign-in, and offers to install or update what is missing (asks first;
                     --yes to skip the question, --no-sign-in to stop before the browser)
  nrp-mcp doctor     check kubectl, the NRP config and sign-in, and print your namespaces
  nrp-mcp init       write an example ~/.config/nrp-mcp/config.yaml (never overwrites)
  nrp-mcp approve    show the newest approval code (for when no notification appeared)
  nrp-mcp s3 setup   save your NRP S3 keys (from https://nrp.ai/s3token/) so job results are kept
  nrp-mcp client <name>  add nrp-mcp (user scope, full path) to an MCP client:
                     claude-desktop, claude-code, vscode, cursor, gemini, opencode, hermes
  nrp-mcp kubectl    run the installed kubectl with the NRP kubeconfig and context
  nrp-mcp version

Flags (serve, doctor, setup): --config PATH  --kubeconfig PATH  --context NAME  --namespace NS

Add to an MCP client automatically with ` + "`" + `nrp-mcp client <name>` + "`" + `, or in JSON (Claude Desktop, Cursor):
  {"mcpServers": {"nrp": {"command": "/full/path/to/nrp-mcp", "args": ["serve"]}}}
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version.Version)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "version", "--version", "-v":
		os.Exit(runVersion())
	case "init":
		os.Exit(runInit())
	case "approve":
		os.Exit(runApprove())
	case "s3":
		if len(args) == 0 || args[0] != "setup" {
			fmt.Fprintln(os.Stderr, "usage: nrp-mcp s3 setup")
			os.Exit(2)
		}
		os.Exit(runS3Setup(config.Load(config.DefaultPath())))
	case "kubectl":
		os.Exit(runKubectl(config.Load(config.DefaultPath()), args))
	case "client":
		os.Exit(runClient(args))
	case "serve", "doctor", "setup":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		cfgPath := fs.String("config", config.DefaultPath(), "config file")
		kc := fs.String("kubeconfig", "", "kubeconfig (default: config file, then ~/.kube/config)")
		kctx := fs.String("context", "", "kubeconfig context (default nautilus)")
		ns := fs.String("namespace", "", "default namespace")
		yes := fs.Bool("yes", false, "setup: install without asking")
		noSignIn := fs.Bool("no-sign-in", false, "setup: stop before the browser sign-in (CI, offline checks)")
		_ = fs.Parse(args)
		cfg := config.Load(*cfgPath)
		if *kc != "" {
			cfg.Kubeconfig = *kc
		}
		if *kctx != "" {
			cfg.Context = *kctx
		}
		if *ns != "" {
			cfg.Namespace = *ns
		}
		if cmd == "doctor" {
			os.Exit(runDoctor(cfg))
		}
		if cmd == "setup" {
			os.Exit(runSetup(cfg, *yes, *noSignIn))
		}
		os.Exit(runServe(cfg))
	case "-h", "--help", "help":
		fmt.Printf(usage, version.Version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n"+usage, cmd, version.Version)
		os.Exit(2)
	}
}

func newServer(cfg config.Config) (*mcpserver.Server, error) {
	st, err := store.New(store.DefaultDir())
	if err != nil {
		return nil, err
	}
	codes, err := consent.New(store.DefaultDir())
	if err != nil {
		return nil, err
	}
	k := kube.New(kube.Config{Kubectl: cfg.Kubectl, Kubeconfig: cfg.Kubeconfig, Context: cfg.Context})
	return &mcpserver.Server{Cfg: cfg, Ops: &ops.Ops{K: k}, Store: st, Codes: codes, HTTP: mcpserver.HTTPCheck, Now: time.Now}, nil
}

func runServe(cfg config.Config) int {
	// stdout is the MCP channel; logs go to stderr only.
	log.SetOutput(os.Stderr)
	s, err := newServer(cfg)
	if err != nil {
		log.Printf("nrp-mcp: %v", err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	srv := mcpserver.New(s)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil && err != io.EOF {
		log.Printf("nrp-mcp: %v", err)
		return 1
	}
	return 0
}

func runVersion() int {
	return runVersionWithChecker(version.DefaultChecker(store.DefaultDir()))
}

func runVersionWithChecker(vc *version.Checker) int {
	cur := version.Version
	if vc != nil && vc.Current != "" {
		cur = vc.Current
	}
	fmt.Println("nrp-mcp", cur)
	if vc != nil {
		info := vc.Check(context.Background(), true)
		if info.Newer && info.Notice != "" {
			fmt.Println("Update available:", info.Notice)
		}
	}
	return 0
}

func runDoctor(cfg config.Config) int {
	return runDoctorWith(cfg, nil)
}

func runDoctorWith(cfg config.Config, vc *version.Checker) int {
	fmt.Printf("nrp-mcp %s doctor\n", version.Version)
	kc := cfg.Kubeconfig
	if kc == "" {
		if env := os.Getenv("KUBECONFIG"); env != "" {
			kc = env
		} else {
			h, _ := os.UserHomeDir()
			kc = filepath.Join(h, ".kube", "config")
		}
	}
	ok := true
	check := func(name string, err error, good, fix string) {
		if err != nil {
			ok = false
			if fix != "" {
				fmt.Printf("  FAIL %-12s %v -> %s\n", name, err, fix)
			} else {
				fmt.Printf("  FAIL %-12s %v\n", name, err)
			}
			return
		}
		fmt.Printf("  ok   %-12s %s\n", name, good)
	}

	// KUBECONFIG may hold several files separated by os.PathListSeparator (: on Unix, ; on Windows).
	kcPaths := splitKubeconfigs(kc)
	var existingKCs, missingKCs []string
	var hasNRPKC bool
	for _, p := range kcPaths {
		b, err := os.ReadFile(p)
		if err != nil {
			missingKCs = append(missingKCs, p)
			continue
		}
		existingKCs = append(existingKCs, p)
		s := string(b)
		if strings.Contains(s, setup.NRPServerHint) || strings.Contains(s, "67.58.53.148") || strings.Contains(s, "nautilus") {
			hasNRPKC = true
		}
	}
	switch {
	case len(existingKCs) == 0:
		check("kubeconfig", fmt.Errorf("%s does not exist", kc), "", "download your NRP config at https://nrp.ai/config and run `nrp-mcp setup`")
	case len(missingKCs) > 0 && !hasNRPKC:
		check("kubeconfig", fmt.Errorf("missing %s and no Nautilus cluster in %s", strings.Join(missingKCs, ", "), strings.Join(existingKCs, ", ")), "", "download your NRP config at https://nrp.ai/config and run `nrp-mcp setup`")
	case !hasNRPKC:
		check("kubeconfig", fmt.Errorf("no Nautilus cluster in %s", strings.Join(existingKCs, ", ")), "", "download your NRP config at https://nrp.ai/config and run `nrp-mcp setup`")
	default:
		detail := strings.Join(existingKCs, string(os.PathListSeparator)) + " (has Nautilus)"
		if len(missingKCs) > 0 {
			detail += fmt.Sprintf(" (missing %s ignored by kubectl)", strings.Join(missingKCs, ", "))
		}
		check("kubeconfig", nil, detail, "")
	}

	s, err := newServer(cfg)
	if err != nil {
		check("store", err, "", "check permissions on "+store.DefaultDir()+" or run `nrp-mcp setup`")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := s.Ops.K.Run(ctx, "", nil, "version", "--client", "-o", "json")
	check("kubectl", err, fmt.Sprintf("%s (%d bytes of version info)", s.Ops.K.Cfg.Kubectl, len(out)), "run `nrp-mcp setup`")

	// Show consent mode and whether S3 keys exist (yes/no, never values).
	check("consent", nil, cfg.Consent, "")
	if _, kerr := results.LoadKeys(cfg.S3KeysFile); kerr == nil {
		check("s3-keys", nil, "yes ("+cfg.S3KeysFile+", values not shown)", "")
	} else {
		check("s3-keys", nil, "no (run `nrp-mcp s3 setup` with keys from https://nrp.ai/s3token/ to save job results)", "")
	}

	// Newest release from GitHub releases API (3 s timeout, skipped offline).
	if vc == nil {
		vc = version.DefaultChecker(s.Store.Dir)
	}
	relInfo := vc.Check(ctx, false)
	switch {
	case relInfo.Latest == "":
		check("release", nil, fmt.Sprintf("v%s (newest check skipped offline)", relInfo.Current), "")
	case relInfo.Newer:
		check("release", nil, fmt.Sprintf("v%s installed; newest is v%s (https://github.com/UCR-Research-Computing/nrp-mcp/releases/latest)", relInfo.Current, relInfo.Latest), "")
	default:
		check("release", nil, fmt.Sprintf("v%s (up to date; newest is v%s)", relInfo.Current, relInfo.Latest), "")
	}

	w, err := s.Ops.K.WhoAmI(ctx)
	if err != nil {
		check("sign-in", err, "", "run `nrp-mcp setup` or `nrp-mcp kubectl auth whoami`")
		fmt.Println("  store       ", s.Store.Dir)
		return 1
	}
	check("sign-in", nil, w.Username, "")
	if len(w.Namespaces) == 0 {
		check("namespaces", errors.New("0 namespaces (not in any namespace yet)"), "", "ask your PI to add you to a namespace at https://nrp.ai/namespaces (faculty can request one there)")
	} else {
		check("namespaces", nil, fmt.Sprintf("%v (admin of %v)", w.Namespaces, w.Admin), "")
	}
	ns := cfg.Namespace
	if ns == "" && len(w.Namespaces) > 0 {
		ns = w.Namespaces[0]
	}
	if ns != "" {
		qs, err := s.Ops.K.Quotas(ctx, ns)
		check("quota", err, fmt.Sprintf("%s special GPUs %v", ns, kube.GPUQuota(qs)), "ask your PI to add you to a namespace at https://nrp.ai/namespaces or check `nrp-mcp kubectl -n "+ns+" get resourcequota`")
	}
	fmt.Println("  store       ", s.Store.Dir)
	if !ok {
		fmt.Println("\nNot ready. Follow the `->` step on each FAIL line above.")
		return 1
	}
	fmt.Println("\nReady. Connect your AI assistant with `nrp-mcp client <claude-desktop|claude-code|vscode|cursor|gemini|opencode|hermes>`.")
	return 0
}

func splitKubeconfigs(kc string) []string {
	var out []string
	for _, p := range filepath.SplitList(kc) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 && strings.TrimSpace(kc) != "" {
		out = []string{strings.TrimSpace(kc)}
	}
	return out
}

// runS3Setup asks for the NRP S3 keys without echoing the secret and saves them 0600.
func runS3Setup(cfg config.Config) int {
	fmt.Println("nrp-mcp keeps job results in your own NRP S3 bucket.")
	fmt.Println("1. Open https://nrp.ai/s3token/ , sign in, and create an access key (it is free).")
	fmt.Println("2. Paste the two values here. The secret is not shown while you type.")
	fmt.Println("   They are saved only on this computer, in", cfg.S3KeysFile, "(readable only by you).")
	fmt.Print("\nAccess key: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	access := strings.TrimSpace(line)
	fmt.Print("Secret key: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not read the secret:", err)
		return 1
	}
	secret := strings.TrimSpace(string(b))
	if access == "" || secret == "" {
		fmt.Println("Nothing saved: both values are needed.")
		return 1
	}
	if err := results.SaveKeys(cfg.S3KeysFile, results.Keys{Access: access, Secret: secret}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Saved. New job plans will keep everything your code writes to results/.")
	return 0
}

// runApprove prints the newest approval code for the person (never for the assistant).
func runApprove() int {
	c, err := consent.New(store.DefaultDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if t := c.Latest(); t != "" {
		fmt.Print(t)
		return 0
	}
	fmt.Println("No approval code yet. Codes appear when your AI assistant asks to run something that needs your OK.")
	return 1
}

// runKubectl runs the configured or installed kubectl with the NRP kubeconfig and
// context, and puts the user bin folder on PATH so the kubelogin plugin is found.
func runKubectl(cfg config.Config, args []string) int {
	k := kube.New(kube.Config{Kubectl: cfg.Kubectl, Kubeconfig: cfg.Kubeconfig, Context: cfg.Context})
	if _, err := exec.LookPath(k.Cfg.Kubectl); err != nil {
		fmt.Fprintln(os.Stderr, "kubectl is not installed yet; run `nrp-mcp setup` first.")
		return 1
	}
	hasFlag := func(names ...string) bool {
		for _, a := range args {
			for _, n := range names {
				if a == n || strings.HasPrefix(a, n+"=") {
					return true
				}
			}
		}
		return false
	}
	var prefix []string
	if k.Cfg.Kubeconfig != "" && !hasFlag("--kubeconfig") {
		prefix = append(prefix, "--kubeconfig", k.Cfg.Kubeconfig)
	}
	if k.Cfg.Context != "" && !hasFlag("--context") {
		prefix = append(prefix, "--context", k.Cfg.Context)
	}
	if cfg.Namespace != "" && !hasFlag("-n", "--namespace") {
		prefix = append(prefix, "-n", cfg.Namespace)
	}
	cmd := exec.Command(k.Cfg.Kubectl, append(prefix, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// runClient writes the user-scope MCP configuration for the named client.
func runClient(args []string) int {
	e, err := client.DefaultEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return runClientWithEnv(e, args)
}

func runClientWithEnv(e *client.Env, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "usage: nrp-mcp client <%s>\n", strings.Join(client.All, "|"))
		return 2
	}
	name := args[0]
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	ns := fs.String("namespace", "", "optional default namespace to pass to nrp-mcp serve")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var extra []string
	if *ns != "" {
		extra = []string{"--namespace", *ns}
	}
	res, err := e.Configure(name, extra...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Wrote %s (%s, user scope) with %s [\"serve\"].\n", res.Path, res.App, e.BinPath)
	if res.Backup != "" {
		fmt.Printf("Backed up previous file to %s.\n", res.Backup)
	}
	fmt.Printf("Next: %s so it picks up the new nrp server.\n", res.Restart)
	return 0
}

func runInit() int {
	p := config.DefaultPath()
	if _, err := os.Stat(p); err == nil {
		fmt.Println("exists, not changed:", p)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(p, []byte(config.Example), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("wrote", p)
	return 0
}

func printReport(r *setup.Report) {
	marks := map[string]string{setup.OK: "ok  ", setup.Missing: "MISS", setup.Old: "OLD ", setup.Problem: "FIX ", setup.Manual: "TODO", setup.Note: "NOTE"}
	for _, c := range r.Checks {
		fmt.Printf("  %s %-10s %s\n", marks[c.Status], c.ID, c.Detail)
		if c.Status != setup.OK && c.Fix != "" {
			fmt.Printf("       -> %s\n", c.Fix)
		}
	}
}

func runSetup(cfg config.Config, yes, noSignIn bool) int {
	fmt.Printf("nrp-mcp %s setup (%s)\n\n", version.Version, "checks first, changes nothing without asking")
	e := setup.DefaultEnv(cfg.Kubeconfig, cfg.Context, cfg.Kubectl)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r := e.Inspect(ctx, false)
	printReport(r)
	auto := false
	for _, c := range r.Checks {
		if c.Status != setup.OK && c.Status != setup.Note && c.Auto {
			auto = true
		}
	}
	if auto {
		fmt.Printf("\nInstall or update the items above into %s?\n", e.BinDir)
		fmt.Println("Official releases only (dl.k8s.io, github.com/int128/kubelogin), SHA256-checked, no admin rights; old copies are kept.")
		if !yes {
			fmt.Print("Proceed? [y/N] ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Println("Nothing changed.")
				return 1
			}
		}
		fixed, err := e.Fix(ctx)
		if err != nil {
			fmt.Println("\nFAILED:", err)
			return 1
		}
		fmt.Println()
		for _, a := range fixed.Actions {
			fmt.Println("  done:", a)
		}
		fmt.Println()
		r = fixed
		printReport(r)
	}
	// Sign in once everything else is in place.
	pending := false
	for _, c := range r.Checks {
		if c.Status != setup.OK && c.Status != setup.Note && c.ID != "signin" {
			pending = true
		}
	}
	if !pending && noSignIn {
		fmt.Println("\nEverything is in place; sign-in skipped (--no-sign-in).")
		fmt.Println("Next: run `nrp-mcp setup` again, or `nrp-mcp kubectl auth whoami`, to sign in.")
		return 0
	}
	if !pending {
		fmt.Println("\nSigning in to Nautilus (your browser opens the first time)...")
		r = e.Inspect(ctx, true)
		printReport(r)
	}
	fmt.Println()
	fmt.Println(r.Summary)
	if r.PathHint != "" {
		fmt.Println("To use kubectl in your own terminal:", r.PathHint)
	}
	if r.Ready {
		fmt.Println("Next: run `nrp-mcp client <claude-desktop|claude-code|vscode|cursor|gemini|opencode|hermes>` to connect your AI assistant, or `nrp-mcp doctor`.")
		return 0
	}
	return 1
}
