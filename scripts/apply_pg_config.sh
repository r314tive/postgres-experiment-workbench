#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_NAME="${1:-${PG_CONFIG:-default}}"
CONFIG_FILE="$REPO_DIR/configs/$CONFIG_NAME/postgresql.conf"
TOPOLOGY_NAME="${TOPOLOGY:-single}"
# shellcheck source=benchmark_capsule.sh
source "$REPO_DIR/scripts/benchmark_capsule.sh"

if benchmark_capsule_active; then
  if [[ "${PGWORKBENCH_BENCHMARK_PG_CONFIG_ID:-}" != "$CONFIG_NAME" ]]; then
    echo "Benchmark capsule PostgreSQL config id mismatch" >&2
    exit 2
  fi
  CONFIG_FILE="$(benchmark_capsule_resolve \
    "configs/$CONFIG_NAME/postgresql.conf" \
    "${PGWORKBENCH_BENCHMARK_PG_CONFIG_DIGEST:-}")"
fi

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "PostgreSQL config profile not found: $CONFIG_FILE" >&2
  exit 1
fi

if [[ "$TOPOLOGY_NAME" = "multi-version-upgrade" ]]; then
  if [[ "$CONFIG_NAME" != "default" ]]; then
    echo "PostgreSQL config profiles are unsupported for multi-version-upgrade" >&2
    exit 2
  fi
  echo "Skipping ALTER SYSTEM reset for independently configured upgrade servers."
  exit 0
fi

echo "Resetting PostgreSQL ALTER SYSTEM settings before profile apply."
"$REPO_DIR/scripts/psql.sh" -c 'ALTER SYSTEM RESET ALL'

if [[ "$CONFIG_NAME" != "default" ]]; then
  echo "Applying PostgreSQL config profile: $CONFIG_NAME"

  while IFS= read -r line; do
    line="${line%%#*}"
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "$line" ]] && continue

    if [[ "$line" != *=* ]]; then
      echo "Skipping non-assignment line: $line" >&2
      continue
    fi

    name="${line%%=*}"
    value="${line#*=}"
    name="${name//[[:space:]]/}"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    value="${value%\'}"
    value="${value#\'}"
    value="${value%\"}"
    value="${value#\"}"

    "$REPO_DIR/scripts/psql.sh" \
      -v setting_name="$name" \
      -v setting_value="$value" <<'SQL'
SELECT format('ALTER SYSTEM SET %I TO %L', :'setting_name', :'setting_value') \gexec
SQL
  done < "$CONFIG_FILE"
else
  echo "Using PostgreSQL default config profile."
fi

PGWORKBENCH_RUNTIME="${PGWORKBENCH_RUNTIME:-docker}" \
COMPOSE="${COMPOSE:-docker compose}" \
ENV_FILE="${ENV_FILE:-.env.example}" \
  "$REPO_DIR/scripts/runtime.sh" restart "$TOPOLOGY_NAME"
