// Package benchmarksettings parses and verifies the deliberately narrow
// pg_settings evidence used by counterbalanced PostgreSQL A/B benchmarks.
// It never collects the full catalog: the immutable A/B protocol names the
// exact settings whose effective values may differ between the two arms.
package benchmarksettings

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	SchemaVersion = "pgworkbench.benchmark-effective-settings/v1"
	ArtifactType  = "pgworkbench.benchmark-effective-settings"
	ParserVersion = "1.0.0"
	SourcePath    = "artifacts/benchmark/effective-pg-settings.tsv"

	maxSourceBytes = 1 << 20
	maxSettings    = 512
)

var settingNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.]*$`)

var sourceHeader = []string{
	"run_id",
	"protocol_digest",
	"trial",
	"captured_at",
	"server_version_num",
	"name",
	"setting",
	"unit",
	"source",
	"pending_restart",
	"context",
}

type SourceRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type PhaseBinding struct {
	Name          string `json:"name"`
	JournalDigest string `json:"journal_digest"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
}

type Setting struct {
	Name           string `json:"name"`
	Setting        string `json:"setting"`
	Unit           string `json:"unit"`
	Source         string `json:"source"`
	PendingRestart bool   `json:"pending_restart"`
	Context        string `json:"context"`
}

type Evidence struct {
	SchemaVersion    string       `json:"schema_version"`
	ArtifactType     string       `json:"artifact_type"`
	ParserVersion    string       `json:"parser_version"`
	RunID            string       `json:"run_id"`
	ProtocolDigest   string       `json:"protocol_digest"`
	Trial            int          `json:"trial"`
	CapturedAt       string       `json:"captured_at"`
	ServerVersionNum string       `json:"server_version_num"`
	Names            []string     `json:"names"`
	Settings         []Setting    `json:"settings"`
	Source           SourceRef    `json:"source"`
	Phase            PhaseBinding `json:"phase"`
	Digest           string       `json:"digest"`
}

type Expectation struct {
	RunID          string
	ProtocolDigest string
	Trial          int
	Names          []string
	Source         SourceRef
	Phase          PhaseBinding
}

// ConfigSettingNames follows the assignment recognition used by
// scripts/apply_pg_config.sh: strip everything after '#', trim whitespace,
// ignore non-assignment lines, split at the first '=', and remove whitespace
// from the setting name. Values are intentionally not returned.
func ConfigSettingNames(content []byte) ([]string, error) {
	seen := make(map[string]struct{})
	for lineNumber, line := range strings.Split(string(content), "\n") {
		if index := strings.IndexByte(line, '#'); index >= 0 {
			line = line[:index]
		}
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		name, _, _ := strings.Cut(line, "=")
		name = strings.Map(func(value rune) rune {
			if unicode.IsSpace(value) {
				return -1
			}
			return value
		}, name)
		name = strings.ToLower(name)
		if !settingNamePattern.MatchString(name) {
			return nil, fmt.Errorf("postgresql.conf line %d has invalid setting name %q", lineNumber+1, name)
		}
		seen[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		if err := ValidateNames(names); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func ConfigSettingNamesFile(path string) ([]string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSourceBytes {
		return nil, fmt.Errorf("PostgreSQL config must be a bounded non-empty regular non-symlink file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ConfigSettingNames(content)
}

func UnionConfigSettingNames(paths ...string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, path := range paths {
		names, err := ConfigSettingNamesFile(path)
		if err != nil {
			return nil, fmt.Errorf("derive assigned settings from %s: %w", path, err)
		}
		for _, name := range names {
			seen[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	if err := ValidateNames(result); err != nil {
		return nil, err
	}
	return result, nil
}

func ValidateNames(names []string) error {
	if len(names) == 0 || len(names) > maxSettings {
		return fmt.Errorf("effective-settings protocol requires between 1 and %d setting names", maxSettings)
	}
	previous := ""
	for index, name := range names {
		if !settingNamePattern.MatchString(name) {
			return fmt.Errorf("effective setting name %d is invalid: %q", index+1, name)
		}
		if sensitiveSettingName(name) {
			return fmt.Errorf("effective setting %q is denied because its value may contain credentials or executable command text", name)
		}
		if index > 0 && name <= previous {
			return fmt.Errorf("effective setting names must be sorted and unique")
		}
		previous = name
	}
	return nil
}

func sensitiveSettingName(name string) bool {
	for _, token := range []string{"password", "passphrase", "secret", "token", "credential", "conninfo"} {
		if strings.Contains(name, token) {
			return true
		}
	}
	return strings.HasSuffix(name, "_command")
}

// ParseFile independently normalizes the bounded raw TSV and binds every row
// to the expected A/B protocol, trial, source file, and prepare phase.
func ParseFile(path string, expected Expectation) (Evidence, error) {
	if err := validateExpectation(expected); err != nil {
		return Evidence{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Evidence{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSourceBytes {
		return Evidence{}, fmt.Errorf("effective-settings source must be a bounded non-empty regular non-symlink file")
	}
	if info.Size() != expected.Source.Size {
		return Evidence{}, fmt.Errorf("effective-settings source size mismatch")
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return Evidence{}, err
	}
	if digest != expected.Source.Digest {
		return Evidence{}, fmt.Errorf("effective-settings source digest mismatch")
	}
	file, err := os.Open(path)
	if err != nil {
		return Evidence{}, err
	}
	defer file.Close()
	reader := csv.NewReader(io.LimitReader(file, maxSourceBytes+1))
	reader.Comma = '\t'
	reader.FieldsPerRecord = len(sourceHeader)
	reader.ReuseRecord = false
	header, err := reader.Read()
	if err != nil || !equalStrings(header, sourceHeader) {
		return Evidence{}, fmt.Errorf("effective-settings source header mismatch")
	}

	var capturedAt, serverVersion string
	settings := make([]Setting, 0, len(expected.Names))
	for rowNumber := 2; ; rowNumber++ {
		row, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Evidence{}, fmt.Errorf("effective-settings row %d: %w", rowNumber, readErr)
		}
		if len(settings) >= maxSettings {
			return Evidence{}, fmt.Errorf("effective-settings source exceeds %d rows", maxSettings)
		}
		trial, parseErr := strconv.Atoi(row[2])
		if parseErr != nil || trial != expected.Trial || row[0] != expected.RunID || row[1] != expected.ProtocolDigest {
			return Evidence{}, fmt.Errorf("effective-settings row %d run/protocol/trial binding mismatch", rowNumber)
		}
		observed, parseErr := parseUTC(row[3])
		if parseErr != nil {
			return Evidence{}, fmt.Errorf("effective-settings row %d captured_at: %w", rowNumber, parseErr)
		}
		normalizedObserved := observed.UTC().Format(time.RFC3339Nano)
		versionNumber, versionErr := strconv.Atoi(row[4])
		if versionErr != nil || versionNumber < 10000 || strconv.Itoa(versionNumber) != row[4] {
			return Evidence{}, fmt.Errorf("effective-settings row %d server_version_num is invalid", rowNumber)
		}
		if capturedAt == "" {
			capturedAt, serverVersion = normalizedObserved, row[4]
		} else if capturedAt != normalizedObserved || serverVersion != row[4] {
			return Evidence{}, fmt.Errorf("effective-settings rows do not share one observation identity")
		}
		if !settingNamePattern.MatchString(row[5]) || row[8] == "" || row[10] == "" {
			return Evidence{}, fmt.Errorf("effective-settings row %d has an incomplete pg_settings row", rowNumber)
		}
		pending, pendingErr := parsePostgresBool(row[9])
		if pendingErr != nil {
			return Evidence{}, fmt.Errorf("effective-settings row %d: %w", rowNumber, pendingErr)
		}
		settings = append(settings, Setting{
			Name: row[5], Setting: row[6], Unit: row[7], Source: row[8], PendingRestart: pending, Context: row[10],
		})
	}
	if len(settings) != len(expected.Names) {
		return Evidence{}, fmt.Errorf("effective-settings row count %d differs from protocol names %d", len(settings), len(expected.Names))
	}
	actualNames := make([]string, len(settings))
	for index, setting := range settings {
		actualNames[index] = setting.Name
	}
	if !equalStrings(actualNames, expected.Names) {
		return Evidence{}, fmt.Errorf("effective-settings rows do not exactly match sorted protocol names")
	}
	captured, _ := time.Parse(time.RFC3339Nano, capturedAt)
	phaseStarted, _ := time.Parse(time.RFC3339Nano, expected.Phase.StartedAt)
	phaseFinished, _ := time.Parse(time.RFC3339Nano, expected.Phase.FinishedAt)
	if captured.Before(phaseStarted) || captured.After(phaseFinished) {
		return Evidence{}, fmt.Errorf("effective-settings observation is outside the prepare phase")
	}
	result := Evidence{
		SchemaVersion: SchemaVersion, ArtifactType: ArtifactType, ParserVersion: ParserVersion,
		RunID: expected.RunID, ProtocolDigest: expected.ProtocolDigest, Trial: expected.Trial,
		CapturedAt: capturedAt, ServerVersionNum: serverVersion,
		Names: append([]string(nil), expected.Names...), Settings: settings,
		Source: expected.Source, Phase: expected.Phase,
	}
	result.Digest, err = Digest(result)
	return result, err
}

func Verify(recorded Evidence) error {
	if recorded.SchemaVersion != SchemaVersion || recorded.ArtifactType != ArtifactType || recorded.ParserVersion != ParserVersion {
		return fmt.Errorf("unsupported effective-settings schema, artifact type, or parser")
	}
	if err := validateExpectation(Expectation{
		RunID: recorded.RunID, ProtocolDigest: recorded.ProtocolDigest, Trial: recorded.Trial,
		Names: recorded.Names, Source: recorded.Source, Phase: recorded.Phase,
	}); err != nil {
		return err
	}
	if len(recorded.Settings) != len(recorded.Names) {
		return fmt.Errorf("effective-settings values do not match names")
	}
	for index, setting := range recorded.Settings {
		if setting.Name != recorded.Names[index] || setting.Source == "" || setting.Context == "" {
			return fmt.Errorf("effective-settings row %d is invalid", index+1)
		}
	}
	captured, err := parseUTC(recorded.CapturedAt)
	if err != nil {
		return err
	}
	phaseStarted, _ := parseUTC(recorded.Phase.StartedAt)
	phaseFinished, _ := parseUTC(recorded.Phase.FinishedAt)
	if captured.Before(phaseStarted) || captured.After(phaseFinished) {
		return fmt.Errorf("effective-settings observation is outside the prepare phase")
	}
	versionNumber, err := strconv.Atoi(recorded.ServerVersionNum)
	if err != nil || versionNumber < 10000 || strconv.Itoa(versionNumber) != recorded.ServerVersionNum {
		return fmt.Errorf("effective-settings server_version_num is invalid")
	}
	digest, err := Digest(recorded)
	if err != nil || digest != recorded.Digest {
		return fmt.Errorf("effective-settings digest mismatch")
	}
	return nil
}

func Digest(recorded Evidence) (string, error) {
	copy := recorded
	copy.Digest = ""
	content, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func Equivalent(left, right Evidence) bool {
	if left.ServerVersionNum != right.ServerVersionNum || !equalStrings(left.Names, right.Names) || len(left.Settings) != len(right.Settings) {
		return false
	}
	for index := range left.Settings {
		if left.Settings[index] != right.Settings[index] {
			return false
		}
	}
	return true
}

func EffectiveDifferenceNames(left, right Evidence) []string {
	if !equalStrings(left.Names, right.Names) || len(left.Settings) != len(right.Settings) {
		return nil
	}
	var result []string
	for index := range left.Settings {
		if left.Settings[index].Setting != right.Settings[index].Setting || left.Settings[index].Unit != right.Settings[index].Unit {
			result = append(result, left.Names[index])
		}
	}
	return result
}

func validateExpectation(expected Expectation) error {
	if expected.RunID == "" || expected.Trial <= 0 || !evidence.IsDigest(expected.ProtocolDigest) {
		return fmt.Errorf("effective-settings run/protocol/trial expectation is invalid")
	}
	if err := ValidateNames(expected.Names); err != nil {
		return err
	}
	if expected.Source.Path != SourcePath || !evidence.IsDigest(expected.Source.Digest) || expected.Source.Size <= 0 || expected.Source.Size > maxSourceBytes {
		return fmt.Errorf("effective-settings source reference is invalid")
	}
	if expected.Phase.Name != "prepare" || !evidence.IsDigest(expected.Phase.JournalDigest) {
		return fmt.Errorf("effective-settings prepare phase binding is invalid")
	}
	started, startErr := parseUTC(expected.Phase.StartedAt)
	finished, finishErr := parseUTC(expected.Phase.FinishedAt)
	if startErr != nil || finishErr != nil || finished.Before(started) {
		return fmt.Errorf("effective-settings prepare phase interval is invalid")
	}
	return nil
}

func parseUTC(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q is not RFC3339", value)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf("timestamp %q is not UTC", value)
	}
	return parsed.UTC(), nil
}

func parsePostgresBool(value string) (bool, error) {
	switch value {
	case "t":
		return true, nil
	case "f":
		return false, nil
	default:
		return false, fmt.Errorf("pending_restart must be PostgreSQL t or f")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
