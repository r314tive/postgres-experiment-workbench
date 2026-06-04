#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

WORKLOAD_LIST="$("$REPO_DIR/scripts/run_workload.sh" list)"
grep -q '^pgbench/tiny$' <<< "$WORKLOAD_LIST"

PGBENCH_SPEC="$("$REPO_DIR/scripts/run_workload.sh" show pgbench/tiny)"
grep -q 'WORKLOAD_KIND="pgbench"' <<< "$PGBENCH_SPEC"

PG_SOURCE_SPEC="$("$REPO_DIR/scripts/run_workload.sh" show pg-source/check)"
grep -q 'WORKLOAD_KIND="pg-source-check"' <<< "$PG_SOURCE_SPEC"

TOPOLOGY_SPEC="$("$REPO_DIR/scripts/run_workload.sh" show topology/replica-readonly)"
grep -q 'WORKLOAD_KIND="shell"' <<< "$TOPOLOGY_SPEC"

LOGICAL_SPEC="$("$REPO_DIR/scripts/run_workload.sh" show topology/logical-status)"
grep -q 'WORKLOAD_KIND="shell"' <<< "$LOGICAL_SPEC"

PGBOUNCER_SPEC="$("$REPO_DIR/scripts/run_workload.sh" show topology/pgbouncer-smoke)"
grep -q 'WORKLOAD_KIND="shell"' <<< "$PGBOUNCER_SPEC"

UPGRADE_SPEC="$("$REPO_DIR/scripts/run_workload.sh" show topology/upgrade-dump-restore)"
grep -q 'WORKLOAD_NAME="multi-version dump restore upgrade"' <<< "$UPGRADE_SPEC"
grep -q 'WORKLOAD_REQUIRES_POSTGRES=0' <<< "$UPGRADE_SPEC"
NATIVE_UPGRADE_SPEC="$("$REPO_DIR/scripts/run_workload.sh" show topology/native-pg-upgrade)"
grep -q 'WORKLOAD_NAME="native pg_upgrade adapter"' <<< "$NATIVE_UPGRADE_SPEC"
grep -q 'WORKLOAD_REQUIRES_POSTGRES=0' <<< "$NATIVE_UPGRADE_SPEC"

NATIVE_UPGRADE_PLAN="$(PG_UPGRADE_ACTION=plan WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run topology/native-pg-upgrade)"
grep -q 'PG_UPGRADE_ACTION=plan' <<< "$NATIVE_UPGRADE_PLAN"
grep -q 'Required for check/run' <<< "$NATIVE_UPGRADE_PLAN"

PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" smoke 00_setup.sql >/dev/null
PROFILE_SIZE=small "$REPO_DIR/scripts/run_workload.sh" run workloads/sql/smoke-run.env >/dev/null
WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run workloads/compose/pg-isready.env >/dev/null
WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run workloads/shell/pg-dump-smoke.env >/dev/null

PGBENCH_TIME=1 \
PGBENCH_CLIENTS=1 \
PGBENCH_THREADS=1 \
PGBENCH_SCALE=1 \
PGBENCH_RESET=1 \
PGBENCH_INIT=1 \
  "$REPO_DIR/scripts/run_workload.sh" run workloads/pgbench/tiny.env >/dev/null

PG_SOURCE_ACTION=plan \
WORKLOAD_RUN_LOG=0 \
  "$REPO_DIR/scripts/run_workload.sh" run workloads/pg-source/check.env >/dev/null

echo "PASS: workload runner"
