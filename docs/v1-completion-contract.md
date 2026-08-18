# v1 Completion Contract

This document defines what “complete” means. A feature list, green unit tests,
or one successful local run is insufficient on its own.

## Candidate identity

The candidate is one immutable Git commit, exact release SemVer/tag, one
scenario-pack digest, and the exact stable Go patch toolchain pinned by
`.go-version`. All gate outputs must record or derive that same identity. Any
source, generated-document, pack-manifest, or release-metadata change creates a
new candidate and restarts the aggregate gate. The release-specific durable
index described in [release-evidence.md](release-evidence.md) is the authority
that joins those gate outputs; a workflow run number alone is not candidate
identity.

## Required local and CI gates

1. `make check` passes from a clean checkout.
2. Docker `single` smoke and the complete Docker regression suite pass.
3. Native `single` smoke passes using an isolated workbench-owned `PGDATA`.
4. A deliberate early setup failure still produces a terminal failed verdict
   and independently verifies without inventing a metrics sample that never ran.
5. A strict SQL assertion returning `false` fails the run.
6. A metrics-disabled run verifies without `metrics.csv`.
7. A run bundle passes required-inventory `run verify --bundle` after extraction
   beneath a different absolute path.
8. A current-platform release archive is extracted outside the checkout; its
   binary runs `version`, `pack validate`, `experiment plan`, native experiment
   smoke, native pgbench smoke, and relocated evidence verification.
9. Benchmark preflight and timeout failures close the exact eleven-phase journal
   and leave a terminal independently verifiable failed artifact. New journals
   bind every row to the linked run and trial; a passed trial also verifies the
   linked primary journal against its byte-identical series mirror.
10. Docker and native pgbench smoke produce verified series. Deterministic
    series, history, campaign, import, operation, and counterbalanced A/B
    artifact and bundle tests pass after relocation and reject their declared
    tamper matrices. External-driver execution-directory relocation and tamper
    tests pass their v2 closed-inventory verifier, including missing, extra,
    changed, mode-drifted, and adapter-invalid runtime-closure files; external
    adapters may use bounded fake producers at this local contract gate. The A/B
    gate may use a synthetic producer and does not imply a locally qualified
    host.
    A/B verification must independently reparse every trial's bounded effective
    `pg_settings` source and reject comment-only/equivalent configs, missing or
    transplanted rows, pending restart, server-version mismatch, and within-arm
    drift as decision evidence. Native-toolchain A/B verification must also
    reject matching executable byte sets, unequal observed versions for any of
    the seven tools, pre/post byte drift, and provenance fields other than the
    required `unattested` value. Documentation and release claims must not
    present those bytes as source-patch causality evidence.
11. A pgdrill baseline-provenance export from a verified experiment re-verifies
    after relocation and rejects a changed manifest, verdict, pack/spec digest,
    runtime identity, or reviewed predicate.
12. Checksums cover every archive; privacy and failure-evidence scans pass.
13. Every release binary embeds the pinned exact Go patch toolchain, and the
    release manifest independently binds the same toolchain across all archives.
14. The same unchanged candidate passes this aggregate gate twice.

## Required publication gates

- Build all advertised OS/architecture archives from the candidate.
- Validate the release-platform ledger: every built archive is explicitly
  `runtime-gated` or `compile-package-only`; the latter classification must not
  be presented as runtime support.
- Attach checksums, signatures, SBOM, and provenance to a non-public draft.
- Before publication, verify an active tag-target ruleset whose only include is
  `refs/tags/v*`, whose exclusion list is empty, and whose rules prohibit both
  update and deletion. Attach an administrator-signed review of bypass actors,
  which repository metadata readers cannot see completely.
- Before publication, verify through a separate repository-scoped
  Administration-read credential that immutable releases are enabled. This
  credential is protected by `release-publication`, is never the publication
  token, and never reaches candidate provisioning or execution.
- Download the draft artifacts into a clean environment and verify them
  independently of the build workspace, including all 16 asset identities,
  checksums, platform-specific SBOM/package roots, provenance, and attestations.
- Repeat all seven advertised compatibility cells using those downloaded draft
  artifacts before publication. Only platforms with such passing cells are runtime-qualified;
  compile/package-only archives remain build outputs rather than support
  claims.
- Execute each advertised pinned BenchBase, HammerDB, and sysbench adapter once
  from a downloaded draft archive on protected GitHub-hosted `ubuntu-24.04`
  against a fresh owned loopback PostgreSQL 16 cluster. Acquire exact pinned
  inputs, prepare the bounded datasets, and locally verify each v2 execution
  artifact and its adapter-selected staged runtime closure. The exact
  acquisition/runtime-root layouts and prepared-database boundary must satisfy
  [external-driver-runner.md](external-driver-runner.md). This is adapter
  compatibility evidence, not host/dependency, dataset, source-to-binary,
  performance, or benchmark-comparability attestation.
- Retain only the candidate-bound metadata allowlist and checksum. No
  third-party runtime, source archive, generated Tcl, build tree, database,
  stdout/stderr, or full execution directory may enter a success or failure
  Actions artifact or release asset. Failure evidence is limited to one
  workflow-authored JSON record. Downstream verification rehashes sanitized
  execution summaries and candidate identity; it cannot claim relocation
  verification after the licensed runtime bytes are destroyed.
- Keep publication fail-closed on that real-driver evidence. Missing protected
  approval/hosted capacity, exact acquisition, prepared datasets, driver
  execution, cleanup, or verified metadata leaves the release as a draft;
  bounded fake producers from the local contract gate do not satisfy this
  publication gate.
- Publish that same verified draft only after the clean-download, seven-cell
  draft compatibility, preventive-control, and external-driver gates succeed.
  The draft-to-public transition is the final command in the publication job.
- In a new clean job, require the resulting release to be public and immutable,
  verify its release attestation, download and authenticate all 16 assets, and
  require their fingerprint to equal the verified draft fingerprint.
- After that public-asset gate, repeat the same seven compatibility cells from
  the published archives. This second pass cannot be substituted by the draft
  pass.
- Preserve every project-authored gate output under the release-specific
  durable evidence location. GitHub Actions retention is transport, not
  permanent publication; the durable external-driver object remains the exact
  metadata-only archive and checksum, never upstream runtime bytes.

Publication is irreversible within the workflow. If public asset verification
or any published compatibility cell fails, the public release exists, but the
candidate remains `NO-GO` for v1 and adoption claims until a new exact candidate
completes the entire contract; the workflow does not silently rewrite assets.

## Required adoption gates

- Two external users execute a bounded starter scenario from the released
  authoring guide.
- At least one user authors or modifies a scenario without maintainer shell
  access and produces a bundle that the maintainer verifies independently.
- Each pilot records candidate version, pack digest, runtime, PostgreSQL major,
  acceptance predicate, cleanup, result, and unresolved friction.
- Pilot records prove only the bounded guide/scenario outcome they contain.
  They do not claim production safety, representative performance, benchmark
  comparability, backup validity, recovery success, RTO, or SLA.

## Stop conditions

The candidate is not v1-ready if any of these remain:

- a run can exit without a terminal verdict;
- evidence verification depends on the producer's absolute filesystem path;
- disabled optional evidence is treated as corruption;
- runtime reset can silently reuse a previous PostgreSQL configuration;
- a release archive needs the source repository or Go toolchain to operate;
- the managed native experiment, utility, or pgbench runtime can attach to a
  PostgreSQL cluster it did not create and own;
- an external driver can run without the explicit disposable-target
  acknowledgement, against a non-loopback host, or against `postgres`,
  `template0`, or `template1`;
- an external driver can run without `--runtime-root`, with a required symlink
  or out-of-root entrypoint, or with a retained runtime tree whose exact
  adapter-selected closure, modes, and digest cannot be re-derived after
  relocation;
- documentation implies production safety, representative performance, backup
  validity, recovery success, RTO, or SLA from an experiment result;
- an ordinary benchmark series, history, campaign, operation series, imported
  result, external single-trial artifact, or unsigned host snapshot can be
  promoted into a causal performance decision;
- a different config path/digest without a stable, applied, cross-arm effective
  `pg_settings` value-and-unit difference can produce an A/B verdict;
- a native-toolchain A/B verdict survives unequal observed tool versions,
  identical seven-file byte sets, pre/post byte drift, or is presented as proof
  of source-patch causality;
- a workbench pgdrill baseline can be presented as restore/recovery evidence;
- a required compatibility or external adoption cell has no attached evidence.

## Release decision

The maintainer records `GO` only when every required local, publication, and
adoption gate is complete for the same candidate. Otherwise the decision is
`NO-GO`, with the exact unclosed gates listed. Deferred product ideas are not
release blockers unless they are advertised as supported.
