package runstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	ManifestSchemaVersion = "pgworkbench.run-manifest/v1"
	VerdictSchemaVersion  = "pgworkbench.run-verdict/v1"
	ManifestArtifactType  = "pgworkbench.run-manifest"
	VerdictArtifactType   = "pgworkbench.run-verdict"

	RuntimeFingerprintUnavailable = "unavailable"
	RuntimeFingerprintObserved    = "observed"
	EngineIdentityUnverified      = "unverified"
	VerdictStatusPassed           = "passed"
	VerdictStatusFailed           = "failed"
)

var (
	engineVersionPattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	engineCommitPattern    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	effectiveParameterKeys = []string{
		"EXPERIMENT_TOPOLOGY", "EXPERIMENT_PG_CONFIG", "EXPERIMENT_PROFILE",
		"EXPERIMENT_PROFILE_SIZE", "EXPERIMENT_PROFILE_SECONDS", "EXPERIMENT_PROFILE_SETUP",
		"EXPERIMENT_PROFILE_RUN", "EXPERIMENT_PROFILE_RUN_SQL", "EXPERIMENT_DATASET_SPEC",
		"EXPERIMENT_DATASET_SIZE", "EXPERIMENT_WORKLOAD_SPEC", "EXPERIMENT_BACKGROUND_SPECS",
		"EXPERIMENT_BACKGROUND_WARMUP", "EXPERIMENT_BACKGROUND_WAIT", "EXPERIMENT_BEFORE_SQL_FILES",
		"EXPERIMENT_BEFORE_SQL", "EXPERIMENT_BEFORE_SHELL", "EXPERIMENT_AFTER_SQL_FILES",
		"EXPERIMENT_AFTER_SQL", "EXPERIMENT_AFTER_SHELL", "EXPERIMENT_ASSERT_SQL_FILES",
		"EXPERIMENT_ASSERT_SQL", "EXPERIMENT_ASSERT_TRUE_SQL", "EXPERIMENT_ASSERT_SHELL",
		"EXPERIMENT_METRICS", "EXPERIMENT_METRICS_INTERVAL", "EXPERIMENT_METRICS_DURATION",
		"EXPERIMENT_METRICS_SAMPLES", "EXPERIMENT_SNAPSHOT", "EXPERIMENT_RUNTIME_RESET",
		"EXPERIMENT_DOCKER_RESET", "EXPERIMENT_CAPTURE_FILES", "EXPERIMENT_SCAN_PATHS", "PROFILE_SIZE", "PROFILE_SECONDS",
		"DATASET_SIZE", "PG_CONFIG", "PGWORKBENCH_RUNTIME",
		"PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST",
		"PGWORKBENCH_EXECUTION_TIMEOUT", "PGWORKBENCH_CLEANUP_GRACE",
		"PGBENCH_RESET", "PGBENCH_INIT", "PGBENCH_SCALE", "PGBENCH_CLIENTS", "PGBENCH_THREADS",
		"PGBENCH_TIME", "PGBENCH_TRANSACTIONS", "PGBENCH_WARMUP_TIME", "PGBENCH_SCRIPT", "PGBENCH_MODE",
		"PGBENCH_PROTOCOL", "PGBENCH_CONNECT_PER_TRANSACTION", "PGBENCH_RATE", "PGBENCH_LATENCY_LIMIT", "PGBENCH_RANDOM_SEED", "PGBENCH_WARMUP_RANDOM_SEED", "PGBENCH_MEASURE_RANDOM_SEED",
		"PGBENCH_MAX_TRIES", "PGBENCH_PROGRESS", "PGBENCH_LOG_TRANSACTIONS", "PGBENCH_LOG_SAMPLE_RATE",
		"PGBENCH_EXTRA_ARGS", "PGBENCH_TARGET",
		"PGBOUNCER_HOST", "PGBOUNCER_PORT", "PGBOUNCER_IMAGE", "PGBOUNCER_POOL_MODE", "PGBOUNCER_AUTH_TYPE",
		"PGBOUNCER_MAX_CLIENT_CONN", "PGBOUNCER_DEFAULT_POOL_SIZE", "PGBOUNCER_MIN_POOL_SIZE", "PGBOUNCER_RESERVE_POOL_SIZE",
		"PGBOUNCER_IGNORE_STARTUP_PARAMETERS", "PGBOUNCER_ADMIN_USERS", "PGBOUNCER_STATS_USERS",
	}
)

type Manifest struct {
	SchemaVersion             string
	ArtifactType              string
	RunID                     string
	StartedAt                 string
	ExperimentSpec            string
	ExperimentSpecID          string
	ExperimentSpecRef         string
	ExperimentSpecDigest      string
	SourceSpecKind            string
	SourceSpecID              string
	SourceSpecRef             string
	SourceSpecDigest          string
	ExecutionParametersDigest string
	ExperimentIdentityDigest  string
	Runtime                   string
	EngineVersion             string
	EngineCommit              string
	PackID                    string
	PackVersion               string
	PackDigest                string
	RuntimeFingerprintStatus  string
	RuntimeFingerprintTarget  string
	RuntimeOS                 string
	RuntimeArch               string
	PostgresServerVersionNum  string
	PostgresServerMajor       string
	RuntimeFingerprintAt      string
	ExperimentName            string
	ExperimentTopology        string
	ExperimentPGConfig        string
	Profile                   string
	DatasetSpec               string
	ProfileSize               string
	WorkloadSpec              string
	BackgroundSpecs           string
	MetricsEnabled            string
	MetricsSamples            string
	ArtifactRoot              string
	RunDir                    string
}

type Verdict struct {
	SchemaVersion            string `json:"schema_version,omitempty"`
	ArtifactType             string `json:"artifact_type,omitempty"`
	RunID                    string `json:"run_id"`
	Status                   string `json:"status"`
	Message                  string `json:"message"`
	StartedAt                string `json:"started_at"`
	FinishedAt               string `json:"finished_at"`
	ExperimentSpecID         string `json:"experiment_spec"`
	ExperimentSpecDigest     string `json:"experiment_spec_digest,omitempty"`
	ExperimentIdentityDigest string `json:"experiment_identity_digest,omitempty"`
	ManifestDigest           string `json:"manifest_digest,omitempty"`
	ArtifactRoot             string `json:"artifact_root,omitempty"`
	RunDir                   string `json:"run_dir"`
	WorkloadExit             int    `json:"workload_exit"`
	AssertExit               int    `json:"assert_exit"`
	ScanExit                 int    `json:"scan_exit"`
}

type ExperimentIdentity struct {
	SpecID                    string `json:"spec_id"`
	Topology                  string `json:"topology"`
	PGConfig                  string `json:"pg_config"`
	Profile                   string `json:"profile"`
	DatasetSpec               string `json:"dataset_spec"`
	ProfileSize               string `json:"profile_size"`
	WorkloadSpec              string `json:"workload_spec"`
	BackgroundSpecs           string `json:"background_specs"`
	MetricsEnabled            string `json:"metrics_enabled"`
	MetricsSamples            string `json:"metrics_samples"`
	Runtime                   string `json:"runtime"`
	EngineVersion             string `json:"engine_version"`
	EngineCommit              string `json:"engine_commit"`
	PackID                    string `json:"pack_id"`
	PackVersion               string `json:"pack_version"`
	PackDigest                string `json:"pack_digest"`
	SourceSpecKind            string `json:"source_spec_kind"`
	SourceSpecID              string `json:"source_spec_id"`
	SourceSpecRef             string `json:"source_spec_ref"`
	SourceSpecDigest          string `json:"source_spec_digest"`
	RuntimeFingerprintStatus  string `json:"runtime_fingerprint_status"`
	RuntimeFingerprintTarget  string `json:"runtime_fingerprint_target"`
	RuntimeOS                 string `json:"runtime_os"`
	RuntimeArch               string `json:"runtime_arch"`
	PostgresServerVersionNum  string `json:"postgres_server_version_num"`
	PostgresServerMajor       string `json:"postgres_server_major"`
	ExecutionParametersDigest string `json:"execution_parameters_digest"`
}

func ManifestFromEnv(getenv func(string) string) Manifest {
	experimentSpecID := getenv("EXPERIMENT_SPEC_ID")
	experimentSpecDigest := valueOr(getenv("EXPERIMENT_SPEC_DIGEST"), getenv("EXPERIMENT_SPEC_SHA256"))
	manifest := Manifest{
		SchemaVersion:             ManifestSchemaVersion,
		ArtifactType:              ManifestArtifactType,
		RunID:                     getenv("RUN_ID"),
		StartedAt:                 getenv("STARTED_AT"),
		ExperimentSpec:            getenv("EXPERIMENT_SPEC_FILE"),
		ExperimentSpecID:          experimentSpecID,
		ExperimentSpecRef:         getenv("EXPERIMENT_SPEC_REF"),
		ExperimentSpecDigest:      canonicalDigest(experimentSpecDigest),
		SourceSpecKind:            getenv("PGWORKBENCH_SOURCE_SPEC_KIND"),
		SourceSpecID:              getenv("PGWORKBENCH_SOURCE_SPEC_ID"),
		SourceSpecRef:             getenv("PGWORKBENCH_SOURCE_SPEC_REF"),
		SourceSpecDigest:          canonicalDigest(getenv("PGWORKBENCH_SOURCE_SPEC_DIGEST")),
		ExecutionParametersDigest: effectiveParametersDigest(getenv),
		ExperimentIdentityDigest:  getenv("EXPERIMENT_IDENTITY_DIGEST"),
		Runtime:                   valueOr(getenv("PGWORKBENCH_RUNTIME"), "docker"),
		EngineVersion:             NormalizeEngineVersion(getenv("PGWORKBENCH_ENGINE_VERSION")),
		EngineCommit:              NormalizeEngineCommit(getenv("PGWORKBENCH_ENGINE_COMMIT")),
		PackID:                    getenv("PGWORKBENCH_PACK_ID"),
		PackVersion:               getenv("PGWORKBENCH_PACK_VERSION"),
		PackDigest:                canonicalDigest(getenv("PGWORKBENCH_PACK_DIGEST")),
		RuntimeFingerprintStatus:  valueOr(getenv("PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS"), RuntimeFingerprintUnavailable),
		RuntimeFingerprintTarget:  getenv("PGWORKBENCH_RUNTIME_FINGERPRINT_TARGET"),
		RuntimeOS:                 getenv("PGWORKBENCH_RUNTIME_OS"),
		RuntimeArch:               getenv("PGWORKBENCH_RUNTIME_ARCH"),
		PostgresServerVersionNum:  getenv("PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM"),
		PostgresServerMajor:       getenv("PGWORKBENCH_POSTGRES_SERVER_MAJOR"),
		RuntimeFingerprintAt:      getenv("PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT"),
		ExperimentName:            valueOr(getenv("EXPERIMENT_NAME"), experimentSpecID),
		ExperimentTopology:        valueOr(getenv("EXPERIMENT_TOPOLOGY"), "single"),
		ExperimentPGConfig:        valueOr(getenv("EXPERIMENT_PG_CONFIG"), valueOr(getenv("PG_CONFIG"), "default")),
		Profile:                   getenv("EXPERIMENT_PROFILE"),
		DatasetSpec:               getenv("EXPERIMENT_DATASET_SPEC"),
		ProfileSize:               valueOr(getenv("EXPERIMENT_PROFILE_SIZE"), valueOr(getenv("PROFILE_SIZE"), "small")),
		WorkloadSpec:              getenv("EXPERIMENT_WORKLOAD_SPEC"),
		BackgroundSpecs:           getenv("EXPERIMENT_BACKGROUND_SPECS"),
		MetricsEnabled:            valueOr(getenv("EXPERIMENT_METRICS"), "1"),
		MetricsSamples:            getenv("EXPERIMENT_METRICS_SAMPLES"),
		ArtifactRoot:              ".",
		RunDir:                    getenv("RUN_DIR"),
	}
	applyManifestDefaults(&manifest, getenv("REPO_DIR"))
	return manifest
}

func VerdictFromEnv(getenv func(string) string, status string, message string, finishedAt string) Verdict {
	if finishedAt == "" {
		finishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return Verdict{
		SchemaVersion:        VerdictSchemaVersion,
		ArtifactType:         VerdictArtifactType,
		RunID:                getenv("RUN_ID"),
		Status:               status,
		Message:              message,
		StartedAt:            getenv("STARTED_AT"),
		FinishedAt:           finishedAt,
		ExperimentSpecID:     getenv("EXPERIMENT_SPEC_ID"),
		ExperimentSpecDigest: canonicalDigest(valueOr(valueOr(getenv("EXPERIMENT_SPEC_DIGEST"), getenv("EXPERIMENT_SPEC_SHA256")), digestFileOrEmpty(getenv("EXPERIMENT_SPEC_FILE")))),
		ArtifactRoot:         ".",
		RunDir:               getenv("RUN_DIR"),
		WorkloadExit:         intFromEnv(getenv, "WORKLOAD_EXIT"),
		AssertExit:           intFromEnv(getenv, "ASSERT_EXIT"),
		ScanExit:             intFromEnv(getenv, "SCAN_EXIT"),
	}
}

func WriteManifest(runDir string, manifest Manifest) error {
	if runDir == "" {
		runDir = manifest.RunDir
	}
	if runDir == "" {
		return fmt.Errorf("run dir is required")
	}
	manifest.RunDir = runDir
	applyManifestDefaults(&manifest, "")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(runDir, "manifest.env")
	manifestLines := []string{
		"schema_version=" + quoteEnvValue(manifest.SchemaVersion),
		"artifact_type=" + quoteEnvValue(manifest.ArtifactType),
		"run_id=" + quoteEnvValue(manifest.RunID),
		"started_at=" + quoteEnvValue(manifest.StartedAt),
		"experiment_spec=" + quoteEnvValue(manifest.ExperimentSpec),
		"experiment_spec_id=" + quoteEnvValue(manifest.ExperimentSpecID),
		"experiment_spec_ref=" + quoteEnvValue(manifest.ExperimentSpecRef),
		"experiment_spec_digest=" + quoteEnvValue(manifest.ExperimentSpecDigest),
		"source_spec_kind=" + quoteEnvValue(manifest.SourceSpecKind),
		"source_spec_id=" + quoteEnvValue(manifest.SourceSpecID),
		"source_spec_ref=" + quoteEnvValue(manifest.SourceSpecRef),
		"source_spec_digest=" + quoteEnvValue(manifest.SourceSpecDigest),
		"execution_parameters_digest=" + quoteEnvValue(manifest.ExecutionParametersDigest),
		"experiment_identity_digest=" + quoteEnvValue(manifest.ExperimentIdentityDigest),
		"runtime=" + quoteEnvValue(manifest.Runtime),
		"engine_version=" + quoteEnvValue(manifest.EngineVersion),
		"engine_commit=" + quoteEnvValue(manifest.EngineCommit),
		"pack_id=" + quoteEnvValue(manifest.PackID),
		"pack_version=" + quoteEnvValue(manifest.PackVersion),
		"pack_digest=" + quoteEnvValue(manifest.PackDigest),
		"runtime_fingerprint_status=" + quoteEnvValue(manifest.RuntimeFingerprintStatus),
		"runtime_fingerprint_target=" + quoteEnvValue(manifest.RuntimeFingerprintTarget),
		"runtime_os=" + quoteEnvValue(manifest.RuntimeOS),
		"runtime_arch=" + quoteEnvValue(manifest.RuntimeArch),
		"postgres_server_version_num=" + quoteEnvValue(manifest.PostgresServerVersionNum),
		"postgres_server_major=" + quoteEnvValue(manifest.PostgresServerMajor),
		"runtime_fingerprint_observed_at=" + quoteEnvValue(manifest.RuntimeFingerprintAt),
		"experiment_name=" + quoteEnvValue(manifest.ExperimentName),
		"experiment_topology=" + quoteEnvValue(manifest.ExperimentTopology),
		"experiment_pg_config=" + quoteEnvValue(manifest.ExperimentPGConfig),
		"profile=" + quoteEnvValue(manifest.Profile),
		"dataset_spec=" + quoteEnvValue(manifest.DatasetSpec),
		"profile_size=" + quoteEnvValue(manifest.ProfileSize),
		"workload_spec=" + quoteEnvValue(manifest.WorkloadSpec),
		"background_specs=" + quoteEnvValue(manifest.BackgroundSpecs),
		"metrics_enabled=" + quoteEnvValue(manifest.MetricsEnabled),
		"metrics_samples=" + quoteEnvValue(manifest.MetricsSamples),
		"artifact_root=" + quoteEnvValue(manifest.ArtifactRoot),
		"run_dir=" + quoteEnvValue("."),
	}
	content := strings.Join(manifestLines, "\n") + "\n"
	return writeEnvFileAtomic(path, content)
}

func WriteVerdict(runDir string, verdict Verdict) error {
	if runDir == "" {
		runDir = verdict.RunDir
	}
	if runDir == "" {
		return fmt.Errorf("run dir is required")
	}
	verdict.RunDir = "."
	if verdict.SchemaVersion == "" {
		verdict.SchemaVersion = VerdictSchemaVersion
	}
	if verdict.ArtifactType == "" {
		verdict.ArtifactType = VerdictArtifactType
	}
	if verdict.ArtifactRoot == "" {
		verdict.ArtifactRoot = "."
	}
	if err := ValidateVerdictOutcome(verdict.Status, verdict.WorkloadExit, verdict.AssertExit, verdict.ScanExit); err != nil {
		return err
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if err := bindVerdictToManifest(runDir, &verdict); err != nil {
		return err
	}

	verdictLines := []string{
		"schema_version=" + quoteEnvValue(verdict.SchemaVersion),
		"artifact_type=" + quoteEnvValue(verdict.ArtifactType),
		"run_id=" + quoteEnvValue(verdict.RunID),
		"status=" + quoteEnvValue(verdict.Status),
		"message=" + quoteEnvValue(verdict.Message),
		"started_at=" + quoteEnvValue(verdict.StartedAt),
		"finished_at=" + quoteEnvValue(verdict.FinishedAt),
		"experiment_spec_id=" + quoteEnvValue(verdict.ExperimentSpecID),
		"experiment_spec_digest=" + quoteEnvValue(verdict.ExperimentSpecDigest),
		"experiment_identity_digest=" + quoteEnvValue(verdict.ExperimentIdentityDigest),
		"manifest_digest=" + quoteEnvValue(verdict.ManifestDigest),
		"artifact_root=" + quoteEnvValue(verdict.ArtifactRoot),
		"run_dir=" + quoteEnvValue(verdict.RunDir),
		"workload_exit=" + quoteEnvValue(fmt.Sprintf("%d", verdict.WorkloadExit)),
		"assert_exit=" + quoteEnvValue(fmt.Sprintf("%d", verdict.AssertExit)),
		"scan_exit=" + quoteEnvValue(fmt.Sprintf("%d", verdict.ScanExit)),
	}
	if err := writeEnvFileAtomic(filepath.Join(runDir, "verdict.env"), strings.Join(verdictLines, "\n")+"\n"); err != nil {
		return err
	}

	jsonBytes, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return err
	}
	jsonBytes = append(jsonBytes, '\n')
	return writeBytesAtomic(filepath.Join(runDir, "verdict.json"), jsonBytes)
}

// ValidateVerdictStatus enforces the closed verdict status set shared by the
// writer and artifact verifier.
func ValidateVerdictStatus(status string) error {
	switch status {
	case VerdictStatusPassed, VerdictStatusFailed:
		return nil
	default:
		return fmt.Errorf("verdict status must be %s or %s, got %q", VerdictStatusPassed, VerdictStatusFailed, status)
	}
}

// ValidateVerdictOutcome makes the terminal status an exact summary of the
// recorded stage exits. Runner failures outside a named stage are recorded in
// workload_exit by the terminal cleanup path.
func ValidateVerdictOutcome(status string, workloadExit int, assertExit int, scanExit int) error {
	if err := ValidateVerdictStatus(status); err != nil {
		return err
	}
	if status == VerdictStatusPassed && (workloadExit != 0 || assertExit != 0 || scanExit != 0) {
		return fmt.Errorf(
			"passed verdict requires zero exit codes: workload_exit=%d assert_exit=%d scan_exit=%d",
			workloadExit,
			assertExit,
			scanExit,
		)
	}
	if status == VerdictStatusFailed && workloadExit == 0 && assertExit == 0 && scanExit == 0 {
		return fmt.Errorf("failed verdict requires at least one nonzero exit code")
	}
	return nil
}

func writeEnvFileAtomic(path string, content string) error {
	return writeBytesAtomic(path, []byte(content))
}

func writeBytesAtomic(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pgworkbench-state-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (manifest Manifest) Identity() ExperimentIdentity {
	fingerprintTarget := manifest.RuntimeFingerprintTarget
	if fingerprintTarget == "" {
		fingerprintTarget = defaultRuntimeFingerprintTarget(manifest.ExperimentTopology)
	}
	return ExperimentIdentity{
		SpecID:                    manifest.ExperimentSpecID,
		Topology:                  valueOr(manifest.ExperimentTopology, "single"),
		PGConfig:                  valueOr(manifest.ExperimentPGConfig, "default"),
		Profile:                   manifest.Profile,
		DatasetSpec:               manifest.DatasetSpec,
		ProfileSize:               valueOr(manifest.ProfileSize, "small"),
		WorkloadSpec:              manifest.WorkloadSpec,
		BackgroundSpecs:           manifest.BackgroundSpecs,
		MetricsEnabled:            valueOr(manifest.MetricsEnabled, "1"),
		MetricsSamples:            manifest.MetricsSamples,
		Runtime:                   valueOr(manifest.Runtime, "docker"),
		EngineVersion:             NormalizeEngineVersion(manifest.EngineVersion),
		EngineCommit:              NormalizeEngineCommit(manifest.EngineCommit),
		PackID:                    manifest.PackID,
		PackVersion:               manifest.PackVersion,
		PackDigest:                manifest.PackDigest,
		SourceSpecKind:            manifest.SourceSpecKind,
		SourceSpecID:              manifest.SourceSpecID,
		SourceSpecRef:             manifest.SourceSpecRef,
		SourceSpecDigest:          manifest.SourceSpecDigest,
		RuntimeFingerprintStatus:  valueOr(manifest.RuntimeFingerprintStatus, RuntimeFingerprintUnavailable),
		RuntimeFingerprintTarget:  fingerprintTarget,
		RuntimeOS:                 manifest.RuntimeOS,
		RuntimeArch:               manifest.RuntimeArch,
		PostgresServerVersionNum:  manifest.PostgresServerVersionNum,
		PostgresServerMajor:       manifest.PostgresServerMajor,
		ExecutionParametersDigest: manifest.ExecutionParametersDigest,
	}
}

func (identity ExperimentIdentity) Digest() string {
	content, err := json.Marshal(identity)
	if err != nil {
		panic(err)
	}
	return evidence.DigestBytes(content)
}

func applyManifestDefaults(manifest *Manifest, repoDir string) {
	if manifest.SchemaVersion == "" {
		manifest.SchemaVersion = ManifestSchemaVersion
	}
	if manifest.ArtifactType == "" {
		manifest.ArtifactType = ManifestArtifactType
	}
	if manifest.ExperimentTopology == "" {
		manifest.ExperimentTopology = "single"
	}
	if manifest.ExperimentPGConfig == "" {
		manifest.ExperimentPGConfig = "default"
	}
	if manifest.ProfileSize == "" {
		manifest.ProfileSize = "small"
	}
	if manifest.MetricsEnabled == "" {
		manifest.MetricsEnabled = "1"
	}
	if manifest.Runtime == "" {
		manifest.Runtime = "docker"
	}
	manifest.EngineVersion = NormalizeEngineVersion(manifest.EngineVersion)
	manifest.EngineCommit = NormalizeEngineCommit(manifest.EngineCommit)
	if manifest.RuntimeFingerprintStatus == "" {
		manifest.RuntimeFingerprintStatus = RuntimeFingerprintUnavailable
	}
	if manifest.RuntimeFingerprintTarget == "" {
		manifest.RuntimeFingerprintTarget = defaultRuntimeFingerprintTarget(manifest.ExperimentTopology)
	}
	if manifest.RuntimeFingerprintStatus == RuntimeFingerprintObserved {
		if manifest.RuntimeOS == "" {
			manifest.RuntimeOS = runtime.GOOS
		}
		if manifest.RuntimeArch == "" {
			manifest.RuntimeArch = runtime.GOARCH
		}
	}
	if manifest.ArtifactRoot == "" {
		manifest.ArtifactRoot = "."
	}
	if manifest.ExperimentSpecRef == "" {
		manifest.ExperimentSpecRef = portableSpecRef(manifest.ExperimentSpec, repoDir, manifest.ExperimentSpecID)
	}
	if manifest.ExperimentSpecDigest == "" {
		manifest.ExperimentSpecDigest = digestFileOrEmpty(manifest.ExperimentSpec)
	}
	manifest.ExperimentSpecDigest = canonicalDigest(manifest.ExperimentSpecDigest)
	manifest.SourceSpecDigest = canonicalDigest(manifest.SourceSpecDigest)
	manifest.PackDigest = canonicalDigest(manifest.PackDigest)
	manifest.ExecutionParametersDigest = canonicalDigest(manifest.ExecutionParametersDigest)
	if manifest.ExecutionParametersDigest == "" {
		manifest.ExecutionParametersDigest = evidence.DigestBytes([]byte("{}"))
	}
	if manifest.ExperimentIdentityDigest == "" {
		manifest.ExperimentIdentityDigest = manifest.Identity().Digest()
	}
}

// NormalizeEngineVersion preserves a canonical SemVer build identity and maps
// missing or non-release source identities to an explicit unverified sentinel.
func NormalizeEngineVersion(value string) string {
	value = strings.TrimSpace(value)
	if !IsEngineVersion(value) {
		return EngineIdentityUnverified
	}
	return value
}

// NormalizeEngineCommit preserves a full SHA-1 or SHA-256 Git object ID only. Short hashes and
// unknown/dirty source labels are deliberately represented as unverified.
func NormalizeEngineCommit(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !IsEngineCommit(value) {
		return EngineIdentityUnverified
	}
	return value
}

func IsEngineVersion(value string) bool {
	return value == EngineIdentityUnverified || engineVersionPattern.MatchString(value)
}

func IsEngineCommit(value string) bool {
	return value == EngineIdentityUnverified || engineCommitPattern.MatchString(value)
}

func defaultRuntimeFingerprintTarget(topology string) string {
	if topology == "multi-version-upgrade" {
		return "upgrade-new"
	}
	return "primary"
}

// EffectiveParametersDigest returns the canonical digest of the execution
// parameters retained in a v1 run manifest. Callers that independently know
// the resolved parameter set (for example the benchmark artifact verifier)
// use this function so producer and verifier cannot silently drift apart.
func EffectiveParametersDigest(getenv func(string) string) string {
	values := make(map[string]string, len(effectiveParameterKeys))
	for _, key := range effectiveParameterKeys {
		values[key] = getenv(key)
	}
	content, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	return evidence.DigestBytes(content)
}

// ExecutionParameterKeys returns the complete v1 execution-parameter key set.
// Runners use it to shadow every ambient value before a benchmark starts.
func ExecutionParameterKeys() []string {
	return append([]string(nil), effectiveParameterKeys...)
}

func effectiveParametersDigest(getenv func(string) string) string {
	return EffectiveParametersDigest(getenv)
}

func bindVerdictToManifest(runDir string, verdict *Verdict) error {
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifestDigest, err := evidence.DigestFile(manifestPath)
	if err != nil {
		return fmt.Errorf("digest manifest.env: %w", err)
	}
	verdict.ManifestDigest = manifestDigest

	manifest, err := envfile.Parse(manifestPath)
	if err != nil {
		return fmt.Errorf("parse manifest.env: %w", err)
	}
	if verdict.ExperimentSpecDigest == "" {
		verdict.ExperimentSpecDigest = manifest["experiment_spec_digest"]
	}
	if verdict.ExperimentIdentityDigest == "" {
		verdict.ExperimentIdentityDigest = manifest["experiment_identity_digest"]
	}
	if verdict.Status == "passed" && manifest["schema_version"] == ManifestSchemaVersion && manifest["runtime_fingerprint_status"] != RuntimeFingerprintObserved {
		return fmt.Errorf("passed verdict requires an observed runtime fingerprint")
	}
	return nil
}

func portableSpecRef(specPath string, repoDir string, specID string) string {
	if specPath != "" {
		if repoDir != "" {
			if rel, err := filepath.Rel(repoDir, specPath); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(filepath.Clean(rel))
			}
		}
		if !filepath.IsAbs(specPath) {
			return filepath.ToSlash(filepath.Clean(specPath))
		}
		slashPath := filepath.ToSlash(filepath.Clean(specPath))
		if index := strings.LastIndex(slashPath, "/experiments/"); index >= 0 {
			return strings.TrimPrefix(slashPath[index+1:], "/")
		}
	}
	if specID == "" {
		return ""
	}
	ref := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(specID)), "experiments/")
	ref = strings.TrimSuffix(ref, ".env")
	return "experiments/" + ref + ".env"
}

func digestFileOrEmpty(path string) string {
	if path == "" {
		return ""
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return ""
	}
	return digest
}

func canonicalDigest(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || evidence.IsDigest(value) {
		return value
	}
	if candidate := evidence.DigestPrefix + value; evidence.IsDigest(candidate) {
		return candidate
	}
	return value
}

func quoteEnvValue(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"':
			out.WriteString("\\\"")
		case '\\':
			out.WriteString("\\\\")
		case '`':
			out.WriteString("\\`")
		case '$':
			out.WriteString("\\$")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		default:
			out.WriteByte(value[i])
		}
	}
	out.WriteByte('"')
	return out.String()
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func intFromEnv(getenv func(string) string, key string) int {
	value := getenv(key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}
