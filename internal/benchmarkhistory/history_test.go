package benchmarkhistory

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
)

func TestDeriveBuildsChronologicalDescriptiveHistory(t *testing.T) {
	root := t.TempDir()
	later := historySeries(t, root, "series-b", "candidate", "default", 110, "2026-08-12T01:00:00Z")
	earlier := historySeries(t, root, "series-a", "baseline", "default", 100, "2026-08-12T00:00:00Z")

	result, err := derive("nightly-history", time.Date(2026, 8, 12, 2, 0, 0, 0, time.UTC), []benchmarkrun.Series{later, earlier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" || result.Conclusion != "descriptive" || len(result.Entries) != 2 || result.Entries[0].RunID != "series-a" || result.Entries[1].Median != 110 {
		t.Fatalf("unexpected history: %#v", result)
	}
	wantPopulation, err := environmentPopulationDigest(*earlier.Environment)
	if err != nil {
		t.Fatal(err)
	}
	if result.EnvironmentDigest != wantPopulation || result.Entries[0].EnvironmentDigest != earlier.Environment.Digest || result.Entries[1].EnvironmentDigest != later.Environment.Digest {
		t.Fatalf("history is not bound to one canonical environment population: %#v", result)
	}
	if result.Entries[0].ChangeFromPreviousPct != nil || result.Entries[0].ChangeFromFirstPct != nil || result.Entries[1].ChangeFromPreviousPct == nil || math.Abs(*result.Entries[1].ChangeFromPreviousPct-10) > 1e-12 || result.Entries[1].ChangeFromFirstPct == nil || math.Abs(*result.Entries[1].ChangeFromFirstPct-10) > 1e-12 {
		t.Fatalf("descriptive history deltas are wrong: %#v", result.Entries)
	}
	if result.Digest == "" || !strings.Contains(string(summaryBytes(result)), "does not establish causality") {
		t.Fatalf("history assurance boundary is incomplete: %#v", result)
	}
	if digest, err := resultDigest(result); err != nil || digest != result.Digest {
		t.Fatalf("history digest is not reproducible: %s %v", digest, err)
	}
}

func TestNormalizedHistoryChangeRespectsMetricDirection(t *testing.T) {
	if got := normalizedChange(100, 90, "higher"); math.Abs(got+10) > 1e-12 {
		t.Fatalf("higher-is-better change = %g, want -10", got)
	}
	if got := normalizedChange(100, 90, "lower"); math.Abs(got-10) > 1e-12 {
		t.Fatalf("lower-is-better change = %g, want 10", got)
	}
}

func TestDeriveRejectsMixedOrOverlappingPopulations(t *testing.T) {
	root := t.TempDir()
	left := historySeries(t, root, "series-a", "baseline", "default", 100, "2026-08-12T00:00:00Z")
	right := historySeries(t, root, "series-b", "candidate", "default", 110, "2026-08-12T01:00:00Z")

	mixed := right
	mixed.Runtime = "native"
	if _, err := derive("mixed", time.Now(), []benchmarkrun.Series{left, mixed}); err == nil || !strings.Contains(err.Error(), "outside the history comparison population") {
		t.Fatalf("mixed runtime was accepted: %v", err)
	}

	driftedEnvironment := right
	driftedEnvironment.Environment = cloneHistoryEnvironment(right.Environment)
	driftedEnvironment.Environment.PGConfig = "tuned"
	driftedEnvironment.Environment.PGConfigDigest = historyDigest("5")
	driftedEnvironment.Environment.Digest = historyDigest("6")
	if _, err := derive("environment-drift", time.Now(), []benchmarkrun.Series{left, driftedEnvironment}); err == nil || !strings.Contains(err.Error(), "outside the exact history environment population") || !strings.Contains(err.Error(), "population digest") {
		t.Fatalf("mixed environment population was accepted or had an unclear diagnostic: %v", err)
	}

	overlap := right
	overlap.Trials[0].RunID = left.Trials[0].RunID
	if _, err := derive("overlap", time.Now(), []benchmarkrun.Series{left, overlap}); err == nil || !strings.Contains(err.Error(), "share linked trial run") {
		t.Fatalf("overlapping population was accepted: %v", err)
	}
}

func TestEnvironmentPopulationNormalizesOnlyNativeSnapshotLocation(t *testing.T) {
	left := benchmarkrun.Environment{
		Digest: historyDigest("1"), Runtime: "native", NativeToolchainDigest: historyDigest("2"),
		NativeToolchainManifestRef: "runs/benchmarks/left/protocol/native-toolchain/manifest.json",
		EngineBinaryDigest:         historyDigest("3"), DockerDriverImageID: "not-applicable", DockerTargetImageID: "not-applicable",
	}
	right := left
	right.Digest = historyDigest("4")
	right.NativeToolchainManifestRef = "runs/benchmarks/right/protocol/native-toolchain/manifest.json"
	leftPopulation, err := environmentPopulationDigest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightPopulation, err := environmentPopulationDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftPopulation != rightPopulation {
		t.Fatalf("series-local snapshot location split one byte-identical native population: %s != %s", leftPopulation, rightPopulation)
	}
	right.EngineBinaryDigest = historyDigest("5")
	rightPopulation, err = environmentPopulationDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftPopulation == rightPopulation {
		t.Fatal("engine byte drift was normalized out of the history population")
	}
}

func TestDeriveUsesTimeOrderingAndFullInterval(t *testing.T) {
	root := t.TempDir()
	// RFC3339Nano permits an omitted fractional part. Lexicographic ordering
	// would incorrectly put 00:00:00.1Z before 00:00:00Z.
	earlier := historySeries(t, root, "series-a", "baseline", "default", 100, "2026-08-12T00:00:00Z")
	earlier.FinishedAt = "2026-08-12T00:30:00Z"
	later := historySeries(t, root, "series-b", "candidate", "default", 110, "2026-08-12T00:00:00.1Z")
	later.FinishedAt = "2026-08-12T00:10:00Z"

	result, err := derive("precise-history", time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC), []benchmarkrun.Series{later, earlier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Entries[0].RunID != "series-a" || result.FinishedAt != earlier.FinishedAt {
		t.Fatalf("history interval/order is not temporal: %#v", result)
	}
}

func TestDeriveRejectsInvalidIntervalsAndPredatedPublication(t *testing.T) {
	root := t.TempDir()
	left := historySeries(t, root, "series-a", "baseline", "default", 100, "2026-08-12T00:00:00Z")
	right := historySeries(t, root, "series-b", "candidate", "default", 110, "2026-08-12T01:00:00Z")

	backwards := right
	backwards.FinishedAt = "2026-08-11T23:00:00Z"
	if _, err := derive("backwards", time.Date(2026, 8, 12, 2, 0, 0, 0, time.UTC), []benchmarkrun.Series{left, backwards}); err == nil || !strings.Contains(err.Error(), "finishes before") {
		t.Fatalf("backwards interval was accepted: %v", err)
	}
	if _, err := derive("predated", time.Date(2026, 8, 12, 1, 10, 0, 0, time.UTC), []benchmarkrun.Series{left, right}); err == nil || !strings.Contains(err.Error(), "generation time precedes") {
		t.Fatalf("predated publication was accepted: %v", err)
	}
}

func TestDecodeStrictRejectsUnknownHistoryField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"x","unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := decodeStrict(path, &result); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown JSON field was accepted: %v", err)
	}
}

func TestBundleInventoryFailsClosedOnTamperAndExtras(t *testing.T) {
	recorded := []BundleFile{
		{Path: "runs/a/result.json", Size: 10, Digest: historyDigest("1")},
		{Path: "runs/b/result.json", Size: 20, Digest: historyDigest("2")},
	}
	if issues := compareBundleFiles(recorded, append([]BundleFile(nil), recorded...)); len(issues) != 0 {
		t.Fatalf("matching inventory failed: %v", issues)
	}
	tests := []struct {
		name     string
		recorded []BundleFile
		actual   []BundleFile
		want     string
	}{
		{"tampered", recorded, []BundleFile{{Path: "runs/a/result.json", Size: 11, Digest: historyDigest("3")}, recorded[1]}, "digest or size mismatch"},
		{"extra", recorded, append(append([]BundleFile(nil), recorded...), BundleFile{Path: "runs/c/result.json", Size: 1, Digest: historyDigest("3")}), "missing file"},
		{"missing", recorded, recorded[:1], "references missing file"},
		{"unsorted", []BundleFile{recorded[1], recorded[0]}, recorded, "not sorted"},
		{"duplicate", []BundleFile{recorded[0], recorded[0]}, recorded, "duplicate path"},
		{"traversal", []BundleFile{{Path: "../outside", Size: 1, Digest: historyDigest("3")}}, recorded, "invalid entry"},
		{"bad digest", []BundleFile{{Path: "runs/a/result.json", Size: 10, Digest: "SHA256:nope"}}, recorded, "invalid entry"},
		{"negative size", []BundleFile{{Path: "runs/a/result.json", Size: -1, Digest: historyDigest("1")}}, recorded, "invalid entry"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := compareBundleFiles(test.recorded, test.actual)
			if !historyIssueContains(issues, test.want) {
				t.Fatalf("inventory mutation did not report %q: %v", test.want, issues)
			}
		})
	}
}

func TestBundleCopyRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, filepath.Join(root, "destination")); err == nil {
		t.Fatal("bundle copied a symlink")
	}
}

func TestStagedHistoryBundleVerificationRejectsDirectSemanticCorruption(t *testing.T) {
	stage := t.TempDir()
	historyID := "corrupt-history"
	historyDir := filepath.Join(stage, "runs", "benchmark-history", historyID)
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "result.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(historyDir, "summary.md"), []byte("# forged history\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _, err := bundleFiles(stage)
	if err != nil {
		t.Fatal(err)
	}
	inventory := BundleInventory{
		SchemaVersion: BundleSchemaVersion,
		ArtifactType:  BundleArtifactType,
		HistoryID:     historyID,
		HistoryRef:    "runs/benchmark-history/" + historyID,
		SeriesRefs:    []string{},
		Files:         files,
	}
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, BundleInventoryName), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	verification, err := VerifyBundle(stage, historyDir)
	if err != nil {
		t.Fatal(err)
	}
	if verification.IsValid() {
		t.Fatal("semantically corrupt staged history bundle verified")
	}
}

func TestHistoryBundleOutputRejectsDirectChildAndAliasedParent(t *testing.T) {
	source := t.TempDir()
	alias := filepath.Join(t.TempDir(), "history-source-alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tests := []struct {
		name   string
		output string
	}{
		{name: "direct child", output: filepath.Join(source, "direct.tar.gz")},
		{name: "aliased parent", output: filepath.Join(alias, "aliased.tar.gz")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveHistoryBundleOutput(source, test.output, "test source")
			if !errors.Is(err, pathguard.ErrOutputWithinSource) {
				t.Fatalf("expected output-containment error, got %v", err)
			}
		})
	}
}

func historySeries(t *testing.T, root, runID, subject, config string, median float64, started string) benchmarkrun.Series {
	t.Helper()
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte("evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cv := 1.0
	return benchmarkrun.Series{
		Benchmark:           "pgbench/read-only",
		Class:               "measurement",
		Subject:             subject,
		RunID:               runID,
		ArtifactDir:         dir,
		ProtocolDigest:      historyDigest("1"),
		ComparisonKeyDigest: historyDigest("2"),
		Runtime:             "docker",
		EvidenceClass:       "unqualified-local-measurement",
		PrimaryMetric:       "pgbench.tps",
		Direction:           "higher",
		StartedAt:           started,
		FinishedAt:          strings.Replace(started, ":00:00Z", ":30:00Z", 1),
		Status:              "passed",
		TrialsValid:         5,
		Stats:               &pgbenchresult.TrialStats{N: 5, Median: median, CVPct: &cv},
		Environment: &benchmarkrun.Environment{
			Digest:             historyDigest("3"),
			PGConfig:           config,
			PGConfigDigest:     historyDigest("4"),
			EngineBinaryDigest: historyDigest("5"),
		},
		Trials: []benchmarkrun.Trial{{RunID: runID + "-t001"}},
	}
}

func historyDigest(character string) string { return "sha256:" + strings.Repeat(character, 64) }

func cloneHistoryEnvironment(environment *benchmarkrun.Environment) *benchmarkrun.Environment {
	if environment == nil {
		return nil
	}
	copy := *environment
	return &copy
}

func historyIssueContains(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return true
		}
	}
	return false
}
