#!/bin/sh
set -eu

repository="${COMPUTER_AGENT_REPOSITORY:-}"
version="${COMPUTER_AGENT_VERSION:-}"
install_dir="${COMPUTER_AGENT_INSTALL_DIR:-${HOME}/.local/bin}"
target="$install_dir/computer-agent"

if [ "${1:-}" = "--uninstall" ]; then
  rm -f -- "$target"
  echo "Removed $target; configuration was preserved."
  exit 0
fi
if [ "$#" -ne 0 ]; then
  echo "usage: install.sh [--uninstall]" >&2
  exit 2
fi
if [ -z "$repository" ]; then
  echo "COMPUTER_AGENT_REPOSITORY is unset; set it to the published owner/repository before installing." >&2
  exit 2
fi
if [ -z "$version" ]; then
  echo "COMPUTER_AGENT_VERSION is required (for example, v1.0.0)." >&2
  exit 2
fi

case "$(uname -s)" in
  Linux) target_os="linux" ;;
  Darwin) target_os="darwin" ;;
  *) echo "unsupported operating system" >&2; exit 2 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) target_arch="amd64" ;;
  arm64|aarch64) target_arch="arm64" ;;
  *) echo "unsupported architecture" >&2; exit 2 ;;
esac

name="chatgpt-computer-agent-mcp-$target_os-$target_arch"
base_url="https://github.com/$repository/releases/download/$version"
download_dir="$(mktemp -d)"
trap 'rm -rf "$download_dir"' EXIT HUP INT TERM
curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
  -o "$download_dir/$name" "$base_url/$name"
curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
  -o "$download_dir/SHA256SUMS" "$base_url/SHA256SUMS"

expected="$(awk -v name="$name" '$2 == name { print $1 }' "$download_dir/SHA256SUMS")"
if ! printf '%s\n' "$expected" | grep -Eq '^[0-9a-fA-F]{64}$'; then
  echo "SHA256SUMS does not contain exactly one valid checksum for $name" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$download_dir/$name" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "$download_dir/$name" | awk '{ print $1 }')"
fi
if [ "$actual" != "$expected" ]; then
  echo "SHA-256 verification failed for $name" >&2
  exit 1
fi

mkdir -p "$install_dir"
temporary_target="$(mktemp "$install_dir/.computer-agent.XXXXXX")"
trap 'rm -f "$temporary_target"; rm -rf "$download_dir"' EXIT HUP INT TERM
install -m 0755 "$download_dir/$name" "$temporary_target"
mv -f -- "$temporary_target" "$target"
echo "Installed $target"
case ":${PATH:-}:" in
  *":$install_dir:"*) echo "You can now run: computer-agent" ;;
  *)
    echo "$install_dir is not on your PATH."
    echo "For this shell, run: export PATH=\"$install_dir:\$PATH\""
    echo "Add the same line to your shell profile to keep it available."
    ;;
esac
