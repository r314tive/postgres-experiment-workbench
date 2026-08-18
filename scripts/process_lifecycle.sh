#!/usr/bin/env bash

# Shared bounded child-process helpers. Owned commands stay in the experiment
# runner's process group, so the Go parent remains the final containment layer.
# A fresh Bash supervisor holds the command as an unreaped job and accepts an
# atomically created fixed-path directory token; callers never signal a PID
# after Bash has reaped it.

readonly _PGWORKBENCH_LIFECYCLE_BASH="${BASH:?Bash interpreter path is required}"
PGWORKBENCH_OWNED_CLEANUP_DEADLINE=""

pgworkbench_start_owned_process() {
  local _pgworkbench_output_name="${1:?output variable is required}"
  local _pgworkbench_stop_path="${2:?stop token path is required}"
  local _pgworkbench_output_log="${3:?output log is required}"
  local _pgworkbench_supervisor_pid _pgworkbench_output_declaration
  shift 3

  case "$-" in
    *m*)
      echo "Owned processes require caller job control to be disabled" >&2
      return 2
      ;;
  esac

  [[ "$_pgworkbench_output_name" =~ ^[A-Za-z_][A-Za-z0-9_]*(_PID|_pid)$ &&
     "$_pgworkbench_output_name" != _pgworkbench_* &&
     "$_pgworkbench_output_name" != PGWORKBENCH_* &&
     "$_pgworkbench_output_name" != _PGWORKBENCH_* ]] || {
    echo "Invalid or reserved owned-process output variable: $_pgworkbench_output_name" >&2
    return 2
  }
  _pgworkbench_output_declaration="$(builtin declare -p "$_pgworkbench_output_name" 2>/dev/null)" || {
    echo "Owned-process output variable must already exist and be empty: $_pgworkbench_output_name" >&2
    return 2
  }
  # Loading env/spec files under `set -a` may leave a preserved empty lifecycle
  # variable exported. It is still a writable scalar, but must not leak the
  # eventual supervisor PID into later experiment children. Normalize that one
  # safe shape before forking; readonly, array, integer, and non-empty variables
  # remain fail-closed.
  if [[ "$_pgworkbench_output_declaration" = "declare -x $_pgworkbench_output_name=\"\"" ]]; then
    builtin export -n "${_pgworkbench_output_name?}"
    _pgworkbench_output_declaration="$(builtin declare -p "$_pgworkbench_output_name" 2>/dev/null)" || return 2
  fi
  if [[ "$_pgworkbench_output_declaration" != "declare -- $_pgworkbench_output_name=\"\"" ]]; then
    echo "Owned-process output variable must be a writable empty scalar: $_pgworkbench_output_name" >&2
    return 2
  fi
  [[ "$_pgworkbench_stop_path" = /* && "$_pgworkbench_stop_path" != *$'\n'* &&
     "$_pgworkbench_stop_path" != *$'\r'* ]] || {
    echo "Owned-process stop token must be an absolute one-line path" >&2
    return 2
  }
  [[ ! -e "$_pgworkbench_stop_path" && ! -L "$_pgworkbench_stop_path" ]] || {
    echo "Refusing an existing owned-process stop token: $_pgworkbench_stop_path" >&2
    return 2
  }
  (( $# > 0 )) || {
    echo "Owned process requires a command" >&2
    return 2
  }

  # The supervisor program is intentionally literal. A fresh Bash does not
  # inherit the runner's EXIT/signal traps; command/builtin bypass imported
  # functions while retaining the exact experiment environment for its child.
  # shellcheck disable=SC2016
  BASH_ENV='' ENV='' "$_PGWORKBENCH_LIFECYCLE_BASH" --noprofile --norc -p -c '
    set -uo pipefail
    set +m
    stop_file="$1"
    shift
    child_status=0
    stop_requested=0
    stop_signal_sent=0
    outer_grace="${PGWORKBENCH_CLEANUP_GRACE_SECONDS:-15}"
    [[ "$outer_grace" =~ ^[1-9][0-9]*$ ]] || outer_grace=15
    grace=$((outer_grace - 4))
    if (( grace < 1 )); then grace=1; fi
    if (( outer_grace <= 4 )); then
      poll_delay=0.025
      grace_polls=20
    else
      poll_delay=0.05
      grace_polls=$((grace * 20))
    fi

    "$@" &
    child_pid="$!"
    while [[ "$(builtin jobs -pr)" = "$child_pid" ]]; do
      if [[ -d "$stop_file" && ! -L "$stop_file" ]]; then
        stop_requested=1
        if builtin kill -TERM %1 >/dev/null 2>&1; then
          stop_signal_sent=1
        fi
        break
      fi
      command sleep "$poll_delay"
    done
    if [[ "$stop_requested" = "1" ]]; then
      remaining_polls="$grace_polls"
      while [[ "$(builtin jobs -pr)" = "$child_pid" ]]; do
        if (( remaining_polls <= 0 )); then
          builtin kill -KILL %1 >/dev/null 2>&1 || true
          break
        fi
        remaining_polls=$((remaining_polls - 1))
        command sleep "$poll_delay"
      done
    fi
    builtin wait "$child_pid" >/dev/null 2>&1 || child_status="$?"
    # Only the supervisor that successfully delivered this cleanup signal may
    # classify the resulting direct SIGTERM as an expected stop. A caller that
    # merely created the token cannot safely infer that causality afterwards.
    if [[ "$stop_signal_sent" = "1" && "$child_status" = "143" ]]; then
      child_status=0
    fi
    exit "$child_status"
  ' pgworkbench-owned-supervisor "$_pgworkbench_stop_path" "$@" >"$_pgworkbench_output_log" 2>&1 &
  _pgworkbench_supervisor_pid="$!"
  printf -v "$_pgworkbench_output_name" '%s' "$_pgworkbench_supervisor_pid"
}

pgworkbench_owned_process_running() {
  local pid="${1:?owned supervisor PID is required}"
  local running_jobs running_pid
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 2
  running_jobs="$(builtin jobs -pr)"
  for running_pid in $running_jobs; do
    if [[ "$running_pid" = "$pid" ]]; then
      return 0
    fi
  done
  return 1
}

pgworkbench_request_owned_stop() {
  local stop_file="${1:?stop token path is required}"
  [[ "$stop_file" = /* && "$stop_file" != *$'\n'* && "$stop_file" != *$'\r'* ]] || return 2
  if [[ -L "$stop_file" ]]; then
    echo "Owned-process stop token became a symlink: $stop_file" >&2
    return 2
  fi
  if [[ -d "$stop_file" ]]; then
    return 0
  fi
  if [[ -e "$stop_file" ]]; then
    echo "Owned-process stop token has an unexpected file type: $stop_file" >&2
    return 2
  fi
  # Publish the token with its final mode in mkdir(2). BSD `mkdir -m` may expose
  # the path before a trailing chmod; a fast supervisor/cleanup pair could then
  # consume it while its creator still has a path-based mutation in flight.
  if (umask 077; command mkdir -- "$stop_file") 2>/dev/null; then
    return 0
  fi
  [[ -d "$stop_file" && ! -L "$stop_file" ]] || return 2
}

pgworkbench_remove_owned_stop_token() {
  local stop_file="${1:?stop token path is required}"
  if [[ -L "$stop_file" ]]; then
    echo "Owned-process stop token became a symlink: $stop_file" >&2
    return 2
  fi
  if [[ ! -e "$stop_file" ]]; then
    return 0
  fi
  if [[ ! -d "$stop_file" ]]; then
    echo "Owned-process stop token has an unexpected file type: $stop_file" >&2
    return 2
  fi
  if ! command rmdir -- "$stop_file" 2>/dev/null; then
    echo "Owned-process stop token is not an empty owned directory: $stop_file" >&2
    return 2
  fi
}

pgworkbench_begin_owned_cleanup() {
  local grace="${PGWORKBENCH_CLEANUP_GRACE_SECONDS:-15}"
  local budget
  [[ "$grace" =~ ^[1-9][0-9]*$ ]] || grace=15
  budget=$((grace - 2))
  if (( budget < 1 )); then
    budget=1
  fi
  # Bash's SECONDS has one-second resolution and may already be near the next
  # tick. Add one boundary second so a nominal one-second cleanup budget never
  # collapses to only a few milliseconds.
  PGWORKBENCH_OWNED_CLEANUP_DEADLINE=$((SECONDS + budget + 1))
}

pgworkbench_wait_after_stop_request() {
  local pid="${1:?owned supervisor PID is required}"
  local stop_file="${2:?stop token path is required}"
  local status=0
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 2
  if [[ -z "$PGWORKBENCH_OWNED_CLEANUP_DEADLINE" ]]; then
    pgworkbench_begin_owned_cleanup
  fi
  pgworkbench_request_owned_stop "$stop_file" || return
  while pgworkbench_owned_process_running "$pid"; do
    if (( SECONDS >= PGWORKBENCH_OWNED_CLEANUP_DEADLINE )); then
      echo "Owned process did not stop within the shared cleanup budget: $pid" >&2
      return 124
    fi
    sleep 0.05
  done
  wait "$pid" >/dev/null 2>&1 || status="$?"
  pgworkbench_remove_owned_stop_token "$stop_file" || return
  return "$status"
}

pgworkbench_wait_owned_process() {
  local pid="${1:?owned supervisor PID is required}"
  local stop_file="${2:?stop token path is required}"
  local status=0
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 2
  wait "$pid" >/dev/null 2>&1 || status="$?"
  pgworkbench_remove_owned_stop_token "$stop_file" || return
  return "$status"
}

pgworkbench_metrics_evidence_ready() {
  local path="${1:?metrics evidence path is required}"
  local ready_file="${2:?metrics ready token is required}"

  if [[ -L "$path" || -L "$ready_file" ]]; then
    return 2
  fi
  [[ -f "$path" ]] || return 1
  if [[ -d "$ready_file" ]]; then
    return 0
  fi
  [[ ! -e "$ready_file" ]] || return 2
  return 1
}

pgworkbench_consume_metrics_ready_token() {
  local path="${1:?metrics evidence path is required}"
  local ready_file="${2:?metrics ready token is required}"
  local ready_status=0

  pgworkbench_metrics_evidence_ready "$path" "$ready_file" || ready_status="$?"
  if [[ "$ready_status" != "0" ]]; then
    return "$ready_status"
  fi
  if ! command rmdir -- "$ready_file" 2>/dev/null; then
    echo "Metrics ready token is not an empty owned directory" >&2
    return 2
  fi
}

pgworkbench_wait_for_metrics_ready() {
  local pid="${1:?metrics supervisor PID is required}"
  local path="${2:?metrics evidence path is required}"
  local ready_file="${3:?metrics ready token is required}"
  local stop_file="${4:?metrics stop token path is required}"
  local ready_seconds="${5:-45}"
  local deadline ready_status child_status=0

  if [[ ! "$pid" =~ ^[1-9][0-9]*$ || ! "$ready_seconds" =~ ^[1-9][0-9]*$ ]]; then
    echo "Metrics readiness requires a canonical PID and positive timeout" >&2
    return 2
  fi
  # SECONDS is integer-valued and may be just about to tick. One boundary
  # second guarantees at least the declared readiness interval.
  deadline=$((SECONDS + ready_seconds + 1))

  while true; do
    ready_status=0
    pgworkbench_consume_metrics_ready_token "$path" "$ready_file" || ready_status="$?"
    if [[ "$ready_status" = "0" ]]; then
      return 0
    fi
    if [[ "$ready_status" = "2" ]]; then
      echo "Metrics evidence or ready-token path has an unexpected type" >&2
      pgworkbench_begin_owned_cleanup
      pgworkbench_wait_after_stop_request "$pid" "$stop_file" || true
      return 2
    fi
    if ! pgworkbench_owned_process_running "$pid"; then
      # A fixed-sample collector may publish the token and exit between the
      # first readiness probe and this job-table observation. Recheck before
      # reaping; a valid token is causally sufficient, while the cached child
      # status remains available to stop_metrics/wait_owned_process.
      ready_status=0
      pgworkbench_consume_metrics_ready_token "$path" "$ready_file" || ready_status="$?"
      if [[ "$ready_status" = "0" ]]; then
        return 0
      fi
      wait "$pid" >/dev/null 2>&1 || child_status="$?"
      if [[ "$ready_status" = "2" ]]; then
        pgworkbench_remove_owned_stop_token "$stop_file" || true
        echo "Metrics evidence or ready-token path has an unexpected type" >&2
        return 2
      fi
      if [[ -d "$ready_file" && ! -L "$ready_file" ]]; then
        command rmdir -- "$ready_file" 2>/dev/null || true
      fi
      pgworkbench_remove_owned_stop_token "$stop_file" || true
      echo "Metrics collector exited before publishing its first complete sample (status=$child_status)" >&2
      if [[ "$child_status" = "0" ]]; then
        return 1
      fi
      return "$child_status"
    fi
    if (( SECONDS >= deadline )); then
      pgworkbench_begin_owned_cleanup
      pgworkbench_wait_after_stop_request "$pid" "$stop_file" || child_status="$?"
      if [[ -d "$ready_file" && ! -L "$ready_file" ]]; then
        command rmdir -- "$ready_file" 2>/dev/null || true
      fi
      echo "Metrics collector did not publish its first complete sample within ${ready_seconds}s (status=$child_status)" >&2
      return 124
    fi
    sleep 0.05
  done
}
