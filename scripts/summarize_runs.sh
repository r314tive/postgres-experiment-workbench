#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/summarize_runs.sh [--output output.md] <series-dir|run-dir> [run-dir...]

Inputs can be:
  - a repeat or matrix directory containing runs.tsv;
  - one or more runs/<run-id>/ directories.

Output is a Markdown statistical summary. Metric counters are summarized as
last-minus-first deltas per run. Gauge-like metrics are summarized as per-run
maximums.
USAGE
}

OUT_FILE=""
RUN_DIRS=()
SEEN_RUN_DIRS=()

CUMULATIVE_METRICS=(
  xact_commit
  xact_rollback
  blks_read
  blks_hit
  tup_inserted
  tup_updated
  tup_deleted
  conflicts
  deadlocks
  temp_files
  temp_bytes
  wal_records
  wal_fpi
  wal_bytes
)

GAUGE_METRICS=(
  active_sessions
  waiting_sessions
  lock_waiting_sessions
  blocked_sessions
  locks_total
  locks_waiting
)

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --output)
      OUT_FILE="${2:?--output requires a path}"
      shift 2
      ;;
    --)
      shift
      break
      ;;
    -*)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      break
      ;;
  esac
done

if [[ $# -lt 1 ]]; then
  usage
  exit 2
fi

resolve_dir() {
  local input="$1"

  if [[ -d "$input" ]]; then
    realpath "$input"
    return 0
  fi

  if [[ -d "$REPO_DIR/$input" ]]; then
    realpath "$REPO_DIR/$input"
    return 0
  fi

  if [[ -d "$REPO_DIR/runs/$input" ]]; then
    realpath "$REPO_DIR/runs/$input"
    return 0
  fi

  echo "Directory not found: $input" >&2
  exit 1
}

add_run_dir() {
  local run_dir="$1"
  local seen

  run_dir="$(resolve_dir "$run_dir")"
  if [[ ! -f "$run_dir/manifest.env" && ! -f "$run_dir/verdict.env" && ! -f "$run_dir/metrics.csv" ]]; then
    echo "Not an experiment run directory: $run_dir" >&2
    exit 1
  fi

  for seen in "${SEEN_RUN_DIRS[@]:-}"; do
    if [[ "$seen" = "$run_dir" ]]; then
      return 0
    fi
  done

  SEEN_RUN_DIRS+=("$run_dir")
  RUN_DIRS+=("$run_dir")
}

add_input() {
  local input="$1"
  local dir
  local run_dir

  dir="$(resolve_dir "$input")"
  if [[ -f "$dir/runs.tsv" ]]; then
    while IFS= read -r run_dir; do
      [[ -z "$run_dir" ]] && continue
      add_run_dir "$run_dir"
    done < <(awk -F '\t' 'NR > 1 && $NF != "" {print $NF}' "$dir/runs.tsv")
  else
    add_run_dir "$dir"
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

  awk -F '=' -v key="$key" -v default="$default" '
    $1 == key {
      print substr($0, length(key) + 2)
      found = 1
      exit
    }
    END {
      if (!found) {
        printf "%s", default
      }
    }
  ' "$file"
}

metric_stat() {
  local file="$1"
  local column="$2"
  local field="$3"

  if [[ ! -s "$file" ]]; then
    printf 'n/a'
    return 0
  fi

  awk -F ',' -v col="$column" -v field="$field" '
    NR == 1 {
      for (i = 1; i <= NF; i++) {
        if ($i == col) idx = i
      }
      next
    }
    idx && $idx != "" {
      value = $idx + 0
      if (count == 0) {
        first = value
        min = value
        max = value
      }
      last = value
      if (value < min) min = value
      if (value > max) max = value
      count++
    }
    END {
      if (!idx || count == 0) {
        printf "n/a"
      } else if (field == "delta") {
        printf "%s", last - first
      } else if (field == "max") {
        printf "%s", max
      } else if (field == "count") {
        printf "%s", count
      }
    }
  ' "$file"
}

is_number() {
  [[ "$1" =~ ^-?[0-9]+([.][0-9]+)?$ ]]
}

append_run_metrics() {
  local run_dir="$1"
  local manifest="$run_dir/manifest.env"
  local verdict="$run_dir/verdict.env"
  local metrics="$run_dir/metrics.csv"
  local run_id status exit_code experiment pg_config profile_size workload metric value

  run_id="$(env_value "$manifest" run_id "$(basename "$run_dir")")"
  status="$(env_value "$verdict" status missing)"
  exit_code="$(env_value "$verdict" workload_exit 0)"
  experiment="$(env_value "$manifest" experiment_spec_id unknown)"
  pg_config="$(env_value "$manifest" experiment_pg_config unknown)"
  profile_size="$(env_value "$manifest" profile_size unknown)"
  workload="$(env_value "$manifest" workload_spec '')"

  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$run_id" "$status" "$exit_code" "$experiment" "$pg_config" "$profile_size" "$workload" >> "$RUNS_TSV"

  for metric in "${CUMULATIVE_METRICS[@]}"; do
    value="$(metric_stat "$metrics" "$metric" delta)"
    if is_number "$value"; then
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$run_id" "$status" "$experiment" "$pg_config" "$profile_size" "$workload" "$metric" delta "$value" >> "$METRICS_TSV"
    fi
  done

  for metric in "${GAUGE_METRICS[@]}"; do
    value="$(metric_stat "$metrics" "$metric" max)"
    if is_number "$value"; then
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$run_id" "$status" "$experiment" "$pg_config" "$profile_size" "$workload" "$metric" max "$value" >> "$METRICS_TSV"
    fi
  done
}

format_stats() {
  local mode="$1"
  local metric="$2"

  awk -F '\t' -v mode="$mode" -v metric="$metric" '
    function fmt(value, decimals) {
      if (decimals) {
        return sprintf("%.3f", value)
      }
      if (value == int(value)) {
        return sprintf("%d", value)
      }
      return sprintf("%.3f", value)
    }
    NR == 1 {
      next
    }
    $7 == metric && $8 == mode {
      value = $9 + 0
      if (n == 0 || value < min) min = value
      if (n == 0 || value > max) max = value
      sum += value
      sumsq += value * value
      n++
    }
    END {
      if (n == 0) {
        printf "0\tn/a\tn/a\tn/a\tn/a"
      } else {
        avg = sum / n
        variance = (sumsq / n) - (avg * avg)
        if (variance < 0) variance = 0
        printf "%d\t%s\t%s\t%s\t%s", n, fmt(min, 0), fmt(avg, 1), fmt(max, 0), fmt(sqrt(variance), 1)
      }
    }
  ' "$METRICS_TSV"
}

print_metric_table() {
  local title="$1"
  local mode="$2"
  shift 2
  local metric stats count min avg max stddev

  printf '## %s\n\n' "$title"
  printf '| Metric | Runs | Min | Avg | Max | Stddev |\n'
  printf '| --- | ---: | ---: | ---: | ---: | ---: |\n'
  for metric in "$@"; do
    IFS=$'\t' read -r count min avg max stddev <<< "$(format_stats "$mode" "$metric")"
    printf '| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n' "$metric" "$count" "$min" "$avg" "$max" "$stddev"
  done
  printf '\n'
}

render_summary() {
  local run_dir

  printf '# Run Series Summary\n\n'
  printf '| Field | Value |\n'
  printf '| --- | --- |\n'
  printf '| Runs | `%s` |\n' "${#RUN_DIRS[@]}"
  printf '| Generated at | `%s` |\n\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  printf '## Status Counts\n\n'
  printf '| Status | Runs |\n'
  printf '| --- | ---: |\n'
  awk -F '\t' 'NR > 1 {count[$2]++} END {for (status in count) print status "\t" count[status]}' "$RUNS_TSV" | sort | while IFS=$'\t' read -r status count; do
    printf '| `%s` | `%s` |\n' "$status" "$count"
  done
  printf '\n'

  printf '## Runs\n\n'
  printf '| Run id | Status | Experiment | PG config | Profile size | Workload |\n'
  printf '| --- | --- | --- | --- | --- | --- |\n'
  awk -F '\t' 'NR > 1 {
    printf "| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n", $1, $2, $4, $5, $6, $7
  }' "$RUNS_TSV"
  printf '\n'

  print_metric_table "Cumulative Metric Deltas" delta "${CUMULATIVE_METRICS[@]}"
  print_metric_table "Gauge Metric Maximums" max "${GAUGE_METRICS[@]}"

  printf '## Input Directories\n\n'
  for run_dir in "${RUN_DIRS[@]}"; do
    printf -- '- `%s`\n' "$run_dir"
  done
  printf '\n'
}

for input in "$@"; do
  add_input "$input"
done

if (( ${#RUN_DIRS[@]} == 0 )); then
  echo "No run directories found" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pg-workbench-summary.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT
RUNS_TSV="$TMP_DIR/runs.tsv"
METRICS_TSV="$TMP_DIR/metrics.tsv"

printf 'run_id\tstatus\texit_code\texperiment\tpg_config\tprofile_size\tworkload\n' > "$RUNS_TSV"
printf 'run_id\tstatus\texperiment\tpg_config\tprofile_size\tworkload\tmetric\tmode\tvalue\n' > "$METRICS_TSV"

for run_dir in "${RUN_DIRS[@]}"; do
  append_run_metrics "$run_dir"
done

if [[ -n "$OUT_FILE" ]]; then
  if [[ "$OUT_FILE" != /* ]]; then
    OUT_FILE="$REPO_DIR/$OUT_FILE"
  fi
  mkdir -p "$(dirname "$OUT_FILE")"
  render_summary > "$OUT_FILE"
  echo "Wrote run series summary: $OUT_FILE"
else
  render_summary
fi
