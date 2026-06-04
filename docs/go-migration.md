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
go run ./cmd/pgworkbench scan failures logs generated
go run ./cmd/pgworkbench report run runs/<run-id>
go run ./cmd/pgworkbench report compare runs/a runs/b
go run ./cmd/pgworkbench report summary runs/repeats/<repeat-id>
go run ./cmd/pgworkbench report history runs/repeats/a runs/repeats/b
go run ./cmd/pgworkbench spec validate
make pgworkbench
make release-snapshot
```

The shell scripts remain the compatibility layer for now. `make check` runs the
Go profile validator and Go failure scanner alongside the existing shell tests.
Run reporting, comparison, summary, and history now have Go equivalents through
`pgworkbench report`. Env spec listing, display, and validation are covered by
`pgworkbench spec`. When a Go command matches shell behavior and is covered by
tests, Make targets can move to the Go implementation.
