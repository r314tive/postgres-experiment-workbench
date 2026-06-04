package patchsetcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogListShowValidate(t *testing.T) {
	root := t.TempDir()
	writePatchsetFile(t, root, "patchsets/chaos/master/patchset.env", strings.Join([]string{
		`PATCHSET_NAME="chaos/master"`,
		`PATCHSET_DESCRIPTION="Chaos source checks."`,
		`PATCHSET_PG_REF="master"`,
		`PATCHSET_ALLOW_EMPTY="1"`,
		`PATCHSET_CONFIGURE_ARGS="--enable-debug --enable-cassert"`,
		"",
	}, "\n"))

	catalog := New(root)
	patchsets, err := catalog.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(patchsets) != 1 || patchsets[0] != "chaos/master" {
		t.Fatalf("unexpected patchsets: %#v", patchsets)
	}

	metadata, err := catalog.Show("chaos/master")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "chaos/master" || metadata.PgRef != "master" || metadata.AllowEmpty != "1" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if len(metadata.ResolvedFiles) != 0 {
		t.Fatalf("expected empty resolved file list, got %#v", metadata.ResolvedFiles)
	}

	if errs := catalog.Validate(nil); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %#v", errs)
	}
}

func TestCatalogResolvePatchFiles(t *testing.T) {
	root := t.TempDir()
	writePatchsetFile(t, root, "patchsets/basic/master/patchset.env", strings.Join([]string{
		`PATCHSET_NAME="basic/master"`,
		`PATCHSET_DESCRIPTION="Basic source checks."`,
		`PATCHSET_PG_REF="master"`,
		"",
	}, "\n"))
	writePatchsetFile(t, root, "patchsets/basic/master/002.diff", "diff --git a/a b/a\n")
	writePatchsetFile(t, root, "patchsets/basic/master/001.patch", "diff --git a/b b/b\n")

	metadata, err := New(root).Show("basic/master")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(metadata.ResolvedFiles, " ") != "001.patch 002.diff" {
		t.Fatalf("unexpected resolved files: %#v", metadata.ResolvedFiles)
	}
}

func TestCatalogResolveSeries(t *testing.T) {
	root := t.TempDir()
	writePatchsetFile(t, root, "patchsets/series/master/patchset.env", strings.Join([]string{
		`PATCHSET_NAME="series/master"`,
		`PATCHSET_DESCRIPTION="Series source checks."`,
		`PATCHSET_PG_REF="master"`,
		"",
	}, "\n"))
	writePatchsetFile(t, root, "patchsets/series/master/series", "b.patch # comment\n\n a.patch\n")
	writePatchsetFile(t, root, "patchsets/series/master/a.patch", "diff --git a/a b/a\n")
	writePatchsetFile(t, root, "patchsets/series/master/b.patch", "diff --git a/b b/b\n")

	metadata, err := New(root).Show("series/master")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(metadata.ResolvedFiles, " ") != "b.patch a.patch" {
		t.Fatalf("unexpected resolved files: %#v", metadata.ResolvedFiles)
	}
}

func TestCatalogValidateBrokenPatchset(t *testing.T) {
	root := t.TempDir()
	writePatchsetFile(t, root, "patchsets/broken/master/patchset.env", strings.Join([]string{
		`PATCHSET_NAME="wrong/name"`,
		`PATCHSET_FILES="../escape.patch missing.patch"`,
		"",
	}, "\n"))

	errs := New(root).Validate([]string{"broken/master"})
	if len(errs) < 4 {
		t.Fatalf("expected validation errors, got %#v", errs)
	}
}

func writePatchsetFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
