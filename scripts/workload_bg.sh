#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="$REPO_DIR/.tmp/workloads"
PID_FILE="$STATE_DIR/current.pid"
LOG_FILE_LINK="$STATE_DIR/current.log"
CMD_FILE="$STATE_DIR/current.cmd"
LOG_DIR="${WORKLOAD_LOG_DIR:-$REPO_DIR/logs/workloads}"

usage() {
  cat <<'USAGE'
Usage:
  scripts/workload_bg.sh start-profile <profile> [sql-file]
  scripts/workload_bg.sh start-spec <workload-spec>
  scripts/workload_bg.sh start-sql <sql-file>
  scripts/workload_bg.sh start-noisia <workload> [extra noisia args...]
  scripts/workload_bg.sh status
  scripts/workload_bg.sh log
  scripts/workload_bg.sh wait
  scripts/workload_bg.sh stop

Environment:
  PROFILE_SIZE=small
  PROFILE_SECONDS=30
  WORKLOAD_LOG_DIR=logs/workloads
  WORKLOAD_STOP_TIMEOUT=10
  WORKLOAD_LOG_LINES=80
USAGE
}

mkdir -p "$STATE_DIR" "$LOG_DIR"

is_running() {
  [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" >/dev/null 2>&1
}

current_pid() {
  [[ -f "$PID_FILE" ]] && cat "$PID_FILE"
}

current_log() {
  [[ -f "$LOG_FILE_LINK" ]] && cat "$LOG_FILE_LINK"
}

require_idle() {
  if is_running; then
    echo "Background workload already running: pid=$(current_pid)" >&2
    echo "Use scripts/workload_bg.sh status|log|stop." >&2
    exit 2
  fi
}

write_state() {
  local pid="$1"
  local log_file="$2"
  shift 2

  printf '%s\n' "$pid" > "$PID_FILE"
  printf '%s\n' "$log_file" > "$LOG_FILE_LINK"
  printf '%q ' "$@" > "$CMD_FILE"
  printf '\n' >> "$CMD_FILE"
}

timestamp() {
  date -u +%Y%m%d_%H%M%S
}

start_profile() {
  local profile="${1:?profile is required}"
  local sql_name="${2:-10_run.sql}"
  local safe_sql_name="${sql_name//\//_}"
  local log_file="$LOG_DIR/profile-${profile}-${safe_sql_name}.$(timestamp).log"

  require_idle

  (
    cd "$REPO_DIR"
    PROFILE_SIZE="${PROFILE_SIZE:-small}" \
    PROFILE_SECONDS="${PROFILE_SECONDS:-30}" \
      "$REPO_DIR/scripts/run_profile_sql.sh" "$profile" "$sql_name"
  ) > "$log_file" 2>&1 &

  write_state "$!" "$log_file" run-profile "$profile" "$sql_name"
  echo "Started profile workload: pid=$(current_pid) log=$log_file"
}

start_spec() {
  local spec="${1:?workload-spec is required}"
  local safe_spec="${spec//\//_}"
  local log_file="$LOG_DIR/spec-${safe_spec}.$(timestamp).log"

  require_idle

  (
    cd "$REPO_DIR"
    WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run "$spec"
  ) > "$log_file" 2>&1 &

  write_state "$!" "$log_file" run-workload "$spec"
  echo "Started workload spec: pid=$(current_pid) log=$log_file"
}

start_sql() {
  local sql_file="${1:?sql-file is required}"
  local safe_sql_name
  safe_sql_name="$(basename "$sql_file")"
  local log_file="$LOG_DIR/sql-${safe_sql_name}.$(timestamp).log"

  require_idle

  (
    cd "$REPO_DIR"
    "$REPO_DIR/scripts/run_sql_logged.sh" "$sql_file" "$log_file"
  ) > "$log_file.stdout" 2>&1 &

  write_state "$!" "$log_file" run-sql "$sql_file"
  echo "Started SQL workload: pid=$(current_pid) log=$log_file"
}

start_noisia() {
  local workload="${1:?workload is required}"
  shift || true
  local log_file="$LOG_DIR/noisia-${workload}.$(timestamp).log"

  require_idle

  (
    cd "$REPO_DIR"
    "$REPO_DIR/scripts/run_noisia.sh" "$workload" "$@"
  ) > "$log_file" 2>&1 &

  write_state "$!" "$log_file" run-noisia "$workload" "$@"
  echo "Started noisia workload: pid=$(current_pid) log=$log_file"
}

status_workload() {
  if is_running; then
    echo "running pid=$(current_pid)"
  elif [[ -f "$PID_FILE" ]]; then
    echo "stopped pid=$(current_pid)"
  else
    echo "not running"
  fi

  if [[ -f "$CMD_FILE" ]]; then
    printf 'command='
    cat "$CMD_FILE"
  fi

  if [[ -f "$LOG_FILE_LINK" ]]; then
    echo "log=$(current_log)"
  fi
}

show_log() {
  local log_file
  log_file="$(current_log || true)"

  if [[ -z "$log_file" || ! -f "$log_file" ]]; then
    echo "No background workload log found." >&2
    exit 1
  fi

  tail -n "${WORKLOAD_LOG_LINES:-80}" "$log_file"
}

wait_workload() {
  if ! [[ -f "$PID_FILE" ]]; then
    echo "No background workload to wait for." >&2
    exit 1
  fi

  while is_running; do
    sleep 1
  done

  status_workload
}

stop_workload() {
  local timeout="${WORKLOAD_STOP_TIMEOUT:-10}"

  if ! is_running; then
    status_workload
    exit 0
  fi

  local pid
  pid="$(current_pid)"
  kill "$pid" >/dev/null 2>&1 || true

  for ((i = 0; i < timeout; i++)); do
    if ! kill -0 "$pid" >/dev/null 2>&1; then
      status_workload
      exit 0
    fi
    sleep 1
  done

  kill -KILL "$pid" >/dev/null 2>&1 || true
  status_workload
}

ACTION="${1:-help}"
if [[ $# -gt 0 ]]; then
  shift
fi

case "$ACTION" in
  start-profile)
    start_profile "$@"
    ;;
  start-spec)
    start_spec "$@"
    ;;
  start-sql)
    start_sql "$@"
    ;;
  start-noisia)
    start_noisia "$@"
    ;;
  status)
    status_workload
    ;;
  log)
    show_log
    ;;
  wait)
    wait_workload
    ;;
  stop)
    stop_workload
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    usage >&2
    echo "Unknown action: $ACTION" >&2
    exit 2
    ;;
esac
