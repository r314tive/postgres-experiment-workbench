#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIAGNOSTICS_DIR="$REPO_DIR/sql/diagnostics"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_diagnostic.sh list
  scripts/run_diagnostic.sh show <diagnostic>
  scripts/run_diagnostic.sh run <diagnostic>
  scripts/run_diagnostic.sh <diagnostic>

Diagnostics are read-only SQL files under sql/diagnostics/*.sql.
USAGE
}

list_diagnostics() {
  find "$DIAGNOSTICS_DIR" -maxdepth 1 -type f -name '*.sql' 2>/dev/null | sort | while read -r path; do
    path="${path#"$DIAGNOSTICS_DIR/"}"
    printf '%s\n' "${path%.sql}"
  done
}

resolve_diagnostic() {
  local input="${1:?diagnostic is required}"
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

  candidate="$DIAGNOSTICS_DIR/$input"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  candidate="$DIAGNOSTICS_DIR/$input.sql"
  if [[ -f "$candidate" ]]; then
    realpath "$candidate"
    return 0
  fi

  echo "Diagnostic not found: $input" >&2
  exit 1
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
    list_diagnostics
    ;;
  show)
    sed -n '1,220p' "$(resolve_diagnostic "${1:?diagnostic is required}")"
    ;;
  run)
    "$REPO_DIR/scripts/psql.sh" -f "$(resolve_diagnostic "${1:?diagnostic is required}")"
    ;;
  *)
    "$REPO_DIR/scripts/psql.sh" -f "$(resolve_diagnostic "$ACTION")"
    ;;
esac
