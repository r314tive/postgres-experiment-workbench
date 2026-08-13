package utilitysuite

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityrun"
)

type Plan struct {
	Spec         string      `json:"spec"`
	SpecPath     string      `json:"spec_path"`
	Name         string      `json:"name"`
	Tests        []string    `json:"tests"`
	ProfileSizes []string    `json:"profile_sizes"`
	Repeats      int         `json:"repeats"`
	StopOnFail   bool        `json:"stop_on_fail"`
	Snapshot     string      `json:"snapshot"`
	RunID        string      `json:"run_id,omitempty"`
	RunDir       string      `json:"run_dir,omitempty"`
	TotalRuns    int         `json:"total_runs"`
	Runs         []PlanEntry `json:"runs"`
}

type PlanEntry struct {
	UtilityTest string `json:"utility_test"`
	ProfileSize string `json:"profile_size"`
	Repeat      int    `json:"repeat"`
}

type UtilityRunner func(root string, catalog speccatalog.Catalog, input string, options utilityrun.Options) (utilityrun.Result, error)

type RunOptions struct {
	Stdout     io.Writer
	Stderr     io.Writer
	Now        func() time.Time
	Getenv     utilityrun.Env
	RunUtility UtilityRunner
}

type RunResult struct {
	Suite      string     `json:"suite"`
	Name       string     `json:"name"`
	RunID      string     `json:"run_id"`
	RunDir     string     `json:"run_dir"`
	StartedAt  string     `json:"started_at"`
	FinishedAt string     `json:"finished_at"`
	Total      int        `json:"total"`
	Passed     int        `json:"passed"`
	Failed     int        `json:"failed"`
	Status     string     `json:"status"`
	Entries    []RunEntry `json:"entries"`
}

type RunEntry struct {
	UtilityTest    string `json:"utility_test"`
	ProfileSize    string `json:"profile_size"`
	Repeat         int    `json:"repeat"`
	RunID          string `json:"run_id"`
	RunDir         string `json:"run_dir"`
	ExperimentSpec string `json:"experiment_spec"`
	DriverLog      string `json:"driver_log"`
	ExitCode       int    `json:"exit_code"`
	Status         string `json:"status"`
	Message        string `json:"message"`
}

func (r RunResult) PassedAll() bool {
	return r.Status == "passed"
}

func Build(catalog speccatalog.Catalog, input string) (Plan, error) {
	spec, err := catalog.Show("utility-suite", input)
	if err != nil {
		return Plan{}, err
	}
	if errs := catalog.Validate("utility-suite", []string{spec.ID}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}

	values := spec.Values
	tests := wordsOr(values["UTILITY_SUITE_TESTS"], nil)
	profileSizes := wordsOr(values["UTILITY_SUITE_PROFILE_SIZES"], []string{"small"})
	repeats := positiveIntOr(values["UTILITY_SUITE_REPEATS"], 1)
	runs := make([]PlanEntry, 0, len(tests)*len(profileSizes)*repeats)
	for _, test := range tests {
		for _, profileSize := range profileSizes {
			for repeat := 1; repeat <= repeats; repeat++ {
				runs = append(runs, PlanEntry{
					UtilityTest: test,
					ProfileSize: profileSize,
					Repeat:      repeat,
				})
			}
		}
	}

	return Plan{
		Spec:         spec.ID,
		SpecPath:     spec.Path,
		Name:         defaultValue(values["UTILITY_SUITE_NAME"], spec.ID),
		Tests:        tests,
		ProfileSizes: profileSizes,
		Repeats:      repeats,
		StopOnFail:   shellDefault(values["UTILITY_SUITE_STOP_ON_FAIL"]) == "1",
		Snapshot:     defaultValue(values["UTILITY_SUITE_SNAPSHOT"], "1"),
		RunID:        shellDefault(values["UTILITY_SUITE_RUN_ID"]),
		RunDir:       shellDefault(values["UTILITY_SUITE_RUN_DIR"]),
		TotalRuns:    len(runs),
		Runs:         runs,
	}, nil
}

func Run(root string, catalog speccatalog.Catalog, input string, options RunOptions) (RunResult, error) {
	options = withDefaults(options)
	plan, err := Build(catalog, input)
	if err != nil {
		return RunResult{}, err
	}

	started := options.Now().UTC()
	runID := firstNonEmpty(options.Getenv("UTILITY_SUITE_RUN_ID"), plan.RunID)
	if runID == "" {
		runID = fmt.Sprintf("%s-utility-suite-%s", sanitizeID(plan.Spec), started.Format("20060102_150405"))
	}
	if !validRunID(runID) {
		return RunResult{}, fmt.Errorf("invalid utility suite run id %q", runID)
	}
	runDir, stagingDir, err := prepareRunDirectory(
		root,
		runID,
		firstNonEmpty(options.Getenv("UTILITY_SUITE_RUN_DIR"), plan.RunDir),
	)
	if err != nil {
		return RunResult{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	result := RunResult{
		Suite:     plan.Spec,
		Name:      plan.Name,
		RunID:     runID,
		RunDir:    runDir,
		StartedAt: started.Format(time.RFC3339),
		Total:     plan.TotalRuns,
		Status:    "passed",
	}

	if err := os.Mkdir(filepath.Join(stagingDir, "driver-logs"), 0o755); err != nil {
		return result, err
	}
	tsv, err := os.OpenFile(filepath.Join(stagingDir, "runs.tsv"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return result, err
	}
	if _, err := fmt.Fprintln(tsv, "utility_test\tprofile_size\trepeat\trun_id\texit_code\tstatus\tmessage\trun_dir\texperiment_spec\tdriver_log"); err != nil {
		_ = tsv.Close()
		return result, err
	}

	for _, entry := range plan.Runs {
		entryRunID := fmt.Sprintf("%s-%s-%s-r%02d", runID, sanitizeID(entry.UtilityTest), sanitizeID(entry.ProfileSize), entry.Repeat)
		driverLog := filepath.Join(runDir, "driver-logs", entryRunID+".log")
		stagingDriverLog := filepath.Join(stagingDir, "driver-logs", entryRunID+".log")
		logFile, err := os.OpenFile(stagingDriverLog, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			_ = tsv.Close()
			return result, err
		}

		env := []string{
			"UTILITY_TEST_RUN_ID=" + entryRunID,
			"PROFILE_SIZE=" + entry.ProfileSize,
			"UTILITY_TEST_SNAPSHOT=" + plan.Snapshot,
		}
		utilityResult, runErr := options.RunUtility(root, catalog, entry.UtilityTest, utilityrun.Options{
			Stdout: logFile,
			Stderr: logFile,
			Env:    env,
			Now:    options.Now,
			Getenv: overlayEnv(env, options.Getenv),
		})
		closeErr := logFile.Close()
		if runErr == nil && closeErr != nil {
			runErr = closeErr
		}

		status := utilityResult.Status
		if status == "" {
			status = "failed"
		}
		message := "utility passed"
		if runErr != nil {
			status = "failed"
			message = runErr.Error()
		}
		if status == "passed" {
			result.Passed++
		} else {
			result.Failed++
			result.Status = "failed"
		}

		runEntry := RunEntry{
			UtilityTest:    entry.UtilityTest,
			ProfileSize:    entry.ProfileSize,
			Repeat:         entry.Repeat,
			RunID:          firstNonEmpty(utilityResult.RunID, entryRunID),
			RunDir:         filepath.Join(root, "runs", firstNonEmpty(utilityResult.RunID, entryRunID)),
			ExperimentSpec: utilityResult.ExperimentSpec,
			DriverLog:      driverLog,
			ExitCode:       utilityResult.ExitCode,
			Status:         status,
			Message:        message,
		}
		result.Entries = append(result.Entries, runEntry)
		if _, err := fmt.Fprintf(
			tsv,
			"%s\t%s\t%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			entry.UtilityTest,
			entry.ProfileSize,
			entry.Repeat,
			runEntry.RunID,
			runEntry.ExitCode,
			runEntry.Status,
			tsvCell(runEntry.Message),
			runEntry.RunDir,
			runEntry.ExperimentSpec,
			runEntry.DriverLog,
		); err != nil {
			_ = tsv.Close()
			return result, err
		}

		if runErr != nil && plan.StopOnFail {
			break
		}
	}
	if err := tsv.Close(); err != nil {
		return result, err
	}

	result.FinishedAt = options.Now().UTC().Format(time.RFC3339)
	if err := RenderRunJSONFile(filepath.Join(stagingDir, "result.json"), result); err != nil {
		return result, err
	}
	if err := RenderSummary(filepath.Join(stagingDir, "summary.md"), result); err != nil {
		return result, err
	}
	if err := publishRunDirectory(stagingDir, runDir); err != nil {
		return result, err
	}
	published = true
	if result.Failed > 0 {
		return result, fmt.Errorf("utility suite failed: %d/%d failed", result.Failed, result.Total)
	}
	return result, nil
}

func Render(w io.Writer, plan Plan) error {
	fmt.Fprintln(w, "# Utility Suite Plan")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Suite: `%s`\n\n", tableCell(plan.Name))
	fmt.Fprintf(w, "Total runs: `%d`\n\n", plan.TotalRuns)
	fmt.Fprintln(w, "| Utility test | Profile size | Repeat |")
	fmt.Fprintln(w, "| --- | --- | ---: |")
	for _, run := range plan.Runs {
		fmt.Fprintf(w, "| `%s` | `%s` | `%d` |\n", tableCell(run.UtilityTest), tableCell(run.ProfileSize), run.Repeat)
	}
	return nil
}

func RenderJSON(w io.Writer, plan Plan) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

func RenderRun(w io.Writer, result RunResult) error {
	status := "FAIL"
	if result.PassedAll() {
		status = "PASS"
	}
	fmt.Fprintf(w, "%s: utility suite %s run_id=%s passed=%d failed=%d total=%d\n", status, result.Suite, result.RunID, result.Passed, result.Failed, result.Total)
	fmt.Fprintf(w, "run_dir=%s\n", result.RunDir)
	fmt.Fprintf(w, "summary=%s\n", filepath.Join(result.RunDir, "summary.md"))
	return nil
}

func RenderRunJSON(w io.Writer, result RunResult) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func RenderRunJSONFile(path string, result RunResult) error {
	return renderFile(path, func(file *os.File) error { return RenderRunJSON(file, result) })
}

func RenderSummary(path string, result RunResult) error {
	return renderFile(path, func(file *os.File) error {
		if _, err := fmt.Fprintln(file, "# Utility Suite Summary"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(file); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(file, "| Field | Value |"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(file, "| --- | --- |"); err != nil {
			return err
		}
		for _, row := range []struct {
			label string
			value any
		}{
			{"Suite", tableCell(result.Suite)},
			{"Name", tableCell(result.Name)},
			{"Run id", tableCell(result.RunID)},
			{"Status", tableCell(result.Status)},
			{"Total", result.Total},
			{"Passed", result.Passed},
			{"Failed", result.Failed},
		} {
			if _, err := fmt.Fprintf(file, "| %s | `%v` |\n", row.label, row.value); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(file); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(file, "| Utility test | Profile size | Repeat | Run id | Status | Exit | Message |"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(file, "| --- | --- | ---: | --- | --- | ---: | --- |"); err != nil {
			return err
		}
		for _, entry := range result.Entries {
			if _, err := fmt.Fprintf(
				file,
				"| `%s` | `%s` | `%d` | `%s` | `%s` | `%d` | %s |\n",
				tableCell(entry.UtilityTest),
				tableCell(entry.ProfileSize),
				entry.Repeat,
				tableCell(entry.RunID),
				tableCell(entry.Status),
				entry.ExitCode,
				tableCell(entry.Message),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func prepareRunDirectory(root, runID, requested string) (string, string, error) {
	suiteParent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "utility-suites"), 0o755)
	if err != nil {
		return "", "", fmt.Errorf("prepare utility suite artifact root: %w", err)
	}
	finalDir := filepath.Join(suiteParent, runID)
	if requested != "" {
		requestedPath := requested
		if !filepath.IsAbs(requestedPath) {
			requestedPath = filepath.Join(root, requestedPath)
		}
		requestedPath, err = filepath.Abs(requestedPath)
		if err != nil {
			return "", "", fmt.Errorf("resolve utility suite run directory: %w", err)
		}
		relative, relErr := filepath.Rel(suiteParent, requestedPath)
		if relErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.Clean(relative) != relative {
			return "", "", fmt.Errorf("utility suite run directory must be below %s", suiteParent)
		}
		if filepath.Base(relative) != runID {
			return "", "", fmt.Errorf("utility suite run directory basename must match run id %q", runID)
		}
		parentRelative := filepath.Dir(relative)
		parent := suiteParent
		if parentRelative != "." {
			parent, err = pathguard.EnsureDirectory(suiteParent, parentRelative, 0o755)
			if err != nil {
				return "", "", fmt.Errorf("prepare utility suite run directory parent: %w", err)
			}
		}
		finalDir = filepath.Join(parent, filepath.Base(relative))
	}
	if info, statErr := os.Lstat(finalDir); statErr == nil {
		return "", "", fmt.Errorf("refusing to overwrite immutable utility suite run: %s (%s)", finalDir, info.Mode())
	} else if !os.IsNotExist(statErr) {
		return "", "", fmt.Errorf("inspect utility suite run directory: %w", statErr)
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(finalDir), "."+filepath.Base(finalDir)+".staging-")
	if err != nil {
		return "", "", fmt.Errorf("create utility suite staging directory: %w", err)
	}
	return finalDir, stagingDir, nil
}

func publishRunDirectory(stagingDir, finalDir string) error {
	if info, err := os.Lstat(finalDir); err == nil {
		return fmt.Errorf("refusing to overwrite immutable utility suite run: %s (%s)", finalDir, info.Mode())
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect utility suite run directory before publication: %w", err)
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		return fmt.Errorf("publish immutable utility suite run: %w", err)
	}
	return nil
}

func renderFile(path string, render func(*os.File) error) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return errors.Join(render(file), file.Close())
}

func withDefaults(options RunOptions) RunOptions {
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
	if options.RunUtility == nil {
		options.RunUtility = utilityrun.Run
	}
	return options
}

func overlayEnv(env []string, fallback utilityrun.Env) utilityrun.Env {
	values := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return func(key string) string {
		if value, ok := values[key]; ok {
			return value
		}
		return fallback(key)
	}
}

func wordsOr(value string, fallback []string) []string {
	words := strings.Fields(shellDefault(value))
	if len(words) == 0 {
		return append([]string(nil), fallback...)
	}
	return words
}

func positiveIntOr(value string, fallback int) int {
	value = shellDefault(value)
	if value == "" {
		return fallback
	}
	parsed := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return fallback
		}
		parsed = parsed*10 + int(ch-'0')
	}
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

func defaultValue(value string, fallback string) string {
	value = shellDefault(value)
	if value == "" {
		return fallback
	}
	return value
}

func shellDefault(value string) string {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return value
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	_, fallback, ok := strings.Cut(inner, ":-")
	if !ok {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sanitizeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "utility-suite"
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
		return "utility-suite"
	}
	return out.String()
}

func validRunID(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= 200 && sanitizeID(value) == value
}

func tsvCell(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return value
}
