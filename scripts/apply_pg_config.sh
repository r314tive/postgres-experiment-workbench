#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_NAME="${1:-${PG_CONFIG:-default}}"
CONFIG_FILE="$REPO_DIR/configs/$CONFIG_NAME/postgresql.conf"

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "PostgreSQL config profile not found: $CONFIG_FILE" >&2
  exit 1
fi

if [[ "$CONFIG_NAME" = "default" ]]; then
  echo "Config profile is default; leaving current settings unchanged."
  exit 0
fi

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

read -r -a COMPOSE_CMD <<< "${COMPOSE:-docker compose}"
"${COMPOSE_CMD[@]}" --env-file "${ENV_FILE:-.env.example}" restart postgres
"$REPO_DIR/scripts/wait_for_pg.sh"
