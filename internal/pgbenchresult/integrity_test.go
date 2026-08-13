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
