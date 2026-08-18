package pgbenchresult

import (
	"fmt"
	"math"
	"sort"
)

const (
	StatsSchemaVersion = "pgworkbench.trial-stats/v1"
	StatsVersion       = "1.0.0"
)

// TrialStats describes independent trial values. It never removes outliers.
// CV fields are null when their denominator is zero.
type TrialStats struct {
	SchemaVersion string   `json:"schema_version"`
	StatsVersion  string   `json:"stats_version"`
	N             int      `json:"n"`
	Mean          float64  `json:"mean"`
	Median        float64  `json:"median"`
	SampleStddev  float64  `json:"sample_stddev"`
	CVPct         *float64 `json:"cv_pct"`
	MAD           float64  `json:"mad"`
	RobustCVPct   *float64 `json:"robust_cv_pct"`
	Min           float64  `json:"min"`
	Max           float64  `json:"max"`
}

// RobustZFlag marks, but does not delete, a trial whose modified robust
// z-score exceeds a caller-supplied threshold. RobustZ is null when MAD is zero
// and a non-median value is flagged deterministically.
type RobustZFlag struct {
	Index   int      `json:"index"`
	Value   float64  `json:"value"`
	RobustZ *float64 `json:"robust_z"`
	Reason  string   `json:"reason"`
}

// Summarize computes robust and classical statistics over independent trials.
// At least two finite values are required because sample standard deviation is
// defined with an n-1 denominator.
func Summarize(values []float64) (TrialStats, error) {
	if len(values) < 2 {
		return TrialStats{}, fmt.Errorf("at least two independent trial values are required")
	}
	if err := validateFiniteValues(values); err != nil {
		return TrialStats{}, err
	}

	mean, sampleStddev := meanAndSampleStddev(values)
	if !finite(mean) || !finite(sampleStddev) {
		return TrialStats{}, fmt.Errorf("trial statistics overflowed finite float64 range")
	}
	median, err := PercentileType7(values, 0.5)
	if err != nil {
		return TrialStats{}, err
	}
	deviations := make([]float64, len(values))
	for index, value := range values {
		deviations[index] = math.Abs(value - median)
	}
	mad, err := PercentileType7(deviations, 0.5)
	if err != nil {
		return TrialStats{}, err
	}

	minimum := values[0]
	maximum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}

	stats := TrialStats{
		SchemaVersion: StatsSchemaVersion,
		StatsVersion:  StatsVersion,
		N:             len(values),
		Mean:          mean,
		Median:        median,
		SampleStddev:  sampleStddev,
		MAD:           mad,
		Min:           minimum,
		Max:           maximum,
	}
	if mean != 0 {
		cv := sampleStddev / math.Abs(mean) * 100
		if !finite(cv) {
			return TrialStats{}, fmt.Errorf("coefficient of variation overflowed finite float64 range")
		}
		stats.CVPct = float64Pointer(cv)
	}
	if median != 0 {
		robustCV := 1.4826 * mad / math.Abs(median) * 100
		if !finite(robustCV) {
			return TrialStats{}, fmt.Errorf("robust coefficient of variation overflowed finite float64 range")
		}
		stats.RobustCVPct = float64Pointer(robustCV)
	}
	return stats, nil
}

// PercentileType7 computes the Hyndman-Fan type-7 sample percentile without
// modifying values. Probability must be in the closed interval [0,1].
func PercentileType7(values []float64, probability float64) (float64, error) {
	if len(values) == 0 {
		return 0, fmt.Errorf("at least one value is required")
	}
	if math.IsNaN(probability) || math.IsInf(probability, 0) || probability < 0 || probability > 1 {
		return 0, fmt.Errorf("percentile probability must be finite and between 0 and 1")
	}
	if err := validateFiniteValues(values); err != nil {
		return 0, err
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0], nil
	}
	position := probability * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower], nil
	}
	fraction := position - float64(lower)
	value := sorted[lower] + fraction*(sorted[upper]-sorted[lower])
	if !finite(value) {
		return 0, fmt.Errorf("percentile interpolation overflowed finite float64 range")
	}
	return value, nil
}

// FlagRobustZ returns optional diagnostic flags. It preserves input order and
// never changes the values or the sample size used by Summarize.
func FlagRobustZ(values []float64, threshold float64) ([]RobustZFlag, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one value is required")
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 {
		return nil, fmt.Errorf("robust-z threshold must be finite and positive")
	}
	if err := validateFiniteValues(values); err != nil {
		return nil, err
	}

	median, err := PercentileType7(values, 0.5)
	if err != nil {
		return nil, err
	}
	deviations := make([]float64, len(values))
	for index, value := range values {
		deviations[index] = math.Abs(value - median)
	}
	mad, err := PercentileType7(deviations, 0.5)
	if err != nil {
		return nil, err
	}

	var flags []RobustZFlag
	if mad == 0 {
		for index, value := range values {
			if value != median {
				flags = append(flags, RobustZFlag{
					Index:  index,
					Value:  value,
					Reason: "mad_zero_nonmedian",
				})
			}
		}
		return flags, nil
	}

	for index, value := range values {
		robustZ := 0.6744897501960817 * (value - median) / mad
		if !finite(robustZ) {
			return nil, fmt.Errorf("robust-z calculation overflowed finite float64 range at index %d", index)
		}
		if math.Abs(robustZ) > threshold {
			flags = append(flags, RobustZFlag{
				Index:   index,
				Value:   value,
				RobustZ: float64Pointer(robustZ),
				Reason:  "robust_z_exceeds_threshold",
			})
		}
	}
	return flags, nil
}

func meanAndSampleStddev(values []float64) (float64, float64) {
	var count int
	var mean float64
	var sumSquaredDeviations float64
	for _, value := range values {
		count++
		delta := value - mean
		mean += delta / float64(count)
		deltaAfterMean := value - mean
		sumSquaredDeviations += delta * deltaAfterMean
	}
	return mean, math.Sqrt(sumSquaredDeviations / float64(count-1))
}

func validateFiniteValues(values []float64) error {
	for index, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("trial value at index %d is not finite", index)
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// JSON returns the deterministic JSON representation of trial statistics.
func (s TrialStats) JSON() ([]byte, error) {
	return MarshalDeterministic(s)
}
