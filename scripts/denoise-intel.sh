#!/usr/bin/env bash
# Denoise an Intel CPU for benchmarking and run the wrapped command pinned to a
# stable core. Disables turbo, sets the performance governor + EPP, and offlines
# the SMT sibling of the chosen core so the pinned thread owns its physical
# core. Restores all touched sysfs state on exit.
#
# Usage:    ./scripts/denoise-intel.sh <command> [args...]
# Override: BENCH_CPU=<n> ./scripts/denoise-intel.sh <command> [args...]
#
# The default core is the one with the highest acpi_cppc/highest_perf (Intel
# ITMT favored-core hint, which also picks a P-core on hybrid CPUs), excluding
# cpu0 and its SMT sibling (cpu0 handles most IRQs). Falls back to the lowest
# non-excluded cpu if no ranking is exposed.
set -euo pipefail

if [[ $# -eq 0 ]]; then
    echo "usage: $0 <command> [args...]" >&2
    exit 2
fi

CPUDIR=/sys/devices/system/cpu

# Modern intel_pstate uses no_turbo (1 = disabled). Older Intel systems on the
# acpi-cpufreq driver expose cpufreq/boost (0 = disabled), same as AMD.
TURBO_FILE=
TURBO_DISABLE_VAL=
if [[ -e "$CPUDIR/intel_pstate/no_turbo" ]]; then
    TURBO_FILE="$CPUDIR/intel_pstate/no_turbo"
    TURBO_DISABLE_VAL=1
elif [[ -e "$CPUDIR/cpufreq/boost" ]]; then
    TURBO_FILE="$CPUDIR/cpufreq/boost"
    TURBO_DISABLE_VAL=0
else
    echo "denoise-intel: no turbo control file found ($CPUDIR/intel_pstate/no_turbo or $CPUDIR/cpufreq/boost); not an Intel system?" >&2
    exit 1
fi

# Prime sudo upfront so the dozens of sudo-tee calls below don't each prompt
# (and so we fail fast if credentials are unavailable).
if ! sudo -v; then
    echo "denoise-intel: sudo authentication failed" >&2
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
    local best_cpu="" best_rank=-1 first_cpu="" d cpu rank
    for d in "$CPUDIR"/cpu[0-9]*; do
        cpu=${d##*/cpu}
        is_excluded "$cpu" && continue
        [[ -z "$first_cpu" ]] && first_cpu=$cpu
        [[ -r "$d/acpi_cppc/highest_perf" ]] || continue
        rank=$(<"$d/acpi_cppc/highest_perf")
        if ((rank > best_rank)); then
            best_rank=$rank
            best_cpu=$cpu
        fi
    done
    echo "${best_cpu:-$first_cpu}"
}

BENCH_CPU=${BENCH_CPU:-$(pick_cpu)}
if [[ -z "$BENCH_CPU" ]]; then
    echo "denoise-intel: could not auto-pick a benchmark CPU" >&2
    exit 1
fi
if is_excluded "$BENCH_CPU"; then
    echo "denoise-intel: BENCH_CPU=$BENCH_CPU shares a thread group with cpu0; pick another" >&2
    exit 1
fi
if [[ ! -d "$CPUDIR/cpu$BENCH_CPU" ]]; then
    echo "denoise-intel: cpu$BENCH_CPU does not exist" >&2
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
ORIG_TURBO=$(<"$TURBO_FILE")
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
    echo "$ORIG_TURBO" | sudo tee "$TURBO_FILE" >/dev/null
    local cpu
    for cpu in "${!ORIG_GOV[@]}"; do
        echo "${ORIG_GOV[$cpu]}" | sudo tee "$CPUDIR/cpu$cpu/cpufreq/scaling_governor" >/dev/null
    done
    for cpu in "${!ORIG_EPP[@]}"; do
        echo "${ORIG_EPP[$cpu]}" | sudo tee "$CPUDIR/cpu$cpu/cpufreq/energy_performance_preference" >/dev/null
    done
}
trap restore EXIT

echo "$TURBO_DISABLE_VAL" | sudo tee "$TURBO_FILE" >/dev/null
echo performance | sudo tee "$CPUDIR"/cpu*/cpufreq/scaling_governor >/dev/null
echo performance | sudo tee "$CPUDIR"/cpu*/cpufreq/energy_performance_preference >/dev/null
if [[ -n "$BENCH_SIBLING" && "$SIBLING_WAS_ONLINE" == "1" ]]; then
    echo 0 | sudo tee "$CPUDIR/cpu$BENCH_SIBLING/online" >/dev/null
fi

echo "denoise-intel: pinned to cpu$BENCH_CPU (sibling cpu${BENCH_SIBLING:-none} offline)" >&2

status=0
taskset -c "$BENCH_CPU" "$@" || status=$?
exit "$status"
