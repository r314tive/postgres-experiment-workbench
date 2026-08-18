package releaseevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestAdoptionPilotRequiresExactCandidateAndBoundedClaims(t *testing.T) {
	index := openIndex(RecordStatusActive)
	record := validAdoptionPilot(index.Candidate, "external-pilot-a", false)
	if err := validateAdoptionPilotRecord(record, index.Candidate); err != nil {
		t.Fatalf("valid pilot: %v", err)
	}
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("adoption-pilot-record.schema.json", marshalRecord(t, record, true)); err != nil {
		t.Fatalf("valid pilot schema: %v", err)
	}
	for name, test := range map[string]struct {
		mutate func(*AdoptionPilotRecord)
		want   string
	}{
		"wrong candidate":  {func(value *AdoptionPilotRecord) { value.Candidate.GitCommit = strings.Repeat("b", 40) }, "candidate"},
		"wrong guide tag":  {func(value *AdoptionPilotRecord) { value.Execution.GuideRef = "docs/authoring-tutorial.md@v0.0.1" }, "released guide"},
		"production claim": {func(value *AdoptionPilotRecord) { value.Assurance.ProductionSafetyClaim = boolPointer(true) }, "claims"},
		"authored with shell": {func(value *AdoptionPilotRecord) {
			value.Execution.AuthoredOrModifiedScenario = boolPointer(true)
			value.Execution.MaintainerShellAccess = boolPointer(true)
		}, "maintainer shell"},
	} {
		t.Run(name, func(t *testing.T) {
			copy := record
			test.mutate(&copy)
			if err := validateAdoptionPilotRecord(copy, index.Candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPilotAttachmentsRequireDistinctParticipantsAndAuthoringProof(t *testing.T) {
	directory := t.TempDir()
	indexPath, _, index := writeAttachIndex(t, directory, true)
	first := validAdoptionPilot(index.Candidate, "external-pilot-a", false)
	firstPath := writePilotRecord(t, directory, "first.json", first)
	firstResult, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "adoption_pilot_1", EvidenceFile: firstPath, EvidenceRef: "urn:pgworkbench:pilot-a", Output: filepath.Join(directory, "index-r1.json")})
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.GateStatus != GateStatusPassed {
		t.Fatalf("first result = %+v", firstResult)
	}
	if _, err := AttachGate(GateAttachOptions{IndexPath: firstResult.Output, Gate: "adoption_pilot_2", EvidenceFile: firstPath, EvidenceRef: "urn:pgworkbench:pilot-a", Output: filepath.Join(directory, "index-r2.json")}); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("same participant error = %v", err)
	}

	second := validAdoptionPilot(index.Candidate, "external-pilot-b", true)
	secondPath := writePilotRecord(t, directory, "second.json", second)
	secondResult, err := AttachGate(GateAttachOptions{IndexPath: firstResult.Output, Gate: "adoption_pilot_2", EvidenceFile: secondPath, EvidenceRef: "urn:pgworkbench:pilot-b", Output: filepath.Join(directory, "index-r2.json")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachGate(GateAttachOptions{IndexPath: secondResult.Output, Gate: "independent_authoring_reproduction", EvidenceFile: secondPath, EvidenceRef: "urn:pgworkbench:pilot-b", Output: filepath.Join(directory, "index-r3.json")}); err != nil {
		t.Fatal(err)
	}
	attached, err := LoadFile(filepath.Join(directory, "index-r3.json"))
	if err != nil {
		t.Fatal(err)
	}
	if attached.Gates.AdoptionPilot1.Evidence.Record.Subject != "external-pilot-a" || attached.Gates.AdoptionPilot2.Evidence.Record.Subject != "external-pilot-b" || attached.Gates.IndependentAuthoringReproduction.Evidence.Record.Subject != "external-pilot-b" {
		t.Fatalf("pilot identities were not retained: %+v", attached.Gates)
	}
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("release-evidence-index.schema.json", mustRead(t, filepath.Join(directory, "index-r3.json"))); err != nil {
		t.Fatalf("pilot attachment index schema: %v", err)
	}
}

func TestVerifyRejectsTransplantedPilotIdentity(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV3
	index.Lineage = &Lineage{Revision: 0}
	index.Gates.AdoptionPilot1 = Gate{Status: GateStatusPassed, Evidence: pilotEvidence("external-pilot-a", AdoptionPilotAdapter)}
	index.Gates.AdoptionPilot2 = Gate{Status: GateStatusPassed, Evidence: pilotEvidence("external-pilot-a", AdoptionPilotAdapter)}
	verification := Verify(index)
	if verification.Valid || !sliceContainsSubstring(verification.Issues, "distinct external participant") {
		t.Fatalf("transplanted pilot identities accepted: %+v", verification)
	}
}

func pilotEvidence(subject, adapter string) *Evidence {
	return &Evidence{Ref: "urn:pgworkbench:" + subject, Digest: "sha256:" + strings.Repeat("d", 64), CapturedAt: testTime, Record: &EvidenceRecord{SchemaVersion: AdoptionPilotSchema, ArtifactType: AdoptionPilotType, Adapter: adapter, Subject: subject}, Assurance: &EvidenceAssurance{Durability: EvidenceDurabilityAsserted, Authenticity: EvidenceAuthenticityUnverified}}
}

func validAdoptionPilot(candidate Candidate, participant string, authored bool) AdoptionPilotRecord {
	evidence := func(suffix string) *AdoptionPilotEvidence {
		return &AdoptionPilotEvidence{Ref: "urn:pgworkbench:" + suffix, Digest: "sha256:" + strings.Repeat("c", 64)}
	}
	record := AdoptionPilotRecord{SchemaVersion: AdoptionPilotSchema, ArtifactType: AdoptionPilotType, RecordStatus: "completed", PilotID: participant + "-record", RecordedAt: "2026-08-14T12:35:00Z", Candidate: AdoptionPilotCandidate{Version: candidate.Version, GitCommit: candidate.GitCommit, PackDigest: candidate.ScenarioPack.Digest}, Participant: AdoptionPilotParticipant{Identifier: participant, Relationship: "external-non-maintainer"}, Execution: AdoptionPilotExecution{GuideRef: "docs/authoring-tutorial.md@" + candidate.Tag, Scenario: "smoke", Runtime: "native", OS: "linux", Arch: "amd64", PostgresMajor: 16, StartedAt: testTime, FinishedAt: "2026-08-14T12:34:57Z", AuthoredOrModifiedScenario: boolPointer(authored), MaintainerShellAccess: boolPointer(false)}, AcceptancePredicate: "The released starter reaches a terminal verdict and its relocated bundle verifies independently.", Cleanup: AdoptionPilotCleanup{Status: "completed", Details: "The disposable runtime was stopped and removed."}, Result: AdoptionPilotResult{Status: GateStatusPassed, Summary: "Completed bounded starter scenario.", BundleEvidence: evidence(participant + "-bundle")}, UnresolvedFriction: []AdoptionPilotFriction{}, Assurance: AdoptionPilotAssurance{ProductionSafetyClaim: boolPointer(false), RepresentativePerformanceClaim: boolPointer(false), BenchmarkDecisionClaim: boolPointer(false)}}
	if authored {
		record.Result.MaintainerVerification = evidence(participant + "-maintainer")
	}
	return record
}

func writePilotRecord(t *testing.T, directory, name string, record AdoptionPilotRecord) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, marshalRecord(t, record, true), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
