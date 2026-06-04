# Roadmap

This project should stay a generic PostgreSQL experiment platform.

## Near Term

- Keep `smoke` as the tiny platform verification profile.
- Expand profile-specific diagnostic SQL where it helps interpretation.
- Add optional profile metadata if conventions need machine-readable fields.
- Add CI presets for source-tree checks without making them default.
- Add optional native `pg_upgrade` binary-pair adapter on top of the
  multi-version topology.
- Add trend/history comparison across multiple local series directories.

## Candidate Profiles

- `temp-spill`: sort/hash spills and temporary file counters.
- `constraints`: constraint validation, foreign keys, deferrable checks.
- `jsonb`: indexing and query shape for JSONB fields.
- `pg-source-check`: maintained patchsets for testing PostgreSQL source builds.
- `logical-ddl`: DDL compatibility checks around logical replication.

## Boundary

Do not turn the platform README into a PostgreSQL textbook. Keep experiments
profile-local and keep the root project focused on reusable mechanics.
