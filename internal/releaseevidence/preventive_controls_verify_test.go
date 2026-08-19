package releaseevidence

import (
	"reflect"
	"strings"
	"testing"
)

func TestVerifyRegistersOneAtomicPreventiveControlsEvidenceSet(t *testing.T) {
	index, err := NewIndex(Candidate{
		Version:          "0.2.6",
		Tag:              "v0.2.6",
		GitCommit:        strings.Repeat("a", 40),
		AssetFingerprint: strings.Repeat("b", 64),
		ScenarioPack: ScenarioPack{
			ID:      "builtin",
			Version: "0.2.6",
			Digest:  "sha256:" + strings.Repeat("c", 64),
		},
	}, "2026-08-18T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	index.PreventiveControls = typedPreventiveControlsBundleEvidence()
	if err := finalizeDerivedDecision(&index, "2026-08-18T12:00:01Z"); err != nil {
		t.Fatal(err)
	}
	verification := Verify(index)
	want := []string{
		"preventive_controls.immutable_releases",
		"preventive_controls.tag_ruleset",
		"preventive_controls.tag_ruleset.bypass_review",
	}
	if !verification.Valid || verification.Decision != DecisionNoGo || verification.AuthorizationEligible || !reflect.DeepEqual(verification.PassedGates, want) || !reflect.DeepEqual(verification.UnqualifiedEvidence, want) {
		t.Fatalf("typed controls verification = %+v", verification)
	}

	tests := []struct {
		name   string
		mutate func(*Index)
		issue  string
	}{
		{
			name: "path adapter transplant",
			mutate: func(value *Index) {
				value.PreventiveControls.TagRuleset.APIEvidence.Record.Adapter = PreventiveControlsBypassReviewAdapter
			},
			issue: "adapter",
		},
		{
			name: "record digest mismatch",
			mutate: func(value *Index) {
				value.PreventiveControls.ImmutableReleases.APIEvidence.Digest = "sha256:" + strings.Repeat("e", 64)
			},
			issue: "one exact record identity",
		},
		{
			name: "partial typed set",
			mutate: func(value *Index) {
				value.PreventiveControls.ImmutableReleases = ImmutableReleases{Status: ControlStatusOpen, Enabled: boolPointer(false)}
			},
			issue: "atomically",
		},
		{
			name: "missing workflow identity",
			mutate: func(value *Index) {
				value.PreventiveControls.TagRuleset.BypassReview.Evidence.RunID = nil
			},
			issue: "canonical workflow capture",
		},
		{
			name: "noncanonical capture",
			mutate: func(value *Index) {
				for _, evidence := range []*Evidence{
					value.PreventiveControls.TagRuleset.APIEvidence,
					value.PreventiveControls.TagRuleset.BypassReview.Evidence,
					value.PreventiveControls.ImmutableReleases.APIEvidence,
				} {
					evidence.CapturedAt = "2026-08-18T17:00:01+05:00"
				}
			},
			issue: "canonical workflow capture",
		},
		{
			name: "reviewer outside typed contract",
			mutate: func(value *Index) {
				reviewer := "release admin"
				value.PreventiveControls.TagRuleset.BypassReview.Reviewer = &reviewer
			},
			issue: "canonical reviewer",
		},
		{
			name: "noncanonical review timestamps",
			mutate: func(value *Index) {
				reviewedAt := "2026-08-18T17:00:00+05:00"
				updatedAt := "2026-08-18T16:00:00+05:00"
				value.PreventiveControls.TagRuleset.BypassReview.ReviewedAt = &reviewedAt
				value.PreventiveControls.TagRuleset.BypassReview.RulesetUpdatedAt = &updatedAt
			},
			issue: "canonical reviewer and UTC timestamps",
		},
		{
			name: "review after evidence capture",
			mutate: func(value *Index) {
				reviewedAt := "2026-08-18T12:00:02Z"
				value.PreventiveControls.TagRuleset.BypassReview.ReviewedAt = &reviewedAt
			},
			issue: "must not follow evidence captured_at",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := index
			mutated.PreventiveControls = clonePreventiveControlsForVerifyTest(index.PreventiveControls)
			test.mutate(&mutated)
			result := Verify(mutated)
			if result.Valid || !sliceContainsSubstring(result.Issues, test.issue) {
				t.Fatalf("mutated controls accepted: %+v", result)
			}
		})
	}
}

func clonePreventiveControlsForVerifyTest(value PreventiveControls) PreventiveControls {
	cloneEvidence := func(source *Evidence) *Evidence {
		if source == nil {
			return nil
		}
		result := *source
		if source.Record != nil {
			record := *source.Record
			result.Record = &record
		}
		if source.Assurance != nil {
			assurance := *source.Assurance
			result.Assurance = &assurance
		}
		if source.RunID != nil {
			runID := *source.RunID
			result.RunID = &runID
		}
		if source.RunAttempt != nil {
			runAttempt := *source.RunAttempt
			result.RunAttempt = &runAttempt
		}
		return &result
	}
	result := value
	result.TagRuleset.APIEvidence = cloneEvidence(value.TagRuleset.APIEvidence)
	result.TagRuleset.BypassReview.Evidence = cloneEvidence(value.TagRuleset.BypassReview.Evidence)
	result.ImmutableReleases.APIEvidence = cloneEvidence(value.ImmutableReleases.APIEvidence)
	return result
}
