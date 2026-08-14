package benchmarksampler

import (
	"context"
	"encoding/csv"
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
	sampleCalls := 0
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
		RunSample: func(context.Context) ([]byte, error) {
			sampleCalls++
			if sampleCalls == 2 {
				marker, err := os.Lstat(filepath.Join(runDir, ReadyRelativePath))
				if err != nil || !marker.IsDir() || marker.Mode().Perm() != 0o700 {
					t.Fatalf("readiness token was not published after first sample: info=%v err=%v", marker, err)
				}
			}
			return []byte(validSampleRow() + "\n"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Samples != 2 || result.MetricsPath != filepath.Join(runDir, MetricsRelativePath) || result.TimingPath != filepath.Join(runDir, filepath.FromSlash(TimingRelativePath)) || result.ReadyPath != filepath.Join(runDir, ReadyRelativePath) {
		t.Fatalf("unexpected result: %#v", result)
	}
	markerInfo, err := os.Lstat(result.ReadyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !markerInfo.IsDir() || markerInfo.Mode().Perm() != 0o700 {
		t.Fatalf("readiness token mode = %s, want directory 0700", markerInfo.Mode())
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

func TestRunDoesNotPublishReadinessForInvalidFirstSample(t *testing.T) {
	root, runDir := samplerFixture(t, "benchmark-t001")
	fields := validSampleFields()
	fields[2] = "-1"
	_, err := Run(Options{
		Root: root, RunDir: runDir, ExpectedRunID: "benchmark-t001",
		Interval: time.Second, Samples: 1,
		RunSample: func(context.Context) ([]byte, error) { return []byte(encodeSampleRow(t, fields)), nil },
	})
	if err == nil || !strings.Contains(err.Error(), "active_sessions") {
		t.Fatalf("Run() error = %v, want typed row rejection", err)
	}
	if _, statErr := os.Lstat(filepath.Join(runDir, ReadyRelativePath)); !os.IsNotExist(statErr) {
		t.Fatalf("failed first sample published readiness: %v", statErr)
	}
}

func TestRunRefusesExistingReadinessBeforeCreatingMetrics(t *testing.T) {
	root, runDir := samplerFixture(t, "benchmark-t001")
	readyPath := filepath.Join(runDir, ReadyRelativePath)
	if err := os.WriteFile(readyPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Options{
		Root: root, RunDir: runDir, ExpectedRunID: "benchmark-t001",
		Interval: time.Second, Samples: 1,
		RunSample: func(context.Context) ([]byte, error) { return []byte(validSampleRow()), nil },
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite sampler evidence") {
		t.Fatalf("Run() error = %v, want immutable readiness rejection", err)
	}
	if _, statErr := os.Lstat(filepath.Join(runDir, MetricsRelativePath)); !os.IsNotExist(statErr) {
		t.Fatalf("stale readiness rejection created metrics evidence: %v", statErr)
	}
}

func TestRunRefusesSymlinkReadinessWithoutTouchingTarget(t *testing.T) {
	root, runDir := samplerFixture(t, "benchmark-t001")
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(runDir, ReadyRelativePath)); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Options{
		Root: root, RunDir: runDir, ExpectedRunID: "benchmark-t001",
		Interval: time.Second, Samples: 1,
		RunSample: func(context.Context) ([]byte, error) { return []byte(validSampleRow()), nil },
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite sampler evidence") {
		t.Fatalf("Run() error = %v, want symlink readiness rejection", err)
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil || string(content) != "outside\n" {
		t.Fatalf("readiness target changed: content=%q err=%v", content, readErr)
	}
}

func TestValidateSampleRowUsesTypedQuoteAwareCSVContract(t *testing.T) {
	validQuoted := validSampleFields()
	validQuoted[1] = "bench,marked"
	hugeCounter := validSampleFields()
	hugeCounter[23] = strings.Repeat("9", 100)

	tests := []struct {
		name    string
		fields  []string
		content string
		want    string
	}{
		{name: "quoted database comma", fields: validQuoted},
		{name: "arbitrary precision counter", fields: hugeCounter},
		{name: "non UTC timestamp", fields: replaceSampleField(0, "2026-08-12T01:02:03+00:00"), want: "UTC RFC3339Nano"},
		{name: "empty database", fields: replaceSampleField(1, ""), want: "database_name"},
		{name: "negative gauge", fields: replaceSampleField(2, "-1"), want: "active_sessions"},
		{name: "overflowing gauge", fields: replaceSampleField(7, "18446744073709551616"), want: "outside uint64"},
		{name: "noncanonical counter", fields: replaceSampleField(8, "01"), want: "xact_commit"},
		{name: "lowercase LSN", fields: replaceSampleField(24, "a/b"), want: "current_wal_lsn"},
		{name: "wrong field count", content: "2026-08-12T01:02:03.000Z,postgres", want: "wrong number of fields"},
		{name: "multiple rows", content: validSampleRow() + "\n" + validSampleRow(), want: "exactly one CSV row"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := test.content
			if test.fields != nil {
				content = encodeSampleRow(t, test.fields)
			}
			row, err := validateSampleRow([]byte(content))
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if string(row) != strings.TrimSuffix(content, "\n") {
					t.Fatalf("validated row changed raw CSV: got %q want %q", row, content)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateSampleRow() error = %v, want %q", err, test.want)
			}
		})
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
	return strings.TrimSuffix(encodeSampleRowUnchecked(validSampleFields()), "\n")
}

func validSampleFields() []string {
	fields := []string{"2026-08-12T01:02:03.000Z", "postgres"}
	for range 22 {
		fields = append(fields, "0")
	}
	fields = append(fields, "0/0")
	return fields
}

func replaceSampleField(index int, value string) []string {
	fields := validSampleFields()
	fields[index] = value
	return fields
}

func encodeSampleRow(t *testing.T, fields []string) string {
	t.Helper()
	return encodeSampleRowUnchecked(fields)
}

func encodeSampleRowUnchecked(fields []string) string {
	var output strings.Builder
	writer := csv.NewWriter(&output)
	if err := writer.Write(fields); err != nil {
		panic(err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		panic(err)
	}
	return output.String()
}
