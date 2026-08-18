package releaseevidence

import (
	"fmt"
	"reflect"
	"sort"
)

const (
	CompatibilityVerificationSchema = "pgworkbench.release-compatibility-verification/v1"
	CompatibilityVerificationType   = "pgworkbench.release-compatibility-verification"
	CompatibilitySourceAdapter      = "pgworkbench.release-compatibility/source/v1"
	CompatibilityDraftAdapter       = "pgworkbench.release-compatibility/draft/v1"
	CompatibilityPublishedAdapter   = "pgworkbench.release-compatibility/published/v1"
	AggregateVerificationSchema     = "pgworkbench.release-aggregate-verification/v1"
	AggregateVerificationType       = "pgworkbench.release-aggregate-verification"
	AggregateAttempt1Adapter        = "pgworkbench.release-aggregate/attempt-1/v1"
	AggregateAttempt2Adapter        = "pgworkbench.release-aggregate/attempt-2/v1"

	qualificationSealSourceDraftJob = "seal-source-draft-and-aggregate-evidence"
	qualificationSealPublishedJob   = "seal-published-compatibility"
)

// QualificationArtifact is the immutable identity returned by the Actions
// artifact API. It deliberately does not claim that Actions retains the bytes.
type QualificationArtifact struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// CompatibilityVerification seals all seven fixed release cells only after a
// candidate-bound draft/public asset observation exists. This is stronger than
// a reusable workflow conclusion, but remains non-authorizing local evidence.
type CompatibilityVerification struct {
	SchemaVersion     string                         `json:"schema_version"`
	ArtifactType      string                         `json:"artifact_type"`
	QualificationMode string                         `json:"qualification_mode"`
	Candidate         Candidate                      `json:"candidate"`
	CapturedAt        string                         `json:"captured_at"`
	WorkflowRun       ReleaseVerificationWorkflowRun `json:"workflow_run"`
	AssetVerification ReleaseAssetVerification       `json:"asset_verification"`
	Cells             []CompatibilityCell            `json:"cells"`
	Assurance         QualificationAssurance         `json:"assurance"`
}

type CompatibilityCell struct {
	ID       string                `json:"id"`
	Artifact QualificationArtifact `json:"artifact"`
}

// AggregateVerification seals either independent clean-checkout aggregate
// attempt. Attempt two hash-binds the exact attempt-one record bytes.
type AggregateVerification struct {
	SchemaVersion               string                         `json:"schema_version"`
	ArtifactType                string                         `json:"artifact_type"`
	AggregateAttempt            int64                          `json:"aggregate_attempt"`
	Candidate                   Candidate                      `json:"candidate"`
	CapturedAt                  string                         `json:"captured_at"`
	WorkflowRun                 ReleaseVerificationWorkflowRun `json:"workflow_run"`
	AssetVerification           ReleaseAssetVerification       `json:"asset_verification"`
	AggregateArtifact           QualificationArtifact          `json:"aggregate_artifact"`
	PreviousAttemptRecordDigest *string                        `json:"previous_attempt_record_digest,omitempty"`
	Assurance                   QualificationAssurance         `json:"assurance"`
}

type QualificationAssurance struct {
	Purpose                     string `json:"purpose"`
	VerificationScope           string `json:"verification_scope"`
	ActionsArtifactDurable      *bool  `json:"actions_artifact_durable"`
	CandidateIdentityReverified *bool  `json:"candidate_identity_reverified"`
	PerformanceClaim            *bool  `json:"performance_claim"`
	BenchmarkComparabilityClaim *bool  `json:"benchmark_comparability_claim"`
	RecoveryClaim               *bool  `json:"recovery_claim"`
	ProductionDecisionEligible  *bool  `json:"production_decision_eligible"`
}

func validateCompatibilityVerification(record CompatibilityVerification, candidate Candidate) error {
	if record.SchemaVersion != CompatibilityVerificationSchema || record.ArtifactType != CompatibilityVerificationType {
		return fmt.Errorf("unsupported compatibility verification type or schema")
	}
	if !oneOf(record.QualificationMode, "source", releaseQualificationDraft, releaseQualificationPublished) || record.Candidate != candidate {
		return fmt.Errorf("compatibility verification mode or candidate is invalid")
	}
	if !canonicalTimestampPattern.MatchString(record.CapturedAt) || !validDateTime(record.CapturedAt) {
		return fmt.Errorf("captured_at must be canonical UTC RFC3339 seconds")
	}
	wantJob := qualificationSealJob(record.QualificationMode)
	if err := validateQualificationWorkflowRun(record.WorkflowRun, candidate, wantJob); err != nil {
		return err
	}
	if err := validateReleaseAssetVerification(record.AssetVerification, candidate); err != nil {
		return fmt.Errorf("asset_verification: %w", err)
	}
	if record.AssetVerification.QualificationMode != releaseQualificationDraft && record.QualificationMode != releaseQualificationPublished {
		return fmt.Errorf("source and draft compatibility must bind the draft asset verification")
	}
	if record.QualificationMode == releaseQualificationPublished && record.AssetVerification.QualificationMode != releaseQualificationPublished {
		return fmt.Errorf("published compatibility must bind the published asset verification")
	}
	if record.AssetVerification.WorkflowRun.ID != record.WorkflowRun.ID || record.AssetVerification.WorkflowRun.Attempt != record.WorkflowRun.Attempt {
		return fmt.Errorf("asset verification must come from the same workflow run and attempt")
	}
	if err := validateQualificationAssurance(record.Assurance, "seven-cell-compatibility-observation"); err != nil {
		return err
	}
	want := compatibilityCellIDs()
	if len(record.Cells) != len(want) {
		return fmt.Errorf("cells must contain the exact seven compatibility cells")
	}
	got := make([]string, len(record.Cells))
	for i, cell := range record.Cells {
		got[i] = cell.ID
		if err := validateQualificationArtifact(cell.Artifact, compatibilityArtifactName(record.QualificationMode, cell.ID, candidate, record.WorkflowRun.Attempt)); err != nil {
			return fmt.Errorf("cells[%d]: %w", i, err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("cells must be the exact ordered seven-cell compatibility set")
	}
	return nil
}

func validateAggregateVerification(record AggregateVerification, candidate Candidate, predecessor Index) error {
	if record.SchemaVersion != AggregateVerificationSchema || record.ArtifactType != AggregateVerificationType || (record.AggregateAttempt != 1 && record.AggregateAttempt != 2) || record.Candidate != candidate {
		return fmt.Errorf("aggregate verification type, attempt, or candidate is invalid")
	}
	if !canonicalTimestampPattern.MatchString(record.CapturedAt) || !validDateTime(record.CapturedAt) {
		return fmt.Errorf("captured_at must be canonical UTC RFC3339 seconds")
	}
	if err := validateQualificationWorkflowRun(record.WorkflowRun, candidate, qualificationSealSourceDraftJob); err != nil {
		return err
	}
	if err := validateReleaseAssetVerification(record.AssetVerification, candidate); err != nil {
		return fmt.Errorf("asset_verification: %w", err)
	}
	if record.AssetVerification.QualificationMode != releaseQualificationDraft || record.AssetVerification.WorkflowRun.ID != record.WorkflowRun.ID || record.AssetVerification.WorkflowRun.Attempt != record.WorkflowRun.Attempt {
		return fmt.Errorf("aggregate verification must bind same-run draft asset verification")
	}
	if err := validateQualificationArtifact(record.AggregateArtifact, fmt.Sprintf("aggregate-%d-%s-%d", record.AggregateAttempt, candidate.GitCommit, record.WorkflowRun.Attempt)); err != nil {
		return fmt.Errorf("aggregate_artifact: %w", err)
	}
	if err := validateQualificationAssurance(record.Assurance, "clean-checkout-aggregate-observation"); err != nil {
		return err
	}
	if record.AggregateAttempt == 1 {
		if record.PreviousAttemptRecordDigest != nil {
			return fmt.Errorf("attempt one must not reference a predecessor record")
		}
	} else {
		if record.PreviousAttemptRecordDigest == nil || !validDigest(*record.PreviousAttemptRecordDigest) {
			return fmt.Errorf("attempt two must bind a sha256 attempt-one record digest")
		}
		previous := predecessor.Gates.AggregateAttempt1.Evidence
		if previous == nil || previous.Record == nil || previous.Record.Adapter != AggregateAttempt1Adapter || previous.Digest != *record.PreviousAttemptRecordDigest {
			return fmt.Errorf("attempt two must bind the attached attempt-one record digest")
		}
	}
	return nil
}

func validateQualificationWorkflowRun(run ReleaseVerificationWorkflowRun, candidate Candidate, job string) error {
	if !validUnsignedDecimal(run.ID) || run.Attempt < 1 || run.Attempt > maxJSONSafeInteger || run.HeadSHA != candidate.GitCommit || run.Repository != releaseWorkflowRepository || run.Workflow != releaseWorkflowName || run.Job != job || run.Ref != "refs/tags/"+candidate.Tag {
		return fmt.Errorf("workflow_run does not match the exact release sealing workflow, job, ref, repository, and candidate")
	}
	return nil
}

func validateQualificationArtifact(a QualificationArtifact, wantName string) error {
	if !validUnsignedDecimal(a.ID) || a.Name != wantName || !validDigest(a.Digest) {
		return fmt.Errorf("artifact must have decimal id, exact name, and lowercase sha256 digest")
	}
	return nil
}

func validateQualificationAssurance(a QualificationAssurance, purpose string) error {
	if a.Purpose != purpose || a.VerificationScope != "workflow-local-actions-artifact-identity" || !requiredBool(a.ActionsArtifactDurable, false) || !requiredBool(a.CandidateIdentityReverified, true) || !requiredBool(a.PerformanceClaim, false) || !requiredBool(a.BenchmarkComparabilityClaim, false) || !requiredBool(a.RecoveryClaim, false) || !requiredBool(a.ProductionDecisionEligible, false) {
		return fmt.Errorf("assurance does not match the bounded qualification contract")
	}
	return nil
}

func qualificationSealJob(mode string) string {
	switch mode {
	case "source", releaseQualificationDraft:
		return qualificationSealSourceDraftJob
	default:
		return qualificationSealPublishedJob
	}
}

func compatibilityCellIDs() []string {
	return []string{"docker-linux-amd64-pg15-to-pg16-multi-version-upgrade", "docker-linux-amd64-pg16-logical-replication", "docker-linux-amd64-pg16-pgbouncer", "docker-linux-amd64-pg16-primary-replica", "docker-linux-amd64-pg16-single", "native-darwin-arm64-pg16-single", "native-linux-amd64-pg16-single"}
}

func compatibilityArtifactName(mode, cell string, candidate Candidate, attempt int64) string {
	suffix := cell
	if cell == "native-darwin-arm64-pg16-single" {
		suffix = "native-darwin-arm64-pg16"
	}
	if cell == "native-linux-amd64-pg16-single" {
		suffix = "native-linux-amd64-pg16"
	}
	return fmt.Sprintf("compatibility-%s-%s-%s-%d", mode, suffix, candidate.GitCommit, attempt)
}

func sortedCompatibilityCells(cells []CompatibilityCell) []CompatibilityCell {
	out := append([]CompatibilityCell(nil), cells...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
