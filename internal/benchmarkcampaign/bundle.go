package benchmarkcampaign

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
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasearchive"
)

const (
	BundleSchemaVersion = "pgworkbench.benchmark-campaign-bundle/v1"
	BundleArtifactType  = "pgworkbench.benchmark-campaign-bundle"
	BundleInventoryName = "benchmark-campaign-bundle.json"
)

type BundleFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type BundleInventory struct {
	SchemaVersion string       `json:"schema_version"`
	ArtifactType  string       `json:"artifact_type"`
	CampaignID    string       `json:"campaign_id"`
	CampaignRef   string       `json:"campaign_ref"`
	SeriesRefs    []string     `json:"series_refs"`
	Files         []BundleFile `json:"files"`
}

type BundleResult struct {
	CampaignID  string `json:"campaign_id"`
	CampaignDir string `json:"campaign_dir"`
	Output      string `json:"output"`
	RootName    string `json:"root_name"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Digest      string `json:"digest"`
	Series      int    `json:"series"`
	LinkedRuns  int    `json:"linked_runs"`
}

func CreateBundle(root, input, output string, epoch time.Time) (BundleResult, error) {
	return createBundle(root, input, output, epoch, nil)
}

func createBundle(root, input, output string, epoch time.Time, beforeStageVerify func(string) error) (BundleResult, error) {
	verification, err := Verify(root, input)
	if err != nil {
		return BundleResult{}, err
	}
	if !verification.IsValid() || verification.Campaign == nil {
		return BundleResult{}, fmt.Errorf("benchmark campaign artifact is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	campaign := *verification.Campaign
	artifactRoot := inferArtifactRoot(root, verification.Dir)
	if output == "" {
		output = verification.Dir + ".tar.gz"
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return BundleResult{}, err
	}
	output, err = pathguard.ResolveOutputOutside(verification.Dir, output)
	if err != nil {
		if errors.Is(err, pathguard.ErrOutputWithinSource) {
			return BundleResult{}, fmt.Errorf("benchmark campaign bundle output must be outside the immutable campaign directory")
		}
		return BundleResult{}, fmt.Errorf("resolve benchmark campaign bundle output: %w", err)
	}
	if epoch.IsZero() {
		epoch = time.Unix(0, 0).UTC()
	}
	stage, err := os.MkdirTemp("", ".pgworkbench-benchmark-campaign-bundle-*")
	if err != nil {
		return BundleResult{}, err
	}
	defer os.RemoveAll(stage)

	campaignRef := filepath.ToSlash(filepath.Join("runs", "benchmark-campaign", campaign.CampaignID))
	campaignDestination, err := safeJoin(stage, campaignRef)
	if err != nil {
		return BundleResult{}, err
	}
	if err := copyTree(verification.Dir, campaignDestination); err != nil {
		return BundleResult{}, err
	}
	seriesRefs := make([]string, 0, len(campaign.Executions))
	linkedRuns := make(map[string]struct{})
	for _, execution := range campaign.Executions {
		if execution.EvidenceStatus != "verified" {
			continue
		}
		seriesRefs = append(seriesRefs, execution.SeriesRef)
		seriesSource, err := safeExistingJoin(artifactRoot, execution.SeriesRef)
		if err != nil {
			return BundleResult{}, fmt.Errorf("resolve series %s: %w", execution.SeriesRunID, err)
		}
		seriesDestination, err := safeJoin(stage, execution.SeriesRef)
		if err != nil {
			return BundleResult{}, err
		}
		if err := copyTree(seriesSource, seriesDestination); err != nil {
			return BundleResult{}, fmt.Errorf("copy series %s: %w", execution.SeriesRunID, err)
		}
		series, err := benchmarkartifact.Load(artifactRoot, seriesSource)
		if err != nil {
			return BundleResult{}, err
		}
		for _, trial := range series.Trials {
			if _, exists := linkedRuns[trial.RunRef]; exists {
				continue
			}
			linkedRuns[trial.RunRef] = struct{}{}
			source, err := safeExistingJoin(artifactRoot, trial.RunRef)
			if err != nil {
				return BundleResult{}, fmt.Errorf("resolve linked trial run %s: %w", trial.RunID, err)
			}
			destination, err := safeJoin(stage, trial.RunRef)
			if err != nil {
				return BundleResult{}, err
			}
			if err := copyTree(source, destination); err != nil {
				return BundleResult{}, fmt.Errorf("copy linked trial run %s: %w", trial.RunID, err)
			}
		}
	}
	files, bytes, err := bundleFiles(stage)
	if err != nil {
		return BundleResult{}, err
	}
	inventory := BundleInventory{
		SchemaVersion: BundleSchemaVersion,
		ArtifactType:  BundleArtifactType,
		CampaignID:    campaign.CampaignID,
		CampaignRef:   campaignRef,
		SeriesRefs:    seriesRefs,
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
			return BundleResult{}, fmt.Errorf("prepare staged benchmark campaign bundle verification: %w", err)
		}
	}
	stagedVerification, err := VerifyBundle(stage, campaignDestination)
	if err != nil {
		return BundleResult{}, fmt.Errorf("verify staged benchmark campaign bundle: %w", err)
	}
	if !stagedVerification.IsValid() {
		return BundleResult{}, fmt.Errorf("staged benchmark campaign bundle is invalid: %s", strings.Join(stagedVerification.Issues, "; "))
	}
	archive, err := releasearchive.Create(stage, output, "pgworkbench-benchmark-campaign-"+campaign.CampaignID, epoch)
	if err != nil {
		return BundleResult{}, err
	}
	return BundleResult{
		CampaignID:  campaign.CampaignID,
		CampaignDir: verification.Dir,
		Output:      archive.Output,
		RootName:    archive.RootName,
		Files:       archive.Files,
		Bytes:       bytes,
		Digest:      archive.SHA256,
		Series:      len(seriesRefs),
		LinkedRuns:  len(linkedRuns),
	}, nil
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
		addIssue(&result, "unsupported benchmark campaign bundle schema or artifact type")
	}
	if result.Campaign != nil {
		wantRef := filepath.ToSlash(filepath.Join("runs", "benchmark-campaign", result.Campaign.CampaignID))
		wantSeries := make([]string, 0, len(result.Campaign.Executions))
		for _, execution := range result.Campaign.Executions {
			if execution.EvidenceStatus == "verified" {
				wantSeries = append(wantSeries, execution.SeriesRef)
			}
		}
		if inventory.CampaignID != result.Campaign.CampaignID || inventory.CampaignRef != wantRef || !equalStrings(inventory.SeriesRefs, wantSeries) {
			addIssue(&result, "benchmark campaign bundle identity mismatch")
		}
	}
	actual, _, filesErr := bundleFiles(artifactRoot)
	if filesErr != nil {
		addIssue(&result, "benchmark campaign bundle inventory failed: %v", filesErr)
	} else {
		for _, issue := range compareBundleFiles(inventory.Files, actual) {
			addIssue(&result, "%s", issue)
		}
	}
	result.Valid = result.IsValid()
	return result, nil
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
			return fmt.Errorf("benchmark campaign bundle contains unsafe file: %s", path)
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
			return fmt.Errorf("benchmark campaign bundle contains non-portable path: %s", reference)
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
		issues = append(issues, "benchmark campaign bundle inventory is not sorted")
	}
	if len(recorded) != len(actual) {
		issues = append(issues, fmt.Sprintf("benchmark campaign bundle file count mismatch: recorded %d actual %d", len(recorded), len(actual)))
	}
	byPath := make(map[string]BundleFile, len(recorded))
	for _, file := range recorded {
		if !evidence.IsPortablePath(file.Path) || !evidence.IsDigest(file.Digest) || file.Size < 0 {
			issues = append(issues, "benchmark campaign bundle inventory contains invalid entry: "+file.Path)
			continue
		}
		if _, exists := byPath[file.Path]; exists {
			issues = append(issues, "benchmark campaign bundle inventory contains duplicate path: "+file.Path)
		}
		byPath[file.Path] = file
	}
	for _, file := range actual {
		recordedFile, ok := byPath[file.Path]
		if !ok {
			issues = append(issues, "benchmark campaign bundle inventory is missing file: "+file.Path)
			continue
		}
		if recordedFile.Size != file.Size || recordedFile.Digest != file.Digest {
			issues = append(issues, "benchmark campaign bundle file digest or size mismatch: "+file.Path)
		}
		delete(byPath, file.Path)
	}
	for path := range byPath {
		issues = append(issues, "benchmark campaign bundle inventory references missing file: "+path)
	}
	return uniqueSorted(issues)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func RenderBundle(w io.Writer, result BundleResult) error {
	_, err := fmt.Fprintf(w, "Wrote benchmark campaign bundle: %s files=%d bytes=%d series=%d linked_runs=%d digest=%s\n", result.Output, result.Files, result.Bytes, result.Series, result.LinkedRuns, result.Digest)
	return err
}
