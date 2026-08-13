package benchmarkartifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics"
	metricstest "github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics/testfixture"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchlog"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
)

func TestSeriesEnvironmentRejectsTamperedOrdinaryNativeToolchainSnapshot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "runs", "benchmarks", "native-pg-config")
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range nativetoolchain.RequiredExecutableNames() {
		if err := os.WriteFile(filepath.Join(bindir, name), []byte("#!/bin/sh\necho '"+name+" (PostgreSQL) artifact-test'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installation, err := nativetoolchain.Inspect(bindir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "protocol", "native-toolchain")
	if err := nativetoolchain.Snapshot(installation, snapshot); err != nil {
		t.Fatal(err)
	}
	environment := benchmarkrun.Environment{
		SchemaVersion: benchmarkrun.EnvironmentSchemaVersion, ArtifactType: benchmarkrun.EnvironmentArtifactType,
		Runtime: "native", RuntimeOS: "darwin", RuntimeArch: "arm64", Driver: "pgbench",
		Target: benchmarkplan.TargetDirectPostgres, TargetEndpointContract: benchmarkplan.EndpointDirectV1,
		TargetEndpointHost: "127.0.0.1", TargetEndpointPort: 59433, TargetTopology: "single",
		DockerDriverImageID: "not-applicable", DockerTargetImageID: "not-applicable",
		DriverVersion: "19devel", ParserVersion: pgbenchresult.ParserVersion,
		PostgresServerVersionNum: "190000", PostgresServerMajor: "19", PGConfig: "default", PGConfigDigest: "sha256:" + strings.Repeat("1", 64),
		SubjectDimension: "pg_config", NativeToolchainDigest: installation.Manifest.Digest,
		NativeToolchainManifestRef: filepath.ToSlash(filepath.Join("runs", "benchmarks", "native-pg-config", benchmarkrun.NativeToolchainSeriesRef)),
		NativeToolchainProvenance:  nativetoolchain.Unattested,
		EngineVersion:              "0.3.0", EngineCommit: strings.Repeat("a", 40), EngineBinaryDigest: "sha256:" + strings.Repeat("e", 64), Qualification: "unqualified-local",
	}
	environment.Digest = benchmarkEnvironmentDigest(t, environment)
	writeArtifactJSON(t, filepath.Join(dir, "environment.json"), environment)
	series := benchmarkrun.Series{
		Runtime: "native", Driver: "pgbench", Target: benchmarkplan.TargetDirectPostgres,
		TargetEndpointContract: benchmarkplan.EndpointDirectV1, TargetTopology: "single",
		EngineBinaryDigest: environment.EngineBinaryDigest,
		AllowedDifferences: []string{"pg_config"}, Environment: &environment,
	}
	before := VerifyResult{Issues: []string{}}
	checkSeriesEnvironment(&before, dir, series)
	if len(before.Issues) != 0 {
		t.Fatalf("untampered ordinary native toolchain was rejected: %v", before.Issues)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "bin", "pgbench"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := VerifyResult{Issues: []string{}}
	checkSeriesEnvironment(&after, dir, series)
	if !containsArtifactIssue(after.Issues, "native toolchain snapshot verification failed") {
		t.Fatalf("tampered ordinary native toolchain snapshot passed: %v", after.Issues)
	}
}

func TestProtocolNativeToolchainBindsFailedSeriesWithoutEnvironment(t *testing.T) {
	seriesDir := t.TempDir()
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range nativetoolchain.RequiredExecutableNames() {
		if err := os.WriteFile(filepath.Join(bindir, name), []byte("#!/bin/sh\necho '"+name+" (PostgreSQL) failed-series-test'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installation, err := nativetoolchain.Inspect(bindir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(seriesDir, filepath.Dir(filepath.FromSlash(benchmarkrun.NativeToolchainSeriesRef)))
	if err := nativetoolchain.Snapshot(installation, snapshot); err != nil {
		t.Fatal(err)
	}
	verification := VerifyResult{Issues: []string{}}
	digest := checkProtocolNativeToolchain(&verification, seriesDir, benchmarkrun.Series{Runtime: "native"})
	if digest != installation.Manifest.Digest || !verification.IsValid() {
		t.Fatalf("failed native series lost its protocol toolchain binding: digest=%q issues=%v", digest, verification.Issues)
	}

	dockerVerification := VerifyResult{Issues: []string{}}
	if got := checkProtocolNativeToolchain(&dockerVerification, seriesDir, benchmarkrun.Series{Runtime: "docker"}); got != "" || !containsArtifactIssue(dockerVerification.Issues, "Docker benchmark protocol contains native toolchain snapshot") {
		t.Fatalf("Docker series accepted a native protocol snapshot: digest=%q issues=%v", got, dockerVerification.Issues)
	}
}

func TestVerifyAcceptsSyntheticLinkedRunFixture(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	result, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsValid() {
		t.Fatalf("valid synthetic artifact was rejected: %s", strings.Join(result.Issues, "; "))
	}
}

func TestVerifyAcceptsProtocolV2Artifact(t *testing.T) {
	root, seriesDir, _, _, linkedRunDir := writeArtifactFixture(t)
	upgradeArtifactFixtureToProtocolV2(t, seriesDir, linkedRunDir)

	result, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsValid() {
		t.Fatalf("valid protocol-v2 artifact was rejected: %s", strings.Join(result.Issues, "; "))
	}
}

func TestVerifyRejectsTamperedBenchmarkEngineSnapshot(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	path := filepath.Join(seriesDir, filepath.FromSlash(benchmarkrun.EngineBinarySeriesRef))
	if err := os.WriteFile(path, []byte("tampered benchmark engine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsValid() || !containsArtifactIssue(result.Issues, "engine binary snapshot") {
		t.Fatalf("tampered benchmark engine snapshot was accepted: %v", result.Issues)
	}
}

func TestScenarioPackInventoryVerificationIsIndependentAndMandatory(t *testing.T) {
	packRoot := t.TempDir()
	writeArtifactFile(t, filepath.Join(packRoot, scenariopack.ManifestName), `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "fixture-pack",
  "version": "1.0.0",
  "engine_constraint": ">=0.2.0",
  "assets": ["scripts"]
}
`)
	writeArtifactFile(t, filepath.Join(packRoot, "scripts", "run.sh"), "#!/bin/sh\n")
	inspection, err := scenariopack.Validate(packRoot)
	if err != nil {
		t.Fatal(err)
	}
	inventory := benchmarkrun.ScenarioPackInventory{
		SchemaVersion: benchmarkrun.ScenarioPackSchemaVersion,
		ArtifactType:  benchmarkrun.ScenarioPackArtifactType,
		ID:            inspection.ID,
		Version:       inspection.Version,
		Digest:        inspection.Digest,
		Manifest:      inspection.Manifest,
		Files:         inspection.Files,
	}
	seriesDir := t.TempDir()
	inventoryPath := filepath.Join(seriesDir, filepath.FromSlash(benchmarkrun.ScenarioPackInventoryRef))
	writeArtifactJSON(t, inventoryPath, inventory)
	series := benchmarkrun.Series{ScenarioPack: &benchmarkrun.ScenarioPackIdentity{
		ID: inspection.ID, Version: inspection.Version, Digest: inspection.Digest,
		InventoryRef:    benchmarkrun.ScenarioPackInventoryRef,
		InventoryDigest: artifactFixtureDigest(t, inventoryPath),
	}}
	verification := VerifyResult{Issues: []string{}}
	checkScenarioPackInventory(&verification, seriesDir, series)
	if !verification.IsValid() {
		t.Fatalf("valid retained scenario-pack inventory was rejected: %v", verification.Issues)
	}

	tampered := inventory
	tampered.Files = append([]scenariopack.File(nil), inventory.Files...)
	tampered.Files[0].SHA256 = strings.Repeat("0", 64)
	writeArtifactJSON(t, inventoryPath, tampered)
	series.ScenarioPack.InventoryDigest = artifactFixtureDigest(t, inventoryPath)
	verification = VerifyResult{Issues: []string{}}
	checkScenarioPackInventory(&verification, seriesDir, series)
	if verification.IsValid() || !containsArtifactIssue(verification.Issues, "scenario-pack inventory verification failed") {
		t.Fatalf("self-consistent inventory-file rewrite bypassed pack-digest verification: %v", verification.Issues)
	}

	if err := os.Remove(inventoryPath); err != nil {
		t.Fatal(err)
	}
	verification = VerifyResult{Issues: []string{}}
	checkScenarioPackInventory(&verification, seriesDir, series)
	if verification.IsValid() || !containsArtifactIssue(verification.Issues, "configured scenario-pack inventory is missing") {
		t.Fatalf("configured series passed without its mandatory inventory: %v", verification.Issues)
	}
}

func TestVerifyRejectsSeriesChronologySummaryAndTrialInventoryTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(t *testing.T, seriesDir string, series *benchmarkrun.Series)
		want string
	}{
		{
			name: "non-canonical series timestamp",
			edit: func(_ *testing.T, _ string, series *benchmarkrun.Series) {
				series.StartedAt = "2026-08-12T00:00:00+00:00"
			},
			want: "series timestamps must be canonical UTC RFC3339Nano",
		},
		{
			name: "trial outside series interval",
			edit: func(_ *testing.T, _ string, series *benchmarkrun.Series) {
				series.StartedAt = "2026-08-12T00:00:01Z"
			},
			want: "trial 1 interval is outside the series interval",
		},
		{
			name: "tampered summary",
			edit: func(t *testing.T, seriesDir string, _ *benchmarkrun.Series) {
				writeArtifactFile(t, filepath.Join(seriesDir, "summary.md"), "PASS: invented result\n")
			},
			want: "summary.md does not match independently rendered result.json",
		},
		{
			name: "unexpected trial receipt",
			edit: func(t *testing.T, seriesDir string, _ *benchmarkrun.Series) {
				writeArtifactFile(t, filepath.Join(seriesDir, "trials", "999.json"), "{}\n")
			},
			want: "trials directory has 2 entries, want 1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, seriesDir, _, _, _ := writeArtifactFixture(t)
			var series benchmarkrun.Series
			readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
			test.edit(t, seriesDir, &series)
			writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
			verification, err := Verify(root, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !containsArtifactIssue(verification.Issues, test.want) {
				t.Fatalf("tamper verified; want %q in %v", test.want, verification.Issues)
			}
		})
	}
}

func TestVerifyRejectsPhaseJournalBindingAndMirrorTampering(t *testing.T) {
	t.Run("passed trial without linked reference", func(t *testing.T) {
		root, seriesDir, _, _, _ := writeArtifactFixture(t)
		var series benchmarkrun.Series
		readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
		series.Trials[0].PhaseJournal = nil
		writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
		writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "passed without a linked benchmark phase journal reference") {
			t.Fatalf("missing phase journal reference verified: %v", verification.Issues)
		}
	})

	t.Run("series mirror differs", func(t *testing.T) {
		root, seriesDir, _, _, _ := writeArtifactFixture(t)
		mirror := filepath.Join(seriesDir, "driver-logs", "trial-001-phases.tsv")
		content, err := os.ReadFile(mirror)
		if err != nil {
			t.Fatal(err)
		}
		writeArtifactFile(t, mirror, strings.Replace(string(content), "zero warmup duration", "no warmup configured", 1))
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "differs from series mirror") {
			t.Fatalf("divergent phase journal mirror verified: %v", verification.Issues)
		}
	})

	t.Run("coordinated row run rebinding", func(t *testing.T) {
		root, seriesDir, _, _, linkedRunDir := writeArtifactFixture(t)
		mirror := filepath.Join(seriesDir, "driver-logs", "trial-001-phases.tsv")
		primary := filepath.Join(linkedRunDir, "artifacts", "benchmark", "phases.tsv")
		content, err := os.ReadFile(primary)
		if err != nil {
			t.Fatal(err)
		}
		tampered := strings.ReplaceAll(string(content), "synthetic-series-t001\t1\t", "other-series-t001\t1\t")
		writeArtifactFile(t, primary, tampered)
		writeArtifactFile(t, mirror, tampered)
		var series benchmarkrun.Series
		readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
		series.Trials[0].PhaseJournal = artifactFixtureRef(t, linkedRunDir, primary)
		writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
		writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "run id mismatch") {
			t.Fatalf("coordinated phase journal run rebinding verified: %v", verification.Issues)
		}
	})
}

func TestVerifyIndependentlyValidatesOnDiskEnvironment(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	path := filepath.Join(seriesDir, "environment.json")
	var environment benchmarkrun.Environment
	readArtifactJSON(t, path, &environment)
	environment.RuntimeOS = "darwin"
	// Keep the former digest. The verifier must validate the on-disk document
	// itself rather than comparing only its retained digest field.
	writeArtifactJSON(t, path, environment)

	verification, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"environment.json digest does not match environment fields",
		"environment.json does not fully match result.json environment",
	} {
		if !containsArtifactIssue(verification.Issues, want) {
			t.Fatalf("on-disk environment tamper omitted %q: %v", want, verification.Issues)
		}
	}
}

func TestVerifyRejectsCoordinatedEnvironmentIdentityRewrite(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	var series benchmarkrun.Series
	readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
	if series.Environment == nil {
		t.Fatal("fixture has no benchmark environment")
	}
	series.Environment.RuntimeOS = "darwin"
	series.Environment.Digest = benchmarkEnvironmentDigest(t, *series.Environment)
	series.Trials[0].EnvironmentDigest = series.Environment.Digest
	writeArtifactJSON(t, filepath.Join(seriesDir, "environment.json"), series.Environment)
	writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)

	verification, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if verification.IsValid() || !containsArtifactIssue(verification.Issues, "benchmark environment field runtime_os does not match independently derived linked evidence") {
		t.Fatalf("coordinated embedded/on-disk/trial environment rewrite verified: %v", verification.Issues)
	}
}

func TestVerifyRejectsArtifactReferencesThroughAliasedAncestors(t *testing.T) {
	tests := []struct {
		name      string
		aliasPath func(summaryPath, rawPath string) string
		wantIssue string
	}{
		{
			name: "summary ancestor",
			aliasPath: func(summaryPath, _ string) string {
				return filepath.Dir(summaryPath)
			},
			wantIssue: "summary path is unsafe",
		},
		{
			name: "raw-log ancestor",
			aliasPath: func(_, rawPath string) string {
				return filepath.Dir(rawPath)
			},
			wantIssue: "raw log path is unsafe",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, seriesDir, summaryPath, rawPath, _ := writeArtifactFixture(t)
			alias := test.aliasPath(summaryPath, rawPath)
			target := alias + "-real"
			if err := os.Rename(alias, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(target), alias); err != nil {
				t.Fatal(err)
			}

			verification, err := Verify(root, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !containsArtifactIssue(verification.Issues, test.wantIssue) {
				t.Fatalf("artifact ancestor alias verified without %q: %v", test.wantIssue, verification.Issues)
			}
		})
	}
}

func TestVerifyRejectsRedigestedTPSInconsistentWithMeasureTimeline(t *testing.T) {
	root, seriesDir, summaryPath, _, linkedRunDir := writeArtifactFixture(t)
	content, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(content), "tps = 100.000000", "tps = 200.000000", 1)
	writeArtifactFile(t, summaryPath, tampered)
	parsed, err := pgbenchresult.Parse(strings.NewReader(tampered))
	if err != nil {
		t.Fatal(err)
	}

	var series benchmarkrun.Series
	readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
	value := 200.0
	series.Trials[0].Pgbench = &parsed
	series.Trials[0].PrimaryValue = &value
	series.Trials[0].Summary = artifactFixtureRef(t, linkedRunDir, summaryPath)
	writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)

	verification, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if verification.IsValid() || !containsArtifactIssue(verification.Issues, "pgbench TPS integrity failed") {
		t.Fatalf("redigested TPS/count/measure contradiction passed: %v", verification.Issues)
	}
}

func TestVerifyRejectsTamperedSummaryRawLogAndLinkedRun(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(t *testing.T, summaryPath, rawPath, linkedRunDir string)
		wantIssue string
	}{
		{
			name: "summary digest",
			mutate: func(t *testing.T, summaryPath, _, _ string) {
				t.Helper()
				if err := os.WriteFile(summaryPath, []byte("tampered pgbench summary\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantIssue: "summary digest mismatch",
		},
		{
			name: "raw log digest",
			mutate: func(t *testing.T, _, rawPath, _ string) {
				t.Helper()
				if err := os.WriteFile(rawPath, []byte("tampered raw transaction log\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantIssue: "raw log digest mismatch",
		},
		{
			name: "linked experiment verdict",
			mutate: func(t *testing.T, _, _, linkedRunDir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(linkedRunDir, "verdict.json")); err != nil {
					t.Fatal(err)
				}
			},
			wantIssue: "linked run is invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, seriesDir, summaryPath, rawPath, linkedRunDir := writeArtifactFixture(t)
			test.mutate(t, summaryPath, rawPath, linkedRunDir)
			result, err := Verify(root, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if result.IsValid() {
				t.Fatal("tampered artifact unexpectedly verified")
			}
			if !containsArtifactIssue(result.Issues, test.wantIssue) {
				t.Fatalf("issues do not contain %q: %v", test.wantIssue, result.Issues)
			}
		})
	}
}

func TestVerifyRejectsRedigestedInvalidProtocolDeclaration(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	var plan benchmarkplan.Plan
	readArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), &plan)
	plan.CacheRegime = "invented"
	var err error
	plan.ProtocolDigest, plan.ComparisonKeyDigest, err = benchmarkplan.IdentityDigests(plan)
	if err != nil {
		t.Fatal(err)
	}
	writeArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), plan)

	result, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsValid() || !containsArtifactIssue(result.Issues, "plan identity verification failed: invalid declared cache regime") {
		t.Fatalf("redigested invalid protocol declaration passed: %v", result.Issues)
	}
}

func TestVerifyBindsRedigestedValidDeclarationToSpecSnapshot(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	var plan benchmarkplan.Plan
	readArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), &plan)
	plan.CacheRegime = "steady"
	var err error
	plan.ProtocolDigest, plan.ComparisonKeyDigest, err = benchmarkplan.IdentityDigests(plan)
	if err != nil {
		t.Fatal(err)
	}
	writeArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), plan)
	var series benchmarkrun.Series
	readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
	series.ProtocolDigest = plan.ProtocolDigest
	series.ComparisonKeyDigest = plan.ComparisonKeyDigest
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)

	result, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsValid() || !containsArtifactIssue(result.Issues, "plan declarations do not match benchmark spec snapshot: BENCHMARK_CACHE_REGIME") {
		t.Fatalf("valid redigested declaration escaped its immutable spec snapshot: %v", result.Issues)
	}
}

func TestVerifyBindsDerivedWorkloadFieldsToSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(t *testing.T, seriesDir string, plan *benchmarkplan.Plan)
		wantIssue string
	}{
		{
			name: "workload kind",
			mutate: func(t *testing.T, seriesDir string, plan *benchmarkplan.Plan) {
				path := filepath.Join(seriesDir, "protocol", "workload-spec.env")
				writeArtifactFile(t, path, "WORKLOAD_KIND=shell\n")
				plan.WorkloadDigest = artifactFixtureDigest(t, path)
			},
			wantIssue: "WORKLOAD_KIND declaration must be pgbench",
		},
		{
			name: "workload mode",
			mutate: func(_ *testing.T, _ string, plan *benchmarkplan.Plan) {
				plan.WorkloadMode = "select-only"
			},
			wantIssue: "PGBENCH_MODE declaration does not match plan",
		},
		{
			name: "workload script",
			mutate: func(_ *testing.T, _ string, plan *benchmarkplan.Plan) {
				plan.WorkloadScript = "workloads/pgbench/scripts/foreign.sql"
				plan.WorkloadScriptDigest = "sha256:" + strings.Repeat("a", 64)
			},
			wantIssue: "PGBENCH_SCRIPT declaration does not match plan",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, seriesDir, _, _, _ := writeArtifactFixture(t)
			var plan benchmarkplan.Plan
			readArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), &plan)
			test.mutate(t, seriesDir, &plan)
			var err error
			plan.ProtocolDigest, plan.ComparisonKeyDigest, err = benchmarkplan.IdentityDigests(plan)
			if err != nil {
				t.Fatal(err)
			}
			writeArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), plan)
			var series benchmarkrun.Series
			readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
			series.ProtocolDigest = plan.ProtocolDigest
			series.ComparisonKeyDigest = plan.ComparisonKeyDigest
			writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)

			verification, err := Verify(root, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !containsArtifactIssue(verification.Issues, test.wantIssue) {
				t.Fatalf("derived workload substitution passed without %q: %v", test.wantIssue, verification.Issues)
			}
		})
	}
}

func TestVerifyBindsTrialOutcomeAndIntervalToLinkedVerdictAndPhases(t *testing.T) {
	t.Run("failed linked verdict", func(t *testing.T) {
		root, seriesDir, _, _, linkedRunDir := writeArtifactFixture(t)
		var series benchmarkrun.Series
		readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
		verdict := runstate.Verdict{
			RunID:            series.Trials[0].RunID,
			Status:           runstate.VerdictStatusFailed,
			Message:          "synthetic workload failed",
			StartedAt:        "2026-08-12T00:00:00Z",
			FinishedAt:       "2026-08-12T00:00:30Z",
			ExperimentSpecID: "benchmarks/pgbench",
			WorkloadExit:     1,
		}
		if err := runstate.WriteVerdict(linkedRunDir, verdict); err != nil {
			t.Fatal(err)
		}
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "passed despite failed linked verdict") || !containsArtifactIssue(verification.Issues, "does not retain failed linked verdict outcome") {
			t.Fatalf("failed linked verdict was not bound to trial outcome: %v", verification.Issues)
		}
	})

	t.Run("trial interval differs from phases", func(t *testing.T) {
		root, seriesDir, _, _, _ := writeArtifactFixture(t)
		var series benchmarkrun.Series
		readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
		series.Trials[0].StartedAt = "2026-08-12T00:00:01Z"
		series.Trials[0].DurationMS = 29000
		writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
		writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "interval does not match its normalized benchmark phase timeline") {
			t.Fatalf("independently edited trial interval passed: %v", verification.Issues)
		}
	})

	t.Run("phase interval does not contain verdict", func(t *testing.T) {
		root, seriesDir, _, _, _ := writeArtifactFixture(t)
		journal := filepath.Join(seriesDir, "driver-logs", "trial-001-phases.tsv")
		content, err := os.ReadFile(journal)
		if err != nil {
			t.Fatal(err)
		}
		shifted := strings.ReplaceAll(string(content), "2026-08-12T00:00:00Z", "2026-08-12T01:00:00Z")
		shifted = strings.ReplaceAll(shifted, "2026-08-12T00:00:30Z", "2026-08-12T01:00:30Z")
		writeArtifactFile(t, journal, shifted)
		file, err := os.Open(journal)
		if err != nil {
			t.Fatal(err)
		}
		timeline, parseErr := benchmarkphase.ParseTSV(file, 1)
		if err := errors.Join(parseErr, file.Close()); err != nil {
			t.Fatal(err)
		}
		var series benchmarkrun.Series
		readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
		series.Trials[0].PhaseTimeline = &timeline
		series.Trials[0].StartedAt = timeline.StartedAt
		series.Trials[0].FinishedAt = timeline.FinishedAt
		series.Trials[0].DurationMS = timeline.DurationMS
		writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
		writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "lifecycle does not contain the linked verdict interval") {
			t.Fatalf("out-of-interval phase timeline passed: %v", verification.Issues)
		}
	})
}

func TestMetricsCoverageMustSpanMeasurePhase(t *testing.T) {
	trial := benchmarkrun.Trial{PhaseTimeline: &benchmarkphase.Timeline{Events: []benchmarkphase.Event{
		{Name: benchmarkphase.MeasureName, StartedAt: "2026-08-12T00:00:10Z", FinishedAt: "2026-08-12T00:00:20Z"},
	}}}
	valid := VerifyResult{Issues: []string{}}
	checkMetricsCoverage(&valid, 1, trial, &runverify.MetricsCoverage{Samples: 2, First: "2026-08-12T00:00:09Z", Last: "2026-08-12T00:00:21Z"})
	if !valid.IsValid() {
		t.Fatalf("complete metrics coverage was rejected: %v", valid.Issues)
	}
	early := VerifyResult{Issues: []string{}}
	checkMetricsCoverage(&early, 1, trial, &runverify.MetricsCoverage{Samples: 2, First: "2026-08-12T00:00:09Z", Last: "2026-08-12T00:00:19Z"})
	if early.IsValid() || !containsArtifactIssue(early.Issues, "do not cover the complete measure phase") {
		t.Fatalf("early collector exit passed: %v", early.Issues)
	}
}

func TestVerifyIndependentlyRecomputesPostgresSamplerSummary(t *testing.T) {
	t.Run("missing normalized summary", func(t *testing.T) {
		root, seriesDir, _, _, _ := writeArtifactFixture(t)
		var series benchmarkrun.Series
		readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
		series.Trials[0].PostgresMetrics = nil
		writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
		writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "passed without normalized PostgreSQL sampler summary") {
			t.Fatalf("passed trial without PostgreSQL summary verified: %v", verification.Issues)
		}
	})

	t.Run("self-consistent summary from different source", func(t *testing.T) {
		root, seriesDir, _, _, _ := writeArtifactFixture(t)
		alternatePath := filepath.Join(t.TempDir(), benchmarkmetrics.SourcePath)
		writeArtifactFile(t, alternatePath, string(metricstest.CSV([]string{
			"2026-08-12T00:00:00Z", "2026-08-12T00:00:15Z", "2026-08-12T00:00:31Z",
		}, "other_database")))
		alternate, err := benchmarkmetrics.DeriveFile(benchmarkmetrics.DeriveOptions{
			Path: alternatePath, PostgresServerMajor: "17", MeasureStartedAt: "2026-08-12T00:00:00Z", MeasureFinishedAt: "2026-08-12T00:00:30Z",
			CollectorIntervalSeconds: 15, ContractVersion: "1", StatisticsResetPolicy: "none", StatisticsResetBoundary: "none",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := benchmarkmetrics.VerifyDigest(alternate); err != nil {
			t.Fatalf("alternate summary must be internally valid: %v", err)
		}
		var series benchmarkrun.Series
		readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
		series.Trials[0].PostgresMetrics = &alternate
		writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
		writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "does not match linked metrics.csv") {
			t.Fatalf("summary from different source verified: %v", verification.Issues)
		}
	})

	t.Run("WAL counter reset in linked source", func(t *testing.T) {
		root, seriesDir, _, _, linkedRunDir := writeArtifactFixture(t)
		mutateArtifactMetricsCell(t, filepath.Join(linkedRunDir, benchmarkmetrics.SourcePath), 1, "wal_bytes", "1")
		verification, err := Verify(root, seriesDir)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !containsArtifactIssue(verification.Issues, "wal_bytes decreased") {
			t.Fatalf("linked WAL reset verified: %v", verification.Issues)
		}
	})
}

func TestVerifyRejectsTraversalInLinkedRunIdentity(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	var series benchmarkrun.Series
	readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
	series.Trials[0].RunID = "../../outside"
	// This is the exact value the former filepath.Join-based canonical check
	// derived, despite it escaping the required runs/ subtree.
	series.Trials[0].RunRef = filepath.ToSlash(filepath.Join("runs", series.Trials[0].RunID))
	writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
	writeArtifactFile(t, filepath.Join(seriesDir, "runs.tsv"), fmt.Sprintf("trial\trun_id\tstatus\tprimary_value\trun_ref\n1\t%s\tpassed\t100\t%s\n", series.Trials[0].RunID, series.Trials[0].RunRef))

	result, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsValid() {
		t.Fatal("linked-run traversal unexpectedly verified")
	}
	for _, want := range []string{"invalid run id", "non-canonical run_ref", "linked run path is unsafe"} {
		if !containsArtifactIssue(result.Issues, want) {
			t.Fatalf("issues do not contain %q: %v", want, result.Issues)
		}
	}
	if _, err := CreateBundle(root, seriesDir, filepath.Join(t.TempDir(), "unsafe.tar.gz"), time.Unix(0, 0)); err == nil {
		t.Fatal("bundle creation accepted linked-run traversal")
	}
}

func TestSafeJoinRejectsTraversalAndExistingSymlink(t *testing.T) {
	root := t.TempDir()
	for _, ref := range []string{"../outside", "runs/../../outside"} {
		if _, err := safeJoin(root, ref); err == nil {
			t.Fatalf("safeJoin accepted traversal %q", ref)
		}
	}

	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "runs", "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeExistingJoin(root, "runs/escape"); err == nil {
		t.Fatal("safeExistingJoin accepted a linked-run symlink")
	}
}

func TestVerifyRejectsMissingOrForgedNormalizedTransactionLog(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*benchmarkrun.Trial)
		wantIssue string
	}{
		{
			name: "missing",
			mutate: func(trial *benchmarkrun.Trial) {
				trial.TransactionLog = nil
			},
			wantIssue: "has no normalized transaction-log result",
		},
		{
			name: "forged count",
			mutate: func(trial *benchmarkrun.Trial) {
				trial.TransactionLog.Completed++
			},
			wantIssue: "normalized transaction-log result does not match raw logs",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, seriesDir, _, _, _ := writeArtifactFixture(t)
			var series benchmarkrun.Series
			content, err := os.ReadFile(filepath.Join(seriesDir, "result.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(content, &series); err != nil {
				t.Fatal(err)
			}
			test.mutate(&series.Trials[0])
			writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
			writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)

			verification, err := Verify(root, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !containsArtifactIssue(verification.Issues, test.wantIssue) {
				t.Fatalf("expected issue containing %q, got %v", test.wantIssue, verification.Issues)
			}
		})
	}
}

func TestVerifyRejectsPassedTrialWithFailedRawTransaction(t *testing.T) {
	root, seriesDir, _, rawPath, linkedRunDir := writeArtifactFixture(t)
	writeArtifactFile(t, rawPath, "0 1 failed 0 1786492800 780769\n1 1 200 0 1786492800 781111\n")
	transactionLog, err := pgbenchlog.ParseFiles([]string{rawPath}, pgbenchlog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var series benchmarkrun.Series
	content, err := os.ReadFile(filepath.Join(seriesDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &series); err != nil {
		t.Fatal(err)
	}
	series.Trials[0].RawLogs = []benchmarkrun.ArtifactRef{*artifactFixtureRef(t, linkedRunDir, rawPath)}
	series.Trials[0].TransactionLog = &transactionLog
	writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)

	verification, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if verification.IsValid() || !containsArtifactIssue(verification.Issues, "reported 1 failed transactions") {
		t.Fatalf("failed raw transaction did not invalidate passed artifact: %v", verification.Issues)
	}
}

func TestVerifyRejectsSelfConsistentRawTransactionWindowOutsideMeasure(t *testing.T) {
	root, seriesDir, _, rawPath, linkedRunDir := writeArtifactFixture(t)
	raw := strings.ReplaceAll(artifactPgbenchLog(3000, 2), "1786492800", "1786496400")
	writeArtifactFile(t, rawPath, raw)
	transactionLog, err := pgbenchlog.ParseFiles([]string{rawPath}, pgbenchlog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var series benchmarkrun.Series
	content, err := os.ReadFile(filepath.Join(seriesDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &series); err != nil {
		t.Fatal(err)
	}
	series.Trials[0].RawLogs = []benchmarkrun.ArtifactRef{*artifactFixtureRef(t, linkedRunDir, rawPath)}
	series.Trials[0].TransactionLog = &transactionLog
	writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), series.Trials[0])
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)

	verification, err := Verify(root, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if verification.IsValid() || !containsArtifactIssue(verification.Issues, "completion window falls outside the measure phase") {
		t.Fatalf("out-of-phase raw transaction window passed: %v", verification.Issues)
	}
}

func TestVerifyIndependentlyDerivesSeriesPolicy(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*benchmarkrun.Series)
		wantIssue string
	}{
		{
			name: "forged status",
			mutate: func(series *benchmarkrun.Series) {
				series.Status = "inconclusive"
			},
			wantIssue: "does not match independently derived status",
		},
		{
			name: "forged planned count",
			mutate: func(series *benchmarkrun.Series) {
				series.TrialsPlanned = 2
			},
			wantIssue: "trial counts do not match the benchmark protocol",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, seriesDir, _, _, _ := writeArtifactFixture(t)
			var series benchmarkrun.Series
			readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
			test.mutate(&series)
			writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
			verification, err := Verify(root, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !containsArtifactIssue(verification.Issues, test.wantIssue) {
				t.Fatalf("expected issue containing %q, got %v", test.wantIssue, verification.Issues)
			}
		})
	}
}

func TestBenchmarkBundleIsReproducibleAndVerifiesAfterRelocation(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	firstPath := filepath.Join(t.TempDir(), "first.tar.gz")
	secondPath := filepath.Join(t.TempDir(), "second.tar.gz")
	epoch := time.Unix(1_700_000_000, 0).UTC()
	first, err := CreateBundle(root, seriesDir, firstPath, epoch)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateBundle(root, seriesDir, secondPath, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.IsDigest(first.Digest) || first.Digest != second.Digest || first.Files == 0 || first.LinkedRuns != 1 {
		t.Fatalf("unexpected reproducible bundle metadata: first=%#v second=%#v", first, second)
	}
	left, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != string(right) {
		t.Fatal("identical benchmark bundles differ byte-for-byte")
	}

	extractRoot := t.TempDir()
	extractBenchmarkBundle(t, firstPath, extractRoot)
	bundleRoot := filepath.Join(extractRoot, first.RootName)
	relocatedSeries := filepath.Join(bundleRoot, "runs", "benchmarks", first.SeriesRunID)
	verification, err := VerifyBundle(bundleRoot, relocatedSeries)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.IsValid() {
		t.Fatalf("relocated benchmark bundle did not verify: %v", verification.Issues)
	}
}

func TestGenericBundleRequiresABClosureForNativeToolchainArm(t *testing.T) {
	if !requiresABBundle(benchmarkrun.Series{Environment: &benchmarkrun.Environment{SubjectDimension: "native_toolchain"}}) {
		t.Fatal("native_toolchain A/B arm did not require the dedicated A/B bundle closure")
	}
	if requiresABBundle(benchmarkrun.Series{Environment: &benchmarkrun.Environment{SubjectDimension: "pg_config"}}) {
		t.Fatal("ordinary pg_config series was incorrectly restricted to an A/B bundle")
	}
}

func TestCreateBenchmarkBundleRejectsTamperedStageBeforeArchive(t *testing.T) {
	root, seriesDir, _, _, _ := writeArtifactFixture(t)
	output := filepath.Join(t.TempDir(), "tampered-stage.tar.gz")
	_, err := createBundle(root, seriesDir, output, time.Unix(0, 0).UTC(), func(stage string) error {
		path := filepath.Join(stage, "runs", "benchmarks", filepath.Base(seriesDir), "result.json")
		file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if openErr != nil {
			return openErr
		}
		_, writeErr := file.WriteString(" ")
		return errors.Join(writeErr, file.Close())
	})
	if err == nil || !strings.Contains(err.Error(), "staged benchmark bundle is invalid") {
		t.Fatalf("tampered staged benchmark bundle was published: %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive exists after staged verification failure: %v", statErr)
	}
}

func TestBenchmarkBundleRejectsOutputInsidePrimaryOrAliasedLinkedArtifact(t *testing.T) {
	root, seriesDir, _, _, linkedRunDir := writeArtifactFixture(t)
	alias := filepath.Join(t.TempDir(), "linked-run-alias")
	if err := os.Symlink(linkedRunDir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tests := []struct {
		name   string
		output string
		target string
	}{
		{name: "direct series child", output: filepath.Join(seriesDir, "direct.tar.gz"), target: filepath.Join(seriesDir, "direct.tar.gz")},
		{name: "aliased linked-run parent", output: filepath.Join(alias, "aliased.tar.gz"), target: filepath.Join(linkedRunDir, "aliased.tar.gz")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CreateBundle(root, seriesDir, test.output, time.Unix(0, 0))
			if !errors.Is(err, pathguard.ErrOutputWithinSource) {
				t.Fatalf("expected output-containment error, got %v", err)
			}
			if _, statErr := os.Lstat(test.target); !os.IsNotExist(statErr) {
				t.Fatalf("output was written inside immutable bundle source: %v", statErr)
			}
		})
	}
}

func writeArtifactFixture(t *testing.T) (root, seriesDir, summaryPath, rawPath, linkedRunDir string) {
	t.Helper()
	root = t.TempDir()
	seriesID := "synthetic-series"
	trialRunID := seriesID + "-t001"
	seriesDir = filepath.Join(root, "runs", "benchmarks", seriesID)
	linkedRunDir = filepath.Join(root, "runs", trialRunID)
	for _, dir := range []string{
		filepath.Join(seriesDir, "trials"),
		filepath.Join(seriesDir, "driver-logs"),
		filepath.Join(linkedRunDir, "driver", "pgbench-raw"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeArtifactFile(t, filepath.Join(seriesDir, "benchmark-spec.env"), strings.Join([]string{
		"BENCHMARK_NAME=Synthetic verifier fixture",
		"BENCHMARK_CLASS=smoke",
		"BENCHMARK_DRIVER=pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/tiny",
		"BENCHMARK_PG_CONFIG=default",
		"BENCHMARK_MODE=fixed-time",
		"BENCHMARK_SCALE=1",
		"BENCHMARK_CLIENTS=2",
		"BENCHMARK_THREADS=1",
		"BENCHMARK_WARMUP_SECONDS=0",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_TRIALS=1",
		"BENCHMARK_MIN_VALID_TRIALS=1",
		"BENCHMARK_RESET_POLICY=rebuild-per-trial",
		"BENCHMARK_CACHE_REGIME=uncontrolled",
		"BENCHMARK_STATISTICS_RESET_POLICY=none",
		"BENCHMARK_STATISTICS_RESET_BOUNDARY=none",
		"BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v1'",
		"BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1",
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified",
		"BENCHMARK_CLIENT_PLACEMENT=same-host",
		"BENCHMARK_RESOURCE_BUDGET_MODE=unbounded",
		"BENCHMARK_PRIMARY_METRIC=pgbench.tps",
		"BENCHMARK_DIRECTION=higher",
		"BENCHMARK_MAX_CV_PCT=100",
		"BENCHMARK_PROTOCOL=simple",
		"BENCHMARK_LOG_TRANSACTIONS=1",
		"BENCHMARK_LOG_SAMPLE_RATE=1",
		"BENCHMARK_ALLOWED_SUBJECT_DIFFERENCES=pg_config",
		"",
	}, "\n"))
	writeArtifactFile(t, filepath.Join(seriesDir, "protocol", "experiment-spec.env"), "EXPERIMENT_NAME=Synthetic benchmark trial\n")
	writeArtifactFile(t, filepath.Join(seriesDir, "protocol", "workload-spec.env"), "WORKLOAD_KIND=pgbench\n")
	writeArtifactFile(t, filepath.Join(seriesDir, "protocol", "postgresql.conf"), "shared_buffers = '128MB'\n")
	writeArtifactFile(t, filepath.Join(seriesDir, "summary.md"), "# Synthetic benchmark series\n")
	writeArtifactFile(t, filepath.Join(seriesDir, "driver-logs", "trial-001.log"), "driver output\n")
	phaseJournalPath := filepath.Join(seriesDir, "driver-logs", "trial-001-phases.tsv")
	writeArtifactFile(t, phaseJournalPath, strings.Join([]string{
		trialRunID + "\t1\t1\tpreflight\tpassed\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00Z\t",
		trialRunID + "\t1\t2\tprepare\tpassed\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00Z\t",
		trialRunID + "\t1\t3\tstabilize\tskipped\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00Z\tno stabilization gate declared",
		trialRunID + "\t1\t4\tpre-warmup-control\tskipped\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00Z\tprotocol controls are not enabled",
		trialRunID + "\t1\t5\twarmup\tskipped\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00Z\tzero warmup duration",
		trialRunID + "\t1\t6\tpre-measure-control\tskipped\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00Z\tprotocol controls are not enabled",
		trialRunID + "\t1\t7\tmeasure\tpassed\t2026-08-12T00:00:00Z\t2026-08-12T00:00:30Z\t",
		trialRunID + "\t1\t8\tcooldown\tpassed\t2026-08-12T00:00:30Z\t2026-08-12T00:00:30Z\t",
		trialRunID + "\t1\t9\tvalidate\tpassed\t2026-08-12T00:00:30Z\t2026-08-12T00:00:30Z\t",
		trialRunID + "\t1\t10\tcollect\tpassed\t2026-08-12T00:00:30Z\t2026-08-12T00:00:30Z\t",
		trialRunID + "\t1\t11\tcleanup\tpassed\t2026-08-12T00:00:30Z\t2026-08-12T00:00:30Z\t",
	}, "\n")+"\n")
	linkedPhasePath := filepath.Join(linkedRunDir, "artifacts", "benchmark", "phases.tsv")
	phaseContent, err := os.ReadFile(phaseJournalPath)
	if err != nil {
		t.Fatal(err)
	}
	writeArtifactFile(t, linkedPhasePath, string(phaseContent))
	phaseFile, err := os.Open(phaseJournalPath)
	if err != nil {
		t.Fatal(err)
	}
	phaseTimeline, parsePhaseErr := benchmarkphase.ParseTSV(phaseFile, 1, trialRunID)
	if err := errors.Join(parsePhaseErr, phaseFile.Close()); err != nil {
		t.Fatal(err)
	}
	summaryPath = filepath.Join(linkedRunDir, "driver", "pgbench-summary.log")
	rawPath = filepath.Join(linkedRunDir, "driver", "pgbench-raw", "pgbench_log.1")
	writeArtifactFile(t, summaryPath, strings.Join([]string{
		"pgbench (17.9, server 17.9)",
		"transaction type: <builtin: TPC-B (sort of)>",
		"scaling factor: 1",
		"query mode: simple",
		"number of clients: 2",
		"number of threads: 1",
		"maximum number of tries: 1",
		"duration: 30 s",
		"number of transactions actually processed: 3000",
		"number of failed transactions: 0 (0.000%)",
		"latency average = 0.150 ms",
		"latency stddev = 0.100 ms",
		"initial connection time = 2.000 ms",
		"tps = 100.000000 (without initial connection time)",
	}, "\n")+"\n")
	writeArtifactFile(t, rawPath, artifactPgbenchLog(3000, 2))
	transactionLog, err := pgbenchlog.ParseFiles([]string{rawPath}, pgbenchlog.Options{})
	if err != nil {
		t.Fatal(err)
	}

	writeVerifiedLinkedRun(t, linkedRunDir, trialRunID, "docker", seriesDir)
	measure, ok := benchmarkphase.EventByName(phaseTimeline, benchmarkphase.MeasureName)
	if !ok {
		t.Fatal("fixture phase timeline has no measure")
	}
	postgresMetrics, err := benchmarkmetrics.DeriveFile(benchmarkmetrics.DeriveOptions{
		Path: filepath.Join(linkedRunDir, benchmarkmetrics.SourcePath), PostgresServerMajor: "17",
		MeasureStartedAt: measure.StartedAt, MeasureFinishedAt: measure.FinishedAt,
		CollectorIntervalSeconds: 1, ContractVersion: "1", StatisticsResetPolicy: "none", StatisticsResetBoundary: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := runverify.Verify(root, trialRunID)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid() {
		t.Fatalf("linked-run fixture is invalid: %v", verification.Issues)
	}

	specDigest, err := evidence.DigestFile(filepath.Join(seriesDir, "benchmark-spec.env"))
	if err != nil {
		t.Fatal(err)
	}
	targetTopologyContent := "TOPOLOGY_NAME=single\nTOPOLOGY_SERVICES=postgres\n"
	targetTopologySnapshot := filepath.Join(seriesDir, "protocol", "target-topology.env")
	writeArtifactFile(t, targetTopologySnapshot, targetTopologyContent)
	plan := benchmarkplan.Plan{
		ProtocolSchemaVersion:     benchmarkplan.ProtocolSchemaVersion,
		Spec:                      "synthetic",
		SpecPath:                  "benchmarks/synthetic.env",
		SpecDigest:                specDigest,
		Name:                      "Synthetic verifier fixture",
		Class:                     "smoke",
		Driver:                    "pgbench",
		Target:                    benchmarkplan.TargetDirectPostgres,
		TargetEndpointContract:    benchmarkplan.EndpointDirectV1,
		TargetTopology:            "single",
		TargetTopologyPath:        "topologies/single.env",
		TargetTopologyDigest:      artifactFixtureDigest(t, targetTopologySnapshot),
		ExperimentSpec:            "benchmarks/pgbench",
		ExperimentPath:            "experiments/benchmarks/pgbench.env",
		ExperimentDigest:          artifactFixtureDigest(t, filepath.Join(seriesDir, "protocol", "experiment-spec.env")),
		WorkloadSpec:              "pgbench/tiny",
		WorkloadPath:              "workloads/pgbench/tiny.env",
		WorkloadDigest:            artifactFixtureDigest(t, filepath.Join(seriesDir, "protocol", "workload-spec.env")),
		WorkloadMode:              "builtin",
		PGConfig:                  "default",
		PGConfigPath:              "configs/default/postgresql.conf",
		PGConfigDigest:            artifactFixtureDigest(t, filepath.Join(seriesDir, "protocol", "postgresql.conf")),
		Mode:                      "fixed-time",
		Scale:                     1,
		Clients:                   2,
		Threads:                   1,
		MeasureSeconds:            30,
		Trials:                    1,
		MinValidTrials:            1,
		CacheRegime:               "uncontrolled",
		StatisticsResetPolicy:     "none",
		StatisticsResetBoundary:   "none",
		Collectors:                []string{"pgbench-driver", "postgresql-sampler-v1"},
		CollectorIntervalSeconds:  1,
		CollectorOverheadMode:     "included-unquantified",
		ClientPlacement:           "same-host",
		ResourceBudgetMode:        "unbounded",
		PrimaryMetric:             "pgbench.tps",
		Direction:                 "higher",
		QueryProtocol:             "simple",
		RandomSeedSemantics:       "client-random-default",
		LogTransactions:           true,
		LogSampleRate:             1,
		AllowedSubjectDifferences: []string{"pg_config"},
		MaxCVPct:                  100,
		ResetPolicy:               "rebuild-per-trial",
		RuntimeReset:              true,
	}
	protocolDigest, comparisonDigest, err := benchmarkplan.IdentityDigests(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.ProtocolDigest = protocolDigest
	plan.ComparisonKeyDigest = comparisonDigest
	capsuleRoot := filepath.Join(seriesDir, "protocol", "capsule")
	writeArtifactCopy(t, filepath.Join(seriesDir, "benchmark-spec.env"), filepath.Join(capsuleRoot, "benchmarks", "synthetic.env"))
	writeArtifactCopy(t, filepath.Join(seriesDir, "protocol", "experiment-spec.env"), filepath.Join(capsuleRoot, "experiments", "benchmarks", "pgbench.env"))
	writeArtifactCopy(t, filepath.Join(seriesDir, "protocol", "workload-spec.env"), filepath.Join(capsuleRoot, "workloads", "pgbench", "tiny.env"))
	writeArtifactCopy(t, filepath.Join(seriesDir, "protocol", "postgresql.conf"), filepath.Join(capsuleRoot, "configs", "default", "postgresql.conf"))
	writeArtifactCopy(t, targetTopologySnapshot, filepath.Join(capsuleRoot, "topologies", "single.env"))
	writeArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), plan)

	enginePath := filepath.Join(seriesDir, filepath.FromSlash(benchmarkrun.EngineBinarySeriesRef))
	writeArtifactFile(t, enginePath, "synthetic benchmark engine\n")
	engineDigest := artifactFixtureDigest(t, enginePath)
	environment := benchmarkrun.Environment{
		SchemaVersion:             benchmarkrun.EnvironmentSchemaVersion,
		ArtifactType:              benchmarkrun.EnvironmentArtifactType,
		Runtime:                   "docker",
		RuntimeOS:                 "linux",
		RuntimeArch:               "amd64",
		Driver:                    "pgbench",
		Target:                    benchmarkplan.TargetDirectPostgres,
		TargetEndpointContract:    benchmarkplan.EndpointDirectV1,
		TargetEndpointHost:        "127.0.0.1",
		TargetEndpointPort:        5432,
		DockerDriverImageID:       "sha256:" + strings.Repeat("d", 64),
		DockerTargetImageID:       "sha256:" + strings.Repeat("d", 64),
		TargetTopology:            "single",
		DriverVersion:             "17.9",
		ParserVersion:             pgbenchresult.ParserVersion,
		PostgresServerVersionNum:  "170009",
		PostgresServerMajor:       "17",
		PGConfig:                  "default",
		PGConfigDigest:            plan.PGConfigDigest,
		SubjectDimension:          "pg_config",
		NativeToolchainProvenance: "not-applicable",
		EngineVersion:             "0.3.0",
		EngineCommit:              strings.Repeat("a", 40),
		EngineBinaryDigest:        engineDigest,
		Qualification:             "unqualified-local",
	}
	environment.Digest = benchmarkEnvironmentDigest(t, environment)
	writeArtifactJSON(t, filepath.Join(seriesDir, "environment.json"), environment)

	value := 100.0
	duration := 30.0
	latencyStddev := 0.1
	initialConnection := 2.0
	trial := benchmarkrun.Trial{
		SchemaVersion:      benchmarkrun.TrialSchemaVersion,
		ArtifactType:       benchmarkrun.TrialArtifactType,
		Trial:              1,
		RunID:              trialRunID,
		RunRef:             filepath.ToSlash(filepath.Join("runs", trialRunID)),
		StartedAt:          "2026-08-12T00:00:00Z",
		FinishedAt:         "2026-08-12T00:00:30Z",
		DurationMS:         30000,
		Status:             "passed",
		Reasons:            []string{},
		ExperimentVerified: true,
		EnvironmentDigest:  environment.Digest,
		Summary:            artifactFixtureRef(t, linkedRunDir, summaryPath),
		RawLogs:            []benchmarkrun.ArtifactRef{*artifactFixtureRef(t, linkedRunDir, rawPath)},
		Pgbench: &pgbenchresult.Result{
			SchemaVersion:           pgbenchresult.ResultSchemaVersion,
			ParserVersion:           pgbenchresult.ParserVersion,
			PgbenchVersion:          "17.9",
			ServerVersion:           "17.9",
			TransactionType:         "<builtin: TPC-B (sort of)>",
			ScaleFactor:             1,
			QueryMode:               "simple",
			Mode:                    pgbenchresult.ModeTime,
			Clients:                 2,
			Threads:                 1,
			MaximumTries:            1,
			DurationSeconds:         &duration,
			TransactionsProcessed:   3000,
			LatencyMeanMS:           0.15,
			LatencyStddevMS:         &latencyStddev,
			InitialConnectionTimeMS: &initialConnection,
			TPSExcludingConnections: &value,
		},
		TransactionLog:  &transactionLog,
		PostgresMetrics: &postgresMetrics,
		PhaseJournal:    artifactFixtureRef(t, linkedRunDir, linkedPhasePath),
		PhaseTimeline:   &phaseTimeline,
		PrimaryMetric:   "pgbench.tps",
		PrimaryValue:    &value,
	}
	series := benchmarkrun.Series{
		SchemaVersion:          benchmarkrun.SeriesSchemaVersion,
		ArtifactType:           benchmarkrun.SeriesArtifactType,
		Benchmark:              "synthetic",
		Name:                   "Synthetic verifier fixture",
		Class:                  "smoke",
		Driver:                 "pgbench",
		Target:                 benchmarkplan.TargetDirectPostgres,
		TargetEndpointContract: benchmarkplan.EndpointDirectV1,
		TargetTopology:         "single",
		Subject:                "default",
		RunID:                  seriesID,
		RunDir:                 ".",
		SpecRef:                "benchmarks/synthetic.env",
		SpecDigest:             specDigest,
		ProtocolDigest:         protocolDigest,
		ComparisonKeyDigest:    comparisonDigest,
		EngineBinaryRef:        benchmarkrun.EngineBinarySeriesRef,
		EngineBinaryDigest:     engineDigest,
		AllowedDifferences:     []string{"pg_config"},
		Runtime:                "docker",
		EvidenceClass:          "smoke-only",
		PrimaryMetric:          "pgbench.tps",
		Direction:              "higher",
		MaxCVPct:               100,
		ResetPolicy:            "rebuild-per-trial",
		StartedAt:              "2026-08-12T00:00:00Z",
		FinishedAt:             "2026-08-12T00:00:30Z",
		Status:                 "passed",
		Reasons:                []string{},
		TrialsPlanned:          1,
		TrialsValid:            1,
		Environment:            &environment,
		Trials:                 []benchmarkrun.Trial{trial},
	}
	writeArtifactFile(t, filepath.Join(seriesDir, "summary.md"), string(benchmarkrun.SummaryBytes(series)))
	writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), trial)
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
	writeArtifactFile(t, filepath.Join(seriesDir, "runs.tsv"), fmt.Sprintf("trial\trun_id\tstatus\tprimary_value\trun_ref\n1\t%s\tpassed\t100\truns/%s\n", trialRunID, trialRunID))
	return root, seriesDir, summaryPath, rawPath, linkedRunDir
}

func upgradeArtifactFixtureToProtocolV2(t *testing.T, seriesDir, linkedRunDir string) {
	t.Helper()

	specPath := filepath.Join(seriesDir, "benchmark-spec.env")
	specContent := string(mustReadArtifactFile(t, specPath))
	const v1Collectors = "BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v1'"
	const v2Collectors = "BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v2'"
	if !strings.Contains(specContent, v1Collectors) {
		t.Fatal("protocol-v2 fixture cannot locate the v1 collector declaration")
	}
	specContent = "BENCHMARK_CONTRACT_VERSION=2\n" + strings.Replace(specContent, v1Collectors, v2Collectors, 1)
	writeArtifactFile(t, specPath, specContent)
	writeArtifactFile(t, filepath.Join(seriesDir, "protocol", "capsule", "benchmarks", "synthetic.env"), specContent)

	var plan benchmarkplan.Plan
	readArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), &plan)
	plan.ProtocolSchemaVersion = benchmarkplan.ProtocolSchemaVersionV2
	plan.ContractVersion = "2"
	plan.SpecDigest = artifactFixtureDigest(t, specPath)
	plan.Collectors = []string{"pgbench-driver", "postgresql-sampler-v2"}
	protocolDigest, comparisonDigest, err := benchmarkplan.IdentityDigests(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.ProtocolDigest = protocolDigest
	plan.ComparisonKeyDigest = comparisonDigest
	if err := benchmarkplan.VerifyDigests(plan); err != nil {
		t.Fatalf("build protocol-v2 fixture plan: %v", err)
	}
	writeArtifactJSON(t, filepath.Join(seriesDir, "plan.json"), plan)

	phaseMirrorPath := filepath.Join(seriesDir, "driver-logs", "trial-001-phases.tsv")
	phaseContent := string(mustReadArtifactFile(t, phaseMirrorPath))
	lines := strings.Split(strings.TrimSuffix(phaseContent, "\n"), "\n")
	updatedControlPhase := false
	for index, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) == 8 && fields[3] == benchmarkphase.PreMeasureControlName {
			fields[4] = "passed"
			fields[7] = ""
			lines[index] = strings.Join(fields, "\t")
			updatedControlPhase = true
		}
	}
	if !updatedControlPhase {
		t.Fatal("protocol-v2 fixture has no pre-measure control phase")
	}
	phaseContent = strings.Join(lines, "\n") + "\n"
	writeArtifactFile(t, phaseMirrorPath, phaseContent)
	linkedPhasePath := filepath.Join(linkedRunDir, "artifacts", "benchmark", "phases.tsv")
	writeArtifactFile(t, linkedPhasePath, phaseContent)
	timeline, err := benchmarkphase.ParseTSV(strings.NewReader(phaseContent), 1, filepath.Base(linkedRunDir))
	if err != nil {
		t.Fatal(err)
	}

	controlDir := filepath.Join(linkedRunDir, "artifacts", "benchmark", "controls")
	writeArtifactFile(t, filepath.Join(controlDir, benchmarkcontrol.CacheStateSourceFile), "relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\n")
	writeArtifactFile(t, filepath.Join(controlDir, benchmarkcontrol.StatisticsResetSourceFile), "record\tscope\tvalue\trows\tcommand_completed\n")
	writeArtifactFile(t, filepath.Join(controlDir, benchmarkcontrol.CollectorOverheadSourceFile), "sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus\n")
	writeArtifactFile(t, filepath.Join(controlDir, benchmarkcontrol.ResourceBudgetSourceFile), "{\n  \"mode\": \"unbounded\"\n}\n")
	materializeRunDir, err := filepath.EvalSymlinks(linkedRunDir)
	if err != nil {
		t.Fatal(err)
	}
	controls, err := benchmarkrun.MaterializeControlsV2(materializeRunDir, plan, filepath.Base(linkedRunDir), 1, timeline, "17")
	if err != nil {
		t.Fatalf("materialize protocol-v2 fixture controls: %v", err)
	}

	var series benchmarkrun.Series
	readArtifactJSON(t, filepath.Join(seriesDir, "result.json"), &series)
	trial := series.Trials[0]
	trial.PhaseTimeline = &timeline
	trial.PhaseJournal = artifactFixtureRef(t, linkedRunDir, linkedPhasePath)
	trial.Controls = controls
	measure, ok := benchmarkphase.EventByName(timeline, benchmarkphase.MeasureName)
	if !ok {
		t.Fatal("protocol-v2 fixture phase timeline has no measure")
	}
	metricsOptions, err := artifactMetricsOptions(linkedRunDir, 1, trial, &plan, "17", measure)
	if err != nil {
		t.Fatalf("load protocol-v2 fixture metric controls: %v", err)
	}
	postgresMetrics, err := benchmarkmetrics.DeriveFile(metricsOptions)
	if err != nil {
		t.Fatalf("derive protocol-v2 fixture metrics: %v", err)
	}
	trial.PostgresMetrics = &postgresMetrics

	writeVerifiedLinkedRunForPlan(t, linkedRunDir, trial.RunID, "docker", plan)
	series.SpecDigest = plan.SpecDigest
	series.ProtocolDigest = plan.ProtocolDigest
	series.ComparisonKeyDigest = plan.ComparisonKeyDigest
	series.Trials[0] = trial
	writeArtifactJSON(t, filepath.Join(seriesDir, "trials", "001.json"), trial)
	writeArtifactJSON(t, filepath.Join(seriesDir, "result.json"), series)
	writeArtifactFile(t, filepath.Join(seriesDir, "summary.md"), string(benchmarkrun.SummaryBytes(series)))
}

func writeArtifactCopy(t *testing.T, source string, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	writeArtifactFile(t, destination, string(content))
}

func artifactPgbenchLog(count int64, clients int64) string {
	var output strings.Builder
	for index := int64(0); index < count; index++ {
		client := index % clients
		transaction := index/clients + 1
		latency := int64(100)
		if index%2 == 1 {
			latency = 200
		}
		fmt.Fprintf(&output, "%d %d %d 0 1786492800 %d\n", client, transaction, latency, 780000+index)
	}
	return output.String()
}

func writeVerifiedLinkedRun(t *testing.T, runDir, runID, runtimeName, seriesDir string) {
	t.Helper()
	fixturePlan := benchmarkplan.Plan{
		Spec:                     "synthetic",
		SpecDigest:               artifactFixtureDigest(t, filepath.Join(seriesDir, "benchmark-spec.env")),
		Target:                   benchmarkplan.TargetDirectPostgres,
		TargetEndpointContract:   benchmarkplan.EndpointDirectV1,
		TargetTopology:           "single",
		ExperimentSpec:           "benchmarks/pgbench",
		ExperimentDigest:         artifactFixtureDigest(t, filepath.Join(seriesDir, "protocol", "experiment-spec.env")),
		WorkloadSpec:             "pgbench/tiny",
		WorkloadMode:             "builtin",
		PGConfig:                 "default",
		Mode:                     "fixed-time",
		Scale:                    1,
		Clients:                  2,
		Threads:                  1,
		MeasureSeconds:           30,
		ResetPolicy:              "rebuild-per-trial",
		CollectorIntervalSeconds: 1,
		QueryProtocol:            "simple",
		LogTransactions:          true,
		LogSampleRate:            1,
	}
	writeVerifiedLinkedRunForPlan(t, runDir, runID, runtimeName, fixturePlan)
}

func writeVerifiedLinkedRunForPlan(t *testing.T, runDir, runID, runtimeName string, plan benchmarkplan.Plan) {
	t.Helper()
	manifest := runstate.Manifest{
		RunID:                     runID,
		StartedAt:                 "2026-08-12T00:00:00Z",
		ExperimentSpecID:          "benchmarks/pgbench",
		ExperimentSpecRef:         "experiments/benchmarks/pgbench.env",
		ExperimentSpecDigest:      plan.ExperimentDigest,
		SourceSpecKind:            "benchmark",
		SourceSpecID:              plan.Spec,
		SourceSpecRef:             "benchmarks/synthetic.env",
		SourceSpecDigest:          plan.SpecDigest,
		ExecutionParametersDigest: benchmarkrun.ExpectedExecutionParametersDigest(plan, runtimeName, 1),
		Runtime:                   runtimeName,
		EngineVersion:             "0.3.0",
		EngineCommit:              strings.Repeat("a", 40),
		RuntimeFingerprintStatus:  runstate.RuntimeFingerprintObserved,
		RuntimeFingerprintTarget:  "primary",
		RuntimeOS:                 "linux",
		RuntimeArch:               "amd64",
		PostgresServerVersionNum:  "170009",
		PostgresServerMajor:       "17",
		RuntimeFingerprintAt:      "2026-08-12T00:00:00Z",
		ExperimentName:            "Synthetic benchmark trial",
		ExperimentTopology:        "single",
		ExperimentPGConfig:        "default",
		ProfileSize:               "small",
		WorkloadSpec:              "pgbench/tiny",
		MetricsEnabled:            "1",
	}
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	verdict := runstate.Verdict{
		RunID:            runID,
		Status:           runstate.VerdictStatusPassed,
		Message:          "synthetic benchmark trial passed",
		StartedAt:        "2026-08-12T00:00:00Z",
		FinishedAt:       "2026-08-12T00:00:30Z",
		ExperimentSpecID: "benchmarks/pgbench",
		WorkloadExit:     0,
		AssertExit:       0,
		ScanExit:         0,
	}
	if err := runstate.WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}
	writeArtifactFile(t, filepath.Join(runDir, "stdout.log"), "pgworkbench_benchmark_target=direct-postgres endpoint_contract=pgworkbench.pgbench-target/direct-postgres/v1 driver_service=postgres endpoint_host=127.0.0.1 endpoint_port=5432 driver_image_id=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd target_image_id=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\n")
	writeArtifactFile(t, filepath.Join(runDir, "metrics.csv"), string(metricstest.Default()))
}

func artifactFixtureRef(t *testing.T, root, path string) *benchmarkrun.ArtifactRef {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return &benchmarkrun.ArtifactRef{Path: filepath.ToSlash(relative), Digest: digest, Size: info.Size()}
}

func artifactFixtureDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := evidence.DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mutateArtifactMetricsCell(t *testing.T, path string, dataRow int, columnName, value string) {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(mustReadArtifactFile(t, path)))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	column := slices.Index(records[0], columnName)
	if column < 0 || dataRow < 0 || dataRow+1 >= len(records) {
		t.Fatal("invalid metrics fixture mutation")
	}
	records[dataRow+1][column] = value
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.WriteAll(records)
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	writeArtifactFile(t, path, output.String())
}

func mustReadArtifactFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func benchmarkEnvironmentDigest(t *testing.T, environment benchmarkrun.Environment) string {
	t.Helper()
	environment.Digest = ""
	content, err := json.Marshal(environment)
	if err != nil {
		t.Fatal(err)
	}
	return evidence.DigestBytes(content)
}

func writeArtifactJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeArtifactFile(t, path, string(content)+"\n")
}

func readArtifactJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, value); err != nil {
		t.Fatal(err)
	}
}

func writeArtifactFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsArtifactIssue(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return true
		}
	}
	return false
}

func extractBenchmarkBundle(t *testing.T, archivePath string, destination string) {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !evidence.IsPortablePath(name) {
			t.Fatalf("archive contains unsafe path: %s", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			_, copyErr := io.Copy(output, reader)
			closeErr := output.Close()
			if copyErr != nil || closeErr != nil {
				t.Fatalf("extract %s: copy=%v close=%v", header.Name, copyErr, closeErr)
			}
		default:
			t.Fatalf("archive contains unsupported entry type %d", header.Typeflag)
		}
	}
}
