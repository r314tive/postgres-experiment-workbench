#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-pgbench-phase-io.XXXXXX")"
TEST_DIR="$(cd "$TEST_DIR" && pwd -P)"

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

mkdir -p "$TEST_DIR/container" "$TEST_DIR/trial-io"
printf '\\set aid random(1, 10)\nSELECT :aid;\n' > "$TEST_DIR/custom.sql"
touch "$TEST_DIR/phases.tsv"

cat > "$TEST_DIR/workload.env" <<ENV
WORKLOAD_NAME="phase-owned pgbench I/O"
WORKLOAD_KIND="pgbench"
WORKLOAD_REQUIRES_POSTGRES=0
PGBENCH_INIT=0
PGBENCH_RESET=0
PGBENCH_CLIENTS=1
PGBENCH_THREADS=1
PGBENCH_TRANSACTIONS=1
PGBENCH_WARMUP_TIME=0
PGBENCH_SCRIPT="$TEST_DIR/custom.sql"
PGBENCH_LOG_TRANSACTIONS=1
PGBENCH_LOG_SAMPLE_RATE=1
ENV

cat > "$TEST_DIR/fake-compose" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

root="${FAKE_CONTAINER_ROOT:?}"
printf '%s\n' "$*" >> "${FAKE_COMPOSE_LOG:?}"

if [[ "${1:-}" = "--env-file" ]]; then
  shift 2
fi

case "${1:-}" in
  images)
    if [[ "${2:-}" = "-q" && "${3:-}" = "postgres" && $# -eq 3 ]]; then
      printf 'sha256:%064d\n' 1
      exit 0
    fi
    if [[ "${2:-}" = "-q" && "${3:-}" = "pgbouncer" && $# -eq 3 ]]; then
      printf 'sha256:%064d\n' 2
      exit 0
    fi
    echo "unsupported fake compose images request: $*" >&2
    exit 2
    ;;
  cp)
    source_path="${2:?}"
    destination="${3:?}"
    if [[ "$destination" = postgres:* ]]; then
      container_path="${destination#postgres:}"
      mkdir -p "$root$(dirname "$container_path")"
      cp -- "$source_path" "$root$container_path"
      exit 0
    fi
    if [[ "$source_path" = postgres:* ]]; then
      container_path="${source_path#postgres:}"
      container_path="${container_path%/.}"
      mkdir -p "$destination"
      cp -R -- "$root$container_path/." "$destination"
      exit 0
    fi
    echo "unsupported fake compose cp: $*" >&2
    exit 2
    ;;
  exec)
    shift
    while (( $# > 0 )); do
      case "$1" in
        -T)
          shift
          ;;
        -e)
          shift 2
          ;;
        postgres)
          shift
          break
          ;;
        *)
          echo "unsupported fake compose exec option: $1" >&2
          exit 2
          ;;
      esac
    done
    command="${1:?missing container command}"
    shift
    case "$command" in
      mkdir)
        mkdir "$root${1:?missing container directory}"
        ;;
      sh)
        script_path="${@: -2:1}"
        raw_dir="${@: -1}"
        if [[ -n "$script_path" ]]; then
          rm -f -- "$root$script_path"
        fi
        if [[ -n "$raw_dir" && -d "$root$raw_dir" ]]; then
          find "$root$raw_dir" -type f -delete
          rmdir "$root$raw_dir"
        fi
        ;;
      pgbench)
        log_prefix=""
        for argument in "$@"; do
          case "$argument" in
            --log-prefix=*) log_prefix="${argument#--log-prefix=}" ;;
          esac
        done
        if [[ -n "$log_prefix" ]]; then
          mkdir -p "$root$(dirname "$log_prefix")"
          printf '1 2 3 0 4 5\n' > "$root${log_prefix}.123"
        fi
        if [[ "${FAKE_PGBENCH_FAIL:-0}" = "1" ]]; then
          exit 7
        fi
        printf 'latency average = 1.000 ms\ntps = 1000.000 (without initial connection time)\n'
        ;;
      *)
        echo "unsupported fake container command: $command" >&2
        exit 2
        ;;
    esac
    ;;
  *)
    echo "unsupported fake compose command: $*" >&2
    exit 2
    ;;
esac
SCRIPT
chmod +x "$TEST_DIR/fake-compose"

COMMON_ENV=(
  env
  ENV_FILE="$REPO_DIR/.env.example"
  COMPOSE="$TEST_DIR/fake-compose"
  FAKE_CONTAINER_ROOT="$TEST_DIR/container"
  FAKE_COMPOSE_LOG="$TEST_DIR/compose.log"
  PGWORKBENCH_RUNTIME=docker
  PGWORKBENCH_EXPERIMENT_MODE=1
  PGWORKBENCH_BENCHMARK_PHASE_FILE="$TEST_DIR/phases.tsv"
  PGWORKBENCH_BENCHMARK_RUN_ID=phase-io-t001
  PGWORKBENCH_BENCHMARK_TRIAL=1
  POSTGRES_HOST=127.0.0.1
  POSTGRES_DB=pg_experiment_workbench
  ALLOW_NONLOCAL_PG=0
  ALLOW_SYSTEM_DB=0
  WORKLOAD_LOG_FILE="$TEST_DIR/trial-io/workload.log"
  WORKLOAD_LOG_DIR="$TEST_DIR/trial-io"
  WORKLOAD_RUN_LOG=0
  PGBENCH_RESULT_FILE="$TEST_DIR/trial-io/driver/pgbench-summary.log"
  PGBENCH_RAW_LOG_DIR="$TEST_DIR/trial-io/driver/pgbench-raw"
)

"${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" prepare "$TEST_DIR/workload.env"
script_path="$(find "$TEST_DIR/container/tmp" -type f -name '*-script.sql')"
raw_container_dir="$(find "$TEST_DIR/container/tmp" -type d -name '*-raw')"
test -f "$script_path"
test -d "$raw_container_dir"
test -d "$TEST_DIR/trial-io/driver/pgbench-raw"
prepare_lines="$(wc -l < "$TEST_DIR/compose.log" | tr -d ' ')"

"${COMMON_ENV[@]}" PGWORKBENCH_BENCHMARK_PREPARED=1 \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/workload.env" > "$TEST_DIR/run.out"
run_lines="$(wc -l < "$TEST_DIR/compose.log" | tr -d ' ')"
if sed -n "$((prepare_lines + 1)),${run_lines}p" "$TEST_DIR/compose.log" | grep -Eq '(^| )cp |(^| )mkdir '; then
  echo "FAIL: pgbench run performed hidden script staging or raw-dir preparation" >&2
  exit 1
fi
test ! -e "$TEST_DIR/trial-io/driver/pgbench-raw/pgbench.123"
grep -q $'^phase-io-t001\t1\t4\tpre-warmup-control\tskipped\t' "$TEST_DIR/phases.tsv"
grep -q $'^phase-io-t001\t1\t5\twarmup\tskipped\t' "$TEST_DIR/phases.tsv"
grep -q $'^phase-io-t001\t1\t6\tpre-measure-control\tskipped\t' "$TEST_DIR/phases.tsv"
grep -q $'^phase-io-t001\t1\t7\tmeasure\tpassed\t' "$TEST_DIR/phases.tsv"
grep -q 'images -q postgres' "$TEST_DIR/compose.log"
grep -q 'driver_image_id=sha256:0000000000000000000000000000000000000000000000000000000000000001 target_image_id=sha256:0000000000000000000000000000000000000000000000000000000000000001' "$TEST_DIR/run.out"

"${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" collect "$TEST_DIR/workload.env"
test -s "$TEST_DIR/trial-io/driver/pgbench-raw/pgbench.123"
grep -q 'cp postgres:/tmp/pgworkbench-.*-raw/\. ' "$TEST_DIR/compose.log"

"${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" cleanup "$TEST_DIR/workload.env"
test ! -e "$script_path"
test ! -e "$raw_container_dir"
test -s "$TEST_DIR/trial-io/driver/pgbench-raw/pgbench.123"

# A failed measure still leaves phase-2 resources for the experiment's phase-11
# cleanup, and that cleanup must remove both resource classes.
: > "$TEST_DIR/phases.tsv"
rm -rf -- "$TEST_DIR/trial-io/driver"
"${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" prepare "$TEST_DIR/workload.env"
script_path="$(find "$TEST_DIR/container/tmp" -type f -name '*-script.sql')"
raw_container_dir="$(find "$TEST_DIR/container/tmp" -type d -name '*-raw')"
if "${COMMON_ENV[@]}" FAKE_PGBENCH_FAIL=1 PGWORKBENCH_BENCHMARK_PREPARED=1 \
  "$REPO_DIR/scripts/run_workload.sh" run "$TEST_DIR/workload.env" >/dev/null 2>&1; then
  echo "FAIL: fake failed pgbench measure returned success" >&2
  exit 1
fi
grep -q $'^phase-io-t001\t1\t7\tmeasure\tfailed\t' "$TEST_DIR/phases.tsv"
test -e "$script_path"
test -d "$raw_container_dir"
"${COMMON_ENV[@]}" "$REPO_DIR/scripts/run_workload.sh" cleanup "$TEST_DIR/workload.env"
test ! -e "$script_path"
test ! -e "$raw_container_dir"

echo "PASS: pgbench prepare, collect, and cleanup own their declared phase I/O"
