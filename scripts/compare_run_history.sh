#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/compare_run_history.sh [--output output.md] <series-dir|run-dir> [series-dir|run-dir...]

Inputs can be repeat or matrix directories containing runs.tsv, or individual
runs/<run-id>/ directories. Output is a Markdown history/trend comparison.
Series are compared in the order provided.
USAGE
}

OUT_FILE=""
INPUTS=()

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

INPUTS=("$@")

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
      }
    }
  ' "$file"
}

is_number() {
  [[ "$1" =~ ^-?[0-9]+([.][0-9]+)?$ ]]
}

format_number() {
  local value="$1"

  if ! is_number "$value"; then
    printf '%s' "$value"
    return 0
  fi

  awk -v value="$value" 'BEGIN {
    if (value == int(value)) {
      printf "%d", value
    } else {
      printf "%.3f", value
    }
  }'
}

series_label() {
  local dir="$1"
  local parent

  parent="$(basename "$(dirname "$dir")")"
  if [[ "$parent" = "repeats" || "$parent" = "matrices" || "$parent" = "runs" ]]; then
    basename "$dir"
  else
    printf '%s/%s' "$parent" "$(basename "$dir")"
  fi
}

collect_run_dirs() {
  local dir="$1"

  if [[ -f "$dir/runs.tsv" ]]; then
    awk -F '\t' 'NR > 1 && $NF != "" {print $NF}' "$dir/runs.tsv"
    return 0
  fi

  if [[ -f "$dir/manifest.env" || -f "$dir/verdict.env" || -f "$dir/metrics.csv" ]]; then
    printf '%s\n' "$dir"
    return 0
  fi

  echo "Not a series or run directory: $dir" >&2
  exit 1
}

metric_values_for_series() {
  local metric="$1"
  local mode="$2"
  shift 2
  local run_dir value

  for run_dir in "$@"; do
    value="$(metric_stat "$run_dir/metrics.csv" "$metric" "$mode")"
    if is_number "$value"; then
      printf '%s\n' "$value"
    fi
  done
}

append_metric_stats() {
  local series_index="$1"
  local label="$2"
  local metric="$3"
  local mode="$4"
  shift 4
  local stats

  stats="$(metric_values_for_series "$metric" "$mode" "$@" | awk '
    {
      value = $1 + 0
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
        printf "%d\t%s\t%s\t%s\t%s", n, min, avg, max, sqrt(variance)
      }
    }
  ')"

  printf '%s\t%s\t%s\t%s\t%s\n' "$series_index" "$label" "$metric" "$mode" "$stats" >> "$METRICS_TSV"
}

append_series() {
  local series_index="$1"
  local input="$2"
  local dir label run_dir status
  local passed=0
  local failed=0
  local other=0
  local run_dirs=()
  local metric

  dir="$(resolve_dir "$input")"
  label="$(series_label "$dir")"

  while IFS= read -r run_dir; do
    [[ -z "$run_dir" ]] && continue
    run_dir="$(resolve_dir "$run_dir")"
    run_dirs+=("$run_dir")
  done < <(collect_run_dirs "$dir")

  if (( ${#run_dirs[@]} == 0 )); then
    echo "No run directories found in $dir" >&2
    exit 1
  fi

  for run_dir in "${run_dirs[@]}"; do
    status="$(env_value "$run_dir/verdict.env" status missing)"
    case "$status" in
      passed)
        ((passed += 1))
        ;;
      failed)
        ((failed += 1))
        ;;
      *)
        ((other += 1))
        ;;
    esac
  done

  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$series_index" "$label" "$dir" "${#run_dirs[@]}" "$passed" "$failed" "$other" >> "$SERIES_TSV"

  for metric in "${CUMULATIVE_METRICS[@]}"; do
    append_metric_stats "$series_index" "$label" "$metric" delta "${run_dirs[@]}"
  done

  for metric in "${GAUGE_METRICS[@]}"; do
    append_metric_stats "$series_index" "$label" "$metric" max "${run_dirs[@]}"
  done
}

metric_avg() {
  local series_index="$1"
  local metric="$2"
  local mode="$3"

  awk -F '\t' -v idx="$series_index" -v metric="$metric" -v mode="$mode" '
    $1 == idx && $3 == metric && $4 == mode {
      print $7
      found = 1
      exit
    }
    END {
      if (!found) print "n/a"
    }
  ' "$METRICS_TSV"
}

metric_trend() {
  local metric="$1"
  local mode="$2"
  local first last

  first="$(metric_avg 1 "$metric" "$mode")"
  last="$(metric_avg "$SERIES_COUNT" "$metric" "$mode")"

  if is_number "$first" && is_number "$last"; then
    format_number "$(awk -v first="$first" -v last="$last" 'BEGIN {print last - first}')"
  else
    printf 'n/a'
  fi
}

print_metric_table() {
  local title="$1"
  local mode="$2"
  shift 2
  local metric series_index avg trend

  printf '## %s\n\n' "$title"
  printf '| Metric |'
  awk -F '\t' 'NR > 1 {printf " %s |", "`" $2 "`"}' "$SERIES_TSV"
  printf ' Trend |\n'

  printf '| --- |'
  awk -F '\t' 'NR > 1 {printf " ---: |"}' "$SERIES_TSV"
  printf ' ---: |\n'

  for metric in "$@"; do
    printf '| `%s` |' "$metric"
    for ((series_index = 1; series_index <= SERIES_COUNT; series_index++)); do
      avg="$(metric_avg "$series_index" "$metric" "$mode")"
      printf ' `%s` |' "$(format_number "$avg")"
    done
    trend="$(metric_trend "$metric" "$mode")"
    printf ' `%s` |\n' "$trend"
  done
  printf '\n'
}

render_history() {
  printf '# Run History Comparison\n\n'
  printf '| Field | Value |\n'
  printf '| --- | --- |\n'
  printf '| Series | `%s` |\n' "$SERIES_COUNT"
  printf '| Generated at | `%s` |\n\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  printf '## Series\n\n'
  printf '| Series | Runs | Passed | Failed | Other | Directory |\n'
  printf '| --- | ---: | ---: | ---: | ---: | --- |\n'
  awk -F '\t' 'NR > 1 {
    printf "| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n", $2, $4, $5, $6, $7, $3
  }' "$SERIES_TSV"
  printf '\n'

  print_metric_table "Cumulative Metric Delta Averages" delta "${CUMULATIVE_METRICS[@]}"
  print_metric_table "Gauge Metric Maximum Averages" max "${GAUGE_METRICS[@]}"
}

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pg-workbench-history.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT
SERIES_TSV="$TMP_DIR/series.tsv"
METRICS_TSV="$TMP_DIR/metrics.tsv"

printf 'series_index\tlabel\tdir\truns\tpassed\tfailed\tother\n' > "$SERIES_TSV"
printf 'series_index\tlabel\tmetric\tmode\tcount\tmin\tavg\tmax\tstddev\n' > "$METRICS_TSV"

series_index=0
for input in "${INPUTS[@]}"; do
  ((series_index += 1))
  append_series "$series_index" "$input"
done
SERIES_COUNT="$series_index"

if [[ -n "$OUT_FILE" ]]; then
  if [[ "$OUT_FILE" != /* ]]; then
    OUT_FILE="$REPO_DIR/$OUT_FILE"
  fi
  mkdir -p "$(dirname "$OUT_FILE")"
  render_history > "$OUT_FILE"
  echo "Wrote run history comparison: $OUT_FILE"
else
  render_history
fi
