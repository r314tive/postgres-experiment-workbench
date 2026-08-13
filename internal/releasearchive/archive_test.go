package releasearchive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

func TestCreateIsReproducibleAndNormalized(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "README.md"), "hello\n", 0o600)
	writeFixture(t, filepath.Join(root, "scripts", "run.sh"), "#!/bin/sh\n", 0o755)
	epoch := time.Unix(1_700_000_000, 0).UTC()
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")

	firstResult, err := Create(root, first, "pgworkbench-1.0.0-linux-amd64", epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "README.md"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	secondResult, err := Create(root, second, "pgworkbench-1.0.0-linux-amd64", epoch)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.SHA256 != secondResult.SHA256 {
		t.Fatalf("archive digest changed: %s != %s", firstResult.SHA256, secondResult.SHA256)
	}
	if info, err := os.Stat(first); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("archive mode is not canonical: info=%v err=%v", info, err)
	}
	firstBytes, _ := os.ReadFile(first)
	secondBytes, _ := os.ReadFile(second)
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("archive bytes are not reproducible")
	}

	file, err := os.Open(first)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	seenExecutable := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !header.ModTime.Equal(epoch) || header.Uid != 0 || header.Gid != 0 {
			t.Fatalf("non-normalized header: %#v", header)
		}
		if header.Name == "pgworkbench-1.0.0-linux-amd64/scripts/run.sh" {
			seenExecutable = header.Mode == 0o755
		}
	}
	if !seenExecutable {
		t.Fatal("executable mode was not preserved canonically")
	}
}

func TestCreateRejectsSymlinkAndUnsafeRoot(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "file"), "fixture", 0o644)
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, filepath.Join(t.TempDir(), "out.tar.gz"), "release", time.Unix(1, 0)); err == nil {
		t.Fatal("expected symlink rejection")
	}
	if _, err := Create(t.TempDir(), filepath.Join(t.TempDir(), "out.tar.gz"), "../release", time.Unix(1, 0)); err == nil {
		t.Fatal("expected unsafe root rejection")
	}
}

func TestCreateRejectsOutputWithinSourceThroughDirectOrAliasedParent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "file"), "fixture\n", 0o644)
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	tests := []struct {
		name   string
		output string
		target string
	}{
		{name: "direct child", output: filepath.Join(root, "direct.tar.gz"), target: filepath.Join(root, "direct.tar.gz")},
		{name: "aliased parent", output: filepath.Join(alias, "aliased.tar.gz"), target: filepath.Join(root, "aliased.tar.gz")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Create(root, test.output, "release", time.Unix(1, 0))
			if !errors.Is(err, pathguard.ErrOutputWithinSource) {
				t.Fatalf("expected output-containment error, got %v", err)
			}
			if _, statErr := os.Lstat(test.target); !os.IsNotExist(statErr) {
				t.Fatalf("output was written inside source: %v", statErr)
			}
		})
	}
}

func TestCreateRejectsTamperedArchiveBeforePublication(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "README.md"), "hello\n", 0o644)
	output := filepath.Join(t.TempDir(), "release.tar.gz")
	_, err := create(root, output, "release", time.Unix(1, 0).UTC(), func(temporaryPath string) error {
		return os.WriteFile(temporaryPath, []byte("not a gzip archive\n"), 0o600)
	})
	if err == nil || !strings.Contains(err.Error(), "verify staged release archive") {
		t.Fatalf("tampered release archive was published: %v", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("release output exists after staged verification failure: %v", statErr)
	}
}

func TestCreateNeverReplacesExistingOutput(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "README.md"), "hello\n", 0o644)
	output := filepath.Join(t.TempDir(), "release.tar.gz")
	writeFixture(t, output, "sentinel\n", 0o644)
	_, err := Create(root, output, "release", time.Unix(1, 0).UTC())
	if !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("expected existing-output rejection, got %v", err)
	}
	content, readErr := os.ReadFile(output)
	if readErr != nil || string(content) != "sentinel\n" {
		t.Fatalf("existing output changed: content=%q err=%v", content, readErr)
	}
}

func writeFixture(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
