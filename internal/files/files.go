package files

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/policy"
)

const (
	defaultListLimit = 100
	maxListLimit     = 200
	maxEditTextBytes = 512 << 10
	tempPrefix       = ".computer-agent-"
)

type Type string

const (
	TypeFile      Type = "file"
	TypeDirectory Type = "directory"
	TypeSymlink   Type = "symlink"
	TypeOther     Type = "other"
)

type Service struct {
	policy   *policy.Set
	maxRead  int
	maxWrite int
	// ponytail: one lock hides temporary publication files from sibling file tools;
	// split it only if measured file-operation contention warrants the complexity.
	mu sync.Mutex
}

type ReadRequest struct {
	Root     string `json:"root"`
	Path     string `json:"path"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

type ReadResult struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type ListRequest struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type Entry struct {
	Name       string `json:"name"`
	Type       Type   `json:"type"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

type ListResult struct {
	Root       string  `json:"root"`
	Path       string  `json:"path"`
	Entries    []Entry `json:"entries"`
	Offset     int     `json:"offset"`
	NextOffset *int    `json:"next_offset"`
	HasMore    bool    `json:"has_more"`
}

type InfoRequest struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

type InfoResult struct {
	Root       string  `json:"root"`
	Path       string  `json:"path"`
	Name       string  `json:"name"`
	Type       Type    `json:"type"`
	Size       int64   `json:"size"`
	Mode       string  `json:"mode"`
	ModifiedAt string  `json:"modified_at"`
	LinkTarget *string `json:"link_target"`
}

type CreateDirectoryRequest struct {
	Root          string `json:"root"`
	Path          string `json:"path"`
	CreateParents bool   `json:"create_parents,omitempty"`
}

type CreateDirectoryResult struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

type WriteRequest struct {
	Root          string `json:"root"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	Overwrite     bool   `json:"overwrite,omitempty"`
	CreateParents bool   `json:"create_parents,omitempty"`
}

type WriteResult struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	SHA256  string `json:"sha256"`
	Created bool   `json:"created"`
}

type EditRequest struct {
	Root    string `json:"root"`
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type EditResult struct {
	Root         string `json:"root"`
	Path         string `json:"path"`
	Bytes        int    `json:"bytes"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
}

func New(approved *policy.Set, limits config.Limits) *Service {
	return &Service{policy: approved, maxRead: limits.MaxReadBytes, maxWrite: limits.MaxWriteBytes}
}

func (s *Service) Read(request ReadRequest) (ReadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := request.MaxBytes
	if limit == 0 {
		limit = s.maxRead
	}
	if limit < 1 || limit > s.maxRead {
		return ReadResult{}, agenterr.New(agenterr.InvalidInput, fmt.Sprintf("max_bytes must be between 1 and %d", s.maxRead))
	}
	authorized, err := s.policy.Authorize(policy.ReadFile, request.Root, request.Path)
	if err != nil {
		return ReadResult{}, err
	}
	data, _, err := readRegular(authorized, limit)
	if err != nil {
		return ReadResult{}, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return ReadResult{}, agenterr.New(agenterr.NotText, "file is not NUL-free UTF-8 text")
	}
	return ReadResult{
		Root: authorized.Alias(), Path: authorized.Path(), Content: string(data),
		Bytes: len(data), SHA256: digest(data),
	}, nil
}

func (s *Service) List(request ListRequest) (ListResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Offset < 0 {
		return ListResult{}, agenterr.New(agenterr.InvalidInput, "offset must not be negative")
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return ListResult{}, agenterr.New(agenterr.InvalidInput, "limit must be between 1 and 200")
	}
	authorized, err := s.policy.Authorize(policy.ListDirectory, request.Root, request.Path)
	if err != nil {
		return ListResult{}, err
	}
	info, err := authorized.OSRoot().Stat(authorized.Path())
	if err != nil {
		return ListResult{}, mapFilesystemError("directory", err)
	}
	if !info.IsDir() {
		return ListResult{}, agenterr.New(agenterr.NotDirectory, "path is not a directory")
	}
	directory, err := authorized.OSRoot().Open(authorized.Path())
	if err != nil {
		return ListResult{}, mapFilesystemError("directory", err)
	}
	defer func() { _ = directory.Close() }()

	remaining := request.Offset
	for remaining > 0 {
		chunk := min(remaining, maxListLimit)
		skipped, readErr := directory.ReadDir(chunk)
		remaining -= len(skipped)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return ListResult{Root: authorized.Alias(), Path: authorized.Path(), Entries: []Entry{}, Offset: request.Offset}, nil
			}
			return ListResult{}, mapFilesystemError("directory", readErr)
		}
	}
	directoryEntries, err := directory.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return ListResult{}, mapFilesystemError("directory", err)
	}
	hasMore := len(directoryEntries) > limit
	if hasMore {
		directoryEntries = directoryEntries[:limit]
	}
	entries := make([]Entry, 0, len(directoryEntries))
	for _, directoryEntry := range directoryEntries {
		entryInfo, err := directoryEntry.Info()
		if err != nil {
			return ListResult{}, mapFilesystemError("directory entry", err)
		}
		entries = append(entries, Entry{
			Name: directoryEntry.Name(), Type: fileType(entryInfo.Mode()), Size: entryInfo.Size(),
			ModifiedAt: timestamp(entryInfo.ModTime()),
		})
	}
	result := ListResult{Root: authorized.Alias(), Path: authorized.Path(), Entries: entries, Offset: request.Offset, HasMore: hasMore}
	if hasMore {
		next := request.Offset + len(entries)
		result.NextOffset = &next
	}
	return result, nil
}

func (s *Service) Info(request InfoRequest) (InfoResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	authorized, err := s.policy.Authorize(policy.FileInfo, request.Root, request.Path)
	if err != nil {
		return InfoResult{}, err
	}
	info, err := authorized.OSRoot().Lstat(authorized.Path())
	if err != nil {
		return InfoResult{}, mapFilesystemError("path", err)
	}
	var target *string
	if info.Mode()&os.ModeSymlink != 0 {
		value, err := authorized.OSRoot().Readlink(authorized.Path())
		if err != nil {
			return InfoResult{}, mapFilesystemError("symbolic link", err)
		}
		target = &value
	}
	return InfoResult{
		Root: authorized.Alias(), Path: authorized.Path(), Name: path.Base(authorized.Path()),
		Type: fileType(info.Mode()), Size: info.Size(), Mode: info.Mode().String(),
		ModifiedAt: timestamp(info.ModTime()), LinkTarget: target,
	}, nil
}

func (s *Service) CreateDirectory(request CreateDirectoryRequest) (CreateDirectoryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	authorized, err := s.policy.Authorize(policy.CreateDirectory, request.Root, request.Path)
	if err != nil {
		return CreateDirectoryResult{}, err
	}
	if authorized.Path() == "." {
		return CreateDirectoryResult{}, agenterr.New(agenterr.InvalidInput, "path must name a new directory")
	}
	if info, statErr := authorized.OSRoot().Lstat(authorized.Path()); statErr == nil {
		if !info.IsDir() {
			return CreateDirectoryResult{}, agenterr.New(agenterr.NotDirectory, "path exists and is not a directory")
		}
		return CreateDirectoryResult{Root: authorized.Alias(), Path: authorized.Path(), Created: false}, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return CreateDirectoryResult{}, mapFilesystemError("directory", statErr)
	}
	if request.CreateParents {
		err = authorized.OSRoot().MkdirAll(authorized.Path(), 0o755)
	} else {
		err = authorized.OSRoot().Mkdir(authorized.Path(), 0o755)
	}
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if info, statErr := authorized.OSRoot().Lstat(authorized.Path()); statErr == nil && info.IsDir() {
				return CreateDirectoryResult{Root: authorized.Alias(), Path: authorized.Path(), Created: false}, nil
			}
		}
		return CreateDirectoryResult{}, mapFilesystemError("directory", err)
	}
	return CreateDirectoryResult{Root: authorized.Alias(), Path: authorized.Path(), Created: true}, nil
}

func (s *Service) Write(request WriteRequest) (WriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := []byte(request.Content)
	if err := validateText(data, s.maxWrite, "content"); err != nil {
		return WriteResult{}, err
	}
	authorized, err := s.policy.Authorize(policy.WriteFile, request.Root, request.Path)
	if err != nil {
		return WriteResult{}, err
	}
	if request.CreateParents {
		parent := path.Dir(authorized.Path())
		if parent != "." {
			if err := authorized.OSRoot().MkdirAll(parent, 0o755); err != nil {
				return WriteResult{}, mapFilesystemError("parent directory", err)
			}
		}
	}
	created, err := atomicWrite(authorized, data, request.Overwrite)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{
		Root: authorized.Alias(), Path: authorized.Path(), Bytes: len(data),
		SHA256: digest(data), Created: created,
	}, nil
}

func (s *Service) Edit(request EditRequest) (EditResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(request.OldText) == 0 {
		return EditResult{}, agenterr.New(agenterr.InvalidInput, "old_text must not be empty")
	}
	if err := validateText([]byte(request.OldText), maxEditTextBytes, "old_text"); err != nil {
		return EditResult{}, err
	}
	if err := validateText([]byte(request.NewText), maxEditTextBytes, "new_text"); err != nil {
		return EditResult{}, err
	}
	authorized, err := s.policy.Authorize(policy.EditFile, request.Root, request.Path)
	if err != nil {
		return EditResult{}, err
	}
	info, err := authorized.OSRoot().Lstat(authorized.Path())
	if err != nil {
		return EditResult{}, mapFilesystemError("file", err)
	}
	if !info.Mode().IsRegular() {
		return EditResult{}, agenterr.New(agenterr.NotFile, "path is not a regular file")
	}
	before, _, err := readRegular(authorized, s.maxWrite)
	if err != nil {
		return EditResult{}, err
	}
	if !utf8.Valid(before) || bytes.IndexByte(before, 0) >= 0 {
		return EditResult{}, agenterr.New(agenterr.NotText, "file is not NUL-free UTF-8 text")
	}
	old := []byte(request.OldText)
	if count := bytes.Count(before, old); count != 1 {
		return EditResult{}, agenterr.New(agenterr.InvalidInput, fmt.Sprintf("old_text must occur exactly once; found %d matches", count))
	}
	after := bytes.Replace(before, old, []byte(request.NewText), 1)
	if len(after) > s.maxWrite {
		return EditResult{}, agenterr.New(agenterr.TooLarge, fmt.Sprintf("edited file exceeds %d bytes", s.maxWrite))
	}
	if _, err := atomicWrite(authorized, after, true); err != nil {
		return EditResult{}, err
	}
	return EditResult{
		Root: authorized.Alias(), Path: authorized.Path(), Bytes: len(after),
		BeforeSHA256: digest(before), AfterSHA256: digest(after),
	}, nil
}

func readRegular(authorized policy.Authorized, limit int) ([]byte, os.FileMode, error) {
	info, err := authorized.OSRoot().Stat(authorized.Path())
	if err != nil {
		return nil, 0, mapFilesystemError("file", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, agenterr.New(agenterr.NotFile, "path is not a regular file")
	}
	file, err := authorized.OSRoot().OpenFile(authorized.Path(), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, mapFilesystemError("file", err)
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, 0, mapFilesystemError("file", err)
	}
	if !openedInfo.Mode().IsRegular() {
		return nil, 0, agenterr.New(agenterr.NotFile, "path is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, 0, mapFilesystemError("file", err)
	}
	if len(data) > limit {
		return nil, 0, agenterr.New(agenterr.TooLarge, fmt.Sprintf("file exceeds %d bytes", limit))
	}
	return data, openedInfo.Mode(), nil
}

func atomicWrite(authorized policy.Authorized, data []byte, overwrite bool) (bool, error) {
	root := authorized.OSRoot()
	destination := authorized.Path()
	mode := os.FileMode(0o644)
	created := true
	if info, err := root.Lstat(destination); err == nil {
		created = false
		if !info.Mode().IsRegular() {
			return false, agenterr.New(agenterr.NotFile, "destination is not a regular file")
		}
		if !overwrite {
			return false, agenterr.New(agenterr.AlreadyExists, "destination already exists")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, mapFilesystemError("destination", err)
	}

	temporary, file, err := createTemporary(root, path.Dir(destination), mode, !created)
	if err != nil {
		return false, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		return false, mapFilesystemError("temporary file", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return false, mapFilesystemError("temporary file", err)
	}
	if err := file.Close(); err != nil {
		return false, mapFilesystemError("temporary file", err)
	}

	if overwrite {
		if err := root.Rename(temporary, destination); err != nil {
			return false, mapFilesystemError("destination", err)
		}
		cleanup = false
		return created, nil
	}
	if err := root.Link(temporary, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, agenterr.New(agenterr.AlreadyExists, "destination already exists")
		}
		return false, mapFilesystemError("destination", err)
	}
	if err := root.Remove(temporary); err != nil {
		return false, agenterr.Wrap(agenterr.InternalError, "file was created but its temporary link could not be removed", err)
	}
	cleanup = false
	return true, nil
}

func createTemporary(root *os.Root, directory string, mode os.FileMode, preserveMode bool) (string, *os.File, error) {
	for range 10 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, agenterr.Wrap(agenterr.InternalError, "cannot generate a temporary file name", err)
		}
		name := tempPrefix + hex.EncodeToString(random[:])
		if directory != "." {
			name = path.Join(directory, name)
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
		if err == nil {
			if preserveMode {
				err = file.Chmod(mode.Perm())
			}
			if err != nil {
				_ = file.Close()
				_ = root.Remove(name)
				return "", nil, mapFilesystemError("temporary file", err)
			}
			return name, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, mapFilesystemError("temporary file", err)
		}
	}
	return "", nil, agenterr.New(agenterr.InternalError, "could not allocate a temporary file")
}

func validateText(data []byte, limit int, field string) error {
	if len(data) > limit {
		return agenterr.New(agenterr.TooLarge, fmt.Sprintf("%s exceeds %d bytes", field, limit))
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return agenterr.New(agenterr.NotText, field+" must be NUL-free UTF-8 text")
	}
	return nil
}

func mapFilesystemError(subject string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return agenterr.Wrap(agenterr.NotFound, subject+" does not exist", err)
	}
	if errors.Is(err, os.ErrExist) {
		return agenterr.Wrap(agenterr.AlreadyExists, subject+" already exists", err)
	}
	if errors.Is(err, os.ErrPermission) {
		return agenterr.Wrap(agenterr.PermissionDenied, "permission denied for "+subject, err)
	}
	return agenterr.Wrap(agenterr.PathDenied, "cannot access "+subject, err)
}

func fileType(mode os.FileMode) Type {
	switch {
	case mode.IsRegular():
		return TypeFile
	case mode.IsDir():
		return TypeDirectory
	case mode&os.ModeSymlink != 0:
		return TypeSymlink
	default:
		return TypeOther
	}
}

func timestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
