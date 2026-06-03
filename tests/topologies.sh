#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TOPOLOGY_LIST="$("$REPO_DIR/scripts/topology.sh" list)"
grep -q '^single$' <<< "$TOPOLOGY_LIST"
grep -q '^primary-replica$' <<< "$TOPOLOGY_LIST"

"$REPO_DIR/scripts/topology.sh" show primary-replica | grep -q 'TOPOLOGY_NAME="primary-replica"'

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

echo "PASS: topologies"
