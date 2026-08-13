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

const benchBase33c0047Version = "2023-SNAPSHOT+33c0047"

// benchBase33c0047Summary mirrors ResultWriter.writeSummary at the pinned
// commit. Keep this closed: silently accepting a later layout would turn a
// pinned parser into a heuristic one.
type benchBase33c0047Summary struct {
	StartMilliseconds   int64                   `json:"Start timestamp (milliseconds)"`
	CurrentMilliseconds int64                   `json:"Current Timestamp (milliseconds)"`
	ElapsedNanoseconds  int64                   `json:"Elapsed Time (nanoseconds)"`
	DBMSType            string                  `json:"DBMS Type"`
	DBMSVersion         string                  `json:"DBMS Version"`
	BenchmarkType       string                  `json:"Benchmark Type"`
	FinalState          string                  `json:"Final State"`
	MeasuredRequests    int64                   `json:"Measured Requests"`
	Isolation           string                  `json:"isolation"`
	ScaleFactor         string                  `json:"scalefactor"`
	Terminals           string                  `json:"terminals"`
	Latency             benchBase33c0047Latency `json:"Latency Distribution"`
	Throughput          float64                 `json:"Throughput (requests/second)"`
	Goodput             float64                 `json:"Goodput (requests/second)"`
}

type benchBase33c0047Latency struct {
	Minimum int64 `json:"Minimum Latency (microseconds)"`
	P25     int64 `json:"25th Percentile Latency (microseconds)"`
	Median  int64 `json:"Median Latency (microseconds)"`
	Average int64 `json:"Average Latency (microseconds)"`
	P75     int64 `json:"75th Percentile Latency (microseconds)"`
	P90     int64 `json:"90th Percentile Latency (microseconds)"`
	P95     int64 `json:"95th Percentile Latency (microseconds)"`
	P99     int64 `json:"99th Percentile Latency (microseconds)"`
	Maximum int64 `json:"Maximum Latency (microseconds)"`
}

func parseBenchBase33c0047(source []byte) (Artifact, error) {
	var summary benchBase33c0047Summary
	if err := decodeClosedJSON(source, &summary); err != nil {
		return Artifact{}, fmt.Errorf("decode pinned BenchBase summary: %w", err)
	}
	if summary.DBMSType != "POSTGRES" {
		return Artifact{}, fmt.Errorf("BenchBase DBMS Type must be POSTGRES")
	}
	if summary.FinalState != "DONE" {
		return Artifact{}, fmt.Errorf("BenchBase Final State must be DONE")
	}
	if summary.ElapsedNanoseconds <= 0 || summary.MeasuredRequests <= 0 {
		return Artifact{}, fmt.Errorf("BenchBase elapsed nanoseconds and measured requests must be positive")
	}
	if summary.StartMilliseconds <= 0 || summary.CurrentMilliseconds <= summary.StartMilliseconds {
		return Artifact{}, fmt.Errorf("BenchBase timestamps must be positive and strictly chronological")
	}
	wallNanoseconds := (summary.CurrentMilliseconds - summary.StartMilliseconds) * int64(time.Millisecond)
	if summary.ElapsedNanoseconds > wallNanoseconds+int64(time.Millisecond) {
		return Artifact{}, fmt.Errorf("BenchBase elapsed time falls outside the start/current timestamp window")
	}
	if !printableText(summary.DBMSVersion, 512) {
		return Artifact{}, fmt.Errorf("BenchBase DBMS Version must be non-empty printable text")
	}
	if err := validateWorkload(summary.BenchmarkType); err != nil {
		return Artifact{}, fmt.Errorf("BenchBase Benchmark Type: %w", err)
	}
	if !printableToken(summary.Isolation, 128) {
		return Artifact{}, fmt.Errorf("BenchBase isolation must be a printable token")
	}
	if err := positiveDecimalString("scalefactor", summary.ScaleFactor); err != nil {
		return Artifact{}, err
	}
	if err := positiveDecimalString("terminals", summary.Terminals); err != nil {
		return Artifact{}, err
	}
	if err := validateBenchBaseLatency(summary.Latency); err != nil {
		return Artifact{}, err
	}
	expectedThroughput := float64(summary.MeasuredRequests) / float64(summary.ElapsedNanoseconds) * 1e9
	if !closeRate(summary.Throughput, expectedThroughput) {
		return Artifact{}, fmt.Errorf("BenchBase Throughput is inconsistent with Measured Requests and Elapsed Time")
	}
	if !finite(summary.Goodput) || summary.Goodput < 0 || summary.Goodput > summary.Throughput+rateTolerance(summary.Throughput) {
		return Artifact{}, fmt.Errorf("BenchBase Goodput must be finite, non-negative, and no greater than Throughput")
	}

	artifact := newArtifact(DriverBenchBase, benchBase33c0047Version, summary.BenchmarkType, BenchBase33c0047SourceFormat, BenchBase33c0047ParserVersion, "strict-pinned-parser")
	artifact.DriverCommit = BenchBase33c0047Commit
	artifact.PrimaryMetric = PrimaryMetric{
		Name: "requests_per_second", Value: summary.Throughput, Unit: "requests/s",
		Direction: "higher", Basis: "reported",
	}
	// ResultWriter does not emit an exhaustive error list/count. Goodput is
	// retained in raw/source and checked for bounds, but it is not promoted to
	// an invented complete error count.
	artifact.Errors = ErrorSummary{Total: 0, Messages: []string{}, Complete: false}
	artifact.Timing = Timing{Basis: "reported-elapsed", ElapsedSeconds: float64(summary.ElapsedNanoseconds) / 1e9}
	if err := validateNormalized(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func decodeClosedJSON(content []byte, destination any) error {
	if err := validateJSONDocument(content); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func positiveDecimalString(label, value string) error {
	if strings.TrimSpace(value) != value || value == "" {
		return fmt.Errorf("BenchBase %s must be a positive decimal string", label)
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || !finite(parsed) || parsed <= 0 {
		return fmt.Errorf("BenchBase %s must be a positive decimal string", label)
	}
	return nil
}

func validateBenchBaseLatency(latency benchBase33c0047Latency) error {
	percentiles := []int64{latency.Minimum, latency.P25, latency.Median, latency.P75, latency.P90, latency.P95, latency.P99, latency.Maximum}
	for index, value := range percentiles {
		if value < 0 || index > 0 && value < percentiles[index-1] {
			return fmt.Errorf("BenchBase Latency Distribution must contain non-negative ordered percentiles")
		}
	}
	if latency.Average < latency.Minimum || latency.Average > latency.Maximum {
		return fmt.Errorf("BenchBase average latency must fall between minimum and maximum")
	}
	return nil
}

func closeRate(reported, expected float64) bool {
	return finite(reported) && reported > 0 && math.Abs(reported-expected) <= rateTolerance(expected)
}

func rateTolerance(value float64) float64 {
	return math.Max(1e-9, math.Abs(value)*1e-12)
}
