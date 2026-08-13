package pgdrillbridge

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
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
)

func TestCreateAndVerifyOrdinaryExperimentBaseline(t *testing.T) {
	root := t.TempDir()
	runDir := writeBridgeRun(t, root, bridgeRunOptions{})
	output := filepath.Join(root, "exports", DefaultFileName)

	artifact, err := Create(root, runDir, output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SourceVerification.Mode != VerificationModeRun || artifact.SourceVerification.BundleInventory != nil {
		t.Fatalf("unexpected source verification: %#v", artifact.SourceVerification)
	}
	if artifact.Predicate != nil {
		t.Fatalf("default export unexpectedly has a predicate: %#v", artifact.Predicate)
	}
	if artifact.ScenarioPack.ID != "fixture-pack" || artifact.ScenarioPack.Version != "1.2.3" || !evidence.IsDigest(artifact.ScenarioPack.Digest) {
		t.Fatalf("scenario pack identity was not exported: %#v", artifact.ScenarioPack)
	}
	if artifact.ExperimentSpec.ID != "smoke" || artifact.ExperimentSpec.Ref != "experiments/smoke.env" || !evidence.IsDigest(artifact.ExperimentSpec.Digest) {
		t.Fatalf("experiment spec identity was not exported: %#v", artifact.ExperimentSpec)
	}
	if artifact.Postgres.Runtime != "native" || artifact.Postgres.ServerVersionNum != "170004" || artifact.Postgres.ServerMajor != "17" {
		t.Fatalf("runtime identity was not exported: %#v", artifact.Postgres)
	}
	if artifact.AssuranceBoundary.Scope != ProvenanceScope || artifact.AssuranceBoundary.Authenticity != Authenticity {
		t.Fatalf("assurance boundary widened: %#v", artifact.AssuranceBoundary)
	}
	content := mustRead(t, output)
	if strings.Contains(string(content), root) || strings.Contains(string(content), runDir) {
		t.Fatalf("artifact serialized a mutable absolute source path: %s", content)
	}
	for _, forbidden := range []string{"credential", "password", "shell", "backup", "restore", "rto", "rpo", "provider_config"} {
		if strings.Contains(strings.ToLower(string(content)), forbidden) {
			t.Fatalf("artifact serialized forbidden field or claim %q: %s", forbidden, content)
		}
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("published artifact is writable: mode=%o", info.Mode().Perm())
	}

	verification, err := Verify(output)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.IsValid() || !verification.Valid {
		t.Fatalf("valid baseline rejected: %v", verification.Issues)
	}
	withSource, err := VerifyAgainstSource(root, output, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !withSource.IsValid() {
		t.Fatalf("valid baseline/source binding rejected: %v", withSource.Issues)
	}
}

func TestCreateBundleBaselineWithReviewedPredicate(t *testing.T) {
	root := t.TempDir()
	runDir := writeBridgeRun(t, root, bridgeRunOptions{Bundle: true})
	output := filepath.Join(root, "exports", "baseline.json")
	predicate := "SELECT count(*) = 10 FROM public.items"

	artifact, err := Create(root, runDir, output, Options{RequireBundle: true, ReviewedPredicateSQL: predicate})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SourceVerification.Mode != VerificationModeBundle || artifact.SourceVerification.BundleInventory == nil {
		t.Fatalf("complete bundle identity is missing: %#v", artifact.SourceVerification)
	}
	if artifact.Predicate == nil || artifact.Predicate.SQL != predicate || artifact.Predicate.ExpectedBoolean != true || artifact.Predicate.ReviewStatus != PredicateReviewStatus {
		t.Fatalf("reviewed predicate is missing or widened: %#v", artifact.Predicate)
	}
	if artifact.Predicate.Digest != evidence.DigestBytes([]byte(predicate)) {
		t.Fatal("predicate digest does not bind exact reviewed SQL bytes")
	}
	verification, err := VerifyAgainstSource(root, output, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.IsValid() {
		t.Fatalf("bundle baseline did not re-verify: %v", verification.Issues)
	}
}

func TestCreateRejectsIneligibleSourceKindsAndOutcomes(t *testing.T) {
	t.Run("benchmark is not an experiment baseline", func(t *testing.T) {
		root := t.TempDir()
		runDir := writeBridgeRun(t, root, bridgeRunOptions{Bundle: true, SourceKind: "benchmark"})
		_, err := Create(root, runDir, filepath.Join(root, "baseline.json"), Options{})
		if err == nil || !strings.Contains(err.Error(), "ordinary experiment run") {
			t.Fatalf("benchmark run was accepted: %v", err)
		}
	})

	t.Run("failed experiment", func(t *testing.T) {
		root := t.TempDir()
		runDir := writeBridgeRun(t, root, bridgeRunOptions{Failed: true})
		_, err := Create(root, runDir, filepath.Join(root, "baseline.json"), Options{})
		if err == nil || !strings.Contains(err.Error(), "versioned passed verdict") {
			t.Fatalf("failed run was accepted: %v", err)
		}
	})

	t.Run("unversioned pack identity", func(t *testing.T) {
		root := t.TempDir()
		runDir := writeBridgeRun(t, root, bridgeRunOptions{NoPack: true})
		_, err := Create(root, runDir, filepath.Join(root, "baseline.json"), Options{})
		if err == nil || !strings.Contains(err.Error(), "scenario_pack") {
			t.Fatalf("missing pack identity was accepted: %v", err)
		}
	})

	t.Run("bundle explicitly required", func(t *testing.T) {
		root := t.TempDir()
		runDir := writeBridgeRun(t, root, bridgeRunOptions{})
		_, err := Create(root, runDir, filepath.Join(root, "baseline.json"), Options{RequireBundle: true})
		if err == nil || !strings.Contains(err.Error(), "complete run bundle is required") {
			t.Fatalf("live run escaped explicit bundle requirement: %v", err)
		}
	})
}

func TestVerifyRejectsTamperingAndStrictSchemaViolations(t *testing.T) {
	tests := []struct {
		name string
		edit func(*testing.T, string)
		want string
	}{
		{
			name: "content without digest update",
			edit: func(t *testing.T, path string) {
				artifact := readArtifact(t, path)
				artifact.Run.ID = "changed-run"
				writeArtifact(t, path, artifact)
			},
			want: "digest does not match",
		},
		{
			name: "unknown field",
			edit: func(t *testing.T, path string) {
				content := mustRead(t, path)
				writeBytes(t, path, []byte(strings.Replace(string(content), "{", "{\n  \"recovery_passed\": true,", 1)))
			},
			want: "unknown field",
		},
		{
			name: "duplicate field",
			edit: func(t *testing.T, path string) {
				content := mustRead(t, path)
				writeBytes(t, path, []byte(strings.Replace(string(content), "{", "{\n  \"schema_version\": \"pgworkbench.pgdrill-baseline/v1\",", 1)))
			},
			want: "duplicate object key",
		},
		{
			name: "redigested recovery-like claim",
			edit: func(t *testing.T, path string) {
				artifact := readArtifact(t, path)
				artifact.Classification = "recovery-proof"
				artifact.Digest = mustArtifactDigest(t, artifact)
				writeArtifact(t, path, artifact)
			},
			want: "classification must be",
		},
		{
			name: "redigested false predicate",
			edit: func(t *testing.T, path string) {
				artifact := readArtifact(t, path)
				artifact.Predicate = &ReviewedPredicate{
					ReviewStatus: PredicateReviewStatus, Language: PredicateLanguage, Kind: PredicateKind,
					SQL: "SELECT true", Digest: evidence.DigestBytes([]byte("SELECT true")), ExpectedBoolean: false,
					Execution: PredicateExecution, SafetyBasis: PredicateSafetyBasis,
				}
				artifact.Digest = mustArtifactDigest(t, artifact)
				writeArtifact(t, path, artifact)
			},
			want: "expected_boolean must be true",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			runDir := writeBridgeRun(t, root, bridgeRunOptions{})
			output := filepath.Join(root, "baseline.json")
			if _, err := Create(root, runDir, output, Options{}); err != nil {
				t.Fatal(err)
			}
			test.edit(t, output)
			verification, err := Verify(output)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !issuesContain(verification.Issues, test.want) {
				t.Fatalf("tamper passed or omitted %q: %v", test.want, verification.Issues)
			}
		})
	}
}

func TestVerifyAgainstSourceDetectsRedigestedIdentityAndSourceTampering(t *testing.T) {
	t.Run("redigested baseline identity", func(t *testing.T) {
		root := t.TempDir()
		runDir := writeBridgeRun(t, root, bridgeRunOptions{})
		output := filepath.Join(root, "baseline.json")
		if _, err := Create(root, runDir, output, Options{}); err != nil {
			t.Fatal(err)
		}
		artifact := readArtifact(t, output)
		artifact.ScenarioPack.Digest = evidence.DigestBytes([]byte("attacker-selected-pack"))
		artifact.Digest = mustArtifactDigest(t, artifact)
		writeArtifact(t, output, artifact)

		standalone, err := Verify(output)
		if err != nil {
			t.Fatal(err)
		}
		if !standalone.IsValid() {
			t.Fatalf("a self-consistent unsigned identity should be structurally valid: %v", standalone.Issues)
		}
		bound, err := VerifyAgainstSource(root, output, runDir)
		if err != nil {
			t.Fatal(err)
		}
		if bound.IsValid() || !issuesContain(bound.Issues, "does not match independently re-derived") {
			t.Fatalf("source-bound verification missed redigested identity: %v", bound.Issues)
		}
	})

	t.Run("source changed after export", func(t *testing.T) {
		root := t.TempDir()
		runDir := writeBridgeRun(t, root, bridgeRunOptions{})
		output := filepath.Join(root, "baseline.json")
		if _, err := Create(root, runDir, output, Options{}); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(runDir, "manifest.env")
		writeBytes(t, manifestPath, append(mustRead(t, manifestPath), []byte("# tampered\n")...))
		bound, err := VerifyAgainstSource(root, output, runDir)
		if err != nil {
			t.Fatal(err)
		}
		if bound.IsValid() || !issuesContain(bound.Issues, "source re-verification failed") {
			t.Fatalf("source tampering was missed: %v", bound.Issues)
		}
	})
}

func TestBundleBaselineSurvivesSourceRelocation(t *testing.T) {
	originalRoot := t.TempDir()
	originalRun := writeBridgeRun(t, originalRoot, bridgeRunOptions{Bundle: true})
	output := filepath.Join(originalRoot, "baseline.json")
	if _, err := Create(originalRoot, originalRun, output, Options{RequireBundle: true}); err != nil {
		t.Fatal(err)
	}

	relocatedRoot := t.TempDir()
	relocatedRun := filepath.Join(relocatedRoot, "imported", "renamed-run")
	if err := os.MkdirAll(filepath.Dir(relocatedRun), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(originalRun, relocatedRun); err != nil {
		t.Fatal(err)
	}
	verification, err := VerifyAgainstSource(relocatedRoot, output, relocatedRun)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.IsValid() {
		t.Fatalf("relocated complete bundle lost provenance binding: %v", verification.Issues)
	}
}

func TestCreateIsImmutablePortableAndSymlinkSafe(t *testing.T) {
	root := t.TempDir()
	runDir := writeBridgeRun(t, root, bridgeRunOptions{})
	output := filepath.Join(root, "exports", "baseline.json")
	if _, err := Create(root, runDir, output, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, runDir, output, Options{}); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("immutable output was overwritten: %v", err)
	}
	if _, err := Create(root, runDir, filepath.Join(runDir, "baseline.json"), Options{}); err == nil || !strings.Contains(err.Error(), "must not be inside") {
		t.Fatalf("output inside mutable source was accepted: %v", err)
	}

	relocated := filepath.Join(t.TempDir(), "renamed.json")
	if err := os.WriteFile(relocated, mustRead(t, output), 0o600); err != nil {
		t.Fatal(err)
	}
	verification, err := Verify(relocated)
	if err != nil || !verification.IsValid() {
		t.Fatalf("relocated artifact is invalid: %v %#v", err, verification.Issues)
	}

	link := filepath.Join(t.TempDir(), "baseline-link.json")
	if err := os.Symlink(relocated, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Verify(link); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked baseline was accepted: %v", err)
	}
}

func TestCreateRejectsSymlinkAncestorIntoSourceBeforeAnyWrite(t *testing.T) {
	root := t.TempDir()
	runDir := writeBridgeRun(t, root, bridgeRunOptions{})
	beforeBytes := snapshotBridgeRun(t, runDir)
	beforeVerification, err := runverify.Verify(root, runDir)
	if err != nil || !beforeVerification.Valid() {
		t.Fatalf("source fixture is not valid before attempted export: err=%v issues=%v", err, beforeVerification.Issues)
	}

	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(runDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	output := filepath.Join(alias, "exports", "nested", DefaultFileName)
	if _, err := Create(root, runDir, output, Options{}); err == nil || !strings.Contains(err.Error(), "must not be inside the source run") {
		t.Fatalf("symlink-ancestor output escaped source containment: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(runDir, "exports")); !os.IsNotExist(err) {
		t.Fatalf("rejected export mutated the source run: %v", err)
	}

	afterBytes := snapshotBridgeRun(t, runDir)
	if len(afterBytes) != len(beforeBytes) {
		t.Fatalf("source file population changed: before=%d after=%d", len(beforeBytes), len(afterBytes))
	}
	for path, before := range beforeBytes {
		after, exists := afterBytes[path]
		if !exists || !bytes.Equal(after, before) {
			t.Fatalf("source bytes changed for %s", path)
		}
	}
	afterVerification, err := runverify.Verify(root, runDir)
	if err != nil || !afterVerification.Valid() {
		t.Fatalf("source verification changed after rejected export: err=%v issues=%v", err, afterVerification.Issues)
	}
}

func TestReviewedPredicateFailsClosed(t *testing.T) {
	valid := []string{
		"SELECT true",
		"SELECT count(*) = 42 FROM public.items",
		"SELECT bool_and(state = 'ready') FROM public.baseline_state",
	}
	for _, sql := range valid {
		if _, err := newReviewedPredicate(sql); err != nil {
			t.Fatalf("valid reviewed predicate %q rejected: %v", sql, err)
		}
	}
	invalid := []string{
		"UPDATE items SET value = 1",
		"SELECT true; DROP TABLE items",
		"SELECT pg_read_file('/etc/passwd') IS NOT NULL",
		"SELECT nextval('items_id_seq') > 0",
		"SELECT 'postgres://user:password@host/db' IS NULL",
		"SELECT secret FROM app_config",
		"SELECT true -- trust me",
		"SELECT set_config('work_mem', '1GB', false) IS NOT NULL",
	}
	for _, sql := range invalid {
		if _, err := newReviewedPredicate(sql); err == nil {
			t.Fatalf("unsafe or credential-like predicate %q was accepted", sql)
		}
	}
}

func TestReviewedPredicateFileDoesNotFollowSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "predicate.sql")
	if err := os.WriteFile(target, []byte("SELECT true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReviewedPredicateSQL(target)
	if err != nil || loaded != "SELECT true" {
		t.Fatalf("regular reviewed predicate did not load: sql=%q err=%v", loaded, err)
	}
	link := filepath.Join(directory, "predicate-link.sql")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := LoadReviewedPredicateSQL(link); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked reviewed predicate was accepted: %v", err)
	}
}

type bridgeRunOptions struct {
	Bundle     bool
	Failed     bool
	NoPack     bool
	SourceKind string
}

func writeBridgeRun(t *testing.T, root string, options bridgeRunOptions) string {
	t.Helper()
	runID := "bridge-run"
	runDir := filepath.Join(root, "runs", runID)
	spec := []byte("EXPERIMENT_NAME=smoke\n")
	manifest := runstate.Manifest{
		RunID:                    runID,
		StartedAt:                "2026-08-12T08:00:00Z",
		ExperimentSpec:           "experiments/smoke.env",
		ExperimentSpecID:         "smoke",
		ExperimentSpecRef:        "experiments/smoke.env",
		ExperimentSpecDigest:     evidence.DigestBytes(spec),
		ExperimentName:           "bridge smoke",
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		ProfileSize:              "small",
		WorkloadSpec:             "sql/smoke-run",
		MetricsEnabled:           "0",
		Runtime:                  "native",
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeFingerprintTarget: "primary",
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "170004",
		PostgresServerMajor:      "17",
		RuntimeFingerprintAt:     "2026-08-12T08:00:01Z",
		RunDir:                   runDir,
	}
	if !options.NoPack {
		manifest.PackID = "fixture-pack"
		manifest.PackVersion = "1.2.3"
		manifest.PackDigest = evidence.DigestBytes([]byte("fixture-pack@1.2.3"))
	}
	if options.SourceKind == "benchmark" {
		source := []byte("BENCHMARK_ID=bridge-benchmark\n")
		manifest.SourceSpecKind = "benchmark"
		manifest.SourceSpecID = "bridge-benchmark"
		manifest.SourceSpecRef = "benchmarks/bridge-benchmark.env"
		manifest.SourceSpecDigest = evidence.DigestBytes(source)
		writeTestFile(t, filepath.Join(runDir, "artifacts", "provenance", "source-benchmark.env"), source)
	}
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), spec)
	verdict := runstate.Verdict{
		RunID: runID, Status: runstate.VerdictStatusPassed, Message: "experiment passed",
		StartedAt: manifest.StartedAt, FinishedAt: "2026-08-12T08:00:02Z", ExperimentSpecID: manifest.ExperimentSpecID,
	}
	if options.Failed {
		verdict.Status = runstate.VerdictStatusFailed
		verdict.Message = "experiment failed"
		verdict.WorkloadExit = 1
	}
	if err := runstate.WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}
	if options.Bundle {
		writeBridgeInventory(t, runDir)
	}
	return runDir
}

func writeBridgeInventory(t *testing.T, runDir string) {
	t.Helper()
	var files []evidence.BundleFile
	if err := filepath.WalkDir(runDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == evidence.BundleInventoryName {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("unexpected non-regular fixture path %s", relative)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return err
		}
		files = append(files, evidence.BundleFile{Path: relative, Size: info.Size(), Digest: digest})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	manifest, err := runartifact.LoadOptionalEnv(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := evidence.MarshalBundleInventory(evidence.NewBundleInventory(manifest.Value("run_id", ""), files))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runDir, evidence.BundleInventoryName), content)
}

func snapshotBridgeRun(t *testing.T, runDir string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	if err := filepath.WalkDir(runDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = content
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func readArtifact(t *testing.T, path string) Artifact {
	t.Helper()
	artifact, err := parseArtifact(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func writeArtifact(t *testing.T, path string, artifact Artifact) {
	t.Helper()
	content, err := marshalArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	writeBytes(t, path, content)
}

func mustArtifactDigest(t *testing.T, artifact Artifact) string {
	t.Helper()
	digest, err := artifactDigest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func issuesContain(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return true
		}
	}
	return false
}

func TestArtifactJSONContainsNoUnexportedPath(t *testing.T) {
	artifact := Artifact{ArtifactPath: "/tmp/mutable"}
	content, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "mutable") || strings.Contains(string(content), "artifact_path") {
		t.Fatalf("unexported producer path leaked: %s", content)
	}
}
