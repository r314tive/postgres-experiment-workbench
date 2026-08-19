package releaseevidence

import (
	"fmt"
	"reflect"

	"github.com/r314tive/postgres-experiment-workbench/internal/strictjson"
)

var preventiveControlAttachmentPaths = []string{
	"preventive_controls.tag_ruleset",
	"preventive_controls.tag_ruleset.bypass_review",
	"preventive_controls.immutable_releases",
}

type ControlsAttachOptions struct {
	IndexPath    string
	EvidenceFile string
	EvidenceRef  string
	Output       string
}

type ControlsAttachResult struct {
	Output               string       `json:"output"`
	Digest               string       `json:"digest"`
	PreviousIndexDigest  string       `json:"previous_index_digest"`
	Revision             int64        `json:"revision"`
	Controls             []string     `json:"controls"`
	EvidenceDigest       string       `json:"evidence_digest"`
	EvidenceDurability   string       `json:"evidence_durability"`
	EvidenceAuthenticity string       `json:"evidence_authenticity"`
	RecordSchemaVersion  string       `json:"record_schema_version"`
	RecordArtifactType   string       `json:"record_artifact_type"`
	RecordAdapters       []string     `json:"record_adapters"`
	IndexVerification    Verification `json:"index_verification"`
}

type controlsAttachHooks struct {
	beforePublication func()
}

// AttachControls consumes one exact typed record and closes all
// three preventive-control requirements in one copy-on-write revision. The
// record, rather than the caller, supplies every control and review fact.
func AttachControls(options ControlsAttachOptions) (ControlsAttachResult, error) {
	return attachControls(options, controlsAttachHooks{})
}

func attachControls(options ControlsAttachOptions, hooks controlsAttachHooks) (ControlsAttachResult, error) {
	if options.IndexPath == "" || options.EvidenceFile == "" || options.EvidenceRef == "" || options.Output == "" {
		return ControlsAttachResult{}, fmt.Errorf("index, evidence file, evidence ref, and output are required")
	}
	if !validDurableRef(options.EvidenceRef) {
		return ControlsAttachResult{}, fmt.Errorf("evidence ref must be an absolute durable https, s3, gs, or urn URI; durability and authenticity are operator assertions")
	}

	chain, err := openPinnedAttachmentChain(options.IndexPath, options.Output)
	if err != nil {
		return ControlsAttachResult{}, err
	}
	defer chain.close()

	indexBytes, err := strictjson.ReadOpenedFile(chain.predecessor, maxIndexBytes)
	if err != nil {
		return ControlsAttachResult{}, fmt.Errorf("read predecessor release evidence index: %w", err)
	}
	chain.predecessorInfo, err = chain.predecessor.Stat()
	if err != nil {
		return ControlsAttachResult{}, fmt.Errorf("inspect pinned predecessor release evidence index: %w", err)
	}
	index, err := Parse(indexBytes)
	if err != nil {
		return ControlsAttachResult{}, fmt.Errorf("parse predecessor release evidence index: %w", err)
	}
	verification := Verify(index)
	if !verification.Valid {
		return ControlsAttachResult{}, fmt.Errorf("predecessor release evidence index is invalid: %s", joinIssues(verification.Issues))
	}
	if !oneOf(index.SchemaVersion, SchemaVersionV2, SchemaVersionV3) || index.Lineage == nil {
		return ControlsAttachResult{}, fmt.Errorf("preventive-controls attachment requires a v2 or v3 predecessor with lineage")
	}
	if index.RecordStatus != RecordStatusActive {
		return ControlsAttachResult{}, fmt.Errorf("preventive-controls attachment requires an active predecessor, got %q", index.RecordStatus)
	}
	if index.Lineage.Revision >= maxJSONSafeInteger {
		return ControlsAttachResult{}, fmt.Errorf("predecessor lineage revision cannot be incremented safely")
	}
	if !reflect.DeepEqual(index.PreventiveControls, canonicalOpenPreventiveControls()) {
		return ControlsAttachResult{}, fmt.Errorf("all preventive controls must be at the exact canonical open baseline; attachment does not supersede or repair partial control state")
	}

	evidenceBytes, err := strictjson.ReadFile(options.EvidenceFile, maxGateRecordBytes)
	if err != nil {
		return ControlsAttachResult{}, fmt.Errorf("read preventive-controls evidence record: %w", err)
	}
	header, err := inspectArtifactHeader(evidenceBytes)
	if err != nil {
		return ControlsAttachResult{}, fmt.Errorf("inspect preventive-controls evidence record: %w", err)
	}
	if header.SchemaVersion != PreventiveControlsVerificationSchema || header.ArtifactType != PreventiveControlsVerificationType {
		return ControlsAttachResult{}, fmt.Errorf("unsupported preventive-controls evidence record %q with schema %q", header.ArtifactType, header.SchemaVersion)
	}
	var record PreventiveControlsVerification
	if err := strictjson.Parse(evidenceBytes, &record); err != nil {
		return ControlsAttachResult{}, fmt.Errorf("parse preventive-controls evidence record: %w", err)
	}
	if err := ValidatePreventiveControlsVerification(record, index.Candidate); err != nil {
		return ControlsAttachResult{}, fmt.Errorf("verify preventive-controls evidence record: %w", err)
	}
	if captured, ok := parseDateTime(record.CapturedAt); ok {
		if created, createdOK := parseDateTime(index.CreatedAt); createdOK && captured.Before(created) {
			return ControlsAttachResult{}, fmt.Errorf("preventive-controls evidence captured_at must not precede the candidate index created_at")
		}
	}

	target, err := canonicalNextOutput(chain.predecessorName, chain.outputName, chain.displayOutput, index.Lineage.Revision)
	if err != nil {
		return ControlsAttachResult{}, err
	}

	beforeCandidate := index.Candidate
	beforeCreatedAt := index.CreatedAt
	beforeGates := index.Gates
	previousDigest := digestExactBytes(indexBytes)
	evidenceDigest := digestExactBytes(evidenceBytes)
	assurance := EvidenceAssurance{
		Durability:   EvidenceDurabilityAsserted,
		Authenticity: EvidenceAuthenticityUnverified,
	}
	newEvidence := func(adapter string) *Evidence {
		return &Evidence{
			Ref:        options.EvidenceRef,
			Digest:     evidenceDigest,
			CapturedAt: record.CapturedAt,
			RunID:      stringPointer(record.WorkflowRun.ID),
			RunAttempt: int64Pointer(record.WorkflowRun.Attempt),
			Record: &EvidenceRecord{
				SchemaVersion: record.SchemaVersion,
				ArtifactType:  record.ArtifactType,
				Adapter:       adapter,
			},
			Assurance: &EvidenceAssurance{
				Durability:   assurance.Durability,
				Authenticity: assurance.Authenticity,
			},
		}
	}
	index.PreventiveControls = PreventiveControls{
		TagRuleset: TagRuleset{
			Status:             ControlStatusVerified,
			Target:             record.TagRuleset.Target,
			Enforcement:        record.TagRuleset.Enforcement,
			IncludePattern:     record.TagRuleset.IncludePattern,
			Excludes:           append([]string{}, record.TagRuleset.Excludes...),
			CreationRestricted: boolPointer(*record.TagRuleset.CreationRestricted),
			UpdateProhibited:   boolPointer(*record.TagRuleset.UpdateProhibited),
			DeletionProhibited: boolPointer(*record.TagRuleset.DeletionProhibited),
			APIEvidence:        newEvidence(PreventiveControlsTagRulesetAdapter),
			BypassReview: AdminReview{
				Status:           ReviewStatusAdminReviewed,
				Reviewer:         stringPointer(record.BypassReview.Reviewer),
				ReviewedAt:       stringPointer(record.BypassReview.ReviewedAt),
				RulesetID:        int64Pointer(record.BypassReview.RulesetID),
				RulesetUpdatedAt: stringPointer(record.BypassReview.RulesetUpdatedAt),
				Evidence:         newEvidence(PreventiveControlsBypassReviewAdapter),
			},
		},
		ImmutableReleases: ImmutableReleases{
			Status:      ControlStatusVerified,
			Enabled:     boolPointer(*record.ImmutableReleases.Enabled),
			APIEvidence: newEvidence(PreventiveControlsImmutableReleasesAdapter),
		},
	}
	index.SchemaVersion = SchemaVersionV3
	index.Lineage = &Lineage{Revision: index.Lineage.Revision + 1, PreviousIndexDigest: &previousDigest}
	if err := finalizeDerivedDecision(&index, record.CapturedAt); err != nil {
		return ControlsAttachResult{}, err
	}
	if index.Candidate != beforeCandidate || index.CreatedAt != beforeCreatedAt || !reflect.DeepEqual(index.Gates, beforeGates) {
		return ControlsAttachResult{}, fmt.Errorf("internal preventive-controls adapter changed candidate, creation time, or readiness gates")
	}

	if hooks.beforePublication != nil {
		hooks.beforePublication()
	}
	written, writeErr := WriteNewAt(chain.directory, chain.outputName, target, index)
	writeErr = chain.finishWrite(written, writeErr, indexBytes)
	result := ControlsAttachResult{
		Output:               written.Output,
		Digest:               written.Digest,
		PreviousIndexDigest:  previousDigest,
		Revision:             index.Lineage.Revision,
		Controls:             append([]string(nil), preventiveControlAttachmentPaths...),
		EvidenceDigest:       evidenceDigest,
		EvidenceDurability:   assurance.Durability,
		EvidenceAuthenticity: assurance.Authenticity,
		RecordSchemaVersion:  record.SchemaVersion,
		RecordArtifactType:   record.ArtifactType,
		RecordAdapters: []string{
			PreventiveControlsTagRulesetAdapter,
			PreventiveControlsBypassReviewAdapter,
			PreventiveControlsImmutableReleasesAdapter,
		},
		IndexVerification: written.Verification,
	}
	return result, writeErr
}
