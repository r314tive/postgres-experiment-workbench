package scenariopack

import (
	"errors"
	"strings"
	"testing"
)

func TestStrictSemVersionParsing(t *testing.T) {
	valid := []string{
		"0.2.0",
		"1.2.3-rc.1",
		"1.2.3-alpha-beta+build.42",
	}
	for _, value := range valid {
		if _, err := parseSemVersion(value); err != nil {
			t.Errorf("parseSemVersion(%q): %v", value, err)
		}
	}

	invalid := []string{
		"dev",
		"v1.2.3",
		"1.2",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"1.2.3-01",
		"1.2.3-",
		"1.2.3+",
		"1.2.3+bad_value",
		" 1.2.3",
	}
	for _, value := range invalid {
		if _, err := parseSemVersion(value); err == nil {
			t.Errorf("parseSemVersion(%q) unexpectedly succeeded", value)
		}
	}
}

func TestConstraintSemantics(t *testing.T) {
	tests := []struct {
		constraint string
		version    string
		matches    bool
	}{
		{constraint: ">=0.2.0", version: "0.2.0", matches: true},
		{constraint: ">=0.2.0", version: "0.1.99", matches: false},
		{constraint: "=1.2.3", version: "1.2.3+build.7", matches: true},
		{constraint: "^1.2.3", version: "1.9.0", matches: true},
		{constraint: "^1.2.3", version: "2.0.0", matches: false},
		{constraint: "^0.2.3", version: "0.2.99", matches: true},
		{constraint: "^0.2.3", version: "0.3.0", matches: false},
		{constraint: "^0.0.3", version: "0.0.4", matches: false},
		{constraint: "~1.2.3", version: "1.2.99", matches: true},
		{constraint: "~1.2.3", version: "1.3.0", matches: false},
		{constraint: ">=1.2.3-rc.1", version: "1.2.3-rc.2", matches: true},
	}
	for _, test := range tests {
		constraint, err := parseConstraint(test.constraint)
		if err != nil {
			t.Fatal(err)
		}
		version, err := parseSemVersion(test.version)
		if err != nil {
			t.Fatal(err)
		}
		if got := constraint.matches(version); got != test.matches {
			t.Errorf("%s matches %s = %t, want %t", test.version, test.constraint, got, test.matches)
		}
	}
}

func TestConstraintRejectsNonStrictAndOverflowingRanges(t *testing.T) {
	invalid := []string{
		">=0.2",
		">=00.2.0",
		">= 0.2.0",
		"^18446744073709551615.0.0",
		"~1.18446744073709551615.0",
	}
	for _, value := range invalid {
		if _, err := parseConstraint(value); err == nil {
			t.Errorf("parseConstraint(%q) unexpectedly succeeded", value)
		}
	}
}

func TestEngineCompatibilityStatuses(t *testing.T) {
	manifest := Manifest{ID: "demo-pack", EngineConstraint: ">=0.2.0"}

	compatible, err := CheckEngineCompatibility(manifest, "0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if compatible.Status != EngineCompatibleRelease || !compatible.ReleaseEvidenceEligible {
		t.Fatalf("unexpected release compatibility: %#v", compatible)
	}

	prereleaseManifest := Manifest{ID: "demo-pack", EngineConstraint: ">=0.2.0-rc.1"}
	prerelease, err := CheckEngineCompatibility(prereleaseManifest, "0.2.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if prerelease.Status != EngineCompatiblePrerelease || prerelease.ReleaseEvidenceEligible {
		t.Fatalf("unexpected prerelease compatibility: %#v", prerelease)
	}

	for _, developmentVersion := range []string{"dev", "0.2.0-dev", "0.2.0-dirty.1"} {
		development, err := CheckEngineCompatibility(manifest, developmentVersion)
		if err != nil {
			t.Fatal(err)
		}
		if development.Status != EngineUnverifiedDevelopment || development.ReleaseEvidenceEligible {
			t.Fatalf("development engine falsely treated as release-compatible: %#v", development)
		}
	}
}

func TestIncompatibleEngineReturnsMigrationDiagnostic(t *testing.T) {
	manifest := Manifest{ID: "future-pack", EngineConstraint: "^0.3.0"}
	compatibility, err := CheckEngineCompatibility(manifest, "0.2.4")
	if err == nil {
		t.Fatal("expected incompatibility")
	}
	var compatibilityError *EngineCompatibilityError
	if !errors.As(err, &compatibilityError) {
		t.Fatalf("expected EngineCompatibilityError, got %T: %v", err, err)
	}
	if compatibility.Status != EngineIncompatible || compatibility.ReleaseEvidenceEligible {
		t.Fatalf("unexpected incompatibility result: %#v", compatibility)
	}
	for _, fragment := range []string{"future-pack", "^0.3.0", "0.2.4", "migrate and retest", "updating engine_constraint"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("diagnostic %q does not contain %q", err, fragment)
		}
	}
}

func TestInvalidEngineVersionIsNotSilentlyDevelopment(t *testing.T) {
	manifest := Manifest{ID: "demo-pack", EngineConstraint: ">=0.2.0"}
	if _, err := CheckEngineCompatibility(manifest, "not-a-version"); err == nil || !strings.Contains(err.Error(), "not strict SemVer") {
		t.Fatalf("expected strict version diagnostic, got %v", err)
	}
}
