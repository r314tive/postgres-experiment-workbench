#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$REPO_DIR"

runtime="${PGWORKBENCH_RUNTIME:-docker}"
run_id="${1:-benchmark-${runtime}-smoke-$(date -u +%Y%m%d_%H%M%S)}"
peer_run_id="${run_id}-history-peer"
history_id="history-${run_id}"
go_command="${PGWORKBENCH_GO:-go}"
go_cache="${GOCACHE:-$REPO_DIR/.tmp/go-cache}"
go_mod_cache="${GOMODCACHE:-$REPO_DIR/.tmp/go-mod-cache}"
series_dir="$REPO_DIR/runs/benchmarks/$run_id"
trial_dir="$REPO_DIR/runs/$run_id-t001"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-benchmark-smoke.XXXXXX")"

cleanup() {
  rm -rf -- "$temporary_root"
}
trap cleanup EXIT

cli() {
  GOCACHE="$go_cache" GOMODCACHE="$go_mod_cache" "$go_command" run ./cmd/pgworkbench "$@"
}

cli benchmark run --runtime "$runtime" --run-id "$run_id" --subject "${runtime}-contract" pgbench/smoke
cli benchmark run-verify "$series_dir"

raw_count="$(find "$trial_dir/driver/pgbench-raw" -type f -size +0c | wc -l | tr -d ' ')"
if [[ "$raw_count" -lt 1 ]]; then
  echo "FAIL: benchmark smoke did not preserve a non-empty raw pgbench log" >&2
  exit 1
fi
if grep -Fq "$REPO_DIR" "$series_dir/plan.json"; then
  echo "FAIL: benchmark plan contains a producer-absolute path" >&2
  exit 1
fi

first_archive="$temporary_root/benchmark-first.tar.gz"
second_archive="$temporary_root/benchmark-second.tar.gz"
cli benchmark run-bundle "$series_dir" "$first_archive" >/dev/null
cli benchmark run-bundle "$series_dir" "$second_archive" >/dev/null
if ! cmp -s "$first_archive" "$second_archive"; then
  echo "FAIL: identical benchmark bundles are not byte-reproducible" >&2
  exit 1
fi

mkdir "$temporary_root/extracted"
tar -C "$temporary_root/extracted" -xzf "$first_archive"
bundle_root="$temporary_root/extracted/pgworkbench-benchmark-$run_id"
cli benchmark run-verify --bundle "$bundle_root/runs/benchmarks/$run_id"

# A second independently scheduled smoke series exercises the bounded history
# lifecycle. History deltas are descriptive only; this is an integrity and
# portability gate, not performance evidence.
cli benchmark run --runtime "$runtime" --run-id "$peer_run_id" \
  --subject "${runtime}-contract-peer" pgbench/smoke
cli benchmark run-verify "$REPO_DIR/runs/benchmarks/$peer_run_id"
cli benchmark history-create --history-id "$history_id" \
  "$series_dir" "$REPO_DIR/runs/benchmarks/$peer_run_id"
history_dir="$REPO_DIR/runs/benchmark-history/$history_id"
cli benchmark history-verify "$history_dir"

history_first="$temporary_root/history-first.tar.gz"
history_second="$temporary_root/history-second.tar.gz"
cli benchmark history-bundle "$history_dir" "$history_first" >/dev/null
cli benchmark history-bundle "$history_dir" "$history_second" >/dev/null
if ! cmp -s "$history_first" "$history_second"; then
  echo "FAIL: identical benchmark history bundles are not byte-reproducible" >&2
  exit 1
fi
mkdir "$temporary_root/extracted-history"
tar -C "$temporary_root/extracted-history" -xzf "$history_first"
history_bundle_root="$temporary_root/extracted-history/pgworkbench-benchmark-history-$history_id"
cli benchmark history-verify --bundle \
  "$history_bundle_root/runs/benchmark-history/$history_id"

if cli benchmark compare "$series_dir" "$series_dir" >"$temporary_root/compare.out" 2>&1; then
  echo "FAIL: smoke series was accepted as performance-comparison evidence" >&2
  exit 1
fi
if ! grep -Eq 'NOT-COMPARABLE|not-comparable' "$temporary_root/compare.out"; then
  echo "FAIL: smoke comparison did not explain its not-comparable verdict" >&2
  cat "$temporary_root/compare.out" >&2
  exit 1
fi

cleanup
trap - EXIT
echo "PASS: $runtime benchmark smoke, raw evidence, deterministic relocated series/history bundles, and comparison assurance boundary"
