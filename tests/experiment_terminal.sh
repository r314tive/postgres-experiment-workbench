#!/usr/bin/env bash
set -euo pipefail

export PGWORKBENCH_SUPERVISED=1
INTERNAL_RUN_ACTION=__pgworkbench_internal_run_v1

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-terminal.XXXXXX")"
RUN_ID="terminal-contract-$(date -u +%Y%m%d_%H%M%S)-$$"
RUN_DIR="$REPO_DIR/runs/$RUN_ID"
ENGINE_COMMIT="0123456789abcdef0123456789abcdef01234567"

cleanup() {
	rm -rf "$TMP_DIR" "$RUN_DIR" "$REPO_DIR/runs/$RUN_ID-source"
}
trap cleanup EXIT

GOCACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}" \
GOMODCACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}" \
  go build -ldflags "-X main.version=0.2.0 -X main.commit=$ENGINE_COMMIT" -o "$TMP_DIR/pgworkbench" ./cmd/pgworkbench

if PGWORKBENCH_BIN="$TMP_DIR/pgworkbench" \
  PGWORKBENCH_RUNTIME=native \
  PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS=observed \
  PGWORKBENCH_RUNTIME_OS=forged \
  PGWORKBENCH_RUNTIME_ARCH=forged \
  PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM=999999 \
  PGWORKBENCH_POSTGRES_SERVER_MAJOR=99 \
  PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT=2099-01-01T00:00:00Z \
  EXPERIMENT_RUN_ID="$RUN_ID" \
  EXPERIMENT_METRICS=0 \
  EXPERIMENT_SNAPSHOT=0 \
  "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" replica-readonly >/dev/null 2>&1; then
  echo "FAIL: unsupported native topology unexpectedly passed" >&2
  exit 1
fi

test -s "$RUN_DIR/manifest.env"
test -s "$RUN_DIR/verdict.env"
test -s "$RUN_DIR/verdict.json"
grep -q '^runtime_fingerprint_status="unavailable"$' "$RUN_DIR/manifest.env"
grep -q '^postgres_server_version_num=""$' "$RUN_DIR/manifest.env"
grep -q '^engine_version="0.2.0"$' "$RUN_DIR/manifest.env"
grep -q "^engine_commit=\"$ENGINE_COMMIT\"$" "$RUN_DIR/manifest.env"
grep -q '"status": "failed"' "$RUN_DIR/verdict.json"
grep -q 'aborted before terminal verdict' "$RUN_DIR/verdict.json"
"$TMP_DIR/pgworkbench" run verify "$RUN_DIR" >/dev/null

SOURCE_RUN_ID="$RUN_ID-source"
SOURCE_RUN_DIR="$REPO_DIR/runs/$SOURCE_RUN_ID"
if PGWORKBENCH_BIN='' \
  PGWORKBENCH_RUNTIME=native \
  EXPERIMENT_RUN_ID="$SOURCE_RUN_ID" \
  EXPERIMENT_METRICS=0 \
  EXPERIMENT_SNAPSHOT=0 \
  "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" replica-readonly >/dev/null 2>&1; then
  echo "FAIL: unsupported native topology unexpectedly passed for source runner" >&2
  exit 1
fi
grep -q '^engine_version="unverified"$' "$SOURCE_RUN_DIR/manifest.env"
grep -q '^engine_commit="unverified"$' "$SOURCE_RUN_DIR/manifest.env"
"$TMP_DIR/pgworkbench" run verify "$SOURCE_RUN_DIR" >/dev/null

before_digest="$(shasum -a 256 "$RUN_DIR/verdict.json" | awk '{print $1}')"
if PGWORKBENCH_BIN="$TMP_DIR/pgworkbench" \
  PGWORKBENCH_RUNTIME=native \
  EXPERIMENT_RUN_ID="$RUN_ID" \
  "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" replica-readonly >/dev/null 2>&1; then
  echo "FAIL: immutable run directory was overwritten" >&2
  exit 1
fi
after_digest="$(shasum -a 256 "$RUN_DIR/verdict.json" | awk '{print $1}')"
test "$before_digest" = "$after_digest"

if PGWORKBENCH_BIN="$TMP_DIR/pgworkbench" \
  EXPERIMENT_RUN_ID='../escape' \
  "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" smoke >/dev/null 2>&1; then
  echo "FAIL: unsafe run id was accepted" >&2
  exit 1
fi

STATE_WRITER_RUN_ID="$RUN_ID-state-writer"
if PGWORKBENCH_BIN="$TMP_DIR/pgworkbench" \
  EXPERIMENT_STATE_WRITER=shell \
  EXPERIMENT_RUN_ID="$STATE_WRITER_RUN_ID" \
  "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" smoke >"$TMP_DIR/state-writer.log" 2>&1; then
  echo "FAIL: legacy shell state writer was accepted" >&2
  exit 1
fi
grep -q 'cannot write the v1 evidence contract' "$TMP_DIR/state-writer.log"
if [[ -e "$REPO_DIR/runs/$STATE_WRITER_RUN_ID" || -L "$REPO_DIR/runs/$STATE_WRITER_RUN_ID" ]]; then
  echo "FAIL: rejected state writer created a run directory" >&2
  exit 1
fi

cat > "$TMP_DIR/failing-writer" <<'SCRIPT'
#!/usr/bin/env bash
exit 42
SCRIPT
chmod +x "$TMP_DIR/failing-writer"
FAILED_WRITER_RUN_ID="$RUN_ID-failed-writer"
if PGWORKBENCH_BIN="$TMP_DIR/failing-writer" \
  PGWORKBENCH_RUNTIME=native \
  EXPERIMENT_RUN_ID="$FAILED_WRITER_RUN_ID" \
  "$REPO_DIR/scripts/run_experiment.sh" "$INTERNAL_RUN_ACTION" smoke >/dev/null 2>&1; then
  echo "FAIL: failing manifest writer unexpectedly passed" >&2
  exit 1
fi
if [[ -e "$REPO_DIR/runs/$FAILED_WRITER_RUN_ID" || -L "$REPO_DIR/runs/$FAILED_WRITER_RUN_ID" ]]; then
  echo "FAIL: failed manifest publication left a non-terminal run directory" >&2
  exit 1
fi

echo "PASS: terminal verdict and immutable run contract"
