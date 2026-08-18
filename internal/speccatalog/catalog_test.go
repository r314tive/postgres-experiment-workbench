package speccatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogListShowValidate(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeSpec(t, root, "profiles/smoke/sql/10_run.sql", "SELECT 1;\n")
	writeSpec(t, root, "configs/default/postgresql.conf", "# default\n")
	writeSpec(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_DESCRIPTION=One PostgreSQL container.\n")
	writeSpec(t, root, "workloads/sql/smoke-run.env", "WORKLOAD_NAME=smoke\nWORKLOAD_KIND=profile-sql\nPROFILE=smoke\nWORKLOAD_SQL=10_run.sql\n")
	writeSpec(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\nEXPERIMENT_TOPOLOGY=single\nEXPERIMENT_PG_CONFIG=default\nEXPERIMENT_PROFILE=smoke\nEXPERIMENT_WORKLOAD_SPEC=sql/smoke-run\n")
	writeSpec(t, root, "matrices/smoke.env", "MATRIX_NAME=smoke\nMATRIX_EXPERIMENTS=smoke\nMATRIX_PG_CONFIGS=default\nMATRIX_PROFILE_SIZES=small\nMATRIX_REPEATS=1\n")
	writeSpec(t, root, "datasets/synthetic/items.env", "DATASET_NAME=items\nDATASET_KIND=sql\nDATASET_SQL=sql/datasets/synthetic_items.sql\n")
	writeSpec(t, root, "utility-tests/pg-dump/smoke.env", "UTILITY_TEST_NAME=pg_dump smoke\nUTILITY_TEST_PROFILE=smoke\nUTILITY_TEST_WORKLOAD_SPEC=sql/smoke-run\nUTILITY_TEST_METRICS=1\n")
	writeSpec(t, root, "utility-suites/native.env", "UTILITY_SUITE_NAME=native\nUTILITY_SUITE_TESTS=pg-dump/smoke\nUTILITY_SUITE_PROFILE_SIZES=small\nUTILITY_SUITE_REPEATS=1\n")
	writeSpec(t, root, "sql/datasets/synthetic_items.sql", "SELECT 1;\n")

	catalog := New(root)
	specs, err := catalog.List("workload")
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0] != "sql/smoke-run" {
		t.Fatalf("unexpected specs: %#v", specs)
	}

	spec, err := catalog.Show("experiment", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Values["EXPERIMENT_WORKLOAD_SPEC"] != "sql/smoke-run" {
		t.Fatalf("unexpected spec values: %#v", spec.Values)
	}

	if errs := catalog.Validate("all", nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
}

func TestCatalogRawListAndShowMatchShellAdapters(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/pg-source/check-world.env", "WORKLOAD_NAME=check world\nWORKLOAD_KIND=pg-source-check\n")
	writeSpec(t, root, "workloads/pg-source/check.env", "WORKLOAD_NAME=check\nWORKLOAD_KIND=pg-source-check\n")

	catalog := New(root)
	specs, err := catalog.ListRaw("workload")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pg-source/check-world", "pg-source/check"}
	if len(specs) != len(want) {
		t.Fatalf("unexpected raw specs: %#v", specs)
	}
	for i := range want {
		if specs[i] != want[i] {
			t.Fatalf("unexpected raw specs: %#v", specs)
		}
	}

	content, err := catalog.ShowRaw("workload", "pg-source/check")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "WORKLOAD_NAME=check\nWORKLOAD_KIND=pg-source-check\n" {
		t.Fatalf("unexpected raw content:\n%s", content)
	}
}

func TestCatalogResolveExperimentRequiresPackContainment(t *testing.T) {
	root := t.TempDir()
	inPack := filepath.Join(root, "experiments", "smoke.env")
	writeSpec(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")

	resolved, id, err := New(root).Resolve("experiment", inPack)
	if err != nil {
		t.Fatalf("absolute in-pack experiment must resolve: %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(inPack)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != wantResolved || id != "smoke" {
		t.Fatalf("unexpected in-pack resolution: path=%q id=%q", resolved, id)
	}

	externalRoot := t.TempDir()
	external := filepath.Join(externalRoot, "external.env")
	writeSpec(t, externalRoot, "external.env", "EXPERIMENT_NAME=external\n")
	if _, _, err := New(root).Resolve("experiment", external); err == nil || !strings.Contains(err.Error(), "outside scenario pack experiments") {
		t.Fatalf("expected external absolute experiment rejection, got %v", err)
	}

	link := filepath.Join(root, "experiments", "nested", "escape.env")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := New(root).Resolve("experiment", "escape"); err == nil || !strings.Contains(err.Error(), "outside scenario pack experiments") {
		t.Fatalf("expected experiment symlink escape rejection, got %v", err)
	}

	if _, _, err := New(root).Resolve("experiment", "../external"); err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("expected parent traversal rejection, got %v", err)
	}
}

func TestCatalogValidateBrokenReferences(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "experiments/broken.env", "EXPERIMENT_NAME=broken\nEXPERIMENT_TOPOLOGY=missing\nEXPERIMENT_WORKLOAD_SPEC=missing\n")
	writeSpec(t, root, "workloads/profile/broken.env", "WORKLOAD_NAME=broken\nWORKLOAD_KIND=profile-sql\nPROFILE=missing\nWORKLOAD_SQL=10_run.sql\n")

	errs := New(root).Validate("all", nil)
	if len(errs) < 2 {
		t.Fatalf("expected validation errors, got %#v", errs)
	}
}

func TestCatalogValidateDatasetProfile(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeSpec(t, root, "datasets/profile/smoke.env", "DATASET_NAME=smoke\nDATASET_KIND=profile\nDATASET_PROFILE=smoke\n")

	if errs := New(root).Validate("dataset", nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
}

func TestCatalogValidateUtilityTestReferences(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeSpec(t, root, "profiles/smoke/sql/10_run.sql", "SELECT 1;\n")
	writeSpec(t, root, "sql/assertions/smoke.sql", "SELECT 1;\n")
	writeSpec(t, root, "workloads/sql/smoke-run.env", "WORKLOAD_NAME=smoke\nWORKLOAD_KIND=profile-sql\nPROFILE=smoke\nWORKLOAD_SQL=10_run.sql\n")
	writeSpec(t, root, "utility-tests/pg-dump/smoke.env", "UTILITY_TEST_NAME=pg_dump smoke\nUTILITY_TEST_PROFILE=smoke\nUTILITY_TEST_WORKLOAD_SPEC=sql/smoke-run\nUTILITY_TEST_BACKGROUND_SPECS=sql/smoke-run\nUTILITY_TEST_ASSERT_SQL_FILES=sql/assertions/smoke.sql\nUTILITY_TEST_METRICS=1\n")

	if errs := New(root).Validate("utility-test", nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}

	writeSpec(t, root, "utility-tests/broken.env", "UTILITY_TEST_NAME=broken\nUTILITY_TEST_PROFILE=missing\nUTILITY_TEST_WORKLOAD_SPEC=missing\nUTILITY_TEST_BACKGROUND_SPECS=also-missing\nUTILITY_TEST_ASSERT_SQL_FILES=missing.sql\nUTILITY_TEST_METRICS=maybe\n")
	errs := New(root).Validate("utility-test", []string{"broken"})
	if len(errs) != 5 {
		t.Fatalf("expected five validation errors, got %#v", errs)
	}
}

func TestCatalogValidateUtilityTestTrustedShell(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/noop.env", "WORKLOAD_NAME=noop\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo noop'\n")

	writeSpec(t, root, "utility-tests/sql-only.env", "UTILITY_TEST_NAME=sql-only\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\nUTILITY_TEST_ASSERT_TRUE_SQL='SELECT true'\n")
	if errs := New(root).Validate("utility-test", []string{"sql-only"}); len(errs) != 0 {
		t.Fatalf("SQL-only utility assertions must not require shell trust: %#v", errs)
	}

	writeSpec(t, root, "utility-tests/trusted.env", "UTILITY_TEST_NAME=trusted\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\nUTILITY_TEST_TRUSTED_SHELL=1\nUTILITY_TEST_EXPECT_FILES=out.log\nUTILITY_TEST_ASSERT_SHELL=true\n")
	if errs := New(root).Validate("utility-test", []string{"trusted"}); len(errs) != 0 {
		t.Fatalf("unexpected trusted-shell validation errors: %#v", errs)
	}

	writeSpec(t, root, "utility-tests/untrusted.env", "UTILITY_TEST_NAME=untrusted\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\nUTILITY_TEST_ASSERT_SHELL=true\n")
	errs := New(root).Validate("utility-test", []string{"untrusted"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "UTILITY_TEST_ASSERT_SHELL require UTILITY_TEST_TRUSTED_SHELL=1") {
		t.Fatalf("unexpected untrusted-shell validation errors: %#v", errs)
	}

	writeSpec(t, root, "utility-tests/untrusted-files.env", "UTILITY_TEST_NAME=untrusted-files\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\nUTILITY_TEST_EXPECT_FILES=out.log\n")
	errs = New(root).Validate("utility-test", []string{"untrusted-files"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "UTILITY_TEST_EXPECT_FILES require UTILITY_TEST_TRUSTED_SHELL=1") {
		t.Fatalf("unexpected expected-files validation errors: %#v", errs)
	}

	writeSpec(t, root, "utility-tests/invalid.env", "UTILITY_TEST_NAME=invalid\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\nUTILITY_TEST_TRUSTED_SHELL=yes\n")
	errs = New(root).Validate("utility-test", []string{"invalid"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "UTILITY_TEST_TRUSTED_SHELL must be 0 or 1") {
		t.Fatalf("unexpected trust-marker validation errors: %#v", errs)
	}

	writeSpec(t, root, "utility-tests/dynamic.env", "UTILITY_TEST_NAME=dynamic\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\nUTILITY_TEST_TRUSTED_SHELL=\"${UTILITY_TEST_TRUSTED_SHELL:-0}\"\nUTILITY_TEST_ASSERT_SHELL=true\n")
	if errs := New(root).Validate("utility-test", []string{"dynamic"}); len(errs) != 0 {
		t.Fatalf("dynamic trust marker must be deferred to the runner: %#v", errs)
	}
}

func TestCatalogValidateUtilitySuiteReferences(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/noop.env", "WORKLOAD_NAME=noop\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo noop'\n")
	writeSpec(t, root, "utility-tests/pg-dump/smoke.env", "UTILITY_TEST_NAME=pg_dump smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/noop\n")
	writeSpec(t, root, "utility-suites/native.env", "UTILITY_SUITE_NAME=native\nUTILITY_SUITE_TESTS=pg-dump/smoke\nUTILITY_SUITE_PROFILE_SIZES=small medium\nUTILITY_SUITE_REPEATS=2\nUTILITY_SUITE_STOP_ON_FAIL=1\nUTILITY_SUITE_SNAPSHOT=0\n")

	if errs := New(root).Validate("utility-suite", nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}

	writeSpec(t, root, "utility-suites/broken.env", "UTILITY_SUITE_NAME=broken\nUTILITY_SUITE_TESTS=missing\nUTILITY_SUITE_PROFILE_SIZES=huge\nUTILITY_SUITE_REPEATS=0\nUTILITY_SUITE_STOP_ON_FAIL=maybe\nUTILITY_SUITE_SNAPSHOT=maybe\n")
	errs := New(root).Validate("utility-suite", []string{"broken"})
	if len(errs) != 5 {
		t.Fatalf("expected five validation errors, got %#v", errs)
	}
}

func TestCatalogValidateExperimentStateWriter(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "configs/default/postgresql.conf", "# default\n")
	writeSpec(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_DESCRIPTION=One PostgreSQL container.\n")
	writeSpec(t, root, "experiments/broken.env", "EXPERIMENT_NAME=broken\nEXPERIMENT_STATE_WRITER=python\n")

	errs := New(root).Validate("experiment", nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "unsupported EXPERIMENT_STATE_WRITER") {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
}

func TestCatalogValidateExperimentTimeout(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "configs/default/postgresql.conf", "# default\n")
	writeSpec(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_DESCRIPTION=One PostgreSQL container.\n")
	writeSpec(t, root, "experiments/valid.env", "EXPERIMENT_NAME=valid\nEXPERIMENT_TIMEOUT=45m\n")
	if errs := New(root).Validate("experiment", []string{"valid"}); len(errs) != 0 {
		t.Fatalf("valid timeout rejected: %#v", errs)
	}
	for name, value := range map[string]string{"invalid": "soon", "too-small": "500ms"} {
		writeSpec(t, root, "experiments/"+name+".env", "EXPERIMENT_NAME="+name+"\nEXPERIMENT_TIMEOUT="+value+"\n")
		errs := New(root).Validate("experiment", []string{name})
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "EXPERIMENT_TIMEOUT") {
			t.Fatalf("unexpected timeout validation for %s: %#v", name, errs)
		}
	}
}

func TestCatalogValidateExperimentTrustedShellHooks(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "configs/default/postgresql.conf", "# default\n")
	writeSpec(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_DESCRIPTION=One PostgreSQL container.\n")

	writeSpec(t, root, "experiments/sql-only.env", "EXPERIMENT_NAME=sql-only\nEXPERIMENT_ASSERT_TRUE_SQL='SELECT true'\n")
	if errs := New(root).Validate("experiment", []string{"sql-only"}); len(errs) != 0 {
		t.Fatalf("SQL-only assertions must not require shell trust: %#v", errs)
	}

	writeSpec(t, root, "experiments/trusted.env", "EXPERIMENT_NAME=trusted\nEXPERIMENT_TRUSTED_SHELL=1\nEXPERIMENT_BEFORE_SHELL=true\nEXPERIMENT_AFTER_SHELL=true\nEXPERIMENT_ASSERT_SHELL=true\n")
	if errs := New(root).Validate("experiment", []string{"trusted"}); len(errs) != 0 {
		t.Fatalf("unexpected trusted-shell validation errors: %#v", errs)
	}

	writeSpec(t, root, "experiments/untrusted.env", "EXPERIMENT_NAME=untrusted\nEXPERIMENT_BEFORE_SHELL=true\n")
	errs := New(root).Validate("experiment", []string{"untrusted"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "EXPERIMENT_BEFORE_SHELL require EXPERIMENT_TRUSTED_SHELL=1") {
		t.Fatalf("unexpected untrusted-shell validation errors: %#v", errs)
	}

	writeSpec(t, root, "experiments/disabled.env", "EXPERIMENT_NAME=disabled\nEXPERIMENT_TRUSTED_SHELL=0\nEXPERIMENT_ASSERT_SHELL=true\n")
	errs = New(root).Validate("experiment", []string{"disabled"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "EXPERIMENT_ASSERT_SHELL require EXPERIMENT_TRUSTED_SHELL=1") {
		t.Fatalf("unexpected disabled-shell validation errors: %#v", errs)
	}

	writeSpec(t, root, "experiments/invalid.env", "EXPERIMENT_NAME=invalid\nEXPERIMENT_TRUSTED_SHELL=yes\n")
	errs = New(root).Validate("experiment", []string{"invalid"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "EXPERIMENT_TRUSTED_SHELL must be 0 or 1") {
		t.Fatalf("unexpected trust-marker validation errors: %#v", errs)
	}

	writeSpec(t, root, "experiments/dynamic.env", "EXPERIMENT_NAME=dynamic\nEXPERIMENT_TRUSTED_SHELL=\"${EXPERIMENT_TRUSTED_SHELL:-0}\"\nEXPERIMENT_BEFORE_SHELL=true\n")
	if errs := New(root).Validate("experiment", []string{"dynamic"}); len(errs) != 0 {
		t.Fatalf("dynamic trust marker must be deferred to the runner: %#v", errs)
	}
}

func TestCatalogValidatePgSourcePatchset(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "patchsets/chaos/master/patchset.env", "PATCHSET_NAME=chaos/master\nPATCHSET_DESCRIPTION=Chaos\nPATCHSET_PG_REF=master\nPATCHSET_ALLOW_EMPTY=1\n")
	writeSpec(t, root, "workloads/pg-source/chaos.env", "WORKLOAD_NAME=chaos\nWORKLOAD_KIND=pg-source-check\nPG_SOURCE_ACTION=plan\nPG_PATCHSET=chaos/master\n")

	if errs := New(root).Validate("workload", nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}

	writeSpec(t, root, "workloads/pg-source/broken.env", "WORKLOAD_NAME=broken\nWORKLOAD_KIND=pg-source-check\nPG_SOURCE_ACTION=explode\nPG_PATCHSET=missing/master\n")
	errs := New(root).Validate("workload", []string{"pg-source/broken"})
	if len(errs) != 2 {
		t.Fatalf("expected two validation errors, got %#v", errs)
	}
}

func TestCatalogValidatePostgreSQLUtilityWorkloads(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "workloads/utility/dump.env", "WORKLOAD_NAME=dump\nWORKLOAD_KIND=pg-dump\nUTILITY_SOURCE_SCHEMA=smoke\nUTILITY_OUTPUT_FILE=logs/utility/dump.sql\n")
	writeSpec(t, root, "workloads/utility/dumpall.env", "WORKLOAD_NAME=dumpall\nWORKLOAD_KIND=pg-dumpall\nUTILITY_OUTPUT_FILE=logs/utility/dumpall.sql\n")
	writeSpec(t, root, "workloads/utility/restore.env", "WORKLOAD_NAME=restore\nWORKLOAD_KIND=pg-restore\nUTILITY_SOURCE_SCHEMA=smoke\nUTILITY_TARGET_SCHEMA=restore_check\nUTILITY_ARCHIVE_FILE=logs/utility/restore.dump\nUTILITY_OUTPUT_FILE=logs/utility/restore.sql\n")

	if errs := New(root).Validate("workload", nil); len(errs) != 0 {
		t.Fatalf("unexpected utility workload validation errors: %#v", errs)
	}

	writeSpec(t, root, "workloads/utility/broken.env", "WORKLOAD_NAME=broken\nWORKLOAD_KIND=pg-restore\nUTILITY_SOURCE_SCHEMA=9bad\nUTILITY_TARGET_SCHEMA=9bad\nUTILITY_ARCHIVE_FILE=../escape.dump\nUTILITY_OUTPUT_FILE=../escape.dump\n")
	errs := New(root).Validate("workload", []string{"utility/broken"})
	joined := errors.Join(errs...).Error()
	for _, want := range []string{
		"UTILITY_ARCHIVE_FILE must be a portable repository-relative file path",
		"UTILITY_OUTPUT_FILE must be a portable repository-relative file path",
		"UTILITY_SOURCE_SCHEMA must be a simple PostgreSQL identifier",
		"UTILITY_TARGET_SCHEMA must be a simple PostgreSQL identifier",
		"UTILITY_OUTPUT_FILE and UTILITY_ARCHIVE_FILE must differ",
		"UTILITY_SOURCE_SCHEMA and UTILITY_TARGET_SCHEMA must differ",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("utility validation errors missing %q: %#v", want, errs)
		}
	}
}

func TestCatalogValidateBenchmarkContract(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "configs/default/postgresql.conf", "# default\n")
	writeSpec(t, root, "experiments/benchmark.env", "EXPERIMENT_NAME=benchmark\nEXPERIMENT_PG_CONFIG=${EXPERIMENT_PG_CONFIG:-default}\nEXPERIMENT_WORKLOAD_SPEC=${EXPERIMENT_WORKLOAD_SPEC:-pgbench/write}\n")
	writeSpec(t, root, "workloads/pgbench/write.env", "WORKLOAD_NAME=write\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=builtin\n")
	writeSpec(t, root, "workloads/pgbench/read.env", "WORKLOAD_NAME=read\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=select-only\n")
	writeSpec(t, root, "workloads/pgbench/dynamic.env", "WORKLOAD_NAME=dynamic\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=${PGBENCH_MODE:-builtin}\n")
	writeSpec(t, root, "workloads/sql/not-pgbench.env", "WORKLOAD_NAME=sql\nWORKLOAD_KIND=sql\nSQL=sql/noop.sql\n")
	writeSpec(t, root, "sql/noop.sql", "SELECT 1;\n")
	writeSpec(t, root, "benchmarks/valid.env", strings.Join([]string{
		"BENCHMARK_NAME=valid",
		"BENCHMARK_EXPERIMENT_SPEC=benchmark",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/write",
		"BENCHMARK_SCALE=10",
		"BENCHMARK_CLIENTS=8",
		"BENCHMARK_THREADS=4",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_TRIALS=10",
		"BENCHMARK_MIN_VALID_TRIALS=8",
		"BENCHMARK_CACHE_REGIME=warm",
		"BENCHMARK_STATISTICS_RESET_POLICY=none",
		"BENCHMARK_STATISTICS_RESET_BOUNDARY=none",
		"BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v1'",
		"BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1",
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=included-unquantified",
		"BENCHMARK_CLIENT_PLACEMENT=same-host",
		"BENCHMARK_RESOURCE_BUDGET_MODE=unbounded",
		"BENCHMARK_LOG_TRANSACTIONS=1",
		"BENCHMARK_LOG_SAMPLE_RATE=0.25",
		"BENCHMARK_MAX_TRIES=0",
		"",
	}, "\n"))
	if errs := New(root).Validate("benchmark", []string{"valid"}); len(errs) != 0 {
		t.Fatalf("unexpected benchmark validation errors: %#v", errs)
	}

	cases := []struct {
		id      string
		content string
		want    string
	}{
		{
			id: "wrong-driver",
			content: "BENCHMARK_NAME=x\nBENCHMARK_DRIVER=benchbase\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\n",
			want: "BENCHMARK_DRIVER must be pgbench",
		},
		{
			id: "wrong-workload",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=sql/not-pgbench\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\n",
			want: "must use WORKLOAD_KIND=pgbench",
		},
		{
			id: "fixed-time-transactions",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_TRANSACTIONS_PER_CLIENT=10\n",
			want: "incompatible with BENCHMARK_MODE=fixed-time",
		},
		{
			id: "fixed-transactions-time",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_MODE=fixed-transactions\nBENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_TRANSACTIONS_PER_CLIENT=10\nBENCHMARK_MEASURE_SECONDS=1\n",
			want: "incompatible with BENCHMARK_MODE=fixed-transactions",
		},
		{
			id: "minimum",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_TRIALS=2\nBENCHMARK_MIN_VALID_TRIALS=3\n",
			want: "must not exceed BENCHMARK_TRIALS",
		},
		{
			id: "reuse-write",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_RESET_POLICY=reuse-readonly\n",
			want: "requires a pgbench select-only workload",
		},
		{
			id: "direction",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/read\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_PRIMARY_METRIC=pgbench.latency_mean_us\nBENCHMARK_DIRECTION=higher\n",
			want: "is inconsistent with pgbench.latency_mean_us",
		},
		{
			id: "dynamic",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=${BENCHMARK_SCALE:-1}\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\n",
			want: "must be a static value",
		},
		{
			id: "difference",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_ALLOWED_SUBJECT_DIFFERENCES=clients\n",
			want: "supports pg_config or native_toolchain only",
		},
		{
			id: "sample-rate",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_LOG_SAMPLE_RATE=1.1\n",
			want: "at most 1",
		},
		{
			id: "unbounded-retries-without-bound",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_MODE=fixed-transactions\nBENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_TRANSACTIONS_PER_CLIENT=10\nBENCHMARK_MAX_TRIES=0\n",
			want: "BENCHMARK_MAX_TRIES=0 requires",
		},
		{
			id: "integer-overflow",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=999999999999999999999999999999\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\n",
			want: "BENCHMARK_CLIENTS must be a positive integer",
		},
		{
			id: "seed-overflow",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_RANDOM_SEED=18446744073709551616\n",
			want: "BENCHMARK_RANDOM_SEED must be a non-negative integer",
		},
		{
			id: "dynamic-workload-protocol",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/dynamic\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\n",
			want: "PGBENCH_MODE in benchmark workload pgbench/dynamic must be static",
		},
		{
			id: "statistics-reset-boundary",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\n" +
				"BENCHMARK_STATISTICS_RESET_POLICY=none\nBENCHMARK_STATISTICS_RESET_BOUNDARY=before-measure\n",
			want: "is inconsistent with BENCHMARK_STATISTICS_RESET_BOUNDARY",
		},
		{
			id: "resource-budget",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/write\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\n" +
				"BENCHMARK_RESOURCE_BUDGET_MODE=operator-declared\nBENCHMARK_CPU_BUDGET_CORES=2\n",
			want: "requires positive BENCHMARK_MEMORY_BUDGET_MIB",
		},
		{
			id: "connection-churn-flag",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/read\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_CONNECT_PER_TRANSACTION=2\n",
			want: "BENCHMARK_CONNECT_PER_TRANSACTION must be 0 or 1",
		},
		{
			id: "connection-churn-tps",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/read\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_CONNECT_PER_TRANSACTION=1\n",
			want: "requires BENCHMARK_PRIMARY_METRIC=pgbench.latency_mean_us",
		},
		{
			id: "latency-budget-without-limit",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/read\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT=1\n",
			want: "requires BENCHMARK_LATENCY_LIMIT_MS",
		},
		{
			id: "latency-budget-range",
			content: "BENCHMARK_NAME=x\nBENCHMARK_EXPERIMENT_SPEC=benchmark\nBENCHMARK_WORKLOAD_SPEC=pgbench/read\n" +
				"BENCHMARK_SCALE=1\nBENCHMARK_CLIENTS=1\nBENCHMARK_THREADS=1\nBENCHMARK_MEASURE_SECONDS=1\nBENCHMARK_LATENCY_LIMIT_MS=50\nBENCHMARK_MAX_LATENCY_LIMIT_EXCEEDED_PCT=101\n",
			want: "must be a decimal in [0,100]",
		},
	}

	for _, test := range cases {
		t.Run(test.id, func(t *testing.T) {
			writeSpec(t, root, "benchmarks/"+test.id+".env", test.content)
			errs := New(root).Validate("benchmark", []string{test.id})
			if len(errs) == 0 || !strings.Contains(errors.Join(errs...).Error(), test.want) {
				t.Fatalf("benchmark validation missing %q: %#v", test.want, errs)
			}
		})
	}
}

func TestCatalogValidateBenchmarkV2ControlsAndRejectLegacyModes(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "configs/default/postgresql.conf", "# default\n")
	writeSpec(t, root, "experiments/benchmark.env", "EXPERIMENT_NAME=benchmark\nEXPERIMENT_PG_CONFIG=${EXPERIMENT_PG_CONFIG:-default}\nEXPERIMENT_WORKLOAD_SPEC=${EXPERIMENT_WORKLOAD_SPEC:-pgbench/write}\n")
	writeSpec(t, root, "workloads/pgbench/write.env", "WORKLOAD_NAME=write\nWORKLOAD_KIND=pgbench\nPGBENCH_MODE=builtin\n")
	base := strings.Join([]string{
		"BENCHMARK_CONTRACT_VERSION=2",
		"BENCHMARK_NAME=v2",
		"BENCHMARK_EXPERIMENT_SPEC=benchmark",
		"BENCHMARK_WORKLOAD_SPEC=pgbench/write",
		"BENCHMARK_SCALE=10",
		"BENCHMARK_CLIENTS=8",
		"BENCHMARK_THREADS=4",
		"BENCHMARK_MEASURE_SECONDS=30",
		"BENCHMARK_CACHE_REGIME=postgres-shared-buffer-warm",
		"BENCHMARK_CACHE_TARGET_RELATIONS='public.accounts public.branches'",
		"BENCHMARK_CACHE_MIN_RESIDENT_PCT=90",
		"BENCHMARK_STATISTICS_RESET_POLICY=runner-managed",
		"BENCHMARK_STATISTICS_RESET_BOUNDARY=before-measure",
		"BENCHMARK_COLLECTORS='pgbench-driver postgresql-sampler-v2'",
		"BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1",
		"BENCHMARK_COLLECTOR_OVERHEAD_MODE=runner-calibrated-duty-cycle",
		"BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES=5",
		"BENCHMARK_COLLECTOR_MAX_DUTY_CYCLE_PCT=2",
		"BENCHMARK_CLIENT_PLACEMENT=same-host",
		"BENCHMARK_RESOURCE_BUDGET_MODE=runner-enforced",
		"BENCHMARK_CPU_BUDGET_MILLICORES=1500",
		"BENCHMARK_MEMORY_BUDGET_MIB=1024",
		"BENCHMARK_RESOURCE_BUDGET_SCOPE=postgres-server-and-pgbench-driver",
		"BENCHMARK_RESOURCE_ENFORCEMENT_PROVIDER=docker-single-container-linux-cgroup-v2",
		"",
	}, "\n")
	writeSpec(t, root, "benchmarks/valid-v2.env", base)
	if errs := New(root).Validate("benchmark", []string{"valid-v2"}); len(errs) != 0 {
		t.Fatalf("unexpected v2 validation errors: %#v", errs)
	}

	tests := []struct{ name, old, replacement, want string }{
		{"cache legacy", "BENCHMARK_CACHE_REGIME=postgres-shared-buffer-warm", "BENCHMARK_CACHE_REGIME=warm", "must be uncontrolled or postgres-shared-buffer-warm"},
		{"cache missing threshold", "BENCHMARK_CACHE_MIN_RESIDENT_PCT=90\n", "", "requires BENCHMARK_CACHE_MIN_RESIDENT_PCT"},
		{"cache invalid relation", "public.accounts public.branches", "public.accounts public.\"branches\"", "contains invalid relation"},
		{"cache unqualified relation", "public.accounts public.branches", "accounts public.branches", "contains invalid relation"},
		{"reset legacy", "BENCHMARK_STATISTICS_RESET_POLICY=runner-managed", "BENCHMARK_STATISTICS_RESET_POLICY=operator-managed", "must be none or runner-managed"},
		{"collector legacy", "postgresql-sampler-v2", "postgresql-sampler-v1", "must contain exactly"},
		{"overhead legacy", "runner-calibrated-duty-cycle", "operator-calibrated", "must be included-unquantified or runner-calibrated-duty-cycle"},
		{"overhead missing samples", "BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES=5\n", "", "requires BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES in [1,10000]"},
		{"overhead samples unbounded", "BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES=5", "BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES=10001", "requires BENCHMARK_COLLECTOR_OVERHEAD_SAMPLES in [1,10000]"},
		{"collector interval unbounded", "BENCHMARK_COLLECTOR_INTERVAL_SECONDS=1", "BENCHMARK_COLLECTOR_INTERVAL_SECONDS=3601", "must be in [1,3600]"},
		{"resource legacy", "BENCHMARK_RESOURCE_BUDGET_MODE=runner-enforced", "BENCHMARK_RESOURCE_BUDGET_MODE=operator-declared", "must be unbounded or runner-enforced"},
		{"resource legacy CPU", "BENCHMARK_CPU_BUDGET_MILLICORES=1500", "BENCHMARK_CPU_BUDGET_CORES=1.5", "declaration-only v1 syntax"},
		{"resource provider", "docker-single-container-linux-cgroup-v2", "linux-cgroup-v2", "must be docker-single-container-linux-cgroup-v2"},
		{"resource placement", "BENCHMARK_CLIENT_PLACEMENT=same-host", "BENCHMARK_CLIENT_PLACEMENT=remote-host", "require BENCHMARK_CLIENT_PLACEMENT=same-host"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writeSpec(t, root, "benchmarks/rejected.env", strings.Replace(base, test.old, test.replacement, 1))
			errs := New(root).Validate("benchmark", []string{"rejected"})
			if len(errs) == 0 || !strings.Contains(errors.Join(errs...).Error(), test.want) {
				t.Fatalf("v2 validation missing %q: %#v", test.want, errs)
			}
		})
	}

	writeSpec(t, root, "benchmarks/v1-with-v2-field.env", strings.Replace(base, "BENCHMARK_CONTRACT_VERSION=2", "BENCHMARK_CONTRACT_VERSION=1", 1))
	if errs := New(root).Validate("benchmark", []string{"v1-with-v2-field"}); len(errs) == 0 || !strings.Contains(errors.Join(errs...).Error(), "requires BENCHMARK_CONTRACT_VERSION=2") {
		t.Fatalf("v1 silently accepted v2 control fields: %#v", errs)
	}
}

func TestRenderReference(t *testing.T) {
	var out bytes.Buffer
	if err := RenderReference(&out, "all"); err != nil {
		t.Fatal(err)
	}
	content := out.String()
	for _, want := range []string{
		"# Env Spec Reference",
		"## workload",
		"`WORKLOAD_KIND`",
		"profile-sql, sql, pgbench, pg-dump, pg-dumpall, pg-restore, pg-source-check, noisia, shell, compose-run",
		"`PG_PATCHSET`",
		"## experiment",
		"`EXPERIMENT_NAME`",
		"`EXPERIMENT_TRUSTED_SHELL`",
		"## benchmark",
		"`BENCHMARK_PRIMARY_METRIC`",
		"## dataset",
		"`DATASET_KIND`",
		"sql, profile, pgbench",
		"## utility-test",
		"`UTILITY_TEST_WORKLOAD_SPEC`",
		"`UTILITY_TEST_TRUSTED_SHELL`",
		"## utility-suite",
		"`UTILITY_SUITE_TESTS`",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("reference missing %q:\n%s", want, content)
		}
	}
}

func TestRenderSchema(t *testing.T) {
	var out bytes.Buffer
	if err := RenderSchema(&out, "workload"); err != nil {
		t.Fatal(err)
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	required := schema["required"].([]interface{})
	if len(required) < 2 || required[0] != "WORKLOAD_NAME" || required[1] != "WORKLOAD_KIND" {
		t.Fatalf("unexpected required keys: %#v", required)
	}
	properties := schema["properties"].(map[string]interface{})
	kindProperty := properties["WORKLOAD_KIND"].(map[string]interface{})
	enum := kindProperty["enum"].([]interface{})
	if len(enum) != 10 || enum[0] != "profile-sql" || enum[9] != "compose-run" {
		t.Fatalf("unexpected enum: %#v", enum)
	}
	if kindProperty["x-workbench-requirement"] != "required" {
		t.Fatalf("missing requirement metadata: %#v", kindProperty)
	}
}

func TestRenderAllSchemas(t *testing.T) {
	var out bytes.Buffer
	if err := RenderSchema(&out, "all"); err != nil {
		t.Fatal(err)
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	defs := schema["$defs"].(map[string]interface{})
	for _, kind := range []string{"workload", "experiment", "benchmark", "matrix", "topology", "dataset", "utility-test", "utility-suite"} {
		if _, ok := defs[kind]; !ok {
			t.Fatalf("missing $defs schema for %s", kind)
		}
	}
	if !strings.Contains(out.String(), "runs/matrices/<id>") {
		t.Fatalf("schema output escaped matrix run dir default:\n%s", out.String())
	}
	experiment := defs["experiment"].(map[string]interface{})
	properties := experiment["properties"].(map[string]interface{})
	trustedShell := properties["EXPERIMENT_TRUSTED_SHELL"].(map[string]interface{})
	trustedShellEnum := trustedShell["enum"].([]interface{})
	if trustedShell["default"] != "0" || len(trustedShellEnum) != 2 || trustedShellEnum[0] != "0" || trustedShellEnum[1] != "1" {
		t.Fatalf("unexpected trusted-shell schema: %#v", trustedShell)
	}
	utilityTest := defs["utility-test"].(map[string]interface{})
	utilityProperties := utilityTest["properties"].(map[string]interface{})
	utilityTrustedShell := utilityProperties["UTILITY_TEST_TRUSTED_SHELL"].(map[string]interface{})
	utilityTrustedShellEnum := utilityTrustedShell["enum"].([]interface{})
	if utilityTrustedShell["default"] != "0" || len(utilityTrustedShellEnum) != 2 || utilityTrustedShellEnum[0] != "0" || utilityTrustedShellEnum[1] != "1" {
		t.Fatalf("unexpected utility trusted-shell schema: %#v", utilityTrustedShell)
	}
}

func writeSpec(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
