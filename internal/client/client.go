// Package client writes user-scope MCP client configurations for nrp-mcp.
//
// Each client entry uses the absolute path to the nrp-mcp binary and ["serve"],
// writes at user scope so the server is available across all projects, keeps a
// .bak copy of any existing file before modifying it, preserves existing keys
// and other MCP servers in the file, and returns the exact restart instruction
// for that application.
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// All is the ordered list of supported client names.
var All = []string{
	"claude-desktop",
	"claude-code",
	"vscode",
	"cursor",
	"gemini",
	"opencode",
	"hermes",
}

// Env holds environment overrides so tests can run against a fake HOME and OS
// without ever touching real user configuration files.
type Env struct {
	GOOS          string
	Home          string
	AppData       string
	XDGConfigHome string
	ClaudeConfig  string
	OpenCodeCfg   string
	HermesHome    string
	BinPath       string
}

// DefaultEnv builds an Env from the current process environment and executable path.
func DefaultEnv() (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("could not determine home directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return nil, fmt.Errorf("could not determine nrp-mcp executable path: %w", err)
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return &Env{
		GOOS:          runtime.GOOS,
		Home:          home,
		AppData:       os.Getenv("APPDATA"),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		ClaudeConfig:  os.Getenv("CLAUDE_CONFIG_DIR"),
		OpenCodeCfg:   os.Getenv("OPENCODE_CONFIG"),
		HermesHome:    os.Getenv("HERMES_HOME"),
		BinPath:       exe,
	}, nil
}

// Result describes what Configure wrote.
type Result struct {
	Client  string
	App     string
	Path    string
	Backup  string
	Restart string
}

// Normalize maps aliases to canonical client names.
func Normalize(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	switch s {
	case "claude", "claudedesktop", "claude_desktop":
		return "claude-desktop"
	case "claudecode", "claude_code":
		return "claude-code"
	case "code", "vs-code", "vs_code":
		return "vscode"
	case "gemini-cli", "geminicli", "gemini_cli":
		return "gemini"
	case "open-code", "open_code":
		return "opencode"
	case "hermes-agent", "hermes_agent":
		return "hermes"
	default:
		return s
	}
}

// appData returns the Windows %APPDATA% directory or falls back to
// %USERPROFILE%\AppData\Roaming.
func (e *Env) appData() string {
	if e.AppData != "" {
		return e.AppData
	}
	return filepath.Join(e.Home, "AppData", "Roaming")
}

// xdgConfig returns $XDG_CONFIG_HOME or ~/.config.
func (e *Env) xdgConfig() string {
	if e.XDGConfigHome != "" {
		return e.XDGConfigHome
	}
	return filepath.Join(e.Home, ".config")
}

// ConfigPath returns the user-scope configuration file path and display name
// for the given client.
//
// Official documentation sources for paths and formats:
//   - claude-desktop: https://modelcontextprotocol.io/docs/2024-11-05/develop/connect-local-servers
//     macOS: ~/Library/Application Support/Claude/claude_desktop_config.json
//     Windows: %APPDATA%\Claude\claude_desktop_config.json
//     Linux: $XDG_CONFIG_HOME/Claude/claude_desktop_config.json (or ~/.config/Claude/claude_desktop_config.json)
//     Format: top-level "mcpServers": {"nrp": {"command": "<abs-path>", "args": ["serve"]}}
//   - claude-code: https://code.claude.com/docs/en/mcp-quickstart
//     User scope (--scope user): ~/.claude.json (or $CLAUDE_CONFIG_DIR/.claude.json)
//     Format: top-level "mcpServers": {"nrp": {"type": "stdio", "command": "<abs-path>", "args": ["serve"]}}
//   - vscode: https://code.visualstudio.com/docs/agents/reference/mcp-configuration
//     User profile mcp.json:
//     macOS: ~/Library/Application Support/Code/User/mcp.json
//     Windows: %APPDATA%\Code\User\mcp.json
//     Linux: $XDG_CONFIG_HOME/Code/User/mcp.json (or ~/.config/Code/User/mcp.json)
//     Format: top-level "servers" (NOT "mcpServers"): {"nrp": {"type": "stdio", "command": "<abs-path>", "args": ["serve"]}}
//   - cursor: https://cursor.com/docs/mcp
//     Global user config: ~/.cursor/mcp.json
//     Format: top-level "mcpServers": {"nrp": {"command": "<abs-path>", "args": ["serve"]}}
//   - gemini: https://geminicli.com/docs/tools/mcp-server/ and https://google-gemini.github.io/gemini-cli/docs/get-started/configuration.html
//     User settings (--scope user): ~/.gemini/settings.json
//     Format: top-level "mcpServers": {"nrp": {"command": "<abs-path>", "args": ["serve"]}}
//   - opencode: https://opencode.ai/docs/mcp-servers/ and https://opencode.ai/docs/config/
//     Global user config: ~/.config/opencode/opencode.json (or $XDG_CONFIG_HOME/opencode/opencode.json or $OPENCODE_CONFIG)
//     Format: top-level "mcp": {"nrp": {"type": "local", "command": ["<abs-path>", "serve"], "enabled": true}}
//   - hermes: https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp
//     User config: ~/.hermes/config.yaml (or $HERMES_HOME/config.yaml)
//     Format: YAML top-level mcp_servers:\n  nrp:\n    command: "<abs-path>"\n    args: ["serve"]
func (e *Env) ConfigPath(name string) (path string, appName string, err error) {
	switch Normalize(name) {
	case "claude-desktop":
		switch e.GOOS {
		case "darwin":
			path = filepath.Join(e.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		case "windows":
			path = filepath.Join(e.appData(), "Claude", "claude_desktop_config.json")
		default:
			path = filepath.Join(e.xdgConfig(), "Claude", "claude_desktop_config.json")
		}
		return path, "Claude Desktop", nil

	case "claude-code":
		if e.ClaudeConfig != "" {
			path = filepath.Join(e.ClaudeConfig, ".claude.json")
		} else {
			path = filepath.Join(e.Home, ".claude.json")
		}
		return path, "Claude Code", nil

	case "vscode":
		switch e.GOOS {
		case "darwin":
			path = filepath.Join(e.Home, "Library", "Application Support", "Code", "User", "mcp.json")
		case "windows":
			path = filepath.Join(e.appData(), "Code", "User", "mcp.json")
		default:
			path = filepath.Join(e.xdgConfig(), "Code", "User", "mcp.json")
		}
		return path, "VS Code", nil

	case "cursor":
		return filepath.Join(e.Home, ".cursor", "mcp.json"), "Cursor", nil

	case "gemini":
		return filepath.Join(e.Home, ".gemini", "settings.json"), "Gemini CLI", nil

	case "opencode":
		if e.OpenCodeCfg != "" {
			return e.OpenCodeCfg, "OpenCode", nil
		}
		return filepath.Join(e.xdgConfig(), "opencode", "opencode.json"), "OpenCode", nil

	case "hermes":
		if e.HermesHome != "" {
			return filepath.Join(e.HermesHome, "config.yaml"), "Hermes Agent", nil
		}
		return filepath.Join(e.Home, ".hermes", "config.yaml"), "Hermes Agent", nil

	default:
		return "", "", fmt.Errorf("unknown client %q (supported: %s)", name, strings.Join(All, ", "))
	}
}

// Configure writes or updates the user-scope configuration for the named client.
// If the target file already exists, it first writes a copy to <path>.bak.
func (e *Env) Configure(name string, extraArgs ...string) (*Result, error) {
	canon := Normalize(name)
	path, app, err := e.ConfigPath(canon)
	if err != nil {
		return nil, err
	}
	if e.BinPath == "" {
		return nil, errors.New("binary path is empty")
	}
	bin := e.BinPath
	if !filepath.IsAbs(bin) {
		if abs, err := filepath.Abs(bin); err == nil {
			bin = abs
		}
	}
	args := append([]string{"serve"}, extraArgs...)

	if canon == "hermes" {
		bk, err := writeHermesYAML(path, bin, args)
		if err != nil {
			return nil, err
		}
		return &Result{
			Client:  canon,
			App:     app,
			Path:    path,
			Backup:  bk,
			Restart: fmt.Sprintf("restart %s", app),
		}, nil
	}

	bk, err := writeJSONConfig(canon, path, bin, args)
	if err != nil {
		return nil, err
	}
	return &Result{
		Client:  canon,
		App:     app,
		Path:    path,
		Backup:  bk,
		Restart: fmt.Sprintf("restart %s", app),
	}, nil
}

func writeJSONConfig(canon, path, bin string, args []string) (string, error) {
	root := map[string]any{}
	var bk string
	if old, err := os.ReadFile(path); err == nil {
		bk = path + ".bak"
		if err := os.WriteFile(bk, old, 0o600); err != nil {
			return "", fmt.Errorf("backup %s: %w", bk, err)
		}
		if len(strings.TrimSpace(string(old))) > 0 {
			if err := json.Unmarshal(old, &root); err != nil {
				return "", fmt.Errorf("%s is not valid JSON (backed up to %s): %w", path, bk, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	topKey := "mcpServers"
	var entry map[string]any
	switch canon {
	case "vscode":
		// VS Code user profile mcp.json uses top-level "servers" (not "mcpServers")
		// and "type": "stdio": https://code.visualstudio.com/docs/agents/reference/mcp-configuration
		topKey = "servers"
		entry = map[string]any{
			"type":    "stdio",
			"command": bin,
			"args":    args,
		}
	case "claude-code":
		// Claude Code ~/.claude.json user scope uses top-level "mcpServers" with "type": "stdio":
		// https://code.claude.com/docs/en/mcp-quickstart
		topKey = "mcpServers"
		entry = map[string]any{
			"type":    "stdio",
			"command": bin,
			"args":    args,
		}
	case "opencode":
		// OpenCode ~/.config/opencode/opencode.json uses top-level "mcp" with "type": "local"
		// and a single "command" array: https://opencode.ai/docs/mcp-servers/
		topKey = "mcp"
		if _, ok := root["$schema"]; !ok {
			root["$schema"] = "https://opencode.ai/config.json"
		}
		cmdList := append([]string{bin}, args...)
		entry = map[string]any{
			"type":    "local",
			"command": cmdList,
			"enabled": true,
		}
	default:
		// claude-desktop, cursor, gemini use top-level "mcpServers" with "command" and "args".
		topKey = "mcpServers"
		entry = map[string]any{
			"command": bin,
			"args":    args,
		}
	}

	servers, ok := root[topKey].(map[string]any)
	if !ok || servers == nil {
		servers = map[string]any{}
	}
	servers["nrp"] = entry
	root[topKey] = servers

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeAtomic(path, out, 0o600); err != nil {
		return "", err
	}
	return bk, nil
}

// writeHermesYAML updates or creates ~/.hermes/config.yaml with mcp_servers.nrp,
// preserving existing YAML lines and comments.
// Official docs: https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp
func writeHermesYAML(path, bin string, args []string) (string, error) {
	var bk string
	var existing string
	if old, err := os.ReadFile(path); err == nil {
		bk = path + ".bak"
		if err := os.WriteFile(bk, old, 0o600); err != nil {
			return "", fmt.Errorf("backup %s: %w", bk, err)
		}
		existing = string(old)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	argsJSON, _ := json.Marshal(args)
	binJSON, _ := json.Marshal(bin)
	nrpBlock := fmt.Sprintf("  nrp:\n    command: %s\n    args: %s", string(binJSON), string(argsJSON))

	updated := upsertHermesMCPServer(existing, nrpBlock)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := writeAtomic(path, []byte(updated), 0o600); err != nil {
		return "", err
	}
	return bk, nil
}

func upsertHermesMCPServer(content, nrpBlock string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "mcp_servers:\n" + nrpBlock + "\n"
	}
	rawLines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	// Drop a single trailing empty element from splitting a newline-terminated file.
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	mcpIdx := -1
	for i, line := range rawLines {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
			(t == "mcp_servers:" || strings.HasPrefix(t, "mcp_servers:")) {
			mcpIdx = i
			break
		}
	}
	if mcpIdx == -1 {
		return strings.Join(rawLines, "\n") + "\n\nmcp_servers:\n" + nrpBlock + "\n"
	}

	// Find end of mcp_servers block (next top-level key or EOF).
	endIdx := len(rawLines)
	for i := mcpIdx + 1; i < len(rawLines); i++ {
		line := rawLines[i]
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			endIdx = i
			break
		}
	}

	// Handle inline empty map `mcp_servers: {}` or `mcp_servers: null`.
	headerRest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawLines[mcpIdx]), "mcp_servers:"))
	if headerRest == "{}" || headerRest == "null" || headerRest == "~" {
		rawLines[mcpIdx] = "mcp_servers:"
	}

	// Within [mcpIdx+1, endIdx), look for an existing `  nrp:` entry at 2-space indent.
	nrpStart, nrpEnd := -1, -1
	for i := mcpIdx + 1; i < endIdx; i++ {
		line := rawLines[i]
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if nrpStart == -1 {
			if indent == 2 && (t == "nrp:" || strings.HasPrefix(t, "nrp:")) {
				nrpStart = i
				nrpEnd = endIdx
			}
		} else if indent <= 2 {
			nrpEnd = i
			break
		}
	}

	blockLines := strings.Split(nrpBlock, "\n")
	var out []string
	if nrpStart != -1 {
		out = append(out, rawLines[:nrpStart]...)
		out = append(out, blockLines...)
		out = append(out, rawLines[nrpEnd:]...)
	} else {
		// Insert before any trailing blank lines at the end of mcp_servers.
		insertAt := endIdx
		for insertAt > mcpIdx+1 && strings.TrimSpace(rawLines[insertAt-1]) == "" {
			insertAt--
		}
		out = append(out, rawLines[:insertAt]...)
		out = append(out, blockLines...)
		out = append(out, rawLines[insertAt:]...)
	}
	return strings.Join(out, "\n") + "\n"
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".nrp-tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
