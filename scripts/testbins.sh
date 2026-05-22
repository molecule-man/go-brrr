#!/usr/bin/env bash
set -euo pipefail

BASE_BRANCH="${BASE_BRANCH:-main}"

# PGO_BEFORE / PGO_AFTER are optional paths to pprof CPU profiles passed to
# `go test -pgo` when building the before/after binaries. Empty means no PGO.
PGO_BEFORE="${PGO_BEFORE:-}"
PGO_AFTER="${PGO_AFTER:-}"

# FUNCALIGNS is a space-separated list of -funcalign=N values to build with
# (Go 1.25+). For each value, a separate before/after binary pair is produced.
# FUNCALIGN (singular) is accepted as a shorthand for FUNCALIGNS="$FUNCALIGN".
# With neither set, a single pair is built without -funcalign, matching the
# previous behaviour.
if [[ -n "${FUNCALIGNS:-}" ]]; then
    read -ra FAS <<< "$FUNCALIGNS"
elif [[ -n "${FUNCALIGN:-}" ]]; then
    FAS=("$FUNCALIGN")
else
    FAS=("")
fi

# bin_paths echoes "before_bin after_bin" for a given funcalign value (empty
# string for the no-flag default pair). Kept in sync with bench.sh.
bin_paths() {
    local fa="$1"
    local suffix="${fa:+-fa$fa}"
    echo "/tmp/bench-before${suffix}.test /tmp/bench-after${suffix}.test"
}

build_flags_for() {
    local fa="$1"
    if [[ -n "$fa" ]]; then
        printf '%s' "-ldflags=-funcalign=$fa"
    fi
}

WORKTREE_DIR=$(mktemp -d "/tmp/bench-worktree.XXXXXX")
trap_worktree() { git worktree remove --force "$WORKTREE_DIR" 2>/dev/null || rm -rf "$WORKTREE_DIR"; }

git worktree add --quiet --detach "$WORKTREE_DIR" "$BASE_BRANCH"
ln -s "$PWD/lib" "$WORKTREE_DIR/lib"
rm -rf "$WORKTREE_DIR/brotli-ref"
ln -s "$PWD/brotli-ref" "$WORKTREE_DIR/brotli-ref"

for fa in "${FAS[@]}"; do
    read -r BEFORE_BIN _ <<< "$(bin_paths "$fa")"
    label="${fa:+ (funcalign=$fa)}"
    echo "Building test binary for $BASE_BRANCH${label}..."
    args=(-c -o "$BEFORE_BIN")
    flag="$(build_flags_for "$fa")"
    [[ -n "$flag" ]] && args+=("$flag")
    [[ -n "$PGO_BEFORE" ]] && args+=(-pgo "$PGO_BEFORE")
    (cd "$WORKTREE_DIR/benchmarks" && go test "${args[@]}" .)
done

trap_worktree

for fa in "${FAS[@]}"; do
    read -r _ AFTER_BIN <<< "$(bin_paths "$fa")"
    label="${fa:+ (funcalign=$fa)}"
    echo "Building test binary for the current workdir${label}..."
    args=(-c -o "$AFTER_BIN")
    flag="$(build_flags_for "$fa")"
    [[ -n "$flag" ]] && args+=("$flag")
    [[ -n "$PGO_AFTER" ]] && args+=(-pgo "$PGO_AFTER")
    (cd benchmarks && go test "${args[@]}" .)
done

# Manifest consumed by bench.sh / bench-compare.sh. One funcalign value per
# line; an empty line means "no -funcalign flag" (default pair).
printf '%s\n' "${FAS[@]}" > /tmp/bench-funcaligns
