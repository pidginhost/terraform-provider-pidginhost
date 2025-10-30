#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

mkdir -p "$ROOT/.gocache" "$ROOT/.gomodcache" "$ROOT/.gotmp" "$ROOT/bin"

export GOCACHE="$ROOT/.gocache"
export GOMODCACHE="$ROOT/.gomodcache"
export GOTMPDIR="$ROOT/.gotmp"

cd "$ROOT"

go mod tidy
go build -o bin/terraform-provider-pidginhost .
