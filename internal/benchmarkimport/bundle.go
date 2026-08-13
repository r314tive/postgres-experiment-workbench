package benchmarkimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasearchive"
)

const (
	BundleSchemaVersion = "pgworkbench.benchmark-import-bundle/v1"
	BundleArtifactType  = "pgworkbench.benchmark-import-bundle"
	BundleInventoryName = "benchmark-import-bundle.json"

	maxBundleInventoryBytes = int64(4 << 20)
)

type BundleFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// BundleInventory binds the canonical relocated import path and every retained
// byte. The imported result itself remains descriptive-only and decision
// ineligible; packaging it does not widen that assurance boundary.
type BundleInventory struct {
	SchemaVersion  string       `json:"schema_version"`
	ArtifactType   string       `json:"artifact_type"`
	ImportRef      string       `json:"import_ref"`
	ArtifactDigest string       `json:"artifact_digest"`
	Driver         string       `json:"driver"`
	Files          []BundleFile `json:"files"`
}

type BundleResult struct {
	ImportDir      string `json:"import_dir"`
	ImportRef      string `json:"import_ref"`
	ArtifactDigest string `json:"artifact_digest"`
	Output         string `json:"output"`
	RootName       string `json:"root_name"`
	Files          int    `json:"files"`
	Bytes          int64  `json:"bytes"`
	Digest         string `json:"digest"`
}

// CreateBundle produces a deterministic portable archive around one valid
// offline import. It never executes the imported benchmark driver.
func CreateBundle(input, output string, epoch time.Time) (BundleResult, error) {
	return createBundle(input, output, epoch, nil)
}

func createBundle(input, output string, epoch time.Time, beforeStageVerify func(string) error) (BundleResult, error) {
	verification, err := Verify(input)
	if err != nil {
		return BundleResult{}, err
	}
	if !verification.IsValid() || verification.Artifact == nil {
		return BundleResult{}, fmt.Errorf("benchmark import is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	artifact := *verification.Artifact
	if output == "" {
		output = verification.Dir + ".tar.gz"
	}
	output, err = pathguard.ResolveOutputOutside(verification.Dir, output)
	if err != nil {
		if errors.Is(err, pathguard.ErrOutputWithinSource) {
			return BundleResult{}, fmt.Errorf("benchmark import bundle output must be outside the immutable import directory")
		}
		return BundleResult{}, fmt.Errorf("resolve benchmark import bundle output: %w", err)
	}
	if epoch.IsZero() {
		epoch = time.Unix(0, 0).UTC()
	}

	stage, err := os.MkdirTemp("", ".pgworkbench-benchmark-import-bundle-*")
	if err != nil {
		return BundleResult{}, fmt.Errorf("create benchmark import bundle staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	importRef, err := canonicalBundleImportRef(artifact.Digest)
	if err != nil {
		return BundleResult{}, err
	}
	destination, err := safeBundleJoin(stage, importRef)
	if err != nil {
		return BundleResult{}, err
	}
	if err := copyBundleTree(verification.Dir, destination); err != nil {
		return BundleResult{}, fmt.Errorf("copy benchmark import into bundle: %w", err)
	}
	files, sourceBytes, err := collectBundleFiles(stage)
	if err != nil {
		return BundleResult{}, err
	}
	inventory := BundleInventory{
		SchemaVersion:  BundleSchemaVersion,
		ArtifactType:   BundleArtifactType,
		ImportRef:      importRef,
		ArtifactDigest: artifact.Digest,
		Driver:         artifact.Driver,
		Files:          files,
	}
	if err := writeJSON(filepath.Join(stage, BundleInventoryName), inventory); err != nil {
		return BundleResult{}, fmt.Errorf("write benchmark import bundle inventory: %w", err)
	}
	if beforeStageVerify != nil {
		if err := beforeStageVerify(stage); err != nil {
			return BundleResult{}, fmt.Errorf("prepare staged benchmark import bundle verification: %w", err)
		}
	}
	stagedVerification, err := VerifyBundle(destination)
	if err != nil {
		return BundleResult{}, fmt.Errorf("verify staged benchmark import bundle: %w", err)
	}
	if !stagedVerification.IsValid() {
		return BundleResult{}, fmt.Errorf("staged benchmark import bundle is invalid: %s", strings.Join(stagedVerification.Issues, "; "))
	}

	hexDigest := strings.TrimPrefix(artifact.Digest, evidence.DigestPrefix)
	rootName := "pgworkbench-benchmark-import-" + hexDigest
	archive, err := releasearchive.Create(stage, output, rootName, epoch)
	if err != nil {
		return BundleResult{}, fmt.Errorf("create benchmark import bundle archive: %w", err)
	}
	return BundleResult{
		ImportDir:      verification.Dir,
		ImportRef:      importRef,
		ArtifactDigest: artifact.Digest,
		Output:         archive.Output,
		RootName:       archive.RootName,
		Files:          archive.Files,
		Bytes:          sourceBytes,
		Digest:         archive.SHA256,
	}, nil
}

// VerifyBundle verifies both the ordinary import contract and the enclosing
// relocated bundle inventory. input is the canonical imports/<digest>
// directory (or its result.json) inside an extracted bundle.
func VerifyBundle(input string) (Verification, error) {
	result, err := Verify(input)
	if err != nil {
		return result, err
	}
	add := func(format string, values ...any) {
		result.Issues = append(result.Issues, fmt.Sprintf(format, values...))
	}
	if result.Artifact == nil {
		add("benchmark import result is unavailable for bundle verification")
		finalizeBundleVerification(&result)
		return result, nil
	}

	parent := filepath.Dir(result.Dir)
	if filepath.Base(parent) != "imports" {
		add("bundled benchmark import must be stored below canonical imports directory")
		finalizeBundleVerification(&result)
		return result, nil
	}
	bundleRoot := filepath.Dir(parent)
	inventoryPath := filepath.Join(bundleRoot, BundleInventoryName)
	content, readErr := readRegularLimited(inventoryPath, maxBundleInventoryBytes, "benchmark import bundle inventory")
	if readErr != nil {
		add("%s: %v", BundleInventoryName, readErr)
		finalizeBundleVerification(&result)
		return result, nil
	}
	inventory, parseErr := parseBundleInventory(content)
	if parseErr != nil {
		add("%s parse failed: %v", BundleInventoryName, parseErr)
		finalizeBundleVerification(&result)
		return result, nil
	}
	if inventory.SchemaVersion != BundleSchemaVersion || inventory.ArtifactType != BundleArtifactType {
		add("unsupported benchmark import bundle schema or artifact type")
	}
	wantRef, refErr := canonicalBundleImportRef(result.Artifact.Digest)
	if refErr != nil {
		add("derive canonical benchmark import bundle reference: %v", refErr)
	} else {
		actualRef, relativeErr := filepath.Rel(bundleRoot, result.Dir)
		if relativeErr != nil || filepath.ToSlash(actualRef) != wantRef || inventory.ImportRef != wantRef {
			add("benchmark import bundle canonical reference mismatch")
		}
	}
	if inventory.ArtifactDigest != result.Artifact.Digest || inventory.Driver != result.Artifact.Driver {
		add("benchmark import bundle identity mismatch")
	}

	actual, _, filesErr := collectBundleFiles(bundleRoot)
	if filesErr != nil {
		add("benchmark import bundle inventory failed: %v", filesErr)
	} else {
		for _, issue := range compareBundleFiles(inventory.Files, actual) {
			add("%s", issue)
		}
		for _, issue := range verifyBundleDirectories(bundleRoot, inventory.Files) {
			add("%s", issue)
		}
	}
	finalizeBundleVerification(&result)
	return result, nil
}

func finalizeBundleVerification(result *Verification) {
	result.Issues = uniqueSorted(result.Issues)
	result.Valid = result.IsValid()
}

func canonicalBundleImportRef(digest string) (string, error) {
	if !evidence.IsDigest(digest) {
		return "", fmt.Errorf("benchmark import digest is not a lowercase sha256 digest")
	}
	return "imports/" + strings.TrimPrefix(digest, evidence.DigestPrefix), nil
}

func parseBundleInventory(content []byte) (BundleInventory, error) {
	if err := validateJSONDocument(content); err != nil {
		return BundleInventory{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var inventory BundleInventory
	if err := decoder.Decode(&inventory); err != nil {
		return BundleInventory{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return BundleInventory{}, fmt.Errorf("multiple JSON values")
		}
		return BundleInventory{}, err
	}
	return inventory, nil
}

func copyBundleTree(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bundle source must be a real directory: %s", source)
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle source contains symlink: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("bundle source contains non-regular file: %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, input.Close(), output.Close())
	})
}

func safeBundleJoin(base, reference string) (string, error) {
	if !evidence.IsPortablePath(filepath.ToSlash(reference)) {
		return "", fmt.Errorf("benchmark import bundle path is not portable: %s", reference)
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(base, filepath.FromSlash(reference))
	relative, err := filepath.Rel(base, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("benchmark import bundle path escapes staging root")
	}
	return candidate, nil
}

func collectBundleFiles(root string) ([]BundleFile, int64, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, 0, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, 0, fmt.Errorf("benchmark import bundle root must be a real directory")
	}
	var files []BundleFile
	var bytesTotal int64
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
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
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("benchmark import bundle contains unsafe file: %s", path)
		}
		reference, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		reference = filepath.ToSlash(reference)
		if reference == BundleInventoryName {
			return nil
		}
		if !evidence.IsPortablePath(reference) {
			return fmt.Errorf("benchmark import bundle contains non-portable path: %s", reference)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return err
		}
		files = append(files, BundleFile{Path: reference, Size: info.Size(), Digest: digest})
		bytesTotal += info.Size()
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, bytesTotal, err
}

func compareBundleFiles(recorded, actual []BundleFile) []string {
	var issues []string
	if !sort.SliceIsSorted(recorded, func(i, j int) bool { return recorded[i].Path < recorded[j].Path }) {
		issues = append(issues, "benchmark import bundle inventory is not sorted")
	}
	if len(recorded) != len(actual) {
		issues = append(issues, fmt.Sprintf("benchmark import bundle file count mismatch: recorded %d actual %d", len(recorded), len(actual)))
	}
	byPath := make(map[string]BundleFile, len(recorded))
	for _, file := range recorded {
		if !evidence.IsPortablePath(file.Path) || !evidence.IsDigest(file.Digest) || file.Size < 0 {
			issues = append(issues, "benchmark import bundle inventory contains invalid entry: "+file.Path)
			continue
		}
		if _, exists := byPath[file.Path]; exists {
			issues = append(issues, "benchmark import bundle inventory contains duplicate path: "+file.Path)
		}
		byPath[file.Path] = file
	}
	for _, file := range actual {
		recordedFile, exists := byPath[file.Path]
		if !exists {
			issues = append(issues, "benchmark import bundle inventory is missing file: "+file.Path)
			continue
		}
		if recordedFile.Size != file.Size || recordedFile.Digest != file.Digest {
			issues = append(issues, "benchmark import bundle file digest or size mismatch: "+file.Path)
		}
		delete(byPath, file.Path)
	}
	for path := range byPath {
		issues = append(issues, "benchmark import bundle inventory references missing file: "+path)
	}
	return uniqueSorted(issues)
}

// verifyBundleDirectories rejects even unrecorded empty-directory additions.
// Every allowed directory must be an ancestor of a recorded regular file.
func verifyBundleDirectories(root string, files []BundleFile) []string {
	expected := map[string]struct{}{".": {}}
	for _, file := range files {
		if !evidence.IsPortablePath(file.Path) {
			continue
		}
		for directory := filepath.ToSlash(filepath.Dir(file.Path)); directory != "."; directory = filepath.ToSlash(filepath.Dir(directory)) {
			expected[directory] = struct{}{}
		}
	}
	var issues []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || !entry.IsDir() {
			return nil
		}
		reference, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		reference = filepath.ToSlash(reference)
		if _, exists := expected[reference]; !exists {
			issues = append(issues, "benchmark import bundle contains unexpected directory: "+reference)
		}
		return nil
	})
	return uniqueSorted(issues)
}

func RenderBundle(writer io.Writer, result BundleResult) error {
	_, err := fmt.Fprintf(writer, "Wrote descriptive benchmark import bundle: %s files=%d bytes=%d digest=%s\n", result.Output, result.Files, result.Bytes, result.Digest)
	return err
}
