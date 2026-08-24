//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

func assertOwnerOnly(t *testing.T, path string, wantDir bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() != wantDir {
		t.Fatalf("%q directory = %v, want %v", path, info.IsDir(), wantDir)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("%q permissions = %o, want no group/other access", path, perm)
	}
}

func TestConfigureCreatesOwnerOnlyFileAndDirectory(t *testing.T) {
	path := configurePath(t)
	if _, err := Configure(path, Readonly, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, path, false)
	assertOwnerOnly(t, filepath.Dir(path), true)
}

func TestConfigureWritesOwnerOnlyBackupAndUpdate(t *testing.T) {
	path := configurePath(t)
	if _, err := Configure(path, Readonly, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	result, err := Configure(path, Workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, path, false)
	assertOwnerOnly(t, result.BackupPath, false)
}

func TestConfigureRejectsGroupWritableExistingConfiguration(t *testing.T) {
	path := writeConfig(t, validDocument(t.TempDir()))
	if err := os.Chmod(path, 0o660); err != nil {
		t.Fatal(err)
	}
	if _, err := Configure(path, Workspace, ""); agenterr.CodeOf(err) != agenterr.PermissionDenied {
		t.Fatalf("error = %v", err)
	}
}
