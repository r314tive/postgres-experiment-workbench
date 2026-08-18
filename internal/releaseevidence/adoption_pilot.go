package releaseevidence

import (
	"fmt"
	"strings"
)

const (
	AdoptionPilotSchema              = "pgworkbench.adoption-pilot-record/v1"
	AdoptionPilotType                = "pgworkbench.adoption-pilot-record"
	AdoptionPilotAdapter             = "pgworkbench.adoption-pilot-record/pilot/v1"
	IndependentAuthoringPilotAdapter = "pgworkbench.adoption-pilot-record/authoring/v1"
)

// AdoptionPilotRecord is bounded external-user evidence. It is intentionally
// not a performance, recovery, or production-safety attestation.
type AdoptionPilotRecord struct {
	SchemaVersion       string                   `json:"schema_version"`
	ArtifactType        string                   `json:"artifact_type"`
	RecordStatus        string                   `json:"record_status"`
	PilotID             string                   `json:"pilot_id"`
	RecordedAt          string                   `json:"recorded_at"`
	Candidate           AdoptionPilotCandidate   `json:"candidate"`
	Participant         AdoptionPilotParticipant `json:"participant"`
	Execution           AdoptionPilotExecution   `json:"execution"`
	AcceptancePredicate string                   `json:"acceptance_predicate"`
	Cleanup             AdoptionPilotCleanup     `json:"cleanup"`
	Result              AdoptionPilotResult      `json:"result"`
	UnresolvedFriction  []AdoptionPilotFriction  `json:"unresolved_friction"`
	Assurance           AdoptionPilotAssurance   `json:"assurance"`
}

type AdoptionPilotCandidate struct {
	Version    string `json:"version"`
	GitCommit  string `json:"git_commit"`
	PackDigest string `json:"pack_digest"`
}
type AdoptionPilotParticipant struct {
	Identifier   string `json:"identifier"`
	Relationship string `json:"relationship"`
}
type AdoptionPilotExecution struct {
	GuideRef                   string `json:"guide_ref"`
	Scenario                   string `json:"scenario"`
	Runtime                    string `json:"runtime"`
	OS                         string `json:"os"`
	Arch                       string `json:"arch"`
	PostgresMajor              int64  `json:"postgres_major"`
	StartedAt                  string `json:"started_at"`
	FinishedAt                 string `json:"finished_at"`
	AuthoredOrModifiedScenario *bool  `json:"authored_or_modified_scenario"`
	MaintainerShellAccess      *bool  `json:"maintainer_shell_access"`
}
type AdoptionPilotCleanup struct {
	Status  string `json:"status"`
	Details string `json:"details"`
}
type AdoptionPilotResult struct {
	Status                 string                 `json:"status"`
	Summary                string                 `json:"summary"`
	BundleEvidence         *AdoptionPilotEvidence `json:"bundle_evidence,omitempty"`
	MaintainerVerification *AdoptionPilotEvidence `json:"maintainer_verification,omitempty"`
}
type AdoptionPilotEvidence struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type AdoptionPilotFriction struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}
type AdoptionPilotAssurance struct {
	ProductionSafetyClaim          *bool `json:"production_safety_claim"`
	RepresentativePerformanceClaim *bool `json:"representative_performance_claim"`
	BenchmarkDecisionClaim         *bool `json:"benchmark_decision_claim"`
}

func validateAdoptionPilotRecord(record AdoptionPilotRecord, candidate Candidate) error {
	if record.SchemaVersion != AdoptionPilotSchema || record.ArtifactType != AdoptionPilotType || record.RecordStatus != "completed" || !criticalReviewIDPattern.MatchString(record.PilotID) || !canonicalTimestampPattern.MatchString(record.RecordedAt) || !validDateTime(record.RecordedAt) {
		return fmt.Errorf("adoption pilot must be a completed canonical typed record")
	}
	if record.Candidate.Version != candidate.Version || record.Candidate.GitCommit != candidate.GitCommit || record.Candidate.PackDigest != candidate.ScenarioPack.Digest {
		return fmt.Errorf("adoption pilot candidate does not match the predecessor index candidate")
	}
	if !criticalReviewIDPattern.MatchString(record.Participant.Identifier) || record.Participant.Relationship != "external-non-maintainer" {
		return fmt.Errorf("adoption pilot requires a canonical external non-maintainer participant")
	}
	if err := validateAdoptionPilotExecution(record.Execution, candidate); err != nil {
		return err
	}
	recorded, _ := parseDateTime(record.RecordedAt)
	finished, _ := parseDateTime(record.Execution.FinishedAt)
	if recorded.Before(finished) {
		return fmt.Errorf("pilot recorded_at must not precede execution.finished_at")
	}
	if !validLength(record.AcceptancePredicate, 1, 2000) || !oneOf(record.Cleanup.Status, "completed", "not-required") || !validLength(record.Cleanup.Details, 1, 2000) {
		return fmt.Errorf("adoption pilot requires bounded acceptance and cleanup evidence")
	}
	if !oneOf(record.Result.Status, GateStatusPassed, GateStatusFailed) || !validLength(record.Result.Summary, 1, 4000) || !validPilotEvidence(record.Result.BundleEvidence) {
		return fmt.Errorf("completed adoption pilot requires a passed/failed result and durable bundle evidence")
	}
	if requiredBool(record.Execution.AuthoredOrModifiedScenario, true) && (!requiredBool(record.Execution.MaintainerShellAccess, false) || !validPilotEvidence(record.Result.MaintainerVerification)) {
		return fmt.Errorf("authored or modified scenario requires no maintainer shell access and independent maintainer verification")
	}
	if !requiredBool(record.Assurance.ProductionSafetyClaim, false) || !requiredBool(record.Assurance.RepresentativePerformanceClaim, false) || !requiredBool(record.Assurance.BenchmarkDecisionClaim, false) {
		return fmt.Errorf("adoption pilot assurance claims must remain false")
	}
	for index, friction := range record.UnresolvedFriction {
		if !oneOf(friction.Severity, "critical", "high", "medium", "low") || !validLength(friction.Summary, 1, 2000) {
			return fmt.Errorf("unresolved_friction[%d] is invalid", index)
		}
	}
	return nil
}

func validateAdoptionPilotExecution(execution AdoptionPilotExecution, candidate Candidate) error {
	if !strings.HasSuffix(execution.GuideRef, "@"+candidate.Tag) || !validLength(execution.Scenario, 1, 128) || !oneOf(execution.Runtime, "docker", "native") || !oneOf(execution.OS, "linux", "darwin") || !oneOf(execution.Arch, "amd64", "arm64") || execution.PostgresMajor < 10 || execution.PostgresMajor > 99 || !canonicalTimestampPattern.MatchString(execution.StartedAt) || !validDateTime(execution.StartedAt) || !canonicalTimestampPattern.MatchString(execution.FinishedAt) || !validDateTime(execution.FinishedAt) || execution.AuthoredOrModifiedScenario == nil || execution.MaintainerShellAccess == nil {
		return fmt.Errorf("pilot execution does not bind the released guide, bounded runtime, and canonical timestamps")
	}
	started, _ := parseDateTime(execution.StartedAt)
	finished, _ := parseDateTime(execution.FinishedAt)
	if finished.Before(started) {
		return fmt.Errorf("pilot execution finished_at must not precede started_at")
	}
	return nil
}

func validPilotEvidence(evidence *AdoptionPilotEvidence) bool {
	return evidence != nil && validDurableRef(evidence.Ref) && validDigest(evidence.Digest)
}

func pilotAttachment(record AdoptionPilotRecord, index Index, requestedGate string) (derivedGateAttachment, error) {
	if err := validateAdoptionPilotRecord(record, index.Candidate); err != nil {
		return derivedGateAttachment{}, err
	}
	if !oneOf(requestedGate, "adoption_pilot_1", "adoption_pilot_2", "independent_authoring_reproduction") {
		return derivedGateAttachment{}, fmt.Errorf("adoption pilot record may attach only an adoption or independent-authoring gate")
	}
	adapter := AdoptionPilotAdapter
	if requestedGate == "independent_authoring_reproduction" {
		if !requiredBool(record.Execution.AuthoredOrModifiedScenario, true) || record.Result.Status != GateStatusPassed {
			return derivedGateAttachment{}, fmt.Errorf("independent authoring requires a passed authored or modified scenario")
		}
		adapter = IndependentAuthoringPilotAdapter
	} else if otherPilotSubject(index, requestedGate) == record.Participant.Identifier {
		return derivedGateAttachment{}, fmt.Errorf("adoption pilots must have distinct external participant identities")
	}
	status := GateStatusPassed
	if record.Result.Status == GateStatusFailed {
		status = GateStatusFailed
	}
	return derivedGateAttachment{Gate: requestedGate, Status: status, CapturedAt: record.RecordedAt, SchemaVersion: record.SchemaVersion, ArtifactType: record.ArtifactType, Adapter: adapter, Subject: record.Participant.Identifier}, nil
}

func otherPilotSubject(index Index, requestedGate string) string {
	var gate Gate
	if requestedGate == "adoption_pilot_1" {
		gate = index.Gates.AdoptionPilot2
	} else {
		gate = index.Gates.AdoptionPilot1
	}
	if gate.Evidence == nil || gate.Evidence.Record == nil {
		return ""
	}
	return gate.Evidence.Record.Subject
}
