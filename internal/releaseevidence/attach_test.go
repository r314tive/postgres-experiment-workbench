package releaseevidence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestAttachGateCreatesOneCandidateBoundRevision(t *testing.T) {
	directory := t.TempDir()
	indexPath, indexBytes, original := writeAttachIndex(t, directory, true)
	recordPath, recordBytes, record := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(original.Candidate), true)
	output := filepath.Join(directory, "index-r1.json")
	result, err := AttachGate(GateAttachOptions{
		IndexPath:    indexPath,
		Gate:         "draft_external_drivers",
		EvidenceFile: recordPath,
		EvidenceRef:  "https://evidence.example.test/releases/v0.3.0/external-drivers.json",
		Output:       output,
	})
	if err != nil {
		t.Fatal(err)
	}
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != absoluteOutput || result.Revision != 1 || result.Gate != "draft_external_drivers" || result.GateStatus != GateStatusPassed {
		t.Fatalf("attach result = %+v", result)
	}
	if result.PreviousIndexDigest != digestExactBytes(indexBytes) || result.EvidenceDigest != digestExactBytes(recordBytes) {
		t.Fatalf("exact byte digests not retained: %+v", result)
	}
	if result.EvidenceDurability != EvidenceDurabilityAsserted || result.EvidenceAuthenticity != EvidenceAuthenticityUnverified || result.RecordSchemaVersion != ExternalDriverVerificationSchema || result.RecordArtifactType != ExternalDriverVerificationType {
		t.Fatalf("record scope missing from result: %+v", result)
	}
	if !result.IndexVerification.Valid || result.IndexVerification.Status != StatusOpen || result.IndexVerification.Decision != DecisionNoGo || len(result.IndexVerification.OpenGates) != 15 || len(result.IndexVerification.PassedGates) != 1 {
		t.Fatalf("attached verification = %+v", result.IndexVerification)
	}

	attached, err := LoadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("release-evidence-index.schema.json", mustRead(t, output)); err != nil {
		t.Fatalf("attached v3 index does not conform to schema: %v", err)
	}
	if attached.Candidate != original.Candidate || attached.CreatedAt != original.CreatedAt || !reflect.DeepEqual(attached.PreventiveControls, original.PreventiveControls) {
		t.Fatal("attachment changed immutable candidate, creation time, or controls")
	}
	if attached.Lineage == nil || attached.Lineage.Revision != 1 || attached.Lineage.PreviousIndexDigest == nil || *attached.Lineage.PreviousIndexDigest != digestExactBytes(indexBytes) {
		t.Fatalf("attached lineage = %+v", attached.Lineage)
	}
	gate := attached.Gates.DraftExternalDrivers
	if gate.Status != GateStatusPassed || gate.Evidence == nil || gate.Evidence.Ref != "https://evidence.example.test/releases/v0.3.0/external-drivers.json" || gate.Evidence.Digest != digestExactBytes(recordBytes) || gate.Evidence.CapturedAt != record.CapturedAt || valueOrEmpty(gate.Evidence.RunID) != record.WorkflowRun.ID || gate.Evidence.RunAttempt == nil || *gate.Evidence.RunAttempt != record.WorkflowRun.Attempt {
		t.Fatalf("attached gate = %+v", gate)
	}
	if gate.Evidence.Assurance == nil || *gate.Evidence.Assurance != (EvidenceAssurance{Durability: EvidenceDurabilityAsserted, Authenticity: EvidenceAuthenticityUnverified}) {
		t.Fatalf("attached gate trust boundary = %+v", gate.Evidence.Assurance)
	}
	if gate.Evidence.Record == nil || *gate.Evidence.Record != (EvidenceRecord{SchemaVersion: ExternalDriverVerificationSchema, ArtifactType: ExternalDriverVerificationType}) {
		t.Fatalf("attached gate record contract = %+v", gate.Evidence.Record)
	}
	if attached.SchemaVersion != SchemaVersionV3 {
		t.Fatalf("attached schema version = %q, want %q", attached.SchemaVersion, SchemaVersionV3)
	}
	if !reflect.DeepEqual(result.IndexVerification.UnqualifiedEvidence, []string{"draft_external_drivers"}) || result.IndexVerification.AssuranceStatus != AssuranceOperatorAttested || result.IndexVerification.AuthorizationEligible {
		t.Fatalf("attached trust classification = %+v", result.IndexVerification)
	}
	if attached.RecordStatus != RecordStatusActive || attached.Decision.Status != DecisionNoGo || !sortStringsEqual(attached.Decision.Reasons, result.IndexVerification.Reasons) {
		t.Fatalf("derived lifecycle = status %q decision %+v", attached.RecordStatus, attached.Decision)
	}
	if !onlyGateChanged(original.Gates, attached.Gates, "draft_external_drivers") {
		t.Fatal("attachment changed a non-target gate")
	}
	assertFileBytes(t, indexPath, indexBytes)
	assertFileBytes(t, recordPath, recordBytes)
	if strings.Contains(string(mustRead(t, output)), recordPath) {
		t.Fatal("local evidence path leaked into the index")
	}
}

func TestAttachGateCannotAuthorizeGoWithOperatorAssertedRemoteEvidence(t *testing.T) {
	directory := t.TempDir()
	index := completeV2Index()
	index.RecordStatus = RecordStatusActive
	index.Gates.DraftExternalDrivers = Gate{Status: GateStatusOpen}
	index.Decision = Decision{
		Scope: DecisionScope, Status: DecisionNoGo, RecordedAt: testTime,
		Reasons: []string{"External-driver qualification remains open."},
	}
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
	recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
	output := filepath.Join(directory, "index-r1.json")

	result, err := AttachGate(GateAttachOptions{
		IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
		EvidenceRef: "urn:pgworkbench:evidence:last-gate", Output: output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IndexVerification.Valid || result.IndexVerification.Status != StatusOpen || result.IndexVerification.Decision != DecisionNoGo {
		t.Fatalf("operator-asserted last-gate evidence authorized the index: %+v", result.IndexVerification)
	}
	if len(result.IndexVerification.OpenGates) != 0 || len(result.IndexVerification.PassedGates) != 16 || len(result.IndexVerification.UnqualifiedEvidence) != 16 || result.IndexVerification.AssuranceStatus != AssuranceLegacyUnspecified || result.IndexVerification.AuthorizationEligible {
		t.Fatalf("last-gate assurance classification = %+v", result.IndexVerification)
	}
	attached, err := LoadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if attached.RecordStatus != RecordStatusActive || attached.Decision.Status != DecisionNoGo {
		t.Fatalf("last-gate lifecycle = status %q decision %+v", attached.RecordStatus, attached.Decision)
	}
	if len(attached.Decision.Reasons) != 16 || !contains(attached.Decision.Reasons, "unqualified evidence cannot authorize release: draft_external_drivers") {
		t.Fatalf("last-gate reasons = %v", attached.Decision.Reasons)
	}
}

func TestAttachGateMigratesV2ToV3WithoutRegressingDecisionTime(t *testing.T) {
	directory := t.TempDir()
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	index.Decision.RecordedAt = "2026-08-14T12:36:00Z"
	indexPath := filepath.Join(directory, "index-r0.json")
	writeIndex(t, indexPath, index)
	indexBytes := mustRead(t, indexPath)
	record := externalDriverRecordFixture(index.Candidate)
	record.CapturedAt = "2026-08-14T12:35:00Z"
	recordPath, _, _ := writeExternalDriverRecord(t, directory, record, true)
	output := filepath.Join(directory, "index-r1.json")

	if _, err := AttachGate(GateAttachOptions{
		IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
		EvidenceRef: "urn:pgworkbench:evidence:out-of-order", Output: output,
	}); err != nil {
		t.Fatal(err)
	}
	attached, err := LoadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Gates.DraftExternalDrivers.Evidence == nil || attached.Gates.DraftExternalDrivers.Evidence.CapturedAt != record.CapturedAt {
		t.Fatalf("evidence capture time was not preserved: %+v", attached.Gates.DraftExternalDrivers.Evidence)
	}
	if attached.Decision.RecordedAt != index.Decision.RecordedAt {
		t.Fatalf("decision time regressed from %q to %q", index.Decision.RecordedAt, attached.Decision.RecordedAt)
	}
	if attached.SchemaVersion != SchemaVersionV3 || attached.Lineage == nil || attached.Lineage.PreviousIndexDigest == nil || *attached.Lineage.PreviousIndexDigest != digestExactBytes(indexBytes) {
		t.Fatalf("v2 to v3 lineage migration = schema %q lineage %+v", attached.SchemaVersion, attached.Lineage)
	}
}

func TestAttachGateHashesExactPredecessorAndEvidenceBytes(t *testing.T) {
	makeRevision := func(t *testing.T, prettyIndex, prettyRecord bool) GateAttachResult {
		t.Helper()
		directory := t.TempDir()
		indexPath, _, index := writeAttachIndex(t, directory, prettyIndex)
		recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), prettyRecord)
		result, err := AttachGate(GateAttachOptions{
			IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
			EvidenceRef: "urn:pgworkbench:evidence:external-drivers", Output: filepath.Join(directory, "index-r1.json"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	prettyIndex := makeRevision(t, true, true)
	compactIndex := makeRevision(t, false, true)
	if prettyIndex.PreviousIndexDigest == compactIndex.PreviousIndexDigest {
		t.Fatal("predecessor whitespace was lost before hashing")
	}
	prettyRecord := makeRevision(t, true, true)
	compactRecord := makeRevision(t, true, false)
	if prettyRecord.EvidenceDigest == compactRecord.EvidenceDigest {
		t.Fatal("evidence whitespace was lost before hashing")
	}
}

func TestAttachGateAcceptsThirtyTwoDigitProviderIdentifiers(t *testing.T) {
	directory := t.TempDir()
	indexPath, _, index := writeAttachIndex(t, directory, true)
	record := externalDriverRecordFixture(index.Candidate)
	record.WorkflowRun.ID = "12345678901234567890123456789012"
	record.Source.ProviderArtifact.ID = "98765432109876543210987654321098"
	recordPath, _, _ := writeExternalDriverRecord(t, directory, record, true)
	if _, err := AttachGate(GateAttachOptions{
		IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
		EvidenceRef: "urn:pgworkbench:evidence:wide-provider-ids", Output: filepath.Join(directory, "index-r1.json"),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAttachGateRejectsRecordMutationsWithoutOutput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExternalDriverVerification)
		want   string
	}{
		{name: "schema", mutate: func(value *ExternalDriverVerification) { value.SchemaVersion += ".unknown" }, want: "unsupported typed gate record"},
		{name: "artifact type", mutate: func(value *ExternalDriverVerification) { value.ArtifactType = "pgworkbench.other" }, want: "unsupported typed gate record"},
		{name: "mode", mutate: func(value *ExternalDriverVerification) { value.QualificationMode = "published-release-smoke" }, want: "qualification_mode"},
		{name: "candidate version", mutate: func(value *ExternalDriverVerification) { value.Candidate.Version = "0.3.1" }, want: "candidate does not match"},
		{name: "candidate tag", mutate: func(value *ExternalDriverVerification) { value.Candidate.Tag = "v0.3.1" }, want: "candidate does not match"},
		{name: "candidate commit 40", mutate: func(value *ExternalDriverVerification) { value.Candidate.GitCommit = strings.Repeat("4", 40) }, want: "candidate does not match"},
		{name: "candidate commit 64", mutate: func(value *ExternalDriverVerification) { value.Candidate.GitCommit = strings.Repeat("4", 64) }, want: "candidate does not match"},
		{name: "candidate fingerprint", mutate: func(value *ExternalDriverVerification) { value.Candidate.AssetFingerprint = strings.Repeat("4", 64) }, want: "candidate does not match"},
		{name: "pack id", mutate: func(value *ExternalDriverVerification) { value.Candidate.ScenarioPack.ID = "other" }, want: "candidate does not match"},
		{name: "pack version", mutate: func(value *ExternalDriverVerification) { value.Candidate.ScenarioPack.Version = "0.3.1" }, want: "candidate does not match"},
		{name: "pack digest", mutate: func(value *ExternalDriverVerification) {
			value.Candidate.ScenarioPack.Digest = "sha256:" + strings.Repeat("4", 64)
		}, want: "candidate does not match"},
		{name: "captured offset", mutate: func(value *ExternalDriverVerification) { value.CapturedAt = "2026-08-14T15:35:00+03:00" }, want: "canonical UTC"},
		{name: "captured before predecessor", mutate: func(value *ExternalDriverVerification) { value.CapturedAt = "2026-08-14T12:34:55Z" }, want: "must not precede"},
		{name: "run id zero", mutate: func(value *ExternalDriverVerification) { value.WorkflowRun.ID = "0" }, want: "workflow_run.id"},
		{name: "run id too long", mutate: func(value *ExternalDriverVerification) { value.WorkflowRun.ID = strings.Repeat("9", 33) }, want: "workflow_run.id"},
		{name: "attempt zero", mutate: func(value *ExternalDriverVerification) { value.WorkflowRun.Attempt = 0 }, want: "workflow_run.attempt"},
		{name: "head sha", mutate: func(value *ExternalDriverVerification) { value.WorkflowRun.HeadSHA = strings.Repeat("4", 40) }, want: "head_sha"},
		{name: "repository", mutate: func(value *ExternalDriverVerification) { value.WorkflowRun.Repository = "fork/project" }, want: "repository"},
		{name: "bad digest", mutate: func(value *ExternalDriverVerification) { value.Source.GateDigest = strings.Repeat("a", 64) }, want: "gate_digest"},
		{name: "artifact id", mutate: func(value *ExternalDriverVerification) { value.Source.ProviderArtifact.ID = "01" }, want: "provider_artifact.id"},
		{name: "artifact id too long", mutate: func(value *ExternalDriverVerification) { value.Source.ProviderArtifact.ID = strings.Repeat("9", 33) }, want: "provider_artifact.id"},
		{name: "artifact name", mutate: func(value *ExternalDriverVerification) { value.Source.ProviderArtifact.Name += "-other" }, want: "provider_artifact.name"},
		{name: "artifact name too long", mutate: func(value *ExternalDriverVerification) { value.Source.ProviderArtifact.Name = strings.Repeat("a", 257) }, want: "1 to 256"},
		{name: "driver missing", mutate: func(value *ExternalDriverVerification) { value.Drivers = value.Drivers[:2] }, want: "exact ordered"},
		{name: "driver reordered", mutate: func(value *ExternalDriverVerification) {
			value.Drivers[0], value.Drivers[1] = value.Drivers[1], value.Drivers[0]
		}, want: "exact ordered"},
		{name: "performance claim", mutate: func(value *ExternalDriverVerification) { value.Assurance.PerformanceClaim = boolPointer(true) }, want: "assurance"},
		{name: "runtime closure", mutate: func(value *ExternalDriverVerification) {
			value.Assurance.DriverRuntimeClosureAttested = boolPointer(false)
		}, want: "assurance"},
		{name: "provider not reverified", mutate: func(value *ExternalDriverVerification) {
			value.Assurance.ProviderArtifactReverified = boolPointer(false)
		}, want: "assurance"},
		{name: "required false omitted", mutate: func(value *ExternalDriverVerification) { value.Assurance.PerformanceClaim = nil }, want: "assurance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			indexPath, _, index := writeAttachIndex(t, directory, true)
			record := externalDriverRecordFixture(index.Candidate)
			test.mutate(&record)
			recordPath, _, _ := writeExternalDriverRecord(t, directory, record, true)
			output := filepath.Join(directory, "index-r1.json")
			_, err := AttachGate(GateAttachOptions{
				IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
				EvidenceRef: "urn:pgworkbench:evidence:test", Output: output,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AttachGate error = %v, want %q", err, test.want)
			}
			assertNotExist(t, output)
		})
	}
}

func TestAttachGateRejectsAmbiguousAndUnsafeRecordFiles(t *testing.T) {
	directory := t.TempDir()
	indexPath, _, index := writeAttachIndex(t, directory, true)
	record := externalDriverRecordFixture(index.Candidate)
	canonical := marshalRecord(t, record, false)
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{name: "unknown", content: []byte(strings.Replace(string(canonical), `"artifact_type":`, `"unknown":true,"artifact_type":`, 1)), want: "unknown field"},
		{name: "duplicate", content: []byte(strings.Replace(string(canonical), `"artifact_type":`, `"artifact_type":"duplicate","artifact_type":`, 1)), want: "duplicate property"},
		{name: "null", content: []byte(strings.Replace(string(canonical), `"qualification_mode":"draft-release-smoke"`, `"qualification_mode":null`, 1)), want: "null is not allowed"},
		{name: "trailing", content: append(append([]byte(nil), canonical...), []byte(` {}`)...), want: "trailing JSON"},
		{name: "invalid utf8", content: append(append([]byte(nil), canonical...), 0xff), want: "valid UTF-8"},
		{name: "missing required false", content: []byte(strings.Replace(string(canonical), `"performance_claim":false,`, "", 1)), want: "assurance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recordPath := filepath.Join(directory, "record-"+strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(recordPath, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(directory, "index-r1.json")
			_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: output})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AttachGate error = %v, want %q", err, test.want)
			}
			assertNotExist(t, output)
		})
	}

	recordPath, _, _ := writeExternalDriverRecord(t, directory, record, true)
	symlink := filepath.Join(directory, "record-link.json")
	if err := os.Symlink(recordPath, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: symlink, EvidenceRef: "urn:pgworkbench:evidence:test", Output: filepath.Join(directory, "index-r1.json")})
	if err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("symlink evidence error = %v", err)
	}

	oversize := filepath.Join(directory, "record-oversize.json")
	if err := os.WriteFile(oversize, make([]byte, maxGateRecordBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: oversize, EvidenceRef: "urn:pgworkbench:evidence:test", Output: filepath.Join(directory, "index-r1.json")})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize evidence error = %v", err)
	}
}

func TestAttachGateRejectsInvalidPredecessorGateAndOutput(t *testing.T) {
	t.Run("v1", func(t *testing.T) {
		directory := t.TempDir()
		index := openIndex(RecordStatusActive)
		indexPath := filepath.Join(directory, "index-r0.json")
		writeIndex(t, indexPath, index)
		recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
		_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: filepath.Join(directory, "index-r1.json")})
		if err == nil || !strings.Contains(err.Error(), "requires a v2 or v3 predecessor") {
			t.Fatalf("v1 error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*Index)
		want   string
	}{
		{name: "template", mutate: func(value *Index) { value.RecordStatus = RecordStatusTemplate }, want: "requires an active predecessor"},
		{name: "complete", mutate: func(value *Index) {
			*value = completeV2Index()
		}, want: "requires an active predecessor"},
		{name: "max revision", mutate: func(value *Index) {
			value.Lineage.Revision = maxJSONSafeInteger
			previous := testDigest()
			value.Lineage.PreviousIndexDigest = &previous
		}, want: "cannot be incremented"},
		{name: "gate passed", mutate: func(value *Index) {
			value.Gates.DraftExternalDrivers = Gate{Status: GateStatusPassed, Evidence: typedExternalDriverEvidence("https://example.test/external.json")}
		}, want: "is not open"},
		{name: "gate failed", mutate: func(value *Index) {
			value.Gates.DraftExternalDrivers = Gate{Status: GateStatusFailed, Evidence: typedExternalDriverEvidence("https://example.test/external.json")}
		}, want: "is not open"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			_, _, index := writeAttachIndex(t, directory, true)
			test.mutate(&index)
			indexPath := filepath.Join(directory, "mutated.json")
			writeIndex(t, indexPath, index)
			recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
			_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: filepath.Join(directory, "index-r1.json")})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AttachGate error = %v, want %q", err, test.want)
			}
		})
	}

	t.Run("wrong gate assertion", func(t *testing.T) {
		directory := t.TempDir()
		indexPath, _, index := writeAttachIndex(t, directory, true)
		recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
		_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "publication", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: filepath.Join(directory, "index-r1.json")})
		if err == nil || !strings.Contains(err.Error(), "does not match typed adapter gate") {
			t.Fatalf("wrong gate error = %v", err)
		}
	})

	t.Run("noncanonical output", func(t *testing.T) {
		directory := t.TempDir()
		indexPath, _, index := writeAttachIndex(t, directory, true)
		recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
		_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: filepath.Join(directory, "fork-r1.json")})
		if err == nil || !strings.Contains(err.Error(), `output basename must be "index-r1.json"`) {
			t.Fatalf("noncanonical output error = %v", err)
		}
	})

	t.Run("noncanonical predecessor", func(t *testing.T) {
		directory := t.TempDir()
		_, _, index := writeAttachIndex(t, directory, true)
		indexPath := filepath.Join(directory, "copied-head.json")
		writeIndex(t, indexPath, index)
		recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
		_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: filepath.Join(directory, "index-r1.json")})
		if err == nil || !strings.Contains(err.Error(), `predecessor basename must be "index-r0.json"`) {
			t.Fatalf("noncanonical predecessor error = %v", err)
		}
	})

	t.Run("symlink predecessor", func(t *testing.T) {
		directory := t.TempDir()
		_, _, index := writeAttachIndex(t, directory, true)
		canonical := filepath.Join(directory, "index-r0.json")
		stored := filepath.Join(directory, "stored-index.json")
		if err := os.Rename(canonical, stored); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(stored, canonical); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
		output := filepath.Join(directory, "index-r1.json")
		_, err := AttachGate(GateAttachOptions{IndexPath: canonical, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:test", Output: output})
		if err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("symlink predecessor error = %v", err)
		}
		assertNotExist(t, output)
	})
}

func TestAttachGateRejectsNonDurableReference(t *testing.T) {
	invalid := []string{
		"relative.json",
		"http://example.test/evidence.json",
		"https://user@example.test/evidence.json",
		"https://example.test/evidence.json#fragment",
		"s3://bucket",
		"gs://bucket/",
		"https://github.com/r314tive/postgres-experiment-workbench/actions/runs/1/artifacts/2",
		"https://github.com:443/r314tive/postgres-experiment-workbench/actions/runs/1/artifacts/2",
		"https://github.com./r314tive/postgres-experiment-workbench/actions/runs/1/artifacts/2",
	}
	for _, ref := range invalid {
		t.Run(ref, func(t *testing.T) {
			directory := t.TempDir()
			indexPath, _, index := writeAttachIndex(t, directory, true)
			recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
			output := filepath.Join(directory, "index-r1.json")
			_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: ref, Output: output})
			if err == nil || !strings.Contains(err.Error(), "operator assertions") {
				t.Fatalf("ref %q error = %v", ref, err)
			}
			assertNotExist(t, output)
		})
	}
}

func TestAttachGateCanonicalOutputHasOneConcurrentWinner(t *testing.T) {
	directory := t.TempDir()
	indexPath, _, index := writeAttachIndex(t, directory, true)
	recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
	output := filepath.Join(directory, "index-r1.json")
	options := GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:concurrent", Output: output}

	const workers = 16
	start := make(chan struct{})
	errorsByWorker := make(chan error, workers)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := AttachGate(options)
			errorsByWorker <- err
		}()
	}
	close(start)
	group.Wait()
	close(errorsByWorker)
	successes := 0
	for err := range errorsByWorker {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, pathguard.ErrOutputExists) {
			t.Fatalf("concurrent loser error = %v, want ErrOutputExists", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent successes = %d, want 1", successes)
	}
	verification, err := VerifyFile(output)
	if err != nil || !verification.Valid {
		t.Fatalf("winner output invalid: %+v, %v", verification, err)
	}
}

func TestAttachGatePinsChainDirectoryAcrossPathReplacement(t *testing.T) {
	container := t.TempDir()
	directory := filepath.Join(container, "evidence")
	moved := filepath.Join(container, "evidence-moved")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	indexPath, indexBytes, index := writeAttachIndex(t, directory, true)
	recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
	output := filepath.Join(directory, "index-r1.json")

	result, err := attachGate(GateAttachOptions{
		IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
		EvidenceRef: "urn:pgworkbench:evidence:pinned-directory", Output: output,
	}, gateAttachHooks{beforePublication: func() {
		if renameErr := os.Rename(directory, moved); renameErr != nil {
			t.Fatal(renameErr)
		}
		if mkdirErr := os.Mkdir(directory, 0o755); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
	}})
	var committed *CommittedError
	if !errors.As(err, &committed) || !strings.Contains(err.Error(), "no longer identifies the pinned chain directory") {
		t.Fatalf("attach path-replacement error = %v, want committed-unconfirmed directory identity error", err)
	}
	if result.Digest == "" || committed.Result.Digest != result.Digest {
		t.Fatalf("committed result lost successor identity: result=%+v error=%+v", result, committed)
	}
	if verification, verifyErr := VerifyFile(filepath.Join(moved, "index-r1.json")); verifyErr != nil || !verification.Valid {
		t.Fatalf("pinned-directory successor = %+v, %v", verification, verifyErr)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("replacement directory received successor: %v", statErr)
	}
	assertFileBytes(t, filepath.Join(moved, "index-r0.json"), indexBytes)
}

func TestAttachGateReportsConfirmedOutputAliasAfterSameInodeRetarget(t *testing.T) {
	container := t.TempDir()
	original := filepath.Join(container, "evidence-original")
	moved := filepath.Join(container, "evidence-moved")
	alias := filepath.Join(container, "evidence-current")
	if err := os.Mkdir(original, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	indexPath, _, index := writeAttachIndex(t, alias, true)
	recordPath, _, _ := writeExternalDriverRecord(t, alias, externalDriverRecordFixture(index.Candidate), true)
	output := filepath.Join(alias, "index-r1.json")

	result, err := attachGate(GateAttachOptions{
		IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
		EvidenceRef: "urn:pgworkbench:evidence:retargeted-alias", Output: output,
	}, gateAttachHooks{beforePublication: func() {
		if renameErr := os.Rename(original, moved); renameErr != nil {
			t.Fatal(renameErr)
		}
		if unlinkErr := os.Remove(alias); unlinkErr != nil {
			t.Fatal(unlinkErr)
		}
		if symlinkErr := os.Symlink(moved, alias); symlinkErr != nil {
			t.Fatal(symlinkErr)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != absoluteOutput {
		t.Fatalf("reported output = %q, want confirmed caller path %q", result.Output, absoluteOutput)
	}
	if verification, verifyErr := VerifyFile(result.Output); verifyErr != nil || !verification.Valid {
		t.Fatalf("retargeted output = %+v, %v", verification, verifyErr)
	}
}

func externalDriverRecordFixture(candidate Candidate) ExternalDriverVerification {
	return ExternalDriverVerification{
		SchemaVersion:     ExternalDriverVerificationSchema,
		ArtifactType:      ExternalDriverVerificationType,
		QualificationMode: externalDriverQualificationMode,
		Candidate:         candidate,
		CapturedAt:        "2026-08-14T12:35:00Z",
		WorkflowRun: ExternalDriverWorkflowRun{
			ID: "123456789", Attempt: 2, HeadSHA: candidate.GitCommit, Repository: externalDriverRepository,
		},
		Source: ExternalDriverVerificationSource{
			GateDigest:            "sha256:" + strings.Repeat("a", 64),
			MetadataArchiveDigest: "sha256:" + strings.Repeat("b", 64),
			ProviderArtifact: ExternalDriverProviderArtifact{
				ID: "987654321", Name: "draft-external-driver-metadata-" + candidate.Tag + "-" + candidate.GitCommit + "-2", Digest: "sha256:" + strings.Repeat("c", 64),
			},
			ReleaseArchiveDigest:  "sha256:" + strings.Repeat("d", 64),
			ReleaseManifestDigest: "sha256:" + strings.Repeat("e", 64),
		},
		Drivers: append([]string(nil), externalDriverIDs...),
		Assurance: ExternalDriverAssurance{
			Purpose:                              "adapter-compatibility-release-smoke",
			ArtifactPayload:                      "metadata-only-no-third-party-runtime-bytes",
			VerificationScope:                    "workflow-local-content-and-semantics",
			ThirdPartyRuntimeBytesUploaded:       boolPointer(false),
			PerformanceClaim:                     boolPointer(false),
			ProductionDecisionEligible:           boolPointer(false),
			SourceToBinaryAttested:               boolPointer(false),
			DriverRuntimeClosureAttested:         boolPointer(true),
			HostRuntimeDependenciesAttested:      boolPointer(false),
			BenchmarkComparabilityClaim:          boolPointer(false),
			ProjectRedistribution:                boolPointer(false),
			AllExecutionsLocallyVerified:         boolPointer(true),
			ExactSourceToStagedFileMatch:         boolPointer(true),
			DisposableLoopbackTargetAcknowledged: boolPointer(true),
			SystemDatabasesDenied:                boolPointer(true),
			CandidateIdentityReverified:          boolPointer(true),
			ProviderArtifactReverified:           boolPointer(true),
			ReleaseArchiveProvenanceVerified:     boolPointer(true),
			ReleaseManifestProvenanceVerified:    boolPointer(true),
		},
	}
}

func writeAttachIndex(t *testing.T, directory string, pretty bool) (string, []byte, Index) {
	t.Helper()
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV3
	index.Lineage = &Lineage{Revision: 0}
	content := marshalRecord(t, index, pretty)
	path := filepath.Join(directory, "index-r0.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content, index
}

func writeExternalDriverRecord(t *testing.T, directory string, record ExternalDriverVerification, pretty bool) (string, []byte, ExternalDriverVerification) {
	t.Helper()
	content := marshalRecord(t, record, pretty)
	path := filepath.Join(directory, "external-driver-verification.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content, record
}

func marshalRecord(t *testing.T, value any, pretty bool) []byte {
	t.Helper()
	var content []byte
	var err error
	if pretty {
		content, err = json.MarshalIndent(value, "", "  ")
	} else {
		content, err = json.Marshal(value)
	}
	if err != nil {
		t.Fatal(err)
	}
	return append(content, '\n')
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func assertFileBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	if actual := mustRead(t, path); string(actual) != string(expected) {
		t.Fatalf("file %s changed", path)
	}
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("output %s exists after rejected attachment: %v", path, err)
	}
}

func sortStringsEqual(left, right []string) bool {
	return reflect.DeepEqual(left, right)
}
