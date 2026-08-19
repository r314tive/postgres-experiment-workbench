package releaseevidence

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasearchive"
	"github.com/r314tive/postgres-experiment-workbench/internal/strictjson"
)

const (
	BundleSchemaVersion = "pgworkbench.release-evidence-bundle/v1"
	BundleArtifactType  = "pgworkbench.release-evidence-bundle"
	BundleInventoryName = "release-evidence-bundle.json"
	BundleRootName      = "pgworkbench-release-evidence"

	bundleFileMode              = uint32(0o644)
	maxBundleRevisions          = int64(256)
	maxBundleEvidenceBytes      = int64(64 << 20)
	maxBundleInventoryBytes     = int64(64 << 10)
	maxBundleArchiveBytes       = int64(72 << 20)
	maxBundleDirectoryEntries   = int(maxBundleRevisions + 1024)
	canonicalBundleArchiveEpoch = int64(0)
)

// BundleFile binds one exact canonical index revision in a closed evidence
// bundle. Mode is represented as an integer so relocation checks do not depend
// on the verifier process umask.
type BundleFile struct {
	Path     string `json:"path"`
	Revision int64  `json:"revision"`
	Size     int64  `json:"size"`
	Digest   string `json:"digest"`
	Mode     uint32 `json:"mode"`
}

// BundleOutcome is a compact, independently reproducible view of the head
// index. It is descriptive only: a valid bundle can and commonly will retain
// a NO-GO decision and non-authorizing assurance.
type BundleOutcome struct {
	Status                string `json:"status"`
	Decision              string `json:"decision"`
	ReadinessStatus       string `json:"readiness_status"`
	ReadinessDecision     string `json:"readiness_decision"`
	AssuranceStatus       string `json:"assurance_status"`
	AuthorizationEligible bool   `json:"authorization_eligible"`
}

// BundleInventory closes the exact set of project-authored index revisions.
// It intentionally excludes itself and never embeds or rewrites the remote
// evidence objects named by durable references inside those indexes.
type BundleInventory struct {
	SchemaVersion  string        `json:"schema_version"`
	ArtifactType   string        `json:"artifact_type"`
	Candidate      Candidate     `json:"candidate"`
	HeadIndex      string        `json:"head_index"`
	HeadRevision   int64         `json:"head_revision"`
	HeadDigest     string        `json:"head_digest"`
	FileCount      int64         `json:"file_count"`
	TotalSizeBytes int64         `json:"total_size_bytes"`
	TreeDigest     string        `json:"tree_digest"`
	Outcome        BundleOutcome `json:"outcome"`
	Files          []BundleFile  `json:"files"`
}

type BundleCreateResult struct {
	Output            string       `json:"output"`
	Digest            string       `json:"digest"`
	RootName          string       `json:"root_name"`
	ArchiveFiles      int          `json:"archive_files"`
	ArchiveBytes      int64        `json:"archive_bytes"`
	Records           int64        `json:"records"`
	EvidenceBytes     int64        `json:"evidence_bytes"`
	Candidate         Candidate    `json:"candidate"`
	HeadIndex         string       `json:"head_index"`
	HeadRevision      int64        `json:"head_revision"`
	HeadDigest        string       `json:"head_digest"`
	TreeDigest        string       `json:"tree_digest"`
	IndexVerification Verification `json:"index_verification"`
}

// BundleVerification describes closure and lineage independently of the
// bundle's final release decision. Valid remains true for a consistent open,
// failed, or unqualified NO-GO chain.
type BundleVerification struct {
	Root              string       `json:"root"`
	Valid             bool         `json:"valid"`
	Candidate         Candidate    `json:"candidate"`
	HeadIndex         string       `json:"head_index"`
	HeadRevision      int64        `json:"head_revision"`
	HeadDigest        string       `json:"head_digest"`
	Records           int64        `json:"records"`
	EvidenceBytes     int64        `json:"evidence_bytes"`
	TreeDigest        string       `json:"tree_digest"`
	IndexVerification Verification `json:"index_verification"`
	Issues            []string     `json:"issues"`
}

type bundleSnapshot struct {
	file         BundleFile
	content      []byte
	index        Index
	verification Verification
	sourceInfo   os.FileInfo
}

type bundleFilePin struct {
	description string
	file        *os.File
}

type bundleCreateHooks struct {
	beforeStageVerify   func(string) error
	afterStageVerify    func(string) error
	beforeSourceConfirm func() error
	beforeOutputPrepare func() error
	beforeOutputPublish func() error
	afterOutputPublish  func() error
	removeStage         func(string) error
	removeArchiveStage  func(string) error
}

type bundleVerifyHooks struct {
	beforeFinalConfirm func() error
	afterEntryConfirm  func() error
}

// BundleCommittedError reports that the exclusive archive destination was
// created but its final inode, pathname, cleanup, or directory-durability
// confirmation failed. Result is retained so callers can report the expected
// immutable identity and must not retry blindly.
type BundleCommittedError struct {
	Result BundleCreateResult
	Err    error
}

func (err *BundleCommittedError) Error() string {
	return fmt.Sprintf("release evidence bundle publication reached committed state for requested output %s with expected digest %s, but final confirmation failed: %v", err.Result.Output, err.Result.Digest, err.Err)
}

func (err *BundleCommittedError) Unwrap() error {
	return err.Err
}

// CreateBundle snapshots the complete adjacent lineage ending at headIndex,
// verifies the staged closed representation, and publishes a deterministic
// archive without replacing an existing destination.
func CreateBundle(headIndex, output string) (BundleCreateResult, error) {
	return createBundle(headIndex, output, bundleCreateHooks{})
}

func createBundle(headIndex, output string, hooks bundleCreateHooks) (returned BundleCreateResult, returnErr error) {
	if headIndex == "" || output == "" {
		return BundleCreateResult{}, fmt.Errorf("head index and bundle output are required")
	}
	headAbs, err := filepath.Abs(headIndex)
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("resolve evidence bundle head index: %w", err)
	}
	headName := filepath.Base(headAbs)
	headRevision, err := parseBundleIndexName(headName)
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("evidence bundle head index: %w", err)
	}
	if headRevision >= maxBundleRevisions {
		return BundleCreateResult{}, fmt.Errorf("evidence bundle contains more than %d revisions", maxBundleRevisions)
	}

	sourceParent := filepath.Dir(headAbs)
	resolvedOutput, err := pathguard.ResolveOutputOutside(sourceParent, output)
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("resolve evidence bundle output outside source chain: %w", err)
	}
	directory, err := openPinnedBundleDirectory(sourceParent)
	if err != nil {
		return BundleCreateResult{}, err
	}
	defer directory.Close()

	wantNames := bundleIndexNames(headRevision)
	if err := verifyBundleSourceNames(directory, wantNames); err != nil {
		return BundleCreateResult{}, err
	}
	snapshots, sourcePins, err := readBundleChain(directory, headRevision)
	if err != nil {
		return BundleCreateResult{}, err
	}
	defer func() {
		closeErr := closeBundleFilePins(sourcePins)
		if closeErr == nil {
			return
		}
		closeErr = fmt.Errorf("close pinned release evidence source chain: %w", closeErr)
		if returned.Digest == "" {
			returnErr = errors.Join(returnErr, closeErr)
			return
		}
		var committed *BundleCommittedError
		if errors.As(returnErr, &committed) {
			returnErr = &BundleCommittedError{Result: returned, Err: errors.Join(committed.Err, closeErr)}
			return
		}
		returnErr = &BundleCommittedError{Result: returned, Err: errors.Join(returnErr, closeErr)}
	}()

	stage, err := os.MkdirTemp("", ".pgworkbench-release-evidence-bundle-*")
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("create evidence bundle stage: %w", err)
	}
	stagePresent := true
	defer func() {
		if stagePresent {
			_ = os.RemoveAll(stage)
		}
	}()

	files := make([]BundleFile, 0, len(snapshots))
	var totalSize int64
	for _, snapshot := range snapshots {
		if err := writeBundleFile(filepath.Join(stage, snapshot.file.Path), snapshot.content); err != nil {
			return BundleCreateResult{}, err
		}
		files = append(files, snapshot.file)
		totalSize += snapshot.file.Size
	}
	treeDigest, err := bundleTreeDigest(files)
	if err != nil {
		return BundleCreateResult{}, err
	}
	head := snapshots[len(snapshots)-1]
	inventory := BundleInventory{
		SchemaVersion:  BundleSchemaVersion,
		ArtifactType:   BundleArtifactType,
		Candidate:      head.index.Candidate,
		HeadIndex:      head.file.Path,
		HeadRevision:   head.file.Revision,
		HeadDigest:     head.file.Digest,
		FileCount:      int64(len(files)),
		TotalSizeBytes: totalSize,
		TreeDigest:     treeDigest,
		Outcome:        outcomeFromVerification(head.verification),
		Files:          files,
	}
	inventoryContent, err := encodeBundleInventory(inventory)
	if err != nil {
		return BundleCreateResult{}, err
	}
	if int64(len(inventoryContent)) > maxBundleInventoryBytes {
		return BundleCreateResult{}, fmt.Errorf("evidence bundle inventory exceeds %d bytes", maxBundleInventoryBytes)
	}
	if err := writeBundleFile(filepath.Join(stage, BundleInventoryName), inventoryContent); err != nil {
		return BundleCreateResult{}, err
	}

	if hooks.beforeStageVerify != nil {
		if err := hooks.beforeStageVerify(stage); err != nil {
			return BundleCreateResult{}, fmt.Errorf("prepare staged evidence bundle verification: %w", err)
		}
	}
	staged, err := VerifyBundle(stage)
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("verify staged evidence bundle: %w", err)
	}
	if !staged.Valid {
		return BundleCreateResult{}, fmt.Errorf("staged evidence bundle is invalid: %s", joinIssues(staged.Issues))
	}
	if hooks.afterStageVerify != nil {
		if err := hooks.afterStageVerify(stage); err != nil {
			return BundleCreateResult{}, fmt.Errorf("prepare verified evidence bundle archive: %w", err)
		}
	}
	if hooks.beforeSourceConfirm != nil {
		if err := hooks.beforeSourceConfirm(); err != nil {
			return BundleCreateResult{}, fmt.Errorf("prepare release evidence source confirmation: %w", err)
		}
	}
	if err := confirmBundleSource(directory, sourceParent, snapshots, wantNames); err != nil {
		return BundleCreateResult{}, err
	}

	archiveStage, err := os.MkdirTemp("", ".pgworkbench-release-evidence-archive-*")
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("create evidence bundle archive stage: %w", err)
	}
	archiveStagePresent := true
	defer func() {
		if archiveStagePresent {
			_ = os.RemoveAll(archiveStage)
		}
	}()
	archivePath := filepath.Join(archiveStage, "bundle.tar.gz")
	archive, err := releasearchive.Create(
		stage,
		archivePath,
		BundleRootName,
		time.Unix(canonicalBundleArchiveEpoch, 0).UTC(),
	)
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("create staged release evidence bundle archive: %w", err)
	}
	archiveBytes, err := readBoundedBundleArchive(archivePath, archive.Bytes)
	if err != nil {
		return BundleCreateResult{}, err
	}
	if digestExactBytes(archiveBytes) != archive.SHA256 {
		return BundleCreateResult{}, fmt.Errorf("staged release evidence bundle archive digest changed after creation")
	}
	if err := verifyExactBundleArchive(archiveBytes, inventoryContent, snapshots); err != nil {
		return BundleCreateResult{}, fmt.Errorf("verify exact staged release evidence bundle archive: %w", err)
	}

	removeStage := os.RemoveAll
	if hooks.removeStage != nil {
		removeStage = hooks.removeStage
	}
	if err := removeStage(stage); err != nil {
		return BundleCreateResult{}, fmt.Errorf("remove verified evidence bundle stage before publication: %w", err)
	}
	stagePresent = false
	removeArchiveStage := os.RemoveAll
	if hooks.removeArchiveStage != nil {
		removeArchiveStage = hooks.removeArchiveStage
	}
	if err := removeArchiveStage(archiveStage); err != nil {
		return BundleCreateResult{}, fmt.Errorf("remove verified evidence archive stage before publication: %w", err)
	}
	archiveStagePresent = false
	if hooks.beforeOutputPrepare != nil {
		if err := hooks.beforeOutputPrepare(); err != nil {
			return BundleCreateResult{}, fmt.Errorf("prepare evidence bundle output binding: %w", err)
		}
	}

	finalOutput, err := pathguard.PrepareNewOutputOutside(sourceParent, resolvedOutput, 0o755)
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("prepare evidence bundle output outside source chain: %w", err)
	}
	if finalOutput != resolvedOutput {
		return BundleCreateResult{}, fmt.Errorf("evidence bundle output parent changed after source verification")
	}
	outputDirectory, err := openDirectoryPath(filepath.Dir(finalOutput))
	if err != nil {
		return BundleCreateResult{}, fmt.Errorf("pin evidence bundle output directory: %w", err)
	}
	defer outputDirectory.Close()
	if err := confirmPinnedDirectoryPath(outputDirectory, filepath.Dir(finalOutput), "evidence bundle output directory"); err != nil {
		return BundleCreateResult{}, err
	}
	result := BundleCreateResult{
		Output:            archive.Output,
		Digest:            archive.SHA256,
		RootName:          archive.RootName,
		ArchiveFiles:      archive.Files,
		ArchiveBytes:      archive.Bytes,
		Records:           int64(len(files)),
		EvidenceBytes:     totalSize,
		Candidate:         head.index.Candidate,
		HeadIndex:         head.file.Path,
		HeadRevision:      head.file.Revision,
		HeadDigest:        head.file.Digest,
		TreeDigest:        treeDigest,
		IndexVerification: head.verification,
	}
	result.Output = finalOutput
	if hooks.beforeOutputPublish != nil {
		if err := hooks.beforeOutputPublish(); err != nil {
			return BundleCreateResult{}, fmt.Errorf("prepare evidence bundle publication: %w", err)
		}
	}
	if err := confirmBundleSource(directory, sourceParent, snapshots, wantNames); err != nil {
		return BundleCreateResult{}, err
	}
	if err := confirmBundlePublicationBoundary(directory, outputDirectory, sourceParent, finalOutput); err != nil {
		return BundleCreateResult{}, err
	}
	published, err := publishBundleArchiveAt(outputDirectory, filepath.Base(finalOutput), finalOutput, archiveBytes, result, hooks.afterOutputPublish)
	if err != nil {
		return published, err
	}
	postSourceErr := confirmBundleSource(directory, sourceParent, snapshots, wantNames)
	postBoundaryErr := confirmBundlePublicationBoundary(directory, outputDirectory, sourceParent, finalOutput)
	if err := errors.Join(postSourceErr, postBoundaryErr); err != nil {
		return published, &BundleCommittedError{Result: published, Err: err}
	}
	return published, nil
}

// VerifyBundle independently verifies an extracted bundle root. It performs no
// network access and treats the durable refs inside index bytes as immutable
// recorded claims rather than local paths to resolve.
func VerifyBundle(root string) (BundleVerification, error) {
	return verifyBundle(root, bundleVerifyHooks{})
}

func verifyBundle(root string, hooks bundleVerifyHooks) (result BundleVerification, returnErr error) {
	result = BundleVerification{Issues: []string{}}
	pins := make([]bundleFilePin, 0)
	defer func() {
		closeErr := closeBundleFilePins(pins)
		if closeErr == nil {
			return
		}
		closeErr = fmt.Errorf("close pinned release evidence bundle entries: %w", closeErr)
		if returnErr != nil {
			returnErr = errors.Join(returnErr, closeErr)
			return
		}
		result.Issues = append(result.Issues, closeErr.Error())
		result = finalizeBundleVerification(result)
	}()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return result, fmt.Errorf("resolve evidence bundle root: %w", err)
	}
	result.Root = absRoot
	rootInfo, err := os.Lstat(absRoot)
	if err != nil {
		return result, fmt.Errorf("inspect evidence bundle root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return result, fmt.Errorf("evidence bundle root must be a real non-symlink directory")
	}
	directory, err := openDirectoryPath(absRoot)
	if err != nil {
		return result, fmt.Errorf("open evidence bundle root: %w", err)
	}
	defer directory.Close()
	openedInfo, err := directory.Stat()
	if err != nil {
		return result, fmt.Errorf("inspect opened evidence bundle root: %w", err)
	}
	if !openedInfo.IsDir() || !os.SameFile(rootInfo, openedInfo) {
		return result, fmt.Errorf("evidence bundle root changed while it was being opened")
	}

	inventoryFile, err := openReadOnlyEntryAt(directory, BundleInventoryName, "release evidence bundle inventory")
	if err != nil {
		result.Issues = append(result.Issues, err.Error())
		return finalizeBundleVerification(result), nil
	}
	pins = append(pins, bundleFilePin{description: BundleInventoryName, file: inventoryFile})
	inventoryInfo, inspectErr := inventoryFile.Stat()
	if inspectErr != nil {
		return result, fmt.Errorf("inspect evidence bundle inventory: %w", inspectErr)
	}
	if inventoryInfo.Mode() != os.FileMode(bundleFileMode) || !inventoryInfo.Mode().IsRegular() || bundleFileHasMultipleLinks(inventoryInfo) {
		result.Issues = append(result.Issues, "release evidence bundle inventory must be one regular non-hardlinked file with mode 0644")
	}
	inventoryContent, readErr := strictjson.ReadOpenedFile(inventoryFile, maxBundleInventoryBytes)
	if readErr != nil {
		result.Issues = append(result.Issues, readErr.Error())
		return finalizeBundleVerification(result), nil
	}
	var inventory BundleInventory
	if err := strictjson.Parse(inventoryContent, &inventory); err != nil {
		result.Issues = append(result.Issues, "parse release evidence bundle inventory: "+err.Error())
		return finalizeBundleVerification(result), nil
	}
	canonicalInventory, err := encodeBundleInventory(inventory)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(canonicalInventory, inventoryContent) {
		result.Issues = append(result.Issues, "release evidence bundle inventory is not in canonical project encoding")
	}
	result.Issues = append(result.Issues, validateBundleInventory(inventory)...)
	if len(result.Issues) != 0 {
		return finalizeBundleVerification(result), nil
	}

	actualEntries, err := readBundleDirectoryEntries(directory)
	if err != nil {
		result.Issues = append(result.Issues, err.Error())
		return finalizeBundleVerification(result), nil
	}
	expected := make(map[string]struct{}, len(inventory.Files)+1)
	expected[BundleInventoryName] = struct{}{}
	for _, file := range inventory.Files {
		expected[file.Path] = struct{}{}
	}
	for name, info := range actualEntries {
		if _, ok := expected[name]; !ok {
			result.Issues = append(result.Issues, "release evidence bundle contains unexpected entry: "+name)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			result.Issues = append(result.Issues, "release evidence bundle entry is not a regular non-symlink file: "+name)
		}
	}
	for name := range expected {
		if _, ok := actualEntries[name]; !ok {
			result.Issues = append(result.Issues, "release evidence bundle is missing entry: "+name)
		}
	}
	if len(result.Issues) != 0 {
		return finalizeBundleVerification(result), nil
	}

	snapshots := make([]bundleSnapshot, 0, len(inventory.Files))
	var actualTotal int64
	for _, want := range inventory.Files {
		file, err := openReadOnlyEntryAt(directory, want.Path, "release evidence index")
		if err != nil {
			result.Issues = append(result.Issues, want.Path+": "+err.Error())
			continue
		}
		pins = append(pins, bundleFilePin{description: want.Path, file: file})
		info, inspectErr := file.Stat()
		if inspectErr != nil {
			result.Issues = append(result.Issues, want.Path+": "+inspectErr.Error())
			continue
		}
		if !info.Mode().IsRegular() || info.Mode() != os.FileMode(want.Mode) || bundleFileHasMultipleLinks(info) {
			result.Issues = append(result.Issues, want.Path+": mode, type, or hardlink count does not match the closed inventory")
		}
		content, readErr := strictjson.ReadOpenedFile(file, maxIndexBytes)
		if readErr != nil {
			result.Issues = append(result.Issues, want.Path+": "+readErr.Error())
			continue
		}
		digest := digestExactBytes(content)
		if int64(len(content)) != want.Size {
			result.Issues = append(result.Issues, want.Path+": size does not match the closed inventory")
		}
		if digest != want.Digest {
			result.Issues = append(result.Issues, want.Path+": digest does not match the closed inventory")
		}
		index, parseErr := Parse(content)
		if parseErr != nil {
			result.Issues = append(result.Issues, want.Path+": parse release evidence index: "+parseErr.Error())
			continue
		}
		canonicalIndex, encodeErr := encodeIndex(index)
		if encodeErr != nil {
			return result, encodeErr
		}
		if !bytes.Equal(canonicalIndex, content) {
			result.Issues = append(result.Issues, want.Path+": release evidence index is not in canonical project encoding")
		}
		verification := Verify(index)
		if !verification.Valid {
			result.Issues = append(result.Issues, want.Path+": invalid release evidence index: "+joinIssues(verification.Issues))
		}
		snapshots = append(snapshots, bundleSnapshot{file: want, content: content, index: index, verification: verification, sourceInfo: info})
		actualTotal += int64(len(content))
	}
	if len(snapshots) == len(inventory.Files) {
		result.Issues = append(result.Issues, verifyBundleChain(snapshots)...)
	}
	if actualTotal != inventory.TotalSizeBytes {
		result.Issues = append(result.Issues, "release evidence bundle total size does not match the closed inventory")
	}
	if len(snapshots) == len(inventory.Files) {
		head := snapshots[len(snapshots)-1]
		if inventory.Candidate != head.index.Candidate || inventory.HeadIndex != head.file.Path || inventory.HeadRevision != head.file.Revision || inventory.HeadDigest != head.file.Digest {
			result.Issues = append(result.Issues, "release evidence bundle head identity does not match the verified chain")
		}
		if inventory.Outcome != outcomeFromVerification(head.verification) {
			result.Issues = append(result.Issues, "release evidence bundle outcome does not match independent head verification")
		}
		result.Candidate = head.index.Candidate
		result.HeadIndex = head.file.Path
		result.HeadRevision = head.file.Revision
		result.HeadDigest = head.file.Digest
		result.IndexVerification = head.verification
	}
	result.Records = int64(len(snapshots))
	result.EvidenceBytes = actualTotal
	result.TreeDigest = inventory.TreeDigest
	if hooks.beforeFinalConfirm != nil {
		if err := hooks.beforeFinalConfirm(); err != nil {
			return result, fmt.Errorf("prepare evidence bundle final confirmation: %w", err)
		}
	}
	finalEntries, err := readBundleDirectoryEntries(directory)
	if err != nil {
		result.Issues = append(result.Issues, err.Error())
	} else {
		for name := range finalEntries {
			if _, ok := expected[name]; !ok {
				result.Issues = append(result.Issues, "release evidence bundle contains unexpected entry after verification: "+name)
			}
		}
		for name := range expected {
			if _, ok := finalEntries[name]; !ok {
				result.Issues = append(result.Issues, "release evidence bundle is missing entry after verification: "+name)
			}
		}
	}
	if err := confirmBundleEntry(directory, BundleInventoryName, inventoryInfo, inventoryContent, maxBundleInventoryBytes, "release evidence bundle inventory"); err != nil {
		result.Issues = append(result.Issues, err.Error())
	}
	for _, snapshot := range snapshots {
		if err := confirmBundleEntry(directory, snapshot.file.Path, snapshot.sourceInfo, snapshot.content, maxIndexBytes, "release evidence index"); err != nil {
			result.Issues = append(result.Issues, err.Error())
		}
	}
	if hooks.afterEntryConfirm != nil {
		if err := hooks.afterEntryConfirm(); err != nil {
			return result, fmt.Errorf("prepare evidence bundle terminal entry-set confirmation: %w", err)
		}
	}
	terminalEntries, err := readBundleDirectoryEntries(directory)
	if err != nil {
		result.Issues = append(result.Issues, err.Error())
	} else {
		for name := range terminalEntries {
			if _, ok := expected[name]; !ok {
				result.Issues = append(result.Issues, "release evidence bundle contains unexpected entry at terminal confirmation: "+name)
			}
		}
		for name := range expected {
			if _, ok := terminalEntries[name]; !ok {
				result.Issues = append(result.Issues, "release evidence bundle is missing entry at terminal confirmation: "+name)
			}
		}
	}
	if err := confirmBundleEntry(directory, BundleInventoryName, inventoryInfo, inventoryContent, maxBundleInventoryBytes, "terminal release evidence bundle inventory"); err != nil {
		result.Issues = append(result.Issues, err.Error())
	}
	for _, snapshot := range snapshots {
		if err := confirmBundleEntry(directory, snapshot.file.Path, snapshot.sourceInfo, snapshot.content, maxIndexBytes, "terminal release evidence index"); err != nil {
			result.Issues = append(result.Issues, err.Error())
		}
	}
	finalRootInfo, err := os.Lstat(absRoot)
	if err != nil {
		result.Issues = append(result.Issues, "inspect final evidence bundle root: "+err.Error())
	} else if finalRootInfo.Mode()&os.ModeSymlink != 0 || !finalRootInfo.IsDir() || !os.SameFile(rootInfo, finalRootInfo) {
		result.Issues = append(result.Issues, "evidence bundle root must remain the original real non-symlink directory")
	}
	if err := confirmPinnedDirectoryPath(directory, absRoot, "evidence bundle root"); err != nil {
		result.Issues = append(result.Issues, err.Error())
	}
	return finalizeBundleVerification(result), nil
}

func openPinnedBundleDirectory(path string) (*os.File, error) {
	directory, err := openDirectoryPath(path)
	if err != nil {
		return nil, fmt.Errorf("open release evidence chain directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = directory.Close()
		}
	}()
	info, err := directory.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect release evidence chain directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("release evidence chain parent is not a directory")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve release evidence chain directory: %w", err)
	}
	resolved, err := openDirectoryPath(canonical)
	if err != nil {
		return nil, fmt.Errorf("open resolved release evidence chain directory: %w", err)
	}
	resolvedInfo, inspectErr := resolved.Stat()
	closeErr := resolved.Close()
	if inspectErr != nil || closeErr != nil {
		return nil, errors.Join(inspectErr, closeErr)
	}
	if !resolvedInfo.IsDir() || !os.SameFile(info, resolvedInfo) {
		return nil, fmt.Errorf("resolved release evidence chain directory changed while it was being pinned")
	}
	keep = true
	return directory, nil
}

func readBundleChain(directory *os.File, headRevision int64) (snapshots []bundleSnapshot, pins []bundleFilePin, returnErr error) {
	snapshots = make([]bundleSnapshot, 0, headRevision+1)
	pins = make([]bundleFilePin, 0, headRevision+1)
	defer func() {
		if returnErr == nil {
			return
		}
		closeErr := closeBundleFilePins(pins)
		snapshots = nil
		pins = nil
		if closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close release evidence source pins after failure: %w", closeErr))
		}
	}()
	var total int64
	for revision := int64(0); revision <= headRevision; revision++ {
		name := bundleIndexName(revision)
		file, err := openReadOnlyEntryAt(directory, name, "release evidence index")
		if err != nil {
			return snapshots, pins, err
		}
		pins = append(pins, bundleFilePin{description: name, file: file})
		info, inspectErr := file.Stat()
		if inspectErr != nil {
			return snapshots, pins, fmt.Errorf("inspect %s: %w", name, inspectErr)
		}
		if !info.Mode().IsRegular() || info.Mode() != os.FileMode(bundleFileMode) || bundleFileHasMultipleLinks(info) {
			return snapshots, pins, fmt.Errorf("%s must be one regular non-hardlinked file with mode 0644", name)
		}
		content, readErr := strictjson.ReadOpenedFile(file, maxIndexBytes)
		if readErr != nil {
			return snapshots, pins, fmt.Errorf("read %s: %w", name, readErr)
		}
		total += int64(len(content))
		if total > maxBundleEvidenceBytes {
			return snapshots, pins, fmt.Errorf("release evidence chain exceeds %d bytes", maxBundleEvidenceBytes)
		}
		index, err := Parse(content)
		if err != nil {
			return snapshots, pins, fmt.Errorf("parse %s: %w", name, err)
		}
		canonical, err := encodeIndex(index)
		if err != nil {
			return snapshots, pins, err
		}
		if !bytes.Equal(canonical, content) {
			return snapshots, pins, fmt.Errorf("%s is not in canonical project encoding", name)
		}
		verification := Verify(index)
		if !verification.Valid {
			return snapshots, pins, fmt.Errorf("%s is invalid: %s", name, joinIssues(verification.Issues))
		}
		snapshots = append(snapshots, bundleSnapshot{
			file: BundleFile{
				Path:     name,
				Revision: revision,
				Size:     int64(len(content)),
				Digest:   digestExactBytes(content),
				Mode:     bundleFileMode,
			},
			content:      content,
			index:        index,
			verification: verification,
			sourceInfo:   info,
		})
	}
	if issues := verifyBundleChain(snapshots); len(issues) != 0 {
		return snapshots, pins, fmt.Errorf("release evidence chain is invalid: %s", joinIssues(issues))
	}
	return snapshots, pins, nil
}

func closeBundleFilePins(pins []bundleFilePin) error {
	var closeErr error
	for index := range pins {
		if pins[index].file == nil {
			continue
		}
		if err := pins[index].file.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close pinned %s: %w", pins[index].description, err))
		}
		pins[index].file = nil
	}
	return closeErr
}

func verifyBundleChain(snapshots []bundleSnapshot) []string {
	issues := make([]string, 0)
	if len(snapshots) == 0 {
		return []string{"release evidence bundle contains no index revisions"}
	}
	first := snapshots[0]
	if first.index.SchemaVersion != SchemaVersionV2 && first.index.SchemaVersion != SchemaVersionV3 {
		issues = append(issues, "index-r0.json must use a lineaged v2 or v3 schema")
	}
	if first.index.Lineage == nil || first.index.Lineage.Revision != 0 || first.index.Lineage.PreviousIndexDigest != nil {
		issues = append(issues, "index-r0.json must be lineage revision zero without a predecessor")
	}
	expectedFirst, err := NewIndex(first.index.Candidate, first.index.CreatedAt)
	if err != nil {
		issues = append(issues, "index-r0.json cannot be reconstructed as a canonical initialization: "+err.Error())
	} else {
		expectedFirst.SchemaVersion = first.index.SchemaVersion
		if !reflect.DeepEqual(expectedFirst, first.index) {
			issues = append(issues, "index-r0.json is not the exact canonical candidate initialization")
		}
	}
	for index := range snapshots {
		current := snapshots[index]
		if current.file.Path != bundleIndexName(int64(index)) || current.file.Revision != int64(index) {
			issues = append(issues, fmt.Sprintf("bundle index entry %d does not match its canonical revision", index))
		}
		if current.index.Lineage == nil || current.index.Lineage.Revision != int64(index) {
			issues = append(issues, fmt.Sprintf("%s lineage revision does not match its filename", current.file.Path))
		}
		if current.index.Candidate != first.index.Candidate {
			issues = append(issues, fmt.Sprintf("%s candidate identity differs from revision zero", current.file.Path))
		}
		if current.index.CreatedAt != first.index.CreatedAt {
			issues = append(issues, fmt.Sprintf("%s created_at differs from revision zero", current.file.Path))
		}
		if index == 0 {
			continue
		}
		previous := snapshots[index-1]
		if current.index.Lineage == nil || current.index.Lineage.PreviousIndexDigest == nil || *current.index.Lineage.PreviousIndexDigest != previous.file.Digest {
			issues = append(issues, fmt.Sprintf("%s predecessor digest does not match exact %s bytes", current.file.Path, previous.file.Path))
		}
		if previous.index.SchemaVersion == SchemaVersionV3 && current.index.SchemaVersion != SchemaVersionV3 || previous.index.SchemaVersion == SchemaVersionV2 && current.index.SchemaVersion != SchemaVersionV2 && current.index.SchemaVersion != SchemaVersionV3 {
			issues = append(issues, fmt.Sprintf("%s schema version regresses or is unsupported", current.file.Path))
		}
		previousTime, previousOK := parseDateTime(previous.index.Decision.RecordedAt)
		currentTime, currentOK := parseDateTime(current.index.Decision.RecordedAt)
		if previousOK && currentOK && currentTime.Before(previousTime) {
			issues = append(issues, fmt.Sprintf("%s decision.recorded_at precedes its predecessor", current.file.Path))
		}
		issues = append(issues, verifyBundleTransition(previous.index, current.index, current.file.Path)...)
	}
	sort.Strings(issues)
	return issues
}

func verifyBundleTransition(previous, current Index, name string) []string {
	issues := make([]string, 0)
	changedGates := 0
	changedGateName := ""
	previousGates := gateRequirements(previous.Gates)
	currentGates := gateRequirements(current.Gates)
	for index := range previousGates {
		before := previousGates[index]
		after := currentGates[index]
		if before.name != after.name {
			issues = append(issues, name+": internal readiness gate order mismatch")
			continue
		}
		if reflect.DeepEqual(before.gate, after.gate) {
			continue
		}
		changedGates++
		changedGateName = before.name
		if before.gate.Status != GateStatusOpen || before.gate.Evidence != nil || !bundleGateStatusAllowed(before.name, after.gate.Status) || after.gate.Evidence == nil {
			issues = append(issues, fmt.Sprintf("%s illegally reopens, supersedes, or mutates gate %s", name, before.name))
		}
	}
	controlsChanged := !reflect.DeepEqual(previous.PreventiveControls, current.PreventiveControls)
	if controlsChanged {
		issues = append(issues, name+": preventive control transitions are not registered for closed bundle lineage")
	}
	switch {
	case controlsChanged && changedGates != 0:
		issues = append(issues, name+": one revision cannot mutate both preventive controls and a readiness gate")
	case controlsChanged:
	case changedGates != 1:
		issues = append(issues, fmt.Sprintf("%s must close exactly one previously open readiness gate", name))
	default:
		after, err := gatePointer(&current.Gates, changedGateName)
		if err != nil || after.Evidence == nil {
			issues = append(issues, name+": changed readiness gate has no registered evidence attachment")
			break
		}
		if metadataIssue := bundleAttachmentMetadataIssue(previous, changedGateName, *after); metadataIssue != "" {
			issues = append(issues, name+": "+metadataIssue)
			break
		}
		capturedAt, capturedOK := parseDateTime(after.Evidence.CapturedAt)
		createdAt, createdOK := parseDateTime(previous.CreatedAt)
		if !capturedOK || !createdOK || capturedAt.Before(createdAt) {
			issues = append(issues, name+": gate evidence captured_at precedes candidate initialization")
			break
		}
		expected := previous
		expectedTarget, err := gatePointer(&expected.Gates, changedGateName)
		if err != nil {
			issues = append(issues, name+": changed readiness gate has no registered attachment adapter")
			break
		}
		*expectedTarget = Gate{Status: after.Status, Evidence: after.Evidence}
		expected.SchemaVersion = SchemaVersionV3
		expected.Lineage = current.Lineage
		if err := finalizeDerivedDecision(&expected, after.Evidence.CapturedAt); err != nil {
			issues = append(issues, name+": cannot reconstruct registered gate attachment: "+err.Error())
			break
		}
		if !reflect.DeepEqual(expected, current) {
			issues = append(issues, name+": successor is not the exact registered gate attachment transition")
		}
	}
	return issues
}

func bundleGateStatusAllowed(gate, status string) bool {
	if status == GateStatusPassed {
		return true
	}
	if status != GateStatusFailed {
		return false
	}
	return oneOf(gate, "critical_finding_review", "adoption_pilot_1", "adoption_pilot_2")
}

func bundleAttachmentMetadataIssue(previous Index, gate string, attached Gate) string {
	if attached.Evidence == nil || !canonicalTimestampPattern.MatchString(attached.Evidence.CapturedAt) {
		return "attached evidence captured_at is not canonical UTC RFC3339 seconds"
	}
	if attached.Evidence.Record == nil || attached.Evidence.Assurance == nil {
		return "registered attachment must retain its typed record and assurance metadata"
	}
	if wantAdapter := bundleGateAdapter(gate); wantAdapter == "" || attached.Evidence.Record.Adapter != wantAdapter {
		return "registered attachment must retain the exact gate adapter identity"
	}
	workflowGate := oneOf(
		gate,
		"source_compatibility",
		"aggregate_attempt_1",
		"aggregate_attempt_2",
		"draft_asset_verification",
		"draft_compatibility_7_cells",
		"draft_external_drivers",
		"publication",
		"public_asset_verification",
		"published_compatibility_7_cells",
	)
	if workflowGate {
		if attached.Evidence.RunID == nil || !validUnsignedDecimal(*attached.Evidence.RunID) ||
			attached.Evidence.RunAttempt == nil || *attached.Evidence.RunAttempt < 1 || *attached.Evidence.RunAttempt > maxJSONSafeInteger {
			return "workflow-derived attachment must retain its canonical run_id and run_attempt"
		}
	} else if attached.Evidence.RunID != nil || attached.Evidence.RunAttempt != nil {
		return "non-workflow attachment must not contain run_id or run_attempt"
	}
	if gate == "aggregate_attempt_2" {
		first := previous.Gates.AggregateAttempt1
		if first.Status != GateStatusPassed || first.Evidence == nil || first.Evidence.Record == nil || first.Evidence.Record.Adapter != AggregateAttempt1Adapter {
			return "aggregate attempt two requires an already attached registered attempt-one predecessor"
		}
	}
	return ""
}

func bundleGateAdapter(gate string) string {
	switch gate {
	case "source_compatibility":
		return CompatibilitySourceAdapter
	case "aggregate_attempt_1":
		return AggregateAttempt1Adapter
	case "aggregate_attempt_2":
		return AggregateAttempt2Adapter
	case "draft_asset_verification":
		return ReleaseAssetDraftAdapter
	case "draft_compatibility_7_cells":
		return CompatibilityDraftAdapter
	case "draft_external_drivers":
		return ExternalDriverVerificationAdapter
	case "publication":
		return ReleasePublicationAdapter
	case "public_asset_verification":
		return ReleaseAssetPublishedAdapter
	case "published_compatibility_7_cells":
		return CompatibilityPublishedAdapter
	case "critical_finding_review":
		return CriticalFindingReviewAdapter
	case "adoption_pilot_1", "adoption_pilot_2":
		return AdoptionPilotAdapter
	case "independent_authoring_reproduction":
		return IndependentAuthoringPilotAdapter
	default:
		return ""
	}
}

func validateBundleInventory(inventory BundleInventory) []string {
	issues := make([]string, 0)
	add := func(format string, args ...any) { issues = append(issues, fmt.Sprintf(format, args...)) }
	if inventory.SchemaVersion != BundleSchemaVersion {
		add("bundle schema_version = %q, want %q", inventory.SchemaVersion, BundleSchemaVersion)
	}
	if inventory.ArtifactType != BundleArtifactType {
		add("bundle artifact_type = %q, want %q", inventory.ArtifactType, BundleArtifactType)
	}
	validateCandidate(add, inventory.Candidate)
	if inventory.HeadRevision < 0 || inventory.HeadRevision >= maxBundleRevisions {
		add("bundle head_revision must be between 0 and %d", maxBundleRevisions-1)
	}
	wantHead := bundleIndexName(inventory.HeadRevision)
	if inventory.HeadIndex != wantHead {
		add("bundle head_index = %q, want %q", inventory.HeadIndex, wantHead)
	}
	if !validDigest(inventory.HeadDigest) {
		add("bundle head_digest must be a lowercase sha256 digest")
	}
	if inventory.FileCount != int64(len(inventory.Files)) || inventory.FileCount != inventory.HeadRevision+1 {
		add("bundle file_count must equal head_revision + 1 and the files array length")
	}
	if len(inventory.Files) == 0 || int64(len(inventory.Files)) > maxBundleRevisions {
		add("bundle files must contain between 1 and %d entries", maxBundleRevisions)
	}
	var total int64
	for index, file := range inventory.Files {
		wantRevision := int64(index)
		wantPath := bundleIndexName(wantRevision)
		if file.Path != wantPath || file.Revision != wantRevision {
			add("bundle files[%d] must identify canonical revision %d", index, wantRevision)
		}
		if file.Mode != bundleFileMode {
			add("bundle files[%d].mode must be 420 (0644)", index)
		}
		if file.Size <= 0 || file.Size > maxIndexBytes {
			add("bundle files[%d].size must be between 1 and %d", index, maxIndexBytes)
		}
		if !validDigest(file.Digest) {
			add("bundle files[%d].digest must be a lowercase sha256 digest", index)
		}
		if file.Size > 0 && total <= maxBundleEvidenceBytes-file.Size {
			total += file.Size
		} else {
			total = maxBundleEvidenceBytes + 1
		}
	}
	if inventory.TotalSizeBytes != total || total > maxBundleEvidenceBytes {
		add("bundle total_size_bytes must equal the bounded sum of file sizes")
	}
	treeDigest, err := bundleTreeDigest(inventory.Files)
	if err != nil {
		add("derive bundle tree digest: %v", err)
	} else if inventory.TreeDigest != treeDigest {
		add("bundle tree_digest does not match the ordered file inventory")
	}
	if len(inventory.Files) > 0 {
		last := inventory.Files[len(inventory.Files)-1]
		if inventory.HeadIndex != last.Path || inventory.HeadRevision != last.Revision || inventory.HeadDigest != last.Digest {
			add("bundle head identity must equal the final inventory entry")
		}
	}
	if !oneOf(inventory.Outcome.Status, StatusOpen, StatusFailed, StatusPassed) ||
		!oneOf(inventory.Outcome.Decision, DecisionNoGo, DecisionGo) ||
		!oneOf(inventory.Outcome.ReadinessStatus, StatusOpen, StatusFailed, StatusPassed) ||
		!oneOf(inventory.Outcome.ReadinessDecision, DecisionNoGo, DecisionGo) ||
		!oneOf(inventory.Outcome.AssuranceStatus, AssuranceNotApplicable, AssuranceLegacyUnspecified, AssuranceOperatorAttested, AssuranceAuthorizationEligible) {
		add("bundle outcome contains an unsupported status, decision, or assurance value")
	}
	if inventory.Outcome.AuthorizationEligible && (inventory.Outcome.Status != StatusPassed || inventory.Outcome.Decision != DecisionGo || inventory.Outcome.AssuranceStatus != AssuranceAuthorizationEligible) {
		add("bundle authorization_eligible is inconsistent with its outcome")
	}
	sort.Strings(issues)
	return issues
}

func verifyBundleSourceNames(directory *os.File, want []string) error {
	entries, err := readBundleDirectoryEntries(directory)
	if err != nil {
		return err
	}
	for _, name := range want {
		if _, ok := entries[name]; !ok {
			return fmt.Errorf("release evidence chain is missing %s", name)
		}
	}
	return nil
}

func confirmBundleSource(directory *os.File, sourcePath string, snapshots []bundleSnapshot, wantNames []string) error {
	if err := verifyBundleSourceNames(directory, wantNames); err != nil {
		return fmt.Errorf("confirm release evidence source chain: %w", err)
	}
	for _, snapshot := range snapshots {
		file, err := openReadOnlyEntryAt(directory, snapshot.file.Path, "release evidence source index confirmation")
		if err != nil {
			return fmt.Errorf("confirm %s: %w", snapshot.file.Path, err)
		}
		info, inspectErr := file.Stat()
		if inspectErr != nil || !os.SameFile(snapshot.sourceInfo, info) || info.Mode() != snapshot.sourceInfo.Mode() || bundleFileHasMultipleLinks(info) {
			_ = file.Close()
			return fmt.Errorf("confirm %s: source inode or mode changed after snapshot", snapshot.file.Path)
		}
		content, readErr := strictjson.ReadOpenedFile(file, maxIndexBytes)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return fmt.Errorf("confirm %s: read source after snapshot: %w", snapshot.file.Path, errors.Join(readErr, closeErr))
		}
		if !bytes.Equal(content, snapshot.content) {
			return fmt.Errorf("confirm %s: source bytes changed after snapshot", snapshot.file.Path)
		}
	}
	if err := confirmPinnedDirectoryPath(directory, sourcePath, "release evidence source chain directory"); err != nil {
		return err
	}
	return nil
}

func confirmBundlePublicationBoundary(sourceDirectory, outputDirectory *os.File, sourcePath, outputPath string) error {
	if err := confirmPinnedDirectoryPath(sourceDirectory, sourcePath, "release evidence source chain directory"); err != nil {
		return err
	}
	outputParent := filepath.Dir(outputPath)
	if err := confirmPinnedDirectoryPath(outputDirectory, outputParent, "evidence bundle output directory"); err != nil {
		return err
	}
	sourceInfo, sourceErr := sourceDirectory.Stat()
	outputInfo, outputErr := outputDirectory.Stat()
	if sourceErr != nil || outputErr != nil {
		return fmt.Errorf("inspect pinned bundle source and output directories: %w", errors.Join(sourceErr, outputErr))
	}
	if os.SameFile(sourceInfo, outputInfo) {
		return pathguard.ErrOutputWithinSource
	}
	reconfirmed, err := pathguard.ResolveOutputOutside(sourcePath, outputPath)
	if err != nil {
		return fmt.Errorf("reconfirm pinned evidence bundle output containment: %w", err)
	}
	if reconfirmed != outputPath {
		return fmt.Errorf("evidence bundle output pathname changed during publication")
	}
	return nil
}

func confirmBundleEntry(directory *os.File, name string, expectedInfo os.FileInfo, expectedContent []byte, maxBytes int64, description string) error {
	file, err := openReadOnlyEntryAt(directory, name, description+" confirmation")
	if err != nil {
		return fmt.Errorf("confirm %s %s: %w", description, name, err)
	}
	info, inspectErr := file.Stat()
	if inspectErr != nil || expectedInfo == nil || !os.SameFile(expectedInfo, info) || info.Mode() != expectedInfo.Mode() || bundleFileHasMultipleLinks(info) {
		_ = file.Close()
		return fmt.Errorf("confirm %s %s: directory entry no longer identifies the verified inode and mode", description, name)
	}
	content, readErr := strictjson.ReadOpenedFile(file, maxBytes)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("confirm %s %s after verification: %w", description, name, errors.Join(readErr, closeErr))
	}
	if !bytes.Equal(content, expectedContent) {
		return fmt.Errorf("confirm %s %s: bytes changed after verification", description, name)
	}
	return nil
}

func readBundleDirectoryEntries(directory *os.File) (map[string]os.FileInfo, error) {
	if _, err := directory.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind evidence bundle directory: %w", err)
	}
	entries, err := directory.ReadDir(maxBundleDirectoryEntries + 1)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read evidence bundle directory: %w", err)
	}
	if len(entries) > maxBundleDirectoryEntries {
		return nil, fmt.Errorf("evidence bundle directory contains more than %d entries", maxBundleDirectoryEntries)
	}
	result := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect evidence bundle entry %s: %w", entry.Name(), err)
		}
		result[entry.Name()] = info
	}
	return result, nil
}

func writeBundleFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(bundleFileMode))
	if err != nil {
		return fmt.Errorf("create staged evidence bundle file: %w", err)
	}
	writeErr := func() error {
		if _, err := file.Write(content); err != nil {
			return err
		}
		if err := file.Chmod(os.FileMode(bundleFileMode)); err != nil {
			return err
		}
		return file.Sync()
	}()
	return errors.Join(writeErr, file.Close())
}

func encodeBundleInventory(inventory BundleInventory) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(inventory); err != nil {
		return nil, fmt.Errorf("encode release evidence bundle inventory: %w", err)
	}
	return buffer.Bytes(), nil
}

func readBoundedBundleArchive(path string, expectedSize int64) ([]byte, error) {
	if expectedSize <= 0 || expectedSize > maxBundleArchiveBytes {
		return nil, fmt.Errorf("staged release evidence bundle archive size must be between 1 and %d bytes", maxBundleArchiveBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open staged release evidence bundle archive: %w", err)
	}
	info, inspectErr := file.Stat()
	if inspectErr != nil || !info.Mode().IsRegular() || info.Mode() != 0o644 || bundleFileHasMultipleLinks(info) || info.Size() != expectedSize {
		_ = file.Close()
		if inspectErr != nil {
			return nil, fmt.Errorf("inspect staged release evidence bundle archive: %w", inspectErr)
		}
		return nil, fmt.Errorf("staged release evidence bundle archive must be one regular non-hardlinked 0644 file with the reported size")
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxBundleArchiveBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read staged release evidence bundle archive: %w", errors.Join(readErr, closeErr))
	}
	if int64(len(content)) != expectedSize || int64(len(content)) > maxBundleArchiveBytes {
		return nil, fmt.Errorf("staged release evidence bundle archive size changed after creation")
	}
	return content, nil
}

func verifyExactBundleArchive(content, inventoryContent []byte, snapshots []bundleSnapshot) error {
	buffered := bufio.NewReader(bytes.NewReader(content))
	gzipReader, err := gzip.NewReader(buffered)
	if err != nil {
		return err
	}
	gzipReader.Multistream(false)
	epoch := time.Unix(canonicalBundleArchiveEpoch, 0).UTC()
	canonicalGzipTime := gzipReader.ModTime.Equal(epoch) || canonicalBundleArchiveEpoch == 0 && gzipReader.ModTime.IsZero()
	if !canonicalGzipTime || gzipReader.OS != 255 || gzipReader.Name != "" || gzipReader.Comment != "" {
		_ = gzipReader.Close()
		return fmt.Errorf("gzip header is not canonical")
	}

	type expectedArchiveEntry struct {
		name    string
		mode    int64
		content []byte
	}
	expected := make([]expectedArchiveEntry, 0, len(snapshots)+1)
	for _, snapshot := range snapshots {
		expected = append(expected, expectedArchiveEntry{name: snapshot.file.Path, mode: 0o644, content: snapshot.content})
	}
	expected = append(expected, expectedArchiveEntry{name: BundleInventoryName, mode: 0o644, content: inventoryContent})
	sort.Slice(expected, func(i, j int) bool { return expected[i].name < expected[j].name })

	tarReader := tar.NewReader(gzipReader)
	rootHeader, err := tarReader.Next()
	if err != nil {
		_ = gzipReader.Close()
		return fmt.Errorf("read bundle archive root: %w", err)
	}
	if err := verifyCanonicalBundleArchiveHeader(rootHeader, BundleRootName+"/", tar.TypeDir, 0o755, 0, epoch); err != nil {
		_ = gzipReader.Close()
		return err
	}
	for index, want := range expected {
		header, nextErr := tarReader.Next()
		if nextErr != nil {
			_ = gzipReader.Close()
			return fmt.Errorf("read bundle archive entry %d: %w", index+1, nextErr)
		}
		if err := verifyCanonicalBundleArchiveHeader(header, BundleRootName+"/"+want.name, tar.TypeReg, want.mode, int64(len(want.content)), epoch); err != nil {
			_ = gzipReader.Close()
			return err
		}
		actual := make([]byte, len(want.content))
		if _, err := io.ReadFull(tarReader, actual); err != nil {
			_ = gzipReader.Close()
			return fmt.Errorf("read exact bundle archive entry %s: %w", header.Name, err)
		}
		if !bytes.Equal(actual, want.content) {
			_ = gzipReader.Close()
			return fmt.Errorf("bundle archive entry bytes differ from verified snapshot: %s", header.Name)
		}
	}
	if header, nextErr := tarReader.Next(); nextErr != io.EOF {
		_ = gzipReader.Close()
		if nextErr != nil {
			return fmt.Errorf("read trailing bundle archive entry: %w", nextErr)
		}
		return fmt.Errorf("bundle archive contains unexpected entry: %s", header.Name)
	}
	if _, err := io.Copy(io.Discard, gzipReader); err != nil {
		_ = gzipReader.Close()
		return fmt.Errorf("validate bundle gzip payload: %w", err)
	}
	if err := gzipReader.Close(); err != nil {
		return err
	}
	if _, err := buffered.ReadByte(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("inspect trailing bundle archive bytes: %w", err)
		}
		return fmt.Errorf("bundle archive contains trailing bytes")
	}
	return nil
}

func verifyCanonicalBundleArchiveHeader(header *tar.Header, name string, typeFlag byte, mode, size int64, epoch time.Time) error {
	if header.Name != name || header.Typeflag != typeFlag || header.Mode != mode || header.Size != size ||
		header.Uid != 0 || header.Gid != 0 || header.Uname != "root" || header.Gname != "root" ||
		!header.ModTime.Equal(epoch) || !header.AccessTime.IsZero() || !header.ChangeTime.IsZero() || header.Linkname != "" {
		return fmt.Errorf("bundle archive header mismatch: %s", header.Name)
	}
	return nil
}

func bundleTreeDigest(files []BundleFile) (string, error) {
	content, err := json.Marshal(files)
	if err != nil {
		return "", fmt.Errorf("encode release evidence bundle tree: %w", err)
	}
	return digestExactBytes(content), nil
}

func outcomeFromVerification(verification Verification) BundleOutcome {
	return BundleOutcome{
		Status:                verification.Status,
		Decision:              verification.Decision,
		ReadinessStatus:       verification.ReadinessStatus,
		ReadinessDecision:     verification.ReadinessDecision,
		AssuranceStatus:       verification.AssuranceStatus,
		AuthorizationEligible: verification.AuthorizationEligible,
	}
}

func parseBundleIndexName(name string) (int64, error) {
	if !strings.HasPrefix(name, "index-r") || !strings.HasSuffix(name, ".json") {
		return 0, fmt.Errorf("index name must be canonical index-r<N>.json")
	}
	digits := strings.TrimSuffix(strings.TrimPrefix(name, "index-r"), ".json")
	if digits == "" || len(digits) > 1 && digits[0] == '0' {
		return 0, fmt.Errorf("index name must use a canonical decimal revision")
	}
	revision, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || revision < 0 || bundleIndexName(revision) != name {
		return 0, fmt.Errorf("index name must use a canonical non-negative decimal revision")
	}
	return revision, nil
}

func bundleIndexName(revision int64) string {
	return "index-r" + strconv.FormatInt(revision, 10) + ".json"
}

func bundleIndexNames(headRevision int64) []string {
	names := make([]string, 0, headRevision+1)
	for revision := int64(0); revision <= headRevision; revision++ {
		names = append(names, bundleIndexName(revision))
	}
	return names
}

func finalizeBundleVerification(result BundleVerification) BundleVerification {
	sort.Strings(result.Issues)
	result.Valid = len(result.Issues) == 0
	return result
}
