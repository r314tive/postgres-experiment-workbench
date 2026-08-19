# Release evidence records

Release and v1 decisions use durable records tied to one candidate rather than
a feature checklist or current workflow status. The
three tracked templates are:

- [`release-evidence-index.json`](../evidence/templates/release-evidence-index.json),
  validated by
  [`release-evidence-index.schema.json`](../schemas/release-evidence-index.schema.json);
- [`adoption-pilot-record.json`](../evidence/templates/adoption-pilot-record.json),
  validated by
  [`adoption-pilot-record.schema.json`](../schemas/adoption-pilot-record.schema.json);
- [`critical-finding-review.json`](../evidence/templates/critical-finding-review.json),
  validated by
  [`critical-finding-review.schema.json`](../schemas/critical-finding-review.schema.json).

The generated `release-evidence-bundle.json` closed inventory is validated by
[`release-evidence-bundle-inventory.schema.json`](../schemas/release-evidence-bundle-inventory.schema.json).
It is generated from an existing immutable revision chain and therefore has no
tracked template.

The release workflow additionally emits a typed
[`release-asset-inventory`](../schemas/release-asset-inventory.schema.json)
record alongside draft/public verification evidence. It is not a
seventeenth release asset: including it in the release would make its own
asset-set fingerprint recursive.

After a draft exists, read-only jobs emit
[`release-compatibility-verification`](../schemas/release-compatibility-verification.schema.json)
and [`release-aggregate-verification`](../schemas/release-aggregate-verification.schema.json)
records. They bind the candidate and asset observation to the seven
compatibility artifact IDs, names, and digests or to one aggregate artifact.
Aggregate attempt two also hashes the attempt-one record bytes. Each local
adapter can update only its matching readiness requirement.

The protected controls job emits
[`release-preventive-controls-verification`](../schemas/release-preventive-controls-verification.schema.json).
It binds the candidate and same-run draft assets to the source control artifact,
current tag-ruleset and immutable-release API digests, and the reviewed ruleset
revision.

Current adapters verify local record semantics and content digests. They do not
fetch the durable reference, authenticate its producer, or prove remote
retention. Attached evidence therefore uses these values:

- `evidence_durability=operator-asserted-not-verified`;
- `evidence_authenticity=record-semantics-verified-remote-authenticity-not-verified`;
- `authorization_eligible=false`.

Passed records with only these assurances appear in `unqualified_evidence`, and
the effective decision stays `NO-GO`. V3 cannot authorize release because it
does not verify the producer, remote object digest, or retention. A future
adapter must perform those checks.

GitHub Actions run and artifact URLs are transport references, not durable
evidence, and the CLI rejects them as `--evidence-ref` values. The
preventive-controls record likewise does not fetch the referenced bypass review
or verify its signature. These boundaries apply to every record below unless a
stricter one is stated.

Copy templates into a release-specific durable location; do not edit the
templates as historical evidence. One possible layout is
`releases/v<version>/evidence-index.json`, `pilots/<pilot-id>.json`, and
`reviews/critical-findings.json` in an immutable object store or a separately
protected evidence repository. Every referenced object has a SHA-256 digest.

## Candidate binding

One decision binds the SemVer/tag, full Git commit, scenario-pack
identity/version/digest, and release asset fingerprint. Draft compatibility,
external-driver evidence, public verification, published compatibility,
critical review, and pilots must all name that same identity. Rebuilding,
retagging, replacing an asset, or changing a pack produces a different
candidate; evidence does not carry forward by description alone.

Before publication, the workflow requires:

- an active tag ruleset that targets exactly `refs/tags/v*`, has no exclusions,
  restricts tag creation, and prohibits tag update and deletion;
- an administrator-signed durable record covering bypass actors, because
  metadata reads do not expose the complete bypass set;
- immutable releases enabled and checked with a dedicated repository-scoped
  fine-grained PAT or GitHub App token with only `Administration: read`.

That credential is the `PGWORKBENCH_ADMIN_READ_TOKEN` secret in the protected
`release-publication` environment. It is separate from `github.token`, which
performs publication. That environment supplies these non-secret variables for
the administrator-signed bypass review:

- `PGWORKBENCH_TAG_RULESET_ADMIN_REVIEW_REF` — durable `https:`, `s3:`, `gs:`,
  or `urn:` reference;
- `PGWORKBENCH_TAG_RULESET_ADMIN_REVIEW_DIGEST` — `sha256:<64 lowercase hex>`;
- `PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWER` — repository administrator identity;
- `PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWED_AT` — UTC timestamp ending in `Z`;
- `PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWED_ID` and
  `PGWORKBENCH_TAG_RULESET_ADMIN_REVIEWED_UPDATED_AT` — the exact ruleset
  revision reviewed for hidden bypass actors.

The protected `release-external-drivers` environment approves the GitHub-hosted
release smoke. It supplies no runner paths, driver passwords, administration
credential, or review variables. The job downloads the digest-bound control
artifact from `release-publication`, acquires pinned driver inputs in disposable
runner state, uses loopback-trust PostgreSQL 16, and uploads metadata only. Its
artifacts contain no third-party runtime bytes.

Repository settings must give `release-external-drivers` a required reviewer,
prevent self-review, and restrict deployment to selected release tags.

The live ruleset ID and `updated_at` must match the signed review, whose
`reviewed_at` cannot precede that revision. Any ruleset edit invalidates the
review. Missing credentials or review metadata, the wrong active ruleset, or
immutable releases with `.enabled != true` keep publication closed.
`seal-preventive-controls` queries the controls after the draft and emits the
typed record with its artifact ID, name, and digest. `publish-release` consumes
that artifact and repeats the live comparison immediately before publication.

## Publication boundary

The source-build job has `contents: read`, does not persist its checkout token,
and emits ten unsigned candidate files under an attempt-qualified artifact ID,
name, digest, and fingerprint. A separate read-only job downloads that artifact
and verifies the manifest and SBOMs. The protected tag publisher consumes the
same artifact without a checkout, archive extraction, or candidate code
execution. It mints the attestations, then creates the fixed draft as its final
mutation. Manual dispatches cannot enter this job.

Before publication, all seven compatibility cells run from downloaded draft
archives. Another read-only job verifies the nine sanitized external-driver
metadata records and rederives the driver-registry identity. It cannot rerun
`driver-run-verify` after the runtime and execution bytes have been destroyed.
The `release-publication` job rehashes these artifacts, refreshes the controls
and draft fingerprint, and publishes only after they still match.

A clean post-publication job requires `isImmutable=true`, verifies the release
attestation, authenticates the same 16 assets, and compares the draft and public
fingerprints. The seven compatibility cells then run again from the published
archives.

Sealing jobs select current-run artifacts through the Actions API by candidate
SHA, non-expired ID, and name, then download and rehash them before writing typed
records. The published sealer does the same for public verification and all
seven published cells. This binds records to observed provider identities under
the trust model above.

A post-publication failure leaves the immutable release public and the evidence
index at `NO-GO`. Do not replace its assets. For transient failures in
`public-verify` or published compatibility, prefer
`gh run rerun <run-id> --failed`. A targeted
`gh run rerun <run-id> --job <job-database-id>` is allowed only for those jobs;
GitHub also reruns their dependants. Never target `publish-release`, a
draft/source job, or the complete workflow after publication. If the read-only
rerun cannot recover, prepare a new candidate.

## Adoption and review boundaries

Each pilot records an external non-maintainer and the candidate, released guide,
scenario, runtime/OS/architecture/PostgreSQL major, acceptance predicate,
cleanup, result, unresolved friction, bundle evidence, and where required an
independent maintainer verification. At least one scenario must be authored or
modified without maintainer shell access.

A passed pilot covers only its guide and scenario. Its schema keeps
production-safety, representative-performance, and benchmark-decision claims
false; it carries no recovery, RTO, or SLA claim.

The critical-finding review covers security, data loss, portability, evidence
integrity, the tag ruleset, immutable releases, and administrator sign-off. A
`GO` record cannot contain an open or accepted critical finding. The top-level
index reaches `complete`/`go` only when every gate has passed, both preventive
controls are verified, and every passed requirement carries independently
verified evidence eligible for a release decision. V3 cannot meet that last
condition.

## Semantic CLI verification

JSON Schema defines the wire format; the CLI independently derives the semantic
result instead of trusting `decision.status`.

```bash
pgworkbench evidence release verify evidence-index.json
pgworkbench evidence release status --json evidence-index.json
```

Both commands work outside a scenario-pack checkout. They read one size-limited
regular JSON file, reject symlinks, duplicate or unknown properties, and
trailing JSON, and derive the aggregate status and decision. V3 reports the
recorded decision, readiness outcome, effective authorization decision,
aggregate assurance status, and `authorization_eligible` separately. Legacy
v1/v2 `complete/go` records remain readable, but evidence without persisted
typed-record and assurance metadata appears in `unqualified_evidence` and has
an effective `NO-GO`. A consistent open or failed index is valid evidence; a
stored result that contradicts derived gates or controls fails verification.

## Candidate initialization

Revision zero is derived from a locally complete downloaded release set and a
typed provider asset inventory:

```bash
release_version="${PGWORKBENCH_RELEASE_VERSION:?export PGWORKBENCH_RELEASE_VERSION}"
pgworkbench evidence candidate init \
  --release-manifest "downloaded/pgworkbench-${release_version}-release-manifest.json" \
  --asset-inventory draft-verification/asset-inventory.json \
  --output evidence/index-r0.json
```

The command verifies the manifest, archive/SBOM/checksum relationships, closed
asset set, local sizes and digests, metadata checksum coverage, and the workflow
fingerprint over sorted `{id,name,size,digest}` records. It accepts no separate
version, tag, commit, pack, fingerprint, timestamp, or gate-status flag. The v3
result is `active`, revision `0`, and `NO-GO` with every readiness requirement
open.

All 16 source files are copied to a private, digest-checked snapshot outside the
download root. Manifest, archive, embedded pack/toolchain, SBOM, and metadata
checks use only those bytes. The snapshot must be removed before publishing the
index; cleanup failure aborts initialization.

The inventory alone does not authenticate GitHub, Sigstore, or its producer;
draft/public authenticity and repository state have separate gates. Output is
exclusive and copy-on-write, so an existing file or symlink is never replaced.
If linking succeeds but inode or directory-durability confirmation fails, the
command exits non-zero with `committed-unconfirmed`, `retry_safe=false`, the
requested output, and expected digest in `--json` mode. Reconcile the chain
directory before retrying.

## Typed gate attachment

Every gate mutation consumes a producer-specific fact record. There is no
generic `--status` or `passed=true` input:

```bash
release_version="${PGWORKBENCH_RELEASE_VERSION:?export PGWORKBENCH_RELEASE_VERSION}"
pgworkbench evidence gate attach \
  --index evidence/index-r0.json \
  --gate draft_external_drivers \
  --evidence-file downloaded/verification.json \
  --evidence-ref "s3://release-evidence/v${release_version}/external-drivers.json?versionId=..." \
  --output evidence/index-r1.json
```

The command parses and hashes one size-limited regular-file snapshot of each
input, rejects symlinks, checks the candidate identity, and derives the gate and
outcome allowed by the record type. It stores the durable reference, not the
local path. The new
revision binds the previous index digest, changes only the adapter-owned gate
and derived lifecycle fields, and never replaces its predecessor.

Revisions are adjacent canonical paths, from `index-r<N>.json` to
`index-r<N+1>.json`. The CLI pins the directory and predecessor inode, publishes
through the open directory descriptor, and syncs the directory. A path
replacement cannot redirect the successor. If the path or predecessor changes
after publication, the result is `committed-unconfirmed` with the successor
digest. A copied chain may still fork in another directory; lineage detects the
fork but does not provide a distributed global head.

The supported positive mappings are closed:

- `pgworkbench.release-external-driver-verification/v1` to
  `draft_external_drivers`;
- `pgworkbench.release-asset-verification/v1` in `draft` mode to
  `draft_asset_verification`;
- the same asset contract in `published` mode to
  `public_asset_verification`;
- `pgworkbench.release-publication-verification/v1` to `publication`;
- `pgworkbench.release-compatibility-verification/v1` in `source`, `draft`, or
  `published` mode to its corresponding compatibility gate;
- `pgworkbench.release-aggregate-verification/v1` attempt 1 or 2 to its
  corresponding aggregate gate, with attempt 2 bound to the attached attempt-1
  record digest.

Preventive controls use a separate atomic command because they are one observed
control set, not three caller-selectable gate outcomes:

```bash
release_version="${PGWORKBENCH_RELEASE_VERSION:?export PGWORKBENCH_RELEASE_VERSION}"
pgworkbench evidence controls attach \
  --index evidence/index-rN.json \
  --evidence-file downloaded/preventive-controls-verification.json \
  --evidence-ref "s3://release-evidence/v${release_version}/preventive-controls.json?versionId=..." \
  --output evidence/index-rN+1.json
```

One valid record changes exactly the canonical open tag ruleset, bypass review,
and immutable-release requirements to `verified`, `admin-reviewed`, and
`verified`. It cannot repair a partial state or replace earlier evidence. All
three paths store the same record identity and trust values with path-specific
adapter discriminators. Bundle verification accepts only this atomic
transition; the three requirements remain in `unqualified_evidence`.

The signed `pgworkbench.critical-finding-review/v1` record maps to
`critical_finding_review`. A signed `go` review with the fixed controls and no
unresolved critical finding derives `passed`; a signed `no-go` review derives
`failed`. It must match the index version, tag, commit, and scenario-pack
digest, cover the four required categories, reference digest-bound controls and
signoff, and include administrator signoff. It has no workflow-run identity and
uses the trust model above.

`pgworkbench.adoption-pilot-record/v1` may attach to either
`adoption_pilot_1` or `adoption_pilot_2`; attachment derives `passed` or
`failed` only from a completed pilot result, never a caller-supplied status.
The predecessor stores the pseudonymous external participant identity, so the
other pilot slot rejects the same person. A completed, passed pilot that records
an authored/modified scenario, no maintainer shell access, and independent
bundle verification may additionally attach to
`independent_authoring_reproduction`. Its claim remains limited to the recorded
guide and scenario.

The asset record embeds the provider inventory and full candidate,
requires the fixed 16-asset set, and binds the verified manifest asset. The
publication record is emitted only by the read-only `public-verify` job
after it observes a published immutable release and verifies the release
attestation; it embeds the complete published-asset record. It is never emitted
by the mutating `publish-release` job. All workflow verification mappings are
pass-only and carry fixed non-performance/non-production assurance facts.
Missing, malformed, contradictory, wrong-candidate, or wrong-gate records
produce no revision. An
already passed or failed gate is not silently superseded.

The attachment stores the record identity, adapter discriminator, and trust
values, so draft and published uses of the shared asset schema cannot be
swapped later. Callers cannot supply `passed`, `decision_eligible`, or an
assurance override. The CLI rejects GitHub Actions run and artifact URLs as
`--evidence-ref`; first move the summary bytes to a versioned or
content-addressed durable location. A successful attachment may still report
`release_status=open decision=no-go`.

## Closed evidence bundle contract

M1.4 defines a deterministic, relocatable archive around one complete index
revision chain:

```bash
pgworkbench evidence bundle create --json \
  evidence/index-r7.json generated/release-evidence-r7.tar.gz

# Preserve the archived 0644 modes even under a restrictive umask, then verify:
mkdir -p extracted
tar -xpzf generated/release-evidence-r7.tar.gz -C extracted
pgworkbench evidence bundle verify --json \
  extracted/pgworkbench-release-evidence
```

The archive root contains exactly `release-evidence-bundle.json` and the
canonical contiguous `index-r0.json` through `index-r<N>.json` chain. Index
bytes are copied verbatim: durable `evidence.ref` values are not rewritten or
replaced by bundled copies of the referenced objects.

`index-r<N>.json` selects an immutable prefix. Later adjacent revisions are not
imported and do not invalidate it; `N` is not claimed as a global or unique
head.

The inventory binds the candidate identity, canonical head name/revision, head
digest, independently recomputed outcome, file count, total size,
sorted path/revision/size/digest/mode rows, and a deterministic tree digest.
Index files have normalized mode `0644`; the inventory permits at most 256
revisions and uses JSON-safe integer bounds. Extraction must preserve archived
modes (`tar -xpzf`, not an umask-dependent extraction). Verification must
reject a gap, wrong predecessor digest, different candidate, missing or extra
path, duplicate or unsorted row, symlink or non-regular entry, changed bytes,
size or mode, and an index transplanted from another chain. A valid bundle may
still have a `NO-GO` head; packaging never changes the decision.

The bundle contains no downloaded gate records, Actions logs, provider
artifacts, or other external evidence. It verifies only the relocated chain's
bytes, modes, lineage, candidate, and outcome. The inventory is unsigned and
self-describing, so publisher trust still requires an independently trusted
archive digest, signature, or release provenance.
