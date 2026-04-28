#!/usr/bin/env bash
# Denoise an AMD CPU for benchmarking and run the wrapped command pinned to a
# stable core. Disables boost, sets the performance governor + EPP, and offlines
# the SMT sibling of the chosen core so the pinned thread owns its physical
# core. Restores all touched sysfs state on exit.
#
# Usage:    ./scripts/denoise-amd.sh <command> [args...]
# Override: BENCH_CPU=<n> ./scripts/denoise-amd.sh <command> [args...]
#
# The default core is the one with the highest amd_pstate_prefcore_ranking,
# excluding cpu0 and its SMT sibling (cpu0 handles most IRQs).
set -euo pipefail

if [[ $# -eq 0 ]]; then
    echo "usage: $0 <command> [args...]" >&2
    exit 2
fi

CPUDIR=/sys/devices/system/cpu

if [[ ! -e "$CPUDIR/cpufreq/boost" ]]; then
    echo "denoise-amd: $CPUDIR/cpufreq/boost not present; not an AMD/acpi-cpufreq system?" >&2
    exit 1
fi

# Prime sudo upfront so the dozens of sudo-tee calls below don't each prompt
# (and so we fail fast if credentials are unavailable).
if ! sudo -v; then
    echo "denoise-amd: sudo authentication failed" >&2
    exit 1
fi

expand_cpulist() {
    local list=$1 part lo hi i
    local -a parts
    IFS=',' read -ra parts <<<"$list"
    for part in "${parts[@]}"; do
        if [[ $part == *-* ]]; then
            lo=${part%-*}; hi=${part#*-}
            for ((i = lo; i <= hi; i++)); do echo "$i"; done
        else
            echo "$part"
        fi
    done
}

mapfile -t EXCLUDED_CPUS < <(expand_cpulist "$(<"$CPUDIR/cpu0/topology/thread_siblings_list")")

is_excluded() {
    local c=$1 e
    for e in "${EXCLUDED_CPUS[@]}"; do
        [[ "$c" == "$e" ]] && return 0
    done
    return 1
}

pick_cpu() {
    local best_cpu="" best_rank=-1 d cpu rank
    for d in "$CPUDIR"/cpu[0-9]*; do
        cpu=${d##*/cpu}
        is_excluded "$cpu" && continue
        [[ -r "$d/cpufreq/amd_pstate_prefcore_ranking" ]] || continue
        rank=$(<"$d/cpufreq/amd_pstate_prefcore_ranking")
        if ((rank > best_rank)); then
            best_rank=$rank
            best_cpu=$cpu
        fi
    done
    echo "$best_cpu"
}

BENCH_CPU=${BENCH_CPU:-$(pick_cpu)}
if [[ -z "$BENCH_CPU" ]]; then
    echo "denoise-amd: could not auto-pick a benchmark CPU" >&2
    exit 1
fi
if is_excluded "$BENCH_CPU"; then
    echo "denoise-amd: BENCH_CPU=$BENCH_CPU shares a thread group with cpu0; pick another" >&2
    exit 1
fi
if [[ ! -d "$CPUDIR/cpu$BENCH_CPU" ]]; then
    echo "denoise-amd: cpu$BENCH_CPU does not exist" >&2
    exit 1
fi

BENCH_SIBLING=
mapfile -t bench_siblings < <(expand_cpulist "$(<"$CPUDIR/cpu$BENCH_CPU/topology/thread_siblings_list")")
for s in "${bench_siblings[@]}"; do
    if [[ "$s" != "$BENCH_CPU" ]]; then
        BENCH_SIBLING=$s
        break
    fi
done

# Snapshot original state for restore.
ORIG_BOOST=$(<"$CPUDIR/cpufreq/boost")
declare -A ORIG_GOV ORIG_EPP
for d in "$CPUDIR"/cpu[0-9]*; do
    cpu=${d##*/cpu}
    [[ -e "$d/cpufreq/scaling_governor" ]] || continue
    ORIG_GOV[$cpu]=$(<"$d/cpufreq/scaling_governor")
    if [[ -e "$d/cpufreq/energy_performance_preference" ]]; then
        ORIG_EPP[$cpu]=$(<"$d/cpufreq/energy_performance_preference")
    fi
done
SIBLING_WAS_ONLINE=
if [[ -n "$BENCH_SIBLING" && -e "$CPUDIR/cpu$BENCH_SIBLING/online" ]]; then
    SIBLING_WAS_ONLINE=$(<"$CPUDIR/cpu$BENCH_SIBLING/online")
fi

restore() {
    set +e
    if [[ -n "$BENCH_SIBLING" && "$SIBLING_WAS_ONLINE" == "1" ]]; then
        echo 1 | sudo tee "$CPUDIR/cpu$BENCH_SIBLING/online" >/dev/null
    fi
    echo "$ORIG_BOOST" | sudo tee "$CPUDIR/cpufreq/boost" >/dev/null
    local cpu
    for cpu in "${!ORIG_GOV[@]}"; do
        echo "${ORIG_GOV[$cpu]}" | sudo tee "$CPUDIR/cpu$cpu/cpufreq/scaling_governor" >/dev/null
    done
    for cpu in "${!ORIG_EPP[@]}"; do
        echo "${ORIG_EPP[$cpu]}" | sudo tee "$CPUDIR/cpu$cpu/cpufreq/energy_performance_preference" >/dev/null
    done
}
trap restore EXIT

echo 0 | sudo tee "$CPUDIR/cpufreq/boost" >/dev/null
echo performance | sudo tee "$CPUDIR"/cpu*/cpufreq/scaling_governor >/dev/null
echo performance | sudo tee "$CPUDIR"/cpu*/cpufreq/energy_performance_preference >/dev/null
if [[ -n "$BENCH_SIBLING" && "$SIBLING_WAS_ONLINE" == "1" ]]; then
    echo 0 | sudo tee "$CPUDIR/cpu$BENCH_SIBLING/online" >/dev/null
fi

echo "denoise-amd: pinned to cpu$BENCH_CPU (sibling cpu${BENCH_SIBLING:-none} offline)" >&2

status=0
taskset -c "$BENCH_CPU" "$@" || status=$?
exit "$status"
