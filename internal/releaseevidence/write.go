package releaseevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

// WriteResult identifies one exclusively published index revision.
type WriteResult struct {
	Output       string       `json:"output"`
	Digest       string       `json:"digest"`
	Index        Index        `json:"index"`
	Verification Verification `json:"verification"`
}

// CommittedError reports that an exclusive destination was created but a
// post-publication identity or directory-durability confirmation failed. The
// Result preserves the requested destination and expected digest so callers do
// not retry blindly and mistake ErrOutputExists for the original outcome. If a
// directory pathname changed, the requested path may no longer resolve to the
// committed inode and must not be presented as a confirmed current location.
type CommittedError struct {
	Result WriteResult
	Err    error
}

func (err *CommittedError) Error() string {
	return fmt.Sprintf("release evidence index publication reached committed state for requested output %s with expected digest %s, but final confirmation failed: %v", err.Result.Output, err.Result.Digest, err.Err)
}

func (err *CommittedError) Unwrap() error {
	return err.Err
}

// WriteNew serializes and semantically verifies an index before atomically
// publishing it at a destination that must not exist. Evidence revisions are
// immutable: callers create a new path rather than replacing an old index.
func WriteNew(output string, index Index) (WriteResult, error) {
	return writeNew(output, "", index, pathguard.PublishFileExclusive, syncDirectory)
}

// WriteNewOutside is WriteNew with a final containment check against an
// immutable source directory. The check is repeated while preparing the
// destination, after any earlier input verification, so a parent path changed
// into a symlink cannot redirect the evidence index into sourceDir.
func WriteNewOutside(sourceDir string, output string, index Index) (WriteResult, error) {
	if sourceDir == "" {
		return WriteResult{}, fmt.Errorf("immutable source directory is required")
	}
	return writeNew(output, sourceDir, index, pathguard.PublishFileExclusive, syncDirectory)
}

func writeNew(
	output string,
	forbiddenRoot string,
	index Index,
	publish func(string, string) error,
	syncOutputDirectory func(string) error,
) (WriteResult, error) {
	verification := Verify(index)
	if !verification.Valid {
		return WriteResult{}, fmt.Errorf("refuse to write invalid release evidence index: %s", joinIssues(verification.Issues))
	}

	content, err := encodeIndex(index)
	if err != nil {
		return WriteResult{}, err
	}
	var target string
	if forbiddenRoot == "" {
		target, err = pathguard.PrepareNewOutput(output, 0o755)
	} else {
		target, err = pathguard.PrepareNewOutputOutside(forbiddenRoot, output, 0o755)
	}
	if err != nil {
		return WriteResult{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".pgworkbench-release-evidence-*")
	if err != nil {
		return WriteResult{}, fmt.Errorf("create staged release evidence index: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	writeErr := func() error {
		if _, err := temporary.Write(content); err != nil {
			return err
		}
		if err := temporary.Chmod(0o644); err != nil {
			return err
		}
		if err := temporary.Sync(); err != nil {
			return err
		}
		return temporary.Close()
	}()
	if writeErr != nil {
		_ = temporary.Close()
		return WriteResult{}, fmt.Errorf("stage release evidence index: %w", writeErr)
	}

	staged, err := VerifyFile(temporaryPath)
	if err != nil {
		return WriteResult{}, fmt.Errorf("verify staged release evidence index: %w", err)
	}
	if !staged.Valid {
		return WriteResult{}, fmt.Errorf("verify staged release evidence index: %s", joinIssues(staged.Issues))
	}
	stagedInfo, err := os.Lstat(temporaryPath)
	if err != nil {
		return WriteResult{}, fmt.Errorf("inspect staged release evidence index: %w", err)
	}
	digest := sha256.Sum256(content)
	result := WriteResult{
		Output:       target,
		Digest:       "sha256:" + hex.EncodeToString(digest[:]),
		Index:        index,
		Verification: staged,
	}
	if err := publish(temporaryPath, target); err != nil {
		// PublishFileExclusive first links the complete inode at target and then
		// removes its staging name. If that cleanup fails, target is already an
		// immutable committed destination. Detect the inode explicitly so the
		// caller receives its identity and does not retry blindly.
		if publishedTargetIsStagedInode(target, stagedInfo) {
			confirmationErr := verifyPublishedFile(target, stagedInfo, content)
			if confirmationErr != nil {
				err = fmt.Errorf("publish and confirm committed release evidence index: %w", errors.Join(err, confirmationErr))
			}
			return result, &CommittedError{Result: result, Err: err}
		}
		return WriteResult{}, err
	}
	if err := verifyPublishedFile(target, stagedInfo, content); err != nil {
		return result, &CommittedError{Result: result, Err: err}
	}
	if err := syncOutputDirectory(filepath.Dir(target)); err != nil {
		return result, &CommittedError{Result: result, Err: err}
	}
	return result, nil
}

func publishedTargetIsStagedInode(path string, stagedInfo os.FileInfo) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && os.SameFile(stagedInfo, info)
}

func encodeIndex(index Index) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(index); err != nil {
		return nil, fmt.Errorf("encode release evidence index: %w", err)
	}
	return buffer.Bytes(), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open release evidence output directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync release evidence output directory: %w", err)
	}
	return nil
}

func verifyPublishedFile(path string, stagedInfo os.FileInfo, expected []byte) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect published release evidence index: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(stagedInfo, pathInfo) {
		return fmt.Errorf("published release evidence index does not identify the verified staged inode")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open published release evidence index: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened published release evidence index: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(stagedInfo, openedInfo) {
		return fmt.Errorf("published release evidence index changed while it was being opened")
	}
	content, err := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	if err != nil {
		return fmt.Errorf("read published release evidence index: %w", err)
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect read published release evidence index: %w", err)
	}
	if !os.SameFile(openedInfo, finalInfo) || finalInfo.Size() != int64(len(content)) || !bytes.Equal(content, expected) {
		return fmt.Errorf("published release evidence index bytes differ from the verified staged revision")
	}
	return nil
}

func joinIssues(issues []string) string {
	if len(issues) == 0 {
		return "unknown semantic defect"
	}
	return strings.Join(issues, "; ")
}
