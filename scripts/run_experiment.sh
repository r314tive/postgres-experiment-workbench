#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=exact_environment.sh
source "$REPO_DIR/scripts/exact_environment.sh"
pgworkbench_initialize_exact_environment
# shellcheck source=target_arg_guard.sh
source "$REPO_DIR/scripts/target_arg_guard.sh"
# shellcheck source=benchmark_phase.sh
source "$REPO_DIR/scripts/benchmark_phase.sh"
# shellcheck source=benchmark_control.sh
source "$REPO_DIR/scripts/benchmark_control.sh"
# shellcheck source=benchmark_capsule.sh
source "$REPO_DIR/scripts/benchmark_capsule.sh"
# shellcheck source=capture_effective_pg_settings.sh
source "$REPO_DIR/scripts/capture_effective_pg_settings.sh"
# shellcheck source=process_lifecycle.sh
source "$REPO_DIR/scripts/process_lifecycle.sh"
PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_experiment.sh list
  scripts/run_experiment.sh show <experiment-spec>
  scripts/run_experiment.sh run <experiment-spec>
  scripts/run_experiment.sh <experiment-spec>

Experiment specs live under experiments/**/*.env and orchestrate profiles,
workloads, background workloads, hooks, metrics, snapshots, assertions, scans,
and verdicts into runs/<run-id>/.
USAGE
}

timestamp() {
  date -u +%Y%m%d_%H%M%S
}

iso_now() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

sanitize_id() {
  printf '%s' "$1" | tr '/ ' '__' | tr -cd '[:alnum:]_.-'
}

sha256_digest_file() {
  local file="${1:?file is required}"
  local digest

  if command -v shasum >/dev/null 2>&1; then
    digest="$(shasum -a 256 -- "$file" | awk '{print $1}')"
  elif command -v sha256sum >/dev/null 2>&1; then
    digest="$(sha256sum -- "$file" | awk '{print $1}')"
  else
    echo "A SHA-256 implementation (shasum or sha256sum) is required" >&2
    return 2
  fi
  if [[ ! "$digest" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Failed to calculate a canonical SHA-256 digest for: $file" >&2
    return 2
  fi
  printf 'sha256:%s\n' "$digest"
}

is_safe_utility_source_id() {
  local value="$1"
  local component
  local -a components=()

  [[ -n "$value" && ${#value} -le 200 ]] || return 1
  [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$ ]] || return 1
  IFS='/' read -r -a components <<< "$value"
  for component in "${components[@]}"; do
    [[ "$component" != "." && "$component" != ".." ]] || return 1
  done
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

run_pgworkbench() {
  if [[ -n "${PGWORKBENCH_BIN:-}" && -x "$PGWORKBENCH_BIN" ]]; then
    "$PGWORKBENCH_BIN" "$@"
    return
  fi

  if [[ -x "$REPO_DIR/pgworkbench" ]]; then
    "$REPO_DIR/pgworkbench" "$@"
    return
  fi

  if [[ -f "$REPO_DIR/go.mod" && -f "$REPO_DIR/cmd/pgworkbench/main.go" ]] && command -v go >/dev/null 2>&1; then
    (
      cd "$REPO_DIR"
      GOCACHE="${GOCACHE:-$REPO_DIR/.tmp/go-cache}" \
      GOMODCACHE="${GOMODCACHE:-$REPO_DIR/.tmp/go-mod-cache}" \
        go run ./cmd/pgworkbench "$@"
    )
    return
  fi

  if [[ -x "$REPO_DIR/generated/bin/pgworkbench" ]]; then
    "$REPO_DIR/generated/bin/pgworkbench" "$@"
    return
  fi

  if command -v pgworkbench >/dev/null 2>&1; then
    pgworkbench "$@"
    return
  fi

  return 127
}

selected_built_pgworkbench() {
  local candidate

  if [[ -n "${PGWORKBENCH_BIN:-}" && -x "$PGWORKBENCH_BIN" ]]; then
    printf '%s\n' "$PGWORKBENCH_BIN"
    return
  fi
  if [[ -x "$REPO_DIR/pgworkbench" ]]; then
    printf '%s\n' "$REPO_DIR/pgworkbench"
    return
  fi

  # The state writer below will use go run before any cached/generated binary.
  # Source execution has no immutable candidate identity, especially in a dirty
  # worktree, so leave it explicitly unverified.
  if [[ -f "$REPO_DIR/go.mod" && -f "$REPO_DIR/cmd/pgworkbench/main.go" ]] && command -v go >/dev/null 2>&1; then
    return 1
  fi

  if [[ -x "$REPO_DIR/generated/bin/pgworkbench" ]]; then
    printf '%s\n' "$REPO_DIR/generated/bin/pgworkbench"
    return
  fi
  candidate="$(command -v pgworkbench 2>/dev/null || true)"
  if [[ -n "$candidate" && -x "$candidate" ]]; then
    printf '%s\n' "$candidate"
    return
  fi
  return 1
}

resolve_engine_identity() {
  local binary output

  if [[ "${PGWORKBENCH_ENGINE_VERSION+x}" = x || "${PGWORKBENCH_ENGINE_COMMIT+x}" = x ]]; then
    PGWORKBENCH_ENGINE_VERSION="${PGWORKBENCH_ENGINE_VERSION:-unverified}"
    PGWORKBENCH_ENGINE_COMMIT="${PGWORKBENCH_ENGINE_COMMIT:-unverified}"
    export PGWORKBENCH_ENGINE_VERSION PGWORKBENCH_ENGINE_COMMIT
    return
  fi

  PGWORKBENCH_ENGINE_VERSION=unverified
  PGWORKBENCH_ENGINE_COMMIT=unverified
  if ! binary="$(selected_built_pgworkbench)"; then
    export PGWORKBENCH_ENGINE_VERSION PGWORKBENCH_ENGINE_COMMIT
    return
  fi
  if ! output="$("$binary" version 2>/dev/null)"; then
    export PGWORKBENCH_ENGINE_VERSION PGWORKBENCH_ENGINE_COMMIT
    return
  fi
  if [[ "$output" =~ ^pgworkbench[[:space:]]version=([^[:space:]]+)[[:space:]]commit=([^[:space:]]+)[[:space:]]built_at=([^[:space:]]+)$ ]]; then
    PGWORKBENCH_ENGINE_VERSION="${BASH_REMATCH[1]}"
    PGWORKBENCH_ENGINE_COMMIT="${BASH_REMATCH[2]}"
  fi
  export PGWORKBENCH_ENGINE_VERSION PGWORKBENCH_ENGINE_COMMIT
}

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  while IFS= read -r name; do
    case "$name" in
      PGWORKBENCH_EXACT_ENVIRONMENT)
        # Runner-owned and readonly; broad restore loops must never assign it.
        continue
        ;;
      ENV_FILE|COMPOSE|GOCACHE|GOMODCACHE|POSTGRES_*|PGBOUNCER_*|ALLOW_*|TOPOLOGY|TOPOLOGY_*|LOGICAL_REPLICATION_*|PG_CONFIG|PROFILE_*|DATASET_*|METRICS_*|WORKLOAD_*|PGBENCH_*|EXPERIMENT_*|PGWORKBENCH_*)
        PRESERVED_ENV_NAMES+=("$name")
        PRESERVED_ENV_VALUES+=("${!name}")
        ;;
    esac
  done < <(compgen -v)
}

restore_env_overrides() {
  local i

  for ((i = 0; i < ${#PRESERVED_ENV_NAMES[@]}; i++)); do
    export "${PRESERVED_ENV_NAMES[$i]}=${PRESERVED_ENV_VALUES[$i]}"
  done
}

activate_experiment_target_guard() {
  # This exported mode follows every experiment-owned subprocess. Generic
  # utility/workload commands keep their existing explicit external-target
  # contract when the marker is absent.
  export PGWORKBENCH_EXPERIMENT_MODE=1
  "$REPO_DIR/scripts/guard_local_pg.sh"

  # Materialize the complete disposable target contract before nested dataset
  # and workload specs are sourced. Their loaders restore these values in
  # experiment mode, including ports, so a nested spec cannot retarget a local
  # client to another server on the same host.
  export POSTGRES_HOST="${POSTGRES_HOST:-127.0.0.1}"
  export POSTGRES_PORT="${POSTGRES_PORT:-55433}"
  export POSTGRES_REPLICA_HOST="${POSTGRES_REPLICA_HOST:-127.0.0.1}"
  export POSTGRES_REPLICA_PORT="${POSTGRES_REPLICA_PORT:-55434}"
  export POSTGRES_LOGICAL_SUBSCRIBER_HOST="${POSTGRES_LOGICAL_SUBSCRIBER_HOST:-127.0.0.1}"
  export POSTGRES_LOGICAL_SUBSCRIBER_PORT="${POSTGRES_LOGICAL_SUBSCRIBER_PORT:-55435}"
  export POSTGRES_UPGRADE_OLD_HOST="${POSTGRES_UPGRADE_OLD_HOST:-127.0.0.1}"
  export POSTGRES_UPGRADE_OLD_PORT="${POSTGRES_UPGRADE_OLD_PORT:-55436}"
  export POSTGRES_UPGRADE_NEW_HOST="${POSTGRES_UPGRADE_NEW_HOST:-127.0.0.1}"
  export POSTGRES_UPGRADE_NEW_PORT="${POSTGRES_UPGRADE_NEW_PORT:-55437}"
  export PGBOUNCER_HOST="${PGBOUNCER_HOST:-127.0.0.1}"
  export PGBOUNCER_PORT="${PGBOUNCER_PORT:-56432}"
  export POSTGRES_DB="${POSTGRES_DB:-pg_experiment_workbench}"
  export POSTGRES_USER="${POSTGRES_USER:-postgres}"
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-postgres}"
  export ALLOW_NONLOCAL_PG=0
  export ALLOW_SYSTEM_DB=0

  # Explicit -h/-p arguments do not override every libpq environment route:
  # PGHOSTADDR and service files can still redirect the socket. Experiment
  # clients must derive their complete target only from the owned contract.
  unset PGHOSTADDR PGSERVICE PGSERVICEFILE PGPASSFILE PGOPTIONS \
    PGTARGETSESSIONATTRS PGSSLMODE PGSSLROOTCERT PGSSLCERT PGSSLKEY \
    PGREQUIRESSL PGREQUIREAUTH PGCHANNELBINDING

  # Container-only adapters must stay on the Compose network's owned primary.
  export WORKLOAD_PGHOST=postgres
  export WORKLOAD_PGPORT=5432
  export NOISIA_CONNINFO="host=postgres port=5432 dbname=$POSTGRES_DB user=$POSTGRES_USER password=$POSTGRES_PASSWORD sslmode=disable"
}

reject_experiment_target_overrides() {
  pgworkbench_reject_experiment_target_args
}

prepare_runs_root() {
  local canonical_repo canonical_runs
  local runs_root="$REPO_DIR/runs"

  canonical_repo="$(realpath "$REPO_DIR")"
  if [[ -L "$runs_root" ]]; then
    echo "Refusing symlinked experiment runs root: $runs_root" >&2
    return 2
  fi
  if [[ -e "$runs_root" && ! -d "$runs_root" ]]; then
    echo "Experiment runs root is not a directory: $runs_root" >&2
    return 2
  fi
  if [[ ! -e "$runs_root" ]]; then
    mkdir "$runs_root"
  fi
  if [[ -L "$runs_root" || ! -d "$runs_root" ]]; then
    echo "Refusing unsafe experiment runs root: $runs_root" >&2
    return 2
  fi

  canonical_runs="$(realpath "$runs_root")"
  if [[ "$canonical_runs" != "$canonical_repo/runs" ]]; then
    echo "Experiment runs root escaped the scenario pack: $canonical_runs" >&2
    return 2
  fi
  RUNS_ROOT="$canonical_runs"
}

list_specs() {
  find "$REPO_DIR/experiments" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    spec="${spec#"$REPO_DIR/experiments/"}"
    printf '%s\n' "${spec%.env}"
  done
}

validate_standard_spec_capability() {
  if [[ -n "${PGWORKBENCH_EXPERIMENT_SPEC_SCOPE:-}" ]]; then
    echo "Unsupported PGWORKBENCH_EXPERIMENT_SPEC_SCOPE: ${PGWORKBENCH_EXPERIMENT_SPEC_SCOPE}" >&2
    return 2
  fi
  if [[ -n "${PGWORKBENCH_DERIVED_EXPERIMENT_ID:-}" ||
        -n "${PGWORKBENCH_SOURCE_SPEC_KIND:-}" ||
        -n "${PGWORKBENCH_SOURCE_SPEC_ID:-}" ||
        -n "${PGWORKBENCH_SOURCE_SPEC_REF:-}" ||
        -n "${PGWORKBENCH_SOURCE_SPEC_DIGEST:-}" ]]; then
    if [[ -z "${PGWORKBENCH_DERIVED_EXPERIMENT_ID:-}" && "${PGWORKBENCH_SOURCE_SPEC_KIND:-}" = "benchmark" ]]; then
      validate_benchmark_source_spec "$(realpath "$REPO_DIR")"
      return
    fi
    echo "Source-spec provenance is only valid for an authorized utility-derived or benchmark experiment" >&2
    return 2
  fi
}

validate_benchmark_source_spec() {
  local pack_root="$1"
  local source_id="${PGWORKBENCH_SOURCE_SPEC_ID:-}"
  local expected_ref="benchmarks/$source_id.env"
  local source_ref="${PGWORKBENCH_SOURCE_SPEC_REF:-}"
  local source_file current component info_index actual_digest
  local -a components=()

  if ! is_safe_utility_source_id "$source_id"; then
    echo "Invalid benchmark source spec id: ${source_id:-<empty>}" >&2
    return 2
  fi
  if [[ "$source_ref" != "$expected_ref" ]]; then
    echo "Benchmark source spec ref must be $expected_ref" >&2
    return 2
  fi
  if [[ ! "${PGWORKBENCH_SOURCE_SPEC_DIGEST:-}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "Benchmark source spec digest must be canonical sha256" >&2
    return 2
  fi

  if benchmark_capsule_active; then
    source_file="$(benchmark_capsule_resolve "$expected_ref" "$PGWORKBENCH_SOURCE_SPEC_DIGEST")" || return
    return 0
  fi

  IFS='/' read -r -a components <<< "$source_ref"
  current="$pack_root"
  for ((info_index = 0; info_index < ${#components[@]}; info_index++)); do
    component="${components[$info_index]}"
    current="$current/$component"
    if [[ -L "$current" ]]; then
      echo "Benchmark source path must not contain symlinks: $current" >&2
      return 2
    fi
    if (( info_index + 1 < ${#components[@]} )); then
      if [[ ! -d "$current" ]]; then
        echo "Benchmark source path component is not a directory: $current" >&2
        return 2
      fi
    elif [[ ! -f "$current" ]]; then
      echo "Benchmark source spec is not a regular file: $current" >&2
      return 2
    fi
  done
  source_file="$(realpath "$current")"
  if [[ "$source_file" != "$pack_root/$expected_ref" ]]; then
    echo "Benchmark source spec escaped its canonical path: $source_file" >&2
    return 2
  fi
  actual_digest="$(sha256_digest_file "$source_file")"
  if [[ "$actual_digest" != "$PGWORKBENCH_SOURCE_SPEC_DIGEST" ]]; then
    echo "Benchmark source spec digest mismatch for $expected_ref" >&2
    return 2
  fi
}

validate_utility_source_spec() {
  local pack_root="$1"
  local source_id="${PGWORKBENCH_SOURCE_SPEC_ID:-}"
  local expected_ref="utility-tests/$source_id.env"
  local source_ref="${PGWORKBENCH_SOURCE_SPEC_REF:-}"
  local source_file current component info_index actual_digest
  local -a components=()

  if ! is_safe_utility_source_id "$source_id"; then
    echo "Invalid utility-derived source spec id: ${source_id:-<empty>}" >&2
    return 2
  fi
  if [[ "${PGWORKBENCH_SOURCE_SPEC_KIND:-}" != "utility-test" ]]; then
    echo "Utility-derived source spec kind must be utility-test" >&2
    return 2
  fi
  if [[ "$source_ref" != "$expected_ref" ]]; then
    echo "Utility-derived source spec ref must be $expected_ref" >&2
    return 2
  fi
  if [[ ! "${PGWORKBENCH_SOURCE_SPEC_DIGEST:-}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "Utility-derived source spec digest must be canonical sha256" >&2
    return 2
  fi

  IFS='/' read -r -a components <<< "$source_ref"
  current="$pack_root"
  for ((info_index = 0; info_index < ${#components[@]}; info_index++)); do
    component="${components[$info_index]}"
    current="$current/$component"
    if [[ -L "$current" ]]; then
      echo "Utility-derived source path must not contain symlinks: $current" >&2
      return 2
    fi
    if (( info_index + 1 < ${#components[@]} )); then
      if [[ ! -d "$current" ]]; then
        echo "Utility-derived source path component is not a directory: $current" >&2
        return 2
      fi
    elif [[ ! -f "$current" ]]; then
      echo "Utility-derived source spec is not a regular file: $current" >&2
      return 2
    fi
  done
  source_file="$(realpath "$current")"
  if [[ "$source_file" != "$pack_root/$expected_ref" ]]; then
    echo "Utility-derived source spec escaped its canonical path: $source_file" >&2
    return 2
  fi
  actual_digest="$(sha256_digest_file "$source_file")"
  if [[ "$actual_digest" != "$PGWORKBENCH_SOURCE_SPEC_DIGEST" ]]; then
    echo "Utility-derived source spec digest mismatch for $expected_ref" >&2
    return 2
  fi
}

canonical_utility_derived_spec() {
  local candidate="${1:?experiment spec candidate is required}"
  local pack_root generated_root resolved expected_id base

  pack_root="$(realpath "$REPO_DIR")"
  generated_root="$pack_root/.tmp/utility-tests"
  if [[ -L "$pack_root/.tmp" || -L "$generated_root" || ! -d "$generated_root" ]]; then
    echo "Refusing unsafe generated utility spec directory: $generated_root" >&2
    return 2
  fi
  if [[ -L "$candidate" || ! -f "$candidate" ]]; then
    echo "Generated utility experiment spec must be a regular non-symlink file: $candidate" >&2
    return 2
  fi
  resolved="$(realpath "$candidate")"
  if [[ "$(dirname "$resolved")" != "$generated_root" ]]; then
    echo "Generated utility experiment spec escaped $generated_root: $resolved" >&2
    return 2
  fi
  base="$(basename "$resolved")"
  if [[ "$base" != *.env || "$base" = ".env" ]]; then
    echo "Generated utility experiment spec must be a flat named .env file: $base" >&2
    return 2
  fi

  validate_utility_source_spec "$pack_root"
  expected_id="utility/${PGWORKBENCH_SOURCE_SPEC_ID}"
  if [[ "${PGWORKBENCH_DERIVED_EXPERIMENT_ID:-}" != "$expected_id" ]]; then
    echo "Utility-derived experiment id must be $expected_id" >&2
    return 2
  fi
  if [[ -n "${PGWORKBENCH_PACK_ID:-}" ||
        -n "${PGWORKBENCH_PACK_VERSION:-}" ||
        -n "${PGWORKBENCH_PACK_DIGEST:-}" ]]; then
    echo "Utility-derived experiment specs must not claim scenario-pack identity" >&2
    return 2
  fi
  printf '%s\n' "$resolved"
}

canonical_experiment_spec() {
  local candidate="${1:?experiment spec candidate is required}"
  local pack_root experiment_root resolved

  case "${PGWORKBENCH_EXPERIMENT_SPEC_SCOPE:-}" in
    utility-derived)
      canonical_utility_derived_spec "$candidate"
      return
      ;;
    "")
      validate_standard_spec_capability
      ;;
    *)
      echo "Unsupported PGWORKBENCH_EXPERIMENT_SPEC_SCOPE: ${PGWORKBENCH_EXPERIMENT_SPEC_SCOPE}" >&2
      return 2
      ;;
  esac

  if benchmark_capsule_active; then
    local expected_id="${PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_ID:-}"
    local expected_digest="${PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_DIGEST:-}"
    local expected_path
    if ! is_safe_utility_source_id "$expected_id"; then
      echo "Invalid benchmark capsule experiment spec id: ${expected_id:-<empty>}" >&2
      return 2
    fi
    expected_path="$(benchmark_capsule_resolve "experiments/$expected_id.env" "$expected_digest")" || return
    resolved="$(realpath "$candidate")"
    if [[ "$resolved" != "$expected_path" ]]; then
      echo "Benchmark execution must consume its exact experiment-spec snapshot" >&2
      return 2
    fi
    printf '%s\n' "$resolved"
    return 0
  fi

  pack_root="$(realpath "$REPO_DIR")"
  experiment_root="$pack_root/experiments"
  resolved="$(realpath "$candidate")"
  case "$resolved" in
    "$experiment_root"/*)
      printf '%s\n' "$resolved"
      ;;
    *)
      echo "Experiment spec resolves outside scenario pack experiments: $resolved" >&2
      return 2
      ;;
  esac
}

resolve_spec() {
  local input="${1:?experiment spec is required}"
  local candidate

  case "/$input/" in
    *'/../'*)
      echo "Experiment spec path must not contain parent traversal: $input" >&2
      return 2
      ;;
  esac

  if [[ -f "$input" ]]; then
    canonical_experiment_spec "$input"
    return 0
  fi

  candidate="$REPO_DIR/$input"
  if [[ -f "$candidate" ]]; then
    canonical_experiment_spec "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/experiments/$input"
  if [[ -f "$candidate" ]]; then
    canonical_experiment_spec "$candidate"
    return 0
  fi

  candidate="$REPO_DIR/experiments/$input.env"
  if [[ -f "$candidate" ]]; then
    canonical_experiment_spec "$candidate"
    return 0
  fi

  mapfile -t matches < <(find "$REPO_DIR/experiments" -type f -name '*.env' 2>/dev/null | sort | while read -r spec; do
    local id="${spec#"$REPO_DIR/experiments/"}"
    id="${id%.env}"
    if [[ "$id" = "$input" || "$(basename "$id")" = "$input" ]]; then
      printf '%s\n' "$spec"
    fi
  done)

  if (( ${#matches[@]} == 1 )); then
    canonical_experiment_spec "${matches[0]}"
    return 0
  fi

  if (( ${#matches[@]} > 1 )); then
    echo "Ambiguous experiment spec: $input" >&2
    printf '  %s\n' "${matches[@]#"$REPO_DIR/experiments/"}" >&2
    exit 2
  fi

  echo "Experiment spec not found: $input" >&2
  exit 1
}

load_repo_env() {
  local env_file="${ENV_FILE:-}"

  if [[ -z "$env_file" ]]; then
    if [[ -f "$REPO_DIR/.env" ]]; then
      env_file="$REPO_DIR/.env"
    else
      env_file="$REPO_DIR/.env.example"
    fi
  elif [[ "$env_file" != /* ]]; then
    env_file="$REPO_DIR/$env_file"
  fi

  ENV_PATH="$env_file"
  if [[ -f "$ENV_PATH" ]] && ! pgworkbench_exact_environment_active; then
    capture_env_overrides
    set -a
    # shellcheck disable=SC1090
    source "$ENV_PATH"
    set +a
    restore_env_overrides
  fi
}

load_spec() {
  local desired_spec_id desired_spec_ref logical_spec_file execution_spec_file
  local expected_spec_digest digest_before_source digest_after_source

  logical_spec_file="$(resolve_spec "$1")"
  EXPERIMENT_SPEC_FILE="$logical_spec_file"
  case "${PGWORKBENCH_EXPERIMENT_SPEC_SCOPE:-}" in
    utility-derived)
      desired_spec_id="$PGWORKBENCH_DERIVED_EXPERIMENT_ID"
      desired_spec_ref=".tmp/utility-tests/$(basename "$logical_spec_file")"
      ;;
    "")
      if benchmark_capsule_active; then
        desired_spec_id="${PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_ID:-}"
      else
        desired_spec_id="${logical_spec_file#"$(realpath "$REPO_DIR")/experiments/"}"
        desired_spec_id="${desired_spec_id%.env}"
      fi
      desired_spec_ref="experiments/$desired_spec_id.env"
      ;;
    *)
      echo "Unsupported PGWORKBENCH_EXPERIMENT_SPEC_SCOPE: ${PGWORKBENCH_EXPERIMENT_SPEC_SCOPE}" >&2
      return 2
      ;;
  esac

  execution_spec_file="${PGWORKBENCH_EXECUTION_SPEC_FILE:-$logical_spec_file}"
  if [[ "$execution_spec_file" != /* || -L "$execution_spec_file" || ! -f "$execution_spec_file" ]]; then
    echo "Runner-selected experiment spec snapshot must be an absolute regular non-symlink file" >&2
    return 2
  fi
  digest_before_source="$(sha256_digest_file "$execution_spec_file")"
  expected_spec_digest="${EXPERIMENT_SPEC_SHA256:-}"
  if [[ -n "${PGWORKBENCH_EXECUTION_SPEC_FILE:-}" && -z "$expected_spec_digest" ]]; then
    echo "Runner-selected experiment spec snapshot requires its expected digest" >&2
    return 2
  fi
  if [[ -n "$expected_spec_digest" ]]; then
    if [[ "$expected_spec_digest" =~ ^[0-9a-f]{64}$ ]]; then
      expected_spec_digest="sha256:$expected_spec_digest"
    elif [[ ! "$expected_spec_digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
      echo "Runner-selected experiment spec digest is not canonical" >&2
      return 2
    fi
    if [[ "$digest_before_source" != "$expected_spec_digest" ]]; then
      echo "Runner-selected experiment spec snapshot digest mismatch" >&2
      return 2
    fi
  fi

  capture_env_overrides
  set -a
  # shellcheck disable=SC1090
  source "$execution_spec_file"
  set +a
  restore_env_overrides

  digest_after_source="$(sha256_digest_file "$execution_spec_file")"
  if [[ "$digest_after_source" != "$digest_before_source" ||
        ( -n "$expected_spec_digest" && "$digest_after_source" != "$expected_spec_digest" ) ]]; then
    echo "Runner-selected experiment spec snapshot changed while it was sourced" >&2
    return 2
  fi

  if benchmark_capsule_active; then
    # The env spec is executable shell. Re-check the immutable capability after
    # sourcing so a spec that rewrites itself cannot pass the pre-source digest
    # check and then leave different bytes for provenance or later consumers.
    benchmark_capsule_resolve \
      "experiments/$desired_spec_id.env" \
      "${PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_DIGEST:-}" >/dev/null
  fi

  case "${PGWORKBENCH_EXPERIMENT_SPEC_SCOPE:-}" in
    utility-derived)
      canonical_utility_derived_spec "$logical_spec_file" >/dev/null
      ;;
    "")
      validate_standard_spec_capability
      ;;
  esac
  EXPERIMENT_SPEC_ID="$desired_spec_id"
  EXPERIMENT_SPEC_REF="$desired_spec_ref"
  EXPERIMENT_SPEC_ORIGIN_FILE="$logical_spec_file"
  EXPERIMENT_SPEC_FILE="$execution_spec_file"
  EXPERIMENT_SPEC_SHA256="$digest_before_source"
  unset EXPERIMENT_SPEC_DIGEST
  export EXPERIMENT_SPEC_FILE EXPERIMENT_SPEC_ORIGIN_FILE EXPERIMENT_SPEC_ID EXPERIMENT_SPEC_REF EXPERIMENT_SPEC_SHA256
}

write_manifest_shell() {
	echo "EXPERIMENT_STATE_WRITER=shell is legacy and cannot write the v1 evidence contract; use go" >&2
	return 2
}

write_manifest_go() {
  local spec_id="$EXPERIMENT_SPEC_ID"
  local spec_ref="${EXPERIMENT_SPEC_REF:-experiments/$EXPERIMENT_SPEC_ID.env}"
  local run_dir="$RUN_DIR"

  RUN_ID="$RUN_ID" \
  STARTED_AT="$STARTED_AT" \
  EXPERIMENT_SPEC_FILE="${EXPERIMENT_SPEC_ORIGIN_FILE:-$EXPERIMENT_SPEC_FILE}" \
	EXPERIMENT_SPEC_ID="$spec_id" \
	EXPERIMENT_SPEC_REF="$spec_ref" \
	EXPERIMENT_SPEC_SHA256="${EXPERIMENT_SPEC_SHA256:-}" \
  EXPERIMENT_NAME="${EXPERIMENT_NAME:-}" \
  EXPERIMENT_TOPOLOGY="${EXPERIMENT_TOPOLOGY:-}" \
  EXPERIMENT_PG_CONFIG="${EXPERIMENT_PG_CONFIG:-}" \
  PG_CONFIG="${PG_CONFIG:-}" \
  EXPERIMENT_PROFILE="${EXPERIMENT_PROFILE:-}" \
  EXPERIMENT_DATASET_SPEC="${EXPERIMENT_DATASET_SPEC:-}" \
  EXPERIMENT_PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-}" \
  PROFILE_SIZE="${PROFILE_SIZE:-}" \
  EXPERIMENT_WORKLOAD_SPEC="${EXPERIMENT_WORKLOAD_SPEC:-}" \
  EXPERIMENT_BACKGROUND_SPECS="${EXPERIMENT_BACKGROUND_SPECS:-}" \
	EXPERIMENT_METRICS="${EXPERIMENT_METRICS:-1}" \
	PGWORKBENCH_RUNTIME="${PGWORKBENCH_RUNTIME:-docker}" \
	PGWORKBENCH_ENGINE_VERSION="${PGWORKBENCH_ENGINE_VERSION:-unverified}" \
	PGWORKBENCH_ENGINE_COMMIT="${PGWORKBENCH_ENGINE_COMMIT:-unverified}" \
	PGWORKBENCH_PACK_ID="${PGWORKBENCH_PACK_ID:-}" \
	PGWORKBENCH_PACK_VERSION="${PGWORKBENCH_PACK_VERSION:-}" \
	PGWORKBENCH_PACK_DIGEST="${PGWORKBENCH_PACK_DIGEST:-}" \
	PGWORKBENCH_SOURCE_SPEC_KIND="${PGWORKBENCH_SOURCE_SPEC_KIND:-}" \
	PGWORKBENCH_SOURCE_SPEC_ID="${PGWORKBENCH_SOURCE_SPEC_ID:-}" \
	PGWORKBENCH_SOURCE_SPEC_REF="${PGWORKBENCH_SOURCE_SPEC_REF:-}" \
	PGWORKBENCH_SOURCE_SPEC_DIGEST="${PGWORKBENCH_SOURCE_SPEC_DIGEST:-}" \
	PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS="${PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS:-unavailable}" \
	PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET="${PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET:-primary}" \
	PGWORKBENCH_RUNTIME_OS="${PGWORKBENCH_RUNTIME_OS:-}" \
	PGWORKBENCH_RUNTIME_ARCH="${PGWORKBENCH_RUNTIME_ARCH:-}" \
	PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM="${PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM:-}" \
	PGWORKBENCH_POSTGRES_SERVER_MAJOR="${PGWORKBENCH_POSTGRES_SERVER_MAJOR:-}" \
	PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT="${PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT:-}" \
	REPO_DIR="$REPO_DIR" \
	RUN_DIR="$run_dir" \
    run_pgworkbench run write-manifest --run-dir "$run_dir"
}

write_manifest() {
  case "${EXPERIMENT_STATE_WRITER:-go}" in
    go|auto)
      write_manifest_go
      ;;
    shell)
      write_manifest_shell
      ;;
    *)
      echo "Unsupported EXPERIMENT_STATE_WRITER: ${EXPERIMENT_STATE_WRITER:-}" >&2
      exit 2
      ;;
  esac
}

# Publish the smallest independently verifiable failed-run envelope before any
# benchmark-owned preflight input is sourced. It is replaced by the complete
# manifest only after the repository/spec guards have passed. Keeping metrics
# and source provenance disabled here is deliberate: neither exists yet, and a
# preflight failure must not claim evidence that was never collected.
write_benchmark_preflight_manifest() {
  local runtime="${PGWORKBENCH_RUNTIME:-docker}"
  local spec_file="${EXPERIMENT_SPEC_FILE:-}"
  local spec_id="${EXPERIMENT_SPEC_ID:-benchmark-preflight}"
  local spec_ref="${EXPERIMENT_SPEC_REF:-experiments/benchmark-preflight.env}"
  local spec_digest="${EXPERIMENT_SPEC_SHA256:-}"

  case "$runtime" in
    docker|native) ;;
    *) runtime=docker ;;
  esac
  if [[ ! "$spec_digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    spec_digest=""
  fi

  EXPERIMENT_SPEC_FILE="$spec_file" \
  EXPERIMENT_SPEC_ID="$spec_id" \
  EXPERIMENT_SPEC_REF="$spec_ref" \
  EXPERIMENT_SPEC_SHA256="$spec_digest" \
  EXPERIMENT_IDENTITY_DIGEST='' \
  EXPERIMENT_NAME="benchmark preflight" \
  EXPERIMENT_TOPOLOGY=single \
  EXPERIMENT_PG_CONFIG=default \
  PG_CONFIG=default \
  EXPERIMENT_PROFILE='' \
  EXPERIMENT_DATASET_SPEC='' \
  EXPERIMENT_PROFILE_SIZE=small \
  PROFILE_SIZE=small \
  EXPERIMENT_WORKLOAD_SPEC='' \
  EXPERIMENT_BACKGROUND_SPECS='' \
  EXPERIMENT_METRICS=0 \
  EXPERIMENT_METRICS_SAMPLES='' \
  PGWORKBENCH_RUNTIME="$runtime" \
  PGWORKBENCH_PACK_ID='' \
  PGWORKBENCH_PACK_VERSION='' \
  PGWORKBENCH_PACK_DIGEST='' \
  PGWORKBENCH_SOURCE_SPEC_KIND='' \
  PGWORKBENCH_SOURCE_SPEC_ID='' \
  PGWORKBENCH_SOURCE_SPEC_REF='' \
  PGWORKBENCH_SOURCE_SPEC_DIGEST='' \
  PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS=unavailable \
  PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET=primary \
  PGWORKBENCH_RUNTIME_OS='' \
  PGWORKBENCH_RUNTIME_ARCH='' \
  PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM='' \
  PGWORKBENCH_POSTGRES_SERVER_MAJOR='' \
  PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT='' \
    write_manifest_go
}

validate_state_writer() {
  case "${EXPERIMENT_STATE_WRITER:-go}" in
    go|auto)
      EXPERIMENT_STATE_WRITER=go
      export EXPERIMENT_STATE_WRITER
      ;;
    shell)
      echo "EXPERIMENT_STATE_WRITER=shell is legacy and cannot write the v1 evidence contract; use go" >&2
      return 2
      ;;
    *)
      echo "Unsupported EXPERIMENT_STATE_WRITER: ${EXPERIMENT_STATE_WRITER:-}" >&2
      return 2
      ;;
  esac
}

run_psql_file_list() {
  local files="$1"
  local file
  local status=0

  for file in $files; do
    [[ -z "$file" ]] && continue
    if [[ "$file" != /* ]]; then
      file="$REPO_DIR/$file"
    fi
    "$REPO_DIR/scripts/psql.sh" -f "$file" || status="$?"
  done

  return "$status"
}

run_inline_sql() {
  local sql="$1"
  [[ -z "$sql" ]] && return 0
  "$REPO_DIR/scripts/psql.sh" -c "$sql"
}

run_true_sql_assertion() {
  local sql="$1"
  local output
  [[ -z "$sql" ]] && return 0

  output="$("$REPO_DIR/scripts/psql.sh" -Atq -c "$sql")" || return "$?"
  if [[ "$output" != "t" ]]; then
    echo "Boolean SQL assertion must return exactly one true row; got: ${output:-<empty>}" >&2
    return 1
  fi
}

validate_shell_hook_trust() {
  local trusted="${EXPERIMENT_TRUSTED_SHELL:-0}"
  local -a hooks=()
  local hook_list

  case "$trusted" in
    0|1) ;;
    *)
      echo "EXPERIMENT_TRUSTED_SHELL must be 0 or 1: $trusted" >&2
      return 2
      ;;
  esac

  [[ -n "${EXPERIMENT_BEFORE_SHELL:-}" ]] && hooks+=(EXPERIMENT_BEFORE_SHELL)
  [[ -n "${EXPERIMENT_AFTER_SHELL:-}" ]] && hooks+=(EXPERIMENT_AFTER_SHELL)
  [[ -n "${EXPERIMENT_ASSERT_SHELL:-}" ]] && hooks+=(EXPERIMENT_ASSERT_SHELL)
  (( ${#hooks[@]} == 0 )) && return 0

  hook_list="$(IFS=,; printf '%s' "${hooks[*]}")"
  if [[ "$trusted" != "1" ]]; then
    echo "Host-shell hooks require EXPERIMENT_TRUSTED_SHELL=1: $hook_list" >&2
    return 2
  fi

  printf 'trusted_shell_hooks=%s\n' "$hook_list"
}

run_shell_hook() {
  local field="$1"
  local command="$2"
  [[ -z "$command" ]] && return 0
  if [[ "${EXPERIMENT_TRUSTED_SHELL:-0}" != "1" ]]; then
    echo "$field requires EXPERIMENT_TRUSTED_SHELL=1" >&2
    return 2
  fi
  printf 'trusted_shell_hook=%s\n' "$field"
  export REPO_DIR RUN_ID RUN_DIR EXPERIMENT_SPEC_FILE EXPERIMENT_SPEC_ID
  BASH_ENV=/dev/null bash --noprofile --norc -c "$command"
}

run_assertions() {
  local status=0

  run_psql_file_list "${EXPERIMENT_ASSERT_SQL_FILES:-}" || status="$?"
  run_inline_sql "${EXPERIMENT_ASSERT_SQL:-}" || status="$?"
  run_true_sql_assertion "${EXPERIMENT_ASSERT_TRUE_SQL:-}" || status="$?"
  run_shell_hook EXPERIMENT_ASSERT_SHELL "${EXPERIMENT_ASSERT_SHELL:-}" || status="$?"

  return "$status"
}

capture_evidence_files() {
  local relative source destination current component index
  local -a components=()

  for relative in ${EXPERIMENT_CAPTURE_FILES:-}; do
    if [[ "$relative" = /* || "$relative" == *\\* || "$relative" = */ ]]; then
      echo "Captured evidence path must be portable and repository-relative: $relative" >&2
      return 2
    fi
    case "$relative" in
      logs/utility/*|.tmp/utility-output/*) ;;
      *)
        echo "Captured evidence path must be under logs/utility/ or .tmp/utility-output/: $relative" >&2
        return 2
        ;;
    esac
    IFS='/' read -r -a components <<< "$relative"
    current="$REPO_DIR"
    for ((index = 0; index < ${#components[@]}; index++)); do
      component="${components[$index]}"
      if [[ -z "$component" || "$component" = "." || "$component" = ".." || ! "$component" =~ ^[A-Za-z0-9._-]+$ ]]; then
        echo "Captured evidence path is not portable: $relative" >&2
        return 2
      fi
      current="$current/$component"
      if [[ -L "$current" ]]; then
        echo "Captured evidence path must not contain symlinks: $current" >&2
        return 2
      fi
    done
    source="$current"
    if [[ ! -f "$source" || ! -s "$source" ]]; then
      echo "Captured evidence file is missing or empty: $relative" >&2
      return 1
    fi
    destination="$RUN_DIR/artifacts/utility/$relative"
    mkdir -p "$(dirname "$destination")"
    if [[ -e "$destination" || -L "$destination" ]]; then
      echo "Refusing to overwrite captured evidence: $destination" >&2
      return 2
    fi
    cp -p -- "$source" "$destination"
  done
}

capture_spec_provenance() {
  local source destination actual_digest source_kind source_snapshot
  destination="$RUN_DIR/artifacts/provenance/experiment-spec.env"
  mkdir -p "$(dirname "$destination")"
  cp -p -- "$EXPERIMENT_SPEC_FILE" "$destination"
  actual_digest="$(sha256_digest_file "$destination")"
  if [[ "$actual_digest" != "$EXPERIMENT_SPEC_SHA256" ]]; then
    echo "Captured experiment spec digest changed during execution" >&2
    return 1
  fi
  if [[ -n "${PGWORKBENCH_SOURCE_SPEC_REF:-}" ]]; then
    source_kind="${PGWORKBENCH_SOURCE_SPEC_KIND:-}"
    case "$source_kind" in
      utility-test|benchmark)
        ;;
      *)
        echo "Unsupported source spec kind for provenance: $source_kind" >&2
        return 2
        ;;
    esac
    if benchmark_capsule_active && [[ "$source_kind" = "benchmark" ]]; then
      source="$(benchmark_capsule_resolve "$PGWORKBENCH_SOURCE_SPEC_REF" "$PGWORKBENCH_SOURCE_SPEC_DIGEST")" || return
    else
      source="$REPO_DIR/$PGWORKBENCH_SOURCE_SPEC_REF"
    fi
    source_snapshot="$RUN_DIR/artifacts/provenance/source-$source_kind.env"
    cp -p -- "$source" "$source_snapshot"
    actual_digest="$(sha256_digest_file "$source_snapshot")"
    if [[ "$actual_digest" != "$PGWORKBENCH_SOURCE_SPEC_DIGEST" ]]; then
      echo "Captured source spec digest changed during execution" >&2
      return 1
    fi
  fi
}

snapshot() {
  local label="$1"

  if [[ "${EXPERIMENT_SNAPSHOT:-1}" = "1" ]]; then
    "$REPO_DIR/scripts/snapshot_pg.sh" "$RUN_DIR/snapshots/$label"
  fi
}

start_metrics() {
  local ready_status=0
  if [[ "${EXPERIMENT_METRICS:-1}" != "1" ]]; then
    return 0
  fi

  METRICS_STOP_FILE="$RUN_DIR/.metrics-supervisor.stop"
  METRICS_READY_FILE="$RUN_DIR/.metrics-ready"
  if [[ -e "$METRICS_STOP_FILE" || -L "$METRICS_STOP_FILE" ||
        -e "$METRICS_READY_FILE" || -L "$METRICS_READY_FILE" ]]; then
    echo "Refusing pre-existing metrics lifecycle state in $RUN_DIR" >&2
    return 2
  fi

  if benchmark_control_v2_active; then
    local -a sampler_args=(
      --run-dir "$RUN_DIR"
      --interval-seconds "${EXPERIMENT_METRICS_INTERVAL:-${METRICS_INTERVAL:-1}}"
    )
    if [[ -z "${PGWORKBENCH_BIN:-}" || ! -x "$PGWORKBENCH_BIN" ]]; then
      echo "Benchmark contract v2 requires the exact executable pgworkbench sampler" >&2
      return 2
    fi
    if [[ -n "${EXPERIMENT_METRICS_SAMPLES:-${METRICS_SAMPLES:-}}" ]]; then
      sampler_args+=(--samples "${EXPERIMENT_METRICS_SAMPLES:-${METRICS_SAMPLES:-}}")
    else
      sampler_args+=(--duration-seconds "${EXPERIMENT_METRICS_DURATION:-${METRICS_DURATION:-30}}")
    fi
    pgworkbench_start_owned_process METRICS_PID "$METRICS_STOP_FILE" "$RUN_DIR/metrics.log" \
      "$PGWORKBENCH_BIN" benchmark sample-metrics-v2 "${sampler_args[@]}"
  else
    pgworkbench_start_owned_process METRICS_PID "$METRICS_STOP_FILE" "$RUN_DIR/metrics.log" \
      env \
        "METRICS_INTERVAL=${EXPERIMENT_METRICS_INTERVAL:-${METRICS_INTERVAL:-1}}" \
        "METRICS_DURATION=${EXPERIMENT_METRICS_DURATION:-${METRICS_DURATION:-30}}" \
        "METRICS_SAMPLES=${EXPERIMENT_METRICS_SAMPLES:-${METRICS_SAMPLES:-}}" \
        "METRICS_OUT=$RUN_DIR/metrics.csv" \
        "METRICS_READY_FILE=$METRICS_READY_FILE" \
        "$REPO_DIR/scripts/sample_metrics.sh"
  fi
  pgworkbench_wait_for_metrics_ready \
    "$METRICS_PID" "$RUN_DIR/metrics.csv" "$METRICS_READY_FILE" "$METRICS_STOP_FILE" || ready_status="$?"
  if [[ "$ready_status" != "0" ]]; then
    METRICS_EXIT="$ready_status"
    if ! pgworkbench_owned_process_running "$METRICS_PID"; then
      METRICS_PID=""
    fi
    # A header or partial row is not evidence. Failed runs deliberately permit
    # an absent metrics.csv, while retaining metrics.log for diagnosis.
    if [[ -f "$RUN_DIR/metrics.csv" && ! -L "$RUN_DIR/metrics.csv" ]]; then
      rm -- "$RUN_DIR/metrics.csv"
    fi
    return "$ready_status"
  fi
}

stop_metrics() {
  local samples="${EXPERIMENT_METRICS_SAMPLES:-${METRICS_SAMPLES:-}}"
  local status=0
  local stop_requested=0
  if [[ -n "${METRICS_PID:-}" ]]; then
    # A bounded sampler is evidence-producing foreground work: let it finish
    # and preserve its real exit status. Duration-based sampling is stopped
    # when the experiment completes because it intentionally has no fixed
    # sample count.
    if [[ -d "${METRICS_STOP_FILE:-}" && ! -L "${METRICS_STOP_FILE:-}" ]]; then
      stop_requested=1
    elif [[ -z "$samples" || "${PGWORKBENCH_TERMINATING:-0}" = "1" ]]; then
      pgworkbench_request_owned_stop "$METRICS_STOP_FILE"
      stop_requested=1
    fi
    if [[ "$stop_requested" = "1" ]]; then
      pgworkbench_wait_after_stop_request "$METRICS_PID" "$METRICS_STOP_FILE" || status="$?"
    else
      # A fixed-sample metrics collector is declared evidence work and may
      # legitimately outlive the foreground workload. The outer execution
      # deadline still bounds this wait.
      pgworkbench_wait_owned_process "$METRICS_PID" "$METRICS_STOP_FILE" || status="$?"
    fi
    # Both owned samplers handle cooperative SIGTERM, publish a final boundary
    # sample, and exit zero. A raw 143 means termination won the startup race or
    # the handler failed, so it must never be normalized to success.
    if [[ "$status" != "0" ]]; then
      METRICS_EXIT="$status"
    fi
    if pgworkbench_owned_process_running "$METRICS_PID"; then
      return "$status"
    fi
    METRICS_PID=""
  fi
  return "$status"
}

wait_background_specs() {
  local index pid stop_file status
  for ((index = 0; index < ${#BACKGROUND_PIDS[@]}; index++)); do
    pid="${BACKGROUND_PIDS[$index]}"
    stop_file="${BACKGROUND_STOP_FILES[$index]}"
    status=0
    # The outer Go execution deadline bounds this intentional wait.
    # Do not reuse cleanup grace here: a declared background workload may be
    # longer than cleanup itself.
    pgworkbench_wait_owned_process "$pid" "$stop_file" || status="$?"
    if [[ "$status" != "0" ]]; then
      BACKGROUND_EXIT="$status"
    fi
  done
  BACKGROUND_PIDS=()
  BACKGROUND_STOP_FILES=()
}

start_background_specs() {
  local specs="${EXPERIMENT_BACKGROUND_SPECS:-}"
  local spec safe log stop_file background_pid index=0
  mkdir -p "$RUN_DIR/background"

  for spec in $specs; do
    index=$((index + 1))
    safe="$(sanitize_id "$spec")"
    log="$RUN_DIR/background/$safe.log"
    stop_file="$RUN_DIR/background/.supervisor-$index.stop"
    background_pid=""
    pgworkbench_start_owned_process background_pid "$stop_file" "$log" \
      env \
        WORKLOAD_RUN_LOG=0 \
        "WORKLOAD_LOG_DIR=$RUN_DIR/background" \
        PGWORKBENCH_BENCHMARK_PHASE_FILE='' \
        "PROFILE_SIZE=${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
        "PROFILE_SECONDS=${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
        "$REPO_DIR/scripts/run_workload.sh" run "$spec"
    BACKGROUND_PIDS+=("$background_pid")
    BACKGROUND_STOP_FILES+=("$stop_file")
    BACKGROUND_LOGS+=("$log")
  done

  if [[ -n "$specs" && "${EXPERIMENT_BACKGROUND_WARMUP:-0}" != "0" ]]; then
    sleep "$EXPERIMENT_BACKGROUND_WARMUP"
  fi
}

stop_background_specs() {
  local index pid stop_file status
  local overall_status=0
  local -a remaining_pids=()
  local -a remaining_stop_files=()
  for stop_file in "${BACKGROUND_STOP_FILES[@]}"; do
    pgworkbench_request_owned_stop "$stop_file"
  done

  for ((index = 0; index < ${#BACKGROUND_PIDS[@]}; index++)); do
    pid="${BACKGROUND_PIDS[$index]}"
    stop_file="${BACKGROUND_STOP_FILES[$index]}"
    status=0
    pgworkbench_wait_after_stop_request "$pid" "$stop_file" || status="$?"
    if pgworkbench_owned_process_running "$pid"; then
      remaining_pids+=("$pid")
      remaining_stop_files+=("$stop_file")
    fi
    # The supervisor normalizes only a SIGTERM it successfully delivered after
    # observing this token. Any raw 143 here is an independent lifecycle failure.
    if [[ "$status" != "0" ]]; then
      BACKGROUND_EXIT="$status"
      overall_status="$status"
    fi
  done
  BACKGROUND_PIDS=("${remaining_pids[@]}")
  BACKGROUND_STOP_FILES=("${remaining_stop_files[@]}")
  return "$overall_status"
}

begin_owned_children_stop() {
  local force_metrics="${1:-0}"
  local stop_file

  pgworkbench_begin_owned_cleanup
  if [[ -n "${METRICS_PID:-}" &&
        ( "$force_metrics" = "1" || -z "${EXPERIMENT_METRICS_SAMPLES:-${METRICS_SAMPLES:-}}" ) ]]; then
    pgworkbench_request_owned_stop "$METRICS_STOP_FILE"
  fi
  for stop_file in "${BACKGROUND_STOP_FILES[@]}"; do
    pgworkbench_request_owned_stop "$stop_file"
  done
}

verify_finalized_manifest() {
  local current_digest
  if [[ -z "${MANIFEST_FINALIZED_DIGEST:-}" ]]; then
    echo "Finalized manifest digest is unavailable" >&2
    return 1
  fi
  if [[ ! -f "$RUN_DIR/manifest.env" || -L "$RUN_DIR/manifest.env" ]]; then
    echo "Finalized manifest is missing or not a regular owned file" >&2
    return 1
  fi
  current_digest="$(sha256_digest_file "$RUN_DIR/manifest.env")"
  if [[ "$current_digest" != "$MANIFEST_FINALIZED_DIGEST" ]]; then
    echo "Finalized manifest changed during experiment execution" >&2
    return 1
  fi
}

write_verdict_shell() {
	echo "EXPERIMENT_STATE_WRITER=shell is legacy and cannot write the v1 evidence contract; use go" >&2
	return 2
}

write_verdict_go() {
  local status="$1"
  local message="$2"
  local finished_at
  local run_dir="$RUN_DIR"
  if [[ -n "${VERDICT_FINISHED_AT:-}" ]]; then
    finished_at="$VERDICT_FINISHED_AT"
  elif [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" ]]; then
    finished_at="$(benchmark_phase_now)"
  else
    finished_at="$(iso_now)"
  fi

  RUN_ID="$RUN_ID" \
  STARTED_AT="$STARTED_AT" \
  EXPERIMENT_SPEC_ID="$EXPERIMENT_SPEC_ID" \
  RUN_DIR="$run_dir" \
  WORKLOAD_EXIT="${WORKLOAD_EXIT:-0}" \
  ASSERT_EXIT="${ASSERT_EXIT:-0}" \
  SCAN_EXIT="${SCAN_EXIT:-0}" \
    run_pgworkbench run write-verdict \
      --run-dir "$run_dir" \
      --status "$status" \
      --message "$message" \
      --finished-at "$finished_at"
}

write_verdict() {
  case "${EXPERIMENT_STATE_WRITER:-go}" in
    go|auto)
      write_verdict_go "$@"
      ;;
    shell)
      write_verdict_shell "$@"
      ;;
    *)
      echo "Unsupported EXPERIMENT_STATE_WRITER: ${EXPERIMENT_STATE_WRITER:-}" >&2
      exit 2
      ;;
  esac
}

write_terminal_failed_verdict() {
  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" ]]; then
    # The preflight failure may be the rejected state-writer setting itself.
    # Use the same Go writer that published the minimal manifest instead of
    # delegating to an already rejected EXPERIMENT_STATE_WRITER value.
    write_verdict_go failed "$1"
  else
    write_verdict failed "$1"
  fi
}

materialize_benchmark_controls_v2() {
  benchmark_control_v2_active || return 0
  if [[ -z "${PGWORKBENCH_BIN:-}" || ! -x "$PGWORKBENCH_BIN" || -z "${RUN_DIR:-}" ]]; then
    echo "Benchmark contract v2 requires the exact executable control materializer" >&2
    return 2
  fi
  "$PGWORKBENCH_BIN" benchmark materialize-controls-v2 --run-dir "$RUN_DIR" >/dev/null
}

write_intermediate_failed_verdict() {
  local reason="${1:?failed verdict reason is required}"
  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" ]]; then
    # The terminal handler still owns cleanup and the final lifecycle-bound
    # control materialization. Publishing a verdict before those gates would
    # make a transient, incomplete run look immutable.
    return 0
  fi
  write_verdict failed "$reason"
  VERDICT_WRITTEN=1
}

terminal_cleanup() {
  local exit_code="$?"
  local cleanup_started cleanup_finished cleanup_status="passed" cleanup_reason="" cleanup_exit=0 phase_status=0 controls_exit=0 terminal_failure=0 preflight_passed=0
  local phase_run_id phase_trial phase_sequence phase_name phase_result
  trap - EXIT
  trap - HUP INT TERM
  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" ]]; then
    PGWORKBENCH_BENCHMARK_PHASE_FILE="$BENCHMARK_PREFLIGHT_PHASE_FILE"
    PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE="${BENCHMARK_PREFLIGHT_PHASE_MIRROR_FILE:-}"
    PGWORKBENCH_BENCHMARK_RUN_ID="$BENCHMARK_PREFLIGHT_RUN_ID"
    PGWORKBENCH_BENCHMARK_TRIAL="$BENCHMARK_PREFLIGHT_TRIAL"
    RUN_ID="$BENCHMARK_PREFLIGHT_RUN_ID"
    STARTED_AT="$BENCHMARK_PREFLIGHT_STARTED_AT"
    PGWORKBENCH_BIN="$BENCHMARK_PREFLIGHT_PGWORKBENCH_BIN"
    if [[ -n "${BENCHMARK_PREFLIGHT_OWNED_RUN_DIR:-}" ]]; then
      RUN_DIR="$BENCHMARK_PREFLIGHT_OWNED_RUN_DIR"
      RUN_DIRECTORY_OWNED=1
    else
      RUN_DIR=""
      RUN_DIRECTORY_OWNED=0
    fi
    if [[ -s "$PGWORKBENCH_BENCHMARK_PHASE_FILE" ]] &&
       IFS=$'\t' read -r phase_run_id phase_trial phase_sequence phase_name phase_result _ < "$PGWORKBENCH_BENCHMARK_PHASE_FILE" &&
       [[ "$phase_run_id" = "$BENCHMARK_PREFLIGHT_RUN_ID" && "$phase_trial" = "$BENCHMARK_PREFLIGHT_TRIAL" &&
          "$phase_sequence" = "1" && "$phase_name" = "preflight" && "$phase_result" = "passed" ]]; then
      preflight_passed=1
    else
      EXPERIMENT_SPEC_FILE="${BENCHMARK_PREFLIGHT_SPEC_FILE:-}"
      EXPERIMENT_SPEC_ID="${BENCHMARK_PREFLIGHT_SPEC_ID:-benchmark-preflight}"
      EXPERIMENT_SPEC_REF="${BENCHMARK_PREFLIGHT_SPEC_REF:-experiments/benchmark-preflight.env}"
      EXPERIMENT_SPEC_SHA256="${BENCHMARK_PREFLIGHT_SPEC_SHA256:-}"
      METRICS_PID=""
      METRICS_STOP_FILE=""
      METRICS_READY_FILE=""
      BACKGROUND_PIDS=()
      BACKGROUND_STOP_FILES=()
      EXPERIMENT_WORKLOAD_LIFECYCLE_STARTED=0
    fi
  fi
  if [[ "${BENCHMARK_TERMINAL_CLEANUP_DONE:-0}" != "1" ]]; then
    if ! benchmark_phase_complete_before_cleanup "$exit_code"; then
      phase_status=1
    elif [[ "${BENCHMARK_PHASE_BACKFILLED_FAILURE:-0}" = "1" ]]; then
      phase_status=1
    fi
    if [[ "$phase_status" != "0" ]]; then
      terminal_failure=1
    fi
    if [[ "$exit_code" = "0" && "$phase_status" != "0" ]]; then
      exit_code=1
    fi
    cleanup_started="$(benchmark_phase_now)"
    cleanup || {
      cleanup_exit="$?"
      cleanup_status="failed"
      cleanup_reason="cleanup exited $cleanup_exit"
    }
    cleanup_finished="$(benchmark_phase_now)"
    if ! benchmark_phase_append 11 cleanup "$cleanup_status" "$cleanup_started" "$cleanup_finished" "$cleanup_reason"; then
      cleanup_status=failed
    fi
    if [[ "${RUN_DIRECTORY_OWNED:-0}" = "1" && -n "${RUN_DIR:-}" ]]; then
      if materialize_benchmark_controls_v2; then
        :
      else
        controls_exit="$?"
        terminal_failure=1
        if [[ "$exit_code" = "0" ]]; then
          exit_code="$controls_exit"
        fi
      fi
    fi
    if [[ "$cleanup_status" = "failed" ]]; then
      terminal_failure=1
    fi
    if [[ "$exit_code" = "0" && "$cleanup_status" = "failed" ]]; then
      exit_code=1
    fi
  else
    cleanup_finished="$BENCHMARK_TERMINAL_CLEANUP_FINISHED_AT"
  fi
  VERDICT_FINISHED_AT="$cleanup_finished"
  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" && "$preflight_passed" != "1" &&
        "${RUN_DIRECTORY_OWNED:-0}" = "1" && -n "${RUN_DIR:-}" ]]; then
    # A full manifest may have been partially advanced before a preflight
    # operation failed. Restore the conservative preflight envelope so the
    # terminal artifact remains truthful and independently verifiable.
    set +e
    write_benchmark_preflight_manifest
    set -e
  fi
  if [[ "$terminal_failure" = "1" && "${VERDICT_WRITTEN:-0}" = "1" &&
        "${RUN_DIRECTORY_OWNED:-0}" = "1" && -n "${RUN_DIR:-}" && -f "$RUN_DIR/manifest.env" ]]; then
    set +e
    WORKLOAD_EXIT="$exit_code"
    write_terminal_failed_verdict "benchmark lifecycle or cleanup failed (runner exit $exit_code)"
    set -e
  fi
  if [[ "${VERDICT_WRITTEN:-0}" != "1" && "${RUN_DIRECTORY_OWNED:-0}" = "1" &&
        -n "${RUN_DIR:-}" && -f "$RUN_DIR/manifest.env" ]]; then
    set +e
    WORKLOAD_EXIT="${WORKLOAD_EXIT:-$exit_code}"
    if [[ "$WORKLOAD_EXIT" = "0" ]]; then
      WORKLOAD_EXIT="$exit_code"
    fi
    write_terminal_failed_verdict "experiment aborted before terminal verdict (runner exit $exit_code)"
    set -e
  elif [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" != "1" && "${RUN_DIRECTORY_OWNED:-0}" = "1" &&
          -n "${RUN_DIR:-}" && ! -e "$RUN_DIR/manifest.env" && ! -L "$RUN_DIR/manifest.env" ]]; then
    # A state-writer failure is still pre-publication: remove only the empty
    # directories this invocation created so it cannot masquerade as a run
    # without a terminal verdict. rmdir is deliberately fail-closed and never
    # removes unexpected content.
    rmdir "$RUN_DIR/hooks" "$RUN_DIR/snapshots" "$RUN_DIR/artifacts" "$RUN_DIR" >/dev/null 2>&1 || true
  fi
  exit "$exit_code"
}

handle_termination() {
  local signal="${1:?signal is required}"
  local exit_code=143
  case "$signal" in
    INT) exit_code=130 ;;
    HUP) exit_code=129 ;;
  esac
  trap - HUP INT TERM
  PGWORKBENCH_TERMINATING=1
  if [[ "${WORKLOAD_EXIT:-0}" = "0" ]]; then
    WORKLOAD_EXIT="$exit_code"
  fi
  echo "Experiment runner received $signal; beginning bounded terminal cleanup" >&2
  exit "$exit_code"
}

experiment_workload_action() {
  local action="${1:?workload lifecycle action is required}"
  local benchmark_prepared=0

  [[ -n "${EXPERIMENT_WORKLOAD_SPEC:-}" ]] || return 0
  if [[ -n "${PGWORKBENCH_BENCHMARK_PHASE_FILE:-}" ]]; then
    benchmark_prepared=1
  fi
  WORKLOAD_LOG_FILE="$RUN_DIR/workload.log" \
  WORKLOAD_LOG_DIR="$RUN_DIR" \
  PGBENCH_RESULT_FILE="${PGBENCH_RESULT_FILE:-$RUN_DIR/driver/pgbench-summary.log}" \
  PGBENCH_RAW_LOG_DIR="${PGBENCH_RAW_LOG_DIR:-$RUN_DIR/driver/pgbench-raw}" \
  PGWORKBENCH_BENCHMARK_PREPARED="$benchmark_prepared" \
  PGWORKBENCH_BENCHMARK_CONTROL_RUN_DIR="$RUN_DIR" \
  PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
  PROFILE_SECONDS="${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
    "$REPO_DIR/scripts/run_workload.sh" "$action" "$EXPERIMENT_WORKLOAD_SPEC"
}

cleanup() {
  local status=0 current_status=0

  begin_owned_children_stop 1 || status="$?"
  stop_metrics || status="$?"
  stop_background_specs || status="$?"
  PGWORKBENCH_OWNED_CLEANUP_DEADLINE=""
  if [[ "${EXPERIMENT_WORKLOAD_LIFECYCLE_STARTED:-0}" = "1" &&
        -n "${RUN_DIR:-}" && -n "${EXPERIMENT_WORKLOAD_SPEC:-}" ]]; then
    current_status=0
    experiment_workload_action cleanup || current_status="$?"
    if [[ "$current_status" != "0" ]]; then
      status="$current_status"
    fi
  fi
  return "$status"
}

runtime_fingerprint_target() {
	case "$1" in
		multi-version-upgrade)
			printf '%s\n' upgrade-new
			;;
		*)
			printf '%s\n' primary
			;;
	esac
}

capture_runtime_fingerprint() {
	local topology="$1"
	local psql_script version_num numeric_version major

	psql_script="$REPO_DIR/scripts/psql.sh"
	if [[ "$topology" = "multi-version-upgrade" ]]; then
		psql_script="$REPO_DIR/scripts/psql_upgrade_new.sh"
	fi

	if ! version_num="$("$psql_script" -Atq -c "SHOW server_version_num")"; then
		echo "Failed to observe PostgreSQL server_version_num at fingerprint target ${PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET}" >&2
		return 1
	fi
	if [[ ! "$version_num" =~ ^[0-9]+$ ]]; then
		echo "Invalid PostgreSQL server_version_num observation: $version_num" >&2
		return 1
	fi

	numeric_version=$((10#$version_num))
	if (( numeric_version < 10000 )); then
		echo "Invalid PostgreSQL server_version_num observation: $version_num" >&2
		return 1
	fi
	if (( numeric_version >= 100000 )); then
		major="$((numeric_version / 10000))"
	else
		major="$((numeric_version / 10000)).$(((numeric_version / 100) % 100))"
	fi

	PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS=observed
	PGWORKBENCH_RUNTIME_OS=
	PGWORKBENCH_RUNTIME_ARCH=
	PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM="$version_num"
	PGWORKBENCH_POSTGRES_SERVER_MAJOR="$major"
	PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT="$(iso_now)"
	write_manifest
}

benchmark_preflight_requested() {
  [[ -n "${PGWORKBENCH_BENCHMARK_PHASE_FILE:-}" && -n "${EXPERIMENT_RUN_ID:-}" ]]
}

seed_benchmark_preflight_spec() {
  local input="${1:-}"
  local pack_root experiment_root candidate="" resolved relative

  pack_root="$(realpath "$REPO_DIR")"
  experiment_root="$pack_root/experiments"
  case "$input" in
    /*) candidate="$input" ;;
    experiments/*) candidate="$pack_root/$input" ;;
    *.env) candidate="$experiment_root/$input" ;;
    "") candidate="" ;;
    *) candidate="$experiment_root/$input.env" ;;
  esac

  EXPERIMENT_SPEC_FILE=""
  EXPERIMENT_SPEC_ID=benchmark-preflight
  EXPERIMENT_SPEC_REF=experiments/benchmark-preflight.env
  if [[ -n "$candidate" && ! -L "$candidate" && -f "$candidate" ]]; then
    resolved="$(realpath "$candidate")"
    case "$resolved" in
      "$experiment_root"/*)
        relative="${resolved#"$experiment_root/"}"
        if [[ "$relative" = *.env && "$relative" != ".env" ]]; then
          EXPERIMENT_SPEC_FILE="$resolved"
          EXPERIMENT_SPEC_ID="${relative%.env}"
          EXPERIMENT_SPEC_REF="experiments/$relative"
        fi
        ;;
    esac
  fi
  export EXPERIMENT_SPEC_FILE EXPERIMENT_SPEC_ID EXPERIMENT_SPEC_REF
}

begin_benchmark_preflight() {
  local input="${1:-}"
  local journal="${PGWORKBENCH_BENCHMARK_PHASE_FILE:-}"
  local primary_journal

  benchmark_preflight_requested || return 0
  BENCHMARK_PREFLIGHT_ACTIVE=1
  readonly BENCHMARK_PREFLIGHT_ACTIVE
  RUN_DIRECTORY_OWNED=0
  RUN_DIR=""
  METRICS_PID=""
  METRICS_STOP_FILE=""
  METRICS_READY_FILE=""
  BACKGROUND_PIDS=()
  BACKGROUND_STOP_FILES=()
  BACKGROUND_LOGS=()
  BACKGROUND_EXIT=0
  METRICS_EXIT=0
  MANIFEST_FINALIZED_DIGEST=
  WORKLOAD_EXIT=0
  ASSERT_EXIT=0
  SCAN_EXIT=0
  VERDICT_WRITTEN=0
  PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS=unavailable
  PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET=primary
  PGWORKBENCH_RUNTIME_OS=
  PGWORKBENCH_RUNTIME_ARCH=
  PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM=
  PGWORKBENCH_POSTGRES_SERVER_MAJOR=
  PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT=

  BENCHMARK_PREFLIGHT_PHASE_FILE="$journal"
  BENCHMARK_PREFLIGHT_PHASE_MIRROR_FILE=""
  BENCHMARK_PREFLIGHT_RUN_ID="$EXPERIMENT_RUN_ID"
  BENCHMARK_PREFLIGHT_TRIAL="${PGWORKBENCH_BENCHMARK_TRIAL:-}"
  BENCHMARK_PREFLIGHT_PGWORKBENCH_BIN="${PGWORKBENCH_BIN:-}"
  BENCHMARK_PREFLIGHT_STARTED_AT="$(benchmark_phase_now)"
  readonly BENCHMARK_PREFLIGHT_RUN_ID BENCHMARK_PREFLIGHT_TRIAL BENCHMARK_PREFLIGHT_PGWORKBENCH_BIN BENCHMARK_PREFLIGHT_STARTED_AT

  trap terminal_cleanup EXIT
  trap 'handle_termination HUP' HUP
  trap 'handle_termination INT' INT
  trap 'handle_termination TERM' TERM

  if [[ "$journal" != /* || -L "$journal" || ! -f "$journal" || -s "$journal" ]]; then
    echo "Benchmark phase journal must be an empty regular absolute file: $journal" >&2
    return 2
  fi
  STARTED_AT="$BENCHMARK_PREFLIGHT_STARTED_AT"
  RUN_ID="$BENCHMARK_PREFLIGHT_RUN_ID"
  if [[ -z "$RUN_ID" || "$RUN_ID" = "." || "$RUN_ID" = ".." || "$(sanitize_id "$RUN_ID")" != "$RUN_ID" || ${#RUN_ID} -gt 200 ]]; then
    echo "Invalid EXPERIMENT_RUN_ID: $RUN_ID" >&2
    return 2
  fi
  if [[ "${PGWORKBENCH_BENCHMARK_RUN_ID:-}" != "$RUN_ID" || ! "$BENCHMARK_PREFLIGHT_TRIAL" =~ ^[1-9][0-9]*$ ]]; then
    echo "Benchmark phase journal run/trial binding is missing or inconsistent" >&2
    return 2
  fi

  seed_benchmark_preflight_spec "$input"
  BENCHMARK_PREFLIGHT_SPEC_FILE="$EXPERIMENT_SPEC_FILE"
  BENCHMARK_PREFLIGHT_SPEC_ID="$EXPERIMENT_SPEC_ID"
  BENCHMARK_PREFLIGHT_SPEC_REF="$EXPERIMENT_SPEC_REF"
  BENCHMARK_PREFLIGHT_SPEC_SHA256="${EXPERIMENT_SPEC_SHA256:-}"
  readonly BENCHMARK_PREFLIGHT_SPEC_FILE BENCHMARK_PREFLIGHT_SPEC_ID BENCHMARK_PREFLIGHT_SPEC_REF BENCHMARK_PREFLIGHT_SPEC_SHA256
  prepare_runs_root
  RUN_DIR="$RUNS_ROOT/$RUN_ID"
  if [[ -e "$RUN_DIR" || -L "$RUN_DIR" ]]; then
    echo "Refusing to overwrite existing immutable run: $RUN_DIR" >&2
    return 2
  fi
  mkdir "$RUN_DIR"
  BENCHMARK_PREFLIGHT_OWNED_RUN_DIR="$RUN_DIR"
  readonly BENCHMARK_PREFLIGHT_OWNED_RUN_DIR
  RUN_DIRECTORY_OWNED=1
  mkdir "$RUN_DIR/hooks" "$RUN_DIR/snapshots" "$RUN_DIR/artifacts"
  mkdir "$RUN_DIR/artifacts/benchmark"
  if benchmark_control_v2_active; then
    mkdir "$RUN_DIR/artifacts/benchmark/controls"
  fi
  primary_journal="$RUN_DIR/artifacts/benchmark/phases.tsv"
  mv -- "$journal" "$primary_journal"
  ( set -o noclobber; umask 077; : > "$journal" )
  BENCHMARK_PREFLIGHT_PHASE_FILE="$primary_journal"
  BENCHMARK_PREFLIGHT_PHASE_MIRROR_FILE="$journal"
  PGWORKBENCH_BENCHMARK_PHASE_FILE="$primary_journal"
  PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE="$journal"
  export PGWORKBENCH_BENCHMARK_PHASE_FILE PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE
  resolve_engine_identity
  write_benchmark_preflight_manifest
}

restore_benchmark_preflight_ownership() {
  [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" ]] || return 0
  PGWORKBENCH_BENCHMARK_PHASE_FILE="$BENCHMARK_PREFLIGHT_PHASE_FILE"
  PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE="${BENCHMARK_PREFLIGHT_PHASE_MIRROR_FILE:-}"
  PGWORKBENCH_BENCHMARK_RUN_ID="$BENCHMARK_PREFLIGHT_RUN_ID"
  PGWORKBENCH_BENCHMARK_TRIAL="$BENCHMARK_PREFLIGHT_TRIAL"
  PGWORKBENCH_BIN="$BENCHMARK_PREFLIGHT_PGWORKBENCH_BIN"
  RUN_ID="$BENCHMARK_PREFLIGHT_RUN_ID"
  STARTED_AT="$BENCHMARK_PREFLIGHT_STARTED_AT"
  RUN_DIR="$BENCHMARK_PREFLIGHT_OWNED_RUN_DIR"
  RUN_DIRECTORY_OWNED=1
  export PGWORKBENCH_BENCHMARK_PHASE_FILE PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE PGWORKBENCH_BENCHMARK_RUN_ID PGWORKBENCH_BENCHMARK_TRIAL PGWORKBENCH_BIN RUN_ID STARTED_AT RUN_DIR
}

run_selected_experiment() {
  local input="${1:-}"

  begin_benchmark_preflight "$input"
  if [[ -z "$input" ]]; then
    echo "experiment spec is required" >&2
    return 2
  fi
  load_repo_env
  restore_benchmark_preflight_ownership
  load_spec "$input"
  restore_benchmark_preflight_ownership
  run_experiment
}

delegate_selected_experiment() {
  local input="${1:-}"
  local -a runner_args=(experiment run)

  # Public/direct shell entrypoints always route through the Go runner. An
  # ambient PGWORKBENCH_SUPERVISED value is deliberately not authorization:
  # only the private argv action below enters the supervised shell body.
  if [[ -n "${PGWORKBENCH_RUNTIME:-}" ]]; then
    runner_args+=(--runtime "$PGWORKBENCH_RUNTIME")
  fi
  if [[ -n "${EXPERIMENT_RUN_ID:-}" ]]; then
    runner_args+=(--run-id "$EXPERIMENT_RUN_ID")
  fi
  if [[ -n "${PGWORKBENCH_EXECUTION_TIMEOUT:-}" ]]; then
    runner_args+=(--timeout "$PGWORKBENCH_EXECUTION_TIMEOUT")
  fi
  if [[ -n "${PGWORKBENCH_CLEANUP_GRACE:-}" ]]; then
    runner_args+=(--cleanup-grace "$PGWORKBENCH_CLEANUP_GRACE")
  fi
  run_pgworkbench "${runner_args[@]}" "$input"
}

run_experiment() {
  local topology="${EXPERIMENT_TOPOLOGY:-${TOPOLOGY:-single}}"
  local -a scan_paths=()
  local phase_started phase_finished phase_status phase_reason prior_failed_phase
  local cleanup_started cleanup_finished cleanup_status cleanup_reason cleanup_exit controls_exit

  validate_state_writer
  activate_experiment_target_guard
  reject_experiment_target_overrides
  validate_shell_hook_trust
  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" != "1" ]]; then
    STARTED_AT="$(iso_now)"
    RUN_ID="${EXPERIMENT_RUN_ID:-$(sanitize_id "${EXPERIMENT_SPEC_ID}")-$(timestamp)}"
    if [[ -z "$RUN_ID" || "$RUN_ID" = "." || "$RUN_ID" = ".." || "$(sanitize_id "$RUN_ID")" != "$RUN_ID" || ${#RUN_ID} -gt 200 ]]; then
      echo "Invalid EXPERIMENT_RUN_ID: $RUN_ID" >&2
      return 2
    fi
    prepare_runs_root
    RUN_DIR="$RUNS_ROOT/$RUN_ID"
    METRICS_PID=""
    METRICS_STOP_FILE=""
    METRICS_READY_FILE=""
    BACKGROUND_PIDS=()
    BACKGROUND_STOP_FILES=()
    BACKGROUND_LOGS=()
    BACKGROUND_EXIT=0
    METRICS_EXIT=0
    MANIFEST_FINALIZED_DIGEST=
    WORKLOAD_EXIT=0
    ASSERT_EXIT=0
    SCAN_EXIT=0
    VERDICT_WRITTEN=0
    PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS=unavailable
    PGWORKBENCH_RUNTIME_OS=
    PGWORKBENCH_RUNTIME_ARCH=
    PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM=
    PGWORKBENCH_POSTGRES_SERVER_MAJOR=
    PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT=
    if [[ -e "$RUN_DIR" || -L "$RUN_DIR" ]]; then
      echo "Refusing to overwrite existing immutable run: $RUN_DIR" >&2
      return 2
    fi
    mkdir -p "$RUN_DIR" "$RUN_DIR/hooks" "$RUN_DIR/snapshots" "$RUN_DIR/artifacts"
    RUN_DIRECTORY_OWNED=1
    trap terminal_cleanup EXIT
  fi
  PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET="$(runtime_fingerprint_target "$topology")"
  resolve_engine_identity
  trap 'handle_termination HUP' HUP
  trap 'handle_termination INT' INT
  trap 'handle_termination TERM' TERM
  write_manifest
  capture_spec_provenance
  benchmark_control_prepare_directory

  # Keep the canonical transcript without relying on Bash process substitution
  # or /dev/fd. Some macOS sandboxes reject opening the synthetic descriptor;
  # direct append works for both native and Docker runtimes.
  exec >> "$RUN_DIR/stdout.log" 2>&1

  echo "run_id=$RUN_ID"
  echo "run_dir=$RUN_DIR"
  echo "started_at=$STARTED_AT"

  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" ]]; then
    phase_finished="$(benchmark_phase_now)"
    benchmark_phase_append 1 preflight passed "$BENCHMARK_PREFLIGHT_STARTED_AT" "$phase_finished" ""
  fi
  phase_started="$(benchmark_phase_now)"
  if [[ "${EXPERIMENT_RUNTIME_RESET:-${EXPERIMENT_DOCKER_RESET:-0}}" = "1" ]]; then
	PGWORKBENCH_RUNTIME="${PGWORKBENCH_RUNTIME:-docker}" \
	  "$REPO_DIR/scripts/runtime.sh" reset "$topology"
  else
	PGWORKBENCH_RUNTIME="${PGWORKBENCH_RUNTIME:-docker}" \
	  "$REPO_DIR/scripts/runtime.sh" up "$topology"
  fi
	capture_runtime_fingerprint "$topology"
	MANIFEST_FINALIZED_DIGEST="$(sha256_digest_file "$RUN_DIR/manifest.env")"
	TOPOLOGY="$topology" PGWORKBENCH_RUNTIME="${PGWORKBENCH_RUNTIME:-docker}" \
	  "$REPO_DIR/scripts/apply_pg_config.sh" "${EXPERIMENT_PG_CONFIG:-${PG_CONFIG:-default}}"
	capture_effective_pg_settings
	# Applying a profile restarts PostgreSQL. Enforce and inspect the final
	# server/driver container only after that restart so the recorded limits
	# cover the process that actually executes pgbench.
	benchmark_control_enforce_resource_budget

  if [[ -n "${EXPERIMENT_DATASET_SPEC:-}" ]]; then
    DATASET_SIZE="${EXPERIMENT_DATASET_SIZE:-${DATASET_SIZE:-small}}" \
      "$REPO_DIR/scripts/load_dataset.sh" load "$EXPERIMENT_DATASET_SPEC"
  fi

  if [[ -n "${EXPERIMENT_PROFILE:-}" ]]; then
    if [[ "${EXPERIMENT_PROFILE_SETUP:-1}" = "1" ]]; then
      PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
      PROFILE_SECONDS="${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
        "$REPO_DIR/scripts/run_profile_sql.sh" "$EXPERIMENT_PROFILE" 00_setup.sql
    fi

    if [[ "${EXPERIMENT_PROFILE_RUN:-0}" = "1" ]]; then
      PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-${PROFILE_SIZE:-small}}" \
      PROFILE_SECONDS="${EXPERIMENT_PROFILE_SECONDS:-${PROFILE_SECONDS:-30}}" \
        "$REPO_DIR/scripts/run_profile_sql.sh" "$EXPERIMENT_PROFILE" "${EXPERIMENT_PROFILE_RUN_SQL:-10_run.sql}"
    fi
  fi

  run_psql_file_list "${EXPERIMENT_BEFORE_SQL_FILES:-}"
  run_inline_sql "${EXPERIMENT_BEFORE_SQL:-}"
  run_shell_hook EXPERIMENT_BEFORE_SHELL "${EXPERIMENT_BEFORE_SHELL:-}"
  if [[ -n "${PGWORKBENCH_BENCHMARK_PHASE_FILE:-}" && -n "${EXPERIMENT_WORKLOAD_SPEC:-}" ]]; then
    EXPERIMENT_WORKLOAD_LIFECYCLE_STARTED=1
    WORKLOAD_RUN_LOG=0 experiment_workload_action prepare
  fi
  benchmark_control_run_statistics_reset before-trial
  benchmark_control_prepare_statistics_none
  phase_finished="$(benchmark_phase_now)"
  benchmark_phase_append 2 prepare passed "$phase_started" "$phase_finished" ""

  phase_started="$(benchmark_phase_now)"
  snapshot before
  benchmark_control_prepare_overhead_unquantified
  start_metrics
  start_background_specs
  phase_finished="$(benchmark_phase_now)"
  if [[ -n "${EXPERIMENT_BACKGROUND_SPECS:-}" || "${EXPERIMENT_BACKGROUND_WARMUP:-0}" != "0" ]]; then
    benchmark_phase_append 3 stabilize passed "$phase_started" "$phase_finished" ""
  else
    benchmark_phase_append 3 stabilize skipped "$phase_started" "$phase_finished" "no stabilization gate declared"
  fi

  if [[ -n "${EXPERIMENT_WORKLOAD_SPEC:-}" ]]; then
    EXPERIMENT_WORKLOAD_LIFECYCLE_STARTED=1
    set +e
    experiment_workload_action run
    WORKLOAD_EXIT="$?"
    set -e
  fi

  phase_started="$(benchmark_phase_now)"
  if [[ "${EXPERIMENT_BACKGROUND_WAIT:-0}" = "1" ]]; then
    wait_background_specs
  fi

  begin_owned_children_stop 0
  stop_background_specs
  stop_metrics
  PGWORKBENCH_OWNED_CLEANUP_DEADLINE=""
  phase_finished="$(benchmark_phase_now)"
  phase_status=passed
  phase_reason=""
  prior_failed_phase="$(benchmark_phase_first_failure_name_or_empty)"
  if [[ -n "$prior_failed_phase" ]]; then
    phase_status=skipped
    phase_reason="not reached after failed $prior_failed_phase phase"
  elif [[ "$BACKGROUND_EXIT" != "0" || "$METRICS_EXIT" != "0" ]]; then
    phase_status=failed
    phase_reason="background or metrics collector failed (background=$BACKGROUND_EXIT metrics=$METRICS_EXIT)"
  fi
  benchmark_phase_append 8 cooldown "$phase_status" "$phase_started" "$phase_finished" "$phase_reason"

  if [[ "$BACKGROUND_EXIT" != "0" && "$WORKLOAD_EXIT" = "0" ]]; then
    WORKLOAD_EXIT="$BACKGROUND_EXIT"
  fi
  if [[ "$METRICS_EXIT" != "0" && "$WORKLOAD_EXIT" = "0" ]]; then
    WORKLOAD_EXIT="$METRICS_EXIT"
  fi

  phase_started="$(benchmark_phase_now)"
  run_psql_file_list "${EXPERIMENT_AFTER_SQL_FILES:-}"
  run_inline_sql "${EXPERIMENT_AFTER_SQL:-}"
  run_shell_hook EXPERIMENT_AFTER_SHELL "${EXPERIMENT_AFTER_SHELL:-}"

  snapshot after

  set +e
  run_assertions
  ASSERT_EXIT="$?"
  set -e
  phase_finished="$(benchmark_phase_now)"
  phase_status=passed
  phase_reason=""
  prior_failed_phase="$(benchmark_phase_first_failure_name_or_empty)"
  if [[ -n "$prior_failed_phase" ]]; then
    phase_status=skipped
    phase_reason="not reached after failed $prior_failed_phase phase"
  elif [[ "$ASSERT_EXIT" != "0" ]]; then
    phase_status=failed
    phase_reason="assertions exited $ASSERT_EXIT"
  fi
  benchmark_phase_append 9 validate "$phase_status" "$phase_started" "$phase_finished" "$phase_reason"

  phase_started="$(benchmark_phase_now)"
  prior_failed_phase="$(benchmark_phase_first_failure_name_or_empty)"
  if [[ -z "$prior_failed_phase" && "$WORKLOAD_EXIT" = "0" && "$ASSERT_EXIT" = "0" && -n "${EXPERIMENT_WORKLOAD_SPEC:-}" ]]; then
    set +e
    experiment_workload_action collect
    WORKLOAD_COLLECT_EXIT="$?"
    set -e
    if [[ "$WORKLOAD_COLLECT_EXIT" != "0" ]]; then
      WORKLOAD_EXIT="$WORKLOAD_COLLECT_EXIT"
    fi
  fi
  if [[ "$ASSERT_EXIT" = "0" ]]; then
    set +e
    capture_evidence_files
    ASSERT_EXIT="$?"
    set -e
  fi

  set +e
  read -r -a scan_paths <<< "${EXPERIMENT_SCAN_PATHS:-}"
  "$REPO_DIR/scripts/scan_pg_failures.sh" "$RUN_DIR" "${scan_paths[@]}" > "$RUN_DIR/scan.log" 2>&1
  SCAN_EXIT="$?"
  set -e
  if ! verify_finalized_manifest; then
    if [[ "$ASSERT_EXIT" = "0" ]]; then
      ASSERT_EXIT=1
    fi
    SCAN_EXIT="${SCAN_EXIT:-0}"
  fi

  phase_finished="$(benchmark_phase_now)"
  prior_failed_phase="$(benchmark_phase_first_failure_name_or_empty)"

  if [[ "$WORKLOAD_EXIT" != "0" ]]; then
    if [[ -n "$prior_failed_phase" ]]; then
      benchmark_phase_append 10 collect skipped "$phase_started" "$phase_finished" "not reached after failed $prior_failed_phase phase"
    else
      benchmark_phase_append 10 collect failed "$phase_started" "$phase_finished" "workload or post-measure artifact collection failed"
    fi
    write_intermediate_failed_verdict "workload failed"
    exit "$WORKLOAD_EXIT"
  fi

  if [[ "$ASSERT_EXIT" != "0" ]]; then
    if [[ -n "$prior_failed_phase" ]]; then
      benchmark_phase_append 10 collect skipped "$phase_started" "$phase_finished" "not reached after failed $prior_failed_phase phase"
    else
      benchmark_phase_append 10 collect failed "$phase_started" "$phase_finished" "assertion or manifest validation failed"
    fi
    write_intermediate_failed_verdict "assertion failed"
    exit "$ASSERT_EXIT"
  fi

  if [[ "$SCAN_EXIT" != "0" ]]; then
    if [[ -n "$prior_failed_phase" ]]; then
      benchmark_phase_append 10 collect skipped "$phase_started" "$phase_finished" "not reached after failed $prior_failed_phase phase"
    else
      benchmark_phase_append 10 collect failed "$phase_started" "$phase_finished" "failure scan found evidence"
    fi
    write_intermediate_failed_verdict "failure evidence found"
    exit "$SCAN_EXIT"
  fi

  if [[ -n "$prior_failed_phase" ]]; then
    benchmark_phase_append 10 collect skipped "$phase_started" "$phase_finished" "not reached after failed $prior_failed_phase phase"
    write_intermediate_failed_verdict "benchmark lifecycle failed"
    exit 1
  fi

  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" = "1" ]]; then
    phase_finished="$(benchmark_phase_now)"
    benchmark_phase_append 10 collect passed "$phase_started" "$phase_finished" ""
    cleanup_started="$(benchmark_phase_now)"
    cleanup_status=passed
    cleanup_reason=""
    cleanup_exit=0
    cleanup || {
      cleanup_exit="$?"
      cleanup_status=failed
      cleanup_reason="cleanup exited $cleanup_exit"
    }
    cleanup_finished="$(benchmark_phase_now)"
    benchmark_phase_append 11 cleanup "$cleanup_status" "$cleanup_started" "$cleanup_finished" "$cleanup_reason"
    BENCHMARK_TERMINAL_CLEANUP_DONE=1
    BENCHMARK_TERMINAL_CLEANUP_FINISHED_AT="$cleanup_finished"
    controls_exit=0
    if materialize_benchmark_controls_v2; then
      :
    else
      controls_exit="$?"
    fi
    if [[ "$controls_exit" != "0" ]]; then
      WORKLOAD_EXIT="$controls_exit"
      VERDICT_FINISHED_AT="$cleanup_finished"
      write_verdict failed "benchmark control materialization failed"
      VERDICT_WRITTEN=1
      exit "$controls_exit"
    fi
    if [[ "$cleanup_status" = "failed" ]]; then
      WORKLOAD_EXIT="$cleanup_exit"
      write_verdict failed "benchmark cleanup failed"
      VERDICT_WRITTEN=1
      exit "$cleanup_exit"
    fi
    VERDICT_FINISHED_AT="$cleanup_finished"
    write_verdict passed "experiment passed"
    VERDICT_WRITTEN=1
  else
    write_verdict passed "experiment passed"
  fi
  set +e
  run_pgworkbench run verify "$RUN_DIR"
  EVIDENCE_VERIFY_EXIT="$?"
  set -e
  if [[ "$EVIDENCE_VERIFY_EXIT" != "0" ]]; then
    ASSERT_EXIT="$EVIDENCE_VERIFY_EXIT"
    if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" != "1" ]]; then
      phase_finished="$(benchmark_phase_now)"
      benchmark_phase_append 10 collect failed "$phase_started" "$phase_finished" "post-run evidence verification failed"
    fi
    write_verdict failed "post-run evidence verification failed"
    VERDICT_WRITTEN=1
    exit "$EVIDENCE_VERIFY_EXIT"
  fi
  if [[ "${BENCHMARK_PREFLIGHT_ACTIVE:-0}" != "1" ]]; then
    phase_finished="$(benchmark_phase_now)"
    benchmark_phase_append 10 collect passed "$phase_started" "$phase_finished" ""
  fi
  VERDICT_WRITTEN=1
  echo "verdict=passed"
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
    sed -n '1,220p' "$(resolve_spec "${1:?experiment spec is required}")"
    ;;
  run)
    delegate_selected_experiment "${1:-}"
    ;;
  __pgworkbench_internal_run_v1)
    if [[ "${PGWORKBENCH_SUPERVISED:-0}" != "1" ]]; then
      echo "Refusing internal experiment route without Go supervision" >&2
      exit 2
    fi
    if [[ $# -ne 1 ]]; then
      echo "Internal experiment route requires exactly one resolved spec path" >&2
      exit 2
    fi
    run_selected_experiment "$1"
    ;;
  *)
    delegate_selected_experiment "$ACTION"
    ;;
esac
