// Package benchmarkhistory creates descriptive, independently verifiable
// timelines over immutable benchmark series. A history never upgrades its
// inputs into a performance verdict: it records compatible observations and
// their provenance so drift can be inspected without manufacturing causality.
package benchmarkhistory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

const (
	SchemaVersion  = "pgworkbench.benchmark-history/v1"
	ArtifactType   = "pgworkbench.benchmark-history"
	AnalysisDesign = "compatible-series-descriptive-history"
	maxJSONBytes   = 16 << 20
)

type Entry struct {
	RunID                 string   `json:"run_id"`
	SeriesRef             string   `json:"series_ref"`
	ResultDigest          string   `json:"result_digest"`
	Subject               string   `json:"subject"`
	ProtocolDigest        string   `json:"protocol_digest"`
	EnvironmentDigest     string   `json:"environment_digest"`
	PGConfig              string   `json:"pg_config"`
	PGConfigDigest        string   `json:"pg_config_digest"`
	StartedAt             string   `json:"started_at"`
	FinishedAt            string   `json:"finished_at"`
	SeriesStatus          string   `json:"series_status"`
	TrialsValid           int      `json:"trials_valid"`
	Median                float64  `json:"median"`
	CVPct                 *float64 `json:"cv_pct,omitempty"`
	ChangeFromPreviousPct *float64 `json:"change_from_previous_pct,omitempty"`
	ChangeFromFirstPct    *float64 `json:"change_from_first_pct,omitempty"`
}

type Result struct {
	SchemaVersion       string   `json:"schema_version"`
	ArtifactType        string   `json:"artifact_type"`
	Design              string   `json:"design"`
	HistoryID           string   `json:"history_id"`
	RunDir              string   `json:"run_dir"`
	GeneratedAt         string   `json:"generated_at"`
	Benchmark           string   `json:"benchmark"`
	Class               string   `json:"class"`
	Runtime             string   `json:"runtime"`
	EvidenceClass       string   `json:"evidence_class"`
	ComparisonKeyDigest string   `json:"comparison_key_digest"`
	EnvironmentDigest   string   `json:"environment_digest"`
	PrimaryMetric       string   `json:"primary_metric"`
	Direction           string   `json:"direction"`
	StartedAt           string   `json:"started_at"`
	FinishedAt          string   `json:"finished_at"`
	Status              string   `json:"status"`
	Conclusion          string   `json:"conclusion"`
	Reasons             []string `json:"reasons"`
	Entries             []Entry  `json:"entries"`
	Digest              string   `json:"digest"`
	ArtifactDir         string   `json:"-"`
}

type Options struct {
	HistoryID string
	Now       func() time.Time
}

type VerifyResult struct {
	Dir     string   `json:"dir"`
	Valid   bool     `json:"valid"`
	Issues  []string `json:"issues"`
	History *Result  `json:"history,omitempty"`
}

func (result VerifyResult) IsValid() bool { return len(result.Issues) == 0 }

func Create(root string, inputs []string, options Options) (Result, error) {
	if len(inputs) < 2 {
		return Result{}, fmt.Errorf("benchmark history requires at least two series")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	generatedAt := options.Now().UTC()
	historyID := strings.TrimSpace(options.HistoryID)
	if historyID == "" {
		historyID = "history-" + generatedAt.Format("20060102_150405")
	}
	if !benchmarkrun.ValidRunID(historyID) {
		return Result{}, fmt.Errorf("invalid benchmark history id %q", historyID)
	}

	series := make([]benchmarkrun.Series, 0, len(inputs))
	for _, input := range inputs {
		verification, verifyErr := benchmarkartifact.Verify(root, input)
		if verifyErr != nil {
			return Result{}, fmt.Errorf("verify benchmark series %q: %w", input, verifyErr)
		}
		if !verification.IsValid() || verification.Series == nil {
			return Result{}, fmt.Errorf("benchmark series %q is invalid: %s", input, strings.Join(verification.Issues, "; "))
		}
		item := *verification.Series
		item.ArtifactDir = verification.Dir
		series = append(series, item)
	}
	result, err := derive(historyID, generatedAt, series)
	if err != nil {
		return Result{}, err
	}

	parent, err := pathguard.EnsureDirectory(root, filepath.Join("runs", "benchmark-history"), 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare benchmark history artifact parent: %w", err)
	}
	finalDir := filepath.Join(parent, historyID)
	if _, err := os.Lstat(finalDir); err == nil {
		return Result{}, fmt.Errorf("refusing to overwrite immutable benchmark history: %s", finalDir)
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	stage, err := os.MkdirTemp(parent, "."+historyID+".tmp-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	if err := writeArtifacts(stage, result); err != nil {
		return Result{}, err
	}
	if err := os.Rename(stage, finalDir); err != nil {
		return Result{}, err
	}
	result.ArtifactDir = finalDir
	verification, err := Verify(root, finalDir)
	if err != nil {
		return result, err
	}
	if !verification.IsValid() {
		return result, fmt.Errorf("produced benchmark history is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	return result, nil
}

func derive(historyID string, generatedAt time.Time, series []benchmarkrun.Series) (Result, error) {
	if len(series) < 2 {
		return Result{}, fmt.Errorf("benchmark history requires at least two series")
	}
	seenSeries := make(map[string]struct{}, len(series))
	seenTrials := make(map[string]string)
	entries := make([]Entry, 0, len(series))
	first := series[0]
	populationEnvironmentDigest := ""
	populationSeries := ""
	for _, item := range series {
		if _, exists := seenSeries[item.RunID]; exists {
			return Result{}, fmt.Errorf("benchmark history contains duplicate series %s", item.RunID)
		}
		seenSeries[item.RunID] = struct{}{}
		if item.Benchmark != first.Benchmark || item.Class != first.Class || item.Runtime != first.Runtime || item.EvidenceClass != first.EvidenceClass || item.ComparisonKeyDigest != first.ComparisonKeyDigest || item.PrimaryMetric != first.PrimaryMetric || item.Direction != first.Direction {
			return Result{}, fmt.Errorf("series %s is outside the history comparison population", item.RunID)
		}
		if item.Status != "passed" && item.Status != "inconclusive" {
			return Result{}, fmt.Errorf("series %s has terminal status %s and cannot enter history", item.RunID, item.Status)
		}
		if item.Environment == nil {
			return Result{}, fmt.Errorf("series %s has no environment evidence", item.RunID)
		}
		if !evidence.IsDigest(item.Environment.Digest) {
			return Result{}, fmt.Errorf("series %s has an invalid environment digest", item.RunID)
		}
		populationDigest, digestErr := environmentPopulationDigest(*item.Environment)
		if digestErr != nil {
			return Result{}, fmt.Errorf("derive series %s environment population: %w", item.RunID, digestErr)
		}
		if populationEnvironmentDigest == "" {
			populationEnvironmentDigest = populationDigest
			populationSeries = item.RunID
		} else if populationDigest != populationEnvironmentDigest {
			return Result{}, fmt.Errorf("series %s is outside the exact history environment population: population digest %s differs from series %s digest %s", item.RunID, populationDigest, populationSeries, populationEnvironmentDigest)
		}
		for _, trial := range item.Trials {
			if owner, exists := seenTrials[trial.RunID]; exists {
				return Result{}, fmt.Errorf("series %s and %s share linked trial run %s", owner, item.RunID, trial.RunID)
			}
			seenTrials[trial.RunID] = item.RunID
		}
		median, cv, err := representative(item)
		if err != nil {
			return Result{}, err
		}
		digest, err := evidence.DigestFile(filepath.Join(item.ArtifactDir, "result.json"))
		if err != nil {
			return Result{}, fmt.Errorf("digest series %s result: %w", item.RunID, err)
		}
		entries = append(entries, Entry{
			RunID:             item.RunID,
			SeriesRef:         filepath.ToSlash(filepath.Join("runs", "benchmarks", item.RunID)),
			ResultDigest:      digest,
			Subject:           item.Subject,
			ProtocolDigest:    item.ProtocolDigest,
			EnvironmentDigest: item.Environment.Digest,
			PGConfig:          item.Environment.PGConfig,
			PGConfigDigest:    item.Environment.PGConfigDigest,
			StartedAt:         item.StartedAt,
			FinishedAt:        item.FinishedAt,
			SeriesStatus:      item.Status,
			TrialsValid:       item.TrialsValid,
			Median:            median,
			CVPct:             cv,
		})
	}
	type interval struct {
		started  time.Time
		finished time.Time
	}
	intervals := make(map[string]interval, len(entries))
	for _, entry := range entries {
		if !canonicalUTC(entry.StartedAt) || !canonicalUTC(entry.FinishedAt) {
			return Result{}, fmt.Errorf("series %s interval is not canonical UTC RFC3339", entry.RunID)
		}
		started, _ := time.Parse(time.RFC3339Nano, entry.StartedAt)
		finished, _ := time.Parse(time.RFC3339Nano, entry.FinishedAt)
		if finished.Before(started) {
			return Result{}, fmt.Errorf("series %s finishes before it starts", entry.RunID)
		}
		intervals[entry.RunID] = interval{started: started, finished: finished}
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := intervals[entries[i].RunID].started, intervals[entries[j].RunID].started
		if left.Equal(right) {
			return entries[i].RunID < entries[j].RunID
		}
		return left.Before(right)
	})
	latestFinished := intervals[entries[0].RunID].finished
	for index := range entries {
		if index > 0 && entries[index-1].Median != 0 {
			change := normalizedChange(entries[index-1].Median, entries[index].Median, first.Direction)
			entries[index].ChangeFromPreviousPct = &change
		}
		if index > 0 && entries[0].Median != 0 {
			change := normalizedChange(entries[0].Median, entries[index].Median, first.Direction)
			entries[index].ChangeFromFirstPct = &change
		}
	}
	for _, entry := range entries[1:] {
		if finished := intervals[entry.RunID].finished; finished.After(latestFinished) {
			latestFinished = finished
		}
	}
	if generatedAt.Before(latestFinished) {
		return Result{}, fmt.Errorf("history generation time precedes an input series")
	}
	reasons := []string{"descriptive history does not establish causality or issue a performance verdict"}
	for _, entry := range entries {
		if entry.SeriesStatus == "inconclusive" {
			reasons = append(reasons, "one or more input series are inconclusive")
			break
		}
	}
	result := Result{
		SchemaVersion:       SchemaVersion,
		ArtifactType:        ArtifactType,
		Design:              AnalysisDesign,
		HistoryID:           historyID,
		RunDir:              ".",
		GeneratedAt:         generatedAt.UTC().Format(time.RFC3339Nano),
		Benchmark:           first.Benchmark,
		Class:               first.Class,
		Runtime:             first.Runtime,
		EvidenceClass:       first.EvidenceClass,
		ComparisonKeyDigest: first.ComparisonKeyDigest,
		EnvironmentDigest:   populationEnvironmentDigest,
		PrimaryMetric:       first.PrimaryMetric,
		Direction:           first.Direction,
		StartedAt:           entries[0].StartedAt,
		FinishedAt:          latestFinished.UTC().Format(time.RFC3339Nano),
		Status:              "passed",
		Conclusion:          "descriptive",
		Reasons:             uniqueSorted(reasons),
		Entries:             entries,
	}
	digest, err := resultDigest(result)
	if err != nil {
		return Result{}, err
	}
	result.Digest = digest
	return result, nil
}

// environmentPopulationDigest binds every runtime and byte-identity field but
// excludes the self digest and the series-local native snapshot location. The
// referenced snapshot is independently verified by the series verifier; its
// path changes with run ID and is not itself a performance population trait.
func environmentPopulationDigest(environment benchmarkrun.Environment) (string, error) {
	if !evidence.IsDigest(environment.Digest) {
		return "", fmt.Errorf("environment digest is invalid")
	}
	environment.Digest = ""
	environment.NativeToolchainManifestRef = ""
	content, err := json.Marshal(environment)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func representative(series benchmarkrun.Series) (float64, *float64, error) {
	if series.Stats != nil {
		var cv *float64
		if series.Stats.CVPct != nil {
			value := *series.Stats.CVPct
			cv = &value
		}
		return series.Stats.Median, cv, nil
	}
	if series.TrialsValid == 1 {
		for _, trial := range series.Trials {
			if trial.Status == "passed" && trial.PrimaryValue != nil {
				return *trial.PrimaryValue, nil, nil
			}
		}
	}
	return 0, nil, fmt.Errorf("series %s has no representative primary value", series.RunID)
}

// normalizedChange expresses every history delta as positive-is-better while
// keeping it descriptive. The history artifact never maps this value to a
// regression or improvement verdict.
func normalizedChange(baseline, candidate float64, direction string) float64 {
	if direction == "lower" {
		return 100 * (1 - candidate/baseline)
	}
	return 100 * (candidate/baseline - 1)
}

func Resolve(root, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates, filepath.Join(root, input), filepath.Join(root, "runs", "benchmark-history", input))
	}
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate)
		if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return filepath.Abs(candidate)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("benchmark history directory not found: %s", input)
}

func Load(root, input string) (Result, error) {
	dir, err := Resolve(root, input)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := decodeStrict(filepath.Join(dir, "result.json"), &result); err != nil {
		return Result{}, err
	}
	result.ArtifactDir = dir
	return result, nil
}

func Verify(root, input string) (VerifyResult, error) {
	dir, err := Resolve(root, input)
	if err != nil {
		return VerifyResult{}, err
	}
	verification := VerifyResult{Dir: dir, Issues: []string{}}
	for _, name := range []string{"result.json", "summary.md"} {
		if err := checkRegular(filepath.Join(dir, name)); err != nil {
			addIssue(&verification, "%s: %v", name, err)
		}
	}
	stored, err := Load(root, dir)
	if err != nil {
		addIssue(&verification, "result.json parse failed: %v", err)
		return verification, nil
	}
	verification.History = &stored
	checkIdentity(&verification, dir, stored)
	artifactRoot := inferArtifactRoot(root, dir)
	series := make([]benchmarkrun.Series, 0, len(stored.Entries))
	for index, entry := range stored.Entries {
		wantRef := filepath.ToSlash(filepath.Join("runs", "benchmarks", entry.RunID))
		if entry.SeriesRef != wantRef || !evidence.IsPortablePath(entry.SeriesRef) {
			addIssue(&verification, "entry %d series reference is not canonical", index+1)
			continue
		}
		path, joinErr := safeExistingJoin(artifactRoot, entry.SeriesRef)
		if joinErr != nil {
			addIssue(&verification, "entry %d series reference is unsafe: %v", index+1, joinErr)
			continue
		}
		checked, verifyErr := benchmarkartifact.Verify(artifactRoot, path)
		if verifyErr != nil {
			addIssue(&verification, "entry %d series verification failed: %v", index+1, verifyErr)
			continue
		}
		if !checked.IsValid() || checked.Series == nil {
			addIssue(&verification, "entry %d series is invalid: %s", index+1, strings.Join(checked.Issues, "; "))
			continue
		}
		digest, digestErr := evidence.DigestFile(filepath.Join(path, "result.json"))
		if digestErr != nil || digest != entry.ResultDigest {
			addIssue(&verification, "entry %d result digest mismatch", index+1)
		}
		item := *checked.Series
		item.ArtifactDir = path
		series = append(series, item)
	}
	if len(series) == len(stored.Entries) {
		generatedAt, parseErr := time.Parse(time.RFC3339Nano, stored.GeneratedAt)
		if parseErr != nil {
			addIssue(&verification, "generated_at is not RFC3339")
		} else if rebuilt, deriveErr := derive(stored.HistoryID, generatedAt, series); deriveErr != nil {
			addIssue(&verification, "history cannot be independently derived: %v", deriveErr)
		} else {
			rebuilt.ArtifactDir = stored.ArtifactDir
			if !reflect.DeepEqual(rebuilt, stored) {
				addIssue(&verification, "result.json does not match independently derived history")
			}
		}
	}
	if content, readErr := readRegularLimited(filepath.Join(dir, "summary.md"), maxJSONBytes); readErr != nil {
		addIssue(&verification, "summary.md read failed: %v", readErr)
	} else if !bytes.Equal(content, summaryBytes(stored)) {
		addIssue(&verification, "summary.md does not match independently rendered history")
	}
	verification.Valid = verification.IsValid()
	return verification, nil
}

func checkIdentity(verification *VerifyResult, dir string, result Result) {
	if result.SchemaVersion != SchemaVersion || result.ArtifactType != ArtifactType || result.Design != AnalysisDesign {
		addIssue(verification, "unsupported history schema, artifact type, or design")
	}
	if !benchmarkrun.ValidRunID(result.HistoryID) || filepath.Base(dir) != result.HistoryID || result.RunDir != "." {
		addIssue(verification, "history identity or portable run_dir is invalid")
	}
	if result.Status != "passed" || result.Conclusion != "descriptive" {
		addIssue(verification, "history must remain passed/descriptive")
	}
	if len(result.Entries) < 2 || result.Entries == nil || result.Reasons == nil {
		addIssue(verification, "history requires present reasons and at least two entries")
	}
	if !evidence.IsDigest(result.ComparisonKeyDigest) || !evidence.IsDigest(result.EnvironmentDigest) || !evidence.IsDigest(result.Digest) {
		addIssue(verification, "history contains an invalid digest")
	}
	if digest, err := resultDigest(result); err != nil || digest != result.Digest {
		addIssue(verification, "history digest mismatch")
	}
	if !canonicalUTC(result.GeneratedAt) || !canonicalUTC(result.StartedAt) || !canonicalUTC(result.FinishedAt) {
		addIssue(verification, "history timestamps must be canonical UTC RFC3339")
	}
	if !reflect.DeepEqual(result.Reasons, uniqueSorted(result.Reasons)) {
		addIssue(verification, "history reasons are not sorted and unique")
	}
	if !sort.SliceIsSorted(result.Entries, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, result.Entries[i].StartedAt)
		right, rightErr := time.Parse(time.RFC3339Nano, result.Entries[j].StartedAt)
		if leftErr != nil || rightErr != nil {
			return result.Entries[i].StartedAt < result.Entries[j].StartedAt
		}
		if left.Equal(right) {
			return result.Entries[i].RunID < result.Entries[j].RunID
		}
		return left.Before(right)
	}) {
		addIssue(verification, "history entries are not chronological")
	}
}

func writeArtifacts(dir string, result Result) error {
	content, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), append(content, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), summaryBytes(result), 0o644)
}

func summaryBytes(result Result) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Benchmark history `%s`\n\n", result.HistoryID)
	fmt.Fprintf(&builder, "- Benchmark: `%s`\n", result.Benchmark)
	fmt.Fprintf(&builder, "- Population: `%s` / comparison `%s` / environment `%s`\n", result.Runtime, result.ComparisonKeyDigest, result.EnvironmentDigest)
	fmt.Fprintf(&builder, "- Primary metric: `%s` (%s is better)\n", result.PrimaryMetric, result.Direction)
	fmt.Fprintf(&builder, "- Conclusion: **descriptive only**\n\n")
	builder.WriteString("| Started (UTC) | Run | Subject | PostgreSQL config | Status | Valid trials | Median | CV % | vs previous % | vs first % |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, entry := range result.Entries {
		cv := ""
		if entry.CVPct != nil {
			cv = fmt.Sprintf("%.6g", *entry.CVPct)
		}
		previous, first := "", ""
		if entry.ChangeFromPreviousPct != nil {
			previous = fmt.Sprintf("%+.6g", *entry.ChangeFromPreviousPct)
		}
		if entry.ChangeFromFirstPct != nil {
			first = fmt.Sprintf("%+.6g", *entry.ChangeFromFirstPct)
		}
		fmt.Fprintf(&builder, "| %s | `%s` | %s | `%s` | %s | %d | %.12g | %s | %s | %s |\n", entry.StartedAt, entry.RunID, escapeTable(entry.Subject), entry.PGConfig, entry.SeriesStatus, entry.TrialsValid, entry.Median, cv, previous, first)
	}
	builder.WriteString("\nThis timeline records compatible immutable observations. It does not establish causality and cannot issue a performance verdict.\n")
	return []byte(builder.String())
}

func resultDigest(result Result) (string, error) {
	result.Digest = ""
	result.ArtifactDir = ""
	content, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func decodeStrict(path string, value any) error {
	content, err := readRegularLimited(path, maxJSONBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func readRegularLimited(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("file is empty, oversized, symlinked, or non-regular")
	}
	return os.ReadFile(path)
}

func checkRegular(path string) error {
	_, err := readRegularLimited(path, maxJSONBytes)
	return err
}

func inferArtifactRoot(root, dir string) string {
	dir, _ = filepath.Abs(dir)
	marker := filepath.Join("runs", "benchmark-history", filepath.Base(dir))
	if strings.HasSuffix(dir, marker) {
		candidate := strings.TrimSuffix(dir, marker)
		candidate = strings.TrimSuffix(candidate, string(filepath.Separator))
		if candidate != "" {
			return candidate
		}
	}
	root, _ = filepath.Abs(root)
	return root
}

func safeExistingJoin(root, reference string) (string, error) {
	if !evidence.IsPortablePath(reference) {
		return "", fmt.Errorf("reference is not portable")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(reference))
	relative, err := filepath.Rel(root, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference escapes artifact root")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	if resolved != filepath.Join(resolvedRoot, filepath.FromSlash(reference)) {
		return "", fmt.Errorf("reference resolves through a symlink")
	}
	return resolved, nil
}

func canonicalUTC(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z") && parsed.Location() == time.UTC && parsed.Format(time.RFC3339Nano) == value
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

func addIssue(result *VerifyResult, format string, values ...any) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, values...))
	result.Issues = uniqueSorted(result.Issues)
}

func escapeTable(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " ")
}

func Render(w io.Writer, result Result) error {
	_, err := fmt.Fprintf(w, "PASS: benchmark history %s entries=%d conclusion=%s\nhistory_dir=%s\n", result.HistoryID, len(result.Entries), result.Conclusion, result.ArtifactDir)
	return err
}

func RenderJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func RenderVerify(w io.Writer, result VerifyResult) error {
	status := "PASS"
	if !result.IsValid() {
		status = "FAIL"
	}
	if _, err := fmt.Fprintf(w, "%s: benchmark history %s\n", status, result.Dir); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(w, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}
