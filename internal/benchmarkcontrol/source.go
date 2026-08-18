package benchmarkcontrol

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

var (
	cacheSourceHeader    = []string{"relation", "database_oid", "relation_oid", "fork", "relation_blocks", "resident_blocks"}
	resetSourceHeader    = []string{"record", "scope", "value", "rows", "command_completed"}
	overheadSourceHeader = []string{"sequence", "scheduled_at", "started_at", "finished_at", "duration_ns", "status"}
)

func NewSourceEvidence(path string, content []byte) (SourceEvidence, error) {
	if path == "" || filepath.Base(path) != path || strings.ContainsAny(path, `/\:`) {
		return SourceEvidence{}, fmt.Errorf("raw source path must be one portable file name")
	}
	if len(content) == 0 || len(content) > maxArtifactBytes {
		return SourceEvidence{}, fmt.Errorf("raw source size must be between 1 and %d bytes", maxArtifactBytes)
	}
	return SourceEvidence{Path: path, Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content))}, nil
}

func NewCacheStateFromSource(input CacheStateInput, content []byte) (CacheState, error) {
	relations, err := ParseCacheStateSource(content)
	if err != nil {
		return CacheState{}, err
	}
	input.Relations = relations
	input.RawSource, err = NewSourceEvidence(CacheStateSourceFile, content)
	if err != nil {
		return CacheState{}, err
	}
	return NewCacheState(input)
}

func NewStatisticsResetFromSource(input StatisticsResetInput, content []byte) (StatisticsReset, error) {
	databaseBefore, databaseAfter, walBefore, walAfter, operations, err := ParseStatisticsResetSource(content)
	if err != nil {
		return StatisticsReset{}, err
	}
	input.DatabaseBefore, input.DatabaseAfter, input.WALBefore, input.WALAfter = databaseBefore, databaseAfter, walBefore, walAfter
	input.Operations = operations
	input.RawSource, err = NewSourceEvidence(StatisticsResetSourceFile, content)
	if err != nil {
		return StatisticsReset{}, err
	}
	return NewStatisticsReset(input)
}

func NewCollectorOverheadFromSource(input CollectorOverheadInput, content []byte) (CollectorOverhead, error) {
	samples, err := ParseCollectorOverheadSource(content)
	if err != nil {
		return CollectorOverhead{}, err
	}
	input.Samples = samples
	input.RawSource, err = NewSourceEvidence(CollectorOverheadSourceFile, content)
	if err != nil {
		return CollectorOverhead{}, err
	}
	return NewCollectorOverhead(input)
}

func NewResourceBudgetFromSource(input ResourceBudgetInput, content []byte) (ResourceBudget, error) {
	source, err := ParseResourceBudgetSource(content)
	if err != nil {
		return ResourceBudget{}, err
	}
	if source.Mode != input.Mode {
		return ResourceBudget{}, fmt.Errorf("resource raw source mode does not match requested mode")
	}
	input.ObservedDockerNanoCPUs = source.ObservedDockerNanoCPUs
	input.ObservedDockerMemoryBytes = source.ObservedDockerMemoryBytes
	input.CgroupVersion = source.CgroupVersion
	input.PostgresContainerIDDigest = source.PostgresContainerIDDigest
	input.PgbenchContainerIDDigest = source.PgbenchContainerIDDigest
	input.RawSource, err = NewSourceEvidence(ResourceBudgetSourceFile, content)
	if err != nil {
		return ResourceBudget{}, err
	}
	return NewResourceBudget(input)
}

func ParseCacheStateSource(content []byte) ([]CacheRelationObservation, error) {
	records, err := parseTSV(content, cacheSourceHeader)
	if err != nil {
		return nil, fmt.Errorf("cache state source: %w", err)
	}
	relations := make([]CacheRelationObservation, 0, len(records))
	for row, record := range records {
		databaseOID, err := parseCanonicalUint(record[1], 32)
		if err != nil || databaseOID == 0 {
			return nil, fmt.Errorf("cache state source row %d database_oid is not a positive canonical uint32", row+2)
		}
		relationOID, err := parseCanonicalUint(record[2], 32)
		if err != nil || relationOID == 0 {
			return nil, fmt.Errorf("cache state source row %d relation_oid is not a positive canonical uint32", row+2)
		}
		relationBlocks, err := parseCanonicalUint(record[4], 64)
		if err != nil || relationBlocks == 0 {
			return nil, fmt.Errorf("cache state source row %d relation_blocks is not a positive canonical uint64", row+2)
		}
		residentBlocks, err := parseCanonicalUint(record[5], 64)
		if err != nil || residentBlocks > relationBlocks {
			return nil, fmt.Errorf("cache state source row %d resident_blocks is invalid", row+2)
		}
		if !qualifiedIdentifier(record[0]) || record[3] != "main" {
			return nil, fmt.Errorf("cache state source row %d relation or fork is unsupported", row+2)
		}
		relations = append(relations, CacheRelationObservation{
			Relation: record[0], DatabaseOID: uint32(databaseOID), RelationOID: uint32(relationOID), Fork: record[3],
			RelationBlocks: relationBlocks, ResidentBlocks: residentBlocks, ResidentPct: percent(residentBlocks, relationBlocks),
		})
	}
	return relations, nil
}

func ParseStatisticsResetSource(content []byte) (ResetTimestampObservation, ResetTimestampObservation, ResetTimestampObservation, ResetTimestampObservation, []ResetOperation, error) {
	records, err := parseTSV(content, resetSourceHeader)
	if err != nil {
		return ResetTimestampObservation{}, ResetTimestampObservation{}, ResetTimestampObservation{}, ResetTimestampObservation{}, nil, fmt.Errorf("statistics reset source: %w", err)
	}
	unavailable := ResetTimestampObservation{Availability: ObservationUnavailable}
	if len(records) == 0 {
		return unavailable, unavailable, unavailable, unavailable, []ResetOperation{}, nil
	}
	if len(records) != 6 {
		return unavailable, unavailable, unavailable, unavailable, nil, fmt.Errorf("statistics reset source must contain zero or six ordered rows")
	}
	wantKinds := []string{"timestamp-before", "timestamp-after", "timestamp-before", "timestamp-after", "operation", "operation"}
	wantScopes := []string{"current-database", "current-database", "cluster-wal", "cluster-wal", "current-database", "cluster-wal"}
	for index, record := range records {
		if record[0] != wantKinds[index] || record[1] != wantScopes[index] {
			return unavailable, unavailable, unavailable, unavailable, nil, fmt.Errorf("statistics reset source row %d has unexpected kind or scope", index+2)
		}
	}
	observations := make([]ResetTimestampObservation, 4)
	for index := range observations {
		record := records[index]
		if record[3] != "" || record[4] != "" {
			return unavailable, unavailable, unavailable, unavailable, nil, fmt.Errorf("statistics reset timestamp row %d has operation fields", index+2)
		}
		if record[2] == "unavailable" {
			observations[index] = ResetTimestampObservation{Availability: ObservationUnavailable}
		} else if record[2] == "null" {
			observations[index] = ResetTimestampObservation{Availability: ObservationNull}
		} else if canonicalUTC(record[2]) {
			observations[index] = ResetTimestampObservation{Availability: ObservationAvailable, Value: record[2]}
		} else {
			return unavailable, unavailable, unavailable, unavailable, nil, fmt.Errorf("statistics reset timestamp row %d is not unavailable, null, or canonical UTC", index+2)
		}
	}
	operations := make([]ResetOperation, 0, 2)
	for index := 4; index < 6; index++ {
		record := records[index]
		rows, err := strconv.Atoi(record[3])
		if err != nil || strconv.Itoa(rows) != record[3] || rows < 0 || rows > 1 {
			return unavailable, unavailable, unavailable, unavailable, nil, fmt.Errorf("statistics reset operation row %d has invalid rows", index+2)
		}
		if record[4] != "true" && record[4] != "false" {
			return unavailable, unavailable, unavailable, unavailable, nil, fmt.Errorf("statistics reset operation row %d has invalid command_completed", index+2)
		}
		operations = append(operations, ResetOperation{Function: record[2], Scope: record[1], Rows: rows, CommandCompleted: record[4] == "true"})
	}
	return observations[0], observations[1], observations[2], observations[3], operations, nil
}

func ParseCollectorOverheadSource(content []byte) ([]OverheadSample, error) {
	records, err := parseTSV(content, overheadSourceHeader)
	if err != nil {
		return nil, fmt.Errorf("collector overhead source: %w", err)
	}
	samples := make([]OverheadSample, 0, len(records))
	for row, record := range records {
		sequence, err := strconv.Atoi(record[0])
		if err != nil || strconv.Itoa(sequence) != record[0] || sequence <= 0 {
			return nil, fmt.Errorf("collector overhead source row %d has invalid sequence", row+2)
		}
		if !canonicalUTC(record[1]) || !canonicalUTC(record[2]) || !canonicalUTC(record[3]) || record[4] == "" || !oneOf(record[5], "succeeded", "failed") {
			return nil, fmt.Errorf("collector overhead source row %d has invalid timestamps or status", row+2)
		}
		duration, err := strconv.ParseInt(record[4], 10, 64)
		if err != nil || strconv.FormatInt(duration, 10) != record[4] || duration < 0 {
			return nil, fmt.Errorf("collector overhead source row %d has invalid duration_ns", row+2)
		}
		samples = append(samples, OverheadSample{Sequence: sequence, ScheduledAt: record[1], StartedAt: record[2], FinishedAt: record[3], DurationNS: duration, Status: record[5]})
	}
	return samples, nil
}

func ParseResourceBudgetSource(content []byte) (ResourceBudgetSource, error) {
	source, err := parseStrict[ResourceBudgetSource](content)
	if err != nil {
		return ResourceBudgetSource{}, fmt.Errorf("resource budget source: %w", err)
	}
	if source.Mode == ResourceModeUnbounded {
		if source.ObservedDockerNanoCPUs != nil || source.ObservedDockerMemoryBytes != nil || source.CgroupVersion != "" || source.PostgresContainerIDDigest != "" || source.PgbenchContainerIDDigest != "" {
			return ResourceBudgetSource{}, fmt.Errorf("unbounded resource source must omit observations")
		}
		return source, nil
	}
	if source.Mode != ResourceModeRunnerEnforced || source.ObservedDockerNanoCPUs == nil || *source.ObservedDockerNanoCPUs < 0 || source.ObservedDockerMemoryBytes == nil || *source.ObservedDockerMemoryBytes < 0 || source.CgroupVersion == "" || !evidence.IsDigest(source.PostgresContainerIDDigest) || !evidence.IsDigest(source.PgbenchContainerIDDigest) {
		return ResourceBudgetSource{}, fmt.Errorf("runner-enforced resource source is incomplete or malformed")
	}
	return source, nil
}

func MarshalResourceBudgetSource(source ResourceBudgetSource) ([]byte, error) {
	content, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return nil, err
	}
	content = append(content, '\n')
	if _, err := ParseResourceBudgetSource(content); err != nil {
		return nil, err
	}
	return content, nil
}

func VerifyCacheStateWithSource(artifact CacheState, content []byte) error {
	if err := VerifyCacheState(artifact); err != nil {
		return err
	}
	if err := verifySourceBytes(artifact.RawSource, CacheStateSourceFile, content); err != nil {
		return err
	}
	relations, err := ParseCacheStateSource(content)
	if err != nil {
		return err
	}
	if !slices.Equal(relations, artifact.Relations) {
		return fmt.Errorf("cache normalized relations do not match raw source")
	}
	return nil
}

func VerifyStatisticsResetWithSource(artifact StatisticsReset, content []byte) error {
	if err := VerifyStatisticsReset(artifact); err != nil {
		return err
	}
	if err := verifySourceBytes(artifact.RawSource, StatisticsResetSourceFile, content); err != nil {
		return err
	}
	dbBefore, dbAfter, walBefore, walAfter, operations, err := ParseStatisticsResetSource(content)
	if err != nil {
		return err
	}
	if dbBefore != artifact.DatabaseBefore || dbAfter != artifact.DatabaseAfter || walBefore != artifact.WALBefore || walAfter != artifact.WALAfter || !slices.Equal(operations, artifact.Operations) {
		return fmt.Errorf("statistics reset normalized observations do not match raw source")
	}
	return nil
}

func VerifyCollectorOverheadWithSource(artifact CollectorOverhead, content []byte) error {
	if err := VerifyCollectorOverhead(artifact); err != nil {
		return err
	}
	if err := verifySourceBytes(artifact.RawSource, CollectorOverheadSourceFile, content); err != nil {
		return err
	}
	samples, err := ParseCollectorOverheadSource(content)
	if err != nil {
		return err
	}
	if !slices.Equal(samples, artifact.Samples) {
		return fmt.Errorf("collector overhead normalized samples do not match raw source")
	}
	return nil
}

func VerifyResourceBudgetWithSource(artifact ResourceBudget, content []byte) error {
	if err := VerifyResourceBudget(artifact); err != nil {
		return err
	}
	if err := verifySourceBytes(artifact.RawSource, ResourceBudgetSourceFile, content); err != nil {
		return err
	}
	source, err := ParseResourceBudgetSource(content)
	if err != nil {
		return err
	}
	if source.Mode != artifact.Mode || !equalInt64(source.ObservedDockerNanoCPUs, artifact.ObservedDockerNanoCPUs) || !equalInt64(source.ObservedDockerMemoryBytes, artifact.ObservedDockerMemoryBytes) || source.CgroupVersion != artifact.CgroupVersion || source.PostgresContainerIDDigest != artifact.PostgresContainerIDDigest || source.PgbenchContainerIDDigest != artifact.PgbenchContainerIDDigest {
		return fmt.Errorf("resource normalized observations do not match raw source")
	}
	return nil
}

func VerifyCacheStateFile(path string) error {
	artifact, source, err := readArtifactAndSource(path, CacheStateSourceFile, ParseCacheState)
	if err != nil {
		return err
	}
	return VerifyCacheStateWithSource(artifact, source)
}
func VerifyStatisticsResetFile(path string) error {
	artifact, source, err := readArtifactAndSource(path, StatisticsResetSourceFile, ParseStatisticsReset)
	if err != nil {
		return err
	}
	return VerifyStatisticsResetWithSource(artifact, source)
}
func VerifyCollectorOverheadFile(path string) error {
	artifact, source, err := readArtifactAndSource(path, CollectorOverheadSourceFile, ParseCollectorOverhead)
	if err != nil {
		return err
	}
	return VerifyCollectorOverheadWithSource(artifact, source)
}
func VerifyResourceBudgetFile(path string) error {
	artifact, source, err := readArtifactAndSource(path, ResourceBudgetSourceFile, ParseResourceBudget)
	if err != nil {
		return err
	}
	return VerifyResourceBudgetWithSource(artifact, source)
}

func WriteRawSource(path string, content []byte) error {
	if len(content) == 0 || len(content) > maxArtifactBytes {
		return fmt.Errorf("raw source size must be between 1 and %d bytes", maxArtifactBytes)
	}
	return writeBytesNoReplace(path, content, ".benchmark-control-source-*.tmp")
}

func validateSourceEvidence(source SourceEvidence, expectedPath string) []string {
	issues := []string{}
	if source.Path != expectedPath {
		issues = append(issues, fmt.Sprintf("raw_source.path = %q, want %q", source.Path, expectedPath))
	}
	if !evidence.IsDigest(source.Digest) {
		issues = append(issues, "raw_source.digest is not a lowercase sha256 digest")
	}
	if source.SizeBytes <= 0 || source.SizeBytes > maxArtifactBytes {
		issues = append(issues, "raw_source.size_bytes is out of bounds")
	}
	return issues
}

func verifySourceBytes(source SourceEvidence, expectedPath string, content []byte) error {
	if issues := validateSourceEvidence(source, expectedPath); len(issues) > 0 {
		return fmt.Errorf("invalid raw source reference: %s", strings.Join(issues, "; "))
	}
	if int64(len(content)) != source.SizeBytes {
		return fmt.Errorf("raw source size mismatch")
	}
	if evidence.DigestBytes(content) != source.Digest {
		return fmt.Errorf("raw source digest mismatch")
	}
	return nil
}

func parseTSV(content []byte, expectedHeader []string) ([][]string, error) {
	if len(content) == 0 || len(content) > maxArtifactBytes || !utf8.Valid(content) {
		return nil, fmt.Errorf("source must be non-empty bounded UTF-8")
	}
	if content[len(content)-1] != '\n' || bytes.ContainsAny(content, "\r\x00\"") || bytes.Contains(content, []byte("\n\n")) {
		return nil, fmt.Errorf("source must use canonical unquoted LF-terminated TSV rows")
	}
	reader := csv.NewReader(bytes.NewReader(content))
	reader.Comma = '\t'
	reader.FieldsPerRecord = len(expectedHeader)
	reader.ReuseRecord = false
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	if !slices.Equal(header, expectedHeader) {
		return nil, fmt.Errorf("header does not match exact contract")
	}
	var records [][]string
	for row := 2; ; row++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		for _, field := range record {
			if strings.ContainsAny(field, "\r\n\x00") {
				return nil, fmt.Errorf("row %d contains control characters", row)
			}
		}
		records = append(records, record)
		if len(records) > maxControlRows {
			return nil, fmt.Errorf("source has too many rows")
		}
	}
	return records, nil
}

func parseCanonicalUint(value string, bits int) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, bits)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("not canonical uint")
	}
	return parsed, nil
}

func readArtifactAndSource[T any](path, sourceName string, parse func([]byte) (T, error)) (T, []byte, error) {
	var zero T
	content, err := readRegular(path)
	if err != nil {
		return zero, nil, err
	}
	artifact, err := parse(content)
	if err != nil {
		return zero, nil, err
	}
	source, err := readRegular(filepath.Join(filepath.Dir(path), sourceName))
	if err != nil {
		return zero, nil, err
	}
	return artifact, source, nil
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArtifactBytes {
		return nil, fmt.Errorf("benchmark control file must be a bounded regular non-symlink file: %s", path)
	}
	return os.ReadFile(path)
}

func writeBytesNoReplace(path string, content []byte, pattern string) error {
	if path == "" || path == "-" {
		return fmt.Errorf("output must be a file path")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("publish benchmark control source: %w", err)
	}
	return nil
}

func equalInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
