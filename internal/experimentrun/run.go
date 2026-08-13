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
	// DefaultExecutionTimeout is deliberately large enough for setup-heavy
	// experiments while still making every runner invocation finite. Callers
	// should set a tighter budget for benchmarks and CI jobs.
	DefaultExecutionTimeout = 6 * time.Hour
	DefaultCleanupGrace     = 15 * time.Second
	MinimumExecutionTimeout = time.Second
	MinimumCleanupGrace     = 100 * time.Millisecond
	TimeoutExitCode         = 124
)

type CommandResult struct {
	ExitCode          int
	Err               error
	TimedOut          bool
	TerminationSignal string
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
	Status             string   `json:"status"`
}

func (r Result) Passed() bool {
	return r.Status == "passed"
}

func Run(root string, catalog speccatalog.Catalog, input string, options Options) (Result, error) {
	options = withDefaults(options)
	lookup := options.Getenv
	if options.ExactEnvironment {
		lookup = environmentLookup(options.Env)
	}
	plan, err := experimentplan.Build(catalog, input)
	if err != nil {
		return Result{}, err
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

	specDigest, err := fileSHA256(plan.Spec.Path)
	if err != nil {
		return Result{}, fmt.Errorf("hash experiment spec: %w", err)
	}
	command := []string{filepath.Join(root, "scripts", "run_experiment.sh"), "run", plan.Spec.Path}
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
	env = append(env,
		"EXPERIMENT_RUN_ID="+runID,
		"EXPERIMENT_SPEC_SHA256="+specDigest,
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
		commandEnv := env
		if options.ExactEnvironment {
			commandEnv = mergeEnvironment(exactEnvironmentBase(os.Environ()), env)
		}
		commandResult = options.RunCommand(root, command, commandEnv, options.Stdout, options.Stderr)
	} else if options.ExactEnvironment {
		commandResult = defaultRunCommandWithBase(root, command, exactEnvironmentBase(os.Environ()), env, options.Stdout, options.Stderr, executionTimeout, cleanupGrace)
	} else {
		commandResult = defaultRunCommand(root, command, env, options.Stdout, options.Stderr, executionTimeout, cleanupGrace)
	}
	finished := options.Now().UTC()
	result.FinishedAt = finished.Format(time.RFC3339)
	result.DurationMS = maxDurationMS(finished.Sub(started))
	result.ExitCode = commandResult.ExitCode
	result.TimedOut = commandResult.TimedOut
	result.TerminationSignal = commandResult.TerminationSignal
	if commandResult.TimedOut && commandResult.TerminationSignal != "" {
		// The shell normally writes its own failed terminal verdict from its EXIT
		// trap. Re-publish it from the runner after the process group is fully
		// reaped so a forced SIGKILL still has a deterministic terminal artifact.
		if verdictErr := writeTimeoutVerdict(result); verdictErr != nil {
			commandResult.Err = errors.Join(commandResult.Err, fmt.Errorf("write timeout verdict: %w", verdictErr))
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
	_, err := fmt.Fprintf(w, "%s: experiment %s runtime=%s run_id=%s exit=%d duration_ms=%d timed_out=%t signal=%s\nrun_dir=%s\n", status, result.ExperimentSpec, result.Runtime, result.RunID, result.ExitCode, result.DurationMS, result.TimedOut, result.TerminationSignal, result.RunDir)
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
	return options
}

func defaultRunCommand(root string, command []string, env []string, stdout io.Writer, stderr io.Writer, executionTimeout time.Duration, cleanupGrace time.Duration) CommandResult {
	return defaultRunCommandWithBase(root, command, os.Environ(), env, stdout, stderr, executionTimeout, cleanupGrace)
}

func defaultRunCommandWithBase(root string, command, baseEnv, env []string, stdout io.Writer, stderr io.Writer, executionTimeout time.Duration, cleanupGrace time.Duration) CommandResult {
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
		result := commandResultFromWait(err)
		if alive, aliveErr := processGroupStatus(cmd); aliveErr != nil {
			result.Err = errors.Join(result.Err, fmt.Errorf("verify experiment process group after command exit: %w", aliveErr))
			if result.ExitCode == 0 {
				result.ExitCode = -1
			}
		} else if alive {
			result.TerminationSignal = terminateResidualProcessGroup(cmd, cleanupGrace)
			result.Err = errors.Join(result.Err, fmt.Errorf("experiment command exited with live descendants; residual process group terminated with %s", result.TerminationSignal))
			if result.ExitCode == 0 {
				result.ExitCode = -1
			}
		}
		return result
	case <-timer.C:
	}

	terminationSignal := "SIGTERM"
	if signalErr := signalProcessGroup(cmd, processSignalTerminate); signalErr != nil {
		return timeoutCommandResult(executionTimeout, terminationSignal, signalErr)
	}
	graceTimer := time.NewTimer(cleanupGrace)
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	leaderWaited := false
	for {
		select {
		case err = <-wait:
			leaderWaited = true
			alive, statusErr := processGroupStatus(cmd)
			if statusErr != nil {
				return timeoutCommandResult(executionTimeout, terminationSignal, statusErr)
			}
			if !alive {
				graceTimer.Stop()
				return timeoutCommandResult(executionTimeout, terminationSignal, nil)
			}
		case <-poll.C:
			alive, statusErr := processGroupStatus(cmd)
			if statusErr != nil {
				return timeoutCommandResult(executionTimeout, terminationSignal, statusErr)
			}
			if !alive {
				graceTimer.Stop()
				if !leaderWaited {
					err = <-wait
				}
				return timeoutCommandResult(executionTimeout, terminationSignal, nil)
			}
		case <-graceTimer.C:
			terminationSignal = "SIGKILL"
			killErr := signalProcessGroup(cmd, processSignalKill)
			// cmd.Wait continues in its buffered goroutine and will reap the
			// leader. Do not add an unbounded wait after SIGKILL: an
			// uninterruptible process must not defeat the runner deadline.
			return timeoutCommandResult(executionTimeout, terminationSignal, killErr)
		}
	}
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
			if !ok || key == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
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

func terminateResidualProcessGroup(cmd *exec.Cmd, cleanupGrace time.Duration) string {
	_ = signalProcessGroup(cmd, processSignalTerminate)
	deadline := time.NewTimer(cleanupGrace)
	poll := time.NewTicker(25 * time.Millisecond)
	defer deadline.Stop()
	defer poll.Stop()
	for {
		select {
		case <-poll.C:
			alive, err := processGroupStatus(cmd)
			if err != nil || !alive {
				return "SIGTERM"
			}
		case <-deadline.C:
			_ = signalProcessGroup(cmd, processSignalKill)
			return "SIGKILL"
		}
	}
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

func timeoutCommandResult(timeout time.Duration, signal string, terminationErr error) CommandResult {
	err := fmt.Errorf("experiment execution timed out after %s; process group terminated with %s", timeout, signal)
	if terminationErr != nil {
		err = errors.Join(err, fmt.Errorf("process-group termination failed: %w", terminationErr))
	}
	return CommandResult{
		ExitCode:          TimeoutExitCode,
		Err:               err,
		TimedOut:          true,
		TerminationSignal: signal,
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

func writeTimeoutVerdict(result Result) error {
	runInfo, err := os.Lstat(result.RunDir)
	if err != nil {
		return err
	}
	if !runInfo.IsDir() || runInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("timeout run directory is not a regular directory: %s", result.RunDir)
	}
	manifestPath := filepath.Join(result.RunDir, "manifest.env")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return err
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("timeout manifest is not a regular file: %s", manifestPath)
	}
	manifest, err := envfile.Parse(manifestPath)
	if err != nil {
		return err
	}
	if manifest["run_id"] != result.RunID {
		return fmt.Errorf("timeout manifest run_id %q does not match %q", manifest["run_id"], result.RunID)
	}
	if manifest["experiment_spec_id"] != result.ExperimentSpec {
		return fmt.Errorf("timeout manifest experiment_spec_id %q does not match %q", manifest["experiment_spec_id"], result.ExperimentSpec)
	}
	if manifest["experiment_spec_digest"] != "sha256:"+result.SpecSHA256 {
		return fmt.Errorf("timeout manifest experiment_spec_digest does not match the executed spec")
	}
	for _, key := range []string{"started_at", "experiment_identity_digest"} {
		if strings.TrimSpace(manifest[key]) == "" {
			return fmt.Errorf("timeout manifest %s is empty", key)
		}
	}
	verdict := runstate.Verdict{
		RunID:                result.RunID,
		Status:               runstate.VerdictStatusFailed,
		Message:              fmt.Sprintf("experiment execution timed out after %d ms; runner sent %s", result.ExecutionTimeoutMS, result.TerminationSignal),
		StartedAt:            manifest["started_at"],
		FinishedAt:           result.FinishedAt,
		ExperimentSpecID:     manifest["experiment_spec_id"],
		ExperimentSpecDigest: manifest["experiment_spec_digest"],
		RunDir:               result.RunDir,
		WorkloadExit:         TimeoutExitCode,
	}
	return runstate.WriteVerdict(result.RunDir, verdict)
}

func fileSHA256(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
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
