package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.env")
	content := "# comment\nPLAIN=value\nDOUBLE=\"two words\"\nSINGLE='three words here'\nSPACED = trimmed\nSHELL_SQL=\"DO \\$\\$ BEGIN RAISE NOTICE 'ok'; END \\$\\$;\"\nMULTI_LINE=\"line1\\nline2\\ttab\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	values, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"PLAIN":      "value",
		"DOUBLE":     "two words",
		"SINGLE":     "three words here",
		"SPACED":     "trimmed",
		"SHELL_SQL":  "DO $$ BEGIN RAISE NOTICE 'ok'; END $$;",
		"MULTI_LINE": "line1\nline2\ttab",
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Fatalf("%s: expected %q, got %q", key, expected, values[key])
		}
	}
}

func TestParseEnvFileRejectsInvalidLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.env")
	if err := os.WriteFile(path, []byte("BROKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Parse(path); err == nil {
		t.Fatal("expected parse error")
	}
}
