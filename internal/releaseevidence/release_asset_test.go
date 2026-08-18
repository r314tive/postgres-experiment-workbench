package releaseevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/releaseassets"
	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestAttachReleaseBoundaryRecordsCreatesThreeUnqualifiedRevisions(t *testing.T) {
	directory := t.TempDir()
	index, draftRecord := releaseAssetAttachFixture(t, releaseQualificationDraft)
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
	indexBytes := mustRead(t, indexPath)

	draftPath, draftBytes := writeTypedRecord(t, directory, "draft-asset-verification.json", draftRecord)
	draftResult, err := AttachGate(GateAttachOptions{
		IndexPath: indexPath, Gate: "draft_asset_verification", EvidenceFile: draftPath,
		EvidenceRef: "urn:pgworkbench:evidence:draft-assets", Output: filepath.Join(directory, "index-r1.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if draftResult.PreviousIndexDigest != digestExactBytes(indexBytes) || draftResult.EvidenceDigest != digestExactBytes(draftBytes) || draftResult.GateStatus != GateStatusPassed || draftResult.RecordAdapter != ReleaseAssetDraftAdapter {
		t.Fatalf("draft attachment result = %+v", draftResult)
	}

	publicRecord := releaseAssetRecordFixture(t, index.Candidate, releaseQualificationPublished)
	publicationRecord := releasePublicationRecordFixture(publicRecord)
	publicationPath, publicationBytes := writeTypedRecord(t, directory, "publication-verification.json", publicationRecord)
	publicationResult, err := AttachGate(GateAttachOptions{
		IndexPath: filepath.Join(directory, "index-r1.json"), Gate: "publication", EvidenceFile: publicationPath,
		EvidenceRef: "urn:pgworkbench:evidence:publication", Output: filepath.Join(directory, "index-r2.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if publicationResult.EvidenceDigest != digestExactBytes(publicationBytes) || publicationResult.RecordSchemaVersion != ReleasePublicationSchema || publicationResult.RecordArtifactType != ReleasePublicationType || publicationResult.RecordAdapter != ReleasePublicationAdapter {
		t.Fatalf("publication attachment result = %+v", publicationResult)
	}

	publicPath, publicBytes := writeTypedRecord(t, directory, "public-asset-verification.json", publicRecord)
	publicResult, err := AttachGate(GateAttachOptions{
		IndexPath: filepath.Join(directory, "index-r2.json"), Gate: "public_asset_verification", EvidenceFile: publicPath,
		EvidenceRef: "urn:pgworkbench:evidence:public-assets", Output: filepath.Join(directory, "index-r3.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if publicResult.EvidenceDigest != digestExactBytes(publicBytes) || publicResult.RecordSchemaVersion != ReleaseAssetVerificationSchema || publicResult.RecordArtifactType != ReleaseAssetVerificationType || publicResult.RecordAdapter != ReleaseAssetPublishedAdapter {
		t.Fatalf("public attachment result = %+v", publicResult)
	}

	attached, err := LoadFile(filepath.Join(directory, "index-r3.json"))
	if err != nil {
		t.Fatal(err)
	}
	if attached.Lineage == nil || attached.Lineage.Revision != 3 || attached.Candidate != index.Candidate || !reflect.DeepEqual(attached.PreventiveControls, index.PreventiveControls) {
		t.Fatalf("final chain identity = lineage %+v candidate %+v", attached.Lineage, attached.Candidate)
	}
	for name, gate := range map[string]Gate{
		"draft_asset_verification":  attached.Gates.DraftAssetVerification,
		"publication":               attached.Gates.Publication,
		"public_asset_verification": attached.Gates.PublicAssetVerification,
	} {
		if gate.Status != GateStatusPassed || gate.Evidence == nil || gate.Evidence.Assurance == nil || gate.Evidence.Record == nil {
			t.Fatalf("%s gate = %+v", name, gate)
		}
		if gate.Evidence.Assurance.Durability != EvidenceDurabilityAsserted || gate.Evidence.Assurance.Authenticity != EvidenceAuthenticityUnverified {
			t.Fatalf("%s assurance = %+v", name, gate.Evidence.Assurance)
		}
	}
	verification := Verify(attached)
	if !verification.Valid || verification.Status != StatusOpen || verification.Decision != DecisionNoGo || verification.AuthorizationEligible || verification.AssuranceStatus != AssuranceOperatorAttested {
		t.Fatalf("final release boundary verification = %+v", verification)
	}
	if len(verification.PassedGates) != 3 || len(verification.OpenGates) != 13 || !reflect.DeepEqual(verification.UnqualifiedEvidence, []string{"draft_asset_verification", "public_asset_verification", "publication"}) {
		t.Fatalf("final requirement classification = %+v", verification)
	}
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("release-evidence-index.schema.json", mustRead(t, filepath.Join(directory, "index-r3.json"))); err != nil {
		t.Fatalf("final release-boundary index does not conform to schema: %v", err)
	}
	assertFileBytes(t, indexPath, indexBytes)

	attached.Gates.DraftAssetVerification.Evidence.Record.Adapter = ReleaseAssetPublishedAdapter
	if swapped := Verify(attached); swapped.Valid || !sliceContainsSubstring(swapped.Issues, ".adapter") {
		t.Fatalf("persisted draft/public adapter swap was accepted: %+v", swapped)
	}
}

func TestReleaseBoundaryRecordsConformToSchemasAndForbidOutcomeInjection(t *testing.T) {
	index, draft := releaseAssetAttachFixture(t, releaseQualificationDraft)
	public := releaseAssetRecordFixture(t, index.Candidate, releaseQualificationPublished)
	publication := releasePublicationRecordFixture(public)
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		schema string
		value  any
	}{
		{name: "draft", schema: "release-asset-verification.schema.json", value: draft},
		{name: "published", schema: "release-asset-verification.schema.json", value: public},
		{name: "publication", schema: "release-publication-verification.schema.json", value: publication},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := marshalRecord(t, test.value, true)
			if err := registry.ValidateJSON(test.schema, content); err != nil {
				t.Fatalf("canonical record rejected: %v", err)
			}
			injected := strings.Replace(string(content), `"artifact_type":`, `"status":"passed","artifact_type":`, 1)
			if err := registry.ValidateJSON(test.schema, []byte(injected)); err == nil {
				t.Fatal("schema accepted a caller-selected outcome")
			}
		})
	}
}

func TestAttachReleaseAssetRejectsSemanticMutationsWithoutOutput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReleaseAssetVerification)
		want   string
	}{
		{name: "candidate", mutate: func(value *ReleaseAssetVerification) { value.Candidate.Version = "0.3.1" }, want: "candidate does not match"},
		{name: "mode replay", mutate: func(value *ReleaseAssetVerification) { value.QualificationMode = releaseQualificationPublished }, want: "workflow_run"},
		{name: "workflow job", mutate: func(value *ReleaseAssetVerification) { value.WorkflowRun.Job = releasePublicVerificationJob }, want: "workflow_run"},
		{name: "workflow ref", mutate: func(value *ReleaseAssetVerification) { value.WorkflowRun.Ref = "refs/tags/v9.9.9" }, want: "workflow_run"},
		{name: "early capture", mutate: func(value *ReleaseAssetVerification) { value.CapturedAt = testTime }, want: "must not precede"},
		{name: "provider state", mutate: func(value *ReleaseAssetVerification) {
			value.ProviderObservation.ReleaseState = releaseQualificationPublished
		}, want: "provider_observation"},
		{name: "draft immutable claim", mutate: func(value *ReleaseAssetVerification) { value.ProviderObservation.IsImmutable = boolPointer(true) }, want: "must be absent"},
		{name: "manifest digest", mutate: func(value *ReleaseAssetVerification) {
			value.Source.ReleaseManifestDigest = "sha256:" + strings.Repeat("f", 64)
		}, want: "does not match"},
		{name: "draft release attestation", mutate: func(value *ReleaseAssetVerification) { value.Source.ReleaseAttestationDigest = pointer(testDigest()) }, want: "must be absent"},
		{name: "check", mutate: func(value *ReleaseAssetVerification) { value.Checks.SBOMContents = "unchecked" }, want: "checks"},
		{name: "claim", mutate: func(value *ReleaseAssetVerification) { value.Assurance.PerformanceClaim = boolPointer(true) }, want: "assurance"},
		{name: "missing asset", mutate: func(value *ReleaseAssetVerification) { value.Inventory.Assets = value.Inventory.Assets[:15] }, want: "inventory is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			index, record := releaseAssetAttachFixture(t, releaseQualificationDraft)
			indexPath := filepath.Join(directory, "index-r0.json")
			writeIndex(t, indexPath, index)
			test.mutate(&record)
			recordPath, _ := writeTypedRecord(t, directory, "record.json", record)
			output := filepath.Join(directory, "index-r1.json")
			_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_asset_verification", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: output})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AttachGate error = %v, want %q", err, test.want)
			}
			assertNotExist(t, output)
		})
	}
}

func TestAttachReleasePublicationRejectsMutationsAndWrongGate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReleasePublicationVerification)
		want   string
	}{
		{name: "candidate", mutate: func(value *ReleasePublicationVerification) { value.Candidate.Tag = "v9.9.9" }, want: "candidate does not match"},
		{name: "mutating job", mutate: func(value *ReleasePublicationVerification) { value.WorkflowRun.Job = "publish-release" }, want: "workflow_run"},
		{name: "nested mode", mutate: func(value *ReleasePublicationVerification) {
			value.PublicAssetVerification.QualificationMode = releaseQualificationDraft
		}, want: "workflow_run"},
		{name: "nested workflow", mutate: func(value *ReleasePublicationVerification) {
			value.PublicAssetVerification.WorkflowRun.ID = "987654321"
		}, want: "same workflow identity"},
		{name: "not post publication", mutate: func(value *ReleasePublicationVerification) {
			value.Observation.PostPublicationObservation = boolPointer(false)
		}, want: "observation"},
		{name: "verifier mutation", mutate: func(value *ReleasePublicationVerification) {
			value.Observation.MutationPerformedByVerifier = boolPointer(true)
		}, want: "observation"},
		{name: "immutable false", mutate: func(value *ReleasePublicationVerification) { value.Observation.IsImmutable = boolPointer(false) }, want: "observation"},
		{name: "claim", mutate: func(value *ReleasePublicationVerification) {
			value.Assurance.ProductionDecisionEligible = boolPointer(true)
		}, want: "assurance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			index, _ := releaseAssetAttachFixture(t, releaseQualificationDraft)
			indexPath := filepath.Join(directory, "index-r0.json")
			writeIndex(t, indexPath, index)
			record := releasePublicationRecordFixture(releaseAssetRecordFixture(t, index.Candidate, releaseQualificationPublished))
			test.mutate(&record)
			recordPath, _ := writeTypedRecord(t, directory, "publication.json", record)
			output := filepath.Join(directory, "index-r1.json")
			_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "publication", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:publication", Output: output})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AttachGate error = %v, want %q", err, test.want)
			}
			assertNotExist(t, output)
		})
	}

	directory := t.TempDir()
	index, record := releaseAssetAttachFixture(t, releaseQualificationDraft)
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
	recordPath, _ := writeTypedRecord(t, directory, "draft.json", record)
	_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "public_asset_verification", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:wrong-gate", Output: filepath.Join(directory, "index-r1.json")})
	if err == nil || !strings.Contains(err.Error(), "does not match typed adapter gate") {
		t.Fatalf("wrong gate error = %v", err)
	}
}

func releaseAssetAttachFixture(t *testing.T, mode string) (Index, ReleaseAssetVerification) {
	t.Helper()
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV3
	index.Lineage = &Lineage{Revision: 0}
	record := releaseAssetRecordFixture(t, index.Candidate, mode)
	index.Candidate = record.Candidate
	return index, record
}

func releaseAssetRecordFixture(t *testing.T, candidate Candidate, mode string) ReleaseAssetVerification {
	t.Helper()
	inventory := releaseAssetInventoryFixture(t, candidate, mode)
	candidate.AssetFingerprint = inventory.AssetFingerprint
	inventory.AssetFingerprint = candidate.AssetFingerprint
	manifestDigest := inventoryAssetDigest(inventory, "pgworkbench-"+candidate.Version+"-release-manifest.json")
	wantDraft := mode == releaseQualificationDraft
	job := releaseDraftVerificationJob
	publicOnly := "not-applicable"
	var immutable *bool
	var attestationDigest *string
	if !wantDraft {
		job = releasePublicVerificationJob
		publicOnly = "verified"
		immutable = boolPointer(true)
		attestationDigest = pointer("sha256:" + strings.Repeat("e", 64))
	}
	return ReleaseAssetVerification{
		SchemaVersion:     ReleaseAssetVerificationSchema,
		ArtifactType:      ReleaseAssetVerificationType,
		QualificationMode: mode,
		Candidate:         candidate,
		CapturedAt:        "2026-08-14T12:35:00Z",
		WorkflowRun: ReleaseVerificationWorkflowRun{
			ID: "123456789", Attempt: 2, HeadSHA: candidate.GitCommit,
			Repository: releaseWorkflowRepository, Workflow: releaseWorkflowName,
			Job: job, Ref: "refs/tags/" + candidate.Tag,
		},
		Inventory: inventory,
		ProviderObservation: ReleaseAssetProviderObservation{
			Tag: candidate.Tag, TagTargetSHA: candidate.GitCommit, ReleaseState: mode,
			IsDraft: boolPointer(wantDraft), IsImmutable: immutable, AssetCount: 16,
			AssetFingerprint: candidate.AssetFingerprint,
		},
		Source: ReleaseAssetVerificationSource{
			AssetInventoryDigest:  "sha256:" + strings.Repeat("d", 64),
			ReleaseManifestDigest: manifestDigest, ReleaseAttestationDigest: attestationDigest,
		},
		Checks: verifiedReleaseAssetChecks(publicOnly),
		Assurance: ReleaseAssetVerificationAssurance{
			Purpose: "release-asset-authenticity-and-integrity", VerificationScope: "workflow-local-provider-and-content",
			ActionsArtifactDurable: boolPointer(false), CandidateIdentityReverified: boolPointer(true),
			ProviderAssetSetRecomputed: boolPointer(true), AllDownloadedBytesVerified: boolPointer(true),
			PerformanceClaim: boolPointer(false), BenchmarkComparabilityClaim: boolPointer(false),
			RecoveryClaim: boolPointer(false), ProductionDecisionEligible: boolPointer(false),
		},
	}
}

func releasePublicationRecordFixture(public ReleaseAssetVerification) ReleasePublicationVerification {
	return ReleasePublicationVerification{
		SchemaVersion: ReleasePublicationSchema, ArtifactType: ReleasePublicationType,
		Candidate: public.Candidate, CapturedAt: public.CapturedAt, WorkflowRun: public.WorkflowRun,
		PublicAssetVerification: public,
		Observation: ReleasePublicationObservation{
			PostPublicationObservation: boolPointer(true), MutationPerformedByVerifier: boolPointer(false),
			DraftPublicFingerprintEqual: boolPointer(true), ReleaseState: releaseQualificationPublished,
			IsDraft: boolPointer(false), IsImmutable: boolPointer(true), TagTargetSHA: public.Candidate.GitCommit,
			AssetCount: 16, AssetFingerprint: public.Candidate.AssetFingerprint, ReleaseAttestation: "verified",
		},
		Assurance: ReleasePublicationAssurance{
			Purpose: "post-publication-read-only-observation", VerificationScope: "workflow-local-provider-and-content",
			ActionsArtifactDurable: boolPointer(false), CandidateIdentityReverified: boolPointer(true),
			PerformanceClaim: boolPointer(false), BenchmarkComparabilityClaim: boolPointer(false),
			RecoveryClaim: boolPointer(false), ProductionDecisionEligible: boolPointer(false),
		},
	}
}

func releaseAssetInventoryFixture(t *testing.T, candidate Candidate, mode string) releaseassets.Inventory {
	t.Helper()
	platforms := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"}
	names := make([]string, 0, 16)
	for _, platform := range platforms {
		prefix := "pgworkbench-" + candidate.Version + "-" + platform
		names = append(names, prefix+".tar.gz", prefix+".spdx.json", prefix+"-sbom.sigstore.json")
	}
	names = append(names,
		"pgworkbench-"+candidate.Version+"-SHA256SUMS.txt",
		"pgworkbench-"+candidate.Version+"-METADATA-SHA256SUMS.txt",
		"pgworkbench-"+candidate.Version+"-release-manifest.json",
		"pgworkbench-"+candidate.Version+"-provenance.sigstore.json",
	)
	sort.Strings(names)
	assets := make([]releaseassets.Asset, len(names))
	for index, name := range names {
		id, err := releaseassets.NewIntegerAssetID(uint64(index + 1))
		if err != nil {
			t.Fatal(err)
		}
		assets[index] = releaseassets.Asset{
			ID: id, Name: name, Size: int64(index + 1),
			Digest: fmt.Sprintf("sha256:%064x", index+1),
		}
	}
	fingerprint, err := releaseassets.ComputeFingerprint(assets)
	if err != nil {
		t.Fatal(err)
	}
	return releaseassets.Inventory{
		SchemaVersion: releaseassets.SchemaVersion, ArtifactType: releaseassets.ArtifactType,
		ReleaseState: mode, Tag: candidate.Tag, GitCommit: candidate.GitCommit,
		CapturedAt: "2026-08-14T12:34:58Z", FingerprintAlgorithm: releaseassets.FingerprintAlgorithm,
		AssetFingerprint: fingerprint, Assets: assets,
	}
}

func writeTypedRecord(t *testing.T, directory, name string, value any) (string, []byte) {
	t.Helper()
	content := marshalRecord(t, value, true)
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content
}

func TestReleaseBoundaryStrictJSONRejectsUnknownDuplicateAndTrailing(t *testing.T) {
	directory := t.TempDir()
	index, record := releaseAssetAttachFixture(t, releaseQualificationDraft)
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
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
			path := filepath.Join(directory, test.name+".json")
			if err := os.WriteFile(path, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(directory, "index-r1.json")
			_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_asset_verification", EvidenceFile: path, EvidenceRef: "urn:pgworkbench:evidence:strict", Output: output})
			if err == nil {
				t.Fatal("ambiguous JSON was accepted")
			}
			assertNotExist(t, output)
		})
	}
}
