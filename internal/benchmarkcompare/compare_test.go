package benchmarkcompare

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	metricstest "github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics/testfixture"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestCompareRejectsClientAndProtocolMismatch(t *testing.T) {
	root := writeCompareCatalog(t)
	threshold := 5.0
	writeCompareSpec(t, root, &threshold, "higher", 2, "simple")
	baseline := runCompareSeries(t, root, "clients-protocol-baseline", "baseline", "docker", []float64{100, 101, 99, 100, 100}, 2, "simple", "higher")

	// Keep the benchmark id stable while changing two protocol dimensions. Each
	// immutable series carries its own spec snapshot and comparison-key digest.
	writeCompareSpec(t, root, &threshold, "higher", 3, "extended")
	candidate := runCompareSeries(t, root, "clients-protocol-candidate", "candidate", "docker", []float64{100, 101, 99, 100, 100}, 3, "extended", "higher")

	comparison, err := Compare(root, baseline.ArtifactDir, candidate.ArtifactDir, deterministicCompareOptions())
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Status != "not-comparable" || comparison.Decision != "not-comparable" {
		t.Fatalf("protocol mismatch must be not-comparable: %#v", comparison)
	}
	if !containsCompareReason(comparison.Reasons, "comparison key digests differ") {
		t.Fatalf("protocol mismatch reason is missing: %v", comparison.Reasons)
	}
}

func TestCompareRejectsDockerAndNativePopulationMix(t *testing.T) {
	root := writeCompareCatalog(t)
	threshold := 5.0
	writeCompareSpec(t, root, &threshold, "higher", 2, "simple")
	baseline := runCompareSeries(t, root, "docker-baseline", "baseline", "docker", []float64{100, 101, 99, 100, 100}, 2, "simple", "higher")
	candidate := runCompareSeries(t, root, "native-candidate", "candidate", "native", []float64{100, 101, 99, 100, 100}, 2, "simple", "higher")

	comparison, err := Compare(root, baseline.ArtifactDir, candidate.ArtifactDir, deterministicCompareOptions())
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Status != "not-comparable" || !containsCompareReason(comparison.Reasons, "native and Docker results are different performance populations") {
		t.Fatalf("runtime population mismatch was not rejected: %#v", comparison)
	}
}

func TestComparabilityBindsNativeBytesButIgnoresSnapshotLocation(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	environment := &benchmarkrun.Environment{
		Runtime: "native", RuntimeOS: "linux", RuntimeArch: "amd64", Driver: "pgbench", DriverVersion: "19devel",
		ParserVersion: "1.0.0", PostgresServerVersionNum: "190000", EngineVersion: "0.3.0", EngineCommit: strings.Repeat("b", 40), EngineBinaryDigest: digest,
		TargetEndpointHost: "127.0.0.1", TargetEndpointPort: 55433,
		PGConfig: "default", PGConfigDigest: digest, Qualification: "unqualified-local",
		SubjectDimension: "pg_config", NativeToolchainDigest: digest, NativeToolchainManifestRef: "runs/benchmarks/a/protocol/native-toolchain/manifest.json", NativeToolchainProvenance: nativetoolchain.Unattested,
	}
	baseline := benchmarkrun.Series{
		RunID: "a", Class: "measurement", Runtime: "native", ResetPolicy: "rebuild-per-trial",
		PrimaryMetric: "pgbench.tps", Direction: "higher", ComparisonKeyDigest: digest,
		EngineBinaryDigest: digest,
		AllowedDifferences: []string{"pg_config"}, Environment: environment,
		Trials: []benchmarkrun.Trial{{RunID: "a-t1"}},
	}
	rightEnvironment := *environment
	rightEnvironment.NativeToolchainManifestRef = "runs/benchmarks/b/protocol/native-toolchain/manifest.json"
	candidate := baseline
	candidate.RunID, candidate.Environment, candidate.Trials = "b", &rightEnvironment, []benchmarkrun.Trial{{RunID: "b-t1"}}
	comparison := Comparison{Reasons: []string{}, Differences: []string{}}
	checkComparability(&comparison, baseline, candidate)
	if containsCompareReason(comparison.Reasons, "native toolchain") {
		t.Fatalf("portable snapshot location was treated as byte drift: %v", comparison.Reasons)
	}
	candidate.EngineBinaryDigest = "sha256:" + strings.Repeat("e", 64)
	comparison = Comparison{Reasons: []string{}, Differences: []string{}}
	checkComparability(&comparison, baseline, candidate)
	if !containsCompareReason(comparison.Reasons, "benchmark engine binary digests differ") {
		t.Fatalf("benchmark engine byte drift was accepted: %#v", comparison)
	}
	candidate.EngineBinaryDigest = baseline.EngineBinaryDigest
	candidate.Environment.NativeToolchainDigest = "sha256:" + strings.Repeat("c", 64)
	comparison = Comparison{Reasons: []string{}, Differences: []string{}}
	checkComparability(&comparison, baseline, candidate)
	if !containsCompareReason(comparison.Reasons, "native toolchain differs") {
		t.Fatalf("native byte drift outside declared subject was accepted: %#v", comparison)
	}
	baseline.AllowedDifferences, candidate.AllowedDifferences = []string{"native_toolchain"}, []string{"native_toolchain"}
	comparison = Comparison{Reasons: []string{}, Differences: []string{}}
	checkComparability(&comparison, baseline, candidate)
	if !containsCompareReason(comparison.Differences, "native_toolchain") || containsCompareReason(comparison.Reasons, "native toolchain differs") {
		t.Fatalf("declared native_toolchain subject was not normalized: %#v", comparison)
	}
}

func TestCompareWithoutPredeclaredThresholdIsInconclusive(t *testing.T) {
	root := writeCompareCatalog(t)
	writeCompareSpec(t, root, nil, "higher", 2, "simple")
	baseline := runCompareSeries(t, root, "no-threshold-baseline", "baseline", "docker", []float64{100, 101, 99, 100, 100}, 2, "simple", "higher")
	candidate := runCompareSeries(t, root, "no-threshold-candidate", "candidate", "docker", []float64{110, 111, 109, 110, 110}, 2, "simple", "higher")

	comparison, err := Compare(root, baseline.ArtifactDir, candidate.ArtifactDir, deterministicCompareOptions())
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Status != "inconclusive" || comparison.Decision != "inconclusive" || comparison.BaselineN != 5 || comparison.CandidateN != 5 {
		t.Fatalf("missing threshold must be inconclusive after sample validation: %#v", comparison)
	}
	if !containsCompareReason(comparison.Reasons, "predeclared regression threshold is required") {
		t.Fatalf("missing-threshold reason is absent: %v", comparison.Reasons)
	}
}

func TestCompareUnqualifiedSeriesCannotProduceVerdict(t *testing.T) {
	root := writeCompareCatalog(t)
	threshold := 5.0
	writeCompareSpec(t, root, &threshold, "higher", 2, "simple")
	baseline := runCompareSeries(t, root, "unqualified-baseline", "baseline", "docker", []float64{100, 100, 100, 100, 100}, 2, "simple", "higher")
	candidate := runCompareSeries(t, root, "unqualified-candidate", "candidate", "docker", []float64{110, 110, 110, 110, 110}, 2, "simple", "higher")
	comparison, err := Compare(root, baseline.ArtifactDir, candidate.ArtifactDir, deterministicCompareOptions())
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Status != "inconclusive" || comparison.Decision != "inconclusive" || !containsCompareReason(comparison.Reasons, "descriptive only") {
		t.Fatalf("assurance boundary was not enforced: %#v", comparison)
	}
}

func TestCompareRejectsSameSeriesAndOverlappingTrialPopulation(t *testing.T) {
	root := writeCompareCatalog(t)
	threshold := 5.0
	writeCompareSpec(t, root, &threshold, "higher", 2, "simple")
	baseline := runCompareSeries(t, root, "self-baseline", "baseline", "docker", []float64{100, 100, 100, 100, 100}, 2, "simple", "higher")

	self, err := Compare(root, baseline.ArtifactDir, baseline.ArtifactDir, deterministicCompareOptions())
	if err != nil {
		t.Fatal(err)
	}
	if self.Status != "not-comparable" || !containsCompareReason(self.Reasons, "benchmark series must be distinct") || !containsCompareReason(self.Reasons, "share linked trial runs") {
		t.Fatalf("self-comparison was not rejected: %#v", self)
	}

	candidateDir := filepath.Join(root, "runs", "benchmarks", "population-copy")
	copyCompareTree(t, baseline.ArtifactDir, candidateDir)
	var candidate benchmarkrun.Series
	readCompareJSON(t, filepath.Join(candidateDir, "result.json"), &candidate)
	candidate.RunID = "population-copy"
	writeCompareJSON(t, filepath.Join(candidateDir, "result.json"), candidate)

	overlap, err := Compare(root, baseline.ArtifactDir, candidateDir, deterministicCompareOptions())
	if err != nil {
		t.Fatal(err)
	}
	if overlap.Status != "not-comparable" || !containsCompareReason(overlap.Reasons, "share linked trial runs") {
		t.Fatalf("overlapping trial population was not rejected: %#v", overlap)
	}
	if containsCompareReason(overlap.Reasons, "benchmark series must be distinct") {
		t.Fatalf("distinct copied series was mistaken for self-comparison: %#v", overlap)
	}
}

func TestCompareBootstrapDecisionsAreDeterministic(t *testing.T) {
	tests := []struct {
		name       string
		direction  string
		baseline   []float64
		candidate  []float64
		wantChange float64
	}{
		{"higher improved", "higher", []float64{100, 100, 100, 100, 100}, []float64{110, 110, 110, 110, 110}, 10},
		{"higher regressed", "higher", []float64{100, 100, 100, 100, 100}, []float64{90, 90, 90, 90, 90}, -10},
		{"higher mixed", "higher", []float64{100, 100, 100, 100, 100}, []float64{94, 94, 100, 100, 100}, 0},
		{"lower improved", "lower", []float64{100, 100, 100, 100, 100}, []float64{90, 90, 90, 90, 90}, 10},
		{"lower regressed", "lower", []float64{100, 100, 100, 100, 100}, []float64{110, 110, 110, 110, 110}, -10},
		{"lower mixed", "lower", []float64{100, 100, 100, 100, 100}, []float64{100, 100, 100, 106, 106}, 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeCompareCatalog(t)
			threshold := 5.0
			writeCompareSpec(t, root, &threshold, test.direction, 2, "simple")
			baseline := runCompareSeries(t, root, "bootstrap-baseline", "baseline", "docker", test.baseline, 2, "simple", test.direction)
			candidate := runCompareSeries(t, root, "bootstrap-candidate", "candidate", "docker", test.candidate, 2, "simple", test.direction)

			options := deterministicCompareOptions()
			first, err := Compare(root, baseline.ArtifactDir, candidate.ArtifactDir, options)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Compare(root, baseline.ArtifactDir, candidate.ArtifactDir, options)
			if err != nil {
				t.Fatal(err)
			}
			if first.Status != "inconclusive" || first.Decision != "inconclusive" || !containsCompareReason(first.Reasons, "descriptive only") {
				t.Fatalf("independent analysis crossed its assurance boundary: %#v", first)
			}
			if first.BaselineN != 5 || first.CandidateN != 5 {
				t.Fatalf("bootstrap gate used incomplete samples: %#v", first)
			}
			if first.CILowPct != second.CILowPct || first.CIHighPct != second.CIHighPct || first.ChangePct != second.ChangePct || first.Status != second.Status || first.Decision != second.Decision {
				t.Fatalf("seeded bootstrap is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
			}
			if math.Abs(first.ChangePct-test.wantChange) > 1e-9 {
				t.Fatalf("unexpected descriptive change: %#v", first)
			}
		})
	}
}

func writeCompareCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCompareFile(t, filepath.Join(root, "configs", "default", "postgresql.conf"), "shared_buffers = '128MB'\n")
	writeCompareFile(t, filepath.Join(root, "topologies", "single.env"), "TOPOLOGY_NAME=single\nTOPOLOGY_SERVICES=postgres\n")
	writeCompareFile(t, filepath.Join(root, "experiments", "benchmarks", "pgbench.env"), strings.Join([]string{
		"EXPERIMENT_NAME=Benchmark driver",
		"EXPERIMENT_TOPOLOGY=single",
		"EXPERIMENT_PG_CONFIG=default",
		"EXPERIMENT_WORKLOAD_SPEC=pgbench/tiny",
	}, "\n")+"\n")
	writeCompareFile(t, filepath.Join(root, "workloads", "pgbench", "tiny.env"), strings.Join([]string{
		"WORKLOAD_NAME=Tiny pgbench",
		"WORKLOAD_KIND=pgbench",
		"PGBENCH_MODE=builtin",
	}, "\n")+"\n")
	return root
}

func writeCompareSpec(t *testing.T, root string, threshold *float64, direction string, clients int, protocol string) {
	t.Helper()
	metric := "pgbench.tps"
	if direction == "lower" {
		metric = "pgbench.latency_mean_us"
	}
	lines := []string{
		"BENCHMARK_NAME=Synthetic comparison fixture",
		"BENCHMARK_CLASS=measurement",
		"BENCHMARK_DRIVER=pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/tiny",
		"BENCHMARK_PG_CONFIG=default",
		"BENCHMARK_MODE=fixed-time",
		"BENCHMARK_SCALE=1",
		fmt.Sprintf("BENCHMARK_CLIENTS=%d", clients),
		"BENCHMARK_THREADS=1",
		"BENCHMARK_WARMUP_SECONDS=0",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_TRIALS=5",
		"BENCHMARK_MIN_VALID_TRIALS=5",
		"BENCHMARK_RESET_POLICY=rebuild-per-trial",
		"BENCHMARK_CACHE_REGIME=uncontrolled",
		"BENCHMARK_STATISTICS_RESET_POLICY=none",
		"BENCHMARK_STATISTICS_RESET_BOUNDARY=none",
		"BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v1'",
		"BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1",
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified",
		"BENCHMARK_CLIENT_PLACEMENT=same-host",
		"BENCHMARK_RESOURCE_BUDGET_MODE=unbounded",
		"BENCHMARK_PRIMARY_METRIC=" + metric,
		"BENCHMARK_DIRECTION=" + direction,
		"BENCHMARK_MAX_CV_PCT=100",
		"BENCHMARK_PROTOCOL=" + protocol,
		"BENCHMARK_LOG_TRANSACTIONS=1",
		"BENCHMARK_LOG_SAMPLE_RATE=0.001",
	}
	if threshold != nil {
		lines = append(lines, fmt.Sprintf("BENCHMARK_REGRESSION_THRESHOLD_PCT=%g", *threshold))
	}
	writeCompareFile(t, filepath.Join(root, "benchmarks", "test.env"), strings.Join(lines, "\n")+"\n")
}

func runCompareSeries(t *testing.T, root, runID, subject, runtimeName string, values []float64, clients int, protocol, direction string) benchmarkrun.Series {
	t.Helper()
	if len(values) != 5 {
		t.Fatalf("comparison fixture requires 5 values, got %d", len(values))
	}
	call := 0
	clockCalls := 0
	base := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	options := benchmarkrun.Options{
		Runtime: runtimeName,
		RunID:   runID,
		Subject: subject,
		Now: func() time.Time {
			clockCalls++
			if clockCalls == 1 {
				return base
			}
			return base.Add(time.Hour)
		},
		Getenv: func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			if call >= len(values) {
				t.Fatalf("runner called more than %d times", len(values))
			}
			writeCompareLinkedRun(t, root, options, values[call], clients, protocol, direction)
			completeComparePhaseJournal(t, options.Env)
			started := base.Add(time.Duration(call) * 31 * time.Second)
			finished := started.Add(30 * time.Second)
			call++
			return experimentrun.Result{
				SchemaVersion: experimentrun.SchemaVersion,
				RunID:         options.RunID,
				Runtime:       options.Runtime,
				StartedAt:     started.Format(time.RFC3339Nano),
				FinishedAt:    finished.Format(time.RFC3339Nano),
				DurationMS:    30000,
				ExitCode:      0,
				Status:        "passed",
			}, nil
		},
	}
	if runtimeName == "native" {
		options.NativeBindir = fakeCompareToolchain(t, "shared-native")
	}
	series, err := benchmarkrun.Run(root, speccatalog.New(root), "test", options)
	if err != nil {
		t.Fatal(err)
	}
	if call != len(values) || !series.Passed() {
		t.Fatalf("synthetic series did not complete: calls=%d series=%#v", call, series)
	}
	return series
}

func completeComparePhaseJournal(t *testing.T, env []string) {
	t.Helper()
	values := make(map[string]string, len(env))
	for _, item := range env {
		if key, value, found := strings.Cut(item, "="); found {
			values[key] = value
		}
	}
	path := values["PGWORKBENCH_BENCHMARK_PHASE_FILE"]
	runID := values["PGWORKBENCH_BENCHMARK_RUN_ID"]
	trial := values["PGWORKBENCH_BENCHMARK_TRIAL"]
	if path == "" {
		t.Fatal("benchmark phase journal environment is missing")
	}
	prefix := runID + "\t" + trial + "\t"
	start, finish := compareTrialInterval(t, values)
	startText, finishText := start.Format(time.RFC3339Nano), finish.Format(time.RFC3339Nano)
	content := strings.Join([]string{
		prefix + "1\tpreflight\tpassed\t" + startText + "\t" + startText + "\t",
		prefix + "2\tprepare\tpassed\t" + startText + "\t" + startText + "\t",
		prefix + "3\tstabilize\tskipped\t" + startText + "\t" + startText + "\tno stabilization gate declared",
		prefix + "4\tpre-warmup-control\tskipped\t" + startText + "\t" + startText + "\tprotocol controls are not enabled",
		prefix + "5\twarmup\tskipped\t" + startText + "\t" + startText + "\tzero warmup duration",
		prefix + "6\tpre-measure-control\tskipped\t" + startText + "\t" + startText + "\tprotocol controls are not enabled",
		prefix + "7\tmeasure\tpassed\t" + startText + "\t" + finishText + "\t",
		prefix + "8\tcooldown\tpassed\t" + finishText + "\t" + finishText + "\t",
		prefix + "9\tvalidate\tpassed\t" + finishText + "\t" + finishText + "\t",
		prefix + "10\tcollect\tpassed\t" + finishText + "\t" + finishText + "\t",
		prefix + "11\tcleanup\tpassed\t" + finishText + "\t" + finishText + "\t",
	}, "\n") + "\n"
	runDir := filepath.Dir(filepath.Dir(values["PGBENCH_RESULT_FILE"]))
	primary := filepath.Join(runDir, "artifacts", "benchmark", "phases.tsv")
	for _, target := range []string{path, primary} {
		writeCompareFile(t, target, content)
	}
}

func writeCompareLinkedRun(t *testing.T, root string, options experimentrun.Options, value float64, clients int, protocol, direction string) {
	t.Helper()
	values := make(map[string]string, len(options.Env))
	for _, item := range options.Env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	runID := options.RunID
	runDir := filepath.Join(root, "runs", runID)
	started, finished := compareTrialInterval(t, values)
	startedText, finishedText := started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano)
	experimentPath := filepath.Join(root, "experiments", "benchmarks", "pgbench.env")
	experimentDigest, err := evidence.DigestFile(experimentPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := runstate.Manifest{
		RunID:                runID,
		StartedAt:            startedText,
		ExperimentSpec:       experimentPath,
		ExperimentSpecID:     "benchmarks/pgbench",
		ExperimentSpecRef:    "experiments/benchmarks/pgbench.env",
		ExperimentSpecDigest: experimentDigest,
		SourceSpecKind:       values["PGWORKBENCH_SOURCE_SPEC_KIND"],
		SourceSpecID:         values["PGWORKBENCH_SOURCE_SPEC_ID"],
		SourceSpecRef:        values["PGWORKBENCH_SOURCE_SPEC_REF"],
		SourceSpecDigest:     values["PGWORKBENCH_SOURCE_SPEC_DIGEST"],
		ExecutionParametersDigest: runstate.EffectiveParametersDigest(func(key string) string {
			return values[key]
		}),
		Runtime:                  options.Runtime,
		EngineVersion:            "0.3.0",
		EngineCommit:             strings.Repeat("a", 40),
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeFingerprintTarget: "primary",
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "170009",
		PostgresServerMajor:      "17",
		RuntimeFingerprintAt:     startedText,
		ExperimentName:           "Synthetic benchmark trial",
		ExperimentTopology:       values["EXPERIMENT_TOPOLOGY"],
		ExperimentPGConfig:       values["EXPERIMENT_PG_CONFIG"],
		Profile:                  values["EXPERIMENT_PROFILE"],
		DatasetSpec:              values["EXPERIMENT_DATASET_SPEC"],
		ProfileSize:              values["EXPERIMENT_PROFILE_SIZE"],
		WorkloadSpec:             values["EXPERIMENT_WORKLOAD_SPEC"],
		BackgroundSpecs:          values["EXPERIMENT_BACKGROUND_SPECS"],
		MetricsEnabled:           values["EXPERIMENT_METRICS"],
		MetricsSamples:           values["EXPERIMENT_METRICS_SAMPLES"],
	}
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}

	latencyMS, tps := 0.1, value
	if direction == "lower" {
		latencyMS, tps = value/1000, 1000
	}
	processed := int64(tps*30 + 0.5)
	summary := fmt.Sprintf(strings.Join([]string{
		"pgbench (17.9, server 17.9)",
		"transaction type: <builtin: TPC-B (sort of)>",
		"scaling factor: 1",
		"query mode: %s",
		"number of clients: %d",
		"number of threads: 1",
		"maximum number of tries: 1",
		"duration: 30 s",
		"number of transactions actually processed: %d",
		"number of failed transactions: 0 (0.000%%)",
		"latency average = %.6f ms",
		"latency stddev = 0.010 ms",
		"initial connection time = 2.000 ms",
		"tps = %.6f (without initial connection time)",
	}, "\n")+"\n", protocol, clients, processed, latencyMS, tps)
	writeCompareFile(t, filepath.Join(runDir, "driver", "pgbench-summary.log"), summary)
	targetHost, targetPort, driverService := "127.0.0.1", "5432", "postgres"
	driverImageID, targetImageID := "sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("d", 64)
	if options.Runtime == "native" {
		targetHost, targetPort, driverService = values["POSTGRES_HOST"], values["POSTGRES_PORT"], "native-host"
		driverImageID, targetImageID = "not-applicable", "not-applicable"
	}
	writeCompareFile(t, filepath.Join(runDir, "stdout.log"), fmt.Sprintf("pgworkbench_benchmark_target=direct-postgres endpoint_contract=pgworkbench.pgbench-target/direct-postgres/v1 driver_service=%s endpoint_host=%s endpoint_port=%s driver_image_id=%s target_image_id=%s\n", driverService, targetHost, targetPort, driverImageID, targetImageID))
	completion := started.Add(time.Millisecond)
	writeCompareFile(t, filepath.Join(runDir, "driver", "pgbench-raw", "pgbench_log.1"), fmt.Sprintf("0 1 %.0f 0 %d %d\n", latencyMS*1000, completion.Unix(), completion.Nanosecond()/1000))
	metricTimes := make([]string, 32)
	for index := range metricTimes {
		metricTimes[index] = started.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
	}
	writeCompareFile(t, filepath.Join(runDir, "metrics.csv"), string(metricstest.CSV(metricTimes, "postgres")))

	verdict := runstate.Verdict{
		RunID:            runID,
		Status:           runstate.VerdictStatusPassed,
		Message:          "synthetic benchmark trial passed",
		StartedAt:        startedText,
		FinishedAt:       finishedText,
		ExperimentSpecID: "benchmarks/pgbench",
	}
	if err := runstate.WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}
}

func fakeCompareToolchain(t *testing.T, identity string) string {
	t.Helper()
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range nativetoolchain.RequiredExecutableNames() {
		if err := os.WriteFile(filepath.Join(bindir, name), []byte("#!/bin/sh\necho '"+name+" (PostgreSQL) "+identity+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bindir
}

func deterministicCompareOptions() Options {
	return Options{BootstrapResamples: 2000, ConfidenceLevel: 0.95, Seed: 42}
}

func compareTrialInterval(t *testing.T, values map[string]string) (time.Time, time.Time) {
	t.Helper()
	trial, err := strconv.Atoi(values["PGWORKBENCH_BENCHMARK_TRIAL"])
	if err != nil || trial <= 0 {
		t.Fatalf("invalid synthetic benchmark trial binding %q", values["PGWORKBENCH_BENCHMARK_TRIAL"])
	}
	started := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC).Add(time.Duration(trial-1) * 31 * time.Second)
	return started, started.Add(30 * time.Second)
}

func readCompareJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, value); err != nil {
		t.Fatal(err)
	}
}

func copyCompareTree(t *testing.T, source string, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
}

func writeCompareJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, path, string(content)+"\n")
}

func writeCompareFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsCompareReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}
