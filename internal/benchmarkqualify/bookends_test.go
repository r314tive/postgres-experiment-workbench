package benchmarkqualify

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPolicyAndStableHostClassDigestsAreDeterministic(t *testing.T) {
	minimumCPUsA := uint64(8)
	minimumCPUsB := uint64(8)
	policyA := Policy{Strict: true, MinLogicalCPUs: &minimumCPUsA, RequiredClocksource: "tsc"}
	policyB := Policy{Strict: true, MinLogicalCPUs: &minimumCPUsB, RequiredClocksource: "tsc"}

	digestA, err := PolicyDigest(policyA)
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := PolicyDigest(policyB)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB {
		t.Fatalf("equivalent policies have different digests: %s != %s", digestA, digestB)
	}
	policyB.RequiredClocksource = "hpet"
	digestB, err = PolicyDigest(policyB)
	if err != nil {
		t.Fatal(err)
	}
	if digestA == digestB {
		t.Fatal("different policies have the same digest")
	}

	artifact := qualifiedBookend(t, time.Unix(1, 0).UTC(), policyA)
	class := ProjectStableHostClass(artifact.Snapshot)
	classDigestA, err := StableHostClassDigest(class)
	if err != nil {
		t.Fatal(err)
	}
	classDigestB, err := StableHostClassDigest(ProjectStableHostClass(artifact.Snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if classDigestA != classDigestB {
		t.Fatalf("stable class digest is not deterministic: %s != %s", classDigestA, classDigestB)
	}

	// The projection owns pointer/slice values rather than aliasing the source.
	*artifact.Snapshot.CPU.LogicalCPUs.Value = 16
	artifact.Snapshot.Power.Governors.Values[0] = "powersave"
	if class.LogicalCPUs.Value == nil || *class.LogicalCPUs.Value != 8 || !reflect.DeepEqual(class.Governors.Values, []string{"performance"}) {
		t.Fatalf("stable class aliases mutable snapshot data: %#v", class)
	}
}

func TestDecisionProfileRequiresEveryFixedGate(t *testing.T) {
	memory, storage, load := 20.0, 20.0, 0.5
	policy := Policy{
		Strict:                  true,
		MinMemoryAvailablePct:   &memory,
		MinStorageAvailablePct:  &storage,
		MaxLoad1PerCPU:          &load,
		RequiredClocksource:     "tsc",
		RequiredGovernor:        "performance",
		RequiredClientPlacement: "separate-host",
	}
	if !DecisionProfileComplete(policy) {
		t.Fatal("complete fixed decision profile was rejected")
	}
	policy.RequiredGovernor = ""
	if DecisionProfileComplete(policy) {
		t.Fatal("incomplete decision profile was accepted")
	}
}

func TestAssessBookendsPassesOnlyBoundedRecordedContract(t *testing.T) {
	minimumCPUs := uint64(8)
	policy := Policy{Strict: true, MinLogicalCPUs: &minimumCPUs, RequiredClientPlacement: "separate-host"}
	before := qualifiedBookend(t, time.Unix(1, 123).UTC(), policy)
	after := qualifiedBookend(t, time.Unix(2, 456).UTC(), policy)

	first := AssessBookends(before, after)
	second := AssessBookends(before, after)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("assessment is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.Status != BookendStatusRecordedPolicyPassed || len(first.Reasons) != 0 {
		t.Fatalf("matching qualified bookends did not pass: %#v", first)
	}
	if first.BeforePolicyDigest == "" || first.BeforePolicyDigest != first.AfterPolicyDigest {
		t.Fatalf("policy digests were not bound: %#v", first)
	}
	if first.BeforeStableHostClassDigest == "" || first.BeforeStableHostClassDigest != first.AfterStableHostClassDigest {
		t.Fatalf("stable class digests were not bound: %#v", first)
	}
	if first.BeforeArtifactDigest == first.AfterArtifactDigest {
		t.Fatal("distinct time-stamped bookends unexpectedly have the same artifact digest")
	}

	// A volatile thermal observation may differ without changing the stable
	// class. It remains independently verified as part of the full artifact.
	changedTemperature := 57.0
	after.Snapshot.Thermal.MaxCelsius.Value = &changedTemperature
	resealArtifact(t, &after)
	assessment := AssessBookends(before, after)
	if assessment.Status != BookendStatusRecordedPolicyPassed {
		t.Fatalf("volatile observation changed stable-class assessment: %#v", assessment)
	}
}

func TestAssessBookendsRejectsPolicyAndStableClassMismatch(t *testing.T) {
	minimumCPUs := uint64(8)
	beforePolicy := Policy{Strict: true, MinLogicalCPUs: &minimumCPUs}
	afterPolicy := Policy{Strict: true, MinLogicalCPUs: &minimumCPUs, RequiredClientPlacement: "separate-host"}
	before := qualifiedBookend(t, time.Unix(1, 0).UTC(), beforePolicy)
	after := qualifiedBookend(t, time.Unix(2, 0).UTC(), afterPolicy)

	assessment := AssessBookends(before, after)
	if assessment.Status != BookendStatusUnqualified || !hasReason(assessment.Reasons, "policies differ") {
		t.Fatalf("policy mismatch passed: %#v", assessment)
	}
	if assessment.BeforePolicyDigest == assessment.AfterPolicyDigest {
		t.Fatalf("policy mismatch was not visible in digests: %#v", assessment)
	}
	assertSortedReasons(t, assessment.Reasons)

	after = qualifiedBookend(t, time.Unix(2, 0).UTC(), beforePolicy)
	after.Snapshot.Platform.Kernel.Value = "6.13.0-other"
	resealArtifact(t, &after)
	if verification := Verify(after); !verification.Valid {
		t.Fatalf("stable-class mismatch fixture is invalid: %#v", verification)
	}
	assessment = AssessBookends(before, after)
	if assessment.Status != BookendStatusUnqualified || !hasReason(assessment.Reasons, "stable host classes differ") {
		t.Fatalf("stable-class mismatch passed: %#v", assessment)
	}
	if assessment.BeforeStableHostClassDigest == assessment.AfterStableHostClassDigest {
		t.Fatalf("stable-class mismatch was not visible in digests: %#v", assessment)
	}
	assertSortedReasons(t, assessment.Reasons)
}

func TestAssessBookendsReverifiesTamperedArtifacts(t *testing.T) {
	minimumCPUs := uint64(8)
	policy := Policy{Strict: true, MinLogicalCPUs: &minimumCPUs}
	before := qualifiedBookend(t, time.Unix(1, 0).UTC(), policy)
	after := qualifiedBookend(t, time.Unix(2, 0).UTC(), policy)
	after.Digest = "sha256:" + strings.Repeat("0", 64)

	assessment := AssessBookends(before, after)
	if assessment.Status != BookendStatusUnqualified || !hasReason(assessment.Reasons, "after artifact invalid: artifact digest mismatch") {
		t.Fatalf("tampered artifact passed assessment: %#v", assessment)
	}
	assertSortedReasons(t, assessment.Reasons)
}

func TestAssessBookendsRejectsUnqualifiedVerdictsAndReverseChronology(t *testing.T) {
	before, err := inspectWith(InspectOptions{RecordedAt: time.Unix(3, 0).UTC(), ClientPlacement: "separate-host"}, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	after, err := inspectWith(InspectOptions{RecordedAt: time.Unix(2, 0).UTC(), ClientPlacement: "separate-host"}, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}

	assessment := AssessBookends(before, after)
	for _, want := range []string{
		"before recorded verdict is \"unqualified\"",
		"after recorded verdict is \"unqualified\"",
		"before recorded_at is later than after recorded_at",
	} {
		if !hasReason(assessment.Reasons, want) {
			t.Fatalf("assessment reasons %q omit %q", assessment.Reasons, want)
		}
	}
	if assessment.Status != BookendStatusUnqualified {
		t.Fatalf("unqualified/reversed bookends passed: %#v", assessment)
	}
	assertSortedReasons(t, assessment.Reasons)
}

func qualifiedBookend(t *testing.T, recordedAt time.Time, policy Policy) Artifact {
	t.Helper()
	artifact, err := inspectWith(InspectOptions{
		RecordedAt:      recordedAt,
		StorageLabel:    "postgres-data",
		ClientPlacement: "separate-host",
		Policy:          policy,
	}, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Verdict != VerdictQualified {
		t.Fatalf("bookend fixture is not qualified: %#v", artifact)
	}
	if verification := Verify(artifact); !verification.Valid {
		t.Fatalf("bookend fixture does not verify: %#v", verification)
	}
	return artifact
}

func hasReason(reasons []string, substring string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, substring) {
			return true
		}
	}
	return false
}

func assertSortedReasons(t *testing.T, reasons []string) {
	t.Helper()
	want := append([]string(nil), reasons...)
	sort.Strings(want)
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("reasons are not sorted: %#v", reasons)
	}
}
