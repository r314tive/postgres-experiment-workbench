package benchmarkab

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metricstest "github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics/testfixture"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkqualify"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestRunInitialArtifactWriteFailureLeavesNoFinalOrStagingDirectory(t *testing.T) {
	root := t.TempDir()
	writeABCatalog(t, root)
	memory, storage, load := 10.0, 10.0, 1.0
	qualification := benchmarkqualify.InspectOptions{
		StorageLabel:    "postgres-data",
		ClientPlacement: "same-host",
		Policy: benchmarkqualify.Policy{
			Strict:                  true,
			MinMemoryAvailablePct:   &memory,
			MinStorageAvailablePct:  &storage,
			MaxLoad1PerCPU:          &load,
			RequiredClocksource:     "tsc",
			RequiredGovernor:        "performance",
			RequiredClientPlacement: "same-host",
		},
	}
	runID := "atomic-init-write-failure"
	result, err := Run(root, speccatalog.New(root), "ab/baseline", "ab/candidate", Options{
		Runtime:          "docker",
		RunID:            runID,
		BaselineSubject:  "baseline",
		CandidateSubject: "candidate",
		Qualification:    qualification,
		Now: func() time.Time {
			return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
		},
		InspectHost: func(options benchmarkqualify.InspectOptions) (benchmarkqualify.Artifact, error) {
			artifact, inspectErr := syntheticABQualification(options)
			if inspectErr != nil {
				return benchmarkqualify.Artifact{}, inspectErr
			}
			notANumber := math.NaN()
			artifact.Snapshot.Memory.AvailablePct.Value = &notANumber
			return artifact, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "write before host qualification") {
		t.Fatalf("synthetic initial artifact failure was not returned: result=%#v err=%v", result, err)
	}
	if result.ArtifactDir != "" {
		t.Fatalf("unpublished initial artifact reported a final directory: %#v", result)
	}
	finalDir := filepath.Join(root, "runs", "benchmark-ab", runID)
	if _, statErr := os.Lstat(finalDir); !os.IsNotExist(statErr) {
		t.Fatalf("partial final A/B directory survived initial write failure: %v", statErr)
	}
	staging, globErr := filepath.Glob(filepath.Join(filepath.Dir(finalDir), "."+runID+".staging-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(staging) != 0 {
		t.Fatalf("A/B staging debris survived initial write failure: %v", staging)
	}
}

func TestCounterbalancedABReleaseArtifactVerifiesAfterRelocation(t *testing.T) {
	fixture := produceCounterbalancedABFixture(t)
	archivePath := filepath.Join(t.TempDir(), "counterbalanced-ab.tar.gz")
	bundle, err := CreateBundle(fixture.root, fixture.result.ArtifactDir, archivePath, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	secondArchivePath := filepath.Join(t.TempDir(), "counterbalanced-ab.tar.gz")
	secondBundle, err := CreateBundle(fixture.root, fixture.result.ArtifactDir, secondArchivePath, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Series != 2 || bundle.LinkedRuns != 20 || bundle.Files == 0 || !evidence.IsDigest(bundle.Digest) {
		t.Fatalf("unexpected bundle closure: %#v", bundle)
	}
	if bundle.Digest != secondBundle.Digest || string(readABFile(t, archivePath)) != string(readABFile(t, secondArchivePath)) {
		t.Fatal("identical counterbalanced A/B bundles are not byte-for-byte reproducible")
	}

	// Moving the original artifact root out of the way ensures verification
	// cannot accidentally follow an absolute producer path after extraction.
	hiddenSource := fixture.root + "-moved"
	if err := os.Rename(fixture.root, hiddenSource); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(hiddenSource, fixture.root); err != nil {
			t.Errorf("restore source fixture for TempDir cleanup: %v", err)
		}
	}()
	extractRoot := t.TempDir()
	extractABBundle(t, archivePath, extractRoot)
	bundleRoot := filepath.Join(extractRoot, bundle.RootName)
	relocatedRun := filepath.Join(bundleRoot, "runs", "benchmark-ab", fixture.result.RunID)
	verification, err := VerifyBundle(bundleRoot, relocatedRun)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.IsValid() {
		t.Fatalf("relocated counterbalanced A/B bundle is invalid: %v", verification.Issues)
	}
	if verification.Result == nil || verification.Result.RunDir != "." || verification.Result.Baseline.Ref == "" || filepath.IsAbs(verification.Result.Baseline.Ref) {
		t.Fatalf("relocated result is not portable: %#v", verification.Result)
	}
}

func TestCreateCounterbalancedABBundleRejectsTamperedStageBeforeArchive(t *testing.T) {
	fixture := produceCounterbalancedABFixture(t)
	output := filepath.Join(t.TempDir(), "tampered-stage.tar.gz")
	_, err := createBundle(fixture.root, fixture.result.ArtifactDir, output, time.Unix(0, 0).UTC(), func(stage string) error {
		path := filepath.Join(stage, "runs", "benchmark-ab", fixture.result.RunID, "result.json")
		return os.WriteFile(path, append(readABFile(t, path), ' '), 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "staged counterbalanced benchmark bundle is invalid") {
		t.Fatalf("tampered staged counterbalanced bundle was published: %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive exists after staged verification failure: %v", statErr)
	}
}

func TestCounterbalancedABLiveVerifierReopensEffectiveSettingsRawSource(t *testing.T) {
	fixture := produceCounterbalancedABFixture(t)
	seriesPath := filepath.Join(fixture.root, filepath.FromSlash(fixture.result.Baseline.Ref), "result.json")
	var series benchmarkrun.Series
	readABJSON(t, seriesPath, &series)
	if len(series.Trials) == 0 || series.Trials[0].EffectiveSettings == nil {
		t.Fatal("synthetic A/B fixture has no effective pg_settings evidence")
	}
	rawPath := filepath.Join(fixture.root, filepath.FromSlash(series.Trials[0].RunRef), filepath.FromSlash(series.Trials[0].EffectiveSettings.Source.Path))
	if err := os.WriteFile(rawPath, append(readABFile(t, rawPath), []byte("extra\trow\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	verification, err := Verify(fixture.root, fixture.result.ArtifactDir)
	if err != nil {
		t.Fatal(err)
	}
	if verification.IsValid() || !reasonContains(verification.Issues, "effective pg_settings source") {
		t.Fatalf("raw effective pg_settings tamper was not independently detected: %v", verification.Issues)
	}
}

func TestCounterbalancedABBundleOutputRejectsDirectChildAndAliasedParent(t *testing.T) {
	source := t.TempDir()
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tests := []struct {
		name   string
		output string
	}{
		{name: "direct child", output: filepath.Join(source, "direct.tar.gz")},
		{name: "aliased parent", output: filepath.Join(alias, "aliased.tar.gz")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveCounterbalancedBundleOutput(source, test.output, "test source")
			if !errors.Is(err, pathguard.ErrOutputWithinSource) {
				t.Fatalf("expected output-containment error, got %v", err)
			}
		})
	}
}

func TestCounterbalancedABBundleTamperMatrixFailsClosed(t *testing.T) {
	fixture := produceCounterbalancedABFixture(t)
	archivePath := filepath.Join(t.TempDir(), "counterbalanced-ab.tar.gz")
	bundle, err := CreateBundle(fixture.root, fixture.result.ArtifactDir, archivePath, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		wantIssue string
		mutate    func(t *testing.T, root, runDir string)
	}{
		{
			name:      "protocol",
			wantIssue: "protocol verification failed",
			mutate: func(t *testing.T, _, runDir string) {
				var protocol Protocol
				readABJSON(t, filepath.Join(runDir, "protocol.json"), &protocol)
				protocol.Orders[0] = "BA"
				writeABJSON(t, filepath.Join(runDir, "protocol.json"), protocol)
			},
		},
		{
			name:      "result",
			wantIssue: "terminal status, decision, or reasons",
			mutate: func(t *testing.T, _, runDir string) {
				var result Result
				readABJSON(t, filepath.Join(runDir, "result.json"), &result)
				result.Decision = "forged"
				writeABJSON(t, filepath.Join(runDir, "result.json"), result)
			},
		},
		{
			name:      "series",
			wantIssue: "baseline series result digest mismatch",
			mutate: func(t *testing.T, root, runDir string) {
				var result Result
				readABJSON(t, filepath.Join(runDir, "result.json"), &result)
				seriesPath := filepath.Join(root, filepath.FromSlash(result.Baseline.Ref), "result.json")
				var series benchmarkrun.Series
				readABJSON(t, seriesPath, &series)
				series.Subject = "forged-subject"
				writeABJSON(t, seriesPath, series)
			},
		},
		{
			name:      "qualification",
			wantIssue: "before qualification invalid",
			mutate: func(t *testing.T, _, runDir string) {
				path := filepath.Join(runDir, "qualification", "before.json")
				var artifact benchmarkqualify.Artifact
				readABJSON(t, path, &artifact)
				artifact.Verdict = benchmarkqualify.VerdictUnqualified
				writeABJSON(t, path, artifact)
			},
		},
		{
			name:      "block",
			wantIssue: "does not match independently reconstructed block",
			mutate: func(t *testing.T, _, runDir string) {
				path := filepath.Join(runDir, "blocks", "001-ab.json")
				var block Block
				readABJSON(t, path, &block)
				block.Status = "invalid"
				writeABJSON(t, path, block)
			},
		},
		{
			name:      "inventory digest",
			wantIssue: "file digest or size mismatch",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readABBundleInventory(t, root)
				inventory.Files[0].Digest = "sha256:" + strings.Repeat("f", 64)
				writeABJSON(t, filepath.Join(root, BundleInventoryName), inventory)
			},
		},
		{
			name:      "extra file",
			wantIssue: "file count mismatch",
			mutate: func(t *testing.T, root, _ string) {
				writeABFile(t, filepath.Join(root, "unexpected.txt"), "not inventoried\n")
			},
		},
		{
			name:      "missing file",
			wantIssue: "references missing file",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readABBundleInventory(t, root)
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(inventory.Files[0].Path))); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "duplicate inventory path",
			wantIssue: "duplicate path",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readABBundleInventory(t, root)
				inventory.Files = append(inventory.Files, inventory.Files[0])
				writeABJSON(t, filepath.Join(root, BundleInventoryName), inventory)
			},
		},
		{
			name:      "inventory path traversal",
			wantIssue: "invalid entry",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readABBundleInventory(t, root)
				inventory.Files[0].Path = "../outside"
				writeABJSON(t, filepath.Join(root, BundleInventoryName), inventory)
			},
		},
		{
			name:      "unsorted inventory",
			wantIssue: "inventory is not sorted",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readABBundleInventory(t, root)
				inventory.Files[0], inventory.Files[1] = inventory.Files[1], inventory.Files[0]
				writeABJSON(t, filepath.Join(root, BundleInventoryName), inventory)
			},
		},
		{
			name:      "symlink",
			wantIssue: "bundle inventory failed",
			mutate: func(t *testing.T, root, _ string) {
				if err := os.Symlink(BundleInventoryName, filepath.Join(root, "unsafe-link")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extractRoot := t.TempDir()
			extractABBundle(t, archivePath, extractRoot)
			root := filepath.Join(extractRoot, bundle.RootName)
			runDir := filepath.Join(root, "runs", "benchmark-ab", fixture.result.RunID)
			test.mutate(t, root, runDir)

			verification, err := VerifyBundle(root, runDir)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || len(verification.Issues) == 0 {
				t.Fatalf("%s tamper unexpectedly verified: %#v", test.name, verification)
			}
			if !reasonContains(verification.Issues, test.wantIssue) {
				t.Fatalf("%s tamper issues omit %q: %v", test.name, test.wantIssue, verification.Issues)
			}
		})
	}
}

type counterbalancedABFixture struct {
	root   string
	result Result
}

func produceCounterbalancedABFixture(t *testing.T) counterbalancedABFixture {
	t.Helper()
	root := t.TempDir()
	writeABCatalog(t, root)
	catalog := speccatalog.New(root)
	clock := newABFixtureClock(time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC))
	memory, storage, load := 10.0, 10.0, 1.0
	qualification := benchmarkqualify.InspectOptions{
		StorageLabel:    "postgres-data",
		ClientPlacement: "same-host",
		Policy: benchmarkqualify.Policy{
			Strict:                  true,
			MinMemoryAvailablePct:   &memory,
			MinStorageAvailablePct:  &storage,
			MaxLoad1PerCPU:          &load,
			RequiredClocksource:     "tsc",
			RequiredGovernor:        "performance",
			RequiredClientPlacement: "same-host",
		},
	}
	options := Options{
		Runtime:              "native",
		RunID:                "synthetic-counterbalanced-ab",
		BaselineSubject:      "default-128mb",
		CandidateSubject:     "candidate-256mb",
		BootstrapResamples:   1000,
		ConfidenceLevel:      0.95,
		Seed:                 42,
		Qualification:        qualification,
		MaxBookendGapSeconds: 3600,
		InspectHost:          syntheticABQualification,
		Now:                  clock,
		Stdout:               io.Discard,
		Stderr:               io.Discard,
		SeriesOptions: benchmarkrun.Options{
			EngineVersion: "0.3.0",
			EngineCommit:  strings.Repeat("a", 40),
			NativeBindir:  fakeABReleaseToolchain(t),
			Getenv:        func(string) string { return "" },
			RunExperiment: syntheticABExperiment,
			VerifyRun:     runverify.Verify,
		},
	}
	result, err := Run(root, catalog, "ab/baseline", "ab/candidate", options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" || result.Qualification.Assessment.Status != benchmarkqualify.BookendStatusRecordedPolicyPassed || len(result.Blocks) != 10 {
		t.Fatalf("synthetic producer did not close the A/B contract: %#v", result)
	}
	verification, err := Verify(root, result.ArtifactDir)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.IsValid() {
		t.Fatalf("produced counterbalanced A/B fixture is invalid: %v", verification.Issues)
	}
	return counterbalancedABFixture{root: root, result: result}
}

func writeABCatalog(t *testing.T, root string) {
	t.Helper()
	writeABFile(t, filepath.Join(root, "topologies", "single.env"), "TOPOLOGY_NAME=single\nTOPOLOGY_SERVICES=postgres\n")
	writeABFile(t, filepath.Join(root, "experiments", "benchmarks", "pgbench.env"), "EXPERIMENT_NAME=Synthetic_A_B_benchmark\nEXPERIMENT_TOPOLOGY=single\n")
	writeABFile(t, filepath.Join(root, "workloads", "pgbench", "tpcb.env"), "WORKLOAD_NAME=Synthetic_pgbench\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=builtin\n")
	writeABFile(t, filepath.Join(root, "configs", "default", "postgresql.conf"), "shared_buffers = '128MB'\n")
	writeABFile(t, filepath.Join(root, "configs", "candidate", "postgresql.conf"), "shared_buffers = '256MB'\n")

	common := strings.Join([]string{
		"BENCHMARK_NAME=Synthetic_counterbalanced_A_B",
		"BENCHMARK_CLASS=measurement",
		"BENCHMARK_DRIVER=pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/tpcb",
		"BENCHMARK_MODE=fixed-time",
		"BENCHMARK_SCALE=1",
		"BENCHMARK_CLIENTS=2",
		"BENCHMARK_THREADS=1",
		"BENCHMARK_WARMUP_SECONDS=0",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_TRIALS=10",
		"BENCHMARK_MIN_VALID_TRIALS=10",
		"BENCHMARK_RESET_POLICY=rebuild-per-trial",
		"BENCHMARK_CACHE_REGIME=uncontrolled",
		"BENCHMARK_STATISTICS_RESET_POLICY=none",
		"BENCHMARK_STATISTICS_RESET_BOUNDARY=none",
		"BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v1'",
		"BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1",
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified",
		"BENCHMARK_CLIENT_PLACEMENT=same-host",
		"BENCHMARK_RESOURCE_BUDGET_MODE=unbounded",
		"BENCHMARK_PRIMARY_METRIC=pgbench.tps",
		"BENCHMARK_DIRECTION=higher",
		"BENCHMARK_MAX_CV_PCT=100",
		"BENCHMARK_REGRESSION_THRESHOLD_PCT=5",
		"BENCHMARK_CONNECT_PER_TRANSACTION=0",
		"BENCHMARK_PROTOCOL=simple",
		"BENCHMARK_LOG_TRANSACTIONS=1",
		"BENCHMARK_LOG_SAMPLE_RATE=0.001",
		"BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES=pg_config",
	}, "\n") + "\n"
	writeABFile(t, filepath.Join(root, "benchmarks", "ab", "baseline.env"), common+"BENCHMARK_PG_CONFIG=default\n")
	writeABFile(t, filepath.Join(root, "benchmarks", "ab", "candidate.env"), common+"BENCHMARK_PG_CONFIG=candidate\n")
}

func syntheticABExperiment(root string, _ speccatalog.Catalog, input string, options experimentrun.Options) (experimentrun.Result, error) {
	environment := envSliceMap(options.Env)
	phaseStart := options.Now().UTC()
	phaseFinish := phaseStart.Add(30 * time.Second)
	verdictStart := phaseStart.Add(time.Second)
	verdictFinish := phaseFinish.Add(-time.Second)
	runDir := filepath.Join(root, "runs", options.RunID)
	phasePath := environment["PGWORKBENCH_BENCHMARK_PHASE_FILE"]
	if phasePath == "" {
		return experimentrun.Result{}, fmt.Errorf("synthetic A/B experiment has no phase journal")
	}
	if err := writeSyntheticABPhases(phasePath, runDir, options.RunID, environment["PGWORKBENCH_BENCHMARK_TRIAL"], phaseStart, phaseFinish); err != nil {
		return experimentrun.Result{}, err
	}
	effectiveValue := "16384"
	if environment["PGWORKBENCH_SOURCE_SPEC_ID"] == "ab/candidate" {
		effectiveValue = "32768"
	}
	effectiveSettings := strings.Join([]string{
		"run_id\tprotocol_digest\ttrial\tcaptured_at\tserver_version_num\tname\tsetting\tunit\tsource\tpending_restart\tcontext",
		strings.Join([]string{
			options.RunID,
			environment["PGWORKBENCH_AB_PROTOCOL_DIGEST"],
			environment["PGWORKBENCH_BENCHMARK_TRIAL"],
			phaseStart.Format(time.RFC3339Nano),
			"170009",
			"shared_buffers",
			effectiveValue,
			"8kB",
			"configuration file",
			"f",
			"postmaster",
		}, "\t"),
	}, "\n") + "\n"
	effectivePath := filepath.Join(runDir, "artifacts", "benchmark", "effective-pg-settings.tsv")
	if err := os.WriteFile(effectivePath, []byte(effectiveSettings), 0o600); err != nil {
		return experimentrun.Result{}, err
	}

	experimentPath := filepath.Join(root, "experiments", "benchmarks", "pgbench.env")
	experimentDigest, err := evidence.DigestFile(experimentPath)
	if err != nil {
		return experimentrun.Result{}, err
	}
	manifest := runstate.Manifest{
		RunID:                options.RunID,
		StartedAt:            verdictStart.Format(time.RFC3339Nano),
		ExperimentSpec:       experimentPath,
		ExperimentSpecID:     input,
		ExperimentSpecRef:    "experiments/benchmarks/pgbench.env",
		ExperimentSpecDigest: experimentDigest,
		SourceSpecKind:       environment["PGWORKBENCH_SOURCE_SPEC_KIND"],
		SourceSpecID:         environment["PGWORKBENCH_SOURCE_SPEC_ID"],
		SourceSpecRef:        environment["PGWORKBENCH_SOURCE_SPEC_REF"],
		SourceSpecDigest:     environment["PGWORKBENCH_SOURCE_SPEC_DIGEST"],
		ExecutionParametersDigest: runstate.EffectiveParametersDigest(func(key string) string {
			return environment[key]
		}),
		Runtime:                  options.Runtime,
		EngineVersion:            options.EngineVersion,
		EngineCommit:             options.EngineCommit,
		PackID:                   options.PackID,
		PackVersion:              options.PackVersion,
		PackDigest:               options.PackDigest,
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeFingerprintTarget: "primary",
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "170009",
		PostgresServerMajor:      "17",
		RuntimeFingerprintAt:     verdictStart.Format(time.RFC3339Nano),
		ExperimentName:           "Synthetic A/B benchmark trial",
		ExperimentTopology:       environment["EXPERIMENT_TOPOLOGY"],
		ExperimentPGConfig:       environment["EXPERIMENT_PG_CONFIG"],
		Profile:                  environment["EXPERIMENT_PROFILE"],
		DatasetSpec:              environment["EXPERIMENT_DATASET_SPEC"],
		ProfileSize:              environment["EXPERIMENT_PROFILE_SIZE"],
		WorkloadSpec:             environment["EXPERIMENT_WORKLOAD_SPEC"],
		BackgroundSpecs:          environment["EXPERIMENT_BACKGROUND_SPECS"],
		MetricsEnabled:           environment["EXPERIMENT_METRICS"],
		MetricsSamples:           environment["EXPERIMENT_METRICS_SAMPLES"],
	}
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		return experimentrun.Result{}, err
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID:            options.RunID,
		Status:           runstate.VerdictStatusPassed,
		Message:          "synthetic A/B benchmark trial passed",
		StartedAt:        verdictStart.Format(time.RFC3339Nano),
		FinishedAt:       verdictFinish.Format(time.RFC3339Nano),
		ExperimentSpecID: input,
	}); err != nil {
		return experimentrun.Result{}, err
	}
	if err := os.MkdirAll(filepath.Join(runDir, "driver", "pgbench-raw"), 0o755); err != nil {
		return experimentrun.Result{}, err
	}
	metricTimes := make([]string, int(phaseFinish.Sub(phaseStart)/time.Second)+1)
	for index := range metricTimes {
		metricTimes[index] = phaseStart.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
	}
	metrics := metricstest.CSV(metricTimes, "postgres")
	if err := os.WriteFile(filepath.Join(runDir, "metrics.csv"), metrics, 0o644); err != nil {
		return experimentrun.Result{}, err
	}
	tps := 100.0
	if environment["PGWORKBENCH_SOURCE_SPEC_ID"] == "ab/candidate" {
		tps = 101.0
	}
	processed := int64(tps*30 + 0.5)
	summary := strings.Join([]string{
		"pgbench (17.9, server 17.9)",
		"transaction type: <builtin: TPC-B (sort of)>",
		"scaling factor: 1",
		"query mode: simple",
		"number of clients: 2",
		"number of threads: 1",
		"maximum number of tries: 1",
		"duration: 30 s",
		fmt.Sprintf("number of transactions actually processed: %d", processed),
		"number of failed transactions: 0 (0.000%)",
		"latency average = 0.150 ms",
		"latency stddev = 0.100 ms",
		"initial connection time = 2.000 ms",
		fmt.Sprintf("tps = %.6f (without initial connection time)", tps),
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "driver", "pgbench-summary.log"), []byte(summary), 0o644); err != nil {
		return experimentrun.Result{}, err
	}
	if err := os.WriteFile(filepath.Join(runDir, "stdout.log"), []byte(fmt.Sprintf("pgworkbench_benchmark_target=direct-postgres endpoint_contract=pgworkbench.pgbench-target/direct-postgres/v1 driver_service=native-host endpoint_host=%s endpoint_port=%s driver_image_id=not-applicable target_image_id=not-applicable\n", environment["POSTGRES_HOST"], environment["POSTGRES_PORT"])), 0o644); err != nil {
		return experimentrun.Result{}, err
	}
	rawLog := fmt.Sprintf("0 1 100 0 %d 780769\n1 1 200 0 %d 781111\n", phaseStart.Unix(), phaseStart.Unix())
	if err := os.WriteFile(filepath.Join(runDir, "driver", "pgbench-raw", "pgbench_log.1"), []byte(rawLog), 0o644); err != nil {
		return experimentrun.Result{}, err
	}
	return experimentrun.Result{
		SchemaVersion:  experimentrun.SchemaVersion,
		ExperimentSpec: input,
		ExperimentName: "Synthetic A/B benchmark trial",
		SpecPath:       experimentPath,
		SpecSHA256:     experimentDigest,
		Runtime:        options.Runtime,
		Topology:       "single",
		RunID:          options.RunID,
		RunDir:         runDir,
		EngineVersion:  options.EngineVersion,
		EngineCommit:   options.EngineCommit,
		StartedAt:      verdictStart.Format(time.RFC3339Nano),
		FinishedAt:     verdictFinish.Format(time.RFC3339Nano),
		DurationMS:     verdictFinish.Sub(verdictStart).Milliseconds(),
		ExitCode:       0,
		Status:         "passed",
	}, nil
}

func fakeABReleaseToolchain(t *testing.T) string {
	t.Helper()
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range nativetoolchain.RequiredExecutableNames() {
		if err := os.WriteFile(filepath.Join(bindir, name), []byte("#!/bin/sh\necho '"+name+" (PostgreSQL) synthetic-ab'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bindir
}

// syntheticABQualification creates schema-valid, unsigned recorded-content
// test evidence only. It does not inspect or make a claim about the test host,
// and every artifact remains confined to t.TempDir through the producer.
func syntheticABQualification(options benchmarkqualify.InspectOptions) (benchmarkqualify.Artifact, error) {
	if options.StorageLabel != "postgres-data" || options.ClientPlacement != "same-host" || options.Policy.MinMemoryAvailablePct == nil || *options.Policy.MinMemoryAvailablePct != 10 || options.Policy.MinStorageAvailablePct == nil || *options.Policy.MinStorageAvailablePct != 10 || options.Policy.MaxLoad1PerCPU == nil || *options.Policy.MaxLoad1PerCPU != 1 || options.Policy.RequiredClocksource != "tsc" || options.Policy.RequiredGovernor != "performance" || options.Policy.RequiredClientPlacement != "same-host" {
		return benchmarkqualify.Artifact{}, fmt.Errorf("unexpected synthetic qualification protocol")
	}
	logicalCPUs, total, available, runnable, processes := uint64(8), uint64(1000), uint64(800), uint64(1), uint64(100)
	memoryPct, storagePct, load, loadPerCPU, loadHeadroom := 80.0, 80.0, 0.8, 0.1, 90.0
	snapshot := benchmarkqualify.Snapshot{
		Platform: benchmarkqualify.PlatformSnapshot{
			OS:           abObservedString("linux", "runtime"),
			Architecture: abObservedString("amd64", "runtime"),
			Kernel:       abObservedString("6.1.0-synthetic", "procfs"),
		},
		CPU: benchmarkqualify.CPUSnapshot{
			Model:       abObservedString("Synthetic-CPU", "procfs"),
			LogicalCPUs: benchmarkqualify.UintObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &logicalCPUs, Source: "runtime"},
		},
		Memory: benchmarkqualify.CapacitySnapshot{
			TotalBytes:     benchmarkqualify.UintObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &total, Source: "procfs"},
			AvailableBytes: benchmarkqualify.UintObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &available, Source: "procfs"},
			AvailablePct:   benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &memoryPct, Source: "derived"},
		},
		Storage: benchmarkqualify.StorageSnapshot{
			Label:          "postgres-data",
			Filesystem:     abObservedString("ext4", "statfs"),
			TotalBytes:     benchmarkqualify.UintObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &total, Source: "statfs"},
			AvailableBytes: benchmarkqualify.UintObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &available, Source: "statfs"},
			AvailablePct:   benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &storagePct, Source: "derived"},
		},
		Clock:   benchmarkqualify.ClockSnapshot{Clocksource: abObservedString("tsc", "sysfs")},
		Power:   benchmarkqualify.PowerSnapshot{Governors: benchmarkqualify.StringListObservation{Availability: benchmarkqualify.AvailabilityObserved, Values: []string{"performance"}, Source: "sysfs"}},
		Thermal: benchmarkqualify.ThermalSnapshot{MaxCelsius: benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityUnavailable, Source: "sysfs"}},
		Client:  benchmarkqualify.ClientSnapshot{Placement: abObservedString("same-host", "operator")},
		Interference: benchmarkqualify.InterferenceSnapshot{
			Load1:             benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &load, Source: "procfs"},
			Load1PerCPU:       benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &loadPerCPU, Source: "derived"},
			RunnableProcesses: benchmarkqualify.UintObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &runnable, Source: "procfs"},
			ProcessCount:      benchmarkqualify.UintObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &processes, Source: "procfs"},
		},
		Headroom: benchmarkqualify.HeadroomSnapshot{
			LoadCapacityPct: benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &loadHeadroom, Source: "derived"},
			MemoryPct:       benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &memoryPct, Source: "derived"},
			StoragePct:      benchmarkqualify.NumberObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: &storagePct, Source: "derived"},
		},
	}
	artifact := benchmarkqualify.Artifact{
		SchemaVersion:    benchmarkqualify.SchemaVersion,
		ArtifactType:     benchmarkqualify.ArtifactType,
		CollectorVersion: benchmarkqualify.CollectorVersion,
		RecordedAt:       options.RecordedAt.UTC().Format(time.RFC3339Nano),
		Assurance: benchmarkqualify.Assurance{
			EvidenceOrigin:    benchmarkqualify.EvidenceOriginOperatorRecorded,
			Signed:            false,
			DigestPurpose:     benchmarkqualify.DigestPurposeIntegrityOnly,
			VerificationScope: benchmarkqualify.VerificationScopeRecordedOnly,
		},
		Snapshot: snapshot,
		Policy:   options.Policy,
		Checks: []benchmarkqualify.Check{
			{Gate: "min_memory_available_pct", Status: benchmarkqualify.CheckPassed, Observation: "80", Requirement: ">= 10"},
			{Gate: "min_storage_available_pct", Status: benchmarkqualify.CheckPassed, Observation: "80", Requirement: ">= 10"},
			{Gate: "max_load_1m_per_cpu", Status: benchmarkqualify.CheckPassed, Observation: "0.1", Requirement: "<= 1"},
			{Gate: "required_clocksource", Status: benchmarkqualify.CheckPassed, Observation: "tsc", Requirement: "= tsc"},
			{Gate: "required_governor", Status: benchmarkqualify.CheckPassed, Observation: "[performance]", Requirement: "contains performance"},
			{Gate: "required_client_placement", Status: benchmarkqualify.CheckPassed, Observation: "same-host", Requirement: "= same-host"},
		},
		Verdict: benchmarkqualify.VerdictQualified,
		Reasons: []string{},
	}
	var err error
	artifact.SnapshotDigest, err = abJSONDigest(artifact.Snapshot)
	if err != nil {
		return benchmarkqualify.Artifact{}, err
	}
	digestView := artifact
	digestView.Digest = ""
	artifact.Digest, err = abJSONDigest(digestView)
	if err != nil {
		return benchmarkqualify.Artifact{}, err
	}
	if verification := benchmarkqualify.Verify(artifact); !verification.Valid {
		return benchmarkqualify.Artifact{}, fmt.Errorf("synthetic qualification fixture is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	return artifact, nil
}

func writeSyntheticABPhases(path string, runDir string, runID string, trial string, started, finished time.Time) error {
	start := started.Format(time.RFC3339Nano)
	finish := finished.Format(time.RFC3339Nano)
	prefix := runID + "\t" + trial + "\t"
	content := strings.Join([]string{
		fmt.Sprintf("%s1\tpreflight\tpassed\t%s\t%s\t", prefix, start, start),
		fmt.Sprintf("%s2\tprepare\tpassed\t%s\t%s\t", prefix, start, start),
		fmt.Sprintf("%s3\tstabilize\tskipped\t%s\t%s\tno stabilization gate declared", prefix, start, start),
		fmt.Sprintf("%s4\tpre-warmup-control\tskipped\t%s\t%s\tprotocol controls are not enabled", prefix, start, start),
		fmt.Sprintf("%s5\twarmup\tskipped\t%s\t%s\tzero warmup duration", prefix, start, start),
		fmt.Sprintf("%s6\tpre-measure-control\tskipped\t%s\t%s\tprotocol controls are not enabled", prefix, start, start),
		fmt.Sprintf("%s7\tmeasure\tpassed\t%s\t%s\t", prefix, start, finish),
		fmt.Sprintf("%s8\tcooldown\tpassed\t%s\t%s\t", prefix, finish, finish),
		fmt.Sprintf("%s9\tvalidate\tpassed\t%s\t%s\t", prefix, finish, finish),
		fmt.Sprintf("%s10\tcollect\tpassed\t%s\t%s\t", prefix, finish, finish),
		fmt.Sprintf("%s11\tcleanup\tpassed\t%s\t%s\t", prefix, finish, finish),
	}, "\n") + "\n"
	primary := filepath.Join(runDir, "artifacts", "benchmark", "phases.tsv")
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	return os.WriteFile(primary, []byte(content), 0o600)
}

func newABFixtureClock(initial time.Time) func() time.Time {
	current := initial
	return func() time.Time {
		value := current
		current = current.Add(time.Minute)
		return value
	}
}

func envSliceMap(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if ok {
			result[key] = item
		}
	}
	return result
}

func abObservedString(value, source string) benchmarkqualify.StringObservation {
	return benchmarkqualify.StringObservation{Availability: benchmarkqualify.AvailabilityObserved, Value: value, Source: source}
}

func abJSONDigest(value any) (string, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func readABBundleInventory(t *testing.T, root string) BundleInventory {
	t.Helper()
	var inventory BundleInventory
	readABJSON(t, filepath.Join(root, BundleInventoryName), &inventory)
	if len(inventory.Files) < 2 {
		t.Fatalf("bundle inventory is too small for tamper tests: %#v", inventory)
	}
	return inventory
}

func readABJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, value); err != nil {
		t.Fatal(err)
	}
}

func readABFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func writeABJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeABFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func extractABBundle(t *testing.T, archivePath, destination string) {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !evidence.IsPortablePath(name) {
			t.Fatalf("archive contains unsafe path %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			_, copyErr := io.Copy(output, reader)
			if closeErr := output.Close(); copyErr != nil || closeErr != nil {
				t.Fatalf("extract %s: copy=%v close=%v", header.Name, copyErr, closeErr)
			}
		default:
			t.Fatalf("archive contains unsupported entry %q type %d", header.Name, header.Typeflag)
		}
	}
}
