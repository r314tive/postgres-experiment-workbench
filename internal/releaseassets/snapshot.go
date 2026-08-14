package releaseassets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

const verifiedSnapshotPrefix = "pgworkbench-release-assets-"

// VerifiedSnapshot is a private, process-owned copy of one complete release
// asset inventory. Semantic verification must run against Root rather than the
// caller-controlled source paths, so repeated archive/SBOM/manifest reads all
// observe the exact bytes whose size and digest matched the inventory.
type VerifiedSnapshot struct {
	root     string
	base     string
	rootInfo os.FileInfo
}

func (snapshot *VerifiedSnapshot) Root() string {
	if snapshot == nil {
		return ""
	}
	return snapshot.root
}

// Close removes only the private directory created by CreateVerifiedSnapshot.
func (snapshot *VerifiedSnapshot) Close() error {
	if snapshot == nil || snapshot.root == "" {
		return nil
	}
	root := snapshot.root
	if filepath.Dir(root) != snapshot.base || !strings.HasPrefix(filepath.Base(root), verifiedSnapshotPrefix) {
		return fmt.Errorf("refuse to remove unrecognized release asset snapshot: %s", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect release asset snapshot before removal: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || snapshot.rootInfo == nil || !os.SameFile(snapshot.rootInfo, info) {
		return fmt.Errorf("refuse to remove changed release asset snapshot: %s", root)
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove release asset snapshot: %w", err)
	}
	snapshot.root = ""
	return nil
}

// CreateVerifiedSnapshot copies the exact closed source root into a private
// directory while independently enforcing every inventory size and digest.
// The caller must Close the returned snapshot. Provider authenticity is not
// established by this local byte snapshot.
func CreateVerifiedSnapshot(root string, inventory Inventory) (*VerifiedSnapshot, error) {
	verification := Verify(inventory)
	if !verification.Valid {
		return nil, fmt.Errorf("invalid release asset inventory: %s", strings.Join(verification.Issues, "; "))
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve release asset root: %w", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect release asset root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("release asset root must be a non-symlink directory")
	}
	if err := requireExactSourceEntries(absoluteRoot, inventory); err != nil {
		return nil, err
	}
	tempProbe, err := pathguard.ResolveOutputOutside(
		absoluteRoot,
		filepath.Join(os.TempDir(), verifiedSnapshotPrefix+"containment-probe"),
	)
	if err != nil {
		return nil, fmt.Errorf("resolve private snapshot base outside release assets: %w", err)
	}
	snapshotBase := filepath.Dir(tempProbe)

	snapshotRoot, err := os.MkdirTemp(snapshotBase, verifiedSnapshotPrefix)
	if err != nil {
		return nil, fmt.Errorf("create private release asset snapshot: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(snapshotRoot)
		}
	}()
	if _, err := pathguard.ResolveOutputOutside(absoluteRoot, filepath.Join(snapshotRoot, "containment-probe")); err != nil {
		return nil, fmt.Errorf("private snapshot resolved inside release assets: %w", err)
	}
	snapshotInfo, err := os.Lstat(snapshotRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect private release asset snapshot: %w", err)
	}
	if snapshotInfo.Mode()&os.ModeSymlink != 0 || !snapshotInfo.IsDir() {
		return nil, fmt.Errorf("private release asset snapshot is not a real directory")
	}

	for _, asset := range inventory.Assets {
		if err := copyVerifiedAsset(
			filepath.Join(absoluteRoot, asset.Name),
			filepath.Join(snapshotRoot, asset.Name),
			asset,
		); err != nil {
			return nil, fmt.Errorf("snapshot release asset %s: %w", asset.Name, err)
		}
	}
	finalRootInfo, err := os.Lstat(absoluteRoot)
	if err != nil || !os.SameFile(rootInfo, finalRootInfo) {
		return nil, fmt.Errorf("release asset root changed while snapshotting")
	}
	if err := requireExactSourceEntries(absoluteRoot, inventory); err != nil {
		return nil, fmt.Errorf("release asset root changed while snapshotting: %w", err)
	}

	keep = true
	return &VerifiedSnapshot{root: snapshotRoot, base: snapshotBase, rootInfo: snapshotInfo}, nil
}

func requireExactSourceEntries(root string, inventory Inventory) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read release asset root: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		actual = append(actual, entry.Name())
		info, statErr := os.Lstat(filepath.Join(root, entry.Name()))
		if statErr != nil {
			return fmt.Errorf("inspect release asset %s: %w", entry.Name(), statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("release asset root entry is not a regular non-symlink file: %s", entry.Name())
		}
	}
	sort.Strings(actual)
	expected := make([]string, 0, len(inventory.Assets))
	for _, asset := range inventory.Assets {
		expected = append(expected, asset.Name)
	}
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("release asset root does not equal inventory: got %v want %v", actual, expected)
	}
	return nil
}

func copyVerifiedAsset(sourcePath string, destinationPath string, asset Asset) error {
	pathInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular non-symlink file")
	}
	if pathInfo.Size() != asset.Size {
		return fmt.Errorf("source size = %d, want inventory size %d", pathInfo.Size(), asset.Size)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	openedInfo, err := source.Stat()
	if err != nil {
		return err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return fmt.Errorf("source changed while it was being opened")
	}

	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.CopyN(io.MultiWriter(destination, hasher), source, asset.Size)
	if copyErr == nil {
		extra, extraErr := io.CopyN(io.Discard, source, 1)
		switch {
		case extra != 0:
			copyErr = fmt.Errorf("source exceeds inventory size %d", asset.Size)
		case extraErr != nil && !errors.Is(extraErr, io.EOF):
			copyErr = extraErr
		}
	}
	if copyErr == nil && written != asset.Size {
		copyErr = fmt.Errorf("copied %d bytes, want %d", written, asset.Size)
	}
	if copyErr == nil {
		digest := evidence.DigestPrefix + hex.EncodeToString(hasher.Sum(nil))
		if digest != asset.Digest {
			copyErr = fmt.Errorf("source digest = %s, want inventory digest %s", digest, asset.Digest)
		}
	}
	if copyErr == nil {
		finalInfo, statErr := source.Stat()
		if statErr != nil {
			copyErr = statErr
		} else if !os.SameFile(openedInfo, finalInfo) || finalInfo.Size() != asset.Size {
			copyErr = fmt.Errorf("source changed while it was being copied")
		}
	}
	if copyErr == nil {
		finalPathInfo, statErr := os.Lstat(sourcePath)
		if statErr != nil {
			copyErr = statErr
		} else if finalPathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, finalPathInfo) {
			copyErr = fmt.Errorf("source path changed while it was being copied")
		}
	}
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	return nil
}
