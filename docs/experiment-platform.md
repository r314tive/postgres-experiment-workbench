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
./scripts/report_run.sh runs/<run-id> runs/<run-id>/report.md
```

Reports are Markdown summaries built from `manifest.env`, `verdict.env`,
`metrics.csv`, snapshots, background logs, and scan artifacts.

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
are written under `runs/matrices/<matrix-run-id>/`.

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
