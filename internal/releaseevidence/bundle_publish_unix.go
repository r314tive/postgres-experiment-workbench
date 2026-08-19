//go:build darwin || linux

package releaseevidence

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

func publishBundleArchiveAt(directory *os.File, outputName, displayOutput string, content []byte, result BundleCreateResult, afterPublish func() error) (BundleCreateResult, error) {
	return publishBundleArchiveAtWithOperations(directory, outputName, displayOutput, content, result, afterPublish, systemWriteNewAtOperations())
}

func publishBundleArchiveAtWithOperations(
	directory *os.File,
	outputName, displayOutput string,
	content []byte,
	result BundleCreateResult,
	afterPublish func() error,
	operations writeNewAtOperations,
) (BundleCreateResult, error) {
	if directory == nil {
		return BundleCreateResult{}, fmt.Errorf("evidence bundle output directory is required")
	}
	if err := validateDirectoryEntryName(outputName); err != nil {
		return BundleCreateResult{}, err
	}
	directoryInfo, err := directory.Stat()
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("inspect pinned evidence bundle output directory: %w", err)
	}
	if !directoryInfo.IsDir() {
		return BundleCreateResult{}, fmt.Errorf("pinned evidence bundle output is not a directory")
	}
	if len(content) == 0 || int64(len(content)) > maxBundleArchiveBytes || digestExactBytes(content) != result.Digest {
		return BundleCreateResult{}, fmt.Errorf("evidence bundle archive bytes do not match the verified publication identity")
	}
	result.Output = displayOutput
	result.ArchiveBytes = int64(len(content))

	directoryFD := int(directory.Fd())
	stagingName, stagedFile, err := createStagedFileAt(directoryFD, operations)
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("create staged evidence bundle archive: %w", err)
	}
	stagingPresent := true
	cleanupStaging := func() error {
		if !stagingPresent {
			return nil
		}
		cleanupErr := operations.unlink(directoryFD, stagingName)
		if cleanupErr == nil || errors.Is(cleanupErr, syscall.ENOENT) {
			stagingPresent = false
			return nil
		}
		return fmt.Errorf("remove staged evidence bundle archive name: %w", cleanupErr)
	}

	stagedInfo, stageErr := writeAndConfirmBundleArchive(stagedFile, content)
	if stageErr != nil {
		closeErr := stagedFile.Close()
		cleanupErr := cleanupStaging()
		return BundleCreateResult{}, fmt.Errorf("stage evidence bundle archive: %w", errors.Join(stageErr, wrapCloseError(closeErr), cleanupErr))
	}
	if err := operations.link(directoryFD, stagingName, outputName); err != nil {
		closeErr := stagedFile.Close()
		cleanupErr := cleanupStaging()
		if errors.Is(err, syscall.EEXIST) {
			err = fmt.Errorf("%w: %s", pathguard.ErrOutputExists, displayOutput)
		} else {
			err = fmt.Errorf("publish evidence bundle archive: %w", err)
		}
		return BundleCreateResult{}, errors.Join(err, wrapCloseError(closeErr), cleanupErr)
	}

	closeErr := wrapCloseError(stagedFile.Close())
	cleanupErr := cleanupStaging()
	syncErr := operations.syncDirectory(directory)
	if syncErr != nil {
		syncErr = fmt.Errorf("sync pinned evidence bundle output directory: %w", syncErr)
	}
	var hookErr error
	if afterPublish != nil {
		if err := afterPublish(); err != nil {
			hookErr = fmt.Errorf("prepare evidence bundle post-publication confirmation: %w", err)
		}
	}
	// Reopen only after the staging name is removed and the directory has been
	// synced. A replacement during cleanup or persistence must not inherit a
	// previously successful confirmation.
	confirmationErr := verifyPublishedBundleArchiveAt(directoryFD, outputName, stagedInfo, content, operations)
	if committedErr := errors.Join(confirmationErr, closeErr, cleanupErr, syncErr, hookErr); committedErr != nil {
		return result, &BundleCommittedError{Result: result, Err: committedErr}
	}
	return result, nil
}

func writeAndConfirmBundleArchive(file *os.File, expected []byte) (os.FileInfo, error) {
	if _, err := file.Write(expected); err != nil {
		return nil, err
	}
	if err := file.Chmod(0o644); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode() != 0o644 || info.Size() != int64(len(expected)) {
		return nil, fmt.Errorf("staged evidence bundle archive identity is not canonical")
	}
	if err := confirmOpenedBundleArchive(file, expected); err != nil {
		return nil, err
	}
	return info, nil
}

func verifyPublishedBundleArchiveAt(directoryFD int, outputName string, stagedInfo os.FileInfo, expected []byte, operations writeNewAtOperations) error {
	fd, err := operations.open(directoryFD, outputName, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open published evidence bundle archive: %w", err)
	}
	published := os.NewFile(uintptr(fd), outputName)
	if published == nil {
		_ = syscall.Close(fd)
		return fmt.Errorf("open published evidence bundle archive returned an invalid descriptor")
	}
	info, inspectErr := published.Stat()
	if inspectErr != nil {
		inspectErr = fmt.Errorf("inspect published evidence bundle archive: %w", inspectErr)
	} else if !info.Mode().IsRegular() || info.Mode() != 0o644 || bundleFileHasMultipleLinks(info) || !os.SameFile(stagedInfo, info) {
		inspectErr = fmt.Errorf("published evidence bundle archive does not identify the verified staged inode")
	}
	var contentErr error
	if inspectErr == nil {
		contentErr = confirmOpenedBundleArchive(published, expected)
	}
	closeErr := published.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close published evidence bundle archive: %w", closeErr)
	}
	return errors.Join(inspectErr, contentErr, closeErr)
}

func confirmOpenedBundleArchive(file *os.File, expected []byte) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind evidence bundle archive: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	if err != nil {
		return fmt.Errorf("read evidence bundle archive: %w", err)
	}
	if !bytes.Equal(content, expected) {
		return fmt.Errorf("evidence bundle archive bytes differ from the verified staged payload")
	}
	return nil
}
