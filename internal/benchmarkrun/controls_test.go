package benchmarkrun

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestMaterializeBenchmarkControlsBindsRawEvidenceToTrial(t *testing.T) {
	runID := "control-materialize-t001"
	runDir := controlRunFixture(t, runID, map[string]string{
		benchmarkcontrol.CacheStateSourceFile:        "relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\n",
		benchmarkcontrol.StatisticsResetSourceFile:   "record\tscope\tvalue\trows\tcommand_completed\n",
		benchmarkcontrol.CollectorOverheadSourceFile: "sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus\n",
		benchmarkcontrol.ResourceBudgetSourceFile:    "{\n  \"mode\": \"unbounded\"\n}\n",
	})
	plan := uncontrolledV2Plan()
	timeline := controlTimeline(t, runID)

	controls, err := MaterializeControlsV2(runDir, plan, runID, 1, timeline, "17")
	if err != nil {
		t.Fatal(err)
	}
	if controls == nil {
		t.Fatal("protocol v2 did not retain normalized controls")
	}
	wantPrefix := "artifacts/benchmark/controls/"
	for label, ref := range map[string]ArtifactRef{
		"cache": controls.CacheState, "reset": controls.StatisticsReset,
		"overhead": controls.CollectorOverhead, "resource": controls.ResourceBudget,
	} {
		if !strings.HasPrefix(ref.Path, wantPrefix) || ref.Digest == "" || ref.Size <= 0 {
			t.Fatalf("%s control reference is not canonical: %#v", label, ref)
		}
	}
	controlDir := filepath.Join(runDir, "artifacts", "benchmark", "controls")
	for name, verify := range map[string]func(string) error{
		benchmarkcontrol.CacheStateFile:        benchmarkcontrol.VerifyCacheStateFile,
		benchmarkcontrol.StatisticsResetFile:   benchmarkcontrol.VerifyStatisticsResetFile,
		benchmarkcontrol.CollectorOverheadFile: benchmarkcontrol.VerifyCollectorOverheadFile,
		benchmarkcontrol.ResourceBudgetFile:    benchmarkcontrol.VerifyResourceBudgetFile,
	} {
		if err := verify(filepath.Join(controlDir, name)); err != nil {
			t.Fatalf("verify %s: %v", name, err)
		}
	}
}

func TestMaterializeBenchmarkControlsRetainsUnsatisfiedTypedEvidence(t *testing.T) {
	runID := "control-unsatisfied-t001"
	runDir := controlRunFixture(t, runID, map[string]string{
		benchmarkcontrol.CacheStateSourceFile:        "relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\npublic.accounts\t16384\t16390\tmain\t100\t10\n",
		benchmarkcontrol.StatisticsResetSourceFile:   "record\tscope\tvalue\trows\tcommand_completed\n",
		benchmarkcontrol.CollectorOverheadSourceFile: "sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus\n",
		benchmarkcontrol.ResourceBudgetSourceFile:    "{\n  \"mode\": \"unbounded\"\n}\n",
	})
	plan := uncontrolledV2Plan()
	plan.CacheRegime = benchmarkcontrol.CacheModeWarm
	plan.CacheTargetRelations = []string{"public.accounts"}
	threshold := 80.0
	plan.CacheMinResidentPct = &threshold

	controls, err := MaterializeControlsV2(runDir, plan, runID, 1, controlTimeline(t, runID), "17")
	if err == nil || !strings.Contains(err.Error(), "cache control is unsatisfied") {
		t.Fatalf("materialize error = %v, want unsatisfied cache gate", err)
	}
	if controls == nil || controls.CacheState.Path == "" || controls.ResourceBudget.Path == "" {
		t.Fatalf("unsatisfied run lost normalized evidence: %#v", controls)
	}
	path := filepath.Join(runDir, filepath.FromSlash(controls.CacheState.Path))
	content, readErr := os.ReadFile(path)
	if readErr != nil || !strings.Contains(string(content), `"status": "unsatisfied"`) {
		t.Fatalf("unsatisfied cache artifact missing: read=%v content=%s", readErr, content)
	}
}

func TestMaterializeBenchmarkControlsRejectsMissingRawSource(t *testing.T) {
	runID := "control-missing-t001"
	runDir := controlRunFixture(t, runID, map[string]string{
		benchmarkcontrol.CacheStateSourceFile:      "relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\n",
		benchmarkcontrol.StatisticsResetSourceFile: "record\tscope\tvalue\trows\tcommand_completed\n",
	})
	controls, err := MaterializeControlsV2(runDir, uncontrolledV2Plan(), runID, 1, controlTimeline(t, runID), "17")
	if err == nil || controls != nil || !strings.Contains(err.Error(), "collector overhead raw source") {
		t.Fatalf("materialize controls=%#v err=%v, want missing raw source rejection", controls, err)
	}
}

func TestLoadBenchmarkControlsContractV1DoesNotRequireControls(t *testing.T) {
	controls, err := LoadControlsV2(t.TempDir(), benchmarkplan.Plan{}, "legacy-t001", 1, benchmarkphase.Timeline{}, "17")
	if err != nil || controls != nil {
		t.Fatalf("protocol v1 controls=%#v err=%v, want no-op", controls, err)
	}
}

func TestControlWindowsUseDedicatedPassedPhases(t *testing.T) {
	timeline := controlTimeline(t, "control-window-t001")
	cacheWindow, err := cacheControlWindow(timeline)
	if err != nil {
		t.Fatal(err)
	}
	preMeasure := timeline.Events[benchmarkphase.PreMeasureControlIndex]
	if cacheWindow.StartedAt != preMeasure.StartedAt || cacheWindow.FinishedAt != preMeasure.FinishedAt {
		t.Fatalf("cache window = %#v, want pre-measure control %#v", cacheWindow, preMeasure)
	}
	warmupReset, err := statisticsResetWindow("before-warmup", timeline)
	if err != nil {
		t.Fatal(err)
	}
	preWarmup := timeline.Events[benchmarkphase.PreWarmupControlIndex]
	if warmupReset.StartedAt != preWarmup.StartedAt || warmupReset.FinishedAt != preWarmup.FinishedAt {
		t.Fatalf("reset window = %#v, want pre-warmup control %#v", warmupReset, preWarmup)
	}

	timeline.Events[benchmarkphase.PreMeasureControlIndex].Status = "skipped"
	timeline.Events[benchmarkphase.PreMeasureControlIndex].Reason = "synthetic skip"
	if _, err := cacheControlWindow(timeline); err == nil || !strings.Contains(err.Error(), "requires a passed pre-measure-control phase") {
		t.Fatalf("skipped cache-control phase was accepted: %v", err)
	}
}

func TestMaterializeBenchmarkControlsRefusesExistingTypedArtifacts(t *testing.T) {
	runID := "control-no-overwrite-t001"
	runDir := controlRunFixture(t, runID, validUncontrolledControlSources())
	plan := uncontrolledV2Plan()
	timeline := controlTimeline(t, runID)
	if _, err := MaterializeControlsV2(runDir, plan, runID, 1, timeline, "17"); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeControlsV2(runDir, plan, runID, 1, timeline, "17"); err == nil || !strings.Contains(err.Error(), "publish benchmark control evidence") {
		t.Fatalf("second materialization error = %v, want no-replace rejection", err)
	}
}

func TestLoadBenchmarkControlsDoesNotMutateVerdictBearingRun(t *testing.T) {
	runID := "control-read-only-t001"
	runDir := controlRunFixture(t, runID, validUncontrolledControlSources())
	plan := uncontrolledV2Plan()
	timeline := controlTimeline(t, runID)
	if _, err := MaterializeControlsV2(runDir, plan, runID, 1, timeline, "17"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "verdict.env"), []byte("status=passed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotControlRun(t, runDir)
	controls, err := LoadControlsV2(runDir, plan, runID, 1, timeline, "17")
	if err != nil || controls == nil {
		t.Fatalf("load controls=%#v err=%v", controls, err)
	}
	after := snapshotControlRun(t, runDir)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("post-verdict control load mutated run\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestMaterializeControlsV2FromEnvironmentUsesImmutableCapsuleAndPrimaryJournal(t *testing.T) {
	root := controlMaterializerPack(t)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "pgbench/control-v2-env-test")
	if err != nil {
		t.Fatal(err)
	}
	seriesID := "control-env-series"
	runID := seriesID + "-t001"
	seriesDir := filepath.Join(root, "runs", "benchmarks", seriesID)
	capsuleRoot, err := snapshotProtocolInputs(root, seriesDir, plan)
	if err != nil {
		t.Fatal(err)
	}
	runDir := controlRunFixtureAt(t, filepath.Join(root, "runs", runID), validUncontrolledControlSources())
	timeline := controlTimeline(t, runID)
	journal := &strings.Builder{}
	for _, event := range timeline.Events {
		fmt.Fprintf(journal, "%s\t1\t%d\t%s\t%s\t%s\t%s\t%s\n", runID, event.Sequence, event.Name, event.Status, event.StartedAt, event.FinishedAt, event.Reason)
	}
	if err := os.WriteFile(filepath.Join(runDir, "artifacts", "benchmark", "phases.tsv"), []byte(journal.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]string{
		"run_id": runID, "runtime": "native", "postgres_server_major": "17",
		"source_spec_kind": "benchmark", "source_spec_id": plan.Spec,
		"source_spec_ref": filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env")), "source_spec_digest": plan.SpecDigest,
		"experiment_spec_id": plan.ExperimentSpec, "experiment_spec_ref": filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(plan.ExperimentSpec)+".env")), "experiment_spec_digest": plan.ExperimentDigest,
		"experiment_topology": "single", "experiment_pg_config": plan.PGConfig,
		"profile": "", "dataset_spec": "", "profile_size": "small", "workload_spec": plan.WorkloadSpec,
		"background_specs": "", "metrics_enabled": "1", "metrics_samples": "",
		"execution_parameters_digest": ExpectedExecutionParametersDigest(plan, "native", 1),
	}
	if err := writeControlManifest(filepath.Join(runDir, "manifest.env"), manifest); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"PGWORKBENCH_BENCHMARK_RUN_ID": runID, "PGWORKBENCH_BENCHMARK_SERIES_ID": seriesID,
		"PGWORKBENCH_BENCHMARK_TRIAL": "1", "PGWORKBENCH_BENCHMARK_CAPSULE_ROOT": capsuleRoot,
		"PGWORKBENCH_SOURCE_SPEC_KIND": "benchmark", "PGWORKBENCH_SOURCE_SPEC_ID": plan.Spec,
		"PGWORKBENCH_SOURCE_SPEC_REF": manifest["source_spec_ref"], "PGWORKBENCH_SOURCE_SPEC_DIGEST": plan.SpecDigest,
		"PGWORKBENCH_BENCHMARK_CONTRACT_VERSION": "2", "PGWORKBENCH_BENCHMARK_PROTOCOL_DIGEST": plan.ProtocolDigest,
		"PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_ID": plan.ExperimentSpec, "PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_DIGEST": plan.ExperimentDigest,
		"PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID": plan.WorkloadSpec, "PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_DIGEST": plan.WorkloadDigest,
		"PGWORKBENCH_BENCHMARK_PG_CONFIG_ID": plan.PGConfig, "PGWORKBENCH_BENCHMARK_PG_CONFIG_DIGEST": plan.PGConfigDigest,
		"PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_REF": plan.WorkloadScript, "PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_DIGEST": plan.WorkloadScriptDigest,
	}
	controls, err := MaterializeControlsV2FromEnvironment(root, runDir, func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if controls == nil || controls.CacheState.Path == "" || controls.ResourceBudget.Path == "" {
		t.Fatalf("materializer did not publish all typed controls: %#v", controls)
	}

	env["PGWORKBENCH_BENCHMARK_SERIES_ID"] = "different-series"
	if _, err := MaterializeControlsV2FromEnvironment(root, runDir, func(key string) string { return env[key] }); err == nil || !strings.Contains(err.Error(), "series and trial") {
		t.Fatalf("cross-series binding error = %v", err)
	}
}

func uncontrolledV2Plan() benchmarkplan.Plan {
	return benchmarkplan.Plan{
		ContractVersion: "2", ProtocolDigest: "sha256:" + strings.Repeat("a", 64),
		CacheRegime:           benchmarkcontrol.CacheModeUncontrolled,
		StatisticsResetPolicy: benchmarkcontrol.StatisticsPolicyNone, StatisticsResetBoundary: "none",
		CollectorIntervalSeconds: 1, CollectorOverheadMode: benchmarkcontrol.OverheadModeIncludedUnquantified,
		ResourceBudgetMode: benchmarkcontrol.ResourceModeUnbounded,
	}
}

func validUncontrolledControlSources() map[string]string {
	return map[string]string{
		benchmarkcontrol.CacheStateSourceFile:        "relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\n",
		benchmarkcontrol.StatisticsResetSourceFile:   "record\tscope\tvalue\trows\tcommand_completed\n",
		benchmarkcontrol.CollectorOverheadSourceFile: "sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus\n",
		benchmarkcontrol.ResourceBudgetSourceFile:    "{\n  \"mode\": \"unbounded\"\n}\n",
	}
}

func snapshotControlRun(t *testing.T, runDir string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	if err := filepath.WalkDir(runDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := fmt.Sprintf("mode=%s;size=%d;mtime=%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(";sha256=%x", sha256.Sum256(content))
		}
		snapshot[filepath.ToSlash(relative)] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func controlRunFixture(t *testing.T, runID string, sources map[string]string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, runID)
	return controlRunFixtureAt(t, runDir, sources)
}

func controlRunFixtureAt(t *testing.T, runDir string, sources map[string]string) string {
	t.Helper()
	controlDir := filepath.Join(runDir, "artifacts", "benchmark", "controls")
	if err := os.MkdirAll(controlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range sources {
		if err := os.WriteFile(filepath.Join(controlDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return runDir
}

func controlMaterializerPack(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"benchmarks/pgbench/control-v2-env-test.env": `BENCHMARK_CONTRACT_VERSION=2
BENCHMARK_NAME="control materializer test"
BENCHMARK_CLASS="smoke"
BENCHMARK_DRIVER="pgbench"
BENCHMARK_EXPERIMENT_SPEC="benchmarks/pgbench"
BENCHMARK_WORKLOAD_SPEC="pgbench/tiny"
BENCHMARK_PG_CONFIG="default"
BENCHMARK_MODE="fixed-time"
BENCHMARK_SCALE=1
BENCHMARK_CLIENTS=1
BENCHMARK_THREADS=1
BENCHMARK_WARMUP_SECONDS=0
BENCHMARK_MEASURE_SECONDS=1
BENCHMARK_TRIALS=1
BENCHMARK_MIN_VALID_TRIALS=1
BENCHMARK_RESET_POLICY="rebuild-per-trial"
BENCHMARK_CACHE_REGIME="uncontrolled"
BENCHMARK_STATISTICS_RESET_POLICY="none"
BENCHMARK_STATISTICS_RESET_BOUNDARY="none"
BENCHMARK_COLLECTORS="pgbench-driver postgresql-sampler-v2"
BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1
BENCHMARK_COLLECTOR_OVERHEAD_MODE="included-unquantified"
BENCHMARK_CLIENT_PLACEMENT="same-host"
BENCHMARK_RESOURCE_BUDGET_MODE="unbounded"
BENCHMARK_PRIMARY_METRIC="pgbench.tps"
BENCHMARK_DIRECTION="higher"
BENCHMARK_MAX_CV_PCT=100
BENCHMARK_PROTOCOL="simple"
BENCHMARK_LOG_TRANSACTIONS=0
BENCHMARK_LOG_SAMPLE_RATE=1
BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES="pg_config"
`,
		"experiments/benchmarks/pgbench.env": `EXPERIMENT_NAME="benchmark fixture"
EXPERIMENT_TOPOLOGY="single"
EXPERIMENT_PG_CONFIG="default"
EXPERIMENT_WORKLOAD_SPEC="pgbench/tiny"
EXPERIMENT_METRICS=1
`,
		"workloads/pgbench/tiny.env": `WORKLOAD_NAME="tiny fixture"
WORKLOAD_KIND="pgbench"
PGBENCH_MODE="builtin"
`,
		"configs/default/postgresql.conf": "# fixture\n",
		"topologies/single.env":           "TOPOLOGY_NAME=single\nTOPOLOGY_SERVICES=postgres\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeControlManifest(path string, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	content := &strings.Builder{}
	for _, key := range keys {
		fmt.Fprintf(content, "%s=%s\n", key, strconv.Quote(values[key]))
	}
	return os.WriteFile(path, []byte(content.String()), 0o600)
}

func controlTimeline(t *testing.T, runID string) benchmarkphase.Timeline {
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
