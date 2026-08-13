package benchmarkqualify

import "time"

const (
	SchemaVersion    = "pgworkbench.benchmark-host-qualification/v1"
	ArtifactType     = "pgworkbench.benchmark-host-qualification"
	CollectorVersion = "1"

	AvailabilityObserved    = "observed"
	AvailabilityUnavailable = "unavailable"

	VerdictQualified   = "qualified"
	VerdictUnqualified = "unqualified"

	CheckPassed = "passed"
	CheckFailed = "failed"

	EvidenceOriginOperatorRecorded = "operator-recorded"
	DigestPurposeIntegrityOnly     = "content-integrity-only"
	VerificationScopeRecordedOnly  = "structure-and-recorded-content-only"
)

// Assurance deliberately describes a bounded, unsigned observation. A digest
// protects content integrity only; it is not a signature, host identity, or
// remote/current-state attestation.
type Assurance struct {
	EvidenceOrigin    string `json:"evidence_origin"`
	Signed            bool   `json:"signed"`
	DigestPurpose     string `json:"digest_purpose"`
	VerificationScope string `json:"verification_scope"`
}

type StringObservation struct {
	Availability string `json:"availability"`
	Value        string `json:"value,omitempty"`
	Source       string `json:"source"`
}

type StringListObservation struct {
	Availability string   `json:"availability"`
	Values       []string `json:"values,omitempty"`
	Source       string   `json:"source"`
}

type UintObservation struct {
	Availability string  `json:"availability"`
	Value        *uint64 `json:"value,omitempty"`
	Source       string  `json:"source"`
}

type NumberObservation struct {
	Availability string   `json:"availability"`
	Value        *float64 `json:"value,omitempty"`
	Source       string   `json:"source"`
}

type PlatformSnapshot struct {
	OS           StringObservation `json:"os"`
	Architecture StringObservation `json:"architecture"`
	Kernel       StringObservation `json:"kernel"`
}

type CPUSnapshot struct {
	Model       StringObservation `json:"model"`
	LogicalCPUs UintObservation   `json:"logical_cpus"`
}

type CapacitySnapshot struct {
	TotalBytes     UintObservation   `json:"total_bytes"`
	AvailableBytes UintObservation   `json:"available_bytes"`
	AvailablePct   NumberObservation `json:"available_pct"`
}

type StorageSnapshot struct {
	Label          string            `json:"label"`
	Filesystem     StringObservation `json:"filesystem"`
	TotalBytes     UintObservation   `json:"total_bytes"`
	AvailableBytes UintObservation   `json:"available_bytes"`
	AvailablePct   NumberObservation `json:"available_pct"`
}

type ClockSnapshot struct {
	Clocksource StringObservation `json:"clocksource"`
}

type PowerSnapshot struct {
	Governors StringListObservation `json:"governors"`
}

type ThermalSnapshot struct {
	MaxCelsius NumberObservation `json:"max_celsius"`
}

type ClientSnapshot struct {
	Placement StringObservation `json:"placement"`
}

type InterferenceSnapshot struct {
	Load1             NumberObservation `json:"load_1m"`
	Load1PerCPU       NumberObservation `json:"load_1m_per_cpu"`
	RunnableProcesses UintObservation   `json:"runnable_processes"`
	ProcessCount      UintObservation   `json:"process_count"`
}

type HeadroomSnapshot struct {
	LoadCapacityPct NumberObservation `json:"load_capacity_pct"`
	MemoryPct       NumberObservation `json:"memory_available_pct"`
	StoragePct      NumberObservation `json:"storage_available_pct"`
}

type Snapshot struct {
	Platform     PlatformSnapshot     `json:"platform"`
	CPU          CPUSnapshot          `json:"cpu"`
	Memory       CapacitySnapshot     `json:"memory"`
	Storage      StorageSnapshot      `json:"storage"`
	Clock        ClockSnapshot        `json:"clock"`
	Power        PowerSnapshot        `json:"power"`
	Thermal      ThermalSnapshot      `json:"thermal"`
	Client       ClientSnapshot       `json:"client"`
	Interference InterferenceSnapshot `json:"interference"`
	Headroom     HeadroomSnapshot     `json:"headroom"`
}

// Policy contains only explicit operator-selected gates. Strict must be true
// and at least one gate must be present before a qualified verdict is possible.
type Policy struct {
	Strict                  bool     `json:"strict"`
	MinLogicalCPUs          *uint64  `json:"min_logical_cpus,omitempty"`
	MinMemoryAvailablePct   *float64 `json:"min_memory_available_pct,omitempty"`
	MinStorageAvailablePct  *float64 `json:"min_storage_available_pct,omitempty"`
	MaxLoad1PerCPU          *float64 `json:"max_load_1m_per_cpu,omitempty"`
	RequiredClocksource     string   `json:"required_clocksource,omitempty"`
	RequiredGovernor        string   `json:"required_governor,omitempty"`
	MaxTemperatureCelsius   *float64 `json:"max_temperature_celsius,omitempty"`
	RequiredClientPlacement string   `json:"required_client_placement,omitempty"`
}

type Check struct {
	Gate        string `json:"gate"`
	Status      string `json:"status"`
	Observation string `json:"observation"`
	Requirement string `json:"requirement"`
}

type Artifact struct {
	SchemaVersion    string    `json:"schema_version"`
	ArtifactType     string    `json:"artifact_type"`
	CollectorVersion string    `json:"collector_version"`
	RecordedAt       string    `json:"recorded_at"`
	Assurance        Assurance `json:"assurance"`
	Snapshot         Snapshot  `json:"snapshot"`
	Policy           Policy    `json:"policy"`
	Checks           []Check   `json:"checks"`
	Verdict          string    `json:"verdict"`
	Reasons          []string  `json:"reasons"`
	SnapshotDigest   string    `json:"snapshot_digest"`
	Digest           string    `json:"digest"`
}

type InspectOptions struct {
	RecordedAt      time.Time
	StoragePath     string
	StorageLabel    string
	ClientPlacement string
	Policy          Policy
}

type Verification struct {
	Valid           bool      `json:"valid"`
	RecordedVerdict string    `json:"recorded_verdict,omitempty"`
	Assurance       Assurance `json:"assurance"`
	Issues          []string  `json:"issues"`
}
