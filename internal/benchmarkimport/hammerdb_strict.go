package benchmarkimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

type hammerDB6Report struct {
	Schema           string              `json:"schema"`
	Description      string              `json:"description"`
	Artifact         hammerDB6Artifact   `json:"artifact"`
	Disclaimer       hammerDB6Disclaimer `json:"disclaimer"`
	ReportHints      json.RawMessage     `json:"report_hints"`
	AnalysisHints    json.RawMessage     `json:"analysis_hints"`
	Job              hammerDB6Job        `json:"job"`
	BenchmarkConfig  json.RawMessage     `json:"benchmark_config"`
	System           json.RawMessage     `json:"system"`
	Result           json.RawMessage     `json:"result"`
	TransactionCount json.RawMessage     `json:"transaction_count,omitempty"`
	ResponseTimes    json.RawMessage     `json:"response_times,omitempty"`
	QueryTimes       json.RawMessage     `json:"query_times,omitempty"`
	Metrics          json.RawMessage     `json:"metrics"`
}

type hammerDB6Artifact struct {
	Type        string `json:"type"`
	Schema      string `json:"schema"`
	Source      string `json:"source"`
	GeneratedBy string `json:"generated_by"`
	Visibility  string `json:"visibility"`
	Redacted    *bool  `json:"redacted"`
}

type hammerDB6Disclaimer struct {
	ResultType        string `json:"result_type"`
	Audited           *bool  `json:"audited"`
	Text              string `json:"text"`
	TPCTrademarks     string `json:"tpc_trademarks"`
	HammerDBCopyright string `json:"hammerdb_copyright"`
	LearnMoreTPC      string `json:"learn_more_tpc"`
	LearnMoreHammerDB string `json:"learn_more_hammerdb"`
}

type hammerDB6Job struct {
	JobID           string `json:"jobid"`
	HDBVersion      string `json:"hdb_version"`
	Database        string `json:"database"`
	DatabaseDisplay string `json:"database_display"`
	Release         string `json:"release"`
	Benchmark       string `json:"benchmark"`
	Timestamp       string `json:"timestamp"`
}

type hammerDB6Config struct {
	Warehouses      json.RawMessage `json:"warehouses,omitempty"`
	VirtualUsers    json.RawMessage `json:"virtual_users,omitempty"`
	ScaleFactor     json.RawMessage `json:"scale_factor,omitempty"`
	RampupMinutes   json.RawMessage `json:"rampup_minutes,omitempty"`
	DurationMinutes json.RawMessage `json:"duration_minutes,omitempty"`
}

type hammerDB6TPROCCResult struct {
	Type               string                 `json:"type"`
	DatabaseDisplay    string                 `json:"database_display"`
	ActiveVirtualUsers json.RawMessage        `json:"active_virtual_users"`
	NOPM               json.RawMessage        `json:"nopm"`
	TPM                json.RawMessage        `json:"tpm"`
	ChartData          []hammerDB6ChartMetric `json:"chart_data"`
}

type hammerDB6TPROCHResult struct {
	Type                  string                 `json:"type"`
	DatabaseDisplay       string                 `json:"database_display"`
	Queries               json.RawMessage        `json:"queries"`
	QuerySets             json.RawMessage        `json:"query_sets"`
	GeomeanSeconds        json.RawMessage        `json:"geomean_seconds"`
	TotalQueryTimeSeconds json.RawMessage        `json:"total_query_time_seconds"`
	ChartData             []hammerDB6ChartMetric `json:"chart_data"`
}

type hammerDB6ChartMetric struct {
	Metric string          `json:"metric"`
	Value  json.RawMessage `json:"value"`
}

func parseHammerDB6Report(source []byte) (Artifact, error) {
	var report hammerDB6Report
	if err := decodeClosedJSON(source, &report); err != nil {
		return Artifact{}, fmt.Errorf("decode pinned HammerDB v6.0 job report: %w", err)
	}
	if err := validateHammerDB6Envelope(report); err != nil {
		return Artifact{}, err
	}
	var config hammerDB6Config
	if err := decodeClosedRaw(report.BenchmarkConfig, &config, "benchmark_config"); err != nil {
		return Artifact{}, err
	}

	var artifact Artifact
	switch report.Job.Benchmark {
	case "TPROC-C":
		artifact = newArtifact(DriverHammerDB, "v6.0", "tprocc/postgresql", HammerDB6ReportSourceFormat, HammerDB6ReportParserVersion, "strict-pinned-parser")
		if err := fillHammerDB6TPROCC(&artifact, report, config); err != nil {
			return Artifact{}, err
		}
	case "TPROC-H":
		artifact = newArtifact(DriverHammerDB, "v6.0", "tproch/postgresql", HammerDB6ReportSourceFormat, HammerDB6ReportParserVersion, "strict-pinned-parser")
		if err := fillHammerDB6TPROCH(&artifact, report, config); err != nil {
			return Artifact{}, err
		}
	default:
		return Artifact{}, fmt.Errorf("HammerDB benchmark must be TPROC-C or TPROC-H")
	}
	artifact.DriverCommit = HammerDB6Commit
	// The saved job report has no exhaustive error channel. Zero here means no
	// errors were normalized, not that the upstream run proved zero errors.
	artifact.Errors = ErrorSummary{Total: 0, Messages: []string{}, Complete: false}
	if err := validateNormalized(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func validateHammerDB6Envelope(report hammerDB6Report) error {
	if report.Schema != "hammerdb-job-report-v1" || report.Artifact.Type != "hammerdb-result-json" || report.Artifact.Schema != report.Schema || report.Artifact.Source != "HammerDB" || report.Artifact.GeneratedBy != "HammerDB jobs summaryjson" {
		return fmt.Errorf("HammerDB report artifact identity is not hammerdb-job-report-v1")
	}
	if report.Artifact.Visibility != "public" || report.Artifact.Redacted == nil || !*report.Artifact.Redacted {
		return fmt.Errorf("HammerDB report must be the public redacted job artifact")
	}
	if report.Disclaimer.ResultType != "HammerDB result artifact" || report.Disclaimer.Audited == nil || *report.Disclaimer.Audited || !strings.Contains(report.Disclaimer.Text, "not audited") || !strings.Contains(report.Disclaimer.Text, "not a TPC result") {
		return fmt.Errorf("HammerDB report must retain its unaudited non-TPC disclaimer")
	}
	if report.Disclaimer.LearnMoreTPC != "https://www.tpc.org/" || report.Disclaimer.LearnMoreHammerDB != "https://www.hammerdb.com/" || !printableText(report.Description, 1024) || !printableText(report.Disclaimer.TPCTrademarks, 1024) || !printableText(report.Disclaimer.HammerDBCopyright, 1024) {
		return fmt.Errorf("HammerDB report disclaimer metadata is incomplete")
	}
	job := report.Job
	if job.HDBVersion != "v6.0" {
		return fmt.Errorf("HammerDB hdb_version must be v6.0")
	}
	if !strings.EqualFold(job.Database, "pg") && !strings.EqualFold(job.Database, "PostgreSQL") {
		return fmt.Errorf("HammerDB database must be pg or PostgreSQL")
	}
	if !strings.EqualFold(job.DatabaseDisplay, "PostgreSQL") || !printableToken(job.JobID, 128) || !printableText(job.Release, 256) {
		return fmt.Errorf("HammerDB PostgreSQL job identity or release is invalid")
	}
	parsedTimestamp, err := time.Parse("2006-01-02 15:04:05", job.Timestamp)
	if err != nil || parsedTimestamp.Format("2006-01-02 15:04:05") != job.Timestamp {
		return fmt.Errorf("HammerDB job timestamp must use its timezone-free repository format")
	}
	for label, raw := range map[string]json.RawMessage{
		"report_hints": report.ReportHints, "analysis_hints": report.AnalysisHints,
		"benchmark_config": report.BenchmarkConfig, "system": report.System,
		"result": report.Result, "metrics": report.Metrics,
	} {
		if !nonNullJSON(raw) {
			return fmt.Errorf("HammerDB %s is required", label)
		}
	}
	return nil
}

func fillHammerDB6TPROCC(artifact *Artifact, report hammerDB6Report, config hammerDB6Config) error {
	if nonNullJSON(config.ScaleFactor) || !nonNullJSON(config.Warehouses) || !nonNullJSON(config.VirtualUsers) || !nonNullJSON(config.RampupMinutes) || !nonNullJSON(config.DurationMinutes) {
		return fmt.Errorf("HammerDB TPROC-C benchmark_config must contain warehouses, virtual_users, rampup_minutes, and duration_minutes only")
	}
	warehouses, err := hammerPositive(config.Warehouses, "benchmark_config.warehouses", true)
	if err != nil {
		return err
	}
	virtualUsers, err := hammerPositive(config.VirtualUsers, "benchmark_config.virtual_users", true)
	if err != nil {
		return err
	}
	rampup, err := hammerNumber(config.RampupMinutes, "benchmark_config.rampup_minutes", true, true)
	if err != nil {
		return err
	}
	duration, err := hammerPositive(config.DurationMinutes, "benchmark_config.duration_minutes", false)
	if err != nil {
		return err
	}
	if warehouses < 1 || rampup < 0 {
		return fmt.Errorf("HammerDB TPROC-C configuration is invalid")
	}
	var result hammerDB6TPROCCResult
	if err := decodeClosedRaw(report.Result, &result, "result"); err != nil {
		return err
	}
	if result.Type != "tproc_c_result" || result.DatabaseDisplay != report.Job.DatabaseDisplay {
		return fmt.Errorf("HammerDB TPROC-C result identity is invalid")
	}
	active, err := hammerPositive(result.ActiveVirtualUsers, "result.active_virtual_users", true)
	if err != nil {
		return err
	}
	if active != virtualUsers {
		return fmt.Errorf("HammerDB active virtual users do not match benchmark_config.virtual_users")
	}
	nopm, err := hammerPositive(result.NOPM, "result.nopm", false)
	if err != nil {
		return err
	}
	tpm, err := hammerPositive(result.TPM, "result.tpm", false)
	if err != nil {
		return err
	}
	if err := validateHammerChart(result.ChartData, map[string]float64{"NOPM": nopm, "TPM": tpm}); err != nil {
		return err
	}
	if !nonNullJSON(report.TransactionCount) || !nonNullJSON(report.ResponseTimes) || nonNullJSON(report.QueryTimes) {
		return fmt.Errorf("HammerDB TPROC-C report sections are incomplete or ambiguous")
	}
	artifact.PrimaryMetric = PrimaryMetric{Name: "nopm", Value: nopm, Unit: "NOPM", Direction: "higher", Basis: "reported"}
	artifact.Timing = Timing{Basis: "declared-window", ElapsedSeconds: duration * 60}
	return nil
}

func fillHammerDB6TPROCH(artifact *Artifact, report hammerDB6Report, config hammerDB6Config) error {
	if nonNullJSON(config.Warehouses) || nonNullJSON(config.VirtualUsers) || nonNullJSON(config.RampupMinutes) || nonNullJSON(config.DurationMinutes) || !nonNullJSON(config.ScaleFactor) {
		return fmt.Errorf("HammerDB TPROC-H benchmark_config must contain scale_factor only")
	}
	if _, err := hammerPositive(config.ScaleFactor, "benchmark_config.scale_factor", false); err != nil {
		return err
	}
	var result hammerDB6TPROCHResult
	if err := decodeClosedRaw(report.Result, &result, "result"); err != nil {
		return err
	}
	if result.Type != "tproc_h_result" || result.DatabaseDisplay != report.Job.DatabaseDisplay {
		return fmt.Errorf("HammerDB TPROC-H result identity is invalid")
	}
	if _, err := hammerPositive(result.Queries, "result.queries", true); err != nil {
		return err
	}
	if _, err := hammerPositive(result.QuerySets, "result.query_sets", true); err != nil {
		return err
	}
	geomean, err := hammerPositive(result.GeomeanSeconds, "result.geomean_seconds", false)
	if err != nil {
		return err
	}
	totalQueryTime, err := hammerPositive(result.TotalQueryTimeSeconds, "result.total_query_time_seconds", false)
	if err != nil {
		return err
	}
	if err := validateHammerChart(result.ChartData, map[string]float64{"Geomean": geomean, "Query Time": totalQueryTime}); err != nil {
		return err
	}
	if !nonNullJSON(report.QueryTimes) || nonNullJSON(report.TransactionCount) || nonNullJSON(report.ResponseTimes) {
		return fmt.Errorf("HammerDB TPROC-H report sections are incomplete or ambiguous")
	}
	artifact.PrimaryMetric = PrimaryMetric{Name: "geomean_seconds", Value: geomean, Unit: "seconds", Direction: "lower", Basis: "reported"}
	artifact.Timing = Timing{Basis: "reported-aggregate-query-time", ElapsedSeconds: totalQueryTime}
	return nil
}

func decodeClosedRaw(raw json.RawMessage, destination any, label string) error {
	if !nonNullJSON(raw) {
		return fmt.Errorf("HammerDB %s is required", label)
	}
	if err := decodeClosedJSON(raw, destination); err != nil {
		return fmt.Errorf("decode HammerDB %s: %w", label, err)
	}
	return nil
}

func nonNullJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func hammerPositive(raw json.RawMessage, label string, integer bool) (float64, error) {
	return hammerNumber(raw, label, integer, false)
}

func hammerNumber(raw json.RawMessage, label string, integer, allowZero bool) (float64, error) {
	if !nonNullJSON(raw) {
		return 0, fmt.Errorf("HammerDB %s is required", label)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("HammerDB %s must be a number or decimal string", label)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return 0, fmt.Errorf("HammerDB %s must contain one scalar", label)
	}
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case string:
		if strings.TrimSpace(typed) != typed {
			return 0, fmt.Errorf("HammerDB %s must be a canonical number", label)
		}
		text = typed
	default:
		return 0, fmt.Errorf("HammerDB %s must be a number or decimal string", label)
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil || !finite(parsed) || parsed < 0 || !allowZero && parsed == 0 || integer && math.Trunc(parsed) != parsed {
		return 0, fmt.Errorf("HammerDB %s must be a %sfinite value", label, map[bool]string{true: "non-negative ", false: "positive "}[allowZero])
	}
	return parsed, nil
}

func validateHammerChart(rows []hammerDB6ChartMetric, expected map[string]float64) error {
	if len(rows) != len(expected) {
		return fmt.Errorf("HammerDB result.chart_data must contain each reported metric exactly once")
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		want, exists := expected[row.Metric]
		if !exists {
			return fmt.Errorf("HammerDB result.chart_data contains unknown metric %q", row.Metric)
		}
		if _, duplicate := seen[row.Metric]; duplicate {
			return fmt.Errorf("HammerDB result.chart_data contains duplicate metric %q", row.Metric)
		}
		value, err := hammerPositive(row.Value, "result.chart_data.value", false)
		if err != nil || math.Abs(value-want) > rateTolerance(want) {
			return fmt.Errorf("HammerDB result.chart_data %s does not match the result metric", row.Metric)
		}
		seen[row.Metric] = struct{}{}
	}
	return nil
}
