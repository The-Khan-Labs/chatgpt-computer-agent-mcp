# Security Policy

## Supported versions

| Version | Supported |
|---|---|
| v1.0.0 | yes |
| earlier versions | no |

## Reporting a vulnerability

Do not include exploit details, credentials, private paths, or sensitive file/command output in a public issue. After publication, use the repository's GitHub private vulnerability reporting channel. If that channel is unavailable, contact the project owner through the channel from which you received the software and request a private reporting path.

Include the affected commit/version, operating system and architecture, active permission mode, minimal reproduction, and expected impact. Never use production credentials or another person's data in a report.

## Security boundaries

- File tools are limited to configured aliases backed by `os.Root`, accept relative paths only, and refuse special files for content operations.
- Command working-directory identity is revalidated immediately before process creation. Supported process APIs still consume a path, leaving a same-user replacement race after that final check.
- `workspace` mutations are bounded and atomically published from the destination directory. No delete or permission-changing tool exists.
- `user-shell` deliberately permits arbitrary current-user commands. It is not a sandbox and has no elevation path.
- Command environments use a small allowlist; this reduces accidental credential forwarding but does not stop a command from reaching credentials available elsewhere to the current user.
- The MCP reaps its direct child and attempts to terminate descendants through POSIX process groups or Windows Job Objects. Arbitrary POSIX descendants reparented elsewhere are not reaped by this process. Self-daemonizing processes that intentionally escape ownership are unsupported.
- The MCP server is stdio-only. Secure MCP Tunnel credentials belong to the external official client and must never be placed in this server's JSON configuration.
- `os.Root` does not block mount crossings, hostile device trees, or sensitive hard links already present within an approved root. Approve roots accordingly.

See [docs/architecture.md](docs/architecture.md) for the complete threat model and exclusions.
