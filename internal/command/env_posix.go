//go:build linux || darwin

package command

import (
	"os"
	"sort"
	"strings"
)

func baselineEnvironment() []string {
	names := []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "TERM"}
	result := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
			seen[name] = true
		}
	}
	lc := make([]string, 0)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "LC_") && !seen[name] {
			lc = append(lc, entry)
			seen[name] = true
		}
	}
	sort.Strings(lc)
	return append(result, lc...)
}
