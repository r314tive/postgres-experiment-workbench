#!/usr/bin/env bash
set -euo pipefail
trap 'status=$?; echo "FAIL: candidate identity guard failed at line $LINENO (status $status)" >&2; exit "$status"' ERR

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-candidate-identity.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

MANIFEST="$TMP_DIR/manifest.env"
COMMIT=0123456789abcdef0123456789abcdef01234567
VERSION=0.2.0

cat > "$MANIFEST" <<EOF
engine_version="$VERSION"
engine_commit="$COMMIT"
EOF
"$REPO_DIR/scripts/assert_run_candidate_identity.sh" "$MANIFEST" "$VERSION" "$COMMIT" >/dev/null

sed -i.bak 's/engine_version="0.2.0"/engine_version="dev"/' "$MANIFEST"
rm -f "$MANIFEST.bak"
if "$REPO_DIR/scripts/assert_run_candidate_identity.sh" "$MANIFEST" "$VERSION" "$COMMIT" >"$TMP_DIR/dev.out" 2>&1; then
  echo 'FAIL: development manifest identity was accepted' >&2
  exit 1
fi
grep -q 'engine_version does not match candidate' "$TMP_DIR/dev.out"

cat > "$MANIFEST" <<EOF
engine_version="$VERSION"
engine_commit="unknown"
EOF
if "$REPO_DIR/scripts/assert_run_candidate_identity.sh" "$MANIFEST" "$VERSION" "$COMMIT" >"$TMP_DIR/unknown.out" 2>&1; then
  echo 'FAIL: unknown manifest commit was accepted' >&2
  exit 1
fi
grep -q 'engine_commit does not match candidate' "$TMP_DIR/unknown.out"

PREFLIGHT="$TMP_DIR/repo"
mkdir -p "$PREFLIGHT/scripts"
cp "$REPO_DIR/scripts/candidate_preflight.sh" "$PREFLIGHT/scripts/"
cp "$REPO_DIR/scripts/build_candidate_binary.sh" "$PREFLIGHT/scripts/"
cat > "$PREFLIGHT/fake-go" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" = env && "${2:-}" = GOVERSION ]]; then
  printf '%s\n' "${FAKE_GO_VERSION:-go1.26.5}"
  exit 0
fi
cat <<'JSON'
{
  "id": "test-pack",
  "version": "0.2.0",
  "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "files": [
    {
      "path": "scripts/candidate_preflight.sh"
    }
JSON
if [[ -n "${FAKE_PACK_EXTRA_PATH:-}" ]]; then
  printf ',\n    {\n      "path": "%s"\n    }\n' "$FAKE_PACK_EXTRA_PATH"
fi
cat <<'JSON'
  ]
}
JSON
EOF
chmod +x "$PREFLIGHT/fake-go"
printf '*.log\n' > "$PREFLIGHT/.gitignore"
printf '1.26.5\n' > "$PREFLIGHT/.go-version"
printf '{}\n' > "$PREFLIGHT/pgworkbench-pack.json"
(
  cd "$PREFLIGHT"
  git init -q
  git config user.name test
  git config user.email test@example.invalid
  git add .
  git commit -qm initial
)
HEAD_COMMIT="$(git -C "$PREFLIGHT" rev-parse HEAD)"
BUILD_COMMIT="$HEAD_COMMIT" GITHUB_SHA="$HEAD_COMMIT" PGWORKBENCH_GO="$PREFLIGHT/fake-go" \
  "$PREFLIGHT/scripts/candidate_preflight.sh" "$VERSION" >/dev/null

printf 'ignored but pack-visible\n' > "$PREFLIGHT/ignored-pack.log"
if BUILD_COMMIT="$HEAD_COMMIT" GITHUB_SHA= PGWORKBENCH_GO="$PREFLIGHT/fake-go" \
  FAKE_PACK_EXTRA_PATH=ignored-pack.log \
  "$PREFLIGHT/scripts/candidate_preflight.sh" "$VERSION" >"$TMP_DIR/ignored-pack.out" 2>&1; then
  echo 'FAIL: candidate preflight accepted an ignored file in the scenario pack' >&2
  exit 1
fi
if ! grep -q 'scenario pack contains files that are not bound to the candidate commit' \
  "$TMP_DIR/ignored-pack.out"; then
  echo 'FAIL: ignored scenario-pack file was rejected for an unexpected reason:' >&2
  cat "$TMP_DIR/ignored-pack.out" >&2
  exit 1
fi

if BUILD_COMMIT="$HEAD_COMMIT" GITHUB_SHA= PGWORKBENCH_GO="$PREFLIGHT/fake-go" \
  FAKE_GO_VERSION=go1.26.4 \
  "$PREFLIGHT/scripts/candidate_preflight.sh" "$VERSION" >"$TMP_DIR/go-toolchain.out" 2>&1; then
  echo 'FAIL: candidate preflight accepted a different Go patch toolchain' >&2
  exit 1
fi
grep -q 'candidate requires Go 1.26.5, got go1.26.4' "$TMP_DIR/go-toolchain.out"

git -C "$PREFLIGHT" rm -q --cached pgworkbench-pack.json
printf 'pgworkbench-pack.json\n' >> "$PREFLIGHT/.gitignore"
git -C "$PREFLIGHT" add .gitignore
git -C "$PREFLIGHT" commit -qm 'ignore pack manifest'
HEAD_COMMIT="$(git -C "$PREFLIGHT" rev-parse HEAD)"
if BUILD_COMMIT="$HEAD_COMMIT" GITHUB_SHA= PGWORKBENCH_GO="$PREFLIGHT/fake-go" \
  "$PREFLIGHT/scripts/candidate_preflight.sh" "$VERSION" >"$TMP_DIR/untracked-manifest.out" 2>&1; then
  echo 'FAIL: candidate preflight accepted an untracked scenario-pack manifest' >&2
  exit 1
fi
grep -q 'candidate release identity files must be tracked' "$TMP_DIR/untracked-manifest.out"
sed -i.bak '/^pgworkbench-pack\.json$/d' "$PREFLIGHT/.gitignore"
rm -f "$PREFLIGHT/.gitignore.bak"
git -C "$PREFLIGHT" add -f .gitignore pgworkbench-pack.json
git -C "$PREFLIGHT" commit -qm 'track pack manifest'
HEAD_COMMIT="$(git -C "$PREFLIGHT" rev-parse HEAD)"

WRONG_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
if BUILD_COMMIT="$WRONG_COMMIT" GITHUB_SHA= PGWORKBENCH_GO="$PREFLIGHT/fake-go" \
  "$PREFLIGHT/scripts/candidate_preflight.sh" "$VERSION" >"$TMP_DIR/build-commit.out" 2>&1; then
  echo 'FAIL: BUILD_COMMIT different from HEAD was accepted' >&2
  exit 1
fi
grep -q 'BUILD_COMMIT .* does not match candidate HEAD' "$TMP_DIR/build-commit.out"

if BUILD_COMMIT="$HEAD_COMMIT" GITHUB_SHA="$WRONG_COMMIT" PGWORKBENCH_GO="$PREFLIGHT/fake-go" \
  "$PREFLIGHT/scripts/candidate_preflight.sh" "$VERSION" >"$TMP_DIR/github-sha.out" 2>&1; then
  echo 'FAIL: GITHUB_SHA different from HEAD was accepted' >&2
  exit 1
fi
grep -q 'does not match GITHUB_SHA' "$TMP_DIR/github-sha.out"

if GITHUB_SHA="$WRONG_COMMIT" PGWORKBENCH_GO="$PREFLIGHT/fake-go" \
  "$PREFLIGHT/scripts/build_candidate_binary.sh" "$VERSION" "$HEAD_COMMIT" "$TMP_DIR/candidate" \
  >"$TMP_DIR/build-github-sha.out" 2>&1; then
  echo 'FAIL: candidate builder accepted a commit different from GITHUB_SHA' >&2
  exit 1
fi
grep -q 'does not match GITHUB_SHA' "$TMP_DIR/build-github-sha.out"

COMPATIBILITY_WORKFLOW="$REPO_DIR/.github/workflows/compatibility.yml"
AGGREGATE_WORKFLOW="$REPO_DIR/.github/workflows/aggregate-gate.yml"
require_count() {
  local needle="$1" file="$2" expected="$3" actual
  actual="$(grep -Fc -- "$needle" "$file")"
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: expected $expected occurrences of $needle in $file, got $actual" >&2
    exit 1
  fi
}
require_text() {
  local needle="$1" file="$2"
  if ! grep -Fq -- "$needle" "$file"; then
    echo "FAIL: expected $needle in $file" >&2
    exit 1
  fi
}
require_count './scripts/build_candidate_binary.sh' "$COMPATIBILITY_WORKFLOW" 5
require_count './scripts/assert_run_candidate_identity.sh' "$COMPATIBILITY_WORKFLOW" 6
if grep -Fq 'go build -trimpath -o .tmp/qualification/pgworkbench' "$COMPATIBILITY_WORKFLOW"; then
  echo 'FAIL: compatibility source candidate bypasses the identity-bound builder' >&2
  exit 1
fi
require_text 'BUILD_COMMIT="$GITHUB_SHA"' "$AGGREGATE_WORKFLOW"
require_text 'PGWORKBENCH_CLI="$PGWORKBENCH_BIN"' "$AGGREGATE_WORKFLOW"
require_text './scripts/assert_run_candidate_identity.sh' "$AGGREGATE_WORKFLOW"

printf 'PASS: immutable candidate and run-manifest identity guards\n'
