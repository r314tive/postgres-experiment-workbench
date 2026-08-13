#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "$TEST_ROOT"' EXIT

mkdir -p "$TEST_ROOT/runner/scripts" "$TEST_ROOT/run/artifacts/benchmark"
cp "$PROJECT_DIR/scripts/capture_effective_pg_settings.sh" "$TEST_ROOT/runner/scripts/"

cat > "$TEST_ROOT/runner/scripts/psql.sh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
printf '%s\n' \
  "ab-a-t001\tsha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\t1\t2026-08-12T00:00:01.000000Z\t170009\tshared_buffers\t16384\t8kB\tconfiguration file\tf\tpostmaster" \
  "ab-a-t001\tsha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\t1\t2026-08-12T00:00:01.000000Z\t170009\twork_mem\t4096\tkB\tdefault\tf\tuser"
STUB
chmod +x "$TEST_ROOT/runner/scripts/psql.sh"

REPO_DIR="$TEST_ROOT/runner"
RUN_DIR="$TEST_ROOT/run"
RUN_ID=ab-a-t001
PGWORKBENCH_BENCHMARK_RUN_ID=ab-a-t001
PGWORKBENCH_BENCHMARK_TRIAL=1
PGWORKBENCH_AB_PROTOCOL_DIGEST="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
PGWORKBENCH_AB_EFFECTIVE_SETTING_NAMES="shared_buffers,work_mem"
source "$PROJECT_DIR/scripts/capture_effective_pg_settings.sh"

capture_effective_pg_settings
output="$RUN_DIR/artifacts/benchmark/effective-pg-settings.tsv"
[[ -f "$output" && ! -L "$output" && "$(wc -l < "$output" | tr -d '[:space:]')" = 3 ]]
head -n 1 "$output" | grep -q $'^run_id\tprotocol_digest\ttrial\tcaptured_at\tserver_version_num\tname\tsetting\tunit\tsource\tpending_restart\tcontext$'

if capture_effective_pg_settings >/dev/null 2>&1; then
  echo "effective pg_settings collector overwrote immutable evidence" >&2
  exit 1
fi

empty_run="$TEST_ROOT/empty-run"
mkdir -p "$empty_run/artifacts/benchmark"
RUN_DIR="$empty_run"
PGWORKBENCH_AB_EFFECTIVE_SETTING_NAMES=""
capture_effective_pg_settings
[[ ! -e "$empty_run/artifacts/benchmark/effective-pg-settings.tsv" ]]

unsafe_run="$TEST_ROOT/unsafe-run"
mkdir -p "$unsafe_run/artifacts/benchmark"
RUN_DIR="$unsafe_run"
PGWORKBENCH_AB_EFFECTIVE_SETTING_NAMES="work_mem,shared_buffers"
if capture_effective_pg_settings >/dev/null 2>&1; then
  echo "effective pg_settings collector accepted unsorted names" >&2
  exit 1
fi

echo "PASS: effective pg_settings collector"
