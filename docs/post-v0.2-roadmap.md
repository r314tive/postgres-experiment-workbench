# Post-v0.2 execution roadmap

This roadmap starts from the immutable `v0.2.0` source candidate. It is an
execution order, not a feature wishlist. A milestone closes only when its
artifact, verifier, failure paths, portability checks, and bounded claim all
exist for one exact candidate.

The product remains a reproducible PostgreSQL experiment and performance
regression laboratory:

```text
question -> immutable protocol -> owned disposable runtime -> execution
         -> raw evidence -> independent verification -> bounded conclusion
```

It is not a production database operator, a universal tuning advisor, a TPC
implementation, a public leaderboard, or a recovery-assurance engine.
`pgdrill` remains the separate consumer responsible for provider-backed restore
and recovery evidence.

## Work streams and dependency order

```text
M0 release v0.2.0 ---------------------------> M9 v1 evidence/adoption gate
          |                                      ^
          v                                      |
M1 evidence control plane -> M2 A/A + design -> M3 metric/noise controls
                                                     |
                                                     v
M4 collectors -> M5 source-build subjects -> M6 workloads/topologies
       |                                             |
       +-----------------> M7 pgdrill bridge <-------+
                                                     |
                                                     v
                                             M8 remote execution
```

M0 and post-v0.2 development may proceed in parallel, but the frozen release
candidate must not change. Post-v0.2 commits live on a separate branch and
cannot be cherry-picked into `release/v0.2.0` without creating a new candidate
and repeating every release gate.

## Milestone map

| Milestone | Product outcome | Exit criterion |
| --- | --- | --- |
| M0 | Published and independently verified `v0.2.0` | Exact candidate passes every draft/public release gate; adoption gates remain explicitly open |
| M1 | Evidence control plane | One CLI derives `GO`/`NO-GO` from verified candidate-bound records and produces a relocated closed bundle |
| M2 | Statistically qualified benchmark method | A/A calibration and immutable study design precede any A/B decision |
| M3 | Measurement-grade metric and noise contract | Warm-up, dataset identity, continuous noise, outcome budgets, and tail metrics verify independently |
| M4 | Version-aware collector platform | Every collector has an availability, reset, coverage, privacy, and measured-overhead contract |
| M5 | PostgreSQL build subjects | A/B arms bind verified source/build/install capsules, not only seven executable bytes |
| M6 | Workload and topology depth | New packs close correctness, lifecycle, applicability, and evidence gates before performance claims |
| M7 | pgdrill end-to-end lineage | A workbench baseline is consumed and revalidated only inside an isolated recovered target |
| M8 | Portable trusted execution | Signed job/result capsules run on a single-use worker before any scheduler or Kubernetes control plane |
| M9 | v1 product gate | Published immutable candidate plus independent adoption and zero open critical integrity findings |

Current execution state on `next/v0.3`:

- M1.1 is implemented and committed: strict semantic status/verification works
  outside a checkout and derives `GO`/`NO-GO` independently.
- Candidate initialization is implemented and committed: exact 16-asset
  snapshot, manifest/inventory cross-binding, revision zero, copy-on-write
  publication, and typed ambiguous-commit handling.
- The typed attachment framework and four pass-only mappings are implemented.
  External-driver, draft/public asset, and post-publication records close only
  their adapter-owned gates; the caller cannot supply an outcome. The persisted
  adapter discriminator keeps draft and published uses of the shared asset
  record type distinct during later semantic verification.
- M1.2 remains open until the remaining workflow producers emit equally strict
  candidate-bound records and preventive controls have a separate atomic
  attachment adapter. Unsupported records stay open rather than being inferred
  from artifact presence.

## M0 — publish the frozen v0.2.0 candidate

No feature or documentation bytes may change in this milestone.

Deliverables:

1. Push the exact release branch and open a PR.
2. Pass `check` and source `compatibility` on the synthetic merge commit.
3. Merge, then pass both workflows again with `headSha` equal to exact `main`.
4. Run two consecutive aggregate candidate gates for that identity.
5. Optionally run the untagged exact-SHA release snapshot.
6. Create `v0.2.0` only after the exact-main gates pass.
7. Build the protected non-public draft, signatures, attestations, checksums,
   SPDX SBOMs, and release manifest.
8. Independently download and authenticate all draft assets, run all seven
   draft compatibility cells, and run the real protected BenchBase, HammerDB,
   and sysbench adapter cells.
9. Verify tag rules, reviewed bypass actors, and immutable releases; publish as
   the final state-changing command.
10. From a fresh job, authenticate public immutable assets and repeat all seven
    published compatibility cells.

Gate:

- every record binds one version, tag, full commit, pack digest, and asset
  fingerprint;
- a rebuilt or replaced byte creates a different candidate;
- a missing hosted gate leaves the release decision `NO-GO`;
- publication does not imply v1 adoption readiness.

## M1 — evidence control plane and adoption UX

The repository already defines release-index, pilot, and critical-review JSON
schemas. The missing product surface is semantic assembly: users currently
copy JSON manually, and schema validity alone cannot prove that all records
describe the same candidate.

### M1.1 Semantic release-index verifier

CLI:

```text
pgworkbench evidence release verify [--json] <index.json>
pgworkbench evidence release status [--json] <index.json>
```

The verifier must:

- reject unknown JSON, duplicate/trailing input, invalid timestamps, malformed
  SemVer/commit/digests, and non-durable evidence references;
- recompute open, failed, and passed gates rather than trust a stored decision;
- require verified tag controls, reviewed bypass actors, and immutable releases
  before `GO`;
- reject `complete`/`go` when any required gate is open or failed;
- return stable sorted reasons and machine-readable status;
- distinguish malformed input from a well-formed `NO-GO` record.

### M1.2 Candidate initialization and gate attachment

CLI:

```text
pgworkbench evidence candidate init --release-manifest ... \
  --asset-inventory ... --output index-r0.json
pgworkbench evidence gate attach --index index-r0.json --gate ... \
  --evidence-file ... --evidence-ref ... --output index-r1.json
```

Candidate identity is derived from verified release artifacts. Version, commit,
pack identity, and asset fingerprint are never accepted as unrelated free-text
values. The asset fingerprint is recomputed from the typed 16-asset provider
inventory with the same canonical algorithm as the release workflow. Offline
initialization proves local byte/content binding, not GitHub or Sigstore
authenticity; those remain separate gates. Gate attachment rehashes a supplied
downloaded object and records its durable URI separately from the local
verification path. Every mutation creates a new index revision bound to the
exact previous index digest; an existing index is never rewritten in place.
The predecessor and successor names are canonical `index-r<N>.json` and
`index-r<N+1>.json` in one directory, giving concurrent local writers one
exclusive destination. The directory and predecessor inode stay pinned while
the successor is staged, linked, confirmed, cleaned, and fsynced with
descriptor-relative operations; pathname replacement cannot redirect the
write. A copied chain can still fork in another directory; lineage makes that
fork detectable but does not claim a distributed global compare-and-swap head.

The first adapter accepts only
`pgworkbench.release-external-driver-verification/v1`, emitted after the
read-only workflow has reverified the draft candidate, authenticated release
archive and manifest, metadata-only provider artifact, exact three-driver set,
and bounded non-performance assurance facts. An Actions artifact is transport,
not a durable reference. The CLI rejects Actions transport URLs and reports
durability and remote authenticity separately as operator-asserted and
unverified because it does not fetch the URI. The successor upgrades the chain
to v3 and stores both the typed record identity and that exact trust class. It
appears in `unqualified_evidence`; a positive typed record closes its
record-level check but cannot produce release `GO`. V3 exposes no self-declared
verified value. A future proof-backed assurance class lands only with an
adapter that independently verifies durable remote presence, exact digest,
producer identity, and the remote object binding.

### M1.3 Pilot and critical-review readers

Add typed semantic verification for adoption pilots and critical-finding
reviews. Cross-record verification must require the same candidate identity,
two distinct external non-maintainers, and at least one independently verified
authored/modified scenario completed without maintainer shell access.

### M1.4 Closed evidence bundle

```text
pgworkbench evidence bundle create <index.json> <bundle.tar.gz>
pgworkbench evidence bundle verify <extracted-root>
```

The deterministic bundle contains only project-authored evidence records and a
closed inventory. It never rewrites durable references into local paths. The
verifier rejects missing, extra, transplanted, duplicate, symlinked, mode-
drifted, or digest-changed records after relocation.

Exit gate:

- tamper and relocation matrices pass;
- wrong commit, pack, or fingerprint cannot be attached;
- any open/failed gate deterministically yields `NO-GO`;
- two real pilot records can be ingested without manual semantic exceptions.

## M2 — benchmark method qualification: A/A before A/B

The first scientific gap is not another workload. It is proof that the method
does not invent changes under a known-no-change subject.

### M2.1 A/A protocol and run

Artifacts:

- `pgworkbench.benchmark-aa-protocol/v1`;
- `pgworkbench.benchmark-aa-run/v1`;
- deterministic A/A bundle inventory.

CLI:

```text
pgworkbench benchmark aa-run ...
pgworkbench benchmark aa-show <run>
pgworkbench benchmark aa-verify <run>
pgworkbench benchmark aa-bundle <run> <archive>
```

The A/A path permits intentionally identical byte/config subjects, reuses the
complete paired schedule and qualification contract, and is permanently
diagnostic. It can report dispersion, confidence-interval width, order effect,
time trend, and missingness asymmetry, but never `improved` or `regressed`.

### M2.2 Immutable study design

Artifacts:

- `benchmark-noise-profile/v1`, derived only from verified A/A evidence;
- `benchmark-design/v1`, binding minimum detectable effect, practical
  regression threshold, confidence/power target, fixed unit count,
  randomization algorithm, seeds, exclusion policy, and stopping rule.

The design command estimates a fixed sample size from calibration evidence.
Execution cannot extend the run after observing results. A calibration run may
design a later study but cannot be reused as an A/B outcome.

### M2.3 Balanced randomized scheduling

Replace the permanently fixed `AB,BA` unit orientation with a predeclared,
seeded, balanced schedule. Every complete unit still contains both orders.
Verification independently regenerates the schedule from its algorithm/version
and seed; editing serialized orders cannot change the study.

Exit gate:

- zero-effect synthetic and live A/A studies remain diagnostic;
- injected secular drift and order effects are detected;
- treatment-dependent missingness closes the decision gate;
- optional stopping and post-hoc schedule/threshold edits fail;
- one native and one Docker A/A bundle verify after relocation.

## M3 — stabilization, dataset identity, noise, and metric policy

### M3.1 Stabilization contract

Add a predeclared warm-up policy with minimum/maximum duration, rolling window,
slope and CV thresholds, and required consecutive stable windows. A run that
never stabilizes is valid but inconclusive; it is not silently measured after a
fixed arbitrary pause.

### M3.2 Dataset-state identity

Before every arm, retain a narrow workload-specific state identity: schema,
indexes/extensions, declared row counts, and invariant query digests. Temporary
mutation followed by restoration remains visible in the phase journal.

### M3.3 Continuous host/noise evidence

Before/after host snapshots become bookends around a phase-owned host time
series. Measurement windows require cadence coverage and predeclared limits.
After execution, the runner waits for bounded host recovery before recording
the final bookend so the workload's own residual load is not mislabeled as
external interference.

### M3.4 Metric policy v2

Allow exactly one primary metric and predeclared non-inferiority guardrails.
Add p95/p99 latency and schedule lag, goodput, SLO-success ratio,
failed/skipped/retried ratios, and WAL bytes per successful transaction where
reset evidence is valid. There is no weighted composite score.

`outcome_policy=strict-zero|budgeted` makes an open-loop skipped/late budget an
explicit protocol choice. Quantiles require complete transaction logs, or an
explicit sampling contract that remains descriptive until its estimator is
qualified.

Exit gate:

- dataset state is equivalent across paired arms;
- noise coverage contains every measure window;
- sampled-tail, cadence-gap, transplant, and reset tamper tests fail closed;
- primary and guardrail decisions are recomputed over the same complete units;
- no metric is promoted post hoc.

## M4 — version-aware PostgreSQL and host collectors

Build a collector registry whose entries declare supported PostgreSQL majors,
privilege needs, phase window, reset semantics, cadence, raw format, maximum
evidence size, privacy class, and availability behavior.

Initial collectors:

- PostgreSQL: `pg_stat_io`, WAL/checkpointer/bgwriter, waits/locks, and an
  opt-in bounded `pg_stat_statements` projection;
- Linux: process and cgroup-v2 CPU/memory/I/O, PSI, CPU frequency/governor,
  page faults, context switches, and block-I/O counters;
- privileged optional tier: `perf`/eBPF only after an explicit capability and
  privacy contract.

Each PostgreSQL major gets its own tested query shape. In particular, version
changes such as checkpointer statistics or WAL I/O timing cannot be normalized
by silently filling missing fields with zero.

Add `benchmark collector-calibrate`: diagnostic A/A collector-off/on runs must
prove both low sampling duty cycle and bounded workload impact before a
collector is measurement-grade.

Exit gate:

- PostgreSQL 15–19 parser fixtures cover supported shapes;
- unsupported major/OS/privilege produces typed `unavailable` evidence;
- exact measurement-window coverage and reset boundaries rederive from raw
  bytes;
- collector overhead stays below its declared bound.

## M5 — PostgreSQL source-build and patch subjects

The existing `native_toolchain` subject binds seven executable byte sets and
intentionally leaves source/build provenance unattested. Keep it for byte-level
compatibility; introduce a stronger, separate `postgres-build` subject.

Deliverables:

- Go-owned `subject build|inspect|verify|bundle` commands;
- exact repository URL/commit, dirty-state rejection, ordered patch bytes and
  digest, configure arguments, compiler/linker/tool versions, build/test logs,
  installed `bin/lib/share` inventory, and runtime file digests;
- a local self-recorded provenance class and a separately signed protected-CI
  class;
- A/B subject binding to two verified build capsules with one predeclared
  source/build dimension.

Exit gate:

- source, patch, configure, compiler, library, share, or installed-file
  transplant fails verification;
- the exact installed tree is revalidated before and after every arm;
- matching byte identities and server-version mismatch are rejected;
- claims remain limited to the recorded builds and environment, not universal
  patch causality.

## M6 — workload, version, and topology depth

New packs enter as correctness experiments and descriptive operation
benchmarks first. Performance decisions require a separately qualified paired
protocol.

Priority order:

1. autovacuum lifecycle and bloat under controlled churn;
2. temporary spill, parallel query, and JIT with result checksums and retained
   JSON plan evidence outside the measurement window;
3. WAL/checkpoint saturation with version-aware I/O/checkpointer collectors;
4. PgBouncer with separate server, driver, and pooler resource budgets;
5. physical-replication apply/replay and lag evidence;
6. real `pg_upgrade`, distinct from dump/restore;
7. stable-major native/runtime cells derived from real hardware evidence.

Every pack requires deterministic small smoke, terminal failure evidence,
owned cleanup, major-version applicability, a typed result schema, portable
bundle, and a tamper matrix. Podman follows only after Docker contract parity.

## M7 — pgdrill consumer and recovery lineage

The workbench side exports a versioned baseline, reviewed read-only predicate,
and optional signed source-bundle reference. It never receives provider
credentials or executes a restore.

The pgdrill side must:

- import and independently validate the baseline schema;
- revalidate the predicate and execute it only inside the isolated recovered
  target, with a restricted role, read-only transaction, statement timeout,
  and exact one-boolean-row result;
- bind the baseline check to pgdrill's selected backup, restore lifecycle,
  recovery target, probes, cleanup, and report;
- reject benchmark artifacts and any credential or executable content crossing
  the bridge.

End-to-end gate:

```text
workbench state -> backup -> later mutation -> isolated restore
                -> baseline predicate -> pgdrill report -> cleanup
```

The report proves only that recorded drill. Workbench evidence never implies
backup validity, PITR, RPO, RTO, SLA, or universal recoverability.

## M8 — trusted portable execution

Remote execution starts only after local evidence, A/A, noise, and collector
contracts are stable.

Sequence:

1. signed/OCI scenario packs with an explicit SQL-versus-trusted-shell
   capability declaration and trust policy;
2. immutable `benchmark job-create` capsule;
3. single-use `job-execute` worker with a private workspace and owned target;
4. signed result capsule and offline `job-verify`;
5. only then a scheduler, RBAC, and optional disposable Kubernetes/CNPG
   backend.

Baseline and candidate must run fresh on the same qualified worker population.
Results from different host populations are not promoted into a comparison.
No remote option may attach to an arbitrary production database.

## M9 — v1 product gate

v1 is an evidence and adoption milestone, not the sum of M2–M8 features. It
requires one immutable candidate with:

- all local, publication, draft/public artifact, compatibility, signature,
  provenance, and durable-evidence gates complete;
- two bounded external users;
- one non-maintainer-authored or modified scenario created without maintainer
  shell access and independently verified from its bundle;
- a signed critical review with no open or accepted critical security,
  data-loss, portability, or evidence-integrity finding;
- documentation and claims inside the assurance boundary.

Features from later milestones are not v1 blockers unless the release advertises
them as supported.

## Permanent exclusions

- no TPC compliance or use of official TPC metric names without the required
  specification and audit;
- no cross-system leaderboard or aggregate performance score;
- no universal tuning recommendation from one recorded environment;
- no production-target execution path;
- no promotion of ordinary series, imports, operation results, or unsigned host
  snapshots into causal decisions;
- no recovery claim from a workbench baseline;
- no UI, SaaS control plane, or Kubernetes scheduler before evidence and pilot
  demand justifies them.

## Immediate implementation slice

The current M1.2b tranche establishes the attachment substrate:

1. completed: a closed adapter registry instead of a generic user-authored
   `passed` flag;
2. completed: one pinned parse/hash snapshot for the predecessor and evidence
   record, strict v3 lineage with v2-to-v3 migration, canonical adjacent
   revision names, inode-pinned descriptor-relative publication, and exclusive
   copy-on-write semantics;
3. completed: the first candidate-bound pass-only adapter for the draft
   external-driver qualification, including its typed workflow producer;
4. completed: independently separated recorded, readiness, and effective
   authorization decisions; legacy v1/v2 `GO` remains readable but is not
   grandfathered when its evidence lacks persisted trust metadata, and one
   passed current record leaves the release `NO-GO`;
5. completed: candidate-bound draft/public asset-authenticity summaries and
   adapters plus a self-contained publication record emitted only by the fresh
   read-only public verifier; all three outcomes remain operator-attested and
   non-authorizing;
6. completed: typed source/draft/published compatibility and aggregate-attempt
   records are sealed post-draft over exact provider artifact identities; the
   second aggregate record hash-binds the first. All remain operator-attested
   and non-authorizing;
7. next: preventive controls require a separate atomic three-control adapter
   over a final live recheck. Current raw directories, workflow success,
   pre-draft control snapshots, or asset inventories alone are not positive
   evidence;
8. exit: finish the remaining adapters, full fault/race/standalone matrix, and
   only then mark M1.2 complete.

M1.3 typed pilot/critical-review readers and M1.4 relocated closed bundles then
complete the control plane. M2.1 A/A calibration starts only after the M1 exit
gate can retain and independently reverify its own release/adoption state.

## Method references

- PostgreSQL `pgbench`: <https://www.postgresql.org/docs/current/pgbench.html>
- PostgreSQL statistics views: <https://www.postgresql.org/docs/current/monitoring-stats.html>
- PostgreSQL runtime statistics overhead: <https://www.postgresql.org/docs/current/runtime-config-statistics.html>
- BenchBase: <https://github.com/cmu-db/benchbase>
- HammerDB result-comparison boundary: <https://www.hammerdb.com/docs/ch03s04.html>
- Kalibera and Jones, effect-size confidence intervals:
  <https://www.cs.kent.ac.uk/pubs/2012/3233/>
