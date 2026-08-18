#!/usr/bin/env bash

# The Go experiment runner owns this marker. Shell entrypoints default to the
# compatibility environment when invoked directly, reject malformed inherited
# values, and make the selected boundary immutable before sourcing any env/spec
# file. Readonly state does not cross exec(2), so every env-sourcing descendant
# initializes the marker again.
pgworkbench_initialize_exact_environment() {
  local marker="${PGWORKBENCH_EXACT_ENVIRONMENT:-0}"

  case "$marker" in
    0|1)
      ;;
    *)
      echo "PGWORKBENCH_EXACT_ENVIRONMENT must be 0 or 1: $marker" >&2
      return 2
      ;;
  esac

  PGWORKBENCH_EXACT_ENVIRONMENT="$marker"
  export PGWORKBENCH_EXACT_ENVIRONMENT
  readonly PGWORKBENCH_EXACT_ENVIRONMENT
}

pgworkbench_exact_environment_active() {
  [[ "$PGWORKBENCH_EXACT_ENVIRONMENT" = "1" ]]
}
