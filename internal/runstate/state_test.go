package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteManifest(t *testing.T) {
	runDir := t.TempDir()
	env := map[string]string{
		"RUN_ID":                   "run-a",
		"STARTED_AT":               "2026-01-01T00:00:00Z",
		"EXPERIMENT_SPEC_FILE":     "/repo/experiments/smoke.env",
		"EXPERIMENT_SPEC_ID":       "smoke",
		"EXPERIMENT_NAME":          "smoke experiment",
		"EXPERIMENT_TOPOLOGY":      "single",
		"EXPERIMENT_PG_CONFIG":     "default",
		"EXPERIMENT_PROFILE":       "smoke",
		"EXPERIMENT_PROFILE_SIZE":  "small",
		"EXPERIMENT_WORKLOAD_SPEC": "sql/smoke-run",
		"RUN_DIR":                  runDir,
	}

	if err := WriteManifest(runDir, ManifestFromEnv(mapGetter(env))); err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(runDir, "manifest.env"))
	for _, want := range []string{"run_id=run-a", "experiment_spec_id=smoke", "workload_spec=sql/smoke-run"} {
		if !strings.Contains(content, want) {
			t.Fatalf("manifest missing %q:\n%s", want, content)
		}
	}
}

func TestWriteManifestUsesOutputDirWhenEnvRunDirIsMissing(t *testing.T) {
	runDir := t.TempDir()
	env := map[string]string{
		"RUN_ID":             "run-a",
		"EXPERIMENT_SPEC_ID": "smoke",
	}

	if err := WriteManifest(runDir, ManifestFromEnv(mapGetter(env))); err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(runDir, "manifest.env"))
	if !strings.Contains(content, "run_dir="+runDir) {
		t.Fatalf("manifest did not use output dir:\n%s", content)
	}
}

func TestWriteVerdict(t *testing.T) {
	runDir := t.TempDir()
	env := map[string]string{
		"RUN_ID":             "run-a",
		"STARTED_AT":         "2026-01-01T00:00:00Z",
		"EXPERIMENT_SPEC_ID": "smoke",
		"RUN_DIR":            runDir,
		"WORKLOAD_EXIT":      "0",
		"ASSERT_EXIT":        "0",
		"SCAN_EXIT":          "0",
	}

	verdict := VerdictFromEnv(mapGetter(env), "passed", "experiment passed", "2026-01-01T00:00:02Z")
	if err := WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}

	envContent := readFile(t, filepath.Join(runDir, "verdict.env"))
	if !strings.Contains(envContent, "status=passed") || !strings.Contains(envContent, "message=experiment passed") {
		t.Fatalf("unexpected verdict.env:\n%s", envContent)
	}
	jsonContent := readFile(t, filepath.Join(runDir, "verdict.json"))
	for _, want := range []string{`"status": "passed"`, `"experiment_spec": "smoke"`, `"scan_exit": 0`} {
		if !strings.Contains(jsonContent, want) {
			t.Fatalf("verdict.json missing %q:\n%s", want, jsonContent)
		}
	}
}

func TestWriteVerdictUsesOutputDirWhenEnvRunDirIsMissing(t *testing.T) {
	runDir := t.TempDir()
	env := map[string]string{
		"RUN_ID":             "run-a",
		"EXPERIMENT_SPEC_ID": "smoke",
	}

	verdict := VerdictFromEnv(mapGetter(env), "passed", "experiment passed", "2026-01-01T00:00:02Z")
	if err := WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}
	jsonContent := readFile(t, filepath.Join(runDir, "verdict.json"))
	if !strings.Contains(jsonContent, `"run_dir": "`+runDir+`"`) {
		t.Fatalf("verdict did not use output dir:\n%s", jsonContent)
	}
}

func mapGetter(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
