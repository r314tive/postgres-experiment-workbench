// Package benchmarkmetrics normalizes the PostgreSQL sampler CSV retained by
// one benchmark trial. It deliberately reports descriptive counters and
// gauges only; it does not infer tuning quality or benchmark causality.
package benchmarkmetrics

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	SchemaVersion = "pgworkbench.benchmark-postgres-metrics/v2"
	ArtifactType  = "pgworkbench.benchmark-postgres-metrics"
	ParserVersion = "postgresql-sampler-csv/v2"
	SourcePath    = "metrics.csv"

	maxSourceBytes = int64(64 << 20)

	// CadenceGapToleranceIntervals is an explicit fail-closed wall-clock
	// allowance. A sample gap may consume one declared interval plus at most
	// one further interval of query/scheduler/terminal-boundary jitter. Protocol
	// v2 calibrated runs additionally correlate every regular metrics row with
	// the runner's exact monotonic-grid timing evidence.
	CadenceGapToleranceIntervals = int64(2)
)

var (
	expectedHeader = []string{
		"sampled_at", "database_name",
		"active_sessions", "waiting_sessions", "lock_waiting_sessions", "blocked_sessions", "locks_total", "locks_waiting",
		"xact_commit", "xact_rollback", "blks_read", "blks_hit", "tup_returned", "tup_fetched", "tup_inserted", "tup_updated", "tup_deleted", "conflicts", "deadlocks", "temp_files", "temp_bytes",
		"wal_records", "wal_fpi", "wal_bytes", "current_wal_lsn",
	}
	gaugeNames = []string{
		"active_sessions", "waiting_sessions", "lock_waiting_sessions", "blocked_sessions", "locks_total", "locks_waiting",
	}
	databaseCounterNames = []string{
		"xact_commit", "xact_rollback", "blks_read", "blks_hit", "tup_returned", "tup_fetched", "tup_inserted", "tup_updated", "tup_deleted", "conflicts", "deadlocks", "temp_files", "temp_bytes",
	}
	walCounterNames = []string{"wal_records", "wal_fpi", "wal_bytes"}
	decimalInteger  = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	walLSN          = regexp.MustCompile(`^[0-9A-F]+/[0-9A-F]+$`)
)

type SourceEvidence struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
}

type MeasureWindow struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// Coverage describes the minimal contiguous sampler window that brackets the
// measure phase: the latest sample at/before start through the earliest sample
// at/after finish. No warmup/cooldown samples beyond those boundaries enter
// aggregates.
type Coverage struct {
	FirstSampleAt   string `json:"first_sample_at"`
	LastSampleAt    string `json:"last_sample_at"`
	Samples         int    `json:"samples"`
	TotalSamples    int    `json:"total_samples"`
	LeadingSlackNS  int64  `json:"leading_slack_ns"`
	TrailingSlackNS int64  `json:"trailing_slack_ns"`
}

type CollectorCadence struct {
	Collector             string          `json:"collector"`
	DeclaredIntervalNS    int64           `json:"declared_interval_ns"`
	MaxConsecutiveGapNS   int64           `json:"max_consecutive_gap_ns"`
	MaxAllowedGapNS       int64           `json:"max_allowed_gap_ns"`
	VerificationMode      string          `json:"verification_mode"`
	RegularSamplesMatched int             `json:"regular_samples_matched"`
	BoundarySamples       int             `json:"boundary_samples"`
	ControlDigest         string          `json:"control_digest,omitempty"`
	RawSource             *SourceEvidence `json:"raw_source,omitempty"`
}

type ResetScope struct {
	Scope              string `json:"scope"`
	BeforeAvailability string `json:"before_availability"`
	BeforeResetAt      string `json:"before_reset_at,omitempty"`
	ResetAt            string `json:"reset_at,omitempty"`
}

type StatisticsResetEvidence struct {
	Policy           string          `json:"policy"`
	Boundary         string          `json:"boundary"`
	VerificationMode string          `json:"verification_mode"`
	Status           string          `json:"status"`
	ControlDigest    string          `json:"control_digest,omitempty"`
	RawSource        *SourceEvidence `json:"raw_source,omitempty"`
	Database         ResetScope      `json:"database"`
	WAL              ResetScope      `json:"wal"`
}

type DatabaseIdentity struct {
	Name string `json:"name"`
}

// CounterDelta uses decimal strings so PostgreSQL numeric wal_bytes and bigint
// pg_stat_database values remain lossless outside JavaScript's integer range.
type CounterDelta struct {
	Scope        string `json:"scope"`
	Name         string `json:"name"`
	First        string `json:"first"`
	Last         string `json:"last"`
	Delta        string `json:"delta"`
	Segments     int    `json:"segments"`
	ResetApplied bool   `json:"reset_applied"`
}

type GaugeSummary struct {
	Name string  `json:"name"`
	Mean float64 `json:"mean"`
	Max  uint64  `json:"max"`
}

type Summary struct {
	SchemaVersion       string                  `json:"schema_version"`
	ArtifactType        string                  `json:"artifact_type"`
	ParserVersion       string                  `json:"parser_version"`
	PostgresServerMajor string                  `json:"postgres_server_major"`
	Source              SourceEvidence          `json:"source"`
	Measure             MeasureWindow           `json:"measure"`
	Coverage            Coverage                `json:"coverage"`
	Cadence             CollectorCadence        `json:"cadence"`
	StatisticsReset     StatisticsResetEvidence `json:"statistics_reset"`
	Database            DatabaseIdentity        `json:"database"`
	CounterDeltas       []CounterDelta          `json:"counter_deltas"`
	Gauges              []GaugeSummary          `json:"gauges"`
	Digest              string                  `json:"digest"`
}

type DeriveOptions struct {
	Path                     string
	PostgresServerMajor      string
	MeasureStartedAt         string
	MeasureFinishedAt        string
	CollectorIntervalSeconds int
	ContractVersion          string
	StatisticsResetPolicy    string
	StatisticsResetBoundary  string
	ControlBinding           *benchmarkcontrol.Binding
	StatisticsReset          *benchmarkcontrol.StatisticsReset
	StatisticsResetSource    []byte
	CollectorOverhead        *benchmarkcontrol.CollectorOverhead
	CollectorOverheadSource  []byte
}

type sample struct {
	at       time.Time
	database string
	gauges   map[string]uint64
	counters map[string]*big.Int
}

// DeriveFile strictly parses the stable PostgreSQL 15-19 sampler CSV shape and
// derives one deterministic, measure-scoped descriptive summary. The declared
// interval is mandatory: cadence is part of the evidence contract, not a
// property inferred after observing the samples.
func DeriveFile(options DeriveOptions) (Summary, error) {
	if !supportedMajor(options.PostgresServerMajor) {
		return Summary{}, fmt.Errorf("unsupported PostgreSQL server major %q: expected 15 through 19", options.PostgresServerMajor)
	}
	if options.CollectorIntervalSeconds <= 0 || int64(options.CollectorIntervalSeconds) > int64(time.Hour/time.Second) {
		return Summary{}, fmt.Errorf("collector interval must be between 1 and 3600 seconds")
	}
	contractVersion := options.ContractVersion
	if contractVersion == "" {
		contractVersion = "1"
	}
	if contractVersion != "1" && contractVersion != "2" {
		return Summary{}, fmt.Errorf("unsupported benchmark contract version %q", contractVersion)
	}
	measureStarted, err := parseUTC(options.MeasureStartedAt)
	if err != nil {
		return Summary{}, fmt.Errorf("measure started_at: %w", err)
	}
	measureFinished, err := parseUTC(options.MeasureFinishedAt)
	if err != nil {
		return Summary{}, fmt.Errorf("measure finished_at: %w", err)
	}
	if !measureFinished.After(measureStarted) {
		return Summary{}, fmt.Errorf("measure interval must have positive duration")
	}

	content, info, err := readSource(options.Path)
	if err != nil {
		return Summary{}, err
	}
	samples, err := parseCSV(content)
	if err != nil {
		return Summary{}, err
	}
	startIndex, finishIndex := coverageIndexes(samples, measureStarted, measureFinished)
	if startIndex < 0 {
		return Summary{}, fmt.Errorf("metrics.csv has no sample at or before measure start")
	}
	if finishIndex < 0 {
		return Summary{}, fmt.Errorf("metrics.csv has no sample at or after measure finish")
	}
	if startIndex >= finishIndex {
		return Summary{}, fmt.Errorf("metrics.csv does not provide distinct samples bracketing the positive measure interval")
	}
	selected := samples[startIndex : finishIndex+1]

	reset, resetBoundaries, err := normalizeResetEvidence(options, contractVersion)
	if err != nil {
		return Summary{}, err
	}
	cadence, err := normalizeCadence(options, contractVersion, samples, selected)
	if err != nil {
		return Summary{}, err
	}
	counters, err := summarizeCounters(selected, resetBoundaries)
	if err != nil {
		return Summary{}, err
	}
	gauges, err := summarizeGauges(selected)
	if err != nil {
		return Summary{}, err
	}
	leading := measureStarted.Sub(selected[0].at)
	trailing := selected[len(selected)-1].at.Sub(measureFinished)
	if leading < 0 || trailing < 0 {
		return Summary{}, fmt.Errorf("internal metrics coverage selection escaped measure boundaries")
	}

	summary := Summary{
		SchemaVersion:       SchemaVersion,
		ArtifactType:        ArtifactType,
		ParserVersion:       ParserVersion,
		PostgresServerMajor: options.PostgresServerMajor,
		Source: SourceEvidence{
			Path: SourcePath, Digest: evidence.DigestBytes(content), SizeBytes: info.Size(),
		},
		Measure: MeasureWindow{
			StartedAt: canonicalTime(measureStarted), FinishedAt: canonicalTime(measureFinished),
		},
		Coverage: Coverage{
			FirstSampleAt: canonicalTime(selected[0].at), LastSampleAt: canonicalTime(selected[len(selected)-1].at),
			Samples: len(selected), TotalSamples: len(samples), LeadingSlackNS: leading.Nanoseconds(), TrailingSlackNS: trailing.Nanoseconds(),
		},
		Cadence:         cadence,
		StatisticsReset: reset,
		Database:        DatabaseIdentity{Name: selected[0].database},
		CounterDeltas:   counters,
		Gauges:          gauges,
	}
	summary.Digest, err = summaryDigest(summary)
	if err != nil {
		return Summary{}, err
	}
	return summary, nil
}

func readSource(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect metrics.csv: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("metrics.csv must be a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > maxSourceBytes {
		return nil, nil, fmt.Errorf("metrics.csv size must be between 1 and %d bytes", maxSourceBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read metrics.csv: %w", err)
	}
	return content, info, nil
}

func parseCSV(content []byte) ([]sample, error) {
	reader := csv.NewReader(bytes.NewReader(content))
	reader.FieldsPerRecord = len(expectedHeader)
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("metrics.csv header: %w", err)
	}
	if !slices.Equal(header, expectedHeader) {
		return nil, fmt.Errorf("metrics.csv header does not match the stable PostgreSQL sampler CSV contract")
	}
	indexes := make(map[string]int, len(header))
	for index, name := range header {
		indexes[name] = index
	}
	var samples []sample
	var previous time.Time
	database := ""
	for row := 2; ; row++ {
		record, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("metrics.csv row %d: %w", row, err)
		}
		at, err := parseUTC(record[indexes["sampled_at"]])
		if err != nil {
			return nil, fmt.Errorf("metrics.csv row %d sampled_at: %w", row, err)
		}
		if !previous.IsZero() && !at.After(previous) {
			return nil, fmt.Errorf("metrics.csv sampled_at values are not strictly monotonic at row %d", row)
		}
		previous = at
		name := record[indexes["database_name"]]
		if !validDatabaseName(name) {
			return nil, fmt.Errorf("metrics.csv row %d database_name is empty, oversized, or contains control characters", row)
		}
		if database == "" {
			database = name
		} else if name != database {
			return nil, fmt.Errorf("metrics.csv database identity changed at row %d", row)
		}

		parsed := sample{at: at, database: name, gauges: make(map[string]uint64, len(gaugeNames)), counters: make(map[string]*big.Int, len(databaseCounterNames)+len(walCounterNames))}
		for _, gauge := range gaugeNames {
			value, parseErr := parseGauge(record[indexes[gauge]])
			if parseErr != nil {
				return nil, fmt.Errorf("metrics.csv row %d gauge %s: %w", row, gauge, parseErr)
			}
			parsed.gauges[gauge] = value
		}
		for _, counter := range appendCounterNames(nil) {
			value, parseErr := parseCounter(record[indexes[counter]])
			if parseErr != nil {
				return nil, fmt.Errorf("metrics.csv row %d counter %s: %w", row, counter, parseErr)
			}
			parsed.counters[counter] = value
		}
		if !walLSN.MatchString(record[indexes["current_wal_lsn"]]) {
			return nil, fmt.Errorf("metrics.csv row %d current_wal_lsn is not canonical PostgreSQL LSN text", row)
		}
		samples = append(samples, parsed)
	}
	if len(samples) < 2 {
		return nil, fmt.Errorf("metrics.csv requires at least two samples")
	}
	return samples, nil
}

func coverageIndexes(samples []sample, started, finished time.Time) (int, int) {
	startIndex, finishIndex := -1, -1
	for index, item := range samples {
		if !item.at.After(started) {
			startIndex = index
		}
		if finishIndex < 0 && !item.at.Before(finished) {
			finishIndex = index
		}
	}
	return startIndex, finishIndex
}

func summarizeCounters(samples []sample, resetAt map[string]time.Time) ([]CounterDelta, error) {
	result := make([]CounterDelta, 0, len(databaseCounterNames)+len(walCounterNames))
	for _, scope := range []struct {
		name     string
		counters []string
	}{
		{"pg_stat_database", databaseCounterNames},
		{"pg_stat_wal", walCounterNames},
	} {
		for _, name := range scope.counters {
			delta := new(big.Int)
			segments := 1
			resetApplied := false
			for index := 1; index < len(samples); index++ {
				previous := samples[index-1].counters[name]
				current := samples[index].counters[name]
				boundary, hasBoundary := resetAt[scope.name]
				crossesReset := hasBoundary && samples[index-1].at.Before(boundary) && !samples[index].at.Before(boundary)
				if crossesReset {
					if resetApplied {
						return nil, fmt.Errorf("metrics.csv selected samples cross the same %s reset boundary more than once", scope.name)
					}
					// The pre-reset value accumulated after the preceding sample is not
					// observable. Retain only proven segments: all preceding observed
					// increments plus the current cumulative value after reset.
					delta.Add(delta, current)
					segments++
					resetApplied = true
					continue
				}
				if current.Cmp(previous) < 0 {
					return nil, fmt.Errorf("metrics.csv cumulative counter %s decreased inside measure coverage between selected samples %d and %d without one matching proven %s reset boundary", name, index, index+1, scope.name)
				}
				delta.Add(delta, new(big.Int).Sub(new(big.Int).Set(current), previous))
			}
			first := samples[0].counters[name]
			last := samples[len(samples)-1].counters[name]
			result = append(result, CounterDelta{
				Scope: scope.name, Name: name, First: first.String(), Last: last.String(), Delta: delta.String(),
				Segments: segments, ResetApplied: resetApplied,
			})
		}
	}
	return result, nil
}

func summarizeGauges(samples []sample) ([]GaugeSummary, error) {
	result := make([]GaugeSummary, 0, len(gaugeNames))
	for _, name := range gaugeNames {
		var sum, maximum uint64
		for _, item := range samples {
			value := item.gauges[name]
			if ^uint64(0)-sum < value {
				return nil, fmt.Errorf("metrics.csv gauge %s sum overflows uint64", name)
			}
			sum += value
			if value > maximum {
				maximum = value
			}
		}
		result = append(result, GaugeSummary{Name: name, Mean: float64(sum) / float64(len(samples)), Max: maximum})
	}
	return result, nil
}

func normalizeResetEvidence(options DeriveOptions, contractVersion string) (StatisticsResetEvidence, map[string]time.Time, error) {
	policy := options.StatisticsResetPolicy
	boundary := options.StatisticsResetBoundary
	if policy == "" {
		policy = benchmarkcontrol.StatisticsPolicyNone
	}
	if boundary == "" {
		boundary = "none"
	}
	normalized := StatisticsResetEvidence{
		Policy: policy, Boundary: boundary, VerificationMode: "declaration-only",
		Status:   benchmarkcontrol.StatisticsStatusNotRequested,
		Database: ResetScope{Scope: "pg_stat_database", BeforeAvailability: benchmarkcontrol.ObservationUnavailable},
		WAL:      ResetScope{Scope: "pg_stat_wal", BeforeAvailability: benchmarkcontrol.ObservationUnavailable},
	}
	if contractVersion == "1" {
		if policy != benchmarkcontrol.StatisticsPolicyNone && policy != "operator-managed" {
			return StatisticsResetEvidence{}, nil, fmt.Errorf("unsupported protocol-v1 statistics reset policy %q", policy)
		}
		if (policy == benchmarkcontrol.StatisticsPolicyNone) != (boundary == "none") {
			return StatisticsResetEvidence{}, nil, fmt.Errorf("statistics reset policy %q is inconsistent with boundary %q", policy, boundary)
		}
		if policy == "operator-managed" {
			normalized.Status = "operator-managed-unproven"
		}
		return normalized, map[string]time.Time{}, nil
	}
	if options.ControlBinding == nil || options.StatisticsReset == nil || len(options.StatisticsResetSource) == 0 {
		return StatisticsResetEvidence{}, nil, fmt.Errorf("protocol v2 metrics require bound typed statistics-reset evidence and raw source")
	}
	artifact := *options.StatisticsReset
	if err := benchmarkcontrol.VerifyStatisticsResetWithSource(artifact, options.StatisticsResetSource); err != nil {
		return StatisticsResetEvidence{}, nil, fmt.Errorf("statistics-reset evidence: %w", err)
	}
	if err := benchmarkcontrol.VerifyBinding(benchmarkcontrol.StatisticsResetBinding(artifact), *options.ControlBinding); err != nil {
		return StatisticsResetEvidence{}, nil, fmt.Errorf("statistics-reset binding: %w", err)
	}
	if artifact.PostgresServerMajor != options.PostgresServerMajor || artifact.Policy != policy || artifact.Boundary != boundary || !benchmarkcontrol.StatisticsResetSatisfied(artifact) {
		return StatisticsResetEvidence{}, nil, fmt.Errorf("statistics-reset evidence does not match the metrics protocol or is unsatisfied")
	}
	normalized.VerificationMode = "typed-control-and-raw-source"
	normalized.Status = artifact.Status
	normalized.ControlDigest = artifact.Digest
	normalized.RawSource = controlSource(artifact.RawSource)
	boundaries := map[string]time.Time{}
	if policy == benchmarkcontrol.StatisticsPolicyRunnerManaged {
		databaseReset, databaseErr := resetObservationTime(artifact.DatabaseAfter)
		walReset, walErr := resetObservationTime(artifact.WALAfter)
		if databaseErr != nil || walErr != nil {
			return StatisticsResetEvidence{}, nil, fmt.Errorf("satisfied runner-managed statistics reset lacks canonical scope timestamps")
		}
		normalized.Database.ResetAt = canonicalTime(databaseReset)
		normalized.WAL.ResetAt = canonicalTime(walReset)
		normalized.Database.BeforeAvailability = artifact.DatabaseBefore.Availability
		normalized.WAL.BeforeAvailability = artifact.WALBefore.Availability
		if artifact.DatabaseBefore.Availability == benchmarkcontrol.ObservationAvailable {
			normalized.Database.BeforeResetAt = artifact.DatabaseBefore.Value
		}
		if artifact.WALBefore.Availability == benchmarkcontrol.ObservationAvailable {
			normalized.WAL.BeforeResetAt = artifact.WALBefore.Value
		}
		boundaries[normalized.Database.Scope] = databaseReset
		boundaries[normalized.WAL.Scope] = walReset
	} else if policy != benchmarkcontrol.StatisticsPolicyNone || boundary != "none" {
		return StatisticsResetEvidence{}, nil, fmt.Errorf("protocol v2 statistics reset policy %q is inconsistent with boundary %q", policy, boundary)
	}
	return normalized, boundaries, nil
}

func normalizeCadence(options DeriveOptions, contractVersion string, all, selected []sample) (CollectorCadence, error) {
	interval := time.Duration(options.CollectorIntervalSeconds) * time.Second
	maximum := maxSampleGap(selected)
	allowed := time.Duration(CadenceGapToleranceIntervals) * interval
	result := CollectorCadence{
		Collector:          "postgresql-sampler-v" + contractVersion,
		DeclaredIntervalNS: interval.Nanoseconds(), MaxConsecutiveGapNS: maximum.Nanoseconds(),
		MaxAllowedGapNS: allowed.Nanoseconds(), VerificationMode: "wall-clock-explicit-two-interval-bound",
	}
	if maximum > allowed {
		return CollectorCadence{}, fmt.Errorf("metrics.csv maximum consecutive sample gap %s exceeds explicit cadence bound %s (two declared intervals)", maximum, allowed)
	}
	if contractVersion == "1" {
		return result, nil
	}
	if options.ControlBinding == nil || options.CollectorOverhead == nil || len(options.CollectorOverheadSource) == 0 {
		return CollectorCadence{}, fmt.Errorf("protocol v2 metrics require bound typed collector timing evidence and raw source")
	}
	artifact := *options.CollectorOverhead
	if err := benchmarkcontrol.VerifyCollectorOverheadWithSource(artifact, options.CollectorOverheadSource); err != nil {
		return CollectorCadence{}, fmt.Errorf("collector timing evidence: %w", err)
	}
	if err := benchmarkcontrol.VerifyBinding(benchmarkcontrol.CollectorOverheadBinding(artifact), *options.ControlBinding); err != nil {
		return CollectorCadence{}, fmt.Errorf("collector timing binding: %w", err)
	}
	if artifact.IntervalNS != interval.Nanoseconds() || !benchmarkcontrol.CollectorOverheadSatisfied(artifact) {
		return CollectorCadence{}, fmt.Errorf("collector timing evidence interval does not match the metrics protocol or is unsatisfied")
	}
	result.ControlDigest = artifact.Digest
	result.RawSource = controlSource(artifact.RawSource)
	if artifact.Mode == benchmarkcontrol.OverheadModeIncludedUnquantified {
		result.VerificationMode = "typed-unquantified-control-plus-wall-clock-explicit-two-interval-bound"
		return result, nil
	}
	matched, boundary, err := correlateTiming(all, artifact.Samples)
	if err != nil {
		return CollectorCadence{}, err
	}
	result.VerificationMode = "typed-monotonic-regular-grid-plus-wall-clock-explicit-two-interval-bound"
	result.RegularSamplesMatched = matched
	result.BoundarySamples = boundary
	return result, nil
}

func correlateTiming(samples []sample, timing []benchmarkcontrol.OverheadSample) (int, int, error) {
	if len(timing) == 0 || len(samples) < len(timing) || len(samples)-len(timing) > 1 {
		return 0, 0, fmt.Errorf("metrics.csv rows do not match the calibrated collector timing inventory")
	}
	for index, row := range timing {
		started, startErr := parseUTC(row.StartedAt)
		finished, finishErr := parseUTC(row.FinishedAt)
		if startErr != nil || finishErr != nil || samples[index].at.Before(started) || samples[index].at.After(finished) {
			return 0, 0, fmt.Errorf("metrics.csv sample %d cannot be correlated with its exact collector timing row", index+1)
		}
	}
	boundary := len(samples) - len(timing)
	if boundary == 1 && !samples[len(samples)-1].at.After(samples[len(timing)-1].at) {
		return 0, 0, fmt.Errorf("terminal boundary metrics sample is not ordered after regular timing rows")
	}
	return len(timing), boundary, nil
}

func maxSampleGap(samples []sample) time.Duration {
	var maximum time.Duration
	for index := 1; index < len(samples); index++ {
		if gap := samples[index].at.Sub(samples[index-1].at); gap > maximum {
			maximum = gap
		}
	}
	return maximum
}

func resetObservationTime(observation benchmarkcontrol.ResetTimestampObservation) (time.Time, error) {
	if observation.Availability != benchmarkcontrol.ObservationAvailable {
		return time.Time{}, fmt.Errorf("reset timestamp was not observed")
	}
	return parseUTC(observation.Value)
}

func controlSource(source benchmarkcontrol.SourceEvidence) *SourceEvidence {
	return &SourceEvidence{Path: source.Path, Digest: source.Digest, SizeBytes: source.SizeBytes}
}

func appendCounterNames(destination []string) []string {
	destination = append(destination, databaseCounterNames...)
	return append(destination, walCounterNames...)
}

func parseGauge(value string) (uint64, error) {
	if !decimalInteger.MatchString(value) {
		return 0, fmt.Errorf("must be a canonical non-negative integer")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("is outside uint64 range")
	}
	return parsed, nil
}

func parseCounter(value string) (*big.Int, error) {
	if !decimalInteger.MatchString(value) {
		return nil, fmt.Errorf("must be a canonical non-negative integer")
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, fmt.Errorf("cannot be parsed as an integer")
	}
	return parsed, nil
}

func parseUTC(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !strings.HasSuffix(value, "Z") {
		return time.Time{}, fmt.Errorf("must be UTC RFC3339Nano text")
	}
	return parsed.UTC(), nil
}

func canonicalTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func validDatabaseName(value string) bool {
	return value != "" && len(value) <= 63 && utf8.ValidString(value) && strings.IndexFunc(value, func(character rune) bool {
		return character < 0x20 || character == 0x7f
	}) < 0
}

func supportedMajor(value string) bool {
	return value == "15" || value == "16" || value == "17" || value == "18" || value == "19"
}

// Header returns an isolated copy of the stable PostgreSQL sampler CSV header.
// V1 and v2 deliberately share the raw row shape; v2 adds typed control inputs
// to normalization. Mutating the result cannot change parser behavior.
func Header() []string { return slices.Clone(expectedHeader) }

func summaryDigest(summary Summary) (string, error) {
	summary.Digest = ""
	content, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

// VerifyDigest checks the normalized artifact's self-digest without making any
// claim about the source file. Consumers that have metrics.csv must rederive.
func VerifyDigest(summary Summary) error {
	if summary.SchemaVersion != SchemaVersion || summary.ArtifactType != ArtifactType || summary.ParserVersion != ParserVersion {
		return fmt.Errorf("unsupported PostgreSQL sampler summary contract")
	}
	if !evidence.IsDigest(summary.Digest) {
		return fmt.Errorf("PostgreSQL sampler summary digest is invalid")
	}
	digest, err := summaryDigest(summary)
	if err != nil {
		return err
	}
	if digest != summary.Digest {
		return fmt.Errorf("PostgreSQL sampler summary digest mismatch")
	}
	if !evidence.IsPortablePath(filepath.ToSlash(summary.Source.Path)) || summary.Source.Path != SourcePath || !evidence.IsDigest(summary.Source.Digest) || summary.Source.SizeBytes <= 0 {
		return fmt.Errorf("PostgreSQL sampler summary source evidence is invalid")
	}
	if summary.Cadence.Collector != "postgresql-sampler-v1" && summary.Cadence.Collector != "postgresql-sampler-v2" {
		return fmt.Errorf("PostgreSQL sampler cadence collector is invalid")
	}
	if summary.Cadence.DeclaredIntervalNS <= 0 || summary.Cadence.MaxConsecutiveGapNS <= 0 || summary.Cadence.MaxAllowedGapNS != CadenceGapToleranceIntervals*summary.Cadence.DeclaredIntervalNS || summary.Cadence.MaxConsecutiveGapNS > summary.Cadence.MaxAllowedGapNS {
		return fmt.Errorf("PostgreSQL sampler cadence bounds are invalid")
	}
	if summary.Cadence.VerificationMode == "" || summary.Cadence.RegularSamplesMatched < 0 || summary.Cadence.BoundarySamples < 0 || summary.Cadence.BoundarySamples > 1 {
		return fmt.Errorf("PostgreSQL sampler cadence verification is invalid")
	}
	if summary.Cadence.Collector == "postgresql-sampler-v2" {
		if !evidence.IsDigest(summary.Cadence.ControlDigest) || summary.Cadence.RawSource == nil || !validControlSource(*summary.Cadence.RawSource, benchmarkcontrol.CollectorOverheadSourceFile) {
			return fmt.Errorf("PostgreSQL sampler cadence control evidence is invalid")
		}
	} else if summary.Cadence.ControlDigest != "" || summary.Cadence.RawSource != nil || summary.Cadence.RegularSamplesMatched != 0 || summary.Cadence.BoundarySamples != 0 {
		return fmt.Errorf("protocol-v1 sampler cadence claims protocol-v2 control evidence")
	}
	if summary.StatisticsReset.Database.Scope != "pg_stat_database" || summary.StatisticsReset.WAL.Scope != "pg_stat_wal" || summary.StatisticsReset.Policy == "" || summary.StatisticsReset.Boundary == "" || summary.StatisticsReset.VerificationMode == "" || summary.StatisticsReset.Status == "" {
		return fmt.Errorf("PostgreSQL sampler statistics-reset evidence is invalid")
	}
	if summary.Cadence.Collector == "postgresql-sampler-v2" {
		if !evidence.IsDigest(summary.StatisticsReset.ControlDigest) || summary.StatisticsReset.RawSource == nil || !validControlSource(*summary.StatisticsReset.RawSource, benchmarkcontrol.StatisticsResetSourceFile) {
			return fmt.Errorf("PostgreSQL sampler statistics-reset control evidence is invalid")
		}
	} else if summary.StatisticsReset.ControlDigest != "" || summary.StatisticsReset.RawSource != nil {
		return fmt.Errorf("protocol-v1 sampler statistics reset claims protocol-v2 control evidence")
	}
	switch summary.StatisticsReset.Policy {
	case benchmarkcontrol.StatisticsPolicyNone:
		if summary.StatisticsReset.Boundary != "none" || summary.StatisticsReset.Status != benchmarkcontrol.StatisticsStatusNotRequested || !unavailableSummaryReset(summary.StatisticsReset.Database) || !unavailableSummaryReset(summary.StatisticsReset.WAL) {
			return fmt.Errorf("PostgreSQL sampler no-reset evidence is inconsistent")
		}
	case "operator-managed":
		if summary.Cadence.Collector != "postgresql-sampler-v1" || summary.StatisticsReset.Boundary == "none" || summary.StatisticsReset.Status != "operator-managed-unproven" || !unavailableSummaryReset(summary.StatisticsReset.Database) || !unavailableSummaryReset(summary.StatisticsReset.WAL) {
			return fmt.Errorf("PostgreSQL sampler operator-managed reset evidence is inconsistent")
		}
	case benchmarkcontrol.StatisticsPolicyRunnerManaged:
		if summary.Cadence.Collector != "postgresql-sampler-v2" || summary.StatisticsReset.Boundary == "none" || summary.StatisticsReset.Status != benchmarkcontrol.StatisticsStatusSucceeded || !validSummaryReset(summary.StatisticsReset.Database) || !validSummaryReset(summary.StatisticsReset.WAL) {
			return fmt.Errorf("PostgreSQL sampler runner-managed reset evidence is inconsistent")
		}
	default:
		return fmt.Errorf("PostgreSQL sampler statistics-reset policy is invalid")
	}
	for _, counter := range summary.CounterDeltas {
		if counter.Segments != 1 && counter.Segments != 2 || counter.ResetApplied != (counter.Segments == 2) {
			return fmt.Errorf("PostgreSQL sampler counter segmentation is invalid")
		}
		if counter.ResetApplied && ((counter.Scope == "pg_stat_database" && summary.StatisticsReset.Database.ResetAt == "") || (counter.Scope == "pg_stat_wal" && summary.StatisticsReset.WAL.ResetAt == "")) {
			return fmt.Errorf("PostgreSQL sampler counter segment lacks its matching reset timestamp")
		}
	}
	return nil
}

func validControlSource(source SourceEvidence, expectedPath string) bool {
	return source.Path == expectedPath && evidence.IsDigest(source.Digest) && source.SizeBytes > 0
}

func canonicalResetTime(value string) bool {
	parsed, err := parseUTC(value)
	return err == nil && canonicalTime(parsed) == value
}

func unavailableSummaryReset(scope ResetScope) bool {
	return scope.BeforeAvailability == benchmarkcontrol.ObservationUnavailable && scope.BeforeResetAt == "" && scope.ResetAt == ""
}

func validSummaryReset(scope ResetScope) bool {
	if !canonicalResetTime(scope.ResetAt) {
		return false
	}
	switch scope.BeforeAvailability {
	case benchmarkcontrol.ObservationNull:
		return scope.BeforeResetAt == ""
	case benchmarkcontrol.ObservationAvailable:
		if !canonicalResetTime(scope.BeforeResetAt) {
			return false
		}
		before, _ := parseUTC(scope.BeforeResetAt)
		after, _ := parseUTC(scope.ResetAt)
		return after.After(before)
	default:
		return false
	}
}
