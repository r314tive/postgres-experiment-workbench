package benchmarkqualify

import (
	"fmt"
	"sort"
	"strconv"
)

func evaluate(snapshot Snapshot, policy Policy) ([]Check, string, []string) {
	checks := make([]Check, 0, policyGateCount(policy))
	if policy.MinLogicalCPUs != nil {
		checks = append(checks, minimumUintCheck("min_logical_cpus", snapshot.CPU.LogicalCPUs, *policy.MinLogicalCPUs))
	}
	if policy.MinMemoryAvailablePct != nil {
		checks = append(checks, minimumNumberCheck("min_memory_available_pct", snapshot.Memory.AvailablePct, *policy.MinMemoryAvailablePct))
	}
	if policy.MinStorageAvailablePct != nil {
		checks = append(checks, minimumNumberCheck("min_storage_available_pct", snapshot.Storage.AvailablePct, *policy.MinStorageAvailablePct))
	}
	if policy.MaxLoad1PerCPU != nil {
		checks = append(checks, maximumNumberCheck("max_load_1m_per_cpu", snapshot.Interference.Load1PerCPU, *policy.MaxLoad1PerCPU))
	}
	if policy.RequiredClocksource != "" {
		checks = append(checks, equalStringCheck("required_clocksource", snapshot.Clock.Clocksource, policy.RequiredClocksource))
	}
	if policy.RequiredGovernor != "" {
		checks = append(checks, containsStringCheck("required_governor", snapshot.Power.Governors, policy.RequiredGovernor))
	}
	if policy.MaxTemperatureCelsius != nil {
		checks = append(checks, maximumNumberCheck("max_temperature_celsius", snapshot.Thermal.MaxCelsius, *policy.MaxTemperatureCelsius))
	}
	if policy.RequiredClientPlacement != "" {
		checks = append(checks, equalStringCheck("required_client_placement", snapshot.Client.Placement, policy.RequiredClientPlacement))
	}

	reasons := make([]string, 0, len(checks)+2)
	qualified := true
	if !policy.Strict {
		qualified = false
		reasons = append(reasons, "strict policy is not enabled")
	}
	if len(checks) == 0 {
		qualified = false
		reasons = append(reasons, "no explicit qualification gates were configured")
	}
	for _, check := range checks {
		if check.Status == CheckFailed {
			qualified = false
			reasons = append(reasons, fmt.Sprintf("%s failed: observed %s; require %s", check.Gate, check.Observation, check.Requirement))
		}
	}
	if qualified {
		return checks, VerdictQualified, reasons
	}
	return checks, VerdictUnqualified, reasons
}

func policyGateCount(policy Policy) int {
	count := 0
	if policy.MinLogicalCPUs != nil {
		count++
	}
	if policy.MinMemoryAvailablePct != nil {
		count++
	}
	if policy.MinStorageAvailablePct != nil {
		count++
	}
	if policy.MaxLoad1PerCPU != nil {
		count++
	}
	if policy.RequiredClocksource != "" {
		count++
	}
	if policy.RequiredGovernor != "" {
		count++
	}
	if policy.MaxTemperatureCelsius != nil {
		count++
	}
	if policy.RequiredClientPlacement != "" {
		count++
	}
	return count
}

func minimumUintCheck(gate string, observation UintObservation, minimum uint64) Check {
	check := Check{Gate: gate, Status: CheckFailed, Observation: "unavailable", Requirement: ">= " + strconv.FormatUint(minimum, 10)}
	if observation.Availability != AvailabilityObserved || observation.Value == nil {
		return check
	}
	check.Observation = strconv.FormatUint(*observation.Value, 10)
	if *observation.Value >= minimum {
		check.Status = CheckPassed
	}
	return check
}

func minimumNumberCheck(gate string, observation NumberObservation, minimum float64) Check {
	check := Check{Gate: gate, Status: CheckFailed, Observation: "unavailable", Requirement: ">= " + formatNumber(minimum)}
	if observation.Availability != AvailabilityObserved || observation.Value == nil {
		return check
	}
	check.Observation = formatNumber(*observation.Value)
	if *observation.Value >= minimum {
		check.Status = CheckPassed
	}
	return check
}

func maximumNumberCheck(gate string, observation NumberObservation, maximum float64) Check {
	check := Check{Gate: gate, Status: CheckFailed, Observation: "unavailable", Requirement: "<= " + formatNumber(maximum)}
	if observation.Availability != AvailabilityObserved || observation.Value == nil {
		return check
	}
	check.Observation = formatNumber(*observation.Value)
	if *observation.Value <= maximum {
		check.Status = CheckPassed
	}
	return check
}

func equalStringCheck(gate string, observation StringObservation, required string) Check {
	check := Check{Gate: gate, Status: CheckFailed, Observation: "unavailable", Requirement: "= " + required}
	if observation.Availability != AvailabilityObserved {
		return check
	}
	check.Observation = observation.Value
	if observation.Value == required {
		check.Status = CheckPassed
	}
	return check
}

func containsStringCheck(gate string, observation StringListObservation, required string) Check {
	check := Check{Gate: gate, Status: CheckFailed, Observation: "unavailable", Requirement: "contains " + required}
	if observation.Availability != AvailabilityObserved {
		return check
	}
	values := append([]string(nil), observation.Values...)
	sort.Strings(values)
	check.Observation = "[" + joinComma(values) + "]"
	for _, value := range values {
		if value == required {
			check.Status = CheckPassed
			break
		}
	}
	return check
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func joinComma(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ","
		}
		result += value
	}
	return result
}
