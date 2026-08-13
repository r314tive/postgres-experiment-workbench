package benchmarkimport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportBundleIsReproducibleAndVerifiesAfterRelocation(t *testing.T) {
	workspace := t.TempDir()
	importDir := filepath.Join(workspace, "source-import")
	artifact, err := Create(AdapterBenchBase, filepath.Join("testdata", "benchbase-histogram.json"), importDir, Options{
		MappingPath: filepath.Join("testdata", "benchbase-mapping.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Unix(1_700_000_000, 0).UTC()
	first, err := CreateBundle(importDir, filepath.Join(workspace, "first.tar.gz"), epoch)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateBundle(filepath.Join(importDir, ResultFile), filepath.Join(workspace, "second.tar.gz"), epoch)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.RootName != second.RootName || first.ArtifactDigest != artifact.Digest {
		t.Fatalf("bundle identity is not reproducible: first=%#v second=%#v", first, second)
	}
	if !bytes.Equal(mustReadPath(t, first.Output), mustReadPath(t, second.Output)) {
		t.Fatal("same import and epoch produced different archive bytes")
	}

	for _, location := range []string{"relocated-a", "nested/relocated-b"} {
		extractedRoot := extractBundle(t, first.Output, filepath.Join(workspace, location))
		bundledImport := bundledImportPath(t, extractedRoot)
		verification, err := VerifyBundle(bundledImport)
		if err != nil {
			t.Fatal(err)
		}
		if !verification.IsValid() || verification.Artifact == nil || verification.Artifact.Conclusion != ConclusionDescriptive || verification.Artifact.DecisionEligible {
			t.Fatalf("relocated descriptive import bundle rejected or widened: %#v", verification)
		}
	}
}

func TestCreateImportBundleRejectsTamperedStageBeforeArchive(t *testing.T) {
	workspace := t.TempDir()
	importDir := filepath.Join(workspace, "source-import")
	if _, err := Create(AdapterBenchBase, filepath.Join("testdata", "benchbase-histogram.json"), importDir, Options{
		MappingPath: filepath.Join("testdata", "benchbase-mapping.json"),
	}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(workspace, "tampered-stage.tar.gz")
	_, err := createBundle(importDir, output, time.Unix(0, 0).UTC(), func(stage string) error {
		entries, readErr := os.ReadDir(filepath.Join(stage, "imports"))
		if readErr != nil {
			return readErr
		}
		if len(entries) != 1 {
			return fmt.Errorf("unexpected staged imports: %d", len(entries))
		}
		path := filepath.Join(stage, "imports", entries[0].Name(), ResultFile)
		return os.WriteFile(path, append(mustReadPath(t, path), ' '), 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "staged benchmark import bundle is invalid") {
		t.Fatalf("tampered staged import bundle was published: %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive exists after staged verification failure: %v", statErr)
	}
}

func TestImportBundleTamperMatrixFailsClosed(t *testing.T) {
	workspace := t.TempDir()
	importDir := filepath.Join(workspace, "source-import")
	if _, err := Create(AdapterBenchBase, filepath.Join("testdata", "benchbase-histogram.json"), importDir, Options{
		MappingPath: filepath.Join("testdata", "benchbase-mapping.json"),
	}); err != nil {
		t.Fatal(err)
	}
	bundle, err := CreateBundle(importDir, filepath.Join(workspace, "bundle.tar.gz"), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(t *testing.T, root, imported string)
		want   string
	}{
		{
			name: "raw source",
			mutate: func(t *testing.T, _, imported string) {
				appendFile(t, filepath.Join(imported, filepath.FromSlash(RawSourceFile)), []byte("\n"))
			},
			want: "raw_input does not match",
		},
		{
			name: "mapping",
			mutate: func(t *testing.T, _, imported string) {
				appendFile(t, filepath.Join(imported, filepath.FromSlash(MappingFile)), []byte("\n"))
			},
			want: "mapping_input does not match",
		},
		{
			name: "result",
			mutate: func(t *testing.T, _, imported string) {
				path := filepath.Join(imported, ResultFile)
				content := strings.Replace(string(mustReadPath(t, path)), `"conclusion": "descriptive"`, `"conclusion": "causal"`, 1)
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "does not match independently re-derived",
		},
		{
			name: "inventory digest",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readBundleInventory(t, root)
				inventory.Files[0].Digest = "sha256:" + strings.Repeat("0", 64)
				writeJSONForTest(t, filepath.Join(root, BundleInventoryName), inventory)
			},
			want: "file digest or size mismatch",
		},
		{
			name: "extra file",
			mutate: func(t *testing.T, root, _ string) {
				if err := os.WriteFile(filepath.Join(root, "extra.txt"), []byte("unrecorded\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "inventory is missing file",
		},
		{
			name: "extra empty directory",
			mutate: func(t *testing.T, root, _ string) {
				if err := os.Mkdir(filepath.Join(root, "unrecorded-empty"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "unexpected directory",
		},
		{
			name: "missing retained file",
			mutate: func(t *testing.T, _, imported string) {
				if err := os.Remove(filepath.Join(imported, filepath.FromSlash(RawSourceFile))); err != nil {
					t.Fatal(err)
				}
			},
			want: "required artifact entry source is missing",
		},
		{
			name: "missing inventory",
			mutate: func(t *testing.T, root, _ string) {
				if err := os.Remove(filepath.Join(root, BundleInventoryName)); err != nil {
					t.Fatal(err)
				}
			},
			want: "benchmark import bundle inventory",
		},
		{
			name: "duplicate inventory path",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readBundleInventory(t, root)
				inventory.Files = append(inventory.Files, inventory.Files[0])
				writeJSONForTest(t, filepath.Join(root, BundleInventoryName), inventory)
			},
			want: "duplicate path",
		},
		{
			name: "traversal inventory path",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readBundleInventory(t, root)
				inventory.Files[0].Path = "../outside"
				writeJSONForTest(t, filepath.Join(root, BundleInventoryName), inventory)
			},
			want: "invalid entry",
		},
		{
			name: "unsorted inventory",
			mutate: func(t *testing.T, root, _ string) {
				inventory := readBundleInventory(t, root)
				inventory.Files[0], inventory.Files[len(inventory.Files)-1] = inventory.Files[len(inventory.Files)-1], inventory.Files[0]
				writeJSONForTest(t, filepath.Join(root, BundleInventoryName), inventory)
			},
			want: "inventory is not sorted",
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, _, imported string) {
				link := filepath.Join(imported, "unrecorded-link")
				if err := os.Symlink(ResultFile, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
			want: "unsafe file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := extractBundle(t, bundle.Output, filepath.Join(workspace, "tamper-"+strings.ReplaceAll(test.name, " ", "-")))
			imported := bundledImportPath(t, root)
			test.mutate(t, root, imported)
			verification, err := VerifyBundle(imported)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !issuesContain(verification.Issues, test.want) {
				t.Fatalf("tampering passed or missed %q: %v", test.want, verification.Issues)
			}
		})
	}
}

func TestCreateImportBundleRejectsUnsafeSourceTree(t *testing.T) {
	importDir := filepath.Join(t.TempDir(), "imported")
	if _, err := Create(AdapterSysbench1, filepath.Join("testdata", "sysbench-1.0-oltp.txt"), importDir, Options{Workload: "oltp"}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(importDir, "unrecorded-link")
	if err := os.Symlink(ResultFile, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CreateBundle(importDir, filepath.Join(t.TempDir(), "bundle.tar.gz"), time.Unix(0, 0).UTC()); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unsafe import tree entered bundle: %v", err)
	}
}

func TestCreateImportBundleCannotOverwriteImmutableImport(t *testing.T) {
	importDir := filepath.Join(t.TempDir(), "imported")
	if _, err := Create(AdapterSysbench1, filepath.Join("testdata", "sysbench-1.0-oltp.txt"), importDir, Options{Workload: "oltp"}); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(importDir, ResultFile)
	if _, err := CreateBundle(importDir, resultPath, time.Unix(0, 0).UTC()); err == nil || !strings.Contains(err.Error(), "outside the immutable import") {
		t.Fatalf("bundle could overwrite immutable import evidence: %v", err)
	}
	verification, err := Verify(importDir)
	if err != nil || !verification.IsValid() {
		t.Fatalf("rejected output damaged immutable import: verification=%#v err=%v", verification, err)
	}
}

func TestCreateImportBundleCannotOverwriteImmutableImportThroughSymlinkAncestor(t *testing.T) {
	workspace := t.TempDir()
	importDir := filepath.Join(workspace, "imported")
	if _, err := Create(AdapterSysbench1, filepath.Join("testdata", "sysbench-1.0-oltp.txt"), importDir, Options{Workload: "oltp"}); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(importDir, ResultFile)
	wantResult := mustReadPath(t, resultPath)
	reject := func(output string) {
		t.Helper()
		if _, err := CreateBundle(importDir, output, time.Unix(0, 0).UTC()); err == nil || !strings.Contains(err.Error(), "outside the immutable import") {
			t.Fatalf("bundle output %s could overwrite immutable import evidence: %v", output, err)
		}
	}

	reject(resultPath)
	alias := filepath.Join(workspace, "import-alias")
	if err := os.Symlink(importDir, alias); err != nil {
		t.Fatalf("create import source alias: %v", err)
	}
	reject(filepath.Join(alias, ResultFile))
	reject(filepath.Join(alias, "new.tar.gz"))

	siblingOutput := filepath.Join(workspace, "bundle.tar.gz")
	if _, err := CreateBundle(importDir, siblingOutput, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("normal sibling import bundle output was rejected: %v", err)
	}
	if info, err := os.Stat(siblingOutput); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("normal sibling import bundle was not created: %v", err)
	}

	if got := mustReadPath(t, resultPath); !bytes.Equal(got, wantResult) {
		t.Fatal("rejected bundle output changed immutable import result.json")
	}
	verification, err := Verify(importDir)
	if err != nil || !verification.IsValid() {
		t.Fatalf("rejected symlink-ancestor outputs damaged immutable import: verification=%#v err=%v", verification, err)
	}
}

func extractBundle(t *testing.T, archivePath, destination string) string {
	t.Helper()
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	rootName := ""
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		clean := filepath.ToSlash(filepath.Clean(header.Name))
		if clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(header.Name) {
			t.Fatalf("unsafe archive member %q", header.Name)
		}
		first := strings.Split(clean, "/")[0]
		if rootName == "" {
			rootName = first
		} else if rootName != first {
			t.Fatalf("archive has multiple roots: %q and %q", rootName, first)
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(tarReader)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, content, 0o644); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected archive member type %d", header.Typeflag)
		}
	}
	if rootName == "" {
		t.Fatal("archive is empty")
	}
	return filepath.Join(destination, rootName)
}

func bundledImportPath(t *testing.T, root string) string {
	t.Helper()
	inventory := readBundleInventory(t, root)
	return filepath.Join(root, filepath.FromSlash(inventory.ImportRef))
}

func readBundleInventory(t *testing.T, root string) BundleInventory {
	t.Helper()
	inventory, err := parseBundleInventory(mustReadPath(t, filepath.Join(root, BundleInventoryName)))
	if err != nil {
		t.Fatal(err)
	}
	return inventory
}

func writeJSONForTest(t *testing.T, path string, value any) {
	t.Helper()
	if err := writeJSON(path, value); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path string, suffix []byte) {
	t.Helper()
	content := append(mustReadPath(t, path), suffix...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
