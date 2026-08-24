package system

import (
	"os"
	"runtime"
	"unicode/utf8"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/policy"
)

const maxHostnameBytes = 255

type Result struct {
	ServerVersion    string               `json:"server_version"`
	OS               string               `json:"os"`
	Architecture     string               `json:"architecture"`
	Hostname         string               `json:"hostname"`
	Mode             config.Mode          `json:"mode"`
	Roots            []policy.RootSummary `json:"roots"`
	CommandsEnabled  bool                 `json:"commands_enabled"`
	ManagedProcesses int                  `json:"managed_processes"`
}

func Info(version string, approved *policy.Set, managedProcesses int) (Result, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return Result{}, agenterr.Wrap(agenterr.InternalError, "cannot read hostname", err)
	}
	hostname = truncateUTF8(hostname, maxHostnameBytes)
	return Result{
		ServerVersion: version, OS: runtime.GOOS, Architecture: runtime.GOARCH,
		Hostname: hostname, Mode: approved.Mode(), Roots: approved.Roots(),
		CommandsEnabled: policy.Allows(approved.Mode(), policy.RunCommand), ManagedProcesses: managedProcesses,
	}, nil
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
