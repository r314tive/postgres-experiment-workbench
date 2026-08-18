package pgbenchresult

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePostgreSQL15TransactionSummary(t *testing.T) {
	result := parseFixture(t, "pg15-transactions.txt")

	if result.SchemaVersion != ResultSchemaVersion || result.ParserVersion != ParserVersion {
		t.Fatalf("unexpected contract identity: %#v", result)
	}
	if result.PgbenchVersion != "15.17" || result.ServerVersion != "15.17" {
		t.Fatalf("unexpected versions: %#v", result)
	}
	if result.TransactionType != "<builtin: TPC-B (sort of)>" || result.QueryMode != "simple" {
		t.Fatalf("unexpected workload identity: %#v", result)
	}
	if result.ScaleFactor != 10 || result.Clients != 10 || result.Threads != 2 || result.MaximumTries != 1 {
		t.Fatalf("unexpected dimensions: %#v", result)
	}
	if result.Mode != ModeTransactions || result.TransactionsPerClient == nil || *result.TransactionsPerClient != 1000 {
		t.Fatalf("unexpected transaction mode: %#v", result)
	}
	if result.TransactionsExpected == nil || *result.TransactionsExpected != 10000 || result.TransactionsProcessed != 10000 {
		t.Fatalf("unexpected transaction counts: %#v", result)
	}
	if result.TransactionsFailed != 0 || result.TransactionsRetried != nil || result.TotalRetries != nil {
		t.Fatalf("unexpected failure/retry counts: %#v", result)
	}
	if result.LatencyMeanMS != 11.013 || result.LatencyStddevMS == nil || *result.LatencyStddevMS != 7.351 {
		t.Fatalf("unexpected latency: %#v", result)
	}
	if result.TPSExcludingConnections == nil || *result.TPSExcludingConnections != 896.967014 || result.TPSIncludingConnections != nil {
		t.Fatalf("unexpected TPS: %#v", result)
	}
}

func TestEffectiveServerMajorHandlesExplicitAndCollapsedBanners(t *testing.T) {
	for _, test := range []struct {
		name     string
		result   Result
		expected string
	}{
		{name: "explicit", result: Result{PgbenchVersion: "18.2", ServerVersion: "17.9"}, expected: "17"},
		{name: "collapsed release", result: Result{PgbenchVersion: "18.2"}, expected: "18"},
		{name: "collapsed devel", result: Result{PgbenchVersion: "19devel"}, expected: "19"},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := EffectiveServerMajor(test.result)
			if err != nil || actual != test.expected {
				t.Fatalf("EffectiveServerMajor() = %q, %v; want %q", actual, err, test.expected)
			}
		})
	}
	if err := ValidateServerMajor(Result{PgbenchVersion: "18.2", ServerVersion: "17.9"}, "18"); err == nil {
		t.Fatal("expected a pgbench banner/manifest major mismatch")
	}
}

func TestParseRejectsUnsupportedServerMajor(t *testing.T) {
	fixture := strings.Replace(readFixture(t, "pg15-transactions.txt"), "pgbench (15.17, server 15.17)", "pgbench (15.17, server 14.22)", 1)
	if _, err := Parse(strings.NewReader(fixture)); err == nil || !strings.Contains(err.Error(), "server_version") {
		t.Fatalf("expected unsupported server major error, got %v", err)
	}
}

func TestParsePostgreSQL17RetrySummary(t *testing.T) {
	result := parseFixture(t, "pg17-retries.txt")

	if result.MaximumTries != 10 || result.TransactionsProcessed != 6317 || result.TransactionsFailed != 3683 {
		t.Fatalf("unexpected retry result counts: %#v", result)
	}
	if result.TransactionsRetried == nil || *result.TransactionsRetried != 7667 {
		t.Fatalf("unexpected retried transactions: %#v", result)
	}
	if result.TotalRetries == nil || *result.TotalRetries != 45339 {
		t.Fatalf("unexpected total retries: %#v", result)
	}
}

func TestParsePostgreSQL19TimeSummaryWithPlainProcessedCount(t *testing.T) {
	result := parseFixture(t, "pg19-time.txt")

	if result.Mode != ModeTime || result.DurationSeconds == nil || *result.DurationSeconds != 60 {
		t.Fatalf("unexpected time mode: %#v", result)
	}
	if result.TransactionsExpected != nil || result.TransactionsProcessed != 4153490 {
		t.Fatalf("unexpected processed count: %#v", result)
	}
	if result.QueryMode != "prepared" || result.TPSExcludingConnections == nil || *result.TPSExcludingConnections != 69224.826220 {
		t.Fatalf("unexpected measured result: %#v", result)
	}
}

func TestParseConnectionTPSVariants(t *testing.T) {
	result := parseFixture(t, "connection-tps.txt")
	if result.TPSIncludingConnections == nil || *result.TPSIncludingConnections != 2394.718707 {
		t.Fatalf("unexpected including-connections TPS: %#v", result)
	}
	if result.TPSExcludingConnections == nil || *result.TPSExcludingConnections != 2394.874350 {
		t.Fatalf("unexpected excluding-connections TPS: %#v", result)
	}
}

func TestParsePostgreSQL16ReconnectSummary(t *testing.T) {
	result := parseFixture(t, "pg16-reconnect.txt")
	if result.Mode != ModeTime || result.AverageConnectionTimeMS == nil || *result.AverageConnectionTimeMS != 1.455 {
		t.Fatalf("unexpected reconnect result: %#v", result)
	}
	if result.TPSIncludingConnections == nil || *result.TPSIncludingConnections != 684.615907 {
		t.Fatalf("unexpected reconnect TPS: %#v", result)
	}
}

func TestParsePostgreSQL18DetailedRateSummary(t *testing.T) {
	result := parseFixture(t, "pg18-rate-detailed.txt")
	if result.Mode != ModeTime || result.TransactionsFailed != 3 {
		t.Fatalf("unexpected rate result: %#v", result)
	}
	if result.SerializationFailures == nil || *result.SerializationFailures != 1 ||
		result.DeadlockFailures == nil || *result.DeadlockFailures != 1 ||
		result.OtherFailures == nil || *result.OtherFailures != 1 {
		t.Fatalf("unexpected detailed failure counts: %#v", result)
	}
	if result.TransactionsSkipped == nil || *result.TransactionsSkipped != 7 {
		t.Fatalf("unexpected skipped count: %#v", result)
	}
	if result.LatencyLimitMS == nil || *result.LatencyLimitMS != 25 ||
		result.TransactionsAboveLimit == nil || *result.TransactionsAboveLimit != 20 ||
		result.LatencyLimitTotal == nil || *result.LatencyLimitTotal != 2990 {
		t.Fatalf("unexpected latency-limit result: %#v", result)
	}
	if result.LatencyMeanMS != 10.5 || result.LatencyStddevMS == nil || *result.LatencyStddevMS != 3.25 {
		t.Fatalf("unexpected rate latency: %#v", result)
	}
	if result.ScheduleLagAverageMS == nil || *result.ScheduleLagAverageMS != 1.25 ||
		result.ScheduleLagMaxMS == nil || *result.ScheduleLagMaxMS != 4.5 {
		t.Fatalf("unexpected schedule lag: %#v", result)
	}
}

func TestParseUnlimitedRetriesWithoutMaximumTriesLine(t *testing.T) {
	fixture := strings.Replace(readFixture(t, "pg17-retries.txt"), "maximum number of tries: 10\n", "", 1)
	result, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if result.MaximumTries != 0 || result.TransactionsRetried == nil || result.TotalRetries == nil {
		t.Fatalf("unexpected unlimited retry result: %#v", result)
	}
}

func TestParseSelectsLastMeasuredSummary(t *testing.T) {
	first := readFixture(t, "pg15-transactions.txt")
	last := readFixture(t, "pg19-time.txt")
	result, err := Parse(strings.NewReader(first + "\n" + last))
	if err != nil {
		t.Fatal(err)
	}
	if result.PgbenchVersion != "19devel" || result.Mode != ModeTime || result.Clients != 10 {
		t.Fatalf("parser did not select last measured summary: %#v", result)
	}
}

func TestParseAcceptsPlainProcessedCountInTransactionMode(t *testing.T) {
	fixture := strings.Replace(readFixture(t, "pg15-transactions.txt"), "10000/10000", "10000", 1)
	result, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if result.TransactionsExpected == nil || *result.TransactionsExpected != 10000 {
		t.Fatalf("expected count was not derived: %#v", result)
	}
}

func TestParseRejectsLocaleUnknownAndTruncatedOutput(t *testing.T) {
	for _, name := range []string{"localized.txt", "unknown-field.txt", "truncated.txt"} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(readFixture(t, name)))
			if err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
			var want string
			switch name {
			case "localized.txt":
				want = "transaction type not found"
			case "unknown-field.txt":
				want = "unknown summary field"
			case "truncated.txt":
				want = "required field is missing"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("unexpected error for %s: %v", name, err)
			}
		})
	}
}

func TestParseRejectsMalformedAndInconsistentSummaries(t *testing.T) {
	base := readFixture(t, "pg15-transactions.txt")
	tests := []struct {
		name    string
		old     string
		new     string
		message string
	}{
		{"decimal comma", "latency average = 11.013 ms", "latency average = 11,013 ms", "canonical non-negative decimal"},
		{"duplicate", "latency stddev = 7.351 ms", "latency stddev = 7.351 ms\nlatency stddev = 7.351 ms", "duplicate field"},
		{"mixed mode", "number of transactions per client: 1000", "number of transactions per client: 1000\nduration: 60 s", "exactly one"},
		{"bad denominator", "10000/10000", "10000/9000", "clients*transactions_per_client"},
		{"unknown tps qualifier", "without initial connection time", "excluding warmup", "unsupported qualifier"},
		{"zero processed", "10000/10000", "0/10000", "must be positive"},
		{"retry fields with one try", "number of failed transactions: 0 (0.000%)", "number of failed transactions: 0 (0.000%)\nnumber of transactions retried: 1 (0.010%)\ntotal number of retries: 1", "inconsistent with maximum number of tries 1"},
		{"failure breakdown exceeds total", "number of failed transactions: 0 (0.000%)", "number of failed transactions: 0 (0.000%)\nnumber of serialization failures: 1 (0.010%)", "detailed failure counts exceed"},
		{"latency denominator mismatch", "latency average = 11.013 ms", "number of transactions above the 25.0 ms latency limit: 1/9999 (0.010%)\nlatency average = 11.013 ms", "denominator must equal completed"},
		{"schedule average above max", "latency average = 11.013 ms", "rate limit schedule lag: avg 5.000 (max 4.000) ms\nlatency average = 11.013 ms", "average schedule lag exceeds"},
		{"unsupported pgbench major", "pgbench (15.17, server 15.17)", "pgbench (20.0, server 20.0)", "supported majors are 15 through 19"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := strings.Replace(base, test.old, test.new, 1)
			_, err := Parse(strings.NewReader(fixture))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}

func TestParseRequiresVersionBanner(t *testing.T) {
	fixture := strings.Replace(readFixture(t, "pg15-transactions.txt"), "pgbench (15.17, server 15.17)\n", "", 1)
	_, err := Parse(strings.NewReader(fixture))
	if err == nil || !strings.Contains(err.Error(), "version banner is missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDoesNotInheritBannerFromEarlierMeasuredSummary(t *testing.T) {
	first := readFixture(t, "pg15-transactions.txt")
	last := strings.Replace(readFixture(t, "pg19-time.txt"), "pgbench (19devel, server 19devel)\n", "", 1)
	_, err := Parse(strings.NewReader(first + "\n" + last))
	if err == nil || !strings.Contains(err.Error(), "version banner is missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRequiresRetryFieldsWhenRetriesAreEnabled(t *testing.T) {
	fixture := strings.Replace(readFixture(t, "pg15-transactions.txt"), "maximum number of tries: 1", "maximum number of tries: 10", 1)
	_, err := Parse(strings.NewReader(fixture))
	if err == nil || !strings.Contains(err.Error(), "requires retried transactions and total retries") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAcceptsLatencyMeanIncludingFailures(t *testing.T) {
	fixture := strings.Replace(readFixture(t, "pg15-transactions.txt"), "latency average = 11.013 ms", "latency average = 11.013 ms (including failures)", 1)
	result, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if result.LatencyMeanMS != 11.013 {
		t.Fatalf("unexpected latency mean: %#v", result)
	}
}

func TestResultJSONIsDeterministicAndVersioned(t *testing.T) {
	result := parseFixture(t, "pg15-transactions.txt")
	first, err := result.JSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := result.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("deterministic JSON changed between calls")
	}
	for _, want := range []string{
		`"schema_version": "pgworkbench.pgbench-result/v1"`,
		`"parser_version": "1.2.0"`,
		`"transactions_processed": 10000`,
		`"transactions_skipped": null`,
		`"transactions_retried": null`,
	} {
		if !bytes.Contains(first, []byte(want)) {
			t.Fatalf("JSON missing %q:\n%s", want, first)
		}
	}
	if len(first) == 0 || first[len(first)-1] != '\n' {
		t.Fatal("deterministic JSON must have a trailing newline")
	}
}

func parseFixture(t *testing.T, name string) Result {
	t.Helper()
	result, err := Parse(strings.NewReader(readFixture(t, name)))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
