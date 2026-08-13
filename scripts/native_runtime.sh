#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
PRESERVED_ENV_NAMES=()
PRESERVED_ENV_VALUES=()
ENV_PATH=""

TOPOLOGY_NAME=""
RUNTIME_ROOT=""
DATA_DIR=""
LOG_DIR=""
LOG_FILE=""
STATE_FILE=""
WRITE_STATE_TEMP=""

INITDB_BIN=""
PG_CTL_BIN=""
CREATEDB_BIN=""
PG_ISREADY_BIN=""
PSQL_BIN=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/native_runtime.sh list
  scripts/native_runtime.sh show <topology>
  scripts/native_runtime.sh up [topology]
  scripts/native_runtime.sh reset [topology]
  scripts/native_runtime.sh restart [topology]
  scripts/native_runtime.sh down [topology]
  scripts/native_runtime.sh status [topology]
  scripts/native_runtime.sh wait [topology]

Supported topologies:
  single       One host PostgreSQL instance in .tmp/native/single.
  source-tree  A separate single-node instance suitable for source builds.

Binary lookup order:
  PGWORKBENCH_NATIVE_BINDIR, PG_INSTALL_DIR/bin, then PATH.

The backend only binds to loopback and never adopts an existing server or a
data directory that it did not initialize itself.
USAGE
}

die() {
  echo "$*" >&2
  exit 2
}

capture_env_overrides() {
  PRESERVED_ENV_NAMES=()
  PRESERVED_ENV_VALUES=()

  local name
  while IFS= read -r name; do
    case "$name" in
      ENV_FILE|POSTGRES_*|PGBOUNCER_*|ALLOW_*|TOPOLOGY|PGWORKBENCH_*|PG_INSTALL_DIR)
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
  if [[ -f "$ENV_PATH" ]]; then
    capture_env_overrides
    set -a
    # shellcheck disable=SC1090
    source "$ENV_PATH"
    set +a
    restore_env_overrides
  fi

  # Every native client belongs to the workbench-owned loopback cluster.
  # libpq variables such as PGHOSTADDR and service files can override or
  # supplement explicit -h/-p arguments, so neither caller nor .env may route
  # lifecycle credentials outside that target.
  unset PGHOSTADDR PGSERVICE PGSERVICEFILE PGPASSFILE PGOPTIONS \
    PGTARGETSESSIONATTRS PGSSLMODE PGSSLROOTCERT PGSSLCERT PGSSLKEY \
    PGREQUIRESSL PGREQUIREAUTH PGCHANNELBINDING
}

require_topology() {
  case "$1" in
    single|source-tree)
      ;;
    *)
      die "Native runtime supports only single and source-tree topologies, got: $1"
      ;;
  esac
}

set_runtime_paths() {
  TOPOLOGY_NAME="$1"
  require_topology "$TOPOLOGY_NAME"

  RUNTIME_ROOT="$REPO_DIR/.tmp/native/$TOPOLOGY_NAME"
  DATA_DIR="$RUNTIME_ROOT/data"
  LOG_DIR="$RUNTIME_ROOT/log"
  LOG_FILE="$LOG_DIR/postgresql.log"
  STATE_FILE="$RUNTIME_ROOT/runtime.state"
}

validate_runtime_paths() {
  local path

  case "$RUNTIME_ROOT" in
    "$REPO_DIR/.tmp/native/"*)
      ;;
    *)
      die "Refusing unsafe native runtime path: $RUNTIME_ROOT"
      ;;
  esac

  for path in \
    "$REPO_DIR/.tmp" \
    "$REPO_DIR/.tmp/native" \
    "$RUNTIME_ROOT" \
    "$DATA_DIR" \
    "$LOG_DIR"; do
    if [[ -L "$path" ]]; then
      die "Refusing symlink in native runtime path: $path"
    fi
  done

  # These leaves are opened by shell redirection, sed, initdb, or pg_ctl. Test
  # with -L separately because a dangling symlink is false for -e/-f.
  for path in \
    "$STATE_FILE" \
    "$RUNTIME_ROOT/.initdb-password" \
    "$LOG_FILE" \
    "$DATA_DIR/postgresql.conf" \
    "$DATA_DIR/PG_VERSION"; do
    if [[ -L "$path" ]]; then
      die "Refusing symlink in native runtime file: $path"
    fi
  done

  if [[ -e "$STATE_FILE" && ! -f "$STATE_FILE" ]]; then
    die "Refusing non-regular native runtime state file: $STATE_FILE"
  fi
  if [[ -e "$LOG_FILE" && ! -f "$LOG_FILE" ]]; then
    die "Refusing non-regular native runtime log file: $LOG_FILE"
  fi
  if [[ -e "$DATA_DIR/postgresql.conf" && ! -f "$DATA_DIR/postgresql.conf" ]]; then
    die "Refusing non-regular native PostgreSQL config file: $DATA_DIR/postgresql.conf"
  fi
  if [[ -e "$DATA_DIR/PG_VERSION" && ! -f "$DATA_DIR/PG_VERSION" ]]; then
    die "Refusing non-regular native PostgreSQL version file: $DATA_DIR/PG_VERSION"
  fi
}

prepare_runtime_dirs() {
  local resolved_root

  validate_runtime_paths
  mkdir -p "$RUNTIME_ROOT" "$LOG_DIR"
  resolved_root="$(cd "$RUNTIME_ROOT" && pwd -P)"
  case "$resolved_root" in
    "$REPO_DIR/.tmp/native/"*)
      ;;
    *)
      die "Native runtime directory escaped the repository: $resolved_root"
      ;;
  esac
}

validate_target() {
  local port_number

  case "${POSTGRES_HOST:-127.0.0.1}" in
    127.0.0.1|localhost)
      ;;
    *)
      die "Native runtime requires POSTGRES_HOST=127.0.0.1 or localhost; refusing: ${POSTGRES_HOST:-}"
      ;;
  esac

  if ! [[ "${POSTGRES_PORT:-55433}" =~ ^[0-9]+$ ]]; then
    die "POSTGRES_PORT must be an integer, got: ${POSTGRES_PORT:-}"
  fi
  port_number=$((10#${POSTGRES_PORT:-55433}))
  if ((port_number < 1024 || port_number > 65535)); then
    die "POSTGRES_PORT must be between 1024 and 65535, got: $port_number"
  fi

  if ! [[ "${POSTGRES_USER:-postgres}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    die "POSTGRES_USER must be a simple PostgreSQL identifier, got: ${POSTGRES_USER:-}"
  fi
  if ! [[ "${POSTGRES_DB:-pg_experiment_workbench}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    die "POSTGRES_DB must be a simple PostgreSQL identifier, got: ${POSTGRES_DB:-}"
  fi
  if [[ -z "${POSTGRES_PASSWORD:-postgres}" ]]; then
    die "POSTGRES_PASSWORD must not be empty for the native runtime"
  fi

  "$REPO_DIR/scripts/guard_local_pg.sh"
}

native_bindir() {
  local bindir="${PGWORKBENCH_NATIVE_BINDIR:-}"

  if [[ -z "$bindir" && -n "${PG_INSTALL_DIR:-}" ]]; then
    bindir="$PG_INSTALL_DIR/bin"
  fi
  if [[ -n "$bindir" && "$bindir" != /* ]]; then
    bindir="$REPO_DIR/$bindir"
  fi
  printf '%s\n' "$bindir"
}

resolve_binary() {
  local name="$1"
  local bindir
  local binary

  bindir="$(native_bindir)"
  if [[ -n "$bindir" ]]; then
    binary="$bindir/$name"
    if [[ ! -x "$binary" ]]; then
      die "Required PostgreSQL binary is not executable: $binary"
    fi
    printf '%s\n' "$binary"
    return 0
  fi

  binary="$(command -v "$name" || true)"
  if [[ -z "$binary" ]]; then
    die "Required PostgreSQL binary not found: $name (set PGWORKBENCH_NATIVE_BINDIR)"
  fi
  printf '%s\n' "$binary"
}

resolve_control_binaries() {
  PG_CTL_BIN="$(resolve_binary pg_ctl)"
}

resolve_client_binaries() {
  PG_ISREADY_BIN="$(resolve_binary pg_isready)"
  PSQL_BIN="$(resolve_binary psql)"
}

resolve_setup_binaries() {
  INITDB_BIN="$(resolve_binary initdb)"
  CREATEDB_BIN="$(resolve_binary createdb)"
  resolve_control_binaries
  resolve_client_binaries
}

cluster_initialized() {
  [[ -f "$DATA_DIR/PG_VERSION" ]]
}

cluster_running() {
  "$PG_CTL_BIN" -D "$DATA_DIR" status >/dev/null 2>&1
}

state_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "$STATE_FILE" | head -n 1
}

require_managed_state() {
  [[ -f "$STATE_FILE" ]] || die "Refusing unmanaged PostgreSQL data directory: $DATA_DIR"
  [[ "$(state_value format)" = "1" ]] || die "Unsupported native runtime state format: $STATE_FILE"
  [[ "$(state_value backend)" = "native" ]] || die "Native runtime state has the wrong backend: $STATE_FILE"
  [[ "$(state_value topology)" = "$TOPOLOGY_NAME" ]] || die "Native runtime state has the wrong topology: $STATE_FILE"
  [[ "$(state_value data_dir)" = "$DATA_DIR" ]] || die "Native runtime state has an unexpected data directory: $STATE_FILE"
}

require_matching_state() {
  require_managed_state

  [[ "$(state_value host)" = "${POSTGRES_HOST:-127.0.0.1}" ]] || \
    die "POSTGRES_HOST differs from the initialized native runtime; reset it explicitly to change the target"
  [[ "$(state_value port)" = "${POSTGRES_PORT:-55433}" ]] || \
    die "POSTGRES_PORT differs from the initialized native runtime; reset it explicitly to change the port"
  [[ "$(state_value database)" = "${POSTGRES_DB:-pg_experiment_workbench}" ]] || \
    die "POSTGRES_DB differs from the initialized native runtime; reset it explicitly to change the database"
  [[ "$(state_value user)" = "${POSTGRES_USER:-postgres}" ]] || \
    die "POSTGRES_USER differs from the initialized native runtime; reset it explicitly to change the user"
}

write_state() {
  validate_runtime_paths
  umask 077
  WRITE_STATE_TEMP="$(mktemp "$RUNTIME_ROOT/.runtime.state.XXXXXX")"
  {
    printf 'format=1\n'
    printf 'backend=native\n'
    printf 'topology=%s\n' "$TOPOLOGY_NAME"
    printf 'data_dir=%s\n' "$DATA_DIR"
    printf 'host=%s\n' "${POSTGRES_HOST:-127.0.0.1}"
    printf 'port=%s\n' "${POSTGRES_PORT:-55433}"
    printf 'database=%s\n' "${POSTGRES_DB:-pg_experiment_workbench}"
    printf 'user=%s\n' "${POSTGRES_USER:-postgres}"
  } > "$WRITE_STATE_TEMP"
  mv -f -- "$WRITE_STATE_TEMP" "$STATE_FILE"
  WRITE_STATE_TEMP=""
}

port_is_unused() {
  local status

  set +e
  "$PG_ISREADY_BIN" \
    -h "${POSTGRES_HOST:-127.0.0.1}" \
    -p "${POSTGRES_PORT:-55433}" \
    -t 1 >/dev/null 2>&1
  status=$?
  set -e

  if ((status == 2)); then
    return 0
  fi

  die "Refusing to start native PostgreSQL: local port ${POSTGRES_PORT:-55433} is already in use"
}

append_native_config() {
  validate_runtime_paths
  {
    printf '\n# Managed by postgres-experiment-workbench native runtime.\n'
    printf "listen_addresses = '127.0.0.1'\n"
    printf 'port = %s\n' "${POSTGRES_PORT:-55433}"
    # TCP loopback avoids platform-specific Unix socket path limits while all
    # persistent runtime state remains inside the repository.
    printf "unix_socket_directories = ''\n"
  } >> "$DATA_DIR/postgresql.conf"
}

initialize_cluster() {
  local password_file="$RUNTIME_ROOT/.initdb-password"
  local temporary_password

  validate_runtime_paths
  if [[ -e "$DATA_DIR" || -L "$DATA_DIR" ]]; then
    die "Refusing existing uninitialized native data path: $DATA_DIR"
  fi

  umask 077
  temporary_password="$(mktemp "$RUNTIME_ROOT/.initdb-password.XXXXXX")"
  printf '%s\n' "${POSTGRES_PASSWORD:-postgres}" > "$temporary_password"
  mv -f -- "$temporary_password" "$password_file"
  if ! "$INITDB_BIN" \
    -D "$DATA_DIR" \
    -U "${POSTGRES_USER:-postgres}" \
    --encoding=UTF8 \
    --locale=C \
    --auth-local=trust \
    --auth-host=scram-sha-256 \
    --pwfile="$password_file"; then
    rm -f -- "$password_file"
    return 1
  fi
  rm -f -- "$password_file"

  append_native_config
  write_state
}

verify_server_identity() {
  local actual_data_dir
  local resolved_actual

  actual_data_dir="$(PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" "$PSQL_BIN" \
    -h "${POSTGRES_HOST:-127.0.0.1}" \
    -p "${POSTGRES_PORT:-55433}" \
    -U "${POSTGRES_USER:-postgres}" \
    -d postgres \
    -AtX \
    -v ON_ERROR_STOP=1 \
    -c 'SHOW data_directory')"

  if [[ ! -d "$actual_data_dir" ]]; then
    die "Native PostgreSQL reported an invalid data directory: $actual_data_dir"
  fi
  resolved_actual="$(cd "$actual_data_dir" && pwd -P)"
  if [[ "$resolved_actual" != "$DATA_DIR" ]]; then
    die "Refusing unexpected PostgreSQL server on port ${POSTGRES_PORT:-55433}: data_directory=$resolved_actual"
  fi
}

wait_for_cluster() {
  local wait_seconds="${PGWORKBENCH_NATIVE_WAIT_SECONDS:-60}"
  local wait_number
  local attempt

  if ! [[ "$wait_seconds" =~ ^[0-9]+$ ]]; then
    die "PGWORKBENCH_NATIVE_WAIT_SECONDS must be between 1 and 300, got: $wait_seconds"
  fi
  wait_number=$((10#$wait_seconds))
  if ((wait_number < 1 || wait_number > 300)); then
    die "PGWORKBENCH_NATIVE_WAIT_SECONDS must be between 1 and 300, got: $wait_seconds"
  fi
  if ! cluster_running; then
    die "Managed native PostgreSQL is not running: $DATA_DIR"
  fi

  for ((attempt = 0; attempt < wait_number; attempt++)); do
    if "$PG_ISREADY_BIN" \
      -h "${POSTGRES_HOST:-127.0.0.1}" \
      -p "${POSTGRES_PORT:-55433}" \
      -U "${POSTGRES_USER:-postgres}" \
      -d postgres >/dev/null 2>&1; then
      verify_server_identity
      return 0
    fi
    sleep 1
  done

  echo "Native PostgreSQL is not ready; see $LOG_FILE" >&2
  return 1
}

ensure_database() {
  local exists

  exists="$(PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" "$PSQL_BIN" \
    -h "${POSTGRES_HOST:-127.0.0.1}" \
    -p "${POSTGRES_PORT:-55433}" \
    -U "${POSTGRES_USER:-postgres}" \
    -d postgres \
    -AtX \
    -v ON_ERROR_STOP=1 \
    -c "SELECT 1 FROM pg_database WHERE datname = '${POSTGRES_DB:-pg_experiment_workbench}'")"
  if [[ "$exists" = "1" ]]; then
    return 0
  fi

  PGPASSWORD="${POSTGRES_PASSWORD:-postgres}" "$CREATEDB_BIN" \
    -h "${POSTGRES_HOST:-127.0.0.1}" \
    -p "${POSTGRES_PORT:-55433}" \
    -U "${POSTGRES_USER:-postgres}" \
    "${POSTGRES_DB:-pg_experiment_workbench}"
}

start_cluster() {
  validate_runtime_paths
  if cluster_running; then
    wait_for_cluster
    ensure_database
    return 0
  fi

  port_is_unused
  "$PG_CTL_BIN" -D "$DATA_DIR" -l "$LOG_FILE" -w start
  if ! wait_for_cluster || ! ensure_database; then
    "$PG_CTL_BIN" -D "$DATA_DIR" -m fast -w stop >/dev/null 2>&1 || true
    return 1
  fi
}

up_topology() {
  set_runtime_paths "$1"
  validate_runtime_paths
  validate_target
  prepare_runtime_dirs
  resolve_setup_binaries

  if cluster_initialized; then
    require_matching_state
  else
    if [[ -e "$DATA_DIR" || -L "$DATA_DIR" || -e "$STATE_FILE" || -L "$STATE_FILE" ]]; then
      die "Refusing incomplete or unmanaged native runtime state: $RUNTIME_ROOT"
    fi
    port_is_unused
    initialize_cluster
  fi

  start_cluster
  printf 'Native PostgreSQL is ready: topology=%s host=%s port=%s database=%s data_dir=%s\n' \
    "$TOPOLOGY_NAME" \
    "${POSTGRES_HOST:-127.0.0.1}" \
    "${POSTGRES_PORT:-55433}" \
    "${POSTGRES_DB:-pg_experiment_workbench}" \
    "$DATA_DIR"
}

down_topology() {
  set_runtime_paths "$1"
  validate_runtime_paths

  if ! cluster_initialized; then
    if [[ -e "$DATA_DIR" || -L "$DATA_DIR" || -e "$STATE_FILE" || -L "$STATE_FILE" ]]; then
      die "Refusing incomplete or unmanaged native runtime state: $RUNTIME_ROOT"
    fi
    printf 'Native PostgreSQL is not initialized: topology=%s\n' "$TOPOLOGY_NAME"
    return 0
  fi

  require_managed_state
  resolve_control_binaries
  if cluster_running; then
    "$PG_CTL_BIN" -D "$DATA_DIR" -m fast -w stop
  fi
  printf 'Native PostgreSQL is stopped: topology=%s data_dir=%s\n' "$TOPOLOGY_NAME" "$DATA_DIR"
}

reset_topology() {
  set_runtime_paths "$1"
  validate_runtime_paths

  if cluster_initialized; then
    require_managed_state
    resolve_control_binaries
    if cluster_running; then
      "$PG_CTL_BIN" -D "$DATA_DIR" -m fast -w stop
    fi
    rm -rf -- "$RUNTIME_ROOT"
  elif [[ -e "$DATA_DIR" || -L "$DATA_DIR" || -e "$STATE_FILE" || -L "$STATE_FILE" ]]; then
    die "Refusing to delete incomplete or unmanaged native runtime state: $RUNTIME_ROOT"
  fi

  up_topology "$TOPOLOGY_NAME"
}

restart_topology() {
  set_runtime_paths "$1"
  validate_runtime_paths
  validate_target
  cluster_initialized || die "Native PostgreSQL is not initialized: $DATA_DIR"
  require_matching_state
  resolve_setup_binaries
  cluster_running || die "Native PostgreSQL is not running; use the up action"

  # Keep the long-lived postmaster detached from the invoking experiment's
  # immutable stdout.log. Without -l, pg_ctl restart lets the new server inherit
  # that descriptor and a later trial's shutdown appends to the previous run.
  "$PG_CTL_BIN" -D "$DATA_DIR" -l "$LOG_FILE" -m fast -w restart
  wait_for_cluster
  ensure_database
  printf 'Native PostgreSQL restarted: topology=%s data_dir=%s\n' "$TOPOLOGY_NAME" "$DATA_DIR"
}

wait_topology() {
  set_runtime_paths "$1"
  validate_runtime_paths
  validate_target
  cluster_initialized || die "Native PostgreSQL is not initialized: $DATA_DIR"
  require_matching_state
  resolve_control_binaries
  resolve_client_binaries
  wait_for_cluster
  printf 'Native PostgreSQL is ready: topology=%s host=%s port=%s database=%s\n' \
    "$TOPOLOGY_NAME" \
    "${POSTGRES_HOST:-127.0.0.1}" \
    "${POSTGRES_PORT:-55433}" \
    "${POSTGRES_DB:-pg_experiment_workbench}"
}

status_topology() {
  set_runtime_paths "$1"
  validate_runtime_paths
  validate_target

  if ! cluster_initialized; then
    if [[ -e "$DATA_DIR" || -L "$DATA_DIR" || -e "$STATE_FILE" || -L "$STATE_FILE" ]]; then
      die "Refusing incomplete or unmanaged native runtime state: $RUNTIME_ROOT"
    fi
    printf 'backend=native\ntopology=%s\nstate=not-initialized\ndata_dir=%s\n' \
      "$TOPOLOGY_NAME" "$DATA_DIR"
    return 0
  fi

  require_matching_state
  resolve_control_binaries
  if ! cluster_running; then
    printf 'backend=native\ntopology=%s\nstate=stopped\ndata_dir=%s\n' \
      "$TOPOLOGY_NAME" "$DATA_DIR"
    return 0
  fi

  resolve_client_binaries
  wait_for_cluster >/dev/null
  printf 'backend=native\ntopology=%s\nstate=running\nhost=%s\nport=%s\ndatabase=%s\ndata_dir=%s\n' \
    "$TOPOLOGY_NAME" \
    "${POSTGRES_HOST:-127.0.0.1}" \
    "${POSTGRES_PORT:-55433}" \
    "${POSTGRES_DB:-pg_experiment_workbench}" \
    "$DATA_DIR"
}

show_topology() {
  set_runtime_paths "$1"
  cat <<EOF
TOPOLOGY_NAME="$TOPOLOGY_NAME"
TOPOLOGY_RUNTIME="native"
TOPOLOGY_DESCRIPTION="One repository-local host PostgreSQL instance."
NATIVE_DATA_DIR="$DATA_DIR"
NATIVE_BINARY_LOOKUP="PGWORKBENCH_NATIVE_BINDIR, PG_INSTALL_DIR/bin, PATH"
EOF
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
    printf 'single\nsource-tree\n'
    ;;
  show)
    load_repo_env
    show_topology "${1:?topology is required}"
    ;;
  up|reset|restart|down|status|wait)
    load_repo_env
    "${ACTION}_topology" "${1:-${TOPOLOGY:-single}}"
    ;;
  *)
    load_repo_env
    up_topology "$ACTION"
    ;;
esac
