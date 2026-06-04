# Go Migration

The workbench started as shell-first glue because most behavior is PostgreSQL,
Docker Compose, and external utilities. Go should replace the parts where shell
is weakest:

- structured env parsing;
- validation;
- report generation;
- artifact scanning;
- release packaging.

Current Go CLI:

```bash
go run ./cmd/pgworkbench profile list
go run ./cmd/pgworkbench profile show locks
go run ./cmd/pgworkbench profile validate
go run ./cmd/pgworkbench profile plan locks
go run ./cmd/pgworkbench workload list
go run ./cmd/pgworkbench workload show pgbench/tiny
go run ./cmd/pgworkbench workload validate
go run ./cmd/pgworkbench workload plan pgbench/tiny
go run ./cmd/pgworkbench dataset list
go run ./cmd/pgworkbench dataset show synthetic/items
go run ./cmd/pgworkbench dataset validate
go run ./cmd/pgworkbench dataset plan synthetic/items
go run ./cmd/pgworkbench patchset list
go run ./cmd/pgworkbench patchset show chaos/master
go run ./cmd/pgworkbench patchset validate
go run ./cmd/pgworkbench experiment plan smoke
go run ./cmd/pgworkbench matrix plan --json smoke
go run ./cmd/pgworkbench topology inspect primary-replica
go run ./cmd/pgworkbench topology ps primary-replica
go run ./cmd/pgworkbench source plan pg-source/check
go run ./cmd/pgworkbench source classify generated/pg-source/<run-id>
go run ./cmd/pgworkbench scan failures logs generated
go run ./cmd/pgworkbench report run runs/<run-id>
go run ./cmd/pgworkbench report compare runs/a runs/b
go run ./cmd/pgworkbench report summary runs/repeats/<repeat-id>
go run ./cmd/pgworkbench report history runs/repeats/a runs/repeats/b
go run ./cmd/pgworkbench run verify runs/<run-id>
go run ./cmd/pgworkbench run write-manifest --run-dir runs/<run-id>
go run ./cmd/pgworkbench run write-verdict --run-dir runs/<run-id> --status passed --message 'experiment passed'
go run ./cmd/pgworkbench spec reference all
go run ./cmd/pgworkbench spec schema all
go run ./cmd/pgworkbench spec validate
make pgworkbench
make release-snapshot
```

The shell scripts remain the compatibility layer for now. `make check` runs the
Go profile validator, profile SQL planner, and Go failure scanner alongside the
existing shell tests.
Run reporting, comparison, summary, and history now have Go equivalents through
`pgworkbench report`. Env spec listing, display, and validation are covered by
`pgworkbench spec`. When a Go command matches shell behavior and is covered by
tests, Make targets can move to the Go implementation.
Run manifest and verdict writing now uses Go by default. The explicit
`EXPERIMENT_STATE_WRITER=shell` mode keeps the shell compatibility path
available; `auto` remains a compatibility alias for Go.
Run directory integrity checks are covered by `pgworkbench run verify`.
Env spec contracts can be rendered with `pgworkbench spec reference` and
`pgworkbench spec schema`.
Experiment specs can be preflighted as execution plans with
`pgworkbench experiment plan`.
Matrix specs can be preflighted as Markdown or JSON with
`pgworkbench matrix plan`.
Workload and dataset specs can be listed, shown, and validated with
`pgworkbench workload` and `pgworkbench dataset`.
Workload specs can be preflighted as execution plans with
`pgworkbench workload plan`.
Dataset specs can be preflighted as load plans with
`pgworkbench dataset plan`.
Topology specs can be inspected with `pgworkbench topology inspect` without
starting Docker. Live Compose state can be parsed with `pgworkbench topology ps`
after a topology has been started.
Patchset metadata and PostgreSQL source-check planning can be inspected with
`pgworkbench patchset` and `pgworkbench source plan`; PostgreSQL source-check
artifacts can be summarized with `pgworkbench source classify`. The
clone/build/test runtime stays in shell.
Profile reset/run SQL can be preflighted with `pgworkbench profile plan`
without opening `psql`; actual profile execution stays in shell.
