# PostgreSQL Experiment Workbench

Portable, local PostgreSQL experiment engine for utility testing, workload
generation, monitoring, and reproducible profile-based demos. Runs produce
versioned evidence that can be bundled and verified on another path.

This project is a generic platform. Domain-specific labs should live as
profiles or separate focused repositories.

## Core Shape

```text
scenario pack
-> Docker or isolated native PostgreSQL
-> profile setup SQL
-> optional workload/noise
-> monitoring
-> assertions + immutable evidence
-> repeatable teardown/reset
```

## Quick Start

```bash
make runtime-reset PGWORKBENCH_RUNTIME=docker
make profile-reset PROFILE=smoke PROFILE_SIZE=small
make monitor
make diagnostics-list
make diagnostics-run DIAGNOSTIC=activity
```

Docker is the default, but it is not required for the single-node path:

```bash
pgworkbench doctor --runtime native
pgworkbench experiment run --runtime native smoke

# From a source checkout:
PGWORKBENCH_RUNTIME=native make quickstart
```

Native mode creates and owns an isolated cluster under `.tmp/native/`; it does
not attach the experiment runner to an arbitrary PostgreSQL server. See
[docs/runtime-backends.md](docs/runtime-backends.md).

Pinned external load generators use a separate guarded native-process
envelope. They require an explicit disposable-target acknowledgement and accept
only a loopback, non-system database; that path does not inherit the managed
runtime's cluster-ownership claim.

## Install without Docker

The recommended standalone installation is a release archive. It contains the
`pgworkbench` binary and the complete built-in scenario pack; after extracting
it, keep the binary inside that directory so pack discovery works without a Git
checkout or Go toolchain. The release workflow package-verifies all four
archives. It declares native runtime qualification gates only for
`darwin/arm64` and `linux/amd64`; those platforms become runtime-supported only
after the exact candidate passes its draft and public compatibility cells. The
`darwin/amd64` and `linux/arm64` archives are compile/package-only outputs in
the current compatibility ledger and must not be treated as runtime-supported:

```bash
tar -xzf pgworkbench-<version>-<os>-<arch>.tar.gz
cd pgworkbench-<version>-<os>-<arch>
./pgworkbench version
./pgworkbench pack validate
# Run these only on a runtime-gated platform:
./pgworkbench doctor --runtime native
./pgworkbench benchmark run --runtime native --subject baseline pgbench/smoke
```

For a source build, run `make pgworkbench`. If the binary is moved away from
its pack, set `PGWORKBENCH_ROOT` to the extracted pack or checkout root. Native
execution requires PostgreSQL client/server binaries (`initdb`, `pg_ctl`,
`createdb`, `pg_isready`, and `psql`); benchmark execution additionally needs
`pgbench`. `PGWORKBENCH_NATIVE_BINDIR` may point at their common `bin`
directory. Docker remains useful for multi-service topologies, but is not a
requirement for the isolated single-node experiment, utility, and benchmark
paths.

## Benchmarks

The benchmark track is the implemented foundation of a reproducible PostgreSQL
performance-regression laboratory. It uses `pgbench` as the first reference
driver and keeps raw driver output, normalized summaries and transaction logs,
independent-trial statistics, a versioned protocol identity, an eleven-phase
timeline, a bounded runtime fingerprint, and fail-closed comparison output
separate from the older descriptive reports.

The protocol identity now also fixes declared cache/reset conditions, the v1
collector set and interval, collector-overhead mode, client placement,
resource budget, and distinct warm-up/measurement seed semantics. Fields that
the runtime cannot yet prove or enforce remain labeled declarations.

Every passed trial also embeds a measure-scoped PostgreSQL sampler summary:
lossless `pg_stat_database`/`pg_stat_wal` deltas, session/lock gauge summaries,
exact raw CSV identity, and boundary coverage. Verification recalculates it
from the linked `metrics.csv`; a reset, counter decrease, database drift, or
coverage gap invalidates the trial instead of silently producing a delta.

The benchmark layer uses closed, versioned JSON Schema contracts, including
counterbalanced A/B, descriptive history/campaign, normalized PostgreSQL
sampler, and offline-import evidence. Every portable bundle has an explicit
inventory and a relocated verification mode.

Ordinary benchmark series initialization is published atomically and retains
the exact engine binary. Native runs additionally snapshot the selected
PostgreSQL toolchain; Docker runs record the local driver and target image IDs
reported by Compose. These identities prevent accidental population mixing but
do not claim image-byte, build, or publisher provenance.

```bash
pgworkbench benchmark list
pgworkbench benchmark plan --json pgbench/smoke
pgworkbench benchmark run --runtime docker --subject baseline pgbench/smoke
pgworkbench benchmark run-show <benchmark-series>
pgworkbench benchmark run-verify <benchmark-series>
pgworkbench benchmark run-bundle <benchmark-series> generated/benchmark.tar.gz

# Non-TPS PostgreSQL operations use a distinct descriptive evidence contract.
pgworkbench benchmark operation list
pgworkbench benchmark operation run --runtime docker \
  massive-dml/offline-bulk-load-indexed
PGWORKBENCH_NATIVE_BINDIR=/absolute/postgresql/bin \
  pgworkbench benchmark operation run --runtime native \
  massive-dml/offline-bulk-load-indexed
pgworkbench benchmark operation verify <operation-series>
pgworkbench benchmark operation bundle \
  <operation-series> generated/operation-benchmark.tar.gz
# After extraction, require the inventory bound to this canonical series path:
pgworkbench benchmark operation verify --bundle \
  <extracted-root>/runs/operation-benchmarks/<series-id>

# Pinned external drivers run natively through a fixed, no-shell argv and
# remain descriptive single trials. See docs/benchmarking.md for config rules.
pgworkbench benchmark driver-run \
  --acknowledge-external-disposable-target \
  --driver sysbench-postgresql-1.0.20 \
  --runtime-root /opt/pgworkbench/sysbench-runtime \
  --binary /opt/pgworkbench/sysbench-runtime/bin/sysbench \
  --config configs/benchmark-drivers/sysbench-postgresql.json \
  --script /opt/pgworkbench/sysbench-runtime/share/sysbench/oltp_read_write.lua \
  --workload oltp_read_write/postgresql --timeout 20m \
  generated/sysbench-execution
pgworkbench benchmark driver-run-verify generated/sysbench-execution

# HammerDB v6.0 uses a closed execute-only config and an exact non-Tcl marker;
# create that one-line marker in private temporary state. The release helper
# does this automatically; pgworkbench generates/verifies Tcl and job reports.
marker_file="$(mktemp)"
chmod 0600 "$marker_file"
printf '%s\n' 'pgworkbench.hammerdb-v6-execute-only-template/v1' > "$marker_file"
PGWORKBENCH_DRIVER_PASSWORD='use-a-secret-source' \
pgworkbench benchmark driver-run \
  --acknowledge-external-disposable-target \
  --driver hammerdb-postgresql-6.0 \
  --runtime-root /opt/pgworkbench/hammerdb-runtime \
  --binary /opt/pgworkbench/hammerdb-runtime/hammerdbcli \
  --config configs/benchmark-drivers/hammerdb-v6-tprocc-postgresql.json \
  --script "$marker_file" \
  --workload tprocc/postgresql --timeout 20m \
  generated/hammerdb-execution
rm -f "$marker_file"

pgworkbench benchmark import sysbench1 --workload oltp_read_write/postgresql \
  sysbench.txt generated/sysbench-import
pgworkbench benchmark import hammerdb6 --manifest hammerdb-mapping.json \
  hammerdb-job-report.json generated/hammerdb-import
pgworkbench benchmark import hammerdb6report \
  hdb-job-id.json generated/hammerdb-pinned-import
pgworkbench benchmark import benchbase33c0047 \
  benchbase.summary.json generated/benchbase-pinned-import
pgworkbench benchmark import-verify generated/hammerdb-import
pgworkbench benchmark import-bundle \
  generated/hammerdb-import generated/hammerdb-import.tar.gz
# After extraction, require the import bundle inventory:
pgworkbench benchmark import-verify --bundle \
  <extracted-root>/imports/<artifact-digest>
# After extraction, verify the series with the bundle inventory required:
pgworkbench benchmark run-verify --bundle \
  <extracted-root>/runs/benchmarks/<series-id>

# The same single-node benchmark path works without Docker:
PGWORKBENCH_NATIVE_BINDIR=/path/to/postgresql/bin \
  pgworkbench benchmark run --runtime native --subject baseline pgbench/smoke

# A 60-second measurement-class calibration example:
pgworkbench benchmark run --runtime native --subject calibration pgbench/read-only

# Longer built-in study templates are explicit specs, not portable defaults:
pgworkbench benchmark plan pgbench/rate-limited-slo
pgworkbench benchmark plan pgbench/connection-churn
pgworkbench benchmark plan pgbench/pgbouncer/proxy-connection-churn
pgworkbench benchmark plan pgbench/wal-checkpoint-fsync
pgworkbench benchmark plan pgbench/saturation/c16

# PgBouncer is an explicit Docker-only target. Run direct and proxy rows as a
# descriptive campaign; target identity prevents treating them as one A/B key.
pgworkbench benchmark campaign-run --runtime docker \
  --campaign-id pgbouncer-connection-paths --subject local-calibration \
  pgbench/pgbouncer/direct-connection-churn \
  pgbench/pgbouncer/proxy-connection-churn

# Comparison is fail-closed for the current unqualified producer artifacts:
pgworkbench benchmark compare --json <baseline-series> <candidate-series>

# Record and independently check a bounded host snapshot. Its digests provide
# content integrity only; this is not host identity or attestation.
pgworkbench benchmark host-inspect --output host-qualification.json \
  --storage-path /path/to/postgres-data --storage-label postgres-data \
  --client-placement same-host
pgworkbench benchmark host-verify --json host-qualification.json

# The counterbalanced path requires the complete strict policy documented in
# docs/benchmark-ab.md, then supports independent verification and bundling:
pgworkbench benchmark ab-run [analysis-and-qualification-options] \
  <baseline-benchmark> <candidate-benchmark>
pgworkbench benchmark ab-verify <ab-run-id>
pgworkbench benchmark ab-bundle <ab-run-id> generated/benchmark-ab.tar.gz

# Native executable-byte-set comparison uses one identical benchmark protocol
# and two explicit PostgreSQL bindirs. The observed versions of all seven
# selected tools must match. Evidence binds seven executable files; it does not
# attest a source commit, build pipeline, adjacent files, or patch causality.
pgworkbench benchmark ab-run --runtime native \
  --subject-dimension native_toolchain \
  --baseline-native-bindir /absolute/postgres-a/bin \
  --candidate-native-bindir /absolute/postgres-b/bin \
  [analysis-and-qualification-options] \
  pgbench/source-patch pgbench/source-patch

# Run a predeclared ordered saturation campaign. Rows remain independent and
# descriptive; the campaign deliberately has no aggregate score or winner.
pgworkbench benchmark campaign-run --runtime native \
  --campaign-id saturation-local --subject host-calibration \
  pgbench/saturation/c01 pgbench/saturation/c04 \
  pgbench/saturation/c16 pgbench/saturation/c64
pgworkbench benchmark campaign-verify saturation-local
pgworkbench benchmark campaign-bundle \
  saturation-local generated/saturation-local.tar.gz

# Export ordinary experiment baseline identity for a future pgdrill consumer.
# This is provenance only, never recovery evidence.
pgworkbench bridge pgdrill export --bundle \
  <extracted-experiment-run> generated/pgdrill-baseline.json
pgworkbench bridge pgdrill verify --source <extracted-experiment-run> \
  generated/pgdrill-baseline.json
```

The one-trial smoke is classified `unqualified-local-smoke`; it exercises
execution and parsing, while `benchmark run-verify` separately verifies the
series artifact. Ordinary series record `qualification=unqualified-local`, so
even a valid standalone measurement series cannot produce a performance
verdict.

The bundled 60-second measurement packs are calibration examples, not qualified
defaults. Native and Docker results are distinct populations, and `--subject`
is only a label rather than an A/B configuration override. Ordinary
independent-series comparison is permanently descriptive. The candidate's
separate `benchmark ab-run` path may issue a bounded decision only after its
counterbalanced `AB/BA` schedule, complete fixed qualification policy, and
before/after bookends all pass. Its v3 decision contract independently
re-parses the exact assigned settings from every trial. `pg_config` requires a
real applied cross-arm value/unit difference; `native_toolchain` instead binds
two byte-distinct seven-file executable snapshots, matching observed versions
for every selected tool, one cross-arm runtime server version, and per-arm
stability. Source
and build provenance, adjacent `share`/`lib` files, system dependencies, and
source-patch causality remain outside the claim. See
[docs/benchmarking.md](docs/benchmarking.md) and
[docs/evidence-format.md](docs/evidence-format.md). The decision contract and
CLI are specified separately in [docs/benchmark-ab.md](docs/benchmark-ab.md).
Compatible immutable series can also be assembled into a portable descriptive
history; heterogeneous specs can be predeclared and run sequentially as a
campaign. Neither artifact is a comparison design, and neither may issue a
causal or cross-spec performance verdict.

Declared candidate cells and their still-required gates are explicit in
[docs/compatibility-matrix.md](docs/compatibility-matrix.md); a listed cell is
not itself proof that its gate passed. The same machine-readable ledger marks
Linux/amd64 and Darwin/arm64 archives `runtime-gated`, while Darwin/amd64 and
Linux/arm64 are explicitly `compile-package-only`; four built archives are not
a four-platform runtime-support claim.

For the experiment-layer smoke flow:

```bash
make doctor
make quickstart-plan
make quickstart
```

The full transcript lives in [docs/quickstart.md](docs/quickstart.md).

Run one of the starter experiment profiles:

```bash
make profile-reset PROFILE=locks PROFILE_SIZE=small
make profile-reset PROFILE=vacuum-bloat PROFILE_SIZE=small
make profile-reset PROFILE=indexes PROFILE_SIZE=small
make profile-reset PROFILE=wal-pressure PROFILE_SIZE=small
make profile-reset PROFILE=partitioning PROFILE_SIZE=small
make profile-reset PROFILE=temp-spill PROFILE_SIZE=small
make profile-reset PROFILE=massive-dml PROFILE_SIZE=small
```

Open psql:

```bash
make psql
```

Connection URL:

```text
postgres://postgres:postgres@127.0.0.1:55433/pg_experiment_workbench?sslmode=disable
```

## Profiles

Profiles live under:

```text
profiles/<profile-name>/
```

Expected SQL files:

```text
profiles/<profile-name>/sql/00_setup.sql
profiles/<profile-name>/sql/10_run.sql
```

Run:

```bash
make profile-list
make profile-show PROFILE=locks
make profile-plan PROFILE=locks
make profile-plan-json PROFILE=locks
make profile-reset PROFILE=smoke PROFILE_SIZE=small
```

Run a specific profile SQL file:

```bash
make profile-plan PROFILE=locks PROFILE_PLAN_SQL=30_diagnostics.sql
make profile-run PROFILE=locks WORKLOAD_SQL=30_diagnostics.sql
```

Profiles should be self-contained and safe to reset in a local disposable
database.

Profile authoring guidance lives in [docs/profile-authoring.md](docs/profile-authoring.md).

## Workloads

The generic workload runner can execute SQL, profile SQL, `pgbench`, noisia,
PostgreSQL source checks, host shell commands, or arbitrary Docker images:

```bash
make workload-list
make workload-show WORKLOAD_SPEC=pgbench/tiny
make workload-plan WORKLOAD_SPEC=pgbench/tiny
make workload-plan-json WORKLOAD_SPEC=pgbench/tiny
make workload-run WORKLOAD_SPEC=pgbench/tiny
make workload-run-json WORKLOAD_SPEC=pgbench/tiny
make workload-run WORKLOAD_SPEC=compose/pg-isready
make source-plan SOURCE_WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan make workload-run WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan PG_PATCHSET=chaos/master make workload-run WORKLOAD_SPEC=pg-source/check
make source-classify SOURCE_CHECK_PATH=generated/pg-source/<run-id>
make workload-run WORKLOAD_SPEC=topology/native-pg-upgrade
```

Workload platform details live in [docs/workload-platform.md](docs/workload-platform.md).
Utility workflow examples live in
[docs/utility-workflows.md](docs/utility-workflows.md).
Read-only diagnostic SQL snippets live in [docs/diagnostics.md](docs/diagnostics.md).

## Utility Tests

Utility tests are reusable tool scenarios for `pg_dump`, `pg_restore`, backup
tools, data checkers, fuzzers, and other PostgreSQL-adjacent utilities. They
bind prepared state, optional background pressure, metrics, and a foreground
workload into one reviewable plan:

```bash
make utility-list
make utility-show UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan-json UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan-expanded UTILITY_TEST_SPEC=pg-dumpall/wal-pressure
make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
make utility-run-json UTILITY_TEST_SPEC=pg-dump/smoke
PGWORKBENCH_RUNTIME=native UTILITY_TEST_RUN_ID=native-pgdump make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
go run ./cmd/pgworkbench utility run --runtime native --run-id native-pgdump pg-dump/smoke
make utility-suite-list
make utility-suite-plan UTILITY_SUITE=native-dump
make utility-suite-plan-json UTILITY_SUITE=native-dump
make utility-suite-run UTILITY_SUITE=native-dump
make utility-suite-run-list
make utility-suite-run-show UTILITY_SUITE_RUN=<suite-run-id>
make utility-suite-run-bundle UTILITY_SUITE_RUN=<suite-run-id> UTILITY_SUITE_BUNDLE_OUT=generated/suite.tar.gz
make utility-suite-run-verify UTILITY_SUITE_RUN=<suite-run-id>
```

Specs live under `utility-tests/**/*.env`; executable utility actions remain
normal workload specs under `workloads/`. `utility run` translates the
utility-test spec into a temporary experiment spec under `.tmp/` and executes it
through the existing experiment runner, so run artifacts still land under
`runs/<run-id>/`. Utility tests can declare expected output files, SQL
assertions, shell assertions, and extra failure-scan paths so tool checks have a
stable result contract.

Utility suites live under `utility-suites/**/*.env` and batch utility tests
across profile sizes and repeats. Suite artifacts are written under
`runs/utility-suites/<suite-run-id>/` with `runs.tsv`, `result.json`,
`summary.md`, driver logs, and links back to individual experiment run
artifacts. Suite bundles archive both the suite artifact and linked experiment
runs for portable review.

## Topologies

Runtime topologies describe the PostgreSQL shape an experiment needs:

```bash
make topology-list
make topology-inspect TOPOLOGY=primary-replica
make topology-up TOPOLOGY=primary-replica
make topology-ps TOPOLOGY=primary-replica
make topology-status TOPOLOGY=primary-replica
```

Implemented topologies:

- `single`: one disposable PostgreSQL container.
- `primary-replica`: physical streaming replica with a local replication slot.
- `logical-replication`: publisher plus independent logical subscriber.
- `pgbouncer`: PostgreSQL plus PgBouncer pooler.
- `multi-version-upgrade`: old and new PostgreSQL versions for upgrade tests.

## Experiments

Experiments orchestrate datasets, profiles, workloads, background pressure,
metrics, snapshots, assertions, scans, and verdicts into `runs/<run-id>/`:

```bash
make experiment-list
make experiment-plan EXPERIMENT_SPEC=smoke
make experiment-plan-json EXPERIMENT_SPEC=smoke
make experiment-plan-expanded EXPERIMENT_SPEC=smoke
make experiment-plan-expanded-json EXPERIMENT_SPEC=smoke
make experiment-run EXPERIMENT_SPEC=smoke
make experiment-run EXPERIMENT_SPEC=constraints-validation
make experiment-run EXPERIMENT_SPEC=jsonb-indexing
make experiment-run EXPERIMENT_SPEC=locks-under-contention
make experiment-run EXPERIMENT_SPEC=replica-readonly
make experiment-run EXPERIMENT_SPEC=logical-replication
make experiment-run EXPERIMENT_SPEC=logical-ddl
make experiment-run EXPERIMENT_SPEC=pgbouncer-smoke
make experiment-run EXPERIMENT_SPEC=multi-version-upgrade-smoke
make experiment-run EXPERIMENT_SPEC=temp-spill
make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-update
make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-delete
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-update
make experiment-run EXPERIMENT_SPEC=massive-dml/queue-update
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-delete
make experiment-run EXPERIMENT_SPEC=massive-dml/transaction-caveats
make experiment-run EXPERIMENT_SPEC=massive-dml/offline-bulk-load-indexed
make experiment-run EXPERIMENT_SPEC=massive-dml/offline-bulk-load-index-after
make experiment-run EXPERIMENT_SPEC=massive-dml/partition-drop-vs-delete
make run-list
make run-list RUN_STATUS=failed RUN_LIMIT=20
make run-show RUN_DIR=runs/<run-id>
make run-bundle RUN_DIR=runs/<run-id> RUN_BUNDLE_OUT=generated/run.tar.gz
make run-bundle-json RUN_DIR=runs/<run-id> RUN_BUNDLE_OUT=generated/run.tar.gz
make experiment-verify-json RUN_DIR=runs/<run-id>
make experiment-verify-bundle RUN_DIR=<extracted-run-dir>
make experiment-report RUN_DIR=runs/<run-id>
make experiment-repeat EXPERIMENT_SPEC=smoke EXPERIMENT_REPEAT_COUNT=3
make experiment-summary SUMMARY_INPUT=runs/repeats/<repeat-id>
make experiment-history HISTORY_INPUTS='runs/repeats/a runs/repeats/b'
make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b
```

Experiment platform details live in [docs/experiment-platform.md](docs/experiment-platform.md).
Scenario-pack and evidence boundaries live in
[docs/scenario-packs.md](docs/scenario-packs.md) and
[docs/assurance-boundary.md](docs/assurance-boundary.md).
The end-to-end no-Go authoring path starts in
[docs/authoring-tutorial.md](docs/authoring-tutorial.md).

For batches:

```bash
make matrix-list
make matrix-plan MATRIX_SPEC=smoke
make matrix-plan-json MATRIX_SPEC=smoke
make matrix-run MATRIX_SPEC=smoke
```

Validate env specs before running larger suites:

```bash
make spec-validate
make spec-reference SPEC_KIND=all
make spec-schema SPEC_KIND=all
make spec-docs-check
make spec-list SPEC_KIND=workload
make spec-list SPEC_KIND=utility-test
make spec-list SPEC_KIND=utility-suite
make spec-show SPEC_KIND=experiment SPEC_ID=smoke
```

Tracked env spec reference and schema live in
[docs/spec-reference.md](docs/spec-reference.md) and
[schemas/env-specs.schema.json](schemas/env-specs.schema.json).

## Datasets

Reusable data-loading specs live under `datasets/`:

```bash
make dataset-list
make dataset-show DATASET_SPEC=synthetic/items
make dataset-plan DATASET_SPEC=synthetic/items
make dataset-plan-json DATASET_SPEC=synthetic/items
make dataset-load DATASET_SPEC=synthetic/items DATASET_SIZE=small
```

Noisia can be used as optional PostgreSQL pressure:

```bash
NOISIA_DURATION=120 NOISIA_JOBS=4 make noisia-wait
NOISIA_DURATION=120 NOISIA_JOBS=2 make noisia-temp
```

Noisia is intentionally harmful test tooling. Use it only against disposable
local databases.

For profile-local SQL that should run in the background:

```bash
make workload-start PROFILE=locks WORKLOAD_SQL=20_blocker.sql PROFILE_SECONDS=60
make workload-status
make workload-status-json
make workload-log
make workload-stop
```

Any workload spec can also run in the background:

```bash
make workload-start-spec WORKLOAD_SPEC=profile/locks-blocker PROFILE_SECONDS=60
make workload-status-json
```

Noisia can also run through the background helper:

```bash
make workload-start-noisia WORKLOAD=wait-xacts NOISIA_DURATION=120 NOISIA_JOBS=4
```

## Metrics

Sample broad PostgreSQL metrics to CSV:

```bash
make metrics-plan
METRICS_DURATION=30 METRICS_INTERVAL=1 make metrics-sample
```

Metrics are written under:

```text
logs/metrics/
```

## Failure Scanning

Scan logs and generated artifacts for PostgreSQL crash/error evidence:

```bash
make scan-artifacts
make scan-artifacts-go
./scripts/scan_pg_failures.sh logs generated
go run ./cmd/pgworkbench scan failures logs generated
```

## CI

Default CI runs `make check`, `make test`, and `make scan-artifacts`.
PostgreSQL source-tree checks are manual/opt-in. Details live in
[docs/ci.md](docs/ci.md).
Compatibility notes live in [docs/compatibility.md](docs/compatibility.md).

## Release Snapshot

Build ignored local `pgworkbench` release archives:

```bash
make release-snapshot VERSION=0.2.0
```

All four archives receive deterministic package and supply-chain checks.
Runtime execution is separately gated only for Linux/amd64 and Darwin/arm64;
see the compatibility ledger before making platform-support claims.

Release notes live in [CHANGELOG.md](CHANGELOG.md). Release process details
live in [docs/release.md](docs/release.md).

## Go CLI

The Go CLI is the primary interface for runtime, scenario-pack, experiment,
utility, benchmark, operation, external-driver, A/B, evidence, and release
contracts. Selected source-checkout examples:

```bash
go run ./cmd/pgworkbench version
go run ./cmd/pgworkbench doctor
go run ./cmd/pgworkbench profile list
go run ./cmd/pgworkbench profile validate
go run ./cmd/pgworkbench experiment plan smoke
go run ./cmd/pgworkbench experiment run --runtime native smoke
go run ./cmd/pgworkbench pack validate
go run ./cmd/pgworkbench benchmark list
go run ./cmd/pgworkbench benchmark run --runtime native --subject baseline pgbench/smoke
go run ./cmd/pgworkbench benchmark operation list
go run ./cmd/pgworkbench benchmark driver-show sysbench-postgresql-1.0.20
go run ./cmd/pgworkbench benchmark ab-show <ab-run>
go run ./cmd/pgworkbench scan failures logs generated
go run ./cmd/pgworkbench run list
go run ./cmd/pgworkbench run list --status failed --limit 20
go run ./cmd/pgworkbench run show runs/<run-id>
go run ./cmd/pgworkbench run bundle runs/<run-id> generated/run.tar.gz
go run ./cmd/pgworkbench run bundle --json runs/<run-id> generated/run.tar.gz
go run ./cmd/pgworkbench run verify --json runs/<run-id>
go run ./cmd/pgworkbench run verify --bundle <extracted-run-dir>
go run ./cmd/pgworkbench report run runs/<run-id>
go run ./cmd/pgworkbench report compare runs/a runs/b
go run ./cmd/pgworkbench report summary runs/repeats/<repeat-id>
go run ./cmd/pgworkbench report history runs/repeats/a runs/repeats/b
go run ./cmd/pgworkbench spec reference all
go run ./cmd/pgworkbench spec schema all
go run ./cmd/pgworkbench spec validate
go run ./cmd/pgworkbench utility plan --expanded pg-dump/smoke
go run ./cmd/pgworkbench utility run --json --runtime native --run-id native-pgdump pg-dump/smoke
go run ./cmd/pgworkbench utility-suite plan --json native-dump
go run ./cmd/pgworkbench utility-suite run --json native-dump
```

Run `go run ./cmd/pgworkbench` without arguments for the complete command tree.
Go migration notes live in [docs/go-migration.md](docs/go-migration.md).

## Logs

Run any SQL file with logging:

```bash
make run-sql SQL=profiles/smoke/sql/10_run.sql
```

Logs are written to:

```text
logs/
```

## First Intended Profiles

- `smoke`: minimal profile proving the platform works.
- `constraints`: constraint validation, deferrable foreign keys, uniqueness, checks.
- `jsonb`: JSONB containment, expression indexes, partial indexes, update shape.
- `locks`: lock waits, blockers, blocked sessions.
- `vacuum-bloat`: dead tuples, vacuum behavior, bloat.
- `indexes`: index creation, query plans, write overhead.
- `wal-pressure`: WAL-heavy writes and checkpoint pressure.
- `partitioning`: partition attach/detach/drop experiments.
- `temp-spill`: sort/hash spills and temporary file counters.
- `replication-slots`: physical slot retention and streaming state.
- `logical-replication`: publication/subscription convergence and DDL boundary checks.
- `connection-pressure`: session churn and pooler-shaped behavior.

Massive-DML-specific work belongs in the separate focused repository unless a
small generic scenario is useful here.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE). Exact Go module checksums and retained upstream license and
attribution texts are recorded in [third_party/go-modules.json](third_party/go-modules.json).
Those modules support the source-pack schema test gate and are not linked into
the `pgworkbench` release binary; each release SBOM keeps test and runtime
dependency scopes separate.

## Status

`v0.2.0` candidate: the source tree contains the portable Docker/native runtime,
scenario-pack, and versioned evidence contracts. It is not a v1 release claim;
the exact candidate still has to satisfy the local, publication, compatibility,
and external-adoption gates in
[docs/v1-completion-contract.md](docs/v1-completion-contract.md).
