package pgsourcecheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyCleanArtifacts(t *testing.T) {
	root := t.TempDir()
	writeSourceArtifact(t, root, "generated/pg-source/run1/artifacts/check.log", "All tests passed.\n")

	summary, err := Classify(root, "generated/pg-source/run1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Found {
		t.Fatalf("unexpected failure evidence: %#v", summary)
	}
	if summary.FilesSeen != 1 || len(summary.LogFiles) != 1 {
		t.Fatalf("unexpected artifact counts: %#v", summary)
	}
}

func TestClassifyFailureArtifacts(t *testing.T) {
	root := t.TempDir()
	writeSourceArtifact(t, root, "generated/pg-source/run2/artifacts/check.log", "PANIC:  test crash\n")
	writeSourceArtifact(t, root, "generated/pg-source/run2/artifacts/diffs/regression.diffs", "+ERROR:  broken plan\n")
	writeSourceArtifact(t, root, "generated/pg-source/run2/artifacts/cores/core.123", "core\n")

	summary, err := Classify(root, "generated/pg-source/run2")
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Found {
		t.Fatal("expected failure evidence")
	}
	if len(summary.DiffFiles) != 1 || len(summary.CoreFiles) != 1 {
		t.Fatalf("unexpected artifact counts: %#v", summary)
	}

	var out bytes.Buffer
	if err := Render(&out, summary); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"diff_files=1",
		"core_files=1",
		"crash_and_assertion_patterns=1",
		"regression_diff_error_patterns=1",
		"result=failure-evidence-found",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered summary missing %q:\n%s", want, out.String())
		}
	}
}

func TestClassifyMissingPath(t *testing.T) {
	if _, err := Classify(t.TempDir(), "missing"); err == nil {
		t.Fatal("expected missing path error")
	}
}

func writeSourceArtifact(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
