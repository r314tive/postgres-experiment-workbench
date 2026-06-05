# Changelog

## Unreleased

No changes yet.

## v0.1.17 - 2026-06-05

Changed platform behavior:

- Moved the run comparison Make target to Go `--raw` output while keeping an
  explicit shell compatibility target.

## v0.1.16 - 2026-06-05

Changed platform behavior:

- Moved run report, summary, and history Make targets to Go defaults while
  keeping explicit shell compatibility targets.

## v0.1.15 - 2026-06-05

Changed platform behavior:

- Moved `make matrix-plan` to Go raw output while preserving shell-compatible
  Markdown.

## v0.1.14 - 2026-06-05

Changed platform behavior:

- Moved experiment, matrix, and topology catalog Make targets to Go raw output
  while preserving shell-compatible list/show output.
- Moved diagnostic catalog Make targets to the Go CLI while keeping diagnostic
  execution in shell.

## v0.1.13 - 2026-06-05

Changed platform behavior:

- Moved workload and dataset catalog Make targets to Go raw output while
  preserving shell-compatible list/show output.

## v0.1.12 - 2026-06-05

Changed platform behavior:

- Moved profile catalog Make targets to the Go CLI while keeping the shell
  compatibility script.

## v0.1.11 - 2026-06-05

Added platform capabilities:

- JSON output for Go profile SQL plans.

## v0.1.10 - 2026-06-05

Changed licensing:

- Replaced the proprietary source-available license with Apache License 2.0.

## v0.1.9 - 2026-06-05

Changed licensing:

- Replaced MIT licensing with a proprietary source-available, all-rights-
  reserved license.

## v0.1.8 - 2026-06-05

Added platform capabilities:

- Read-only PostgreSQL diagnostics SQL catalog and runner for activity, locks,
  settings, table/index health, WAL, and replication state.

## v0.1.7 - 2026-06-04

Added platform capabilities:

- JSON output for Go workload and dataset plans.

## v0.1.6 - 2026-06-04

Added platform capabilities:

- JSON output for Go experiment plans and expanded experiment dry-runs.

## v0.1.5 - 2026-06-04

Added platform capabilities:

- Expanded Go experiment dry-run previews for topology, dataset, foreground
  workload, and background workloads.

## v0.1.4 - 2026-06-04

Added platform capabilities:

- Go dataset load plan renderer for no-Docker dataset preflight.

## v0.1.3 - 2026-06-04

Added platform capabilities:

- Go workload execution plan renderer for no-Docker workload preflight.

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
