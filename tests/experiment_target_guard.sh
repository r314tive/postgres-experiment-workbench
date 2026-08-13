#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-target-guard.XXXXXX")"
trap 'rm -rf -- "$TMP_DIR"' EXIT

RUNNER="$REPO_DIR/scripts/run_experiment.sh"

assert_rejected_without_run() {
  local run_id="$1"
  local expected="$2"
  shift 2

  if env EXPERIMENT_RUN_ID="$run_id" "$@" "$RUNNER" run smoke \
    >"$TMP_DIR/$run_id.out" 2>"$TMP_DIR/$run_id.err"; then
    echo "FAIL: unsafe experiment target was accepted: $run_id" >&2
    exit 1
  fi
  grep -q "$expected" "$TMP_DIR/$run_id.err"
  if [[ -e "$REPO_DIR/runs/$run_id" ]]; then
    echo "FAIL: rejected target created a run directory: $run_id" >&2
    exit 1
  fi
}

assert_rejected_without_run "target-remote-primary-$$" \
  'Experiment runs do not support ALLOW_NONLOCAL_PG' \
  POSTGRES_HOST=db.example.test ALLOW_NONLOCAL_PG=1

assert_rejected_without_run "target-remote-replica-$$" \
  'Experiment runs require loopback POSTGRES_REPLICA_HOST' \
  POSTGRES_REPLICA_HOST=replica.example.test

assert_rejected_without_run "target-system-db-$$" \
  'Experiment runs do not support ALLOW_SYSTEM_DB' \
  POSTGRES_DB=postgres ALLOW_SYSTEM_DB=1

assert_rejected_without_run "target-system-db-direct-$$" \
  'Experiment runs refuse PostgreSQL system database: postgres' \
  POSTGRES_DB=postgres

mkdir -p "$TMP_DIR/fake-bin"
cat > "$TMP_DIR/fake-bin/psql" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf 'psql %s\n' "$*" >> "${TARGET_COMMAND_LOG:?}"
SCRIPT
chmod +x "$TMP_DIR/fake-bin/psql"
cat > "$TMP_DIR/fake-bin/pgbench" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf 'pgbench %s\n' "$*" >> "${TARGET_COMMAND_LOG:?}"
SCRIPT
chmod +x "$TMP_DIR/fake-bin/pgbench"
printf 'SELECT 1;\n' > "$TMP_DIR/probe.sql"

cat > "$TMP_DIR/nested-workload.env" <<ENV
WORKLOAD_NAME="nested target override"
WORKLOAD_KIND=sql
WORKLOAD_REQUIRES_POSTGRES=0
SQL="$TMP_DIR/probe.sql"
POSTGRES_HOST=203.0.113.10
POSTGRES_PORT=6543
POSTGRES_DB=postgres
ALLOW_NONLOCAL_PG=1
ALLOW_SYSTEM_DB=1
ENV

cat > "$TMP_DIR/nested-dataset.env" <<ENV
DATASET_NAME="nested dataset target override"
DATASET_KIND=sql
DATASET_SQL="$TMP_DIR/probe.sql"
POSTGRES_HOST=203.0.113.11
POSTGRES_PORT=6544
POSTGRES_DB=template1
ALLOW_NONLOCAL_PG=1
ALLOW_SYSTEM_DB=1
ENV

BOUNDARY_ENV=(
  env
  PATH="$TMP_DIR/fake-bin:$PATH"
  PGWORKBENCH_RUNTIME=native
  PGWORKBENCH_NATIVE_BINDIR="$TMP_DIR/fake-bin"
  PGWORKBENCH_EXPERIMENT_MODE=1
  POSTGRES_HOST=127.0.0.1
  POSTGRES_PORT=56543
  POSTGRES_REPLICA_HOST=127.0.0.1
  POSTGRES_LOGICAL_SUBSCRIBER_HOST=127.0.0.1
  POSTGRES_UPGRADE_OLD_HOST=127.0.0.1
  POSTGRES_UPGRADE_NEW_HOST=127.0.0.1
  PGBOUNCER_HOST=127.0.0.1
  POSTGRES_DB=pg_experiment_workbench
  ALLOW_NONLOCAL_PG=0
  ALLOW_SYSTEM_DB=0
  WORKLOAD_RUN_LOG=0
  TARGET_COMMAND_LOG="$TMP_DIR/commands.log"
)

/bin/bash -uc 'source "$1"; pgworkbench_reject_experiment_target_args' bash \
  "$REPO_DIR/scripts/target_arg_guard.sh"

"${BOUNDARY_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run "$TMP_DIR/nested-workload.env" >/dev/null
"${BOUNDARY_ENV[@]}" "$REPO_DIR/scripts/load_dataset.sh" load "$TMP_DIR/nested-dataset.env" >/dev/null

if grep -Eq -- '-h 203\.0\.113\.[0-9]+ |-p 654[34] |-d (postgres|template1)( |$)' "$TMP_DIR/commands.log"; then
  echo "FAIL: nested spec changed an experiment-owned PostgreSQL target" >&2
  cat "$TMP_DIR/commands.log" >&2
  exit 1
fi
[[ "$(grep -c -- '-h 127.0.0.1 -p 56543 .* -d pg_experiment_workbench ' "$TMP_DIR/commands.log")" = "2" ]]

unsafe_pgbench_args=(
  '-h 203.0.113.88'
  '--host=203.0.113.88'
  '-h203.0.113.88'
  '-vh203.0.113.88'
  '-p6543'
  '-Uevil'
  '-dpostgres'
  '--'
)
for i in "${!unsafe_pgbench_args[@]}"; do
  spec="$TMP_DIR/nested-pgbench-$i.env"
  cat > "$spec" <<ENV
WORKLOAD_NAME="nested pgbench target override"
WORKLOAD_KIND=pgbench
WORKLOAD_REQUIRES_POSTGRES=0
PGBENCH_INIT=0
PGBENCH_TRANSACTIONS=1
PGBENCH_EXTRA_ARGS="${unsafe_pgbench_args[$i]}"
ENV
  if "${BOUNDARY_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run "$spec" \
    >"$TMP_DIR/nested-pgbench-$i.out" 2>&1; then
    echo "FAIL: nested target-changing pgbench args were accepted: ${unsafe_pgbench_args[$i]}" >&2
    exit 1
  fi
  grep -q 'Experiment runs reject target-changing PGBENCH_EXTRA_ARGS' \
    "$TMP_DIR/nested-pgbench-$i.out"
done

noisia_index=0
for noisia_args in '--conninfo host=203.0.113.89' '--conninfo=host=203.0.113.89' '--'; do
  spec="$TMP_DIR/nested-noisia-$noisia_index.env"
  cat > "$spec" <<ENV
WORKLOAD_NAME="nested noisia target override"
WORKLOAD_KIND=noisia
WORKLOAD_REQUIRES_POSTGRES=0
NOISIA_WORKLOAD=wait-xacts
NOISIA_EXTRA_ARGS="$noisia_args"
ENV
  if "${BOUNDARY_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run "$spec" \
    >"$TMP_DIR/nested-noisia.out" 2>&1; then
    echo "FAIL: nested target-changing noisia args were accepted: $noisia_args" >&2
    exit 1
  fi
  grep -q 'Experiment runs reject target-changing NOISIA_EXTRA_ARGS' \
    "$TMP_DIR/nested-noisia.out"
  noisia_index=$((noisia_index + 1))
done

cat > "$TMP_DIR/nested-pgbench-safe.env" <<'ENV'
WORKLOAD_NAME="nested safe pgbench extras"
WORKLOAD_KIND=pgbench
WORKLOAD_REQUIRES_POSTGRES=0
PGBENCH_INIT=0
PGBENCH_TRANSACTIONS=1
PGBENCH_EXTRA_ARGS="--aggregate-interval=5 -Mprepared"
ENV
"${BOUNDARY_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run \
  "$TMP_DIR/nested-pgbench-safe.env" >/dev/null
grep -q -- 'pgbench .*--aggregate-interval=5 -Mprepared .* -h 127.0.0.1 -p 56543 -U postgres pg_experiment_workbench$' \
  "$TMP_DIR/commands.log"

cat > "$TMP_DIR/native-proxy.env" <<'ENV'
WORKLOAD_NAME="native proxy must fail closed"
WORKLOAD_KIND=pgbench
WORKLOAD_REQUIRES_POSTGRES=0
PGBENCH_INIT=0
PGBENCH_TRANSACTIONS=1
PGBENCH_TARGET=pgbouncer
ENV
if "${BOUNDARY_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run \
  "$TMP_DIR/native-proxy.env" >"$TMP_DIR/native-proxy.out" 2>&1; then
  echo "FAIL: native pgbench accepted the PgBouncer benchmark target" >&2
  exit 1
fi
grep -q 'native benchmark targets are direct PostgreSQL only' "$TMP_DIR/native-proxy.out"

cat > "$TMP_DIR/fake-compose" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf 'compose %s\n' "$*" >> "${TARGET_COMMAND_LOG:?}"
if [[ "${1:-}" = "--env-file" ]]; then
  shift 2
fi
if [[ "${1:-}" = "images" && "${2:-}" = "-q" && $# -eq 3 ]]; then
  case "${3:-}" in
    postgres) printf 'sha256:%064d\n' 1 ;;
    pgbouncer) printf 'sha256:%064d\n' 2 ;;
    *) echo "unsupported fake Compose service: ${3:-}" >&2; exit 2 ;;
  esac
fi
SCRIPT
chmod +x "$TMP_DIR/fake-compose"
cat > "$TMP_DIR/docker-proxy.env" <<'ENV'
WORKLOAD_NAME="Docker PgBouncer target argv"
WORKLOAD_KIND=pgbench
WORKLOAD_REQUIRES_POSTGRES=0
PGBENCH_INIT=0
PGBENCH_TRANSACTIONS=1
PGBENCH_TARGET=pgbouncer
ENV
env \
  COMPOSE="$TMP_DIR/fake-compose" \
  PGWORKBENCH_RUNTIME=docker \
  PGWORKBENCH_EXPERIMENT_MODE=1 \
  PGWORKBENCH_BENCHMARK_CONTRACT_VERSION=1 \
  PGWORKBENCH_BENCHMARK_PREPARED=1 \
  EXPERIMENT_TOPOLOGY=pgbouncer \
  POSTGRES_HOST=127.0.0.1 \
  POSTGRES_DB=pg_experiment_workbench \
  ALLOW_NONLOCAL_PG=0 \
  ALLOW_SYSTEM_DB=0 \
  WORKLOAD_RUN_LOG=0 \
  TARGET_COMMAND_LOG="$TMP_DIR/proxy-commands.log" \
  "$REPO_DIR/scripts/run_workload.sh" run "$TMP_DIR/docker-proxy.env" >/dev/null
grep -Eq -- 'compose (.* )?exec -T -e PGPASSWORD=postgres postgres pgbench .* -h pgbouncer -p 5432 -U postgres pg_experiment_workbench$' \
  "$TMP_DIR/proxy-commands.log"
if grep -Eq -- ' -h (127\.0\.0\.1|[^ ]*example[^ ]*) -p (55433|56432) ' "$TMP_DIR/proxy-commands.log"; then
  echo "FAIL: Docker PgBouncer target used a host-side or external endpoint" >&2
  cat "$TMP_DIR/proxy-commands.log" >&2
  exit 1
fi

if PGWORKBENCH_EXPERIMENT_MODE=1 \
  POSTGRES_HOST=203.0.113.12 \
  POSTGRES_DB=pg_experiment_workbench \
  ALLOW_NONLOCAL_PG=1 \
  ALLOW_SYSTEM_DB=0 \
  "$REPO_DIR/scripts/psql.sh" -c 'SELECT 1' > "$TMP_DIR/direct.out" 2>&1; then
  echo "FAIL: direct experiment SQL accepted a remote target" >&2
  exit 1
fi
grep -q 'Experiment runs do not support ALLOW_NONLOCAL_PG' "$TMP_DIR/direct.out"

# The generic utility/workload contract intentionally retains its explicit
# external-target opt-in when no experiment marker is present.
TARGET_COMMAND_LOG="$TMP_DIR/generic.log" \
PATH="$TMP_DIR/fake-bin:$PATH" \
PGWORKBENCH_RUNTIME=native \
PGWORKBENCH_NATIVE_BINDIR="$TMP_DIR/fake-bin" \
POSTGRES_HOST=203.0.113.13 \
POSTGRES_DB=postgres \
ALLOW_NONLOCAL_PG=1 \
ALLOW_SYSTEM_DB=1 \
  "$REPO_DIR/scripts/psql.sh" -c 'SELECT 1'
grep -q -- '-h 203.0.113.13 .* -d postgres ' "$TMP_DIR/generic.log"

# A repository .env must not be able to clear the experiment marker or replace
# caller-owned safe target values at the final psql boundary.
BOUNDARY_REPO="$TMP_DIR/boundary-repo"
mkdir -p "$BOUNDARY_REPO/scripts"
cp "$REPO_DIR/scripts/psql.sh" "$BOUNDARY_REPO/scripts/psql.sh"
cp "$REPO_DIR/scripts/guard_local_pg.sh" "$BOUNDARY_REPO/scripts/guard_local_pg.sh"
chmod +x "$BOUNDARY_REPO/scripts/"*.sh
cat > "$BOUNDARY_REPO/.env" <<'ENV'
PGWORKBENCH_EXPERIMENT_MODE=0
POSTGRES_HOST=203.0.113.20
POSTGRES_DB=postgres
ALLOW_NONLOCAL_PG=1
ALLOW_SYSTEM_DB=1
ENV
TARGET_COMMAND_LOG="$TMP_DIR/preserved.log" \
PATH="$TMP_DIR/fake-bin:$PATH" \
PGWORKBENCH_RUNTIME=native \
PGWORKBENCH_NATIVE_BINDIR="$TMP_DIR/fake-bin" \
PGWORKBENCH_EXPERIMENT_MODE=1 \
POSTGRES_HOST=127.0.0.1 \
POSTGRES_DB=pg_experiment_workbench \
ALLOW_NONLOCAL_PG=0 \
ALLOW_SYSTEM_DB=0 \
  "$BOUNDARY_REPO/scripts/psql.sh" -c 'SELECT 1'
grep -q -- '-h 127.0.0.1 .* -d pg_experiment_workbench ' "$TMP_DIR/preserved.log"

# libpq can prefer PGHOSTADDR or service-file routing even when -h is present.
cat > "$TMP_DIR/fake-bin/psql" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
for name in PGHOSTADDR PGSERVICE PGSERVICEFILE PGPASSFILE PGOPTIONS PGTARGETSESSIONATTRS PGSSLMODE; do
  if [[ ${!name+x} ]]; then
    echo "unsafe libpq variable survived: $name" >&2
    exit 17
  fi
done
printf 'psql %s\n' "$*" >> "${TARGET_COMMAND_LOG:?}"
SCRIPT
chmod +x "$TMP_DIR/fake-bin/psql"
TARGET_COMMAND_LOG="$TMP_DIR/libpq.log" \
PATH="$TMP_DIR/fake-bin:$PATH" \
PGWORKBENCH_RUNTIME=native \
PGWORKBENCH_NATIVE_BINDIR="$TMP_DIR/fake-bin" \
PGWORKBENCH_EXPERIMENT_MODE=1 \
POSTGRES_HOST=127.0.0.1 \
POSTGRES_DB=pg_experiment_workbench \
ALLOW_NONLOCAL_PG=0 \
ALLOW_SYSTEM_DB=0 \
PGHOSTADDR=203.0.113.40 \
PGSERVICE=unsafe \
PGSERVICEFILE="$TMP_DIR/unsafe-service.conf" \
PGPASSFILE="$TMP_DIR/unsafe-pass" \
PGOPTIONS='-c search_path=unsafe' \
PGTARGETSESSIONATTRS=any \
PGSSLMODE=allow \
  "$REPO_DIR/scripts/psql.sh" -c 'SELECT 1'
grep -q -- '-h 127.0.0.1 .* -d pg_experiment_workbench ' "$TMP_DIR/libpq.log"

echo "PASS: experiment target guard"
