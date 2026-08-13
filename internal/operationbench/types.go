package operationbench

import "github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"

const (
	SpecSchemaVersion   = "pgworkbench.operation-benchmark-spec/v1"
	ResultSchemaVersion = "pgworkbench.operation-result/v1"
	ResultArtifactType  = "pgworkbench.operation-result"
	SeriesSchemaVersion = "pgworkbench.operation-benchmark-series/v1"
	SeriesArtifactType  = "pgworkbench.operation-benchmark-series"
	Classification      = "descriptive-engineering"
	BundleSchemaVersion = "pgworkbench.operation-benchmark-bundle/v1"
	BundleArtifactType  = "pgworkbench.operation-benchmark-bundle"
	BundleInventoryName = "operation-benchmark-bundle.json"
	EngineBinaryRef     = "protocol/engine/pgworkbench"
	NativeManifestRef   = "protocol/native-toolchain/manifest.json"
)

type Spec struct {
	SchemaVersion    string      `json:"schema_version"`
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Description      string      `json:"description"`
	Classification   string      `json:"classification"`
	DecisionEligible bool        `json:"decision_eligible"`
	ExperimentSpec   string      `json:"experiment_spec"`
	Trials           int         `json:"trials"`
	MaxCVPct         float64     `json:"max_cv_pct"`
	SupportedRuntime []string    `json:"supported_runtimes"`
	Measurement      Measurement `json:"measurement"`
	Assurance        string      `json:"assurance"`
	Path             string      `json:"-"`
	Digest           string      `json:"-"`
}

type Measurement struct {
	Basis      string `json:"basis"`
	ResultPath string `json:"result_path,omitempty"`
	Metric     string `json:"metric"`
	Unit       string `json:"unit"`
	Direction  string `json:"direction"`
	Scope      string `json:"scope"`
}

type OperationResult struct {
	SchemaVersion string        `json:"schema_version"`
	ArtifactType  string        `json:"artifact_type"`
	OperationID   string        `json:"operation_id"`
	Variant       string        `json:"variant"`
	PrimaryMetric ResultMetric  `json:"primary_metric"`
	Measurement   ResultMeasure `json:"measurement"`
}

type ResultMetric struct {
	Name      string  `json:"name"`
	Unit      string  `json:"unit"`
	Direction string  `json:"direction"`
	Value     float64 `json:"value"`
}

type ResultMeasure struct {
	Basis string `json:"basis"`
	Scope string `json:"scope"`
}

type InputFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type Trial struct {
	Trial                     int              `json:"trial"`
	RunID                     string           `json:"run_id"`
	RunRef                    string           `json:"run_ref"`
	RunDigest                 string           `json:"run_digest"`
	StartedAt                 string           `json:"started_at"`
	FinishedAt                string           `json:"finished_at"`
	DurationMS                int64            `json:"duration_ms"`
	Status                    string           `json:"status"`
	Reasons                   []string         `json:"reasons"`
	ExperimentVerified        bool             `json:"experiment_verified"`
	ExecutionParametersDigest string           `json:"execution_parameters_digest"`
	ExperimentIdentityDigest  string           `json:"experiment_identity_digest"`
	PackDigest                string           `json:"pack_digest"`
	ResultRef                 string           `json:"result_ref,omitempty"`
	ResultDigest              string           `json:"result_digest,omitempty"`
	OperationResult           *OperationResult `json:"operation_result,omitempty"`
	PrimaryValue              *float64         `json:"primary_value,omitempty"`
}

type Series struct {
	SchemaVersion              string                    `json:"schema_version"`
	ArtifactType               string                    `json:"artifact_type"`
	Operation                  string                    `json:"operation"`
	Name                       string                    `json:"name"`
	Description                string                    `json:"description"`
	Classification             string                    `json:"classification"`
	DecisionEligible           bool                      `json:"decision_eligible"`
	Assurance                  string                    `json:"assurance"`
	RunID                      string                    `json:"run_id"`
	RunDir                     string                    `json:"run_dir"`
	SpecRef                    string                    `json:"spec_ref"`
	SpecDigest                 string                    `json:"spec_digest"`
	ExperimentSpec             string                    `json:"experiment_spec"`
	ExperimentRef              string                    `json:"experiment_ref"`
	ExperimentDigest           string                    `json:"experiment_digest"`
	EngineVersion              string                    `json:"engine_version"`
	EngineCommit               string                    `json:"engine_commit"`
	EngineBinaryRef            string                    `json:"engine_binary_ref"`
	EngineBinaryDigest         string                    `json:"engine_binary_digest"`
	NativeToolchainDigest      string                    `json:"native_toolchain_digest,omitempty"`
	NativeToolchainManifestRef string                    `json:"native_toolchain_manifest_ref,omitempty"`
	NativeToolchainProvenance  string                    `json:"native_toolchain_provenance,omitempty"`
	ExecutionParametersDigest  string                    `json:"execution_parameters_digest"`
	PackDigest                 string                    `json:"pack_digest"`
	InputsDigest               string                    `json:"inputs_digest"`
	Inputs                     []InputFile               `json:"inputs"`
	Runtime                    string                    `json:"runtime"`
	Topology                   string                    `json:"topology"`
	Measurement                Measurement               `json:"measurement"`
	TrialsPlanned              int                       `json:"trials_planned"`
	TrialsValid                int                       `json:"trials_valid"`
	TrialsFailed               int                       `json:"trials_failed"`
	MaxCVPct                   float64                   `json:"max_cv_pct"`
	StartedAt                  string                    `json:"started_at"`
	FinishedAt                 string                    `json:"finished_at"`
	Status                     string                    `json:"status"`
	Reasons                    []string                  `json:"reasons"`
	Stats                      *pgbenchresult.TrialStats `json:"stats,omitempty"`
	Trials                     []Trial                   `json:"trials"`
	ArtifactDir                string                    `json:"-"`
}

func (series Series) Passed() bool { return series.Status == "passed" }

type VerifyResult struct {
	Dir                     string   `json:"dir"`
	BundleInventoryRequired bool     `json:"bundle_inventory_required"`
	Valid                   bool     `json:"valid"`
	Issues                  []string `json:"issues"`
	Series                  *Series  `json:"series,omitempty"`
}

type BundleFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type BundleInventory struct {
	SchemaVersion string       `json:"schema_version"`
	ArtifactType  string       `json:"artifact_type"`
	SeriesRunID   string       `json:"series_run_id"`
	SeriesRef     string       `json:"series_ref"`
	Files         []BundleFile `json:"files"`
}

type BundleResult struct {
	SeriesRunID string `json:"series_run_id"`
	SeriesDir   string `json:"series_dir"`
	Output      string `json:"output"`
	RootName    string `json:"root_name"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Digest      string `json:"digest"`
	LinkedRuns  int    `json:"linked_runs"`
}
