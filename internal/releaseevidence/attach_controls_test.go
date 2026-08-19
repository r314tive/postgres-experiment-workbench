package releaseevidence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

func TestAttachControlsCreatesOneAtomicCandidateBoundRevision(t *testing.T) {
	directory := t.TempDir()
	index, err := NewIndex(controlsAttachCandidate(t), testTime)
	if err != nil {
		t.Fatal(err)
	}
	before := index
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
	indexBytes := mustRead(t, indexPath)
	record := controlsAttachRecordFixture(t, index.Candidate)
	recordPath, recordBytes := writeTypedRecord(t, directory, "preventive-controls.json", record)
	output := filepath.Join(directory, "index-r1.json")

	result, err := AttachControls(ControlsAttachOptions{
		IndexPath: indexPath, EvidenceFile: recordPath,
		EvidenceRef: "https://evidence.example.test/releases/v0.3.0/preventive-controls.json",
		Output:      output,
	})
	if err != nil {
		t.Fatal(err)
	}
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		"preventive_controls.tag_ruleset",
		"preventive_controls.tag_ruleset.bypass_review",
		"preventive_controls.immutable_releases",
	}
	wantAdapters := []string{
		PreventiveControlsTagRulesetAdapter,
		PreventiveControlsBypassReviewAdapter,
		PreventiveControlsImmutableReleasesAdapter,
	}
	if result.Output != absoluteOutput || result.Revision != 1 || !reflect.DeepEqual(result.Controls, wantPaths) || !reflect.DeepEqual(result.RecordAdapters, wantAdapters) {
		t.Fatalf("attach result identity = %+v", result)
	}
	if result.PreviousIndexDigest != digestExactBytes(indexBytes) || result.EvidenceDigest != digestExactBytes(recordBytes) || result.RecordSchemaVersion != PreventiveControlsVerificationSchema || result.RecordArtifactType != PreventiveControlsVerificationType {
		t.Fatalf("attach result record binding = %+v", result)
	}
	if result.EvidenceDurability != EvidenceDurabilityAsserted || result.EvidenceAuthenticity != EvidenceAuthenticityUnverified {
		t.Fatalf("attach result trust boundary = %+v", result)
	}
	if !result.IndexVerification.Valid || result.IndexVerification.Status != StatusOpen || result.IndexVerification.Decision != DecisionNoGo || result.IndexVerification.AuthorizationEligible || result.IndexVerification.AssuranceStatus != AssuranceOperatorAttested {
		t.Fatalf("attach verification = %+v", result.IndexVerification)
	}
	wantVerificationPaths := []string{
		"preventive_controls.immutable_releases",
		"preventive_controls.tag_ruleset",
		"preventive_controls.tag_ruleset.bypass_review",
	}
	if !reflect.DeepEqual(result.IndexVerification.PassedGates, wantVerificationPaths) || !reflect.DeepEqual(result.IndexVerification.UnqualifiedEvidence, wantVerificationPaths) {
		t.Fatalf("controls were not classified as exactly three unqualified passes: %+v", result.IndexVerification)
	}

	attached, err := LoadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Candidate != before.Candidate || attached.CreatedAt != before.CreatedAt || !reflect.DeepEqual(attached.Gates, before.Gates) {
		t.Fatal("controls attachment changed candidate, creation time, or readiness gates")
	}
	if attached.Lineage == nil || attached.Lineage.Revision != 1 || attached.Lineage.PreviousIndexDigest == nil || *attached.Lineage.PreviousIndexDigest != digestExactBytes(indexBytes) {
		t.Fatalf("attached lineage = %+v", attached.Lineage)
	}
	controls := attached.PreventiveControls
	if controls.TagRuleset.Status != ControlStatusVerified || controls.TagRuleset.BypassReview.Status != ReviewStatusAdminReviewed || controls.ImmutableReleases.Status != ControlStatusVerified || controls.ImmutableReleases.Enabled == nil || !*controls.ImmutableReleases.Enabled {
		t.Fatalf("attached controls = %+v", controls)
	}
	if controls.TagRuleset.BypassReview.Reviewer == nil || *controls.TagRuleset.BypassReview.Reviewer != record.BypassReview.Reviewer || controls.TagRuleset.BypassReview.RulesetID == nil || *controls.TagRuleset.BypassReview.RulesetID != record.BypassReview.RulesetID {
		t.Fatalf("attached bypass review = %+v", controls.TagRuleset.BypassReview)
	}
	evidence := []*Evidence{
		controls.TagRuleset.APIEvidence,
		controls.TagRuleset.BypassReview.Evidence,
		controls.ImmutableReleases.APIEvidence,
	}
	for index, item := range evidence {
		if item == nil || item.Record == nil || item.Assurance == nil || item.Record.Adapter != wantAdapters[index] {
			t.Fatalf("attached evidence[%d] = %+v", index, item)
		}
		if item.Ref != "https://evidence.example.test/releases/v0.3.0/preventive-controls.json" || item.Digest != digestExactBytes(recordBytes) || item.CapturedAt != record.CapturedAt || item.RunID == nil || *item.RunID != record.WorkflowRun.ID || item.RunAttempt == nil || *item.RunAttempt != record.WorkflowRun.Attempt {
			t.Fatalf("attached evidence[%d] identity = %+v", index, item)
		}
		if index > 0 && !reflect.DeepEqual(normalizedPreventiveControlEvidence(*evidence[0]), normalizedPreventiveControlEvidence(*item)) {
			t.Fatalf("attached evidence[%d] did not retain one atomic record identity", index)
		}
	}
	assertFileBytes(t, indexPath, indexBytes)
	assertFileBytes(t, recordPath, recordBytes)
	if strings.Contains(string(mustRead(t, output)), recordPath) {
		t.Fatal("local evidence path leaked into the successor index")
	}
}

func TestAttachControlsRejectsNonCanonicalOrAlreadyClosedState(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, directory string, index Index, recordPath string) string
	}{
		{
			name: "partial open state",
			setup: func(t *testing.T, directory string, index Index, _ string) string {
				index.PreventiveControls.ImmutableReleases.Enabled = boolPointer(true)
				path := filepath.Join(directory, "index-r0.json")
				writeIndex(t, path, index)
				return path
			},
		},
		{
			name: "partially closed state",
			setup: func(t *testing.T, directory string, index Index, _ string) string {
				index.PreventiveControls.TagRuleset.Status = ControlStatusVerified
				index.PreventiveControls.TagRuleset.APIEvidence = &Evidence{
					Ref: "urn:pgworkbench:evidence:legacy-partial-control", Digest: "sha256:" + strings.Repeat("a", 64), CapturedAt: testTime,
				}
				if err := finalizeDerivedDecision(&index, testTime); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(directory, "index-r0.json")
				writeIndex(t, path, index)
				return path
			},
		},
		{
			name: "already attached",
			setup: func(t *testing.T, directory string, index Index, recordPath string) string {
				path := filepath.Join(directory, "index-r0.json")
				writeIndex(t, path, index)
				first := filepath.Join(directory, "index-r1.json")
				if _, err := AttachControls(ControlsAttachOptions{IndexPath: path, EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:first-controls", Output: first}); err != nil {
					t.Fatal(err)
				}
				return first
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			index, err := NewIndex(controlsAttachCandidate(t), testTime)
			if err != nil {
				t.Fatal(err)
			}
			recordPath, _ := writeTypedRecord(t, directory, "controls.json", controlsAttachRecordFixture(t, index.Candidate))
			predecessor := test.setup(t, directory, index, recordPath)
			output := filepath.Join(directory, "index-r2.json")
			if filepath.Base(predecessor) == "index-r0.json" {
				output = filepath.Join(directory, "index-r1.json")
			}
			_, attachErr := AttachControls(ControlsAttachOptions{IndexPath: predecessor, EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:controls", Output: output})
			if attachErr == nil || !strings.Contains(attachErr.Error(), "exact canonical open baseline") {
				t.Fatalf("AttachControls error = %v", attachErr)
			}
			assertNotExist(t, output)
		})
	}
}

func TestAttachControlsMigratesV2PredecessorAndRejectsV1(t *testing.T) {
	for _, test := range []struct {
		name       string
		schema     string
		lineage    *Lineage
		wantError  string
		wantSchema string
	}{
		{name: "v2", schema: SchemaVersionV2, lineage: &Lineage{Revision: 0}, wantSchema: SchemaVersionV3},
		{name: "v1", schema: SchemaVersionV1, lineage: nil, wantError: "requires a v2 or v3 predecessor"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			index, err := NewIndex(controlsAttachCandidate(t), testTime)
			if err != nil {
				t.Fatal(err)
			}
			index.SchemaVersion = test.schema
			index.Lineage = test.lineage
			indexPath := filepath.Join(directory, "index-r0.json")
			writeIndex(t, indexPath, index)
			recordPath, _ := writeTypedRecord(t, directory, "controls.json", controlsAttachRecordFixture(t, index.Candidate))
			output := filepath.Join(directory, "index-r1.json")
			_, attachErr := AttachControls(ControlsAttachOptions{IndexPath: indexPath, EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:controls-version", Output: output})
			if test.wantError != "" {
				if attachErr == nil || !strings.Contains(attachErr.Error(), test.wantError) {
					t.Fatalf("AttachControls error = %v, want %q", attachErr, test.wantError)
				}
				assertNotExist(t, output)
				return
			}
			if attachErr != nil {
				t.Fatal(attachErr)
			}
			attached, err := LoadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if attached.SchemaVersion != test.wantSchema || attached.Lineage == nil || attached.Lineage.Revision != 1 {
				t.Fatalf("migrated controls attachment = schema %q lineage %+v", attached.SchemaVersion, attached.Lineage)
			}
		})
	}
}

func TestAttachControlsStrictRecordAndNoClobber(t *testing.T) {
	directory := t.TempDir()
	index, err := NewIndex(controlsAttachCandidate(t), testTime)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
	record := controlsAttachRecordFixture(t, index.Candidate)
	canonical, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{name: "unknown", content: []byte(strings.Replace(string(canonical), `"artifact_type":`, `"unknown":true,"artifact_type":`, 1))},
		{name: "duplicate", content: []byte(strings.Replace(string(canonical), `"artifact_type":`, `"artifact_type":"duplicate","artifact_type":`, 1))},
		{name: "trailing", content: append(append([]byte(nil), canonical...), []byte(` {}`)...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			recordPath := filepath.Join(directory, test.name+".json")
			if err := os.WriteFile(recordPath, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(directory, "index-r1.json")
			if _, err := AttachControls(ControlsAttachOptions{IndexPath: indexPath, EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:strict-controls", Output: output}); err == nil {
				t.Fatal("ambiguous preventive-controls record was accepted")
			}
			assertNotExist(t, output)
		})
	}

	recordPath, _ := writeTypedRecord(t, directory, "valid-controls.json", record)
	output := filepath.Join(directory, "index-r1.json")
	if err := os.WriteFile(output, []byte("sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = AttachControls(ControlsAttachOptions{IndexPath: indexPath, EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:no-clobber-controls", Output: output})
	if !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("AttachControls existing output error = %v", err)
	}
	if content := string(mustRead(t, output)); content != "sentinel\n" {
		t.Fatalf("existing output changed: %q", content)
	}
}

func TestAttachControlsReportsCommittedUnconfirmedPredecessorRace(t *testing.T) {
	directory := t.TempDir()
	index, err := NewIndex(controlsAttachCandidate(t), testTime)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
	indexBytes := mustRead(t, indexPath)
	recordPath, _ := writeTypedRecord(t, directory, "controls.json", controlsAttachRecordFixture(t, index.Candidate))
	output := filepath.Join(directory, "index-r1.json")

	result, attachErr := attachControls(ControlsAttachOptions{
		IndexPath: indexPath, EvidenceFile: recordPath,
		EvidenceRef: "urn:pgworkbench:evidence:race-controls", Output: output,
	}, controlsAttachHooks{beforePublication: func() {
		if err := os.Rename(indexPath, filepath.Join(directory, "parsed-predecessor.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(indexPath, indexBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}})
	var committed *CommittedError
	if !errors.As(attachErr, &committed) {
		t.Fatalf("AttachControls race error = %v, want committed-unconfirmed", attachErr)
	}
	if result.Output == "" || result.Digest == "" || committed.Result.Output != result.Output || committed.Result.Digest != result.Digest {
		t.Fatalf("committed result identity = result %+v error %+v", result, committed.Result)
	}
	if _, err := os.Lstat(output); err != nil {
		t.Fatalf("committed output is absent: %v", err)
	}
}

func controlsAttachRecordFixture(t *testing.T, candidate Candidate) PreventiveControlsVerification {
	t.Helper()
	draft := releaseAssetRecordFixture(t, candidate, releaseQualificationDraft)
	candidate = draft.Candidate
	run := draft.WorkflowRun
	run.Job = PreventiveControlsSealJob
	return PreventiveControlsVerification{
		SchemaVersion:          PreventiveControlsVerificationSchema,
		ArtifactType:           PreventiveControlsVerificationType,
		Candidate:              candidate,
		CapturedAt:             "2026-08-14T12:36:00Z",
		WorkflowRun:            run,
		DraftAssetVerification: draft,
		Source: PreventiveControlsVerificationSource{
			ControlsArtifact: QualificationArtifact{
				ID: "7654321", Name: "release-controls-" + candidate.Tag + "-" + candidate.GitCommit + "-2",
				Digest: "sha256:" + strings.Repeat("1", 64),
			},
			RepositoryControlsDigest:   "sha256:" + strings.Repeat("2", 64),
			TagRulesetAPIDigest:        "sha256:" + strings.Repeat("3", 64),
			ImmutableReleasesAPIDigest: "sha256:" + strings.Repeat("4", 64),
		},
		TagRuleset: PreventiveControlsTagRuleset{
			ID: 987654, UpdatedAt: "2026-08-14T12:34:57Z", Target: "tag", Enforcement: "active",
			IncludePattern: "refs/tags/v*", Excludes: []string{}, CreationRestricted: boolPointer(true),
			UpdateProhibited: boolPointer(true), DeletionProhibited: boolPointer(true),
		},
		BypassReview: PreventiveControlsBypassReview{
			Reviewer: "release-admin", ReviewedAt: "2026-08-14T12:34:58Z", RulesetID: 987654,
			RulesetUpdatedAt: "2026-08-14T12:34:57Z", EvidenceRef: "urn:pgworkbench:review:tag-ruleset",
			EvidenceDigest: "sha256:" + strings.Repeat("5", 64),
		},
		ImmutableReleases: PreventiveControlsImmutableReleases{Enabled: boolPointer(true), EnforcedByOwner: boolPointer(true)},
		Assurance: PreventiveControlsVerificationAssurance{
			Purpose: preventiveControlsAssurancePurpose, VerificationScope: preventiveControlsAssuranceVerificationScope,
			ActionsArtifactDurable: boolPointer(false), CandidateIdentityReverified: boolPointer(true),
			BypassReviewRemoteObjectFetched: boolPointer(false), BypassReviewSignatureVerified: boolPointer(false),
			PerformanceClaim: boolPointer(false), BenchmarkComparabilityClaim: boolPointer(false),
			RecoveryClaim: boolPointer(false), ProductionDecisionEligible: boolPointer(false),
		},
	}
}

func controlsAttachCandidate(t *testing.T) Candidate {
	t.Helper()
	candidate := openIndex(RecordStatusActive).Candidate
	inventory := releaseAssetInventoryFixture(t, candidate, releaseQualificationDraft)
	candidate.AssetFingerprint = inventory.AssetFingerprint
	return candidate
}
