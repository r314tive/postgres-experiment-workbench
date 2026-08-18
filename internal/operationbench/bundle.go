package operationbench

import (
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

func CreateBundle(root, input, output string, epoch time.Time) (BundleResult, error) {
	verification, err := Verify(root, input)
	if err != nil {
		return BundleResult{}, err
	}
	if !verification.Valid || verification.Series == nil {
		return BundleResult{}, fmt.Errorf("operation benchmark series is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	series := *verification.Series
	seriesDir := verification.Dir
	artifactRoot, err := inferArtifactRoot(root, seriesDir, series.Trials)
	if err != nil {
		return BundleResult{}, err
	}
	if output == "" {
		output = seriesDir + ".tar.gz"
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	output, err = pathguard.ResolveOutputOutside(seriesDir, output)
	if err != nil {
		return BundleResult{}, err
	}
	for _, trial := range series.Trials {
		runDir, joinErr := safeExistingJoin(artifactRoot, trial.RunRef)
		if joinErr != nil {
			return BundleResult{}, joinErr
		}
		output, err = pathguard.ResolveOutputOutside(runDir, output)
		if err != nil {
			return BundleResult{}, err
		}
	}
	if epoch.IsZero() {
		epoch = time.Unix(0, 0).UTC()
	}
	stage, err := os.MkdirTemp("", ".pgworkbench-operation-bundle-*")
	if err != nil {
		return BundleResult{}, err
	}
	defer os.RemoveAll(stage)
	seriesRef := filepath.ToSlash(filepath.Join("runs", "operation-benchmarks", series.RunID))
	seriesDestination, err := safeJoin(stage, seriesRef)
	if err != nil {
		return BundleResult{}, err
	}
	if err := copyTree(seriesDir, seriesDestination); err != nil {
		return BundleResult{}, err
	}
	seen := map[string]bool{}
	for _, trial := range series.Trials {
		if seen[trial.RunRef] {
			continue
		}
		seen[trial.RunRef] = true
		source, err := safeExistingJoin(artifactRoot, trial.RunRef)
		if err != nil {
			return BundleResult{}, err
		}
		destination, err := safeJoin(stage, trial.RunRef)
		if err != nil {
			return BundleResult{}, err
		}
		if err := copyTree(source, destination); err != nil {
			return BundleResult{}, err
		}
	}
	files, bytesWritten, err := bundleFiles(stage)
	if err != nil {
		return BundleResult{}, err
	}
	inventory := BundleInventory{SchemaVersion: BundleSchemaVersion, ArtifactType: BundleArtifactType, SeriesRunID: series.RunID, SeriesRef: seriesRef, Files: files}
	if err := writeJSON(filepath.Join(stage, BundleInventoryName), inventory); err != nil {
		return BundleResult{}, err
	}
	staged, err := VerifyBundle(stage, seriesDestination)
	if err != nil {
		return BundleResult{}, err
	}
	if !staged.Valid {
		return BundleResult{}, fmt.Errorf("staged operation benchmark bundle is invalid: %s", strings.Join(staged.Issues, "; "))
	}
	rootName := "pgworkbench-operation-benchmark-" + series.RunID
	archive, err := releasearchive.Create(stage, output, rootName, epoch)
	if err != nil {
		return BundleResult{}, err
	}
	return BundleResult{SeriesRunID: series.RunID, SeriesDir: seriesDir, Output: archive.Output, RootName: archive.RootName, Files: archive.Files, Bytes: bytesWritten, Digest: archive.SHA256, LinkedRuns: len(seen)}, nil
}

func checkBundleInventory(result *VerifyResult, artifactRoot string) {
	if artifactRoot == "" {
		addIssue(result, "bundle artifact root is unresolved")
		return
	}
	path := filepath.Join(artifactRoot, BundleInventoryName)
	var inventory BundleInventory
	if err := decodeStrictFile(path, &inventory); err != nil {
		addIssue(result, "%s parse failed: %v", BundleInventoryName, err)
		return
	}
	if inventory.SchemaVersion != BundleSchemaVersion || inventory.ArtifactType != BundleArtifactType {
		addIssue(result, "unsupported operation benchmark bundle schema or artifact type")
	}
	if result.Series != nil {
		expectedRef := filepath.ToSlash(filepath.Join("runs", "operation-benchmarks", result.Series.RunID))
		if inventory.SeriesRunID != result.Series.RunID || inventory.SeriesRef != expectedRef {
			addIssue(result, "operation benchmark bundle series identity mismatch")
		}
		expectedDir, joinErr := safeExistingJoin(artifactRoot, inventory.SeriesRef)
		actualDir, actualErr := filepath.EvalSymlinks(result.Dir)
		if joinErr != nil || actualErr != nil || expectedDir != actualDir {
			addIssue(result, "operation benchmark bundle inventory is not bound to the verified series directory")
		}
	}
	actual, _, err := bundleFiles(artifactRoot)
	if err != nil {
		addIssue(result, "operation benchmark bundle inventory scan failed: %v", err)
		return
	}
	for _, issue := range compareBundleFiles(inventory.Files, actual) {
		addIssue(result, "%s", issue)
	}
}

func bundleFiles(root string) ([]BundleFile, int64, error) {
	var files []BundleFile
	var bytesWritten int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle contains unsafe path: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == BundleInventoryName {
			return nil
		}
		if !evidence.IsPortablePath(rel) {
			return fmt.Errorf("bundle contains non-portable path: %s", rel)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return err
		}
		files = append(files, BundleFile{Path: rel, Size: info.Size(), Digest: digest})
		bytesWritten += info.Size()
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, bytesWritten, err
}

func compareBundleFiles(recorded, actual []BundleFile) []string {
	var issues []string
	if !sort.SliceIsSorted(recorded, func(i, j int) bool { return recorded[i].Path < recorded[j].Path }) {
		issues = append(issues, "operation benchmark bundle inventory is not sorted")
	}
	if len(recorded) != len(actual) {
		issues = append(issues, fmt.Sprintf("operation benchmark bundle file count mismatch: recorded %d actual %d", len(recorded), len(actual)))
	}
	byPath := map[string]BundleFile{}
	for _, file := range recorded {
		if !evidence.IsPortablePath(file.Path) || !evidence.IsDigest(file.Digest) || file.Size < 0 {
			issues = append(issues, "operation benchmark bundle inventory has invalid entry: "+file.Path)
			continue
		}
		if _, exists := byPath[file.Path]; exists {
			issues = append(issues, "operation benchmark bundle inventory has duplicate path: "+file.Path)
		}
		byPath[file.Path] = file
	}
	for _, file := range actual {
		recordedFile, ok := byPath[file.Path]
		if !ok {
			issues = append(issues, "operation benchmark bundle inventory is missing file: "+file.Path)
			continue
		}
		if recordedFile.Size != file.Size || recordedFile.Digest != file.Digest {
			issues = append(issues, "operation benchmark bundle digest or size mismatch: "+file.Path)
		}
		delete(byPath, file.Path)
	}
	for path := range byPath {
		issues = append(issues, "operation benchmark bundle references missing file: "+path)
	}
	sort.Strings(issues)
	return issues
}

func copyTree(source, destination string) error {
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
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle source contains symlink: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle source contains non-regular path: %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, input.Close(), output.Close())
	})
}

func RenderBundle(writer io.Writer, result BundleResult) error {
	_, err := fmt.Fprintf(writer, "Wrote operation benchmark bundle: %s files=%d bytes=%d linked_runs=%d digest=%s\n", result.Output, result.Files, result.Bytes, result.LinkedRuns, result.Digest)
	return err
}
