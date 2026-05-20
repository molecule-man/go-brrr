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
# Multiple FUNCALIGN values (set via FUNCALIGNS in testbins.sh) produce N
# before/after pairs. Each outer iteration walks all N pairs, alternating
# between a forward sweep (B0 A0 B1 A1 ... B(N-1) A(N-1)) and a reverse sweep
# (A(N-1) B(N-1) ... A0 B0). Two consecutive iterations form a palindrome
# (BAABBAAB-style), so each binary's expected position is equidistant from the
# centre over time — extending the BAABBAAB pattern from the 1-pair case.
#
# After all samples are collected, this script picks the *lucky* fa per side
# (lowest sec/op geomean for the current bench pattern) and appends that fa's
# samples to /tmp/{before,after}.txt. The pick runs from an EXIT trap, so a
# partial round still flushes its lucky data on interrupt. Every fa's samples
# are additionally appended to /tmp/{before,after}-fa<N>.txt so each
# alignment slot has its own per-run accumulator. bench-compare.sh truncates
# all of these at start, so `watch benchstat /tmp/before.txt /tmp/after.txt`
# tracks the run as it progresses.
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

# Load funcalign list. testbins.sh writes one value per line; empty line means
# "no -funcalign flag" (default pair).
MANIFEST="/tmp/bench-funcaligns"
if [[ -s "$MANIFEST" ]]; then
    mapfile -t FAS < "$MANIFEST"
else
    FAS=("")
fi

BEFORE_BINS=()
AFTER_BINS=()
BEFORE_TXTS=()
AFTER_TXTS=()
for fa in "${FAS[@]}"; do
    suffix="${fa:+-fa$fa}"
    BEFORE_BINS+=("/tmp/bench-before${suffix}.test")
    AFTER_BINS+=("/tmp/bench-after${suffix}.test")
    BEFORE_TXTS+=("/tmp/round-before${suffix}.txt")
    AFTER_TXTS+=("/tmp/round-after${suffix}.txt")
done
N=${#FAS[@]}

# Truncate per-fa transients so lucky-fa selection at finalize() only sees
# samples from this round.
for f in "${BEFORE_TXTS[@]}" "${AFTER_TXTS[@]}"; do
    : > "$f"
done

geomean_secop() {
    local file="$1"
    [[ -s "$file" ]] || return 0
    benchstat -format csv -filter '.unit:sec/op' "$file" 2>/dev/null | \
        awk -F, '/^geomean,/ { print $2; exit }'
}

# Run on EXIT (including signals): pick the lucky fa per side from this
# round's per-fa transients and append its samples to /tmp/{before,after}.txt.
# Fans every fa's samples to /tmp/{before,after}-fa<N>.txt as well, so each
# alignment slot has its own accumulator for the current run.
finalize() {
    set +e
    local side i fa suffix file val
    for side in before after; do
        local best_idx=-1 best_val=""
        for ((i = 0; i < N; i++)); do
            fa="${FAS[$i]}"
            suffix="${fa:+-fa$fa}"
            file="/tmp/round-${side}${suffix}.txt"
            val=$(geomean_secop "$file")
            [[ -z "$val" ]] && continue
            if (( best_idx == -1 )) || awk "BEGIN { exit !($val < $best_val) }"; then
                best_val="$val"
                best_idx=$i
            fi
        done
        if (( best_idx >= 0 )); then
            fa="${FAS[$best_idx]}"
            suffix="${fa:+-fa$fa}"
            cat "/tmp/round-${side}${suffix}.txt" >> "/tmp/${side}.txt"
        fi
        # Fan per-fa data out to /tmp/{side}-fa<N>.txt. Skip the default fa
        # because its accumulator path would collide with /tmp/{side}.txt.
        for ((i = 0; i < N; i++)); do
            fa="${FAS[$i]}"
            [[ -z "$fa" ]] && continue
            file="/tmp/round-${side}-fa${fa}.txt"
            [[ -s "$file" ]] && cat "$file" >> "/tmp/${side}-fa${fa}.txt"
        done
    done
}
trap 'finalize' EXIT

export BENCH_QUALITIES=all

# BENCH_HEAP_PAD is consumed by benchmarks/perturb_test.go to allocate and touch a
# heap pad of the given size, shifting physical page placement of every later
# allocation. Cycling pad sizes across processes samples multiple alignments
# so benchstat averages over alignment noise instead of being captured by it.
# First three cover distinct regimes (no pad / page shift / large shift) so
# partial runs with COUNT < len(PADS)*RPP still sample the informative ones.
# The rest fill in finer granularity for full sweeps.
PADS=(${PADS:-0 4096 65536 64 256 1024 16384})

run_one() {
    local bin="$1" out="$2" pad="$3"
    BENCH_HEAP_PAD="$pad" GOGC=off setarch -R "$bin" \
        -test.run '^$' -test.bench="$BENCH_PATTERN" -test.cpu=1 \
        -test.benchtime "$BENCHTIME" -test.count "$RUNS_PER_PROCESS" ./... >> "$out"
}

for i in $(seq 1 "$PROCESSES"); do
    pad="${PADS[$(( (i - 1) % ${#PADS[@]} ))]}"

    if (( i % 2 == 1 )); then
        # Forward sweep: B0 A0 B1 A1 ... B(N-1) A(N-1)
        for ((j = 0; j < N; j++)); do
            run_one "${BEFORE_BINS[$j]}" "${BEFORE_TXTS[$j]}" "$pad"
            run_one "${AFTER_BINS[$j]}"  "${AFTER_TXTS[$j]}"  "$pad"
        done
    else
        # Reverse sweep: A(N-1) B(N-1) ... A0 B0
        for ((j = N - 1; j >= 0; j--)); do
            run_one "${AFTER_BINS[$j]}"  "${AFTER_TXTS[$j]}"  "$pad"
            run_one "${BEFORE_BINS[$j]}" "${BEFORE_TXTS[$j]}" "$pad"
        done
    fi
done
