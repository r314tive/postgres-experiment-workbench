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

// ValidateLatencyIntegrity binds an ordinary closed-loop pgbench latency mean
// to the same global driver window as the reported TPS. PostgreSQL computes
// that mean as elapsed_time*clients/(processed+failed+skipped), while TPS is
// processed/elapsed_time. Detailed summaries (progress, throttle, or a latency
// limit) use the per-transaction accumulator instead and are intentionally
// checked against raw logs by the benchmark layer.
func ValidateLatencyIntegrity(result Result) error {
	if result.ScheduleLagAverageMS != nil || result.LatencyLimitMS != nil || result.LatencyStddevMS != nil {
		return nil
	}
	intervals, err := ordinaryLatencyIntervals(result)
	if err != nil {
		return err
	}
	if math.Max(intervals.globalLower, intervals.printedLower) > math.Min(intervals.globalUpper, intervals.printedUpper) {
		return fmt.Errorf("reported pgbench latency is inconsistent with clients, transaction counts, and TPS: printed interval [%.9g, %.9g] ms does not intersect derived global interval [%.9g, %.9g] ms", intervals.printedLower, intervals.printedUpper, intervals.globalLower, intervals.globalUpper)
	}
	return nil
}

// ValidateRawLatencyMean binds the mean of a complete plain transaction log
// to the independently parsed pgbench summary. Detailed summaries and the log
// share one accumulator, so their rounded intervals must overlap. Ordinary
// closed-loop summaries include scheduler/client-loop gaps which have no
// universal lower bound; their only valid raw relationship is the conservative
// one-sided upper bound derived jointly from printed latency and TPS rounding.
// result must come directly from Parse so printed decimal precision is known.
func ValidateRawLatencyMean(result Result, rawMeanMS float64) error {
	if math.IsNaN(rawMeanMS) || math.IsInf(rawMeanMS, 0) || rawMeanMS < 0 {
		return fmt.Errorf("raw pgbench latency mean must be finite and non-negative")
	}
	if result.TransactionsFailed != 0 || optionalInt64(result.TransactionsSkipped) != 0 {
		return fmt.Errorf("raw pgbench latency validation requires zero failed and skipped transactions")
	}
	if result.latencyObservation == nil {
		return fmt.Errorf("raw pgbench latency validation requires the original printed-precision observation")
	}

	rawLower := math.Nextafter(rawMeanMS, math.Inf(-1))
	rawUpper := math.Nextafter(rawMeanMS, math.Inf(1))
	latencyHalfUnit := printedHalfUnit(result.latencyObservation.decimalPlaces)
	printedLower := math.Nextafter(math.Max(0, result.LatencyMeanMS-latencyHalfUnit), math.Inf(-1))
	printedUpper := math.Nextafter(result.LatencyMeanMS+latencyHalfUnit, math.Inf(1))
	if result.ScheduleLagAverageMS != nil || result.LatencyLimitMS != nil || result.LatencyStddevMS != nil {
		if rawUpper < printedLower || rawLower > printedUpper {
			return fmt.Errorf("raw pgbench latency mean %.9g ms is outside printed summary interval [%.9g, %.9g] ms", rawMeanMS, printedLower, printedUpper)
		}
		return nil
	}

	intervals, err := ordinaryLatencyIntervals(result)
	if err != nil {
		return err
	}
	if math.Max(intervals.globalLower, intervals.printedLower) > math.Min(intervals.globalUpper, intervals.printedUpper) {
		return fmt.Errorf("reported pgbench latency is inconsistent with clients, transaction counts, and TPS")
	}
	upper := math.Min(intervals.globalUpper, intervals.printedUpper)
	if rawLower > upper {
		return fmt.Errorf("raw pgbench latency mean %.9g ms exceeds derived global upper bound %.9g ms", rawMeanMS, upper)
	}
	return nil
}

type latencyIntervals struct {
	printedLower float64
	printedUpper float64
	globalLower  float64
	globalUpper  float64
}

func ordinaryLatencyIntervals(result Result) (latencyIntervals, error) {
	if result.latencyObservation == nil {
		return latencyIntervals{}, fmt.Errorf("pgbench latency integrity requires the original printed-precision observation")
	}
	reportedTPS, tpsPrecision, err := ordinaryLatencyTPS(result)
	if err != nil {
		return latencyIntervals{}, err
	}
	totalCount, err := latencyDenominator(result)
	if err != nil {
		return latencyIntervals{}, err
	}

	tpsHalfUnit := printedHalfUnit(tpsPrecision)
	tpsLower := reportedTPS - tpsHalfUnit
	tpsUpper := reportedTPS + tpsHalfUnit
	latencyHalfUnit := printedHalfUnit(result.latencyObservation.decimalPlaces)
	printedLower := math.Max(0, result.LatencyMeanMS-latencyHalfUnit)
	printedUpper := result.LatencyMeanMS + latencyHalfUnit
	if !finitePositive(tpsLower) || !finitePositive(tpsUpper) || !finitePositive(float64(totalCount)) || result.Clients <= 0 || result.TransactionsProcessed <= 0 {
		return latencyIntervals{}, fmt.Errorf("pgbench latency integrity cannot derive positive printed intervals")
	}

	// Rounded TPS maps to elapsed time, and therefore global latency, in the
	// opposite direction. Expand only by one representational ULP so binary
	// floating point cannot turn two touching decimal intervals into a failure.
	numerator := 1000 * float64(result.Clients) * float64(result.TransactionsProcessed)
	return latencyIntervals{
		globalLower:  math.Nextafter(numerator/(tpsUpper*float64(totalCount)), math.Inf(-1)),
		globalUpper:  math.Nextafter(numerator/(tpsLower*float64(totalCount)), math.Inf(1)),
		printedLower: math.Nextafter(printedLower, math.Inf(-1)),
		printedUpper: math.Nextafter(printedUpper, math.Inf(1)),
	}, nil
}

func ordinaryLatencyTPS(result Result) (float64, int, error) {
	if result.TPSIncludingConnections != nil {
		if result.tpsIncludingObservation == nil {
			return 0, 0, fmt.Errorf("pgbench latency integrity requires the original including-connections TPS observation")
		}
		switch result.tpsIncludingObservation.qualifier {
		case "including reconnection times":
			if result.TPSExcludingConnections != nil {
				return 0, 0, fmt.Errorf("pgbench latency integrity received contradictory reconnect and excluding-connections TPS values")
			}
			return *result.TPSIncludingConnections, result.tpsIncludingObservation.decimalPlaces, nil
		case "including connections establishing":
			if result.tpsExcludingObservation != nil && result.tpsExcludingObservation.qualifier != "excluding connections establishing" {
				return 0, 0, fmt.Errorf("pgbench latency integrity received mixed historical and current TPS qualifiers")
			}
			// Historical dual-TPS output computed latency from time_include.
			return *result.TPSIncludingConnections, result.tpsIncludingObservation.decimalPlaces, nil
		default:
			return 0, 0, fmt.Errorf("pgbench latency integrity received an unsupported including-connections TPS qualifier")
		}
	}
	if result.TPSExcludingConnections != nil {
		if result.tpsExcludingObservation == nil {
			return 0, 0, fmt.Errorf("pgbench latency integrity requires the original excluding-connections TPS observation")
		}
		switch result.tpsExcludingObservation.qualifier {
		case "without initial connection time":
			return *result.TPSExcludingConnections, result.tpsExcludingObservation.decimalPlaces, nil
		case "excluding connections establishing":
			return 0, 0, fmt.Errorf("pgbench latency integrity cannot derive the historical global window from excluding-connections TPS alone")
		default:
			return 0, 0, fmt.Errorf("pgbench latency integrity received an unsupported excluding-connections TPS qualifier")
		}
	}
	return 0, 0, fmt.Errorf("pgbench latency integrity requires a reported TPS value")
}

func latencyDenominator(result Result) (int64, error) {
	total := result.TransactionsProcessed
	for _, value := range []int64{result.TransactionsFailed, optionalInt64(result.TransactionsSkipped)} {
		if value < 0 || total > math.MaxInt64-value {
			return 0, fmt.Errorf("pgbench latency integrity transaction count overflows")
		}
		total += value
	}
	if total <= 0 {
		return 0, fmt.Errorf("pgbench latency integrity requires a positive transaction count")
	}
	return total, nil
}

func optionalInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
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
