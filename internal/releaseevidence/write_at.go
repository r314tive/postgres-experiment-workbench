//go:build darwin || linux

package releaseevidence

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/strictjson"
)

const maximumStagingNameAttempts = 64

type writeNewAtOperations struct {
	open          func(int, string, int, uint32) (int, error)
	link          func(int, string, string) error
	unlink        func(int, string) error
	syncDirectory func(*os.File) error
}

func systemWriteNewAtOperations() writeNewAtOperations {
	return writeNewAtOperations{
		open:   openAt,
		link:   linkAt,
		unlink: unlinkAt,
		syncDirectory: func(directory *os.File) error {
			return directory.Sync()
		},
	}
}

// WriteNewAt serializes and verifies index, then exclusively publishes it as
// outputName relative to one already-open directory descriptor. outputName
// must be a basename. displayOutput is reporting metadata only and is never
// used for filesystem access, so a concurrent rename or path replacement
// cannot redirect staging, publication, confirmation, cleanup, or fsync.
func WriteNewAt(directory *os.File, outputName, displayOutput string, index Index) (WriteResult, error) {
	return writeNewAt(directory, outputName, displayOutput, index, systemWriteNewAtOperations())
}

func writeNewAt(directory *os.File, outputName, displayOutput string, index Index, operations writeNewAtOperations) (WriteResult, error) {
	if directory == nil {
		return WriteResult{}, fmt.Errorf("release evidence output directory is required")
	}
	if err := validateDirectoryEntryName(outputName); err != nil {
		return WriteResult{}, err
	}
	if strings.TrimSpace(displayOutput) == "" {
		return WriteResult{}, fmt.Errorf("release evidence display output is required")
	}
	directoryInfo, err := directory.Stat()
	if err != nil {
		return WriteResult{}, fmt.Errorf("inspect pinned release evidence output directory: %w", err)
	}
	if !directoryInfo.IsDir() {
		return WriteResult{}, fmt.Errorf("pinned release evidence output is not a directory")
	}

	verification := Verify(index)
	if !verification.Valid {
		return WriteResult{}, fmt.Errorf("refuse to write invalid release evidence index: %s", joinIssues(verification.Issues))
	}
	content, err := encodeIndex(index)
	if err != nil {
		return WriteResult{}, err
	}

	directoryFD := int(directory.Fd())
	stagingName, stagedFile, err := createStagedFileAt(directoryFD, operations)
	if err != nil {
		return WriteResult{}, fmt.Errorf("create staged release evidence index: %w", err)
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
		return fmt.Errorf("remove staged release evidence name: %w", cleanupErr)
	}

	stagedInfo, stagedVerification, stageErr := writeAndVerifyStagedFile(stagedFile, content)
	if stageErr != nil {
		closeErr := stagedFile.Close()
		cleanupErr := cleanupStaging()
		return WriteResult{}, fmt.Errorf("stage release evidence index: %w", errors.Join(stageErr, wrapCloseError(closeErr), cleanupErr))
	}
	verification = stagedVerification

	digest := sha256.Sum256(content)
	result := WriteResult{
		Output:       displayOutput,
		Digest:       "sha256:" + hex.EncodeToString(digest[:]),
		Index:        index,
		Verification: verification,
	}

	if err := operations.link(directoryFD, stagingName, outputName); err != nil {
		closeErr := stagedFile.Close()
		cleanupErr := cleanupStaging()
		if errors.Is(err, syscall.EEXIST) {
			err = fmt.Errorf("%w: %s", pathguard.ErrOutputExists, displayOutput)
		} else {
			err = fmt.Errorf("publish release evidence index: %w", err)
		}
		return WriteResult{}, errors.Join(err, wrapCloseError(closeErr), cleanupErr)
	}

	// The destination now exists exclusively. Every later failure is a
	// committed-but-unconfirmed outcome and must retain result identity.
	confirmationErr := verifyPublishedFileAt(directoryFD, outputName, stagedInfo, content, operations)
	closeErr := wrapCloseError(stagedFile.Close())
	cleanupErr := cleanupStaging()
	syncErr := operations.syncDirectory(directory)
	if syncErr != nil {
		syncErr = fmt.Errorf("sync pinned release evidence output directory: %w", syncErr)
	}
	if committedErr := errors.Join(confirmationErr, closeErr, cleanupErr, syncErr); committedErr != nil {
		return result, &CommittedError{Result: result, Err: committedErr}
	}
	return result, nil
}

func createStagedFileAt(directoryFD int, operations writeNewAtOperations) (string, *os.File, error) {
	for attempt := 0; attempt < maximumStagingNameAttempts; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", nil, err
		}
		name := ".pgworkbench-release-evidence-" + hex.EncodeToString(nonce[:])
		fd, err := operations.open(
			directoryFD,
			name,
			syscall.O_RDWR|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
			0o600,
		)
		if errors.Is(err, syscall.EEXIST) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, os.NewFile(uintptr(fd), name), nil
	}
	return "", nil, fmt.Errorf("could not allocate an exclusive staging name after %d attempts", maximumStagingNameAttempts)
}

func writeAndVerifyStagedFile(file *os.File, expected []byte) (os.FileInfo, Verification, error) {
	if _, err := file.Write(expected); err != nil {
		return nil, Verification{}, err
	}
	if err := file.Chmod(0o644); err != nil {
		return nil, Verification{}, err
	}
	if err := file.Sync(); err != nil {
		return nil, Verification{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, Verification{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, Verification{}, fmt.Errorf("staged release evidence index is not a regular file")
	}
	content, err := strictjson.ReadOpenedFile(file, int64(len(expected)))
	if err != nil {
		return nil, Verification{}, err
	}
	if !bytes.Equal(content, expected) {
		return nil, Verification{}, fmt.Errorf("staged release evidence index bytes differ from encoded revision")
	}
	parsed, err := Parse(content)
	if err != nil {
		return nil, Verification{}, err
	}
	verification := Verify(parsed)
	if !verification.Valid {
		return nil, Verification{}, fmt.Errorf("staged release evidence index is invalid: %s", joinIssues(verification.Issues))
	}
	return info, verification, nil
}

func verifyPublishedFileAt(directoryFD int, outputName string, stagedInfo os.FileInfo, expected []byte, operations writeNewAtOperations) error {
	fd, err := operations.open(directoryFD, outputName, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open published release evidence index: %w", err)
	}
	published := os.NewFile(uintptr(fd), outputName)
	if published == nil {
		_ = syscall.Close(fd)
		return fmt.Errorf("open published release evidence index returned an invalid descriptor")
	}

	publishedInfo, inspectErr := published.Stat()
	if inspectErr != nil {
		inspectErr = fmt.Errorf("inspect published release evidence index: %w", inspectErr)
	} else if !publishedInfo.Mode().IsRegular() || publishedInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(stagedInfo, publishedInfo) {
		inspectErr = fmt.Errorf("published release evidence index does not identify the verified staged inode")
	}
	var contentErr error
	if inspectErr == nil {
		var content []byte
		content, contentErr = strictjson.ReadOpenedFile(published, int64(len(expected)))
		if contentErr != nil {
			contentErr = fmt.Errorf("read published release evidence index: %w", contentErr)
		} else if !bytes.Equal(content, expected) {
			contentErr = fmt.Errorf("published release evidence index bytes differ from the verified staged revision")
		}
	}
	closeErr := published.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close published release evidence index: %w", closeErr)
	}
	return errors.Join(inspectErr, contentErr, closeErr)
}

func wrapCloseError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close staged release evidence index: %w", err)
}
