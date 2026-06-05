package utilitysuiteartifact

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilitysuite"
)

type Summary struct {
	RunID         string  `json:"run_id"`
	Suite         string  `json:"suite"`
	Name          string  `json:"name"`
	Dir           string  `json:"dir"`
	Status        string  `json:"status"`
	StartedAt     string  `json:"started_at"`
	FinishedAt    string  `json:"finished_at"`
	Total         int     `json:"total"`
	Passed        int     `json:"passed"`
	Failed        int     `json:"failed"`
	EntriesCount  int     `json:"entries_count"`
	HasSummary    bool    `json:"has_summary"`
	HasResultJSON bool    `json:"has_result_json"`
	HasDriverLogs bool    `json:"has_driver_logs"`
	Entries       []Entry `json:"entries"`
}

type Entry struct {
	UtilityTest    string `json:"utility_test"`
	ProfileSize    string `json:"profile_size"`
	Repeat         int    `json:"repeat"`
	RunID          string `json:"run_id"`
	ExitCode       int    `json:"exit_code"`
	Status         string `json:"status"`
	Message        string `json:"message"`
	RunDir         string `json:"run_dir"`
	ExperimentSpec string `json:"experiment_spec"`
	DriverLog      string `json:"driver_log"`
}

type VerifyResult struct {
	Summary Summary  `json:"summary"`
	Valid   bool     `json:"valid"`
	Issues  []string `json:"issues"`
}

func (r VerifyResult) IsValid() bool {
	return len(r.Issues) == 0
}

func List(root string, inputs []string) ([]Summary, error) {
	if len(inputs) == 0 {
		inputs = []string{"runs/utility-suites"}
	}

	var dirs []string
	seen := make(map[string]struct{})
	for _, input := range inputs {
		resolved, err := resolveInput(root, input)
		if err != nil {
			if input == "runs/utility-suites" && os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		discovered, err := discoverSuiteDirs(resolved)
		if err != nil {
			return nil, err
		}
		for _, dir := range discovered {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[abs]; ok {
				continue
			}
			seen[abs] = struct{}{}
			dirs = append(dirs, abs)
		}
	}

	summaries := make([]Summary, 0, len(dirs))
	for _, dir := range dirs {
		summary, err := LoadDir(root, dir)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		left := firstNonEmpty(summaries[i].StartedAt, summaries[i].RunID, summaries[i].Dir)
		right := firstNonEmpty(summaries[j].StartedAt, summaries[j].RunID, summaries[j].Dir)
		if left == right {
			return summaries[i].Dir > summaries[j].Dir
		}
		return left > right
	})
	return summaries, nil
}

func Show(root string, input string) (Summary, error) {
	dir, err := resolveSuiteRunDir(root, input)
	if err != nil {
		return Summary{}, err
	}
	return LoadDir(root, dir)
}

func Verify(root string, input string) (VerifyResult, error) {
	dir, err := resolveSuiteRunDir(root, input)
	if err != nil {
		return VerifyResult{}, err
	}

	result := VerifyResult{}
	entries, err := readEntries(filepath.Join(dir, "runs.tsv"))
	if err != nil {
		addIssue(&result, "runs.tsv parse failed: %v", err)
	}
	result.Summary = buildSummary(root, dir, entries, nil)
	result.Summary.HasSummary = fileExists(filepath.Join(dir, "summary.md"))
	result.Summary.HasResultJSON = fileExists(filepath.Join(dir, "result.json"))
	result.Summary.HasDriverLogs = dirExists(filepath.Join(dir, "driver-logs"))

	checkRequiredRegularFile(&result, filepath.Join(dir, "runs.tsv"), "runs.tsv")
	checkRequiredRegularFile(&result, filepath.Join(dir, "summary.md"), "summary.md")
	checkRequiredRegularFile(&result, filepath.Join(dir, "result.json"), "result.json")
	checkRequiredDir(&result, filepath.Join(dir, "driver-logs"), "driver-logs")

	runResult, ok := loadRunResultJSON(&result, filepath.Join(dir, "result.json"))
	if ok {
		result.Summary = buildSummary(root, dir, entries, &runResult)
		result.Summary.HasSummary = fileExists(filepath.Join(dir, "summary.md"))
		result.Summary.HasResultJSON = true
		result.Summary.HasDriverLogs = dirExists(filepath.Join(dir, "driver-logs"))
		checkRunResultConsistency(&result, root, dir, runResult, entries)
	}

	checkEntries(&result, root, dir, entries)
	result.Valid = result.IsValid()
	if result.Issues == nil {
		result.Issues = []string{}
	}
	return result, nil
}

func LoadDir(root string, dir string) (Summary, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Summary{}, err
	}
	entries, err := readEntries(filepath.Join(abs, "runs.tsv"))
	if err != nil {
		return Summary{}, err
	}
	runResult, _ := readRunResultJSON(filepath.Join(abs, "result.json"))
	summary := buildSummary(root, abs, entries, runResult)
	summary.HasSummary = fileExists(filepath.Join(abs, "summary.md"))
	summary.HasResultJSON = runResult != nil
	summary.HasDriverLogs = dirExists(filepath.Join(abs, "driver-logs"))
	return summary, nil
}

func RenderList(w io.Writer, summaries []Summary) error {
	if _, err := fmt.Fprintln(w, "# Utility Suite Runs"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if len(summaries) == 0 {
		_, err := fmt.Fprintln(w, "No utility suite run directories were found.")
		return err
	}
	if _, err := fmt.Fprintln(w, "| Run | Status | Suite | Passed | Failed | Total | Entries | Dir |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- | ---: | ---: | ---: | ---: | --- |"); err != nil {
		return err
	}
	for _, summary := range summaries {
		if _, err := fmt.Fprintf(
			w,
			"| `%s` | `%s` | `%s` | `%d` | `%d` | `%d` | `%d` | `%s` |\n",
			tableCell(summary.RunID),
			tableCell(summary.Status),
			tableCell(defaultValue(summary.Suite, "-")),
			summary.Passed,
			summary.Failed,
			summary.Total,
			summary.EntriesCount,
			tableCell(summary.Dir),
		); err != nil {
			return err
		}
	}
	return nil
}

func RenderShow(w io.Writer, summary Summary) error {
	if _, err := fmt.Fprintln(w, "# Utility Suite Run"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Field | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}
	rows := []struct {
		key   string
		value string
	}{
		{"Run", summary.RunID},
		{"Suite", summary.Suite},
		{"Name", summary.Name},
		{"Status", summary.Status},
		{"Started", summary.StartedAt},
		{"Finished", summary.FinishedAt},
		{"Total", strconv.Itoa(summary.Total)},
		{"Passed", strconv.Itoa(summary.Passed)},
		{"Failed", strconv.Itoa(summary.Failed)},
		{"Entries", strconv.Itoa(summary.EntriesCount)},
		{"Has summary", fmt.Sprintf("%t", summary.HasSummary)},
		{"Has result JSON", fmt.Sprintf("%t", summary.HasResultJSON)},
		{"Has driver logs", fmt.Sprintf("%t", summary.HasDriverLogs)},
		{"Run dir", summary.Dir},
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "| %s | `%s` |\n", tableCell(row.key), tableCell(defaultValue(row.value, "-"))); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Utility test | Profile size | Repeat | Run | Status | Exit | Driver log |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | ---: | --- | --- | ---: | --- |"); err != nil {
		return err
	}
	for _, entry := range summary.Entries {
		if _, err := fmt.Fprintf(
			w,
			"| `%s` | `%s` | `%d` | `%s` | `%s` | `%d` | `%s` |\n",
			tableCell(entry.UtilityTest),
			tableCell(entry.ProfileSize),
			entry.Repeat,
			tableCell(entry.RunID),
			tableCell(entry.Status),
			entry.ExitCode,
			tableCell(defaultValue(entry.DriverLog, "-")),
		); err != nil {
			return err
		}
	}
	return nil
}

func RenderVerify(w io.Writer, result VerifyResult) error {
	if result.IsValid() {
		_, err := fmt.Fprintf(w, "PASS: utility suite artifact %s\n", result.Summary.Dir)
		return err
	}
	if _, err := fmt.Fprintf(w, "FAIL: utility suite artifact %s\n", result.Summary.Dir); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(w, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func resolveInput(root string, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates, filepath.Join(root, input), filepath.Join(root, "runs", "utility-suites", input))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return filepath.Abs(candidate)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", os.ErrNotExist
}

func resolveSuiteRunDir(root string, input string) (string, error) {
	dir, err := resolveInput(root, input)
	if err != nil {
		return "", fmt.Errorf("utility suite run directory not found: %s", input)
	}
	return dir, nil
}

func discoverSuiteDirs(dir string) ([]string, error) {
	if isSuiteDir(dir) {
		return []string{dir}, nil
	}

	var dirs []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != dir && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if isSuiteDir(path) {
			dirs = append(dirs, path)
			return filepath.SkipDir
		}
		return nil
	})
	return dirs, err
}

func isSuiteDir(dir string) bool {
	header, err := readHeader(filepath.Join(dir, "runs.tsv"))
	if err != nil {
		return false
	}
	columns := columnsByName(header)
	for _, name := range requiredColumns() {
		if _, ok := columns[name]; !ok {
			return false
		}
	}
	return true
}

func readHeader(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	return reader.Read()
}

func readEntries(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("missing header")
	}
	columns := columnsByName(records[0])
	for _, name := range requiredColumns() {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("missing column: %s", name)
		}
	}

	entries := make([]Entry, 0, len(records)-1)
	for rowIndex, record := range records[1:] {
		if len(record) == 0 || strings.TrimSpace(strings.Join(record, "")) == "" {
			continue
		}
		repeat, err := parseIntCell(recordCell(record, columns["repeat"]), "repeat", rowIndex+2)
		if err != nil {
			return nil, err
		}
		exitCode, err := parseIntCell(recordCell(record, columns["exit_code"]), "exit_code", rowIndex+2)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{
			UtilityTest:    recordCell(record, columns["utility_test"]),
			ProfileSize:    recordCell(record, columns["profile_size"]),
			Repeat:         repeat,
			RunID:          recordCell(record, columns["run_id"]),
			ExitCode:       exitCode,
			Status:         recordCell(record, columns["status"]),
			Message:        recordCell(record, columns["message"]),
			RunDir:         recordCell(record, columns["run_dir"]),
			ExperimentSpec: recordCell(record, columns["experiment_spec"]),
			DriverLog:      recordCell(record, columns["driver_log"]),
		})
	}
	return entries, nil
}

func requiredColumns() []string {
	return []string{
		"utility_test",
		"profile_size",
		"repeat",
		"run_id",
		"exit_code",
		"status",
		"message",
		"run_dir",
		"experiment_spec",
		"driver_log",
	}
}

func columnsByName(header []string) map[string]int {
	columns := make(map[string]int, len(header))
	for index, name := range header {
		columns[strings.TrimSpace(name)] = index
	}
	return columns
}

func recordCell(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func parseIntCell(value string, label string, row int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("row %d %s is not an integer: %s", row, label, value)
	}
	return parsed, nil
}

func readRunResultJSON(path string) (*utilitysuite.RunResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result utilitysuite.RunResult
	if err := json.NewDecoder(file).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func loadRunResultJSON(result *VerifyResult, path string) (utilitysuite.RunResult, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			addIssue(result, "result.json read failed: %v", err)
		}
		return utilitysuite.RunResult{}, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		addIssue(result, "result.json parse failed: %v", err)
		return utilitysuite.RunResult{}, false
	}
	for _, key := range []string{
		"suite",
		"name",
		"run_id",
		"run_dir",
		"started_at",
		"finished_at",
		"total",
		"passed",
		"failed",
		"status",
		"entries",
	} {
		if _, ok := raw[key]; !ok {
			addIssue(result, "result.json missing key: %s", key)
		}
	}
	var runResult utilitysuite.RunResult
	if err := json.Unmarshal(content, &runResult); err != nil {
		addIssue(result, "result.json schema failed: %v", err)
		return utilitysuite.RunResult{}, false
	}
	return runResult, true
}

func buildSummary(root string, dir string, entries []Entry, result *utilitysuite.RunResult) Summary {
	passed := 0
	failed := 0
	unknown := 0
	for _, entry := range entries {
		switch strings.ToLower(entry.Status) {
		case "passed":
			passed++
		case "failed":
			failed++
		default:
			unknown++
		}
	}
	status := "missing"
	if len(entries) > 0 {
		status = "passed"
	}
	if failed > 0 || unknown > 0 {
		status = "failed"
	}

	summary := Summary{
		RunID:        filepath.Base(dir),
		Dir:          displayPath(root, dir),
		Status:       status,
		Total:        len(entries),
		Passed:       passed,
		Failed:       failed,
		EntriesCount: len(entries),
		Entries:      entries,
	}
	if result != nil {
		summary.RunID = defaultValue(result.RunID, summary.RunID)
		summary.Suite = result.Suite
		summary.Name = result.Name
		summary.Status = defaultValue(result.Status, summary.Status)
		summary.StartedAt = result.StartedAt
		summary.FinishedAt = result.FinishedAt
		summary.Total = result.Total
		summary.Passed = result.Passed
		summary.Failed = result.Failed
	}
	return summary
}

func checkRunResultConsistency(result *VerifyResult, root string, dir string, runResult utilitysuite.RunResult, entries []Entry) {
	if runResult.RunID == "" {
		addIssue(result, "result.json run_id is empty")
	}
	if runResult.Status == "" {
		addIssue(result, "result.json status is empty")
	}
	if runResult.StartedAt == "" {
		addIssue(result, "result.json started_at is empty")
	}
	if runResult.FinishedAt == "" {
		addIssue(result, "result.json finished_at is empty")
	}
	if runResult.RunDir != "" {
		resolved := resolveRootPath(root, runResult.RunDir)
		if filepath.Clean(resolved) != filepath.Clean(dir) {
			addIssue(result, "result.json run_dir points to %s, expected %s", resolved, dir)
		}
	}
	if runResult.Total < len(entries) {
		addIssue(result, "result.json total is smaller than runs.tsv entries")
	}
	if runResult.Passed+runResult.Failed != len(runResult.Entries) {
		addIssue(result, "result.json passed+failed does not match entries")
	}
	if len(runResult.Entries) != len(entries) {
		addIssue(result, "result.json entries count does not match runs.tsv entries")
	}

	byRun := make(map[string]utilitysuite.RunEntry, len(runResult.Entries))
	for _, entry := range runResult.Entries {
		byRun[entry.RunID] = entry
	}
	for _, entry := range entries {
		jsonEntry, ok := byRun[entry.RunID]
		if !ok {
			addIssue(result, "result.json missing entry for run_id: %s", entry.RunID)
			continue
		}
		if jsonEntry.Status != entry.Status {
			addIssue(result, "result.json status mismatch for %s", entry.RunID)
		}
		if jsonEntry.ExitCode != entry.ExitCode {
			addIssue(result, "result.json exit_code mismatch for %s", entry.RunID)
		}
	}
}

func checkEntries(result *VerifyResult, root string, suiteDir string, entries []Entry) {
	if len(entries) == 0 {
		addIssue(result, "runs.tsv has no entries")
		return
	}
	for index, entry := range entries {
		label := fmt.Sprintf("runs.tsv row %d", index+2)
		if entry.UtilityTest == "" {
			addIssue(result, "%s utility_test is empty", label)
		}
		if entry.ProfileSize == "" {
			addIssue(result, "%s profile_size is empty", label)
		}
		if entry.Repeat <= 0 {
			addIssue(result, "%s repeat must be positive", label)
		}
		if entry.RunID == "" {
			addIssue(result, "%s run_id is empty", label)
		}
		if entry.RunDir == "" {
			addIssue(result, "%s run_dir is empty", label)
		}
		if entry.DriverLog == "" {
			addIssue(result, "%s driver_log is empty", label)
		}
		status := strings.ToLower(entry.Status)
		if status != "passed" && status != "failed" {
			addIssue(result, "%s status must be passed or failed", label)
		}
		if status == "passed" && entry.ExperimentSpec == "" {
			addIssue(result, "%s experiment_spec is empty for passed run", label)
		}
		checkDriverLog(result, root, suiteDir, entry)
		checkExperimentRun(result, root, entry)
	}
}

func checkDriverLog(result *VerifyResult, root string, suiteDir string, entry Entry) {
	if entry.DriverLog == "" {
		return
	}
	path := resolveSuitePath(root, suiteDir, entry.DriverLog)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			addIssue(result, "missing driver log for %s: %s", entry.RunID, path)
			return
		}
		addIssue(result, "driver log stat failed for %s: %v", entry.RunID, err)
		return
	}
	if !info.Mode().IsRegular() {
		addIssue(result, "driver log for %s is not a regular file: %s", entry.RunID, path)
	}
}

func checkExperimentRun(result *VerifyResult, root string, entry Entry) {
	if entry.RunDir == "" {
		return
	}
	path := resolveRootPath(root, entry.RunDir)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if strings.EqualFold(entry.Status, "passed") {
				addIssue(result, "missing experiment run artifact for passed run %s: %s", entry.RunID, path)
			}
			return
		}
		addIssue(result, "experiment run stat failed for %s: %v", entry.RunID, err)
		return
	}
	if !info.IsDir() {
		addIssue(result, "experiment run path for %s is not a directory: %s", entry.RunID, path)
		return
	}
	if !runartifact.IsRunDir(path) {
		addIssue(result, "experiment run path for %s is not a run artifact: %s", entry.RunID, path)
		return
	}
	verification, err := runverify.Verify(root, path)
	if err != nil {
		addIssue(result, "experiment run verify failed for %s: %v", entry.RunID, err)
		return
	}
	for _, issue := range verification.Issues {
		addIssue(result, "experiment run %s: %s", entry.RunID, issue)
	}
}

func checkRequiredRegularFile(result *VerifyResult, path string, label string) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			addIssue(result, "missing %s", label)
			return
		}
		addIssue(result, "%s stat failed: %v", label, err)
		return
	}
	if !info.Mode().IsRegular() {
		addIssue(result, "%s is not a regular file", label)
		return
	}
	if info.Size() == 0 {
		addIssue(result, "%s is empty", label)
	}
}

func checkRequiredDir(result *VerifyResult, path string, label string) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			addIssue(result, "missing %s", label)
			return
		}
		addIssue(result, "%s stat failed: %v", label, err)
		return
	}
	if !info.IsDir() {
		addIssue(result, "%s is not a directory", label)
	}
}

func resolveRootPath(root string, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(root, value))
}

func resolveSuitePath(root string, suiteDir string, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	candidate := filepath.Join(suiteDir, value)
	if _, err := os.Stat(candidate); err == nil {
		return filepath.Clean(candidate)
	}
	rootCandidate := filepath.Join(root, value)
	if _, err := os.Stat(rootCandidate); err == nil {
		return filepath.Clean(rootCandidate)
	}
	return filepath.Clean(candidate)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func displayPath(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return rel
	}
	return path
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func defaultValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return value
}

func addIssue(result *VerifyResult, format string, args ...interface{}) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, args...))
}
