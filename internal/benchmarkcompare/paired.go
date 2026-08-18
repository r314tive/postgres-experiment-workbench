package benchmarkcompare

import (
	"fmt"
	"math"

	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
)

const (
	// MinimumPairedUnits is the smallest predeclared complete AB/BA population
	// accepted by the paired decision path.
	MinimumPairedUnits = 5

	minimumPairedBootstrapResamples = 1000
	maximumPairedBootstrapResamples = 1000000
)

// PairedBlock records one subject-order block within a counterbalance unit.
// Baseline and Candidate are mandatory finite non-zero values only when Valid
// is true; failed execution blocks commonly have no primary metric value.
type PairedBlock struct {
	Valid     bool    `json:"valid"`
	Baseline  float64 `json:"baseline"`
	Candidate float64 `json:"candidate"`
}

// CounterbalanceUnit contains exactly one AB and one BA block. Keeping the two
// orders in the type prevents a caller from constructing an incomplete unit.
type CounterbalanceUnit struct {
	AB PairedBlock `json:"ab"`
	BA PairedBlock `json:"ba"`
}

// PairedOptions is deliberately explicit: invalid or omitted statistical
// gates never fall back to permissive defaults.
type PairedOptions struct {
	Direction              string   `json:"direction"`
	RegressionThresholdPct *float64 `json:"regression_threshold_pct,omitempty"`
	MinUnits               int      `json:"min_units"`
	BootstrapResamples     int      `json:"bootstrap_resamples"`
	ConfidenceLevel        float64  `json:"confidence_level"`
	Seed                   uint64   `json:"seed"`
}

// PairedUnitEffect preserves the normalized effect for each block and whether
// the complete unit was eligible for the statistical population.
type PairedUnitEffect struct {
	UnitIndex   int      `json:"unit_index"`
	Eligible    bool     `json:"eligible"`
	ABEffectPct *float64 `json:"ab_effect_pct,omitempty"`
	BAEffectPct *float64 `json:"ba_effect_pct,omitempty"`
}

// PairedAnalysis is a pure, deterministic analysis result. UnitsN and BlocksN
// count only complete eligible units; TotalUnits preserves exclusion context.
type PairedAnalysis struct {
	Design                 string             `json:"design"`
	Direction              string             `json:"direction"`
	BootstrapMethod        string             `json:"bootstrap_method"`
	BootstrapResamples     int                `json:"bootstrap_resamples"`
	BootstrapSeed          uint64             `json:"bootstrap_seed"`
	ConfidenceLevel        float64            `json:"confidence_level"`
	RegressionThresholdPct *float64           `json:"regression_threshold_pct,omitempty"`
	MinUnits               int                `json:"min_units"`
	TotalUnits             int                `json:"total_units"`
	UnitsN                 int                `json:"units_n"`
	BlocksN                int                `json:"blocks_n"`
	UnitEffects            []PairedUnitEffect `json:"unit_effects"`
	MedianEffectPct        float64            `json:"median_effect_pct,omitempty"`
	CILowPct               float64            `json:"ci_low_pct,omitempty"`
	CIHighPct              float64            `json:"ci_high_pct,omitempty"`
	Status                 string             `json:"status"`
	Decision               string             `json:"decision"`
	Reasons                []string           `json:"reasons"`
}

// AnalyzePaired evaluates complete counterbalanced AB/BA units. Bootstrap
// resampling operates on whole units, then takes the median of all block
// effects in the resampled units, preserving the paired design.
func AnalyzePaired(units []CounterbalanceUnit, options PairedOptions) PairedAnalysis {
	analysis := PairedAnalysis{
		Design:                 "paired-counterbalanced-ab-ba",
		Direction:              options.Direction,
		BootstrapMethod:        "percentile-cluster-bootstrap-of-block-median",
		BootstrapResamples:     options.BootstrapResamples,
		BootstrapSeed:          options.Seed,
		ConfidenceLevel:        options.ConfidenceLevel,
		RegressionThresholdPct: cloneFloat64(options.RegressionThresholdPct),
		MinUnits:               options.MinUnits,
		TotalUnits:             len(units),
		UnitEffects:            make([]PairedUnitEffect, 0, len(units)),
		Status:                 "invalid",
		Decision:               "invalid",
		Reasons:                []string{},
	}

	analysis.Reasons = append(analysis.Reasons, validatePairedOptions(options)...)
	if len(analysis.Reasons) > 0 {
		return finishPaired(analysis)
	}

	eligible := make([]pairedEffects, 0, len(units))
	for index, unit := range units {
		var ab, ba float64
		var abErr, baErr error
		if unit.AB.Valid {
			ab, abErr = pairedBlockEffect(unit.AB, options.Direction)
		}
		if unit.BA.Valid {
			ba, baErr = pairedBlockEffect(unit.BA, options.Direction)
		}
		if abErr != nil {
			analysis.Reasons = append(analysis.Reasons, fmt.Sprintf("unit %d AB: %v", index+1, abErr))
		}
		if baErr != nil {
			analysis.Reasons = append(analysis.Reasons, fmt.Sprintf("unit %d BA: %v", index+1, baErr))
		}
		if abErr != nil || baErr != nil {
			continue
		}

		complete := unit.AB.Valid && unit.BA.Valid
		unitEffect := PairedUnitEffect{
			UnitIndex: index + 1,
			Eligible:  complete,
		}
		if unit.AB.Valid {
			unitEffect.ABEffectPct = pairedFloat64(ab)
		}
		if unit.BA.Valid {
			unitEffect.BAEffectPct = pairedFloat64(ba)
		}
		analysis.UnitEffects = append(analysis.UnitEffects, unitEffect)
		if complete {
			eligible = append(eligible, pairedEffects{ab: ab, ba: ba})
		}
	}
	if len(analysis.Reasons) > 0 {
		return finishPaired(analysis)
	}

	analysis.UnitsN = len(eligible)
	analysis.BlocksN = 2 * len(eligible)
	if analysis.UnitsN < options.MinUnits {
		analysis.Status = "inconclusive"
		analysis.Decision = "inconclusive"
		analysis.Reasons = append(analysis.Reasons, fmt.Sprintf("at least %d complete valid AB/BA units are required; got %d", options.MinUnits, analysis.UnitsN))
		if excluded := len(units) - analysis.UnitsN; excluded > 0 {
			analysis.Reasons = append(analysis.Reasons, fmt.Sprintf("%d counterbalance units were excluded because at least one block was invalid", excluded))
		}
		return finishPaired(analysis)
	}

	effects := flattenPairedEffects(eligible)
	median, err := pgbenchresult.PercentileType7(effects, 0.5)
	if err != nil {
		analysis.Reasons = append(analysis.Reasons, "paired effect median: "+err.Error())
		return finishPaired(analysis)
	}
	low, high, err := bootstrapPairedCI(eligible, options)
	if err != nil {
		analysis.Reasons = append(analysis.Reasons, err.Error())
		return finishPaired(analysis)
	}
	analysis.MedianEffectPct = median
	analysis.CILowPct = low
	analysis.CIHighPct = high

	if options.RegressionThresholdPct == nil {
		analysis.Status = "inconclusive"
		analysis.Decision = "inconclusive"
		analysis.Reasons = append(analysis.Reasons, "a predeclared regression threshold is required for a paired comparison gate")
		return finishPaired(analysis)
	}

	analysis.Status, analysis.Decision, analysis.Reasons = DecideInterval(low, high, *options.RegressionThresholdPct)
	return finishPaired(analysis)
}

type pairedEffects struct {
	ab float64
	ba float64
}

func validatePairedOptions(options PairedOptions) []string {
	var reasons []string
	if options.Direction != "higher" && options.Direction != "lower" {
		reasons = append(reasons, "direction must be higher or lower")
	}
	if options.MinUnits < MinimumPairedUnits {
		reasons = append(reasons, fmt.Sprintf("min units must be at least %d", MinimumPairedUnits))
	}
	if options.BootstrapResamples < minimumPairedBootstrapResamples || options.BootstrapResamples > maximumPairedBootstrapResamples {
		reasons = append(reasons, fmt.Sprintf("bootstrap resamples must be between %d and %d", minimumPairedBootstrapResamples, maximumPairedBootstrapResamples))
	}
	if !finitePaired(options.ConfidenceLevel) || options.ConfidenceLevel <= 0.5 || options.ConfidenceLevel >= 1 {
		reasons = append(reasons, "confidence level must be finite and greater than 0.5 and less than 1")
	}
	if threshold := options.RegressionThresholdPct; threshold != nil && (!finitePaired(*threshold) || *threshold < 0) {
		reasons = append(reasons, "regression threshold must be finite and non-negative")
	}
	return reasons
}

func pairedBlockEffect(block PairedBlock, direction string) (float64, error) {
	if !finitePaired(block.Baseline) || block.Baseline == 0 {
		return 0, fmt.Errorf("baseline must be finite and non-zero")
	}
	if !finitePaired(block.Candidate) || block.Candidate == 0 {
		return 0, fmt.Errorf("candidate must be finite and non-zero")
	}
	effect := normalizedChange(block.Baseline, block.Candidate, direction)
	if !finitePaired(effect) {
		return 0, fmt.Errorf("normalized effect overflowed finite float64 range")
	}
	return effect, nil
}

func flattenPairedEffects(units []pairedEffects) []float64 {
	values := make([]float64, 0, 2*len(units))
	for _, unit := range units {
		values = append(values, unit.ab, unit.ba)
	}
	return values
}

func bootstrapPairedCI(units []pairedEffects, options PairedOptions) (float64, float64, error) {
	random := newGenerator(options.Seed)
	statistics := make([]float64, options.BootstrapResamples)
	sample := make([]float64, 2*len(units))
	for iteration := range statistics {
		for index := range units {
			selected := units[random.intn(len(units))]
			sample[2*index] = selected.ab
			sample[2*index+1] = selected.ba
		}
		median, err := pgbenchresult.PercentileType7(sample, 0.5)
		if err != nil {
			return 0, 0, fmt.Errorf("paired bootstrap median: %w", err)
		}
		statistics[iteration] = median
	}

	alpha := 1 - options.ConfidenceLevel
	low, err := pgbenchresult.PercentileType7(statistics, alpha/2)
	if err != nil {
		return 0, 0, fmt.Errorf("paired bootstrap lower bound: %w", err)
	}
	high, err := pgbenchresult.PercentileType7(statistics, 1-alpha/2)
	if err != nil {
		return 0, 0, fmt.Errorf("paired bootstrap upper bound: %w", err)
	}
	return low, high, nil
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func pairedFloat64(value float64) *float64 { return &value }

func finitePaired(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finishPaired(analysis PairedAnalysis) PairedAnalysis {
	analysis.Reasons = uniqueSorted(analysis.Reasons)
	if analysis.UnitEffects == nil {
		analysis.UnitEffects = []PairedUnitEffect{}
	}
	if analysis.Reasons == nil {
		analysis.Reasons = []string{}
	}
	return analysis
}
