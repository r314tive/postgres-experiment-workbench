package benchmarkplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

const (
	ProtocolSchemaVersion   = "pgworkbench.benchmark-protocol/v1"
	ProtocolSchemaVersionV2 = "pgworkbench.benchmark-protocol/v2"
	TargetDirectPostgres    = "direct-postgres"
	TargetPgBouncer         = "pgbouncer"
	EndpointDirectV1        = "pgworkbench.pgbench-target/direct-postgres/v1"
	EndpointPgBouncerV1     = "pgworkbench.pgbench-target/pgbouncer/v1"
	v2MaxCollectorInterval  = 3600
	v2MaxOverheadSamples    = 10_000
	v2MaxCPUMillicores      = int64(math.MaxInt64 / 1_000_000)
	v2MaxMemoryMiB          = int64(math.MaxInt64 / (1024 * 1024))
)

type Plan struct {
	ProtocolSchemaVersion       string   `json:"protocol_schema_version"`
	ContractVersion             string   `json:"contract_version,omitempty"`
	Spec                        string   `json:"spec"`
	SpecPath                    string   `json:"spec_path"`
	SpecDigest                  string   `json:"spec_digest"`
	Name                        string   `json:"name"`
	Class                       string   `json:"class"`
	Driver                      string   `json:"driver"`
	Target                      string   `json:"target"`
	TargetEndpointContract      string   `json:"target_endpoint_contract"`
	TargetTopology              string   `json:"target_topology"`
	TargetTopologyPath          string   `json:"target_topology_path"`
	TargetTopologyDigest        string   `json:"target_topology_digest"`
	ExperimentSpec              string   `json:"experiment_spec"`
	ExperimentPath              string   `json:"experiment_path"`
	ExperimentDigest            string   `json:"experiment_digest"`
	WorkloadSpec                string   `json:"workload_spec"`
	WorkloadPath                string   `json:"workload_path"`
	WorkloadDigest              string   `json:"workload_digest"`
	WorkloadMode                string   `json:"workload_mode"`
	WorkloadScript              string   `json:"workload_script,omitempty"`
	WorkloadScriptDigest        string   `json:"workload_script_digest,omitempty"`
	PGConfig                    string   `json:"pg_config"`
	PGConfigPath                string   `json:"pg_config_path"`
	PGConfigDigest              string   `json:"pg_config_digest"`
	Mode                        string   `json:"mode"`
	Scale                       int      `json:"scale"`
	Clients                     int      `json:"clients"`
	Threads                     int      `json:"threads"`
	WarmupSeconds               int      `json:"warmup_seconds"`
	MeasureSeconds              int      `json:"measure_seconds,omitempty"`
	TransactionsPerClient       int      `json:"transactions_per_client,omitempty"`
	Trials                      int      `json:"trials"`
	MinValidTrials              int      `json:"min_valid_trials"`
	ResetPolicy                 string   `json:"reset_policy"`
	RuntimeReset                bool     `json:"runtime_reset"`
	CacheRegime                 string   `json:"cache_regime"`
	CacheTargetRelations        []string `json:"cache_target_relations,omitempty"`
	CacheMinResidentPct         *float64 `json:"cache_min_resident_pct,omitempty"`
	StatisticsResetPolicy       string   `json:"statistics_reset_policy"`
	StatisticsResetBoundary     string   `json:"statistics_reset_boundary"`
	Collectors                  []string `json:"collectors"`
	CollectorIntervalSeconds    int      `json:"collector_interval_seconds"`
	CollectorOverheadMode       string   `json:"collector_overhead_mode"`
	CollectorOverheadSamples    *int     `json:"collector_overhead_samples,omitempty"`
	CollectorMaxDutyCyclePct    *float64 `json:"collector_max_duty_cycle_pct,omitempty"`
	ClientPlacement             string   `json:"client_placement"`
	ResourceBudgetMode          string   `json:"resource_budget_mode"`
	CPUBudgetCores              *float64 `json:"cpu_budget_cores,omitempty"`
	CPUBudgetMillicores         *int     `json:"cpu_budget_millicores,omitempty"`
	MemoryBudgetMiB             *int     `json:"memory_budget_mib,omitempty"`
	ResourceBudgetScope         string   `json:"resource_budget_scope,omitempty"`
	ResourceEnforcementProvider string   `json:"resource_enforcement_provider,omitempty"`
	ResourceProviderConstraints []string `json:"resource_provider_constraints,omitempty"`
	PrimaryMetric               string   `json:"primary_metric"`
	Direction                   string   `json:"direction"`
	MaxCVPct                    float64  `json:"max_cv_pct"`
	RegressionThresholdPct      *float64 `json:"regression_threshold_pct,omitempty"`
	Rate                        *float64 `json:"rate,omitempty"`
	LatencyLimitMS              *float64 `json:"latency_limit_ms,omitempty"`
	MaxLatencyLimitExceededPct  *float64 `json:"max_latency_limit_exceeded_pct,omitempty"`
	ConnectPerTransaction       bool     `json:"connect_per_transaction"`
	QueryProtocol               string   `json:"query_protocol"`
	RandomSeed                  *uint64  `json:"random_seed,omitempty"`
	RandomSeedSemantics         string   `json:"random_seed_semantics"`
	WarmupRandomSeed            *uint64  `json:"warmup_random_seed,omitempty"`
	MeasureRandomSeed           *uint64  `json:"measure_random_seed,omitempty"`
	MaxTries                    *int     `json:"max_tries,omitempty"`
	LogTransactions             bool     `json:"log_transactions"`
	LogSampleRate               float64  `json:"log_sample_rate"`
	AllowedSubjectDifferences   []string `json:"allowed_subject_differences"`
	ProtocolDigest              string   `json:"protocol_digest"`
	ComparisonKeyDigest         string   `json:"comparison_key_digest"`
}

type protocolIdentity struct {
	SchemaVersion               string   `json:"schema_version"`
	ContractVersion             string   `json:"contract_version,omitempty"`
	Class                       string   `json:"class"`
	Driver                      string   `json:"driver"`
	Target                      string   `json:"target"`
	TargetEndpointContract      string   `json:"target_endpoint_contract"`
	TargetTopology              string   `json:"target_topology"`
	TargetTopologyDigest        string   `json:"target_topology_digest"`
	ExperimentSpec              string   `json:"experiment_spec"`
	ExperimentDigest            string   `json:"experiment_digest"`
	WorkloadSpec                string   `json:"workload_spec"`
	WorkloadDigest              string   `json:"workload_digest"`
	WorkloadMode                string   `json:"workload_mode"`
	WorkloadScript              string   `json:"workload_script,omitempty"`
	WorkloadScriptDigest        string   `json:"workload_script_digest,omitempty"`
	PGConfig                    string   `json:"pg_config"`
	PGConfigDigest              string   `json:"pg_config_digest"`
	Mode                        string   `json:"mode"`
	Scale                       int      `json:"scale"`
	Clients                     int      `json:"clients"`
	Threads                     int      `json:"threads"`
	WarmupSeconds               int      `json:"warmup_seconds"`
	MeasureSeconds              int      `json:"measure_seconds,omitempty"`
	TransactionsPerClient       int      `json:"transactions_per_client,omitempty"`
	Trials                      int      `json:"trials"`
	MinValidTrials              int      `json:"min_valid_trials"`
	ResetPolicy                 string   `json:"reset_policy"`
	RuntimeReset                bool     `json:"runtime_reset"`
	CacheRegime                 string   `json:"cache_regime"`
	CacheTargetRelations        []string `json:"cache_target_relations,omitempty"`
	CacheMinResidentPct         *float64 `json:"cache_min_resident_pct,omitempty"`
	StatisticsResetPolicy       string   `json:"statistics_reset_policy"`
	StatisticsResetBoundary     string   `json:"statistics_reset_boundary"`
	Collectors                  []string `json:"collectors"`
	CollectorIntervalSeconds    int      `json:"collector_interval_seconds"`
	CollectorOverheadMode       string   `json:"collector_overhead_mode"`
	CollectorOverheadSamples    *int     `json:"collector_overhead_samples,omitempty"`
	CollectorMaxDutyCyclePct    *float64 `json:"collector_max_duty_cycle_pct,omitempty"`
	ClientPlacement             string   `json:"client_placement"`
	ResourceBudgetMode          string   `json:"resource_budget_mode"`
	CPUBudgetCores              *float64 `json:"cpu_budget_cores,omitempty"`
	CPUBudgetMillicores         *int     `json:"cpu_budget_millicores,omitempty"`
	MemoryBudgetMiB             *int     `json:"memory_budget_mib,omitempty"`
	ResourceBudgetScope         string   `json:"resource_budget_scope,omitempty"`
	ResourceEnforcementProvider string   `json:"resource_enforcement_provider,omitempty"`
	ResourceProviderConstraints []string `json:"resource_provider_constraints,omitempty"`
	PrimaryMetric               string   `json:"primary_metric"`
	Direction                   string   `json:"direction"`
	MaxCVPct                    float64  `json:"max_cv_pct"`
	RegressionThresholdPct      *float64 `json:"regression_threshold_pct,omitempty"`
	Rate                        *float64 `json:"rate,omitempty"`
	LatencyLimitMS              *float64 `json:"latency_limit_ms,omitempty"`
	MaxLatencyLimitExceededPct  *float64 `json:"max_latency_limit_exceeded_pct,omitempty"`
	ConnectPerTransaction       bool     `json:"connect_per_transaction"`
	QueryProtocol               string   `json:"query_protocol"`
	RandomSeed                  *uint64  `json:"random_seed,omitempty"`
	RandomSeedSemantics         string   `json:"random_seed_semantics"`
	WarmupRandomSeed            *uint64  `json:"warmup_random_seed,omitempty"`
	MeasureRandomSeed           *uint64  `json:"measure_random_seed,omitempty"`
	MaxTries                    *int     `json:"max_tries,omitempty"`
	LogTransactions             bool     `json:"log_transactions"`
	LogSampleRate               float64  `json:"log_sample_rate"`
	AllowedSubjectDifferences   []string `json:"allowed_subject_differences"`
}

func Build(catalog speccatalog.Catalog, input string) (Plan, error) {
	spec, err := catalog.Show("benchmark", input)
	if err != nil {
		return Plan{}, err
	}
	if errs := catalog.Validate("benchmark", []string{spec.ID}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}

	values := spec.Values
	contractVersion := valueOr(values["BENCHMARK_CONTRACT_VERSION"], "1")
	protocolSchemaVersion := ProtocolSchemaVersion
	planContractVersion := ""
	if contractVersion == "2" {
		protocolSchemaVersion = ProtocolSchemaVersionV2
		planContractVersion = "2"
	}
	class := valueOr(values["BENCHMARK_CLASS"], "measurement")
	target := valueOr(values["BENCHMARK_TARGET"], TargetDirectPostgres)
	targetTopology, targetEndpointContract, err := TargetContract(target)
	if err != nil {
		return Plan{}, err
	}
	targetTopologyPath, err := filepath.Abs(filepath.Join(catalog.Root, "topologies", targetTopology+".env"))
	if err != nil {
		return Plan{}, fmt.Errorf("resolve benchmark target topology: %w", err)
	}
	experiment, err := catalog.Show("experiment", values["BENCHMARK_EXPERIMENT_SPEC"])
	if err != nil {
		return Plan{}, err
	}
	workload, err := catalog.Show("workload", values["BENCHMARK_WORKLOAD_SPEC"])
	if err != nil {
		return Plan{}, err
	}

	pgConfig := valueOr(values["BENCHMARK_PG_CONFIG"], "default")
	pgConfigPath := filepath.Join(catalog.Root, "configs", pgConfig, "postgresql.conf")
	workloadScript := workload.Values["PGBENCH_SCRIPT"]
	workloadScriptDigest := ""
	if workloadScript != "" {
		workloadScriptDigest, err = digestFile(resolvePath(catalog.Root, workloadScript))
		if err != nil {
			return Plan{}, fmt.Errorf("digest pgbench script: %w", err)
		}
	}

	mode := valueOr(values["BENCHMARK_MODE"], "fixed-time")
	trialsDefault := 10
	if class == "smoke" {
		trialsDefault = 1
	}
	trials := intOr(values["BENCHMARK_TRIALS"], trialsDefault)
	minValidTrials := intOr(values["BENCHMARK_MIN_VALID_TRIALS"], trials)
	primaryMetric := valueOr(values["BENCHMARK_PRIMARY_METRIC"], "pgbench.tps")
	directionDefault := "higher"
	if primaryMetric == "pgbench.latency_mean_us" {
		directionDefault = "lower"
	}
	resetPolicy := valueOr(values["BENCHMARK_RESET_POLICY"], "rebuild-per-trial")
	logTransactionsDefault := class == "measurement"
	allowedDifferences := strings.Fields(valueOr(values["BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES"], "pg_config"))
	sort.Strings(allowedDifferences)
	collectors := strings.Fields(values["BENCHMARK_COLLECTORS"])
	sort.Strings(collectors)
	cacheTargetRelations := strings.Fields(values["BENCHMARK_CACHE_TARGET_RELATIONS"])
	sort.Strings(cacheTargetRelations)
	resourceProviderConstraints := []string(nil)
	if values["BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER"] == "docker-single-container-linux-cgroup-v2" {
		resourceProviderConstraints = []string{"cgroup-v2-required", "docker-engine-required", "linux-only", "postgres-and-driver-share-one-container"}
	}
	randomSeed := optionalUint64(values["BENCHMARK_RANDOM_SEED"])
	randomSeedSemantics := "client-random-default"
	var warmupRandomSeed, measureRandomSeed *uint64
	if randomSeed != nil {
		if *randomSeed > math.MaxInt64 {
			return Plan{}, fmt.Errorf("BENCHMARK_RANDOM_SEED must be at most %d", int64(math.MaxInt64))
		}
		measure := *randomSeed
		warmup := measure + 1
		if measure == math.MaxInt64 {
			warmup = 0
		}
		warmupRandomSeed, measureRandomSeed = &warmup, &measure
		randomSeedSemantics = "phase-split-offset-v1"
	}

	plan := Plan{
		ProtocolSchemaVersion:       protocolSchemaVersion,
		ContractVersion:             planContractVersion,
		Spec:                        spec.ID,
		SpecPath:                    spec.Path,
		Name:                        values["BENCHMARK_NAME"],
		Class:                       class,
		Driver:                      valueOr(values["BENCHMARK_DRIVER"], "pgbench"),
		Target:                      target,
		TargetEndpointContract:      targetEndpointContract,
		TargetTopology:              targetTopology,
		TargetTopologyPath:          targetTopologyPath,
		ExperimentSpec:              experiment.ID,
		ExperimentPath:              experiment.Path,
		WorkloadSpec:                workload.ID,
		WorkloadPath:                workload.Path,
		WorkloadMode:                valueOr(workload.Values["PGBENCH_MODE"], "builtin"),
		WorkloadScript:              filepath.ToSlash(workloadScript),
		WorkloadScriptDigest:        workloadScriptDigest,
		PGConfig:                    pgConfig,
		PGConfigPath:                pgConfigPath,
		Mode:                        mode,
		Scale:                       intOr(values["BENCHMARK_SCALE"], 0),
		Clients:                     intOr(values["BENCHMARK_CLIENTS"], 0),
		Threads:                     intOr(values["BENCHMARK_THREADS"], 0),
		WarmupSeconds:               intOr(values["BENCHMARK_WARMUP_SECONDS"], 0),
		Trials:                      trials,
		MinValidTrials:              minValidTrials,
		ResetPolicy:                 resetPolicy,
		RuntimeReset:                resetPolicy == "rebuild-per-trial",
		CacheRegime:                 values["BENCHMARK_CACHE_REGIME"],
		CacheTargetRelations:        cacheTargetRelations,
		CacheMinResidentPct:         optionalFloat(values["BENCHMARK_CACHE_MIN_RESIDENT_PCT"]),
		StatisticsResetPolicy:       values["BENCHMARK_STATISTICS_RESET_POLICY"],
		StatisticsResetBoundary:     values["BENCHMARK_STATISTICS_RESET_BOUNDARY"],
		Collectors:                  collectors,
		CollectorIntervalSeconds:    intOr(values["BENCHMARK_COLLECTOR_INTERVAL_SECONDS"], 0),
		CollectorOverheadMode:       values["BENCHMARK_COLLECTOR_OVERHEAD_MODE"],
		CollectorOverheadSamples:    optionalInt(values["BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES"]),
		CollectorMaxDutyCyclePct:    optionalFloat(values["BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT"]),
		ClientPlacement:             values["BENCHMARK_CLIENT_PLACEMENT"],
		ResourceBudgetMode:          values["BENCHMARK_RESOURCE_BUDGET_MODE"],
		CPUBudgetCores:              optionalFloat(values["BENCHMARK_CPU_BUDGET_CORES"]),
		CPUBudgetMillicores:         optionalInt(values["BENCHMARK_CPU_BUDGET_MILLICORES"]),
		MemoryBudgetMiB:             optionalInt(values["BENCHMARK_MEMORY_BUDGET_MIB"]),
		ResourceBudgetScope:         values["BENCHMARK_RESOURCE_BUDGET_SCOPE"],
		ResourceEnforcementProvider: values["BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER"],
		ResourceProviderConstraints: resourceProviderConstraints,
		PrimaryMetric:               primaryMetric,
		Direction:                   valueOr(values["BENCHMARK_DIRECTION"], directionDefault),
		MaxCVPct:                    floatOr(values["BENCHMARK_MAX_CV_PCT"], 10),
		RegressionThresholdPct:      optionalFloat(values["BENCHMARK_REGRESSION_THRESHOLD_PCT"]),
		Rate:                        optionalFloat(values["BENCHMARK_RATE"]),
		LatencyLimitMS:              optionalFloat(values["BENCHMARK_LATENCY_LIMIT_MS"]),
		MaxLatencyLimitExceededPct:  optionalFloat(values["BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT"]),
		ConnectPerTransaction:       boolOr(values["BENCHMARK_CONNECT_PER_TRANSACTION"], false),
		QueryProtocol:               valueOr(values["BENCHMARK_PROTOCOL"], "simple"),
		RandomSeed:                  randomSeed,
		RandomSeedSemantics:         randomSeedSemantics,
		WarmupRandomSeed:            warmupRandomSeed,
		MeasureRandomSeed:           measureRandomSeed,
		MaxTries:                    optionalInt(values["BENCHMARK_MAX_TRIES"]),
		LogTransactions:             boolOr(values["BENCHMARK_LOG_TRANSACTIONS"], logTransactionsDefault),
		LogSampleRate:               floatOr(values["BENCHMARK_LOG_SAMPLE_RATE"], 1),
		AllowedSubjectDifferences:   allowedDifferences,
	}
	if mode == "fixed-time" {
		plan.MeasureSeconds = intOr(values["BENCHMARK_MEASURE_SECONDS"], 0)
	} else {
		plan.TransactionsPerClient = intOr(values["BENCHMARK_TRANSACTIONS_PER_CLIENT"], 0)
	}

	plan.SpecDigest, err = digestFile(spec.Path)
	if err != nil {
		return Plan{}, fmt.Errorf("digest benchmark spec: %w", err)
	}
	plan.ExperimentDigest, err = digestFile(experiment.Path)
	if err != nil {
		return Plan{}, fmt.Errorf("digest experiment spec: %w", err)
	}
	plan.WorkloadDigest, err = digestFile(workload.Path)
	if err != nil {
		return Plan{}, fmt.Errorf("digest workload spec: %w", err)
	}
	plan.PGConfigDigest, err = digestFile(pgConfigPath)
	if err != nil {
		return Plan{}, fmt.Errorf("digest PostgreSQL config: %w", err)
	}
	plan.TargetTopologyDigest, err = digestFile(plan.TargetTopologyPath)
	if err != nil {
		return Plan{}, fmt.Errorf("digest benchmark target topology: %w", err)
	}

	if err := validateDeclarations(plan); err != nil {
		return Plan{}, err
	}
	plan.ProtocolDigest, plan.ComparisonKeyDigest, err = IdentityDigests(plan)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (plan Plan) protocolIdentity() protocolIdentity {
	return protocolIdentity{
		SchemaVersion:               plan.ProtocolSchemaVersion,
		ContractVersion:             plan.ContractVersion,
		Class:                       plan.Class,
		Driver:                      plan.Driver,
		Target:                      plan.Target,
		TargetEndpointContract:      plan.TargetEndpointContract,
		TargetTopology:              plan.TargetTopology,
		TargetTopologyDigest:        plan.TargetTopologyDigest,
		ExperimentSpec:              plan.ExperimentSpec,
		ExperimentDigest:            plan.ExperimentDigest,
		WorkloadSpec:                plan.WorkloadSpec,
		WorkloadDigest:              plan.WorkloadDigest,
		WorkloadMode:                plan.WorkloadMode,
		WorkloadScript:              plan.WorkloadScript,
		WorkloadScriptDigest:        plan.WorkloadScriptDigest,
		PGConfig:                    plan.PGConfig,
		PGConfigDigest:              plan.PGConfigDigest,
		Mode:                        plan.Mode,
		Scale:                       plan.Scale,
		Clients:                     plan.Clients,
		Threads:                     plan.Threads,
		WarmupSeconds:               plan.WarmupSeconds,
		MeasureSeconds:              plan.MeasureSeconds,
		TransactionsPerClient:       plan.TransactionsPerClient,
		Trials:                      plan.Trials,
		MinValidTrials:              plan.MinValidTrials,
		ResetPolicy:                 plan.ResetPolicy,
		RuntimeReset:                plan.RuntimeReset,
		CacheRegime:                 plan.CacheRegime,
		CacheTargetRelations:        append([]string(nil), plan.CacheTargetRelations...),
		CacheMinResidentPct:         plan.CacheMinResidentPct,
		StatisticsResetPolicy:       plan.StatisticsResetPolicy,
		StatisticsResetBoundary:     plan.StatisticsResetBoundary,
		Collectors:                  append([]string(nil), plan.Collectors...),
		CollectorIntervalSeconds:    plan.CollectorIntervalSeconds,
		CollectorOverheadMode:       plan.CollectorOverheadMode,
		CollectorOverheadSamples:    plan.CollectorOverheadSamples,
		CollectorMaxDutyCyclePct:    plan.CollectorMaxDutyCyclePct,
		ClientPlacement:             plan.ClientPlacement,
		ResourceBudgetMode:          plan.ResourceBudgetMode,
		CPUBudgetCores:              plan.CPUBudgetCores,
		CPUBudgetMillicores:         plan.CPUBudgetMillicores,
		MemoryBudgetMiB:             plan.MemoryBudgetMiB,
		ResourceBudgetScope:         plan.ResourceBudgetScope,
		ResourceEnforcementProvider: plan.ResourceEnforcementProvider,
		ResourceProviderConstraints: append([]string(nil), plan.ResourceProviderConstraints...),
		PrimaryMetric:               plan.PrimaryMetric,
		Direction:                   plan.Direction,
		MaxCVPct:                    plan.MaxCVPct,
		RegressionThresholdPct:      plan.RegressionThresholdPct,
		Rate:                        plan.Rate,
		LatencyLimitMS:              plan.LatencyLimitMS,
		MaxLatencyLimitExceededPct:  plan.MaxLatencyLimitExceededPct,
		ConnectPerTransaction:       plan.ConnectPerTransaction,
		QueryProtocol:               plan.QueryProtocol,
		RandomSeed:                  plan.RandomSeed,
		RandomSeedSemantics:         plan.RandomSeedSemantics,
		WarmupRandomSeed:            plan.WarmupRandomSeed,
		MeasureRandomSeed:           plan.MeasureRandomSeed,
		MaxTries:                    plan.MaxTries,
		LogTransactions:             plan.LogTransactions,
		LogSampleRate:               plan.LogSampleRate,
		AllowedSubjectDifferences:   append([]string(nil), plan.AllowedSubjectDifferences...),
	}
}

func Render(w io.Writer, plan Plan) error {
	rows := []struct {
		key   string
		value string
	}{
		{"Benchmark", plan.Spec},
		{"Contract version", valueOr(plan.ContractVersion, "1")},
		{"Name", plan.Name},
		{"Class", plan.Class},
		{"Driver", plan.Driver},
		{"Target", plan.Target},
		{"Target endpoint contract", plan.TargetEndpointContract},
		{"Target topology", plan.TargetTopology},
		{"Target topology digest", plan.TargetTopologyDigest},
		{"Experiment", plan.ExperimentSpec},
		{"Workload", plan.WorkloadSpec},
		{"PostgreSQL config", plan.PGConfig},
		{"Mode", plan.Mode},
		{"Scale", strconv.Itoa(plan.Scale)},
		{"Clients", strconv.Itoa(plan.Clients)},
		{"Threads", strconv.Itoa(plan.Threads)},
		{"Warmup seconds", strconv.Itoa(plan.WarmupSeconds)},
		{"Measure seconds", optionalNumber(plan.MeasureSeconds)},
		{"Transactions per client", optionalNumber(plan.TransactionsPerClient)},
		{"Trials", strconv.Itoa(plan.Trials)},
		{"Minimum valid trials", strconv.Itoa(plan.MinValidTrials)},
		{"Reset policy", plan.ResetPolicy},
		{"Runtime reset", strconv.FormatBool(plan.RuntimeReset)},
		{"Cache regime (declared)", plan.CacheRegime},
		{"Cache target relations", strings.Join(plan.CacheTargetRelations, ", ")},
		{"Cache minimum resident percent", optionalFloatValue(plan.CacheMinResidentPct)},
		{"Statistics reset policy (declared)", plan.StatisticsResetPolicy},
		{"Statistics reset boundary (declared)", plan.StatisticsResetBoundary},
		{"Collectors", strings.Join(plan.Collectors, ", ")},
		{"Collector interval seconds", strconv.Itoa(plan.CollectorIntervalSeconds)},
		{"Collector overhead mode (declared)", plan.CollectorOverheadMode},
		{"Collector overhead samples", optionalIntValue(plan.CollectorOverheadSamples)},
		{"Collector maximum duty cycle percent", optionalFloatValue(plan.CollectorMaxDutyCyclePct)},
		{"Client placement (declared)", plan.ClientPlacement},
		{"Resource budget mode (declared)", plan.ResourceBudgetMode},
		{"CPU budget cores (declared)", optionalFloatValue(plan.CPUBudgetCores)},
		{"CPU budget millicores", optionalIntValue(plan.CPUBudgetMillicores)},
		{"Memory budget MiB (declared)", optionalIntValue(plan.MemoryBudgetMiB)},
		{"Resource budget scope", plan.ResourceBudgetScope},
		{"Resource enforcement provider", plan.ResourceEnforcementProvider},
		{"Resource provider constraints", strings.Join(plan.ResourceProviderConstraints, ", ")},
		{"Primary metric", plan.PrimaryMetric},
		{"Direction", plan.Direction},
		{"Maximum CV percent", formatFloat(plan.MaxCVPct)},
		{"Maximum latency-limit exceeded percent", optionalFloatValue(plan.MaxLatencyLimitExceededPct)},
		{"Connect per transaction", strconv.FormatBool(plan.ConnectPerTransaction)},
		{"Transaction logging", strconv.FormatBool(plan.LogTransactions)},
		{"Random seed", optionalUint64Value(plan.RandomSeed)},
		{"Random seed semantics", plan.RandomSeedSemantics},
		{"Warmup random seed", optionalUint64Value(plan.WarmupRandomSeed)},
		{"Measure random seed", optionalUint64Value(plan.MeasureRandomSeed)},
		{"Log sample rate", formatFloat(plan.LogSampleRate)},
		{"Allowed subject differences", strings.Join(plan.AllowedSubjectDifferences, ", ")},
		{"Protocol digest", plan.ProtocolDigest},
		{"Comparison key digest", plan.ComparisonKeyDigest},
	}
	if _, err := fmt.Fprintln(w, "# Benchmark Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Field | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "| %s | `%s` |\n", tableCell(row.key), tableCell(row.value)); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, plan Plan) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

// VerifyDigests recomputes the protocol and comparison identities from the
// normalized plan. Artifact verification uses it so a producer cannot change
// plan fields and merely copy stale or attacker-selected identity digests.
func VerifyDigests(plan Plan) error {
	if err := validateDeclarations(plan); err != nil {
		return err
	}
	protocolDigest, comparisonDigest, err := IdentityDigests(plan)
	if err != nil {
		return err
	}
	if protocolDigest != plan.ProtocolDigest {
		return fmt.Errorf("protocol digest mismatch: got %s want %s", plan.ProtocolDigest, protocolDigest)
	}
	if comparisonDigest != plan.ComparisonKeyDigest {
		return fmt.Errorf("comparison key digest mismatch: got %s want %s", plan.ComparisonKeyDigest, comparisonDigest)
	}
	return nil
}

// IdentityDigests returns the exact protocol identity and the comparison key.
// The latter excludes only the explicitly modeled PostgreSQL configuration
// subject dimension.
func IdentityDigests(plan Plan) (string, string, error) {
	identity := plan.protocolIdentity()
	protocolDigest, err := digestJSON(identity)
	if err != nil {
		return "", "", err
	}
	identity.PGConfig = ""
	identity.PGConfigDigest = ""
	comparisonDigest, err := digestJSON(identity)
	if err != nil {
		return "", "", err
	}
	return protocolDigest, comparisonDigest, nil
}

func validateDeclarations(plan Plan) error {
	topology, endpointContract, err := TargetContract(plan.Target)
	if err != nil {
		return err
	}
	if plan.TargetTopology != topology || plan.TargetEndpointContract != endpointContract {
		return fmt.Errorf("benchmark target %q does not match its canonical topology and endpoint contract", plan.Target)
	}
	wantTopologyPath := filepath.ToSlash(filepath.Join("topologies", filepath.FromSlash(topology)+".env"))
	actualTopologyPath := filepath.ToSlash(plan.TargetTopologyPath)
	if filepath.IsAbs(plan.TargetTopologyPath) {
		actualTopologyPath = filepath.ToSlash(filepath.Join("topologies", filepath.Base(plan.TargetTopologyPath)))
	}
	if actualTopologyPath != wantTopologyPath || !evidence.IsDigest(plan.TargetTopologyDigest) {
		return fmt.Errorf("benchmark target topology source identity is invalid")
	}
	if plan.ProtocolSchemaVersion == ProtocolSchemaVersion {
		if plan.ContractVersion != "" {
			return fmt.Errorf("benchmark protocol v1 must omit contract_version")
		}
		if err := validateV1Controls(plan); err != nil {
			return err
		}
	} else if plan.ProtocolSchemaVersion == ProtocolSchemaVersionV2 {
		if plan.ContractVersion != "2" {
			return fmt.Errorf("benchmark protocol v2 requires contract_version 2")
		}
		if err := validateV2Controls(plan); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("unsupported benchmark protocol schema version %q", plan.ProtocolSchemaVersion)
	}
	if !isOneOf(plan.ClientPlacement, "same-host", "separate-host", "remote-host") {
		return fmt.Errorf("invalid declared client placement %q", plan.ClientPlacement)
	}
	if plan.ConnectPerTransaction && plan.PrimaryMetric == "pgbench.tps" {
		return fmt.Errorf("connect-per-transaction protocol cannot use pgbench.tps because reconnect TPS includes connection setup")
	}
	if plan.MaxLatencyLimitExceededPct != nil {
		if plan.LatencyLimitMS == nil || math.IsNaN(*plan.MaxLatencyLimitExceededPct) || math.IsInf(*plan.MaxLatencyLimitExceededPct, 0) || *plan.MaxLatencyLimitExceededPct < 0 || *plan.MaxLatencyLimitExceededPct > 100 {
			return fmt.Errorf("latency-limit exceeded budget requires a latency limit and a finite percentage in [0,100]")
		}
	}
	if plan.RandomSeed == nil {
		if plan.RandomSeedSemantics != "client-random-default" || plan.WarmupRandomSeed != nil || plan.MeasureRandomSeed != nil {
			return fmt.Errorf("unseeded protocol must use client-random-default without derived phase seeds")
		}
	} else {
		if *plan.RandomSeed > math.MaxInt64 || plan.RandomSeedSemantics != "phase-split-offset-v1" || plan.WarmupRandomSeed == nil || plan.MeasureRandomSeed == nil {
			return fmt.Errorf("seeded protocol must use phase-split-offset-v1 with valid derived phase seeds")
		}
		wantWarmup := *plan.RandomSeed + 1
		if *plan.RandomSeed == math.MaxInt64 {
			wantWarmup = 0
		}
		if *plan.MeasureRandomSeed != *plan.RandomSeed || *plan.WarmupRandomSeed != wantWarmup {
			return fmt.Errorf("derived phase seeds do not match phase-split-offset-v1 semantics")
		}
	}
	return nil
}

func validateV1Controls(plan Plan) error {
	if !isOneOf(plan.CacheRegime, "uncontrolled", "cold", "warm", "steady") {
		return fmt.Errorf("invalid declared cache regime %q", plan.CacheRegime)
	}
	if len(plan.CacheTargetRelations) != 0 || plan.CacheMinResidentPct != nil {
		return fmt.Errorf("benchmark protocol v1 must omit cache control evidence thresholds")
	}
	if !isOneOf(plan.StatisticsResetPolicy, "none", "operator-managed") {
		return fmt.Errorf("invalid declared statistics reset policy %q", plan.StatisticsResetPolicy)
	}
	if !isOneOf(plan.StatisticsResetBoundary, "none", "before-trial", "before-warmup", "before-measure") {
		return fmt.Errorf("invalid declared statistics reset boundary %q", plan.StatisticsResetBoundary)
	}
	if (plan.StatisticsResetPolicy == "none" && plan.StatisticsResetBoundary != "none") ||
		(plan.StatisticsResetPolicy == "operator-managed" && plan.StatisticsResetBoundary == "none") {
		return fmt.Errorf("statistics reset policy %q is inconsistent with boundary %q", plan.StatisticsResetPolicy, plan.StatisticsResetBoundary)
	}
	wantCollectors := []string{"pgbench-driver", "postgresql-sampler-v1"}
	if len(plan.Collectors) != len(wantCollectors) {
		return fmt.Errorf("benchmark protocol v1 requires collectors %v, got %v", wantCollectors, plan.Collectors)
	}
	for index := range wantCollectors {
		if plan.Collectors[index] != wantCollectors[index] {
			return fmt.Errorf("benchmark protocol v1 requires sorted collectors %v, got %v", wantCollectors, plan.Collectors)
		}
	}
	if plan.CollectorIntervalSeconds <= 0 {
		return fmt.Errorf("collector interval seconds must be positive")
	}
	if !isOneOf(plan.CollectorOverheadMode, "included-unquantified", "operator-calibrated") {
		return fmt.Errorf("invalid declared collector overhead mode %q", plan.CollectorOverheadMode)
	}
	if plan.CollectorOverheadSamples != nil || plan.CollectorMaxDutyCyclePct != nil {
		return fmt.Errorf("benchmark protocol v1 must omit collector calibration evidence thresholds")
	}
	if !isOneOf(plan.ResourceBudgetMode, "unbounded", "operator-declared") {
		return fmt.Errorf("invalid declared resource budget mode %q", plan.ResourceBudgetMode)
	}
	switch plan.ResourceBudgetMode {
	case "unbounded":
		if plan.CPUBudgetCores != nil || plan.CPUBudgetMillicores != nil || plan.MemoryBudgetMiB != nil || plan.ResourceBudgetScope != "" || plan.ResourceEnforcementProvider != "" || len(plan.ResourceProviderConstraints) != 0 {
			return fmt.Errorf("unbounded resource budget must not declare CPU or memory limits")
		}
	case "operator-declared":
		if plan.CPUBudgetMillicores != nil || plan.ResourceBudgetScope != "" || plan.ResourceEnforcementProvider != "" || len(plan.ResourceProviderConstraints) != 0 {
			return fmt.Errorf("benchmark protocol v1 operator-declared resource budget must omit v2 enforcement fields")
		}
		if plan.CPUBudgetCores == nil || math.IsNaN(*plan.CPUBudgetCores) || math.IsInf(*plan.CPUBudgetCores, 0) || *plan.CPUBudgetCores <= 0 {
			return fmt.Errorf("operator-declared resource budget requires positive finite CPU cores")
		}
		if plan.MemoryBudgetMiB == nil || *plan.MemoryBudgetMiB <= 0 {
			return fmt.Errorf("operator-declared resource budget requires positive memory MiB")
		}
	}
	return nil
}

func validateV2Controls(plan Plan) error {
	if !isOneOf(plan.CacheRegime, "uncontrolled", "postgres-shared-buffer-warm") {
		return fmt.Errorf("benchmark protocol v2 cache regime must be uncontrolled or postgres-shared-buffer-warm, got %q", plan.CacheRegime)
	}
	switch plan.CacheRegime {
	case "uncontrolled":
		if len(plan.CacheTargetRelations) != 0 || plan.CacheMinResidentPct != nil {
			return fmt.Errorf("uncontrolled cache regime must omit target relations and resident threshold")
		}
	case "postgres-shared-buffer-warm":
		if len(plan.CacheTargetRelations) == 0 || !sortedUniqueNonempty(plan.CacheTargetRelations) {
			return fmt.Errorf("postgres-shared-buffer-warm requires sorted unique cache target relations")
		}
		if plan.CacheMinResidentPct == nil || math.IsNaN(*plan.CacheMinResidentPct) || math.IsInf(*plan.CacheMinResidentPct, 0) || *plan.CacheMinResidentPct <= 0 || *plan.CacheMinResidentPct > 100 {
			return fmt.Errorf("postgres-shared-buffer-warm requires a finite cache minimum resident percentage in (0,100]")
		}
	}
	if !isOneOf(plan.StatisticsResetPolicy, "none", "runner-managed") {
		return fmt.Errorf("benchmark protocol v2 statistics reset policy must be none or runner-managed, got %q", plan.StatisticsResetPolicy)
	}
	if !isOneOf(plan.StatisticsResetBoundary, "none", "before-trial", "before-warmup", "before-measure") {
		return fmt.Errorf("invalid statistics reset boundary %q", plan.StatisticsResetBoundary)
	}
	if (plan.StatisticsResetPolicy == "none" && plan.StatisticsResetBoundary != "none") || (plan.StatisticsResetPolicy == "runner-managed" && plan.StatisticsResetBoundary == "none") {
		return fmt.Errorf("statistics reset policy %q is inconsistent with boundary %q", plan.StatisticsResetPolicy, plan.StatisticsResetBoundary)
	}
	wantCollectors := []string{"pgbench-driver", "postgresql-sampler-v2"}
	if !slices.Equal(plan.Collectors, wantCollectors) {
		return fmt.Errorf("benchmark protocol v2 requires sorted collectors %v, got %v", wantCollectors, plan.Collectors)
	}
	if plan.CollectorIntervalSeconds <= 0 || plan.CollectorIntervalSeconds > v2MaxCollectorInterval {
		return fmt.Errorf("benchmark protocol v2 collector interval seconds must be in [1,%d]", v2MaxCollectorInterval)
	}
	if !isOneOf(plan.CollectorOverheadMode, "included-unquantified", "runner-calibrated-duty-cycle") {
		return fmt.Errorf("invalid benchmark protocol v2 collector overhead mode %q", plan.CollectorOverheadMode)
	}
	if plan.CollectorOverheadMode == "included-unquantified" {
		if plan.CollectorOverheadSamples != nil || plan.CollectorMaxDutyCyclePct != nil {
			return fmt.Errorf("included-unquantified collector overhead must omit calibration thresholds")
		}
	} else if plan.CollectorOverheadSamples == nil || *plan.CollectorOverheadSamples <= 0 || *plan.CollectorOverheadSamples > v2MaxOverheadSamples || plan.CollectorMaxDutyCyclePct == nil || math.IsNaN(*plan.CollectorMaxDutyCyclePct) || math.IsInf(*plan.CollectorMaxDutyCyclePct, 0) || *plan.CollectorMaxDutyCyclePct <= 0 || *plan.CollectorMaxDutyCyclePct > 100 {
		return fmt.Errorf("runner-calibrated-duty-cycle requires samples in [1,%d] and a finite maximum duty percentage in (0,100]", v2MaxOverheadSamples)
	}
	if plan.CPUBudgetCores != nil {
		return fmt.Errorf("benchmark protocol v2 forbids cpu_budget_cores; use integer cpu_budget_millicores")
	}
	if !isOneOf(plan.ResourceBudgetMode, "unbounded", "runner-enforced") {
		return fmt.Errorf("benchmark protocol v2 resource budget mode must be unbounded or runner-enforced, got %q", plan.ResourceBudgetMode)
	}
	switch plan.ResourceBudgetMode {
	case "unbounded":
		if plan.CPUBudgetMillicores != nil || plan.MemoryBudgetMiB != nil || plan.ResourceBudgetScope != "" || plan.ResourceEnforcementProvider != "" || len(plan.ResourceProviderConstraints) != 0 {
			return fmt.Errorf("unbounded resource budget must omit limits, scope, provider, and provider constraints")
		}
	case "runner-enforced":
		if plan.CPUBudgetMillicores == nil || *plan.CPUBudgetMillicores <= 0 || int64(*plan.CPUBudgetMillicores) > v2MaxCPUMillicores || plan.MemoryBudgetMiB == nil || *plan.MemoryBudgetMiB <= 0 || int64(*plan.MemoryBudgetMiB) > v2MaxMemoryMiB {
			return fmt.Errorf("runner-enforced resource budget requires positive CPU millicores and memory MiB")
		}
		if plan.ResourceBudgetScope != "postgres-server-and-pgbench-driver" {
			return fmt.Errorf("runner-enforced resource budget scope must be postgres-server-and-pgbench-driver")
		}
		if plan.ResourceEnforcementProvider != "docker-single-container-linux-cgroup-v2" || !slices.Equal(plan.ResourceProviderConstraints, []string{"cgroup-v2-required", "docker-engine-required", "linux-only", "postgres-and-driver-share-one-container"}) {
			return fmt.Errorf("runner-enforced resource provider must record the exact Docker single-container cgroup-v2 constraints")
		}
		if plan.ClientPlacement != "same-host" {
			return fmt.Errorf("runner-enforced Docker single-container resources require same-host client placement")
		}
	}
	return nil
}

func sortedUniqueNonempty(values []string) bool {
	for index, value := range values {
		if !qualifiedRelation(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func qualifiedRelation(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for index := range part {
			character := part[index]
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_' || index > 0 && character >= '0' && character <= '9' {
				continue
			}
			return false
		}
	}
	return true
}

// VerifySpecDeclarations binds the normalized protocol declarations back to
// the immutable benchmark-spec snapshot. Digest recomputation alone proves
// internal plan consistency; this check prevents a producer from replacing a
// valid declaration, recomputing both digests, and leaving the source snapshot
// unchanged.
func VerifySpecDeclarations(plan Plan, values map[string]string) error {
	if err := validateDeclarations(plan); err != nil {
		return err
	}
	if valueOr(values["BENCHMARK_CONTRACT_VERSION"], "1") != valueOr(plan.ContractVersion, "1") {
		return fmt.Errorf("BENCHMARK_CONTRACT_VERSION declaration does not match plan")
	}
	stringsToCompare := []struct {
		key  string
		plan string
	}{
		{"BENCHMARK_NAME", plan.Name},
		{"BENCHMARK_CLASS", plan.Class},
		{"BENCHMARK_DRIVER", plan.Driver},
		{"BENCHMARK_TARGET", plan.Target},
		{"BENCHMARK_EXPERIMENT_SPEC", plan.ExperimentSpec},
		{"BENCHMARK_WORKLOAD_SPEC", plan.WorkloadSpec},
		{"BENCHMARK_PG_CONFIG", plan.PGConfig},
		{"BENCHMARK_MODE", plan.Mode},
		{"BENCHMARK_RESET_POLICY", plan.ResetPolicy},
		{"BENCHMARK_CACHE_REGIME", plan.CacheRegime},
		{"BENCHMARK_STATISTICS_RESET_POLICY", plan.StatisticsResetPolicy},
		{"BENCHMARK_STATISTICS_RESET_BOUNDARY", plan.StatisticsResetBoundary},
		{"BENCHMARK_COLLECTOR_OVERHEAD_MODE", plan.CollectorOverheadMode},
		{"BENCHMARK_CLIENT_PLACEMENT", plan.ClientPlacement},
		{"BENCHMARK_RESOURCE_BUDGET_MODE", plan.ResourceBudgetMode},
		{"BENCHMARK_RESOURCE_BUDGET_SCOPE", plan.ResourceBudgetScope},
		{"BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER", plan.ResourceEnforcementProvider},
		{"BENCHMARK_PRIMARY_METRIC", plan.PrimaryMetric},
		{"BENCHMARK_DIRECTION", plan.Direction},
		{"BENCHMARK_PROTOCOL", plan.QueryProtocol},
	}
	for _, item := range stringsToCompare {
		if declarationValue(values, item.key, benchmarkStringDefault(item.key, plan)) != item.plan {
			return fmt.Errorf("%s declaration does not match plan", item.key)
		}
	}
	integersToCompare := []struct {
		key  string
		plan int
	}{
		{"BENCHMARK_SCALE", plan.Scale},
		{"BENCHMARK_CLIENTS", plan.Clients},
		{"BENCHMARK_THREADS", plan.Threads},
		{"BENCHMARK_WARMUP_SECONDS", plan.WarmupSeconds},
		{"BENCHMARK_TRIALS", plan.Trials},
		{"BENCHMARK_MIN_VALID_TRIALS", plan.MinValidTrials},
	}
	if plan.Mode == "fixed-time" {
		if plan.TransactionsPerClient != 0 {
			return fmt.Errorf("fixed-time plan must not retain transactions per client")
		}
		integersToCompare = append(integersToCompare, struct {
			key  string
			plan int
		}{"BENCHMARK_MEASURE_SECONDS", plan.MeasureSeconds})
		if values["BENCHMARK_TRANSACTIONS_PER_CLIENT"] != "" {
			return fmt.Errorf("BENCHMARK_TRANSACTIONS_PER_CLIENT declaration does not match plan")
		}
	} else {
		if plan.MeasureSeconds != 0 {
			return fmt.Errorf("fixed-transactions plan must not retain measure seconds")
		}
		integersToCompare = append(integersToCompare, struct {
			key  string
			plan int
		}{"BENCHMARK_TRANSACTIONS_PER_CLIENT", plan.TransactionsPerClient})
		if values["BENCHMARK_MEASURE_SECONDS"] != "" {
			return fmt.Errorf("BENCHMARK_MEASURE_SECONDS declaration does not match plan")
		}
	}
	for _, item := range integersToCompare {
		parsed, err := strconv.Atoi(declarationValue(values, item.key, benchmarkIntDefault(item.key, plan)))
		if err != nil || parsed != item.plan {
			return fmt.Errorf("%s declaration does not match plan", item.key)
		}
	}
	if plan.RuntimeReset != (plan.ResetPolicy == "rebuild-per-trial") {
		return fmt.Errorf("runtime reset derivation does not match reset policy")
	}
	if (values["BENCHMARK_CONNECT_PER_TRANSACTION"] == "1") != plan.ConnectPerTransaction {
		return fmt.Errorf("BENCHMARK_CONNECT_PER_TRANSACTION declaration does not match plan")
	}
	logTransactionsDefault := plan.Class == "measurement"
	if boolOr(values["BENCHMARK_LOG_TRANSACTIONS"], logTransactionsDefault) != plan.LogTransactions {
		return fmt.Errorf("BENCHMARK_LOG_TRANSACTIONS declaration does not match plan")
	}
	latencyBudget, err := parseOptionalFloat(values["BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT"])
	if err != nil || !equalOptionalFloat(latencyBudget, plan.MaxLatencyLimitExceededPct) {
		return fmt.Errorf("BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT declaration does not match plan")
	}
	for _, item := range []struct {
		key  string
		plan *float64
	}{
		{"BENCHMARK_REGRESSION_THRESHOLD_PCT", plan.RegressionThresholdPct},
		{"BENCHMARK_RATE", plan.Rate},
		{"BENCHMARK_LATENCY_LIMIT_MS", plan.LatencyLimitMS},
	} {
		parsed, parseErr := parseOptionalFloat(values[item.key])
		if parseErr != nil || !equalOptionalFloat(parsed, item.plan) {
			return fmt.Errorf("%s declaration does not match plan", item.key)
		}
	}
	if maximum, parseErr := parseOptionalInt(values["BENCHMARK_MAX_TRIES"]); parseErr != nil || !equalOptionalInt(maximum, plan.MaxTries) {
		return fmt.Errorf("BENCHMARK_MAX_TRIES declaration does not match plan")
	}
	for _, item := range []struct {
		key      string
		plan     float64
		fallback float64
	}{
		{"BENCHMARK_MAX_CV_PCT", plan.MaxCVPct, 10},
		{"BENCHMARK_LOG_SAMPLE_RATE", plan.LogSampleRate, 1},
	} {
		parsed, parseErr := strconv.ParseFloat(declarationValue(values, item.key, formatFloat(item.fallback)), 64)
		if parseErr != nil || parsed != item.plan {
			return fmt.Errorf("%s declaration does not match plan", item.key)
		}
	}
	collectors := strings.Fields(values["BENCHMARK_COLLECTORS"])
	sort.Strings(collectors)
	if !slices.Equal(collectors, plan.Collectors) {
		return fmt.Errorf("BENCHMARK_COLLECTORS declaration does not match plan")
	}
	cacheTargets := strings.Fields(values["BENCHMARK_CACHE_TARGET_RELATIONS"])
	sort.Strings(cacheTargets)
	if !slices.Equal(cacheTargets, plan.CacheTargetRelations) {
		return fmt.Errorf("BENCHMARK_CACHE_TARGET_RELATIONS declaration does not match plan")
	}
	interval, err := strconv.Atoi(values["BENCHMARK_COLLECTOR_INTERVAL_SECONDS"])
	if err != nil || interval != plan.CollectorIntervalSeconds {
		return fmt.Errorf("BENCHMARK_COLLECTOR_INTERVAL_SECONDS declaration does not match plan")
	}
	cpuBudget, err := parseOptionalFloat(values["BENCHMARK_CPU_BUDGET_CORES"])
	if err != nil || !equalOptionalFloat(cpuBudget, plan.CPUBudgetCores) {
		return fmt.Errorf("BENCHMARK_CPU_BUDGET_CORES declaration does not match plan")
	}
	cpuMillicores, err := parseOptionalInt(values["BENCHMARK_CPU_BUDGET_MILLICORES"])
	if err != nil || !equalOptionalInt(cpuMillicores, plan.CPUBudgetMillicores) {
		return fmt.Errorf("BENCHMARK_CPU_BUDGET_MILLICORES declaration does not match plan")
	}
	memoryBudget, err := parseOptionalInt(values["BENCHMARK_MEMORY_BUDGET_MIB"])
	if err != nil || !equalOptionalInt(memoryBudget, plan.MemoryBudgetMiB) {
		return fmt.Errorf("BENCHMARK_MEMORY_BUDGET_MIB declaration does not match plan")
	}
	for _, item := range []struct {
		key  string
		plan *float64
	}{
		{"BENCHMARK_CACHE_MIN_RESIDENT_PCT", plan.CacheMinResidentPct},
		{"BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT", plan.CollectorMaxDutyCyclePct},
	} {
		parsed, parseErr := parseOptionalFloat(values[item.key])
		if parseErr != nil || !equalOptionalFloat(parsed, item.plan) {
			return fmt.Errorf("%s declaration does not match plan", item.key)
		}
	}
	overheadSamples, err := parseOptionalInt(values["BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES"])
	if err != nil || !equalOptionalInt(overheadSamples, plan.CollectorOverheadSamples) {
		return fmt.Errorf("BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES declaration does not match plan")
	}
	randomSeed, err := parseOptionalUint64(values["BENCHMARK_RANDOM_SEED"])
	if err != nil || !equalOptionalUint64(randomSeed, plan.RandomSeed) {
		return fmt.Errorf("BENCHMARK_RANDOM_SEED declaration does not match plan")
	}
	allowedDifferences := strings.Fields(valueOr(values["BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES"], "pg_config"))
	sort.Strings(allowedDifferences)
	if !slices.Equal(allowedDifferences, plan.AllowedSubjectDifferences) {
		return fmt.Errorf("BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES declaration does not match plan")
	}
	return nil
}

// VerifyWorkloadDeclarations binds the derived workload fields retained in
// plan.json back to the immutable workload-spec snapshot. The snapshot digest
// proves byte identity; this check proves the typed interpretation was not
// coherently replaced together with the plan digests.
func VerifyWorkloadDeclarations(plan Plan, values map[string]string) error {
	if values["WORKLOAD_KIND"] != "pgbench" {
		return fmt.Errorf("WORKLOAD_KIND declaration must be pgbench")
	}
	if mode := valueOr(values["PGBENCH_MODE"], "builtin"); mode != plan.WorkloadMode {
		return fmt.Errorf("PGBENCH_MODE declaration does not match plan")
	}
	script := filepath.ToSlash(values["PGBENCH_SCRIPT"])
	if script != plan.WorkloadScript {
		return fmt.Errorf("PGBENCH_SCRIPT declaration does not match plan")
	}
	if script == "" && plan.WorkloadScriptDigest != "" {
		return fmt.Errorf("workload script digest exists without a script declaration")
	}
	if script != "" && !evidence.IsDigest(plan.WorkloadScriptDigest) {
		return fmt.Errorf("workload script declaration has no valid digest")
	}
	return nil
}

func declarationValue(values map[string]string, key string, fallback string) string {
	if values[key] == "" {
		return fallback
	}
	return values[key]
}

func benchmarkStringDefault(key string, plan Plan) string {
	switch key {
	case "BENCHMARK_CLASS":
		return "measurement"
	case "BENCHMARK_DRIVER":
		return "pgbench"
	case "BENCHMARK_TARGET":
		return TargetDirectPostgres
	case "BENCHMARK_PG_CONFIG":
		return "default"
	case "BENCHMARK_MODE":
		return "fixed-time"
	case "BENCHMARK_RESET_POLICY":
		return "rebuild-per-trial"
	case "BENCHMARK_PRIMARY_METRIC":
		return "pgbench.tps"
	case "BENCHMARK_DIRECTION":
		if plan.PrimaryMetric == "pgbench.latency_mean_us" {
			return "lower"
		}
		return "higher"
	case "BENCHMARK_PROTOCOL":
		return "simple"
	default:
		return ""
	}
}

// TargetContract returns the only endpoint/topology mappings currently
// supported by the pgbench benchmark engine. The endpoint contract version is
// part of both protocol identities; changing an argv mapping requires a new
// contract value rather than silently reinterpreting existing evidence.
func TargetContract(target string) (topology string, endpointContract string, err error) {
	switch target {
	case TargetDirectPostgres:
		return "single", EndpointDirectV1, nil
	case TargetPgBouncer:
		return "pgbouncer", EndpointPgBouncerV1, nil
	default:
		return "", "", fmt.Errorf("unsupported benchmark target %q", target)
	}
}

func benchmarkIntDefault(key string, plan Plan) string {
	switch key {
	case "BENCHMARK_WARMUP_SECONDS":
		return "0"
	case "BENCHMARK_TRIALS":
		if plan.Class == "smoke" {
			return "1"
		}
		return "10"
	case "BENCHMARK_MIN_VALID_TRIALS":
		return strconv.Itoa(plan.Trials)
	default:
		return ""
	}
}

func parseOptionalFloat(value string) (*float64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseOptionalInt(value string) (*int, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseOptionalUint64(value string) (*uint64, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func equalOptionalFloat(left, right *float64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalOptionalUint64(left, right *uint64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func digestJSON(value interface{}) (string, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal benchmark protocol: %w", err)
	}
	return evidence.DigestBytes(content), nil
}

func digestFile(path string) (string, error) {
	return evidence.DigestFile(path)
}

func resolvePath(root string, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, filepath.FromSlash(value))
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func intOr(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func floatOr(value string, fallback float64) float64 {
	if value == "" {
		return fallback
	}
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func optionalFloat(value string) *float64 {
	if value == "" {
		return nil
	}
	parsed, _ := strconv.ParseFloat(value, 64)
	return &parsed
}

func optionalInt(value string) *int {
	if value == "" {
		return nil
	}
	parsed, _ := strconv.Atoi(value)
	return &parsed
}

func optionalUint64(value string) *uint64 {
	if value == "" {
		return nil
	}
	parsed, _ := strconv.ParseUint(value, 10, 64)
	return &parsed
}

func boolOr(value string, fallback bool) bool {
	if value == "" {
		return fallback
	}
	return value == "1"
}

func optionalNumber(value int) string {
	if value == 0 {
		return "-"
	}
	return strconv.Itoa(value)
}

func optionalFloatValue(value *float64) string {
	if value == nil {
		return "-"
	}
	return formatFloat(*value)
}

func optionalIntValue(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

func optionalUint64Value(value *uint64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatUint(*value, 10)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	if value == "" {
		return "-"
	}
	return value
}

func isOneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
