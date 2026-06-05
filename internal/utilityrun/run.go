package utilityrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityplan"
)

type CommandResult struct {
	ExitCode int
	Err      error
}

type CommandRunner func(root string, command []string, env []string, stdout io.Writer, stderr io.Writer) CommandResult

type Env func(string) string

type Options struct {
	Stdout     io.Writer
	Stderr     io.Writer
	Env        []string
	Now        func() time.Time
	Getenv     Env
	RunCommand CommandRunner
}

type Result struct {
	UtilityTestSpec string   `json:"utility_test_spec"`
	UtilityTestName string   `json:"utility_test_name"`
	SpecPath        string   `json:"spec_path"`
	ExperimentSpec  string   `json:"experiment_spec"`
	RunID           string   `json:"run_id"`
	Command         []string `json:"command"`
	StartedAt       string   `json:"started_at"`
	FinishedAt      string   `json:"finished_at"`
	DurationMS      int64    `json:"duration_ms"`
	ExitCode        int      `json:"exit_code"`
	Status          string   `json:"status"`
}

func (r Result) Passed() bool {
	return r.Status == "passed"
}

func Run(root string, catalog speccatalog.Catalog, input string, options Options) (Result, error) {
	options = withDefaults(options)
	plan, err := utilityplan.Build(catalog, input)
	if err != nil {
		return Result{}, err
	}

	started := options.Now().UTC()
	runID := strings.TrimSpace(options.Getenv("UTILITY_TEST_RUN_ID"))
	if runID == "" {
		runID = fmt.Sprintf("utility-%s-%s", sanitizeID(plan.Spec.ID), started.Format("20060102_150405"))
	}

	experimentSpec, err := writeExperimentSpec(root, plan, runID)
	if err != nil {
		return Result{}, err
	}

	command := []string{filepath.Join(root, "scripts", "run_experiment.sh"), "run", experimentSpec}
	result := Result{
		UtilityTestSpec: plan.Spec.ID,
		UtilityTestName: plan.Fields["name"],
		SpecPath:        plan.Spec.Path,
		ExperimentSpec:  experimentSpec,
		RunID:           runID,
		Command:         append([]string(nil), command...),
		StartedAt:       started.Format(time.RFC3339),
	}

	commandResult := options.RunCommand(root, command, options.Env, options.Stdout, options.Stderr)
	finished := options.Now().UTC()
	result.FinishedAt = finished.Format(time.RFC3339)
	result.DurationMS = maxDurationMS(finished.Sub(started))
	result.ExitCode = commandResult.ExitCode
	if commandResult.Err == nil && commandResult.ExitCode == 0 {
		result.Status = "passed"
		return result, nil
	}
	result.Status = "failed"
	if commandResult.Err != nil {
		return result, commandResult.Err
	}
	return result, fmt.Errorf("utility test command exited with code %d", commandResult.ExitCode)
}

func Render(w io.Writer, result Result) error {
	status := "FAIL"
	if result.Passed() {
		status = "PASS"
	}
	if _, err := fmt.Fprintf(w, "%s: utility %s run_id=%s exit=%d duration_ms=%d\n", status, result.UtilityTestSpec, result.RunID, result.ExitCode, result.DurationMS); err != nil {
		return err
	}
	if result.ExperimentSpec != "" {
		if _, err := fmt.Fprintf(w, "experiment_spec=%s\n", result.ExperimentSpec); err != nil {
			return err
		}
	}
	return nil
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
	if options.RunCommand == nil {
		options.RunCommand = defaultRunCommand
	}
	return options
}

func defaultRunCommand(root string, command []string, env []string, stdout io.Writer, stderr io.Writer) CommandResult {
	if len(command) == 0 {
		return CommandResult{ExitCode: -1, Err: fmt.Errorf("empty utility test command")}
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err == nil {
		return CommandResult{ExitCode: 0}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return CommandResult{ExitCode: exitErr.ExitCode(), Err: err}
	}
	return CommandResult{ExitCode: -1, Err: err}
}

func writeExperimentSpec(root string, plan utilityplan.Plan, runID string) (string, error) {
	dir := filepath.Join(root, ".tmp", "utility-tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, sanitizeID(runID)+".env")

	var out strings.Builder
	out.WriteString("# Generated from utility-test spec. This file is ignored local runtime state.\n")
	writeEnv(&out, "EXPERIMENT_NAME", "utility: "+plan.Fields["name"])
	writeEnv(&out, "EXPERIMENT_RUN_ID", runID)
	writeEnv(&out, "EXPERIMENT_STATE_WRITER", "go")
	writeEnv(&out, "EXPERIMENT_PROFILE", plan.Fields["profile"])
	writeEnv(&out, "EXPERIMENT_PROFILE_SIZE", plan.Fields["profile_size"])
	writeEnv(&out, "EXPERIMENT_PROFILE_SECONDS", plan.Fields["profile_seconds"])
	writeEnv(&out, "EXPERIMENT_DATASET_SPEC", plan.Fields["dataset"])
	writeEnv(&out, "EXPERIMENT_DATASET_SIZE", plan.Fields["dataset_size"])
	writeEnv(&out, "EXPERIMENT_BACKGROUND_SPECS", plan.Fields["backgrounds"])
	writeEnv(&out, "EXPERIMENT_BACKGROUND_WARMUP", plan.Fields["background_warmup"])
	writeEnv(&out, "EXPERIMENT_BACKGROUND_WAIT", plan.Fields["background_wait"])
	writeEnv(&out, "EXPERIMENT_WORKLOAD_SPEC", plan.Fields["workload"])
	writeEnv(&out, "EXPERIMENT_METRICS", plan.Fields["metrics"])
	writeEnv(&out, "EXPERIMENT_METRICS_INTERVAL", plan.Fields["metrics_interval"])
	writeEnv(&out, "EXPERIMENT_METRICS_DURATION", plan.Fields["metrics_duration"])
	writeEnv(&out, "EXPERIMENT_METRICS_SAMPLES", plan.Fields["metrics_samples"])
	writeEnv(&out, "EXPERIMENT_ASSERT_SQL_FILES", plan.Fields["assert_sql_files"])
	writeEnv(&out, "EXPERIMENT_ASSERT_SQL", plan.Fields["assert_sql"])
	writeEnv(&out, "EXPERIMENT_ASSERT_SHELL", combinedAssertShell(plan.Fields))
	writeEnv(&out, "EXPERIMENT_SCAN_PATHS", plan.Fields["scan_paths"])
	writeEnv(&out, "EXPERIMENT_SNAPSHOT", "${UTILITY_TEST_SNAPSHOT:-1}")

	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func writeEnv(out *strings.Builder, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	out.WriteString(key)
	out.WriteByte('=')
	out.WriteString(shellValue(value))
	out.WriteByte('\n')
}

func shellValue(value string) string {
	if strings.Contains(value, "$") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(value) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func combinedAssertShell(fields map[string]string) string {
	var parts []string
	if fields["assert_shell"] != "" {
		parts = append(parts, fields["assert_shell"])
	}
	for _, path := range strings.Fields(fields["expect_files"]) {
		parts = append(parts, "test -s "+shellPath(path))
	}
	return strings.Join(parts, "; ")
}

func shellPath(path string) string {
	if filepath.IsAbs(path) || strings.Contains(path, "$") {
		return shellQuote(path)
	}
	return `"$REPO_DIR/` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(path) + `"`
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func sanitizeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "utility-test"
	}
	var out strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '.', ch == '-':
			out.WriteRune(ch)
		case ch == '/' || ch == ' ' || ch == '_':
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "utility-test"
	}
	return out.String()
}

func maxDurationMS(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}
