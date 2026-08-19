package releaseevidence

import (
	"fmt"
	"regexp"
)

const (
	PreventiveControlsVerificationSchema = "pgworkbench.release-preventive-controls-verification/v1"
	PreventiveControlsVerificationType   = "pgworkbench.release-preventive-controls-verification"

	PreventiveControlsTagRulesetAdapter          = "pgworkbench.release-preventive-controls/tag-ruleset/v1"
	PreventiveControlsBypassReviewAdapter        = "pgworkbench.release-preventive-controls/bypass-review/v1"
	PreventiveControlsImmutableReleasesAdapter   = "pgworkbench.release-preventive-controls/immutable-releases/v1"
	PreventiveControlsSealJob                    = "seal-preventive-controls"
	preventiveControlsAssurancePurpose           = "prepublication-preventive-controls-observation"
	preventiveControlsAssuranceVerificationScope = "workflow-local-github-api-and-source-binding"
)

var preventiveControlsReviewerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+:-]{0,127}$`)

// PreventiveControlsVerification binds one pre-publication observation of the
// repository's release controls to a candidate and a same-run draft asset
// verification. It deliberately records facts only: it has no caller-selected
// status, decision, or authorization field.
type PreventiveControlsVerification struct {
	SchemaVersion          string                                  `json:"schema_version"`
	ArtifactType           string                                  `json:"artifact_type"`
	Candidate              Candidate                               `json:"candidate"`
	CapturedAt             string                                  `json:"captured_at"`
	WorkflowRun            ReleaseVerificationWorkflowRun          `json:"workflow_run"`
	DraftAssetVerification ReleaseAssetVerification                `json:"draft_asset_verification"`
	Source                 PreventiveControlsVerificationSource    `json:"source"`
	TagRuleset             PreventiveControlsTagRuleset            `json:"tag_ruleset"`
	BypassReview           PreventiveControlsBypassReview          `json:"bypass_review"`
	ImmutableReleases      PreventiveControlsImmutableReleases     `json:"immutable_releases"`
	Assurance              PreventiveControlsVerificationAssurance `json:"assurance"`
}

type PreventiveControlsVerificationSource struct {
	ControlsArtifact           QualificationArtifact `json:"controls_artifact"`
	RepositoryControlsDigest   string                `json:"repository_controls_digest"`
	TagRulesetAPIDigest        string                `json:"tag_ruleset_api_digest"`
	ImmutableReleasesAPIDigest string                `json:"immutable_releases_api_digest"`
}

type PreventiveControlsTagRuleset struct {
	ID                 int64    `json:"id"`
	UpdatedAt          string   `json:"updated_at"`
	Target             string   `json:"target"`
	Enforcement        string   `json:"enforcement"`
	IncludePattern     string   `json:"include_pattern"`
	Excludes           []string `json:"excludes"`
	CreationRestricted *bool    `json:"creation_restricted"`
	UpdateProhibited   *bool    `json:"update_prohibited"`
	DeletionProhibited *bool    `json:"deletion_prohibited"`
}

type PreventiveControlsBypassReview struct {
	Reviewer         string `json:"reviewer"`
	ReviewedAt       string `json:"reviewed_at"`
	RulesetID        int64  `json:"ruleset_id"`
	RulesetUpdatedAt string `json:"ruleset_updated_at"`
	EvidenceRef      string `json:"evidence_ref"`
	EvidenceDigest   string `json:"evidence_digest"`
}

type PreventiveControlsImmutableReleases struct {
	Enabled         *bool `json:"enabled"`
	EnforcedByOwner *bool `json:"enforced_by_owner"`
}

type PreventiveControlsVerificationAssurance struct {
	Purpose                         string `json:"purpose"`
	VerificationScope               string `json:"verification_scope"`
	ActionsArtifactDurable          *bool  `json:"actions_artifact_durable"`
	CandidateIdentityReverified     *bool  `json:"candidate_identity_reverified"`
	BypassReviewRemoteObjectFetched *bool  `json:"bypass_review_remote_object_fetched"`
	BypassReviewSignatureVerified   *bool  `json:"bypass_review_signature_verified"`
	PerformanceClaim                *bool  `json:"performance_claim"`
	BenchmarkComparabilityClaim     *bool  `json:"benchmark_comparability_claim"`
	RecoveryClaim                   *bool  `json:"recovery_claim"`
	ProductionDecisionEligible      *bool  `json:"production_decision_eligible"`
}

// ValidatePreventiveControlsVerification verifies all cross-field and
// candidate-bound semantics that JSON Schema cannot express.
func ValidatePreventiveControlsVerification(record PreventiveControlsVerification, candidate Candidate) error {
	if record.SchemaVersion != PreventiveControlsVerificationSchema || record.ArtifactType != PreventiveControlsVerificationType {
		return fmt.Errorf("unsupported preventive-controls verification type or schema")
	}
	if record.Candidate != candidate {
		return fmt.Errorf("preventive-controls verification candidate does not match the predecessor index candidate")
	}
	if !canonicalTimestampPattern.MatchString(record.CapturedAt) || !validDateTime(record.CapturedAt) {
		return fmt.Errorf("captured_at must be a canonical UTC RFC3339 date-time with second precision")
	}
	if err := validateReleaseVerificationWorkflowRun(record.WorkflowRun, candidate, PreventiveControlsSealJob); err != nil {
		return err
	}
	if err := validateReleaseAssetVerification(record.DraftAssetVerification, candidate); err != nil {
		return fmt.Errorf("draft_asset_verification: %w", err)
	}
	if record.DraftAssetVerification.QualificationMode != releaseQualificationDraft {
		return fmt.Errorf("draft_asset_verification must use draft qualification mode")
	}
	if record.DraftAssetVerification.WorkflowRun.ID != record.WorkflowRun.ID ||
		record.DraftAssetVerification.WorkflowRun.Attempt != record.WorkflowRun.Attempt {
		return fmt.Errorf("draft_asset_verification must come from the same workflow run and attempt")
	}
	recordCapturedAt, recordTimeOK := parseDateTime(record.CapturedAt)
	draftCapturedAt, draftTimeOK := parseDateTime(record.DraftAssetVerification.CapturedAt)
	if !recordTimeOK || !draftTimeOK || recordCapturedAt.Before(draftCapturedAt) {
		return fmt.Errorf("captured_at must not precede draft_asset_verification.captured_at")
	}

	wantArtifactName := fmt.Sprintf(
		"release-controls-%s-%s-%d",
		candidate.Tag,
		candidate.GitCommit,
		record.WorkflowRun.Attempt,
	)
	if err := validateQualificationArtifact(record.Source.ControlsArtifact, wantArtifactName); err != nil {
		return fmt.Errorf("source.controls_artifact: %w", err)
	}
	for _, digest := range []struct {
		name  string
		value string
	}{
		{name: "source.repository_controls_digest", value: record.Source.RepositoryControlsDigest},
		{name: "source.tag_ruleset_api_digest", value: record.Source.TagRulesetAPIDigest},
		{name: "source.immutable_releases_api_digest", value: record.Source.ImmutableReleasesAPIDigest},
	} {
		if !validDigest(digest.value) {
			return fmt.Errorf("%s must be a lowercase sha256 digest", digest.name)
		}
	}

	ruleset := record.TagRuleset
	if ruleset.ID < 1 || ruleset.ID > maxJSONSafeInteger ||
		!canonicalTimestampPattern.MatchString(ruleset.UpdatedAt) || !validDateTime(ruleset.UpdatedAt) ||
		ruleset.Target != "tag" || ruleset.Enforcement != "active" ||
		ruleset.IncludePattern != "refs/tags/v*" || ruleset.Excludes == nil || len(ruleset.Excludes) != 0 ||
		!requiredBool(ruleset.CreationRestricted, true) ||
		!requiredBool(ruleset.UpdateProhibited, true) ||
		!requiredBool(ruleset.DeletionProhibited, true) {
		return fmt.Errorf("tag_ruleset does not match the fixed preventive-control contract")
	}

	review := record.BypassReview
	if !preventiveControlsReviewerPattern.MatchString(review.Reviewer) {
		return fmt.Errorf("bypass_review.reviewer must be a restricted non-whitespace identifier of 1 to 128 characters")
	}
	if !canonicalTimestampPattern.MatchString(review.ReviewedAt) || !validDateTime(review.ReviewedAt) {
		return fmt.Errorf("bypass_review.reviewed_at must be a canonical UTC RFC3339 date-time with second precision")
	}
	if review.RulesetID != ruleset.ID || review.RulesetUpdatedAt != ruleset.UpdatedAt {
		return fmt.Errorf("bypass_review must bind the exact observed ruleset id and updated_at")
	}
	if !validLength(review.EvidenceRef, 1, 2048) || !validDurableRef(review.EvidenceRef) || !validDigest(review.EvidenceDigest) {
		return fmt.Errorf("bypass_review evidence must have a durable reference and lowercase sha256 digest")
	}
	reviewedAt, reviewedTimeOK := parseDateTime(review.ReviewedAt)
	rulesetUpdatedAt, rulesetTimeOK := parseDateTime(ruleset.UpdatedAt)
	if !reviewedTimeOK || !rulesetTimeOK || reviewedAt.Before(rulesetUpdatedAt) {
		return fmt.Errorf("bypass_review.reviewed_at must not precede tag_ruleset.updated_at")
	}
	if recordCapturedAt.Before(rulesetUpdatedAt) || recordCapturedAt.Before(reviewedAt) {
		return fmt.Errorf("observed ruleset and bypass review timestamps must not follow captured_at")
	}

	if !requiredBool(record.ImmutableReleases.Enabled, true) || record.ImmutableReleases.EnforcedByOwner == nil {
		return fmt.Errorf("immutable_releases must be enabled and report owner enforcement")
	}

	assurance := record.Assurance
	if assurance.Purpose != preventiveControlsAssurancePurpose ||
		assurance.VerificationScope != preventiveControlsAssuranceVerificationScope ||
		!requiredBool(assurance.ActionsArtifactDurable, false) ||
		!requiredBool(assurance.CandidateIdentityReverified, true) ||
		!requiredBool(assurance.BypassReviewRemoteObjectFetched, false) ||
		!requiredBool(assurance.BypassReviewSignatureVerified, false) ||
		!requiredBool(assurance.PerformanceClaim, false) ||
		!requiredBool(assurance.BenchmarkComparabilityClaim, false) ||
		!requiredBool(assurance.RecoveryClaim, false) ||
		!requiredBool(assurance.ProductionDecisionEligible, false) {
		return fmt.Errorf("assurance does not match the bounded preventive-controls verification contract")
	}
	return nil
}
