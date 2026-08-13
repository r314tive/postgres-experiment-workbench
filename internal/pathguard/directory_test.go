package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDirectoryCreatesOnlyCanonicalChildren(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "runs", "benchmarks")
	got, err := EnsureDirectory(root, filepath.Join("runs", "benchmarks"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("EnsureDirectory() = %q, want %q", got, want)
	}
	if info, err := os.Lstat(got); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("created directory is unsafe: info=%v err=%v", info, err)
	}
}

func TestEnsureDirectoryRejectsSymlinkedAncestorBeforeWritingOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "runs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := EnsureDirectory(root, filepath.Join("runs", "benchmarks"), 0o755)
	if !errors.Is(err, ErrUnsafeDirectory) {
		t.Fatalf("EnsureDirectory() error = %v, want ErrUnsafeDirectory", err)
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "benchmarks")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe destination was modified: %v", statErr)
	}
}

func TestEnsureDirectoryRejectsUncleanAndNonDirectoryComponents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "runs"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{filepath.Join("..", "outside"), "runs" + string(filepath.Separator) + ".." + string(filepath.Separator) + "benchmarks", "runs"} {
		t.Run(relative, func(t *testing.T) {
			_, err := EnsureDirectory(root, relative, 0o755)
			if !errors.Is(err, ErrUnsafeDirectory) {
				t.Fatalf("EnsureDirectory(%q) error = %v, want ErrUnsafeDirectory", relative, err)
			}
		})
	}
}
