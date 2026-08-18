#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-runtime-workloads.XXXXXX")"
TEST_DIR="$(cd "$TEST_DIR" && pwd -P)"
OUTPUT_REL=".tmp/utility-output/runtime-workload-adapters-$$"
OUTPUT_DIR="$REPO_DIR/$OUTPUT_REL"

cleanup() {
  rm -rf -- "$TEST_DIR"
  rm -rf -- "$OUTPUT_DIR"
}
trap cleanup EXIT

mkdir -p "$TEST_DIR/fake-bin"

cat > "$TEST_DIR/fake-bin/psql" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf 'psql %s\n' "$*" >> "${WORKLOAD_COMMAND_LOG:?}"
if [[ ! -t 0 ]]; then
  while IFS= read -r _; do :; done
fi
SCRIPT

cat > "$TEST_DIR/fake-bin/pgbench" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${PGBENCH_RANDOM_SEED+x} && -z "$PGBENCH_RANDOM_SEED" ]]; then
  echo 'empty PGBENCH_RANDOM_SEED reached pgbench' >&2
  exit 19
fi
printf 'pgbench %s\n' "$*" >> "${WORKLOAD_COMMAND_LOG:?}"
SCRIPT

cat > "$TEST_DIR/fake-bin/pg_dump" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${PGWORKBENCH_EXPERIMENT_MODE:-0}" = "1" ]]; then
  for name in PGHOSTADDR PGSERVICE PGSERVICEFILE PGPASSFILE PGOPTIONS PGTARGETSESSIONATTRS PGSSLMODE; do
    if [[ ${!name+x} ]]; then
      echo "unsafe libpq variable survived: $name" >&2
      exit 17
    fi
  done
fi
printf 'pg_dump %s\n' "$*" >> "${WORKLOAD_COMMAND_LOG:?}"
if [[ "${FAKE_PG_DUMP_FAIL:-0}" = "1" ]]; then
  exit 7
fi
if [[ " $* " = *" --format=custom "* ]]; then
  printf 'PGDMP-fake-archive\n'
else
  printf 'CREATE SCHEMA smoke;\nCREATE TABLE smoke.items(id bigint);\n'
fi
SCRIPT

cat > "$TEST_DIR/fake-bin/pg_dumpall" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf 'pg_dumpall %s\n' "$*" >> "${WORKLOAD_COMMAND_LOG:?}"
printf 'CREATE DATABASE pg_experiment_workbench;\n'
SCRIPT

cat > "$TEST_DIR/fake-bin/pg_restore" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf 'pg_restore %s\n' "$*" >> "${WORKLOAD_COMMAND_LOG:?}"
archive="$(cat)"
[[ "$archive" = PGDMP-fake-archive ]]
SCRIPT

cat > "$TEST_DIR/fake-compose" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf 'compose %s\n' "$*" >> "${COMPOSE_COMMAND_LOG:?}"
while (( $# > 0 )) && [[ "$1" != "postgres" ]]; do
  shift
done
[[ "${1:-}" = "postgres" ]]
shift
tool="${1:?missing PostgreSQL utility}"
shift
exec "$tool" "$@"
SCRIPT

chmod +x "$TEST_DIR/fake-bin/"* "$TEST_DIR/fake-compose"

cat > "$TEST_DIR/shell.env" <<'ENV'
WORKLOAD_NAME="native shell probe"
WORKLOAD_KIND="shell"
WORKLOAD_REQUIRES_POSTGRES=0
WORKLOAD_CMD='printf "%s|%s|%s\n" "$PGHOST" "$PGPORT" "$PGDATABASE" > "$WORKLOAD_SHELL_OUT"'
ENV

cat > "$TEST_DIR/sql.env" <<ENV
WORKLOAD_NAME="native SQL probe"
WORKLOAD_KIND="sql"
WORKLOAD_REQUIRES_POSTGRES=0
SQL="$TEST_DIR/probe.sql"
ENV
printf 'SELECT 1;\n' > "$TEST_DIR/probe.sql"

cat > "$TEST_DIR/profile.env" <<'ENV'
WORKLOAD_NAME="native profile probe"
WORKLOAD_KIND="profile-sql"
WORKLOAD_REQUIRES_POSTGRES=0
PROFILE="smoke"
WORKLOAD_SQL="10_run.sql"
ENV

cat > "$TEST_DIR/pgbench.env" <<'ENV'
WORKLOAD_NAME="native pgbench probe"
WORKLOAD_KIND="pgbench"
WORKLOAD_REQUIRES_POSTGRES=0
PGBENCH_RESET=0
PGBENCH_INIT=1
PGBENCH_SCALE=1
PGBENCH_CLIENTS=1
PGBENCH_THREADS=1
PGBENCH_TRANSACTIONS=1
PGBENCH_SCRIPT="workloads/pgbench/scripts/simple-transfer.sql"
ENV

cat > "$TEST_DIR/compose.env" <<'ENV'
WORKLOAD_NAME="native compose rejection"
WORKLOAD_KIND="compose-run"
WORKLOAD_REQUIRES_POSTGRES=0
WORKLOAD_COMMAND="true"
ENV

cat > "$TEST_DIR/noisia.env" <<'ENV'
WORKLOAD_NAME="native noisia rejection"
WORKLOAD_KIND="noisia"
WORKLOAD_REQUIRES_POSTGRES=0
NOISIA_WORKLOAD="wait-xacts"
ENV

cat > "$TEST_DIR/pg-dump.env" <<ENV
WORKLOAD_NAME="native pg_dump probe"
WORKLOAD_KIND="pg-dump"
WORKLOAD_REQUIRES_POSTGRES=0
UTILITY_SOURCE_SCHEMA="smoke"
UTILITY_OUTPUT_FILE="$OUTPUT_REL/pg-dump.sql"
ENV

cat > "$TEST_DIR/pg-dumpall.env" <<ENV
WORKLOAD_NAME="native pg_dumpall probe"
WORKLOAD_KIND="pg-dumpall"
WORKLOAD_REQUIRES_POSTGRES=0
UTILITY_OUTPUT_FILE="$OUTPUT_REL/pg-dumpall.sql"
ENV

cat > "$TEST_DIR/pg-restore.env" <<ENV
WORKLOAD_NAME="native pg_restore probe"
WORKLOAD_KIND="pg-restore"
WORKLOAD_REQUIRES_POSTGRES=0
UTILITY_SOURCE_SCHEMA="smoke"
UTILITY_TARGET_SCHEMA="restore_check"
UTILITY_ARCHIVE_FILE="$OUTPUT_REL/pg-restore.dump"
UTILITY_OUTPUT_FILE="$OUTPUT_REL/pg-restore.sql"
ENV

cat > "$TEST_DIR/pg-dump-retarget.env" <<ENV
WORKLOAD_NAME="nested pg_dump target override"
WORKLOAD_KIND="pg-dump"
WORKLOAD_REQUIRES_POSTGRES=0
UTILITY_SOURCE_SCHEMA="smoke"
UTILITY_OUTPUT_FILE="$OUTPUT_REL/guarded.sql"
POSTGRES_HOST=203.0.113.40
POSTGRES_PORT=6543
POSTGRES_DB=postgres
ALLOW_NONLOCAL_PG=1
ALLOW_SYSTEM_DB=1
ENV

COMMON_ENV=(
  env
  PATH="$TEST_DIR/fake-bin:$PATH"
  PGWORKBENCH_RUNTIME=native
  PGWORKBENCH_NATIVE_BINDIR="$TEST_DIR/fake-bin"
  POSTGRES_HOST=127.0.0.1
  POSTGRES_PORT=56543
  POSTGRES_DB=pg_experiment_workbench
  POSTGRES_USER=postgres
  POSTGRES_PASSWORD=postgres
  WORKLOAD_RUN_LOG=0
  WORKLOAD_COMMAND_LOG="$TEST_DIR/commands.log"
)

WORKLOAD_SHELL_OUT="$TEST_DIR/shell.out" "${COMMON_ENV[@]}" \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/shell.env" >/dev/null
grep -q '^127.0.0.1|56543|pg_experiment_workbench$' "$TEST_DIR/shell.out"

"${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/sql.env" >/dev/null
grep -q -- "-p 56543 .* -f $TEST_DIR/probe.sql" "$TEST_DIR/commands.log"

"${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/profile.env" >/dev/null
grep -q -- "-f $REPO_DIR/profiles/smoke/sql/10_run.sql" "$TEST_DIR/commands.log"

"${COMMON_ENV[@]}" PGBENCH_RANDOM_SEED= PGBENCH_WARMUP_TIME=1 \
  PGBENCH_WARMUP_RANDOM_SEED=18 PGBENCH_MEASURE_RANDOM_SEED=17 \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pgbench.env" >/dev/null
grep -q '^pgbench -h 127.0.0.1 -p 56543 .* -i -s 1 pg_experiment_workbench$' "$TEST_DIR/commands.log"
grep -q -- "-f $REPO_DIR/workloads/pgbench/scripts/simple-transfer.sql" "$TEST_DIR/commands.log"
grep -q -- '--random-seed=18 -T 1 ' "$TEST_DIR/commands.log"
grep -q -- '--random-seed=17 -t 1 ' "$TEST_DIR/commands.log"

"${COMMON_ENV[@]}" PGBENCH_CONNECT_PER_TRANSACTION=1 \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pgbench.env" >/dev/null
grep -q -- '^pgbench -c 1 -j 1 --connect -f .* -t 1 -h 127.0.0.1 -p 56543 -U postgres pg_experiment_workbench$' "$TEST_DIR/commands.log"

for kind in pg-dump pg-dumpall pg-restore; do
  "${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/$kind.env" >/dev/null
done
for output in pg-dump.sql pg-dumpall.sql pg-restore.dump pg-restore.sql; do
  test -s "$OUTPUT_DIR/$output"
done
grep -q '^pg_dump -h 127.0.0.1 -p 56543 -U postgres -d pg_experiment_workbench --schema smoke$' "$TEST_DIR/commands.log"
grep -q '^pg_dumpall -h 127.0.0.1 -p 56543 -U postgres -d dbname=pg_experiment_workbench --no-role-passwords$' "$TEST_DIR/commands.log"
grep -q '^pg_restore -h 127.0.0.1 -p 56543 -U postgres -d pgw_restore_.* --no-owner --no-privileges$' "$TEST_DIR/commands.log"

"${COMMON_ENV[@]}" PGWORKBENCH_EXPERIMENT_MODE=1 ALLOW_NONLOCAL_PG=0 ALLOW_SYSTEM_DB=0 \
  PGHOSTADDR=203.0.113.77 PGSERVICE=unsafe_service \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pg-dump-retarget.env" >/dev/null
grep -q '^pg_dump -h 127.0.0.1 -p 56543 -U postgres -d pg_experiment_workbench --schema smoke$' "$TEST_DIR/commands.log"
if grep -Eq -- '203\.0\.113\.40| -p 6543 | -d postgres ' "$TEST_DIR/commands.log"; then
  echo 'FAIL: utility adapter escaped the experiment target boundary' >&2
  exit 1
fi

COMPOSE_COMMAND_LOG="$TEST_DIR/compose.log" \
WORKLOAD_COMMAND_LOG="$TEST_DIR/docker-commands.log" \
PATH="$TEST_DIR/fake-bin:$PATH" \
PGWORKBENCH_RUNTIME=docker \
COMPOSE="$TEST_DIR/fake-compose" \
WORKLOAD_REQUIRES_POSTGRES=0 \
UTILITY_OUTPUT_FILE="$OUTPUT_REL/docker-pg-dump.sql" \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pg-dump.env" >/dev/null
grep -q 'exec -T -e PGPASSWORD=postgres postgres pg_dump -h 127.0.0.1 -p 5432' "$TEST_DIR/compose.log"
grep -q '^pg_dump -h 127.0.0.1 -p 5432 -U postgres -d pg_experiment_workbench --schema smoke$' "$TEST_DIR/docker-commands.log"
test -s "$OUTPUT_DIR/docker-pg-dump.sql"

cp "$REPO_DIR/Makefile" "$TEST_DIR/Makefile.before"
if "${COMMON_ENV[@]}" UTILITY_OUTPUT_FILE=Makefile \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pg-dump.env" > "$TEST_DIR/source-output.out" 2>&1; then
  echo 'FAIL: utility adapter accepted a source file as output' >&2
  exit 1
fi
grep -q 'must be under logs/utility/ or .tmp/utility-output/' "$TEST_DIR/source-output.out"
cmp "$REPO_DIR/Makefile" "$TEST_DIR/Makefile.before"

if "${COMMON_ENV[@]}" UTILITY_OUTPUT_FILE=.git/config \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pg-dump.env" > "$TEST_DIR/git-output.out" 2>&1; then
  echo 'FAIL: utility adapter accepted Git metadata as output' >&2
  exit 1
fi
grep -q 'must be under logs/utility/ or .tmp/utility-output/' "$TEST_DIR/git-output.out"

ln -s "$TEST_DIR/escaped.sql" "$OUTPUT_DIR/symlink.sql"
if "${COMMON_ENV[@]}" UTILITY_OUTPUT_FILE="$OUTPUT_REL/symlink.sql" \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pg-dump.env" > "$TEST_DIR/symlink.out" 2>&1; then
  echo 'FAIL: utility adapter accepted a symlink output' >&2
  exit 1
fi
grep -q 'UTILITY_OUTPUT_FILE must not be a symlink' "$TEST_DIR/symlink.out"
[[ ! -e "$TEST_DIR/escaped.sql" ]]

printf 'preserve-on-failure\n' > "$OUTPUT_DIR/atomic.sql"
if "${COMMON_ENV[@]}" FAKE_PG_DUMP_FAIL=1 UTILITY_OUTPUT_FILE="$OUTPUT_REL/atomic.sql" \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/pg-dump.env" > "$TEST_DIR/atomic.out" 2>&1; then
  echo 'FAIL: failing utility adapter returned success' >&2
  exit 1
fi
grep -q '^preserve-on-failure$' "$OUTPUT_DIR/atomic.sql"

for kind in compose noisia; do
  if "${COMMON_ENV[@]}" \
    "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/$kind.env" > "$TEST_DIR/$kind.out" 2>&1; then
    echo "FAIL: native runtime accepted Docker-only workload kind: $kind" >&2
    exit 1
  fi
  grep -q 'requires PGWORKBENCH_RUNTIME=docker' "$TEST_DIR/$kind.out"
done

if PGWORKBENCH_RUNTIME=invalid WORKLOAD_RUN_LOG=0 \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/shell.env" > "$TEST_DIR/invalid.out" 2>&1; then
  echo "FAIL: workload runner accepted an unknown runtime" >&2
  exit 1
fi
grep -q 'Unsupported PGWORKBENCH_RUNTIME' "$TEST_DIR/invalid.out"

echo "PASS: native workload adapters"
