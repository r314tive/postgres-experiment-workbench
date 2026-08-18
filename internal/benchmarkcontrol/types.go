// Package benchmarkcontrol defines the benchmark protocol v2 control evidence
// contracts. The artifacts are unsigned runner observations. Their digests and
// derived verdicts are independently verifiable, but they do not attest a host,
// defeat a privileged observer, or prove effects outside their stated scope.
package benchmarkcontrol

const (
	MaxCollectorOverheadSamples = 10_000
	maxControlRows              = MaxCollectorOverheadSamples

	CacheStateSchemaVersion        = "pgworkbench.benchmark-cache-state/v1"
	StatisticsResetSchemaVersion   = "pgworkbench.benchmark-statistics-reset/v1"
	CollectorOverheadSchemaVersion = "pgworkbench.benchmark-collector-overhead/v1"
	ResourceBudgetSchemaVersion    = "pgworkbench.benchmark-resource-budget/v1"

	CacheStateArtifactType        = "pgworkbench.benchmark-cache-state"
	StatisticsResetArtifactType   = "pgworkbench.benchmark-statistics-reset"
	CollectorOverheadArtifactType = "pgworkbench.benchmark-collector-overhead"
	ResourceBudgetArtifactType    = "pgworkbench.benchmark-resource-budget"

	CacheStateFile              = "cache-state.json"
	StatisticsResetFile         = "statistics-reset.json"
	CollectorOverheadFile       = "collector-overhead.json"
	ResourceBudgetFile          = "resource-budget.json"
	CacheStateSourceFile        = "cache-state.tsv"
	StatisticsResetSourceFile   = "statistics-reset.tsv"
	CollectorOverheadSourceFile = "collector-overhead.tsv"
	ResourceBudgetSourceFile    = "resource-budget-source.json"

	EvidenceOriginRunnerRecorded = "runner-recorded"
	DigestPurposeIntegrityOnly   = "content-integrity-only"

	CacheVerificationScope    = "structure-digest-raw-source-and-postgres-shared-buffer-residency-derivation-only"
	ResetVerificationScope    = "structure-digest-raw-source-and-reset-timestamp-advancement-derivation-only"
	OverheadVerificationScope = "structure-digest-raw-source-and-runner-monotonic-duty-cycle-derivation-only"
	ResourceVerificationScope = "structure-digest-raw-source-and-recorded-docker-limit-derivation-only"

	CacheModeUncontrolled   = "uncontrolled"
	CacheModeWarm           = "postgres-shared-buffer-warm"
	CacheStatusUncontrolled = "uncontrolled"
	CacheStatusSatisfied    = "satisfied"
	CacheStatusUnsatisfied  = "unsatisfied"

	StatisticsPolicyNone          = "none"
	StatisticsPolicyRunnerManaged = "runner-managed"
	StatisticsStatusNotRequested  = "not-requested"
	StatisticsStatusSucceeded     = "succeeded"
	StatisticsStatusFailed        = "failed"

	OverheadModeIncludedUnquantified = "included-unquantified"
	OverheadModeRunnerCalibrated     = "runner-calibrated-duty-cycle"
	OverheadStatusIncluded           = "included-unquantified"
	OverheadStatusWithinBudget       = "within-budget"
	OverheadStatusExceededBudget     = "exceeded-budget"
	OverheadStatusInvalidSamples     = "invalid-samples"

	ResourceModeUnbounded      = "unbounded"
	ResourceModeRunnerEnforced = "runner-enforced"
	ResourceStatusUnbounded    = "unbounded"
	ResourceStatusEnforced     = "enforced"
	ResourceStatusMismatch     = "mismatch"
	ResourceScope              = "postgres-server-and-pgbench-driver"
	ResourceProvider           = "docker-single-container-linux-cgroup-v2"

	ObservationAvailable   = "observed"
	ObservationNull        = "null"
	ObservationUnavailable = "unavailable"
)

var resourceProviderConstraints = []string{
	"cgroup-v2-required",
	"docker-engine-required",
	"linux-only",
	"postgres-and-driver-share-one-container",
}

func ExpectedResourceProviderConstraints() []string {
	return append([]string(nil), resourceProviderConstraints...)
}

type Assurance struct {
	EvidenceOrigin    string `json:"evidence_origin"`
	Signed            bool   `json:"signed"`
	DigestPurpose     string `json:"digest_purpose"`
	VerificationScope string `json:"verification_scope"`
}

type BoundaryWindow struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

type SourceEvidence struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
}

type Binding struct {
	RunID          string
	ProtocolDigest string
	Trial          int
}

type CacheRelationObservation struct {
	Relation       string  `json:"relation"`
	DatabaseOID    uint32  `json:"database_oid"`
	RelationOID    uint32  `json:"relation_oid"`
	Fork           string  `json:"fork"`
	RelationBlocks uint64  `json:"relation_blocks"`
	ResidentBlocks uint64  `json:"resident_blocks"`
	ResidentPct    float64 `json:"resident_pct"`
}

type CacheState struct {
	SchemaVersion     string                     `json:"schema_version"`
	ArtifactType      string                     `json:"artifact_type"`
	RunID             string                     `json:"run_id"`
	ProtocolDigest    string                     `json:"protocol_digest"`
	Trial             int                        `json:"trial"`
	CapturedAt        string                     `json:"captured_at"`
	Assurance         Assurance                  `json:"assurance"`
	ObservationMethod string                     `json:"observation_method"`
	Boundary          string                     `json:"boundary"`
	SnapshotSemantics string                     `json:"snapshot_semantics"`
	BoundaryWindow    BoundaryWindow             `json:"boundary_window"`
	RawSource         SourceEvidence             `json:"raw_source"`
	Mode              string                     `json:"mode"`
	TargetRelations   []string                   `json:"target_relations"`
	MinResidentPct    *float64                   `json:"min_resident_pct,omitempty"`
	Relations         []CacheRelationObservation `json:"relations"`
	Status            string                     `json:"status"`
	Reasons           []string                   `json:"reasons"`
	Digest            string                     `json:"digest"`
}

type CacheStateInput struct {
	ProtocolDigest  string
	RunID           string
	Trial           int
	CapturedAt      string
	BoundaryWindow  BoundaryWindow
	RawSource       SourceEvidence
	Mode            string
	TargetRelations []string
	MinResidentPct  *float64
	Relations       []CacheRelationObservation
}

type ResetOperation struct {
	Function         string `json:"function"`
	Scope            string `json:"scope"`
	Rows             int    `json:"rows"`
	CommandCompleted bool   `json:"command_completed"`
}

type ResetTimestampObservation struct {
	Availability string `json:"availability"`
	Value        string `json:"value,omitempty"`
}

type StatisticsReset struct {
	SchemaVersion       string                    `json:"schema_version"`
	ArtifactType        string                    `json:"artifact_type"`
	RunID               string                    `json:"run_id"`
	ProtocolDigest      string                    `json:"protocol_digest"`
	Trial               int                       `json:"trial"`
	CapturedAt          string                    `json:"captured_at"`
	Assurance           Assurance                 `json:"assurance"`
	PostgresServerMajor string                    `json:"postgres_server_major"`
	ObservationMethod   string                    `json:"observation_method"`
	Policy              string                    `json:"policy"`
	Boundary            string                    `json:"boundary"`
	BoundaryWindow      BoundaryWindow            `json:"boundary_window"`
	DatabaseBefore      ResetTimestampObservation `json:"database_before"`
	DatabaseAfter       ResetTimestampObservation `json:"database_after"`
	WALBefore           ResetTimestampObservation `json:"wal_before"`
	WALAfter            ResetTimestampObservation `json:"wal_after"`
	RawSource           SourceEvidence            `json:"raw_source"`
	Operations          []ResetOperation          `json:"operations"`
	Status              string                    `json:"status"`
	Reasons             []string                  `json:"reasons"`
	Digest              string                    `json:"digest"`
}

type StatisticsResetInput struct {
	ProtocolDigest      string
	RunID               string
	Trial               int
	CapturedAt          string
	PostgresServerMajor string
	Policy              string
	Boundary            string
	BoundaryWindow      BoundaryWindow
	DatabaseBefore      ResetTimestampObservation
	DatabaseAfter       ResetTimestampObservation
	WALBefore           ResetTimestampObservation
	WALAfter            ResetTimestampObservation
	RawSource           SourceEvidence
	Operations          []ResetOperation
}

type OverheadSample struct {
	Sequence    int    `json:"sequence"`
	ScheduledAt string `json:"scheduled_at"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
	DurationNS  int64  `json:"duration_ns"`
	Status      string `json:"status"`
}

type CollectorOverhead struct {
	SchemaVersion       string           `json:"schema_version"`
	ArtifactType        string           `json:"artifact_type"`
	RunID               string           `json:"run_id"`
	ProtocolDigest      string           `json:"protocol_digest"`
	Trial               int              `json:"trial"`
	CapturedAt          string           `json:"captured_at"`
	Assurance           Assurance        `json:"assurance"`
	Collector           string           `json:"collector"`
	TimingSource        string           `json:"timing_source"`
	CalibrationWindow   BoundaryWindow   `json:"calibration_window"`
	RawSource           SourceEvidence   `json:"raw_source"`
	Mode                string           `json:"mode"`
	IntervalNS          int64            `json:"interval_ns"`
	RequiredSamples     int              `json:"required_samples"`
	MaxDutyCyclePct     *float64         `json:"max_duty_cycle_pct,omitempty"`
	Samples             []OverheadSample `json:"samples"`
	ObservedMeanDutyPct float64          `json:"observed_mean_duty_pct"`
	ObservedMaxDutyPct  float64          `json:"observed_max_duty_pct"`
	Status              string           `json:"status"`
	Reasons             []string         `json:"reasons"`
	Digest              string           `json:"digest"`
}

type CollectorOverheadInput struct {
	ProtocolDigest    string
	RunID             string
	Trial             int
	CapturedAt        string
	CalibrationWindow BoundaryWindow
	RawSource         SourceEvidence
	Mode              string
	IntervalNS        int64
	RequiredSamples   int
	MaxDutyCyclePct   *float64
	Samples           []OverheadSample
}

type ResourceBudget struct {
	SchemaVersion             string         `json:"schema_version"`
	ArtifactType              string         `json:"artifact_type"`
	RunID                     string         `json:"run_id"`
	ProtocolDigest            string         `json:"protocol_digest"`
	Trial                     int            `json:"trial"`
	CapturedAt                string         `json:"captured_at"`
	Assurance                 Assurance      `json:"assurance"`
	Mode                      string         `json:"mode"`
	Scope                     string         `json:"scope"`
	Provider                  string         `json:"provider"`
	ProviderConstraints       []string       `json:"provider_constraints"`
	InspectSource             string         `json:"inspect_source"`
	EnforcementWindow         BoundaryWindow `json:"enforcement_window"`
	RawSource                 SourceEvidence `json:"raw_source"`
	CPUMillicores             *int           `json:"cpu_millicores,omitempty"`
	MemoryMiB                 *int           `json:"memory_mib,omitempty"`
	ExpectedDockerNanoCPUs    *int64         `json:"expected_docker_nano_cpus,omitempty"`
	ExpectedDockerMemoryBytes *int64         `json:"expected_docker_memory_bytes,omitempty"`
	ObservedDockerNanoCPUs    *int64         `json:"observed_docker_nano_cpus,omitempty"`
	ObservedDockerMemoryBytes *int64         `json:"observed_docker_memory_bytes,omitempty"`
	CgroupVersion             string         `json:"cgroup_version"`
	PostgresContainerIDDigest string         `json:"postgres_container_id_digest"`
	PgbenchContainerIDDigest  string         `json:"pgbench_container_id_digest"`
	Status                    string         `json:"status"`
	Reasons                   []string       `json:"reasons"`
	Digest                    string         `json:"digest"`
}

type ResourceBudgetInput struct {
	ProtocolDigest            string
	RunID                     string
	Trial                     int
	CapturedAt                string
	EnforcementWindow         BoundaryWindow
	RawSource                 SourceEvidence
	Mode                      string
	Scope                     string
	Provider                  string
	ProviderConstraints       []string
	CPUMillicores             *int
	MemoryMiB                 *int
	ObservedDockerNanoCPUs    *int64
	ObservedDockerMemoryBytes *int64
	CgroupVersion             string
	PostgresContainerIDDigest string
	PgbenchContainerIDDigest  string
}

type ResourceBudgetSource struct {
	Mode                      string `json:"mode"`
	ObservedDockerNanoCPUs    *int64 `json:"observed_docker_nano_cpus,omitempty"`
	ObservedDockerMemoryBytes *int64 `json:"observed_docker_memory_bytes,omitempty"`
	CgroupVersion             string `json:"cgroup_version,omitempty"`
	PostgresContainerIDDigest string `json:"postgres_container_id_digest,omitempty"`
	PgbenchContainerIDDigest  string `json:"pgbench_container_id_digest,omitempty"`
}
