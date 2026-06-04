# Roadmap

This project should stay a generic PostgreSQL experiment platform.

## Near Term

- Keep `smoke` as the tiny platform verification profile.
- Expand profile-specific diagnostic SQL where it helps interpretation.

## Candidate Profiles

- `constraints`: constraint validation, foreign keys, deferrable checks.
- `jsonb`: indexing and query shape for JSONB fields.
- `pg-source-check`: maintained patchsets for testing PostgreSQL source builds.
- `logical-ddl`: DDL compatibility checks around logical replication.

## Boundary

Do not turn the platform README into a PostgreSQL textbook. Keep experiments
profile-local and keep the root project focused on reusable mechanics.
