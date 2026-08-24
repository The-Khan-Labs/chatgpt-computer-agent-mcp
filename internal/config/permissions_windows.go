//go:build windows

package config

import "os"

func checkPermissions(_ string, _ os.FileInfo) error {
	return nil
}
