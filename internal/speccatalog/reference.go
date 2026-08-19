package speccatalog

import (
	"fmt"
	"io"
	"strings"
)

type KindReference struct {
	Kind    string
	Summary string
	Fields  []FieldReference
	Notes   []string
}

type FieldReference struct {
	Key         string
	Requirement string
	Default     string
	Allowed     string
	Description string
}

func References(kind string) ([]KindReference, error) {
	if kind == "" || kind == "all" {
		return []KindReference{
			workloadReference(),
			experimentReference(),
			benchmarkReference(),
			matrixReference(),
			topologyReference(),
			datasetReference(),
			utilityTestReference(),
			utilitySuiteReference(),
		}, nil
	}

	switch kind {
	case "workload":
		return []KindReference{workloadReference()}, nil
	case "experiment":
		return []KindReference{experimentReference()}, nil
	case "benchmark":
		return []KindReference{benchmarkReference()}, nil
	case "matrix":
		return []KindReference{matrixReference()}, nil
	case "topology":
		return []KindReference{topologyReference()}, nil
	case "dataset":
		return []KindReference{datasetReference()}, nil
	case "utility-test":
		return []KindReference{utilityTestReference()}, nil
	case "utility-suite":
		return []KindReference{utilitySuiteReference()}, nil
	default:
		return nil, fmt.Errorf("unsupported spec kind: %s", kind)
	}
}

func RenderReference(w io.Writer, kind string) error {
	references, err := References(kind)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "# Env Spec Reference")
	for _, reference := range references {
		fmt.Fprintf(w, "\n## %s\n\n", reference.Kind)
		fmt.Fprintf(w, "%s\n\n", reference.Summary)
		fmt.Fprintln(w, "| Key | Requirement | Default | Allowed | Description |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
		for _, field := range reference.Fields {
			fmt.Fprintf(
				w,
				"| `%s` | %s | %s | %s | %s |\n",
				field.Key,
				tableCell(field.Requirement),
				tableCell(field.Default),
				tableCell(field.Allowed),
				tableCell(field.Description),
			)
		}
		if len(reference.Notes) > 0 {
			fmt.Fprintln(w)
			for _, note := range reference.Notes {
				fmt.Fprintf(w, "- %s\n", note)
			}
		}
	}
	return nil
}

func workloadReference() KindReference {
	return KindReference{
		Kind:    "workload",
		Summary: "Workload specs live under `workloads/**/*.env` and define one foreground or background executable workload.",
		Fields: []FieldReference{
			{"WORKLOAD_NAME", "required", "", "", "Human-readable workload name."},
			{"WORKLOAD_KIND", "required", "", "profile-sql, sql, pgbench, pg-dump, pg-dumpall, pg-restore, pg-source-check, noisia, shell, compose-run", "Adapter used by `scripts/run_workload.sh`."},
			{"WORKLOAD_REQUIRES_POSTGRES", "optional", "1", "0, 1", "Set to `0` for host-only workloads that do not need the PostgreSQL container."},
			{"WORKLOAD_RUN_LOG", "optional", "1", "0, 1", "Set to `0` to stream directly without a workload log wrapper."},
			{"WORKLOAD_LOG_DIR", "optional", "logs/workloads", "", "Directory for workload logs."},
			{"WORKLOAD_LOG_FILE", "optional", "", "", "Explicit log file path."},
			{"PROFILE", "required for profile-sql", "", "", "Profile directory under `profiles/`."},
			{"WORKLOAD_SQL", "conditional", "10_run.sql for profile-sql", "", "Profile SQL file name or repository SQL path."},
			{"SQL", "required for sql if WORKLOAD_SQL is empty", "", "", "Repository or absolute SQL path for `WORKLOAD_KIND=sql`."},
			{"PGBENCH_RESET", "optional for pgbench", "0", "0, 1", "Drop pgbench tables before init/run."},
			{"PGBENCH_INIT", "optional for pgbench", "1", "0, 1", "Run `pgbench -i` before the workload."},
			{"PGBENCH_SCALE", "optional for pgbench", "1", "", "Scale factor for pgbench initialization."},
			{"PGBENCH_CLIENTS", "optional for pgbench", "2", "", "Client count."},
			{"PGBENCH_THREADS", "optional for pgbench", "1", "", "Thread count."},
			{"PGBENCH_TIME", "optional for pgbench", "30", "", "Run duration in seconds when transactions are unset."},
			{"PGBENCH_TRANSACTIONS", "optional for pgbench", "", "", "Transaction count; overrides duration mode."},
			{"PGBENCH_SCRIPT", "optional for pgbench", "", "", "Custom pgbench script path."},
			{"PGBENCH_MODE", "optional for pgbench", "builtin", "", "Builtin pgbench mode passed with `-b` when no script is set."},
			{"PGBENCH_EXTRA_ARGS", "optional for pgbench", "", "", "Extra pgbench arguments split as shell words. Experiment-owned runs reject host, port, user, database, and option-terminator overrides."},
			{"UTILITY_OUTPUT_FILE", "required for pg-dump, pg-dumpall, pg-restore", "", "portable repository-relative file", "Host-side output file written atomically by a PostgreSQL utility adapter."},
			{"UTILITY_ARCHIVE_FILE", "required for pg-restore", "", "portable repository-relative file", "Custom-format archive used for the pg_restore round trip."},
			{"UTILITY_SOURCE_SCHEMA", "optional for pg-dump; required for pg-restore", "", "simple PostgreSQL identifier", "Source schema selected for a dump or restore round trip."},
			{"UTILITY_TARGET_SCHEMA", "required for pg-restore", "", "simple PostgreSQL identifier", "Schema name produced by the isolated pg_restore round trip."},
			{"NOISIA_WORKLOAD", "required for noisia", "", "wait-xacts, temp-files", "Noisia workload adapter."},
			{"NOISIA_EXTRA_ARGS", "optional for noisia", "", "", "Extra noisia arguments split as shell words. Experiment-owned runs reject conninfo and option-terminator overrides."},
			{"WORKLOAD_CMD", "required for shell", "", "", "Host shell command run with PostgreSQL connection env exported."},
			{"WORKLOAD_IMAGE", "required for compose-run", "", "", "Docker Compose workload image."},
			{"WORKLOAD_COMMAND", "required for compose-run", "", "", "Command run in the Compose workload service."},
			{"PG_SOURCE_ACTION", "optional for pg-source-check", "run", "plan, run, scan", "PostgreSQL source-check action."},
			{"PG_PATCHSET", "optional for pg-source-check", "", "", "Named patchset under `patchsets/<name>/<pg-ref>`."},
			{"PG_PATCH_DIR", "optional for pg-source-check", "", "", "Ad hoc patch directory; overrides the directory from `PG_PATCHSET`."},
			{"PG_CHECK_TARGET", "optional for pg-source-check", "check", "", "Make target for PostgreSQL source tests."},
			{"PG_CLONE_DEPTH", "optional for pg-source-check", "1", "", "Git clone depth for PostgreSQL source."},
			{"PG_CONFIGURE_ARGS", "optional for pg-source-check", "--enable-debug --enable-cassert --enable-tap-tests", "", "Configure arguments for PostgreSQL source builds."},
			{"PG_BUILD_CFLAGS", "optional for pg-source-check", "-O0 -g", "", "CFLAGS used by PostgreSQL source builds."},
			{"PG_TEST_INITDB_EXTRA_OPTS", "optional for pg-source-check", "", "", "Extra initdb options passed to PostgreSQL source test targets."},
			{"PG_SOURCE_KEEP_GOING", "optional for pg-source-check", "1", "0, 1", "When `1`, scan artifacts even if the make target failed."},
		},
		Notes: []string{
			"Values containing `$` are treated as dynamic by the validator and are not path-checked.",
			"Tool-specific knobs can live in workload specs as long as the adapter consumes them.",
		},
	}
}

func experimentReference() KindReference {
	return KindReference{
		Kind:    "experiment",
		Summary: "Experiment specs live under `experiments/**/*.env` and orchestrate topology, setup, workload, monitoring, assertions, and artifacts.",
		Fields: []FieldReference{
			{"EXPERIMENT_NAME", "required", "", "", "Human-readable experiment name."},
			{"EXPERIMENT_TOPOLOGY", "optional", "single", "single, primary-replica, logical-replication, pgbouncer, multi-version-upgrade, source-tree", "Runtime topology."},
			{"EXPERIMENT_PG_CONFIG", "optional", "default", "", "Config directory under `configs/`."},
			{"EXPERIMENT_PROFILE", "optional", "", "", "Profile directory under `profiles/`."},
			{"EXPERIMENT_PROFILE_SIZE", "optional", "small", "small, medium, large", "Profile scale passed to profile SQL."},
			{"EXPERIMENT_PROFILE_SECONDS", "optional", "30", "", "Profile duration passed to profile SQL."},
			{"EXPERIMENT_PROFILE_SETUP", "optional", "1", "0, 1", "Run profile `00_setup.sql` before hooks/workload."},
			{"EXPERIMENT_PROFILE_RUN", "optional", "0", "0, 1", "Run profile SQL before hooks/workload."},
			{"EXPERIMENT_PROFILE_RUN_SQL", "optional", "10_run.sql", "", "Profile SQL file used when `EXPERIMENT_PROFILE_RUN=1`."},
			{"EXPERIMENT_DATASET_SPEC", "optional", "", "", "Dataset spec loaded before profile/workload execution."},
			{"EXPERIMENT_DATASET_SIZE", "optional", "small", "small, medium, large", "Dataset size passed to dataset loader."},
			{"EXPERIMENT_WORKLOAD_SPEC", "optional", "", "", "Foreground workload spec."},
			{"EXPERIMENT_BACKGROUND_SPECS", "optional", "", "", "Space-separated background workload specs."},
			{"EXPERIMENT_BACKGROUND_WARMUP", "optional", "0", "", "Seconds to wait after background workloads start."},
			{"EXPERIMENT_BACKGROUND_WAIT", "optional", "0", "0, 1", "Wait for background workloads before after-hooks."},
			{"EXPERIMENT_TRUSTED_SHELL", "optional", "0", "0, 1", "Explicitly allow configured host-shell hooks."},
			{"EXPERIMENT_BEFORE_SQL_FILES", "optional", "", "", "Space-separated SQL files run before snapshots/workload."},
			{"EXPERIMENT_BEFORE_SQL", "optional", "", "", "Inline SQL run before snapshots/workload."},
			{"EXPERIMENT_BEFORE_SHELL", "optional", "", "", "Trusted host-shell hook run before snapshots/workload; requires `EXPERIMENT_TRUSTED_SHELL=1`."},
			{"EXPERIMENT_AFTER_SQL_FILES", "optional", "", "", "Space-separated SQL files run after workload."},
			{"EXPERIMENT_AFTER_SQL", "optional", "", "", "Inline SQL run after workload."},
			{"EXPERIMENT_AFTER_SHELL", "optional", "", "", "Trusted host-shell hook run after workload; requires `EXPERIMENT_TRUSTED_SHELL=1`."},
			{"EXPERIMENT_ASSERT_SQL_FILES", "optional", "", "", "Space-separated SQL assertion files."},
			{"EXPERIMENT_ASSERT_SQL", "optional", "", "", "Inline SQL assertion."},
			{"EXPERIMENT_ASSERT_TRUE_SQL", "optional", "", "", "Boolean SQL assertion that must return exactly one `t` row."},
			{"EXPERIMENT_ASSERT_SHELL", "optional", "", "", "Trusted host-shell assertion hook; requires `EXPERIMENT_TRUSTED_SHELL=1`."},
			{"EXPERIMENT_METRICS", "optional", "1", "0, 1", "Enable metrics sampling."},
			{"EXPERIMENT_METRICS_INTERVAL", "optional", "1", "", "Metrics sampling interval in seconds."},
			{"EXPERIMENT_METRICS_DURATION", "optional", "30", "", "Metrics sampling duration in seconds."},
			{"EXPERIMENT_METRICS_SAMPLES", "optional", "", "", "Fixed sample count; overrides duration loop."},
			{"EXPERIMENT_SNAPSHOT", "optional", "1", "0, 1", "Capture before/after PostgreSQL snapshots."},
			{"EXPERIMENT_RUNTIME_RESET", "optional", "0", "0, 1", "Reset the selected disposable runtime before the run."},
			{"EXPERIMENT_DOCKER_RESET", "legacy alias", "0", "0, 1", "Compatibility alias for `EXPERIMENT_RUNTIME_RESET`."},
			{"EXPERIMENT_STATE_WRITER", "optional", "go", "auto, go", "State-file writer mode for manifest and verdict artifacts. `auto` is a compatibility alias for `go`; legacy `shell` is rejected for versioned evidence."},
			{"EXPERIMENT_TIMEOUT", "optional", "6h", "positive Go duration", "Runner execution deadline; CLI `--timeout` and `PGWORKBENCH_EXECUTION_TIMEOUT` take precedence."},
			{"EXPERIMENT_SCAN_PATHS", "optional", "run directory", "", "Paths scanned for failure evidence."},
			{"EXPERIMENT_RUN_ID", "optional", "generated", "", "Explicit run id."},
		},
		Notes: []string{
			"Keep interpretation profile-local; experiment specs should describe orchestration.",
			"Foreground and background workload specs use the workload contract.",
			"Declarative SQL hooks do not require shell trust. Any host-shell hook fails closed unless `EXPERIMENT_TRUSTED_SHELL=1`; this marker is not a sandbox for the sourced env spec.",
		},
	}
}

func benchmarkReference() KindReference {
	return KindReference{
		Kind:    "benchmark",
		Summary: "Benchmark specs live under `benchmarks/**/*.env` and define one immutable pgbench measurement protocol over an existing experiment and workload.",
		Fields: []FieldReference{
			{"BENCHMARK_NAME", "required", "", "", "Human-readable benchmark name."},
			{"BENCHMARK_CONTRACT_VERSION", "optional", "1", "1, 2", "Explicit protocol contract. Version 1 preserves declaration-only legacy controls; version 2 opts in to runner-produced control evidence and strict supported modes."},
			{"BENCHMARK_CLASS", "optional", "measurement", "smoke, measurement", "Evidence class. Smoke exercises the contract but is not performance evidence."},
			{"BENCHMARK_DRIVER", "optional", "pgbench", "pgbench", "Measurement driver; benchmark contracts v1 and v2 support pgbench only."},
			{"BENCHMARK_TARGET", "optional", "direct-postgres", "direct-postgres, pgbouncer", "Digest-bound pgbench endpoint contract. `direct-postgres` requires the `single` topology and supports Docker or native execution; `pgbouncer` requires the `pgbouncer` topology and Docker execution."},
			{"BENCHMARK_EXPERIMENT_SPEC", "required", "", "", "Experiment spec that owns the disposable runtime and prepared database state."},
			{"BENCHMARK_WORKLOAD_SPEC", "required", "", "pgbench workload", "Workload spec used for the measured pgbench command."},
			{"BENCHMARK_PG_CONFIG", "optional", "default", "", "PostgreSQL config profile applied to benchmark trials."},
			{"BENCHMARK_MODE", "optional", "fixed-time", "fixed-time, fixed-transactions", "Termination mode for each measured pgbench trial."},
			{"BENCHMARK_SCALE", "required", "", "positive integer", "Pgbench scale factor recorded in the protocol."},
			{"BENCHMARK_CLIENTS", "required", "", "positive integer", "Pgbench client count recorded in the protocol."},
			{"BENCHMARK_THREADS", "required", "", "positive integer", "Pgbench worker thread count recorded in the protocol."},
			{"BENCHMARK_WARMUP_SECONDS", "optional", "0", "non-negative integer", "Warmup duration inside each trial; excluded from the measured result."},
			{"BENCHMARK_MEASURE_SECONDS", "required for fixed-time", "", "positive integer", "Measured duration for fixed-time trials."},
			{"BENCHMARK_TRANSACTIONS_PER_CLIENT", "required for fixed-transactions", "", "positive integer", "Measured transaction count per client; incompatible with BENCHMARK_MEASURE_SECONDS."},
			{"BENCHMARK_TRIALS", "optional", "smoke: 1; measurement: 10", "positive integer", "Number of measured trials."},
			{"BENCHMARK_MIN_VALID_TRIALS", "optional", "BENCHMARK_TRIALS", "positive integer", "Minimum valid trials required for an aggregate result; cannot exceed BENCHMARK_TRIALS."},
			{"BENCHMARK_RESET_POLICY", "optional", "rebuild-per-trial", "rebuild-per-trial, reuse-readonly", "Database-state policy between trials. Reuse is restricted to pgbench select-only workloads."},
			{"BENCHMARK_CACHE_REGIME", "required", "", "v1: uncontrolled, cold, warm, steady; v2: uncontrolled, postgres-shared-buffer-warm", "V1 is declaration-only. V2 can gate exact target-relation residency in PostgreSQL shared buffers; neither contract controls the operating-system page cache."},
			{"BENCHMARK_CACHE_TARGET_RELATIONS", "required for v2 postgres-shared-buffer-warm", "", "space-separated schema.relation identifiers", "Exact target relation set whose shared-buffer residency must be evidenced before measure."},
			{"BENCHMARK_CACHE_MIN_RESIDENT_PCT", "required for v2 postgres-shared-buffer-warm", "", "decimal in (0, 100]", "Minimum pg_buffercache resident-block percentage required for every target relation."},
			{"BENCHMARK_STATISTICS_RESET_POLICY", "required", "", "v1: none, operator-managed; v2: none, runner-managed", "V1 records operator-managed resets without proof. V2 runner-managed mode executes and verifies current-database and WAL reset evidence at the declared boundary."},
			{"BENCHMARK_STATISTICS_RESET_BOUNDARY", "required", "", "none, before-trial, before-warmup, before-measure", "Declared reset boundary; must be none exactly when the reset policy is none."},
			{"BENCHMARK_COLLECTORS", "required", "", "v1: pgbench-driver postgresql-sampler-v1; v2: pgbench-driver postgresql-sampler-v2", "Exact space-separated collector set selected by the explicit protocol contract."},
			{"BENCHMARK_COLLECTOR_INTERVAL_SECONDS", "required", "", "positive integer; v2 maximum 3600", "PostgreSQL sampler interval in seconds; the benchmark runner passes this value to the sampler."},
			{"BENCHMARK_COLLECTOR_OVERHEAD_MODE", "required", "", "v1: included-unquantified, operator-calibrated; v2: included-unquantified, runner-calibrated-duty-cycle", "V1 operator calibration is declaration-only. V2 runner calibration retains exact-cadence raw timing rows and independently derives the duty-cycle gate."},
			{"BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES", "required for v2 runner-calibrated-duty-cycle", "", "integer in [1, 10000]", "Minimum number of raw monotonic sampler timing rows required for the duty-cycle gate."},
			{"BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT", "required for v2 runner-calibrated-duty-cycle", "", "decimal in (0, 100]", "Maximum permitted per-sample collector duty cycle; every retained sample participates."},
			{"BENCHMARK_CLIENT_PLACEMENT", "required", "", "same-host, separate-host, remote-host", "Declared pgbench client placement. Ordinary runs do not attest it; v2 runner-enforced single-container resources require same-host, and counterbalanced A/B requires matching strict qualification bookends."},
			{"BENCHMARK_RESOURCE_BUDGET_MODE", "required", "", "v1: unbounded, operator-declared; v2: unbounded, runner-enforced", "V1 records but does not enforce limits. V2 runner enforcement is restricted to the exact Docker single-container Linux cgroup-v2 provider."},
			{"BENCHMARK_CPU_BUDGET_CORES", "required for operator-declared", "", "positive decimal", "Declared CPU core budget; forbidden when the resource budget mode is unbounded."},
			{"BENCHMARK_CPU_BUDGET_MILLICORES", "required for v2 runner-enforced", "", "positive integer", "Runner-enforced Docker CPU budget in integer millicores; v2 forbids BENCHMARK_CPU_BUDGET_CORES."},
			{"BENCHMARK_MEMORY_BUDGET_MIB", "required for v1 operator-declared or v2 runner-enforced", "", "positive integer", "Memory budget in MiB; forbidden when the resource budget mode is unbounded."},
			{"BENCHMARK_RESOURCE_BUDGET_SCOPE", "required for v2 runner-enforced", "", "postgres-server-and-pgbench-driver", "Fixed v2 scope: server and in-container pgbench share the enforced Docker limit."},
			{"BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER", "required for v2 runner-enforced", "", "docker-single-container-linux-cgroup-v2", "Exact supported provider. Native runs and other resource-control providers fail closed."},
			{"BENCHMARK_PRIMARY_METRIC", "optional", "pgbench.tps", "pgbench.tps, pgbench.latency_mean_us", "Primary metric used by summaries and comparisons."},
			{"BENCHMARK_DIRECTION", "optional", "derived from primary metric", "higher, lower", "Improvement direction: TPS is higher, mean latency is lower."},
			{"BENCHMARK_MAX_CV_PCT", "optional", "10", "positive decimal", "Maximum coefficient of variation for a stable measured series."},
			{"BENCHMARK_REGRESSION_THRESHOLD_PCT", "optional", "", "non-negative decimal", "Optional material-regression threshold; omission keeps comparisons descriptive."},
			{"BENCHMARK_RATE", "optional", "", "positive decimal", "Optional target transaction rate passed to pgbench."},
			{"BENCHMARK_LATENCY_LIMIT_MS", "optional", "", "positive decimal", "Optional pgbench latency limit in milliseconds."},
			{"BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT", "optional", "", "decimal in [0, 100]", "Maximum allowed percentage of completed transactions above the latency limit; exceeding it invalidates the trial."},
			{"BENCHMARK_CONNECT_PER_TRANSACTION", "optional", "0", "0, 1", "Open a new database connection for every pgbench transaction and require reconnect-specific result evidence. This mode must use mean latency as its primary metric because its TPS includes reconnection time."},
			{"BENCHMARK_PROTOCOL", "optional", "simple", "simple, extended, prepared", "Pgbench query protocol."},
			{"BENCHMARK_RANDOM_SEED", "optional", "", "integer in [0, 9223372036854775807]", "Explicit deterministic measure seed N; warmup uses N+1, with maximum-int wrap to 0, and both derived phase seeds are protocol-bound."},
			{"BENCHMARK_MAX_TRIES", "optional", "", "non-negative integer", "Optional maximum transaction tries. Zero means unlimited and requires fixed-time mode or a latency limit."},
			{"BENCHMARK_LOG_TRANSACTIONS", "optional", "smoke: 0; measurement: 1", "0, 1", "Capture raw per-transaction pgbench logs for audit and later percentile analysis."},
			{"BENCHMARK_LOG_SAMPLE_RATE", "optional", "1", "decimal in (0, 1]", "Sampling rate used when transaction logging is enabled."},
			{"BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES", "optional", "pg_config", "pg_config, native_toolchain", "The one explicit subject dimension permitted by counterbalanced A/B; native_toolchain requires two byte-distinct seven-file executable snapshots with matching observed versions for all seven selected tools."},
		},
		Notes: []string{
			"Benchmark protocol fields must be static values; runtime shell expansion would make the protocol digest ambiguous.",
			"The protocol digest binds resolved parameters and referenced spec content. It is not a claim that two hosts are performance-comparable.",
			"Target identity, endpoint-contract version, topology id, and topology-spec digest enter both the protocol and comparison-key digests. Direct and PgBouncer series are distinct populations, not interchangeable baselines.",
			"`BENCHMARK_CONNECT_PER_TRANSACTION=1` must use `pgbench.latency_mean_us`; reconnect TPS includes connection setup and is retained as secondary evidence rather than relabeled as ordinary `pgbench.tps`.",
			"Cache, operator-managed statistics resets, collector calibration, client placement outside qualified A/B, and resource budgets are recorded declarations unless a runtime gate explicitly verifies them.",
			"`BENCHMARK_CONTRACT_VERSION=2` never reinterprets v1 warm, cold, steady, operator-managed, operator-calibrated, or operator-declared declarations; unsupported v2 control modes fail closed.",
			"Use BENCHMARK_CLASS=smoke only for fast contract and portability checks.",
		},
	}
}

func matrixReference() KindReference {
	return KindReference{
		Kind:    "matrix",
		Summary: "Matrix specs live under `matrices/**/*.env` and expand experiments across config/profile-size/repeat combinations.",
		Fields: []FieldReference{
			{"MATRIX_NAME", "required", "", "", "Human-readable matrix name."},
			{"MATRIX_EXPERIMENTS", "optional", "smoke", "", "Space-separated experiment specs."},
			{"MATRIX_PG_CONFIGS", "optional", "default", "", "Space-separated PostgreSQL config profiles."},
			{"MATRIX_PROFILE_SIZES", "optional", "small", "small, medium, large", "Space-separated profile sizes."},
			{"MATRIX_REPEATS", "optional", "1", "positive integer", "Repeat count per combination."},
			{"MATRIX_STOP_ON_FAIL", "optional", "0", "0, 1", "Stop matrix after first failed run."},
			{"MATRIX_RUNTIME_RESET", "optional", "0", "0, 1", "Reset the selected runtime before each run."},
			{"MATRIX_DOCKER_RESET", "legacy alias", "0", "0, 1", "Compatibility alias for `MATRIX_RUNTIME_RESET`."},
			{"MATRIX_RUN_ID", "optional", "generated", "", "Explicit matrix run id."},
			{"MATRIX_RUN_DIR", "optional", "runs/matrices/<id>", "", "Explicit matrix artifact directory."},
		},
	}
}

func topologyReference() KindReference {
	return KindReference{
		Kind:    "topology",
		Summary: "Topology specs live under `topologies/**/*.env` and describe supported runtime shapes.",
		Fields: []FieldReference{
			{"TOPOLOGY_NAME", "required", "", "single, primary-replica, logical-replication, pgbouncer, multi-version-upgrade", "Topology id; must match the spec id."},
			{"TOPOLOGY_DESCRIPTION", "required", "", "", "Human-readable topology description."},
		},
		Notes: []string{
			"Topology implementation remains in shell/Docker Compose adapters.",
		},
	}
}

func datasetReference() KindReference {
	return KindReference{
		Kind:    "dataset",
		Summary: "Dataset specs live under `datasets/**/*.env` and load reusable data before an experiment workload.",
		Fields: []FieldReference{
			{"DATASET_NAME", "required", "", "", "Human-readable dataset name."},
			{"DATASET_KIND", "required", "", "sql, profile, pgbench", "Dataset loader adapter."},
			{"DATASET_SQL", "required for sql", "", "", "Repository or absolute SQL path."},
			{"DATASET_PROFILE", "required for profile", "", "", "Profile setup SQL used as dataset source."},
			{"DATASET_SIZE", "optional", "small", "small, medium, large", "Dataset/profile size override."},
			{"DATASET_SCHEMA", "optional for sql", "dataset_synthetic", "", "Target schema variable passed to dataset SQL."},
			{"DATASET_ROWS", "optional for sql", "10000", "", "Row count variable passed to dataset SQL."},
			{"DATASET_SEED", "optional for sql", "1", "", "Seed variable passed to dataset SQL."},
			{"DATASET_SCALE", "optional for pgbench", "1", "", "Pgbench initialization scale."},
		},
	}
}

func utilityTestReference() KindReference {
	return KindReference{
		Kind:    "utility-test",
		Summary: "Utility test specs live under `utility-tests/**/*.env` and describe a reusable PostgreSQL utility/tool test scenario.",
		Fields: []FieldReference{
			{"UTILITY_TEST_NAME", "required", "", "", "Human-readable utility test name."},
			{"UTILITY_TEST_WORKLOAD_SPEC", "required", "", "", "Foreground workload spec that invokes the utility or external tool."},
			{"UTILITY_TEST_PROFILE", "optional", "", "", "Profile directory under `profiles/` used to prepare database state."},
			{"UTILITY_TEST_PROFILE_SIZE", "optional", "small", "small, medium, large", "Profile scale passed to setup SQL."},
			{"UTILITY_TEST_PROFILE_SECONDS", "optional", "30", "", "Profile duration passed to setup/run SQL when used."},
			{"UTILITY_TEST_DATASET_SPEC", "optional", "", "", "Dataset spec loaded before background and utility workloads."},
			{"UTILITY_TEST_DATASET_SIZE", "optional", "small", "small, medium, large", "Dataset size passed to the dataset loader."},
			{"UTILITY_TEST_BACKGROUND_SPECS", "optional", "", "", "Space-separated background workload specs started before the utility workload."},
			{"UTILITY_TEST_BACKGROUND_WARMUP", "optional", "0", "", "Seconds to wait after background workloads start."},
			{"UTILITY_TEST_BACKGROUND_WAIT", "optional", "0", "0, 1", "Wait for background workloads after the foreground utility workload."},
			{"UTILITY_TEST_METRICS", "optional", "1", "0, 1", "Enable metrics sampling during the utility test."},
			{"UTILITY_TEST_METRICS_INTERVAL", "optional", "1", "", "Metrics sampling interval in seconds."},
			{"UTILITY_TEST_METRICS_DURATION", "optional", "30", "", "Metrics sampling duration in seconds."},
			{"UTILITY_TEST_METRICS_SAMPLES", "optional", "", "", "Fixed metrics sample count; overrides duration loop."},
			{"UTILITY_TEST_TRUSTED_SHELL", "optional", "0", "0, 1", "Explicitly allow utility assertions that execute in the host shell."},
			{"UTILITY_TEST_EXPECT_FILES", "optional", "", "", "Space-separated output files checked in the host shell; requires `UTILITY_TEST_TRUSTED_SHELL=1`."},
			{"UTILITY_TEST_ASSERT_SQL_FILES", "optional", "", "", "Space-separated SQL assertion files run after the utility workload."},
			{"UTILITY_TEST_ASSERT_SQL", "optional", "", "", "Inline SQL assertion run after the utility workload."},
			{"UTILITY_TEST_ASSERT_TRUE_SQL", "optional", "", "", "Boolean SQL assertion that must return exactly one `t` row."},
			{"UTILITY_TEST_ASSERT_SHELL", "optional", "", "", "Trusted host-shell assertion run after the utility workload; requires `UTILITY_TEST_TRUSTED_SHELL=1`."},
			{"UTILITY_TEST_SCAN_PATHS", "optional", "run directory", "", "Extra paths scanned for failure evidence."},
			{"UTILITY_TEST_NOTES", "optional", "", "", "Short operator notes for expected evidence or caveats."},
		},
		Notes: []string{
			"Use utility tests for pg_dump, pg_restore, pg_upgrade, external backup tools, data checkers, fuzzers, and other PostgreSQL-adjacent utilities.",
			"Values containing `$` are treated as dynamic by the validator and are not path-checked.",
			"SQL assertions do not require shell trust. `UTILITY_TEST_EXPECT_FILES` currently compiles to host-shell `test -s` checks and shares the explicit trust gate with `UTILITY_TEST_ASSERT_SHELL`.",
		},
	}
}

func utilitySuiteReference() KindReference {
	return KindReference{
		Kind:    "utility-suite",
		Summary: "Utility suite specs live under `utility-suites/**/*.env` and batch utility-test specs across profile sizes and repeats.",
		Fields: []FieldReference{
			{"UTILITY_SUITE_NAME", "required", "", "", "Human-readable utility suite name."},
			{"UTILITY_SUITE_TESTS", "required", "", "", "Space-separated utility-test specs."},
			{"UTILITY_SUITE_PROFILE_SIZES", "optional", "small", "small, medium, large", "Space-separated profile sizes passed through `PROFILE_SIZE`."},
			{"UTILITY_SUITE_REPEATS", "optional", "1", "positive integer", "Repeat count per utility-test/profile-size combination."},
			{"UTILITY_SUITE_STOP_ON_FAIL", "optional", "0", "0, 1", "Stop suite execution after the first failed utility test run."},
			{"UTILITY_SUITE_SNAPSHOT", "optional", "1", "0, 1", "Snapshot toggle passed through as `UTILITY_TEST_SNAPSHOT`."},
			{"UTILITY_SUITE_RUN_ID", "optional", "generated", "", "Explicit utility suite run id."},
			{"UTILITY_SUITE_RUN_DIR", "optional", "runs/utility-suites/<id>", "", "Explicit utility suite artifact directory."},
		},
		Notes: []string{
			"Suite runs delegate each entry to `pgworkbench utility run`, so per-test artifacts still live under `runs/<run-id>/`.",
		},
	}
}

func tableCell(value string) string {
	if value == "" {
		return "-"
	}
	return strings.ReplaceAll(value, "|", `\|`)
}
