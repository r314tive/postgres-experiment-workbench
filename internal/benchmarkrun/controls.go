package benchmarkrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

const controlSourceLimit = 2 << 20

// ControlEvidence points from the normalized trial to all four protocol-v2
// controls in the linked immutable experiment run. The JSON artifacts retain
// the typed derivation; their sibling raw sources retain the observations.
type ControlEvidence struct {
	CacheState        ArtifactRef `json:"cache_state"`
	StatisticsReset   ArtifactRef `json:"statistics_reset"`
	CollectorOverhead ArtifactRef `json:"collector_overhead"`
	ResourceBudget    ArtifactRef `json:"resource_budget"`
}

type derivedControls struct {
	cache         benchmarkcontrol.CacheState
	reset         benchmarkcontrol.StatisticsReset
	overhead      benchmarkcontrol.CollectorOverhead
	resource      benchmarkcontrol.ResourceBudget
	cachePhase    benchmarkcontrol.BoundaryWindow
	resetPhase    benchmarkcontrol.BoundaryWindow
	overheadPhase benchmarkcontrol.BoundaryWindow
	resourcePhase benchmarkcontrol.BoundaryWindow
}

// MaterializeControlsV2 is the sole typed-control producer. It is called by
// the exact benchmark binary during the experiment collect/cleanup boundary,
// before the linked verdict makes the run immutable. Every output is
// no-replace; replaying the producer fails closed.
func MaterializeControlsV2(runDir string, plan benchmarkplan.Plan, runID string, trial int, timeline benchmarkphase.Timeline, postgresMajor string) (*ControlEvidence, error) {
	derived, controlsDir, err := deriveControlsFromRaw(runDir, plan, runID, trial, timeline, postgresMajor)
	if err != nil {
		return nil, err
	}
	for _, output := range []struct {
		name  string
		write func(string) error
	}{
		{benchmarkcontrol.CacheStateFile, func(path string) error { return benchmarkcontrol.WriteCacheState(path, derived.cache) }},
		{benchmarkcontrol.StatisticsResetFile, func(path string) error { return benchmarkcontrol.WriteStatisticsReset(path, derived.reset) }},
		{benchmarkcontrol.CollectorOverheadFile, func(path string) error { return benchmarkcontrol.WriteCollectorOverhead(path, derived.overhead) }},
		{benchmarkcontrol.ResourceBudgetFile, func(path string) error { return benchmarkcontrol.WriteResourceBudget(path, derived.resource) }},
	} {
		if err := output.write(filepath.Join(controlsDir, output.name)); err != nil {
			return nil, fmt.Errorf("write %s: %w", output.name, err)
		}
	}
	return loadDerivedControls(runDir, plan, runID, trial, derived, controlsDir)
}

// LoadControlsV2 only reopens existing typed JSON and raw sources. It never
// writes. The benchmark runner invokes it after linked-run verification so a
// completed/verdict-bearing run cannot be mutated during series normalization.
func LoadControlsV2(runDir string, plan benchmarkplan.Plan, runID string, trial int, timeline benchmarkphase.Timeline, postgresMajor string) (*ControlEvidence, error) {
	if plan.ContractVersion != "2" {
		return nil, nil
	}
	derived, controlsDir, err := deriveControlsFromRaw(runDir, plan, runID, trial, timeline, postgresMajor)
	if err != nil {
		return nil, err
	}
	return loadDerivedControls(runDir, plan, runID, trial, derived, controlsDir)
}

// MaterializeControlsV2FromEnvironment resolves only runner-owned fixed
// capabilities. It rebuilds the protocol from the immutable series capsule,
// binds it to the linked run/manifest/phase journal, and then materializes the
// four typed artifacts before the verdict is written.
func MaterializeControlsV2FromEnvironment(root, runDir string, getenv func(string) string) (*ControlEvidence, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	runID := strings.TrimSpace(getenv("PGWORKBENCH_BENCHMARK_RUN_ID"))
	seriesID := strings.TrimSpace(getenv("PGWORKBENCH_BENCHMARK_SERIES_ID"))
	trialText := strings.TrimSpace(getenv("PGWORKBENCH_BENCHMARK_TRIAL"))
	trial, err := strconv.Atoi(trialText)
	if !ValidRunID(runID) || !ValidRunID(seriesID) || err != nil || trial <= 0 || strconv.Itoa(trial) != trialText {
		return nil, fmt.Errorf("benchmark control materializer requires canonical series/run/trial bindings")
	}
	if runID != fmt.Sprintf("%s-t%03d", seriesID, trial) {
		return nil, fmt.Errorf("benchmark control materializer run id does not match its series and trial")
	}
	if err := validateMaterializerRunDir(root, runDir, runID); err != nil {
		return nil, err
	}
	capsuleRoot := strings.TrimSpace(getenv("PGWORKBENCH_BENCHMARK_CAPSULE_ROOT"))
	wantCapsule := filepath.Join(root, "runs", "benchmarks", seriesID, "protocol", "capsule")
	if err := exactExistingDirectory(capsuleRoot, wantCapsule); err != nil {
		return nil, fmt.Errorf("benchmark capsule capability: %w", err)
	}
	if getenv("PGWORKBENCH_SOURCE_SPEC_KIND") != "benchmark" {
		return nil, fmt.Errorf("benchmark control materializer requires benchmark source provenance")
	}
	specID := strings.TrimSpace(getenv("PGWORKBENCH_SOURCE_SPEC_ID"))
	if !validPortableSpecID(specID) || getenv("PGWORKBENCH_SOURCE_SPEC_REF") != filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(specID)+".env")) {
		return nil, fmt.Errorf("benchmark control materializer source id/ref binding is invalid")
	}
	plan, err := benchmarkplan.Build(speccatalog.New(capsuleRoot), specID)
	if err != nil {
		return nil, fmt.Errorf("build benchmark plan from immutable capsule: %w", err)
	}
	if plan.ContractVersion != "2" || getenv("PGWORKBENCH_BENCHMARK_CONTRACT_VERSION") != "2" ||
		plan.ProtocolDigest != getenv("PGWORKBENCH_BENCHMARK_PROTOCOL_DIGEST") ||
		plan.Spec != specID || plan.SpecDigest != getenv("PGWORKBENCH_SOURCE_SPEC_DIGEST") {
		return nil, fmt.Errorf("immutable capsule plan does not match bound v2 source/protocol identity")
	}
	for _, identity := range []struct{ label, got, want string }{
		{"experiment id", plan.ExperimentSpec, getenv("PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_ID")},
		{"experiment digest", plan.ExperimentDigest, getenv("PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_DIGEST")},
		{"workload id", plan.WorkloadSpec, getenv("PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID")},
		{"workload digest", plan.WorkloadDigest, getenv("PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_DIGEST")},
		{"PostgreSQL config id", plan.PGConfig, getenv("PGWORKBENCH_BENCHMARK_PG_CONFIG_ID")},
		{"PostgreSQL config digest", plan.PGConfigDigest, getenv("PGWORKBENCH_BENCHMARK_PG_CONFIG_DIGEST")},
		{"workload script ref", plan.WorkloadScript, getenv("PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_REF")},
		{"workload script digest", plan.WorkloadScriptDigest, getenv("PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_DIGEST")},
	} {
		if identity.got != identity.want {
			return nil, fmt.Errorf("benchmark capsule %s mismatch", identity.label)
		}
	}
	journal, err := readPhaseJournal(filepath.Join(runDir, "artifacts", "benchmark", "phases.tsv"))
	if err != nil {
		return nil, fmt.Errorf("read complete linked phase journal: %w", err)
	}
	timeline, err := benchmarkphase.ParseTSV(bytes.NewReader(journal), trial, runID)
	if err != nil {
		return nil, fmt.Errorf("parse complete linked phase journal: %w", err)
	}
	manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		return nil, fmt.Errorf("parse linked manifest: %w", err)
	}
	if manifest["run_id"] != runID {
		return nil, fmt.Errorf("linked manifest run id does not match materializer binding")
	}
	if err := ValidateLinkedRunProtocolWithToolchain(plan, manifest["runtime"], trial, getenv("PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST"), manifest); err != nil {
		return nil, fmt.Errorf("linked manifest protocol binding: %w", err)
	}
	return MaterializeControlsV2(runDir, plan, runID, trial, timeline, manifest["postgres_server_major"])
}

func deriveControlsFromRaw(runDir string, plan benchmarkplan.Plan, runID string, trial int, timeline benchmarkphase.Timeline, postgresMajor string) (derivedControls, string, error) {
	if plan.ContractVersion != "2" {
		return derivedControls{}, "", fmt.Errorf("typed control materialization requires benchmark contract v2")
	}
	if timeline.RunID != runID || timeline.Trial != trial || len(timeline.Events) != len(benchmarkphase.OrderedNames) {
		return derivedControls{}, "", fmt.Errorf("benchmark control evidence requires the complete bound phase timeline")
	}
	controlsDir, err := canonicalControlDirectory(runDir)
	if err != nil {
		return derivedControls{}, "", err
	}
	cachePhase, err := cacheControlWindow(timeline)
	if err != nil {
		return derivedControls{}, "", err
	}
	resetPhase, err := statisticsResetWindow(plan.StatisticsResetBoundary, timeline)
	if err != nil {
		return derivedControls{}, "", err
	}
	stabilize, err := phaseEventWindow(timeline, benchmarkphase.StabilizeName)
	if err != nil {
		return derivedControls{}, "", err
	}
	cooldown, err := phaseEventWindow(timeline, benchmarkphase.CooldownName)
	if err != nil {
		return derivedControls{}, "", err
	}
	overheadPhase := benchmarkcontrol.BoundaryWindow{StartedAt: stabilize.StartedAt, FinishedAt: cooldown.FinishedAt}
	resourcePhase, err := passedPhaseEventWindow(timeline, benchmarkphase.PrepareName)
	if err != nil {
		return derivedControls{}, "", err
	}

	cacheRaw, err := readControlSource(controlsDir, benchmarkcontrol.CacheStateSourceFile)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("cache control raw source: %w", err)
	}
	cache, err := benchmarkcontrol.NewCacheStateFromSource(benchmarkcontrol.CacheStateInput{
		RunID: runID, ProtocolDigest: plan.ProtocolDigest, Trial: trial, CapturedAt: cachePhase.FinishedAt,
		BoundaryWindow: cachePhase, Mode: plan.CacheRegime, TargetRelations: plan.CacheTargetRelations, MinResidentPct: plan.CacheMinResidentPct,
	}, cacheRaw)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("normalize cache control evidence: %w", err)
	}
	resetRaw, err := readControlSource(controlsDir, benchmarkcontrol.StatisticsResetSourceFile)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("statistics reset raw source: %w", err)
	}
	reset, err := benchmarkcontrol.NewStatisticsResetFromSource(benchmarkcontrol.StatisticsResetInput{
		RunID: runID, ProtocolDigest: plan.ProtocolDigest, Trial: trial, CapturedAt: resetPhase.FinishedAt,
		PostgresServerMajor: postgresMajor, Policy: plan.StatisticsResetPolicy, Boundary: plan.StatisticsResetBoundary, BoundaryWindow: resetPhase,
	}, resetRaw)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("normalize statistics reset evidence: %w", err)
	}
	overheadRaw, err := readControlSource(controlsDir, benchmarkcontrol.CollectorOverheadSourceFile)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("collector overhead raw source: %w", err)
	}
	requiredSamples := 0
	if plan.CollectorOverheadSamples != nil {
		requiredSamples = *plan.CollectorOverheadSamples
	}
	overhead, err := benchmarkcontrol.NewCollectorOverheadFromSource(benchmarkcontrol.CollectorOverheadInput{
		RunID: runID, ProtocolDigest: plan.ProtocolDigest, Trial: trial, CapturedAt: overheadPhase.FinishedAt,
		CalibrationWindow: overheadPhase, Mode: plan.CollectorOverheadMode,
		IntervalNS: int64(plan.CollectorIntervalSeconds) * 1_000_000_000, RequiredSamples: requiredSamples, MaxDutyCyclePct: plan.CollectorMaxDutyCyclePct,
	}, overheadRaw)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("normalize collector overhead evidence: %w", err)
	}
	resourceRaw, err := readControlSource(controlsDir, benchmarkcontrol.ResourceBudgetSourceFile)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("resource budget raw source: %w", err)
	}
	resource, err := benchmarkcontrol.NewResourceBudgetFromSource(benchmarkcontrol.ResourceBudgetInput{
		RunID: runID, ProtocolDigest: plan.ProtocolDigest, Trial: trial, CapturedAt: resourcePhase.FinishedAt,
		EnforcementWindow: resourcePhase, Mode: plan.ResourceBudgetMode, Scope: plan.ResourceBudgetScope,
		Provider: plan.ResourceEnforcementProvider, ProviderConstraints: plan.ResourceProviderConstraints,
		CPUMillicores: plan.CPUBudgetMillicores, MemoryMiB: plan.MemoryBudgetMiB,
	}, resourceRaw)
	if err != nil {
		return derivedControls{}, "", fmt.Errorf("normalize resource budget evidence: %w", err)
	}
	return derivedControls{cache: cache, reset: reset, overhead: overhead, resource: resource, cachePhase: cachePhase, resetPhase: resetPhase, overheadPhase: overheadPhase, resourcePhase: resourcePhase}, controlsDir, nil
}

func loadDerivedControls(runDir string, plan benchmarkplan.Plan, runID string, trial int, derived derivedControls, controlsDir string) (*ControlEvidence, error) {
	artifacts := &ControlEvidence{}
	for _, input := range []struct {
		label, name string
		verify      func(string) error
		parse       func([]byte) (any, error)
		expected    any
		ref         *ArtifactRef
	}{
		{"cache", benchmarkcontrol.CacheStateFile, benchmarkcontrol.VerifyCacheStateFile, func(content []byte) (any, error) { return benchmarkcontrol.ParseCacheState(content) }, derived.cache, &artifacts.CacheState},
		{"statistics reset", benchmarkcontrol.StatisticsResetFile, benchmarkcontrol.VerifyStatisticsResetFile, func(content []byte) (any, error) { return benchmarkcontrol.ParseStatisticsReset(content) }, derived.reset, &artifacts.StatisticsReset},
		{"collector overhead", benchmarkcontrol.CollectorOverheadFile, benchmarkcontrol.VerifyCollectorOverheadFile, func(content []byte) (any, error) { return benchmarkcontrol.ParseCollectorOverhead(content) }, derived.overhead, &artifacts.CollectorOverhead},
		{"resource budget", benchmarkcontrol.ResourceBudgetFile, benchmarkcontrol.VerifyResourceBudgetFile, func(content []byte) (any, error) { return benchmarkcontrol.ParseResourceBudget(content) }, derived.resource, &artifacts.ResourceBudget},
	} {
		path := filepath.Join(controlsDir, input.name)
		if err := input.verify(path); err != nil {
			return nil, fmt.Errorf("verify %s against raw source: %w", input.name, err)
		}
		content, err := readControlSource(controlsDir, input.name)
		if err != nil {
			return nil, err
		}
		actual, err := input.parse(content)
		if err != nil {
			return nil, err
		}
		actualJSON, _ := json.Marshal(actual)
		expectedJSON, _ := json.Marshal(input.expected)
		if !bytes.Equal(actualJSON, expectedJSON) {
			return nil, fmt.Errorf("%s typed evidence does not match independent raw/plan/phase derivation", input.label)
		}
		ref, err := artifactRef(runDir, path)
		if err != nil {
			return nil, fmt.Errorf("reference %s: %w", input.name, err)
		}
		*input.ref = ref
	}
	binding := benchmarkcontrol.Binding{RunID: runID, ProtocolDigest: plan.ProtocolDigest, Trial: trial}
	for _, check := range []struct {
		label     string
		binding   benchmarkcontrol.Binding
		window    benchmarkcontrol.BoundaryWindow
		phase     benchmarkcontrol.BoundaryWindow
		satisfied bool
	}{
		{"cache", benchmarkcontrol.CacheStateBinding(derived.cache), derived.cache.BoundaryWindow, derived.cachePhase, benchmarkcontrol.CacheControlSatisfied(derived.cache)},
		{"statistics reset", benchmarkcontrol.StatisticsResetBinding(derived.reset), derived.reset.BoundaryWindow, derived.resetPhase, benchmarkcontrol.StatisticsResetSatisfied(derived.reset)},
		{"collector overhead", benchmarkcontrol.CollectorOverheadBinding(derived.overhead), derived.overhead.CalibrationWindow, derived.overheadPhase, benchmarkcontrol.CollectorOverheadSatisfied(derived.overhead)},
		{"resource budget", benchmarkcontrol.ResourceBudgetBinding(derived.resource), derived.resource.EnforcementWindow, derived.resourcePhase, benchmarkcontrol.ResourceBudgetSatisfied(derived.resource)},
	} {
		if err := benchmarkcontrol.VerifyBinding(check.binding, binding); err != nil {
			return nil, fmt.Errorf("%s binding: %w", check.label, err)
		}
		if err := benchmarkcontrol.VerifyWindowWithin(check.window, check.phase); err != nil {
			return nil, fmt.Errorf("%s phase binding: %w", check.label, err)
		}
		if !check.satisfied {
			return artifacts, fmt.Errorf("%s control is unsatisfied", check.label)
		}
	}
	return artifacts, nil
}

func cacheControlWindow(timeline benchmarkphase.Timeline) (benchmarkcontrol.BoundaryWindow, error) {
	return passedPhaseEventWindow(timeline, benchmarkphase.PreMeasureControlName)
}

func statisticsResetWindow(boundary string, timeline benchmarkphase.Timeline) (benchmarkcontrol.BoundaryWindow, error) {
	names := map[string]string{
		"none":           benchmarkphase.PrepareName,
		"before-trial":   benchmarkphase.PrepareName,
		"before-warmup":  benchmarkphase.PreWarmupControlName,
		"before-measure": benchmarkphase.PreMeasureControlName,
	}
	name, ok := names[boundary]
	if !ok {
		return benchmarkcontrol.BoundaryWindow{}, fmt.Errorf("unsupported statistics reset boundary %q", boundary)
	}
	return passedPhaseEventWindow(timeline, name)
}

func phaseEventWindow(timeline benchmarkphase.Timeline, name string) (benchmarkcontrol.BoundaryWindow, error) {
	if window, ok := optionalPhaseEventWindow(timeline, name); ok {
		return window, nil
	}
	return benchmarkcontrol.BoundaryWindow{}, fmt.Errorf("benchmark phase timeline has no %s event", name)
}

func optionalPhaseEventWindow(timeline benchmarkphase.Timeline, name string) (benchmarkcontrol.BoundaryWindow, bool) {
	event, ok := benchmarkphase.EventByName(timeline, name)
	if !ok {
		return benchmarkcontrol.BoundaryWindow{}, false
	}
	return benchmarkcontrol.BoundaryWindow{StartedAt: event.StartedAt, FinishedAt: event.FinishedAt}, true
}

func passedPhaseEventWindow(timeline benchmarkphase.Timeline, name string) (benchmarkcontrol.BoundaryWindow, error) {
	event, ok := benchmarkphase.EventByName(timeline, name)
	if !ok {
		return benchmarkcontrol.BoundaryWindow{}, fmt.Errorf("benchmark phase timeline has no %s event", name)
	}
	if event.Status != "passed" {
		return benchmarkcontrol.BoundaryWindow{}, fmt.Errorf("benchmark control requires a passed %s phase", name)
	}
	return benchmarkcontrol.BoundaryWindow{StartedAt: event.StartedAt, FinishedAt: event.FinishedAt}, nil
}

func canonicalControlDirectory(runDir string) (string, error) {
	runPath, err := filepath.Abs(runDir)
	if err != nil {
		return "", err
	}
	resolvedRun, err := filepath.EvalSymlinks(runPath)
	if err != nil || resolvedRun != runPath {
		return "", fmt.Errorf("linked run directory is not canonical and symlink-free")
	}
	directory := filepath.Join(runPath, "artifacts", "benchmark", "controls")
	if err := exactExistingDirectory(directory, directory); err != nil {
		return "", fmt.Errorf("benchmark control directory: %w", err)
	}
	return directory, nil
}

func readControlSource(directory, name string) ([]byte, error) {
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > controlSourceLimit {
		return nil, fmt.Errorf("%s is not a bounded non-empty regular file", name)
	}
	return os.ReadFile(path)
}

func validateMaterializerRunDir(root, runDir, runID string) error {
	if !filepath.IsAbs(runDir) {
		return fmt.Errorf("benchmark control run directory must be absolute")
	}
	want := filepath.Join(root, "runs", runID)
	if err := exactExistingDirectory(runDir, want); err != nil {
		return fmt.Errorf("linked run capability: %w", err)
	}
	return nil
}

func exactExistingDirectory(actual, expected string) error {
	actualAbs, err := filepath.Abs(actual)
	if err != nil {
		return err
	}
	expectedAbs, err := filepath.Abs(expected)
	if err != nil {
		return err
	}
	actualResolved, err := filepath.EvalSymlinks(actualAbs)
	if err != nil {
		return err
	}
	expectedResolved, err := filepath.EvalSymlinks(expectedAbs)
	if err != nil {
		return err
	}
	if actualResolved != expectedResolved || filepath.Clean(actualAbs) != filepath.Clean(expectedAbs) {
		return fmt.Errorf("path is not the exact canonical capability")
	}
	info, err := os.Lstat(actualAbs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path is not a non-symlink directory")
	}
	return nil
}

func validPortableSpecID(value string) bool {
	if value == "" || len(value) > 200 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
		for index, character := range component {
			alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
			if !alphanumeric && (index == 0 || character != '.' && character != '_' && character != '-') {
				return false
			}
		}
	}
	return true
}
