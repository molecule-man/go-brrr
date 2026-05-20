#!/usr/bin/env bash
set -euo pipefail

BASE_BRANCH="${BASE_BRANCH:-main}"

BEFORE_BIN="/tmp/bench-before.test"
AFTER_BIN="/tmp/bench-after.test"
WORKTREE_DIR=$(mktemp -d "/tmp/bench-worktree.XXXXXX")
trap_worktree() { git worktree remove --force "$WORKTREE_DIR" 2>/dev/null || rm -rf "$WORKTREE_DIR"; }

# FUNCALIGN, if set, is passed to the linker via -ldflags=-funcalign=N
# (available in Go 1.25+). Affects both before/after builds so layout-roulette
# is the same on both sides.
BUILD_FLAGS=()
if [[ -n "${FUNCALIGN:-}" ]]; then
    BUILD_FLAGS+=(-ldflags="-funcalign=$FUNCALIGN")
fi

echo "Building test binary for $BASE_BRANCH${FUNCALIGN:+ (funcalign=$FUNCALIGN)}..."
git worktree add --quiet --detach "$WORKTREE_DIR" "$BASE_BRANCH"
ln -s "$PWD/lib" "$WORKTREE_DIR/lib"
rm -rf "$WORKTREE_DIR/brotli-ref"
ln -s "$PWD/brotli-ref" "$WORKTREE_DIR/brotli-ref"
(cd "$WORKTREE_DIR/benchmarks" && go test -c "${BUILD_FLAGS[@]}" -o "$BEFORE_BIN" .)
trap_worktree

echo "Building test binary for the current workdir${FUNCALIGN:+ (funcalign=$FUNCALIGN)} ..."
(cd benchmarks && go test -c "${BUILD_FLAGS[@]}" -o "$AFTER_BIN" .)
