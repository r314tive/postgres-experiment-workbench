#!/usr/bin/env bash
set -euo pipefail

ARCHIVE="${1:?usage: tests/release_reproducibility.sh <archive> <version> <commit> <epoch> <build-date>}"
VERSION="${2:?usage: tests/release_reproducibility.sh <archive> <version> <commit> <epoch> <build-date>}"
COMMIT="${3:?usage: tests/release_reproducibility.sh <archive> <version> <commit> <epoch> <build-date>}"
EPOCH="${4:?usage: tests/release_reproducibility.sh <archive> <version> <commit> <epoch> <build-date>}"
BUILD_DATE="${5:?usage: tests/release_reproducibility.sh <archive> <version> <commit> <epoch> <build-date>}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${PGWORKBENCH_GO:-go}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-release-repro.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

archive_name="$(basename "$ARCHIVE")"
root_name="${archive_name%.tar.gz}"
platform="${root_name#pgworkbench-"${VERSION}"-}"
host_os="${platform%-*}"
host_arch="${platform##*-}"
source_dir="$TMP_DIR/$root_name"
rebuilt="$TMP_DIR/$archive_name"

cd "$ROOT_DIR"
"$GO_BIN" run ./cmd/pgworkbench pack export --engine-version "$VERSION" "$source_dir" >/dev/null
CGO_ENABLED=0 GOOS="$host_os" GOARCH="$host_arch" \
  "$GO_BIN" build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.builtAt=$BUILD_DATE" \
    -o "$source_dir/pgworkbench" ./cmd/pgworkbench
"$GO_BIN" run ./cmd/pgworkbench release archive create \
  --source "$source_dir" --output "$rebuilt" --root-name "$root_name" --epoch "$EPOCH" >/dev/null

if ! cmp -s "$ARCHIVE" "$rebuilt"; then
  echo "release archive is not byte-for-byte reproducible: $archive_name" >&2
  exit 1
fi

printf 'PASS: reproducible release archive %s\n' "$archive_name"
