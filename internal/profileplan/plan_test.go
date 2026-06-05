package profileplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/profilecatalog"
)

func TestBuildDefaultResetPlan(t *testing.T) {
	root := t.TempDir()
	writeProfileFile(t, root, "profiles/smoke/profile.env", strings.Join([]string{
		`PROFILE_NAME="smoke"`,
		`PROFILE_DESCRIPTION="Smoke profile."`,
		`PROFILE_DEFAULT_SIZE="small"`,
		`PROFILE_SIZES="small medium"`,
		"",
	}, "\n"))
	writeProfileFile(t, root, "profiles/smoke/README.md", "# Smoke\n")
	writeProfileFile(t, root, "profiles/smoke/sql/00_setup.sql", "SELECT 1;\n")
	writeProfileFile(t, root, "profiles/smoke/sql/10_run.sql", "SELECT 2;\n")

	plan, err := Build(root, profilecatalog.New(root), "smoke", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Profile != "smoke" || plan.Size != "small" || plan.Seconds != "30" {
		t.Fatalf("unexpected plan metadata: %#v", plan)
	}
	if len(plan.SQL) != 2 || plan.SQL[0].Name != "00_setup.sql" || plan.SQL[1].Name != "10_run.sql" {
		t.Fatalf("unexpected SQL steps: %#v", plan.SQL)
	}
}

func TestBuildSpecificSQLPlan(t *testing.T) {
	root := t.TempDir()
	writeProfileFile(t, root, "profiles/locks/profile.env", strings.Join([]string{
		`PROFILE_NAME="locks"`,
		`PROFILE_DESCRIPTION="Locks profile."`,
		`PROFILE_DEFAULT_SIZE="small"`,
		`PROFILE_SIZES="small medium"`,
		"",
	}, "\n"))
	writeProfileFile(t, root, "profiles/locks/README.md", "# Locks\n")
	writeProfileFile(t, root, "profiles/locks/sql/00_setup.sql", "SELECT 1;\n")
	writeProfileFile(t, root, "profiles/locks/sql/10_run.sql", "SELECT 2;\n")
	writeProfileFile(t, root, "profiles/locks/sql/30_diagnostics.sql", "SELECT 3;\n")

	plan, err := Build(root, profilecatalog.New(root), "locks", Options{
		Size:    "medium",
		Seconds: "90",
		SQL:     []string{"30_diagnostics.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.SQL) != 1 || plan.SQL[0].Name != "30_diagnostics.sql" {
		t.Fatalf("unexpected SQL steps: %#v", plan.SQL)
	}
	command := strings.Join(plan.SQL[0].Command, " ")
	if !strings.Contains(command, "PROFILE_SIZE=medium") || !strings.Contains(command, "PROFILE_SECONDS=90") {
		t.Fatalf("unexpected command: %s", command)
	}

	var out bytes.Buffer
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Profile Plan",
		"| Profile size | medium |",
		"30_diagnostics.sql",
		"PROFILE_SECONDS=90",
	} {
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
	if payload.Profile != "locks" || payload.Size != "medium" || len(payload.SQL) != 1 {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
	if payload.SQL[0].Name != "30_diagnostics.sql" || len(payload.SQL[0].Command) == 0 {
		t.Fatalf("unexpected JSON SQL step: %#v", payload.SQL[0])
	}
}

func TestBuildMissingSQLPlan(t *testing.T) {
	root := t.TempDir()
	writeProfileFile(t, root, "profiles/smoke/profile.env", "PROFILE_NAME=smoke\nPROFILE_DESCRIPTION=Smoke\n")
	writeProfileFile(t, root, "profiles/smoke/README.md", "# Smoke\n")
	writeProfileFile(t, root, "profiles/smoke/sql/00_setup.sql", "SELECT 1;\n")
	writeProfileFile(t, root, "profiles/smoke/sql/10_run.sql", "SELECT 2;\n")

	if _, err := Build(root, profilecatalog.New(root), "smoke", Options{SQL: []string{"missing.sql"}}); err == nil {
		t.Fatal("expected missing SQL error")
	}
}

func writeProfileFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
