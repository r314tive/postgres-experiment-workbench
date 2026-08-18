package benchmarkab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareBundleFilesFailsClosedOnTamperAndExtras(t *testing.T) {
	recorded := []BundleFile{{Path: "runs/a/result.json", Size: 10, Digest: testDigest("1")}}
	if issues := compareBundleFiles(recorded, append([]BundleFile(nil), recorded...)); len(issues) != 0 {
		t.Fatalf("matching inventory failed: %v", issues)
	}
	tampered := []BundleFile{{Path: "runs/a/result.json", Size: 11, Digest: testDigest("2")}, {Path: "extra", Size: 1, Digest: testDigest("3")}}
	if issues := compareBundleFiles(recorded, tampered); len(issues) < 2 {
		t.Fatalf("tampered/extra closure did not fail closed: %v", issues)
	}
	duplicate := append(append([]BundleFile(nil), recorded...), recorded[0])
	if issues := compareBundleFiles(duplicate, recorded); !reasonContains(issues, "duplicate path") {
		t.Fatalf("duplicate inventory path passed: %v", issues)
	}
}

func TestCopyTreeRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, destination); err == nil {
		t.Fatal("bundle copied a symlink")
	}
}
