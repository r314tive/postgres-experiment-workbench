//go:build darwin || linux

package releaseevidence

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

func TestWriteNewAtPublishesVerifiedNoClobberRevision(t *testing.T) {
	directoryPath := t.TempDir()
	directory, err := os.Open(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	index := validWriteAtIndex()
	outputName := "index-r0.json"
	output := filepath.Join(directoryPath, outputName)

	result, err := WriteNewAt(directory, outputName, output, index)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != output || !strings.HasPrefix(result.Digest, "sha256:") || !result.Verification.Valid {
		t.Fatalf("WriteNewAt() result = %+v", result)
	}
	if info, err := os.Lstat(output); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		t.Fatalf("published output info=%v err=%v", info, err)
	}
	if verification, err := VerifyFile(output); err != nil || !verification.Valid {
		t.Fatalf("VerifyFile(output) = %+v, %v", verification, err)
	}
	assertNoWriteAtStagingNames(t, directoryPath)

	if _, err := WriteNewAt(directory, outputName, output, index); !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("second WriteNewAt() error = %v, want ErrOutputExists", err)
	}
	assertNoWriteAtStagingNames(t, directoryPath)
}

func TestWriteNewAtStaysWithPinnedDirectoryAfterPathReplacement(t *testing.T) {
	container := t.TempDir()
	original := filepath.Join(container, "evidence")
	moved := filepath.Join(container, "evidence-moved")
	if err := os.Mkdir(original, 0o755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0o755); err != nil {
		t.Fatal(err)
	}

	outputName := "index-r0.json"
	result, err := WriteNewAt(directory, outputName, filepath.Join(original, outputName), validWriteAtIndex())
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != filepath.Join(original, outputName) {
		t.Fatalf("reporting output = %q", result.Output)
	}
	if verification, err := VerifyFile(filepath.Join(moved, outputName)); err != nil || !verification.Valid {
		t.Fatalf("pinned-directory output = %+v, %v", verification, err)
	}
	if _, err := os.Lstat(filepath.Join(original, outputName)); !os.IsNotExist(err) {
		t.Fatalf("replacement directory received output: %v", err)
	}
	assertNoWriteAtStagingNames(t, moved)
	assertNoWriteAtStagingNames(t, original)
}

func TestWriteNewAtReturnsCommittedErrorAfterDirectorySyncFailure(t *testing.T) {
	directoryPath := t.TempDir()
	directory, err := os.Open(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	operations := systemWriteNewAtOperations()
	syncFailure := errors.New("injected pinned directory sync failure")
	operations.syncDirectory = func(*os.File) error { return syncFailure }
	outputName := "index-r0.json"
	output := filepath.Join(directoryPath, outputName)

	result, err := writeNewAt(directory, outputName, output, validWriteAtIndex(), operations)
	var committed *CommittedError
	if !errors.As(err, &committed) || !errors.Is(err, syncFailure) {
		t.Fatalf("writeNewAt() error = %v, want committed sync failure", err)
	}
	if result.Output != output || committed.Result.Digest != result.Digest {
		t.Fatalf("committed result=%+v error=%+v", result, committed)
	}
	if verification, verifyErr := VerifyFile(output); verifyErr != nil || !verification.Valid {
		t.Fatalf("committed output = %+v, %v", verification, verifyErr)
	}
	assertNoWriteAtStagingNames(t, directoryPath)
}

func TestWriteNewAtReturnsCommittedErrorWhenStagingCleanupFails(t *testing.T) {
	directoryPath := t.TempDir()
	directory, err := os.Open(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	operations := systemWriteNewAtOperations()
	cleanupFailure := errors.New("injected fd-relative staging cleanup failure")
	operations.unlink = func(int, string) error { return cleanupFailure }
	outputName := "index-r0.json"
	output := filepath.Join(directoryPath, outputName)

	result, err := writeNewAt(directory, outputName, output, validWriteAtIndex(), operations)
	var committed *CommittedError
	if !errors.As(err, &committed) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("writeNewAt() error = %v, want committed cleanup failure", err)
	}
	if result.Output != output || committed.Result.Digest != result.Digest {
		t.Fatalf("committed cleanup result=%+v error=%+v", result, committed)
	}
	if verification, verifyErr := VerifyFile(output); verifyErr != nil || !verification.Valid {
		t.Fatalf("committed output = %+v, %v", verification, verifyErr)
	}
	entries, readErr := os.ReadDir(directoryPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	stagingNames := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pgworkbench-release-evidence-") {
			stagingNames++
		}
	}
	if stagingNames != 1 {
		t.Fatalf("staging names after injected cleanup failure = %d, want 1", stagingNames)
	}
}

func TestWriteNewAtDetectsDifferentPublishedInode(t *testing.T) {
	directoryPath := t.TempDir()
	directory, err := os.Open(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	operations := systemWriteNewAtOperations()
	operations.link = func(directoryFD int, stagingName, outputName string) error {
		if err := unlinkAt(directoryFD, stagingName); err != nil {
			return err
		}
		fd, err := openAt(directoryFD, stagingName, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
		if err != nil {
			return err
		}
		replacement := os.NewFile(uintptr(fd), stagingName)
		if _, err := replacement.Write([]byte("{}\n")); err != nil {
			_ = replacement.Close()
			return err
		}
		if err := replacement.Close(); err != nil {
			return err
		}
		return linkAt(directoryFD, stagingName, outputName)
	}
	outputName := "index-r0.json"
	output := filepath.Join(directoryPath, outputName)

	result, err := writeNewAt(directory, outputName, output, validWriteAtIndex(), operations)
	var committed *CommittedError
	if !errors.As(err, &committed) || !strings.Contains(err.Error(), "verified staged inode") {
		t.Fatalf("writeNewAt() error = %v, want committed inode mismatch", err)
	}
	if result.Output != output || committed.Result.Digest != result.Digest {
		t.Fatalf("committed mismatch result=%+v error=%+v", result, committed)
	}
	assertNoWriteAtStagingNames(t, directoryPath)
}

func TestWriteNewAtRejectsNonBasenameWithoutFilesystemAccess(t *testing.T) {
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	for _, name := range []string{"", ".", "..", "child/index.json", "/index.json"} {
		if _, err := WriteNewAt(directory, name, "reported.json", validWriteAtIndex()); err == nil || !strings.Contains(err.Error(), "basename") {
			t.Fatalf("WriteNewAt(%q) error = %v", name, err)
		}
	}
}

func validWriteAtIndex() Index {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	return index
}

func assertNoWriteAtStagingNames(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pgworkbench-release-evidence-") {
			t.Fatalf("staging name leaked: %s", filepath.Join(directory, entry.Name()))
		}
	}
}
