// Package benchmarkimport implements a deliberately separate, descriptive-only
// artifact contract for results produced by benchmark drivers that
// pgworkbench does not execute. Imported artifacts are not benchmark series and
// cannot enter the pgbench comparison or counterbalanced A/B decision paths.
package benchmarkimport

const (
	SchemaVersion        = "pgworkbench.benchmark-import/v1"
	ArtifactType         = "pgworkbench.benchmark-import"
	ContractVersion      = "1.1.0"
	MappingSchemaVersion = "pgworkbench.benchmark-import-mapping/v1"
	MappingArtifactType  = "pgworkbench.benchmark-import-mapping"

	AdapterHammerDB6 = "hammerdb6"
	// AdapterHammerDB6Report parses the pinned HammerDB v6.0
	// hammerdb-job-report-v1 document directly, without an operator mapping.
	AdapterHammerDB6Report = "hammerdb6report"
	AdapterSysbench1       = "sysbench1"
	AdapterBenchBase       = "benchbase"
	// AdapterBenchBase33c0047 parses the exact ResultWriter summary layout at
	// commit 33c00473807ebd49304d114a6d769d2d2b2bbb34.
	AdapterBenchBase33c0047 = "benchbase33c0047"

	DriverHammerDB  = "hammerdb"
	DriverSysbench  = "sysbench"
	DriverBenchBase = "benchbase"

	HammerDBSourceFormat         = "hammerdb6-saved-job-report-json"
	HammerDB6ReportSourceFormat  = "hammerdb-job-report-v1"
	SysbenchSourceFormat         = "sysbench-1.0-console-summary"
	BenchBaseSourceFormat        = "benchbase-structured-result-json-explicit-mapping"
	BenchBase33c0047SourceFormat = "benchbase-summary-json@33c0047"

	SysbenchParserVersion         = "sysbench-console-summary/v1.1"
	MappingParserVersion          = "explicit-json-pointer-mapping/v1"
	HammerDB6ReportParserVersion  = "hammerdb6-job-report/v1"
	BenchBase33c0047ParserVersion = "benchbase-summary/33c0047-v1"

	HammerDB6Commit        = "d33f879aec858063edd17aa2daa46db03abb2bae"
	BenchBase33c0047Commit = "33c00473807ebd49304d114a6d769d2d2b2bbb34"

	ClassificationImported = "descriptive-imported"
	AnalysisDesignImported = "offline-import"
	StatusImported         = "imported"
	ConclusionDescriptive  = "descriptive"

	RawSourceFile = "raw/source"
	MappingFile   = "raw/mapping.json"
	ResultFile    = "result.json"
)

type PrimaryMetric struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Direction string  `json:"direction"`
	Basis     string  `json:"basis"`
}

type ErrorSummary struct {
	Total    uint64   `json:"total"`
	Messages []string `json:"messages"`
	// Complete is false when the retained upstream document has no exhaustive
	// error channel. A zero total with Complete=false is not a zero-error claim.
	Complete bool `json:"complete"`
}

type Timing struct {
	Basis          string  `json:"basis"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	StartedAt      string  `json:"started_at,omitempty"`
	FinishedAt     string  `json:"finished_at,omitempty"`
}

type FileEvidence struct {
	File      string `json:"file"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
}

type Assurance struct {
	EvidenceOrigin                string `json:"evidence_origin"`
	NormalizationOrigin           string `json:"normalization_origin"`
	VerificationScope             string `json:"verification_scope"`
	TPCComplianceClaim            bool   `json:"tpc_compliance_claim"`
	CrossSystemComparisonEligible bool   `json:"cross_system_comparison_eligible"`
}

// Artifact intentionally has no benchmarkrun.Series fields. Its eligibility
// flags are constants enforced by Verify, not mutable operator assertions.
type Artifact struct {
	SchemaVersion         string        `json:"schema_version"`
	ArtifactType          string        `json:"artifact_type"`
	ContractVersion       string        `json:"contract_version"`
	Classification        string        `json:"classification"`
	AnalysisDesign        string        `json:"analysis_design"`
	Status                string        `json:"status"`
	Conclusion            string        `json:"conclusion"`
	DecisionEligible      bool          `json:"decision_eligible"`
	PGbenchSeriesEligible bool          `json:"pgbench_series_eligible"`
	Driver                string        `json:"driver"`
	DriverVersion         string        `json:"driver_version"`
	DriverCommit          string        `json:"driver_commit,omitempty"`
	Workload              string        `json:"workload"`
	SourceFormat          string        `json:"source_format"`
	ParserVersion         string        `json:"parser_version"`
	PrimaryMetric         PrimaryMetric `json:"primary_metric"`
	Errors                ErrorSummary  `json:"errors"`
	Timing                Timing        `json:"timing"`
	RawInput              FileEvidence  `json:"raw_input"`
	MappingInput          *FileEvidence `json:"mapping_input,omitempty"`
	Assurance             Assurance     `json:"assurance"`
	Digest                string        `json:"digest"`
	ArtifactDir           string        `json:"-"`
}

type PrimaryMetricMapping struct {
	Name         string `json:"name"`
	ValuePointer string `json:"value_pointer"`
	Unit         string `json:"unit"`
	Direction    string `json:"direction"`
}

type ErrorMapping struct {
	TotalPointer    string `json:"total_pointer"`
	MessagesPointer string `json:"messages_pointer"`
}

type TimingMapping struct {
	ElapsedSecondsPointer string `json:"elapsed_seconds_pointer"`
	StartedAtPointer      string `json:"started_at_pointer,omitempty"`
	FinishedAtPointer     string `json:"finished_at_pointer,omitempty"`
}

// Mapping is an operator-supplied semantic sidecar whose RFC 6901 JSON
// Pointers bind every normalized value to the retained structured source. It
// is required because pgworkbench does not claim a stable upstream layout.
type Mapping struct {
	SchemaVersion        string               `json:"schema_version"`
	ArtifactType         string               `json:"artifact_type"`
	Driver               string               `json:"driver"`
	DriverVersionPointer string               `json:"driver_version_pointer"`
	WorkloadPointer      string               `json:"workload_pointer"`
	SourceFormat         string               `json:"source_format"`
	PrimaryMetric        PrimaryMetricMapping `json:"primary_metric"`
	Errors               ErrorMapping         `json:"errors"`
	Timing               TimingMapping        `json:"timing"`
}

type Options struct {
	Workload    string
	MappingPath string
}

type Verification struct {
	Dir      string    `json:"dir"`
	Valid    bool      `json:"valid"`
	Issues   []string  `json:"issues"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

func (verification Verification) IsValid() bool { return len(verification.Issues) == 0 }
