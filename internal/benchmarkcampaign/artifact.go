package benchmarkcampaign

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const maxArtifactBytes int64 = 16 << 20

func Resolve(root, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates, filepath.Join(root, input), filepath.Join(root, "runs", "benchmark-campaign", input))
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
	return "", fmt.Errorf("benchmark campaign directory not found: %s", input)
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

// Verify independently replays campaign derivation. It verifies each linked
// series as a standalone benchmark artifact but never compares values between
// rows, because a campaign may intentionally contain different protocols.
func Verify(root, input string) (VerifyResult, error) {
	dir, err := Resolve(root, input)
	if err != nil {
		return VerifyResult{}, err
	}
	verification := VerifyResult{Dir: dir, Issues: []string{}}
	for _, name := range []string{"protocol.json", "result.json", "summary.md"} {
		if err := checkRegular(filepath.Join(dir, name)); err != nil {
			addIssue(&verification, "%s is missing or unsafe: %v", name, err)
		}
	}
	executionsDir := filepath.Join(dir, "executions")
	if info, err := os.Lstat(executionsDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		addIssue(&verification, "executions directory is missing or unsafe")
	}

	var protocol Protocol
	if err := decodeStrict(filepath.Join(dir, "protocol.json"), &protocol); err != nil {
		addIssue(&verification, "protocol.json parse failed: %v", err)
	} else {
		verification.Protocol = &protocol
		if err := validateProtocol(protocol); err != nil {
			addIssue(&verification, "campaign protocol is invalid: %v", err)
		}
	}
	result, loadErr := Load(root, dir)
	if loadErr != nil {
		addIssue(&verification, "result.json parse failed: %v", loadErr)
		verification.Valid = false
		return verification, nil
	}
	verification.Campaign = &result
	checkResultIdentity(&verification, dir, result)

	if verification.Protocol != nil {
		checkProtocolBinding(&verification, dir, *verification.Protocol, result)
		checkExecutions(&verification, root, dir, *verification.Protocol, result)
	}
	if content, readErr := readRegularLimited(filepath.Join(dir, "summary.md"), maxArtifactBytes); readErr != nil {
		addIssue(&verification, "summary.md read failed: %v", readErr)
	} else if !bytes.Equal(content, summaryBytes(result)) {
		addIssue(&verification, "summary.md does not match independently rendered campaign")
	}
	verification.Valid = verification.IsValid()
	return verification, nil
}

func checkResultIdentity(verification *VerifyResult, dir string, result Result) {
	if result.SchemaVersion != RunSchemaVersion || result.ArtifactType != RunArtifactType || result.SchedulerVersion != SchedulerVersion || result.Design != AnalysisDesign {
		addIssue(verification, "unsupported campaign result schema, artifact type, scheduler, or design")
	}
	if !benchmarkrun.ValidRunID(result.CampaignID) || filepath.Base(dir) != result.CampaignID || result.RunDir != "." {
		addIssue(verification, "campaign identity or portable run_dir is invalid")
	}
	if result.Runtime != "docker" && result.Runtime != "native" {
		addIssue(verification, "campaign runtime is invalid")
	}
	if strings.TrimSpace(result.Subject) != result.Subject || result.Subject == "" || strings.ContainsAny(result.Subject, "\r\n") {
		addIssue(verification, "campaign subject is invalid")
	}
	if result.Conclusion != "descriptive" || result.Decision != "none" {
		addIssue(verification, "campaign must remain descriptive and decision-free")
	}
	if len(result.Executions) == 0 || result.Executions == nil || result.Reasons == nil {
		addIssue(verification, "campaign executions and reasons must be present")
	}
	if !canonicalUTC(result.StartedAt) || !canonicalUTC(result.FinishedAt) {
		addIssue(verification, "campaign timestamps must be canonical UTC RFC3339")
	} else {
		started, _ := time.Parse(time.RFC3339Nano, result.StartedAt)
		finished, _ := time.Parse(time.RFC3339Nano, result.FinishedAt)
		if finished.Before(started) {
			addIssue(verification, "campaign finishes before it starts")
		}
	}
	if !evidence.IsDigest(result.Digest) {
		addIssue(verification, "campaign result digest is invalid")
	} else if digest, err := resultDigest(result); err != nil || digest != result.Digest {
		addIssue(verification, "campaign result digest mismatch")
	}
	if !reflect.DeepEqual(result.Reasons, uniqueSorted(result.Reasons)) {
		addIssue(verification, "campaign reasons are not sorted and unique")
	}
	status, reasons := deriveTerminal(result.Executions)
	if result.Status != status || !reflect.DeepEqual(result.Reasons, reasons) {
		addIssue(verification, "campaign terminal status or reasons do not match independent derivation")
	}
	checkFileRef(verification, dir, result.Protocol, "protocol.json")
}

func checkProtocolBinding(verification *VerifyResult, dir string, protocol Protocol, result Result) {
	if protocol.CampaignID != result.CampaignID || protocol.Runtime != result.Runtime || protocol.Subject != result.Subject || len(protocol.OrderedSeries) != len(result.Executions) {
		addIssue(verification, "campaign result does not match its predeclared protocol")
	}
	ref, err := localFileRef(dir, filepath.Join(dir, "protocol.json"))
	if err != nil || !reflect.DeepEqual(ref, result.Protocol) {
		addIssue(verification, "campaign protocol file reference mismatch")
	}
}

func checkExecutions(verification *VerifyResult, root, dir string, protocol Protocol, result Result) {
	entries, err := os.ReadDir(filepath.Join(dir, "executions"))
	if err != nil {
		addIssue(verification, "read executions directory: %v", err)
		return
	}
	if len(entries) != len(protocol.OrderedSeries) {
		addIssue(verification, "execution receipt count does not match predeclared protocol")
	}
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil || entry.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			addIssue(verification, "execution receipt directory contains unsafe entry: %s", entry.Name())
		}
	}

	artifactRoot := inferArtifactRoot(root, dir)
	for index, declaration := range protocol.OrderedSeries {
		if index >= len(result.Executions) {
			break
		}
		stored := result.Executions[index]
		var receipt Execution
		path := filepath.Join(dir, "executions", fmt.Sprintf("%03d.json", index+1))
		if err := decodeStrict(path, &receipt); err != nil {
			addIssue(verification, "execution receipt %d parse failed: %v", index+1, err)
			continue
		}
		if !reflect.DeepEqual(receipt, stored) {
			addIssue(verification, "execution receipt %d does not match result.json", index+1)
		}
		checkExecutionIdentity(verification, declaration, stored)
		if stored.EvidenceStatus == "verified" {
			checkVerifiedExecution(verification, artifactRoot, result, declaration, stored)
		} else if stored.EvidenceStatus == "unverified" {
			checkUnverifiedExecution(verification, artifactRoot, declaration, stored)
		}
	}
	checkSequentialExecutionChronology(verification, result.Executions)
}

func checkSequentialExecutionChronology(verification *VerifyResult, executions []Execution) {
	var previousFinish time.Time
	previousPosition := 0
	for _, execution := range executions {
		if execution.EvidenceStatus != "verified" {
			continue
		}
		started, startErr := time.Parse(time.RFC3339Nano, execution.StartedAt)
		finished, finishErr := time.Parse(time.RFC3339Nano, execution.FinishedAt)
		if startErr != nil || finishErr != nil {
			continue
		}
		if previousPosition != 0 && started.Before(previousFinish) {
			addIssue(verification, "linked series %d overlaps earlier verified series %d despite sequential campaign order", execution.Position, previousPosition)
		}
		if previousPosition == 0 || finished.After(previousFinish) {
			previousFinish = finished
		}
		previousPosition = execution.Position
	}
}

func checkExecutionIdentity(verification *VerifyResult, declaration PlannedSeries, execution Execution) {
	if execution.SchemaVersion != ExecutionSchemaVersion || execution.ArtifactType != ExecutionArtifactType {
		addIssue(verification, "execution %d has unsupported schema or artifact type", declaration.Position)
	}
	if execution.Position != declaration.Position || execution.Benchmark != declaration.Benchmark || execution.SeriesRunID != declaration.SeriesRunID || execution.SpecDigest != declaration.SpecDigest || execution.ProtocolDigest != declaration.ProtocolDigest || execution.ComparisonKeyDigest != declaration.ComparisonKeyDigest || execution.Class != declaration.Class || execution.PrimaryMetric != declaration.PrimaryMetric || execution.Direction != declaration.Direction {
		addIssue(verification, "execution %d does not match predeclared protocol", declaration.Position)
	}
	if !reflect.DeepEqual(execution.Reasons, uniqueSorted(execution.Reasons)) {
		addIssue(verification, "execution %d reasons are not sorted and unique", declaration.Position)
	}
	if !evidence.IsDigest(execution.Digest) {
		addIssue(verification, "execution %d digest is invalid", declaration.Position)
	} else if digest, err := executionDigest(execution); err != nil || digest != execution.Digest {
		addIssue(verification, "execution %d digest mismatch", declaration.Position)
	}
	if execution.EvidenceStatus != "verified" && execution.EvidenceStatus != "unverified" {
		addIssue(verification, "execution %d evidence status is invalid", declaration.Position)
	}
}

func checkVerifiedExecution(verification *VerifyResult, artifactRoot string, campaign Result, declaration PlannedSeries, execution Execution) {
	wantRef := filepath.ToSlash(filepath.Join("runs", "benchmarks", declaration.SeriesRunID))
	if execution.SeriesRef != wantRef || !evidence.IsDigest(execution.ResultDigest) || execution.Status == "unavailable" {
		addIssue(verification, "verified execution %d has an invalid series reference", declaration.Position)
		return
	}
	checked, err := benchmarkartifact.Verify(artifactRoot, execution.SeriesRef)
	if err != nil || !checked.IsValid() || checked.Series == nil {
		addIssue(verification, "linked series %d failed independent verification", declaration.Position)
		return
	}
	series := *checked.Series
	if err := checkSeriesDeclaration(declaration, series); err != nil || series.Runtime != campaign.Runtime || series.Subject != campaign.Subject {
		addIssue(verification, "linked series %d does not match campaign protocol", declaration.Position)
		return
	}
	want := executionFromSeries(declaration, series)
	want.Digest, _ = executionDigest(want)
	if !reflect.DeepEqual(want, execution) {
		addIssue(verification, "execution %d does not match independently derived linked series row", declaration.Position)
	}
	if !intervalContains(campaign.StartedAt, campaign.FinishedAt, execution.StartedAt, execution.FinishedAt) {
		addIssue(verification, "linked series %d lies outside campaign interval", declaration.Position)
	}
}

func checkUnverifiedExecution(verification *VerifyResult, artifactRoot string, declaration PlannedSeries, execution Execution) {
	if execution.Status != "unavailable" || execution.SeriesRef != "" || execution.ResultDigest != "" || execution.StartedAt != "" || execution.FinishedAt != "" || execution.Median != nil || execution.CVPct != nil || execution.TrialsPlanned != 0 || execution.TrialsValid != 0 || execution.TrialsFailed != 0 || execution.TrialsInvalid != 0 || len(execution.Reasons) == 0 {
		addIssue(verification, "unverified execution %d contains unsupported performance claims", declaration.Position)
	}
	if checked, err := benchmarkartifact.Verify(artifactRoot, filepath.ToSlash(filepath.Join("runs", "benchmarks", declaration.SeriesRunID))); err == nil && checked.IsValid() && checked.Series != nil {
		addIssue(verification, "execution %d claims unavailable evidence but a valid linked series exists", declaration.Position)
	}
}

func checkFileRef(verification *VerifyResult, base string, reference FileRef, wantPath string) {
	if reference.Path != wantPath || !evidence.IsPortablePath(reference.Path) || !evidence.IsDigest(reference.Digest) || reference.Size <= 0 {
		addIssue(verification, "campaign %s reference is invalid", wantPath)
		return
	}
	path, err := safeExistingJoin(base, reference.Path)
	if err != nil {
		addIssue(verification, "campaign %s reference is unsafe", wantPath)
		return
	}
	info, err := os.Lstat(path)
	if err != nil || info.Size() != reference.Size {
		addIssue(verification, "campaign %s reference size mismatch", wantPath)
		return
	}
	digest, err := evidence.DigestFile(path)
	if err != nil || digest != reference.Digest {
		addIssue(verification, "campaign %s reference digest mismatch", wantPath)
	}
}

func intervalContains(outerStart, outerFinish, innerStart, innerFinish string) bool {
	start, err1 := time.Parse(time.RFC3339Nano, outerStart)
	finish, err2 := time.Parse(time.RFC3339Nano, outerFinish)
	innerStarted, err3 := time.Parse(time.RFC3339Nano, innerStart)
	innerFinished, err4 := time.Parse(time.RFC3339Nano, innerFinish)
	return err1 == nil && err2 == nil && err3 == nil && err4 == nil && !innerFinished.Before(innerStarted) && !innerStarted.Before(start) && !innerFinished.After(finish)
}

func summaryBytes(result Result) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Benchmark campaign `%s`\n\n", result.CampaignID)
	fmt.Fprintf(&builder, "- Runtime: `%s`\n", result.Runtime)
	fmt.Fprintf(&builder, "- Subject: %s\n", escapeTable(result.Subject))
	fmt.Fprintf(&builder, "- Status: `%s`\n", result.Status)
	builder.WriteString("- Conclusion: **descriptive only**\n")
	builder.WriteString("- Decision: **none**\n\n")
	builder.WriteString("| # | Benchmark | Series | Class | Primary metric | Direction | Status | Evidence | Valid trials | Median | CV % | Protocol identity | Comparison identity |\n")
	builder.WriteString("| ---: | --- | --- | --- | --- | --- | --- | --- | ---: | ---: | ---: | --- | --- |\n")
	for _, execution := range result.Executions {
		median, cv := "", ""
		if execution.Median != nil {
			median = fmt.Sprintf("%.12g", *execution.Median)
		}
		if execution.CVPct != nil {
			cv = fmt.Sprintf("%.6g", *execution.CVPct)
		}
		fmt.Fprintf(&builder, "| %d | `%s` | `%s` | %s | `%s` | %s | %s | %s | %d | %s | %s | `%s` | `%s` |\n", execution.Position, execution.Benchmark, execution.SeriesRunID, execution.Class, execution.PrimaryMetric, execution.Direction, execution.Status, execution.EvidenceStatus, execution.TrialsValid, median, cv, execution.ProtocolDigest, execution.ComparisonKeyDigest)
	}
	builder.WriteString("\nRows are independently executed observations and may intentionally be non-comparable. This artifact defines no aggregate/composite score, winner, regression verdict, or causal decision.\n")
	return []byte(builder.String())
}

func decodeStrict(path string, value any) error {
	content, err := readRegularLimited(path, maxArtifactBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func readRegularLimited(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("file is empty, oversized, symlinked, or non-regular")
	}
	return os.ReadFile(path)
}

func checkRegular(path string) error {
	_, err := readRegularLimited(path, maxArtifactBytes)
	return err
}

func canonicalUTC(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z") && parsed.Location() == time.UTC && parsed.Format(time.RFC3339Nano) == value
}

func inferArtifactRoot(root, dir string) string {
	dir, _ = filepath.Abs(dir)
	marker := filepath.Join("runs", "benchmark-campaign", filepath.Base(dir))
	if strings.HasSuffix(dir, marker) {
		candidate := strings.TrimSuffix(dir, marker)
		candidate = strings.TrimSuffix(candidate, string(filepath.Separator))
		if candidate != "" {
			return candidate
		}
	}
	root, _ = filepath.Abs(root)
	return root
}

func safeExistingJoin(root, reference string) (string, error) {
	if !evidence.IsPortablePath(filepath.ToSlash(reference)) {
		return "", fmt.Errorf("reference is not portable")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(reference))
	relative, err := filepath.Rel(root, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference escapes artifact root")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	if resolved != filepath.Join(resolvedRoot, filepath.FromSlash(reference)) {
		return "", fmt.Errorf("reference resolves through a symlink")
	}
	return resolved, nil
}

func escapeTable(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " ")
}

func addIssue(result *VerifyResult, format string, values ...any) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, values...))
	result.Issues = uniqueSorted(result.Issues)
}

func Render(w io.Writer, result Result) error {
	_, err := fmt.Fprintf(w, "%s: benchmark campaign %s series=%d conclusion=%s decision=%s\ncampaign_dir=%s\n", strings.ToUpper(result.Status), result.CampaignID, len(result.Executions), result.Conclusion, result.Decision, result.ArtifactDir)
	return err
}

func RenderJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func RenderVerify(w io.Writer, result VerifyResult) error {
	status := "PASS"
	if !result.IsValid() {
		status = "FAIL"
	}
	if _, err := fmt.Fprintf(w, "%s: benchmark campaign %s\n", status, result.Dir); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(w, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}
