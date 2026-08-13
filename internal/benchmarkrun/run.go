package benchmarkrun

import (
	"bufio"
	"bytes"
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
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksettings"
	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchlog"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

const (
	SeriesSchemaVersion       = "pgworkbench.benchmark-series/v1"
	SeriesArtifactType        = "pgworkbench.benchmark-series"
	TrialSchemaVersion        = "pgworkbench.benchmark-trial/v1"
	TrialArtifactType         = "pgworkbench.benchmark-trial"
	EnvironmentSchemaVersion  = "pgworkbench.benchmark-environment/v1"
	EnvironmentArtifactType   = "pgworkbench.benchmark-environment"
	ScenarioPackSchemaVersion = "pgworkbench.benchmark-scenario-pack/v1"
	ScenarioPackArtifactType  = "pgworkbench.benchmark-scenario-pack"
	ScenarioPackInventoryRef  = "protocol/scenario-pack.json"
	NativeToolchainSeriesRef  = "protocol/native-toolchain/manifest.json"
)

type ExperimentRunner func(root string, catalog speccatalog.Catalog, input string, options experimentrun.Options) (experimentrun.Result, error)
type RunVerifier func(root string, input string) (runverify.Result, error)
type ResultParser func(io.Reader) (pgbenchresult.Result, error)

type Options struct {
	Runtime                    string
	RunID                      string
	Subject                    string
	PackID                     string
	PackVersion                string
	PackDigest                 string
	EngineVersion              string
	EngineCommit               string
	BinaryPath                 string
	ABProtocolDigest           string
	ABEffectiveSettingNames    []string
	SubjectDimension           string
	NativeBindir               string
	NativeToolchainDigest      string
	NativeToolchainManifestRef string
	PostgresHost               string
	PostgresPort               int
	Stdout                     io.Writer
	Stderr                     io.Writer
	Now                        func() time.Time
	Getenv                     func(string) string
	RunExperiment              ExperimentRunner
	VerifyRun                  RunVerifier
	ParseResult                ResultParser
	beforeInitialPublish       func(string) error
}

type ArtifactRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type ScenarioPackIdentity struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	Digest          string `json:"digest"`
	InventoryRef    string `json:"inventory_ref"`
	InventoryDigest string `json:"inventory_digest"`
}

// ScenarioPackInventory is the portable full-file inventory retained at the
// start of a pack-bound benchmark series. It intentionally omits the producer's
// absolute root and engine-observation fields from scenariopack.Inspection.
type ScenarioPackInventory struct {
	SchemaVersion string                `json:"schema_version"`
	ArtifactType  string                `json:"artifact_type"`
	ID            string                `json:"id"`
	Version       string                `json:"version"`
	Digest        string                `json:"digest"`
	Manifest      scenariopack.Manifest `json:"manifest"`
	Files         []scenariopack.File   `json:"files"`
}

type Environment struct {
	SchemaVersion              string `json:"schema_version"`
	ArtifactType               string `json:"artifact_type"`
	Runtime                    string `json:"runtime"`
	RuntimeOS                  string `json:"runtime_os"`
	RuntimeArch                string `json:"runtime_arch"`
	Driver                     string `json:"driver"`
	Target                     string `json:"target"`
	TargetEndpointContract     string `json:"target_endpoint_contract"`
	TargetEndpointHost         string `json:"target_endpoint_host"`
	TargetEndpointPort         int    `json:"target_endpoint_port"`
	DockerDriverImageID        string `json:"docker_driver_image_id"`
	DockerTargetImageID        string `json:"docker_target_image_id"`
	TargetTopology             string `json:"target_topology"`
	DriverVersion              string `json:"driver_version"`
	ParserVersion              string `json:"parser_version"`
	PostgresServerVersionNum   string `json:"postgres_server_version_num"`
	PostgresServerMajor        string `json:"postgres_server_major"`
	PGConfig                   string `json:"pg_config"`
	PGConfigDigest             string `json:"pg_config_digest"`
	SubjectDimension           string `json:"subject_dimension"`
	NativeToolchainDigest      string `json:"native_toolchain_digest,omitempty"`
	NativeToolchainManifestRef string `json:"native_toolchain_manifest_ref,omitempty"`
	NativeToolchainProvenance  string `json:"native_toolchain_provenance"`
	EngineVersion              string `json:"engine_version"`
	EngineCommit               string `json:"engine_commit"`
	EngineBinaryDigest         string `json:"engine_binary_digest"`
	PackID                     string `json:"pack_id"`
	PackVersion                string `json:"pack_version"`
	PackDigest                 string `json:"pack_digest"`
	Qualification              string `json:"qualification"`
	Digest                     string `json:"digest"`
}

type Trial struct {
	SchemaVersion      string                      `json:"schema_version"`
	ArtifactType       string                      `json:"artifact_type"`
	Trial              int                         `json:"trial"`
	RunID              string                      `json:"run_id"`
	RunRef             string                      `json:"run_ref"`
	StartedAt          string                      `json:"started_at"`
	FinishedAt         string                      `json:"finished_at"`
	DurationMS         int64                       `json:"duration_ms"`
	Status             string                      `json:"status"`
	Reasons            []string                    `json:"reasons"`
	ExperimentVerified bool                        `json:"experiment_verified"`
	EnvironmentDigest  string                      `json:"environment_digest,omitempty"`
	Summary            *ArtifactRef                `json:"summary,omitempty"`
	RawLogs            []ArtifactRef               `json:"raw_logs"`
	Pgbench            *pgbenchresult.Result       `json:"pgbench,omitempty"`
	TransactionLog     *pgbenchlog.Result          `json:"transaction_log,omitempty"`
	PostgresMetrics    *benchmarkmetrics.Summary   `json:"postgres_metrics,omitempty"`
	EffectiveSettings  *benchmarksettings.Evidence `json:"effective_settings,omitempty"`
	Controls           *ControlEvidence            `json:"controls,omitempty"`
	PhaseJournal       *ArtifactRef                `json:"phase_journal,omitempty"`
	PhaseTimeline      *benchmarkphase.Timeline    `json:"phase_timeline"`
	PrimaryMetric      string                      `json:"primary_metric"`
	PrimaryValue       *float64                    `json:"primary_value,omitempty"`
}

type Series struct {
	SchemaVersion          string                    `json:"schema_version"`
	ArtifactType           string                    `json:"artifact_type"`
	Benchmark              string                    `json:"benchmark"`
	Name                   string                    `json:"name"`
	Class                  string                    `json:"class"`
	Driver                 string                    `json:"driver"`
	Target                 string                    `json:"target"`
	TargetEndpointContract string                    `json:"target_endpoint_contract"`
	TargetTopology         string                    `json:"target_topology"`
	Subject                string                    `json:"subject"`
	RunID                  string                    `json:"run_id"`
	RunDir                 string                    `json:"run_dir"`
	SpecRef                string                    `json:"spec_ref"`
	SpecDigest             string                    `json:"spec_digest"`
	ProtocolDigest         string                    `json:"protocol_digest"`
	ComparisonKeyDigest    string                    `json:"comparison_key_digest"`
	EngineBinaryRef        string                    `json:"engine_binary_ref"`
	EngineBinaryDigest     string                    `json:"engine_binary_digest"`
	AllowedDifferences     []string                  `json:"allowed_subject_differences"`
	Runtime                string                    `json:"runtime"`
	EvidenceClass          string                    `json:"evidence_class"`
	PrimaryMetric          string                    `json:"primary_metric"`
	Direction              string                    `json:"direction"`
	RegressionThresholdPct *float64                  `json:"regression_threshold_pct,omitempty"`
	MaxCVPct               float64                   `json:"max_cv_pct"`
	ResetPolicy            string                    `json:"reset_policy"`
	StartedAt              string                    `json:"started_at"`
	FinishedAt             string                    `json:"finished_at"`
	Status                 string                    `json:"status"`
	Reasons                []string                  `json:"reasons"`
	TrialsPlanned          int                       `json:"trials_planned"`
	TrialsValid            int                       `json:"trials_valid"`
	TrialsFailed           int                       `json:"trials_failed"`
	TrialsInvalid          int                       `json:"trials_invalid"`
	ScenarioPack           *ScenarioPackIdentity     `json:"scenario_pack,omitempty"`
	Environment            *Environment              `json:"environment,omitempty"`
	Stats                  *pgbenchresult.TrialStats `json:"stats,omitempty"`
	Trials                 []Trial                   `json:"trials"`
	ArtifactDir            string                    `json:"-"`
}

func (s Series) Passed() bool { return s.Status == "passed" }

// Execution owns one immutable benchmark series while allowing its trials to
// be scheduled one at a time. The ordinary runner uses it sequentially; the
// counterbalanced A/B runner interleaves two Executions without duplicating
// trial, environment, parsing, or evidence logic.
type Execution struct {
	root                string
	catalog             speccatalog.Catalog
	plan                benchmarkplan.Plan
	options             Options
	series              Series
	expectedEnvironment *Environment
	scenarioPack        *ScenarioPackInventory
	engine              engineBinaryIdentity
	halted              bool
	finished            bool
}

func Run(root string, catalog speccatalog.Catalog, input string, options Options) (Series, error) {
	plan, err := benchmarkplan.Build(catalog, input)
	if err != nil {
		return Series{}, err
	}
	execution, err := Start(root, catalog, plan, options)
	if err != nil {
		if execution != nil {
			return execution.Snapshot(), err
		}
		return Series{}, err
	}
	for execution.ExecutedTrials() < plan.Trials && !execution.Halted() {
		if _, err := execution.ExecuteTrial(); err != nil {
			return execution.Snapshot(), err
		}
	}
	return execution.Finish()
}

// Start creates the immutable series directory and snapshots every protocol
// input before the first trial is allowed to execute.
func Start(root string, catalog speccatalog.Catalog, plan benchmarkplan.Plan, options Options) (*Execution, error) {
	options = withDefaults(options)
	if err := validateABEffectiveSettingsOptions(options); err != nil {
		return nil, err
	}
	options.ABEffectiveSettingNames = append([]string(nil), options.ABEffectiveSettingNames...)
	runtimeName := firstNonEmpty(options.Runtime, options.Getenv("PGWORKBENCH_RUNTIME"), "docker")
	if runtimeName != "docker" && runtimeName != "native" {
		return nil, fmt.Errorf("unsupported runtime %q: expected docker or native", runtimeName)
	}
	// Both current runtime adapters execute pgbench on the PostgreSQL host: in
	// the postgres container for Docker and on the native host for native. The
	// schema reserves other placements for future adapters, but accepting them
	// here would mislabel the executed protocol.
	if plan.ClientPlacement != "same-host" {
		return nil, fmt.Errorf("benchmark client placement %q is unsupported by the %s pgbench adapter: expected same-host", plan.ClientPlacement, runtimeName)
	}
	if runtimeName == "native" && plan.Target != benchmarkplan.TargetDirectPostgres {
		return nil, fmt.Errorf("benchmark target %q is unsupported by the native adapter; native benchmarks may target only the owned direct PostgreSQL endpoint", plan.Target)
	}
	if err := validateProtocolSources(root, plan); err != nil {
		return nil, err
	}
	// The current native adapter has no owned cgroup provider. Reject an
	// enforced budget before reserving evidence instead of silently treating an
	// operator limit or the runner process itself as the PostgreSQL+driver
	// scope. Unbounded protocol-v2 runs remain available on both adapters.
	if plan.ContractVersion == "2" && plan.ResourceBudgetMode == "runner-enforced" && runtimeName != "docker" {
		return nil, fmt.Errorf("runner-enforced benchmark resource budgets require the Docker single-container adapter")
	}
	if plan.ContractVersion == "2" && plan.ResourceBudgetMode == "runner-enforced" && plan.Target == benchmarkplan.TargetPgBouncer {
		return nil, fmt.Errorf("runner-enforced benchmark resource budgets do not cover the separate PgBouncer container")
	}
	postgresHost, postgresPort, err := resolveOwnedPostgresEndpoint(options)
	if err != nil {
		return nil, err
	}
	options.PostgresHost, options.PostgresPort = postgresHost, postgresPort
	packInventory, err := inspectConfiguredScenarioPack(root, options)
	if err != nil {
		return nil, err
	}
	engine, err := inspectEngineBinary(options.BinaryPath)
	if err != nil {
		return nil, fmt.Errorf("inspect benchmark engine binary: %w", err)
	}
	options.BinaryPath = engine.Path

	started := options.Now().UTC()
	runID := firstNonEmpty(options.RunID, options.Getenv("BENCHMARK_RUN_ID"))
	if runID == "" {
		runID = fmt.Sprintf("%s-benchmark-%s", sanitizeID(plan.Spec), started.Format("20060102_150405"))
	}
	if !ValidRunID(runID) {
		return nil, fmt.Errorf("invalid benchmark run id %q", runID)
	}
	toolchainSnapshotRequired := false
	if runtimeName == "native" {
		options.NativeBindir = firstNonEmpty(strings.TrimSpace(options.NativeBindir), strings.TrimSpace(options.Getenv("PGWORKBENCH_NATIVE_BINDIR")))
		installation, inspectErr := nativetoolchain.Inspect(options.NativeBindir)
		if inspectErr != nil {
			return nil, inspectErr
		}
		hasBoundDigest := options.NativeToolchainDigest != ""
		hasBoundManifest := options.NativeToolchainManifestRef != ""
		if hasBoundDigest != hasBoundManifest {
			return nil, fmt.Errorf("native benchmark runtime requires a toolchain digest and portable manifest reference")
		}
		if hasBoundDigest {
			if err := validateNativeToolchainOptions(runtimeName, options); err != nil {
				return nil, err
			}
			manifestPath, pathErr := nativeToolchainManifestPath(root, options.NativeToolchainManifestRef)
			if pathErr != nil {
				return nil, pathErr
			}
			if _, verifyErr := nativetoolchain.VerifySnapshot(filepath.Dir(manifestPath), options.NativeToolchainDigest); verifyErr != nil {
				return nil, fmt.Errorf("verify bound native toolchain snapshot: %w", verifyErr)
			}
		} else {
			options.NativeToolchainDigest = installation.Manifest.Digest
		}
		// Every native series retains its own snapshot. A caller-supplied
		// manifest (for example an A/B protocol snapshot) binds the input bytes,
		// but is not used as the generic series artifact's closure.
		options.NativeToolchainManifestRef = filepath.ToSlash(filepath.Join("runs", "benchmarks", runID, NativeToolchainSeriesRef))
		toolchainSnapshotRequired = true
	}
	if err := validateNativeToolchainOptions(runtimeName, options); err != nil {
		return nil, err
	}
	runParent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "benchmarks"), 0o755)
	if err != nil {
		return nil, fmt.Errorf("prepare benchmark artifact parent: %w", err)
	}
	runDir := filepath.Join(runParent, runID)
	if info, statErr := os.Lstat(runDir); statErr == nil {
		return nil, fmt.Errorf("benchmark run directory already exists: %s (%s)", runDir, info.Mode())
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	stagingDir, err := os.MkdirTemp(runParent, "."+runID+".staging-")
	if err != nil {
		return nil, fmt.Errorf("create benchmark staging directory: %w", err)
	}
	stagingOwned := true
	defer func() {
		if stagingOwned {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	if toolchainSnapshotRequired {
		installation, inspectErr := nativetoolchain.Inspect(options.NativeBindir)
		if inspectErr != nil {
			return nil, fmt.Errorf("reinspect native toolchain before snapshot: %w", inspectErr)
		}
		if installation.Manifest.Digest != options.NativeToolchainDigest {
			return nil, fmt.Errorf("native toolchain byte identity changed before snapshot")
		}
		if snapshotErr := nativetoolchain.Snapshot(installation, filepath.Join(stagingDir, "protocol", "native-toolchain")); snapshotErr != nil {
			return nil, fmt.Errorf("snapshot native toolchain: %w", snapshotErr)
		}
	}

	if !evidence.IsDigest(plan.SpecDigest) {
		return nil, fmt.Errorf("benchmark plan spec digest is invalid")
	}
	specRef := filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env"))
	series := Series{
		SchemaVersion:          SeriesSchemaVersion,
		ArtifactType:           SeriesArtifactType,
		Benchmark:              plan.Spec,
		Name:                   plan.Name,
		Class:                  plan.Class,
		Driver:                 plan.Driver,
		Target:                 plan.Target,
		TargetEndpointContract: plan.TargetEndpointContract,
		TargetTopology:         plan.TargetTopology,
		Subject:                firstNonEmpty(options.Subject, "default"),
		RunID:                  runID,
		RunDir:                 ".",
		ArtifactDir:            runDir,
		SpecRef:                specRef,
		SpecDigest:             plan.SpecDigest,
		ProtocolDigest:         plan.ProtocolDigest,
		ComparisonKeyDigest:    plan.ComparisonKeyDigest,
		EngineBinaryRef:        EngineBinarySeriesRef,
		EngineBinaryDigest:     engine.Digest,
		AllowedDifferences:     append([]string(nil), plan.AllowedSubjectDifferences...),
		Runtime:                runtimeName,
		EvidenceClass:          evidenceClass(plan.Class),
		PrimaryMetric:          plan.PrimaryMetric,
		Direction:              plan.Direction,
		RegressionThresholdPct: plan.RegressionThresholdPct,
		MaxCVPct:               plan.MaxCVPct,
		ResetPolicy:            plan.ResetPolicy,
		StartedAt:              started.Format(time.RFC3339Nano),
		Status:                 "passed",
		Reasons:                []string{},
		TrialsPlanned:          plan.Trials,
		Trials:                 make([]Trial, 0, plan.Trials),
	}
	if packInventory != nil {
		inventoryDigest, digestErr := scenarioPackInventoryFileDigest(*packInventory)
		if digestErr != nil {
			return nil, fmt.Errorf("digest scenario-pack inventory: %w", digestErr)
		}
		series.ScenarioPack = &ScenarioPackIdentity{
			ID:              packInventory.ID,
			Version:         packInventory.Version,
			Digest:          packInventory.Digest,
			InventoryRef:    ScenarioPackInventoryRef,
			InventoryDigest: inventoryDigest,
		}
	}
	if err := snapshotEngineBinary(engine, filepath.Join(stagingDir, filepath.FromSlash(EngineBinarySeriesRef))); err != nil {
		return nil, fmt.Errorf("snapshot benchmark engine binary: %w", err)
	}
	capsuleRoot, err := snapshotProtocolInputs(root, stagingDir, plan)
	if err != nil {
		return nil, err
	}
	capsuleCatalog := speccatalog.New(capsuleRoot)
	capsulePlan, err := benchmarkplan.Build(capsuleCatalog, plan.Spec)
	if err != nil {
		return nil, fmt.Errorf("validate immutable benchmark input capsule: %w", err)
	}
	if capsulePlan.ProtocolDigest != plan.ProtocolDigest || capsulePlan.ComparisonKeyDigest != plan.ComparisonKeyDigest {
		return nil, fmt.Errorf("immutable benchmark input capsule changed the typed protocol identity")
	}
	if packInventory != nil {
		if err := writeJSON(filepath.Join(stagingDir, filepath.FromSlash(ScenarioPackInventoryRef)), packInventory); err != nil {
			return nil, fmt.Errorf("retain scenario-pack inventory: %w", err)
		}
	}
	if err := writeJSON(filepath.Join(stagingDir, "plan.json"), portablePlan(plan)); err != nil {
		return nil, err
	}
	if options.beforeInitialPublish != nil {
		if err := options.beforeInitialPublish(stagingDir); err != nil {
			return nil, fmt.Errorf("benchmark initial publication hook: %w", err)
		}
	}
	if err := os.Rename(stagingDir, runDir); err != nil {
		return nil, fmt.Errorf("publish initialized benchmark series: %w", err)
	}
	stagingOwned = false
	finalCapsuleRoot := filepath.Join(runDir, "protocol", "capsule")
	finalCapsulePlan, err := benchmarkplan.Build(speccatalog.New(finalCapsuleRoot), plan.Spec)
	if err != nil {
		if rollbackErr := os.Rename(runDir, stagingDir); rollbackErr == nil {
			stagingOwned = true
		}
		return nil, fmt.Errorf("reload published immutable benchmark capsule: %w", err)
	}
	execution := &Execution{root: root, catalog: speccatalog.New(finalCapsuleRoot), plan: finalCapsulePlan, options: options, series: series, scenarioPack: packInventory, engine: engine}
	return execution, nil
}

// ExecuteTrial runs the next declared trial and persists it immediately. A
// protocol-invalid attempt is evidence, not an orchestration error, and can be
// followed by later attempts. A hard failed attempt halts this Execution.
func (execution *Execution) ExecuteTrial() (Trial, error) {
	if execution == nil {
		return Trial{}, fmt.Errorf("benchmark execution is nil")
	}
	if execution.finished {
		return Trial{}, fmt.Errorf("benchmark execution is already finalized")
	}
	if execution.halted {
		return Trial{}, fmt.Errorf("benchmark execution is halted after a failed trial")
	}
	if len(execution.series.Trials) >= execution.plan.Trials {
		return Trial{}, fmt.Errorf("all %d benchmark trials have already executed", execution.plan.Trials)
	}

	trialNumber := len(execution.series.Trials) + 1
	if err := revalidateEngineBinary(execution.engine); err != nil {
		reason := fmt.Sprintf("benchmark engine changed before trial %d", trialNumber)
		invalidateErr := execution.invalidateForProtocolIdentityDrift(reason)
		fmt.Fprintf(execution.options.Stderr, "benchmark trial %d: %v\n", trialNumber, errors.Join(err, invalidateErr))
		return Trial{Trial: trialNumber, Status: "invalid", Reasons: []string{reason}}, nil
	}
	if err := execution.revalidateScenarioPack(); err != nil {
		reason := fmt.Sprintf("scenario pack changed before trial %d", trialNumber)
		invalidateErr := execution.invalidateForProtocolIdentityDrift(reason)
		fmt.Fprintf(execution.options.Stderr, "benchmark trial %d: %v\n", trialNumber, errors.Join(err, invalidateErr))
		return Trial{Trial: trialNumber, Status: "invalid", Reasons: []string{reason}}, nil
	}
	trial, trialErr := runTrial(execution.root, execution.series.ArtifactDir, execution.catalog, execution.plan, execution.series, trialNumber, execution.options)
	engineErr := revalidateEngineBinary(execution.engine)
	execution.series.Trials = append(execution.series.Trials, trial)
	actual := &execution.series.Trials[len(execution.series.Trials)-1]
	packErr := execution.revalidateScenarioPack()
	if engineErr != nil {
		reason := fmt.Sprintf("benchmark engine changed during trial %d", trialNumber)
		trialErr = errors.Join(trialErr, engineErr, execution.invalidateForProtocolIdentityDrift(reason))
	} else if packErr != nil {
		reason := fmt.Sprintf("scenario pack changed during trial %d", trialNumber)
		trialErr = errors.Join(trialErr, packErr, execution.invalidateForProtocolIdentityDrift(reason))
	} else {
		if trial.Status == "passed" && trial.PrimaryValue != nil {
			execution.series.TrialsValid++
		} else if trial.Status == "failed" {
			execution.series.TrialsFailed++
		} else {
			execution.series.TrialsInvalid++
		}

		if trial.Status == "passed" && trial.EnvironmentDigest != "" {
			environment, envErr := environmentFromRun(execution.root, trial.RunID, execution.plan, *trial.Pgbench, execution.options, execution.engine.Digest)
			if envErr != nil {
				trialErr = errors.Join(trialErr, envErr)
				actual.Status = "invalid"
				actual.Reasons = append(actual.Reasons, "environment evidence invalid")
				execution.series.TrialsInvalid++
				execution.series.TrialsValid--
			} else if execution.expectedEnvironment == nil {
				execution.expectedEnvironment = &environment
			} else if execution.expectedEnvironment.Digest != environment.Digest {
				trialErr = errors.Join(trialErr, fmt.Errorf("environment drift: trial %d digest %s differs from %s", trialNumber, environment.Digest, execution.expectedEnvironment.Digest))
				actual.Status = "invalid"
				actual.Reasons = append(actual.Reasons, "environment drift")
				execution.series.TrialsInvalid++
				execution.series.TrialsValid--
			}
		}
	}
	if trialErr != nil {
		fmt.Fprintf(execution.options.Stderr, "benchmark trial %d: %v\n", trialNumber, trialErr)
	}
	if err := writeJSON(filepath.Join(execution.series.ArtifactDir, "trials", fmt.Sprintf("%03d.json", trialNumber)), *actual); err != nil {
		return *actual, err
	}
	if actual.Status == "failed" {
		execution.series.Reasons = append(execution.series.Reasons, fmt.Sprintf("trial %d execution failed", trialNumber))
		execution.halted = true
	}
	return *actual, nil
}

// Finish independently derives aggregate policy, closes the immutable series,
// and returns a non-nil error for every non-passing terminal status.
func (execution *Execution) Finish() (Series, error) {
	if execution == nil {
		return Series{}, fmt.Errorf("benchmark execution is nil")
	}
	if execution.finished {
		return execution.Snapshot(), fmt.Errorf("benchmark execution is already finalized")
	}
	var identityIntegrityErr error
	if err := revalidateEngineBinary(execution.engine); err != nil {
		reason := "benchmark engine changed before series finalization"
		identityIntegrityErr = errors.Join(err, execution.invalidateForProtocolIdentityDrift(reason))
	}
	if err := validateNativeIdentity(execution.root, execution.series.Runtime, execution.options); err != nil {
		reason := "native toolchain changed before series finalization"
		identityIntegrityErr = errors.Join(identityIntegrityErr, err, execution.invalidateForProtocolIdentityDrift(reason))
	}
	var packIntegrityErr error
	if err := execution.revalidateScenarioPack(); err != nil {
		reason := "scenario pack changed before series finalization"
		packIntegrityErr = errors.Join(err, execution.invalidateForProtocolIdentityDrift(reason))
	}
	execution.series.Environment = execution.expectedEnvironment
	status, stats, policyReasons, policyErr := EvaluateSeries(execution.plan, execution.series)
	if policyErr != nil {
		execution.series.Status = "invalid"
		execution.series.Reasons = append(execution.series.Reasons, "aggregate statistics failed: "+policyErr.Error())
	} else {
		execution.series.Status = status
		execution.series.Stats = stats
		execution.series.Reasons = append(execution.series.Reasons, policyReasons...)
	}
	execution.series.FinishedAt = execution.options.Now().UTC().Format(time.RFC3339Nano)
	execution.series.Reasons = uniqueSorted(execution.series.Reasons)
	if err := finalize(execution.series.ArtifactDir, execution.series); err != nil {
		return execution.Snapshot(), err
	}
	execution.finished = true
	if !execution.series.Passed() {
		return execution.Snapshot(), errors.Join(fmt.Errorf("benchmark series ended with status %s", execution.series.Status), identityIntegrityErr, packIntegrityErr)
	}
	return execution.Snapshot(), nil
}

func (execution *Execution) Snapshot() Series {
	if execution == nil {
		return Series{}
	}
	copy := execution.series
	copy.Reasons = append([]string(nil), execution.series.Reasons...)
	copy.Trials = append([]Trial(nil), execution.series.Trials...)
	return copy
}

func (execution *Execution) Plan() benchmarkplan.Plan { return execution.plan }
func (execution *Execution) ExecutedTrials() int      { return len(execution.series.Trials) }
func (execution *Execution) Halted() bool             { return execution.halted }

// EvaluateSeries derives the terminal series status and aggregate statistics
// only from the immutable protocol and recorded trials. Artifact verification
// calls the same function and therefore does not trust a producer-selected
// status or aggregate.
func EvaluateSeries(plan benchmarkplan.Plan, series Series) (string, *pgbenchresult.TrialStats, []string, error) {
	values := make([]float64, 0, len(series.Trials))
	valid, failed, invalid := 0, 0, 0
	for _, trial := range series.Trials {
		switch trial.Status {
		case "passed":
			valid++
			if trial.PrimaryValue == nil {
				return "invalid", nil, nil, fmt.Errorf("passed trial %d has no primary value", trial.Trial)
			}
			values = append(values, *trial.PrimaryValue)
		case "failed":
			failed++
		case "invalid":
			invalid++
		default:
			return "invalid", nil, nil, fmt.Errorf("trial %d has unsupported status %q", trial.Trial, trial.Status)
		}
	}

	var stats *pgbenchresult.TrialStats
	if len(values) >= 2 {
		summary, err := pgbenchresult.Summarize(values)
		if err != nil {
			return "invalid", nil, nil, err
		}
		stats = &summary
	}
	status := "passed"
	var reasons []string
	switch {
	case failed > 0:
		status = "failed"
	case len(series.Trials) != plan.Trials:
		status = "invalid"
		reasons = append(reasons, fmt.Sprintf("recorded trials %d differ from planned %d; partial series cannot be performance evidence", len(series.Trials), plan.Trials))
	case valid < plan.MinValidTrials:
		status = "invalid"
		reasons = append(reasons, fmt.Sprintf("valid trials %d below required %d", valid, plan.MinValidTrials))
	case plan.Class == "measurement" && valid < 5:
		status = "invalid"
		reasons = append(reasons, "measurement evidence requires at least 5 valid independent trials")
	case stats != nil && stats.CVPct != nil && *stats.CVPct > plan.MaxCVPct:
		status = "inconclusive"
		reasons = append(reasons, fmt.Sprintf("coefficient of variation %.4f%% exceeds %.4f%%", *stats.CVPct, plan.MaxCVPct))
	case invalid > 0:
		reasons = append(reasons, fmt.Sprintf("%d protocol-invalid attempts retained and excluded from statistics", invalid))
	}
	return status, stats, reasons, nil
}

func portablePlan(plan benchmarkplan.Plan) benchmarkplan.Plan {
	plan.SpecPath = filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env"))
	plan.ExperimentPath = filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(plan.ExperimentSpec)+".env"))
	plan.WorkloadPath = filepath.ToSlash(filepath.Join("workloads", filepath.FromSlash(plan.WorkloadSpec)+".env"))
	plan.PGConfigPath = filepath.ToSlash(filepath.Join("configs", filepath.FromSlash(plan.PGConfig), "postgresql.conf"))
	plan.TargetTopologyPath = filepath.ToSlash(filepath.Join("topologies", filepath.FromSlash(plan.TargetTopology)+".env"))
	return plan
}

func inspectConfiguredScenarioPack(root string, options Options) (*ScenarioPackInventory, error) {
	configured := options.PackID != "" || options.PackVersion != "" || options.PackDigest != ""
	if !configured {
		return nil, nil
	}
	if options.PackID == "" || options.PackVersion == "" || !evidence.IsDigest(options.PackDigest) {
		return nil, fmt.Errorf("configured benchmark scenario-pack identity is incomplete")
	}
	inspection, err := scenariopack.Validate(root)
	if err != nil {
		return nil, fmt.Errorf("validate configured benchmark scenario pack: %w", err)
	}
	if inspection.ID != options.PackID || inspection.Version != options.PackVersion || inspection.Digest != options.PackDigest {
		return nil, fmt.Errorf("configured benchmark scenario-pack identity does not match the validated pack: got %s/%s/%s want %s/%s/%s",
			inspection.ID, inspection.Version, inspection.Digest, options.PackID, options.PackVersion, options.PackDigest)
	}
	inventory := scenarioPackInventory(inspection)
	if err := scenariopack.VerifyInventory(inventory.Manifest, inventory.Files, inventory.Digest); err != nil {
		return nil, fmt.Errorf("verify configured benchmark scenario-pack inventory: %w", err)
	}
	return &inventory, nil
}

func scenarioPackInventory(inspection scenariopack.Inspection) ScenarioPackInventory {
	manifest := inspection.Manifest
	manifest.Assets = append([]string(nil), inspection.Manifest.Assets...)
	return ScenarioPackInventory{
		SchemaVersion: ScenarioPackSchemaVersion,
		ArtifactType:  ScenarioPackArtifactType,
		ID:            inspection.ID,
		Version:       inspection.Version,
		Digest:        inspection.Digest,
		Manifest:      manifest,
		Files:         append([]scenariopack.File(nil), inspection.Files...),
	}
}

func scenarioPackInventoryFileDigest(inventory ScenarioPackInventory) (string, error) {
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(append(content, '\n')), nil
}

func (execution *Execution) revalidateScenarioPack() error {
	if execution == nil || execution.scenarioPack == nil {
		return nil
	}
	inspection, err := scenariopack.Validate(execution.root)
	if err != nil {
		return err
	}
	actual := scenarioPackInventory(inspection)
	if !reflect.DeepEqual(actual, *execution.scenarioPack) {
		return fmt.Errorf("scenario-pack inventory changed: got %s/%s/%s want %s/%s/%s",
			actual.ID, actual.Version, actual.Digest,
			execution.scenarioPack.ID, execution.scenarioPack.Version, execution.scenarioPack.Digest)
	}
	return nil
}

func (execution *Execution) invalidateForProtocolIdentityDrift(reason string) error {
	if execution == nil {
		return nil
	}
	execution.halted = true
	execution.expectedEnvironment = nil
	execution.series.Environment = nil
	execution.series.TrialsValid = 0
	execution.series.TrialsFailed = 0
	execution.series.TrialsInvalid = len(execution.series.Trials)
	execution.series.Reasons = append(execution.series.Reasons, reason)
	var writeErr error
	for index := range execution.series.Trials {
		trial := &execution.series.Trials[index]
		trial.Status = "invalid"
		trial.Reasons = uniqueSorted(append(trial.Reasons, reason))
		path := filepath.Join(execution.series.ArtifactDir, "trials", fmt.Sprintf("%03d.json", index+1))
		writeErr = errors.Join(writeErr, writeJSON(path, *trial))
	}
	return writeErr
}

func validateProtocolSources(root string, plan benchmarkplan.Plan) error {
	inputs := []struct {
		label, path, relative, digest string
	}{
		{"benchmark spec", plan.SpecPath, filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env")), plan.SpecDigest},
		{"experiment spec", plan.ExperimentPath, filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(plan.ExperimentSpec)+".env")), plan.ExperimentDigest},
		{"workload spec", plan.WorkloadPath, filepath.ToSlash(filepath.Join("workloads", filepath.FromSlash(plan.WorkloadSpec)+".env")), plan.WorkloadDigest},
		{"PostgreSQL config", plan.PGConfigPath, filepath.ToSlash(filepath.Join("configs", filepath.FromSlash(plan.PGConfig), "postgresql.conf")), plan.PGConfigDigest},
		{"benchmark target topology", plan.TargetTopologyPath, filepath.ToSlash(filepath.Join("topologies", filepath.FromSlash(plan.TargetTopology)+".env")), plan.TargetTopologyDigest},
	}
	if plan.WorkloadScript != "" {
		if !evidence.IsPortablePath(plan.WorkloadScript) || !strings.HasPrefix(plan.WorkloadScript, "workloads/") || filepath.Ext(plan.WorkloadScript) != ".sql" {
			return fmt.Errorf("benchmark workload script must be a portable workloads/**/*.sql path")
		}
		inputs = append(inputs, struct {
			label, path, relative, digest string
		}{"workload script", filepath.Join(root, filepath.FromSlash(plan.WorkloadScript)), plan.WorkloadScript, plan.WorkloadScriptDigest})
	}
	for _, input := range inputs {
		if !evidence.IsPortablePath(input.relative) || !evidence.IsDigest(input.digest) {
			return fmt.Errorf("benchmark %s source identity is invalid", input.label)
		}
		if err := validateProtocolSource(root, input.path, input.relative, input.digest); err != nil {
			return fmt.Errorf("benchmark %s source: %w", input.label, err)
		}
	}
	return nil
}

func validateProtocolSource(root, actual, relative, expectedDigest string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return err
	}
	expectedAbs := filepath.Join(rootAbs, filepath.FromSlash(relative))
	actualAbs, err := filepath.Abs(actual)
	if err != nil {
		return err
	}
	current := rootAbs
	components := strings.Split(relative, "/")
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains a symlink: %s", current)
		}
		if index+1 < len(components) && !info.IsDir() {
			return fmt.Errorf("path component is not a directory: %s", current)
		}
		if index+1 == len(components) && !info.Mode().IsRegular() {
			return fmt.Errorf("input is not a regular file: %s", current)
		}
	}
	resolvedActual, err := filepath.EvalSymlinks(actualAbs)
	if err != nil {
		return err
	}
	resolvedExpected := filepath.Join(resolvedRoot, filepath.FromSlash(relative))
	if filepath.Clean(resolvedActual) != filepath.Clean(resolvedExpected) {
		return fmt.Errorf("path is not canonical: got %s want %s", actual, relative)
	}
	content, err := os.ReadFile(expectedAbs)
	if err != nil {
		return err
	}
	if evidence.DigestBytes(content) != expectedDigest {
		return fmt.Errorf("digest changed after plan construction")
	}
	return nil
}

func snapshotProtocolInputs(root string, seriesDir string, plan benchmarkplan.Plan) (string, error) {
	protocolDir := filepath.Join(seriesDir, "protocol")
	capsuleRoot := filepath.Join(protocolDir, "capsule")
	absoluteCapsuleRoot, err := filepath.Abs(capsuleRoot)
	if err != nil {
		return "", fmt.Errorf("resolve benchmark capsule root: %w", err)
	}
	capsuleRoot = absoluteCapsuleRoot
	if err := copyRegularFileDigest(plan.SpecPath, plan.SpecDigest,
		filepath.Join(seriesDir, "benchmark-spec.env"),
		filepath.Join(capsuleRoot, "benchmarks", filepath.FromSlash(plan.Spec)+".env")); err != nil {
		return "", fmt.Errorf("snapshot benchmark spec: %w", err)
	}
	if err := copyRegularFileDigest(plan.ExperimentPath, plan.ExperimentDigest,
		filepath.Join(protocolDir, "experiment-spec.env"),
		filepath.Join(capsuleRoot, "experiments", filepath.FromSlash(plan.ExperimentSpec)+".env")); err != nil {
		return "", fmt.Errorf("snapshot benchmark experiment spec: %w", err)
	}
	if err := copyRegularFileDigest(plan.WorkloadPath, plan.WorkloadDigest,
		filepath.Join(protocolDir, "workload-spec.env"),
		filepath.Join(capsuleRoot, "workloads", filepath.FromSlash(plan.WorkloadSpec)+".env")); err != nil {
		return "", fmt.Errorf("snapshot benchmark workload spec: %w", err)
	}
	if err := copyRegularFileDigest(plan.PGConfigPath, plan.PGConfigDigest,
		filepath.Join(protocolDir, "postgresql.conf"),
		filepath.Join(capsuleRoot, "configs", filepath.FromSlash(plan.PGConfig), "postgresql.conf")); err != nil {
		return "", fmt.Errorf("snapshot benchmark PostgreSQL config: %w", err)
	}
	if err := copyRegularFileDigest(plan.TargetTopologyPath, plan.TargetTopologyDigest,
		filepath.Join(protocolDir, "target-topology.env"),
		filepath.Join(capsuleRoot, "topologies", filepath.FromSlash(plan.TargetTopology)+".env")); err != nil {
		return "", fmt.Errorf("snapshot benchmark target topology: %w", err)
	}
	if plan.WorkloadScript != "" {
		if !evidence.IsPortablePath(plan.WorkloadScript) || !strings.HasPrefix(plan.WorkloadScript, "workloads/") || filepath.Ext(plan.WorkloadScript) != ".sql" {
			return "", fmt.Errorf("benchmark workload script must be a portable workloads/**/*.sql path")
		}
		scriptPath := plan.WorkloadScript
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(root, filepath.FromSlash(plan.WorkloadScript))
		}
		if err := copyRegularFileDigest(scriptPath, plan.WorkloadScriptDigest,
			filepath.Join(protocolDir, "workload-script.sql"),
			filepath.Join(capsuleRoot, filepath.FromSlash(plan.WorkloadScript))); err != nil {
			return "", fmt.Errorf("snapshot benchmark workload script: %w", err)
		}
	}
	return capsuleRoot, nil
}

func runTrial(root string, seriesDir string, catalog speccatalog.Catalog, plan benchmarkplan.Plan, series Series, number int, options Options) (Trial, error) {
	if err := validateNativeIdentity(root, series.Runtime, options); err != nil {
		return Trial{}, fmt.Errorf("revalidate native toolchain before trial %d: %w", number, err)
	}
	trialRunID := fmt.Sprintf("%s-t%03d", series.RunID, number)
	trial := Trial{
		SchemaVersion: TrialSchemaVersion,
		ArtifactType:  TrialArtifactType,
		Trial:         number,
		RunID:         trialRunID,
		RunRef:        filepath.ToSlash(filepath.Join("runs", trialRunID)),
		Status:        "invalid",
		Reasons:       []string{},
		RawLogs:       []ArtifactRef{},
		PrimaryMetric: plan.PrimaryMetric,
	}
	logPath := filepath.Join(seriesDir, "driver-logs", fmt.Sprintf("trial-%03d.log", number))
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return trial, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return trial, err
	}

	runDir := filepath.Join(root, "runs", trialRunID)
	capsuleRoot, err := filepath.Abs(filepath.Join(seriesDir, "protocol", "capsule"))
	if err != nil {
		_ = logFile.Close()
		return trial, fmt.Errorf("resolve benchmark capsule root: %w", err)
	}
	env := trialEnvWithToolchain(runDir, plan, series.Runtime, number, options.NativeToolchainDigest)
	env = append(env,
		"ENV_FILE=.env.example",
		"POSTGRES_HOST="+options.PostgresHost,
		"POSTGRES_PORT="+strconv.Itoa(options.PostgresPort),
		"PGWORKBENCH_NATIVE_BINDIR="+options.NativeBindir,
		"PGWORKBENCH_BENCHMARK_CAPSULE_ROOT="+capsuleRoot,
		"PGWORKBENCH_BENCHMARK_SERIES_ID="+series.RunID,
		"PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_ID="+plan.ExperimentSpec,
		"PGWORKBENCH_BENCHMARK_EXPERIMENT_SPEC_DIGEST="+plan.ExperimentDigest,
		"PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_ID="+plan.WorkloadSpec,
		"PGWORKBENCH_BENCHMARK_WORKLOAD_SPEC_DIGEST="+plan.WorkloadDigest,
		"PGWORKBENCH_BENCHMARK_PG_CONFIG_ID="+plan.PGConfig,
		"PGWORKBENCH_BENCHMARK_PG_CONFIG_DIGEST="+plan.PGConfigDigest,
		"PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_REF="+plan.WorkloadScript,
		"PGWORKBENCH_BENCHMARK_WORKLOAD_SCRIPT_DIGEST="+plan.WorkloadScriptDigest,
		"PGWORKBENCH_AB_PROTOCOL_DIGEST="+options.ABProtocolDigest,
		"PGWORKBENCH_AB_EFFECTIVE_SETTING_NAMES="+strings.Join(options.ABEffectiveSettingNames, ","),
	)
	phaseJournalPath := filepath.Join(seriesDir, "driver-logs", fmt.Sprintf("trial-%03d-phases.tsv", number))
	env = append(env, "PGWORKBENCH_BENCHMARK_PHASE_FILE="+phaseJournalPath)
	if err := preparePhaseJournal(seriesDir, phaseJournalPath); err != nil {
		_ = logFile.Close()
		return trial, err
	}
	result, runErr := options.RunExperiment(root, catalog, plan.ExperimentSpec, experimentrun.Options{
		Runtime:          series.Runtime,
		RunID:            trialRunID,
		Env:              env,
		ExactEnvironment: true,
		PackID:           options.PackID,
		PackVersion:      options.PackVersion,
		PackDigest:       options.PackDigest,
		EngineVersion:    options.EngineVersion,
		EngineCommit:     options.EngineCommit,
		BinaryPath:       options.BinaryPath,
		ExecutionTimeout: experimentrun.DefaultExecutionTimeout,
		CleanupGrace:     experimentrun.DefaultCleanupGrace,
		// A real *os.File keeps the child transcript deterministic and avoids
		// an extra pipe/reader goroutine around a potentially long benchmark.
		Stdout: logFile,
		Stderr: logFile,
		Now:    options.Now,
		Getenv: overlayEnv(env, options.Getenv),
	})
	if revalidateErr := validateNativeIdentity(root, series.Runtime, options); revalidateErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("revalidate native toolchain after trial %d: %w", number, revalidateErr))
	}
	closeErr := logFile.Close()
	if linkedLog, linkedErr := os.Open(filepath.Join(runDir, "stdout.log")); linkedErr == nil {
		if driverLog, appendErr := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644); appendErr == nil {
			_, _ = io.Copy(driverLog, linkedLog)
			_ = driverLog.Close()
		}
		_ = linkedLog.Close()
	}
	if replay, replayErr := os.Open(logPath); replayErr == nil {
		_, _ = io.Copy(options.Stdout, replay)
		_ = replay.Close()
	}
	trial.StartedAt = result.StartedAt
	trial.FinishedAt = result.FinishedAt
	trial.DurationMS = result.DurationMS
	if closeErr != nil {
		return trial, closeErr
	}
	timeline, phaseRef, timelineErr := parsePhaseEvidence(runDir, phaseJournalPath, trialRunID, number)
	if timelineErr != nil {
		trial.Reasons = append(trial.Reasons, "benchmark phase timeline invalid")
	}
	if timeline.SchemaVersion != "" {
		trial.PhaseTimeline = &timeline
		trial.PhaseJournal = phaseRef
		// The benchmark trial interval is the normalized full lifecycle, including
		// runner-owned preflight and cleanup. The linked experiment verdict is a
		// contained interval and is checked independently by artifact verification.
		trial.StartedAt = timeline.StartedAt
		trial.FinishedAt = timeline.FinishedAt
		trial.DurationMS = timeline.DurationMS
		if timeline.Status != "passed" {
			timelineErr = errors.Join(timelineErr, fmt.Errorf("benchmark phase timeline ended with status %s", timeline.Status))
			trial.Reasons = append(trial.Reasons, "benchmark phase timeline failed")
		}
	}
	if runErr != nil || !result.Passed() {
		trial.Status = "failed"
		trial.Reasons = append(trial.Reasons, "experiment execution failed")
		return trial, errors.Join(runErr, timelineErr, fmt.Errorf("experiment status %s", result.Status))
	}
	if timelineErr != nil {
		return trial, timelineErr
	}

	verification, verifyErr := options.VerifyRun(root, trialRunID)
	if verifyErr != nil || !verification.Valid() {
		trial.Reasons = append(trial.Reasons, "linked experiment verification failed")
		if verifyErr == nil {
			verifyErr = fmt.Errorf("%s", strings.Join(verification.Issues, "; "))
		}
		return trial, verifyErr
	}
	trial.ExperimentVerified = true
	manifest, manifestErr := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if manifestErr != nil {
		trial.Reasons = append(trial.Reasons, "linked benchmark protocol binding failed")
		return trial, manifestErr
	}
	if err := ValidateLinkedRunProtocolWithToolchain(plan, series.Runtime, number, options.NativeToolchainDigest, manifest); err != nil {
		trial.Reasons = append(trial.Reasons, "linked benchmark protocol binding failed")
		return trial, err
	}
	controls, controlErr := LoadControlsV2(runDir, plan, trialRunID, number, timeline, manifest["postgres_server_major"])
	trial.Controls = controls
	if controlErr != nil {
		trial.Reasons = append(trial.Reasons, "benchmark protocol control evidence invalid or unsatisfied")
		return trial, controlErr
	}
	if options.ABProtocolDigest != "" {
		effectiveSettings, settingsErr := effectiveSettingsFromRun(runDir, trialRunID, number, options, timeline, phaseRef)
		if settingsErr != nil {
			trial.Reasons = append(trial.Reasons, "effective pg_settings evidence invalid")
			return trial, settingsErr
		}
		trial.EffectiveSettings = &effectiveSettings
	}

	summaryPath := filepath.Join(runDir, "driver", "pgbench-summary.log")
	ref, err := artifactRef(runDir, summaryPath)
	if err != nil {
		trial.Reasons = append(trial.Reasons, "pgbench summary missing")
		return trial, err
	}
	trial.Summary = &ref
	summaryFile, err := os.Open(summaryPath)
	if err != nil {
		return trial, err
	}
	parsed, parseErr := options.ParseResult(summaryFile)
	closeErr = summaryFile.Close()
	if parseErr != nil || closeErr != nil {
		trial.Reasons = append(trial.Reasons, "pgbench summary parse failed")
		return trial, errors.Join(parseErr, closeErr)
	}
	trial.Pgbench = &parsed
	if err := ValidatePgbenchResult(plan, parsed); err != nil {
		trial.Reasons = append(trial.Reasons, err.Error())
		return trial, err
	}
	measureDuration := time.Duration(timeline.Events[benchmarkphase.MeasureIndex].DurationNS)
	if err := pgbenchresult.ValidateTPSIntegrity(parsed, measureDuration); err != nil {
		trial.Reasons = append(trial.Reasons, err.Error())
		return trial, err
	}
	environment, err := environmentFromRun(root, trialRunID, plan, parsed, options, series.EngineBinaryDigest)
	if err != nil {
		trial.Reasons = append(trial.Reasons, "environment evidence invalid")
		return trial, err
	}
	trial.EnvironmentDigest = environment.Digest

	rawLogs, err := collectRawLogs(runDir, filepath.Join(runDir, "driver", "pgbench-raw"))
	if err != nil {
		trial.Reasons = append(trial.Reasons, "raw transaction logs invalid")
		return trial, err
	}
	trial.RawLogs = rawLogs
	if plan.LogTransactions && len(rawLogs) == 0 {
		trial.Reasons = append(trial.Reasons, "raw transaction logs missing")
		return trial, fmt.Errorf("raw transaction logging is required by the benchmark protocol")
	}
	if len(rawLogs) > 0 {
		paths := make([]string, 0, len(rawLogs))
		for _, rawLog := range rawLogs {
			paths = append(paths, filepath.Join(runDir, filepath.FromSlash(rawLog.Path)))
		}
		transactionLog, parseErr := pgbenchlog.ParseFiles(paths, transactionLogOptions(plan))
		if parseErr != nil {
			trial.Reasons = append(trial.Reasons, "raw transaction logs parse failed")
			return trial, parseErr
		}
		trial.TransactionLog = &transactionLog
		if err := ValidateTransactionLog(parsed, transactionLog); err != nil {
			trial.Reasons = append(trial.Reasons, err.Error())
			return trial, err
		}
		measureStarted, startErr := time.Parse(time.RFC3339Nano, timeline.Events[benchmarkphase.MeasureIndex].StartedAt)
		measureFinished, finishErr := time.Parse(time.RFC3339Nano, timeline.Events[benchmarkphase.MeasureIndex].FinishedAt)
		if startErr != nil || finishErr != nil {
			err := errors.Join(startErr, finishErr)
			trial.Reasons = append(trial.Reasons, "measure phase timestamps cannot bind raw transaction logs")
			return trial, err
		}
		if err := pgbenchlog.ValidateCompletionWindow(transactionLog, measureStarted, measureFinished); err != nil {
			trial.Reasons = append(trial.Reasons, err.Error())
			return trial, err
		}
	}
	value, err := primaryValue(plan.PrimaryMetric, parsed)
	if err != nil {
		trial.Reasons = append(trial.Reasons, err.Error())
		return trial, err
	}
	trial.PrimaryValue = &value
	measure, ok := benchmarkphase.EventByName(timeline, benchmarkphase.MeasureName)
	if !ok {
		trial.Reasons = append(trial.Reasons, "measure phase missing")
		return trial, fmt.Errorf("benchmark phase timeline has no measure phase")
	}
	metricsOptions, err := benchmarkMetricsOptions(runDir, plan, trialRunID, number, environment.PostgresServerMajor, measure, trial.Controls)
	if err != nil {
		trial.Reasons = append(trial.Reasons, "postgresql sampler controls invalid")
		return trial, err
	}
	postgresMetrics, err := benchmarkmetrics.DeriveFile(metricsOptions)
	if err != nil {
		trial.Reasons = append(trial.Reasons, "postgresql sampler summary invalid")
		return trial, err
	}
	trial.PostgresMetrics = &postgresMetrics
	trial.Status = "passed"
	return trial, nil
}

func preparePhaseJournal(seriesDir string, path string) error {
	rootPath, err := filepath.Abs(seriesDir)
	if err != nil {
		return err
	}
	journalPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootPath, journalPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("benchmark phase journal escapes owned series directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create benchmark phase journal: %w", err)
	}
	return file.Close()
}

func appendPhaseJournal(path string, runID string, trial int, sequence int, name string, status string, started time.Time, finished time.Time, reason string) error {
	if strings.ContainsAny(reason, "\t\r\n") {
		return fmt.Errorf("benchmark phase reason contains control characters")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", runID, trial, sequence, name, status, started.UTC().Format(time.RFC3339Nano), finished.UTC().Format(time.RFC3339Nano), reason)
	return errors.Join(writeErr, file.Close())
}

func parsePhaseEvidence(runDir string, mirrorPath string, runID string, trial int) (benchmarkphase.Timeline, *ArtifactRef, error) {
	primaryPath := filepath.Join(runDir, "artifacts", "benchmark", "phases.tsv")
	primary, primaryErr := readPhaseJournal(primaryPath)
	if primaryErr != nil {
		// Failures before the shell reserves its linked run can only close the
		// runner-created series journal. Preserve that bound timeline as failure
		// evidence, but never promote it to a passed linked artifact.
		mirror, mirrorErr := readPhaseJournal(mirrorPath)
		if mirrorErr != nil {
			return benchmarkphase.Timeline{}, nil, errors.Join(primaryErr, mirrorErr)
		}
		timeline, parseErr := benchmarkphase.ParseTSV(bytes.NewReader(mirror), trial, runID)
		return timeline, nil, errors.Join(fmt.Errorf("linked benchmark phase journal is unavailable: %w", primaryErr), parseErr)
	}
	timeline, parseErr := benchmarkphase.ParseTSV(bytes.NewReader(primary), trial, runID)
	if parseErr != nil {
		return benchmarkphase.Timeline{}, nil, parseErr
	}
	ref, refErr := artifactRef(runDir, primaryPath)
	if refErr != nil {
		return timeline, nil, refErr
	}
	wantPath := filepath.ToSlash(filepath.Join("artifacts", "benchmark", "phases.tsv"))
	if ref.Path != wantPath {
		return timeline, &ref, fmt.Errorf("linked benchmark phase journal has non-canonical path %q", ref.Path)
	}
	mirror, mirrorErr := readPhaseJournal(mirrorPath)
	if mirrorErr != nil {
		return timeline, &ref, fmt.Errorf("read benchmark phase series mirror: %w", mirrorErr)
	}
	if !bytes.Equal(primary, mirror) {
		return timeline, &ref, fmt.Errorf("linked benchmark phase journal differs from series mirror")
	}
	return timeline, &ref, nil
}

func validateABEffectiveSettingsOptions(options Options) error {
	configured := options.ABProtocolDigest != "" || len(options.ABEffectiveSettingNames) != 0
	if !configured {
		return nil
	}
	if !evidence.IsDigest(options.ABProtocolDigest) {
		return fmt.Errorf("counterbalanced A/B effective-settings protocol digest is invalid")
	}
	if err := benchmarksettings.ValidateNames(options.ABEffectiveSettingNames); err != nil {
		return fmt.Errorf("counterbalanced A/B effective-settings names: %w", err)
	}
	return nil
}

func effectiveSettingsFromRun(runDir, runID string, trial int, options Options, timeline benchmarkphase.Timeline, phaseRef *ArtifactRef) (benchmarksettings.Evidence, error) {
	if phaseRef == nil || len(timeline.Events) < 2 {
		return benchmarksettings.Evidence{}, fmt.Errorf("effective pg_settings evidence requires a linked prepare phase journal")
	}
	sourcePath := filepath.Join(runDir, filepath.FromSlash(benchmarksettings.SourcePath))
	source, err := artifactRef(runDir, sourcePath)
	if err != nil {
		return benchmarksettings.Evidence{}, fmt.Errorf("effective pg_settings source: %w", err)
	}
	prepare := timeline.Events[benchmarkphase.PrepareIndex]
	parsed, err := benchmarksettings.ParseFile(sourcePath, benchmarksettings.Expectation{
		RunID:          runID,
		ProtocolDigest: options.ABProtocolDigest,
		Trial:          trial,
		Names:          options.ABEffectiveSettingNames,
		Source: benchmarksettings.SourceRef{
			Path: source.Path, Digest: source.Digest, Size: source.Size,
		},
		Phase: benchmarksettings.PhaseBinding{
			Name:          prepare.Name,
			JournalDigest: phaseRef.Digest,
			StartedAt:     prepare.StartedAt,
			FinishedAt:    prepare.FinishedAt,
		},
	})
	if err != nil {
		return benchmarksettings.Evidence{}, err
	}
	return parsed, benchmarksettings.Verify(parsed)
}

func readPhaseJournal(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 1024*1024 {
		return nil, fmt.Errorf("benchmark phase journal is not a bounded non-empty regular file: %s", path)
	}
	return os.ReadFile(path)
}

func transactionLogOptions(plan benchmarkplan.Plan) pgbenchlog.Options {
	retries := plan.MaxTries != nil && *plan.MaxTries != 1
	return pgbenchlog.Options{
		SampleRate:  plan.LogSampleRate,
		ScheduleLag: plan.Rate != nil,
		Retries:     retries,
	}
}

// ValidateTransactionLog binds the independently parsed per-transaction logs
// to the final pgbench summary. It is exported so artifact verification applies
// the same fail-closed semantic checks as the producer.
func ValidateTransactionLog(summary pgbenchresult.Result, transactionLog pgbenchlog.Result) error {
	var issues []string
	if transactionLog.Failed > 0 {
		issues = append(issues, fmt.Sprintf("raw transaction log reported %d failed transactions", transactionLog.Failed))
	}
	if transactionLog.Skipped > 0 {
		issues = append(issues, fmt.Sprintf("raw transaction log reported %d skipped transactions", transactionLog.Skipped))
	}
	if transactionLog.Retried > 0 {
		issues = append(issues, fmt.Sprintf("raw transaction log reported %d retried transactions", transactionLog.Retried))
	}
	if transactionLog.SampleRate == 1 && transactionLog.Completed != summary.TransactionsProcessed {
		issues = append(issues, fmt.Sprintf("raw transaction log completed count mismatch: got %d want %d", transactionLog.Completed, summary.TransactionsProcessed))
	}
	if transactionLog.SampleRate == 1 {
		if transactionLog.Failed != summary.TransactionsFailed {
			issues = append(issues, fmt.Sprintf("raw transaction log failed count mismatch: got %d want %d", transactionLog.Failed, summary.TransactionsFailed))
		}
		if summary.TransactionsSkipped != nil && transactionLog.Skipped != *summary.TransactionsSkipped {
			issues = append(issues, fmt.Sprintf("raw transaction log skipped count mismatch: got %d want %d", transactionLog.Skipped, *summary.TransactionsSkipped))
		}
		if summary.TransactionsRetried != nil && transactionLog.Retried != *summary.TransactionsRetried {
			issues = append(issues, fmt.Sprintf("raw transaction log retried count mismatch: got %d want %d", transactionLog.Retried, *summary.TransactionsRetried))
		}
		if summary.TotalRetries != nil && transactionLog.TotalRetries != *summary.TotalRetries {
			issues = append(issues, fmt.Sprintf("raw transaction log total retries mismatch: got %d want %d", transactionLog.TotalRetries, *summary.TotalRetries))
		}
		if transactionLog.LatencyUS != nil && !pgbenchLatencyMatchesSummary(transactionLog.LatencyUS.Mean/1000, summary) {
			issues = append(issues, fmt.Sprintf("raw transaction log mean latency %.6f ms does not match summary %.6f ms", transactionLog.LatencyUS.Mean/1000, summary.LatencyMeanMS))
		}
		if transactionLog.ScheduleLagUS != nil && summary.ScheduleLagAverageMS != nil && !closeRoundedMilliseconds(transactionLog.ScheduleLagUS.Mean/1000, *summary.ScheduleLagAverageMS) {
			issues = append(issues, fmt.Sprintf("raw transaction log mean schedule lag %.6f ms does not match summary %.6f ms", transactionLog.ScheduleLagUS.Mean/1000, *summary.ScheduleLagAverageMS))
		}
	}
	if len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
}

func trialParameters(plan benchmarkplan.Plan, runtimeName string, number int) map[string]string {
	rebuild := plan.ResetPolicy == "rebuild-per-trial" || number == 1
	init := "0"
	resetRuntime := "0"
	if rebuild {
		init = "1"
		resetRuntime = "1"
	}
	metricsDuration := plan.WarmupSeconds + plan.MeasureSeconds + 10
	if metricsDuration < 300 && plan.Mode == "fixed-transactions" {
		metricsDuration = 300
	}
	parameters := map[string]string{
		"EXPERIMENT_TOPOLOGY":                 "single",
		"EXPERIMENT_PG_CONFIG":                plan.PGConfig,
		"EXPERIMENT_PROFILE":                  "",
		"EXPERIMENT_PROFILE_SIZE":             "small",
		"EXPERIMENT_PROFILE_SECONDS":          "30",
		"EXPERIMENT_PROFILE_SETUP":            "0",
		"EXPERIMENT_PROFILE_RUN":              "0",
		"EXPERIMENT_PROFILE_RUN_SQL":          "",
		"EXPERIMENT_DATASET_SPEC":             "",
		"EXPERIMENT_DATASET_SIZE":             "small",
		"EXPERIMENT_WORKLOAD_SPEC":            plan.WorkloadSpec,
		"EXPERIMENT_BACKGROUND_SPECS":         "",
		"EXPERIMENT_BACKGROUND_WARMUP":        "0",
		"EXPERIMENT_BACKGROUND_WAIT":          "0",
		"EXPERIMENT_BEFORE_SQL_FILES":         "",
		"EXPERIMENT_BEFORE_SQL":               "",
		"EXPERIMENT_BEFORE_SHELL":             "",
		"EXPERIMENT_AFTER_SQL_FILES":          "",
		"EXPERIMENT_AFTER_SQL":                "",
		"EXPERIMENT_AFTER_SHELL":              "",
		"EXPERIMENT_ASSERT_SQL_FILES":         "",
		"EXPERIMENT_ASSERT_SQL":               "",
		"EXPERIMENT_ASSERT_TRUE_SQL":          "",
		"EXPERIMENT_ASSERT_SHELL":             "",
		"EXPERIMENT_METRICS":                  "1",
		"EXPERIMENT_METRICS_INTERVAL":         strconv.Itoa(plan.CollectorIntervalSeconds),
		"EXPERIMENT_METRICS_DURATION":         strconv.Itoa(metricsDuration),
		"EXPERIMENT_METRICS_SAMPLES":          "",
		"EXPERIMENT_SNAPSHOT":                 "1",
		"EXPERIMENT_RUNTIME_RESET":            resetRuntime,
		"EXPERIMENT_DOCKER_RESET":             "0",
		"EXPERIMENT_CAPTURE_FILES":            "",
		"EXPERIMENT_SCAN_PATHS":               "",
		"PROFILE_SIZE":                        "small",
		"PROFILE_SECONDS":                     "30",
		"DATASET_SIZE":                        "small",
		"PG_CONFIG":                           plan.PGConfig,
		"PGWORKBENCH_RUNTIME":                 runtimeName,
		"PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST": "",
		"PGWORKBENCH_EXECUTION_TIMEOUT":       experimentrun.DefaultExecutionTimeout.String(),
		"PGWORKBENCH_CLEANUP_GRACE":           experimentrun.DefaultCleanupGrace.String(),
		"PGBENCH_RESET":                       "0",
		"PGBENCH_INIT":                        init,
		"PGBENCH_SCALE":                       strconv.Itoa(plan.Scale),
		"PGBENCH_CLIENTS":                     strconv.Itoa(plan.Clients),
		"PGBENCH_THREADS":                     strconv.Itoa(plan.Threads),
		"PGBENCH_WARMUP_TIME":                 strconv.Itoa(plan.WarmupSeconds),
		"PGBENCH_SCRIPT":                      plan.WorkloadScript,
		"PGBENCH_MODE":                        plan.WorkloadMode,
		"PGBENCH_PROTOCOL":                    plan.QueryProtocol,
		"PGBENCH_CONNECT_PER_TRANSACTION":     bool01(plan.ConnectPerTransaction),
		"PGBENCH_PROGRESS":                    "",
		"PGBENCH_LOG_TRANSACTIONS":            bool01(plan.LogTransactions),
		"PGBENCH_LOG_SAMPLE_RATE":             strconv.FormatFloat(plan.LogSampleRate, 'g', -1, 64),
		"PGBENCH_EXTRA_ARGS":                  "",
		"PGBENCH_TARGET":                      plan.Target,
		// The experiment target guard always materializes every local endpoint,
		// including the dormant PgBouncer endpoint on a direct-PostgreSQL run.
		// Shadow it with the same canonical values before manifest creation so
		// execution identity cannot drift as a side effect of guard activation.
		"PGBOUNCER_HOST":                      "127.0.0.1",
		"PGBOUNCER_PORT":                      "56432",
		"PGBOUNCER_IMAGE":                     "",
		"PGBOUNCER_POOL_MODE":                 "",
		"PGBOUNCER_AUTH_TYPE":                 "",
		"PGBOUNCER_MAX_CLIENT_CONN":           "",
		"PGBOUNCER_DEFAULT_POOL_SIZE":         "",
		"PGBOUNCER_MIN_POOL_SIZE":             "",
		"PGBOUNCER_RESERVE_POOL_SIZE":         "",
		"PGBOUNCER_IGNORE_STARTUP_PARAMETERS": "",
		"PGBOUNCER_ADMIN_USERS":               "",
		"PGBOUNCER_STATS_USERS":               "",
	}
	parameters["EXPERIMENT_TOPOLOGY"] = plan.TargetTopology
	if plan.Target == benchmarkplan.TargetPgBouncer {
		parameters["PGBOUNCER_HOST"] = "127.0.0.1"
		parameters["PGBOUNCER_PORT"] = "56432"
		parameters["PGBOUNCER_IMAGE"] = "edoburu/pgbouncer:v1.25.1-p0"
		parameters["PGBOUNCER_POOL_MODE"] = "transaction"
		parameters["PGBOUNCER_AUTH_TYPE"] = "plain"
		parameters["PGBOUNCER_MAX_CLIENT_CONN"] = "100"
		parameters["PGBOUNCER_DEFAULT_POOL_SIZE"] = "10"
		parameters["PGBOUNCER_MIN_POOL_SIZE"] = "0"
		parameters["PGBOUNCER_RESERVE_POOL_SIZE"] = "5"
		parameters["PGBOUNCER_IGNORE_STARTUP_PARAMETERS"] = "extra_float_digits"
		parameters["PGBOUNCER_ADMIN_USERS"] = "postgres"
		parameters["PGBOUNCER_STATS_USERS"] = "postgres"
	}
	if plan.Mode == "fixed-transactions" {
		parameters["PGBENCH_TIME"] = ""
		parameters["PGBENCH_TRANSACTIONS"] = strconv.Itoa(plan.TransactionsPerClient)
	} else {
		parameters["PGBENCH_TIME"] = strconv.Itoa(plan.MeasureSeconds)
		parameters["PGBENCH_TRANSACTIONS"] = ""
	}
	if plan.Rate != nil {
		parameters["PGBENCH_RATE"] = strconv.FormatFloat(*plan.Rate, 'g', -1, 64)
	} else {
		parameters["PGBENCH_RATE"] = ""
	}
	if plan.LatencyLimitMS != nil {
		parameters["PGBENCH_LATENCY_LIMIT"] = strconv.FormatFloat(*plan.LatencyLimitMS, 'g', -1, 64)
	} else {
		parameters["PGBENCH_LATENCY_LIMIT"] = ""
	}
	if plan.RandomSeed != nil {
		parameters["PGBENCH_RANDOM_SEED"] = ""
		parameters["PGBENCH_WARMUP_RANDOM_SEED"] = strconv.FormatUint(*plan.WarmupRandomSeed, 10)
		parameters["PGBENCH_MEASURE_RANDOM_SEED"] = strconv.FormatUint(*plan.MeasureRandomSeed, 10)
	} else {
		parameters["PGBENCH_RANDOM_SEED"] = ""
		parameters["PGBENCH_WARMUP_RANDOM_SEED"] = ""
		parameters["PGBENCH_MEASURE_RANDOM_SEED"] = ""
	}
	if plan.MaxTries != nil {
		parameters["PGBENCH_MAX_TRIES"] = strconv.Itoa(*plan.MaxTries)
	} else {
		parameters["PGBENCH_MAX_TRIES"] = ""
	}
	return parameters
}

func trialEnv(runDir string, plan benchmarkplan.Plan, runtimeName string, number int) []string {
	return trialEnvWithToolchain(runDir, plan, runtimeName, number, "")
}

func trialEnvWithToolchain(runDir string, plan benchmarkplan.Plan, runtimeName string, number int, toolchainDigest string) []string {
	parameters := trialParameters(plan, runtimeName, number)
	parameters["PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST"] = toolchainDigest
	env := []string{
		"PGWORKBENCH_SOURCE_SPEC_KIND=benchmark",
		"PGWORKBENCH_SOURCE_SPEC_ID=" + plan.Spec,
		"PGWORKBENCH_SOURCE_SPEC_REF=" + filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env")),
		"PGWORKBENCH_SOURCE_SPEC_DIGEST=" + plan.SpecDigest,
		"PGWORKBENCH_BENCHMARK_RUN_ID=" + filepath.Base(runDir),
		"PGWORKBENCH_BENCHMARK_TRIAL=" + strconv.Itoa(number),
		"PGWORKBENCH_BENCHMARK_CONTRACT_VERSION=" + firstNonEmpty(plan.ContractVersion, "1"),
		"PGWORKBENCH_BENCHMARK_PROTOCOL_DIGEST=" + plan.ProtocolDigest,
		"PGWORKBENCH_BENCHMARK_CACHE_REGIME=" + plan.CacheRegime,
		"PGWORKBENCH_BENCHMARK_CACHE_TARGET_RELATIONS=" + strings.Join(plan.CacheTargetRelations, " "),
		"PGWORKBENCH_BENCHMARK_CACHE_MIN_RESIDENT_PCT=" + optionalFloatString(plan.CacheMinResidentPct),
		"PGWORKBENCH_BENCHMARK_STATISTICS_RESET_POLICY=" + plan.StatisticsResetPolicy,
		"PGWORKBENCH_BENCHMARK_STATISTICS_RESET_BOUNDARY=" + plan.StatisticsResetBoundary,
		"PGWORKBENCH_BENCHMARK_COLLECTOR_OVERHEAD_MODE=" + plan.CollectorOverheadMode,
		"PGWORKBENCH_BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES=" + optionalIntString(plan.CollectorOverheadSamples),
		"PGWORKBENCH_BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT=" + optionalFloatString(plan.CollectorMaxDutyCyclePct),
		"PGWORKBENCH_BENCHMARK_RESOURCE_BUDGET_MODE=" + plan.ResourceBudgetMode,
		"PGWORKBENCH_BENCHMARK_CPU_BUDGET_MILLICORES=" + optionalIntString(plan.CPUBudgetMillicores),
		"PGWORKBENCH_BENCHMARK_MEMORY_BUDGET_MIB=" + optionalIntString(plan.MemoryBudgetMiB),
		"PGWORKBENCH_BENCHMARK_RESOURCE_SCOPE=" + plan.ResourceBudgetScope,
		"PGWORKBENCH_BENCHMARK_RESOURCE_PROVIDER=" + plan.ResourceEnforcementProvider,
	}
	for _, key := range runstate.ExecutionParameterKeys() {
		env = append(env, key+"="+parameters[key])
	}
	// METRICS_SAMPLES is a legacy fallback used by the shell sampler. Emptying
	// it prevents a host setting from overriding the declared duration mode.
	env = append(env,
		"METRICS_SAMPLES=",
		"PGBENCH_RESULT_FILE="+filepath.Join(runDir, "driver", "pgbench-summary.log"),
		"PGBENCH_RAW_LOG_DIR="+filepath.Join(runDir, "driver", "pgbench-raw"),
	)
	return env
}

// ExpectedExecutionParametersDigest independently projects a typed benchmark
// protocol into the exact environment consumed by the experiment state writer.
// Artifact verification uses it to bind a linked run manifest to plan.json
// without trusting the manifest's self-consistent identity digest.
func ExpectedExecutionParametersDigest(plan benchmarkplan.Plan, runtimeName string, trialNumber int) string {
	return ExpectedExecutionParametersDigestWithToolchain(plan, runtimeName, trialNumber, "")
}

func ExpectedExecutionParametersDigestWithToolchain(plan benchmarkplan.Plan, runtimeName string, trialNumber int, toolchainDigest string) string {
	values := trialParameters(plan, runtimeName, trialNumber)
	values["PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST"] = toolchainDigest
	return runstate.EffectiveParametersDigest(func(key string) string { return values[key] })
}

// ValidateLinkedRunProtocol binds the externally verified linked-run envelope
// back to the immutable benchmark protocol. The generic run verifier proves
// manifest/verdict self-consistency; this check proves it is the run requested
// by plan.json.
func ValidateLinkedRunProtocol(plan benchmarkplan.Plan, runtimeName string, trialNumber int, manifest map[string]string) error {
	return ValidateLinkedRunProtocolWithToolchain(plan, runtimeName, trialNumber, "", manifest)
}

func ValidateLinkedRunProtocolWithToolchain(plan benchmarkplan.Plan, runtimeName string, trialNumber int, toolchainDigest string, manifest map[string]string) error {
	expected := []struct {
		key, value string
	}{
		{"source_spec_kind", "benchmark"},
		{"source_spec_id", plan.Spec},
		{"source_spec_ref", filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env"))},
		{"source_spec_digest", plan.SpecDigest},
		{"experiment_spec_id", plan.ExperimentSpec},
		{"experiment_spec_ref", filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(plan.ExperimentSpec)+".env"))},
		{"experiment_spec_digest", plan.ExperimentDigest},
		{"experiment_topology", plan.TargetTopology},
		{"experiment_pg_config", plan.PGConfig},
		{"profile", ""},
		{"dataset_spec", ""},
		{"profile_size", "small"},
		{"workload_spec", plan.WorkloadSpec},
		{"background_specs", ""},
		{"metrics_enabled", "1"},
		{"metrics_samples", ""},
		{"runtime", runtimeName},
		{"execution_parameters_digest", ExpectedExecutionParametersDigestWithToolchain(plan, runtimeName, trialNumber, toolchainDigest)},
	}
	var issues []string
	for _, field := range expected {
		if manifest[field.key] != field.value {
			issues = append(issues, fmt.Sprintf("linked manifest %s mismatch: got %q want %q", field.key, manifest[field.key], field.value))
		}
	}
	if len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
}

// ValidatePgbenchResult proves that normalized pgbench output describes the
// exact typed benchmark protocol rather than merely being syntactically valid.
func ValidatePgbenchResult(plan benchmarkplan.Plan, parsed pgbenchresult.Result) error {
	var issues []string
	if parsed.Clients != int64(plan.Clients) {
		issues = append(issues, fmt.Sprintf("pgbench clients mismatch: got %d want %d", parsed.Clients, plan.Clients))
	}
	if parsed.Threads != int64(plan.Threads) {
		issues = append(issues, fmt.Sprintf("pgbench threads mismatch: got %d want %d", parsed.Threads, plan.Threads))
	}
	if parsed.ScaleFactor != int64(plan.Scale) {
		issues = append(issues, fmt.Sprintf("pgbench scale mismatch: got %d want %d", parsed.ScaleFactor, plan.Scale))
	}
	if parsed.QueryMode != plan.QueryProtocol {
		issues = append(issues, fmt.Sprintf("pgbench query protocol mismatch: got %s want %s", parsed.QueryMode, plan.QueryProtocol))
	}
	expectedTries := int64(1)
	if plan.MaxTries != nil {
		expectedTries = int64(*plan.MaxTries)
	}
	if parsed.MaximumTries != expectedTries {
		issues = append(issues, fmt.Sprintf("pgbench maximum tries mismatch: got %d want %d", parsed.MaximumTries, expectedTries))
	}
	if plan.WorkloadScript != "" {
		if strings.HasPrefix(parsed.TransactionType, "<builtin:") {
			issues = append(issues, "pgbench reported a builtin transaction type for a custom-script protocol")
		}
	} else {
		expectedTransactionType := map[string]string{
			"builtin":       "<builtin: TPC-B (sort of)>",
			"tpcb-like":     "<builtin: TPC-B (sort of)>",
			"select-only":   "<builtin: select only>",
			"simple-update": "<builtin: simple update>",
		}[plan.WorkloadMode]
		if expectedTransactionType != "" && parsed.TransactionType != expectedTransactionType {
			issues = append(issues, fmt.Sprintf("pgbench transaction type mismatch: got %q want %q", parsed.TransactionType, expectedTransactionType))
		}
	}
	expectedMode := pgbenchresult.ModeTime
	if plan.Mode == "fixed-transactions" {
		expectedMode = pgbenchresult.ModeTransactions
	}
	if parsed.Mode != expectedMode {
		issues = append(issues, fmt.Sprintf("pgbench mode mismatch: got %s want %s", parsed.Mode, expectedMode))
	}
	if parsed.TransactionsFailed != 0 {
		issues = append(issues, fmt.Sprintf("pgbench reported %d failed transactions", parsed.TransactionsFailed))
	}
	if parsed.TransactionsRetried != nil && *parsed.TransactionsRetried != 0 {
		issues = append(issues, fmt.Sprintf("pgbench reported %d retried transactions", *parsed.TransactionsRetried))
	}
	if parsed.TransactionsSkipped != nil && *parsed.TransactionsSkipped != 0 {
		issues = append(issues, fmt.Sprintf("pgbench reported %d skipped transactions", *parsed.TransactionsSkipped))
	}
	if plan.LatencyLimitMS == nil {
		if parsed.LatencyLimitMS != nil || parsed.TransactionsAboveLimit != nil || parsed.LatencyLimitTotal != nil {
			issues = append(issues, "pgbench reported a latency limit absent from the protocol")
		}
	} else if parsed.LatencyLimitMS == nil || *parsed.LatencyLimitMS != *plan.LatencyLimitMS {
		issues = append(issues, "pgbench latency limit does not match the protocol")
	}
	if plan.MaxLatencyLimitExceededPct != nil {
		if parsed.TransactionsAboveLimit == nil || parsed.LatencyLimitTotal == nil || *parsed.LatencyLimitTotal <= 0 {
			issues = append(issues, "pgbench omitted latency-limit counts required by the SLO budget")
		} else {
			exceededPct := 100 * float64(*parsed.TransactionsAboveLimit) / float64(*parsed.LatencyLimitTotal)
			if exceededPct > *plan.MaxLatencyLimitExceededPct {
				issues = append(issues, fmt.Sprintf("latency-limit exceeded rate %.6f%% is above the declared %.6f%% budget", exceededPct, *plan.MaxLatencyLimitExceededPct))
			}
		}
	}
	if plan.ConnectPerTransaction {
		if parsed.AverageConnectionTimeMS == nil || parsed.TPSIncludingConnections == nil {
			issues = append(issues, "pgbench omitted reconnect-specific evidence for connect-per-transaction protocol")
		}
		if parsed.InitialConnectionTimeMS != nil || parsed.TPSExcludingConnections != nil {
			issues = append(issues, "pgbench reported non-reconnect connection evidence for connect-per-transaction protocol")
		}
	} else {
		if parsed.AverageConnectionTimeMS != nil {
			issues = append(issues, "pgbench reported reconnect evidence for a protocol without connect-per-transaction")
		}
	}
	if plan.Rate == nil {
		if parsed.ScheduleLagAverageMS != nil || parsed.ScheduleLagMaxMS != nil {
			issues = append(issues, "pgbench reported schedule lag for a protocol without a target rate")
		}
	} else if parsed.ScheduleLagAverageMS == nil || parsed.ScheduleLagMaxMS == nil {
		issues = append(issues, "pgbench omitted schedule lag for a rate-limited protocol")
	}
	if plan.Mode == "fixed-transactions" {
		expected := int64(plan.Clients * plan.TransactionsPerClient)
		if parsed.TransactionsProcessed != expected {
			issues = append(issues, fmt.Sprintf("processed transactions mismatch: got %d want %d", parsed.TransactionsProcessed, expected))
		}
	} else if parsed.DurationSeconds == nil || *parsed.DurationSeconds != float64(plan.MeasureSeconds) {
		issues = append(issues, "fixed-time duration does not match the declared measurement budget")
	}
	if len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
}

func closeRoundedMilliseconds(left float64, right float64) bool {
	return math.Abs(left-right) <= 0.001
}

// Without throttle, progress, or a latency limit, pgbench prints latency from
// the global measured window (duration * clients / completed transactions),
// while its plain log records the sum of per-client transaction intervals.
// The former includes bounded client-loop/start-stop gaps and is therefore an
// upper bound. Keep that unmeasured portion below two percent plus the printed
// 0.001 ms rounding interval. Detailed pgbench summaries use the same latency
// accumulator as the log and must match at printed precision.
func pgbenchLatencyMatchesSummary(rawMean float64, summary pgbenchresult.Result) bool {
	const printedHalfMillisecondUnit = 0.0005
	if summary.ScheduleLagAverageMS != nil || summary.LatencyLimitMS != nil || summary.LatencyStddevMS != nil {
		return math.Abs(rawMean-summary.LatencyMeanMS) <= printedHalfMillisecondUnit
	}
	if rawMean > summary.LatencyMeanMS+printedHalfMillisecondUnit {
		return false
	}
	tolerance := printedHalfMillisecondUnit + math.Abs(summary.LatencyMeanMS)*0.02
	return summary.LatencyMeanMS-rawMean <= tolerance
}

func primaryValue(metric string, parsed pgbenchresult.Result) (float64, error) {
	switch metric {
	case "pgbench.tps":
		if parsed.TPSExcludingConnections == nil {
			return 0, fmt.Errorf("pgbench TPS excluding initial connections is missing")
		}
		return *parsed.TPSExcludingConnections, nil
	case "pgbench.latency_mean_us":
		return parsed.LatencyMeanMS * 1000, nil
	default:
		return 0, fmt.Errorf("unsupported primary metric: %s", metric)
	}
}

type TargetEvidence struct {
	Target           string
	EndpointContract string
	DriverService    string
	Host             string
	Port             int
	DriverImageID    string
	TargetImageID    string
}

// ReadTargetEvidence parses the runner-emitted measured endpoint line. This is
// the cross-artifact proof that an explicit native host/port reached pgbench;
// it is intentionally derived from the linked run transcript, not copied from
// caller options into environment.json.
func ReadTargetEvidence(path string) (TargetEvidence, error) {
	file, err := os.Open(path)
	if err != nil {
		return TargetEvidence{}, err
	}
	defer file.Close()

	const prefix = "pgworkbench_benchmark_target="
	var result TargetEvidence
	found := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if found {
			return TargetEvidence{}, fmt.Errorf("benchmark target transcript contains multiple endpoint records")
		}
		values := make(map[string]string, 7)
		for _, token := range strings.Fields(line) {
			key, value, ok := strings.Cut(token, "=")
			if !ok || key == "" || value == "" {
				return TargetEvidence{}, fmt.Errorf("benchmark target transcript contains malformed token %q", token)
			}
			if _, duplicate := values[key]; duplicate {
				return TargetEvidence{}, fmt.Errorf("benchmark target transcript repeats %s", key)
			}
			values[key] = value
		}
		for _, key := range []string{"pgworkbench_benchmark_target", "endpoint_contract", "driver_service", "endpoint_host", "endpoint_port", "driver_image_id", "target_image_id"} {
			if values[key] == "" {
				return TargetEvidence{}, fmt.Errorf("benchmark target transcript omits %s", key)
			}
		}
		if len(values) != 7 {
			return TargetEvidence{}, fmt.Errorf("benchmark target transcript contains unexpected fields")
		}
		port, parseErr := strconv.Atoi(values["endpoint_port"])
		if parseErr != nil || port < 1 || port > 65535 || strconv.Itoa(port) != values["endpoint_port"] {
			return TargetEvidence{}, fmt.Errorf("benchmark target transcript contains invalid endpoint port")
		}
		result = TargetEvidence{
			Target: values["pgworkbench_benchmark_target"], EndpointContract: values["endpoint_contract"],
			DriverService: values["driver_service"], Host: values["endpoint_host"], Port: port,
			DriverImageID: values["driver_image_id"], TargetImageID: values["target_image_id"],
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		return TargetEvidence{}, err
	}
	if !found {
		return TargetEvidence{}, fmt.Errorf("benchmark target transcript has no endpoint record")
	}
	return result, nil
}

func ValidateTargetEvidence(observed TargetEvidence, runtimeName string, plan benchmarkplan.Plan, postgresHost string, postgresPort int) error {
	if observed.Target != plan.Target || observed.EndpointContract != plan.TargetEndpointContract {
		return fmt.Errorf("measured target identity differs from the benchmark protocol")
	}
	wantService, wantHost, wantPort := "postgres", "127.0.0.1", 5432
	switch runtimeName {
	case "native":
		wantService, wantHost, wantPort = "native-host", postgresHost, postgresPort
		if observed.DriverImageID != "not-applicable" || observed.TargetImageID != "not-applicable" {
			return fmt.Errorf("native benchmark target evidence contains Docker image identity")
		}
	case "docker":
		if plan.Target == benchmarkplan.TargetPgBouncer {
			wantHost = "pgbouncer"
		}
		if !evidence.IsDigest(observed.DriverImageID) || !evidence.IsDigest(observed.TargetImageID) {
			return fmt.Errorf("Docker benchmark target evidence lacks immutable image identity")
		}
	default:
		return fmt.Errorf("unsupported benchmark runtime %q", runtimeName)
	}
	if observed.DriverService != wantService || observed.Host != wantHost || observed.Port != wantPort {
		return fmt.Errorf("measured endpoint %s:%d via %s differs from bound endpoint %s:%d via %s",
			observed.Host, observed.Port, observed.DriverService, wantHost, wantPort, wantService)
	}
	return nil
}

func environmentFromRun(root string, runID string, plan benchmarkplan.Plan, parsed pgbenchresult.Result, options Options, engineBinaryDigest string) (Environment, error) {
	manifest, err := envfile.Parse(filepath.Join(root, "runs", runID, "manifest.env"))
	if err != nil {
		return Environment{}, err
	}
	if err := pgbenchresult.ValidateServerMajor(parsed, manifest["postgres_server_major"]); err != nil {
		return Environment{}, err
	}
	targetEvidence, err := ReadTargetEvidence(filepath.Join(root, "runs", runID, "stdout.log"))
	if err != nil {
		return Environment{}, fmt.Errorf("read benchmark target evidence for trial %s: %w", runID, err)
	}
	if err := ValidateTargetEvidence(targetEvidence, manifest["runtime"], plan, options.PostgresHost, options.PostgresPort); err != nil {
		return Environment{}, fmt.Errorf("validate benchmark target evidence for trial %s: %w", runID, err)
	}
	environment := Environment{
		SchemaVersion:              EnvironmentSchemaVersion,
		ArtifactType:               EnvironmentArtifactType,
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
		SubjectDimension:           firstNonEmpty(options.SubjectDimension, "pg_config"),
		NativeToolchainDigest:      options.NativeToolchainDigest,
		NativeToolchainManifestRef: options.NativeToolchainManifestRef,
		NativeToolchainProvenance:  "not-applicable",
		EngineVersion:              manifest["engine_version"],
		EngineCommit:               manifest["engine_commit"],
		EngineBinaryDigest:         engineBinaryDigest,
		PackID:                     manifest["pack_id"],
		PackVersion:                manifest["pack_version"],
		PackDigest:                 manifest["pack_digest"],
		Qualification:              "unqualified-local",
	}
	if environment.Runtime == "native" {
		environment.NativeToolchainProvenance = nativetoolchain.Unattested
	}
	if environment.Runtime == "" || environment.RuntimeOS == "" || environment.RuntimeArch == "" || environment.Driver == "" || environment.DriverVersion == "" || environment.ParserVersion == "" || environment.PostgresServerVersionNum == "" || environment.PostgresServerMajor == "" || !evidence.IsDigest(environment.EngineBinaryDigest) {
		return Environment{}, fmt.Errorf("incomplete runtime fingerprint for benchmark trial %s", runID)
	}
	digestView := environment
	digestView.Digest = ""
	content, err := json.Marshal(digestView)
	if err != nil {
		return Environment{}, err
	}
	environment.Digest = evidence.DigestBytes(content)
	return environment, nil
}

func validateNativeToolchainOptions(runtimeName string, options Options) error {
	dimension := firstNonEmpty(options.SubjectDimension, "pg_config")
	if dimension != "pg_config" && dimension != "native_toolchain" {
		return fmt.Errorf("unsupported benchmark subject dimension %q", dimension)
	}
	if runtimeName != "native" {
		if dimension == "native_toolchain" {
			return fmt.Errorf("native_toolchain subject dimension requires native runtime")
		}
		if options.NativeBindir != "" || options.NativeToolchainDigest != "" || options.NativeToolchainManifestRef != "" {
			return fmt.Errorf("Docker benchmark runtime rejects native toolchain identity")
		}
		return nil
	}
	if strings.TrimSpace(options.NativeBindir) == "" {
		return fmt.Errorf("native benchmark runtime requires a concrete PostgreSQL bindir")
	}
	if !evidence.IsDigest(options.NativeToolchainDigest) || !evidence.IsPortablePath(options.NativeToolchainManifestRef) {
		return fmt.Errorf("native benchmark runtime requires a toolchain digest and portable manifest reference")
	}
	installation, err := nativetoolchain.Inspect(options.NativeBindir)
	if err != nil {
		return err
	}
	if installation.Manifest.Digest != options.NativeToolchainDigest {
		return fmt.Errorf("native toolchain byte identity differs from the bound protocol")
	}
	return nil
}

func validateNativeIdentity(root, runtimeName string, options Options) error {
	if err := validateNativeToolchainOptions(runtimeName, options); err != nil {
		return err
	}
	if runtimeName != "native" {
		return nil
	}
	manifestPath, err := nativeToolchainManifestPath(root, options.NativeToolchainManifestRef)
	if err != nil {
		return err
	}
	if _, err := nativetoolchain.VerifySnapshot(filepath.Dir(manifestPath), options.NativeToolchainDigest); err != nil {
		return fmt.Errorf("retained native toolchain snapshot changed: %w", err)
	}
	return nil
}

func resolveOwnedPostgresEndpoint(options Options) (string, int, error) {
	host := strings.TrimSpace(options.PostgresHost)
	if host == "" {
		host = strings.TrimSpace(options.Getenv("POSTGRES_HOST"))
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if host != "127.0.0.1" && host != "localhost" {
		return "", 0, fmt.Errorf("benchmark runtime requires an owned loopback POSTGRES_HOST, got %q", host)
	}
	port := options.PostgresPort
	if port == 0 {
		value := strings.TrimSpace(options.Getenv("POSTGRES_PORT"))
		if value == "" {
			value = "55433"
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || strconv.Itoa(parsed) != value {
			return "", 0, fmt.Errorf("benchmark POSTGRES_PORT must be a canonical integer, got %q", value)
		}
		port = parsed
	}
	if port < 1024 || port > 65535 {
		return "", 0, fmt.Errorf("benchmark POSTGRES_PORT must be between 1024 and 65535, got %d", port)
	}
	return host, port, nil
}

func nativeToolchainManifestPath(root, reference string) (string, error) {
	if !evidence.IsPortablePath(reference) || filepath.Base(filepath.FromSlash(reference)) != nativetoolchain.ManifestName {
		return "", fmt.Errorf("native toolchain manifest reference is not a portable manifest path")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(absoluteRoot, filepath.FromSlash(reference)))
	relative, err := filepath.Rel(absoluteRoot, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("native toolchain manifest reference escapes the artifact root")
	}
	return path, nil
}

func finalize(runDir string, series Series) error {
	if series.Environment != nil {
		if err := writeJSON(filepath.Join(runDir, "environment.json"), series.Environment); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(runDir, "result.json"), series); err != nil {
		return err
	}
	if err := writeRunsTSV(filepath.Join(runDir, "runs.tsv"), series.Trials); err != nil {
		return err
	}
	return writeSummary(filepath.Join(runDir, "summary.md"), series)
}

func writeRunsTSV(path string, trials []Trial) error {
	var out strings.Builder
	out.WriteString("trial\trun_id\tstatus\tprimary_value\trun_ref\n")
	for _, trial := range trials {
		value := ""
		if trial.PrimaryValue != nil {
			value = strconv.FormatFloat(*trial.PrimaryValue, 'g', -1, 64)
		}
		fmt.Fprintf(&out, "%d\t%s\t%s\t%s\t%s\n", trial.Trial, trial.RunID, trial.Status, value, trial.RunRef)
	}
	return writeFileAtomic(path, []byte(out.String()), 0o644)
}

func writeSummary(path string, series Series) error {
	return writeFileAtomic(path, SummaryBytes(series), 0o644)
}

// SummaryBytes renders the deterministic human-readable projection of a
// normalized series. Artifact verification compares this projection byte for
// byte, so summary.md cannot contradict the independently verified JSON.
func SummaryBytes(series Series) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "# Benchmark Series\n\n")
	fmt.Fprintf(&out, "- Benchmark: `%s`\n", series.Benchmark)
	fmt.Fprintf(&out, "- Subject: `%s`\n", series.Subject)
	fmt.Fprintf(&out, "- Status: `%s`\n", series.Status)
	fmt.Fprintf(&out, "- Evidence class: `%s`\n", series.EvidenceClass)
	fmt.Fprintf(&out, "- Runtime: `%s`\n", series.Runtime)
	fmt.Fprintf(&out, "- Target: `%s` (`%s`, topology `%s`)\n", series.Target, series.TargetEndpointContract, series.TargetTopology)
	fmt.Fprintf(&out, "- Protocol: `%s`\n", series.ProtocolDigest)
	fmt.Fprintf(&out, "- Trials: valid `%d`, failed `%d`, invalid `%d`, planned `%d`\n", series.TrialsValid, series.TrialsFailed, series.TrialsInvalid, series.TrialsPlanned)
	if series.Stats != nil {
		fmt.Fprintf(&out, "- `%s`: median `%g`, mean `%g`, min `%g`, max `%g`\n", series.PrimaryMetric, series.Stats.Median, series.Stats.Mean, series.Stats.Min, series.Stats.Max)
	}
	if len(series.Reasons) > 0 {
		out.WriteString("\n## Reasons\n\n")
		for _, reason := range series.Reasons {
			fmt.Fprintf(&out, "- %s\n", reason)
		}
	}
	return []byte(out.String())
}

func Render(w io.Writer, series Series) error {
	status := strings.ToUpper(series.Status)
	dir := firstNonEmpty(series.ArtifactDir, series.RunDir)
	_, err := fmt.Fprintf(w, "%s: benchmark %s subject=%s runtime=%s valid=%d/%d\nrun_dir=%s\n", status, series.Benchmark, series.Subject, series.Runtime, series.TrialsValid, series.TrialsPlanned, dir)
	return err
}

func RenderJSON(w io.Writer, series Series) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(series)
}

func withDefaults(options Options) Options {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	if options.Now == nil {
		options.Now = time.Now
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
	if options.ParseResult == nil {
		options.ParseResult = pgbenchresult.Parse
	}
	return options
}

func createRunDir(path string) error {
	if info, err := os.Lstat(path); err == nil {
		return fmt.Errorf("benchmark run directory already exists: %s (%s)", path, info.Mode())
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.Mkdir(path, 0o755)
}

func artifactRef(root string, path string) (ArtifactRef, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return ArtifactRef{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		return ArtifactRef{}, fmt.Errorf("artifact is not a non-empty regular file: %s", path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ArtifactRef{}, err
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return ArtifactRef{}, err
	}
	return ArtifactRef{Path: filepath.ToSlash(rel), Digest: digest, Size: info.Size()}, nil
}

func collectRawLogs(root string, dir string) ([]ArtifactRef, error) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return []ArtifactRef{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("raw log path is not a regular directory: %s", dir)
	}
	var refs []ArtifactRef
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		ref, err := artifactRef(root, path)
		if err != nil {
			return err
		}
		refs = append(refs, ref)
		return nil
	})
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	if refs == nil {
		refs = []ArtifactRef{}
	}
	return refs, err
}

func copyRegularFile(source string, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source is not a regular file: %s", source)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeFileAtomic(destination, content, 0o644)
}

func copyRegularFileDigest(source string, expectedDigest string, destinations ...string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source is not a regular file: %s", source)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if !evidence.IsDigest(expectedDigest) || evidence.DigestBytes(content) != expectedDigest {
		return fmt.Errorf("source digest changed after plan construction: %s", source)
	}
	for _, destination := range destinations {
		if err := writeFileAtomic(destination, content, 0o644); err != nil {
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
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pgworkbench-benchmark-*.tmp")
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

func overlayEnv(values []string, fallback func(string) string) func(string) string {
	overrides := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if ok {
			overrides[key] = item
		}
	}
	return func(key string) string {
		if value, ok := overrides[key]; ok {
			return value
		}
		return fallback(key)
	}
}

func bool01(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func evidenceClass(class string) string {
	if class == "smoke" {
		return "unqualified-local-smoke"
	}
	return "unqualified-local-measurement"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func optionalIntString(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func optionalFloatString(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'g', -1, 64)
}

func sanitizeID(value string) string {
	value = strings.NewReplacer("/", "_", " ", "_").Replace(value)
	var out strings.Builder
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '-' {
			out.WriteRune(ch)
		}
	}
	return out.String()
}

// ValidRunID reports whether value is a canonical benchmark series or trial
// identifier. Artifact consumers use the same grammar as the producer so a
// linked-run identity cannot turn into a filesystem path.
func ValidRunID(value string) bool {
	if value == "" || len(value) > 180 {
		return false
	}
	first := value[0]
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z' || first >= '0' && first <= '9') {
		return false
	}
	return sanitizeID(value) == value
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func countNonEmptyLines(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var count int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count, scanner.Err()
}
