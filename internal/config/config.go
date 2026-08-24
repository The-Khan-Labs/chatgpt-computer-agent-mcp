package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"chatgpt-computer-agent-mcp/internal/agenterr"
)

type Mode string

const (
	Readonly  Mode = "readonly"
	Workspace Mode = "workspace"
	UserShell Mode = "user-shell"
)

type Root struct {
	Name string
	Path string
}

type Limits struct {
	MaxReadBytes                 int
	MaxWriteBytes                int
	DefaultCommandTimeoutSeconds int
	MaxCommandTimeoutSeconds     int
	MaxOutputBytesPerStream      int
	MaxBackgroundProcesses       int
	ProcessStopGraceSeconds      int
}

var defaultLimits = Limits{
	MaxReadBytes:                 1_048_576,
	MaxWriteBytes:                2_097_152,
	DefaultCommandTimeoutSeconds: 120,
	MaxCommandTimeoutSeconds:     600,
	MaxOutputBytesPerStream:      1_048_576,
	MaxBackgroundProcesses:       8,
	ProcessStopGraceSeconds:      2,
}

type Config struct {
	path   string
	mode   Mode
	roots  []Root
	limits Limits
}

func (c *Config) Path() string   { return c.path }
func (c *Config) Mode() Mode     { return c.mode }
func (c *Config) Limits() Limits { return c.limits }
func (c *Config) Roots() []Root  { return append([]Root(nil), c.roots...) }

type document struct {
	Version int            `json:"version"`
	Mode    Mode           `json:"mode"`
	Roots   []rootDocument `json:"roots"`
	Limits  limitDocument  `json:"limits,omitzero"`
}

type rootDocument struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type limitDocument struct {
	MaxReadBytes                 *int `json:"max_read_bytes,omitempty"`
	MaxWriteBytes                *int `json:"max_write_bytes,omitempty"`
	DefaultCommandTimeoutSeconds *int `json:"default_command_timeout_seconds,omitempty"`
	MaxCommandTimeoutSeconds     *int `json:"max_command_timeout_seconds,omitempty"`
	MaxOutputBytesPerStream      *int `json:"max_output_bytes_per_stream,omitempty"`
	MaxBackgroundProcesses       *int `json:"max_background_processes,omitempty"`
	ProcessStopGraceSeconds      *int `json:"process_stop_grace_seconds,omitempty"`
}

var rootName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

func Load(path string) (*Config, error) {
	data, err := readVerified(path)
	if err != nil {
		return nil, err
	}
	raw, err := decodeDocument(path, data)
	if err != nil {
		return nil, err
	}
	return validate(path, raw)
}

func readVerified(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("cannot open configuration %q", path), err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("cannot inspect configuration %q", path), err)
	}
	if err := checkPermissions(path, info); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("cannot read configuration %q", path), err)
	}
	return data, nil
}

func decodeDocument(path string, data []byte) (document, error) {
	var raw document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return document{}, agenterr.Wrap(agenterr.InvalidInput, fmt.Sprintf("invalid configuration %q", path), err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return document{}, agenterr.New(agenterr.InvalidInput, fmt.Sprintf("configuration %q must contain one JSON document", path))
	}
	return raw, nil
}

func validate(path string, raw document) (*Config, error) {
	fail := func(format string, args ...any) (*Config, error) {
		return nil, agenterr.New(agenterr.InvalidInput, fmt.Sprintf(format, args...))
	}
	if raw.Version != 1 {
		return fail("configuration version must be 1")
	}
	if raw.Mode != Readonly && raw.Mode != Workspace && raw.Mode != UserShell {
		return fail("mode must be readonly, workspace, or user-shell")
	}
	if len(raw.Roots) == 0 {
		return fail("at least one root is required")
	}

	roots := make([]Root, 0, len(raw.Roots))
	seen := make(map[string]bool, len(raw.Roots))
	for _, root := range raw.Roots {
		if !rootName.MatchString(root.Name) {
			return fail("invalid root name %q", root.Name)
		}
		if seen[root.Name] {
			return fail("duplicate root name %q", root.Name)
		}
		if !filepath.IsAbs(root.Path) {
			return fail("root %q path must be absolute", root.Name)
		}
		seen[root.Name] = true
		roots = append(roots, Root(root))
	}

	limits := defaultLimits
	set := func(value *int, target *int) {
		if value != nil {
			*target = *value
		}
	}
	set(raw.Limits.MaxReadBytes, &limits.MaxReadBytes)
	set(raw.Limits.MaxWriteBytes, &limits.MaxWriteBytes)
	set(raw.Limits.DefaultCommandTimeoutSeconds, &limits.DefaultCommandTimeoutSeconds)
	set(raw.Limits.MaxCommandTimeoutSeconds, &limits.MaxCommandTimeoutSeconds)
	set(raw.Limits.MaxOutputBytesPerStream, &limits.MaxOutputBytesPerStream)
	set(raw.Limits.MaxBackgroundProcesses, &limits.MaxBackgroundProcesses)
	set(raw.Limits.ProcessStopGraceSeconds, &limits.ProcessStopGraceSeconds)

	if limits.MaxReadBytes < 1 || limits.MaxReadBytes > 8<<20 {
		return fail("max_read_bytes must be between 1 and 8388608")
	}
	if limits.MaxWriteBytes < 1 || limits.MaxWriteBytes > 8<<20 {
		return fail("max_write_bytes must be between 1 and 8388608")
	}
	if limits.DefaultCommandTimeoutSeconds < 1 || limits.DefaultCommandTimeoutSeconds > 3600 {
		return fail("default_command_timeout_seconds must be between 1 and 3600")
	}
	if limits.MaxCommandTimeoutSeconds < 1 || limits.MaxCommandTimeoutSeconds > 3600 {
		return fail("max_command_timeout_seconds must be between 1 and 3600")
	}
	if limits.DefaultCommandTimeoutSeconds > limits.MaxCommandTimeoutSeconds {
		return fail("default command timeout may not exceed maximum command timeout")
	}
	if limits.MaxOutputBytesPerStream < 1 || limits.MaxOutputBytesPerStream > 8<<20 {
		return fail("max_output_bytes_per_stream must be between 1 and 8388608")
	}
	if limits.MaxBackgroundProcesses < 1 || limits.MaxBackgroundProcesses > 32 {
		return fail("max_background_processes must be between 1 and 32")
	}
	if limits.ProcessStopGraceSeconds < 1 || limits.ProcessStopGraceSeconds > 30 {
		return fail("process_stop_grace_seconds must be between 1 and 30")
	}

	return &Config{path: path, mode: raw.Mode, roots: roots, limits: limits}, nil
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	name := "chatgpt-computer-agent-mcp"
	switch runtime.GOOS {
	case "windows":
		name = "ChatGPTComputerAgentMCP"
	case "darwin":
		name = "ChatGPT Computer Agent MCP"
	}
	return filepath.Join(dir, name, "config.json"), nil
}
