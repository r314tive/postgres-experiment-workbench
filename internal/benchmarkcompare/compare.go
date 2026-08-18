package benchmarkcompare

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
)

const (
	SchemaVersion   = "pgworkbench.benchmark-comparison/v1"
	ArtifactType    = "pgworkbench.benchmark-comparison"
	AnalysisVersion = "1.0.0"
)

type Options struct {
	BootstrapResamples int
	ConfidenceLevel    float64
	Seed               uint64
}

type Comparison struct {
	SchemaVersion          string   `json:"schema_version"`
	ArtifactType           string   `json:"artifact_type"`
	AnalysisVersion        string   `json:"analysis_version"`
	BaselineRunID          string   `json:"baseline_run_id"`
	CandidateRunID         string   `json:"candidate_run_id"`
	BaselineSubject        string   `json:"baseline_subject"`
	CandidateSubject       string   `json:"candidate_subject"`
	Benchmark              string   `json:"benchmark"`
	ComparisonKeyDigest    string   `json:"comparison_key_digest"`
	PrimaryMetric          string   `json:"primary_metric"`
	Direction              string   `json:"direction"`
	Design                 string   `json:"design"`
	ConfidenceLevel        float64  `json:"confidence_level"`
	BootstrapMethod        string   `json:"bootstrap_method"`
	BootstrapResamples     int      `json:"bootstrap_resamples"`
	BootstrapSeed          uint64   `json:"bootstrap_seed"`
	BaselineN              int      `json:"baseline_n"`
	CandidateN             int      `json:"candidate_n"`
	BaselineMedian         float64  `json:"baseline_median,omitempty"`
	CandidateMedian        float64  `json:"candidate_median,omitempty"`
	ChangePct              float64  `json:"change_pct,omitempty"`
	CILowPct               float64  `json:"ci_low_pct,omitempty"`
	CIHighPct              float64  `json:"ci_high_pct,omitempty"`
	RegressionThresholdPct *float64 `json:"regression_threshold_pct,omitempty"`
	Differences            []string `json:"differences"`
	Status                 string   `json:"status"`
	Decision               string   `json:"decision"`
	Reasons                []string `json:"reasons"`
}

func Compare(root string, baselineInput string, candidateInput string, options Options) (Comparison, error) {
	options = withDefaults(options)
	comparison := Comparison{
		SchemaVersion:      SchemaVersion,
		ArtifactType:       ArtifactType,
		AnalysisVersion:    AnalysisVersion,
		Design:             "independent-series-unpaired",
		ConfidenceLevel:    options.ConfidenceLevel,
		BootstrapMethod:    "percentile-bootstrap-of-median-ratio",
		BootstrapResamples: options.BootstrapResamples,
		BootstrapSeed:      options.Seed,
		Differences:        []string{},
		Reasons:            []string{},
		Status:             "not-comparable",
		Decision:           "not-comparable",
	}

	baselineVerification, err := benchmarkartifact.Verify(root, baselineInput)
	if err != nil {
		return comparison, err
	}
	candidateVerification, err := benchmarkartifact.Verify(root, candidateInput)
	if err != nil {
		return comparison, err
	}
	if !baselineVerification.IsValid() {
		comparison.Reasons = append(comparison.Reasons, "baseline artifact verification failed: "+strings.Join(baselineVerification.Issues, "; "))
	}
	if !candidateVerification.IsValid() {
		comparison.Reasons = append(comparison.Reasons, "candidate artifact verification failed: "+strings.Join(candidateVerification.Issues, "; "))
	}
	if len(comparison.Reasons) > 0 {
		comparison.Status = "invalid"
		comparison.Decision = "invalid"
		return finish(comparison), nil
	}
	baseline := *baselineVerification.Series
	candidate := *candidateVerification.Series
	comparison.BaselineRunID = baseline.RunID
	comparison.CandidateRunID = candidate.RunID
	comparison.BaselineSubject = baseline.Subject
	comparison.CandidateSubject = candidate.Subject
	comparison.Benchmark = baseline.Benchmark
	comparison.ComparisonKeyDigest = baseline.ComparisonKeyDigest
	comparison.PrimaryMetric = baseline.PrimaryMetric
	comparison.Direction = baseline.Direction
	comparison.RegressionThresholdPct = baseline.RegressionThresholdPct

	checkComparability(&comparison, baseline, candidate)
	if len(comparison.Reasons) > 0 {
		comparison.Status = "not-comparable"
		comparison.Decision = "not-comparable"
		return finish(comparison), nil
	}
	if baseline.Status == "failed" || baseline.Status == "invalid" || candidate.Status == "failed" || candidate.Status == "invalid" {
		comparison.Status = "invalid"
		comparison.Decision = "invalid"
		comparison.Reasons = append(comparison.Reasons, "failed or invalid benchmark series cannot enter performance analysis")
		return finish(comparison), nil
	}
	if baseline.Status == "inconclusive" || candidate.Status == "inconclusive" {
		comparison.Status = "inconclusive"
		comparison.Decision = "inconclusive"
		comparison.Reasons = append(comparison.Reasons, "an input benchmark series is inconclusive")
		return finish(comparison), nil
	}
	baselineValues := trialValues(baseline)
	candidateValues := trialValues(candidate)
	comparison.BaselineN = len(baselineValues)
	comparison.CandidateN = len(candidateValues)
	if len(baselineValues) < 5 || len(candidateValues) < 5 {
		comparison.Status = "inconclusive"
		comparison.Decision = "inconclusive"
		comparison.Reasons = append(comparison.Reasons, "at least 5 valid independent trials per subject are required")
		return finish(comparison), nil
	}
	if baseline.RegressionThresholdPct == nil || candidate.RegressionThresholdPct == nil {
		comparison.Status = "inconclusive"
		comparison.Decision = "inconclusive"
		comparison.Reasons = append(comparison.Reasons, "a predeclared regression threshold is required for a comparison gate")
		return finish(comparison), nil
	}
	if *baseline.RegressionThresholdPct != *candidate.RegressionThresholdPct {
		comparison.Status = "not-comparable"
		comparison.Decision = "not-comparable"
		comparison.Reasons = append(comparison.Reasons, "regression thresholds differ")
		return finish(comparison), nil
	}

	baselineMedian, _ := pgbenchresult.PercentileType7(baselineValues, 0.5)
	candidateMedian, _ := pgbenchresult.PercentileType7(candidateValues, 0.5)
	if baselineMedian == 0 || candidateMedian == 0 {
		comparison.Status = "invalid"
		comparison.Decision = "invalid"
		comparison.Reasons = append(comparison.Reasons, "median primary metric must be non-zero")
		return finish(comparison), nil
	}
	comparison.BaselineMedian = baselineMedian
	comparison.CandidateMedian = candidateMedian
	comparison.ChangePct = normalizedChange(baselineMedian, candidateMedian, baseline.Direction)
	low, high, err := bootstrapCI(baselineValues, candidateValues, baseline.Direction, options)
	if err != nil {
		comparison.Status = "invalid"
		comparison.Decision = "invalid"
		comparison.Reasons = append(comparison.Reasons, err.Error())
		return finish(comparison), nil
	}
	comparison.CILowPct = low
	comparison.CIHighPct = high
	// Two independently executed series are useful descriptive diagnostics, but
	// they are not a counterbalanced A/B design. No mutable environment label
	// may elevate this path into a performance decision.
	comparison.Status = "inconclusive"
	comparison.Decision = "inconclusive"
	comparison.Reasons = append(comparison.Reasons, "independent unqualified series are descriptive only; a counterbalanced run with recorded qualification bookends is required for a performance verdict")
	return finish(comparison), nil
}

// DecideInterval applies the common predeclared practical-threshold semantics.
// The independent-series path never calls it; the counterbalanced paired path
// may call it only after its execution and qualification gates pass.
func DecideInterval(low, high, threshold float64) (status, decision string, reasons []string) {
	switch {
	case high < -threshold:
		return "failed", "regressed", nil
	case low >= -threshold:
		switch {
		case low > 0:
			return "passed", "improved", nil
		case high <= threshold:
			return "passed", "equivalent-within-threshold", nil
		default:
			return "passed", "no-regression", nil
		}
	default:
		return "inconclusive", "inconclusive", []string{"confidence interval crosses the predeclared regression boundary"}
	}
}

func checkComparability(comparison *Comparison, baseline benchmarkrun.Series, candidate benchmarkrun.Series) {
	if baseline.RunID == candidate.RunID {
		comparison.Reasons = append(comparison.Reasons, "baseline and candidate benchmark series must be distinct")
	}
	if trialPopulationsOverlap(baseline.Trials, candidate.Trials) {
		comparison.Reasons = append(comparison.Reasons, "baseline and candidate share linked trial runs")
	}
	if baseline.Class != "measurement" || candidate.Class != "measurement" {
		comparison.Reasons = append(comparison.Reasons, "smoke series are not performance-comparison evidence")
	}
	if baseline.ComparisonKeyDigest != candidate.ComparisonKeyDigest {
		comparison.Reasons = append(comparison.Reasons, "comparison key digests differ")
	}
	if baseline.PrimaryMetric != candidate.PrimaryMetric || baseline.Direction != candidate.Direction {
		comparison.Reasons = append(comparison.Reasons, "primary metric or direction differs")
	}
	if baseline.Runtime != candidate.Runtime {
		comparison.Reasons = append(comparison.Reasons, "native and Docker results are different performance populations")
	}
	if baseline.ResetPolicy != candidate.ResetPolicy {
		comparison.Reasons = append(comparison.Reasons, "dataset reset policies differ")
	}
	if baseline.EngineBinaryDigest != candidate.EngineBinaryDigest {
		comparison.Reasons = append(comparison.Reasons, "benchmark engine binary digests differ")
	}
	if baseline.Environment == nil || candidate.Environment == nil {
		comparison.Reasons = append(comparison.Reasons, "environment evidence is missing")
		return
	}
	left, right := baseline.Environment, candidate.Environment
	if left.Qualification != right.Qualification {
		comparison.Reasons = append(comparison.Reasons, "environment qualification differs")
	}
	for _, field := range []struct{ name, left, right string }{
		{"runtime", left.Runtime, right.Runtime},
		{"runtime_os", left.RuntimeOS, right.RuntimeOS},
		{"runtime_arch", left.RuntimeArch, right.RuntimeArch},
		{"driver", left.Driver, right.Driver},
		{"driver_version", left.DriverVersion, right.DriverVersion},
		{"parser_version", left.ParserVersion, right.ParserVersion},
		{"target_endpoint_host", left.TargetEndpointHost, right.TargetEndpointHost},
		{"docker_driver_image_id", left.DockerDriverImageID, right.DockerDriverImageID},
		{"docker_target_image_id", left.DockerTargetImageID, right.DockerTargetImageID},
		{"postgres_server_version_num", left.PostgresServerVersionNum, right.PostgresServerVersionNum},
		{"engine_version", left.EngineVersion, right.EngineVersion},
		{"engine_commit", left.EngineCommit, right.EngineCommit},
		{"pack_id", left.PackID, right.PackID},
		{"pack_version", left.PackVersion, right.PackVersion},
		{"pack_digest", left.PackDigest, right.PackDigest},
		{"native_toolchain_provenance", left.NativeToolchainProvenance, right.NativeToolchainProvenance},
	} {
		if field.left != field.right {
			comparison.Reasons = append(comparison.Reasons, field.name+" differs")
		}
	}
	if left.TargetEndpointPort != right.TargetEndpointPort {
		comparison.Reasons = append(comparison.Reasons, "target_endpoint_port differs")
	}
	if left.NativeToolchainDigest != right.NativeToolchainDigest {
		if allowed(baseline.AllowedDifferences, "native_toolchain") && allowed(candidate.AllowedDifferences, "native_toolchain") {
			comparison.Differences = append(comparison.Differences, "native_toolchain")
		} else {
			comparison.Reasons = append(comparison.Reasons, "native toolchain differs without an allowed subject dimension")
		}
	}
	if left.PGConfig != right.PGConfig || left.PGConfigDigest != right.PGConfigDigest {
		if allowed(baseline.AllowedDifferences, "pg_config") && allowed(candidate.AllowedDifferences, "pg_config") {
			comparison.Differences = append(comparison.Differences, "pg_config")
		} else {
			comparison.Reasons = append(comparison.Reasons, "PostgreSQL config differs without an allowed subject dimension")
		}
	}
}

func trialPopulationsOverlap(baseline []benchmarkrun.Trial, candidate []benchmarkrun.Trial) bool {
	runIDs := make(map[string]struct{}, len(baseline))
	for _, trial := range baseline {
		runIDs[trial.RunID] = struct{}{}
	}
	for _, trial := range candidate {
		if _, exists := runIDs[trial.RunID]; exists {
			return true
		}
	}
	return false
}

func trialValues(series benchmarkrun.Series) []float64 {
	values := make([]float64, 0, series.TrialsValid)
	for _, trial := range series.Trials {
		if trial.Status == "passed" && trial.PrimaryValue != nil {
			values = append(values, *trial.PrimaryValue)
		}
	}
	return values
}

func bootstrapCI(baseline []float64, candidate []float64, direction string, options Options) (float64, float64, error) {
	random := newGenerator(options.Seed)
	changes := make([]float64, options.BootstrapResamples)
	left := make([]float64, len(baseline))
	right := make([]float64, len(candidate))
	for iteration := range changes {
		for index := range left {
			left[index] = baseline[random.intn(len(baseline))]
		}
		for index := range right {
			right[index] = candidate[random.intn(len(candidate))]
		}
		leftMedian, _ := pgbenchresult.PercentileType7(left, 0.5)
		rightMedian, _ := pgbenchresult.PercentileType7(right, 0.5)
		if leftMedian == 0 {
			return 0, 0, fmt.Errorf("bootstrap baseline median is zero")
		}
		changes[iteration] = normalizedChange(leftMedian, rightMedian, direction)
	}
	alpha := 1 - options.ConfidenceLevel
	low, err := pgbenchresult.PercentileType7(changes, alpha/2)
	if err != nil {
		return 0, 0, err
	}
	high, err := pgbenchresult.PercentileType7(changes, 1-alpha/2)
	return low, high, err
}

func normalizedChange(baseline float64, candidate float64, direction string) float64 {
	if direction == "lower" {
		return 100 * (1 - candidate/baseline)
	}
	return 100 * (candidate/baseline - 1)
}

type generator struct{ state uint64 }

func newGenerator(seed uint64) *generator {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &generator{state: seed}
}

func (g *generator) next() uint64 {
	// SplitMix64 is small, deterministic across Go releases, and adequate for
	// reproducible bootstrap index generation (not cryptography).
	g.state += 0x9e3779b97f4a7c15
	z := g.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (g *generator) intn(n int) int { return int(g.next() % uint64(n)) }

func withDefaults(options Options) Options {
	if options.BootstrapResamples == 0 {
		options.BootstrapResamples = 10000
	}
	if options.ConfidenceLevel == 0 {
		options.ConfidenceLevel = 0.95
	}
	if options.Seed == 0 {
		options.Seed = 0x5eed5eed5eed5eed
	}
	if options.BootstrapResamples < 1000 || options.BootstrapResamples > 1000000 {
		options.BootstrapResamples = 10000
	}
	if math.IsNaN(options.ConfidenceLevel) || options.ConfidenceLevel <= 0.5 || options.ConfidenceLevel >= 1 {
		options.ConfidenceLevel = 0.95
	}
	return options
}

func allowed(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func finish(comparison Comparison) Comparison {
	comparison.Reasons = uniqueSorted(comparison.Reasons)
	comparison.Differences = uniqueSorted(comparison.Differences)
	return comparison
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func Render(w io.Writer, comparison Comparison) error {
	if _, err := fmt.Fprintf(w, "# Benchmark Comparison\n\nStatus: `%s`  \nDecision: `%s`\n\n", comparison.Status, comparison.Decision); err != nil {
		return err
	}
	if comparison.BaselineN > 0 || comparison.CandidateN > 0 {
		if _, err := fmt.Fprintf(w, "| Metric | Baseline | Candidate | Change | %g%% CI |\n| --- | ---: | ---: | ---: | ---: |\n| `%s` | `%g` | `%g` | `%+.3f%%` | `%+.3f%% .. %+.3f%%` |\n", comparison.ConfidenceLevel*100, comparison.PrimaryMetric, comparison.BaselineMedian, comparison.CandidateMedian, comparison.ChangePct, comparison.CILowPct, comparison.CIHighPct); err != nil {
			return err
		}
	}
	if len(comparison.Reasons) > 0 {
		if _, err := fmt.Fprintln(w, "\nReasons:"); err != nil {
			return err
		}
		for _, reason := range comparison.Reasons {
			if _, err := fmt.Fprintf(w, "- %s\n", reason); err != nil {
				return err
			}
		}
	}
	return nil
}

func RenderJSON(w io.Writer, comparison Comparison) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(comparison)
}
