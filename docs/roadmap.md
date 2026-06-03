# Roadmap

This project should stay a generic PostgreSQL experiment platform.

## Near Term

- Keep `smoke` as the tiny platform verification profile.
- Expand profile-specific diagnostic SQL where it helps interpretation.
- Add optional profile metadata if conventions need machine-readable fields.
- Add CI presets for source-tree checks without making them default.
- Expand topology implementations beyond `single`.
- Add trend/history comparison across multiple local series directories.

## Candidate Profiles

- `replication-slots`: retained WAL and slot lag in a local setup.
- `temp-spill`: sort/hash spills and temporary file counters.
- `connection-pressure`: session churn, idle sessions, pooler-shaped behavior.
- `constraints`: constraint validation, foreign keys, deferrable checks.
- `jsonb`: indexing and query shape for JSONB fields.
- `pg-source-check`: maintained patchsets for testing PostgreSQL source builds.

## Boundary

Do not turn the platform README into a PostgreSQL textbook. Keep experiments
profile-local and keep the root project focused on reusable mechanics.
