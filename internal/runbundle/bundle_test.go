package runbundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestCreateBundle(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeFile(t, filepath.Join(runDir, "manifest.env"), "run_id=run-a\n")
	writeFile(t, filepath.Join(runDir, "nested", "artifact.txt"), "artifact\n")

	output := filepath.Join(root, "generated", "run-a.tar.gz")
	result, err := Create(root, "run-a", output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Bytes == 0 || result.Output != output {
		t.Fatalf("unexpected result: %#v", result)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"run_dir"`, `"output"`, `"files"`, `"bytes"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("JSON payload missing %q: %s", want, payload)
		}
	}

	names := readTarNames(t, output)
	want := []string{"run-a/manifest.env", "run-a/nested/artifact.txt"}
	if len(names) != len(want) {
		t.Fatalf("unexpected tar entries: %#v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entry %d: expected %q, got %q", i, want[i], names[i])
		}
	}
}

func TestCreateRejectsOutputInsideRunDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "runs", "run-a", "manifest.env"), "run_id=run-a\n")

	if _, err := Create(root, "run-a", filepath.Join(root, "runs", "run-a", "bundle.tar.gz")); err == nil {
		t.Fatal("expected output-inside-run-dir error")
	}
}

func readTarNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()

	reader := tar.NewReader(gzipReader)
	var names []string
	for {
		header, err := reader.Next()
		if err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
		names = append(names, header.Name)
	}
	sort.Strings(names)
	return names
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
