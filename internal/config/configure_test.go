package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

func configurePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "config.json")
}

func readRawDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("configuration is not valid JSON: %v", err)
	}
	return raw
}

func TestConfigureFirstTimeCreatesEachMode(t *testing.T) {
	for _, mode := range []Mode{Readonly, Workspace, UserShell} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			path := configurePath(t)
			result, err := Configure(path, mode, root)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Created || result.BackupPath != "" {
				t.Fatalf("result = %#v", result)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("generated configuration does not load: %v", err)
			}
			if cfg.Mode() != mode {
				t.Fatalf("mode = %q, want %q", cfg.Mode(), mode)
			}
			if roots := cfg.Roots(); len(roots) != 1 || roots[0] != (Root{Name: "workspace", Path: root}) {
				t.Fatalf("roots = %#v", roots)
			}
			if cfg.Limits() != defaultLimits {
				t.Fatalf("limits = %#v, want defaults %#v", cfg.Limits(), defaultLimits)
			}
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Fatalf("first-time configuration wrote a backup: %v", err)
			}
			if _, materialized := readRawDocument(t, path)["limits"]; materialized {
				t.Fatal("first-time configuration materialized explicit limits")
			}
		})
	}
}

func TestConfigureFirstTimeRequiresRoot(t *testing.T) {
	path := configurePath(t)
	_, err := Configure(path, Readonly, "")
	if agenterr.CodeOf(err) != agenterr.InvalidInput {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("failed configure created a file")
	}
}

func TestConfigureRejectsInvalidMode(t *testing.T) {
	for _, mode := range []Mode{"", "admin", "Readonly", "user_shell"} {
		if _, err := Configure(configurePath(t), mode, t.TempDir()); agenterr.CodeOf(err) != agenterr.InvalidInput {
			t.Fatalf("mode %q: error = %v", mode, err)
		}
	}
}

func TestConfigureRejectsBadRoots(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{
		"relative":    "relative/dir",
		"nonexistent": filepath.Join(t.TempDir(), "missing"),
		"not a dir":   file,
	} {
		t.Run(name, func(t *testing.T) {
			path := configurePath(t)
			if _, err := Configure(path, Readonly, root); agenterr.CodeOf(err) != agenterr.InvalidInput {
				t.Fatalf("error = %v", err)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatal("failed configure created a file")
			}
		})
	}
}

func writeAdvancedConfig(t *testing.T, workspaceRoot, otherRoot string) string {
	t.Helper()
	return writeConfig(t, map[string]any{
		"version": 1,
		"mode":    "readonly",
		"roots": []any{
			map[string]any{"name": "docs", "path": otherRoot},
			map[string]any{"name": "workspace", "path": workspaceRoot},
		},
		"limits": map[string]any{
			"max_read_bytes":              4096,
			"max_background_processes":    3,
			"max_output_bytes_per_stream": 2048,
		},
	})
}

func TestConfigureModeChangePreservesRootsAndLimits(t *testing.T) {
	workspaceRoot, otherRoot := t.TempDir(), t.TempDir()
	path := writeAdvancedConfig(t, workspaceRoot, otherRoot)
	result, err := Configure(path, UserShell, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Created {
		t.Fatal("update reported first-time creation")
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode() != UserShell {
		t.Fatalf("mode = %q", cfg.Mode())
	}
	wantRoots := []Root{{Name: "docs", Path: otherRoot}, {Name: "workspace", Path: workspaceRoot}}
	if roots := cfg.Roots(); len(roots) != 2 || roots[0] != wantRoots[0] || roots[1] != wantRoots[1] {
		t.Fatalf("roots = %#v, want %#v", roots, wantRoots)
	}
	limits := cfg.Limits()
	if limits.MaxReadBytes != 4096 || limits.MaxBackgroundProcesses != 3 || limits.MaxOutputBytesPerStream != 2048 {
		t.Fatalf("custom limits were not preserved: %#v", limits)
	}
	if limits.MaxWriteBytes != defaultLimits.MaxWriteBytes {
		t.Fatalf("default limits changed: %#v", limits)
	}
	rawLimits, ok := readRawDocument(t, path)["limits"].(map[string]any)
	if !ok || len(rawLimits) != 3 {
		t.Fatalf("rewritten limits materialized or dropped fields: %#v", rawLimits)
	}
}

func TestConfigureUpdatesWorkspaceRootPreservingOthers(t *testing.T) {
	otherRoot := t.TempDir()
	path := writeAdvancedConfig(t, t.TempDir(), otherRoot)
	newWorkspace := t.TempDir()
	if _, err := Configure(path, Workspace, newWorkspace); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	wantRoots := []Root{{Name: "docs", Path: otherRoot}, {Name: "workspace", Path: newWorkspace}}
	if roots := cfg.Roots(); len(roots) != 2 || roots[0] != wantRoots[0] || roots[1] != wantRoots[1] {
		t.Fatalf("roots = %#v, want %#v", roots, wantRoots)
	}
	if cfg.Limits().MaxReadBytes != 4096 {
		t.Fatalf("limits were not preserved: %#v", cfg.Limits())
	}
}

func TestConfigureAddsWorkspaceRootWhenMissing(t *testing.T) {
	otherRoot := t.TempDir()
	path := writeConfig(t, map[string]any{
		"version": 1,
		"mode":    "readonly",
		"roots":   []any{map[string]any{"name": "docs", "path": otherRoot}},
	})
	newWorkspace := t.TempDir()
	if _, err := Configure(path, Workspace, newWorkspace); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	wantRoots := []Root{{Name: "docs", Path: otherRoot}, {Name: "workspace", Path: newWorkspace}}
	if roots := cfg.Roots(); len(roots) != 2 || roots[0] != wantRoots[0] || roots[1] != wantRoots[1] {
		t.Fatalf("roots = %#v, want %#v", roots, wantRoots)
	}
}

func TestConfigureIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := configurePath(t)
	if _, err := Configure(path, UserShell, root); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Configure(path, UserShell, root)
	if err != nil {
		t.Fatalf("repeated configure failed: %v", err)
	}
	if result.Created {
		t.Fatal("repeated configure reported first-time creation")
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("repeated configure changed the file:\nfirst  = %s\nsecond = %s", first, second)
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, first) {
		t.Fatalf("backup does not preserve the previous configuration:\nbackup = %s\nwant   = %s", backup, first)
	}
}

func TestConfigureBackupPreservesPreviousConfiguration(t *testing.T) {
	path := writeAdvancedConfig(t, t.TempDir(), t.TempDir())
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Configure(path, UserShell, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath != path+".bak" {
		t.Fatalf("backup path = %q", result.BackupPath)
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup = %s, want original %s", backup, original)
	}
}

func TestConfigureFailedUpdateLeavesPreviousConfigIntact(t *testing.T) {
	path := writeAdvancedConfig(t, t.TempDir(), t.TempDir())
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, attempt := range map[string]func() error{
		"nonexistent root": func() error {
			_, err := Configure(path, Workspace, filepath.Join(t.TempDir(), "missing"))
			return err
		},
		"invalid mode": func() error {
			_, err := Configure(path, "admin", "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := attempt(); agenterr.CodeOf(err) != agenterr.InvalidInput {
				t.Fatalf("error = %v", err)
			}
			current, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(current, original) {
				t.Fatalf("failed configure modified the file:\ncurrent = %s\nwant    = %s", current, original)
			}
		})
	}
}

func TestConfigureRefusesToRewriteInvalidExistingConfiguration(t *testing.T) {
	path := writeConfig(t, map[string]any{"version": 2, "mode": "readonly", "roots": []any{}})
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Configure(path, Readonly, t.TempDir()); agenterr.CodeOf(err) != agenterr.InvalidInput {
		t.Fatalf("error = %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, original) {
		t.Fatal("configure rewrote an invalid configuration")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("configure backed up an invalid configuration")
	}
}
