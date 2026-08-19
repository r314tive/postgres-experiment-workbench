# Release evidence records

Release and v1 decisions are made from durable, candidate-bound records rather
than from a feature checklist or the current colour of a workflow run. The
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
record alongside draft/public verification evidence. It is deliberately not a
seventeenth release asset: including it in the release would make its own
asset-set fingerprint recursive.

After a draft exists, read-only sealing jobs also emit
[`release-compatibility-verification`](../schemas/release-compatibility-verification.schema.json)
and [`release-aggregate-verification`](../schemas/release-aggregate-verification.schema.json)
records. They enumerate the fixed seven compatibility artifact IDs/names/digests
or one aggregate artifact identity, embed the candidate-bound asset observation,
and make aggregate attempt two hash-bind the exact attempt-one record bytes.
They are deliberately not summaries of a green workflow, nor proof that Actions
will retain the referenced bytes. The closed local adapters can attach them only
to their one matching readiness requirement, with operator-attested, `NO-GO`
assurance.

Copy templates into a release-specific durable location; do not edit the
templates as historical evidence. A practical layout is
`releases/v<version>/evidence-index.json`, `pilots/<pilot-id>.json`, and
`reviews/critical-findings.json` in an immutable object store or a separately
protected evidence repository. Every referenced object has a SHA-256 digest.
An Actions artifact URL may be recorded as transport evidence, but Actions
retention alone is not the durable location and cannot close a v1 gate.

## Exact candidate binding

One decision binds the exact SemVer/tag, full Git commit, scenario-pack
identity/version/digest, and release asset fingerprint. Draft compatibility,
external-driver evidence, public verification, published compatibility,
critical review, and pilots must all name that same identity. Rebuilding,
retagging, replacing an asset, or changing a pack produces a different
candidate; evidence from the previous candidate cannot be carried forward by
description alone.

The release workflow checks preventive state before publication:

- an active tag ruleset targets exactly `refs/tags/v*`, has no exclusions, and
  restricts tag creation and prohibits tag update and deletion;
- an administrator-signed durable record covers bypass actors because ordinary
  metadata reads do not expose the complete bypass set;
- immutable releases are enabled, checked with a dedicated repository-scoped
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

The protected `release-external-drivers` environment is an approval boundary
for the GitHub-hosted release smoke; it supplies no runner paths, driver
passwords, administration credential, or review variables. The job downloads
the exact digest-bound control artifact emitted by `release-publication`,
acquires pinned driver inputs into disposable runner state, uses fresh
loopback-trust PostgreSQL 16, and uploads metadata only. No third-party runtime
bytes enter its success or failure artifacts.

Repository setup must give `release-external-drivers` a required reviewer,
prevent self-review, and restrict deployment to selected release tags. The
workflow name alone does not create those repository-side protections.

The review evidence is operator-trusted and digest-bound; the workflow does not
fetch an arbitrary evidence URI. The environment must require an appropriate
administrator reviewer. The live ruleset ID and `updated_at` must equal the
signed review, and review time must not precede the ruleset revision. Any edit
invalidates the old review. Missing credentials, review metadata, an exact
active ruleset, or `.enabled == true` for immutable releases leaves publication
closed. Both controls are queried again in the final publish step.

## Publication boundary

The source-build job has only `contents: read`, does not persist its checkout
token, and emits exactly ten unsigned candidate files under an
attempt-qualified artifact ID/name/digest/fingerprint. A separate read-only job
downloads that immutable ID and executes the candidate verifier for semantic
manifest/SBOM checks. The tag-only protected publisher consumes the same
verified ID but has no checkout, never extracts an archive, and never executes
candidate or repository code. It performs only static checks before minting
attestations and creating the fixed draft as its final mutation. Manual
dispatches cannot enter the protected publisher.

All seven compatibility cells run from downloaded draft archives before the
draft can become public. A separate read-only job statically rehashes and
validates the nine sanitized external-driver metadata records, then uses the
downloaded candidate only to rederive the driver-registry identity. It cannot
rerun `driver-run-verify` after the runtime and execution bytes are destroyed.
The job exports the exact artifact identity and verified digests. The final
`release-publication` job
uses trusted static tools only: it rehashes those exact artifacts, refreshes
live controls and the draft fingerprint, and makes the state transition as its
final command. A new clean job then requires `isImmutable=true`, verifies the
release attestation, downloads and authenticates the same 16 assets, and checks
the draft/public fingerprint. Only after that succeeds do all seven cells run
again from published archives.

The source, draft, and aggregate sealing job lists current-run artifacts through
the Actions API, requires one non-expired identity with the exact name and
candidate SHA, downloads the selected draft verification ZIP by exact artifact
ID, and rehashes it before writing typed records. A final published sealing job
repeats that pattern for public verification and all seven published cells. This
binds local record semantics to observed provider identities without claiming
remote durability, runtime support, performance comparability, recovery, or
release authorization.

A failure after publication cannot make the release private again. It means the
release exists but the release-evidence index stays `NO-GO`; it must not be
described as v1-ready or adoption-ready. Repair by preparing a new exact
candidate and running the complete contract, not by replacing public assets.
For a transient failure in `public-verify` or published compatibility, use
`gh run rerun <run-id> --failed` by default; that resumes failed jobs and their
dependants without rerunning the successful publication ancestor. A targeted
`gh run rerun <run-id> --job <job-database-id>` is also allowed only for
`public-verify` or a published-archive compatibility job, because GitHub reruns
the selected job and its dependants rather than its ancestors. Never target
`publish-release`, a draft/source job, or rerun the complete workflow after
publication: those paths reach intentional draft/existing-release guards. If a
safe failed-only or targeted read-only rerun cannot resume cleanly, prepare a
new exact release candidate; do not replace public assets.

## Adoption and review boundaries

Each pilot records one external non-maintainer, the exact candidate, released
guide, scenario, runtime/OS/architecture/PostgreSQL major, acceptance predicate,
cleanup, result, unresolved friction, bundle evidence, and where required an
independent maintainer verification. At least one authored or modified scenario
requires no maintainer shell access.

A passed pilot proves only that bounded guide/scenario outcome. Its schema
forces production-safety, representative-performance, and benchmark-decision
claims to remain false. It is not recovery evidence and carries no RTO or SLA
claim.

The critical-finding review covers security, data loss, portability, evidence
integrity, the tag ruleset, immutable releases, and administrator sign-off. A
`GO` record cannot contain an open or accepted critical finding. The top-level
release evidence index can become `complete`/`go` only when every declared gate
has passed evidence, both preventive controls are verified, and every closed
requirement carries proof-backed, decision-eligible durability and authenticity
assurance. The current v3 contract intentionally defines no such positive
assurance class, so it cannot authorize `GO` before the corresponding verifier
exists.

## Semantic CLI verification

JSON Schema remains the wire-format contract. The post-v0.2 CLI adds a second,
independent semantic layer so a structurally valid document cannot be mistaken
for release authorization merely because it contains `decision.status=go`.

```bash
pgworkbench evidence release verify evidence-index.json
pgworkbench evidence release status --json evidence-index.json
```

Both commands work outside a scenario-pack checkout. They strictly decode one
bounded regular non-symlink JSON file, reject duplicate or unknown properties
and trailing JSON, and independently derive the aggregate status and decision.
A v3 result separates the recorded decision, outcome-only readiness decision,
effective authorization decision, aggregate assurance status, and
`authorization_eligible` boolean. Legacy v1/v2 `complete/go` records remain
readable and internally verifiable, but evidence without persisted typed-record
and assurance metadata is listed under `unqualified_evidence`; the effective
decision is therefore `NO-GO` rather than a grandfathered authorization.
A consistent index with open or failed gates is valid evidence of `NO-GO`; it
is not a command failure or a release claim. A stored decision or record status
that contradicts the derived gates and preventive controls is semantically
invalid and fails verification.

## Candidate initialization

Revision zero is derived from a locally complete downloaded release set and a
typed provider asset inventory:

```bash
pgworkbench evidence candidate init \
  --release-manifest downloaded/pgworkbench-0.2.0-release-manifest.json \
  --asset-inventory draft-verification/asset-inventory.json \
  --output evidence/index-r0.json
```

The command verifies the release manifest, archive/SBOM/checksum relationships,
the closed top-level asset set, every local asset size and digest, metadata
checksum coverage, and the workflow-compatible fingerprint over sorted
`{id,name,size,digest}` records. It accepts no independent version, tag,
commit, pack, fingerprint, timestamp, or gate-status flag. The resulting v3
index is `active`, revision `0`, and a valid `NO-GO` with all readiness
requirements open.

All 16 source files are copied once into a private, digest-checked snapshot
outside the downloaded release root. Manifest, archive, embedded pack/toolchain,
SBOM, and metadata semantics are then evaluated only from those pinned bytes.
The snapshot is removed before the immutable index is published; a snapshot
cleanup failure fails initialization rather than silently leaking a second
release tree or publishing an index over unclosed temporary state.

The inventory is evidence supplied to an offline integrity verifier; its mere
presence does not authenticate GitHub, Sigstore, or the inventory producer.
Draft/public authenticity and repository state must close their own typed
gates. Output is copy-on-write and exclusive: an existing file or symlink is
never replaced. If the destination has already been linked but final inode or
directory-durability confirmation fails, the command exits non-zero and reports
`committed-unconfirmed`, `retry_safe=false`, plus the requested output and
expected digest in `--json` mode. If path identity itself failed, that requested
path is not claimed as the current inode location. Operators must reconcile the
chain directory; a blind retry is incorrect.

## Typed gate attachment

Every gate mutation consumes a producer-specific fact record. There is no
generic `--status` or `passed=true` input:

```bash
pgworkbench evidence gate attach \
  --index evidence/index-r0.json \
  --gate draft_external_drivers \
  --evidence-file downloaded/verification.json \
  --evidence-ref 's3://release-evidence/v0.2.0/external-drivers.json?versionId=...' \
  --output evidence/index-r1.json
```

The command reads one bounded regular non-symlink snapshot of each input and
uses that same exact buffer for parsing and hashing. After publication it
reopens and rereads the predecessor only to confirm that its directory entry,
inode, and bytes still match that snapshot. It checks the complete candidate
identity and derives the only gate and outcome allowed by the record type. The
local path is never stored. The new revision binds the exact previous index
digest, changes only its adapter-owned gate plus derived lifecycle fields, and
is published exclusively without replacing the predecessor.

Revision paths are canonical and adjacent in one chain directory:
`index-r<N>.json` to `index-r<N+1>.json`. This gives concurrent local writers a
single destination. The CLI pins that directory and predecessor inode before
parsing, then stages, links, confirms, cleans, and fsyncs only relative to the
open directory descriptor. Renaming or replacing the pathname cannot redirect
the successor into a different directory. If a pathname or predecessor identity
changes after exclusive publication, the command reports
`committed-unconfirmed` with the successor digest instead of claiming a clean
commit. Copying a predecessor into a separate directory can still create a
detectable fork; the local CLI does not claim a distributed global head or
compare-and-swap service.

The supported positive mappings are deliberately closed:

- `pgworkbench.release-external-driver-verification/v1` to
  `draft_external_drivers`;
- `pgworkbench.release-asset-verification/v1` in `draft` mode to
  `draft_asset_verification`;
- the same asset contract in `published` mode to
  `public_asset_verification`;
- `pgworkbench.release-publication-verification/v1` to `publication`.

The signed `pgworkbench.critical-finding-review/v1` record is also a closed
human-review mapping to `critical_finding_review`: a valid signed `go` review
with the fixed controls and no unresolved critical finding derives `passed`; a
valid signed `no-go` review derives `failed`. It requires the same index
candidate (version, tag, commit, and scenario-pack digest), exact four-category
scope, durable digest-bound control/signoff references, and an administrator
signoff. It has no workflow-run identity. Like every current attachment, its
remote durability and signature authenticity remain operator-attested rather
than authorization-eligible, so even a passed critical-review gate is not a
release `GO`.

`pgworkbench.adoption-pilot-record/v1` may attach to either
`adoption_pilot_1` or `adoption_pilot_2`; attachment derives `passed` or
`failed` only from a completed pilot result, never a caller-supplied status.
The predecessor stores the pseudonymous external participant identity, so the
other pilot slot rejects the same person. A completed, passed pilot that records
an authored/modified scenario, no maintainer shell access, and independent
bundle verification may additionally attach to
`independent_authoring_reproduction`. Pilot evidence proves only the listed
guide/scenario outcome and remains non-authorizing until a future proof-backed
durability/authenticity adapter exists.

The asset record embeds the complete provider inventory and full candidate,
requires the fixed 16-asset set, and binds the verified manifest asset. The
publication record is emitted only by the fresh read-only `public-verify` job
after it observes a published immutable release and verifies the release
attestation; it embeds the complete published-asset record. It is never emitted
by the mutating `publish-release` job. All four mappings are pass-only and carry
fixed non-performance/non-production assurance facts. Missing, malformed,
contradictory, wrong-candidate, or wrong-gate records produce no revision. An
already passed or failed gate is not silently superseded.

`--evidence-ref` is an operator assertion. Offline attachment verifies URI
shape, typed record semantics, and local content digest but does not fetch,
authenticate, or prove remote retention. Its machine-readable result therefore
reports `evidence_durability=operator-asserted-not-verified` and
`evidence_authenticity=record-semantics-verified-remote-authenticity-not-verified`.
The typed record identity, derived adapter discriminator, and same trust pair
are persisted inside the attached gate evidence; draft and published uses of
the shared asset schema cannot be swapped after the CLI exits. Independent
verification reports that gate in `unqualified_evidence` and derives
`status=open decision=no-go`, even if it was the last gate whose typed record
had a positive outcome. V3 defines no self-declared verified class. A future
proof-backed class may be added only together with an adapter that actually
authenticates the producer, durable remote object, and exact digest binding.
Callers cannot supply a `passed`, `decision_eligible`, or assurance override.
GitHub Actions run/artifact URLs are rejected because their retention is
transport-only. Move the exact summary bytes to a versioned or
content-addressed durable location first. A successful attachment can still
report `release_status=open decision=no-go`; it is not release authorization.

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

The schema, command surface, and full relocation/tamper/fault/race matrix now
complete the bounded M1.4 packaging gate. They do not complete M1, authenticate
external evidence, or authorize a release. The archive root contains exactly
`release-evidence-bundle.json` and the canonical
contiguous `index-r0.json` through `index-r<N>.json` chain. Index bytes are
copied verbatim: durable `evidence.ref` values are neither rewritten to local
paths nor replaced by bundled copies of the referenced objects.

`index-r<N>.json` selects an explicit immutable prefix. Adjacent later
revisions are not imported and do not invalidate that prefix; the bundle makes
no claim that `N` is a globally current or unique head.

The inventory binds the full candidate identity, canonical head name/revision,
exact head digest, independently recomputed outcome, file count, total size,
sorted path/revision/size/digest/mode rows, and a deterministic tree digest.
Index files have normalized mode `0644`; the inventory permits at most 256
revisions and uses JSON-safe integer bounds. Extraction must preserve archived
modes (`tar -xpzf`, not an umask-dependent extraction). Verification must
reject a gap, wrong predecessor digest, different candidate, missing or extra
path, duplicate or unsorted row, symlink or non-regular entry, changed bytes,
size or mode, and an index transplanted from a different chain. A well-formed
bundle whose head derives `NO-GO` remains valid evidence of `NO-GO`; packaging
never promotes it to `GO`.

The bundle deliberately contains no downloaded gate-record bytes, GitHub
Actions logs, provider artifacts, or other externally authored evidence. It
therefore proves only the relocated chain's internal byte, mode, lineage,
candidate, and outcome consistency. It does not verify remote retention,
authenticate a durable URI or producer, validate a signature, or authorize a
release. Because the inventory is unsigned and self-describing, an attacker
who rewrites the complete chain and inventory can create a different internally
consistent bundle; publisher trust still requires an independently trusted
archive digest, signature, or release provenance.
