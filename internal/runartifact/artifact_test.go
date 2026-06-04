package runartifact

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRunDir(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveRunDir(root, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != runDir {
		t.Fatalf("expected %q, got %q", runDir, resolved)
	}
}

func TestMetricStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.csv")
	content := "sampled_at,wal_bytes,temp_bytes\nt0,100,0\nt1,250,20\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := MetricStat(path, "wal_bytes")
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Valid || stats.Count != 2 || stats.First != 100 || stats.Last != 250 || stats.Delta != 150 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestListRelativeFiles(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"snapshots/b.txt", "snapshots/a.txt"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ListRelativeFiles(root, "snapshots", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "snapshots/a.txt" || files[1] != "snapshots/b.txt" {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestCollectRunDirsFromSeries(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	seriesDir := filepath.Join(root, "series")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.env"), []byte("run_id=run-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runsTSV := "iteration\trun_id\texit_code\tstatus\tmessage\trun_dir\n1\trun-a\t0\tpassed\tok\t" + runDir + "\n"
	if err := os.WriteFile(filepath.Join(seriesDir, "runs.tsv"), []byte(runsTSV), 0o644); err != nil {
		t.Fatal(err)
	}

	runDirs, err := CollectRunDirs(root, []string{seriesDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(runDirs) != 1 || runDirs[0] != runDir {
		t.Fatalf("unexpected run dirs: %#v", runDirs)
	}
}
