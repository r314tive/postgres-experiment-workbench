#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$REPO_DIR/.tmp/test-scan"

rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR/clean" "$TEST_DIR/dirty/results" "$TEST_DIR/dirty/log"

printf 'ordinary PostgreSQL log line\n' > "$TEST_DIR/clean/postgresql.log"
"$REPO_DIR/scripts/scan_pg_failures.sh" "$TEST_DIR/clean" >/dev/null

cat > "$TEST_DIR/dirty/log/postgresql.log" <<'LOG'
server process (PID 12345) was terminated by signal 11: SIGSEGV
terminating any other active server processes
LOG

cat > "$TEST_DIR/dirty/results/regression.diffs" <<'DIFF'
+ERROR:  could not find pathkey item to sort
DIFF

if "$REPO_DIR/scripts/scan_pg_failures.sh" "$TEST_DIR/dirty" >/dev/null 2>&1; then
  echo "FAIL: expected scan_pg_failures.sh to detect failure evidence" >&2
  exit 1
fi

echo "PASS: failure scanner"
