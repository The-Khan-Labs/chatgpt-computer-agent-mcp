package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

func writeConfig(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsPath(text, path string) bool {
	return strings.Contains(text, path) || strings.Contains(text, strconv.Quote(path))
}

func validDocument(root string) map[string]any {
	return map[string]any{
		"version": 1,
		"mode":    "readonly",
		"roots": []any{
			map[string]any{"name": "workspace", "path": root},
		},
	}
}

func TestLoadAppliesDocumentedDefaults(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(writeConfig(t, validDocument(root)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode() != Readonly {
		t.Fatalf("unexpected mode: %q", cfg.Mode())
	}
	if got := cfg.Roots(); len(got) != 1 || got[0] != (Root{Name: "workspace", Path: root}) {
		t.Fatalf("roots = %#v", got)
	}
	want := Limits{
		MaxReadBytes:                 1_048_576,
		MaxWriteBytes:                2_097_152,
		DefaultCommandTimeoutSeconds: 120,
		MaxCommandTimeoutSeconds:     600,
		MaxOutputBytesPerStream:      1_048_576,
		MaxBackgroundProcesses:       8,
		ProcessStopGraceSeconds:      2,
	}
	if got := cfg.Limits(); got != want {
		t.Fatalf("limits = %#v, want %#v", got, want)
	}

	roots := cfg.Roots()
	roots[0].Name = "changed"
	if cfg.Roots()[0].Name != "workspace" {
		t.Fatal("Roots returned mutable configuration storage")
	}
}

func TestLoadAcceptsExplicitLimits(t *testing.T) {
	doc := validDocument(t.TempDir())
	doc["mode"] = "user-shell"
	doc["limits"] = map[string]any{
		"max_read_bytes":                  1,
		"max_write_bytes":                 2,
		"default_command_timeout_seconds": 3,
		"max_command_timeout_seconds":     4,
		"max_output_bytes_per_stream":     5,
		"max_background_processes":        6,
		"process_stop_grace_seconds":      7,
	}
	cfg, err := Load(writeConfig(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode() != UserShell || cfg.Limits().MaxCommandTimeoutSeconds != 4 {
		t.Fatalf("config = mode %q limits %#v", cfg.Mode(), cfg.Limits())
	}
}

func TestLoadRejectsInvalidDocuments(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.ToSlash(root)
	cases := map[string]string{
		"unknown top-level field": `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"extra":true}`,
		"unknown root field":      `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `","extra":true}]}`,
		"unknown limits field":    `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"extra":1}}`,
		"wrong version":           `{"version":2,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}]}`,
		"missing mode":            `{"version":1,"roots":[{"name":"r","path":"` + absolute + `"}]}`,
		"unknown mode":            `{"version":1,"mode":"admin","roots":[{"name":"r","path":"` + absolute + `"}]}`,
		"missing roots":           `{"version":1,"mode":"readonly","roots":[]}`,
		"invalid root name":       `{"version":1,"mode":"readonly","roots":[{"name":"1bad","path":"` + absolute + `"}]}`,
		"long root name":          `{"version":1,"mode":"readonly","roots":[{"name":"` + strings.Repeat("a", 33) + `","path":"` + absolute + `"}]}`,
		"duplicate root name":     `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"},{"name":"r","path":"` + absolute + `"}]}`,
		"relative root":           `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"relative"}]}`,
		"zero read cap":           `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_read_bytes":0}}`,
		"large read cap":          `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_read_bytes":8388609}}`,
		"large write cap":         `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_write_bytes":8388609}}`,
		"zero default timeout":    `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"default_command_timeout_seconds":0}}`,
		"large default timeout":   `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"default_command_timeout_seconds":3601}}`,
		"zero max timeout":        `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_command_timeout_seconds":0}}`,
		"large max timeout":       `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_command_timeout_seconds":3601}}`,
		"default exceeds max":     `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"default_command_timeout_seconds":11,"max_command_timeout_seconds":10}}`,
		"large output cap":        `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_output_bytes_per_stream":8388609}}`,
		"zero process capacity":   `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_background_processes":0}}`,
		"large process capacity":  `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"max_background_processes":33}}`,
		"zero stop grace":         `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"process_stop_grace_seconds":0}}`,
		"large stop grace":        `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}],"limits":{"process_stop_grace_seconds":31}}`,
		"second JSON document":    `{"version":1,"mode":"readonly","roots":[{"name":"r","path":"` + absolute + `"}]} {}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if agenterr.CodeOf(err) != agenterr.InvalidInput {
				t.Fatalf("error = %v, code = %q", err, agenterr.CodeOf(err))
			}
		})
	}
}

func TestLoadReportsMissingFileWithoutLeakingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	_, err := Load(path)
	if err == nil || !containsPath(err.Error(), path) {
		t.Fatalf("error = %v", err)
	}
}

func TestDefaultPathUsesPlatformConfigDirectory(t *testing.T) {
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
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}
