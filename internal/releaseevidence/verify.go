package releaseevidence

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	semVerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|([0-9]*[A-Za-z-][0-9A-Za-z-]*))(\.((0|[1-9][0-9]*)|([0-9]*[A-Za-z-][0-9A-Za-z-]*)))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	lowerHex40    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	lowerHex64    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	urnNIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,30}[A-Za-z0-9]$`)
)

type requirement struct {
	name string
	gate Gate
}

// VerifyFile strictly loads and independently verifies an index. File and JSON
// errors are returned as error; semantic defects are returned in Verification.
func VerifyFile(path string) (Verification, error) {
	index, err := LoadFile(path)
	if err != nil {
		return Verification{}, err
	}
	return Verify(index), nil
}

// Verify independently evaluates candidate identity, evidence references,
// readiness requirements, and the stored lifecycle decision.
func Verify(index Index) Verification {
	result := Verification{
		RecordedDecision:    index.Decision.Status,
		OpenGates:           make([]string, 0),
		FailedGates:         make([]string, 0),
		PassedGates:         make([]string, 0),
		UnqualifiedEvidence: make([]string, 0),
		Reasons:             make([]string, 0),
		Warnings:            make([]string, 0),
		Issues:              make([]string, 0),
	}
	add := func(format string, args ...any) {
		result.Issues = append(result.Issues, fmt.Sprintf(format, args...))
	}

	integrityOK := true
	if !oneOf(index.SchemaVersion, SchemaVersionV1, SchemaVersionV2, SchemaVersionV3) {
		add("schema_version = %q, want %q, %q, or %q", index.SchemaVersion, SchemaVersionV1, SchemaVersionV2, SchemaVersionV3)
		integrityOK = false
	}
	if !validateLineage(add, index.SchemaVersion, index.Lineage) {
		integrityOK = false
	}
	if index.ArtifactType != ArtifactType {
		add("artifact_type = %q, want %q", index.ArtifactType, ArtifactType)
		integrityOK = false
	}
	if !oneOf(index.RecordStatus, RecordStatusTemplate, RecordStatusActive, RecordStatusComplete) {
		add("record_status = %q, want template, active, or complete", index.RecordStatus)
		integrityOK = false
	}
	if !validDateTime(index.CreatedAt) {
		add("created_at must be an RFC3339 date-time")
		integrityOK = false
	}
	if !validateCandidate(add, index.Candidate) {
		integrityOK = false
	}
	if index.RecordStatus != RecordStatusTemplate && hasTemplateSentinel(index.Candidate) {
		add("candidate identity contains a release-evidence template sentinel outside a template record")
		integrityOK = false
	}
	if !validateDecisionShape(add, index.Decision) {
		integrityOK = false
	}
	if createdAt, createdOK := parseDateTime(index.CreatedAt); createdOK {
		if recordedAt, recordedOK := parseDateTime(index.Decision.RecordedAt); recordedOK && recordedAt.Before(createdAt) {
			add("decision.recorded_at must not precede created_at")
			integrityOK = false
		}
	}

	states, qualifications, readinessValid := validateReadinessRequirements(add, index.Gates, index.PreventiveControls)
	if !readinessValid {
		integrityOK = false
	}

	for name, state := range states {
		switch state {
		case StatusOpen:
			result.OpenGates = append(result.OpenGates, name)
		case StatusFailed:
			result.FailedGates = append(result.FailedGates, name)
		case StatusPassed:
			result.PassedGates = append(result.PassedGates, name)
		}
	}
	for name, qualification := range qualifications {
		if qualification != AssuranceAuthorizationEligible {
			result.UnqualifiedEvidence = append(result.UnqualifiedEvidence, name)
		}
		if qualification == AssuranceLegacyUnspecified {
			result.Warnings = append(result.Warnings, "legacy evidence has no persisted record and assurance: "+name)
		}
	}
	sort.Strings(result.OpenGates)
	sort.Strings(result.FailedGates)
	sort.Strings(result.PassedGates)
	sort.Strings(result.UnqualifiedEvidence)
	sort.Strings(result.Warnings)
	result.AssuranceStatus = aggregateAssuranceStatus(qualifications)

	result.ReadinessStatus, result.ReadinessDecision = readinessOutcome(integrityOK, states, false)
	result.Status, result.Decision = readinessOutcome(integrityOK, states, len(result.UnqualifiedEvidence) != 0)
	storedExpectedDecision := result.Decision
	if oneOf(index.SchemaVersion, SchemaVersionV1, SchemaVersionV2) {
		storedExpectedDecision = result.ReadinessDecision
	}

	if index.Decision.Status != storedExpectedDecision {
		add("decision.status = %q, want independently recomputed %q for schema_version %q", index.Decision.Status, storedExpectedDecision, index.SchemaVersion)
	}
	if storedExpectedDecision == DecisionGo && index.RecordStatus != RecordStatusComplete {
		add("record_status = %q, want complete for an independently recomputed go decision", index.RecordStatus)
	}
	if storedExpectedDecision == DecisionNoGo && index.RecordStatus == RecordStatusComplete {
		add("record_status complete is inconsistent with an independently recomputed no-go decision")
	}
	if index.RecordStatus == RecordStatusTemplate && !allRequirementsHaveState(states, StatusOpen) {
		add("record_status template requires every readiness requirement to remain open")
	}

	sort.Strings(result.Issues)
	result.Valid = len(result.Issues) == 0
	if !result.Valid {
		result.Status = StatusFailed
		result.Decision = DecisionNoGo
	}
	result.AuthorizationEligible = result.Valid && result.Decision == DecisionGo && result.AssuranceStatus == AssuranceAuthorizationEligible
	for _, name := range result.FailedGates {
		result.Reasons = append(result.Reasons, "failed readiness requirement: "+name)
	}
	for _, name := range result.OpenGates {
		result.Reasons = append(result.Reasons, "open readiness requirement: "+name)
	}
	for _, name := range result.UnqualifiedEvidence {
		result.Reasons = append(result.Reasons, "unqualified evidence cannot authorize release: "+name)
	}
	if !result.Valid {
		result.Reasons = append(result.Reasons, "semantic verification issues present")
	} else if result.Status == StatusPassed {
		result.Reasons = append(result.Reasons, "all readiness requirements passed")
	}
	sort.Strings(result.Reasons)
	return result
}

// validateReadinessRequirements is shared by the independent verifier and by
// copy-on-write mutation code that must derive lifecycle metadata without
// consulting the currently stored lifecycle decision.
func validateReadinessRequirements(
	add func(string, ...any),
	gates Gates,
	controls PreventiveControls,
) (map[string]string, map[string]string, bool) {
	states := make(map[string]string, 16)
	qualifications := make(map[string]string, 16)
	valid := true
	for _, item := range gateRequirements(gates) {
		state, qualification, gateValid := validateGate(add, "gates."+item.name, item.gate)
		states[item.name] = state
		if state == StatusPassed {
			qualifications[item.name] = qualification
		}
		if !gateValid {
			valid = false
		}
	}
	controlStates, controlQualifications, controlsValid := validatePreventiveControls(add, controls)
	for name, state := range controlStates {
		states[name] = state
	}
	for name, qualification := range controlQualifications {
		qualifications[name] = qualification
	}
	return states, qualifications, valid && controlsValid
}

func aggregateAssuranceStatus(qualifications map[string]string) string {
	if len(qualifications) == 0 {
		return AssuranceNotApplicable
	}
	status := AssuranceAuthorizationEligible
	for _, qualification := range qualifications {
		switch qualification {
		case AssuranceLegacyUnspecified:
			return AssuranceLegacyUnspecified
		case AssuranceOperatorAttested:
			status = AssuranceOperatorAttested
		case AssuranceAuthorizationEligible:
		default:
			return AssuranceLegacyUnspecified
		}
	}
	return status
}

func readinessOutcome(integrityOK bool, states map[string]string, assurancePending bool) (string, string) {
	failed := false
	open := false
	for _, state := range states {
		switch state {
		case StatusFailed:
			failed = true
		case StatusOpen:
			open = true
		}
	}
	switch {
	case !integrityOK || failed:
		return StatusFailed, DecisionNoGo
	case open || assurancePending:
		return StatusOpen, DecisionNoGo
	default:
		return StatusPassed, DecisionGo
	}
}

func gateRequirements(gates Gates) []requirement {
	return []requirement{
		{name: "adoption_pilot_1", gate: gates.AdoptionPilot1},
		{name: "adoption_pilot_2", gate: gates.AdoptionPilot2},
		{name: "aggregate_attempt_1", gate: gates.AggregateAttempt1},
		{name: "aggregate_attempt_2", gate: gates.AggregateAttempt2},
		{name: "critical_finding_review", gate: gates.CriticalFindingReview},
		{name: "draft_asset_verification", gate: gates.DraftAssetVerification},
		{name: "draft_compatibility_7_cells", gate: gates.DraftCompatibility7Cells},
		{name: "draft_external_drivers", gate: gates.DraftExternalDrivers},
		{name: "independent_authoring_reproduction", gate: gates.IndependentAuthoringReproduction},
		{name: "public_asset_verification", gate: gates.PublicAssetVerification},
		{name: "publication", gate: gates.Publication},
		{name: "published_compatibility_7_cells", gate: gates.PublishedCompatibility7Cells},
		{name: "source_compatibility", gate: gates.SourceCompatibility},
	}
}

func validateCandidate(add func(string, ...any), candidate Candidate) bool {
	valid := true
	if !semVerPattern.MatchString(candidate.Version) {
		add("candidate.version = %q, want canonical SemVer", candidate.Version)
		valid = false
	}
	if candidate.Tag != "v"+candidate.Version {
		add("candidate.tag = %q, want %q", candidate.Tag, "v"+candidate.Version)
		valid = false
	}
	if !validCommit(candidate.GitCommit) {
		add("candidate.git_commit must be a non-zero full lowercase 40- or 64-character hexadecimal object id")
		valid = false
	}
	if !lowerHex64.MatchString(candidate.AssetFingerprint) {
		add("candidate.asset_fingerprint must be 64 lowercase hexadecimal characters")
		valid = false
	}
	if !validPackID(candidate.ScenarioPack.ID) {
		add("candidate.scenario_pack.id must contain a lowercase alphanumeric character and only lowercase alphanumeric, dot, underscore, or hyphen characters")
		valid = false
	}
	if !semVerPattern.MatchString(candidate.ScenarioPack.Version) {
		add("candidate.scenario_pack.version = %q, want canonical SemVer", candidate.ScenarioPack.Version)
		valid = false
	}
	if !validDigest(candidate.ScenarioPack.Digest) {
		add("candidate.scenario_pack.digest must be a lowercase sha256 digest")
		valid = false
	}
	return valid
}

func validateLineage(add func(string, ...any), schemaVersion string, lineage *Lineage) bool {
	switch schemaVersion {
	case SchemaVersionV1:
		if lineage != nil {
			add("lineage must be absent for schema_version %q", SchemaVersionV1)
			return false
		}
		return true
	case SchemaVersionV2, SchemaVersionV3:
		if lineage == nil {
			add("lineage is required for schema_version %q", schemaVersion)
			return false
		}
		valid := true
		if lineage.Revision < 0 {
			add("lineage.revision must be non-negative")
			valid = false
		} else if lineage.Revision > maxJSONSafeInteger {
			add("lineage.revision must be no greater than %d", maxJSONSafeInteger)
			valid = false
		}
		if lineage.Revision == 0 {
			if lineage.PreviousIndexDigest != nil {
				add("lineage.previous_index_digest must be absent for revision zero")
				valid = false
			}
		} else if lineage.PreviousIndexDigest == nil || !validDigest(*lineage.PreviousIndexDigest) {
			add("lineage.previous_index_digest must be a lowercase sha256 digest for revision %d", lineage.Revision)
			valid = false
		}
		return valid
	default:
		return false
	}
}

func validateDecisionShape(add func(string, ...any), decision Decision) bool {
	valid := true
	if decision.Scope != DecisionScope {
		add("decision.scope = %q, want %q", decision.Scope, DecisionScope)
		valid = false
	}
	if !oneOf(decision.Status, DecisionNoGo, DecisionGo) {
		add("decision.status = %q, want no-go or go", decision.Status)
		valid = false
	}
	if !validDateTime(decision.RecordedAt) {
		add("decision.recorded_at must be an RFC3339 date-time")
		valid = false
	}
	if len(decision.Reasons) == 0 {
		add("decision.reasons must contain at least one reason")
		valid = false
	}
	for index, reason := range decision.Reasons {
		if !validLength(reason, 1, 1000) {
			add("decision.reasons[%d] must contain 1 to 1000 characters", index)
			valid = false
		}
	}
	return valid
}

func validateGate(add func(string, ...any), path string, gate Gate) (string, string, bool) {
	valid := true
	if gate.Note != nil && !validLength(*gate.Note, 1, 1000) {
		add("%s.note must contain 1 to 1000 characters when present", path)
		valid = false
	}

	switch gate.Status {
	case GateStatusOpen:
		if gate.Evidence != nil {
			add("%s.evidence must be absent while status is open", path)
			valid = false
		}
		if !valid {
			return StatusFailed, AssuranceNotApplicable, false
		}
		return StatusOpen, AssuranceNotApplicable, true
	case GateStatusPassed, GateStatusFailed:
		if gate.Evidence == nil {
			add("%s.evidence is required while status is %s", path, gate.Status)
			return StatusFailed, AssuranceNotApplicable, false
		}
		evidenceValid, qualification := validateEvidence(add, path+".evidence", *gate.Evidence)
		if !evidenceValid {
			valid = false
		}
		if !valid || gate.Status == GateStatusFailed {
			return StatusFailed, qualification, valid
		}
		return StatusPassed, qualification, true
	default:
		add("%s.status = %q, want open, passed, or failed", path, gate.Status)
		return StatusFailed, AssuranceNotApplicable, false
	}
}

func validatePreventiveControls(add func(string, ...any), controls PreventiveControls) (map[string]string, map[string]string, bool) {
	states := make(map[string]string, 3)
	qualifications := make(map[string]string, 3)
	valid := true

	tagState, tagQualification, tagValid := validateTagRuleset(add, controls.TagRuleset)
	states["preventive_controls.tag_ruleset"] = tagState
	if tagState == StatusPassed {
		qualifications["preventive_controls.tag_ruleset"] = tagQualification
	}
	if !tagValid {
		valid = false
	}
	reviewState, reviewQualification, reviewValid := validateAdminReview(add, controls.TagRuleset.BypassReview)
	states["preventive_controls.tag_ruleset.bypass_review"] = reviewState
	if reviewState == StatusPassed {
		qualifications["preventive_controls.tag_ruleset.bypass_review"] = reviewQualification
	}
	if !reviewValid {
		valid = false
	}
	immutableState, immutableQualification, immutableValid := validateImmutableReleases(add, controls.ImmutableReleases)
	states["preventive_controls.immutable_releases"] = immutableState
	if immutableState == StatusPassed {
		qualifications["preventive_controls.immutable_releases"] = immutableQualification
	}
	if !immutableValid {
		valid = false
	}
	return states, qualifications, valid
}

func validateTagRuleset(add func(string, ...any), control TagRuleset) (string, string, bool) {
	const path = "preventive_controls.tag_ruleset"
	valid := true
	if control.Target != "tag" {
		add("%s.target = %q, want tag", path, control.Target)
		valid = false
	}
	if control.Enforcement != "active" {
		add("%s.enforcement = %q, want active", path, control.Enforcement)
		valid = false
	}
	if control.IncludePattern != "refs/tags/v*" {
		add("%s.include_pattern = %q, want refs/tags/v*", path, control.IncludePattern)
		valid = false
	}
	if control.Excludes == nil || len(control.Excludes) != 0 {
		add("%s.excludes must be a present empty array", path)
		valid = false
	}
	if control.CreationRestricted == nil || !*control.CreationRestricted {
		add("%s.creation_restricted must be true", path)
		valid = false
	}
	if control.UpdateProhibited == nil || !*control.UpdateProhibited {
		add("%s.update_prohibited must be true", path)
		valid = false
	}
	if control.DeletionProhibited == nil || !*control.DeletionProhibited {
		add("%s.deletion_prohibited must be true", path)
		valid = false
	}

	switch control.Status {
	case ControlStatusOpen:
		if control.APIEvidence != nil {
			add("%s.api_evidence must be absent while status is open", path)
			valid = false
		}
		if !valid {
			return StatusFailed, AssuranceNotApplicable, false
		}
		return StatusOpen, AssuranceNotApplicable, true
	case ControlStatusVerified:
		if control.APIEvidence == nil {
			add("%s.api_evidence is required while status is verified", path)
			return StatusFailed, AssuranceNotApplicable, false
		}
		evidenceValid, qualification := validateEvidence(add, path+".api_evidence", *control.APIEvidence)
		if !evidenceValid {
			valid = false
		}
		if !valid {
			return StatusFailed, qualification, false
		}
		return StatusPassed, qualification, true
	default:
		add("%s.status = %q, want open or verified", path, control.Status)
		return StatusFailed, AssuranceNotApplicable, false
	}
}

func validateAdminReview(add func(string, ...any), review AdminReview) (string, string, bool) {
	const path = "preventive_controls.tag_ruleset.bypass_review"
	switch review.Status {
	case ReviewStatusOpen:
		if review.Reviewer != nil || review.ReviewedAt != nil || review.RulesetID != nil || review.RulesetUpdatedAt != nil || review.Evidence != nil {
			add("%s review details must be absent while status is open", path)
			return StatusFailed, AssuranceNotApplicable, false
		}
		return StatusOpen, AssuranceNotApplicable, true
	case ReviewStatusAdminReviewed:
		valid := true
		qualification := AssuranceNotApplicable
		if review.Reviewer == nil || !validLength(valueOrEmpty(review.Reviewer), 1, 128) {
			add("%s.reviewer must contain 1 to 128 characters", path)
			valid = false
		}
		if review.ReviewedAt == nil || !validDateTime(valueOrEmpty(review.ReviewedAt)) {
			add("%s.reviewed_at must be an RFC3339 date-time", path)
			valid = false
		}
		if review.RulesetID == nil || *review.RulesetID < 1 {
			add("%s.ruleset_id must be at least 1", path)
			valid = false
		} else if *review.RulesetID > maxJSONSafeInteger {
			add("%s.ruleset_id must be no greater than %d", path, maxJSONSafeInteger)
			valid = false
		}
		if review.RulesetUpdatedAt == nil || !validDateTime(valueOrEmpty(review.RulesetUpdatedAt)) {
			add("%s.ruleset_updated_at must be an RFC3339 date-time", path)
			valid = false
		}
		if reviewedAt, reviewedOK := parseDateTime(valueOrEmpty(review.ReviewedAt)); reviewedOK {
			if updatedAt, updatedOK := parseDateTime(valueOrEmpty(review.RulesetUpdatedAt)); updatedOK && reviewedAt.Before(updatedAt) {
				add("%s.reviewed_at must not precede ruleset_updated_at", path)
				valid = false
			}
		}
		if review.Evidence == nil {
			add("%s.evidence is required while status is admin-reviewed", path)
			valid = false
		} else {
			evidenceValid, evidenceQualification := validateEvidence(add, path+".evidence", *review.Evidence)
			qualification = evidenceQualification
			if !evidenceValid {
				valid = false
			}
		}
		if !valid {
			return StatusFailed, qualification, false
		}
		return StatusPassed, qualification, true
	default:
		add("%s.status = %q, want open or admin-reviewed", path, review.Status)
		return StatusFailed, AssuranceNotApplicable, false
	}
}

func validateImmutableReleases(add func(string, ...any), control ImmutableReleases) (string, string, bool) {
	const path = "preventive_controls.immutable_releases"
	valid := true
	if control.Enabled == nil {
		add("%s.enabled must be present", path)
		valid = false
	}
	switch control.Status {
	case ControlStatusOpen:
		if control.APIEvidence != nil {
			add("%s.api_evidence must be absent while status is open", path)
			valid = false
		}
		if !valid {
			return StatusFailed, AssuranceNotApplicable, false
		}
		return StatusOpen, AssuranceNotApplicable, true
	case ControlStatusVerified:
		qualification := AssuranceNotApplicable
		if control.Enabled == nil || !*control.Enabled {
			add("%s.enabled must be true while status is verified", path)
			valid = false
		}
		if control.APIEvidence == nil {
			add("%s.api_evidence is required while status is verified", path)
			valid = false
		} else {
			evidenceValid, evidenceQualification := validateEvidence(add, path+".api_evidence", *control.APIEvidence)
			qualification = evidenceQualification
			if !evidenceValid {
				valid = false
			}
		}
		if !valid {
			return StatusFailed, qualification, false
		}
		return StatusPassed, qualification, true
	default:
		add("%s.status = %q, want open or verified", path, control.Status)
		return StatusFailed, AssuranceNotApplicable, false
	}
}

func validateEvidence(add func(string, ...any), path string, evidence Evidence) (bool, string) {
	valid := true
	if !validDurableRef(evidence.Ref) {
		add("%s.ref must be an absolute durable https, s3, gs, or urn URI", path)
		valid = false
	}
	if !validDigest(evidence.Digest) {
		add("%s.digest must be a lowercase sha256 digest", path)
		valid = false
	}
	if !validDateTime(evidence.CapturedAt) {
		add("%s.captured_at must be an RFC3339 date-time", path)
		valid = false
	}
	if evidence.RunID != nil && !validLength(*evidence.RunID, 1, 128) {
		add("%s.run_id must contain 1 to 128 characters when present", path)
		valid = false
	}
	if evidence.RunAttempt != nil {
		if *evidence.RunAttempt < 1 {
			add("%s.run_attempt must be at least 1 when present", path)
			valid = false
		} else if *evidence.RunAttempt > maxJSONSafeInteger {
			add("%s.run_attempt must be no greater than %d", path, maxJSONSafeInteger)
			valid = false
		}
	}
	if evidence.Record == nil && evidence.Assurance == nil {
		return valid, AssuranceLegacyUnspecified
	}
	if evidence.Record == nil || evidence.Assurance == nil {
		add("%s.record and assurance must either both be present or both be absent", path)
		return false, AssuranceLegacyUnspecified
	}
	if !validateEvidenceRecord(add, path+".record", path, *evidence.Record) {
		valid = false
	}
	if *evidence.Assurance != (EvidenceAssurance{Durability: EvidenceDurabilityAsserted, Authenticity: EvidenceAuthenticityUnverified}) {
		add("%s.assurance must be the supported operator-asserted, remote-authenticity-unverified trust pair", path)
		valid = false
	}
	return valid, AssuranceOperatorAttested
}

func validateEvidenceRecord(add func(string, ...any), recordPath, evidencePath string, record EvidenceRecord) bool {
	wantSchema, wantType, wantAdapter := "", "", ""
	adapterRequired := false
	switch evidencePath {
	case "gates.source_compatibility.evidence":
		wantSchema, wantType, wantAdapter = CompatibilityVerificationSchema, CompatibilityVerificationType, CompatibilitySourceAdapter
		adapterRequired = true
	case "gates.aggregate_attempt_1.evidence":
		wantSchema, wantType, wantAdapter = AggregateVerificationSchema, AggregateVerificationType, AggregateAttempt1Adapter
		adapterRequired = true
	case "gates.aggregate_attempt_2.evidence":
		wantSchema, wantType, wantAdapter = AggregateVerificationSchema, AggregateVerificationType, AggregateAttempt2Adapter
		adapterRequired = true
	case "gates.draft_asset_verification.evidence":
		wantSchema, wantType, wantAdapter = ReleaseAssetVerificationSchema, ReleaseAssetVerificationType, ReleaseAssetDraftAdapter
		adapterRequired = true
	case "gates.public_asset_verification.evidence":
		wantSchema, wantType, wantAdapter = ReleaseAssetVerificationSchema, ReleaseAssetVerificationType, ReleaseAssetPublishedAdapter
		adapterRequired = true
	case "gates.draft_external_drivers.evidence":
		wantSchema, wantType, wantAdapter = ExternalDriverVerificationSchema, ExternalDriverVerificationType, ExternalDriverVerificationAdapter
	case "gates.publication.evidence":
		wantSchema, wantType, wantAdapter = ReleasePublicationSchema, ReleasePublicationType, ReleasePublicationAdapter
		adapterRequired = true
	case "gates.draft_compatibility_7_cells.evidence":
		wantSchema, wantType, wantAdapter = CompatibilityVerificationSchema, CompatibilityVerificationType, CompatibilityDraftAdapter
		adapterRequired = true
	case "gates.published_compatibility_7_cells.evidence":
		wantSchema, wantType, wantAdapter = CompatibilityVerificationSchema, CompatibilityVerificationType, CompatibilityPublishedAdapter
		adapterRequired = true
	}
	if wantSchema != "" {
		valid := true
		if record.SchemaVersion != wantSchema {
			add("%s.schema_version = %q, want %q", recordPath, record.SchemaVersion, wantSchema)
			valid = false
		}
		if record.ArtifactType != wantType {
			add("%s.artifact_type = %q, want %q", recordPath, record.ArtifactType, wantType)
			valid = false
		}
		if record.Adapter != wantAdapter && (adapterRequired || record.Adapter != "") {
			add("%s.adapter = %q, want %q", recordPath, record.Adapter, wantAdapter)
			valid = false
		}
		return valid
	}
	add("%s has no registered typed evidence contract", recordPath)
	return false
}

func validDurableRef(value string) bool {
	for _, character := range value {
		if character <= 0x20 || character == 0x7f {
			return false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return parsed.Host != "" && parsed.Path != "" && parsed.Path != "/" && !isEphemeralGitHubActionsRef(parsed.Hostname(), parsed.Path)
	case "s3", "gs":
		return parsed.Host != "" && parsed.Path != "" && parsed.Path != "/"
	case "urn":
		return validURNRef(parsed)
	default:
		return false
	}
}

func validURNRef(parsed *url.URL) bool {
	if parsed.Host != "" || parsed.RawQuery != "" || parsed.Opaque == "" {
		return false
	}
	parts := strings.SplitN(parsed.Opaque, ":", 2)
	if len(parts) != 2 || !urnNIDPattern.MatchString(parts[0]) || strings.EqualFold(parts[0], "urn") {
		return false
	}
	return validURNNamespaceSpecificString(parts[1])
}

func validURNNamespaceSpecificString(value string) bool {
	if value == "" || value[0] == '/' {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if isURNPChar(character) || character == '/' {
			continue
		}
		if character != '%' || index+2 >= len(value) || !isHexDigit(value[index+1]) || !isHexDigit(value[index+2]) {
			return false
		}
		index += 2
	}
	return true
}

func isURNPChar(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' ||
		strings.ContainsRune("-._~!$&'()*+,;=:@", rune(character))
}

func isHexDigit(character byte) bool {
	return character >= '0' && character <= '9' || character >= 'a' && character <= 'f' || character >= 'A' && character <= 'F'
}

func isEphemeralGitHubActionsRef(parsedHost, parsedPath string) bool {
	host := strings.TrimRight(strings.ToLower(parsedHost), ".")
	path := strings.ToLower(parsedPath)
	if host == "github.com" && (strings.Contains(path, "/actions/runs/") || strings.Contains(path, "/actions/artifacts/")) {
		return true
	}
	if host == "api.github.com" && strings.Contains(path, "/actions/artifacts/") {
		return true
	}
	return host == "pipelines.actions.githubusercontent.com" || host == "objects.githubusercontent.com"
}

func validCommit(value string) bool {
	if !lowerHex40.MatchString(value) && !lowerHex64.MatchString(value) {
		return false
	}
	return strings.Trim(value, "0") != ""
}

func hasTemplateSentinel(candidate Candidate) bool {
	return allCharacters(candidate.GitCommit, '1') ||
		allCharacters(candidate.AssetFingerprint, '1') ||
		strings.HasPrefix(candidate.ScenarioPack.Digest, "sha256:") && allCharacters(strings.TrimPrefix(candidate.ScenarioPack.Digest, "sha256:"), '1')
}

func allCharacters(value string, want byte) bool {
	if value == "" {
		return false
	}
	for index := range value {
		if value[index] != want {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && lowerHex64.MatchString(strings.TrimPrefix(value, "sha256:"))
}

func validPackID(value string) bool {
	containsAlphanumeric := false
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			containsAlphanumeric = true
		case character == '.', character == '_', character == '-':
		default:
			return false
		}
	}
	return containsAlphanumeric
}

func validDateTime(value string) bool {
	_, valid := parseDateTime(value)
	return valid
}

func parseDateTime(value string) (time.Time, bool) {
	if !validJSONSchemaDateTime(value) {
		return time.Time{}, false
	}
	normalized := []byte(value)
	normalized[10] = 'T'
	if normalized[len(normalized)-1] == 'z' {
		normalized[len(normalized)-1] = 'Z'
	}
	leapSecond := normalized[17] == '6' && normalized[18] == '0'
	if leapSecond {
		normalized[17], normalized[18] = '5', '9'
	}
	if normalized[19] == '.' {
		fractionEnd := 20
		for fractionEnd < len(normalized) && normalized[fractionEnd] >= '0' && normalized[fractionEnd] <= '9' {
			fractionEnd++
		}
		if fractionEnd-20 > 9 {
			normalized = append(normalized[:29], normalized[fractionEnd:]...)
		}
	}
	parsed, err := time.Parse(time.RFC3339Nano, string(normalized))
	if err != nil {
		return time.Time{}, false
	}
	if leapSecond {
		parsed = parsed.Add(time.Second)
	}
	return parsed, true
}

// validJSONSchemaDateTime mirrors the RFC 3339 date-time profile asserted by
// the repository's Draft 2020-12 schema validator, including lower-case t/z,
// leap seconds, arbitrary precision fractions, and offsets up to 23:59.
func validJSONSchemaDateTime(value string) bool {
	if len(value) < 20 || (value[10] != 'T' && value[10] != 't') {
		return false
	}
	if _, err := time.Parse("2006-01-02", value[:10]); err != nil {
		return false
	}
	timePart := value[11:]
	if len(timePart) < 9 || timePart[2] != ':' || timePart[5] != ':' {
		return false
	}
	hour, hourOK := twoDigits(timePart[0:2])
	minute, minuteOK := twoDigits(timePart[3:5])
	second, secondOK := twoDigits(timePart[6:8])
	if !hourOK || !minuteOK || !secondOK || hour > 23 || minute > 59 || second > 60 {
		return false
	}
	remainder := timePart[8:]
	if strings.HasPrefix(remainder, ".") {
		fractionEnd := 1
		for fractionEnd < len(remainder) && remainder[fractionEnd] >= '0' && remainder[fractionEnd] <= '9' {
			fractionEnd++
		}
		if fractionEnd == 1 {
			return false
		}
		remainder = remainder[fractionEnd:]
	}

	utcMinutes := hour*60 + minute
	if remainder != "z" && remainder != "Z" {
		if len(remainder) != 6 || (remainder[0] != '+' && remainder[0] != '-') || remainder[3] != ':' {
			return false
		}
		offsetHour, offsetHourOK := twoDigits(remainder[1:3])
		offsetMinute, offsetMinuteOK := twoDigits(remainder[4:6])
		if !offsetHourOK || !offsetMinuteOK || offsetHour > 23 || offsetMinute > 59 {
			return false
		}
		offset := offsetHour*60 + offsetMinute
		if remainder[0] == '+' {
			utcMinutes -= offset
		} else {
			utcMinutes += offset
		}
		utcMinutes %= 24 * 60
		if utcMinutes < 0 {
			utcMinutes += 24 * 60
		}
	}
	return second < 60 || utcMinutes == 23*60+59
}

func twoDigits(value string) (int, bool) {
	if len(value) != 2 || value[0] < '0' || value[0] > '9' || value[1] < '0' || value[1] > '9' {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}

func validLength(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length >= minimum && length <= maximum
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func allRequirementsHaveState(states map[string]string, want string) bool {
	for _, state := range states {
		if state != want {
			return false
		}
	}
	return true
}
