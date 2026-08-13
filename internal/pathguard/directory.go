package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsafeDirectory means an artifact directory would be reached through a
// symlink or another non-directory path component.
var ErrUnsafeDirectory = errors.New("artifact directory path is unsafe")

// EnsureDirectory creates relative below root one component at a time and
// returns its canonical absolute path. Existing components must be real
// directories, never symlinks. This prevents a repository-local artifact root
// such as runs/ from redirecting writes outside the repository.
func EnsureDirectory(root, relative string, mode fs.FileMode) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve artifact root: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", fmt.Errorf("resolve artifact root symlinks: %w", err)
	}
	rootInfo, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("inspect artifact root: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("%w: artifact root is not a directory: %s", ErrUnsafeDirectory, canonicalRoot)
	}

	clean := filepath.Clean(relative)
	if relative == "" || clean == "." || filepath.IsAbs(relative) || clean != relative || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: artifact directory must be a clean relative path: %q", ErrUnsafeDirectory, relative)
	}

	current := canonicalRoot
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("%w: invalid artifact directory component in %q", ErrUnsafeDirectory, relative)
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if mkdirErr := os.Mkdir(current, mode); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return "", fmt.Errorf("create artifact directory %s: %w", current, mkdirErr)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return "", fmt.Errorf("inspect artifact directory %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%w: component is not a real directory: %s", ErrUnsafeDirectory, current)
		}
	}

	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve artifact directory: %w", err)
	}
	if resolved != current || !within(canonicalRoot, resolved) {
		return "", fmt.Errorf("%w: directory resolves outside its canonical root: %s", ErrUnsafeDirectory, current)
	}
	// Preserve the caller's absolute root spelling (for example macOS /var
	// versus /private/var) so portable references computed relative to root do
	// not spuriously escape. Safety was established against canonicalRoot.
	return filepath.Join(absoluteRoot, clean), nil
}
