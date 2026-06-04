package matrixplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestBuildMatrixPlan(t *testing.T) {
	root := t.TempDir()
	writeMatrixSpec(t, root, "configs/default/postgresql.conf", "# default\n")
	writeMatrixSpec(t, root, "configs/debug/postgresql.conf", "# debug\n")
	writeMatrixSpec(t, root, "topologies/single.env", "TOPOLOGY_NAME=single\nTOPOLOGY_DESCRIPTION=Single.\n")
	writeMatrixSpec(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\nEXPERIMENT_TOPOLOGY=single\n")
	writeMatrixSpec(t, root, "experiments/locks.env", "EXPERIMENT_NAME=locks\nEXPERIMENT_TOPOLOGY=single\n")
	writeMatrixSpec(t, root, "matrices/full.env", strings.Join([]string{
		`MATRIX_NAME="full matrix"`,
		`MATRIX_EXPERIMENTS="smoke locks"`,
		`MATRIX_PG_CONFIGS="default debug"`,
		`MATRIX_PROFILE_SIZES="small medium"`,
		`MATRIX_REPEATS=2`,
		"",
	}, "\n"))

	plan, err := Build(speccatalog.New(root), "full")
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalRuns != 16 {
		t.Fatalf("unexpected total runs: %#v", plan)
	}
	first := plan.Runs[0]
	if first.Experiment != "smoke" || first.PGConfig != "default" || first.ProfileSize != "small" || first.Repeat != 1 {
		t.Fatalf("unexpected first run: %#v", first)
	}
	last := plan.Runs[len(plan.Runs)-1]
	if last.Experiment != "locks" || last.PGConfig != "debug" || last.ProfileSize != "medium" || last.Repeat != 2 {
		t.Fatalf("unexpected last run: %#v", last)
	}
}

func TestRenderMatrixPlan(t *testing.T) {
	plan := Plan{
		Spec:      "smoke",
		Name:      "smoke matrix",
		TotalRuns: 1,
		Runs: []PlanEntry{{
			Experiment:  "smoke",
			PGConfig:    "default",
			ProfileSize: "small",
			Repeat:      1,
		}},
	}
	var out bytes.Buffer
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Experiment Matrix Plan",
		"Total runs: `1`",
		"| `smoke` | `default` | `small` | `1` |",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered plan missing %q:\n%s", want, out.String())
		}
	}
}

func TestRenderMatrixPlanJSON(t *testing.T) {
	plan := Plan{
		Spec:      "smoke",
		Name:      "smoke matrix",
		TotalRuns: 1,
		Runs: []PlanEntry{{
			Experiment:  "smoke",
			PGConfig:    "default",
			ProfileSize: "small",
			Repeat:      1,
		}},
	}
	var out bytes.Buffer
	if err := RenderJSON(&out, plan); err != nil {
		t.Fatal(err)
	}
	var decoded Plan
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TotalRuns != 1 || len(decoded.Runs) != 1 {
		t.Fatalf("unexpected JSON plan: %#v", decoded)
	}
}

func writeMatrixSpec(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
