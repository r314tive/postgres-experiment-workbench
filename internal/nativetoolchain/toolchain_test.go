package nativetoolchain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectSnapshotVerifyRelocateAndTamper(t *testing.T) {
	bindir := fakeBindir(t, "baseline")
	installation, err := Inspect(bindir)
	if err != nil {
		t.Fatal(err)
	}
	if installation.Manifest.SourceCommit != Unattested || installation.Manifest.BuildProvenance != Unattested {
		t.Fatalf("fake provenance was invented: %#v", installation.Manifest)
	}
	snapshot := filepath.Join(t.TempDir(), "toolchain")
	if err := Snapshot(installation, snapshot); err != nil {
		t.Fatal(err)
	}
	relocated := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(snapshot, relocated); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySnapshot(relocated, installation.Manifest.Digest); err != nil {
		t.Fatalf("relocated snapshot did not verify: %v", err)
	}
	for _, binary := range installation.Manifest.Binaries {
		info, err := os.Lstat(filepath.Join(relocated, filepath.FromSlash(binary.Path)))
		if err != nil || info.Mode().Perm()&0o111 != 0 {
			t.Fatalf("identity-only snapshot unexpectedly executable: %s (%v)", binary.Path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(relocated, "bin", "postgres"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySnapshot(relocated, installation.Manifest.Digest); err == nil || !(strings.Contains(err.Error(), "digest mismatch") || strings.Contains(err.Error(), "missing or unsafe")) {
		t.Fatalf("tampered snapshot passed: %v", err)
	}
}

func TestInspectRejectsSymlinkSameDigestAndMutation(t *testing.T) {
	baseline := fakeBindir(t, "same")
	candidate := fakeBindir(t, "same")
	left, err := Inspect(baseline)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Inspect(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if left.Manifest.Digest != right.Manifest.Digest {
		t.Fatalf("byte-identical toolchains have different digests")
	}
	if err := os.WriteFile(filepath.Join(candidate, "pgbench"), []byte("#!/bin/sh\necho pgbench-mutated --version\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Revalidate(right); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("mutated installation passed revalidation: %v", err)
	}

	symlinkDir := fakeBindir(t, "symlink")
	if err := os.Remove(filepath.Join(symlinkDir, "psql")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(symlinkDir, "postgres"), filepath.Join(symlinkDir, "psql")); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(symlinkDir); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked executable passed: %v", err)
	}
	nonExecutable := fakeBindir(t, "mode")
	if err := os.Chmod(filepath.Join(nonExecutable, "pgbench"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(nonExecutable); err == nil || !strings.Contains(err.Error(), "executable regular") {
		t.Fatalf("non-executable binary passed: %v", err)
	}
}

func TestRequireComparableVersionsChecksEverySelectedTool(t *testing.T) {
	left, err := Inspect(fakeBindir(t, "left"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := Inspect(fakeBindir(t, "right"))
	if err != nil {
		t.Fatal(err)
	}
	for index := range right.Manifest.Binaries {
		if right.Manifest.Binaries[index].Name == "createdb" {
			right.Manifest.Binaries[index].Version += "-different"
			break
		}
	}
	if err := RequireComparableVersions(left.Manifest, right.Manifest); err == nil || !strings.Contains(err.Error(), "createdb version identity differs") {
		t.Fatalf("non-core tool version mismatch passed: %v", err)
	}
}

func TestVerifySnapshotRejectsStrictJSONAndUnexpectedEntries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, directory string, installation Installation)
		want   string
	}{
		{"unknown JSON field", func(t *testing.T, directory string, _ Installation) {
			path := filepath.Join(directory, ManifestName)
			content, _ := os.ReadFile(path)
			content = []byte(strings.Replace(string(content), `"artifact_type":`, `"unknown": true, "artifact_type":`, 1))
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "unknown field"},
		{"duplicate JSON field", func(t *testing.T, directory string, _ Installation) {
			path := filepath.Join(directory, ManifestName)
			content, _ := os.ReadFile(path)
			content = []byte(strings.Replace(string(content), `"artifact_type":`, `"artifact_type": "pgworkbench.native-toolchain", "artifact_type":`, 1))
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "duplicate JSON object key"},
		{"trailing JSON", func(t *testing.T, directory string, _ Installation) {
			path := filepath.Join(directory, ManifestName)
			content, _ := os.ReadFile(path)
			if err := os.WriteFile(path, append(content, []byte("{}\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "trailing"},
		{"extra file", func(t *testing.T, directory string, _ Installation) {
			if err := os.WriteFile(filepath.Join(directory, "extra"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "unexpected file"},
		{"extra directory", func(t *testing.T, directory string, _ Installation) {
			if err := os.Mkdir(filepath.Join(directory, "extra"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "unexpected directory"},
		{"symlink binary", func(t *testing.T, directory string, _ Installation) {
			path := filepath.Join(directory, "bin", "psql")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("postgres", path); err != nil {
				t.Fatal(err)
			}
		}, "missing or unsafe"},
		{"executable snapshot mode", func(t *testing.T, directory string, _ Installation) {
			if err := os.Chmod(filepath.Join(directory, "bin", "pgbench"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "missing or unsafe"},
		{"manifest mode", func(t *testing.T, directory string, _ Installation) {
			if err := os.Chmod(filepath.Join(directory, ManifestName), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "manifest is missing or unsafe"},
		{"root directory mode", func(t *testing.T, directory string, _ Installation) {
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
		}, "snapshot is missing or unsafe"},
		{"bin directory mode", func(t *testing.T, directory string, _ Installation) {
			if err := os.Chmod(filepath.Join(directory, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, "unexpected directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installation, err := Inspect(fakeBindir(t, test.name))
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(t.TempDir(), "snapshot")
			if err := Snapshot(installation, directory); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, directory, installation)
			if _, err := VerifySnapshot(directory, installation.Manifest.Digest); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mutation passed: got %v, want %q", err, test.want)
			}
		})
	}
}

func TestManifestJSONRoundTripHasNoLocalBindir(t *testing.T) {
	installation, err := Inspect(fakeBindir(t, "portable"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(installation.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), installation.Bindir) || strings.Contains(string(content), t.TempDir()) {
		t.Fatalf("portable manifest contains a local absolute path: %s", content)
	}
}

func fakeBindir(t *testing.T, identity string) string {
	t.Helper()
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range RequiredExecutableNames() {
		content := "#!/bin/sh\necho '" + name + " (PostgreSQL) " + identity + "'\n"
		if err := os.WriteFile(filepath.Join(bindir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bindir
}
