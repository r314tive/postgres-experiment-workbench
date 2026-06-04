# Roadmap

This project should stay a generic PostgreSQL experiment platform.

## MVP Ready

MVP means a user can clone the repo, run local experiments, test external
utilities, inspect evidence, and trust the result shape.

- Keep `smoke` as the tiny platform verification profile.
- Keep default commands local/disposable and guarded against accidental
  non-local PostgreSQL targets.
- Keep `make check`, `make test`, and `make scan-artifacts` green.
- Keep ignored local-only files out of public artifacts.
- Keep profile metadata valid with `make profile-validate`.
- Keep every real profile runnable at `PROFILE_SIZE=small`.
- Keep experiment outputs self-contained under `runs/<run-id>/`.

## Release Ready

Release means the repo has stable public contracts, documented extension points,
and a small number of reliable binaries/scripts.

- Add release notes and versioned tags once MVP checks are stable.
- Keep packaged `pgworkbench` snapshot binaries buildable for common platforms.
- Add a minimal quickstart video or terminal transcript in docs.
- Add schema/reference docs for profile, workload, topology, experiment, and
  matrix env specs.
- Add compatibility notes for Docker, Compose, PostgreSQL image versions, and
  host tools.
- Add more profile-specific diagnostic SQL where it helps interpretation.
- Add at least one example utility-test workflow for dump/restore, PgBouncer,
  source-check plan, and upgrade path.

## Go Migration

Use Go where deterministic parsing, validation, reporting, or packaging matters.
Keep shell where it is only glue around Docker Compose, `psql`, or host tools.

Already started:

- `pgworkbench profile list|show|validate`.
- `pgworkbench scan failures`.
- `pgworkbench report run|compare|summary|history`.
- `pgworkbench run verify|write-manifest|write-verdict`.
- `pgworkbench spec list|show|validate`.
- `make release-snapshot`.

Good Go candidates:

- env spec schema/reference export;
- experiment plan rendering;
- runner compatibility switch from shell state writers to Go state writers.

Keep in shell for now:

- Docker Compose lifecycle adapters;
- `psql` wrappers;
- one-shot workload command execution;
- intentionally flexible host-tool adapters.

Migration rule: introduce Go commands in parallel, test them against existing
shell behavior, then switch Make targets only after compatibility is proven.

## Candidate Profiles

- `constraints`: constraint validation, foreign keys, deferrable checks.
- `jsonb`: indexing and query shape for JSONB fields.
- `pg-source-check`: maintained patchsets for testing PostgreSQL source builds.
- `logical-ddl`: DDL compatibility checks around logical replication.

## Boundary

Do not turn the platform README into a PostgreSQL textbook. Keep experiments
profile-local and keep the root project focused on reusable mechanics.
