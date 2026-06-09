package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
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
	manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{
		"run_id":             "run-a",
		"experiment_spec_id": "smoke",
		"workload_spec":      "sql/smoke-run",
	}
	for key, want := range wants {
		if got := manifest[key]; got != want {
			t.Fatalf("manifest[%q] = %q, want %q", key, got, want)
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
	manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest["run_dir"]; got != runDir {
		t.Fatalf("manifest run_dir = %q, want %q", got, runDir)
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

	verdictEnv, err := envfile.Parse(filepath.Join(runDir, "verdict.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := verdictEnv["status"]; got != "passed" {
		t.Fatalf("verdict_env status=%q, want passed", got)
	}
	if got := verdictEnv["message"]; got != "experiment passed" {
		t.Fatalf("verdict_env message=%q, want 'experiment passed'", got)
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
