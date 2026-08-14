#!/usr/bin/env bash
set -euo pipefail

export PGWORKBENCH_SUPERVISED=1
INTERNAL_RUN_ACTION=__pgworkbench_internal_run_v1

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-benchmark-preflight.XXXXXX")"
SPEC_DIR="$(mktemp -d "$REPO_DIR/experiments/.benchmark-preflight.XXXXXX")"
RUN_IDS=()

cleanup() {
  local run_id
  for run_id in "${RUN_IDS[@]}"; do
    rm -rf -- "$REPO_DIR/runs/$run_id"
  done
  rm -rf -- "$TMP_DIR" "$SPEC_DIR"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

GOCACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}" \
GOMODCACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}" \
  go build -o "$TMP_DIR/pgworkbench" ./cmd/pgworkbench

assert_phase_journal() {
  local journal="$1"

  [[ "$(wc -l < "$journal" | tr -d ' ')" = "11" ]] || fail "phase journal does not contain exactly eleven events: $journal"
  awk -F '\t' '
    NR == 1 && !($3 == 1 && $4 == "preflight" && $5 == "failed" && $8 != "") { exit 1 }
    NR >= 2 && NR <= 10 && !($3 == NR && $5 == "skipped" && $8 != "") { exit 1 }
    NR == 11 && !($3 == 11 && $4 == "cleanup" && ($5 == "passed" || $5 == "failed")) { exit 1 }
    END { if (NR != 11) exit 1 }
  ' "$journal" || fail "phase journal does not describe a terminal preflight failure: $journal"
}

assert_terminal_preflight_failure() {
  local label="$1"
  local spec="$2"
  shift 2
  local run_id="benchmark-preflight-$label-$$"
  local run_dir="$REPO_DIR/runs/$run_id"
  local journal="$TMP_DIR/$label.tsv"

  RUN_IDS+=("$run_id")
  : > "$journal"
  if env \
    PGWORKBENCH_BIN="$TMP_DIR/pgworkbench" \
    PGWORKBENCH_RUNTIME=native \
    PGWORKBENCH_BENCHMARK_PHASE_FILE="$journal" \
    PGWORKBENCH_BENCHMARK_RUN_ID="$run_id" \
    PGWORKBENCH_BENCHMARK_TRIAL=1 \
    EXPERIMENT_RUN_ID="$run_id" \
    "$@" \
    "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$spec" \
    >"$TMP_DIR/$label.out" 2>&1; then
    fail "$label preflight unexpectedly passed"
  fi

  assert_phase_journal "$journal"
  awk -F '\t' -v run_id="$run_id" 'NF != 8 || $1 != run_id || $2 != 1 { exit 1 }' "$journal" || fail "$label journal rows are not bound to the exact run/trial"
  cmp -s "$journal" "$run_dir/artifacts/benchmark/phases.tsv" || fail "$label linked journal and series mirror differ"
  [[ -s "$run_dir/manifest.env" && -s "$run_dir/verdict.env" && -s "$run_dir/verdict.json" ]] ||
    fail "$label preflight did not publish a terminal linked run"
  grep -q '^metrics_enabled="0"$' "$run_dir/manifest.env" || fail "$label preflight manifest claims unavailable metrics"
  grep -q '"status": "failed"' "$run_dir/verdict.json" || fail "$label preflight verdict is not failed"
  "$TMP_DIR/pgworkbench" run verify "$run_dir" >/dev/null || fail "$label terminal run does not verify"
}

cat > "$TMP_DIR/repo-env-abort.env" <<ENV
RUN_ID=forged-run-id
RUN_DIR=$TMP_DIR/forged-run-dir
PGWORKBENCH_BENCHMARK_PHASE_FILE=$TMP_DIR/forged.tsv
PGWORKBENCH_BIN=/bin/false
EXPERIMENT_SPEC_ID=forged-spec
return 19
ENV

cat > "$SPEC_DIR/spec-abort.env" <<ENV
RUN_ID=forged-spec-run-id
RUN_DIR=$TMP_DIR/forged-spec-run-dir
PGWORKBENCH_BENCHMARK_PHASE_FILE=$TMP_DIR/forged-spec.tsv
PGWORKBENCH_BIN=/bin/false
EXPERIMENT_SPEC_ID=forged-spec
return 23
ENV

cat > "$SPEC_DIR/untrusted-hook.env" <<'ENV'
EXPERIMENT_NAME="untrusted benchmark preflight hook"
EXPERIMENT_BEFORE_SHELL=true
ENV

cat > "$SPEC_DIR/ownership-override.env" <<ENV
EXPERIMENT_NAME="benchmark ownership override"
RUN_ID=forged-success-run-id
RUN_DIR=$TMP_DIR/forged-success-run-dir
STARTED_AT=2099-01-01T00:00:00Z
PGWORKBENCH_BENCHMARK_PHASE_FILE=$TMP_DIR/forged-success.tsv
PGWORKBENCH_BIN=/bin/false
EXPERIMENT_STATE_WRITER=shell
ENV

assert_terminal_preflight_failure repo-env smoke ENV_FILE="$TMP_DIR/repo-env-abort.env"
assert_terminal_preflight_failure load-spec "$SPEC_DIR/spec-abort.env"
[[ ! -e "$TMP_DIR/forged-run-dir" && ! -e "$TMP_DIR/forged-spec-run-dir" ]] || fail "sourced input redirected terminal evidence"
[[ ! -e "$TMP_DIR/forged.tsv" && ! -e "$TMP_DIR/forged-spec.tsv" ]] || fail "sourced input redirected the phase journal"
assert_terminal_preflight_failure state-writer smoke EXPERIMENT_STATE_WRITER=shell
assert_terminal_preflight_failure target-guard smoke POSTGRES_HOST=benchmark.invalid
assert_terminal_preflight_failure hook-trust "$SPEC_DIR/untrusted-hook.env"
assert_terminal_preflight_failure ownership-override "$SPEC_DIR/ownership-override.env"
[[ ! -e "$TMP_DIR/forged-success-run-dir" && ! -e "$TMP_DIR/forged-success.tsv" ]] || fail "successful spec load redirected benchmark ownership"

# The canonical run id is immutable. A conflicting directory cannot become a
# truthful new linked artifact without overwriting prior evidence, so retain it
# byte-for-byte and keep this attempt's terminal lifecycle in the series-owned
# journal instead.
conflict_id="benchmark-preflight-existing-$$"
conflict_dir="$REPO_DIR/runs/$conflict_id"
conflict_journal="$TMP_DIR/existing.tsv"
RUN_IDS+=("$conflict_id")
mkdir "$conflict_dir"
printf 'immutable evidence\n' > "$conflict_dir/sentinel"
: > "$conflict_journal"
if env \
  PGWORKBENCH_BIN="$TMP_DIR/pgworkbench" \
  PGWORKBENCH_RUNTIME=native \
  PGWORKBENCH_BENCHMARK_PHASE_FILE="$conflict_journal" \
  PGWORKBENCH_BENCHMARK_RUN_ID="$conflict_id" \
  PGWORKBENCH_BENCHMARK_TRIAL=1 \
  EXPERIMENT_RUN_ID="$conflict_id" \
  "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" smoke \
  >"$TMP_DIR/existing.out" 2>&1; then
  fail "existing immutable run unexpectedly passed"
fi
assert_phase_journal "$conflict_journal"
[[ "$(find "$conflict_dir" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d ' ')" = "1" ]] || fail "existing run was modified"
grep -q '^immutable evidence$' "$conflict_dir/sentinel" || fail "existing run evidence was overwritten"

echo "PASS: benchmark shell owns preflight failures and terminal linked artifacts"
