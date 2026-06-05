# Quickstart Transcript

This transcript shows the smallest end-to-end experiment flow: render a plan,
run a disposable PostgreSQL smoke experiment, verify its run directory, render a
report, and scan artifacts.

The quickstart writes only ignored local artifacts under `runs/`, `logs/`, and
`generated/`.

## No-Docker Preview

Preview the smoke experiment without starting PostgreSQL:

```bash
make quickstart-plan
```

Equivalent explicit command:

```bash
make experiment-plan EXPERIMENT_SPEC=smoke
```

Expected shape:

```text
# Experiment Plan

| Field | Value |
| --- | --- |
| Spec | smoke |
| Name | smoke experiment |
| Topology | single |
| PostgreSQL config | default |
| Profile | smoke |
| Workload | sql/smoke-run |
```

## Run

Run the full quickstart:

```bash
make quickstart
```

Representative output:

```text
run_id=quickstart-YYYYMMDD_HHMMSS
run_dir=/path/to/postgres-experiment-workbench/runs/quickstart-YYYYMMDD_HHMMSS
started_at=YYYY-MM-DDTHH:MM:SSZ
...
verdict=passed
Quickstart run: runs/quickstart-YYYYMMDD_HHMMSS
Quickstart report: runs/quickstart-YYYYMMDD_HHMMSS/report.md
```

Use a fixed run id when you want deterministic paths:

```bash
make quickstart QUICKSTART_RUN_ID=quickstart-local
```

## Inspect Evidence

The quickstart target runs these checks after the experiment finishes:

```bash
make experiment-verify RUN_DIR=runs/<run-id>
make experiment-report RUN_DIR=runs/<run-id>
```

Review the generated files:

```text
runs/<run-id>/
  manifest.env
  stdout.log
  workload.log
  metrics.csv
  metrics.log
  snapshots/
  scan.log
  verdict.env
  verdict.json
  report.md
```

Key verdict fields should look like:

```json
{
  "status": "passed",
  "message": "experiment passed"
}
```

Run the generic artifact scanners:

```bash
make scan-artifacts
make scan-artifacts-go
```

Both scanners should report `result=clean`.

## Next Commands

After the smoke flow is green, try the higher-signal workflows:

```bash
make experiment-run EXPERIMENT_SPEC=locks-under-contention
make experiment-run EXPERIMENT_SPEC=pgdump-under-wal-pressure
make experiment-run EXPERIMENT_SPEC=pgbouncer-smoke
make matrix-plan MATRIX_SPEC=smoke
```
