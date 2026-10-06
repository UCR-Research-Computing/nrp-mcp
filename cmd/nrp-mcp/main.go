// Command nrp-mcp is a local MCP server for running research work on the NRP
// Nautilus cluster as the signed-in researcher.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/UCR-Research-Computing/nrp-mcp/internal/config"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/kube"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/mcpserver"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/ops"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/setup"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/store"
	"github.com/UCR-Research-Computing/nrp-mcp/internal/version"
)

const usage = `nrp-mcp %s - run research work on NRP Nautilus from your AI assistant.

Usage:
  nrp-mcp serve      run the MCP server on stdio (what your MCP client starts)
  nrp-mcp setup      get this computer ready: checks kubectl, kubelogin, the NRP config and
                     sign-in, and offers to install or update what is missing (asks first;
                     --yes to skip the question)
  nrp-mcp doctor     check kubectl, the NRP config and sign-in, and print your namespaces
  nrp-mcp init       write an example ~/.config/nrp-mcp/config.yaml (never overwrites)
  nrp-mcp version

Flags (serve, doctor, setup): --config PATH  --kubeconfig PATH  --context NAME  --namespace NS

Add to an MCP client, e.g. Claude Desktop or Claude Code:
  {"mcpServers": {"nrp": {"command": "nrp-mcp", "args": ["serve"]}}}
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version.Version)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("nrp-mcp", version.Version)
	case "init":
		os.Exit(runInit())
	case "serve", "doctor", "setup":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		cfgPath := fs.String("config", config.DefaultPath(), "config file")
		kc := fs.String("kubeconfig", "", "kubeconfig (default: config file, then ~/.kube/config)")
		kctx := fs.String("context", "", "kubeconfig context (default nautilus)")
		ns := fs.String("namespace", "", "default namespace")
		yes := fs.Bool("yes", false, "setup: install without asking")
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
			os.Exit(runSetup(cfg, *yes))
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
	k := kube.New(kube.Config{Kubectl: cfg.Kubectl, Kubeconfig: cfg.Kubeconfig, Context: cfg.Context})
	return &mcpserver.Server{Cfg: cfg, Ops: &ops.Ops{K: k}, Store: st, HTTP: mcpserver.HTTPCheck, Now: time.Now}, nil
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

func runDoctor(cfg config.Config) int {
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
	check := func(name string, err error, good string) {
		if err != nil {
			ok = false
			fmt.Printf("  FAIL %-12s %v\n", name, err)
			return
		}
		fmt.Printf("  ok   %-12s %s\n", name, good)
	}
	_, err := os.Stat(kc)
	check("kubeconfig", err, kc+" (contents not read by nrp-mcp)")
	s, err := newServer(cfg)
	if err != nil {
		check("store", err, "")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := s.Ops.K.Run(ctx, "", nil, "version", "--client", "-o", "json")
	check("kubectl", err, fmt.Sprintf("%s (%d bytes of version info)", cfg.Kubectl, len(out)))
	w, err := s.Ops.K.WhoAmI(ctx)
	if err != nil {
		check("sign-in", err, "")
		fmt.Println("\n  Run `kubectl --context " + cfg.Context + " auth whoami` once in a terminal to sign in through your browser (KB024).")
		return 1
	}
	check("sign-in", nil, w.Username)
	check("namespaces", nil, fmt.Sprintf("%v (admin of %v)", w.Namespaces, w.Admin))
	ns := cfg.Namespace
	if ns == "" && len(w.Namespaces) > 0 {
		ns = w.Namespaces[0]
	}
	if ns != "" {
		qs, err := s.Ops.K.Quotas(ctx, ns)
		check("quota", err, fmt.Sprintf("%s special GPUs %v", ns, kube.GPUQuota(qs)))
	}
	fmt.Println("  store       ", s.Store.Dir)
	if !ok {
		return 1
	}
	fmt.Println("\nReady. Add nrp-mcp to your MCP client with args [\"serve\"].")
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

func runSetup(cfg config.Config, yes bool) int {
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
		fmt.Println("Next: add nrp-mcp to your AI client (see README), or run `nrp-mcp doctor`.")
		return 0
	}
	return 1
}
