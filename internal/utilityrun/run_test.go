package utilityrun

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
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
		"UTILITY_TEST_TRUSTED_SHELL=1",
		"UTILITY_TEST_EXPECT_FILES=logs/utility/out.sql",
		"UTILITY_TEST_ASSERT_SQL=SELECT 1;",
		"UTILITY_TEST_ASSERT_SHELL=echo ok",
		"UTILITY_TEST_SCAN_PATHS=logs/utility",
		"",
	}, "\n"))

	var command []string
	var commandEnv []string
	now := time.Date(2026, 6, 5, 10, 11, 12, 0, time.UTC)
	result, err := Run(root, speccatalog.New(root), "pg-dump/smoke", Options{
		Env: []string{
			"KEEP_ME=present",
			ExperimentSpecScopeEnv + "=untrusted",
			ExperimentSpecScopeEnv + "=still-untrusted",
			SourceSpecKindEnv + "=untrusted",
			"PGWORKBENCH_PACK_ID=untrusted-pack",
			"PGWORKBENCH_PACK_VERSION=9.9.9",
			"PGWORKBENCH_PACK_DIGEST=sha256:untrusted",
		},
		Now:    func() time.Time { return now },
		Getenv: func(string) string { return "" },
		RunCommand: func(_ string, cmd []string, env []string, _, _ io.Writer) CommandResult {
			command = append([]string(nil), cmd...)
			commandEnv = append([]string(nil), env...)
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
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if rel, relErr := filepath.Rel(canonicalRoot, command[2]); relErr != nil || filepath.ToSlash(rel) != ".tmp/utility-tests/utility-pg-dump_smoke-20260605_101112.env" {
		t.Fatalf("unexpected generated spec path %q (rel=%q err=%v)", command[2], rel, relErr)
	}
	sourceDigest, err := evidence.DigestFile(filepath.Join(root, "utility-tests", "pg-dump", "smoke.env"))
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := map[string]string{
		"KEEP_ME":                  "present",
		"PGWORKBENCH_RUNTIME":      "docker",
		"UTILITY_TEST_RUN_ID":      "utility-pg-dump_smoke-20260605_101112",
		ExperimentSpecScopeEnv:     UtilityDerivedSpecScope,
		DerivedExperimentIDEnv:     "utility/pg-dump/smoke",
		SourceSpecKindEnv:          UtilityTestSourceKind,
		SourceSpecIDEnv:            "pg-dump/smoke",
		SourceSpecRefEnv:           "utility-tests/pg-dump/smoke.env",
		SourceSpecDigestEnv:        sourceDigest,
		"PGWORKBENCH_PACK_ID":      "",
		"PGWORKBENCH_PACK_VERSION": "",
		"PGWORKBENCH_PACK_DIGEST":  "",
	}
	gotEnv := envMap(t, commandEnv)
	if len(gotEnv) != len(wantEnv) {
		t.Fatalf("command env has unexpected entries: %#v", commandEnv)
	}
	for key, want := range wantEnv {
		if got := gotEnv[key]; got != want {
			t.Fatalf("command env %s=%q, want %q (all=%#v)", key, got, want, commandEnv)
		}
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
		"EXPERIMENT_TRUSTED_SHELL='1'",
		`EXPERIMENT_ASSERT_SHELL="echo ok; test -s \"$REPO_DIR/logs/utility/out.sql\""`,
		"EXPERIMENT_CAPTURE_FILES='logs/utility/out.sql'",
		"EXPERIMENT_SCAN_PATHS='logs/utility'",
		`EXPERIMENT_SNAPSHOT="${UTILITY_TEST_SNAPSHOT:-1}"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated spec missing %q:\n%s", want, text)
		}
	}
}

func TestRunUtilityTestCLIOptionsOverrideEnvironment(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")

	var commandEnv []string
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "native",
		RunID:   "cli-run",
		Env: []string{
			"PGWORKBENCH_RUNTIME=docker",
			"UTILITY_TEST_RUN_ID=env-run",
		},
		Getenv: func(key string) string {
			switch key {
			case "PGWORKBENCH_RUNTIME":
				return "docker"
			case "UTILITY_TEST_RUN_ID":
				return "getenv-run"
			default:
				return ""
			}
		},
		RunCommand: func(_ string, _ []string, env []string, _, _ io.Writer) CommandResult {
			commandEnv = append([]string(nil), env...)
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Runtime != "native" || result.RunID != "cli-run" {
		t.Fatalf("CLI options did not win: %#v", result)
	}
	values := envMap(t, commandEnv)
	if values["PGWORKBENCH_RUNTIME"] != "native" || values["UTILITY_TEST_RUN_ID"] != "cli-run" {
		t.Fatalf("command environment did not receive CLI values: %#v", commandEnv)
	}
	content, err := os.ReadFile(result.ExperimentSpec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "EXPERIMENT_RUN_ID='cli-run'") {
		t.Fatalf("generated spec did not receive CLI run id:\n%s", content)
	}
}

func TestRunUtilityTestUsesOptionsEnvironmentBeforeAmbient(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")

	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Env: []string{"PGWORKBENCH_RUNTIME=native", "UTILITY_TEST_RUN_ID=env-run"},
		Getenv: func(key string) string {
			if key == "PGWORKBENCH_RUNTIME" {
				return "docker"
			}
			if key == "UTILITY_TEST_RUN_ID" {
				return "ambient-run"
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
	if result.Runtime != "native" || result.RunID != "env-run" {
		t.Fatalf("options environment did not win over ambient: %#v", result)
	}
}

func TestRunUtilityTestRejectsRuntimeAndRunIDBeforeGeneratingSpec(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")

	for _, options := range []Options{
		{Runtime: "remote"},
		{Runtime: "native", RunID: "../escape"},
		{Runtime: "docker", RunID: strings.Repeat("a", 201)},
	} {
		options.Getenv = func(string) string { return "" }
		options.RunCommand = func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			t.Fatal("command must not run")
			return CommandResult{}
		}
		if _, err := Run(root, speccatalog.New(root), "smoke", options); err == nil {
			t.Fatalf("unsafe options were accepted: %#v", options)
		}
		if _, err := os.Lstat(filepath.Join(root, ".tmp")); !os.IsNotExist(err) {
			t.Fatalf("rejected options created .tmp: %v", err)
		}
	}
}

func TestRunUtilityTestRejectsSymlinkedGeneratedDirectory(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".tmp")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(root, speccatalog.New(root), "smoke", Options{
		Getenv: func(string) string { return "" },
		RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			t.Fatal("command must not run")
			return CommandResult{}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain symlinks") {
		t.Fatalf("expected generated directory symlink rejection, got %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("generated spec escaped through symlink: %#v", entries)
	}
}

func TestRunUtilityTestRejectsSymlinkedSourceAncestor(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	outside := t.TempDir()
	writeSpec(t, outside, "smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")
	if err := os.Symlink(outside, filepath.Join(root, "utility-tests")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(root, speccatalog.New(root), "smoke", Options{
		Getenv: func(string) string { return "" },
		RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			t.Fatal("command must not run")
			return CommandResult{}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "path must not contain symlinks") {
		t.Fatalf("expected source ancestor symlink rejection, got %v", err)
	}
}

func TestRunUtilityTestRejectsSymlinkedGeneratedFile(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")
	generatedDir := filepath.Join(root, ".tmp", "utility-tests")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.env")
	if err := os.WriteFile(outside, []byte("unchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(generatedDir, "manual.env")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(root, speccatalog.New(root), "smoke", Options{
		Getenv: func(key string) string {
			if key == "UTILITY_TEST_RUN_ID" {
				return "manual"
			}
			return ""
		},
		RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			t.Fatal("command must not run")
			return CommandResult{}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "generated utility spec must not be a symlink") {
		t.Fatalf("expected generated file symlink rejection, got %v", err)
	}
	content, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "unchanged\n" {
		t.Fatalf("outside target was modified: %q", content)
	}
}

func TestRunUtilityTestNeverReplacesExistingGeneratedSpec(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")
	generated := filepath.Join(root, ".tmp", "utility-tests", "manual.env")
	writeSpec(t, root, ".tmp/utility-tests/manual.env", "unchanged\n")

	_, err := Run(root, speccatalog.New(root), "smoke", Options{
		RunID:  "manual",
		Getenv: func(string) string { return "" },
		RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			t.Fatal("command must not run")
			return CommandResult{}
		},
	})
	if !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("expected immutable generated-spec rejection, got %v", err)
	}
	content, readErr := os.ReadFile(generated)
	if readErr != nil || string(content) != "unchanged\n" {
		t.Fatalf("existing generated spec changed: content=%q err=%v", content, readErr)
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

func envMap(t *testing.T, values []string) map[string]string {
	t.Helper()
	result := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if !ok {
			t.Fatalf("invalid environment entry: %q", value)
		}
		if _, exists := result[key]; exists {
			t.Fatalf("duplicate environment key %q in %#v", key, values)
		}
		result[key] = item
	}
	return result
}
