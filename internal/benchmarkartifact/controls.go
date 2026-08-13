package benchmarkartifact

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
)

func checkBenchmarkControls(result *VerifyResult, runDir string, number int, trial benchmarkrun.Trial, plan *benchmarkplan.Plan) {
	if plan == nil {
		if trial.Controls != nil {
			addIssue(result, "trial %d control evidence cannot be bound without a valid protocol plan", number)
		}
		return
	}
	if plan.ContractVersion != "2" {
		if trial.Controls != nil {
			addIssue(result, "trial %d protocol v1 unexpectedly claims protocol-v2 controls", number)
		}
		return
	}
	if trial.Controls == nil {
		if trial.ExperimentVerified || trial.Status == "passed" {
			addIssue(result, "trial %d verified protocol-v2 execution has no normalized controls", number)
		}
		return
	}
	if trial.PhaseTimeline == nil || len(trial.PhaseTimeline.Events) != len(benchmarkphase.OrderedNames) {
		addIssue(result, "trial %d controls have no complete independently verified phase timeline", number)
		return
	}
	checkControlDirectoryInventory(result, runDir, number)

	binding := benchmarkcontrol.Binding{RunID: trial.RunID, ProtocolDigest: plan.ProtocolDigest, Trial: number}
	cacheControl := trial.PhaseTimeline.Events[benchmarkphase.PreMeasureControlIndex]
	if cacheControl.Status != "passed" {
		addIssue(result, "trial %d cache evidence requires a passed %s phase", number, benchmarkphase.PreMeasureControlName)
	}
	cacheWindow := controlPhaseWindow(cacheControl)
	prepareWindow := controlPhaseWindow(trial.PhaseTimeline.Events[benchmarkphase.PrepareIndex])
	resetWindow, resetWindowErr := controlResetWindow(plan.StatisticsResetBoundary, *trial.PhaseTimeline)
	if resetWindowErr != nil {
		addIssue(result, "trial %d statistics-reset plan boundary is invalid: %v", number, resetWindowErr)
		return
	}
	overheadWindow := benchmarkcontrol.BoundaryWindow{
		StartedAt:  trial.PhaseTimeline.Events[benchmarkphase.StabilizeIndex].StartedAt,
		FinishedAt: trial.PhaseTimeline.Events[benchmarkphase.CooldownIndex].FinishedAt,
	}

	cachePath, cacheOK := checkExactControlRef(result, runDir, number, "cache state", trial.Controls.CacheState, benchmarkcontrol.CacheStateFile)
	if cacheOK {
		if err := benchmarkcontrol.VerifyCacheStateFile(cachePath); err != nil {
			addIssue(result, "trial %d cache control cannot be independently verified from raw source: %v", number, err)
		} else if content, err := os.ReadFile(cachePath); err != nil {
			addIssue(result, "trial %d cache control cannot be read: %v", number, err)
		} else if artifact, err := benchmarkcontrol.ParseCacheState(content); err != nil {
			addIssue(result, "trial %d cache control cannot be parsed: %v", number, err)
		} else {
			if err := benchmarkcontrol.VerifyBinding(benchmarkcontrol.CacheStateBinding(artifact), binding); err != nil {
				addIssue(result, "trial %d cache control binding mismatch: %v", number, err)
			}
			if artifact.Mode != plan.CacheRegime || !slices.Equal(artifact.TargetRelations, plan.CacheTargetRelations) || !equalOptionalFloat(artifact.MinResidentPct, plan.CacheMinResidentPct) {
				addIssue(result, "trial %d cache control declarations do not match plan", number)
			}
			checkExactControlWindow(result, number, "cache", artifact.CapturedAt, artifact.BoundaryWindow, cacheWindow)
			checkControlSatisfaction(result, number, "cache", benchmarkcontrol.CacheControlSatisfied(artifact), trial.Status)
		}
	}

	resetPath, resetOK := checkExactControlRef(result, runDir, number, "statistics reset", trial.Controls.StatisticsReset, benchmarkcontrol.StatisticsResetFile)
	if resetOK {
		if err := benchmarkcontrol.VerifyStatisticsResetFile(resetPath); err != nil {
			addIssue(result, "trial %d statistics-reset control cannot be independently verified from raw source: %v", number, err)
		} else if content, err := os.ReadFile(resetPath); err != nil {
			addIssue(result, "trial %d statistics-reset control cannot be read: %v", number, err)
		} else if artifact, err := benchmarkcontrol.ParseStatisticsReset(content); err != nil {
			addIssue(result, "trial %d statistics-reset control cannot be parsed: %v", number, err)
		} else {
			if err := benchmarkcontrol.VerifyBinding(benchmarkcontrol.StatisticsResetBinding(artifact), binding); err != nil {
				addIssue(result, "trial %d statistics-reset control binding mismatch: %v", number, err)
			}
			if artifact.Policy != plan.StatisticsResetPolicy || artifact.Boundary != plan.StatisticsResetBoundary {
				addIssue(result, "trial %d statistics-reset control declarations do not match plan", number)
			}
			if manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env")); err != nil || artifact.PostgresServerMajor != manifest["postgres_server_major"] {
				addIssue(result, "trial %d statistics-reset PostgreSQL major does not match linked manifest", number)
			}
			checkExactControlWindow(result, number, "statistics reset", artifact.CapturedAt, artifact.BoundaryWindow, resetWindow)
			checkControlSatisfaction(result, number, "statistics reset", benchmarkcontrol.StatisticsResetSatisfied(artifact), trial.Status)
		}
	}

	overheadPath, overheadOK := checkExactControlRef(result, runDir, number, "collector overhead", trial.Controls.CollectorOverhead, benchmarkcontrol.CollectorOverheadFile)
	if overheadOK {
		if err := benchmarkcontrol.VerifyCollectorOverheadFile(overheadPath); err != nil {
			addIssue(result, "trial %d collector-overhead control cannot be independently verified from raw source: %v", number, err)
		} else if content, err := os.ReadFile(overheadPath); err != nil {
			addIssue(result, "trial %d collector-overhead control cannot be read: %v", number, err)
		} else if artifact, err := benchmarkcontrol.ParseCollectorOverhead(content); err != nil {
			addIssue(result, "trial %d collector-overhead control cannot be parsed: %v", number, err)
		} else {
			if err := benchmarkcontrol.VerifyBinding(benchmarkcontrol.CollectorOverheadBinding(artifact), binding); err != nil {
				addIssue(result, "trial %d collector-overhead control binding mismatch: %v", number, err)
			}
			wantSamples := 0
			if plan.CollectorOverheadSamples != nil {
				wantSamples = *plan.CollectorOverheadSamples
			}
			if artifact.Mode != plan.CollectorOverheadMode || artifact.IntervalNS != int64(plan.CollectorIntervalSeconds)*1_000_000_000 || artifact.RequiredSamples != wantSamples || !equalOptionalFloat(artifact.MaxDutyCyclePct, plan.CollectorMaxDutyCyclePct) {
				addIssue(result, "trial %d collector-overhead control declarations do not match plan", number)
			}
			checkExactControlWindow(result, number, "collector overhead", artifact.CapturedAt, artifact.CalibrationWindow, overheadWindow)
			checkControlSatisfaction(result, number, "collector overhead", benchmarkcontrol.CollectorOverheadSatisfied(artifact), trial.Status)
		}
	}

	resourcePath, resourceOK := checkExactControlRef(result, runDir, number, "resource budget", trial.Controls.ResourceBudget, benchmarkcontrol.ResourceBudgetFile)
	if resourceOK {
		if err := benchmarkcontrol.VerifyResourceBudgetFile(resourcePath); err != nil {
			addIssue(result, "trial %d resource-budget control cannot be independently verified from raw source: %v", number, err)
		} else if content, err := os.ReadFile(resourcePath); err != nil {
			addIssue(result, "trial %d resource-budget control cannot be read: %v", number, err)
		} else if artifact, err := benchmarkcontrol.ParseResourceBudget(content); err != nil {
			addIssue(result, "trial %d resource-budget control cannot be parsed: %v", number, err)
		} else {
			if err := benchmarkcontrol.VerifyBinding(benchmarkcontrol.ResourceBudgetBinding(artifact), binding); err != nil {
				addIssue(result, "trial %d resource-budget control binding mismatch: %v", number, err)
			}
			if artifact.Mode != plan.ResourceBudgetMode || artifact.Scope != plan.ResourceBudgetScope || artifact.Provider != plan.ResourceEnforcementProvider ||
				!slices.Equal(artifact.ProviderConstraints, plan.ResourceProviderConstraints) || !equalOptionalInt(artifact.CPUMillicores, plan.CPUBudgetMillicores) || !equalOptionalInt(artifact.MemoryMiB, plan.MemoryBudgetMiB) {
				addIssue(result, "trial %d resource-budget control declarations do not match plan", number)
			}
			checkExactControlWindow(result, number, "resource budget", artifact.CapturedAt, artifact.EnforcementWindow, prepareWindow)
			checkControlSatisfaction(result, number, "resource budget", benchmarkcontrol.ResourceBudgetSatisfied(artifact), trial.Status)
		}
	}
}

func checkControlDirectoryInventory(result *VerifyResult, runDir string, number int) {
	const ref = "artifacts/benchmark/controls"
	directory, err := safeExistingJoin(runDir, ref)
	if err != nil {
		addIssue(result, "trial %d benchmark control directory is unsafe: %v", number, err)
		return
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		addIssue(result, "trial %d benchmark control directory cannot be read: %v", number, err)
		return
	}
	want := map[string]bool{
		benchmarkcontrol.CacheStateFile: false, benchmarkcontrol.CacheStateSourceFile: false,
		benchmarkcontrol.StatisticsResetFile: false, benchmarkcontrol.StatisticsResetSourceFile: false,
		benchmarkcontrol.CollectorOverheadFile: false, benchmarkcontrol.CollectorOverheadSourceFile: false,
		benchmarkcontrol.ResourceBudgetFile: false, benchmarkcontrol.ResourceBudgetSourceFile: false,
	}
	for _, entry := range entries {
		if _, expected := want[entry.Name()]; !expected || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			addIssue(result, "trial %d benchmark control directory contains unexpected or unsafe entry %q", number, entry.Name())
			continue
		}
		want[entry.Name()] = true
	}
	for name, present := range want {
		if !present {
			addIssue(result, "trial %d benchmark control directory is missing %s", number, name)
		}
	}
}

func checkExactControlRef(result *VerifyResult, runDir string, number int, label string, ref benchmarkrun.ArtifactRef, name string) (string, bool) {
	want := filepath.ToSlash(filepath.Join("artifacts", "benchmark", "controls", name))
	if ref.Path != want {
		addIssue(result, "trial %d %s reference path %q is not canonical", number, label, ref.Path)
		return "", false
	}
	return checkArtifactRef(result, runDir, number, label, ref)
}

func checkExactControlWindow(result *VerifyResult, number int, label string, capturedAt string, got, want benchmarkcontrol.BoundaryWindow) {
	if got != want || capturedAt != want.FinishedAt {
		addIssue(result, "trial %d %s control window/capture does not match its exact phase boundary", number, label)
		return
	}
	if err := benchmarkcontrol.VerifyWindowWithin(got, want); err != nil {
		addIssue(result, "trial %d %s control phase containment failed: %v", number, label, err)
	}
}

func checkControlSatisfaction(result *VerifyResult, number int, label string, satisfied bool, trialStatus string) {
	if satisfied {
		return
	}
	if trialStatus != "invalid" {
		addIssue(result, "trial %d is %s despite unsatisfied %s control", number, trialStatus, label)
	}
}

func controlPhaseWindow(event benchmarkphase.Event) benchmarkcontrol.BoundaryWindow {
	return benchmarkcontrol.BoundaryWindow{StartedAt: event.StartedAt, FinishedAt: event.FinishedAt}
}

func controlResetWindow(boundary string, timeline benchmarkphase.Timeline) (benchmarkcontrol.BoundaryWindow, error) {
	index := benchmarkphase.PrepareIndex
	switch boundary {
	case "none", "before-trial":
		index = benchmarkphase.PrepareIndex
	case "before-warmup":
		index = benchmarkphase.PreWarmupControlIndex
	case "before-measure":
		index = benchmarkphase.PreMeasureControlIndex
	default:
		return benchmarkcontrol.BoundaryWindow{}, fmt.Errorf("unsupported boundary %q", boundary)
	}
	event := timeline.Events[index]
	if event.Status != "passed" {
		return benchmarkcontrol.BoundaryWindow{}, fmt.Errorf("boundary %q requires a passed %s phase", boundary, event.Name)
	}
	return controlPhaseWindow(event), nil
}

func equalOptionalInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func controlIssuesContain(issues []string, fragment string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, fragment) {
			return true
		}
	}
	return false
}
