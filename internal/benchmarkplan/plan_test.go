package benchmarkplan

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestBuildBenchmarkPlanAndProtocolIdentity(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/pgbench/read.env", benchmarkSpec("4", "default"))

	first, err := Build(speccatalog.New(root), "pgbench/read")
	if err != nil {
		t.Fatal(err)
	}
	if first.Clients != 4 || first.Trials != 10 || first.MinValidTrials != 8 {
		t.Fatalf("unexpected typed benchmark plan: %#v", first)
	}
	if first.ProtocolSchemaVersion != ProtocolSchemaVersion {
		t.Fatalf("unexpected protocol schema version: %q", first.ProtocolSchemaVersion)
	}
	if first.ProtocolDigest == "" || first.ComparisonKeyDigest == "" || first.ProtocolDigest == first.ComparisonKeyDigest {
		t.Fatalf("unexpected benchmark digests: %#v", first)
	}
	if !first.LogTransactions || first.LogSampleRate != 1 || !first.RuntimeReset {
		t.Fatalf("unexpected measurement defaults: %#v", first)
	}
	if first.PGConfigPath != filepath.Join(root, "configs", "default", "postgresql.conf") {
		t.Fatalf("unexpected PostgreSQL config path: %q", first.PGConfigPath)
	}
	if err := VerifyDigests(first); err != nil {
		t.Fatalf("fresh plan identity did not verify: %v", err)
	}
	tampered := first
	tampered.Clients++
	if err := VerifyDigests(tampered); err == nil {
		t.Fatal("tampered plan retained a valid protocol identity")
	}

	writeBenchmarkFile(t, root, "benchmarks/pgbench/read.env", benchmarkSpec("64", "default"))
	moreClients, err := Build(speccatalog.New(root), "pgbench/read")
	if err != nil {
		t.Fatal(err)
	}
	if moreClients.ProtocolDigest == first.ProtocolDigest || moreClients.ComparisonKeyDigest == first.ComparisonKeyDigest {
		t.Fatal("client-count change must alter both protocol and comparison-key digests")
	}

	writeBenchmarkFile(t, root, "benchmarks/pgbench/read.env", benchmarkSpec("4", "tuned"))
	tuned, err := Build(speccatalog.New(root), "pgbench/read")
	if err != nil {
		t.Fatal(err)
	}
	if tuned.ProtocolDigest == first.ProtocolDigest {
		t.Fatal("PostgreSQL config change must alter the exact protocol digest")
	}
	if tuned.ComparisonKeyDigest != first.ComparisonKeyDigest {
		t.Fatal("allowed pg_config subject change must not alter comparison-key digest")
	}
}

func TestBuildBenchmarkPlanDefaultsAndFixedTransactions(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/smoke.env", strings.Join([]string{
		"BENCHMARK_NAME=smoke",
		"BENCHMARK_CLASS=smoke",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/tiny",
		"BENCHMARK_MODE=fixed-transactions",
		"BENCHMARK_SCALE=1",
		"BENCHMARK_CLIENTS=2",
		"BENCHMARK_THREADS=1",
		"BENCHMARK_TRANSACTIONS_PER_CLIENT=20",
		"BENCHMARK_CACHE_REGIME=uncontrolled",
		"BENCHMARK_STATISTICS_RESET_POLICY=none",
		"BENCHMARK_STATISTICS_RESET_BOUNDARY=none",
		"BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v1'",
		"BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1",
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified",
		"BENCHMARK_CLIENT_PLACEMENT=same-host",
		"BENCHMARK_RESOURCE_BUDGET_MODE=unbounded",
		"",
	}, "\n"))

	plan, err := Build(speccatalog.New(root), "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Driver != "pgbench" || plan.Trials != 1 || plan.MinValidTrials != 1 {
		t.Fatalf("unexpected smoke defaults: %#v", plan)
	}
	if plan.LogTransactions || plan.MeasureSeconds != 0 || plan.TransactionsPerClient != 20 {
		t.Fatalf("unexpected fixed-transaction plan: %#v", plan)
	}
	if len(plan.AllowedSubjectDifferences) != 1 || plan.AllowedSubjectDifferences[0] != "pg_config" {
		t.Fatalf("unexpected allowed subject differences: %#v", plan.AllowedSubjectDifferences)
	}
}

func TestBenchmarkClassIsPartOfProtocolIdentity(t *testing.T) {
	root := benchmarkFixture(t)
	content := benchmarkSpec("4", "default") + "BENCHMARK_LOG_TRANSACTIONS=1\n"
	writeBenchmarkFile(t, root, "benchmarks/read.env", content)
	measurement, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}

	content = strings.Replace(content, "BENCHMARK_CLASS=measurement", "BENCHMARK_CLASS=smoke", 1)
	writeBenchmarkFile(t, root, "benchmarks/read.env", content)
	smoke, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	if smoke.ProtocolDigest == measurement.ProtocolDigest || smoke.ComparisonKeyDigest == measurement.ComparisonKeyDigest {
		t.Fatal("benchmark evidence class must alter both protocol identities")
	}
}

func TestBenchmarkProtocolDeclarationsAreTypedAndIdentityBound(t *testing.T) {
	root := benchmarkFixture(t)
	content := benchmarkSpec("4", "default")
	writeBenchmarkFile(t, root, "benchmarks/read.env", content)
	first, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheRegime != "warm" || first.StatisticsResetPolicy != "none" || first.StatisticsResetBoundary != "none" || first.CollectorIntervalSeconds != 1 || first.CollectorOverheadMode != "included-unquantified" || first.ClientPlacement != "same-host" || first.ResourceBudgetMode != "unbounded" {
		t.Fatalf("protocol declarations were not typed into the plan: %#v", first)
	}
	if strings.Join(first.Collectors, ",") != "pgbench-driver,postgresql-sampler-v1" {
		t.Fatalf("collector set was not normalized: %#v", first.Collectors)
	}

	writeBenchmarkFile(t, root, "benchmarks/read.env", strings.Replace(content, "BENCHMARK_CACHE_REGIME=warm", "BENCHMARK_CACHE_REGIME=steady", 1))
	changed, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ProtocolDigest == first.ProtocolDigest || changed.ComparisonKeyDigest == first.ComparisonKeyDigest {
		t.Fatal("cache-regime declaration did not alter both protocol identities")
	}

	tampered := first
	tampered.CacheRegime = "invented"
	tampered.ProtocolDigest, tampered.ComparisonKeyDigest, err = IdentityDigests(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigests(tampered); err == nil || !strings.Contains(err.Error(), "cache regime") {
		t.Fatalf("semantically invalid redigested declaration passed verification: %v", err)
	}

	cpu := 2.5
	memory := 4096
	tampered = first
	tampered.ResourceBudgetMode = "operator-declared"
	tampered.CPUBudgetCores = &cpu
	tampered.MemoryBudgetMiB = &memory
	tampered.ProtocolDigest, tampered.ComparisonKeyDigest, err = IdentityDigests(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigests(tampered); err != nil {
		t.Fatalf("valid operator-declared resource budget did not verify: %v", err)
	}

	resourceSpec := strings.Replace(content, "BENCHMARK_RESOURCE_BUDGET_MODE=unbounded", "BENCHMARK_RESOURCE_BUDGET_MODE=operator-declared", 1) +
		"BENCHMARK_CPU_BUDGET_CORES=2.5\nBENCHMARK_MEMORY_BUDGET_MIB=4096\n"
	resourceSpec = strings.Replace(resourceSpec, "BENCHMARK_STATISTICS_RESET_POLICY=none", "BENCHMARK_STATISTICS_RESET_POLICY=operator-managed", 1)
	resourceSpec = strings.Replace(resourceSpec, "BENCHMARK_STATISTICS_RESET_BOUNDARY=none", "BENCHMARK_STATISTICS_RESET_BOUNDARY=before-measure", 1)
	writeBenchmarkFile(t, root, "benchmarks/read.env", resourceSpec)
	declared, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	if declared.CPUBudgetCores == nil || *declared.CPUBudgetCores != 2.5 || declared.MemoryBudgetMiB == nil || *declared.MemoryBudgetMiB != 4096 {
		t.Fatalf("operator-declared resource values were not typed: %#v", declared)
	}
	if declared.StatisticsResetPolicy != "operator-managed" || declared.StatisticsResetBoundary != "before-measure" {
		t.Fatalf("operator-managed statistics reset declaration was not typed: %#v", declared)
	}
}

func TestBuildBenchmarkProtocolV2ControlsAreExplicitAndIdentityBound(t *testing.T) {
	root := benchmarkFixture(t)
	content := benchmarkSpecV2("4", "default")
	writeBenchmarkFile(t, root, "benchmarks/read-v2.env", content)
	plan, err := Build(speccatalog.New(root), "read-v2")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProtocolSchemaVersion != ProtocolSchemaVersionV2 || plan.ContractVersion != "2" {
		t.Fatalf("v2 source did not produce a v2 plan: %#v", plan)
	}
	if plan.CacheRegime != "postgres-shared-buffer-warm" || strings.Join(plan.CacheTargetRelations, ",") != "public.accounts,public.branches" || plan.CacheMinResidentPct == nil || *plan.CacheMinResidentPct != 90 {
		t.Fatalf("v2 cache controls were not typed: %#v", plan)
	}
	if plan.StatisticsResetPolicy != "runner-managed" || plan.StatisticsResetBoundary != "before-measure" || strings.Join(plan.Collectors, ",") != "pgbench-driver,postgresql-sampler-v2" {
		t.Fatalf("v2 reset/collector controls were not typed: %#v", plan)
	}
	if plan.CollectorOverheadSamples == nil || *plan.CollectorOverheadSamples != 5 || plan.CollectorMaxDutyCyclePct == nil || *plan.CollectorMaxDutyCyclePct != 2 {
		t.Fatalf("v2 overhead controls were not typed: %#v", plan)
	}
	if plan.ResourceBudgetMode != "runner-enforced" || plan.CPUBudgetCores != nil || plan.CPUBudgetMillicores == nil || *plan.CPUBudgetMillicores != 1500 || plan.MemoryBudgetMiB == nil || *plan.MemoryBudgetMiB != 1024 || plan.ResourceBudgetScope != "postgres-server-and-pgbench-driver" || plan.ResourceEnforcementProvider != "docker-single-container-linux-cgroup-v2" {
		t.Fatalf("v2 resource controls were not typed: %#v", plan)
	}
	wantConstraints := "cgroup-v2-required,docker-engine-required,linux-only,postgres-and-driver-share-one-container"
	if strings.Join(plan.ResourceProviderConstraints, ",") != wantConstraints {
		t.Fatalf("v2 resource provider constraints drifted: %#v", plan.ResourceProviderConstraints)
	}
	if err := VerifyDigests(plan); err != nil {
		t.Fatalf("v2 plan identity did not verify: %v", err)
	}
	values, err := envfile.Parse(filepath.Join(root, "benchmarks", "read-v2.env"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySpecDeclarations(plan, values); err != nil {
		t.Fatalf("v2 plan did not bind to source declarations: %v", err)
	}

	mutations := []struct {
		name   string
		mutate func(*Plan)
	}{
		{"contract", func(value *Plan) { value.ContractVersion = "" }},
		{"cache targets", func(value *Plan) { value.CacheTargetRelations = []string{"public.accounts"} }},
		{"cache threshold", func(value *Plan) { value.CacheMinResidentPct = floatPointerForTest(91) }},
		{"overhead samples", func(value *Plan) { value.CollectorOverheadSamples = intPointerForTest(6) }},
		{"overhead duty", func(value *Plan) { value.CollectorMaxDutyCyclePct = floatPointerForTest(3) }},
		{"cpu millicores", func(value *Plan) { value.CPUBudgetMillicores = intPointerForTest(1600) }},
		{"resource scope", func(value *Plan) { value.ResourceBudgetScope = "substituted" }},
		{"resource provider", func(value *Plan) { value.ResourceEnforcementProvider = "substituted" }},
		{"resource constraints", func(value *Plan) { value.ResourceProviderConstraints = []string{"substituted"} }},
		{"resource placement", func(value *Plan) { value.ClientPlacement = "remote-host" }},
	}
	for _, relation := range []string{"accounts", "public.аккаунты", "public.accounts.extra"} {
		tampered := plan
		tampered.CacheTargetRelations = []string{relation}
		tampered.ProtocolDigest, tampered.ComparisonKeyDigest, err = IdentityDigests(tampered)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyDigests(tampered); err == nil {
			t.Fatalf("unsupported runtime relation name passed plan validation: %q", relation)
		}
	}
	for name, mutate := range map[string]func(*Plan){
		"collector interval": func(value *Plan) { value.CollectorIntervalSeconds = 3601 },
		"overhead samples":   func(value *Plan) { value.CollectorOverheadSamples = intPointerForTest(10001) },
	} {
		t.Run("bounded "+name, func(t *testing.T) {
			tampered := plan
			mutate(&tampered)
			tampered.ProtocolDigest, tampered.ComparisonKeyDigest, err = IdentityDigests(tampered)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyDigests(tampered); err == nil {
				t.Fatalf("unbounded v2 %s passed", name)
			}
		})
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			changed := plan
			test.mutate(&changed)
			protocol, comparison, digestErr := IdentityDigests(changed)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			if protocol == plan.ProtocolDigest || comparison == plan.ComparisonKeyDigest {
				t.Fatalf("v2 %s is absent from protocol identity", test.name)
			}
			changed.ProtocolDigest, changed.ComparisonKeyDigest = protocol, comparison
			if err := VerifySpecDeclarations(changed, values); err == nil {
				t.Fatalf("coherently redigested v2 %s escaped immutable source binding", test.name)
			}
		})
	}
}

func TestProtocolV2DoesNotReinterpretLegacyDeclarations(t *testing.T) {
	root := benchmarkFixture(t)
	legacy := benchmarkSpec("4", "default")
	writeBenchmarkFile(t, root, "benchmarks/legacy.env", legacy)
	plan, err := Build(speccatalog.New(root), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProtocolSchemaVersion != ProtocolSchemaVersion || plan.ContractVersion != "" || plan.CacheRegime != "warm" {
		t.Fatalf("legacy v1 plan was reinterpreted: %#v", plan)
	}

	for _, replacement := range []struct{ old, new, want string }{
		{"BENCHMARK_CACHE_REGIME=warm", "BENCHMARK_CACHE_REGIME=warm\nBENCHMARK_CONTRACT_VERSION=2", "must be uncontrolled or postgres-shared-buffer-warm"},
		{"BENCHMARK_STATISTICS_RESET_POLICY=none", "BENCHMARK_STATISTICS_RESET_POLICY=operator-managed\nBENCHMARK_CONTRACT_VERSION=2", "must be none or runner-managed"},
		{"BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified", "BENCHMARK_COLLECTOR_OVERHEAD_MODE=operator-calibrated\nBENCHMARK_CONTRACT_VERSION=2", "must be included-unquantified or runner-calibrated-duty-cycle"},
		{"BENCHMARK_RESOURCE_BUDGET_MODE=unbounded", "BENCHMARK_RESOURCE_BUDGET_MODE=operator-declared\nBENCHMARK_CONTRACT_VERSION=2", "must be unbounded or runner-enforced"},
	} {
		content := strings.Replace(legacy, replacement.old, replacement.new, 1)
		writeBenchmarkFile(t, root, "benchmarks/rejected.env", content)
		if _, err := Build(speccatalog.New(root), "rejected"); err == nil || !strings.Contains(err.Error(), replacement.want) {
			t.Fatalf("legacy declaration was silently reinterpreted by v2: %v", err)
		}
	}
}

func TestEveryProtocolDeclarationAffectsBothIdentities(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/read.env", benchmarkSpec("4", "default"))
	base, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{"cache regime", func(plan *Plan) { plan.CacheRegime = "steady" }},
		{"statistics reset policy", func(plan *Plan) { plan.StatisticsResetPolicy = "operator-managed" }},
		{"statistics reset boundary", func(plan *Plan) { plan.StatisticsResetBoundary = "before-measure" }},
		{"collector set", func(plan *Plan) { plan.Collectors = []string{"pgbench-driver"} }},
		{"collector interval", func(plan *Plan) { plan.CollectorIntervalSeconds = 2 }},
		{"collector overhead", func(plan *Plan) { plan.CollectorOverheadMode = "operator-calibrated" }},
		{"client placement", func(plan *Plan) { plan.ClientPlacement = "remote-host" }},
		{"resource budget mode", func(plan *Plan) { plan.ResourceBudgetMode = "operator-declared" }},
		{"CPU budget", func(plan *Plan) { value := 2.5; plan.CPUBudgetCores = &value }},
		{"memory budget", func(plan *Plan) { value := 4096; plan.MemoryBudgetMiB = &value }},
		{"connect per transaction", func(plan *Plan) { plan.ConnectPerTransaction = true }},
		{"latency-limit exceeded budget", func(plan *Plan) { value := 1.0; plan.MaxLatencyLimitExceededPct = &value }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			protocolDigest, comparisonDigest, digestErr := IdentityDigests(changed)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			if protocolDigest == base.ProtocolDigest || comparisonDigest == base.ComparisonKeyDigest {
				t.Fatalf("%s is absent from one or both protocol identities", test.name)
			}
		})
	}
}

func TestVerifySpecDeclarationsRejectsCoherentlyRedigestedMutations(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/read.env", benchmarkSpec("4", "default"))
	plan, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	values, err := envfile.Parse(filepath.Join(root, "benchmarks", "read.env"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySpecDeclarations(plan, values); err != nil {
		t.Fatalf("fresh plan does not bind to its source snapshot: %v", err)
	}

	floatPointer := func(value float64) *float64 { return &value }
	intPointer := func(value int) *int { return &value }
	uintPointer := func(value uint64) *uint64 { return &value }
	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{"name", func(p *Plan) { p.Name = "substituted" }},
		{"class", func(p *Plan) { p.Class = "smoke" }},
		{"driver", func(p *Plan) { p.Driver = "substituted" }},
		{"experiment", func(p *Plan) { p.ExperimentSpec = "smoke" }},
		{"workload", func(p *Plan) { p.WorkloadSpec = "pgbench/select-only" }},
		{"pg config", func(p *Plan) { p.PGConfig = "tuned" }},
		{"mode", func(p *Plan) { p.Mode = "fixed-transactions" }},
		{"scale", func(p *Plan) { p.Scale++ }},
		{"clients", func(p *Plan) { p.Clients++ }},
		{"threads", func(p *Plan) { p.Threads++ }},
		{"warmup", func(p *Plan) { p.WarmupSeconds++ }},
		{"measure", func(p *Plan) { p.MeasureSeconds++ }},
		{"inactive transactions", func(p *Plan) { p.TransactionsPerClient = 1 }},
		{"trials", func(p *Plan) { p.Trials++ }},
		{"minimum valid", func(p *Plan) { p.MinValidTrials-- }},
		{"reset policy", func(p *Plan) { p.ResetPolicy = "reuse-after-first"; p.RuntimeReset = false }},
		{"runtime reset", func(p *Plan) { p.RuntimeReset = false }},
		{"cache regime", func(p *Plan) { p.CacheRegime = "steady" }},
		{"statistics reset", func(p *Plan) {
			p.StatisticsResetPolicy = "operator-managed"
			p.StatisticsResetBoundary = "before-measure"
		}},
		{"collectors", func(p *Plan) { p.Collectors = []string{"pgbench-driver"} }},
		{"collector interval", func(p *Plan) { p.CollectorIntervalSeconds++ }},
		{"collector overhead", func(p *Plan) { p.CollectorOverheadMode = "operator-calibrated" }},
		{"client placement", func(p *Plan) { p.ClientPlacement = "remote-host" }},
		{"resource budget", func(p *Plan) {
			p.ResourceBudgetMode = "operator-declared"
			p.CPUBudgetCores = floatPointer(2)
			p.MemoryBudgetMiB = intPointer(1024)
		}},
		{"primary metric", func(p *Plan) { p.PrimaryMetric = "pgbench.latency_mean_us"; p.Direction = "lower" }},
		{"direction", func(p *Plan) { p.Direction = "lower" }},
		{"maximum CV", func(p *Plan) { p.MaxCVPct++ }},
		{"regression threshold", func(p *Plan) { p.RegressionThresholdPct = floatPointer(5) }},
		{"rate", func(p *Plan) { p.Rate = floatPointer(100) }},
		{"latency limit", func(p *Plan) { p.LatencyLimitMS = floatPointer(50) }},
		{"latency SLO", func(p *Plan) { p.LatencyLimitMS = floatPointer(50); p.MaxLatencyLimitExceededPct = floatPointer(1) }},
		{"connect per transaction", func(p *Plan) {
			p.PrimaryMetric = "pgbench.latency_mean_us"
			p.Direction = "lower"
			p.ConnectPerTransaction = true
		}},
		{"query protocol", func(p *Plan) { p.QueryProtocol = "extended" }},
		{"random seed", func(p *Plan) {
			p.RandomSeed = uintPointer(17)
			p.RandomSeedSemantics = "phase-split-offset-v1"
			p.WarmupRandomSeed = uintPointer(18)
			p.MeasureRandomSeed = uintPointer(17)
		}},
		{"maximum tries", func(p *Plan) { p.MaxTries = intPointer(2) }},
		{"transaction logging", func(p *Plan) { p.LogTransactions = false }},
		{"log sample rate", func(p *Plan) { p.LogSampleRate = 0.5 }},
		{"allowed differences", func(p *Plan) { p.AllowedSubjectDifferences = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := plan
			test.mutate(&changed)
			changed.ProtocolDigest, changed.ComparisonKeyDigest, err = IdentityDigests(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifySpecDeclarations(changed, values); err == nil {
				t.Fatal("coherently redigested plan mutation was not rejected")
			}
		})
	}
}

func TestVerifyWorkloadDeclarationsBindsDerivedFields(t *testing.T) {
	plan := Plan{WorkloadMode: "builtin"}
	values := map[string]string{"WORKLOAD_KIND": "pgbench"}
	if err := VerifyWorkloadDeclarations(plan, values); err != nil {
		t.Fatalf("valid builtin workload did not bind: %v", err)
	}
	for _, test := range []struct {
		name   string
		plan   Plan
		values map[string]string
	}{
		{"kind", plan, map[string]string{"WORKLOAD_KIND": "shell"}},
		{"mode", Plan{WorkloadMode: "select-only"}, values},
		{"script", Plan{WorkloadMode: "builtin", WorkloadScript: "workloads/foreign.sql", WorkloadScriptDigest: "sha256:" + strings.Repeat("a", 64)}, values},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := VerifyWorkloadDeclarations(test.plan, test.values); err == nil {
				t.Fatal("workload substitution was not rejected")
			}
		})
	}
}

func TestBenchmarkRandomSeedUsesDistinctBoundPhaseStreams(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/read.env", benchmarkSpec("4", "default")+"BENCHMARK_RANDOM_SEED=17\n")
	plan, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	if plan.RandomSeed == nil || *plan.RandomSeed != 17 || plan.MeasureRandomSeed == nil || *plan.MeasureRandomSeed != 17 || plan.WarmupRandomSeed == nil || *plan.WarmupRandomSeed != 18 || plan.RandomSeedSemantics != "phase-split-offset-v1" {
		t.Fatalf("random phase streams were not derived explicitly: %#v", plan)
	}
	seededDigest := plan.ProtocolDigest
	writeBenchmarkFile(t, root, "benchmarks/read.env", benchmarkSpec("4", "default")+"BENCHMARK_RANDOM_SEED=18\n")
	changed, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ProtocolDigest == seededDigest {
		t.Fatal("phase seed change did not alter protocol identity")
	}
	tampered := plan
	wrongWarmup := uint64(19)
	tampered.WarmupRandomSeed = &wrongWarmup
	tampered.ProtocolDigest, tampered.ComparisonKeyDigest, err = IdentityDigests(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigests(tampered); err == nil || !strings.Contains(err.Error(), "derived phase seeds") {
		t.Fatalf("redigested invalid phase-seed derivation passed: %v", err)
	}
	writeBenchmarkFile(t, root, "benchmarks/read.env", benchmarkSpec("4", "default")+"BENCHMARK_RANDOM_SEED=9223372036854775808\n")
	if _, err := Build(speccatalog.New(root), "read"); err == nil || !strings.Contains(err.Error(), "must be at most") {
		t.Fatalf("out-of-range pgbench seed was accepted: %v", err)
	}
}

func TestConnectionChurnAndLatencySLOBudgetAreTypedAndBound(t *testing.T) {
	root := benchmarkFixture(t)
	content := benchmarkSpec("4", "default") + strings.Join([]string{
		"BENCHMARK_PRIMARY_METRIC=pgbench.latency_mean_us",
		"BENCHMARK_DIRECTION=lower",
		"BENCHMARK_CONNECT_PER_TRANSACTION=1",
		"BENCHMARK_LATENCY_LIMIT_MS=50",
		"BENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT=1",
		"",
	}, "\n")
	writeBenchmarkFile(t, root, "benchmarks/read.env", content)
	plan, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ConnectPerTransaction || plan.MaxLatencyLimitExceededPct == nil || *plan.MaxLatencyLimitExceededPct != 1 {
		t.Fatalf("connection/SLO declarations were not typed: %#v", plan)
	}
	if err := VerifyDigests(plan); err != nil {
		t.Fatalf("typed connection/SLO protocol did not verify: %v", err)
	}
	changed := plan
	budget := 2.0
	changed.MaxLatencyLimitExceededPct = &budget
	protocolDigest, comparisonDigest, err := IdentityDigests(changed)
	if err != nil {
		t.Fatal(err)
	}
	if protocolDigest == plan.ProtocolDigest || comparisonDigest == plan.ComparisonKeyDigest {
		t.Fatal("latency SLO budget did not alter both protocol identities")
	}
}

func TestRenderBenchmarkPlan(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/read.env", benchmarkSpec("4", "default"))
	plan, err := Build(speccatalog.New(root), "read")
	if err != nil {
		t.Fatal(err)
	}

	var markdown bytes.Buffer
	if err := Render(&markdown, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Benchmark Plan", "| Clients | `4` |", "Cache regime (declared)", "Collectors", "Random seed semantics", "Protocol digest", "Comparison key digest"} {
		if !strings.Contains(markdown.String(), want) {
			t.Fatalf("benchmark plan missing %q:\n%s", want, markdown.String())
		}
	}

	var encoded bytes.Buffer
	if err := RenderJSON(&encoded, plan); err != nil {
		t.Fatal(err)
	}
	var decoded Plan
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ProtocolDigest != plan.ProtocolDigest || decoded.Clients != 4 {
		t.Fatalf("unexpected JSON plan: %#v", decoded)
	}
}

func TestRepositoryBuiltInBenchmarkPlans(t *testing.T) {
	root := filepath.Join("..", "..")
	catalog := speccatalog.New(root)
	ids, err := catalog.List("benchmark")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pgbench/connection-churn",
		"pgbench/control-v2-docker-enforced-smoke",
		"pgbench/control-v2-smoke",
		"pgbench/custom-transfer",
		"pgbench/pgbouncer/direct-connection-churn",
		"pgbench/pgbouncer/direct-smoke",
		"pgbench/pgbouncer/proxy-connection-churn",
		"pgbench/pgbouncer/proxy-smoke",
		"pgbench/rate-limited-slo",
		"pgbench/read-only",
		"pgbench/read-write",
		"pgbench/saturation/c01",
		"pgbench/saturation/c04",
		"pgbench/saturation/c16",
		"pgbench/saturation/c64",
		"pgbench/smoke",
		"pgbench/source-patch",
		"pgbench/wal-checkpoint-fsync",
		"pgbench/wal-checkpoint-fsync-baseline",
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected built-in benchmark catalog: %#v", ids)
	}
	for _, id := range ids {
		plan, err := Build(catalog, id)
		if err != nil {
			t.Fatalf("build built-in benchmark %s: %v", id, err)
		}
		if plan.ProtocolDigest == "" || plan.ComparisonKeyDigest == "" {
			t.Fatalf("built-in benchmark %s has empty digests: %#v", id, plan)
		}
	}
}

func TestPgBouncerTargetIsTopologyAndIdentityBound(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/direct.env", benchmarkSpec("4", "default")+"BENCHMARK_TARGET=direct-postgres\n")
	proxySpec := strings.Replace(benchmarkSpec("4", "default"),
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench-pgbouncer\nBENCHMARK_TARGET=pgbouncer", 1)
	writeBenchmarkFile(t, root, "benchmarks/proxy.env", proxySpec)

	direct, err := Build(speccatalog.New(root), "direct")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := Build(speccatalog.New(root), "proxy")
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Target != TargetPgBouncer || proxy.TargetTopology != "pgbouncer" || proxy.TargetEndpointContract != EndpointPgBouncerV1 {
		t.Fatalf("unexpected proxy target contract: %#v", proxy)
	}
	if proxy.TargetTopologyDigest == "" || proxy.TargetTopologyPath == "" {
		t.Fatalf("proxy topology source is not bound: %#v", proxy)
	}
	if direct.ProtocolDigest == proxy.ProtocolDigest || direct.ComparisonKeyDigest == proxy.ComparisonKeyDigest {
		t.Fatal("direct and PgBouncer targets share a protocol or comparison identity")
	}

	tampered := proxy
	tampered.TargetEndpointContract = EndpointDirectV1
	if _, _, err := IdentityDigests(tampered); err != nil {
		t.Fatal(err)
	}
	tampered.ProtocolDigest, tampered.ComparisonKeyDigest, err = IdentityDigests(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigests(tampered); err == nil {
		t.Fatal("coherently redigested endpoint-contract substitution passed verification")
	}
}

func TestBenchmarkTargetMustMatchStaticExperimentTopology(t *testing.T) {
	root := benchmarkFixture(t)
	writeBenchmarkFile(t, root, "benchmarks/mismatch.env", benchmarkSpec("4", "default")+"BENCHMARK_TARGET=pgbouncer\n")
	errList := speccatalog.New(root).Validate("benchmark", []string{"mismatch"})
	if len(errList) == 0 || !strings.Contains(errors.Join(errList...).Error(), "requires experiment topology pgbouncer") {
		t.Fatalf("target/topology mismatch passed validation: %v", errList)
	}
}

func benchmarkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeBenchmarkFile(t, root, "configs/default/postgresql.conf", "# default\n")
	writeBenchmarkFile(t, root, "configs/tuned/postgresql.conf", "shared_buffers = '256MB'\n")
	writeBenchmarkFile(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_SERVICES=postgres\n")
	writeBenchmarkFile(t, root, "topologies/pgbouncer.env", "TOPOLOGY_NAME=pgbouncer\nTOPOLOGY_SERVICES='postgres pgbouncer'\n")
	writeBenchmarkFile(t, root, "experiments/benchmarks/pgbench.env", strings.Join([]string{
		"BENCHMARK_UNUSED=1",
		"EXPERIMENT_NAME=benchmark",
		"EXPERIMENT_PG_CONFIG=${EXPERIMENT_PG_CONFIG:-default}",
		"EXPERIMENT_WORKLOAD_SPEC=${EXPERIMENT_WORKLOAD_SPEC:-pgbench/tiny}",
		"",
	}, "\n"))
	writeBenchmarkFile(t, root, "experiments/benchmarks/pgbench-pgbouncer.env", strings.Join([]string{
		"EXPERIMENT_NAME=benchmark-through-pgbouncer",
		"EXPERIMENT_TOPOLOGY=pgbouncer",
		"EXPERIMENT_PG_CONFIG=${EXPERIMENT_PG_CONFIG:-default}",
		"EXPERIMENT_WORKLOAD_SPEC=${EXPERIMENT_WORKLOAD_SPEC:-pgbench/tiny}",
		"",
	}, "\n"))
	writeBenchmarkFile(t, root, "workloads/pgbench/tiny.env", "WORKLOAD_NAME=tiny\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=builtin\n")
	writeBenchmarkFile(t, root, "workloads/pgbench/select-only.env", "WORKLOAD_NAME=select\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=select-only\n")
	return root
}

func benchmarkSpec(clients string, config string) string {
	return strings.Join([]string{
		"BENCHMARK_NAME=read",
		"BENCHMARK_CLASS=measurement",
		"BENCHMARK_DRIVER=pgbench",
		"BENCHMARK_EXPERIMENT_SPEC=benchmarks/pgbench",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/tiny",
		"BENCHMARK_PG_CONFIG=" + config,
		"BENCHMARK_MODE=fixed-time",
		"BENCHMARK_SCALE=10",
		"BENCHMARK_CLIENTS=" + clients,
		"BENCHMARK_THREADS=2",
		"BENCHMARK_WARMUP_SECONDS=5",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_TRIALS=10",
		"BENCHMARK_MIN_VALID_TRIALS=8",
		"BENCHMARK_RESET_POLICY=rebuild-per-trial",
		"BENCHMARK_CACHE_REGIME=warm",
		"BENCHMARK_STATISTICS_RESET_POLICY=none",
		"BENCHMARK_STATISTICS_RESET_BOUNDARY=none",
		"BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v1'",
		"BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1",
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified",
		"BENCHMARK_CLIENT_PLACEMENT=same-host",
		"BENCHMARK_RESOURCE_BUDGET_MODE=unbounded",
		"",
	}, "\n")
}

func benchmarkSpecV2(clients string, config string) string {
	content := benchmarkSpec(clients, config)
	content = strings.Replace(content, "BENCHMARK_CACHE_REGIME=warm", strings.Join([]string{
		"BENCHMARK_CONTRACT_VERSION=2",
		"BENCHMARK_CACHE_REGIME=postgres-shared-buffer-warm",
		"BENCHMARK_CACHE_TARGET_RELATIONS='public.branches public.accounts'",
		"BENCHMARK_CACHE_MIN_RESIDENT_PCT=90",
	}, "\n"), 1)
	content = strings.Replace(content, "BENCHMARK_STATISTICS_RESET_POLICY=none", "BENCHMARK_STATISTICS_RESET_POLICY=runner-managed", 1)
	content = strings.Replace(content, "BENCHMARK_STATISTICS_RESET_BOUNDARY=none", "BENCHMARK_STATISTICS_RESET_BOUNDARY=before-measure", 1)
	content = strings.Replace(content, "postgresql-sampler-v1", "postgresql-sampler-v2", 1)
	content = strings.Replace(content, "BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified", strings.Join([]string{
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=runner-calibrated-duty-cycle",
		"BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES=5",
		"BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT=2",
	}, "\n"), 1)
	content = strings.Replace(content, "BENCHMARK_RESOURCE_BUDGET_MODE=unbounded", strings.Join([]string{
		"BENCHMARK_RESOURCE_BUDGET_MODE=runner-enforced",
		"BENCHMARK_CPU_BUDGET_MILLICORES=1500",
		"BENCHMARK_MEMORY_BUDGET_MIB=1024",
		"BENCHMARK_RESOURCE_BUDGET_SCOPE=postgres-server-and-pgbench-driver",
		"BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER=docker-single-container-linux-cgroup-v2",
	}, "\n"), 1)
	return content
}

func floatPointerForTest(value float64) *float64 { return &value }
func intPointerForTest(value int) *int           { return &value }

func writeBenchmarkFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
