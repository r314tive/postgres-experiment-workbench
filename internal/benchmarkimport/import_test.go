package benchmarkimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

func TestCreateAndVerifySupportedOfflineImports(t *testing.T) {
	tests := []struct {
		name, adapter, source, mapping, workload string
		wantDriver, wantVersion, wantMetric      string
		wantValue                                float64
	}{
		{
			name: "sysbench 1.0 strict console summary", adapter: AdapterSysbench1,
			source: "sysbench-1.0-oltp.txt", workload: "oltp_read_write/postgresql",
			wantDriver: DriverSysbench, wantVersion: "1.0.20", wantMetric: "transactions_per_second", wantValue: 738.30,
		},
		{
			name: "HammerDB 6 structured report with explicit mapping", adapter: AdapterHammerDB6,
			source: "hammerdb6-report.json", mapping: "hammerdb6-mapping.json",
			wantDriver: DriverHammerDB, wantVersion: "6.0", wantMetric: "nopm", wantValue: 125000,
		},
		{
			name: "BenchBase structured result with explicit mapping", adapter: AdapterBenchBase,
			source: "benchbase-histogram.json", mapping: "benchbase-mapping.json",
			wantDriver: DriverBenchBase, wantVersion: "2026.08", wantMetric: "requests_per_second", wantValue: 4321.5,
		},
		{
			name: "pinned BenchBase ResultWriter summary", adapter: AdapterBenchBase33c0047,
			source:     "benchbase-33c0047-tpcc.summary.json",
			wantDriver: DriverBenchBase, wantVersion: "2023-SNAPSHOT+33c0047", wantMetric: "requests_per_second", wantValue: 4321.5,
		},
		{
			name: "pinned HammerDB 6 saved TPROC-C job report", adapter: AdapterHammerDB6Report,
			source:     "hammerdb-6.0-tprocc-job-report.json",
			wantDriver: DriverHammerDB, wantVersion: "v6.0", wantMetric: "nopm", wantValue: 125000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := filepath.Join("testdata", test.source)
			options := Options{Workload: test.workload}
			if test.mapping != "" {
				options.MappingPath = filepath.Join("testdata", test.mapping)
			}
			output := filepath.Join(t.TempDir(), "imported")
			artifact, err := Create(test.adapter, sourcePath, output, options)
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Driver != test.wantDriver || artifact.DriverVersion != test.wantVersion || artifact.PrimaryMetric.Name != test.wantMetric || artifact.PrimaryMetric.Value != test.wantValue {
				t.Fatalf("unexpected normalized artifact: %#v", artifact)
			}
			if artifact.DecisionEligible || artifact.PGbenchSeriesEligible || artifact.Classification != ClassificationImported || artifact.Conclusion != ConclusionDescriptive {
				t.Fatalf("import escaped descriptive-only boundary: %#v", artifact)
			}
			raw, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(RawSourceFile)))
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != string(original) || artifact.RawInput.Digest != evidence.DigestBytes(original) || artifact.RawInput.SizeBytes != int64(len(original)) {
				t.Fatal("raw input bytes, digest, or size were not preserved exactly")
			}
			verification, err := Verify(output)
			if err != nil {
				t.Fatal(err)
			}
			if !verification.IsValid() {
				t.Fatalf("valid import rejected: %v", verification.Issues)
			}
			seriesVerification, err := benchmarkartifact.Verify(t.TempDir(), output)
			if err != nil {
				t.Fatal(err)
			}
			if seriesVerification.IsValid() {
				t.Fatal("descriptive import entered the pgbench series contract")
			}
		})
	}
}

func TestStrictPinnedAdaptersPreserveBoundedAssurance(t *testing.T) {
	tests := []struct {
		name, adapter, source, workload, commit, timing, metric, direction string
		elapsed                                                            float64
	}{
		{
			name: "BenchBase", adapter: AdapterBenchBase33c0047, source: "benchbase-33c0047-tpcc.summary.json",
			workload: "tpcc", commit: BenchBase33c0047Commit, timing: "reported-elapsed", metric: "requests_per_second", direction: "higher", elapsed: 10,
		},
		{
			name: "HammerDB TPROC-C", adapter: AdapterHammerDB6Report, source: "hammerdb-6.0-tprocc-job-report.json",
			workload: "tprocc/postgresql", commit: HammerDB6Commit, timing: "declared-window", metric: "nopm", direction: "higher", elapsed: 300,
		},
		{
			name: "HammerDB TPROC-H", adapter: AdapterHammerDB6Report, source: "hammerdb-6.0-tproch-job-report.json",
			workload: "tproch/postgresql", commit: HammerDB6Commit, timing: "reported-aggregate-query-time", metric: "geomean_seconds", direction: "lower", elapsed: 93.11,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifact, err := Create(test.adapter, filepath.Join("testdata", test.source), filepath.Join(t.TempDir(), "imported"), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Workload != test.workload || artifact.DriverCommit != test.commit || artifact.Timing.Basis != test.timing || artifact.Timing.ElapsedSeconds != test.elapsed || artifact.PrimaryMetric.Name != test.metric || artifact.PrimaryMetric.Direction != test.direction {
				t.Fatalf("unexpected strict normalization: %#v", artifact)
			}
			if artifact.Errors.Complete || artifact.Errors.Total != 0 || len(artifact.Errors.Messages) != 0 {
				t.Fatalf("strict import fabricated complete error evidence: %#v", artifact.Errors)
			}
			if artifact.MappingInput != nil || artifact.Assurance.NormalizationOrigin != "strict-pinned-parser" || artifact.Assurance.TPCComplianceClaim || artifact.DecisionEligible {
				t.Fatalf("strict import escaped its assurance boundary: %#v", artifact)
			}
		})
	}
}

func TestPinnedAdaptersFailClosedOnRealShapeAmbiguityAndInconsistency(t *testing.T) {
	benchbase := string(mustReadFixture(t, "benchbase-33c0047-tpcc.summary.json"))
	hammer := string(mustReadFixture(t, "hammerdb-6.0-tprocc-job-report.json"))
	tests := []struct {
		name, adapter, source, workload string
		mapping                         []byte
		want                            string
	}{
		{name: "BenchBase duplicate key", adapter: AdapterBenchBase33c0047, source: strings.Replace(benchbase, `"Final State": "DONE",`, `"Final State": "DONE", "Final State": "DONE",`, 1), want: "duplicate object key"},
		{name: "BenchBase unknown field", adapter: AdapterBenchBase33c0047, source: strings.Replace(benchbase, `"Goodput (requests/second)": 4308.2`, `"Goodput (requests/second)": 4308.2, "unknown": 1`, 1), want: "unknown field"},
		{name: "BenchBase wrong DBMS", adapter: AdapterBenchBase33c0047, source: strings.Replace(benchbase, `"POSTGRES"`, `"MYSQL"`, 1), want: "must be POSTGRES"},
		{name: "BenchBase incomplete state", adapter: AdapterBenchBase33c0047, source: strings.Replace(benchbase, `"DONE"`, `"FAILED"`, 1), want: "must be DONE"},
		{name: "BenchBase impossible chronology", adapter: AdapterBenchBase33c0047, source: strings.Replace(benchbase, `1722513610500`, `1722513600000`, 1), want: "strictly chronological"},
		{name: "BenchBase throughput tamper", adapter: AdapterBenchBase33c0047, source: strings.Replace(benchbase, `4321.5`, `4321.6`, 1), want: "inconsistent"},
		{name: "BenchBase manifest ambiguity", adapter: AdapterBenchBase33c0047, source: benchbase, mapping: []byte(`{}`), want: "does not accept --manifest"},
		{name: "BenchBase workload ambiguity", adapter: AdapterBenchBase33c0047, source: benchbase, workload: "tpcc", want: "does not accept --workload"},
		{name: "HammerDB duplicate key", adapter: AdapterHammerDB6Report, source: strings.Replace(hammer, `"hdb_version": "v6.0",`, `"hdb_version": "v6.0", "hdb_version": "v6.0",`, 1), want: "duplicate object key"},
		{name: "HammerDB unknown result field", adapter: AdapterHammerDB6Report, source: strings.Replace(hammer, `"type": "tproc_c_result",`, `"type": "tproc_c_result", "unknown": 1,`, 1), want: "unknown field"},
		{name: "HammerDB wrong version", adapter: AdapterHammerDB6Report, source: strings.Replace(hammer, `"hdb_version": "v6.0"`, `"hdb_version": "v6.1"`, 1), want: "must be v6.0"},
		{name: "HammerDB wrong database", adapter: AdapterHammerDB6Report, source: strings.Replace(hammer, `"database": "PostgreSQL"`, `"database": "Oracle"`, 1), want: "must be pg or PostgreSQL"},
		{name: "HammerDB missing declared duration", adapter: AdapterHammerDB6Report, source: strings.Replace(hammer, `"duration_minutes": 5`, `"duration_minutes": 0`, 1), want: "duration_minutes"},
		{name: "HammerDB chart mismatch", adapter: AdapterHammerDB6Report, source: strings.Replace(hammer, `{"metric": "NOPM", "value": 125000}`, `{"metric": "NOPM", "value": 125001}`, 1), want: "does not match"},
		{name: "HammerDB manifest ambiguity", adapter: AdapterHammerDB6Report, source: hammer, mapping: []byte(`{}`), want: "does not accept --manifest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := derive(test.adapter, []byte(test.source), test.mapping, test.workload)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestVerifyRederivesPinnedAdapterFromRawBytes(t *testing.T) {
	output := filepath.Join(t.TempDir(), "imported")
	_, err := Create(AdapterBenchBase33c0047, filepath.Join("testdata", "benchbase-33c0047-tpcc.summary.json"), output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(output, ResultFile)
	artifact, err := parseArtifact(mustReadPath(t, resultPath))
	if err != nil {
		t.Fatal(err)
	}
	artifact.PrimaryMetric.Value = 9999
	artifact.Digest, err = artifactDigest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, resultPath, artifact)
	verification, err := Verify(output)
	if err != nil {
		t.Fatal(err)
	}
	if verification.IsValid() || !issuesContain(verification.Issues, "does not match independently re-derived") {
		t.Fatalf("redigested strict normalized tamper passed: %v", verification.Issues)
	}
}

func TestVerifyRejectsRawAndEligibilityTampering(t *testing.T) {
	t.Run("raw source", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "imported")
		_, err := Create(AdapterSysbench1, filepath.Join("testdata", "sysbench-1.0-oltp.txt"), output, Options{Workload: "oltp_read_write/postgresql"})
		if err != nil {
			t.Fatal(err)
		}
		rawPath := filepath.Join(output, filepath.FromSlash(RawSourceFile))
		content, err := os.ReadFile(rawPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rawPath, append(content, []byte("\nERROR: injected\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		verification, err := Verify(output)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !issuesContain(verification.Issues, "raw_input does not match") {
			t.Fatalf("raw tampering passed: %v", verification.Issues)
		}
	})

	t.Run("redigested eligibility", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "imported")
		_, err := Create(AdapterBenchBase, filepath.Join("testdata", "benchbase-histogram.json"), output, Options{MappingPath: filepath.Join("testdata", "benchbase-mapping.json")})
		if err != nil {
			t.Fatal(err)
		}
		resultPath := filepath.Join(output, ResultFile)
		content, err := os.ReadFile(resultPath)
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := parseArtifact(content)
		if err != nil {
			t.Fatal(err)
		}
		artifact.DecisionEligible = true
		artifact.PGbenchSeriesEligible = true
		artifact.Digest, err = artifactDigest(artifact)
		if err != nil {
			t.Fatal(err)
		}
		writeTestJSON(t, resultPath, artifact)
		verification, err := Verify(output)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !issuesContain(verification.Issues, "ineligible for pgbench series or decisions") {
			t.Fatalf("redigested eligibility tampering passed: %v", verification.Issues)
		}
	})

	t.Run("redigested selected raw value", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "imported")
		_, err := Create(AdapterBenchBase, filepath.Join("testdata", "benchbase-histogram.json"), output, Options{MappingPath: filepath.Join("testdata", "benchbase-mapping.json")})
		if err != nil {
			t.Fatal(err)
		}
		rawPath := filepath.Join(output, filepath.FromSlash(RawSourceFile))
		raw := mustReadPath(t, rawPath)
		raw = []byte(strings.Replace(string(raw), "4321.5", "9999.5", 1))
		if err := os.WriteFile(rawPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		resultPath := filepath.Join(output, ResultFile)
		artifact, err := parseArtifact(mustReadPath(t, resultPath))
		if err != nil {
			t.Fatal(err)
		}
		// Simulate an attacker updating integrity metadata while leaving the
		// normalized selected value stale. Independent re-derivation must catch it.
		artifact.RawInput = fileEvidence(RawSourceFile, raw)
		artifact.Digest, err = artifactDigest(artifact)
		if err != nil {
			t.Fatal(err)
		}
		writeTestJSON(t, resultPath, artifact)
		verification, err := Verify(output)
		if err != nil {
			t.Fatal(err)
		}
		if verification.IsValid() || !issuesContain(verification.Issues, "does not match independently re-derived") {
			t.Fatalf("stale selected metric passed after raw redigest: %v", verification.Issues)
		}
	})
}

func TestStrictInputsFailClosed(t *testing.T) {
	validSysbench, err := os.ReadFile(filepath.Join("testdata", "sysbench-1.0-oltp.txt"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, adapter   string
		source, mapping []byte
		workload, want  string
	}{
		{name: "unsupported sysbench version", adapter: AdapterSysbench1, source: []byte(strings.Replace(string(validSysbench), "sysbench 1.0.20", "sysbench 1.1.0", 1)), workload: "oltp", want: "missing supported sysbench 1.0 version"},
		{name: "duplicate total", adapter: AdapterSysbench1, source: []byte(strings.Replace(string(validSysbench), "total time:                          10.0009s", "total time:                          10.0009s\n    total time:                          10.0009s", 1)), workload: "oltp", want: "duplicate total time"},
		{name: "truncated summary", adapter: AdapterSysbench1, source: []byte("sysbench 1.0.20\nGeneral statistics:\n total time: 1.0s\n"), workload: "oltp", want: "incomplete General statistics"},
		{name: "reported transaction rate inconsistent with totals", adapter: AdapterSysbench1, source: []byte(strings.Replace(string(validSysbench), "738.30 per sec.", "999999.99 per sec.", 1)), workload: "oltp", want: "inconsistent with total events and elapsed time"},
		{name: "mapping required", adapter: AdapterHammerDB6, source: []byte(`{"result": 1}`), want: "requires --manifest"},
		{name: "duplicate structured key", adapter: AdapterHammerDB6, source: []byte(`{"result": 1, "result": 2}`), mapping: mustReadFixture(t, "hammerdb6-mapping.json"), want: "duplicate object key"},
		{name: "wrong HammerDB major", adapter: AdapterHammerDB6, source: []byte(strings.Replace(string(mustReadFixture(t, "hammerdb6-report.json")), `"6.0"`, `"5.0"`, 1)), mapping: mustReadFixture(t, "hammerdb6-mapping.json"), want: "HammerDB 6.x"},
		{name: "stale selected pointer", adapter: AdapterBenchBase, source: mustReadFixture(t, "benchbase-histogram.json"), mapping: []byte(strings.Replace(string(mustReadFixture(t, "benchbase-mapping.json")), `"/throughput/requests_per_second"`, `"/throughput/stale"`, 1)), want: "does not exist"},
		{name: "BenchBase source must be structured", adapter: AdapterBenchBase, source: []byte("not json"), mapping: mustReadFixture(t, "benchbase-mapping.json"), want: "validate structured"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := derive(test.adapter, test.source, test.mapping, test.workload)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestReportedRateConsistencyToleranceIsBounded(t *testing.T) {
	within := reportedRate{value: 100.104, decimalPlaces: 2}
	if err := checkReportedRate("events per second", 1000, 10, within); err != nil {
		t.Fatalf("bounded rounding/timer skew was rejected: %v", err)
	}
	beyond := reportedRate{value: 100.106, decimalPlaces: 2}
	if err := checkReportedRate("events per second", 1000, 10, beyond); err == nil {
		t.Fatal("rate beyond the bounded tolerance was accepted")
	}
}

func TestCreateRefusesOverwriteAndSymlinkInput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "imported")
	options := Options{Workload: "oltp"}
	if _, err := Create(AdapterSysbench1, filepath.Join("testdata", "sysbench-1.0-oltp.txt"), output, options); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(AdapterSysbench1, filepath.Join("testdata", "sysbench-1.0-oltp.txt"), output, options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("immutable destination was overwritten: %v", err)
	}

	link := filepath.Join(t.TempDir(), "source-link")
	target, err := filepath.Abs(filepath.Join("testdata", "sysbench-1.0-oltp.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Create(AdapterSysbench1, link, filepath.Join(t.TempDir(), "linked"), options); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked source was accepted: %v", err)
	}
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func mustReadPath(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func issuesContain(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return true
		}
	}
	return false
}
