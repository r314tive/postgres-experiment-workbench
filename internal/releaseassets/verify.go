package releaseassets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasemanifest"
)

var (
	tagPattern    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|([0-9]*[A-Za-z-][0-9A-Za-z-]*))(\.((0|[1-9][0-9]*)|([0-9]*[A-Za-z-][0-9A-Za-z-]*)))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	lowerHex40    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	lowerHex64    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	assetNameExpr = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,254}$`)
)

const fixedReleaseAssetCount = 16

var fixedReleasePlatforms = []string{
	"darwin-amd64",
	"darwin-arm64",
	"linux-amd64",
	"linux-arm64",
}

// Verify validates the self-contained inventory and independently recomputes
// its jq-compatible fingerprint. It does not establish that the inventory was
// returned by GitHub or that the release currently has this asset set.
func Verify(inventory Inventory) Verification {
	verification := Verification{Issues: make([]string, 0)}
	add := func(format string, args ...any) {
		verification.Issues = append(verification.Issues, fmt.Sprintf(format, args...))
	}

	if inventory.SchemaVersion != SchemaVersion {
		add("schema_version = %q, want %q", inventory.SchemaVersion, SchemaVersion)
	}
	if inventory.ArtifactType != ArtifactType {
		add("artifact_type = %q, want %q", inventory.ArtifactType, ArtifactType)
	}
	if inventory.ReleaseState != ReleaseStateDraft && inventory.ReleaseState != ReleaseStatePublished {
		add("release_state = %q, want draft or published", inventory.ReleaseState)
	}
	if !tagPattern.MatchString(inventory.Tag) {
		add("tag = %q, want a canonical v-prefixed SemVer", inventory.Tag)
	}
	if !validCommit(inventory.GitCommit) {
		add("git_commit must be a non-zero full lowercase 40- or 64-character hexadecimal object id")
	}
	if !canonicalUTCTime(inventory.CapturedAt) {
		add("captured_at must be canonical UTC RFC3339Nano")
	}
	if inventory.FingerprintAlgorithm != FingerprintAlgorithm {
		add("fingerprint_algorithm = %q, want %q", inventory.FingerprintAlgorithm, FingerprintAlgorithm)
	}
	if !lowerHex64.MatchString(inventory.AssetFingerprint) {
		add("asset_fingerprint must be 64 lowercase hexadecimal characters")
	}
	if inventory.Assets == nil {
		add("assets must be a present array containing exactly %d release assets", fixedReleaseAssetCount)
	} else if len(inventory.Assets) != fixedReleaseAssetCount {
		add("assets contains %d entries, want exactly %d release assets", len(inventory.Assets), fixedReleaseAssetCount)
	}

	seenIDs := make(map[string]struct{}, len(inventory.Assets))
	seenNames := make(map[string]struct{}, len(inventory.Assets))
	previousName := ""
	for index, asset := range inventory.Assets {
		path := fmt.Sprintf("assets[%d]", index)
		if asset.ID.kind != assetIDString && asset.ID.kind != assetIDInteger {
			add("%s.id must be a non-empty string or positive integer", path)
		} else if asset.ID.kind == assetIDString && !validAssetIDString(asset.ID.value) {
			add("%s.id string is not canonical", path)
		} else if asset.ID.kind == assetIDInteger && !validPositiveInteger(asset.ID.value) {
			add("%s.id integer is not canonical", path)
		} else if _, exists := seenIDs[asset.ID.key()]; exists {
			add("%s.id duplicates an earlier asset id", path)
		} else {
			seenIDs[asset.ID.key()] = struct{}{}
		}

		if !validAssetName(asset.Name) {
			add("%s.name = %q, want a canonical top-level release asset name", path, asset.Name)
		}
		if _, exists := seenNames[asset.Name]; exists {
			add("%s.name duplicates an earlier asset name: %q", path, asset.Name)
		} else {
			seenNames[asset.Name] = struct{}{}
		}
		if previousName != "" && asset.Name <= previousName {
			add("assets must be strictly sorted by name")
		}
		previousName = asset.Name
		if asset.Size <= 0 {
			add("%s.size must be positive", path)
		} else if asset.Size > int64(maxJSONSafeInteger) {
			add("%s.size must be no greater than %d", path, maxJSONSafeInteger)
		}
		if !evidence.IsDigest(asset.Digest) {
			add("%s.digest must be a lowercase sha256 digest", path)
		}
	}

	fingerprint, err := ComputeFingerprint(inventory.Assets)
	if err != nil {
		add("cannot compute asset fingerprint: %v", err)
	} else {
		verification.ComputedFingerprint = fingerprint
		if inventory.AssetFingerprint != fingerprint {
			add("asset_fingerprint = %q, want independently recomputed %q", inventory.AssetFingerprint, fingerprint)
		}
	}

	sort.Strings(verification.Issues)
	verification.Valid = len(verification.Issues) == 0
	return verification
}

// VerifyDirectory extends structural verification with a closed-root check of
// downloaded release asset bytes. manifest must be the value already returned
// by releasemanifest.Verify for root and manifestBasename. This function still
// re-reads the local manifest to prevent accidental cross-directory binding.
func VerifyDirectory(root string, inventory Inventory, manifest releasemanifest.Manifest, manifestBasename string) (Verification, error) {
	verification := Verify(inventory)
	add := func(format string, args ...any) {
		verification.Issues = append(verification.Issues, fmt.Sprintf(format, args...))
	}

	if err := releasemanifest.Validate(manifest); err != nil {
		add("verified release manifest value is invalid: %v", err)
	}
	if !validAssetName(manifestBasename) || filepath.Base(manifestBasename) != manifestBasename {
		add("release manifest basename is not a canonical top-level asset name: %q", manifestBasename)
	}
	if inventory.Tag != "v"+manifest.Version {
		add("tag = %q, want %q from release manifest", inventory.Tag, "v"+manifest.Version)
	}
	if inventory.GitCommit != manifest.GitCommit {
		add("git_commit = %q, want release manifest commit %q", inventory.GitCommit, manifest.GitCommit)
	}

	expectedNames, expectedErr := expectedAssetNames(manifest, manifestBasename)
	if expectedErr != nil {
		add("derive expected release asset names: %v", expectedErr)
	}

	rootInfo, err := os.Lstat(root)
	if err != nil {
		return verification, fmt.Errorf("inspect release asset root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return verification, fmt.Errorf("release asset root must be a non-symlink directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return verification, fmt.Errorf("read release asset root: %w", err)
	}

	inventoryNames := make([]string, 0, len(inventory.Assets))
	inventoryByName := make(map[string]Asset, len(inventory.Assets))
	for _, asset := range inventory.Assets {
		inventoryNames = append(inventoryNames, asset.Name)
		if _, exists := inventoryByName[asset.Name]; !exists {
			inventoryByName[asset.Name] = asset
		}
	}
	sort.Strings(inventoryNames)
	verifyManifestInventoryBinding(manifest, inventoryByName, add)
	if expectedErr == nil && !reflect.DeepEqual(inventoryNames, expectedNames) {
		add("inventory asset names do not equal the fixed closed release set: got %v want %v", inventoryNames, expectedNames)
	}

	actualNames := make([]string, 0, len(entries))
	regularNames := make(map[string]bool, len(entries))
	for _, entry := range entries {
		actualNames = append(actualNames, entry.Name())
		path := filepath.Join(root, entry.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return verification, fmt.Errorf("inspect release asset %s: %w", entry.Name(), statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			add("release asset root entry is not a regular non-symlink file: %s", entry.Name())
			continue
		}
		regularNames[entry.Name()] = true
	}
	sort.Strings(actualNames)
	if expectedErr == nil && !reflect.DeepEqual(actualNames, expectedNames) {
		add("release asset root is not the fixed closed release set: got %v want %v", actualNames, expectedNames)
	}

	for _, name := range actualNames {
		asset, exists := inventoryByName[name]
		if !exists || !regularNames[name] {
			continue
		}
		actualDigest, actualSize, hashErr := hashRegularFile(filepath.Join(root, name))
		if hashErr != nil {
			return verification, fmt.Errorf("hash release asset %s: %w", name, hashErr)
		}
		if actualSize != asset.Size {
			add("release asset %s size = %d, want inventory size %d", name, actualSize, asset.Size)
		}
		if actualDigest != asset.Digest {
			add("release asset %s digest = %s, want inventory digest %s", name, actualDigest, asset.Digest)
		}
	}
	if regularNames[metadataChecksumName(manifest.Version)] {
		if checksumErr := verifyMetadataChecksum(root, manifest, manifestBasename, add); checksumErr != nil {
			return verification, checksumErr
		}
	}

	if validAssetName(manifestBasename) && filepath.Base(manifestBasename) == manifestBasename {
		localManifest, readErr := releasemanifest.Read(filepath.Join(root, manifestBasename))
		if readErr != nil {
			add("read local release manifest: %v", readErr)
		} else if !reflect.DeepEqual(localManifest, manifest) {
			add("local release manifest does not equal the verified manifest value")
		}
	}

	sort.Strings(verification.Issues)
	verification.Valid = len(verification.Issues) == 0
	return verification, nil
}

// verifyManifestInventoryBinding closes the split-verification gap between the
// semantic manifest pass and the provider inventory pass. Archive, SBOM, and
// checksum bytes must have the same identity in both independently supplied
// records; matching only their names would permit a different distribution to
// be substituted between those passes.
func verifyManifestInventoryBinding(manifest releasemanifest.Manifest, inventoryByName map[string]Asset, add func(string, ...any)) {
	for _, archive := range manifest.Archives {
		asset, exists := inventoryByName[archive.Path]
		if !exists {
			continue
		}
		manifestDigest := evidence.DigestPrefix + archive.SHA256
		if asset.Size != archive.Size {
			add("release asset %s size %d does not match release manifest size %d", archive.Path, asset.Size, archive.Size)
		}
		if asset.Digest != manifestDigest {
			add("release asset %s digest %s does not match release manifest digest %s", archive.Path, asset.Digest, manifestDigest)
		}
	}
	for _, sbom := range manifest.SBOMs {
		asset, exists := inventoryByName[sbom.Path]
		if !exists {
			continue
		}
		manifestDigest := evidence.DigestPrefix + sbom.SHA256
		if asset.Size != sbom.Size {
			add("release asset %s size %d does not match release manifest size %d", sbom.Path, asset.Size, sbom.Size)
		}
		if asset.Digest != manifestDigest {
			add("release asset %s digest %s does not match release manifest digest %s", sbom.Path, asset.Digest, manifestDigest)
		}
	}
	if asset, exists := inventoryByName[manifest.ChecksumFile.Path]; exists && asset.Digest != manifest.ChecksumFile.Digest {
		add("release asset %s digest %s does not match release manifest checksum digest %s", manifest.ChecksumFile.Path, asset.Digest, manifest.ChecksumFile.Digest)
	}
}

// ComputeFingerprint implements the workflow formula:
//
//	jq -cS '[.assets[] | {id, name, size, digest}] | sort_by(.name)'
//
// followed by SHA-256 over the compact JSON bytes without a trailing newline.
func ComputeFingerprint(assets []Asset) (string, error) {
	sorted := append([]Asset(nil), assets...)
	sort.SliceStable(sorted, func(left, right int) bool {
		return sorted[left].Name < sorted[right].Name
	})

	var canonical strings.Builder
	canonical.WriteByte('[')
	for index, asset := range sorted {
		if index > 0 {
			canonical.WriteByte(',')
		}
		if asset.ID.kind != assetIDString && asset.ID.kind != assetIDInteger {
			return "", fmt.Errorf("asset %d id is not initialized", index)
		}
		canonical.WriteString(`{"digest":`)
		appendJQString(&canonical, asset.Digest)
		canonical.WriteString(`,"id":`)
		if asset.ID.kind == assetIDString {
			appendJQString(&canonical, asset.ID.value)
		} else {
			if !validPositiveInteger(asset.ID.value) {
				return "", fmt.Errorf("asset %d integer id is not canonical", index)
			}
			canonical.WriteString(asset.ID.value)
		}
		canonical.WriteString(`,"name":`)
		appendJQString(&canonical, asset.Name)
		canonical.WriteString(`,"size":`)
		canonical.WriteString(strconv.FormatInt(asset.Size, 10))
		canonical.WriteByte('}')
	}
	canonical.WriteByte(']')

	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:]), nil
}

func appendJQString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"', '\\':
			builder.WriteByte('\\')
			builder.WriteRune(character)
		case '\b':
			builder.WriteString(`\b`)
		case '\t':
			builder.WriteString(`\t`)
		case '\n':
			builder.WriteString(`\n`)
		case '\f':
			builder.WriteString(`\f`)
		case '\r':
			builder.WriteString(`\r`)
		default:
			if character < 0x20 {
				builder.WriteString(`\u00`)
				const digits = "0123456789abcdef"
				builder.WriteByte(digits[(character>>4)&0xf])
				builder.WriteByte(digits[character&0xf])
			} else {
				builder.WriteRune(character)
			}
		}
	}
	builder.WriteByte('"')
}

func expectedAssetNames(manifest releasemanifest.Manifest, manifestBasename string) ([]string, error) {
	expectedManifest := releasemanifest.DefaultManifestPath(manifest.Version)
	if manifestBasename != expectedManifest {
		return nil, fmt.Errorf("release manifest name = %q, want fixed release name %q", manifestBasename, expectedManifest)
	}
	expectedChecksum := releasemanifest.DefaultChecksumPath(manifest.Version)
	if manifest.ChecksumFile.Path != expectedChecksum {
		return nil, fmt.Errorf("archive checksum name = %q, want fixed release name %q", manifest.ChecksumFile.Path, expectedChecksum)
	}

	expectedArchives := make([]string, 0, len(fixedReleasePlatforms))
	expectedSBOMs := make([]string, 0, len(fixedReleasePlatforms))
	for _, platform := range fixedReleasePlatforms {
		prefix := "pgworkbench-" + manifest.Version + "-" + platform
		expectedArchives = append(expectedArchives, prefix+".tar.gz")
		expectedSBOMs = append(expectedSBOMs, prefix+".spdx.json")
	}
	actualArchives := make([]string, 0, len(manifest.Archives))
	for _, archive := range manifest.Archives {
		actualArchives = append(actualArchives, archive.Path)
	}
	if !reflect.DeepEqual(actualArchives, expectedArchives) {
		return nil, fmt.Errorf("release manifest archives = %v, want fixed platform archives %v", actualArchives, expectedArchives)
	}
	actualSBOMs := make([]string, 0, len(manifest.SBOMs))
	for _, sbom := range manifest.SBOMs {
		actualSBOMs = append(actualSBOMs, sbom.Path)
	}
	if !reflect.DeepEqual(actualSBOMs, expectedSBOMs) {
		return nil, fmt.Errorf("release manifest SBOMs = %v, want fixed platform SBOMs %v", actualSBOMs, expectedSBOMs)
	}

	names := make([]string, 0, fixedReleaseAssetCount)
	names = append(names, expectedArchives...)
	for _, sbom := range expectedSBOMs {
		names = append(names, sbom, strings.TrimSuffix(sbom, ".spdx.json")+"-sbom.sigstore.json")
	}
	names = append(names,
		expectedChecksum,
		manifestBasename,
		metadataChecksumName(manifest.Version),
		"pgworkbench-"+manifest.Version+"-provenance.sigstore.json",
	)
	sort.Strings(names)
	if len(names) != fixedReleaseAssetCount {
		return nil, fmt.Errorf("fixed release asset count = %d, want %d", len(names), fixedReleaseAssetCount)
	}
	for index, name := range names {
		if !validAssetName(name) {
			return nil, fmt.Errorf("noncanonical expected asset name: %q", name)
		}
		if index > 0 && names[index-1] == name {
			return nil, fmt.Errorf("duplicate expected asset name: %s", name)
		}
	}
	return names, nil
}

func verifyMetadataChecksum(root string, manifest releasemanifest.Manifest, manifestBasename string, add func(string, ...any)) error {
	const maxMetadataChecksumBytes = int64(2 << 20)
	checksumName := metadataChecksumName(manifest.Version)
	content, err := readRegularFileLimited(filepath.Join(root, checksumName), maxMetadataChecksumBytes)
	if err != nil {
		return fmt.Errorf("read metadata checksum file: %w", err)
	}
	if len(content) == 0 || content[len(content)-1] != '\n' {
		add("metadata checksum file must be non-empty and end with one newline")
		return nil
	}
	if len(content) >= 2 && content[len(content)-2] == '\n' {
		add("metadata checksum file must not contain a trailing blank row")
	}

	expectedNames, err := expectedMetadataChecksumNames(manifest, manifestBasename)
	if err != nil {
		add("derive metadata checksum coverage: %v", err)
		return nil
	}
	rows := strings.Split(string(content[:len(content)-1]), "\n")
	if len(rows) != len(expectedNames) {
		add("metadata checksum row count = %d, want %d", len(rows), len(expectedNames))
	}
	seen := make(map[string]struct{}, len(rows))
	for index, row := range rows {
		if len(row) < 68 || row[64:68] != "  ./" || !lowerHex64.MatchString(row[:64]) {
			add("metadata checksum row %d is not canonical sha256sum output", index+1)
			continue
		}
		name := row[68:]
		if !validAssetName(name) {
			add("metadata checksum row %d has a noncanonical asset name: %q", index+1, name)
			continue
		}
		if _, exists := seen[name]; exists {
			add("metadata checksum row %d duplicates asset %s", index+1, name)
			continue
		}
		seen[name] = struct{}{}
		if index >= len(expectedNames) || name != expectedNames[index] {
			want := "no row"
			if index < len(expectedNames) {
				want = expectedNames[index]
			}
			add("metadata checksum row %d names %s, want %s in workflow order", index+1, name, want)
		}
		digest, _, hashErr := hashRegularFile(filepath.Join(root, name))
		if hashErr != nil {
			add("metadata checksum row %d cannot hash %s: %v", index+1, name, hashErr)
			continue
		}
		if row[:64] != strings.TrimPrefix(digest, evidence.DigestPrefix) {
			add("metadata checksum row %d digest does not match %s", index+1, name)
		}
	}
	if len(seen) != len(expectedNames) {
		missing := make([]string, 0)
		for _, name := range expectedNames {
			if _, exists := seen[name]; !exists {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			add("metadata checksum coverage is missing: %s", strings.Join(missing, ", "))
		}
	}
	return nil
}

func expectedMetadataChecksumNames(manifest releasemanifest.Manifest, manifestBasename string) ([]string, error) {
	names := make([]string, 0, 2*len(manifest.SBOMs)+2)
	for _, sbom := range manifest.SBOMs {
		if !strings.HasSuffix(sbom.Path, ".spdx.json") {
			return nil, fmt.Errorf("SBOM path has no .spdx.json suffix: %s", sbom.Path)
		}
		names = append(names, sbom.Path)
	}
	sigstoreNames := make([]string, 0, len(manifest.SBOMs)+1)
	for _, sbom := range manifest.SBOMs {
		sigstoreNames = append(sigstoreNames, strings.TrimSuffix(sbom.Path, ".spdx.json")+"-sbom.sigstore.json")
	}
	sigstoreNames = append(sigstoreNames, "pgworkbench-"+manifest.Version+"-provenance.sigstore.json")
	sort.Strings(sigstoreNames)
	names = append(names, sigstoreNames...)
	names = append(names, manifestBasename)

	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !validAssetName(name) {
			return nil, fmt.Errorf("noncanonical metadata asset name: %q", name)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate metadata asset name: %s", name)
		}
		seen[name] = struct{}{}
	}
	return names, nil
}

func metadataChecksumName(version string) string {
	return "pgworkbench-" + version + "-METADATA-SHA256SUMS.txt"
}

func readRegularFileLimited(path string, maximum int64) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular non-symlink file")
	}
	if pathInfo.Size() > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !fileInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		return nil, fmt.Errorf("file changed while it was being opened")
	}
	content, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return content, nil
}

func hashRegularFile(path string) (string, int64, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return "", 0, fmt.Errorf("not a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !fileInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		return "", 0, fmt.Errorf("file changed while it was being opened")
	}
	digest, size, err := evidence.DigestReader(file)
	return digest, size, err
}

func validCommit(value string) bool {
	if !lowerHex40.MatchString(value) && !lowerHex64.MatchString(value) {
		return false
	}
	return strings.Trim(value, "0") != ""
}

func validAssetName(value string) bool {
	return assetNameExpr.MatchString(value) && utf8.ValidString(value) &&
		!strings.ContainsAny(value, `/\\`) && filepath.Base(value) == value
}

func canonicalUTCTime(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z") && parsed.UTC().Format(time.RFC3339Nano) == value
}

func validPositiveInteger(value string) bool {
	if !canonicalPositiveInteger.MatchString(value) {
		return false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed <= maxJSONSafeInteger
}
