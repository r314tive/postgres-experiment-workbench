#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/compare_runs.sh <baseline-run-dir> <candidate-run-dir>

Compares run verdicts and selected metrics deltas. Output is Markdown.
USAGE
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
  exit 0
fi

if [[ $# -ne 2 ]]; then
  usage
  exit 2
fi

BASE="$1"
CAND="$2"

metric_delta() {
  local file="$1"
  local column="$2"

  if [[ ! -s "$file" ]]; then
    printf 'n/a'
    return 0
  fi

  awk -F ',' -v col="$column" '
    NR == 1 {
      for (i = 1; i <= NF; i++) {
        if ($i == col) idx = i
      }
      next
    }
    idx {
      if (count == 0) first = $idx
      last = $idx
      count++
    }
    END {
      if (!idx || count == 0) {
        printf "n/a"
      } else {
        printf "%s", (last - first)
      }
    }
  ' "$file"
}

verdict_value() {
  local dir="$1"
  local key="$2"
  if [[ -f "$dir/verdict.env" ]]; then
    awk -F '=' -v key="$key" '$1 == key {print substr($0, length(key) + 2)}' "$dir/verdict.env"
  else
    printf 'missing'
  fi
}

printf '# Run Comparison\n\n'
printf '| Field | Baseline | Candidate |\n'
printf '| --- | --- | --- |\n'
printf '| Run dir | `%s` | `%s` |\n' "$BASE" "$CAND"
printf '| Status | `%s` | `%s` |\n' "$(verdict_value "$BASE" status)" "$(verdict_value "$CAND" status)"
printf '| Message | `%s` | `%s` |\n' "$(verdict_value "$BASE" message)" "$(verdict_value "$CAND" message)"
printf '| WAL bytes delta | `%s` | `%s` |\n' "$(metric_delta "$BASE/metrics.csv" wal_bytes)" "$(metric_delta "$CAND/metrics.csv" wal_bytes)"
printf '| Temp bytes delta | `%s` | `%s` |\n' "$(metric_delta "$BASE/metrics.csv" temp_bytes)" "$(metric_delta "$CAND/metrics.csv" temp_bytes)"
printf '| Tuples inserted delta | `%s` | `%s` |\n' "$(metric_delta "$BASE/metrics.csv" tup_inserted)" "$(metric_delta "$CAND/metrics.csv" tup_inserted)"
printf '| Tuples updated delta | `%s` | `%s` |\n' "$(metric_delta "$BASE/metrics.csv" tup_updated)" "$(metric_delta "$CAND/metrics.csv" tup_updated)"
printf '| Tuples deleted delta | `%s` | `%s` |\n' "$(metric_delta "$BASE/metrics.csv" tup_deleted)" "$(metric_delta "$CAND/metrics.csv" tup_deleted)"
