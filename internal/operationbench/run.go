package operationbench

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

type ExperimentRunner func(string, speccatalog.Catalog, string, experimentrun.Options) (experimentrun.Result, error)
type RunVerifier func(string, string) (runverify.Result, error)

type Options struct {
	Runtime       string
	RunID         string
	PackID        string
	PackVersion   string
	PackDigest    string
	EngineVersion string
	EngineCommit  string
	BinaryPath    string
	NativeBindir  string
	Stdout        io.Writer
	Stderr        io.Writer
	Now           func() time.Time
	Getenv        func(string) string
	RunExperiment ExperimentRunner
	VerifyRun     RunVerifier
}

func Run(root string, input string, options Options) (Series, error) {
	options = defaultOptions(options)
	spec, err := NewCatalog(root).Load(input)
	if err != nil {
		return Series{}, err
	}
	runtimeName := firstNonEmpty(options.Runtime, options.Getenv("PGWORKBENCH_RUNTIME"), "docker")
	if !supportsRuntime(spec, runtimeName) {
		return Series{}, fmt.Errorf("operation benchmark %s does not support runtime %q", spec.ID, runtimeName)
	}
	runtimePorts, err := resolveRuntimePorts(options.Getenv)
	if err != nil {
		return Series{}, err
	}
	runtimePortsDigest := digestRuntimePorts(runtimePorts)
	runtimePortEnv := append(runtimePorts.environment(), "PGWORKBENCH_RUNTIME_PORTS_DIGEST="+runtimePortsDigest)
	if !evidence.IsDigest(options.PackDigest) {
		return Series{}, fmt.Errorf("operation benchmark requires a canonical scenario pack digest")
	}
	engine, err := inspectEngineBinary(options.BinaryPath)
	if err != nil {
		return Series{}, fmt.Errorf("inspect operation benchmark engine binary: %w", err)
	}
	options.BinaryPath = engine.Path
	options.EngineVersion = runstate.NormalizeEngineVersion(options.EngineVersion)
	options.EngineCommit = runstate.NormalizeEngineCommit(options.EngineCommit)
	var nativeInstallation *nativetoolchain.Installation
	if runtimeName == "native" {
		installation, inspectErr := nativetoolchain.Inspect(strings.TrimSpace(options.NativeBindir))
		if inspectErr != nil {
			return Series{}, fmt.Errorf("inspect operation benchmark native toolchain: %w", inspectErr)
		}
		nativeInstallation = &installation
	}
	inputs, err := collectInputClosure(root, spec)
	if err != nil {
		return Series{}, fmt.Errorf("resolve operation input closure: %w", err)
	}
	inputsDigest, err := inputClosureDigest(inputs)
	if err != nil {
		return Series{}, err
	}
	experimentPath := filepath.Join(root, "experiments", filepath.FromSlash(spec.ExperimentSpec)+".env")
	experimentDigest, err := evidence.DigestFile(experimentPath)
	if err != nil {
		return Series{}, fmt.Errorf("digest experiment spec: %w", err)
	}
	started := options.Now().UTC()
	runID := firstNonEmpty(options.RunID, options.Getenv("OPERATION_BENCHMARK_RUN_ID"))
	if runID == "" {
		runID = sanitizeID(spec.ID) + "-operation-" + started.Format("20060102_150405")
	}
	if !validRunID(runID) {
		return Series{}, fmt.Errorf("invalid operation benchmark run id %q", runID)
	}
	seriesParent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "operation-benchmarks"), 0o755)
	if err != nil {
		return Series{}, fmt.Errorf("prepare operation benchmark artifact parent: %w", err)
	}
	seriesDir := filepath.Join(seriesParent, runID)
	if _, err := os.Lstat(seriesDir); err == nil {
		return Series{}, fmt.Errorf("operation benchmark series already exists: %s", seriesDir)
	} else if !os.IsNotExist(err) {
		return Series{}, fmt.Errorf("inspect operation benchmark series destination: %w", err)
	}
	stagingDir, err := os.MkdirTemp(seriesParent, "."+runID+".staging-")
	if err != nil {
		return Series{}, fmt.Errorf("create operation benchmark staging directory: %w", err)
	}
	stagingOwned := true
	defer func() {
		if stagingOwned {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	series := Series{
		SchemaVersion:             SeriesSchemaVersionV2,
		ArtifactType:              SeriesArtifactType,
		Operation:                 spec.ID,
		Name:                      spec.Name,
		Description:               spec.Description,
		Classification:            Classification,
		DecisionEligible:          false,
		Assurance:                 spec.Assurance,
		RunID:                     runID,
		RunDir:                    ".",
		ArtifactDir:               seriesDir,
		SpecRef:                   spec.Path,
		SpecDigest:                spec.Digest,
		ExperimentSpec:            spec.ExperimentSpec,
		ExperimentRef:             filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(spec.ExperimentSpec)+".env")),
		ExperimentDigest:          experimentDigest,
		EngineVersion:             options.EngineVersion,
		EngineCommit:              options.EngineCommit,
		EngineBinaryRef:           EngineBinaryRef,
		EngineBinaryDigest:        engine.Digest,
		PackDigest:                options.PackDigest,
		InputsDigest:              inputsDigest,
		Inputs:                    append([]InputFile(nil), inputs...),
		Runtime:                   runtimeName,
		RuntimePorts:              &runtimePorts,
		RuntimePortsDigest:        runtimePortsDigest,
		RuntimePortsPresent:       true,
		RuntimePortsDigestPresent: true,
		Measurement:               spec.Measurement,
		TrialsPlanned:             spec.Trials,
		MaxCVPct:                  spec.MaxCVPct,
		StartedAt:                 started.Format(time.RFC3339Nano),
		Status:                    "passed",
		Reasons:                   []string{},
		Trials:                    make([]Trial, 0, spec.Trials),
	}
	if nativeInstallation != nil {
		series.NativeToolchainDigest = nativeInstallation.Manifest.Digest
		series.NativeToolchainManifestRef = NativeManifestRef
		series.NativeToolchainProvenance = nativetoolchain.Unattested
	}
	if err := snapshotFile(filepath.Join(root, filepath.FromSlash(spec.Path)), filepath.Join(stagingDir, "operation-spec.json")); err != nil {
		return series, err
	}
	if err := snapshotFile(experimentPath, filepath.Join(stagingDir, "experiment-spec.env")); err != nil {
		return series, err
	}
	if err := snapshotInputs(root, stagingDir, inputs); err != nil {
		return series, err
	}
	if err := snapshotEngineBinary(engine, filepath.Join(stagingDir, filepath.FromSlash(EngineBinaryRef))); err != nil {
		return series, fmt.Errorf("snapshot operation benchmark engine binary: %w", err)
	}
	if nativeInstallation != nil {
		if err := nativetoolchain.Snapshot(*nativeInstallation, filepath.Join(stagingDir, "protocol", "native-toolchain")); err != nil {
			return series, fmt.Errorf("snapshot operation benchmark native toolchain: %w", err)
		}
	}
	if err := os.Rename(stagingDir, seriesDir); err != nil {
		return series, fmt.Errorf("publish initialized operation benchmark series: %w", err)
	}
	stagingOwned = false

	for number := 1; number <= spec.Trials; number++ {
		if err := revalidateEngineBinary(engine); err != nil {
			series.Status = "failed"
			series.Reasons = append(series.Reasons, fmt.Sprintf("trial %d pre-execution engine binary: %v", number, err))
			break
		}
		if nativeInstallation != nil {
			if err := nativetoolchain.Revalidate(*nativeInstallation); err != nil {
				series.Status = "failed"
				series.Reasons = append(series.Reasons, fmt.Sprintf("trial %d pre-execution native toolchain: %v", number, err))
				break
			}
		}
		if err := verifyLiveInputs(root, inputs); err != nil {
			series.Status = "failed"
			series.Reasons = append(series.Reasons, fmt.Sprintf("trial %d pre-execution input closure: %v", number, err))
			break
		}
		trialRunID := fmt.Sprintf("%s-trial-%03d", runID, number)
		trialEnv := append([]string{"ENV_FILE=.env.example"}, runtimePortEnv...)
		if nativeInstallation != nil {
			trialEnv = append(trialEnv,
				"PGWORKBENCH_NATIVE_BINDIR="+nativeInstallation.Bindir,
				"PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST="+nativeInstallation.Manifest.Digest,
			)
		}
		result, runErr := options.RunExperiment(root, speccatalog.New(root), spec.ExperimentSpec, experimentrun.Options{
			Runtime:          runtimeName,
			RunID:            trialRunID,
			Env:              trialEnv,
			ExactEnvironment: true,
			PackID:           options.PackID,
			PackVersion:      options.PackVersion,
			PackDigest:       options.PackDigest,
			EngineVersion:    options.EngineVersion,
			EngineCommit:     options.EngineCommit,
			BinaryPath:       options.BinaryPath,
			Stdout:           options.Stdout,
			Stderr:           options.Stderr,
		})
		trial, trialErr := collectTrial(root, spec, number, result, runErr, options.PackDigest, options.VerifyRun)
		if identityErr := revalidateEngineBinary(engine); identityErr != nil && trialErr == nil {
			trialErr = fmt.Errorf("post-execution engine binary: %w", identityErr)
		}
		if nativeInstallation != nil {
			if identityErr := nativetoolchain.Revalidate(*nativeInstallation); identityErr != nil && trialErr == nil {
				trialErr = fmt.Errorf("post-execution native toolchain: %w", identityErr)
			}
		}
		if inputErr := verifyLiveInputs(root, inputs); inputErr != nil && trialErr == nil {
			trialErr = fmt.Errorf("post-execution input closure: %w", inputErr)
		}
		if series.ExecutionParametersDigest == "" && evidence.IsDigest(trial.ExecutionParametersDigest) {
			series.ExecutionParametersDigest = trial.ExecutionParametersDigest
		}
		if trial.Status == "passed" {
			series.TrialsValid++
			if series.Topology == "" {
				series.Topology = result.Topology
			} else if series.Topology != result.Topology {
				trialErr = fmt.Errorf("trial topology changed from %s to %s", series.Topology, result.Topology)
			}
		} else {
			series.TrialsFailed++
		}
		series.Trials = append(series.Trials, trial)
		if trialErr != nil {
			series.Status = "failed"
			series.Reasons = append(series.Reasons, fmt.Sprintf("trial %d: %v", number, trialErr))
			break
		}
		if current, digestErr := evidence.DigestFile(experimentPath); digestErr != nil || current != experimentDigest {
			series.Status = "failed"
			series.Reasons = append(series.Reasons, "experiment spec changed during the series")
			break
		}
		if current, digestErr := evidence.DigestFile(filepath.Join(root, filepath.FromSlash(spec.Path))); digestErr != nil || current != spec.Digest {
			series.Status = "failed"
			series.Reasons = append(series.Reasons, "operation benchmark spec changed during the series")
			break
		}
	}
	finishSeries(&series, options.Now().UTC())
	if err := writeSeries(seriesDir, series); err != nil {
		return series, err
	}
	if series.Status != "failed" {
		verification, verifyErr := Verify(root, seriesDir)
		if verifyErr != nil {
			return series, fmt.Errorf("verify completed operation benchmark series: %w", verifyErr)
		}
		if !verification.Valid {
			return series, fmt.Errorf("completed operation benchmark series failed independent verification: %s", strings.Join(verification.Issues, "; "))
		}
	}
	if series.Status == "failed" {
		return series, fmt.Errorf("operation benchmark failed: %s", strings.Join(series.Reasons, "; "))
	}
	return series, nil
}

func resolveRuntimePorts(getenv func(string) string) (RuntimePorts, error) {
	ports := RuntimePorts{}
	resolved := []struct {
		name        string
		defaultPort int
		destination *int
	}{
		{"POSTGRES_PORT", 55433, &ports.Postgres},
		{"POSTGRES_REPLICA_PORT", 55434, &ports.Replica},
		{"POSTGRES_LOGICAL_SUBSCRIBER_PORT", 55435, &ports.LogicalSubscriber},
		{"PGBOUNCER_PORT", 56432, &ports.PgBouncer},
		{"POSTGRES_UPGRADE_OLD_PORT", 55436, &ports.UpgradeOld},
		{"POSTGRES_UPGRADE_NEW_PORT", 55437, &ports.UpgradeNew},
	}
	for _, item := range resolved {
		value := strings.TrimSpace(getenv(item.name))
		if value == "" {
			value = strconv.Itoa(item.defaultPort)
		}
		port, err := strconv.Atoi(value)
		if err != nil || strconv.Itoa(port) != value {
			return RuntimePorts{}, fmt.Errorf("operation benchmark %s must be a canonical integer, got %q", item.name, value)
		}
		*item.destination = port
	}
	if err := validateRuntimePorts(ports); err != nil {
		return RuntimePorts{}, err
	}
	return ports, nil
}

func (ports RuntimePorts) values() []struct {
	name string
	port int
} {
	return []struct {
		name string
		port int
	}{
		{"POSTGRES_PORT", ports.Postgres},
		{"POSTGRES_REPLICA_PORT", ports.Replica},
		{"POSTGRES_LOGICAL_SUBSCRIBER_PORT", ports.LogicalSubscriber},
		{"PGBOUNCER_PORT", ports.PgBouncer},
		{"POSTGRES_UPGRADE_OLD_PORT", ports.UpgradeOld},
		{"POSTGRES_UPGRADE_NEW_PORT", ports.UpgradeNew},
	}
}

func (ports RuntimePorts) environment() []string {
	values := ports.values()
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.name+"="+strconv.Itoa(value.port))
	}
	return result
}

func validateRuntimePorts(ports RuntimePorts) error {
	used := make(map[int]string, 6)
	for _, value := range ports.values() {
		if value.port < 1024 || value.port > 65535 {
			return fmt.Errorf("operation benchmark %s must be between 1024 and 65535, got %d", value.name, value.port)
		}
		if previous := used[value.port]; previous != "" {
			return fmt.Errorf("operation benchmark runtime ports must be distinct: %s and %s both resolve to %d", previous, value.name, value.port)
		}
		used[value.port] = value.name
	}
	return nil
}

func digestRuntimePorts(ports RuntimePorts) string {
	content, err := json.Marshal(ports)
	if err != nil {
		panic(err)
	}
	return evidence.DigestBytes(content)
}

func collectTrial(root string, spec Spec, number int, result experimentrun.Result, runErr error, expectedPackDigest string, verifier RunVerifier) (Trial, error) {
	trial := Trial{
		Trial:      number,
		RunID:      result.RunID,
		RunRef:     filepath.ToSlash(filepath.Join("runs", result.RunID)),
		StartedAt:  canonicalTimestamp(result.StartedAt),
		FinishedAt: canonicalTimestamp(result.FinishedAt),
		DurationMS: result.DurationMS,
		Status:     "failed",
		Reasons:    []string{},
	}
	if runErr != nil {
		trial.Reasons = append(trial.Reasons, "experiment runner: "+runErr.Error())
	}
	if result.RunID == "" || !validRunID(result.RunID) {
		return trial, fmt.Errorf("experiment runner returned invalid run id")
	}
	runDir := filepath.Join(root, "runs", result.RunID)
	verification, verifyErr := verifier(root, runDir)
	if verifyErr != nil {
		trial.Reasons = append(trial.Reasons, "linked run verifier: "+verifyErr.Error())
	} else if !verification.Valid() {
		trial.Reasons = append(trial.Reasons, "linked run invalid: "+strings.Join(verification.Issues, "; "))
	} else {
		trial.ExperimentVerified = true
	}
	if verification.Verdict != nil {
		trial.StartedAt = canonicalTimestamp(verification.Verdict.StartedAt)
		trial.FinishedAt = canonicalTimestamp(verification.Verdict.FinishedAt)
		if duration, err := timestampDurationMS(trial.StartedAt, trial.FinishedAt); err == nil {
			trial.DurationMS = duration
		}
	}
	manifest, manifestErr := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if manifestErr != nil {
		trial.Reasons = append(trial.Reasons, "linked run manifest: "+manifestErr.Error())
	} else {
		trial.ExecutionParametersDigest = manifest["execution_parameters_digest"]
		trial.ExperimentIdentityDigest = manifest["experiment_identity_digest"]
		trial.PackDigest = manifest["pack_digest"]
		if !evidence.IsDigest(trial.ExecutionParametersDigest) || !evidence.IsDigest(trial.ExperimentIdentityDigest) || !evidence.IsDigest(trial.PackDigest) || trial.PackDigest != expectedPackDigest {
			trial.Reasons = append(trial.Reasons, "linked run execution or experiment identity digest is invalid")
		}
	}
	runDigest, digestErr := digestTree(runDir)
	if digestErr != nil {
		trial.Reasons = append(trial.Reasons, "linked run digest: "+digestErr.Error())
	} else {
		trial.RunDigest = runDigest
	}
	if runErr == nil && result.Passed() && trial.ExperimentVerified && digestErr == nil && manifestErr == nil && evidence.IsDigest(trial.ExecutionParametersDigest) && evidence.IsDigest(trial.ExperimentIdentityDigest) && trial.PackDigest == expectedPackDigest {
		value, operationResult, resultRef, resultDigest, err := derivePrimaryValue(runDir, spec)
		if err != nil {
			trial.Reasons = append(trial.Reasons, err.Error())
		} else {
			trial.PrimaryValue = &value
			trial.OperationResult = operationResult
			trial.ResultRef = resultRef
			trial.ResultDigest = resultDigest
			trial.Status = "passed"
		}
	}
	if trial.Status != "passed" {
		if len(trial.Reasons) == 0 {
			trial.Reasons = append(trial.Reasons, "experiment did not pass")
		}
		return trial, fmt.Errorf("%s", strings.Join(trial.Reasons, "; "))
	}
	return trial, nil
}

func derivePrimaryValue(runDir string, spec Spec) (float64, *OperationResult, string, string, error) {
	if spec.Measurement.Basis == "linked-run-wall-clock" {
		manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
		if err != nil {
			return 0, nil, "", "", fmt.Errorf("parse linked run manifest: %w", err)
		}
		verdict, err := envfile.Parse(filepath.Join(runDir, "verdict.env"))
		if err != nil {
			return 0, nil, "", "", fmt.Errorf("parse linked run verdict: %w", err)
		}
		duration, err := timestampDurationMS(manifest["started_at"], verdict["finished_at"])
		return float64(duration), nil, "", "", err
	}
	path, err := safeExistingJoin(runDir, spec.Measurement.ResultPath)
	if err != nil {
		return 0, nil, "", "", err
	}
	var result OperationResult
	if err := decodeStrictFile(path, &result); err != nil {
		return 0, nil, "", "", fmt.Errorf("parse operation result: %w", err)
	}
	if err := validateOperationResult(result, spec); err != nil {
		return 0, nil, "", "", err
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return 0, nil, "", "", err
	}
	return result.PrimaryMetric.Value, &result, spec.Measurement.ResultPath, digest, nil
}

func validateOperationResult(result OperationResult, spec Spec) error {
	if result.SchemaVersion != ResultSchemaVersion || result.ArtifactType != ResultArtifactType {
		return fmt.Errorf("operation result has unsupported schema or artifact type")
	}
	if result.OperationID != spec.ID || strings.TrimSpace(result.Variant) == "" {
		return fmt.Errorf("operation result identity does not match spec")
	}
	metric := result.PrimaryMetric
	if metric.Name != spec.Measurement.Metric || metric.Unit != spec.Measurement.Unit || metric.Direction != spec.Measurement.Direction {
		return fmt.Errorf("operation result primary metric does not match spec")
	}
	if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) || metric.Value < 0 {
		return fmt.Errorf("operation result primary value must be finite and non-negative")
	}
	if result.Measurement.Basis != "postgres-server-clock" && result.Measurement.Basis != "client-monotonic-clock" && result.Measurement.Basis != "tool-reported" {
		return fmt.Errorf("operation result measurement basis is unsupported")
	}
	if result.Measurement.Scope != spec.Measurement.Scope {
		return fmt.Errorf("operation result measurement scope does not match spec")
	}
	return nil
}

func finishSeries(series *Series, finished time.Time) {
	series.FinishedAt = finished.UTC().Format(time.RFC3339Nano)
	// Linked experiment manifests currently use whole-second timestamps. Expand
	// the outer series envelope to those independently recorded boundaries so a
	// sub-second orchestration timestamp can never make a valid first or last
	// trial appear to fall outside its parent series.
	seriesStarted, startedErr := time.Parse(time.RFC3339Nano, series.StartedAt)
	seriesFinished, finishedErr := time.Parse(time.RFC3339Nano, series.FinishedAt)
	for _, trial := range series.Trials {
		trialStarted, trialStartedErr := time.Parse(time.RFC3339Nano, trial.StartedAt)
		if startedErr == nil && trialStartedErr == nil && trialStarted.Before(seriesStarted) {
			seriesStarted = trialStarted
			series.StartedAt = trialStarted.UTC().Format(time.RFC3339Nano)
		}
		trialFinished, trialFinishedErr := time.Parse(time.RFC3339Nano, trial.FinishedAt)
		if finishedErr == nil && trialFinishedErr == nil && trialFinished.After(seriesFinished) {
			seriesFinished = trialFinished
			series.FinishedAt = trialFinished.UTC().Format(time.RFC3339Nano)
		}
	}
	if series.Status == "passed" && (len(series.Trials) != series.TrialsPlanned || series.TrialsValid != series.TrialsPlanned) {
		series.Status = "failed"
		series.Reasons = append(series.Reasons, "not all exact trials passed")
	}
	if series.Status == "passed" {
		if !evidence.IsDigest(series.ExecutionParametersDigest) {
			series.Status = "failed"
			series.Reasons = append(series.Reasons, "series execution parameters digest is missing")
		} else {
			for _, trial := range series.Trials {
				if trial.Status == "passed" && trial.ExecutionParametersDigest != series.ExecutionParametersDigest {
					series.Status = "failed"
					series.Reasons = append(series.Reasons, "execution parameters changed across exact trials")
					break
				}
			}
		}
	}
	if series.Status == "passed" {
		experimentIdentityDigest := ""
		for _, trial := range series.Trials {
			if trial.Status != "passed" {
				continue
			}
			if !evidence.IsDigest(trial.ExperimentIdentityDigest) {
				series.Status = "failed"
				series.Reasons = append(series.Reasons, "trial experiment identity digest is missing")
				break
			}
			if experimentIdentityDigest == "" {
				experimentIdentityDigest = trial.ExperimentIdentityDigest
			} else if trial.ExperimentIdentityDigest != experimentIdentityDigest {
				series.Status = "failed"
				series.Reasons = append(series.Reasons, "experiment identity changed across exact trials")
				break
			}
		}
	}
	if series.TrialsValid >= 2 {
		values := make([]float64, 0, series.TrialsValid)
		for _, trial := range series.Trials {
			if trial.Status == "passed" && trial.PrimaryValue != nil {
				values = append(values, *trial.PrimaryValue)
			}
		}
		stats, err := pgbenchresult.Summarize(values)
		if err != nil {
			series.Status = "failed"
			series.Reasons = append(series.Reasons, "statistics: "+err.Error())
		} else {
			series.Stats = &stats
			if stats.CVPct == nil || *stats.CVPct > series.MaxCVPct {
				if series.Status == "passed" {
					series.Status = "inconclusive"
				}
				series.Reasons = append(series.Reasons, "coefficient of variation exceeds max_cv_pct")
			}
		}
	}
	if series.Status == "passed" && len(series.Reasons) != 0 {
		series.Status = "failed"
	}
}

func defaultOptions(options Options) Options {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.RunExperiment == nil {
		options.RunExperiment = experimentrun.Run
	}
	if options.VerifyRun == nil {
		options.VerifyRun = runverify.Verify
	}
	return options
}

func writeSeries(dir string, series Series) error {
	if err := os.Mkdir(filepath.Join(dir, "trials"), 0o755); err != nil {
		return err
	}
	for index, trial := range series.Trials {
		if err := writeJSON(filepath.Join(dir, "trials", fmt.Sprintf("%03d.json", index+1)), trial); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(dir, "result.json"), series); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), SummaryBytes(series), 0o644)
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}

func snapshotFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("snapshot source is unsafe: %s", source)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, content, 0o644)
}

func digestTree(root string) (string, error) {
	type entry struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	}
	var entries []entry
	err := filepath.WalkDir(root, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() {
			return nil
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked run contains unsafe path: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !evidence.IsPortablePath(rel) {
			return fmt.Errorf("linked run contains non-portable path: %s", rel)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{Path: rel, Size: info.Size(), Digest: digest})
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("linked run is empty")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	content, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func timestampDurationMS(started, finished string) (int64, error) {
	start, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return 0, fmt.Errorf("invalid linked run started_at: %w", err)
	}
	finish, err := time.Parse(time.RFC3339Nano, finished)
	if err != nil {
		return 0, fmt.Errorf("invalid linked run finished_at: %w", err)
	}
	if finish.Before(start) {
		return 0, fmt.Errorf("linked run finishes before it starts")
	}
	return finish.Sub(start).Milliseconds(), nil
}

func canonicalTimestamp(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizeID(value string) string {
	return strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("_.-", char) {
			return char
		}
		return '_'
	}, value)
}

func validRunID(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= 200 && sanitizeID(value) == value
}

func safeJoin(root, relative string) (string, error) {
	if !evidence.IsPortablePath(relative) {
		return "", fmt.Errorf("unsafe portable path %q", relative)
	}
	joined := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root")
	}
	return joined, nil
}

func safeExistingJoin(root, relative string) (string, error) {
	joined, err := safeJoin(root, relative)
	if err != nil {
		return "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	canonicalJoined, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalJoined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("existing path escapes root")
	}
	return canonicalJoined, nil
}
