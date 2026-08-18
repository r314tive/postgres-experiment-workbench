package speccatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
)

type Catalog struct {
	Root string
}

type Spec struct {
	Kind   string
	ID     string
	Path   string
	Values map[string]string
}

type Kind struct {
	Name string
	Root string
}

var Kinds = []Kind{
	{Name: "workload", Root: "workloads"},
	{Name: "experiment", Root: "experiments"},
	{Name: "benchmark", Root: "benchmarks"},
	{Name: "matrix", Root: "matrices"},
	{Name: "topology", Root: "topologies"},
	{Name: "dataset", Root: "datasets"},
	{Name: "utility-test", Root: "utility-tests"},
	{Name: "utility-suite", Root: "utility-suites"},
}

var kindRoots = map[string]string{
	"workload":      "workloads",
	"experiment":    "experiments",
	"benchmark":     "benchmarks",
	"matrix":        "matrices",
	"topology":      "topologies",
	"dataset":       "datasets",
	"utility-test":  "utility-tests",
	"utility-suite": "utility-suites",
}

func New(root string) Catalog {
	return Catalog{Root: root}
}

func (c Catalog) List(kind string) ([]string, error) {
	root, err := c.kindRoot(kind)
	if err != nil {
		return nil, err
	}
	var specs []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".env" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		specs = append(specs, strings.TrimSuffix(filepath.ToSlash(rel), ".env"))
		return nil
	}); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(specs)
	return specs, nil
}

func (c Catalog) ListRaw(kind string) ([]string, error) {
	root, err := c.kindRoot(kind)
	if err != nil {
		return nil, err
	}
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".env" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(paths)
	specs := make([]string, 0, len(paths))
	for _, path := range paths {
		specs = append(specs, strings.TrimSuffix(path, ".env"))
	}
	return specs, nil
}

func (c Catalog) Show(kind string, id string) (Spec, error) {
	path, resolvedID, err := c.Resolve(kind, id)
	if err != nil {
		return Spec{}, err
	}
	values, err := envfile.Parse(path)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Kind: kind, ID: resolvedID, Path: path, Values: values}, nil
}

func (c Catalog) ShowRaw(kind string, id string) ([]byte, error) {
	path, _, err := c.Resolve(kind, id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (c Catalog) Resolve(kind string, input string) (string, string, error) {
	root, err := c.kindRoot(kind)
	if err != nil {
		return "", "", err
	}
	if kind == "experiment" && hasParentPathComponent(input) {
		return "", "", fmt.Errorf("experiment spec path must not contain parent traversal: %s", input)
	}

	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates,
			filepath.Join(c.Root, input),
			filepath.Join(root, input),
			filepath.Join(root, input+".env"),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			resolvedRoot := root
			resolvedCandidate := candidate
			if kind == "experiment" {
				resolvedRoot, resolvedCandidate, err = resolveExperimentPath(c.Root, candidate)
				if err != nil {
					return "", "", err
				}
			}
			id, err := specID(resolvedRoot, resolvedCandidate)
			return resolvedCandidate, id, err
		}
	}

	list, err := c.List(kind)
	if err != nil {
		return "", "", err
	}
	var matches []string
	for _, id := range list {
		if id == input || filepath.Base(id) == input {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		path := filepath.Join(root, filepath.FromSlash(matches[0])+".env")
		if kind == "experiment" {
			resolvedRoot, resolvedPath, err := resolveExperimentPath(c.Root, path)
			if err != nil {
				return "", "", err
			}
			id, err := specID(resolvedRoot, resolvedPath)
			return resolvedPath, id, err
		}
		return path, matches[0], nil
	}
	if len(matches) > 1 {
		return "", "", fmt.Errorf("ambiguous %s spec: %s: %s", kind, input, strings.Join(matches, ", "))
	}

	return "", "", fmt.Errorf("%s spec not found: %s", kind, input)
}

func (c Catalog) Validate(kind string, ids []string) []error {
	if kind == "all" || kind == "" {
		var errs []error
		for _, item := range Kinds {
			errs = append(errs, c.Validate(item.Name, nil)...)
		}
		return errs
	}

	var specs []Spec
	if len(ids) == 0 {
		list, err := c.List(kind)
		if err != nil {
			return []error{err}
		}
		for _, id := range list {
			spec, err := c.Show(kind, id)
			if err != nil {
				return []error{err}
			}
			specs = append(specs, spec)
		}
	} else {
		for _, id := range ids {
			spec, err := c.Show(kind, id)
			if err != nil {
				return []error{err}
			}
			specs = append(specs, spec)
		}
	}

	var errs []error
	for _, spec := range specs {
		errs = append(errs, c.validateSpec(spec)...)
	}
	return errs
}

// ValidateSpec validates an already-resolved spec without reopening its path.
// This is used by execution paths that must keep parsing, validation, hashing,
// and shell execution bound to one immutable byte snapshot.
func (c Catalog) ValidateSpec(spec Spec) []error {
	return c.validateSpec(spec)
}

func (c Catalog) validateSpec(spec Spec) []error {
	switch spec.Kind {
	case "workload":
		return c.validateWorkload(spec)
	case "experiment":
		return c.validateExperiment(spec)
	case "benchmark":
		return c.validateBenchmark(spec)
	case "matrix":
		return c.validateMatrix(spec)
	case "topology":
		return c.validateTopology(spec)
	case "dataset":
		return c.validateDataset(spec)
	case "utility-test":
		return c.validateUtilityTest(spec)
	case "utility-suite":
		return c.validateUtilitySuite(spec)
	default:
		return []error{fmt.Errorf("unsupported spec kind: %s", spec.Kind)}
	}
}

func (c Catalog) validateWorkload(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "WORKLOAD_NAME")
	kind := requireValue(&errs, spec, "WORKLOAD_KIND")
	if kind != "" && !oneOf(kind, "profile-sql", "sql", "pgbench", "pg-dump", "pg-dumpall", "pg-restore", "pg-source-check", "noisia", "shell", "compose-run") {
		errs = append(errs, specError(spec, "unsupported WORKLOAD_KIND: %s", kind))
	}

	switch kind {
	case "profile-sql":
		profile := requireValue(&errs, spec, "PROFILE")
		if profile != "" {
			if !c.dirExists("profiles", profile) {
				errs = append(errs, specError(spec, "PROFILE not found: %s", profile))
			}
			sqlName := valueOr(spec.Values["WORKLOAD_SQL"], "10_run.sql")
			if !isDynamic(sqlName) && !c.fileExists("profiles", profile, "sql", sqlName) {
				errs = append(errs, specError(spec, "profile SQL not found: profiles/%s/sql/%s", profile, sqlName))
			}
		}
	case "sql":
		sqlPath := firstValue(spec.Values, "SQL", "WORKLOAD_SQL")
		if sqlPath == "" {
			errs = append(errs, specError(spec, "SQL or WORKLOAD_SQL is required for WORKLOAD_KIND=sql"))
		} else if !isDynamic(sqlPath) && !c.pathExists(sqlPath) {
			errs = append(errs, specError(spec, "SQL file not found: %s", sqlPath))
		}
	case "pgbench":
		script := spec.Values["PGBENCH_SCRIPT"]
		if script != "" && !isDynamic(script) && !c.pathExists(script) {
			errs = append(errs, specError(spec, "PGBENCH_SCRIPT not found: %s", script))
		}
	case "pg-dump", "pg-dumpall":
		validateUtilityOutput(&errs, spec, "UTILITY_OUTPUT_FILE")
		if schema := spec.Values["UTILITY_SOURCE_SCHEMA"]; schema != "" && !isDynamic(schema) && !simpleIdentifier(schema) {
			errs = append(errs, specError(spec, "UTILITY_SOURCE_SCHEMA must be a simple PostgreSQL identifier: %s", schema))
		}
	case "pg-restore":
		output := validateUtilityOutput(&errs, spec, "UTILITY_OUTPUT_FILE")
		archive := validateUtilityOutput(&errs, spec, "UTILITY_ARCHIVE_FILE")
		source := requireValue(&errs, spec, "UTILITY_SOURCE_SCHEMA")
		target := requireValue(&errs, spec, "UTILITY_TARGET_SCHEMA")
		if source != "" && !isDynamic(source) && !simpleIdentifier(source) {
			errs = append(errs, specError(spec, "UTILITY_SOURCE_SCHEMA must be a simple PostgreSQL identifier: %s", source))
		}
		if target != "" && !isDynamic(target) && !simpleIdentifier(target) {
			errs = append(errs, specError(spec, "UTILITY_TARGET_SCHEMA must be a simple PostgreSQL identifier: %s", target))
		}
		if output != "" && archive != "" && !isDynamic(output) && !isDynamic(archive) && output == archive {
			errs = append(errs, specError(spec, "UTILITY_OUTPUT_FILE and UTILITY_ARCHIVE_FILE must differ"))
		}
		if source != "" && target != "" && !isDynamic(source) && !isDynamic(target) && source == target {
			errs = append(errs, specError(spec, "UTILITY_SOURCE_SCHEMA and UTILITY_TARGET_SCHEMA must differ"))
		}
	case "pg-source-check":
		action := valueOr(spec.Values["PG_SOURCE_ACTION"], "run")
		if !isDynamic(action) && !oneOf(action, "plan", "run", "scan") {
			errs = append(errs, specError(spec, "unsupported PG_SOURCE_ACTION: %s", action))
		}
		patchset := spec.Values["PG_PATCHSET"]
		if patchset != "" && !isDynamic(patchset) && !c.fileExists("patchsets", filepath.FromSlash(patchset), "patchset.env") {
			errs = append(errs, specError(spec, "PG_PATCHSET not found: %s", patchset))
		}
		patchDir := spec.Values["PG_PATCH_DIR"]
		if patchDir != "" && !isDynamic(patchDir) && !c.pathExists(patchDir) {
			errs = append(errs, specError(spec, "PG_PATCH_DIR not found: %s", patchDir))
		}
	case "noisia":
		workload := requireValue(&errs, spec, "NOISIA_WORKLOAD")
		if workload != "" && !oneOf(workload, "wait-xacts", "temp-files") {
			errs = append(errs, specError(spec, "unsupported NOISIA_WORKLOAD: %s", workload))
		}
	case "shell":
		requireValue(&errs, spec, "WORKLOAD_CMD")
	case "compose-run":
		requireValue(&errs, spec, "WORKLOAD_IMAGE")
		requireValue(&errs, spec, "WORKLOAD_COMMAND")
	}
	return errs
}

func (c Catalog) validateExperiment(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "EXPERIMENT_NAME")

	topology := valueOr(spec.Values["EXPERIMENT_TOPOLOGY"], "single")
	if !isDynamic(topology) && !oneOf(topology, "single", "primary-replica", "logical-replication", "pgbouncer", "multi-version-upgrade", "source-tree") {
		errs = append(errs, specError(spec, "unsupported EXPERIMENT_TOPOLOGY: %s", topology))
	}
	if topology != "source-tree" && !isDynamic(topology) && !c.specExists("topology", topology) {
		errs = append(errs, specError(spec, "topology spec not found: %s", topology))
	}

	pgConfig := valueOr(spec.Values["EXPERIMENT_PG_CONFIG"], "default")
	if !isDynamic(pgConfig) && !c.fileExists("configs", pgConfig, "postgresql.conf") {
		errs = append(errs, specError(spec, "PostgreSQL config not found: %s", pgConfig))
	}

	stateWriter := valueOr(spec.Values["EXPERIMENT_STATE_WRITER"], "go")
	if !isDynamic(stateWriter) && !oneOf(stateWriter, "auto", "go") {
		errs = append(errs, specError(spec, "unsupported EXPERIMENT_STATE_WRITER: %s", stateWriter))
	}
	if timeout := spec.Values["EXPERIMENT_TIMEOUT"]; timeout != "" && !isDynamic(timeout) {
		parsed, err := time.ParseDuration(timeout)
		if err != nil || parsed < time.Second {
			errs = append(errs, specError(spec, "EXPERIMENT_TIMEOUT must be a Go duration of at least 1s: %s", timeout))
		}
	}

	profile := spec.Values["EXPERIMENT_PROFILE"]
	if profile != "" && !isDynamic(profile) && !c.dirExists("profiles", profile) {
		errs = append(errs, specError(spec, "profile not found: %s", profile))
	}

	dataset := spec.Values["EXPERIMENT_DATASET_SPEC"]
	if dataset != "" && !isDynamic(dataset) && !c.specExists("dataset", dataset) {
		errs = append(errs, specError(spec, "dataset spec not found: %s", dataset))
	}

	workload := spec.Values["EXPERIMENT_WORKLOAD_SPEC"]
	if workload != "" && !isDynamic(workload) && !c.specExists("workload", workload) {
		errs = append(errs, specError(spec, "workload spec not found: %s", workload))
	}

	for _, background := range splitWords(spec.Values["EXPERIMENT_BACKGROUND_SPECS"]) {
		if !isDynamic(background) && !c.specExists("workload", background) {
			errs = append(errs, specError(spec, "background workload spec not found: %s", background))
		}
	}

	for _, sqlPath := range splitWords(spec.Values["EXPERIMENT_ASSERT_SQL_FILES"]) {
		if !isDynamic(sqlPath) && !c.pathExists(sqlPath) {
			errs = append(errs, specError(spec, "assert SQL file not found: %s", sqlPath))
		}
	}

	trustedShell := valueOr(spec.Values["EXPERIMENT_TRUSTED_SHELL"], "0")
	if !isDynamic(trustedShell) && !oneOf(trustedShell, "0", "1") {
		errs = append(errs, specError(spec, "EXPERIMENT_TRUSTED_SHELL must be 0 or 1: %s", trustedShell))
	}

	var shellHooks []string
	for _, field := range []string{"EXPERIMENT_BEFORE_SHELL", "EXPERIMENT_AFTER_SHELL", "EXPERIMENT_ASSERT_SHELL"} {
		if spec.Values[field] != "" {
			shellHooks = append(shellHooks, field)
		}
	}
	if len(shellHooks) > 0 && !isDynamic(trustedShell) && trustedShell != "1" {
		errs = append(errs, specError(spec, "%s require EXPERIMENT_TRUSTED_SHELL=1", strings.Join(shellHooks, ", ")))
	}
	return errs
}

func (c Catalog) validateBenchmark(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "BENCHMARK_NAME")
	contractVersion := benchmarkStaticValue(&errs, spec, "BENCHMARK_CONTRACT_VERSION", "1")
	if contractVersion != "" && !oneOf(contractVersion, "1", "2") {
		errs = append(errs, specError(spec, "BENCHMARK_CONTRACT_VERSION must be 1 or 2: %s", contractVersion))
	}

	class := benchmarkStaticValue(&errs, spec, "BENCHMARK_CLASS", "measurement")
	if class != "" && !oneOf(class, "smoke", "measurement") {
		errs = append(errs, specError(spec, "BENCHMARK_CLASS must be smoke or measurement: %s", class))
	}

	driver := benchmarkStaticValue(&errs, spec, "BENCHMARK_DRIVER", "pgbench")
	if driver != "" && driver != "pgbench" {
		errs = append(errs, specError(spec, "BENCHMARK_DRIVER must be pgbench: %s", driver))
	}
	target := benchmarkStaticValue(&errs, spec, "BENCHMARK_TARGET", "direct-postgres")
	targetTopology := ""
	switch target {
	case "direct-postgres":
		targetTopology = "single"
	case "pgbouncer":
		targetTopology = "pgbouncer"
	case "":
		// benchmarkStaticValue already reported a dynamic declaration.
	default:
		errs = append(errs, specError(spec, "BENCHMARK_TARGET must be direct-postgres or pgbouncer: %s", target))
	}

	experimentRef := benchmarkRequiredStaticValue(&errs, spec, "BENCHMARK_EXPERIMENT_SPEC")
	var experiment Spec
	if experimentRef != "" && !isDynamic(experimentRef) {
		resolved, err := c.Show("experiment", experimentRef)
		if err != nil {
			errs = append(errs, specError(spec, "experiment spec not found: %s", experimentRef))
		} else {
			experiment = resolved
		}
	}

	workloadRef := benchmarkRequiredStaticValue(&errs, spec, "BENCHMARK_WORKLOAD_SPEC")
	var workload Spec
	if workloadRef != "" && !isDynamic(workloadRef) {
		resolved, err := c.Show("workload", workloadRef)
		if err != nil {
			errs = append(errs, specError(spec, "workload spec not found: %s", workloadRef))
		} else {
			workload = resolved
			if kind := workload.Values["WORKLOAD_KIND"]; kind != "pgbench" {
				errs = append(errs, specError(spec, "BENCHMARK_WORKLOAD_SPEC must use WORKLOAD_KIND=pgbench: %s uses %s", workloadRef, kind))
			}
			for _, key := range []string{"PGBENCH_MODE", "PGBENCH_SCRIPT"} {
				if isDynamic(workload.Values[key]) {
					errs = append(errs, specError(spec, "%s in benchmark workload %s must be static so the benchmark protocol can be digested", key, workloadRef))
				}
			}
		}
	}

	pgConfig := benchmarkStaticValue(&errs, spec, "BENCHMARK_PG_CONFIG", "default")
	if pgConfig != "" && !c.fileExists("configs", pgConfig, "postgresql.conf") {
		errs = append(errs, specError(spec, "PostgreSQL config not found: %s", pgConfig))
	}

	if experiment.ID != "" {
		declaredTopology := valueOr(experiment.Values["EXPERIMENT_TOPOLOGY"], "single")
		if isDynamic(declaredTopology) {
			errs = append(errs, specError(spec, "EXPERIMENT_TOPOLOGY in benchmark experiment %s must be static so the target contract can be digested", experimentRef))
		} else if targetTopology != "" && declaredTopology != targetTopology {
			errs = append(errs, specError(spec, "benchmark target %s requires experiment topology %s; %s declares %s", target, targetTopology, experimentRef, declaredTopology))
		}
		if declared := experiment.Values["EXPERIMENT_WORKLOAD_SPEC"]; declared != "" && !isDynamic(declared) && workloadRef != "" && declared != workloadRef {
			errs = append(errs, specError(spec, "experiment %s fixes EXPERIMENT_WORKLOAD_SPEC=%s; benchmark requests %s", experimentRef, declared, workloadRef))
		}
		if declared := experiment.Values["EXPERIMENT_PG_CONFIG"]; declared != "" && !isDynamic(declared) && pgConfig != "" && declared != pgConfig {
			errs = append(errs, specError(spec, "experiment %s fixes EXPERIMENT_PG_CONFIG=%s; benchmark requests %s", experimentRef, declared, pgConfig))
		}
	}

	for _, key := range []string{"BENCHMARK_SCALE", "BENCHMARK_CLIENTS", "BENCHMARK_THREADS"} {
		value := benchmarkRequiredStaticValue(&errs, spec, key)
		if value != "" && !benchmarkPositiveInt(value) {
			errs = append(errs, specError(spec, "%s must be a positive integer: %s", key, value))
		}
	}

	warmup := benchmarkStaticValue(&errs, spec, "BENCHMARK_WARMUP_SECONDS", "0")
	if warmup != "" && !benchmarkNonnegativeInt(warmup) {
		errs = append(errs, specError(spec, "BENCHMARK_WARMUP_SECONDS must be a non-negative integer: %s", warmup))
	}

	mode := benchmarkStaticValue(&errs, spec, "BENCHMARK_MODE", "fixed-time")
	if mode != "" && !oneOf(mode, "fixed-time", "fixed-transactions") {
		errs = append(errs, specError(spec, "BENCHMARK_MODE must be fixed-time or fixed-transactions: %s", mode))
	}
	measureSeconds := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_MEASURE_SECONDS")
	transactions := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_TRANSACTIONS_PER_CLIENT")
	switch mode {
	case "fixed-time":
		if measureSeconds == "" {
			errs = append(errs, specError(spec, "BENCHMARK_MEASURE_SECONDS is required for BENCHMARK_MODE=fixed-time"))
		} else if !benchmarkPositiveInt(measureSeconds) {
			errs = append(errs, specError(spec, "BENCHMARK_MEASURE_SECONDS must be a positive integer: %s", measureSeconds))
		}
		if transactions != "" {
			errs = append(errs, specError(spec, "BENCHMARK_TRANSACTIONS_PER_CLIENT is incompatible with BENCHMARK_MODE=fixed-time"))
		}
	case "fixed-transactions":
		if transactions == "" {
			errs = append(errs, specError(spec, "BENCHMARK_TRANSACTIONS_PER_CLIENT is required for BENCHMARK_MODE=fixed-transactions"))
		} else if !benchmarkPositiveInt(transactions) {
			errs = append(errs, specError(spec, "BENCHMARK_TRANSACTIONS_PER_CLIENT must be a positive integer: %s", transactions))
		}
		if measureSeconds != "" {
			errs = append(errs, specError(spec, "BENCHMARK_MEASURE_SECONDS is incompatible with BENCHMARK_MODE=fixed-transactions"))
		}
	}

	defaultTrials := "10"
	if class == "smoke" {
		defaultTrials = "1"
	}
	trials := benchmarkStaticValue(&errs, spec, "BENCHMARK_TRIALS", defaultTrials)
	if trials != "" && !benchmarkPositiveInt(trials) {
		errs = append(errs, specError(spec, "BENCHMARK_TRIALS must be a positive integer: %s", trials))
	}
	minValid := benchmarkStaticValue(&errs, spec, "BENCHMARK_MIN_VALID_TRIALS", trials)
	if minValid != "" && !benchmarkPositiveInt(minValid) {
		errs = append(errs, specError(spec, "BENCHMARK_MIN_VALID_TRIALS must be a positive integer: %s", minValid))
	} else if benchmarkPositiveInt(trials) && benchmarkPositiveInt(minValid) {
		trialCount, _ := strconv.Atoi(trials)
		minimum, _ := strconv.Atoi(minValid)
		if minimum > trialCount {
			errs = append(errs, specError(spec, "BENCHMARK_MIN_VALID_TRIALS must not exceed BENCHMARK_TRIALS: %d > %d", minimum, trialCount))
		}
	}

	resetPolicy := benchmarkStaticValue(&errs, spec, "BENCHMARK_RESET_POLICY", "rebuild-per-trial")
	if resetPolicy != "" && !oneOf(resetPolicy, "rebuild-per-trial", "reuse-readonly") {
		errs = append(errs, specError(spec, "BENCHMARK_RESET_POLICY must be rebuild-per-trial or reuse-readonly: %s", resetPolicy))
	}
	if resetPolicy == "reuse-readonly" && workload.ID != "" {
		if workload.Values["PGBENCH_MODE"] != "select-only" || workload.Values["PGBENCH_SCRIPT"] != "" {
			errs = append(errs, specError(spec, "BENCHMARK_RESET_POLICY=reuse-readonly requires a pgbench select-only workload"))
		}
	}

	if contractVersion == "2" {
		validateBenchmarkControlsV2(&errs, spec)
	} else if contractVersion == "1" {
		validateBenchmarkControlsV1(&errs, spec)
	}
	clientPlacement := benchmarkRequiredStaticValue(&errs, spec, "BENCHMARK_CLIENT_PLACEMENT")
	if clientPlacement != "" && !oneOf(clientPlacement, "same-host", "separate-host", "remote-host") {
		errs = append(errs, specError(spec, "BENCHMARK_CLIENT_PLACEMENT must be same-host, separate-host, or remote-host: %s", clientPlacement))
	}
	if contractVersion == "2" && spec.Values["BENCHMARK_RESOURCE_BUDGET_MODE"] == "runner-enforced" && clientPlacement != "" && clientPlacement != "same-host" {
		errs = append(errs, specError(spec, "runner-enforced Docker single-container resources require BENCHMARK_CLIENT_PLACEMENT=same-host"))
	}

	connectPerTransaction := benchmarkStaticValue(&errs, spec, "BENCHMARK_CONNECT_PER_TRANSACTION", "0")
	if connectPerTransaction != "" && !oneOf(connectPerTransaction, "0", "1") {
		errs = append(errs, specError(spec, "BENCHMARK_CONNECT_PER_TRANSACTION must be 0 or 1: %s", connectPerTransaction))
	}

	primaryMetric := benchmarkStaticValue(&errs, spec, "BENCHMARK_PRIMARY_METRIC", "pgbench.tps")
	if primaryMetric != "" && !oneOf(primaryMetric, "pgbench.tps", "pgbench.latency_mean_us") {
		errs = append(errs, specError(spec, "unsupported BENCHMARK_PRIMARY_METRIC: %s", primaryMetric))
	}
	expectedDirection := "higher"
	if primaryMetric == "pgbench.latency_mean_us" {
		expectedDirection = "lower"
	}
	if connectPerTransaction == "1" && primaryMetric == "pgbench.tps" {
		errs = append(errs, specError(spec, "BENCHMARK_CONNECT_PER_TRANSACTION=1 requires BENCHMARK_PRIMARY_METRIC=pgbench.latency_mean_us because reconnect TPS includes connection setup"))
	}
	direction := benchmarkStaticValue(&errs, spec, "BENCHMARK_DIRECTION", expectedDirection)
	if direction != "" && !oneOf(direction, "higher", "lower") {
		errs = append(errs, specError(spec, "BENCHMARK_DIRECTION must be higher or lower: %s", direction))
	} else if direction != "" && primaryMetric != "" && direction != expectedDirection {
		errs = append(errs, specError(spec, "BENCHMARK_DIRECTION=%s is inconsistent with %s; expected %s", direction, primaryMetric, expectedDirection))
	}

	maxCV := benchmarkStaticValue(&errs, spec, "BENCHMARK_MAX_CV_PCT", "10")
	if maxCV != "" && !positiveDecimal(maxCV) {
		errs = append(errs, specError(spec, "BENCHMARK_MAX_CV_PCT must be a positive decimal: %s", maxCV))
	}
	if threshold := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_REGRESSION_THRESHOLD_PCT"); threshold != "" && !nonnegativeDecimal(threshold) {
		errs = append(errs, specError(spec, "BENCHMARK_REGRESSION_THRESHOLD_PCT must be a non-negative decimal: %s", threshold))
	}
	if rate := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_RATE"); rate != "" && !positiveDecimal(rate) {
		errs = append(errs, specError(spec, "BENCHMARK_RATE must be a positive decimal: %s", rate))
	}
	latencyLimit := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_LATENCY_LIMIT_MS")
	if latencyLimit != "" && !positiveDecimal(latencyLimit) {
		errs = append(errs, specError(spec, "BENCHMARK_LATENCY_LIMIT_MS must be a positive decimal: %s", latencyLimit))
	}
	latencyLimitBudget := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT")
	if latencyLimitBudget != "" {
		parsed, err := strconv.ParseFloat(latencyLimitBudget, 64)
		if err != nil || !nonnegativeDecimal(latencyLimitBudget) || parsed > 100 {
			errs = append(errs, specError(spec, "BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT must be a decimal in [0,100]: %s", latencyLimitBudget))
		} else if latencyLimit == "" {
			errs = append(errs, specError(spec, "BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT requires BENCHMARK_LATENCY_LIMIT_MS"))
		}
	}
	protocol := benchmarkStaticValue(&errs, spec, "BENCHMARK_PROTOCOL", "simple")
	if protocol != "" && !oneOf(protocol, "simple", "extended", "prepared") {
		errs = append(errs, specError(spec, "BENCHMARK_PROTOCOL must be simple, extended, or prepared: %s", protocol))
	}
	if seed := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_RANDOM_SEED"); seed != "" && !benchmarkNonnegativeUint64(seed) {
		errs = append(errs, specError(spec, "BENCHMARK_RANDOM_SEED must be a non-negative integer: %s", seed))
	}
	if maxTries := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_MAX_TRIES"); maxTries != "" {
		if !benchmarkNonnegativeInt(maxTries) {
			errs = append(errs, specError(spec, "BENCHMARK_MAX_TRIES must be a non-negative integer: %s", maxTries))
		} else if maxTries == "0" && mode != "fixed-time" && latencyLimit == "" {
			errs = append(errs, specError(spec, "BENCHMARK_MAX_TRIES=0 requires BENCHMARK_MODE=fixed-time or BENCHMARK_LATENCY_LIMIT_MS"))
		}
	}
	if logTransactions := benchmarkOptionalStaticValue(&errs, spec, "BENCHMARK_LOG_TRANSACTIONS"); logTransactions != "" && !oneOf(logTransactions, "0", "1") {
		errs = append(errs, specError(spec, "BENCHMARK_LOG_TRANSACTIONS must be 0 or 1: %s", logTransactions))
	}
	if sampleRate := benchmarkStaticValue(&errs, spec, "BENCHMARK_LOG_SAMPLE_RATE", "1"); sampleRate != "" {
		parsed, err := strconv.ParseFloat(sampleRate, 64)
		if err != nil || !positiveDecimal(sampleRate) || parsed > 1 {
			errs = append(errs, specError(spec, "BENCHMARK_LOG_SAMPLE_RATE must be a decimal greater than 0 and at most 1: %s", sampleRate))
		}
	}
	allowedDifferences := benchmarkStaticValue(&errs, spec, "BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES", "pg_config")
	seenDifferences := make(map[string]struct{})
	for _, difference := range strings.Fields(allowedDifferences) {
		if difference != "pg_config" && difference != "native_toolchain" {
			errs = append(errs, specError(spec, "BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES supports pg_config or native_toolchain only: %s", difference))
			continue
		}
		if _, exists := seenDifferences[difference]; exists {
			errs = append(errs, specError(spec, "BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES contains duplicate value: %s", difference))
		}
		seenDifferences[difference] = struct{}{}
	}

	return errs
}

func validateBenchmarkControlsV1(errs *[]error, spec Spec) {
	cacheRegime := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_CACHE_REGIME")
	if cacheRegime != "" && !oneOf(cacheRegime, "uncontrolled", "cold", "warm", "steady") {
		*errs = append(*errs, specError(spec, "BENCHMARK_CACHE_REGIME must be uncontrolled, cold, warm, or steady in benchmark contract v1: %s", cacheRegime))
	}
	for _, key := range []string{"BENCHMARK_CACHE_TARGET_RELATIONS", "BENCHMARK_CACHE_MIN_RESIDENT_PCT", "BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES", "BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT", "BENCHMARK_CPU_BUDGET_MILLICORES", "BENCHMARK_RESOURCE_BUDGET_SCOPE", "BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER"} {
		if benchmarkOptionalStaticValue(errs, spec, key) != "" {
			*errs = append(*errs, specError(spec, "%s requires BENCHMARK_CONTRACT_VERSION=2", key))
		}
	}
	policy := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_STATISTICS_RESET_POLICY")
	if policy != "" && !oneOf(policy, "none", "operator-managed") {
		*errs = append(*errs, specError(spec, "BENCHMARK_STATISTICS_RESET_POLICY must be none or operator-managed in benchmark contract v1: %s", policy))
	}
	boundary := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_STATISTICS_RESET_BOUNDARY")
	validateStatisticsBoundary(errs, spec, policy, boundary, "operator-managed")
	validateExactCollectors(errs, spec, []string{"pgbench-driver", "postgresql-sampler-v1"}, "v1")
	validateCollectorInterval(errs, spec)
	overhead := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_COLLECTOR_OVERHEAD_MODE")
	if overhead != "" && !oneOf(overhead, "included-unquantified", "operator-calibrated") {
		*errs = append(*errs, specError(spec, "BENCHMARK_COLLECTOR_OVERHEAD_MODE must be included-unquantified or operator-calibrated in benchmark contract v1: %s", overhead))
	}
	mode := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_RESOURCE_BUDGET_MODE")
	if mode != "" && !oneOf(mode, "unbounded", "operator-declared") {
		*errs = append(*errs, specError(spec, "BENCHMARK_RESOURCE_BUDGET_MODE must be unbounded or operator-declared in benchmark contract v1: %s", mode))
	}
	cpu := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_CPU_BUDGET_CORES")
	memory := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_MEMORY_BUDGET_MIB")
	if mode == "unbounded" && (cpu != "" || memory != "") {
		*errs = append(*errs, specError(spec, "BENCHMARK_RESOURCE_BUDGET_MODE=unbounded is incompatible with CPU or memory budget values"))
	}
	if mode == "operator-declared" {
		if cpu == "" || !positiveDecimal(cpu) {
			*errs = append(*errs, specError(spec, "BENCHMARK_RESOURCE_BUDGET_MODE=operator-declared requires positive BENCHMARK_CPU_BUDGET_CORES"))
		}
		if memory == "" || !benchmarkPositiveInt(memory) {
			*errs = append(*errs, specError(spec, "BENCHMARK_RESOURCE_BUDGET_MODE=operator-declared requires positive BENCHMARK_MEMORY_BUDGET_MIB"))
		}
	}
}

func validateBenchmarkControlsV2(errs *[]error, spec Spec) {
	cacheRegime := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_CACHE_REGIME")
	if cacheRegime != "" && !oneOf(cacheRegime, "uncontrolled", "postgres-shared-buffer-warm") {
		*errs = append(*errs, specError(spec, "BENCHMARK_CACHE_REGIME must be uncontrolled or postgres-shared-buffer-warm in benchmark contract v2: %s", cacheRegime))
	}
	targets := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_CACHE_TARGET_RELATIONS")
	minimum := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_CACHE_MIN_RESIDENT_PCT")
	if cacheRegime == "uncontrolled" && (targets != "" || minimum != "") {
		*errs = append(*errs, specError(spec, "BENCHMARK_CACHE_REGIME=uncontrolled must omit cache target relations and resident threshold"))
	}
	if cacheRegime == "postgres-shared-buffer-warm" {
		if fields := strings.Fields(targets); len(fields) == 0 {
			*errs = append(*errs, specError(spec, "BENCHMARK_CACHE_REGIME=postgres-shared-buffer-warm requires BENCHMARK_CACHE_TARGET_RELATIONS"))
		} else {
			seen := make(map[string]struct{}, len(fields))
			for _, relation := range fields {
				if !qualifiedIdentifier(relation) {
					*errs = append(*errs, specError(spec, "BENCHMARK_CACHE_TARGET_RELATIONS contains invalid relation: %s", relation))
				}
				if _, ok := seen[relation]; ok {
					*errs = append(*errs, specError(spec, "BENCHMARK_CACHE_TARGET_RELATIONS contains duplicate relation: %s", relation))
				}
				seen[relation] = struct{}{}
			}
		}
		if !percentageAboveZero(minimum) {
			*errs = append(*errs, specError(spec, "BENCHMARK_CACHE_REGIME=postgres-shared-buffer-warm requires BENCHMARK_CACHE_MIN_RESIDENT_PCT in (0,100]"))
		}
	}
	policy := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_STATISTICS_RESET_POLICY")
	if policy != "" && !oneOf(policy, "none", "runner-managed") {
		*errs = append(*errs, specError(spec, "BENCHMARK_STATISTICS_RESET_POLICY must be none or runner-managed in benchmark contract v2: %s", policy))
	}
	boundary := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_STATISTICS_RESET_BOUNDARY")
	validateStatisticsBoundary(errs, spec, policy, boundary, "runner-managed")
	validateExactCollectors(errs, spec, []string{"pgbench-driver", "postgresql-sampler-v2"}, "v2")
	validateCollectorInterval(errs, spec)
	if interval := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_COLLECTOR_INTERVAL_SECONDS"); interval != "" && !benchmarkPositiveIntAtMost(interval, 3600) {
		*errs = append(*errs, specError(spec, "BENCHMARK_COLLECTOR_INTERVAL_SECONDS must be in [1,3600] for benchmark contract v2"))
	}
	overhead := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_COLLECTOR_OVERHEAD_MODE")
	samples := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES")
	maxDuty := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT")
	if overhead != "" && !oneOf(overhead, "included-unquantified", "runner-calibrated-duty-cycle") {
		*errs = append(*errs, specError(spec, "BENCHMARK_COLLECTOR_OVERHEAD_MODE must be included-unquantified or runner-calibrated-duty-cycle in benchmark contract v2: %s", overhead))
	} else if overhead == "included-unquantified" && (samples != "" || maxDuty != "") {
		*errs = append(*errs, specError(spec, "included-unquantified collector overhead must omit calibration sample and duty-cycle thresholds"))
	} else if overhead == "runner-calibrated-duty-cycle" && (!benchmarkPositiveIntAtMost(samples, 10_000) || !percentageAboveZero(maxDuty)) {
		*errs = append(*errs, specError(spec, "runner-calibrated-duty-cycle requires BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES in [1,10000] and BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT in (0,100]"))
	}
	if benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_CPU_BUDGET_CORES") != "" {
		*errs = append(*errs, specError(spec, "BENCHMARK_CPU_BUDGET_CORES is declaration-only v1 syntax; v2 uses BENCHMARK_CPU_BUDGET_MILLICORES"))
	}
	mode := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_RESOURCE_BUDGET_MODE")
	if mode != "" && !oneOf(mode, "unbounded", "runner-enforced") {
		*errs = append(*errs, specError(spec, "BENCHMARK_RESOURCE_BUDGET_MODE must be unbounded or runner-enforced in benchmark contract v2: %s", mode))
	}
	cpu := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_CPU_BUDGET_MILLICORES")
	memory := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_MEMORY_BUDGET_MIB")
	scope := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_RESOURCE_BUDGET_SCOPE")
	provider := benchmarkOptionalStaticValue(errs, spec, "BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER")
	if mode == "unbounded" && (cpu != "" || memory != "" || scope != "" || provider != "") {
		*errs = append(*errs, specError(spec, "BENCHMARK_RESOURCE_BUDGET_MODE=unbounded must omit limits, scope, and enforcement provider"))
	}
	if mode == "runner-enforced" {
		if !benchmarkPositiveIntAtMost(cpu, 9_223_372_036_854) || !benchmarkPositiveIntAtMost(memory, 8_796_093_022_207) {
			*errs = append(*errs, specError(spec, "runner-enforced resource budget requires positive integer CPU millicores and memory MiB"))
		}
		if scope != "postgres-server-and-pgbench-driver" {
			*errs = append(*errs, specError(spec, "runner-enforced BENCHMARK_RESOURCE_BUDGET_SCOPE must be postgres-server-and-pgbench-driver"))
		}
		if provider != "docker-single-container-linux-cgroup-v2" {
			*errs = append(*errs, specError(spec, "runner-enforced BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER must be docker-single-container-linux-cgroup-v2"))
		}
	}
}

func validateStatisticsBoundary(errs *[]error, spec Spec, policy, boundary, managedPolicy string) {
	if boundary != "" && !oneOf(boundary, "none", "before-trial", "before-warmup", "before-measure") {
		*errs = append(*errs, specError(spec, "unsupported BENCHMARK_STATISTICS_RESET_BOUNDARY: %s", boundary))
	}
	if policy == "none" && boundary != "" && boundary != "none" || policy == managedPolicy && boundary == "none" {
		*errs = append(*errs, specError(spec, "BENCHMARK_STATISTICS_RESET_POLICY=%s is inconsistent with BENCHMARK_STATISTICS_RESET_BOUNDARY=%s", policy, boundary))
	}
}

func validateExactCollectors(errs *[]error, spec Spec, required []string, version string) {
	value := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_COLLECTORS")
	fields := strings.Fields(value)
	sort.Strings(fields)
	want := append([]string(nil), required...)
	sort.Strings(want)
	if value != "" && !slices.Equal(fields, want) {
		*errs = append(*errs, specError(spec, "BENCHMARK_COLLECTORS must contain exactly %s in benchmark protocol %s", strings.Join(want, " "), version))
	}
}

func validateCollectorInterval(errs *[]error, spec Spec) {
	value := benchmarkRequiredStaticValue(errs, spec, "BENCHMARK_COLLECTOR_INTERVAL_SECONDS")
	if value != "" && !benchmarkPositiveInt(value) {
		*errs = append(*errs, specError(spec, "BENCHMARK_COLLECTOR_INTERVAL_SECONDS must be a positive integer: %s", value))
	}
}

func percentageAboveZero(value string) bool {
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && positiveDecimal(value) && parsed <= 100
}

func qualifiedIdentifier(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if !simpleIdentifier(part) {
			return false
		}
	}
	return true
}

func (c Catalog) validateMatrix(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "MATRIX_NAME")
	for _, experiment := range splitWords(valueOr(spec.Values["MATRIX_EXPERIMENTS"], "smoke")) {
		if !isDynamic(experiment) && !c.specExists("experiment", experiment) {
			errs = append(errs, specError(spec, "experiment spec not found: %s", experiment))
		}
	}
	for _, pgConfig := range splitWords(valueOr(spec.Values["MATRIX_PG_CONFIGS"], "default")) {
		if !isDynamic(pgConfig) && !c.fileExists("configs", pgConfig, "postgresql.conf") {
			errs = append(errs, specError(spec, "PostgreSQL config not found: %s", pgConfig))
		}
	}
	for _, profileSize := range splitWords(valueOr(spec.Values["MATRIX_PROFILE_SIZES"], "small")) {
		if !isDynamic(profileSize) && !oneOf(profileSize, "small", "medium", "large") {
			errs = append(errs, specError(spec, "unsupported MATRIX_PROFILE_SIZE: %s", profileSize))
		}
	}
	repeats := valueOr(spec.Values["MATRIX_REPEATS"], "1")
	if !isDynamic(repeats) && !positiveInt(repeats) {
		errs = append(errs, specError(spec, "MATRIX_REPEATS must be a positive integer: %s", repeats))
	}
	return errs
}

func (c Catalog) validateTopology(spec Spec) []error {
	var errs []error
	name := requireValue(&errs, spec, "TOPOLOGY_NAME")
	requireValue(&errs, spec, "TOPOLOGY_DESCRIPTION")
	if name != "" && name != spec.ID {
		errs = append(errs, specError(spec, "TOPOLOGY_NAME must match spec id %q, got %q", spec.ID, name))
	}
	if name != "" && !oneOf(name, "single", "primary-replica", "logical-replication", "pgbouncer", "multi-version-upgrade") {
		errs = append(errs, specError(spec, "unsupported TOPOLOGY_NAME: %s", name))
	}
	return errs
}

func (c Catalog) validateDataset(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "DATASET_NAME")
	kind := requireValue(&errs, spec, "DATASET_KIND")
	if kind != "" && !oneOf(kind, "sql", "profile", "pgbench") {
		errs = append(errs, specError(spec, "unsupported DATASET_KIND: %s", kind))
	}
	switch kind {
	case "sql":
		sqlPath := requireValue(&errs, spec, "DATASET_SQL")
		if sqlPath != "" && !isDynamic(sqlPath) && !c.pathExists(sqlPath) {
			errs = append(errs, specError(spec, "DATASET_SQL not found: %s", sqlPath))
		}
	case "profile":
		profile := requireValue(&errs, spec, "DATASET_PROFILE")
		if profile != "" && !isDynamic(profile) && !c.dirExists("profiles", profile) {
			errs = append(errs, specError(spec, "DATASET_PROFILE not found: %s", profile))
		}
	}
	return errs
}

func (c Catalog) validateUtilityTest(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "UTILITY_TEST_NAME")

	workload := requireValue(&errs, spec, "UTILITY_TEST_WORKLOAD_SPEC")
	if workload != "" && !isDynamic(workload) && !c.specExists("workload", workload) {
		errs = append(errs, specError(spec, "workload spec not found: %s", workload))
	}

	profile := spec.Values["UTILITY_TEST_PROFILE"]
	if profile != "" && !isDynamic(profile) && !c.dirExists("profiles", profile) {
		errs = append(errs, specError(spec, "profile not found: %s", profile))
	}
	profileSize := valueOr(spec.Values["UTILITY_TEST_PROFILE_SIZE"], "small")
	if !isDynamic(profileSize) && !oneOf(profileSize, "small", "medium", "large") {
		errs = append(errs, specError(spec, "unsupported UTILITY_TEST_PROFILE_SIZE: %s", profileSize))
	}

	dataset := spec.Values["UTILITY_TEST_DATASET_SPEC"]
	if dataset != "" && !isDynamic(dataset) && !c.specExists("dataset", dataset) {
		errs = append(errs, specError(spec, "dataset spec not found: %s", dataset))
	}
	datasetSize := valueOr(spec.Values["UTILITY_TEST_DATASET_SIZE"], "small")
	if !isDynamic(datasetSize) && !oneOf(datasetSize, "small", "medium", "large") {
		errs = append(errs, specError(spec, "unsupported UTILITY_TEST_DATASET_SIZE: %s", datasetSize))
	}

	for _, background := range splitWords(spec.Values["UTILITY_TEST_BACKGROUND_SPECS"]) {
		if !isDynamic(background) && !c.specExists("workload", background) {
			errs = append(errs, specError(spec, "background workload spec not found: %s", background))
		}
	}

	for _, sqlPath := range splitWords(spec.Values["UTILITY_TEST_ASSERT_SQL_FILES"]) {
		if !isDynamic(sqlPath) && !c.pathExists(sqlPath) {
			errs = append(errs, specError(spec, "assert SQL file not found: %s", sqlPath))
		}
	}

	if wait := spec.Values["UTILITY_TEST_BACKGROUND_WAIT"]; wait != "" && !isDynamic(wait) && !oneOf(wait, "0", "1") {
		errs = append(errs, specError(spec, "UTILITY_TEST_BACKGROUND_WAIT must be 0 or 1: %s", wait))
	}
	if metrics := spec.Values["UTILITY_TEST_METRICS"]; metrics != "" && !isDynamic(metrics) && !oneOf(metrics, "0", "1") {
		errs = append(errs, specError(spec, "UTILITY_TEST_METRICS must be 0 or 1: %s", metrics))
	}

	trustedShell := valueOr(spec.Values["UTILITY_TEST_TRUSTED_SHELL"], "0")
	if !isDynamic(trustedShell) && !oneOf(trustedShell, "0", "1") {
		errs = append(errs, specError(spec, "UTILITY_TEST_TRUSTED_SHELL must be 0 or 1: %s", trustedShell))
	}

	var shellAssertions []string
	for _, field := range []string{"UTILITY_TEST_EXPECT_FILES", "UTILITY_TEST_ASSERT_SHELL"} {
		if spec.Values[field] != "" {
			shellAssertions = append(shellAssertions, field)
		}
	}
	if len(shellAssertions) > 0 && !isDynamic(trustedShell) && trustedShell != "1" {
		errs = append(errs, specError(spec, "%s require UTILITY_TEST_TRUSTED_SHELL=1", strings.Join(shellAssertions, ", ")))
	}
	return errs
}

func (c Catalog) validateUtilitySuite(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "UTILITY_SUITE_NAME")

	for _, test := range splitWords(requireValue(&errs, spec, "UTILITY_SUITE_TESTS")) {
		if !isDynamic(test) && !c.specExists("utility-test", test) {
			errs = append(errs, specError(spec, "utility-test spec not found: %s", test))
		}
	}

	for _, profileSize := range splitWords(valueOr(spec.Values["UTILITY_SUITE_PROFILE_SIZES"], "small")) {
		if !isDynamic(profileSize) && !oneOf(profileSize, "small", "medium", "large") {
			errs = append(errs, specError(spec, "unsupported UTILITY_SUITE_PROFILE_SIZE: %s", profileSize))
		}
	}

	repeats := valueOr(spec.Values["UTILITY_SUITE_REPEATS"], "1")
	if !isDynamic(repeats) && !positiveInt(repeats) {
		errs = append(errs, specError(spec, "UTILITY_SUITE_REPEATS must be a positive integer: %s", repeats))
	}
	if stopOnFail := spec.Values["UTILITY_SUITE_STOP_ON_FAIL"]; stopOnFail != "" && !isDynamic(stopOnFail) && !oneOf(stopOnFail, "0", "1") {
		errs = append(errs, specError(spec, "UTILITY_SUITE_STOP_ON_FAIL must be 0 or 1: %s", stopOnFail))
	}
	if snapshot := spec.Values["UTILITY_SUITE_SNAPSHOT"]; snapshot != "" && !isDynamic(snapshot) && !oneOf(snapshot, "0", "1") {
		errs = append(errs, specError(spec, "UTILITY_SUITE_SNAPSHOT must be 0 or 1: %s", snapshot))
	}
	return errs
}

func (c Catalog) kindRoot(kind string) (string, error) {
	root, ok := kindRoots[kind]
	if !ok {
		return "", fmt.Errorf("unsupported spec kind: %s", kind)
	}
	return filepath.Join(c.Root, root), nil
}

func (c Catalog) specExists(kind string, id string) bool {
	_, _, err := c.Resolve(kind, id)
	return err == nil
}

func (c Catalog) dirExists(parts ...string) bool {
	info, err := os.Stat(filepath.Join(append([]string{c.Root}, parts...)...))
	return err == nil && info.IsDir()
}

func (c Catalog) fileExists(parts ...string) bool {
	info, err := os.Stat(filepath.Join(append([]string{c.Root}, parts...)...))
	return err == nil && !info.IsDir()
}

func (c Catalog) pathExists(path string) bool {
	if filepath.IsAbs(path) {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	info, err := os.Stat(filepath.Join(c.Root, path))
	return err == nil && !info.IsDir()
}

func specID(root string, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), ".env"), nil
}

func resolveExperimentPath(packRoot string, candidate string) (string, string, error) {
	packRoot, err := filepath.Abs(packRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve scenario pack root: %w", err)
	}
	packRoot, err = filepath.EvalSymlinks(packRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve scenario pack root: %w", err)
	}
	root := filepath.Join(packRoot, kindRoots["experiment"])

	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve experiment spec: %w", err)
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve experiment spec: %w", err)
	}

	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve experiment spec relative to pack: %w", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("experiment spec resolves outside scenario pack experiments: %s", candidate)
	}
	return root, candidate, nil
}

func hasParentPathComponent(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if component == ".." {
			return true
		}
	}
	return false
}

func requireValue(errs *[]error, spec Spec, key string) string {
	value := spec.Values[key]
	if value == "" {
		*errs = append(*errs, specError(spec, "%s is required", key))
	}
	return value
}

func specError(spec Spec, format string, args ...interface{}) error {
	return fmt.Errorf("%s:%s: %s", spec.Kind, spec.ID, fmt.Sprintf(format, args...))
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func firstValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values[key] != "" {
			return values[key]
		}
	}
	return ""
}

func splitWords(value string) []string {
	if value == "" || isDynamic(value) {
		return nil
	}
	return strings.Fields(value)
}

func isDynamic(value string) bool {
	return strings.Contains(value, "$")
}

func positiveInt(value string) bool {
	if value == "" {
		return false
	}
	for i, ch := range value {
		if ch < '0' || ch > '9' || (i == 0 && ch == '0') {
			return false
		}
	}
	return true
}

func nonnegativeInt(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character < '0' || character > '9' || index == 0 && character == '0' && len(value) > 1 {
			return false
		}
	}
	return true
}

func benchmarkPositiveInt(value string) bool {
	if !positiveInt(value) {
		return false
	}
	_, err := strconv.Atoi(value)
	return err == nil
}

func benchmarkPositiveIntAtMost(value string, maximum int64) bool {
	if !positiveInt(value) {
		return false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed <= maximum
}

func benchmarkNonnegativeInt(value string) bool {
	if !nonnegativeInt(value) {
		return false
	}
	_, err := strconv.Atoi(value)
	return err == nil
}

func benchmarkNonnegativeUint64(value string) bool {
	if !nonnegativeInt(value) {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func positiveDecimal(value string) bool {
	if !nonnegativeDecimal(value) {
		return false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && parsed > 0
}

func nonnegativeDecimal(value string) bool {
	if value == "" {
		return false
	}
	dot := -1
	for index, character := range value {
		switch {
		case character >= '0' && character <= '9':
		case character == '.' && dot == -1 && index > 0 && index < len(value)-1:
			dot = index
		default:
			return false
		}
	}
	integerPart := value
	if dot >= 0 {
		integerPart = value[:dot]
	}
	if len(integerPart) > 1 && integerPart[0] == '0' {
		return false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && parsed >= 0
}

func benchmarkRequiredStaticValue(errs *[]error, spec Spec, key string) string {
	value := requireValue(errs, spec, key)
	if value != "" && isDynamic(value) {
		*errs = append(*errs, specError(spec, "%s must be a static value so the benchmark protocol can be digested", key))
		return ""
	}
	return value
}

func benchmarkOptionalStaticValue(errs *[]error, spec Spec, key string) string {
	value := spec.Values[key]
	if value != "" && isDynamic(value) {
		*errs = append(*errs, specError(spec, "%s must be a static value so the benchmark protocol can be digested", key))
		return ""
	}
	return value
}

func benchmarkStaticValue(errs *[]error, spec Spec, key string, fallback string) string {
	value := benchmarkOptionalStaticValue(errs, spec, key)
	if value == "" {
		return fallback
	}
	return value
}

func validateUtilityOutput(errs *[]error, spec Spec, key string) string {
	value := requireValue(errs, spec, key)
	if value != "" && !isDynamic(value) && !portableOutputPath(value) {
		*errs = append(*errs, specError(spec, "%s must be a portable repository-relative file path: %s", key, value))
	}
	return value
}

func portableOutputPath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, `\`) {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
		for _, character := range component {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func simpleIdentifier(value string) bool {
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return value != ""
}
