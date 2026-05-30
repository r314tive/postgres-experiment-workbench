#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="${1:?Usage: scripts/run_profile_sql.sh profile sql-file}"
SQL_NAME="${2:?Usage: scripts/run_profile_sql.sh profile sql-file}"
SQL_FILE="$REPO_DIR/profiles/$PROFILE/sql/$SQL_NAME"

if [[ ! -f "$SQL_FILE" ]]; then
  echo "Profile SQL not found: $SQL_FILE" >&2
  exit 1
fi

"$REPO_DIR/scripts/psql.sh" \
  -v profile="$PROFILE" \
  -v profile_size="${PROFILE_SIZE:-small}" \
  -v profile_seconds="${PROFILE_SECONDS:-30}" \
  -f "$SQL_FILE"
