package benchmarkab

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcompare"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkqualify"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksettings"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

const defaultMaxBookendGapSeconds int64 = 4 * 60 * 60

// Run executes two fresh benchmark series in the protocol-declared AB/BA
// order. It never upgrades independently scheduled series into paired
// evidence, and it records every invalid or failed execution prefix.
func Run(root string, catalog speccatalog.Catalog, baselineInput, candidateInput string, options Options) (Result, error) {
	options = withDefaults(options)
	root, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	baselinePlan, err := benchmarkplan.Build(catalog, baselineInput)
	if err != nil {
		return Result{}, fmt.Errorf("build baseline benchmark plan: %w", err)
	}
	candidatePlan, err := benchmarkplan.Build(catalog, candidateInput)
	if err != nil {
		return Result{}, fmt.Errorf("build candidate benchmark plan: %w", err)
	}
	runID := options.RunID
	if runID == "" {
		runID = "ab-" + options.Now().UTC().Format("20060102_150405")
	}
	protocol, err := BuildProtocol(runID, options.Runtime, options.BaselineSubject, options.CandidateSubject, baselinePlan, candidatePlan, options)
	if err != nil {
		return Result{}, err
	}

	abParent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "benchmark-ab"), 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare counterbalanced benchmark artifact parent: %w", err)
	}
	seriesParent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "benchmarks"), 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare benchmark series artifact parent: %w", err)
	}
	abDir := filepath.Join(abParent, runID)
	baselineDir := filepath.Join(seriesParent, runID+"-a")
	candidateDir := filepath.Join(seriesParent, runID+"-b")
	if err := requireAbsent(abDir, baselineDir, candidateDir); err != nil {
		return Result{}, err
	}

	beforeOptions := options.Qualification
	beforeOptions.RecordedAt = options.Now().UTC()
	before, err := options.InspectHost(beforeOptions)
	if err != nil {
		return Result{}, fmt.Errorf("record before host qualification: %w", err)
	}
	var baselineInstallation, candidateInstallation nativetoolchain.Installation
	if err := initializeArtifactDir(abDir, func(stagingDir string) error {
		if protocol.SubjectDimension == SubjectNativeToolchain {
			baselineInstallation, candidateInstallation, err = inspectNativeToolchains(options)
			if err != nil {
				return err
			}
			if baselineInstallation.Manifest.Digest != protocol.Baseline.NativeToolchain.Digest || candidateInstallation.Manifest.Digest != protocol.Candidate.NativeToolchain.Digest {
				return fmt.Errorf("native toolchain identity changed before artifact reservation")
			}
			if err := nativetoolchain.Snapshot(baselineInstallation, filepath.Join(stagingDir, "toolchains", "baseline")); err != nil {
				return fmt.Errorf("snapshot baseline native toolchain: %w", err)
			}
			if err := nativetoolchain.Snapshot(candidateInstallation, filepath.Join(stagingDir, "toolchains", "candidate")); err != nil {
				return fmt.Errorf("snapshot candidate native toolchain: %w", err)
			}
		}
		if err := writeJSON(filepath.Join(stagingDir, "protocol.json"), protocol); err != nil {
			return fmt.Errorf("write counterbalanced benchmark protocol: %w", err)
		}
		beforePath := filepath.Join(stagingDir, "qualification", "before.json")
		if err := benchmarkqualify.WriteFile(beforePath, before); err != nil {
			return fmt.Errorf("write before host qualification: %w", err)
		}
		verification, err := benchmarkqualify.VerifyFile(beforePath)
		if err != nil {
			return fmt.Errorf("verify before host qualification: %w", err)
		}
		if !verification.Valid {
			return fmt.Errorf("verify before host qualification: %s", strings.Join(verification.Issues, "; "))
		}
		return nil
	}); err != nil {
		return Result{RunID: runID}, err
	}

	baselineOptions := options.SeriesOptions
	baselineOptions.Runtime = protocol.Runtime
	baselineOptions.RunID = runID + "-a"
	baselineOptions.Subject = protocol.Baseline.Subject
	baselineOptions.Stdout = options.Stdout
	baselineOptions.Stderr = options.Stderr
	baselineOptions.Now = options.Now
	baselineOptions.ABProtocolDigest = protocol.Digest
	baselineOptions.ABEffectiveSettingNames = append([]string(nil), protocol.EffectiveSettings.Names...)
	baselineOptions.SubjectDimension = protocol.SubjectDimension
	candidateOptions := options.SeriesOptions
	candidateOptions.Runtime = protocol.Runtime
	candidateOptions.RunID = runID + "-b"
	candidateOptions.Subject = protocol.Candidate.Subject
	candidateOptions.Stdout = options.Stdout
	candidateOptions.Stderr = options.Stderr
	candidateOptions.Now = options.Now
	candidateOptions.ABProtocolDigest = protocol.Digest
	candidateOptions.ABEffectiveSettingNames = append([]string(nil), protocol.EffectiveSettings.Names...)
	candidateOptions.SubjectDimension = protocol.SubjectDimension
	if protocol.SubjectDimension == SubjectNativeToolchain {
		baselineOptions.NativeBindir = baselineInstallation.Bindir
		baselineOptions.NativeToolchainDigest = protocol.Baseline.NativeToolchain.Digest
		baselineOptions.NativeToolchainManifestRef = filepath.ToSlash(filepath.Join("runs", "benchmark-ab", runID, protocol.Baseline.NativeToolchain.ManifestRef))
		candidateOptions.NativeBindir = candidateInstallation.Bindir
		candidateOptions.NativeToolchainDigest = protocol.Candidate.NativeToolchain.Digest
		candidateOptions.NativeToolchainManifestRef = filepath.ToSlash(filepath.Join("runs", "benchmark-ab", runID, protocol.Candidate.NativeToolchain.ManifestRef))
	}

	baselineExecution, err := benchmarkrun.Start(root, catalog, baselinePlan, baselineOptions)
	if err != nil {
		return Result{RunID: runID, ArtifactDir: abDir}, fmt.Errorf("start baseline benchmark series: %w", err)
	}
	candidateExecution, err := benchmarkrun.Start(root, catalog, candidatePlan, candidateOptions)
	if err != nil {
		_, _ = baselineExecution.Finish()
		return Result{RunID: runID, ArtifactDir: abDir}, fmt.Errorf("start candidate benchmark series: %w", err)
	}

	var scheduleErr error
	for index, order := range protocol.Orders {
		roles := []string{"baseline", "candidate"}
		if order == "BA" {
			roles[0], roles[1] = roles[1], roles[0]
		}
		for _, role := range roles {
			execution := baselineExecution
			bindir := baselineInstallation.Bindir
			installation := baselineInstallation
			if role == "candidate" {
				execution = candidateExecution
				bindir = candidateInstallation.Bindir
				installation = candidateInstallation
			}
			trial, executeErr := execution.ExecuteTrial()
			if protocol.SubjectDimension == SubjectNativeToolchain {
				executeErr = errors.Join(executeErr, nativetoolchain.Revalidate(installation))
				stopErr := options.StopNativeRuntime(root, bindir, options.SeriesOptions.Getenv, options.Stdout, options.Stderr)
				executeErr = errors.Join(executeErr, stopErr)
			}
			if executeErr != nil {
				scheduleErr = errors.Join(scheduleErr, fmt.Errorf("block %d %s execution: %w", index+1, role, executeErr))
				break
			}
			if trial.Status == "failed" {
				scheduleErr = errors.Join(scheduleErr, fmt.Errorf("block %d %s execution failed", index+1, role))
				break
			}
		}
		if scheduleErr != nil {
			break
		}
	}

	baselineSeries, baselineFinishErr := baselineExecution.Finish()
	candidateSeries, candidateFinishErr := candidateExecution.Finish()
	// A non-passing series is represented by the terminal A/B result. Only an
	// absent result.json means finalization itself failed and cannot be verified.
	if !regularFile(filepath.Join(baselineSeries.ArtifactDir, "result.json")) {
		return Result{RunID: runID, ArtifactDir: abDir}, errors.Join(scheduleErr, baselineFinishErr, fmt.Errorf("baseline series did not finalize"))
	}
	if !regularFile(filepath.Join(candidateSeries.ArtifactDir, "result.json")) {
		return Result{RunID: runID, ArtifactDir: abDir}, errors.Join(scheduleErr, candidateFinishErr, fmt.Errorf("candidate series did not finalize"))
	}

	afterOptions := options.Qualification
	afterOptions.RecordedAt = options.Now().UTC()
	after, err := options.InspectHost(afterOptions)
	if err != nil {
		return Result{RunID: runID, ArtifactDir: abDir}, errors.Join(scheduleErr, fmt.Errorf("record after host qualification: %w", err))
	}
	if err := benchmarkqualify.WriteFile(filepath.Join(abDir, "qualification", "after.json"), after); err != nil {
		return Result{RunID: runID, ArtifactDir: abDir}, fmt.Errorf("write after host qualification: %w", err)
	}

	result, err := buildResult(root, abDir, protocol, before, after, baselineSeries, candidateSeries)
	if err != nil {
		return Result{RunID: runID, ArtifactDir: abDir}, err
	}
	if err := writeResultArtifacts(abDir, result); err != nil {
		return result, err
	}
	verification, verifyErr := Verify(root, abDir)
	if verifyErr != nil {
		return result, fmt.Errorf("verify produced counterbalanced benchmark: %w", verifyErr)
	}
	if !verification.IsValid() {
		return result, fmt.Errorf("produced counterbalanced benchmark is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	if result.Status != "passed" {
		return result, fmt.Errorf("counterbalanced benchmark ended with status %s and decision %s", result.Status, result.Decision)
	}
	return result, nil
}

func buildResult(root, abDir string, protocol Protocol, before, after benchmarkqualify.Artifact, baseline, candidate benchmarkrun.Series) (Result, error) {
	protocolRef, err := localFileRef(abDir, filepath.Join(abDir, "protocol.json"))
	if err != nil {
		return Result{}, err
	}
	beforeRef, err := localFileRef(abDir, filepath.Join(abDir, "qualification", "before.json"))
	if err != nil {
		return Result{}, err
	}
	afterRef, err := localFileRef(abDir, filepath.Join(abDir, "qualification", "after.json"))
	if err != nil {
		return Result{}, err
	}
	baselineRef, err := seriesRef(root, "baseline", baseline)
	if err != nil {
		return Result{}, err
	}
	candidateRef, err := seriesRef(root, "candidate", candidate)
	if err != nil {
		return Result{}, err
	}
	blocks := DeriveBlocks(protocol, baseline, candidate)
	analysis := benchmarkcompare.AnalyzePaired(pairedUnits(blocks), pairedOptions(protocol))
	assessment := assessQualification(protocol, before, after, baseline, candidate)
	effectiveSettings := assessEffectiveSettings(protocol, baseline, candidate)
	status, decision, reasons := deriveTerminal(protocol, baseline, candidate, blocks, assessment, effectiveSettings, analysis)
	result := Result{
		SchemaVersion:    RunSchemaVersion,
		ArtifactType:     RunArtifactType,
		SchedulerVersion: SchedulerVersion,
		RunID:            protocol.RunID,
		RunDir:           ".",
		ProtocolRef:      protocolRef,
		StartedAt:        before.RecordedAt,
		FinishedAt:       after.RecordedAt,
		Baseline:         baselineRef,
		Candidate:        candidateRef,
		Blocks:           blocks,
		Qualification: QualificationResult{
			Before:     beforeRef,
			After:      afterRef,
			Assessment: assessment,
		},
		EffectiveSettings: effectiveSettings,
		Analysis:          analysis,
		Status:            status,
		Decision:          decision,
		Reasons:           reasons,
		ArtifactDir:       abDir,
	}
	result.Digest, err = resultDigest(result)
	return result, err
}

// DeriveBlocks constructs the full predeclared schedule from the two immutable
// series. Missing executions remain explicit invalid block entries.
func DeriveBlocks(protocol Protocol, baseline, candidate benchmarkrun.Series) []Block {
	blocks := make([]Block, protocol.BlocksPlanned)
	for index := 0; index < protocol.BlocksPlanned; index++ {
		block := Block{
			Number:       index + 1,
			Unit:         index/2 + 1,
			PlannedOrder: protocol.Orders[index],
			Executions:   []BlockExecution{},
			Status:       "invalid",
			Reasons:      []string{},
		}
		roles := []string{"baseline", "candidate"}
		if block.PlannedOrder == "BA" {
			roles[0], roles[1] = roles[1], roles[0]
		}
		trials := map[string][]benchmarkrun.Trial{"baseline": baseline.Trials, "candidate": candidate.Trials}
		seriesIDs := map[string]string{"baseline": baseline.RunID, "candidate": candidate.RunID}
		for position, role := range roles {
			if index >= len(trials[role]) {
				block.Reasons = append(block.Reasons, role+" trial was not executed")
				continue
			}
			trial := trials[role][index]
			execution := BlockExecution{
				Position:    position + 1,
				Role:        role,
				SeriesRunID: seriesIDs[role],
				Trial:       trial.Trial,
				TrialRunID:  trial.RunID,
				Status:      trial.Status,
			}
			if trial.PhaseTimeline != nil {
				execution.PhaseDigest = trial.PhaseTimeline.Digest
			}
			block.Executions = append(block.Executions, execution)
			if trial.Status != "passed" {
				block.Reasons = append(block.Reasons, fmt.Sprintf("%s trial status is %s", role, trial.Status))
			}
		}
		if index < len(baseline.Trials) && index < len(candidate.Trials) {
			left, right := baseline.Trials[index], candidate.Trials[index]
			if left.Status == "failed" || right.Status == "failed" {
				block.Status = "failed"
			} else if left.Status == "passed" && right.Status == "passed" && left.PrimaryValue != nil && right.PrimaryValue != nil {
				block.BaselineValue = cloneFloat(left.PrimaryValue)
				block.CandidateValue = cloneFloat(right.PrimaryValue)
				effect, effectErr := normalizedEffect(*left.PrimaryValue, *right.PrimaryValue, protocol.Direction)
				if effectErr == nil {
					block.EffectPct = &effect
					block.Status = "passed"
				} else {
					block.Reasons = append(block.Reasons, effectErr.Error())
				}
			}
		} else {
			for _, execution := range block.Executions {
				if execution.Status == "failed" {
					block.Status = "failed"
				}
			}
		}
		block.Reasons = uniqueSorted(block.Reasons)
		blocks[index] = block
	}
	return blocks
}

func pairedUnits(blocks []Block) []benchmarkcompare.CounterbalanceUnit {
	units := make([]benchmarkcompare.CounterbalanceUnit, 0, len(blocks)/2)
	for index := 0; index+1 < len(blocks); index += 2 {
		units = append(units, benchmarkcompare.CounterbalanceUnit{
			AB: pairedBlock(blocks[index]),
			BA: pairedBlock(blocks[index+1]),
		})
	}
	return units
}

func pairedBlock(block Block) benchmarkcompare.PairedBlock {
	result := benchmarkcompare.PairedBlock{Valid: block.Status == "passed" && block.BaselineValue != nil && block.CandidateValue != nil}
	if result.Valid {
		result.Baseline = *block.BaselineValue
		result.Candidate = *block.CandidateValue
	}
	return result
}

func pairedOptions(protocol Protocol) benchmarkcompare.PairedOptions {
	threshold := protocol.RegressionThresholdPct
	return benchmarkcompare.PairedOptions{
		Direction:              protocol.Direction,
		RegressionThresholdPct: &threshold,
		MinUnits:               protocol.MinValidUnits,
		BootstrapResamples:     protocol.Analysis.BootstrapResamples,
		ConfidenceLevel:        protocol.Analysis.ConfidenceLevel,
		Seed:                   protocol.Analysis.Seed,
	}
}

func assessQualification(protocol Protocol, before, after benchmarkqualify.Artifact, baseline, candidate benchmarkrun.Series) benchmarkqualify.BookendAssessment {
	assessment := benchmarkqualify.AssessBookends(before, after)
	add := func(reason string) {
		assessment.Reasons = append(assessment.Reasons, reason)
		assessment.Status = benchmarkqualify.BookendStatusUnqualified
	}
	beforePolicyDigest, _ := benchmarkqualify.PolicyDigest(before.Policy)
	afterPolicyDigest, _ := benchmarkqualify.PolicyDigest(after.Policy)
	if beforePolicyDigest != protocol.Qualification.PolicyDigest || afterPolicyDigest != protocol.Qualification.PolicyDigest || !reflect.DeepEqual(before.Policy, protocol.Qualification.Policy) || !reflect.DeepEqual(after.Policy, protocol.Qualification.Policy) {
		add("qualification bookends do not use the protocol policy")
	}
	for label, artifact := range map[string]benchmarkqualify.Artifact{"before": before, "after": after} {
		if artifact.Snapshot.Storage.Label != protocol.Qualification.StorageLabel {
			add(label + " storage label differs from protocol")
		}
		placement := artifact.Snapshot.Client.Placement
		if placement.Availability != benchmarkqualify.AvailabilityObserved || placement.Value != protocol.Qualification.ClientPlacement {
			add(label + " client placement differs from protocol")
		}
	}
	beforeTime, beforeErr := time.Parse(time.RFC3339Nano, before.RecordedAt)
	afterTime, afterErr := time.Parse(time.RFC3339Nano, after.RecordedAt)
	if beforeErr == nil && afterErr == nil && afterTime.Sub(beforeTime) > time.Duration(protocol.MaxBookendGapSeconds)*time.Second {
		add("qualification bookend interval exceeds protocol maximum")
	}
	first, last, ok := trialInterval(baseline, candidate)
	if !ok {
		add("no complete trial timeline is available for temporal qualification")
	} else {
		if beforeErr != nil || beforeTime.After(first) {
			add("before qualification does not precede all trial phases")
		}
		if afterErr != nil || afterTime.Before(last) {
			add("after qualification does not follow all trial phases")
		}
	}
	assessment.Reasons = uniqueSorted(assessment.Reasons)
	return assessment
}

func trialInterval(series ...benchmarkrun.Series) (time.Time, time.Time, bool) {
	var first, last time.Time
	found := false
	for _, item := range series {
		for _, trial := range item.Trials {
			if trial.PhaseTimeline == nil {
				continue
			}
			started, startErr := time.Parse(time.RFC3339Nano, trial.PhaseTimeline.StartedAt)
			finished, finishErr := time.Parse(time.RFC3339Nano, trial.PhaseTimeline.FinishedAt)
			if startErr != nil || finishErr != nil {
				continue
			}
			if !found || started.Before(first) {
				first = started
			}
			if !found || finished.After(last) {
				last = finished
			}
			found = true
		}
	}
	return first, last, found
}

func assessEffectiveSettings(protocol Protocol, baseline, candidate benchmarkrun.Series) EffectiveSettingsAssessment {
	assessment := EffectiveSettingsAssessment{
		Status:               "invalid",
		Names:                append([]string(nil), protocol.EffectiveSettings.Names...),
		EffectiveDifferences: []string{},
		Reasons:              []string{},
	}
	var baselineStable, candidateStable *benchmarksettings.Evidence
	var invalid, unqualified []string
	for _, arm := range []struct {
		role   string
		series benchmarkrun.Series
		stable **benchmarksettings.Evidence
	}{
		{role: "baseline", series: baseline, stable: &baselineStable},
		{role: "candidate", series: candidate, stable: &candidateStable},
	} {
		if len(arm.series.Trials) != protocol.BlocksPlanned {
			invalid = append(invalid, arm.role+" effective pg_settings population is incomplete")
		}
		for index, trial := range arm.series.Trials {
			if trial.Status != "passed" || trial.EffectiveSettings == nil {
				invalid = append(invalid, fmt.Sprintf("%s trial %d has no passed effective pg_settings evidence", arm.role, index+1))
				continue
			}
			recorded := trial.EffectiveSettings
			if err := benchmarksettings.Verify(*recorded); err != nil {
				invalid = append(invalid, fmt.Sprintf("%s trial %d effective pg_settings evidence is invalid", arm.role, index+1))
				continue
			}
			if recorded.RunID != trial.RunID || recorded.Trial != trial.Trial || recorded.ProtocolDigest != protocol.Digest || !reflect.DeepEqual(recorded.Names, protocol.EffectiveSettings.Names) {
				invalid = append(invalid, fmt.Sprintf("%s trial %d effective pg_settings identity differs from protocol", arm.role, index+1))
				continue
			}
			for _, setting := range recorded.Settings {
				if setting.PendingRestart {
					unqualified = append(unqualified, fmt.Sprintf("%s trial %d setting %s is pending restart", arm.role, index+1, setting.Name))
				}
			}
			if *arm.stable == nil {
				copy := *recorded
				*arm.stable = &copy
			} else if !benchmarksettings.Equivalent(**arm.stable, *recorded) {
				unqualified = append(unqualified, arm.role+" effective pg_settings drifted between trials")
			}
		}
	}
	if baselineStable != nil {
		assessment.BaselineServerVersionNum = baselineStable.ServerVersionNum
	}
	if candidateStable != nil {
		assessment.CandidateServerVersionNum = candidateStable.ServerVersionNum
	}
	if len(invalid) > 0 || baselineStable == nil || candidateStable == nil {
		assessment.Status = "invalid"
		assessment.Reasons = uniqueSorted(append(invalid, unqualified...))
		return assessment
	}
	if baselineStable.ServerVersionNum != candidateStable.ServerVersionNum {
		unqualified = append(unqualified, "effective pg_settings arms do not share one PostgreSQL server version")
	}
	assessment.EffectiveDifferences = append([]string{}, benchmarksettings.EffectiveDifferenceNames(*baselineStable, *candidateStable)...)
	if len(unqualified) > 0 {
		assessment.Status = "unstable"
		assessment.Reasons = uniqueSorted(unqualified)
		return assessment
	}
	if protocol.EffectiveSettings.RequireCrossArmDifference && len(assessment.EffectiveDifferences) == 0 {
		assessment.Status = "equivalent"
		assessment.Reasons = []string{"baseline and candidate have no effective value-and-unit pg_settings difference"}
		return assessment
	}
	if protocol.EffectiveSettings.RequireCrossArmDifference {
		assessment.Status = "verified-different"
	} else {
		assessment.Status = "verified-stable"
	}
	return assessment
}

func deriveTerminal(protocol Protocol, baseline, candidate benchmarkrun.Series, blocks []Block, assessment benchmarkqualify.BookendAssessment, effectiveSettings EffectiveSettingsAssessment, analysis benchmarkcompare.PairedAnalysis) (string, string, []string) {
	var invalid, inconclusive []string
	for role, series := range map[string]benchmarkrun.Series{"baseline": baseline, "candidate": candidate} {
		if series.RunID == "" || len(series.Trials) != protocol.BlocksPlanned {
			invalid = append(invalid, role+" series did not complete the predeclared schedule")
		}
		switch series.Status {
		case "passed":
		case "inconclusive":
			inconclusive = append(inconclusive, role+" series is inconclusive")
		default:
			invalid = append(invalid, fmt.Sprintf("%s series status is %s", role, series.Status))
		}
		for _, reason := range series.Reasons {
			if series.Status != "passed" {
				inconclusive = append(inconclusive, role+" series: "+reason)
			}
		}
	}
	if len(blocks) != protocol.BlocksPlanned {
		invalid = append(invalid, "recorded block count differs from protocol")
	}
	if analysis.Status == "invalid" {
		invalid = append(invalid, analysis.Reasons...)
	}
	if effectiveSettings.Status == "invalid" {
		invalid = append(invalid, effectiveSettings.Reasons...)
		invalid = append(invalid, "effective pg_settings evidence is invalid")
	}
	if len(invalid) > 0 {
		return "invalid", "invalid", uniqueSorted(append(invalid, inconclusive...))
	}
	if assessment.Status != benchmarkqualify.BookendStatusRecordedPolicyPassed {
		inconclusive = append(inconclusive, assessment.Reasons...)
	}
	if analysis.Status == "inconclusive" {
		inconclusive = append(inconclusive, analysis.Reasons...)
	}
	requiredSettingsStatus := "verified-different"
	if !protocol.EffectiveSettings.RequireCrossArmDifference {
		requiredSettingsStatus = "verified-stable"
	}
	if effectiveSettings.Status != requiredSettingsStatus {
		inconclusive = append(inconclusive, effectiveSettings.Reasons...)
		inconclusive = append(inconclusive, "effective pg_settings stability/difference gate is not decision-qualified")
	}
	if len(inconclusive) > 0 {
		return "inconclusive", "inconclusive", uniqueSorted(inconclusive)
	}
	return analysis.Status, analysis.Decision, uniqueSorted(analysis.Reasons)
}

func normalizedEffect(baseline, candidate float64, direction string) (float64, error) {
	if math.IsNaN(baseline) || math.IsInf(baseline, 0) || baseline == 0 || math.IsNaN(candidate) || math.IsInf(candidate, 0) || candidate == 0 {
		return 0, fmt.Errorf("block primary values must be finite and non-zero")
	}
	value := 100 * (candidate/baseline - 1)
	if direction == "lower" {
		value = 100 * (1 - candidate/baseline)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("block effect is not finite")
	}
	return value, nil
}

func writeResultArtifacts(dir string, result Result) error {
	for _, block := range result.Blocks {
		name := fmt.Sprintf("%03d-%s.json", block.Number, strings.ToLower(block.PlannedOrder))
		if err := writeJSON(filepath.Join(dir, "blocks", name), block); err != nil {
			return err
		}
	}
	if err := writeBlocksTSV(filepath.Join(dir, "blocks.tsv"), result.Blocks); err != nil {
		return err
	}
	if err := writeSummary(filepath.Join(dir, "summary.md"), result); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "result.json"), result)
}

func writeBlocksTSV(path string, blocks []Block) error {
	return writeFileAtomic(path, blocksTSVBytes(blocks), 0o644)
}

func blocksTSVBytes(blocks []Block) []byte {
	var out strings.Builder
	out.WriteString("block\tunit\torder\tstatus\tbaseline_value\tcandidate_value\teffect_pct\n")
	for _, block := range blocks {
		fmt.Fprintf(&out, "%d\t%d\t%s\t%s\t%s\t%s\t%s\n", block.Number, block.Unit, block.PlannedOrder, block.Status, optionalFloat(block.BaselineValue), optionalFloat(block.CandidateValue), optionalFloat(block.EffectPct))
	}
	return []byte(out.String())
}

func summaryBytes(result Result) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "# Counterbalanced PostgreSQL A/B Benchmark\n\n")
	fmt.Fprintf(&out, "- Run: `%s`\n", result.RunID)
	fmt.Fprintf(&out, "- Status: `%s`\n", result.Status)
	fmt.Fprintf(&out, "- Decision: `%s`\n", result.Decision)
	fmt.Fprintf(&out, "- Baseline: `%s`\n", result.Baseline.RunID)
	fmt.Fprintf(&out, "- Candidate: `%s`\n", result.Candidate.RunID)
	fmt.Fprintf(&out, "- Complete valid units: `%d` / `%d`\n", result.Analysis.UnitsN, result.Analysis.TotalUnits)
	fmt.Fprintf(&out, "- Qualification: `%s`\n", result.Qualification.Assessment.Status)
	fmt.Fprintf(&out, "- Effective pg_settings: `%s`\n", result.EffectiveSettings.Status)
	if len(result.EffectiveSettings.EffectiveDifferences) > 0 {
		fmt.Fprintf(&out, "- Effective setting differences: `%s`\n", strings.Join(result.EffectiveSettings.EffectiveDifferences, ", "))
	}
	if result.Analysis.UnitsN > 0 {
		fmt.Fprintf(&out, "- Normalized median effect: `%g%%`\n", result.Analysis.MedianEffectPct)
	}
	if result.Analysis.Status == "passed" || result.Analysis.Status == "failed" {
		fmt.Fprintf(&out, "- Confidence interval: `[%g%%, %g%%]`\n", result.Analysis.CILowPct, result.Analysis.CIHighPct)
	}
	if len(result.Reasons) > 0 {
		out.WriteString("\n## Reasons\n\n")
		for _, reason := range result.Reasons {
			fmt.Fprintf(&out, "- %s\n", reason)
		}
	}
	return []byte(out.String())
}

func writeSummary(path string, result Result) error {
	return writeFileAtomic(path, summaryBytes(result), 0o644)
}

func resultDigest(result Result) (string, error) {
	copy := result
	copy.Digest = ""
	copy.ArtifactDir = ""
	content, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func seriesRef(root, role string, series benchmarkrun.Series) (SeriesRef, error) {
	path := filepath.Join(series.ArtifactDir, "result.json")
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return SeriesRef{}, err
	}
	reference, err := filepath.Rel(root, series.ArtifactDir)
	if err != nil || !evidence.IsPortablePath(filepath.ToSlash(reference)) {
		return SeriesRef{}, fmt.Errorf("series directory is outside artifact root")
	}
	return SeriesRef{Role: role, RunID: series.RunID, Ref: filepath.ToSlash(reference), ResultDigest: digest}, nil
}

func localFileRef(base, path string) (FileRef, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return FileRef{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return FileRef{}, fmt.Errorf("artifact reference is not a non-empty regular file: %s", path)
	}
	reference, err := filepath.Rel(base, path)
	if err != nil || !evidence.IsPortablePath(filepath.ToSlash(reference)) {
		return FileRef{}, fmt.Errorf("artifact reference escapes its root")
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return FileRef{}, err
	}
	return FileRef{Path: filepath.ToSlash(reference), Digest: digest, Size: info.Size()}, nil
}

func initializeArtifactDir(path string, initialize func(string) error) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create counterbalanced benchmark parent directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(parent, "."+filepath.Base(path)+".staging-")
	if err != nil {
		return fmt.Errorf("create counterbalanced benchmark staging directory: %w", err)
	}
	if err := os.Chmod(stagingDir, 0o755); err != nil {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("set counterbalanced benchmark staging directory permissions: %w", err)
	}
	stagingOwned := true
	defer func() {
		if stagingOwned {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	for _, name := range []string{"qualification", "blocks"} {
		if err := os.Mkdir(filepath.Join(stagingDir, name), 0o755); err != nil {
			return fmt.Errorf("create counterbalanced benchmark %s directory: %w", name, err)
		}
	}
	if err := initialize(stagingDir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		return fmt.Errorf("counterbalanced benchmark target already exists: %s (%s)", path, info.Mode())
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stagingDir, path); err != nil {
		return fmt.Errorf("publish counterbalanced benchmark directory: %w", err)
	}
	stagingOwned = false
	return nil
}

func requireAbsent(paths ...string) error {
	for _, path := range paths {
		if info, err := os.Lstat(path); err == nil {
			return fmt.Errorf("counterbalanced benchmark target already exists: %s (%s)", path, info.Mode())
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(content, '\n'), 0o644)
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pgworkbench-ab-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func optionalFloat(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'g', -1, 64)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func withDefaults(options Options) Options {
	if options.Runtime == "" {
		options.Runtime = options.SeriesOptions.Runtime
	}
	if options.Runtime == "" {
		options.Runtime = "docker"
	}
	if options.SubjectDimension == "" {
		options.SubjectDimension = SubjectPGConfig
	}
	if options.BaselineSubject == "" {
		options.BaselineSubject = "baseline"
	}
	if options.CandidateSubject == "" {
		options.CandidateSubject = "candidate"
	}
	if options.BootstrapResamples == 0 {
		options.BootstrapResamples = 10000
	}
	if options.ConfidenceLevel == 0 {
		options.ConfidenceLevel = 0.95
	}
	if options.MaxBookendGapSeconds == 0 {
		options.MaxBookendGapSeconds = defaultMaxBookendGapSeconds
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.InspectHost == nil {
		options.InspectHost = benchmarkqualify.Inspect
	}
	if options.StopNativeRuntime == nil {
		options.StopNativeRuntime = stopNativeRuntime
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	return options
}

func stopNativeRuntime(root, bindir string, getenv func(string) string, stdout, stderr io.Writer) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	command := exec.Command(filepath.Join(root, "scripts", "runtime.sh"), "down", "single")
	command.Dir = root
	command.Stdout = stdout
	command.Stderr = stderr
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for _, key := range []string{"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD"} {
		if value := getenv(key); value != "" {
			values[key] = value
		}
	}
	values["PGWORKBENCH_RUNTIME"] = "native"
	values["PGWORKBENCH_NATIVE_BINDIR"] = bindir
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command.Env = make([]string, 0, len(keys))
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+values[key])
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("stop native runtime with bound toolchain %s: %w", bindir, err)
	}
	return nil
}
