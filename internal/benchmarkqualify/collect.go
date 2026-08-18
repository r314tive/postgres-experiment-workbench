package benchmarkqualify

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	defaultStorageLabel = "target-filesystem"
	maxPortableLabelLen = 64
)

type storageResult struct {
	filesystem     string
	totalBytes     uint64
	availableBytes uint64
}

type collectorProbes struct {
	goos     string
	goarch   string
	numCPU   func() int
	readFile func(string) ([]byte, error)
	glob     func(string) ([]string, error)
	command  func(string, ...string) ([]byte, error)
	storage  func(string) (storageResult, error)
}

func defaultCollectorProbes() collectorProbes {
	return collectorProbes{
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		numCPU:   runtime.NumCPU,
		readFile: os.ReadFile,
		glob:     filepath.Glob,
		command: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		},
		storage: collectStorage,
	}
}

func Inspect(options InspectOptions) (Artifact, error) {
	return inspectWith(options, defaultCollectorProbes())
}

func inspectWith(options InspectOptions, probes collectorProbes) (Artifact, error) {
	if probes.goos != "linux" && probes.goos != "darwin" {
		return Artifact{}, fmt.Errorf("host inspection is supported only on linux and darwin, got %q", probes.goos)
	}
	if options.RecordedAt.IsZero() {
		options.RecordedAt = time.Now().UTC()
	}
	options.RecordedAt = options.RecordedAt.UTC()
	if options.StoragePath == "" {
		options.StoragePath = "."
	}
	if options.StorageLabel == "" {
		options.StorageLabel = defaultStorageLabel
	}
	if !portableLabel(options.StorageLabel) {
		return Artifact{}, fmt.Errorf("storage label must be a portable label of at most %d characters", maxPortableLabelLen)
	}
	if options.ClientPlacement != "" && !validClientPlacement(options.ClientPlacement) {
		return Artifact{}, fmt.Errorf("client placement must be one of same-host, separate-host, or remote-host")
	}
	if err := validatePolicy(options.Policy); err != nil {
		return Artifact{}, err
	}

	snapshot := collectSnapshot(options, probes)
	checks, verdict, reasons := evaluate(snapshot, options.Policy)
	artifact := Artifact{
		SchemaVersion:    SchemaVersion,
		ArtifactType:     ArtifactType,
		CollectorVersion: CollectorVersion,
		RecordedAt:       options.RecordedAt.Format(time.RFC3339Nano),
		Assurance: Assurance{
			EvidenceOrigin:    EvidenceOriginOperatorRecorded,
			Signed:            false,
			DigestPurpose:     DigestPurposeIntegrityOnly,
			VerificationScope: VerificationScopeRecordedOnly,
		},
		Snapshot: snapshot,
		Policy:   options.Policy,
		Checks:   checks,
		Verdict:  verdict,
		Reasons:  reasons,
	}
	var err error
	artifact.SnapshotDigest, err = digestJSON(snapshot)
	if err != nil {
		return Artifact{}, fmt.Errorf("digest host snapshot: %w", err)
	}
	artifact.Digest, err = artifactDigest(artifact)
	if err != nil {
		return Artifact{}, fmt.Errorf("digest host qualification artifact: %w", err)
	}
	return artifact, nil
}

func collectSnapshot(options InspectOptions, probes collectorProbes) Snapshot {
	snapshot := Snapshot{
		Platform: PlatformSnapshot{
			OS:           observedString(probes.goos, "runtime"),
			Architecture: observedString(probes.goarch, "runtime"),
			Kernel:       unavailableString(platformSource(probes.goos)),
		},
		CPU: CPUSnapshot{
			Model:       unavailableString(platformSource(probes.goos)),
			LogicalCPUs: unavailableUint("runtime"),
		},
		Memory: unavailableCapacity(platformSource(probes.goos)),
		Storage: StorageSnapshot{
			Label:          options.StorageLabel,
			Filesystem:     unavailableString("statfs"),
			TotalBytes:     unavailableUint("statfs"),
			AvailableBytes: unavailableUint("statfs"),
			AvailablePct:   unavailableNumber("derived"),
		},
		Clock:   ClockSnapshot{Clocksource: unavailableString(platformSource(probes.goos))},
		Power:   PowerSnapshot{Governors: unavailableStringList(governorSource(probes.goos))},
		Thermal: ThermalSnapshot{MaxCelsius: unavailableNumber(thermalSource(probes.goos))},
		Client:  ClientSnapshot{Placement: unavailableString("operator")},
		Interference: InterferenceSnapshot{
			Load1:             unavailableNumber(platformSource(probes.goos)),
			Load1PerCPU:       unavailableNumber("derived"),
			RunnableProcesses: unavailableUint(platformSource(probes.goos)),
			ProcessCount:      unavailableUint(platformSource(probes.goos)),
		},
		Headroom: HeadroomSnapshot{
			LoadCapacityPct: unavailableNumber("derived"),
			MemoryPct:       unavailableNumber("derived"),
			StoragePct:      unavailableNumber("derived"),
		},
	}

	if count := probes.numCPU(); count > 0 {
		snapshot.CPU.LogicalCPUs = observedUint(uint64(count), "runtime")
	}
	if options.ClientPlacement != "" {
		snapshot.Client.Placement = observedString(options.ClientPlacement, "operator")
	}
	if storage, err := probes.storage(options.StoragePath); err == nil && storage.totalBytes > 0 && storage.availableBytes <= storage.totalBytes {
		snapshot.Storage.Filesystem = observedString(storage.filesystem, "statfs")
		snapshot.Storage.TotalBytes = observedUint(storage.totalBytes, "statfs")
		snapshot.Storage.AvailableBytes = observedUint(storage.availableBytes, "statfs")
		pct := percent(storage.availableBytes, storage.totalBytes)
		snapshot.Storage.AvailablePct = observedNumber(pct, "derived")
		snapshot.Headroom.StoragePct = observedNumber(pct, "derived")
	}

	if probes.goos == "linux" {
		collectLinux(&snapshot, probes)
	} else {
		collectDarwin(&snapshot, probes)
	}
	deriveSnapshot(&snapshot)
	return snapshot
}

func collectLinux(snapshot *Snapshot, probes collectorProbes) {
	if content, err := probes.readFile("/proc/sys/kernel/osrelease"); err == nil {
		if value := strings.TrimSpace(string(content)); value != "" {
			snapshot.Platform.Kernel = observedString(value, "procfs")
		}
	}
	if content, err := probes.readFile("/proc/cpuinfo"); err == nil {
		if value := linuxCPUModel(string(content)); value != "" {
			snapshot.CPU.Model = observedString(value, "procfs")
		}
	}
	if content, err := probes.readFile("/proc/meminfo"); err == nil {
		if total, available, ok := linuxMemory(string(content)); ok {
			snapshot.Memory = observedCapacity(total, available, "procfs")
		}
	}
	if content, err := probes.readFile("/proc/loadavg"); err == nil {
		load, runnable, processes, ok := linuxLoad(string(content))
		if ok {
			snapshot.Interference.Load1 = observedNumber(load, "procfs")
			snapshot.Interference.RunnableProcesses = observedUint(runnable, "procfs")
			snapshot.Interference.ProcessCount = observedUint(processes, "procfs")
		}
	}
	if content, err := probes.readFile("/sys/devices/system/clocksource/clocksource0/current_clocksource"); err == nil {
		if value := strings.TrimSpace(string(content)); value != "" {
			snapshot.Clock.Clocksource = observedString(value, "sysfs")
		}
	}
	if paths, err := probes.glob("/sys/devices/system/cpu/cpu*/cpufreq/scaling_governor"); err == nil {
		var values []string
		for _, path := range paths {
			content, readErr := probes.readFile(path)
			if readErr == nil {
				value := strings.TrimSpace(string(content))
				if portableToken(value) {
					values = append(values, value)
				}
			}
		}
		if values = uniqueSorted(values); len(values) > 0 {
			snapshot.Power.Governors = observedStringList(values, "sysfs")
		}
	}
	if paths, err := probes.glob("/sys/class/thermal/thermal_zone*/temp"); err == nil {
		var maximum *float64
		for _, path := range paths {
			content, readErr := probes.readFile(path)
			if readErr != nil {
				continue
			}
			value, parseErr := strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
			if parseErr != nil {
				continue
			}
			if math.Abs(value) > 1000 {
				value /= 1000
			}
			if !finite(value) || value < -100 || value > 250 {
				continue
			}
			if maximum == nil || value > *maximum {
				copy := value
				maximum = &copy
			}
		}
		if maximum != nil {
			snapshot.Thermal.MaxCelsius = observedNumber(*maximum, "sysfs")
		}
	}
}

func collectDarwin(snapshot *Snapshot, probes collectorProbes) {
	if value := commandString(probes, "sysctl", "-n", "kern.osrelease"); value != "" {
		snapshot.Platform.Kernel = observedString(value, "sysctl")
	}
	model := commandString(probes, "sysctl", "-n", "machdep.cpu.brand_string")
	if model == "" {
		model = commandString(probes, "sysctl", "-n", "hw.model")
	}
	if model != "" {
		snapshot.CPU.Model = observedString(model, "sysctl")
	}
	if value := commandString(probes, "sysctl", "-n", "hw.memsize"); value != "" {
		if total, err := strconv.ParseUint(value, 10, 64); err == nil && total > 0 {
			if output, commandErr := probes.command("vm_stat"); commandErr == nil {
				if available, ok := darwinAvailableMemory(string(output)); ok && available <= total {
					snapshot.Memory = CapacitySnapshot{
						TotalBytes:     observedUint(total, "sysctl"),
						AvailableBytes: observedUint(available, "vm-stat"),
						AvailablePct:   observedNumber(percent(available, total), "derived"),
					}
				}
			}
		}
	}
	if value := commandString(probes, "sysctl", "-n", "vm.loadavg"); value != "" {
		if load, ok := darwinLoad(value); ok {
			snapshot.Interference.Load1 = observedNumber(load, "sysctl")
		}
	}
	if value := commandString(probes, "sysctl", "-n", "kern.timecounter.hardware"); value != "" {
		snapshot.Clock.Clocksource = observedString(value, "sysctl")
	}
}

func deriveSnapshot(snapshot *Snapshot) {
	if snapshot.Memory.AvailablePct.Availability == AvailabilityObserved {
		snapshot.Headroom.MemoryPct = observedNumber(*snapshot.Memory.AvailablePct.Value, "derived")
	}
	if snapshot.CPU.LogicalCPUs.Availability == AvailabilityObserved && snapshot.Interference.Load1.Availability == AvailabilityObserved {
		perCPU := *snapshot.Interference.Load1.Value / float64(*snapshot.CPU.LogicalCPUs.Value)
		snapshot.Interference.Load1PerCPU = observedNumber(perCPU, "derived")
		headroom := math.Max(0, (1-perCPU)*100)
		snapshot.Headroom.LoadCapacityPct = observedNumber(headroom, "derived")
	}
}

func linuxCPUModel(content string) string {
	for _, key := range []string{"model name", "Model", "Processor", "Hardware"} {
		for _, line := range strings.Split(content, "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func linuxMemory(content string) (uint64, uint64, bool) {
	values := make(map[string]uint64)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value > math.MaxUint64/1024 {
			return 0, 0, false
		}
		values[key] = value * 1024
	}
	total, totalOK := values["MemTotal"]
	available, availableOK := values["MemAvailable"]
	return total, available, totalOK && availableOK && total > 0 && available <= total
}

func linuxLoad(content string) (float64, uint64, uint64, bool) {
	fields := strings.Fields(content)
	if len(fields) < 4 {
		return 0, 0, 0, false
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || !finite(load) || load < 0 {
		return 0, 0, 0, false
	}
	processes := strings.Split(fields[3], "/")
	if len(processes) != 2 {
		return 0, 0, 0, false
	}
	runnable, firstErr := strconv.ParseUint(processes[0], 10, 64)
	total, secondErr := strconv.ParseUint(processes[1], 10, 64)
	if firstErr != nil || secondErr != nil || runnable > total {
		return 0, 0, 0, false
	}
	return load, runnable, total, true
}

func darwinAvailableMemory(content string) (uint64, bool) {
	pageSize := uint64(4096)
	if start := strings.Index(content, "page size of "); start >= 0 {
		fragment := content[start+len("page size of "):]
		if end := strings.Index(fragment, " bytes"); end > 0 {
			if parsed, err := strconv.ParseUint(strings.TrimSpace(fragment[:end]), 10, 64); err == nil && parsed > 0 {
				pageSize = parsed
			}
		}
	}
	var pages uint64
	found := false
	for _, line := range strings.Split(content, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key != "Pages free" && key != "Pages inactive" && key != "Pages speculative" {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(parts[1], "."))
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed > math.MaxUint64-pages {
			return 0, false
		}
		pages += parsed
		found = true
	}
	if !found || pages > math.MaxUint64/pageSize {
		return 0, false
	}
	return pages * pageSize, true
}

func darwinLoad(content string) (float64, bool) {
	for _, field := range strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(content)) {
		value, err := strconv.ParseFloat(field, 64)
		if err == nil && finite(value) && value >= 0 {
			return value, true
		}
	}
	return 0, false
}

func commandString(probes collectorProbes, name string, args ...string) string {
	output, err := probes.command(name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func observedCapacity(total, available uint64, source string) CapacitySnapshot {
	return CapacitySnapshot{
		TotalBytes:     observedUint(total, source),
		AvailableBytes: observedUint(available, source),
		AvailablePct:   observedNumber(percent(available, total), "derived"),
	}
}

func unavailableCapacity(source string) CapacitySnapshot {
	return CapacitySnapshot{
		TotalBytes:     unavailableUint(source),
		AvailableBytes: unavailableUint(source),
		AvailablePct:   unavailableNumber("derived"),
	}
}

func percent(part, total uint64) float64 {
	return float64(part) / float64(total) * 100
}

func observedString(value, source string) StringObservation {
	return StringObservation{Availability: AvailabilityObserved, Value: value, Source: source}
}

func unavailableString(source string) StringObservation {
	return StringObservation{Availability: AvailabilityUnavailable, Source: source}
}

func observedStringList(values []string, source string) StringListObservation {
	return StringListObservation{Availability: AvailabilityObserved, Values: append([]string(nil), values...), Source: source}
}

func unavailableStringList(source string) StringListObservation {
	return StringListObservation{Availability: AvailabilityUnavailable, Source: source}
}

func observedUint(value uint64, source string) UintObservation {
	copy := value
	return UintObservation{Availability: AvailabilityObserved, Value: &copy, Source: source}
}

func unavailableUint(source string) UintObservation {
	return UintObservation{Availability: AvailabilityUnavailable, Source: source}
}

func observedNumber(value float64, source string) NumberObservation {
	copy := value
	return NumberObservation{Availability: AvailabilityObserved, Value: &copy, Source: source}
}

func unavailableNumber(source string) NumberObservation {
	return NumberObservation{Availability: AvailabilityUnavailable, Source: source}
}

func platformSource(goos string) string {
	if goos == "linux" {
		return "procfs"
	}
	return "sysctl"
}

func governorSource(goos string) string {
	if goos == "linux" {
		return "sysfs"
	}
	return "unsupported"
}

func thermalSource(goos string) string {
	if goos == "linux" {
		return "sysfs"
	}
	return "unsupported"
}

func portableLabel(value string) bool {
	if len(value) == 0 || len(value) > maxPortableLabelLen {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (index > 0 && (char == '-' || char == '_' || char == '.')) {
			continue
		}
		return false
	}
	return true
}

func portableToken(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._+-", char) {
			continue
		}
		return false
	}
	return true
}

func validClientPlacement(value string) bool {
	return value == "same-host" || value == "separate-host" || value == "remote-host"
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func digestJSON(value any) (string, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func artifactDigest(artifact Artifact) (string, error) {
	copy := artifact
	copy.Digest = ""
	return digestJSON(copy)
}

func validatePolicy(policy Policy) error {
	var problems []string
	if policy.MinLogicalCPUs != nil && *policy.MinLogicalCPUs == 0 {
		problems = append(problems, "min logical CPUs must be greater than zero")
	}
	for label, value := range map[string]*float64{
		"min memory available percent":  policy.MinMemoryAvailablePct,
		"min storage available percent": policy.MinStorageAvailablePct,
	} {
		if value != nil && (!finite(*value) || *value < 0 || *value > 100) {
			problems = append(problems, label+" must be between 0 and 100")
		}
	}
	if policy.MaxLoad1PerCPU != nil && (!finite(*policy.MaxLoad1PerCPU) || *policy.MaxLoad1PerCPU < 0) {
		problems = append(problems, "max 1-minute load per CPU must be non-negative")
	}
	if policy.RequiredClocksource != "" && !portableToken(policy.RequiredClocksource) {
		problems = append(problems, "required clocksource must be a portable token")
	}
	if policy.RequiredGovernor != "" && !portableToken(policy.RequiredGovernor) {
		problems = append(problems, "required governor must be a portable token")
	}
	if policy.MaxTemperatureCelsius != nil && (!finite(*policy.MaxTemperatureCelsius) || *policy.MaxTemperatureCelsius < -100 || *policy.MaxTemperatureCelsius > 250) {
		problems = append(problems, "max temperature Celsius must be between -100 and 250")
	}
	if policy.RequiredClientPlacement != "" && !validClientPlacement(policy.RequiredClientPlacement) {
		problems = append(problems, "required client placement must be one of same-host, separate-host, or remote-host")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
