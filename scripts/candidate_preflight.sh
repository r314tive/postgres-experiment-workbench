#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:?usage: scripts/candidate_preflight.sh <version>}"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${PGWORKBENCH_GO:-go}"
GO_VERSION_FILE="$REPO_DIR/.go-version"

if [[ ! "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
  echo "release VERSION must be a strict SemVer: $VERSION" >&2
  exit 2
fi
if [[ "$VERSION" == *dev* ]]; then
  echo "release VERSION must not be a development version: $VERSION" >&2
  exit 2
fi

if ! git -C "$REPO_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "candidate preflight requires a Git checkout" >&2
  exit 2
fi

DIRTY_STATUS="$(git -C "$REPO_DIR" status --porcelain=v1 --untracked-files=all)"
if [[ -n "$DIRTY_STATUS" ]]; then
  echo "candidate preflight requires a clean Git worktree" >&2
  printf '%s\n' "$DIRTY_STATUS" >&2
  exit 1
fi

COMMIT="$(git -C "$REPO_DIR" rev-parse HEAD)"
if [[ ! "$COMMIT" =~ ^[0-9a-f]{40,64}$ ]]; then
  echo "candidate commit is not a full Git object id: $COMMIT" >&2
  exit 1
fi
if [[ -z "${BUILD_COMMIT:-}" ]]; then
  echo "candidate preflight requires BUILD_COMMIT to bind release metadata to HEAD" >&2
  exit 2
fi
if [[ ! "$BUILD_COMMIT" =~ ^[0-9a-f]{40,64}$ ]]; then
  echo "BUILD_COMMIT is not a full lowercase Git object id: $BUILD_COMMIT" >&2
  exit 2
fi
if [[ "$BUILD_COMMIT" != "$COMMIT" ]]; then
  echo "BUILD_COMMIT $BUILD_COMMIT does not match candidate HEAD $COMMIT" >&2
  exit 1
fi
if [[ -n "${GITHUB_SHA:-}" && "$COMMIT" != "$GITHUB_SHA" ]]; then
  echo "candidate commit $COMMIT does not match GITHUB_SHA $GITHUB_SHA" >&2
  exit 1
fi
if [[ "${GITHUB_REF_TYPE:-}" = "tag" && "${GITHUB_REF_NAME:-}" != "v$VERSION" ]]; then
  echo "candidate version $VERSION does not match tag ${GITHUB_REF_NAME:-<unset>}" >&2
  exit 1
fi

if ! git -C "$REPO_DIR" ls-files --error-unmatch -- pgworkbench-pack.json .go-version >/dev/null 2>&1; then
  echo "candidate release identity files must be tracked: pgworkbench-pack.json and .go-version" >&2
  exit 1
fi
EXPECTED_GO_VERSION="$(tr -d '[:space:]' < "$GO_VERSION_FILE")"
if [[ ! "$EXPECTED_GO_VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "candidate .go-version must contain one exact stable Go patch version" >&2
  exit 1
fi
ACTUAL_GO_VERSION="$($GO_BIN env GOVERSION)"
if [[ "$ACTUAL_GO_VERSION" != "go$EXPECTED_GO_VERSION" ]]; then
  echo "candidate requires Go $EXPECTED_GO_VERSION, got $ACTUAL_GO_VERSION" >&2
  exit 1
fi

PACK_JSON="$(
  cd "$REPO_DIR"
  "$GO_BIN" run ./cmd/pgworkbench pack inspect --json --engine-version "$VERSION"
)"
PACK_VERSION="$(sed -n 's/^[[:space:]]*"version": "\([^"]*\)",*$/\1/p' <<<"$PACK_JSON" | head -1)"
PACK_ID="$(sed -n 's/^[[:space:]]*"id": "\([^"]*\)",*$/\1/p' <<<"$PACK_JSON" | head -1)"
PACK_DIGEST="$(sed -n 's/^[[:space:]]*"digest": "\([^"]*\)",*$/\1/p' <<<"$PACK_JSON" | head -1)"
PACK_UNTRACKED="$(
  comm -23 \
    <(sed -n 's/^[[:space:]]*"path": "\([^"]*\)",*$/\1/p' <<<"$PACK_JSON" | LC_ALL=C sort -u) \
    <(git -C "$REPO_DIR" ls-files | LC_ALL=C sort -u)
)"

if [[ "$PACK_VERSION" != "$VERSION" ]]; then
  echo "scenario pack version $PACK_VERSION does not match candidate VERSION $VERSION" >&2
  exit 1
fi
if [[ ! "$PACK_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "scenario pack did not produce a canonical digest" >&2
  exit 1
fi
if [[ -n "$PACK_UNTRACKED" ]]; then
  echo "scenario pack contains files that are not bound to the candidate commit:" >&2
  printf '%s\n' "$PACK_UNTRACKED" >&2
  exit 1
fi

printf 'PASS: candidate preflight\n'
printf 'candidate_version=%s\n' "$VERSION"
printf 'candidate_commit=%s\n' "$COMMIT"
printf 'candidate_pack_id=%s\n' "$PACK_ID"
printf 'candidate_pack_digest=%s\n' "$PACK_DIGEST"
