package benchmarkcampaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

// Run snapshots the complete ordered protocol before invoking any series
// runner, then executes every item sequentially. A non-passing or unavailable
// row is retained and does not prevent later independent rows from running.
func Run(root string, catalog speccatalog.Catalog, inputs []string, options Options) (Result, error) {
	options = withDefaults(options)
	root, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	started := options.Now().UTC()
	campaignID := strings.TrimSpace(options.CampaignID)
	if campaignID == "" {
		campaignID = "campaign-" + started.Format("20060102_150405")
	}
	runtimeName := strings.TrimSpace(options.Runtime)
	if runtimeName == "" {
		runtimeName = strings.TrimSpace(options.Getenv("PGWORKBENCH_RUNTIME"))
	}
	if runtimeName == "" {
		runtimeName = "docker"
	}
	subject := strings.TrimSpace(options.Subject)
	if subject == "" {
		subject = "default"
	}
	protocol, plans, err := BuildProtocol(catalog, campaignID, runtimeName, subject, inputs)
	if err != nil {
		return Result{}, err
	}
	if err := validateProtocol(protocol); err != nil {
		return Result{}, fmt.Errorf("validate benchmark campaign protocol: %w", err)
	}
	campaignParent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "benchmark-campaign"), 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare benchmark campaign artifact parent: %w", err)
	}
	seriesParent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "benchmarks"), 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare benchmark series artifact parent: %w", err)
	}
	campaignDir := filepath.Join(campaignParent, campaignID)
	paths := []string{campaignDir}
	for _, item := range protocol.OrderedSeries {
		paths = append(paths, filepath.Join(seriesParent, item.SeriesRunID))
	}
	if err := requireAbsent(paths...); err != nil {
		return Result{}, err
	}
	var protocolRef FileRef
	if err := initializeCampaignDir(campaignDir, func(stagingDir string) error {
		if err := os.Mkdir(filepath.Join(stagingDir, "executions"), 0o755); err != nil {
			return fmt.Errorf("create benchmark campaign executions directory: %w", err)
		}
		if err := writeJSONExclusive(filepath.Join(stagingDir, "protocol.json"), protocol); err != nil {
			return fmt.Errorf("write benchmark campaign protocol: %w", err)
		}
		var err error
		protocolRef, err = localFileRef(stagingDir, filepath.Join(stagingDir, "protocol.json"))
		if err != nil {
			return fmt.Errorf("validate benchmark campaign protocol artifact: %w", err)
		}
		return nil
	}); err != nil {
		return Result{CampaignID: campaignID}, err
	}

	result := Result{
		SchemaVersion:    RunSchemaVersion,
		ArtifactType:     RunArtifactType,
		SchedulerVersion: SchedulerVersion,
		Design:           AnalysisDesign,
		CampaignID:       campaignID,
		RunDir:           ".",
		Runtime:          protocol.Runtime,
		Subject:          protocol.Subject,
		StartedAt:        started.Format(time.RFC3339Nano),
		Conclusion:       "descriptive",
		Decision:         "none",
		Executions:       make([]Execution, 0, len(plans)),
		ArtifactDir:      campaignDir,
	}
	result.Protocol = protocolRef

	var runErrors []error
	for index, plan := range plans {
		declaration := protocol.OrderedSeries[index]
		seriesOptions := options.SeriesOptions
		seriesOptions.Runtime = protocol.Runtime
		seriesOptions.RunID = declaration.SeriesRunID
		seriesOptions.Subject = protocol.Subject
		seriesOptions.Now = options.Now
		seriesOptions.Stdout = options.Stdout
		seriesOptions.Stderr = options.Stderr
		series, runErr := options.RunSeries(root, catalog, plan, seriesOptions)
		record, captureErr := captureExecution(root, declaration, series, runErr)
		if captureErr != nil {
			runErrors = append(runErrors, fmt.Errorf("capture campaign item %d: %w", index+1, captureErr))
		}
		if runErr != nil {
			runErrors = append(runErrors, fmt.Errorf("campaign item %d ended non-passing", index+1))
		}
		if err := writeJSONExclusive(filepath.Join(campaignDir, "executions", fmt.Sprintf("%03d.json", index+1)), record); err != nil {
			return result, errors.Join(errors.Join(runErrors...), err)
		}
		result.Executions = append(result.Executions, record)
	}
	result.FinishedAt = options.Now().UTC().Format(time.RFC3339Nano)
	result.Status, result.Reasons = deriveTerminal(result.Executions)
	result.Digest, err = resultDigest(result)
	if err != nil {
		return result, errors.Join(errors.Join(runErrors...), err)
	}
	if err := writeResultArtifacts(campaignDir, result); err != nil {
		return result, errors.Join(errors.Join(runErrors...), err)
	}
	verification, verifyErr := Verify(root, campaignDir)
	if verifyErr != nil {
		return result, errors.Join(errors.Join(runErrors...), verifyErr)
	}
	if !verification.IsValid() {
		return result, errors.Join(errors.Join(runErrors...), fmt.Errorf("produced benchmark campaign is invalid: %s", strings.Join(verification.Issues, "; ")))
	}
	if result.Status != "passed" {
		return result, errors.Join(errors.Join(runErrors...), fmt.Errorf("benchmark campaign ended with status %s", result.Status))
	}
	return result, errors.Join(runErrors...)
}

func defaultSeriesRunner(root string, catalog speccatalog.Catalog, plan benchmarkplan.Plan, options benchmarkrun.Options) (benchmarkrun.Series, error) {
	execution, err := benchmarkrun.Start(root, catalog, plan, options)
	if err != nil {
		if execution != nil {
			return execution.Snapshot(), err
		}
		return benchmarkrun.Series{}, err
	}
	for execution.ExecutedTrials() < plan.Trials && !execution.Halted() {
		if _, err := execution.ExecuteTrial(); err != nil {
			series, finishErr := execution.Finish()
			return series, errors.Join(err, finishErr)
		}
	}
	return execution.Finish()
}

func captureExecution(root string, declaration PlannedSeries, returned benchmarkrun.Series, runErr error) (Execution, error) {
	record := Execution{
		SchemaVersion:       ExecutionSchemaVersion,
		ArtifactType:        ExecutionArtifactType,
		Position:            declaration.Position,
		Benchmark:           declaration.Benchmark,
		SeriesRunID:         declaration.SeriesRunID,
		SpecDigest:          declaration.SpecDigest,
		ProtocolDigest:      declaration.ProtocolDigest,
		ComparisonKeyDigest: declaration.ComparisonKeyDigest,
		Class:               declaration.Class,
		PrimaryMetric:       declaration.PrimaryMetric,
		Direction:           declaration.Direction,
		Status:              "unavailable",
		EvidenceStatus:      "unverified",
		Reasons:             []string{},
	}
	seriesDir := filepath.Join(root, "runs", "benchmarks", declaration.SeriesRunID)
	verification, verifyErr := benchmarkartifact.Verify(root, seriesDir)
	if verifyErr == nil && verification.IsValid() && verification.Series != nil {
		series := *verification.Series
		if returned.RunID != "" && returned.RunID != declaration.SeriesRunID {
			record.Reasons = append(record.Reasons, "series runner returned an unexpected run identity")
		} else if err := checkSeriesDeclaration(declaration, series); err != nil {
			record.Reasons = append(record.Reasons, err.Error())
		} else {
			record = executionFromSeries(declaration, series)
			if runErr != nil && series.Status == "passed" {
				record = unavailableExecution(declaration, "series runner returned an error for a passing artifact")
			}
		}
	} else {
		record.Reasons = append(record.Reasons, "series artifact is unavailable or failed independent verification")
	}
	if len(record.Reasons) == 0 && record.EvidenceStatus == "unverified" {
		record.Reasons = append(record.Reasons, "series execution produced no independently verifiable artifact")
	}
	record.Reasons = uniqueSorted(record.Reasons)
	digest, err := executionDigest(record)
	if err != nil {
		return record, err
	}
	record.Digest = digest
	if record.EvidenceStatus == "unverified" {
		if verifyErr != nil {
			return record, nil
		}
		return record, nil
	}
	return record, nil
}

func executionFromSeries(declaration PlannedSeries, series benchmarkrun.Series) Execution {
	record := Execution{
		SchemaVersion:       ExecutionSchemaVersion,
		ArtifactType:        ExecutionArtifactType,
		Position:            declaration.Position,
		Benchmark:           declaration.Benchmark,
		SeriesRunID:         declaration.SeriesRunID,
		SeriesRef:           filepath.ToSlash(filepath.Join("runs", "benchmarks", series.RunID)),
		SpecDigest:          declaration.SpecDigest,
		ProtocolDigest:      declaration.ProtocolDigest,
		ComparisonKeyDigest: declaration.ComparisonKeyDigest,
		Class:               declaration.Class,
		PrimaryMetric:       declaration.PrimaryMetric,
		Direction:           declaration.Direction,
		StartedAt:           series.StartedAt,
		FinishedAt:          series.FinishedAt,
		Status:              series.Status,
		EvidenceStatus:      "verified",
		TrialsPlanned:       series.TrialsPlanned,
		TrialsValid:         series.TrialsValid,
		TrialsFailed:        series.TrialsFailed,
		TrialsInvalid:       series.TrialsInvalid,
		Reasons:             uniqueSorted(series.Reasons),
	}
	if series.Stats != nil {
		median := series.Stats.Median
		record.Median = &median
		if series.Stats.CVPct != nil {
			cv := *series.Stats.CVPct
			record.CVPct = &cv
		}
	}
	digest, _ := evidence.DigestFile(filepath.Join(series.ArtifactDir, "result.json"))
	record.ResultDigest = digest
	return record
}

func unavailableExecution(declaration PlannedSeries, reason string) Execution {
	return Execution{
		SchemaVersion:       ExecutionSchemaVersion,
		ArtifactType:        ExecutionArtifactType,
		Position:            declaration.Position,
		Benchmark:           declaration.Benchmark,
		SeriesRunID:         declaration.SeriesRunID,
		SpecDigest:          declaration.SpecDigest,
		ProtocolDigest:      declaration.ProtocolDigest,
		ComparisonKeyDigest: declaration.ComparisonKeyDigest,
		Class:               declaration.Class,
		PrimaryMetric:       declaration.PrimaryMetric,
		Direction:           declaration.Direction,
		Status:              "unavailable",
		EvidenceStatus:      "unverified",
		Reasons:             []string{reason},
	}
}

func checkSeriesDeclaration(declaration PlannedSeries, series benchmarkrun.Series) error {
	if series.RunID != declaration.SeriesRunID || series.Benchmark != declaration.Benchmark || series.SpecDigest != declaration.SpecDigest || series.ProtocolDigest != declaration.ProtocolDigest || series.ComparisonKeyDigest != declaration.ComparisonKeyDigest || series.Class != declaration.Class || series.PrimaryMetric != declaration.PrimaryMetric || series.Direction != declaration.Direction {
		return fmt.Errorf("series artifact does not match its predeclared campaign protocol")
	}
	return nil
}

func deriveTerminal(executions []Execution) (string, []string) {
	status := "passed"
	reasons := []string{"campaign rows are independent descriptive observations; no aggregate score or cross-spec verdict is defined"}
	for _, execution := range executions {
		switch execution.Status {
		case "unavailable", "failed":
			status = "failed"
		case "invalid":
			if status != "failed" {
				status = "invalid"
			}
		case "inconclusive":
			if status == "passed" {
				status = "inconclusive"
			}
		case "passed":
		default:
			status = "invalid"
		}
		if execution.Status != "passed" {
			reasons = append(reasons, fmt.Sprintf("series %d (%s) ended with status %s", execution.Position, execution.Benchmark, execution.Status))
		}
	}
	return status, uniqueSorted(reasons)
}

func withDefaults(options Options) Options {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.RunSeries == nil {
		options.RunSeries = defaultSeriesRunner
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	return options
}

func requireAbsent(paths ...string) error {
	for _, path := range paths {
		if info, err := os.Lstat(path); err == nil {
			return fmt.Errorf("refusing to overwrite immutable campaign path: %s (%s)", path, info.Mode())
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func initializeCampaignDir(path string, initialize func(string) error) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create benchmark campaign parent directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(parent, "."+filepath.Base(path)+".staging-")
	if err != nil {
		return fmt.Errorf("create benchmark campaign staging directory: %w", err)
	}
	if err := os.Chmod(stagingDir, 0o755); err != nil {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("set benchmark campaign staging directory permissions: %w", err)
	}
	stagingOwned := true
	defer func() {
		if stagingOwned {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	if err := initialize(stagingDir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		return fmt.Errorf("refusing to overwrite immutable campaign path: %s (%s)", path, info.Mode())
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stagingDir, path); err != nil {
		return fmt.Errorf("publish benchmark campaign directory: %w", err)
	}
	stagingOwned = false
	return nil
}

func localFileRef(base, path string) (FileRef, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return FileRef{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return FileRef{}, fmt.Errorf("campaign file reference must be a non-empty regular file")
	}
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return FileRef{}, err
	}
	reference := filepath.ToSlash(relative)
	if !evidence.IsPortablePath(reference) {
		return FileRef{}, fmt.Errorf("campaign file reference is not portable")
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return FileRef{}, err
	}
	return FileRef{Path: reference, Digest: digest, Size: info.Size()}, nil
}

func executionDigest(execution Execution) (string, error) {
	execution.Digest = ""
	content, err := json.Marshal(execution)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func resultDigest(result Result) (string, error) {
	result.Digest = ""
	result.ArtifactDir = ""
	content, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func writeJSONExclusive(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(content, '\n'))
	return errors.Join(writeErr, file.Close())
}

func writeResultArtifacts(dir string, result Result) error {
	if err := writeJSONExclusive(filepath.Join(dir, "result.json"), result); err != nil {
		return err
	}
	return writeBytesExclusive(filepath.Join(dir, "summary.md"), summaryBytes(result))
}

func writeBytesExclusive(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	return errors.Join(writeErr, file.Close())
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
