package scenariopack

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAndCopy(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "test-pack",
  "version": "1.2.3",
  "engine_constraint": ">=0.2.0",
  "assets": ["profiles", "scripts/run.sh"]
}
`)
	write(t, root, "profiles/smoke/sql/00_setup.sql", "select 1;\n")
	writeMode(t, root, "scripts/run.sh", "#!/bin/sh\n", 0o755)

	inspection, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.ID != "test-pack" || len(inspection.Files) != 2 || !strings.HasPrefix(inspection.Digest, "sha256:") {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}

	destination := filepath.Join(t.TempDir(), "workspace")
	copied, err := Copy(root, destination)
	if err != nil {
		t.Fatal(err)
	}
	if copied.Digest != inspection.Digest {
		t.Fatalf("digest changed after copy: %s != %s", copied.Digest, inspection.Digest)
	}
	info, err := os.Stat(filepath.Join(destination, "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("executable mode was not preserved")
	}

	var out bytes.Buffer
	if err := RenderJSON(&out, copied); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"schema_version": "pgworkbench.scenario-pack/v1"`) {
		t.Fatalf("unexpected JSON: %s", out.String())
	}
}

func TestValidateForEngineIncludesCompatibility(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "test-pack",
  "version": "1.2.3",
  "engine_constraint": "^0.2.0",
  "assets": ["profiles"]
}`)
	write(t, root, "profiles/smoke.sql", "select 1;\n")

	inspection, err := ValidateForEngine(root, "0.2.7")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.EngineCompatibility == nil || inspection.EngineCompatibility.Status != EngineCompatibleRelease {
		t.Fatalf("missing engine compatibility: %#v", inspection)
	}

	var out bytes.Buffer
	if err := RenderJSON(&out, inspection); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"release_evidence_eligible": true`) {
		t.Fatalf("compatibility was not rendered: %s", out.String())
	}
}

func TestVerifyInventoryRejectsDigestAndOrderingTampering(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{"schema_version":"pgworkbench.scenario-pack/v1","id":"test","version":"1.0.0","engine_constraint":">=0.2.0","assets":["profiles"]}`)
	write(t, root, "profiles/a.sql", "select 1;\n")
	write(t, root, "profiles/b.sql", "select 2;\n")
	inspection, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyInventory(inspection.Manifest, inspection.Files, inspection.Digest); err != nil {
		t.Fatalf("validated pack inventory did not independently verify: %v", err)
	}

	tampered := append([]File(nil), inspection.Files...)
	tampered[0].SHA256 = strings.Repeat("0", 64)
	if err := VerifyInventory(inspection.Manifest, tampered, inspection.Digest); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered file digest passed retained inventory verification: %v", err)
	}
	tampered = append([]File(nil), inspection.Files...)
	tampered[0], tampered[1] = tampered[1], tampered[0]
	if err := VerifyInventory(inspection.Manifest, tampered, inspection.Digest); err == nil || !strings.Contains(err.Error(), "strictly sorted") {
		t.Fatalf("reordered retained inventory passed verification: %v", err)
	}
}

func TestValidateForDevelopmentEngineIsExplicitlyUnverified(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "test-pack",
  "version": "1.2.3",
  "engine_constraint": ">=99.0.0",
  "assets": ["profiles"]
}`)
	write(t, root, "profiles/smoke.sql", "select 1;\n")

	inspection, err := ValidateForEngine(root, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.EngineCompatibility == nil || inspection.EngineCompatibility.Status != EngineUnverifiedDevelopment {
		t.Fatalf("development build was not marked unverified: %#v", inspection)
	}
}

func TestRejectsUnsafeAndUnknownManifestFields(t *testing.T) {
	tests := []string{
		`{"schema_version":"pgworkbench.scenario-pack/v1","id":"bad","version":"1.0.0","engine_constraint":">=0.2.0","assets":["../secret"]}`,
		`{"schema_version":"pgworkbench.scenario-pack/v1","id":"bad","version":"1.0.0","engine_constraint":">=0.2.0","assets":["profiles"],"unknown":true}`,
		`{"schema_version":"pgworkbench.scenario-pack/v1","id":"bad","version":"1.0.0","engine_constraint":">=0.2","assets":["profiles"]}`,
		`{"schema_version":"pgworkbench.scenario-pack/v1","id":"bad","version":"1.0.0","engine_constraint":">=00.2.0","assets":["profiles"]}`,
	}
	for _, manifest := range tests {
		root := t.TempDir()
		write(t, root, ManifestName, manifest)
		write(t, root, "profiles/smoke.sql", "select 1;\n")
		if _, err := Validate(root); err == nil {
			t.Fatalf("expected manifest rejection: %s", manifest)
		}
	}
}

func TestCopyRequiresEmptyDestination(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{"schema_version":"pgworkbench.scenario-pack/v1","id":"test","version":"1.0.0","engine_constraint":">=0.2.0","assets":["profiles"]}`)
	write(t, root, "profiles/smoke.sql", "select 1;\n")
	destination := t.TempDir()
	write(t, destination, "owned.txt", "keep\n")
	if _, err := Copy(root, destination); err == nil {
		t.Fatal("expected non-empty destination rejection")
	}
}

func TestCopyRejectsSymlinkDestination(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{"schema_version":"pgworkbench.scenario-pack/v1","id":"test","version":"1.0.0","engine_constraint":">=0.2.0","assets":["profiles"]}`)
	write(t, root, "profiles/smoke.sql", "select 1;\n")
	outside := t.TempDir()
	destination := filepath.Join(t.TempDir(), "export")
	if err := os.Symlink(outside, destination); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if _, err := Copy(root, destination); err == nil || !strings.Contains(err.Error(), "destination must not be a symlink") {
		t.Fatalf("expected symlink destination rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "profiles", "smoke.sql")); !os.IsNotExist(err) {
		t.Fatalf("pack was written through destination symlink: %v", err)
	}
}

func TestValidateAndCopyRejectAssetWithSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, root, ManifestName, `{"schema_version":"pgworkbench.scenario-pack/v1","id":"test","version":"1.0.0","engine_constraint":">=0.2.0","assets":["link/file.sql"]}`)
	write(t, outside, "file.sql", "select 'outside';\n")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if _, err := Validate(root); err == nil || !strings.Contains(err.Error(), "must not contain symlink") {
		t.Fatalf("expected symlinked asset ancestor rejection, got %v", err)
	}

	destination := filepath.Join(t.TempDir(), "export")
	if _, err := Copy(root, destination); err == nil || !strings.Contains(err.Error(), "must not contain symlink") {
		t.Fatalf("expected export to reject symlinked asset ancestor, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "link", "file.sql")); !os.IsNotExist(err) {
		t.Fatalf("outside asset was copied despite rejection: %v", err)
	}
}

func TestCopyVersionOverridesReleaseIdentity(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{"schema_version":"pgworkbench.scenario-pack/v1","id":"test","version":"0.2.0-dev","engine_constraint":">=0.2.0","assets":["profiles"]}`)
	write(t, root, "profiles/smoke.sql", "select 1;\n")
	destination := filepath.Join(t.TempDir(), "release")
	inspection, err := CopyVersion(root, destination, "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Version != "0.2.0" || inspection.Manifest.Version != "0.2.0" {
		t.Fatalf("unexpected version override: %#v", inspection)
	}
}

func TestCopyAsCreatesAuthoringStarterIdentity(t *testing.T) {
	root := t.TempDir()
	write(t, root, ManifestName, `{"schema_version":"pgworkbench.scenario-pack/v1","id":"builtin","version":"0.2.0","engine_constraint":">=0.2.0","assets":["profiles"]}`)
	write(t, root, "profiles/smoke.sql", "select 1;\n")
	inspection, err := CopyAs(root, filepath.Join(t.TempDir(), "starter"), "my-lab", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.ID != "my-lab" || inspection.Version != "0.1.0" {
		t.Fatalf("unexpected starter identity: %#v", inspection)
	}
}

func write(t *testing.T, root string, rel string, content string) {
	t.Helper()
	writeMode(t, root, rel, content, 0o644)
}

func writeMode(t *testing.T, root string, rel string, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
