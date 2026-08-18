package releaseevidence

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestAttachSealedCompatibilityAndAggregateRecords(t *testing.T) {
	dir := t.TempDir()
	index, draftAsset := releaseAssetAttachFixture(t, releaseQualificationDraft)
	indexPath := filepath.Join(dir, "index-r0.json")
	writeIndex(t, indexPath, index)

	source := compatibilityRecordFixture(t, index.Candidate, "source", draftAsset)
	sourcePath, _ := writeTypedRecord(t, dir, "source.json", source)
	if result, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "source_compatibility", EvidenceFile: sourcePath, EvidenceRef: "urn:pgworkbench:source", Output: filepath.Join(dir, "index-r1.json")}); err != nil || result.RecordAdapter != CompatibilitySourceAdapter {
		t.Fatalf("source attachment = %+v, %v", result, err)
	}

	first := aggregateRecordFixture(t, index.Candidate, 1, draftAsset, nil)
	firstPath, firstBytes := writeTypedRecord(t, dir, "aggregate-1.json", first)
	if result, err := AttachGate(GateAttachOptions{IndexPath: filepath.Join(dir, "index-r1.json"), Gate: "aggregate_attempt_1", EvidenceFile: firstPath, EvidenceRef: "urn:pgworkbench:aggregate:1", Output: filepath.Join(dir, "index-r2.json")}); err != nil || result.RecordAdapter != AggregateAttempt1Adapter {
		t.Fatalf("attempt one attachment = %+v, %v", result, err)
	}

	previous := digestExactBytes(firstBytes)
	second := aggregateRecordFixture(t, index.Candidate, 2, draftAsset, &previous)
	secondPath, _ := writeTypedRecord(t, dir, "aggregate-2.json", second)
	if result, err := AttachGate(GateAttachOptions{IndexPath: filepath.Join(dir, "index-r2.json"), Gate: "aggregate_attempt_2", EvidenceFile: secondPath, EvidenceRef: "urn:pgworkbench:aggregate:2", Output: filepath.Join(dir, "index-r3.json")}); err != nil || result.RecordAdapter != AggregateAttempt2Adapter {
		t.Fatalf("attempt two attachment = %+v, %v", result, err)
	}

	draft := compatibilityRecordFixture(t, index.Candidate, "draft", draftAsset)
	draftPath, _ := writeTypedRecord(t, dir, "draft.json", draft)
	if _, err := AttachGate(GateAttachOptions{IndexPath: filepath.Join(dir, "index-r3.json"), Gate: "draft_compatibility_7_cells", EvidenceFile: draftPath, EvidenceRef: "urn:pgworkbench:draft", Output: filepath.Join(dir, "index-r4.json")}); err != nil {
		t.Fatal(err)
	}
	attached, err := LoadFile(filepath.Join(dir, "index-r4.json"))
	if err != nil {
		t.Fatal(err)
	}
	if attached.Gates.SourceCompatibility.Status != GateStatusPassed || attached.Gates.AggregateAttempt1.Status != GateStatusPassed || attached.Gates.AggregateAttempt2.Status != GateStatusPassed || attached.Gates.DraftCompatibility7Cells.Status != GateStatusPassed {
		t.Fatalf("attached gates = %+v", attached.Gates)
	}
	if attached.Decision.Status != DecisionNoGo {
		t.Fatalf("operator-attested records must remain no-go: %+v", attached.Decision)
	}
}

func TestSealedQualificationSchemaAndSemanticRejections(t *testing.T) {
	index, asset := releaseAssetAttachFixture(t, releaseQualificationDraft)
	compat := compatibilityRecordFixture(t, index.Candidate, "source", asset)
	aggregate := aggregateRecordFixture(t, index.Candidate, 1, asset, nil)
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		schema string
		value  any
	}{{"release-compatibility-verification.schema.json", compat}, {"release-aggregate-verification.schema.json", aggregate}} {
		content, err := json.Marshal(item.value)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.ValidateJSON(item.schema, content); err != nil {
			t.Fatalf("%s: %v", item.schema, err)
		}
	}
	compat.Cells[1].Artifact.Name = "wrong"
	if err := validateCompatibilityVerification(compat, index.Candidate); err == nil || !strings.Contains(err.Error(), "exact name") {
		t.Fatalf("bad artifact error = %v", err)
	}
	aggregate = aggregateRecordFixture(t, index.Candidate, 2, asset, pointer("sha256:"+strings.Repeat("a", 64)))
	if err := validateAggregateVerification(aggregate, index.Candidate, index); err == nil || !strings.Contains(err.Error(), "attempt-one") {
		t.Fatalf("unbound attempt two error = %v", err)
	}
}

func compatibilityRecordFixture(t *testing.T, candidate Candidate, mode string, asset ReleaseAssetVerification) CompatibilityVerification {
	t.Helper()
	job := qualificationSealJob(mode)
	asset.WorkflowRun.ID, asset.WorkflowRun.Attempt = "987654321", 2
	cells := make([]CompatibilityCell, 0, 7)
	for i, id := range compatibilityCellIDs() {
		digestNibble := string("abcdef"[i%6])
		cells = append(cells, CompatibilityCell{ID: id, Artifact: QualificationArtifact{ID: string(rune('1' + i)), Name: compatibilityArtifactName(mode, id, candidate, 2), Digest: "sha256:" + strings.Repeat(digestNibble, 64)}})
	}
	return CompatibilityVerification{SchemaVersion: CompatibilityVerificationSchema, ArtifactType: CompatibilityVerificationType, QualificationMode: mode, Candidate: candidate, CapturedAt: "2026-08-14T12:36:00Z", WorkflowRun: ReleaseVerificationWorkflowRun{ID: "987654321", Attempt: 2, HeadSHA: candidate.GitCommit, Repository: releaseWorkflowRepository, Workflow: releaseWorkflowName, Job: job, Ref: "refs/tags/" + candidate.Tag}, AssetVerification: asset, Cells: cells, Assurance: qualificationAssurance("seven-cell-compatibility-observation")}
}

func aggregateRecordFixture(t *testing.T, candidate Candidate, attempt int64, asset ReleaseAssetVerification, previous *string) AggregateVerification {
	t.Helper()
	asset.WorkflowRun.ID, asset.WorkflowRun.Attempt = "987654321", 2
	return AggregateVerification{SchemaVersion: AggregateVerificationSchema, ArtifactType: AggregateVerificationType, AggregateAttempt: attempt, Candidate: candidate, CapturedAt: "2026-08-14T12:36:00Z", WorkflowRun: ReleaseVerificationWorkflowRun{ID: "987654321", Attempt: 2, HeadSHA: candidate.GitCommit, Repository: releaseWorkflowRepository, Workflow: releaseWorkflowName, Job: qualificationSealSourceDraftJob, Ref: "refs/tags/" + candidate.Tag}, AssetVerification: asset, AggregateArtifact: QualificationArtifact{ID: "42", Name: "aggregate-" + string(rune('0'+attempt)) + "-" + candidate.GitCommit + "-2", Digest: "sha256:" + strings.Repeat("b", 64)}, PreviousAttemptRecordDigest: previous, Assurance: qualificationAssurance("clean-checkout-aggregate-observation")}
}

func qualificationAssurance(purpose string) QualificationAssurance {
	return QualificationAssurance{Purpose: purpose, VerificationScope: "workflow-local-actions-artifact-identity", ActionsArtifactDurable: boolPointer(false), CandidateIdentityReverified: boolPointer(true), PerformanceClaim: boolPointer(false), BenchmarkComparabilityClaim: boolPointer(false), RecoveryClaim: boolPointer(false), ProductionDecisionEligible: boolPointer(false)}
}
