package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveOutputOutsideRejectsDirectChildAndAliasedParentWithoutCreatingPaths(t *testing.T) {
	source := t.TempDir()
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tests := []struct {
		name   string
		output string
	}{
		{name: "direct child with missing parents", output: filepath.Join(source, "missing", "nested", "direct.tar.gz")},
		{name: "aliased parent", output: filepath.Join(alias, "aliased.tar.gz")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveOutputOutside(source, test.output)
			if !errors.Is(err, ErrOutputWithinSource) {
				t.Fatalf("expected output-containment error, got %v", err)
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(source, "missing")); !os.IsNotExist(err) {
		t.Fatalf("containment check created an output parent: %v", err)
	}
}

func TestPrepareNewOutputCreatesCanonicalParentsAndRejectsExistingLeaf(t *testing.T) {
	outside := t.TempDir()
	alias := filepath.Join(t.TempDir(), "outside-alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	output, err := PrepareNewOutput(filepath.Join(alias, "new", "artifact.json"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	canonicalOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalOutside, "new", "artifact.json")
	if output != want {
		t.Fatalf("PrepareNewOutput() = %q, want %q", output, want)
	}
	if info, err := os.Lstat(filepath.Dir(output)); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("prepared output parent is unsafe: info=%v err=%v", info, err)
	}

	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, existing := range []string{
		target,
		filepath.Join(outside, "existing-directory"),
		filepath.Join(outside, "target-link"),
	} {
		if strings.HasSuffix(existing, "directory") {
			if err := os.Mkdir(existing, 0o755); err != nil {
				t.Fatal(err)
			}
		} else if strings.HasSuffix(existing, "link") {
			if err := os.Symlink(target, existing); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}
		if _, err := PrepareNewOutput(existing, 0o755); !errors.Is(err, ErrOutputExists) {
			t.Fatalf("PrepareNewOutput(%q) error = %v, want ErrOutputExists", existing, err)
		}
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "sentinel\n" {
		t.Fatalf("existing target changed: content=%q err=%v", content, err)
	}
}

func TestPrepareNewOutputOutsideRejectsAliasBeforeCreatingParent(t *testing.T) {
	source := t.TempDir()
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := PrepareNewOutputOutside(source, filepath.Join(alias, "missing", "artifact.tar.gz"), 0o755)
	if !errors.Is(err, ErrOutputWithinSource) {
		t.Fatalf("PrepareNewOutputOutside() error = %v, want ErrOutputWithinSource", err)
	}
	if _, statErr := os.Lstat(filepath.Join(source, "missing")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe output parent was created: %v", statErr)
	}
}

func TestPublishFileExclusivePublishesWithoutReplacingExistingPath(t *testing.T) {
	dir := t.TempDir()
	destination, err := PrepareNewOutput(filepath.Join(dir, "artifact.json"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".artifact-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	temporaryPath := temporary.Name()
	if _, err := temporary.WriteString("artifact\n"); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := PublishFileExclusive(temporaryPath, destination); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(destination); err != nil || string(content) != "artifact\n" {
		t.Fatalf("published content=%q err=%v", content, err)
	}
	if _, err := os.Lstat(temporaryPath); !os.IsNotExist(err) {
		t.Fatalf("temporary name still exists: %v", err)
	}

	existing := filepath.Join(dir, "sentinel.txt")
	if err := os.WriteFile(existing, []byte("sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	temporary, err = os.CreateTemp(dir, ".artifact-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	temporaryPath = temporary.Name()
	if _, err := temporary.WriteString("replacement\n"); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(temporaryPath)
	if err := PublishFileExclusive(temporaryPath, existing); !errors.Is(err, ErrOutputExists) {
		t.Fatalf("PublishFileExclusive() error = %v, want ErrOutputExists", err)
	}
	if content, err := os.ReadFile(existing); err != nil || string(content) != "sentinel\n" {
		t.Fatalf("existing destination changed: content=%q err=%v", content, err)
	}
}
