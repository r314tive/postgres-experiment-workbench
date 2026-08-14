package releaseassets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasemanifest"
	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func TestFingerprintMatchesJQCompactSortedObjectContract(t *testing.T) {
	stringID, err := NewStringAssetID("RA_<>&é")
	if err != nil {
		t.Fatal(err)
	}
	integerID, err := NewIntegerAssetID(42)
	if err != nil {
		t.Fatal(err)
	}
	assets := []Asset{
		{ID: integerID, Name: "z.tar.gz", Size: 19, Digest: "sha256:" + strings.Repeat("b", 64)},
		{ID: stringID, Name: "a.spdx.json", Size: 7, Digest: "sha256:" + strings.Repeat("a", 64)},
	}

	fingerprint, err := ComputeFingerprint(assets)
	if err != nil {
		t.Fatal(err)
	}
	// Generated with jq 1.7.1 using the exact documented -cS expression and
	// hashing command-substitution output without its trailing newline.
	const want = "e32b265200d80fcb8be8b532cc5f137a5a8fa8b0c02c0b611d5d3d3262215145"
	if fingerprint != want {
		t.Fatalf("fingerprint = %s, want jq vector %s", fingerprint, want)
	}

	numericStringID, err := NewStringAssetID("42")
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]Asset(nil), assets...)
	changed[0].ID = numericStringID
	stringFingerprint, err := ComputeFingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if stringFingerprint == fingerprint {
		t.Fatal("integer and string provider IDs produced the same fingerprint")
	}
}

func TestLoadFileStrictlyPreservesAssetIDJSONType(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	content := marshalInventory(t, fixture.inventory)
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Assets[0].ID.IsString() || loaded.Assets[0].ID.Value() != fixture.inventory.Assets[0].ID.Value() {
		t.Fatalf("first asset id type/value changed: %#v", loaded.Assets[0].ID)
	}
	if !loaded.Assets[1].ID.IsInteger() || loaded.Assets[1].ID.Value() != fixture.inventory.Assets[1].ID.Value() {
		t.Fatalf("second asset id type/value changed: %#v", loaded.Assets[1].ID)
	}

	invalid := []struct {
		name    string
		content string
	}{
		{name: "duplicate property", content: `{"schema_version":"x","schema_version":"y"}`},
		{name: "unknown property", content: strings.Replace(string(content), `"assets":`, `"unknown":true,"assets":`, 1)},
		{name: "explicit null", content: strings.Replace(string(content), `"release_state": "draft"`, `"release_state": null`, 1)},
		{name: "trailing JSON", content: string(content) + `{}`},
		{name: "noncanonical integer", content: strings.Replace(string(content), `"id": 2`, `"id": 2.0`, 1)},
		{name: "zero integer", content: strings.Replace(string(content), `"id": 2`, `"id": 0`, 1)},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.content)); err == nil {
				t.Fatalf("Parse() accepted %s", test.name)
			}
		})
	}
}

func TestVerifyRejectsNoncanonicalAndDuplicateInventory(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	tests := []struct {
		name   string
		mutate func(*Inventory)
		issue  string
	}{
		{
			name: "wrong fingerprint",
			mutate: func(inventory *Inventory) {
				inventory.AssetFingerprint = strings.Repeat("0", 64)
			},
			issue: "independently recomputed",
		},
		{
			name: "duplicate id",
			mutate: func(inventory *Inventory) {
				inventory.Assets[1].ID = inventory.Assets[0].ID
				refreshFingerprint(t, inventory)
			},
			issue: "duplicates an earlier asset id",
		},
		{
			name: "duplicate name",
			mutate: func(inventory *Inventory) {
				inventory.Assets[1].Name = inventory.Assets[0].Name
				refreshFingerprint(t, inventory)
			},
			issue: "duplicates an earlier asset name",
		},
		{
			name: "unsorted assets",
			mutate: func(inventory *Inventory) {
				inventory.Assets[0], inventory.Assets[1] = inventory.Assets[1], inventory.Assets[0]
			},
			issue: "strictly sorted by name",
		},
		{
			name: "noncanonical digest",
			mutate: func(inventory *Inventory) {
				inventory.Assets[0].Digest = "sha256:" + strings.Repeat("A", 64)
				refreshFingerprint(t, inventory)
			},
			issue: "lowercase sha256 digest",
		},
		{
			name: "noncanonical timestamp",
			mutate: func(inventory *Inventory) {
				inventory.CapturedAt = "2026-08-14T17:34:56+05:00"
			},
			issue: "canonical UTC RFC3339Nano",
		},
		{
			name: "incomplete release set",
			mutate: func(inventory *Inventory) {
				inventory.Assets = inventory.Assets[:len(inventory.Assets)-1]
				refreshFingerprint(t, inventory)
			},
			issue: "want exactly 16 release assets",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := cloneInventory(fixture.inventory)
			test.mutate(&inventory)
			verification := Verify(inventory)
			if verification.Valid || !containsIssue(verification.Issues, test.issue) {
				t.Fatalf("verification = %+v, want issue containing %q", verification, test.issue)
			}
			if !sort.StringsAreSorted(verification.Issues) {
				t.Fatalf("issues are not sorted: %v", verification.Issues)
			}
		})
	}
}

func TestVerifyDirectoryAcceptsExactClosedReleaseRoot(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	verification, err := VerifyDirectory(fixture.root, fixture.inventory, fixture.manifest, fixture.manifestName)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid {
		t.Fatalf("exact release root issues: %v", verification.Issues)
	}
	if verification.ComputedFingerprint != fixture.inventory.AssetFingerprint {
		t.Fatalf("computed fingerprint = %s, want %s", verification.ComputedFingerprint, fixture.inventory.AssetFingerprint)
	}
}

func TestVerifyDirectoryRejectsIdentityClosureAndByteDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *releaseAssetFixture)
		issue  string
		err    string
	}{
		{
			name: "wrong tag",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				fixture.inventory.Tag = "v9.9.9"
			},
			issue: "want \"v1.2.3\" from release manifest",
		},
		{
			name: "wrong commit",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				fixture.inventory.GitCommit = strings.Repeat("a", 40)
			},
			issue: "want release manifest commit",
		},
		{
			name: "missing file",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				if err := os.Remove(filepath.Join(fixture.root, fixture.inventory.Assets[0].Name)); err != nil {
					t.Fatal(err)
				}
			},
			issue: "fixed closed release set",
		},
		{
			name: "extra file",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				if err := os.WriteFile(filepath.Join(fixture.root, "extra.txt"), []byte("extra\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			issue: "fixed closed release set",
		},
		{
			name: "incomplete fixed inventory",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				fixture.inventory.Assets = fixture.inventory.Assets[:len(fixture.inventory.Assets)-1]
				refreshFingerprint(t, &fixture.inventory)
			},
			issue: "fixed closed release set",
		},
		{
			name: "changed bytes",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				asset := fixture.inventory.Assets[0]
				if err := os.WriteFile(filepath.Join(fixture.root, asset.Name), []byte("changed bytes\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			issue: "digest =",
		},
		{
			name: "inventory-bound archive differs from manifest",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				name := fixture.manifest.Archives[0].Path
				if err := os.WriteFile(filepath.Join(fixture.root, name), []byte("different provider archive\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				updateInventoryAssetFromFile(t, fixture, name)
			},
			issue: "does not match release manifest digest",
		},
		{
			name: "inventory-bound SBOM differs from manifest",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				name := fixture.manifest.SBOMs[0].Path
				if err := os.WriteFile(filepath.Join(fixture.root, name), []byte("different provider SBOM\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				updateInventoryAssetFromFile(t, fixture, name)
			},
			issue: "does not match release manifest digest",
		},
		{
			name: "inventory-bound checksum differs from manifest",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				name := fixture.manifest.ChecksumFile.Path
				if err := os.WriteFile(filepath.Join(fixture.root, name), []byte("different provider checksums\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				updateInventoryAssetFromFile(t, fixture, name)
			},
			issue: "does not match release manifest checksum digest",
		},
		{
			name: "symlink asset",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				asset := fixture.inventory.Assets[0]
				path := filepath.Join(fixture.root, asset.Name)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(fixture.inventory.Assets[1].Name, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
			issue: "not a regular non-symlink file",
		},
		{
			name: "manifest from another candidate",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				fixture.manifest.GoToolchain = "go1.24.1"
			},
			issue: "local release manifest does not equal the verified manifest value",
		},
		{
			name: "manifest omits fixed platform",
			mutate: func(t *testing.T, fixture *releaseAssetFixture) {
				fixture.manifest.Archives = fixture.manifest.Archives[:len(fixture.manifest.Archives)-1]
				fixture.manifest.SBOMs = fixture.manifest.SBOMs[:len(fixture.manifest.SBOMs)-1]
			},
			issue: "want fixed platform archives",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReleaseAssetFixture(t)
			test.mutate(t, &fixture)
			verification, err := VerifyDirectory(fixture.root, fixture.inventory, fixture.manifest, fixture.manifestName)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("error = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if verification.Valid || !containsIssue(verification.Issues, test.issue) {
				t.Fatalf("verification = %+v, want issue containing %q", verification, test.issue)
			}
		})
	}
}

func TestVerifyDirectoryRejectsSemanticallyInvalidMetadataChecksums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]string) []string
		issue  string
	}{
		{
			name: "missing row",
			mutate: func(rows []string) []string {
				return rows[:len(rows)-1]
			},
			issue: "row count",
		},
		{
			name: "extra archive row",
			mutate: func(rows []string) []string {
				return append(rows, strings.Repeat("a", 64)+"  ./pgworkbench-1.2.3-linux-amd64.tar.gz")
			},
			issue: "row count",
		},
		{
			name: "duplicate row",
			mutate: func(rows []string) []string {
				return append(rows, rows[0])
			},
			issue: "duplicates asset",
		},
		{
			name: "wrong digest",
			mutate: func(rows []string) []string {
				rows[0] = strings.Repeat("0", 64) + rows[0][64:]
				return rows
			},
			issue: "digest does not match",
		},
		{
			name: "noncanonical separator",
			mutate: func(rows []string) []string {
				rows[0] = rows[0][:64] + " *./" + rows[0][68:]
				return rows
			},
			issue: "not canonical sha256sum output",
		},
		{
			name: "reordered rows",
			mutate: func(rows []string) []string {
				rows[0], rows[1] = rows[1], rows[0]
				return rows
			},
			issue: "workflow order",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReleaseAssetFixture(t)
			path := filepath.Join(fixture.root, metadataChecksumName(fixture.manifest.Version))
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			rows := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
			rows = test.mutate(append([]string(nil), rows...))
			changed := []byte(strings.Join(rows, "\n") + "\n")
			if err := os.WriteFile(path, changed, 0o644); err != nil {
				t.Fatal(err)
			}
			updateInventoryAssetFromFile(t, &fixture, metadataChecksumName(fixture.manifest.Version))

			verification, err := VerifyDirectory(fixture.root, fixture.inventory, fixture.manifest, fixture.manifestName)
			if err != nil {
				t.Fatal(err)
			}
			if verification.Valid || !containsIssue(verification.Issues, test.issue) {
				t.Fatalf("verification = %+v, want issue containing %q", verification, test.issue)
			}
			if containsIssue(verification.Issues, "want inventory digest") {
				t.Fatalf("test did not preserve outer inventory integrity: %v", verification.Issues)
			}
		})
	}
}

func TestInventoryConformsToRepositorySchema(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("release-asset-inventory.schema.json", marshalInventory(t, fixture.inventory)); err != nil {
		t.Fatalf("valid inventory schema error: %v", err)
	}
}

func TestInventorySchemaRejectsEverySemanticRepresentationMismatch(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	base := string(marshalInventory(t, fixture.inventory))
	firstSize := fmt.Sprintf(`"size": %d`, fixture.inventory.Assets[0].Size)
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "noncanonical trailing fractional zeros",
			content: strings.Replace(base, `"captured_at": "2026-08-14T12:35:00Z"`, `"captured_at": "2026-08-14T12:35:00.000Z"`, 1),
		},
		{
			name:    "unsupported RFC3339 leap second",
			content: strings.Replace(base, `"captured_at": "2026-08-14T12:35:00Z"`, `"captured_at": "1990-12-31T23:59:60Z"`, 1),
		},
		{
			name:    "escaped control in string asset id",
			content: strings.Replace(base, `"id": "RA_fixture_1"`, `"id": "RA_fixture_1\u0001"`, 1),
		},
		{
			name:    "size exceeds JSON safe integer",
			content: strings.Replace(base, firstSize, `"size": 9007199254740992`, 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.content == base {
				t.Fatal("schema parity test vector was not injected")
			}
			content := []byte(test.content)
			if err := registry.ValidateJSON("release-asset-inventory.schema.json", content); err == nil {
				t.Fatal("repository schema accepted a representation rejected by the semantic reader")
			}
			parsed, parseErr := Parse(content)
			if parseErr == nil && Verify(parsed).Valid {
				t.Fatal("semantic reader accepted the rejected representation")
			}
		})
	}

	canonical := cloneInventory(fixture.inventory)
	canonical.CapturedAt = "2026-08-14T12:35:00.000000001Z"
	if verification := Verify(canonical); !verification.Valid {
		t.Fatalf("canonical nanosecond timestamp issues: %v", verification.Issues)
	}
	if err := registry.ValidateJSON("release-asset-inventory.schema.json", marshalInventory(t, canonical)); err != nil {
		t.Fatalf("schema rejected canonical nanosecond timestamp: %v", err)
	}
}

func TestCreateVerifiedSnapshotPinsInventoryBytes(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	snapshot, err := CreateVerifiedSnapshot(fixture.root, fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	snapshotRoot := snapshot.Root()
	asset := fixture.inventory.Assets[0]
	if err := os.WriteFile(filepath.Join(fixture.root, asset.Name), []byte("source changed after snapshot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := evidence.DigestFile(filepath.Join(snapshotRoot, asset.Name))
	if err != nil {
		t.Fatal(err)
	}
	if digest != asset.Digest {
		t.Fatalf("snapshot digest = %s, want inventory digest %s", digest, asset.Digest)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(snapshotRoot); !os.IsNotExist(err) {
		t.Fatalf("snapshot close left private directory: %v", err)
	}
}

func TestCreateVerifiedSnapshotRejectsTempBaseInsideImmutableSource(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	t.Setenv("TMPDIR", fixture.root)
	before, err := os.ReadDir(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateVerifiedSnapshot(fixture.root, fixture.inventory); err == nil || !strings.Contains(err.Error(), "outside release assets") {
		t.Fatalf("snapshot accepted temp base inside release root: %v", err)
	}
	after, err := os.ReadDir(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("rejected temp base mutated immutable root: before=%d after=%d", len(before), len(after))
	}
}

type releaseAssetFixture struct {
	root         string
	inventory    Inventory
	manifest     releasemanifest.Manifest
	manifestName string
}

func newReleaseAssetFixture(t *testing.T) releaseAssetFixture {
	t.Helper()
	root := t.TempDir()
	version := "1.2.3"
	manifestName := releasemanifest.DefaultManifestPath(version)

	archiveNames := []string{
		"pgworkbench-1.2.3-darwin-amd64.tar.gz",
		"pgworkbench-1.2.3-darwin-arm64.tar.gz",
		"pgworkbench-1.2.3-linux-amd64.tar.gz",
		"pgworkbench-1.2.3-linux-arm64.tar.gz",
	}
	sbomNames := []string{
		"pgworkbench-1.2.3-darwin-amd64.spdx.json",
		"pgworkbench-1.2.3-darwin-arm64.spdx.json",
		"pgworkbench-1.2.3-linux-amd64.spdx.json",
		"pgworkbench-1.2.3-linux-arm64.spdx.json",
	}
	archives := make([]releasemanifest.Archive, 0, len(archiveNames))
	sboms := make([]releasemanifest.SBOM, 0, len(sbomNames))
	for index, name := range archiveNames {
		content := []byte("archive " + name + "\n")
		writeTestFile(t, root, name, content)
		archives = append(archives, releasemanifest.Archive{
			Path: name, SHA256: strings.TrimPrefix(evidence.DigestBytes(content), evidence.DigestPrefix), Size: int64(len(content)),
		})
		sbomContent := []byte("sbom " + sbomNames[index] + "\n")
		writeTestFile(t, root, sbomNames[index], sbomContent)
		sboms = append(sboms, releasemanifest.SBOM{
			Path: sbomNames[index], SHA256: strings.TrimPrefix(evidence.DigestBytes(sbomContent), evidence.DigestPrefix), Size: int64(len(sbomContent)), Subject: name,
		})
		writeTestFile(t, root, strings.TrimSuffix(sbomNames[index], ".spdx.json")+"-sbom.sigstore.json", []byte("attestation "+name+"\n"))
	}
	checksumName := releasemanifest.DefaultChecksumPath(version)
	checksumContent := []byte("archive checksums\n")
	writeTestFile(t, root, checksumName, checksumContent)
	writeTestFile(t, root, "pgworkbench-1.2.3-provenance.sigstore.json", []byte("provenance\n"))

	manifest := releasemanifest.Manifest{
		SchemaVersion: releasemanifest.SchemaVersion,
		Version:       version,
		GitCommit:     testCommit,
		GoToolchain:   "go1.25.0",
		ScenarioPack: releasemanifest.ScenarioPack{
			ID: "builtin", Version: version, Digest: "sha256:" + strings.Repeat("c", 64),
		},
		GeneratedAt: "2026-08-14T12:34:56Z",
		Archives:    archives,
		SBOMs:       sboms,
		ChecksumFile: releasemanifest.ChecksumFile{
			Path: checksumName, Digest: evidence.DigestBytes(checksumContent),
		},
	}
	manifestContent, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, manifestName, append(manifestContent, '\n'))
	writeTestFile(t, root, metadataChecksumName(version), metadataChecksumContent(t, root, manifest, manifestName))

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	assets := make([]Asset, 0, len(entries))
	for index, entry := range entries {
		path := filepath.Join(root, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var id AssetID
		if index == 0 {
			id, err = NewStringAssetID("RA_fixture_1")
		} else {
			id, err = NewIntegerAssetID(uint64(index + 1))
		}
		if err != nil {
			t.Fatal(err)
		}
		assets = append(assets, Asset{ID: id, Name: entry.Name(), Size: int64(len(content)), Digest: evidence.DigestBytes(content)})
	}
	sort.Slice(assets, func(left, right int) bool { return assets[left].Name < assets[right].Name })
	inventory := Inventory{
		SchemaVersion:        SchemaVersion,
		ArtifactType:         ArtifactType,
		ReleaseState:         ReleaseStateDraft,
		Tag:                  "v" + version,
		GitCommit:            testCommit,
		CapturedAt:           "2026-08-14T12:35:00Z",
		FingerprintAlgorithm: FingerprintAlgorithm,
		Assets:               assets,
	}
	refreshFingerprint(t, &inventory)
	return releaseAssetFixture{root: root, inventory: inventory, manifest: manifest, manifestName: manifestName}
}

func writeTestFile(t *testing.T, root, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func marshalInventory(t *testing.T, inventory Inventory) []byte {
	t.Helper()
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(content, '\n')
}

func refreshFingerprint(t *testing.T, inventory *Inventory) {
	t.Helper()
	fingerprint, err := ComputeFingerprint(inventory.Assets)
	if err != nil {
		t.Fatal(err)
	}
	inventory.AssetFingerprint = fingerprint
}

func metadataChecksumContent(t *testing.T, root string, manifest releasemanifest.Manifest, manifestName string) []byte {
	t.Helper()
	names, err := expectedMetadataChecksumNames(manifest, manifestName)
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	for _, name := range names {
		digest, err := evidence.DigestFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		content.WriteString(strings.TrimPrefix(digest, evidence.DigestPrefix))
		content.WriteString("  ./")
		content.WriteString(name)
		content.WriteByte('\n')
	}
	return []byte(content.String())
}

func updateInventoryAssetFromFile(t *testing.T, fixture *releaseAssetFixture, name string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixture.root, name))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for index := range fixture.inventory.Assets {
		if fixture.inventory.Assets[index].Name != name {
			continue
		}
		fixture.inventory.Assets[index].Size = int64(len(content))
		fixture.inventory.Assets[index].Digest = evidence.DigestBytes(content)
		found = true
		break
	}
	if !found {
		t.Fatalf("inventory has no asset %s", name)
	}
	refreshFingerprint(t, &fixture.inventory)
}

func cloneInventory(inventory Inventory) Inventory {
	clone := inventory
	clone.Assets = append([]Asset(nil), inventory.Assets...)
	return clone
}

func containsIssue(issues []string, substring string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, substring) {
			return true
		}
	}
	return false
}

func TestExpectedAssetNamesAreDerivedWithoutHiddenEntries(t *testing.T) {
	fixture := newReleaseAssetFixture(t)
	names, err := expectedAssetNames(fixture.manifest, fixture.manifestName)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(fixture.inventory.Assets))
	for _, asset := range fixture.inventory.Assets {
		got = append(got, asset.Name)
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("inventory names = %v, want derived names %v", got, names)
	}
}
