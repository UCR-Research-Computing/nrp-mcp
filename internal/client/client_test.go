package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenPerClientInTempHome(t *testing.T) {
	home := t.TempDir()
	env := &Env{
		GOOS:    "darwin",
		Home:    home,
		BinPath: "/opt/nrp/bin/nrp-mcp",
	}

	cases := []struct {
		name       string
		wantRel    string
		goldenFile string
		wantApp    string
	}{
		{
			name:       "claude-desktop",
			wantRel:    filepath.Join("Library", "Application Support", "Claude", "claude_desktop_config.json"),
			goldenFile: "claude-desktop.json",
			wantApp:    "Claude Desktop",
		},
		{
			name:       "claude-code",
			wantRel:    ".claude.json",
			goldenFile: "claude-code.json",
			wantApp:    "Claude Code",
		},
		{
			name:       "vscode",
			wantRel:    filepath.Join("Library", "Application Support", "Code", "User", "mcp.json"),
			goldenFile: "vscode.json",
			wantApp:    "VS Code",
		},
		{
			name:       "cursor",
			wantRel:    filepath.Join(".cursor", "mcp.json"),
			goldenFile: "cursor.json",
			wantApp:    "Cursor",
		},
		{
			name:       "gemini",
			wantRel:    filepath.Join(".gemini", "settings.json"),
			goldenFile: "gemini.json",
			wantApp:    "Gemini CLI",
		},
		{
			name:       "opencode",
			wantRel:    filepath.Join(".config", "opencode", "opencode.json"),
			goldenFile: "opencode.json",
			wantApp:    "OpenCode",
		},
		{
			name:       "hermes",
			wantRel:    filepath.Join(".hermes", "config.yaml"),
			goldenFile: "hermes.yaml",
			wantApp:    "Hermes Agent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := env.Configure(tc.name)
			if err != nil {
				t.Fatalf("Configure(%s): %v", tc.name, err)
			}
			wantPath := filepath.Join(home, tc.wantRel)
			if res.Path != wantPath {
				t.Fatalf("path = %q, want %q", res.Path, wantPath)
			}
			if res.Backup != "" {
				t.Fatalf("expected no backup on fresh file, got %q", res.Backup)
			}
			wantRestart := "restart " + tc.wantApp
			if res.Restart != wantRestart {
				t.Fatalf("restart = %q, want %q", res.Restart, wantRestart)
			}

			got, err := os.ReadFile(res.Path)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", res.Path, err)
			}
			goldenPath := filepath.Join("testdata", "golden", tc.goldenFile)
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", goldenPath, err)
			}
			if string(got) != string(want) {
				t.Fatalf("mismatch against %s:\ngot:\n%s\nwant:\n%s", goldenPath, got, want)
			}
		})
	}
}

func TestConfigPathsAcrossOS(t *testing.T) {
	// Windows paths use %APPDATA% for Claude Desktop and VS Code, and %USERPROFILE% for dot-configs.
	winEnv := &Env{
		GOOS:    "windows",
		Home:    `C:\Users\jane`,
		AppData: filepath.Join(`C:\Users\jane`, "AppData", "Roaming"),
		BinPath: `C:\Users\jane\AppData\Local\Programs\nrp-mcp\nrp-mcp.exe`,
	}
	p, _, err := winEnv.ConfigPath("claude-desktop")
	if err != nil || !strings.HasSuffix(p, filepath.Join("Claude", "claude_desktop_config.json")) {
		t.Fatalf("windows claude-desktop path = %q, err = %v", p, err)
	}
	p, _, err = winEnv.ConfigPath("vscode")
	if err != nil || !strings.HasSuffix(p, filepath.Join("Code", "User", "mcp.json")) {
		t.Fatalf("windows vscode path = %q, err = %v", p, err)
	}

	// Linux paths use ~/.config (or $XDG_CONFIG_HOME) for Claude Desktop, VS Code, and OpenCode.
	linEnv := &Env{
		GOOS:          "linux",
		Home:          "/home/jane",
		XDGConfigHome: "/home/jane/.xdg",
		BinPath:       "/home/jane/.local/bin/nrp-mcp",
	}
	p, _, err = linEnv.ConfigPath("claude-desktop")
	if err != nil || p != filepath.Join("/home/jane/.xdg", "Claude", "claude_desktop_config.json") {
		t.Fatalf("linux claude-desktop path = %q, err = %v", p, err)
	}
	p, _, err = linEnv.ConfigPath("vscode")
	if err != nil || p != filepath.Join("/home/jane/.xdg", "Code", "User", "mcp.json") {
		t.Fatalf("linux vscode path = %q, err = %v", p, err)
	}
	p, _, err = linEnv.ConfigPath("opencode")
	if err != nil || p != filepath.Join("/home/jane/.xdg", "opencode", "opencode.json") {
		t.Fatalf("linux opencode path = %q, err = %v", p, err)
	}
}

func TestBackupAndMergeExistingConfigs(t *testing.T) {
	home := t.TempDir()
	env := &Env{
		GOOS:    "darwin",
		Home:    home,
		BinPath: "/Users/test/.local/bin/nrp-mcp",
	}

	// 1. VS Code existing file: preserves other settings and other servers, uses "servers" not "mcpServers", writes .bak.
	vsPath, _, _ := env.ConfigPath("vscode")
	if err := os.MkdirAll(filepath.Dir(vsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	oldVS := "{\n  \"inputs\": [],\n  \"servers\": {\n    \"other\": {\n      \"type\": \"stdio\",\n      \"command\": \"npx\"\n    }\n  }\n}\n"
	if err := os.WriteFile(vsPath, []byte(oldVS), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := env.Configure("vscode")
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != vsPath+".bak" {
		t.Fatalf("backup = %q, want %q", res.Backup, vsPath+".bak")
	}
	bakBytes, err := os.ReadFile(res.Backup)
	if err != nil || string(bakBytes) != oldVS {
		t.Fatalf("backup content mismatch: err=%v body=%q", err, bakBytes)
	}
	newVS, err := os.ReadFile(vsPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(newVS, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, hasMCPServers := parsed["mcpServers"]; hasMCPServers {
		t.Fatalf("vscode config must use 'servers', not 'mcpServers': %s", newVS)
	}
	servers, ok := parsed["servers"].(map[string]any)
	if !ok || servers["other"] == nil || servers["nrp"] == nil {
		t.Fatalf("expected both 'other' and 'nrp' in servers: %s", newVS)
	}

	// 2. Hermes existing config.yaml: preserves existing keys and other mcp_servers, writes .bak, updates nrp in place on second run.
	hermesPath, _, _ := env.ConfigPath("hermes")
	if err := os.MkdirAll(filepath.Dir(hermesPath), 0o700); err != nil {
		t.Fatal(err)
	}
	oldHermes := "model: gemini-4-argon\nmcp_servers:\n  time:\n    command: \"uvx\"\n    args: [\"mcp-server-time\"]\ndisplay:\n  skin: default\n"
	if err := os.WriteFile(hermesPath, []byte(oldHermes), 0o600); err != nil {
		t.Fatal(err)
	}
	hRes, err := env.Configure("hermes")
	if err != nil {
		t.Fatal(err)
	}
	if hRes.Backup != hermesPath+".bak" {
		t.Fatalf("hermes backup = %q, want %q", hRes.Backup, hermesPath+".bak")
	}
	hBak, err := os.ReadFile(hRes.Backup)
	if err != nil || string(hBak) != oldHermes {
		t.Fatalf("hermes backup mismatch: %q", hBak)
	}
	hNew, err := os.ReadFile(hermesPath)
	if err != nil {
		t.Fatal(err)
	}
	wantHermes := "model: gemini-4-argon\nmcp_servers:\n  time:\n    command: \"uvx\"\n    args: [\"mcp-server-time\"]\n  nrp:\n    command: \"/Users/test/.local/bin/nrp-mcp\"\n    args: [\"serve\"]\ndisplay:\n  skin: default\n"
	if string(hNew) != wantHermes {
		t.Fatalf("hermes merged YAML:\ngot:\n%s\nwant:\n%s", hNew, wantHermes)
	}

	// Second run with a new binary path updates nrp in place without duplicating it.
	env.BinPath = "/opt/new/nrp-mcp"
	if _, err := env.Configure("hermes", "--namespace", "ucr-lab"); err != nil {
		t.Fatal(err)
	}
	hNew2, err := os.ReadFile(hermesPath)
	if err != nil {
		t.Fatal(err)
	}
	wantHermes2 := "model: gemini-4-argon\nmcp_servers:\n  time:\n    command: \"uvx\"\n    args: [\"mcp-server-time\"]\n  nrp:\n    command: \"/opt/new/nrp-mcp\"\n    args: [\"serve\",\"--namespace\",\"ucr-lab\"]\ndisplay:\n  skin: default\n"
	if string(hNew2) != wantHermes2 {
		t.Fatalf("hermes second update:\ngot:\n%s\nwant:\n%s", hNew2, wantHermes2)
	}
}

func TestWindowsBackslashEscapingInJSON(t *testing.T) {
	home := t.TempDir()
	winBin := `C:\Users\jane\AppData\Local\Programs\nrp-mcp\nrp-mcp.exe`
	env := &Env{
		GOOS:    "windows",
		Home:    home,
		AppData: filepath.Join(home, "AppData", "Roaming"),
		BinPath: winBin,
	}
	res, err := env.Configure("claude-desktop")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `C:\\Users\\jane\\AppData\\Local\\Programs\\nrp-mcp\\nrp-mcp.exe`) {
		t.Fatalf("expected JSON-escaped Windows path, got:\n%s", b)
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("Windows JSON did not parse: %v", err)
	}
}

func TestUnknownClientRejected(t *testing.T) {
	env := &Env{GOOS: "darwin", Home: t.TempDir(), BinPath: "/opt/nrp/bin/nrp-mcp"}
	if _, err := env.Configure("nonexistent-client"); err == nil {
		t.Fatal("expected error for unknown client")
	}
}
