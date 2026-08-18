package benchmarkab

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

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasearchive"
)

const (
	BundleSchemaVersion = "pgworkbench.benchmark-ab-bundle/v1"
	BundleArtifactType  = "pgworkbench.benchmark-ab-bundle"
	BundleInventoryName = "benchmark-ab-bundle.json"
)

type BundleFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type BundleInventory struct {
	SchemaVersion string       `json:"schema_version"`
	ArtifactType  string       `json:"artifact_type"`
	RunID         string       `json:"run_id"`
	RunRef        string       `json:"run_ref"`
	BaselineRef   string       `json:"baseline_ref"`
	CandidateRef  string       `json:"candidate_ref"`
	Files         []BundleFile `json:"files"`
}

type BundleResult struct {
	RunID      string `json:"run_id"`
	RunDir     string `json:"run_dir"`
	Output     string `json:"output"`
	RootName   string `json:"root_name"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	Digest     string `json:"digest"`
	Series     int    `json:"series"`
	LinkedRuns int    `json:"linked_runs"`
}

func CreateBundle(root, input, output string, epoch time.Time) (BundleResult, error) {
	return createBundle(root, input, output, epoch, nil)
}

func createBundle(root, input, output string, epoch time.Time, beforeStageVerify func(string) error) (BundleResult, error) {
	verification, err := Verify(root, input)
	if err != nil {
		return BundleResult{}, err
	}
	if !verification.IsValid() || verification.Result == nil {
		return BundleResult{}, fmt.Errorf("counterbalanced benchmark artifact is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	result := *verification.Result
	artifactRoot := inferArtifactRoot(root, verification.Dir)
	if output == "" {
		output = verification.Dir + ".tar.gz"
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	output, err = resolveCounterbalancedBundleOutput(verification.Dir, output, "run artifact")
	if err != nil {
		return BundleResult{}, err
	}
	seriesRefs := []SeriesRef{result.Baseline, result.Candidate}
	seriesSources := make([]struct {
		reference SeriesRef
		source    string
	}, 0, len(seriesRefs))
	linkedRuns := make(map[string]struct{})
	linkedSources := make([]struct {
		runID  string
		ref    string
		source string
	}, 0)
	for _, reference := range seriesRefs {
		seriesSource, err := safeExistingJoin(artifactRoot, reference.Ref)
		if err != nil {
			return BundleResult{}, err
		}
		output, err = resolveCounterbalancedBundleOutput(seriesSource, output, "series "+reference.RunID)
		if err != nil {
			return BundleResult{}, err
		}
		series, err := benchmarkSeriesAt(artifactRoot, seriesSource)
		if err != nil {
			return BundleResult{}, err
		}
		seriesSources = append(seriesSources, struct {
			reference SeriesRef
			source    string
		}{reference: reference, source: seriesSource})
		for _, trial := range series.Trials {
			if _, exists := linkedRuns[trial.RunRef]; exists {
				continue
			}
			linkedRuns[trial.RunRef] = struct{}{}
			source, err := safeExistingJoin(artifactRoot, trial.RunRef)
			if err != nil {
				return BundleResult{}, fmt.Errorf("resolve linked trial %s: %w", trial.RunID, err)
			}
			output, err = resolveCounterbalancedBundleOutput(source, output, "linked trial "+trial.RunID)
			if err != nil {
				return BundleResult{}, err
			}
			linkedSources = append(linkedSources, struct {
				runID  string
				ref    string
				source string
			}{runID: trial.RunID, ref: trial.RunRef, source: source})
		}
	}
	if epoch.IsZero() {
		epoch = time.Unix(0, 0).UTC()
	}
	stage, err := os.MkdirTemp("", ".pgworkbench-ab-bundle-*")
	if err != nil {
		return BundleResult{}, err
	}
	defer os.RemoveAll(stage)

	runRef := filepath.ToSlash(filepath.Join("runs", "benchmark-ab", result.RunID))
	runDestination, err := safeJoin(stage, runRef)
	if err != nil {
		return BundleResult{}, err
	}
	if err := copyTree(verification.Dir, runDestination); err != nil {
		return BundleResult{}, err
	}
	for _, seriesSource := range seriesSources {
		seriesDestination, err := safeJoin(stage, seriesSource.reference.Ref)
		if err != nil {
			return BundleResult{}, err
		}
		if err := copyTree(seriesSource.source, seriesDestination); err != nil {
			return BundleResult{}, err
		}
	}
	for _, linked := range linkedSources {
		destination, err := safeJoin(stage, linked.ref)
		if err != nil {
			return BundleResult{}, err
		}
		if err := copyTree(linked.source, destination); err != nil {
			return BundleResult{}, err
		}
	}
	files, bytes, err := bundleFiles(stage)
	if err != nil {
		return BundleResult{}, err
	}
	inventory := BundleInventory{
		SchemaVersion: BundleSchemaVersion,
		ArtifactType:  BundleArtifactType,
		RunID:         result.RunID,
		RunRef:        runRef,
		BaselineRef:   result.Baseline.Ref,
		CandidateRef:  result.Candidate.Ref,
		Files:         files,
	}
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return BundleResult{}, err
	}
	if err := writeFileAtomic(filepath.Join(stage, BundleInventoryName), append(content, '\n'), 0o644); err != nil {
		return BundleResult{}, err
	}
	if beforeStageVerify != nil {
		if err := beforeStageVerify(stage); err != nil {
			return BundleResult{}, fmt.Errorf("prepare staged counterbalanced benchmark bundle verification: %w", err)
		}
	}
	stagedVerification, err := VerifyBundle(stage, runDestination)
	if err != nil {
		return BundleResult{}, fmt.Errorf("verify staged counterbalanced benchmark bundle: %w", err)
	}
	if !stagedVerification.IsValid() {
		return BundleResult{}, fmt.Errorf("staged counterbalanced benchmark bundle is invalid: %s", strings.Join(stagedVerification.Issues, "; "))
	}
	rootName := "pgworkbench-benchmark-ab-" + result.RunID
	archive, err := releasearchive.Create(stage, output, rootName, epoch)
	if err != nil {
		return BundleResult{}, err
	}
	return BundleResult{
		RunID:      result.RunID,
		RunDir:     verification.Dir,
		Output:     archive.Output,
		RootName:   archive.RootName,
		Files:      archive.Files,
		Bytes:      bytes,
		Digest:     archive.SHA256,
		Series:     2,
		LinkedRuns: len(linkedRuns),
	}, nil
}

func resolveCounterbalancedBundleOutput(source, output, description string) (string, error) {
	resolved, err := pathguard.ResolveOutputOutside(source, output)
	if err != nil {
		return "", fmt.Errorf("resolve counterbalanced bundle output outside %s: %w", description, err)
	}
	return resolved, nil
}

func VerifyBundle(root, input string) (VerifyResult, error) {
	result, err := Verify(root, input)
	if err != nil {
		return result, err
	}
	artifactRoot := inferArtifactRoot(root, result.Dir)
	var inventory BundleInventory
	if err := decodeStrict(filepath.Join(artifactRoot, BundleInventoryName), &inventory); err != nil {
		addIssue(&result, "%s parse failed: %v", BundleInventoryName, err)
		result.Valid = false
		return result, nil
	}
	if inventory.SchemaVersion != BundleSchemaVersion || inventory.ArtifactType != BundleArtifactType {
		addIssue(&result, "unsupported counterbalanced benchmark bundle schema or artifact type")
	}
	if result.Result != nil {
		wantRunRef := filepath.ToSlash(filepath.Join("runs", "benchmark-ab", result.Result.RunID))
		if inventory.RunID != result.Result.RunID || inventory.RunRef != wantRunRef || inventory.BaselineRef != result.Result.Baseline.Ref || inventory.CandidateRef != result.Result.Candidate.Ref {
			addIssue(&result, "counterbalanced benchmark bundle identity mismatch")
		}
	}
	actual, _, err := bundleFiles(artifactRoot)
	if err != nil {
		addIssue(&result, "counterbalanced benchmark bundle inventory failed: %v", err)
	} else {
		for _, issue := range compareBundleFiles(inventory.Files, actual) {
			addIssue(&result, "%s", issue)
		}
	}
	result.Valid = result.IsValid()
	return result, nil
}

func benchmarkSeriesAt(root, path string) (series benchmarkrun.Series, err error) {
	series, err = benchmarkartifact.Load(root, path)
	if err != nil {
		return series, err
	}
	for _, trial := range series.Trials {
		want := filepath.ToSlash(filepath.Join("runs", trial.RunID))
		if trial.RunRef != want || !evidence.IsPortablePath(trial.RunRef) {
			return series, fmt.Errorf("series contains non-canonical linked trial reference")
		}
	}
	return series, nil
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

func safeJoin(base, reference string) (string, error) {
	if !evidence.IsPortablePath(filepath.ToSlash(reference)) {
		return "", fmt.Errorf("bundle path is not portable: %s", reference)
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(base, filepath.FromSlash(reference))
	relative, err := filepath.Rel(base, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bundle path escapes staging root")
	}
	return candidate, nil
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
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("counterbalanced benchmark bundle contains unsafe file: %s", path)
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
			return fmt.Errorf("counterbalanced benchmark bundle contains non-portable path: %s", reference)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return err
		}
		files = append(files, BundleFile{Path: reference, Size: info.Size(), Digest: digest})
		bytes += info.Size()
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, bytes, err
}

func compareBundleFiles(recorded, actual []BundleFile) []string {
	var issues []string
	if !sort.SliceIsSorted(recorded, func(i, j int) bool { return recorded[i].Path < recorded[j].Path }) {
		issues = append(issues, "counterbalanced benchmark bundle inventory is not sorted")
	}
	if len(recorded) != len(actual) {
		issues = append(issues, fmt.Sprintf("counterbalanced benchmark bundle file count mismatch: recorded %d actual %d", len(recorded), len(actual)))
	}
	byPath := make(map[string]BundleFile, len(recorded))
	for _, file := range recorded {
		if !evidence.IsPortablePath(file.Path) || !evidence.IsDigest(file.Digest) || file.Size < 0 {
			issues = append(issues, "counterbalanced benchmark bundle inventory contains invalid entry: "+file.Path)
			continue
		}
		if _, exists := byPath[file.Path]; exists {
			issues = append(issues, "counterbalanced benchmark bundle inventory contains duplicate path: "+file.Path)
		}
		byPath[file.Path] = file
	}
	for _, file := range actual {
		recordedFile, ok := byPath[file.Path]
		if !ok {
			issues = append(issues, "counterbalanced benchmark bundle inventory is missing file: "+file.Path)
			continue
		}
		if recordedFile.Size != file.Size || recordedFile.Digest != file.Digest {
			issues = append(issues, "counterbalanced benchmark bundle file digest or size mismatch: "+file.Path)
		}
		delete(byPath, file.Path)
	}
	for path := range byPath {
		issues = append(issues, "counterbalanced benchmark bundle inventory references missing file: "+path)
	}
	return uniqueSorted(issues)
}

func RenderBundle(w io.Writer, result BundleResult) error {
	_, err := fmt.Fprintf(w, "Wrote counterbalanced benchmark bundle: %s files=%d bytes=%d series=%d linked_runs=%d digest=%s\n", result.Output, result.Files, result.Bytes, result.Series, result.LinkedRuns, result.Digest)
	return err
}
