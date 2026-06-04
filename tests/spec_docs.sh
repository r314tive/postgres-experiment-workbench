#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d "$REPO_DIR/.tmp/spec-docs.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

GO_CACHE="${GO_CACHE:-$REPO_DIR/.tmp/go-cache}"
GO_MOD_CACHE="${GO_MOD_CACHE:-$REPO_DIR/.tmp/go-mod-cache}"

GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" \
  go run ./cmd/pgworkbench spec reference all > "$TMP_DIR/spec-reference.md"

GOCACHE="$GO_CACHE" GOMODCACHE="$GO_MOD_CACHE" \
  go run ./cmd/pgworkbench spec schema all > "$TMP_DIR/env-specs.schema.json"

diff -u "$REPO_DIR/docs/spec-reference.md" "$TMP_DIR/spec-reference.md"
diff -u "$REPO_DIR/schemas/env-specs.schema.json" "$TMP_DIR/env-specs.schema.json"

echo "PASS: spec docs"
