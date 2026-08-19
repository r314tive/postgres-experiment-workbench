# CI

The default GitHub Actions workflow runs:

```bash
make check
make test
make scan-artifacts
```

Before Docker-backed runtime tests, CI assigns dynamic localhost ports for the
PostgreSQL, replica, logical subscriber, PgBouncer, and upgrade containers. This
keeps the workflow independent from whatever fixed ports are already occupied on
the runner.

`make check` is a no-Docker static/synthetic test set, including Go unit tests,
Go profile validation, Go env spec validation, Go run artifact verification,
Go env spec reference/schema rendering, Go experiment plan, expanded plan, and
JSON plan rendering, Go matrix JSON plan rendering, Go profile SQL plan
rendering, Go topology inspection, Go patchset validation, Go workload/dataset
validation, Go workload and dataset Markdown/JSON plan rendering, Go
source-check plan rendering, Go source-check artifact classification tests, Go
topology runtime parser tests, and Go failure scanning.
It also runs `make schema-check`: a network-independent Go gate that
metaschema-validates and compiles every repository schema as Draft 2020-12,
resolves all inter-schema `$ref` values from the local registry, asserts JSON
formats, evaluates patterns with an ECMA-compatible engine, and checks tracked
plus positive/negative representative artifacts. This replaces the former
syntax-only `jq` loop;
runtime artifact verifiers still enforce cross-file and byte-integrity rules
that JSON Schema cannot express.
`make test` is the full local runtime verification and uses Docker Compose.
`make release-check` is the local pre-release gate: it runs doctor checks,
static checks, quickstart, full runtime tests, artifact scans, privacy scan, and
the local `pgworkbench` build.

The native job is pinned to Ubuntu 24.04 and PostgreSQL 16. It uploads the
versioned native run artifacts even when the job fails. This is useful failure
evidence, but the candidate support ledger is qualified by the separate
`compatibility` workflow described below.

## Exact compatibility qualification

`.github/workflows/compatibility.yml` maps one CI job to every candidate cell in
`compatibility/matrix.json`:

- Docker on Linux/amd64 with PostgreSQL 16 for `single`, `primary-replica`,
  `logical-replication`, and `pgbouncer`;
- Docker on Linux/amd64 with an observed PostgreSQL 15 source and PostgreSQL 16
  target for `multi-version-upgrade`;
- native PostgreSQL 16 `single` on Linux/amd64 and Darwin/arm64.

The Docker jobs assert the runner and daemon architecture, the configured image
references, the container image IDs and registry digests, and the live
`server_version_num` of every PostgreSQL node. The native jobs install a
major-pinned PostgreSQL 16 package and assert both host architecture and the
observed run-manifest fingerprint. Every job uploads its identity, pack,
image/version, diagnostics, manifest, and verdict evidence.
Compatibility and aggregate evidence is retained as GitHub Actions artifacts for
90 days. Before a v1 decision, copy the exact green run's artifacts into the
release-specific durable evidence location required by the completion contract;
workflow-artifact retention alone is not permanent publication.
For tag releases, post-draft read-only sealing records bind those artifacts by
exact API ID/name/digest to the full candidate and fixed seven-cell set. These
records are input to the local evidence adapter, not a substitute for durable
evidence or an authorization decision.
The draft external-driver gate uses the same 90-day retention, but its artifact
is metadata-only. Any durable copy must preserve the exact metadata archive and
checksum; third-party driver, build, generated-script, database, log, and full
execution bytes are never copied into Actions or release evidence. This is a
technical compliance boundary, not legal advice; license metadata explicitly
leaves the complete dependency/source closure unattested.

After the independent `verify-publication-evidence` job has reverified that
metadata artifact and the authenticated draft archive/manifest, it emits
`pgworkbench.release-external-driver-verification/v1` as a separate fact-only
summary. The summary carries no `status` or `passed` field; the CLI adapter
derives `draft_external_drivers=passed` only from its exact candidate and fixed
assurance contract. Its Actions artifact is still 90-day transport and must be
copied byte-for-byte to the durable URI supplied during attachment.

The read-only draft verifier also emits
`pgworkbench.release-asset-verification/v1` in `draft` mode after authenticating
the tag target, complete 16-asset set, checksums, manifest, SBOM contents, and
attestations. The fresh public verifier emits the same record in `published`
mode after independently observing `isDraft=false`, `isImmutable=true`, and the
same draft/public fingerprint. It then emits the self-contained
`pgworkbench.release-publication-verification/v1` record, embedding that public
asset verification. The mutating publisher emits neither record. These files
live inside the existing draft/public verification artifacts; Actions remains
transport-only, and offline attachment classifies durable presence and producer
authenticity as operator-asserted/unverified.

`macos-15` is selected because it is the current arm64 GitHub-hosted image; the job still
fails closed unless `uname -m` is exactly `arm64`. Runner availability depends
on the repository and GitHub plan. If GitHub cannot allocate that label, the
Darwin cell remains unqualified rather than being silently skipped. See the
[GitHub-hosted runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

The workflow can qualify either a source candidate or a published tag:

```bash
gh workflow run compatibility.yml
release_version="${PGWORKBENCH_RELEASE_VERSION:?export PGWORKBENCH_RELEASE_VERSION}"
gh workflow run compatibility.yml -f release_tag="v${release_version}"
```

When a release tag is supplied, every job checks the live GitHub Release state:
`draft` mode accepts only a draft and `published` mode accepts only a public
release. A mislabeled or concurrently changed release fails before extraction
or execution.

Published mode starts without a checkout. Each job downloads the matching
platform archive and checksum file, verifies its GitHub provenance attestation,
extracts it, and executes the exact cell through the downloaded binary. A green
workflow run is evidence for that exact commit/archive only; the ledger remains
a declaration and is not rewritten into a historical pass claim.

Source mode also executes an immutable candidate binary, not `go run`: the
workflow embeds a non-development strict SemVer and the full `GITHUB_SHA`, then
requires Git `HEAD`, `GITHUB_SHA`, the binary identity, and every produced
`manifest.env` identity to agree. Aggregate release gates apply the same binding
to all native, Docker, quickstart, and utility runs before their evidence can be
accepted.

PostgreSQL source-tree checks are intentionally opt-in. Use the
`source-check` workflow manually, or run locally:

```bash
make source-plan SOURCE_WORKLOAD_SPEC=pg-source/check
PG_PATCHSET=chaos/master make source-plan SOURCE_WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan make workload-run WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan PG_PATCHSET=chaos/master make workload-run WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=run make workload-run WORKLOAD_SPEC=pg-source/check
```

The manual workflow defaults to `PG_SOURCE_ACTION=plan` so a heavy source build
is never part of the default push or pull-request path.

Native `pg_upgrade` is also opt-in. The workload defaults to a dry plan:

```bash
make workload-run WORKLOAD_SPEC=topology/native-pg-upgrade
```

Set `PG_UPGRADE_ACTION=check` or `run` only with a `PG_UPGRADE_IMAGE` that
contains the required old and new PostgreSQL binary directories.

## Release Snapshot Workflow

The `release-snapshot` workflow first calls the exact source compatibility
workflow, then runs two sequential `make release-check` attempts. Each attempt
uses a separate job and therefore a fresh checkout and runner. Only then does it
build the release archives. It runs on tags matching `v*` and can also be
started manually:

```bash
release_version="${PGWORKBENCH_RELEASE_VERSION:?export PGWORKBENCH_RELEASE_VERSION}"
gh workflow run release-snapshot.yml -f version="$release_version"
```

A manual dispatch stops after the read-only build and semantic artifact
verification path. It cannot request an attestation token, create a draft, or
reach any publication job; only an exact `v*` tag run can cross that boundary.

The aggregate jobs run:

```bash
make release-check VERSION=<version>
```

Each aggregate job first builds one CLI with the exact release SemVer and full
candidate commit embedded through linker flags. `BUILD_COMMIT` must equal Git
`HEAD` and, in GitHub Actions, `GITHUB_SHA`. Any candidate run manifest with a
development, unknown, shortened, or mismatching engine identity fails the gate.

`build-snapshot` validates the deterministic SPDX 2.3 SBOM generated for each
archive and the release manifest that binds those SBOMs to the archives, exact
Git commit, and scenario pack. Its checkout does not persist credentials and
the job has only `contents: read`; it exports exactly ten unsigned files under
an attempt-qualified artifact name, plus the artifact ID, platform digest, and
canonical ten-file fingerprint. A second read-only job downloads that exact ID,
checks the REST artifact name/digest/workflow-run/commit binding, and uses the
downloaded Linux binary to verify manifest and SBOM semantics.

Only the tag-only `attest-and-create-draft` job enters the protected
`release-publication` environment and receives repository write, attestation,
and OIDC permissions. It has no checkout, does not run repository scripts,
does not extract archives, and never executes the candidate binary. It
downloads the same immutable artifact ID, repeats static filename, digest,
manifest, SBOM, and tar-member checks with runner tools, verifies the preventive
repository controls, creates signed GitHub/Sigstore provenance and SBOM
attestations, and creates the fixed 16-asset draft as its final mutation. It
refuses to overwrite an existing release.

SBOM creation also verifies exact `go.mod`/`go.sum` coverage, retained license
digests, dependency package ordering, SPDX purls/licenses/relationship scopes,
and the binary's embedded Go build dependency closure. Current external Go
modules are test-only schema-gate dependencies; the release CLI binary has no
external Go modules, so the gate rejects any unlisted module that later becomes
runtime-linked.

While the release is still a draft, a clean job downloads all 16 release assets,
extracts every platform package into an isolated root, and uses the
Linux/amd64 binary to verify the release manifest and each SPDX document
against its matching package root. It also verifies the tag target, both
checksum sets, provenance for every bound subject, and every SBOM attestation.
The workflow then calls
`compatibility` again in `draft` mode, so the advertised cells run from the
downloaded draft archives rather than from the builder workspace. A protected
`draft-external-drivers` job then uses the downloaded Linux archive on GitHub's
`ubuntu-24.04` hosted runner. Under the protected `release-external-drivers`
approval environment, a candidate helper acquires exact digest/size-pinned
inputs, creates curated runtime roots and a fresh loopback PostgreSQL 16
cluster, prepares all three datasets, and runs one bounded BenchBase, HammerDB,
and sysbench compatibility smoke. Each complete v2 execution artifact is
verified locally and then deleted with every acquired runtime and database
byte. The job uploads only candidate-bound JSON metadata and its checksum; no
third-party runtime, generated Tcl, source archive, log, or full execution
directory enters an Actions artifact. The read-only downstream job therefore
rehashes the sanitized summaries and candidate registry identity rather than
pretending to relocate and reverify deleted execution bytes. The gate makes no
performance, comparability, source-provenance, or complete host-dependency
claim. Missing approval, acquisition, preparation, execution, metadata, or
cleanup evidence fails closed and leaves the release as a draft. The exact
contract is [external-driver-runner.md](external-driver-runner.md); its
presence is not proof that a live tag run has passed. The protected
publisher verifies the exact active `refs/tags/v*` creation/update/deletion
ruleset, an admin-reviewed current ruleset revision, and immutable releases
with a separate Administration-read credential, then exports digest-bound
control evidence. The candidate-executing external job consumes that artifact
but never receives the administrative credential or any driver secret. A
separate read-only job downloads and independently verifies the metadata-only
external-driver gate with the candidate binary. Only after all checks succeed
does the final static-only job
enter `release-publication`, download exact artifact IDs, rehash evidence,
re-query both preventive controls, compare the draft fingerprint, and publish
that unchanged asset set as its final command. It has no checkout and never
extracts or executes candidate bytes.

A subsequent clean `public-verify` job requires the release to be immutable,
verifies its release attestation, downloads and authenticates all 16 public
assets, and compares their complete fingerprint with the verified draft. Only
then does the workflow call all seven compatibility cells again in `published`
mode. A failure in either public gate cannot undo publication: the release
exists, but v1/adoption stays `NO-GO`. Neither compatibility mode closes the
external-user adoption gates in `v1-completion-contract.md`; durable evidence
indexing is documented in [`release-evidence.md`](release-evidence.md).
