# Compatibility

The workbench is designed for local, disposable PostgreSQL experiments.

## Required

- `make`
- Bash
- Docker Engine or Docker Desktop
- Docker Compose v2, available as `docker compose`
- Go for `pgworkbench`, `make check`, release snapshots, and Go-backed
  reporting/validation

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
- Host PostgreSQL utilities when testing host-installed tools directly.
- Third-party workload/fuzzing images for specs under `workloads/external/`.
- A `PG_UPGRADE_IMAGE` containing old and new PostgreSQL binaries for native
  `pg_upgrade` checks.

## Runtime Notes

Use `.env` for local overrides. If no `.env` exists, `.env.example` is used.

Keep experiments disposable. The default commands are intended for local Docker
targets, not production PostgreSQL instances.

The experiment runner writes `manifest.env`, `verdict.env`, and `verdict.json`
with the Go state writer by default. Set `EXPERIMENT_STATE_WRITER=shell` to
force the shell compatibility path. `EXPERIMENT_STATE_WRITER=auto` remains a
compatibility alias for the Go writer.

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
make release-check
make test
```
