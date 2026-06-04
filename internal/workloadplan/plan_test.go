package workloadplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestBuildProfileSQLPlan(t *testing.T) {
	root := t.TempDir()
	writeWorkloadFile(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeWorkloadFile(t, root, "profiles/smoke/README.md", "# Smoke\n")
	writeWorkloadFile(t, root, "profiles/smoke/sql/00_setup.sql", "SELECT 1;\n")
	writeWorkloadFile(t, root, "profiles/smoke/sql/10_run.sql", "SELECT 2;\n")
	writeWorkloadFile(t, root, "workloads/sql/smoke-run.env", strings.Join([]string{
		`WORKLOAD_NAME="smoke profile run SQL"`,
		`WORKLOAD_KIND="profile-sql"`,
		`PROFILE="smoke"`,
		`WORKLOAD_SQL="10_run.sql"`,
		"",
	}, "\n"))

	plan, err := Build(root, speccatalog.New(root), "sql/smoke-run")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID != "sql/smoke-run" || plan.Kind != "profile-sql" || !plan.RequiresPostgres {
		t.Fatalf("unexpected plan metadata: %#v", plan)
	}
	if len(plan.Steps) != 1 || !strings.Contains(strings.Join(plan.Steps[0].Command, " "), "./scripts/run_profile_sql.sh smoke 10_run.sql") {
		t.Fatalf("unexpected steps: %#v", plan.Steps)
	}
}

func TestBuildPgbenchPlan(t *testing.T) {
	root := t.TempDir()
	writeWorkloadFile(t, root, "workloads/pgbench/tiny.env", strings.Join([]string{
		`WORKLOAD_NAME="tiny pgbench builtin workload"`,
		`WORKLOAD_KIND="pgbench"`,
		`PGBENCH_RESET="1"`,
		`PGBENCH_INIT="1"`,
		`PGBENCH_SCALE="2"`,
		`PGBENCH_CLIENTS="3"`,
		`PGBENCH_THREADS="1"`,
		`PGBENCH_TIME="5"`,
		"",
	}, "\n"))

	plan, err := Build(root, speccatalog.New(root), "pgbench/tiny")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected reset/init/run steps, got %#v", plan.Steps)
	}
	command := strings.Join(plan.Steps[2].Command, " ")
	for _, want := range []string{"pgbench", "-c 3", "-T 5"} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q: %s", want, command)
		}
	}
}

func TestRenderShellPlan(t *testing.T) {
	root := t.TempDir()
	writeWorkloadFile(t, root, "workloads/topology/pgbouncer-smoke.env", strings.Join([]string{
		`WORKLOAD_NAME="PgBouncer smoke query"`,
		`WORKLOAD_KIND="shell"`,
		`WORKLOAD_REQUIRES_POSTGRES=0`,
		`WORKLOAD_CMD='"$REPO_DIR/scripts/topology.sh" up pgbouncer'`,
		"",
	}, "\n"))

	plan, err := Build(root, speccatalog.New(root), "topology/pgbouncer-smoke")
	if err != nil {
		t.Fatal(err)
	}
	if plan.RequiresPostgres {
		t.Fatalf("expected no direct PostgreSQL requirement: %#v", plan)
	}
	var out bytes.Buffer
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Workload Plan", "PgBouncer smoke query", "bash -lc", "DATABASE_URL"} {
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
	if payload.ID != "topology/pgbouncer-smoke" || len(payload.Steps) != 1 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func writeWorkloadFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
