package runverify

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
)

func TestVerifyValidRun(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidRun(t, runDir)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("expected valid result, got: %#v", result.Issues)
	}

	var out bytes.Buffer
	if err := Render(&out, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PASS: run artifact") {
		t.Fatalf("unexpected render output: %s", out.String())
	}
	out.Reset()
	if err := RenderJSON(&out, result); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Dir                     string   `json:"dir"`
		BundleInventoryRequired bool     `json:"bundle_inventory_required"`
		Valid                   bool     `json:"valid"`
		Issues                  []string `json:"issues"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Dir != runDir || payload.BundleInventoryRequired || !payload.Valid || len(payload.Issues) != 0 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestVerifyBundleRequiresInventoryWithoutChangingLiveVerification(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")

	liveResult, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !liveResult.Valid() {
		t.Fatalf("live run without inventory should remain valid: %#v", liveResult.Issues)
	}

	bundleResult, err := VerifyBundle(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	want := "missing " + evidence.BundleInventoryName + ": bundle verification requires a complete inventory"
	if !hasIssue(bundleResult, want) || !bundleResult.BundleInventoryRequired {
		t.Fatalf("missing required-inventory issue %q in %#v", want, bundleResult)
	}

	var out bytes.Buffer
	if err := RenderJSON(&out, bundleResult); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"bundle_inventory_required": true`) {
		t.Fatalf("bundle verification mode missing from JSON: %s", out.String())
	}
}

func TestVerifyBundleRejectsArtifactTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, string)
		issue  string
	}{
		{
			name: "modified artifact",
			tamper: func(t *testing.T, path string) {
				writeFile(t, path, "tampered evidence\n")
			},
			issue: evidence.BundleInventoryName + " digest mismatch for evidence.txt",
		},
		{
			name: "deleted artifact",
			tamper: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			issue: evidence.BundleInventoryName + " missing inventoried file: evidence.txt",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runDir := filepath.Join(root, "runs", "run-a")
			writeValidV1Run(t, runDir, "0")
			writeManifestSpecSnapshot(t, runDir, "EXPERIMENT_NAME=smoke\n")
			artifactPath := filepath.Join(runDir, "evidence.txt")
			writeFile(t, artifactPath, "original evidence\n")
			writeInventory(t, runDir)

			before, err := VerifyBundle(root, "run-a")
			if err != nil {
				t.Fatal(err)
			}
			if !before.Valid() {
				t.Fatalf("complete bundle fixture is invalid: %#v", before.Issues)
			}
			var output bytes.Buffer
			if err := Render(&output, before); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "PASS: complete run bundle") {
				t.Fatalf("bundle verification mode missing from text output: %s", output.String())
			}

			test.tamper(t, artifactPath)
			after, err := VerifyBundle(root, "run-a")
			if err != nil {
				t.Fatal(err)
			}
			if !hasIssue(after, test.issue) {
				t.Fatalf("missing tamper issue %q in %#v", test.issue, after.Issues)
			}
		})
	}
}

func TestVerifyBundleBindsOrdinaryExperimentSpecSnapshot(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	spec := "EXPERIMENT_NAME=smoke\n"
	manifest := runstate.Manifest{
		RunID:                    "run-a",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpec:           "experiments/smoke.env",
		ExperimentSpecID:         "smoke",
		ExperimentSpecDigest:     evidence.DigestBytes([]byte(spec)),
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		ProfileSize:              "small",
		MetricsEnabled:           "0",
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-01-01T00:00:01Z",
	}
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID: "run-a", Status: "passed", Message: "experiment passed",
		StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:00:02Z", ExperimentSpecID: "smoke",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts", "provenance"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), spec)
	writeInventory(t, runDir)

	result, err := VerifyBundle(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("complete ordinary bundle is invalid: %#v", result.Issues)
	}
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), spec+"# tampered\n")
	writeInventory(t, runDir)
	result, err = VerifyBundle(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "experiment spec snapshot digest does not match manifest.env") {
		t.Fatalf("missing ordinary spec snapshot binding failure: %#v", result.Issues)
	}
}

func TestVerifyDetectsMissingFiles(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.env"), []byte("run_id=run-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected invalid result")
	}
	for _, want := range []string{"missing verdict.env", "missing verdict.json", "missing metrics.csv"} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestVerifyDetectsVerdictMismatch(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidRun(t, runDir)
	writeFile(t, filepath.Join(runDir, "verdict.env"), `status=failed
message=experiment passed
finished_at=2026-01-01T00:00:02Z
workload_exit=0
assert_exit=0
scan_exit=0
`)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected invalid result")
	}
	if !hasIssue(result, "verdict.json status does not match verdict.env status") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyDetectsMetricsWithoutSamples(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidRun(t, runDir)
	writeFile(t, filepath.Join(runDir, "metrics.csv"), "sampled_at,database_name\n")

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected invalid result")
	}
	if !hasIssue(result, "metrics.csv has no samples") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestBenchmarkMetricsRequireCanonicalMonotonicTimestamps(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "metrics.csv")
		writeFile(t, path, "sampled_at,value\nnot-a-time,1\n")
		result := Result{}
		if coverage := checkMetrics(&result, path, true, "", true); coverage != nil {
			t.Fatalf("malformed timestamp unexpectedly produced coverage: %#v", coverage)
		}
		if !hasIssue(result, "metrics.csv row 2 sampled_at is not UTC RFC3339") {
			t.Fatalf("unexpected issues: %#v", result.Issues)
		}
	})

	t.Run("non-monotonic", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "metrics.csv")
		writeFile(t, path, strings.Join([]string{
			"sampled_at,value",
			"2026-08-12T00:00:02Z,1",
			"2026-08-12T00:00:01Z,2",
		}, "\n")+"\n")
		result := Result{}
		_ = checkMetrics(&result, path, true, "", true)
		if !hasIssue(result, "metrics.csv sampled_at values are not monotonic") {
			t.Fatalf("unexpected issues: %#v", result.Issues)
		}
	})
}

func TestVerifyAllowsMissingMetricsWhenVersionedManifestDisablesThem(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("disabled metrics run should be valid: %#v", result.Issues)
	}
}

func TestVerifyStillRequiresMetricsWhenVersionedManifestEnablesThem(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "1")

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "missing metrics.csv") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyRequiresDeclaredMetricsSampleCount(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1RunWithMetricsSamples(t, runDir, "1", "2")
	writeFile(t, filepath.Join(runDir, "metrics.csv"), `sampled_at,database_name,wal_bytes
t0,db,100
`)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	want := "metrics.csv sample count does not match manifest.env metrics_samples: got 1, want 2"
	if !hasIssue(result, want) {
		t.Fatalf("missing issue %q in %#v", want, result.Issues)
	}
}

func TestVerifyRequiresMetricsSamplesKeyInVersionedManifest(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifest := strings.Replace(readFile(t, manifestPath), `metrics_samples=""`+"\n", "", 1)
	writeFile(t, manifestPath, manifest)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "manifest.env missing key: metrics_samples") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyAcceptsCompleteUtilitySourceSpecIdentity(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "utility-run")
	writeValidUtilityV1Run(t, runDir)

	result, err := Verify(root, "utility-run")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("complete utility source identity should be valid: %#v", result.Issues)
	}
}

func TestVerifyBundleBindsUtilityProvenanceSnapshots(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "utility-run")
	generated := "EXPERIMENT_NAME='utility smoke'\nEXPERIMENT_CAPTURE_FILES='logs/utility/result.sql'\n"
	source := "UTILITY_TEST_NAME='utility smoke'\n"
	manifest := validUtilityManifest(runDir)
	manifest.ExperimentSpecDigest = evidence.DigestBytes([]byte(generated))
	manifest.SourceSpecDigest = evidence.DigestBytes([]byte(source))
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts", "provenance"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), generated)
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "source-utility-test.env"), source)
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts", "utility", "logs", "utility"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "artifacts", "utility", "logs", "utility", "result.sql"), "select 1;\n")
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	writePassedVerdict(t, runDir, "utility/pg-dump/smoke")
	writeInventory(t, runDir)

	result, err := VerifyBundle(root, "utility-run")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("utility bundle should bind both provenance snapshots: %#v", result.Issues)
	}

	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "source-utility-test.env"), "tampered source spec\n")
	result, err = VerifyBundle(root, "utility-run")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "utility source spec snapshot digest does not match manifest.env") {
		t.Fatalf("missing source snapshot binding failure: %#v", result.Issues)
	}
}

func TestVerifyBundleRequiresEveryDeclaredUtilityOutput(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "utility-run")
	generated := "EXPERIMENT_CAPTURE_FILES='logs/utility/result.sql'\n"
	source := "UTILITY_TEST_NAME='utility smoke'\n"
	manifest := validUtilityManifest(runDir)
	manifest.ExperimentSpecDigest = evidence.DigestBytes([]byte(generated))
	manifest.SourceSpecDigest = evidence.DigestBytes([]byte(source))
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts", "provenance"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), generated)
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "source-utility-test.env"), source)
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	writePassedVerdict(t, runDir, "utility/pg-dump/smoke")
	writeInventory(t, runDir)

	result, err := VerifyBundle(root, "utility-run")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "missing captured utility output logs/utility/result.sql") {
		t.Fatalf("missing declared utility-output failure: %#v", result.Issues)
	}
}

func TestVerifyBindsUtilitySourceDigestToExperimentIdentity(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "utility-run")
	writeValidUtilityV1Run(t, runDir)
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifest := strings.Replace(
		readFile(t, manifestPath),
		`source_spec_digest="sha256:`+strings.Repeat("a", 64)+`"`,
		`source_spec_digest="sha256:`+strings.Repeat("b", 64)+`"`,
		1,
	)
	writeFile(t, manifestPath, manifest)

	result, err := Verify(root, "utility-run")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "manifest.env experiment_identity_digest does not match resolved experiment identity") {
		t.Fatalf("source digest was not bound to experiment identity: %#v", result.Issues)
	}
}

func TestVerifyRejectsPartialOrIncoherentUtilitySourceSpecIdentity(t *testing.T) {
	tests := []struct {
		name  string
		build func(string) runstate.Manifest
		issue string
	}{
		{
			name: "partial tuple",
			build: func(runDir string) runstate.Manifest {
				manifest := validUtilityManifest(runDir)
				manifest.SourceSpecRef = ""
				return manifest
			},
			issue: "manifest.env missing key for source spec identity: source_spec_ref",
		},
		{
			name: "source ref mismatch",
			build: func(runDir string) runstate.Manifest {
				manifest := validUtilityManifest(runDir)
				manifest.SourceSpecRef = "utility-tests/pg-dump/other.env"
				return manifest
			},
			issue: "manifest.env source_spec_ref must match source_spec_id: want utility-tests/pg-dump/smoke.env, got utility-tests/pg-dump/other.env",
		},
		{
			name: "derived experiment id mismatch",
			build: func(runDir string) runstate.Manifest {
				manifest := validUtilityManifest(runDir)
				manifest.ExperimentSpecID = "utility/wrong"
				return manifest
			},
			issue: "manifest.env experiment_spec_id must match utility source identity: want utility/pg-dump/smoke, got utility/wrong",
		},
		{
			name: "generated spec ref escape",
			build: func(runDir string) runstate.Manifest {
				manifest := validUtilityManifest(runDir)
				manifest.ExperimentSpecRef = "experiments/smoke.env"
				return manifest
			},
			issue: "manifest.env experiment_spec_ref must identify a generated utility spec under .tmp/utility-tests: experiments/smoke.env",
		},
		{
			name: "pack identity conflict",
			build: func(runDir string) runstate.Manifest {
				manifest := validUtilityManifest(runDir)
				manifest.PackID = "builtin"
				manifest.PackVersion = "0.2.0"
				manifest.PackDigest = "sha256:" + strings.Repeat("b", 64)
				return manifest
			},
			issue: "manifest.env pack_id must be empty for a derived utility-test run",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runDir := filepath.Join(root, "runs", "utility-run")
			if err := runstate.WriteManifest(runDir, test.build(runDir)); err != nil {
				t.Fatal(err)
			}
			writePassedVerdict(t, runDir, "utility/pg-dump/smoke")
			result, err := Verify(root, "utility-run")
			if err != nil {
				t.Fatal(err)
			}
			if !hasIssue(result, test.issue) {
				t.Fatalf("missing issue %q in %#v", test.issue, result.Issues)
			}
		})
	}
}

func TestVerifyRejectsSymlinkedEvidenceFiles(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		issue string
	}{
		{name: "manifest", file: "manifest.env", issue: "manifest.env must not be a symlink"},
		{name: "verdict env", file: "verdict.env", issue: "verdict.env must not be a symlink"},
		{name: "verdict json", file: "verdict.json", issue: "verdict.json must not be a symlink"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runDir := filepath.Join(root, "runs", "run-a")
			writeValidV1Run(t, runDir, "0")
			target := filepath.Join(runDir, test.file)
			outside := filepath.Join(t.TempDir(), test.file)
			writeFile(t, outside, readFile(t, target))
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}

			result, err := Verify(root, "run-a")
			if err != nil {
				t.Fatal(err)
			}
			if !hasIssue(result, test.issue) {
				t.Fatalf("missing issue %q in %#v", test.issue, result.Issues)
			}
		})
	}
}

func TestVerifyRejectsSymlinkedMetricsAndBundleInventory(t *testing.T) {
	t.Run("metrics", func(t *testing.T) {
		root := t.TempDir()
		runDir := filepath.Join(root, "runs", "run-a")
		writeValidV1Run(t, runDir, "1")
		outside := filepath.Join(t.TempDir(), "metrics.csv")
		writeFile(t, outside, "sampled_at,database_name,wal_bytes\nt0,db,100\n")
		if err := os.Symlink(outside, filepath.Join(runDir, "metrics.csv")); err != nil {
			t.Fatal(err)
		}
		result, err := Verify(root, "run-a")
		if err != nil {
			t.Fatal(err)
		}
		if !hasIssue(result, "metrics.csv must not be a symlink") {
			t.Fatalf("unexpected issues: %#v", result.Issues)
		}
	})

	t.Run("bundle inventory", func(t *testing.T) {
		root := t.TempDir()
		runDir := filepath.Join(root, "runs", "run-a")
		writeValidV1Run(t, runDir, "0")
		outside := filepath.Join(t.TempDir(), "inventory.json")
		writeFile(t, outside, "{}\n")
		if err := os.Symlink(outside, filepath.Join(runDir, evidence.BundleInventoryName)); err != nil {
			t.Fatal(err)
		}
		result, err := Verify(root, "run-a")
		if err != nil {
			t.Fatal(err)
		}
		want := evidence.BundleInventoryName + " must not be a symlink"
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	})
}

func TestVerifyAllowsUnavailableFingerprintForEarlyFailedRun(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeFailedV1Run(t, runDir)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("early failed run should allow unavailable fingerprint: %#v", result.Issues)
	}
}

func TestVerifyAllowsMissingEnabledMetricsForEarlyFailedRun(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeFailedV1RunWithMetrics(t, runDir, "1")

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("early failed run invented a mandatory sampler output: %#v", result.Issues)
	}
}

func TestVerifyPassedVersionedRunRequiresObservedFingerprint(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeFailedV1Run(t, runDir)
	verdictEnvPath := filepath.Join(runDir, "verdict.env")
	writeFile(t, verdictEnvPath, strings.Replace(readFile(t, verdictEnvPath), `status="failed"`, `status="passed"`, 1))
	verdictJSONPath := filepath.Join(runDir, "verdict.json")
	writeFile(t, verdictJSONPath, strings.Replace(readFile(t, verdictJSONPath), `"status": "failed"`, `"status": "passed"`, 1))

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "passed versioned run requires an observed runtime fingerprint") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyRejectsUnknownVerdictStatus(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")

	verdictEnvPath := filepath.Join(runDir, "verdict.env")
	writeFile(t, verdictEnvPath, strings.Replace(readFile(t, verdictEnvPath), `status="passed"`, `status="inconclusive"`, 1))
	verdictJSONPath := filepath.Join(runDir, "verdict.json")
	writeFile(t, verdictJSONPath, strings.Replace(readFile(t, verdictJSONPath), `"status": "passed"`, `"status": "inconclusive"`, 1))

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`verdict.env verdict status must be passed or failed, got "inconclusive"`,
		`verdict.json verdict status must be passed or failed, got "inconclusive"`,
	} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestVerifyRejectsPassedVerdictWithNonzeroExit(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")

	verdictEnvPath := filepath.Join(runDir, "verdict.env")
	writeFile(t, verdictEnvPath, strings.Replace(readFile(t, verdictEnvPath), `workload_exit="0"`, `workload_exit="7"`, 1))
	verdictJSONPath := filepath.Join(runDir, "verdict.json")
	writeFile(t, verdictJSONPath, strings.Replace(readFile(t, verdictJSONPath), `"workload_exit": 0`, `"workload_exit": 7`, 1))

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"verdict.env passed verdict requires zero exit codes: workload_exit=7 assert_exit=0 scan_exit=0",
		"verdict.json passed verdict requires zero exit codes: workload_exit=7 assert_exit=0 scan_exit=0",
	} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestVerifyRejectsFailedVerdictWithZeroExits(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")

	verdictEnvPath := filepath.Join(runDir, "verdict.env")
	writeFile(t, verdictEnvPath, strings.Replace(readFile(t, verdictEnvPath), `status="passed"`, `status="failed"`, 1))
	verdictJSONPath := filepath.Join(runDir, "verdict.json")
	writeFile(t, verdictJSONPath, strings.Replace(readFile(t, verdictJSONPath), `"status": "passed"`, `"status": "failed"`, 1))

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"verdict.env failed verdict requires at least one nonzero exit code",
		"verdict.json failed verdict requires at least one nonzero exit code",
	} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestVerifyDetectsRuntimeFingerprintVersionDrift(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifest := strings.Replace(readFile(t, manifestPath), `postgres_server_major="16"`, `postgres_server_major="17"`, 1)
	writeFile(t, manifestPath, manifest)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "manifest.env postgres_server_major does not match postgres_server_version_num") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyRejectsNonCanonicalEngineIdentity(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifest := readFile(t, manifestPath)
	manifest = strings.Replace(manifest, `engine_version="unverified"`, `engine_version="dev"`, 1)
	manifest = strings.Replace(manifest, `engine_commit="unverified"`, `engine_commit="0123456"`, 1)
	writeFile(t, manifestPath, manifest)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"manifest.env engine_version must be canonical SemVer or unverified, got dev",
		"manifest.env engine_commit must be a full lowercase Git object ID or unverified, got 0123456",
	} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestVerifyRejectsFingerprintTargetThatDoesNotMatchTopology(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	if err := runstate.WriteManifest(runDir, runstate.Manifest{
		RunID:                    "run-a",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpecID:         "multi-version-upgrade-smoke",
		ExperimentTopology:       "multi-version-upgrade",
		ExperimentPGConfig:       "default",
		ProfileSize:              "small",
		MetricsEnabled:           "0",
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeFingerprintTarget: "primary",
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-01-01T00:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID:            "run-a",
		Status:           "passed",
		Message:          "experiment passed",
		StartedAt:        "2026-01-01T00:00:00Z",
		FinishedAt:       "2026-01-01T00:00:02Z",
		ExperimentSpecID: "multi-version-upgrade-smoke",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "manifest.env runtime_fingerprint_target must be upgrade-new for topology multi-version-upgrade, got primary") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyVersionedArtifactAfterRelocation(t *testing.T) {
	originalRoot := t.TempDir()
	originalRunDir := filepath.Join(originalRoot, "runs", "run-a")
	writeValidV1Run(t, originalRunDir, "0")

	movedRoot := t.TempDir()
	movedRunDir := filepath.Join(movedRoot, "portable", "run-a")
	if err := os.MkdirAll(filepath.Dir(movedRunDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(originalRunDir, movedRunDir); err != nil {
		t.Fatal(err)
	}
	result, err := Verify(movedRoot, movedRunDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("relocated artifact should remain valid: %#v", result.Issues)
	}
}

func TestVerifyLegacyArtifactAfterRelocation(t *testing.T) {
	originalRoot := t.TempDir()
	originalRunDir := filepath.Join(originalRoot, "runs", "run-a")
	writeValidRun(t, originalRunDir)

	movedRoot := t.TempDir()
	movedRunDir := filepath.Join(movedRoot, "imported", "run-a")
	if err := os.MkdirAll(filepath.Dir(movedRunDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(originalRunDir, movedRunDir); err != nil {
		t.Fatal(err)
	}
	result, err := Verify(movedRoot, movedRunDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("relocated legacy artifact should remain valid: %#v", result.Issues)
	}
}

func TestVerifyDetectsResolvedExperimentIdentityDrift(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifest := readFile(t, manifestPath)
	manifest = strings.Replace(manifest, `workload_spec="sql/smoke-run"`, `workload_spec="sql/changed"`, 1)
	writeFile(t, manifestPath, manifest)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"manifest.env experiment_identity_digest does not match resolved experiment identity",
		"verdict.env manifest_digest does not match manifest.env",
		"verdict.json manifest_digest does not match manifest.env",
	} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestVerifyDetectsCrossArtifactRunIdentityDrift(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")
	verdictPath := filepath.Join(runDir, "verdict.json")
	verdict := readFile(t, verdictPath)
	verdict = strings.Replace(verdict, `"run_id": "run-a"`, `"run_id": "run-b"`, 1)
	writeFile(t, verdictPath, verdict)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "verdict.json run_id does not match manifest.env run_id") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyChecksOptionalBundleInventory(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")
	writeInventory(t, runDir)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("inventoried artifact should be valid: %#v", result.Issues)
	}
	verdictEnvPath := filepath.Join(runDir, "verdict.env")
	writeFile(t, verdictEnvPath, readFile(t, verdictEnvPath)+"# post-bundle mutation\n")
	result, err = Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	want := evidence.BundleInventoryName + " digest mismatch for verdict.env"
	if !hasIssue(result, want) {
		t.Fatalf("missing issue %q in %#v", want, result.Issues)
	}
}

func TestVerifyRejectsUnknownVersionedSchema(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidV1Run(t, runDir, "0")
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifest := strings.Replace(readFile(t, manifestPath), `schema_version="`+runstate.ManifestSchemaVersion+`"`, `schema_version="pgworkbench.run-manifest/v2"`, 1)
	writeFile(t, manifestPath, manifest)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(result, "manifest.env unsupported schema_version: pgworkbench.run-manifest/v2") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func writeValidRun(t *testing.T, runDir string) {
	t.Helper()
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "manifest.env"), `run_id=run-a
started_at=2026-01-01T00:00:00Z
experiment_spec=experiments/smoke.env
experiment_spec_id=smoke
experiment_name=smoke experiment
experiment_topology=single
experiment_pg_config=default
profile=smoke
dataset_spec=
profile_size=small
workload_spec=sql/smoke-run
background_specs=
run_dir=`+runDir+`
`)
	writeFile(t, filepath.Join(runDir, "verdict.env"), `status=passed
message=experiment passed
finished_at=2026-01-01T00:00:02Z
workload_exit=0
assert_exit=0
scan_exit=0
`)
	writeFile(t, filepath.Join(runDir, "verdict.json"), `{
  "run_id": "run-a",
  "status": "passed",
  "message": "experiment passed",
  "started_at": "2026-01-01T00:00:00Z",
  "finished_at": "2026-01-01T00:00:02Z",
  "experiment_spec": "smoke",
  "run_dir": "`+runDir+`",
  "workload_exit": 0,
  "assert_exit": 0,
  "scan_exit": 0
}
`)
	writeFile(t, filepath.Join(runDir, "metrics.csv"), `sampled_at,database_name,wal_bytes
t0,db,100
`)
}

func writeValidV1Run(t *testing.T, runDir string, metricsEnabled string) {
	t.Helper()
	writeValidV1RunWithMetricsSamples(t, runDir, metricsEnabled, "")
}

func writeValidV1RunWithMetricsSamples(t *testing.T, runDir string, metricsEnabled string, metricsSamples string) {
	t.Helper()
	if err := runstate.WriteManifest(runDir, runstate.Manifest{
		RunID:                    "run-a",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpec:           "experiments/smoke.env",
		ExperimentSpecID:         "smoke",
		ExperimentName:           "smoke experiment",
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		Profile:                  "smoke",
		ProfileSize:              "small",
		WorkloadSpec:             "sql/smoke-run",
		MetricsEnabled:           metricsEnabled,
		MetricsSamples:           metricsSamples,
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-01-01T00:00:01Z",
		RunDir:                   runDir,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID:            "run-a",
		Status:           "passed",
		Message:          "experiment passed",
		StartedAt:        "2026-01-01T00:00:00Z",
		FinishedAt:       "2026-01-01T00:00:02Z",
		ExperimentSpecID: "smoke",
		RunDir:           runDir,
	}); err != nil {
		t.Fatal(err)
	}
}

func writeManifestSpecSnapshot(t *testing.T, runDir string, content string) {
	t.Helper()
	manifestPath := filepath.Join(runDir, "manifest.env")
	manifest := readFile(t, manifestPath)
	manifest = strings.Replace(manifest, `experiment_spec_digest=""`, `experiment_spec_digest="`+evidence.DigestBytes([]byte(content))+`"`, 1)
	writeFile(t, manifestPath, manifest)
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts", "provenance"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), content)
	writePassedVerdictForRun(t, runDir, "run-a", "smoke")
}

func writePassedVerdictForRun(t *testing.T, runDir string, runID string, experimentSpecID string) {
	t.Helper()
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID: runID, Status: "passed", Message: "experiment passed",
		StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:00:02Z", ExperimentSpecID: experimentSpecID,
	}); err != nil {
		t.Fatal(err)
	}
}

func validUtilityManifest(runDir string) runstate.Manifest {
	return runstate.Manifest{
		RunID:                    "utility-run",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpec:           ".tmp/utility-tests/utility-run.env",
		ExperimentSpecID:         "utility/pg-dump/smoke",
		ExperimentSpecRef:        ".tmp/utility-tests/utility-run.env",
		SourceSpecKind:           "utility-test",
		SourceSpecID:             "pg-dump/smoke",
		SourceSpecRef:            "utility-tests/pg-dump/smoke.env",
		SourceSpecDigest:         "sha256:" + strings.Repeat("a", 64),
		ExperimentName:           "pg_dump smoke",
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		ProfileSize:              "small",
		MetricsEnabled:           "0",
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-01-01T00:00:01Z",
		RunDir:                   runDir,
	}
}

func writeValidUtilityV1Run(t *testing.T, runDir string) {
	t.Helper()
	if err := runstate.WriteManifest(runDir, validUtilityManifest(runDir)); err != nil {
		t.Fatal(err)
	}
	writePassedVerdict(t, runDir, "utility/pg-dump/smoke")
}

func writePassedVerdict(t *testing.T, runDir string, experimentSpecID string) {
	t.Helper()
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID:            "utility-run",
		Status:           "passed",
		Message:          "experiment passed",
		StartedAt:        "2026-01-01T00:00:00Z",
		FinishedAt:       "2026-01-01T00:00:02Z",
		ExperimentSpecID: experimentSpecID,
		RunDir:           runDir,
	}); err != nil {
		t.Fatal(err)
	}
}

func writeFailedV1Run(t *testing.T, runDir string) {
	t.Helper()
	writeFailedV1RunWithMetrics(t, runDir, "0")
}

func writeFailedV1RunWithMetrics(t *testing.T, runDir string, metricsEnabled string) {
	t.Helper()
	if err := runstate.WriteManifest(runDir, runstate.Manifest{
		RunID:                    "run-a",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpec:           "experiments/smoke.env",
		ExperimentSpecID:         "smoke",
		ExperimentName:           "smoke experiment",
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		ProfileSize:              "small",
		MetricsEnabled:           metricsEnabled,
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintUnavailable,
		RunDir:                   runDir,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID:            "run-a",
		Status:           "failed",
		Message:          "runtime start failed",
		StartedAt:        "2026-01-01T00:00:00Z",
		FinishedAt:       "2026-01-01T00:00:02Z",
		ExperimentSpecID: "smoke",
		WorkloadExit:     1,
	}); err != nil {
		t.Fatal(err)
	}
}

func writeInventory(t *testing.T, runDir string) {
	t.Helper()
	var files []evidence.BundleFile
	if err := filepath.WalkDir(runDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(runDir, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == evidence.BundleInventoryName || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		digest, err := evidence.DigestFile(filePath)
		if err != nil {
			return err
		}
		files = append(files, evidence.BundleFile{Path: rel, Size: info.Size(), Digest: digest})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Slice(files, func(i int, j int) bool { return files[i].Path < files[j].Path })
	manifest, err := runartifact.LoadOptionalEnv(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := evidence.MarshalBundleInventory(evidence.NewBundleInventory(manifest.Value("run_id", filepath.Base(runDir)), files))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, evidence.BundleInventoryName), string(content))
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func hasIssue(result Result, issue string) bool {
	for _, candidate := range result.Issues {
		if candidate == issue {
			return true
		}
	}
	return false
}
