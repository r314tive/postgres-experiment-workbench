package utilitysuiteartifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilitysuite"
)

func TestUtilitySuiteArtifactListShowVerify(t *testing.T) {
	root := t.TempDir()
	run := writeValidSuiteRun(t, root)

	summaries, err := List(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].RunID != "suite-a" || summaries[0].Total != 2 {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}

	summary, err := Show(root, "suite-a")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != "passed" || summary.Passed != 2 || summary.Failed != 0 || !summary.HasResultJSON {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	verification, err := Verify(root, run.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.IsValid() {
		t.Fatalf("expected valid artifact, got: %#v", verification.Issues)
	}

	var out bytes.Buffer
	if err := RenderList(&out, summaries); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "# Utility Suite Runs") {
		t.Fatalf("unexpected list render: %s", out.String())
	}
	out.Reset()
	if err := RenderVerify(&out, verification); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PASS: utility suite artifact") {
		t.Fatalf("unexpected verify render: %s", out.String())
	}

	output := filepath.Join(root, "generated", "suite-a.tar.gz")
	bundle, err := CreateBundle(root, "suite-a", output)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput, err := pathguard.ResolveOutputOutside(run.RunDir, output)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Output != wantOutput || len(bundle.LinkedRuns) != 2 || len(bundle.MissingLinkedRuns) != 0 || bundle.Files == 0 || bundle.Bytes == 0 {
		t.Fatalf("unexpected bundle result: %#v", bundle)
	}
	out.Reset()
	if err := RenderBundle(&out, bundle); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Wrote utility suite bundle") {
		t.Fatalf("unexpected bundle render: %s", out.String())
	}
	secondOutput := filepath.Join(root, "generated", "suite-a-second.tar.gz")
	if _, err := CreateBundle(root, "suite-a", secondOutput); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("identical utility suite bundles differ byte-for-byte")
	}

	names := readTarNames(t, output)
	for _, want := range []string{
		"utility-suites/suite-a/result.json",
		"utility-suites/suite-a/runs.tsv",
		"utility-suites/suite-a/summary.md",
		"runs/suite-a-pg-dump_smoke-small-r01/manifest.env",
		"runs/suite-a-pg-restore_smoke-small-r01/verdict.json",
	} {
		if !hasTarName(names, want) {
			t.Fatalf("missing tar entry %q in %#v", want, names)
		}
	}

	relocatedRoot := t.TempDir()
	extractTarGz(t, output, relocatedRoot)
	relocatedSuiteDir := filepath.Join(relocatedRoot, "utility-suites", "suite-a")
	portableEntries, err := readEntries(filepath.Join(relocatedSuiteDir, "runs.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	portableResult, err := readRunResultJSON(filepath.Join(relocatedSuiteDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(portableResult.RunDir) || portableResult.RunDir != "utility-suites/suite-a" {
		t.Fatalf("result.json run_dir is not portable: %q", portableResult.RunDir)
	}
	for _, entry := range portableResult.Entries {
		for label, value := range map[string]string{
			"run_dir":         entry.RunDir,
			"experiment_spec": entry.ExperimentSpec,
			"driver_log":      entry.DriverLog,
		} {
			if filepath.IsAbs(value) || strings.Contains(value, root) {
				t.Fatalf("result.json %s is not portable for %s: %q", label, entry.RunID, value)
			}
		}
	}
	for _, entry := range portableEntries {
		for label, value := range map[string]string{
			"run_dir":         entry.RunDir,
			"experiment_spec": entry.ExperimentSpec,
			"driver_log":      entry.DriverLog,
		} {
			if filepath.IsAbs(value) || strings.Contains(value, root) {
				t.Fatalf("runs.tsv %s is not portable for %s: %q", label, entry.RunID, value)
			}
		}
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("producer tree is still accessible: %v", err)
	}
	relocatedVerification, err := Verify(relocatedRoot, relocatedSuiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if !relocatedVerification.IsValid() {
		t.Fatalf("relocated bundle must verify without producer tree: %#v", relocatedVerification.Issues)
	}
}

func TestCreateUtilitySuiteBundleRejectsTamperedStageBeforeArchive(t *testing.T) {
	root := t.TempDir()
	writeValidSuiteRun(t, root)
	output := filepath.Join(root, "generated", "tampered-stage.tar.gz")
	_, err := createBundle(root, "suite-a", output, func(stage string) error {
		path := filepath.Join(stage, "utility-suites", "suite-a", "result.json")
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		mutated := bytes.ReplaceAll(content, []byte(`"status": "passed"`), []byte(`"status": "failed"`))
		if bytes.Equal(mutated, content) {
			return errors.New("utility suite fixture has no passed status")
		}
		return os.WriteFile(path, mutated, 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "staged utility suite bundle is invalid") {
		t.Fatalf("tampered staged utility suite bundle was published: %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive exists after staged verification failure: %v", statErr)
	}
}

func TestCreateBundleNeverReplacesExistingOutput(t *testing.T) {
	root := t.TempDir()
	run := writeValidSuiteRun(t, root)
	output := filepath.Join(root, "generated", "existing.tar.gz")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := CreateBundle(root, run.RunDir, output)
	if !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("expected existing-output rejection, got %v", err)
	}
	content, readErr := os.ReadFile(output)
	if readErr != nil || string(content) != "sentinel\n" {
		t.Fatalf("existing output changed: content=%q err=%v", content, readErr)
	}
}

func TestUtilitySuiteArtifactVerifyDetectsBrokenStructure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "runs", "utility-suites", "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "runs.tsv"), strings.Join([]string{
		"utility_test\tprofile_size\trepeat\trun_id\texit_code\tstatus\tmessage\trun_dir\texperiment_spec\tdriver_log",
		"pg-dump/smoke\tsmall\t0\trun-a\t0\tunknown\tbad\truns/run-a\t\tdriver-logs/run-a.log",
		"",
	}, "\n"))

	result, err := Verify(root, "broken")
	if err != nil {
		t.Fatal(err)
	}
	if result.IsValid() {
		t.Fatal("expected invalid suite artifact")
	}
	for _, want := range []string{
		"missing summary.md",
		"missing result.json",
		"missing driver-logs",
		"runs.tsv row 2 repeat must be positive",
		"runs.tsv row 2 status must be passed or failed",
		"missing driver log for run-a: " + filepath.Join(dir, "driver-logs", "run-a.log"),
	} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestCreateBundleFailsClosed(t *testing.T) {
	t.Run("output inside suite directly or through aliased parent", func(t *testing.T) {
		root := t.TempDir()
		run := writeValidSuiteRun(t, root)
		linkedRunDir := run.Entries[0].RunDir
		alias := filepath.Join(t.TempDir(), "linked-run-alias")
		if err := os.Symlink(linkedRunDir, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		tests := []struct {
			name   string
			output string
			target string
		}{
			{name: "direct child", output: filepath.Join(run.RunDir, "direct.tar.gz"), target: filepath.Join(run.RunDir, "direct.tar.gz")},
			{name: "aliased linked-run parent", output: filepath.Join(alias, "aliased.tar.gz"), target: filepath.Join(linkedRunDir, "aliased.tar.gz")},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, err := CreateBundle(root, run.RunDir, test.output)
				if !errors.Is(err, pathguard.ErrOutputWithinSource) {
					t.Fatalf("expected output-containment error, got %v", err)
				}
				if _, statErr := os.Lstat(test.target); !os.IsNotExist(statErr) {
					t.Fatalf("output was written inside suite artifact: %v", statErr)
				}
			})
		}
	})

	t.Run("invalid suite artifact", func(t *testing.T) {
		root := t.TempDir()
		run := writeValidSuiteRun(t, root)
		if err := os.Remove(filepath.Join(run.RunDir, "summary.md")); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(root, "generated", "invalid.tar.gz")
		_, err := CreateBundle(root, run.RunDir, output)
		if err == nil || !strings.Contains(err.Error(), "utility suite artifact is invalid") {
			t.Fatalf("expected invalid-suite error, got %v", err)
		}
		if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
			t.Fatalf("bundle output must not be created after validation failure: %v", statErr)
		}
	})

	t.Run("missing linked failed run", func(t *testing.T) {
		root := t.TempDir()
		run := writeValidSuiteRun(t, root)
		entries, err := readEntries(filepath.Join(run.RunDir, "runs.tsv"))
		if err != nil {
			t.Fatal(err)
		}
		entries[0].Status = "failed"
		entries[0].ExitCode = 1
		entries[0].Message = "utility failed"
		runsTSV, err := marshalEntriesTSV(entries)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(run.RunDir, "runs.tsv"), string(runsTSV))

		runResult, err := readRunResultJSON(filepath.Join(run.RunDir, "result.json"))
		if err != nil {
			t.Fatal(err)
		}
		runResult.Status = "failed"
		runResult.Passed--
		runResult.Failed++
		runResult.Entries[0].Status = "failed"
		runResult.Entries[0].ExitCode = 1
		runResult.Entries[0].Message = "utility failed"
		if err := utilitysuite.RenderRunJSONFile(filepath.Join(run.RunDir, "result.json"), *runResult); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(entries[0].RunDir); err != nil {
			t.Fatal(err)
		}

		verification, err := Verify(root, run.RunDir)
		if err != nil {
			t.Fatal(err)
		}
		if !verification.IsValid() {
			t.Fatalf("fixture must isolate missing-linked-run bundle gate: %#v", verification.Issues)
		}
		output := filepath.Join(root, "generated", "missing.tar.gz")
		_, err = CreateBundle(root, run.RunDir, output)
		if err == nil || !strings.Contains(err.Error(), "linked experiment run artifact is missing") {
			t.Fatalf("expected missing-linked-run error, got %v", err)
		}
		if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
			t.Fatalf("bundle output must not be created with a missing linked run: %v", statErr)
		}
	})
}

func writeValidSuiteRun(t *testing.T, root string) utilitysuite.RunResult {
	t.Helper()
	writeSuiteSpecs(t, root)
	now := time.Date(2026, 6, 5, 12, 30, 0, 0, time.UTC)
	run, err := utilitysuite.Run(root, speccatalog.New(root), "native", utilitysuite.RunOptions{
		Now: func() time.Time { return now },
		Getenv: func(key string) string {
			if key == "UTILITY_SUITE_RUN_ID" {
				return "suite-a"
			}
			return ""
		},
		RunUtility: func(root string, _ speccatalog.Catalog, input string, options utilityrun.Options) (utilityrun.Result, error) {
			runID := options.Getenv("UTILITY_TEST_RUN_ID")
			runDir := filepath.Join(root, "runs", runID)
			writeValidExperimentRun(t, runDir, runID)
			return utilityrun.Result{
				UtilityTestSpec: input,
				RunID:           runID,
				ExperimentSpec:  filepath.Join(root, ".tmp", runID+".env"),
				ExitCode:        0,
				Status:          "passed",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func writeSuiteSpecs(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "profiles", "smoke", "profile.env"), "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeFile(t, filepath.Join(root, "workloads", "utility", "noop.env"), "WORKLOAD_NAME=noop\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo noop'\n")
	writeFile(t, filepath.Join(root, "utility-tests", "pg-dump", "smoke.env"), "UTILITY_TEST_NAME=pg_dump smoke\nUTILITY_TEST_PROFILE=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\n")
	writeFile(t, filepath.Join(root, "utility-tests", "pg-restore", "smoke.env"), "UTILITY_TEST_NAME=pg_restore smoke\nUTILITY_TEST_PROFILE=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\n")
	writeFile(t, filepath.Join(root, "utility-suites", "native.env"), strings.Join([]string{
		"UTILITY_SUITE_NAME=native utility suite",
		"UTILITY_SUITE_TESTS=\"pg-dump/smoke pg-restore/smoke\"",
		"UTILITY_SUITE_PROFILE_SIZES=\"small\"",
		"UTILITY_SUITE_REPEATS=1",
		"UTILITY_SUITE_SNAPSHOT=0",
		"",
	}, "\n"))
}

func writeValidExperimentRun(t *testing.T, runDir string, runID string) {
	t.Helper()
	startedAt := "2026-06-05T12:30:00Z"
	finishedAt := "2026-06-05T12:30:01Z"
	if err := runstate.WriteManifest(runDir, runstate.Manifest{
		RunID:                    runID,
		StartedAt:                startedAt,
		ExperimentSpec:           filepath.Join(filepath.Dir(runDir), runID+".env"),
		ExperimentSpecID:         "utility/generated",
		ExperimentName:           "utility generated",
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		Profile:                  "smoke",
		ProfileSize:              "small",
		WorkloadSpec:             "utility/noop",
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-06-05T12:30:00Z",
		RunDir:                   runDir,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID:            runID,
		Status:           "passed",
		Message:          "experiment passed",
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		ExperimentSpecID: "utility/generated",
		RunDir:           runDir,
		WorkloadExit:     0,
		AssertExit:       0,
		ScanExit:         0,
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "metrics.csv"), "sampled_at,database_name,wal_bytes\nt0,postgres,100\n")
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), "EXPERIMENT_NAME=utility generated\n")
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTarNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
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
	var names []string
	for {
		header, err := reader.Next()
		if err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
		names = append(names, header.Name)
	}
	sort.Strings(names)
	return names
}

func extractTarGz(t *testing.T, archivePath string, destination string) {
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
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("unsafe tar path: %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("unexpected tar entry type for %q: %d", header.Name, header.Typeflag)
		}
		target := filepath.Join(destination, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		if copyErr != nil {
			t.Fatal(copyErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

func hasTarName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func hasIssue(result VerifyResult, issue string) bool {
	for _, candidate := range result.Issues {
		if candidate == issue {
			return true
		}
	}
	return false
}
