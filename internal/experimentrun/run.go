package experimentrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

const SchemaVersion = "pgworkbench.experiment-run-result/v1"

const (
	ContainmentStatusConfirmed   = "confirmed"
	ContainmentStatusUnconfirmed = "unconfirmed"
)

const internalRunAction = "__pgworkbench_internal_run_v1"

const (
	// DefaultExecutionTimeout is deliberately large enough for setup-heavy
	// experiments while still making every runner invocation finite. Callers
	// should set a tighter budget for benchmarks and CI jobs.
	DefaultExecutionTimeout = 6 * time.Hour
	DefaultCleanupGrace     = 15 * time.Second
	MinimumExecutionTimeout = time.Second
	MinimumCleanupGrace     = 100 * time.Millisecond
	TimeoutExitCode         = 124

	processGroupPollInterval        = 25 * time.Millisecond
	maximumPostKillConfirmationTime = time.Second
)

type CommandResult struct {
	ExitCode          int
	Err               error
	TimedOut          bool
	TerminationSignal string
	// ContainmentConfirmed is meaningful when TerminationSignal is non-empty.
	// It is true only after error-free cleanup and a successful status probe
	// observed that the complete process group had disappeared.
	ContainmentConfirmed bool
	terminationReason    commandTerminationReason
	interruptSignal      string
}

type commandTerminationReason string

const (
	commandTerminationNone      commandTerminationReason = ""
	commandTerminationTimeout   commandTerminationReason = "timeout"
	commandTerminationInterrupt commandTerminationReason = "interrupt"
	commandTerminationResidual  commandTerminationReason = "residual"
)

type terminationSignalSubscription func() (<-chan os.Signal, func())

type processGroupOperations struct {
	sendSignal func(*exec.Cmd, processSignal) error
	status     func(*exec.Cmd) (bool, error)
}

type processGroupTermination struct {
	cleanupSignal        string
	containmentConfirmed bool
	err                  error
}

type CommandRunner func(root string, command []string, env []string, stdout io.Writer, stderr io.Writer) CommandResult

type Options struct {
	Runtime string
	RunID   string
	Env     []string
	// ExactEnvironment removes ambient workbench/protocol variables from the
	// child. Only a small process-bootstrap allow-list plus Env and runner-owned
	// identity values reaches the experiment command.
	ExactEnvironment bool
	PackID           string
	PackVersion      string
	PackDigest       string
	EngineVersion    string
	EngineCommit     string
	BinaryPath       string
	Stdout           io.Writer
	Stderr           io.Writer
	ExecutionTimeout time.Duration
	CleanupGrace     time.Duration
	Now              func() time.Time
	Getenv           func(string) string
	RunCommand       CommandRunner
	// signalSubscription is an internal test seam. Production runs always use
	// subscribeProcessTerminationSignals immediately before the child starts.
	signalSubscription terminationSignalSubscription
}

type Result struct {
	SchemaVersion      string   `json:"schema_version"`
	ExperimentSpec     string   `json:"experiment_spec"`
	ExperimentName     string   `json:"experiment_name"`
	SpecPath           string   `json:"spec_path"`
	SpecSHA256         string   `json:"spec_sha256"`
	Runtime            string   `json:"runtime"`
	Topology           string   `json:"topology"`
	RunID              string   `json:"run_id"`
	RunDir             string   `json:"run_dir"`
	PackID             string   `json:"pack_id,omitempty"`
	PackVersion        string   `json:"pack_version,omitempty"`
	PackDigest         string   `json:"pack_digest,omitempty"`
	EngineVersion      string   `json:"engine_version"`
	EngineCommit       string   `json:"engine_commit"`
	Command            []string `json:"command"`
	StartedAt          string   `json:"started_at"`
	FinishedAt         string   `json:"finished_at"`
	DurationMS         int64    `json:"duration_ms"`
	ExitCode           int      `json:"exit_code"`
	ExecutionTimeoutMS int64    `json:"execution_timeout_ms"`
	CleanupGraceMS     int64    `json:"cleanup_grace_ms"`
	TimedOut           bool     `json:"timed_out"`
	TerminationSignal  string   `json:"termination_signal,omitempty"`
	ContainmentStatus  string   `json:"containment_status,omitempty"`
	Status             string   `json:"status"`
}

func (r Result) Passed() bool {
	return r.Status == "passed"
}

func Run(root string, catalog speccatalog.Catalog, input string, options Options) (Result, error) {
	path, id, err := catalog.Resolve("experiment", input)
	if err != nil {
		return Result{}, err
	}
	spec, content, err := readSpecSnapshot("experiment", id, path)
	if err != nil {
		return Result{}, err
	}
	if errs := catalog.ValidateSpec(spec); len(errs) > 0 {
		return Result{}, errors.Join(errs...)
	}
	plan, err := experimentplan.BuildPrepared(spec)
	if err != nil {
		return Result{}, err
	}
	return runPreparedPlan(root, plan, content, options)
}

// RunPrepared executes an internally produced experiment spec without making
// its path resolvable through the public scenario-pack catalog. The producer is
// responsible for validating the source capability and exact prepared path.
func RunPrepared(root string, spec speccatalog.Spec, options Options) (Result, error) {
	selected, content, err := readSpecSnapshot(spec.Kind, spec.ID, spec.Path)
	if err != nil {
		return Result{}, err
	}
	plan, err := experimentplan.BuildPrepared(selected)
	if err != nil {
		return Result{}, err
	}
	return runPreparedPlan(root, plan, content, options)
}

func runPreparedPlan(root string, plan experimentplan.Plan, specContent []byte, options Options) (Result, error) {
	options = withDefaults(options)
	lookup := options.Getenv
	if options.ExactEnvironment {
		lookup = environmentLookup(options.Env)
	}
	executionTimeout, err := resolvePositiveDuration(options.ExecutionTimeout, firstNonEmptyDuration(
		lookup("PGWORKBENCH_EXECUTION_TIMEOUT"),
		plan.Fields["timeout"],
	), DefaultExecutionTimeout, "execution timeout")
	if err != nil {
		return Result{}, err
	}
	cleanupGrace, err := resolvePositiveDuration(options.CleanupGrace, lookup("PGWORKBENCH_CLEANUP_GRACE"), DefaultCleanupGrace, "cleanup grace")
	if err != nil {
		return Result{}, err
	}
	options.ExecutionTimeout = executionTimeout
	options.CleanupGrace = cleanupGrace

	runtime := strings.TrimSpace(options.Runtime)
	if runtime == "" {
		runtime = strings.TrimSpace(lookup("PGWORKBENCH_RUNTIME"))
	}
	if runtime == "" {
		runtime = "docker"
	}
	if runtime != "docker" && runtime != "native" {
		return Result{}, fmt.Errorf("unsupported runtime %q: expected docker or native", runtime)
	}
	if runtime == "native" && plan.Fields["topology"] != "single" {
		return Result{}, fmt.Errorf("native runtime supports topology single; experiment %s requests %s", plan.Spec.ID, plan.Fields["topology"])
	}

	started := options.Now().UTC()
	runID := strings.TrimSpace(options.RunID)
	if runID == "" {
		runID = strings.TrimSpace(lookup("EXPERIMENT_RUN_ID"))
	}
	if runID == "" {
		runID = fmt.Sprintf("%s-%s", sanitizeID(plan.Spec.ID), started.Format("20060102_150405"))
	}
	if !validRunID(runID) {
		return Result{}, fmt.Errorf("invalid run id %q", runID)
	}

	specDigest := bytesSHA256(specContent)
	executionSpecPath, cleanupExecutionSpec, err := createExecutionSpecSnapshot(specContent)
	if err != nil {
		return Result{}, fmt.Errorf("create immutable experiment spec snapshot: %w", err)
	}
	defer func() { _ = cleanupExecutionSpec() }()
	command := []string{filepath.Join(root, "scripts", "run_experiment.sh"), internalRunAction, plan.Spec.Path}
	engineVersion := options.EngineVersion
	if strings.TrimSpace(engineVersion) == "" {
		engineVersion = lookup("PGWORKBENCH_ENGINE_VERSION")
	}
	engineVersion = runstate.NormalizeEngineVersion(engineVersion)
	engineCommit := options.EngineCommit
	if strings.TrimSpace(engineCommit) == "" {
		engineCommit = lookup("PGWORKBENCH_ENGINE_COMMIT")
	}
	engineCommit = runstate.NormalizeEngineCommit(engineCommit)
	env := append([]string(nil), options.Env...)
	exactEnvironmentMarker := "0"
	if options.ExactEnvironment {
		exactEnvironmentMarker = "1"
	}
	env = append(env,
		"BASHOPTS=",
		"BASH_ENV=/dev/null",
		"EXPERIMENT_RUN_ID="+runID,
		"EXPERIMENT_SPEC_SHA256="+specDigest,
		"PGWORKBENCH_EXACT_ENVIRONMENT="+exactEnvironmentMarker,
		"PGWORKBENCH_EXECUTION_SPEC_FILE="+executionSpecPath,
		"PGWORKBENCH_SUPERVISED=1",
		"SHELLOPTS=",
		"PGWORKBENCH_RUNTIME="+runtime,
		"PGWORKBENCH_ROOT="+root,
		"PGWORKBENCH_ENGINE_VERSION="+engineVersion,
		"PGWORKBENCH_ENGINE_COMMIT="+engineCommit,
		"PGWORKBENCH_EXECUTION_TIMEOUT="+executionTimeout.String(),
		"PGWORKBENCH_EXECUTION_TIMEOUT_SECONDS="+strconv.FormatInt(ceilSeconds(executionTimeout), 10),
		"PGWORKBENCH_CLEANUP_GRACE="+cleanupGrace.String(),
		"PGWORKBENCH_CLEANUP_GRACE_SECONDS="+strconv.FormatInt(ceilSeconds(cleanupGrace), 10),
	)
	if options.BinaryPath != "" {
		env = append(env, "PGWORKBENCH_BIN="+options.BinaryPath)
	}
	if options.PackID != "" {
		env = append(env, "PGWORKBENCH_PACK_ID="+options.PackID)
	}
	if options.PackVersion != "" {
		env = append(env, "PGWORKBENCH_PACK_VERSION="+options.PackVersion)
	}
	if options.PackDigest != "" {
		env = append(env, "PGWORKBENCH_PACK_DIGEST="+options.PackDigest)
	}

	result := Result{
		SchemaVersion:      SchemaVersion,
		ExperimentSpec:     plan.Spec.ID,
		ExperimentName:     plan.Fields["name"],
		SpecPath:           plan.Spec.Path,
		SpecSHA256:         specDigest,
		Runtime:            runtime,
		Topology:           plan.Fields["topology"],
		RunID:              runID,
		RunDir:             filepath.Join(root, "runs", runID),
		PackID:             options.PackID,
		PackVersion:        options.PackVersion,
		PackDigest:         options.PackDigest,
		EngineVersion:      engineVersion,
		EngineCommit:       engineCommit,
		Command:            append([]string(nil), command...),
		StartedAt:          started.Format(time.RFC3339),
		ExecutionTimeoutMS: ceilMilliseconds(executionTimeout),
		CleanupGraceMS:     ceilMilliseconds(cleanupGrace),
	}

	var commandResult CommandResult
	if options.RunCommand != nil {
		// Keep the test seam faithful to production: runner-owned overrides are
		// canonical and unambiguous even when no ambient base is projected.
		commandEnv := mergeEnvironment(nil, env)
		if options.ExactEnvironment {
			commandEnv = mergeEnvironment(exactEnvironmentBase(os.Environ()), env)
		}
		commandResult = options.RunCommand(root, command, commandEnv, options.Stdout, options.Stderr)
	} else if options.ExactEnvironment {
		commandResult = defaultRunCommandWithBaseAndSignals(root, command, exactEnvironmentBase(os.Environ()), env, options.Stdout, options.Stderr, executionTimeout, cleanupGrace, options.signalSubscription)
	} else {
		commandResult = defaultRunCommandWithBaseAndSignals(root, command, os.Environ(), env, options.Stdout, options.Stderr, executionTimeout, cleanupGrace, options.signalSubscription)
	}
	if cleanupErr := cleanupExecutionSpec(); cleanupErr != nil {
		commandResult.Err = errors.Join(commandResult.Err, fmt.Errorf("remove immutable experiment spec snapshot: %w", cleanupErr))
	}
	finished := options.Now().UTC()
	result.FinishedAt = finished.Format(time.RFC3339)
	result.DurationMS = maxDurationMS(finished.Sub(started))
	result.ExitCode = commandResult.ExitCode
	result.TimedOut = commandResult.TimedOut
	result.TerminationSignal = commandResult.TerminationSignal
	result.ContainmentStatus = commandContainmentStatus(commandResult)
	if commandResult.TerminationSignal != "" || (commandResult.Err != nil && commandResult.ExitCode == 0) {
		// The shell normally writes its own terminal verdict from its EXIT trap.
		// Re-publish a failed verdict after any runner-owned post-shell gate fails.
		// This closes both the brief residual-descendant case and failures while
		// removing the private execution-spec snapshot after a zero shell exit.
		if verdictErr := writeRunnerFailureVerdict(result, commandResult); verdictErr != nil {
			commandResult.Err = errors.Join(commandResult.Err, fmt.Errorf("write runner failure verdict: %w", verdictErr))
		}
	}
	if commandResult.Err == nil && commandResult.ExitCode == 0 && !commandResult.TimedOut {
		result.Status = "passed"
		return result, nil
	}
	result.Status = "failed"
	if commandResult.Err != nil {
		return result, commandResult.Err
	}
	return result, fmt.Errorf("experiment command exited with code %d", commandResult.ExitCode)
}

func Render(w io.Writer, result Result) error {
	status := "FAIL"
	if result.Passed() {
		status = "PASS"
	}
	containmentEvidence := ""
	if result.ContainmentStatus != "" {
		containmentEvidence = " containment_status=" + result.ContainmentStatus
	}
	_, err := fmt.Fprintf(w, "%s: experiment %s runtime=%s run_id=%s exit=%d duration_ms=%d timed_out=%t signal=%s%s\nrun_dir=%s\n", status, result.ExperimentSpec, result.Runtime, result.RunID, result.ExitCode, result.DurationMS, result.TimedOut, result.TerminationSignal, containmentEvidence, result.RunDir)
	return err
}

func RenderJSON(w io.Writer, result Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func withDefaults(options Options) Options {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.signalSubscription == nil {
		options.signalSubscription = subscribeProcessTerminationSignals
	}
	return options
}

func defaultRunCommand(root string, command []string, env []string, stdout io.Writer, stderr io.Writer, executionTimeout time.Duration, cleanupGrace time.Duration) CommandResult {
	return defaultRunCommandWithBase(root, command, os.Environ(), env, stdout, stderr, executionTimeout, cleanupGrace)
}

func defaultRunCommandWithBase(root string, command, baseEnv, env []string, stdout io.Writer, stderr io.Writer, executionTimeout time.Duration, cleanupGrace time.Duration) CommandResult {
	return defaultRunCommandWithBaseAndSignals(root, command, baseEnv, env, stdout, stderr, executionTimeout, cleanupGrace, subscribeProcessTerminationSignals)
}

func defaultRunCommandWithBaseAndSignals(root string, command, baseEnv, env []string, stdout io.Writer, stderr io.Writer, executionTimeout time.Duration, cleanupGrace time.Duration, subscribe terminationSignalSubscription) CommandResult {
	if len(command) == 0 {
		return CommandResult{ExitCode: -1, Err: fmt.Errorf("empty experiment command")}
	}
	cmd := exec.Command(command[0], command[1:]...)
	if err := configureProcessGroup(cmd); err != nil {
		return CommandResult{ExitCode: -1, Err: err}
	}
	cmd.Dir = root
	cmd.Env = mergeEnvironment(baseEnv, env)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	terminationSignals, stopSignals := subscribe()
	if stopSignals == nil {
		stopSignals = func() {}
	}
	// Subscribe before Start so no process group can exist in a window where
	// normal SIGINT/SIGTERM handling terminates the runner without cleanup.
	// signal.Stop restores normal handling as soon as this invocation ends.
	defer stopSignals()
	if err := cmd.Start(); err != nil {
		return CommandResult{ExitCode: -1, Err: err}
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	timer := time.NewTimer(executionTimeout)
	defer timer.Stop()
	var err error
	select {
	case err = <-wait:
		return commandResultAfterWait(cmd, err, cleanupGrace, systemProcessGroupOperations())
	case <-timer.C:
		return terminateActiveCommand(cmd, wait, cleanupGrace, commandTerminationTimeout, nil, executionTimeout)
	case received := <-terminationSignals:
		return terminateActiveCommand(cmd, wait, cleanupGrace, commandTerminationInterrupt, received, executionTimeout)
	}
}

func subscribeProcessTerminationSignals() (<-chan os.Signal, func()) {
	signals := processInterruptSignals()
	ch := make(chan os.Signal, len(signals))
	signal.Notify(ch, signals...)
	return ch, func() { signal.Stop(ch) }
}

func exactEnvironmentBase(base []string) []string {
	allowed := map[string]bool{
		"HOME": true, "LOGNAME": true, "PATH": true, "TEMP": true,
		"TMP": true, "TMPDIR": true, "USER": true,
	}
	filtered := make([]string, 0, len(allowed)+4)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[key] {
			filtered = append(filtered, entry)
		}
	}
	return mergeEnvironment(filtered, []string{"BASH_ENV=/dev/null", "LANG=C", "LC_ALL=C", "TZ=UTC"})
}

func environmentLookup(env []string) func(string) string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	return func(key string) string { return values[key] }
}

// mergeEnvironment gives runner-owned values one unambiguous process entry.
// Duplicate environment names have implementation-defined lookup behavior in
// child runtimes, so appending an override is not a sufficient capability
// boundary for bindirs, endpoints, or protocol parameters.
func mergeEnvironment(base, overrides []string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, group := range [][]string{base, overrides} {
		for _, entry := range group {
			key, value, ok := strings.Cut(entry, "=")
			if !ok || key == "" || strings.HasPrefix(key, "BASH_FUNC_") ||
				strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
				continue
			}
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func systemProcessGroupOperations() processGroupOperations {
	return processGroupOperations{
		sendSignal: signalProcessGroup,
		status:     processGroupStatus,
	}
}

func commandContainmentStatus(result CommandResult) string {
	if result.TerminationSignal == "" {
		return ""
	}
	if result.ContainmentConfirmed {
		return ContainmentStatusConfirmed
	}
	return ContainmentStatusUnconfirmed
}

func commandResultAfterWait(cmd *exec.Cmd, waitErr error, cleanupGrace time.Duration, operations processGroupOperations) CommandResult {
	result := commandResultFromWait(waitErr)
	alive, initialStatusErr := operations.status(cmd)
	if initialStatusErr == nil && !alive {
		return result
	}

	termination := terminateProcessGroup(cmd, nil, cleanupGrace, operations)
	result.TerminationSignal = termination.cleanupSignal
	result.ContainmentConfirmed = termination.containmentConfirmed
	result.terminationReason = commandTerminationResidual
	if initialStatusErr != nil {
		result.ContainmentConfirmed = false
		termination.err = errors.Join(
			fmt.Errorf("verify experiment process group after command exit: %w", initialStatusErr),
			termination.err,
			fmt.Errorf("process-group containment not confirmed because the initial post-exit status probe failed"),
		)
		result.Err = errors.Join(result.Err, fmt.Errorf("experiment command exited before descendant containment could be verified; cleanup signal %s attempted", result.TerminationSignal))
	} else if termination.containmentConfirmed {
		result.Err = errors.Join(result.Err, fmt.Errorf("experiment command exited with live descendants; residual process-group disappearance confirmed after %s", result.TerminationSignal))
	} else {
		result.Err = errors.Join(result.Err, fmt.Errorf("experiment command exited with live descendants; residual process-group containment not confirmed after %s", result.TerminationSignal))
	}
	result.Err = errors.Join(result.Err, termination.err)
	if result.ExitCode == 0 {
		result.ExitCode = -1
	}
	return result
}

func terminateActiveCommand(cmd *exec.Cmd, wait <-chan error, cleanupGrace time.Duration, reason commandTerminationReason, received os.Signal, executionTimeout time.Duration) CommandResult {
	return terminateActiveCommandWithProcessGroupOperations(cmd, wait, cleanupGrace, reason, received, executionTimeout, systemProcessGroupOperations())
}

func terminateActiveCommandWithProcessGroupOperations(cmd *exec.Cmd, wait <-chan error, cleanupGrace time.Duration, reason commandTerminationReason, received os.Signal, executionTimeout time.Duration, operations processGroupOperations) CommandResult {
	termination := terminateProcessGroup(cmd, wait, cleanupGrace, operations)
	return activeTerminationResult(reason, received, executionTimeout, termination)
}

func terminateProcessGroup(cmd *exec.Cmd, wait <-chan error, cleanupGrace time.Duration, operations processGroupOperations) processGroupTermination {
	result := processGroupTermination{cleanupSignal: "SIGTERM"}
	if err := operations.sendSignal(cmd, processSignalTerminate); err != nil {
		result.err = errors.Join(result.err, fmt.Errorf("send SIGTERM to process group: %w", err))
	}

	confirmed, leaderWait, confirmationErr := confirmProcessGroupDisappearance(
		cmd,
		wait,
		cleanupGrace,
		"verify process-group disappearance during SIGTERM grace",
		operations,
	)
	result.err = errors.Join(result.err, confirmationErr)
	if confirmed {
		result.containmentConfirmed = result.err == nil
		if !result.containmentConfirmed {
			result.err = errors.Join(result.err, fmt.Errorf("process-group containment not confirmed after SIGTERM because cleanup or confirmation encountered errors"))
		}
		return result
	}

	result.cleanupSignal = "SIGKILL"
	if err := operations.sendSignal(cmd, processSignalKill); err != nil {
		result.err = errors.Join(result.err, fmt.Errorf("send SIGKILL to process group: %w", err))
	}

	confirmationTime := postKillConfirmationTime(cleanupGrace)
	confirmed, leaderWait, confirmationErr = confirmProcessGroupDisappearance(
		cmd,
		leaderWait,
		confirmationTime,
		"verify process-group disappearance after SIGKILL",
		operations,
	)
	result.err = errors.Join(result.err, confirmationErr)
	result.containmentConfirmed = confirmed && result.err == nil
	reapLeaderWithoutBlocking(leaderWait)
	if !confirmed {
		result.err = errors.Join(result.err, fmt.Errorf("process-group containment not confirmed after SIGKILL within %s", confirmationTime))
	} else if !result.containmentConfirmed {
		result.err = errors.Join(result.err, fmt.Errorf("process-group containment not confirmed after SIGKILL because cleanup or confirmation encountered errors"))
	}
	return result
}

func confirmProcessGroupDisappearance(cmd *exec.Cmd, wait <-chan error, timeout time.Duration, errorContext string, operations processGroupOperations) (bool, <-chan error, error) {
	var firstStatusErr error
	groupGone := func() bool {
		alive, err := operations.status(cmd)
		if err != nil {
			if firstStatusErr == nil {
				firstStatusErr = fmt.Errorf("%s: %w", errorContext, err)
			}
			return false
		}
		return !alive
	}
	if groupGone() {
		reapLeaderWithoutBlocking(wait)
		return true, wait, firstStatusErr
	}

	deadline := time.NewTimer(timeout)
	poll := time.NewTicker(processGroupPollInterval)
	defer deadline.Stop()
	defer poll.Stop()
	leaderWait := wait
	for {
		select {
		case <-leaderWait:
			leaderWait = nil
			if groupGone() {
				return true, leaderWait, firstStatusErr
			}
		case <-poll.C:
			if groupGone() {
				reapLeaderWithoutBlocking(leaderWait)
				return true, leaderWait, firstStatusErr
			}
		case <-deadline.C:
			return false, leaderWait, firstStatusErr
		}
	}
}

func postKillConfirmationTime(cleanupGrace time.Duration) time.Duration {
	if cleanupGrace < MinimumCleanupGrace {
		return MinimumCleanupGrace
	}
	if cleanupGrace > maximumPostKillConfirmationTime {
		return maximumPostKillConfirmationTime
	}
	return cleanupGrace
}

func reapLeaderWithoutBlocking(wait <-chan error) {
	if wait == nil {
		return
	}
	select {
	case <-wait:
	default:
	}
}

func activeTerminationResult(reason commandTerminationReason, received os.Signal, executionTimeout time.Duration, termination processGroupTermination) CommandResult {
	if reason == commandTerminationInterrupt {
		return interruptCommandResult(received, termination)
	}
	return timeoutCommandResult(executionTimeout, termination)
}

func commandResultFromWait(err error) CommandResult {
	if err == nil {
		return CommandResult{ExitCode: 0}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return CommandResult{ExitCode: exitErr.ExitCode(), Err: err}
	}
	return CommandResult{ExitCode: -1, Err: err}
}

func timeoutCommandResult(timeout time.Duration, termination processGroupTermination) CommandResult {
	err := fmt.Errorf("experiment execution timed out after %s; process-group disappearance confirmed after %s", timeout, termination.cleanupSignal)
	if !termination.containmentConfirmed {
		err = fmt.Errorf("experiment execution timed out after %s; process-group containment not confirmed after %s", timeout, termination.cleanupSignal)
	}
	if termination.err != nil {
		err = errors.Join(err, fmt.Errorf("process-group cleanup encountered errors: %w", termination.err))
	}
	return CommandResult{
		ExitCode:             TimeoutExitCode,
		Err:                  err,
		TimedOut:             true,
		TerminationSignal:    termination.cleanupSignal,
		ContainmentConfirmed: termination.containmentConfirmed,
		terminationReason:    commandTerminationTimeout,
	}
}

func interruptCommandResult(received os.Signal, termination processGroupTermination) CommandResult {
	interruptSignal := processInterruptSignalName(received)
	err := fmt.Errorf("experiment execution interrupted by %s; process-group disappearance confirmed after %s", interruptSignal, termination.cleanupSignal)
	if !termination.containmentConfirmed {
		err = fmt.Errorf("experiment execution interrupted by %s; process-group containment not confirmed after %s", interruptSignal, termination.cleanupSignal)
	}
	if termination.err != nil {
		err = errors.Join(err, fmt.Errorf("process-group cleanup encountered errors: %w", termination.err))
	}
	return CommandResult{
		ExitCode:             processInterruptExitCode(received),
		Err:                  err,
		TerminationSignal:    termination.cleanupSignal,
		ContainmentConfirmed: termination.containmentConfirmed,
		terminationReason:    commandTerminationInterrupt,
		interruptSignal:      interruptSignal,
	}
}

func resolvePositiveDuration(explicit time.Duration, fromEnv string, fallback time.Duration, label string) (time.Duration, error) {
	if explicit < 0 {
		return 0, fmt.Errorf("%s must be positive", label)
	}
	if explicit > 0 {
		return validateMinimumDuration(explicit, label)
	}
	if strings.TrimSpace(fromEnv) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(fromEnv))
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", label, fromEnv, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be positive", label)
	}
	return validateMinimumDuration(parsed, label)
}

func firstNonEmptyDuration(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func validateMinimumDuration(duration time.Duration, label string) (time.Duration, error) {
	minimum := MinimumExecutionTimeout
	if label == "cleanup grace" {
		minimum = MinimumCleanupGrace
	}
	if duration < minimum {
		return 0, fmt.Errorf("%s must be at least %s", label, minimum)
	}
	return duration, nil
}

func ceilSeconds(duration time.Duration) int64 {
	return int64((duration-1)/time.Second) + 1
}

func ceilMilliseconds(duration time.Duration) int64 {
	return int64((duration-1)/time.Millisecond) + 1
}

func writeRunnerFailureVerdict(result Result, commandResult CommandResult) error {
	runInfo, err := os.Lstat(result.RunDir)
	if err != nil {
		return err
	}
	if !runInfo.IsDir() || runInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runner-failed run directory is not a regular directory: %s", result.RunDir)
	}
	manifestPath := filepath.Join(result.RunDir, "manifest.env")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return err
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runner-failed manifest is not a regular file: %s", manifestPath)
	}
	manifest, err := envfile.Parse(manifestPath)
	if err != nil {
		return err
	}
	if manifest["run_id"] != result.RunID {
		return fmt.Errorf("runner-failed manifest run_id %q does not match %q", manifest["run_id"], result.RunID)
	}
	if manifest["experiment_spec_id"] != result.ExperimentSpec {
		return fmt.Errorf("runner-failed manifest experiment_spec_id %q does not match %q", manifest["experiment_spec_id"], result.ExperimentSpec)
	}
	if manifest["experiment_spec_digest"] != "sha256:"+result.SpecSHA256 {
		return fmt.Errorf("runner-failed manifest experiment_spec_digest does not match the executed spec")
	}
	for _, key := range []string{"started_at", "experiment_identity_digest"} {
		if strings.TrimSpace(manifest[key]) == "" {
			return fmt.Errorf("runner-failed manifest %s is empty", key)
		}
	}
	message, workloadExit := runnerFailureVerdictMessage(result, commandResult)
	verdict := runstate.Verdict{
		RunID:                result.RunID,
		Status:               runstate.VerdictStatusFailed,
		Message:              message,
		StartedAt:            manifest["started_at"],
		FinishedAt:           result.FinishedAt,
		ExperimentSpecID:     manifest["experiment_spec_id"],
		ExperimentSpecDigest: manifest["experiment_spec_digest"],
		RunDir:               result.RunDir,
		WorkloadExit:         workloadExit,
	}
	return runstate.WriteVerdict(result.RunDir, verdict)
}

func runnerFailureVerdictMessage(result Result, commandResult CommandResult) (string, int) {
	containmentMessage := "process-group containment not confirmed"
	if commandResult.ContainmentConfirmed {
		containmentMessage = "process-group disappearance confirmed"
	}
	message := fmt.Sprintf("experiment execution timed out after %d ms; cleanup signal %s attempted; %s", result.ExecutionTimeoutMS, result.TerminationSignal, containmentMessage)
	workloadExit := TimeoutExitCode
	switch commandResult.terminationReason {
	case commandTerminationInterrupt:
		message = fmt.Sprintf("experiment execution interrupted by %s; cleanup signal %s attempted; %s", commandResult.interruptSignal, result.TerminationSignal, containmentMessage)
		workloadExit = commandResult.ExitCode
	case commandTerminationResidual:
		message = fmt.Sprintf("experiment shell exited with live descendants; cleanup signal %s attempted; %s", result.TerminationSignal, containmentMessage)
		workloadExit = -1
	case commandTerminationNone:
		if commandResult.Err != nil {
			message = "experiment runner post-shell finalization failed"
			workloadExit = -1
		}
	default:
		if !result.TimedOut {
			message = fmt.Sprintf("experiment shell exited with live descendants; cleanup signal %s attempted; %s", result.TerminationSignal, containmentMessage)
			workloadExit = -1
		}
	}
	return message, workloadExit
}

func readSpecSnapshot(kind, id, path string) (speccatalog.Spec, []byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return speccatalog.Spec{}, nil, fmt.Errorf("inspect experiment spec: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return speccatalog.Spec{}, nil, fmt.Errorf("experiment spec is not a regular non-symlink file: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return speccatalog.Spec{}, nil, fmt.Errorf("open experiment spec: %w", err)
	}
	openedBefore, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return speccatalog.Spec{}, nil, fmt.Errorf("inspect opened experiment spec: %w", err)
	}
	if !openedBefore.Mode().IsRegular() || !os.SameFile(before, openedBefore) {
		_ = file.Close()
		return speccatalog.Spec{}, nil, fmt.Errorf("experiment spec changed while it was opened: %s", path)
	}
	content, readErr := io.ReadAll(file)
	openedAfter, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, closeErr); err != nil {
		return speccatalog.Spec{}, nil, fmt.Errorf("read experiment spec snapshot: %w", err)
	}
	if openedBefore.Size() != openedAfter.Size() || !openedBefore.ModTime().Equal(openedAfter.ModTime()) {
		return speccatalog.Spec{}, nil, fmt.Errorf("experiment spec changed while it was read: %s", path)
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedAfter, after) {
		if err != nil {
			return speccatalog.Spec{}, nil, fmt.Errorf("reinspect experiment spec: %w", err)
		}
		return speccatalog.Spec{}, nil, fmt.Errorf("experiment spec path changed while it was read: %s", path)
	}
	values, err := envfile.ParseBytes(path, content)
	if err != nil {
		return speccatalog.Spec{}, nil, err
	}
	return speccatalog.Spec{Kind: kind, ID: id, Path: path, Values: values}, content, nil
}

func createExecutionSpecSnapshot(content []byte) (string, func() error, error) {
	dir, err := os.MkdirTemp("", "pgworkbench-experiment-spec-")
	if err != nil {
		return "", nil, err
	}
	createdDirInfo, err := os.Lstat(dir)
	if err != nil {
		return "", nil, err
	}
	if !createdDirInfo.IsDir() || createdDirInfo.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("immutable experiment spec path is not the created directory: %s", dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", nil, errors.Join(err, cleanupExecutionSpecSnapshot(dir, filepath.Join(dir, "experiment.env"), createdDirInfo, nil))
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(createdDirInfo, dirInfo) || dirInfo.Mode().Perm() != 0o700 {
		if err != nil {
			return "", nil, err
		}
		return "", nil, errors.Join(
			fmt.Errorf("immutable experiment spec directory changed or has unsafe mode %s", dirInfo.Mode()),
			cleanupExecutionSpecSnapshot(dir, filepath.Join(dir, "experiment.env"), createdDirInfo, nil),
		)
	}
	path := filepath.Join(dir, "experiment.env")
	var fileInfo os.FileInfo
	cleanup := func() error { return cleanupExecutionSpecSnapshot(dir, path, dirInfo, fileInfo) }
	fail := func(cause error) (string, func() error, error) {
		return "", nil, errors.Join(cause, cleanup())
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fail(err)
	}
	fileInfo, err = file.Stat()
	if err != nil {
		_ = file.Close()
		return fail(err)
	}
	writeErr := error(nil)
	if _, err := file.Write(content); err != nil {
		writeErr = err
	} else if err := file.Sync(); err != nil {
		writeErr = err
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fail(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		return fail(err)
	}
	currentFileInfo, err := os.Lstat(path)
	if err != nil {
		return fail(err)
	}
	if !currentFileInfo.Mode().IsRegular() || currentFileInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(fileInfo, currentFileInfo) || currentFileInfo.Mode().Perm() != 0o400 {
		return fail(fmt.Errorf("immutable experiment spec snapshot has unsafe mode %s", currentFileInfo.Mode()))
	}
	return path, cleanup, nil
}

func cleanupExecutionSpecSnapshot(dir, path string, dirInfo, fileInfo os.FileInfo) error {
	if fileInfo != nil {
		current, err := os.Lstat(path)
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return err
		case !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(fileInfo, current):
			return fmt.Errorf("refusing to remove replaced experiment spec snapshot: %s", path)
		default:
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	current, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return err
	case !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(dirInfo, current):
		return fmt.Errorf("refusing to remove replaced experiment spec directory: %s", dir)
	default:
		return os.Remove(dir)
	}
}

func bytesSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func fileSHA256(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return bytesSHA256(content), nil
}

func sanitizeID(value string) string {
	value = strings.NewReplacer("/", "_", " ", "_").Replace(value)
	var out strings.Builder
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '-' {
			out.WriteRune(ch)
		}
	}
	return out.String()
}

func validRunID(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= 200 && sanitizeID(value) == value
}

func maxDurationMS(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}
