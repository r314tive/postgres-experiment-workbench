package releaseevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestCriticalFindingReviewBindsCandidateAndFailsClosed(t *testing.T) {
	index := openIndex(RecordStatusActive)
	record := validCriticalFindingReview(index.Candidate)
	if err := validateCriticalFindingReview(record, index.Candidate); err != nil {
		t.Fatalf("valid signed review: %v", err)
	}
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("critical-finding-review.schema.json", marshalRecord(t, record, true)); err != nil {
		t.Fatalf("valid signed review schema: %v", err)
	}
	for name, test := range map[string]struct {
		mutate func(*CriticalFindingReview)
		want   string
	}{
		"wrong candidate": {func(value *CriticalFindingReview) { value.Candidate.GitCommit = strings.Repeat("a", 40) }, "candidate"},
		"missing ruleset review": {func(value *CriticalFindingReview) {
			value.RepositoryControls.TagRuleset.BypassActorsReviewed = boolPointer(false)
		}, "tag_ruleset"},
		"unsigned": {func(value *CriticalFindingReview) { value.Signoff.Status = "open" }, "signoff"},
		"go with open critical": {func(value *CriticalFindingReview) {
			value.Findings = []CriticalFinding{{ID: "SEC-1", Category: "security", Severity: "critical", Status: "open", Summary: "unresolved"}}
		}, "critical finding"},
	} {
		t.Run(name, func(t *testing.T) {
			copy := record
			test.mutate(&copy)
			if err := validateCriticalFindingReview(copy, index.Candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAttachCriticalFindingReviewDerivesOutcomeWithoutWorkflowIdentity(t *testing.T) {
	directory := t.TempDir()
	indexPath, _, index := writeAttachIndex(t, directory, true)
	record := validCriticalFindingReview(index.Candidate)
	content := marshalRecord(t, record, true)
	recordPath := filepath.Join(directory, "critical-review.json")
	if err := os.WriteFile(recordPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "critical_finding_review", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:critical-review", Output: filepath.Join(directory, "index-r1.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.GateStatus != GateStatusPassed || result.RecordAdapter != CriticalFindingReviewAdapter || result.IndexVerification.Decision != DecisionNoGo || result.IndexVerification.AuthorizationEligible {
		t.Fatalf("critical review attachment = %+v", result)
	}
	attached, err := LoadFile(result.Output)
	if err != nil {
		t.Fatal(err)
	}
	evidence := attached.Gates.CriticalFindingReview.Evidence
	if evidence == nil || evidence.RunID != nil || evidence.RunAttempt != nil || evidence.Record == nil || *evidence.Record != (EvidenceRecord{SchemaVersion: CriticalFindingReviewSchema, ArtifactType: CriticalFindingReviewType, Adapter: CriticalFindingReviewAdapter}) {
		t.Fatalf("attached critical review evidence = %+v", evidence)
	}
}

func TestAttachCriticalFindingNoGoDerivesFailedGate(t *testing.T) {
	directory := t.TempDir()
	indexPath, _, index := writeAttachIndex(t, directory, true)
	record := validCriticalFindingReview(index.Candidate)
	record.Decision.Status = DecisionNoGo
	record.Decision.Rationale = "A review may record no-go without claiming the gate passed."
	recordPath := filepath.Join(directory, "critical-review.json")
	if err := os.WriteFile(recordPath, marshalRecord(t, record, true), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "critical_finding_review", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:critical-review-no-go", Output: filepath.Join(directory, "index-r1.json")})
	if err != nil {
		t.Fatal(err)
	}
	if result.GateStatus != GateStatusFailed || result.IndexVerification.Status != StatusFailed || result.IndexVerification.Decision != DecisionNoGo {
		t.Fatalf("no-go review attachment = %+v", result)
	}
}

func validCriticalFindingReview(candidate Candidate) CriticalFindingReview {
	evidence := func(suffix string) *CriticalReviewEvidence {
		return &CriticalReviewEvidence{Ref: "urn:pgworkbench:" + suffix, Digest: "sha256:" + strings.Repeat("a", 64), CapturedAt: testTime}
	}
	return CriticalFindingReview{
		SchemaVersion: CriticalFindingReviewSchema,
		ArtifactType:  CriticalFindingReviewType,
		RecordStatus:  "signed",
		ReviewID:      "v0-3-critical-review",
		Candidate:     CriticalReviewCandidate{Version: candidate.Version, Tag: candidate.Tag, GitCommit: candidate.GitCommit, PackDigest: candidate.ScenarioPack.Digest},
		Scope:         []string{"security", "data-loss", "portability", "evidence-integrity"},
		RepositoryControls: CriticalReviewControls{
			TagRuleset:        CriticalTagRuleset{Status: ControlStatusVerified, ID: 1, UpdatedAt: testTime, Target: "tag", Enforcement: "active", IncludePattern: "refs/tags/v*", Excludes: []string{}, CreationRestricted: boolPointer(true), UpdateProhibited: boolPointer(true), DeletionProhibited: boolPointer(true), BypassActorsReviewed: boolPointer(true), Evidence: evidence("ruleset")},
			ImmutableReleases: CriticalImmutable{Status: ControlStatusVerified, Enabled: boolPointer(true), EnforcedByOwner: boolPointer(true), Evidence: evidence("immutable")},
		},
		Findings: []CriticalFinding{},
		Decision: CriticalReviewDecision{Status: DecisionGo, RecordedAt: "2026-08-14T12:34:57Z", Rationale: "All critical categories were reviewed."},
		Signoff:  CriticalReviewSignoff{Status: "signed", Reviewer: "reviewer", ReviewerRole: "repository-administrator", SignedAt: testTime, Statement: "Reviewed critical findings and controls.", SignatureEvidence: evidence("signature")},
	}
}
