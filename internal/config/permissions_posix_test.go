//go:build !windows

package config

import (
	"os"
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

func TestLoadRejectsGroupOrWorldWritableConfig(t *testing.T) {
	path := writeConfig(t, validDocument(t.TempDir()))
	if err := os.Chmod(path, 0o620); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if agenterr.CodeOf(err) != agenterr.PermissionDenied {
		t.Fatalf("error = %v, code = %q", err, agenterr.CodeOf(err))
	}
}
