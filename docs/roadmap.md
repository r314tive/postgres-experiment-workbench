# Roadmap

This project should stay a generic PostgreSQL experiment platform.

## MVP Ready

MVP means a user can clone the repo, run local experiments, test external
utilities, inspect evidence, and trust the result shape.

- Keep `smoke` as the tiny platform verification profile.
- Keep `pgworkbench doctor` useful for prerequisite checks.
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
- Keep `make release-check` green before tags.
- Keep packaged `pgworkbench` snapshot binaries buildable for common platforms.
- Keep the minimal quickstart transcript current.
- Keep schema/reference docs for workload, topology, experiment, matrix, and
  dataset env specs generated from code and checked for drift.
- Keep compatibility notes current for Docker, Compose, PostgreSQL image
  versions, and host tools.
- Add more profile-specific diagnostic SQL where it helps interpretation.
- Keep utility-test workflows documented for dump/restore, PgBouncer,
  source-check plan, and upgrade paths.

## Go Migration

Use Go where deterministic parsing, validation, reporting, or packaging matters.
Keep shell where it is only glue around Docker Compose, `psql`, or host tools.

Already started:

- `pgworkbench profile list|show|validate`.
- `pgworkbench experiment plan`, including JSON output and expanded dry-run
  previews.
- `pgworkbench scan failures`.
- `pgworkbench report run|compare|summary|history`.
- `pgworkbench run verify|write-manifest|write-verdict`.
- experiment runner state writer defaulted to Go with explicit shell
  compatibility mode.
- `pgworkbench workload list|show|validate`.
- `pgworkbench dataset list|show|validate`.
- `pgworkbench matrix plan`.
- `pgworkbench spec list|show|reference|schema|validate`.
- `pgworkbench topology inspect|ps`.
- `pgworkbench patchset list|show|validate`.
- `pgworkbench source plan|classify`.
- `pgworkbench profile plan`.
- `pgworkbench workload plan`.
- `pgworkbench dataset plan`.
- `make release-snapshot`.

Good Go candidates:

- JSON output for workload and dataset plans.

Keep in shell for now:

- Docker Compose lifecycle adapters;
- `psql` wrappers;
- one-shot workload command execution;
- intentionally flexible host-tool adapters.

Migration rule: introduce Go commands in parallel, test them against existing
shell behavior, then switch Make targets only after compatibility is proven.

## Candidate Profiles

No named MVP candidate profile is pending. Next profile additions should come
from concrete utility, topology, or PostgreSQL behavior gaps found during real
runs.

## Boundary

Do not turn the platform README into a PostgreSQL textbook. Keep experiments
profile-local and keep the root project focused on reusable mechanics.
