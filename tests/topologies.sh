#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TOPOLOGY_LIST="$("$REPO_DIR/scripts/topology.sh" list)"
grep -q '^single$' <<< "$TOPOLOGY_LIST"
grep -q '^primary-replica$' <<< "$TOPOLOGY_LIST"
grep -q '^logical-replication$' <<< "$TOPOLOGY_LIST"
grep -q '^pgbouncer$' <<< "$TOPOLOGY_LIST"

"$REPO_DIR/scripts/topology.sh" show primary-replica | grep -q 'TOPOLOGY_NAME="primary-replica"'
"$REPO_DIR/scripts/topology.sh" show logical-replication | grep -q 'TOPOLOGY_NAME="logical-replication"'
"$REPO_DIR/scripts/topology.sh" show pgbouncer | grep -q 'TOPOLOGY_NAME="pgbouncer"'

TOPOLOGY=primary-replica "$REPO_DIR/scripts/topology.sh" up primary-replica >/dev/null

PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" smoke 00_setup.sql >/dev/null
TOPOLOGY=primary-replica "$REPO_DIR/scripts/topology.sh" wait primary-replica >/dev/null

for _ in {1..60}; do
  if "$REPO_DIR/scripts/psql_replica.sh" -At -c "SELECT count(*) = 10000 FROM smoke.items" 2>/dev/null | grep -q '^t$'; then
    break
  fi
  sleep 1
done

"$REPO_DIR/scripts/psql_replica.sh" -At -c "SELECT count(*) = 10000 FROM smoke.items" | grep -q '^t$'
"$REPO_DIR/scripts/psql_replica.sh" -At -c "SELECT pg_is_in_recovery()" | grep -q '^t$'
"$REPO_DIR/scripts/psql.sh" -At -c "SELECT count(*) >= 1 FROM pg_stat_replication" | grep -q '^t$'

WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run topology/replica-readonly >/dev/null

PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" replication-slots 00_setup.sql >/dev/null
PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" replication-slots 10_run.sql >/dev/null
"$REPO_DIR/scripts/psql.sh" -At -c "SELECT count(*) >= 1 FROM pg_replication_slots WHERE slot_type = 'physical'" | grep -q '^t$'

TOPOLOGY=logical-replication "$REPO_DIR/scripts/topology.sh" up logical-replication >/dev/null
"$REPO_DIR/scripts/psql_logical_subscriber.sh" -At -c "SELECT 1" | grep -q '^1$'

PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" logical-replication 00_setup.sql >/dev/null
"$REPO_DIR/scripts/setup_logical_replication.sh" >/dev/null
PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" logical-replication 10_run.sql >/dev/null

LOGICAL_REPLICATION_COMPARE_SQL="SELECT count(*), coalesce(sum(id), 0), coalesce(sum(length(payload)), 0), coalesce(sum(CASE WHEN updated_at IS NULL THEN 0 ELSE 1 END), 0) FROM logical_repl.events" \
  "$REPO_DIR/scripts/wait_logical_replication.sh" >/dev/null

WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run topology/logical-status >/dev/null

TOPOLOGY=pgbouncer "$REPO_DIR/scripts/topology.sh" up pgbouncer >/dev/null
"$REPO_DIR/scripts/psql_pgbouncer.sh" -At -c "SELECT 1" | grep -q '^1$'

PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" smoke 00_setup.sql >/dev/null
WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run topology/pgbouncer-smoke >/dev/null

PROFILE_SIZE=small "$REPO_DIR/scripts/run_profile_sql.sh" connection-pressure 00_setup.sql >/dev/null
PROFILE_SIZE=small WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run topology/pgbouncer-connection-pressure >/dev/null
WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run topology/pgbouncer-prepared-statement >/dev/null
WORKLOAD_RUN_LOG=0 "$REPO_DIR/scripts/run_workload.sh" run topology/pgbouncer-admin >/dev/null

echo "PASS: topologies"
