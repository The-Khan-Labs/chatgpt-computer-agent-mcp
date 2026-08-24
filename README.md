# ChatGPT Computer Agent MCP

A local MCP server that gives ChatGPT controlled access to text files and current-user commands. It runs over stdio behind the official OpenAI Secure MCP Tunnel and does not listen on a network port.

The current stable release is [v1.0.0](https://github.com/The-Khan-Labs/chatgpt-computer-agent-mcp/releases/tag/v1.0.0).

## Quick Start

### Windows

Run in PowerShell:

```powershell
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/The-Khan-Labs/chatgpt-computer-agent-mcp/v1.0.0/scripts/install.ps1" -OutFile install.ps1
$env:COMPUTER_AGENT_REPOSITORY = "The-Khan-Labs/chatgpt-computer-agent-mcp"
$env:COMPUTER_AGENT_VERSION = "v1.0.0"
.\install.ps1
```

### Linux and macOS

```sh
curl --proto '=https' --tlsv1.2 -fsSLO \
  https://raw.githubusercontent.com/The-Khan-Labs/chatgpt-computer-agent-mcp/v1.0.0/scripts/install.sh
COMPUTER_AGENT_REPOSITORY=The-Khan-Labs/chatgpt-computer-agent-mcp \
COMPUTER_AGENT_VERSION=v1.0.0 \
sh ./install.sh
```

Confirm the installation, then choose a permission mode and an existing absolute directory:

```sh
computer-agent --version

computer-agent configure --mode readonly --root <PATH>
computer-agent configure --mode workspace --root <PATH>
computer-agent configure --mode user-shell --root <PATH>
```

Start with `readonly`. See the [setup guide](docs/setup.md) to connect the configured server through the official Secure MCP Tunnel client.

## Permission Modes

| Mode | Access |
|---|---|
| `readonly` | Read, list, and inspect files inside approved roots |
| `workspace` | `readonly` plus create and edit files inside approved roots |
| `user-shell` | `workspace` plus arbitrary commands as the current OS user |

`user-shell` allows arbitrary commands as the current OS user. Approved roots constrain file tools and validate a command's initial working directory, but they do not sandbox `user-shell` commands or limit what those commands can access. `user-shell` is not automatically Administrator/root, and the project never auto-elevates or requests UAC/sudo.

## How It Works

```text
ChatGPT
  ↓
Official OpenAI Secure MCP Tunnel
  ↓
local computer-agent
  ↓
approved file roots / current-user commands
```

The tunnel launches `computer-agent` as a local stdio child. The server exposes only the tools allowed by the configured mode. Detailed module boundaries, tool contracts, and data flow are documented in [docs/architecture.md](docs/architecture.md).

## Security

- File operations use Go's traversal-resistant `os.Root` APIs and stay within explicitly approved roots.
- Configuration is validated strictly; missing or invalid configuration does not fall back to unrestricted access.
- Commands use an executable and argument array without an implicit shell.
- The server has no HTTP listener, SSH tool, delete tool, privilege escalation, or built-in tunnel client.
- Background processes are bounded, owned by the runtime, and stopped when it shuts down.

Read [SECURITY.md](SECURITY.md) and the [architecture security model](docs/architecture.md#approved-root-path-model) before enabling `user-shell`.

## Supported Platforms

Stable v1.0.0 release binaries are available for Linux, Windows, and macOS on amd64 and arm64.

CI runs native validation on Linux amd64, Windows amd64, macOS amd64, and macOS arm64. Race tests run natively on Linux amd64, Windows amd64, and macOS amd64; the complete six-target release matrix is cross-built.

Windows received real end-to-end public acceptance through installation, configuration, Secure MCP Tunnel readiness, and ChatGPT tool calls. Equivalent manual Linux or macOS end-to-end acceptance has not been performed.

## Development / Verification

Go 1.25.0 is the module minimum; development and releases use Go 1.27.0.

```sh
go build -trimpath -o computer-agent ./cmd/computer-agent
./scripts/check.sh
```

The repository check covers formatting, module verification, tests, race detection, vet, static and vulnerability analysis, installer fixtures, cross-builds, and sensitive-reference scanning.

## Documentation

- [Setup and Secure MCP Tunnel connection](docs/setup.md)
- [Architecture and tool contracts](docs/architecture.md)
- [Security policy](SECURITY.md)
- [Example configuration](config.example.json)
- [Apache License 2.0](LICENSE)
- [Third-party notices](THIRD_PARTY_NOTICES)
