package mcp

import "chatgpt-computer-agent-mcp/internal/config"

type schema map[string]any

const noNULPattern = "^[^\x00]*$"

func inputSchemas(limits config.Limits) map[string]schema {
	root := stringSchema(1, 32)
	root["pattern"] = `^[A-Za-z][A-Za-z0-9_-]{0,31}$`
	path := stringSchema(1, 4096)
	cwd := object(schema{"root": root, "path": path}, "root", "path")
	processID := stringSchema(1, 128)
	executable := stringSchema(1, 4096)
	argument := stringSchema(0, 16<<10)
	arguments := schema{"type": "array", "items": argument, "maxItems": 256, "default": []any{}}
	return map[string]schema{
		"system_info": object(schema{}),
		"read_file": object(schema{
			"root": root, "path": path,
			"max_bytes": integerSchema(1, limits.MaxReadBytes, limits.MaxReadBytes),
		}, "root", "path"),
		"list_directory": object(schema{
			"root": root, "path": path,
			"offset": integerSchema(0, nil, 0), "limit": integerSchema(1, 200, 100),
		}, "root", "path"),
		"file_info": object(schema{"root": root, "path": path}, "root", "path"),
		"create_directory": object(schema{
			"root": root, "path": path, "create_parents": booleanSchema(false),
		}, "root", "path"),
		"write_file": object(schema{
			"root": root, "path": path, "content": stringSchema(0, limits.MaxWriteBytes),
			"overwrite": booleanSchema(false), "create_parents": booleanSchema(false),
		}, "root", "path", "content"),
		"edit_file": object(schema{
			"root": root, "path": path, "old_text": stringSchema(1, 512<<10), "new_text": stringSchema(0, 512<<10),
		}, "root", "path", "old_text", "new_text"),
		"run_command": object(schema{
			"executable": executable, "arguments": arguments, "cwd": cwd,
			"timeout_seconds": integerSchema(1, limits.MaxCommandTimeoutSeconds, limits.DefaultCommandTimeoutSeconds),
		}, "executable", "cwd"),
		"process_start": object(schema{
			"executable": executable, "arguments": arguments, "cwd": cwd,
		}, "executable", "cwd"),
		"process_status": object(schema{"process_id": processID}, "process_id"),
		"process_output": object(schema{
			"process_id": processID, "stream": enumSchema("stdout", "stderr"),
			"offset": integerSchema(0, nil, 0), "max_bytes": integerSchema(1, 65536, 65536),
		}, "process_id", "stream"),
		"process_stop": object(schema{"process_id": processID}, "process_id"),
	}
}

func outputSchemas() map[string]schema {
	text := outputStringSchema(0, 8<<20)
	timestamp := outputStringSchema(1, 64)
	nullableTimestamp := nullableStringSchema(64)
	nullableInteger := nullable("integer")
	nullableString := nullableStringSchema(32768)
	rootSummary := object(schema{
		"name": outputStringSchema(1, 32), "path": outputStringSchema(1, 32768),
		"readable": schema{"type": "boolean"}, "writable": schema{"type": "boolean"},
	}, "name", "path", "readable", "writable")
	entry := object(schema{
		"name": outputStringSchema(1, 4096), "type": enumSchema("file", "directory", "symlink", "other"),
		"size": integerSchema(0, nil, nil), "modified_at": timestamp,
	}, "name", "type", "size", "modified_at")
	statusProperties := schema{
		"process_id": outputStringSchema(1, 128), "state": enumSchema("running", "exited", "stopped"),
		"exit_code": nullableInteger, "termination": nullableString,
		"started_at": timestamp, "finished_at": nullableTimestamp,
		"duration_ms":  integerSchema(0, nil, nil),
		"stdout_bytes": integerSchema(0, nil, nil), "stderr_bytes": integerSchema(0, nil, nil),
		"stdout_truncated": schema{"type": "boolean"}, "stderr_truncated": schema{"type": "boolean"},
	}
	return map[string]schema{
		"system_info": object(schema{
			"server_version": outputStringSchema(1, 128), "os": enumSchema("windows", "darwin", "linux"),
			"architecture": enumSchema("amd64", "arm64"), "hostname": outputStringSchema(1, 255),
			"mode":             enumSchema("readonly", "workspace", "user-shell"),
			"roots":            schema{"type": "array", "items": rootSummary},
			"commands_enabled": schema{"type": "boolean"}, "managed_processes": integerSchema(0, 32, nil),
		}, "server_version", "os", "architecture", "hostname", "mode", "roots", "commands_enabled", "managed_processes"),
		"read_file": object(schema{
			"root": outputStringSchema(1, 32), "path": outputStringSchema(1, 4096), "content": text,
			"bytes": integerSchema(0, nil, nil), "sha256": outputStringSchema(64, 64),
		}, "root", "path", "content", "bytes", "sha256"),
		"list_directory": object(schema{
			"root": outputStringSchema(1, 32), "path": outputStringSchema(1, 4096),
			"entries": schema{"type": "array", "items": entry, "maxItems": 200},
			"offset":  integerSchema(0, nil, nil), "next_offset": nullableInteger, "has_more": schema{"type": "boolean"},
		}, "root", "path", "entries", "offset", "next_offset", "has_more"),
		"file_info": object(schema{
			"root": outputStringSchema(1, 32), "path": outputStringSchema(1, 4096), "name": outputStringSchema(1, 4096),
			"type": enumSchema("file", "directory", "symlink", "other"), "size": integerSchema(0, nil, nil),
			"mode": outputStringSchema(1, 32), "modified_at": timestamp, "link_target": nullableString,
		}, "root", "path", "name", "type", "size", "mode", "modified_at", "link_target"),
		"create_directory": object(schema{
			"root": outputStringSchema(1, 32), "path": outputStringSchema(1, 4096), "created": schema{"type": "boolean"},
		}, "root", "path", "created"),
		"write_file": object(schema{
			"root": outputStringSchema(1, 32), "path": outputStringSchema(1, 4096), "bytes": integerSchema(0, nil, nil),
			"sha256": outputStringSchema(64, 64), "created": schema{"type": "boolean"},
		}, "root", "path", "bytes", "sha256", "created"),
		"edit_file": object(schema{
			"root": outputStringSchema(1, 32), "path": outputStringSchema(1, 4096), "bytes": integerSchema(0, nil, nil),
			"before_sha256": outputStringSchema(64, 64), "after_sha256": outputStringSchema(64, 64),
		}, "root", "path", "bytes", "before_sha256", "after_sha256"),
		"run_command": object(schema{
			"stdout": text, "stderr": text, "exit_code": nullableInteger, "termination": nullableString,
			"timed_out": schema{"type": "boolean"}, "duration_ms": integerSchema(0, nil, nil),
			"stdout_truncated": schema{"type": "boolean"}, "stderr_truncated": schema{"type": "boolean"},
		}, "stdout", "stderr", "exit_code", "termination", "timed_out", "duration_ms", "stdout_truncated", "stderr_truncated"),
		"process_start": object(schema{
			"process_id": outputStringSchema(1, 128), "state": enumSchema("running", "exited"),
			"started_at": timestamp, "finished_at": nullableTimestamp,
			"exit_code": nullableInteger, "termination": nullableString,
		}, "process_id", "state", "started_at", "finished_at", "exit_code", "termination"),
		"process_status": object(statusProperties, "process_id", "state", "exit_code", "termination", "started_at", "finished_at", "duration_ms", "stdout_bytes", "stderr_bytes", "stdout_truncated", "stderr_truncated"),
		"process_output": object(schema{
			"process_id": outputStringSchema(1, 128), "stream": enumSchema("stdout", "stderr"), "data": text,
			"offset": integerSchema(0, nil, nil), "next_offset": integerSchema(0, nil, nil),
			"end_of_stream": schema{"type": "boolean"}, "truncated": schema{"type": "boolean"},
		}, "process_id", "stream", "data", "offset", "next_offset", "end_of_stream", "truncated"),
		"process_stop": object(statusProperties, "process_id", "state", "exit_code", "termination", "started_at", "finished_at", "duration_ms", "stdout_bytes", "stderr_bytes", "stdout_truncated", "stderr_truncated"),
	}
}

func object(properties schema, required ...string) schema {
	result := schema{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func stringSchema(minimum, maximum int) schema {
	return schema{
		"type": "string", "minLength": minimum, "maxLength": maximum, "pattern": noNULPattern,
		"description": "JSON Schema maxLength counts Unicode characters and does not guarantee the byte limit; runtime enforces the limit in UTF-8 bytes.",
	}
}

func outputStringSchema(minimum, maximum int) schema {
	return schema{"type": "string", "minLength": minimum, "maxLength": maximum}
}

func integerSchema(minimum int, maximum, defaultValue any) schema {
	result := schema{"type": "integer", "minimum": minimum}
	if maximum != nil {
		result["maximum"] = maximum
	}
	if defaultValue != nil {
		result["default"] = defaultValue
	}
	return result
}

func booleanSchema(defaultValue bool) schema {
	return schema{"type": "boolean", "default": defaultValue}
}

func enumSchema(values ...string) schema {
	items := make([]any, len(values))
	for i, value := range values {
		items[i] = value
	}
	return schema{"type": "string", "enum": items}
}

func nullable(kind string) schema { return schema{"type": []any{kind, "null"}} }

func nullableStringSchema(maximum int) schema {
	return schema{"type": []any{"string", "null"}, "maxLength": maximum}
}
