package operationbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
)

func ResolveSeriesDir(root, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates,
			filepath.Join(root, input),
			filepath.Join(root, "runs", "operation-benchmarks", input),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate)
		if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return filepath.Abs(candidate)
		}
	}
	return "", fmt.Errorf("operation benchmark series not found: %s", input)
}

func Load(root, input string) (Series, error) {
	dir, err := ResolveSeriesDir(root, input)
	if err != nil {
		return Series{}, err
	}
	var series Series
	if err := decodeStrictFile(filepath.Join(dir, "result.json"), &series); err != nil {
		return Series{}, fmt.Errorf("parse operation benchmark result: %w", err)
	}
	series.ArtifactDir = dir
	return series, nil
}

func Verify(root, input string) (VerifyResult, error) {
	return verify(root, input, false)
}

func VerifyBundle(root, input string) (VerifyResult, error) {
	return verify(root, input, true)
}

func verify(root, input string, requireBundle bool) (VerifyResult, error) {
	dir, err := ResolveSeriesDir(root, input)
	if err != nil {
		return VerifyResult{}, err
	}
	verification := VerifyResult{Dir: dir, BundleInventoryRequired: requireBundle, Issues: []string{}}
	var series Series
	if err := decodeStrictFile(filepath.Join(dir, "result.json"), &series); err != nil {
		addIssue(&verification, "result.json parse failed: %v", err)
		return finalizeVerification(verification, requireBundle, root)
	}
	series.ArtifactDir = dir
	verification.Series = &series
	checkSeriesIdentity(&verification, series, dir)
	checkInputCapsule(&verification, dir, series)
	checkRetainedRuntimeIdentity(&verification, dir, series)

	var spec Spec
	specPath := filepath.Join(dir, "operation-spec.json")
	if err := decodeStrictFile(specPath, &spec); err != nil {
		addIssue(&verification, "operation spec snapshot parse failed: %v", err)
	} else if err := validateSpec(spec, series.Operation); err != nil {
		addIssue(&verification, "operation spec snapshot invalid: %v", err)
	} else {
		digest, digestErr := evidence.DigestFile(specPath)
		if digestErr != nil || digest != series.SpecDigest {
			addIssue(&verification, "operation spec snapshot digest mismatch")
		}
		checkSeriesAgainstSpec(&verification, series, spec)
		checkInputClosureCompleteness(&verification, dir, series)
	}
	experimentSnapshot := filepath.Join(dir, "experiment-spec.env")
	if digest, digestErr := evidence.DigestFile(experimentSnapshot); digestErr != nil || digest != series.ExperimentDigest {
		addIssue(&verification, "experiment spec snapshot digest mismatch")
	}

	var artifactRoot string
	var rootErr error
	if requireBundle {
		artifactRoot, rootErr = bundleArtifactRoot(dir, series)
	} else {
		artifactRoot, rootErr = inferArtifactRoot(root, dir, series.Trials)
	}
	if rootErr != nil {
		addIssue(&verification, "resolve linked artifact root: %v", rootErr)
	} else {
		checkTrials(&verification, artifactRoot, dir, series, spec)
	}
	checkAggregate(&verification, series, spec)
	checkSummary(&verification, dir, series)
	return finalizeVerification(verification, requireBundle, artifactRoot)
}

func finalizeVerification(result VerifyResult, requireBundle bool, artifactRoot string) (VerifyResult, error) {
	if requireBundle {
		checkBundleInventory(&result, artifactRoot)
	}
	result.Valid = len(result.Issues) == 0
	return result, nil
}

func checkSeriesIdentity(result *VerifyResult, series Series, dir string) {
	if series.SchemaVersion != SeriesSchemaVersion && series.SchemaVersion != SeriesSchemaVersionV2 || series.ArtifactType != SeriesArtifactType {
		addIssue(result, "unsupported operation benchmark series schema or artifact type")
	}
	if series.Classification != Classification || series.DecisionEligible {
		addIssue(result, "operation benchmark must remain descriptive and decision_eligible=false")
	}
	if !validRunID(series.RunID) || filepath.Base(dir) != series.RunID || series.RunDir != "." {
		addIssue(result, "series run identity is not canonical")
	}
	if !evidence.IsDigest(series.SpecDigest) || !evidence.IsDigest(series.ExperimentDigest) || !evidence.IsDigest(series.ExecutionParametersDigest) || !evidence.IsDigest(series.PackDigest) || !evidence.IsDigest(series.InputsDigest) {
		addIssue(result, "series contains invalid input digest")
	}
	if !runstate.IsEngineVersion(series.EngineVersion) || !runstate.IsEngineCommit(series.EngineCommit) || series.EngineBinaryRef != EngineBinaryRef || !evidence.IsDigest(series.EngineBinaryDigest) {
		addIssue(result, "series engine identity is invalid")
	}
	if series.SpecRef != filepath.ToSlash(filepath.Join("benchmarks", "operations", filepath.FromSlash(series.Operation)+".json")) {
		addIssue(result, "series spec_ref does not match operation id")
	}
	if series.ExperimentRef != filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(series.ExperimentSpec)+".env")) {
		addIssue(result, "series experiment_ref does not match experiment id")
	}
	if series.Runtime != "docker" && series.Runtime != "native" {
		addIssue(result, "series runtime is unsupported")
	}
	checkRuntimePorts(result, series)
	if series.Status != "passed" && series.Status != "failed" && series.Status != "inconclusive" {
		addIssue(result, "series status is unsupported: %s", series.Status)
	}
	started, startErr := parseCanonicalUTC(series.StartedAt)
	finished, finishErr := parseCanonicalUTC(series.FinishedAt)
	if startErr != nil || finishErr != nil || finished.Before(started) {
		addIssue(result, "series timestamps are not canonical chronological UTC")
	}
}

func checkRuntimePorts(result *VerifyResult, series Series) {
	if series.SchemaVersion == SeriesSchemaVersion {
		if series.RuntimePortsPresent || series.RuntimePortsDigestPresent || series.RuntimePorts != nil || series.RuntimePortsDigest != "" {
			addIssue(result, "operation benchmark series v1 must omit runtime port binding fields")
		}
		return
	}
	if series.SchemaVersion != SeriesSchemaVersionV2 {
		return
	}
	if !series.RuntimePortsPresent || !series.RuntimePortsDigestPresent || series.RuntimePorts == nil || series.RuntimePortsDigest == "" {
		addIssue(result, "operation benchmark series v2 requires a present runtime port snapshot and digest")
		return
	}
	if err := validateRuntimePorts(*series.RuntimePorts); err != nil {
		addIssue(result, "series runtime port snapshot is invalid: %v", err)
		return
	}
	if !evidence.IsDigest(series.RuntimePortsDigest) || digestRuntimePorts(*series.RuntimePorts) != series.RuntimePortsDigest {
		addIssue(result, "series runtime port snapshot digest does not match retained ports")
	}
}

func checkRetainedRuntimeIdentity(result *VerifyResult, seriesDir string, series Series) {
	canonicalSeries, canonicalErr := filepath.EvalSymlinks(seriesDir)
	if canonicalErr != nil {
		addIssue(result, "series directory cannot be canonicalized")
		return
	}
	enginePath, err := safeExistingJoin(seriesDir, series.EngineBinaryRef)
	wantEnginePath := filepath.Join(canonicalSeries, filepath.FromSlash(EngineBinaryRef))
	if err != nil || enginePath != wantEnginePath {
		addIssue(result, "engine binary snapshot path is unsafe")
	} else {
		info, statErr := os.Lstat(enginePath)
		digest, digestErr := evidence.DigestFile(enginePath)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || digestErr != nil || digest != series.EngineBinaryDigest {
			addIssue(result, "engine binary snapshot digest or type mismatch")
		}
	}
	if series.Runtime == "native" {
		if !evidence.IsDigest(series.NativeToolchainDigest) || series.NativeToolchainManifestRef != NativeManifestRef || series.NativeToolchainProvenance != nativetoolchain.Unattested {
			addIssue(result, "native operation series toolchain identity is incomplete")
			return
		}
		manifestPath, joinErr := safeExistingJoin(seriesDir, series.NativeToolchainManifestRef)
		wantManifest := filepath.Join(canonicalSeries, filepath.FromSlash(NativeManifestRef))
		if joinErr != nil || manifestPath != wantManifest {
			addIssue(result, "native operation series toolchain path is unsafe")
			return
		}
		if _, verifyErr := nativetoolchain.VerifySnapshot(filepath.Dir(manifestPath), series.NativeToolchainDigest); verifyErr != nil {
			addIssue(result, "native operation series toolchain snapshot failed verification: %v", verifyErr)
		}
		return
	}
	if series.NativeToolchainDigest != "" || series.NativeToolchainManifestRef != "" || series.NativeToolchainProvenance != "" {
		addIssue(result, "Docker operation series contains native toolchain identity")
	}
	if _, err := os.Lstat(filepath.Join(seriesDir, "protocol", "native-toolchain")); err == nil || !os.IsNotExist(err) {
		addIssue(result, "Docker operation series contains a native toolchain snapshot")
	}
}

func checkInputClosureCompleteness(result *VerifyResult, seriesDir string, series Series) {
	capsuleRoot := filepath.Join(seriesDir, "inputs")
	spec, err := NewCatalog(capsuleRoot).Load(series.Operation)
	if err != nil {
		addIssue(result, "operation input capsule cannot reload its operation spec: %v", err)
		return
	}
	if spec.Digest != series.SpecDigest {
		addIssue(result, "operation input closure spec digest does not match series")
	}
	experimentPath, pathErr := safeExistingJoin(capsuleRoot, series.ExperimentRef)
	if pathErr != nil {
		addIssue(result, "operation input closure experiment path is unsafe")
	} else if digest, digestErr := evidence.DigestFile(experimentPath); digestErr != nil || digest != series.ExperimentDigest {
		addIssue(result, "operation input closure experiment digest does not match series")
	}
	recomputed, err := collectInputClosure(capsuleRoot, spec)
	if err != nil {
		addIssue(result, "operation input closure cannot be independently recomputed: %v", err)
		return
	}
	if !reflect.DeepEqual(recomputed, series.Inputs) {
		addIssue(result, "operation input closure is incomplete or contains undeclared files")
	}
}

func checkSeriesAgainstSpec(result *VerifyResult, series Series, spec Spec) {
	if series.Name != spec.Name || series.Description != spec.Description || series.Assurance != spec.Assurance ||
		series.ExperimentSpec != spec.ExperimentSpec || series.TrialsPlanned != spec.Trials || series.MaxCVPct != spec.MaxCVPct ||
		!reflect.DeepEqual(series.Measurement, spec.Measurement) {
		addIssue(result, "series metadata does not match operation spec snapshot")
	}
	if !supportsRuntime(spec, series.Runtime) {
		addIssue(result, "series runtime is not allowed by operation spec")
	}
}

func checkTrials(result *VerifyResult, artifactRoot, seriesDir string, series Series, spec Spec) {
	trialDir := filepath.Join(seriesDir, "trials")
	entries, err := os.ReadDir(trialDir)
	if err != nil {
		addIssue(result, "trials directory cannot be read: %v", err)
		return
	}
	if len(entries) != len(series.Trials) {
		addIssue(result, "trials directory count does not match result.json")
	}
	previousFinish, _ := parseCanonicalUTC(series.StartedAt)
	seriesFinish, _ := parseCanonicalUTC(series.FinishedAt)
	seen := map[string]bool{}
	for index, trial := range series.Trials {
		number := index + 1
		if trial.Trial != number || !validRunID(trial.RunID) || seen[trial.RunID] {
			addIssue(result, "trial %d identity is invalid or duplicate", number)
		}
		seen[trial.RunID] = true
		expectedRef := filepath.ToSlash(filepath.Join("runs", trial.RunID))
		if trial.RunRef != expectedRef || !evidence.IsDigest(trial.RunDigest) {
			addIssue(result, "trial %d linked run reference or digest is invalid", number)
		}
		var stored Trial
		if err := decodeStrictFile(filepath.Join(trialDir, fmt.Sprintf("%03d.json", number)), &stored); err != nil {
			addIssue(result, "trial %d artifact parse failed: %v", number, err)
		} else if !reflect.DeepEqual(stored, trial) {
			addIssue(result, "trial %d artifact does not match result.json", number)
		}
		started, startErr := parseCanonicalUTC(trial.StartedAt)
		finished, finishErr := parseCanonicalUTC(trial.FinishedAt)
		if startErr != nil || finishErr != nil || finished.Before(started) || started.Before(previousFinish) || finished.After(seriesFinish) {
			addIssue(result, "trial %d timestamps are not canonical, chronological, and non-overlapping", number)
		} else {
			previousFinish = finished
		}
		runDir, joinErr := safeExistingJoin(artifactRoot, trial.RunRef)
		if joinErr != nil {
			addIssue(result, "trial %d linked run path is unsafe", number)
			continue
		}
		if digest, digestErr := digestTree(runDir); digestErr != nil || digest != trial.RunDigest {
			addIssue(result, "trial %d linked run tree digest mismatch", number)
		}
		runVerification, verifyErr := runverify.Verify(artifactRoot, runDir)
		if verifyErr != nil || !runVerification.Valid() {
			addIssue(result, "trial %d linked run does not independently verify", number)
		}
		manifest, manifestErr := envfile.Parse(filepath.Join(runDir, "manifest.env"))
		if manifestErr != nil {
			addIssue(result, "trial %d linked run manifest cannot be parsed", number)
		} else {
			if manifest["experiment_spec_id"] != series.ExperimentSpec || manifest["experiment_spec_digest"] != series.ExperimentDigest || manifest["runtime"] != series.Runtime || manifest["experiment_topology"] != series.Topology || manifest["engine_version"] != series.EngineVersion || manifest["engine_commit"] != series.EngineCommit {
				addIssue(result, "trial %d linked run identity does not match series", number)
			}
			if !evidence.IsDigest(trial.ExecutionParametersDigest) || !evidence.IsDigest(trial.ExperimentIdentityDigest) || !evidence.IsDigest(trial.PackDigest) || manifest["execution_parameters_digest"] != trial.ExecutionParametersDigest || manifest["experiment_identity_digest"] != trial.ExperimentIdentityDigest || manifest["pack_digest"] != trial.PackDigest || trial.PackDigest != series.PackDigest {
				addIssue(result, "trial %d linked execution/experiment identity digest does not match manifest", number)
			}
			checkLinkedRuntimePortBinding(result, series, manifest, number)
		}
		if runVerification.Verdict == nil || runVerification.Verdict.Status != trial.Status || !trial.ExperimentVerified {
			addIssue(result, "trial %d verification/status claim does not match linked verdict", number)
		} else {
			wantStarted := canonicalTimestamp(runVerification.Verdict.StartedAt)
			wantFinished := canonicalTimestamp(runVerification.Verdict.FinishedAt)
			wantDuration, _ := timestampDurationMS(wantStarted, wantFinished)
			if trial.StartedAt != wantStarted || trial.FinishedAt != wantFinished || trial.DurationMS != wantDuration {
				addIssue(result, "trial %d timing does not match linked run", number)
			}
		}
		value, operationResult, resultRef, resultDigest, deriveErr := derivePrimaryValue(runDir, spec)
		if trial.Status == "passed" {
			if deriveErr != nil || trial.PrimaryValue == nil || *trial.PrimaryValue != value || trial.ResultRef != resultRef || trial.ResultDigest != resultDigest || !reflect.DeepEqual(trial.OperationResult, operationResult) {
				addIssue(result, "trial %d primary metric does not match independently parsed evidence", number)
			}
		} else if trial.PrimaryValue != nil {
			addIssue(result, "failed trial %d must not expose a primary value", number)
		}
	}
}

func checkLinkedRuntimePortBinding(result *VerifyResult, series Series, manifest map[string]string, number int) {
	manifestSchema := manifest["schema_version"]
	_, manifestHasRuntimePortsDigest := manifest["runtime_ports_digest"]
	switch series.SchemaVersion {
	case SeriesSchemaVersion:
		if manifestSchema != runstate.ManifestSchemaVersion || manifestHasRuntimePortsDigest {
			addIssue(result, "trial %d legacy series requires a v1 linked manifest without runtime port binding", number)
		}
	case SeriesSchemaVersionV2:
		if manifestSchema != runstate.ManifestSchemaVersionV2 || !manifestHasRuntimePortsDigest || manifest["runtime_ports_digest"] != series.RuntimePortsDigest {
			addIssue(result, "trial %d linked v2 runtime ports digest does not match series", number)
		}
	}
}

func checkInputCapsule(result *VerifyResult, seriesDir string, series Series) {
	if len(series.Inputs) == 0 {
		addIssue(result, "operation input closure is empty")
		return
	}
	if len(series.Inputs) > maxOperationInputFiles {
		addIssue(result, "operation input closure exceeds %d files", maxOperationInputFiles)
	}
	if !sort.SliceIsSorted(series.Inputs, func(i, j int) bool { return series.Inputs[i].Path < series.Inputs[j].Path }) {
		addIssue(result, "operation input closure is not sorted")
	}
	digest, err := inputClosureDigest(series.Inputs)
	if err != nil || digest != series.InputsDigest {
		addIssue(result, "operation input closure digest mismatch")
	}
	seen := map[string]bool{}
	var totalBytes int64
	for _, input := range series.Inputs {
		if seen[input.Path] || !evidence.IsPortablePath(input.Path) || operationInputPathForbidden(input.Path) || !evidence.IsDigest(input.Digest) || input.Size < 0 {
			addIssue(result, "operation input closure contains invalid or duplicate entry: %s", input.Path)
			continue
		}
		if input.Size > maxOperationInputBytes-totalBytes {
			addIssue(result, "operation input closure exceeds %d bytes", maxOperationInputBytes)
		} else {
			totalBytes += input.Size
		}
		seen[input.Path] = true
		path := filepath.Join(seriesDir, "inputs", filepath.FromSlash(input.Path))
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != input.Size {
			addIssue(result, "operation input capsule file is missing or unsafe: %s", input.Path)
			continue
		}
		actual, digestErr := evidence.DigestFile(path)
		if digestErr != nil || actual != input.Digest {
			addIssue(result, "operation input capsule digest mismatch: %s", input.Path)
		}
	}
	inputRoot := filepath.Join(seriesDir, "inputs")
	walkErr := filepath.WalkDir(inputRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(inputRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !seen[rel] {
			addIssue(result, "operation input capsule contains unexpected file: %s", rel)
		}
		return nil
	})
	if walkErr != nil {
		addIssue(result, "operation input capsule cannot be scanned: %v", walkErr)
	}
}

func checkAggregate(result *VerifyResult, series Series, spec Spec) {
	valid, failed := 0, 0
	var values []float64
	executionParametersStable := evidence.IsDigest(series.ExecutionParametersDigest)
	experimentIdentityStable := true
	experimentIdentityDigest := ""
	firstPassed := true
	for _, trial := range series.Trials {
		switch trial.Status {
		case "passed":
			valid++
			if firstPassed {
				firstPassed = false
				if trial.ExecutionParametersDigest != series.ExecutionParametersDigest {
					addIssue(result, "series execution_parameters_digest does not bind the first exact trial")
				}
			}
			if trial.ExecutionParametersDigest != series.ExecutionParametersDigest {
				executionParametersStable = false
			}
			if experimentIdentityDigest == "" {
				experimentIdentityDigest = trial.ExperimentIdentityDigest
			} else if trial.ExperimentIdentityDigest != experimentIdentityDigest {
				experimentIdentityStable = false
			}
			if trial.PrimaryValue != nil {
				values = append(values, *trial.PrimaryValue)
			}
		case "failed":
			failed++
		default:
			addIssue(result, "trial %d has unsupported status %q", trial.Trial, trial.Status)
		}
	}
	if valid != series.TrialsValid || failed != series.TrialsFailed || len(series.Trials) > series.TrialsPlanned {
		addIssue(result, "series trial totals are inconsistent")
	}
	derivedStatus := "failed"
	var derivedStats *pgbenchresult.TrialStats
	if valid == spec.Trials && failed == 0 && len(series.Trials) == spec.Trials {
		stats, err := pgbenchresult.Summarize(values)
		if err != nil {
			addIssue(result, "recompute operation statistics: %v", err)
		} else {
			derivedStats = &stats
			derivedStatus = "passed"
			if !executionParametersStable || !experimentIdentityStable {
				derivedStatus = "failed"
			} else if stats.CVPct == nil || *stats.CVPct > spec.MaxCVPct {
				derivedStatus = "inconclusive"
			}
		}
	}
	if series.Status != derivedStatus {
		addIssue(result, "series status %q does not match independently derived status %q", series.Status, derivedStatus)
	}
	if !reflect.DeepEqual(series.Stats, derivedStats) {
		addIssue(result, "series statistics do not match independently derived trial values")
	}
}

func checkSummary(result *VerifyResult, dir string, series Series) {
	content, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil || !bytes.Equal(content, SummaryBytes(series)) {
		addIssue(result, "summary.md does not match independently rendered result.json")
	}
}

func inferArtifactRoot(root, seriesDir string, trials []Trial) (string, error) {
	candidates := []string{root, filepath.Clean(filepath.Join(seriesDir, "..", "..", ".."))}
	for _, candidate := range candidates {
		candidate, _ = filepath.Abs(candidate)
		if len(trials) == 0 {
			continue
		}
		path, err := safeJoin(candidate, trials[0].RunRef)
		if err != nil {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no root contains linked runs")
}

func bundleArtifactRoot(seriesDir string, series Series) (string, error) {
	canonicalSeries, err := filepath.EvalSymlinks(seriesDir)
	if err != nil {
		return "", fmt.Errorf("resolve bundle series directory: %w", err)
	}
	canonicalSeries, err = filepath.Abs(canonicalSeries)
	if err != nil {
		return "", err
	}
	artifactRoot := filepath.Clean(filepath.Join(canonicalSeries, "..", "..", ".."))
	expectedRef := filepath.ToSlash(filepath.Join("runs", "operation-benchmarks", series.RunID))
	expectedSeries, err := safeExistingJoin(artifactRoot, expectedRef)
	if err != nil || expectedSeries != canonicalSeries {
		return "", fmt.Errorf("bundle series is not at canonical ref %s", expectedRef)
	}
	inventoryPath := filepath.Join(artifactRoot, BundleInventoryName)
	info, err := os.Lstat(inventoryPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("bundle inventory is missing or unsafe")
	}
	return artifactRoot, nil
}

func parseCanonicalUTC(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("non-canonical timestamp %q", value)
	}
	return parsed, nil
}

func addIssue(result *VerifyResult, format string, values ...any) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, values...))
}

func SummaryBytes(series Series) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Operation benchmark: %s\n\n", series.Name)
	fmt.Fprintf(&builder, "- Status: `%s`\n", series.Status)
	fmt.Fprintf(&builder, "- Classification: `%s`\n", series.Classification)
	fmt.Fprintf(&builder, "- Decision eligible: `%t`\n", series.DecisionEligible)
	fmt.Fprintf(&builder, "- Operation: `%s`\n", series.Operation)
	fmt.Fprintf(&builder, "- Runtime/topology: `%s` / `%s`\n", series.Runtime, series.Topology)
	fmt.Fprintf(&builder, "- Primary metric: `%s` (`%s`, `%s`)\n", series.Measurement.Metric, series.Measurement.Unit, series.Measurement.Direction)
	fmt.Fprintf(&builder, "- Measurement basis: `%s`\n", series.Measurement.Basis)
	fmt.Fprintf(&builder, "- Execution parameters: `%s`\n", series.ExecutionParametersDigest)
	fmt.Fprintf(&builder, "- Trials: %d valid of %d planned\n", series.TrialsValid, series.TrialsPlanned)
	if series.Stats != nil {
		fmt.Fprintf(&builder, "- Mean/median/CV: %.6g / %.6g / %.6g%%\n", series.Stats.Mean, series.Stats.Median, dereference(series.Stats.CVPct))
	}
	fmt.Fprintf(&builder, "\nAssurance boundary: %s\n", series.Assurance)
	return []byte(builder.String())
}

func dereference(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func RenderJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func RenderSeries(writer io.Writer, series Series) error {
	_, err := fmt.Fprintf(writer, "%s: operation=%s runtime=%s trials=%d/%d status=%s decision_eligible=false\nseries_dir=%s\n", strings.ToUpper(series.Status), series.Operation, series.Runtime, series.TrialsValid, series.TrialsPlanned, series.Status, series.ArtifactDir)
	return err
}

func RenderVerify(writer io.Writer, result VerifyResult) error {
	status := "PASS"
	if !result.Valid {
		status = "FAIL"
	}
	if _, err := fmt.Fprintf(writer, "%s: operation benchmark verification dir=%s bundle_inventory_required=%t\n", status, result.Dir, result.BundleInventoryRequired); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(writer, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

func RenderCatalog(writer io.Writer, specs []Spec) error {
	if _, err := fmt.Fprintln(writer, "ID\tTRIALS\tRUNTIMES\tMETRIC\tBASIS\tDECISION"); err != nil {
		return err
	}
	for _, spec := range specs {
		if _, err := fmt.Fprintf(writer, "%s\t%d\t%s\t%s %s\t%s\tfalse\n", spec.ID, spec.Trials, strings.Join(spec.SupportedRuntime, ","), spec.Measurement.Metric, spec.Measurement.Unit, spec.Measurement.Basis); err != nil {
			return err
		}
	}
	return nil
}

func RenderSpec(writer io.Writer, spec Spec) error {
	_, err := fmt.Fprintf(writer, "Operation benchmark: %s\nName: %s\nExperiment: %s\nTrials: %d\nRuntime: %s\nMetric: %s (%s, %s)\nBasis: %s\nDecision eligible: false\nAssurance: %s\n", spec.ID, spec.Name, spec.ExperimentSpec, spec.Trials, strings.Join(spec.SupportedRuntime, ","), spec.Measurement.Metric, spec.Measurement.Unit, spec.Measurement.Direction, spec.Measurement.Basis, spec.Assurance)
	return err
}
