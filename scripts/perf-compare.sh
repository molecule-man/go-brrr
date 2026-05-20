#!/usr/bin/env bash
# Diff hardware perf counters between /tmp/bench-before.test and /tmp/bench-after.test.
#
# Usage: perf-compare.sh '<bench-pattern>'
#
# Env:
#   BENCHTIME   passed to -test.benchtime; MUST be Nx form so before/after do
#               the same work (default 100000x).
#   COUNT       perf stat -r repetition count (default 10).

set -euo pipefail

BENCH_PATTERN="${1:?usage: perf-compare.sh <bench-pattern>}"
BENCHTIME="${BENCHTIME:-1000x}"
COUNT="${COUNT:-10}"

BEFORE_BIN="/tmp/bench-before.test"
AFTER_BIN="/tmp/bench-after.test"

EVENTS='cpu_core/cycles/u,cpu_core/instructions/u,cpu_core/branches/u,cpu_core/br_misp_retired.cond/u,cpu_core/frontend_retired.l1i_miss/u,cpu_core/mem_load_retired.l1_miss/u,cpu_core/idq.dsb_uops/u,cpu_core/idq.mite_uops/u'

./scripts/testbins.sh

BEFORE_CSV=$(mktemp /tmp/perf-compare.XXXXXX.before.csv)
AFTER_CSV=$(mktemp /tmp/perf-compare.XXXXXX.after.csv)
trap 'rm -f "$BEFORE_CSV" "$AFTER_CSV"' EXIT

run_side() {
    local bin="$1" out="$2"
    GOGC=off setarch -R perf stat -x',' -r "$COUNT" -e "$EVENTS" -o "$out" -- \
        "$bin" -test.run '^$' -test.bench "$BENCH_PATTERN" \
               -test.cpu=1 -test.benchtime "$BENCHTIME" -test.count=1 \
        > /dev/null
}

echo "before: $COUNT reps x -benchtime=$BENCHTIME" >&2
run_side "$BEFORE_BIN" "$BEFORE_CSV"
echo "after:  $COUNT reps x -benchtime=$BENCHTIME" >&2
run_side "$AFTER_BIN"  "$AFTER_CSV"

awk -F, -v before="$BEFORE_CSV" -v after="$AFTER_CSV" '
function short(e,   s) {
    s = e
    sub(/^cpu_core\//, "", s); sub(/\/u$/, "", s); sub(/:u$/, "", s)
    return s
}
function fmt_n(x) {
    if (x >= 1e9) return sprintf("%.3fG", x/1e9)
    if (x >= 1e6) return sprintf("%.3fM", x/1e6)
    if (x >= 1e3) return sprintf("%.3fK", x/1e3)
    return sprintf("%.0f", x)
}
function row(name, b, a,   d) {
    d = (b > 0) ? (a - b) / b * 100 : 0
    printf "%-32s  %12s  %12s  %+10.2f%%\n", name, fmt_n(b), fmt_n(a), d
}
function load(path, dest,   line, f, n) {
    while ((getline line < path) > 0) {
        if (line == "" || line ~ /^#/) continue
        n = split(line, f, ",")
        if (n < 3) continue
        dest[short(f[3])] = f[1] + 0
    }
    close(path)
}
BEGIN {
    load(before, B); load(after, A)
    printf "%-32s  %12s  %12s  %11s\n", "event", "before", "after", "delta"
    print  "----------------------------------------------------------------------------"
    n = split("cycles instructions branches br_misp_retired.cond " \
              "frontend_retired.l1i_miss mem_load_retired.l1_miss " \
              "idq.dsb_uops idq.mite_uops", evs, " ")
    for (i = 1; i <= n; i++) row(evs[i], B[evs[i]], A[evs[i]])

    if (B["cycles"] > 0 && A["cycles"] > 0) {
        ipc_b = B["instructions"] / B["cycles"]
        ipc_a = A["instructions"] / A["cycles"]
        printf "%-32s  %12.3f  %12.3f  %+10.2f%%\n", "IPC", ipc_b, ipc_a,
               (ipc_a - ipc_b) / ipc_b * 100
    }
    dsb_b = B["idq.dsb_uops"]; mite_b = B["idq.mite_uops"]
    dsb_a = A["idq.dsb_uops"]; mite_a = A["idq.mite_uops"]
    if (dsb_b + mite_b > 0 && dsb_a + mite_a > 0) {
        p_b = dsb_b / (dsb_b + mite_b) * 100
        p_a = dsb_a / (dsb_a + mite_a) * 100
        printf "%-32s  %11.2f%%  %11.2f%%  %+9.2fpp\n", "DSB share of uops",
               p_b, p_a, p_a - p_b
    }
}
'
