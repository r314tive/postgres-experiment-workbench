package runbundle

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
)

type Result struct {
	RunDir string
	Output string
	Files  int
	Bytes  int64
}

func Create(root string, input string, output string) (Result, error) {
	runDir, err := runartifact.ResolveRunDir(root, input)
	if err != nil {
		return Result{}, err
	}
	if output == "" {
		output = runDir + ".tar.gz"
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return Result{}, err
	}
	if isSubpath(runDir, output) {
		return Result{}, fmt.Errorf("bundle output must not be inside the run directory: %s", output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return Result{}, err
	}

	file, err := os.Create(output)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	result := Result{RunDir: runDir, Output: output}
	baseName := filepath.Base(runDir)
	err = filepath.WalkDir(runDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(baseName, rel))
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		inputFile, err := os.Open(path)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(tarWriter, inputFile)
		closeErr := inputFile.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		result.Files++
		result.Bytes += written
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func isSubpath(parent string, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}
