#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-privacy-test.XXXXXX")"
trap 'rm -rf "$TEST_ROOT"' EXIT

mkdir -p "$TEST_ROOT/safe" "$TEST_ROOT/leak"
printf '%s\n' 'Documentation may discuss credentials, tokens, and secrets.' >"$TEST_ROOT/safe/README.md"
"$REPO_DIR/scripts/privacy_scan.sh" "$TEST_ROOT/safe" >/dev/null

FAKE_GITHUB_TOKEN="gh""p_$(printf 'a%.0s' {1..36})"
printf '%s\n' "$FAKE_GITHUB_TOKEN" >"$TEST_ROOT/leak/credential.txt"

if "$REPO_DIR/scripts/privacy_scan.sh" "$TEST_ROOT/leak" >"$TEST_ROOT/output.log" 2>&1; then
  echo "FAIL: privacy scan accepted a credential-shaped fixture" >&2
  exit 1
fi
grep -q 'credential.txt' "$TEST_ROOT/output.log"

echo "PASS: privacy scanner contract"
