#!/bin/sh
set -eu

repository_dir="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$repository_dir"

for file in LICENSE THIRD_PARTY_NOTICES; do
  if [ ! -s "$file" ]; then
    echo "required release license file is missing or empty: $file" >&2
    exit 1
  fi
done

unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
  echo "gofmt is required for:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go mod verify
go test ./...
go test -race ./...
go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0 run --timeout=5m --default=none --enable=errcheck --enable=staticcheck
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
RUN_RELEASE_SCRIPT_TEST=1 go test ./scripts

build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT HUP INT TERM
for target in linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64; do
  target_os="${target%/*}"
  target_arch="${target#*/}"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
    go build -o "$build_dir/$target_os-$target_arch" ./cmd/computer-agent
done

set +e
scan_output="$(rg -n -i -e \
  "-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----|github_pat_[A-Za-z0-9_]{20,}|ghp_[A-Za-z0-9]{20,}|tunnel_[A-Za-z0-9]{32,}" \
  --hidden --glob '!.git/**' . 2>&1)"
scan_status=$?
set -e
case "$scan_status" in
  0) echo "$scan_output"; echo "sensitive secret scan failed" >&2; exit 1 ;;
  1) ;;
  *) echo "$scan_output" >&2; echo "sensitive secret scan could not run" >&2; exit "$scan_status" ;;
esac

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck scripts/*.sh
fi
