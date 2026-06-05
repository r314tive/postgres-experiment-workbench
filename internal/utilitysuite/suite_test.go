package utilitysuite

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityrun"
)

func TestBuildUtilitySuitePlan(t *testing.T) {
	root := t.TempDir()
	writeUtilitySuiteFixture(t, root)

	plan, err := Build(speccatalog.New(root), "native")
	if err != nil {
		t.Fatal(err)
	}

	if plan.TotalRuns != 4 {
		t.Fatalf("unexpected total runs: %#v", plan)
	}
	if !plan.StopOnFail || plan.Snapshot != "0" {
		t.Fatalf("unexpected suite flags: %#v", plan)
	}
	if plan.Runs[0].UtilityTest != "pg-dump/smoke" || plan.Runs[3].ProfileSize != "medium" || plan.Runs[3].Repeat != 1 {
		t.Fatalf("unexpected runs: %#v", plan.Runs)
	}
}

func TestRunUtilitySuiteWritesSummary(t *testing.T) {
	root := t.TempDir()
	writeUtilitySuiteFixture(t, root)
	now := time.Date(2026, 6, 5, 12, 30, 0, 0, time.UTC)
	var seen []string

	result, err := Run(root, speccatalog.New(root), "native", RunOptions{
		Now: func() time.Time { return now },
		Getenv: func(key string) string {
			if key == "UTILITY_SUITE_RUN_ID" {
				return "suite-manual"
			}
			return ""
		},
		RunUtility: func(_ string, _ speccatalog.Catalog, input string, options utilityrun.Options) (utilityrun.Result, error) {
			seen = append(seen, input+"|"+options.Getenv("UTILITY_TEST_RUN_ID")+"|"+options.Getenv("PROFILE_SIZE")+"|"+options.Getenv("UTILITY_TEST_SNAPSHOT"))
			return utilityrun.Result{
				UtilityTestSpec: input,
				RunID:           options.Getenv("UTILITY_TEST_RUN_ID"),
				ExperimentSpec:  "/tmp/" + input + ".env",
				ExitCode:        0,
				Status:          "passed",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != "passed" || result.Passed != 4 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(seen) != 4 || !strings.Contains(seen[0], "pg-dump/smoke|suite-manual-pg-dump_smoke-small-r01|small|0") {
		t.Fatalf("unexpected utility calls: %#v", seen)
	}
	for _, rel := range []string{"runs.tsv", "summary.md", "driver-logs/suite-manual-pg-dump_smoke-small-r01.log"} {
		if _, err := os.Stat(filepath.Join(result.RunDir, rel)); err != nil {
			t.Fatalf("missing suite artifact %s: %v", rel, err)
		}
	}
}

func TestRunUtilitySuiteStopsOnFail(t *testing.T) {
	root := t.TempDir()
	writeUtilitySuiteFixture(t, root)
	calls := 0

	result, err := Run(root, speccatalog.New(root), "native", RunOptions{
		RunUtility: func(_ string, _ speccatalog.Catalog, input string, options utilityrun.Options) (utilityrun.Result, error) {
			calls++
			return utilityrun.Result{
				UtilityTestSpec: input,
				RunID:           options.Getenv("UTILITY_TEST_RUN_ID"),
				ExitCode:        2,
				Status:          "failed",
			}, errors.New("utility failed")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "utility suite failed") {
		t.Fatalf("expected suite failure, got %v", err)
	}
	if calls != 1 || result.Failed != 1 || result.Status != "failed" {
		t.Fatalf("unexpected stopped result calls=%d result=%#v", calls, result)
	}
}

func TestRenderUtilitySuite(t *testing.T) {
	root := t.TempDir()
	writeUtilitySuiteFixture(t, root)
	plan, err := Build(speccatalog.New(root), "native")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Utility Suite Plan", "Total runs", "pg-restore/smoke"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered suite missing %q:\n%s", want, out.String())
		}
	}
}

func writeUtilitySuiteFixture(t *testing.T, root string) {
	t.Helper()
	writeSpec(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeSpec(t, root, "workloads/utility/noop.env", "WORKLOAD_NAME=noop\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo noop'\n")
	writeSpec(t, root, "utility-tests/pg-dump/smoke.env", "UTILITY_TEST_NAME=pg_dump smoke\nUTILITY_TEST_PROFILE=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\n")
	writeSpec(t, root, "utility-tests/pg-restore/smoke.env", "UTILITY_TEST_NAME=pg_restore smoke\nUTILITY_TEST_PROFILE=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\n")
	writeSpec(t, root, "utility-suites/native.env", strings.Join([]string{
		"UTILITY_SUITE_NAME=native utility suite",
		"UTILITY_SUITE_TESTS=\"pg-dump/smoke pg-restore/smoke\"",
		"UTILITY_SUITE_PROFILE_SIZES=\"small medium\"",
		"UTILITY_SUITE_REPEATS=1",
		"UTILITY_SUITE_STOP_ON_FAIL=1",
		"UTILITY_SUITE_SNAPSHOT=0",
		"",
	}, "\n"))
}

func writeSpec(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
