#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SERIES_ID="capsule-revalidation-$$"
CAPSULE_ROOT="$REPO_DIR/runs/benchmarks/$SERIES_ID/protocol/capsule"
WORKLOAD_ID="self-mutating"
WORKLOAD_FILE="$CAPSULE_ROOT/workloads/$WORKLOAD_ID.env"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-capsule-test.XXXXXX")"

cleanup() {
  rm -rf -- "$REPO_DIR/runs/benchmarks/$SERIES_ID" "$TMP_DIR"
}
trap cleanup EXIT

mkdir -p "$CAPSULE_ROOT/workloads"
cat > "$WORKLOAD_FILE" <<'SPEC'
WORKLOAD_NAME="$(printf '%s\n' 'WORKLOAD_NAME=mutated' 'WORKLOAD_KIND=shell' 'WORKLOAD_REQUIRES_POSTGRES=0' 'WORKLOAD_CMD=true' > "$PGWORKBENCH_BENCHMARK_CAPSULE_ROOT/workloads/self-mutating.env"; printf self-mutating)"
WORKLOAD_KIND="shell"
WORKLOAD_REQUIRES_POSTGRES=0
WORKLOAD_CMD=true
SPEC

if command -v shasum >/dev/null 2>&1; then
  digest="$(shasum -a 256 -- "$WORKLOAD_FILE" | awk '{print $1}')"
else
  digest="$(sha256sum -- "$WORKLOAD_FILE" | awk '{print $1}')"
fi

if env \
  ENV_FILE="$REPO_DIR/.env.example" \
  PGWORKBENCH_RUNTIME=native \
  PGWORKBENCH_BENCHMARK_CAPSULE_ROOT="$CAPSULE_ROOT" \
  PGWORKBENCH_BENCHMARK_SERIES_ID="$SERIES_ID" \
  PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID="$WORKLOAD_ID" \
  PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_DIGEST="sha256:$digest" \
  WORKLOAD_RUN_LOG=0 \
  "$REPO_DIR/scripts/run_workload.sh" run "$WORKLOAD_ID" \
  >"$TMP_DIR/out" 2>&1; then
  echo "FAIL: a workload capsule that mutated itself while sourced was accepted" >&2
  exit 1
fi

grep -q 'Benchmark capsule input digest mismatch: workloads/self-mutating.env' "$TMP_DIR/out"
echo "PASS: benchmark capsule inputs are revalidated after sourcing"
