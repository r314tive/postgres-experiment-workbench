package experimentrun

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestMergeEnvironmentMakesRunnerOwnedNativeBindirUnambiguous(t *testing.T) {
	merged := mergeEnvironment(
		[]string{"PGWORKBENCH_NATIVE_BINDIR=/hostile/a", "KEEP=value", "PGWORKBENCH_NATIVE_BINDIR=/hostile/b"},
		[]string{"PGWORKBENCH_NATIVE_BINDIR=/bound/toolchain", "PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST=sha256:" + strings.Repeat("a", 64)},
	)
	seen := 0
	for _, entry := range merged {
		if strings.HasPrefix(entry, "PGWORKBENCH_NATIVE_BINDIR=") {
			seen++
			if entry != "PGWORKBENCH_NATIVE_BINDIR=/bound/toolchain" {
				t.Fatalf("hostile bindir survived canonical merge: %q", entry)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("native bindir appears %d times, want exactly one: %#v", seen, merged)
	}
}

func TestExactEnvironmentBaseRejectsAmbientProtocolControls(t *testing.T) {
	base := exactEnvironmentBase([]string{
		"PATH=/usr/bin:/bin",
		"HOME=/tmp/home",
		"TMPDIR=/tmp/work",
		"EXPERIMENT_ASSERT_SHELL=exit 0",
		"WORKLOAD_CMD=hostile",
		"POSTGRES_PORT=59999",
		"COMPOSE=hostile compose",
		"PGWORKBENCH_BIN=/tmp/hostile",
		"BASH_ENV=/tmp/hostile-profile",
	})
	for _, want := range []string{"PATH=/usr/bin:/bin", "HOME=/tmp/home", "TMPDIR=/tmp/work", "BASH_ENV=/dev/null", "LANG=C", "LC_ALL=C", "TZ=UTC"} {
		if !contains(base, want) {
			t.Fatalf("exact base is missing %q: %#v", want, base)
		}
	}
	for _, forbidden := range []string{"EXPERIMENT_", "WORKLOAD_", "POSTGRES_", "COMPOSE=", "PGWORKBENCH_", "BASH_ENV=/tmp"} {
		for _, entry := range base {
			if strings.HasPrefix(entry, forbidden) {
				t.Fatalf("ambient control survived exact base: %q", entry)
			}
		}
	}
}

func TestRunExactEnvironmentPassesOnlyBaseAndRunnerOwnedValues(t *testing.T) {
	root := t.TempDir()
	writeExperiment(t, root, "experiments/smoke.env", "EXPERIMENT_NAME=smoke\n")
	old := os.Getenv("POSTGRES_PORT")
	t.Cleanup(func() { _ = os.Setenv("POSTGRES_PORT", old) })
	if err := os.Setenv("POSTGRES_PORT", "59999"); err != nil {
		t.Fatal(err)
	}
	var seen []string
	_, err := Run(root, speccatalog.New(root), "smoke", Options{
		Runtime:          "docker",
		RunID:            "exact-run",
		ExactEnvironment: true,
		Env:              []string{"ENV_FILE=.env.example"},
		RunCommand: func(_ string, _ []string, env []string, _, _ io.Writer) CommandResult {
			seen = append([]string(nil), env...)
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if contains(seen, "POSTGRES_PORT=59999") || !contains(seen, "ENV_FILE=.env.example") || !contains(seen, "BASH_ENV=/dev/null") {
		t.Fatalf("unexpected exact child environment: %#v", seen)
	}
}
