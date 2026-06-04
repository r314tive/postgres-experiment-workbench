package pgsourceplan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildDefaultPlan(t *testing.T) {
	root := t.TempDir()
	plan, err := Build(root, Options{
		Action:   "plan",
		Env:      map[string]string{},
		Now:      fixedNow,
		CPUCount: func() int { return 8 },
	})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Action != "plan" || plan.Ref != "master" || plan.MakeJobs != "8" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if !strings.HasSuffix(plan.SourceRunDir, filepath.Join("generated", "pg-source", "pg-master-20260102_030405")) {
		t.Fatalf("unexpected source run dir: %s", plan.SourceRunDir)
	}
	if len(plan.PatchFiles) != 0 {
		t.Fatalf("unexpected patch files: %#v", plan.PatchFiles)
	}
}

func TestBuildPlanWithPatchset(t *testing.T) {
	root := t.TempDir()
	writeSourcePlanFile(t, root, "patchsets/chaos/master/patchset.env", strings.Join([]string{
		`PATCHSET_NAME="chaos/master"`,
		`PATCHSET_DESCRIPTION="Chaos source checks."`,
		`PATCHSET_PG_REF="master"`,
		`PATCHSET_ALLOW_EMPTY="1"`,
		`PATCHSET_CONFIGURE_ARGS="--enable-debug --enable-injection-points"`,
		`PATCHSET_BUILD_CFLAGS="-O0 -DUSE_INJECTION_POINTS"`,
		"",
	}, "\n"))

	plan, err := Build(root, Options{
		Action: "plan",
		Env: map[string]string{
			"PG_PATCHSET":      "chaos/master",
			"PG_SOURCE_RUN_ID": "fixed",
		},
		Now:      fixedNow,
		CPUCount: func() int { return 4 },
	})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Patchset != "chaos/master" || plan.PatchsetDescription != "Chaos source checks." {
		t.Fatalf("unexpected patchset fields: %#v", plan)
	}
	if plan.ConfigureArgs != "--enable-debug --enable-injection-points" {
		t.Fatalf("unexpected configure args: %s", plan.ConfigureArgs)
	}
	if plan.BuildCflags != "-O0 -DUSE_INJECTION_POINTS" {
		t.Fatalf("unexpected build cflags: %s", plan.BuildCflags)
	}
}

func TestBuildPlanFromWorkloadSpec(t *testing.T) {
	root := t.TempDir()
	writeSourcePlanFile(t, root, "patchsets/chaos/master/patchset.env", strings.Join([]string{
		`PATCHSET_NAME="chaos/master"`,
		`PATCHSET_DESCRIPTION="Chaos source checks."`,
		`PATCHSET_PG_REF="master"`,
		`PATCHSET_FILES="001.patch"`,
		"",
	}, "\n"))
	writeSourcePlanFile(t, root, "patchsets/chaos/master/001.patch", "diff --git a/a b/a\n")
	writeSourcePlanFile(t, root, "workloads/pg-source/chaos.env", strings.Join([]string{
		`WORKLOAD_NAME="chaos"`,
		`WORKLOAD_KIND="pg-source-check"`,
		`PG_SOURCE_ACTION="${PG_SOURCE_ACTION:-run}"`,
		`PG_PATCHSET="${PG_PATCHSET:-chaos/master}"`,
		`PG_CHECK_TARGET="${PG_CHECK_TARGET:-check-world}"`,
		"",
	}, "\n"))

	plan, err := Build(root, Options{
		Action:       "plan",
		WorkloadSpec: "pg-source/chaos",
		Env: map[string]string{
			"PG_CHECK_TARGET":  "check",
			"PG_SOURCE_RUN_ID": "fixed",
		},
		Now:      fixedNow,
		CPUCount: func() int { return 2 },
	})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Action != "plan" {
		t.Fatalf("expected forced plan action, got %s", plan.Action)
	}
	if plan.Patchset != "chaos/master" {
		t.Fatalf("unexpected patchset: %s", plan.Patchset)
	}
	if plan.CheckTarget != "check" {
		t.Fatalf("environment override was not preserved: %s", plan.CheckTarget)
	}
	if strings.Join(plan.PatchFiles, " ") != "001.patch" {
		t.Fatalf("unexpected patch files: %#v", plan.PatchFiles)
	}
}

func TestRenderPlan(t *testing.T) {
	root := t.TempDir()
	plan, err := Build(root, Options{
		Action: "plan",
		Env: map[string]string{
			"PG_SOURCE_RUN_ID": "fixed",
		},
		Now:      fixedNow,
		CPUCount: func() int { return 2 },
	})
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PG_SOURCE_ACTION=plan",
		"PG_REPO_URL=https://github.com/postgres/postgres.git",
		"PG_PATCH_FILES=(none)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered plan missing %q:\n%s", want, out.String())
		}
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

func writeSourcePlanFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
