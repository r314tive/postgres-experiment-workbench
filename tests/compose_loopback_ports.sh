#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
COMPOSE_FILE="$REPO_DIR/compose.yaml"

expected=(
  '127.0.0.1:${POSTGRES_PORT:-55433}:5432'
  '127.0.0.1:${POSTGRES_REPLICA_PORT:-55434}:5432'
  '127.0.0.1:${POSTGRES_LOGICAL_SUBSCRIBER_PORT:-55435}:5432'
  '127.0.0.1:${PGBOUNCER_PORT:-56432}:5432'
  '127.0.0.1:${POSTGRES_UPGRADE_OLD_PORT:-55436}:5432'
  '127.0.0.1:${POSTGRES_UPGRADE_NEW_PORT:-55437}:5432'
)

mapfile -t published < <(
  awk '
    /^[[:space:]]+ports:[[:space:]]*$/ { in_ports = 1; next }
    in_ports && /^[[:space:]]+-[[:space:]]+/ {
      value = $0
      sub(/^[[:space:]]+-[[:space:]]+/, "", value)
      gsub(/^"|"$/, "", value)
      print value
      next
    }
    in_ports { in_ports = 0 }
  ' "$COMPOSE_FILE"
)

if (( ${#published[@]} != ${#expected[@]} )); then
  echo "FAIL: expected ${#expected[@]} published Compose ports, found ${#published[@]}" >&2
  printf '  %s\n' "${published[@]}" >&2
  exit 1
fi

for i in "${!expected[@]}"; do
  if [[ "${published[$i]}" != "${expected[$i]}" ]]; then
    echo "FAIL: Compose port is not loopback-only: ${published[$i]}" >&2
    exit 1
  fi
done

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  rendered="$(docker compose --profile '*' --env-file "$REPO_DIR/.env.example" -f "$COMPOSE_FILE" config --format json)"
  if ! jq -e '
    [
      .services[]
      | (.ports // [])[]
      | select(.published != null)
    ] as $ports
    | ($ports | length) == 6
      and all($ports[]; .host_ip == "127.0.0.1")
  ' <<< "$rendered" >/dev/null; then
    echo "FAIL: rendered Compose ports are not all bound to 127.0.0.1" >&2
    exit 1
  fi
fi

echo "PASS: Docker Compose published ports are loopback-only"
