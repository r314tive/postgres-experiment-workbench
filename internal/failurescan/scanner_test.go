package failurescan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanCleanDirectory(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "postgresql.log"), []byte("ordinary PostgreSQL log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Scan(root, Options{Paths: []string{"logs"}, ContextLines: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Found {
		t.Fatalf("unexpected failure evidence: %#v", result)
	}
	if result.FilesSeen != 1 {
		t.Fatalf("expected one file, got %d", result.FilesSeen)
	}
}

func TestScanDetectsFailureEvidence(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "dirty", "log")
	diffDir := filepath.Join(root, "dirty", "results")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(diffDir, 0o755); err != nil {
		t.Fatal(err)
	}

	log := "before\nserver process (PID 12345) was terminated by signal 11: SIGSEGV\nafter\n"
	if err := os.WriteFile(filepath.Join(logDir, "postgresql.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diffDir, "regression.diffs"), []byte("+ERROR:  could not find pathkey item to sort\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dirty", "core.123"), []byte("core\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Scan(root, Options{Paths: []string{"dirty"}, ContextLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found {
		t.Fatal("expected failure evidence")
	}
	if len(result.CoreFiles) != 1 {
		t.Fatalf("expected one core file, got %#v", result.CoreFiles)
	}

	var out bytes.Buffer
	if err := Render(&out, result); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"result=failure-evidence-found", "SIGSEGV", "+ERROR"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, rendered)
		}
	}
}

func TestScanMissingPathsAreClean(t *testing.T) {
	result, err := Scan(t.TempDir(), Options{Paths: []string{"logs", "generated"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoPaths {
		t.Fatalf("expected no paths result: %#v", result)
	}
	if result.Found {
		t.Fatal("missing paths should be clean")
	}
}
