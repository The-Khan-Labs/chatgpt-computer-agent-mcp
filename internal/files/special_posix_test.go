//go:build !windows

package files

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
)

func TestReadFileRejectsFIFOWithoutBlocking(t *testing.T) {
	service, root := newService(t, config.Readonly, 64, 64)
	path := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	_, err := service.Read(ReadRequest{Root: "workspace", Path: "pipe"})
	assertCode(t, err, agenterr.NotFile)
	if info, statErr := os.Lstat(path); statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("fixture is not a FIFO: %v %v", info, statErr)
	}
}

func TestFileOperationsMapPermissionErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses ordinary permission checks")
	}
	service, root := newService(t, config.Workspace, 64, 64)
	file := filepath.Join(root, "unreadable.txt")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o600) })
	_, err := service.Read(ReadRequest{Root: "workspace", Path: "unreadable.txt"})
	assertCode(t, err, agenterr.PermissionDenied)

	directory := filepath.Join(root, "unwritable")
	if err := os.Mkdir(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	_, err = service.Write(WriteRequest{Root: "workspace", Path: "unwritable/file.txt", Content: "content"})
	assertCode(t, err, agenterr.PermissionDenied)
}
