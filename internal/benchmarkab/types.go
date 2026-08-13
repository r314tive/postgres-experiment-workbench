// Package benchmarkab implements a counterbalanced PostgreSQL A/B benchmark
// lifecycle. It composes ordinary benchmark series and qualification evidence;
// it does not create a second pgbench parser or claim remote host attestation.
package benchmarkab

import (
	"io"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcompare"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkqualify"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
)

const (
	ProtocolSchemaVersion  = "pgworkbench.benchmark-ab-protocol/v3"
	ProtocolArtifactType   = "pgworkbench.benchmark-ab-protocol"
	RunSchemaVersion       = "pgworkbench.benchmark-ab-run/v3"
	RunArtifactType        = "pgworkbench.benchmark-ab-run"
	SchedulerVersion       = "3.0.0"
	SubjectPGConfig        = "pg_config"
	SubjectNativeToolchain = "native_toolchain"
)

type NativeToolchainIdentity struct {
	ManifestRef     string `json:"manifest_ref"`
	Digest          string `json:"digest"`
	PostgresVersion string `json:"postgres_version"`
	PgbenchVersion  string `json:"pgbench_version"`
	PsqlVersion     string `json:"psql_version"`
	SourceCommit    string `json:"source_commit"`
	BuildProvenance string `json:"build_provenance"`
}

type Subject struct {
	Role            string                   `json:"role"`
	Benchmark       string                   `json:"benchmark"`
	Subject         string                   `json:"subject"`
	ProtocolDigest  string                   `json:"protocol_digest"`
	PGConfig        string                   `json:"pg_config"`
	PGConfigDigest  string                   `json:"pg_config_digest"`
	NativeToolchain *NativeToolchainIdentity `json:"native_toolchain,omitempty"`
}

type AnalysisProtocol struct {
	BootstrapMethod    string  `json:"bootstrap_method"`
	BootstrapResamples int     `json:"bootstrap_resamples"`
	ConfidenceLevel    float64 `json:"confidence_level"`
	Seed               uint64  `json:"seed"`
}

type QualificationProtocol struct {
	Policy          benchmarkqualify.Policy `json:"policy"`
	PolicyDigest    string                  `json:"policy_digest"`
	StorageLabel    string                  `json:"storage_label"`
	ClientPlacement string                  `json:"client_placement"`
}

type EffectiveSettingsProtocol struct {
	ParserVersion             string   `json:"parser_version"`
	SourcePath                string   `json:"source_path"`
	Names                     []string `json:"names"`
	RequireCrossArmDifference bool     `json:"require_cross_arm_difference"`
}

type Protocol struct {
	SchemaVersion          string                    `json:"schema_version"`
	ArtifactType           string                    `json:"artifact_type"`
	SchedulerVersion       string                    `json:"scheduler_version"`
	RunID                  string                    `json:"run_id"`
	Runtime                string                    `json:"runtime"`
	SubjectDimension       string                    `json:"subject_dimension"`
	Baseline               Subject                   `json:"baseline"`
	Candidate              Subject                   `json:"candidate"`
	ComparisonKeyDigest    string                    `json:"comparison_key_digest"`
	BlocksPlanned          int                       `json:"blocks_planned"`
	MinValidUnits          int                       `json:"min_valid_units"`
	Orders                 []string                  `json:"orders"`
	PrimaryMetric          string                    `json:"primary_metric"`
	Direction              string                    `json:"direction"`
	RegressionThresholdPct float64                   `json:"regression_threshold_pct"`
	Analysis               AnalysisProtocol          `json:"analysis"`
	Qualification          QualificationProtocol     `json:"qualification"`
	EffectiveSettings      EffectiveSettingsProtocol `json:"effective_settings"`
	MaxBookendGapSeconds   int64                     `json:"max_bookend_gap_seconds"`
	Digest                 string                    `json:"digest"`
}

type FileRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type SeriesRef struct {
	Role         string `json:"role"`
	RunID        string `json:"run_id"`
	Ref          string `json:"ref"`
	ResultDigest string `json:"result_digest"`
}

type BlockExecution struct {
	Position    int    `json:"position"`
	Role        string `json:"role"`
	SeriesRunID string `json:"series_run_id"`
	Trial       int    `json:"trial"`
	TrialRunID  string `json:"trial_run_id"`
	Status      string `json:"status"`
	PhaseDigest string `json:"phase_digest,omitempty"`
}

type Block struct {
	Number         int              `json:"number"`
	Unit           int              `json:"unit"`
	PlannedOrder   string           `json:"planned_order"`
	Executions     []BlockExecution `json:"executions"`
	Status         string           `json:"status"`
	Reasons        []string         `json:"reasons"`
	BaselineValue  *float64         `json:"baseline_value,omitempty"`
	CandidateValue *float64         `json:"candidate_value,omitempty"`
	EffectPct      *float64         `json:"effect_pct,omitempty"`
}

type QualificationResult struct {
	Before     FileRef                            `json:"before"`
	After      FileRef                            `json:"after"`
	Assessment benchmarkqualify.BookendAssessment `json:"assessment"`
}

type EffectiveSettingsAssessment struct {
	Status                    string   `json:"status"`
	Names                     []string `json:"names"`
	BaselineServerVersionNum  string   `json:"baseline_server_version_num,omitempty"`
	CandidateServerVersionNum string   `json:"candidate_server_version_num,omitempty"`
	EffectiveDifferences      []string `json:"effective_differences"`
	Reasons                   []string `json:"reasons"`
}

type Result struct {
	SchemaVersion     string                          `json:"schema_version"`
	ArtifactType      string                          `json:"artifact_type"`
	SchedulerVersion  string                          `json:"scheduler_version"`
	RunID             string                          `json:"run_id"`
	RunDir            string                          `json:"run_dir"`
	ProtocolRef       FileRef                         `json:"protocol"`
	StartedAt         string                          `json:"started_at"`
	FinishedAt        string                          `json:"finished_at"`
	Baseline          SeriesRef                       `json:"baseline"`
	Candidate         SeriesRef                       `json:"candidate"`
	Blocks            []Block                         `json:"blocks"`
	Qualification     QualificationResult             `json:"qualification"`
	EffectiveSettings EffectiveSettingsAssessment     `json:"effective_settings"`
	Analysis          benchmarkcompare.PairedAnalysis `json:"analysis"`
	Status            string                          `json:"status"`
	Decision          string                          `json:"decision"`
	Reasons           []string                        `json:"reasons"`
	Digest            string                          `json:"digest"`
	ArtifactDir       string                          `json:"-"`
}

type HostInspector func(benchmarkqualify.InspectOptions) (benchmarkqualify.Artifact, error)
type NativeRuntimeStopper func(root, bindir string, getenv func(string) string, stdout, stderr io.Writer) error

type Options struct {
	Runtime               string
	SubjectDimension      string
	BaselineNativeBindir  string
	CandidateNativeBindir string
	RunID                 string
	BaselineSubject       string
	CandidateSubject      string
	BootstrapResamples    int
	ConfidenceLevel       float64
	Seed                  uint64
	Qualification         benchmarkqualify.InspectOptions
	MaxBookendGapSeconds  int64
	SeriesOptions         benchmarkrun.Options
	InspectHost           HostInspector
	StopNativeRuntime     NativeRuntimeStopper
	Now                   func() time.Time
	Stdout                io.Writer
	Stderr                io.Writer
}

type VerifyResult struct {
	Dir    string   `json:"dir"`
	Valid  bool     `json:"valid"`
	Issues []string `json:"issues"`
	Result *Result  `json:"result,omitempty"`
}

func (result VerifyResult) IsValid() bool { return len(result.Issues) == 0 }
