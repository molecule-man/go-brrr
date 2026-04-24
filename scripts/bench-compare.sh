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
# bench.sh child is also interrupted. That is fine: any benchmark lines already
# flushed to /tmp/before.txt and /tmp/after.txt by completed runs are still
# used for the partial output.
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

./scripts/testbins.sh

: > "$BEFORE_TXT"
: > "$AFTER_TXT"

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
                        BENCH_CORPUS_FILE="$rawfile" ./scripts/bench.sh "CompressCorpusFile\$/q=$q\$" > /dev/null
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
            CompressDict)
                for q in "${QUALITIES[@]}"; do
                    ./scripts/bench.sh "$b\$/q=$q\$" > /dev/null
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
