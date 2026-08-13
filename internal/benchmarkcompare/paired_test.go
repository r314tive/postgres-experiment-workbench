package benchmarkcompare

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestAnalyzePairedNormalizesHigherAndLowerEffects(t *testing.T) {
	tests := []struct {
		name         string
		direction    string
		abCandidate  float64
		baCandidate  float64
		wantMedian   float64
		wantStatus   string
		wantDecision string
	}{
		{
			name:         "higher is better",
			direction:    "higher",
			abCandidate:  110,
			baCandidate:  120,
			wantMedian:   15,
			wantStatus:   "passed",
			wantDecision: "improved",
		},
		{
			name:         "lower is better",
			direction:    "lower",
			abCandidate:  110,
			baCandidate:  120,
			wantMedian:   -15,
			wantStatus:   "failed",
			wantDecision: "regressed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			units := repeatedPairedUnits(5, PairedBlock{Valid: true, Baseline: 100, Candidate: test.abCandidate}, PairedBlock{Valid: true, Baseline: 100, Candidate: test.baCandidate})
			options := validPairedOptions(test.direction)

			analysis := AnalyzePaired(units, options)

			if analysis.Status != test.wantStatus || analysis.Decision != test.wantDecision {
				t.Fatalf("unexpected decision: %#v", analysis)
			}
			if analysis.TotalUnits != 5 || analysis.UnitsN != 5 || analysis.BlocksN != 10 {
				t.Fatalf("unexpected population counts: %#v", analysis)
			}
			assertPairedClose(t, analysis.MedianEffectPct, test.wantMedian)
			assertPairedClose(t, analysis.CILowPct, test.wantMedian)
			assertPairedClose(t, analysis.CIHighPct, test.wantMedian)
			if len(analysis.UnitEffects) != 5 || !analysis.UnitEffects[0].Eligible {
				t.Fatalf("unit effects were not retained: %#v", analysis.UnitEffects)
			}
			assertPairedEffect(t, analysis.UnitEffects[0].ABEffectPct, normalizedChange(100, test.abCandidate, test.direction))
			assertPairedEffect(t, analysis.UnitEffects[0].BAEffectPct, normalizedChange(100, test.baCandidate, test.direction))
		})
	}
}

func TestAnalyzePairedBootstrapResamplesWholeCounterbalanceUnits(t *testing.T) {
	// Every unit contains the same opposing pair. A cluster bootstrap must keep
	// the pair together, making every resampled median exactly zero. Sampling
	// flattened blocks independently would produce a non-zero interval.
	units := repeatedPairedUnits(5,
		PairedBlock{Valid: true, Baseline: 1, Candidate: 0.5},
		PairedBlock{Valid: true, Baseline: 1, Candidate: 1.5},
	)
	options := validPairedOptions("higher")
	threshold := 60.0
	options.RegressionThresholdPct = &threshold

	analysis := AnalyzePaired(units, options)

	if analysis.Status != "passed" || analysis.Decision != "equivalent-within-threshold" {
		t.Fatalf("unexpected paired decision: %#v", analysis)
	}
	if analysis.MedianEffectPct != 0 || analysis.CILowPct != 0 || analysis.CIHighPct != 0 {
		t.Fatalf("bootstrap did not preserve whole units: %#v", analysis)
	}
}

func TestAnalyzePairedExcludesWholeUnitWhenEitherBlockIsInvalid(t *testing.T) {
	units := repeatedPairedUnits(6,
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
	)
	units[2].BA = PairedBlock{Valid: false}

	analysis := AnalyzePaired(units, validPairedOptions("higher"))

	if analysis.Status != "passed" || analysis.Decision != "improved" {
		t.Fatalf("an ineligible unit poisoned the complete population: %#v", analysis)
	}
	if analysis.TotalUnits != 6 || analysis.UnitsN != 5 || analysis.BlocksN != 10 {
		t.Fatalf("partial unit entered the statistical population: %#v", analysis)
	}
	if len(analysis.UnitEffects) != 6 || analysis.UnitEffects[2].Eligible || analysis.UnitEffects[2].ABEffectPct == nil || analysis.UnitEffects[2].BAEffectPct != nil {
		t.Fatalf("excluded unit was not represented correctly: %#v", analysis.UnitEffects)
	}
}

func TestAnalyzePairedInsufficientCompleteUnitsIsInconclusive(t *testing.T) {
	units := repeatedPairedUnits(5,
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
	)
	units[4].AB = PairedBlock{Valid: false}

	analysis := AnalyzePaired(units, validPairedOptions("higher"))

	if analysis.Status != "inconclusive" || analysis.Decision != "inconclusive" || analysis.UnitsN != 4 || analysis.BlocksN != 8 {
		t.Fatalf("insufficient complete units did not fail closed: %#v", analysis)
	}
	if !pairedReasonContains(analysis.Reasons, "at least 5 complete valid AB/BA units are required") || !pairedReasonContains(analysis.Reasons, "1 counterbalance units were excluded") {
		t.Fatalf("population failure reasons are incomplete: %v", analysis.Reasons)
	}
}

func TestAnalyzePairedRejectsMalformedBlockValues(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*CounterbalanceUnit)
		wantReason string
	}{
		{"zero baseline", func(unit *CounterbalanceUnit) { unit.AB.Baseline = 0 }, "baseline must be finite and non-zero"},
		{"zero candidate", func(unit *CounterbalanceUnit) { unit.AB.Candidate = 0 }, "candidate must be finite and non-zero"},
		{"nan baseline", func(unit *CounterbalanceUnit) { unit.BA.Baseline = math.NaN() }, "baseline must be finite and non-zero"},
		{"infinite candidate", func(unit *CounterbalanceUnit) { unit.BA.Candidate = math.Inf(1) }, "candidate must be finite and non-zero"},
		{"overflowing effect", func(unit *CounterbalanceUnit) {
			unit.AB.Baseline = math.SmallestNonzeroFloat64
			unit.AB.Candidate = math.MaxFloat64
		}, "normalized effect overflowed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			units := repeatedPairedUnits(5,
				PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
				PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
			)
			test.mutate(&units[0])

			analysis := AnalyzePaired(units, validPairedOptions("higher"))

			if analysis.Status != "invalid" || analysis.Decision != "invalid" || !pairedReasonContains(analysis.Reasons, test.wantReason) {
				t.Fatalf("malformed block did not fail closed: %#v", analysis)
			}
		})
	}
}

func TestAnalyzePairedRejectsInvalidStatisticalContract(t *testing.T) {
	negativeThreshold := -1.0
	nanThreshold := math.NaN()
	tests := []struct {
		name       string
		mutate     func(*PairedOptions)
		wantReason string
	}{
		{"direction", func(options *PairedOptions) { options.Direction = "sideways" }, "direction must be higher or lower"},
		{"min units", func(options *PairedOptions) { options.MinUnits = MinimumPairedUnits - 1 }, "min units must be at least 5"},
		{"too few resamples", func(options *PairedOptions) { options.BootstrapResamples = 999 }, "bootstrap resamples must be between"},
		{"too many resamples", func(options *PairedOptions) { options.BootstrapResamples = 1000001 }, "bootstrap resamples must be between"},
		{"low confidence", func(options *PairedOptions) { options.ConfidenceLevel = 0.5 }, "confidence level must be finite"},
		{"high confidence", func(options *PairedOptions) { options.ConfidenceLevel = 1 }, "confidence level must be finite"},
		{"nan confidence", func(options *PairedOptions) { options.ConfidenceLevel = math.NaN() }, "confidence level must be finite"},
		{"negative threshold", func(options *PairedOptions) { options.RegressionThresholdPct = &negativeThreshold }, "regression threshold must be finite and non-negative"},
		{"nan threshold", func(options *PairedOptions) { options.RegressionThresholdPct = &nanThreshold }, "regression threshold must be finite and non-negative"},
	}
	units := repeatedPairedUnits(5,
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
	)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validPairedOptions("higher")
			test.mutate(&options)

			analysis := AnalyzePaired(units, options)

			if analysis.Status != "invalid" || analysis.Decision != "invalid" || !pairedReasonContains(analysis.Reasons, test.wantReason) {
				t.Fatalf("invalid analysis contract did not fail closed: %#v", analysis)
			}
		})
	}
}

func TestAnalyzePairedRequiresPredeclaredThresholdForVerdict(t *testing.T) {
	units := repeatedPairedUnits(5,
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
		PairedBlock{Valid: true, Baseline: 100, Candidate: 110},
	)
	options := validPairedOptions("higher")
	options.RegressionThresholdPct = nil

	analysis := AnalyzePaired(units, options)

	if analysis.Status != "inconclusive" || analysis.Decision != "inconclusive" || !pairedReasonContains(analysis.Reasons, "predeclared regression threshold") {
		t.Fatalf("missing predeclared threshold produced a verdict: %#v", analysis)
	}
	assertPairedClose(t, analysis.MedianEffectPct, 10)
	assertPairedClose(t, analysis.CILowPct, 10)
	assertPairedClose(t, analysis.CIHighPct, 10)
}

func TestAnalyzePairedSeededBootstrapIsDeterministic(t *testing.T) {
	units := []CounterbalanceUnit{
		pairedUnit(100, 102, 100, 98),
		pairedUnit(100, 105, 100, 99),
		pairedUnit(100, 101, 100, 104),
		pairedUnit(100, 97, 100, 103),
		pairedUnit(100, 106, 100, 100),
		pairedUnit(100, 99, 100, 107),
	}
	options := validPairedOptions("higher")
	options.Seed = 0x123456789abcdef0

	first := AnalyzePaired(units, options)
	second := AnalyzePaired(units, options)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("seeded paired bootstrap is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func validPairedOptions(direction string) PairedOptions {
	threshold := 5.0
	return PairedOptions{
		Direction:              direction,
		RegressionThresholdPct: &threshold,
		MinUnits:               MinimumPairedUnits,
		BootstrapResamples:     2000,
		ConfidenceLevel:        0.95,
		Seed:                   0x5eed,
	}
}

func repeatedPairedUnits(count int, ab, ba PairedBlock) []CounterbalanceUnit {
	units := make([]CounterbalanceUnit, count)
	for index := range units {
		units[index] = CounterbalanceUnit{AB: ab, BA: ba}
	}
	return units
}

func pairedUnit(abBaseline, abCandidate, baBaseline, baCandidate float64) CounterbalanceUnit {
	return CounterbalanceUnit{
		AB: PairedBlock{Valid: true, Baseline: abBaseline, Candidate: abCandidate},
		BA: PairedBlock{Valid: true, Baseline: baBaseline, Candidate: baCandidate},
	}
}

func pairedReasonContains(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}

func assertPairedClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %g, want %g", got, want)
	}
}

func assertPairedEffect(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("missing block effect; want %g", want)
	}
	assertPairedClose(t, *got, want)
}
