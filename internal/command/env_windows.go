//go:build windows

package command

import "os"

func baselineEnvironment() []string {
	names := []string{
		"Path", "SystemRoot", "ComSpec", "PATHEXT", "TEMP", "TMP", "USERPROFILE",
		"APPDATA", "LOCALAPPDATA", "HOMEDRIVE", "HOMEPATH",
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}
