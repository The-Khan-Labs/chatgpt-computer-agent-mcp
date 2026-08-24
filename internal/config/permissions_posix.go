//go:build !windows

package config

import (
	"fmt"
	"os"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

func checkPermissions(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o022 != 0 {
		return agenterr.New(agenterr.PermissionDenied, fmt.Sprintf("configuration %q must not be group or world writable", path))
	}
	return nil
}
