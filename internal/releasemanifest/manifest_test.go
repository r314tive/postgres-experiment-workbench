package releasemanifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasearchive"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasesbom"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func TestCreateWriteAndVerify(t *testing.T) {
	releaseDir, packRoot := releaseFixture(t, "1.2.3")
	epoch := int64(1_700_000_000)
	manifest, err := Create(CreateOptions{
		ReleaseDir:      releaseDir,
		Version:         "1.2.3",
		GitCommit:       testCommit,
		PackRoot:        packRoot,
		SourceDateEpoch: &epoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != SchemaVersion || manifest.GitCommit != testCommit {
		t.Fatalf("unexpected identity: %#v", manifest)
	}
	if manifest.GoToolchain != runtime.Version() {
		t.Fatalf("unexpected Go toolchain: got %s want %s", manifest.GoToolchain, runtime.Version())
	}
	if manifest.GeneratedAt != "2023-11-14T22:13:20Z" || manifest.SourceDateEpoch == nil || *manifest.SourceDateEpoch != epoch {
		t.Fatalf("unexpected source date: %#v", manifest)
	}
	if len(manifest.Archives) != 2 || manifest.Archives[0].Path >= manifest.Archives[1].Path {
		t.Fatalf("unexpected archive inventory: %#v", manifest.Archives)
	}
	if len(manifest.SBOMs) != 2 || manifest.SBOMs[0].Path >= manifest.SBOMs[1].Path {
		t.Fatalf("unexpected SBOM inventory: %#v", manifest.SBOMs)
	}
	for index, archive := range manifest.Archives {
		if manifest.SBOMs[index].Subject != archive.Path || manifest.SBOMs[index].Path != sbomPathForArchive(archive.Path) {
			t.Fatalf("SBOM is not bound to archive: archive=%#v sbom=%#v", archive, manifest.SBOMs[index])
		}
	}
	if !strings.HasPrefix(manifest.ScenarioPack.Digest, "sha256:") || !strings.HasPrefix(manifest.ChecksumFile.Digest, "sha256:") {
		t.Fatalf("missing canonical digests: %#v", manifest)
	}

	manifestPath := DefaultManifestPath(manifest.Version)
	if err := Write(releaseDir, manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(releaseDir, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Version != manifest.Version || verified.ScenarioPack != manifest.ScenarioPack {
		t.Fatalf("verified manifest changed: %#v", verified)
	}

	contentBefore, err := os.ReadFile(filepath.Join(releaseDir, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(releaseDir, manifestPath, manifest); !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("second immutable manifest write error = %v, want ErrOutputExists", err)
	}
	contentAfter, err := os.ReadFile(filepath.Join(releaseDir, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(contentAfter) != string(contentBefore) {
		t.Fatal("existing release manifest changed after refused write")
	}
}

func TestWriteRefusesExistingSymlinkAndDirectoryTargets(t *testing.T) {
	releaseDir, packRoot := releaseFixture(t, "1.2.3")
	manifest, err := Create(CreateOptions{
		ReleaseDir:  releaseDir,
		Version:     "1.2.3",
		GitCommit:   testCommit,
		PackRoot:    packRoot,
		GeneratedAt: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("symlink", func(t *testing.T) {
		sentinel := filepath.Join(releaseDir, "symlink-sentinel.txt")
		if err := os.WriteFile(sentinel, []byte("unchanged\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		targetName := "linked-manifest.json"
		target := filepath.Join(releaseDir, targetName)
		if err := os.Symlink(sentinel, target); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := Write(releaseDir, targetName, manifest); !errors.Is(err, pathguard.ErrOutputExists) {
			t.Fatalf("Write() error = %v, want ErrOutputExists", err)
		}
		if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink target was replaced: info=%v err=%v", info, err)
		}
		if content, err := os.ReadFile(sentinel); err != nil || string(content) != "unchanged\n" {
			t.Fatalf("symlink sentinel changed: content=%q err=%v", content, err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		targetName := "directory-manifest.json"
		target := filepath.Join(releaseDir, targetName)
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(target, "sentinel.txt")
		if err := os.WriteFile(sentinel, []byte("unchanged\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Write(releaseDir, targetName, manifest); !errors.Is(err, pathguard.ErrOutputExists) {
			t.Fatalf("Write() error = %v, want ErrOutputExists", err)
		}
		if info, err := os.Lstat(target); err != nil || !info.IsDir() {
			t.Fatalf("directory target was replaced: info=%v err=%v", info, err)
		}
		if content, err := os.ReadFile(sentinel); err != nil || string(content) != "unchanged\n" {
			t.Fatalf("directory sentinel changed: content=%q err=%v", content, err)
		}
	})
}

func TestVerifyRejectsTamperedArchive(t *testing.T) {
	releaseDir, packRoot := releaseFixture(t, "1.2.3")
	manifest, err := Create(CreateOptions{
		ReleaseDir: releaseDir,
		Version:    "1.2.3",
		GitCommit:  testCommit,
		PackRoot:   packRoot,
		GeneratedAt: time.Date(2026, 8, 12, 9, 0, 0, 0,
			time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(releaseDir, "manifest.json", manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, manifest.Archives[0].Path), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(releaseDir, "manifest.json"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected tamper rejection, got %v", err)
	}
}

func TestVerifyRejectsTamperedMissingExtraAndSymlinkSBOM(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*testing.T, string, Manifest)
		wantError string
	}{
		{
			name: "tampered",
			mutate: func(t *testing.T, releaseDir string, manifest Manifest) {
				path := filepath.Join(releaseDir, manifest.SBOMs[0].Path)
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(content, ' '), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "SBOM digest mismatch",
		},
		{
			name: "missing",
			mutate: func(t *testing.T, releaseDir string, manifest Manifest) {
				if err := os.Remove(filepath.Join(releaseDir, manifest.SBOMs[0].Path)); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "SBOM coverage mismatch",
		},
		{
			name: "extra",
			mutate: func(t *testing.T, releaseDir string, manifest Manifest) {
				content, err := os.ReadFile(filepath.Join(releaseDir, manifest.SBOMs[0].Path))
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, releaseDir, "pgworkbench-1.2.3-windows-amd64.spdx.json", content)
			},
			wantError: "SBOM coverage mismatch",
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, releaseDir string, manifest Manifest) {
				path := filepath.Join(releaseDir, manifest.SBOMs[0].Path)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(manifest.SBOMs[1].Path, path); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "not a regular file",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			releaseDir, packRoot := releaseFixture(t, "1.2.3")
			manifest, err := Create(CreateOptions{
				ReleaseDir: releaseDir,
				Version:    "1.2.3",
				GitCommit:  testCommit,
				PackRoot:   packRoot,
				GeneratedAt: time.Date(2026, 8, 12, 9, 0, 0, 0,
					time.UTC),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := Write(releaseDir, "manifest.json", manifest); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, releaseDir, manifest)
			if _, err := Verify(releaseDir, "manifest.json"); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected %q rejection, got %v", test.wantError, err)
			}
		})
	}
}

func TestCreateRequiresExactSBOMCoverageAndIdentity(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		if err := os.Remove(filepath.Join(releaseDir, "pgworkbench-1.2.3-darwin-arm64.spdx.json")); err != nil {
			t.Fatal(err)
		}
		_, err := Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "SBOM coverage mismatch") {
			t.Fatalf("expected missing SBOM rejection, got %v", err)
		}
	})

	t.Run("extra", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		content, err := os.ReadFile(filepath.Join(releaseDir, "pgworkbench-1.2.3-darwin-arm64.spdx.json"))
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, releaseDir, "pgworkbench-1.2.3-windows-amd64.spdx.json", content)
		_, err = Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "SBOM coverage mismatch") {
			t.Fatalf("expected extra SBOM rejection, got %v", err)
		}
	})

	t.Run("version", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		path := filepath.Join(releaseDir, "pgworkbench-1.2.3-darwin-arm64.spdx.json")
		rewriteSPDX(t, path, func(document *releasesbom.Document) {
			document.Packages[0].VersionInfo = "1.2.4"
		})
		_, err := Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "package version mismatch") {
			t.Fatalf("expected version identity rejection, got %v", err)
		}
	})

	t.Run("commit", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		_, err := Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: strings.Repeat("b", 40), PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "namespace mismatch") {
			t.Fatalf("expected commit identity rejection, got %v", err)
		}
	})

	t.Run("subject", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		path := filepath.Join(releaseDir, "pgworkbench-1.2.3-darwin-arm64.spdx.json")
		rewriteSPDX(t, path, func(document *releasesbom.Document) {
			document.Packages[0].PackageFileName = "pgworkbench-1.2.3-linux-amd64.tar.gz"
		})
		_, err := Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "package subject mismatch") {
			t.Fatalf("expected subject identity rejection, got %v", err)
		}
	})

	t.Run("file inventory", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		path := filepath.Join(releaseDir, "pgworkbench-1.2.3-darwin-arm64.spdx.json")
		rewriteSPDX(t, path, func(document *releasesbom.Document) {
			for index := range document.Files[0].Checksums {
				if document.Files[0].Checksums[index].Algorithm == "SHA256" {
					document.Files[0].Checksums[index].Value = strings.Repeat("0", 64)
				}
			}
		})
		_, err := Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "package inventory") || !strings.Contains(err.Error(), "SHA256 mismatch") {
			t.Fatalf("expected semantic inventory rejection, got %v", err)
		}
	})
}

func TestCreateAcceptsFullSHA256GitObjectID(t *testing.T) {
	commit := strings.Repeat("a", 64)
	releaseDir, packRoot := releaseFixtureForCommit(t, "1.2.3", commit)
	manifest, err := Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: commit, PackRoot: packRoot})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.GitCommit != commit {
		t.Fatalf("unexpected commit: %s", manifest.GitCommit)
	}
}

func TestCreateRequiresCompleteChecksumCoverage(t *testing.T) {
	releaseDir, packRoot := releaseFixture(t, "1.2.3")
	checksumPath := filepath.Join(releaseDir, DefaultChecksumPath("1.2.3"))
	content, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatal(err)
	}
	firstLine := strings.Split(string(content), "\n")[0] + "\n"
	if err := os.WriteFile(checksumPath, []byte(firstLine), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
	if err == nil || !strings.Contains(err.Error(), "coverage mismatch") {
		t.Fatalf("expected coverage rejection, got %v", err)
	}
}

func TestCreateRejectsChecksumMismatchAndNoncanonicalText(t *testing.T) {
	t.Run("mismatch", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		checksumPath := filepath.Join(releaseDir, DefaultChecksumPath("1.2.3"))
		content, err := os.ReadFile(checksumPath)
		if err != nil {
			t.Fatal(err)
		}
		content[0] = 'f'
		if err := os.WriteFile(checksumPath, content, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("expected checksum rejection, got %v", err)
		}
	})

	t.Run("format", func(t *testing.T) {
		releaseDir, packRoot := releaseFixture(t, "1.2.3")
		checksumPath := filepath.Join(releaseDir, DefaultChecksumPath("1.2.3"))
		content, err := os.ReadFile(checksumPath)
		if err != nil {
			t.Fatal(err)
		}
		content = []byte(strings.ReplaceAll(string(content), "  ", " "))
		if err := os.WriteFile(checksumPath, content, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
		if err == nil || !strings.Contains(err.Error(), "not canonical") {
			t.Fatalf("expected canonical-format rejection, got %v", err)
		}
	})
}

func TestCreateRejectsArchiveWithDifferentScenarioPack(t *testing.T) {
	releaseDir, packRoot := releaseFixture(t, "1.2.3")
	otherPack := t.TempDir()
	otherManifest := `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "different-pack",
  "version": "1.2.3",
  "engine_constraint": ">=0.2.0",
  "assets": ["profiles"]
}`
	writeFixture(t, otherPack, "pgworkbench-pack.json", []byte(otherManifest))
	writeFixture(t, otherPack, "profiles/smoke.sql", []byte("select 1;\n"))
	writeFixture(t, otherPack, "pgworkbench", []byte("binary\n"))
	archiveName := "pgworkbench-1.2.3-linux-amd64.tar.gz"
	if err := os.Remove(filepath.Join(releaseDir, archiveName)); err != nil {
		t.Fatal(err)
	}
	if _, err := releasearchive.Create(
		otherPack,
		filepath.Join(releaseDir, archiveName),
		strings.TrimSuffix(archiveName, ".tar.gz"),
		time.Unix(1_700_000_000, 0),
	); err != nil {
		t.Fatal(err)
	}
	writeReleaseChecksums(t, releaseDir, "1.2.3")
	_, err := Create(CreateOptions{ReleaseDir: releaseDir, Version: "1.2.3", GitCommit: testCommit, PackRoot: packRoot})
	if err == nil || !strings.Contains(err.Error(), "scenario pack identity mismatch") {
		t.Fatalf("expected embedded pack rejection, got %v", err)
	}
}

func TestValidateRejectsNoncanonicalIdentityAndOrdering(t *testing.T) {
	base := Manifest{
		SchemaVersion: SchemaVersion,
		Version:       "1.2.3",
		GitCommit:     testCommit,
		GoToolchain:   runtime.Version(),
		ScenarioPack: ScenarioPack{
			ID:      "test-pack",
			Version: "1.2.3-rc.1",
			Digest:  "sha256:" + strings.Repeat("a", 64),
		},
		GeneratedAt: "2026-08-12T09:00:00Z",
		Archives: []Archive{
			{Path: "pgworkbench-1.2.3-linux-amd64.tar.gz", SHA256: strings.Repeat("b", 64), Size: 1},
		},
		SBOMs: []SBOM{
			{
				Path:    "pgworkbench-1.2.3-linux-amd64.spdx.json",
				SHA256:  strings.Repeat("d", 64),
				Size:    1,
				Subject: "pgworkbench-1.2.3-linux-amd64.tar.gz",
			},
		},
		ChecksumFile: ChecksumFile{Path: DefaultChecksumPath("1.2.3"), Digest: "sha256:" + strings.Repeat("c", 64)},
	}
	if err := Validate(base); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "short commit", mutate: func(value *Manifest) { value.GitCommit = "abc123" }},
		{name: "41-character commit", mutate: func(value *Manifest) { value.GitCommit = strings.Repeat("a", 41) }},
		{name: "all-zero SHA-1 commit", mutate: func(value *Manifest) { value.GitCommit = strings.Repeat("0", 40) }},
		{name: "all-zero SHA-256 commit", mutate: func(value *Manifest) { value.GitCommit = strings.Repeat("0", 64) }},
		{name: "uppercase commit", mutate: func(value *Manifest) { value.GitCommit = strings.ToUpper(testCommit) }},
		{name: "unpinned Go toolchain", mutate: func(value *Manifest) { value.GoToolchain = "go1.26" }},
		{name: "noncanonical version", mutate: func(value *Manifest) { value.Version = "01.2.3" }},
		{name: "unsafe archive", mutate: func(value *Manifest) { value.Archives[0].Path = "../release.tar.gz" }},
		{name: "wrong SBOM subject", mutate: func(value *Manifest) { value.SBOMs[0].Subject = "pgworkbench-1.2.3-darwin-arm64.tar.gz" }},
		{name: "unprefixed pack digest", mutate: func(value *Manifest) { value.ScenarioPack.Digest = strings.Repeat("a", 64) }},
		{name: "timestamp offset", mutate: func(value *Manifest) { value.GeneratedAt = "2026-08-12T14:00:00+05:00" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Archives = append([]Archive(nil), base.Archives...)
			candidate.SBOMs = append([]SBOM(nil), base.SBOMs...)
			test.mutate(&candidate)
			if err := Validate(candidate); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestReadRejectsUnknownFieldsAndSymlinkManifest(t *testing.T) {
	releaseDir, _ := releaseFixture(t, "1.2.3")
	unknown := `{
  "schema_version": "pgworkbench.release-manifest/v1",
  "unknown": true
}`
	if err := os.WriteFile(filepath.Join(releaseDir, "unknown.json"), []byte(unknown), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(filepath.Join(releaseDir, "unknown.json")); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field rejection, got %v", err)
	}
	if err := os.Symlink("unknown.json", filepath.Join(releaseDir, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(releaseDir, "linked.json"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestValidSemVerSupportsCanonicalPrereleaseHyphens(t *testing.T) {
	for _, value := range []string{"0.2.0", "1.2.3-rc.1", "1.2.3-rc-1+build.7"} {
		if !validSemVer(value) {
			t.Fatalf("valid SemVer rejected: %s", value)
		}
	}
	for _, value := range []string{"v1.2.3", "1.2", "01.2.3", "1.2.3-01", "1.2.3+"} {
		if validSemVer(value) {
			t.Fatalf("invalid SemVer accepted: %s", value)
		}
	}
}

func releaseFixture(t *testing.T, version string) (string, string) {
	return releaseFixtureForCommit(t, version, testCommit)
}

func releaseFixtureForCommit(t *testing.T, version string, commit string) (string, string) {
	t.Helper()
	packRoot := t.TempDir()
	packManifest := `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "fixture-pack",
  "version": "` + version + `",
  "engine_constraint": ">=0.2.0",
  "assets": ["profiles"]
}`
	writeFixture(t, packRoot, "pgworkbench-pack.json", []byte(packManifest))
	writeFixture(t, packRoot, "profiles/smoke.sql", []byte("select 1;\n"))
	addReleaseSupplyChainFixture(t, packRoot)

	releaseDir := t.TempDir()
	archivePaths := []string{
		"pgworkbench-" + version + "-darwin-arm64.tar.gz",
		"pgworkbench-" + version + "-linux-amd64.tar.gz",
	}
	for _, path := range archivePaths {
		if _, err := releasearchive.Create(
			packRoot,
			filepath.Join(releaseDir, path),
			strings.TrimSuffix(path, ".tar.gz"),
			time.Unix(1_700_000_000, 0),
		); err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(path, ".tar.gz")
		if _, err := releasesbom.Create(releasesbom.Options{
			Root:    packRoot,
			Output:  filepath.Join(releaseDir, name+".spdx.json"),
			Name:    name,
			Version: version,
			Commit:  commit,
			Created: time.Unix(1_700_000_000, 0),
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeReleaseChecksums(t, releaseDir, version)
	return releaseDir, packRoot
}

func addReleaseSupplyChainFixture(t *testing.T, root string) {
	t.Helper()
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, relative := range []string{
		"go.mod",
		"go.sum",
		"third_party/go-modules.json",
		"third_party/licenses/github.com/dlclark/regexp2/v1.11.0/ATTRIB",
		"third_party/licenses/github.com/dlclark/regexp2/v1.11.0/LICENSE",
		"third_party/licenses/github.com/santhosh-tekuri/jsonschema/v6/v6.0.2/LICENSE",
		"third_party/licenses/golang.org/x/text/v0.14.0/LICENSE",
		"third_party/licenses/golang.org/x/text/v0.14.0/PATENTS",
	} {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, root, relative, content)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.OpenFile(filepath.Join(root, "pgworkbench"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeReleaseChecksums(t *testing.T, releaseDir string, version string) {
	t.Helper()
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		t.Fatal(err)
	}
	var checksum strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "pgworkbench-"+version+"-") || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(releaseDir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		checksum.WriteString(hex.EncodeToString(sum[:]))
		checksum.WriteString("  ")
		checksum.WriteString(name)
		checksum.WriteByte('\n')
	}
	writeFixture(t, releaseDir, DefaultChecksumPath(version), []byte(checksum.String()))
}

func writeFixture(t *testing.T, root string, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func rewriteSPDX(t *testing.T, path string, mutate func(*releasesbom.Document)) {
	t.Helper()
	document, err := releasesbom.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&document)
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
