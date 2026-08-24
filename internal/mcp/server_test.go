package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"chatgpt-computer-agent-mcp/internal/command"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/files"
	"chatgpt-computer-agent-mcp/internal/policy"
	"chatgpt-computer-agent-mcp/internal/processes"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolDiscoveryMatchesPermissionModes(t *testing.T) {
	tests := []struct {
		mode config.Mode
		want []string
	}{
		{config.Readonly, []string{"file_info", "list_directory", "read_file", "system_info"}},
		{config.Workspace, []string{"create_directory", "edit_file", "file_info", "list_directory", "read_file", "system_info", "write_file"}},
		{config.UserShell, []string{"create_directory", "edit_file", "file_info", "list_directory", "process_output", "process_start", "process_status", "process_stop", "read_file", "run_command", "system_info", "write_file"}},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			server, _, _, _, _ := newTestServer(t, test.mode)
			session := connectClient(t, server)
			listed, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(listed.Tools))
			for _, tool := range listed.Tools {
				got = append(got, tool.Name)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("tools=%v want=%v", got, test.want)
			}
		})
	}
}

func TestToolsExposeClosedSchemasAndExactAnnotations(t *testing.T) {
	server, _, _, _, _ := newTestServer(t, config.UserShell)
	session := connectClient(t, server)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantAnnotations := map[string][4]bool{
		"system_info": {true, false, true, false}, "read_file": {true, false, true, false},
		"list_directory": {true, false, true, false}, "file_info": {true, false, true, false},
		"create_directory": {false, false, true, false}, "write_file": {false, true, false, false},
		"edit_file": {false, true, false, false}, "run_command": {false, true, false, true},
		"process_start": {false, true, false, true}, "process_status": {true, false, true, false},
		"process_output": {true, false, true, false}, "process_stop": {false, true, true, false},
	}
	for _, tool := range listed.Tools {
		input, ok := tool.InputSchema.(map[string]any)
		if !ok || input["additionalProperties"] != false {
			t.Errorf("%s input schema is not closed: %#v", tool.Name, tool.InputSchema)
		}
		output, ok := tool.OutputSchema.(map[string]any)
		if !ok || output["additionalProperties"] != false {
			t.Errorf("%s output schema is not closed: %#v", tool.Name, tool.OutputSchema)
		}
		assertClosedObjects(t, tool.Name+" input", input)
		assertClosedObjects(t, tool.Name+" output", output)
		assertByteLimitDescriptions(t, tool.Name+" input", input)
		want := wantAnnotations[tool.Name]
		annotations := tool.Annotations
		if annotations == nil || annotations.DestructiveHint == nil || annotations.OpenWorldHint == nil {
			t.Errorf("%s missing annotations: %#v", tool.Name, annotations)
			continue
		}
		got := [4]bool{annotations.ReadOnlyHint, *annotations.DestructiveHint, annotations.IdempotentHint, *annotations.OpenWorldHint}
		if got != want {
			t.Errorf("%s annotations=%v want=%v", tool.Name, got, want)
		}
	}
}

func assertByteLimitDescriptions(t *testing.T, name string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["maxLength"]; ok {
			description, _ := typed["description"].(string)
			if !strings.Contains(description, "maxLength counts Unicode characters") ||
				!strings.Contains(description, "runtime enforces the limit in UTF-8 bytes") {
				t.Errorf("%s has an ambiguous byte limit: %#v", name, typed)
			}
		}
		for key, child := range typed {
			assertByteLimitDescriptions(t, name+"."+key, child)
		}
	case []any:
		for i, child := range typed {
			assertByteLimitDescriptions(t, name+"["+strconv.Itoa(i)+"]", child)
		}
	}
}

func TestFileCallsReturnTextAndStructuredContent(t *testing.T) {
	server, root, _, _, _ := newTestServer(t, config.Workspace)
	session := connectClient(t, server)
	write, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "write_file", Arguments: map[string]any{
			"root": "workspace", "path": "folder/雪.txt", "content": "hello", "create_parents": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if write.IsError || write.StructuredContent == nil || textContent(write) == "" {
		t.Fatalf("write result: %+v", write)
	}
	if data, err := os.ReadFile(filepath.Join(root, "folder", "雪.txt")); err != nil || string(data) != "hello" {
		t.Fatalf("written data=%q err=%v", data, err)
	}
	read, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "read_file", Arguments: map[string]any{"root": "workspace", "path": "folder/雪.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	structured := read.StructuredContent.(map[string]any)
	if read.IsError || structured["content"] != "hello" || textContent(read) == "" {
		t.Fatalf("read result: %+v", read)
	}
}

func TestRemainingToolTranslations(t *testing.T) {
	server, _, _, _, _ := newTestServer(t, config.UserShell)
	session := connectClient(t, server)
	callSuccess(t, session, "system_info", map[string]any{})
	callSuccess(t, session, "create_directory", map[string]any{"root": "workspace", "path": "directory"})
	callSuccess(t, session, "list_directory", map[string]any{"root": "workspace", "path": "."})
	callSuccess(t, session, "file_info", map[string]any{"root": "workspace", "path": "directory"})
	callSuccess(t, session, "write_file", map[string]any{"root": "workspace", "path": "edit.txt", "content": "before"})
	callSuccess(t, session, "edit_file", map[string]any{
		"root": "workspace", "path": "edit.txt", "old_text": "before", "new_text": "after",
	})

	executable, _ := os.Executable()
	started := callSuccess(t, session, "process_start", map[string]any{
		"executable": executable,
		"arguments":  []string{"-test.run=TestMCPHelperProcess", "--", "sleep"},
		"cwd":        map[string]any{"root": "workspace", "path": "."},
	})
	processID, _ := started["process_id"].(string)
	if processID == "" {
		t.Fatalf("process_start: %#v", started)
	}
	callSuccess(t, session, "process_status", map[string]any{"process_id": processID})
	callSuccess(t, session, "process_output", map[string]any{"process_id": processID, "stream": "stdout"})
	stopped := callSuccess(t, session, "process_stop", map[string]any{"process_id": processID})
	if stopped["state"] != "stopped" {
		t.Fatalf("process_stop: %#v", stopped)
	}
}

func TestSchemaAndDomainErrorsStayToolErrors(t *testing.T) {
	server, _, _, _, _ := newTestServer(t, config.Readonly)
	session := connectClient(t, server)
	malformed, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "read_file", Arguments: map[string]any{"root": "workspace", "path": "missing", "unknown": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !malformed.IsError || strings.Contains(textContent(malformed), "NOT_FOUND") {
		t.Fatalf("schema validation did not run first: %+v", malformed)
	}
	domain, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "read_file", Arguments: map[string]any{"root": "workspace", "path": "missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !domain.IsError || !strings.HasPrefix(textContent(domain), "NOT_FOUND:") {
		t.Fatalf("domain error: %+v", domain)
	}
	if _, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "unknown_tool"}); err == nil {
		t.Fatal("unknown tool did not produce a protocol error")
	}
}

func TestCommandFailureRetainsStructuredResult(t *testing.T) {
	server, _, _, _, _ := newTestServer(t, config.UserShell)
	session := connectClient(t, server)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "run_command", Arguments: map[string]any{
			"executable": executable,
			"arguments":  []string{"-test.run=TestMCPHelperProcess", "--", "exit"},
			"cwd":        map[string]any{"root": "workspace", "path": "."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !result.IsError || !ok || structured["exit_code"] != float64(7) || !strings.HasPrefix(textContent(result), "COMMAND_FAILED:") {
		t.Fatalf("command failure: %+v", result)
	}
}

func TestCommandOutputMayContainNUL(t *testing.T) {
	server, _, _, _, _ := newTestServer(t, config.UserShell)
	session := connectClient(t, server)
	executable, _ := os.Executable()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "run_command", Arguments: map[string]any{
			"executable": executable,
			"arguments":  []string{"-test.run=TestMCPHelperProcess", "--", "nul"},
			"cwd":        map[string]any{"root": "workspace", "path": "."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	structured := result.StructuredContent.(map[string]any)
	if result.IsError || structured["stdout"] != "a\x00b" {
		t.Fatalf("NUL output: %+v", result)
	}
}

func TestCancellationReachesForegroundCommand(t *testing.T) {
	server, _, launcher, _, _ := newTestServer(t, config.UserShell)
	session := connectClient(t, server)
	executable, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "run_command", Arguments: map[string]any{
			"executable": executable,
			"arguments":  []string{"-test.run=TestMCPHelperProcess", "--", "sleep"},
			"cwd":        map[string]any{"root": "workspace", "path": "."},
		},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call error=%v", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := launcher.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("foreground cleanup: %v", err)
	}
}

func TestMCPHelperProcess(t *testing.T) {
	separator := -1
	for i, argument := range os.Args {
		if argument == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "exit":
		os.Exit(7)
	case "sleep":
		time.Sleep(30 * time.Second)
	case "nul":
		_, _ = os.Stdout.Write([]byte{'a', 0, 'b'})
	}
	os.Exit(0)
}

func newTestServer(t *testing.T, mode config.Mode) (*Server, string, *command.Launcher, *processes.Registry, *policy.Set) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	document := map[string]any{
		"version": 1, "mode": mode,
		"roots": []map[string]string{{"name": "workspace", "path": root}},
		"limits": map[string]int{
			"default_command_timeout_seconds": 30, "process_stop_grace_seconds": 1,
			"max_output_bytes_per_stream": 1024, "max_background_processes": 2,
		},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := policy.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fileService := files.New(roots, cfg.Limits())
	launcher := command.New(roots, cfg.Limits())
	registry := processes.New(launcher, cfg.Limits().MaxBackgroundProcesses)
	server := New("test-version", roots, fileService, launcher, registry, cfg.Limits())
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = roots.Close()
	})
	return server, root, launcher, registry, roots
}

func connectClient(t *testing.T, server *Server) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- server.sdk.Run(ctx, serverTransport) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("MCP server did not stop")
		}
	})
	return session
}

func textContent(result *sdk.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	text, _ := result.Content[0].(*sdk.TextContent)
	if text == nil {
		return ""
	}
	return text.Text
}

func callSuccess(t *testing.T, session *sdk.ClientSession, name string, arguments map[string]any) map[string]any {
	t.Helper()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.IsError || textContent(result) == "" {
		t.Fatalf("%s: %+v", name, result)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s structured content: %#v", name, result.StructuredContent)
	}
	return structured
}

func assertClosedObjects(t *testing.T, name string, value any) {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	if object["type"] == "object" && object["additionalProperties"] != false {
		t.Errorf("%s contains an open object: %#v", name, object)
	}
	for key, child := range object {
		switch child := child.(type) {
		case map[string]any:
			assertClosedObjects(t, name+"."+key, child)
		case []any:
			for _, item := range child {
				assertClosedObjects(t, name+"."+key, item)
			}
		}
	}
}
