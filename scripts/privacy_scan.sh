#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v rg >/dev/null 2>&1; then
  echo "FAIL: privacy scan requires ripgrep (rg)" >&2
  exit 2
fi

if (( $# > 0 )); then
  SCAN_ROOTS=("$@")
else
  SCAN_ROOTS=("$REPO_DIR")
fi

TMP_FILE="$(mktemp "${TMPDIR:-/tmp}/postgres-experiment-workbench-privacy.XXXXXX")"
trap 'rm -f "$TMP_FILE"' EXIT

# Match credential shapes, not words such as "token" or "secret". Those words
# legitimately occur in security documentation and negative-path fixtures.
PATTERN='gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,}|sk-(proj-)?[A-Za-z0-9_-]{20,}|sk_live_[A-Za-z0-9]{20,}|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|AWS_SECRET_ACCESS_KEY[[:space:]]*[:=][[:space:]]*[A-Za-z0-9/+=]{30,}'

if [[ -n "${HOME:-}" ]]; then
  HOME_PATTERN="$(printf '%s' "$HOME" | sed 's/[][\.^$*+?{}|()]/\\&/g')"
  PATTERN="$PATTERN|$HOME_PATTERN"
fi

set +e
rg --hidden --no-ignore -n -i "$PATTERN" "${SCAN_ROOTS[@]}" \
  -g '!notes/**' \
  -g '!logs/**' \
  -g '!runs/**' \
  -g '!generated/**' \
  -g '!.tmp/**' \
  -g '!.git' \
  -g '!.git/**' \
  -g '!**/notes/**' \
  -g '!**/logs/**' \
  -g '!**/runs/**' \
  -g '!**/generated/**' \
  -g '!**/.tmp/**' \
  -g '!**/.git' \
  -g '!**/.git/**' >"$TMP_FILE" 2>&1
STATUS="$?"
set -e

case "$STATUS" in
  1)
    echo "PASS: privacy scan"
    ;;
  0)
    cat "$TMP_FILE"
    echo "FAIL: privacy scan found credential-shaped or private-path text" >&2
    exit 1
    ;;
  *)
    cat "$TMP_FILE"
    echo "FAIL: privacy scan command failed" >&2
    exit "$STATUS"
    ;;
esac
