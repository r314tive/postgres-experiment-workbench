package releaseevidence

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/r314tive/postgres-experiment-workbench/internal/releaseassets"
)

const (
	ReleaseAssetVerificationSchema = "pgworkbench.release-asset-verification/v1"
	ReleaseAssetVerificationType   = "pgworkbench.release-asset-verification"
	ReleaseAssetDraftAdapter       = "pgworkbench.release-asset-verification/draft/v1"
	ReleaseAssetPublishedAdapter   = "pgworkbench.release-asset-verification/published/v1"
	ReleasePublicationSchema       = "pgworkbench.release-publication-verification/v1"
	ReleasePublicationType         = "pgworkbench.release-publication-verification"
	ReleasePublicationAdapter      = "pgworkbench.release-publication-verification/post-publication/v1"

	releaseQualificationDraft     = "draft"
	releaseQualificationPublished = "published"
	releaseWorkflowRepository     = "r314tive/postgres-experiment-workbench"
	releaseWorkflowName           = "release-snapshot"
	releaseDraftVerificationJob   = "draft-verify"
	releasePublicVerificationJob  = "public-verify"
)

// ReleaseAssetVerification is a fact-only summary emitted after a read-only
// workflow job has authenticated and inspected one complete draft or public
// release. A valid record has one derived positive meaning; it carries no
// caller-selectable gate or outcome.
type ReleaseAssetVerification struct {
	SchemaVersion       string                            `json:"schema_version"`
	ArtifactType        string                            `json:"artifact_type"`
	QualificationMode   string                            `json:"qualification_mode"`
	Candidate           Candidate                         `json:"candidate"`
	CapturedAt          string                            `json:"captured_at"`
	WorkflowRun         ReleaseVerificationWorkflowRun    `json:"workflow_run"`
	Inventory           releaseassets.Inventory           `json:"inventory"`
	ProviderObservation ReleaseAssetProviderObservation   `json:"provider_observation"`
	Source              ReleaseAssetVerificationSource    `json:"source"`
	Checks              ReleaseAssetVerificationChecks    `json:"checks"`
	Assurance           ReleaseAssetVerificationAssurance `json:"assurance"`
}

type ReleaseVerificationWorkflowRun struct {
	ID         string `json:"id"`
	Attempt    int64  `json:"attempt"`
	HeadSHA    string `json:"head_sha"`
	Repository string `json:"repository"`
	Workflow   string `json:"workflow"`
	Job        string `json:"job"`
	Ref        string `json:"ref"`
}

type ReleaseAssetProviderObservation struct {
	Tag              string `json:"tag"`
	TagTargetSHA     string `json:"tag_target_sha"`
	ReleaseState     string `json:"release_state"`
	IsDraft          *bool  `json:"is_draft"`
	IsImmutable      *bool  `json:"is_immutable,omitempty"`
	AssetCount       int64  `json:"asset_count"`
	AssetFingerprint string `json:"asset_fingerprint"`
}

type ReleaseAssetVerificationSource struct {
	AssetInventoryDigest     string  `json:"asset_inventory_digest"`
	ReleaseManifestDigest    string  `json:"release_manifest_digest"`
	ReleaseAttestationDigest *string `json:"release_attestation_digest,omitempty"`
}

type ReleaseAssetVerificationChecks struct {
	TagTarget               string `json:"tag_target"`
	ClosedAssetSet          string `json:"closed_asset_set"`
	DownloadedAssetBytes    string `json:"downloaded_asset_bytes"`
	ArchiveChecksums        string `json:"archive_checksums"`
	MetadataChecksums       string `json:"metadata_checksums"`
	ReleaseManifest         string `json:"release_manifest"`
	CandidateBinaryIdentity string `json:"candidate_binary_identity"`
	ProvenanceAttestations  string `json:"provenance_attestations"`
	SBOMAttestations        string `json:"sbom_attestations"`
	SBOMContents            string `json:"sbom_contents"`
	ImmutableRelease        string `json:"immutable_release"`
	ReleaseAttestation      string `json:"release_attestation"`
}

type ReleaseAssetVerificationAssurance struct {
	Purpose                     string `json:"purpose"`
	VerificationScope           string `json:"verification_scope"`
	ActionsArtifactDurable      *bool  `json:"actions_artifact_durable"`
	CandidateIdentityReverified *bool  `json:"candidate_identity_reverified"`
	ProviderAssetSetRecomputed  *bool  `json:"provider_asset_set_recomputed"`
	AllDownloadedBytesVerified  *bool  `json:"all_downloaded_bytes_verified"`
	PerformanceClaim            *bool  `json:"performance_claim"`
	BenchmarkComparabilityClaim *bool  `json:"benchmark_comparability_claim"`
	RecoveryClaim               *bool  `json:"recovery_claim"`
	ProductionDecisionEligible  *bool  `json:"production_decision_eligible"`
}

// ReleasePublicationVerification is emitted only by the fresh read-only
// public verifier after it observes a published immutable release. It embeds
// the complete published-asset fact record, so its adapter does not have to
// trust the success of the mutating publication job or fetch a second input.
type ReleasePublicationVerification struct {
	SchemaVersion           string                         `json:"schema_version"`
	ArtifactType            string                         `json:"artifact_type"`
	Candidate               Candidate                      `json:"candidate"`
	CapturedAt              string                         `json:"captured_at"`
	WorkflowRun             ReleaseVerificationWorkflowRun `json:"workflow_run"`
	PublicAssetVerification ReleaseAssetVerification       `json:"public_asset_verification"`
	Observation             ReleasePublicationObservation  `json:"observation"`
	Assurance               ReleasePublicationAssurance    `json:"assurance"`
}

type ReleasePublicationObservation struct {
	PostPublicationObservation  *bool  `json:"post_publication_observation"`
	MutationPerformedByVerifier *bool  `json:"mutation_performed_by_verifier"`
	DraftPublicFingerprintEqual *bool  `json:"draft_public_fingerprint_equal"`
	ReleaseState                string `json:"release_state"`
	IsDraft                     *bool  `json:"is_draft"`
	IsImmutable                 *bool  `json:"is_immutable"`
	TagTargetSHA                string `json:"tag_target_sha"`
	AssetCount                  int64  `json:"asset_count"`
	AssetFingerprint            string `json:"asset_fingerprint"`
	ReleaseAttestation          string `json:"release_attestation"`
}

type ReleasePublicationAssurance struct {
	Purpose                     string `json:"purpose"`
	VerificationScope           string `json:"verification_scope"`
	ActionsArtifactDurable      *bool  `json:"actions_artifact_durable"`
	CandidateIdentityReverified *bool  `json:"candidate_identity_reverified"`
	PerformanceClaim            *bool  `json:"performance_claim"`
	BenchmarkComparabilityClaim *bool  `json:"benchmark_comparability_claim"`
	RecoveryClaim               *bool  `json:"recovery_claim"`
	ProductionDecisionEligible  *bool  `json:"production_decision_eligible"`
}

func validateReleaseAssetVerification(record ReleaseAssetVerification, candidate Candidate) error {
	if record.SchemaVersion != ReleaseAssetVerificationSchema || record.ArtifactType != ReleaseAssetVerificationType {
		return fmt.Errorf("unsupported release-asset verification type or schema")
	}
	if !oneOf(record.QualificationMode, releaseQualificationDraft, releaseQualificationPublished) {
		return fmt.Errorf("qualification_mode = %q, want draft or published", record.QualificationMode)
	}
	if record.Candidate != candidate {
		return fmt.Errorf("release-asset verification candidate does not match the predecessor index candidate")
	}
	if !canonicalTimestampPattern.MatchString(record.CapturedAt) || !validDateTime(record.CapturedAt) {
		return fmt.Errorf("captured_at must be a canonical UTC RFC3339 date-time with second precision")
	}
	wantJob := releaseDraftVerificationJob
	if record.QualificationMode == releaseQualificationPublished {
		wantJob = releasePublicVerificationJob
	}
	if err := validateReleaseVerificationWorkflowRun(record.WorkflowRun, candidate, wantJob); err != nil {
		return err
	}

	inventoryVerification := releaseassets.Verify(record.Inventory)
	if !inventoryVerification.Valid {
		return fmt.Errorf("embedded release asset inventory is invalid: %s", joinIssues(inventoryVerification.Issues))
	}
	if record.Inventory.ReleaseState != record.QualificationMode ||
		record.Inventory.Tag != candidate.Tag ||
		record.Inventory.GitCommit != candidate.GitCommit ||
		record.Inventory.AssetFingerprint != candidate.AssetFingerprint {
		return fmt.Errorf("embedded release asset inventory does not match the qualification mode and candidate")
	}
	if !exactReleaseAssetNames(record.Inventory, candidate.Version) {
		return fmt.Errorf("embedded release asset inventory does not contain the exact fixed 16-asset set")
	}
	inventoryCapturedAt, inventoryTimeOK := parseDateTime(record.Inventory.CapturedAt)
	recordCapturedAt, recordTimeOK := parseDateTime(record.CapturedAt)
	if !inventoryTimeOK || !recordTimeOK || recordCapturedAt.Before(inventoryCapturedAt) {
		return fmt.Errorf("captured_at must not precede inventory.captured_at")
	}

	wantDraft := record.QualificationMode == releaseQualificationDraft
	observation := record.ProviderObservation
	if observation.Tag != candidate.Tag ||
		observation.TagTargetSHA != candidate.GitCommit ||
		observation.ReleaseState != record.QualificationMode ||
		!requiredBool(observation.IsDraft, wantDraft) ||
		observation.AssetCount != 16 ||
		observation.AssetFingerprint != candidate.AssetFingerprint {
		return fmt.Errorf("provider_observation does not match the candidate and qualification mode")
	}
	if wantDraft {
		if observation.IsImmutable != nil {
			return fmt.Errorf("provider_observation.is_immutable must be absent for draft qualification")
		}
	} else if !requiredBool(observation.IsImmutable, true) {
		return fmt.Errorf("provider_observation.is_immutable must be true for published qualification")
	}

	if !validDigest(record.Source.AssetInventoryDigest) || !validDigest(record.Source.ReleaseManifestDigest) {
		return fmt.Errorf("source inventory and release-manifest digests must be lowercase sha256 digests")
	}
	manifestName := fmt.Sprintf("pgworkbench-%s-release-manifest.json", candidate.Version)
	manifestDigest := inventoryAssetDigest(record.Inventory, manifestName)
	if manifestDigest == "" || manifestDigest != record.Source.ReleaseManifestDigest {
		return fmt.Errorf("source.release_manifest_digest does not match the embedded inventory manifest asset")
	}
	if wantDraft {
		if record.Source.ReleaseAttestationDigest != nil {
			return fmt.Errorf("source.release_attestation_digest must be absent for draft qualification")
		}
	} else if record.Source.ReleaseAttestationDigest == nil || !validDigest(*record.Source.ReleaseAttestationDigest) {
		return fmt.Errorf("source.release_attestation_digest must be a lowercase sha256 digest for published qualification")
	}

	wantChecks := verifiedReleaseAssetChecks("not-applicable")
	if !wantDraft {
		wantChecks = verifiedReleaseAssetChecks("verified")
	}
	if record.Checks != wantChecks {
		return fmt.Errorf("checks do not match the closed release-asset verification contract")
	}
	if record.Assurance.Purpose != "release-asset-authenticity-and-integrity" ||
		record.Assurance.VerificationScope != "workflow-local-provider-and-content" ||
		!requiredBool(record.Assurance.ActionsArtifactDurable, false) ||
		!requiredBool(record.Assurance.CandidateIdentityReverified, true) ||
		!requiredBool(record.Assurance.ProviderAssetSetRecomputed, true) ||
		!requiredBool(record.Assurance.AllDownloadedBytesVerified, true) ||
		!requiredBool(record.Assurance.PerformanceClaim, false) ||
		!requiredBool(record.Assurance.BenchmarkComparabilityClaim, false) ||
		!requiredBool(record.Assurance.RecoveryClaim, false) ||
		!requiredBool(record.Assurance.ProductionDecisionEligible, false) {
		return fmt.Errorf("assurance does not match the bounded release-asset verification contract")
	}
	return nil
}

func validateReleasePublicationVerification(record ReleasePublicationVerification, candidate Candidate) error {
	if record.SchemaVersion != ReleasePublicationSchema || record.ArtifactType != ReleasePublicationType {
		return fmt.Errorf("unsupported release-publication verification type or schema")
	}
	if record.Candidate != candidate {
		return fmt.Errorf("release-publication verification candidate does not match the predecessor index candidate")
	}
	if !canonicalTimestampPattern.MatchString(record.CapturedAt) || !validDateTime(record.CapturedAt) {
		return fmt.Errorf("captured_at must be a canonical UTC RFC3339 date-time with second precision")
	}
	if err := validateReleaseVerificationWorkflowRun(record.WorkflowRun, candidate, releasePublicVerificationJob); err != nil {
		return err
	}
	if err := validateReleaseAssetVerification(record.PublicAssetVerification, candidate); err != nil {
		return fmt.Errorf("public_asset_verification: %w", err)
	}
	if record.PublicAssetVerification.QualificationMode != releaseQualificationPublished {
		return fmt.Errorf("public_asset_verification must use published qualification mode")
	}
	if record.PublicAssetVerification.WorkflowRun != record.WorkflowRun || record.PublicAssetVerification.CapturedAt != record.CapturedAt {
		return fmt.Errorf("publication and public-asset records must have the same workflow identity and capture time")
	}
	observation := record.Observation
	if !requiredBool(observation.PostPublicationObservation, true) ||
		!requiredBool(observation.MutationPerformedByVerifier, false) ||
		!requiredBool(observation.DraftPublicFingerprintEqual, true) ||
		observation.ReleaseState != releaseQualificationPublished ||
		!requiredBool(observation.IsDraft, false) ||
		!requiredBool(observation.IsImmutable, true) ||
		observation.TagTargetSHA != candidate.GitCommit ||
		observation.AssetCount != 16 ||
		observation.AssetFingerprint != candidate.AssetFingerprint ||
		observation.ReleaseAttestation != "verified" {
		return fmt.Errorf("observation does not match the post-publication immutable-release contract")
	}
	if record.Assurance.Purpose != "post-publication-read-only-observation" ||
		record.Assurance.VerificationScope != "workflow-local-provider-and-content" ||
		!requiredBool(record.Assurance.ActionsArtifactDurable, false) ||
		!requiredBool(record.Assurance.CandidateIdentityReverified, true) ||
		!requiredBool(record.Assurance.PerformanceClaim, false) ||
		!requiredBool(record.Assurance.BenchmarkComparabilityClaim, false) ||
		!requiredBool(record.Assurance.RecoveryClaim, false) ||
		!requiredBool(record.Assurance.ProductionDecisionEligible, false) {
		return fmt.Errorf("assurance does not match the bounded release-publication verification contract")
	}
	return nil
}

func validateReleaseVerificationWorkflowRun(run ReleaseVerificationWorkflowRun, candidate Candidate, wantJob string) error {
	if !validUnsignedDecimal(run.ID) {
		return fmt.Errorf("workflow_run.id must contain 1 to 32 decimal digits without a leading zero")
	}
	if run.Attempt < 1 || run.Attempt > maxJSONSafeInteger {
		return fmt.Errorf("workflow_run.attempt must be between 1 and %d", maxJSONSafeInteger)
	}
	if run.HeadSHA != candidate.GitCommit ||
		run.Repository != releaseWorkflowRepository ||
		run.Workflow != releaseWorkflowName ||
		run.Job != wantJob ||
		run.Ref != "refs/tags/"+candidate.Tag {
		return fmt.Errorf("workflow_run does not match the exact release workflow, job, ref, repository, and candidate")
	}
	return nil
}

func verifiedReleaseAssetChecks(publicOnly string) ReleaseAssetVerificationChecks {
	return ReleaseAssetVerificationChecks{
		TagTarget:               "verified",
		ClosedAssetSet:          "verified",
		DownloadedAssetBytes:    "verified",
		ArchiveChecksums:        "verified",
		MetadataChecksums:       "verified",
		ReleaseManifest:         "verified",
		CandidateBinaryIdentity: "verified",
		ProvenanceAttestations:  "verified",
		SBOMAttestations:        "verified",
		SBOMContents:            "verified",
		ImmutableRelease:        publicOnly,
		ReleaseAttestation:      publicOnly,
	}
}

func inventoryAssetDigest(inventory releaseassets.Inventory, name string) string {
	for _, asset := range inventory.Assets {
		if asset.Name == name {
			return asset.Digest
		}
	}
	return ""
}

func exactReleaseAssetNames(inventory releaseassets.Inventory, version string) bool {
	platforms := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"}
	want := make([]string, 0, 16)
	for _, platform := range platforms {
		prefix := "pgworkbench-" + version + "-" + platform
		want = append(want, prefix+".tar.gz", prefix+".spdx.json", prefix+"-sbom.sigstore.json")
	}
	want = append(want,
		"pgworkbench-"+version+"-SHA256SUMS.txt",
		"pgworkbench-"+version+"-METADATA-SHA256SUMS.txt",
		"pgworkbench-"+version+"-release-manifest.json",
		"pgworkbench-"+version+"-provenance.sigstore.json",
	)
	sort.Strings(want)
	got := make([]string, len(inventory.Assets))
	for index, asset := range inventory.Assets {
		got[index] = asset.Name
	}
	return reflect.DeepEqual(got, want)
}
