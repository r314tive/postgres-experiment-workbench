package profilecatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogListShowValidate(t *testing.T) {
	root := t.TempDir()
	profileDir := filepath.Join(root, "profiles", "smoke")
	if err := os.MkdirAll(filepath.Join(profileDir, "sql"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"README.md":        "# smoke\n",
		"sql/00_setup.sql": "\\set ON_ERROR_STOP on\n",
		"sql/10_run.sql":   "\\set ON_ERROR_STOP on\n",
		"profile.env":      "PROFILE_NAME=\"smoke\"\nPROFILE_DESCRIPTION=\"Smoke profile\"\nPROFILE_TAGS=\"platform smoke\"\nPROFILE_DEFAULT_SIZE=\"small\"\nPROFILE_SIZES=\"small medium large\"\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(profileDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	catalog := New(root)
	profiles, err := catalog.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0] != "smoke" {
		t.Fatalf("unexpected profiles: %#v", profiles)
	}

	metadata, err := catalog.Show("smoke")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "smoke" || metadata.Description != "Smoke profile" || metadata.RequiresTopology != "single" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}

	if errs := catalog.Validate(nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
}

func TestCatalogValidateMetadataMismatch(t *testing.T) {
	root := t.TempDir()
	profileDir := filepath.Join(root, "profiles", "locks")
	if err := os.MkdirAll(filepath.Join(profileDir, "sql"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"README.md":        "# locks\n",
		"sql/00_setup.sql": "\\set ON_ERROR_STOP on\n",
		"sql/10_run.sql":   "\\set ON_ERROR_STOP on\n",
		"profile.env":      "PROFILE_NAME=\"wrong\"\nPROFILE_DESCRIPTION=\"Locks\"\nPROFILE_DEFAULT_SIZE=\"large\"\nPROFILE_SIZES=\"small medium\"\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(profileDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	errs := New(root).Validate([]string{"locks"})
	if len(errs) != 2 {
		t.Fatalf("expected two validation errors, got %#v", errs)
	}
}
