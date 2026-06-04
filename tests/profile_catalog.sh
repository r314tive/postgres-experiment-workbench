#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO="${GO:-go}"
GO_CACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}"
GO_MOD_CACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}"

PROFILE_LIST="$("$REPO_DIR/scripts/profile_catalog.sh" list)"
grep -q '^smoke$' <<< "$PROFILE_LIST"
grep -q '^locks$' <<< "$PROFILE_LIST"
grep -q '^connection-pressure$' <<< "$PROFILE_LIST"
grep -q '^temp-spill$' <<< "$PROFILE_LIST"

SMOKE_METADATA="$("$REPO_DIR/scripts/profile_catalog.sh" show smoke)"
grep -q 'PROFILE_NAME="smoke"' <<< "$SMOKE_METADATA"
grep -q 'PROFILE_DESCRIPTION="Minimal platform verification profile."' <<< "$SMOKE_METADATA"
grep -q 'PROFILE_REQUIRES_TOPOLOGY="single"' <<< "$SMOKE_METADATA"

REPLICATION_METADATA="$("$REPO_DIR/scripts/profile_catalog.sh" show replication-slots)"
grep -q 'PROFILE_REQUIRES_TOPOLOGY="primary-replica"' <<< "$REPLICATION_METADATA"

TEMP_SPILL_METADATA="$("$REPO_DIR/scripts/profile_catalog.sh" show temp-spill)"
grep -q 'PROFILE_TAGS="temp-files work-mem sort hash"' <<< "$TEMP_SPILL_METADATA"

"$REPO_DIR/scripts/profile_catalog.sh" validate >/dev/null

GO_PROFILE_PLAN="$(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench profile plan --size small --seconds 30 smoke
)"
grep -q '# Profile Plan' <<< "$GO_PROFILE_PLAN"
grep -q '00_setup.sql' <<< "$GO_PROFILE_PLAN"
grep -q '10_run.sql' <<< "$GO_PROFILE_PLAN"

GO_PROFILE_SQL_PLAN="$(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench profile plan --size small locks 30_diagnostics.sql
)"
grep -q '30_diagnostics.sql' <<< "$GO_PROFILE_SQL_PLAN"

echo "PASS: profile catalog"
