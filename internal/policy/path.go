package policy

import (
	"path/filepath"
	"strings"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

func cleanPath(name string) (string, error) {
	if name == "" || len(name) > 4096 || strings.IndexByte(name, 0) >= 0 {
		return "", agenterr.New(agenterr.PathDenied, "path must contain 1 to 4096 bytes and no NUL")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || filepath.IsAbs(name) || filepath.VolumeName(name) != "" || isDrivePath(name) {
		return "", agenterr.New(agenterr.PathDenied, "absolute, UNC, and volume-qualified paths are not allowed")
	}
	for _, part := range pathParts(name) {
		if part == ".." {
			return "", agenterr.New(agenterr.PathDenied, "parent path traversal is not allowed")
		}
	}
	clean := cleanNative(name)
	if strings.HasPrefix(clean, "/") {
		return "", agenterr.New(agenterr.PathDenied, "absolute, UNC, and volume-qualified paths are not allowed")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", agenterr.New(agenterr.PathDenied, "path escapes its approved root")
	}
	return clean, nil
}

func isDrivePath(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':'
}
