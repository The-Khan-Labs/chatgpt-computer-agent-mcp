package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

// WorkspaceRootName is the root alias managed by Configure.
const WorkspaceRootName = "workspace"

// ConfigureResult describes what Configure wrote.
type ConfigureResult struct {
	Config     *Config
	Created    bool
	BackupPath string
}

// Configure creates the configuration at path when none exists, or updates
// only the permission mode (and optionally the "workspace" root) of an
// existing one. All other roots and every explicit limit are preserved.
// The final write is atomic, and an existing file is backed up first.
func Configure(path string, mode Mode, workspaceRoot string) (*ConfigureResult, error) {
	if mode != Readonly && mode != Workspace && mode != UserShell {
		return nil, agenterr.New(agenterr.InvalidInput, fmt.Sprintf("invalid mode %q; supported modes are readonly, workspace, and user-shell", mode))
	}
	if workspaceRoot != "" {
		workspaceRoot = filepath.Clean(workspaceRoot)
		if err := checkWorkspaceRoot(workspaceRoot); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return createConfiguration(path, mode, workspaceRoot)
	}
	previous, err := readVerified(path)
	if err != nil {
		return nil, err
	}
	return updateConfiguration(path, previous, mode, workspaceRoot)
}

func checkWorkspaceRoot(root string) error {
	if !filepath.IsAbs(root) {
		return agenterr.New(agenterr.InvalidInput, fmt.Sprintf("root %q must be an absolute path", root))
	}
	info, err := os.Stat(root)
	if err != nil {
		return agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("root %q must be an existing directory", root), err)
	}
	if !info.IsDir() {
		return agenterr.New(agenterr.InvalidInput, fmt.Sprintf("root %q must be a directory", root))
	}
	return nil
}

func createConfiguration(path string, mode Mode, workspaceRoot string) (*ConfigureResult, error) {
	if workspaceRoot == "" {
		return nil, agenterr.New(agenterr.InvalidInput, fmt.Sprintf("no configuration exists at %q; --root is required for first-time configuration", path))
	}
	doc := document{
		Version: 1,
		Mode:    mode,
		Roots:   []rootDocument{{Name: WorkspaceRootName, Path: workspaceRoot}},
	}
	cfg, err := validate(path, doc)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, agenterr.Wrap(agenterr.InternalError, fmt.Sprintf("cannot create configuration directory for %q", path), err)
	}
	if err := writeDocumentAtomically(path, doc); err != nil {
		return nil, err
	}
	return &ConfigureResult{Config: cfg, Created: true}, nil
}

func updateConfiguration(path string, previous []byte, mode Mode, workspaceRoot string) (*ConfigureResult, error) {
	doc, err := decodeDocument(path, previous)
	if err != nil {
		return nil, err
	}
	if _, err := validate(path, doc); err != nil {
		return nil, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("existing configuration %q is invalid (%v); fix or remove it before configuring", path, err), err)
	}
	doc.Mode = mode
	if workspaceRoot != "" {
		updated := false
		for i := range doc.Roots {
			if doc.Roots[i].Name == WorkspaceRootName {
				doc.Roots[i].Path = workspaceRoot
				updated = true
				break
			}
		}
		if !updated {
			doc.Roots = append(doc.Roots, rootDocument{Name: WorkspaceRootName, Path: workspaceRoot})
		}
	}
	cfg, err := validate(path, doc)
	if err != nil {
		return nil, err
	}
	backup := path + ".bak"
	if err := writeFileAtomically(backup, previous); err != nil {
		return nil, err
	}
	if err := writeDocumentAtomically(path, doc); err != nil {
		return nil, err
	}
	return &ConfigureResult{Config: cfg, BackupPath: backup}, nil
}

func writeDocumentAtomically(path string, doc document) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return agenterr.Wrap(agenterr.InternalError, fmt.Sprintf("cannot encode configuration %q", path), err)
	}
	return writeFileAtomically(path, append(data, '\n'))
}

// writeFileAtomically stages the content in a same-directory temporary file
// (created 0600) and renames it into place, so the destination is never
// truncated by a failed write.
func writeFileAtomically(path string, data []byte) error {
	staged, err := os.CreateTemp(filepath.Dir(path), ".config-write-*")
	if err != nil {
		return agenterr.Wrap(agenterr.InternalError, fmt.Sprintf("cannot stage configuration %q", path), err)
	}
	defer func() { _ = os.Remove(staged.Name()) }()
	wrap := func(err error) error {
		_ = staged.Close()
		return agenterr.Wrap(agenterr.InternalError, fmt.Sprintf("cannot write configuration %q", path), err)
	}
	if _, err := staged.Write(data); err != nil {
		return wrap(err)
	}
	if err := staged.Sync(); err != nil {
		return wrap(err)
	}
	if err := staged.Close(); err != nil {
		return agenterr.Wrap(agenterr.InternalError, fmt.Sprintf("cannot write configuration %q", path), err)
	}
	if err := os.Rename(staged.Name(), path); err != nil {
		return agenterr.Wrap(agenterr.InternalError, fmt.Sprintf("cannot replace configuration %q", path), err)
	}
	return nil
}
