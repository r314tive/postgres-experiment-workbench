package utilityrun

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestRunUtilityTestGeneratesExperimentSpec(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/pg-dump/smoke.env", strings.Join([]string{
		"UTILITY_TEST_NAME=pg_dump smoke",
		"UTILITY_TEST_PROFILE=smoke",
		"UTILITY_TEST_PROFILE_SIZE=\"${PROFILE_SIZE:-small}\"",
		"UTILITY_TEST_WORKLOAD_SPEC=utility/smoke",
		"UTILITY_TEST_BACKGROUND_WARMUP=2",
		"UTILITY_TEST_METRICS=1",
		"UTILITY_TEST_METRICS_SAMPLES=\"${METRICS_SAMPLES:-2}\"",
		"UTILITY_TEST_EXPECT_FILES=logs/utility/out.sql",
		"UTILITY_TEST_ASSERT_SQL=SELECT 1;",
		"UTILITY_TEST_ASSERT_SHELL=echo ok",
		"UTILITY_TEST_SCAN_PATHS=logs/utility",
		"",
	}, "\n"))

	var command []string
	now := time.Date(2026, 6, 5, 10, 11, 12, 0, time.UTC)
	result, err := Run(root, speccatalog.New(root), "pg-dump/smoke", Options{
		Now:    func() time.Time { return now },
		Getenv: func(string) string { return "" },
		RunCommand: func(_ string, cmd []string, _ []string, _, _ io.Writer) CommandResult {
			command = append([]string(nil), cmd...)
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != "passed" || result.RunID != "utility-pg-dump_smoke-20260605_101112" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(command) != 3 || command[1] != "run" {
		t.Fatalf("unexpected command: %#v", command)
	}

	content, err := os.ReadFile(result.ExperimentSpec)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{
		"EXPERIMENT_NAME='utility: pg_dump smoke'",
		"EXPERIMENT_RUN_ID='utility-pg-dump_smoke-20260605_101112'",
		"EXPERIMENT_PROFILE='smoke'",
		`EXPERIMENT_PROFILE_SIZE="${PROFILE_SIZE:-small}"`,
		"EXPERIMENT_WORKLOAD_SPEC='utility/smoke'",
		`EXPERIMENT_METRICS_SAMPLES="${METRICS_SAMPLES:-2}"`,
		"EXPERIMENT_ASSERT_SQL='SELECT 1;'",
		`EXPERIMENT_ASSERT_SHELL="echo ok; test -s \"$REPO_DIR/logs/utility/out.sql\""`,
		"EXPERIMENT_SCAN_PATHS='logs/utility'",
		`EXPERIMENT_SNAPSHOT="${UTILITY_TEST_SNAPSHOT:-1}"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated spec missing %q:\n%s", want, text)
		}
	}
}

func TestRunUtilityTestUsesRunIDOverride(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")

	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Getenv: func(key string) string {
			if key == "UTILITY_TEST_RUN_ID" {
				return "manual-run"
			}
			return ""
		},
		RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != "manual-run" {
		t.Fatalf("expected override run id, got %q", result.RunID)
	}
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
