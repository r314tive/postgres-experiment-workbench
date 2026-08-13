#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-runtime.XXXXXX")"
TEST_DIR="$(cd "$TEST_DIR" && pwd -P)"

cleanup() {
  chmod -R u+w "$TEST_DIR" 2>/dev/null || true
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

mkdir -p "$TEST_DIR/scripts" "$TEST_DIR/fake-bin"
cp "$REPO_DIR/scripts/runtime.sh" "$TEST_DIR/scripts/runtime.sh"
cp "$REPO_DIR/scripts/native_runtime.sh" "$TEST_DIR/scripts/native_runtime.sh"
cp "$REPO_DIR/scripts/guard_local_pg.sh" "$TEST_DIR/scripts/guard_local_pg.sh"
chmod +x "$TEST_DIR/scripts/"*.sh

cat > "$TEST_DIR/.env.example" <<'ENV'
POSTGRES_HOST=127.0.0.1
POSTGRES_PORT=56543
POSTGRES_DB=pg_experiment_workbench
POSTGRES_USER=postgres
POSTGRES_PASSWORD=postgres
ALLOW_NONLOCAL_PG=0
ALLOW_SYSTEM_DB=0
TOPOLOGY=single
PGHOSTADDR=203.0.113.99
PGSERVICE=unsafe
ENV

cat > "$TEST_DIR/scripts/topology.sh" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${FAKE_DOCKER_LOG:?}"
if [[ "${1:-}" = "list" ]]; then
  printf 'docker-single\n'
fi
SCRIPT
chmod +x "$TEST_DIR/scripts/topology.sh"

cat > "$TEST_DIR/fake-bin/fake-postgres-command" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

command_name="$(basename "$0")"
data_dir="${FAKE_NATIVE_DATA_DIR:?}"

case "$command_name" in
  pg_isready|psql|createdb)
    for name in PGHOSTADDR PGSERVICE PGSERVICEFILE PGPASSFILE PGOPTIONS PGTARGETSESSIONATTRS PGSSLMODE; do
      if [[ ${!name+x} ]]; then
        echo "unsafe native libpq variable survived: $name" >&2
        exit 17
      fi
    done
    ;;
esac

case "$command_name" in
  initdb)
    target=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        -D)
          target="$2"
          shift 2
          ;;
        *)
          shift
          ;;
      esac
    done
    [[ "$target" = "$data_dir" ]]
    mkdir -p "$target"
    printf '16\n' > "$target/PG_VERSION"
    : > "$target/postgresql.conf"
    ;;
  pg_ctl)
    target=""
    action=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        -D)
          target="$2"
          shift 2
          ;;
        start|stop|restart|status)
          action="$1"
          shift
          ;;
        *)
          shift
          ;;
      esac
    done
    [[ "$target" = "$data_dir" ]]
    case "$action" in
      start)
        : > "$data_dir/.fake-running"
        ;;
      stop)
        rm -f -- "$data_dir/.fake-running"
        ;;
      restart)
        [[ -f "$data_dir/.fake-running" ]]
        : > "$data_dir/.fake-running"
        ;;
      status)
        [[ -f "$data_dir/.fake-running" ]]
        ;;
      *)
        exit 2
        ;;
    esac
    ;;
  pg_isready)
    if [[ "${FAKE_PORT_BUSY:-0}" = "1" ]]; then
      exit 0
    fi
    [[ -f "$data_dir/.fake-running" ]] && exit 0
    exit 2
    ;;
  psql)
    [[ -f "$data_dir/.fake-running" ]]
    case "$*" in
      *"SHOW data_directory"*)
        printf '%s\n' "$data_dir"
        ;;
      *"FROM pg_database"*)
        [[ -f "$data_dir/.fake-database" ]] && printf '1\n'
        ;;
    esac
    ;;
  createdb)
    [[ -f "$data_dir/.fake-running" ]]
    : > "$data_dir/.fake-database"
    ;;
  *)
    exit 2
    ;;
esac
SCRIPT
chmod +x "$TEST_DIR/fake-bin/fake-postgres-command"
for command_name in initdb pg_ctl createdb pg_isready psql; do
  cp "$TEST_DIR/fake-bin/fake-postgres-command" "$TEST_DIR/fake-bin/$command_name"
  chmod +x "$TEST_DIR/fake-bin/$command_name"
done

RUNTIME="$TEST_DIR/scripts/runtime.sh"
FAKE_DATA="$TEST_DIR/.tmp/native/single/data"
NATIVE_ENV=(
  env
  PGWORKBENCH_RUNTIME=native
  PGWORKBENCH_NATIVE_BINDIR="$TEST_DIR/fake-bin"
  FAKE_NATIVE_DATA_DIR="$FAKE_DATA"
)

"${NATIVE_ENV[@]}" "$RUNTIME" up single >/dev/null
[[ -f "$FAKE_DATA/PG_VERSION" ]]
[[ -f "$FAKE_DATA/.fake-database" ]]
grep -q "port = 56543" "$FAKE_DATA/postgresql.conf"
grep -q "data_dir=$FAKE_DATA" "$TEST_DIR/.tmp/native/single/runtime.state"

STATUS_OUTPUT="$("${NATIVE_ENV[@]}" "$RUNTIME" status single)"
grep -q '^state=running$' <<< "$STATUS_OUTPUT"
"${NATIVE_ENV[@]}" "$RUNTIME" wait single >/dev/null
"${NATIVE_ENV[@]}" "$RUNTIME" restart single >/dev/null
"${NATIVE_ENV[@]}" "$RUNTIME" down single >/dev/null

STATUS_OUTPUT="$("${NATIVE_ENV[@]}" "$RUNTIME" status single)"
grep -q '^state=stopped$' <<< "$STATUS_OUTPUT"

if POSTGRES_PORT=56544 "${NATIVE_ENV[@]}" "$RUNTIME" status single > "$TEST_DIR/mismatch.out" 2>&1; then
  echo "FAIL: native runtime accepted settings that differ from its state" >&2
  exit 1
fi
grep -q 'POSTGRES_PORT differs' "$TEST_DIR/mismatch.out"

"${NATIVE_ENV[@]}" "$RUNTIME" reset single >/dev/null
grep -q '^state=running$' < <("${NATIVE_ENV[@]}" "$RUNTIME" status single)
"${NATIVE_ENV[@]}" "$RUNTIME" down single >/dev/null

SOURCE_DATA="$TEST_DIR/.tmp/native/source-tree/data"
if env \
  PGWORKBENCH_RUNTIME=native \
  PGWORKBENCH_NATIVE_BINDIR="$TEST_DIR/fake-bin" \
  FAKE_NATIVE_DATA_DIR="$SOURCE_DATA" \
  FAKE_PORT_BUSY=1 \
  "$RUNTIME" up source-tree > "$TEST_DIR/busy.out" 2>&1; then
  echo "FAIL: native runtime adopted an occupied port" >&2
  exit 1
fi
grep -q 'already in use' "$TEST_DIR/busy.out"
[[ ! -e "$SOURCE_DATA/PG_VERSION" ]]

if env \
  PGWORKBENCH_RUNTIME=native \
  PGWORKBENCH_NATIVE_BINDIR="$TEST_DIR/fake-bin" \
  FAKE_NATIVE_DATA_DIR="$SOURCE_DATA" \
  POSTGRES_HOST=db.example.test \
  "$RUNTIME" status source-tree > "$TEST_DIR/remote.out" 2>&1; then
  echo "FAIL: native runtime accepted a non-loopback host" >&2
  exit 1
fi
grep -q 'requires POSTGRES_HOST' "$TEST_DIR/remote.out"

if env \
  PGWORKBENCH_RUNTIME=native \
  PGWORKBENCH_NATIVE_BINDIR="$TEST_DIR/fake-bin" \
  FAKE_NATIVE_DATA_DIR="$SOURCE_DATA" \
  "$RUNTIME" up primary-replica > "$TEST_DIR/topology.out" 2>&1; then
  echo "FAIL: native runtime accepted a multi-node topology" >&2
  exit 1
fi
grep -q 'supports only single and source-tree' "$TEST_DIR/topology.out"

DOCKER_LOG="$TEST_DIR/docker.log"
FAKE_DOCKER_LOG="$DOCKER_LOG" PGWORKBENCH_RUNTIME=docker "$RUNTIME" restart single >/dev/null
grep -q '^down single$' "$DOCKER_LOG"
grep -q '^up single$' "$DOCKER_LOG"

if PGWORKBENCH_RUNTIME=invalid "$RUNTIME" list > "$TEST_DIR/backend.out" 2>&1; then
  echo "FAIL: runtime dispatcher accepted an unknown backend" >&2
  exit 1
fi
grep -q 'Unsupported PGWORKBENCH_RUNTIME' "$TEST_DIR/backend.out"

assert_native_leaf_symlink_rejected() {
  local leaf="$1"
  local target
  target="$TEST_DIR/outside-$(printf '%s' "$leaf" | tr '/.' '__')"
  local runtime_root="$TEST_DIR/.tmp/native/source-tree"
  local link="$runtime_root/$leaf"

  rm -rf -- "$runtime_root"
  mkdir -p "$(dirname "$link")"
  ln -s "$target" "$link"
  if "${NATIVE_ENV[@]}" "$RUNTIME" up source-tree > "$TEST_DIR/leaf.out" 2>&1; then
    echo "FAIL: native runtime accepted symlinked writable leaf: $leaf" >&2
    exit 1
  fi
  grep -q 'Refusing symlink in native runtime file' "$TEST_DIR/leaf.out"
  [[ ! -e "$target" ]] || {
    echo "FAIL: native runtime wrote through symlinked leaf: $leaf" >&2
    exit 1
  }
}

assert_native_leaf_symlink_rejected runtime.state
assert_native_leaf_symlink_rejected .initdb-password
assert_native_leaf_symlink_rejected log/postgresql.log

rm -rf -- "$TEST_DIR/.tmp/native/source-tree"
mkdir -p "$TEST_DIR/.tmp/native/source-tree/data"
printf '16\n' > "$TEST_DIR/.tmp/native/source-tree/data/PG_VERSION"
ln -s "$TEST_DIR/outside-postgresql-conf" "$TEST_DIR/.tmp/native/source-tree/data/postgresql.conf"
if "${NATIVE_ENV[@]}" "$RUNTIME" up source-tree > "$TEST_DIR/config-leaf.out" 2>&1; then
  echo "FAIL: native runtime accepted symlinked postgresql.conf" >&2
  exit 1
fi
grep -q 'Refusing symlink in native runtime file' "$TEST_DIR/config-leaf.out"
[[ ! -e "$TEST_DIR/outside-postgresql-conf" ]]

echo "PASS: runtime dispatcher and native lifecycle"
