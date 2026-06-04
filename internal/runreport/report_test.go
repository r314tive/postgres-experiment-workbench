package runreport

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRun(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "manifest.env"), `run_id=run-a
started_at=2026-01-01T00:00:00Z
experiment_spec_id=smoke
experiment_topology=single
experiment_pg_config=default
profile=smoke
dataset_spec=synthetic/items
workload_spec=sql/smoke-run
background_specs=profile/locks-blocker
`)
	writeFile(t, filepath.Join(runDir, "verdict.env"), `status=passed
message=experiment passed
finished_at=2026-01-01T00:00:02Z
workload_exit=0
assert_exit=0
scan_exit=0
`)
	writeFile(t, filepath.Join(runDir, "metrics.csv"), `sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_inserted,tup_updated,tup_deleted,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes
t0,db,1,0,0,0,5,0,10,0,1,100,10,0,0,0,0,0,10,0,100
t1,db,2,1,1,1,8,1,15,0,2,150,40,1,0,0,1,20,20,0,250
`)

	var out bytes.Buffer
	if err := RenderRun(root, "run-a", &out); err != nil {
		t.Fatal(err)
	}

	rendered := out.String()
	for _, want := range []string{
		"# Experiment Run Report",
		"| Status | `passed` |",
		"Samples: `2`",
		"| `wal_bytes` | `100` | `250` | `150` | `100` | `250` |",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderComparison(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "runs", "base")
	candidate := filepath.Join(root, "runs", "candidate")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(base, "verdict.env"), "status=passed\nmessage=baseline\n")
	writeFile(t, filepath.Join(candidate, "verdict.env"), "status=passed\nmessage=candidate\n")
	writeFile(t, filepath.Join(base, "metrics.csv"), `sampled_at,database_name,temp_bytes,wal_bytes,tup_inserted,tup_updated,tup_deleted
t0,db,0,100,10,20,30
t1,db,10,160,15,30,35
`)
	writeFile(t, filepath.Join(candidate, "metrics.csv"), `sampled_at,database_name,temp_bytes,wal_bytes,tup_inserted,tup_updated,tup_deleted
t0,db,0,100,10,20,30
t1,db,20,220,30,40,45
`)

	var out bytes.Buffer
	if err := RenderComparison(root, "base", "candidate", &out); err != nil {
		t.Fatal(err)
	}

	rendered := out.String()
	for _, want := range []string{"# Run Comparison", "WAL bytes delta", "`60`", "`120`"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, rendered)
		}
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
