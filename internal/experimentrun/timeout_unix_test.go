//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package experimentrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestRunTimeoutKillsProcessGroupAndPublishesFailedVerdict(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	script := `#!/usr/bin/env bash
set -eu
printf '%s\n' "${EXPERIMENT_RUN_ID-unset}" > "__TEST_ROOT__/observed-run-id"
run_dir="__TEST_ROOT__/runs/timeout-run"
mkdir -p "$run_dir"
printf '%s\n' \
  'schema_version="pgworkbench.run-manifest/v1"' \
  'artifact_type="pgworkbench.run-manifest"' \
  'run_id="timeout-run"' \
  'started_at="2026-08-12T10:00:00Z"' \
  'experiment_spec_id="smoke"' \
  'experiment_spec_digest="sha256:__SPEC_DIGEST__"' \
  'experiment_identity_digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' \
  'runtime_fingerprint_status="unavailable"' \
  > "$run_dir/manifest.env"
(
  trap '' TERM
  while :; do sleep 3600; done
) &
child="$!"
printf '%s\n' "$child" > "__TEST_ROOT__/timeout-child.pid"
trap '' TERM
wait "$child"
	`
	script = strings.ReplaceAll(script, "__TEST_ROOT__", root)
	specDigest, err := fileSHA256(filepath.Join(root, "experiments", "smoke.env"))
	if err != nil {
		t.Fatal(err)
	}
	script = strings.ReplaceAll(script, "__SPEC_DIGEST__", specDigest)
	writeFixture(t, root, "scripts/run_experiment.sh", script, 0o755)

	const executionTimeout = 10 * time.Second
	var stdout, stderr bytes.Buffer
	started := time.Now()
	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime: "native",
		RunID:   "timeout-run",
		// Process startup can be delayed while the full package graph runs in
		// parallel. Keep a finite deadline with enough scheduler headroom so this
		// test exercises timeout handling after the fixture is actually running.
		ExecutionTimeout: executionTimeout,
		CleanupGrace:     150 * time.Millisecond,
		Stdout:           &stdout,
		Stderr:           &stderr,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out after 10s") {
		t.Fatalf("expected timeout error, result=%#v err=%v stdout=%q stderr=%q", result, err, stdout.String(), stderr.String())
	}
	if elapsed := time.Since(started); elapsed > 12*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
	if result.Status != "failed" || !result.TimedOut || result.ExitCode != TimeoutExitCode || (result.TerminationSignal != "SIGTERM" && result.TerminationSignal != "SIGKILL") {
		t.Fatalf("unexpected timeout result: %#v", result)
	}
	observedRunID, observedErr := readFileWithin(filepath.Join(root, "observed-run-id"), 10*time.Second)
	if observedErr != nil || strings.TrimSpace(string(observedRunID)) != "timeout-run" {
		t.Fatalf("runner environment was not delivered: value=%q err=%v stdout=%q stderr=%q", observedRunID, observedErr, stdout.String(), stderr.String())
	}

	content, readErr := os.ReadFile(filepath.Join(result.RunDir, "verdict.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var verdict runstate.Verdict
	if jsonErr := json.Unmarshal(content, &verdict); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if verdict.Status != runstate.VerdictStatusFailed || verdict.WorkloadExit != TimeoutExitCode || !strings.Contains(verdict.Message, "timed out after 10000 ms") || !strings.Contains(verdict.Message, result.TerminationSignal) {
		t.Fatalf("unexpected timeout verdict: %#v", verdict)
	}

	pidContent, readErr := os.ReadFile(filepath.Join(root, "timeout-child.pid"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidContent)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	deadline := time.Now().Add(time.Second)
	for {
		killErr := syscall.Kill(pid, 0)
		if errors.Is(killErr, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d survived process-group timeout (kill -0: %v)", pid, killErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readFileWithin(path string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		content, err := os.ReadFile(path)
		if err == nil || !os.IsNotExist(err) || time.Now().After(deadline) {
			return content, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDefaultRunCommandEscalatesToKillForTermIgnoringGroup(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "helper-child.pid")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := defaultRunCommand(root,
		[]string{executable, "-test.run=TestTimeoutProcessHelper"},
		[]string{"PGWORKBENCH_TIMEOUT_HELPER=1", "PGWORKBENCH_TIMEOUT_HELPER_PID_FILE=" + pidFile},
		io.Discard,
		io.Discard,
		time.Second,
		100*time.Millisecond,
	)
	if !result.TimedOut || result.ExitCode != TimeoutExitCode || result.TerminationSignal != "SIGKILL" || result.Err == nil {
		t.Fatalf("unexpected escalation result: %#v", result)
	}
	pidContent, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidContent)))
	if err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, pid)
}

func TestDefaultRunCommandFailsClosedOnResidualDescendant(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "residual-child.pid")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	started := time.Now()
	result := defaultRunCommand(root,
		[]string{executable, "-test.run=TestTimeoutProcessHelper"},
		[]string{"PGWORKBENCH_TIMEOUT_HELPER=1", "PGWORKBENCH_TIMEOUT_HELPER_LEADER_EXIT=1", "PGWORKBENCH_TIMEOUT_HELPER_PID_FILE=" + pidFile},
		devNull,
		devNull,
		2*time.Second,
		100*time.Millisecond,
	)
	if time.Since(started) > time.Second {
		t.Fatalf("residual descendant cleanup exceeded its grace: %#v", result)
	}
	if result.TimedOut || result.ExitCode != -1 || result.TerminationSignal != "SIGKILL" || result.Err == nil || !strings.Contains(result.Err.Error(), "live descendants") {
		t.Fatalf("unexpected residual descendant result: %#v", result)
	}
	pidContent, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidContent)))
	if err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, pid)
}

func TestTimeoutProcessHelper(t *testing.T) {
	if os.Getenv("PGWORKBENCH_TIMEOUT_HELPER") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	if os.Getenv("PGWORKBENCH_TIMEOUT_HELPER_CHILD") != "1" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(91)
		}
		child := exec.Command(executable, "-test.run=TestTimeoutProcessHelper")
		child.Env = append(os.Environ(), "PGWORKBENCH_TIMEOUT_HELPER_CHILD=1")
		if err := child.Start(); err != nil {
			os.Exit(92)
		}
		if err := os.WriteFile(os.Getenv("PGWORKBENCH_TIMEOUT_HELPER_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)+"\n"), 0o644); err != nil {
			os.Exit(93)
		}
		if os.Getenv("PGWORKBENCH_TIMEOUT_HELPER_LEADER_EXIT") == "1" {
			return
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		killErr := syscall.Kill(pid, 0)
		if errors.Is(killErr, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d survived process-group termination (kill -0: %v)", pid, killErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
