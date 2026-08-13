// Package benchmarkexternal runs pinned external benchmark drivers through a
// deliberately small native execution surface. Its immutable artifacts are
// descriptive single-trial evidence only: they never enter pgbench series or
// decision paths and they do not attest source-to-binary provenance.
package benchmarkexternal

import (
	"context"
	"io"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkimport"
)

const (
	SchemaVersion                  = "pgworkbench.benchmark-driver-execution/v2"
	ArtifactType                   = "pgworkbench.benchmark-driver-execution"
	ContractVersion                = "2.0.0"
	InventorySchemaVersion         = "pgworkbench.benchmark-driver-execution-inventory/v2"
	InventoryArtifactType          = "pgworkbench.benchmark-driver-execution-inventory"
	SysbenchConfigSchema           = "pgworkbench.sysbench-native-run-config/v1"
	SysbenchConfigArtifact         = "pgworkbench.sysbench-native-run-config"
	HammerDBConfigSchema           = "pgworkbench.hammerdb-v6-native-run-config/v1"
	HammerDBConfigArtifact         = "pgworkbench.hammerdb-v6-native-run-config"
	HammerDBExecutionMode          = "execute-only-prepared-schema"
	HammerDBTemplate               = "pgworkbench.hammerdb-v6-execute-only-template/v1\n"
	Classification                 = "descriptive-external-single-trial"
	AnalysisDesign                 = "native-single-trial"
	Conclusion                     = "descriptive"
	StatusCompleted                = "completed"
	RuntimeNative                  = "native"
	SecretPasswordEnv              = "PGWORKBENCH_DRIVER_PASSWORD"
	ExecutionFile                  = "execution.json"
	InventoryFile                  = "inventory.json"
	LockFile                       = "inputs/driver-lock.json"
	BinaryFile                     = "inputs/binary"
	DriverRuntimeDir               = "inputs/runtime"
	StdoutFile                     = "raw/stdout"
	StderrFile                     = "raw/stderr"
	NormalizedImportDir            = "normalized-import"
	NormalizedImportResult         = "normalized-import/result.json"
	BinaryExecutionMode            = "adapter-owned-staged-driver-runtime-v2"
	DriverRuntimeMode              = "staged-copy-with-pre-post-tree-match"
	EnvironmentPolicy              = "minimal-fixed-v2"
	BenchBaseRuntimeStrategy       = "jar-manifest-transitive-closure/v1"
	HammerDBRuntimeStrategy        = "hammerdb-self-contained-launcher/v1"
	SysbenchRuntimeStrategy        = "sysbench-pinned-lua-closure/v1"
	TargetSafetyPolicy             = "loopback-nonsystem-explicit-disposable-ack/v1"
	TargetAcknowledgement          = "operator-asserted-external-disposable-non-production"
	TargetEndpointSource           = "retained-driver-config"
	maxInputBytes            int64 = 512 << 20
	maxOutputBytes           int64 = 64 << 20
	maxJSONBytes             int64 = 2 << 20
	maxManifestBytes         int64 = 1 << 20
	maxDriverRuntimeBytes    int64 = 4 << 30
	maxArtifactBytes         int64 = 5 << 30
	maxDriverRuntimeFiles          = 16384
	maxArtifactFiles               = 20000
	maxJAREntries                  = 100000
)

type FileRef struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
}

type RegistryBinding struct {
	Lock   FileRef                 `json:"lock"`
	Driver benchmarkdrivers.Driver `json:"driver"`
}

type Invocation struct {
	ExecutableMode    string   `json:"executable_mode"`
	DriverRuntimeMode string   `json:"driver_runtime_mode"`
	Argv              []string `json:"argv"`
	EnvironmentPolicy string   `json:"environment_policy"`
	SecretEnvironment []string `json:"secret_environment"`
	TimeoutSeconds    int64    `json:"timeout_seconds"`
}

// RuntimeFileRef binds the executable mode as well as the bytes retained in
// the staged driver runtime. Runtime files are canonicalized to read-only
// 0444/0555 modes so relocation does not depend on source umasks.
type RuntimeFileRef struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
	Mode      uint32 `json:"mode"`
}

// DriverRuntime is a closed, sorted, independently derivable driver tree. It
// intentionally excludes ambient host runtimes such as the JDK, libc, and
// dynamically loaded system libraries.
type DriverRuntime struct {
	Strategy       string           `json:"strategy"`
	Root           string           `json:"root"`
	Entrypoint     string           `json:"entrypoint"`
	Files          []RuntimeFileRef `json:"files"`
	FileCount      int              `json:"file_count"`
	TotalSizeBytes int64            `json:"total_size_bytes"`
	TreeDigest     string           `json:"tree_digest"`
}

type Inputs struct {
	Binary        FileRef       `json:"binary"`
	Config        FileRef       `json:"config"`
	Script        FileRef       `json:"script"`
	DriverRuntime DriverRuntime `json:"driver_runtime"`
}

type Outputs struct {
	Stdout       FileRef `json:"stdout"`
	Stderr       FileRef `json:"stderr"`
	DriverResult FileRef `json:"driver_result"`
}

type NormalizedImport struct {
	Result         FileRef `json:"result"`
	ArtifactDigest string  `json:"artifact_digest"`
}

// TargetSafety records the deliberately narrow guard applied before invoking
// an external driver. The acknowledgement is an operator assertion, not
// ownership, database-identity, or non-production attestation by workbench.
type TargetSafety struct {
	Policy                  string `json:"policy"`
	Acknowledged            bool   `json:"acknowledged"`
	Acknowledgement         string `json:"acknowledgement"`
	EndpointSource          string `json:"endpoint_source"`
	Host                    string `json:"host"`
	Port                    uint16 `json:"port"`
	Database                string `json:"database"`
	LoopbackOnly            bool   `json:"loopback_only"`
	SystemDatabasesDenied   bool   `json:"system_databases_denied"`
	TargetOwnershipVerified bool   `json:"target_ownership_verified"`
	TargetIdentityAttested  bool   `json:"target_identity_attested"`
}

type Assurance struct {
	EvidenceOrigin                  string `json:"evidence_origin"`
	VerificationScope               string `json:"verification_scope"`
	BinaryDistributedByProject      bool   `json:"binary_distributed_by_project"`
	SourceToBinaryAttested          bool   `json:"source_to_binary_attested"`
	DecisionEligible                bool   `json:"decision_eligible"`
	PGbenchSeriesEligible           bool   `json:"pgbench_series_eligible"`
	CrossSystemComparisonEligible   bool   `json:"cross_system_comparison_eligible"`
	TPCComplianceClaim              bool   `json:"tpc_compliance_claim"`
	DriverRuntimeClosureAttested    bool   `json:"driver_runtime_closure_attested"`
	HostRuntimeDependenciesAttested bool   `json:"host_runtime_dependencies_attested"`
}

type Artifact struct {
	SchemaVersion   string           `json:"schema_version"`
	ArtifactType    string           `json:"artifact_type"`
	ContractVersion string           `json:"contract_version"`
	Classification  string           `json:"classification"`
	AnalysisDesign  string           `json:"analysis_design"`
	Conclusion      string           `json:"conclusion"`
	Status          string           `json:"status"`
	Runtime         string           `json:"runtime"`
	Workload        string           `json:"workload"`
	Registry        RegistryBinding  `json:"registry"`
	Invocation      Invocation       `json:"invocation"`
	TargetSafety    TargetSafety     `json:"target_safety"`
	Inputs          Inputs           `json:"inputs"`
	Outputs         Outputs          `json:"outputs"`
	StartedAt       string           `json:"started_at"`
	FinishedAt      string           `json:"finished_at"`
	ExitCode        int              `json:"exit_code"`
	Normalized      NormalizedImport `json:"normalized_import"`
	Assurance       Assurance        `json:"assurance"`
	Digest          string           `json:"digest"`
	ArtifactDir     string           `json:"-"`
}

type Inventory struct {
	SchemaVersion   string    `json:"schema_version"`
	ArtifactType    string    `json:"artifact_type"`
	ExecutionDigest string    `json:"execution_digest"`
	Files           []FileRef `json:"files"`
}

type SysbenchPostgreSQL struct {
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	User     string `json:"user"`
	Database string `json:"database"`
}

type SysbenchConfig struct {
	SchemaVersion         string             `json:"schema_version"`
	ArtifactType          string             `json:"artifact_type"`
	Threads               uint32             `json:"threads"`
	DurationSeconds       uint32             `json:"duration_seconds"`
	ReportIntervalSeconds uint32             `json:"report_interval_seconds"`
	Rate                  uint32             `json:"rate"`
	RandomSeed            uint64             `json:"random_seed"`
	PostgreSQL            SysbenchPostgreSQL `json:"postgresql"`
}

type HammerDBPostgreSQL struct {
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	User     string `json:"user"`
	Database string `json:"database"`
	SSLMode  string `json:"sslmode"`
}

type HammerDBTPROCC struct {
	Warehouses      uint32 `json:"warehouses"`
	VirtualUsers    uint32 `json:"virtual_users"`
	RampupMinutes   uint32 `json:"rampup_minutes"`
	DurationMinutes uint32 `json:"duration_minutes"`
	TotalIterations uint64 `json:"total_iterations"`
}

type HammerDBTPROCH struct {
	ScaleFactor         uint32 `json:"scale_factor"`
	VirtualUsers        uint32 `json:"virtual_users"`
	QuerySets           uint32 `json:"query_sets"`
	DegreeOfParallelism uint32 `json:"degree_of_parallelism"`
}

// HammerDBConfig is intentionally execute-only. Schema creation, loading,
// deletion, arbitrary Tcl, transaction-counter agents, and metrics agents are
// outside this native adapter's authority.
type HammerDBConfig struct {
	SchemaVersion string             `json:"schema_version"`
	ArtifactType  string             `json:"artifact_type"`
	Mode          string             `json:"mode"`
	PostgreSQL    HammerDBPostgreSQL `json:"postgresql"`
	TPROCC        *HammerDBTPROCC    `json:"tprocc,omitempty"`
	TPROCH        *HammerDBTPROCH    `json:"tproch,omitempty"`
}

type Options struct {
	Root                                string
	DriverID                            string
	BinaryPath                          string
	ConfigPath                          string
	ScriptPath                          string
	RuntimeRoot                         string
	Workload                            string
	OutputDir                           string
	Timeout                             time.Duration
	AcknowledgeExternalDisposableTarget bool
	Getenv                              func(string) string
	Now                                 func() time.Time
	Context                             context.Context
}

type Verification struct {
	Dir      string                        `json:"dir"`
	Valid    bool                          `json:"valid"`
	Issues   []string                      `json:"issues"`
	Artifact *Artifact                     `json:"artifact,omitempty"`
	Import   *benchmarkimport.Verification `json:"normalized_import,omitempty"`
}

func (verification Verification) IsValid() bool { return len(verification.Issues) == 0 }

type RunResult struct {
	Artifact Artifact `json:"artifact"`
}

func RenderJSON(writer io.Writer, value any) error { return renderJSON(writer, value) }
