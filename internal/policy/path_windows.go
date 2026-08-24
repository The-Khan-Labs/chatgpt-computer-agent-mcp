//go:build windows

package policy

import (
	"path"
	"strings"
)

func pathParts(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' })
}

func cleanNative(name string) string {
	return path.Clean(strings.ReplaceAll(name, `\`, "/"))
}
