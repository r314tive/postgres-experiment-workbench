// Package testfixture produces the exact PostgreSQL sampler CSV contract for
// cross-package benchmark artifact tests.
package testfixture

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics"
)

var gauges = []string{
	"active_sessions", "waiting_sessions", "lock_waiting_sessions", "blocked_sessions", "locks_total", "locks_waiting",
}

var counters = []string{
	"xact_commit", "xact_rollback", "blks_read", "blks_hit", "tup_returned", "tup_fetched", "tup_inserted", "tup_updated", "tup_deleted", "conflicts", "deadlocks", "temp_files", "temp_bytes",
	"wal_records", "wal_fpi", "wal_bytes",
}

func CSV(times []string, database string) []byte {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	header := benchmarkmetrics.Header()
	_ = writer.Write(header)
	for row, sampledAt := range times {
		record := make([]string, len(header))
		for column, name := range header {
			switch name {
			case "sampled_at":
				record[column] = sampledAt
			case "database_name":
				record[column] = database
			case "current_wal_lsn":
				record[column] = fmt.Sprintf("0/%X", 0x100+row)
			default:
				if slices.Contains(gauges, name) {
					record[column] = fmt.Sprint(row + 1)
				} else {
					record[column] = fmt.Sprint(100 + row*10 + slices.Index(counters, name))
				}
			}
		}
		_ = writer.Write(record)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		panic(err)
	}
	return output.Bytes()
}

func Default() []byte {
	base := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	times := make([]string, 32)
	for index := range times {
		times[index] = base.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
	}
	return CSV(times, "postgres")
}

// Header is useful to diagnostics that need a shell-ready exact contract.
func Header() string { return strings.Join(benchmarkmetrics.Header(), ",") }
