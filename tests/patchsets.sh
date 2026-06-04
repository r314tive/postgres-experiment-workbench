#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO="${GO:-go}"
GO_CACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}"
GO_MOD_CACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}"

PATCHSET_LIST="$("$REPO_DIR/scripts/patchset_catalog.sh" list)"
grep -q '^chaos/master$' <<< "$PATCHSET_LIST"

CHAOS_METADATA="$("$REPO_DIR/scripts/patchset_catalog.sh" show chaos/master)"
grep -q 'PATCHSET_NAME="chaos/master"' <<< "$CHAOS_METADATA"
grep -q 'PATCHSET_PG_REF="master"' <<< "$CHAOS_METADATA"
grep -q 'PATCHSET_ALLOW_EMPTY="1"' <<< "$CHAOS_METADATA"

"$REPO_DIR/scripts/patchset_catalog.sh" validate >/dev/null

GO_PATCHSET_LIST="$(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench patchset list
)"
grep -q '^chaos/master$' <<< "$GO_PATCHSET_LIST"

GO_CHAOS_METADATA="$(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench patchset show chaos/master
)"
grep -q 'PATCHSET_NAME="chaos/master"' <<< "$GO_CHAOS_METADATA"
grep -q 'PATCHSET_PG_REF="master"' <<< "$GO_CHAOS_METADATA"
grep -q 'PATCHSET_ALLOW_EMPTY="1"' <<< "$GO_CHAOS_METADATA"

(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench patchset validate >/dev/null
)

PLAN="$(
  PG_SOURCE_ACTION=plan \
  PG_PATCHSET=chaos/master \
  WORKLOAD_RUN_LOG=0 \
    "$REPO_DIR/scripts/run_workload.sh" run workloads/pg-source/check.env
)"
grep -q '^PG_PATCHSET=chaos/master$' <<< "$PLAN"
grep -q '^PATCHSET_SPEC_FILE=' <<< "$PLAN"
grep -q '^PATCHSET_DESCRIPTION=Default PostgreSQL master source-check patchset slot' <<< "$PLAN"
grep -q '^PG_PATCH_FILES=(none)$' <<< "$PLAN"

GO_PLAN="$(
  cd "$REPO_DIR"
  PG_PATCHSET=chaos/master \
  PG_SOURCE_RUN_ID=test \
  GOCACHE="$GO_CACHE" \
  GOMODCACHE="$GO_MOD_CACHE" \
    "$GO" run ./cmd/pgworkbench source plan pg-source/check
)"
grep -q '^PG_SOURCE_ACTION=plan$' <<< "$GO_PLAN"
grep -q '^PG_PATCHSET=chaos/master$' <<< "$GO_PLAN"
grep -q '^PATCHSET_SPEC_FILE=' <<< "$GO_PLAN"
grep -q '^PATCHSET_DESCRIPTION=Default PostgreSQL master source-check patchset slot' <<< "$GO_PLAN"
grep -q '^PG_PATCH_FILES=(none)$' <<< "$GO_PLAN"

echo "PASS: patchset catalog"
