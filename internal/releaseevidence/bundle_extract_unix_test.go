//go:build darwin || linux

package releaseevidence

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBundleVerifiesAfterPermissionPreservingSystemTarExtraction(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skipf("system tar unavailable: %v", err)
	}
	chain := t.TempDir()
	head := createBundleTestChain(t, chain, GateStatusPassed)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(head, archive); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	command := exec.Command("sh", "-c", `umask 077; exec tar -xpzf "$1" -C "$2"`, "sh", archive, destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("permission-preserving tar extraction failed: %v\n%s", err, output)
	}
	root := filepath.Join(destination, BundleRootName)
	verification, err := VerifyBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid {
		t.Fatalf("system-tar relocation failed: %+v", verification)
	}
	for _, name := range append(bundleIndexNames(verification.HeadRevision), BundleInventoryName) {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("extracted %s mode=%v err=%v, want 0644", name, info, err)
		}
	}
}
