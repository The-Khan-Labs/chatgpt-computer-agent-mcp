package mcp

import (
	"context"
	"errors"
	"fmt"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/command"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/files"
	"chatgpt-computer-agent-mcp/internal/policy"
	"chatgpt-computer-agent-mcp/internal/processes"
	"chatgpt-computer-agent-mcp/internal/system"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	sdk      *sdk.Server
	launcher *command.Launcher
	registry *processes.Registry
}

type emptyInput struct{}
type processIDInput struct {
	ProcessID string `json:"process_id"`
}

func New(version string, approved *policy.Set, fileService *files.Service, launcher *command.Launcher, registry *processes.Registry, limits config.Limits) *Server {
	server := &Server{
		sdk: sdk.NewServer(&sdk.Implementation{
			Name: "chatgpt-computer-agent-mcp", Title: "ChatGPT Computer Agent", Version: version,
		}, nil),
		launcher: launcher,
		registry: registry,
	}
	inputs := inputSchemas(limits)
	outputs := outputSchemas()
	add := func(capability policy.Capability, register func()) {
		if policy.Allows(approved.Mode(), capability) {
			register()
		}
	}

	add(policy.SystemInfo, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("system_info", inputs, outputs, "Report bounded system and capability information.", true, false, true, false),
			func(context.Context, *sdk.CallToolRequest, emptyInput) (*sdk.CallToolResult, system.Result, error) {
				result, err := system.Info(version, approved, registry.Count())
				return textResult("System information retrieved."), result, stableError(err)
			})
	})
	add(policy.ReadFile, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("read_file", inputs, outputs, "Read a bounded UTF-8 text file from an approved root.", true, false, true, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input files.ReadRequest) (*sdk.CallToolResult, files.ReadResult, error) {
				result, err := fileService.Read(input)
				return textResult(fmt.Sprintf("Read %d bytes from %s:%s.", result.Bytes, result.Root, result.Path)), result, stableError(err)
			})
	})
	add(policy.ListDirectory, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("list_directory", inputs, outputs, "List one page of a directory in an approved root.", true, false, true, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input files.ListRequest) (*sdk.CallToolResult, files.ListResult, error) {
				result, err := fileService.List(input)
				return textResult(fmt.Sprintf("Listed %d entries in %s:%s.", len(result.Entries), result.Root, result.Path)), result, stableError(err)
			})
	})
	add(policy.FileInfo, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("file_info", inputs, outputs, "Inspect one path without following a final symbolic link.", true, false, true, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input files.InfoRequest) (*sdk.CallToolResult, files.InfoResult, error) {
				result, err := fileService.Info(input)
				return textResult(fmt.Sprintf("Inspected %s:%s.", result.Root, result.Path)), result, stableError(err)
			})
	})
	add(policy.CreateDirectory, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("create_directory", inputs, outputs, "Create a directory in an approved root.", false, false, true, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input files.CreateDirectoryRequest) (*sdk.CallToolResult, files.CreateDirectoryResult, error) {
				result, err := fileService.CreateDirectory(input)
				return textResult(fmt.Sprintf("Directory operation completed for %s:%s.", result.Root, result.Path)), result, stableError(err)
			})
	})
	add(policy.WriteFile, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("write_file", inputs, outputs, "Atomically create or replace a bounded UTF-8 text file.", false, true, false, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input files.WriteRequest) (*sdk.CallToolResult, files.WriteResult, error) {
				result, err := fileService.Write(input)
				return textResult(fmt.Sprintf("Wrote %d bytes to %s:%s.", result.Bytes, result.Root, result.Path)), result, stableError(err)
			})
	})
	add(policy.EditFile, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("edit_file", inputs, outputs, "Atomically replace exactly one literal text occurrence.", false, true, false, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input files.EditRequest) (*sdk.CallToolResult, files.EditResult, error) {
				result, err := fileService.Edit(input)
				return textResult(fmt.Sprintf("Edited %s:%s; resulting size %d bytes.", result.Root, result.Path, result.Bytes)), result, stableError(err)
			})
	})
	add(policy.RunCommand, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("run_command", inputs, outputs, "Run a direct executable and exact argument array as the current user.", false, true, false, true),
			func(ctx context.Context, _ *sdk.CallToolRequest, input command.Request) (*sdk.CallToolResult, command.Result, error) {
				result, err := launcher.Run(ctx, input)
				if err != nil {
					return nil, result, stableError(err)
				}
				if result.TimedOut {
					return errorResult(agenterr.New(agenterr.TimedOut, "command timed out")), result, nil
				}
				if result.Termination != nil || result.ExitCode == nil || *result.ExitCode != 0 {
					return errorResult(agenterr.New(agenterr.CommandFailed, "command exited unsuccessfully")), result, nil
				}
				return textResult("Command completed successfully."), result, nil
			})
	})
	add(policy.ProcessStart, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("process_start", inputs, outputs, "Start a direct background process owned by this runtime.", false, true, false, true),
			func(ctx context.Context, _ *sdk.CallToolRequest, input processes.StartRequest) (*sdk.CallToolResult, processes.StartResult, error) {
				result, err := registry.Start(ctx, input)
				return textResult(fmt.Sprintf("Managed process %s is %s.", result.ProcessID, result.State)), result, stableError(err)
			})
	})
	add(policy.ProcessStatus, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("process_status", inputs, outputs, "Inspect one managed process.", true, false, true, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input processIDInput) (*sdk.CallToolResult, processes.StatusResult, error) {
				result, err := registry.Status(input.ProcessID)
				return textResult(fmt.Sprintf("Managed process %s is %s.", result.ProcessID, result.State)), result, stableError(err)
			})
	})
	add(policy.ProcessOutput, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("process_output", inputs, outputs, "Read one retained output page from a managed process.", true, false, true, false),
			func(_ context.Context, _ *sdk.CallToolRequest, input processes.OutputRequest) (*sdk.CallToolResult, processes.OutputResult, error) {
				result, err := registry.Output(input)
				return textResult(fmt.Sprintf("Read managed process %s %s bytes %d..%d.", result.ProcessID, result.Stream, result.Offset, result.NextOffset)), result, stableError(err)
			})
	})
	add(policy.ProcessStop, func() {
		sdk.AddTool(server.sdk, toolWithSchemas("process_stop", inputs, outputs, "Stop one managed process tree.", false, true, true, false),
			func(ctx context.Context, _ *sdk.CallToolRequest, input processIDInput) (*sdk.CallToolResult, processes.StatusResult, error) {
				result, err := registry.Stop(ctx, input.ProcessID)
				return textResult(fmt.Sprintf("Managed process %s is %s.", result.ProcessID, result.State)), result, stableError(err)
			})
	})
	return server
}

func (s *Server) RunStdio(ctx context.Context) error {
	err := s.sdk.Run(ctx, &sdk.StdioTransport{})
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return errors.Join(err, s.Shutdown(context.Background()))
}

func (s *Server) Shutdown(ctx context.Context) error {
	return errors.Join(s.registry.Shutdown(ctx), s.launcher.Shutdown(ctx))
}

func tool(name, description string, readOnly, destructive, idempotent, openWorld bool) *sdk.Tool {
	return &sdk.Tool{Name: name, Description: description, Annotations: annotations(readOnly, destructive, idempotent, openWorld)}
}

func toolWithSchemas(name string, inputs, outputs map[string]schema, description string, readOnly, destructive, idempotent, openWorld bool) *sdk.Tool {
	result := tool(name, description, readOnly, destructive, idempotent, openWorld)
	result.InputSchema = inputs[name]
	result.OutputSchema = outputs[name]
	return result
}

func annotations(readOnly, destructive, idempotent, openWorld bool) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{
		ReadOnlyHint: readOnly, DestructiveHint: boolPointer(destructive),
		IdempotentHint: idempotent, OpenWorldHint: boolPointer(openWorld),
	}
}

func boolPointer(value bool) *bool { return &value }

func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

func errorResult(err error) *sdk.CallToolResult {
	result := textResult(err.Error())
	result.IsError = true
	return result
}

func stableError(err error) error {
	if err == nil || agenterr.CodeOf(err) != "" {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return agenterr.Wrap(agenterr.CommandFailed, "operation was cancelled", err)
	}
	return agenterr.Wrap(agenterr.InternalError, "operation failed", err)
}
