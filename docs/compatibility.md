# Compatibility

The workbench is designed for local, disposable PostgreSQL experiments.

## Required for all executions

- Bash
- `psql` plus standard `awk`, `sed`, `realpath`, and `tee`
- a released `pgworkbench` binary, or Go when running from source

Choose one runtime:

- Docker Engine/Desktop plus Compose v2 for all topologies; or
- host `initdb`, `pg_ctl`, `createdb`, `pg_isready`, and optional `pgbench` for
  the native `single` topology.

`make` and Go are development/source-checkout requirements, not requirements
for a released archive.

Default PostgreSQL runtime:

```text
postgres:16-alpine
```

The default connection target is local and disposable:

```text
postgres://postgres:postgres@127.0.0.1:55433/pg_experiment_workbench?sslmode=disable
```

## Recommended

- GNU or BSD coreutils with standard `date`, `sed`, `awk`, `realpath`, and
  `tee` behavior.
- Enough local disk for Docker volumes, source-check artifacts, generated
  reports, and release snapshots.
- Enough memory for multi-topology runs such as primary/replica, logical
  replication, PgBouncer, or multi-version upgrade checks.

## Optional

- `gh` only for manual GitHub workflows outside the workbench contract.
- Host PostgreSQL utilities when using the native runtime or testing utilities.
- Third-party workload/fuzzing images for specs under `workloads/external/`.
- A `PG_UPGRADE_IMAGE` containing old and new PostgreSQL binaries for native
  `pg_upgrade` checks.

## Runtime Notes

Use `.env` for local overrides. If no `.env` exists, `.env.example` is used.
If default host ports are occupied, export overrides directly or evaluate
`./scripts/assign_test_ports.sh` before `make test`. The helper requires
`python3`; direct env overrides do not.

Keep experiments disposable. Docker and native runtime commands are intended
for workbench-owned local targets, not production PostgreSQL instances.

The experiment runner writes versioned `manifest.env`, `verdict.env`, and
`verdict.json` artifacts with the Go state writer. `auto` remains an alias for
`go`; the legacy shell writer is rejected because it cannot satisfy the v1
portable evidence contract.

## Verification

Run the portable checks:

```bash
make doctor
make check
make scan-artifacts
make scan-artifacts-go
```

Run the Docker-backed suite before release-level changes:

```bash
make release-check VERSION=0.2.0
make test
```
