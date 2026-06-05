package runverify

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
)

type Result struct {
	Dir    string
	Issues []string
}

func (r Result) Valid() bool {
	return len(r.Issues) == 0
}

func Verify(root string, input string) (Result, error) {
	dir, err := runartifact.ResolveRunDir(root, input)
	if err != nil {
		return Result{}, err
	}

	result := Result{Dir: dir}
	manifest, manifestOK := loadEnv(&result, filepath.Join(dir, "manifest.env"), "manifest.env")
	verdict, verdictOK := loadEnv(&result, filepath.Join(dir, "verdict.env"), "verdict.env")
	verdictJSON, verdictJSONOK := loadVerdictJSON(&result, filepath.Join(dir, "verdict.json"))
	checkMetrics(&result, filepath.Join(dir, "metrics.csv"))

	if manifestOK {
		checkRequiredEnv(&result, "manifest.env", manifest, []string{
			"run_id",
			"started_at",
			"experiment_spec_id",
			"experiment_topology",
			"experiment_pg_config",
			"profile_size",
			"run_dir",
		})
		checkRunDirValue(&result, root, dir, "manifest.env", manifest.Value("run_dir", ""))
	}
	if verdictOK {
		checkRequiredEnv(&result, "verdict.env", verdict, []string{
			"status",
			"message",
			"finished_at",
			"workload_exit",
			"assert_exit",
			"scan_exit",
		})
		checkExitCode(&result, verdict, "workload_exit")
		checkExitCode(&result, verdict, "assert_exit")
		checkExitCode(&result, verdict, "scan_exit")
	}

	if verdictJSONOK {
		checkVerdictJSONKeys(&result, verdictJSON)
		checkVerdictConsistency(&result, manifest, verdict, verdictJSON)
		checkRunDirValue(&result, root, dir, "verdict.json", verdictJSON.RunDir)
	}

	return result, nil
}

func Render(w io.Writer, result Result) error {
	if result.Valid() {
		_, err := fmt.Fprintf(w, "PASS: run artifact %s\n", result.Dir)
		return err
	}

	if _, err := fmt.Fprintf(w, "FAIL: run artifact %s\n", result.Dir); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(w, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, result Result) error {
	issues := result.Issues
	if issues == nil {
		issues = []string{}
	}
	payload := struct {
		Dir    string   `json:"dir"`
		Valid  bool     `json:"valid"`
		Issues []string `json:"issues"`
	}{
		Dir:    result.Dir,
		Valid:  result.Valid(),
		Issues: issues,
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func loadEnv(result *Result, path string, label string) (runartifact.Env, bool) {
	if !checkRequiredRegularFile(result, path, label) {
		return runartifact.Env{}, false
	}
	values, err := runartifact.LoadOptionalEnv(path)
	if err != nil {
		addIssue(result, "%s parse failed: %v", label, err)
		return runartifact.Env{}, false
	}
	return values, true
}

func loadVerdictJSON(result *Result, path string) (runstate.Verdict, bool) {
	if !checkRequiredRegularFile(result, path, "verdict.json") {
		return runstate.Verdict{}, false
	}

	content, err := os.ReadFile(path)
	if err != nil {
		addIssue(result, "verdict.json read failed: %v", err)
		return runstate.Verdict{}, false
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		addIssue(result, "verdict.json parse failed: %v", err)
		return runstate.Verdict{}, false
	}

	var verdict runstate.Verdict
	if err := json.Unmarshal(content, &verdict); err != nil {
		addIssue(result, "verdict.json schema failed: %v", err)
		return runstate.Verdict{}, false
	}

	for _, key := range []string{
		"run_id",
		"status",
		"message",
		"started_at",
		"finished_at",
		"experiment_spec",
		"run_dir",
		"workload_exit",
		"assert_exit",
		"scan_exit",
	} {
		if _, ok := raw[key]; !ok {
			addIssue(result, "verdict.json missing key: %s", key)
		}
	}

	return verdict, true
}

func checkRequiredRegularFile(result *Result, path string, label string) bool {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			addIssue(result, "missing %s", label)
			return false
		}
		addIssue(result, "%s stat failed: %v", label, err)
		return false
	}
	if !info.Mode().IsRegular() {
		addIssue(result, "%s is not a regular file", label)
		return false
	}
	if info.Size() == 0 {
		addIssue(result, "%s is empty", label)
		return false
	}
	return true
}

func checkRequiredEnv(result *Result, label string, values runartifact.Env, keys []string) {
	for _, key := range keys {
		if values.Value(key, "") == "" {
			addIssue(result, "%s missing key: %s", label, key)
		}
	}
}

func checkExitCode(result *Result, verdict runartifact.Env, key string) {
	value := verdict.Value(key, "")
	if value == "" {
		return
	}
	if _, err := strconv.Atoi(value); err != nil {
		addIssue(result, "verdict.env %s is not an integer: %s", key, value)
	}
}

func checkMetrics(result *Result, path string) {
	if !checkRequiredRegularFile(result, path, "metrics.csv") {
		return
	}

	file, err := os.Open(path)
	if err != nil {
		addIssue(result, "metrics.csv read failed: %v", err)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		addIssue(result, "metrics.csv header read failed: %v", err)
		return
	}
	if !contains(header, "sampled_at") {
		addIssue(result, "metrics.csv missing sampled_at column")
	}

	rows := 0
	for {
		record, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			addIssue(result, "metrics.csv row read failed: %v", err)
			return
		}
		if len(record) > 0 {
			rows++
		}
	}
	if rows == 0 {
		addIssue(result, "metrics.csv has no samples")
	}
}

func checkVerdictJSONKeys(result *Result, verdict runstate.Verdict) {
	if verdict.RunID == "" {
		addIssue(result, "verdict.json run_id is empty")
	}
	if verdict.Status == "" {
		addIssue(result, "verdict.json status is empty")
	}
	if verdict.Message == "" {
		addIssue(result, "verdict.json message is empty")
	}
	if verdict.FinishedAt == "" {
		addIssue(result, "verdict.json finished_at is empty")
	}
}

func checkVerdictConsistency(result *Result, manifest runartifact.Env, verdict runartifact.Env, verdictJSON runstate.Verdict) {
	checkStringMatch(result, "verdict.json", "run_id", verdictJSON.RunID, "manifest.env", "run_id", manifest.Value("run_id", ""))
	checkStringMatch(result, "verdict.json", "started_at", verdictJSON.StartedAt, "manifest.env", "started_at", manifest.Value("started_at", ""))
	checkStringMatch(result, "verdict.json", "experiment_spec", verdictJSON.ExperimentSpecID, "manifest.env", "experiment_spec_id", manifest.Value("experiment_spec_id", ""))
	checkStringMatch(result, "verdict.json", "status", verdictJSON.Status, "verdict.env", "status", verdict.Value("status", ""))
	checkStringMatch(result, "verdict.json", "message", verdictJSON.Message, "verdict.env", "message", verdict.Value("message", ""))
	checkStringMatch(result, "verdict.json", "finished_at", verdictJSON.FinishedAt, "verdict.env", "finished_at", verdict.Value("finished_at", ""))
	checkIntMatch(result, "workload_exit", verdictJSON.WorkloadExit, verdict.Value("workload_exit", ""))
	checkIntMatch(result, "assert_exit", verdictJSON.AssertExit, verdict.Value("assert_exit", ""))
	checkIntMatch(result, "scan_exit", verdictJSON.ScanExit, verdict.Value("scan_exit", ""))
}

func checkStringMatch(result *Result, leftLabel string, leftKey string, leftValue string, rightLabel string, rightKey string, rightValue string) {
	if leftValue == "" || rightValue == "" {
		return
	}
	if leftValue != rightValue {
		addIssue(result, "%s %s does not match %s %s", leftLabel, leftKey, rightLabel, rightKey)
	}
}

func checkIntMatch(result *Result, key string, jsonValue int, envValue string) {
	if envValue == "" {
		return
	}
	parsed, err := strconv.Atoi(envValue)
	if err != nil {
		return
	}
	if jsonValue != parsed {
		addIssue(result, "verdict.json %s does not match verdict.env %s", key, key)
	}
}

func checkRunDirValue(result *Result, root string, expectedDir string, label string, value string) {
	if value == "" {
		return
	}
	candidate := value
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	resolved, err := filepath.Abs(candidate)
	if err != nil {
		addIssue(result, "%s run_dir cannot be resolved: %v", label, err)
		return
	}
	if filepath.Clean(resolved) != filepath.Clean(expectedDir) {
		addIssue(result, "%s run_dir points to %s, expected %s", label, resolved, expectedDir)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func addIssue(result *Result, format string, args ...interface{}) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, args...))
}
