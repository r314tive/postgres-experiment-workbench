// Package pathguard provides filesystem containment checks for immutable
// artifacts. It resolves existing symlink ancestors without creating output
// paths, so callers can reject an archive destination before any write occurs.
package pathguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutputWithinSource means an output path resolves to the immutable source
// directory itself or one of its descendants.
var ErrOutputWithinSource = errors.New("output resolves within source directory")

// ErrOutputExists means an immutable artifact destination already exists.
// Producers must choose a new path instead of replacing an existing file,
// directory, or symlink.
var ErrOutputExists = errors.New("output already exists")

// ResolveOutputOutside returns a canonical absolute output path after proving
// that it is outside sourceDir. Existing output-parent ancestors are resolved
// with EvalSymlinks; missing descendants are then appended without creating
// them. The output basename is appended last, so a symlinked parent cannot
// disguise a write into the immutable source tree.
func ResolveOutputOutside(sourceDir, output string) (string, error) {
	source, err := canonicalDirectory(sourceDir, "source directory")
	if err != nil {
		return "", err
	}

	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	parent, err := resolveProspectiveDirectory(filepath.Dir(absoluteOutput))
	if err != nil {
		return "", fmt.Errorf("resolve output parent: %w", err)
	}
	canonicalOutput := filepath.Join(parent, filepath.Base(absoluteOutput))
	if within(source, canonicalOutput) {
		return "", ErrOutputWithinSource
	}
	return canonicalOutput, nil
}

// PrepareNewOutput creates any missing parent directories one component at a
// time, resolves existing parent symlinks, and returns a canonical absolute
// destination. The destination itself must not already exist. directoryMode
// applies only to parents created by this function.
func PrepareNewOutput(output string, directoryMode os.FileMode) (string, error) {
	return prepareNewOutput(output, directoryMode, "")
}

// PrepareNewOutputOutside is PrepareNewOutput with the additional guarantee
// that the destination resolves outside sourceDir. Containment is checked
// before any parent is created and again against the final canonical parent.
func PrepareNewOutputOutside(sourceDir, output string, directoryMode os.FileMode) (string, error) {
	source, err := canonicalDirectory(sourceDir, "source directory")
	if err != nil {
		return "", err
	}
	return prepareNewOutput(output, directoryMode, source)
}

// PublishFileExclusive atomically gives destination a staged regular file's
// inode without replacing an existing path. The staged file must be in the
// same directory as destination. A hard-link publication provides O_EXCL-like
// semantics for an already-complete file; the temporary name is then removed.
func PublishFileExclusive(temporary, destination string) error {
	absTemporary, err := filepath.Abs(temporary)
	if err != nil {
		return fmt.Errorf("resolve staged output: %w", err)
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve output destination: %w", err)
	}
	temporaryParent, err := filepath.EvalSymlinks(filepath.Dir(absTemporary))
	if err != nil {
		return fmt.Errorf("resolve staged output parent: %w", err)
	}
	destinationParent, err := filepath.EvalSymlinks(filepath.Dir(absDestination))
	if err != nil {
		return fmt.Errorf("resolve output destination parent: %w", err)
	}
	if temporaryParent != destinationParent {
		return fmt.Errorf("staged output and destination must share a directory")
	}
	info, err := os.Lstat(absTemporary)
	if err != nil {
		return fmt.Errorf("inspect staged output: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staged output is not a regular file: %s", absTemporary)
	}
	if _, err := os.Lstat(absDestination); err == nil {
		return fmt.Errorf("%w: %s", ErrOutputExists, absDestination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output destination: %w", err)
	}
	if err := os.Link(absTemporary, absDestination); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: %s", ErrOutputExists, absDestination)
		}
		return fmt.Errorf("publish output: %w", err)
	}
	if err := os.Remove(absTemporary); err != nil {
		return fmt.Errorf("remove staged output name after publication: %w", err)
	}
	return nil
}

func prepareNewOutput(output string, directoryMode os.FileMode, forbiddenRoot string) (string, error) {
	if output == "" {
		return "", fmt.Errorf("output path is required")
	}
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	if filepath.Base(absoluteOutput) == "." || filepath.Base(absoluteOutput) == string(filepath.Separator) {
		return "", fmt.Errorf("output path must name a file: %s", absoluteOutput)
	}
	parent, err := prepareProspectiveDirectory(filepath.Dir(absoluteOutput), directoryMode, forbiddenRoot)
	if err != nil {
		return "", fmt.Errorf("prepare output parent: %w", err)
	}
	destination := filepath.Join(parent, filepath.Base(absoluteOutput))
	if forbiddenRoot != "" && within(forbiddenRoot, destination) {
		return "", ErrOutputWithinSource
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", fmt.Errorf("%w: %s", ErrOutputExists, destination)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect output destination: %w", err)
	}
	return destination, nil
}

func prepareProspectiveDirectory(path string, mode os.FileMode, forbiddenRoot string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cursor := absolute
	var missing []string
	for {
		info, statErr := os.Lstat(cursor)
		if statErr == nil {
			resolved, evalErr := filepath.EvalSymlinks(cursor)
			if evalErr != nil {
				return "", evalErr
			}
			resolvedInfo, statErr := os.Stat(resolved)
			if statErr != nil {
				return "", statErr
			}
			if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() || !resolvedInfo.IsDir() {
				return "", fmt.Errorf("existing output ancestor is not a directory: %s", cursor)
			}
			cursor = resolved
			break
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", statErr
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}

	for index := len(missing) - 1; index >= 0; index-- {
		next := filepath.Join(cursor, missing[index])
		if forbiddenRoot != "" && within(forbiddenRoot, next) {
			return "", ErrOutputWithinSource
		}
		info, statErr := os.Lstat(next)
		if os.IsNotExist(statErr) {
			if mkdirErr := os.Mkdir(next, mode); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return "", mkdirErr
			}
			info, statErr = os.Lstat(next)
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("output parent component is not a real directory: %s", next)
		}
		cursor = next
	}
	resolved, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", err
	}
	if resolved != cursor {
		return "", fmt.Errorf("output parent changed while being prepared: %s", cursor)
	}
	return resolved, nil
}

func canonicalDirectory(path, label string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s symlinks: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory: %s", label, resolved)
	}
	return resolved, nil
}

// resolveProspectiveDirectory canonicalizes the nearest existing ancestor and
// reattaches missing path components. This retains callers' ability to create
// a new output directory while still detecting symlinks in every existing
// ancestor.
func resolveProspectiveDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cursor := absolute
	var missing []string
	for {
		info, statErr := os.Stat(cursor)
		if statErr == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("existing output ancestor is not a directory: %s", cursor)
			}
			resolved, evalErr := filepath.EvalSymlinks(cursor)
			if evalErr != nil {
				return "", evalErr
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", statErr
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
