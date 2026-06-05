#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

DIAGNOSTICS_LIST="$("$REPO_DIR/scripts/run_diagnostic.sh" list)"
grep -q '^activity$' <<< "$DIAGNOSTICS_LIST"
grep -q '^locks$' <<< "$DIAGNOSTICS_LIST"
grep -q '^index_health$' <<< "$DIAGNOSTICS_LIST"
grep -q '^table_health$' <<< "$DIAGNOSTICS_LIST"

"$REPO_DIR/scripts/run_diagnostic.sh" show settings | grep -q 'pg_settings'
"$REPO_DIR/scripts/run_diagnostic.sh" show sql/diagnostics/activity.sql | grep -q 'pg_stat_activity'

echo "PASS: diagnostics catalog"
