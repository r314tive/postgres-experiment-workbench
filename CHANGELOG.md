# Changelog

## [Unreleased]

## [0.2.1] - 2026-08-18

### Fixed

- Isolated the native runtime fixture from qualification-injected ports so the
  aggregate release gate remains reproducible under its exact CI environment.

## [0.2.0] - 2026-08-13

- Added an exact-candidate matrix verifier that requires a predeclared row
  count, applies full live-run verification to every indexed TSV row, rejects
  path or symlink escape, binds retained experiment-spec bytes to the current
  checkout scenario pack, and requires both run evidence and the verifier
  executable to name one non-development version and full commit. The release
  runbook now uses a scrubbed process environment in a fresh detached evidence
  worktree, a candidate-private home/cache and Docker context with one copied
  Compose plugin and explicit local socket, a fixed bootstrap path plus
  `.env.example`, and fail-closed checks for conflicting Docker resources.
- Pinned release and CI builds to the exact Go 1.26.5 patch toolchain, made
  candidate preflight reject toolchain drift and pack-visible untracked files,
  and bound the embedded toolchain across all four archives into the verified
  release manifest.
- Made ordinary benchmark initialization atomic, retained and revalidated the
  exact `pgworkbench` executable bytes, and bound that digest into each series
  environment population. Docker pgbench trials now record the local driver
  and target image IDs reported by Compose; histories reject mixed environment populations,
  and every native series retains its own canonical seven-executable snapshot.
  `native_toolchain` A/B verifies both its arm-level protocol binding and each
  linked series-local closure.
- Early failed benchmark series now verify their retained native toolchain
  directly from the protocol snapshot and may omit a sampler output that never
  started; passed metrics-enabled runs still require real samples.
- Made counterbalanced A/B and campaign initialization atomic before the first
  runnable artifact is published. Release publication now also fails closed on
  a protected, real three-driver draft gate for pinned BenchBase, HammerDB, and
  sysbench execution evidence.
- Prepared utility-derived experiments now execute with a runner-owned
  workbench/protocol environment. The CLI projects the selected Docker/native
  backend configuration, primary disposable PostgreSQL endpoint, and utility
  sizing controls; the fixed Compose env file is never executed by the host
  shell, and trusted nested specs cannot downgrade that boundary. Conventional
  process-bootstrap values such as `HOME`, temporary directories, and `PATH`
  remain inherited and unattested; native PostgreSQL tool selection therefore
  requires an explicit bindir or installation directory.
- Pre-run `experiment run --json` validation failures no longer serialize an
  uninitialized Go result as the versioned experiment-result contract.
- Bound experiment planning, validation, result identity, shell sourcing, and
  captured spec provenance to one runner-selected byte snapshot. The shell
  verifies the runner-owned digest before and after sourcing the read-only
  snapshot, so a concurrent replacement of the logical pack path cannot yield
  a passed run whose JSON result and manifest describe different specs.
- Replaced syntax-only schema parsing with a network-independent Draft 2020-12
  compile and validation gate. It resolves local cross-schema references,
  asserts formats, evaluates ECMA-compatible regular expressions, and exercises
  tracked plus positive/negative representative artifacts from `make check`.
- Added a fail-closed Go module/license inventory for the source-containing
  release pack, retained exact upstream license/attribution bytes, included
  `go.sum` in standalone archives, and expanded deterministic SPDX 2.3 output
  with exact package URLs, licenses, and correctly directed test-dependency
  relationships. Runtime module closure is independently derived from binary
  Go build information and is empty for the v0.2.0 CLI.

- Extended counterbalanced A/B to protocol/run v3 with a bounded native
  executable-byte-set subject: per-arm absolute bindirs, seven required
  executable digests, matching observed versions for all seven tools,
  one observed runtime server version across arms, identity-only portable
  snapshots, hostile-env resistance, per-trial pre/post byte revalidation, and
  independently verified bundle closure. Source/build provenance, adjacent
  installation files, system dependencies, and source-patch causality remain
  outside the claim.

- Bound the PostgreSQL 15-19 server major observed in pgbench's banner to the
  linked experiment fingerprint; re-derived ordinary summary latency from TPS,
  clients, counts, and printed precision; kept the raw-log relation one-sided
  where pgbench defines no lower gap bound; bound detailed-summary mode to the
  typed rate/latency-limit protocol; and made the contract-v2 owned sampler
  publish an atomic first-sample readiness directory token only after strict
  CSV validation.
  Samplers and background workloads stay inside the Go runner's single
  containment group;
  atomically created stop-token directories and still-unreaped Bash job handles
  replace raw-PID signals and detached cleanup watchdogs. The outer runner
  applies bounded TERM/KILL cleanup to residual descendants, confirms group
  disappearance in a separately bounded post-KILL window, and atomically
  replaces a transient passed shell verdict with a failed terminal artifact.
  Signal and status-probe errors and unconfirmed containment stay explicit.
  Experiment-run JSON/text output publishes `containment_status` as `confirmed`
  or `unconfirmed` whenever a cleanup signal was attempted.
  Runner SIGHUP/SIGINT/SIGTERM use the same whole-group cleanup, remain distinct
  from timeout evidence, and publish failed terminal artifacts with
  conventional 129/130/143 exit codes.
- Added the benchmark protocol foundation: first-class specs/plans, native and
  Docker pgbench warm-up/measurement invocations, strict PostgreSQL 15-18 and
  19devel final-summary parsing, normalized plain transaction-log latency and
  counter evidence, independent-trial statistics without automatic outlier
  removal, retained invalid attempts with minimum-valid aggregation, verified
  portable series/bundles, and fail-closed comparison. Ordinary series remain
  `unqualified-local` and cannot issue a performance verdict by themselves.
- Added first-class eleven-phase benchmark timeline artifacts, including
  isolated pre-warmup and pre-measure control boundaries, and a standalone,
  fail-closed host inspection/verification contract. Host observations remain
  unsigned and operator-recorded; their digests protect content integrity but
  do not attest host identity, dedicated ownership, or current state.
- Added runner-owned trial deadlines with process-group TERM/KILL escalation,
  timeout evidence, and verifiable terminal artifacts for early preflight and
  setup failures.
- Bound every configured pgbench scenario pack through a retained full-file
  inventory and revalidation before/after each trial and before finalization;
  persistent drift invalidates the entire series, while transient
  change-and-revert inside one trial remains outside the claim.
- Bound explicit cache/reset declarations, the fixed v1 collector set and
  interval, collector-overhead mode, client placement, resource budget, and
  distinct warm-up/measurement seed semantics into benchmark protocol and
  comparison identity. The runner configures the sampler interval and checks
  window coverage, while qualified A/B placement must match its bookend gate;
  exact cadence and the other declarations remain bounded operator assertions.
- Made ordinary independent-series comparison permanently descriptive, added
  deterministic paired AB/BA cluster-bootstrap analysis, and added the
  counterbalanced A/B scheduler, protocol/run/bundle schemas, reader, and
  independent verifier. Only that separate path, with complete fixed policy
  and before/after bookends, may enter the bounded performance-decision gate;
  deterministic A/B bundles capture and inventory both series and every linked
  experiment run for relocated verification.
- Added compatible-series descriptive histories and ordered heterogeneous
  benchmark campaigns, each with independently re-derived reports,
  deterministic transitive-closure bundles, relocation verification, and
  tamper gates. Neither path creates a composite score, winner, or causal
  verdict.
- Added fixed saturation points, an open-loop latency-limit/SLO probe,
  per-transaction connection churn, and WAL/explicit-checkpoint/fsync study
  packs. They remain unqualified local study templates rather than portable
  tuning or capacity defaults.
- Added strict descriptive offline imports for HammerDB 6, sysbench 1.0, and
  BenchBase, retaining exact source/mapping bytes and typed normalization, plus
  deterministic import bundles that re-normalize after relocation. Imported
  evidence is permanently excluded from pgbench comparisons and A/B decisions.
- Added a permanently descriptive operation-benchmark contract with exact
  repeats, strict result or linked-wall-clock normalization, robust/classical
  statistics, transitive bundles, and five executable packs: two bulk-load
  strategies, manual vacuum, logical-marker convergence, and multi-version
  dump/restore. Every operation artifact remains `decision_eligible=false`.
- Hardened operation execution with a clean runner-owned child environment and
  explicit `.env.example`, a recomputable input capsule that rejects private
  and runtime-output roots and is capped at 1,024 files/64 MiB, retained and
  per-trial revalidated `pgworkbench` bytes, and a seven-executable native
  PostgreSQL identity snapshot. Bundle verification now binds the canonical
  extracted series path to its closed inventory instead of accepting linked
  runs from another root; the unsigned inventory remains internal-integrity,
  not publisher-authentication, evidence.
- Added bounded native BenchBase `33c0047` and sysbench `1.0.20` execution
  envelopes with fixed no-shell argv, adapter-specific staged runtime closure,
  immutable retained inputs/results, strict nested normalization, process-group
  containment, and closed relocation/tamper verification. BenchBase retains its
  recursive manifest classpath and plugin, while sysbench retains its binary,
  workload, and common Lua file. They remain descriptive single trials.
- Added a bounded native HammerDB v6.0 PostgreSQL TPROC-C/TPROC-H execute-only
  envelope with a staged standalone launcher, closed configs,
  adapter-generated Tcl, ephemeral password handling, exact `vurun`
  job-id/report binding, strict nested normalization, closed relocation/tamper
  verification, and explicitly no TPC or complete upstream-error claim.
- Made every native external-driver execution require an integrity-bound
  disposable/non-production operator acknowledgement and an exact numeric
  loopback, non-system-database target. Hostnames are rejected to avoid
  resolver redirection; BenchBase JDBC targets are parsed fail-closed. There is
  no v1 remote override and no ownership or database-identity claim.
- Added measure-scoped PostgreSQL sampler normalization for PostgreSQL 15-19:
  lossless cumulative counter deltas, gauge summaries, exact source/coverage
  identity, reset detection, and independent re-derivation from linked
  `metrics.csv`.
- Added a guarded native PostgreSQL backend alongside Docker Compose, with a
  shared runtime lifecycle and host `pgbench`/SQL/profile workload adapters,
  plus native `pg_dump`, `pg_dumpall`, and isolated `pg_restore` round trips.
- Added `pgworkbench experiment run` with a structured result contract,
  scenario-pack/spec/runtime identity, and standalone binary discovery.
- Added the versioned scenario-pack manifest, deterministic digest, validation,
  inspection, safe export, and complete self-contained release archives.
- Added versioned run manifest/verdict schemas, terminal verdicts for early
  failures, strict boolean SQL assertions, optional metrics verification,
  portable run paths, captured utility provenance/results, deterministic bundle
  inventories, fail-closed suite bundles, and relocation tests.
- Added fail-closed trust markers and explicit execution logging for host-shell
  experiment/utility assertions while keeping declarative SQL hooks ungated.
- Added an explicit v1 completion contract, assurance boundary, pgdrill
  integration boundary, native runtime guide, and product-oriented roadmap.
- Added an immutable, unsigned pgdrill baseline-provenance bridge for verified
  ordinary experiment runs and complete bundles. It binds pack/spec/run/runtime
  identities and optional human-reviewed predicate input without creating a
  pgdrill configuration, executing recovery, or making backup/RPO/RTO claims.
- Added a declared compatibility ledger and source/draft/published CI gates for the
  exact Docker, native, topology, upgrade, PostgreSQL, OS, and architecture
  cells advertised by the candidate.
- Added deterministic four-platform archives, exact candidate preflight,
  per-archive SPDX 2.3 SBOMs, a tamper-evident release manifest, reproducibility
  checks, and tag-only provenance/SBOM attestations with clean-download
  verification.
- Classified all four release archive targets in the machine-readable
  compatibility ledger: Linux/amd64 and Darwin/arm64 are runtime-gated, while
  Darwin/amd64 and Linux/arm64 are explicitly compile/package-only and carry no
  runtime-support claim.
- Extended the extracted release-archive gate through a real isolated native
  experiment and native pgbench run with relocated bundle verification, so the
  supported standalone path is exercised without Docker, Git, or Go.

## v0.1.37 - 2026-07-20

Added domain experiment coverage:

- Added the `massive-dml` profile with deterministic scalable data, generated
  committed UPDATE/DELETE batches, procedure and queue alternatives,
  transaction caveats, diagnostics, assertions, and profile-local guidance.
- Added generated UPDATE/DELETE, procedure UPDATE/DELETE, queue UPDATE, and
  transaction-caveat workloads and experiments with SQL/error artifacts,
  metrics, snapshots, assertions, and verdicts.
- Added the `massive-dml-comparison` parity matrix and Docker-backed runtime
  coverage for the complete migrated standalone behavior set.
- Added offline bulk-load experiments comparing index maintenance during load
  with building secondary indexes afterward, preserving row, timing, WAL, and
  size measurements as run artifacts.
- Added a partition detach/drop versus row DELETE experiment with identical
  source data, correctness assertions, timing/WAL measurements, and a focused
  three-repeat `massive-dml-strategy` matrix.
- Reworked the roadmap, release gate, profile documentation, and demo flow
  around a `v0.1.37` release candidate and an explicit post-release redirect and
  archive boundary for the standalone repository.
- Added a reproducible nine-run medium-size candidate demo record with local
  bulk-load and partition-removal measurements and explicit interpretation
  limits.

## v0.1.36 - 2026-06-09

Fixed test infrastructure reliability:

- Fixed experiment manifest parsing in [tests/experiments.sh] to correctly handle
  quoted `manifest.env` values (e.g. `profile="constraints"`) written by
  the Go state writer.

## v0.1.33 - 2026-06-05

Added platform features:

- Added utility-suite run artifact bundles with Markdown/JSON CLI output,
  Make targets, linked experiment-run inclusion, and tar.gz evidence archives.

## v0.1.32 - 2026-06-05

Added platform features:

- Added utility-suite run artifact discovery, display, verification, JSON
  output, Make targets, suite `result.json` artifacts, and linked
  experiment-run integrity checks.

## v0.1.31 - 2026-06-05

Added platform features:

- Added `utility-suites/**/*.env` specs, `pgworkbench utility-suite`
  catalog/plan/run commands, Make targets, JSON output, suite summaries, and
  starter native dump suite definitions.

## v0.1.30 - 2026-06-05

Added platform features:

- Added utility-test result contracts for expected output files, SQL
  assertions, shell assertions, and extra failure-scan paths, and mapped them
  into generated experiment runs.

## v0.1.29 - 2026-06-05

Added platform features:

- Added first-class `utility-tests/**/*.env` specs, `pgworkbench utility`
  catalog/plan/run commands, Make targets, JSON/expanded planning output, a
  bridge to the experiment runner, and native PostgreSQL utility smoke
  scenarios.

## v0.1.28 - 2026-06-05

Added platform features:

- Added `pgworkbench workload run` with structured workload execution results,
  plus `make workload-run-json` and a `workload-run-shell` compatibility target.

## v0.1.27 - 2026-06-05

Improved release checks:

- Added a Go test that keeps the shell metrics sampler CSV header synchronized
  with the Go metrics planning contract.

## v0.1.26 - 2026-06-05

Added platform features:

- Added structured background workload status through
  `pgworkbench workload bg status --json` and `make workload-status-json`.

## v0.1.25 - 2026-06-05

Added platform features:

- Added structured JSON output for run artifact verification through
  `pgworkbench run verify --json` and `make experiment-verify-json`.

## v0.1.24 - 2026-06-05

Added platform features:

- Added JSON metadata output for run artifact bundles through
  `pgworkbench run bundle --json` and `make run-bundle-json`.

## v0.1.23 - 2026-06-05

Changed platform interface:

- Added default `make workload-plan` and `make dataset-plan` targets while
  keeping the explicit `*-go` compatibility targets.

## v0.1.22 - 2026-06-05

Changed documentation:

- Refreshed public examples to prefer current default Make targets where Go is
  already the default implementation.

## v0.1.21 - 2026-06-05

Added platform features:

- Added `pgworkbench run bundle` and `make run-bundle` for portable tar.gz
  archives of local run artifacts.

## v0.1.20 - 2026-06-05

Added platform features:

- Added `--status` and `--limit` filters to `pgworkbench run list` plus Make
  variables for filtered run catalog views.

## v0.1.19 - 2026-06-05

Added platform features:

- Added `pgworkbench run list|show` with Markdown/JSON output for local run
  artifact discovery and summaries.

## v0.1.18 - 2026-06-05

Added platform features:

- Added `pgworkbench metrics plan` with Markdown/JSON output for the metrics
  sampler CSV contract.

## v0.1.17 - 2026-06-05

Changed platform behavior:

- Moved the run comparison Make target to Go `--raw` output while keeping an
  explicit shell compatibility target.

## v0.1.16 - 2026-06-05

Changed platform behavior:

- Moved run report, summary, and history Make targets to Go defaults while
  keeping explicit shell compatibility targets.

## v0.1.15 - 2026-06-05

Changed platform behavior:

- Moved `make matrix-plan` to Go raw output while preserving shell-compatible
  Markdown.

## v0.1.14 - 2026-06-05

Changed platform behavior:

- Moved experiment, matrix, and topology catalog Make targets to Go raw output
  while preserving shell-compatible list/show output.
- Moved diagnostic catalog Make targets to the Go CLI while keeping diagnostic
  execution in shell.

## v0.1.13 - 2026-06-05

Changed platform behavior:

- Moved workload and dataset catalog Make targets to Go raw output while
  preserving shell-compatible list/show output.

## v0.1.12 - 2026-06-05

Changed platform behavior:

- Moved profile catalog Make targets to the Go CLI while keeping the shell
  compatibility script.

## v0.1.11 - 2026-06-05

Added platform capabilities:

- JSON output for Go profile SQL plans.

## v0.1.10 - 2026-06-05

Changed licensing:

- Replaced the proprietary source-available license with Apache License 2.0.

## v0.1.9 - 2026-06-05

Changed licensing:

- Replaced MIT licensing with a proprietary source-available, all-rights-
  reserved license.

## v0.1.8 - 2026-06-05

Added platform capabilities:

- Read-only PostgreSQL diagnostics SQL catalog and runner for activity, locks,
  settings, table/index health, WAL, and replication state.

## v0.1.7 - 2026-06-04

Added platform capabilities:

- JSON output for Go workload and dataset plans.

## v0.1.6 - 2026-06-04

Added platform capabilities:

- JSON output for Go experiment plans and expanded experiment dry-runs.

## v0.1.5 - 2026-06-04

Added platform capabilities:

- Expanded Go experiment dry-run previews for topology, dataset, foreground
  workload, and background workloads.

## v0.1.4 - 2026-06-04

Added platform capabilities:

- Go dataset load plan renderer for no-Docker dataset preflight.

## v0.1.3 - 2026-06-04

Added platform capabilities:

- Go workload execution plan renderer for no-Docker workload preflight.

## v0.1.2 - 2026-06-04

Added platform capabilities:

- Go topology inspection command for no-Docker topology runtime preflight.
- Go topology live Compose state parser for started topologies.
- Go experiment matrix plan renderer with JSON output for external tooling.
- Go workload and dataset catalog list/show/validate commands.
- Go profile SQL plan renderer for no-Docker profile reset/run preflight.
- Dynamic CI runtime port assignment for Docker-backed topology tests.
- Runtime env override preservation for dataset and topology psql helpers.
- Topology readiness waits before topology-sensitive experiment assertions.
- Host-port readiness waits for topology-sensitive experiment assertions.
- Workload runner preservation for replica and logical subscriber port
  overrides.

## v0.1.1 - 2026-06-04

Added platform capabilities:

- Go patchset catalog, PostgreSQL source-check planning, and source-check
  artifact classification commands.
- SHA256 checksum files for release snapshots and GitHub Release assets.

## v0.1.0 - 2026-06-04

MVP baseline for the generic PostgreSQL experiment workbench.

Added platform capabilities:

- disposable PostgreSQL topologies for single-node, physical replica, logical
  replication, PgBouncer, and multi-version upgrade workflows;
- profile catalog metadata and validation;
- workload adapters for profile SQL, SQL files, `pgbench`, noisia, shell,
  Compose one-shots, PostgreSQL source checks, dump/restore, PgBouncer, and
  upgrade utilities;
- experiment orchestration with metrics, snapshots, background workloads,
  assertions, artifact scanning, repeat runs, matrices, comparisons, summaries,
  and history reports;
- Go CLI support for doctor checks, profile/spec validation, experiment plans,
  run artifact verification, run reports, state writing, failure scanning, and
  release snapshots;
- patchset catalog support for PostgreSQL source-check workloads;
- tag/manual release snapshot workflow for `pgworkbench` archives and GitHub
  Release publishing.

Added first real profiles:

- `locks`
- `vacuum-bloat`
- `indexes`
- `wal-pressure`
- `partitioning`
- `constraints`
- `jsonb`
- `logical-ddl`

Release gate:

- `make release-check` is the local pre-release gate.
- GitHub `check` runs `make check`, `make test`, and artifact scanning.
- PostgreSQL source builds remain opt-in through manual workflows and
  `PG_SOURCE_ACTION=run`.
