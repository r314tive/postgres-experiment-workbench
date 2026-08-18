# Experiment Platform

The experiment layer is the workbench's top-level contract.

An experiment creates one immutable local run directory:

```text
runs/<run-id>/
  manifest.env
  stdout.log
  workload.log
  metrics.csv
  metrics.log
  snapshots/
    before/
    after/
  background/
  scan.log
  verdict.env
  verdict.json
```

## Run

```bash
make experiment-list
make experiment-show EXPERIMENT_SPEC=smoke
make experiment-plan EXPERIMENT_SPEC=smoke
make experiment-plan-json EXPERIMENT_SPEC=smoke
make experiment-plan-expanded EXPERIMENT_SPEC=smoke
make experiment-plan-expanded-json EXPERIMENT_SPEC=smoke
make experiment-run EXPERIMENT_SPEC=smoke
```

`experiment-plan` renders the resolved execution phases without starting a runtime.
`experiment-plan-expanded` also embeds topology, dataset, foreground workload,
and background workload previews without starting Docker.
The JSON variants render the same dry-runs for external tools.

## Verify

```bash
make experiment-verify RUN_DIR=runs/<run-id>
make experiment-verify-json RUN_DIR=runs/<run-id>
go run ./cmd/pgworkbench run verify runs/<run-id>
go run ./cmd/pgworkbench run verify --json runs/<run-id>
go run ./cmd/pgworkbench run verify --bundle <extracted-run-dir>
go run ./cmd/pgworkbench run verify --json --bundle <extracted-run-dir>
```

Run verification checks required state files, env/JSON/CSV parseability, verdict
consistency, exit-code fields, and metrics sample presence. JSON verification
output is suitable for CI jobs that need structured `valid` and `issues`
fields. `--bundle` is required for an extracted run bundle; it fails closed if
the complete bundle inventory is absent. Without `--bundle`, inventory remains
optional so that a live run can be checked before an archive is created.

## Inspect

```bash
make run-list
make run-list RUN_STATUS=failed RUN_LIMIT=20
make run-list-json
make run-show RUN_DIR=runs/<run-id>
make run-show-json RUN_DIR=runs/<run-id>
make run-bundle RUN_DIR=runs/<run-id> RUN_BUNDLE_OUT=generated/run.tar.gz
make run-bundle-json RUN_DIR=runs/<run-id> RUN_BUNDLE_OUT=generated/run.tar.gz
go run ./cmd/pgworkbench run list --json --status failed --limit 20
go run ./cmd/pgworkbench run show runs/<run-id>
go run ./cmd/pgworkbench run bundle runs/<run-id> generated/run.tar.gz
go run ./cmd/pgworkbench run bundle --json runs/<run-id> generated/run.tar.gz
```

Run catalog commands summarize local run artifacts without starting Docker or
connecting to PostgreSQL. Run bundles are gzip-compressed tar archives with
relative paths rooted at the run id, and JSON bundle output reports the archive
path, file count, and uncompressed byte count. After extraction, verify them
with `run verify --bundle`, not the live-run form.

## Compare

```bash
make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b
```

Comparison uses `verdict.env` and selected `metrics.csv` deltas. It is a compact
first-pass report, not a statistical benchmark framework.

## Report

```bash
make experiment-report RUN_DIR=runs/<run-id>
make experiment-report-shell RUN_DIR=runs/<run-id>
./scripts/report_run.sh runs/<run-id> reports/<run-id>.md
go run ./cmd/pgworkbench report run runs/<run-id> runs/<run-id>/report.md
go run ./cmd/pgworkbench report summary runs/repeats/<repeat-id>
go run ./cmd/pgworkbench report history runs/repeats/a runs/repeats/b
```

Reports are Markdown summaries built from `manifest.env`, `verdict.env`,
`metrics.csv`, snapshots, background logs, and scan artifacts.

## Run State

The runner writes versioned, machine-readable state files with the Go state
writer. `EXPERIMENT_STATE_WRITER=auto` remains a compatibility alias for Go;
the legacy shell writer is rejected because it cannot satisfy the portable v1
evidence contract.

```bash
go run ./cmd/pgworkbench run write-manifest --run-dir runs/<run-id>
go run ./cmd/pgworkbench run write-verdict --run-dir runs/<run-id> --status passed --message 'experiment passed'
```

## Statistical Summary

```bash
make experiment-summary SUMMARY_INPUT=runs/repeats/<repeat-id>
make experiment-summary SUMMARY_INPUT=runs/matrices/<matrix-run-id>
./scripts/summarize_runs.sh runs/a runs/b
```

Run-series summaries count verdict statuses and aggregate selected metrics
across runs. Cumulative counters are summarized as per-run deltas
(`last - first`). Gauge-like metrics are summarized as per-run maximums.

## History

```bash
make experiment-history HISTORY_INPUTS='runs/repeats/a runs/repeats/b'
./scripts/compare_run_history.sh runs/repeats/a runs/matrices/b
```

History comparison treats each repeat, matrix, or individual run directory as a
series. Series are compared in argument order, and trend columns show the final
series average minus the first series average.

## Repeat

```bash
make experiment-repeat EXPERIMENT_SPEC=smoke EXPERIMENT_REPEAT_COUNT=3
```

Repeat runs are written under:

```text
runs/repeats/<repeat-id>/
  runs.tsv
  summary.md
  reports/
  compare/
  driver-logs/
```

Each repeat directory also receives `statistics.md`.

The repeat runner keeps going after failures by default, so flaky experiments
produce evidence for every attempted iteration. Set
`EXPERIMENT_REPEAT_STOP_ON_FAIL=1` to stop at the first failed run.

## Matrix

```bash
make matrix-list
make matrix-plan MATRIX_SPEC=smoke
make matrix-plan-json MATRIX_SPEC=smoke
make matrix-run MATRIX_SPEC=smoke
PGWORKBENCH_CLI=/path/to/candidate make matrix-candidate-verify \
  MATRIX_RUN=runs/matrices/<matrix-run-id> MATRIX_EXPECTED_RUNS=<count> \
  VERSION=<version> BUILD_COMMIT=<full-commit>
```

Matrix specs live under `matrices/**/*.env`. They vary experiment specs,
PostgreSQL config profiles, profile sizes, and repeat counts. Matrix artifacts
are written under `runs/matrices/<matrix-run-id>/`, including `statistics.md`.
Use `matrix-plan-json` when another tool needs a stable machine-readable list
of planned combinations without starting Docker.

`matrix-candidate-verify` is a release qualification gate, not a selector for
the newest local output. It requires the exact expected row count, applies the
live-run artifact verifier to every indexed run, checks the retained
experiment-spec digest, current checkout scenario-pack identity,
row-to-manifest/verdict bindings, and path containment, and requires both every
run and the verifier binary itself to name the supplied non-development version
and full commit. This gate verifies live artifacts; portable bundle inventory
and relocated verification remain separate contracts.

## Spec Responsibilities

Use experiment specs for orchestration:

- topology and PostgreSQL config profile;
- dataset loading;
- profile setup/run;
- declarative pre/post SQL hooks and explicitly trusted host-shell hooks;
- foreground workload;
- background workloads;
- metrics sampling;
- snapshots;
- assertions;
- artifact scanning and verdicts.

Keep scenario-specific interpretation in profile docs and tool-specific
execution details in workload specs.

Host-shell hooks are a separate trust boundary. `EXPERIMENT_BEFORE_SHELL`,
`EXPERIMENT_AFTER_SHELL`, and `EXPERIMENT_ASSERT_SHELL` fail closed unless the
spec explicitly sets `EXPERIMENT_TRUSTED_SHELL=1`; the runner names each trusted
hook before executing it. SQL hook and assertion fields do not require that
marker. This is an audit/intent gate rather than a sandbox because experiment
env files are sourced and therefore the whole spec or pack must already be
trusted.

Render the generated env spec contract with:

```bash
make spec-reference SPEC_KIND=all
make spec-schema SPEC_KIND=all
go run ./cmd/pgworkbench spec reference all
go run ./cmd/pgworkbench spec schema all
```

## Topology Examples

`EXPERIMENT_TOPOLOGY=primary-replica` asks the runtime layer to start the
primary plus physical replica before profile setup and workload execution.
`EXPERIMENT_TOPOLOGY=logical-replication` starts a publisher plus independent
logical subscriber.
`EXPERIMENT_TOPOLOGY=pgbouncer` starts PostgreSQL plus PgBouncer.
`EXPERIMENT_TOPOLOGY=multi-version-upgrade` starts old and new PostgreSQL
versions for upgrade-path utility checks.

Examples:

```bash
make experiment-run EXPERIMENT_SPEC=replica-readonly
make experiment-run EXPERIMENT_SPEC=replication-slots
make experiment-run EXPERIMENT_SPEC=logical-replication
make experiment-run EXPERIMENT_SPEC=pgbouncer-smoke
make experiment-run EXPERIMENT_SPEC=multi-version-upgrade-smoke
```
