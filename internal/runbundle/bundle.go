package runbundle

import (
	"archive/tar"
	"compress/gzip"
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
	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
)

type Result struct {
	RunDir string `json:"run_dir"`
	Output string `json:"output"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	Digest string `json:"digest"`
}

type sourceFile struct {
	path   string
	rel    string
	size   int64
	digest string
}

func Create(root string, input string, output string) (Result, error) {
	return create(root, input, output, nil)
}

func create(root string, input string, output string, beforeStageVerify func(string) error) (Result, error) {
	runDir, err := runartifact.ResolveRunDir(root, input)
	if err != nil {
		return Result{}, err
	}
	if output == "" {
		output = runDir + ".tar.gz"
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	output, err = pathguard.ResolveOutputOutside(runDir, output)
	if err != nil {
		return Result{}, fmt.Errorf("resolve bundle output: %w", err)
	}
	output, err = pathguard.PrepareNewOutputOutside(runDir, output, 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare bundle output: %w", err)
	}

	files, err := collectSourceFiles(runDir)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, fmt.Errorf("run artifact contains no regular files: %s", runDir)
	}
	runID, err := bundleRunID(runDir)
	if err != nil {
		return Result{}, err
	}
	inventoryFiles := make([]evidence.BundleFile, 0, len(files))
	result := Result{RunDir: runDir, Output: output, Files: len(files)}
	for _, file := range files {
		inventoryFiles = append(inventoryFiles, evidence.BundleFile{
			Path:   file.rel,
			Size:   file.size,
			Digest: file.digest,
		})
		result.Bytes += file.size
	}
	inventoryBytes, err := evidence.MarshalBundleInventory(evidence.NewBundleInventory(runID, inventoryFiles))
	if err != nil {
		return Result{}, err
	}
	stage, err := os.MkdirTemp("", ".pgworkbench-run-bundle-*")
	if err != nil {
		return Result{}, fmt.Errorf("create run bundle staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	stagedRunDir := filepath.Join(stage, filepath.Base(runDir))
	for _, source := range files {
		destination := filepath.Join(stagedRunDir, filepath.FromSlash(source.rel))
		if err := copySourceFile(destination, source); err != nil {
			return Result{}, fmt.Errorf("stage run bundle file %s: %w", source.rel, err)
		}
	}
	if err := os.WriteFile(filepath.Join(stagedRunDir, evidence.BundleInventoryName), inventoryBytes, 0o644); err != nil {
		return Result{}, fmt.Errorf("write staged run bundle inventory: %w", err)
	}
	if beforeStageVerify != nil {
		if err := beforeStageVerify(stage); err != nil {
			return Result{}, fmt.Errorf("prepare staged run bundle verification: %w", err)
		}
	}
	stagedVerification, err := runverify.VerifyBundle(stage, stagedRunDir)
	if err != nil {
		return Result{}, fmt.Errorf("verify staged run bundle: %w", err)
	}
	if !stagedVerification.Valid() {
		return Result{}, fmt.Errorf("staged run bundle is invalid: %s", strings.Join(stagedVerification.Issues, "; "))
	}

	tempFile, err := os.CreateTemp(filepath.Dir(output), ".pgworkbench-bundle-*.tmp")
	if err != nil {
		return Result{}, err
	}
	tempPath := tempFile.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	gzipWriter := gzip.NewWriter(tempFile)
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	baseName := filepath.Base(runDir)

	var writeErr error
	for _, source := range files {
		if err := writeSourceFile(tarWriter, baseName, source); err != nil {
			writeErr = err
			break
		}
	}
	if writeErr == nil {
		inventoryName := filepath.ToSlash(filepath.Join(baseName, evidence.BundleInventoryName))
		writeErr = writeBytes(tarWriter, inventoryName, inventoryBytes)
	}
	if err := tarWriter.Close(); writeErr == nil && err != nil {
		writeErr = err
	}
	if err := gzipWriter.Close(); writeErr == nil && err != nil {
		writeErr = err
	}
	if err := tempFile.Chmod(0o644); writeErr == nil && err != nil {
		writeErr = err
	}
	if err := tempFile.Sync(); writeErr == nil && err != nil {
		writeErr = err
	}
	if err := tempFile.Close(); writeErr == nil && err != nil {
		writeErr = err
	}
	if writeErr != nil {
		return Result{}, writeErr
	}
	if err := pathguard.PublishFileExclusive(tempPath, output); err != nil {
		return Result{}, err
	}
	keepTemp = true

	result.Digest, err = evidence.DigestFile(output)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func copySourceFile(destination string, source sourceFile) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source.path)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		_ = input.Close()
		return err
	}
	digest, written, copyErr := evidence.DigestReader(io.TeeReader(input, output))
	closeErr := errors.Join(input.Close(), output.Close())
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if written != source.size {
		return fmt.Errorf("run artifact changed while staging: %s size was %d, now %d", source.rel, source.size, written)
	}
	if digest != source.digest {
		return fmt.Errorf("run artifact changed while staging: %s digest mismatch", source.rel)
	}
	return nil
}

func collectSourceFiles(runDir string) ([]sourceFile, error) {
	var files []sourceFile
	err := filepath.WalkDir(runDir, func(filePath string, entry os.DirEntry, walkErr error) error {
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
		rel, err := filepath.Rel(runDir, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == evidence.BundleInventoryName {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("run artifact contains unsupported non-regular path: %s", rel)
		}
		digest, err := evidence.DigestFile(filePath)
		if err != nil {
			return err
		}
		files = append(files, sourceFile{path: filePath, rel: rel, size: info.Size(), digest: digest})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i int, j int) bool {
		return files[i].rel < files[j].rel
	})
	return files, nil
}

func bundleRunID(runDir string) (string, error) {
	manifest, err := runartifact.LoadOptionalEnv(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		return "", fmt.Errorf("parse manifest.env: %w", err)
	}
	return manifest.Value("run_id", filepath.Base(runDir)), nil
}

func writeSourceFile(writer *tar.Writer, baseName string, source sourceFile) error {
	name := filepath.ToSlash(filepath.Join(baseName, filepath.FromSlash(source.rel)))
	if err := writeHeader(writer, name, source.size); err != nil {
		return err
	}
	inputFile, err := os.Open(source.path)
	if err != nil {
		return err
	}
	digest, written, copyErr := evidence.DigestReader(io.TeeReader(inputFile, writer))
	closeErr := inputFile.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != source.size {
		return fmt.Errorf("run artifact changed while bundling: %s size was %d, now %d", source.rel, source.size, written)
	}
	if digest != source.digest {
		return fmt.Errorf("run artifact changed while bundling: %s digest mismatch", source.rel)
	}
	return nil
}

func writeBytes(writer *tar.Writer, name string, content []byte) error {
	if err := writeHeader(writer, name, int64(len(content))); err != nil {
		return err
	}
	_, err := writer.Write(content)
	return err
}

func writeHeader(writer *tar.Writer, name string, size int64) error {
	header := &tar.Header{
		Name:       name,
		Mode:       0o644,
		Size:       size,
		ModTime:    time.Unix(0, 0).UTC(),
		AccessTime: time.Time{},
		ChangeTime: time.Time{},
		Typeflag:   tar.TypeReg,
		Format:     tar.FormatPAX,
	}
	return writer.WriteHeader(header)
}

func isSubpath(parent string, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}
