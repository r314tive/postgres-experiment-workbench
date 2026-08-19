//go:build darwin || linux

package releaseevidence

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

func TestPublishBundleArchiveAtReportsCommittedUnconfirmedFailures(t *testing.T) {
	content := []byte("verified deterministic archive bytes\n")
	newFixture := func(t *testing.T) (*os.File, string, BundleCreateResult) {
		t.Helper()
		root := t.TempDir()
		directory, err := openDirectoryPath(root)
		if err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(root, "bundle.tar.gz")
		return directory, output, BundleCreateResult{Output: output, Digest: digestExactBytes(content), ArchiveBytes: int64(len(content))}
	}

	t.Run("staging cleanup after link", func(t *testing.T) {
		directory, output, result := newFixture(t)
		defer directory.Close()
		operations := systemWriteNewAtOperations()
		operations.unlink = func(int, string) error { return syscall.EIO }
		published, err := publishBundleArchiveAtWithOperations(directory, filepath.Base(output), output, content, result, nil, operations)
		assertBundleCommittedError(t, published, err, output, content)
	})

	t.Run("directory sync after link", func(t *testing.T) {
		directory, output, result := newFixture(t)
		defer directory.Close()
		operations := systemWriteNewAtOperations()
		operations.syncDirectory = func(*os.File) error { return syscall.EIO }
		published, err := publishBundleArchiveAtWithOperations(directory, filepath.Base(output), output, content, result, nil, operations)
		assertBundleCommittedError(t, published, err, output, content)
	})

	t.Run("published destination confirmation", func(t *testing.T) {
		directory, output, result := newFixture(t)
		defer directory.Close()
		operations := systemWriteNewAtOperations()
		openCalls := 0
		systemOpen := operations.open
		operations.open = func(fd int, name string, flags int, mode uint32) (int, error) {
			openCalls++
			if openCalls > 1 {
				return -1, syscall.EIO
			}
			return systemOpen(fd, name, flags, mode)
		}
		published, err := publishBundleArchiveAtWithOperations(directory, filepath.Base(output), output, content, result, nil, operations)
		assertBundleCommittedError(t, published, err, output, content)
	})
}

func TestPublishBundleArchiveAtNeverReplacesConcurrentDestination(t *testing.T) {
	root := t.TempDir()
	directory, err := openDirectoryPath(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	output := filepath.Join(root, "bundle.tar.gz")
	content := []byte("verified deterministic archive bytes\n")
	result := BundleCreateResult{Output: output, Digest: digestExactBytes(content), ArchiveBytes: int64(len(content))}
	operations := systemWriteNewAtOperations()
	operations.link = func(int, string, string) error {
		if err := os.WriteFile(output, []byte("concurrent sentinel\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return syscall.EEXIST
	}
	published, err := publishBundleArchiveAtWithOperations(directory, filepath.Base(output), output, content, result, nil, operations)
	if !errors.Is(err, pathguard.ErrOutputExists) || published.Digest != "" {
		t.Fatalf("concurrent destination result=%+v err=%v", published, err)
	}
	actual, readErr := os.ReadFile(output)
	if readErr != nil || string(actual) != "concurrent sentinel\n" {
		t.Fatalf("concurrent destination changed: %q %v", actual, readErr)
	}
}

func TestPublishBundleArchiveAtDetectsDifferentPublishedInode(t *testing.T) {
	root := t.TempDir()
	directory, err := openDirectoryPath(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	output := filepath.Join(root, "bundle.tar.gz")
	content := []byte("verified deterministic archive bytes\n")
	result := BundleCreateResult{Output: output, Digest: digestExactBytes(content), ArchiveBytes: int64(len(content))}
	operations := systemWriteNewAtOperations()
	operations.link = func(int, string, string) error {
		return os.WriteFile(output, []byte("different inode\n"), 0o644)
	}
	published, err := publishBundleArchiveAtWithOperations(directory, filepath.Base(output), output, content, result, nil, operations)
	var committed *BundleCommittedError
	if !errors.As(err, &committed) || published.Digest != result.Digest || !strings.Contains(err.Error(), "does not identify the verified staged inode") {
		t.Fatalf("different published inode result=%+v err=%v", published, err)
	}
}

func TestPublishBundleArchiveAtDetectsReplacementDuringStagingCleanup(t *testing.T) {
	root := t.TempDir()
	directory, err := openDirectoryPath(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	output := filepath.Join(root, "bundle.tar.gz")
	content := []byte("verified deterministic archive bytes\n")
	result := BundleCreateResult{Output: output, Digest: digestExactBytes(content), ArchiveBytes: int64(len(content))}
	operations := systemWriteNewAtOperations()
	systemUnlink := operations.unlink
	operations.unlink = func(fd int, name string) error {
		if err := systemUnlink(fd, name); err != nil {
			return err
		}
		if err := os.Remove(output); err != nil {
			return err
		}
		return os.WriteFile(output, []byte("replacement during cleanup\n"), 0o644)
	}
	published, err := publishBundleArchiveAtWithOperations(directory, filepath.Base(output), output, content, result, nil, operations)
	var committed *BundleCommittedError
	if !errors.As(err, &committed) || published.Digest != result.Digest || !strings.Contains(err.Error(), "does not identify the verified staged inode") {
		t.Fatalf("cleanup replacement result=%+v err=%v", published, err)
	}
}

func TestPublishBundleArchiveAtRejectsSpecialModeBitsAfterCleanup(t *testing.T) {
	root := t.TempDir()
	requireBundleSpecialModeBits(t, root)
	directory, err := openDirectoryPath(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	output := filepath.Join(root, "bundle.tar.gz")
	content := []byte("verified deterministic archive bytes\n")
	result := BundleCreateResult{Output: output, Digest: digestExactBytes(content), ArchiveBytes: int64(len(content))}
	operations := systemWriteNewAtOperations()
	systemUnlink := operations.unlink
	operations.unlink = func(fd int, name string) error {
		if err := systemUnlink(fd, name); err != nil {
			return err
		}
		return os.Chmod(output, 0o644|os.ModeSetuid)
	}
	published, err := publishBundleArchiveAtWithOperations(directory, filepath.Base(output), output, content, result, nil, operations)
	var committed *BundleCommittedError
	if !errors.As(err, &committed) || published.Digest != result.Digest || !strings.Contains(err.Error(), "does not identify the verified staged inode") {
		t.Fatalf("special publication mode result=%+v err=%v", published, err)
	}
}

func assertBundleCommittedError(t *testing.T, result BundleCreateResult, err error, output string, content []byte) {
	t.Helper()
	var committed *BundleCommittedError
	if !errors.As(err, &committed) || committed.Result.Digest != digestExactBytes(content) || result.Digest != digestExactBytes(content) {
		t.Fatalf("publication result=%+v err=%v, want committed-unconfirmed", result, err)
	}
	actual, readErr := os.ReadFile(output)
	if readErr != nil || !bytes.Equal(actual, content) {
		t.Fatalf("committed destination bytes=%q err=%v", actual, readErr)
	}
}
