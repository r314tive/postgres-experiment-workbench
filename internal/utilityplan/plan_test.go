package utilityplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestBuildUtilityPlanExpanded(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeSpec(t, root, "profiles/smoke/sql/10_run.sql", "SELECT 1;\n")
	writeSpec(t, root, "datasets/synthetic/items.env", "DATASET_NAME=items\nDATASET_KIND=sql\nDATASET_SQL=sql/datasets/synthetic_items.sql\n")
	writeSpec(t, root, "sql/datasets/synthetic_items.sql", "SELECT 1;\n")
	writeSpec(t, root, "workloads/utility/smoke.env", "WORKLOAD_NAME=utility smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo utility'\n")
	writeSpec(t, root, "workloads/profile/bg.env", "WORKLOAD_NAME=background\nWORKLOAD_KIND=profile-sql\nPROFILE=smoke\nWORKLOAD_SQL=10_run.sql\n")
	writeSpec(t, root, "utility-tests/utility/smoke.env", strings.Join([]string{
		"UTILITY_TEST_NAME=utility smoke",
		"UTILITY_TEST_PROFILE=smoke",
		"UTILITY_TEST_PROFILE_SIZE=small",
		"UTILITY_TEST_PROFILE_SECONDS=15",
		"UTILITY_TEST_DATASET_SPEC=synthetic/items",
		"UTILITY_TEST_DATASET_SIZE=small",
		"UTILITY_TEST_BACKGROUND_SPECS=profile/bg",
		"UTILITY_TEST_BACKGROUND_WARMUP=2",
		"UTILITY_TEST_WORKLOAD_SPEC=utility/smoke",
		"UTILITY_TEST_METRICS=1",
		"UTILITY_TEST_METRICS_SAMPLES=3",
		"UTILITY_TEST_NOTES=review logs",
		"",
	}, "\n"))

	plan, err := BuildExpanded(root, speccatalog.New(root), "utility/smoke")
	if err != nil {
		t.Fatal(err)
	}

	if plan.Fields["workload"] != "utility/smoke" {
		t.Fatalf("unexpected fields: %#v", plan.Fields)
	}
	if len(plan.Phases) != 7 {
		t.Fatalf("unexpected phases: %#v", plan.Phases)
	}
	if len(plan.Previews) != 3 {
		t.Fatalf("unexpected previews: %#v", plan.Previews)
	}

	var rendered bytes.Buffer
	if err := Render(&rendered, plan); err != nil {
		t.Fatal(err)
	}
	content := rendered.String()
	for _, want := range []string{"# Utility Test Plan", "utility workload", "Embedded Previews", "review logs"} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered plan missing %q:\n%s", want, content)
		}
	}

	var jsonOut bytes.Buffer
	if err := RenderJSON(&jsonOut, plan); err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(jsonOut.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["spec"] != "utility/smoke" || payload["name"] != "utility smoke" {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestBuildUtilityPlanRequiresSpec(t *testing.T) {
	root := t.TempDir()
	_, err := Build(speccatalog.New(root), "missing")
	if err == nil || !strings.Contains(err.Error(), "utility-test spec not found") {
		t.Fatalf("expected missing spec error, got %v", err)
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
