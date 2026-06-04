#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO="${GO:-go}"
GO_CACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}"
GO_MOD_CACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}"

DATASET_LIST="$("$REPO_DIR/scripts/load_dataset.sh" list)"
grep -q '^synthetic/items$' <<< "$DATASET_LIST"
grep -q '^pgbench/tiny$' <<< "$DATASET_LIST"

GO_DATASET_LIST="$(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench dataset list
)"
grep -q '^synthetic/items$' <<< "$GO_DATASET_LIST"
grep -q '^pgbench/tiny$' <<< "$GO_DATASET_LIST"

"$REPO_DIR/scripts/load_dataset.sh" show synthetic/items | grep -q 'DATASET_KIND="sql"'

GO_DATASET_SPEC="$(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench dataset show synthetic/items
)"
grep -q 'DATASET_KIND="sql"' <<< "$GO_DATASET_SPEC"

(
  cd "$REPO_DIR"
  GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" "$GO" run ./cmd/pgworkbench dataset validate >/dev/null
)

DATASET_SIZE=small DATASET_SEED=7 "$REPO_DIR/scripts/load_dataset.sh" load synthetic/items >/dev/null

COUNT="$("$REPO_DIR/scripts/psql.sh" -A -t -c 'SELECT count(*) FROM dataset_synthetic.items;')"
if [[ "$COUNT" != "10000" ]]; then
  echo "FAIL: expected 10000 synthetic dataset rows, got $COUNT" >&2
  exit 1
fi

echo "PASS: datasets"
