package runreport

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRenderSummary(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "summary")
	runA := filepath.Join(base, "run-a")
	runB := filepath.Join(base, "run-b")
	series := filepath.Join(base, "repeat")
	if err := os.MkdirAll(series, 0o755); err != nil {
		t.Fatal(err)
	}

	writeSummaryRun(t, runA, "run-a", 100, 250, 1, 3)
	writeSummaryRun(t, runB, "run-b", 100, 350, 2, 4)
	writeFile(t, filepath.Join(series, "runs.tsv"), "iteration\trun_id\texit_code\tstatus\tmessage\trun_dir\n1\trun-a\t0\tpassed\tok\t"+runA+"\n2\trun-b\t0\tpassed\tok\t"+runB+"\n")

	var out bytes.Buffer
	if err := RenderSummaryWithClock(root, []string{series}, &out, fixedClock); err != nil {
		t.Fatal(err)
	}

	rendered := out.String()
	for _, want := range []string{
		"# Run Series Summary",
		"| `passed` | `2` |",
		"| `wal_bytes` | `2` | `150` | `200.000` | `250` | `50.000` |",
		"| `active_sessions` | `2` | `3` | `3.500` | `4` | `0.500` |",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderHistory(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "history")
	seriesA := filepath.Join(base, "series-a")
	seriesB := filepath.Join(base, "series-b")
	if err := os.MkdirAll(seriesA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(seriesB, 0o755); err != nil {
		t.Fatal(err)
	}

	writeHistoryRun(t, filepath.Join(seriesA, "run-a1"), "run-a1", 100, 200, 1, 3)
	writeHistoryRun(t, filepath.Join(seriesA, "run-a2"), "run-a2", 100, 300, 2, 4)
	writeHistoryRun(t, filepath.Join(seriesB, "run-b1"), "run-b1", 100, 400, 3, 5)
	writeHistoryRun(t, filepath.Join(seriesB, "run-b2"), "run-b2", 100, 600, 4, 6)
	writeFile(t, filepath.Join(seriesA, "runs.tsv"), "iteration\trun_id\texit_code\tstatus\tmessage\trun_dir\n1\trun-a1\t0\tpassed\tok\t"+filepath.Join(seriesA, "run-a1")+"\n2\trun-a2\t0\tpassed\tok\t"+filepath.Join(seriesA, "run-a2")+"\n")
	writeFile(t, filepath.Join(seriesB, "runs.tsv"), "iteration\trun_id\texit_code\tstatus\tmessage\trun_dir\n1\trun-b1\t0\tpassed\tok\t"+filepath.Join(seriesB, "run-b1")+"\n2\trun-b2\t0\tpassed\tok\t"+filepath.Join(seriesB, "run-b2")+"\n")

	var out bytes.Buffer
	if err := RenderHistoryWithClock(root, []string{seriesA, seriesB}, &out, fixedClock); err != nil {
		t.Fatal(err)
	}

	rendered := out.String()
	for _, want := range []string{
		"# Run History Comparison",
		"| `history/series-a` | `2` | `2` | `0` | `0` |",
		"| `history/series-b` | `2` | `2` | `0` | `0` |",
		"| `wal_bytes` | `150` | `400` | `250` |",
		"| `active_sessions` | `3.500` | `5.500` | `2` |",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, rendered)
		}
	}
}

func fixedClock() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func writeSummaryRun(t *testing.T, runDir string, runID string, walStart int, walEnd int, activeStart int, activeEnd int) {
	t.Helper()
	writeBaseRun(t, runDir, runID)
	writeFile(t, filepath.Join(runDir, "metrics.csv"), "sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes\n"+
		"t0,db,"+itoa(activeStart)+",0,0,0,5,0,10,0,1,100,10,0,0,0,0,0,0,10,0,"+itoa(walStart)+"\n"+
		"t1,db,"+itoa(activeEnd)+",1,1,1,8,1,15,0,2,150,40,1,0,0,0,1,20,20,0,"+itoa(walEnd)+"\n")
}

func writeHistoryRun(t *testing.T, runDir string, runID string, walStart int, walEnd int, activeStart int, activeEnd int) {
	t.Helper()
	writeBaseRun(t, runDir, runID)
	writeFile(t, filepath.Join(runDir, "metrics.csv"), "sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes\n"+
		"t0,db,"+itoa(activeStart)+",0,0,0,5,0,0,0,0,0,0,0,0,0,0,0,0,0,0,"+itoa(walStart)+"\n"+
		"t1,db,"+itoa(activeEnd)+",0,0,0,7,0,0,0,0,0,0,0,0,0,0,0,0,0,0,"+itoa(walEnd)+"\n")
}

func writeBaseRun(t *testing.T, runDir string, runID string) {
	t.Helper()
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "manifest.env"), "run_id="+runID+"\nexperiment_spec_id=smoke\nexperiment_pg_config=default\nprofile_size=small\nworkload_spec=sql/smoke-run\n")
	writeFile(t, filepath.Join(runDir, "verdict.env"), "status=passed\nmessage=ok\nworkload_exit=0\nassert_exit=0\nscan_exit=0\n")
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
