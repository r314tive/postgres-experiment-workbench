package workloadrun

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
	"github.com/r314tive/postgres-experiment-workbench/internal/workloadplan"
)

type CommandResult struct {
	ExitCode int
	Err      error
}

type CommandRunner func(root string, command []string, env []string, stdout io.Writer, stderr io.Writer) CommandResult

type Env func(string) string

type Options struct {
	AdapterArgs []string
	Stdout      io.Writer
	Stderr      io.Writer
	Now         func() time.Time
	Getenv      Env
	RunCommand  CommandRunner
}

type Result struct {
	WorkloadSpec     string   `json:"workload_spec"`
	WorkloadName     string   `json:"workload_name"`
	WorkloadKind     string   `json:"workload_kind"`
	SpecPath         string   `json:"spec_path"`
	RequiresPostgres bool     `json:"requires_postgres"`
	Logging          bool     `json:"logging"`
	LogFile          string   `json:"log_file,omitempty"`
	Command          []string `json:"command"`
	AdapterArgs      []string `json:"adapter_args,omitempty"`
	StartedAt        string   `json:"started_at"`
	FinishedAt       string   `json:"finished_at"`
	DurationMS       int64    `json:"duration_ms"`
	ExitCode         int      `json:"exit_code"`
	Status           string   `json:"status"`
}

func (r Result) Passed() bool {
	return r.Status == "passed"
}

func Run(root string, catalog speccatalog.Catalog, workloadSpec string, options Options) (Result, error) {
	options = withDefaults(options)
	plan, err := workloadplan.Build(root, catalog, workloadSpec)
	if err != nil {
		return Result{}, err
	}

	started := options.Now()
	logging := effectiveLogging(plan.Logging, options.Getenv)
	logFile, env := workloadLog(root, plan.ID, logging, started, options.Getenv)
	command := append([]string{filepath.Join(root, "scripts", "run_workload.sh"), "run", plan.Path}, options.AdapterArgs...)

	result := Result{
		WorkloadSpec:     plan.ID,
		WorkloadName:     plan.Name,
		WorkloadKind:     plan.Kind,
		SpecPath:         plan.Path,
		RequiresPostgres: plan.RequiresPostgres,
		Logging:          logging,
		LogFile:          logFile,
		Command:          append([]string(nil), command...),
		AdapterArgs:      append([]string(nil), options.AdapterArgs...),
		StartedAt:        started.UTC().Format(time.RFC3339),
	}

	commandResult := options.RunCommand(root, command, env, options.Stdout, options.Stderr)
	finished := options.Now()
	result.FinishedAt = finished.UTC().Format(time.RFC3339)
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
	return result, fmt.Errorf("workload command exited with code %d", commandResult.ExitCode)
}

func Render(w io.Writer, result Result) error {
	status := "FAIL"
	if result.Passed() {
		status = "PASS"
	}
	if _, err := fmt.Fprintf(w, "%s: workload %s exit=%d duration_ms=%d\n", status, result.WorkloadSpec, result.ExitCode, result.DurationMS); err != nil {
		return err
	}
	if result.LogFile != "" {
		if _, err := fmt.Fprintf(w, "log=%s\n", result.LogFile); err != nil {
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
		return CommandResult{ExitCode: -1, Err: fmt.Errorf("empty workload command")}
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

func effectiveLogging(defaultLogging bool, getenv Env) bool {
	switch getenv("WORKLOAD_RUN_LOG") {
	case "0":
		return false
	case "":
		return defaultLogging
	default:
		return true
	}
}

func workloadLog(root string, workloadID string, logging bool, started time.Time, getenv Env) (string, []string) {
	if !logging {
		return "", nil
	}
	if logFile := strings.TrimSpace(getenv("WORKLOAD_LOG_FILE")); logFile != "" {
		return logFile, nil
	}
	logDir := strings.TrimSpace(getenv("WORKLOAD_LOG_DIR"))
	if logDir == "" {
		logDir = filepath.Join(root, "logs", "workloads")
	} else if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(root, logDir)
	}
	logFile := filepath.Join(logDir, fmt.Sprintf("%s.%s.log", sanitizeID(workloadID), started.UTC().Format("20060102_150405")))
	return logFile, []string{"WORKLOAD_LOG_FILE=" + logFile}
}

func sanitizeID(value string) string {
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, " ", "_")
	var out strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-' {
			out.WriteRune(ch)
		}
	}
	if out.Len() == 0 {
		return "workload"
	}
	return out.String()
}

func maxDurationMS(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}
