package speccatalog

import (
	"bytes"
	"encoding/json"
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
	writeSpec(t, root, "workloads/sql/smoke-run.env", "WORKLOAD_NAME=smoke\nWORKLOAD_KIND=profile-sql\nPROFILE=smoke\nWORKLOAD_SQL=10_run.sql\n")
	writeSpec(t, root, "utility-tests/pg-dump/smoke.env", "UTILITY_TEST_NAME=pg_dump smoke\nUTILITY_TEST_PROFILE=smoke\nUTILITY_TEST_WORKLOAD_SPEC=sql/smoke-run\nUTILITY_TEST_BACKGROUND_SPECS=sql/smoke-run\nUTILITY_TEST_METRICS=1\n")

	if errs := New(root).Validate("utility-test", nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}

	writeSpec(t, root, "utility-tests/broken.env", "UTILITY_TEST_NAME=broken\nUTILITY_TEST_PROFILE=missing\nUTILITY_TEST_WORKLOAD_SPEC=missing\nUTILITY_TEST_BACKGROUND_SPECS=also-missing\nUTILITY_TEST_METRICS=maybe\n")
	errs := New(root).Validate("utility-test", []string{"broken"})
	if len(errs) != 4 {
		t.Fatalf("expected four validation errors, got %#v", errs)
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
		"profile-sql, sql, pgbench, pg-source-check, noisia, shell, compose-run",
		"`PG_PATCHSET`",
		"## experiment",
		"`EXPERIMENT_NAME`",
		"## dataset",
		"`DATASET_KIND`",
		"sql, profile, pgbench",
		"## utility-test",
		"`UTILITY_TEST_WORKLOAD_SPEC`",
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
	if len(enum) != 7 || enum[0] != "profile-sql" || enum[6] != "compose-run" {
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
	for _, kind := range []string{"workload", "experiment", "matrix", "topology", "dataset", "utility-test"} {
		if _, ok := defs[kind]; !ok {
			t.Fatalf("missing $defs schema for %s", kind)
		}
	}
	if !strings.Contains(out.String(), "runs/matrices/<id>") {
		t.Fatalf("schema output escaped matrix run dir default:\n%s", out.String())
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
