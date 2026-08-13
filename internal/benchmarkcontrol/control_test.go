package benchmarkcontrol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	testRunID    = "benchmark-v2-test"
	testProtocol = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	t0           = "2026-08-12T00:00:00Z"
	t1           = "2026-08-12T00:00:01Z"
	t2           = "2026-08-12T00:00:02Z"
	t3           = "2026-08-12T00:00:03Z"
)

func TestControlEvidenceRoundTripsAndVerifiesRawSources(t *testing.T) {
	cache := validCache(t)
	if cache.Status != CacheStatusSatisfied || !CacheControlSatisfied(cache) {
		t.Fatalf("unexpected cache status: %#v", cache)
	}
	if err := VerifyCacheStateWithSource(cache, cacheSource()); err != nil {
		t.Fatalf("cache evidence did not verify from raw source: %v", err)
	}

	reset := validReset(t)
	if reset.Status != StatisticsStatusSucceeded || !StatisticsResetSatisfied(reset) {
		t.Fatalf("unexpected reset status: %#v", reset)
	}
	if err := VerifyStatisticsResetWithSource(reset, resetSource()); err != nil {
		t.Fatalf("statistics reset did not verify from raw source: %v", err)
	}

	overhead := validOverhead(t)
	if overhead.Status != OverheadStatusWithinBudget || !CollectorOverheadSatisfied(overhead) || len(overhead.Samples) != 3 {
		t.Fatalf("unexpected overhead status: %#v", overhead)
	}
	if err := VerifyCollectorOverheadWithSource(overhead, overheadSource()); err != nil {
		t.Fatalf("collector overhead did not verify from raw source: %v", err)
	}

	resource := validResource(t)
	if resource.Status != ResourceStatusEnforced || !ResourceBudgetSatisfied(resource) {
		t.Fatalf("unexpected resource status: %#v", resource)
	}
	if err := VerifyResourceBudgetWithSource(resource, resourceSource(t)); err != nil {
		t.Fatalf("resource budget did not verify from raw source: %v", err)
	}
}

func TestRawSourcesPreventCoordinatedNormalizedRewrites(t *testing.T) {
	cache := validCache(t)
	cache.Relations[0].ResidentBlocks = 1
	cache.Relations[0].ResidentPct = percent(1, cache.Relations[0].RelationBlocks)
	cache.Status = CacheStatusUnsatisfied
	cache.Reasons = []string{"cache-residency-below-minimum:public.accounts"}
	cache.Digest = mustDigest(t, cache)
	if err := VerifyCacheState(cache); err != nil {
		t.Fatalf("coherent normalized fixture should remain internally valid: %v", err)
	}
	if err := VerifyCacheStateWithSource(cache, cacheSource()); err == nil || !strings.Contains(err.Error(), "raw source") {
		t.Fatalf("normalized cache rewrite was not rejected against raw bytes: %v", err)
	}

	overhead := validOverhead(t)
	overhead.Samples[0].DurationNS++
	mean, maximum, status, reasons, err := deriveCollectorOverhead(overhead)
	if err != nil {
		t.Fatal(err)
	}
	overhead.ObservedMeanDutyPct, overhead.ObservedMaxDutyPct, overhead.Status, overhead.Reasons = mean, maximum, status, reasons
	overhead.Digest = mustDigest(t, overhead)
	if err := VerifyCollectorOverhead(overhead); err != nil {
		t.Fatalf("coherent overhead rewrite should remain internally valid: %v", err)
	}
	if err := VerifyCollectorOverheadWithSource(overhead, overheadSource()); err == nil || !strings.Contains(err.Error(), "raw source") {
		t.Fatalf("normalized overhead rewrite was not rejected against raw bytes: %v", err)
	}
}

func TestAdversarialBindingsWindowsDigestsAndStrictJSON(t *testing.T) {
	cache := validCache(t)
	want := CacheStateBinding(cache)
	for _, changed := range []Binding{
		{RunID: "other", ProtocolDigest: want.ProtocolDigest, Trial: want.Trial},
		{RunID: want.RunID, ProtocolDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Trial: want.Trial},
		{RunID: want.RunID, ProtocolDigest: want.ProtocolDigest, Trial: want.Trial + 1},
	} {
		if err := VerifyBinding(changed, want); err == nil {
			t.Fatalf("transplanted binding passed: %#v", changed)
		}
	}
	if err := VerifyBinding(want, want); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWindowWithin(BoundaryWindow{StartedAt: t1, FinishedAt: t2}, BoundaryWindow{StartedAt: t0, FinishedAt: t3}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWindowWithin(BoundaryWindow{StartedAt: t0, FinishedAt: t3}, BoundaryWindow{StartedAt: t1, FinishedAt: t2}); err == nil {
		t.Fatal("out-of-phase control window passed")
	}

	cache.Digest = strings.Replace(cache.Digest, "a", "b", 1)
	if err := VerifyCacheState(cache); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered digest passed: %v", err)
	}

	content, err := json.Marshal(validCache(t))
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(content, []byte(`{"schema_version"`), []byte(`{"unknown":true,"schema_version"`), 1)
	if _, err := ParseCacheState(unknown); err == nil {
		t.Fatal("unknown field passed strict parser")
	}
	duplicate := bytes.Replace(content, []byte(`{"schema_version"`), []byte(`{"schema_version":"duplicate","schema_version"`), 1)
	if _, err := ParseCacheState(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate key passed: %v", err)
	}
	if _, err := ParseCacheState(append(content, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON passed strict parser")
	}
}

func TestFailedControlObservationsRemainValidButUnsatisfied(t *testing.T) {
	resetRaw := strings.Replace(string(resetSource()), "timestamp-before\tcurrent-database\tnull", "timestamp-before\tcurrent-database\t"+t1, 1)
	reset, err := NewStatisticsResetFromSource(StatisticsResetInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t2, PostgresServerMajor: "17",
		Policy: StatisticsPolicyRunnerManaged, Boundary: "before-measure", BoundaryWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t2},
	}, []byte(resetRaw))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Status != StatisticsStatusFailed || StatisticsResetSatisfied(reset) {
		t.Fatalf("non-advancing reset was not a valid failed control: %#v", reset)
	}

	overheadRaw := strings.Replace(string(overheadSource()), "succeeded\n3", "failed\n3", 1)
	overhead, err := NewCollectorOverheadFromSource(CollectorOverheadInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t3, CalibrationWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t3},
		Mode: OverheadModeRunnerCalibrated, IntervalNS: 1_000_000_000, RequiredSamples: 2, MaxDutyCyclePct: floatPtr(1),
	}, []byte(overheadRaw))
	if err != nil {
		t.Fatal(err)
	}
	if overhead.Status != OverheadStatusInvalidSamples || CollectorOverheadSatisfied(overhead) {
		t.Fatalf("failed sample was not retained as failed control: %#v", overhead)
	}

	resourceSourceValue, err := ParseResourceBudgetSource(resourceSource(t))
	if err != nil {
		t.Fatal(err)
	}
	wrong := int64(1)
	resourceSourceValue.ObservedDockerNanoCPUs = &wrong
	content, err := MarshalResourceBudgetSource(resourceSourceValue)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := NewResourceBudgetFromSource(resourceInput(), content)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Status != ResourceStatusMismatch || ResourceBudgetSatisfied(resource) {
		t.Fatalf("mismatched Docker limits were not retained as failed control: %#v", resource)
	}
}

func TestCollectorOverheadRejectsSilentCadenceGaps(t *testing.T) {
	raw := strings.Replace(
		string(overheadSource()),
		"3\t"+t2+"\t"+t2+"\t"+t3,
		"3\t"+t3+"\t"+t3+"\t"+t3,
		1,
	)
	_, err := NewCollectorOverheadFromSource(CollectorOverheadInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t3,
		CalibrationWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t3},
		Mode:              OverheadModeRunnerCalibrated, IntervalNS: 1_000_000_000,
		RequiredSamples: 2, MaxDutyCyclePct: floatPtr(1),
	}, []byte(raw))
	if err == nil || !strings.Contains(err.Error(), "invalid collector overhead sample") {
		t.Fatalf("long scheduled-sample gap passed: %v", err)
	}
}

func TestCollectorOverheadRetainsShortAndEmptyCalibrationsAsInvalid(t *testing.T) {
	for name, raw := range map[string][]byte{
		"short": []byte(strings.Join([]string{
			"sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus",
			"1\t" + t0 + "\t" + t0 + "\t" + t1 + "\t1000000\tsucceeded",
			"",
		}, "\n")),
		"empty": []byte("sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus\n"),
	} {
		t.Run(name, func(t *testing.T) {
			artifact, err := NewCollectorOverheadFromSource(CollectorOverheadInput{
				RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t3,
				CalibrationWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t3},
				Mode:              OverheadModeRunnerCalibrated, IntervalNS: 1_000_000_000,
				RequiredSamples: 2, MaxDutyCyclePct: floatPtr(1),
			}, raw)
			if err != nil {
				t.Fatalf("short calibration was not retained: %v", err)
			}
			if artifact.Status != OverheadStatusInvalidSamples || CollectorOverheadSatisfied(artifact) || !slices.Contains(artifact.Reasons, "collector-sample-count-below-required") {
				t.Fatalf("short calibration did not fail satisfaction honestly: %#v", artifact)
			}
		})
	}
}

func TestStatisticsResetAcceptsPostgresFixedPrecisionUTC(t *testing.T) {
	raw := strings.ReplaceAll(string(resetSource()), t1, "2026-08-12T00:00:01.000000Z")
	artifact, err := NewStatisticsResetFromSource(StatisticsResetInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t2,
		PostgresServerMajor: "17", Policy: StatisticsPolicyRunnerManaged,
		Boundary: "before-measure", BoundaryWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t2},
	}, []byte(raw))
	if err != nil {
		t.Fatalf("valid PostgreSQL fixed-precision UTC timestamp was rejected: %v", err)
	}
	if artifact.Status != StatisticsStatusSucceeded {
		t.Fatalf("unexpected reset status: %s", artifact.Status)
	}
}

func TestRawTSVRejectsNonCanonicalEncodingAndRuntimeUnsupportedRelations(t *testing.T) {
	for name, raw := range map[string][]byte{
		"crlf":        bytes.ReplaceAll(cacheSource(), []byte("\n"), []byte("\r\n")),
		"quoted":      bytes.Replace(cacheSource(), []byte("public.accounts"), []byte(`"public.accounts"`), 1),
		"blank-line":  bytes.Replace(cacheSource(), []byte("public.branches"), []byte("\npublic.branches"), 1),
		"unqualified": bytes.Replace(cacheSource(), []byte("public.accounts"), []byte("accounts"), 1),
		"unicode":     bytes.Replace(cacheSource(), []byte("public.accounts"), []byte("public.аккаунты"), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseCacheStateSource(raw); err == nil {
				t.Fatalf("non-canonical cache source passed: %q", raw)
			}
		})
	}
}

func TestControlFilesAreNonReplacingAndRejectSymlinkedSources(t *testing.T) {
	directory := t.TempDir()
	cache := validCache(t)
	sourcePath := filepath.Join(directory, CacheStateSourceFile)
	artifactPath := filepath.Join(directory, CacheStateFile)
	if err := WriteRawSource(sourcePath, cacheSource()); err != nil {
		t.Fatal(err)
	}
	if err := WriteCacheState(artifactPath, cache); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCacheStateFile(artifactPath); err != nil {
		t.Fatal(err)
	}
	if err := WriteCacheState(artifactPath, cache); err == nil {
		t.Fatal("artifact overwrite was allowed")
	}

	other := t.TempDir()
	target := filepath.Join(other, "source.tsv")
	if err := os.WriteFile(target, cacheSource(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, sourcePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := VerifyCacheStateFile(artifactPath); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked source passed: %v", err)
	}
}

func TestCanonicalDigestOmitsOnlyDigestField(t *testing.T) {
	artifact := validCache(t)
	content, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "digest")
	canonical, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Digest != evidence.DigestBytes(canonical) {
		t.Fatalf("digest was not computed over every field except digest itself")
	}
	fields["digest"] = json.RawMessage(`""`)
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Digest == evidence.DigestBytes(legacy) {
		t.Fatal("digest unexpectedly includes an empty digest field")
	}
}

func validCache(t *testing.T) CacheState {
	t.Helper()
	artifact, err := NewCacheStateFromSource(CacheStateInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t2,
		BoundaryWindow: BoundaryWindow{StartedAt: t1, FinishedAt: t2}, Mode: CacheModeWarm,
		TargetRelations: []string{"public.accounts", "public.branches"}, MinResidentPct: floatPtr(50),
	}, cacheSource())
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func validReset(t *testing.T) StatisticsReset {
	t.Helper()
	artifact, err := NewStatisticsResetFromSource(StatisticsResetInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t2, PostgresServerMajor: "17",
		Policy: StatisticsPolicyRunnerManaged, Boundary: "before-measure", BoundaryWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t2},
	}, resetSource())
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func validOverhead(t *testing.T) CollectorOverhead {
	t.Helper()
	artifact, err := NewCollectorOverheadFromSource(CollectorOverheadInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t3,
		CalibrationWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t3}, Mode: OverheadModeRunnerCalibrated,
		IntervalNS: 1_000_000_000, RequiredSamples: 2, MaxDutyCyclePct: floatPtr(1),
	}, overheadSource())
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func validResource(t *testing.T) ResourceBudget {
	t.Helper()
	artifact, err := NewResourceBudgetFromSource(resourceInput(), resourceSource(t))
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func resourceInput() ResourceBudgetInput {
	return ResourceBudgetInput{
		RunID: testRunID, ProtocolDigest: testProtocol, Trial: 1, CapturedAt: t2,
		EnforcementWindow: BoundaryWindow{StartedAt: t0, FinishedAt: t2}, Mode: ResourceModeRunnerEnforced,
		Scope: ResourceScope, Provider: ResourceProvider, ProviderConstraints: ExpectedResourceProviderConstraints(),
		CPUMillicores: intPtr(1500), MemoryMiB: intPtr(1024),
	}
}

func cacheSource() []byte {
	return []byte(strings.Join([]string{
		"relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks",
		"public.accounts\t16384\t16390\tmain\t100\t90",
		"public.branches\t16384\t16391\tmain\t50\t40",
		"",
	}, "\n"))
}

func resetSource() []byte {
	return []byte(strings.Join([]string{
		"record\tscope\tvalue\trows\tcommand_completed",
		"timestamp-before\tcurrent-database\tnull\t\t",
		"timestamp-after\tcurrent-database\t" + t1 + "\t\t",
		"timestamp-before\tcluster-wal\tnull\t\t",
		"timestamp-after\tcluster-wal\t" + t1 + "\t\t",
		"operation\tcurrent-database\tpg_catalog.pg_stat_reset\t1\ttrue",
		"operation\tcluster-wal\tpg_catalog.pg_stat_reset_shared('wal')\t1\ttrue",
		"",
	}, "\n"))
}

func overheadSource() []byte {
	return []byte(strings.Join([]string{
		"sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus",
		"1\t" + t0 + "\t" + t0 + "\t" + t1 + "\t1000000\tsucceeded",
		"2\t" + t1 + "\t" + t1 + "\t" + t2 + "\t2000000\tsucceeded",
		"3\t" + t2 + "\t" + t2 + "\t" + t3 + "\t3000000\tsucceeded",
		"",
	}, "\n"))
}

func resourceSource(t *testing.T) []byte {
	t.Helper()
	digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	content, err := MarshalResourceBudgetSource(ResourceBudgetSource{
		Mode: ResourceModeRunnerEnforced, ObservedDockerNanoCPUs: int64Ptr(1_500_000_000), ObservedDockerMemoryBytes: int64Ptr(1024 * 1024 * 1024),
		CgroupVersion: "2", PostgresContainerIDDigest: digest, PgbenchContainerIDDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func mustDigest(t *testing.T, artifact any) string {
	t.Helper()
	digest, err := canonicalDigest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
func floatPtr(value float64) *float64 { return &value }
func intPtr(value int) *int           { return &value }
func int64Ptr(value int64) *int64     { return &value }
