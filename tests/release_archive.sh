#!/usr/bin/env bash
set -euo pipefail

ARCHIVE="${1:?usage: tests/release_archive.sh <archive.tar.gz>}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-release.XXXXXX")"
ROOT=""
NATIVE_BINDIR="${PGWORKBENCH_NATIVE_BINDIR:-}"
NATIVE_PORT=""

cleanup() {
  if [[ -n "$ROOT" && -x "$ROOT/scripts/runtime.sh" && -n "$NATIVE_BINDIR" ]]; then
    POSTGRES_PORT="$NATIVE_PORT" PGWORKBENCH_RUNTIME=native PGWORKBENCH_NATIVE_BINDIR="$NATIVE_BINDIR" \
      "$ROOT/scripts/runtime.sh" down single >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

tar -C "$TMP_DIR" -xzf "$ARCHIVE"
root_count=0
while IFS= read -r candidate; do
  ROOT="$candidate"
  root_count=$((root_count + 1))
done < <(find "$TMP_DIR" -mindepth 1 -maxdepth 1 -type d | sort)
if (( root_count != 1 )); then
  echo "release archive must contain exactly one root directory" >&2
  exit 1
fi

BIN="$ROOT/pgworkbench"
test -x "$BIN"
test -f "$ROOT/pgworkbench-pack.json"
test -f "$ROOT/go.mod"
test -f "$ROOT/go.sum"
test -f "$ROOT/evidence/templates/release-evidence-index.json"
test -f "$ROOT/evidence/templates/adoption-pilot-record.json"
test -f "$ROOT/evidence/templates/critical-finding-review.json"
test -f "$ROOT/third_party/go-modules.json"
test -f "$ROOT/third_party/licenses/github.com/dlclark/regexp2/v1.11.0/LICENSE"
test -f "$ROOT/third_party/licenses/github.com/dlclark/regexp2/v1.11.0/ATTRIB"
test -f "$ROOT/third_party/licenses/github.com/santhosh-tekuri/jsonschema/v6/v6.0.2/LICENSE"
test -f "$ROOT/third_party/licenses/golang.org/x/text/v0.14.0/LICENSE"
test -f "$ROOT/third_party/licenses/golang.org/x/text/v0.14.0/PATENTS"
test -f "$ROOT/cmd/pgworkbench/main.go"
test -f "$ROOT/internal/scenariopack/pack.go"
test -f "$ROOT/Makefile"
test -x "$ROOT/scripts/run_experiment.sh"

(
  cd "$TMP_DIR"
  "$BIN" version
  "$BIN" pack validate
  "$BIN" experiment plan smoke >/dev/null
)

if [[ -z "$NATIVE_BINDIR" ]] && command -v pg_config >/dev/null 2>&1; then
  NATIVE_BINDIR="$(pg_config --bindir)"
fi
if [[ -z "$NATIVE_BINDIR" || ! -x "$NATIVE_BINDIR/initdb" ]]; then
  echo "native PostgreSQL binaries not found; set PGWORKBENCH_NATIVE_BINDIR" >&2
  exit 1
fi

ports="$("$ROOT/scripts/assign_test_ports.sh")"
NATIVE_PORT="$(awk -F= '$1 == "POSTGRES_PORT" {print $2}' <<< "$ports")"
if [[ -z "$NATIVE_PORT" ]]; then
  echo "could not reserve a native PostgreSQL test port" >&2
  exit 1
fi

(
  cd "$TMP_DIR"
  POSTGRES_PORT="$NATIVE_PORT" PGWORKBENCH_NATIVE_BINDIR="$NATIVE_BINDIR" \
  EXPERIMENT_METRICS=0 \
  EXPERIMENT_SNAPSHOT=0 \
    "$BIN" experiment run --runtime native --run-id release-native-smoke smoke
  "$BIN" run verify "$ROOT/runs/release-native-smoke"
  "$BIN" run bundle \
    "$ROOT/runs/release-native-smoke" \
    "$ROOT/generated/release-native-smoke.tar.gz"
  mkdir -p "$TMP_DIR/relocated-bundle"
  tar -C "$TMP_DIR/relocated-bundle" -xzf "$ROOT/generated/release-native-smoke.tar.gz"
  "$BIN" run verify --bundle "$TMP_DIR/relocated-bundle/release-native-smoke"

  POSTGRES_PORT="$NATIVE_PORT" PGWORKBENCH_NATIVE_BINDIR="$NATIVE_BINDIR" \
    "$BIN" benchmark run --runtime native \
      --run-id release-native-benchmark-smoke \
      --subject release-archive-contract \
      pgbench/smoke
  "$BIN" benchmark run-verify \
    "$ROOT/runs/benchmarks/release-native-benchmark-smoke"
  "$BIN" benchmark run-bundle \
    "$ROOT/runs/benchmarks/release-native-benchmark-smoke" \
    "$ROOT/generated/release-native-benchmark-smoke.tar.gz"
  mkdir -p "$TMP_DIR/relocated-benchmark-bundle"
  tar -C "$TMP_DIR/relocated-benchmark-bundle" \
    -xzf "$ROOT/generated/release-native-benchmark-smoke.tar.gz"
  "$BIN" benchmark run-verify --bundle \
    "$TMP_DIR/relocated-benchmark-bundle/pgworkbench-benchmark-release-native-benchmark-smoke/runs/benchmarks/release-native-benchmark-smoke"
)

POSTGRES_PORT="$NATIVE_PORT" PGWORKBENCH_RUNTIME=native PGWORKBENCH_NATIVE_BINDIR="$NATIVE_BINDIR" \
  "$ROOT/scripts/runtime.sh" down single >/dev/null

echo "PASS: standalone release archive, native experiment, native benchmark, and relocated bundles"
