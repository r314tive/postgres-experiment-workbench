package benchmarkartifact

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksettings"
	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchlog"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
)

type VerifyResult struct {
	Dir    string               `json:"dir"`
	Valid  bool                 `json:"valid"`
	Issues []string             `json:"issues"`
	Series *benchmarkrun.Series `json:"series,omitempty"`
	Plan   *benchmarkplan.Plan  `json:"-"`
}

func (r VerifyResult) IsValid() bool { return len(r.Issues) == 0 }

func Resolve(root string, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates,
			filepath.Join(root, input),
			filepath.Join(root, "runs", "benchmarks", input),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate)
		if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return filepath.Abs(candidate)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("benchmark series directory not found: %s", input)
}

func Load(root string, input string) (benchmarkrun.Series, error) {
	dir, err := Resolve(root, input)
	if err != nil {
		return benchmarkrun.Series{}, err
	}
	var series benchmarkrun.Series
	if err := decodeStrict(filepath.Join(dir, "result.json"), &series); err != nil {
		return benchmarkrun.Series{}, err
	}
	series.ArtifactDir = dir
	return series, nil
}

func Verify(root string, input string) (VerifyResult, error) {
	dir, err := Resolve(root, input)
	if err != nil {
		return VerifyResult{}, err
	}
	result := VerifyResult{Dir: dir, Issues: []string{}}
	for _, name := range []string{"benchmark-spec.env", "plan.json", "result.json", "runs.tsv", "summary.md"} {
		checkRegular(&result, filepath.Join(dir, name), name, true)
	}
	checkDirectory(&result, filepath.Join(dir, "trials"), "trials")
	checkDirectory(&result, filepath.Join(dir, "driver-logs"), "driver-logs")
	checkDirectory(&result, filepath.Join(dir, "protocol"), "protocol")

	series, err := Load(root, dir)
	if err != nil {
		addIssue(&result, "result.json parse failed: %v", err)
		result.Valid = false
		return result, nil
	}
	result.Series = &series
	checkSeriesIdentity(&result, dir, series)
	checkScenarioPackInventory(&result, dir, series)
	checkSeriesChronology(&result, series)
	checkSummary(&result, dir, series)
	checkTrialInventory(&result, dir, series)

	var plan benchmarkplan.Plan
	planValid := false
	if err := decodeStrict(filepath.Join(dir, "plan.json"), &plan); err != nil {
		addIssue(&result, "plan.json parse failed: %v", err)
	} else {
		result.Plan = &plan
		planValid = true
		checkProtocolInputs(&result, dir, plan, series)
		if values, parseErr := envfile.Parse(filepath.Join(dir, "benchmark-spec.env")); parseErr != nil {
			addIssue(&result, "benchmark spec snapshot parse failed: %v", parseErr)
		} else if declarationErr := benchmarkplan.VerifySpecDeclarations(plan, values); declarationErr != nil {
			addIssue(&result, "plan declarations do not match benchmark spec snapshot: %v", declarationErr)
		}
		if err := benchmarkplan.VerifyDigests(plan); err != nil {
			addIssue(&result, "plan identity verification failed: %v", err)
		}
		if plan.Spec != series.Benchmark {
			addIssue(&result, "plan benchmark does not match result.json")
		}
		if plan.ProtocolDigest != series.ProtocolDigest {
			addIssue(&result, "plan protocol digest does not match result.json")
		}
		if plan.ComparisonKeyDigest != series.ComparisonKeyDigest {
			addIssue(&result, "plan comparison key digest does not match result.json")
		}
		if plan.MaxCVPct != series.MaxCVPct || plan.ResetPolicy != series.ResetPolicy || !equalOptionalFloat(plan.RegressionThresholdPct, series.RegressionThresholdPct) {
			addIssue(&result, "plan decision policy does not match result.json")
		}
	}

	rows, err := readRunsTSV(filepath.Join(dir, "runs.tsv"))
	if err != nil {
		addIssue(&result, "runs.tsv parse failed: %v", err)
	}
	if len(rows) != len(series.Trials) {
		addIssue(&result, "runs.tsv rows %d do not match result trials %d", len(rows), len(series.Trials))
	} else {
		for index, row := range rows {
			trial := series.Trials[index]
			primaryValue := ""
			if trial.PrimaryValue != nil {
				primaryValue = strconv.FormatFloat(*trial.PrimaryValue, 'g', -1, 64)
			}
			if row.Trial != trial.Trial || row.RunID != trial.RunID || row.Status != trial.Status || row.PrimaryValue != primaryValue || row.RunRef != trial.RunRef {
				addIssue(&result, "runs.tsv row %d does not match result trial", index+2)
			}
		}
	}

	artifactRoot := inferArtifactRoot(root, dir)
	nativeToolchainDigest := checkProtocolNativeToolchain(&result, dir, series)
	values := make([]float64, 0, len(series.Trials))
	valid, failed, invalid := 0, 0, 0
	seenRunIDs := make(map[string]struct{}, len(series.Trials))
	var trialPlan *benchmarkplan.Plan
	if planValid {
		trialPlan = &plan
	}
	for index, trial := range series.Trials {
		if _, exists := seenRunIDs[trial.RunID]; exists {
			addIssue(&result, "duplicate trial run id: %s", trial.RunID)
		}
		seenRunIDs[trial.RunID] = struct{}{}
		checkTrial(&result, artifactRoot, dir, index+1, series, trial, trialPlan, nativeToolchainDigest)
		switch trial.Status {
		case "passed":
			valid++
			if trial.PrimaryValue != nil {
				values = append(values, *trial.PrimaryValue)
			}
		case "failed":
			failed++
		case "invalid":
			invalid++
		default:
			addIssue(&result, "trial %d has unsupported status %q", index+1, trial.Status)
		}
	}
	if valid != series.TrialsValid || failed != series.TrialsFailed || invalid != series.TrialsInvalid {
		addIssue(&result, "trial status totals do not match result.json")
	}
	if planValid {
		if series.TrialsPlanned != plan.Trials || len(series.Trials) > plan.Trials {
			addIssue(&result, "recorded/planned trial counts do not match the benchmark protocol")
		}
		derivedStatus, derivedStats, _, deriveErr := benchmarkrun.EvaluateSeries(plan, series)
		if deriveErr != nil {
			addIssue(&result, "derive series policy: %v", deriveErr)
		} else {
			if series.Status != derivedStatus {
				addIssue(&result, "series status %q does not match independently derived status %q", series.Status, derivedStatus)
			}
			if (series.Stats == nil) != (derivedStats == nil) || series.Stats != nil && derivedStats != nil && !equalStats(*series.Stats, *derivedStats) {
				addIssue(&result, "series aggregate does not match independently derived policy")
			}
		}
	} else if len(series.Trials) > series.TrialsPlanned {
		addIssue(&result, "recorded trials exceed planned trials")
	}
	if len(values) >= 2 {
		stats, err := pgbenchresult.Summarize(values)
		if err != nil {
			addIssue(&result, "recompute statistics: %v", err)
		} else if series.Stats == nil || !equalStats(stats, *series.Stats) {
			addIssue(&result, "aggregate statistics do not match trial primary values")
		}
	} else if len(values) == 0 && series.Stats != nil {
		addIssue(&result, "statistics present without valid trial values")
	} else if len(values) == 1 && series.Stats != nil {
		addIssue(&result, "one-trial smoke series must not claim sample statistics")
	}
	checkSeriesEnvironment(&result, dir, series)
	result.Valid = result.IsValid()
	return result, nil
}

func checkScenarioPackInventory(result *VerifyResult, dir string, series benchmarkrun.Series) {
	path := filepath.Join(dir, filepath.FromSlash(benchmarkrun.ScenarioPackInventoryRef))
	if series.ScenarioPack == nil {
		if _, err := os.Lstat(path); err == nil {
			addIssue(result, "scenario-pack inventory exists without result.json identity")
		} else if !os.IsNotExist(err) {
			addIssue(result, "scenario-pack inventory stat failed: %v", err)
		}
		return
	}
	identity := series.ScenarioPack
	if identity.ID == "" || identity.Version == "" || !evidence.IsDigest(identity.Digest) ||
		identity.InventoryRef != benchmarkrun.ScenarioPackInventoryRef || !evidence.IsDigest(identity.InventoryDigest) {
		addIssue(result, "result.json scenario-pack identity is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		addIssue(result, "configured scenario-pack inventory is missing, empty, or unsafe")
		return
	}
	if digest, digestErr := evidence.DigestFile(path); digestErr != nil || digest != identity.InventoryDigest {
		addIssue(result, "scenario-pack inventory file digest does not match result.json")
	}
	var inventory benchmarkrun.ScenarioPackInventory
	if err := decodeStrict(path, &inventory); err != nil {
		addIssue(result, "scenario-pack inventory parse failed: %v", err)
		return
	}
	if inventory.SchemaVersion != benchmarkrun.ScenarioPackSchemaVersion || inventory.ArtifactType != benchmarkrun.ScenarioPackArtifactType {
		addIssue(result, "scenario-pack inventory has unsupported schema or artifact type")
	}
	if inventory.ID != identity.ID || inventory.Version != identity.Version || inventory.Digest != identity.Digest ||
		inventory.ID != inventory.Manifest.ID || inventory.Version != inventory.Manifest.Version {
		addIssue(result, "scenario-pack inventory identity does not match result.json and retained manifest")
	}
	if err := scenariopack.VerifyInventory(inventory.Manifest, inventory.Files, inventory.Digest); err != nil {
		addIssue(result, "scenario-pack inventory verification failed: %v", err)
	}
}

func checkSummary(result *VerifyResult, dir string, series benchmarkrun.Series) {
	content, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		addIssue(result, "summary.md cannot be read: %v", err)
		return
	}
	if !bytes.Equal(content, benchmarkrun.SummaryBytes(series)) {
		addIssue(result, "summary.md does not match independently rendered result.json")
	}
}

func checkTrialInventory(result *VerifyResult, dir string, series benchmarkrun.Series) {
	entries, err := os.ReadDir(filepath.Join(dir, "trials"))
	if err != nil {
		addIssue(result, "trials directory cannot be read: %v", err)
		return
	}
	if len(entries) != len(series.Trials) {
		addIssue(result, "trials directory has %d entries, want %d", len(entries), len(series.Trials))
	}
	expected := make(map[string]struct{}, len(series.Trials))
	for index := range series.Trials {
		expected[fmt.Sprintf("%03d.json", index+1)] = struct{}{}
	}
	for _, entry := range entries {
		_, known := expected[entry.Name()]
		if !known || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			addIssue(result, "trials directory entry %q is unexpected or unsafe", entry.Name())
			continue
		}
		delete(expected, entry.Name())
	}
	for name := range expected {
		addIssue(result, "trials directory is missing %q", name)
	}
}

func checkSeriesChronology(result *VerifyResult, series benchmarkrun.Series) {
	started, startErr := parseCanonicalUTC(series.StartedAt)
	finished, finishErr := parseCanonicalUTC(series.FinishedAt)
	if startErr != nil || finishErr != nil {
		addIssue(result, "series timestamps must be canonical UTC RFC3339Nano")
		return
	}
	if finished.Before(started) {
		addIssue(result, "series finishes before it starts")
		return
	}
	previousFinish := started
	for index, trial := range series.Trials {
		trialStarted, trialStartErr := parseCanonicalUTC(trial.StartedAt)
		trialFinished, trialFinishErr := parseCanonicalUTC(trial.FinishedAt)
		if trialStartErr != nil || trialFinishErr != nil {
			addIssue(result, "trial %d timestamps must be canonical UTC RFC3339Nano", index+1)
			continue
		}
		if trialFinished.Before(trialStarted) {
			addIssue(result, "trial %d finishes before it starts", index+1)
			continue
		}
		if trialStarted.Before(started) || trialFinished.After(finished) {
			addIssue(result, "trial %d interval is outside the series interval", index+1)
		}
		if trialStarted.Before(previousFinish) {
			addIssue(result, "trial %d overlaps or precedes the previous trial", index+1)
		}
		previousFinish = trialFinished
	}
}

func parseCanonicalUTC(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("non-canonical timestamp %q", value)
	}
	return parsed, nil
}

func checkProtocolInputs(result *VerifyResult, seriesDir string, plan benchmarkplan.Plan, series benchmarkrun.Series) {
	if plan.ProtocolSchemaVersion != benchmarkplan.ProtocolSchemaVersion &&
		plan.ProtocolSchemaVersion != benchmarkplan.ProtocolSchemaVersionV2 {
		addIssue(result, "unsupported benchmark protocol schema: %q", plan.ProtocolSchemaVersion)
	}
	if plan.Name != series.Name || plan.Class != series.Class || plan.Driver != series.Driver ||
		plan.Target != series.Target || plan.TargetEndpointContract != series.TargetEndpointContract || plan.TargetTopology != series.TargetTopology {
		addIssue(result, "plan benchmark metadata does not match result.json")
	}
	paths := []struct {
		label    string
		actual   string
		expected string
		digest   string
		snapshot string
	}{
		{"benchmark spec", plan.SpecPath, filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env")), plan.SpecDigest, filepath.Join(seriesDir, "benchmark-spec.env")},
		{"experiment spec", plan.ExperimentPath, filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(plan.ExperimentSpec)+".env")), plan.ExperimentDigest, filepath.Join(seriesDir, "protocol", "experiment-spec.env")},
		{"workload spec", plan.WorkloadPath, filepath.ToSlash(filepath.Join("workloads", filepath.FromSlash(plan.WorkloadSpec)+".env")), plan.WorkloadDigest, filepath.Join(seriesDir, "protocol", "workload-spec.env")},
		{"PostgreSQL config", plan.PGConfigPath, filepath.ToSlash(filepath.Join("configs", filepath.FromSlash(plan.PGConfig), "postgresql.conf")), plan.PGConfigDigest, filepath.Join(seriesDir, "protocol", "postgresql.conf")},
		{"benchmark target topology", plan.TargetTopologyPath, filepath.ToSlash(filepath.Join("topologies", filepath.FromSlash(plan.TargetTopology)+".env")), plan.TargetTopologyDigest, filepath.Join(seriesDir, "protocol", "target-topology.env")},
	}
	for _, item := range paths {
		if !evidence.IsPortablePath(item.actual) || item.actual != item.expected {
			addIssue(result, "plan %s path is not canonical", item.label)
		}
		checkSnapshotDigest(result, item.snapshot, item.label, item.digest)
	}
	workloadSnapshot := filepath.Join(seriesDir, "protocol", "workload-spec.env")
	if values, err := envfile.Parse(workloadSnapshot); err != nil {
		addIssue(result, "workload spec snapshot cannot be parsed: %v", err)
	} else if err := benchmarkplan.VerifyWorkloadDeclarations(plan, values); err != nil {
		addIssue(result, "workload spec snapshot does not match plan: %v", err)
	}
	workloadScriptSnapshot := filepath.Join(seriesDir, "protocol", "workload-script.sql")
	if plan.WorkloadScript == "" {
		if _, err := os.Lstat(workloadScriptSnapshot); !os.IsNotExist(err) {
			addIssue(result, "workload script snapshot exists for a protocol without a script")
		}
	} else {
		if !evidence.IsPortablePath(plan.WorkloadScript) {
			addIssue(result, "plan workload script path is not portable")
		}
		checkSnapshotDigest(result, workloadScriptSnapshot, "workload script", plan.WorkloadScriptDigest)
	}
	checkProtocolCapsule(result, seriesDir, plan)
}

func checkProtocolCapsule(result *VerifyResult, seriesDir string, plan benchmarkplan.Plan) {
	capsuleRoot := filepath.Join(seriesDir, "protocol", "capsule")
	info, err := os.Lstat(capsuleRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		addIssue(result, "immutable protocol capsule is missing or unsafe")
		return
	}
	expected := map[string]string{
		filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env")):              plan.SpecDigest,
		filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(plan.ExperimentSpec)+".env")):   plan.ExperimentDigest,
		filepath.ToSlash(filepath.Join("workloads", filepath.FromSlash(plan.WorkloadSpec)+".env")):       plan.WorkloadDigest,
		filepath.ToSlash(filepath.Join("configs", filepath.FromSlash(plan.PGConfig), "postgresql.conf")): plan.PGConfigDigest,
		filepath.ToSlash(filepath.Join("topologies", filepath.FromSlash(plan.TargetTopology)+".env")):    plan.TargetTopologyDigest,
	}
	if plan.WorkloadScript != "" {
		expected[plan.WorkloadScript] = plan.WorkloadScriptDigest
	}
	seen := make(map[string]struct{}, len(expected))
	walkErr := filepath.WalkDir(capsuleRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(capsuleRoot, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			addIssue(result, "immutable protocol capsule contains symlink: %s", relative)
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		want, ok := expected[relative]
		if !ok {
			addIssue(result, "immutable protocol capsule contains unexpected file: %s", relative)
			return nil
		}
		if !entry.Type().IsRegular() {
			addIssue(result, "immutable protocol capsule input is not a regular file: %s", relative)
			return nil
		}
		digest, digestErr := evidence.DigestFile(path)
		if digestErr != nil || digest != want {
			addIssue(result, "immutable protocol capsule digest mismatch: %s", relative)
		}
		seen[relative] = struct{}{}
		return nil
	})
	if walkErr != nil {
		addIssue(result, "immutable protocol capsule cannot be inspected: %v", walkErr)
	}
	for relative := range expected {
		if _, ok := seen[relative]; !ok {
			addIssue(result, "immutable protocol capsule input is missing: %s", relative)
		}
	}
}

func checkSnapshotDigest(result *VerifyResult, path string, label string, expected string) {
	if !evidence.IsDigest(expected) {
		addIssue(result, "plan %s digest is invalid", label)
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		addIssue(result, "%s snapshot is missing or unsafe", label)
		return
	}
	digest, err := evidence.DigestFile(path)
	if err != nil || digest != expected {
		addIssue(result, "%s snapshot digest mismatch", label)
	}
}

func checkSeriesIdentity(result *VerifyResult, dir string, series benchmarkrun.Series) {
	if series.SchemaVersion != benchmarkrun.SeriesSchemaVersion {
		addIssue(result, "unsupported result schema: %q", series.SchemaVersion)
	}
	if series.ArtifactType != benchmarkrun.SeriesArtifactType {
		addIssue(result, "unsupported result artifact type: %q", series.ArtifactType)
	}
	if !benchmarkrun.ValidRunID(series.RunID) || series.RunID != filepath.Base(dir) {
		addIssue(result, "result run id must match directory basename")
	}
	if series.RunDir != "." {
		addIssue(result, "result run_dir must be portable root '.'")
	}
	if !evidence.IsDigest(series.SpecDigest) || !evidence.IsDigest(series.ProtocolDigest) || !evidence.IsDigest(series.ComparisonKeyDigest) {
		addIssue(result, "result contains an invalid identity digest")
	}
	if series.EngineBinaryRef != benchmarkrun.EngineBinarySeriesRef || !evidence.IsDigest(series.EngineBinaryDigest) {
		addIssue(result, "result benchmark engine identity is invalid")
	} else {
		enginePath := filepath.Join(dir, filepath.FromSlash(series.EngineBinaryRef))
		info, statErr := os.Lstat(enginePath)
		digest, digestErr := evidence.DigestFile(enginePath)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || digestErr != nil || digest != series.EngineBinaryDigest {
			addIssue(result, "benchmark engine binary snapshot digest or type mismatch")
		}
	}
	if digest, err := evidence.DigestFile(filepath.Join(dir, "benchmark-spec.env")); err != nil || digest != series.SpecDigest {
		addIssue(result, "benchmark spec snapshot digest does not match result.json")
	}
	expectedRef := filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(series.Benchmark)+".env"))
	if series.SpecRef != expectedRef {
		addIssue(result, "benchmark spec ref does not match benchmark id")
	}
	if series.Status != "passed" && series.Status != "failed" && series.Status != "invalid" && series.Status != "inconclusive" {
		addIssue(result, "unsupported series status: %q", series.Status)
	}
}

func checkTrial(result *VerifyResult, artifactRoot string, seriesDir string, number int, series benchmarkrun.Series, trial benchmarkrun.Trial, plan *benchmarkplan.Plan, nativeToolchainDigest string) {
	if trial.SchemaVersion != benchmarkrun.TrialSchemaVersion || trial.ArtifactType != benchmarkrun.TrialArtifactType {
		addIssue(result, "trial %d has unsupported schema or artifact type", number)
	}
	if trial.Trial != number {
		addIssue(result, "trial sequence mismatch at position %d", number)
	}
	if trial.PrimaryMetric != series.PrimaryMetric {
		addIssue(result, "trial %d primary metric differs from series", number)
	}
	if !benchmarkrun.ValidRunID(trial.RunID) {
		addIssue(result, "trial %d has invalid run id", number)
	}
	expectedRunRef := filepath.ToSlash(filepath.Join("runs", trial.RunID))
	if !evidence.IsPortablePath(trial.RunRef) || trial.RunRef != expectedRunRef {
		addIssue(result, "trial %d has non-canonical run_ref", number)
	}
	trialPath := filepath.Join(seriesDir, "trials", fmt.Sprintf("%03d.json", number))
	var onDisk benchmarkrun.Trial
	if err := decodeStrict(trialPath, &onDisk); err != nil {
		addIssue(result, "trial %d artifact parse failed: %v", number, err)
	} else {
		left, _ := json.Marshal(trial)
		right, _ := json.Marshal(onDisk)
		if string(left) != string(right) {
			addIssue(result, "trial %d artifact does not match result.json", number)
		}
	}
	runDir, joinErr := safeExistingJoin(artifactRoot, trial.RunRef)
	if joinErr != nil {
		addIssue(result, "trial %d linked run path is unsafe: %v", number, joinErr)
		return
	}
	verification, err := runverify.Verify(artifactRoot, runDir)
	if err != nil {
		addIssue(result, "trial %d linked run verification failed: %v", number, err)
	} else if !verification.Valid() {
		addIssue(result, "trial %d linked run is invalid: %s", number, strings.Join(verification.Issues, "; "))
	}
	if plan != nil {
		manifest, manifestErr := envfile.Parse(filepath.Join(runDir, "manifest.env"))
		if manifestErr != nil {
			addIssue(result, "trial %d linked benchmark protocol manifest cannot be parsed: %v", number, manifestErr)
		} else {
			if bindErr := benchmarkrun.ValidateLinkedRunProtocolWithToolchain(*plan, series.Runtime, number, nativeToolchainDigest, manifest); bindErr != nil {
				addIssue(result, "trial %d linked benchmark protocol does not match plan: %v", number, bindErr)
			}
		}
	}
	if err == nil && verification.Verdict != nil {
		checkLinkedVerdict(result, number, trial, *verification.Verdict)
	} else if err == nil {
		addIssue(result, "trial %d linked run has no normalized verdict", number)
	}
	if trial.Status == "passed" {
		checkMetricsCoverage(result, number, trial, verification.Metrics)
	}
	if trial.Status == "passed" || trial.PostgresMetrics != nil {
		checkPostgresMetrics(result, runDir, number, trial, plan)
	}
	if trial.ExperimentVerified && (err != nil || !verification.Valid()) {
		addIssue(result, "trial %d claims verification but linked run is invalid", number)
	}
	summaryPath := ""
	if trial.Summary != nil {
		if resolved, ok := checkArtifactRef(result, runDir, number, "summary", *trial.Summary); ok {
			summaryPath = resolved
			checkNormalizedSummary(result, seriesDir, summaryPath, number, trial, plan)
		}
	} else if trial.Status == "passed" {
		addIssue(result, "trial %d passed without pgbench summary", number)
	}
	rawPaths := make([]string, 0, len(trial.RawLogs))
	for _, ref := range trial.RawLogs {
		if resolved, ok := checkArtifactRef(result, runDir, number, "raw log", ref); ok {
			rawPaths = append(rawPaths, resolved)
		}
	}
	if plan != nil {
		checkTransactionLogs(result, number, trial, *plan, rawPaths)
	}
	checkPhaseTimeline(result, runDir, seriesDir, number, trial, plan)
	checkBenchmarkControls(result, runDir, number, trial, plan)
	if trial.EffectiveSettings != nil {
		checkEffectiveSettings(result, runDir, number, trial)
	}
	if series.Environment != nil && trial.Status == "passed" && trial.EnvironmentDigest != series.Environment.Digest {
		addIssue(result, "trial %d environment digest does not match series", number)
	}
	if trial.Status == "passed" {
		if series.Environment == nil {
			addIssue(result, "trial %d passed without a series benchmark environment", number)
		} else if plan != nil && err == nil && verification.Valid() {
			checkTrialEnvironment(result, runDir, summaryPath, number, trial, *plan, *series.Environment, series.EngineBinaryDigest)
		}
	}
	if trial.Status == "passed" && (trial.Pgbench == nil || trial.PrimaryValue == nil || !trial.ExperimentVerified) {
		addIssue(result, "trial %d passed without complete normalized evidence", number)
	}
}

func checkEffectiveSettings(result *VerifyResult, runDir string, number int, trial benchmarkrun.Trial) {
	recorded := trial.EffectiveSettings
	if recorded == nil {
		return
	}
	if err := benchmarksettings.Verify(*recorded); err != nil {
		addIssue(result, "trial %d effective pg_settings normalization is invalid: %v", number, err)
	}
	if trial.PhaseTimeline == nil || len(trial.PhaseTimeline.Events) < 2 || trial.PhaseJournal == nil {
		addIssue(result, "trial %d effective pg_settings evidence has no linked prepare phase", number)
		return
	}
	prepare := trial.PhaseTimeline.Events[benchmarkphase.PrepareIndex]
	sourcePath, err := safeExistingJoin(runDir, benchmarksettings.SourcePath)
	if err != nil {
		addIssue(result, "trial %d effective pg_settings source is unsafe: %v", number, err)
		return
	}
	derived, err := benchmarksettings.ParseFile(sourcePath, benchmarksettings.Expectation{
		RunID:          trial.RunID,
		ProtocolDigest: recorded.ProtocolDigest,
		Trial:          number,
		Names:          recorded.Names,
		Source:         recorded.Source,
		Phase: benchmarksettings.PhaseBinding{
			Name:          prepare.Name,
			JournalDigest: trial.PhaseJournal.Digest,
			StartedAt:     prepare.StartedAt,
			FinishedAt:    prepare.FinishedAt,
		},
	})
	if err != nil {
		addIssue(result, "trial %d effective pg_settings source cannot be independently parsed: %v", number, err)
		return
	}
	left, leftErr := json.Marshal(recorded)
	right, rightErr := json.Marshal(derived)
	if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
		addIssue(result, "trial %d normalized effective pg_settings do not match the linked raw source", number)
	}
}

func checkPhaseTimeline(result *VerifyResult, runDir string, seriesDir string, number int, trial benchmarkrun.Trial, plan *benchmarkplan.Plan) {
	mirrorPath := filepath.Join(seriesDir, "driver-logs", fmt.Sprintf("trial-%03d-phases.tsv", number))
	info, statErr := os.Lstat(mirrorPath)
	if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		addIssue(result, "trial %d benchmark phase series mirror is missing or unsafe", number)
		return
	}
	mirror, err := os.ReadFile(mirrorPath)
	if err != nil {
		addIssue(result, "trial %d benchmark phase series mirror cannot be opened", number)
		return
	}
	mirrorTimeline, mirrorParseErr := benchmarkphase.ParseTSV(bytes.NewReader(mirror), number, trial.RunID)
	if mirrorParseErr != nil {
		addIssue(result, "trial %d benchmark phase series mirror cannot be independently parsed: %v", number, mirrorParseErr)
		return
	}
	parsed := mirrorTimeline
	canonicalRef := filepath.ToSlash(filepath.Join("artifacts", "benchmark", "phases.tsv"))
	if trial.PhaseJournal == nil {
		if trial.Status == "passed" {
			addIssue(result, "trial %d passed without a linked benchmark phase journal reference", number)
		}
	} else {
		if trial.PhaseJournal.Path != canonicalRef {
			addIssue(result, "trial %d linked benchmark phase journal has non-canonical path", number)
		}
		if primaryPath, ok := checkArtifactRef(result, runDir, number, "phase journal", *trial.PhaseJournal); ok {
			primary, readErr := os.ReadFile(primaryPath)
			if readErr != nil {
				addIssue(result, "trial %d linked benchmark phase journal cannot be read", number)
			} else {
				if !bytes.Equal(primary, mirror) {
					addIssue(result, "trial %d linked benchmark phase journal differs from series mirror", number)
				}
				primaryTimeline, parseErr := benchmarkphase.ParseTSV(bytes.NewReader(primary), number, trial.RunID)
				if parseErr != nil {
					addIssue(result, "trial %d linked benchmark phase journal cannot be independently parsed: %v", number, parseErr)
				} else {
					left, _ := json.Marshal(primaryTimeline)
					right, _ := json.Marshal(mirrorTimeline)
					if string(left) != string(right) {
						addIssue(result, "trial %d linked and mirrored benchmark phase timelines differ", number)
					}
					parsed = primaryTimeline
				}
			}
		}
	}
	if trial.PhaseTimeline == nil {
		addIssue(result, "trial %d has no normalized benchmark phase timeline", number)
		return
	}
	left, _ := json.Marshal(parsed)
	right, _ := json.Marshal(*trial.PhaseTimeline)
	if string(left) != string(right) {
		addIssue(result, "trial %d normalized benchmark phase timeline does not match journal", number)
	}
	if err := benchmarkphase.Verify(*trial.PhaseTimeline); err != nil {
		addIssue(result, "trial %d benchmark phase timeline verification failed: %v", number, err)
	}
	if trial.StartedAt != parsed.StartedAt || trial.FinishedAt != parsed.FinishedAt || trial.DurationMS != parsed.DurationMS {
		addIssue(result, "trial %d interval does not match its normalized benchmark phase timeline", number)
	}
	if trial.Status == "passed" && parsed.Status != "passed" {
		addIssue(result, "trial %d passed despite failed benchmark phase timeline", number)
	}
	if trial.Status == "failed" && parsed.Status != "failed" {
		addIssue(result, "trial %d failed without a failed benchmark phase", number)
	}
	if plan != nil && trial.Status == "passed" {
		warmup := parsed.Events[benchmarkphase.WarmupIndex]
		if plan.WarmupSeconds == 0 && warmup.Status != "skipped" {
			addIssue(result, "trial %d warmup phase was not skipped for a zero-warmup protocol", number)
		}
		if plan.WarmupSeconds > 0 && warmup.Status != "passed" {
			addIssue(result, "trial %d warmup phase did not pass", number)
		}
		if parsed.Events[benchmarkphase.MeasureIndex].Status != "passed" {
			addIssue(result, "trial %d measure phase did not pass", number)
		}
	}
}

func checkLinkedVerdict(result *VerifyResult, number int, trial benchmarkrun.Trial, verdict runstate.Verdict) {
	if verdict.RunID != trial.RunID {
		addIssue(result, "trial %d linked verdict run id mismatch", number)
	}
	if trial.Status == "passed" && verdict.Status != runstate.VerdictStatusPassed {
		addIssue(result, "trial %d passed despite failed linked verdict", number)
	}
	if verdict.Status == runstate.VerdictStatusFailed && trial.Status != "failed" {
		addIssue(result, "trial %d does not retain failed linked verdict outcome", number)
	}
	trialStarted, trialStartErr := time.Parse(time.RFC3339Nano, trial.StartedAt)
	trialFinished, trialFinishErr := time.Parse(time.RFC3339Nano, trial.FinishedAt)
	verdictStarted, verdictStartErr := time.Parse(time.RFC3339Nano, verdict.StartedAt)
	verdictFinished, verdictFinishErr := time.Parse(time.RFC3339Nano, verdict.FinishedAt)
	if trialStartErr != nil || trialFinishErr != nil || verdictStartErr != nil || verdictFinishErr != nil {
		addIssue(result, "trial %d or linked verdict interval is not RFC3339", number)
		return
	}
	if verdictStarted.Before(trialStarted) || verdictFinished.After(trialFinished) {
		addIssue(result, "trial %d lifecycle does not contain the linked verdict interval", number)
	}
}

func checkMetricsCoverage(result *VerifyResult, number int, trial benchmarkrun.Trial, coverage *runverify.MetricsCoverage) {
	if trial.PhaseTimeline == nil {
		return
	}
	if coverage == nil {
		addIssue(result, "trial %d metrics do not expose a valid timestamp extent", number)
		return
	}
	measure, ok := benchmarkphase.EventByName(*trial.PhaseTimeline, benchmarkphase.MeasureName)
	if !ok {
		return
	}
	measureStarted, startErr := time.Parse(time.RFC3339Nano, measure.StartedAt)
	measureFinished, finishErr := time.Parse(time.RFC3339Nano, measure.FinishedAt)
	first, firstErr := time.Parse(time.RFC3339Nano, coverage.First)
	last, lastErr := time.Parse(time.RFC3339Nano, coverage.Last)
	if startErr != nil || finishErr != nil || firstErr != nil || lastErr != nil {
		addIssue(result, "trial %d metrics/measure coverage interval is invalid", number)
		return
	}
	if first.After(measureStarted) || last.Before(measureFinished) {
		addIssue(result, "trial %d metrics samples do not cover the complete measure phase", number)
	}
}

func checkPostgresMetrics(result *VerifyResult, runDir string, number int, trial benchmarkrun.Trial, plan *benchmarkplan.Plan) {
	if trial.PhaseTimeline == nil {
		if trial.Status == "passed" {
			addIssue(result, "trial %d has no measure phase for PostgreSQL sampler normalization", number)
		}
		return
	}
	measure, ok := benchmarkphase.EventByName(*trial.PhaseTimeline, benchmarkphase.MeasureName)
	if !ok {
		if trial.Status == "passed" {
			addIssue(result, "trial %d has no measure phase for PostgreSQL sampler normalization", number)
		}
		return
	}
	if measure.Status != "passed" {
		if trial.PostgresMetrics != nil {
			addIssue(result, "trial %d claims PostgreSQL sampler summary without a passed measure phase", number)
		}
		return
	}
	if trial.PostgresMetrics == nil {
		addIssue(result, "trial %d passed without normalized PostgreSQL sampler summary", number)
	}
	if trial.PostgresMetrics != nil {
		if err := benchmarkmetrics.VerifyDigest(*trial.PostgresMetrics); err != nil {
			addIssue(result, "trial %d PostgreSQL sampler summary verification failed: %v", number, err)
		}
	}
	manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		addIssue(result, "trial %d cannot read linked PostgreSQL server major for sampler normalization: %v", number, err)
		return
	}
	metricsOptions, optionsErr := artifactMetricsOptions(runDir, number, trial, plan, manifest["postgres_server_major"], measure)
	if optionsErr != nil {
		addIssue(result, "trial %d PostgreSQL sampler controls cannot be independently loaded: %v", number, optionsErr)
		return
	}
	derived, err := benchmarkmetrics.DeriveFile(metricsOptions)
	if err != nil {
		addIssue(result, "trial %d PostgreSQL sampler summary cannot be independently derived: %v", number, err)
		return
	}
	if trial.PostgresMetrics == nil {
		return
	}
	recorded, recordedErr := json.Marshal(trial.PostgresMetrics)
	recomputed, recomputedErr := json.Marshal(derived)
	if recordedErr != nil || recomputedErr != nil || string(recorded) != string(recomputed) {
		addIssue(result, "trial %d normalized PostgreSQL sampler summary does not match linked metrics.csv", number)
	}
}

// safeJoin keeps a portable artifact reference lexically below root. Callers
// that consume an existing path must additionally use safeExistingJoin so
// symlink resolution cannot escape the artifact tree.
func safeJoin(root string, ref string) (string, error) {
	if !evidence.IsPortablePath(ref) {
		return "", fmt.Errorf("reference is not a portable relative path: %q", ref)
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined, err := filepath.Abs(filepath.Join(rootPath, filepath.FromSlash(ref)))
	if err != nil {
		return "", err
	}
	if !pathContained(rootPath, joined) {
		return "", fmt.Errorf("reference escapes root: %q", ref)
	}
	return joined, nil
}

func safeExistingJoin(root string, ref string) (string, error) {
	joined, err := safeJoin(root, ref)
	if err != nil {
		return "", err
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", fmt.Errorf("resolve artifact root: %w", err)
	}
	resolvedJoined, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("resolve linked path: %w", err)
	}
	expectedResolved := filepath.Join(resolvedRoot, filepath.FromSlash(ref))
	if !pathContained(resolvedRoot, resolvedJoined) || filepath.Clean(resolvedJoined) != filepath.Clean(expectedResolved) {
		return "", fmt.Errorf("reference resolves through a symlink or outside root: %q", ref)
	}
	return resolvedJoined, nil
}

func pathContained(root string, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func checkTransactionLogs(result *VerifyResult, number int, trial benchmarkrun.Trial, plan benchmarkplan.Plan, rawPaths []string) {
	if plan.LogTransactions && trial.Status == "passed" && len(rawPaths) == 0 {
		addIssue(result, "trial %d passed without protocol-required raw transaction logs", number)
	}
	if len(rawPaths) == 0 {
		if trial.TransactionLog != nil {
			addIssue(result, "trial %d has normalized transaction-log evidence without raw logs", number)
		}
		return
	}

	parsed, err := pgbenchlog.ParseFiles(rawPaths, pgbenchlog.Options{
		SampleRate:  plan.LogSampleRate,
		ScheduleLag: plan.Rate != nil,
		Retries:     plan.MaxTries != nil && *plan.MaxTries != 1,
	})
	if err != nil {
		addIssue(result, "trial %d raw transaction logs cannot be independently parsed: %v", number, err)
		return
	}
	if trial.TransactionLog == nil {
		addIssue(result, "trial %d has no normalized transaction-log result", number)
		return
	}
	left, _ := json.Marshal(parsed)
	right, _ := json.Marshal(*trial.TransactionLog)
	if string(left) != string(right) {
		addIssue(result, "trial %d normalized transaction-log result does not match raw logs", number)
	}
	if trial.Status != "passed" {
		return
	}
	if trial.Pgbench != nil {
		if err := benchmarkrun.ValidateTransactionLog(*trial.Pgbench, parsed); err != nil {
			addIssue(result, "trial %d raw transaction log semantic validation failed: %v", number, err)
		}
	}
	if trial.PhaseTimeline == nil || len(trial.PhaseTimeline.Events) != len(benchmarkphase.OrderedNames) {
		addIssue(result, "trial %d raw transaction logs have no complete phase timeline", number)
		return
	}
	measure := trial.PhaseTimeline.Events[benchmarkphase.MeasureIndex]
	measureStarted, startErr := time.Parse(time.RFC3339Nano, measure.StartedAt)
	measureFinished, finishErr := time.Parse(time.RFC3339Nano, measure.FinishedAt)
	if startErr != nil || finishErr != nil {
		addIssue(result, "trial %d measure timestamps cannot bind raw transaction logs", number)
		return
	}
	if err := pgbenchlog.ValidateCompletionWindow(parsed, measureStarted, measureFinished); err != nil {
		addIssue(result, "trial %d raw transaction log phase containment failed: %v", number, err)
	}
}

func checkNormalizedSummary(result *VerifyResult, seriesDir string, summaryPath string, number int, trial benchmarkrun.Trial, plan *benchmarkplan.Plan) {
	if trial.Summary == nil {
		return
	}
	file, err := os.Open(summaryPath)
	if err != nil {
		return
	}
	parsed, parseErr := pgbenchresult.Parse(file)
	closeErr := file.Close()
	if parseErr != nil || closeErr != nil {
		addIssue(result, "trial %d pgbench summary cannot be independently parsed", number)
		return
	}
	if trial.Pgbench == nil {
		addIssue(result, "trial %d has no normalized pgbench result", number)
		return
	}
	left, _ := json.Marshal(parsed)
	right, _ := json.Marshal(*trial.Pgbench)
	if string(left) != string(right) {
		addIssue(result, "trial %d normalized pgbench result does not match raw summary", number)
	}
	if plan != nil {
		if err := benchmarkrun.ValidatePgbenchResult(*plan, parsed); err != nil {
			addIssue(result, "trial %d pgbench protocol validation failed: %v", number, err)
		}
	}
	phasePath := filepath.Join(seriesDir, "driver-logs", fmt.Sprintf("trial-%03d-phases.tsv", number))
	phaseFile, phaseOpenErr := os.Open(phasePath)
	if phaseOpenErr == nil {
		phaseTimeline, phaseParseErr := benchmarkphase.ParseTSV(phaseFile, number, trial.RunID)
		phaseCloseErr := phaseFile.Close()
		if phaseParseErr == nil && phaseCloseErr == nil {
			if err := pgbenchresult.ValidateTPSIntegrity(parsed, time.Duration(phaseTimeline.Events[benchmarkphase.MeasureIndex].DurationNS)); err != nil {
				addIssue(result, "trial %d pgbench TPS integrity failed: %v", number, err)
			}
		}
	}
	var value float64
	switch trial.PrimaryMetric {
	case "pgbench.tps":
		if parsed.TPSExcludingConnections == nil {
			addIssue(result, "trial %d primary TPS is absent from raw summary", number)
			return
		}
		value = *parsed.TPSExcludingConnections
	case "pgbench.latency_mean_us":
		value = parsed.LatencyMeanMS * 1000
	default:
		addIssue(result, "trial %d has unsupported primary metric %q", number, trial.PrimaryMetric)
		return
	}
	if trial.PrimaryValue == nil || !equalOptionalFloat(&value, trial.PrimaryValue) {
		addIssue(result, "trial %d primary value does not match raw pgbench summary", number)
	}
}

func checkArtifactRef(result *VerifyResult, root string, trial int, label string, ref benchmarkrun.ArtifactRef) (string, bool) {
	if !evidence.IsPortablePath(ref.Path) || !evidence.IsDigest(ref.Digest) || ref.Size <= 0 {
		addIssue(result, "trial %d %s reference is invalid", trial, label)
		return "", false
	}
	path, joinErr := safeExistingJoin(root, ref.Path)
	if joinErr != nil {
		addIssue(result, "trial %d %s path is unsafe: %v", trial, label, joinErr)
		return "", false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		addIssue(result, "trial %d %s file is missing or unsafe: %s", trial, label, ref.Path)
		return "", false
	}
	valid := true
	if info.Size() != ref.Size {
		addIssue(result, "trial %d %s size mismatch: %s", trial, label, ref.Path)
		valid = false
	}
	digest, err := evidence.DigestFile(path)
	if err != nil || digest != ref.Digest {
		addIssue(result, "trial %d %s digest mismatch: %s", trial, label, ref.Path)
		valid = false
	}
	return path, valid
}

// checkProtocolNativeToolchain verifies the retained native executable bytes
// independently of the optional benchmark environment. A series that fails
// before runtime fingerprinting has no environment population, but its linked
// run still binds PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST and must remain
// independently verifiable.
func checkProtocolNativeToolchain(result *VerifyResult, seriesDir string, series benchmarkrun.Series) string {
	snapshotDir := filepath.Join(seriesDir, filepath.Dir(filepath.FromSlash(benchmarkrun.NativeToolchainSeriesRef)))
	if series.Runtime != "native" {
		if _, err := os.Lstat(snapshotDir); err == nil {
			addIssue(result, "Docker benchmark protocol contains native toolchain snapshot")
		} else if !os.IsNotExist(err) {
			addIssue(result, "native toolchain snapshot stat failed: %v", err)
		}
		return ""
	}
	manifest, err := nativetoolchain.VerifySnapshot(snapshotDir, "")
	if err != nil {
		addIssue(result, "native benchmark protocol toolchain snapshot verification failed: %v", err)
		return ""
	}
	if series.Environment != nil && series.Environment.NativeToolchainDigest != manifest.Digest {
		addIssue(result, "native benchmark environment toolchain digest differs from protocol snapshot")
	}
	return manifest.Digest
}

func checkSeriesEnvironment(result *VerifyResult, dir string, series benchmarkrun.Series) {
	path := filepath.Join(dir, "environment.json")
	if series.Environment == nil {
		for _, trial := range series.Trials {
			if trial.Status == "passed" {
				addIssue(result, "result.json has passed trials but no benchmark environment")
				break
			}
		}
		if _, err := os.Lstat(path); err == nil {
			addIssue(result, "environment.json exists without result.json environment")
		} else if !os.IsNotExist(err) {
			addIssue(result, "environment.json stat failed: %v", err)
		}
		return
	}

	checkEnvironment(result, "result.json environment", *series.Environment)
	if series.Runtime != series.Environment.Runtime {
		addIssue(result, "result.json runtime does not match benchmark environment")
	}
	if series.Driver != series.Environment.Driver {
		addIssue(result, "result.json driver does not match benchmark environment")
	}
	if series.EngineBinaryDigest != series.Environment.EngineBinaryDigest {
		addIssue(result, "result.json benchmark engine digest does not match benchmark environment")
	}
	if series.Target != series.Environment.Target || series.TargetEndpointContract != series.Environment.TargetEndpointContract || series.TargetTopology != series.Environment.TargetTopology {
		addIssue(result, "result.json target does not match benchmark environment")
	}
	if series.ScenarioPack == nil {
		if series.Environment.PackID != "" || series.Environment.PackVersion != "" || series.Environment.PackDigest != "" {
			addIssue(result, "benchmark environment claims scenario-pack identity without a retained series inventory")
		}
	} else if series.Environment.PackID != series.ScenarioPack.ID || series.Environment.PackVersion != series.ScenarioPack.Version || series.Environment.PackDigest != series.ScenarioPack.Digest {
		addIssue(result, "benchmark environment scenario-pack identity does not match retained series inventory")
	}
	dimension := series.Environment.SubjectDimension
	if dimension == "" {
		dimension = "pg_config"
	}
	if !reflect.DeepEqual(series.AllowedDifferences, []string{dimension}) {
		addIssue(result, "result.json allowed subject difference does not match benchmark environment")
	}
	if series.Environment.Runtime == "native" {
		artifactRoot := inferArtifactRoot("", dir)
		manifestPath, joinErr := safeExistingJoin(artifactRoot, series.Environment.NativeToolchainManifestRef)
		if joinErr != nil || filepath.Base(manifestPath) != nativetoolchain.ManifestName {
			addIssue(result, "native toolchain manifest reference is unsafe: %v", joinErr)
		} else if _, verifyErr := nativetoolchain.VerifySnapshot(filepath.Dir(manifestPath), series.Environment.NativeToolchainDigest); verifyErr != nil {
			addIssue(result, "native toolchain snapshot verification failed: %v", verifyErr)
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		addIssue(result, "environment.json is missing, empty, or unsafe")
		return
	}
	var onDisk benchmarkrun.Environment
	if err := decodeStrict(path, &onDisk); err != nil {
		addIssue(result, "environment.json parse failed: %v", err)
		return
	}
	checkEnvironment(result, "environment.json", onDisk)
	left, leftErr := json.Marshal(onDisk)
	right, rightErr := json.Marshal(*series.Environment)
	if leftErr != nil || rightErr != nil || string(left) != string(right) {
		addIssue(result, "environment.json does not fully match result.json environment")
	}
}

func checkEnvironment(result *VerifyResult, label string, environment benchmarkrun.Environment) {
	if environment.SchemaVersion != benchmarkrun.EnvironmentSchemaVersion || environment.ArtifactType != benchmarkrun.EnvironmentArtifactType {
		addIssue(result, "%s has unsupported benchmark environment schema or artifact type", label)
	}
	if environment.Runtime != "docker" && environment.Runtime != "native" {
		addIssue(result, "%s runtime must be docker or native", label)
	}
	if !validEnvironmentToken(environment.RuntimeOS) || !validEnvironmentToken(environment.RuntimeArch) {
		addIssue(result, "%s runtime OS or architecture is invalid", label)
	}
	if environment.Driver != "pgbench" || environment.DriverVersion == "" {
		addIssue(result, "%s driver identity is invalid", label)
	}
	topology, endpointContract, targetErr := benchmarkplan.TargetContract(environment.Target)
	if targetErr != nil || environment.TargetTopology != topology || environment.TargetEndpointContract != endpointContract {
		addIssue(result, "%s benchmark target identity is invalid", label)
	}
	if environment.TargetEndpointPort < 1 || environment.TargetEndpointPort > 65535 || environment.TargetEndpointHost == "" || strings.ContainsAny(environment.TargetEndpointHost, " \t\r\n") {
		addIssue(result, "%s measured target endpoint is invalid", label)
	} else if environment.Runtime == "docker" {
		wantHost := "127.0.0.1"
		if environment.Target == benchmarkplan.TargetPgBouncer {
			wantHost = "pgbouncer"
		}
		if environment.TargetEndpointHost != wantHost || environment.TargetEndpointPort != 5432 {
			addIssue(result, "%s Docker measured endpoint does not match the owned target", label)
		}
	} else if (environment.TargetEndpointHost != "127.0.0.1" && environment.TargetEndpointHost != "localhost") || environment.TargetEndpointPort < 1024 {
		addIssue(result, "%s native measured endpoint is not an owned loopback endpoint", label)
	}
	if environment.ParserVersion != pgbenchresult.ParserVersion {
		addIssue(result, "%s parser version does not match the verifier", label)
	}
	versionNum, versionErr := strconv.Atoi(environment.PostgresServerVersionNum)
	if versionErr != nil || versionNum < 10000 || strconv.Itoa(versionNum) != environment.PostgresServerVersionNum {
		addIssue(result, "%s PostgreSQL server_version_num is invalid", label)
	} else if environment.PostgresServerMajor != postgresEnvironmentMajor(versionNum) {
		addIssue(result, "%s PostgreSQL major does not match server_version_num", label)
	}
	if !evidence.IsPortablePath(environment.PGConfig) || !evidence.IsDigest(environment.PGConfigDigest) {
		addIssue(result, "%s PostgreSQL configuration identity is invalid", label)
	}
	if environment.SubjectDimension != "pg_config" && environment.SubjectDimension != "native_toolchain" {
		addIssue(result, "%s subject dimension is invalid", label)
	}
	if environment.Runtime == "native" {
		if environment.DockerDriverImageID != "not-applicable" || environment.DockerTargetImageID != "not-applicable" {
			addIssue(result, "%s native runtime contains Docker image identity", label)
		}
		if !evidence.IsDigest(environment.NativeToolchainDigest) || !evidence.IsPortablePath(environment.NativeToolchainManifestRef) || environment.NativeToolchainProvenance != nativetoolchain.Unattested {
			addIssue(result, "%s native runtime toolchain identity is invalid", label)
		}
	} else {
		if !evidence.IsDigest(environment.DockerDriverImageID) || !evidence.IsDigest(environment.DockerTargetImageID) {
			addIssue(result, "%s Docker runtime image identity is invalid", label)
		}
		if environment.SubjectDimension == "native_toolchain" || environment.NativeToolchainDigest != "" || environment.NativeToolchainManifestRef != "" || environment.NativeToolchainProvenance != "not-applicable" {
			addIssue(result, "%s Docker runtime contains native toolchain identity", label)
		}
	}
	if !runstate.IsEngineVersion(environment.EngineVersion) || !runstate.IsEngineCommit(environment.EngineCommit) || !evidence.IsDigest(environment.EngineBinaryDigest) {
		addIssue(result, "%s engine identity is invalid", label)
	}
	packConfigured := environment.PackID != "" || environment.PackVersion != "" || environment.PackDigest != ""
	if packConfigured && (environment.PackID == "" || environment.PackVersion == "" || !evidence.IsDigest(environment.PackDigest)) {
		addIssue(result, "%s scenario-pack identity is incomplete", label)
	}
	if environment.Qualification != "unqualified-local" {
		addIssue(result, "%s qualification must be unqualified-local", label)
	}
	if !evidence.IsDigest(environment.Digest) {
		addIssue(result, "%s digest is invalid", label)
		return
	}
	copy := environment
	copy.Digest = ""
	content, err := json.Marshal(copy)
	if err != nil || evidence.DigestBytes(content) != environment.Digest {
		addIssue(result, "%s digest does not match environment fields", label)
	}
}

func checkTrialEnvironment(result *VerifyResult, runDir string, summaryPath string, number int, trial benchmarkrun.Trial, plan benchmarkplan.Plan, recorded benchmarkrun.Environment, engineBinaryDigest string) {
	manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		addIssue(result, "trial %d cannot independently derive benchmark environment from linked manifest: %v", number, err)
		return
	}
	if trial.Summary == nil || summaryPath == "" {
		addIssue(result, "trial %d cannot independently derive benchmark environment without a safe pgbench summary", number)
		return
	}
	file, err := os.Open(summaryPath)
	if err != nil {
		addIssue(result, "trial %d cannot open pgbench summary for environment derivation: %v", number, err)
		return
	}
	parsed, parseErr := pgbenchresult.Parse(file)
	closeErr := file.Close()
	if parseErr != nil || closeErr != nil {
		addIssue(result, "trial %d cannot parse pgbench summary for environment derivation: %v", number, errors.Join(parseErr, closeErr))
		return
	}
	if err := pgbenchresult.ValidateServerMajor(parsed, manifest["postgres_server_major"]); err != nil {
		addIssue(result, "trial %d pgbench server identity does not match linked manifest: %v", number, err)
	}
	targetEvidence, targetEvidenceErr := benchmarkrun.ReadTargetEvidence(filepath.Join(runDir, "stdout.log"))
	if targetEvidenceErr != nil {
		addIssue(result, "trial %d benchmark target evidence is invalid: %v", number, targetEvidenceErr)
		return
	}
	if targetEvidence.Target != plan.Target || targetEvidence.EndpointContract != plan.TargetEndpointContract {
		addIssue(result, "trial %d benchmark target evidence does not match the protocol", number)
	}
	expected := benchmarkrun.Environment{
		SchemaVersion:              benchmarkrun.EnvironmentSchemaVersion,
		ArtifactType:               benchmarkrun.EnvironmentArtifactType,
		Runtime:                    manifest["runtime"],
		RuntimeOS:                  manifest["runtime_os"],
		RuntimeArch:                manifest["runtime_arch"],
		Driver:                     plan.Driver,
		Target:                     plan.Target,
		TargetEndpointContract:     plan.TargetEndpointContract,
		TargetEndpointHost:         targetEvidence.Host,
		TargetEndpointPort:         targetEvidence.Port,
		DockerDriverImageID:        targetEvidence.DriverImageID,
		DockerTargetImageID:        targetEvidence.TargetImageID,
		TargetTopology:             plan.TargetTopology,
		DriverVersion:              parsed.PgbenchVersion,
		ParserVersion:              pgbenchresult.ParserVersion,
		PostgresServerVersionNum:   manifest["postgres_server_version_num"],
		PostgresServerMajor:        manifest["postgres_server_major"],
		PGConfig:                   plan.PGConfig,
		PGConfigDigest:             plan.PGConfigDigest,
		SubjectDimension:           recorded.SubjectDimension,
		NativeToolchainDigest:      recorded.NativeToolchainDigest,
		NativeToolchainManifestRef: recorded.NativeToolchainManifestRef,
		NativeToolchainProvenance:  recorded.NativeToolchainProvenance,
		EngineVersion:              manifest["engine_version"],
		EngineCommit:               manifest["engine_commit"],
		EngineBinaryDigest:         engineBinaryDigest,
		PackID:                     manifest["pack_id"],
		PackVersion:                manifest["pack_version"],
		PackDigest:                 manifest["pack_digest"],
		Qualification:              "unqualified-local",
	}
	digestView := expected
	content, marshalErr := json.Marshal(digestView)
	if marshalErr != nil {
		addIssue(result, "trial %d cannot digest independently derived benchmark environment: %v", number, marshalErr)
		return
	}
	expected.Digest = evidence.DigestBytes(content)

	fields := []struct {
		name           string
		recorded, want string
	}{
		{"schema_version", recorded.SchemaVersion, expected.SchemaVersion},
		{"artifact_type", recorded.ArtifactType, expected.ArtifactType},
		{"runtime", recorded.Runtime, expected.Runtime},
		{"runtime_os", recorded.RuntimeOS, expected.RuntimeOS},
		{"runtime_arch", recorded.RuntimeArch, expected.RuntimeArch},
		{"driver", recorded.Driver, expected.Driver},
		{"target", recorded.Target, expected.Target},
		{"target_endpoint_contract", recorded.TargetEndpointContract, expected.TargetEndpointContract},
		{"target_endpoint_host", recorded.TargetEndpointHost, expected.TargetEndpointHost},
		{"docker_driver_image_id", recorded.DockerDriverImageID, expected.DockerDriverImageID},
		{"docker_target_image_id", recorded.DockerTargetImageID, expected.DockerTargetImageID},
		{"target_topology", recorded.TargetTopology, expected.TargetTopology},
		{"driver_version", recorded.DriverVersion, expected.DriverVersion},
		{"parser_version", recorded.ParserVersion, expected.ParserVersion},
		{"postgres_server_version_num", recorded.PostgresServerVersionNum, expected.PostgresServerVersionNum},
		{"postgres_server_major", recorded.PostgresServerMajor, expected.PostgresServerMajor},
		{"pg_config", recorded.PGConfig, expected.PGConfig},
		{"pg_config_digest", recorded.PGConfigDigest, expected.PGConfigDigest},
		{"subject_dimension", recorded.SubjectDimension, expected.SubjectDimension},
		{"native_toolchain_digest", recorded.NativeToolchainDigest, expected.NativeToolchainDigest},
		{"native_toolchain_manifest_ref", recorded.NativeToolchainManifestRef, expected.NativeToolchainManifestRef},
		{"native_toolchain_provenance", recorded.NativeToolchainProvenance, expected.NativeToolchainProvenance},
		{"engine_version", recorded.EngineVersion, expected.EngineVersion},
		{"engine_commit", recorded.EngineCommit, expected.EngineCommit},
		{"engine_binary_digest", recorded.EngineBinaryDigest, expected.EngineBinaryDigest},
		{"pack_id", recorded.PackID, expected.PackID},
		{"pack_version", recorded.PackVersion, expected.PackVersion},
		{"pack_digest", recorded.PackDigest, expected.PackDigest},
		{"qualification", recorded.Qualification, expected.Qualification},
		{"digest", recorded.Digest, expected.Digest},
	}
	for _, field := range fields {
		if field.recorded != field.want {
			addIssue(result, "trial %d benchmark environment field %s does not match independently derived linked evidence", number, field.name)
		}
	}
	if recorded.TargetEndpointPort != expected.TargetEndpointPort {
		addIssue(result, "trial %d benchmark environment field target_endpoint_port does not match independently derived linked evidence", number)
	}
	if trial.EnvironmentDigest != expected.Digest {
		addIssue(result, "trial %d environment digest does not match independently derived linked evidence", number)
	}
}

func validEnvironmentToken(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || index > 0 && (character == '_' || character == '-' || character == '.') {
			continue
		}
		return false
	}
	return true
}

func postgresEnvironmentMajor(versionNum int) string {
	if versionNum >= 100000 {
		return strconv.Itoa(versionNum / 10000)
	}
	return fmt.Sprintf("%d.%d", versionNum/10000, (versionNum/100)%100)
}

type runRow struct {
	Trial        int
	RunID        string
	Status       string
	PrimaryValue string
	RunRef       string
}

func readRunsTSV(path string) ([]runRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.Comma = '\t'
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 || strings.Join(records[0], "\t") != "trial\trun_id\tstatus\tprimary_value\trun_ref" {
		return nil, fmt.Errorf("unexpected header")
	}
	rows := make([]runRow, 0, len(records)-1)
	for index, record := range records[1:] {
		if len(record) != 5 {
			return nil, fmt.Errorf("row %d has %d columns", index+2, len(record))
		}
		trial, err := strconv.Atoi(record[0])
		if err != nil {
			return nil, fmt.Errorf("row %d trial: %w", index+2, err)
		}
		rows = append(rows, runRow{Trial: trial, RunID: record[1], Status: record[2], PrimaryValue: record[3], RunRef: record[4]})
	}
	return rows, nil
}

func inferArtifactRoot(root string, seriesDir string) string {
	parent := filepath.Dir(seriesDir)
	if filepath.Base(parent) == "benchmarks" && filepath.Base(filepath.Dir(parent)) == "runs" {
		return filepath.Dir(filepath.Dir(parent))
	}
	return root
}

func equalStats(left pgbenchresult.TrialStats, right pgbenchresult.TrialStats) bool {
	if left.SchemaVersion != right.SchemaVersion || left.StatsVersion != right.StatsVersion || left.N != right.N {
		return false
	}
	for _, pair := range [][2]float64{{left.Mean, right.Mean}, {left.Median, right.Median}, {left.SampleStddev, right.SampleStddev}, {left.MAD, right.MAD}, {left.Min, right.Min}, {left.Max, right.Max}} {
		if math.Abs(pair[0]-pair[1]) > 1e-12*math.Max(1, math.Max(math.Abs(pair[0]), math.Abs(pair[1]))) {
			return false
		}
	}
	return equalOptionalFloat(left.CVPct, right.CVPct) && equalOptionalFloat(left.RobustCVPct, right.RobustCVPct)
}

func equalOptionalFloat(left *float64, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return math.Abs(*left-*right) <= 1e-12*math.Max(1, math.Max(math.Abs(*left), math.Abs(*right)))
}

func decodeStrict(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func checkRegular(result *VerifyResult, path string, label string, nonEmpty bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || nonEmpty && info.Size() == 0 {
		addIssue(result, "%s is missing, empty, or unsafe", label)
	}
}

func checkDirectory(result *VerifyResult, path string, label string) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		addIssue(result, "%s directory is missing or unsafe", label)
	}
}

func addIssue(result *VerifyResult, format string, args ...any) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, args...))
	sort.Strings(result.Issues)
}

func RenderVerify(w io.Writer, result VerifyResult) error {
	if result.IsValid() {
		_, err := fmt.Fprintf(w, "PASS: benchmark series artifact %s\n", result.Dir)
		return err
	}
	if _, err := fmt.Fprintf(w, "FAIL: benchmark series artifact %s\n", result.Dir); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(w, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
