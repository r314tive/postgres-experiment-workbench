package benchmarkqualify

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestInspectLinuxStrictPolicyQualifiesDeterministically(t *testing.T) {
	minimumCPUs := uint64(8)
	minimumMemory := 40.0
	minimumStorage := 50.0
	maximumLoad := 0.5
	maximumTemperature := 60.0
	options := InspectOptions{
		RecordedAt:      time.Date(2026, 8, 12, 9, 10, 11, 123, time.UTC),
		StoragePath:     "/private/operator/path",
		StorageLabel:    "postgres-data",
		ClientPlacement: "separate-host",
		Policy: Policy{
			Strict:                  true,
			MinLogicalCPUs:          &minimumCPUs,
			MinMemoryAvailablePct:   &minimumMemory,
			MinStorageAvailablePct:  &minimumStorage,
			MaxLoad1PerCPU:          &maximumLoad,
			RequiredClocksource:     "tsc",
			RequiredGovernor:        "performance",
			MaxTemperatureCelsius:   &maximumTemperature,
			RequiredClientPlacement: "separate-host",
		},
	}

	first, err := inspectWith(options, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	second, err := inspectWith(options, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("fixed probes did not produce a deterministic artifact:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.Verdict != VerdictQualified || len(first.Checks) != 8 || len(first.Reasons) != 0 {
		t.Fatalf("unexpected strict verdict: %#v", first)
	}
	for _, check := range first.Checks {
		if check.Status != CheckPassed {
			t.Fatalf("gate did not pass: %#v", check)
		}
	}
	verification := Verify(first)
	if !verification.Valid || len(verification.Issues) != 0 {
		t.Fatalf("generated artifact did not verify: %#v", verification)
	}
	if first.Assurance.EvidenceOrigin != EvidenceOriginOperatorRecorded || first.Assurance.Signed || first.Assurance.DigestPurpose != DigestPurposeIntegrityOnly {
		t.Fatalf("assurance overclaims: %#v", first.Assurance)
	}

	var rendered bytes.Buffer
	if err := RenderJSON(&rendered, first); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{options.StoragePath, "operator", os.Getenv("USER")} {
		if forbidden != "operator" && forbidden != "" && strings.Contains(rendered.String(), forbidden) {
			t.Fatalf("artifact leaked ambient value %q: %s", forbidden, rendered.String())
		}
	}
	if strings.Contains(rendered.String(), `"signed": true`) {
		t.Fatal("artifact unexpectedly claimed a signature")
	}
}

func TestInspectDefaultsToUnqualifiedAndNeverInfersClientPlacement(t *testing.T) {
	artifact, err := inspectWith(InspectOptions{
		RecordedAt: time.Unix(1, 0).UTC(),
	}, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Verdict != VerdictUnqualified || artifact.Policy.Strict || len(artifact.Checks) != 0 {
		t.Fatalf("default inspection was not unqualified: %#v", artifact)
	}
	if artifact.Snapshot.Client.Placement.Availability != AvailabilityUnavailable || artifact.Snapshot.Client.Placement.Value != "" || artifact.Snapshot.Client.Placement.Source != "operator" {
		t.Fatalf("client placement was inferred: %#v", artifact.Snapshot.Client.Placement)
	}
	wantReasons := []string{"strict policy is not enabled", "no explicit qualification gates were configured"}
	if !reflect.DeepEqual(artifact.Reasons, wantReasons) {
		t.Fatalf("reasons = %#v, want %#v", artifact.Reasons, wantReasons)
	}
	if verification := Verify(artifact); !verification.Valid {
		t.Fatalf("valid unqualified artifact failed verification: %#v", verification)
	}
}

func TestUnavailableStrictObservationFailsClosed(t *testing.T) {
	probes := linuxFixtureProbes()
	probes.glob = func(pattern string) ([]string, error) {
		if strings.Contains(pattern, "cpufreq") {
			return nil, nil
		}
		return linuxFixtureGlob(pattern)
	}
	artifact, err := inspectWith(InspectOptions{
		RecordedAt: time.Unix(1, 0).UTC(),
		Policy: Policy{
			Strict:           true,
			RequiredGovernor: "performance",
		},
	}, probes)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Verdict != VerdictUnqualified || len(artifact.Checks) != 1 || artifact.Checks[0].Status != CheckFailed || artifact.Checks[0].Observation != "unavailable" {
		t.Fatalf("missing strict observation did not fail closed: %#v", artifact)
	}
}

func TestVerifierDetectsTamperingEvenWhenDigestsAreRecomputed(t *testing.T) {
	minimumCPUs := uint64(8)
	artifact, err := inspectWith(InspectOptions{
		RecordedAt: time.Unix(1, 0).UTC(),
		Policy:     Policy{Strict: true, MinLogicalCPUs: &minimumCPUs},
	}, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}

	tampered := artifact
	tampered.Checks = []Check{{Gate: "min_logical_cpus", Status: CheckPassed, Observation: "999", Requirement: ">= 8"}}
	resealArtifact(t, &tampered)
	verification := Verify(tampered)
	if verification.Valid || !hasIssue(verification.Issues, "checks do not match independently recomputed") {
		t.Fatalf("forged checks passed verification: %#v", verification)
	}

	tampered = artifact
	value := uint64(2)
	tampered.Snapshot.CPU.LogicalCPUs.Value = &value
	resealArtifact(t, &tampered)
	verification = Verify(tampered)
	if verification.Valid || !hasIssue(verification.Issues, "verdict = \"qualified\", want independently recomputed \"unqualified\"") {
		t.Fatalf("forged verdict passed verification: %#v", verification)
	}

	tampered = artifact
	tampered.Assurance.Signed = true
	resealArtifact(t, &tampered)
	verification = Verify(tampered)
	if verification.Valid || !hasIssue(verification.Issues, "assurance must declare operator-recorded unsigned") {
		t.Fatalf("false signature claim passed verification: %#v", verification)
	}
}

func TestParseIsStrictAndVerifierDoesNotPanicOnMissingValues(t *testing.T) {
	artifact, err := inspectWith(InspectOptions{RecordedAt: time.Unix(1, 0).UTC()}, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := RenderJSON(&buffer, artifact); err != nil {
		t.Fatal(err)
	}
	content := buffer.String()
	unknown := strings.Replace(content, `"schema_version":`, `"unknown": true, "schema_version":`, 1)
	if _, err := Parse([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field accepted: %v", err)
	}
	if _, err := Parse([]byte(content + `{}`)); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing JSON accepted: %v", err)
	}

	malformed := artifact
	malformed.Snapshot.Interference.Load1.Value = nil
	malformed.Snapshot.Interference.Load1.Availability = AvailabilityObserved
	verification := Verify(malformed)
	if verification.Valid || !hasIssue(verification.Issues, "snapshot.interference.load_1m observed value must be finite") {
		t.Fatalf("missing observed value passed verification: %#v", verification)
	}
}

func TestDarwinFixtureRecordsAvailableAndUnavailableObservations(t *testing.T) {
	artifact, err := inspectWith(InspectOptions{RecordedAt: time.Unix(1, 0).UTC()}, darwinFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Snapshot.Platform.OS.Value != "darwin" || artifact.Snapshot.Platform.Kernel.Value != "25.6.0" || artifact.Snapshot.CPU.Model.Value != "Apple M4" {
		t.Fatalf("unexpected Darwin platform snapshot: %#v", artifact.Snapshot)
	}
	if artifact.Snapshot.Memory.AvailablePct.Availability != AvailabilityObserved || artifact.Snapshot.Interference.Load1PerCPU.Availability != AvailabilityObserved {
		t.Fatalf("Darwin observable metrics were lost: %#v", artifact.Snapshot)
	}
	if artifact.Snapshot.Power.Governors.Availability != AvailabilityUnavailable || artifact.Snapshot.Thermal.MaxCelsius.Availability != AvailabilityUnavailable {
		t.Fatalf("Darwin unsupported metrics were claimed: %#v", artifact.Snapshot)
	}
	if verification := Verify(artifact); !verification.Valid {
		t.Fatalf("Darwin artifact did not verify: %#v", verification)
	}
}

func TestWriteFileDoesNotOverwriteAndVerifyFileRejectsSymlink(t *testing.T) {
	artifact, err := inspectWith(InspectOptions{RecordedAt: time.Unix(1, 0).UTC()}, linuxFixtureProbes())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "host.json")
	if err := WriteFile(path, artifact); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, artifact); err == nil {
		t.Fatal("existing artifact was overwritten")
	}
	verification, err := VerifyFile(path)
	if err != nil || !verification.Valid {
		t.Fatalf("written artifact did not verify: verification=%#v err=%v", verification, err)
	}
	symlink := filepath.Join(directory, "host-link.json")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(symlink); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink was accepted: %v", err)
	}
}

func TestQualificationSchemaTracksRootContractAndAssuranceBoundary(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "benchmark-host-qualification.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema               string                     `json:"$schema"`
		Description          string                     `json:"description"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("unexpected root schema contract: %#v", schema)
	}
	description := strings.ToLower(schema.Description)
	if !strings.Contains(description, "unsigned") || !strings.Contains(description, "operator-recorded") || !strings.Contains(description, "not attest") {
		t.Fatalf("schema omits assurance boundary: %q", schema.Description)
	}
	wantFields := jsonFieldNames(reflect.TypeOf(Artifact{}))
	sort.Strings(schema.Required)
	if !reflect.DeepEqual(schema.Required, wantFields) {
		t.Fatalf("schema required fields = %#v, want %#v", schema.Required, wantFields)
	}
	for _, field := range wantFields {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("schema property %q is missing", field)
		}
	}
}

func linuxFixtureProbes() collectorProbes {
	files := map[string]string{
		"/proc/sys/kernel/osrelease": "6.12.0-test\n",
		"/proc/cpuinfo":              "processor: 0\nmodel name: Test CPU 8-Core\n",
		"/proc/meminfo":              "MemTotal: 16777216 kB\nMemAvailable: 8388608 kB\n",
		"/proc/loadavg":              "2.00 1.00 0.50 2/200 42\n",
		"/sys/devices/system/clocksource/clocksource0/current_clocksource": "tsc\n",
		"/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor":            "performance\n",
		"/sys/devices/system/cpu/cpu1/cpufreq/scaling_governor":            "performance\n",
		"/sys/class/thermal/thermal_zone0/temp":                            "55000\n",
	}
	return collectorProbes{
		goos:   "linux",
		goarch: "amd64",
		numCPU: func() int { return 8 },
		readFile: func(path string) ([]byte, error) {
			value, ok := files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(value), nil
		},
		glob:    linuxFixtureGlob,
		command: func(string, ...string) ([]byte, error) { return nil, errors.New("unexpected command") },
		storage: func(string) (storageResult, error) {
			return storageResult{filesystem: "ext", totalBytes: 1000, availableBytes: 600}, nil
		},
	}
}

func linuxFixtureGlob(pattern string) ([]string, error) {
	switch pattern {
	case "/sys/devices/system/cpu/cpu*/cpufreq/scaling_governor":
		return []string{
			"/sys/devices/system/cpu/cpu1/cpufreq/scaling_governor",
			"/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor",
		}, nil
	case "/sys/class/thermal/thermal_zone*/temp":
		return []string{"/sys/class/thermal/thermal_zone0/temp"}, nil
	default:
		return nil, nil
	}
}

func darwinFixtureProbes() collectorProbes {
	commands := map[string]string{
		"sysctl -n kern.osrelease":            "25.6.0\n",
		"sysctl -n machdep.cpu.brand_string":  "Apple M4\n",
		"sysctl -n hw.memsize":                "17179869184\n",
		"sysctl -n vm.loadavg":                "{ 1.00 0.50 0.25 }\n",
		"sysctl -n kern.timecounter.hardware": "ARM64\n",
		"vm_stat": "Mach Virtual Memory Statistics: (page size of 16384 bytes)\n" +
			"Pages free: 262144.\nPages inactive: 262144.\nPages speculative: 0.\n",
	}
	return collectorProbes{
		goos:     "darwin",
		goarch:   "arm64",
		numCPU:   func() int { return 4 },
		readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		glob:     func(string) ([]string, error) { return nil, nil },
		command: func(name string, args ...string) ([]byte, error) {
			key := strings.TrimSpace(name + " " + strings.Join(args, " "))
			value, ok := commands[key]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(value), nil
		},
		storage: func(string) (storageResult, error) {
			return storageResult{filesystem: "apfs", totalBytes: 1000, availableBytes: 500}, nil
		},
	}
}

func resealArtifact(t *testing.T, artifact *Artifact) {
	t.Helper()
	digest, err := digestJSON(artifact.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	artifact.SnapshotDigest = digest
	digest, err = artifactDigest(*artifact)
	if err != nil {
		t.Fatal(err)
	}
	artifact.Digest = digest
}

func hasIssue(issues []string, substring string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, substring) {
			return true
		}
	}
	return false
}

func jsonFieldNames(value reflect.Type) []string {
	fields := make([]string, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		name := strings.Split(value.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}
