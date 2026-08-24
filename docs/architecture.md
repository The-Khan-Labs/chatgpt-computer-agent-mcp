# ChatGPT Computer Agent MCP Architecture

- **Status:** Stable v1.0.0
- **Date:** 2026-08-20

## Summary

ChatGPT Computer Agent MCP is one cross-platform Go program that exposes a
small set of local computer tools over MCP stdio. Users explicitly choose a
permission mode and one or more filesystem roots. The server runs with the
permissions of the current operating-system user and never elevates itself.

The v1 implementation uses the official MCP Go SDK, Go's traversal-resistant
`os.Root` filesystem API, direct executable-plus-argument process invocation,
and only the platform-specific process adapters required to terminate owned
process trees.

The OpenAI Secure MCP Tunnel remains an external launcher and connectivity
layer. This repository neither bundles nor forks it.

## Goals

- One prebuilt binary for Windows, macOS, and Linux on amd64 and arm64.
- Stdio as the only v1 MCP transport.
- Explicit, traversal-resistant approved-root file access.
- Nested `readonly`, `workspace`, and `user-shell` permission modes.
- Direct, current-user foreground command execution with timeouts and bounded
  output.
- Four small managed-process tools: `process_start`, `process_status`,
  `process_output`, and `process_stop`.
- Typed MCP input and output schemas, annotations, structured content, and
  actionable errors.
- Policy, file, command, and process modules testable without MCP transport.
- Honest documentation of security limits and native-platform validation.

## V1 non-goals

The following features are absent, not dormant:

- root, sudo, Administrator, UAC, or other privileged execution
- HTTP, SSE, or any listening MCP network service
- SSH, SFTP, remote-host control, bastions, or host profiles
- an embedded or forked Secure MCP Tunnel client
- terminal emulation, PTYs, interactive sessions, shell-state persistence, or
  a full session manager
- implicit shell parsing, command-string classifiers, or destructive-command
  heuristics
- containers, virtual machines, operating-system sandboxes, cgroups, or a
  claim that the process is sandboxed
- audit databases, tamper-evident ledgers, tracing stacks, OPA, policy engines,
  web dashboards, or approval brokers
- service installation, firewall changes, public listeners, or automatic
  package-manager installation

Any addition from this list requires a separately reviewed design.

## Source and version baseline

- Module language minimum: **Go 1.25.0**.
- Development and release toolchain: **Go 1.27.0**, pinned in CI and the module
  toolchain directive.
- MCP dependency: `github.com/modelcontextprotocol/go-sdk` **v1.7.0**.
- Windows system dependency: `golang.org/x/sys` **v0.47.0**.

Go 1.25.0 is the minimum because the design uses `os.Root.MkdirAll`,
`os.Root.ReadFile`, and `os.Root.Rename`, which were added in Go 1.25. The MCP
Go SDK v1.7.0 and x/sys v0.47.0 also declare Go 1.25.0 as their minimum.
Release builds use a current supported toolchain because standard-library patch
releases include security fixes, including fixes to `os`.

Primary references:

- [MCP Go SDK v1.7.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.7.0)
- [MCP Go SDK server guide](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/docs/server.md)
- [`os.Root` package documentation](https://pkg.go.dev/os#Root)
- [Go traversal-resistant filesystem API](https://go.dev/blog/osroot)
- [Go release policy and history](https://go.dev/doc/devel/release)
- [Go x/sys Windows APIs](https://pkg.go.dev/golang.org/x/sys/windows)
- [OpenAI Secure MCP Tunnel client](https://github.com/openai/tunnel-client)

## System architecture

```text
ChatGPT
   |
   | OpenAI-hosted tunnel connection
   v
official tunnel-client (external)
   |
   | launches one local stdio child
   v
cmd/computer-agent
   |
   v
internal/mcp  ------ typed translation only ------+
   |                                             |
   +--> internal/policy --> internal/files ------+
   |                         (`os.Root`)          |
   +--> internal/command --> internal/platform --+
   +--> internal/processes -^                    |
   +--> internal/system -------------------------+
```

`internal/mcp` is deliberately thin. It registers the tools allowed by the
active mode, converts typed SDK requests to module calls, and converts results
to MCP responses. It contains no path authorization, file operations, command
launching, process ownership, or platform behavior.

## Module interfaces and seams

### `internal/config`

Loads one strict JSON document with `json.Decoder.DisallowUnknownFields`,
applies defaults, validates every limit, and returns an immutable runtime
configuration. It does not open roots, operate on files, or register tools.

### `internal/policy`

Owns permission-mode capability checks and the approved-root set. During
startup it canonicalizes each configured path and opens the corresponding
`os.Root`. A root is selected by configured alias, never inferred from an
arbitrary absolute path. It returns an already-authorized root reference plus a
validated relative path and closes every root during shutdown.

### `internal/files`

Owns all observation/mutation file behavior through authorized roots supplied
by `internal/policy`. Its public Go interface uses typed requests and results
corresponding to the file tool schemas below. Tests call it directly with
temporary root sets.

### `internal/command`

Runs a single direct executable and argument array as the current user. It owns
timeouts, cancellation, environment construction, stdout/stderr capture, caps,
exit reporting, and foreground process-tree cleanup. It depends on the small
platform process interface, not MCP.

### `internal/processes`

Owns a bounded in-memory registry of background processes. It starts processes
through the same command launcher, retains bounded stdout and stderr, reports
state, stops only registry-owned process IDs, evicts old completed records when
capacity is needed, and shuts down every still-owned tree. It has no terminal
or session semantics.

### `internal/platform`

The only real platform seam. POSIX and Windows adapters implement process-tree
start, wait, graceful-stop where supported, hard-stop, and close. Build-tagged
files contain the necessary OS behavior.

### `internal/system`

Returns bounded, non-secret system facts: operating system, architecture,
hostname, server version, active mode, configured roots, and enabled
capabilities.

### `internal/mcp`

Creates the SDK server, registers only allowed tools, applies tool annotations,
and runs `mcp.StdioTransport`. Handler context cancellation is passed unchanged
to command and process modules. Tests use the SDK's in-memory transports; the
module itself does not recreate MCP or JSON-RPC behavior.

## Configuration contract

The program accepts `--config <path>`, `--version`, and `--help`. With no
`--config`, it looks in the platform user configuration directory:

- Windows: `%APPDATA%\ChatGPTComputerAgentMCP\config.json`
- macOS: `$HOME/Library/Application Support/ChatGPT Computer Agent MCP/config.json`
- Linux: `$XDG_CONFIG_HOME/chatgpt-computer-agent-mcp/config.json`, falling back
  to `$HOME/.config/chatgpt-computer-agent-mcp/config.json`

Missing or invalid configuration is a startup error with a concrete path and
remediation. There is no zero-configuration unrestricted mode.

```json
{
  "version": 1,
  "mode": "readonly",
  "roots": [
    {
      "name": "workspace",
      "path": "/absolute/path/to/an/approved/directory"
    }
  ],
  "limits": {
    "max_read_bytes": 1048576,
    "max_write_bytes": 2097152,
    "default_command_timeout_seconds": 120,
    "max_command_timeout_seconds": 600,
    "max_output_bytes_per_stream": 1048576,
    "max_background_processes": 8,
    "process_stop_grace_seconds": 2
  }
}
```

Configuration rules:

- `version` must be `1`.
- `mode` is `readonly`, `workspace`, or `user-shell`.
- At least one root is required.
- Root names are unique and match `^[A-Za-z][A-Za-z0-9_-]{0,31}$`.
- Root paths are absolute, existing directories.
- During policy initialization each root path is made absolute, evaluated
  through existing symlinks, and opened with `os.OpenRoot`; startup fails if
  any step fails.
- Overlapping roots are allowed because aliases make selection explicit.
- On POSIX, a group/world-writable configuration file is rejected. The file
  contains no secrets, so group/world readability alone is not rejected.
- Numeric limits are positive. Command timeout limits may not exceed 3600
  seconds, file/output limits may not exceed 8 MiB per configured unit,
  background capacity may not exceed 32, and stop grace may not exceed 30
  seconds.
- Unknown keys are errors at every object level.

## Permission modes

Modes are nested capability sets. Disallowed tools are not registered and
therefore do not appear in MCP discovery.

| Capability | `readonly` | `workspace` | `user-shell` |
|---|:---:|:---:|:---:|
| `system_info` | yes | yes | yes |
| `read_file` | yes | yes | yes |
| `list_directory` | yes | yes | yes |
| `file_info` | yes | yes | yes |
| `create_directory` | no | yes | yes |
| `write_file` | no | yes | yes |
| `edit_file` | no | yes | yes |
| `run_command` | no | no | yes |
| four `process_*` tools | no | no | yes |
| privileged execution | no | no | no |

Selecting `user-shell` is explicit authorization for arbitrary commands as the
current user. Filesystem roots constrain file tools and validate the command's
initial working directory. They **do not confine what a command can read,
write, execute, or access over the network**.

## Approved-root path model

Every file path and command working directory is a pair:

```json
{ "root": "workspace", "path": "project/src/main.go" }
```

- `root` selects one configured alias.
- `path` is relative to that root. `.` names the root directory itself.
- Absolute paths, UNC paths, Windows volume-qualified or drive-relative paths,
  NUL bytes, and lexical escapes are rejected before filesystem access.
- `~`, environment variables, globs, and shell substitutions are never
  expanded.
- Forward slashes work as portable separators. Native separators are also
  accepted where the operating system accepts them.
- Input is cleaned and bounded before passing it to `os.Root`.
- `os.Root` is the final authorization mechanism for file operations and
  rejects symlink traversal outside the approved tree.

For command working directories, authorization records the directory identity
returned through `os.Root`. The platform adapter stats the native path again
immediately before process creation and rejects an identity change. Supported
cross-platform process APIs still consume a path rather than that open root
handle, so a same-user actor can replace the path between the final check and
the OS consuming it. This residual point-in-time race is not a sandbox escape
because `user-shell` already authorizes arbitrary current-user effects, but it
limits the cwd identity guarantee.

### `os.Root` is not a complete filesystem sandbox

`os.Root` provides traversal-resistant pathname access. It does not prohibit
crossing filesystem mount points, Linux bind mounts, mounted `/proc` trees,
device nodes, or other special files. It also cannot turn a hard link located
inside a root into a distinct file. The file module therefore reads only
regular files, writes only regular or nonexistent final targets, rejects
FIFOs/devices/sockets, bounds all I/O, and does not expose recursive deletion.

Those checks reduce risk but do not make the root a sandbox. Operators must not
approve a root containing hostile mounts, device trees, sensitive hard links,
or untrusted special filesystem behavior. Root restrictions never apply to
commands in `user-shell` mode.

## File semantics

- File content tools are UTF-8 text only.
- Reads reject invalid UTF-8, NUL-containing content, non-regular files, and
  content above the configured cap.
- Directory listing is paginated and does not recursively walk.
- Metadata uses `lstat` semantics so a final symlink is identified as a link.
- Writes default to no overwrite. Overwrite requires `overwrite: true`.
- Parent creation requires `create_parents: true`.
- Writes use a temporary regular file in the destination directory, flush and
  close it, then rename it within the same `os.Root`. This prevents readers from
  seeing partial content; it does not promise survival across power loss on
  every filesystem.
- Existing regular-file permissions are preserved through the opened temporary
  file where the platform supports them. No ownership or permission-changing
  tool is exposed.
- Edits replace exactly one occurrence. Missing or repeated matches leave the
  file unchanged.
- No delete, move, symlink, hard-link, chmod, or recursive tree tool exists in
  v1.

## Command execution model

`run_command` and `process_start` receive an executable and argument array.
The server calls the operating-system process API directly. It never parses an
implicit command string and never inserts `sh -c`, `bash -c`, `cmd /c`, or
PowerShell.

Shell behavior is possible only when the caller explicitly selects a shell as
the executable and supplies that shell's arguments. Such a call is visibly and
semantically a shell invocation.

Commands:

- run as the current non-elevated user
- use a validated approved-root directory as their initial working directory,
  with identity revalidation immediately before process creation
- resolve a bare executable through `PATH`/`PATHEXT`; separator-containing
  relative executables are resolved against that working directory
- receive a small platform baseline environment rather than the server's full
  environment
- never receive tunnel runtime credentials from the server
- have separate stdout and stderr pipes and independent byte caps
- continue draining output after a cap is reached
- return exit code, signal/termination reason, duration, timeout state, and
  truncation flags
- have their owned process tree stopped on timeout, MCP cancellation, or server
  shutdown
- promptly terminate descendants still addressable through the owned group/job
  when the direct foreground executable exits naturally; the MCP reaps the
  direct child, not arbitrary descendants reparented elsewhere

The POSIX child environment contains only `PATH`, `HOME`, `USER`, `LOGNAME`,
`SHELL`, `TMPDIR`, `LANG`, `TERM`, and existing `LC_*` variables. The Windows
baseline contains only `Path`, `SystemRoot`, `ComSpec`, `PATHEXT`, `TEMP`,
`TMP`, `USERPROFILE`, `APPDATA`, `LOCALAPPDATA`, `HOMEDRIVE`, and `HOMEPATH`,
using case-insensitive lookup. Absent variables stay absent. V1 has no tool or
configuration option for forwarding additional environment variables.

This reduces accidental credential inheritance; it does not prevent a
user-shell command from reading credentials available to the current user from
files, credential helpers, operating-system agents, or other local services.
Commands and their arguments are not written to an audit log by this project.

## Managed-process lifecycle

Managed processes survive individual tool calls only. Registry state is
in-memory and is not restored after an MCP runtime restart.

- `process_start` allocates a random, opaque 128-bit process ID. Tools never
  accept an operating-system PID as authority.
- A registry slot owns one process tree, stdout buffer, stderr buffer, and state
  record.
- Each stream retains at most `max_output_bytes_per_stream`; additional bytes
  are drained and discarded with a truncation flag.
- Registry size is bounded by `max_background_processes`. A start first evicts
  the oldest completed record; it never evicts a running process. If every slot
  is running, start fails.
- Completed output remains available until its record is evicted or the MCP
  runtime exits.
- `process_stop` is idempotent for a record that still exists.
- The directly launched executable is the lifetime anchor. After a requested
  graceful stop, descendants may finish within the configured grace period even
  if that executable exits first; surviving owned descendants are force-stopped
  only after the deadline. On natural leader exit, remaining owned descendants
  are force-stopped promptly.
- This process reaps its direct child. It attempts to terminate ordinary
  descendants through the process group or Job Object, but arbitrary POSIX
  descendants are not necessarily reaped here after reparenting. V1 does not
  adopt them as new managed processes or act as a subreaper/container init.
- Self-daemonizing programs and programs that intentionally escape their
  inherited process group are unsupported. This is process ownership, not a
  hostile-code sandbox.

Orderly shutdown is triggered by stdio EOF, parent context cancellation,
SIGINT, or SIGTERM. The server stops accepting work, cancels foreground
commands, stops all registered process trees, waits the configured grace period
where the platform supports graceful termination, hard-kills remaining owned
trees, waits for the direct children and registered executions to complete,
closes root handles, and exits.

POSIX uses a new process group and sends TERM followed by KILL to the group.
Windows uses a kill-on-close Job Object assigned before the target is released
to run; timeout and shutdown terminate the job. Windows cannot provide a
portable POSIX-style graceful signal to every console and GUI child, so stop is
best-effort graceful and then job termination. Native test cases cover ordinary
descendant cleanup on each supported operating system; only the Linux cases have
been executed locally for v1.0.0.

No process manager can run cleanup after power loss or an uncatchable runtime
termination. The shutdown guarantee applies to orderly shutdown and to process
trees still owned by the platform mechanism.

## Public MCP tool contract

All names are stable, lowercase snake_case. Every input and output is a closed
JSON object (`additionalProperties: false`). Required strings reject NUL and
have explicit length caps. JSON Schema `maxLength` counts Unicode characters,
while runtime safety limits count UTF-8 bytes; each affected input schema says
that schema validation alone is not the byte-limit guarantee. Timestamps are
RFC 3339 UTC strings; durations are integer milliseconds; byte offsets and
sizes count raw bytes.

The common root/path object used by command working directories is:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `root` | string | yes | Configured root name |
| `path` | string | yes | Relative directory path or `.`; at most 4096 bytes |

Each system root summary is the closed object `{name, path, readable,
writable}`. Each directory entry is the closed object `{name, type, size,
modified_at}`. No MCP `roots` capability is used; approved roots are this
server's local policy concept.

Successful tools return both human-readable text content and the documented
structured content. Domain failures return `isError: true` with a stable
uppercase code followed by a concise message. Exact human-readable wording is
not a compatibility contract. Invalid JSON/schema input is rejected by the MCP
SDK before a handler runs.

Common error codes are `INVALID_INPUT`, `MODE_DENIED`, `ROOT_NOT_FOUND`,
`PATH_DENIED`, `NOT_FOUND`, `NOT_FILE`, `NOT_DIRECTORY`, `NOT_TEXT`,
`TOO_LARGE`, `ALREADY_EXISTS`, `PERMISSION_DENIED`, `LAUNCH_FAILED`, `TIMED_OUT`,
`PROCESS_NOT_FOUND`, `PROCESS_LIMIT`, and `INTERNAL_ERROR`.

### Tool annotations and availability

| Tool | Modes | `readOnlyHint` | `destructiveHint` | `idempotentHint` | `openWorldHint` |
|---|---|:---:|:---:|:---:|:---:|
| `system_info` | all | true | false | true | false |
| `read_file` | all | true | false | true | false |
| `list_directory` | all | true | false | true | false |
| `file_info` | all | true | false | true | false |
| `create_directory` | workspace, user-shell | false | false | true | false |
| `write_file` | workspace, user-shell | false | true | false | false |
| `edit_file` | workspace, user-shell | false | true | false | false |
| `run_command` | user-shell | false | true | false | true |
| `process_start` | user-shell | false | true | false | true |
| `process_status` | user-shell | true | false | true | false |
| `process_output` | user-shell | true | false | true | false |
| `process_stop` | user-shell | false | true | true | false |

Annotations are descriptive hints, not authorization. Registration by mode and
module-level policy checks are the authorization controls.

### `system_info`

Purpose: report bounded system and capability information without enumerating
the environment, network, processes, or secrets. When invoked, it exposes the
local hostname and configured approved-root paths to ChatGPT.

Input schema: empty object.

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `server_version` | string | Program version |
| `os` | `windows`, `darwin`, or `linux` | Native OS |
| `architecture` | `amd64` or `arm64` | Native architecture |
| `hostname` | string | Local hostname |
| `mode` | permission-mode enum | Active mode |
| `roots` | array of root summaries | `name`, canonical `path`, `readable`, `writable` |
| `commands_enabled` | boolean | Whether command/process tools are registered |
| `managed_processes` | integer | Current registry record count |

### `read_file`

Purpose: read one bounded regular UTF-8 text file inside an approved root.

Input schema:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `root` | string | yes | Configured root name |
| `path` | string | yes | Relative regular-file path, 1..4096 bytes |
| `max_bytes` | integer | no | 1..configured read cap; defaults to the cap |

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `root` | string | Selected root |
| `path` | string | Clean relative path |
| `content` | string | UTF-8 content |
| `bytes` | integer | Raw byte length |
| `sha256` | string | Lowercase SHA-256 hex digest |

### `list_directory`

Purpose: return one bounded page of immediate directory entries.

Input schema:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `root` | string | yes | Configured root name |
| `path` | string | yes | Relative directory path or `.` |
| `offset` | integer | no | Minimum 0; default 0 |
| `limit` | integer | no | 1..200; default 100 |

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `root` | string | Selected root |
| `path` | string | Clean relative path |
| `entries` | array | Each item has `name`, `type`, `size`, `modified_at` |
| `offset` | integer | Starting offset used |
| `next_offset` | integer or null | Offset for the next page |
| `has_more` | boolean | Whether another entry was observed |

Entry `type` is `file`, `directory`, `symlink`, or `other`. Pagination follows
native directory order; concurrent directory mutation may change page
boundaries.

### `file_info`

Purpose: inspect one path without following a final symlink.

Input schema: required `root` and `path` strings with the common root/path
constraints.

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `root` | string | Selected root |
| `path` | string | Clean relative path |
| `name` | string | Final path element |
| `type` | file-type enum | `file`, `directory`, `symlink`, or `other` |
| `size` | integer | Platform-reported byte size |
| `mode` | string | Portable textual mode representation |
| `modified_at` | string | RFC 3339 UTC timestamp |
| `link_target` | string or null | Stored symlink target when applicable |

### `create_directory`

Purpose: create a directory inside an approved root.

Input schema:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `root` | string | yes | Configured root name |
| `path` | string | yes | Relative directory path, not `.` |
| `create_parents` | boolean | no | Default `false` |

Output schema: `root`, clean `path`, and boolean `created`. An existing
directory succeeds with `created: false`; an existing non-directory fails.

### `write_file`

Purpose: atomically create or replace one bounded UTF-8 text file.

Input schema:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `root` | string | yes | Configured root name |
| `path` | string | yes | Relative file path, 1..4096 bytes |
| `content` | string | yes | At most configured write cap in UTF-8 bytes |
| `overwrite` | boolean | no | Default `false` |
| `create_parents` | boolean | no | Default `false` |

Output schema: `root`, clean `path`, `bytes`, lowercase `sha256`, and boolean
`created`.

### `edit_file`

Purpose: atomically replace exactly one literal text occurrence.

Input schema:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `root` | string | yes | Configured root name |
| `path` | string | yes | Relative existing regular-file path |
| `old_text` | string | yes | Non-empty literal, at most 512 KiB |
| `new_text` | string | yes | Literal, at most 512 KiB |

The resulting full file must remain within the configured write cap.

Output schema: `root`, clean `path`, `bytes`, `before_sha256`, and
`after_sha256`.

### `run_command`

Purpose: run one direct foreground process and wait for its process tree to
exit, time out, or be cancelled.

Input schema:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `executable` | string | yes | Non-empty, at most 4096 bytes, no NUL |
| `arguments` | array of strings | no | At most 256 items; default empty |
| `cwd` | root/path object | yes | Validated initial working directory |
| `timeout_seconds` | integer | no | 1..configured maximum; configured default |

Each argument is at most 16 KiB, the executable plus arguments may contain at
most 16 KiB of UTF-8 input in total, and every argument is passed as one exact
OS argument. The platform adapter also rejects a command that exceeds the
native process API's encoded command-line limit.

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `stdout` | string | Captured stdout; invalid UTF-8 replaced for JSON |
| `stderr` | string | Captured stderr; invalid UTF-8 replaced for JSON |
| `exit_code` | integer or null | OS exit code when available |
| `termination` | string or null | Signal or platform termination reason |
| `timed_out` | boolean | Whether timeout initiated termination |
| `duration_ms` | integer | Elapsed wall-clock time |
| `stdout_truncated` | boolean | Whether stdout exceeded its cap |
| `stderr_truncated` | boolean | Whether stderr exceeded its cap |

A non-zero exit, timeout, or signal termination returns this structured result
with MCP `isError: true`.

### `process_start`

Purpose: start one direct background process owned by this MCP runtime.

Input schema: `executable`, optional `arguments`, and required `cwd` with the
same constraints as `run_command`. There is no implicit shell and no persistent
session state.

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `process_id` | string | Opaque registry ID |
| `state` | enum | `running` or `exited` |
| `started_at` | string | RFC 3339 UTC timestamp |
| `finished_at` | string or null | Completion timestamp when already exited |
| `exit_code` | integer or null | Exit code when already available |
| `termination` | string or null | Signal/platform reason when already available |

### `process_status`

Purpose: inspect one registry-owned process without changing it.

Input schema: required `process_id` string, 1..128 bytes.

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `process_id` | string | Registry ID |
| `state` | enum | `running`, `exited`, or `stopped` |
| `exit_code` | integer or null | Exit code when available |
| `termination` | string or null | Signal/platform reason |
| `started_at` | string | Start timestamp |
| `finished_at` | string or null | Completion timestamp |
| `duration_ms` | integer | Current/final elapsed time |
| `stdout_bytes` | integer | Retained stdout bytes |
| `stderr_bytes` | integer | Retained stderr bytes |
| `stdout_truncated` | boolean | Whether stdout exceeded its cap |
| `stderr_truncated` | boolean | Whether stderr exceeded its cap |

### `process_output`

Purpose: page through retained stdout or stderr for one managed process.

Input schema:

| Field | Type | Required | Constraints |
|---|---|:---:|---|
| `process_id` | string | yes | Existing registry ID |
| `stream` | enum | yes | `stdout` or `stderr` |
| `offset` | integer | no | Raw byte offset, minimum 0; default 0 |
| `max_bytes` | integer | no | 1..65536; default 65536 |

Output schema:

| Field | Type | Meaning |
|---|---|---|
| `process_id` | string | Registry ID |
| `stream` | enum | Selected stream |
| `data` | string | Requested bytes represented as UTF-8 JSON text |
| `offset` | integer | Requested starting raw-byte offset |
| `next_offset` | integer | Next raw-byte offset |
| `end_of_stream` | boolean | Process finished and no later retained bytes exist |
| `truncated` | boolean | Stream exceeded retained-output capacity |

Invalid UTF-8 sequences are replaced for JSON rendering; offsets continue to
refer to the original raw retained bytes.

### `process_stop`

Purpose: stop one registry-owned process tree using the configured grace
period and return its final status.

Input schema: required `process_id` string, 1..128 bytes.

Output schema: the same fields as `process_status`. Repeating stop for an
existing completed record succeeds without launching or signaling anything.

## Error and logging behavior

- Validation errors do not reach module implementations.
- File and policy errors expose stable codes but not host stack traces.
- Launch errors name the executable and category without dumping the full
  environment.
- Tool input, file content, command output, and command arguments are not logged
  by default.
- Stdout is reserved exclusively for MCP frames. Startup diagnostics and fatal
  lifecycle errors go to stderr.
- Output returned to the model is untrusted data and may contain prompt
  injection or secrets. The server does not claim reliable heuristic redaction.

## Platform behavior

### Windows

- Bare executable lookup and `PATHEXT` handling use Go's native Windows lookup;
  separator-relative paths are first anchored to the validated cwd. Argument
  quoting uses the Windows process API; no WSL is required.
- Approved roots use Windows path and reparse-point behavior provided by
  `os.Root`; volume-qualified, drive-relative, UNC, and reserved device-name
  cases receive native tests.
- A Job Object owns each command tree. No UAC or Administrator path exists.
- PowerShell or `cmd.exe` runs only when explicitly named as the executable.

### macOS

- Native paths and POSIX process groups are used.
- No Homebrew dependency exists at runtime.
- Shells run only when explicitly named.

### Linux

- Native paths and POSIX process groups are used.
- No assumption is made about systemd, apt, dnf, Docker, Bash, or a desktop
  environment.
- Shells run only when explicitly named.

Platform-specific implementation stays limited to process ownership and
termination. File and policy behavior remains shared Go code.

## Security model

ChatGPT and all content it reads are treated as an untrusted-but-authorized
controller. Prompt injection may persuade it to request harmful operations.
Modes and approved roots reduce available capability; they do not make model
decisions trustworthy.

- `readonly` is the default recommended starting mode.
- `workspace` permits irreversible overwrite/edit operations inside approved
  roots but exposes no command tool.
- `user-shell` permits arbitrary current-user effects, including effects
  outside roots and network access.
- No operation elevates privilege automatically or exposes a privileged tool.
- Secure MCP Tunnel prevents a required public listener and provides the
  supported connectivity path. It does not make local file or command tools
  inherently safe.
- Untrusted repositories may contain executable hooks, build scripts, tool
  configuration, malicious filenames, or prompt-injection text.
- Files and command output may disclose secrets. Users should choose narrow
  roots, avoid production machines, and review tool approvals.
- This program is not a sandbox.

## Testing strategy

TDD is required for implementation. Tests use each module's public Go
interface; the command launcher also has an unexported process-start seam for
deterministically exercising cwd replacement between authorization and launch.
MCP transport tests verify translation and discovery rather than retesting
module internals.

### File and policy tests

- reads, listings, metadata, writes, and exact edits
- missing paths, permission errors, wrong file types, invalid UTF-8, NUL, and
  size caps
- spaces and Unicode in paths and content
- `..`, absolute, UNC, volume-qualified, drive-relative, and normalization
  attempts
- internal symlinks and escaping symlinks
- special-file refusal where the host supports fixtures
- root aliases, unknown roots, all three modes, and absent disallowed tools

### Command and process tests

- success, non-zero exit, stdout, stderr, Unicode, spaces, invalid executable,
  and working directory
- timeout, MCP cancellation, large-output draining, and separate caps
- descendant process cleanup after timeout, stop, cancellation, and orderly
  shutdown
- background lifecycle, status transitions, output paging, truncation,
  capacity, eviction, and idempotent stop
- environment allowlist and explicit absence of representative tunnel/secret
  variables
- Windows Job Object and POSIX process-group behavior on native runners

### MCP tests

- initialization and negotiated stdio/in-memory session
- exact tool discovery for each mode
- valid typed calls and structured content
- malformed and unknown payloads
- domain errors versus protocol errors
- cancellation propagation
- stdin closure and clean shutdown

### Cross-platform verification

- The configured CI matrix contains native Ubuntu, Windows, and macOS jobs and
  race-enabled tests where supported.
- Local native Linux tests, race tests, vet, and the repository check have run
  for v1.0.0.
- All six release targets cross-build locally with `CGO_ENABLED=0`.
- GitHub-hosted CI has run the native matrix successfully. Stable v1.0.0 was
  validated end to end on Windows (install with SHA256 verification, the
  `configure` command, Secure MCP Tunnel readiness, real ChatGPT MCP tool
  calls, and harmless `user-shell` command execution). Equivalent manual
  end-to-end acceptance has not yet been performed on Linux or macOS.

## CI and release preparation

CI is configured to enforce formatting, `go vet`, `go test ./...`, race tests
where supported, static analysis, `govulncheck`, secret scanning, native OS
tests, and six-target cross-builds. Third-party actions are pinned to immutable
commit SHAs.

For a `v*` tag, release build and publication jobs run in the same workflow and
exact SHA as those validation gates. Publication depends on every gate and is
the only job granted `contents: write`. The release build creates:

```text
chatgpt-computer-agent-mcp-windows-amd64.exe
chatgpt-computer-agent-mcp-windows-arm64.exe
chatgpt-computer-agent-mcp-darwin-amd64
chatgpt-computer-agent-mcp-darwin-arm64
chatgpt-computer-agent-mcp-linux-amd64
chatgpt-computer-agent-mcp-linux-arm64
LICENSE
THIRD_PARTY_NOTICES
SHA256SUMS
```

Install scripts install only the project binary into a user-level location,
verify it against `SHA256SUMS`, preserve configuration on uninstall, and never
change firewalls or install development runtimes. Installers require
`COMPUTER_AGENT_REPOSITORY` to be set to the published owner/repository before
use.

## Secure MCP Tunnel integration

The documented workflow is:

```text
ChatGPT
  -> OpenAI Secure MCP Tunnel
  -> official tunnel-client
  -> this binary with --config over stdio
  -> local policy/files/commands/processes
```

Tunnel IDs and runtime credentials belong to the official client and user
environment, never this configuration. Examples use placeholders. The setup
guide directs users to the official client's profile initialization,
`doctor --explain`, readiness checks, and ChatGPT Tunnel connection flow. This
project does not accept, store, print, or forward tunnel credentials.

## Decisions and rejected alternatives

### Official Go SDK instead of handwritten MCP

The SDK provides protocol negotiation, typed tools, schema validation,
structured content, annotations, stdio, in-memory tests, and cancellation.
Reimplementing these creates more code and protocol risk without product value.

### `os.Root` instead of lexical prefix checks

Prefix and `filepath.Rel` checks are vulnerable to symlink and race behavior.
`os.Root` is the standard traversal-resistant mechanism on the supported native
platforms. Its non-sandbox limits remain explicit.

### Root aliases and relative paths instead of arbitrary absolute paths

Aliases make authority explicit in every call, avoid drive/case ambiguity, and
keep the authorization seam small. Absolute paths remain configuration only.

### Direct argv instead of a command string

Direct invocation preserves argument boundaries across platforms and avoids
introducing a hidden shell. Explicit shell calls remain possible and obvious.

### Small process registry instead of sessions

Four process lifecycle tools are sufficient for development servers and other
long-running tasks. PTYs, stdin streaming, shell persistence, and terminal
state are separate products and remain excluded.

### No privileged lane in v1

Cross-platform elevation introduces a new trust boundary and conflicts with
ordinary-user operation. Omitting it is safer and smaller than shipping a
nominally gated path whose guarantees vary by operating system and MCP client.

## Design consistency checklist

- Every tool maps to exactly one independently testable module operation.
- Only `internal/mcp` imports the MCP SDK.
- File tools always require an approved root alias and relative path.
- File authorization uses `os.Root`, with its mount/proc/device/hard-link
  limitations documented.
- Command working directories are authorized through roots and revalidated by
  identity immediately before launch, with the residual path race documented;
  commands are explicitly not filesystem-confined.
- Commands default to direct executable plus argument array.
- Background scope is exactly start, status, output, and stop.
- Managed state lasts only for the current MCP runtime.
- Orderly shutdown reaps direct children and attempts to terminate descendants
  still addressable through the owned process group or Job Object.
- No privileged, network-listener, SSH, terminal, or bundled-tunnel feature is
  present.
- Go 1.25.0 is the declared minimum for every selected `os.Root` method and
  pinned dependency; Go 1.27.0 is the release toolchain.
- Native runtime testing and cross-compilation are reported separately.
