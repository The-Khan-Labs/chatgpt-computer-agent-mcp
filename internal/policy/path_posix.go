//go:build !windows

package policy

import (
	"path"
	"strings"
)

func pathParts(name string) []string { return strings.Split(name, "/") }
func cleanNative(name string) string { return path.Clean(name) }
