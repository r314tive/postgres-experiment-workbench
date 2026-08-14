#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-utility-provenance.XXXXXX")"
BIN="$TEST_DIR/pgworkbench"
CLI_INVALID_RUN_ID="provenance/invalid-$$"
CLI_INVALID_GENERATED="$REPO_DIR/.tmp/utility-tests/${CLI_INVALID_RUN_ID//\//_}.env"

cleanup() {
  rm -f -- "$CLI_INVALID_GENERATED"
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

sha256_digest_file() {
  local file="$1"
  local digest

  if command -v shasum >/dev/null 2>&1; then
    digest="$(shasum -a 256 -- "$file" | awk '{print $1}')"
  elif command -v sha256sum >/dev/null 2>&1; then
    digest="$(sha256sum -- "$file" | awk '{print $1}')"
  else
    echo "FAIL: shasum or sha256sum is required" >&2
    exit 2
  fi
  printf 'sha256:%s\n' "$digest"
}

GOCACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}" \
GOMODCACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}" \
  go build -o "$BIN" ./cmd/pgworkbench

# CLI validation is fail-closed before the translator creates its generated
# experiment spec or invokes the shell runner.
if PGWORKBENCH_BIN="$BIN" \
  "$BIN" utility run --runtime native --run-id "$CLI_INVALID_RUN_ID" pg-dump/smoke >"$TEST_DIR/cli-invalid.out" 2>&1; then
  echo "FAIL: utility run accepted an invalid run id" >&2
  exit 1
fi
grep -q "invalid run id \"$CLI_INVALID_RUN_ID\"" "$TEST_DIR/cli-invalid.out"
if [[ -e "$CLI_INVALID_GENERATED" || -L "$CLI_INVALID_GENERATED" ]]; then
  echo "FAIL: rejected utility run created a generated spec" >&2
  exit 1
fi
if [[ -e "$REPO_DIR/runs/$CLI_INVALID_RUN_ID" || -L "$REPO_DIR/runs/$CLI_INVALID_RUN_ID" ]]; then
  echo "FAIL: rejected utility run created a run directory" >&2
  exit 1
fi

# The remaining fixtures deliberately exercise the shell capability boundary
# in isolated incomplete packs. Utility runner tests prove that production uses
# the prepared Go route and an exact child environment; process-lifecycle tests
# independently prove that every public shell route delegates through Go.
export PGWORKBENCH_SUPERVISED=1
INTERNAL_RUN_ACTION=__pgworkbench_internal_run_v1

PACK="$TEST_DIR/pack"
mkdir -p "$PACK/scripts" "$PACK/experiments" "$PACK/utility-tests/example" "$PACK/.tmp/utility-tests"
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

SOURCE="$PACK/utility-tests/example/smoke.env"
GENERATED="$PACK/.tmp/utility-tests/derived.env"
printf 'UTILITY_TEST_NAME="provenance smoke"\n' > "$SOURCE"
cat > "$GENERATED" <<'ENV'
EXPERIMENT_NAME="derived provenance smoke"
EXPERIMENT_RUN_ID="invalid/run-id"
EXPERIMENT_STATE_WRITER="go"
EXPERIMENT_METRICS=0
EXPERIMENT_SNAPSHOT=0
ENV
SOURCE_DIGEST="$(sha256_digest_file "$SOURCE")"

DERIVED_ENV=(
  "PGWORKBENCH_BIN=$BIN"
  "PGWORKBENCH_EXPERIMENT_SPEC_SCOPE=utility-derived"
  "PGWORKBENCH_DERIVED_EXPERIMENT_ID=utility/example/smoke"
  "PGWORKBENCH_SOURCE_SPEC_KIND=utility-test"
  "PGWORKBENCH_SOURCE_SPEC_ID=example/smoke"
  "PGWORKBENCH_SOURCE_SPEC_REF=utility-tests/example/smoke.env"
  "PGWORKBENCH_SOURCE_SPEC_DIGEST=$SOURCE_DIGEST"
  "PGWORKBENCH_PACK_ID="
  "PGWORKBENCH_PACK_VERSION="
  "PGWORKBENCH_PACK_DIGEST="
)

if env "${DERIVED_ENV[@]}" "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$GENERATED" >"$TEST_DIR/accepted.out" 2>&1; then
  echo "FAIL: derived fixture accepted an invalid run id" >&2
  exit 1
fi
if ! grep -q 'Invalid EXPERIMENT_RUN_ID: invalid/run-id' "$TEST_DIR/accepted.out"; then
  echo "FAIL: derived invalid-run-id rejection changed unexpectedly" >&2
  cat "$TEST_DIR/accepted.out" >&2
  exit 1
fi
if [[ -e "$PACK/runs/invalid/run-id" || -L "$PACK/runs/invalid/run-id" ]]; then
  echo "FAIL: rejected derived fixture created a run directory" >&2
  exit 1
fi

# A prepared utility run keeps the generated logical path for source-capability
# checks and portable identity, but sources the exact bytes snapshotted by Go.
GENERATED_ORIGINAL="$TEST_DIR/generated-original.env"
EXECUTION_SNAPSHOT="$TEST_DIR/generated-execution-snapshot.env"
cp "$GENERATED" "$GENERATED_ORIGINAL"
cp "$GENERATED" "$EXECUTION_SNAPSHOT"
printf '%s\n' 'EXPERIMENT_STATE_WRITER=snapshot-selected-A' >> "$EXECUTION_SNAPSHOT"
printf '%s\n' 'EXPERIMENT_STATE_WRITER=logical-replacement-B' >> "$GENERATED"
EXECUTION_DIGEST="$(sha256_digest_file "$EXECUTION_SNAPSHOT")"
snapshot_status=0
env "${DERIVED_ENV[@]}" \
  PGWORKBENCH_EXECUTION_SPEC_FILE="$EXECUTION_SNAPSHOT" \
  EXPERIMENT_SPEC_SHA256="$EXECUTION_DIGEST" \
  "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$GENERATED" \
  >"$TEST_DIR/utility-snapshot.out" 2>&1 || snapshot_status="$?"
if [[ "$snapshot_status" != "2" ]] ||
   ! grep -q 'Unsupported EXPERIMENT_STATE_WRITER: snapshot-selected-A' "$TEST_DIR/utility-snapshot.out"; then
  echo "FAIL: utility-derived route did not source the runner-selected bytes" >&2
  cat "$TEST_DIR/utility-snapshot.out" >&2
  exit 1
fi
cp "$GENERATED_ORIGINAL" "$GENERATED"

# The capability is fail-closed when the reviewed source bytes drift.
printf '# tampered\n' >> "$SOURCE"
if env "${DERIVED_ENV[@]}" "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$GENERATED" >"$TEST_DIR/tamper.out" 2>&1; then
  echo "FAIL: stale source digest was accepted" >&2
  exit 1
fi
grep -q 'Utility-derived source spec digest mismatch' "$TEST_DIR/tamper.out"
printf 'UTILITY_TEST_NAME="provenance smoke"\n' > "$SOURCE"

# Neither source nor generated paths may pass through symlinks.
OUTSIDE_SOURCE="$TEST_DIR/outside-source.env"
cp "$SOURCE" "$OUTSIDE_SOURCE"
rm "$SOURCE"
ln -s "$OUTSIDE_SOURCE" "$SOURCE"
if env "${DERIVED_ENV[@]}" "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$GENERATED" >"$TEST_DIR/source-symlink.out" 2>&1; then
  echo "FAIL: symlinked utility source was accepted" >&2
  exit 1
fi
grep -q 'Utility-derived source path must not contain symlinks' "$TEST_DIR/source-symlink.out"
rm "$SOURCE"
cp "$OUTSIDE_SOURCE" "$SOURCE"

OUTSIDE_GENERATED="$TEST_DIR/outside-generated.env"
cp "$GENERATED" "$OUTSIDE_GENERATED"
rm "$GENERATED"
ln -s "$OUTSIDE_GENERATED" "$GENERATED"
if env "${DERIVED_ENV[@]}" "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$GENERATED" >"$TEST_DIR/generated-symlink.out" 2>&1; then
  echo "FAIL: symlinked generated utility spec was accepted" >&2
  exit 1
fi
grep -q 'Generated utility experiment spec must be a regular non-symlink file' "$TEST_DIR/generated-symlink.out"
rm "$GENERATED"
cp "$OUTSIDE_GENERATED" "$GENERATED"

# Incomplete source tuples and pack-identity overclaims are rejected before
# the generated adapter is sourced or any run directory is prepared.
if env "${DERIVED_ENV[@]}" \
  PGWORKBENCH_SOURCE_SPEC_DIGEST= \
  "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$GENERATED" >"$TEST_DIR/incomplete.out" 2>&1; then
  echo "FAIL: incomplete utility source tuple was accepted" >&2
  exit 1
fi
grep -q 'Utility-derived source spec digest must be canonical sha256' "$TEST_DIR/incomplete.out"

if env "${DERIVED_ENV[@]}" \
  PGWORKBENCH_PACK_ID=forged-pack \
  "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" "$GENERATED" >"$TEST_DIR/pack-claim.out" 2>&1; then
  echo "FAIL: derived utility spec claimed scenario-pack identity" >&2
  exit 1
fi
grep -q 'Utility-derived experiment specs must not claim scenario-pack identity' "$TEST_DIR/pack-claim.out"

# Source provenance is not valid on the ordinary experiments boundary.
cat > "$PACK/experiments/smoke.env" <<'ENV'
EXPERIMENT_NAME="ordinary smoke"
EXPERIMENT_RUN_ID="invalid/run-id"
EXPERIMENT_METRICS=0
EXPERIMENT_SNAPSHOT=0
ENV
if env \
  PGWORKBENCH_BIN="$BIN" \
  PGWORKBENCH_SOURCE_SPEC_KIND=utility-test \
  PGWORKBENCH_SOURCE_SPEC_ID=example/smoke \
  PGWORKBENCH_SOURCE_SPEC_REF=utility-tests/example/smoke.env \
  PGWORKBENCH_SOURCE_SPEC_DIGEST="$SOURCE_DIGEST" \
  "$PACK/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" smoke >"$TEST_DIR/ordinary-spoof.out" 2>&1; then
  echo "FAIL: ordinary experiment accepted utility source provenance" >&2
  exit 1
fi
grep -q 'Source-spec provenance is only valid for an authorized utility-derived or benchmark experiment' "$TEST_DIR/ordinary-spoof.out"

# Exercise the manifest writer/verifier boundary with both generated and source
# identities present. This fixture is portable and needs no live PostgreSQL.
FIXTURE="$TEST_DIR/fixture-run"
mkdir -p "$FIXTURE"
GENERATED_DIGEST="$(sha256_digest_file "$GENERATED")"
RUN_ID=fixture-run \
STARTED_AT=2026-01-01T00:00:00Z \
EXPERIMENT_SPEC_FILE="$GENERATED" \
EXPERIMENT_SPEC_ID=utility/example/smoke \
EXPERIMENT_SPEC_REF=.tmp/utility-tests/derived.env \
EXPERIMENT_SPEC_SHA256="$GENERATED_DIGEST" \
EXPERIMENT_NAME="derived provenance smoke" \
EXPERIMENT_METRICS=0 \
PGWORKBENCH_RUNTIME=native \
PGWORKBENCH_PACK_ID='' \
PGWORKBENCH_PACK_VERSION='' \
PGWORKBENCH_PACK_DIGEST='' \
PGWORKBENCH_SOURCE_SPEC_KIND=utility-test \
PGWORKBENCH_SOURCE_SPEC_ID=example/smoke \
PGWORKBENCH_SOURCE_SPEC_REF=utility-tests/example/smoke.env \
PGWORKBENCH_SOURCE_SPEC_DIGEST="$SOURCE_DIGEST" \
PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS=observed \
PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET=primary \
PGWORKBENCH_RUNTIME_OS=linux \
PGWORKBENCH_RUNTIME_ARCH=amd64 \
PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM=160004 \
PGWORKBENCH_POSTGRES_SERVER_MAJOR=16 \
PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT=2026-01-01T00:00:01Z \
RUN_DIR="$FIXTURE" \
  "$BIN" run write-manifest --run-dir "$FIXTURE"

RUN_ID=fixture-run \
STARTED_AT=2026-01-01T00:00:00Z \
EXPERIMENT_SPEC_FILE="$GENERATED" \
EXPERIMENT_SPEC_ID=utility/example/smoke \
EXPERIMENT_SPEC_SHA256="$GENERATED_DIGEST" \
WORKLOAD_EXIT=0 \
ASSERT_EXIT=0 \
SCAN_EXIT=0 \
RUN_DIR="$FIXTURE" \
  "$BIN" run write-verdict --run-dir "$FIXTURE" --status passed --message passed --finished-at 2026-01-01T00:00:02Z

grep -q '^experiment_spec_id="utility/example/smoke"$' "$FIXTURE/manifest.env"
grep -q '^experiment_spec_ref=".tmp/utility-tests/derived.env"$' "$FIXTURE/manifest.env"
grep -q '^source_spec_kind="utility-test"$' "$FIXTURE/manifest.env"
grep -q '^source_spec_id="example/smoke"$' "$FIXTURE/manifest.env"
grep -q '^source_spec_ref="utility-tests/example/smoke.env"$' "$FIXTURE/manifest.env"
grep -q "^source_spec_digest=\"$SOURCE_DIGEST\"$" "$FIXTURE/manifest.env"
"$BIN" run verify "$FIXTURE" >/dev/null

echo "PASS: utility-derived provenance and manifest contract"
