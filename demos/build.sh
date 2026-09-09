#!/usr/bin/env bash

# SPDX-License-Identifier: MIT

# Builds the WebAssembly demo at grace.github.io/demos/warden.
#
#   ./demos/build.sh [path-to-grace.github.io]
#
# The page runs warden itself rather than a JavaScript reimplementation of it,
# which is the only reason the demo is worth having: it cannot disagree with the
# tool, because it is the tool. wasm.go calls the same code the CLI calls.
#
# This script exists because the .wasm and wasm_exec.js under demos/warden in the
# blog repository were placed there by hand, with nothing recording how to
# reproduce them and nothing stating what they may be used under. Following
# https://go.dev/wiki/WebAssembly, the build is two commands; the licenses are
# the part that was missing.
set -euo pipefail

cd "$(dirname "$0")/.."   # repository root

BLOG=${1:-../grace.github.io}
OUT="$BLOG/demos/warden"

if [ ! -d "$OUT" ]; then
  echo "no demo directory at $OUT" >&2
  echo "pass the path to grace.github.io as the first argument" >&2
  exit 1
fi

echo "building $OUT/warden.wasm"
GOOS=js GOARCH=wasm go build -o "$OUT/warden.wasm" .

# wasm_exec.js is the Go runtime's own JavaScript shim and must come from the
# toolchain that built the binary -- a mismatched pair fails at load with an
# error that does not say so.
#
# It lives in lib/wasm as of Go 1.24. Most references still give misc/wasm,
# which is where it was before, so both are tried and the miss is reported
# rather than silently leaving a stale copy in place.
GOROOT=$(go env GOROOT)
for candidate in "$GOROOT/lib/wasm/wasm_exec.js" "$GOROOT/misc/wasm/wasm_exec.js"; do
  if [ -f "$candidate" ]; then
    echo "copying $(basename "$candidate") from $(go version | cut -d' ' -f3)"
    cp "$candidate" "$OUT/wasm_exec.js"
    exec_copied=1
    break
  fi
done
if [ -z "${exec_copied:-}" ]; then
  echo "could not find wasm_exec.js under $GOROOT" >&2
  exit 1
fi

# A binary carries no terms on its face, and MIT asks that the copyright notice
# and the permission notice be included in all copies. The blog repository is
# where this one is distributed from, so the license travels with it rather than
# being placed by hand once and left to drift.
echo "copying LICENSE"
cp LICENSE "$OUT/LICENSE"

# wasm_exec.js says its license "can be found in the LICENSE file", meaning Go's,
# and next to a LICENSE that is MIT that sentence points at the wrong file. So
# Go's BSD-3-Clause goes beside it under a name that says whose it is. The
# runtime linked into the .wasm is under the same terms, so this would be owed
# even if the shim were not here.
#
# It sits at $GOROOT/LICENSE on a stock install and one level up on Homebrew,
# whose GOROOT points into libexec. Both are tried, and a miss is fatal rather
# than a warning: shipping the shim without its terms is the thing being fixed.
for candidate in "$GOROOT/LICENSE" "$GOROOT/../LICENSE"; do
  if [ -f "$candidate" ]; then
    echo "copying Go's LICENSE from $(go version | cut -d' ' -f3)"
    cp "$candidate" "$OUT/LICENSE.go"
    go_license_copied=1
    break
  fi
done
if [ -z "${go_license_copied:-}" ]; then
  echo "could not find Go's LICENSE under $GOROOT or its parent" >&2
  echo "wasm_exec.js and the runtime in the .wasm are BSD-3-Clause and cannot be" >&2
  echo "redistributed without it, so this is fatal rather than a warning." >&2
  exit 1
fi

echo
echo "built:"
ls -lh "$OUT/warden.wasm" "$OUT/wasm_exec.js" \
       "$OUT/LICENSE" "$OUT/LICENSE.go" | awk '{print "  " $9 "  " $5}'
echo
echo "the .wasm is committed to the blog repository, so commit it there."
