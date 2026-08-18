// Package pgdrillbridge exports a deliberately narrow experiment-baseline
// provenance record for a future pgdrill consumer. It does not create a
// pgdrill configuration or make any recovery assurance claim.
package pgdrillbridge

const (
	SchemaVersion   = "pgworkbench.pgdrill-baseline/v1"
	ArtifactType    = "pgworkbench.pgdrill-baseline"
	ContractVersion = "1.0.0"
	DefaultFileName = "pgdrill-baseline.json"

	Classification  = "external-experiment-baseline-provenance"
	ProvenanceScope = "experiment-baseline-identity-only"
	Authenticity    = "unsigned-operator-recorded"

	VerificationModeRun    = "independently-verified-run"
	VerificationModeBundle = "independently-verified-complete-bundle"
	RunVerifierContract    = "pgworkbench.run-verify/v1"

	PredicateReviewStatus    = "reviewed"
	PredicateLanguage        = "postgresql-sql"
	PredicateKind            = "read-only-boolean-predicate"
	PredicateExecution       = "future-isolated-consumer-target"
	PredicateSafetyBasis     = "lexical-guard-plus-human-review-not-semantic-proof"
	ConsumerValidationStatus = "required"
)

type FileBinding struct {
	File   string `json:"file"`
	Digest string `json:"digest"`
}

type SourceVerification struct {
	Mode             string       `json:"mode"`
	VerifierContract string       `json:"verifier_contract"`
	Verified         bool         `json:"verified"`
	BundleInventory  *FileBinding `json:"bundle_inventory"`
}

type RunIdentity struct {
	ID          string      `json:"id"`
	StartedAt   string      `json:"started_at"`
	FinishedAt  string      `json:"finished_at"`
	Manifest    FileBinding `json:"manifest"`
	VerdictEnv  FileBinding `json:"verdict_env"`
	VerdictJSON FileBinding `json:"verdict_json"`
}

type ScenarioPackIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type ExperimentSpecIdentity struct {
	ID     string `json:"id"`
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type PostgresIdentity struct {
	Runtime               string `json:"runtime"`
	RuntimeOS             string `json:"runtime_os"`
	RuntimeArch           string `json:"runtime_arch"`
	ServerVersionNum      string `json:"server_version_num"`
	ServerMajor           string `json:"server_major"`
	FingerprintObservedAt string `json:"fingerprint_observed_at"`
}

type ReviewedPredicate struct {
	ReviewStatus    string `json:"review_status"`
	Language        string `json:"language"`
	Kind            string `json:"kind"`
	SQL             string `json:"sql"`
	Digest          string `json:"digest"`
	ExpectedBoolean bool   `json:"expected_boolean"`
	Execution       string `json:"execution"`
	SafetyBasis     string `json:"safety_basis"`
}

type AssuranceBoundary struct {
	Scope                    string `json:"scope"`
	Authenticity             string `json:"authenticity"`
	ConsumerValidationStatus string `json:"consumer_validation_status"`
}

type Artifact struct {
	SchemaVersion      string                 `json:"schema_version"`
	ArtifactType       string                 `json:"artifact_type"`
	ContractVersion    string                 `json:"contract_version"`
	Classification     string                 `json:"classification"`
	SourceVerification SourceVerification     `json:"source_verification"`
	Run                RunIdentity            `json:"run"`
	ScenarioPack       ScenarioPackIdentity   `json:"scenario_pack"`
	ExperimentSpec     ExperimentSpecIdentity `json:"experiment_spec"`
	Postgres           PostgresIdentity       `json:"postgres"`
	Predicate          *ReviewedPredicate     `json:"predicate"`
	AssuranceBoundary  AssuranceBoundary      `json:"assurance_boundary"`
	Digest             string                 `json:"digest"`
	ArtifactPath       string                 `json:"-"`
}

type Options struct {
	RequireBundle        bool
	ReviewedPredicateSQL string
}

type Verification struct {
	Path     string    `json:"path"`
	Valid    bool      `json:"valid"`
	Issues   []string  `json:"issues"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

func (verification Verification) IsValid() bool { return len(verification.Issues) == 0 }
