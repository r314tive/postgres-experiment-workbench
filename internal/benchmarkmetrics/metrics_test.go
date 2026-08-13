package benchmarkmetrics

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
)

const testControlProtocol = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDeriveFileSupportsPostgres15Through19AndScopesMeasure(t *testing.T) {
	content := fixtureCSV([]string{
		"2026-08-12T00:00:09Z",
		"2026-08-12T00:00:10Z",
		"2026-08-12T00:00:15Z",
		"2026-08-12T00:00:20Z",
		"2026-08-12T00:00:21Z",
	})
	path := filepath.Join(t.TempDir(), SourcePath)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	var reference Summary
	for _, major := range []string{"15", "16", "17", "18", "19"} {
		summary, err := deriveTestFile(path, major, "2026-08-12T00:00:10Z", "2026-08-12T00:00:20Z", 5)
		if err != nil {
			t.Fatalf("PostgreSQL %s shaped sampler rejected: %v", major, err)
		}
		if summary.SchemaVersion != SchemaVersion || summary.ArtifactType != ArtifactType || summary.ParserVersion != ParserVersion || summary.PostgresServerMajor != major {
			t.Fatalf("unexpected normalized identity for %s: %#v", major, summary)
		}
		if summary.Coverage.Samples != 3 || summary.Coverage.TotalSamples != 5 || summary.Coverage.FirstSampleAt != "2026-08-12T00:00:10Z" || summary.Coverage.LastSampleAt != "2026-08-12T00:00:20Z" || summary.Coverage.LeadingSlackNS != 0 || summary.Coverage.TrailingSlackNS != 0 {
			t.Fatalf("summary was not scoped to the minimal measure window: %#v", summary.Coverage)
		}
		if summary.Database.Name != "bench_db" || len(summary.CounterDeltas) != 16 || len(summary.Gauges) != 6 {
			t.Fatalf("incomplete PostgreSQL normalization: %#v", summary)
		}
		xact := findCounter(t, summary, "xact_commit")
		if xact.Scope != "pg_stat_database" || xact.First != "110" || xact.Last != "130" || xact.Delta != "20" {
			t.Fatalf("unexpected xact_commit delta: %#v", xact)
		}
		wal := findCounter(t, summary, "wal_bytes")
		if wal.Scope != "pg_stat_wal" || wal.First != "125" || wal.Last != "145" || wal.Delta != "20" {
			t.Fatalf("unexpected wal_bytes delta: %#v", wal)
		}
		active := findGauge(t, summary, "active_sessions")
		if active.Mean != 3 || active.Max != 4 {
			t.Fatalf("unexpected active_sessions summary: %#v", active)
		}
		if err := VerifyDigest(summary); err != nil {
			t.Fatalf("self digest rejected: %v", err)
		}
		if major == "17" {
			reference = summary
		}
	}
	repeated, err := deriveTestFile(path, "17", "2026-08-12T00:00:10Z", "2026-08-12T00:00:20Z", 5)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Digest != reference.Digest {
		t.Fatalf("same source and measure interval produced different digest: %s != %s", repeated.Digest, reference.Digest)
	}
}

func TestDeriveFileRecordsBoundarySlackWithoutWarmupAggregation(t *testing.T) {
	path := filepath.Join(t.TempDir(), SourcePath)
	if err := os.WriteFile(path, fixtureCSV([]string{
		"2026-08-12T00:00:00Z",
		"2026-08-12T00:00:09Z",
		"2026-08-12T00:00:15Z",
		"2026-08-12T00:00:21Z",
		"2026-08-12T00:00:30Z",
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	summary, err := deriveTestFile(path, "17", "2026-08-12T00:00:10Z", "2026-08-12T00:00:20Z", 6)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Coverage.FirstSampleAt != "2026-08-12T00:00:09Z" || summary.Coverage.LastSampleAt != "2026-08-12T00:00:21Z" || summary.Coverage.Samples != 3 || summary.Coverage.LeadingSlackNS != 1_000_000_000 || summary.Coverage.TrailingSlackNS != 1_000_000_000 {
		t.Fatalf("unexpected bracketing window: %#v", summary.Coverage)
	}
	active := findGauge(t, summary, "active_sessions")
	if active.Mean != 3 || active.Max != 4 {
		t.Fatalf("samples outside minimal boundary window entered gauge aggregate: %#v", active)
	}
}

func TestDeriveFileFailsClosedOnMalformedOrResetEvidence(t *testing.T) {
	base := fixtureCSV([]string{
		"2026-08-12T00:00:09Z",
		"2026-08-12T00:00:10Z",
		"2026-08-12T00:00:15Z",
		"2026-08-12T00:00:20Z",
		"2026-08-12T00:00:21Z",
	})
	tests := []struct {
		name    string
		content []byte
		major   string
		start   string
		finish  string
		want    string
	}{
		{name: "unsupported old major", content: base, major: "14", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "expected 15 through 19"},
		{name: "unsupported future major", content: base, major: "20", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "expected 15 through 19"},
		{name: "wrong header", content: mutateHeader(t, base), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "header does not match"},
		{name: "negative gauge", content: mutateCell(t, base, 2, "active_sessions", "-1"), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "canonical non-negative integer"},
		{name: "fractional numeric WAL counter", content: mutateCell(t, base, 2, "wal_bytes", "12.5"), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "canonical non-negative integer"},
		{name: "database counter reset", content: mutateCell(t, base, 2, "xact_commit", "1"), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "xact_commit decreased"},
		{name: "WAL counter reset", content: mutateCell(t, base, 2, "wal_bytes", "1"), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "wal_bytes decreased"},
		{name: "duplicate timestamp", content: mutateCell(t, base, 2, "sampled_at", "2026-08-12T00:00:10Z"), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "not strictly monotonic"},
		{name: "database drift", content: mutateCell(t, base, 2, "database_name", "other_db"), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "database identity changed"},
		{name: "malformed LSN", content: mutateCell(t, base, 2, "current_wal_lsn", "not-an-lsn"), major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:20Z", want: "not canonical PostgreSQL LSN"},
		{name: "no leading coverage", content: base, major: "17", start: "2026-08-12T00:00:08Z", finish: "2026-08-12T00:00:20Z", want: "no sample at or before measure start"},
		{name: "no trailing coverage", content: base, major: "17", start: "2026-08-12T00:00:10Z", finish: "2026-08-12T00:00:22Z", want: "no sample at or after measure finish"},
		{name: "invalid measure interval", content: base, major: "17", start: "2026-08-12T00:00:20Z", finish: "2026-08-12T00:00:10Z", want: "positive duration"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), SourcePath)
			if err := os.WriteFile(path, test.content, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := deriveTestFile(path, test.major, test.start, test.finish, 5)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestDeriveFileRejectsSymlinkAndDigestTampering(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.csv")
	if err := os.WriteFile(target, fixtureCSV([]string{"2026-08-12T00:00:09Z", "2026-08-12T00:00:21Z"}), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, SourcePath)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := deriveTestFile(link, "17", "2026-08-12T00:00:10Z", "2026-08-12T00:00:20Z", 10); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked metrics source passed: %v", err)
	}

	summary, err := deriveTestFile(target, "17", "2026-08-12T00:00:10Z", "2026-08-12T00:00:20Z", 10)
	if err != nil {
		t.Fatal(err)
	}
	summary.Gauges[0].Max++
	if err := VerifyDigest(summary); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered normalized summary digest passed: %v", err)
	}
}

func TestDeriveFileEnforcesExplicitCadenceBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), SourcePath)
	if err := os.WriteFile(path, fixtureCSV([]string{
		"2026-08-12T00:00:00Z", "2026-08-12T00:00:01Z", "2026-08-12T00:00:04Z",
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := deriveTestFile(path, "17", "2026-08-12T00:00:00Z", "2026-08-12T00:00:04Z", 1)
	if err == nil || !strings.Contains(err.Error(), "exceeds explicit cadence bound") {
		t.Fatalf("three-interval cadence gap passed: %v", err)
	}
}

func TestDeriveFileV2CorrelatesExactCollectorTiming(t *testing.T) {
	path := filepath.Join(t.TempDir(), SourcePath)
	if err := os.WriteFile(path, fixtureCSV([]string{
		"2026-08-12T00:00:00.1Z", "2026-08-12T00:00:01.1Z", "2026-08-12T00:00:02.1Z",
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	binding := benchmarkcontrol.Binding{RunID: "metrics-v2-t001", ProtocolDigest: testControlProtocol, Trial: 1}
	reset, resetRaw := testStatisticsReset(t, binding, benchmarkcontrol.StatisticsPolicyNone, "none", "", "")
	overheadRaw := []byte(strings.Join([]string{
		"sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus",
		"1\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00Z\t2026-08-12T00:00:00.2Z\t100000000\tsucceeded",
		"2\t2026-08-12T00:00:01Z\t2026-08-12T00:00:01Z\t2026-08-12T00:00:01.2Z\t100000000\tsucceeded",
		"3\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02.2Z\t100000000\tsucceeded",
		"",
	}, "\n"))
	maximum := 20.0
	overhead, err := benchmarkcontrol.NewCollectorOverheadFromSource(benchmarkcontrol.CollectorOverheadInput{
		RunID: binding.RunID, ProtocolDigest: binding.ProtocolDigest, Trial: binding.Trial,
		CapturedAt:        "2026-08-12T00:00:03Z",
		CalibrationWindow: benchmarkcontrol.BoundaryWindow{StartedAt: "2026-08-12T00:00:00Z", FinishedAt: "2026-08-12T00:00:03Z"},
		Mode:              benchmarkcontrol.OverheadModeRunnerCalibrated, IntervalNS: int64(time.Second), RequiredSamples: 3, MaxDutyCyclePct: &maximum,
	}, overheadRaw)
	if err != nil {
		t.Fatal(err)
	}
	options := v2Options(path, binding, reset, resetRaw, overhead, overheadRaw)
	options.MeasureStartedAt = "2026-08-12T00:00:00.1Z"
	options.MeasureFinishedAt = "2026-08-12T00:00:02.1Z"
	summary, err := DeriveFile(options)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Cadence.RegularSamplesMatched != 3 || summary.Cadence.BoundarySamples != 0 || !strings.HasPrefix(summary.Cadence.VerificationMode, "typed-monotonic") {
		t.Fatalf("exact timing evidence was not bound: %#v", summary.Cadence)
	}
	if err := VerifyDigest(summary); err != nil {
		t.Fatalf("valid protocol-v2 metrics summary failed self-verification: %v", err)
	}

	bad := mutateCell(t, fixtureCSV([]string{
		"2026-08-12T00:00:00.1Z", "2026-08-12T00:00:01.1Z", "2026-08-12T00:00:02.1Z",
	}), 1, "sampled_at", "2026-08-12T00:00:01.5Z")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DeriveFile(options); err == nil || !strings.Contains(err.Error(), "cannot be correlated") {
		t.Fatalf("metrics/timing mismatch passed: %v", err)
	}
}

func TestDeriveFileV2SegmentsCountersOnlyAcrossProvenScopeReset(t *testing.T) {
	content := fixtureCSV([]string{
		"2026-08-12T00:00:00Z", "2026-08-12T00:00:01Z", "2026-08-12T00:00:02Z", "2026-08-12T00:00:03Z",
	})
	content = mutateCell(t, content, 2, "xact_commit", "5")
	content = mutateCell(t, content, 2, "wal_bytes", "7")
	path := filepath.Join(t.TempDir(), SourcePath)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	binding := benchmarkcontrol.Binding{RunID: "metrics-reset-t001", ProtocolDigest: testControlProtocol, Trial: 1}
	reset, resetRaw := testStatisticsReset(t, binding, benchmarkcontrol.StatisticsPolicyRunnerManaged, "before-measure", "2026-08-12T00:00:01.5Z", "2026-08-12T00:00:01.5Z")
	overhead, overheadRaw := testUnquantifiedOverhead(t, binding)
	options := v2Options(path, binding, reset, resetRaw, overhead, overheadRaw)
	options.MeasureFinishedAt = "2026-08-12T00:00:03Z"
	options.StatisticsResetPolicy = benchmarkcontrol.StatisticsPolicyRunnerManaged
	options.StatisticsResetBoundary = "before-measure"
	summary, err := DeriveFile(options)
	if err != nil {
		t.Fatal(err)
	}
	xact := findCounter(t, summary, "xact_commit")
	wal := findCounter(t, summary, "wal_bytes")
	if xact.Delta != "140" || xact.Segments != 2 || !xact.ResetApplied || wal.Delta != "155" || wal.Segments != 2 || !wal.ResetApplied {
		t.Fatalf("reset segments were not summed deterministically: xact=%#v wal=%#v", xact, wal)
	}
	if summary.StatisticsReset.Database.ResetAt != "2026-08-12T00:00:01.5Z" || summary.StatisticsReset.WAL.ResetAt != "2026-08-12T00:00:01.5Z" {
		t.Fatalf("reset scope timestamps were not bound: %#v", summary.StatisticsReset)
	}
	if summary.StatisticsReset.Database.BeforeAvailability != benchmarkcontrol.ObservationNull || summary.StatisticsReset.WAL.BeforeAvailability != benchmarkcontrol.ObservationNull {
		t.Fatalf("pre-reset timestamp observations were not bound: %#v", summary.StatisticsReset)
	}

	wrongScopeReset, wrongScopeRaw := testStatisticsReset(t, binding, benchmarkcontrol.StatisticsPolicyRunnerManaged, "before-measure", "2026-08-12T00:00:01.5Z", "2026-08-12T00:00:00.5Z")
	wrongScope := options
	wrongScope.StatisticsReset = &wrongScopeReset
	wrongScope.StatisticsResetSource = wrongScopeRaw
	if _, err := DeriveFile(wrongScope); err == nil || !strings.Contains(err.Error(), "without one matching proven pg_stat_wal reset boundary") {
		t.Fatalf("WAL decrease borrowed database reset boundary: %v", err)
	}

	bad := mutateCell(t, content, 3, "xact_commit", "1")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DeriveFile(options); err == nil || !strings.Contains(err.Error(), "without one matching proven pg_stat_database reset boundary") {
		t.Fatalf("second unexplained database counter decrease passed: %v", err)
	}
}

func fixtureCSV(times []string) []byte {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	_ = writer.Write(expectedHeader)
	for row, sampledAt := range times {
		record := make([]string, len(expectedHeader))
		for column, name := range expectedHeader {
			switch name {
			case "sampled_at":
				record[column] = sampledAt
			case "database_name":
				record[column] = "bench_db"
			case "current_wal_lsn":
				record[column] = "0/" + strings.ToUpper(strconv.FormatInt(int64(0x100+row), 16))
			default:
				if slices.Contains(gaugeNames, name) {
					record[column] = strconv.Itoa(row + 1)
				} else {
					counterOffset := slices.Index(appendCounterNames(nil), name)
					record[column] = strconv.Itoa(100 + row*10 + counterOffset)
				}
			}
		}
		_ = writer.Write(record)
	}
	writer.Flush()
	return output.Bytes()
}

func deriveTestFile(path, major, started, finished string, intervalSeconds int) (Summary, error) {
	return DeriveFile(DeriveOptions{
		Path: path, PostgresServerMajor: major, MeasureStartedAt: started, MeasureFinishedAt: finished,
		CollectorIntervalSeconds: intervalSeconds, ContractVersion: "1",
		StatisticsResetPolicy: "none", StatisticsResetBoundary: "none",
	})
}

func v2Options(path string, binding benchmarkcontrol.Binding, reset benchmarkcontrol.StatisticsReset, resetRaw []byte, overhead benchmarkcontrol.CollectorOverhead, overheadRaw []byte) DeriveOptions {
	return DeriveOptions{
		Path: path, PostgresServerMajor: "17", MeasureStartedAt: "2026-08-12T00:00:00Z", MeasureFinishedAt: "2026-08-12T00:00:02Z",
		CollectorIntervalSeconds: 1, ContractVersion: "2",
		StatisticsResetPolicy: benchmarkcontrol.StatisticsPolicyNone, StatisticsResetBoundary: "none",
		ControlBinding: &binding, StatisticsReset: &reset, StatisticsResetSource: resetRaw,
		CollectorOverhead: &overhead, CollectorOverheadSource: overheadRaw,
	}
}

func testStatisticsReset(t *testing.T, binding benchmarkcontrol.Binding, policy, boundary, databaseAfter, walAfter string) (benchmarkcontrol.StatisticsReset, []byte) {
	t.Helper()
	window := benchmarkcontrol.BoundaryWindow{StartedAt: "2026-08-12T00:00:00Z", FinishedAt: "2026-08-12T00:00:03Z"}
	capturedAt := window.FinishedAt
	raw := []byte("record\tscope\tvalue\trows\tcommand_completed\n")
	if policy == benchmarkcontrol.StatisticsPolicyRunnerManaged {
		raw = []byte(strings.Join([]string{
			"record\tscope\tvalue\trows\tcommand_completed",
			"timestamp-before\tcurrent-database\tnull\t\t",
			"timestamp-after\tcurrent-database\t" + databaseAfter + "\t\t",
			"timestamp-before\tcluster-wal\tnull\t\t",
			"timestamp-after\tcluster-wal\t" + walAfter + "\t\t",
			"operation\tcurrent-database\tpg_catalog.pg_stat_reset\t1\ttrue",
			"operation\tcluster-wal\tpg_catalog.pg_stat_reset_shared('wal')\t1\ttrue",
			"",
		}, "\n"))
	}
	artifact, err := benchmarkcontrol.NewStatisticsResetFromSource(benchmarkcontrol.StatisticsResetInput{
		RunID: binding.RunID, ProtocolDigest: binding.ProtocolDigest, Trial: binding.Trial, CapturedAt: capturedAt,
		PostgresServerMajor: "17", Policy: policy, Boundary: boundary, BoundaryWindow: window,
	}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return artifact, raw
}

func testUnquantifiedOverhead(t *testing.T, binding benchmarkcontrol.Binding) (benchmarkcontrol.CollectorOverhead, []byte) {
	t.Helper()
	raw := []byte("sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus\n")
	artifact, err := benchmarkcontrol.NewCollectorOverheadFromSource(benchmarkcontrol.CollectorOverheadInput{
		RunID: binding.RunID, ProtocolDigest: binding.ProtocolDigest, Trial: binding.Trial,
		CapturedAt:        "2026-08-12T00:00:03Z",
		CalibrationWindow: benchmarkcontrol.BoundaryWindow{StartedAt: "2026-08-12T00:00:00Z", FinishedAt: "2026-08-12T00:00:03Z"},
		Mode:              benchmarkcontrol.OverheadModeIncludedUnquantified, IntervalNS: int64(time.Second),
	}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return artifact, raw
}

func mutateCell(t *testing.T, content []byte, dataRow int, columnName, value string) []byte {
	t.Helper()
	records := readRecords(t, content)
	column := slices.Index(records[0], columnName)
	if column < 0 || dataRow < 0 || dataRow+1 >= len(records) {
		t.Fatal("invalid test mutation")
	}
	records[dataRow+1][column] = value
	return writeRecords(t, records)
}

func mutateHeader(t *testing.T, content []byte) []byte {
	t.Helper()
	records := readRecords(t, content)
	records[0][0], records[0][1] = records[0][1], records[0][0]
	return writeRecords(t, records)
}

func readRecords(t *testing.T, content []byte) [][]string {
	t.Helper()
	records, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func writeRecords(t *testing.T, records [][]string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.WriteAll(records)
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func findCounter(t *testing.T, summary Summary, name string) CounterDelta {
	t.Helper()
	for _, counter := range summary.CounterDeltas {
		if counter.Name == name {
			return counter
		}
	}
	t.Fatalf("counter %s not found", name)
	return CounterDelta{}
}

func findGauge(t *testing.T, summary Summary, name string) GaugeSummary {
	t.Helper()
	for _, gauge := range summary.Gauges {
		if gauge.Name == name {
			return gauge
		}
	}
	t.Fatalf("gauge %s not found", name)
	return GaugeSummary{}
}
