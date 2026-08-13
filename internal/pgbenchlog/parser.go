// Package pgbenchlog parses and normalizes plain per-transaction pgbench logs.
//
// The supported PostgreSQL 15-19 subset is deliberately explicit and
// fail-closed: six mandatory integer/status fields, followed by schedule lag
// when --rate was used and retries when --max-tries is not one. A seven-column
// row is inherently ambiguous, so Options must declare which optional column
// is present. Aggregated logs and --failures-detailed status tokens
// (serialization, deadlock, and, in PostgreSQL 19, other) are not accepted.
package pgbenchlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
)

const (
	ResultSchemaVersion = "pgworkbench.pgbench-log-result/v2"
	ParserVersion       = "1.1.0"
)

// Options is protocol evidence, not a format guess. ScheduleLag and Retries
// must match the pgbench invocation because either option alone adds one
// indistinguishable numeric column. SampleRate zero means the pgbench default
// of 1; a value below 1 marks the result as sampled without extrapolating it.
type Options struct {
	SampleRate  float64
	ScheduleLag bool
	Retries     bool
}

// Source names one worker log. All sources passed to Parse are normalized as
// one benchmark trial; their transactions are observations, never independent
// benchmark trials.
type Source struct {
	Name   string
	Reader io.Reader
}

// Distribution contains exact extrema and type-7 sample percentiles. N is the
// number of logged observations used, not an estimate of unsampled rows.
type Distribution struct {
	N    int64   `json:"n"`
	Min  int64   `json:"min"`
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  int64   `json:"max"`
}

// CompletionWindow retains the exact wall-clock extent present in pgbench's
// plain log rows. Keeping the seconds and microseconds as separate canonical
// integers avoids precision loss and lets the benchmark verifier prove that
// every retained row completed inside the independently recorded measure
// phase.
type CompletionWindow struct {
	FirstEpochSeconds      int64 `json:"first_epoch_seconds"`
	FirstEpochMicroseconds int64 `json:"first_epoch_microseconds"`
	LastEpochSeconds       int64 `json:"last_epoch_seconds"`
	LastEpochMicroseconds  int64 `json:"last_epoch_microseconds"`
}

// Result is a versioned aggregate over all worker files from one benchmark
// trial. Completed is the number of rows with numeric latency. Retried counts
// logged rows whose retries field is greater than zero; TotalRetries sums that
// field. Counts are intentionally never divided by SampleRate.
type Result struct {
	SchemaVersion      string           `json:"schema_version"`
	ParserVersion      string           `json:"parser_version"`
	Files              int              `json:"files"`
	SampleRate         float64          `json:"sample_rate"`
	Sampled            bool             `json:"sampled"`
	ScheduleLagPresent bool             `json:"schedule_lag_present"`
	RetriesPresent     bool             `json:"retries_present"`
	Logged             int64            `json:"logged"`
	Completed          int64            `json:"completed"`
	Failed             int64            `json:"failed"`
	Skipped            int64            `json:"skipped"`
	Retried            int64            `json:"retried"`
	TotalRetries       int64            `json:"total_retries"`
	CompletionWindow   CompletionWindow `json:"completion_window"`
	LatencyUS          *Distribution    `json:"latency_us"`
	ScheduleLagUS      *Distribution    `json:"schedule_lag_us"`
}

// ParseError identifies a fail-closed grammar or cross-row contradiction.
type ParseError struct {
	Source  string
	Line    int
	Field   string
	Problem string
}

func (e *ParseError) Error() string {
	where := "pgbench transaction log"
	if e.Source != "" {
		where += " " + e.Source
	}
	if e.Line > 0 {
		where += fmt.Sprintf(":%d", e.Line)
	}
	if e.Field != "" {
		return fmt.Sprintf("%s (%s): %s", where, e.Field, e.Problem)
	}
	return fmt.Sprintf("%s: %s", where, e.Problem)
}

// Parse combines one or more pgbench worker log streams into one trial result.
func Parse(sources []Source, options Options) (Result, error) {
	accumulator, err := newAccumulator(options)
	if err != nil {
		return Result{}, err
	}
	if len(sources) == 0 {
		return Result{}, fmt.Errorf("at least one pgbench transaction log source is required")
	}

	normalized := make([]Source, len(sources))
	copy(normalized, sources)
	for index := range normalized {
		if normalized[index].Reader == nil {
			return Result{}, fmt.Errorf("pgbench transaction log source %d has a nil reader", index+1)
		}
		if strings.TrimSpace(normalized[index].Name) == "" {
			normalized[index].Name = fmt.Sprintf("input-%03d", index+1)
		}
	}
	sort.SliceStable(normalized, func(i, j int) bool { return normalized[i].Name < normalized[j].Name })
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1].Name == normalized[index].Name {
			return Result{}, fmt.Errorf("duplicate pgbench transaction log source name: %s", normalized[index].Name)
		}
	}
	for _, source := range normalized {
		if err := accumulator.parseSource(source.Name, source.Reader); err != nil {
			return Result{}, err
		}
	}
	return accumulator.result()
}

// ParseReader is a convenience wrapper for a single worker log stream.
func ParseReader(reader io.Reader, options Options) (Result, error) {
	return Parse([]Source{{Name: "input", Reader: reader}}, options)
}

// ParseFiles safely opens regular, non-symlink worker logs and combines them as
// one trial. Paths are sorted so diagnostics are deterministic.
func ParseFiles(paths []string, options Options) (Result, error) {
	accumulator, err := newAccumulator(options)
	if err != nil {
		return Result{}, err
	}
	if len(paths) == 0 {
		return Result{}, fmt.Errorf("at least one pgbench transaction log path is required")
	}
	sortedPaths := append([]string(nil), paths...)
	sort.Strings(sortedPaths)
	for index := 1; index < len(sortedPaths); index++ {
		if sortedPaths[index-1] == sortedPaths[index] {
			return Result{}, fmt.Errorf("duplicate pgbench transaction log path: %s", sortedPaths[index])
		}
	}
	for _, path := range sortedPaths {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return Result{}, fmt.Errorf("stat pgbench transaction log %s: %w", path, statErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("pgbench transaction log is not a regular non-symlink file: %s", path)
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return Result{}, fmt.Errorf("open pgbench transaction log %s: %w", path, openErr)
		}
		parseErr := accumulator.parseSource(filepath.ToSlash(path), file)
		closeErr := file.Close()
		if parseErr != nil || closeErr != nil {
			if parseErr != nil {
				return Result{}, parseErr
			}
			return Result{}, fmt.Errorf("close pgbench transaction log %s: %w", path, closeErr)
		}
	}
	return accumulator.result()
}

// JSON returns stable, indented JSON with a trailing newline.
func (result Result) JSON() ([]byte, error) {
	content, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

// ValidateCompletionWindow proves that the raw log's retained completion
// extent is contained by an independently recorded measure phase. It makes no
// coverage claim for sampled logs: absence of rows near either phase edge is
// expected when SampleRate is below one.
func ValidateCompletionWindow(result Result, measureStarted, measureFinished time.Time) error {
	if result.SchemaVersion != ResultSchemaVersion || result.ParserVersion != ParserVersion || result.Logged <= 0 {
		return fmt.Errorf("pgbench transaction log identity or row count is invalid")
	}
	if measureFinished.Before(measureStarted) {
		return fmt.Errorf("measure phase finishes before it starts")
	}
	window := result.CompletionWindow
	if window.FirstEpochSeconds < 0 || window.LastEpochSeconds < 0 ||
		window.FirstEpochMicroseconds < 0 || window.FirstEpochMicroseconds >= 1_000_000 ||
		window.LastEpochMicroseconds < 0 || window.LastEpochMicroseconds >= 1_000_000 ||
		timestampLess(window.LastEpochSeconds, window.LastEpochMicroseconds, window.FirstEpochSeconds, window.FirstEpochMicroseconds) {
		return fmt.Errorf("pgbench transaction log completion window is invalid")
	}
	first := time.Unix(window.FirstEpochSeconds, window.FirstEpochMicroseconds*1_000)
	last := time.Unix(window.LastEpochSeconds, window.LastEpochMicroseconds*1_000)
	if first.Before(measureStarted) || last.After(measureFinished) {
		return fmt.Errorf("pgbench transaction log completion window falls outside the measure phase")
	}
	return nil
}

type outcome uint8

const (
	outcomeCompleted outcome = iota
	outcomeFailed
	outcomeSkipped
)

type record struct {
	clientID       int64
	transactionNo  int64
	latencyUS      int64
	outcome        outcome
	scriptNo       int64
	epochSeconds   int64
	epochMicrosecs int64
	scheduleLagUS  int64
	retries        int64
}

type clientSequence struct {
	source         string
	transactionNo  int64
	consumed       bool
	epochSeconds   int64
	epochMicrosecs int64
}

type accumulator struct {
	options      Options
	files        int
	logged       int64
	completed    int64
	failed       int64
	skipped      int64
	retried      int64
	totalRetries int64
	firstSeconds int64
	firstMicros  int64
	lastSeconds  int64
	lastMicros   int64
	hasTimestamp bool
	latencies    []int64
	lags         []int64
	clients      map[int64]clientSequence
}

func newAccumulator(options Options) (*accumulator, error) {
	if options.SampleRate == 0 {
		options.SampleRate = 1
	}
	if math.IsNaN(options.SampleRate) || math.IsInf(options.SampleRate, 0) || options.SampleRate <= 0 || options.SampleRate > 1 {
		return nil, fmt.Errorf("pgbench log sample rate must be finite and greater than 0 and at most 1")
	}
	return &accumulator{options: options, clients: make(map[int64]clientSequence)}, nil
}

func (accumulator *accumulator) parseSource(name string, reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	lineNumber := 0
	rows := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			return &ParseError{Source: name, Line: lineNumber, Problem: "blank lines are not valid transaction records"}
		}
		fields := strings.Fields(line)
		expectedFields := 6
		if accumulator.options.ScheduleLag {
			expectedFields++
		}
		if accumulator.options.Retries {
			expectedFields++
		}
		if len(fields) != expectedFields {
			return &ParseError{Source: name, Line: lineNumber, Problem: fmt.Sprintf("expected %d fields for declared layout, got %d", expectedFields, len(fields))}
		}
		record, err := parseRecord(name, lineNumber, fields, accumulator.options)
		if err != nil {
			return err
		}
		if err := accumulator.addRecord(name, lineNumber, record); err != nil {
			return err
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		return &ParseError{Source: name, Line: lineNumber + 1, Problem: "read failed: " + err.Error()}
	}
	if rows == 0 {
		return &ParseError{Source: name, Problem: "transaction log is empty"}
	}
	accumulator.files++
	return nil
}

func parseRecord(source string, line int, fields []string, options Options) (record, error) {
	var parsed record
	var err error
	if parsed.clientID, err = parseNonnegative(source, line, "client_id", fields[0]); err != nil {
		return record{}, err
	}
	if parsed.transactionNo, err = parseNonnegative(source, line, "transaction_no", fields[1]); err != nil {
		return record{}, err
	}
	switch fields[2] {
	case "skipped":
		if !options.ScheduleLag {
			return record{}, &ParseError{Source: source, Line: line, Field: "latency_us", Problem: "skipped requires the declared schedule_lag column"}
		}
		parsed.outcome = outcomeSkipped
	case "failed":
		parsed.outcome = outcomeFailed
	case "serialization", "deadlock", "other":
		return record{}, &ParseError{Source: source, Line: line, Field: "latency_us", Problem: "--failures-detailed status tokens are outside the supported plain-log subset"}
	default:
		parsed.latencyUS, err = parseNonnegative(source, line, "latency_us", fields[2])
		if err != nil {
			return record{}, err
		}
		parsed.outcome = outcomeCompleted
	}
	if parsed.scriptNo, err = parseNonnegative(source, line, "script_no", fields[3]); err != nil {
		return record{}, err
	}
	if parsed.epochSeconds, err = parseNonnegative(source, line, "epoch_seconds", fields[4]); err != nil {
		return record{}, err
	}
	if parsed.epochMicrosecs, err = parseNonnegative(source, line, "epoch_microseconds", fields[5]); err != nil {
		return record{}, err
	}
	if parsed.epochMicrosecs >= 1_000_000 {
		return record{}, &ParseError{Source: source, Line: line, Field: "epoch_microseconds", Problem: "must be between 0 and 999999"}
	}
	index := 6
	if options.ScheduleLag {
		if parsed.scheduleLagUS, err = parseNonnegative(source, line, "schedule_lag_us", fields[index]); err != nil {
			return record{}, err
		}
		index++
	}
	if options.Retries {
		if parsed.retries, err = parseNonnegative(source, line, "retries", fields[index]); err != nil {
			return record{}, err
		}
		if parsed.outcome == outcomeSkipped && parsed.retries != 0 {
			return record{}, &ParseError{Source: source, Line: line, Field: "retries", Problem: "a skipped transaction cannot have retries"}
		}
	}
	return parsed, nil
}

func parseNonnegative(source string, line int, field string, value string) (int64, error) {
	if !canonicalUnsigned(value) {
		return 0, &ParseError{Source: source, Line: line, Field: field, Problem: fmt.Sprintf("expected a canonical non-negative integer, got %q", value)}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, &ParseError{Source: source, Line: line, Field: field, Problem: "integer is outside int64 range"}
	}
	return parsed, nil
}

func canonicalUnsigned(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (accumulator *accumulator) addRecord(source string, line int, record record) error {
	state, exists := accumulator.clients[record.clientID]
	if exists && state.source != source {
		return &ParseError{Source: source, Line: line, Field: "client_id", Problem: fmt.Sprintf("client %d also appears in worker log %s", record.clientID, state.source)}
	}
	if exists {
		if timestampLess(record.epochSeconds, record.epochMicrosecs, state.epochSeconds, state.epochMicrosecs) {
			return &ParseError{Source: source, Line: line, Field: "epoch_seconds", Problem: fmt.Sprintf("completion timestamp moves backwards for client %d", record.clientID)}
		}
		switch {
		case record.transactionNo < state.transactionNo:
			return &ParseError{Source: source, Line: line, Field: "transaction_no", Problem: fmt.Sprintf("transaction sequence moves backwards for client %d", record.clientID)}
		case record.transactionNo == state.transactionNo:
			if state.consumed {
				return &ParseError{Source: source, Line: line, Field: "transaction_no", Problem: fmt.Sprintf("transaction %d for client %d was already completed or failed", record.transactionNo, record.clientID)}
			}
			if record.outcome != outcomeSkipped {
				state.consumed = true
			}
		case record.transactionNo > state.transactionNo:
			if accumulator.options.SampleRate == 1 {
				if !state.consumed {
					return &ParseError{Source: source, Line: line, Field: "transaction_no", Problem: fmt.Sprintf("client %d advanced after only skipped records for transaction %d", record.clientID, state.transactionNo)}
				}
				if state.transactionNo == math.MaxInt64 || record.transactionNo != state.transactionNo+1 {
					return &ParseError{Source: source, Line: line, Field: "transaction_no", Problem: fmt.Sprintf("unsampled sequence gap for client %d: got %d after %d", record.clientID, record.transactionNo, state.transactionNo)}
				}
			}
			state.transactionNo = record.transactionNo
			state.consumed = record.outcome != outcomeSkipped
		}
	} else {
		state = clientSequence{source: source, transactionNo: record.transactionNo, consumed: record.outcome != outcomeSkipped}
	}
	state.epochSeconds = record.epochSeconds
	state.epochMicrosecs = record.epochMicrosecs
	accumulator.clients[record.clientID] = state

	if accumulator.logged == math.MaxInt64 {
		return &ParseError{Source: source, Line: line, Problem: "logged row count exceeds int64 range"}
	}
	accumulator.logged++
	if !accumulator.hasTimestamp || timestampLess(record.epochSeconds, record.epochMicrosecs, accumulator.firstSeconds, accumulator.firstMicros) {
		accumulator.firstSeconds = record.epochSeconds
		accumulator.firstMicros = record.epochMicrosecs
	}
	if !accumulator.hasTimestamp || timestampLess(accumulator.lastSeconds, accumulator.lastMicros, record.epochSeconds, record.epochMicrosecs) {
		accumulator.lastSeconds = record.epochSeconds
		accumulator.lastMicros = record.epochMicrosecs
	}
	accumulator.hasTimestamp = true
	switch record.outcome {
	case outcomeCompleted:
		accumulator.completed++
		accumulator.latencies = append(accumulator.latencies, record.latencyUS)
	case outcomeFailed:
		accumulator.failed++
	case outcomeSkipped:
		accumulator.skipped++
	}
	if accumulator.options.ScheduleLag {
		accumulator.lags = append(accumulator.lags, record.scheduleLagUS)
	}
	if accumulator.options.Retries && record.retries > 0 {
		accumulator.retried++
		if record.retries > math.MaxInt64-accumulator.totalRetries {
			return &ParseError{Source: source, Line: line, Field: "retries", Problem: "total retries exceed int64 range"}
		}
		accumulator.totalRetries += record.retries
	}
	return nil
}

func timestampLess(seconds int64, microseconds int64, previousSeconds int64, previousMicroseconds int64) bool {
	return seconds < previousSeconds || seconds == previousSeconds && microseconds < previousMicroseconds
}

func (accumulator *accumulator) result() (Result, error) {
	latency, err := summarize(accumulator.latencies)
	if err != nil {
		return Result{}, err
	}
	var lag *Distribution
	if accumulator.options.ScheduleLag {
		lag, err = summarize(accumulator.lags)
		if err != nil {
			return Result{}, err
		}
	}
	return Result{
		SchemaVersion:      ResultSchemaVersion,
		ParserVersion:      ParserVersion,
		Files:              accumulator.files,
		SampleRate:         accumulator.options.SampleRate,
		Sampled:            accumulator.options.SampleRate < 1,
		ScheduleLagPresent: accumulator.options.ScheduleLag,
		RetriesPresent:     accumulator.options.Retries,
		Logged:             accumulator.logged,
		Completed:          accumulator.completed,
		Failed:             accumulator.failed,
		Skipped:            accumulator.skipped,
		Retried:            accumulator.retried,
		TotalRetries:       accumulator.totalRetries,
		CompletionWindow: CompletionWindow{
			FirstEpochSeconds:      accumulator.firstSeconds,
			FirstEpochMicroseconds: accumulator.firstMicros,
			LastEpochSeconds:       accumulator.lastSeconds,
			LastEpochMicroseconds:  accumulator.lastMicros,
		},
		LatencyUS:     latency,
		ScheduleLagUS: lag,
	}, nil
}

func summarize(values []int64) (*Distribution, error) {
	if len(values) == 0 {
		return nil, nil
	}
	floatValues := make([]float64, len(values))
	minimum, maximum := values[0], values[0]
	mean := 0.0
	for index, value := range values {
		floatValue := float64(value)
		floatValues[index] = floatValue
		mean += (floatValue - mean) / float64(index+1)
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	if math.IsNaN(mean) || math.IsInf(mean, 0) {
		return nil, fmt.Errorf("pgbench transaction log mean overflowed finite float64 range")
	}
	p50, err := pgbenchresult.PercentileType7(floatValues, 0.50)
	if err != nil {
		return nil, err
	}
	p95, err := pgbenchresult.PercentileType7(floatValues, 0.95)
	if err != nil {
		return nil, err
	}
	p99, err := pgbenchresult.PercentileType7(floatValues, 0.99)
	if err != nil {
		return nil, err
	}
	return &Distribution{N: int64(len(values)), Min: minimum, Mean: mean, P50: p50, P95: p95, P99: p99, Max: maximum}, nil
}
