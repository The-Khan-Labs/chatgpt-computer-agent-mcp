package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"chatgpt-computer-agent-mcp/internal/config"
)

const userShellWarning = "user-shell allows arbitrary commands as your current OS user"

func runConfigureCommand(t *testing.T, arguments ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"configure"}, arguments...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestConfigureCommandFirstRun(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	code, stdout, stderr := runConfigureCommand(t, "--config", path, "--mode", "readonly", "--root", root)
	if code != 0 {
		t.Fatalf("code = %d stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"Configured ChatGPT Computer Agent MCP",
		"Mode: readonly",
		"Approved workspace: " + root,
		"Config: " + path,
		"Restart tunnel-client for permission changes to take effect.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, missing %q", stdout, want)
		}
	}
	if strings.Contains(stdout, userShellWarning) {
		t.Fatalf("readonly output contains the user-shell warning: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode() != config.Readonly {
		t.Fatalf("mode = %q", cfg.Mode())
	}
}

func TestConfigureCommandPrintsUserShellWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	code, stdout, stderr := runConfigureCommand(t, "--config", path, "--mode", "user-shell", "--root", t.TempDir())
	if code != 0 {
		t.Fatalf("code = %d stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, userShellWarning) {
		t.Fatalf("stdout = %q, missing user-shell warning", stdout)
	}
	if !strings.Contains(stdout, "do not sandbox commands") {
		t.Fatalf("stdout = %q, missing sandbox caveat", stdout)
	}
}

func TestConfigureCommandModeOnlyUpdatePreservesWorkspace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	if code, _, stderr := runConfigureCommand(t, "--config", path, "--mode", "readonly", "--root", root); code != 0 {
		t.Fatalf("first run failed: %q", stderr)
	}
	code, stdout, stderr := runConfigureCommand(t, "--config", path, "--mode", "workspace")
	if code != 0 {
		t.Fatalf("code = %d stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Mode: workspace") || !strings.Contains(stdout, "Approved workspace: "+root) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestConfigureCommandRejectsInvalidArguments(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	for name, test := range map[string]struct {
		arguments []string
		wantCode  int
		wantError string
	}{
		"invalid mode":   {[]string{"--config", path, "--mode", "admin", "--root", root}, 2, "invalid mode"},
		"missing mode":   {[]string{"--config", path, "--root", root}, 2, "--mode is required"},
		"positional":     {[]string{"--config", path, "--mode", "readonly", "extra"}, 2, "unexpected positional"},
		"relative root":  {[]string{"--config", path, "--mode", "readonly", "--root", "relative/dir"}, 1, "must be an absolute path"},
		"missing root":   {[]string{"--config", path, "--mode", "readonly", "--root", filepath.Join(root, "missing")}, 1, "existing directory"},
		"first run bare": {[]string{"--config", path, "--mode", "readonly"}, 1, "--root is required"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runConfigureCommand(t, test.arguments...)
			if code != test.wantCode {
				t.Fatalf("code = %d, want %d (stderr = %q)", code, test.wantCode, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q", stdout)
			}
			if !strings.Contains(stderr, test.wantError) {
				t.Fatalf("stderr = %q, missing %q", stderr, test.wantError)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("failed configure created a file: %v", err)
			}
		})
	}
}

func TestConfigureCommandUsesPlatformDefaultPath(t *testing.T) {
	var want string
	switch runtime.GOOS {
	case "linux":
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
		want = filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "chatgpt-computer-agent-mcp", "config.json")
	case "darwin":
		t.Setenv("HOME", t.TempDir())
		want = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "ChatGPT Computer Agent MCP", "config.json")
	case "windows":
		t.Setenv("APPDATA", filepath.Join(t.TempDir(), "AppData", "Roaming"))
		want = filepath.Join(os.Getenv("APPDATA"), "ChatGPTComputerAgentMCP", "config.json")
	default:
		t.Skip("unsupported target")
	}
	code, stdout, stderr := runConfigureCommand(t, "--mode", "readonly", "--root", t.TempDir())
	if code != 0 {
		t.Fatalf("code = %d stderr = %q", code, stderr)
	}
	if !containsPath(stdout, want) {
		t.Fatalf("stdout = %q, missing %q", stdout, want)
	}
	if _, err := config.Load(want); err != nil {
		t.Fatalf("default-path configuration does not load: %v", err)
	}
}

func TestConfigureCommandHelp(t *testing.T) {
	code, stdout, stderr := runConfigureCommand(t, "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Usage: computer-agent configure") {
		t.Fatalf("stdout = %q", stdout)
	}
}
