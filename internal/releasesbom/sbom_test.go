package releasesbom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

func TestCreateProducesDeterministicSPDX23(t *testing.T) {
	root := t.TempDir()
	writeSupplyChainFixture(t, root)
	writeFixture(t, filepath.Join(root, "docs", "README.md"), "docs", 0o644)
	options := Options{
		Root:    root,
		Output:  filepath.Join(t.TempDir(), "release.spdx.json"),
		Name:    "pgworkbench-0.2.0-linux-amd64",
		Version: "0.2.0",
		Commit:  strings.Repeat("a", 40),
		Created: time.Unix(1_700_000_000, 0),
	}
	first, err := Create(options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Files < 6 || !strings.HasPrefix(first.SHA256, "sha256:") {
		t.Fatalf("unexpected result: %#v", first)
	}
	if err := Validate(first.Output); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseSubject(
		first.Output,
		options.Version,
		options.Commit,
		options.Name+".tar.gz",
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePackageRoot(first.Output, root); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(first.Output)
	if err != nil {
		t.Fatal(err)
	}
	var document Document
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	if document.SPDXVersion != "SPDX-2.3" || len(document.Packages[0].HasFiles) != first.Files || len(document.Packages) != 2 {
		t.Fatalf("unexpected SPDX document: %#v", document)
	}
	dependency := document.Packages[1]
	if dependency.Name != "example.com/schema-gate" || dependency.VersionInfo != "v1.2.3" || dependency.LicenseDeclared != "MIT" || !validPURL(dependency.ExternalRefs, "pkg:golang/example.com/schema-gate@v1.2.3") {
		t.Fatalf("unexpected dependency package: %#v", dependency)
	}
	if !hasRelationship(document.Relationships, Relationship{ElementID: dependency.SPDXID, RelatedElement: document.Packages[0].SPDXID, RelationshipType: "TEST_DEPENDENCY_OF"}) {
		t.Fatalf("missing test dependency relationship: %#v", document.Relationships)
	}
	if hasRelationship(document.Relationships, Relationship{ElementID: document.Packages[0].SPDXID, RelatedElement: dependency.SPDXID, RelationshipType: "DEPENDS_ON"}) {
		t.Fatalf("test-only module was presented as a runtime dependency: %#v", document.Relationships)
	}
	secondOutput := filepath.Join(t.TempDir(), "release.spdx.json")
	options.Output = secondOutput
	second, err := Create(options)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("SPDX output is not deterministic: %s != %s", first.SHA256, second.SHA256)
	}
}

func TestValidatePackageRootRejectsInventoryAndVerificationCodeMismatch(t *testing.T) {
	makeFixture := func(t *testing.T) (string, string) {
		t.Helper()
		root := t.TempDir()
		writeSupplyChainFixture(t, root)
		writeFixture(t, filepath.Join(root, "docs", "README.md"), "docs", 0o644)
		result, err := Create(Options{
			Root:    root,
			Output:  filepath.Join(t.TempDir(), "release.spdx.json"),
			Name:    "pgworkbench-0.2.0-linux-amd64",
			Version: "0.2.0",
			Commit:  strings.Repeat("a", 40),
			Created: time.Unix(1_700_000_000, 0),
		})
		if err != nil {
			t.Fatal(err)
		}
		return root, result.Output
	}

	t.Run("file content", func(t *testing.T) {
		root, sbomPath := makeFixture(t)
		writeFixture(t, filepath.Join(root, "docs", "README.md"), "tampered", 0o644)
		if err := ValidatePackageRoot(sbomPath, root); err == nil || !strings.Contains(err.Error(), "SHA") {
			t.Fatalf("expected file checksum rejection, got %v", err)
		}
	})

	t.Run("file coverage", func(t *testing.T) {
		root, sbomPath := makeFixture(t)
		writeFixture(t, filepath.Join(root, "extra.txt"), "extra", 0o644)
		if err := ValidatePackageRoot(sbomPath, root); err == nil || !strings.Contains(err.Error(), "coverage mismatch") {
			t.Fatalf("expected file coverage rejection, got %v", err)
		}
	})

	t.Run("verification code", func(t *testing.T) {
		root, sbomPath := makeFixture(t)
		document, err := Read(sbomPath)
		if err != nil {
			t.Fatal(err)
		}
		document.Packages[0].PackageVerificationCode.Value = strings.Repeat("0", 40)
		content, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sbomPath, append(content, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePackageRoot(sbomPath, root); err == nil || !strings.Contains(err.Error(), "verification code mismatch") {
			t.Fatalf("expected verification-code rejection, got %v", err)
		}
	})

	t.Run("dependency scope", func(t *testing.T) {
		root, sbomPath := makeFixture(t)
		document, err := Read(sbomPath)
		if err != nil {
			t.Fatal(err)
		}
		last := len(document.Relationships) - 1
		document.Relationships[last] = Relationship{
			ElementID:        document.Packages[0].SPDXID,
			RelatedElement:   document.Packages[1].SPDXID,
			RelationshipType: "DEPENDS_ON",
		}
		writeDocument(t, sbomPath, document)
		if err := ValidatePackageRoot(sbomPath, root); err == nil || !strings.Contains(err.Error(), "relationship mismatch") {
			t.Fatalf("expected inventory-bound dependency-scope rejection, got %v", err)
		}
	})
}

func TestValidateReleaseSubjectRejectsIdentityMismatchAndSymlink(t *testing.T) {
	root := t.TempDir()
	writeSupplyChainFixture(t, root)
	options := Options{
		Root:    root,
		Output:  filepath.Join(t.TempDir(), "pgworkbench-0.2.0-linux-amd64.spdx.json"),
		Name:    "pgworkbench-0.2.0-linux-amd64",
		Version: "0.2.0",
		Commit:  strings.Repeat("a", 40),
		Created: time.Unix(1_700_000_000, 0),
	}
	result, err := Create(options)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		version string
		commit  string
		archive string
	}{
		{name: "version", version: "0.2.1", commit: options.Commit, archive: options.Name + ".tar.gz"},
		{name: "commit", version: options.Version, commit: strings.Repeat("b", 40), archive: options.Name + ".tar.gz"},
		{name: "archive", version: options.Version, commit: options.Commit, archive: "pgworkbench-0.2.0-darwin-arm64.tar.gz"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateReleaseSubject(result.Output, test.version, test.commit, test.archive); err == nil {
				t.Fatal("expected identity mismatch")
			}
		})
	}

	link := filepath.Join(t.TempDir(), "linked.spdx.json")
	if err := os.Symlink(result.Output, link); err != nil {
		t.Fatal(err)
	}
	if err := Validate(link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestCreateAcceptsOnlyNonzeroFullGitObjectIDs(t *testing.T) {
	for _, length := range []int{40, 64} {
		t.Run(strings.Repeat("a", length), func(t *testing.T) {
			root := t.TempDir()
			writeSupplyChainFixture(t, root)
			_, err := Create(Options{
				Root:    root,
				Output:  filepath.Join(t.TempDir(), "release.spdx.json"),
				Name:    "pgworkbench-0.2.0-linux-amd64",
				Version: "0.2.0",
				Commit:  strings.Repeat("a", length),
				Created: time.Unix(1_700_000_000, 0),
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, commit := range []string{strings.Repeat("a", 41), strings.Repeat("0", 40), strings.Repeat("0", 64)} {
		root := t.TempDir()
		writeFixture(t, filepath.Join(root, "pgworkbench"), "binary", 0o755)
		_, err := Create(Options{
			Root:    root,
			Output:  filepath.Join(t.TempDir(), "release.spdx.json"),
			Name:    "pgworkbench-0.2.0-linux-amd64",
			Version: "0.2.0",
			Commit:  commit,
			Created: time.Unix(1_700_000_000, 0),
		})
		if err == nil {
			t.Fatalf("expected commit rejection for length %d", len(commit))
		}
	}
}

func TestCreateRejectsOutputInsidePackageDirectlyOrThroughAlias(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "pgworkbench"), "binary", 0o755)
	alias := filepath.Join(t.TempDir(), "package-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, output := range []string{filepath.Join(root, "release.spdx.json"), filepath.Join(alias, "release.spdx.json")} {
		_, err := Create(Options{
			Root:    root,
			Output:  output,
			Name:    "release",
			Version: "0.2.0",
			Commit:  strings.Repeat("a", 40),
			Created: time.Unix(1, 0),
		})
		if !errors.Is(err, pathguard.ErrOutputWithinSource) {
			t.Fatalf("expected output-inside-root rejection for %s, got %v", output, err)
		}
	}
}

func TestCreateNeverReplacesExistingOutput(t *testing.T) {
	root := t.TempDir()
	writeSupplyChainFixture(t, root)
	output := filepath.Join(t.TempDir(), "release.spdx.json")
	writeFixture(t, output, "sentinel\n", 0o644)
	_, err := Create(Options{
		Root: root, Output: output, Name: "pgworkbench-0.2.0-linux-amd64", Version: "0.2.0",
		Commit: strings.Repeat("a", 40), Created: time.Unix(1_700_000_000, 0),
	})
	if !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("expected existing-output rejection, got %v", err)
	}
	content, readErr := os.ReadFile(output)
	if readErr != nil || string(content) != "sentinel\n" {
		t.Fatalf("existing output changed: content=%q err=%v", content, readErr)
	}
}

func TestValidateRejectsDependencyLicensePURLAndOrderingTamper(t *testing.T) {
	makeDocument := func(t *testing.T) (string, Document) {
		t.Helper()
		root := t.TempDir()
		writeSupplyChainFixture(t, root)
		result, err := Create(Options{
			Root: root, Output: filepath.Join(t.TempDir(), "release.spdx.json"),
			Name: "pgworkbench-0.2.0-linux-amd64", Version: "0.2.0",
			Commit: strings.Repeat("a", 40), Created: time.Unix(1_700_000_000, 0),
		})
		if err != nil {
			t.Fatal(err)
		}
		document, err := Read(result.Output)
		if err != nil {
			t.Fatal(err)
		}
		return result.Output, document
	}
	tests := []struct {
		name   string
		mutate func(*Document)
	}{
		{name: "license", mutate: func(document *Document) { document.Packages[1].LicenseDeclared = "Apache-2.0" }},
		{name: "purl", mutate: func(document *Document) { document.Packages[1].ExternalRefs[0].ReferenceLocator += "-tampered" }},
		{name: "relationship direction", mutate: func(document *Document) {
			last := len(document.Relationships) - 1
			document.Relationships[last].ElementID, document.Relationships[last].RelatedElement = document.Relationships[last].RelatedElement, document.Relationships[last].ElementID
		}},
		{name: "relationship ordering", mutate: func(document *Document) {
			document.Relationships[0], document.Relationships[1] = document.Relationships[1], document.Relationships[0]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, document := makeDocument(t)
			test.mutate(&document)
			writeDocument(t, path, document)
			if err := Validate(path); err == nil {
				t.Fatal("tampered SPDX document unexpectedly validated")
			}
		})
	}
}

func writeSupplyChainFixture(t *testing.T, root string) {
	t.Helper()
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
	moduleSum := "h1:G/nrcoOa7ZXlpoa/91N3X7mM3r8eIlMBBJZvsz/mxKI="
	goModSum := "h1:DHkYz0B9wPfa6wondMfaivmHpzrQ3v9q8cnmRbL6yW8="
	writeFixture(t, filepath.Join(root, "go.mod"), "module github.com/r314tive/postgres-experiment-workbench\n\ngo 1.23\n\nrequire example.com/schema-gate v1.2.3\n", 0o644)
	writeFixture(t, filepath.Join(root, "go.sum"), "example.com/schema-gate v1.2.3 "+moduleSum+"\nexample.com/schema-gate v1.2.3/go.mod "+goModSum+"\n", 0o644)
	licensePath := "third_party/licenses/example.com/schema-gate/v1.2.3/LICENSE"
	licenseContent := "fixture MIT license\n"
	writeFixture(t, filepath.Join(root, filepath.FromSlash(licensePath)), licenseContent, 0o644)
	licenseDigest := sha256.Sum256([]byte(licenseContent))
	inventory := map[string]any{
		"schema_version": "pgworkbench.go-module-inventory/v1",
		"module_path":    "github.com/r314tive/postgres-experiment-workbench",
		"modules": []any{map[string]any{
			"path": "example.com/schema-gate", "version": "v1.2.3", "scope": "test",
			"module_sum": moduleSum, "go_mod_sum": goModSum, "license": "MIT",
			"license_files": []any{map[string]any{"path": licensePath, "sha256": "sha256:" + hex.EncodeToString(licenseDigest[:])}},
		}},
	}
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "third_party", "go-modules.json"), string(append(content, '\n')), 0o644)
}

func writeDocument(t *testing.T, path string, document Document) {
	t.Helper()
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFixture(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
