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
make experiment-run EXPERIMENT_SPEC=smoke
```

## Compare

```bash
make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b
```

Comparison uses `verdict.env` and selected `metrics.csv` deltas. It is a compact
first-pass report, not a statistical benchmark framework.

## Report

```bash
make experiment-report RUN_DIR=runs/<run-id>
make experiment-report-go RUN_DIR=runs/<run-id>
./scripts/report_run.sh runs/<run-id> runs/<run-id>/report.md
go run ./cmd/pgworkbench report run runs/<run-id> runs/<run-id>/report.md
go run ./cmd/pgworkbench report summary runs/repeats/<repeat-id>
go run ./cmd/pgworkbench report history runs/repeats/a runs/repeats/b
```

Reports are Markdown summaries built from `manifest.env`, `verdict.env`,
`metrics.csv`, snapshots, background logs, and scan artifacts.

## Run State

The runner writes machine-readable state files for every experiment. Shell
scripts remain the default compatibility path, and the Go CLI can write the same
public artifacts for future runner migration:

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
make matrix-run MATRIX_SPEC=smoke
```

Matrix specs live under `matrices/**/*.env`. They vary experiment specs,
PostgreSQL config profiles, profile sizes, and repeat counts. Matrix artifacts
are written under `runs/matrices/<matrix-run-id>/`, including `statistics.md`.

## Spec Responsibilities

Use experiment specs for orchestration:

- topology and PostgreSQL config profile;
- dataset loading;
- profile setup/run;
- pre/post SQL and shell hooks;
- foreground workload;
- background workloads;
- metrics sampling;
- snapshots;
- assertions;
- artifact scanning and verdicts.

Keep scenario-specific interpretation in profile docs and tool-specific
execution details in workload specs.

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
