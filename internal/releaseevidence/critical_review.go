package releaseevidence

import (
	"fmt"
	"regexp"
	"sort"
)

const (
	CriticalFindingReviewSchema  = "pgworkbench.critical-finding-review/v1"
	CriticalFindingReviewType    = "pgworkbench.critical-finding-review"
	CriticalFindingReviewAdapter = "pgworkbench.critical-finding-review/v1"
)

var criticalReviewIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var criticalFindingIDPattern = regexp.MustCompile(`^[A-Z]+-[0-9]+$`)

// CriticalFindingReview is a signed, human-authored review. It deliberately
// binds only an operator-attested durable reference in the index: a signed
// record can close the review gate, but cannot make a v3 index authorizing.
type CriticalFindingReview struct {
	SchemaVersion      string                  `json:"schema_version"`
	ArtifactType       string                  `json:"artifact_type"`
	RecordStatus       string                  `json:"record_status"`
	ReviewID           string                  `json:"review_id"`
	Candidate          CriticalReviewCandidate `json:"candidate"`
	Scope              []string                `json:"scope"`
	RepositoryControls CriticalReviewControls  `json:"repository_controls"`
	Findings           []CriticalFinding       `json:"findings"`
	Decision           CriticalReviewDecision  `json:"decision"`
	Signoff            CriticalReviewSignoff   `json:"signoff"`
}

type CriticalReviewCandidate struct {
	Version    string `json:"version"`
	Tag        string `json:"tag"`
	GitCommit  string `json:"git_commit"`
	PackDigest string `json:"pack_digest"`
}

type CriticalReviewControls struct {
	TagRuleset        CriticalTagRuleset `json:"tag_ruleset"`
	ImmutableReleases CriticalImmutable  `json:"immutable_releases"`
}

type CriticalTagRuleset struct {
	Status               string                  `json:"status"`
	ID                   int64                   `json:"id"`
	UpdatedAt            string                  `json:"updated_at"`
	Target               string                  `json:"target"`
	Enforcement          string                  `json:"enforcement"`
	IncludePattern       string                  `json:"include_pattern"`
	Excludes             []string                `json:"excludes"`
	CreationRestricted   *bool                   `json:"creation_restricted"`
	UpdateProhibited     *bool                   `json:"update_prohibited"`
	DeletionProhibited   *bool                   `json:"deletion_prohibited"`
	BypassActorsReviewed *bool                   `json:"bypass_actors_reviewed"`
	Evidence             *CriticalReviewEvidence `json:"evidence"`
}

type CriticalImmutable struct {
	Status          string                  `json:"status"`
	Enabled         *bool                   `json:"enabled"`
	EnforcedByOwner *bool                   `json:"enforced_by_owner"`
	Evidence        *CriticalReviewEvidence `json:"evidence"`
}

type CriticalReviewEvidence struct {
	Ref        string `json:"ref"`
	Digest     string `json:"digest"`
	CapturedAt string `json:"captured_at"`
}

type CriticalFinding struct {
	ID         string                  `json:"id"`
	Category   string                  `json:"category"`
	Severity   string                  `json:"severity"`
	Status     string                  `json:"status"`
	Summary    string                  `json:"summary"`
	Resolution string                  `json:"resolution"`
	Evidence   *CriticalReviewEvidence `json:"evidence"`
}

type CriticalReviewDecision struct {
	Status     string `json:"status"`
	RecordedAt string `json:"recorded_at"`
	Rationale  string `json:"rationale"`
}

type CriticalReviewSignoff struct {
	Status            string                  `json:"status"`
	Reviewer          string                  `json:"reviewer"`
	ReviewerRole      string                  `json:"reviewer_role"`
	SignedAt          string                  `json:"signed_at"`
	Statement         string                  `json:"statement"`
	SignatureEvidence *CriticalReviewEvidence `json:"signature_evidence"`
}

func validateCriticalFindingReview(record CriticalFindingReview, candidate Candidate) error {
	if record.SchemaVersion != CriticalFindingReviewSchema || record.ArtifactType != CriticalFindingReviewType {
		return fmt.Errorf("unsupported critical-finding review type or schema")
	}
	if record.RecordStatus != "signed" || !criticalReviewIDPattern.MatchString(record.ReviewID) {
		return fmt.Errorf("critical-finding review must be signed with a canonical review_id")
	}
	if record.Candidate.Version != candidate.Version || record.Candidate.Tag != candidate.Tag || record.Candidate.GitCommit != candidate.GitCommit || record.Candidate.PackDigest != candidate.ScenarioPack.Digest {
		return fmt.Errorf("critical-finding review candidate does not match the predecessor index candidate")
	}
	if !exactCriticalScope(record.Scope) {
		return fmt.Errorf("scope must contain the exact four critical-review categories")
	}
	if err := validateCriticalReviewControls(record.RepositoryControls); err != nil {
		return err
	}
	if err := validateCriticalFindings(record.Findings); err != nil {
		return err
	}
	if !oneOf(record.Decision.Status, DecisionGo, DecisionNoGo) || !canonicalTimestampPattern.MatchString(record.Decision.RecordedAt) || !validDateTime(record.Decision.RecordedAt) || !validLength(record.Decision.Rationale, 1, 4000) {
		return fmt.Errorf("decision must have a canonical no-go/go status, timestamp, and rationale")
	}
	if err := validateCriticalSignoff(record.Signoff); err != nil {
		return err
	}
	decisionTime, _ := parseDateTime(record.Decision.RecordedAt)
	signedTime, _ := parseDateTime(record.Signoff.SignedAt)
	if decisionTime.Before(signedTime) {
		return fmt.Errorf("decision.recorded_at must not precede signoff.signed_at")
	}
	if record.Decision.Status == DecisionGo && hasUnresolvedCriticalFinding(record.Findings) {
		return fmt.Errorf("go decision cannot contain an open or accepted critical finding")
	}
	return nil
}

func exactCriticalScope(scope []string) bool {
	want := []string{"data-loss", "evidence-integrity", "portability", "security"}
	got := append([]string(nil), scope...)
	sort.Strings(got)
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func validateCriticalReviewControls(controls CriticalReviewControls) error {
	tag := controls.TagRuleset
	if tag.Status != ControlStatusVerified || tag.ID < 1 || !canonicalTimestampPattern.MatchString(tag.UpdatedAt) || !validDateTime(tag.UpdatedAt) || tag.Target != "tag" || tag.Enforcement != "active" || tag.IncludePattern != "refs/tags/v*" || len(tag.Excludes) != 0 || !requiredBool(tag.CreationRestricted, true) || !requiredBool(tag.UpdateProhibited, true) || !requiredBool(tag.DeletionProhibited, true) || !requiredBool(tag.BypassActorsReviewed, true) {
		return fmt.Errorf("tag_ruleset does not meet the fixed preventive-control contract")
	}
	if err := validateCriticalEvidence(tag.Evidence, "tag_ruleset.evidence"); err != nil {
		return err
	}
	immutable := controls.ImmutableReleases
	if immutable.Status != ControlStatusVerified || !requiredBool(immutable.Enabled, true) || immutable.EnforcedByOwner == nil {
		return fmt.Errorf("immutable_releases does not meet the fixed preventive-control contract")
	}
	return validateCriticalEvidence(immutable.Evidence, "immutable_releases.evidence")
}

func validateCriticalFindings(findings []CriticalFinding) error {
	seen := make(map[string]struct{}, len(findings))
	for index, finding := range findings {
		if !criticalFindingIDPattern.MatchString(finding.ID) || !oneOf(finding.Category, "security", "data-loss", "portability", "evidence-integrity") || !oneOf(finding.Severity, "critical", "high", "medium", "low") || !oneOf(finding.Status, "open", "closed", "accepted") || !validLength(finding.Summary, 1, 2000) {
			return fmt.Errorf("findings[%d] is not a bounded critical finding", index)
		}
		if _, exists := seen[finding.ID]; exists {
			return fmt.Errorf("findings contain duplicate id %q", finding.ID)
		}
		seen[finding.ID] = struct{}{}
		if finding.Status == "closed" {
			if !validLength(finding.Resolution, 1, 4000) {
				return fmt.Errorf("closed finding %q requires a resolution", finding.ID)
			}
			if err := validateCriticalEvidence(finding.Evidence, "findings.evidence"); err != nil {
				return err
			}
		} else if finding.Resolution != "" || finding.Evidence != nil {
			return fmt.Errorf("non-closed finding %q must not carry resolution evidence", finding.ID)
		}
	}
	return nil
}

func hasUnresolvedCriticalFinding(findings []CriticalFinding) bool {
	for _, finding := range findings {
		if finding.Severity == "critical" && finding.Status != "closed" {
			return true
		}
	}
	return false
}

func validateCriticalSignoff(signoff CriticalReviewSignoff) error {
	if signoff.Status != "signed" || !validLength(signoff.Reviewer, 1, 128) || signoff.ReviewerRole != "repository-administrator" || !canonicalTimestampPattern.MatchString(signoff.SignedAt) || !validDateTime(signoff.SignedAt) || !validLength(signoff.Statement, 1, 4000) {
		return fmt.Errorf("critical-finding review requires a canonical administrator signoff")
	}
	return validateCriticalEvidence(signoff.SignatureEvidence, "signoff.signature_evidence")
}

func validateCriticalEvidence(evidence *CriticalReviewEvidence, path string) error {
	if evidence == nil || !validDurableRef(evidence.Ref) || !validDigest(evidence.Digest) || !canonicalTimestampPattern.MatchString(evidence.CapturedAt) || !validDateTime(evidence.CapturedAt) {
		return fmt.Errorf("%s must contain a durable reference, digest, and canonical timestamp", path)
	}
	return nil
}
