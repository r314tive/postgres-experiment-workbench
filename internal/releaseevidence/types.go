// Package releaseevidence loads and independently verifies release readiness
// evidence indexes. Verification describes the consistency of the recorded
// index; a consistent no-go index is valid but is not release authorization.
package releaseevidence

const (
	SchemaVersionV1 = "pgworkbench.release-evidence-index/v1"
	SchemaVersionV2 = "pgworkbench.release-evidence-index/v2"
	SchemaVersionV3 = "pgworkbench.release-evidence-index/v3"
	// SchemaVersion remains the v1 name for source compatibility. New indexes
	// are created with SchemaVersionV3, explicit immutable lineage, and
	// persisted evidence trust metadata.
	SchemaVersion = SchemaVersionV1
	ArtifactType  = "pgworkbench.release-evidence-index"
	DecisionScope = "v1-readiness"

	RecordStatusTemplate = "template"
	RecordStatusActive   = "active"
	RecordStatusComplete = "complete"

	GateStatusOpen   = "open"
	GateStatusPassed = "passed"
	GateStatusFailed = "failed"

	ControlStatusOpen     = "open"
	ControlStatusVerified = "verified"

	ReviewStatusOpen          = "open"
	ReviewStatusAdminReviewed = "admin-reviewed"

	DecisionNoGo = "no-go"
	DecisionGo   = "go"

	// Evidence assurance is recorded inside every new v3 attachment. The only
	// currently supported pair is a valid operator assertion, but is not
	// sufficient for release authorization. A proof-backed pair must not be
	// introduced until a typed adapter independently establishes it.
	EvidenceDurabilityAsserted     = "operator-asserted-not-verified"
	EvidenceAuthenticityUnverified = "record-semantics-verified-remote-authenticity-not-verified"

	AssuranceNotApplicable         = "not-applicable"
	AssuranceLegacyUnspecified     = "legacy-unspecified-unverified"
	AssuranceOperatorAttested      = "operator-attested-not-verified"
	AssuranceAuthorizationEligible = "authorization-eligible"

	// Verification status is the aggregate state of all release gates and
	// mandatory preventive controls. Failed takes precedence over open.
	StatusOpen   = "open"
	StatusFailed = "failed"
	StatusPassed = "passed"

	maxJSONSafeInteger = int64(9007199254740991)
)

type Index struct {
	SchemaVersion      string             `json:"schema_version"`
	ArtifactType       string             `json:"artifact_type"`
	Lineage            *Lineage           `json:"lineage,omitempty"`
	RecordStatus       string             `json:"record_status"`
	CreatedAt          string             `json:"created_at"`
	Candidate          Candidate          `json:"candidate"`
	PreventiveControls PreventiveControls `json:"preventive_controls"`
	Gates              Gates              `json:"gates"`
	Decision           Decision           `json:"decision"`
}

// Lineage makes copy-on-write evidence-index revisions explicit. Revision zero
// has no predecessor; later revisions bind the exact previous index bytes.
type Lineage struct {
	Revision            int64   `json:"revision"`
	PreviousIndexDigest *string `json:"previous_index_digest,omitempty"`
}

type Candidate struct {
	Version          string       `json:"version"`
	Tag              string       `json:"tag"`
	GitCommit        string       `json:"git_commit"`
	AssetFingerprint string       `json:"asset_fingerprint"`
	ScenarioPack     ScenarioPack `json:"scenario_pack"`
}

type ScenarioPack struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type Evidence struct {
	Ref        string             `json:"ref"`
	Digest     string             `json:"digest"`
	CapturedAt string             `json:"captured_at"`
	RunID      *string            `json:"run_id,omitempty"`
	RunAttempt *int64             `json:"run_attempt,omitempty"`
	Record     *EvidenceRecord    `json:"record,omitempty"`
	Assurance  *EvidenceAssurance `json:"assurance,omitempty"`
}

// EvidenceRecord preserves the closed adapter contract that interpreted the
// exact evidence bytes. It must appear together with EvidenceAssurance in new
// v3 attachments; legacy v1/v2 evidence can have neither.
type EvidenceRecord struct {
	SchemaVersion string `json:"schema_version"`
	ArtifactType  string `json:"artifact_type"`
}

// EvidenceAssurance separates record/content verification from remote
// durability and producer authenticity. It is deliberately not a caller
// supplied boolean and the current pair is never release-authorizing.
type EvidenceAssurance struct {
	Durability   string `json:"durability"`
	Authenticity string `json:"authenticity"`
}

type Gate struct {
	Status   string    `json:"status"`
	Evidence *Evidence `json:"evidence,omitempty"`
	Note     *string   `json:"note,omitempty"`
}

type AdminReview struct {
	Status           string    `json:"status"`
	Reviewer         *string   `json:"reviewer,omitempty"`
	ReviewedAt       *string   `json:"reviewed_at,omitempty"`
	RulesetID        *int64    `json:"ruleset_id,omitempty"`
	RulesetUpdatedAt *string   `json:"ruleset_updated_at,omitempty"`
	Evidence         *Evidence `json:"evidence,omitempty"`
}

type TagRuleset struct {
	Status             string      `json:"status"`
	Target             string      `json:"target"`
	Enforcement        string      `json:"enforcement"`
	IncludePattern     string      `json:"include_pattern"`
	Excludes           []string    `json:"excludes"`
	CreationRestricted *bool       `json:"creation_restricted"`
	UpdateProhibited   *bool       `json:"update_prohibited"`
	DeletionProhibited *bool       `json:"deletion_prohibited"`
	APIEvidence        *Evidence   `json:"api_evidence,omitempty"`
	BypassReview       AdminReview `json:"bypass_review"`
}

type ImmutableReleases struct {
	Status      string    `json:"status"`
	Enabled     *bool     `json:"enabled"`
	APIEvidence *Evidence `json:"api_evidence,omitempty"`
}

type PreventiveControls struct {
	TagRuleset        TagRuleset        `json:"tag_ruleset"`
	ImmutableReleases ImmutableReleases `json:"immutable_releases"`
}

type Gates struct {
	SourceCompatibility              Gate `json:"source_compatibility"`
	AggregateAttempt1                Gate `json:"aggregate_attempt_1"`
	AggregateAttempt2                Gate `json:"aggregate_attempt_2"`
	DraftAssetVerification           Gate `json:"draft_asset_verification"`
	DraftCompatibility7Cells         Gate `json:"draft_compatibility_7_cells"`
	DraftExternalDrivers             Gate `json:"draft_external_drivers"`
	Publication                      Gate `json:"publication"`
	PublicAssetVerification          Gate `json:"public_asset_verification"`
	PublishedCompatibility7Cells     Gate `json:"published_compatibility_7_cells"`
	CriticalFindingReview            Gate `json:"critical_finding_review"`
	AdoptionPilot1                   Gate `json:"adoption_pilot_1"`
	AdoptionPilot2                   Gate `json:"adoption_pilot_2"`
	IndependentAuthoringReproduction Gate `json:"independent_authoring_reproduction"`
}

type Decision struct {
	Scope      string   `json:"scope"`
	Status     string   `json:"status"`
	RecordedAt string   `json:"recorded_at"`
	Reasons    []string `json:"reasons"`
}

// Verification is an independently recomputed view of an index. Readiness is
// the outcome-only legacy view; Status and Decision are the effective release
// authorization after evidence qualification. RecordedDecision preserves the
// input for explicit comparison. Gate lists, warnings, and reasons are sorted
// and derived without trusting Decision.Status or Decision.Reasons. Issues
// contains only structural or version-specific lifecycle defects; an ordinary
// open, failed, legacy-unqualified, or operator-attested outcome is not itself
// a semantic defect.
type Verification struct {
	Valid                 bool     `json:"valid"`
	Status                string   `json:"status"`
	Decision              string   `json:"decision"`
	RecordedDecision      string   `json:"recorded_decision"`
	ReadinessStatus       string   `json:"readiness_status"`
	ReadinessDecision     string   `json:"readiness_decision"`
	AssuranceStatus       string   `json:"assurance_status"`
	AuthorizationEligible bool     `json:"authorization_eligible"`
	OpenGates             []string `json:"open_gates"`
	FailedGates           []string `json:"failed_gates"`
	PassedGates           []string `json:"passed_gates"`
	UnqualifiedEvidence   []string `json:"unqualified_evidence"`
	Reasons               []string `json:"reasons"`
	Warnings              []string `json:"warnings"`
	Issues                []string `json:"issues"`
}
