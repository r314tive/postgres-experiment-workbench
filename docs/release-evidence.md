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
has durable passed evidence and both preventive controls are verified.
