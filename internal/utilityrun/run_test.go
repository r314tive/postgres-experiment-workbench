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
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestCLIEnvironmentProjectsOnlyUtilityRuntimeInputs(t *testing.T) {
	values := map[string]string{
		"ENV_FILE":                            ".env.native",
		"COMPOSE":                             "docker compose --ansi never",
		"PGWORKBENCH_RUNTIME":                 "native",
		"PGWORKBENCH_NATIVE_BINDIR":           "/opt/postgres/bin",
		"PGWORKBENCH_NATIVE_WAIT_SECONDS":     "90",
		"PG_INSTALL_DIR":                      "/opt/postgres",
		"POSTGRES_HOST":                       "127.0.0.1",
		"POSTGRES_PORT":                       "59433",
		"POSTGRES_DB":                         "pg_experiment_workbench",
		"POSTGRES_USER":                       "postgres",
		"POSTGRES_PASSWORD":                   "secret",
		"PROFILE_SIZE":                        "medium",
		"PROFILE_SECONDS":                     "45",
		"METRICS_INTERVAL":                    "2",
		"METRICS_DURATION":                    "10",
		"METRICS_SAMPLES":                     "3",
		"UTILITY_TEST_SNAPSHOT":               "0",
		"PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST": "sha256:" + strings.Repeat("a", 64),
		"PGWORKBENCH_BENCHMARK_CAPSULE_ROOT":  "/tmp/hostile-capsule",
		"EXPERIMENT_BEFORE_SHELL":             "hostile-command",
		"WORKLOAD_CMD":                        "hostile-command",
	}
	projected := CLILookupEnvironment(func(key string) (string, bool) {
		value, present := values[key]
		return value, present
	})
	got := envMap(t, projected)

	for key, value := range values {
		switch key {
		case "ENV_FILE", "PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST", "PGWORKBENCH_BENCHMARK_CAPSULE_ROOT", "EXPERIMENT_BEFORE_SHELL", "WORKLOAD_CMD":
			if _, exists := got[key]; exists {
				t.Fatalf("unowned capability %s leaked into utility CLI environment: %#v", key, projected)
			}
		default:
			if got[key] != value {
				t.Fatalf("utility runtime input %s=%q, want %q (all=%#v)", key, got[key], value, projected)
			}
		}
	}
	if len(got) != len(values)-5 {
		t.Fatalf("utility CLI environment has unexpected entries: %#v", projected)
	}
}

func TestCLILookupEnvironmentPreservesExplicitEmptyOverrides(t *testing.T) {
	present := map[string]string{
		"ENV_FILE":                  "",
		"PGWORKBENCH_NATIVE_BINDIR": "",
		"POSTGRES_PORT":             "",
	}
	projected := CLILookupEnvironment(func(key string) (string, bool) {
		value, ok := present[key]
		return value, ok
	})
	got := envMap(t, projected)
	for key := range present {
		if key == "ENV_FILE" {
			if _, exists := got[key]; exists {
				t.Fatalf("host-selected ENV_FILE was projected: %#v", projected)
			}
			continue
		}
		if value, exists := got[key]; !exists || value != "" {
			t.Fatalf("explicit empty %s was not preserved: %#v", key, projected)
		}
	}
	if len(got) != len(present)-1 {
		t.Fatalf("unset variables were projected: %#v", projected)
	}
}

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
		"ENV_FILE":                 ".env.example",
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

func TestRunUtilityTestUsesPreparedExperimentRunnerByDefault(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", strings.Join([]string{
		"UTILITY_TEST_NAME=smoke",
		"UTILITY_TEST_WORKLOAD_SPEC=utility/smoke",
		`UTILITY_TEST_PROFILE_SIZE="${PROFILE_SIZE:-small}"`,
		`UTILITY_TEST_METRICS_SAMPLES="${METRICS_SAMPLES:-1}"`,
		"",
	}, "\n"))
	runtimeValues := map[string]string{
		"PGWORKBENCH_NATIVE_BINDIR": "/opt/postgres/bin",
		"POSTGRES_HOST":             "127.0.0.1",
		"POSTGRES_PORT":             "59433",
		"PROFILE_SIZE":              "medium",
		"METRICS_SAMPLES":           "3",
		"UTILITY_TEST_SNAPSHOT":     "0",
	}

	called := false
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime:       "native",
		RunID:         "prepared-run",
		EngineVersion: "0.2.0",
		EngineCommit:  "0123456789abcdef0123456789abcdef01234567",
		BinaryPath:    "/opt/pgworkbench",
		Env: CLILookupEnvironment(func(key string) (string, bool) {
			value, present := runtimeValues[key]
			return value, present
		}),
		Getenv: func(string) string { return "" },
		RunExperiment: func(gotRoot string, spec speccatalog.Spec, options experimentrun.Options) (experimentrun.Result, error) {
			called = true
			if gotRoot != root || spec.Kind != "experiment" || spec.ID != "utility/smoke" {
				t.Fatalf("unexpected prepared spec: root=%q spec=%#v", gotRoot, spec)
			}
			canonicalRoot, canonicalErr := filepath.EvalSymlinks(root)
			if canonicalErr != nil {
				t.Fatal(canonicalErr)
			}
			if filepath.Dir(spec.Path) != filepath.Join(canonicalRoot, ".tmp", "utility-tests") {
				t.Fatalf("prepared spec escaped generated directory: %s", spec.Path)
			}
			if got := spec.Values["EXPERIMENT_PROFILE_SIZE"]; got != "${PROFILE_SIZE:-small}" {
				t.Fatalf("prepared profile override expression changed: %q", got)
			}
			if got := spec.Values["EXPERIMENT_METRICS_SAMPLES"]; got != "${METRICS_SAMPLES:-1}" {
				t.Fatalf("prepared metrics override expression changed: %q", got)
			}
			if options.Runtime != "native" || options.RunID != "prepared-run" || !options.ExactEnvironment || options.EngineVersion != "0.2.0" || options.EngineCommit == "" || options.BinaryPath != "/opt/pgworkbench" {
				t.Fatalf("prepared runner options lost identity: %#v", options)
			}
			values := envMap(t, options.Env)
			for key, want := range runtimeValues {
				if values[key] != want {
					t.Fatalf("prepared runtime environment %s=%q, want %q", key, values[key], want)
				}
			}
			for key, want := range map[string]string{
				"ENV_FILE":                 ".env.example",
				ExperimentSpecScopeEnv:     UtilityDerivedSpecScope,
				DerivedExperimentIDEnv:     "utility/smoke",
				SourceSpecKindEnv:          UtilityTestSourceKind,
				SourceSpecIDEnv:            "smoke",
				SourceSpecRefEnv:           "utility-tests/smoke.env",
				"PGWORKBENCH_PACK_ID":      "",
				"PGWORKBENCH_PACK_VERSION": "",
				"PGWORKBENCH_PACK_DIGEST":  "",
			} {
				if values[key] != want {
					t.Fatalf("prepared environment %s=%q, want %q", key, values[key], want)
				}
			}
			if _, exists := values["PGWORKBENCH_SUPERVISED"]; exists {
				t.Fatal("utility translator, rather than experiment runner, claimed supervision")
			}
			return experimentrun.Result{
				SchemaVersion: experimentrun.SchemaVersion,
				Command:       []string{"/opt/pgworkbench-internal", "__prepared", spec.Path},
				ExitCode:      0,
				Status:        "passed",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || !result.Passed() {
		t.Fatalf("prepared runner was not used successfully: called=%t result=%#v", called, result)
	}
	if got := strings.Join(result.Command, "\x00"); got != strings.Join([]string{"/opt/pgworkbench-internal", "__prepared", result.ExperimentSpec}, "\x00") {
		t.Fatalf("utility result did not report the actually executed prepared command: %#v", result.Command)
	}
}

func TestRunUtilityTestBindsGeneratedAdapterAndSourceDigestToSelectedBytes(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	const selected = "UTILITY_TEST_NAME=selected-A\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n"
	const replacement = "UTILITY_TEST_NAME=replacement-B\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n"
	sourcePath := filepath.Join(root, "utility-tests", "smoke.env")
	writeSpec(t, root, "utility-tests/smoke.env", selected)

	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		RunID:  "source-snapshot-binding",
		Getenv: func(string) string { return "" },
		RunCommand: func(_ string, command []string, env []string, _, _ io.Writer) CommandResult {
			if err := os.WriteFile(sourcePath, []byte(replacement), 0o644); err != nil {
				t.Fatal(err)
			}
			values := envMap(t, env)
			if values[SourceSpecDigestEnv] != evidence.DigestBytes([]byte(selected)) {
				t.Fatalf("source digest drifted from selected bytes: %#v", values)
			}
			generated, err := os.ReadFile(command[2])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(generated), "EXPERIMENT_NAME='utility: selected-A'") || strings.Contains(string(generated), "replacement-B") {
				t.Fatalf("generated adapter drifted to replacement bytes:\n%s", generated)
			}
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil || !result.Passed() || result.UtilityTestName != "selected-A" {
		t.Fatalf("utility source snapshot was not retained: result=%#v err=%v", result, err)
	}
}

func TestRunUtilityTestRequiresExplicitNativeToolDirectory(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")

	_, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "native",
		Getenv:  func(string) string { return "" },
		RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			t.Fatal("native utility started without an explicit tool directory")
			return CommandResult{}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires PGWORKBENCH_NATIVE_BINDIR or PG_INSTALL_DIR") {
		t.Fatalf("native PATH fallback was accepted: %v", err)
	}
}

func TestRunUtilityTestKeepsUnknownExitCodeOnPreparedPreflightFailure(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "utility-tests/smoke.env", "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n")

	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		RunID:  "prepared-preflight-failure",
		Getenv: func(string) string { return "" },
		RunExperiment: func(string, speccatalog.Spec, experimentrun.Options) (experimentrun.Result, error) {
			return experimentrun.Result{}, errors.New("prepared runner rejected input before execution")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "rejected input before execution") {
		t.Fatalf("prepared preflight failure was lost: result=%#v err=%v", result, err)
	}
	if result.Status != "failed" || result.ExitCode != -1 {
		t.Fatalf("preflight failure claimed a successful process exit: %#v", result)
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
			"PGWORKBENCH_NATIVE_BINDIR=/opt/postgres/bin",
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
		Env: []string{"PGWORKBENCH_RUNTIME=native", "PGWORKBENCH_NATIVE_BINDIR=/opt/postgres/bin", "UTILITY_TEST_RUN_ID=env-run"},
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
		{Runtime: "native", RunID: "../escape", Env: []string{"PGWORKBENCH_NATIVE_BINDIR=/opt/postgres/bin"}},
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
