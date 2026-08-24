//go:build windows

package files

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
)

func TestReadFileRejectsWindowsReservedDeviceNames(t *testing.T) {
	service, _ := newService(t, config.Readonly, 64, 64)
	for _, name := range []string{"NUL", "CON", "COM1", "CONOUT$"} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Read(ReadRequest{Root: "workspace", Path: name})
			assertCode(t, err, agenterr.PathDenied)
		})
	}
}

func TestReadFileRejectsEscapingWindowsJunction(t *testing.T) {
	service, root := newService(t, config.Readonly, 64, 64)
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "outside.txt"), []byte("outside"), 0o600)
	junction := filepath.Join(root, "escape")
	command := exec.Command(os.Getenv("ComSpec"), "/d", "/c", "mklink", "/J", junction, outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, output)
	}
	_, err := service.Read(ReadRequest{Root: "workspace", Path: `escape\outside.txt`})
	assertCode(t, err, agenterr.PathDenied)
}
