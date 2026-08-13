package benchmarksampler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunWritesFixedOwnedMetricsAndMonotonicTimingEvidence(t *testing.T) {
	root, runDir := samplerFixture(t, "benchmark-t001")
	base := time.Date(2026, time.August, 12, 1, 2, 3, 0, time.UTC)
	nowCalls := 0
	result, err := Run(Options{
		Root:          root,
		RunDir:        runDir,
		ExpectedRunID: "benchmark-t001",
		Interval:      time.Nanosecond,
		Samples:       2,
		RecordTiming:  true,
		Context:       context.Background(),
		Now: func() time.Time {
			nowCalls++
			return base.Add(time.Duration(nowCalls) * time.Millisecond)
		},
		RunSample: func(context.Context) ([]byte, error) { return []byte(validSampleRow() + "\n"), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Samples != 2 || result.MetricsPath != filepath.Join(runDir, MetricsRelativePath) || result.TimingPath != filepath.Join(runDir, filepath.FromSlash(TimingRelativePath)) {
		t.Fatalf("unexpected result: %#v", result)
	}
	metrics, err := os.ReadFile(result.MetricsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(metrics)), "\n"); got != 2 {
		t.Fatalf("metrics evidence has %d data/header separators, want 2: %s", got, metrics)
	}
	timing, err := os.Open(result.TimingPath)
	if err != nil {
		t.Fatal(err)
	}
	defer timing.Close()
	if count, err := ParseTimingSource(timing); err != nil || count != 2 {
		t.Fatalf("timing source count=%d err=%v", count, err)
	}
}

func TestRunRejectsRunIDMismatchBeforeCreatingEvidence(t *testing.T) {
	root, runDir := samplerFixture(t, "benchmark-t001")
	_, err := Run(Options{
		Root: root, RunDir: runDir, ExpectedRunID: "benchmark-t002",
		Interval: time.Second, Samples: 1,
		RunSample: func(context.Context) ([]byte, error) { return []byte(validSampleRow()), nil },
	})
	if err == nil || !strings.Contains(err.Error(), "does not match bound run id") {
		t.Fatalf("Run() error = %v, want exact run binding rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(runDir, MetricsRelativePath)); !os.IsNotExist(statErr) {
		t.Fatalf("rejected sampler created evidence: %v", statErr)
	}
}

func TestRunRefusesToOverwriteEvidence(t *testing.T) {
	root, runDir := samplerFixture(t, "benchmark-t001")
	options := Options{
		Root: root, RunDir: runDir, ExpectedRunID: "benchmark-t001",
		Interval: time.Second, Samples: 1,
		RunSample: func(context.Context) ([]byte, error) { return []byte(validSampleRow()), nil },
	}
	if _, err := Run(options); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite sampler evidence") {
		t.Fatalf("second Run() error = %v, want immutable-evidence rejection", err)
	}
}

func samplerFixture(t *testing.T, runID string) (string, string) {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "runs", runID)
	for _, directory := range []string{
		filepath.Join(root, "scripts"), filepath.Join(root, "sql"),
		filepath.Join(runDir, "artifacts", "benchmark", "controls"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(root, "scripts", "psql.sh"), filepath.Join(root, "sql", "metrics_sample.sql")} {
		if err := os.WriteFile(file, []byte("fixture\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, runDir
}

func validSampleRow() string {
	fields := []string{"2026-08-12T01:02:03.000Z", "postgres"}
	for range 22 {
		fields = append(fields, "0")
	}
	fields = append(fields, "0/0")
	return strings.Join(fields, ",")
}
