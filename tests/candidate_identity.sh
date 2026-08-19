#!/usr/bin/env bash
set -euo pipefail
trap 'status=$?; echo "FAIL: candidate identity guard failed at line $LINENO (status $status)" >&2; exit "$status"' ERR

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-candidate-identity.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

MANIFEST="$TMP_DIR/manifest.env"
COMMIT=0123456789abcdef0123456789abcdef01234567
VERSION="$(awk -F '"' '$2 == "version" { print $4; exit }' "$REPO_DIR/pgworkbench-pack.json")"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]

cat > "$MANIFEST" <<EOF
engine_version="$VERSION"
engine_commit="$COMMIT"
EOF
"$REPO_DIR/scripts/assert_run_candidate_identity.sh" "$MANIFEST" "$VERSION" "$COMMIT" >/dev/null

sed -i.bak 's/^engine_version=.*/engine_version="dev"/' "$MANIFEST"
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
  "version": "__VERSION__",
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
sed -i.bak "s/__VERSION__/$VERSION/" "$PREFLIGHT/fake-go"
rm -f "$PREFLIGHT/fake-go.bak"
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
RELEASE_WORKFLOW="$REPO_DIR/.github/workflows/release-snapshot.yml"
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
if awk '
  $0 == "      version:" { active = 1; next }
  active && $0 !~ /^        / { exit }
  active && $0 ~ /^[[:space:]]+default:/ { found = 1 }
  END { exit found ? 0 : 1 }
' "$RELEASE_WORKFLOW"; then
  echo 'FAIL: release workflow_dispatch version must not carry a stale default' >&2
  exit 1
fi

RELEASE_RUNBOOK="$REPO_DIR/docs/release.md"
require_text 'release_version="${PGWORKBENCH_RELEASE_VERSION:?' "$RELEASE_RUNBOOK"
require_text '${BASH_VERSINFO[0]:-0}' "$RELEASE_RUNBOOK"
require_text 'git hash-object --stdin <<<"$candidate_parent"' "$RELEASE_RUNBOOK"
require_text '[[ "$candidate_parent_hash" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]]' "$RELEASE_RUNBOOK"
require_text 'candidate_nonce="${candidate_parent_hash:0:16}"' "$RELEASE_RUNBOOK"
require_text 'candidate_checkout="$candidate_parent/pgworkbench-${candidate_sha:0:12}-${candidate_nonce}"' "$RELEASE_RUNBOOK"
require_text 'release_project="${candidate_checkout##*/}"' "$RELEASE_RUNBOOK"
require_text 'test "$("${release_env[@]}" jq -er '\''.version'\'' pgworkbench-pack.json)" = "$release_version"' "$RELEASE_RUNBOOK"
require_text 'mapfile -t release_port_env < <("${release_env[@]}" ./scripts/assign_test_ports.sh)' "$RELEASE_RUNBOOK"
require_text 'release_compose_config="$("${release_env[@]}" docker compose --env-file .env.example --profile '\''*'\''' "$RELEASE_RUNBOOK"
require_text '"${release_env[@]}" jq -e \' "$RELEASE_RUNBOOK"
require_text '--arg new "${release_port_values[5]}"' "$RELEASE_RUNBOOK"
require_text 'all(.services[]; (.container_name? // "") == "")' "$RELEASE_RUNBOOK"
for service in postgres replica logical-subscriber pgbouncer postgres-old postgres-new; do
  require_text "published(\"$service\";" "$RELEASE_RUNBOOK"
done
require_text 'down --volumes --remove-orphans' "$RELEASE_RUNBOOK"
require_text 'resources="$("${release_env[@]}" docker ps -aq' "$RELEASE_RUNBOOK"
require_text 'resources="$("${release_env[@]}" docker volume ls -q' "$RELEASE_RUNBOOK"
require_text 'resources="$("${release_env[@]}" docker network ls -q' "$RELEASE_RUNBOOK"
require_count ')" || return 1' "$RELEASE_RUNBOOK" 3
if grep -Eq '(^|[[:space:]])VERSION=[0-9]+\.[0-9]+\.[0-9]+|build_candidate_binary\.sh [0-9]+\.[0-9]+\.[0-9]+|matrix_run_id="v[0-9]+\.' "$RELEASE_RUNBOOK"; then
  echo 'FAIL: release runbook hardcodes a candidate version in an executable command' >&2
  exit 1
fi
if grep -Eq 'pgworkbench-[0-9]+\.[0-9]+\.[0-9]+|version=[0-9]+\.[0-9]+\.[0-9]+|`v[0-9]+\.[0-9]+\.[0-9]+`' "$RELEASE_RUNBOOK"; then
  echo 'FAIL: release runbook contains a stale operational candidate version' >&2
  exit 1
fi
if grep -Fq 'COMPOSE_PROJECT_NAME' "$RELEASE_RUNBOOK"; then
  echo 'FAIL: release runbook overrides the checkout-derived Compose project identity' >&2
  exit 1
fi
for operational_doc in \
  README.md \
  docs/ci.md \
  docs/go-migration.md \
  docs/compatibility.md \
  docs/authoring-tutorial.md \
  docs/release-evidence.md \
  docs/roadmap.md \
  docs/post-v0.2-roadmap.md; do
  if grep -Fq '0.2.0' "$REPO_DIR/$operational_doc"; then
    echo "FAIL: stale candidate version remains in operational documentation: $operational_doc" >&2
    exit 1
  fi
done
require_text "\`v$VERSION\` candidate:" "$REPO_DIR/README.md"
for legacy_name in POSTGRES_CONTAINER POSTGRES_REPLICA_CONTAINER POSTGRES_LOGICAL_SUBSCRIBER_CONTAINER PGBOUNCER_CONTAINER POSTGRES_UPGRADE_OLD_CONTAINER POSTGRES_UPGRADE_NEW_CONTAINER; do
  if grep -Fq "$legacy_name" "$REPO_DIR/compose.yaml" || grep -Fq "$legacy_name" "$REPO_DIR/.env.example" || grep -Fq "$legacy_name" "$REPO_DIR/scripts/run_workload.sh"; then
    echo "FAIL: removed fixed-name Compose override remains active: $legacy_name" >&2
    exit 1
  fi
done

printf 'PASS: immutable candidate, run-manifest identity, and release runbook guards\n'
