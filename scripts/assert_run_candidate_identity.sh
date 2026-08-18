#!/usr/bin/env bash
set -euo pipefail

MANIFEST="${1:?usage: scripts/assert_run_candidate_identity.sh <manifest.env> <version> <commit>}"
VERSION="${2:?usage: scripts/assert_run_candidate_identity.sh <manifest.env> <version> <commit>}"
COMMIT="${3:?usage: scripts/assert_run_candidate_identity.sh <manifest.env> <version> <commit>}"

if [[ ! -f "$MANIFEST" || -L "$MANIFEST" ]]; then
  echo "run manifest must be a regular non-symlink file: $MANIFEST" >&2
  exit 1
fi
if [[ ! "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ || "$VERSION" == *dev* ]]; then
  echo "expected candidate version must be a non-development strict SemVer: $VERSION" >&2
  exit 2
fi
if [[ ! "$COMMIT" =~ ^[0-9a-f]{40,64}$ ]]; then
  echo "expected candidate commit must be a full lowercase Git object id: $COMMIT" >&2
  exit 2
fi

VERSION_LINES="$(grep -Ec '^engine_version=' "$MANIFEST" || true)"
COMMIT_LINES="$(grep -Ec '^engine_commit=' "$MANIFEST" || true)"
if [[ "$VERSION_LINES" != 1 || "$COMMIT_LINES" != 1 ]]; then
  echo "run manifest must contain exactly one engine_version and engine_commit: $MANIFEST" >&2
  exit 1
fi
if ! grep -Fqx "engine_version=\"$VERSION\"" "$MANIFEST"; then
  echo "run manifest engine_version does not match candidate $VERSION: $MANIFEST" >&2
  exit 1
fi
if ! grep -Fqx "engine_commit=\"$COMMIT\"" "$MANIFEST"; then
  echo "run manifest engine_commit does not match candidate $COMMIT: $MANIFEST" >&2
  exit 1
fi

printf 'PASS: run candidate identity %s %s\n' "$VERSION" "$COMMIT"
