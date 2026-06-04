#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if grep -RInE 'awk.*-v[[:space:]]+default=' "$REPO_DIR/scripts"; then
  echo "FAIL: GNU awk rejects reserved variable name: default" >&2
  exit 1
fi

echo "PASS: shell portability"
