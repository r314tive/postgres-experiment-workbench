package runverify

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyValidRun(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidRun(t, runDir)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("expected valid result, got: %#v", result.Issues)
	}

	var out bytes.Buffer
	if err := Render(&out, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PASS: run artifact") {
		t.Fatalf("unexpected render output: %s", out.String())
	}
}

func TestVerifyDetectsMissingFiles(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.env"), []byte("run_id=run-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected invalid result")
	}
	for _, want := range []string{"missing verdict.env", "missing verdict.json", "missing metrics.csv"} {
		if !hasIssue(result, want) {
			t.Fatalf("missing issue %q in %#v", want, result.Issues)
		}
	}
}

func TestVerifyDetectsVerdictMismatch(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidRun(t, runDir)
	writeFile(t, filepath.Join(runDir, "verdict.env"), `status=failed
message=experiment passed
finished_at=2026-01-01T00:00:02Z
workload_exit=0
assert_exit=0
scan_exit=0
`)

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected invalid result")
	}
	if !hasIssue(result, "verdict.json status does not match verdict.env status") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func TestVerifyDetectsMetricsWithoutSamples(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeValidRun(t, runDir)
	writeFile(t, filepath.Join(runDir, "metrics.csv"), "sampled_at,database_name\n")

	result, err := Verify(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() {
		t.Fatal("expected invalid result")
	}
	if !hasIssue(result, "metrics.csv has no samples") {
		t.Fatalf("unexpected issues: %#v", result.Issues)
	}
}

func writeValidRun(t *testing.T, runDir string) {
	t.Helper()
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "manifest.env"), `run_id=run-a
started_at=2026-01-01T00:00:00Z
experiment_spec=experiments/smoke.env
experiment_spec_id=smoke
experiment_name=smoke experiment
experiment_topology=single
experiment_pg_config=default
profile=smoke
dataset_spec=
profile_size=small
workload_spec=sql/smoke-run
background_specs=
run_dir=`+runDir+`
`)
	writeFile(t, filepath.Join(runDir, "verdict.env"), `status=passed
message=experiment passed
finished_at=2026-01-01T00:00:02Z
workload_exit=0
assert_exit=0
scan_exit=0
`)
	writeFile(t, filepath.Join(runDir, "verdict.json"), `{
  "run_id": "run-a",
  "status": "passed",
  "message": "experiment passed",
  "started_at": "2026-01-01T00:00:00Z",
  "finished_at": "2026-01-01T00:00:02Z",
  "experiment_spec": "smoke",
  "run_dir": "`+runDir+`",
  "workload_exit": 0,
  "assert_exit": 0,
  "scan_exit": 0
}
`)
	writeFile(t, filepath.Join(runDir, "metrics.csv"), `sampled_at,database_name,wal_bytes
t0,db,100
`)
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasIssue(result Result, issue string) bool {
	for _, candidate := range result.Issues {
		if candidate == issue {
			return true
		}
	}
	return false
}
