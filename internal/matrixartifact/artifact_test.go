package matrixartifact

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
)

const (
	testVersion = "0.2.0"
	testCommit  = "0123456789abcdef0123456789abcdef01234567"
	testSpec    = "EXPERIMENT_NAME=smoke\nEXPERIMENT_PROFILE=smoke\n"
)

func TestVerifyCandidateAcceptsExactSuccessfulMatrix(t *testing.T) {
	root, matrixDir, _ := writeCandidateMatrix(t, 2)
	result, err := VerifyCandidate(root, matrixDir, candidateOptions(2))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("VerifyCandidate() issues = %#v", result.Issues)
	}
	canonicalMatrixDir, err := filepath.EvalSymlinks(matrixDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 2 || result.VerifiedRuns != 2 || result.Dir != canonicalMatrixDir {
		t.Fatalf("unexpected result: %#v", result)
	}

	var rendered bytes.Buffer
	if err := Render(&rendered, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "PASS: live candidate matrix") || !strings.Contains(rendered.String(), "rows=2/2 verified=2") || !strings.Contains(rendered.String(), "pack=test-pack/0.2.0") {
		t.Fatalf("unexpected text rendering: %s", rendered.String())
	}
	rendered.Reset()
	if err := RenderJSON(&rendered, result); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Valid        bool     `json:"valid"`
		Rows         int      `json:"rows"`
		VerifiedRuns int      `json:"verified_runs"`
		Issues       []string `json:"issues"`
	}
	if err := json.Unmarshal(rendered.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Valid || payload.Rows != 2 || payload.VerifiedRuns != 2 || payload.Issues == nil || len(payload.Issues) != 0 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestVerifyCandidateChecksIdentityOnEveryRow(t *testing.T) {
	root, matrixDir, rows := writeCandidateMatrix(t, 2)
	manifestPath := filepath.Join(rows[1].runDir, "manifest.env")
	replaceFile(t, manifestPath, `engine_commit="`+testCommit+`"`, `engine_commit="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)

	result := verifyWithoutError(t, root, matrixDir, candidateOptions(2))
	if result.Valid() || !containsIssue(result, "row 3 manifest.env engine_commit") {
		t.Fatalf("second-row identity mismatch was not rejected: %#v", result.Issues)
	}
}

func TestVerifyCandidateRejectsCountAndExactHeaderDrift(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		root, matrixDir, _ := writeCandidateMatrix(t, 2)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(3))
		if result.Valid() || !containsIssue(result, "row count is 2, expected exactly 3") {
			t.Fatalf("row-count mismatch was not rejected: %#v", result.Issues)
		}
	})

	t.Run("header", func(t *testing.T) {
		root, matrixDir, _ := writeCandidateMatrix(t, 1)
		index := filepath.Join(matrixDir, "runs.tsv")
		replaceFile(t, index, "experiment\tpg_config", "scenario\tpg_config")
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "exact 9-column contract") {
			t.Fatalf("header drift was not rejected: %#v", result.Issues)
		}
	})

	t.Run("header only", func(t *testing.T) {
		root, matrixDir, _ := writeCandidateMatrix(t, 1)
		writeMatrixIndex(t, matrixDir, nil)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "row count is 0, expected exactly 1") {
			t.Fatalf("vacuous matrix was not rejected: %#v", result.Issues)
		}
	})
}

func TestVerifyCandidateRejectsUnsafeRunPaths(t *testing.T) {
	t.Run("relative", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		rows[0].runDir = filepath.Join("runs", rows[0].runID)
		writeMatrixIndex(t, matrixDir, rows)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "run_dir must be absolute") {
			t.Fatalf("relative run path was not rejected: %#v", result.Issues)
		}
	})

	t.Run("outside runs root", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		rows[0].runDir = filepath.Join(root, "outside", rows[0].runID)
		writeMatrixIndex(t, matrixDir, rows)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "run_dir:") {
			t.Fatalf("outside run path was not rejected: %#v", result.Issues)
		}
	})

	t.Run("basename mismatch", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		rows[0].runID = "different-run"
		writeMatrixIndex(t, matrixDir, rows)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "does not match run_id") {
			t.Fatalf("run basename mismatch was not rejected: %#v", result.Issues)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		target := rows[0].runDir + "-target"
		if err := os.Rename(rows[0].runDir, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, rows[0].runDir); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "run_dir: must not be a symlink") {
			t.Fatalf("symlinked run directory was not rejected: %#v", result.Issues)
		}
	})
}

func TestVerifyCandidateRejectsSymlinkedMatrixInputs(t *testing.T) {
	t.Run("matrix directory", func(t *testing.T) {
		root, matrixDir, _ := writeCandidateMatrix(t, 1)
		alias := filepath.Join(filepath.Dir(matrixDir), "matrix-alias")
		if err := os.Symlink(matrixDir, alias); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := VerifyCandidate(root, alias, candidateOptions(1)); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("symlinked matrix directory error = %v", err)
		}
	})

	t.Run("runs.tsv", func(t *testing.T) {
		root, matrixDir, _ := writeCandidateMatrix(t, 1)
		index := filepath.Join(matrixDir, "runs.tsv")
		target := filepath.Join(matrixDir, "runs-real.tsv")
		if err := os.Rename(index, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, index); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "runs.tsv: must not be a symlink") {
			t.Fatalf("symlinked index was not rejected: %#v", result.Issues)
		}
	})
}

func TestVerifyCandidateRunsFullArtifactVerification(t *testing.T) {
	root, matrixDir, rows := writeCandidateMatrix(t, 1)
	verdictJSON := filepath.Join(rows[0].runDir, "verdict.json")
	replaceFile(t, verdictJSON, `"message": "experiment passed"`, `"message": "tampered"`)

	result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
	if result.Valid() || !containsIssue(result, "run verification: verdict.json message does not match verdict.env message") {
		t.Fatalf("full-run tampering was not rejected: %#v", result.Issues)
	}
}

func TestVerifyCandidateCrossChecksRowsAndRejectsDuplicates(t *testing.T) {
	t.Run("row to artifact", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		rows[0].experiment = "different-experiment"
		writeMatrixIndex(t, matrixDir, rows)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "manifest.env experiment_spec_id") {
			t.Fatalf("row/artifact drift was not rejected: %#v", result.Issues)
		}
	})

	t.Run("duplicate run", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 2)
		rows[1].runID = rows[0].runID
		rows[1].runDir = rows[0].runDir
		writeMatrixIndex(t, matrixDir, rows)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(2))
		if result.Valid() || !containsIssue(result, "duplicates run_id") || !containsIssue(result, "duplicates run_dir") {
			t.Fatalf("duplicate run was not rejected: %#v", result.Issues)
		}
	})

	t.Run("duplicate env key", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		manifest := filepath.Join(rows[0].runDir, "manifest.env")
		file, err := os.OpenFile(manifest, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(`engine_version="` + testVersion + `"` + "\n"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, `duplicate key "engine_version"`) {
			t.Fatalf("duplicate identity key was not rejected: %#v", result.Issues)
		}
	})
}

func TestVerifyCandidateBindsCheckoutPackAndExperimentProvenance(t *testing.T) {
	t.Run("checkout pack", func(t *testing.T) {
		root, matrixDir, _ := writeCandidateMatrix(t, 1)
		if err := os.WriteFile(filepath.Join(root, "experiments", "smoke.env"), []byte(testSpec+"# drift\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "manifest.env pack_digest") {
			t.Fatalf("checkout pack drift was not rejected: %#v", result.Issues)
		}
	})

	t.Run("manifest pack identity", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		manifest := filepath.Join(rows[0].runDir, "manifest.env")
		replaceFile(t, manifest, `pack_id="test-pack"`, `pack_id="different-pack"`)
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "manifest.env pack_id") {
			t.Fatalf("manifest pack mismatch was not rejected: %#v", result.Issues)
		}
	})

	t.Run("retained experiment spec", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		provenance := filepath.Join(rows[0].runDir, "artifacts", "provenance", "experiment-spec.env")
		if err := os.WriteFile(provenance, []byte(testSpec+"# tampered\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "experiment spec provenance digest") {
			t.Fatalf("experiment provenance tampering was not rejected: %#v", result.Issues)
		}
	})

	t.Run("coherent foreign spec snapshot", func(t *testing.T) {
		root, matrixDir, rows := writeCandidateMatrix(t, 1)
		foreignSpec := testSpec + "# foreign snapshot\n"
		manifest := filepath.Join(rows[0].runDir, "manifest.env")
		replaceFile(
			t,
			manifest,
			`experiment_spec_digest="`+evidence.DigestBytes([]byte(testSpec))+`"`,
			`experiment_spec_digest="`+evidence.DigestBytes([]byte(foreignSpec))+`"`,
		)
		provenance := filepath.Join(rows[0].runDir, "artifacts", "provenance", "experiment-spec.env")
		if err := os.WriteFile(provenance, []byte(foreignSpec), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := runstate.WriteVerdict(rows[0].runDir, runstate.Verdict{
			RunID: rows[0].runID, Status: runstate.VerdictStatusPassed, Message: rows[0].message,
			StartedAt: "2026-08-14T00:00:00Z", FinishedAt: "2026-08-14T00:00:02Z",
			ExperimentSpecID: rows[0].experiment, RunDir: rows[0].runDir,
		}); err != nil {
			t.Fatal(err)
		}
		live, err := runverify.Verify(root, rows[0].runDir)
		if err != nil {
			t.Fatal(err)
		}
		if !live.Valid() {
			t.Fatalf("coherent foreign snapshot must pass the generic live verifier: %#v", live.Issues)
		}
		result := verifyWithoutError(t, root, matrixDir, candidateOptions(1))
		if result.Valid() || !containsIssue(result, "experiment_spec_digest for current pack") {
			t.Fatalf("foreign spec snapshot was not rejected against pack inventory: %#v", result.Issues)
		}
	})
}

func TestVerifyCandidateRejectsInvalidOrDifferentVerifierIdentity(t *testing.T) {
	root, matrixDir, _ := writeCandidateMatrix(t, 1)

	options := candidateOptions(0)
	if _, err := VerifyCandidate(root, matrixDir, options); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("zero expected count error = %v", err)
	}
	options = candidateOptions(1)
	options.VerifierCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := VerifyCandidate(root, matrixDir, options); err == nil || !strings.Contains(err.Error(), "does not match expected candidate") {
		t.Fatalf("different verifier identity error = %v", err)
	}
}

func candidateOptions(expectedRuns int) Options {
	return Options{
		ExpectedVersion: testVersion,
		ExpectedCommit:  testCommit,
		ExpectedRuns:    expectedRuns,
		VerifierVersion: testVersion,
		VerifierCommit:  testCommit,
	}
}

func writeCandidateMatrix(t *testing.T, count int) (string, string, []matrixRow) {
	t.Helper()
	root := t.TempDir()
	writeScenarioPack(t, root)
	pack, err := scenariopack.ValidateForEngine(root, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	runsRoot := filepath.Join(root, "runs")
	matrixDir := filepath.Join(runsRoot, "matrices", "matrix-a")
	if err := os.MkdirAll(matrixDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := make([]matrixRow, 0, count)
	for index := 1; index <= count; index++ {
		runID := fmt.Sprintf("matrix-a-smoke-default-small-r%02d", index)
		runDir := filepath.Join(runsRoot, runID)
		writeCandidateRun(t, runDir, runID, "smoke", "default", "small", "experiment passed", pack)
		rows = append(rows, matrixRow{
			experiment: "smoke", pgConfig: "default", profileSize: "small",
			repeat: fmt.Sprintf("%d", index), runID: runID, exitCode: "0",
			status: "passed", message: "experiment passed", runDir: runDir,
		})
	}
	writeMatrixIndex(t, matrixDir, rows)
	return root, matrixDir, rows
}

func writeCandidateRun(t *testing.T, runDir string, runID string, experiment string, pgConfig string, profileSize string, message string, pack scenariopack.Inspection) {
	t.Helper()
	if err := runstate.WriteManifest(runDir, runstate.Manifest{
		RunID:                    runID,
		StartedAt:                "2026-08-14T00:00:00Z",
		ExperimentSpec:           "experiments/" + experiment + ".env",
		ExperimentSpecID:         experiment,
		ExperimentSpecRef:        "experiments/" + experiment + ".env",
		ExperimentSpecDigest:     evidence.DigestBytes([]byte(testSpec)),
		Runtime:                  "docker",
		EngineVersion:            testVersion,
		EngineCommit:             testCommit,
		PackID:                   pack.ID,
		PackVersion:              pack.Version,
		PackDigest:               pack.Digest,
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-08-14T00:00:01Z",
		ExperimentName:           experiment,
		ExperimentTopology:       "single",
		ExperimentPGConfig:       pgConfig,
		Profile:                  experiment,
		ProfileSize:              profileSize,
		WorkloadSpec:             "sql/smoke-run",
		MetricsEnabled:           "0",
		RunDir:                   runDir,
	}); err != nil {
		t.Fatal(err)
	}
	provenanceDir := filepath.Join(runDir, "artifacts", "provenance")
	if err := os.MkdirAll(provenanceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provenanceDir, "experiment-spec.env"), []byte(testSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID: runID, Status: runstate.VerdictStatusPassed, Message: message,
		StartedAt: "2026-08-14T00:00:00Z", FinishedAt: "2026-08-14T00:00:02Z",
		ExperimentSpecID: experiment, RunDir: runDir,
	}); err != nil {
		t.Fatal(err)
	}
}

func writeScenarioPack(t *testing.T, root string) {
	t.Helper()
	manifest := `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "test-pack",
  "version": "0.2.0",
  "engine_constraint": ">=0.2.0",
  "assets": ["experiments"]
}
`
	if err := os.WriteFile(filepath.Join(root, scenariopack.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "experiments", "smoke.env"), []byte(testSpec), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMatrixIndex(t *testing.T, matrixDir string, rows []matrixRow) {
	t.Helper()
	var content bytes.Buffer
	writer := csv.NewWriter(&content)
	writer.Comma = '\t'
	if err := writer.Write(matrixHeader); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			row.experiment, row.pgConfig, row.profileSize, row.repeat, row.runID,
			row.exitCode, row.status, row.message, row.runDir,
		}); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(matrixDir, "runs.tsv"), content.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func verifyWithoutError(t *testing.T, root string, matrixDir string, options Options) Result {
	t.Helper()
	result, err := VerifyCandidate(root, matrixDir, options)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func containsIssue(result Result, substring string) bool {
	for _, issue := range result.Issues {
		if strings.Contains(issue, substring) {
			return true
		}
	}
	return false
}

func replaceFile(t *testing.T, path string, old string, replacement string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(content), old, replacement, 1)
	if updated == string(content) {
		t.Fatalf("replacement source %q not found in %s", old, path)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}
