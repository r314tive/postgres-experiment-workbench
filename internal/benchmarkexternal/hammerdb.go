package benchmarkexternal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var hammerDBJobMarker = regexp.MustCompile(`(?m)^PGWORKBENCH_HAMMERDB_JOBID=([0-9A-F]{24})$`)

type hammerDBReportBinding struct {
	Schema           string             `json:"schema"`
	Description      json.RawMessage    `json:"description"`
	Artifact         json.RawMessage    `json:"artifact"`
	Disclaimer       json.RawMessage    `json:"disclaimer"`
	ReportHints      json.RawMessage    `json:"report_hints"`
	AnalysisHints    json.RawMessage    `json:"analysis_hints"`
	Job              hammerDBJobBinding `json:"job"`
	BenchmarkConfig  json.RawMessage    `json:"benchmark_config"`
	System           json.RawMessage    `json:"system"`
	Result           json.RawMessage    `json:"result"`
	TransactionCount json.RawMessage    `json:"transaction_count,omitempty"`
	ResponseTimes    json.RawMessage    `json:"response_times,omitempty"`
	QueryTimes       json.RawMessage    `json:"query_times,omitempty"`
	Metrics          json.RawMessage    `json:"metrics"`
}

type hammerDBJobBinding struct {
	JobID           string `json:"jobid"`
	HDBVersion      string `json:"hdb_version"`
	Database        string `json:"database"`
	DatabaseDisplay string `json:"database_display"`
	Release         string `json:"release"`
	Benchmark       string `json:"benchmark"`
	Timestamp       string `json:"timestamp"`
}

type hammerDBTPROCCBinding struct {
	Warehouses      json.RawMessage `json:"warehouses"`
	VirtualUsers    json.RawMessage `json:"virtual_users"`
	RampupMinutes   json.RawMessage `json:"rampup_minutes"`
	DurationMinutes json.RawMessage `json:"duration_minutes"`
}

type hammerDBTPROCHBinding struct {
	ScaleFactor json.RawMessage `json:"scale_factor"`
}

type hammerDBTPROCHResultBinding struct {
	Type                  json.RawMessage `json:"type"`
	DatabaseDisplay       json.RawMessage `json:"database_display"`
	Queries               json.RawMessage `json:"queries"`
	QuerySets             json.RawMessage `json:"query_sets"`
	GeomeanSeconds        json.RawMessage `json:"geomean_seconds"`
	TotalQueryTimeSeconds json.RawMessage `json:"total_query_time_seconds"`
	ChartData             json.RawMessage `json:"chart_data"`
}

func validateHammerDBStdout(stdout []byte) error {
	if bytes.Contains(stdout, []byte("PGWORKBENCH_HAMMERDB_ERROR")) {
		return fmt.Errorf("HammerDB adapter reported a generated-script error")
	}
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "error in virtual user") {
			return fmt.Errorf("HammerDB reported an error outside its saved job report")
		}
	}
	jobID, err := hammerDBJobIDFromStdout(stdout)
	if err != nil {
		return err
	}
	reportMarker := "PGWORKBENCH_HAMMERDB_REPORT=hdb_" + jobID + ".json"
	reportMarkers := 0
	for _, line := range strings.Split(string(stdout), "\n") {
		if strings.HasPrefix(line, "PGWORKBENCH_HAMMERDB_REPORT=") {
			if line != reportMarker {
				return fmt.Errorf("HammerDB stdout report marker does not match its vurun job id")
			}
			reportMarkers++
		}
	}
	if reportMarkers != 1 {
		return fmt.Errorf("HammerDB stdout does not bind the saved report name to its vurun job id")
	}
	return nil
}

func hammerDBJobIDFromStdout(stdout []byte) (string, error) {
	matches := hammerDBJobMarker.FindAllSubmatch(stdout, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("HammerDB stdout must contain exactly one adapter-validated vurun job id marker")
	}
	return string(matches[0][1]), nil
}

func crossCheckHammerDBResult(workload string, configContent, stdout, reportContent []byte) error {
	jobID, err := hammerDBJobIDFromStdout(stdout)
	if err != nil {
		return err
	}
	var report hammerDBReportBinding
	if err := decodeClosedJSON(reportContent, &report, "HammerDB saved job report binding"); err != nil {
		return err
	}
	if report.Schema != "hammerdb-job-report-v1" || report.Job.JobID != jobID || report.Job.HDBVersion != "v6.0" {
		return fmt.Errorf("HammerDB saved report identity does not match the parsed vurun job id and pinned v6.0 format")
	}
	var config HammerDBConfig
	if err := decodeClosedJSON(configContent, &config, "HammerDB retained run config"); err != nil {
		return err
	}
	if err := validateHammerDBConfig(config, workload); err != nil {
		return err
	}
	switch workload {
	case "tprocc/postgresql":
		if report.Job.Benchmark != "TPROC-C" {
			return fmt.Errorf("HammerDB saved report benchmark does not match tprocc/postgresql")
		}
		var actual hammerDBTPROCCBinding
		if err := decodeClosedJSON(report.BenchmarkConfig, &actual, "HammerDB saved TPROC-C benchmark_config binding"); err != nil {
			return err
		}
		expected := config.TPROCC
		checks := []struct {
			label string
			raw   json.RawMessage
			want  uint64
		}{
			{"warehouses", actual.Warehouses, uint64(expected.Warehouses)},
			{"virtual_users", actual.VirtualUsers, uint64(expected.VirtualUsers)},
			{"rampup_minutes", actual.RampupMinutes, uint64(expected.RampupMinutes)},
			{"duration_minutes", actual.DurationMinutes, uint64(expected.DurationMinutes)},
		}
		for _, check := range checks {
			if !hammerDBIntegerEquals(check.raw, check.want) {
				return fmt.Errorf("HammerDB saved report benchmark_config.%s does not match the retained execution config", check.label)
			}
		}
	case "tproch/postgresql":
		if report.Job.Benchmark != "TPROC-H" {
			return fmt.Errorf("HammerDB saved report benchmark does not match tproch/postgresql")
		}
		var actual hammerDBTPROCHBinding
		if err := decodeClosedJSON(report.BenchmarkConfig, &actual, "HammerDB saved TPROC-H benchmark_config binding"); err != nil {
			return err
		}
		if !hammerDBIntegerEquals(actual.ScaleFactor, uint64(config.TPROCH.ScaleFactor)) {
			return fmt.Errorf("HammerDB saved report benchmark_config.scale_factor does not match the retained execution config")
		}
		var result hammerDBTPROCHResultBinding
		if err := decodeClosedJSON(report.Result, &result, "HammerDB saved TPROC-H result binding"); err != nil {
			return err
		}
		if !hammerDBIntegerEquals(result.QuerySets, uint64(config.TPROCH.QuerySets)) {
			return fmt.Errorf("HammerDB saved report result.query_sets does not match the retained execution config")
		}
	default:
		return fmt.Errorf("HammerDB workload %q has no saved-report binding", workload)
	}
	return nil
}

func hammerDBIntegerEquals(raw json.RawMessage, want uint64) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return false
	}
	text := ""
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case string:
		if strings.TrimSpace(typed) != typed {
			return false
		}
		text = typed
	default:
		return false
	}
	parsed, err := strconv.ParseFloat(text, 64)
	return err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed == float64(want) && math.Trunc(parsed) == parsed
}
