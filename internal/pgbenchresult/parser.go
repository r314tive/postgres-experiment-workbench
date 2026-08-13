// Package pgbenchresult parses the final summary emitted by pgbench and
// computes statistics over independent benchmark trials.
package pgbenchresult

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	// ResultSchemaVersion identifies the serialized result contract.
	ResultSchemaVersion = "pgworkbench.pgbench-result/v1"
	// ParserVersion identifies the implementation of the text grammar. It is
	// deliberately independent from the result schema version.
	ParserVersion = "1.2.0"

	ModeTime         = "time"
	ModeTransactions = "transactions"
)

// Result is the normalized form of one pgbench measured-run summary. Optional
// numeric fields are pointers so deterministic JSON uses null instead of a
// non-portable NaN sentinel when pgbench did not report a value.
type Result struct {
	SchemaVersion           string          `json:"schema_version"`
	ParserVersion           string          `json:"parser_version"`
	PgbenchVersion          string          `json:"pgbench_version"`
	ServerVersion           string          `json:"server_version"`
	TransactionType         string          `json:"transaction_type"`
	ScaleFactor             int64           `json:"scale_factor"`
	QueryMode               string          `json:"query_mode"`
	Mode                    string          `json:"mode"`
	Clients                 int64           `json:"clients"`
	Threads                 int64           `json:"threads"`
	MaximumTries            int64           `json:"maximum_tries"`
	DurationSeconds         *float64        `json:"duration_seconds"`
	TransactionsPerClient   *int64          `json:"transactions_per_client"`
	TransactionsExpected    *int64          `json:"transactions_expected"`
	TransactionsProcessed   int64           `json:"transactions_processed"`
	TransactionsFailed      int64           `json:"transactions_failed"`
	SerializationFailures   *int64          `json:"serialization_failures"`
	DeadlockFailures        *int64          `json:"deadlock_failures"`
	OtherFailures           *int64          `json:"other_failures"`
	TransactionsSkipped     *int64          `json:"transactions_skipped"`
	TransactionsRetried     *int64          `json:"transactions_retried"`
	TotalRetries            *int64          `json:"total_retries"`
	LatencyLimitMS          *float64        `json:"latency_limit_ms"`
	TransactionsAboveLimit  *int64          `json:"transactions_above_latency_limit"`
	LatencyLimitTotal       *int64          `json:"latency_limit_total"`
	LatencyMeanMS           float64         `json:"latency_mean_ms"`
	LatencyStddevMS         *float64        `json:"latency_stddev_ms"`
	ScheduleLagAverageMS    *float64        `json:"schedule_lag_average_ms"`
	ScheduleLagMaxMS        *float64        `json:"schedule_lag_max_ms"`
	InitialConnectionTimeMS *float64        `json:"initial_connection_time_ms"`
	AverageConnectionTimeMS *float64        `json:"average_connection_time_ms"`
	TPSIncludingConnections *float64        `json:"tps_including_connections"`
	TPSExcludingConnections *float64        `json:"tps_excluding_connections"`
	tpsIncludingObservation *tpsObservation `json:"-"`
	tpsExcludingObservation *tpsObservation `json:"-"`
}

// tpsObservation retains syntax needed for an independent integrity check
// without widening the serialized result contract.
type tpsObservation struct {
	decimalPlaces int
	qualifier     string
}

// ParseError reports a fail-closed grammar or completeness problem.
type ParseError struct {
	Line    int
	Field   string
	Problem string
}

func (e *ParseError) Error() string {
	where := "pgbench summary"
	if e.Line > 0 {
		where = fmt.Sprintf("pgbench summary line %d", e.Line)
	}
	if e.Field != "" {
		return fmt.Sprintf("%s (%s): %s", where, e.Field, e.Problem)
	}
	return fmt.Sprintf("%s: %s", where, e.Problem)
}

var (
	unsignedIntegerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	numberPattern          = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
	processedPattern       = regexp.MustCompile(`^(0|[1-9][0-9]*)(?:/(0|[1-9][0-9]*))?$`)
	countPercentPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)(?: \((?:0|[1-9][0-9]*)(?:\.[0-9]+)?%\))?$`)
	latencyLimitPattern    = regexp.MustCompile(`^number of transactions above the ((?:0|[1-9][0-9]*)(?:\.[0-9]+)?) ms latency limit: (0|[1-9][0-9]*)/(0|[1-9][0-9]*) \((?:0|[1-9][0-9]*)(?:\.[0-9]+)? ?%\)$`)
	scheduleLagPattern     = regexp.MustCompile(`^rate limit schedule lag: avg ((?:0|[1-9][0-9]*)(?:\.[0-9]+)?) \(max ((?:0|[1-9][0-9]*)(?:\.[0-9]+)?)\) ms$`)
	pgbenchBannerPattern   = regexp.MustCompile(`^pgbench \((.+)\)$`)
	majorVersionPattern    = regexp.MustCompile(`^([1-9][0-9]*)`)
)

type parsedSummary struct {
	result Result
	seen   map[string]bool
}

// Parse reads the last measured pgbench summary from r. Earlier measured
// summaries and initialization/progress output are ignored. The last summary
// itself is parsed strictly: unknown fields, duplicate fields, localized
// labels, malformed numbers, and missing required fields return an error.
func Parse(r io.Reader) (Result, error) {
	lines, err := readLines(r)
	if err != nil {
		return Result{}, err
	}

	start := -1
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "transaction type:") {
			start = index
		}
	}
	if start < 0 {
		return Result{}, &ParseError{Problem: "measured summary start field transaction type not found (localized and truncated output is unsupported)"}
	}

	parsed := parsedSummary{
		result: Result{
			SchemaVersion: ResultSchemaVersion,
			ParserVersion: ParserVersion,
		},
		seen: make(map[string]bool),
	}
	parseNearestBanner(lines[:start], &parsed.result)

	for index := start; index < len(lines); index++ {
		lineNumber := index + 1
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if index > start && strings.HasPrefix(line, "transaction type:") {
			return Result{}, &ParseError{Line: lineNumber, Problem: "unexpected nested measured summary"}
		}
		if isReportTerminator(line) {
			break
		}
		if isKnownNonSummaryLine(line) {
			continue
		}

		matched, err := parsed.parseLine(lineNumber, line)
		if err != nil {
			return Result{}, err
		}
		if !matched {
			return Result{}, &ParseError{Line: lineNumber, Problem: fmt.Sprintf("unknown summary field or output line %q", line)}
		}
	}

	if err := parsed.validate(); err != nil {
		return Result{}, err
	}
	return parsed.result, nil
}

func readLines(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, strings.TrimSuffix(scanner.Text(), "\r"))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read pgbench output: %w", err)
	}
	return lines, nil
}

func parseNearestBanner(lines []string, result *Result) {
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if strings.HasPrefix(line, "transaction type:") {
			// Do not inherit a version banner from an earlier concatenated
			// measured result.
			return
		}
		matches := pgbenchBannerPattern.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		inside := matches[1]
		if split := strings.Index(inside, ", server "); split >= 0 {
			result.PgbenchVersion = strings.TrimSpace(inside[:split])
			result.ServerVersion = strings.TrimSpace(inside[split+len(", server "):])
		} else {
			result.PgbenchVersion = strings.TrimSpace(inside)
		}
		return
	}
}

func isReportTerminator(line string) bool {
	return strings.HasPrefix(line, "statement latencies in milliseconds") ||
		strings.HasPrefix(line, "SQL script ") ||
		line == "script statistics:"
}

func isKnownNonSummaryLine(line string) bool {
	return strings.HasPrefix(line, "progress: ") ||
		line == "starting vacuum...end." ||
		strings.HasPrefix(line, "pgbench (")
}

func (p *parsedSummary) parseLine(lineNumber int, line string) (bool, error) {
	colonFields := []struct {
		prefix string
		field  string
		apply  func(string) error
	}{
		{"transaction type: ", "transaction_type", func(value string) error {
			if value == "" {
				return fmt.Errorf("must not be empty")
			}
			p.result.TransactionType = value
			return nil
		}},
		{"scaling factor: ", "scale_factor", func(value string) error {
			parsed, err := parsePositiveInt(value)
			p.result.ScaleFactor = parsed
			return err
		}},
		{"query mode: ", "query_mode", func(value string) error {
			switch value {
			case "simple", "extended", "prepared":
				p.result.QueryMode = value
				return nil
			default:
				return fmt.Errorf("unsupported query mode %q", value)
			}
		}},
		{"number of clients: ", "clients", func(value string) error {
			parsed, err := parsePositiveInt(value)
			p.result.Clients = parsed
			return err
		}},
		{"number of threads: ", "threads", func(value string) error {
			parsed, err := parsePositiveInt(value)
			p.result.Threads = parsed
			return err
		}},
		{"maximum number of tries: ", "maximum_tries", func(value string) error {
			parsed, err := parseNonNegativeInt(value)
			p.result.MaximumTries = parsed
			return err
		}},
		{"duration: ", "duration_seconds", func(value string) error {
			if !strings.HasSuffix(value, " s") {
				return fmt.Errorf("expected seconds with s suffix")
			}
			parsed, err := parsePositiveInt(strings.TrimSuffix(value, " s"))
			p.result.DurationSeconds = float64Pointer(float64(parsed))
			return err
		}},
		{"number of transactions per client: ", "transactions_per_client", func(value string) error {
			parsed, err := parsePositiveInt(value)
			p.result.TransactionsPerClient = int64Pointer(parsed)
			return err
		}},
		{"number of transactions actually processed: ", "transactions_processed", func(value string) error {
			processed, expected, err := parseProcessed(value)
			p.result.TransactionsProcessed = processed
			p.result.TransactionsExpected = expected
			return err
		}},
		{"number of failed transactions: ", "transactions_failed", func(value string) error {
			parsed, err := parseCountWithOptionalPercent(value)
			p.result.TransactionsFailed = parsed
			return err
		}},
		{"number of serialization failures: ", "serialization_failures", func(value string) error {
			parsed, err := parseCountWithOptionalPercent(value)
			p.result.SerializationFailures = int64Pointer(parsed)
			return err
		}},
		{"number of deadlock failures: ", "deadlock_failures", func(value string) error {
			parsed, err := parseCountWithOptionalPercent(value)
			p.result.DeadlockFailures = int64Pointer(parsed)
			return err
		}},
		{"number of other failures: ", "other_failures", func(value string) error {
			parsed, err := parseCountWithOptionalPercent(value)
			p.result.OtherFailures = int64Pointer(parsed)
			return err
		}},
		{"number of transactions skipped: ", "transactions_skipped", func(value string) error {
			parsed, err := parseCountWithOptionalPercent(value)
			p.result.TransactionsSkipped = int64Pointer(parsed)
			return err
		}},
		{"number of transactions retried: ", "transactions_retried", func(value string) error {
			parsed, err := parseCountWithOptionalPercent(value)
			p.result.TransactionsRetried = int64Pointer(parsed)
			return err
		}},
		{"total number of retries: ", "total_retries", func(value string) error {
			parsed, err := parseNonNegativeInt(value)
			p.result.TotalRetries = int64Pointer(parsed)
			return err
		}},
	}

	for _, candidate := range colonFields {
		if !strings.HasPrefix(line, candidate.prefix) {
			continue
		}
		if err := p.mark(candidate.field, lineNumber); err != nil {
			return true, err
		}
		if err := candidate.apply(strings.TrimSpace(strings.TrimPrefix(line, candidate.prefix))); err != nil {
			return true, &ParseError{Line: lineNumber, Field: candidate.field, Problem: err.Error()}
		}
		return true, nil
	}

	for _, candidate := range []struct {
		prefix string
		field  string
		target **float64
	}{
		{"latency average", "latency_mean_ms", nil},
		{"latency stddev", "latency_stddev_ms", &p.result.LatencyStddevMS},
		{"initial connection time", "initial_connection_time_ms", &p.result.InitialConnectionTimeMS},
		{"average connection time", "average_connection_time_ms", &p.result.AverageConnectionTimeMS},
	} {
		value, ok := metricValue(line, candidate.prefix, "ms")
		if !ok {
			continue
		}
		if err := p.mark(candidate.field, lineNumber); err != nil {
			return true, err
		}
		parsed, err := parseNonNegativeFloat(value)
		if err != nil {
			return true, &ParseError{Line: lineNumber, Field: candidate.field, Problem: err.Error()}
		}
		if candidate.field == "latency_mean_ms" {
			p.result.LatencyMeanMS = parsed
		} else {
			*candidate.target = float64Pointer(parsed)
		}
		return true, nil
	}

	if strings.HasPrefix(line, "tps = ") {
		return true, p.parseTPS(lineNumber, strings.TrimPrefix(line, "tps = "))
	}

	if strings.HasPrefix(line, "number of transactions above the ") {
		matches := latencyLimitPattern.FindStringSubmatch(line)
		if matches == nil {
			return true, &ParseError{Line: lineNumber, Field: "transactions_above_latency_limit", Problem: "malformed count/percentage"}
		}
		if err := p.mark("latency_limit", lineNumber); err != nil {
			return true, err
		}
		limit, err := parseNonNegativeFloat(matches[1])
		if err != nil {
			return true, &ParseError{Line: lineNumber, Field: "latency_limit_ms", Problem: err.Error()}
		}
		above, err := parseNonNegativeInt(matches[2])
		if err != nil {
			return true, &ParseError{Line: lineNumber, Field: "transactions_above_latency_limit", Problem: err.Error()}
		}
		total, err := parsePositiveInt(matches[3])
		if err != nil {
			return true, &ParseError{Line: lineNumber, Field: "latency_limit_total", Problem: err.Error()}
		}
		p.result.LatencyLimitMS = float64Pointer(limit)
		p.result.TransactionsAboveLimit = int64Pointer(above)
		p.result.LatencyLimitTotal = int64Pointer(total)
		return true, nil
	}
	if strings.HasPrefix(line, "rate limit schedule lag: ") {
		matches := scheduleLagPattern.FindStringSubmatch(line)
		if matches == nil {
			return true, &ParseError{Line: lineNumber, Field: "schedule_lag", Problem: "malformed avg/max milliseconds"}
		}
		if err := p.mark("schedule_lag", lineNumber); err != nil {
			return true, err
		}
		average, err := parseNonNegativeFloat(matches[1])
		if err != nil {
			return true, &ParseError{Line: lineNumber, Field: "schedule_lag_average_ms", Problem: err.Error()}
		}
		maximum, err := parseNonNegativeFloat(matches[2])
		if err != nil {
			return true, &ParseError{Line: lineNumber, Field: "schedule_lag_max_ms", Problem: err.Error()}
		}
		p.result.ScheduleLagAverageMS = float64Pointer(average)
		p.result.ScheduleLagMaxMS = float64Pointer(maximum)
		return true, nil
	}

	return false, nil
}

func (p *parsedSummary) parseTPS(lineNumber int, value string) error {
	open := strings.LastIndex(value, " (")
	if open < 0 || !strings.HasSuffix(value, ")") {
		return &ParseError{Line: lineNumber, Field: "tps", Problem: "missing recognized connection-time qualifier"}
	}
	numberText := strings.TrimSpace(value[:open])
	qualifier := value[open+2 : len(value)-1]
	number, err := parsePositiveFloat(numberText)
	if err != nil {
		return &ParseError{Line: lineNumber, Field: "tps", Problem: err.Error()}
	}

	var field string
	var target **float64
	switch qualifier {
	case "including connections establishing", "including reconnection times":
		field = "tps_including_connections"
		target = &p.result.TPSIncludingConnections
	case "excluding connections establishing", "without initial connection time":
		field = "tps_excluding_connections"
		target = &p.result.TPSExcludingConnections
	default:
		return &ParseError{Line: lineNumber, Field: "tps", Problem: fmt.Sprintf("unsupported qualifier %q", qualifier)}
	}
	if err := p.mark(field, lineNumber); err != nil {
		return err
	}
	*target = float64Pointer(number)
	observation := &tpsObservation{decimalPlaces: decimalPlaces(numberText), qualifier: qualifier}
	if field == "tps_including_connections" {
		p.result.tpsIncludingObservation = observation
	} else {
		p.result.tpsExcludingObservation = observation
	}
	return nil
}

func (p *parsedSummary) mark(field string, line int) error {
	if p.seen[field] {
		return &ParseError{Line: line, Field: field, Problem: "duplicate field"}
	}
	p.seen[field] = true
	return nil
}

func (p *parsedSummary) validate() error {
	if p.result.PgbenchVersion == "" {
		return &ParseError{Field: "pgbench_version", Problem: "standard pgbench version banner is missing"}
	}
	majorMatches := majorVersionPattern.FindStringSubmatch(p.result.PgbenchVersion)
	if majorMatches == nil {
		return &ParseError{Field: "pgbench_version", Problem: fmt.Sprintf("cannot determine supported major from %q", p.result.PgbenchVersion)}
	}
	major, err := strconv.Atoi(majorMatches[1])
	if err != nil || major < 15 || major > 19 {
		return &ParseError{Field: "pgbench_version", Problem: fmt.Sprintf("unsupported pgbench major in %q; supported majors are 15 through 19", p.result.PgbenchVersion)}
	}
	if p.result.ServerVersion != "" {
		if _, err := EffectiveServerMajor(p.result); err != nil {
			return &ParseError{Field: "server_version", Problem: err.Error()}
		}
	}

	required := []string{
		"transaction_type",
		"scale_factor",
		"query_mode",
		"clients",
		"threads",
		"transactions_processed",
		"transactions_failed",
		"latency_mean_ms",
	}
	for _, field := range required {
		if !p.seen[field] {
			return &ParseError{Field: field, Problem: "required field is missing"}
		}
	}
	if !p.seen["tps_including_connections"] && !p.seen["tps_excluding_connections"] {
		return &ParseError{Field: "tps", Problem: "at least one recognized TPS value is required"}
	}

	hasDuration := p.seen["duration_seconds"]
	hasTransactions := p.seen["transactions_per_client"]
	if hasDuration == hasTransactions {
		return &ParseError{Field: "mode", Problem: "exactly one of duration or transactions per client is required"}
	}
	if hasDuration {
		p.result.Mode = ModeTime
	} else {
		p.result.Mode = ModeTransactions
		derived, overflow := multiplyPositive(p.result.Clients, *p.result.TransactionsPerClient)
		if overflow {
			return &ParseError{Field: "transactions_expected", Problem: "clients multiplied by transactions per client overflows int64"}
		}
		if p.result.TransactionsExpected == nil {
			p.result.TransactionsExpected = int64Pointer(derived)
		} else if *p.result.TransactionsExpected != derived {
			return &ParseError{Field: "transactions_expected", Problem: fmt.Sprintf("reported %d, expected clients*transactions_per_client=%d", *p.result.TransactionsExpected, derived)}
		}
	}

	if p.result.TransactionsProcessed <= 0 {
		return &ParseError{Field: "transactions_processed", Problem: "must be positive for a measured result"}
	}
	if p.result.TransactionsExpected != nil {
		expected := *p.result.TransactionsExpected
		if p.result.TransactionsProcessed > expected {
			return &ParseError{Field: "transactions_processed", Problem: "processed count exceeds expected count"}
		}
		skipped := int64(0)
		if p.result.TransactionsSkipped != nil {
			skipped = *p.result.TransactionsSkipped
		}
		if p.result.TransactionsFailed > expected-p.result.TransactionsProcessed || skipped > expected-p.result.TransactionsProcessed-p.result.TransactionsFailed {
			return &ParseError{Field: "transaction_counts", Problem: "processed plus failed plus skipped count exceeds expected count"}
		}
	}
	detailedFailures := int64(0)
	for _, count := range []*int64{p.result.SerializationFailures, p.result.DeadlockFailures, p.result.OtherFailures} {
		if count != nil {
			detailedFailures += *count
		}
	}
	if detailedFailures > p.result.TransactionsFailed {
		return &ParseError{Field: "failure_breakdown", Problem: "detailed failure counts exceed total failed transactions"}
	}
	if p.result.LatencyLimitTotal != nil {
		if *p.result.TransactionsAboveLimit > *p.result.LatencyLimitTotal {
			return &ParseError{Field: "transactions_above_latency_limit", Problem: "late transaction count exceeds latency-limit denominator"}
		}
		if *p.result.LatencyLimitTotal != p.result.TransactionsProcessed {
			return &ParseError{Field: "latency_limit_total", Problem: "latency-limit denominator must equal completed transactions"}
		}
	}
	if p.result.ScheduleLagAverageMS != nil && *p.result.ScheduleLagAverageMS > *p.result.ScheduleLagMaxMS {
		return &ParseError{Field: "schedule_lag", Problem: "average schedule lag exceeds maximum schedule lag"}
	}
	hasRetried := p.result.TransactionsRetried != nil
	hasTotalRetries := p.result.TotalRetries != nil
	if hasRetried != hasTotalRetries {
		return &ParseError{Field: "retries", Problem: "retried transactions and total retries must either both be present or both be absent"}
	}
	if !p.seen["maximum_tries"] {
		if !hasRetried {
			return &ParseError{Field: "maximum_tries", Problem: "required field is missing"}
		}
		// pgbench deliberately omits the maximum-tries line for the standard
		// unlimited --max-tries=0 form, while still reporting retry counts.
		p.result.MaximumTries = 0
	}
	if p.result.MaximumTries == 1 && hasRetried {
		return &ParseError{Field: "retries", Problem: "retry fields are inconsistent with maximum number of tries 1"}
	}
	if p.result.MaximumTries != 1 && !hasRetried {
		return &ParseError{Field: "retries", Problem: "maximum number of tries other than 1 requires retried transactions and total retries"}
	}
	if p.result.TransactionsRetried != nil && p.result.TotalRetries != nil && *p.result.TotalRetries < *p.result.TransactionsRetried {
		return &ParseError{Field: "total_retries", Problem: "total retries is smaller than number of retried transactions"}
	}
	return nil
}

// EffectiveServerMajor returns the PostgreSQL server major reported by the
// pgbench banner. pgbench omits the separate "server" value when its own
// version string represents the connected server, so that form deliberately
// falls back to PgbenchVersion.
func EffectiveServerMajor(result Result) (string, error) {
	version := result.ServerVersion
	if version == "" {
		version = result.PgbenchVersion
	}
	matches := majorVersionPattern.FindStringSubmatch(version)
	if matches == nil {
		return "", fmt.Errorf("cannot determine supported PostgreSQL major from %q", version)
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil || major < 15 || major > 19 {
		return "", fmt.Errorf("unsupported PostgreSQL server major in %q; supported majors are 15 through 19", version)
	}
	return strconv.Itoa(major), nil
}

// ValidateServerMajor binds the driver-observed server banner to the linked
// experiment manifest instead of trusting either evidence source alone.
func ValidateServerMajor(result Result, expected string) error {
	actual, err := EffectiveServerMajor(result)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("pgbench server major mismatch: banner reports %s, linked manifest reports %s", actual, expected)
	}
	return nil
}

func parsePositiveInt(value string) (int64, error) {
	parsed, err := parseNonNegativeInt(value)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return parsed, nil
}

func parseNonNegativeInt(value string) (int64, error) {
	if !unsignedIntegerPattern.MatchString(value) {
		return 0, fmt.Errorf("expected a canonical non-negative integer, got %q", value)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("integer out of range: %w", err)
	}
	return parsed, nil
}

func parsePositiveFloat(value string) (float64, error) {
	parsed, err := parseNonNegativeFloat(value)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return parsed, nil
}

func parseNonNegativeFloat(value string) (float64, error) {
	if !numberPattern.MatchString(value) {
		return 0, fmt.Errorf("expected a canonical non-negative decimal, got %q", value)
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("invalid finite decimal %q", value)
	}
	return parsed, nil
}

func parseProcessed(value string) (int64, *int64, error) {
	matches := processedPattern.FindStringSubmatch(value)
	if matches == nil {
		return 0, nil, fmt.Errorf("expected N or N/N canonical count, got %q", value)
	}
	processed, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("processed count out of range: %w", err)
	}
	if matches[2] == "" {
		return processed, nil, nil
	}
	expected, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("expected count out of range: %w", err)
	}
	return processed, int64Pointer(expected), nil
}

func parseCountWithOptionalPercent(value string) (int64, error) {
	matches := countPercentPattern.FindStringSubmatch(value)
	if matches == nil {
		return 0, fmt.Errorf("expected count with optional canonical percentage, got %q", value)
	}
	parsed, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("count out of range: %w", err)
	}
	return parsed, nil
}

func metricValue(line string, name string, unit string) (string, bool) {
	line = strings.TrimSuffix(line, " (including failures)")
	for _, separator := range []string{" = ", ": "} {
		prefix := name + separator
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, " "+unit) {
			return strings.TrimSuffix(strings.TrimPrefix(line, prefix), " "+unit), true
		}
	}
	return "", false
}

func multiplyPositive(left int64, right int64) (int64, bool) {
	if left <= 0 || right <= 0 || left > math.MaxInt64/right {
		return 0, true
	}
	return left * right, false
}

func decimalPlaces(value string) int {
	index := strings.IndexByte(value, '.')
	if index < 0 {
		return 0
	}
	return len(value) - index - 1
}

func int64Pointer(value int64) *int64       { return &value }
func float64Pointer(value float64) *float64 { return &value }

// MarshalDeterministic serializes package evidence structs using stable field
// order, two-space indentation, and one trailing newline.
func MarshalDeterministic(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal deterministic JSON: %w", err)
	}
	return append(encoded, '\n'), nil
}

// JSON returns the deterministic JSON representation of a parsed result.
func (r Result) JSON() ([]byte, error) {
	return MarshalDeterministic(r)
}
