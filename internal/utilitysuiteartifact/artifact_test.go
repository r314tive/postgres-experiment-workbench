package utilitysuiteartifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilitysuite"
)

func TestUtilitySuiteArtifactListShowVerify(t *testing.T) {
	root := t.TempDir()
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
	if bundle.Output != output || len(bundle.LinkedRuns) != 2 || len(bundle.MissingLinkedRuns) != 0 || bundle.Files == 0 || bundle.Bytes == 0 {
		t.Fatalf("unexpected bundle result: %#v", bundle)
	}
	out.Reset()
	if err := RenderBundle(&out, bundle); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Wrote utility suite bundle") {
		t.Fatalf("unexpected bundle render: %s", out.String())
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
		RunID:              runID,
		StartedAt:          startedAt,
		ExperimentSpec:     filepath.Join(filepath.Dir(runDir), runID+".env"),
		ExperimentSpecID:   "utility/generated",
		ExperimentName:     "utility generated",
		ExperimentTopology: "single",
		ExperimentPGConfig: "default",
		Profile:            "smoke",
		ProfileSize:        "small",
		WorkloadSpec:       "utility/noop",
		RunDir:             runDir,
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
