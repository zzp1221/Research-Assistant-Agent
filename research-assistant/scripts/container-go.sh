#!/usr/bin/env bash
# Run backend Go commands inside the pinned builder container.
# Usage: scripts/container-go.sh "go test ./..."
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_IMAGE="${GO_IMAGE:-golang:1.26.1-bookworm}"
CMD="${1:?command required}"
shift || true

exec env -u PWD MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*' docker run --rm \
  -v "${ROOT:-}:/src" \
  -v dsh-go-mod-cache:/go/pkg/mod \
  -v dsh-go-build-cache:/root/.cache/go-build \
  -e GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" \
  -w /src/backend \
  "${GO_IMAGE}" sh -c "export PATH=\$PATH:/usr/local/go/bin; ${CMD}"
