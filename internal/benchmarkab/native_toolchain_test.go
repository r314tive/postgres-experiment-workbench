package benchmarkab

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestRunNativeToolchainABRetainsVerifiableSeriesLocalSnapshots(t *testing.T) {
	root := t.TempDir()
	writeABCatalog(t, root)
	benchmarkPath := filepath.Join(root, "benchmarks", "ab", "baseline.env")
	benchmarkSpec := string(readABFile(t, benchmarkPath))
	benchmarkSpec = strings.Replace(benchmarkSpec,
		"BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES=pg_config",
		"BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES=native_toolchain", 1)
	writeABFile(t, benchmarkPath, benchmarkSpec)

	baselineBindir := fakeNativeToolchain(t, "run-baseline", "19devel")
	candidateBindir := fakeNativeToolchain(t, "run-candidate", "19devel")
	baselineInstallation, err := nativetoolchain.Inspect(baselineBindir)
	if err != nil {
		t.Fatal(err)
	}
	candidateInstallation, err := nativetoolchain.Inspect(candidateBindir)
	if err != nil {
		t.Fatal(err)
	}
	options := testOptions()
	options.Runtime = "native"
	options.SubjectDimension = SubjectNativeToolchain
	options.RunID = "native-toolchain-series-closure"
	options.BaselineSubject = "baseline-build"
	options.CandidateSubject = "candidate-build"
	options.BaselineNativeBindir = baselineBindir
	options.CandidateNativeBindir = candidateBindir
	options.InspectHost = syntheticABQualification
	options.Now = newABFixtureClock(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC))
	options.Stdout = io.Discard
	options.Stderr = io.Discard
	options.StopNativeRuntime = func(string, string, func(string) string, io.Writer, io.Writer) error { return nil }
	options.SeriesOptions = benchmarkrun.Options{
		EngineVersion: "0.3.0",
		EngineCommit:  strings.Repeat("a", 40),
		Getenv:        func(string) string { return "" },
		RunExperiment: syntheticABExperiment,
		VerifyRun:     runverify.Verify,
	}

	result, err := Run(root, speccatalog.New(root), "ab/baseline", "ab/baseline", options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" {
		t.Fatalf("native_toolchain Run did not close successfully: %#v", result)
	}
	if verification, err := Verify(root, result.ArtifactDir); err != nil || !verification.IsValid() {
		t.Fatalf("native_toolchain A/B artifact did not verify: result=%#v err=%v", verification, err)
	}

	arms := []struct {
		role     string
		ref      SeriesRef
		digest   string
		protocol string
	}{
		{"baseline", result.Baseline, baselineInstallation.Manifest.Digest, "baseline"},
		{"candidate", result.Candidate, candidateInstallation.Manifest.Digest, "candidate"},
	}
	for _, arm := range arms {
		seriesDir := filepath.Join(root, filepath.FromSlash(arm.ref.Ref))
		manifestDir := filepath.Join(seriesDir, filepath.Dir(filepath.FromSlash(benchmarkrun.NativeToolchainSeriesRef)))
		if _, err := nativetoolchain.VerifySnapshot(manifestDir, arm.digest); err != nil {
			t.Fatalf("%s series-local native toolchain snapshot did not verify: %v", arm.role, err)
		}
		seriesVerification, err := benchmarkartifact.Verify(root, seriesDir)
		if err != nil || !seriesVerification.IsValid() {
			t.Fatalf("%s generic series artifact did not verify: result=%#v err=%v", arm.role, seriesVerification, err)
		}
		protocolSnapshot := filepath.Join(result.ArtifactDir, "toolchains", arm.protocol)
		if _, err := nativetoolchain.VerifySnapshot(protocolSnapshot, arm.digest); err != nil {
			t.Fatalf("%s A/B protocol toolchain snapshot did not verify: %v", arm.role, err)
		}
	}

	baselineSeriesDir := filepath.Join(root, filepath.FromSlash(result.Baseline.Ref))
	tampered := filepath.Join(baselineSeriesDir, "protocol", "native-toolchain", "bin", "pgbench")
	if err := os.WriteFile(tampered, []byte("tampered native toolchain bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seriesVerification, err := benchmarkartifact.Verify(root, baselineSeriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if seriesVerification.IsValid() || !reasonContains(seriesVerification.Issues, "native benchmark protocol toolchain snapshot verification failed") {
		t.Fatalf("tampered series-local snapshot passed generic verification: %v", seriesVerification.Issues)
	}
	abVerification, err := Verify(root, result.ArtifactDir)
	if err != nil {
		t.Fatal(err)
	}
	if abVerification.IsValid() || !reasonContains(abVerification.Issues, "baseline series invalid: native benchmark protocol toolchain snapshot verification failed") {
		t.Fatalf("tampered series-local snapshot passed A/B verification: %v", abVerification.Issues)
	}
}

func TestBuildNativeToolchainProtocolBindsDistinctBytesWithoutAttestation(t *testing.T) {
	plan, err := benchmarkplan.Build(speccatalog.New(filepath.Join("..", "..")), "pgbench/source-patch")
	if err != nil {
		t.Fatal(err)
	}
	options := testOptions()
	options.SubjectDimension = SubjectNativeToolchain
	options.BaselineNativeBindir = fakeNativeToolchain(t, "baseline", "19devel")
	options.CandidateNativeBindir = fakeNativeToolchain(t, "candidate", "19devel")
	protocol, err := BuildProtocol("native-toolchain-ab", "native", "baseline-build", "candidate-build", plan, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.SubjectDimension != SubjectNativeToolchain || protocol.EffectiveSettings.RequireCrossArmDifference || protocol.Baseline.PGConfig != protocol.Candidate.PGConfig || protocol.Baseline.ProtocolDigest != protocol.Candidate.ProtocolDigest {
		t.Fatalf("native toolchain protocol changed the benchmark/GUC subject: %#v", protocol)
	}
	if protocol.Baseline.NativeToolchain == nil || protocol.Candidate.NativeToolchain == nil || protocol.Baseline.NativeToolchain.Digest == protocol.Candidate.NativeToolchain.Digest {
		t.Fatalf("native toolchain byte identities are missing or equal: %#v", protocol)
	}
	for _, identity := range []*NativeToolchainIdentity{protocol.Baseline.NativeToolchain, protocol.Candidate.NativeToolchain} {
		if identity.SourceCommit != nativetoolchain.Unattested || identity.BuildProvenance != nativetoolchain.Unattested {
			t.Fatalf("protocol invented native build provenance: %#v", identity)
		}
	}
	if err := VerifyProtocol(protocol); err != nil {
		t.Fatalf("native toolchain protocol did not verify: %v", err)
	}
	invalidVersion := protocol
	invalidBaseline := *protocol.Baseline.NativeToolchain
	invalidBaseline.PostgresVersion += "\nforged"
	invalidVersion.Baseline.NativeToolchain = &invalidBaseline
	if err := VerifyProtocol(invalidVersion); err == nil || !strings.Contains(err.Error(), "invalid baseline native toolchain identity") {
		t.Fatalf("invalid observed version identity passed: %v", err)
	}

	baseline := testSeries("native-toolchain-ab-a", 100, 10)
	candidate := testSeries("native-toolchain-ab-b", 101, 10)
	for index := range baseline.Trials {
		baseline.Trials[index].EffectiveSettings = syntheticEffectiveSettings(t, protocol, baseline.Trials[index], "16384", "8kB", "configuration file", "170009", false)
		candidate.Trials[index].EffectiveSettings = syntheticEffectiveSettings(t, protocol, candidate.Trials[index], "16384", "8kB", "configuration file", "170009", false)
	}
	assessment := assessEffectiveSettings(protocol, baseline, candidate)
	if assessment.Status != "verified-stable" || assessment.BaselineServerVersionNum != "170009" || assessment.CandidateServerVersionNum != "170009" || len(assessment.EffectiveDifferences) != 0 {
		t.Fatalf("stable native toolchains required an inapplicable cross-arm GUC difference: %#v", assessment)
	}
	for index := range candidate.Trials {
		candidate.Trials[index].EffectiveSettings = syntheticEffectiveSettings(t, protocol, candidate.Trials[index], "16384", "8kB", "configuration file", "180001", false)
	}
	if drift := assessEffectiveSettings(protocol, baseline, candidate); drift.Status != "unstable" || !reasonContains(drift.Reasons, "do not share one PostgreSQL server version") {
		t.Fatalf("cross-arm runtime server version passed native toolchain gate: %#v", drift)
	}
}

func TestNativeToolchainProtocolRejectsSameBytesAndCrossArmSnapshotSwap(t *testing.T) {
	plan, err := benchmarkplan.Build(speccatalog.New(filepath.Join("..", "..")), "pgbench/source-patch")
	if err != nil {
		t.Fatal(err)
	}
	options := testOptions()
	options.SubjectDimension = SubjectNativeToolchain
	options.BaselineNativeBindir = fakeNativeToolchain(t, "same", "19devel")
	options.CandidateNativeBindir = fakeNativeToolchain(t, "same", "19devel")
	if _, err := BuildProtocol("same-native", "native", "a", "b", plan, plan, options); err == nil || !strings.Contains(err.Error(), "distinct toolchain byte identities") {
		t.Fatalf("same-byte native toolchains passed: %v", err)
	}

	options.CandidateNativeBindir = fakeNativeToolchain(t, "different", "19devel")
	protocol, err := BuildProtocol("swapped-native", "native", "a", "b", plan, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	left, right, err := inspectNativeToolchains(options)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := nativetoolchain.Snapshot(right, filepath.Join(dir, "toolchains", "baseline")); err != nil {
		t.Fatal(err)
	}
	if err := nativetoolchain.Snapshot(left, filepath.Join(dir, "toolchains", "candidate")); err != nil {
		t.Fatal(err)
	}
	verification := VerifyResult{Issues: []string{}}
	checkNativeToolchainSnapshots(&verification, dir, protocol)
	if len(verification.Issues) != 2 || !reasonContains(verification.Issues, "identity mismatch") {
		t.Fatalf("cross-arm toolchain snapshot swap passed: %v", verification.Issues)
	}
	tamperedProtocol := protocol
	tamperedBaseline := *protocol.Baseline.NativeToolchain
	tamperedBaseline.ManifestRef = "../../outside/manifest.json"
	tamperedProtocol.Baseline.NativeToolchain = &tamperedBaseline
	verification = VerifyResult{Issues: []string{}}
	checkNativeToolchainSnapshots(&verification, dir, tamperedProtocol)
	if !reasonContains(verification.Issues, "baseline native toolchain manifest reference is invalid") {
		t.Fatalf("escaping native toolchain reference was followed: %v", verification.Issues)
	}
}

func TestNativeToolchainProtocolRejectsDifferentObservedVersions(t *testing.T) {
	plan, err := benchmarkplan.Build(speccatalog.New(filepath.Join("..", "..")), "pgbench/source-patch")
	if err != nil {
		t.Fatal(err)
	}
	options := testOptions()
	options.SubjectDimension = SubjectNativeToolchain
	options.BaselineNativeBindir = fakeNativeToolchain(t, "left", "18.4")
	options.CandidateNativeBindir = fakeNativeToolchain(t, "right", "19devel")
	if _, err := BuildProtocol("cross-version-native", "native", "a", "b", plan, plan, options); err == nil || !strings.Contains(err.Error(), "version identity differs") {
		t.Fatalf("cross-version native toolchains passed: %v", err)
	}
}

func TestNativeToolchainProtocolDoesNotWeakenPGConfigPath(t *testing.T) {
	baseline, candidate := testPlanPairWithConfigs(t)
	protocol, err := BuildProtocol("pg-config-still-strict", "native", "default", "tuned", baseline, candidate, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if protocol.SubjectDimension != SubjectPGConfig || !protocol.EffectiveSettings.RequireCrossArmDifference || protocol.Baseline.NativeToolchain != nil || protocol.Candidate.NativeToolchain != nil || !reflect.DeepEqual(baseline.AllowedSubjectDifferences, []string{SubjectPGConfig}) {
		t.Fatalf("pg_config path was weakened by native toolchain support: %#v", protocol)
	}
}

func fakeNativeToolchain(t *testing.T, identity, version string) string {
	t.Helper()
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range nativetoolchain.RequiredExecutableNames() {
		content := "#!/bin/sh\n# byte identity: " + identity + "\necho '" + name + " (PostgreSQL) " + version + "'\n"
		if err := os.WriteFile(filepath.Join(bindir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bindir
}
