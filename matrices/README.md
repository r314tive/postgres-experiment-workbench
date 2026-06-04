# Experiment Matrices

Matrices define batches of experiment combinations.

Use them when one experiment is not enough and you need to vary PostgreSQL
config profiles, profile sizes, or repeat counts:

```bash
make matrix-list
make matrix-plan MATRIX_SPEC=smoke
make matrix-plan-go MATRIX_SPEC=smoke
make matrix-plan-json MATRIX_SPEC=smoke
make matrix-run MATRIX_SPEC=smoke
```

Matrix specs are trusted local shell env files under `matrices/**/*.env`.

Useful fields:

```text
MATRIX_NAME
MATRIX_EXPERIMENTS
MATRIX_PG_CONFIGS
MATRIX_PROFILE_SIZES
MATRIX_REPEATS
MATRIX_DOCKER_RESET
MATRIX_STOP_ON_FAIL
```

Runs are written under `runs/matrices/<matrix-run-id>/`, with per-run reports
and a summary Markdown file.

`make matrix-plan-json` renders a stable JSON plan for external tooling and CI
orchestration. It does not start Docker or run experiments.
