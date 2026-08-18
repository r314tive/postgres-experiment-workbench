package benchmarkimport

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	sysbenchVersionPattern = regexp.MustCompile(`^sysbench (1\.0(?:\.[0-9]+)?)(?:\s|$)`)
	totalTimePattern       = regexp.MustCompile(`^total time:\s*([0-9]+(?:\.[0-9]+)?)s$`)
	totalEventsPattern     = regexp.MustCompile(`^total number of events:\s*([0-9]+)$`)
	eventsRatePattern      = regexp.MustCompile(`^events per second:\s*([0-9]+(?:\.[0-9]+)?)$`)
	transactionsPattern    = regexp.MustCompile(`^transactions:\s*([0-9]+)\s*\(([0-9]+(?:\.[0-9]+)?) per sec\.\)$`)
	ignoredErrorsPattern   = regexp.MustCompile(`^ignored errors:\s*([0-9]+)(?:\s*\([0-9]+(?:\.[0-9]+)? per sec\.\))?$`)
	errorLinePattern       = regexp.MustCompile(`(?i)^(?:sysbench\s+)?(?:\[?error\]?|fatal):\s*(.+)$`)
	latencyAveragePattern  = regexp.MustCompile(`^avg:\s*([0-9]+(?:\.[0-9]+)?)$`)
	latencyP95Pattern      = regexp.MustCompile(`^95th percentile:\s*([0-9]+(?:\.[0-9]+)?)$`)
)

type parsedSysbench struct {
	DriverVersion string
	Metric        PrimaryMetric
	Errors        ErrorSummary
	Timing        Timing
}

type reportedRate struct {
	value         float64
	decimalPlaces int
}

func parseSysbench(content []byte) (parsedSysbench, error) {
	if len(content) == 0 {
		return parsedSysbench{}, fmt.Errorf("sysbench result is empty")
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return parsedSysbench{}, fmt.Errorf("sysbench result must be UTF-8 text without NUL bytes")
	}

	var version string
	var elapsed *float64
	var events *uint64
	var reportedEventsRate *reportedRate
	var transactions *uint64
	var transactionRate *reportedRate
	var ignoredErrors *uint64
	var errorMessages []string
	generalStatistics := false
	latencySection := false
	latencyAverage := false
	latencyP95 := false

	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r"))
		if line == "" {
			continue
		}
		if match := sysbenchVersionPattern.FindStringSubmatch(line); match != nil {
			if version != "" {
				return parsedSysbench{}, fmt.Errorf("line %d: duplicate sysbench version", lineNumber)
			}
			version = match[1]
			continue
		}
		switch line {
		case "General statistics:":
			if generalStatistics {
				return parsedSysbench{}, fmt.Errorf("line %d: duplicate General statistics section", lineNumber)
			}
			generalStatistics = true
			continue
		case "Latency (ms):":
			if latencySection {
				return parsedSysbench{}, fmt.Errorf("line %d: duplicate Latency section", lineNumber)
			}
			latencySection = true
			continue
		}
		if match := totalTimePattern.FindStringSubmatch(line); match != nil {
			value, err := parsePositiveFloat(match[1])
			if err != nil || elapsed != nil {
				return parsedSysbench{}, fmt.Errorf("line %d: invalid or duplicate total time", lineNumber)
			}
			elapsed = &value
			continue
		}
		if match := totalEventsPattern.FindStringSubmatch(line); match != nil {
			value, err := strconv.ParseUint(match[1], 10, 64)
			if err != nil || value == 0 || events != nil {
				return parsedSysbench{}, fmt.Errorf("line %d: invalid or duplicate total number of events", lineNumber)
			}
			events = &value
			continue
		}
		if match := eventsRatePattern.FindStringSubmatch(line); match != nil {
			value, err := parsePositiveFloat(match[1])
			if err != nil || reportedEventsRate != nil {
				return parsedSysbench{}, fmt.Errorf("line %d: invalid or duplicate events per second", lineNumber)
			}
			reportedEventsRate = &reportedRate{value: value, decimalPlaces: decimalPlaces(match[1])}
			continue
		}
		if match := transactionsPattern.FindStringSubmatch(line); match != nil {
			count, countErr := strconv.ParseUint(match[1], 10, 64)
			rate, rateErr := parsePositiveFloat(match[2])
			if countErr != nil || rateErr != nil || count == 0 || transactions != nil {
				return parsedSysbench{}, fmt.Errorf("line %d: invalid or duplicate transactions summary", lineNumber)
			}
			transactions = &count
			transactionRate = &reportedRate{value: rate, decimalPlaces: decimalPlaces(match[2])}
			continue
		}
		if match := ignoredErrorsPattern.FindStringSubmatch(line); match != nil {
			value, err := strconv.ParseUint(match[1], 10, 64)
			if err != nil || ignoredErrors != nil {
				return parsedSysbench{}, fmt.Errorf("line %d: invalid or duplicate ignored errors", lineNumber)
			}
			ignoredErrors = &value
			continue
		}
		if match := errorLinePattern.FindStringSubmatch(line); match != nil {
			message := strings.TrimSpace(match[1])
			if message == "" {
				message = line
			}
			errorMessages = append(errorMessages, message)
			continue
		}
		if latencySection {
			if match := latencyAveragePattern.FindStringSubmatch(line); match != nil {
				if _, err := parseNonNegativeFloat(match[1]); err != nil || latencyAverage {
					return parsedSysbench{}, fmt.Errorf("line %d: invalid or duplicate average latency", lineNumber)
				}
				latencyAverage = true
				continue
			}
			if match := latencyP95Pattern.FindStringSubmatch(line); match != nil {
				if _, err := parseNonNegativeFloat(match[1]); err != nil || latencyP95 {
					return parsedSysbench{}, fmt.Errorf("line %d: invalid or duplicate p95 latency", lineNumber)
				}
				latencyP95 = true
				continue
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return parsedSysbench{}, fmt.Errorf("scan sysbench result: %w", err)
	}
	if version == "" {
		return parsedSysbench{}, fmt.Errorf("missing supported sysbench 1.0 version line")
	}
	if !generalStatistics || elapsed == nil || events == nil {
		return parsedSysbench{}, fmt.Errorf("incomplete General statistics summary")
	}
	if !latencySection || !latencyAverage || !latencyP95 {
		return parsedSysbench{}, fmt.Errorf("incomplete Latency (ms) summary")
	}

	metric := PrimaryMetric{Direction: "higher"}
	switch {
	case transactionRate != nil:
		if transactions == nil || *transactions != *events {
			return parsedSysbench{}, fmt.Errorf("transactions count does not match total number of events")
		}
		if err := checkReportedRate("transactions per second", *events, *elapsed, *transactionRate); err != nil {
			return parsedSysbench{}, err
		}
		metric.Name = "transactions_per_second"
		metric.Value = transactionRate.value
		metric.Unit = "transactions/s"
		metric.Basis = "reported"
	case reportedEventsRate != nil:
		if err := checkReportedRate("events per second", *events, *elapsed, *reportedEventsRate); err != nil {
			return parsedSysbench{}, err
		}
		metric.Name = "events_per_second"
		metric.Value = reportedEventsRate.value
		metric.Unit = "events/s"
		metric.Basis = "reported"
	default:
		metric.Name = "events_per_second"
		metric.Value = float64(*events) / *elapsed
		metric.Unit = "events/s"
		metric.Basis = "derived-from-reported-totals"
	}
	if !finite(metric.Value) || metric.Value <= 0 {
		return parsedSysbench{}, fmt.Errorf("primary metric is not positive and finite")
	}

	errorTotal := uint64(len(errorMessages))
	if ignoredErrors != nil && *ignoredErrors > errorTotal {
		errorTotal = *ignoredErrors
	}
	return parsedSysbench{
		DriverVersion: version,
		Metric:        metric,
		Errors: ErrorSummary{
			Total:    errorTotal,
			Messages: uniqueSorted(errorMessages),
			Complete: true,
		},
		Timing: Timing{
			Basis:          "reported-elapsed",
			ElapsedSeconds: *elapsed,
		},
	}, nil
}

func parsePositiveFloat(value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || !finite(parsed) || parsed <= 0 {
		return 0, fmt.Errorf("expected positive finite number")
	}
	return parsed, nil
}

func parseNonNegativeFloat(value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || !finite(parsed) || parsed < 0 {
		return 0, fmt.Errorf("expected non-negative finite number")
	}
	return parsed, nil
}

// checkReportedRate prevents a syntactically valid console rate from becoming
// an attacker-selected primary metric. Sysbench prints the aggregate rate and
// elapsed time from separately rounded timer observations, so exact equality
// is not a valid contract (the retained upstream fixture differs by about
// 0.005%). V1.1 permits at most 0.1% timer-boundary skew plus half one unit in
// the last printed rate decimal. This bounded tolerance covers console
// rounding while rejecting materially inconsistent reported rates.
func checkReportedRate(label string, count uint64, elapsed float64, reported reportedRate) error {
	derived := float64(count) / elapsed
	if !finite(derived) || derived <= 0 {
		return fmt.Errorf("cannot derive a finite positive rate from reported totals")
	}
	rounding := 0.5 * math.Pow10(-reported.decimalPlaces)
	tolerance := derived*0.001 + rounding
	if math.Abs(reported.value-derived) > tolerance {
		return fmt.Errorf("reported %s %.12g is inconsistent with total events and elapsed time (derived %.12g, tolerance %.12g)", label, reported.value, derived, tolerance)
	}
	return nil
}

func decimalPlaces(value string) int {
	index := strings.IndexByte(value, '.')
	if index < 0 {
		return 0
	}
	return len(value) - index - 1
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
