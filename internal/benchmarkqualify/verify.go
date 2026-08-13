package benchmarkqualify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const maxArtifactBytes = 2 << 20

func Parse(content []byte) (Artifact, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var artifact Artifact
	if err := decoder.Decode(&artifact); err != nil {
		return Artifact{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Artifact{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return Artifact{}, err
	}
	return artifact, nil
}

func VerifyFile(path string) (Verification, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Verification{}, fmt.Errorf("inspect host qualification artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Verification{}, fmt.Errorf("host qualification artifact must be a regular non-symlink file")
	}
	if info.Size() > maxArtifactBytes {
		return Verification{}, fmt.Errorf("host qualification artifact exceeds %d bytes", maxArtifactBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Verification{}, fmt.Errorf("read host qualification artifact: %w", err)
	}
	artifact, err := Parse(content)
	if err != nil {
		return invalidVerification("parse artifact: %v", err), nil
	}
	return Verify(artifact), nil
}

func Verify(artifact Artifact) Verification {
	result := Verification{
		RecordedVerdict: artifact.Verdict,
		Assurance:       artifact.Assurance,
		Issues:          make([]string, 0),
	}
	add := func(format string, args ...any) {
		result.Issues = append(result.Issues, fmt.Sprintf(format, args...))
	}

	if artifact.SchemaVersion != SchemaVersion {
		add("schema_version = %q, want %q", artifact.SchemaVersion, SchemaVersion)
	}
	if artifact.ArtifactType != ArtifactType {
		add("artifact_type = %q, want %q", artifact.ArtifactType, ArtifactType)
	}
	if artifact.CollectorVersion != CollectorVersion {
		add("collector_version = %q, want %q", artifact.CollectorVersion, CollectorVersion)
	}
	validateRecordedAt(add, artifact.RecordedAt)
	if artifact.Assurance.EvidenceOrigin != EvidenceOriginOperatorRecorded || artifact.Assurance.Signed || artifact.Assurance.DigestPurpose != DigestPurposeIntegrityOnly || artifact.Assurance.VerificationScope != VerificationScopeRecordedOnly {
		add("assurance must declare operator-recorded unsigned integrity-only evidence with recorded-content-only verification")
	}
	validateSnapshot(add, artifact.Snapshot)
	if err := validatePolicy(artifact.Policy); err != nil {
		add("invalid policy: %v", err)
	}
	if artifact.Checks == nil {
		add("checks must be present as an array")
	}
	if artifact.Reasons == nil {
		add("reasons must be present as an array")
	}

	wantChecks, wantVerdict, wantReasons := evaluate(artifact.Snapshot, artifact.Policy)
	if !reflect.DeepEqual(artifact.Checks, wantChecks) {
		add("checks do not match independently recomputed policy evaluation")
	}
	if artifact.Verdict != wantVerdict {
		add("verdict = %q, want independently recomputed %q", artifact.Verdict, wantVerdict)
	}
	if !reflect.DeepEqual(artifact.Reasons, wantReasons) {
		add("reasons do not match independently recomputed policy evaluation")
	}

	if !evidence.IsDigest(artifact.SnapshotDigest) {
		add("snapshot_digest is not a lowercase sha256 digest")
	} else if digest, err := digestJSON(artifact.Snapshot); err != nil {
		add("cannot recompute snapshot digest: %v", err)
	} else if digest != artifact.SnapshotDigest {
		add("snapshot digest mismatch: got %s want %s", artifact.SnapshotDigest, digest)
	}
	if !evidence.IsDigest(artifact.Digest) {
		add("digest is not a lowercase sha256 digest")
	} else if digest, err := artifactDigest(artifact); err != nil {
		add("cannot recompute artifact digest: %v", err)
	} else if digest != artifact.Digest {
		add("artifact digest mismatch: got %s want %s", artifact.Digest, digest)
	}

	sort.Strings(result.Issues)
	result.Valid = len(result.Issues) == 0
	return result
}

func invalidVerification(format string, args ...any) Verification {
	return Verification{Issues: []string{fmt.Sprintf(format, args...)}}
}

func validateRecordedAt(add func(string, ...any), value string) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Format(time.RFC3339Nano) != value || !strings.HasSuffix(value, "Z") {
		add("recorded_at must be canonical UTC RFC3339Nano")
	}
}

func validateSnapshot(add func(string, ...any), snapshot Snapshot) {
	validateStringObservation(add, "snapshot.platform.os", snapshot.Platform.OS)
	validateStringObservation(add, "snapshot.platform.architecture", snapshot.Platform.Architecture)
	validateStringObservation(add, "snapshot.platform.kernel", snapshot.Platform.Kernel)
	if snapshot.Platform.OS.Availability != AvailabilityObserved || (snapshot.Platform.OS.Value != "linux" && snapshot.Platform.OS.Value != "darwin") {
		add("snapshot.platform.os must be observed linux or darwin")
	}
	if snapshot.Platform.Architecture.Availability != AvailabilityObserved || !portableToken(snapshot.Platform.Architecture.Value) {
		add("snapshot.platform.architecture must be an observed portable token")
	}

	validateStringObservation(add, "snapshot.cpu.model", snapshot.CPU.Model)
	validateUintObservation(add, "snapshot.cpu.logical_cpus", snapshot.CPU.LogicalCPUs)
	if snapshot.CPU.LogicalCPUs.Availability != AvailabilityObserved || snapshot.CPU.LogicalCPUs.Value == nil || *snapshot.CPU.LogicalCPUs.Value == 0 {
		add("snapshot.cpu.logical_cpus must be observed and greater than zero")
	}

	validateCapacity(add, "snapshot.memory", snapshot.Memory)
	if !portableLabel(snapshot.Storage.Label) {
		add("snapshot.storage.label must be a portable label")
	}
	validateStringObservation(add, "snapshot.storage.filesystem", snapshot.Storage.Filesystem)
	storageCapacity := CapacitySnapshot{TotalBytes: snapshot.Storage.TotalBytes, AvailableBytes: snapshot.Storage.AvailableBytes, AvailablePct: snapshot.Storage.AvailablePct}
	validateCapacity(add, "snapshot.storage", storageCapacity)
	if snapshot.Storage.TotalBytes.Availability != snapshot.Storage.Filesystem.Availability {
		add("snapshot.storage.filesystem availability must match storage capacity availability")
	}
	if snapshot.Storage.Filesystem.Availability == AvailabilityObserved && !portableToken(snapshot.Storage.Filesystem.Value) {
		add("snapshot.storage.filesystem must be a portable token")
	}

	validateStringObservation(add, "snapshot.clock.clocksource", snapshot.Clock.Clocksource)
	if snapshot.Clock.Clocksource.Availability == AvailabilityObserved && !portableToken(snapshot.Clock.Clocksource.Value) {
		add("snapshot.clock.clocksource must be a portable token")
	}
	validateStringListObservation(add, "snapshot.power.governors", snapshot.Power.Governors)
	validateNumberObservation(add, "snapshot.thermal.max_celsius", snapshot.Thermal.MaxCelsius)
	if snapshot.Thermal.MaxCelsius.Availability == AvailabilityObserved && !observedNumberValue(snapshot.Thermal.MaxCelsius, func(value float64) bool { return value >= -100 && value <= 250 }) {
		add("snapshot.thermal.max_celsius must be between -100 and 250")
	}

	validateStringObservation(add, "snapshot.client.placement", snapshot.Client.Placement)
	if snapshot.Client.Placement.Source != "operator" {
		add("snapshot.client.placement source must be operator")
	}
	if snapshot.Client.Placement.Availability == AvailabilityObserved && !validClientPlacement(snapshot.Client.Placement.Value) {
		add("snapshot.client.placement has an unsupported value")
	}

	validateNumberObservation(add, "snapshot.interference.load_1m", snapshot.Interference.Load1)
	validateNumberObservation(add, "snapshot.interference.load_1m_per_cpu", snapshot.Interference.Load1PerCPU)
	validateUintObservation(add, "snapshot.interference.runnable_processes", snapshot.Interference.RunnableProcesses)
	validateUintObservation(add, "snapshot.interference.process_count", snapshot.Interference.ProcessCount)
	if snapshot.Interference.Load1.Availability == AvailabilityObserved && snapshot.Interference.Load1.Value != nil && *snapshot.Interference.Load1.Value < 0 {
		add("snapshot.interference.load_1m must be non-negative")
	}
	if snapshot.Interference.Load1PerCPU.Availability == AvailabilityObserved && snapshot.Interference.Load1PerCPU.Value != nil && *snapshot.Interference.Load1PerCPU.Value < 0 {
		add("snapshot.interference.load_1m_per_cpu must be non-negative")
	}
	if snapshot.Interference.RunnableProcesses.Availability == AvailabilityObserved && snapshot.Interference.RunnableProcesses.Value != nil && snapshot.Interference.ProcessCount.Availability == AvailabilityObserved && snapshot.Interference.ProcessCount.Value != nil && *snapshot.Interference.RunnableProcesses.Value > *snapshot.Interference.ProcessCount.Value {
		add("snapshot.interference.runnable_processes exceeds process_count")
	}

	validateNumberObservation(add, "snapshot.headroom.load_capacity_pct", snapshot.Headroom.LoadCapacityPct)
	validateNumberObservation(add, "snapshot.headroom.memory_available_pct", snapshot.Headroom.MemoryPct)
	validateNumberObservation(add, "snapshot.headroom.storage_available_pct", snapshot.Headroom.StoragePct)
	validatePercentage(add, "snapshot.headroom.load_capacity_pct", snapshot.Headroom.LoadCapacityPct)
	validatePercentage(add, "snapshot.headroom.memory_available_pct", snapshot.Headroom.MemoryPct)
	validatePercentage(add, "snapshot.headroom.storage_available_pct", snapshot.Headroom.StoragePct)

	validateDerivedSnapshot(add, snapshot)
}

func validateCapacity(add func(string, ...any), path string, value CapacitySnapshot) {
	validateUintObservation(add, path+".total_bytes", value.TotalBytes)
	validateUintObservation(add, path+".available_bytes", value.AvailableBytes)
	validateNumberObservation(add, path+".available_pct", value.AvailablePct)
	if value.TotalBytes.Availability != value.AvailableBytes.Availability || value.TotalBytes.Availability != value.AvailablePct.Availability {
		add("%s capacity observations must have matching availability", path)
		return
	}
	if value.TotalBytes.Availability != AvailabilityObserved {
		return
	}
	if value.TotalBytes.Value == nil || value.AvailableBytes.Value == nil || value.AvailablePct.Value == nil {
		return
	}
	if *value.TotalBytes.Value == 0 || *value.AvailableBytes.Value > *value.TotalBytes.Value {
		add("%s capacity bytes are inconsistent", path)
		return
	}
	want := percent(*value.AvailableBytes.Value, *value.TotalBytes.Value)
	if !almostEqual(*value.AvailablePct.Value, want) {
		add("%s.available_pct does not match capacity bytes", path)
	}
	validatePercentage(add, path+".available_pct", value.AvailablePct)
}

func validateDerivedSnapshot(add func(string, ...any), snapshot Snapshot) {
	checkDerivedNumber(add, "snapshot.headroom.memory_available_pct", snapshot.Headroom.MemoryPct, snapshot.Memory.AvailablePct)
	checkDerivedNumber(add, "snapshot.headroom.storage_available_pct", snapshot.Headroom.StoragePct, snapshot.Storage.AvailablePct)

	inputsObserved := snapshot.CPU.LogicalCPUs.Availability == AvailabilityObserved && snapshot.CPU.LogicalCPUs.Value != nil && *snapshot.CPU.LogicalCPUs.Value > 0 && snapshot.Interference.Load1.Availability == AvailabilityObserved && snapshot.Interference.Load1.Value != nil
	if !inputsObserved {
		if snapshot.Interference.Load1PerCPU.Availability != AvailabilityUnavailable || snapshot.Headroom.LoadCapacityPct.Availability != AvailabilityUnavailable {
			add("derived load observations must be unavailable when their inputs are unavailable")
		}
		return
	}
	wantPerCPU := *snapshot.Interference.Load1.Value / float64(*snapshot.CPU.LogicalCPUs.Value)
	if snapshot.Interference.Load1PerCPU.Availability != AvailabilityObserved || snapshot.Interference.Load1PerCPU.Value == nil || !almostEqual(*snapshot.Interference.Load1PerCPU.Value, wantPerCPU) {
		add("snapshot.interference.load_1m_per_cpu does not match load and logical CPUs")
	}
	wantHeadroom := math.Max(0, (1-wantPerCPU)*100)
	if snapshot.Headroom.LoadCapacityPct.Availability != AvailabilityObserved || snapshot.Headroom.LoadCapacityPct.Value == nil || !almostEqual(*snapshot.Headroom.LoadCapacityPct.Value, wantHeadroom) {
		add("snapshot.headroom.load_capacity_pct does not match normalized load")
	}
}

func checkDerivedNumber(add func(string, ...any), path string, derived, source NumberObservation) {
	if source.Availability == AvailabilityUnavailable {
		if derived.Availability != AvailabilityUnavailable {
			add("%s must be unavailable when its source is unavailable", path)
		}
		return
	}
	if derived.Availability != AvailabilityObserved || derived.Value == nil || source.Value == nil || !almostEqual(*derived.Value, *source.Value) {
		add("%s does not match its source observation", path)
	}
}

func validateStringObservation(add func(string, ...any), path string, value StringObservation) {
	validateAvailabilityAndSource(add, path, value.Availability, value.Source)
	if value.Availability == AvailabilityObserved {
		if value.Value == "" || len(value.Value) > 512 || strings.IndexFunc(value.Value, func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0 {
			add("%s observed value must be non-empty printable text", path)
		}
	} else if value.Availability == AvailabilityUnavailable && value.Value != "" {
		add("%s unavailable observation must omit value", path)
	}
}

func validateStringListObservation(add func(string, ...any), path string, value StringListObservation) {
	validateAvailabilityAndSource(add, path, value.Availability, value.Source)
	if value.Availability == AvailabilityUnavailable {
		if len(value.Values) != 0 {
			add("%s unavailable observation must omit values", path)
		}
		return
	}
	if value.Availability != AvailabilityObserved {
		return
	}
	if len(value.Values) == 0 {
		add("%s observed values must not be empty", path)
		return
	}
	for index, item := range value.Values {
		if !portableToken(item) {
			add("%s value %q is not a portable token", path, item)
		}
		if index > 0 && value.Values[index-1] >= item {
			add("%s values must be sorted and unique", path)
			break
		}
	}
}

func validateUintObservation(add func(string, ...any), path string, value UintObservation) {
	validateAvailabilityAndSource(add, path, value.Availability, value.Source)
	if value.Availability == AvailabilityObserved && value.Value == nil {
		add("%s observed value is missing", path)
	}
	if value.Availability == AvailabilityUnavailable && value.Value != nil {
		add("%s unavailable observation must omit value", path)
	}
}

func validateNumberObservation(add func(string, ...any), path string, value NumberObservation) {
	validateAvailabilityAndSource(add, path, value.Availability, value.Source)
	if value.Availability == AvailabilityObserved {
		if value.Value == nil || !finite(*value.Value) {
			add("%s observed value must be finite", path)
		}
	} else if value.Availability == AvailabilityUnavailable && value.Value != nil {
		add("%s unavailable observation must omit value", path)
	}
}

func validateAvailabilityAndSource(add func(string, ...any), path, availability, source string) {
	if availability != AvailabilityObserved && availability != AvailabilityUnavailable {
		add("%s availability = %q", path, availability)
	}
	if !portableToken(source) {
		add("%s source must be a portable token", path)
	}
}

func validatePercentage(add func(string, ...any), path string, value NumberObservation) {
	if value.Availability == AvailabilityObserved && value.Value != nil && (*value.Value < 0 || *value.Value > 100) {
		add("%s must be between 0 and 100", path)
	}
}

func observedNumberValue(value NumberObservation, predicate func(float64) bool) bool {
	return value.Availability == AvailabilityObserved && value.Value != nil && predicate(*value.Value)
}

func almostEqual(left, right float64) bool {
	delta := math.Abs(left - right)
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return delta <= 1e-12*scale
}
