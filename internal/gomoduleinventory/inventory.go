// Package gomoduleinventory validates the checked-in Go module and license
// inventory used by release SBOM generation. The inventory describes the
// source-containing release pack; runtime linkage is independently derived
// from the Go build information embedded in the release binary.
package gomoduleinventory

import (
	"bufio"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	SchemaVersion = "pgworkbench.go-module-inventory/v1"
	InventoryPath = "third_party/go-modules.json"
	BinaryPath    = "pgworkbench"
)

type Inventory struct {
	SchemaVersion string   `json:"schema_version"`
	ModulePath    string   `json:"module_path"`
	Modules       []Module `json:"modules"`
}

type Module struct {
	Path         string        `json:"path"`
	Version      string        `json:"version"`
	Scope        string        `json:"scope"`
	ModuleSum    string        `json:"module_sum"`
	GoModSum     string        `json:"go_mod_sum"`
	License      string        `json:"license"`
	LicenseFiles []LicenseFile `json:"license_files"`
}

type LicenseFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// auditedModuleContracts is the code-reviewed license and checksum contract for
// the exact external modules currently shipped in the pgworkbench source pack.
// Keeping this independent of the editable JSON inventory prevents a coherent
// inventory/license rewrite from silently changing an existing module's SPDX
// claim. Adding or upgrading a module intentionally requires updating both
// contracts in review.
var auditedModuleContracts = map[string]Module{
	"github.com/dlclark/regexp2": {
		Path:      "github.com/dlclark/regexp2",
		Version:   "v1.11.0",
		Scope:     "test",
		ModuleSum: "h1:G/nrcoOa7ZXlpoa/91N3X7mM3r8eIlMBBJZvsz/mxKI=",
		GoModSum:  "h1:DHkYz0B9wPfa6wondMfaivmHpzrQ3v9q8cnmRbL6yW8=",
		License:   "MIT",
		LicenseFiles: []LicenseFile{
			{Path: "third_party/licenses/github.com/dlclark/regexp2/v1.11.0/ATTRIB", SHA256: "sha256:8db174fc98abc02f154c20ffdacdfbd37528ef25e0f098e20a2c21defd76329a"},
			{Path: "third_party/licenses/github.com/dlclark/regexp2/v1.11.0/LICENSE", SHA256: "sha256:9be5d04bb4d706914d5bf943710da4afeb42048f7c529902fb57c82762a991a9"},
		},
	},
	"github.com/santhosh-tekuri/jsonschema/v6": {
		Path:      "github.com/santhosh-tekuri/jsonschema/v6",
		Version:   "v6.0.2",
		Scope:     "test",
		ModuleSum: "h1:KRzFb2m7YtdldCEkzs6KqmJw4nqEVZGK7IN2kJkjTuQ=",
		GoModSum:  "h1:JXeL+ps8p7/KNMjDQk3TCwPpBy0wYklyWTfbkIzdIFU=",
		License:   "Apache-2.0",
		LicenseFiles: []LicenseFile{
			{Path: "third_party/licenses/github.com/santhosh-tekuri/jsonschema/v6/v6.0.2/LICENSE", SHA256: "sha256:09e8a9bcec8067104652c168685ab0931e7868f9c8284b66f5ae6edae5f1130b"},
		},
	},
	"golang.org/x/text": {
		Path:      "golang.org/x/text",
		Version:   "v0.14.0",
		Scope:     "test",
		ModuleSum: "h1:ScX5w1eTa3QqT8oi6+ziP7dTV1S2+ALU0bI+0zXKWiQ=",
		GoModSum:  "h1:18ZOQIKpY8NJVqYksKHtTdi31H5itFRjB5/qKTNYzSU=",
		License:   "BSD-3-Clause",
		LicenseFiles: []LicenseFile{
			{Path: "third_party/licenses/golang.org/x/text/v0.14.0/LICENSE", SHA256: "sha256:2d36597f7117c38b006835ae7f537487207d8ec407aa9d9980794b2030cbc067"},
			{Path: "third_party/licenses/golang.org/x/text/v0.14.0/PATENTS", SHA256: "sha256:96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc"},
		},
	},
}

// Load validates the inventory against the exact go.mod, go.sum, and retained
// license bytes under root. It does not inspect the binary; callers that make
// runtime claims must also call ValidateRuntimeBinary.
func Load(root string) (Inventory, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Inventory{}, err
	}
	content, err := readRegular(absRoot, InventoryPath)
	if err != nil {
		return Inventory{}, fmt.Errorf("read Go module inventory: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	var inventory Inventory
	if err := decoder.Decode(&inventory); err != nil {
		return Inventory{}, fmt.Errorf("parse Go module inventory: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Inventory{}, fmt.Errorf("parse Go module inventory: %w", err)
	}
	if err := validateShape(inventory); err != nil {
		return Inventory{}, err
	}
	modulePath, requirements, err := parseGoMod(absRoot)
	if err != nil {
		return Inventory{}, err
	}
	if inventory.ModulePath != modulePath {
		return Inventory{}, fmt.Errorf("Go module inventory root mismatch: got %s want %s", inventory.ModulePath, modulePath)
	}
	if err := compareRequirements(inventory.Modules, requirements); err != nil {
		return Inventory{}, err
	}
	if err := compareGoSum(absRoot, inventory.Modules); err != nil {
		return Inventory{}, err
	}
	if err := validateLicenseFiles(absRoot, inventory.Modules); err != nil {
		return Inventory{}, err
	}
	return inventory, nil
}

// ValidateRuntimeBinary requires the embedded Go build dependency set to
// exactly equal the inventory entries explicitly scoped as runtime. A module
// used only by tests or release gates must stay test-scoped and
// must not silently become a statically linked runtime dependency.
func ValidateRuntimeBinary(root string, inventory Inventory) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	binary, err := regularPath(absRoot, BinaryPath)
	if err != nil {
		return fmt.Errorf("release Go binary: %w", err)
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read release Go build info: %w", err)
	}
	if info.Main.Path != inventory.ModulePath {
		return fmt.Errorf("release Go binary module mismatch: got %s want %s", info.Main.Path, inventory.ModulePath)
	}

	expected := make(map[string]Module)
	for _, module := range inventory.Modules {
		if module.Scope == "runtime" {
			expected[module.Path] = module
		}
	}
	seen := make(map[string]struct{}, len(info.Deps))
	for _, dependency := range info.Deps {
		if dependency.Replace != nil {
			return fmt.Errorf("release Go binary contains replaced module %s", dependency.Path)
		}
		module, exists := expected[dependency.Path]
		if !exists {
			return fmt.Errorf("release Go binary contains unlisted runtime module %s %s", dependency.Path, dependency.Version)
		}
		if _, duplicate := seen[dependency.Path]; duplicate {
			return fmt.Errorf("release Go binary contains duplicate runtime module %s", dependency.Path)
		}
		seen[dependency.Path] = struct{}{}
		if dependency.Version != module.Version {
			return fmt.Errorf("release Go binary module %s version mismatch: got %s want %s", dependency.Path, dependency.Version, module.Version)
		}
		if dependency.Sum != module.ModuleSum {
			return fmt.Errorf("release Go binary module %s checksum mismatch: got %s want %s", dependency.Path, dependency.Sum, module.ModuleSum)
		}
	}
	for path, module := range expected {
		if _, exists := seen[path]; !exists {
			return fmt.Errorf("runtime inventory module is not linked into release Go binary: %s %s", path, module.Version)
		}
	}
	return nil
}

func validateShape(inventory Inventory) error {
	if inventory.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported Go module inventory schema: %q", inventory.SchemaVersion)
	}
	if strings.TrimSpace(inventory.ModulePath) == "" || len(inventory.Modules) == 0 {
		return fmt.Errorf("Go module inventory root and modules are required")
	}
	for index, module := range inventory.Modules {
		if index > 0 && inventory.Modules[index-1].Path >= module.Path {
			return fmt.Errorf("Go module inventory modules must be unique and sorted by path")
		}
		if module.Path == "" || strings.ContainsAny(module.Path, " \\\n\r\t") || !strings.HasPrefix(module.Version, "v") || !validGoSum(module.ModuleSum) || !validGoSum(module.GoModSum) {
			return fmt.Errorf("Go module inventory entry %d is incomplete", index)
		}
		if module.Scope != "test" && module.Scope != "runtime" {
			return fmt.Errorf("Go module inventory module %s has unsupported scope %q", module.Path, module.Scope)
		}
		if !validLicense(module.License) || len(module.LicenseFiles) == 0 {
			return fmt.Errorf("Go module inventory module %s has no license evidence", module.Path)
		}
		for licenseIndex, file := range module.LicenseFiles {
			if licenseIndex > 0 && module.LicenseFiles[licenseIndex-1].Path >= file.Path {
				return fmt.Errorf("Go module inventory license files for %s must be unique and sorted by path", module.Path)
			}
			if !strings.HasPrefix(file.SHA256, "sha256:") || len(file.SHA256) != len("sha256:")+sha256.Size*2 {
				return fmt.Errorf("Go module inventory license file %s has invalid SHA-256", file.Path)
			}
			encoded := strings.TrimPrefix(file.SHA256, "sha256:")
			if strings.ToLower(encoded) != encoded {
				return fmt.Errorf("Go module inventory license file %s has invalid SHA-256", file.Path)
			}
			if _, err := hex.DecodeString(encoded); err != nil {
				return fmt.Errorf("Go module inventory license file %s has invalid SHA-256", file.Path)
			}
		}
		if err := validateAuditedModuleContract(module); err != nil {
			return err
		}
	}
	return nil
}

func validateAuditedModuleContract(module Module) error {
	want, audited := auditedModuleContracts[module.Path]
	if !audited {
		return nil
	}
	if module.Version != want.Version || module.Scope != want.Scope || module.ModuleSum != want.ModuleSum || module.GoModSum != want.GoModSum || module.License != want.License {
		return fmt.Errorf("Go module %s audited checksum, scope, or license evidence contract mismatch", module.Path)
	}
	if len(module.LicenseFiles) != len(want.LicenseFiles) {
		return fmt.Errorf("Go module %s audited license evidence coverage mismatch", module.Path)
	}
	for index := range want.LicenseFiles {
		if module.LicenseFiles[index] != want.LicenseFiles[index] {
			return fmt.Errorf("Go module %s audited license evidence mismatch at index %d", module.Path, index)
		}
	}
	return nil
}

func validGoSum(value string) bool {
	if !strings.HasPrefix(value, "h1:") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "h1:"))
	return err == nil && len(decoded) == sha256.Size
}

func validLicense(value string) bool {
	return value == "Apache-2.0" || value == "BSD-3-Clause" || value == "MIT"
}

func parseGoMod(root string) (string, map[string]string, error) {
	content, err := readRegular(root, "go.mod")
	if err != nil {
		return "", nil, fmt.Errorf("read go.mod: %w", err)
	}
	modulePath := ""
	requirements := make(map[string]string)
	inRequire := false
	seenGo := false
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "//", 2)[0])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if inRequire {
			if len(fields) == 1 && fields[0] == ")" {
				inRequire = false
				continue
			}
			if err := addRequirement(requirements, fields); err != nil {
				return "", nil, err
			}
			continue
		}
		switch fields[0] {
		case "module":
			if len(fields) != 2 || modulePath != "" {
				return "", nil, fmt.Errorf("release go.mod has invalid module directive")
			}
			modulePath = fields[1]
		case "go":
			if len(fields) != 2 || seenGo {
				return "", nil, fmt.Errorf("release go.mod has invalid go directive")
			}
			seenGo = true
		case "require":
			if len(fields) == 2 && fields[1] == "(" {
				inRequire = true
				continue
			}
			if err := addRequirement(requirements, fields[1:]); err != nil {
				return "", nil, err
			}
		default:
			// Fail closed for every directive outside the minimal release
			// grammar. In particular this rejects replace/exclude (including
			// tab-separated and parenthesized forms), retract, tool,
			// toolchain, workspace, and future directives until audited.
			return "", nil, fmt.Errorf("release go.mod contains unsupported directive %q", fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, err
	}
	if inRequire || modulePath == "" || !seenGo {
		return "", nil, fmt.Errorf("release go.mod is incomplete")
	}
	return modulePath, requirements, nil
}

func addRequirement(requirements map[string]string, fields []string) error {
	if len(fields) != 2 {
		return fmt.Errorf("release go.mod has invalid require directive")
	}
	if _, exists := requirements[fields[0]]; exists {
		return fmt.Errorf("release go.mod has duplicate requirement %s", fields[0])
	}
	requirements[fields[0]] = fields[1]
	return nil
}

func compareRequirements(modules []Module, requirements map[string]string) error {
	if len(modules) != len(requirements) {
		return fmt.Errorf("Go module inventory coverage mismatch: inventory=%d go.mod=%d", len(modules), len(requirements))
	}
	seen := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		version, exists := requirements[module.Path]
		if !exists {
			return fmt.Errorf("Go module inventory contains module absent from go.mod: %s", module.Path)
		}
		seen[module.Path] = struct{}{}
		if version != module.Version {
			return fmt.Errorf("Go module inventory version mismatch for %s: got %s want %s", module.Path, module.Version, version)
		}
	}
	for path := range requirements {
		if _, exists := seen[path]; !exists {
			return fmt.Errorf("go.mod module is missing from Go module inventory: %s", path)
		}
	}
	return nil
}

func compareGoSum(root string, modules []Module) error {
	content, err := readRegular(root, "go.sum")
	if err != nil {
		return fmt.Errorf("read go.sum: %w", err)
	}
	expected := make(map[string]Module, len(modules))
	for _, module := range modules {
		expected[module.Path+" "+module.Version] = module
	}
	type sums struct{ module, goMod string }
	actual := make(map[string]sums, len(modules))
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return fmt.Errorf("release go.sum has invalid line")
		}
		version := strings.TrimSuffix(fields[1], "/go.mod")
		key := fields[0] + " " + version
		if _, exists := expected[key]; !exists {
			return fmt.Errorf("go.sum contains module absent from inventory: %s", key)
		}
		entry := actual[key]
		if strings.HasSuffix(fields[1], "/go.mod") {
			if entry.goMod != "" {
				return fmt.Errorf("go.sum contains duplicate go.mod checksum for %s", key)
			}
			entry.goMod = fields[2]
		} else {
			if entry.module != "" {
				return fmt.Errorf("go.sum contains duplicate module checksum for %s", key)
			}
			entry.module = fields[2]
		}
		actual[key] = entry
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	for key, module := range expected {
		entry, exists := actual[key]
		if !exists || entry.module != module.ModuleSum || entry.goMod != module.GoModSum {
			return fmt.Errorf("go.sum checksum mismatch for %s", key)
		}
	}
	return nil
}

func validateLicenseFiles(root string, modules []Module) error {
	for _, module := range modules {
		for _, item := range module.LicenseFiles {
			content, err := readRegular(root, item.Path)
			if err != nil {
				return fmt.Errorf("Go module %s license evidence: %w", module.Path, err)
			}
			sum := sha256.Sum256(content)
			actual := "sha256:" + hex.EncodeToString(sum[:])
			if actual != item.SHA256 {
				return fmt.Errorf("Go module %s license digest mismatch for %s: got %s want %s", module.Path, item.Path, actual, item.SHA256)
			}
		}
	}
	return nil
}

func readRegular(root, relative string) ([]byte, error) {
	path, err := regularPath(root, relative)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func regularPath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("noncanonical package path: %q", relative)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("package path is not a regular file: %s", relative)
	}
	return path, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

// SortedModules returns a defensive copy in canonical module-path order.
func SortedModules(inventory Inventory) []Module {
	modules := append([]Module(nil), inventory.Modules...)
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	return modules
}
