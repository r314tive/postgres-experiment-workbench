package releasesbom

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/gomoduleinventory"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

const SPDXVersion = "SPDX-2.3"

type Options struct {
	Root    string
	Output  string
	Name    string
	Version string
	Commit  string
	Created time.Time
}

type Result struct {
	Output string `json:"output"`
	SHA256 string `json:"sha256"`
	Files  int    `json:"files"`
}

type Document struct {
	SPDXID            string         `json:"SPDXID"`
	SPDXVersion       string         `json:"spdxVersion"`
	CreationInfo      CreationInfo   `json:"creationInfo"`
	Name              string         `json:"name"`
	DataLicense       string         `json:"dataLicense"`
	DocumentNamespace string         `json:"documentNamespace"`
	DocumentDescribes []string       `json:"documentDescribes"`
	Packages          []Package      `json:"packages"`
	Files             []File         `json:"files"`
	Relationships     []Relationship `json:"relationships"`
}

type CreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type Package struct {
	SPDXID                  string                   `json:"SPDXID"`
	Name                    string                   `json:"name"`
	VersionInfo             string                   `json:"versionInfo"`
	PackageFileName         string                   `json:"packageFileName,omitempty"`
	DownloadLocation        string                   `json:"downloadLocation"`
	FilesAnalyzed           bool                     `json:"filesAnalyzed"`
	PackageVerificationCode *PackageVerificationCode `json:"packageVerificationCode,omitempty"`
	LicenseConcluded        string                   `json:"licenseConcluded"`
	LicenseDeclared         string                   `json:"licenseDeclared"`
	LicenseInfoFromFiles    []string                 `json:"licenseInfoFromFiles,omitempty"`
	CopyrightText           string                   `json:"copyrightText"`
	HasFiles                []string                 `json:"hasFiles,omitempty"`
	ExternalRefs            []ExternalRef            `json:"externalRefs"`
}

type PackageVerificationCode struct {
	Value string `json:"packageVerificationCodeValue"`
}

type File struct {
	SPDXID             string     `json:"SPDXID"`
	FileName           string     `json:"fileName"`
	FileTypes          []string   `json:"fileTypes"`
	Checksums          []Checksum `json:"checksums"`
	LicenseConcluded   string     `json:"licenseConcluded"`
	LicenseInfoInFiles []string   `json:"licenseInfoInFiles"`
	CopyrightText      string     `json:"copyrightText"`
}

type Checksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"checksumValue"`
}

type ExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

type Relationship struct {
	ElementID        string `json:"spdxElementId"`
	RelatedElement   string `json:"relatedSpdxElement"`
	RelationshipType string `json:"relationshipType"`
}

type inventoryFile struct {
	path   string
	name   string
	sha1   string
	sha256 string
}

func Create(options Options) (Result, error) {
	if err := validateOptions(options); err != nil {
		return Result{}, err
	}
	absRoot, err := filepath.Abs(options.Root)
	if err != nil {
		return Result{}, err
	}
	absOutput, err := pathguard.ResolveOutputOutside(absRoot, options.Output)
	if err != nil {
		return Result{}, fmt.Errorf("resolve SPDX output: %w", err)
	}
	files, err := inventory(absRoot)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, fmt.Errorf("cannot create SPDX document for an empty package")
	}
	moduleInventory, err := gomoduleinventory.Load(absRoot)
	if err != nil {
		return Result{}, fmt.Errorf("validate release Go module inventory: %w", err)
	}
	if err := gomoduleinventory.ValidateRuntimeBinary(absRoot, moduleInventory); err != nil {
		return Result{}, fmt.Errorf("validate release Go binary dependency closure: %w", err)
	}
	document := buildDocument(options, files, moduleInventory)
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Result{}, err
	}
	content = append(content, '\n')
	absOutput, err = pathguard.PrepareNewOutputOutside(absRoot, absOutput, 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare SPDX output: %w", err)
	}
	if err := writeExclusive(absOutput, content); err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(content)
	return Result{
		Output: absOutput,
		SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		Files:  len(files),
	}, nil
}

func Validate(path string) error {
	_, err := Read(path)
	return err
}

// Read parses and validates a regular SPDX JSON file. Symlinks are rejected so
// callers can safely bind the returned document to a release artifact.
func Read(path string) (Document, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Document{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Document{}, fmt.Errorf("SPDX document is not a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Document{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Document{}, err
	}
	if err := validateDocument(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func validateDocument(document Document) error {
	if document.SPDXVersion != SPDXVersion || document.SPDXID != "SPDXRef-DOCUMENT" || document.DataLicense != "CC0-1.0" {
		return fmt.Errorf("unsupported SPDX document header")
	}
	if len(document.Packages) < 1 || len(document.Files) == 0 {
		return fmt.Errorf("SPDX document must describe one non-empty release package and its dependencies")
	}
	root := document.Packages[0]
	if len(document.DocumentDescribes) != 1 || document.DocumentDescribes[0] != root.SPDXID || root.SPDXID != "SPDXRef-Package-pgworkbench" {
		return fmt.Errorf("SPDX document must describe the canonical root package")
	}
	if !root.FilesAnalyzed || root.PackageVerificationCode == nil || root.PackageVerificationCode.Value == "" {
		return fmt.Errorf("SPDX package verification code is required")
	}
	if root.LicenseDeclared != "Apache-2.0" || root.LicenseConcluded != "Apache-2.0" || len(root.ExternalRefs) != 1 || root.ExternalRefs[0].ReferenceCategory != "PACKAGE-MANAGER" || root.ExternalRefs[0].ReferenceType != "purl" {
		return fmt.Errorf("SPDX root package license or purl is invalid")
	}
	seenPackageIDs := map[string]struct{}{root.SPDXID: {}}
	previousName := ""
	for index, pack := range document.Packages[1:] {
		if pack.Name == "" || pack.VersionInfo == "" || pack.SPDXID == "" || pack.FilesAnalyzed || pack.PackageVerificationCode != nil || pack.PackageFileName != "" || len(pack.HasFiles) != 0 {
			return fmt.Errorf("SPDX dependency package %d is invalid", index+1)
		}
		if previousName != "" && previousName >= pack.Name {
			return fmt.Errorf("SPDX dependency packages must be unique and sorted by name")
		}
		previousName = pack.Name
		if _, exists := seenPackageIDs[pack.SPDXID]; exists {
			return fmt.Errorf("duplicate SPDX package id: %s", pack.SPDXID)
		}
		seenPackageIDs[pack.SPDXID] = struct{}{}
		if !validLicense(pack.LicenseDeclared) || pack.LicenseConcluded != pack.LicenseDeclared || !validPURL(pack.ExternalRefs, modulePURL(pack.Name, pack.VersionInfo)) {
			return fmt.Errorf("SPDX dependency package %s license or purl is invalid", pack.Name)
		}
	}
	if err := validateRelationships(document); err != nil {
		return err
	}
	return nil
}

// ValidateReleaseSubject requires an SPDX document to identify the exact
// release archive, version, and immutable Git object used by the release
// manifest. archive must be the canonical archive basename, not a path.
func ValidateReleaseSubject(path string, version string, commit string, archive string) error {
	if !validCommit(commit) {
		return fmt.Errorf("SPDX commit must be a full lowercase 40- or 64-character Git object id")
	}
	if !canonicalArchiveName(archive) {
		return fmt.Errorf("SPDX subject archive is not canonical: %q", archive)
	}
	document, err := Read(path)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(archive, ".tar.gz")
	expectedNamespace := documentNamespace(version, commit, name)
	if document.Name != name {
		return fmt.Errorf("SPDX document name mismatch: got %q want %q", document.Name, name)
	}
	if document.DocumentNamespace != expectedNamespace {
		return fmt.Errorf("SPDX document namespace mismatch: got %q want %q", document.DocumentNamespace, expectedNamespace)
	}
	pack := document.Packages[0]
	if pack.VersionInfo != version {
		return fmt.Errorf("SPDX package version mismatch: got %q want %q", pack.VersionInfo, version)
	}
	if pack.PackageFileName != archive {
		return fmt.Errorf("SPDX package subject mismatch: got %q want %q", pack.PackageFileName, archive)
	}
	if !validPURL(pack.ExternalRefs, rootPURL(version)) {
		return fmt.Errorf("SPDX root package purl mismatch")
	}
	return nil
}

// ValidatePackageRoot compares the SPDX file inventory with the exact files in
// an extracted release package. Both SHA-1 and SHA-256 checksums, package file
// membership, and the SPDX package verification code must match.
func ValidatePackageRoot(path string, root string) error {
	document, err := Read(path)
	if err != nil {
		return err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	actualFiles, err := inventory(absRoot)
	if err != nil {
		return err
	}
	if len(document.Files) != len(actualFiles) {
		return fmt.Errorf("SPDX file coverage mismatch: document=%d package=%d", len(document.Files), len(actualFiles))
	}
	moduleInventory, err := gomoduleinventory.Load(absRoot)
	if err != nil {
		return fmt.Errorf("validate package Go module inventory: %w", err)
	}
	if err := gomoduleinventory.ValidateRuntimeBinary(absRoot, moduleInventory); err != nil {
		return fmt.Errorf("validate package Go binary dependency closure: %w", err)
	}
	if err := validateModulePackages(document, moduleInventory); err != nil {
		return err
	}
	actualByName := make(map[string]inventoryFile, len(actualFiles))
	verificationInputs := make([]string, 0, len(actualFiles))
	for _, item := range actualFiles {
		actualByName[item.name] = item
		verificationInputs = append(verificationInputs, item.sha1)
	}
	seenNames := make(map[string]struct{}, len(document.Files))
	seenIDs := make(map[string]struct{}, len(document.Files))
	for _, spdxFile := range document.Files {
		name, err := spdxRelativeName(spdxFile.FileName)
		if err != nil {
			return err
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("duplicate SPDX file path: %s", name)
		}
		seenNames[name] = struct{}{}
		if spdxFile.SPDXID == "" {
			return fmt.Errorf("SPDX file id is required for %s", name)
		}
		if _, exists := seenIDs[spdxFile.SPDXID]; exists {
			return fmt.Errorf("duplicate SPDX file id: %s", spdxFile.SPDXID)
		}
		seenIDs[spdxFile.SPDXID] = struct{}{}
		actual, exists := actualByName[name]
		if !exists {
			return fmt.Errorf("SPDX file is not in release package: %s", name)
		}
		checksums := make(map[string]string, len(spdxFile.Checksums))
		for _, checksum := range spdxFile.Checksums {
			if _, exists := checksums[checksum.Algorithm]; exists {
				return fmt.Errorf("duplicate %s checksum for SPDX file %s", checksum.Algorithm, name)
			}
			checksums[checksum.Algorithm] = checksum.Value
		}
		if checksums["SHA1"] != actual.sha1 {
			return fmt.Errorf("SPDX SHA1 mismatch for %s: got %s want %s", name, checksums["SHA1"], actual.sha1)
		}
		if checksums["SHA256"] != actual.sha256 {
			return fmt.Errorf("SPDX SHA256 mismatch for %s: got %s want %s", name, checksums["SHA256"], actual.sha256)
		}
	}

	pack := document.Packages[0]
	if len(pack.HasFiles) != len(seenIDs) {
		return fmt.Errorf("SPDX package file-id coverage mismatch: has_files=%d files=%d", len(pack.HasFiles), len(seenIDs))
	}
	seenHasFiles := make(map[string]struct{}, len(pack.HasFiles))
	for _, id := range pack.HasFiles {
		if _, exists := seenHasFiles[id]; exists {
			return fmt.Errorf("duplicate SPDX package file id: %s", id)
		}
		seenHasFiles[id] = struct{}{}
		if _, exists := seenIDs[id]; !exists {
			return fmt.Errorf("SPDX package references unknown file id: %s", id)
		}
	}
	sort.Strings(verificationInputs)
	expectedVerificationCode := sha1.Sum([]byte(strings.Join(verificationInputs, "")))
	expectedCode := hex.EncodeToString(expectedVerificationCode[:])
	if pack.PackageVerificationCode == nil || pack.PackageVerificationCode.Value != expectedCode {
		got := ""
		if pack.PackageVerificationCode != nil {
			got = pack.PackageVerificationCode.Value
		}
		return fmt.Errorf("SPDX package verification code mismatch: got %s want %s", got, expectedCode)
	}
	return nil
}

func spdxRelativeName(value string) (string, error) {
	if !strings.HasPrefix(value, "./") {
		return "", fmt.Errorf("SPDX file path is not canonical: %q", value)
	}
	name := strings.TrimPrefix(value, "./")
	if name == "" || strings.Contains(name, "\\") || pathpkg.IsAbs(name) || pathpkg.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("SPDX file path is not canonical: %q", value)
	}
	return name, nil
}

func validateRelationships(document Document) error {
	root := document.Packages[0]
	expected := make([]Relationship, 0, 1+len(document.Files)+len(document.Packages)-1)
	expected = append(expected, Relationship{ElementID: "SPDXRef-DOCUMENT", RelatedElement: root.SPDXID, RelationshipType: "DESCRIBES"})
	seenFiles := make(map[string]struct{}, len(document.Files))
	previousFile := ""
	if len(root.HasFiles) != len(document.Files) {
		return fmt.Errorf("SPDX root package file-id coverage mismatch")
	}
	for index, file := range document.Files {
		if file.FileName == "" || file.SPDXID == "" || previousFile != "" && previousFile >= file.FileName {
			return fmt.Errorf("SPDX files must have ids and be unique and sorted by path")
		}
		previousFile = file.FileName
		if _, exists := seenFiles[file.SPDXID]; exists {
			return fmt.Errorf("duplicate SPDX file id: %s", file.SPDXID)
		}
		seenFiles[file.SPDXID] = struct{}{}
		if root.HasFiles[index] != file.SPDXID {
			return fmt.Errorf("SPDX root package file ids must follow file order")
		}
		expected = append(expected, Relationship{ElementID: root.SPDXID, RelatedElement: file.SPDXID, RelationshipType: "CONTAINS"})
	}
	for _, pack := range document.Packages[1:] {
		dependency := Relationship{ElementID: pack.SPDXID, RelatedElement: root.SPDXID, RelationshipType: "TEST_DEPENDENCY_OF"}
		if hasRelationship(document.Relationships, Relationship{ElementID: root.SPDXID, RelatedElement: pack.SPDXID, RelationshipType: "DEPENDS_ON"}) {
			dependency = Relationship{ElementID: root.SPDXID, RelatedElement: pack.SPDXID, RelationshipType: "DEPENDS_ON"}
		}
		expected = append(expected, dependency)
	}
	if len(document.Relationships) != len(expected) {
		return fmt.Errorf("SPDX relationship coverage mismatch: got %d want %d", len(document.Relationships), len(expected))
	}
	for index := range expected {
		if document.Relationships[index] != expected[index] {
			return fmt.Errorf("SPDX relationships are incomplete or noncanonical at index %d", index)
		}
	}
	return nil
}

func validateModulePackages(document Document, inventory gomoduleinventory.Inventory) error {
	modules := gomoduleinventory.SortedModules(inventory)
	if len(document.Packages) != 1+len(modules) {
		return fmt.Errorf("SPDX Go module package coverage mismatch: document=%d inventory=%d", len(document.Packages)-1, len(modules))
	}
	rootID := document.Packages[0].SPDXID
	for index, module := range modules {
		pack := document.Packages[index+1]
		if pack.SPDXID != modulePackageID(module.Path) || pack.Name != module.Path || pack.VersionInfo != module.Version || pack.LicenseDeclared != module.License || pack.LicenseConcluded != module.License || !validPURL(pack.ExternalRefs, modulePURL(module.Path, module.Version)) {
			return fmt.Errorf("SPDX Go module package mismatch for %s", module.Path)
		}
		expected := Relationship{ElementID: pack.SPDXID, RelatedElement: rootID, RelationshipType: "TEST_DEPENDENCY_OF"}
		if module.Scope == "runtime" {
			expected = Relationship{ElementID: rootID, RelatedElement: pack.SPDXID, RelationshipType: "DEPENDS_ON"}
		}
		if !hasRelationship(document.Relationships, expected) {
			return fmt.Errorf("SPDX Go module relationship mismatch for %s", module.Path)
		}
	}
	return nil
}

func hasRelationship(relationships []Relationship, want Relationship) bool {
	for _, relationship := range relationships {
		if relationship == want {
			return true
		}
	}
	return false
}

func modulePackageID(modulePath string) string {
	digest := sha256.Sum256([]byte(modulePath))
	return "SPDXRef-Package-GoModule-" + hex.EncodeToString(digest[:12])
}

func modulePURL(modulePath, version string) string {
	return "pkg:golang/" + modulePath + "@" + version
}

func rootPURL(version string) string {
	return modulePURL("github.com/r314tive/postgres-experiment-workbench", version)
}

func purlExternalRefs(purl string) []ExternalRef {
	return []ExternalRef{{ReferenceCategory: "PACKAGE-MANAGER", ReferenceType: "purl", ReferenceLocator: purl}}
}

func validPURL(refs []ExternalRef, want string) bool {
	return len(refs) == 1 && refs[0] == (ExternalRef{ReferenceCategory: "PACKAGE-MANAGER", ReferenceType: "purl", ReferenceLocator: want})
}

func validLicense(value string) bool {
	return value == "Apache-2.0" || value == "BSD-3-Clause" || value == "MIT"
}

func buildDocument(options Options, files []inventoryFile, inventory gomoduleinventory.Inventory) Document {
	packageID := "SPDXRef-Package-pgworkbench"
	spdxFiles := make([]File, 0, len(files))
	hasFiles := make([]string, 0, len(files))
	verificationInputs := make([]string, 0, len(files))
	relationships := []Relationship{{
		ElementID:        "SPDXRef-DOCUMENT",
		RelatedElement:   packageID,
		RelationshipType: "DESCRIBES",
	}}
	for _, item := range files {
		idHash := sha256.Sum256([]byte(item.name))
		id := "SPDXRef-File-" + hex.EncodeToString(idHash[:12])
		hasFiles = append(hasFiles, id)
		verificationInputs = append(verificationInputs, item.sha1)
		spdxFiles = append(spdxFiles, File{
			SPDXID:    id,
			FileName:  "./" + item.name,
			FileTypes: []string{fileType(item.name)},
			Checksums: []Checksum{
				{Algorithm: "SHA1", Value: item.sha1},
				{Algorithm: "SHA256", Value: item.sha256},
			},
			LicenseConcluded:   "NOASSERTION",
			LicenseInfoInFiles: []string{"NOASSERTION"},
			CopyrightText:      "NOASSERTION",
		})
		relationships = append(relationships, Relationship{
			ElementID:        packageID,
			RelatedElement:   id,
			RelationshipType: "CONTAINS",
		})
	}
	sort.Strings(verificationInputs)
	verificationHash := sha1.Sum([]byte(strings.Join(verificationInputs, "")))
	created := options.Created.UTC().Truncate(time.Second).Format(time.RFC3339)
	packages := []Package{{
		SPDXID:                  packageID,
		Name:                    "pgworkbench",
		VersionInfo:             options.Version,
		PackageFileName:         options.Name + ".tar.gz",
		DownloadLocation:        "NOASSERTION",
		FilesAnalyzed:           true,
		PackageVerificationCode: &PackageVerificationCode{Value: hex.EncodeToString(verificationHash[:])},
		LicenseConcluded:        "Apache-2.0",
		LicenseDeclared:         "Apache-2.0",
		LicenseInfoFromFiles:    []string{"NOASSERTION"},
		CopyrightText:           "Copyright 2026 Ilmar Yunusov",
		HasFiles:                hasFiles,
		ExternalRefs:            purlExternalRefs(rootPURL(options.Version)),
	}}
	for _, module := range gomoduleinventory.SortedModules(inventory) {
		dependencyID := modulePackageID(module.Path)
		packages = append(packages, Package{
			SPDXID:           dependencyID,
			Name:             module.Path,
			VersionInfo:      module.Version,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			LicenseConcluded: module.License,
			LicenseDeclared:  module.License,
			CopyrightText:    "NOASSERTION",
			ExternalRefs:     purlExternalRefs(modulePURL(module.Path, module.Version)),
		})
		switch module.Scope {
		case "test":
			relationships = append(relationships, Relationship{ElementID: dependencyID, RelatedElement: packageID, RelationshipType: "TEST_DEPENDENCY_OF"})
		case "runtime":
			relationships = append(relationships, Relationship{ElementID: packageID, RelatedElement: dependencyID, RelationshipType: "DEPENDS_ON"})
		}
	}
	return Document{
		SPDXID:            "SPDXRef-DOCUMENT",
		SPDXVersion:       SPDXVersion,
		CreationInfo:      CreationInfo{Created: created, Creators: []string{"Tool: pgworkbench-release-sbom"}},
		Name:              options.Name,
		DataLicense:       "CC0-1.0",
		DocumentNamespace: documentNamespace(options.Version, options.Commit, options.Name),
		DocumentDescribes: []string{packageID},
		Packages:          packages,
		Files:             spdxFiles,
		Relationships:     relationships,
	}
}

func inventory(root string) ([]inventoryFile, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("SPDX package root must be a real directory")
	}
	var files []inventoryFile
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("SPDX package accepts regular files only: %s", relative)
		}
		sha1Digest, sha256Digest, err := digestFile(path)
		if err != nil {
			return err
		}
		files = append(files, inventoryFile{path: path, name: relative, sha1: sha1Digest, sha256: sha256Digest})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

func digestFile(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	sha1Hash := sha1.New()
	sha256Hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(sha1Hash, sha256Hash), file); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(sha1Hash.Sum(nil)), hex.EncodeToString(sha256Hash.Sum(nil)), nil
}

func validateOptions(options Options) error {
	if options.Root == "" || options.Output == "" || options.Name == "" || options.Version == "" {
		return fmt.Errorf("SPDX root, output, name, and version are required")
	}
	if options.Created.IsZero() || options.Created.Unix() < 0 {
		return fmt.Errorf("SPDX creation timestamp must be non-negative")
	}
	if !validCommit(options.Commit) {
		return fmt.Errorf("SPDX commit must be a full lowercase 40- or 64-character Git object id")
	}
	for _, value := range []string{options.Name, options.Version} {
		if strings.ContainsAny(value, "/\\\n\r") || strings.Contains(value, "..") {
			return fmt.Errorf("SPDX identity contains unsafe path text")
		}
	}
	return nil
}

func documentNamespace(version string, commit string, name string) string {
	return "https://github.com/r314tive/postgres-experiment-workbench/releases/" + version + "/" + commit + "/sbom/" + name
}

func canonicalArchiveName(value string) bool {
	if value == "" || filepath.Base(value) != value || !strings.HasPrefix(value, "pgworkbench-") || !strings.HasSuffix(value, ".tar.gz") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._+-", character) {
			continue
		}
		return false
	}
	return true
}

func validCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 || strings.Trim(value, "0") == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func fileType(name string) string {
	if name == "pgworkbench" || strings.HasSuffix(name, ".exe") {
		return "BINARY"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".sh", ".sql":
		return "SOURCE"
	case ".md", ".txt":
		return "DOCUMENTATION"
	default:
		return "OTHER"
	}
}

func writeExclusive(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pgworkbench-spdx-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return pathguard.PublishFileExclusive(temporary, path)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("unexpected data after JSON document")
}
