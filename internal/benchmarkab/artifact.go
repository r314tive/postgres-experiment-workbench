package benchmarkab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcompare"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkqualify"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksettings"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
)

const maxABJSONBytes = 16 << 20

func Resolve(root, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates, filepath.Join(root, input), filepath.Join(root, "runs", "benchmark-ab", input))
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
	return "", fmt.Errorf("counterbalanced benchmark directory not found: %s", input)
}

func Load(root, input string) (Result, error) {
	dir, err := Resolve(root, input)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := decodeStrict(filepath.Join(dir, "result.json"), &result); err != nil {
		return Result{}, err
	}
	result.ArtifactDir = dir
	return result, nil
}

// Verify treats result.json as an assertion, not as authority. It reopens both
// series and both qualification bookends, verifies their complete evidence
// closure, reconstructs the schedule, and reruns the paired analysis.
func Verify(root, input string) (VerifyResult, error) {
	dir, err := Resolve(root, input)
	if err != nil {
		return VerifyResult{}, err
	}
	verification := VerifyResult{Dir: dir, Issues: []string{}}
	for _, name := range []string{"protocol.json", "result.json", "blocks.tsv", "summary.md"} {
		checkRegular(&verification, filepath.Join(dir, name), name)
	}
	for _, item := range []struct{ path, label string }{
		{filepath.Join(dir, "qualification"), "qualification"},
		{filepath.Join(dir, "blocks"), "blocks"},
	} {
		checkDirectory(&verification, item.path, item.label)
	}

	var protocol Protocol
	if err := decodeStrict(filepath.Join(dir, "protocol.json"), &protocol); err != nil {
		addIssue(&verification, "protocol.json parse failed: %v", err)
	} else if err := VerifyProtocol(protocol); err != nil {
		addIssue(&verification, "protocol verification failed: %v", err)
	}
	checkNativeToolchainSnapshots(&verification, dir, protocol)
	result, loadErr := Load(root, dir)
	if loadErr != nil {
		addIssue(&verification, "result.json parse failed: %v", loadErr)
		verification.Valid = false
		return verification, nil
	}
	verification.Result = &result
	checkResultIdentity(&verification, dir, protocol, result)
	checkFileRef(&verification, dir, result.ProtocolRef, "protocol.json", "protocol")
	checkFileRef(&verification, dir, result.Qualification.Before, "qualification/before.json", "before qualification")
	checkFileRef(&verification, dir, result.Qualification.After, "qualification/after.json", "after qualification")

	before, beforeOK := loadQualification(&verification, filepath.Join(dir, "qualification", "before.json"), "before")
	after, afterOK := loadQualification(&verification, filepath.Join(dir, "qualification", "after.json"), "after")
	artifactRoot := inferArtifactRoot(root, dir)
	baseline, baselinePlan, baselineOK := loadSeries(&verification, artifactRoot, result.Baseline, "baseline")
	candidate, candidatePlan, candidateOK := loadSeries(&verification, artifactRoot, result.Candidate, "candidate")

	if baselineOK && candidateOK {
		checkSeriesProtocol(&verification, protocol, baseline, candidate, baselinePlan, candidatePlan)
		derivedBlocks := DeriveBlocks(protocol, baseline, candidate)
		if !reflect.DeepEqual(derivedBlocks, result.Blocks) {
			addIssue(&verification, "blocks do not match independently reconstructed series trials")
		}
		checkScheduleChronology(&verification, result.Blocks, baseline, candidate)
		checkBlockFiles(&verification, dir, derivedBlocks)
		checkBytes(&verification, filepath.Join(dir, "blocks.tsv"), blocksTSVBytes(derivedBlocks), "blocks.tsv")

		analysis := benchmarkcompare.AnalyzePaired(pairedUnits(derivedBlocks), pairedOptions(protocol))
		if !reflect.DeepEqual(analysis, result.Analysis) {
			addIssue(&verification, "paired analysis does not match independent recomputation")
		}
		if beforeOK && afterOK {
			assessment := assessQualification(protocol, before, after, baseline, candidate)
			if !reflect.DeepEqual(assessment, result.Qualification.Assessment) {
				addIssue(&verification, "qualification assessment does not match independent recomputation")
			}
			effectiveSettings := assessEffectiveSettings(protocol, baseline, candidate)
			if !reflect.DeepEqual(effectiveSettings, result.EffectiveSettings) {
				addIssue(&verification, "effective pg_settings assessment does not match independent recomputation")
			}
			status, decision, reasons := deriveTerminal(protocol, baseline, candidate, derivedBlocks, assessment, effectiveSettings, analysis)
			if result.Status != status || result.Decision != decision || !reflect.DeepEqual(result.Reasons, reasons) {
				addIssue(&verification, "terminal status, decision, or reasons do not match independent derivation")
			}
			if result.StartedAt != before.RecordedAt || result.FinishedAt != after.RecordedAt {
				addIssue(&verification, "run interval does not match qualification bookends")
			}
		}
	}
	checkBytes(&verification, filepath.Join(dir, "summary.md"), summaryBytes(result), "summary.md")
	if digest, err := resultDigest(result); err != nil {
		addIssue(&verification, "result digest cannot be recomputed: %v", err)
	} else if digest != result.Digest {
		addIssue(&verification, "result digest mismatch")
	}
	verification.Valid = verification.IsValid()
	return verification, nil
}

func checkNativeToolchainSnapshots(verification *VerifyResult, dir string, protocol Protocol) {
	toolchainsDir := filepath.Join(dir, "toolchains")
	if protocol.SubjectDimension != SubjectNativeToolchain {
		if _, err := os.Lstat(toolchainsDir); !os.IsNotExist(err) {
			addIssue(verification, "pg_config protocol contains an unexpected native toolchain snapshot directory")
		}
		return
	}
	if protocol.Baseline.NativeToolchain == nil || protocol.Candidate.NativeToolchain == nil {
		if protocol.Baseline.NativeToolchain == nil {
			addIssue(verification, "baseline native toolchain identity is missing")
		}
		if protocol.Candidate.NativeToolchain == nil {
			addIssue(verification, "candidate native toolchain identity is missing")
		}
		return
	}
	for _, item := range []struct {
		role     string
		identity *NativeToolchainIdentity
	}{
		{"baseline", protocol.Baseline.NativeToolchain},
		{"candidate", protocol.Candidate.NativeToolchain},
	} {
		expectedRef := filepath.ToSlash(filepath.Join("toolchains", item.role, nativetoolchain.ManifestName))
		if item.identity.ManifestRef != expectedRef {
			addIssue(verification, "%s native toolchain manifest reference is invalid", item.role)
			continue
		}
		manifestPath := filepath.Join(dir, filepath.FromSlash(item.identity.ManifestRef))
		manifest, err := nativetoolchain.VerifySnapshot(filepath.Dir(manifestPath), item.identity.Digest)
		if err != nil {
			addIssue(verification, "%s native toolchain snapshot is invalid: %v", item.role, err)
			continue
		}
		if manifest.SourceCommit != item.identity.SourceCommit || manifest.BuildProvenance != item.identity.BuildProvenance || nativetoolchain.Version(manifest, "postgres") != item.identity.PostgresVersion || nativetoolchain.Version(manifest, "pgbench") != item.identity.PgbenchVersion || nativetoolchain.Version(manifest, "psql") != item.identity.PsqlVersion {
			addIssue(verification, "%s native toolchain provenance differs from protocol", item.role)
		}
	}
	baseline, baselineErr := nativetoolchain.VerifySnapshot(filepath.Join(dir, "toolchains", "baseline"), protocol.Baseline.NativeToolchain.Digest)
	candidate, candidateErr := nativetoolchain.VerifySnapshot(filepath.Join(dir, "toolchains", "candidate"), protocol.Candidate.NativeToolchain.Digest)
	if baselineErr == nil && candidateErr == nil {
		if err := nativetoolchain.RequireComparableVersions(baseline, candidate); err != nil {
			addIssue(verification, "native toolchain snapshots are version-incompatible: %v", err)
		}
	}
}

func checkResultIdentity(verification *VerifyResult, dir string, protocol Protocol, result Result) {
	if result.SchemaVersion != RunSchemaVersion || result.ArtifactType != RunArtifactType || result.SchedulerVersion != SchedulerVersion {
		addIssue(verification, "unsupported result schema, artifact type, or scheduler")
	}
	if result.RunID == "" || result.RunID != protocol.RunID || filepath.Base(dir) != result.RunID || !benchmarkrun.ValidRunID(result.RunID) {
		addIssue(verification, "result run identity mismatch")
	}
	if result.RunDir != "." {
		addIssue(verification, "result run_dir must be portable '.'")
	}
	if !canonicalUTC(result.StartedAt) || !canonicalUTC(result.FinishedAt) {
		addIssue(verification, "result timestamps must be canonical UTC RFC3339Nano")
	} else {
		started, _ := time.Parse(time.RFC3339Nano, result.StartedAt)
		finished, _ := time.Parse(time.RFC3339Nano, result.FinishedAt)
		if finished.Before(started) {
			addIssue(verification, "result finishes before it starts")
		}
	}
	if !evidence.IsDigest(result.Digest) {
		addIssue(verification, "result digest is not a lowercase sha256 digest")
	}
	if result.Blocks == nil || result.Reasons == nil {
		addIssue(verification, "result blocks and reasons must be present arrays")
	}
	if result.EffectiveSettings.Names == nil || result.EffectiveSettings.EffectiveDifferences == nil || result.EffectiveSettings.Reasons == nil {
		addIssue(verification, "effective pg_settings assessment arrays must be present")
	}
	if !reflect.DeepEqual(result.EffectiveSettings.Names, protocol.EffectiveSettings.Names) {
		addIssue(verification, "effective pg_settings assessment names differ from protocol")
	}
	if result.EffectiveSettings.Status != "verified-different" && result.EffectiveSettings.Status != "verified-stable" && result.EffectiveSettings.Status != "equivalent" && result.EffectiveSettings.Status != "unstable" && result.EffectiveSettings.Status != "invalid" {
		addIssue(verification, "effective pg_settings assessment status is invalid")
	}
	if !reflect.DeepEqual(result.Reasons, uniqueSorted(result.Reasons)) {
		addIssue(verification, "result reasons are not sorted and unique")
	}
}

func loadQualification(verification *VerifyResult, path, label string) (benchmarkqualify.Artifact, bool) {
	content, err := readRegularLimited(path, maxABJSONBytes)
	if err != nil {
		addIssue(verification, "%s qualification read failed: %v", label, err)
		return benchmarkqualify.Artifact{}, false
	}
	artifact, err := benchmarkqualify.Parse(content)
	if err != nil {
		addIssue(verification, "%s qualification parse failed: %v", label, err)
		return benchmarkqualify.Artifact{}, false
	}
	checked := benchmarkqualify.Verify(artifact)
	for _, issue := range checked.Issues {
		addIssue(verification, "%s qualification invalid: %s", label, issue)
	}
	return artifact, checked.Valid
}

func loadSeries(verification *VerifyResult, artifactRoot string, reference SeriesRef, wantRole string) (benchmarkrun.Series, benchmarkplan.Plan, bool) {
	if reference.Role != wantRole || !benchmarkrun.ValidRunID(reference.RunID) {
		addIssue(verification, "%s series reference identity is invalid", wantRole)
		return benchmarkrun.Series{}, benchmarkplan.Plan{}, false
	}
	wantRef := filepath.ToSlash(filepath.Join("runs", "benchmarks", reference.RunID))
	if reference.Ref != wantRef || !evidence.IsPortablePath(reference.Ref) {
		addIssue(verification, "%s series reference path is not canonical", wantRole)
		return benchmarkrun.Series{}, benchmarkplan.Plan{}, false
	}
	path, err := safeExistingJoin(artifactRoot, reference.Ref)
	if err != nil {
		addIssue(verification, "%s series reference is unsafe: %v", wantRole, err)
		return benchmarkrun.Series{}, benchmarkplan.Plan{}, false
	}
	checked, err := benchmarkartifact.Verify(artifactRoot, path)
	if err != nil {
		addIssue(verification, "%s series verification failed: %v", wantRole, err)
		return benchmarkrun.Series{}, benchmarkplan.Plan{}, false
	}
	for _, issue := range checked.Issues {
		addIssue(verification, "%s series invalid: %s", wantRole, issue)
	}
	if checked.Series == nil {
		addIssue(verification, "%s series result is missing", wantRole)
		return benchmarkrun.Series{}, benchmarkplan.Plan{}, false
	}
	if checked.Plan == nil {
		addIssue(verification, "%s series plan is missing", wantRole)
		return benchmarkrun.Series{}, benchmarkplan.Plan{}, false
	}
	digest, err := evidence.DigestFile(filepath.Join(path, "result.json"))
	if err != nil || digest != reference.ResultDigest {
		addIssue(verification, "%s series result digest mismatch", wantRole)
	}
	if checked.Series.RunID != reference.RunID {
		addIssue(verification, "%s series run id mismatch", wantRole)
	}
	return *checked.Series, *checked.Plan, checked.IsValid() && err == nil && digest == reference.ResultDigest
}

func checkSeriesProtocol(verification *VerifyResult, protocol Protocol, baseline, candidate benchmarkrun.Series, baselinePlan, candidatePlan benchmarkplan.Plan) {
	for _, item := range []struct {
		role    string
		subject Subject
		series  benchmarkrun.Series
	}{
		{"baseline", protocol.Baseline, baseline},
		{"candidate", protocol.Candidate, candidate},
	} {
		series := item.series
		if series.Benchmark != item.subject.Benchmark || series.Subject != item.subject.Subject || series.ProtocolDigest != item.subject.ProtocolDigest {
			addIssue(verification, "%s series does not match protocol subject identity", item.role)
		}
		if series.Runtime != protocol.Runtime || series.Class != "measurement" || series.PrimaryMetric != protocol.PrimaryMetric || series.Direction != protocol.Direction || series.ComparisonKeyDigest != protocol.ComparisonKeyDigest || series.TrialsPlanned != protocol.BlocksPlanned {
			addIssue(verification, "%s series does not match protocol comparison identity", item.role)
		}
		if series.RegressionThresholdPct == nil || *series.RegressionThresholdPct != protocol.RegressionThresholdPct || !reflect.DeepEqual(series.AllowedDifferences, []string{protocol.SubjectDimension}) {
			addIssue(verification, "%s series decision policy differs from protocol", item.role)
		}
		if series.Environment == nil || series.Environment.PGConfig != item.subject.PGConfig || series.Environment.PGConfigDigest != item.subject.PGConfigDigest {
			if series.Environment == nil {
				addIssue(verification, "%s series PostgreSQL configuration differs from protocol: environment is missing", item.role)
			} else {
				addIssue(verification, "%s series PostgreSQL configuration differs from protocol: got %s/%s want %s/%s", item.role, series.Environment.PGConfig, series.Environment.PGConfigDigest, item.subject.PGConfig, item.subject.PGConfigDigest)
			}
		}
		if series.Environment != nil {
			if series.Environment.SubjectDimension != protocol.SubjectDimension {
				addIssue(verification, "%s series subject dimension differs from protocol", item.role)
			}
			if protocol.Runtime == "native" && protocol.SubjectDimension == SubjectPGConfig {
				expectedManifestRef := filepath.ToSlash(filepath.Join("runs", "benchmarks", series.RunID, benchmarkrun.NativeToolchainSeriesRef))
				if !evidence.IsDigest(series.Environment.NativeToolchainDigest) || series.Environment.NativeToolchainManifestRef != expectedManifestRef || series.Environment.NativeToolchainProvenance != nativetoolchain.Unattested {
					addIssue(verification, "%s series native toolchain identity is not the canonical series-local snapshot", item.role)
				}
			} else if protocol.SubjectDimension == SubjectNativeToolchain {
				expectedManifestRef := filepath.ToSlash(filepath.Join("runs", "benchmarks", series.RunID, benchmarkrun.NativeToolchainSeriesRef))
				if series.Environment.NativeToolchainDigest != item.subject.NativeToolchain.Digest || series.Environment.NativeToolchainManifestRef != expectedManifestRef || series.Environment.NativeToolchainProvenance != nativetoolchain.Unattested {
					addIssue(verification, "%s series native toolchain identity differs from protocol or is not the canonical series-local snapshot", item.role)
				}
			}
		}
	}
	if baseline.RunID == candidate.RunID || populationsOverlap(baseline, candidate) {
		addIssue(verification, "baseline and candidate series populations are not distinct")
	}
	if baseline.ResetPolicy != candidate.ResetPolicy {
		addIssue(verification, "baseline and candidate reset policies differ")
	}
	if baseline.EngineBinaryDigest != candidate.EngineBinaryDigest {
		addIssue(verification, "baseline and candidate benchmark engine binary digests differ")
	}
	if baselinePlan.ClientPlacement != protocol.Qualification.ClientPlacement || candidatePlan.ClientPlacement != protocol.Qualification.ClientPlacement {
		addIssue(verification, "series client-placement declarations differ from the qualified protocol gate")
	}
	if baseline.Environment != nil && candidate.Environment != nil {
		left, right := *baseline.Environment, *candidate.Environment
		left.PGConfig, left.PGConfigDigest, left.Digest = "", "", ""
		right.PGConfig, right.PGConfigDigest, right.Digest = "", "", ""
		// Manifest references are portable artifact locations, not runtime
		// identity. Each arm's verifier checks its referenced snapshot; only the
		// byte digest and provenance must remain equal for a pg_config subject.
		left.NativeToolchainManifestRef = ""
		right.NativeToolchainManifestRef = ""
		if protocol.SubjectDimension == SubjectNativeToolchain {
			left.NativeToolchainDigest, left.NativeToolchainProvenance = "", ""
			right.NativeToolchainDigest, right.NativeToolchainProvenance = "", ""
		}
		if !reflect.DeepEqual(left, right) {
			addIssue(verification, "baseline and candidate runtime fingerprints differ outside the declared subject dimension")
		}
	}
	names, err := benchmarksettings.UnionConfigSettingNames(
		filepath.Join(baseline.ArtifactDir, "protocol", "postgresql.conf"),
		filepath.Join(candidate.ArtifactDir, "protocol", "postgresql.conf"),
	)
	if err != nil {
		addIssue(verification, "effective pg_settings protocol cannot be re-derived from series snapshots: %v", err)
	} else if !reflect.DeepEqual(names, protocol.EffectiveSettings.Names) {
		addIssue(verification, "effective pg_settings names do not match series configuration snapshots")
	}
}

func checkScheduleChronology(verification *VerifyResult, blocks []Block, baseline, candidate benchmarkrun.Series) {
	trials := map[string][]benchmarkrun.Trial{"baseline": baseline.Trials, "candidate": candidate.Trials}
	var previous time.Time
	for _, block := range blocks {
		for _, execution := range block.Executions {
			if execution.Trial <= 0 || execution.Trial > len(trials[execution.Role]) {
				addIssue(verification, "block %d execution references a missing trial", block.Number)
				continue
			}
			trial := trials[execution.Role][execution.Trial-1]
			if trial.PhaseTimeline == nil {
				addIssue(verification, "block %d execution has no phase timeline", block.Number)
				continue
			}
			started, startErr := time.Parse(time.RFC3339Nano, trial.PhaseTimeline.StartedAt)
			finished, finishErr := time.Parse(time.RFC3339Nano, trial.PhaseTimeline.FinishedAt)
			if startErr != nil || finishErr != nil {
				addIssue(verification, "block %d execution phase interval is invalid", block.Number)
				continue
			}
			if !previous.IsZero() && started.Before(previous) {
				addIssue(verification, "block %d execution overlaps or precedes the declared schedule", block.Number)
			}
			previous = finished
		}
	}
}

func checkBlockFiles(verification *VerifyResult, dir string, blocks []Block) {
	blockDir := filepath.Join(dir, "blocks")
	entries, err := os.ReadDir(blockDir)
	if err != nil {
		addIssue(verification, "blocks directory cannot be read: %v", err)
		return
	}
	if len(entries) != len(blocks) {
		addIssue(verification, "blocks directory has %d entries, want %d", len(entries), len(blocks))
	}
	for _, block := range blocks {
		name := fmt.Sprintf("%03d-%s.json", block.Number, strings.ToLower(block.PlannedOrder))
		var stored Block
		if err := decodeStrict(filepath.Join(blockDir, name), &stored); err != nil {
			addIssue(verification, "%s parse failed: %v", name, err)
		} else if !reflect.DeepEqual(stored, block) {
			addIssue(verification, "%s does not match independently reconstructed block", name)
		}
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			addIssue(verification, "blocks directory contains an unsafe entry: %s", entry.Name())
		}
	}
}

func checkFileRef(verification *VerifyResult, base string, reference FileRef, wantPath, label string) {
	if reference.Path != wantPath || !evidence.IsPortablePath(reference.Path) || !evidence.IsDigest(reference.Digest) || reference.Size <= 0 {
		addIssue(verification, "%s file reference is invalid", label)
		return
	}
	path, err := safeExistingJoin(base, reference.Path)
	if err != nil {
		addIssue(verification, "%s file reference is unsafe: %v", label, err)
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != reference.Size {
		addIssue(verification, "%s file reference size or type mismatch", label)
		return
	}
	digest, err := evidence.DigestFile(path)
	if err != nil || digest != reference.Digest {
		addIssue(verification, "%s file reference digest mismatch", label)
	}
}

func populationsOverlap(left, right benchmarkrun.Series) bool {
	seen := make(map[string]struct{}, len(left.Trials))
	for _, trial := range left.Trials {
		seen[trial.RunID] = struct{}{}
	}
	for _, trial := range right.Trials {
		if _, ok := seen[trial.RunID]; ok {
			return true
		}
	}
	return false
}

func checkBytes(verification *VerifyResult, path string, want []byte, label string) {
	got, err := readRegularLimited(path, maxABJSONBytes)
	if err != nil {
		addIssue(verification, "%s read failed: %v", label, err)
		return
	}
	if !bytes.Equal(got, want) {
		addIssue(verification, "%s does not match independently rendered content", label)
	}
}

func decodeStrict(path string, value any) error {
	content, err := readRegularLimited(path, maxABJSONBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
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

func readRegularLimited(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("file must be a non-empty regular non-symlink file of at most %d bytes", maximum)
	}
	return os.ReadFile(path)
}

func safeExistingJoin(base, reference string) (string, error) {
	if !evidence.IsPortablePath(filepath.ToSlash(reference)) {
		return "", fmt.Errorf("path is not portable")
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(base, filepath.FromSlash(reference))
	relative, err := filepath.Rel(base, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes artifact root")
	}
	current := base
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path contains symlink component")
		}
	}
	return candidate, nil
}

func inferArtifactRoot(root, dir string) string {
	dir, _ = filepath.Abs(dir)
	parent := filepath.Dir(dir)
	if filepath.Base(parent) == "benchmark-ab" && filepath.Base(filepath.Dir(parent)) == "runs" {
		return filepath.Dir(filepath.Dir(parent))
	}
	root, _ = filepath.Abs(root)
	return root
}

func checkRegular(verification *VerifyResult, path, label string) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		addIssue(verification, "%s is missing, empty, or unsafe", label)
	}
}

func checkDirectory(verification *VerifyResult, path, label string) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		addIssue(verification, "%s directory is missing or unsafe", label)
	}
}

func addIssue(verification *VerifyResult, format string, args ...any) {
	verification.Issues = append(verification.Issues, fmt.Sprintf(format, args...))
	verification.Issues = uniqueSorted(verification.Issues)
}

func canonicalUTC(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z") && parsed.Format(time.RFC3339Nano) == value
}

func Render(w io.Writer, result Result) error {
	_, err := fmt.Fprintf(w, "%s: counterbalanced benchmark %s decision=%s units=%d/%d qualification=%s\nrun_dir=%s\n", strings.ToUpper(result.Status), result.RunID, result.Decision, result.Analysis.UnitsN, result.Analysis.TotalUnits, result.Qualification.Assessment.Status, firstNonEmpty(result.ArtifactDir, result.RunDir))
	return err
}

func RenderVerify(w io.Writer, result VerifyResult) error {
	if result.IsValid() {
		_, err := fmt.Fprintf(w, "PASS: counterbalanced benchmark artifact %s\n", result.Dir)
		return err
	}
	if _, err := fmt.Fprintf(w, "FAIL: counterbalanced benchmark artifact %s\n", result.Dir); err != nil {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
