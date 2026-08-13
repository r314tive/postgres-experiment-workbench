package benchmarkcampaign

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	metricstest "github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics/testfixture"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestRunPredeclaresOrderedProtocolAndRetainsEveryFailure(t *testing.T) {
	root := writeCampaignCatalog(t)
	writeCampaignSpec(t, root, "clients-1", 1, "select-only")
	writeCampaignSpec(t, root, "clients-8", 8, "builtin")

	calls := 0
	want := []string{"clients-1", "clients-8"}
	result, err := Run(root, speccatalog.New(root), want, Options{
		CampaignID: "saturation-sweep",
		Runtime:    "native",
		Subject:    "postgres-17 default",
		Now:        func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) },
		RunSeries: func(_ string, _ speccatalog.Catalog, plan benchmarkplan.Plan, options benchmarkrun.Options) (benchmarkrun.Series, error) {
			calls++
			content, readErr := os.ReadFile(filepath.Join(root, "runs", "benchmark-campaign", "saturation-sweep", "protocol.json"))
			if readErr != nil {
				t.Fatalf("protocol was not persisted before callback %d: %v", calls, readErr)
			}
			var protocol Protocol
			if decodeErr := json.Unmarshal(content, &protocol); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if len(protocol.OrderedSeries) != 2 || protocol.OrderedSeries[calls-1].Benchmark != plan.Spec {
				t.Fatalf("callback observed a partial or reordered protocol: %#v plan=%#v", protocol, plan)
			}
			if options.RunID != fmt.Sprintf("saturation-sweep-s%03d", calls) || options.Runtime != "native" || options.Subject != "postgres-17 default" {
				t.Fatalf("campaign did not own series options: %#v", options)
			}
			return benchmarkrun.Series{}, errors.New("synthetic unavailable series")
		},
	})
	if err == nil {
		t.Fatal("failed campaign unexpectedly returned nil error")
	}
	if calls != 2 || result.Status != "failed" || result.Conclusion != "descriptive" || result.Decision != "none" || len(result.Executions) != 2 {
		t.Fatalf("campaign did not retain its complete failure set: calls=%d result=%#v", calls, result)
	}
	for index, execution := range result.Executions {
		if execution.Position != index+1 || execution.Benchmark != want[index] || execution.Status != "unavailable" || execution.EvidenceStatus != "unverified" || execution.Median != nil || execution.SeriesRef != "" {
			t.Fatalf("unexpected retained execution %d: %#v", index+1, execution)
		}
	}
	verification, verifyErr := Verify(root, result.ArtifactDir)
	if verifyErr != nil || !verification.IsValid() {
		t.Fatalf("honest unavailable campaign did not verify: err=%v issues=%v", verifyErr, verification.Issues)
	}
	serialized, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, forbidden := range []string{"aggregate_score", "composite_score", "winner", "change_pct", "causal"} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("campaign crossed descriptive boundary with %q: %s", forbidden, serialized)
		}
	}
}

func TestSequentialCampaignChronologyRejectsOverlappingVerifiedSeries(t *testing.T) {
	verification := VerifyResult{Issues: []string{}}
	checkSequentialExecutionChronology(&verification, []Execution{
		{Position: 1, EvidenceStatus: "verified", StartedAt: "2026-08-12T00:00:00Z", FinishedAt: "2026-08-12T00:02:00Z"},
		{Position: 2, EvidenceStatus: "verified", StartedAt: "2026-08-12T00:01:00Z", FinishedAt: "2026-08-12T00:03:00Z"},
	})
	if len(verification.Issues) != 1 || !strings.Contains(verification.Issues[0], "overlaps earlier verified series 1") {
		t.Fatalf("overlapping sequential series were accepted: %v", verification.Issues)
	}

	verification.Issues = nil
	checkSequentialExecutionChronology(&verification, []Execution{
		{Position: 1, EvidenceStatus: "verified", StartedAt: "2026-08-12T00:00:00Z", FinishedAt: "2026-08-12T00:01:00Z"},
		{Position: 2, EvidenceStatus: "unverified"},
		{Position: 3, EvidenceStatus: "verified", StartedAt: "2026-08-12T00:01:00Z", FinishedAt: "2026-08-12T00:02:00Z"},
	})
	if len(verification.Issues) != 0 {
		t.Fatalf("non-overlapping verified series were rejected: %v", verification.Issues)
	}
}

func TestBuildProtocolBindsOrderAndSafeDerivedIDs(t *testing.T) {
	root := writeCampaignCatalog(t)
	writeCampaignSpec(t, root, "z-last", 16, "builtin")
	writeCampaignSpec(t, root, "a-first", 1, "select-only")
	protocol, plans, err := BuildProtocol(speccatalog.New(root), "ordered", "docker", "default", []string{"z-last", "a-first", "z-last"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 || protocol.OrderedSeries[0].Benchmark != "z-last" || protocol.OrderedSeries[1].Benchmark != "a-first" || protocol.OrderedSeries[2].Benchmark != "z-last" {
		t.Fatalf("protocol reordered or deduplicated the declared sweep: %#v", protocol.OrderedSeries)
	}
	for index, item := range protocol.OrderedSeries {
		want := fmt.Sprintf("ordered-s%03d", index+1)
		if item.Position != index+1 || item.SeriesRunID != want {
			t.Fatalf("unsafe or unstable derived id at %d: %#v", index, item)
		}
	}
	if err := validateProtocol(protocol); err != nil {
		t.Fatal(err)
	}
	protocol.OrderedSeries[0], protocol.OrderedSeries[1] = protocol.OrderedSeries[1], protocol.OrderedSeries[0]
	if err := validateProtocol(protocol); err == nil {
		t.Fatal("tampered order passed protocol verification")
	}
	tooLong := strings.Repeat("a", 176)
	if _, _, err := BuildProtocol(speccatalog.New(root), tooLong, "docker", "default", []string{"a-first"}); err == nil {
		t.Fatal("campaign id producing an oversized series id was accepted")
	}
}

func TestRunRefusesAnyPreexistingCampaignSeriesPathBeforeExecution(t *testing.T) {
	root := writeCampaignCatalog(t)
	writeCampaignSpec(t, root, "one", 1, "builtin")
	writeCampaignSpec(t, root, "two", 2, "builtin")
	if err := os.MkdirAll(filepath.Join(root, "runs", "benchmarks", "collision-s002"), 0o755); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := Run(root, speccatalog.New(root), []string{"one", "two"}, Options{
		CampaignID: "collision",
		Runtime:    "docker",
		RunSeries: func(string, speccatalog.Catalog, benchmarkplan.Plan, benchmarkrun.Options) (benchmarkrun.Series, error) {
			called = true
			return benchmarkrun.Series{}, nil
		},
	})
	if err == nil || called {
		t.Fatalf("preflight collision did not fail before execution: called=%v err=%v", called, err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "runs", "benchmark-campaign", "collision")); !os.IsNotExist(statErr) {
		t.Fatalf("campaign path was created despite a later series collision: %v", statErr)
	}
}

func TestInitializeCampaignDirFailureLeavesNoFinalOrStagingDirectory(t *testing.T) {
	finalDir := filepath.Join(t.TempDir(), "runs", "benchmark-campaign", "atomic-init-failure")
	syntheticErr := errors.New("synthetic campaign initialization failure")
	err := initializeCampaignDir(finalDir, func(stagingDir string) error {
		if err := os.Mkdir(filepath.Join(stagingDir, "executions"), 0o755); err != nil {
			return err
		}
		if err := writeJSONExclusive(filepath.Join(stagingDir, "protocol.json"), map[string]string{"state": "partial"}); err != nil {
			return err
		}
		return syntheticErr
	})
	if !errors.Is(err, syntheticErr) {
		t.Fatalf("synthetic campaign initialization failure was not returned: %v", err)
	}
	if _, statErr := os.Lstat(finalDir); !os.IsNotExist(statErr) {
		t.Fatalf("partial final campaign directory survived initialization failure: %v", statErr)
	}
	staging, globErr := filepath.Glob(filepath.Join(filepath.Dir(finalDir), ".atomic-init-failure.staging-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(staging) != 0 {
		t.Fatalf("campaign staging debris survived initialization failure: %v", staging)
	}
}

func TestCampaignBundleIsDeterministicRelocatableAndTransitivelyComplete(t *testing.T) {
	root, campaign := createVerifiedCampaign(t)
	firstOutput := filepath.Join(t.TempDir(), "first.tar.gz")
	secondOutput := filepath.Join(t.TempDir(), "second.tar.gz")
	epoch := time.Unix(0, 0).UTC()
	first, err := CreateBundle(root, campaign.ArtifactDir, firstOutput, epoch)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateBundle(root, campaign.ArtifactDir, secondOutput, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if first.Series != 2 || first.LinkedRuns != 4 || first.Digest != second.Digest {
		t.Fatalf("bundle closure or digest is unstable: first=%#v second=%#v", first, second)
	}
	left, err := os.ReadFile(firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("identical campaign bundles differ byte-for-byte")
	}

	extracted := filepath.Join(t.TempDir(), "relocated")
	rootName := extractCampaignArchive(t, firstOutput, extracted)
	relocatedRoot := filepath.Join(extracted, rootName)
	relocatedCampaign := filepath.Join(relocatedRoot, "runs", "benchmark-campaign", campaign.CampaignID)
	verification, verifyErr := VerifyBundle(relocatedRoot, relocatedCampaign)
	if verifyErr != nil || !verification.IsValid() {
		t.Fatalf("relocated campaign bundle did not verify: err=%v issues=%v", verifyErr, verification.Issues)
	}
	for _, reference := range []string{
		"runs/benchmarks/portable-s001/result.json",
		"runs/benchmarks/portable-s002/result.json",
		"runs/portable-s001-t001/verdict.json",
		"runs/portable-s002-t002/verdict.json",
	} {
		if info, statErr := os.Stat(filepath.Join(relocatedRoot, filepath.FromSlash(reference))); statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("transitive closure is missing %s: %v", reference, statErr)
		}
	}
}

func TestCreateCampaignBundleRejectsTamperedStageBeforeArchive(t *testing.T) {
	root, campaign := createVerifiedCampaign(t)
	output := filepath.Join(t.TempDir(), "tampered-stage.tar.gz")
	_, err := createBundle(root, campaign.ArtifactDir, output, time.Unix(0, 0).UTC(), func(stage string) error {
		appendCampaignFile(t, filepath.Join(stage, "runs", "benchmark-campaign", campaign.CampaignID, "result.json"), " ")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "staged benchmark campaign bundle is invalid") {
		t.Fatalf("tampered staged campaign bundle was published: %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive exists after staged verification failure: %v", statErr)
	}
}

func TestCreateCampaignBundleCannotOverwriteImmutableCampaignThroughSymlinkAncestor(t *testing.T) {
	root, campaign := createVerifiedCampaign(t)
	protected := map[string][]byte{}
	for _, name := range []string{"protocol.json", "result.json"} {
		path := filepath.Join(campaign.ArtifactDir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		protected[path] = content
	}
	reject := func(output string) {
		t.Helper()
		if _, err := CreateBundle(root, campaign.ArtifactDir, output, time.Unix(0, 0).UTC()); err == nil || !strings.Contains(err.Error(), "outside the immutable campaign") {
			t.Fatalf("bundle output %s could overwrite immutable campaign evidence: %v", output, err)
		}
	}

	reject(filepath.Join(campaign.ArtifactDir, "result.json"))
	reject(filepath.Join(campaign.ArtifactDir, "protocol.json"))

	alias := filepath.Join(t.TempDir(), "campaign-alias")
	if err := os.Symlink(campaign.ArtifactDir, alias); err != nil {
		t.Fatalf("create campaign source alias: %v", err)
	}
	reject(filepath.Join(alias, "result.json"))
	reject(filepath.Join(alias, "new.tar.gz"))

	siblingOutput := filepath.Join(filepath.Dir(campaign.ArtifactDir), campaign.CampaignID+".tar.gz")
	if _, err := CreateBundle(root, campaign.ArtifactDir, siblingOutput, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("normal sibling campaign bundle output was rejected: %v", err)
	}
	if info, err := os.Stat(siblingOutput); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("normal sibling campaign bundle was not created: %v", err)
	}

	for path, want := range protected {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("rejected bundle output changed immutable campaign file %s", path)
		}
	}
	verification, err := Verify(root, campaign.ArtifactDir)
	if err != nil || !verification.IsValid() {
		t.Fatalf("rejected outputs damaged immutable campaign: verification=%#v err=%v", verification, err)
	}
}

func TestCampaignBundleTamperMatrix(t *testing.T) {
	root, campaign := createVerifiedCampaign(t)
	archive := filepath.Join(t.TempDir(), "campaign.tar.gz")
	if _, err := CreateBundle(root, campaign.ArtifactDir, archive, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	baseParent := filepath.Join(t.TempDir(), "base")
	rootName := extractCampaignArchive(t, archive, baseParent)
	base := filepath.Join(baseParent, rootName)

	tests := []struct {
		name   string
		mutate func(t *testing.T, artifactRoot string)
	}{
		{"campaign-result", func(t *testing.T, artifactRoot string) {
			appendCampaignFile(t, filepath.Join(artifactRoot, "runs", "benchmark-campaign", campaign.CampaignID, "result.json"), " ")
		}},
		{"linked-series", func(t *testing.T, artifactRoot string) {
			appendCampaignFile(t, filepath.Join(artifactRoot, "runs", "benchmarks", "portable-s001", "result.json"), " ")
		}},
		{"linked-trial-run", func(t *testing.T, artifactRoot string) {
			appendCampaignFile(t, filepath.Join(artifactRoot, "runs", "portable-s002-t002", "metrics.csv"), "\n")
		}},
		{"extra-file", func(t *testing.T, artifactRoot string) {
			writeCampaignFile(t, filepath.Join(artifactRoot, "unexpected.txt"), "extra\n")
		}},
		{"missing-file", func(t *testing.T, artifactRoot string) {
			if err := os.Remove(filepath.Join(artifactRoot, "runs", "benchmarks", "portable-s001", "summary.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"inventory-digest", func(t *testing.T, artifactRoot string) {
			inventory := readCampaignInventory(t, artifactRoot)
			inventory.Files[0].Digest = "sha256:" + strings.Repeat("0", 64)
			writeCampaignJSON(t, filepath.Join(artifactRoot, BundleInventoryName), inventory)
		}},
		{"inventory-unsorted", func(t *testing.T, artifactRoot string) {
			inventory := readCampaignInventory(t, artifactRoot)
			inventory.Files[0], inventory.Files[len(inventory.Files)-1] = inventory.Files[len(inventory.Files)-1], inventory.Files[0]
			writeCampaignJSON(t, filepath.Join(artifactRoot, BundleInventoryName), inventory)
		}},
		{"inventory-duplicate", func(t *testing.T, artifactRoot string) {
			inventory := readCampaignInventory(t, artifactRoot)
			inventory.Files = append(inventory.Files, inventory.Files[0])
			writeCampaignJSON(t, filepath.Join(artifactRoot, BundleInventoryName), inventory)
		}},
		{"inventory-traversal", func(t *testing.T, artifactRoot string) {
			inventory := readCampaignInventory(t, artifactRoot)
			inventory.Files[0].Path = "../escape"
			writeCampaignJSON(t, filepath.Join(artifactRoot, BundleInventoryName), inventory)
		}},
		{"inventory-negative-size", func(t *testing.T, artifactRoot string) {
			inventory := readCampaignInventory(t, artifactRoot)
			inventory.Files[0].Size = -1
			writeCampaignJSON(t, filepath.Join(artifactRoot, BundleInventoryName), inventory)
		}},
		{"series-order", func(t *testing.T, artifactRoot string) {
			inventory := readCampaignInventory(t, artifactRoot)
			inventory.SeriesRefs[0], inventory.SeriesRefs[1] = inventory.SeriesRefs[1], inventory.SeriesRefs[0]
			writeCampaignJSON(t, filepath.Join(artifactRoot, BundleInventoryName), inventory)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifactRoot := filepath.Join(t.TempDir(), "artifact")
			copyCampaignTree(t, base, artifactRoot)
			test.mutate(t, artifactRoot)
			checked, err := VerifyBundle(artifactRoot, filepath.Join(artifactRoot, "runs", "benchmark-campaign", campaign.CampaignID))
			if err != nil {
				t.Fatal(err)
			}
			if checked.IsValid() {
				t.Fatalf("tampered bundle %s verified", test.name)
			}
		})
	}
}

func TestCampaignBundleRejectsSymlinkInSource(t *testing.T) {
	root, campaign := createVerifiedCampaign(t)
	link := filepath.Join(campaign.ArtifactDir, "unsafe-link")
	if err := os.Symlink("result.json", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := CreateBundle(root, campaign.ArtifactDir, filepath.Join(t.TempDir(), "bad.tar.gz"), time.Unix(0, 0).UTC()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("bundle accepted a symlink source: %v", err)
	}
}

func createVerifiedCampaign(t *testing.T) (string, Result) {
	t.Helper()
	root := writeCampaignCatalog(t)
	writeCampaignSpec(t, root, "clients-1", 1, "select-only")
	writeCampaignSpec(t, root, "clients-8", 8, "builtin")
	base := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	tick := 0
	clock := func() time.Time {
		value := base.Add(time.Duration(tick) * time.Minute)
		tick++
		return value
	}
	result, err := Run(root, speccatalog.New(root), []string{"clients-1", "clients-8"}, Options{
		CampaignID: "portable",
		Runtime:    "docker",
		Subject:    "default",
		Now:        clock,
		SeriesOptions: benchmarkrun.Options{
			EngineVersion: "0.3.0",
			EngineCommit:  strings.Repeat("a", 40),
			Getenv:        func(string) string { return "" },
			RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
				started := options.Now().UTC()
				finished := started.Add(30 * time.Second)
				writeCampaignLinkedRun(t, root, options, started, finished)
				completeCampaignPhaseJournal(t, options.Env, started, finished)
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
		},
	})
	if err != nil {
		t.Fatalf("create verified campaign: %v result=%#v", err, result)
	}
	if result.Status != "passed" || len(result.Executions) != 2 || result.Executions[0].Median == nil || result.Executions[1].Median == nil {
		t.Fatalf("verified campaign has incomplete descriptive rows: %#v", result)
	}
	verification, verifyErr := Verify(root, result.ArtifactDir)
	if verifyErr != nil || !verification.IsValid() {
		t.Fatalf("campaign fixture is invalid: err=%v issues=%v", verifyErr, verification.Issues)
	}
	return root, result
}

func writeCampaignLinkedRun(t *testing.T, root string, options experimentrun.Options, started, finished time.Time) {
	t.Helper()
	values := campaignEnv(options.Env)
	clients, err := strconv.Atoi(values["PGBENCH_CLIENTS"])
	if err != nil {
		t.Fatal(err)
	}
	mode := values["PGBENCH_MODE"]
	transactionType := "<builtin: TPC-B (sort of)>"
	if mode == "select-only" {
		transactionType = "<builtin: select only>"
	}
	seriesBoost := 100.0
	if strings.Contains(options.RunID, "-s002-") {
		seriesBoost = 200
	}
	trialBoost := 0.0
	if strings.HasSuffix(options.RunID, "t002") {
		trialBoost = 2
	}
	tps := seriesBoost + trialBoost
	processed := int64(tps*30 + 0.5)
	latency := 1000 / tps
	runDir := filepath.Join(root, "runs", options.RunID)
	experimentPath := filepath.Join(root, "experiments", "benchmarks", "pgbench.env")
	experimentDigest, err := evidence.DigestFile(experimentPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := runstate.Manifest{
		RunID:                options.RunID,
		StartedAt:            started.Format(time.RFC3339Nano),
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
		RuntimeFingerprintAt:     started.Format(time.RFC3339Nano),
		ExperimentName:           "Synthetic campaign benchmark trial",
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
	summary := fmt.Sprintf(strings.Join([]string{
		"pgbench (17.9, server 17.9)",
		"transaction type: %s",
		"scaling factor: 1",
		"query mode: simple",
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
	}, "\n")+"\n", transactionType, clients, processed, latency, tps)
	writeCampaignFile(t, filepath.Join(runDir, "driver", "pgbench-summary.log"), summary)
	writeCampaignFile(t, filepath.Join(runDir, "stdout.log"), "pgworkbench_benchmark_target=direct-postgres endpoint_contract=pgworkbench.pgbench-target/direct-postgres/v1 driver_service=postgres endpoint_host=127.0.0.1 endpoint_port=5432 driver_image_id=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd target_image_id=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\n")
	metricTimes := make([]string, 32)
	for index := range metricTimes {
		metricTimes[index] = started.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
	}
	writeCampaignFile(t, filepath.Join(runDir, "metrics.csv"), string(metricstest.CSV(metricTimes, "postgres")))
	verdict := runstate.Verdict{
		RunID:            options.RunID,
		Status:           runstate.VerdictStatusPassed,
		Message:          "synthetic campaign benchmark trial passed",
		StartedAt:        started.Format(time.RFC3339Nano),
		FinishedAt:       finished.Format(time.RFC3339Nano),
		ExperimentSpecID: "benchmarks/pgbench",
	}
	if err := runstate.WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}
}

func completeCampaignPhaseJournal(t *testing.T, env []string, started, finished time.Time) {
	t.Helper()
	values := campaignEnv(env)
	path := values["PGWORKBENCH_BENCHMARK_PHASE_FILE"]
	if path == "" {
		t.Fatal("benchmark phase journal environment is missing")
	}
	prefix := values["PGWORKBENCH_BENCHMARK_RUN_ID"] + "\t" + values["PGWORKBENCH_BENCHMARK_TRIAL"] + "\t"
	startText, finishText := started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano)
	rows := strings.Join([]string{
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
		writeCampaignFile(t, target, rows)
	}
}

func campaignEnv(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if ok {
			result[key] = item
		}
	}
	return result
}

func extractCampaignArchive(t *testing.T, archivePath, destination string) string {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	reader := tar.NewReader(gzipReader)
	rootName := ""
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("unsafe archive path: %s", header.Name)
		}
		parts := strings.Split(filepath.ToSlash(clean), "/")
		if rootName == "" {
			rootName = parts[0]
		} else if parts[0] != rootName {
			t.Fatalf("archive has multiple roots: %s and %s", rootName, parts[0])
		}
		target := filepath.Join(destination, clean)
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
				t.Fatalf("extract archive: copy=%v close=%v", copyErr, closeErr)
			}
		default:
			t.Fatalf("unsupported archive entry: %s type=%d", header.Name, header.Typeflag)
		}
	}
	if closeErr := gzipReader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	return rootName
}

func copyCampaignTree(t *testing.T, source, destination string) {
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

func appendCampaignFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(content)
	if closeErr := file.Close(); writeErr != nil || closeErr != nil {
		t.Fatalf("append file: write=%v close=%v", writeErr, closeErr)
	}
}

func readCampaignInventory(t *testing.T, artifactRoot string) BundleInventory {
	t.Helper()
	var inventory BundleInventory
	content, err := os.ReadFile(filepath.Join(artifactRoot, BundleInventoryName))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func writeCampaignJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCampaignCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCampaignFile(t, filepath.Join(root, "configs", "default", "postgresql.conf"), "shared_buffers = '128MB'\n")
	writeCampaignFile(t, filepath.Join(root, "topologies", "single.env"), "TOPOLOGY_NAME=single\nTOPOLOGY_SERVICES=postgres\n")
	writeCampaignFile(t, filepath.Join(root, "experiments", "benchmarks", "pgbench.env"), strings.Join([]string{
		"EXPERIMENT_NAME=Campaign benchmark driver",
		"EXPERIMENT_TOPOLOGY=single",
		"EXPERIMENT_PG_CONFIG=default",
	}, "\n")+"\n")
	for _, workload := range []struct{ id, mode string }{{"builtin", "builtin"}, {"select-only", "select-only"}} {
		writeCampaignFile(t, filepath.Join(root, "workloads", "pgbench", workload.id+".env"), strings.Join([]string{
			"WORKLOAD_NAME=Campaign " + workload.id,
			"WORKLOAD_KIND=pgbench",
			"PGBENCH_MODE=" + workload.mode,
		}, "\n")+"\n")
	}
	return root
}

func writeCampaignSpec(t *testing.T, root, id string, clients int, workload string) {
	t.Helper()
	lines := []string{
		"BENCHMARK_NAME=Campaign " + id,
		"BENCHMARK_CLASS=smoke",
		"BENCHMARK_DRIVER=pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/" + workload,
		"BENCHMARK_PG_CONFIG=default",
		"BENCHMARK_MODE=fixed-time",
		"BENCHMARK_SCALE=1",
		"BENCHMARK_CLIENTS=" + campaignItoa(clients),
		"BENCHMARK_THREADS=1",
		"BENCHMARK_WARMUP_SECONDS=0",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_TRIALS=2",
		"BENCHMARK_MIN_VALID_TRIALS=2",
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
		"BENCHMARK_PROTOCOL=simple",
		"BENCHMARK_CONNECT_PER_TRANSACTION=0",
		"BENCHMARK_LOG_TRANSACTIONS=0",
	}
	writeCampaignFile(t, filepath.Join(root, "benchmarks", id+".env"), strings.Join(lines, "\n")+"\n")
}

func campaignItoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var reversed [32]byte
	index := len(reversed)
	for value > 0 {
		index--
		reversed[index] = digits[value%10]
		value /= 10
	}
	return string(reversed[index:])
}

func writeCampaignFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
