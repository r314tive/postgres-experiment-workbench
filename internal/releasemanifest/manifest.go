package releasemanifest

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasesbom"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
)

const (
	SchemaVersion = "pgworkbench.release-manifest/v1"
	digestPrefix  = "sha256:"
)

type ScenarioPack struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type Archive struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type SBOM struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Subject string `json:"subject"`
}

type ChecksumFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type Manifest struct {
	SchemaVersion   string       `json:"schema_version"`
	Version         string       `json:"version"`
	GitCommit       string       `json:"git_commit"`
	GoToolchain     string       `json:"go_toolchain"`
	ScenarioPack    ScenarioPack `json:"scenario_pack"`
	GeneratedAt     string       `json:"generated_at"`
	SourceDateEpoch *int64       `json:"source_date_epoch,omitempty"`
	Archives        []Archive    `json:"archives"`
	SBOMs           []SBOM       `json:"sboms"`
	ChecksumFile    ChecksumFile `json:"checksum_file"`
}

type CreateOptions struct {
	ReleaseDir      string
	Version         string
	GitCommit       string
	PackRoot        string
	ChecksumPath    string
	GeneratedAt     time.Time
	SourceDateEpoch *int64
}

// Create inventories an already-built release directory. It does not write the
// manifest; callers can use Write after inspecting the returned value.
func Create(options CreateOptions) (Manifest, error) {
	if !validSemVer(options.Version) {
		return Manifest{}, fmt.Errorf("version must be a canonical SemVer value: %q", options.Version)
	}
	if !validCommit(options.GitCommit) {
		return Manifest{}, fmt.Errorf("git commit must be a full lowercase 40- or 64-character hexadecimal object id")
	}
	releaseDir, err := existingDirectory(options.ReleaseDir, "release directory")
	if err != nil {
		return Manifest{}, err
	}
	packRoot, err := existingDirectory(options.PackRoot, "scenario pack root")
	if err != nil {
		return Manifest{}, err
	}
	pack, err := scenariopack.Validate(packRoot)
	if err != nil {
		return Manifest{}, fmt.Errorf("validate scenario pack: %w", err)
	}
	if !validSemVer(pack.Version) {
		return Manifest{}, fmt.Errorf("scenario pack version must be a canonical SemVer value: %q", pack.Version)
	}

	checksumPath := options.ChecksumPath
	if checksumPath == "" {
		checksumPath = DefaultChecksumPath(options.Version)
	}
	if err := validateChecksumPath(checksumPath); err != nil {
		return Manifest{}, err
	}
	checksumEntries, err := readChecksumEntries(releaseDir, checksumPath, options.Version)
	if err != nil {
		return Manifest{}, err
	}
	archives, err := inspectArchives(releaseDir, options.Version)
	if err != nil {
		return Manifest{}, err
	}
	if err := checkCoverage(archives, checksumEntries); err != nil {
		return Manifest{}, err
	}
	expectedPack := ScenarioPack{ID: pack.ID, Version: pack.Version, Digest: pack.Digest}
	if err := verifyArchivedPacks(releaseDir, archives, expectedPack); err != nil {
		return Manifest{}, err
	}
	goToolchain, err := inspectArchivedGoToolchainSet(releaseDir, archives)
	if err != nil {
		return Manifest{}, err
	}
	sboms, err := inspectSBOMs(releaseDir, options.Version)
	if err != nil {
		return Manifest{}, err
	}
	if err := checkSBOMCoverage(archives, sboms); err != nil {
		return Manifest{}, err
	}
	if err := verifySBOMDocuments(releaseDir, sboms, options.Version, options.GitCommit); err != nil {
		return Manifest{}, err
	}
	checksumDigest, _, err := hashRegularFile(releaseDir, checksumPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("checksum file %s: %w", checksumPath, err)
	}

	generatedAt, sourceDateEpoch, err := resolveGeneratedAt(options.GeneratedAt, options.SourceDateEpoch)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		SchemaVersion:   SchemaVersion,
		Version:         options.Version,
		GitCommit:       options.GitCommit,
		GoToolchain:     goToolchain,
		ScenarioPack:    expectedPack,
		GeneratedAt:     generatedAt,
		SourceDateEpoch: sourceDateEpoch,
		Archives:        archives,
		SBOMs:           sboms,
		ChecksumFile: ChecksumFile{
			Path:   checksumPath,
			Digest: digestPrefix + checksumDigest,
		},
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func DefaultChecksumPath(version string) string {
	return "pgworkbench-" + version + "-SHA256SUMS.txt"
}

func DefaultManifestPath(version string) string {
	return "pgworkbench-" + version + "-release-manifest.json"
}

// Write atomically publishes a new immutable manifest within releaseDir.
// Existing files, directories, and symlinks are never replaced.
func Write(releaseDir string, outputPath string, manifest Manifest) error {
	if err := Validate(manifest); err != nil {
		return err
	}
	absRoot, err := existingDirectory(releaseDir, "release directory")
	if err != nil {
		return err
	}
	if err := validateManifestPath(outputPath); err != nil {
		return err
	}
	if outputPath == manifest.ChecksumFile.Path {
		return fmt.Errorf("manifest path collides with checksum file: %s", outputPath)
	}
	for _, archive := range manifest.Archives {
		if outputPath == archive.Path {
			return fmt.Errorf("manifest path collides with archive: %s", outputPath)
		}
	}
	for _, sbom := range manifest.SBOMs {
		if outputPath == sbom.Path {
			return fmt.Errorf("manifest path collides with SBOM: %s", outputPath)
		}
	}
	target, err := pathguard.PrepareNewOutput(filepath.Join(absRoot, outputPath), 0o755)
	if err != nil {
		return fmt.Errorf("prepare immutable manifest target: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(target), ".pgworkbench-release-manifest-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(manifest)
	chmodErr := temporary.Chmod(0o644)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(encodeErr, chmodErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := pathguard.PublishFileExclusive(temporaryPath, target); err != nil {
		return err
	}
	return nil
}

func Read(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse release manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, fmt.Errorf("parse release manifest: %w", err)
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Verify recomputes the checksum file digest, every archive digest and size,
// and requires exact coverage between the manifest, the checksum file, and all
// version-matching archives in releaseDir.
func Verify(releaseDir string, manifestPath string) (Manifest, error) {
	absRoot, err := existingDirectory(releaseDir, "release directory")
	if err != nil {
		return Manifest{}, err
	}
	if err := validateManifestPath(manifestPath); err != nil {
		return Manifest{}, err
	}
	manifestFile := filepath.Join(absRoot, manifestPath)
	info, err := os.Lstat(manifestFile)
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Manifest{}, fmt.Errorf("release manifest is not a regular file: %s", manifestPath)
	}
	manifest, err := Read(manifestFile)
	if err != nil {
		return Manifest{}, err
	}

	checksumDigest, _, err := hashRegularFile(absRoot, manifest.ChecksumFile.Path)
	if err != nil {
		return Manifest{}, fmt.Errorf("checksum file %s: %w", manifest.ChecksumFile.Path, err)
	}
	if digestPrefix+checksumDigest != manifest.ChecksumFile.Digest {
		return Manifest{}, fmt.Errorf("checksum file digest mismatch: got %s want %s", digestPrefix+checksumDigest, manifest.ChecksumFile.Digest)
	}
	checksumEntries, err := readChecksumEntries(absRoot, manifest.ChecksumFile.Path, manifest.Version)
	if err != nil {
		return Manifest{}, err
	}
	actualArchives, err := inspectArchives(absRoot, manifest.Version)
	if err != nil {
		return Manifest{}, err
	}
	if err := checkCoverage(actualArchives, checksumEntries); err != nil {
		return Manifest{}, err
	}
	if err := compareArchives(manifest.Archives, actualArchives); err != nil {
		return Manifest{}, err
	}
	if err := verifyArchivedPacks(absRoot, actualArchives, manifest.ScenarioPack); err != nil {
		return Manifest{}, err
	}
	goToolchain, err := inspectArchivedGoToolchainSet(absRoot, actualArchives)
	if err != nil {
		return Manifest{}, err
	}
	if goToolchain != manifest.GoToolchain {
		return Manifest{}, fmt.Errorf("release Go toolchain mismatch: got %s want %s", goToolchain, manifest.GoToolchain)
	}
	actualSBOMs, err := inspectSBOMs(absRoot, manifest.Version)
	if err != nil {
		return Manifest{}, err
	}
	if err := checkSBOMCoverage(actualArchives, actualSBOMs); err != nil {
		return Manifest{}, err
	}
	if err := compareSBOMs(manifest.SBOMs, actualSBOMs); err != nil {
		return Manifest{}, err
	}
	if err := verifySBOMDocuments(absRoot, actualSBOMs, manifest.Version, manifest.GitCommit); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Validate(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported release manifest schema: %q", manifest.SchemaVersion)
	}
	if !validSemVer(manifest.Version) {
		return fmt.Errorf("version must be a canonical SemVer value: %q", manifest.Version)
	}
	if !validCommit(manifest.GitCommit) {
		return fmt.Errorf("git commit must be a full lowercase 40- or 64-character hexadecimal object id")
	}
	if !validGoToolchain(manifest.GoToolchain) {
		return fmt.Errorf("Go toolchain must be an exact stable patch version: %q", manifest.GoToolchain)
	}
	if !validID(manifest.ScenarioPack.ID) {
		return fmt.Errorf("invalid scenario pack id: %q", manifest.ScenarioPack.ID)
	}
	if !validSemVer(manifest.ScenarioPack.Version) {
		return fmt.Errorf("scenario pack version must be a canonical SemVer value: %q", manifest.ScenarioPack.Version)
	}
	if !validPrefixedDigest(manifest.ScenarioPack.Digest) {
		return fmt.Errorf("scenario pack digest is not canonical: %q", manifest.ScenarioPack.Digest)
	}
	generatedAt, err := time.Parse(time.RFC3339, manifest.GeneratedAt)
	if err != nil || generatedAt.UTC().Format(time.RFC3339) != manifest.GeneratedAt {
		return fmt.Errorf("generated_at must be a canonical UTC RFC3339 timestamp without fractional seconds: %q", manifest.GeneratedAt)
	}
	if manifest.SourceDateEpoch != nil {
		if *manifest.SourceDateEpoch < 0 {
			return fmt.Errorf("source_date_epoch must be non-negative")
		}
		if time.Unix(*manifest.SourceDateEpoch, 0).UTC().Format(time.RFC3339) != manifest.GeneratedAt {
			return fmt.Errorf("generated_at does not match source_date_epoch")
		}
	}
	if len(manifest.Archives) == 0 {
		return fmt.Errorf("release manifest has no archives")
	}
	seen := make(map[string]struct{}, len(manifest.Archives))
	previous := ""
	for _, archive := range manifest.Archives {
		if !archiveNameValid(archive.Path, manifest.Version) {
			return fmt.Errorf("invalid archive path: %q", archive.Path)
		}
		if previous != "" && archive.Path <= previous {
			return fmt.Errorf("archives must be unique and sorted by path")
		}
		previous = archive.Path
		if _, exists := seen[archive.Path]; exists {
			return fmt.Errorf("duplicate archive path: %s", archive.Path)
		}
		seen[archive.Path] = struct{}{}
		if !validRawDigest(archive.SHA256) {
			return fmt.Errorf("archive %s sha256 is not canonical", archive.Path)
		}
		if archive.Size <= 0 {
			return fmt.Errorf("archive %s size must be positive", archive.Path)
		}
	}
	if len(manifest.SBOMs) != len(manifest.Archives) {
		return fmt.Errorf("SBOM coverage mismatch: archives=%d sboms=%d", len(manifest.Archives), len(manifest.SBOMs))
	}
	archivePaths := make(map[string]struct{}, len(manifest.Archives))
	for _, archive := range manifest.Archives {
		archivePaths[archive.Path] = struct{}{}
	}
	seenSBOMs := make(map[string]struct{}, len(manifest.SBOMs))
	seenSubjects := make(map[string]struct{}, len(manifest.SBOMs))
	previous = ""
	for _, sbom := range manifest.SBOMs {
		if !sbomNameValid(sbom.Path, manifest.Version) {
			return fmt.Errorf("invalid SBOM path: %q", sbom.Path)
		}
		if previous != "" && sbom.Path <= previous {
			return fmt.Errorf("SBOMs must be unique and sorted by path")
		}
		previous = sbom.Path
		if _, exists := seenSBOMs[sbom.Path]; exists {
			return fmt.Errorf("duplicate SBOM path: %s", sbom.Path)
		}
		seenSBOMs[sbom.Path] = struct{}{}
		if !archiveNameValid(sbom.Subject, manifest.Version) {
			return fmt.Errorf("invalid SBOM subject: %q", sbom.Subject)
		}
		if sbom.Path != sbomPathForArchive(sbom.Subject) {
			return fmt.Errorf("SBOM path %s does not match subject %s", sbom.Path, sbom.Subject)
		}
		if _, exists := archivePaths[sbom.Subject]; !exists {
			return fmt.Errorf("SBOM subject is not in archive inventory: %s", sbom.Subject)
		}
		if _, exists := seenSubjects[sbom.Subject]; exists {
			return fmt.Errorf("duplicate SBOM subject: %s", sbom.Subject)
		}
		seenSubjects[sbom.Subject] = struct{}{}
		if !validRawDigest(sbom.SHA256) {
			return fmt.Errorf("SBOM %s sha256 is not canonical", sbom.Path)
		}
		if sbom.Size <= 0 {
			return fmt.Errorf("SBOM %s size must be positive", sbom.Path)
		}
	}
	if err := validateChecksumPath(manifest.ChecksumFile.Path); err != nil {
		return err
	}
	if !validPrefixedDigest(manifest.ChecksumFile.Digest) {
		return fmt.Errorf("checksum file digest is not canonical: %q", manifest.ChecksumFile.Digest)
	}
	return nil
}

func RenderJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func inspectArchives(root string, version string) ([]Archive, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	archives := make([]Archive, 0)
	prefix := "pgworkbench-" + version + "-"
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		if !archiveNameValid(name, version) {
			return nil, fmt.Errorf("noncanonical release archive path: %q", name)
		}
		digest, size, err := hashRegularFile(root, name)
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", name, err)
		}
		archives = append(archives, Archive{Path: name, SHA256: digest, Size: size})
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].Path < archives[j].Path })
	if len(archives) == 0 {
		return nil, fmt.Errorf("release directory has no archives for version %s", version)
	}
	return archives, nil
}

func inspectSBOMs(root string, version string) ([]SBOM, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	sboms := make([]SBOM, 0)
	prefix := "pgworkbench-" + version + "-"
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".spdx.json") {
			continue
		}
		if !sbomNameValid(name, version) {
			return nil, fmt.Errorf("noncanonical release SBOM path: %q", name)
		}
		digest, size, err := hashRegularFile(root, name)
		if err != nil {
			return nil, fmt.Errorf("SBOM %s: %w", name, err)
		}
		sboms = append(sboms, SBOM{
			Path:    name,
			SHA256:  digest,
			Size:    size,
			Subject: archiveForSBOMPath(name),
		})
	}
	sort.Slice(sboms, func(i, j int) bool { return sboms[i].Path < sboms[j].Path })
	return sboms, nil
}

func readChecksumEntries(root string, path string, version string) (map[string]string, error) {
	content, err := readRegularFile(root, path)
	if err != nil {
		return nil, fmt.Errorf("read checksum file %s: %w", path, err)
	}
	if len(content) == 0 || content[len(content)-1] != '\n' || strings.Contains(string(content), "\r") {
		return nil, fmt.Errorf("checksum file must be non-empty canonical LF-terminated text")
	}
	entries := make(map[string]string)
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	previous := ""
	for index, line := range lines {
		parts := strings.Split(line, "  ")
		if len(parts) != 2 || !validRawDigest(parts[0]) || !archiveNameValid(parts[1], version) || line != parts[0]+"  "+parts[1] {
			return nil, fmt.Errorf("checksum file line %d is not canonical", index+1)
		}
		if previous != "" && parts[1] <= previous {
			return nil, fmt.Errorf("checksum entries must be unique and sorted by path")
		}
		previous = parts[1]
		if _, exists := entries[parts[1]]; exists {
			return nil, fmt.Errorf("duplicate checksum entry: %s", parts[1])
		}
		entries[parts[1]] = parts[0]
	}
	return entries, nil
}

func checkCoverage(archives []Archive, checksums map[string]string) error {
	if len(archives) != len(checksums) {
		return fmt.Errorf("checksum coverage mismatch: archives=%d checksum_entries=%d", len(archives), len(checksums))
	}
	for _, archive := range archives {
		digest, exists := checksums[archive.Path]
		if !exists {
			return fmt.Errorf("archive is not covered by checksum file: %s", archive.Path)
		}
		if digest != archive.SHA256 {
			return fmt.Errorf("archive checksum mismatch for %s: got %s want %s", archive.Path, digest, archive.SHA256)
		}
	}
	return nil
}

func compareArchives(expected []Archive, actual []Archive) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("manifest archive coverage mismatch: manifest=%d release_directory=%d", len(expected), len(actual))
	}
	for index := range expected {
		if expected[index].Path != actual[index].Path {
			return fmt.Errorf("manifest archive path mismatch: got %s want %s", actual[index].Path, expected[index].Path)
		}
		if expected[index].SHA256 != actual[index].SHA256 {
			return fmt.Errorf("archive digest mismatch for %s: got %s want %s", expected[index].Path, actual[index].SHA256, expected[index].SHA256)
		}
		if expected[index].Size != actual[index].Size {
			return fmt.Errorf("archive size mismatch for %s: got %d want %d", expected[index].Path, actual[index].Size, expected[index].Size)
		}
	}
	return nil
}

func checkSBOMCoverage(archives []Archive, sboms []SBOM) error {
	if len(archives) != len(sboms) {
		return fmt.Errorf("SBOM coverage mismatch: archives=%d sboms=%d", len(archives), len(sboms))
	}
	bySubject := make(map[string]SBOM, len(sboms))
	for _, sbom := range sboms {
		if _, exists := bySubject[sbom.Subject]; exists {
			return fmt.Errorf("duplicate SBOM subject: %s", sbom.Subject)
		}
		bySubject[sbom.Subject] = sbom
	}
	for _, archive := range archives {
		sbom, exists := bySubject[archive.Path]
		if !exists {
			return fmt.Errorf("archive has no SBOM: %s", archive.Path)
		}
		expectedPath := sbomPathForArchive(archive.Path)
		if sbom.Path != expectedPath {
			return fmt.Errorf("archive %s has noncanonical SBOM path: got %s want %s", archive.Path, sbom.Path, expectedPath)
		}
	}
	return nil
}

func compareSBOMs(expected []SBOM, actual []SBOM) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("manifest SBOM coverage mismatch: manifest=%d release_directory=%d", len(expected), len(actual))
	}
	for index := range expected {
		if expected[index].Path != actual[index].Path {
			return fmt.Errorf("manifest SBOM path mismatch: got %s want %s", actual[index].Path, expected[index].Path)
		}
		if expected[index].Subject != actual[index].Subject {
			return fmt.Errorf("manifest SBOM subject mismatch for %s: got %s want %s", expected[index].Path, actual[index].Subject, expected[index].Subject)
		}
		if expected[index].SHA256 != actual[index].SHA256 {
			return fmt.Errorf("SBOM digest mismatch for %s: got %s want %s", expected[index].Path, actual[index].SHA256, expected[index].SHA256)
		}
		if expected[index].Size != actual[index].Size {
			return fmt.Errorf("SBOM size mismatch for %s: got %d want %d", expected[index].Path, actual[index].Size, expected[index].Size)
		}
	}
	return nil
}

func verifySBOMDocuments(releaseDir string, sboms []SBOM, version string, commit string) error {
	for _, sbom := range sboms {
		sbomPath := filepath.Join(releaseDir, sbom.Path)
		if err := releasesbom.ValidateReleaseSubject(sbomPath, version, commit, sbom.Subject); err != nil {
			return fmt.Errorf("SBOM %s identity: %w", sbom.Path, err)
		}
		if err := withExtractedArchive(releaseDir, sbom.Subject, func(root string) error {
			return releasesbom.ValidatePackageRoot(sbomPath, root)
		}); err != nil {
			return fmt.Errorf("SBOM %s package inventory: %w", sbom.Path, err)
		}
	}
	return nil
}

func verifyArchivedPacks(releaseDir string, archives []Archive, expected ScenarioPack) error {
	for _, archive := range archives {
		inspection, err := inspectArchivedPack(releaseDir, archive.Path)
		if err != nil {
			return fmt.Errorf("archive %s scenario pack: %w", archive.Path, err)
		}
		if inspection.ID != expected.ID || inspection.Version != expected.Version || inspection.Digest != expected.Digest {
			return fmt.Errorf(
				"archive %s scenario pack identity mismatch: got %s@%s %s want %s@%s %s",
				archive.Path,
				inspection.ID,
				inspection.Version,
				inspection.Digest,
				expected.ID,
				expected.Version,
				expected.Digest,
			)
		}
	}
	return nil
}

func inspectArchivedPack(releaseDir string, archiveName string) (scenariopack.Inspection, error) {
	var inspection scenariopack.Inspection
	err := withExtractedArchive(releaseDir, archiveName, func(root string) error {
		var validateErr error
		inspection, validateErr = scenariopack.Validate(root)
		return validateErr
	})
	return inspection, err
}

func inspectArchivedGoToolchainSet(releaseDir string, archives []Archive) (string, error) {
	toolchain := ""
	for _, archive := range archives {
		observed, err := inspectArchivedGoToolchain(releaseDir, archive.Path)
		if err != nil {
			return "", fmt.Errorf("archive %s Go toolchain: %w", archive.Path, err)
		}
		if toolchain == "" {
			toolchain = observed
			continue
		}
		if observed != toolchain {
			return "", fmt.Errorf("release archives use different Go toolchains: %s uses %s, want %s", archive.Path, observed, toolchain)
		}
	}
	if !validGoToolchain(toolchain) {
		return "", fmt.Errorf("release archives do not contain an exact stable Go patch toolchain")
	}
	return toolchain, nil
}

func inspectArchivedGoToolchain(releaseDir string, archiveName string) (string, error) {
	toolchain := ""
	err := withExtractedArchive(releaseDir, archiveName, func(root string) error {
		binary := filepath.Join(root, "pgworkbench")
		info, err := os.Lstat(binary)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("pgworkbench is not a regular executable")
		}
		build, err := buildinfo.ReadFile(binary)
		if err != nil {
			return fmt.Errorf("read pgworkbench build information: %w", err)
		}
		if !validGoToolchain(build.GoVersion) {
			return fmt.Errorf("embedded Go version is not an exact stable patch release: %q", build.GoVersion)
		}
		toolchain = build.GoVersion
		return nil
	})
	return toolchain, err
}

func validGoToolchain(value string) bool {
	version := strings.TrimPrefix(value, "go")
	return version != value && !strings.ContainsAny(version, "-+") && validSemVer(version)
}

func withExtractedArchive(releaseDir string, archiveName string, visit func(string) error) error {
	archivePath := filepath.Join(releaseDir, archiveName)
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	temporaryRoot, err := os.MkdirTemp("", "pgworkbench-release-manifest-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryRoot)

	const (
		maximumFiles             = 100_000
		maximumUncompressedBytes = int64(4 << 30)
	)
	seen := make(map[string]struct{})
	topLevel := ""
	fileCount := 0
	var totalSize int64
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read tar stream: %w", nextErr)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || strings.Contains(name, "\\") || pathpkg.IsAbs(name) || pathpkg.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe or noncanonical tar path: %q", header.Name)
		}
		parts := strings.Split(name, "/")
		if !canonicalFileName(parts[0]) {
			return fmt.Errorf("unsafe archive root: %q", parts[0])
		}
		if topLevel == "" {
			topLevel = parts[0]
		} else if parts[0] != topLevel {
			return fmt.Errorf("archive has multiple top-level roots")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate tar path: %s", name)
		}
		seen[name] = struct{}{}
		destination := filepath.Join(temporaryRoot, filepath.FromSlash(name))
		relative, relErr := filepath.Rel(temporaryRoot, destination)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("tar path escapes extraction root: %s", name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maximumUncompressedBytes-totalSize {
				return fmt.Errorf("archive exceeds uncompressed size limit")
			}
			fileCount++
			if fileCount > maximumFiles {
				return fmt.Errorf("archive exceeds file count limit")
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			output, openErr := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if openErr != nil {
				return openErr
			}
			written, copyErr := io.Copy(output, tarReader)
			closeErr := output.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
			if written != header.Size {
				return fmt.Errorf("short tar entry: %s", name)
			}
			totalSize += written
		default:
			return fmt.Errorf("unsupported tar entry type for %s", name)
		}
	}
	if topLevel == "" {
		return fmt.Errorf("archive is empty")
	}
	return visit(filepath.Join(temporaryRoot, topLevel))
}

func resolveGeneratedAt(generated time.Time, epoch *int64) (string, *int64, error) {
	if epoch != nil {
		if *epoch < 0 {
			return "", nil, fmt.Errorf("source_date_epoch must be non-negative")
		}
		fromEpoch := time.Unix(*epoch, 0).UTC()
		if !generated.IsZero() && !generated.Equal(fromEpoch) {
			return "", nil, fmt.Errorf("generated_at does not match source_date_epoch")
		}
		copyEpoch := *epoch
		return fromEpoch.Format(time.RFC3339), &copyEpoch, nil
	}
	if generated.IsZero() {
		generated = time.Now()
	}
	return generated.UTC().Truncate(time.Second).Format(time.RFC3339), nil, nil
}

func existingDirectory(path string, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory: %s", label, path)
	}
	return abs, nil
}

func readRegularFile(root string, name string) ([]byte, error) {
	if !canonicalFileName(name) {
		return nil, fmt.Errorf("unsafe file path: %q", name)
	}
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("not a regular file: %s", name)
	}
	return os.ReadFile(path)
}

func hashRegularFile(root string, name string) (string, int64, error) {
	if !canonicalFileName(name) {
		return "", 0, fmt.Errorf("unsafe file path: %q", name)
	}
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, fmt.Errorf("not a regular file: %s", name)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", 0, err
	}
	if size != info.Size() {
		return "", 0, fmt.Errorf("file changed while hashing: %s", name)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func validateChecksumPath(path string) error {
	if !canonicalFileName(path) || !strings.HasSuffix(path, ".txt") {
		return fmt.Errorf("invalid checksum file path: %q", path)
	}
	return nil
}

func validateManifestPath(path string) error {
	if !canonicalFileName(path) || !strings.HasSuffix(path, ".json") {
		return fmt.Errorf("invalid release manifest path: %q", path)
	}
	return nil
}

func archiveNameValid(path string, version string) bool {
	if !canonicalFileName(path) {
		return false
	}
	prefix := "pgworkbench-" + version + "-"
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, ".tar.gz") && len(path) > len(prefix)+len(".tar.gz")
}

func sbomNameValid(path string, version string) bool {
	if !canonicalFileName(path) || !strings.HasSuffix(path, ".spdx.json") {
		return false
	}
	return archiveNameValid(archiveForSBOMPath(path), version)
}

func sbomPathForArchive(archive string) string {
	return strings.TrimSuffix(archive, ".tar.gz") + ".spdx.json"
}

func archiveForSBOMPath(sbom string) string {
	return strings.TrimSuffix(sbom, ".spdx.json") + ".tar.gz"
}

func canonicalFileName(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || value == "." || value == ".." || filepath.Base(value) != value {
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

func validRawDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validPrefixedDigest(value string) bool {
	return strings.HasPrefix(value, digestPrefix) && validRawDigest(strings.TrimPrefix(value, digestPrefix))
}

func validCommit(value string) bool {
	return (validHex(value, 40) || validHex(value, 64)) && strings.Trim(value, "0") != ""
}

func validHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validID(value string) bool {
	if value == "" || strings.Trim(value, "-_.") == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.", character) {
			continue
		}
		return false
	}
	return true
}

func validSemVer(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	versionAndBuild := strings.Split(value, "+")
	if len(versionAndBuild) > 2 || len(versionAndBuild) == 2 && !validIdentifiers(versionAndBuild[1], false) {
		return false
	}
	versionAndPre := strings.SplitN(versionAndBuild[0], "-", 2)
	if len(versionAndPre) > 2 || len(versionAndPre) == 2 && !validIdentifiers(versionAndPre[1], true) {
		return false
	}
	core := strings.Split(versionAndPre[0], ".")
	if len(core) != 3 {
		return false
	}
	for _, part := range core {
		if !canonicalNumber(part) {
			return false
		}
	}
	return true
}

func validIdentifiers(value string, numericLeadingZeroForbidden bool) bool {
	identifiers := strings.Split(value, ".")
	for _, identifier := range identifiers {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, character := range identifier {
			if character < '0' || character > '9' {
				numeric = false
			}
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' {
				continue
			}
			return false
		}
		if numericLeadingZeroForbidden && numeric && !canonicalNumber(identifier) {
			return false
		}
	}
	return true
}

func canonicalNumber(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("multiple JSON values")
}
