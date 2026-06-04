package experimentplan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestBuildRenderExperimentPlan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "configs/default/postgresql.conf", "# default\n")
	writeFile(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeFile(t, root, "profiles/smoke/sql/00_setup.sql", "SELECT 1;\n")
	writeFile(t, root, "profiles/smoke/sql/10_run.sql", "SELECT 1;\n")
	writeFile(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_DESCRIPTION=Single\n")
	writeFile(t, root, "workloads/sql/smoke-run.env", "WORKLOAD_NAME=smoke\nWORKLOAD_KIND=profile-sql\nPROFILE=smoke\nWORKLOAD_SQL=10_run.sql\n")
	writeFile(t, root, "workloads/profile/background.env", "WORKLOAD_NAME=background\nWORKLOAD_KIND=profile-sql\nPROFILE=smoke\nWORKLOAD_SQL=10_run.sql\n")
	writeFile(t, root, "experiments/smoke.env", `EXPERIMENT_NAME="smoke experiment"
EXPERIMENT_TOPOLOGY="single"
EXPERIMENT_PG_CONFIG="default"
EXPERIMENT_PROFILE="smoke"
EXPERIMENT_PROFILE_SIZE="${EXPERIMENT_PROFILE_SIZE:-small}"
EXPERIMENT_PROFILE_SETUP=1
EXPERIMENT_BACKGROUND_SPECS="profile/background"
EXPERIMENT_BACKGROUND_WARMUP=2
EXPERIMENT_WORKLOAD_SPEC="sql/smoke-run"
EXPERIMENT_METRICS=1
EXPERIMENT_METRICS_SAMPLES=2
EXPERIMENT_SNAPSHOT="${EXPERIMENT_SNAPSHOT:-1}"
EXPERIMENT_ASSERT_SQL="SELECT 1;"
`)

	plan, err := Build(speccatalog.New(root), "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fields["name"] != "smoke experiment" || plan.Fields["workload"] != "sql/smoke-run" || plan.Fields["profile_size"] != "small" {
		t.Fatalf("unexpected plan fields: %#v", plan.Fields)
	}

	var out bytes.Buffer
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	content := out.String()
	for _, want := range []string{
		"# Experiment Plan",
		"| Topology | single |",
		"| foreground workload | yes | sql/smoke-run |",
		"| assertions | yes | EXPERIMENT_ASSERT_SQL=`SELECT 1;` |",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("plan output missing %q:\n%s", want, content)
		}
	}
}

func TestBuildRejectsInvalidExperiment(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "experiments/broken.env", "EXPERIMENT_NAME=broken\nEXPERIMENT_TOPOLOGY=missing\n")

	if _, err := Build(speccatalog.New(root), "broken"); err == nil {
		t.Fatal("expected validation error")
	}
}

func writeFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
