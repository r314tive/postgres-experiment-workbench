package benchmarkartifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
)

func TestCheckBenchmarkControlsReopensRawSourcesAndBindsPlan(t *testing.T) {
	runDir, plan, trial := writeControlVerificationFixture(t)
	result := VerifyResult{Issues: []string{}}
	checkBenchmarkControls(&result, runDir, 1, trial, &plan)
	if len(result.Issues) != 0 {
		t.Fatalf("valid controls were rejected: %v", result.Issues)
	}

	raw := filepath.Join(runDir, "artifacts", "benchmark", "controls", benchmarkcontrol.CacheStateSourceFile)
	if err := os.WriteFile(raw, []byte("relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\npublic.accounts\t1\t2\tmain\t10\t1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tampered := VerifyResult{Issues: []string{}}
	checkBenchmarkControls(&tampered, runDir, 1, trial, &plan)
	if !controlIssuesContain(tampered.Issues, "cache control cannot be independently verified from raw source") {
		t.Fatalf("raw-source tampering was not rejected: %v", tampered.Issues)
	}
}

func TestCheckBenchmarkControlsRejectsDeclarationDriftAndMissingPassedEvidence(t *testing.T) {
	runDir, plan, trial := writeControlVerificationFixture(t)
	drifted := plan
	drifted.CollectorIntervalSeconds = 2
	result := VerifyResult{Issues: []string{}}
	checkBenchmarkControls(&result, runDir, 1, trial, &drifted)
	if !controlIssuesContain(result.Issues, "collector-overhead control declarations do not match plan") {
		t.Fatalf("control/plan declaration drift was not rejected: %v", result.Issues)
	}

	trial.Controls = nil
	missing := VerifyResult{Issues: []string{}}
	checkBenchmarkControls(&missing, runDir, 1, trial, &plan)
	if !controlIssuesContain(missing.Issues, "has no normalized controls") {
		t.Fatalf("passed v2 trial without controls was not rejected: %v", missing.Issues)
	}
}

func TestCheckBenchmarkControlsRejectsSkippedPreMeasureControl(t *testing.T) {
	runDir, plan, trial := writeControlVerificationFixture(t)
	trial.PhaseTimeline.Events[benchmarkphase.PreMeasureControlIndex].Status = "skipped"
	trial.PhaseTimeline.Events[benchmarkphase.PreMeasureControlIndex].Reason = "synthetic skip"
	result := VerifyResult{Issues: []string{}}
	checkBenchmarkControls(&result, runDir, 1, trial, &plan)
	if !controlIssuesContain(result.Issues, "cache evidence requires a passed pre-measure-control phase") {
		t.Fatalf("skipped cache-control phase was not rejected: %v", result.Issues)
	}
}

func writeControlVerificationFixture(t *testing.T) (string, benchmarkplan.Plan, benchmarkrun.Trial) {
	t.Helper()
	runID := "verify-controls-t001"
	protocolDigest := "sha256:" + strings.Repeat("a", 64)
	runDir := filepath.Join(t.TempDir(), runID)
	controlDir := filepath.Join(runDir, "artifacts", "benchmark", "controls")
	if err := os.MkdirAll(controlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.env"), []byte("postgres_server_major=17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	timeline := controlVerificationTimeline(t, runID)
	measure := benchmarkcontrol.BoundaryWindow{StartedAt: timeline.Events[benchmarkphase.PreMeasureControlIndex].StartedAt, FinishedAt: timeline.Events[benchmarkphase.PreMeasureControlIndex].FinishedAt}
	prepare := benchmarkcontrol.BoundaryWindow{StartedAt: timeline.Events[benchmarkphase.PrepareIndex].StartedAt, FinishedAt: timeline.Events[benchmarkphase.PrepareIndex].FinishedAt}
	overheadWindow := benchmarkcontrol.BoundaryWindow{StartedAt: timeline.Events[benchmarkphase.StabilizeIndex].StartedAt, FinishedAt: timeline.Events[benchmarkphase.CooldownIndex].FinishedAt}

	cacheRaw := []byte("relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\n")
	resetRaw := []byte("record\tscope\tvalue\trows\tcommand_completed\n")
	overheadRaw := []byte("sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus\n")
	resourceRaw := []byte("{\n  \"mode\": \"unbounded\"\n}\n")
	for name, raw := range map[string][]byte{
		benchmarkcontrol.CacheStateSourceFile: cacheRaw, benchmarkcontrol.StatisticsResetSourceFile: resetRaw,
		benchmarkcontrol.CollectorOverheadSourceFile: overheadRaw, benchmarkcontrol.ResourceBudgetSourceFile: resourceRaw,
	} {
		if err := os.WriteFile(filepath.Join(controlDir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cache, err := benchmarkcontrol.NewCacheStateFromSource(benchmarkcontrol.CacheStateInput{
		RunID: runID, ProtocolDigest: protocolDigest, Trial: 1, CapturedAt: measure.FinishedAt,
		BoundaryWindow: measure, Mode: benchmarkcontrol.CacheModeUncontrolled,
	}, cacheRaw)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := benchmarkcontrol.NewStatisticsResetFromSource(benchmarkcontrol.StatisticsResetInput{
		RunID: runID, ProtocolDigest: protocolDigest, Trial: 1, CapturedAt: prepare.FinishedAt,
		PostgresServerMajor: "17", Policy: benchmarkcontrol.StatisticsPolicyNone, Boundary: "none", BoundaryWindow: prepare,
	}, resetRaw)
	if err != nil {
		t.Fatal(err)
	}
	overhead, err := benchmarkcontrol.NewCollectorOverheadFromSource(benchmarkcontrol.CollectorOverheadInput{
		RunID: runID, ProtocolDigest: protocolDigest, Trial: 1, CapturedAt: overheadWindow.FinishedAt,
		CalibrationWindow: overheadWindow, Mode: benchmarkcontrol.OverheadModeIncludedUnquantified, IntervalNS: int64(time.Second),
	}, overheadRaw)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := benchmarkcontrol.NewResourceBudgetFromSource(benchmarkcontrol.ResourceBudgetInput{
		RunID: runID, ProtocolDigest: protocolDigest, Trial: 1, CapturedAt: prepare.FinishedAt,
		EnforcementWindow: prepare, Mode: benchmarkcontrol.ResourceModeUnbounded,
	}, resourceRaw)
	if err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func(string) error{
		benchmarkcontrol.CacheStateFile:        func(path string) error { return benchmarkcontrol.WriteCacheState(path, cache) },
		benchmarkcontrol.StatisticsResetFile:   func(path string) error { return benchmarkcontrol.WriteStatisticsReset(path, reset) },
		benchmarkcontrol.CollectorOverheadFile: func(path string) error { return benchmarkcontrol.WriteCollectorOverhead(path, overhead) },
		benchmarkcontrol.ResourceBudgetFile:    func(path string) error { return benchmarkcontrol.WriteResourceBudget(path, resource) },
	} {
		if err := write(filepath.Join(controlDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	controls := &benchmarkrun.ControlEvidence{
		CacheState:        *artifactFixtureRef(t, runDir, filepath.Join(controlDir, benchmarkcontrol.CacheStateFile)),
		StatisticsReset:   *artifactFixtureRef(t, runDir, filepath.Join(controlDir, benchmarkcontrol.StatisticsResetFile)),
		CollectorOverhead: *artifactFixtureRef(t, runDir, filepath.Join(controlDir, benchmarkcontrol.CollectorOverheadFile)),
		ResourceBudget:    *artifactFixtureRef(t, runDir, filepath.Join(controlDir, benchmarkcontrol.ResourceBudgetFile)),
	}
	plan := benchmarkplan.Plan{
		ContractVersion: "2", ProtocolDigest: protocolDigest,
		CacheRegime:           benchmarkcontrol.CacheModeUncontrolled,
		StatisticsResetPolicy: benchmarkcontrol.StatisticsPolicyNone, StatisticsResetBoundary: "none",
		CollectorIntervalSeconds: 1, CollectorOverheadMode: benchmarkcontrol.OverheadModeIncludedUnquantified,
		ResourceBudgetMode: benchmarkcontrol.ResourceModeUnbounded,
	}
	trial := benchmarkrun.Trial{
		Trial: 1, RunID: runID, Status: "passed", ExperimentVerified: true,
		PhaseTimeline: &timeline, Controls: controls,
	}
	return runDir, plan, trial
}

func controlVerificationTimeline(t *testing.T, runID string) benchmarkphase.Timeline {
	t.Helper()
	base := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	events := make([]benchmarkphase.Event, len(benchmarkphase.OrderedNames))
	for index, name := range benchmarkphase.OrderedNames {
		started := base.Add(time.Duration(index) * time.Second)
		events[index] = benchmarkphase.Event{
			Sequence: index + 1, Name: name, Status: "passed",
			StartedAt: started.Format(time.RFC3339Nano), FinishedAt: started.Add(500 * time.Millisecond).Format(time.RFC3339Nano),
		}
	}
	timeline, err := benchmarkphase.BuildForRun(runID, 1, events)
	if err != nil {
		t.Fatal(err)
	}
	return timeline
}
