package experimentrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestRunBuildsNativeResultAndIdentityEnv(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME='smoke test'\nEXPERIMENT_TOPOLOGY=single\n")

	var seenCommand []string
	var seenEnv []string
	var executionSpecPath string
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime:       "native",
		PackID:        "builtin",
		PackVersion:   "0.2.0",
		PackDigest:    "sha256:pack",
		EngineVersion: "0.2.0",
		EngineCommit:  "0123456789abcdef0123456789abcdef01234567",
		BinaryPath:    "/opt/pgworkbench",
		Env:           []string{"PGBENCH_CLIENTS=64", "PGBENCH_THREADS=8"},
		Now: fixedTimes(
			time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC),
			time.Date(2026, 8, 12, 10, 11, 13, 250_000_000, time.UTC),
		),
		RunCommand: func(root string, command []string, env []string, stdout, stderr io.Writer) CommandResult {
			seenCommand = append([]string(nil), command...)
			seenEnv = append([]string(nil), env...)
			executionSpecPath = environmentLookup(env)("PGWORKBENCH_EXECUTION_SPEC_FILE")
			content, readErr := os.ReadFile(executionSpecPath)
			if readErr != nil {
				t.Fatalf("read execution spec snapshot: %v", readErr)
			}
			if string(content) != "EXPERIMENT_NAME='smoke test'\nEXPERIMENT_TOPOLOGY=single\n" {
				t.Fatalf("unexpected execution spec snapshot: %q", content)
			}
			info, statErr := os.Lstat(executionSpecPath)
			if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
				t.Fatalf("unsafe execution spec snapshot: info=%v err=%v", info, statErr)
			}
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed() || result.Runtime != "native" || result.RunID != "smoke-20260812_101112" || result.DurationMS != 1250 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.ExecutionTimeoutMS != DefaultExecutionTimeout.Milliseconds() || result.CleanupGraceMS != DefaultCleanupGrace.Milliseconds() || result.TimedOut || result.TerminationSignal != "" || result.ContainmentStatus != "" {
		t.Fatalf("unexpected execution deadline evidence: %#v", result)
	}
	if result.SchemaVersion != SchemaVersion || len(result.SpecSHA256) != 64 || result.PackDigest != "sha256:pack" || result.EngineVersion != "0.2.0" || result.EngineCommit != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("missing identity: %#v", result)
	}
	if executionSpecPath == "" {
		t.Fatal("runner did not select an immutable execution spec snapshot")
	}
	if _, err := os.Lstat(executionSpecPath); !os.IsNotExist(err) {
		t.Fatalf("execution spec snapshot was not removed: %v", err)
	}
	resolvedSpec, err := filepath.EvalSymlinks(filepath.Join(root, "experiments", "smoke.env"))
	if err != nil {
		t.Fatal(err)
	}
	wantCommand := []string{filepath.Join(root, "scripts", "run_experiment.sh"), internalRunAction, resolvedSpec}
	if strings.Join(seenCommand, "\x00") != strings.Join(wantCommand, "\x00") {
		t.Fatalf("unexpected command: %#v", seenCommand)
	}
	for _, want := range []string{"BASHOPTS=", "BASH_ENV=/dev/null", "SHELLOPTS=", "PGWORKBENCH_SUPERVISED=1", "PGWORKBENCH_RUNTIME=native", "PGWORKBENCH_PACK_DIGEST=sha256:pack", "PGWORKBENCH_ENGINE_VERSION=0.2.0", "PGWORKBENCH_ENGINE_COMMIT=0123456789abcdef0123456789abcdef01234567", "PGWORKBENCH_EXECUTION_TIMEOUT=6h0m0s", "PGWORKBENCH_EXECUTION_TIMEOUT_SECONDS=21600", "PGWORKBENCH_CLEANUP_GRACE=15s", "PGWORKBENCH_CLEANUP_GRACE_SECONDS=15", "EXPERIMENT_RUN_ID=smoke-20260812_101112", "PGWORKBENCH_BIN=/opt/pgworkbench", "PGBENCH_CLIENTS=64", "PGBENCH_THREADS=8"} {
		if !contains(seenEnv, want) {
			t.Fatalf("missing %q in env %#v", want, seenEnv)
		}
	}
}

func TestRunBindsPlanDigestAndExecutionToSelectedBytes(t *testing.T) {
	root := t.TempDir()
	const selected = "EXPERIMENT_NAME='selected A'\nEXPERIMENT_TOPOLOGY=single\n"
	const replacement = "EXPERIMENT_NAME='replacement B'\nEXPERIMENT_TOPOLOGY=single\n"
	writeExperiment(t, root, "experiments/smoke.env", selected)
	originalPath := filepath.Join(root, "experiments", "smoke.env")
	resolvedOriginalPath, err := filepath.EvalSymlinks(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	var executionSpecPath string

	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "native",
		RunID:   "snapshot-binding",
		RunCommand: func(_ string, command []string, env []string, _, _ io.Writer) CommandResult {
			if err := os.WriteFile(originalPath, []byte(replacement), 0o644); err != nil {
				t.Fatal(err)
			}
			executionSpecPath = environmentLookup(env)("PGWORKBENCH_EXECUTION_SPEC_FILE")
			content, err := os.ReadFile(executionSpecPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != selected {
				t.Fatalf("shell execution snapshot drifted to replacement bytes: %q", content)
			}
			if command[2] != resolvedOriginalPath {
				t.Fatalf("portable spec identity path changed: %#v", command)
			}
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExperimentName != "selected A" || result.SpecSHA256 != bytesSHA256([]byte(selected)) || !result.Passed() {
		t.Fatalf("result was not bound to selected bytes: %#v", result)
	}
	if content, err := os.ReadFile(originalPath); err != nil || string(content) != replacement {
		t.Fatalf("replacement fixture was not installed: content=%q err=%v", content, err)
	}
	if _, err := os.Lstat(executionSpecPath); !os.IsNotExist(err) {
		t.Fatalf("execution spec snapshot was not removed: %v", err)
	}
}

func TestRunPreparedKeepsGeneratedPathOffPublicCatalog(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=ordinary\n")
	preparedPath := filepath.Join(root, ".tmp", "utility-tests", "prepared.env")
	writeFixture(t, root, ".tmp/utility-tests/prepared.env", "EXPERIMENT_NAME='prepared utility'\n")
	prepared := speccatalog.Spec{
		Kind:   "experiment",
		ID:     "utility/smoke",
		Path:   preparedPath,
		Values: map[string]string{"EXPERIMENT_NAME": "prepared utility"},
	}
	var seenEnv []string
	var executionSpecPath string
	result, err := RunPrepared(root, prepared, Options{
		Runtime: "native",
		RunID:   "prepared-run",
		RunCommand: func(_ string, command []string, env []string, _, _ io.Writer) CommandResult {
			if len(command) != 3 || command[1] != internalRunAction || command[2] != preparedPath {
				t.Fatalf("prepared path changed: %#v", command)
			}
			seenEnv = append([]string(nil), env...)
			executionSpecPath = environmentLookup(env)("PGWORKBENCH_EXECUTION_SPEC_FILE")
			content, err := os.ReadFile(executionSpecPath)
			if err != nil || string(content) != "EXPERIMENT_NAME='prepared utility'\n" {
				t.Fatalf("prepared execution snapshot mismatch: content=%q err=%v", content, err)
			}
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExperimentSpec != "utility/smoke" || result.SpecPath != preparedPath || !result.Passed() {
		t.Fatalf("unexpected prepared result: %#v", result)
	}
	if !contains(seenEnv, "PGWORKBENCH_SUPERVISED=1") {
		t.Fatalf("prepared run did not receive supervision marker: %#v", seenEnv)
	}
	if executionSpecPath == "" {
		t.Fatal("prepared utility run did not receive an execution snapshot")
	}
	if _, err := os.Lstat(executionSpecPath); !os.IsNotExist(err) {
		t.Fatalf("prepared execution snapshot was not removed: %v", err)
	}
	if _, err := Run(root, speccatalog.New(root), preparedPath, Options{Runtime: "native"}); err == nil || !strings.Contains(err.Error(), "outside scenario pack experiments") {
		t.Fatalf("prepared path became publicly resolvable: %v", err)
	}
}

func TestRunResolvesPositiveExecutionDeadlines(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	var seenEnv []string
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "docker",
		Getenv: func(name string) string {
			switch name {
			case "PGWORKBENCH_EXECUTION_TIMEOUT":
				return "90s"
			case "PGWORKBENCH_CLEANUP_GRACE":
				return "2500ms"
			default:
				return ""
			}
		},
		RunCommand: func(root string, command []string, env []string, stdout, stderr io.Writer) CommandResult {
			seenEnv = append([]string(nil), env...)
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionTimeoutMS != 90_000 || result.CleanupGraceMS != 2_500 {
		t.Fatalf("unexpected resolved deadlines: %#v", result)
	}
	for _, want := range []string{"PGWORKBENCH_EXECUTION_TIMEOUT=1m30s", "PGWORKBENCH_EXECUTION_TIMEOUT_SECONDS=90", "PGWORKBENCH_CLEANUP_GRACE=2.5s", "PGWORKBENCH_CLEANUP_GRACE_SECONDS=3"} {
		if !contains(seenEnv, want) {
			t.Fatalf("missing %q in env %#v", want, seenEnv)
		}
	}
}

func TestRunUsesSpecTimeoutAndEnvironmentOverride(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\nEXPERIMENT_TIMEOUT=45m\n")
	for _, test := range []struct {
		name   string
		getenv func(string) string
		wantMS int64
	}{
		{name: "spec", getenv: func(string) string { return "" }, wantMS: (45 * time.Minute).Milliseconds()},
		{name: "environment override", getenv: func(key string) string {
			if key == "PGWORKBENCH_EXECUTION_TIMEOUT" {
				return "2h"
			}
			return ""
		}, wantMS: (2 * time.Hour).Milliseconds()},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Run(root, speccatalog.New(root), "smoke", Options{
				Runtime: "docker",
				Getenv:  test.getenv,
				RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
					return CommandResult{ExitCode: 0}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.ExecutionTimeoutMS != test.wantMS {
				t.Fatalf("execution timeout=%dms, want %dms", result.ExecutionTimeoutMS, test.wantMS)
			}
		})
	}
}

func TestRunRejectsInvalidExecutionDeadlinesBeforeStarting(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	for name, options := range map[string]Options{
		"negative explicit timeout":   {ExecutionTimeout: -time.Second},
		"sub-second explicit timeout": {ExecutionTimeout: 500 * time.Millisecond},
		"zero environment timeout": {
			Getenv: func(key string) string {
				if key == "PGWORKBENCH_EXECUTION_TIMEOUT" {
					return "0s"
				}
				return ""
			},
		},
		"invalid environment grace": {
			Getenv: func(key string) string {
				if key == "PGWORKBENCH_CLEANUP_GRACE" {
					return "soon"
				}
				return ""
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			options.RunCommand = func(string, []string, []string, io.Writer, io.Writer) CommandResult {
				called = true
				return CommandResult{}
			}
			if _, err := Run(root, speccatalog.New(root), "smoke", options); err == nil {
				t.Fatal("expected deadline validation error")
			}
			if called {
				t.Fatal("command started after deadline validation failed")
			}
		})
	}
}

func TestRunUsesExplicitUnverifiedEngineIdentity(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	var seenEnv []string
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "docker",
		RunCommand: func(root string, command []string, env []string, stdout, stderr io.Writer) CommandResult {
			seenEnv = append([]string(nil), env...)
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.EngineVersion != "unverified" || result.EngineCommit != "unverified" {
		t.Fatalf("unexpected default engine identity: %#v", result)
	}
	for _, want := range []string{"PGWORKBENCH_ENGINE_VERSION=unverified", "PGWORKBENCH_ENGINE_COMMIT=unverified"} {
		if !contains(seenEnv, want) {
			t.Fatalf("missing %q in env %#v", want, seenEnv)
		}
	}
}

func TestRunRejectsUnsupportedRuntimeAndNativeTopology(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/replica.env", "EXPERIMENT_NAME=replica\nEXPERIMENT_TOPOLOGY=primary-replica\n")
	catalog := speccatalog.New(root)
	if _, err := Run(root, catalog, "replica", Options{Runtime: "podman"}); err == nil || !strings.Contains(err.Error(), "unsupported runtime") {
		t.Fatalf("expected runtime error, got %v", err)
	}
	if _, err := Run(root, catalog, "replica", Options{Runtime: "native"}); err == nil || !strings.Contains(err.Error(), "supports topology single") {
		t.Fatalf("expected topology error, got %v", err)
	}
}

func TestRunRecordsFailureAndRendersJSON(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/fail.env", "EXPERIMENT_NAME=fail\n")
	result, err := Run(root, speccatalog.New(root), "fail", Options{
		Runtime: "docker",
		RunID:   "fixed-run",
		RunCommand: func(root string, command []string, env []string, stdout, stderr io.Writer) CommandResult {
			return CommandResult{ExitCode: 7, Err: fmt.Errorf("exit status 7")}
		},
	})
	if err == nil || result.Status != "failed" || result.ExitCode != 7 {
		t.Fatalf("unexpected failure: result=%#v err=%v", result, err)
	}
	var out bytes.Buffer
	if err := RenderJSON(&out, result); err != nil {
		t.Fatal(err)
	}
	var payload Result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RunID != "fixed-run" || payload.Status != "failed" || payload.ExecutionTimeoutMS != DefaultExecutionTimeout.Milliseconds() || payload.CleanupGraceMS != DefaultCleanupGrace.Milliseconds() || payload.TimedOut {
		t.Fatalf("unexpected JSON: %s", out.String())
	}
}

func TestRunRewritesPassedVerdictWhenSnapshotCleanupFails(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	var injectedPath string
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "docker",
		RunID:   "snapshot-cleanup-failure",
		RunCommand: func(root string, _ []string, env []string, _, _ io.Writer) CommandResult {
			lookup := environmentLookup(env)
			executionSpec := lookup("PGWORKBENCH_EXECUTION_SPEC_FILE")
			injectedPath = filepath.Join(filepath.Dir(executionSpec), "unexpected-entry")
			if err := os.WriteFile(injectedPath, []byte("retain"), 0o600); err != nil {
				t.Fatal(err)
			}
			runDir := filepath.Join(root, "runs", "snapshot-cleanup-failure")
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := strings.Join([]string{
				"run_id=snapshot-cleanup-failure",
				"started_at=2026-08-14T00:00:00Z",
				"experiment_spec_id=smoke",
				"experiment_spec_digest=sha256:" + lookup("EXPERIMENT_SPEC_SHA256"),
				"experiment_identity_digest=sha256:" + strings.Repeat("a", 64),
			}, "\n") + "\n"
			if err := os.WriteFile(filepath.Join(runDir, "manifest.env"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runDir, "verdict.env"), []byte("status=passed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runDir, "verdict.json"), []byte(`{"status":"passed"}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return CommandResult{ExitCode: 0}
		},
	})
	if injectedPath != "" {
		dir := filepath.Dir(injectedPath)
		t.Cleanup(func() {
			_ = os.Remove(injectedPath)
			_ = os.Remove(dir)
		})
	}
	if err == nil || result.Status != "failed" || result.ExitCode != 0 || result.TerminationSignal != "" {
		t.Fatalf("snapshot cleanup failure was not terminal: result=%#v err=%v", result, err)
	}
	content, readErr := os.ReadFile(filepath.Join(result.RunDir, "verdict.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var verdict runstate.Verdict
	if err := json.Unmarshal(content, &verdict); err != nil {
		t.Fatal(err)
	}
	if verdict.Status != runstate.VerdictStatusFailed || verdict.WorkloadExit != -1 || verdict.Message != "experiment runner post-shell finalization failed" {
		t.Fatalf("runner left a transient passed verdict after cleanup failure: %#v", verdict)
	}
}

func TestContainmentStatusMappingAndRendering(t *testing.T) {
	for _, test := range []struct {
		name   string
		result CommandResult
		want   string
	}{
		{name: "not applicable", result: CommandResult{}, want: ""},
		{name: "confirmed", result: CommandResult{TerminationSignal: "SIGKILL", ContainmentConfirmed: true}, want: ContainmentStatusConfirmed},
		{name: "unconfirmed", result: CommandResult{TerminationSignal: "SIGKILL"}, want: ContainmentStatusUnconfirmed},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := commandContainmentStatus(test.result); got != test.want {
				t.Fatalf("containment status=%q, want %q", got, test.want)
			}
		})
	}

	result := Result{
		ExperimentSpec:    "smoke",
		Runtime:           "native",
		RunID:             "containment-run",
		RunDir:            "/tmp/containment-run",
		ExitCode:          TimeoutExitCode,
		TimedOut:          true,
		TerminationSignal: "SIGKILL",
		ContainmentStatus: ContainmentStatusUnconfirmed,
		Status:            "failed",
	}
	var textOutput bytes.Buffer
	if err := Render(&textOutput, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOutput.String(), "containment_status=unconfirmed") {
		t.Fatalf("text output omitted containment status: %q", textOutput.String())
	}
	textOutput.Reset()
	if err := Render(&textOutput, Result{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textOutput.String(), "containment_status") {
		t.Fatalf("text output published inapplicable containment status: %q", textOutput.String())
	}
	var jsonOutput bytes.Buffer
	if err := RenderJSON(&jsonOutput, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOutput.String(), `"containment_status": "unconfirmed"`) {
		t.Fatalf("JSON output omitted containment status: %s", jsonOutput.String())
	}
	jsonOutput.Reset()
	if err := RenderJSON(&jsonOutput, Result{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jsonOutput.String(), "containment_status") {
		t.Fatalf("JSON output published inapplicable containment status: %s", jsonOutput.String())
	}
}

func TestRunDoesNotAcceptZeroExitWithTimeoutEvidence(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "docker",
		RunCommand: func(string, []string, []string, io.Writer, io.Writer) CommandResult {
			return CommandResult{ExitCode: 0, TimedOut: true}
		},
	})
	if err == nil || result.Status != "failed" || !result.TimedOut {
		t.Fatalf("timeout evidence was accepted as passed: result=%#v err=%v", result, err)
	}
}

func TestRunRejectsUnsafeRunID(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	for _, runID := range []string{".", "..", "../escape", "nested/run"} {
		if _, err := Run(root, speccatalog.New(root), "smoke", Options{RunID: runID}); err == nil || !strings.Contains(err.Error(), "invalid run id") {
			t.Fatalf("expected invalid run id %q, got %v", runID, err)
		}
	}
}

func fixedTimes(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeExperiment(t *testing.T, root string, rel string, content string) {
	t.Helper()
	writeFixture(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_DESCRIPTION='single postgres'\n")
	writeFixture(t, root, "topologies/primary-replica.env", "TOPOLOGY_NAME=primary-replica\nTOPOLOGY_DESCRIPTION='primary and replica'\n")
	writeFixture(t, root, "configs/default/postgresql.conf", "# defaults\n")
	writeFixture(t, root, "scripts/run_experiment.sh", "#!/usr/bin/env bash\n", 0o755)
	writeFixture(t, root, rel, content)
}

func writeFixture(t *testing.T, root string, rel string, content string, modes ...os.FileMode) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if len(modes) > 0 {
		mode = modes[0]
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
