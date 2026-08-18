package benchmarkcontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const maxArtifactBytes = 2 << 20

const (
	cacheObservationMethod = "pg_buffercache-resident-block-count/v1"
	cacheBoundary          = "before-measure"
	cacheSnapshotSemantics = "single-transaction-repeatable-read/v1"
	resetObservationMethod = "pg-stat-reset-timestamps-before-and-after/v1"
	overheadCollector      = "postgresql-sampler-v2"
	overheadTimingSource   = "runner-monotonic-clock/v1"
	resourceInspectSource  = "docker-inspect-hostconfig-and-container-cgroup-v2-probe/v1"
)

var expectedResetOperations = []ResetOperation{
	{Function: "pg_catalog.pg_stat_reset", Scope: "current-database", Rows: 1, CommandCompleted: true},
	{Function: "pg_catalog.pg_stat_reset_shared('wal')", Scope: "cluster-wal", Rows: 1, CommandCompleted: true},
}

func ExpectedStatisticsResetOperations() []ResetOperation {
	return append([]ResetOperation(nil), expectedResetOperations...)
}

func NewCacheState(input CacheStateInput) (CacheState, error) {
	artifact := CacheState{
		SchemaVersion: CacheStateSchemaVersion, ArtifactType: CacheStateArtifactType,
		RunID: input.RunID, ProtocolDigest: input.ProtocolDigest, Trial: input.Trial, CapturedAt: input.CapturedAt,
		Assurance: assurance(CacheVerificationScope), ObservationMethod: cacheObservationMethod,
		Boundary: cacheBoundary, SnapshotSemantics: cacheSnapshotSemantics, BoundaryWindow: input.BoundaryWindow, RawSource: input.RawSource,
		Mode: input.Mode, TargetRelations: clone(input.TargetRelations), MinResidentPct: cloneFloat(input.MinResidentPct),
		Relations: clone(input.Relations), Reasons: []string{},
	}
	for index := range artifact.Relations {
		if artifact.Relations[index].RelationBlocks > 0 {
			artifact.Relations[index].ResidentPct = percent(artifact.Relations[index].ResidentBlocks, artifact.Relations[index].RelationBlocks)
		}
	}
	status, reasons, err := deriveCacheState(artifact)
	if err != nil {
		return CacheState{}, err
	}
	artifact.Status, artifact.Reasons = status, reasons
	artifact.Digest, err = canonicalDigest(artifact)
	if err != nil {
		return CacheState{}, err
	}
	return artifact, VerifyCacheState(artifact)
}

func NewStatisticsReset(input StatisticsResetInput) (StatisticsReset, error) {
	artifact := StatisticsReset{
		SchemaVersion: StatisticsResetSchemaVersion, ArtifactType: StatisticsResetArtifactType,
		RunID: input.RunID, ProtocolDigest: input.ProtocolDigest, Trial: input.Trial, CapturedAt: input.CapturedAt,
		Assurance: assurance(ResetVerificationScope), PostgresServerMajor: input.PostgresServerMajor, ObservationMethod: resetObservationMethod,
		Policy: input.Policy, Boundary: input.Boundary, BoundaryWindow: input.BoundaryWindow,
		DatabaseBefore: input.DatabaseBefore, DatabaseAfter: input.DatabaseAfter, WALBefore: input.WALBefore, WALAfter: input.WALAfter,
		RawSource: input.RawSource, Operations: clone(input.Operations), Reasons: []string{},
	}
	status, reasons, err := deriveStatisticsReset(artifact)
	if err != nil {
		return StatisticsReset{}, err
	}
	artifact.Status, artifact.Reasons = status, reasons
	artifact.Digest, err = canonicalDigest(artifact)
	if err != nil {
		return StatisticsReset{}, err
	}
	return artifact, VerifyStatisticsReset(artifact)
}

func NewCollectorOverhead(input CollectorOverheadInput) (CollectorOverhead, error) {
	artifact := CollectorOverhead{
		SchemaVersion: CollectorOverheadSchemaVersion, ArtifactType: CollectorOverheadArtifactType,
		RunID: input.RunID, ProtocolDigest: input.ProtocolDigest, Trial: input.Trial, CapturedAt: input.CapturedAt,
		Assurance: assurance(OverheadVerificationScope), Collector: overheadCollector, TimingSource: overheadTimingSource,
		CalibrationWindow: input.CalibrationWindow, RawSource: input.RawSource,
		Mode: input.Mode, IntervalNS: input.IntervalNS, RequiredSamples: input.RequiredSamples,
		MaxDutyCyclePct: cloneFloat(input.MaxDutyCyclePct), Samples: clone(input.Samples), Reasons: []string{},
	}
	mean, maximum, status, reasons, err := deriveCollectorOverhead(artifact)
	if err != nil {
		return CollectorOverhead{}, err
	}
	artifact.ObservedMeanDutyPct, artifact.ObservedMaxDutyPct = mean, maximum
	artifact.Status, artifact.Reasons = status, reasons
	artifact.Digest, err = canonicalDigest(artifact)
	if err != nil {
		return CollectorOverhead{}, err
	}
	return artifact, VerifyCollectorOverhead(artifact)
}

func NewResourceBudget(input ResourceBudgetInput) (ResourceBudget, error) {
	artifact := ResourceBudget{
		SchemaVersion: ResourceBudgetSchemaVersion, ArtifactType: ResourceBudgetArtifactType,
		RunID: input.RunID, ProtocolDigest: input.ProtocolDigest, Trial: input.Trial, CapturedAt: input.CapturedAt,
		Assurance: assurance(ResourceVerificationScope), Mode: input.Mode, Scope: input.Scope, Provider: input.Provider,
		EnforcementWindow: input.EnforcementWindow, RawSource: input.RawSource,
		ProviderConstraints: clone(input.ProviderConstraints), CPUMillicores: cloneInt(input.CPUMillicores), MemoryMiB: cloneInt(input.MemoryMiB),
		ObservedDockerNanoCPUs: cloneInt64(input.ObservedDockerNanoCPUs), ObservedDockerMemoryBytes: cloneInt64(input.ObservedDockerMemoryBytes),
		CgroupVersion: input.CgroupVersion, PostgresContainerIDDigest: input.PostgresContainerIDDigest, PgbenchContainerIDDigest: input.PgbenchContainerIDDigest,
		Reasons: []string{},
	}
	if input.Mode == ResourceModeRunnerEnforced {
		artifact.InspectSource = resourceInspectSource
		if input.CPUMillicores != nil && *input.CPUMillicores > 0 && int64(*input.CPUMillicores) <= math.MaxInt64/1_000_000 {
			value := int64(*input.CPUMillicores) * 1_000_000
			artifact.ExpectedDockerNanoCPUs = &value
		}
		if input.MemoryMiB != nil && *input.MemoryMiB > 0 && int64(*input.MemoryMiB) <= math.MaxInt64/(1024*1024) {
			value := int64(*input.MemoryMiB) * 1024 * 1024
			artifact.ExpectedDockerMemoryBytes = &value
		}
	} else {
		artifact.InspectSource = "not-applicable"
	}
	status, reasons, err := deriveResourceBudget(artifact)
	if err != nil {
		return ResourceBudget{}, err
	}
	artifact.Status, artifact.Reasons = status, reasons
	artifact.Digest, err = canonicalDigest(artifact)
	if err != nil {
		return ResourceBudget{}, err
	}
	return artifact, VerifyResourceBudget(artifact)
}

func VerifyCacheState(artifact CacheState) error {
	issues := validateCommon(artifact.SchemaVersion, CacheStateSchemaVersion, artifact.ArtifactType, CacheStateArtifactType, artifact.RunID, artifact.ProtocolDigest, artifact.Trial, artifact.CapturedAt, artifact.Assurance, CacheVerificationScope)
	if artifact.ObservationMethod != cacheObservationMethod {
		issues = append(issues, "unsupported cache observation method")
	}
	if artifact.Boundary != cacheBoundary || artifact.SnapshotSemantics != cacheSnapshotSemantics {
		issues = append(issues, "cache boundary or snapshot semantics do not match protocol v2")
	}
	issues = append(issues, validateSourceEvidence(artifact.RawSource, CacheStateSourceFile)...)
	issues = append(issues, validateWindow("cache boundary", artifact.CapturedAt, artifact.BoundaryWindow)...)
	status, reasons, err := deriveCacheState(artifact)
	issues = appendDerivedIssues(issues, err, artifact.Status, status, artifact.Reasons, reasons)
	return finishVerification(issues, artifact.Digest, artifact)
}

func VerifyStatisticsReset(artifact StatisticsReset) error {
	issues := validateCommon(artifact.SchemaVersion, StatisticsResetSchemaVersion, artifact.ArtifactType, StatisticsResetArtifactType, artifact.RunID, artifact.ProtocolDigest, artifact.Trial, artifact.CapturedAt, artifact.Assurance, ResetVerificationScope)
	if artifact.ObservationMethod != resetObservationMethod || !oneOf(artifact.PostgresServerMajor, "15", "16", "17", "18", "19") {
		issues = append(issues, "statistics reset observation method or PostgreSQL major is unsupported")
	}
	issues = append(issues, validateSourceEvidence(artifact.RawSource, StatisticsResetSourceFile)...)
	issues = append(issues, validateWindow("statistics reset boundary", artifact.CapturedAt, artifact.BoundaryWindow)...)
	status, reasons, err := deriveStatisticsReset(artifact)
	issues = appendDerivedIssues(issues, err, artifact.Status, status, artifact.Reasons, reasons)
	return finishVerification(issues, artifact.Digest, artifact)
}

func VerifyCollectorOverhead(artifact CollectorOverhead) error {
	issues := validateCommon(artifact.SchemaVersion, CollectorOverheadSchemaVersion, artifact.ArtifactType, CollectorOverheadArtifactType, artifact.RunID, artifact.ProtocolDigest, artifact.Trial, artifact.CapturedAt, artifact.Assurance, OverheadVerificationScope)
	if artifact.Collector != overheadCollector || artifact.TimingSource != overheadTimingSource {
		issues = append(issues, "collector identity or timing source does not match protocol v2")
	}
	issues = append(issues, validateSourceEvidence(artifact.RawSource, CollectorOverheadSourceFile)...)
	issues = append(issues, validateWindow("collector calibration", artifact.CapturedAt, artifact.CalibrationWindow)...)
	mean, maximum, status, reasons, err := deriveCollectorOverhead(artifact)
	if err == nil && (!almostEqual(artifact.ObservedMeanDutyPct, mean) || !almostEqual(artifact.ObservedMaxDutyPct, maximum)) {
		issues = append(issues, "observed duty-cycle summaries do not match independently derived samples")
	}
	issues = appendDerivedIssues(issues, err, artifact.Status, status, artifact.Reasons, reasons)
	return finishVerification(issues, artifact.Digest, artifact)
}

func VerifyResourceBudget(artifact ResourceBudget) error {
	issues := validateCommon(artifact.SchemaVersion, ResourceBudgetSchemaVersion, artifact.ArtifactType, ResourceBudgetArtifactType, artifact.RunID, artifact.ProtocolDigest, artifact.Trial, artifact.CapturedAt, artifact.Assurance, ResourceVerificationScope)
	issues = append(issues, validateSourceEvidence(artifact.RawSource, ResourceBudgetSourceFile)...)
	issues = append(issues, validateWindow("resource enforcement", artifact.CapturedAt, artifact.EnforcementWindow)...)
	status, reasons, err := deriveResourceBudget(artifact)
	issues = appendDerivedIssues(issues, err, artifact.Status, status, artifact.Reasons, reasons)
	return finishVerification(issues, artifact.Digest, artifact)
}

func ParseCacheState(content []byte) (CacheState, error) { return parseStrict[CacheState](content) }
func ParseStatisticsReset(content []byte) (StatisticsReset, error) {
	return parseStrict[StatisticsReset](content)
}
func ParseCollectorOverhead(content []byte) (CollectorOverhead, error) {
	return parseStrict[CollectorOverhead](content)
}
func ParseResourceBudget(content []byte) (ResourceBudget, error) {
	return parseStrict[ResourceBudget](content)
}

func WriteCacheState(path string, artifact CacheState) error {
	return writeVerified(path, artifact, VerifyCacheState)
}
func WriteStatisticsReset(path string, artifact StatisticsReset) error {
	return writeVerified(path, artifact, VerifyStatisticsReset)
}
func WriteCollectorOverhead(path string, artifact CollectorOverhead) error {
	return writeVerified(path, artifact, VerifyCollectorOverhead)
}
func WriteResourceBudget(path string, artifact ResourceBudget) error {
	return writeVerified(path, artifact, VerifyResourceBudget)
}

func CacheControlSatisfied(artifact CacheState) bool {
	return artifact.Status == CacheStatusUncontrolled || artifact.Status == CacheStatusSatisfied
}
func StatisticsResetSatisfied(artifact StatisticsReset) bool {
	return artifact.Status == StatisticsStatusNotRequested || artifact.Status == StatisticsStatusSucceeded
}
func CollectorOverheadSatisfied(artifact CollectorOverhead) bool {
	return artifact.Status == OverheadStatusIncluded || artifact.Status == OverheadStatusWithinBudget
}
func ResourceBudgetSatisfied(artifact ResourceBudget) bool {
	return artifact.Status == ResourceStatusUnbounded || artifact.Status == ResourceStatusEnforced
}

func VerifyBinding(got, expected Binding) error {
	if got.RunID != expected.RunID || got.ProtocolDigest != expected.ProtocolDigest || got.Trial != expected.Trial {
		return fmt.Errorf("control evidence binding does not match expected run, protocol, and trial")
	}
	return nil
}

func CacheStateBinding(artifact CacheState) Binding {
	return Binding{RunID: artifact.RunID, ProtocolDigest: artifact.ProtocolDigest, Trial: artifact.Trial}
}
func StatisticsResetBinding(artifact StatisticsReset) Binding {
	return Binding{RunID: artifact.RunID, ProtocolDigest: artifact.ProtocolDigest, Trial: artifact.Trial}
}
func CollectorOverheadBinding(artifact CollectorOverhead) Binding {
	return Binding{RunID: artifact.RunID, ProtocolDigest: artifact.ProtocolDigest, Trial: artifact.Trial}
}
func ResourceBudgetBinding(artifact ResourceBudget) Binding {
	return Binding{RunID: artifact.RunID, ProtocolDigest: artifact.ProtocolDigest, Trial: artifact.Trial}
}

// VerifyWindowWithin binds a control action window to its independently
// verified phase-journal window. Equality at phase edges is allowed.
func VerifyWindowWithin(control, phase BoundaryWindow) error {
	for label, window := range map[string]BoundaryWindow{"control": control, "phase": phase} {
		if !canonicalUTC(window.StartedAt) || !canonicalUTC(window.FinishedAt) {
			return fmt.Errorf("%s window is not canonical UTC", label)
		}
	}
	controlStart, _ := time.Parse(time.RFC3339Nano, control.StartedAt)
	controlFinish, _ := time.Parse(time.RFC3339Nano, control.FinishedAt)
	phaseStart, _ := time.Parse(time.RFC3339Nano, phase.StartedAt)
	phaseFinish, _ := time.Parse(time.RFC3339Nano, phase.FinishedAt)
	if controlFinish.Before(controlStart) || phaseFinish.Before(phaseStart) || controlStart.Before(phaseStart) || controlFinish.After(phaseFinish) {
		return fmt.Errorf("control window falls outside independently verified phase window")
	}
	return nil
}

func deriveCacheState(artifact CacheState) (string, []string, error) {
	if artifact.TargetRelations == nil || artifact.Relations == nil || artifact.Reasons == nil {
		return "", nil, fmt.Errorf("cache arrays must be present")
	}
	if artifact.Mode == CacheModeUncontrolled {
		if len(artifact.TargetRelations) != 0 || artifact.MinResidentPct != nil || len(artifact.Relations) != 0 {
			return "", nil, fmt.Errorf("uncontrolled cache evidence must omit targets, threshold, and relation observations")
		}
		return CacheStatusUncontrolled, []string{}, nil
	}
	if artifact.Mode != CacheModeWarm {
		return "", nil, fmt.Errorf("unsupported cache mode %q", artifact.Mode)
	}
	if len(artifact.TargetRelations) > maxControlRows || !sortedUniqueRelations(artifact.TargetRelations) || artifact.MinResidentPct == nil || !finitePercentageAboveZero(*artifact.MinResidentPct) {
		return "", nil, fmt.Errorf("warm cache evidence requires sorted unique targets and a minimum resident percentage in (0,100]")
	}
	if len(artifact.Relations) != len(artifact.TargetRelations) {
		return "", nil, fmt.Errorf("cache relation observations do not cover every target exactly once")
	}
	reasons := []string{}
	var databaseOID uint32
	seenRelationOIDs := map[uint32]struct{}{}
	for index, observation := range artifact.Relations {
		if observation.Relation != artifact.TargetRelations[index] || observation.DatabaseOID == 0 || observation.RelationOID == 0 || observation.Fork != "main" || observation.RelationBlocks == 0 || observation.ResidentBlocks > observation.RelationBlocks {
			return "", nil, fmt.Errorf("invalid cache relation observation at index %d", index)
		}
		if index == 0 {
			databaseOID = observation.DatabaseOID
		} else if observation.DatabaseOID != databaseOID {
			return "", nil, fmt.Errorf("cache target relations do not share one database identity")
		}
		if _, exists := seenRelationOIDs[observation.RelationOID]; exists {
			return "", nil, fmt.Errorf("cache relation OID is duplicated")
		}
		seenRelationOIDs[observation.RelationOID] = struct{}{}
		want := percent(observation.ResidentBlocks, observation.RelationBlocks)
		if !finite(observation.ResidentPct) || !almostEqual(observation.ResidentPct, want) {
			return "", nil, fmt.Errorf("cache resident percentage does not match block counts for %s", observation.Relation)
		}
		if want+1e-12 < *artifact.MinResidentPct {
			reasons = append(reasons, "cache-residency-below-minimum:"+observation.Relation)
		}
	}
	if len(reasons) > 0 {
		return CacheStatusUnsatisfied, reasons, nil
	}
	return CacheStatusSatisfied, reasons, nil
}

func deriveStatisticsReset(artifact StatisticsReset) (string, []string, error) {
	if artifact.Operations == nil || artifact.Reasons == nil {
		return "", nil, fmt.Errorf("statistics reset arrays must be present")
	}
	if artifact.Policy == StatisticsPolicyNone {
		if artifact.Boundary != "none" || len(artifact.Operations) != 0 || !unavailableResetTimestamp(artifact.DatabaseBefore) || !unavailableResetTimestamp(artifact.DatabaseAfter) || !unavailableResetTimestamp(artifact.WALBefore) || !unavailableResetTimestamp(artifact.WALAfter) {
			return "", nil, fmt.Errorf("statistics policy none requires boundary none, unavailable timestamps, and no operations")
		}
		return StatisticsStatusNotRequested, []string{}, nil
	}
	if artifact.Policy != StatisticsPolicyRunnerManaged || !oneOf(artifact.Boundary, "before-trial", "before-warmup", "before-measure") {
		return "", nil, fmt.Errorf("runner-managed statistics reset requires an explicit pre-phase boundary")
	}
	if len(artifact.Operations) != len(expectedResetOperations) {
		return "", nil, fmt.Errorf("runner-managed statistics reset must record both exact reset operations")
	}
	reasons := []string{}
	for _, pair := range []struct {
		scope         string
		before, after ResetTimestampObservation
	}{
		{"current-database", artifact.DatabaseBefore, artifact.DatabaseAfter},
		{"cluster-wal", artifact.WALBefore, artifact.WALAfter},
	} {
		advanced, timestampErr := resetTimestampAdvanced(pair.before, pair.after, artifact.BoundaryWindow, artifact.CapturedAt)
		if timestampErr != nil {
			return "", nil, fmt.Errorf("%s reset timestamp evidence: %w", pair.scope, timestampErr)
		}
		if !advanced {
			reasons = append(reasons, "statistics-reset-timestamp-not-advanced:"+pair.scope)
		}
	}
	for index, operation := range artifact.Operations {
		expected := expectedResetOperations[index]
		if operation.Function != expected.Function || operation.Scope != expected.Scope || operation.Rows < 0 || operation.Rows > 1 {
			return "", nil, fmt.Errorf("invalid statistics reset operation at index %d", index)
		}
		if operation.Rows != 1 || !operation.CommandCompleted {
			reasons = append(reasons, "statistics-reset-operation-failed:"+operation.Scope)
		}
	}
	if len(reasons) > 0 {
		return StatisticsStatusFailed, reasons, nil
	}
	return StatisticsStatusSucceeded, reasons, nil
}

func unavailableResetTimestamp(observation ResetTimestampObservation) bool {
	return observation.Availability == ObservationUnavailable && observation.Value == ""
}

func resetTimestampAdvanced(before, after ResetTimestampObservation, window BoundaryWindow, capturedAt string) (bool, error) {
	if after.Availability != ObservationAvailable || !canonicalUTC(after.Value) {
		return false, fmt.Errorf("after timestamp must be observed canonical UTC")
	}
	afterTime, _ := time.Parse(time.RFC3339Nano, after.Value)
	windowStart, startErr := time.Parse(time.RFC3339Nano, window.StartedAt)
	windowFinish, finishErr := time.Parse(time.RFC3339Nano, window.FinishedAt)
	captured, capturedErr := time.Parse(time.RFC3339Nano, capturedAt)
	if startErr != nil || finishErr != nil || capturedErr != nil || afterTime.Before(windowStart) || afterTime.After(windowFinish) || afterTime.After(captured) {
		return false, fmt.Errorf("after timestamp falls outside the reset boundary or after capture")
	}
	switch before.Availability {
	case ObservationNull:
		if before.Value != "" {
			return false, fmt.Errorf("null before timestamp must omit value")
		}
		return true, nil
	case ObservationAvailable:
		if !canonicalUTC(before.Value) {
			return false, fmt.Errorf("before timestamp is not canonical UTC")
		}
		beforeTime, _ := time.Parse(time.RFC3339Nano, before.Value)
		return afterTime.After(beforeTime), nil
	default:
		return false, fmt.Errorf("before timestamp must be observed or null")
	}
}

func deriveCollectorOverhead(artifact CollectorOverhead) (float64, float64, string, []string, error) {
	if artifact.Samples == nil || artifact.Reasons == nil || artifact.IntervalNS <= 0 {
		return 0, 0, "", nil, fmt.Errorf("collector overhead requires present arrays and a positive interval")
	}
	if artifact.Mode == OverheadModeIncludedUnquantified {
		if artifact.RequiredSamples != 0 || artifact.MaxDutyCyclePct != nil || len(artifact.Samples) != 0 || artifact.ObservedMeanDutyPct != 0 || artifact.ObservedMaxDutyPct != 0 {
			return 0, 0, "", nil, fmt.Errorf("included-unquantified overhead must omit calibration values")
		}
		return 0, 0, OverheadStatusIncluded, []string{}, nil
	}
	if artifact.Mode != OverheadModeRunnerCalibrated || artifact.RequiredSamples <= 0 || artifact.RequiredSamples > MaxCollectorOverheadSamples || len(artifact.Samples) > MaxCollectorOverheadSamples || artifact.MaxDutyCyclePct == nil || !finitePercentageAboveZero(*artifact.MaxDutyCyclePct) {
		return 0, 0, "", nil, fmt.Errorf("runner-calibrated overhead requires at most %d rows, a bounded positive required count, and a maximum duty percentage", MaxCollectorOverheadSamples)
	}
	total, maximum := float64(0), float64(0)
	reasons := []string{}
	if len(artifact.Samples) < artifact.RequiredSamples {
		reasons = append(reasons, "collector-sample-count-below-required")
	}
	windowStart, _ := time.Parse(time.RFC3339Nano, artifact.CalibrationWindow.StartedAt)
	windowFinish, _ := time.Parse(time.RFC3339Nano, artifact.CalibrationWindow.FinishedAt)
	var previousScheduled time.Time
	for index, sample := range artifact.Samples {
		scheduled, scheduledErr := time.Parse(time.RFC3339Nano, sample.ScheduledAt)
		started, startedErr := time.Parse(time.RFC3339Nano, sample.StartedAt)
		finished, finishedErr := time.Parse(time.RFC3339Nano, sample.FinishedAt)
		if sample.Sequence != index+1 || sample.DurationNS < 0 || scheduledErr != nil || startedErr != nil || finishedErr != nil ||
			!canonicalUTC(sample.ScheduledAt) || !canonicalUTC(sample.StartedAt) || !canonicalUTC(sample.FinishedAt) ||
			started.Before(scheduled) || finished.Before(started) || scheduled.Before(windowStart) || finished.After(windowFinish) ||
			index > 0 && scheduled.Sub(previousScheduled) != time.Duration(artifact.IntervalNS) || !oneOf(sample.Status, "succeeded", "failed") {
			return 0, 0, "", nil, fmt.Errorf("invalid collector overhead sample at index %d", index)
		}
		previousScheduled = scheduled
		duty := float64(sample.DurationNS) * 100 / float64(artifact.IntervalNS)
		if !finite(duty) {
			return 0, 0, "", nil, fmt.Errorf("collector duty cycle is not finite")
		}
		total += duty
		maximum = math.Max(maximum, duty)
		if sample.Status != "succeeded" {
			reasons = append(reasons, fmt.Sprintf("collector-sample-failed:%d", sample.Sequence))
		}
	}
	mean := float64(0)
	if len(artifact.Samples) > 0 {
		mean = total / float64(len(artifact.Samples))
	}
	if len(reasons) > 0 {
		return mean, maximum, OverheadStatusInvalidSamples, reasons, nil
	}
	if maximum > *artifact.MaxDutyCyclePct+1e-12 {
		return mean, maximum, OverheadStatusExceededBudget, []string{"collector-duty-cycle-exceeds-maximum"}, nil
	}
	return mean, maximum, OverheadStatusWithinBudget, []string{}, nil
}

func deriveResourceBudget(artifact ResourceBudget) (string, []string, error) {
	if artifact.ProviderConstraints == nil || artifact.Reasons == nil {
		return "", nil, fmt.Errorf("resource budget arrays must be present")
	}
	if artifact.Mode == ResourceModeUnbounded {
		if artifact.Scope != "" || artifact.Provider != "" || len(artifact.ProviderConstraints) != 0 || artifact.InspectSource != "not-applicable" ||
			artifact.CPUMillicores != nil || artifact.MemoryMiB != nil || artifact.ExpectedDockerNanoCPUs != nil || artifact.ExpectedDockerMemoryBytes != nil || artifact.ObservedDockerNanoCPUs != nil || artifact.ObservedDockerMemoryBytes != nil ||
			artifact.CgroupVersion != "" || artifact.PostgresContainerIDDigest != "" || artifact.PgbenchContainerIDDigest != "" {
			return "", nil, fmt.Errorf("unbounded resource evidence must omit enforcement declarations and observations")
		}
		return ResourceStatusUnbounded, []string{}, nil
	}
	if artifact.Mode != ResourceModeRunnerEnforced || artifact.Scope != ResourceScope || artifact.Provider != ResourceProvider || !slices.Equal(artifact.ProviderConstraints, resourceProviderConstraints) || artifact.InspectSource != resourceInspectSource {
		return "", nil, fmt.Errorf("runner-enforced resource evidence has unsupported scope, provider, constraints, or inspect source")
	}
	if artifact.CPUMillicores == nil || *artifact.CPUMillicores <= 0 || int64(*artifact.CPUMillicores) > math.MaxInt64/1_000_000 || artifact.MemoryMiB == nil || *artifact.MemoryMiB <= 0 || int64(*artifact.MemoryMiB) > math.MaxInt64/(1024*1024) {
		return "", nil, fmt.Errorf("runner-enforced resource evidence requires bounded positive CPU millicores and memory MiB")
	}
	wantCPU := int64(*artifact.CPUMillicores) * 1_000_000
	wantMemory := int64(*artifact.MemoryMiB) * 1024 * 1024
	if artifact.ExpectedDockerNanoCPUs == nil || *artifact.ExpectedDockerNanoCPUs != wantCPU || artifact.ExpectedDockerMemoryBytes == nil || *artifact.ExpectedDockerMemoryBytes != wantMemory {
		return "", nil, fmt.Errorf("expected Docker limits do not match requested CPU and memory budget")
	}
	if artifact.ObservedDockerNanoCPUs == nil || *artifact.ObservedDockerNanoCPUs < 0 || artifact.ObservedDockerMemoryBytes == nil || *artifact.ObservedDockerMemoryBytes < 0 {
		return "", nil, fmt.Errorf("runner-enforced resource evidence requires non-negative Docker limit observations")
	}
	if !evidence.IsDigest(artifact.PostgresContainerIDDigest) || !evidence.IsDigest(artifact.PgbenchContainerIDDigest) {
		return "", nil, fmt.Errorf("runner-enforced resource evidence requires digested container identities")
	}
	reasons := []string{}
	if *artifact.ObservedDockerNanoCPUs != wantCPU {
		reasons = append(reasons, "cpu-limit-mismatch")
	}
	if *artifact.ObservedDockerMemoryBytes != wantMemory {
		reasons = append(reasons, "memory-limit-mismatch")
	}
	if artifact.CgroupVersion != "2" {
		reasons = append(reasons, "cgroup-version-mismatch")
	}
	if artifact.PostgresContainerIDDigest != artifact.PgbenchContainerIDDigest {
		reasons = append(reasons, "container-scope-mismatch")
	}
	if len(reasons) > 0 {
		return ResourceStatusMismatch, reasons, nil
	}
	return ResourceStatusEnforced, reasons, nil
}

func assurance(scope string) Assurance {
	return Assurance{EvidenceOrigin: EvidenceOriginRunnerRecorded, Signed: false, DigestPurpose: DigestPurposeIntegrityOnly, VerificationScope: scope}
}

func validateCommon(schema, wantSchema, artifactType, wantType, runID, protocolDigest string, trial int, capturedAt string, gotAssurance Assurance, scope string) []string {
	issues := []string{}
	if schema != wantSchema {
		issues = append(issues, fmt.Sprintf("schema_version = %q, want %q", schema, wantSchema))
	}
	if artifactType != wantType {
		issues = append(issues, fmt.Sprintf("artifact_type = %q, want %q", artifactType, wantType))
	}
	if !validRunID(runID) {
		issues = append(issues, "run_id is not a portable benchmark run identifier")
	}
	if !evidence.IsDigest(protocolDigest) {
		issues = append(issues, "protocol_digest is not a lowercase sha256 digest")
	}
	if trial <= 0 {
		issues = append(issues, "trial must be positive")
	}
	if !canonicalUTC(capturedAt) {
		issues = append(issues, "captured_at must be canonical UTC RFC3339Nano")
	}
	if gotAssurance != assurance(scope) {
		issues = append(issues, "assurance does not match the bounded unsigned control-evidence contract")
	}
	return issues
}

func validateWindow(label, capturedAt string, window BoundaryWindow) []string {
	if !canonicalUTC(window.StartedAt) || !canonicalUTC(window.FinishedAt) {
		return []string{label + " window must use canonical UTC RFC3339Nano timestamps"}
	}
	started, _ := time.Parse(time.RFC3339Nano, window.StartedAt)
	finished, _ := time.Parse(time.RFC3339Nano, window.FinishedAt)
	captured, err := time.Parse(time.RFC3339Nano, capturedAt)
	if finished.Before(started) {
		return []string{label + " window finishes before it starts"}
	}
	if err != nil || captured.Before(started) || captured.After(finished) {
		return []string{"captured_at falls outside " + label + " window"}
	}
	return nil
}

func validRunID(value string) bool {
	if len(value) == 0 || len(value) > 200 || !asciiAlnum(value[0]) {
		return false
	}
	for index := range value {
		character := value[index]
		if asciiAlnum(character) || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func asciiAlnum(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func appendDerivedIssues(issues []string, deriveErr error, gotStatus, wantStatus string, gotReasons, wantReasons []string) []string {
	if deriveErr != nil {
		return append(issues, "cannot independently derive control status: "+deriveErr.Error())
	}
	if gotStatus != wantStatus {
		issues = append(issues, fmt.Sprintf("status = %q, want independently derived %q", gotStatus, wantStatus))
	}
	if !slices.Equal(gotReasons, wantReasons) {
		issues = append(issues, "reasons do not match independently derived reasons")
	}
	return issues
}

func finishVerification(issues []string, digest string, artifact any) error {
	if !evidence.IsDigest(digest) {
		issues = append(issues, "digest is not a lowercase sha256 digest")
	} else if want, err := canonicalDigest(artifact); err != nil {
		issues = append(issues, "cannot recompute canonical digest: "+err.Error())
	} else if digest != want {
		issues = append(issues, fmt.Sprintf("digest mismatch: got %s want %s", digest, want))
	}
	if len(issues) == 0 {
		return nil
	}
	sort.Strings(issues)
	errs := make([]error, len(issues))
	for index, issue := range issues {
		errs[index] = errors.New(issue)
	}
	return errors.Join(errs...)
}

func canonicalDigest(artifact any) (string, error) {
	content, err := json.Marshal(artifact)
	if err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return "", err
	}
	if _, exists := fields["digest"]; !exists {
		return "", fmt.Errorf("artifact has no digest field")
	}
	delete(fields, "digest")
	canonical, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(canonical), nil
}

func parseStrict[T any](content []byte) (T, error) {
	var zero T
	if len(content) == 0 || len(content) > maxArtifactBytes {
		return zero, fmt.Errorf("artifact size must be between 1 and %d bytes", maxArtifactBytes)
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return zero, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var artifact T
	if err := decoder.Decode(&artifact); err != nil {
		return zero, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return zero, fmt.Errorf("unexpected trailing JSON value")
		}
		return zero, err
	}
	return artifact, nil
}

func rejectDuplicateKeys(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil {
			return err
		}
		if closeToken != json.Delim('}') {
			return fmt.Errorf("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil {
			return err
		}
		if closeToken != json.Delim(']') {
			return fmt.Errorf("unterminated JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func writeVerified[T any](path string, artifact T, verify func(T) error) error {
	if path == "" || path == "-" {
		return fmt.Errorf("output must be a file path")
	}
	if err := verify(artifact); err != nil {
		return fmt.Errorf("refuse to write invalid control evidence: %w", err)
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".benchmark-control-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(artifact); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("publish benchmark control evidence: %w", err)
	}
	return nil
}

func canonicalUTC(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z")
}

func sortedUniqueRelations(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for index, value := range values {
		if !qualifiedIdentifier(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func qualifiedIdentifier(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for index := range part {
			character := part[index]
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_' || index > 0 && character >= '0' && character <= '9' {
				continue
			}
			return false
		}
	}
	return true
}

func finite(value float64) bool                    { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finitePercentageAboveZero(value float64) bool { return finite(value) && value > 0 && value <= 100 }
func percent(numerator, denominator uint64) float64 {
	return float64(numerator) * 100 / float64(denominator)
}
func almostEqual(left, right float64) bool {
	return finite(left) && finite(right) && math.Abs(left-right) <= 1e-9*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}
func oneOf(value string, allowed ...string) bool { return slices.Contains(allowed, value) }
func clone[T any](values []T) []T                { return append([]T{}, values...) }
func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
