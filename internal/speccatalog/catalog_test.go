package speccatalog

import (
	"bytes"
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
		"## experiment",
		"`EXPERIMENT_NAME`",
		"## dataset",
		"`DATASET_KIND`",
		"sql, profile, pgbench",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("reference missing %q:\n%s", want, content)
		}
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
