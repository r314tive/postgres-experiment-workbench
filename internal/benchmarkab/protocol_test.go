package benchmarkab

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcompare"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkqualify"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksettings"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
)

func TestBuildProtocolPredeclaresAlternatingCounterbalance(t *testing.T) {
	baseline, candidate := testPlanPairWithConfigs(t)
	options := testOptions()
	protocol, err := BuildProtocol("ab-contract", "native", "default", "candidate", baseline, candidate, options)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.BlocksPlanned != 10 || protocol.MinValidUnits != benchmarkcompare.MinimumPairedUnits {
		t.Fatalf("unexpected population contract: %#v", protocol)
	}
	wantOrders := []string{"AB", "BA", "AB", "BA", "AB", "BA", "AB", "BA", "AB", "BA"}
	if !reflect.DeepEqual(protocol.Orders, wantOrders) {
		t.Fatalf("order was not predeclared deterministically: %v", protocol.Orders)
	}
	if protocol.Baseline.PGConfig != "default" || protocol.Candidate.PGConfig != "candidate" || protocol.Qualification.PolicyDigest == "" || protocol.Digest == "" {
		t.Fatalf("protocol identities are incomplete: %#v", protocol)
	}
	if err := VerifyProtocol(protocol); err != nil {
		t.Fatalf("built protocol does not independently verify: %v", err)
	}
	copy := protocol
	copy.Orders[0] = "BA"
	if err := VerifyProtocol(copy); err == nil || !strings.Contains(err.Error(), "order 1") {
		t.Fatalf("tampered order passed: %v", err)
	}
}

func TestBuildProtocolRejectsUncontrolledDecisionInputs(t *testing.T) {
	baseline, candidate := testPlanPairWithConfigs(t)
	tests := []struct {
		name   string
		mutate func(*benchmarkplan.Plan, *benchmarkplan.Plan, *Options)
	}{
		{"incomplete host policy", func(_, _ *benchmarkplan.Plan, options *Options) { options.Qualification.Policy.RequiredGovernor = "" }},
		{"same configuration", func(_, candidate *benchmarkplan.Plan, _ *Options) {
			candidate.PGConfig = baseline.PGConfig
			candidate.PGConfigDigest = baseline.PGConfigDigest
		}},
		{"odd trials", func(baseline, candidate *benchmarkplan.Plan, _ *Options) { baseline.Trials, candidate.Trials = 11, 11 }},
		{"missing threshold", func(baseline, candidate *benchmarkplan.Plan, _ *Options) {
			baseline.RegressionThresholdPct, candidate.RegressionThresholdPct = nil, nil
		}},
		{"different comparison key", func(_, candidate *benchmarkplan.Plan, _ *Options) { candidate.ComparisonKeyDigest = testDigest("9") }},
		{"implicit placement", func(_, _ *benchmarkplan.Plan, options *Options) { options.Qualification.ClientPlacement = "" }},
		{"plan placement differs from qualification", func(baseline, candidate *benchmarkplan.Plan, _ *Options) {
			baseline.ClientPlacement, candidate.ClientPlacement = "remote-host", "remote-host"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, right := baseline, candidate
			options := testOptions()
			test.mutate(&left, &right, &options)
			if _, err := BuildProtocol("ab-invalid", "native", "baseline", "candidate", left, right, options); err == nil {
				t.Fatal("invalid A/B protocol input was accepted")
			}
		})
	}
}

func TestBuildProtocolRejectsCommentOnlyConfigurationDifference(t *testing.T) {
	baseline, candidate := testPlanPair()
	root := t.TempDir()
	baseline.PGConfigPath = filepath.Join(root, "baseline.conf")
	candidate.PGConfigPath = filepath.Join(root, "candidate.conf")
	if err := os.WriteFile(baseline.PGConfigPath, []byte("# baseline comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate.PGConfigPath, []byte("# candidate comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildProtocol("ab-comments", "native", "baseline", "candidate", baseline, candidate, testOptions()); err == nil || !strings.Contains(err.Error(), "requires between 1") {
		t.Fatalf("comment-only configuration difference was accepted: %v", err)
	}
}

func TestEffectiveSettingsGateRejectsEquivalentDriftPendingAndVersionMismatch(t *testing.T) {
	baselinePlan, candidatePlan := testPlanPairWithConfigs(t)
	protocol, err := BuildProtocol("ab-effective", "native", "baseline", "candidate", baselinePlan, candidatePlan, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	makeSeries := func(role string, setting string, unit string) benchmarkrun.Series {
		series := testSeries("ab-effective-"+role, 100, 10)
		for index := range series.Trials {
			series.Trials[index].EffectiveSettings = syntheticEffectiveSettings(t, protocol, series.Trials[index], setting, unit, "configuration file", "170009", false)
		}
		return series
	}

	baseline := makeSeries("a", "16384", "8kB")
	candidate := makeSeries("b", "16384", "8kB")
	assessment := assessEffectiveSettings(protocol, baseline, candidate)
	if assessment.Status != "equivalent" || !reasonContains(assessment.Reasons, "no effective value-and-unit") {
		t.Fatalf("equivalent normalized values were decision-qualified: %#v", assessment)
	}
	for index := range candidate.Trials {
		candidate.Trials[index].EffectiveSettings = syntheticEffectiveSettings(t, protocol, candidate.Trials[index], "16384", "8kB", "default", "170009", false)
	}
	if got := assessEffectiveSettings(protocol, baseline, candidate); got.Status != "equivalent" {
		t.Fatalf("source-only differences were counted as effective: %#v", got)
	}
	qualified := benchmarkqualify.BookendAssessment{Status: benchmarkqualify.BookendStatusRecordedPolicyPassed, Reasons: []string{}}
	blocks := DeriveBlocks(protocol, baseline, candidate)
	analysis := benchmarkcompare.AnalyzePaired(pairedUnits(blocks), pairedOptions(protocol))
	status, decision, reasons := deriveTerminal(protocol, baseline, candidate, blocks, qualified, assessment, analysis)
	if status != "inconclusive" || decision != "inconclusive" || !reasonContains(reasons, "effective pg_settings") {
		t.Fatalf("equivalent effective settings yielded a verdict: %s %s %v", status, decision, reasons)
	}

	candidate = makeSeries("b", "32768", "8kB")
	assessment = assessEffectiveSettings(protocol, baseline, candidate)
	if assessment.Status != "verified-different" || !reflect.DeepEqual(assessment.EffectiveDifferences, []string{"shared_buffers"}) {
		t.Fatalf("real effective difference was not qualified: %#v", assessment)
	}

	drifted := candidate
	drifted.Trials = append([]benchmarkrun.Trial(nil), candidate.Trials...)
	drifted.Trials[4].EffectiveSettings = syntheticEffectiveSettings(t, protocol, drifted.Trials[4], "65536", "8kB", "configuration file", "170009", false)
	if got := assessEffectiveSettings(protocol, baseline, drifted); got.Status != "unstable" || !reasonContains(got.Reasons, "drifted") {
		t.Fatalf("within-arm drift was decision-qualified: %#v", got)
	}

	pending := candidate
	pending.Trials = append([]benchmarkrun.Trial(nil), candidate.Trials...)
	pending.Trials[0].EffectiveSettings = syntheticEffectiveSettings(t, protocol, pending.Trials[0], "32768", "8kB", "configuration file", "170009", true)
	if got := assessEffectiveSettings(protocol, baseline, pending); got.Status != "unstable" || !reasonContains(got.Reasons, "pending restart") {
		t.Fatalf("pending-restart setting was decision-qualified: %#v", got)
	}

	versionDrift := candidate
	versionDrift.Trials = append([]benchmarkrun.Trial(nil), candidate.Trials...)
	versionDrift.Trials[0].EffectiveSettings = syntheticEffectiveSettings(t, protocol, versionDrift.Trials[0], "32768", "8kB", "configuration file", "180004", false)
	if got := assessEffectiveSettings(protocol, baseline, versionDrift); got.Status != "unstable" || !reasonContains(got.Reasons, "server version") {
		t.Fatalf("server-version drift was decision-qualified: %#v", got)
	}

	missing := candidate
	missing.Trials = append([]benchmarkrun.Trial(nil), candidate.Trials...)
	missing.Trials[0].EffectiveSettings = nil
	if got := assessEffectiveSettings(protocol, baseline, missing); got.Status != "invalid" || !reasonContains(got.Reasons, "has no passed") {
		t.Fatalf("missing effective evidence was decision-qualified: %#v", got)
	}
}

func TestIndependentSeriesCheckBindsDeclaredClientPlacement(t *testing.T) {
	baselinePlan, candidatePlan := testPlanPairWithConfigs(t)
	protocol, err := BuildProtocol("ab-placement", "native", "baseline", "candidate", baselinePlan, candidatePlan, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	baselinePlan.ClientPlacement = "remote-host"
	candidatePlan.ClientPlacement = "remote-host"
	verification := VerifyResult{Issues: []string{}}
	checkSeriesProtocol(&verification, protocol, testSeries("ab-placement-a", 100, 10), testSeries("ab-placement-b", 101, 10), baselinePlan, candidatePlan)
	if !reasonContains(verification.Issues, "client-placement declarations differ") {
		t.Fatalf("independent verifier did not reject plan/bookend placement mismatch: %v", verification.Issues)
	}
}

func TestIndependentSeriesCheckRejectsMixedSeriesSchemaVersions(t *testing.T) {
	baselinePlan, candidatePlan := testPlanPairWithConfigs(t)
	protocol, err := BuildProtocol("ab-series-schema", "native", "baseline", "candidate", baselinePlan, candidatePlan, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	baseline := testSeries("ab-series-schema-a", 100, 10)
	candidate := testSeries("ab-series-schema-b", 101, 10)
	baseline.SchemaVersion = benchmarkrun.SeriesSchemaVersion
	candidate.SchemaVersion = benchmarkrun.SeriesSchemaVersionV2
	verification := VerifyResult{Issues: []string{}}
	checkSeriesProtocol(&verification, protocol, baseline, candidate, baselinePlan, candidatePlan)
	if !reasonContains(verification.Issues, "series schema versions differ") {
		t.Fatalf("mixed benchmark series schema versions were accepted: %v", verification.Issues)
	}
}

func TestPGConfigABComparesNativeBytesNotSeriesLocalManifestLocations(t *testing.T) {
	baselinePlan, candidatePlan := testPlanPairWithConfigs(t)
	protocol, err := BuildProtocol("ab-native-pgconfig", "native", "baseline", "candidate", baselinePlan, candidatePlan, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	toolchainDigest := testDigest("9")
	makeSeries := func(runID string, subject Subject, value float64) benchmarkrun.Series {
		series := testSeries(runID, value, 10)
		threshold := protocol.RegressionThresholdPct
		series.Benchmark, series.Subject, series.ProtocolDigest = subject.Benchmark, subject.Subject, subject.ProtocolDigest
		series.Runtime, series.Class, series.PrimaryMetric, series.Direction = "native", "measurement", protocol.PrimaryMetric, protocol.Direction
		series.ComparisonKeyDigest, series.TrialsPlanned = protocol.ComparisonKeyDigest, protocol.BlocksPlanned
		series.EngineBinaryDigest = testDigest("7")
		series.RegressionThresholdPct, series.AllowedDifferences = &threshold, []string{SubjectPGConfig}
		series.Environment = &benchmarkrun.Environment{
			Runtime: "native", RuntimeOS: "linux", RuntimeArch: "amd64", Driver: "pgbench",
			Target: "direct-postgres", TargetEndpointContract: "pgworkbench.pgbench-target/direct-postgres/v1",
			TargetEndpointHost: "127.0.0.1", TargetEndpointPort: 55433, TargetTopology: "single",
			DriverVersion: "19devel", ParserVersion: "1.0.0", PostgresServerVersionNum: "190000", PostgresServerMajor: "19",
			PGConfig: subject.PGConfig, PGConfigDigest: subject.PGConfigDigest, SubjectDimension: SubjectPGConfig,
			NativeToolchainDigest:      toolchainDigest,
			NativeToolchainManifestRef: filepath.ToSlash(filepath.Join("runs", "benchmarks", runID, benchmarkrun.NativeToolchainSeriesRef)),
			NativeToolchainProvenance:  nativetoolchain.Unattested,
			EngineBinaryDigest:         series.EngineBinaryDigest,
		}
		return series
	}
	baseline := makeSeries("ab-native-pgconfig-a", protocol.Baseline, 100)
	candidate := makeSeries("ab-native-pgconfig-b", protocol.Candidate, 101)
	verification := VerifyResult{Issues: []string{}}
	checkSeriesProtocol(&verification, protocol, baseline, candidate, baselinePlan, candidatePlan)
	if reasonContains(verification.Issues, "runtime fingerprints differ") || reasonContains(verification.Issues, "canonical series-local snapshot") {
		t.Fatalf("equal native bytes with distinct series-local refs were rejected: %v", verification.Issues)
	}
	candidate.EngineBinaryDigest = testDigest("6")
	verification = VerifyResult{Issues: []string{}}
	checkSeriesProtocol(&verification, protocol, baseline, candidate, baselinePlan, candidatePlan)
	if !reasonContains(verification.Issues, "benchmark engine binary digests differ") {
		t.Fatalf("cross-arm benchmark engine byte drift was accepted: %v", verification.Issues)
	}
	candidate.EngineBinaryDigest = baseline.EngineBinaryDigest
	candidate.Environment.NativeToolchainDigest = testDigest("8")
	verification = VerifyResult{Issues: []string{}}
	checkSeriesProtocol(&verification, protocol, baseline, candidate, baselinePlan, candidatePlan)
	if !reasonContains(verification.Issues, "runtime fingerprints differ") {
		t.Fatalf("native byte drift outside pg_config subject was accepted: %v", verification.Issues)
	}
}

func TestDeriveBlocksRetainsOrderEffectsAndIncompletePrefix(t *testing.T) {
	baselinePlan, candidatePlan := testPlanPairWithConfigs(t)
	protocol, err := BuildProtocol("ab-blocks", "native", "baseline", "candidate", baselinePlan, candidatePlan, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	baseline := testSeries("ab-blocks-a", 100, 10)
	candidate := testSeries("ab-blocks-b", 110, 10)
	blocks := DeriveBlocks(protocol, baseline, candidate)
	if len(blocks) != 10 || blocks[0].PlannedOrder != "AB" || blocks[1].PlannedOrder != "BA" {
		t.Fatalf("unexpected derived schedule: %#v", blocks[:2])
	}
	if blocks[0].Status != "passed" || blocks[0].EffectPct == nil || math.Abs(*blocks[0].EffectPct-10) > 1e-12 || blocks[0].Executions[0].Role != "baseline" || blocks[1].Executions[0].Role != "candidate" {
		t.Fatalf("block order/effect was not bound: %#v %#v", blocks[0], blocks[1])
	}

	candidate.Trials = candidate.Trials[:9]
	blocks = DeriveBlocks(protocol, baseline, candidate)
	last := blocks[9]
	if last.Status != "invalid" || len(last.Executions) != 1 || last.Executions[0].Role != "baseline" || !reasonContains(last.Reasons, "candidate trial was not executed") {
		t.Fatalf("incomplete execution prefix was hidden: %#v", last)
	}
}

func TestDeriveTerminalRequiresQualificationButPreservesRegression(t *testing.T) {
	baselinePlan, candidatePlan := testPlanPairWithConfigs(t)
	protocol, err := BuildProtocol("ab-terminal", "native", "baseline", "candidate", baselinePlan, candidatePlan, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	baseline := testSeries("ab-terminal-a", 100, 10)
	candidate := testSeries("ab-terminal-b", 90, 10)
	blocks := DeriveBlocks(protocol, baseline, candidate)
	analysis := benchmarkcompare.AnalyzePaired(pairedUnits(blocks), pairedOptions(protocol))
	qualified := benchmarkqualify.BookendAssessment{Status: benchmarkqualify.BookendStatusRecordedPolicyPassed, Reasons: []string{}}
	effective := EffectiveSettingsAssessment{Status: "verified-different", Names: []string{"shared_buffers"}, EffectiveDifferences: []string{"shared_buffers"}, Reasons: []string{}}
	status, decision, reasons := deriveTerminal(protocol, baseline, candidate, blocks, qualified, effective, analysis)
	if status != "failed" || decision != "regressed" || len(reasons) != 0 {
		t.Fatalf("recorded regression was not preserved: %s %s %v", status, decision, reasons)
	}
	unqualified := qualified
	unqualified.Status = benchmarkqualify.BookendStatusUnqualified
	unqualified.Reasons = []string{"before recorded verdict is unqualified"}
	status, decision, reasons = deriveTerminal(protocol, baseline, candidate, blocks, unqualified, effective, analysis)
	if status != "inconclusive" || decision != "inconclusive" || !reasonContains(reasons, "unqualified") {
		t.Fatalf("unqualified host issued a performance verdict: %s %s %v", status, decision, reasons)
	}
}

func testPlanPair() (benchmarkplan.Plan, benchmarkplan.Plan) {
	threshold := 5.0
	baseline := benchmarkplan.Plan{
		Spec:                      "pgbench/baseline",
		Class:                     "measurement",
		Driver:                    "pgbench",
		PGConfig:                  "default",
		PGConfigDigest:            testDigest("1"),
		ProtocolDigest:            testDigest("2"),
		ComparisonKeyDigest:       testDigest("3"),
		Trials:                    10,
		MinValidTrials:            10,
		ClientPlacement:           "same-host",
		PrimaryMetric:             "pgbench.tps",
		Direction:                 "higher",
		RegressionThresholdPct:    &threshold,
		AllowedSubjectDifferences: []string{"pg_config"},
	}
	candidate := baseline
	candidate.Spec = "pgbench/candidate"
	candidate.PGConfig = "candidate"
	candidate.PGConfigDigest = testDigest("4")
	candidate.ProtocolDigest = testDigest("5")
	candidate.RegressionThresholdPct = &threshold
	return baseline, candidate
}

func testPlanPairWithConfigs(t *testing.T) (benchmarkplan.Plan, benchmarkplan.Plan) {
	t.Helper()
	baseline, candidate := testPlanPair()
	root := t.TempDir()
	baseline.PGConfigPath = filepath.Join(root, "baseline.conf")
	candidate.PGConfigPath = filepath.Join(root, "candidate.conf")
	if err := os.WriteFile(baseline.PGConfigPath, []byte("shared_buffers = '128MB'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate.PGConfigPath, []byte("shared_buffers = '256MB'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return baseline, candidate
}

func testOptions() Options {
	memory, storage, load := 10.0, 10.0, 1.0
	return Options{
		BootstrapResamples:   1000,
		ConfidenceLevel:      0.95,
		Seed:                 42,
		MaxBookendGapSeconds: 3600,
		Qualification: benchmarkqualify.InspectOptions{
			StorageLabel:    "postgres-data",
			ClientPlacement: "same-host",
			Policy: benchmarkqualify.Policy{
				Strict:                  true,
				MinMemoryAvailablePct:   &memory,
				MinStorageAvailablePct:  &storage,
				MaxLoad1PerCPU:          &load,
				RequiredClocksource:     "tsc",
				RequiredGovernor:        "performance",
				RequiredClientPlacement: "same-host",
			},
		},
	}
}

func testSeries(runID string, value float64, count int) benchmarkrun.Series {
	series := benchmarkrun.Series{RunID: runID, Status: "passed", Reasons: []string{}, TrialsPlanned: 10}
	for index := 0; index < count; index++ {
		primary := value
		series.Trials = append(series.Trials, benchmarkrun.Trial{
			Trial:        index + 1,
			RunID:        runID + "-t" + string(rune('a'+index)),
			Status:       "passed",
			PrimaryValue: &primary,
		})
	}
	return series
}

func testDigest(character string) string { return "sha256:" + strings.Repeat(character, 64) }

func syntheticEffectiveSettings(t *testing.T, protocol Protocol, trial benchmarkrun.Trial, setting, unit, source, serverVersion string, pending bool) *benchmarksettings.Evidence {
	t.Helper()
	recorded := benchmarksettings.Evidence{
		SchemaVersion:    benchmarksettings.SchemaVersion,
		ArtifactType:     benchmarksettings.ArtifactType,
		ParserVersion:    benchmarksettings.ParserVersion,
		RunID:            trial.RunID,
		ProtocolDigest:   protocol.Digest,
		Trial:            trial.Trial,
		CapturedAt:       "2026-08-12T00:00:01Z",
		ServerVersionNum: serverVersion,
		Names:            append([]string(nil), protocol.EffectiveSettings.Names...),
		Settings: []benchmarksettings.Setting{{
			Name: "shared_buffers", Setting: setting, Unit: unit, Source: source, PendingRestart: pending, Context: "postmaster",
		}},
		Source: benchmarksettings.SourceRef{Path: benchmarksettings.SourcePath, Digest: testDigest("8"), Size: 100},
		Phase: benchmarksettings.PhaseBinding{
			Name: "prepare", JournalDigest: testDigest("7"), StartedAt: "2026-08-12T00:00:00Z", FinishedAt: "2026-08-12T00:00:02Z",
		},
	}
	var err error
	recorded.Digest, err = benchmarksettings.Digest(recorded)
	if err != nil {
		t.Fatal(err)
	}
	return &recorded
}

func reasonContains(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}
