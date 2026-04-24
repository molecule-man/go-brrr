#!/usr/bin/env bash
# Run before/after benchmark binaries with interleaved process invocations.
#
# Two mitigations for benchmark noise:
#
# 1. GOGC=off disables Go's background GC work (scavenging, sweeping), which
#    causes ~6% bimodal splits even with 0 allocs/op at short benchtimes.
#
# 2. Each process runs -test.count=4 internally. Within a process, physical
#    page layout is fixed, so GOGC=off makes all 4 iterations stable. Between
#    processes, ~30% get unlucky L3 cache coloring from random physical page
#    allocation and run ~6% slower. With enough total data points, benchstat's
#    robust statistics handle these outlier clusters cleanly (±0% CI).
#
# setarch -R disables ASLR so virtual address layout is deterministic.
set -euo pipefail

BENCH_PATTERN="${1:?usage: bench.sh <bench-pattern>}"
COUNT="${COUNT:-24}"
BENCHTIME="${BENCHTIME:-2s}"

RUNS_PER_PROCESS=${RPP:-4}
PROCESSES=$(( COUNT / RUNS_PER_PROCESS ))
if (( PROCESSES < 1 )); then
    PROCESSES=1
fi

# it's assumed that the test binaries are pre-built by scripts/testbins.sh and available at these paths
BEFORE_BIN="/tmp/bench-before.test"
AFTER_BIN="/tmp/bench-after.test"
BEFORE_TXT="/tmp/before.txt"
AFTER_TXT="/tmp/after.txt"

export BENCH_QUALITIES=all

# BENCH_HEAP_PAD is consumed by bench_perturb_test.go to allocate and touch a
# heap pad of the given size, shifting physical page placement of every later
# allocation. Cycling pad sizes across processes samples multiple alignments
# so benchstat averages over alignment noise instead of being captured by it.
# First three cover distinct regimes (no pad / page shift / large shift) so
# partial runs with COUNT < len(PADS)*RPP still sample the informative ones.
# The rest fill in finer granularity for full sweeps.
PADS=(${PADS:-0 4096 65536 64 256 1024 16384})

for i in $(seq 1 "$PROCESSES"); do
    pad="${PADS[$(( (i - 1) % ${#PADS[@]} ))]}"

    if (( i % 2 == 1 )); then
        BENCH_HEAP_PAD="$pad" GOGC=off setarch -R "$BEFORE_BIN" -test.run '^$' -test.bench="$BENCH_PATTERN" -test.cpu=1 -test.benchtime "$BENCHTIME" -test.count "$RUNS_PER_PROCESS" >> "$BEFORE_TXT"
        BENCH_HEAP_PAD="$pad" GOGC=off setarch -R "$AFTER_BIN"  -test.run '^$' -test.bench="$BENCH_PATTERN" -test.cpu=1 -test.benchtime "$BENCHTIME" -test.count "$RUNS_PER_PROCESS" >> "$AFTER_TXT"
    else
        BENCH_HEAP_PAD="$pad" GOGC=off setarch -R "$AFTER_BIN"  -test.run '^$' -test.bench="$BENCH_PATTERN" -test.cpu=1 -test.benchtime "$BENCHTIME" -test.count "$RUNS_PER_PROCESS" >> "$AFTER_TXT"
        BENCH_HEAP_PAD="$pad" GOGC=off setarch -R "$BEFORE_BIN" -test.run '^$' -test.bench="$BENCH_PATTERN" -test.cpu=1 -test.benchtime "$BENCHTIME" -test.count "$RUNS_PER_PROCESS" >> "$BEFORE_TXT"
    fi
done
