package diagnosticcatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListShowDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeDiagnostic(t, root, "sql/diagnostics/locks.sql", "SELECT 'locks';\n")
	writeDiagnostic(t, root, "sql/diagnostics/activity.sql", "SELECT 'activity';\n")
	writeDiagnostic(t, root, "sql/diagnostics/readme.txt", "ignored\n")

	catalog := New(root)
	diagnostics, err := catalog.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 2 || diagnostics[0] != "activity" || diagnostics[1] != "locks" {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}

	content, err := catalog.Show("locks")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "SELECT 'locks';\n" {
		t.Fatalf("unexpected content: %q", content)
	}

	content, err = catalog.Show("sql/diagnostics/activity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "SELECT 'activity';\n" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func writeDiagnostic(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
