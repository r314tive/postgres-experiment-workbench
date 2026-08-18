package pgbenchresult

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTPSIntegrityAcceptsPostgreSQL15Through19SummarySemantics(t *testing.T) {
	tests := []struct {
		fixture        string
		measureSeconds float64
	}{
		{"pg15-transactions.txt", 11.25},
		{"pg16-reconnect.txt", 10.10},
		{"pg17-retries.txt", 33.95},
		{"pg18-rate-detailed.txt", 30.10},
		{"pg19-time.txt", 60.10},
		{"connection-tps.txt", 41.90},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			result := parseFixture(t, test.fixture)
			if err := ValidateTPSIntegrity(result, secondsDuration(test.measureSeconds)); err != nil {
				t.Fatalf("valid PG15-19 TPS evidence rejected: %v", err)
			}
		})
	}
}

func TestTPSIntegrityRejectsCountRateAndMeasureTampering(t *testing.T) {
	valid := readFixture(t, "pg19-time.txt")
	tests := []struct {
		name           string
		summary        string
		measureSeconds float64
	}{
		{
			name:           "processed count",
			summary:        strings.Replace(valid, "4153490", "8306980", 1),
			measureSeconds: 60.1,
		},
		{
			name:           "reported TPS",
			summary:        strings.Replace(valid, "69224.826220", "138449.652440", 1),
			measureSeconds: 60.1,
		},
		{
			name:           "external measure interval",
			summary:        valid,
			measureSeconds: 120,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Parse(strings.NewReader(test.summary))
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateTPSIntegrity(result, secondsDuration(test.measureSeconds))
			if err == nil || !strings.Contains(err.Error(), "inconsistent with processed transactions and elapsed measure time") {
				t.Fatalf("TPS tampering passed: %v", err)
			}
		})
	}
}

func TestTPSIntegrityUsesPrintedDecimalPrecisionAtBoundary(t *testing.T) {
	coarse := parseFixedTransactionSummary(t, "10.0")
	precise := parseFixedTransactionSummary(t, "10.000")
	measure := secondsDuration(10.64)
	if err := ValidateTPSIntegrity(coarse, measure); err != nil {
		t.Fatalf("coarse printed rounding interval should pass boundary: %v", err)
	}
	if err := ValidateTPSIntegrity(precise, measure); err == nil {
		t.Fatal("more precise printed TPS incorrectly inherited a coarse rounding allowance")
	}
}

func TestTPSIntegrityCoversFixedTransactionsWithExternalElapsedTime(t *testing.T) {
	result := parseFixture(t, "pg15-transactions.txt")
	implied := float64(result.TransactionsProcessed)/(*result.TPSExcludingConnections) + *result.InitialConnectionTimeMS/1000
	if err := ValidateTPSIntegrity(result, secondsDuration(implied+0.1)); err != nil {
		t.Fatalf("valid fixed-transactions elapsed evidence rejected: %v", err)
	}
	if err := ValidateTPSIntegrity(result, secondsDuration(implied*2)); err == nil {
		t.Fatal("fixed-transactions TPS passed against a doubled measure duration")
	}
}

func TestLatencyIntegrityBindsOrdinarySummaryToTPS(t *testing.T) {
	for _, fixture := range []string{"pg16-reconnect.txt", "connection-tps.txt"} {
		t.Run(fixture, func(t *testing.T) {
			if err := ValidateLatencyIntegrity(parseFixture(t, fixture)); err != nil {
				t.Fatalf("valid global-window latency rejected: %v", err)
			}
		})
	}

	tampered := strings.Replace(readFixture(t, "pg16-reconnect.txt"), "latency average = 21.910 ms", "latency average = 21.920 ms", 1)
	parsed, err := Parse(strings.NewReader(tampered))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLatencyIntegrity(parsed); err == nil || !strings.Contains(err.Error(), "derived global interval") {
		t.Fatalf("TPS-inconsistent latency passed: %v", err)
	}
}

func TestLatencyIntegrityUsesHistoricalIncludingConnectionWindow(t *testing.T) {
	historical := strings.Join([]string{
		"pgbench (15.17, server 15.17)",
		"transaction type: <builtin: select only>",
		"scaling factor: 1",
		"query mode: simple",
		"number of clients: 1",
		"number of threads: 1",
		"maximum number of tries: 1",
		"number of transactions per client: 100",
		"number of transactions actually processed: 100/100",
		"number of failed transactions: 0 (0.000%)",
		"latency average = 100.000 ms",
		"tps = 10.000000 (including connections establishing)",
		"tps = 20.000000 (excluding connections establishing)",
	}, "\n") + "\n"
	result, err := Parse(strings.NewReader(historical))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLatencyIntegrity(result); err != nil {
		t.Fatalf("historical time_include latency/TPS relation was rejected: %v", err)
	}

	excludingOnly := strings.Replace(historical, "tps = 10.000000 (including connections establishing)\n", "", 1)
	result, err = Parse(strings.NewReader(excludingOnly))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLatencyIntegrity(result); err == nil || !strings.Contains(err.Error(), "cannot derive the historical global window") {
		t.Fatalf("historical excluding-only summary did not fail closed: %v", err)
	}
}

func TestLatencyIntegrityLeavesDetailedAccumulatorToRawLogGate(t *testing.T) {
	detailed := parseFixture(t, "pg19-time.txt")
	detailed.LatencyMeanMS = 999
	if err := ValidateLatencyIntegrity(detailed); err != nil {
		t.Fatalf("detailed summary was incorrectly treated as global-window latency: %v", err)
	}
}

func TestLatencyIntegrityAcceptsPgbenchRoundedGeneratedIntervals(t *testing.T) {
	tests := []struct {
		clients   int64
		processed int64
		failed    int64
		skipped   int64
		elapsed   float64
		reconnect bool
	}{
		{clients: 1, processed: 10_000, elapsed: 0.523},
		{clients: 4, processed: 11_337, elapsed: 4.012345},
		{clients: 16, processed: 87_321, failed: 17, skipped: 13, elapsed: 2.710019},
		{clients: 64, processed: 1_000_003, failed: 3, elapsed: 61.234567, reconnect: true},
	}
	for index, test := range tests {
		t.Run(fmt.Sprintf("case-%d", index+1), func(t *testing.T) {
			result := parseGeneratedOrdinarySummary(t, test.clients, test.processed, test.failed, test.skipped, test.elapsed, test.reconnect)
			if err := ValidateLatencyIntegrity(result); err != nil {
				t.Fatalf("valid independently rounded intervals did not intersect: %v", err)
			}
		})
	}
}

func TestRawLatencyMeanUsesExactOneSidedOrdinaryBound(t *testing.T) {
	ordinary := parseGeneratedOrdinarySummary(t, 1, 10_000, 0, 0, 4.04, false)
	for _, raw := range []float64{0.390826, 0.1, 0} {
		if err := ValidateRawLatencyMean(ordinary, raw); err != nil {
			t.Fatalf("valid raw latency lower than the global window was rejected: raw=%f err=%v", raw, err)
		}
	}
	if err := ValidateRawLatencyMean(ordinary, 0.4041); err == nil || !strings.Contains(err.Error(), "derived global upper bound") {
		t.Fatalf("raw latency above the TPS-derived global bound passed: %v", err)
	}
	ordinary.TransactionsFailed = 1
	if err := ValidateRawLatencyMean(ordinary, 0.390826); err == nil || !strings.Contains(err.Error(), "requires zero failed and skipped") {
		t.Fatalf("successful-only raw mean was compared to a mixed-outcome summary: %v", err)
	}

	detailed := parseFixture(t, "pg19-time.txt")
	if err := ValidateRawLatencyMean(detailed, detailed.LatencyMeanMS-0.0004); err != nil {
		t.Fatalf("detailed raw mean inside printed interval was rejected: %v", err)
	}
	if err := ValidateRawLatencyMean(detailed, detailed.LatencyMeanMS-0.0006); err == nil || !strings.Contains(err.Error(), "outside printed summary interval") {
		t.Fatalf("detailed raw mean outside printed interval passed: %v", err)
	}
}

func parseGeneratedOrdinarySummary(t *testing.T, clients, processed, failed, skipped int64, elapsed float64, reconnect bool) Result {
	t.Helper()
	total := processed + failed + skipped
	latencyMS := 1000 * elapsed * float64(clients) / float64(total)
	tps := float64(processed) / elapsed
	qualifier := "without initial connection time"
	connectionLine := "initial connection time = 1.000 ms"
	if reconnect {
		qualifier = "including reconnection times"
		connectionLine = "average connection time = 0.100 ms"
	}
	summary := fmt.Sprintf(strings.Join([]string{
		"pgbench (19devel, server 19devel)",
		"transaction type: <builtin: select only>",
		"scaling factor: 1",
		"query mode: simple",
		"number of clients: %d",
		"number of threads: 1",
		"maximum number of tries: 1",
		"duration: 10 s",
		"number of transactions actually processed: %d",
		"number of failed transactions: %d (0.000%%)",
		"number of transactions skipped: %d (0.000%%)",
		"latency average = %.3f ms",
		connectionLine,
		"tps = %.6f (%s)",
	}, "\n")+"\n", clients, processed, failed, skipped, latencyMS, tps, qualifier)
	result, err := Parse(strings.NewReader(summary))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func parseFixedTransactionSummary(t *testing.T, tps string) Result {
	t.Helper()
	summary := fmt.Sprintf(strings.Join([]string{
		"pgbench (15.17, server 15.17)",
		"transaction type: <builtin: select only>",
		"scaling factor: 1",
		"query mode: simple",
		"number of clients: 1",
		"number of threads: 1",
		"maximum number of tries: 1",
		"number of transactions per client: 100",
		"number of transactions actually processed: 100/100",
		"number of failed transactions: 0 (0.000%%)",
		"latency average = 100.000 ms",
		"tps = %s (without initial connection time)",
	}, "\n")+"\n", tps)
	result, err := Parse(strings.NewReader(summary))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
