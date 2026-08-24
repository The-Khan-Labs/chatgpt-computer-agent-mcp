package policy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
)

type Capability string

const (
	SystemInfo      Capability = "system_info"
	ReadFile        Capability = "read_file"
	ListDirectory   Capability = "list_directory"
	FileInfo        Capability = "file_info"
	CreateDirectory Capability = "create_directory"
	WriteFile       Capability = "write_file"
	EditFile        Capability = "edit_file"
	RunCommand      Capability = "run_command"
	ProcessStart    Capability = "process_start"
	ProcessStatus   Capability = "process_status"
	ProcessOutput   Capability = "process_output"
	ProcessStop     Capability = "process_stop"
)

type root struct {
	name      string
	canonical string
	handle    *os.Root
}

type RootSummary struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Readable bool   `json:"readable"`
	Writable bool   `json:"writable"`
}

type Set struct {
	mu     sync.RWMutex
	mode   config.Mode
	roots  map[string]*root
	order  []string
	closed bool
}

type Authorized struct {
	root *root
	path string
	info os.FileInfo
}

func Allows(mode config.Mode, capability Capability) bool {
	switch capability {
	case SystemInfo, ReadFile, ListDirectory, FileInfo:
		return mode == config.Readonly || mode == config.Workspace || mode == config.UserShell
	case CreateDirectory, WriteFile, EditFile:
		return mode == config.Workspace || mode == config.UserShell
	case RunCommand, ProcessStart, ProcessStatus, ProcessOutput, ProcessStop:
		return mode == config.UserShell
	default:
		return false
	}
}

func Open(cfg *config.Config) (*Set, error) {
	set := &Set{mode: cfg.Mode(), roots: make(map[string]*root)}
	for _, configured := range cfg.Roots() {
		absolute, err := filepath.Abs(configured.Path)
		if err != nil {
			_ = set.Close()
			return nil, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("cannot make root %q absolute", configured.Name), err)
		}
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			_ = set.Close()
			return nil, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("cannot resolve root %q", configured.Name), err)
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			_ = set.Close()
			return nil, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("root %q must be an existing directory", configured.Name), err)
		}
		handle, err := os.OpenRoot(canonical)
		if err != nil {
			_ = set.Close()
			return nil, agenterr.Wrap(agenterr.PermissionDenied, fmt.Sprintf("cannot open root %q", configured.Name), err)
		}
		set.roots[configured.Name] = &root{name: configured.Name, canonical: canonical, handle: handle}
		set.order = append(set.order, configured.Name)
	}
	return set, nil
}

func (s *Set) Mode() config.Mode { return s.mode }

func (s *Set) Roots() []RootSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]RootSummary, 0, len(s.order))
	for _, name := range s.order {
		root := s.roots[name]
		result = append(result, RootSummary{Name: name, Path: root.canonical, Readable: true, Writable: s.mode != config.Readonly})
	}
	return result
}

func (s *Set) Authorize(capability Capability, alias, name string) (Authorized, error) {
	if !Allows(s.mode, capability) {
		return Authorized{}, agenterr.New(agenterr.ModeDenied, fmt.Sprintf("%s is not allowed in %s mode", capability, s.mode))
	}
	clean, err := cleanPath(name)
	if err != nil {
		return Authorized{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Authorized{}, agenterr.New(agenterr.InternalError, "approved roots are closed")
	}
	root := s.roots[alias]
	if root == nil {
		return Authorized{}, agenterr.New(agenterr.RootNotFound, fmt.Sprintf("unknown root %q", alias))
	}
	return Authorized{root: root, path: clean}, nil
}

func (s *Set) AuthorizeDirectory(capability Capability, alias, name string) (Authorized, error) {
	authorized, err := s.Authorize(capability, alias, name)
	if err != nil {
		return Authorized{}, err
	}
	info, err := authorized.root.handle.Stat(authorized.path)
	if err != nil {
		return Authorized{}, mapFilesystemError("working directory", err)
	}
	if !info.IsDir() {
		return Authorized{}, agenterr.New(agenterr.NotDirectory, "working directory is not a directory")
	}
	authorized.info = info
	return authorized, nil
}

func (a Authorized) Alias() string    { return a.root.name }
func (a Authorized) Path() string     { return a.path }
func (a Authorized) OSRoot() *os.Root { return a.root.handle }
func (a Authorized) DirectoryIdentity() os.FileInfo {
	return a.info
}
func (a Authorized) NativePath() string {
	if a.path == "." {
		return a.root.canonical
	}
	return filepath.Join(a.root.canonical, filepath.FromSlash(a.path))
}

func (s *Set) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []error
	for _, root := range s.roots {
		errs = append(errs, root.handle.Close())
	}
	return errors.Join(errs...)
}

func mapFilesystemError(subject string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return agenterr.Wrap(agenterr.NotFound, subject+" does not exist", err)
	}
	if errors.Is(err, os.ErrPermission) {
		return agenterr.Wrap(agenterr.PermissionDenied, "permission denied for "+subject, err)
	}
	return agenterr.Wrap(agenterr.PathDenied, "cannot access "+subject, err)
}
