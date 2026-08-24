#!/bin/sh
set -eu

if [ -z "${VERSION:-}" ]; then
  echo "VERSION is required (for example, v1.0.0)" >&2
  exit 2
fi

repository_dir="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
output_dir="${OUTPUT_DIR:-$repository_dir/dist}"
for file in LICENSE THIRD_PARTY_NOTICES; do
  if [ ! -s "$repository_dir/$file" ]; then
    echo "required release license file is missing or empty: $file" >&2
    exit 2
  fi
done
mkdir -p "$output_dir"
if [ -n "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
  echo "output directory must be empty: $output_dir" >&2
  exit 2
fi

program_version="${VERSION#v}"
for target in windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  target_os="${target%/*}"
  target_arch="${target#*/}"
  suffix=""
  if [ "$target_os" = "windows" ]; then
    suffix=".exe"
  fi
  name="chatgpt-computer-agent-mcp-$target_os-$target_arch$suffix"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$program_version" \
    -o "$output_dir/$name" "$repository_dir/cmd/computer-agent"
done

cp "$repository_dir/LICENSE" "$repository_dir/THIRD_PARTY_NOTICES" "$output_dir/"

(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum LICENSE THIRD_PARTY_NOTICES chatgpt-computer-agent-mcp-* > SHA256SUMS
  else
    shasum -a 256 LICENSE THIRD_PARTY_NOTICES chatgpt-computer-agent-mcp-* > SHA256SUMS
  fi
)
