#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
RUNTIME="${PGWORKBENCH_RUNTIME:-docker}"

usage() {
  cat <<'USAGE'
Usage:
  PGWORKBENCH_RUNTIME=docker|native scripts/runtime.sh <action> [topology]

Actions are delegated to the selected runtime backend. Both backends support:
  list, show, up, reset, restart, down, status, wait

The native backend is limited to the single and source-tree topologies.
USAGE
}

if [[ "${1:-}" = "help" || "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
  exit 0
fi

case "$RUNTIME" in
  docker)
    if [[ "${1:-}" = "restart" ]]; then
      topology="${2:-${TOPOLOGY:-single}}"
      "$REPO_DIR/scripts/topology.sh" down "$topology"
      exec "$REPO_DIR/scripts/topology.sh" up "$topology"
    fi
    exec "$REPO_DIR/scripts/topology.sh" "$@"
    ;;
  native)
    exec "$REPO_DIR/scripts/native_runtime.sh" "$@"
    ;;
  *)
    echo "Unsupported PGWORKBENCH_RUNTIME: $RUNTIME (expected docker or native)" >&2
    exit 2
    ;;
esac
