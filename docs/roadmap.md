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
- Keep read-only diagnostic SQL snippets generic and safe for local disposable
  targets.
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
- `pgworkbench run list|show`, including status filters and limits.
- `pgworkbench run verify|write-manifest|write-verdict`.
- experiment runner state writer defaulted to Go with explicit shell
  compatibility mode.
- `pgworkbench workload list|show|validate`.
- `pgworkbench dataset list|show|validate`.
- Make profile catalog targets default to Go with shell compatibility still
  available.
- Make workload and dataset catalog targets default to Go raw output with shell
  compatibility still available.
- Make experiment, matrix, and topology catalog targets default to Go raw
  output with shell compatibility still available.
- `pgworkbench diagnostics list|show`; diagnostic execution stays in shell.
- `pgworkbench matrix plan`.
- Make `matrix-plan` default to Go raw output with shell-compatible Markdown.
- Make run report, summary, and history targets default to Go with explicit
  shell compatibility targets.
- Make run comparison default to Go raw output with explicit shell
  compatibility target.
- `pgworkbench spec list|show|reference|schema|validate`.
- `pgworkbench topology inspect|ps`.
- `pgworkbench metrics plan`, including JSON output.
- `pgworkbench patchset list|show|validate`.
- `pgworkbench source plan|classify`.
- `pgworkbench profile plan`, including JSON output.
- `pgworkbench workload plan`, including JSON output.
- `pgworkbench dataset plan`, including JSON output.
- `make release-snapshot`.

Good Go candidates:

- Move the remaining shell-only execution/report glue only when a stable
  structured contract already exists.

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
