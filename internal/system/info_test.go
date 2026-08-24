package system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/policy"
)

func TestInfoReturnsOnlyBoundedDocumentedFacts(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS != "windows" {
		configuredRoot := filepath.Join(t.TempDir(), "root-link")
		if err := os.Symlink(root, configuredRoot); err != nil {
			t.Fatal(err)
		}
		root = configuredRoot
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	document := map[string]any{
		"version": 1,
		"mode":    "user-shell",
		"roots":   []map[string]string{{"name": "workspace", "path": root}},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := policy.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = roots.Close() })

	result, err := Info("1.2.3", roots, 4)
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerVersion != "1.2.3" || result.OS != runtime.GOOS || result.Architecture != runtime.GOARCH || result.Mode != config.UserShell {
		t.Fatalf("unexpected info: %+v", result)
	}
	if result.Hostname == "" || len(result.Hostname) > maxHostnameBytes {
		t.Fatalf("invalid hostname length: %d", len(result.Hostname))
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Roots) != 1 || result.Roots[0].Name != "workspace" || result.Roots[0].Path != canonicalRoot || !result.Roots[0].Readable || !result.Roots[0].Writable {
		t.Fatalf("unexpected roots: %+v", result.Roots)
	}
	if !result.CommandsEnabled || result.ManagedProcesses != 4 {
		t.Fatalf("unexpected command state: %+v", result)
	}
}
