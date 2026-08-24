package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

type Spec struct {
	Executable        string
	Arguments         []string
	Directory         string
	DirectoryIdentity os.FileInfo
	Environment       []string
}

func revalidateDirectory(spec Spec) error {
	if spec.DirectoryIdentity == nil {
		return errors.New("working directory identity is required")
	}
	current, err := os.Stat(spec.Directory)
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(spec.DirectoryIdentity, current) {
		return errors.New("working directory changed after authorization")
	}
	return nil
}

type Exit struct {
	ExitCode    *int
	Termination *string
}

type OwnedProcess interface {
	Wait() (Exit, error)
	RequestGracefulStop()
	GracefulStop() error
	HardStop() error
	Close() error
}

func resolveExecutable(spec Spec) (string, error) {
	executable := spec.Executable
	if !filepath.IsAbs(executable) && hasPathSeparator(executable) {
		executable = filepath.Join(spec.Directory, executable)
	}
	return exec.LookPath(executable)
}

func hasPathSeparator(name string) bool {
	for i := range len(name) {
		if os.IsPathSeparator(name[i]) {
			return true
		}
	}
	return false
}
