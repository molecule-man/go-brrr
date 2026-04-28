#!/usr/bin/env bash
# Run cross-library benchmarks and produce CSV for plotting.
# Compression results measure reused streaming encoders with timed output discarded.
#
# Usage: scripts/bench-crosslib.sh [bench-regex] [output.csv]
#
#   bench-regex   Go benchmark regex (default: BenchmarkCrossLib$)
#   output.csv    output file (default: bench-crosslib.csv)
#
# Environment:
#   BENCHTIME         benchmark duration per case (default: 3s)
#   BENCH_CORPUS_FILE path to input file (default: alice29.txt from brotli testdata)
set -euo pipefail

cd "$(dirname "$0")/.."

COUNT=${COUNT:-6}
BENCHTIME="${BENCHTIME:-3s}"
BENCH="${1:-BenchmarkCrossLib$}"
OUT="${2:-bench-crosslib.csv}"
TMPRAW=$(mktemp)
trap 'rm -f "$TMPRAW"' EXIT

echo "Running benchmarks (bench=$BENCH, benchtime=$BENCHTIME, count=$COUNT)..." >&2

(cd benchmarks && go test -tags bench -run='^$' -bench="$BENCH" \
    -benchtime="$BENCHTIME" -count=$COUNT -timeout=0 -cpu 1 .) 2>&1 \
| tee /dev/stderr > "$TMPRAW"

echo "Running benchstat..." >&2

benchstat "$TMPRAW" | awk '
BEGIN {
    section = ""
    n = 0
}

# Detect metric sections by their header row.
/│/ && /sec\/op/    { section = "secop"; next }
/│/ && /B\/s/       { section = "bps"; next }
/│/ && /B\/op/      { section = "bop"; next }
/│/ && /allocs\/op/ { section = "allocs"; next }
/│/ && /ratio/      { section = "ratio"; next }

# Skip geomean rows and blank lines.
/^geomean/ || /^[[:space:]]*$/ { next }

# Parse benchmark data rows: look for lines containing lib= and level=.
/lib=/ && /level=/ {
    split($1, parts, "/")
    lib = ""; level = ""
    for (i in parts) {
        if (parts[i] ~ /^lib=/) lib = substr(parts[i], 5)
        if (parts[i] ~ /^level=/) {
            s = substr(parts[i], 7)
            sub(/-[0-9]+$/, "", s)
            level = s
        }
    }
    if (lib == "" || level == "") next
    key = lib SUBSEP level

    # Extract the numeric value (first number after │).
    val = ""
    for (i = 2; i <= NF; i++) {
        if ($i == "│") continue
        if ($i == "±") break
        if ($i ~ /^[0-9]/) { val = $i; break }
    }
    if (val == "") next

    if (section == "bps") {
        # Convert binary suffix to decimal MB/s.
        mbps = val
        if (val ~ /Ki$/)  { sub(/Ki$/, "", mbps); mbps = mbps * 1024 / 1000000 }
        else if (val ~ /Mi$/) { sub(/Mi$/, "", mbps); mbps = mbps * 1048576 / 1000000 }
        else if (val ~ /Gi$/) { sub(/Gi$/, "", mbps); mbps = mbps * 1073741824 / 1000000 }
        speed[key] = sprintf("%.2f", mbps)
        if (!(key in order)) { order[key] = n; keys[n] = key; n++ }
    }

    if (section == "ratio") {
        ratios[key] = val + 0
        if (!(key in order)) { order[key] = n; keys[n] = key; n++ }
    }
}

END {
    has_ratios = length(ratios) > 0
    if (has_ratios)
        print "library,level,speed_mbps,ratio"
    else
        print "library,level,speed_mbps"

    for (i = 0; i < n; i++) {
        key = keys[i]
        if (!(key in speed)) continue
        split(key, kp, SUBSEP)
        if (has_ratios)
            print kp[1] "," kp[2] "," speed[key] "," ratios[key]
        else
            print kp[1] "," kp[2] "," speed[key]
    }
}
' > "$OUT"

echo "Results written to $OUT" >&2
