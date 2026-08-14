#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-metrics-readiness.XXXXXX")"
FIXTURE_ROOT="$TMP_DIR/repo"
READY_DIR="$TMP_DIR/ready"
RUN_PID=""

cleanup() {
  if [[ -n "$RUN_PID" ]] && kill -0 "$RUN_PID" 2>/dev/null; then
    kill -TERM "$RUN_PID" 2>/dev/null || true
    wait "$RUN_PID" 2>/dev/null || true
  fi
  rm -rf -- "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$FIXTURE_ROOT/scripts" "$FIXTURE_ROOT/sql" "$READY_DIR"
cp "$REPO_DIR/scripts/sample_metrics.sh" "$FIXTURE_ROOT/scripts/sample_metrics.sh"
: > "$FIXTURE_ROOT/sql/metrics_sample.sql"

cat > "$FIXTURE_ROOT/scripts/psql.sh" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

case "${FAKE_PSQL_MODE:-success}" in
  success)
    printf '%s\n' "${FAKE_METRICS_ROW:?}"
    ;;
  partial-then-success)
    printf '%s' "${FAKE_METRICS_PREFIX:?}"
    : > "${FAKE_PSQL_PAUSED_FILE:?}"
    wait_count=0
    while [[ ! -e "${FAKE_PSQL_RELEASE_FILE:?}" ]]; do
      wait_count=$((wait_count + 1))
      if (( wait_count > 500 )); then
        echo "fake psql timed out waiting for release" >&2
        exit 97
      fi
      sleep 0.01
    done
    printf '%s\n' "${FAKE_METRICS_SUFFIX:?}"
    ;;
  failure)
    printf '%s' 'partial-sample'
    exit 23
    ;;
  race-regular)
    printf '%s\n' "${FAKE_METRICS_ROW:?}"
    printf '%s\n' 'raced-regular' > "${METRICS_READY_FILE:?}"
    ;;
  race-symlink)
    printf '%s\n' "${FAKE_METRICS_ROW:?}"
    printf '%s\n' 'raced-symlink-target' > "${FAKE_SYMLINK_TARGET:?}"
    ln -s "${FAKE_SYMLINK_TARGET:?}" "${METRICS_READY_FILE:?}"
    ;;
  race-fifo)
    printf '%s\n' "${FAKE_METRICS_ROW:?}"
    mkfifo "${METRICS_READY_FILE:?}"
    ;;
  race-empty-directory)
    printf '%s\n' "${FAKE_METRICS_ROW:?}"
    mkdir -m 755 "${METRICS_READY_FILE:?}"
    ;;
  race-nonempty-directory)
    printf '%s\n' "${FAKE_METRICS_ROW:?}"
    mkdir -m 755 "${METRICS_READY_FILE:?}"
    printf '%s\n' 'raced-directory' > "${METRICS_READY_FILE:?}/sentinel"
    ;;
  *)
    echo "unknown FAKE_PSQL_MODE: ${FAKE_PSQL_MODE:-}" >&2
    exit 98
    ;;
esac
SCRIPT
chmod +x "$FIXTURE_ROOT/scripts/psql.sh"

# Model BSD `mkdir -m`, which may create the directory and then apply the mode
# with a path-based chmod. If a readiness consumer removes the visible token in
# that window, the producer must not be using this two-step publication shape.
FAKE_MKDIR_BIN="$TMP_DIR/fake-mkdir-bin"
REAL_MKDIR="$(command -v mkdir)"
mkdir -p "$FAKE_MKDIR_BIN"
cat > "$FAKE_MKDIR_BIN/mkdir" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

mode_requested=0
last_arg=""
for arg in "$@"; do
  case "$arg" in
    -m|-m*) mode_requested=1 ;;
  esac
  last_arg="$arg"
done

if [[ "$mode_requested" = "1" && "$last_arg" = "${FAKE_MKDIR_READY_FILE:-}" ]]; then
  (umask 077; "${FAKE_REAL_MKDIR:?}" -- "$last_arg")
  : > "${FAKE_MKDIR_PUBLISHED_FILE:?}"
  wait_count=0
  while [[ -d "$last_arg" ]]; do
    wait_count=$((wait_count + 1))
    if (( wait_count > 500 )); then
      echo "fake mkdir timed out waiting for readiness consumption" >&2
      exit 97
    fi
    sleep 0.01
  done
  chmod 700 "$last_arg"
fi

exec "${FAKE_REAL_MKDIR:?}" "$@"
SCRIPT
chmod +x "$FAKE_MKDIR_BIN/mkdir"

METRICS_PREFIX='2026-08-13T00:00:00Z,pg_experiment_workbench'
METRICS_SUFFIX=',1,0,0,0,1,0,1,0,0,1,1,1,0,0,0,0,0,0,0,1,0,16,0/100'
METRICS_ROW="${METRICS_PREFIX}${METRICS_SUFFIX}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_no_ready_temps() {
  if find "$READY_DIR" -maxdepth 1 -name '.pgworkbench-metrics-ready.*' -print | grep -q .; then
    fail "temporary readiness state was retained"
  fi
}

path_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

wait_for_file() {
  local path="$1"
  local count=0
  while [[ ! -e "$path" ]]; do
    count=$((count + 1))
    if (( count > 500 )); then
      fail "timed out waiting for $path"
    fi
    sleep 0.01
  done
}

wait_bounded() {
  local count=0
  while kill -0 "$RUN_PID" 2>/dev/null; do
    count=$((count + 1))
    if (( count > 200 )); then
      kill -TERM "$RUN_PID" 2>/dev/null || true
      wait "$RUN_PID" 2>/dev/null || true
      RUN_PID=""
      fail "metrics sampler did not reject the readiness destination promptly"
    fi
    sleep 0.01
  done

  RUN_STATUS=0
  wait "$RUN_PID" || RUN_STATUS="$?"
  RUN_PID=""
}

run_sample() {
  local mode="$1"
  local ready_file="$2"
  local out_file="$3"
  local stdout_file="$4"
  local stderr_file="$5"

  METRICS_SAMPLES=1 \
  METRICS_READY_FILE="$ready_file" \
  FAKE_PSQL_MODE="$mode" \
  FAKE_METRICS_ROW="$METRICS_ROW" \
  FAKE_SYMLINK_TARGET="$TMP_DIR/raced-symlink-target" \
    "$BASH" "$FIXTURE_ROOT/scripts/sample_metrics.sh" "$out_file" \
      >"$stdout_file" 2>"$stderr_file" &
  RUN_PID="$!"
  wait_bounded
}

assert_rejected() {
  local expected_status="$1"
  local mode="$2"
  local ready_file="$3"
  local name="$4"

  run_sample "$mode" "$ready_file" "$TMP_DIR/$name.csv" \
    "$TMP_DIR/$name.stdout" "$TMP_DIR/$name.stderr"
  if [[ "$RUN_STATUS" != "$expected_status" ]]; then
    fail "$name readiness destination returned $RUN_STATUS, expected $expected_status"
  fi
  assert_no_ready_temps
}

# Without a marker capability, the standalone sampler keeps its legacy shape.
standalone_out="$TMP_DIR/standalone.csv"
METRICS_SAMPLES=1 \
FAKE_PSQL_MODE=success \
FAKE_METRICS_ROW="$METRICS_ROW" \
  "$BASH" "$FIXTURE_ROOT/scripts/sample_metrics.sh" "$standalone_out" >/dev/null
if [[ "$(wc -l < "$standalone_out" | tr -d ' ')" != "2" ]]; then
  fail "standalone sampler did not write exactly one complete sample"
fi
assert_no_ready_temps

# Consuming the token as soon as it becomes visible must not convert a valid
# publication into a nonzero producer exit. The mkdir shim makes the historical
# BSD `mkdir -m` create/chmod race deterministic while passing plain mkdir
# through unchanged.
consumed_ready="$READY_DIR/consumed-during-publication.ready"
consumed_out="$TMP_DIR/consumed-during-publication.csv"
METRICS_SAMPLES=1 \
METRICS_READY_FILE="$consumed_ready" \
FAKE_PSQL_MODE=success \
FAKE_METRICS_ROW="$METRICS_ROW" \
FAKE_MKDIR_READY_FILE="$consumed_ready" \
FAKE_MKDIR_PUBLISHED_FILE="$TMP_DIR/fake-mkdir-published" \
FAKE_REAL_MKDIR="$REAL_MKDIR" \
PATH="$FAKE_MKDIR_BIN:$PATH" \
  "$BASH" "$FIXTURE_ROOT/scripts/sample_metrics.sh" "$consumed_out" \
    >"$TMP_DIR/consumed.stdout" 2>"$TMP_DIR/consumed.stderr" &
RUN_PID="$!"
wait_for_file "$consumed_ready"
rmdir -- "$consumed_ready"
wait_bounded
if [[ "$RUN_STATUS" != "0" || -e "$consumed_ready" || -L "$consumed_ready" ]]; then
  fail "consumed readiness publication exited $RUN_STATUS"
fi
if [[ "$(wc -l < "$consumed_out" | tr -d ' ')" != "2" ]]; then
  fail "consumed readiness publication did not retain one complete sample"
fi
assert_no_ready_temps

# Marker publication is an opt-in absolute-path capability.
relative_status=0
(
  cd "$TMP_DIR"
  METRICS_SAMPLES=1 \
  METRICS_READY_FILE=relative.ready \
  FAKE_PSQL_MODE=success \
  FAKE_METRICS_ROW="$METRICS_ROW" \
    "$BASH" "$FIXTURE_ROOT/scripts/sample_metrics.sh" "$TMP_DIR/relative.csv"
) >"$TMP_DIR/relative.stdout" 2>"$TMP_DIR/relative.stderr" || relative_status="$?"
if [[ "$relative_status" != "2" || -e "$TMP_DIR/relative.ready" || -L "$TMP_DIR/relative.ready" ]]; then
  fail "relative readiness token was accepted"
fi
assert_no_ready_temps

# Readiness is invisible while psql has emitted only a partial sample.
ready="$READY_DIR/success.ready"
sample_out="$TMP_DIR/success.csv"
paused="$TMP_DIR/psql.paused"
release="$TMP_DIR/psql.release"
METRICS_SAMPLES=1 \
METRICS_READY_FILE="$ready" \
FAKE_PSQL_MODE=partial-then-success \
FAKE_METRICS_PREFIX="$METRICS_PREFIX" \
FAKE_METRICS_SUFFIX="$METRICS_SUFFIX" \
FAKE_PSQL_PAUSED_FILE="$paused" \
FAKE_PSQL_RELEASE_FILE="$release" \
  "$BASH" "$FIXTURE_ROOT/scripts/sample_metrics.sh" "$sample_out" \
    >"$TMP_DIR/success.stdout" 2>"$TMP_DIR/success.stderr" &
RUN_PID="$!"
wait_for_file "$paused"
if [[ -e "$ready" || -L "$ready" ]]; then
  fail "readiness was published before the first psql sample completed"
fi
: > "$release"
wait_bounded
if [[ "$RUN_STATUS" != "0" ]]; then
  fail "successful sampler exited $RUN_STATUS"
fi
if [[ ! -d "$ready" || -L "$ready" ]]; then
  fail "readiness token is not a real directory"
fi
if find "$ready" -mindepth 1 -maxdepth 1 -print | grep -q .; then
  fail "readiness token directory is not empty"
fi
if [[ "$(path_mode "$ready")" != "700" ]]; then
  fail "readiness token mode is not 0700"
fi
if ! awk -F ',' '
  NR == 1 { columns = NF; next }
  NR == 2 && NF != columns { bad = 1 }
  END { exit !(NR == 2 && !bad) }
' "$sample_out"; then
  fail "successful psql sample was incomplete when readiness was published"
fi
assert_no_ready_temps

# A failed/partial psql invocation never publishes readiness.
failed_ready="$READY_DIR/failed.ready"
assert_rejected 23 failure "$failed_ready" failed
if [[ -e "$failed_ready" || -L "$failed_ready" ]]; then
  fail "failed psql sample published readiness"
fi

# Every destination present at preflight is rejected without being touched.
regular_ready="$READY_DIR/preexisting-regular.ready"
printf '%s\n' 'regular-sentinel' > "$regular_ready"
assert_rejected 2 success "$regular_ready" preexisting-regular
if [[ "$(<"$regular_ready")" != "regular-sentinel" ]]; then
  fail "pre-existing regular readiness destination was changed"
fi

symlink_target="$TMP_DIR/preexisting-symlink-target"
symlink_ready="$READY_DIR/preexisting-symlink.ready"
printf '%s\n' 'symlink-sentinel' > "$symlink_target"
ln -s "$symlink_target" "$symlink_ready"
assert_rejected 2 success "$symlink_ready" preexisting-symlink
if [[ ! -L "$symlink_ready" || "$(<"$symlink_target")" != "symlink-sentinel" ]]; then
  fail "pre-existing symlink readiness destination was changed"
fi

fifo_ready="$READY_DIR/preexisting-fifo.ready"
mkfifo "$fifo_ready"
assert_rejected 2 success "$fifo_ready" preexisting-fifo
if [[ ! -p "$fifo_ready" ]]; then
  fail "pre-existing FIFO readiness destination was changed"
fi

empty_dir_ready="$READY_DIR/preexisting-empty-directory.ready"
mkdir -m 755 "$empty_dir_ready"
assert_rejected 2 success "$empty_dir_ready" preexisting-empty-directory
if [[ ! -d "$empty_dir_ready" || "$(path_mode "$empty_dir_ready")" != "755" ]]; then
  fail "pre-existing empty directory readiness destination was changed"
fi

nonempty_dir_ready="$READY_DIR/preexisting-nonempty-directory.ready"
mkdir -m 755 "$nonempty_dir_ready"
printf '%s\n' 'directory-sentinel' > "$nonempty_dir_ready/sentinel"
assert_rejected 2 success "$nonempty_dir_ready" preexisting-nonempty-directory
if [[ "$(<"$nonempty_dir_ready/sentinel")" != "directory-sentinel" ]]; then
  fail "pre-existing non-empty directory readiness destination was changed"
fi

# Destinations created after preflight still win without being overwritten.
raced_regular="$READY_DIR/raced-regular.ready"
assert_rejected 1 race-regular "$raced_regular" raced-regular
if [[ "$(<"$raced_regular")" != "raced-regular" ]]; then
  fail "raced regular readiness destination was changed"
fi

raced_symlink="$READY_DIR/raced-symlink.ready"
assert_rejected 1 race-symlink "$raced_symlink" raced-symlink
if [[ ! -L "$raced_symlink" || "$(<"$TMP_DIR/raced-symlink-target")" != "raced-symlink-target" ]]; then
  fail "raced symlink readiness destination was changed"
fi

# The FIFO regression proves publication does not open and block on the target.
raced_fifo="$READY_DIR/raced-fifo.ready"
assert_rejected 1 race-fifo "$raced_fifo" raced-fifo
if [[ ! -p "$raced_fifo" ]]; then
  fail "raced FIFO readiness destination was changed"
fi

raced_empty_dir="$READY_DIR/raced-empty-directory.ready"
assert_rejected 1 race-empty-directory "$raced_empty_dir" raced-empty-directory
if [[ ! -d "$raced_empty_dir" || "$(path_mode "$raced_empty_dir")" != "755" ]]; then
  fail "raced empty directory readiness destination was changed"
fi

raced_nonempty_dir="$READY_DIR/raced-nonempty-directory.ready"
assert_rejected 1 race-nonempty-directory "$raced_nonempty_dir" raced-nonempty-directory
if [[ "$(<"$raced_nonempty_dir/sentinel")" != "raced-directory" ]]; then
  fail "raced non-empty directory readiness destination was changed"
fi

echo "PASS: legacy metrics readiness directory publication"
