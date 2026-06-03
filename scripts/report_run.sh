#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/report_run.sh <run-dir-or-id> [output.md]

Renders a compact Markdown report from a runs/<run-id>/ directory. When
output.md is omitted the report is printed to stdout.
USAGE
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
  exit 0
fi

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage
  exit 2
fi

resolve_run_dir() {
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

  echo "Run directory not found: $input" >&2
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
      } else if (field == "first") {
        printf "%s", first
      } else if (field == "last") {
        printf "%s", last
      } else if (field == "delta") {
        printf "%s", last - first
      } else if (field == "min") {
        printf "%s", min
      } else if (field == "max") {
        printf "%s", max
      } else if (field == "count") {
        printf "%s", count
      }
    }
  ' "$file"
}

render_artifact_list() {
  local dir="$1"
  local label="$2"

  if [[ ! -d "$dir" ]]; then
    return 0
  fi

  local files
  files="$(find "$dir" -type f | sort | sed "s#^$RUN_DIR/##" | awk 'NR <= 20')"
  if [[ -z "$files" ]]; then
    return 0
  fi

  printf '### %s\n\n' "$label"
  while IFS= read -r file; do
    printf -- '- `%s`\n' "$file"
  done <<< "$files"
  printf '\n'
}

render_report() {
  local manifest="$RUN_DIR/manifest.env"
  local verdict="$RUN_DIR/verdict.env"
  local metrics="$RUN_DIR/metrics.csv"
  local status message samples metric

  status="$(env_value "$verdict" status missing)"
  message="$(env_value "$verdict" message '')"
  samples="$(metric_stat "$metrics" wal_bytes count)"

  printf '# Experiment Run Report\n\n'
  printf '| Field | Value |\n'
  printf '| --- | --- |\n'
  printf '| Run id | `%s` |\n' "$(env_value "$manifest" run_id "$(basename "$RUN_DIR")")"
  printf '| Status | `%s` |\n' "$status"
  printf '| Message | `%s` |\n' "$message"
  printf '| Started | `%s` |\n' "$(env_value "$manifest" started_at unknown)"
  printf '| Finished | `%s` |\n' "$(env_value "$verdict" finished_at unknown)"
  printf '| Experiment | `%s` |\n' "$(env_value "$manifest" experiment_spec_id unknown)"
  printf '| Topology | `%s` |\n' "$(env_value "$manifest" experiment_topology unknown)"
  printf '| PostgreSQL config | `%s` |\n' "$(env_value "$manifest" experiment_pg_config unknown)"
  printf '| Profile | `%s` |\n' "$(env_value "$manifest" profile '')"
  printf '| Dataset | `%s` |\n' "$(env_value "$manifest" dataset_spec '')"
  printf '| Workload | `%s` |\n' "$(env_value "$manifest" workload_spec '')"
  printf '| Background workloads | `%s` |\n' "$(env_value "$manifest" background_specs '')"
  printf '| Workload exit | `%s` |\n' "$(env_value "$verdict" workload_exit 0)"
  printf '| Assertion exit | `%s` |\n' "$(env_value "$verdict" assert_exit 0)"
  printf '| Scan exit | `%s` |\n' "$(env_value "$verdict" scan_exit 0)"
  printf '| Run dir | `%s` |\n\n' "$RUN_DIR"

  printf '## Metrics\n\n'
  if [[ "$samples" = "n/a" ]]; then
    printf 'No metrics.csv samples were found.\n\n'
  else
    printf 'Samples: `%s`\n\n' "$samples"
    printf '| Metric | First | Last | Delta | Min | Max |\n'
    printf '| --- | ---: | ---: | ---: | ---: | ---: |\n'
    for metric in active_sessions waiting_sessions lock_waiting_sessions blocked_sessions locks_total locks_waiting xact_commit xact_rollback blks_read blks_hit tup_inserted tup_updated tup_deleted deadlocks temp_files temp_bytes wal_records wal_fpi wal_bytes; do
      printf '| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n' \
        "$metric" \
        "$(metric_stat "$metrics" "$metric" first)" \
        "$(metric_stat "$metrics" "$metric" last)" \
        "$(metric_stat "$metrics" "$metric" delta)" \
        "$(metric_stat "$metrics" "$metric" min)" \
        "$(metric_stat "$metrics" "$metric" max)"
    done
    printf '\n'
  fi

  printf '## Artifacts\n\n'
  render_artifact_list "$RUN_DIR/snapshots" "Snapshots"
  render_artifact_list "$RUN_DIR/background" "Background Logs"
  render_artifact_list "$RUN_DIR/artifacts" "Extra Artifacts"
  printf -- '- `%s`\n' "stdout.log"
  [[ -f "$RUN_DIR/workload.log" ]] && printf -- '- `%s`\n' "workload.log"
  [[ -f "$RUN_DIR/scan.log" ]] && printf -- '- `%s`\n' "scan.log"
  [[ -f "$RUN_DIR/verdict.json" ]] && printf -- '- `%s`\n' "verdict.json"
  printf '\n'
}

RUN_DIR="$(resolve_run_dir "$1")"
OUT_FILE="${2:-}"

if [[ -n "$OUT_FILE" ]]; then
  if [[ "$OUT_FILE" != /* ]]; then
    OUT_FILE="$REPO_DIR/$OUT_FILE"
  fi
  mkdir -p "$(dirname "$OUT_FILE")"
  render_report > "$OUT_FILE"
  echo "Wrote report: $OUT_FILE"
else
  render_report
fi
