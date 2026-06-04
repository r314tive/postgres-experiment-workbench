# Changelog

## Unreleased

No changes yet.

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
