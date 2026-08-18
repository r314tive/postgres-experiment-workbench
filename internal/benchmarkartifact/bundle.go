package benchmarkartifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasearchive"
)

const (
	BundleSchemaVersion = "pgworkbench.benchmark-bundle/v1"
	BundleArtifactType  = "pgworkbench.benchmark-bundle"
	BundleInventoryName = "benchmark-bundle.json"
)

type BundleFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type BundleInventory struct {
	SchemaVersion string       `json:"schema_version"`
	ArtifactType  string       `json:"artifact_type"`
	SeriesRunID   string       `json:"series_run_id"`
	SeriesRef     string       `json:"series_ref"`
	Files         []BundleFile `json:"files"`
}

type BundleResult struct {
	SeriesRunID string `json:"series_run_id"`
	SeriesDir   string `json:"series_dir"`
	Output      string `json:"output"`
	RootName    string `json:"root_name"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Digest      string `json:"digest"`
	LinkedRuns  int    `json:"linked_runs"`
}

func CreateBundle(root string, input string, output string, epoch time.Time) (BundleResult, error) {
	return createBundle(root, input, output, epoch, nil)
}

func createBundle(root string, input string, output string, epoch time.Time, beforeStageVerify func(string) error) (BundleResult, error) {
	verification, err := Verify(root, input)
	if err != nil {
		return BundleResult{}, err
	}
	if !verification.IsValid() || verification.Series == nil {
		return BundleResult{}, fmt.Errorf("benchmark series artifact is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	series := *verification.Series
	seriesDir := verification.Dir
	if requiresABBundle(series) {
		return BundleResult{}, fmt.Errorf("native_toolchain A/B arm requires benchmark ab-bundle so the arm-specific toolchain closure is retained")
	}
	artifactRoot := inferArtifactRoot(root, seriesDir)
	if output == "" {
		output = seriesDir + ".tar.gz"
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	output, err = pathguard.ResolveOutputOutside(seriesDir, output)
	if err != nil {
		return BundleResult{}, fmt.Errorf("resolve benchmark bundle output outside series artifact: %w", err)
	}
	linkedSources := make([]struct {
		runID  string
		ref    string
		source string
	}, 0, len(series.Trials))
	seenRuns := make(map[string]struct{}, len(series.Trials))
	for _, trial := range series.Trials {
		if _, ok := seenRuns[trial.RunID]; ok {
			continue
		}
		seenRuns[trial.RunID] = struct{}{}
		source, err := safeExistingJoin(artifactRoot, trial.RunRef)
		if err != nil {
			return BundleResult{}, fmt.Errorf("resolve linked run %s source: %w", trial.RunID, err)
		}
		output, err = pathguard.ResolveOutputOutside(source, output)
		if err != nil {
			return BundleResult{}, fmt.Errorf("resolve benchmark bundle output outside linked run %s: %w", trial.RunID, err)
		}
		linkedSources = append(linkedSources, struct {
			runID  string
			ref    string
			source string
		}{runID: trial.RunID, ref: trial.RunRef, source: source})
	}
	if epoch.IsZero() {
		epoch = time.Unix(0, 0).UTC()
	}

	stage, err := os.MkdirTemp("", ".pgworkbench-benchmark-bundle-*")
	if err != nil {
		return BundleResult{}, err
	}
	defer os.RemoveAll(stage)
	seriesRef := filepath.ToSlash(filepath.Join("runs", "benchmarks", series.RunID))
	seriesDestination, err := safeJoin(stage, seriesRef)
	if err != nil {
		return BundleResult{}, fmt.Errorf("resolve bundle series destination: %w", err)
	}
	if err := copyTree(seriesDir, seriesDestination); err != nil {
		return BundleResult{}, err
	}
	for _, linked := range linkedSources {
		destination, err := safeJoin(stage, linked.ref)
		if err != nil {
			return BundleResult{}, fmt.Errorf("resolve linked run %s destination: %w", linked.runID, err)
		}
		if err := copyTree(linked.source, destination); err != nil {
			return BundleResult{}, fmt.Errorf("copy linked run %s: %w", linked.runID, err)
		}
	}
	files, bytes, err := bundleFiles(stage)
	if err != nil {
		return BundleResult{}, err
	}
	inventory := BundleInventory{
		SchemaVersion: BundleSchemaVersion,
		ArtifactType:  BundleArtifactType,
		SeriesRunID:   series.RunID,
		SeriesRef:     seriesRef,
		Files:         files,
	}
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return BundleResult{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, BundleInventoryName), append(content, '\n'), 0o644); err != nil {
		return BundleResult{}, err
	}
	if beforeStageVerify != nil {
		if err := beforeStageVerify(stage); err != nil {
			return BundleResult{}, fmt.Errorf("prepare staged benchmark bundle verification: %w", err)
		}
	}
	stagedVerification, err := VerifyBundle(stage, seriesDestination)
	if err != nil {
		return BundleResult{}, fmt.Errorf("verify staged benchmark bundle: %w", err)
	}
	if !stagedVerification.IsValid() {
		return BundleResult{}, fmt.Errorf("staged benchmark bundle is invalid: %s", strings.Join(stagedVerification.Issues, "; "))
	}

	rootName := "pgworkbench-benchmark-" + series.RunID
	archive, err := releasearchive.Create(stage, output, rootName, epoch)
	if err != nil {
		return BundleResult{}, err
	}
	return BundleResult{
		SeriesRunID: series.RunID,
		SeriesDir:   seriesDir,
		Output:      archive.Output,
		RootName:    archive.RootName,
		Files:       archive.Files,
		Bytes:       bytes,
		Digest:      archive.SHA256,
		LinkedRuns:  len(seenRuns),
	}, nil
}

func requiresABBundle(series benchmarkrun.Series) bool {
	return series.Environment != nil && series.Environment.SubjectDimension == "native_toolchain"
}

func VerifyBundle(root string, input string) (VerifyResult, error) {
	result, err := Verify(root, input)
	if err != nil {
		return result, err
	}
	artifactRoot := inferArtifactRoot(root, result.Dir)
	path := filepath.Join(artifactRoot, BundleInventoryName)
	var inventory BundleInventory
	if err := decodeStrict(path, &inventory); err != nil {
		addIssue(&result, "%s parse failed: %v", BundleInventoryName, err)
		result.Valid = false
		return result, nil
	}
	if inventory.SchemaVersion != BundleSchemaVersion || inventory.ArtifactType != BundleArtifactType {
		addIssue(&result, "unsupported benchmark bundle schema or artifact type")
	}
	if result.Series != nil {
		expectedRef := filepath.ToSlash(filepath.Join("runs", "benchmarks", result.Series.RunID))
		if inventory.SeriesRunID != result.Series.RunID || inventory.SeriesRef != expectedRef {
			addIssue(&result, "benchmark bundle series identity mismatch")
		}
	}
	expected, _, err := bundleFiles(artifactRoot)
	if err != nil {
		addIssue(&result, "benchmark bundle inventory failed: %v", err)
	} else if issues := compareBundleFiles(inventory.Files, expected); len(issues) > 0 {
		for _, issue := range issues {
			addIssue(&result, "%s", issue)
		}
	}
	result.Valid = result.IsValid()
	return result, nil
}

func copyTree(source string, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source must be a real directory: %s", source)
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
			return fmt.Errorf("bundle source must not contain symlink: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle source must contain regular files only: %s", path)
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
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return errors.Join(inputCloseErr, closeErr)
	})
}

func bundleFiles(root string) ([]BundleFile, int64, error) {
	var files []BundleFile
	var bytes int64
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
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("benchmark bundle contains unsafe file: %s", path)
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
			return fmt.Errorf("benchmark bundle contains non-portable path: %s", rel)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return err
		}
		files = append(files, BundleFile{Path: rel, Size: info.Size(), Digest: digest})
		bytes += info.Size()
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, bytes, err
}

func compareBundleFiles(recorded []BundleFile, actual []BundleFile) []string {
	var issues []string
	if !sort.SliceIsSorted(recorded, func(i, j int) bool { return recorded[i].Path < recorded[j].Path }) {
		issues = append(issues, "benchmark bundle inventory is not sorted")
	}
	if len(recorded) != len(actual) {
		issues = append(issues, fmt.Sprintf("benchmark bundle file count mismatch: recorded %d actual %d", len(recorded), len(actual)))
	}
	recordedByPath := make(map[string]BundleFile, len(recorded))
	for _, file := range recorded {
		if !evidence.IsPortablePath(file.Path) || !evidence.IsDigest(file.Digest) || file.Size < 0 {
			issues = append(issues, "benchmark bundle inventory contains invalid entry: "+file.Path)
			continue
		}
		if _, exists := recordedByPath[file.Path]; exists {
			issues = append(issues, "benchmark bundle inventory contains duplicate path: "+file.Path)
		}
		recordedByPath[file.Path] = file
	}
	for _, file := range actual {
		recorded, ok := recordedByPath[file.Path]
		if !ok {
			issues = append(issues, "benchmark bundle inventory is missing file: "+file.Path)
			continue
		}
		if recorded.Size != file.Size || recorded.Digest != file.Digest {
			issues = append(issues, "benchmark bundle file digest or size mismatch: "+file.Path)
		}
		delete(recordedByPath, file.Path)
	}
	for path := range recordedByPath {
		issues = append(issues, "benchmark bundle inventory references missing file: "+path)
	}
	sort.Strings(issues)
	return issues
}

func RenderBundle(w io.Writer, result BundleResult) error {
	_, err := fmt.Fprintf(w, "Wrote benchmark bundle: %s files=%d bytes=%d linked_runs=%d digest=%s\n", result.Output, result.Files, result.Bytes, result.LinkedRuns, result.Digest)
	return err
}
