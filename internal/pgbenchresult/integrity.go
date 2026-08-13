package pgbenchresult

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// The pgbench timer and the external phase clock have independent
	// boundaries. This matches the sysbench import contract: allow 0.1% timer
	// skew in addition to the exact half-unit of the last printed TPS decimal.
	timerSkewFraction = 0.001
	// The phase journal surrounds the process invocation and therefore also
	// includes bounded shell/process/container startup and output-flush time.
	phaseBoundaryFixedSeconds = 0.5
	phaseBoundaryFraction     = 0.01
)

// ValidateTPSIntegrity independently binds every reported TPS value to the
// processed transaction count and externally journaled measure duration. A
// fixed-time result also checks that its nominal duration is within the same
// bounded clock/process allowance of the TPS-implied driver duration; the
// nominal duration is not treated as pgbench's exact internal timer.
// result must come directly from Parse so the original printed precision and
// pgbench connection-time qualifier are available.
func ValidateTPSIntegrity(result Result, measureDuration time.Duration) error {
	measureSeconds := measureDuration.Seconds()
	if !finitePositive(measureSeconds) {
		return fmt.Errorf("pgbench TPS integrity requires a positive finite measure duration")
	}
	if result.TransactionsProcessed <= 0 {
		return fmt.Errorf("pgbench TPS integrity requires a positive processed transaction count")
	}

	type reportedTPS struct {
		label       string
		value       *float64
		observation *tpsObservation
	}
	reported := []reportedTPS{
		{label: "including connections", value: result.TPSIncludingConnections, observation: result.tpsIncludingObservation},
		{label: "excluding connections", value: result.TPSExcludingConnections, observation: result.tpsExcludingObservation},
	}
	var issues []string
	seen := 0
	for _, item := range reported {
		if item.value == nil {
			continue
		}
		seen++
		if item.observation == nil {
			issues = append(issues, item.label+" TPS has no retained printed-precision observation")
			continue
		}
		if err := validateTPSObservation(result, measureSeconds, *item.value, *item.observation); err != nil {
			issues = append(issues, item.label+" TPS: "+err.Error())
		}
	}
	if seen == 0 {
		issues = append(issues, "no reported TPS value")
	}
	if result.TPSIncludingConnections != nil && result.TPSExcludingConnections != nil &&
		result.tpsIncludingObservation != nil && result.tpsExcludingObservation != nil {
		includingLower := *result.TPSIncludingConnections - printedHalfUnit(result.tpsIncludingObservation.decimalPlaces)
		excludingUpper := *result.TPSExcludingConnections + printedHalfUnit(result.tpsExcludingObservation.decimalPlaces)
		if excludingUpper < includingLower {
			issues = append(issues, "TPS excluding connection establishment is lower than TPS including it")
		}
	}
	if len(issues) > 0 {
		return fmt.Errorf("reported pgbench TPS is inconsistent with processed transactions and elapsed measure time: %s", strings.Join(issues, "; "))
	}
	return nil
}

func validateTPSObservation(result Result, measureSeconds, reported float64, observation tpsObservation) error {
	if !finitePositive(reported) || observation.decimalPlaces < 0 {
		return fmt.Errorf("reported value or precision is invalid")
	}
	if result.Mode != ModeTime && result.Mode != ModeTransactions {
		return fmt.Errorf("unsupported pgbench mode %q", result.Mode)
	}

	halfUnit := printedHalfUnit(observation.decimalPlaces)
	lowRate := reported - halfUnit
	highRate := reported + halfUnit
	if !finitePositive(lowRate) || !finitePositive(highRate) {
		return fmt.Errorf("printed rounding interval is not positive")
	}
	// A rounded rate maps to an elapsed interval in the opposite direction.
	driverMinimum := float64(result.TransactionsProcessed) / highRate
	driverMaximum := float64(result.TransactionsProcessed) / lowRate
	if result.Mode == ModeTime {
		if result.DurationSeconds == nil || !finitePositive(*result.DurationSeconds) {
			return fmt.Errorf("time mode has no positive nominal duration")
		}
		if err := validateNominalWindow(*result.DurationSeconds, driverMinimum, driverMaximum); err != nil {
			return err
		}
	}
	connectionSeconds := connectionOffsetSeconds(result, observation.qualifier)
	return validateMeasureWindow(measureSeconds, driverMinimum+connectionSeconds, driverMaximum+connectionSeconds)
}

func validateMeasureWindow(measureSeconds, minimumSeconds, maximumSeconds float64) error {
	if !finitePositive(minimumSeconds) || !finitePositive(maximumSeconds) || maximumSeconds < minimumSeconds {
		return fmt.Errorf("cannot derive a positive elapsed interval")
	}
	reference := (minimumSeconds + maximumSeconds) / 2
	timerSkew := reference * timerSkewFraction
	boundary := phaseBoundaryFixedSeconds + reference*phaseBoundaryFraction
	lower := minimumSeconds - timerSkew
	upper := maximumSeconds + timerSkew + boundary
	if measureSeconds < lower || measureSeconds > upper {
		return fmt.Errorf("measure duration %.9g s is outside bounded implied interval [%.9g, %.9g] s", measureSeconds, lower, upper)
	}
	return nil
}

func validateNominalWindow(nominalSeconds, minimumSeconds, maximumSeconds float64) error {
	if !finitePositive(nominalSeconds) || !finitePositive(minimumSeconds) || !finitePositive(maximumSeconds) || maximumSeconds < minimumSeconds {
		return fmt.Errorf("cannot derive a positive nominal/driver elapsed interval")
	}
	reference := (minimumSeconds + maximumSeconds) / 2
	timerSkew := reference * timerSkewFraction
	boundary := phaseBoundaryFixedSeconds + reference*phaseBoundaryFraction
	lower := minimumSeconds - timerSkew - boundary
	upper := maximumSeconds + timerSkew + boundary
	if nominalSeconds < lower || nominalSeconds > upper {
		return fmt.Errorf("nominal duration %.9g s is outside bounded TPS-implied driver interval [%.9g, %.9g] s", nominalSeconds, lower, upper)
	}
	return nil
}

func connectionOffsetSeconds(result Result, qualifier string) float64 {
	switch qualifier {
	case "excluding connections establishing", "without initial connection time":
		if result.InitialConnectionTimeMS != nil && *result.InitialConnectionTimeMS >= 0 {
			return *result.InitialConnectionTimeMS / 1000
		}
	}
	return 0
}

func printedHalfUnit(decimalPlaces int) float64 {
	return 0.5 * math.Pow10(-decimalPlaces)
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
