#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

base=$(git log --diff-filter=A --format=%H -- tsc/internal/checker/ssc.go | tail -n 1)^
git diff "$base" -- . ':(exclude)syscript.patch' > syscript.patch

node_include_dir=$(node -p "require('path').join(process.execPath, '..', '..', 'include', 'node')")

cd tsc
go build -o ../built/local/tsc ./cmd/tsc
CGO_CFLAGS="-I$node_include_dir" go build -buildmode=c-shared -o ../built/local/tsc.node ./cmd/sscapi
rm -f ../built/local/tsc.h
