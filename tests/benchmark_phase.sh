#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-phase.XXXXXX")"
trap 'rm -rf -- "$TMP_DIR"' EXIT

# shellcheck source=../scripts/benchmark_phase.sh
source "$REPO_DIR/scripts/benchmark_phase.sh"

PGWORKBENCH_BENCHMARK_RUN_ID=phase-test-t001
PGWORKBENCH_BENCHMARK_TRIAL=1
PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE=
export PGWORKBENCH_BENCHMARK_RUN_ID PGWORKBENCH_BENCHMARK_TRIAL PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE

high_resolution_now="$(benchmark_phase_now)"
[[ "$high_resolution_now" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{1,9}Z$ ]]
test "$(benchmark_phase_timestamp_key 2026-08-12T00:00:00Z)" = "2026-08-12T00:00:00.000000000"
test "$(benchmark_phase_timestamp_key 2026-08-12T00:00:00.1Z)" = "2026-08-12T00:00:00.100000000"

PGWORKBENCH_BENCHMARK_PHASE_FILE="$TMP_DIR/empty-preflight-failure.tsv"
BENCHMARK_PREFLIGHT_STARTED_AT=2026-08-12T00:00:00.000001Z
export PGWORKBENCH_BENCHMARK_PHASE_FILE BENCHMARK_PREFLIGHT_STARTED_AT
: > "$PGWORKBENCH_BENCHMARK_PHASE_FILE"
benchmark_phase_complete_before_cleanup 17
test "$BENCHMARK_PHASE_BACKFILLED_FAILURE" = 1
benchmark_phase_append 11 cleanup passed 2026-08-12T00:00:01Z 2026-08-12T00:00:01Z ""
test "$(wc -l < "$PGWORKBENCH_BENCHMARK_PHASE_FILE" | tr -d ' ')" = 11
awk -F '\t' 'NR == 1 { exit !($1 == "phase-test-t001" && $2 == 1 && $3 == 1 && $4 == "preflight" && $5 == "failed" && $6 == "2026-08-12T00:00:00.000001Z") }' "$PGWORKBENCH_BENCHMARK_PHASE_FILE"
awk -F '\t' 'NR >= 2 && NR <= 10 { if ($5 != "skipped") exit 1 }' "$PGWORKBENCH_BENCHMARK_PHASE_FILE"

PGWORKBENCH_BENCHMARK_PHASE_FILE="$TMP_DIR/aborted.tsv"
export PGWORKBENCH_BENCHMARK_PHASE_FILE
: > "$PGWORKBENCH_BENCHMARK_PHASE_FILE"
benchmark_phase_append 1 preflight passed 2026-08-12T00:00:00Z 2026-08-12T00:00:00Z ""
benchmark_phase_complete_before_cleanup 42
test "$BENCHMARK_PHASE_BACKFILLED_FAILURE" = 1
benchmark_phase_append 11 cleanup passed 2026-08-12T00:00:01Z 2026-08-12T00:00:01Z ""
test "$(wc -l < "$PGWORKBENCH_BENCHMARK_PHASE_FILE" | tr -d ' ')" = 11
awk -F '\t' 'NR == 2 { exit !($3 == 2 && $4 == "prepare" && $5 == "failed") }' "$PGWORKBENCH_BENCHMARK_PHASE_FILE"
awk -F '\t' 'NR == 10 { exit !($3 == 10 && $4 == "collect" && $5 == "skipped") }' "$PGWORKBENCH_BENCHMARK_PHASE_FILE"

PGWORKBENCH_BENCHMARK_PHASE_FILE="$TMP_DIR/prior-failure.tsv"
: > "$PGWORKBENCH_BENCHMARK_PHASE_FILE"
for sequence in 1 2 3 4 5 6; do
  name="$(benchmark_phase_name "$sequence")"
  status=passed
  reason=""
  if [[ "$sequence" = 3 ]]; then status=skipped; reason="not declared"; fi
  if [[ "$sequence" = 4 ]]; then status=skipped; reason="protocol controls are not enabled"; fi
  if [[ "$sequence" = 5 ]]; then status=skipped; reason="zero duration"; fi
  if [[ "$sequence" = 6 ]]; then status=skipped; reason="protocol controls are not enabled"; fi
  benchmark_phase_append "$sequence" "$name" "$status" 2026-08-12T00:00:00Z 2026-08-12T00:00:00Z "$reason"
done
benchmark_phase_append 7 measure failed 2026-08-12T00:00:00Z 2026-08-12T00:00:01Z "pgbench exited 1"
benchmark_phase_complete_before_cleanup 1
test "$BENCHMARK_PHASE_BACKFILLED_FAILURE" = 0
awk -F '\t' 'NR == 8 { exit !($3 == 8 && $4 == "cooldown" && $5 == "skipped") }' "$PGWORKBENCH_BENCHMARK_PHASE_FILE"

# A skipped terminal record can follow the first failure. Completion must find
# that earlier failure and continue skipping rather than inventing a second one.
PGWORKBENCH_BENCHMARK_PHASE_FILE="$TMP_DIR/prior-failure-then-skip.tsv"
: > "$PGWORKBENCH_BENCHMARK_PHASE_FILE"
benchmark_phase_append 1 preflight passed 2026-08-12T00:00:00Z 2026-08-12T00:00:00Z ""
benchmark_phase_append 2 prepare passed 2026-08-12T00:00:00Z 2026-08-12T00:00:00Z ""
benchmark_phase_append 3 stabilize passed 2026-08-12T00:00:00Z 2026-08-12T00:00:00Z ""
benchmark_phase_append 4 pre-warmup-control skipped 2026-08-12T00:00:00Z 2026-08-12T00:00:00Z "protocol controls are not enabled"
benchmark_phase_append 5 warmup failed 2026-08-12T00:00:00Z 2026-08-12T00:00:01Z "pgbench exited 1"
benchmark_phase_append 6 pre-measure-control skipped 2026-08-12T00:00:01Z 2026-08-12T00:00:01Z "not reached after failed warmup phase"
benchmark_phase_append 7 measure skipped 2026-08-12T00:00:01Z 2026-08-12T00:00:01Z "not reached after failed warmup phase"
benchmark_phase_complete_before_cleanup 1
test "$BENCHMARK_PHASE_BACKFILLED_FAILURE" = 0
awk -F '\t' 'NR >= 6 { exit !($5 == "skipped" && $8 ~ /failed warmup phase|earlier benchmark phase failure/) }' "$PGWORKBENCH_BENCHMARK_PHASE_FILE"

PGWORKBENCH_BENCHMARK_PHASE_FILE="$TMP_DIR/rejected-events.tsv"
: > "$PGWORKBENCH_BENCHMARK_PHASE_FILE"
if benchmark_phase_append 7 measure failed 2026-08-12T00:00:00.000001Z 2026-08-12T00:00:00.000002Z "" 2>/dev/null; then
  echo "failed phase without reason was accepted" >&2
  exit 1
fi
if benchmark_phase_append 11 cleanup skipped 2026-08-12T00:00:00.000001Z 2026-08-12T00:00:00.000002Z "not reached" 2>/dev/null; then
  echo "skipped cleanup phase was accepted" >&2
  exit 1
fi
if benchmark_phase_append 2 warmup passed 2026-08-12T00:00:00.000001Z 2026-08-12T00:00:00.000002Z "" 2>/dev/null; then
  echo "mismatched sequence/name phase was accepted" >&2
  exit 1
fi
test ! -s "$PGWORKBENCH_BENCHMARK_PHASE_FILE"

PGWORKBENCH_BENCHMARK_RUN_ID=''
if benchmark_phase_append 1 preflight passed 2026-08-12T00:00:00.000001Z 2026-08-12T00:00:00.000002Z "" 2>/dev/null; then
  echo "phase event without a run binding was accepted" >&2
  exit 1
fi
PGWORKBENCH_BENCHMARK_RUN_ID=phase-test-t001

# Background workloads get an empty journal capability and therefore cannot
# append warmup/measure records to their parent's evidence file.
parent_size="$(wc -c < "$PGWORKBENCH_BENCHMARK_PHASE_FILE")"
PGWORKBENCH_BENCHMARK_PHASE_FILE='' benchmark_phase_append 5 warmup passed 2026-08-12T00:00:00Z 2026-08-12T00:00:01Z ""
test "$(wc -c < "$PGWORKBENCH_BENCHMARK_PHASE_FILE")" = "$parent_size"
grep -A12 '^start_background_specs()' "$REPO_DIR/scripts/run_experiment.sh" | grep -q 'PGWORKBENCH_BENCHMARK_PHASE_FILE='

echo "PASS: benchmark phase clock, validation, abort completion, and background journal isolation"
