package releaseevidence

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestPreventiveControlsVerificationConformsToSchemaAndSemantics(t *testing.T) {
	candidate, record := preventiveControlsVerificationFixture(t)
	if err := ValidatePreventiveControlsVerification(record, candidate); err != nil {
		t.Fatalf("valid preventive-controls verification rejected: %v", err)
	}

	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("release-preventive-controls-verification.schema.json", marshalRecord(t, record, true)); err != nil {
		t.Fatalf("valid preventive-controls verification does not conform to schema: %v", err)
	}

	wantConstants := map[string]string{
		"schema":             "pgworkbench.release-preventive-controls-verification/v1",
		"type":               "pgworkbench.release-preventive-controls-verification",
		"tag ruleset":        "pgworkbench.release-preventive-controls/tag-ruleset/v1",
		"bypass review":      "pgworkbench.release-preventive-controls/bypass-review/v1",
		"immutable releases": "pgworkbench.release-preventive-controls/immutable-releases/v1",
		"seal job":           "seal-preventive-controls",
	}
	gotConstants := map[string]string{
		"schema":             PreventiveControlsVerificationSchema,
		"type":               PreventiveControlsVerificationType,
		"tag ruleset":        PreventiveControlsTagRulesetAdapter,
		"bypass review":      PreventiveControlsBypassReviewAdapter,
		"immutable releases": PreventiveControlsImmutableReleasesAdapter,
		"seal job":           PreventiveControlsSealJob,
	}
	for name, want := range wantConstants {
		if gotConstants[name] != want {
			t.Fatalf("%s constant = %q, want %q", name, gotConstants[name], want)
		}
	}
}

func TestPreventiveControlsVerificationRejectsSemanticMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *PreventiveControlsVerification)
		want   string
	}{
		{name: "schema", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.SchemaVersion = "pgworkbench.release-preventive-controls-verification/v2"
		}, want: "unsupported"},
		{name: "artifact type", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.ArtifactType = "other" }, want: "unsupported"},
		{name: "candidate", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.Candidate.Tag = "v9.9.9" }, want: "candidate"},
		{name: "noncanonical capture", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.CapturedAt = "2026-08-14T12:36:00+00:00"
		}, want: "captured_at"},
		{name: "workflow job", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.WorkflowRun.Job = "publish-release" }, want: "workflow_run"},
		{name: "workflow attempt unsafe", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.WorkflowRun.Attempt = maxJSONSafeInteger + 1
		}, want: "workflow_run"},
		{name: "nested candidate", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.DraftAssetVerification.Candidate.Version = "9.9.9"
		}, want: "draft_asset_verification"},
		{name: "published asset record", mutate: func(t *testing.T, value *PreventiveControlsVerification) {
			value.DraftAssetVerification = releaseAssetRecordFixture(t, value.Candidate, releaseQualificationPublished)
		}, want: "must use draft"},
		{name: "different asset run", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.DraftAssetVerification.WorkflowRun.ID = "987654321"
		}, want: "same workflow run"},
		{name: "capture before draft", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.CapturedAt = "2026-08-14T12:34:59Z" }, want: "must not precede"},
		{name: "controls artifact id", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.Source.ControlsArtifact.ID = "01" }, want: "controls_artifact"},
		{name: "controls artifact name", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Source.ControlsArtifact.Name = "release-controls-wrong"
		}, want: "controls_artifact"},
		{name: "controls artifact digest", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Source.ControlsArtifact.Digest = "sha256:ABC"
		}, want: "controls_artifact"},
		{name: "repository controls digest", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Source.RepositoryControlsDigest = "sha256:bad"
		}, want: "repository_controls_digest"},
		{name: "tag ruleset api digest", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Source.TagRulesetAPIDigest = "sha256:bad"
		}, want: "tag_ruleset_api_digest"},
		{name: "immutable releases api digest", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Source.ImmutableReleasesAPIDigest = "sha256:bad"
		}, want: "immutable_releases_api_digest"},
		{name: "ruleset id zero", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.TagRuleset.ID = 0 }, want: "tag_ruleset"},
		{name: "ruleset id unsafe", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.TagRuleset.ID = maxJSONSafeInteger + 1
		}, want: "tag_ruleset"},
		{name: "ruleset timestamp", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.TagRuleset.UpdatedAt = "2026-08-14T12:35:10.000Z"
		}, want: "tag_ruleset"},
		{name: "ruleset target", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.TagRuleset.Target = "branch" }, want: "tag_ruleset"},
		{name: "ruleset enforcement", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.TagRuleset.Enforcement = "evaluate" }, want: "tag_ruleset"},
		{name: "ruleset include", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.TagRuleset.IncludePattern = "refs/tags/*"
		}, want: "tag_ruleset"},
		{name: "ruleset nil excludes", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.TagRuleset.Excludes = nil }, want: "tag_ruleset"},
		{name: "ruleset nonempty excludes", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.TagRuleset.Excludes = []string{"refs/tags/v0.*"}
		}, want: "tag_ruleset"},
		{name: "creation not restricted", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.TagRuleset.CreationRestricted = boolPointer(false)
		}, want: "tag_ruleset"},
		{name: "update prohibition absent", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.TagRuleset.UpdateProhibited = nil }, want: "tag_ruleset"},
		{name: "deletion not prohibited", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.TagRuleset.DeletionProhibited = boolPointer(false)
		}, want: "tag_ruleset"},
		{name: "reviewer whitespace", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.Reviewer = "release admin"
		}, want: "reviewer"},
		{name: "reviewer overlong", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.Reviewer = strings.Repeat("a", 129)
		}, want: "reviewer"},
		{name: "review timestamp", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.ReviewedAt = "2026-08-14T12:35:20.1Z"
		}, want: "reviewed_at"},
		{name: "review ruleset id", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.BypassReview.RulesetID++ }, want: "exact observed ruleset"},
		{name: "review ruleset timestamp", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.RulesetUpdatedAt = "2026-08-14T12:35:11Z"
		}, want: "exact observed ruleset"},
		{name: "review before update", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.ReviewedAt = "2026-08-14T12:35:09Z"
		}, want: "must not precede"},
		{name: "review after capture", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.ReviewedAt = "2026-08-14T12:36:01Z"
		}, want: "must not follow captured_at"},
		{name: "ephemeral review evidence", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.EvidenceRef = "https://github.com/r314tive/postgres-experiment-workbench/actions/runs/123/artifacts/456"
		}, want: "durable"},
		{name: "review evidence digest", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.EvidenceDigest = "sha256:bad"
		}, want: "durable"},
		{name: "review evidence ref overlong", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.BypassReview.EvidenceRef = "https://evidence.example.test/" + strings.Repeat("a", 2049)
		}, want: "durable"},
		{name: "immutable disabled", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.ImmutableReleases.Enabled = boolPointer(false)
		}, want: "immutable_releases"},
		{name: "immutable enabled absent", mutate: func(_ *testing.T, value *PreventiveControlsVerification) { value.ImmutableReleases.Enabled = nil }, want: "immutable_releases"},
		{name: "owner enforcement absent", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.ImmutableReleases.EnforcedByOwner = nil
		}, want: "immutable_releases"},
		{name: "assurance purpose", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.Purpose = "release-authorization"
		}, want: "assurance"},
		{name: "assurance scope", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.VerificationScope = "remote-authenticity"
		}, want: "assurance"},
		{name: "durability claim", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.ActionsArtifactDurable = boolPointer(true)
		}, want: "assurance"},
		{name: "candidate not reverified", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.CandidateIdentityReverified = boolPointer(false)
		}, want: "assurance"},
		{name: "remote bypass fetched", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.BypassReviewRemoteObjectFetched = boolPointer(true)
		}, want: "assurance"},
		{name: "bypass signature verified", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.BypassReviewSignatureVerified = boolPointer(true)
		}, want: "assurance"},
		{name: "performance claim", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.PerformanceClaim = boolPointer(true)
		}, want: "assurance"},
		{name: "benchmark claim", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.BenchmarkComparabilityClaim = boolPointer(true)
		}, want: "assurance"},
		{name: "recovery claim", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.RecoveryClaim = boolPointer(true)
		}, want: "assurance"},
		{name: "production decision claim", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.ProductionDecisionEligible = boolPointer(true)
		}, want: "assurance"},
		{name: "required assurance boolean absent", mutate: func(_ *testing.T, value *PreventiveControlsVerification) {
			value.Assurance.ActionsArtifactDurable = nil
		}, want: "assurance"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, record := preventiveControlsVerificationFixture(t)
			test.mutate(t, &record)
			err := ValidatePreventiveControlsVerification(record, candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidatePreventiveControlsVerification error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPreventiveControlsVerificationSchemaForbidsOutcomesAndUnknownFields(t *testing.T) {
	_, record := preventiveControlsVerificationFixture(t)
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "status", mutate: func(value map[string]any) { value["status"] = "passed" }},
		{name: "passed", mutate: func(value map[string]any) { value["passed"] = true }},
		{name: "outcome", mutate: func(value map[string]any) { value["outcome"] = "success" }},
		{name: "decision", mutate: func(value map[string]any) { value["decision"] = "go" }},
		{name: "note", mutate: func(value map[string]any) { value["note"] = "looks good" }},
		{name: "nested status", mutate: func(value map[string]any) { objectAt(t, value, "tag_ruleset")["status"] = "verified" }},
		{name: "unknown source", mutate: func(value map[string]any) { objectAt(t, value, "source")["api_response"] = map[string]any{} }},
		{name: "missing restriction", mutate: func(value map[string]any) { delete(objectAt(t, value, "tag_ruleset"), "creation_restricted") }},
		{name: "missing assurance", mutate: func(value map[string]any) { delete(objectAt(t, value, "assurance"), "actions_artifact_durable") }},
		{name: "null excludes", mutate: func(value map[string]any) { objectAt(t, value, "tag_ruleset")["excludes"] = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := preventiveControlsJSONObject(t, record)
			test.mutate(value)
			content, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.ValidateJSON("release-preventive-controls-verification.schema.json", content); err == nil {
				t.Fatal("schema accepted a forbidden or incomplete preventive-controls record")
			}
		})
	}
}

func preventiveControlsVerificationFixture(t *testing.T) (Candidate, PreventiveControlsVerification) {
	t.Helper()
	index, draft := releaseAssetAttachFixture(t, releaseQualificationDraft)
	candidate := index.Candidate
	record := PreventiveControlsVerification{
		SchemaVersion: PreventiveControlsVerificationSchema,
		ArtifactType:  PreventiveControlsVerificationType,
		Candidate:     candidate,
		CapturedAt:    "2026-08-14T12:36:00Z",
		WorkflowRun: ReleaseVerificationWorkflowRun{
			ID: draft.WorkflowRun.ID, Attempt: draft.WorkflowRun.Attempt, HeadSHA: candidate.GitCommit,
			Repository: releaseWorkflowRepository, Workflow: releaseWorkflowName,
			Job: PreventiveControlsSealJob, Ref: "refs/tags/" + candidate.Tag,
		},
		DraftAssetVerification: draft,
		Source: PreventiveControlsVerificationSource{
			ControlsArtifact: QualificationArtifact{
				ID:     "246813579",
				Name:   "release-controls-" + candidate.Tag + "-" + candidate.GitCommit + "-2",
				Digest: "sha256:" + strings.Repeat("a", 64),
			},
			RepositoryControlsDigest:   "sha256:" + strings.Repeat("b", 64),
			TagRulesetAPIDigest:        "sha256:" + strings.Repeat("c", 64),
			ImmutableReleasesAPIDigest: "sha256:" + strings.Repeat("d", 64),
		},
		TagRuleset: PreventiveControlsTagRuleset{
			ID: 42, UpdatedAt: "2026-08-14T12:35:10Z", Target: "tag", Enforcement: "active",
			IncludePattern: "refs/tags/v*", Excludes: []string{}, CreationRestricted: boolPointer(true),
			UpdateProhibited: boolPointer(true), DeletionProhibited: boolPointer(true),
		},
		BypassReview: PreventiveControlsBypassReview{
			Reviewer: "release-admin@example.test", ReviewedAt: "2026-08-14T12:35:20Z",
			RulesetID: 42, RulesetUpdatedAt: "2026-08-14T12:35:10Z",
			EvidenceRef:    "urn:pgworkbench:release-controls:bypass-review:v0.3.0",
			EvidenceDigest: "sha256:" + strings.Repeat("e", 64),
		},
		ImmutableReleases: PreventiveControlsImmutableReleases{
			Enabled: boolPointer(true), EnforcedByOwner: boolPointer(false),
		},
		Assurance: PreventiveControlsVerificationAssurance{
			Purpose: preventiveControlsAssurancePurpose, VerificationScope: preventiveControlsAssuranceVerificationScope,
			ActionsArtifactDurable: boolPointer(false), CandidateIdentityReverified: boolPointer(true),
			BypassReviewRemoteObjectFetched: boolPointer(false), BypassReviewSignatureVerified: boolPointer(false),
			PerformanceClaim: boolPointer(false), BenchmarkComparabilityClaim: boolPointer(false),
			RecoveryClaim: boolPointer(false), ProductionDecisionEligible: boolPointer(false),
		},
	}
	return candidate, record
}

func preventiveControlsJSONObject(t *testing.T, record PreventiveControlsVerification) map[string]any {
	t.Helper()
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func objectAt(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()
	object, ok := value[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", key)
	}
	return object
}
