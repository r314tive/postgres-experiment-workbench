#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_experiment_repeated.sh <experiment-spec> [count]

Environment:
  EXPERIMENT_REPEAT_COUNT=3
  EXPERIMENT_REPEAT_ID=<auto>
  EXPERIMENT_REPEAT_DIR=runs/repeats/<repeat-id>
  EXPERIMENT_REPEAT_STOP_ON_FAIL=0

Runs the same experiment multiple times, writes per-run reports, optional
pairwise comparisons against the first run, and a repeat summary.
USAGE
}

timestamp() {
  date -u +%Y%m%d_%H%M%S
}

sanitize_id() {
  printf '%s' "$1" | tr '/ ' '__' | tr -cd '[:alnum:]_.-'
}

require_positive_int() {
  local label="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "$label must be a positive integer, got: $value" >&2
    exit 2
  fi
}

env_value() {
  local file="$1"
  local key="$2"
  local default="${3:-}"

  if [[ ! -f "$file" ]]; then
    printf '%s' "$default"
    return 0
  fi

  awk -F '=' -v key="$key" -v fallback="$default" '
    $1 == key {
      print substr($0, length(key) + 2)
      found = 1
      exit
    }
    END {
      if (!found) {
        printf "%s", fallback
      }
    }
  ' "$file"
}

write_summary() {
  local summary="$SERIES_DIR/summary.md"

  {
    printf '# Experiment Repeat Summary\n\n'
    printf '| Field | Value |\n'
    printf '| --- | --- |\n'
    printf '| Repeat id | `%s` |\n' "$REPEAT_ID"
    printf '| Experiment | `%s` |\n' "$EXPERIMENT_SPEC"
    printf '| Count | `%s` |\n' "$COUNT"
    printf '| Series dir | `%s` |\n\n' "$SERIES_DIR"

    printf '| Iteration | Run id | Status | Message | Exit |\n'
    printf '| ---: | --- | --- | --- | ---: |\n'
    awk -F '\t' 'NR > 1 {
      printf "| `%s` | `%s` | `%s` | `%s` | `%s` |\n", $1, $2, $4, $5, $3
    }' "$SERIES_DIR/runs.tsv"
  } > "$summary"
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
  exit 0
fi

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage
  exit 2
fi

EXPERIMENT_SPEC="$1"
COUNT="${2:-${EXPERIMENT_REPEAT_COUNT:-3}}"
require_positive_int EXPERIMENT_REPEAT_COUNT "$COUNT"

REPEAT_ID="${EXPERIMENT_REPEAT_ID:-$(sanitize_id "$EXPERIMENT_SPEC")-repeat-$(timestamp)}"
SERIES_DIR="${EXPERIMENT_REPEAT_DIR:-$REPO_DIR/runs/repeats/$REPEAT_ID}"
STOP_ON_FAIL="${EXPERIMENT_REPEAT_STOP_ON_FAIL:-0}"

if [[ "$SERIES_DIR" != /* ]]; then
  SERIES_DIR="$REPO_DIR/$SERIES_DIR"
fi

mkdir -p "$SERIES_DIR/reports" "$SERIES_DIR/compare" "$SERIES_DIR/driver-logs"
printf 'iteration\trun_id\texit_code\tstatus\tmessage\trun_dir\n' > "$SERIES_DIR/runs.tsv"

BASELINE_DIR=""
ANY_FAILURE=0

for ((i = 1; i <= COUNT; i++)); do
  RUN_ID="$(printf '%s-%02d' "$REPEAT_ID" "$i")"
  RUN_DIR="$REPO_DIR/runs/$RUN_ID"
  DRIVER_LOG="$SERIES_DIR/driver-logs/$RUN_ID.log"

  echo "repeat_iteration=$i run_id=$RUN_ID"

  set +e
  EXPERIMENT_RUN_ID="$RUN_ID" "$REPO_DIR/scripts/run_experiment.sh" run "$EXPERIMENT_SPEC" > "$DRIVER_LOG" 2>&1
  EXIT_CODE="$?"
  set -e

  STATUS="$(env_value "$RUN_DIR/verdict.env" status failed)"
  MESSAGE="$(env_value "$RUN_DIR/verdict.env" message 'missing verdict')"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$i" "$RUN_ID" "$EXIT_CODE" "$STATUS" "$MESSAGE" "$RUN_DIR" >> "$SERIES_DIR/runs.tsv"

  if [[ -d "$RUN_DIR" ]]; then
    "$REPO_DIR/scripts/report_run.sh" "$RUN_DIR" "$SERIES_DIR/reports/$RUN_ID.md" >/dev/null
  fi

  if [[ -z "$BASELINE_DIR" ]]; then
    BASELINE_DIR="$RUN_DIR"
  elif [[ -d "$BASELINE_DIR" && -d "$RUN_DIR" ]]; then
    "$REPO_DIR/scripts/compare_runs.sh" "$BASELINE_DIR" "$RUN_DIR" > "$SERIES_DIR/compare/01-vs-$(printf '%02d' "$i").md"
  fi

  if [[ "$EXIT_CODE" != "0" ]]; then
    ANY_FAILURE=1
    if [[ "$STOP_ON_FAIL" = "1" ]]; then
      break
    fi
  fi
done

write_summary
"$REPO_DIR/scripts/summarize_runs.sh" "$SERIES_DIR" > "$SERIES_DIR/statistics.md"
echo "repeat_dir=$SERIES_DIR"
echo "summary=$SERIES_DIR/summary.md"
echo "statistics=$SERIES_DIR/statistics.md"

exit "$ANY_FAILURE"
