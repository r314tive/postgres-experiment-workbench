# Release

Release artifacts are built from the Go CLI and written under ignored
`generated/release/`. Release notes live in [../CHANGELOG.md](../CHANGELOG.md),
and the active milestone is described in [roadmap.md](roadmap.md).

## v0.2 candidate

Before creating the candidate commit, run the source/runtime checks:

```bash
make check
make native-test
make test

MATRIX_PROFILE_SIZES=medium MATRIX_REPEATS=3 \
  make matrix-run MATRIX_SPEC=massive-dml-strategy
```

After committing those exact bytes, run the immutable-candidate gates from a
clean checkout. `candidate-preflight` and therefore `release-check` deliberately
reject a dirty worktree. They also require the exact stable Go patch release
recorded in `.go-version`; the same pin is used by every GitHub build job:

```bash
make candidate-preflight VERSION=0.2.0
make release-check VERSION=0.2.0
```

Verify every matrix row and release archive:

```bash
make experiment-summary SUMMARY_INPUT=runs/matrices/<matrix-run-id>
make scan-artifacts
make privacy-scan
cd generated/release && shasum -a 256 -c pgworkbench-0.2.0-SHA256SUMS.txt
```

Each archive contains the binary and complete scenario pack. `release-smoke`
extracts the current-platform archive outside the checkout and runs version,
pack validation, experiment planning, a real isolated-native experiment, a
real native pgbench smoke series, and relocated bundle verification before
accepting it.

Archive availability and runtime support are separate contracts. The
machine-readable [`compatibility/matrix.json`](../compatibility/matrix.json)
classifies Linux/amd64 and Darwin/arm64 as `runtime-gated`, requiring downloaded
archives to execute exact compatibility cells on those platforms before
release. Darwin/amd64 and Linux/arm64 are `compile-package-only`: they require
cross-compilation, deterministic packaging, checksum, SBOM, provenance, and
clean-download gates but no runtime cell. The static ledger records these
requirements, not their outcome. Do not advertise the latter two archives as
runtime-supported until real execution cells are added and pass.

## Candidate to release

1. Commit and push the candidate changes.
2. Require the GitHub `check` and source-mode `compatibility` workflows to pass
   on that exact commit.
3. Add a dated `v0.2.0` changelog heading and tag the exact green commit.
4. Push the tag and require the GitHub `release-snapshot` workflow to pass.
5. Require both sequential clean-checkout aggregate attempts to pass before
   `build-snapshot` creates its exact ten-file unsigned artifact. Require the
   separate read-only verifier to bind its ID, digest, producer run/commit, and
   fingerprint and to validate manifest/SBOM semantics from the downloaded
   Linux binary.
6. Approve the tag-only `attest-and-create-draft` deployment in the protected
   `release-publication` environment. It must statically reverify the exact
   artifact without checking out or executing candidate code, verify tag
   creation/update/deletion and immutable-release controls, sign the bytes, and
   create the draft as its final mutation. Verify that the draft GitHub Release
   contains every archive, the release
   manifest, both checksum files, four SPDX SBOMs, one provenance bundle, and
   four SBOM-attestation bundles.
7. Require the clean draft-download verification and all declared
   `runtime-gated` release-artifact compatibility cells to pass. The
   `compile-package-only` Darwin/amd64 and Linux/arm64 archives remain outside
   runtime-support claims.
8. Require `draft-external-drivers` to execute the pinned BenchBase, HammerDB,
   and sysbench adapters from the downloaded draft binary on the protected
   GitHub-hosted `ubuntu-24.04` release-smoke job. It locally verifies all three
   contract-v2 closed runtime-closure artifacts, uploads only sanitized bound
   metadata, and deletes all third-party/runtime/database bytes.
   Only the final `publish-release` job may change the verified draft to a
   public release.
9. Require the clean `public-verify` job to observe an immutable public release,
   verify its release attestation, download/authenticate all 16 assets, and
   match their fingerprint to the verified draft.
10. Require all seven compatibility cells to pass again from those published
    archives. A public release with a failed post-publication gate exists but is
    `NO-GO` for v1/adoption claims.
    For a transient post-publication failure, use
    `gh run rerun <run-id> --failed` by default. A targeted
    `gh run rerun <run-id> --job <job-database-id>` is also safe only when the
    selected job is `public-verify` or one of the published-archive
    compatibility jobs; GitHub reruns that job and its dependants, not its
    publication ancestors. Never target `publish-release`, any draft/source
    gate, or rerun the complete workflow after publication: those paths reach
    intentional existing-release/draft-state guards.
11. Copy all outputs into the durable release-specific index described in
    [release-evidence.md](release-evidence.md); Actions retention is not the
    evidence archive.
12. Run the remaining adoption gates from
   [v1-completion-contract.md](v1-completion-contract.md) before a v1 claim.

Do not tag from an uncommitted worktree, and do not archive the standalone lab
before the pinned workbench release is reachable.

## Release supply-chain artifacts

The read-only release builder creates one deterministic SPDX 2.3 SBOM for each of the
four archives, then creates a deterministic release manifest that binds the
archive and SBOM names, sizes, SHA-256 digests, exact Git commit, exact embedded
Go toolchain, and scenario-pack identity. All four archive binaries must report
the same exact stable Go patch release. It exports exactly those four archives, four SBOMs, one archive
checksum file, and one manifest as an unsigned, attempt-qualified Actions
artifact. A separate read-only job downloads and semantically verifies that
exact artifact ID. Only then does the protected tag-only publisher download the
same ID and use `actions/attest` to create signed Sigstore-backed provenance for
the archives, archive checksum file, and release manifest, plus an SBOM
attestation for each archive. The generated Sigstore bundles are retained as
ordinary release assets as well as in GitHub's attestation service.

Each SBOM has one file-analyzed root package covering every archive file and a
package verification code derived only from that root file inventory. The
checked-in `third_party/go-modules.json` must exactly match `go.mod`, `go.sum`,
and retained upstream license bytes before SBOM creation. Its current modules
are source-pack schema-gate dependencies represented as dependency-package
`TEST_DEPENDENCY_OF` relationships with SPDX licenses and Go package URLs;
they are not described as runtime-linked. Runtime module relationships are
derived separately and fail closed against the embedded Go build information
in the packaged `pgworkbench` binary. The v0.2.0 runtime set is empty.

Standalone SBOM verification must be bound to the exact extracted package root;
document-only validation is intentionally not exposed as a successful release
gate:

```sh
pgworkbench release sbom verify \
  --package-root extracted/pgworkbench-0.2.0-linux-amd64 \
  pgworkbench-0.2.0-linux-amd64.spdx.json
```

The release asset set is intentionally fixed at 16 files:

- four `.tar.gz` archives;
- one archive `SHA256SUMS` and one metadata `SHA256SUMS` file;
- one `release-manifest.json` binding the archives and SBOMs;
- four `.spdx.json` SBOMs;
- one provenance and four SBOM `.sigstore.json` bundles.

The metadata checksum file covers the release manifest, all four SPDX
documents, and all five downloaded Sigstore bundles.

The tag workflow refuses to replace an existing GitHub Release. The job that
builds and executes candidate code has no repository-write, attestation, or
OIDC permission and does not persist checkout credentials. The protected
`attest-and-create-draft` job has those publication capabilities but no checkout
and never extracts or executes candidate bytes; it uses only static inventory,
hash, JSON, and tar-listing checks before signing and creating the release. It first creates
a fixed 16-asset draft, then verifies a clean download, runs every
release-artifact compatibility cell, and executes every advertised pinned
external-driver adapter while the release is still non-public. Only the final
job, after all three gates succeed, flips that same draft to public. This
final job compares the draft asset IDs, names, sizes, and GitHub-computed SHA-256
digests with the verified inventory immediately before publication. GitHub
drafts are mutable, so this check and the refusal to use `--clobber` are part of
the gate. A clean public job then requires `isImmutable=true`, verifies the
release attestation, and confirms the same 16-asset fingerprint. A failed draft
requires an explicit maintainer decision; a failed public gate leaves an
existing release but no v1/adoption `GO`.

GitHub artifact attestations are available on all current plans for public
repositories, but private/internal repositories outside GitHub Enterprise Cloud
cannot use them. If the repository does not have attestation access, publication
remains `NO-GO`; do not weaken or skip the signed-metadata gate. See GitHub's
[artifact-attestation documentation](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations).

The tag workflow verifies the draft assets in a new clean job with:

```bash
sha256sum -c pgworkbench-<version>-SHA256SUMS.txt
sha256sum -c pgworkbench-<version>-METADATA-SHA256SUMS.txt
pgworkbench release manifest verify --release-dir . \
  --manifest pgworkbench-<version>-release-manifest.json
gh attestation verify pgworkbench-<version>-linux-amd64.tar.gz -R OWNER/REPO
gh attestation verify pgworkbench-<version>-linux-amd64.tar.gz -R OWNER/REPO \
  --predicate-type https://spdx.dev/Document/v2.3
```

It verifies every SPDX document with the downloaded Linux/amd64 binary, repeats
the SBOM-attestation checks for every platform, verifies provenance for all four
archives, the archive checksum file, and the release manifest, and subsequently
runs the exact compatibility workflow from downloaded release archives. The
release remains a draft throughout those gates and becomes public only after
they succeed.

Verification binds each attestation to the tag commit and ref, and checks both
GitHub's attestation service and the downloaded Sigstore bundle files. The
provenance and SBOM-attestation checks are the trust bootstrap: they run before
any archive is extracted or any downloaded binary is executed. Checksums and
the manifest then verify completeness and semantic archive/SBOM consistency.

### Protected publication gates

`attest-and-create-draft` requires a protected `release-publication`
environment restricted to selected `v*` tags and an administrator reviewer.
That environment supplies the Administration-read credential and
ruleset-review variables listed below. A manual `workflow_dispatch` never
enters this environment and is build/verification only.

`draft-external-drivers` deliberately does not use the bounded fake producers
from local contract tests. It runs on GitHub's exact `ubuntu-24.04` hosted label
behind the protected `release-external-drivers` approval environment. The job
has only `actions`, `attestations`, and `contents` read permissions. It receives
no driver password, pre-provisioned runtime path, administration token, or
ruleset-review variable. Candidate provisioning and execution receive no
write/admin token. Configure this environment with a required reviewer,
self-review prevention, and the selected `v*` tag deployment policy; the YAML
reference alone does not configure those repository-side protections.
Candidate and upstream code run under a dedicated unprivileged UID and exact
credential-free environment, without GitHub command-file, artifact, cache,
write, or administration credentials.

The workflow installs PostgreSQL 16/build prerequisites, stops the distro
service, and rejects an existing server on numeric loopback. The downloaded
candidate's helper then acquires all six exact digest/size-pinned archives,
builds curated roots with 28 BenchBase, one HammerDB, and three sysbench files,
and creates an owned PostgreSQL 16 cluster with fresh loopback-only trust
authentication. Exact acquisition identities, archive safety checks, runtime
layouts, and claim limits are in
[external-driver-runner.md](external-driver-runner.md).

The protected `release-publication` environment is the preventive-control and
repository-mutation boundary. It must require administrator review and expose the dedicated
`PGWORKBENCH_ADMIN_READ_TOKEN` secret: a repository-scoped fine-grained PAT or
GitHub App token with only `Administration: read`. It is used solely for
`GET /repos/{owner}/{repo}/immutable-releases`; publication continues to use a
separate `github.token`. The environment also supplies the administrator-signed
bypass-review reference, digest, reviewer, review time, exact ruleset ID, and
ruleset `updated_at` through the
`PGWORKBENCH_TAG_RULESET_ADMIN_REVIEW_*`/`PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWED_*`
variables. Both draft creation and final publication deployments use that
environment. `release-external-drivers` consumes the
digest-bound control artifact and does not receive the administrative token,
review variables, driver secrets, or runtime variables. See
[release-evidence.md](release-evidence.md) for exact names, formats, and claim
limits.

The helper prepares all three datasets in that fresh cluster: BenchBase TPC-C
at scale factor 1, HammerDB PostgreSQL TPROC-C at 20 warehouses/4 build VUs,
and one 10000-row sysbench table. Release execution uses bounded configs:
BenchBase 1 terminal for 15 seconds, HammerDB 1-minute ramp plus 2-minute run
with 4 VUs and 1M iterations, and sysbench 4 threads for 60 seconds. The
100-warehouse/32-VU HammerDB config remains a manual descriptive study and is
not silently substituted into this release smoke. Preparation postconditions,
config digests, host identity, acquisition digests, and runtime inventories are
bound into the gate metadata. They do not establish benchmark fairness or
performance qualification.

The job authenticates and extracts the draft Linux archive, checks its embedded
version and full candidate commit, and uses only that downloaded `pgworkbench`
binary for the three executions and independent verifications. It also requires
the draft archive's advertised registry to contain exactly those three drivers,
so a newly advertised adapter cannot bypass the gate. `gate.json`
binds each locally verified execution summary as well as the acquisition,
runtime, dataset, host, external config, draft archive, candidate, tag, and
16-asset fingerprint digests. Full execution directories and every third-party
runtime byte stay in runner-temporary storage and are deleted on every outcome.
The Actions artifact is an exact nine-JSON metadata allowlist inside
`metadata-only.tar.gz`; failure upload is limited to `failure.json` and
contains no candidate-produced provisioning JSON. The separate read-only
`verify-publication-evidence` job rehashes those sanitized records, downloads
and authenticates the draft archive again, and checks its binary/registry
identity. It intentionally does not rerun the closed execution verifier after
the licensed runtime bytes have been destroyed. The final protected publisher
consumes those exact artifact IDs and digests, performs only static
hash/control/fingerprint checks, and changes the draft state as its final
command. Missing hosted capacity, approval, acquisition, dataset preparation,
real-driver execution, metadata, or cleanup therefore leaves publication
`NO-GO`. The same is true if the exact active
`refs/tags/v*` creation/update/deletion ruleset, current bypass review, or
immutable-release setting cannot be proven both during preflight and directly
before the final state transition.

This remains adapter-compatibility evidence. External binary provenance is
explicitly unattested, and the gate creates no benchmark-comparability,
source-to-binary, TPC-compliance, or performance claim. Its metadata records
the reviewed driver license expressions while leaving the complete dependency
license/source closure unattested. The metadata-only transport is a technical
compliance boundary, not legal advice, and does not make runtime replay
available or authorize project redistribution of those upstream bytes.

This section specifies a required publication gate; it does not assert that a
protected hosted real-driver execution has passed.
Only candidate-bound gate artifacts copied into the durable release evidence
index close that requirement.

## What `make release-check` covers

- `make doctor`
- `make check`
- `make native-test`
- `make quickstart`
- `make test`
- `make scan-artifacts`
- `make scan-artifacts-go`
- `make pgworkbench`
- `make privacy-scan`
- `make release-smoke`

`make check` also exercises the benchmark phase/preflight contracts, strict
parsers and import CLI, and the synthetic deterministic/tamper suites for
series, history, campaign, import, A/B, PostgreSQL sampler, and pgdrill
baseline provenance. These gates prove artifact mechanics; they do not replace
a qualified-host run or any publication/adoption gate.

When Docker daemon access is intentionally unavailable, use
`make doctor DOCTOR_FLAGS=--skip-docker-daemon` only for prerequisite triage. A
tag still waits for a full Docker-backed local gate or a green GitHub `check`.

## Versioning

Use `0.x` versions while public contracts are settling. Every version bump must
have a changelog entry, green spec-doc drift checks, a successful local release
snapshot, complete checksums and SBOMs, signed provenance, and green tag
workflows. Workflow code creates the gates; only a successful run for the exact
tag produces release evidence.
