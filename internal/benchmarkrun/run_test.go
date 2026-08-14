package benchmarkrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	metricstest "github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics/testfixture"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchlog"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestValidRunIDRequiresLeadingASCIIAlphanumeric(t *testing.T) {
	for _, value := range []string{"benchmark", "Benchmark_17", "17-read.write", "a", strings.Repeat("a", 180)} {
		if !ValidRunID(value) {
			t.Errorf("ValidRunID(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", ".", "..", ".hidden", "-hidden", "_hidden", "éclair", "a/path", "a path", strings.Repeat("a", 181)} {
		if ValidRunID(value) {
			t.Errorf("ValidRunID(%q) = true, want false", value)
		}
	}
}

func TestStartRejectsNativeRunnerEnforcedV2BeforeReservingRunDirectory(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	plan.ProtocolSchemaVersion = benchmarkplan.ProtocolSchemaVersionV2
	plan.ContractVersion = "2"
	plan.ResourceBudgetMode = "runner-enforced"
	execution, err := Start(root, speccatalog.New(root), plan, Options{
		Runtime: "native",
		RunID:   "v2-must-fail-closed",
		Getenv:  func(string) string { return "" },
	})
	if err == nil || !strings.Contains(err.Error(), "require the Docker single-container adapter") {
		t.Fatalf("Start() error = %v, want fail-closed native resource-control error", err)
	}
	if execution != nil {
		t.Fatalf("Start() execution = %#v, want nil", execution)
	}
	if _, statErr := os.Stat(filepath.Join(root, "runs", "benchmarks", "v2-must-fail-closed")); !os.IsNotExist(statErr) {
		t.Fatalf("v2 rejection reserved a run directory: %v", statErr)
	}
}

func TestStartAcceptsExplicitUnboundedV2OnNative(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	path := filepath.Join(root, "benchmarks", "test.env")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := "BENCHMARK_CONTRACT_VERSION=2\n" + string(content)
	updated = strings.Replace(updated, "postgresql-sampler-v1", "postgresql-sampler-v2", 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := Start(root, speccatalog.New(root), plan, Options{
		Runtime: "native", RunID: "v2-unbounded-native", NativeBindir: fakeRunnerToolchain(t, "v2-unbounded"), Getenv: func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("Start() rejected supported native protocol v2: %v", err)
	}
	if execution == nil || execution.Plan().ContractVersion != "2" {
		t.Fatalf("Start() execution = %#v, want protocol v2", execution)
	}
}

func TestPgBouncerTargetRejectsNativeBeforeReservingEvidence(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	writeRunFile(t, root, "topologies/pgbouncer.env", "TOPOLOGY_NAME=pgbouncer\nTOPOLOGY_SERVICES='postgres pgbouncer'\n")
	writeRunFile(t, root, "experiments/benchmarks/pgbench-pgbouncer.env", strings.Join([]string{
		"EXPERIMENT_NAME=Benchmark through PgBouncer",
		"EXPERIMENT_TOPOLOGY=pgbouncer",
		"EXPERIMENT_PG_CONFIG=default",
		"EXPERIMENT_WORKLOAD_SPEC=pgbench/tiny",
	}, "\n")+"\n")
	path := filepath.Join(root, "benchmarks", "test.env")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = []byte(strings.Replace(string(content),
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench-pgbouncer\nBENCHMARK_TARGET=pgbouncer", 1))
	writeRunFile(t, root, "benchmarks/test.env", string(content))
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}

	execution, err := Start(root, speccatalog.New(root), plan, Options{
		Runtime: "native",
		RunID:   "native-proxy-must-fail",
		Getenv:  func(string) string { return "" },
	})
	if err == nil || !strings.Contains(err.Error(), "native benchmarks may target only") {
		t.Fatalf("Start() error = %v, want native target rejection", err)
	}
	if execution != nil {
		t.Fatalf("Start() execution = %#v, want nil", execution)
	}
	if _, statErr := os.Stat(filepath.Join(root, "runs", "benchmarks", "native-proxy-must-fail")); !os.IsNotExist(statErr) {
		t.Fatalf("native proxy rejection reserved evidence: %v", statErr)
	}

	parameters := trialParameters(plan, "docker", 1)
	for key, want := range map[string]string{
		"EXPERIMENT_TOPOLOGY":         "pgbouncer",
		"PGBENCH_TARGET":              "pgbouncer",
		"PGBOUNCER_HOST":              "127.0.0.1",
		"PGBOUNCER_PORT":              "56432",
		"PGBOUNCER_IMAGE":             "edoburu/pgbouncer:v1.25.1-p0",
		"PGBOUNCER_POOL_MODE":         "transaction",
		"PGBOUNCER_DEFAULT_POOL_SIZE": "10",
	} {
		if parameters[key] != want {
			t.Errorf("%s = %q, want %q", key, parameters[key], want)
		}
	}
}

func TestEvaluateSeriesRejectsFavorablePartialPrefix(t *testing.T) {
	plan := benchmarkplan.Plan{Class: "measurement", Trials: 10, MinValidTrials: 5, MaxCVPct: 100}
	series := Series{Trials: make([]Trial, 5)}
	for index := range series.Trials {
		value := float64(100 + index)
		series.Trials[index] = Trial{Trial: index + 1, Status: "passed", PrimaryValue: &value}
	}
	status, _, reasons, err := EvaluateSeries(plan, series)
	if err != nil {
		t.Fatal(err)
	}
	if status != "invalid" || !containsRunReason(reasons, "partial series cannot be performance evidence") {
		t.Fatalf("favorable partial prefix status=%q reasons=%v, want invalid optional-stopping rejection", status, reasons)
	}

	series.Trials[0].Status = "failed"
	series.Trials[0].PrimaryValue = nil
	status, _, _, err = EvaluateSeries(plan, series)
	if err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("partial failed execution status=%q, want failed evidence", status)
	}
}

func TestStartRejectsUnsupportedClientPlacementBeforeReservingRunDirectory(t *testing.T) {
	for _, runtimeName := range []string{"docker", "native"} {
		for _, placement := range []string{"separate-host", "remote-host"} {
			t.Run(runtimeName+"/"+placement, func(t *testing.T) {
				root := t.TempDir()
				runID := runtimeName + "-" + placement
				execution, err := Start(root, speccatalog.New(root), benchmarkplan.Plan{ClientPlacement: placement}, Options{
					Runtime: runtimeName,
					RunID:   runID,
					Getenv:  func(string) string { return "" },
				})
				if err == nil || !strings.Contains(err.Error(), "is unsupported") || !strings.Contains(err.Error(), "expected same-host") {
					t.Fatalf("Start() error = %v, want fail-closed client-placement error", err)
				}
				if execution != nil {
					t.Fatalf("Start() execution = %#v, want nil", execution)
				}
				if _, statErr := os.Stat(filepath.Join(root, "runs", "benchmarks", runID)); !os.IsNotExist(statErr) {
					t.Fatalf("placement rejection reserved a run directory: %v", statErr)
				}
			})
		}
	}
}

func TestStartRejectsProtocolSourceMutationBeforeReservingRunDirectory(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	writeRunFile(t, root, "workloads/pgbench/tiny.env", "WORKLOAD_NAME=mutated\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=builtin\n")
	execution, err := Start(root, speccatalog.New(root), plan, Options{Runtime: "native", RunID: "mutated-source", Getenv: func(string) string { return "" }})
	if err == nil || !strings.Contains(err.Error(), "digest changed after plan construction") {
		t.Fatalf("Start() error = %v, want immutable-input digest failure", err)
	}
	if execution != nil {
		t.Fatalf("Start() execution = %#v, want nil before reservation", execution)
	}
	if _, statErr := os.Stat(filepath.Join(root, "runs", "benchmarks", "mutated-source")); !os.IsNotExist(statErr) {
		t.Fatalf("source mutation reserved a run directory: %v", statErr)
	}
}

func TestStartRejectsProtocolSourceSymlinkBeforeReservingRunDirectory(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	workload := filepath.Join(root, "workloads", "pgbench", "tiny.env")
	target := filepath.Join(root, "workloads", "pgbench", "replacement.env")
	content, err := os.ReadFile(workload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(workload); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, workload); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	execution, err := Start(root, speccatalog.New(root), plan, Options{Runtime: "native", RunID: "symlink-source", Getenv: func(string) string { return "" }})
	if err == nil || !strings.Contains(err.Error(), "path contains a symlink") {
		t.Fatalf("Start() error = %v, want immutable-input symlink failure", err)
	}
	if execution != nil {
		t.Fatalf("Start() execution = %#v, want nil before reservation", execution)
	}
}

func TestStartRejectsSymlinkedRunsRootBeforeWritingOutside(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "runs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := Start(root, speccatalog.New(root), plan, Options{
		Runtime: "docker", RunID: "symlinked-runs-root", BinaryPath: fakeBenchmarkEngine(t, "symlink-guard"), Getenv: func(string) string { return "" },
	})
	if err == nil || !strings.Contains(err.Error(), "artifact directory path is unsafe") {
		t.Fatalf("Start() error = %v, want unsafe artifact-directory rejection", err)
	}
	if execution != nil {
		t.Fatalf("Start() execution = %#v, want nil", execution)
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target was modified before rejection: %v", entries)
	}
}

func TestStartSetupFailureLeavesNoFinalOrStagingSeries(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	runID := "atomic-setup-failure"
	execution, err := Start(root, speccatalog.New(root), plan, Options{
		Runtime: "docker", RunID: runID, BinaryPath: fakeBenchmarkEngine(t, "atomic"),
		Getenv: func(string) string { return "" },
		beforeInitialPublish: func(string) error {
			return fmt.Errorf("injected setup failure")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected setup failure") || execution != nil {
		t.Fatalf("injected initialization failure did not propagate atomically: execution=%#v err=%v", execution, err)
	}
	parent := filepath.Join(root, "runs", "benchmarks")
	if _, statErr := os.Lstat(filepath.Join(parent, runID)); !os.IsNotExist(statErr) {
		t.Fatalf("failed initialization published a final series: %v", statErr)
	}
	staging, globErr := filepath.Glob(filepath.Join(parent, "."+runID+".staging-*"))
	if globErr != nil || len(staging) != 0 {
		t.Fatalf("failed initialization left staging debris: paths=%v err=%v", staging, globErr)
	}
}

func TestEngineMutationDuringTrialInvalidatesAllEvidence(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	enginePath := fakeBenchmarkEngine(t, "before")
	series, runErr := Run(root, speccatalog.New(root), "test", Options{
		Runtime: "docker", RunID: "engine-mutation", BinaryPath: enginePath,
		Now: fixedRunClock, Getenv: func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 2, true)
			completeFakePhaseJournal(t, options.Env)
			if err := os.WriteFile(enginePath, []byte("#!/bin/sh\necho after\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if runErr == nil || series.Status != "invalid" || len(series.Trials) != 1 || series.Trials[0].Status != "invalid" {
		t.Fatalf("engine mutation did not invalidate retained evidence: err=%v series=%#v", runErr, series)
	}
	if series.TrialsValid != 0 || series.TrialsInvalid != 1 || !containsRunReason(series.Reasons, "benchmark engine changed during trial 1") {
		t.Fatalf("engine mutation counters or reason are incomplete: %#v", series)
	}
}

func TestConfiguredScenarioPackIsRetainedAndPersistentMutationInvalidatesSeries(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	scriptPath, pack := writeRunScenarioPack(t, root)

	series, runErr := Run(root, speccatalog.New(root), "test", Options{
		Runtime:      "native",
		RunID:        "scenario-pack-mutation",
		NativeBindir: fakeRunnerToolchain(t, "scenario-pack-mutation"),
		PackID:       pack.ID, PackVersion: pack.Version, PackDigest: pack.Digest,
		Now:    fixedRunClock,
		Getenv: func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			fakeEnv := append([]string(nil), options.Env...)
			fakeEnv = append(fakeEnv,
				"PGWORKBENCH_PACK_ID="+options.PackID,
				"PGWORKBENCH_PACK_VERSION="+options.PackVersion,
				"PGWORKBENCH_PACK_DIGEST="+options.PackDigest,
			)
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, fakeEnv, 100, 2, true)
			completeFakePhaseJournal(t, options.Env)
			if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n# persistent mutation\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if runErr == nil || series.Status != "invalid" || len(series.Trials) != 1 || series.Trials[0].Status != "invalid" {
		t.Fatalf("persistent scenario-pack mutation did not fail closed: err=%v series=%#v", runErr, series)
	}
	if !containsRunReason(series.Trials[0].Reasons, "scenario pack changed during trial 1") || series.TrialsValid != 0 || series.TrialsInvalid != 1 {
		t.Fatalf("scenario-pack mutation was not retained as invalid evidence: %#v", series)
	}
	if series.ScenarioPack == nil || series.ScenarioPack.ID != pack.ID || series.ScenarioPack.Version != pack.Version || series.ScenarioPack.Digest != pack.Digest || !evidence.IsDigest(series.ScenarioPack.InventoryDigest) {
		t.Fatalf("series does not bind the retained scenario-pack inventory: %#v", series.ScenarioPack)
	}
	inventoryPath := filepath.Join(series.ArtifactDir, filepath.FromSlash(ScenarioPackInventoryRef))
	if digest, err := evidence.DigestFile(inventoryPath); err != nil || digest != series.ScenarioPack.InventoryDigest {
		t.Fatalf("scenario-pack inventory file digest mismatch: got %q err=%v identity=%#v", digest, err, series.ScenarioPack)
	}
	var inventory ScenarioPackInventory
	content, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.ID != pack.ID || inventory.Version != pack.Version || inventory.Digest != pack.Digest || len(inventory.Files) != len(pack.Files) {
		t.Fatalf("retained scenario-pack inventory differs from the validated start snapshot: %#v", inventory)
	}
	if err := scenariopack.VerifyInventory(inventory.Manifest, inventory.Files, inventory.Digest); err != nil {
		t.Fatalf("retained scenario-pack inventory does not independently verify: %v", err)
	}
}

func TestFinishRevalidatesScenarioPackAfterLastTrial(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	scriptPath, pack := writeRunScenarioPack(t, root)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := Start(root, speccatalog.New(root), plan, Options{
		Runtime:      "native",
		RunID:        "scenario-pack-finalization-mutation",
		NativeBindir: fakeRunnerToolchain(t, "scenario-pack-finalization"),
		PackID:       pack.ID, PackVersion: pack.Version, PackDigest: pack.Digest,
		Now:    fixedRunClock,
		Getenv: func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			fakeEnv := append([]string(nil), options.Env...)
			fakeEnv = append(fakeEnv,
				"PGWORKBENCH_PACK_ID="+options.PackID,
				"PGWORKBENCH_PACK_VERSION="+options.PackVersion,
				"PGWORKBENCH_PACK_DIGEST="+options.PackDigest,
			)
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, fakeEnv, 100, 2, true)
			completeFakePhaseJournal(t, options.Env)
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	trial, err := execution.ExecuteTrial()
	if err != nil || trial.Status != "passed" {
		t.Fatalf("valid pack-bound trial failed before finalization mutation: trial=%#v err=%v", trial, err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n# mutation after the last trial\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	series, finishErr := execution.Finish()
	if finishErr == nil || series.Status != "invalid" || series.TrialsValid != 0 || series.TrialsInvalid != 1 || series.Trials[0].Status != "invalid" {
		t.Fatalf("finalization-time pack mutation did not invalidate the complete series: err=%v series=%#v", finishErr, series)
	}
	if !containsRunReason(series.Reasons, "scenario pack changed before series finalization") {
		t.Fatalf("finalization pack-drift reason is missing: %v", series.Reasons)
	}
}

func TestTrialEnvironmentShadowsAmbientExecutionParameters(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	parameters := trialParameters(plan, "native", 1)
	for _, key := range runstate.ExecutionParameterKeys() {
		if _, ok := parameters[key]; !ok {
			t.Errorf("canonical execution projection omits %s", key)
		}
	}
	env := trialEnv(filepath.Join(root, "runs", "hostile-env-t001"), plan, "native", 1)
	declared := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			declared[key] = value
		}
	}
	for _, key := range runstate.ExecutionParameterKeys() {
		if _, ok := declared[key]; !ok {
			t.Errorf("execution parameter %s is not explicitly shadowed", key)
		}
	}
	hostile := overlayEnv(env, func(string) string { return "hostile-host-value" })
	for _, key := range []string{
		"EXPERIMENT_PROFILE", "EXPERIMENT_DATASET_SPEC", "EXPERIMENT_BACKGROUND_SPECS",
		"EXPERIMENT_BEFORE_SQL_FILES", "EXPERIMENT_BEFORE_SQL", "EXPERIMENT_BEFORE_SHELL",
		"EXPERIMENT_AFTER_SQL_FILES", "EXPERIMENT_AFTER_SQL", "EXPERIMENT_AFTER_SHELL",
		"EXPERIMENT_ASSERT_SQL_FILES", "EXPERIMENT_ASSERT_SQL", "EXPERIMENT_ASSERT_TRUE_SQL",
		"EXPERIMENT_ASSERT_SHELL", "EXPERIMENT_CAPTURE_FILES", "EXPERIMENT_SCAN_PATHS",
		"EXPERIMENT_METRICS_SAMPLES", "PGBENCH_PROGRESS", "PGBENCH_EXTRA_ARGS", "METRICS_SAMPLES",
	} {
		if got := hostile(key); got != "" {
			t.Errorf("hostile %s leaked into benchmark protocol: %q", key, got)
		}
	}
	for key, want := range map[string]string{
		"EXPERIMENT_TOPOLOGY":           "single",
		"EXPERIMENT_PROFILE_SETUP":      "0",
		"EXPERIMENT_PROFILE_RUN":        "0",
		"EXPERIMENT_PG_CONFIG":          "default",
		"PG_CONFIG":                     "default",
		"PGWORKBENCH_RUNTIME":           "native",
		"PGWORKBENCH_EXECUTION_TIMEOUT": experimentrun.DefaultExecutionTimeout.String(),
		"PGWORKBENCH_CLEANUP_GRACE":     experimentrun.DefaultCleanupGrace.String(),
		"PGBOUNCER_HOST":                "127.0.0.1",
		"PGBOUNCER_PORT":                "56432",
	} {
		if got := hostile(key); got != want {
			t.Errorf("canonical %s = %q, want %q", key, got, want)
		}
	}
	if got := runstate.EffectiveParametersDigest(hostile); got != ExpectedExecutionParametersDigest(plan, "native", 1) {
		t.Fatalf("hostile environment changed execution digest: got %s want %s", got, ExpectedExecutionParametersDigest(plan, "native", 1))
	}
}

func TestNativeTrialUsesExactEnvironmentAndExplicitBoundEndpoint(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime: "native", RunID: "native-exact-endpoint", NativeBindir: fakeRunnerToolchain(t, "exact-endpoint"),
		PostgresHost: "127.0.0.1", PostgresPort: 59433, Now: fixedRunClock,
		Getenv: func(key string) string {
			return map[string]string{
				"POSTGRES_HOST": "hostile.example", "POSTGRES_PORT": "1",
				"WORKLOAD_COMMAND": "hostile-command", "BASH_ENV": "/tmp/hostile",
				"EXPERIMENT_BEFORE_SHELL": "hostile-hook", "COMPOSE": "hostile-compose",
			}[key]
		},
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			if !options.ExactEnvironment {
				t.Fatal("native benchmark trial did not request exact environment isolation")
			}
			for key, want := range map[string]string{
				"ENV_FILE": ".env.example", "POSTGRES_HOST": "127.0.0.1", "POSTGRES_PORT": "59433",
				"EXPERIMENT_BEFORE_SHELL": "", "PGWORKBENCH_RUNTIME": "native",
			} {
				if got := envValue(options.Env, key); got != want {
					t.Fatalf("exact native benchmark %s=%q, want %q", key, got, want)
				}
			}
			for _, key := range []string{"WORKLOAD_COMMAND", "BASH_ENV", "COMPOSE"} {
				if got := envValue(options.Env, key); got != "" {
					t.Fatalf("ambient %s leaked into runner-owned environment: %q", key, got)
				}
			}
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 2, true)
			completeFakePhaseJournal(t, options.Env)
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err != nil || !series.Passed() || series.Environment == nil {
		t.Fatalf("exact native benchmark failed: err=%v series=%#v", err, series)
	}
	if series.Environment.TargetEndpointHost != "127.0.0.1" || series.Environment.TargetEndpointPort != 59433 || !evidence.IsDigest(series.Environment.NativeToolchainDigest) {
		t.Fatalf("native environment omitted endpoint/toolchain binding: %#v", series.Environment)
	}
}

func TestValidateTransactionLogUsesBoundedPgbenchLatencyWindow(t *testing.T) {
	summary := parseLatencySummary(t, 0.353, false)
	transactionLog := pgbenchlog.Result{
		SampleRate: 1,
		Completed:  summary.TransactionsProcessed,
		LatencyUS: &pgbenchlog.Distribution{
			N:    summary.TransactionsProcessed,
			Mean: 350.196,
		},
	}
	if err := ValidateTransactionLog(summary, transactionLog); err != nil {
		t.Fatalf("real short-run pgbench boundary skew must remain valid: %v", err)
	}
	transactionLog.LatencyUS.Mean = 515.052
	summary = parseLatencySummary(t, 0.523, false)
	if err := ValidateTransactionLog(summary, transactionLog); err != nil {
		t.Fatalf("observed global-window/client-interval skew must remain valid: %v", err)
	}
	transactionLog.LatencyUS.Mean = 390.826
	summary = parseLatencySummary(t, 0.404, false)
	if err := ValidateTransactionLog(summary, transactionLog); err != nil {
		t.Fatalf("second observed global-window/client-interval skew must remain valid: %v", err)
	}
	transactionLog.LatencyUS.Mean = 525
	summary = parseLatencySummary(t, 0.523, false)
	if err := ValidateTransactionLog(summary, transactionLog); err == nil {
		t.Fatal("raw mean above the global-window summary must be rejected")
	}
	transactionLog.LatencyUS.Mean = 330
	summary = parseLatencySummary(t, 0.353, false)
	if err := ValidateTransactionLog(summary, transactionLog); err != nil {
		t.Fatalf("a lower raw mean has no universal closed-loop gap bound: %v", err)
	}
	summary = parseLatencySummary(t, 0.353, true)
	transactionLog.LatencyUS.Mean = 352.6
	if err := ValidateTransactionLog(summary, transactionLog); err != nil {
		t.Fatalf("detailed summary must accept printed-precision rounding: %v", err)
	}
	transactionLog.LatencyUS.Mean = 351
	if err := ValidateTransactionLog(summary, transactionLog); err == nil {
		t.Fatal("detailed summary mismatch outside printed precision must be rejected")
	}
}

func parseLatencySummary(t *testing.T, latencyMS float64, detailed bool) pgbenchresult.Result {
	t.Helper()
	lines := []string{
		"pgbench (16.14, server 16.14)",
		"transaction type: <builtin: select only>",
		"scaling factor: 1",
		"query mode: simple",
		"number of clients: 1",
		"number of threads: 1",
		"maximum number of tries: 1",
		"duration: 1 s",
		"number of transactions actually processed: 10000",
		"number of failed transactions: 0 (0.000%)",
		fmt.Sprintf("latency average = %.3f ms", latencyMS),
	}
	if detailed {
		lines = append(lines, "latency stddev = 0.100 ms")
	}
	lines = append(lines, fmt.Sprintf("tps = %.6f (without initial connection time)", 1000/latencyMS))
	parsed, err := pgbenchresult.Parse(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestRunTwoTrialSmokeParsesVerifiesEnvironmentAndStats(t *testing.T) {
	root := writeRunCatalog(t, 2, true)
	runs, verifications := 0, 0

	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime: "docker",
		RunID:   "benchmark-two-trials",
		Now:     fixedRunClock,
		Getenv:  func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, input string, options experimentrun.Options) (experimentrun.Result, error) {
			runs++
			if input != "benchmarks/pgbench" {
				t.Fatalf("unexpected experiment input: %s", input)
			}
			if !options.ExactEnvironment {
				t.Fatal("benchmark trial did not request an exact child environment")
			}
			assertTrialEnvironment(t, options.Env, options.RunID, true)
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, float64(90+runs*10), 2, true)
			completeFakePhaseJournal(t, options.Env)
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			verifications++
			if _, err := os.Stat(filepath.Join(root, "runs", input, "manifest.env")); err != nil {
				t.Fatalf("verification called before fake run was written: %v", err)
			}
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err != nil {
		t.Fatalf("%v: %#v", err, series)
	}
	if !series.Passed() || series.TrialsValid != 2 || series.TrialsInvalid != 0 || series.TrialsFailed != 0 {
		t.Fatalf("unexpected series outcome: %#v", series)
	}
	if runs != 2 || verifications != 2 {
		t.Fatalf("unexpected callback counts: runs=%d verifications=%d", runs, verifications)
	}
	if series.Environment == nil || series.Environment.Runtime != "docker" || series.Environment.RuntimeOS != "linux" || series.Environment.PostgresServerMajor != "17" || series.Environment.Digest == "" {
		t.Fatalf("unexpected environment evidence: %#v", series.Environment)
	}
	if series.Stats == nil || series.Stats.N != 2 || series.Stats.Mean != 105 || series.Stats.Median != 105 || series.Stats.Min != 100 || series.Stats.Max != 110 {
		t.Fatalf("unexpected aggregate statistics: %#v", series.Stats)
	}
	for index, trial := range series.Trials {
		if trial.Status != "passed" || !trial.ExperimentVerified || trial.Pgbench == nil || trial.Pgbench.ParserVersion != pgbenchresult.ParserVersion {
			t.Fatalf("trial %d was not normalized and verified: %#v", index+1, trial)
		}
		if trial.Summary == nil || len(trial.RawLogs) != 1 || trial.EnvironmentDigest != series.Environment.Digest {
			t.Fatalf("trial %d evidence references are incomplete: %#v", index+1, trial)
		}
		if trial.PostgresMetrics == nil || trial.PostgresMetrics.Database.Name != "postgres" || trial.PostgresMetrics.Coverage.Samples != 32 {
			t.Fatalf("trial %d PostgreSQL sampler evidence is incomplete: %#v", index+1, trial.PostgresMetrics)
		}
		wantCompleted := int64((100 + index*10) * 30)
		if trial.TransactionLog == nil || trial.TransactionLog.ParserVersion != pgbenchlog.ParserVersion || trial.TransactionLog.Logged != wantCompleted || trial.TransactionLog.Completed != wantCompleted || trial.TransactionLog.Failed != 0 || trial.TransactionLog.Skipped != 0 || trial.TransactionLog.Retried != 0 {
			t.Fatalf("trial %d transaction log was not normalized: %#v", index+1, trial.TransactionLog)
		}
	}
}

func TestRunOneTrialSmokeDoesNotClaimSampleStats(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime:      "native",
		RunID:        "benchmark-one-trial",
		NativeBindir: fakeRunnerToolchain(t, "one-trial"),
		Now:          fixedRunClock,
		Getenv:       func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			if options.ExecutionTimeout != experimentrun.DefaultExecutionTimeout || options.CleanupGrace != experimentrun.DefaultCleanupGrace {
				t.Fatalf("benchmark timeout policy drifted: timeout=%s grace=%s", options.ExecutionTimeout, options.CleanupGrace)
			}
			assertTrialEnvironment(t, options.Env, options.RunID, true)
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 2, true)
			completeFakePhaseJournal(t, options.Env)
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err != nil {
		t.Fatalf("%v: %#v", err, series)
	}
	if !series.Passed() || series.TrialsValid != 1 {
		t.Fatalf("unexpected smoke outcome: %#v", series)
	}
	if series.Stats != nil {
		t.Fatalf("one-trial smoke must not claim sample statistics: %#v", series.Stats)
	}
}

func TestRunLeavesPreflightDecisionToShellRunner(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	base := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime:      "native",
		RunID:        "benchmark-subsecond-preflight",
		NativeBindir: fakeRunnerToolchain(t, "subsecond-preflight"),
		Now:          fixedRunClock,
		Getenv:       func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			phasePath := envValue(options.Env, "PGWORKBENCH_BENCHMARK_PHASE_FILE")
			info, statErr := os.Stat(phasePath)
			if statErr != nil || info.Size() != 0 {
				t.Fatalf("Go runner must hand an empty phase journal to shell: info=%v err=%v", info, statErr)
			}
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 2, true)
			completeFakePhaseJournalAt(t, options.Env, "", base.Add(111*time.Millisecond))
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight := series.Trials[0].PhaseTimeline.Events[benchmarkphase.PreflightIndex]
	if preflight.StartedAt != "2026-08-12T00:00:00.111Z" || preflight.FinishedAt != "2026-08-12T00:00:00.222Z" || preflight.DurationNS != 111_000_000 {
		t.Fatalf("preflight timestamp precision was lost: %#v", preflight)
	}
}

func TestRunRejectsPassedExperimentWithFailedPhaseTimeline(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime:      "native",
		RunID:        "benchmark-failed-phase",
		NativeBindir: fakeRunnerToolchain(t, "failed-phase"),
		Now:          fixedRunClock,
		Getenv:       func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 2, true)
			completeFakePhaseJournalWithFailure(t, options.Env, "cleanup")
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			t.Fatal("linked run verification must not run after a failed phase timeline")
			return runverify.Result{}, nil
		},
	})
	if err == nil || series.Status != "invalid" || len(series.Trials) != 1 {
		t.Fatalf("failed phase timeline did not invalidate the series: err=%v series=%#v", err, series)
	}
	trial := series.Trials[0]
	if trial.Status != "invalid" || trial.PhaseTimeline == nil || trial.PhaseTimeline.Status != "failed" || !containsRunReason(trial.Reasons, "benchmark phase timeline failed") {
		t.Fatalf("failed lifecycle evidence was not retained: %#v", trial)
	}
}

func TestRunMarksParsedProtocolMismatchInvalid(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime: "docker",
		RunID:   "benchmark-mismatched-clients",
		Now:     fixedRunClock,
		Getenv:  func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 3, true)
			completeFakePhaseJournal(t, options.Env)
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err == nil {
		t.Fatal("expected protocol mismatch to make the series invalid")
	}
	if series.Status != "invalid" || series.TrialsInvalid != 1 || series.TrialsValid != 0 || len(series.Trials) != 1 {
		t.Fatalf("unexpected mismatched series outcome: %#v", series)
	}
	if series.Trials[0].Status != "invalid" || !containsRunReason(series.Trials[0].Reasons, "pgbench clients mismatch: got 3 want 2") {
		t.Fatalf("protocol mismatch was not retained in trial evidence: %#v", series.Trials[0])
	}
	if series.Stats != nil {
		t.Fatalf("invalid trial must not contribute aggregate statistics: %#v", series.Stats)
	}
}

func TestRunRetainsInvalidAttemptButPassesPredeclaredMinimum(t *testing.T) {
	root := writeRunCatalog(t, 3, true)
	specPath := filepath.Join(root, "benchmarks", "test.env")
	content, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	writeRunFile(t, root, filepath.Join("benchmarks", "test.env"), strings.Replace(string(content), "BENCHMARK_MIN_VALID_TRIALS=3", "BENCHMARK_MIN_VALID_TRIALS=2", 1))
	call := 0
	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime: "docker",
		RunID:   "benchmark-minimum-valid",
		Now:     fixedRunClock,
		Getenv:  func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			call++
			clients := 2
			if call == 1 {
				clients = 3
			}
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, float64(90+call*10), clients, true)
			completeFakePhaseJournal(t, options.Env)
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err != nil {
		t.Fatalf("%v: %#v", err, series)
	}
	if series.Status != "passed" || series.TrialsValid != 2 || series.TrialsInvalid != 1 || series.Stats == nil || series.Stats.N != 2 {
		t.Fatalf("unexpected minimum-valid outcome: %#v", series)
	}
	if series.Trials[0].Status != "invalid" || !containsRunReason(series.Reasons, "retained and excluded") {
		t.Fatalf("invalid attempt was not retained transparently: %#v", series)
	}
}

func TestRunRejectsInvalidRawTransactionEvidence(t *testing.T) {
	tests := []struct {
		name           string
		specExtra      string
		raw            string
		patchSummary   bool
		wantReason     string
		wantNormalized bool
	}{
		{
			name:       "malformed",
			raw:        "0 1 truncated\n",
			wantReason: "raw transaction logs parse failed",
		},
		{
			name:           "failed",
			raw:            "0 1 failed 0 1786492800 780769\n1 1 200 0 1786492800 781111\n",
			wantReason:     "reported 1 failed transactions",
			wantNormalized: true,
		},
		{
			name:           "skipped",
			specExtra:      "BENCHMARK_RATE=10\n",
			raw:            "0 1 skipped 0 1786492800 780769 10\n1 1 200 0 1786492800 781111 20\n",
			patchSummary:   true,
			wantReason:     "reported 1 skipped transactions",
			wantNormalized: true,
		},
		{
			name:           "retried",
			specExtra:      "BENCHMARK_MAX_TRIES=2\n",
			raw:            "0 1 100 0 1786492800 780769 1\n1 1 200 0 1786492800 781111 0\n",
			patchSummary:   true,
			wantReason:     "reported 1 retried transactions",
			wantNormalized: true,
		},
		{
			name:           "completed count mismatch",
			raw:            "0 1 100 0 1786492800 780769\n",
			wantReason:     "completed count mismatch: got 1 want 3000",
			wantNormalized: true,
		},
		{
			name:           "completion outside measure phase",
			raw:            strings.ReplaceAll(fakePgbenchLog(3000, 2), "1786492800", "1786496400"),
			wantReason:     "completion window falls outside the measure phase",
			wantNormalized: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeRunCatalog(t, 1, true)
			if test.specExtra != "" {
				path := filepath.Join(root, "benchmarks", "test.env")
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeRunFile(t, root, filepath.Join("benchmarks", "test.env"), string(content)+test.specExtra)
			}
			series, err := Run(root, speccatalog.New(root), "test", Options{
				Runtime: "docker",
				RunID:   "raw-evidence-" + strings.ReplaceAll(test.name, " ", "-"),
				Now:     fixedRunClock,
				Getenv:  func(string) string { return "" },
				RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
					writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 2, true)
					if test.patchSummary {
						path := filepath.Join(root, "runs", options.RunID, "driver", "pgbench-summary.log")
						content, readErr := os.ReadFile(path)
						if readErr != nil {
							t.Fatal(readErr)
						}
						patched := string(content)
						if test.name == "retried" {
							patched = strings.Replace(patched, "maximum number of tries: 1", "maximum number of tries: 2", 1)
							patched = strings.Replace(patched, "number of failed transactions: 0 (0.000%)", "number of failed transactions: 0 (0.000%)\nnumber of transactions retried: 0 (0.000%)\ntotal number of retries: 0", 1)
						}
						if test.name == "skipped" {
							patched = strings.Replace(patched, "latency average = 0.150 ms", "rate limit schedule lag: avg 0.015 (max 0.020) ms\nlatency average = 0.150 ms", 1)
						}
						if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					writeRunFile(t, root, filepath.Join("runs", options.RunID, "driver", "pgbench-raw", "pgbench_log.1"), test.raw)
					completeFakePhaseJournal(t, options.Env)
					return passedExperimentResult(options.RunID), nil
				},
				VerifyRun: func(root string, input string) (runverify.Result, error) {
					return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
				},
			})
			if err == nil || series.Status != "invalid" || len(series.Trials) != 1 {
				t.Fatalf("invalid raw evidence did not invalidate the series: err=%v series=%#v", err, series)
			}
			trial := series.Trials[0]
			if !containsRunReason(trial.Reasons, test.wantReason) {
				t.Fatalf("trial reason does not contain %q: %v", test.wantReason, trial.Reasons)
			}
			if (trial.TransactionLog != nil) != test.wantNormalized {
				t.Fatalf("unexpected normalized transaction-log retention: %#v", trial.TransactionLog)
			}
		})
	}
}

func TestRunAllowsSampledTransactionLogWithoutCountExtrapolation(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	specPath := filepath.Join(root, "benchmarks", "test.env")
	content, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	writeRunFile(t, root, filepath.Join("benchmarks", "test.env"), string(content)+"BENCHMARK_LOG_SAMPLE_RATE=0.5\n")

	series, err := Run(root, speccatalog.New(root), "test", Options{
		Runtime: "docker",
		RunID:   "sampled-transaction-log",
		Now:     fixedRunClock,
		Getenv:  func(string) string { return "" },
		RunExperiment: func(root string, _ speccatalog.Catalog, _ string, options experimentrun.Options) (experimentrun.Result, error) {
			writeFakeExperimentRun(t, root, options.RunID, options.Runtime, options.Env, 100, 2, true)
			writeRunFile(t, root, filepath.Join("runs", options.RunID, "driver", "pgbench-raw", "pgbench_log.1"), "0 1 100 0 1786492800 780769\n")
			completeFakePhaseJournal(t, options.Env)
			return passedExperimentResult(options.RunID), nil
		},
		VerifyRun: func(root string, input string) (runverify.Result, error) {
			return runverify.Result{Dir: filepath.Join(root, "runs", input), Issues: []string{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	transactionLog := series.Trials[0].TransactionLog
	if transactionLog == nil || !transactionLog.Sampled || transactionLog.SampleRate != 0.5 || transactionLog.Logged != 1 || transactionLog.Completed != 1 {
		t.Fatalf("sampled evidence was extrapolated or discarded: %#v", transactionLog)
	}
}

func TestValidatePgbenchResultBindsConnectionChurnEvidence(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "pgbenchresult", "testdata", "pg16-reconnect.txt"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, parseErr := pgbenchresult.Parse(file)
	closeErr := file.Close()
	if parseErr != nil || closeErr != nil {
		t.Fatalf("parse reconnect fixture: parse=%v close=%v", parseErr, closeErr)
	}
	plan := benchmarkplan.Plan{
		Mode:                  "fixed-time",
		Scale:                 1,
		Clients:               15,
		Threads:               1,
		MeasureSeconds:        10,
		WorkloadScript:        "workloads/pgbench/scripts/select1.sql",
		QueryProtocol:         "simple",
		PrimaryMetric:         "pgbench.latency_mean_us",
		ConnectPerTransaction: true,
	}
	if err := ValidatePgbenchResult(plan, parsed); err != nil {
		t.Fatalf("valid reconnect evidence was rejected: %v", err)
	}
	if parsed.TPSIncludingConnections == nil || parsed.AverageConnectionTimeMS == nil {
		t.Fatalf("reconnect metrics were not retained: %#v", parsed)
	}
	stddev := 0.1
	parsed.LatencyStddevMS = &stddev
	if err := ValidatePgbenchResult(plan, parsed); err == nil || !strings.Contains(err.Error(), "unexpected detailed latency evidence") {
		t.Fatalf("ordinary protocol accepted an injected detailed-summary marker: %v", err)
	}
	parsed.LatencyStddevMS = nil
	plan.ConnectPerTransaction = false
	if err := ValidatePgbenchResult(plan, parsed); err == nil || !strings.Contains(err.Error(), "reported reconnect evidence") {
		t.Fatalf("reconnect evidence was accepted without a declared churn protocol: %v", err)
	}
}

func TestValidatePgbenchResultEnforcesLatencyLimitExceededBudget(t *testing.T) {
	duration := 30.0
	limit := 50.0
	above := int64(2)
	total := int64(100)
	initial := 1.0
	tps := 1000.0
	scheduleAverage := 1.0
	scheduleMax := 2.0
	latencyStddev := 0.1
	budget := 1.0
	parsed := pgbenchresult.Result{
		TransactionType:         "<builtin: TPC-B (sort of)>",
		ScaleFactor:             1,
		QueryMode:               "prepared",
		Mode:                    pgbenchresult.ModeTime,
		Clients:                 8,
		Threads:                 4,
		MaximumTries:            1,
		DurationSeconds:         &duration,
		TransactionsProcessed:   total,
		LatencyLimitMS:          &limit,
		TransactionsAboveLimit:  &above,
		LatencyLimitTotal:       &total,
		LatencyStddevMS:         &latencyStddev,
		InitialConnectionTimeMS: &initial,
		TPSExcludingConnections: &tps,
		ScheduleLagAverageMS:    &scheduleAverage,
		ScheduleLagMaxMS:        &scheduleMax,
	}
	plan := benchmarkplan.Plan{
		Mode:                       "fixed-time",
		Scale:                      1,
		Clients:                    8,
		Threads:                    4,
		MeasureSeconds:             30,
		WorkloadMode:               "builtin",
		QueryProtocol:              "prepared",
		Rate:                       &tps,
		LatencyLimitMS:             &limit,
		MaxLatencyLimitExceededPct: &budget,
	}
	if err := ValidatePgbenchResult(plan, parsed); err == nil || !strings.Contains(err.Error(), "above the declared") {
		t.Fatalf("latency SLO violation was not rejected: %v", err)
	}
	above = 1
	if err := ValidatePgbenchResult(plan, parsed); err != nil {
		t.Fatalf("latency SLO boundary should pass: %v", err)
	}
	parsed.LatencyStddevMS = nil
	if err := ValidatePgbenchResult(plan, parsed); err == nil || !strings.Contains(err.Error(), "omitted detailed latency evidence") {
		t.Fatalf("rate/latency-limit protocol accepted an ordinary summary: %v", err)
	}
}

func writeRunCatalog(t *testing.T, trials int, logTransactions bool) string {
	t.Helper()
	root := t.TempDir()
	writeRunFile(t, root, "configs/default/postgresql.conf", "shared_buffers = '128MB'\n")
	writeRunFile(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_SERVICES=postgres\n")
	writeRunFile(t, root, "experiments/benchmarks/pgbench.env", strings.Join([]string{
		"EXPERIMENT_NAME=Benchmark driver",
		"EXPERIMENT_TOPOLOGY=single",
		"EXPERIMENT_PG_CONFIG=default",
		"EXPERIMENT_WORKLOAD_SPEC=pgbench/tiny",
	}, "\n")+"\n")
	writeRunFile(t, root, "workloads/pgbench/tiny.env", strings.Join([]string{
		"WORKLOAD_NAME=Tiny pgbench",
		"WORKLOAD_KIND=pgbench",
		"PGBENCH_MODE=builtin",
	}, "\n")+"\n")
	writeRunFile(t, root, "benchmarks/test.env", fmt.Sprintf(strings.Join([]string{
		"BENCHMARK_NAME=Contract fixture",
		"BENCHMARK_CLASS=smoke",
		"BENCHMARK_DRIVER=pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/tiny",
		"BENCHMARK_PG_CONFIG=default",
		"BENCHMARK_MODE=fixed-time",
		"BENCHMARK_SCALE=1",
		"BENCHMARK_CLIENTS=2",
		"BENCHMARK_THREADS=1",
		"BENCHMARK_WARMUP_SECONDS=0",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_TRIALS=%d",
		"BENCHMARK_MIN_VALID_TRIALS=%d",
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
		"BENCHMARK_MAX_CV_PCT=100",
		"BENCHMARK_PROTOCOL=simple",
		"BENCHMARK_LOG_TRANSACTIONS=%d",
	}, "\n")+"\n", trials, trials, boolRunInt(logTransactions)))
	return root
}

func fakeBenchmarkEngine(t *testing.T, identity string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pgworkbench")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+identity+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeRunScenarioPack(t *testing.T, root string) (string, scenariopack.Inspection) {
	t.Helper()
	scriptPath := filepath.Join(root, "scripts", "run_experiment.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRunFile(t, root, scenariopack.ManifestName, `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "benchmark-test-pack",
  "version": "1.0.0",
  "engine_constraint": ">=0.2.0",
  "assets": ["benchmarks", "experiments", "workloads", "configs", "topologies", "scripts/run_experiment.sh"]
}
`)
	pack, err := scenariopack.Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	return scriptPath, pack
}

func writeFakeExperimentRun(t *testing.T, root, runID, runtimeName string, env []string, tps float64, clients int, raw bool) {
	t.Helper()
	runDir := filepath.Join(root, "runs", runID)
	processed := int64(tps*30 + 0.5)
	values := make(map[string]string, len(env)+8)
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range map[string]string{
		"EXPERIMENT_TOPOLOGY":           "single",
		"EXPERIMENT_PROFILE_SETUP":      "0",
		"EXPERIMENT_PROFILE_RUN":        "0",
		"PROFILE_SIZE":                  "small",
		"PROFILE_SECONDS":               "30",
		"DATASET_SIZE":                  "small",
		"PG_CONFIG":                     "default",
		"PGWORKBENCH_RUNTIME":           runtimeName,
		"PGWORKBENCH_EXECUTION_TIMEOUT": experimentrun.DefaultExecutionTimeout.String(),
		"PGWORKBENCH_CLEANUP_GRACE":     experimentrun.DefaultCleanupGrace.String(),
	} {
		values[key] = value
	}
	experimentDigest, err := evidence.DigestFile(filepath.Join(root, "experiments", "benchmarks", "pgbench.env"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := runstate.Manifest{
		RunID:                     runID,
		StartedAt:                 "2026-08-12T00:00:00Z",
		ExperimentSpecID:          "benchmarks/pgbench",
		ExperimentSpecRef:         "experiments/benchmarks/pgbench.env",
		ExperimentSpecDigest:      experimentDigest,
		SourceSpecKind:            values["PGWORKBENCH_SOURCE_SPEC_KIND"],
		SourceSpecID:              values["PGWORKBENCH_SOURCE_SPEC_ID"],
		SourceSpecRef:             values["PGWORKBENCH_SOURCE_SPEC_REF"],
		SourceSpecDigest:          values["PGWORKBENCH_SOURCE_SPEC_DIGEST"],
		ExecutionParametersDigest: runstate.EffectiveParametersDigest(func(key string) string { return values[key] }),
		Runtime:                   runtimeName,
		EngineVersion:             "0.3.0",
		EngineCommit:              strings.Repeat("a", 40),
		PackID:                    values["PGWORKBENCH_PACK_ID"],
		PackVersion:               values["PGWORKBENCH_PACK_VERSION"],
		PackDigest:                values["PGWORKBENCH_PACK_DIGEST"],
		RuntimeFingerprintStatus:  runstate.RuntimeFingerprintObserved,
		RuntimeFingerprintTarget:  "primary",
		RuntimeOS:                 "linux",
		RuntimeArch:               "amd64",
		PostgresServerVersionNum:  "170009",
		PostgresServerMajor:       "17",
		RuntimeFingerprintAt:      "2026-08-12T00:00:00Z",
		ExperimentName:            "Benchmark driver",
		ExperimentTopology:        "single",
		ExperimentPGConfig:        values["EXPERIMENT_PG_CONFIG"],
		ProfileSize:               "small",
		WorkloadSpec:              values["EXPERIMENT_WORKLOAD_SPEC"],
		MetricsEnabled:            "1",
	}
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	latencyMS := 1000 * float64(clients) / tps
	detailed := values["PGBENCH_RATE"] != "" || values["PGBENCH_LATENCY_LIMIT"] != ""
	if detailed {
		latencyMS = 0.150
	}
	summaryLines := []string{
		"pgbench (17.9, server 17.9)",
		"transaction type: <builtin: TPC-B (sort of)>",
		"scaling factor: 1",
		"query mode: simple",
		"number of clients: %d",
		"number of threads: 1",
		"maximum number of tries: 1",
		"duration: 30 s",
		"number of transactions actually processed: %d",
		"number of failed transactions: 0 (0.000%%)",
		"latency average = %.3f ms",
	}
	if detailed {
		summaryLines = append(summaryLines, "latency stddev = 0.100 ms")
	}
	summaryLines = append(summaryLines,
		"initial connection time = 2.000 ms",
		"tps = %.6f (without initial connection time)",
	)
	summary := fmt.Sprintf(strings.Join(summaryLines, "\n")+"\n", clients, processed, latencyMS, tps)
	if err := os.MkdirAll(filepath.Join(runDir, "driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := values["PGBENCH_TARGET"]
	if target == "" {
		target = benchmarkplan.TargetDirectPostgres
	}
	contract := benchmarkplan.EndpointDirectV1
	host, port, service := "127.0.0.1", "5432", "postgres"
	driverImageID, targetImageID := "sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("d", 64)
	if target == benchmarkplan.TargetPgBouncer {
		contract, host = benchmarkplan.EndpointPgBouncerV1, "pgbouncer"
	}
	if runtimeName == "native" {
		host, port, service = values["POSTGRES_HOST"], values["POSTGRES_PORT"], "native-host"
		driverImageID, targetImageID = "not-applicable", "not-applicable"
	}
	writeRunFile(t, root, filepath.Join("runs", runID, "stdout.log"), fmt.Sprintf("pgworkbench_benchmark_target=%s endpoint_contract=%s driver_service=%s endpoint_host=%s endpoint_port=%s driver_image_id=%s target_image_id=%s\n", target, contract, service, host, port, driverImageID, targetImageID))
	writeRunFile(t, root, filepath.Join("runs", runID, "driver", "pgbench-summary.log"), summary)
	writeRunFile(t, root, filepath.Join("runs", runID, "metrics.csv"), string(metricstest.Default()))
	if raw {
		writeRunFile(t, root, filepath.Join("runs", runID, "driver", "pgbench-raw", "pgbench_log.1"), fakePgbenchLog(processed, clients))
	}
}

func fakePgbenchLog(count int64, clients int) string {
	var output strings.Builder
	for index := int64(0); index < count; index++ {
		client := index % int64(clients)
		transaction := index/int64(clients) + 1
		latency := int64(100)
		if index%2 == 1 {
			latency = 200
		}
		fmt.Fprintf(&output, "%d %d %d 0 1786492800 %d\n", client, transaction, latency, 780000+index)
	}
	return output.String()
}

func passedExperimentResult(runID string) experimentrun.Result {
	return experimentrun.Result{
		SchemaVersion: experimentrun.SchemaVersion,
		RunID:         runID,
		StartedAt:     "2026-08-12T00:00:00Z",
		FinishedAt:    "2026-08-12T00:00:30Z",
		DurationMS:    30000,
		ExitCode:      0,
		Status:        "passed",
	}
}

func completeFakePhaseJournal(t *testing.T, env []string) {
	completeFakePhaseJournalWithFailure(t, env, "")
}

func completeFakePhaseJournalWithFailure(t *testing.T, env []string, failedPhase string) {
	completeFakePhaseJournalAt(t, env, failedPhase, time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC))
}

func completeFakePhaseJournalAt(t *testing.T, env []string, failedPhase string, start time.Time) {
	t.Helper()
	path := envValue(env, "PGWORKBENCH_BENCHMARK_PHASE_FILE")
	if path == "" {
		t.Fatal("benchmark phase journal environment is missing")
	}
	runID := envValue(env, "PGWORKBENCH_BENCHMARK_RUN_ID")
	trial, err := strconv.Atoi(envValue(env, "PGWORKBENCH_BENCHMARK_TRIAL"))
	if err != nil || runID == "" || trial <= 0 {
		t.Fatal("benchmark phase journal binding environment is missing")
	}
	runDir := filepath.Dir(filepath.Dir(envValue(env, "PGBENCH_RESULT_FILE")))
	primaryPath := filepath.Join(runDir, "artifacts", "benchmark", "phases.tsv")
	if err := os.MkdirAll(filepath.Dir(primaryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	primary, err := os.OpenFile(primaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	preflightFinished := start.Add(111 * time.Millisecond)
	finish := preflightFinished.Add(30 * time.Second)
	events := []struct {
		sequence       int
		name, status   string
		started, ended time.Time
		reason         string
	}{
		{1, "preflight", "passed", start, preflightFinished, ""},
		{2, "prepare", "passed", preflightFinished, preflightFinished, ""},
		{3, "stabilize", "skipped", preflightFinished, preflightFinished, "no stabilization gate declared"},
		{4, "pre-warmup-control", "skipped", preflightFinished, preflightFinished, "protocol controls are not enabled"},
		{5, "warmup", "skipped", preflightFinished, preflightFinished, "zero warmup duration"},
		{6, "pre-measure-control", "skipped", preflightFinished, preflightFinished, "protocol controls are not enabled"},
		{7, "measure", "passed", preflightFinished, finish, ""},
		{8, "cooldown", "passed", finish, finish, ""},
		{9, "validate", "passed", finish, finish, ""},
		{10, "collect", "passed", finish, finish, ""},
		{11, "cleanup", "passed", finish, finish, ""},
	}
	for _, event := range events {
		if event.name == failedPhase {
			event.status = "failed"
			event.reason = "synthetic phase failure"
		}
		for _, journal := range []string{primaryPath, path} {
			if err := appendPhaseJournal(journal, runID, trial, event.sequence, event.name, event.status, event.started, event.ended, event.reason); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if value, found := strings.CutPrefix(item, prefix); found {
			return value
		}
	}
	return ""
}

func assertTrialEnvironment(t *testing.T, env []string, runID string, raw bool) {
	t.Helper()
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[key] = value
		}
	}
	for key, want := range map[string]string{
		"PGWORKBENCH_SOURCE_SPEC_KIND": "benchmark",
		"PGWORKBENCH_SOURCE_SPEC_ID":   "test",
		"EXPERIMENT_METRICS_INTERVAL":  "1",
		"PGBENCH_CLIENTS":              "2",
		"PGBENCH_THREADS":              "1",
		"PGBENCH_SCALE":                "1",
		"PGBENCH_TIME":                 "30",
		"PGBENCH_RESULT_FILE":          filepath.Join(filepath.Dir(values["PGBENCH_RESULT_FILE"]), "pgbench-summary.log"),
	} {
		if values[key] != want {
			t.Fatalf("unexpected %s: got %q want %q", key, values[key], want)
		}
	}
	if !strings.Contains(values["PGBENCH_RESULT_FILE"], filepath.Join("runs", runID, "driver")) {
		t.Fatalf("result path does not target trial run: %q", values["PGBENCH_RESULT_FILE"])
	}
	wantRaw := "0"
	if raw {
		wantRaw = "1"
	}
	if values["PGBENCH_LOG_TRANSACTIONS"] != wantRaw {
		t.Fatalf("unexpected logging flag: got %q want %q", values["PGBENCH_LOG_TRANSACTIONS"], wantRaw)
	}
}

func writeRunFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixedRunClock() time.Time {
	return time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
}

func boolRunInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func containsRunReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}
