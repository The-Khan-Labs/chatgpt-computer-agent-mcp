//go:build windows

package policy

import (
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
)

func TestAuthorizeRejectsWindowsRootedAndPortableAbsolutePaths(t *testing.T) {
	set := loadSet(t, config.Readonly, t.TempDir())
	bad := []string{
		"/etc/passwd",
		"/",
		`\Windows`,
		`\Windows\System32`,
		`\\server\share`,
		"//server/share",
		`C:\Windows`,
		"C:relative",
		`a\..\x`,
		"..",
	}
	for _, path := range bad {
		if _, err := set.Authorize(ReadFile, "workspace", path); agenterr.CodeOf(err) != agenterr.PathDenied {
			t.Errorf("Authorize(%q) error = %v, code %q", path, err, agenterr.CodeOf(err))
		}
	}

	authorized, err := set.Authorize(ReadFile, "workspace", `dir\.\file.txt`)
	if err != nil || authorized.Path() != "dir/file.txt" {
		t.Fatalf("in-root native relative path = %q, %v", authorized.Path(), err)
	}
	authorized, err = set.Authorize(ReadFile, "workspace", "dir/file.txt")
	if err != nil || authorized.Path() != "dir/file.txt" {
		t.Fatalf("in-root slash relative path = %q, %v", authorized.Path(), err)
	}
}
