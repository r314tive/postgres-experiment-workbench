package releaseevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

const testTime = "2026-08-14T12:34:56Z"

func TestRepositoryTemplateIsAConsistentOpenNoGoIndex(t *testing.T) {
	verification, err := VerifyFile(filepath.Join("..", "..", "evidence", "templates", "release-evidence-index.json"))
	if err != nil {
		t.Fatalf("VerifyFile(template): %v", err)
	}
	if !verification.Valid {
		t.Fatalf("template verification issues: %v", verification.Issues)
	}
	if verification.Status != StatusOpen || verification.Decision != DecisionNoGo {
		t.Fatalf("template result = status %q decision %q", verification.Status, verification.Decision)
	}
	if len(verification.OpenGates) != 16 {
		t.Fatalf("open requirements = %d, want 16: %v", len(verification.OpenGates), verification.OpenGates)
	}
	if len(verification.FailedGates) != 0 {
		t.Fatalf("failed requirements = %v, want none", verification.FailedGates)
	}
	if len(verification.PassedGates) != 0 {
		t.Fatalf("passed requirements = %v, want none", verification.PassedGates)
	}
	if len(verification.Reasons) != 16 || !sliceContainsSubstring(verification.Reasons, "open readiness requirement: source_compatibility") {
		t.Fatalf("derived template reasons = %v", verification.Reasons)
	}
}

func TestLegacyCompleteGoRemainsReadableButIsNotReleaseAuthorization(t *testing.T) {
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	for name, index := range map[string]Index{"v1": completeIndex(), "v2": completeV2Index()} {
		t.Run(name, func(t *testing.T) {
			verification := Verify(index)
			if !verification.Valid || verification.Status != StatusOpen || verification.Decision != DecisionNoGo {
				t.Fatalf("legacy complete index effective result = %+v", verification)
			}
			if verification.RecordedDecision != DecisionGo || verification.ReadinessStatus != StatusPassed || verification.ReadinessDecision != DecisionGo {
				t.Fatalf("legacy lifecycle was not preserved separately: %+v", verification)
			}
			if verification.AssuranceStatus != AssuranceLegacyUnspecified || verification.AuthorizationEligible || len(verification.UnqualifiedEvidence) != 16 || len(verification.Warnings) != 16 {
				t.Fatalf("legacy trust classification = %+v", verification)
			}
			if len(verification.OpenGates) != 0 || len(verification.FailedGates) != 0 || len(verification.PassedGates) != 16 {
				t.Fatalf("legacy readiness requirements = %+v", verification)
			}
			if err := registry.ValidateJSON("release-evidence-index.schema.json", []byte(marshalIndex(t, index))); err != nil {
				t.Fatalf("legacy index no longer conforms to schema: %v", err)
			}
		})
	}
}

func TestV3StoresOnlyNoGoUntilAProofBackedAssuranceClassExists(t *testing.T) {
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	index := completeV3NoGoIndex()
	verification := Verify(index)
	if !verification.Valid || verification.Status != StatusOpen || verification.Decision != DecisionNoGo || verification.ReadinessDecision != DecisionGo || verification.AssuranceStatus != AssuranceLegacyUnspecified || len(verification.UnqualifiedEvidence) != 16 {
		t.Fatalf("v3 unqualified no-go = %+v", verification)
	}
	for _, name := range []string{
		"preventive_controls.tag_ruleset",
		"preventive_controls.tag_ruleset.bypass_review",
		"preventive_controls.immutable_releases",
	} {
		if !contains(verification.UnqualifiedEvidence, name) {
			t.Fatalf("preventive-control evidence escaped assurance evaluation: %+v", verification)
		}
	}
	if err := registry.ValidateJSON("release-evidence-index.schema.json", []byte(marshalIndex(t, index))); err != nil {
		t.Fatalf("valid v3 no-go does not conform to schema: %v", err)
	}
	index.RecordStatus = RecordStatusComplete
	index.Decision.Status = DecisionGo
	if verification := Verify(index); verification.Valid || verification.Decision != DecisionNoGo {
		t.Fatalf("v3 self-declared GO was accepted: %+v", verification)
	}
	if err := registry.ValidateJSON("release-evidence-index.schema.json", []byte(marshalIndex(t, index))); err == nil {
		t.Fatal("release evidence schema accepted v3 GO before a proof-backed assurance class exists")
	}
}

func TestEvidenceRecordAndAssuranceArePairedAndClosed(t *testing.T) {
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Evidence)
	}{
		{name: "record without assurance", mutate: func(value *Evidence) { value.Assurance = nil }},
		{name: "assurance without record", mutate: func(value *Evidence) { value.Record = nil }},
		{name: "self declared verified", mutate: func(value *Evidence) {
			value.Assurance.Durability = "durable-object-presence-and-digest-verified"
			value.Assurance.Authenticity = "producer-and-remote-object-authenticated"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			index := openIndex(RecordStatusActive)
			index.SchemaVersion = SchemaVersionV3
			index.Lineage = &Lineage{Revision: 0}
			index.Gates.DraftExternalDrivers = Gate{Status: GateStatusPassed, Evidence: typedExternalDriverEvidence("https://example.test/external.json")}
			test.mutate(index.Gates.DraftExternalDrivers.Evidence)
			verification := Verify(index)
			if verification.Valid || !sliceContainsSubstring(verification.Issues, "assurance") {
				t.Fatalf("invalid persisted trust pair accepted: %+v", verification)
			}
			if err := registry.ValidateJSON("release-evidence-index.schema.json", []byte(marshalIndex(t, index))); err == nil {
				t.Fatal("release evidence schema accepted invalid persisted trust pair")
			}
		})
	}
}

func TestScenarioPackVersionIsIndependentlyBound(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.Candidate.ScenarioPack.Version = "7.4.1"
	verification := Verify(index)
	if !verification.Valid {
		t.Fatalf("independent scenario pack version issues: %v", verification.Issues)
	}
}

func TestTemplateSentinelCannotAuthorizeActiveOrCompleteRecord(t *testing.T) {
	for _, recordStatus := range []string{RecordStatusActive, RecordStatusComplete} {
		t.Run(recordStatus, func(t *testing.T) {
			var index Index
			if recordStatus == RecordStatusComplete {
				index = completeIndex()
			} else {
				index = openIndex(recordStatus)
			}
			index.Candidate.GitCommit = strings.Repeat("1", 40)
			index.Candidate.AssetFingerprint = strings.Repeat("1", 64)
			index.Candidate.ScenarioPack.Digest = "sha256:" + strings.Repeat("1", 64)

			verification := Verify(index)
			if verification.Valid || verification.Status != StatusFailed || verification.Decision != DecisionNoGo {
				t.Fatalf("template sentinel was not rejected fail-closed: %+v", verification)
			}
			if !sliceContainsSubstring(verification.Issues, "template sentinel") {
				t.Fatalf("sentinel issue missing: %v", verification.Issues)
			}
		})
	}
}

func TestValidFixturesConformToRepositorySchema(t *testing.T) {
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatalf("CompileDir: %v", err)
	}
	for name, index := range map[string]Index{
		"open":     openIndex(RecordStatusActive),
		"complete": completeIndex(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := registry.ValidateJSON("release-evidence-index.schema.json", []byte(marshalIndex(t, index))); err != nil {
				t.Fatalf("fixture does not conform to repository schema: %v", err)
			}
		})
	}
}

func TestDateTimeValidationMatchesRepositorySchema(t *testing.T) {
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatalf("CompileDir: %v", err)
	}
	tests := []struct {
		value string
		valid bool
	}{
		{value: "2026-08-14t12:34:56z", valid: true},
		{value: "1990-12-31T23:59:60Z", valid: true},
		{value: "1937-01-01T12:00:27.123456789012+00:20", valid: true},
		{value: "2026-08-14T12:34:56+24:00", valid: false},
		{value: "2026-08-14T12:34:56+00:60", valid: false},
		{value: "2026-08-14T12:34:56,1Z", valid: false},
		{value: "2026-08-14T12:00:60Z", valid: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			index := openIndex(RecordStatusActive)
			index.CreatedAt = test.value
			index.Decision.RecordedAt = test.value
			content := []byte(marshalIndex(t, index))
			schemaValid := registry.ValidateJSON("release-evidence-index.schema.json", content) == nil
			verificationValid := Verify(index).Valid
			if schemaValid != test.valid || verificationValid != test.valid {
				t.Fatalf("validity mismatch: schema=%v verifier=%v want=%v", schemaValid, verificationValid, test.valid)
			}
		})
	}
}

func TestVerifyConsistentFailedNoGoIndex(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.Gates.SourceCompatibility = Gate{Status: GateStatusFailed, Evidence: evidence("https://example.test/source-compatibility.json")}

	verification := Verify(index)
	if !verification.Valid {
		t.Fatalf("failed no-go index should be internally valid: %v", verification.Issues)
	}
	if verification.Status != StatusFailed || verification.Decision != DecisionNoGo {
		t.Fatalf("failed result = status %q decision %q", verification.Status, verification.Decision)
	}
	if !contains(verification.FailedGates, "source_compatibility") {
		t.Fatalf("failed requirements do not contain source_compatibility: %v", verification.FailedGates)
	}
	if !contains(verification.Reasons, "failed readiness requirement: source_compatibility") {
		t.Fatalf("derived failed reasons = %v", verification.Reasons)
	}
}

func TestStoredDecisionReasonsAreNonAuthoritative(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.Decision.Reasons = []string{"Everything is ready."}
	verification := Verify(index)
	if !verification.Valid || verification.Decision != DecisionNoGo {
		t.Fatalf("stored free-text changed verification: %+v", verification)
	}
	if contains(verification.Reasons, "Everything is ready.") || !sliceContainsSubstring(verification.Reasons, "open readiness requirement:") {
		t.Fatalf("reasons were not independently derived: %v", verification.Reasons)
	}
}

func TestInconsistentPassedIndexNeverAuthorizesGo(t *testing.T) {
	index := completeIndex()
	index.RecordStatus = RecordStatusActive
	index.Decision.Status = DecisionNoGo

	verification := Verify(index)
	if verification.Valid {
		t.Fatalf("inconsistent passed index unexpectedly valid: %+v", verification)
	}
	if verification.Status != StatusFailed || verification.Decision != DecisionNoGo {
		t.Fatalf("fail-closed result = status %q decision %q", verification.Status, verification.Decision)
	}
	if len(verification.PassedGates) != 16 {
		t.Fatalf("diagnostic passed gate summary lost: %v", verification.PassedGates)
	}
	if !contains(verification.Reasons, "semantic verification issues present") {
		t.Fatalf("fail-closed reasons = %v", verification.Reasons)
	}
}

func TestVerifySemanticDefects(t *testing.T) {
	tests := []struct {
		name  string
		index func() Index
		issue string
	}{
		{
			name: "candidate version",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Candidate.Version = "01.2.3"
				return index
			},
			issue: "candidate.version",
		},
		{
			name: "tag identity mismatch",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Candidate.Tag = "v9.9.9"
				return index
			},
			issue: "candidate.tag",
		},
		{
			name: "zero commit",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Candidate.GitCommit = strings.Repeat("0", 40)
				return index
			},
			issue: "candidate.git_commit",
		},
		{
			name: "asset fingerprint",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Candidate.AssetFingerprint = strings.Repeat("A", 64)
				return index
			},
			issue: "candidate.asset_fingerprint",
		},
		{
			name: "scenario pack id",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Candidate.ScenarioPack.ID = "BAD/ID"
				return index
			},
			issue: "candidate.scenario_pack.id",
		},
		{
			name: "scenario pack version",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Candidate.ScenarioPack.Version = "01.3.1"
				return index
			},
			issue: "candidate.scenario_pack.version",
		},
		{
			name: "scenario pack digest",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Candidate.ScenarioPack.Digest = "sha256:" + strings.Repeat("A", 64)
				return index
			},
			issue: "candidate.scenario_pack.digest",
		},
		{
			name: "gate evidence durable URL",
			index: func() Index {
				index := completeIndex()
				index.Gates.Publication.Evidence.Ref = "file:///tmp/publication.json"
				return index
			},
			issue: "gates.publication.evidence.ref",
		},
		{
			name: "gate evidence digest",
			index: func() Index {
				index := completeIndex()
				index.Gates.Publication.Evidence.Digest = strings.Repeat("1", 64)
				return index
			},
			issue: "gates.publication.evidence.digest",
		},
		{
			name: "open gate carries evidence",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Gates.Publication.Evidence = evidence("https://example.test/publication.json")
				return index
			},
			issue: "must be absent while status is open",
		},
		{
			name: "passed gate missing evidence",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Gates.Publication.Status = GateStatusPassed
				return index
			},
			issue: "evidence is required while status is passed",
		},
		{
			name: "unknown gate status",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Gates.Publication.Status = "skipped"
				return index
			},
			issue: "want open, passed, or failed",
		},
		{
			name: "verified immutable releases disabled",
			index: func() Index {
				index := completeIndex()
				index.PreventiveControls.ImmutableReleases.Enabled = pointer(false)
				return index
			},
			issue: "enabled must be true while status is verified",
		},
		{
			name: "required boolean absent",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.PreventiveControls.TagRuleset.CreationRestricted = nil
				return index
			},
			issue: "creation_restricted must be true",
		},
		{
			name: "incomplete bypass review",
			index: func() Index {
				index := completeIndex()
				index.PreventiveControls.TagRuleset.BypassReview.Reviewer = nil
				return index
			},
			issue: "bypass_review.reviewer",
		},
		{
			name: "decision timestamp",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.Decision.RecordedAt = "yesterday"
				return index
			},
			issue: "decision.recorded_at",
		},
		{
			name: "decision predates index",
			index: func() Index {
				index := openIndex(RecordStatusActive)
				index.CreatedAt = "2026-08-14T12:34:57Z"
				return index
			},
			issue: "decision.recorded_at must not precede created_at",
		},
		{
			name: "bypass review predates ruleset",
			index: func() Index {
				index := completeIndex()
				index.PreventiveControls.TagRuleset.BypassReview.RulesetUpdatedAt = pointer("2026-08-14T12:34:57Z")
				return index
			},
			issue: "reviewed_at must not precede ruleset_updated_at",
		},
		{
			name: "stored go with open gates",
			index: func() Index {
				index := openIndex(RecordStatusComplete)
				index.Decision.Status = DecisionGo
				return index
			},
			issue: "want independently recomputed \"no-go\"",
		},
		{
			name: "stored no-go with passed gates",
			index: func() Index {
				index := completeIndex()
				index.RecordStatus = RecordStatusActive
				index.Decision.Status = DecisionNoGo
				return index
			},
			issue: "want independently recomputed \"go\"",
		},
		{
			name: "template carries completed gate",
			index: func() Index {
				index := openIndex(RecordStatusTemplate)
				index.Gates.SourceCompatibility = Gate{Status: GateStatusPassed, Evidence: evidence("https://example.test/source.json")}
				return index
			},
			issue: "record_status template requires every readiness requirement to remain open",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verification := Verify(test.index())
			if verification.Valid {
				t.Fatalf("Verify() unexpectedly valid: %+v", verification)
			}
			if verification.Decision != DecisionNoGo {
				t.Fatalf("recomputed decision = %q, want no-go", verification.Decision)
			}
			if !sliceContainsSubstring(verification.Issues, test.issue) {
				t.Fatalf("issues %v do not contain %q", verification.Issues, test.issue)
			}
			if !sort.StringsAreSorted(verification.Issues) {
				t.Fatalf("issues are not sorted: %v", verification.Issues)
			}
			if !sort.StringsAreSorted(verification.OpenGates) || !sort.StringsAreSorted(verification.FailedGates) || !sort.StringsAreSorted(verification.PassedGates) {
				t.Fatalf("gate lists are not sorted: open=%v failed=%v passed=%v", verification.OpenGates, verification.FailedGates, verification.PassedGates)
			}
			if !sort.StringsAreSorted(verification.Reasons) {
				t.Fatalf("derived reasons are not sorted: %v", verification.Reasons)
			}
		})
	}
}

func TestLoadRejectsMalformedOrAmbiguousJSON(t *testing.T) {
	valid := marshalIndex(t, openIndex(RecordStatusActive))
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unknown top-level field",
			content: strings.Replace(valid, `"schema_version":`, `"unknown":true,"schema_version":`, 1),
			want:    "unknown field",
		},
		{
			name:    "unknown nested field",
			content: strings.Replace(valid, `"candidate":{`, `"candidate":{"unknown":true,`, 1),
			want:    "unknown field",
		},
		{
			name:    "duplicate top-level field",
			content: strings.Replace(valid, `"schema_version":`, `"schema_version":"duplicate","schema_version":`, 1),
			want:    "duplicate property",
		},
		{
			name:    "duplicate nested field",
			content: strings.Replace(valid, `"candidate":{"version":`, `"candidate":{"version":"duplicate","version":`, 1),
			want:    "duplicate property",
		},
		{
			name:    "trailing JSON",
			content: valid + ` {}`,
			want:    "trailing JSON",
		},
		{
			name:    "wrong JSON type",
			content: strings.Replace(valid, `"record_status":"active"`, `"record_status":1`, 1),
			want:    "cannot unmarshal number",
		},
		{
			name:    "explicit null",
			content: strings.Replace(valid, `"status":"open"`, `"status":"open","note":null`, 1),
			want:    "null is not allowed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.content))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestVerifyFileSeparatesLoadErrorsFromSemanticInvalidity(t *testing.T) {
	directory := t.TempDir()
	badJSONPath := filepath.Join(directory, "bad.json")
	if err := os.WriteFile(badJSONPath, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(badJSONPath); err == nil {
		t.Fatal("VerifyFile(bad JSON) unexpectedly succeeded")
	}

	invalid := openIndex(RecordStatusActive)
	invalid.Candidate.GitCommit = "short"
	invalidPath := filepath.Join(directory, "invalid.json")
	writeIndex(t, invalidPath, invalid)
	verification, err := VerifyFile(invalidPath)
	if err != nil {
		t.Fatalf("VerifyFile(semantic invalidity) error = %v", err)
	}
	if verification.Valid || !sliceContainsSubstring(verification.Issues, "candidate.git_commit") {
		t.Fatalf("semantic invalidity result = %+v", verification)
	}
}

func TestLoadFileRejectsSymlinkDirectoryAndOversize(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "index.json")
	writeIndex(t, path, openIndex(RecordStatusActive))

	symlink := filepath.Join(directory, "index-link.json")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(symlink); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("LoadFile(symlink) error = %v", err)
	}
	if _, err := LoadFile(directory); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("LoadFile(directory) error = %v", err)
	}

	oversize := filepath.Join(directory, "oversize.json")
	if err := os.WriteFile(oversize, make([]byte, maxIndexBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(oversize); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("LoadFile(oversize) error = %v", err)
	}
}

func TestDurableEvidenceSchemes(t *testing.T) {
	valid := []string{
		"https://example.test/evidence.json",
		"s3://evidence-bucket/releases/evidence.json",
		"gs://evidence-bucket/releases/evidence.json",
		"urn:pgworkbench:evidence:1234",
		"urn:pg-workbench:evidence/1234",
		"urn:ab:content%20address",
	}
	for _, ref := range valid {
		if !validDurableRef(ref) {
			t.Errorf("validDurableRef(%q) = false", ref)
		}
	}
	invalid := []string{
		"http://example.test/evidence.json",
		"https:evidence.json",
		"https://example.test",
		"https://user@example.test/evidence.json",
		"https://example.test/evidence.json#mutable-view",
		"https://github.com/r314tive/postgres-experiment-workbench/actions/runs/123/artifacts/456",
		"https://github.com:443/r314tive/postgres-experiment-workbench/actions/runs/123/artifacts/456",
		"https://github.com./r314tive/postgres-experiment-workbench/actions/runs/123/artifacts/456",
		"https://api.github.com:443/repos/r314tive/postgres-experiment-workbench/actions/artifacts/456",
		"https://objects.githubusercontent.com:443/actions/artifacts/456/archive.zip",
		"s3:///evidence.json",
		"s3://evidence-bucket",
		"gs://evidence-bucket/",
		"file:///tmp/evidence.json",
		"urn:",
		"urn::",
		"urn:a:value",
		"urn:ab:",
		"urn:-ab:value",
		"urn:ab-:value",
		"urn:urn:value",
		"urn:ab:/value",
		"urn:ab:value%zz",
		"urn:ab:value\\path",
		"urn:single-component",
		"relative/evidence.json",
	}
	for _, ref := range invalid {
		if validDurableRef(ref) {
			t.Errorf("validDurableRef(%q) = true", ref)
		}
	}
}

func openIndex(recordStatus string) Index {
	return Index{
		SchemaVersion: SchemaVersion,
		ArtifactType:  ArtifactType,
		RecordStatus:  recordStatus,
		CreatedAt:     testTime,
		Candidate: Candidate{
			Version:          "0.3.0",
			Tag:              "v0.3.0",
			GitCommit:        strings.Repeat("3", 40),
			AssetFingerprint: strings.Repeat("2", 64),
			ScenarioPack: ScenarioPack{
				ID:      "builtin",
				Version: "0.3.0",
				Digest:  testDigest(),
			},
		},
		PreventiveControls: PreventiveControls{
			TagRuleset: TagRuleset{
				Status:             ControlStatusOpen,
				Target:             "tag",
				Enforcement:        "active",
				IncludePattern:     "refs/tags/v*",
				Excludes:           []string{},
				CreationRestricted: pointer(true),
				UpdateProhibited:   pointer(true),
				DeletionProhibited: pointer(true),
				BypassReview:       AdminReview{Status: ReviewStatusOpen},
			},
			ImmutableReleases: ImmutableReleases{Status: ControlStatusOpen, Enabled: pointer(false)},
		},
		Gates:    gates(GateStatusOpen, nil),
		Decision: Decision{Scope: DecisionScope, Status: DecisionNoGo, RecordedAt: testTime, Reasons: []string{"Readiness requirements remain open."}},
	}
}

func completeIndex() Index {
	index := openIndex(RecordStatusComplete)
	index.Gates = gates(GateStatusPassed, evidence("https://example.test/evidence.json"))
	index.PreventiveControls.TagRuleset.Status = ControlStatusVerified
	index.PreventiveControls.TagRuleset.APIEvidence = evidence("https://api.github.test/tag-ruleset.json")
	index.PreventiveControls.TagRuleset.BypassReview = AdminReview{
		Status:           ReviewStatusAdminReviewed,
		Reviewer:         pointer("release-admin"),
		ReviewedAt:       pointer(testTime),
		RulesetID:        pointer(int64(123)),
		RulesetUpdatedAt: pointer(testTime),
		Evidence:         evidence("https://example.test/bypass-review.json"),
	}
	index.PreventiveControls.ImmutableReleases = ImmutableReleases{
		Status:      ControlStatusVerified,
		Enabled:     pointer(true),
		APIEvidence: evidence("https://api.github.test/immutable-releases.json"),
	}
	index.Decision = Decision{Scope: DecisionScope, Status: DecisionGo, RecordedAt: testTime, Reasons: []string{"All v1 readiness requirements passed."}}
	return index
}

func completeV2Index() Index {
	index := completeIndex()
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	return index
}

func completeV3NoGoIndex() Index {
	index := completeIndex()
	index.SchemaVersion = SchemaVersionV3
	index.Lineage = &Lineage{Revision: 0}
	index.RecordStatus = RecordStatusActive
	index.Decision.Status = DecisionNoGo
	index.Decision.Reasons = []string{"Evidence assurance is not authorization-eligible."}
	return index
}

func gates(status string, attached *Evidence) Gates {
	gate := func(name string) Gate {
		if attached == nil {
			return Gate{Status: status}
		}
		copy := *attached
		copy.Ref = strings.TrimSuffix(copy.Ref, ".json") + "-" + name + ".json"
		return Gate{Status: status, Evidence: &copy}
	}
	return Gates{
		SourceCompatibility:              gate("source-compatibility"),
		AggregateAttempt1:                gate("aggregate-attempt-1"),
		AggregateAttempt2:                gate("aggregate-attempt-2"),
		DraftAssetVerification:           gate("draft-asset-verification"),
		DraftCompatibility7Cells:         gate("draft-compatibility-7-cells"),
		DraftExternalDrivers:             gate("draft-external-drivers"),
		Publication:                      gate("publication"),
		PublicAssetVerification:          gate("public-asset-verification"),
		PublishedCompatibility7Cells:     gate("published-compatibility-7-cells"),
		CriticalFindingReview:            gate("critical-finding-review"),
		AdoptionPilot1:                   gate("adoption-pilot-1"),
		AdoptionPilot2:                   gate("adoption-pilot-2"),
		IndependentAuthoringReproduction: gate("independent-authoring-reproduction"),
	}
}

func evidence(ref string) *Evidence {
	return &Evidence{Ref: ref, Digest: testDigest(), CapturedAt: testTime}
}

func typedExternalDriverEvidence(ref string) *Evidence {
	value := evidence(ref)
	value.Record = &EvidenceRecord{
		SchemaVersion: ExternalDriverVerificationSchema,
		ArtifactType:  ExternalDriverVerificationType,
	}
	value.Assurance = &EvidenceAssurance{
		Durability:   EvidenceDurabilityAsserted,
		Authenticity: EvidenceAuthenticityUnverified,
	}
	return value
}

func testDigest() string {
	return "sha256:" + strings.Repeat("a", 64)
}

func pointer[T any](value T) *T {
	return &value
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sliceContainsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func marshalIndex(t *testing.T, index Index) string {
	t.Helper()
	content, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func writeIndex(t *testing.T, path string, index Index) {
	t.Helper()
	if err := os.WriteFile(path, []byte(marshalIndex(t, index)), 0o600); err != nil {
		t.Fatal(err)
	}
}
