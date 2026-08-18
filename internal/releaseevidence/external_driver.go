package releaseevidence

import (
	"fmt"
	"regexp"
)

const (
	ExternalDriverVerificationSchema  = "pgworkbench.release-external-driver-verification/v1"
	ExternalDriverVerificationType    = "pgworkbench.release-external-driver-verification"
	ExternalDriverVerificationAdapter = "pgworkbench.release-external-driver-verification/draft/v1"
	externalDriverQualificationMode   = "draft-release-smoke"
	externalDriverRepository          = "r314tive/postgres-experiment-workbench"
)

var (
	unsignedDecimalPattern    = regexp.MustCompile(`^[1-9][0-9]*$`)
	canonicalTimestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-5][0-9]Z$`)
)

// ExternalDriverVerification is the deliberately small, candidate-bound
// result emitted only after the release workflow has independently reverified
// the draft candidate and the closed metadata-only external-driver artifact.
// It contains no caller-selectable gate status: the adapter derives the only
// supported outcome from exact invariant fields.
type ExternalDriverVerification struct {
	SchemaVersion     string                           `json:"schema_version"`
	ArtifactType      string                           `json:"artifact_type"`
	QualificationMode string                           `json:"qualification_mode"`
	Candidate         Candidate                        `json:"candidate"`
	CapturedAt        string                           `json:"captured_at"`
	WorkflowRun       ExternalDriverWorkflowRun        `json:"workflow_run"`
	Source            ExternalDriverVerificationSource `json:"source"`
	Drivers           []string                         `json:"drivers"`
	Assurance         ExternalDriverAssurance          `json:"assurance"`
}

type ExternalDriverWorkflowRun struct {
	ID         string `json:"id"`
	Attempt    int64  `json:"attempt"`
	HeadSHA    string `json:"head_sha"`
	Repository string `json:"repository"`
}

type ExternalDriverVerificationSource struct {
	GateDigest            string                         `json:"gate_digest"`
	MetadataArchiveDigest string                         `json:"metadata_archive_digest"`
	ProviderArtifact      ExternalDriverProviderArtifact `json:"provider_artifact"`
	ReleaseArchiveDigest  string                         `json:"release_archive_digest"`
	ReleaseManifestDigest string                         `json:"release_manifest_digest"`
}

type ExternalDriverProviderArtifact struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type ExternalDriverAssurance struct {
	Purpose                              string `json:"purpose"`
	ArtifactPayload                      string `json:"artifact_payload"`
	VerificationScope                    string `json:"verification_scope"`
	ThirdPartyRuntimeBytesUploaded       *bool  `json:"third_party_runtime_bytes_uploaded"`
	PerformanceClaim                     *bool  `json:"performance_claim"`
	ProductionDecisionEligible           *bool  `json:"production_decision_eligible"`
	SourceToBinaryAttested               *bool  `json:"source_to_binary_attested"`
	DriverRuntimeClosureAttested         *bool  `json:"driver_runtime_closure_attested"`
	HostRuntimeDependenciesAttested      *bool  `json:"host_runtime_dependencies_attested"`
	BenchmarkComparabilityClaim          *bool  `json:"benchmark_comparability_claim"`
	ProjectRedistribution                *bool  `json:"project_redistribution"`
	AllExecutionsLocallyVerified         *bool  `json:"all_executions_locally_verified"`
	ExactSourceToStagedFileMatch         *bool  `json:"exact_source_to_staged_file_match"`
	DisposableLoopbackTargetAcknowledged *bool  `json:"disposable_loopback_target_acknowledged"`
	SystemDatabasesDenied                *bool  `json:"system_databases_denied"`
	CandidateIdentityReverified          *bool  `json:"candidate_identity_reverified"`
	ProviderArtifactReverified           *bool  `json:"provider_artifact_reverified"`
	ReleaseArchiveProvenanceVerified     *bool  `json:"release_archive_provenance_verified"`
	ReleaseManifestProvenanceVerified    *bool  `json:"release_manifest_provenance_verified"`
}

var externalDriverIDs = []string{
	"benchbase-postgresql-33c0047",
	"hammerdb-postgresql-6.0",
	"sysbench-postgresql-1.0.20",
}

func validateExternalDriverVerification(record ExternalDriverVerification, candidate Candidate) error {
	if record.SchemaVersion != ExternalDriverVerificationSchema || record.ArtifactType != ExternalDriverVerificationType {
		return fmt.Errorf("unsupported external-driver verification type or schema")
	}
	if record.QualificationMode != externalDriverQualificationMode {
		return fmt.Errorf("qualification_mode = %q, want %q", record.QualificationMode, externalDriverQualificationMode)
	}
	if record.Candidate != candidate {
		return fmt.Errorf("external-driver verification candidate does not match the predecessor index candidate")
	}
	if !canonicalTimestampPattern.MatchString(record.CapturedAt) || !validDateTime(record.CapturedAt) {
		return fmt.Errorf("captured_at must be a canonical UTC RFC3339 date-time with second precision")
	}
	if record.WorkflowRun.Repository != externalDriverRepository {
		return fmt.Errorf("workflow_run.repository = %q, want %q", record.WorkflowRun.Repository, externalDriverRepository)
	}
	if !validUnsignedDecimal(record.WorkflowRun.ID) {
		return fmt.Errorf("workflow_run.id must contain 1 to 32 decimal digits without a leading zero")
	}
	if record.WorkflowRun.Attempt < 1 || record.WorkflowRun.Attempt > maxJSONSafeInteger {
		return fmt.Errorf("workflow_run.attempt must be between 1 and %d", maxJSONSafeInteger)
	}
	if record.WorkflowRun.HeadSHA != candidate.GitCommit {
		return fmt.Errorf("workflow_run.head_sha does not match the candidate commit")
	}
	for _, field := range []struct {
		name   string
		digest string
	}{
		{name: "source.gate_digest", digest: record.Source.GateDigest},
		{name: "source.metadata_archive_digest", digest: record.Source.MetadataArchiveDigest},
		{name: "source.provider_artifact.digest", digest: record.Source.ProviderArtifact.Digest},
		{name: "source.release_archive_digest", digest: record.Source.ReleaseArchiveDigest},
		{name: "source.release_manifest_digest", digest: record.Source.ReleaseManifestDigest},
	} {
		if !validDigest(field.digest) {
			return fmt.Errorf("%s must be a lowercase sha256 digest", field.name)
		}
	}
	if !validUnsignedDecimal(record.Source.ProviderArtifact.ID) {
		return fmt.Errorf("source.provider_artifact.id must contain 1 to 32 decimal digits without a leading zero")
	}
	if !validLength(record.Source.ProviderArtifact.Name, 1, 256) {
		return fmt.Errorf("source.provider_artifact.name must contain 1 to 256 characters")
	}
	wantArtifactName := fmt.Sprintf(
		"draft-external-driver-metadata-%s-%s-%d",
		candidate.Tag,
		candidate.GitCommit,
		record.WorkflowRun.Attempt,
	)
	if record.Source.ProviderArtifact.Name != wantArtifactName {
		return fmt.Errorf("source.provider_artifact.name = %q, want %q", record.Source.ProviderArtifact.Name, wantArtifactName)
	}
	if len(record.Drivers) != len(externalDriverIDs) {
		return fmt.Errorf("drivers must contain the exact ordered external-driver set")
	}
	for index := range externalDriverIDs {
		if record.Drivers[index] != externalDriverIDs[index] {
			return fmt.Errorf("drivers must contain the exact ordered external-driver set")
		}
	}
	if record.Assurance.Purpose != "adapter-compatibility-release-smoke" ||
		record.Assurance.ArtifactPayload != "metadata-only-no-third-party-runtime-bytes" ||
		record.Assurance.VerificationScope != "workflow-local-content-and-semantics" ||
		!requiredBool(record.Assurance.ThirdPartyRuntimeBytesUploaded, false) ||
		!requiredBool(record.Assurance.PerformanceClaim, false) ||
		!requiredBool(record.Assurance.ProductionDecisionEligible, false) ||
		!requiredBool(record.Assurance.SourceToBinaryAttested, false) ||
		!requiredBool(record.Assurance.DriverRuntimeClosureAttested, true) ||
		!requiredBool(record.Assurance.HostRuntimeDependenciesAttested, false) ||
		!requiredBool(record.Assurance.BenchmarkComparabilityClaim, false) ||
		!requiredBool(record.Assurance.ProjectRedistribution, false) ||
		!requiredBool(record.Assurance.AllExecutionsLocallyVerified, true) ||
		!requiredBool(record.Assurance.ExactSourceToStagedFileMatch, true) ||
		!requiredBool(record.Assurance.DisposableLoopbackTargetAcknowledged, true) ||
		!requiredBool(record.Assurance.SystemDatabasesDenied, true) ||
		!requiredBool(record.Assurance.CandidateIdentityReverified, true) ||
		!requiredBool(record.Assurance.ProviderArtifactReverified, true) ||
		!requiredBool(record.Assurance.ReleaseArchiveProvenanceVerified, true) ||
		!requiredBool(record.Assurance.ReleaseManifestProvenanceVerified, true) {
		return fmt.Errorf("assurance does not match the bounded external-driver verification contract")
	}
	return nil
}

func requiredBool(actual *bool, want bool) bool {
	return actual != nil && *actual == want
}

func validUnsignedDecimal(value string) bool {
	return len(value) <= 32 && unsignedDecimalPattern.MatchString(value)
}
