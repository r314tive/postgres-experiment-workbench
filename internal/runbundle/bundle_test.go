package runbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
)

func TestCreateBundle(t *testing.T) {
	root := t.TempDir()
	runDir := writeValidRunBundleFixture(t, root, "run-a")
	writeFile(t, filepath.Join(runDir, "nested", "artifact.txt"), "artifact\n")

	output := filepath.Join(root, "generated", "run-a.tar.gz")
	result, err := Create(root, "run-a", output)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput, err := pathguard.ResolveOutputOutside(runDir, output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 5 || result.Bytes == 0 || result.Output != wantOutput || !evidence.IsDigest(result.Digest) {
		t.Fatalf("unexpected result: %#v", result)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"run_dir"`, `"output"`, `"files"`, `"bytes"`, `"digest"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("JSON payload missing %q: %s", want, payload)
		}
	}

	names := readTarNames(t, output)
	want := []string{
		"run-a/.pgworkbench-bundle.json",
		"run-a/artifacts/provenance/experiment-spec.env",
		"run-a/manifest.env",
		"run-a/nested/artifact.txt",
		"run-a/verdict.env",
		"run-a/verdict.json",
	}
	if len(names) != len(want) {
		t.Fatalf("unexpected tar entries: %#v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entry %d: expected %q, got %q", i, want[i], names[i])
		}
	}
	inventoryContent := readTarFile(t, output, "run-a/"+evidence.BundleInventoryName)
	inventory, err := evidence.ParseBundleInventory(inventoryContent)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.RunID != "run-a" || len(inventory.Files) != 5 {
		t.Fatalf("unexpected inventory: %#v", inventory)
	}
	for _, file := range inventory.Files {
		if !evidence.IsDigest(file.Digest) {
			t.Fatalf("invalid digest for %s: %q", file.Path, file.Digest)
		}
	}
}

func TestCreateRejectsOutputInsideRunDirDirectlyOrThroughAliasedParent(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeFile(t, filepath.Join(runDir, "manifest.env"), "run_id=run-a\n")
	alias := filepath.Join(t.TempDir(), "run-alias")
	if err := os.Symlink(runDir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	tests := []struct {
		name   string
		output string
		target string
	}{
		{name: "direct child", output: filepath.Join(runDir, "direct.tar.gz"), target: filepath.Join(runDir, "direct.tar.gz")},
		{name: "aliased parent", output: filepath.Join(alias, "aliased.tar.gz"), target: filepath.Join(runDir, "aliased.tar.gz")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Create(root, "run-a", test.output)
			if !errors.Is(err, pathguard.ErrOutputWithinSource) {
				t.Fatalf("expected output-containment error, got %v", err)
			}
			if _, statErr := os.Lstat(test.target); !os.IsNotExist(statErr) {
				t.Fatalf("output was written inside run artifact: %v", statErr)
			}
		})
	}
}

func TestCreateIsDeterministicAcrossFilesystemMetadata(t *testing.T) {
	root := t.TempDir()
	runDir := writeValidRunBundleFixture(t, root, "run-a")
	manifestPath := filepath.Join(runDir, "manifest.env")
	artifactPath := filepath.Join(runDir, "nested", "artifact.txt")
	writeFile(t, artifactPath, "artifact\n")

	firstOutput := filepath.Join(root, "first.tar.gz")
	first, err := Create(root, runDir, firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	changedTime := time.Date(2035, 4, 5, 6, 7, 8, 0, time.FixedZone("offset", 5*60*60))
	if err := os.Chtimes(manifestPath, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(artifactPath, 0o600); err != nil {
		t.Fatal(err)
	}
	secondOutput := filepath.Join(root, "second.tar.gz")
	second, err := Create(root, runDir, secondOutput)
	if err != nil {
		t.Fatal(err)
	}

	firstBytes, err := os.ReadFile(firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("bundle bytes changed with source metadata")
	}
	if first.Digest != second.Digest {
		t.Fatalf("bundle digests differ: %q != %q", first.Digest, second.Digest)
	}

	for _, header := range readTarHeaders(t, firstOutput) {
		if header.ModTime.Unix() != 0 || header.Uid != 0 || header.Gid != 0 || header.Mode != 0o644 {
			t.Fatalf("non-deterministic header for %s: %#v", header.Name, header)
		}
	}
}

func TestCreateRejectsNonRegularArtifacts(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	writeFile(t, filepath.Join(runDir, "manifest.env"), "run_id=run-a\n")
	if err := os.Symlink("manifest.env", filepath.Join(runDir, "manifest-link.env")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Create(root, runDir, filepath.Join(root, "run-a.tar.gz")); err == nil || !strings.Contains(err.Error(), "unsupported non-regular path") {
		t.Fatalf("expected non-regular artifact error, got %v", err)
	}
}

func TestCreatedBundleVerifiesAfterExtractionAndRelocation(t *testing.T) {
	root := t.TempDir()
	runDir := writeValidRunBundleFixture(t, root, "run-a")

	archivePath := filepath.Join(root, "run-a.tar.gz")
	if _, err := Create(root, runDir, archivePath); err != nil {
		t.Fatal(err)
	}
	extractRoot := filepath.Join(root, "extracted")
	extractTar(t, archivePath, extractRoot)
	extractedRunDir := filepath.Join(extractRoot, "run-a")
	relocatedRunDir := filepath.Join(extractRoot, "renamed-artifact")
	if err := os.Rename(extractedRunDir, relocatedRunDir); err != nil {
		t.Fatal(err)
	}
	verification, err := runverify.VerifyBundle(extractRoot, relocatedRunDir)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid() {
		t.Fatalf("relocated extracted bundle is invalid: %#v", verification.Issues)
	}
}

func TestCreateRejectsTamperedStageBeforeArchive(t *testing.T) {
	root := t.TempDir()
	runDir := writeValidRunBundleFixture(t, root, "run-a")
	output := filepath.Join(root, "tampered-stage.tar.gz")
	_, err := create(root, runDir, output, func(stage string) error {
		path := filepath.Join(stage, "run-a", "verdict.json")
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(path, append(content, ' '), 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "staged run bundle is invalid") {
		t.Fatalf("tampered staged run bundle was published: %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive exists after staged verification failure: %v", statErr)
	}
}

func TestCreateNeverReplacesExistingOutput(t *testing.T) {
	root := t.TempDir()
	runDir := writeValidRunBundleFixture(t, root, "run-a")
	output := filepath.Join(root, "existing.tar.gz")
	writeFile(t, output, "sentinel\n")
	_, err := Create(root, runDir, output)
	if !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("expected existing-output rejection, got %v", err)
	}
	content, readErr := os.ReadFile(output)
	if readErr != nil || string(content) != "sentinel\n" {
		t.Fatalf("existing output changed: content=%q err=%v", content, readErr)
	}
}

func writeValidRunBundleFixture(t *testing.T, root, runID string) string {
	t.Helper()
	runDir := filepath.Join(root, "runs", runID)
	spec := "EXPERIMENT_NAME=smoke\n"
	if err := runstate.WriteManifest(runDir, runstate.Manifest{
		RunID:                    runID,
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpecID:         "smoke",
		ExperimentSpecDigest:     evidence.DigestBytes([]byte(spec)),
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		ProfileSize:              "small",
		MetricsEnabled:           "0",
		RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-01-01T00:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(runDir, "artifacts", "provenance", "experiment-spec.env"), spec)
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{
		RunID:            runID,
		Status:           "passed",
		Message:          "experiment passed",
		StartedAt:        "2026-01-01T00:00:00Z",
		FinishedAt:       "2026-01-01T00:00:02Z",
		ExperimentSpecID: "smoke",
	}); err != nil {
		t.Fatal(err)
	}
	return runDir
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

func readTarFile(t *testing.T, archivePath string, target string) []byte {
	t.Helper()
	file, err := os.Open(archivePath)
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
	for {
		header, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				t.Fatalf("tar entry not found: %s", target)
			}
			t.Fatal(err)
		}
		if header.Name != target {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
}

func readTarHeaders(t *testing.T, archivePath string) []*tar.Header {
	t.Helper()
	file, err := os.Open(archivePath)
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
	var headers []*tar.Header
	for {
		header, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		copy := *header
		headers = append(headers, &copy)
	}
	return headers
}

func extractTar(t *testing.T, archivePath string, destination string) {
	t.Helper()
	file, err := os.Open(archivePath)
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
	for {
		header, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				return
			}
			t.Fatal(err)
		}
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		if !isSubpath(destination, target) {
			t.Fatalf("unsafe tar path: %s", header.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
