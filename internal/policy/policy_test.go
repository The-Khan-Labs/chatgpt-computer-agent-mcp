package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
)

func loadSet(t *testing.T, mode config.Mode, root string) *Set {
	t.Helper()
	doc := map[string]any{
		"version": 1,
		"mode":    string(mode),
		"roots": []any{
			map[string]any{"name": "workspace", "path": root},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	set, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close() })
	return set
}

func TestCapabilitiesAreNested(t *testing.T) {
	cases := []struct {
		mode config.Mode
		cap  Capability
		want bool
	}{
		{config.Readonly, SystemInfo, true},
		{config.Readonly, ReadFile, true},
		{config.Readonly, WriteFile, false},
		{config.Workspace, WriteFile, true},
		{config.Workspace, RunCommand, false},
		{config.UserShell, WriteFile, true},
		{config.UserShell, RunCommand, true},
		{config.UserShell, ProcessStop, true},
	}
	for _, tc := range cases {
		if got := Allows(tc.mode, tc.cap); got != tc.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", tc.mode, tc.cap, got, tc.want)
		}
	}
}

func TestOpenCanonicalizesRootAndReportsMode(t *testing.T) {
	configured := t.TempDir()
	if runtime.GOOS != "windows" {
		realRoot := configured
		configured = filepath.Join(t.TempDir(), "root-link")
		if err := os.Symlink(realRoot, configured); err != nil {
			t.Fatal(err)
		}
	}
	set := loadSet(t, config.Workspace, configured)
	roots := set.Roots()
	canonical, err := filepath.EvalSymlinks(configured)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].Name != "workspace" || roots[0].Path != canonical || !roots[0].Readable || !roots[0].Writable {
		t.Fatalf("roots = %#v", roots)
	}
	if set.Mode() != config.Workspace {
		t.Fatalf("mode = %q", set.Mode())
	}
}

func TestAuthorizeEnforcesModeAliasAndPortablePaths(t *testing.T) {
	set := loadSet(t, config.Readonly, t.TempDir())
	if _, err := set.Authorize(WriteFile, "workspace", "file.txt"); agenterr.CodeOf(err) != agenterr.ModeDenied {
		t.Fatalf("write error = %v", err)
	}
	if _, err := set.Authorize(ReadFile, "missing", "file.txt"); agenterr.CodeOf(err) != agenterr.RootNotFound {
		t.Fatalf("root error = %v", err)
	}

	bad := []string{"", "../x", "a/../x", "/etc/passwd", "/", `\\server\share`, "//server/share", `C:\\Windows`, "C:relative", "a\x00b"}
	if runtime.GOOS == "windows" {
		bad = append(bad, `a\..\x`, `\Windows`, `\Windows\System32`)
	}
	for _, path := range bad {
		if _, err := set.Authorize(ReadFile, "workspace", path); agenterr.CodeOf(err) != agenterr.PathDenied {
			t.Errorf("Authorize(%q) error = %v, code %q", path, err, agenterr.CodeOf(err))
		}
	}

	authorized, err := set.Authorize(ReadFile, "workspace", "dir/./file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Alias() != "workspace" || authorized.Path() != "dir/file.txt" {
		t.Fatalf("authorized = alias %q path %q", authorized.Alias(), authorized.Path())
	}
	if runtime.GOOS == "windows" {
		authorized, err = set.Authorize(ReadFile, "workspace", `dir\.\file.txt`)
		if err != nil || authorized.Path() != "dir/file.txt" {
			t.Fatalf("native separator path = %q, %v", authorized.Path(), err)
		}
	}
}

func TestAuthorizeUsesRootForSymlinkContainment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", filepath.Join(root, "internal")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	set := loadSet(t, config.Readonly, root)

	internal, err := set.Authorize(ReadFile, "workspace", "internal/ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := internal.OSRoot().Stat(internal.Path()); err != nil {
		t.Fatalf("internal link rejected: %v", err)
	}
	escape, err := set.Authorize(ReadFile, "workspace", "escape/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escape.OSRoot().Stat(escape.Path()); err == nil {
		t.Fatal("escaping link was followed")
	}
}

func TestAuthorizeDirectoryRequiresExistingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loadSet(t, config.UserShell, root)
	if _, err := set.AuthorizeDirectory(ProcessStart, "workspace", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := set.AuthorizeDirectory(RunCommand, "workspace", "file"); agenterr.CodeOf(err) != agenterr.NotDirectory {
		t.Fatalf("file cwd error = %v", err)
	}
	if _, err := set.AuthorizeDirectory(RunCommand, "workspace", "missing"); agenterr.CodeOf(err) != agenterr.NotFound {
		t.Fatalf("missing cwd error = %v", err)
	}
}

func TestCloseIsIdempotentAndClosesRoots(t *testing.T) {
	set := loadSet(t, config.Readonly, t.TempDir())
	authorized, err := set.Authorize(ReadFile, "workspace", ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := authorized.OSRoot().Stat("."); err == nil {
		t.Fatal("root remained open")
	}
}
