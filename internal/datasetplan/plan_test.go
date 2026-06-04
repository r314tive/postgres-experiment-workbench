package datasetplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestBuildSQLPlan(t *testing.T) {
	root := t.TempDir()
	writeDatasetFile(t, root, "sql/datasets/items.sql", "SELECT 1;\n")
	writeDatasetFile(t, root, "datasets/synthetic/items.env", strings.Join([]string{
		`DATASET_NAME="synthetic items"`,
		`DATASET_KIND="sql"`,
		`DATASET_SQL="sql/datasets/items.sql"`,
		`DATASET_SCHEMA="dataset_synthetic"`,
		`DATASET_SIZE="small"`,
		`DATASET_SEED="7"`,
		"",
	}, "\n"))

	plan, err := Build(root, speccatalog.New(root), "synthetic/items")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID != "synthetic/items" || plan.Kind != "sql" || !plan.RequiresPostgres {
		t.Fatalf("unexpected plan metadata: %#v", plan)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected one SQL step, got %#v", plan.Steps)
	}
	command := strings.Join(plan.Steps[0].Command, " ")
	for _, want := range []string{"./scripts/psql.sh", "dataset_seed=7", "-f " + filepath.ToSlash(filepath.Join(root, "sql/datasets/items.sql"))} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q: %s", want, command)
		}
	}
}

func TestBuildProfilePlan(t *testing.T) {
	root := t.TempDir()
	writeDatasetFile(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeDatasetFile(t, root, "profiles/smoke/README.md", "# Smoke\n")
	writeDatasetFile(t, root, "profiles/smoke/sql/00_setup.sql", "SELECT 1;\n")
	writeDatasetFile(t, root, "datasets/profiles/smoke.env", strings.Join([]string{
		`DATASET_NAME="smoke profile setup"`,
		`DATASET_KIND="profile"`,
		`DATASET_PROFILE="smoke"`,
		`DATASET_SIZE="medium"`,
		"",
	}, "\n"))

	plan, err := Build(root, speccatalog.New(root), "profiles/smoke")
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(plan.Steps[0].Command, " ")
	if !strings.Contains(command, "PROFILE_SIZE=medium ./scripts/run_profile_sql.sh smoke 00_setup.sql") {
		t.Fatalf("unexpected profile command: %s", command)
	}
	if !strings.Contains(strings.Join(plan.Steps[0].Notes, " "), "profiles/smoke/sql/00_setup.sql") {
		t.Fatalf("unexpected profile notes: %#v", plan.Steps[0].Notes)
	}
}

func TestRenderPgbenchPlan(t *testing.T) {
	root := t.TempDir()
	writeDatasetFile(t, root, "datasets/pgbench/tiny.env", strings.Join([]string{
		`DATASET_NAME="pgbench tiny"`,
		`DATASET_KIND="pgbench"`,
		`DATASET_SCALE="2"`,
		"",
	}, "\n"))

	plan, err := Build(root, speccatalog.New(root), "pgbench/tiny")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Dataset Plan", "pgbench tiny", "Initialize pgbench dataset", "PGBENCH_SCALE=2"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered plan missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := RenderJSON(&out, plan); err != nil {
		t.Fatal(err)
	}
	var payload Plan
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != "pgbench/tiny" || len(payload.Steps) != 1 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func writeDatasetFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
