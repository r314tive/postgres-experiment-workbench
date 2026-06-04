#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PROFILE_LIST="$("$REPO_DIR/scripts/profile_catalog.sh" list)"
grep -q '^smoke$' <<< "$PROFILE_LIST"
grep -q '^locks$' <<< "$PROFILE_LIST"
grep -q '^connection-pressure$' <<< "$PROFILE_LIST"

SMOKE_METADATA="$("$REPO_DIR/scripts/profile_catalog.sh" show smoke)"
grep -q 'PROFILE_NAME="smoke"' <<< "$SMOKE_METADATA"
grep -q 'PROFILE_DESCRIPTION="Minimal platform verification profile."' <<< "$SMOKE_METADATA"
grep -q 'PROFILE_REQUIRES_TOPOLOGY="single"' <<< "$SMOKE_METADATA"

REPLICATION_METADATA="$("$REPO_DIR/scripts/profile_catalog.sh" show replication-slots)"
grep -q 'PROFILE_REQUIRES_TOPOLOGY="primary-replica"' <<< "$REPLICATION_METADATA"

"$REPO_DIR/scripts/profile_catalog.sh" validate >/dev/null

echo "PASS: profile catalog"
