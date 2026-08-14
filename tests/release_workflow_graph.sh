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

build_snapshot="$(job_block build-snapshot)"
verify_candidate_artifact="$(job_block verify-candidate-artifact)"
attest_and_create_draft="$(job_block attest-and-create-draft)"
source_compatibility="$(job_block source-compatibility)"
draft_verify="$(job_block draft-verify)"
draft_compatibility="$(job_block draft-compatibility)"
draft_external_drivers="$(job_block draft-external-drivers)"
verify_publication_evidence="$(job_block verify-publication-evidence)"
publish_release="$(job_block publish-release)"
public_verify="$(job_block public-verify)"
published_compatibility="$(job_block published-compatibility)"
last_job="$(awk '/^  [A-Za-z0-9_-]+:$/ { name = $1; sub(/:$/, "", name) } END { print name }' "$WORKFLOW")"
release_header="$(awk '/^jobs:$/ { exit } { print }' "$WORKFLOW")"
check_header="$(awk '/^jobs:$/ { exit } { print }' "$CHECK_WORKFLOW")"

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
require_text "$verify_publication_evidence" '"$verifier" benchmark drivers --json' \
  'read-only job must bind the candidate driver registry without needing deleted runtime bytes'
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

require_line "$publish_release" "    if: github.ref_type == 'tag'" 'publication must be tag-only'
require_line "$publish_release" '      - attest-and-create-draft' \
  'publication must depend on protected draft creation'
require_line "$publish_release" '      - draft-verify' 'publication must depend on clean draft verification'
require_line "$publish_release" '      - draft-compatibility' 'publication must depend on draft compatibility'
require_line "$publish_release" '      - draft-external-drivers' 'publication must depend on real external-driver evidence'
require_line "$publish_release" '      - verify-publication-evidence' \
  'publication must wait for read-only semantic evidence verification'
require_line "$publish_release" '    environment: release-publication' \
  'every repository mutation must use the protected publication environment'
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
require_text "$publish_release" 'test "$live_ruleset_digest" = "$(jq -r .tag_ruleset.api_evidence_digest' \
  'publication must bind the live ruleset bytes to the reviewed preventive-control record'
require_text "$publish_release" 'test "$live_immutable_digest" = "$(jq -r .immutable_releases.api_evidence_digest' \
  'publication must bind live immutable-release state to the preventive-control record'
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

require_line "$public_verify" "    if: github.ref_type == 'tag'" 'public verification must be tag-only'
require_line "$public_verify" '      - publish-release' 'public verification must run only after publication'
require_line "$public_verify" '      - draft-verify' 'public verification must consume the verified draft fingerprint'
require_text "$public_verify" 'test "$(jq -r .isDraft <<<"$release_json")" = false' \
  'public verification must require a published release'
require_text "$public_verify" 'test "$(jq -r .isImmutable <<<"$release_json")" = true' \
  'public verification must require the published release to be immutable'
require_text "$public_verify" 'gh release verify "$tag" --repo "$GITHUB_REPOSITORY" --format json' \
  'public verification must verify the immutable release attestation'
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
require_line "$public_verify" '          name: public-verification-${{ github.ref_name }}-${{ github.sha }}-${{ github.run_attempt }}' \
  'public verification evidence must be rerun-safe'
if grep -Fq -- 'actions/checkout@' <<<"$public_verify"; then
  echo 'FAIL: public verification must not depend on a source checkout' >&2
  exit 1
fi

require_line "$published_compatibility" "    if: github.ref_type == 'tag'" 'published compatibility must be tag-only'
require_line "$published_compatibility" '      - public-verify' \
  'published compatibility must wait for clean public verification'
require_line "$published_compatibility" '      release_tag: ${{ github.ref_name }}' \
  'published compatibility must use the exact published tag'
require_line "$published_compatibility" '      qualification_mode: published' \
  'published compatibility must run all declared cells in published mode'

if [[ "$last_job" != published-compatibility ]]; then
  echo 'FAIL: published compatibility must be the final post-publication qualification job' >&2
  exit 1
fi

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
     [[ "$upload_name" != *'steps.controls.outputs.artifact_name'* ]]; then
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
