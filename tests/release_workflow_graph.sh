#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKFLOW="$REPO_DIR/.github/workflows/release-snapshot.yml"
CHECK_WORKFLOW="$REPO_DIR/.github/workflows/check.yml"
COMPATIBILITY_WORKFLOW="$REPO_DIR/.github/workflows/compatibility.yml"
COMPATIBILITY_LEDGER="$REPO_DIR/compatibility/matrix.json"

job_block() {
  local job="$1"
  awk -v header="  ${job}:" '
    $0 == header { active = 1 }
    active && $0 ~ /^  [A-Za-z0-9_-]+:$/ && $0 != header { exit }
    active { print }
  ' "$WORKFLOW"
}

require_line() {
  local block="$1"
  local line="$2"
  local message="$3"
  if ! grep -Fxq -- "$line" <<<"$block"; then
    echo "FAIL: $message" >&2
    exit 1
  fi
}

require_text() {
  local block="$1"
  local text="$2"
  local message="$3"
  if ! grep -Fq -- "$text" <<<"$block"; then
    echo "FAIL: $message" >&2
    exit 1
  fi
}

prepare="$(job_block prepare)"
build_snapshot="$(job_block build-snapshot)"
verify_candidate_artifact="$(job_block verify-candidate-artifact)"
attest_and_create_draft="$(job_block attest-and-create-draft)"
source_compatibility="$(job_block source-compatibility)"
draft_verify="$(job_block draft-verify)"
draft_compatibility="$(job_block draft-compatibility)"
draft_external_drivers="$(job_block draft-external-drivers)"
verify_publication_evidence="$(job_block verify-publication-evidence)"
seal_preventive_controls="$(job_block seal-preventive-controls)"
publish_release="$(job_block publish-release)"
public_verify="$(job_block public-verify)"
published_compatibility="$(job_block published-compatibility)"
seal_source_draft_aggregate="$(job_block seal-source-draft-and-aggregate-evidence)"
seal_published_compatibility="$(job_block seal-published-compatibility)"
last_job="$(awk '/^  [A-Za-z0-9_-]+:$/ { name = $1; sub(/:$/, "", name) } END { print name }' "$WORKFLOW")"
release_header="$(awk '/^jobs:$/ { exit } { print }' "$WORKFLOW")"
check_header="$(awk '/^jobs:$/ { exit } { print }' "$CHECK_WORKFLOW")"

external_verification_summary="$(awk '
  $0 == "      - name: Write typed external-driver verification summary" { active = 1 }
  active && $0 ~ /^      - name:/ && $0 != "      - name: Write typed external-driver verification summary" { exit }
  active { print }
' <<<"$verify_publication_evidence")"
external_verification_upload="$(awk '
  $0 == "      - name: Upload typed external-driver verification summary" { active = 1 }
  active && $0 ~ /^      - name:/ && $0 != "      - name: Upload typed external-driver verification summary" { exit }
  active { print }
' <<<"$verify_publication_evidence")"
candidate_verifier_setup="$(awk '
  $0 == "      - name: Create isolated candidate verifier identity" { active = 1 }
  active && $0 ~ /^      - name:/ && $0 != "      - name: Create isolated candidate verifier identity" { exit }
  active { print }
' <<<"$verify_publication_evidence")"
candidate_verifier_cleanup="$(awk '
  $0 == "      - name: Destroy isolated candidate verifier identity" { active = 1 }
  active && $0 ~ /^      - name:/ && $0 != "      - name: Destroy isolated candidate verifier identity" { exit }
  active { print }
' <<<"$verify_publication_evidence")"

duplicate_external_step_names="$(
  sed -n 's/^      - name: //p' <<<"$draft_external_drivers" | LC_ALL=C sort | uniq -d
)"
if [[ -n "$duplicate_external_step_names" ]]; then
  echo "FAIL: draft external-driver job has duplicate step names: $duplicate_external_step_names" >&2
  exit 1
fi

if ! awk '
  function indentation(line, copy) {
    copy = line
    sub(/[^ ].*$/, "", copy)
    return length(copy)
  }
  function unsafe(line) {
    return line ~ /\$\{\{[[:space:]]*inputs\./
  }
  {
    current_indent = indentation($0)
    if (in_block && $0 !~ /^[[:space:]]*$/ && current_indent <= run_indent) {
      in_block = 0
    }
    if ($0 ~ /^[[:space:]]+run:[[:space:]]*/) {
      if (unsafe($0)) {
        printf "%s:%d:%s\n", FILENAME, FNR, $0 > "/dev/stderr"
        found = 1
      }
      if ($0 ~ /^[[:space:]]+run:[[:space:]]*[|>][-+0-9]*[[:space:]]*$/) {
        in_block = 1
        run_indent = current_indent
      }
      next
    }
    if (in_block && unsafe($0)) {
      printf "%s:%d:%s\n", FILENAME, FNR, $0 > "/dev/stderr"
      found = 1
    }
  }
  END { exit found ? 1 : 0 }
' "$REPO_DIR"/.github/workflows/*.yml; then
  echo 'FAIL: workflow inputs must enter shell scripts through env, never direct expression interpolation' >&2
  exit 1
fi

require_line "$release_header" 'permissions:' 'release workflow must declare fail-closed top-level permissions'
require_line "$release_header" '  contents: read' 'release workflow must default to read-only repository contents'
require_line "$release_header" 'concurrency:' 'release workflow must serialize one exact-ref release graph'
require_line "$release_header" '  group: release-snapshot-${{ github.ref }}' \
  'release concurrency must be scoped to the exact tag or dispatch ref'
require_line "$release_header" '  cancel-in-progress: false' \
  'a later release run must not cancel a candidate already at irreversible publication gates'
require_line "$check_header" 'permissions:' 'check workflow must declare fail-closed top-level permissions'
require_line "$check_header" '  contents: read' 'check workflow must default to read-only repository contents'

require_line "$source_compatibility" '      version: ${{ needs.prepare.outputs.version }}' \
  'source compatibility must use the exact version resolved by prepare'
require_line "$source_compatibility" '      attestations: read' \
  'source compatibility caller must grant the reusable workflow attestation permission'

require_line "$build_snapshot" '      contents: read' \
  'candidate build must have read-only repository contents'
require_line "$build_snapshot" '          persist-credentials: false' \
  'candidate checkout must not persist even its read-only token'
require_text "$build_snapshot" '--package-root "$package_root" "$sbom"' \
  'candidate build SPDX verification must bind each document to its extracted platform package'
require_line "$build_snapshot" '      artifact_digest: ${{ steps.upload.outputs.artifact-digest }}' \
  'candidate build must export the platform artifact digest'
require_line "$build_snapshot" '      artifact_fingerprint: ${{ steps.inventory.outputs.artifact_fingerprint }}' \
  'candidate build must export its fixed ten-file fingerprint'
require_line "$build_snapshot" '      artifact_id: ${{ steps.upload.outputs.artifact-id }}' \
  'candidate build must export the immutable platform artifact id'
require_line "$build_snapshot" '      artifact_name: ${{ steps.inventory.outputs.artifact_name }}' \
  'candidate build must export the exact producer-attempt artifact name'
require_text "$build_snapshot" 'artifact_name="release-candidate-$VERSION-$GITHUB_SHA-$GITHUB_RUN_ATTEMPT"' \
  'candidate artifact identity must include version, commit, and producer run attempt'
require_text "$build_snapshot" '"$(find "$release_dir" -mindepth 1 -maxdepth 1 | wc -l | tr -d '\'' '\'')" -eq 10' \
  'candidate artifact must contain exactly ten unsigned files'
require_line "$build_snapshot" '          name: ${{ steps.inventory.outputs.artifact_name }}' \
  'candidate upload must use the exact exported artifact name'
for forbidden in 'contents: write' 'id-token: write' 'attestations: write' \
  'artifact-metadata: write' 'actions/attest@' 'gh release create'; do
  if grep -Fq -- "$forbidden" <<<"$build_snapshot"; then
    echo "FAIL: candidate build carries publication capability: $forbidden" >&2
    exit 1
  fi
done

require_line "$verify_candidate_artifact" '      - build-snapshot' \
  'semantic candidate verification must depend on the exact build producer'
require_line "$verify_candidate_artifact" '      actions: read' \
  'semantic candidate verification must have only artifact-read capability'
require_line "$verify_candidate_artifact" '      contents: read' \
  'semantic candidate verification must retain read-only repository contents'
require_line "$verify_candidate_artifact" '          artifact-ids: ${{ env.CANDIDATE_ARTIFACT_ID }}' \
  'semantic candidate verification must download the immutable producer artifact id'
require_text "$verify_candidate_artifact" '.workflow_run.id == $run_id' \
  'semantic candidate verification must bind artifact metadata to this workflow run'
require_text "$verify_candidate_artifact" '.workflow_run.head_sha == $head_sha' \
  'semantic candidate verification must bind artifact metadata to the candidate commit'
require_text "$verify_candidate_artifact" 'test "$artifact_fingerprint" = "$CANDIDATE_ARTIFACT_FINGERPRINT"' \
  'semantic candidate verification must rehash the fixed artifact'
require_text "$verify_candidate_artifact" '"$verifier" release manifest verify' \
  'candidate semantics must be verified before publication privilege is introduced'
require_text "$verify_candidate_artifact" '"$verifier" release sbom verify' \
  'candidate SBOM semantics must be verified before publication privilege is introduced'
for forbidden in 'contents: write' 'id-token: write' 'attestations: write' \
  'artifact-metadata: write' 'actions/attest@' 'gh release create'; do
  if grep -Fq -- "$forbidden" <<<"$verify_candidate_artifact"; then
    echo "FAIL: semantic candidate verification carries publication capability: $forbidden" >&2
    exit 1
  fi
done

require_line "$attest_and_create_draft" "    if: github.ref_type == 'tag'" \
  'attestation and draft creation must be tag-only'
require_line "$attest_and_create_draft" '      - build-snapshot' \
  'protected draft creation must consume the exact build artifact'
require_line "$attest_and_create_draft" '      - verify-candidate-artifact' \
  'protected draft creation must wait for read-only semantic verification'
require_line "$attest_and_create_draft" '    environment: release-publication' \
  'attestation and draft creation must use a protected publication environment'
for permission in '      actions: read' '      artifact-metadata: write' \
  '      attestations: write' '      contents: write' '      id-token: write'; do
  require_line "$attest_and_create_draft" "$permission" \
    "protected draft creation lacks required permission: $permission"
done
require_line "$attest_and_create_draft" '          artifact-ids: ${{ env.CANDIDATE_ARTIFACT_ID }}' \
  'protected draft creation must download the immutable producer artifact id'
require_text "$attest_and_create_draft" '.digest == $digest' \
  'protected draft creation must bind the REST artifact digest'
require_text "$attest_and_create_draft" '.workflow_run.id == $run_id' \
  'protected draft creation must bind artifact metadata to this workflow run'
require_text "$attest_and_create_draft" '.workflow_run.head_sha == $head_sha' \
  'protected draft creation must bind artifact metadata to the candidate commit'
require_text "$attest_and_create_draft" '"$(find "$release_dir" -mindepth 1 -maxdepth 1 | wc -l | tr -d '\'' '\'')" -eq 10' \
  'protected draft creation must require the fixed unsigned ten-file candidate'
require_text "$attest_and_create_draft" '([.rules[].type] | index("creation")) != null' \
  'protected pre-draft control must restrict tag creation'
require_text "$attest_and_create_draft" '([.rules[].type] | index("update")) != null' \
  'protected pre-draft control must prohibit tag updates'
require_text "$attest_and_create_draft" '([.rules[].type] | index("deletion")) != null' \
  'protected pre-draft control must prohibit tag deletion'
require_text "$attest_and_create_draft" 'test "$live_ruleset_digest" = "sha256:$(sha256sum' \
  'protected publisher must bind the final live ruleset bytes to preflight evidence'
require_text "$attest_and_create_draft" 'test "$live_immutable_digest" = "sha256:$(sha256sum' \
  'protected publisher must bind final immutable-release bytes to preflight evidence'
require_text "$attest_and_create_draft" 'actions/attest@c32b4b8b198b65d0bd9d63490e847ff7b53989d4' \
  'protected publisher must create signed provenance and SBOM attestations'
require_text "$attest_and_create_draft" 'gh release create "$tag"' \
  'protected publisher must create the fixed draft'
require_line "$attest_and_create_draft" '      controls_artifact_digest: ${{ steps.upload_controls.outputs.artifact-digest }}' \
  'protected control producer must export the platform artifact digest'
require_line "$attest_and_create_draft" '      controls_artifact_id: ${{ steps.upload_controls.outputs.artifact-id }}' \
  'protected control producer must export the immutable artifact id'
require_line "$attest_and_create_draft" '      controls_artifact_name: ${{ steps.controls.outputs.artifact_name }}' \
  'protected control producer must export its exact attempt-qualified name'
require_text "$attest_and_create_draft" 'artifact_name="release-controls-${GITHUB_REF_NAME}-${GITHUB_SHA}-${GITHUB_RUN_ATTEMPT}"' \
  'protected control artifact identity must include tag, commit, and run attempt'
require_text "$attest_and_create_draft" '--draft --verify-tag' \
  'protected publisher must leave the release in draft state and verify the tag'
for forbidden in 'actions/checkout@' 'make ' 'go run' 'go test' './scripts/' \
  'tar -x' '"$verifier"' ' release manifest verify' ' release sbom verify' \
  '--notes-file CHANGELOG.md'; do
  if grep -Fq -- "$forbidden" <<<"$attest_and_create_draft"; then
    echo "FAIL: protected publication job executes candidate/repository code: $forbidden" >&2
    exit 1
  fi
done
if [[ "$(grep -v '^[[:space:]]*$' <<<"$attest_and_create_draft" | tail -n 1)" != \
      '          gh release create "$tag" "${assets[@]}" --repo "$GITHUB_REPOSITORY" --draft --verify-tag --title "$tag" --notes "$notes"' ]]; then
  echo 'FAIL: fixed draft creation must be the protected job final command' >&2
  exit 1
fi

require_line "$draft_verify" "    if: github.ref_type == 'tag'" 'draft verification must be tag-only'
require_line "$draft_verify" '      - attest-and-create-draft' \
  'draft verification must depend on protected draft creation'
require_text "$draft_verify" 'asset_fingerprint=' 'draft verification must bind the verified asset inventory'
require_text "$draft_verify" 'pgworkbench.release-asset-inventory/v1' \
  'draft verification must retain a typed asset inventory'
require_text "$draft_verify" 'draft-verification/asset-inventory.json' \
  'draft typed asset inventory must remain outside the fingerprinted release assets'
require_text "$draft_verify" '--package-root "$package_root" "$sbom"' \
  'draft SPDX verification must bind each document to its extracted platform package'
require_text "$draft_verify" 'pgworkbench.release-asset-verification/v1' \
  'draft verifier must emit the typed release-asset fact record'
require_text "$draft_verify" "--arg job draft-verify" \
  'draft asset record must identify its only allowed producer job'
require_text "$draft_verify" 'inventory: $inventory[0]' \
  'draft asset record must embed the complete provider inventory'
require_text "$draft_verify" 'scenario_pack: $manifest[0].scenario_pack' \
  'draft asset record must derive pack identity from the verified manifest'
require_text "$draft_verify" 'test "$(jq -r --arg name "$manifest_name"' \
  'draft asset record must bind the manifest bytes to the provider inventory'
require_text "$draft_verify" '> draft-verification/asset-verification.json' \
  'draft typed fact record must remain in the existing verification artifact'
require_line "$draft_verify" '      verification_artifact_digest: ${{ steps.upload_verification.outputs.artifact-digest }}' \
  'draft verifier must export the exact provider artifact digest'
require_line "$draft_verify" '      verification_artifact_id: ${{ steps.upload_verification.outputs.artifact-id }}' \
  'draft verifier must export the immutable provider artifact id'
require_line "$draft_verify" '      verification_artifact_name: ${{ steps.draft_assets.outputs.artifact_name }}' \
  'draft verifier must export its producer-attempt artifact name'
require_line "$draft_verify" '          name: draft-verification-${{ github.ref_name }}-${{ github.sha }}-${{ github.run_attempt }}' \
  'draft verification upload name must be exact and rerun-safe'
for bounded_fact in \
  'actions_artifact_durable: false' \
  'candidate_identity_reverified: true' \
  'provider_asset_set_recomputed: true' \
  'all_downloaded_bytes_verified: true' \
  'performance_claim: false' \
  'benchmark_comparability_claim: false' \
  'recovery_claim: false' \
  'production_decision_eligible: false'; do
  require_text "$draft_verify" "$bounded_fact" \
    "draft asset record lacks bounded fact: $bounded_fact"
done
if grep -Eq '^[[:space:]]+(status|passed):' <<<"$draft_verify"; then
  echo 'FAIL: draft asset producer may not emit a caller-selected gate outcome' >&2
  exit 1
fi
draft_manifest_check_line="$(grep -Fn -- '"$verifier" release manifest verify' <<<"$draft_verify" | head -n 1 | cut -d: -f1)"
draft_summary_line="$(grep -Fn -- 'pgworkbench.release-asset-verification/v1' <<<"$draft_verify" | tail -n 1 | cut -d: -f1)"
if [[ -z "$draft_manifest_check_line" || -z "$draft_summary_line" ]] || \
   (( draft_summary_line <= draft_manifest_check_line )); then
  echo 'FAIL: draft asset summary must follow candidate manifest verification' >&2
  exit 1
fi
require_line "$draft_compatibility" "    if: github.ref_type == 'tag'" 'draft compatibility must be tag-only'
require_line "$draft_compatibility" '      - attest-and-create-draft' \
  'draft compatibility must depend on protected draft creation'
require_line "$draft_compatibility" '      - draft-verify' 'draft compatibility must depend on clean draft verification'
require_line "$draft_compatibility" '      qualification_mode: draft' 'draft compatibility must label evidence as draft'
require_line "$draft_external_drivers" "    if: github.ref_type == 'tag'" 'draft external-driver gate must be tag-only'
require_line "$draft_external_drivers" '      - attest-and-create-draft' \
  'draft external-driver gate must depend on protected draft creation'
require_line "$draft_external_drivers" '      - draft-verify' 'draft external-driver gate must depend on clean draft verification'
require_line "$draft_external_drivers" '      - draft-compatibility' 'draft external-driver gate must depend on draft compatibility'
require_line "$draft_external_drivers" '    runs-on: ubuntu-24.04' \
  'real external-driver release smoke must use the pinned GitHub-hosted image label'
require_line "$draft_external_drivers" '    environment: release-external-drivers' \
  'real external-driver release smoke must retain the protected approval environment'
for permission in '      actions: read' '      attestations: read' '      contents: read'; do
  require_line "$draft_external_drivers" "$permission" \
    "hosted external-driver release smoke lacks required read permission: $permission"
done
require_line "$draft_external_drivers" '          artifact-ids: ${{ env.VERIFIED_CONTROLS_ARTIFACT_ID }}' \
  'external-driver job must consume the immutable protected-control artifact id'
require_text "$draft_external_drivers" '.workflow_run.id == $run_id' \
  'external-driver job must bind protected control evidence to this run'
require_text "$draft_external_drivers" '.tag_ruleset.creation_restricted == true' \
  'external-driver job must consume evidence of restricted tag creation'
if grep -Eq 'secrets\.|PGWORKBENCH_ADMIN_READ_TOKEN|IMMUTABLE_RELEASES_ADMIN_TOKEN|TAG_RULESET_ADMIN_REVIEW|contents: write|id-token: write|attestations: write' <<<"$draft_external_drivers"; then
  echo 'FAIL: hosted candidate execution job may not receive secrets or mutation/admin capability' >&2
  exit 1
fi
require_text "$draft_external_drivers" 'gh release download "$GITHUB_REF_NAME"' \
  'external-driver gate must execute from a downloaded draft archive'
require_text "$draft_external_drivers" '"${sandbox_env[@]}" "$PGWORKBENCH_BIN" benchmark drivers --json' \
  'external-driver gate must derive the complete advertised set from the draft archive'
require_text "$draft_external_drivers" "printf 'PGWORKBENCH_ROOT=%s" \
  'external-driver executions must resolve the downloaded archive as their only pack root'
require_text "$draft_external_drivers" 'benchbase-postgresql-33c0047' \
  'external-driver gate must execute the pinned BenchBase adapter'
require_text "$draft_external_drivers" 'hammerdb-postgresql-6.0' \
  'external-driver gate must execute the pinned HammerDB adapter'
require_text "$draft_external_drivers" 'sysbench-postgresql-1.0.20' \
  'external-driver gate must execute the pinned sysbench adapter'
require_text "$draft_external_drivers" '"$PGWORKBENCH_BIN" benchmark driver-run --json' \
  'external-driver gate must invoke the real external-driver execution envelope'
require_text "$draft_external_drivers" '--driver "$driver" --runtime-root "$runtime_root"' \
  'external-driver execution must retain an explicit per-driver runtime root'
require_text "$draft_external_drivers" '"$PGWORKBENCH_BIN" benchmark driver-run-verify --json' \
  'the producer must locally verify every full execution artifact before deleting it'
require_text "$draft_external_drivers" 'scripts/provision_external_driver_gate.sh" provision' \
  'hosted gate must acquire exact runtimes and prepare datasets through the candidate helper'
require_text "$draft_external_drivers" 'EXPECTED_RUNNER_ENVIRONMENT: ${{ runner.environment }}' \
  'hosted gate must measure the runner environment in a context where it is available'
require_text "$draft_external_drivers" 'test "$EXPECTED_RUNNER_ENVIRONMENT" = github-hosted' \
  'hosted gate must reject a non-hosted runner'
if grep -Fq -- '${{ runner.temp }}' <<<"$(awk '
  /^    env:$/ { active = 1; next }
  active && /^    [a-zA-Z]/ { exit }
  active { print }
' <<<"$draft_external_drivers")"; then
  echo 'FAIL: runner context is unavailable in job-level env' >&2
  exit 1
fi
require_text "$draft_external_drivers" 'sudo --user "$EXTERNAL_DRIVER_SANDBOX_USER" -- env -i' \
  'candidate and upstream execution must use the credential-free sandbox identity'
sandbox_env_blocks="$(awk '
  /^[[:space:]]+sandbox_env=\($/ { active = 1 }
  active { print }
  active && /^[[:space:]]+\)$/ { active = 0; print "--END--" }
' <<<"$draft_external_drivers")"
test "$(grep -Fxc -- '--END--' <<<"$sandbox_env_blocks")" -eq 2 || {
  echo 'FAIL: expected exactly two credential-free sandbox env definitions' >&2
  exit 1
}
if grep -Eq 'ACTIONS_|GH_TOKEN|GITHUB_TOKEN|GITHUB_ENV|GITHUB_OUTPUT|GITHUB_PATH' <<<"$sandbox_env_blocks"; then
  echo 'FAIL: sandbox env exposes GitHub command files or service credentials' >&2
  exit 1
fi
require_text "$draft_external_drivers" '--state-dir "$EXTERNAL_DRIVER_STATE_DIR"' \
  'hosted provisioning must stay beneath a fresh runner-temporary state root'
require_text "$draft_external_drivers" 'test "$actual_names" = "$expected_names"' \
  'hosted provisioning outputs must use an exact environment-variable allowlist'
require_text "$draft_external_drivers" '"$EXTERNAL_DRIVER_STATE_DIR"/*' \
  'all acquired runtime and provisioning paths must remain beneath the owned state root'
require_text "$draft_external_drivers" 'postgresql-16 postgresql-client-16' \
  'hosted release smoke must install and own PostgreSQL 16'
require_text "$draft_external_drivers" 'refusing to reuse a pre-existing PostgreSQL target' \
  'hosted release smoke must reject an existing loopback PostgreSQL target'
require_text "$draft_external_drivers" '"$BENCHBASE_CONFIG"' \
  'hosted release smoke must use the provisioner-validated BenchBase release-only config'
require_text "$draft_external_drivers" 'hammerdb-v6-tprocc-release-smoke.json' \
  'hosted release smoke must use the 20W/4VU/1m/2m/1M HammerDB config'
require_text "$draft_external_drivers" '"$HAMMERDB_TEMPLATE"' \
  'HammerDB execute-only marker must come from ephemeral provisioned state'
require_text "$draft_external_drivers" '"$EXTERNAL_DRIVER_EVIDENCE_DIR/provisioning/$document.json"' \
  'hosted release smoke must retain only project-authored provisioning metadata'
require_text "$draft_external_drivers" 'gate_digest=' \
  'external-driver gate must publish a digest bound to the draft candidate'
require_text "$draft_external_drivers" 'evidence_archive_digest=' \
  'external-driver gate must bind its metadata-only archive'
require_text "$draft_external_drivers" 'pgworkbench.release-external-driver-gate/v2' \
  'external-driver gate must emit the current metadata-only gate contract'
require_text "$draft_external_drivers" 'qualification_mode: "draft-release-smoke"' \
  'hosted external execution must remain a release smoke, not a benchmark result'
require_text "$draft_external_drivers" 'artifact_payload: "metadata-only-no-third-party-runtime-bytes"' \
  'external-driver artifact must declare the metadata-only license boundary'
require_text "$draft_external_drivers" 'third_party_runtime_bytes_uploaded: false' \
  'external-driver artifact must explicitly deny uploading third-party runtime bytes'
require_text "$draft_external_drivers" 'performance_claim: false' \
  'external-driver release smoke must make no performance claim'
require_text "$draft_external_drivers" 'binary_distributed_by_project: false' \
  'external-driver release smoke must not claim project distribution of driver binaries'
require_text "$draft_external_drivers" 'project_redistribution: false' \
  'external-driver evidence must explicitly deny project redistribution of runtime bytes'
require_text "$draft_external_drivers" 'runtime_replay_available: false' \
  'metadata-only external-driver evidence must not claim runtime replay'
require_text "$draft_external_drivers" 'complete_license_or_source_closure_attested: false' \
  'external-driver evidence must not claim a complete license or source closure'
for metadata_path in \
  executions/benchbase-postgresql-33c0047.json \
  executions/hammerdb-postgresql-6.0.json \
  executions/sysbench-postgresql-1.0.20.json \
  gate.json provisioning/acquisitions.json provisioning/datasets.json \
  provisioning/host.json provisioning/runtime-set.json \
  repository-controls/repository-controls.json; do
  require_text "$draft_external_drivers" "$metadata_path" \
    "metadata-only producer allowlist is missing $metadata_path"
done
require_text "$draft_external_drivers" 'test "$actual" = "$expected"' \
  'metadata producer must reject any file outside the explicit allowlist'
require_text "$draft_external_drivers" 'find "$evidence" -mindepth 1 -printf' \
  'metadata producer must inventory directories, links, and special entries too'
require_text "$draft_external_drivers" "-name '*.tcl'" \
  'metadata producer must explicitly reject retained Tcl files'
require_text "$draft_external_drivers" 'metadata-only.tar.gz' \
  'external-driver producer must package a metadata-only archive'
require_text "$draft_external_drivers" 'test "$actual_package" = "$expected_package"' \
  'external-driver producer must enforce the two-file outer package allowlist'
require_text "$draft_external_drivers" 'stat -c %s "$failure"' \
  'failure metadata must be bounded and checked immediately before upload'
failure_upload="$(awk '
  $0 == "      - name: Upload failed external-driver metadata" { active = 1 }
  active && $0 ~ /^      - name:/ && $0 != "      - name: Upload failed external-driver metadata" { exit }
  active { print }
' <<<"$draft_external_drivers")"
require_line "$failure_upload" '          path: ${{ env.EXTERNAL_DRIVER_EVIDENCE_DIR }}/failure.json' \
  'failure upload must contain only the workflow-authored failure record'
if grep -Fq -- 'provisioning/' <<<"$failure_upload"; then
  echo 'FAIL: failure artifact may not contain candidate-produced provisioning metadata' >&2
  exit 1
fi
if grep -Eq '^[[:space:]]+path:[[:space:]]+\$\{\{ env\.EXTERNAL_DRIVER_(EVIDENCE|STATE|WORK)_DIR \}\}/?$' <<<"$draft_external_drivers"; then
  echo 'FAIL: external-driver upload may not include a whole evidence, state, or work directory' >&2
  exit 1
fi
require_line "$draft_external_drivers" '        if: always()' \
  'hosted release smoke must destroy databases and acquired runtime bytes on every outcome'
require_text "$draft_external_drivers" '"$helper" cleanup --state-dir "$EXTERNAL_DRIVER_STATE_DIR"' \
  'hosted release smoke must invoke marker-protected provisioning cleanup'
require_text "$draft_external_drivers" 'test ! -e "$EXTERNAL_DRIVER_STATE_DIR"' \
  'hosted release smoke must verify that acquired runtime/database state is gone'
require_text "$draft_external_drivers" 'test ! -e "$EXTERNAL_DRIVER_WORK_DIR"' \
  'hosted release smoke must verify that full execution bytes are gone'
require_text "$draft_external_drivers" 'test ! -e "$EXTERNAL_DRIVER_SANDBOX_ROOT"' \
  'hosted release smoke must delete and verify the entire dedicated sandbox'
require_text "$verify_publication_evidence" 'pinned_loader_rng_seed' \
  'BenchBase dataset evidence must bind the pinned loader seed'
require_text "$verify_publication_evidence" 'pinned_workload_random_seed' \
  'BenchBase dataset evidence must bind the explicit workload seed'
require_text "$verify_publication_evidence" 'format:"tar.gz-links",root:"HammerDB-6.0"' \
  'downstream evidence must bind HammerDB contained-link archive semantics'
require_line "$draft_external_drivers" '      artifact_name: ${{ steps.package.outputs.artifact_name }}' \
  'external-driver gate must expose the exact producer artifact name'
require_text "$draft_external_drivers" "printf 'artifact_name=%s" \
  'external-driver gate must record its producer-attempt artifact name'
require_line "$draft_external_drivers" '          name: ${{ steps.package.outputs.artifact_name }}' \
  'external-driver upload must use the exact exported artifact name'
require_line "$draft_external_drivers" '      artifact_digest: ${{ steps.upload.outputs.artifact-digest }}' \
  'external-driver gate must export the platform artifact digest'
require_line "$draft_external_drivers" '      artifact_id: ${{ steps.upload.outputs.artifact-id }}' \
  'external-driver gate must export the immutable platform artifact id'
if grep -Eiq 'fake|testdata|go test' <<<"$draft_external_drivers"; then
  echo 'FAIL: publication external-driver gate may not substitute fake or unit-test producers' >&2
  exit 1
fi

require_line "$verify_publication_evidence" "    if: github.ref_type == 'tag'" \
  'semantic publication evidence verification must be tag-only'
require_line "$verify_publication_evidence" '      contents: read' \
  'semantic publication evidence verification must remain read-only'
require_line "$verify_publication_evidence" '          artifact-ids: ${{ env.VERIFIED_EXTERNAL_DRIVER_ARTIFACT_ID }}' \
  'semantic publication evidence verification must consume the immutable producer id'
require_text "$verify_publication_evidence" 'metadata-only.tar.gz' \
  'read-only job must consume the metadata-only external-driver archive'
require_text "$verify_publication_evidence" 'test "$actual_metadata" = "$expected_metadata"' \
  'read-only job must independently enforce the exact metadata file allowlist'
require_text "$verify_publication_evidence" 'test "$metadata_digest" = "$gate_metadata_digest"' \
  'read-only job must bind each sanitized execution record to the gate digest'
require_text "$verify_publication_evidence" '.third_party_runtime_bytes_uploaded == false' \
  'read-only job must enforce the no-third-party-runtime artifact boundary'
for license_expression in \
  'GPL-3.0-or-later AND Apache-2.0' 'GPL-3.0-or-later' 'GPL-2.0-or-later'; do
  require_text "$verify_publication_evidence" "$license_expression" \
    "read-only job must verify external runtime license metadata: $license_expression"
done
require_text "$verify_publication_evidence" '.runtime_replay_available == false' \
  'read-only job must enforce the no-runtime-replay boundary'
require_text "$verify_publication_evidence" '.runner.provider == "github-hosted" and .runner.environment == "github-hosted" and' \
  'read-only job must verify the exact hosted runner metadata contract'
require_text "$verify_publication_evidence" '.runner.label == "ubuntu-24.04"' \
  'read-only job must verify the exact hosted runner label'
for target_fact in \
  '.execution.target.acknowledged == true' \
  '.execution.target.loopback_only == true' \
  '.execution.target.system_databases_denied == true'; do
  if [[ "$(grep -Fc -- "$target_fact" <<<"$verify_publication_evidence")" -ne 2 ]]; then
    echo "FAIL: downstream verifier must independently check target fact in gate and execution records: $target_fact" >&2
    exit 1
  fi
done
require_line "$verify_publication_evidence" '      CANDIDATE_VERIFIER_SANDBOX_USER: pgwverify' \
  'read-only verifier must use a dedicated fixed sandbox identity'
if grep -Fq -- 'CANDIDATE_VERIFIER_SANDBOX_ROOT: ${{ runner.temp }}' <<<"$verify_publication_evidence"; then
  echo 'FAIL: runner context is unavailable in job-level env' >&2
  exit 1
fi
candidate_sandbox_root='CANDIDATE_VERIFIER_SANDBOX_ROOT="$RUNNER_TEMP/pgworkbench-publication-verifier-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT"'
if [[ "$(grep -Fc -- "$candidate_sandbox_root" <<<"$verify_publication_evidence")" -ne 4 ]]; then
  echo 'FAIL: every candidate verifier lifecycle step must derive the rerun-specific root from RUNNER_TEMP' >&2
  exit 1
fi
require_text "$candidate_verifier_setup" 'sudo useradd --system --user-group --no-create-home' \
  'candidate verifier must run as a dedicated unprivileged user'
require_text "$candidate_verifier_setup" 'test ! -e "$CANDIDATE_VERIFIER_SANDBOX_ROOT"' \
  'candidate verifier sandbox setup must refuse a pre-existing root'
require_text "$candidate_verifier_setup" 'install -m 0600 /dev/null "$ownership_marker"' \
  'candidate verifier setup must record workflow ownership before creating the account'
require_text "$candidate_verifier_setup" 'sudo install -d -o root -g root -m 0755' \
  'candidate verifier must not own the sandbox root containing its writable directories'
require_text "$verify_publication_evidence" 'sudo --user "$CANDIDATE_VERIFIER_SANDBOX_USER" -- env -i' \
  'downloaded candidate must execute with an empty inherited environment'
for isolated_env in \
  '"HOME=$CANDIDATE_VERIFIER_SANDBOX_ROOT/home"' \
  '"TMPDIR=$CANDIDATE_VERIFIER_SANDBOX_ROOT/tmp"' \
  '"XDG_CACHE_HOME=$CANDIDATE_VERIFIER_SANDBOX_ROOT/home/.cache"' \
  '"PGWORKBENCH_ROOT=$verifier_root"'; do
  require_text "$verify_publication_evidence" "$isolated_env" \
    "candidate verifier lacks isolated environment binding: $isolated_env"
done
require_text "$verify_publication_evidence" 'test -w "$GITHUB_WORKSPACE"' \
  'candidate verifier identity must be denied workspace write access'
require_text "$verify_publication_evidence" 'test -w "$verifier_root"' \
  'candidate verifier identity must be denied candidate-root write access'
require_text "$verify_publication_evidence" 'find "$GITHUB_WORKSPACE" -writable -print -quit' \
  'candidate verifier identity must be denied every writable path in the workspace'
require_text "$verify_publication_evidence" 'identity="$("${sandbox_env[@]}" "$verifier" version)"' \
  'candidate identity command must use the credential-free sandbox environment'
require_text "$verify_publication_evidence" 'drivers_json="$("${sandbox_env[@]}" "$verifier" benchmark drivers --json)"' \
  'candidate driver-registry command must use the credential-free sandbox environment'
if grep -Fq -- 'identity="$("$verifier" version)"' <<<"$verify_publication_evidence"; then
  echo 'FAIL: downloaded candidate verifier may not execute as the workflow user' >&2
  exit 1
fi
require_text "$verify_publication_evidence" '--pattern "$manifest" --dir draft-external-driver-manifest' \
  'read-only job must download the release manifest separately from the executable archive'
require_text "$verify_publication_evidence" 'test "$release_manifest_digest" = "$release_manifest_asset_digest"' \
  'read-only job must bind the separately downloaded release manifest to the draft asset inventory'
require_text "$verify_publication_evidence" 'gh attestation verify "draft-external-driver-manifest/$manifest"' \
  'read-only job must authenticate the separate release manifest before deriving pack identity'
require_text "$verify_publication_evidence" '.schema_version == "pgworkbench.release-manifest/v1"' \
  'read-only job must reject an unexpected release-manifest contract'
if grep -Fq -- 'benchmark driver-run-verify' <<<"$verify_publication_evidence"; then
  echo 'FAIL: metadata-only downstream verifier may not pretend to re-run verification without third-party runtime bytes' >&2
  exit 1
fi
for forbidden in 'contents: write' 'id-token: write' 'PGWORKBENCH_ADMIN_READ_TOKEN' \
  'IMMUTABLE_RELEASES_ADMIN_TOKEN' 'gh release edit'; do
  if grep -Fq -- "$forbidden" <<<"$verify_publication_evidence"; then
    echo "FAIL: semantic publication verification carries mutation/admin capability: $forbidden" >&2
    exit 1
  fi
done

require_line "$verify_publication_evidence" '      verification_artifact_digest: ${{ steps.upload_verification.outputs.artifact-digest }}' \
  'semantic verifier must export the typed summary artifact digest'
require_line "$verify_publication_evidence" '      verification_artifact_id: ${{ steps.upload_verification.outputs.artifact-id }}' \
  'semantic verifier must export the typed summary artifact id'
require_line "$verify_publication_evidence" '      verification_artifact_name: ${{ steps.verification_summary.outputs.artifact_name }}' \
  'semantic verifier must export the typed summary artifact name'
require_text "$external_verification_summary" 'pgworkbench.release-external-driver-verification/v1' \
  'read-only verifier must emit the typed external-driver summary contract'
require_text "$external_verification_summary" 'artifact_name="draft-external-driver-verification-${SUMMARY_TAG}-${SUMMARY_GIT_COMMIT}-${SUMMARY_WORKFLOW_RUN_ATTEMPT}"' \
  'typed external-driver summary artifact identity must include tag, commit, and run attempt'
for scenario_pack_field in \
  'id: $scenario_pack_id' \
  'version: $scenario_pack_version' \
  'digest: $scenario_pack_digest'; do
  require_text "$external_verification_summary" "$scenario_pack_field" \
    "typed summary lacks captured scenario-pack field: $scenario_pack_field"
done
if grep -Fq -- '--slurpfile release_manifest' <<<"$external_verification_summary"; then
  echo 'FAIL: typed summary may not reread mutable manifest semantics after candidate execution' >&2
  exit 1
fi
for captured_output in \
  'SUMMARY_SCENARIO_PACK_ID: ${{ steps.verify.outputs.summary_scenario_pack_id }}' \
  'SUMMARY_GATE_DIGEST: ${{ steps.verify.outputs.summary_gate_digest }}' \
  'SUMMARY_METADATA_ARCHIVE_DIGEST: ${{ steps.verify.outputs.summary_metadata_archive_digest }}' \
  'SUMMARY_RELEASE_ARCHIVE_DIGEST: ${{ steps.verify.outputs.summary_release_archive_digest }}' \
  'SUMMARY_RELEASE_MANIFEST_DIGEST: ${{ steps.verify.outputs.summary_release_manifest_digest }}' \
  'SUMMARY_VERIFIER_DIGEST: ${{ steps.verify.outputs.summary_verifier_digest }}'; do
  require_text "$external_verification_summary" "$captured_output" \
    "typed summary lacks verified step output binding: $captured_output"
done
for schema_bound in \
  'test "${#SUMMARY_WORKFLOW_RUN_ID}" -le 32' \
  'test "${#SUMMARY_PROVIDER_ARTIFACT_ID}" -le 32' \
  'test "${#SUMMARY_PROVIDER_ARTIFACT_NAME}" -le 256' \
  "'(\$value | tonumber) <= 9007199254740991'"; do
  require_text "$external_verification_summary" "$schema_bound" \
    "typed summary lacks schema parity bound: $schema_bound"
done
for final_digest in \
  '"$SUMMARY_GATE_DIGEST"' \
  '"$SUMMARY_METADATA_ARCHIVE_DIGEST"' \
  '"$SUMMARY_RELEASE_ARCHIVE_DIGEST"' \
  '"$SUMMARY_RELEASE_MANIFEST_DIGEST"' \
  '"$SUMMARY_VERIFIER_DIGEST"'; do
  require_text "$external_verification_summary" "$final_digest" \
    "typed summary lacks final exact-byte digest recheck: $final_digest"
done
for candidate_field in 'version: $version' 'tag: $tag' 'git_commit: $git_commit' \
  'asset_fingerprint: $asset_fingerprint'; do
  require_text "$external_verification_summary" "$candidate_field" \
    "typed external-driver summary lacks full candidate field: $candidate_field"
done
for workflow_field in 'id: $run_id' 'attempt: $run_attempt' 'head_sha: $git_commit' \
  'repository: $repository'; do
  require_text "$external_verification_summary" "$workflow_field" \
    "typed external-driver summary lacks workflow identity: $workflow_field"
done
for source_field in 'gate_digest: $gate_digest' \
  'metadata_archive_digest: $metadata_archive_digest' \
  'id: $provider_artifact_id' 'name: $provider_artifact_name' \
  'digest: $provider_artifact_digest' \
  'release_archive_digest: $release_archive_digest' \
  'release_manifest_digest: $release_manifest_digest'; do
  require_text "$external_verification_summary" "$source_field" \
    "typed external-driver summary lacks verified source identity: $source_field"
done
for driver in benchbase-postgresql-33c0047 hammerdb-postgresql-6.0 sysbench-postgresql-1.0.20; do
  require_text "$external_verification_summary" "\"$driver\"" \
    "typed external-driver summary lacks exact driver id: $driver"
done
for assurance in \
  'verification_scope: "workflow-local-content-and-semantics"' \
  'third_party_runtime_bytes_uploaded: false' \
  'performance_claim: false' \
  'production_decision_eligible: false' \
  'source_to_binary_attested: false' \
  'driver_runtime_closure_attested: true' \
  'host_runtime_dependencies_attested: false' \
  'benchmark_comparability_claim: false' \
  'project_redistribution: false' \
  'all_executions_locally_verified: true' \
  'exact_source_to_staged_file_match: true' \
  'disposable_loopback_target_acknowledged: true' \
  'system_databases_denied: true' \
  'candidate_identity_reverified: true' \
  'provider_artifact_reverified: true' \
  'release_archive_provenance_verified: true' \
  'release_manifest_provenance_verified: true'; do
  require_text "$external_verification_summary" "$assurance" \
    "typed external-driver summary lacks bounded assurance fact: $assurance"
done
if grep -Eq '^[[:space:]]+(status|passed):' <<<"$external_verification_summary"; then
  echo 'FAIL: typed external-driver summary may not contain a caller-selected gate outcome' >&2
  exit 1
fi
require_line "$external_verification_upload" '          name: ${{ steps.verification_summary.outputs.artifact_name }}' \
  'typed external-driver summary upload must use the exact exported artifact name'
require_line "$external_verification_upload" '          path: draft-external-driver-verification/verification.json' \
  'typed external-driver summary artifact must contain only the fact record'
require_line "$external_verification_upload" '          if-no-files-found: error' \
  'typed external-driver summary upload must fail closed when the record is absent'
require_line "$candidate_verifier_cleanup" '        if: always()' \
  'candidate verifier identity cleanup must run on every prior outcome'
for cleanup_fact in \
  'sudo pkill -TERM -u "$CANDIDATE_VERIFIER_SANDBOX_USER"' \
  'sudo pkill -KILL -u "$CANDIDATE_VERIFIER_SANDBOX_USER"' \
  'sudo pgrep -u "$CANDIDATE_VERIFIER_SANDBOX_USER"' \
  'sudo find "$CANDIDATE_VERIFIER_SANDBOX_ROOT" -depth -delete' \
  'sudo userdel "$CANDIDATE_VERIFIER_SANDBOX_USER"' \
  'sudo groupdel "$CANDIDATE_VERIFIER_SANDBOX_USER"' \
  'refusing to clean an unowned candidate verifier identity or root' \
  'unlink "$ownership_marker"' \
  'test ! -e "$CANDIDATE_VERIFIER_SANDBOX_ROOT"'; do
  require_text "$candidate_verifier_cleanup" "$cleanup_fact" \
    "candidate verifier cleanup lacks fail-closed control: $cleanup_fact"
done
require_text "$external_verification_summary" 'candidate verifier sandbox was not completely destroyed' \
  'typed summary must independently require completed candidate cleanup'
registry_check_line="$(grep -Fn -- 'test "$(jq -r .digest <<<"$drivers_json")" = "$gate_registry_digest"' \
  <<<"$verify_publication_evidence" | head -n 1 | cut -d: -f1)"
cleanup_step_line="$(grep -Fn -- '      - name: Destroy isolated candidate verifier identity' \
  <<<"$verify_publication_evidence" | head -n 1 | cut -d: -f1)"
summary_step_line="$(grep -Fn -- '      - name: Write typed external-driver verification summary' \
  <<<"$verify_publication_evidence" | head -n 1 | cut -d: -f1)"
if [[ -z "$registry_check_line" || -z "$cleanup_step_line" || -z "$summary_step_line" ]] || \
   (( cleanup_step_line <= registry_check_line || summary_step_line <= cleanup_step_line )); then
  echo 'FAIL: typed external-driver summary must follow candidate re-verification and sandbox cleanup' >&2
  exit 1
fi

require_line "$seal_preventive_controls" "    if: github.ref_type == 'tag'" \
  'preventive-control sealing must be tag-only'
for prerequisite in '      - attest-and-create-draft' '      - draft-verify' \
  '      - verify-publication-evidence'; do
  require_line "$seal_preventive_controls" "$prerequisite" \
    "preventive-control sealing lacks prerequisite: $prerequisite"
done
require_line "$seal_preventive_controls" '    environment: release-publication' \
  'preventive-control sealing must use the protected publication environment'
require_line "$seal_preventive_controls" '      actions: read' \
  'preventive-control sealing needs read-only artifact metadata access'
require_line "$seal_preventive_controls" '      contents: read' \
  'preventive-control sealing must keep repository contents read-only'
for forbidden_permission in 'contents: write' 'id-token: write' 'attestations: write' \
  'artifact-metadata: write'; do
  if grep -Fq -- "$forbidden_permission" <<<"$seal_preventive_controls"; then
    echo "FAIL: preventive-control sealing carries mutation permission: $forbidden_permission" >&2
    exit 1
  fi
done
for protected_input in \
  'IMMUTABLE_RELEASES_ADMIN_TOKEN: ${{ secrets.PGWORKBENCH_ADMIN_READ_TOKEN }}' \
  'TAG_RULESET_ADMIN_REVIEW_REF: ${{ vars.PGWORKBENCH_TAG_RULESET_ADMIN_REVIEW_REF }}' \
  'TAG_RULESET_ADMIN_REVIEW_DIGEST: ${{ vars.PGWORKBENCH_TAG_RULESET_ADMIN_REVIEW_DIGEST }}' \
  'TAG_RULESET_ADMIN_REVIEWER: ${{ vars.PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWER }}' \
  'TAG_RULESET_ADMIN_REVIEWED_AT: ${{ vars.PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWED_AT }}' \
  'TAG_RULESET_ADMIN_REVIEWED_ID: ${{ vars.PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWED_ID }}' \
  'TAG_RULESET_ADMIN_REVIEWED_UPDATED_AT: ${{ vars.PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWED_UPDATED_AT }}'; do
  require_text "$seal_preventive_controls" "$protected_input" \
    "preventive-control sealing lacks protected input: $protected_input"
done
for exact_input in \
  'SOURCE_CONTROLS_ARTIFACT_DIGEST: ${{ needs['"'"'attest-and-create-draft'"'"'].outputs.controls_artifact_digest }}' \
  'SOURCE_CONTROLS_ARTIFACT_ID: ${{ needs['"'"'attest-and-create-draft'"'"'].outputs.controls_artifact_id }}' \
  'SOURCE_CONTROLS_ARTIFACT_NAME: ${{ needs['"'"'attest-and-create-draft'"'"'].outputs.controls_artifact_name }}' \
  'DRAFT_VERIFICATION_ARTIFACT_DIGEST: ${{ needs['"'"'draft-verify'"'"'].outputs.verification_artifact_digest }}' \
  'DRAFT_VERIFICATION_ARTIFACT_ID: ${{ needs['"'"'draft-verify'"'"'].outputs.verification_artifact_id }}' \
  'DRAFT_VERIFICATION_ARTIFACT_NAME: ${{ needs['"'"'draft-verify'"'"'].outputs.verification_artifact_name }}'; do
  require_text "$seal_preventive_controls" "$exact_input" \
    "preventive-control sealing lacks exact producer identity: $exact_input"
done
require_text "$seal_preventive_controls" '.digest == $digest and .workflow_run.id == $run_id' \
  'preventive-control sealing must authenticate provider digest and run identity'
require_text "$seal_preventive_controls" '.workflow_run.head_sha == $head_sha' \
  'preventive-control sealing must authenticate both provider artifacts to the candidate commit'
require_text "$seal_preventive_controls" 'actions/artifacts/$SOURCE_CONTROLS_ARTIFACT_ID/zip' \
  'preventive-control sealing must download exact raw-control ZIP bytes by id'
require_text "$seal_preventive_controls" 'actions/artifacts/$DRAFT_VERIFICATION_ARTIFACT_ID/zip' \
  'preventive-control sealing must download exact draft-verification ZIP bytes by id'
require_text "$seal_preventive_controls" 'test "sha256:$(sha256sum "$controls_zip"' \
  'preventive-control sealing must hash raw-control ZIP bytes against provider digest'
require_text "$seal_preventive_controls" 'test "sha256:$(sha256sum "$draft_zip"' \
  'preventive-control sealing must hash draft-verification ZIP bytes against provider digest'
require_text "$seal_preventive_controls" 'test "$(unzip -Z1 "$controls_zip" | LC_ALL=C sort)" = "$expected_source_files"' \
  'raw-control ZIP must have an exact root-only entry allowlist before extraction'
require_text "$seal_preventive_controls" 'test "$(unzip -Z1 "$draft_zip" | LC_ALL=C sort)" = "$expected_draft_files"' \
  'draft-verification ZIP must have an exact root-only entry allowlist before extraction'
require_text "$seal_preventive_controls" 'test "$(jq -r .source.asset_inventory_digest "$draft_record")"' \
  'sealer must bind the embedded draft record to exact inventory bytes'
require_text "$seal_preventive_controls" '.inventory == $inventory[0]' \
  'sealer must require exact embedded and downloaded draft inventories'
require_text "$seal_preventive_controls" 'seal_remote_sha="$(gh api' \
  'sealer must refresh the tag target immediately before record capture'
require_text "$seal_preventive_controls" 'test "$seal_remote_sha" = "$GITHUB_SHA"' \
  'sealer must reject a concurrently moved tag'
require_text "$seal_preventive_controls" 'test "$(jq -r .isDraft <<<"$seal_release_json")" = true' \
  'sealer must require the candidate release to remain draft'
require_text "$seal_preventive_controls" 'test "$seal_fingerprint" = "$VERIFIED_ASSET_FINGERPRINT"' \
  'sealer must freshly reverify the complete draft asset identity'
require_text "$seal_preventive_controls" 'valid_durable_ref "$TAG_RULESET_ADMIN_REVIEW_REF"' \
  'sealer must enforce the same durable review-reference semantics as the Go validator'
for durable_guard in \
  'test "${#value}" -le 2048' \
  '"$authority" != *"@"*' \
  '"$path" != "/"' \
  '"$escaped" =~ ^[0-9A-Fa-f]{2}' \
  '"$host" == "github.com"' \
  '"$host" == "api.github.com"' \
  'pipelines.actions.githubusercontent.com' \
  'objects.githubusercontent.com'; do
  require_text "$seal_preventive_controls" "$durable_guard" \
    "sealer durable-ref validation lacks guard: $durable_guard"
done
require_text "$seal_preventive_controls" '^[A-Za-z0-9][A-Za-z0-9._@+:-]{0,127}$' \
  'sealer reviewer validation must exactly match the typed model'
require_text "$seal_preventive_controls" 'pgworkbench.release-preventive-controls-verification/v1' \
  'sealer must emit the versioned preventive-control contract'
require_text "$seal_preventive_controls" 'pgworkbench.release-preventive-controls-verification' \
  'sealer must emit the exact preventive-control artifact type'
require_text "$seal_preventive_controls" 'job:"seal-preventive-controls"' \
  'sealed workflow identity must name its fixed producer job'
for typed_source in \
  'controls_artifact:{id:$controls_artifact_id,name:$controls_artifact_name,digest:$controls_artifact_digest}' \
  'repository_controls_digest:$repository_controls_digest' \
  'tag_ruleset_api_digest:$tag_ruleset_api_digest' \
  'immutable_releases_api_digest:$immutable_releases_api_digest'; do
  require_text "$seal_preventive_controls" "$typed_source" \
    "sealed preventive-control record lacks typed source field: $typed_source"
done
for bounded_fact in \
  'purpose:"prepublication-preventive-controls-observation"' \
  'verification_scope:"workflow-local-github-api-and-source-binding"' \
  'actions_artifact_durable:false' \
  'candidate_identity_reverified:true' \
  'bypass_review_remote_object_fetched:false' \
  'bypass_review_signature_verified:false' \
  'performance_claim:false' \
  'benchmark_comparability_claim:false' \
  'recovery_claim:false' \
  'production_decision_eligible:false'; do
  require_text "$seal_preventive_controls" "$bounded_fact" \
    "sealed preventive-control record lacks bounded fact: $bounded_fact"
done
require_text "$seal_preventive_controls" '(.captured_at | fromdateiso8601) >= (.tag_ruleset.updated_at | fromdateiso8601)' \
  'sealed observation time must not precede the observed ruleset revision'
require_text "$seal_preventive_controls" '(.captured_at | fromdateiso8601) >= (.bypass_review.reviewed_at | fromdateiso8601)' \
  'sealed observation time must not precede the bound bypass review'
require_line "$seal_preventive_controls" '      artifact_digest: ${{ steps.upload_verification.outputs.artifact-digest }}' \
  'sealer must export the sealed artifact digest'
require_line "$seal_preventive_controls" '      artifact_id: ${{ steps.upload_verification.outputs.artifact-id }}' \
  'sealer must export the immutable sealed artifact id'
require_line "$seal_preventive_controls" '      artifact_name: ${{ steps.seal.outputs.artifact_name }}' \
  'sealer must export the exact sealed artifact name'
require_line "$seal_preventive_controls" '      record_digest: ${{ steps.seal.outputs.record_digest }}' \
  'sealer must export the exact typed record digest'
require_line "$seal_preventive_controls" '          name: preventive-controls-verification-${{ github.ref_name }}-${{ github.sha }}-${{ github.run_attempt }}' \
  'sealed artifact upload name must be exact and rerun-safe'
require_line "$seal_preventive_controls" '          path: preventive-controls-verification/verification.json' \
  'sealed artifact must contain only the typed verification record'
for forbidden in 'actions/download-artifact@' 'actions/checkout@' 'make ' 'go run' 'go test' \
  './scripts/' '"$verifier"' ' benchmark drivers' 'gh release edit' 'gh release create'; do
  if grep -Fq -- "$forbidden" <<<"$seal_preventive_controls"; then
    echo "FAIL: protected sealer executes candidate code or mutates the release: $forbidden" >&2
    exit 1
  fi
done

require_line "$publish_release" "    if: github.ref_type == 'tag'" 'publication must be tag-only'
require_line "$publish_release" '      - attest-and-create-draft' \
  'publication must depend on protected draft creation'
require_line "$publish_release" '      - draft-verify' 'publication must depend on clean draft verification'
require_line "$publish_release" '      - draft-compatibility' 'publication must depend on draft compatibility'
require_line "$publish_release" '      - draft-external-drivers' 'publication must depend on real external-driver evidence'
require_line "$publish_release" '      - verify-publication-evidence' \
  'publication must wait for read-only semantic evidence verification'
require_line "$publish_release" '      - seal-preventive-controls' \
  'publication must wait for the typed preventive-control seal'
require_line "$publish_release" '    environment: release-publication' \
  'every repository mutation must use the protected publication environment'
for sealed_output in \
  'VERIFIED_PREVENTIVE_CONTROLS_ARTIFACT_DIGEST: ${{ needs['"'"'seal-preventive-controls'"'"'].outputs.artifact_digest }}' \
  'VERIFIED_PREVENTIVE_CONTROLS_ARTIFACT_ID: ${{ needs['"'"'seal-preventive-controls'"'"'].outputs.artifact_id }}' \
  'VERIFIED_PREVENTIVE_CONTROLS_ARTIFACT_NAME: ${{ needs['"'"'seal-preventive-controls'"'"'].outputs.artifact_name }}' \
  'VERIFIED_PREVENTIVE_CONTROLS_RECORD_DIGEST: ${{ needs['"'"'seal-preventive-controls'"'"'].outputs.record_digest }}'; do
  require_text "$publish_release" "$sealed_output" \
    "publication lacks exact sealed preventive-control output: $sealed_output"
done
require_text "$publish_release" 'test "$prepublish_fingerprint" = "$VERIFIED_ASSET_FINGERPRINT"' \
  'publication must compare the current draft with the verified asset inventory'
require_text "$publish_release" 'test "$static_gate_digest" = "$VERIFIED_EXTERNAL_DRIVER_GATE_DIGEST"' \
  'publication must consume the exact verified external-driver gate artifact'
require_text "$publish_release" 'test "$static_controls_digest" = "$VERIFIED_REPOSITORY_CONTROLS_DIGEST"' \
  'publication must rehash the preventive-control record bound by the external gate'
require_text "$publish_release" '"$VERIFIED_EXTERNAL_DRIVER_ARCHIVE_DIGEST"' \
  'publication must consume the exact mode-preserving external-driver evidence archive'
require_line "$publish_release" '          artifact-ids: ${{ env.VERIFIED_EXTERNAL_DRIVER_ARTIFACT_ID }}' \
  'publication must download the exact semantically verified producer id'
require_line "$publish_release" '          artifact-ids: ${{ env.VERIFIED_CONTROLS_ARTIFACT_ID }}' \
  'publication must download the exact protected-control producer id'
require_text "$publish_release" 'test "$PRODUCER_EXTERNAL_DRIVER_ARTIFACT_ID" = "$VERIFIED_EXTERNAL_DRIVER_ARTIFACT_ID"' \
  'publication must match the semantic-verifier output to the direct producer id'
require_text "$publish_release" '.digest == $digest and .workflow_run.id == $run_id' \
  'publication must bind REST artifact digest and workflow run before mutation'
require_text "$publish_release" '.workflow_run.head_sha == $head_sha' \
  'publication must bind both artifacts to the candidate commit before mutation'
require_text "$publish_release" 'actions/artifacts/$VERIFIED_PREVENTIVE_CONTROLS_ARTIFACT_ID/zip' \
  'publication must download exact sealed ZIP bytes through the authenticated API'
require_text "$publish_release" 'test "sha256:$(sha256sum "$preventive_zip"' \
  'publication must bind exact sealed ZIP bytes to the provider digest'
require_text "$publish_release" 'test "$(unzip -Z1 "$preventive_zip")" = "verification.json"' \
  'publication must require the exact one-file sealed artifact before extraction'
require_text "$publish_release" 'test "$(stat -c %s "$preventive_record")" -le 65536' \
  'publication must bound the typed preventive-control record'
require_text "$publish_release" 'test "sha256:$(sha256sum "$preventive_record"' \
  'publication must bind verification.json bytes to the exported record digest'
require_text "$publish_release" '.schema_version == "pgworkbench.release-preventive-controls-verification/v1"' \
  'publication must require the exact preventive-control schema'
require_text "$publish_release" '.artifact_type == "pgworkbench.release-preventive-controls-verification"' \
  'publication must require the exact preventive-control artifact type'
require_text "$publish_release" 'job:"seal-preventive-controls"' \
  'publication must bind the typed record to the sealer job identity'
require_text "$publish_release" '.source.controls_artifact == {id:$controls_id,name:$controls_name,digest:$controls_digest}' \
  'publication must bind the typed source to the exact raw-control artifact'
require_text "$publish_release" '.source.repository_controls_digest == $repository_controls_digest' \
  'publication must bind the typed source to exact repository-control bytes'
require_text "$publish_release" '.draft_asset_verification.workflow_run == {id:$run_id,attempt:$run_attempt' \
  'publication must bind the embedded draft record to the same run and attempt'
require_text "$publish_release" '.draft_asset_verification.provider_observation == {tag:$tag,tag_target_sha:$sha,release_state:"draft"' \
  'publication must verify the embedded draft provider observation'
require_text "$publish_release" '[.draft_asset_verification.inventory.assets[].name] == expected_names($version)' \
  'publication must verify the fixed closed 16-asset inventory'
require_text "$publish_release" '.draft_asset_verification.checks == {tag_target:"verified"' \
  'publication must verify the complete fixed draft check matrix'
require_text "$publish_release" '.draft_asset_verification.assurance == {purpose:"release-asset-authenticity-and-integrity"' \
  'publication must verify the complete bounded draft assurance'
require_text "$publish_release" '.assurance == {purpose:"prepublication-preventive-controls-observation"' \
  'publication must verify the complete bounded preventive-control assurance'
require_text "$publish_release" '.bypass_review == {reviewer:$reviewer,reviewed_at:$reviewed_at' \
  'publication must bind the exact admin review and ruleset revision'
require_text "$publish_release" '(.captured_at | fromdateiso8601) >= (.bypass_review.reviewed_at | fromdateiso8601)' \
  'publication must reject a control record captured before its review'
require_text "$publish_release" 'gh release edit "$tag" --repo "$GITHUB_REPOSITORY" --draft=false' \
  'only the publication job may publish the verified draft'
require_text "$publish_release" 'prepublish_remote_sha="$(gh api "repos/$GITHUB_REPOSITORY/commits/$tag" --jq .sha)"' \
  'publication must refresh the live tag target immediately before publishing'
require_text "$publish_release" 'test "$prepublish_remote_sha" = "$GITHUB_SHA"' \
  'publication must reject a tag moved during verification'
require_text "$publish_release" 'test "$prepublish_fingerprint" = "$VERIFIED_ASSET_FINGERPRINT"' \
  'publication must refresh the draft asset fingerprint immediately before publishing'
require_text "$publish_release" 'IMMUTABLE_RELEASES_ADMIN_TOKEN: ${{ secrets.PGWORKBENCH_ADMIN_READ_TOKEN }}' \
  'the final publication step must receive the dedicated Administration-read credential'
require_text "$publish_release" '"repos/$GITHUB_REPOSITORY/rulesets/$TAG_RULESET_ADMIN_REVIEWED_ID"' \
  'publication must re-query the exact reviewed ruleset immediately before transition'
require_text "$publish_release" '.updated_at == $updated_at' \
  'publication must reject a ruleset revision newer than its bypass review'
require_text "$publish_release" '([.rules[].type] | index("creation")) != null' \
  'publication must recheck restricted tag creation'
require_text "$publish_release" '"repos/$GITHUB_REPOSITORY/immutable-releases"' \
  'publication must re-query immutable-release state immediately before transition'
require_text "$publish_release" 'test "$live_ruleset_digest" = "$source_ruleset_digest"' \
  'publication must bind canonical live ruleset bytes to the sealed typed record'
require_text "$publish_release" 'test "$live_immutable_digest" = "$source_immutable_digest"' \
  'publication must bind canonical live immutable-release bytes to the sealed typed record'
require_text "$publish_release" 'test "$live_ruleset_normalized" = "$(jq -c .tag_ruleset "$preventive_record")"' \
  'publication must require exact normalized live ruleset facts from the sealed record'
require_text "$publish_release" '"$(jq -c .immutable_releases "$preventive_record")"' \
  'publication must require exact normalized live immutable-release facts from the sealed record'
require_text "$publish_release" 'printf '\''%s'\'' "$live_ruleset"' \
  'publication canonical ruleset digest must not include a synthetic trailing newline'
require_text "$publish_release" 'printf '\''%s'\'' "$live_immutable"' \
  'publication canonical immutable-release digest must not include a synthetic trailing newline'
for forbidden in 'actions/checkout@' 'make ' 'go run' 'go test' './scripts/' \
  'tar -xzf' 'tar -xOf' 'tar -xvf' '"$verifier"' ' benchmark drivers' \
  'driver-run-verify' 'pgworkbench version'; do
  if grep -Fq -- "$forbidden" <<<"$publish_release"; then
    echo "FAIL: privileged publication job executes candidate/repository code: $forbidden" >&2
    exit 1
  fi
done
if [[ "$(grep -v '^[[:space:]]*$' <<<"$publish_release" | tail -n 1)" != \
      '          gh release edit "$tag" --repo "$GITHUB_REPOSITORY" --draft=false' ]]; then
  echo 'FAIL: draft-to-public transition must be the publication job final command' >&2
  exit 1
fi

for unprivileged_job in prepare source-compatibility aggregate-attempt-1 \
  aggregate-attempt-2 build-snapshot verify-candidate-artifact draft-verify \
  draft-compatibility seal-source-draft-and-aggregate-evidence \
  draft-external-drivers verify-publication-evidence public-verify \
  published-compatibility seal-published-compatibility; do
  unprivileged_block="$(job_block "$unprivileged_job")"
  if grep -Eq 'PGWORKBENCH_ADMIN_READ_TOKEN|IMMUTABLE_RELEASES_ADMIN_TOKEN|PGWORKBENCH_TAG_RULESET_ADMIN_REVIEW|TAG_RULESET_ADMIN_REVIEW' \
    <<<"$unprivileged_block"; then
    echo "FAIL: unprivileged/candidate job receives release-control credential: $unprivileged_job" >&2
    exit 1
  fi
done

require_line "$public_verify" "    if: github.ref_type == 'tag'" 'public verification must be tag-only'
require_line "$public_verify" '      - publish-release' 'public verification must run only after publication'
require_line "$public_verify" '      - draft-verify' 'public verification must consume the verified draft fingerprint'
require_text "$public_verify" 'test "$(jq -r .isDraft <<<"$release_json")" = false' \
  'public verification must require a published release'
require_text "$public_verify" 'test "$(jq -r .isImmutable <<<"$release_json")" = true' \
  'public verification must require the published release to be immutable'
require_text "$public_verify" 'gh release verify "$tag" --repo "$GITHUB_REPOSITORY" --format json' \
  'public verification must verify the immutable release attestation'
require_text "$public_verify" 'test -s "$release_attestation_path"' \
  'public typed summary must reject an empty release-attestation observation'
require_text "$public_verify" '.assets | length == 16' \
  'public verification must require the complete fixed asset set'
require_text "$public_verify" 'test "$asset_fingerprint" = "$VERIFIED_ASSET_FINGERPRINT"' \
  'public verification must bind the public asset set to the verified draft'
require_text "$public_verify" 'pgworkbench.release-asset-inventory/v1' \
  'public verification must retain a typed asset inventory'
require_text "$public_verify" 'public-verification/asset-inventory.json' \
  'public typed asset inventory must remain outside the fingerprinted release assets'
require_text "$public_verify" 'gh release download "$tag"' \
  'public verification must download the public release into its clean job'
require_text "$public_verify" 'gh attestation verify "$subject"' \
  'public verification must authenticate every provenance subject'
require_text "$public_verify" 'sha256sum -c "pgworkbench-$VERSION-SHA256SUMS.txt"' \
  'public verification must verify archive checksums'
require_text "$public_verify" '"$verifier" release manifest verify' \
  'public verification must independently verify the release manifest'
require_text "$public_verify" '--package-root "$package_root" "$sbom"' \
  'public verification must independently verify every SPDX document'
require_text "$public_verify" 'pgworkbench.release-asset-verification/v1' \
  'public verifier must emit the typed published-asset fact record'
require_text "$public_verify" 'pgworkbench.release-publication-verification/v1' \
  'public verifier must emit the typed post-publication fact record'
require_text "$public_verify" '--arg job public-verify' \
  'public asset record must identify the read-only verifier job'
require_text "$public_verify" 'inventory: $inventory[0]' \
  'public asset record must embed the complete provider inventory'
require_text "$public_verify" 'public_asset_verification: $asset_verification[0]' \
  'publication record must embed the complete published-asset verification'
require_text "$public_verify" 'post_publication_observation: true' \
  'publication record must be explicitly post-publication'
require_text "$public_verify" 'mutation_performed_by_verifier: false' \
  'read-only public verifier must not claim to have performed publication'
require_text "$public_verify" 'draft_public_fingerprint_equal: true' \
  'publication record must retain the verified draft/public fingerprint equality'
require_text "$public_verify" '> public-verification/asset-verification.json' \
  'public asset fact record must remain in the existing verification artifact'
require_text "$public_verify" '> public-verification/publication-verification.json' \
  'publication fact record must remain in the existing verification artifact'
for bounded_fact in \
  'actions_artifact_durable: false' \
  'candidate_identity_reverified: true' \
  'performance_claim: false' \
  'benchmark_comparability_claim: false' \
  'recovery_claim: false' \
  'production_decision_eligible: false'; do
  require_text "$public_verify" "$bounded_fact" \
    "public release record lacks bounded fact: $bounded_fact"
done
if grep -Eq '^[[:space:]]+(status|passed):' <<<"$public_verify"; then
  echo 'FAIL: public release producers may not emit caller-selected gate outcomes' >&2
  exit 1
fi
public_attestation_line="$(grep -Fn -- 'gh release verify "$tag"' <<<"$public_verify" | head -n 1 | cut -d: -f1)"
public_asset_summary_line="$(grep -Fn -- 'pgworkbench.release-asset-verification/v1' <<<"$public_verify" | tail -n 1 | cut -d: -f1)"
publication_summary_line="$(grep -Fn -- 'pgworkbench.release-publication-verification/v1' <<<"$public_verify" | tail -n 1 | cut -d: -f1)"
if [[ -z "$public_attestation_line" || -z "$public_asset_summary_line" || -z "$publication_summary_line" ]] || \
   (( public_asset_summary_line <= public_attestation_line || publication_summary_line <= public_asset_summary_line )); then
  echo 'FAIL: public asset and publication summaries must follow release verification in that order' >&2
  exit 1
fi
require_line "$public_verify" '          name: public-verification-${{ github.ref_name }}-${{ github.sha }}-${{ github.run_attempt }}' \
  'public verification evidence must be rerun-safe'
if grep -Fq -- 'actions/checkout@' <<<"$public_verify"; then
  echo 'FAIL: public verification must not depend on a source checkout' >&2
  exit 1
fi
for forbidden_record in \
  'pgworkbench.release-publication-verification/v1' \
  'publication-verification.json'; do
  if grep -Fq -- "$forbidden_record" <<<"$publish_release"; then
    echo "FAIL: mutating publication job may not produce read-only verification record: $forbidden_record" >&2
    exit 1
  fi
done

require_line "$published_compatibility" "    if: github.ref_type == 'tag'" 'published compatibility must be tag-only'
require_line "$published_compatibility" '      - public-verify' \
  'published compatibility must wait for clean public verification'
require_line "$published_compatibility" '      release_tag: ${{ github.ref_name }}' \
  'published compatibility must use the exact published tag'
require_line "$published_compatibility" '      qualification_mode: published' \
  'published compatibility must run all declared cells in published mode'

if [[ "$last_job" != seal-published-compatibility ]]; then
  echo 'FAIL: sealed published compatibility must be the final post-publication qualification job' >&2
  exit 1
fi
require_line "$seal_source_draft_aggregate" '      actions: read' \
  'source/draft/aggregate sealing must have artifact-read permission only'
require_line "$seal_source_draft_aggregate" '      contents: read' \
  'source/draft/aggregate sealing must keep repository contents read-only'
require_text "$seal_source_draft_aggregate" 'pgworkbench.release-compatibility-verification/v1' \
  'source/draft sealing must emit typed compatibility records'
require_text "$seal_source_draft_aggregate" 'pgworkbench.release-aggregate-verification/v1' \
  'source/draft sealing must emit typed aggregate records'
require_text "$seal_source_draft_aggregate" 'previous_attempt_record_digest' \
  'aggregate attempt two must bind the exact attempt-one record bytes'
require_line "$seal_published_compatibility" '      actions: read' \
  'published compatibility sealing must have artifact-read permission only'
require_text "$seal_published_compatibility" 'pgworkbench.release-compatibility-verification/v1' \
  'published compatibility sealing must emit its typed record'

if [[ "$(grep -Fc -- '--draft=false' "$WORKFLOW")" -ne 1 ]]; then
  echo 'FAIL: the workflow must have exactly one draft-to-public transition' >&2
  exit 1
fi

if grep -Fq -- '--draft=false' <<<"$build_snapshot$verify_candidate_artifact$attest_and_create_draft$draft_verify$draft_compatibility$draft_external_drivers$public_verify$published_compatibility"; then
  echo 'FAIL: a job other than publish-release can publish the release' >&2
  exit 1
fi

if ! grep -Fq -- "QUALIFICATION_MODE: \${{ inputs.release_tag == '' && 'source' || inputs.qualification_mode }}" "$COMPATIBILITY_WORKFLOW"; then
  echo 'FAIL: compatibility evidence mode must distinguish source, draft, and published candidates' >&2
  exit 1
fi

if grep -Eq '^      RUN_ID: .*env\.QUALIFICATION_MODE' "$COMPATIBILITY_WORKFLOW"; then
  echo 'FAIL: job-level env cannot reference another key in the env context' >&2
  exit 1
fi

if [[ "$(grep -Fc -- "RUN_ID: qualification-" "$COMPATIBILITY_WORKFLOW")" -ne 4 ]] ||
   [[ "$(grep -Fc -- "inputs.release_tag == '' && 'source' || inputs.qualification_mode" "$COMPATIBILITY_WORKFLOW")" -ne 5 ]]; then
  echo 'FAIL: every compatibility execution must encode the qualification mode in its run id' >&2
  exit 1
fi

if ! grep -Fq -- '[[ "$RELEASE_TAG_INPUT" =~ ^v[0-9]+\.[0-9]+\.[0-9]+' "$COMPATIBILITY_WORKFLOW"; then
  echo 'FAIL: release-backed qualification modes must require a strict SemVer release tag' >&2
  exit 1
fi

if [[ "$(grep -Fc -- 'test "$(jq -r .isDraft <<<"$release_json")" = "$expected_is_draft"' "$COMPATIBILITY_WORKFLOW")" -ne 5 ]]; then
  echo 'FAIL: every release-backed compatibility job must verify draft/published release state' >&2
  exit 1
fi

if [[ "$(grep -Fc -- 'draft) expected_is_draft=true ;;' "$COMPATIBILITY_WORKFLOW")" -ne 5 ]] ||
   [[ "$(grep -Fc -- 'published) expected_is_draft=false ;;' "$COMPATIBILITY_WORKFLOW")" -ne 5 ]]; then
  echo 'FAIL: release-backed compatibility must map draft and published modes fail-closed' >&2
  exit 1
fi

if ! grep -Fxq -- '    runs-on: macos-15' "$COMPATIBILITY_WORKFLOW"; then
  echo 'FAIL: Darwin arm64 compatibility must use the current macos-15 runner' >&2
  exit 1
fi
if grep -Fq -- 'runs-on: macos-14' "$COMPATIBILITY_WORKFLOW"; then
  echo 'FAIL: deprecated macos-14 must not gate Darwin arm64 compatibility' >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo 'FAIL: compatibility workflow graph test requires jq' >&2
  exit 1
fi

declared_tuples="$(
  jq -r '
    .cells[] |
    select(.support_level == "candidate") |
    [.id, .runtime, .topology, .os, .arch, .postgres, .gate] |
    join("|")
  ' "$COMPATIBILITY_LEDGER" |
    LC_ALL=C sort
)"
workflow_tuples="$(
  sed -En \
    -e "s/^[[:space:]]+tuple: '([^']+)'$/\\1/p" \
    -e "s/^[[:space:]]+QUALIFICATION_TUPLE: '([^']+)'$/\\1/p" \
    "$COMPATIBILITY_WORKFLOW" |
    LC_ALL=C sort
)"
duplicate_workflow_tuples="$(printf '%s\n' "$workflow_tuples" | uniq -d)"
if [[ -n "$duplicate_workflow_tuples" ]]; then
  echo "FAIL: compatibility workflow maps a support tuple more than once: $duplicate_workflow_tuples" >&2
  exit 1
fi
if [[ "$workflow_tuples" != "$declared_tuples" ]]; then
  echo 'FAIL: compatibility workflow tuples differ from the candidate support ledger' >&2
  diff -u <(printf '%s\n' "$declared_tuples") <(printf '%s\n' "$workflow_tuples") >&2 || true
  exit 1
fi
if [[ "$(grep -Fc -- 'test "$QUALIFICATION_TUPLE" =' "$COMPATIBILITY_WORKFLOW")" -ne 4 ]] ||
   [[ "$(grep -Fc -- "printf 'tuple=%s" "$COMPATIBILITY_WORKFLOW")" -ne 4 ]]; then
  echo 'FAIL: every compatibility execution must assert and record its complete support tuple' >&2
  exit 1
fi

while IFS= read -r upload_name; do
  if [[ "$upload_name" != *'github.run_attempt'* ]] &&
     [[ "$upload_name" != *'steps.package.outputs.artifact_name'* ]] &&
     [[ "$upload_name" != *'steps.inventory.outputs.artifact_name'* ]] &&
     [[ "$upload_name" != *'steps.controls.outputs.artifact_name'* ]] &&
     [[ "$upload_name" != *'steps.verification_summary.outputs.artifact_name'* ]]; then
    echo "FAIL: workflow artifact upload name is not rerun-safe: $upload_name" >&2
    exit 1
  fi
done < <(
  awk '
    /uses: actions\/upload-artifact@/ { upload = 1; next }
    upload && /^[[:space:]]+name:/ { sub(/^[[:space:]]+name:[[:space:]]*/, ""); print; upload = 0 }
  ' "$REPO_DIR"/.github/workflows/*.yml
)

download_consumer_count="$(
  awk '/uses: actions\/download-artifact@/ { count++ } END { print count + 0 }' \
    "$REPO_DIR"/.github/workflows/*.yml
)"
if [[ "$download_consumer_count" -ne 6 ]]; then
  echo 'FAIL: every added workflow artifact consumer needs an explicit producer-name audit' >&2
  exit 1
fi
if [[ "$(grep -Fhc -- '          artifact-ids: ${{ env.CANDIDATE_ARTIFACT_ID }}' "$WORKFLOW")" -ne 2 ]]; then
  echo 'FAIL: both candidate consumers must download the exact immutable producer artifact id' >&2
  exit 1
fi
if [[ "$(grep -Fhc -- '          artifact-ids: ${{ env.VERIFIED_CONTROLS_ARTIFACT_ID }}' "$WORKFLOW")" -ne 2 ]]; then
  echo 'FAIL: both protected-control consumers must download the exact immutable artifact id' >&2
  exit 1
fi
if [[ "$(grep -Fhc -- '          artifact-ids: ${{ env.VERIFIED_EXTERNAL_DRIVER_ARTIFACT_ID }}' "$WORKFLOW")" -ne 2 ]]; then
  echo 'FAIL: both external-evidence consumers must download the exact immutable artifact id' >&2
  exit 1
fi

echo 'PASS: release stays draft through pre-publication gates, then public assets and all cells are requalified'
