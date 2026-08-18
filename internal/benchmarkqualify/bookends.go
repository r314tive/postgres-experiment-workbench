package benchmarkqualify

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	// BookendStatusRecordedPolicyPassed means that two independently verified,
	// operator-recorded artifacts passed the same policy, reported the same
	// stable host class, and form a canonical chronological interval. It is not
	// host identity, execution provenance, current-state, or remote attestation.
	BookendStatusRecordedPolicyPassed = "recorded-policy-passed"
	BookendStatusUnqualified          = "unqualified"
)

// StableString is a source-independent projection of a string observation.
// Availability remains explicit so an unavailable value cannot be confused
// with an observed empty value.
type StableString struct {
	Availability string `json:"availability"`
	Value        string `json:"value,omitempty"`
}

// StableStrings is a source-independent projection of a string-list
// observation. Values are copied and sorted when projected.
type StableStrings struct {
	Availability string   `json:"availability"`
	Values       []string `json:"values,omitempty"`
}

// StableUint is a source-independent projection of an unsigned observation.
type StableUint struct {
	Availability string  `json:"availability"`
	Value        *uint64 `json:"value,omitempty"`
}

// StableHostClass contains only the recorded fields expected to remain stable
// between the before and after bookends. It deliberately excludes hostname,
// filesystem paths, user identity, volatile capacity, load, headroom, and
// thermal observations. Equality is a bounded recorded-content comparison,
// not proof that both observations came from the same physical host.
type StableHostClass struct {
	OS              StableString  `json:"os"`
	Architecture    StableString  `json:"architecture"`
	Kernel          StableString  `json:"kernel"`
	CPUModel        StableString  `json:"cpu_model"`
	LogicalCPUs     StableUint    `json:"logical_cpus"`
	MemoryTotal     StableUint    `json:"memory_total_bytes"`
	StorageLabel    string        `json:"storage_label"`
	StorageFS       StableString  `json:"storage_filesystem"`
	StorageTotal    StableUint    `json:"storage_total_bytes"`
	Clocksource     StableString  `json:"clocksource"`
	Governors       StableStrings `json:"governors"`
	ClientPlacement StableString  `json:"client_placement"`
}

// BookendAssessment is a deterministic, bounded assessment of two recorded
// qualification artifacts. The before/after digests are exposed separately so
// a mismatch remains inspectable; no field is a signature or attestation.
type BookendAssessment struct {
	Status                      string   `json:"status"`
	Reasons                     []string `json:"reasons"`
	BeforeArtifactDigest        string   `json:"before_artifact_digest"`
	AfterArtifactDigest         string   `json:"after_artifact_digest"`
	BeforePolicyDigest          string   `json:"before_policy_digest"`
	AfterPolicyDigest           string   `json:"after_policy_digest"`
	BeforeStableHostClassDigest string   `json:"before_stable_host_class_digest"`
	AfterStableHostClassDigest  string   `json:"after_stable_host_class_digest"`
}

// DecisionProfileComplete reports whether policy contains the minimum fixed
// observation set required by the counterbalanced performance-decision gate.
// A single easy operator-selected check is intentionally insufficient.
func DecisionProfileComplete(policy Policy) bool {
	return policy.Strict &&
		policy.MinMemoryAvailablePct != nil &&
		policy.MinStorageAvailablePct != nil &&
		policy.MaxLoad1PerCPU != nil &&
		policy.RequiredClocksource != "" &&
		policy.RequiredGovernor != "" &&
		policy.RequiredClientPlacement != ""
}

// PolicyDigest returns the deterministic content digest of a policy. It does
// not imply that the policy is valid or that it passed; Verify and
// AssessBookends enforce those properties independently.
func PolicyDigest(policy Policy) (string, error) {
	return digestJSON(policy)
}

// ProjectStableHostClass produces a source-independent, immutable projection
// of the selected stable snapshot fields. It intentionally makes no host
// identity or provenance claim.
func ProjectStableHostClass(snapshot Snapshot) StableHostClass {
	governors := append([]string(nil), snapshot.Power.Governors.Values...)
	sort.Strings(governors)
	return StableHostClass{
		OS:           projectStableString(snapshot.Platform.OS),
		Architecture: projectStableString(snapshot.Platform.Architecture),
		Kernel:       projectStableString(snapshot.Platform.Kernel),
		CPUModel:     projectStableString(snapshot.CPU.Model),
		LogicalCPUs:  projectStableUint(snapshot.CPU.LogicalCPUs),
		MemoryTotal:  projectStableUint(snapshot.Memory.TotalBytes),
		StorageLabel: snapshot.Storage.Label,
		StorageFS:    projectStableString(snapshot.Storage.Filesystem),
		StorageTotal: projectStableUint(snapshot.Storage.TotalBytes),
		Clocksource:  projectStableString(snapshot.Clock.Clocksource),
		Governors: StableStrings{
			Availability: snapshot.Power.Governors.Availability,
			Values:       governors,
		},
		ClientPlacement: projectStableString(snapshot.Client.Placement),
	}
}

// StableHostClassDigest returns the deterministic content digest of a stable
// host class projection. The digest protects recorded-content integrity only.
func StableHostClassDigest(class StableHostClass) (string, error) {
	return digestJSON(class)
}

// AssessBookends independently verifies both artifacts, recomputes their
// policy and stable-class digests, and fails closed on any inconsistency. A
// successful result remains bounded to the unsigned, operator-recorded
// contents and does not attest host identity or current state.
func AssessBookends(before, after Artifact) BookendAssessment {
	assessment := BookendAssessment{
		Status:               BookendStatusUnqualified,
		Reasons:              make([]string, 0),
		BeforeArtifactDigest: before.Digest,
		AfterArtifactDigest:  after.Digest,
	}
	add := func(format string, args ...any) {
		assessment.Reasons = append(assessment.Reasons, fmt.Sprintf(format, args...))
	}

	beforeVerification := Verify(before)
	for _, issue := range beforeVerification.Issues {
		add("before artifact invalid: %s", issue)
	}
	afterVerification := Verify(after)
	for _, issue := range afterVerification.Issues {
		add("after artifact invalid: %s", issue)
	}

	var err error
	assessment.BeforePolicyDigest, err = PolicyDigest(before.Policy)
	if err != nil {
		add("before policy cannot be digested: %v", err)
	}
	assessment.AfterPolicyDigest, err = PolicyDigest(after.Policy)
	if err != nil {
		add("after policy cannot be digested: %v", err)
	}
	if !reflect.DeepEqual(before.Policy, after.Policy) {
		add("before and after policies differ")
	}

	beforeClass := ProjectStableHostClass(before.Snapshot)
	afterClass := ProjectStableHostClass(after.Snapshot)
	assessment.BeforeStableHostClassDigest, err = StableHostClassDigest(beforeClass)
	if err != nil {
		add("before stable host class cannot be digested: %v", err)
	}
	assessment.AfterStableHostClassDigest, err = StableHostClassDigest(afterClass)
	if err != nil {
		add("after stable host class cannot be digested: %v", err)
	}
	if !reflect.DeepEqual(beforeClass, afterClass) {
		add("before and after stable host classes differ")
	}

	if before.Verdict != VerdictQualified {
		add("before recorded verdict is %q, want %q", before.Verdict, VerdictQualified)
	}
	if after.Verdict != VerdictQualified {
		add("after recorded verdict is %q, want %q", after.Verdict, VerdictQualified)
	}

	beforeTime, beforeCanonical := parseCanonicalRecordedAt(before.RecordedAt)
	afterTime, afterCanonical := parseCanonicalRecordedAt(after.RecordedAt)
	if !beforeCanonical {
		add("before recorded_at is not canonical UTC RFC3339Nano")
	}
	if !afterCanonical {
		add("after recorded_at is not canonical UTC RFC3339Nano")
	}
	if beforeCanonical && afterCanonical && beforeTime.After(afterTime) {
		add("before recorded_at is later than after recorded_at")
	}

	assessment.Reasons = sortedUnique(assessment.Reasons)
	if len(assessment.Reasons) == 0 {
		assessment.Status = BookendStatusRecordedPolicyPassed
	}
	return assessment
}

func projectStableString(observation StringObservation) StableString {
	return StableString{Availability: observation.Availability, Value: observation.Value}
}

func projectStableUint(observation UintObservation) StableUint {
	projected := StableUint{Availability: observation.Availability}
	if observation.Value != nil {
		value := *observation.Value
		projected.Value = &value
	}
	return projected
}

func parseCanonicalRecordedAt(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !strings.HasSuffix(value, "Z") || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, false
	}
	return parsed, true
}

func sortedUnique(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
