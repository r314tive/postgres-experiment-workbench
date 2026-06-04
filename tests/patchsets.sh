#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PATCHSET_LIST="$("$REPO_DIR/scripts/patchset_catalog.sh" list)"
grep -q '^chaos/master$' <<< "$PATCHSET_LIST"

CHAOS_METADATA="$("$REPO_DIR/scripts/patchset_catalog.sh" show chaos/master)"
grep -q 'PATCHSET_NAME="chaos/master"' <<< "$CHAOS_METADATA"
grep -q 'PATCHSET_PG_REF="master"' <<< "$CHAOS_METADATA"
grep -q 'PATCHSET_ALLOW_EMPTY="1"' <<< "$CHAOS_METADATA"

"$REPO_DIR/scripts/patchset_catalog.sh" validate >/dev/null

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

echo "PASS: patchset catalog"
