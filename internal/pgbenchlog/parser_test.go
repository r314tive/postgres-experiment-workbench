package pgbenchlog

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRealDockerAndNativePlainLogSubsets(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		wantMean float64
		wantMin  int64
		wantMax  int64
	}{
		{"docker", "docker-basic.log", 907.1666666666666, 463, 1889},
		{"native", "native-basic.log", 1757, 1261, 2429},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ParseFiles([]string{fixturePath(test.fixture)}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if result.SchemaVersion != ResultSchemaVersion || result.ParserVersion != ParserVersion {
				t.Fatalf("unexpected result identity: %#v", result)
			}
			if result.CompletionWindow.FirstEpochSeconds == 0 || result.CompletionWindow.LastEpochSeconds == 0 ||
				timestampLess(result.CompletionWindow.LastEpochSeconds, result.CompletionWindow.LastEpochMicroseconds, result.CompletionWindow.FirstEpochSeconds, result.CompletionWindow.FirstEpochMicroseconds) {
				t.Fatalf("transaction log completion window was not retained: %#v", result.CompletionWindow)
			}
			if result.Files != 1 || result.Logged != 6 || result.Completed != 6 || result.Failed != 0 || result.Skipped != 0 || result.Retried != 0 {
				t.Fatalf("unexpected counts: %#v", result)
			}
			if result.Sampled || result.SampleRate != 1 || result.ScheduleLagPresent || result.RetriesPresent || result.ScheduleLagUS != nil {
				t.Fatalf("unexpected protocol flags: %#v", result)
			}
			if result.LatencyUS == nil || result.LatencyUS.N != 6 || result.LatencyUS.Min != test.wantMin || result.LatencyUS.Max != test.wantMax || !closeLogFloat(result.LatencyUS.Mean, test.wantMean) {
				t.Fatalf("unexpected latency distribution: %#v", result.LatencyUS)
			}
		})
	}

	docker, err := ParseFiles([]string{fixturePath("docker-basic.log")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if docker.LatencyUS.P50 != 541 || docker.LatencyUS.P95 != 1800.75 || !closeLogFloat(docker.LatencyUS.P99, 1871.35) {
		t.Fatalf("unexpected type-7 percentiles: %#v", docker.LatencyUS)
	}
}

func TestParseFilesCombinesWorkersAsOneTrial(t *testing.T) {
	result, err := ParseFiles([]string{fixturePath("worker-b.log"), fixturePath("worker-a.log")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Logged != 6 || result.Completed != 6 {
		t.Fatalf("worker logs were not combined: %#v", result)
	}
	if result.LatencyUS == nil || result.LatencyUS.N != 6 || result.LatencyUS.Min != 100 || result.LatencyUS.Mean != 350 || result.LatencyUS.P50 != 350 || result.LatencyUS.P95 != 575 || result.LatencyUS.P99 != 595 || result.LatencyUS.Max != 600 {
		t.Fatalf("combined trial distribution is wrong: %#v", result.LatencyUS)
	}
	if result.CompletionWindow.FirstEpochSeconds != 1700000000 || result.CompletionWindow.FirstEpochMicroseconds != 100000 ||
		result.CompletionWindow.LastEpochSeconds != 1700000000 || result.CompletionWindow.LastEpochMicroseconds != 300100 {
		t.Fatalf("combined completion extent is wrong: %#v", result.CompletionWindow)
	}
}

func TestValidateCompletionWindowRequiresMeasureContainment(t *testing.T) {
	result, err := ParseReader(strings.NewReader(strings.Join([]string{
		"0 1 100 0 1700000000 100000",
		"0 2 100 0 1700000000 900000",
	}, "\n")+"\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	insideStart := time.Unix(1700000000, 0)
	insideFinish := time.Unix(1700000001, 0)
	if err := ValidateCompletionWindow(result, insideStart, insideFinish); err != nil {
		t.Fatalf("contained completion window failed: %v", err)
	}
	for _, test := range []struct {
		name     string
		started  time.Time
		finished time.Time
	}{
		{"starts too late", time.Unix(1700000000, 200000000), insideFinish},
		{"finishes too early", insideStart, time.Unix(1700000000, 800000000)},
		{"reversed phase", insideFinish, insideStart},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateCompletionWindow(result, test.started, test.finished); err == nil {
				t.Fatal("out-of-phase completion window passed")
			}
		})
	}
	tampered := result
	tampered.CompletionWindow.FirstEpochMicroseconds = 1_000_000
	if err := ValidateCompletionWindow(tampered, insideStart, insideFinish); err == nil {
		t.Fatal("malformed completion window passed")
	}
}

func TestParseScheduleLagAndRepeatedSkippedTransactionNumber(t *testing.T) {
	result, err := ParseFiles([]string{fixturePath("rate-skipped.log")}, Options{ScheduleLag: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Logged != 7 || result.Completed != 5 || result.Skipped != 2 || result.Failed != 0 {
		t.Fatalf("unexpected rate-limited counts: %#v", result)
	}
	if result.LatencyUS == nil || result.LatencyUS.N != 5 || result.LatencyUS.Min != 2465 || result.LatencyUS.Max != 6173 {
		t.Fatalf("skipped rows leaked into latency statistics: %#v", result.LatencyUS)
	}
	if result.ScheduleLagUS == nil || result.ScheduleLagUS.N != 7 || result.ScheduleLagUS.Min != 740 || result.ScheduleLagUS.Max != 5217 {
		t.Fatalf("schedule-lag rows were not normalized: %#v", result.ScheduleLagUS)
	}
}

func TestParseRetriesCountsTransactionsAndRetryAttempts(t *testing.T) {
	result, err := ParseFiles([]string{fixturePath("retries.log")}, Options{Retries: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Logged != 10 || result.Completed != 8 || result.Failed != 2 || result.Skipped != 0 || result.Retried != 6 || result.TotalRetries != 34 {
		t.Fatalf("unexpected retry counts: %#v", result)
	}
	if result.LatencyUS == nil || result.LatencyUS.N != 8 || result.LatencyUS.Min != 8307 || result.LatencyUS.Max != 72345 {
		t.Fatalf("failed rows leaked into latency statistics: %#v", result.LatencyUS)
	}
	if result.ScheduleLagUS != nil || !result.RetriesPresent {
		t.Fatalf("unexpected optional-column metadata: %#v", result)
	}
}

func TestParseScheduleLagAndRetriesTogether(t *testing.T) {
	result, err := ParseFiles([]string{fixturePath("rate-retries.log")}, Options{ScheduleLag: true, Retries: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Logged != 3 || result.Completed != 2 || result.Failed != 1 || result.Retried != 2 || result.TotalRetries != 5 {
		t.Fatalf("unexpected combined-layout counts: %#v", result)
	}
	if result.ScheduleLagUS == nil || result.ScheduleLagUS.N != 3 || result.ScheduleLagUS.P50 != 20 {
		t.Fatalf("unexpected lag distribution: %#v", result.ScheduleLagUS)
	}
}

func TestParseSampledLogDoesNotExtrapolateCounts(t *testing.T) {
	log := strings.Join([]string{
		"0 10 100 0 1700000000 100000",
		"0 30 300 0 1700000000 300000",
	}, "\n") + "\n"
	result, err := ParseReader(strings.NewReader(log), Options{SampleRate: 0.25})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Sampled || result.SampleRate != 0.25 {
		t.Fatalf("sample metadata is missing: %#v", result)
	}
	if result.Logged != 2 || result.Completed != 2 || result.LatencyUS == nil || result.LatencyUS.N != 2 {
		t.Fatalf("sample counts were extrapolated: %#v", result)
	}
}

func TestParseAllowsNoCompletedRowsWithoutInventingLatency(t *testing.T) {
	result, err := ParseReader(strings.NewReader("0 0 failed 0 1700000000 100000\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Logged != 1 || result.Failed != 1 || result.Completed != 0 || result.LatencyUS != nil {
		t.Fatalf("failed-only result invented latency evidence: %#v", result)
	}
}

func TestParseRequiresExplicitOptionalColumnLayout(t *testing.T) {
	sevenFields := "0 1 100 0 1700000000 100000 7\n"
	if _, err := ParseReader(strings.NewReader(sevenFields), Options{}); err == nil || !strings.Contains(err.Error(), "expected 6 fields") {
		t.Fatalf("ambiguous seventh column was inferred: %v", err)
	}
	lag, err := ParseReader(strings.NewReader(sevenFields), Options{ScheduleLag: true})
	if err != nil {
		t.Fatal(err)
	}
	if lag.ScheduleLagUS == nil || lag.ScheduleLagUS.Min != 7 || lag.Retried != 0 {
		t.Fatalf("declared schedule-lag column parsed incorrectly: %#v", lag)
	}
	retries, err := ParseReader(strings.NewReader(sevenFields), Options{Retries: true})
	if err != nil {
		t.Fatal(err)
	}
	if retries.Retried != 1 || retries.TotalRetries != 7 || retries.ScheduleLagUS != nil {
		t.Fatalf("declared retries column parsed incorrectly: %#v", retries)
	}
}

func TestParseRejectsMalformedUnknownTruncatedAndNegativeRows(t *testing.T) {
	tests := []struct {
		name    string
		log     string
		options Options
		want    string
	}{
		{"truncated", "0 1 100 0 1700000000\n", Options{}, "expected 6 fields"},
		{"unknown extra field", "0 1 100 0 1700000000 1 7\n", Options{}, "expected 6 fields"},
		{"aggregated format", "1700000000 10 1000 100000 10 200 0 0 0 0 0 0 0 0 0\n", Options{}, "expected 6 fields"},
		{"negative client", "-1 1 100 0 1700000000 1\n", Options{}, "client_id"},
		{"negative transaction", "0 -1 100 0 1700000000 1\n", Options{}, "transaction_no"},
		{"negative latency", "0 1 -100 0 1700000000 1\n", Options{}, "latency_us"},
		{"negative script", "0 1 100 -1 1700000000 1\n", Options{}, "script_no"},
		{"negative epoch", "0 1 100 0 -1 1\n", Options{}, "epoch_seconds"},
		{"negative epoch micros", "0 1 100 0 1700000000 -1\n", Options{}, "epoch_microseconds"},
		{"epoch micros range", "0 1 100 0 1700000000 1000000\n", Options{}, "between 0 and 999999"},
		{"negative lag", "0 1 100 0 1700000000 1 -1\n", Options{ScheduleLag: true}, "schedule_lag_us"},
		{"negative retries", "0 1 100 0 1700000000 1 -1\n", Options{Retries: true}, "retries"},
		{"non-finite latency", "0 1 NaN 0 1700000000 1\n", Options{}, "latency_us"},
		{"leading zero", "00 1 100 0 1700000000 1\n", Options{}, "canonical non-negative integer"},
		{"integer overflow", "9223372036854775808 1 100 0 1700000000 1\n", Options{}, "outside int64 range"},
		{"blank row", "0 1 100 0 1700000000 1\n\n0 2 100 0 1700000000 2\n", Options{}, "blank lines"},
		{"skipped without rate", "0 1 skipped 0 1700000000 1\n", Options{}, "skipped requires"},
		{"skipped with retries", "0 1 skipped 0 1700000000 1 10 2\n", Options{ScheduleLag: true, Retries: true}, "cannot have retries"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseReader(strings.NewReader(test.log), test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestParseRejectsDetailedFailureTokensOutsideSupportedSubset(t *testing.T) {
	for _, token := range []string{"serialization", "deadlock", "other"} {
		t.Run(token, func(t *testing.T) {
			_, err := ParseReader(strings.NewReader("0 0 "+token+" 0 1700000000 1 0\n"), Options{Retries: true})
			if err == nil || !strings.Contains(err.Error(), "--failures-detailed") {
				t.Fatalf("unexpected error for %s: %v", token, err)
			}
		})
	}
}

func TestParseRejectsSequenceAndTimestampContradictions(t *testing.T) {
	tests := []struct {
		name    string
		log     string
		options Options
		want    string
	}{
		{
			"backwards transaction",
			"0 2 100 0 1700000000 100000\n0 1 100 0 1700000000 200000\n",
			Options{},
			"moves backwards",
		},
		{
			"duplicate completed transaction",
			"0 1 100 0 1700000000 100000\n0 1 100 0 1700000000 200000\n",
			Options{},
			"already completed or failed",
		},
		{
			"unsampled gap",
			"0 1 100 0 1700000000 100000\n0 3 100 0 1700000000 300000\n",
			Options{},
			"unsampled sequence gap",
		},
		{
			"timestamp backwards",
			"0 1 100 0 1700000000 200000\n0 2 100 0 1700000000 100000\n",
			Options{},
			"timestamp moves backwards",
		},
		{
			"advance after skipped only",
			"0 1 skipped 0 1700000000 100000 10\n0 2 100 0 1700000000 200000 20\n",
			Options{ScheduleLag: true},
			"advanced after only skipped",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseReader(strings.NewReader(test.log), test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestParseRejectsClientSplitAcrossWorkerFiles(t *testing.T) {
	_, err := Parse([]Source{
		{Name: "worker-0", Reader: strings.NewReader("0 1 100 0 1700000000 100000\n")},
		{Name: "worker-1", Reader: strings.NewReader("0 2 100 0 1700000000 200000\n")},
	}, Options{})
	if err == nil || !strings.Contains(err.Error(), "also appears in worker log") {
		t.Fatalf("split client sequence was not rejected: %v", err)
	}
}

func TestParseRejectsInvalidInputsAndSampleRates(t *testing.T) {
	if _, err := Parse(nil, Options{}); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("unexpected no-source error: %v", err)
	}
	if _, err := Parse([]Source{{Name: "nil"}}, Options{}); err == nil || !strings.Contains(err.Error(), "nil reader") {
		t.Fatalf("unexpected nil-reader error: %v", err)
	}
	if _, err := Parse([]Source{
		{Name: "same", Reader: strings.NewReader("0 1 1 0 1 1\n")},
		{Name: "same", Reader: strings.NewReader("1 1 1 0 1 1\n")},
	}, Options{}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("unexpected duplicate-source error: %v", err)
	}
	if _, err := ParseReader(strings.NewReader(""), Options{}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("unexpected empty-source error: %v", err)
	}
	for _, sampleRate := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		if _, err := ParseReader(strings.NewReader("0 1 1 0 1 1\n"), Options{SampleRate: sampleRate}); err == nil || !strings.Contains(err.Error(), "sample rate") {
			t.Fatalf("invalid sample rate %v was accepted: %v", sampleRate, err)
		}
	}
}

func TestParseFilesRejectsUnsafeAndDuplicatePaths(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "pgbench.1")
	if err := os.WriteFile(regular, []byte("0 1 1 0 1 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFiles([]string{regular, regular}, Options{}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate path was accepted: %v", err)
	}
	if _, err := ParseFiles([]string{dir}, Options{}); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("directory path was accepted: %v", err)
	}
}

func TestResultJSONIsDeterministicAndExplicit(t *testing.T) {
	result, err := ParseReader(strings.NewReader("0 1 100 0 1700000000 1\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := result.JSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := result.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || len(first) == 0 || first[len(first)-1] != '\n' {
		t.Fatalf("result JSON is not deterministic with a trailing newline:\n%s", first)
	}
	for _, want := range []string{
		`"schema_version": "pgworkbench.pgbench-log-result/v2"`,
		`"sampled": false`,
		`"logged": 1`,
		`"completion_window"`,
		`"schedule_lag_us": null`,
	} {
		if !bytes.Contains(first, []byte(want)) {
			t.Fatalf("result JSON is missing %q:\n%s", want, first)
		}
	}
}

func fixturePath(name string) string {
	return filepath.Join("testdata", name)
}

func closeLogFloat(left, right float64) bool {
	return math.Abs(left-right) <= 1e-12*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}
