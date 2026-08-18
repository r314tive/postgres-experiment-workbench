#!/usr/bin/env bash
set -euo pipefail

export PGWORKBENCH_SUPERVISED=1
INTERNAL_RUN_ACTION=__pgworkbench_internal_run_v1

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-runs-root.XXXXXX")"
trap 'rm -rf -- "$TEST_DIR"' EXIT

PACK="$TEST_DIR/pack"
OUTSIDE="$TEST_DIR/outside"
mkdir -p "$PACK/scripts" "$PACK/experiments" "$OUTSIDE"
cp "$REPO_DIR/scripts/run_experiment.sh" "$PACK/scripts/run_experiment.sh"
cp "$REPO_DIR/scripts/exact_environment.sh" "$PACK/scripts/exact_environment.sh"
cp "$REPO_DIR/scripts/guard_local_pg.sh" "$PACK/scripts/guard_local_pg.sh"
cp "$REPO_DIR/scripts/target_arg_guard.sh" "$PACK/scripts/target_arg_guard.sh"
cp "$REPO_DIR/scripts/benchmark_phase.sh" "$PACK/scripts/benchmark_phase.sh"
cp "$REPO_DIR/scripts/benchmark_control.sh" "$PACK/scripts/benchmark_control.sh"
cp "$REPO_DIR/scripts/benchmark_capsule.sh" "$PACK/scripts/benchmark_capsule.sh"
cp "$REPO_DIR/scripts/capture_effective_pg_settings.sh" "$PACK/scripts/capture_effective_pg_settings.sh"
cp "$REPO_DIR/scripts/process_lifecycle.sh" "$PACK/scripts/process_lifecycle.sh"
chmod +x "$PACK/scripts/"*.sh
cat > "$PACK/experiments/smoke.env" <<'ENV'
EXPERIMENT_NAME="runs root guard"
EXPERIMENT_METRICS=0
EXPERIMENT_SNAPSHOT=0
ENV

assert_runs_root_rejected() {
  local label="$1"

  if EXPERIMENT_RUN_ID="runs-root-$label" \
    "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" smoke \
    > "$TEST_DIR/$label.out" 2>&1; then
    echo "FAIL: unsafe experiment runs root was accepted: $label" >&2
    exit 1
  fi
  grep -Eq 'Refusing (symlinked|unsafe) experiment runs root|Experiment runs root is not a directory' "$TEST_DIR/$label.out"
}

ln -s "$OUTSIDE" "$PACK/runs"
assert_runs_root_rejected symlink
[[ -z "$(find "$OUTSIDE" -mindepth 1 -print -quit)" ]]

rm "$PACK/runs"
ln -s "$TEST_DIR/missing-target" "$PACK/runs"
assert_runs_root_rejected broken-symlink
[[ ! -e "$TEST_DIR/missing-target" ]]

rm "$PACK/runs"
printf 'not a directory\n' > "$PACK/runs"
assert_runs_root_rejected regular-file

echo "PASS: experiment runs root containment"
