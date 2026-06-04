# Experiments

Experiments are the top-level orchestration specs for the workbench.

Profiles prepare database state. Workloads run actions. Experiments combine
profiles, workloads, background pressure, hooks, metrics, snapshots, assertions,
artifact scans, and final verdicts into one run directory:

```text
runs/<run-id>/
  manifest.env
  stdout.log
  workload.log
  metrics.csv
  snapshots/
  background/
  scan.log
  verdict.env
  verdict.json
```

Run:

```bash
make experiment-list
make experiment-show EXPERIMENT_SPEC=smoke
make experiment-plan EXPERIMENT_SPEC=smoke
make experiment-run EXPERIMENT_SPEC=smoke
make experiment-run EXPERIMENT_SPEC=multi-version-upgrade-smoke
make experiment-report RUN_DIR=runs/<run-id>
make experiment-repeat EXPERIMENT_SPEC=smoke EXPERIMENT_REPEAT_COUNT=3
make experiment-history HISTORY_INPUTS='runs/repeats/a runs/repeats/b'
```

Specs are trusted local shell env files. Useful fields:

```text
EXPERIMENT_NAME
EXPERIMENT_TOPOLOGY
EXPERIMENT_PG_CONFIG
EXPERIMENT_PROFILE
EXPERIMENT_PROFILE_SIZE
EXPERIMENT_PROFILE_SETUP
EXPERIMENT_PROFILE_RUN
EXPERIMENT_WORKLOAD_SPEC
EXPERIMENT_BACKGROUND_SPECS
EXPERIMENT_BEFORE_SQL_FILES
EXPERIMENT_BEFORE_SHELL
EXPERIMENT_AFTER_SQL_FILES
EXPERIMENT_AFTER_SHELL
EXPERIMENT_ASSERT_SQL
EXPERIMENT_ASSERT_SQL_FILES
EXPERIMENT_ASSERT_SHELL
EXPERIMENT_METRICS
EXPERIMENT_METRICS_DURATION
EXPERIMENT_METRICS_SAMPLES
EXPERIMENT_STATE_WRITER
EXPERIMENT_SCAN_PATHS
```

Experiment matrices live under `matrices/` and batch experiments across config
profiles, profile sizes, and repeat counts:

```bash
make matrix-plan MATRIX_SPEC=smoke
make matrix-run MATRIX_SPEC=smoke
```
