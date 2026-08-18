#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:?usage: scripts/build_candidate_binary.sh <version> <commit> <output>}"
COMMIT="${2:?usage: scripts/build_candidate_binary.sh <version> <commit> <output>}"
OUTPUT="${3:?usage: scripts/build_candidate_binary.sh <version> <commit> <output>}"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${PGWORKBENCH_GO:-go}"

if [[ ! "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
  echo "candidate VERSION must be a strict SemVer: $VERSION" >&2
  exit 2
fi
if [[ "$VERSION" == *dev* ]]; then
  echo "candidate VERSION must not be a development version: $VERSION" >&2
  exit 2
fi
if [[ ! "$COMMIT" =~ ^[0-9a-f]{40,64}$ ]]; then
  echo "candidate commit must be a full lowercase Git object id: $COMMIT" >&2
  exit 2
fi

HEAD_COMMIT="$(git -C "$REPO_DIR" rev-parse HEAD)"
if [[ "$COMMIT" != "$HEAD_COMMIT" ]]; then
  echo "candidate build commit $COMMIT does not match HEAD $HEAD_COMMIT" >&2
  exit 1
fi
if [[ -n "${GITHUB_SHA:-}" && "$COMMIT" != "$GITHUB_SHA" ]]; then
  echo "candidate build commit $COMMIT does not match GITHUB_SHA $GITHUB_SHA" >&2
  exit 1
fi

BUILD_DATE="$(git -C "$REPO_DIR" show -s --format=%cI HEAD)"
mkdir -p "$(dirname "$OUTPUT")"
(
  cd "$REPO_DIR"
  "$GO_BIN" build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.builtAt=$BUILD_DATE" \
    -o "$OUTPUT" ./cmd/pgworkbench
)

IDENTITY="$($OUTPUT version)"
EXPECTED="pgworkbench version=$VERSION commit=$COMMIT built_at=$BUILD_DATE"
if [[ "$IDENTITY" != "$EXPECTED" ]]; then
  echo "candidate binary identity mismatch" >&2
  echo "expected: $EXPECTED" >&2
  echo "actual:   $IDENTITY" >&2
  exit 1
fi

printf 'PASS: candidate binary identity version=%s commit=%s\n' "$VERSION" "$COMMIT"
