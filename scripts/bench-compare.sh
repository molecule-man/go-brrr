#!/usr/bin/env bash
set -euo pipefail

PROFILES=("$@")
if [[ ${#PROFILES[@]} -eq 0 ]]; then
    PROFILES=("enc" "dec")
fi

# Capture env overrides before profiles clobber them.
_ENV_BENCHTIME="${BENCHTIME:-}"
_ENV_COUNT="${COUNT:-}"
_ENV_QUALITIES="${QUALITIES:-}"
_ENV_PAYLOADS="${PAYLOADS:-}"
_ENV_HASHERS="${HASHERS:-}"
_ENV_BENCHES="${BENCHES:-}"

load_profile() {
    local profile="$1"
    case "$profile" in
        enc)
            BENCHTIME="2s"; COUNT=24
            BENCHES=("Compress")
            QUALITIES=(0 1 2 3 4 5 6 7 8 9 10 11)
            PAYLOADS=("Json_2k" "VariedPayloads" "Large")
            ;;
        ench)
            BENCHTIME="2s"; COUNT=24
            BENCHES=("CompressHasher")
            HASHERS=("onepass" "twopass" "h2" "h3" "h4" "h54" "h40q5" "h40q6" "h5" "h6" "h5b5" "h6b5" "h41q7" "h41q8" "h5b6" "h6b6" "h5b7" "h6b7" "h42" "h5b8" "h6b8" "h10")
            PAYLOADS=("Json_2k" "VariedPayloads" "Large")
            ;;
        encc)
            BENCHTIME="2s"; COUNT=12
            BENCHES=("CompressCorpusFile")
            QUALITIES=(4 6)
            PAYLOADS=()
            ;;
        dec)
            BENCHTIME="1s"; COUNT=12
            BENCHES=("Decompress")
            QUALITIES=(0 1 2 3 4 5 6 7 8 9 10 11)
            PAYLOADS=("Json_2k" "VariedPayloads" "Large")
            ;;
        decc)
            BENCHTIME="1s"; COUNT=12
            BENCHES=("DecompressCorpusFile")
            QUALITIES=()
            PAYLOADS=()
            ;;
        *)
            echo "Unknown profile: $profile"
            echo "Known profiles: enc ench encc dec decc"
            exit 1
            ;;
    esac
}

apply_env_overrides() {
    [[ -n "$_ENV_BENCHTIME" ]] && BENCHTIME="$_ENV_BENCHTIME"
    [[ -n "$_ENV_COUNT" ]] && COUNT="$_ENV_COUNT"
    if [[ -n "$_ENV_BENCHES" ]]; then
        read -ra BENCHES <<< "$_ENV_BENCHES"
    fi
    if [[ -n "$_ENV_QUALITIES" ]]; then
        if [[ "$_ENV_QUALITIES" == "all" ]]; then
            QUALITIES=(0 1 2 3 4 5 6 7 8 9 10 11)
        else
            read -ra QUALITIES <<< "$_ENV_QUALITIES"
        fi
    fi
    if [[ -n "$_ENV_PAYLOADS" ]]; then
        read -ra PAYLOADS <<< "$_ENV_PAYLOADS"
    fi
    if [[ -n "$_ENV_HASHERS" ]]; then
        read -ra HASHERS <<< "$_ENV_HASHERS"
    fi
}

# Collect CPU profiles from BASE_BRANCH and the current workdir, then export
# PGO_BEFORE / PGO_AFTER so testbins.sh builds the test binaries with -pgo.
# The bench filter is `Compress$/q=<qualities>$` where <qualities> is the
# union of QUALITIES across all requested profiles after env overrides.
collect_pgo_profiles() {
    local -A qset=()
    local profile q
    for profile in "${PROFILES[@]}"; do
        load_profile "$profile"
        apply_env_overrides
        for q in "${QUALITIES[@]}"; do
            qset["$q"]=1
        done
    done

    if (( ${#qset[@]} == 0 )); then
        echo "BENCH_PGO=1 but no qualities resolved across profiles; skipping PGO collection." >&2
        return
    fi

    local -a qs
    mapfile -t qs < <(printf '%s\n' "${!qset[@]}" | sort -n)

    local q_pattern
    if (( ${#qs[@]} == 1 )); then
        q_pattern="${qs[0]}"
    else
        q_pattern="($(IFS='|'; printf '%s' "${qs[*]}"))"
    fi
    local p_pattern="${PGO_PAYLOAD_PATTERN:-.}"
    local bench_pattern="Compress\$/q=${q_pattern}\$/${p_pattern}"

    local base_branch="${BASE_BRANCH:-main}"
    local pgo_wt
    pgo_wt=$(mktemp -d /tmp/bench-pgo-wt.XXXXXX)
    git worktree add --quiet --detach "$pgo_wt" "$base_branch"
    ln -s "$PWD/lib" "$pgo_wt/lib"
    rm -rf "$pgo_wt/brotli-ref"
    ln -s "$PWD/brotli-ref" "$pgo_wt/brotli-ref"

    echo "=== Collecting PGO profile from $base_branch -> /tmp/cpu.before (-bench='$bench_pattern') ==="
    (cd "$pgo_wt/benchmarks" && go test -run '^$' -bench "$bench_pattern" -benchtime 1s -cpu 1 -cpuprofile /tmp/cpu.before .)
    git worktree remove --force "$pgo_wt" 2>/dev/null || rm -rf "$pgo_wt"

    echo "=== Collecting PGO profile from workdir -> /tmp/cpu.after (-bench='$bench_pattern') ==="
    (cd benchmarks && go test -run '^$' -bench "$bench_pattern" -benchtime 1s -cpu 1 -cpuprofile /tmp/cpu.after .)

    export PGO_BEFORE=/tmp/cpu.before
    export PGO_AFTER=/tmp/cpu.after
}

# Prints the filtered B/s benchstat table and geomean lines for whatever data
# is in BEFORE_TXT and AFTER_TXT.  Errors are suppressed so this is safe to
# call from an EXIT trap with partial data.
# Usage: print_benchstat_summary "Header title"
print_benchstat_summary() {
    local title="$1"
    echo ""
    echo "=== $title ==="
    benchstat -filter '.unit:B/s' main="$BEFORE_TXT" branch="$AFTER_TXT" 2>/dev/null | awk '
/~ \(p=/ { next }
{
    if (match($0, /[+-][0-9]+\.[0-9]+%/) && tolower($1) != "geomean") {
        pct = substr($0, RSTART, RLENGTH-1) + 0
        if (pct < 0) pct = -pct
        if (pct <= 0.3 && match($0, /\(p=[0-9]+\.[0-9]+/)) {
            pval = substr($0, RSTART+3, RLENGTH-3) + 0
            if (pval < 0.05) next
        }
    }
    print
}
' || true

    echo ""
    for unit in sec/op B/op allocs/op; do
        benchstat -filter ".unit:$unit" main="$BEFORE_TXT" branch="$AFTER_TXT" 2>/dev/null | grep -i 'geomean' | awk -v u="$unit" '{printf "geomean %-10s %s %s %s\n", u, $2, $3, $4}' || true
    done
}

# bench.sh appends each round's lucky-fa pair to these as the run progresses,
# and the per-fa fan-out to /tmp/{before,after}-fa<N>.txt. Truncating all of
# them here means `watch benchstat /tmp/before.txt /tmp/after.txt` tracks
# this comparison's progress, with no leftover from prior runs.
BEFORE_TXT="/tmp/before.txt"
AFTER_TXT="/tmp/after.txt"
stat_out=$(mktemp /tmp/bench-compare-stat.XXXXXX)

_interrupted=false

# To stop the script mid-benchmark and still see partial B/s results and
# geomeans, send SIGINT (Ctrl+C) or SIGTERM ("kill <PID>" from another
# terminal). Do NOT use SIGKILL ("kill -9") — it cannot be trapped and will
# produce no output.
#
# Ctrl+C sends SIGINT to the whole process group, so the currently-running
# bench.sh child is also interrupted. Its EXIT trap still flushes the lucky
# pair from whatever partial-round samples it had collected, so those land in
# /tmp/{before,after}.txt before this script's exit handler runs.
_on_interrupt() {
    _interrupted=true
    exit 130
}

_on_exit() {
    if [[ "$_interrupted" == true ]] && { [[ -s "$BEFORE_TXT" ]] || [[ -s "$AFTER_TXT" ]]; }; then
        benchstat main="$BEFORE_TXT" branch="$AFTER_TXT" > "$stat_out" 2>/dev/null || true
        print_benchstat_summary "Partial benchstat results (B/s)"
    fi
    rm -f "$stat_out"
}

trap '_on_exit' EXIT
trap '_on_interrupt' INT TERM

if [[ "${BENCH_PGO:-}" == "1" ]]; then
    collect_pgo_profiles
fi

./scripts/testbins.sh

# Load funcalign manifest written by testbins.sh, used here only to know which
# per-fa accumulators to truncate.
MANIFEST="/tmp/bench-funcaligns"
if [[ -s "$MANIFEST" ]]; then
    mapfile -t FAS < "$MANIFEST"
else
    FAS=("")
fi

: > "$BEFORE_TXT"
: > "$AFTER_TXT"
for fa in "${FAS[@]}"; do
    [[ -z "$fa" ]] && continue
    : > "/tmp/before-fa${fa}.txt"
    : > "/tmp/after-fa${fa}.txt"
done

for profile in "${PROFILES[@]}"; do
    load_profile "$profile"
    apply_env_overrides

    export BENCH_QUALITIES=all
    export COUNT BENCHTIME

    for b in "${BENCHES[@]}"; do
        case "$b" in
            CompressCorpusFile)
                for rawfile in "$BENCH_CORPUS_DIR"/*; do
                    [[ -f "$rawfile" ]] || continue
                    [[ "$rawfile" == *.br ]] && continue
                    for q in "${QUALITIES[@]}"; do
                        ./scripts/bench.sh "Compress\$/q=$q\$/corpus_$(basename "$rawfile")\$" > /dev/null
                    done
                done
                ;;
            DecompressCorpusFile)
                for brfile in "$BENCH_CORPUS_DIR"/*.br; do
                    [[ -f "$brfile" ]] || continue
                    BENCH_CORPUS_FILE="$brfile" ./scripts/bench.sh "DecompressCorpusFile\$" > /dev/null
                done
                ;;
            CompressHasher)
                for p in "${PAYLOADS[@]}"; do
                    for h in "${HASHERS[@]}"; do
                        ./scripts/bench.sh "$b\$/h=$h\$/$p\$" > /dev/null
                    done
                done
                ;;
            *)
                for p in "${PAYLOADS[@]}"; do
                    for q in "${QUALITIES[@]}"; do
                        ./scripts/bench.sh "$b\$/q=$q\$/$p\$" > /dev/null
                    done
                done
                ;;
        esac
    done
done

# Save full output for regression checks.
benchstat main="$BEFORE_TXT" branch="$AFTER_TXT" > "$stat_out"

print_benchstat_summary "benchstat results (B/s)"

rc=0

# Check 1: Parse benchstat allocs/op section for regressions (any increase is a failure)
echo ""
echo "=== Checking allocations ==="
alloc_output=$(awk '
/allocs\/op.*vs base/ { in_allocs = 1; next }
in_allocs && /^\s*$/ { in_allocs = 0; next }
in_allocs && (/sec\/op/ || /B\/op/) { in_allocs = 0; next }
in_allocs {
    if (match($0, /\+[0-9]+\.[0-9]+%/)) {
        pct_str = substr($0, RSTART + 1, RLENGTH - 2)
        pct = pct_str + 0
        if (pct > 0) {
            name = $1
            printf "FAIL: %s allocs/op regression of +%s%%\n", name, pct_str
            fail = 1
        }
    }
}
END {
    if (!fail) printf "OK: No allocs/op regressions.\n"
    exit fail ? 1 : 0
}
' "$stat_out") || rc=1

echo "$alloc_output"

# Check 2 & 3: Parse benchstat sec/op section for regressions
echo ""
echo "=== Checking performance regressions ==="
regression_output=$(awk '
/sec\/op.*vs base/ { in_secop = 1; next }
in_secop && /^\s*$/ { in_secop = 0; next }
in_secop && (/B\/op/ || /allocs\/op/) { in_secop = 0; next }
in_secop {
    if (match($0, /[+-][0-9]+\.[0-9]+%/)) {
        pct_str = substr($0, RSTART, RLENGTH - 1)
        pct = pct_str + 0
        name = $1
        if (tolower(name) == "geomean" || name == "geomean") {
            if (pct > 1.0) {
                printf "FAIL: Geomean regression of %s%% exceeds 1%% threshold\n", pct_str
                fail = 1
            } else {
                printf "OK: Geomean delta %s%% within 1%% threshold\n", pct_str
            }
        } else {
            if (pct > 2.0) {
                printf "FAIL: %s regression of %s%% exceeds 2%% threshold\n", name, pct_str
                fail = 1
            }
        }
    }
}
END {
    if (!fail) printf "OK: No sec/op regressions above threshold.\n"
    exit fail ? 1 : 0
}
' "$stat_out") || rc=1

echo "$regression_output"

if [ "$rc" -eq 0 ]; then
    echo ""
    echo "All checks passed."
else
    echo ""
    echo "Regression checks failed."
fi

exit $rc
