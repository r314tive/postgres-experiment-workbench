package metricsplan

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var Header = []string{
	"sampled_at",
	"database_name",
	"active_sessions",
	"waiting_sessions",
	"lock_waiting_sessions",
	"blocked_sessions",
	"locks_total",
	"locks_waiting",
	"xact_commit",
	"xact_rollback",
	"blks_read",
	"blks_hit",
	"tup_returned",
	"tup_fetched",
	"tup_inserted",
	"tup_updated",
	"tup_deleted",
	"conflicts",
	"deadlocks",
	"temp_files",
	"temp_bytes",
	"wal_records",
	"wal_fpi",
	"wal_bytes",
	"current_wal_lsn",
}

type Env func(string) string

type Plan struct {
	Output          string   `json:"output"`
	Query           string   `json:"query"`
	Mode            string   `json:"mode"`
	IntervalSeconds int      `json:"interval_seconds"`
	DurationSeconds int      `json:"duration_seconds"`
	Samples         int      `json:"samples,omitempty"`
	Append          bool     `json:"append"`
	Header          []string `json:"header"`
}

func Build(root string, outputArg string, env Env, now time.Time) (Plan, error) {
	if env == nil {
		env = func(string) string { return "" }
	}

	interval, err := positiveInt("METRICS_INTERVAL", defaultValue(env("METRICS_INTERVAL"), "1"))
	if err != nil {
		return Plan{}, err
	}
	duration, err := nonnegativeInt("METRICS_DURATION", defaultValue(env("METRICS_DURATION"), "30"))
	if err != nil {
		return Plan{}, err
	}

	sampleCount := 0
	mode := "duration"
	if samplesValue := env("METRICS_SAMPLES"); samplesValue != "" {
		sampleCount, err = positiveInt("METRICS_SAMPLES", samplesValue)
		if err != nil {
			return Plan{}, err
		}
		mode = "samples"
	}

	output := outputArg
	if output == "" {
		output = env("METRICS_OUT")
	}
	if output == "" {
		output = filepath.Join(root, "logs", "metrics", fmt.Sprintf("metrics.%s.csv", now.UTC().Format("20060102_150405")))
	}

	return Plan{
		Output:          output,
		Query:           filepath.ToSlash(filepath.Join("sql", "metrics_sample.sql")),
		Mode:            mode,
		IntervalSeconds: interval,
		DurationSeconds: duration,
		Samples:         sampleCount,
		Append:          env("METRICS_APPEND") == "1",
		Header:          append([]string(nil), Header...),
	}, nil
}

func Render(w io.Writer, plan Plan) error {
	if _, err := fmt.Fprintln(w, "# Metrics Sampling Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Field | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}

	rows := []struct {
		key   string
		value string
	}{
		{"Output", plan.Output},
		{"Append", fmt.Sprintf("%t", plan.Append)},
		{"Mode", plan.Mode},
		{"Interval seconds", strconv.Itoa(plan.IntervalSeconds)},
		{"Duration seconds", strconv.Itoa(plan.DurationSeconds)},
		{"Samples", samplesDisplay(plan.Samples)},
		{"Query", plan.Query},
		{"Header columns", strconv.Itoa(len(plan.Header))},
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "| %s | `%s` |\n", tableCell(row.key), tableCell(row.value)); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "## CSV Header"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "```csv"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, strings.Join(plan.Header, ",")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "```"); err != nil {
		return err
	}
	return nil
}

func RenderJSON(w io.Writer, plan Plan) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

func positiveInt(label string, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got: %s", label, value)
	}
	return parsed, nil
}

func nonnegativeInt(label string, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got: %s", label, value)
	}
	return parsed, nil
}

func defaultValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func samplesDisplay(samples int) string {
	if samples == 0 {
		return "-"
	}
	return strconv.Itoa(samples)
}

func tableCell(value string) string {
	return strings.ReplaceAll(value, "|", `\|`)
}
