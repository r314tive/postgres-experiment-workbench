#!/usr/bin/env bash

# Append-only benchmark phase journal helpers shared by the experiment and
# pgbench adapters. The Go producer creates the file and independently parses
# it after the child exits; these helpers never decide benchmark validity.

benchmark_phase_now() {
  local epoch="${EPOCHREALTIME:-}" seconds fraction generated

  if [[ "$epoch" =~ ^([0-9]+)\.([0-9]{1,9})$ ]]; then
    seconds="${BASH_REMATCH[1]}"
    fraction="${BASH_REMATCH[2]}"
    LC_ALL=C TZ=UTC0 printf '%(%Y-%m-%dT%H:%M:%S)T.%sZ\n' "$seconds" "$fraction"
    return
  fi

  # GNU date is a fallback for Bash 4 environments. BSD date prints a literal
  # N for %N, so validate the result instead of silently degrading to seconds.
  generated="$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" || return
  if [[ "$generated" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{1,9}Z$ ]]; then
    printf '%s\n' "$generated"
    return
  fi
  echo "A high-resolution UTC clock is required for benchmark phase evidence" >&2
  return 2
}

benchmark_phase_timestamp_valid() {
  [[ "${1:-}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$ ]]
}

benchmark_phase_timestamp_key() {
  local value="${1:?benchmark phase timestamp is required}"
  local base="${value%Z}" fraction=""

  if [[ "$base" = *.* ]]; then
    fraction="${base#*.}"
    base="${base%%.*}"
  fi
  while (( ${#fraction} < 9 )); do
    fraction="${fraction}0"
  done
  printf '%s.%s\n' "$base" "$fraction"
}

benchmark_phase_binding_valid() {
  local run_id="${PGWORKBENCH_BENCHMARK_RUN_ID:-}"
  local trial="${PGWORKBENCH_BENCHMARK_TRIAL:-}"

  [[ "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$ && "$trial" =~ ^[1-9][0-9]*$ ]]
}

benchmark_phase_file_valid() {
  local path="${1:-}"
  [[ -n "$path" && "$path" = /* && ! -L "$path" && -f "$path" ]]
}

benchmark_phase_append() {
  local sequence="${1:?phase sequence is required}"
  local name="${2:?phase name is required}"
  local status="${3:?phase status is required}"
  local started_at="${4:?phase started_at is required}"
  local finished_at="${5:?phase finished_at is required}"
  local reason="${6:-}"
  local journal="${PGWORKBENCH_BENCHMARK_PHASE_FILE:-}"
  local mirror="${PGWORKBENCH_BENCHMARK_PHASE_MIRROR_FILE:-}"
  local run_id="${PGWORKBENCH_BENCHMARK_RUN_ID:-}"
  local trial="${PGWORKBENCH_BENCHMARK_TRIAL:-}"
  local row

  [[ -n "$journal" ]] || return 0
  if ! benchmark_phase_file_valid "$journal" ||
     { [[ -n "$mirror" ]] && { ! benchmark_phase_file_valid "$mirror" || [[ "$mirror" = "$journal" ]]; }; }; then
    echo "Unsafe or missing benchmark phase journal: $journal" >&2
    return 2
  fi
  if benchmark_phase_binding_valid &&
     [[ "$sequence" =~ ^([1-9]|1[01])$ && "$name" = "$(benchmark_phase_name "$sequence")" && "$status" =~ ^(passed|failed|skipped)$ ]] &&
     benchmark_phase_timestamp_valid "$started_at" && benchmark_phase_timestamp_valid "$finished_at" &&
     [[ "$reason" != *$'\t'* && "$reason" != *$'\n'* && "$reason" != *$'\r'* ]] &&
     [[ "$status" = "passed" || -n "$reason" ]] &&
     [[ "$name" != "cleanup" || "$status" != "skipped" ]]; then
    printf -v row '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$run_id" "$trial" "$sequence" "$name" "$status" "$started_at" "$finished_at" "$reason"
    printf '%s' "$row" >> "$journal"
    if [[ -n "$mirror" ]]; then
      printf '%s' "$row" >> "$mirror"
    fi
    return
  fi
  echo "Invalid benchmark phase journal event: $sequence/$name/$status" >&2
  return 2
}

benchmark_phase_first_failure_name() {
  local journal="${PGWORKBENCH_BENCHMARK_PHASE_FILE:-}"

  [[ -n "$journal" ]] || return 1
  if ! benchmark_phase_file_valid "$journal"; then
    echo "Unsafe or missing benchmark phase journal: $journal" >&2
    return 2
  fi
  awk -F '\t' '$5 == "failed" { print $4; found = 1; exit } END { if (!found) exit 1 }' "$journal"
}

benchmark_phase_first_failure_name_or_empty() {
  local failed_name status

  if failed_name="$(benchmark_phase_first_failure_name)"; then
    printf '%s\n' "$failed_name"
    return
  fi
  status="$?"
  if [[ "$status" = "1" ]]; then
    return 0
  fi
  return "$status"
}

benchmark_phase_name() {
  case "${1:-}" in
    1) printf '%s\n' preflight ;;
    2) printf '%s\n' prepare ;;
    3) printf '%s\n' stabilize ;;
    4) printf '%s\n' pre-warmup-control ;;
    5) printf '%s\n' warmup ;;
    6) printf '%s\n' pre-measure-control ;;
    7) printf '%s\n' measure ;;
    8) printf '%s\n' cooldown ;;
    9) printf '%s\n' validate ;;
    10) printf '%s\n' collect ;;
    11) printf '%s\n' cleanup ;;
    *) return 2 ;;
  esac
}

# Complete phases 1-10 after an early shell exit. The first phase that was not
# recorded is failed unless the preceding phase already failed; all later
# phases are explicitly skipped. This keeps aborted trials independently
# parseable without pretending that an unentered phase ran.
benchmark_phase_complete_before_cleanup() {
  local runner_exit="${1:-1}"
  local journal="${PGWORKBENCH_BENCHMARK_PHASE_FILE:-}"
  local last_line row_run_id row_trial sequence name status started_at finished_at reason
  local next point event_started next_name first_status first_reason failed_name failure_status

  BENCHMARK_PHASE_BACKFILLED_FAILURE=0
  [[ -n "$journal" ]] || return 0
  if ! benchmark_phase_file_valid "$journal" || ! benchmark_phase_binding_valid; then
    echo "Unsafe or missing benchmark phase journal: $journal" >&2
    return 2
  fi

  if [[ -s "$journal" ]]; then
    last_line="$(tail -n 1 -- "$journal")"
    IFS=$'\t' read -r row_run_id row_trial sequence name status started_at finished_at reason <<< "$last_line"
    if [[ "$row_run_id" != "$PGWORKBENCH_BENCHMARK_RUN_ID" || "$row_trial" != "$PGWORKBENCH_BENCHMARK_TRIAL" ]]; then
      echo "Invalid terminal benchmark phase journal binding" >&2
      return 2
    fi
    if [[ ! "$sequence" =~ ^([1-9]|10)$ ]] || [[ "$name" != "$(benchmark_phase_name "$sequence")" ]] ||
       [[ ! "$status" =~ ^(passed|failed|skipped)$ ]] ||
       ! benchmark_phase_timestamp_valid "$finished_at" ||
       [[ "$status" != "passed" && -z "$reason" ]]; then
      echo "Invalid terminal benchmark phase journal state" >&2
      return 2
    fi
    next=$((sequence + 1))
  else
    # The shell entrypoint owns preflight. A failure before it can publish the
    # first event is still a complete lifecycle: preflight fails and every
    # later non-cleanup phase is explicitly skipped.
    next=1
    finished_at="$(benchmark_phase_now)"
  fi

  (( next <= 10 )) || return 0
  point="$(benchmark_phase_now)"
  if [[ "$(benchmark_phase_timestamp_key "$point")" < "$(benchmark_phase_timestamp_key "$finished_at")" ]]; then
    point="$finished_at"
  fi
  event_started="$point"
  if [[ "$next" = "1" ]] && benchmark_phase_timestamp_valid "${BENCHMARK_PREFLIGHT_STARTED_AT:-}" &&
     [[ "$(benchmark_phase_timestamp_key "$BENCHMARK_PREFLIGHT_STARTED_AT")" < "$(benchmark_phase_timestamp_key "$point")" ]]; then
    event_started="$BENCHMARK_PREFLIGHT_STARTED_AT"
  fi
  first_status=failed
  first_reason="runner exited during phase (exit $runner_exit)"
  failed_name=""
  if failed_name="$(benchmark_phase_first_failure_name)"; then
    first_status=skipped
    first_reason="not reached after failed $failed_name phase"
  else
    failure_status="$?"
    if [[ "$failure_status" != "1" ]]; then
      return "$failure_status"
    fi
    # Read by the experiment runner after this helper returns.
    # shellcheck disable=SC2034
    BENCHMARK_PHASE_BACKFILLED_FAILURE=1
  fi

  while (( next <= 10 )); do
    next_name="$(benchmark_phase_name "$next")"
    benchmark_phase_append "$next" "$next_name" "$first_status" "$event_started" "$point" "$first_reason" || return
    event_started="$point"
    first_status=skipped
    first_reason="not reached after earlier benchmark phase failure"
    next=$((next + 1))
  done
}
