#!/bin/sh
# Build a static library from the reference C brotli source in the submodule.
# Used by CGo test helpers to link against the exact submodule version rather
# than whatever system library pkg-config finds.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/lib/libbrotli_cref.a}"
mkdir -p "$(dirname "$OUT")"

if [ ! -f "$ROOT/brotli-ref/c/include/brotli/encode.h" ]; then
    echo "brotli submodule not initialized; run: git submodule update --init" >&2
    exit 1
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

for dir in common dec enc; do
    for src in "$ROOT"/brotli-ref/c/"$dir"/*.c; do
        base=$(basename "$src" .c)
        cc -c -O2 -I"$ROOT/brotli-ref/c/include" -o "$TMPDIR/${dir}_${base}.o" "$src"
    done
done

ar rcs "$OUT" "$TMPDIR"/*.o

echo "Built $OUT"
