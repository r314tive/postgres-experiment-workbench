# Profile Authoring

Profiles are resettable PostgreSQL scenarios that run inside the shared
workbench database. Keep each profile focused on one PostgreSQL behavior and
put detailed scenario notes in the profile README, not in the root docs.

## Contract

Every profile should be safe to run against a disposable local database and
should reset only its own schema.

Required files:

```text
profiles/<name>/
  README.md
  sql/
    00_setup.sql
    10_run.sql
```

Optional metadata gives tools a stable catalog without parsing README text:

```text
profiles/<name>/profile.env
```

Supported metadata fields:

```text
PROFILE_NAME
PROFILE_DESCRIPTION
PROFILE_TAGS
PROFILE_SCHEMAS
PROFILE_SIZES
PROFILE_DEFAULT_SIZE
PROFILE_REQUIRES_TOPOLOGY
PROFILE_BACKGROUND_WORKLOADS
PROFILE_DIAGNOSTIC_SQL
```

Run `make profile-validate` before committing profile metadata.
Run `make profile-plan PROFILE=<name>` to verify the default setup/run SQL
sequence and rendered `PROFILE_SIZE`/`PROFILE_SECONDS` command shape without
opening `psql`.

Optional files are useful for concurrent or diagnostic workflows:

```text
profiles/<name>/sql/
  20_background.sql
  21_foreground.sql
  30_diagnostics.sql
```

The workbench passes these psql variables to profile SQL:

```text
:profile
:profile_size
:profile_seconds
```

Use `:profile_size` to scale row counts and payload sizes. Supported values are
`small`, `medium`, and `large`; unknown values should fall back to `small`.
Use `:profile_seconds` for bounded sleeps, lock holders, and short-lived
background actions.

## SQL Style

- Start each SQL file with `\set ON_ERROR_STOP on`.
- Use a schema that matches the profile name, replacing hyphens with
  underscores when needed.
- `00_setup.sql` should use `DROP SCHEMA IF EXISTS <schema> CASCADE`.
- Keep default `small` runs fast enough for local iteration.
- Prefer representative PostgreSQL behavior over large row counts.
- Use transactions with `ROLLBACK` for destructive probes when the persistent
  result is not needed.
- Make expected failures explicit by catching them in SQL, or by turning
  `ON_ERROR_STOP` off only around the expected statement.

## README Checklist

Each profile README should include:

- what the profile demonstrates;
- the default setup/run commands;
- optional background or diagnostic commands;
- what to inspect with `make monitor` or `make metrics-sample`;
- any safety notes for heavier sizes.

## Background Workloads

Use the generic helper when a scenario needs one long-running SQL action while
another session observes or contends with it:

```bash
make workload-start PROFILE=locks WORKLOAD_SQL=20_blocker.sql PROFILE_SECONDS=60
make workload-status
make workload-status-json
make workload-log
make workload-stop
```

Noisia-backed background pressure should stay optional:

```bash
make workload-start-noisia WORKLOAD=wait-xacts NOISIA_DURATION=120 NOISIA_JOBS=4
```

## Metrics

Use CSV sampling for short experiments and attach the output to notes or issue
reports:

```bash
make metrics-plan
METRICS_DURATION=30 METRICS_INTERVAL=1 make metrics-sample
```

Metrics are intentionally broad and generic: active sessions, lock waits,
database counters, temporary file counters, and WAL counters. Profile-specific
measurement queries should live in the profile SQL.

## Boundaries

This repository is the generic experiment workbench. Keep specialized labs in
profile-local docs or separate repositories when they need deep narrative,
large data generators, or domain-specific workflows.
