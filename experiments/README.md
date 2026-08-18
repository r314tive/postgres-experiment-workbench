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
make experiment-run EXPERIMENT_SPEC=constraints-validation
make experiment-run EXPERIMENT_SPEC=jsonb-indexing
make experiment-run EXPERIMENT_SPEC=logical-ddl
make experiment-run EXPERIMENT_SPEC=multi-version-upgrade-smoke
make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-update
make experiment-run EXPERIMENT_SPEC=massive-dml/generated-batched-delete
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-update
make experiment-run EXPERIMENT_SPEC=massive-dml/queue-update
make experiment-run EXPERIMENT_SPEC=massive-dml/procedure-delete
make experiment-run EXPERIMENT_SPEC=massive-dml/transaction-caveats
make experiment-run EXPERIMENT_SPEC=massive-dml/offline-bulk-load-indexed
make experiment-run EXPERIMENT_SPEC=massive-dml/offline-bulk-load-index-after
make experiment-run EXPERIMENT_SPEC=massive-dml/partition-drop-vs-delete
make experiment-report RUN_DIR=runs/<run-id>
make experiment-repeat EXPERIMENT_SPEC=smoke EXPERIMENT_REPEAT_COUNT=3
make experiment-history HISTORY_INPUTS='runs/repeats/a runs/repeats/b'
```

Experiment specs are sourced local env files and must come from a trusted pack.
Declarative SQL hooks and assertions are the normal authoring path. The three
host-shell fields execute `BASH_ENV=/dev/null bash --noprofile --norc -c` on
the host and require the explicit
`EXPERIMENT_TRUSTED_SHELL=1` marker.

Useful fields:

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
EXPERIMENT_TRUSTED_SHELL
EXPERIMENT_BEFORE_SQL_FILES
EXPERIMENT_BEFORE_SHELL
EXPERIMENT_AFTER_SQL_FILES
EXPERIMENT_AFTER_SHELL
EXPERIMENT_ASSERT_SQL
EXPERIMENT_ASSERT_TRUE_SQL
EXPERIMENT_ASSERT_SQL_FILES
EXPERIMENT_ASSERT_SHELL
EXPERIMENT_METRICS
EXPERIMENT_METRICS_DURATION
EXPERIMENT_METRICS_SAMPLES
EXPERIMENT_STATE_WRITER
EXPERIMENT_SCAN_PATHS
```

If a host-shell hook is configured without that marker, the runner fails before
creating the run directory or starting the runtime. An allowed hook is named in
the runner output immediately before execution. The marker is an explicit trust
boundary, not a sandbox or interactive approval: because the env spec itself is
sourced, review and trust the entire spec or scenario pack. SQL-only specs do
not need the marker.

Experiment matrices live under `matrices/` and batch experiments across config
profiles, profile sizes, and repeat counts:

```bash
make matrix-plan MATRIX_SPEC=smoke
make matrix-run MATRIX_SPEC=smoke
make matrix-plan MATRIX_SPEC=massive-dml-comparison
make matrix-plan MATRIX_SPEC=massive-dml-strategy
```
