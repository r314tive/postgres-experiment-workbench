package gomoduleinventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRepositoryInventoryMatchesGoModGoSumAndLicenseEvidence(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	inventory, err := Load(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := []Module{
		{
			Path: "github.com/dlclark/regexp2", Version: "v1.11.0", Scope: "test",
			ModuleSum: "h1:G/nrcoOa7ZXlpoa/91N3X7mM3r8eIlMBBJZvsz/mxKI=", GoModSum: "h1:DHkYz0B9wPfa6wondMfaivmHpzrQ3v9q8cnmRbL6yW8=", License: "MIT",
			LicenseFiles: []LicenseFile{
				{Path: "third_party/licenses/github.com/dlclark/regexp2/v1.11.0/ATTRIB", SHA256: "sha256:8db174fc98abc02f154c20ffdacdfbd37528ef25e0f098e20a2c21defd76329a"},
				{Path: "third_party/licenses/github.com/dlclark/regexp2/v1.11.0/LICENSE", SHA256: "sha256:9be5d04bb4d706914d5bf943710da4afeb42048f7c529902fb57c82762a991a9"},
			},
		},
		{
			Path: "github.com/santhosh-tekuri/jsonschema/v6", Version: "v6.0.2", Scope: "test",
			ModuleSum: "h1:KRzFb2m7YtdldCEkzs6KqmJw4nqEVZGK7IN2kJkjTuQ=", GoModSum: "h1:JXeL+ps8p7/KNMjDQk3TCwPpBy0wYklyWTfbkIzdIFU=", License: "Apache-2.0",
			LicenseFiles: []LicenseFile{{Path: "third_party/licenses/github.com/santhosh-tekuri/jsonschema/v6/v6.0.2/LICENSE", SHA256: "sha256:09e8a9bcec8067104652c168685ab0931e7868f9c8284b66f5ae6edae5f1130b"}},
		},
		{
			Path: "golang.org/x/text", Version: "v0.14.0", Scope: "test",
			ModuleSum: "h1:ScX5w1eTa3QqT8oi6+ziP7dTV1S2+ALU0bI+0zXKWiQ=", GoModSum: "h1:18ZOQIKpY8NJVqYksKHtTdi31H5itFRjB5/qKTNYzSU=", License: "BSD-3-Clause",
			LicenseFiles: []LicenseFile{
				{Path: "third_party/licenses/golang.org/x/text/v0.14.0/LICENSE", SHA256: "sha256:2d36597f7117c38b006835ae7f537487207d8ec407aa9d9980794b2030cbc067"},
				{Path: "third_party/licenses/golang.org/x/text/v0.14.0/PATENTS", SHA256: "sha256:96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc"},
			},
		},
	}
	if !reflect.DeepEqual(inventory.Modules, want) {
		t.Fatalf("repository Go module contract drifted:\n got: %#v\nwant: %#v", inventory.Modules, want)
	}

	for index := range inventory.Modules {
		if inventory.Modules[index].Path == "github.com/santhosh-tekuri/jsonschema/v6" {
			inventory.Modules[index].License = "MIT"
		}
	}
	if err := validateShape(inventory); err == nil || !strings.Contains(err.Error(), "audited") {
		t.Fatalf("allowed-but-wrong SPDX license unexpectedly passed audited contract: %v", err)
	}
}

func TestLoadAndRuntimeClosure(t *testing.T) {
	root := moduleFixture(t)
	inventory, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Modules) != 2 || inventory.Modules[0].Path != "example.com/alpha" || inventory.Modules[1].Path != "example.com/bravo" {
		t.Fatalf("unexpected inventory: %#v", inventory)
	}
	if err := ValidateRuntimeBinary(root, inventory); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsMissingExtraVersionLicenseTamperAndOrdering(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*testing.T, string, *Inventory)
		wantError string
	}{
		{
			name: "missing module", wantError: "coverage mismatch",
			mutate: func(_ *testing.T, _ string, inventory *Inventory) { inventory.Modules = inventory.Modules[:1] },
		},
		{
			name: "extra module", wantError: "coverage mismatch",
			mutate: func(_ *testing.T, _ string, inventory *Inventory) {
				inventory.Modules = append(inventory.Modules, Module{
					Path: "example.com/charlie", Version: "v1.0.0", Scope: "test",
					ModuleSum: inventory.Modules[0].ModuleSum, GoModSum: inventory.Modules[0].GoModSum,
					License: "MIT", LicenseFiles: append([]LicenseFile(nil), inventory.Modules[0].LicenseFiles...),
				})
			},
		},
		{
			name: "version", wantError: "version mismatch",
			mutate: func(_ *testing.T, _ string, inventory *Inventory) { inventory.Modules[0].Version = "v9.9.9" },
		},
		{
			name: "license", wantError: "license evidence",
			mutate: func(_ *testing.T, _ string, inventory *Inventory) { inventory.Modules[0].License = "GPL-3.0" },
		},
		{
			name: "license bytes tamper", wantError: "license digest mismatch",
			mutate: func(t *testing.T, root string, _ *Inventory) {
				writeTestFile(t, root, "third_party/licenses/example.com/alpha/v1.2.3/LICENSE", "tampered\n", 0o644)
			},
		},
		{
			name: "module ordering", wantError: "sorted by path",
			mutate: func(_ *testing.T, _ string, inventory *Inventory) {
				inventory.Modules[0], inventory.Modules[1] = inventory.Modules[1], inventory.Modules[0]
			},
		},
		{
			name: "license ordering", wantError: "license files",
			mutate: func(t *testing.T, root string, inventory *Inventory) {
				path := "third_party/licenses/example.com/alpha/v1.2.3/ATTRIB"
				content := "attribution\n"
				writeTestFile(t, root, path, content, 0o644)
				digest := sha256.Sum256([]byte(content))
				inventory.Modules[0].LicenseFiles = append(inventory.Modules[0].LicenseFiles, LicenseFile{Path: path, SHA256: "sha256:" + hex.EncodeToString(digest[:])})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := moduleFixture(t)
			inventory := readInventory(t, root)
			test.mutate(t, root, &inventory)
			writeInventory(t, root, inventory)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected %q rejection, got %v", test.wantError, err)
			}
		})
	}
}

func TestLoadRejectsUnsupportedGoModDirectivesRegardlessOfWhitespace(t *testing.T) {
	tests := map[string]string{
		"replace tab":         "replace\texample.com/alpha => ./local-alpha\n",
		"replace block":       "replace\t(\n\texample.com/alpha => ./local-alpha\n)\n",
		"exclude tab":         "exclude\texample.com/alpha v1.2.3\n",
		"exclude block":       "exclude\t(\n\texample.com/alpha v1.2.3\n)\n",
		"retract":             "retract\tv1.2.3\n",
		"tool":                "tool\texample.com/tool\n",
		"toolchain":           "toolchain\tgo1.26.0\n",
		"unknown future form": "workspace\t./other\n",
	}
	for name, directive := range tests {
		t.Run(name, func(t *testing.T) {
			root := moduleFixture(t)
			path := filepath.Join(root, "go.mod")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, root, "go.mod", string(content)+"\n"+directive, 0o644)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "unsupported directive") {
				t.Fatalf("expected unsupported directive rejection, got %v", err)
			}
		})
	}
}

func TestLoadAcceptsWhitespaceEquivalentModuleAndRequireDirectives(t *testing.T) {
	root := moduleFixture(t)
	writeTestFile(t, root, "go.mod", `module\tgithub.com/r314tive/postgres-experiment-workbench

go\t1.23

require\t(
\texample.com/alpha\tv1.2.3
\texample.com/bravo\tv2.3.4
)
`, 0o644)
	// The raw string above intentionally spells tabs for readability; convert
	// them into the Go module grammar's actual horizontal whitespace.
	path := filepath.Join(root, "go.mod")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "go.mod", strings.ReplaceAll(string(content), `\t`, "\t"), 0o644)
	if _, err := Load(root); err != nil {
		t.Fatalf("whitespace-equivalent go.mod was rejected: %v", err)
	}
}

func TestRuntimeClosureRejectsInventoryDriftAndNonGoBinary(t *testing.T) {
	t.Run("runtime inventory module is absent", func(t *testing.T) {
		root := moduleFixture(t)
		inventory, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		inventory.Modules[0].Scope = "runtime"
		if err := ValidateRuntimeBinary(root, inventory); err == nil || !strings.Contains(err.Error(), "not linked") {
			t.Fatalf("expected missing runtime module rejection, got %v", err)
		}
	})
	t.Run("binary build info", func(t *testing.T) {
		root := moduleFixture(t)
		inventory, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, BinaryPath, "not a Go binary\n", 0o755)
		if err := ValidateRuntimeBinary(root, inventory); err == nil || !strings.Contains(err.Error(), "build info") {
			t.Fatalf("expected build-info rejection, got %v", err)
		}
	})
}

func moduleFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", `module github.com/r314tive/postgres-experiment-workbench

go 1.23

require (
	example.com/alpha v1.2.3
	example.com/bravo v2.3.4
)
`, 0o644)
	alphaModuleSum := "h1:G/nrcoOa7ZXlpoa/91N3X7mM3r8eIlMBBJZvsz/mxKI="
	alphaGoModSum := "h1:DHkYz0B9wPfa6wondMfaivmHpzrQ3v9q8cnmRbL6yW8="
	bravoModuleSum := "h1:KRzFb2m7YtdldCEkzs6KqmJw4nqEVZGK7IN2kJkjTuQ="
	bravoGoModSum := "h1:JXeL+ps8p7/KNMjDQk3TCwPpBy0wYklyWTfbkIzdIFU="
	writeTestFile(t, root, "go.sum", "example.com/alpha v1.2.3 "+alphaModuleSum+"\nexample.com/alpha v1.2.3/go.mod "+alphaGoModSum+"\nexample.com/bravo v2.3.4 "+bravoModuleSum+"\nexample.com/bravo v2.3.4/go.mod "+bravoGoModSum+"\n", 0o644)
	alphaLicense := writeLicense(t, root, "third_party/licenses/example.com/alpha/v1.2.3/LICENSE", "alpha MIT license\n")
	bravoLicense := writeLicense(t, root, "third_party/licenses/example.com/bravo/v2.3.4/LICENSE", "bravo Apache license\n")
	writeInventory(t, root, Inventory{
		SchemaVersion: SchemaVersion,
		ModulePath:    "github.com/r314tive/postgres-experiment-workbench",
		Modules: []Module{
			{Path: "example.com/alpha", Version: "v1.2.3", Scope: "test", ModuleSum: alphaModuleSum, GoModSum: alphaGoModSum, License: "MIT", LicenseFiles: []LicenseFile{alphaLicense}},
			{Path: "example.com/bravo", Version: "v2.3.4", Scope: "test", ModuleSum: bravoModuleSum, GoModSum: bravoGoModSum, License: "Apache-2.0", LicenseFiles: []LicenseFile{bravoLicense}},
		},
	})
	copyTestExecutable(t, root)
	return root
}

func writeLicense(t *testing.T, root, path, content string) LicenseFile {
	t.Helper()
	writeTestFile(t, root, path, content, 0o644)
	digest := sha256.Sum256([]byte(content))
	return LicenseFile{Path: path, SHA256: "sha256:" + hex.EncodeToString(digest[:])}
}

func readInventory(t *testing.T, root string) Inventory {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(InventoryPath)))
	if err != nil {
		t.Fatal(err)
	}
	var inventory Inventory
	if err := json.Unmarshal(content, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func writeInventory(t *testing.T, root string, inventory Inventory) {
	t.Helper()
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, InventoryPath, string(append(content, '\n')), 0o644)
}

func copyTestExecutable(t *testing.T, root string) {
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
	destinationPath := filepath.Join(root, BinaryPath)
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
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

func writeTestFile(t *testing.T, root, relative, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
