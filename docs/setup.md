# Setup

## 1. Install the binary

Install a prebuilt binary from the [GitHub releases](https://github.com/The-Khan-Labs/chatgpt-computer-agent-mcp/releases) of `The-Khan-Labs/chatgpt-computer-agent-mcp`. Each release contains six raw binaries, `LICENSE`, `THIRD_PARTY_NOTICES`, and `SHA256SUMS`. The verified installer scripts download the binary for your platform, check it against `SHA256SUMS`, and install only the binary into a user-level directory. Do not use GitHub's "Download ZIP" button to install; that is the source code, not a release.

On Linux or macOS:

```sh
curl --proto '=https' --tlsv1.2 -fsSLO \
  https://raw.githubusercontent.com/The-Khan-Labs/chatgpt-computer-agent-mcp/v1.0.0/scripts/install.sh
COMPUTER_AGENT_REPOSITORY=The-Khan-Labs/chatgpt-computer-agent-mcp \
COMPUTER_AGENT_VERSION=v1.0.0 \
sh ./install.sh
```

On Windows PowerShell:

```powershell
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/The-Khan-Labs/chatgpt-computer-agent-mcp/v1.0.0/scripts/install.ps1" -OutFile install.ps1
$env:COMPUTER_AGENT_REPOSITORY = "The-Khan-Labs/chatgpt-computer-agent-mcp"
$env:COMPUTER_AGENT_VERSION = "v1.0.0"
.\install.ps1
```

From a repository clone, the same scripts are at `scripts/install.sh` and `scripts\install.ps1`. The repository's Windows script adds its install directory to the current user's PATH (never the machine PATH) and refreshes PATH for the current PowerShell process, so `computer-agent` is available immediately. On Linux and macOS, the repository script does not edit shell profiles; if the install directory is not already on PATH, it prints the command to run and persist.

`--uninstall` (or `-Uninstall`) removes only the installed binary. Configuration is preserved. The Windows user PATH entry is also preserved because the installer does not track whether an equivalent entry was configured independently. Neither installer requests elevation, changes firewall rules, nor installs Go or the tunnel client.

Building from source remains available for developers:

```sh
go version
# Expected development toolchain: go version go1.27.0 <os>/<arch>
go build -trimpath -o computer-agent ./cmd/computer-agent
./computer-agent --version
```

## 2. Configure the mode and approved folder

Use the built-in `configure` command; no JSON editing is required:

```sh
computer-agent configure --mode readonly --root /absolute/path/to/your/projects
```

The three permission modes:

- `readonly` = inspect (read, list, metadata)
- `workspace` = inspect + create/edit files inside the approved roots
- `user-shell` = full coding-agent mode: arbitrary commands as your current OS user

`user-shell` is not Administrator/root by itself, commands are not sandboxed to the approved roots, and the project never auto-elevates. Advanced users who deliberately launch the runtime with elevated OS authority are responsible for that choice.

On Windows PowerShell:

```powershell
computer-agent configure --mode readonly --root C:\Users\YOU\Projects
```

The first run requires `--root`: an absolute directory that already exists. It creates the platform default configuration with the project's default limits and one approved root named `workspace`:

| Platform | Default configuration |
|---|---|
| Linux | `$XDG_CONFIG_HOME/chatgpt-computer-agent-mcp/config.json`, otherwise `$HOME/.config/chatgpt-computer-agent-mcp/config.json` |
| macOS | `$HOME/Library/Application Support/ChatGPT Computer Agent MCP/config.json` |
| Windows | `%APPDATA%\ChatGPTComputerAgentMCP\config.json` |

To change permissions later, rerun with only `--mode`:

```sh
computer-agent configure --mode workspace
computer-agent configure --mode user-shell
```

That preserves your approved roots and any custom limits. Adding `--root` updates (or adds) only the root named `workspace` and preserves any other roots. Before every update the previous configuration is saved next to it as `config.json.bak`, and the new file is written atomically. Rerunning the same command is safe. Pass `--config <path>` to manage a non-default location.

Start with `readonly`; move to `workspace` or `user-shell` only when the additional authority is required.

Advanced users can still edit the JSON directly (see `config.example.json`). Every root path must be absolute and already exist. On POSIX, keep the file non-writable by group/others:

```sh
chmod 600 /absolute/path/to/config.json
computer-agent --config /absolute/path/to/config.json
```

After changing `mode` (or tool exposure), restart the MCP server/tunnel so the new tool schema takes effect. ChatGPT may then need the connector refreshed or reviewed, or a new conversation, before it discovers the updated tool set.

Missing or invalid configuration is fatal. There is no unrestricted fallback.

## 3. Connect the official Secure MCP Tunnel client

This project does not bundle or reimplement the tunnel. Follow the official [tunnel-client end-user guide](https://github.com/openai/tunnel-client/blob/master/docs/end-user-guide.md) and use its local stdio sample with placeholder values:

```sh
tunnel-client help quickstart
tunnel-client profiles samples list
tunnel-client init \
  --sample sample_mcp_stdio_local \
  --profile local-computer-agent \
  --tunnel-id <TUNNEL_ID> \
  --mcp-command "/absolute/path/to/computer-agent --config /absolute/path/to/config.json"
tunnel-client doctor --profile local-computer-agent --explain
tunnel-client run --profile local-computer-agent
```

Create the runtime key through the official Platform flow with only the documented Tunnels Read + Use permissions, provide it to `tunnel-client` through its supported secret mechanism, and keep it out of this repository and `config.json`. An admin key is only for tunnel CRUD and must not be used as the long-lived runtime key.

Confirm the tunnel client's `/readyz` is healthy, then connect the matching tunnel from ChatGPT connector settings. The official client documentation remains the source of truth for tunnel IDs, profiles, permissions, keys, readiness, and product connection steps.

## 4. Validate locally

Run the full repository gate:

```sh
./scripts/check.sh
```

The configured CI matrix has Linux amd64, Windows amd64, macOS amd64, and macOS arm64 native jobs, with race tests on Linux amd64, Windows amd64, and macOS amd64. It separately cross-builds the complete Linux/Windows/macOS amd64/arm64 release matrix. GitHub-hosted CI has run this matrix successfully.

Stable v1.0.0 was validated end to end on Windows: public install with SHA256 verification, the `configure` command, Secure MCP Tunnel readiness, real ChatGPT MCP tool calls, and harmless `user-shell` command execution. Equivalent manual end-to-end acceptance has not yet been performed on Linux or macOS.
