package runcatalog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListRuns(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "run-a", "2026-01-01T00:00:00Z", "passed", 2)
	writeRun(t, root, "run-b", "2026-01-01T00:01:00Z", "failed", 1)

	summaries, err := List(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected two runs, got %#v", summaries)
	}
	if summaries[0].RunID != "run-b" || summaries[0].SampleCount != 1 || summaries[0].Dir != "runs/run-b" {
		t.Fatalf("unexpected first summary: %#v", summaries[0])
	}
	if summaries[1].RunID != "run-a" || summaries[1].SampleCount != 2 {
		t.Fatalf("unexpected second summary: %#v", summaries[1])
	}

	var out bytes.Buffer
	if err := RenderList(&out, summaries); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Experiment Runs", "`run-b`", "`failed`", "`runs/run-a`"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("list render missing %q:\n%s", want, out.String())
		}
	}
}

func TestShowRun(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "run-a", "2026-01-01T00:00:00Z", "passed", 2)

	summary, err := Show(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RunID != "run-a" || summary.Status != "passed" || !summary.HasMetrics {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	var out bytes.Buffer
	if err := RenderShow(&out, summary); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "| Metrics samples | `2` |") {
		t.Fatalf("show render missing metrics:\n%s", out.String())
	}

	out.Reset()
	if err := RenderJSON(&out, summary); err != nil {
		t.Fatal(err)
	}
	var payload Summary
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RunID != "run-a" || payload.SampleCount != 2 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestListRunsFromSeries(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "run-a", "2026-01-01T00:00:00Z", "passed", 2)
	seriesDir := filepath.Join(root, "runs", "repeats", "repeat-a")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runsTSV := strings.Join([]string{
		"iteration\trun_id\texit_code\tstatus\tmessage\trun_dir",
		"1\trun-a\t0\tpassed\tok\truns/run-a",
		"2\tmissing\t1\tfailed\tstale\truns/missing",
		"",
	}, "\n")
	writeFile(t, filepath.Join(seriesDir, "runs.tsv"), runsTSV)

	summaries, err := List(root, []string{"runs/repeats"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].RunID != "run-a" {
		t.Fatalf("unexpected series summaries: %#v", summaries)
	}
}

func TestListMissingRunsDir(t *testing.T) {
	summaries, err := List(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected no runs, got %#v", summaries)
	}
}

func writeRun(t *testing.T, root string, id string, started string, status string, samples int) {
	t.Helper()
	dir := filepath.Join(root, "runs", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "manifest.env"), strings.Join([]string{
		"run_id=" + id,
		"started_at=" + started,
		"experiment_spec_id=smoke",
		"experiment_topology=single",
		"experiment_pg_config=default",
		"profile=smoke",
		"profile_size=small",
		"dataset_spec=synthetic/items",
		"workload_spec=sql/smoke-run",
		"",
	}, "\n"))
	writeFile(t, filepath.Join(dir, "verdict.env"), strings.Join([]string{
		"status=" + status,
		"message=" + status,
		"finished_at=2026-01-01T00:02:00Z",
		"workload_exit=0",
		"assert_exit=0",
		"scan_exit=0",
		"",
	}, "\n"))
	var metrics strings.Builder
	metrics.WriteString("sampled_at,database_name,wal_bytes\n")
	for i := 0; i < samples; i++ {
		metrics.WriteString("t,db,")
		metrics.WriteString("1")
		metrics.WriteString("\n")
	}
	writeFile(t, filepath.Join(dir, "metrics.csv"), metrics.String())
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
