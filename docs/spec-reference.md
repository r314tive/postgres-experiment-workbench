# Env Spec Reference

## workload

Workload specs live under `workloads/**/*.env` and define one foreground or background executable workload.

| Key | Requirement | Default | Allowed | Description |
| --- | --- | --- | --- | --- |
| `WORKLOAD_NAME` | required | - | - | Human-readable workload name. |
| `WORKLOAD_KIND` | required | - | profile-sql, sql, pgbench, pg-source-check, noisia, shell, compose-run | Adapter used by `scripts/run_workload.sh`. |
| `WORKLOAD_REQUIRES_POSTGRES` | optional | 1 | 0, 1 | Set to `0` for host-only workloads that do not need the PostgreSQL container. |
| `WORKLOAD_RUN_LOG` | optional | 1 | 0, 1 | Set to `0` to stream directly without a workload log wrapper. |
| `WORKLOAD_LOG_DIR` | optional | logs/workloads | - | Directory for workload logs. |
| `WORKLOAD_LOG_FILE` | optional | - | - | Explicit log file path. |
| `PROFILE` | required for profile-sql | - | - | Profile directory under `profiles/`. |
| `WORKLOAD_SQL` | conditional | 10_run.sql for profile-sql | - | Profile SQL file name or repository SQL path. |
| `SQL` | required for sql if WORKLOAD_SQL is empty | - | - | Repository or absolute SQL path for `WORKLOAD_KIND=sql`. |
| `PGBENCH_RESET` | optional for pgbench | 0 | 0, 1 | Drop pgbench tables before init/run. |
| `PGBENCH_INIT` | optional for pgbench | 1 | 0, 1 | Run `pgbench -i` before the workload. |
| `PGBENCH_SCALE` | optional for pgbench | 1 | - | Scale factor for pgbench initialization. |
| `PGBENCH_CLIENTS` | optional for pgbench | 2 | - | Client count. |
| `PGBENCH_THREADS` | optional for pgbench | 1 | - | Thread count. |
| `PGBENCH_TIME` | optional for pgbench | 30 | - | Run duration in seconds when transactions are unset. |
| `PGBENCH_TRANSACTIONS` | optional for pgbench | - | - | Transaction count; overrides duration mode. |
| `PGBENCH_SCRIPT` | optional for pgbench | - | - | Custom pgbench script path. |
| `PGBENCH_MODE` | optional for pgbench | builtin | - | Builtin pgbench mode passed with `-b` when no script is set. |
| `PGBENCH_EXTRA_ARGS` | optional for pgbench | - | - | Extra pgbench arguments split as shell words. |
| `NOISIA_WORKLOAD` | required for noisia | - | wait-xacts, temp-files | Noisia workload adapter. |
| `NOISIA_EXTRA_ARGS` | optional for noisia | - | - | Extra noisia arguments split as shell words. |
| `WORKLOAD_CMD` | required for shell | - | - | Host shell command run with PostgreSQL connection env exported. |
| `WORKLOAD_IMAGE` | required for compose-run | - | - | Docker Compose workload image. |
| `WORKLOAD_COMMAND` | required for compose-run | - | - | Command run in the Compose workload service. |
| `PG_SOURCE_ACTION` | optional for pg-source-check | run | plan, run, scan | PostgreSQL source-check action. |
| `PG_PATCHSET` | optional for pg-source-check | - | - | Named patchset under `patchsets/<name>/<pg-ref>`. |
| `PG_PATCH_DIR` | optional for pg-source-check | - | - | Ad hoc patch directory; overrides the directory from `PG_PATCHSET`. |
| `PG_CHECK_TARGET` | optional for pg-source-check | check | - | Make target for PostgreSQL source tests. |
| `PG_CLONE_DEPTH` | optional for pg-source-check | 1 | - | Git clone depth for PostgreSQL source. |
| `PG_CONFIGURE_ARGS` | optional for pg-source-check | --enable-debug --enable-cassert --enable-tap-tests | - | Configure arguments for PostgreSQL source builds. |
| `PG_BUILD_CFLAGS` | optional for pg-source-check | -O0 -g | - | CFLAGS used by PostgreSQL source builds. |
| `PG_TEST_INITDB_EXTRA_OPTS` | optional for pg-source-check | - | - | Extra initdb options passed to PostgreSQL source test targets. |
| `PG_SOURCE_KEEP_GOING` | optional for pg-source-check | 1 | 0, 1 | When `1`, scan artifacts even if the make target failed. |

- Values containing `$` are treated as dynamic by the validator and are not path-checked.
- Tool-specific knobs can live in workload specs as long as the adapter consumes them.

## experiment

Experiment specs live under `experiments/**/*.env` and orchestrate topology, setup, workload, monitoring, assertions, and artifacts.

| Key | Requirement | Default | Allowed | Description |
| --- | --- | --- | --- | --- |
| `EXPERIMENT_NAME` | required | - | - | Human-readable experiment name. |
| `EXPERIMENT_TOPOLOGY` | optional | single | single, primary-replica, logical-replication, pgbouncer, multi-version-upgrade, source-tree | Runtime topology. |
| `EXPERIMENT_PG_CONFIG` | optional | default | - | Config directory under `configs/`. |
| `EXPERIMENT_PROFILE` | optional | - | - | Profile directory under `profiles/`. |
| `EXPERIMENT_PROFILE_SIZE` | optional | small | small, medium, large | Profile scale passed to profile SQL. |
| `EXPERIMENT_PROFILE_SECONDS` | optional | 30 | - | Profile duration passed to profile SQL. |
| `EXPERIMENT_PROFILE_SETUP` | optional | 1 | 0, 1 | Run profile `00_setup.sql` before hooks/workload. |
| `EXPERIMENT_PROFILE_RUN` | optional | 0 | 0, 1 | Run profile SQL before hooks/workload. |
| `EXPERIMENT_PROFILE_RUN_SQL` | optional | 10_run.sql | - | Profile SQL file used when `EXPERIMENT_PROFILE_RUN=1`. |
| `EXPERIMENT_DATASET_SPEC` | optional | - | - | Dataset spec loaded before profile/workload execution. |
| `EXPERIMENT_DATASET_SIZE` | optional | small | small, medium, large | Dataset size passed to dataset loader. |
| `EXPERIMENT_WORKLOAD_SPEC` | optional | - | - | Foreground workload spec. |
| `EXPERIMENT_BACKGROUND_SPECS` | optional | - | - | Space-separated background workload specs. |
| `EXPERIMENT_BACKGROUND_WARMUP` | optional | 0 | - | Seconds to wait after background workloads start. |
| `EXPERIMENT_BACKGROUND_WAIT` | optional | 0 | 0, 1 | Wait for background workloads before after-hooks. |
| `EXPERIMENT_BEFORE_SQL_FILES` | optional | - | - | Space-separated SQL files run before snapshots/workload. |
| `EXPERIMENT_BEFORE_SQL` | optional | - | - | Inline SQL run before snapshots/workload. |
| `EXPERIMENT_BEFORE_SHELL` | optional | - | - | Host shell hook run before snapshots/workload. |
| `EXPERIMENT_AFTER_SQL_FILES` | optional | - | - | Space-separated SQL files run after workload. |
| `EXPERIMENT_AFTER_SQL` | optional | - | - | Inline SQL run after workload. |
| `EXPERIMENT_AFTER_SHELL` | optional | - | - | Host shell hook run after workload. |
| `EXPERIMENT_ASSERT_SQL_FILES` | optional | - | - | Space-separated SQL assertion files. |
| `EXPERIMENT_ASSERT_SQL` | optional | - | - | Inline SQL assertion. |
| `EXPERIMENT_ASSERT_SHELL` | optional | - | - | Host shell assertion hook. |
| `EXPERIMENT_METRICS` | optional | 1 | 0, 1 | Enable metrics sampling. |
| `EXPERIMENT_METRICS_INTERVAL` | optional | 1 | - | Metrics sampling interval in seconds. |
| `EXPERIMENT_METRICS_DURATION` | optional | 30 | - | Metrics sampling duration in seconds. |
| `EXPERIMENT_METRICS_SAMPLES` | optional | - | - | Fixed sample count; overrides duration loop. |
| `EXPERIMENT_SNAPSHOT` | optional | 1 | 0, 1 | Capture before/after PostgreSQL snapshots. |
| `EXPERIMENT_DOCKER_RESET` | optional | 0 | 0, 1 | Reset runtime before the run. |
| `EXPERIMENT_STATE_WRITER` | optional | go | auto, go, shell | State-file writer mode for manifest and verdict artifacts. `auto` is a compatibility alias for `go`. |
| `EXPERIMENT_SCAN_PATHS` | optional | run directory | - | Paths scanned for failure evidence. |
| `EXPERIMENT_RUN_ID` | optional | generated | - | Explicit run id. |

- Keep interpretation profile-local; experiment specs should describe orchestration.
- Foreground and background workload specs use the workload contract.

## matrix

Matrix specs live under `matrices/**/*.env` and expand experiments across config/profile-size/repeat combinations.

| Key | Requirement | Default | Allowed | Description |
| --- | --- | --- | --- | --- |
| `MATRIX_NAME` | required | - | - | Human-readable matrix name. |
| `MATRIX_EXPERIMENTS` | optional | smoke | - | Space-separated experiment specs. |
| `MATRIX_PG_CONFIGS` | optional | default | - | Space-separated PostgreSQL config profiles. |
| `MATRIX_PROFILE_SIZES` | optional | small | small, medium, large | Space-separated profile sizes. |
| `MATRIX_REPEATS` | optional | 1 | positive integer | Repeat count per combination. |
| `MATRIX_STOP_ON_FAIL` | optional | 0 | 0, 1 | Stop matrix after first failed run. |
| `MATRIX_DOCKER_RESET` | optional | 0 | 0, 1 | Reset runtime before each run. |
| `MATRIX_RUN_ID` | optional | generated | - | Explicit matrix run id. |
| `MATRIX_RUN_DIR` | optional | runs/matrices/<id> | - | Explicit matrix artifact directory. |

## topology

Topology specs live under `topologies/**/*.env` and describe supported runtime shapes.

| Key | Requirement | Default | Allowed | Description |
| --- | --- | --- | --- | --- |
| `TOPOLOGY_NAME` | required | - | single, primary-replica, logical-replication, pgbouncer, multi-version-upgrade | Topology id; must match the spec id. |
| `TOPOLOGY_DESCRIPTION` | required | - | - | Human-readable topology description. |

- Topology implementation remains in shell/Docker Compose adapters.

## dataset

Dataset specs live under `datasets/**/*.env` and load reusable data before an experiment workload.

| Key | Requirement | Default | Allowed | Description |
| --- | --- | --- | --- | --- |
| `DATASET_NAME` | required | - | - | Human-readable dataset name. |
| `DATASET_KIND` | required | - | sql, profile, pgbench | Dataset loader adapter. |
| `DATASET_SQL` | required for sql | - | - | Repository or absolute SQL path. |
| `DATASET_PROFILE` | required for profile | - | - | Profile setup SQL used as dataset source. |
| `DATASET_SIZE` | optional | small | small, medium, large | Dataset/profile size override. |
| `DATASET_SCHEMA` | optional for sql | dataset_synthetic | - | Target schema variable passed to dataset SQL. |
| `DATASET_ROWS` | optional for sql | 10000 | - | Row count variable passed to dataset SQL. |
| `DATASET_SEED` | optional for sql | 1 | - | Seed variable passed to dataset SQL. |
| `DATASET_SCALE` | optional for pgbench | 1 | - | Pgbench initialization scale. |

## utility-test

Utility test specs live under `utility-tests/**/*.env` and describe a reusable PostgreSQL utility/tool test scenario.

| Key | Requirement | Default | Allowed | Description |
| --- | --- | --- | --- | --- |
| `UTILITY_TEST_NAME` | required | - | - | Human-readable utility test name. |
| `UTILITY_TEST_WORKLOAD_SPEC` | required | - | - | Foreground workload spec that invokes the utility or external tool. |
| `UTILITY_TEST_PROFILE` | optional | - | - | Profile directory under `profiles/` used to prepare database state. |
| `UTILITY_TEST_PROFILE_SIZE` | optional | small | small, medium, large | Profile scale passed to setup SQL. |
| `UTILITY_TEST_PROFILE_SECONDS` | optional | 30 | - | Profile duration passed to setup/run SQL when used. |
| `UTILITY_TEST_DATASET_SPEC` | optional | - | - | Dataset spec loaded before background and utility workloads. |
| `UTILITY_TEST_DATASET_SIZE` | optional | small | small, medium, large | Dataset size passed to the dataset loader. |
| `UTILITY_TEST_BACKGROUND_SPECS` | optional | - | - | Space-separated background workload specs started before the utility workload. |
| `UTILITY_TEST_BACKGROUND_WARMUP` | optional | 0 | - | Seconds to wait after background workloads start. |
| `UTILITY_TEST_BACKGROUND_WAIT` | optional | 0 | 0, 1 | Wait for background workloads after the foreground utility workload. |
| `UTILITY_TEST_METRICS` | optional | 1 | 0, 1 | Enable metrics sampling during the utility test. |
| `UTILITY_TEST_METRICS_INTERVAL` | optional | 1 | - | Metrics sampling interval in seconds. |
| `UTILITY_TEST_METRICS_DURATION` | optional | 30 | - | Metrics sampling duration in seconds. |
| `UTILITY_TEST_METRICS_SAMPLES` | optional | - | - | Fixed metrics sample count; overrides duration loop. |
| `UTILITY_TEST_NOTES` | optional | - | - | Short operator notes for expected evidence or caveats. |

- Use utility tests for pg_dump, pg_restore, pg_upgrade, external backup tools, data checkers, fuzzers, and other PostgreSQL-adjacent utilities.
- Values containing `$` are treated as dynamic by the validator and are not path-checked.
