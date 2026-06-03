#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_experiment_matrix.sh list
  scripts/run_experiment_matrix.sh show <matrix-spec>
  scripts/run_experiment_matrix.sh plan <matrix-spec>
  scripts/run_experiment_matrix.sh run <matrix-spec>

Matrix specs live under matrices/**/*.env and define combinations of
experiments, PostgreSQL config profiles, profile sizes, and repeat counts.
USAGE
}

timestamp() {
  date -u +%Y%m%d_%H%M%S
}

sanitize_id() {
  printf '%s' "$1" | tr '/ ' '__' | tr -cd '[:alnum:]_.-'
}

list_specs() {
  find "$REPO_DIR/matrices" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    spec="${spec#"$REPO_DIR/matrices/"}"
    printf '%s\n' "${spec%.env}"
  done
}

resolve_spec() {
  local input="${1:?matrix spec is required}"
  local candidate

  if [[ -f "$input" ]]; then
    realpath "$input"
    return 0
  fi

  candidate="$REPO_DIR/$input"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/matrices/$input"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/matrices/$input.env"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  mapfile -t matches < <(find "$REPO_DIR/matrices" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    local id="${spec#"$REPO_DIR/matrices/"}"
    id="${id%.env}"
    if [[ "$id" = "$input" || "$(basename "$id")" = "$input" ]]; then
      printf '%s\n' "$spec"
    fi
  done)

  if (( ${#matches[@]} == 1 )); then
    realpath "${matches[0]}"
    return 0
  fi

  if (( ${#matches[@]} > 1 )); then
    echo "Ambiguous matrix spec: $input" >&2
    printf '  %s\n' "${matches[@]#"$REPO_DIR/matrices/"}" >&2
    exit 2
  fi

  echo "Matrix spec not found: $input" >&2
  exit 1
}

require_positive_int() {
  local label="$1"
  local value="$2"

  if ! [[ "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "$label must be a positive integer, got: $value" >&2
    exit 2
  fi
}

load_matrix() {
  MATRIX_SPEC_FILE="$(resolve_spec "$1")"
  MATRIX_SPEC_ID="${MATRIX_SPEC_FILE#"$REPO_DIR/matrices/"}"
  MATRIX_SPEC_ID="${MATRIX_SPEC_ID%.env}"

  set -a
  # shellcheck disable=SC1090
  source "$MATRIX_SPEC_FILE"
  set +a

  MATRIX_NAME="${MATRIX_NAME:-$MATRIX_SPEC_ID}"
  MATRIX_EXPERIMENTS="${MATRIX_EXPERIMENTS:-smoke}"
  MATRIX_PG_CONFIGS="${MATRIX_PG_CONFIGS:-default}"
  MATRIX_PROFILE_SIZES="${MATRIX_PROFILE_SIZES:-small}"
  MATRIX_REPEATS="${MATRIX_REPEATS:-1}"
  MATRIX_STOP_ON_FAIL="${MATRIX_STOP_ON_FAIL:-0}"
  MATRIX_DOCKER_RESET="${MATRIX_DOCKER_RESET:-0}"
  require_positive_int MATRIX_REPEATS "$MATRIX_REPEATS"

  read -r -a MATRIX_EXPERIMENT_LIST <<< "$MATRIX_EXPERIMENTS"
  read -r -a MATRIX_PG_CONFIG_LIST <<< "$MATRIX_PG_CONFIGS"
  read -r -a MATRIX_PROFILE_SIZE_LIST <<< "$MATRIX_PROFILE_SIZES"
}

plan_matrix() {
  local experiment pg_config profile_size repeat

  printf '# Experiment Matrix Plan\n\n'
  printf '| Experiment | PG config | Profile size | Repeat |\n'
  printf '| --- | --- | --- | ---: |\n'
  for experiment in "${MATRIX_EXPERIMENT_LIST[@]}"; do
    for pg_config in "${MATRIX_PG_CONFIG_LIST[@]}"; do
      for profile_size in "${MATRIX_PROFILE_SIZE_LIST[@]}"; do
        for ((repeat = 1; repeat <= MATRIX_REPEATS; repeat++)); do
          printf '| `%s` | `%s` | `%s` | `%s` |\n' "$experiment" "$pg_config" "$profile_size" "$repeat"
        done
      done
    done
  done
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

write_summary() {
  local summary="$MATRIX_RUN_DIR/summary.md"

  {
    printf '# Experiment Matrix Summary\n\n'
    printf '| Field | Value |\n'
    printf '| --- | --- |\n'
    printf '| Matrix id | `%s` |\n' "$MATRIX_RUN_ID"
    printf '| Matrix spec | `%s` |\n' "$MATRIX_SPEC_ID"
    printf '| Matrix name | `%s` |\n' "$MATRIX_NAME"
    printf '| Matrix dir | `%s` |\n\n' "$MATRIX_RUN_DIR"

    printf '| Experiment | PG config | Profile size | Repeat | Run id | Status | Message | Exit |\n'
    printf '| --- | --- | --- | ---: | --- | --- | --- | ---: |\n'
    awk -F '\t' 'NR > 1 {
      printf "| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n", $1, $2, $3, $4, $5, $7, $8, $6
    }' "$MATRIX_RUN_DIR/runs.tsv"
  } > "$summary"
}

run_matrix() {
  local experiment pg_config profile_size repeat run_id run_dir exit_code status message
  local any_failure=0

  MATRIX_RUN_ID="${MATRIX_RUN_ID:-$(sanitize_id "$MATRIX_SPEC_ID")-matrix-$(timestamp)}"
  MATRIX_RUN_DIR="${MATRIX_RUN_DIR:-$REPO_DIR/runs/matrices/$MATRIX_RUN_ID}"
  if [[ "$MATRIX_RUN_DIR" != /* ]]; then
    MATRIX_RUN_DIR="$REPO_DIR/$MATRIX_RUN_DIR"
  fi

  mkdir -p "$MATRIX_RUN_DIR/reports" "$MATRIX_RUN_DIR/driver-logs"
  printf 'experiment\tpg_config\tprofile_size\trepeat\trun_id\texit_code\tstatus\tmessage\trun_dir\n' > "$MATRIX_RUN_DIR/runs.tsv"

  for experiment in "${MATRIX_EXPERIMENT_LIST[@]}"; do
    for pg_config in "${MATRIX_PG_CONFIG_LIST[@]}"; do
      for profile_size in "${MATRIX_PROFILE_SIZE_LIST[@]}"; do
        for ((repeat = 1; repeat <= MATRIX_REPEATS; repeat++)); do
          run_id="$(printf '%s-%s-%s-r%02d' "$MATRIX_RUN_ID" "$(sanitize_id "$experiment")" "$(sanitize_id "$pg_config-$profile_size")" "$repeat")"
          run_dir="$REPO_DIR/runs/$run_id"

          echo "matrix_run experiment=$experiment pg_config=$pg_config profile_size=$profile_size repeat=$repeat run_id=$run_id"

          set +e
          EXPERIMENT_RUN_ID="$run_id" \
          EXPERIMENT_PG_CONFIG="$pg_config" \
          EXPERIMENT_PROFILE_SIZE="$profile_size" \
          EXPERIMENT_DOCKER_RESET="$MATRIX_DOCKER_RESET" \
            "$REPO_DIR/scripts/run_experiment.sh" run "$experiment" > "$MATRIX_RUN_DIR/driver-logs/$run_id.log" 2>&1
          exit_code="$?"
          set -e

          status="$(env_value "$run_dir/verdict.env" status failed)"
          message="$(env_value "$run_dir/verdict.env" message 'missing verdict')"
          printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$experiment" "$pg_config" "$profile_size" "$repeat" "$run_id" "$exit_code" "$status" "$message" "$run_dir" >> "$MATRIX_RUN_DIR/runs.tsv"

          if [[ -d "$run_dir" ]]; then
            "$REPO_DIR/scripts/report_run.sh" "$run_dir" "$MATRIX_RUN_DIR/reports/$run_id.md" >/dev/null
          fi

          if [[ "$exit_code" != "0" ]]; then
            any_failure=1
            if [[ "$MATRIX_STOP_ON_FAIL" = "1" ]]; then
              write_summary
              exit "$any_failure"
            fi
          fi
        done
      done
    done
  done

  write_summary
  echo "matrix_dir=$MATRIX_RUN_DIR"
  echo "summary=$MATRIX_RUN_DIR/summary.md"
  exit "$any_failure"
}

ACTION="${1:-help}"
if [[ $# -gt 0 ]]; then
  shift
fi

case "$ACTION" in
  help|-h|--help)
    usage
    ;;
  list)
    list_specs
    ;;
  show)
    sed -n '1,220p' "$(resolve_spec "${1:?matrix spec is required}")"
    ;;
  plan)
    load_matrix "${1:?matrix spec is required}"
    plan_matrix
    ;;
  run)
    load_matrix "${1:?matrix spec is required}"
    run_matrix
    ;;
  *)
    load_matrix "$ACTION"
    plan_matrix
    ;;
esac
