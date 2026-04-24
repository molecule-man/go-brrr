#!/usr/bin/env bash
set -euo pipefail

BASE_BRANCH="${BASE_BRANCH:-main}"

BEFORE_BIN="/tmp/bench-before.test"
AFTER_BIN="/tmp/bench-after.test"
WORKTREE_DIR=$(mktemp -d "/tmp/bench-worktree.XXXXXX")
trap_worktree() { git worktree remove --force "$WORKTREE_DIR" 2>/dev/null || rm -rf "$WORKTREE_DIR"; }

echo "Building test binary for $BASE_BRANCH..."
git worktree add --quiet --detach "$WORKTREE_DIR" "$BASE_BRANCH"
ln -s "$PWD/lib" "$WORKTREE_DIR/lib"
rm -rf "$WORKTREE_DIR/brotli-ref"
ln -s "$PWD/brotli-ref" "$WORKTREE_DIR/brotli-ref"
(cd "$WORKTREE_DIR" && go test -c -o "$BEFORE_BIN" .)
trap_worktree

echo "Building test binary for the current workdir ..."
go test -c -o "$AFTER_BIN" .
