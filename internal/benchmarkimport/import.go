package benchmarkimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	maxRawBytes      = int64(64 << 20)
	maxMappingBytes  = int64(1 << 20)
	maxArtifactBytes = int64(2 << 20)
)

var (
	portableWorkloadPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,159}$`)
	portableVersionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,127}$`)
	commitPattern           = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hammerDB6VersionPattern = regexp.MustCompile(`^6(?:\.[0-9][0-9A-Za-z._+\-]*)+$`)
	metricNamePattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// Create imports one immutable source document into outputDir. It never
// executes a benchmark driver and never overwrites an existing destination.
func Create(adapter, sourcePath, outputDir string, options Options) (Artifact, error) {
	source, err := readRegularLimited(sourcePath, maxRawBytes, "benchmark source")
	if err != nil {
		return Artifact{}, err
	}

	var mapping []byte
	if options.MappingPath != "" {
		if sameFilePath(sourcePath, options.MappingPath) {
			return Artifact{}, fmt.Errorf("benchmark source and mapping must be different files")
		}
		mapping, err = readRegularLimited(options.MappingPath, maxMappingBytes, "benchmark import mapping")
		if err != nil {
			return Artifact{}, err
		}
	}

	artifact, err := derive(adapter, source, mapping, strings.TrimSpace(options.Workload))
	if err != nil {
		return Artifact{}, err
	}
	artifact.RawInput = fileEvidence(RawSourceFile, source)
	if mapping != nil {
		value := fileEvidence(MappingFile, mapping)
		artifact.MappingInput = &value
	}
	artifact.Digest, err = artifactDigest(artifact)
	if err != nil {
		return Artifact{}, err
	}

	finalDir, err := filepath.Abs(outputDir)
	if err != nil {
		return Artifact{}, fmt.Errorf("resolve output directory: %w", err)
	}
	if info, statErr := os.Lstat(finalDir); statErr == nil {
		return Artifact{}, fmt.Errorf("refusing to overwrite immutable benchmark import: %s (%s)", finalDir, info.Mode())
	} else if !os.IsNotExist(statErr) {
		return Artifact{}, fmt.Errorf("inspect output directory: %w", statErr)
	}
	parent := filepath.Dir(finalDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Artifact{}, fmt.Errorf("create output parent: %w", err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return Artifact{}, fmt.Errorf("output parent must be a non-symlink directory")
	}
	stage, err := os.MkdirTemp(parent, ".pgworkbench-import-*.tmp")
	if err != nil {
		return Artifact{}, fmt.Errorf("create import staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := os.Mkdir(filepath.Join(stage, "raw"), 0o755); err != nil {
		return Artifact{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, filepath.FromSlash(RawSourceFile)), source, 0o644); err != nil {
		return Artifact{}, fmt.Errorf("retain raw source: %w", err)
	}
	if mapping != nil {
		if err := os.WriteFile(filepath.Join(stage, filepath.FromSlash(MappingFile)), mapping, 0o644); err != nil {
			return Artifact{}, fmt.Errorf("retain mapping: %w", err)
		}
	}
	if err := writeJSON(filepath.Join(stage, ResultFile), artifact); err != nil {
		return Artifact{}, fmt.Errorf("write import result: %w", err)
	}
	verification, err := Verify(stage)
	if err != nil {
		return Artifact{}, fmt.Errorf("verify staged import: %w", err)
	}
	if !verification.IsValid() {
		return Artifact{}, fmt.Errorf("produced benchmark import is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	if err := os.Rename(stage, finalDir); err != nil {
		return Artifact{}, fmt.Errorf("publish immutable benchmark import: %w", err)
	}
	artifact.ArtifactDir = finalDir
	return artifact, nil
}

func derive(adapter string, source, mapping []byte, workload string) (Artifact, error) {
	switch adapter {
	case AdapterSysbench1:
		if mapping != nil {
			return Artifact{}, fmt.Errorf("sysbench1 import does not accept --manifest; its console summary is parsed directly")
		}
		if err := validateWorkload(workload); err != nil {
			return Artifact{}, fmt.Errorf("sysbench workload: %w", err)
		}
		parsed, err := parseSysbench(source)
		if err != nil {
			return Artifact{}, fmt.Errorf("parse sysbench 1.0 console summary: %w", err)
		}
		artifact := newArtifact(DriverSysbench, parsed.DriverVersion, workload, SysbenchSourceFormat, SysbenchParserVersion, "strict-parser")
		artifact.PrimaryMetric = parsed.Metric
		artifact.Errors = parsed.Errors
		artifact.Timing = parsed.Timing
		if err := validateNormalized(artifact); err != nil {
			return Artifact{}, err
		}
		return artifact, nil
	case AdapterBenchBase33c0047:
		if mapping != nil {
			return Artifact{}, fmt.Errorf("%s parses its pinned summary directly and does not accept --manifest", adapter)
		}
		if workload != "" {
			return Artifact{}, fmt.Errorf("%s gets workload from Benchmark Type and does not accept --workload", adapter)
		}
		return parseBenchBase33c0047(source)
	case AdapterHammerDB6Report:
		if mapping != nil {
			return Artifact{}, fmt.Errorf("%s parses its pinned job report directly and does not accept --manifest", adapter)
		}
		if workload != "" {
			return Artifact{}, fmt.Errorf("%s gets workload from job.benchmark and does not accept --workload", adapter)
		}
		return parseHammerDB6Report(source)
	case AdapterHammerDB6, AdapterBenchBase:
		if workload != "" {
			return Artifact{}, fmt.Errorf("%s import gets workload from --manifest; --workload is not accepted", adapter)
		}
		if mapping == nil {
			return Artifact{}, fmt.Errorf("%s import requires --manifest with explicit metric, error, and timing mapping", adapter)
		}
		structured, err := decodeStructuredJSON(source)
		if err != nil {
			return Artifact{}, fmt.Errorf("validate structured benchmark source: %w", err)
		}
		mapped, err := decodeMapping(mapping)
		if err != nil {
			return Artifact{}, fmt.Errorf("parse benchmark import mapping: %w", err)
		}
		if err := validateMapping(adapter, mapped); err != nil {
			return Artifact{}, err
		}
		driverVersion, err := mappedString(structured, mapped.DriverVersionPointer, "driver_version")
		if err != nil {
			return Artifact{}, err
		}
		if adapter == AdapterHammerDB6 && !hammerDB6VersionPattern.MatchString(driverVersion) {
			return Artifact{}, fmt.Errorf("mapped driver_version must identify a HammerDB 6.x release")
		}
		mappedWorkload, err := mappedString(structured, mapped.WorkloadPointer, "workload")
		if err != nil {
			return Artifact{}, err
		}
		metric, errors, timing, err := extractMappedValues(structured, mapped)
		if err != nil {
			return Artifact{}, err
		}
		artifact := newArtifact(mapped.Driver, driverVersion, mappedWorkload, mapped.SourceFormat, MappingParserVersion, "explicit-json-pointer-mapping")
		artifact.PrimaryMetric = metric
		artifact.Errors = errors
		artifact.Timing = timing
		if err := validateNormalized(artifact); err != nil {
			return Artifact{}, err
		}
		return artifact, nil
	default:
		return Artifact{}, fmt.Errorf("unsupported benchmark import adapter %q; expected hammerdb6, hammerdb6report, sysbench1, benchbase, or benchbase33c0047", adapter)
	}
}

func newArtifact(driver, version, workload, sourceFormat, parserVersion, normalizationOrigin string) Artifact {
	return Artifact{
		SchemaVersion:         SchemaVersion,
		ArtifactType:          ArtifactType,
		ContractVersion:       ContractVersion,
		Classification:        ClassificationImported,
		AnalysisDesign:        AnalysisDesignImported,
		Status:                StatusImported,
		Conclusion:            ConclusionDescriptive,
		DecisionEligible:      false,
		PGbenchSeriesEligible: false,
		Driver:                driver,
		DriverVersion:         version,
		Workload:              workload,
		SourceFormat:          sourceFormat,
		ParserVersion:         parserVersion,
		Assurance: Assurance{
			EvidenceOrigin:                "offline-import",
			NormalizationOrigin:           normalizationOrigin,
			VerificationScope:             "raw-byte-integrity-and-contract-rederivation",
			TPCComplianceClaim:            false,
			CrossSystemComparisonEligible: false,
		},
	}
}

func validateMapping(adapter string, mapping Mapping) error {
	if mapping.SchemaVersion != MappingSchemaVersion || mapping.ArtifactType != MappingArtifactType {
		return fmt.Errorf("mapping must use schema %q and artifact type %q", MappingSchemaVersion, MappingArtifactType)
	}
	switch adapter {
	case AdapterHammerDB6:
		if mapping.Driver != DriverHammerDB || mapping.SourceFormat != HammerDBSourceFormat {
			return fmt.Errorf("hammerdb6 mapping must declare driver %q and source_format %q", DriverHammerDB, HammerDBSourceFormat)
		}
	case AdapterBenchBase:
		if mapping.Driver != DriverBenchBase || mapping.SourceFormat != BenchBaseSourceFormat {
			return fmt.Errorf("benchbase mapping must declare driver %q and source_format %q", DriverBenchBase, BenchBaseSourceFormat)
		}
	default:
		return fmt.Errorf("mapping is not supported for adapter %q", adapter)
	}
	if !metricNamePattern.MatchString(mapping.PrimaryMetric.Name) || !printableToken(mapping.PrimaryMetric.Unit, 64) || mapping.PrimaryMetric.Direction != "higher" && mapping.PrimaryMetric.Direction != "lower" {
		return fmt.Errorf("mapping primary metric name, unit, or direction is invalid")
	}
	for name, pointer := range map[string]string{
		"driver_version_pointer":         mapping.DriverVersionPointer,
		"workload_pointer":               mapping.WorkloadPointer,
		"primary_metric.value_pointer":   mapping.PrimaryMetric.ValuePointer,
		"errors.total_pointer":           mapping.Errors.TotalPointer,
		"errors.messages_pointer":        mapping.Errors.MessagesPointer,
		"timing.elapsed_seconds_pointer": mapping.Timing.ElapsedSecondsPointer,
	} {
		if pointer == "" || !strings.HasPrefix(pointer, "/") {
			return fmt.Errorf("mapping %s must be a non-empty RFC 6901 JSON Pointer", name)
		}
	}
	if (mapping.Timing.StartedAtPointer == "") != (mapping.Timing.FinishedAtPointer == "") {
		return fmt.Errorf("mapping timing timestamp pointers must be both present or both absent")
	}
	return nil
}

func extractMappedValues(root any, mapping Mapping) (PrimaryMetric, ErrorSummary, Timing, error) {
	metricValue, err := mappedNumber(root, mapping.PrimaryMetric.ValuePointer, "primary_metric.value")
	if err != nil {
		return PrimaryMetric{}, ErrorSummary{}, Timing{}, err
	}
	errorTotal, err := mappedUint(root, mapping.Errors.TotalPointer, "errors.total")
	if err != nil {
		return PrimaryMetric{}, ErrorSummary{}, Timing{}, err
	}
	errorMessages, err := mappedStrings(root, mapping.Errors.MessagesPointer, "errors.messages")
	if err != nil {
		return PrimaryMetric{}, ErrorSummary{}, Timing{}, err
	}
	elapsed, err := mappedNumber(root, mapping.Timing.ElapsedSecondsPointer, "timing.elapsed_seconds")
	if err != nil {
		return PrimaryMetric{}, ErrorSummary{}, Timing{}, err
	}
	timing := Timing{Basis: "selected-from-structured-source", ElapsedSeconds: elapsed}
	if mapping.Timing.StartedAtPointer != "" {
		timing.StartedAt, err = mappedString(root, mapping.Timing.StartedAtPointer, "timing.started_at")
		if err != nil {
			return PrimaryMetric{}, ErrorSummary{}, Timing{}, err
		}
		timing.FinishedAt, err = mappedString(root, mapping.Timing.FinishedAtPointer, "timing.finished_at")
		if err != nil {
			return PrimaryMetric{}, ErrorSummary{}, Timing{}, err
		}
	}
	return PrimaryMetric{
		Name: mapping.PrimaryMetric.Name, Value: metricValue, Unit: mapping.PrimaryMetric.Unit,
		Direction: mapping.PrimaryMetric.Direction, Basis: "selected-from-structured-source",
	}, ErrorSummary{Total: errorTotal, Messages: uniqueSorted(errorMessages), Complete: false}, timing, nil
}

func mappedValue(root any, pointer, name string) (any, error) {
	value, err := jsonPointer(root, pointer)
	if err != nil {
		return nil, fmt.Errorf("mapping %s: %w", name, err)
	}
	return value, nil
}

func mappedNumber(root any, pointer, name string) (float64, error) {
	value, err := mappedValue(root, pointer, name)
	if err != nil {
		return 0, err
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("mapping %s must select a JSON number", name)
	}
	parsed, err := number.Float64()
	if err != nil || !finite(parsed) || parsed < 0 {
		return 0, fmt.Errorf("mapping %s must select a non-negative finite JSON number", name)
	}
	return parsed, nil
}

func mappedUint(root any, pointer, name string) (uint64, error) {
	value, err := mappedValue(root, pointer, name)
	if err != nil {
		return 0, err
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("mapping %s must select a JSON integer", name)
	}
	converted, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("mapping %s must select a non-negative integer", name)
	}
	return converted, nil
}

func mappedString(root any, pointer, name string) (string, error) {
	value, err := mappedValue(root, pointer, name)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok || !printableText(text, 512) {
		return "", fmt.Errorf("mapping %s must select non-empty printable JSON text", name)
	}
	return text, nil
}

func mappedStrings(root any, pointer, name string) ([]string, error) {
	value, err := mappedValue(root, pointer, name)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok || len(items) > 100 {
		return nil, fmt.Errorf("mapping %s must select a JSON string array with at most 100 items", name)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || !printableText(text, 512) {
			return nil, fmt.Errorf("mapping %s must select only non-empty printable strings", name)
		}
		result = append(result, text)
	}
	return result, nil
}

func validateNormalized(artifact Artifact) error {
	if !portableVersionPattern.MatchString(artifact.DriverVersion) {
		return fmt.Errorf("driver_version is not a portable version token")
	}
	if artifact.DriverCommit != "" && !commitPattern.MatchString(artifact.DriverCommit) {
		return fmt.Errorf("driver_commit must be a full lowercase SHA-1 object id")
	}
	if err := validateWorkload(artifact.Workload); err != nil {
		return fmt.Errorf("workload: %w", err)
	}
	metric := artifact.PrimaryMetric
	if !metricNamePattern.MatchString(metric.Name) || !printableToken(metric.Unit, 64) || (metric.Direction != "higher" && metric.Direction != "lower") || !finite(metric.Value) || metric.Value < 0 {
		return fmt.Errorf("primary metric name, value, unit, or direction is invalid")
	}
	if metric.Basis != "reported" && metric.Basis != "derived-from-reported-totals" && metric.Basis != "selected-from-structured-source" {
		return fmt.Errorf("primary metric basis is unsupported")
	}
	if artifact.Errors.Messages == nil {
		return fmt.Errorf("errors.messages must be present as an array")
	}
	if len(artifact.Errors.Messages) > 100 || !reflect.DeepEqual(artifact.Errors.Messages, uniqueSorted(artifact.Errors.Messages)) {
		return fmt.Errorf("errors.messages must contain at most 100 sorted unique messages")
	}
	for _, message := range artifact.Errors.Messages {
		if !printableText(message, 512) {
			return fmt.Errorf("error message is empty, oversized, or contains control characters")
		}
	}
	if artifact.Errors.Total < uint64(len(artifact.Errors.Messages)) {
		return fmt.Errorf("errors.total cannot be smaller than retained error message count")
	}
	if !finite(artifact.Timing.ElapsedSeconds) || artifact.Timing.ElapsedSeconds <= 0 {
		return fmt.Errorf("timing.elapsed_seconds must be positive and finite")
	}
	if artifact.Timing.Basis != "reported-elapsed" && artifact.Timing.Basis != "selected-from-structured-source" && artifact.Timing.Basis != "declared-window" && artifact.Timing.Basis != "reported-aggregate-query-time" {
		return fmt.Errorf("timing basis is unsupported")
	}
	if (artifact.Timing.StartedAt == "") != (artifact.Timing.FinishedAt == "") {
		return fmt.Errorf("timing timestamps must be both present or both absent")
	}
	if artifact.Timing.StartedAt != "" {
		started, startErr := parseCanonicalUTC(artifact.Timing.StartedAt)
		finished, finishErr := parseCanonicalUTC(artifact.Timing.FinishedAt)
		if startErr != nil || finishErr != nil || !finished.After(started) {
			return fmt.Errorf("timing timestamps must be canonical UTC RFC3339Nano with finished_at after started_at")
		}
	}
	return nil
}

func validateWorkload(value string) error {
	if !portableWorkloadPattern.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "//") {
		return fmt.Errorf("must be a portable non-empty workload identifier")
	}
	return nil
}

func parseCanonicalUTC(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !strings.HasSuffix(value, "Z") || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("not canonical UTC RFC3339Nano")
	}
	return parsed, nil
}

func printableToken(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	return strings.IndexFunc(value, func(char rune) bool {
		return char <= 0x20 || char == 0x7f
	}) < 0
}

func printableText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	return strings.IndexFunc(value, func(char rune) bool {
		return char < 0x20 || char == 0x7f
	}) < 0
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func fileEvidence(name string, content []byte) FileEvidence {
	return FileEvidence{File: name, Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content))}
}

func artifactDigest(artifact Artifact) (string, error) {
	artifact.Digest = ""
	artifact.ArtifactDir = ""
	content, err := json.Marshal(artifact)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func writeJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}

func readRegularLimited(path string, limit int64, label string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular non-symlink file", label)
	}
	if info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("%s size must be between 1 and %d bytes", label, limit)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return content, nil
}

func sameFilePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && leftAbs == rightAbs
}

func parseArtifact(content []byte) (Artifact, error) {
	if err := validateJSONDocument(content); err != nil {
		return Artifact{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var artifact Artifact
	if err := decoder.Decode(&artifact); err != nil {
		return Artifact{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Artifact{}, fmt.Errorf("multiple JSON values")
		}
		return Artifact{}, err
	}
	return artifact, nil
}
