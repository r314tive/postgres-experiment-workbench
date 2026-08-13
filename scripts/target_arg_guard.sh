#!/usr/bin/env bash

# Sourceable helpers for keeping adapter-specific extra arguments inside the
# experiment-owned PostgreSQL target. The split matches the adapters, so both
# separate and attached short-option forms are covered.

pgworkbench_pgbench_args_change_target() {
  local raw="${1:-}"
  local word short option

  # Bash 3.2 with nounset treats an explicitly declared empty array as
  # unbound. Return before declaring/expanding it when there are no words.
  if [[ -z "$raw" || ! "$raw" =~ [^[:space:]] ]]; then
    return 1
  fi
  local -a words
  read -r -a words <<< "$raw"

  for word in "${words[@]}"; do
    case "$word" in
      --|--host|--host=*|--port|--port=*|--username|--username=*|--dbname|--dbname=*|--database|--database=*)
        return 0
        ;;
      --*)
        continue
        ;;
      -?*)
        short="${word#-}"
        while [[ -n "$short" ]]; do
          option="${short:0:1}"
          short="${short:1}"
          case "$option" in
            h|p|U|d)
              return 0
              ;;
            # These safe pgbench options consume the rest of this token (or
            # the next token), so later characters are values, not a cluster.
            I|F|s|b|f|c|D|j|L|M|P|R|t|T)
              break
              ;;
          esac
        done
        ;;
    esac
  done

  return 1
}

pgworkbench_noisia_args_change_target() {
  local raw="${1:-}"
  local word

  if [[ -z "$raw" || ! "$raw" =~ [^[:space:]] ]]; then
    return 1
  fi
  local -a words
  read -r -a words <<< "$raw"
  for word in "${words[@]}"; do
    case "$word" in
      --|--conninfo|--conninfo=*)
        return 0
        ;;
    esac
  done
  return 1
}

pgworkbench_reject_experiment_target_args() {
  if pgworkbench_pgbench_args_change_target "${PGBENCH_EXTRA_ARGS:-}"; then
    echo "Experiment runs reject target-changing PGBENCH_EXTRA_ARGS" >&2
    return 2
  fi
  if pgworkbench_noisia_args_change_target "${NOISIA_EXTRA_ARGS:-}"; then
    echo "Experiment runs reject target-changing NOISIA_EXTRA_ARGS" >&2
    return 2
  fi
}
