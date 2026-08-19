# Roadmap

The exact `v0.2.7` candidate and its v1 completion contract are the current
release track. The ordered post-v0.2 implementation plan is maintained in
[post-v0.2-roadmap.md](post-v0.2-roadmap.md). Release evidence is bound to one
full commit; any later source or documentation byte creates a new candidate
and requires the exact-candidate gates again.

The project direction is a portable PostgreSQL experiment engine: a scenario is
planned, executed against an isolated disposable runtime, and produces a
versioned, independently verifiable evidence artifact. It is not a production
database operator, a benchmark leaderboard, or a recovery-assurance tool.

## v0.2 platform completion

The v0.2 candidate closes the architectural gaps that made v0.1 useful only
from a source checkout:

- `pgworkbench experiment run` owns the top-level execution result contract;
- `PGWORKBENCH_RUNTIME=docker|native` selects Docker Compose or an isolated
  host PostgreSQL cluster created with `initdb`/`pg_ctl`;
- the native lifecycle helper supports `single` and `source-tree`, while the
  first-class native experiment runner and release compatibility cells support
  `single` only and never attach to an arbitrary external PostgreSQL server;
- once its immutable run directory is created, every execution reaches a
  terminal verdict, including setup and hook failures; fail-closed preflight
  rejection before that boundary creates no run;
- strict boolean SQL assertions fail unless the result is exactly one `t` row;
- run manifests identify the scenario pack, experiment spec digest, runtime,
  and artifact schema versions;
- passed runs require real metrics when collection was enabled; an early failed
  run may omit a sampler output that never started without inventing evidence;
- portable bundles verify after extraction at a different absolute path;
- release archives contain the binary and the complete built-in scenario pack,
  so they work without Git, Go, or the original checkout;
- scenario packs have a versioned manifest, deterministic content digest,
  validation, inspection, and safe export;
- release gates include source checks, Docker and native runtime checks,
  archive extraction, fresh-directory CLI smoke tests, artifact verification,
  privacy scans, deterministic checksums and archives, one SPDX SBOM per
  archive, an exact stable Go patch pin, a manifest binding every artifact to
  the exact commit, embedded Go toolchain, and pack, and
  signed provenance/attestations in the tag workflow.
- the machine-readable release-platform ledger separates runtime-gated
  Linux/amd64 and Darwin/arm64 archives from compile/package-only Darwin/amd64
  and Linux/arm64 outputs, preventing cross-compilation from becoming a runtime
  support claim.

The implementation is complete only when the exact candidate passes the gates
in [v1-completion-contract.md](v1-completion-contract.md). Code present in a
working tree is not release evidence.

## v0.2 authoring and compatibility foundation

The local contracts are now implemented in the candidate:

1. `pack init` creates a complete executable third-party starter.
2. Declarative SQL assertions and explicitly trusted host-shell hooks have
   different fail-closed capabilities.
3. `engine_constraint` is enforced with strict SemVer migration diagnostics.
4. Machine-readable evidence records runtime OS/architecture and the observed
   PostgreSQL version, while the compatibility ledger declares exact candidate
   cells and required gates without claiming they passed.
5. The release-first authoring tutorial ends with a relocated verified bundle.

What remains is execution evidence rather than another local feature: exercise
all seven declared candidate cells from downloaded draft artifacts before
publication, repeat the same seven cells from public immutable artifacts after
publication, and attach both sets of outputs to the exact candidate. The
declaration is not qualified if either set is absent.

## Performance-regression laboratory

The benchmark track extends the experiment engine without turning it into a
load-generator clone. Its methodology and assurance boundary are specified in
[benchmarking.md](benchmarking.md). The current candidate contains a usable,
unqualified local `pgbench` foundation plus a bounded counterbalanced A/B
decision path. The older compact experiment-run comparison is not statistical
benchmark evidence, and code in the working tree is not release evidence.

### Implemented foundation

- versioned JSON contracts for plans, pgbench summary/log normalization,
  PostgreSQL sampler observations, trials, phase timelines, environments,
  series, histories, campaigns, descriptive imports, portable bundles,
  standalone host qualification, and counterbalanced A/B evidence;
- Docker and isolated-native execution for the single-node `pgbench` path,
  with separate warm-up and measured invocations;
- atomic initial publication for ordinary series, counterbalanced A/B, and
  campaigns; exact retained/revalidated `pgworkbench` bytes, native seven-tool
  snapshots, measured endpoint evidence, and actual Docker driver/target image
  IDs segment each environment population;
- strict final-summary parsing for PostgreSQL 15-18 and current PostgreSQL 19
  development output fixtures;
- plain per-transaction log normalization, including latency distributions and,
  when declared by the invocation, schedule lag and retry fields, plus retained
  failure and skip counts;
- independent-trial mean, median, sample deviation, CV, MAD, robust CV, extrema,
  and fail-closed maximum-CV handling without automatic outlier removal;
- retained protocol-invalid attempts: they are excluded from statistics, while
  a series may still pass after reaching its declared minimum valid-trial count;
- portable series verification and deterministic bundles containing the series
  and all linked experiment runs;
- a dedicated A/B closure requirement for `native_toolchain` arms, preventing a
  generic series bundle from dropping its arm-level executable snapshot;
- a permanently descriptive independent-series comparison and deterministic
  bootstrap analysis; every artifact produced by that path remains
  `inconclusive` and cannot yield an improved, regressed, equivalent, or
  no-regression verdict;
- standalone unsigned host inspection/verification, deterministic paired AB/BA
  cluster-bootstrap analysis, and a counterbalanced producer/verifier that
  binds fresh series, exact order, complete policy, and qualification bookends;

The smoke class is emitted as `unqualified-local-smoke`; measurement-class
calibration is emitted as `unqualified-local-measurement`. The supplied
60-second packs are parser, repetition, and variance calibration examples, not
qualified defaults.

### P0 — protocol and evidence contract

- **Implemented:** retained typed plan/input capsules, protocol and
  comparison-key digests, complete configured-pack inventories with
  pre/post-trial and finalization revalidation, normalized trial/series
  artifacts, portable verification, bounded runtime fingerprinting, and closed
  terminal statuses.
- **Implemented foundation:** first-class records for preflight, prepare,
  stabilize, pre-warmup control, warm-up, pre-measure control, measure,
  cool-down, validate, collect, and clean, with
  ordered RFC3339Nano timestamps, nanosecond durations, closed failure/skip
  transitions, raw-journal normalization, and deterministic digest.
- **Implemented hardening:** runner-owned deadlines and bounded process-group
  termination, timeout/signal evidence, collector coverage of the complete
  measurement window, and containment of the linked verdict interval by the
  trial lifecycle.
- **Implemented journal binding:** every v3 phase row is run/trial-bound,
  independently rebuilt from the linked primary journal, and checked against
  its byte-identical series mirror.
- **Implemented declaration identity:** explicit cache regime,
  statistics-reset policy/boundary, fixed v1 collector set and interval,
  declared overhead mode, client placement, resource budget, and exact
  phase-split seed semantics are validated and bound into both protocol
  identities. The runner configures the sampler interval, independently checks
  measurement-window coverage, records the observed maximum gap, and fails
  above the explicit two-interval bound; qualified A/B placement must match its
  bookend gate. The remaining v1 values are recorded declarations.
- **Implemented opt-in protocol v2 controls:** exact-relation PostgreSQL
  shared-buffer residency, runner-owned database/WAL statistics resets,
  exact-cadence collector duty-cycle evidence, and Docker single-container
  Linux cgroup-v2 CPU/memory enforcement. Reset/cache operations have exact
  control phases outside both workload windows, and typed evidence is sealed
  only after the complete lifecycle. Native v2 runs remain explicitly
  unbounded; OS page-cache control and portable cross-runtime enforcement are
  still outside the assurance boundary.
- **Implemented sampler/reset normalization v2:** the metrics digest binds the
  declared cadence and typed timing/reset raw evidence. Exact regular timing
  rows are correlated with `metrics.csv`; one terminal boundary row is explicit.
  A counter decrease is accepted only across the matching proven database/WAL
  reset timestamp and is retained as a two-segment observed lower bound.

### P1 — `pgbench` gold adapter

- **Implemented:** `pgbench` is the reference adapter; it preserves the final
  summary and plain transaction logs, normalizes throughput, latency, schedule
  lag, retries, skips, and failures where the declared log layout supports
  them, and ships parser fixtures.
- **Implemented component:** deterministic paired effect/cluster-bootstrap
  analysis over complete AB/BA units, with a predeclared threshold and closed
  decision taxonomy.
- **Implemented candidate:** one counterbalanced A/B scheduler and independent
  verifier, with exact order, distinct populations, complete fixed policy, and
  before/after qualification bookends. The two independent-series comparison
  path and `--subject` label cannot provide this design; see
  [benchmark-ab.md](benchmark-ab.md).
- **Implemented:** counterbalanced protocol v3 derives the exact assigned-GUC
  union from both configuration snapshots, captures only those effective
  `pg_settings` rows per trial, independently reparses the raw source, and
  requires within-arm stability, one server version, no pending restart, and a
  real cross-arm value/unit difference for `pg_config` before any performance
  verdict. Its separate `native_toolchain` mode binds two byte-distinct
  seven-file executable snapshots, requires matching observed versions for
  all seven tools, one runtime server version across arms, and per-arm
  stability, and does not invent an
  unrelated cross-arm GUC requirement.
- **Implemented:** native executable-byte-set AB/BA snapshots and rehashes
  `postgres`, `initdb`, `pg_ctl`, `pg_isready`, `createdb`, `psql`, and
  `pgbench`; arm-specific bindirs cannot be overridden by ambient environment.
  Adjacent installation/system dependencies, source commit/build provenance,
  and source-patch causality remain explicitly outside the claim.
- **Implemented candidate:** deterministic A/B bundle inventory, full
  transitive-closure capture, and relocated inventory verification.
- **Implemented:** complete synthetic A/B producer, deterministic archive,
  clean relocated verification, and tamper gates for protocol, series,
  qualification, block, inventory, path, duplicate, ordering, and symlink
  corruption.
- **Implemented:** the versioned PostgreSQL sampler identity, interval, and
  complete measurement-window coverage are protocol-bound. Passed trials also
  contain an independently re-derived measure-scoped summary of lossless
  `pg_stat_database`/`pg_stat_wal` deltas and session/lock gauges; malformed,
  reset, drifting-database, or non-monotonic evidence fails closed.
- **Deferred beyond the candidate boundary:** additional version-aware
  PostgreSQL and host collectors require their own phase windows, reset
  semantics, and separately measured overhead before they can be advertised.

### P2 — workload breadth

- **Implemented pgbench breadth:** fixed closed-loop saturation points at 1,
  4, 16, and 64 clients; a rate-limited latency/SLO probe with a typed
  exceeded-limit budget; digest-bound per-transaction connection churn; and a
  custom WAL/explicit-checkpoint workload with default versus deliberately
  fsync-heavy config subjects. These remain unqualified local study templates.
- **Implemented descriptive foundation:** immutable offline imports include
  strict pinned parsers for the real BenchBase `33c0047` summary and HammerDB
  v6.0 `hammerdb-job-report-v1` layouts, plus sysbench 1.0 console summaries and
  generic mapped structured JSON. Raw bytes/digests are retained; only generic
  HammerDB/BenchBase values require explicit typed JSON Pointers. Pinned reports
  retain driver commit identity and honest incomplete-error/timing bases. All imports remain outside the
  pgbench series/AB decision path. Relocated directories verify, and a
  deterministic inventory-bound import bundle independently re-normalizes the
  retained input after extraction.
- **Implemented operation foundation:** strict JSON operation specs repeat exact
  ordinary experiment runs, bind complete linked-run tree digests and precise
  standardized result bytes, recompute robust/classical statistics and CV, and
  produce deterministic relocated bundles. The runner also uses a clean,
  runner-owned child environment with `.env.example`, rejects runtime/output
  roots from a 1,024-file/64-MiB input closure, snapshots and revalidates the
  engine binary, and for native runs snapshots and revalidates the exact seven
  PostgreSQL executable bytes. Bundle verification derives the canonical
  extracted root and binds `series_ref` to the directory being verified.
  These are unsigned internal-integrity controls, not build provenance, host
  attestation, or publisher authentication. Native/Docker packs cover both
  massive-DML offline bulk-load index strategies and bracketed manual vacuum
  after fixed churn. Docker packs additionally cover logical-marker convergence
  and multi-version dump/restore. Their measurements remain deliberately
  narrow: polling-inflated convergence upper bound and complete linked-run wall
  time, not pure apply latency, physical replication, `pg_upgrade`, recovery,
  RTO, or SLA. This path is permanently descriptive and cannot enter pgbench
  comparison or AB/BA decisions.
- **Implemented bounded external execution:** the pinned BenchBase `33c0047`,
  HammerDB `v6.0`, and sysbench `1.0.20` records have native, fixed-argv,
  no-shell execution
  envelopes. Contract v2 stages an adapter-selected runtime closure, retains
  its normalized path/digest/size/mode inventory and tree digest, and
  independently reconstructs that closure during relocated verification. It
  covers BenchBase's manifest-linked JARs plus `config/plugin.xml`, the exact
  HammerDB launcher, and sysbench's executable plus selected/common Lua files.
  The artifacts also retain exact config, stdout/stderr and result bytes, the
  driver lock, a closed directory inventory, and a nested strict import that is
  independently re-derived. Password material exists only in an ephemeral
  process input and is rejected from retained output. These are
  descriptive normalized single trials, not pgbench series or decision input.
  All three adapters require an integrity-bound external-disposable-target
  acknowledgement and accept only exact loopback, non-system database targets;
  the assertion is not ownership or database-identity proof.
  HammerDB accepts only closed PostgreSQL TPROC-C/TPROC-H execute-only configs,
  generates Tcl internally, binds the exact `vurun` job id to one saved public
  report, and never claims complete errors or TPC compliance. Runtime-closure
  integrity does not attest source-to-binary provenance, the JRE/system dynamic
  libraries, host identity, or dataset preparation.
- **Implemented hosted release-smoke contract:** the protected
  `draft-external-drivers` job runs on GitHub `ubuntu-24.04`, acquires exact
  pinned inputs, builds curated 28/1/3-file runtime roots, creates an owned
  loopback PostgreSQL 16 cluster, prepares all three datasets, and locally
  verifies the real adapters. Success and failure uploads are metadata-only
  allowlists; all upstream runtime, generated-script, build, execution, and
  database bytes are deleted on every outcome. This closes the workflow and
  offline fail-closed implementation, not the live-release gate: a successful
  protected tag run for the exact candidate is still required. The 20W/4VU
  HammerDB release cell is compatibility-only; the existing 100W/32VU config
  stays manual and performance-unqualified.
- extend bounded executable packs only where new evidence demands broader
  bloat/autovacuum behavior, temporary/parallel-query work, physical or deeper
  replication protocols, and `pg_upgrade`/recovery-specific upgrade protocols.
  PgBouncer pgbench, native executable-byte-set A/B, all five operation packs,
  and guarded external-driver envelopes are implemented; experiment-only
  profiles outside those contracts are not benchmark evidence.

### P3 — bounded evidence history and campaigns

- **Implemented:** pinned sysbench native Lua execution envelope plus strict
  console-summary normalization; v2 retains the selected workload and required
  common Lua closure while keeping source-to-binary and host-library provenance
  explicitly unattested;
- **Implemented:** compare compatible immutable benchmark series over time in
  a descriptive history, preserving chronological intervals, full transitive
  evidence, deterministic bundles, a canonical environment-population digest
  boundary that normalizes only the native snapshot's run-specific location,
  and the no-dedicated-host claim;
- **Implemented:** predeclare and execute an ordered local benchmark campaign,
  retain failed/unavailable rows, verify every available series independently,
  and bundle its transitive closure without inventing an aggregate score or
  cross-spec verdict;
- keep remote scheduling/execution deferred until the local evidence contract
  and host qualification gates are proven.

No phase adds TPC compliance claims, a cross-system leaderboard, or a composite
performance score. Native and Docker runs remain different performance
populations even when both execute the same scenario successfully.

## v1 product gate

v1 is a release and adoption milestone, not a feature-count milestone. It
requires all of the following on one immutable candidate:

- two consecutive aggregate candidate-gate passes;
- signed archives, checksums, SBOM, and provenance for all advertised targets;
- an active `refs/tags/v*` ruleset that prohibits updates and deletions with no
  exclusions, an administrator-reviewed bypass record, and immutable releases
  enabled before publication;
- independent clean download and verification of all 16 draft assets followed
  by all seven compatibility cells from those draft artifacts;
- a protected GitHub-hosted `ubuntu-24.04` job acquires the six pinned inputs,
  creates fresh disposable loopback PostgreSQL 16 datasets, and locally verifies
  all three v2 runtime-closure artifacts before uploading metadata only;
  downstream verification binds that metadata and candidate registry without
  conveying upstream runtime bytes, and any missing capacity, preparation,
  execution, cleanup, or metadata leaves the draft unpublished;
- independent clean download, release-attestation verification, and
  verification of all 16 public immutable assets followed by the same seven
  compatibility cells from those published artifacts;
- at least two bounded external design-partner runs from the authoring guide;
- one maintainer-independent reproduction from pack to verified bundle;
- no unresolved critical security, data-loss, portability, or evidence-integrity
  findings;
- documentation and claims constrained by the assurance boundary.

External pilots, signatures, publication, and independent reproduction cannot
be completed by a local source edit. They stay visibly open until evidence is
attached to the exact candidate through the durable release evidence index in
[release-evidence.md](release-evidence.md). A public release whose public-asset
or published-compatibility gate fails exists, but remains `NO-GO` for v1 and
adoption claims.

## pgdrill relationship

`pgdrill` remains a separate product. Workbench creates reproducible PostgreSQL
states and experiment evidence; pgdrill exercises an existing backup provider
and produces recovery-assurance evidence. The implemented bridge exports only
a versioned scenario identity, immutable ordinary-experiment baseline
provenance, and an optional reviewed read-only SQL predicate. It refuses
benchmark runs and does not pass credentials, arbitrary shell, provider
lifecycle, or workbench benchmark claims into pgdrill. It creates no pgdrill
configuration or recovery report; see
[pgdrill-integration.md](pgdrill-integration.md).

## Deferred until evidence demands them

- remote executors, a scheduler, RBAC, TUI, web UI, and SaaS control plane;
- Podman and Kubernetes backends beyond a tested runtime interface;
- production-target execution;
- performance rankings or universal tuning recommendations;
- recovery/RTO/SLA claims, which belong to a recovery drill and its evidence.

## Permanent invariants

- Local and disposable by default; fail closed on a non-local target.
- Providers and runtimes stay at the edge of the core contracts.
- Smoke profiles remain bounded at `small` scale; benchmark protocols declare
  scale and resource budgets explicitly.
- A passed run proves only the recorded scenario on the recorded runtime.
- `unqualified-local-smoke` is never dedicated-host performance evidence.
- Native and Docker success does not imply performance equivalence.
- Ordinary independent-series comparison is permanently descriptive; only the
  complete counterbalanced AB/BA gate may issue a bounded performance decision.
- Host qualification is unsigned operator-recorded evidence, not host identity,
  dedicated ownership, current-state proof, or remote attestation.
- Artifact schemas are versioned and verified independently of absolute paths.
- Release and customer claims never exceed completed gates.
