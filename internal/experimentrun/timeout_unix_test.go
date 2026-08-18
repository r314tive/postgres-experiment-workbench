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
	if result.Status != "failed" || !result.TimedOut || result.ExitCode != TimeoutExitCode ||
		(result.TerminationSignal != "SIGTERM" && result.TerminationSignal != "SIGKILL") || result.ContainmentStatus != ContainmentStatusConfirmed {
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
	if !result.TimedOut || result.ExitCode != TimeoutExitCode || result.TerminationSignal != "SIGKILL" || !result.ContainmentConfirmed || result.Err == nil {
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
	// -race instrumentation and scheduler contention can add dispatch latency
	// around the 100 ms cleanup grace; the runner must still remain narrowly
	// bounded rather than inheriting the multi-second execution timeout.
	if time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("residual descendant cleanup exceeded its grace: %#v", result)
	}
	if result.TimedOut || result.ExitCode != -1 ||
		(result.TerminationSignal != "SIGTERM" && result.TerminationSignal != "SIGKILL") ||
		!result.ContainmentConfirmed || result.Err == nil || !strings.Contains(result.Err.Error(), "live descendants") {
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

func TestRunResidualDescendantReplacesPassedShellVerdict(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	specDigest, err := fileSHA256(filepath.Join(root, "experiments", "smoke.env"))
	if err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
set -eu
run_dir="__TEST_ROOT__/runs/residual-run"
mkdir -p "$run_dir"
printf '%s\n' \
  'schema_version="pgworkbench.run-manifest/v1"' \
  'artifact_type="pgworkbench.run-manifest"' \
  'run_id="residual-run"' \
  'started_at="2026-08-12T10:00:00Z"' \
  'experiment_spec_id="smoke"' \
  'experiment_spec_digest="sha256:__SPEC_DIGEST__"' \
  'experiment_identity_digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' \
  'runtime_fingerprint_status="unavailable"' \
  > "$run_dir/manifest.env"
printf 'status="passed"\n' > "$run_dir/verdict.env"
printf '{"status":"passed"}\n' > "$run_dir/verdict.json"
(
  trap '' TERM
  while :; do sleep 3600; done
) &
printf '%s\n' "$!" > "__TEST_ROOT__/residual-run-child.pid"
exit 0
`
	script = strings.ReplaceAll(script, "__TEST_ROOT__", root)
	script = strings.ReplaceAll(script, "__SPEC_DIGEST__", specDigest)
	writeFixture(t, root, "scripts/run_experiment.sh", script, 0o755)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	result, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime:      "native",
		RunID:        "residual-run",
		CleanupGrace: 100 * time.Millisecond,
		Stdout:       devNull,
		Stderr:       devNull,
	})
	if err == nil || !strings.Contains(err.Error(), "live descendants") {
		t.Fatalf("expected residual-descendant failure, result=%#v err=%v", result, err)
	}
	if result.Status != "failed" || result.TimedOut || result.ExitCode != -1 || result.TerminationSignal == "" || result.ContainmentStatus != ContainmentStatusConfirmed {
		t.Fatalf("unexpected residual-descendant result: %#v", result)
	}
	content, readErr := os.ReadFile(filepath.Join(result.RunDir, "verdict.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var verdict runstate.Verdict
	if jsonErr := json.Unmarshal(content, &verdict); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if verdict.Status != runstate.VerdictStatusFailed || verdict.WorkloadExit != -1 ||
		!strings.Contains(verdict.Message, "live descendants") || !strings.Contains(verdict.Message, result.TerminationSignal) {
		t.Fatalf("runner did not replace the transient passed verdict: %#v", verdict)
	}
	pidContent, readErr := os.ReadFile(filepath.Join(root, "residual-run-child.pid"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidContent)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	assertProcessGone(t, pid)
}

func TestRunInterruptKillsProcessGroupAndReplacesPassedVerdict(t *testing.T) {
	// Startup includes a nested test-binary exec and can be scheduler-bound when
	// the package graph runs under -race. These harness deadlines do not change
	// the runner's asserted 100 ms cleanup grace or post-KILL confirmation bound.
	const (
		fixtureStartupTimeout  = 10 * time.Second
		cleanupCompletionLimit = 5 * time.Second
	)
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	specDigest, err := fileSHA256(filepath.Join(root, "experiments", "smoke.env"))
	if err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env bash
set -eu
run_dir="__TEST_ROOT__/runs/interrupt-run"
mkdir -p "$run_dir"
printf '%s\n' \
  'schema_version="pgworkbench.run-manifest/v1"' \
  'artifact_type="pgworkbench.run-manifest"' \
  'run_id="interrupt-run"' \
  'started_at="2026-08-12T10:00:00Z"' \
  'experiment_spec_id="smoke"' \
  'experiment_spec_digest="sha256:__SPEC_DIGEST__"' \
  'experiment_identity_digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"' \
  'runtime_fingerprint_status="unavailable"' \
  > "$run_dir/manifest.env"
printf 'status="passed"\n' > "$run_dir/verdict.env"
printf '{"status":"passed"}\n' > "$run_dir/verdict.json"
(
  trap '' TERM
  while :; do sleep 3600; done
) &
child="$!"
printf '%s\n' "$child" > "__TEST_ROOT__/interrupt-child.pid"
trap '' TERM
wait "$child"
`
	script = strings.ReplaceAll(script, "__TEST_ROOT__", root)
	script = strings.ReplaceAll(script, "__SPEC_DIGEST__", specDigest)
	writeFixture(t, root, "scripts/run_experiment.sh", script, 0o755)

	interrupts := make(chan os.Signal, 1)
	subscribed := make(chan struct{})
	stopped := make(chan struct{})
	type outcome struct {
		result Result
		err    error
	}
	completed := make(chan outcome, 1)
	go func() {
		result, runErr := Run(root, speccatalog.New(root), "smoke", Options{
			Runtime:          "native",
			RunID:            "interrupt-run",
			ExecutionTimeout: 10 * time.Second,
			CleanupGrace:     100 * time.Millisecond,
			Stdout:           io.Discard,
			Stderr:           io.Discard,
			signalSubscription: func() (<-chan os.Signal, func()) {
				close(subscribed)
				return interrupts, func() { close(stopped) }
			},
		})
		completed <- outcome{result: result, err: runErr}
	}()

	select {
	case <-subscribed:
	case got := <-completed:
		t.Fatalf("runner exited before signal subscription: result=%#v err=%v", got.result, got.err)
	case <-time.After(fixtureStartupTimeout):
		t.Fatal("runner did not subscribe for termination signals")
	}
	pidContent, readErr := readFileWithin(filepath.Join(root, "interrupt-child.pid"), fixtureStartupTimeout)
	if readErr != nil {
		interrupts <- syscall.SIGINT
		<-completed
		t.Fatalf("interrupt fixture did not start: %v", readErr)
	}
	interrupts <- syscall.SIGINT

	var got outcome
	select {
	case got = <-completed:
	case <-time.After(cleanupCompletionLimit):
		t.Fatal("runner did not complete bounded interrupt cleanup")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("termination signal subscription was not stopped")
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "interrupted by SIGINT") {
		t.Fatalf("expected interrupt error, result=%#v err=%v", got.result, got.err)
	}
	if got.result.Status != "failed" || got.result.TimedOut || got.result.ExitCode != 130 ||
		got.result.TerminationSignal != "SIGKILL" || got.result.ContainmentStatus != ContainmentStatusConfirmed {
		t.Fatalf("unexpected interrupt result: %#v", got.result)
	}

	content, readErr := os.ReadFile(filepath.Join(got.result.RunDir, "verdict.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var verdict runstate.Verdict
	if jsonErr := json.Unmarshal(content, &verdict); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if verdict.Status != runstate.VerdictStatusFailed || verdict.WorkloadExit != 130 ||
		!strings.Contains(verdict.Message, "interrupted by SIGINT") ||
		!strings.Contains(verdict.Message, "cleanup signal SIGKILL attempted") ||
		!strings.Contains(verdict.Message, "process-group disappearance confirmed") {
		t.Fatalf("runner did not replace the transient passed verdict after interrupt: %#v", verdict)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidContent)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	assertProcessGone(t, pid)
}

func TestInterruptCommandResultUsesConventionalExitCodes(t *testing.T) {
	termination := processGroupTermination{
		cleanupSignal:        "SIGTERM",
		containmentConfirmed: true,
	}
	for _, test := range []struct {
		received os.Signal
		name     string
		exitCode int
	}{
		{received: syscall.SIGHUP, name: "SIGHUP", exitCode: 129},
		{received: syscall.SIGINT, name: "SIGINT", exitCode: 130},
		{received: syscall.SIGTERM, name: "SIGTERM", exitCode: 143},
	} {
		result := interruptCommandResult(test.received, termination)
		if result.ExitCode != test.exitCode || result.TimedOut || result.TerminationSignal != "SIGTERM" ||
			!result.ContainmentConfirmed ||
			result.terminationReason != commandTerminationInterrupt || result.interruptSignal != test.name ||
			result.Err == nil || !strings.Contains(result.Err.Error(), "interrupted by "+test.name) {
			t.Fatalf("unexpected %s result: %#v", test.name, result)
		}
	}
}

func TestCommandResultAfterWaitContainsInitialStatusErrorViaBoundedCleanup(t *testing.T) {
	initialStatusErr := errors.New("initial status seam failure")
	statusCalls := 0
	var signals []processSignal
	operations := processGroupOperations{
		sendSignal: func(_ *exec.Cmd, signal processSignal) error {
			signals = append(signals, signal)
			return nil
		},
		status: func(_ *exec.Cmd) (bool, error) {
			statusCalls++
			if statusCalls == 1 {
				return false, initialStatusErr
			}
			return false, nil
		},
	}

	result := commandResultAfterWait(nil, nil, 5*time.Millisecond, operations)
	if result.ExitCode != -1 || result.TerminationSignal != "SIGTERM" || result.ContainmentConfirmed ||
		result.terminationReason != commandTerminationResidual || result.Err == nil {
		t.Fatalf("unexpected post-Wait status-error result: %#v", result)
	}
	if !errors.Is(result.Err, initialStatusErr) ||
		!strings.Contains(result.Err.Error(), "initial post-exit status probe failed") {
		t.Fatalf("initial status error was not preserved as unconfirmed containment: %v", result.Err)
	}
	if len(signals) != 1 || signals[0] != processSignalTerminate {
		t.Fatalf("post-Wait status error did not enter bounded cleanup: %#v", signals)
	}
	verdictMessage, workloadExit := runnerFailureVerdictMessage(Result{
		TerminationSignal: result.TerminationSignal,
	}, result)
	if workloadExit != -1 || !strings.Contains(verdictMessage, "process-group containment not confirmed") ||
		strings.Contains(verdictMessage, "process-group disappearance confirmed") {
		t.Fatalf("post-Wait status error was misrepresented in verdict message: %q", verdictMessage)
	}
}

func TestTerminateActiveCommandConfirmsDisappearanceAfterSIGKILLViaSeam(t *testing.T) {
	var signals []processSignal
	killAttempted := false
	operations := processGroupOperations{
		sendSignal: func(_ *exec.Cmd, signal processSignal) error {
			signals = append(signals, signal)
			if signal == processSignalKill {
				killAttempted = true
			}
			return nil
		},
		status: func(_ *exec.Cmd) (bool, error) {
			return !killAttempted, nil
		},
	}

	result := terminateActiveCommandWithProcessGroupOperations(
		nil,
		nil,
		5*time.Millisecond,
		commandTerminationTimeout,
		nil,
		time.Second,
		operations,
	)
	if result.TerminationSignal != "SIGKILL" || !result.ContainmentConfirmed || result.Err == nil ||
		!strings.Contains(result.Err.Error(), "process-group disappearance confirmed after SIGKILL") {
		t.Fatalf("unexpected confirmed containment result: %#v", result)
	}
	if len(signals) != 2 || signals[0] != processSignalTerminate || signals[1] != processSignalKill {
		t.Fatalf("unexpected cleanup signals: %#v", signals)
	}
}

func TestTerminateActiveCommandFailsWhenContainmentRemainsUnconfirmedViaSeam(t *testing.T) {
	operations := processGroupOperations{
		sendSignal: func(_ *exec.Cmd, _ processSignal) error { return nil },
		status:     func(_ *exec.Cmd) (bool, error) { return true, nil },
	}
	started := time.Now()
	result := terminateActiveCommandWithProcessGroupOperations(
		nil,
		nil,
		5*time.Millisecond,
		commandTerminationTimeout,
		nil,
		time.Second,
		operations,
	)
	if result.TerminationSignal != "SIGKILL" || result.ContainmentConfirmed || result.Err == nil ||
		!strings.Contains(result.Err.Error(), "process-group containment not confirmed after SIGKILL") {
		t.Fatalf("unexpected unconfirmed containment result: %#v", result)
	}
	verdictMessage, workloadExit := runnerFailureVerdictMessage(Result{
		ExecutionTimeoutMS: 1000,
		TimedOut:           true,
		TerminationSignal:  result.TerminationSignal,
	}, result)
	if workloadExit != TimeoutExitCode || !strings.Contains(verdictMessage, "process-group containment not confirmed") ||
		strings.Contains(verdictMessage, "process-group disappearance confirmed") {
		t.Fatalf("unconfirmed containment was misrepresented in verdict message: %q", verdictMessage)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("post-KILL confirmation exceeded its bound: %s", elapsed)
	}
}

func TestTerminateActiveCommandSurfacesSignalAndConfirmationErrorsViaSeam(t *testing.T) {
	termErr := errors.New("TERM seam failure")
	killErr := errors.New("KILL seam failure")
	statusErr := errors.New("status seam failure")
	killAttempted := false
	postKillStatusCalls := 0
	operations := processGroupOperations{
		sendSignal: func(_ *exec.Cmd, signal processSignal) error {
			if signal == processSignalTerminate {
				return termErr
			}
			killAttempted = true
			return killErr
		},
		status: func(_ *exec.Cmd) (bool, error) {
			if !killAttempted {
				return true, nil
			}
			postKillStatusCalls++
			if postKillStatusCalls == 1 {
				return false, statusErr
			}
			return false, nil
		},
	}

	result := terminateActiveCommandWithProcessGroupOperations(
		nil,
		nil,
		5*time.Millisecond,
		commandTerminationInterrupt,
		syscall.SIGTERM,
		time.Second,
		operations,
	)
	if result.TerminationSignal != "SIGKILL" || result.ContainmentConfirmed || result.TimedOut || result.ExitCode != 143 || result.Err == nil {
		t.Fatalf("unexpected cleanup-error result: %#v", result)
	}
	for label, expected := range map[string]error{
		"TERM":         termErr,
		"KILL":         killErr,
		"confirmation": statusErr,
	} {
		if !errors.Is(result.Err, expected) {
			t.Fatalf("%s error was not surfaced: %v", label, result.Err)
		}
	}
	for _, fragment := range []string{
		"send SIGTERM to process group",
		"send SIGKILL to process group",
		"verify process-group disappearance after SIGKILL",
		"process-group containment not confirmed after SIGKILL because cleanup or confirmation encountered errors",
	} {
		if !strings.Contains(result.Err.Error(), fragment) {
			t.Fatalf("cleanup error does not contain %q: %v", fragment, result.Err)
		}
	}
}

func TestDefaultRunCommandStopsSignalSubscriptionWhenStartFails(t *testing.T) {
	stopped := make(chan struct{})
	result := defaultRunCommandWithBaseAndSignals(
		t.TempDir(),
		[]string{"/path/that/does/not/exist/pgworkbench-test"},
		nil,
		nil,
		io.Discard,
		io.Discard,
		time.Second,
		100*time.Millisecond,
		func() (<-chan os.Signal, func()) {
			return make(chan os.Signal), func() { close(stopped) }
		},
	)
	if result.Err == nil || result.ExitCode != -1 {
		t.Fatalf("expected command start failure, got %#v", result)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("termination signal subscription leaked after command start failed")
	}
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
