package scenariopack

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	EngineCompatibleRelease     = "compatible-release"
	EngineCompatiblePrerelease  = "compatible-prerelease"
	EngineUnverifiedDevelopment = "unverified-development"
	EngineIncompatible          = "incompatible"
)

type EngineCompatibility struct {
	EngineVersion           string `json:"engine_version"`
	Constraint              string `json:"constraint"`
	Status                  string `json:"status"`
	ReleaseEvidenceEligible bool   `json:"release_evidence_eligible"`
	Diagnostic              string `json:"diagnostic"`
}

type EngineCompatibilityError struct {
	PackID        string
	EngineVersion string
	Constraint    string
	Diagnostic    string
}

func (err *EngineCompatibilityError) Error() string {
	return err.Diagnostic
}

func CheckEngineCompatibility(manifest Manifest, engineVersion string) (EngineCompatibility, error) {
	constraint, err := parseConstraint(manifest.EngineConstraint)
	if err != nil {
		return EngineCompatibility{}, fmt.Errorf("invalid engine_constraint %q: %w", manifest.EngineConstraint, err)
	}

	engineVersion = strings.TrimSpace(engineVersion)
	if engineVersion == "dev" {
		return developmentCompatibility(engineVersion, constraint.raw), nil
	}
	engine, err := parseSemVersion(engineVersion)
	if err != nil {
		return EngineCompatibility{}, fmt.Errorf(
			"engine version %q is not strict SemVer: %w; release builds must inject a version such as 0.2.0, while source builds must use dev",
			engineVersion,
			err,
		)
	}
	if engine.isDevelopmentPrerelease() {
		return developmentCompatibility(engineVersion, constraint.raw), nil
	}

	status := EngineCompatibleRelease
	releaseEligible := true
	if engine.prerelease != "" {
		status = EngineCompatiblePrerelease
		releaseEligible = false
	}
	if constraint.matches(engine) {
		diagnostic := fmt.Sprintf("engine %s satisfies %s", engineVersion, constraint.raw)
		if !releaseEligible {
			diagnostic += "; pre-release compatibility is not release evidence"
		}
		return EngineCompatibility{
			EngineVersion:           engineVersion,
			Constraint:              constraint.raw,
			Status:                  status,
			ReleaseEvidenceEligible: releaseEligible,
			Diagnostic:              diagnostic,
		}, nil
	}

	diagnostic := fmt.Sprintf(
		"scenario pack %s requires pgworkbench %s, but engine %s is incompatible; use an engine satisfying %s, or migrate and retest the pack before updating engine_constraint",
		manifest.ID,
		constraint.raw,
		engineVersion,
		constraint.raw,
	)
	compatibility := EngineCompatibility{
		EngineVersion:           engineVersion,
		Constraint:              constraint.raw,
		Status:                  EngineIncompatible,
		ReleaseEvidenceEligible: false,
		Diagnostic:              diagnostic,
	}
	return compatibility, &EngineCompatibilityError{
		PackID:        manifest.ID,
		EngineVersion: engineVersion,
		Constraint:    constraint.raw,
		Diagnostic:    diagnostic,
	}
}

func developmentCompatibility(engineVersion string, constraint string) EngineCompatibility {
	return EngineCompatibility{
		EngineVersion:           engineVersion,
		Constraint:              constraint,
		Status:                  EngineUnverifiedDevelopment,
		ReleaseEvidenceEligible: false,
		Diagnostic: fmt.Sprintf(
			"development engine %s cannot prove release compatibility with %s; rebuild with a strict SemVer release version or pass --engine-version for a candidate check",
			engineVersion,
			constraint,
		),
	}
}

type semVersion struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease string
}

func parseSemVersion(value string) (semVersion, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return semVersion{}, fmt.Errorf("version is empty or contains surrounding whitespace")
	}
	withoutBuild, build, hasBuild := strings.Cut(value, "+")
	if hasBuild {
		if strings.Contains(build, "+") || !validIdentifiers(build, false) {
			return semVersion{}, fmt.Errorf("invalid build metadata")
		}
	}
	core, prerelease, hasPrerelease := strings.Cut(withoutBuild, "-")
	if hasPrerelease {
		if !validIdentifiers(prerelease, true) {
			return semVersion{}, fmt.Errorf("invalid prerelease")
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semVersion{}, fmt.Errorf("version must contain major.minor.patch")
	}
	major, err := parseCoreNumber(parts[0])
	if err != nil {
		return semVersion{}, fmt.Errorf("invalid major: %w", err)
	}
	minor, err := parseCoreNumber(parts[1])
	if err != nil {
		return semVersion{}, fmt.Errorf("invalid minor: %w", err)
	}
	patch, err := parseCoreNumber(parts[2])
	if err != nil {
		return semVersion{}, fmt.Errorf("invalid patch: %w", err)
	}
	return semVersion{major: major, minor: minor, patch: patch, prerelease: prerelease}, nil
}

func parseCoreNumber(value string) (uint64, error) {
	if value == "" {
		return 0, fmt.Errorf("number is empty")
	}
	if len(value) > 1 && value[0] == '0' {
		return 0, fmt.Errorf("leading zero")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("non-numeric character")
		}
	}
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("number out of range")
	}
	return number, nil
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, char := range identifier {
			if !((char >= '0' && char <= '9') || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || char == '-') {
				return false
			}
			if char < '0' || char > '9' {
				numeric = false
			}
		}
		if rejectNumericLeadingZero && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func (version semVersion) compare(other semVersion) int {
	if result := compareUint(version.major, other.major); result != 0 {
		return result
	}
	if result := compareUint(version.minor, other.minor); result != 0 {
		return result
	}
	if result := compareUint(version.patch, other.patch); result != 0 {
		return result
	}
	return comparePrerelease(version.prerelease, other.prerelease)
}

func compareUint(left uint64, right uint64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func comparePrerelease(left string, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		result := comparePrereleaseIdentifier(leftParts[index], rightParts[index])
		if result != 0 {
			return result
		}
	}
	return compareUint(uint64(len(leftParts)), uint64(len(rightParts)))
}

func comparePrereleaseIdentifier(left string, right string) int {
	leftNumeric := numericIdentifier(left)
	rightNumeric := numericIdentifier(right)
	if leftNumeric && !rightNumeric {
		return -1
	}
	if !leftNumeric && rightNumeric {
		return 1
	}
	if leftNumeric {
		if len(left) < len(right) {
			return -1
		}
		if len(left) > len(right) {
			return 1
		}
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func numericIdentifier(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func (version semVersion) isDevelopmentPrerelease() bool {
	for _, identifier := range strings.Split(version.prerelease, ".") {
		switch strings.ToLower(identifier) {
		case "dev", "development", "dirty", "snapshot":
			return true
		}
	}
	return false
}

type engineConstraint struct {
	raw      string
	operator string
	base     semVersion
}

func parseConstraint(value string) (engineConstraint, error) {
	if strings.TrimSpace(value) != value {
		return engineConstraint{}, fmt.Errorf("constraint contains surrounding whitespace")
	}
	operator := ""
	for _, candidate := range []string{">=", "=", "^", "~"} {
		if strings.HasPrefix(value, candidate) {
			operator = candidate
			break
		}
	}
	if operator == "" {
		return engineConstraint{}, fmt.Errorf("constraint must start with >=, =, ^, or ~")
	}
	base, err := parseSemVersion(strings.TrimPrefix(value, operator))
	if err != nil {
		return engineConstraint{}, err
	}
	max := ^uint64(0)
	if operator == "^" && (base.major == max || base.major == 0 && base.minor == max || base.major == 0 && base.minor == 0 && base.patch == max) {
		return engineConstraint{}, fmt.Errorf("caret upper bound overflows SemVer range")
	}
	if operator == "~" && base.minor == max {
		return engineConstraint{}, fmt.Errorf("tilde upper bound overflows SemVer range")
	}
	return engineConstraint{raw: value, operator: operator, base: base}, nil
}

func (constraint engineConstraint) matches(version semVersion) bool {
	comparison := version.compare(constraint.base)
	switch constraint.operator {
	case "=":
		return comparison == 0
	case ">=":
		return comparison >= 0
	case "^":
		return comparison >= 0 && version.compare(constraint.caretUpperBound()) < 0
	case "~":
		return comparison >= 0 && version.compare(constraint.tildeUpperBound()) < 0
	default:
		return false
	}
}

func (constraint engineConstraint) caretUpperBound() semVersion {
	base := constraint.base
	if base.major != 0 {
		return semVersion{major: base.major + 1}
	}
	if base.minor != 0 {
		return semVersion{minor: base.minor + 1}
	}
	return semVersion{patch: base.patch + 1}
}

func (constraint engineConstraint) tildeUpperBound() semVersion {
	return semVersion{major: constraint.base.major, minor: constraint.base.minor + 1}
}
