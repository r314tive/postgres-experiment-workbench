#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$REPO_DIR/scripts/run_profile_sql.sh" smoke 00_setup.sql >/dev/null
"$REPO_DIR/scripts/run_profile_sql.sh" smoke 10_run.sql >/dev/null

COUNT="$("$REPO_DIR/scripts/psql.sh" -A -t -c "SELECT count(*) FROM smoke.items;")"
if [[ "$COUNT" != "10000" ]]; then
  echo "FAIL: expected 10000 smoke rows, got $COUNT" >&2
  exit 1
fi

echo "PASS: smoke profile"
