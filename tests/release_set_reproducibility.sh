#!/usr/bin/env bash
set -euo pipefail

RELEASE_DIR="${1:?usage: tests/release_set_reproducibility.sh <release-dir> <version> <commit> <epoch> <build-date>}"
VERSION="${2:?usage: tests/release_set_reproducibility.sh <release-dir> <version> <commit> <epoch> <build-date>}"
COMMIT="${3:?usage: tests/release_set_reproducibility.sh <release-dir> <version> <commit> <epoch> <build-date>}"
EPOCH="${4:?usage: tests/release_set_reproducibility.sh <release-dir> <version> <commit> <epoch> <build-date>}"
BUILD_DATE="${5:?usage: tests/release_set_reproducibility.sh <release-dir> <version> <commit> <epoch> <build-date>}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
GO_BIN="${PGWORKBENCH_GO:-go}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-release-set-repro.XXXXXX")"
REBUILT_DIR="$TMP_DIR/release"
trap 'rm -rf -- "$TMP_DIR"' EXIT

RELEASE_DIR="$(cd "$RELEASE_DIR" && pwd -P)"

(
  cd "$ROOT_DIR"
  make release-snapshot \
    VERSION="$VERSION" \
    BUILD_COMMIT="$COMMIT" \
    SOURCE_DATE_EPOCH="$EPOCH" \
    BUILD_DATE="$BUILD_DATE" \
    GO="$GO_BIN" \
    RELEASE_DIR="$REBUILT_DIR" \
    RELEASE_CHECKSUM_FILE="$REBUILT_DIR/pgworkbench-$VERSION-SHA256SUMS.txt" \
    RELEASE_MANIFEST_FILE="$REBUILT_DIR/pgworkbench-$VERSION-release-manifest.json" \
    >/dev/null
)

expected_names=()
for platform in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
  expected_names+=(
    "pgworkbench-$VERSION-$platform.tar.gz"
    "pgworkbench-$VERSION-$platform.spdx.json"
  )
done
expected_names+=(
  "pgworkbench-$VERSION-SHA256SUMS.txt"
  "pgworkbench-$VERSION-release-manifest.json"
)

for name in "${expected_names[@]}"; do
  if [[ ! -f "$RELEASE_DIR/$name" || -L "$RELEASE_DIR/$name" ]]; then
    echo "missing regular release artifact for reproducibility check: $name" >&2
    exit 1
  fi
  if [[ ! -f "$REBUILT_DIR/$name" || -L "$REBUILT_DIR/$name" ]]; then
    echo "rebuilt release artifact is missing or unsafe: $name" >&2
    exit 1
  fi
  if ! cmp -s "$RELEASE_DIR/$name" "$REBUILT_DIR/$name"; then
    echo "release artifact is not byte-for-byte reproducible: $name" >&2
    exit 1
  fi
done

actual_count="$(find "$REBUILT_DIR" -maxdepth 1 -type f \
  \( -name "pgworkbench-$VERSION-*.tar.gz" \
  -o -name "pgworkbench-$VERSION-*.spdx.json" \
  -o -name "pgworkbench-$VERSION-SHA256SUMS.txt" \
  -o -name "pgworkbench-$VERSION-release-manifest.json" \) | wc -l | tr -d ' ')"
if [[ "$actual_count" != "${#expected_names[@]}" ]]; then
  echo "rebuilt release core artifact set is not exact: got $actual_count, want ${#expected_names[@]}" >&2
  exit 1
fi

printf 'PASS: reproducible release core set (%d artifacts)\n' "${#expected_names[@]}"
