package releasearchive

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
)

const SchemaVersion = "pgworkbench.release-archive-result/v1"

type Result struct {
	SchemaVersion string `json:"schema_version"`
	RootName      string `json:"root_name"`
	Output        string `json:"output"`
	SHA256        string `json:"sha256"`
	Files         int    `json:"files"`
	Bytes         int64  `json:"bytes"`
}

type sourceFile struct {
	path       string
	relative   string
	executable bool
	size       int64
	digest     string
}

func Create(sourceRoot string, output string, rootName string, epoch time.Time) (Result, error) {
	return create(sourceRoot, output, rootName, epoch, nil)
}

func create(sourceRoot string, output string, rootName string, epoch time.Time, beforeArchiveVerify func(string) error) (Result, error) {
	if err := validateRootName(rootName); err != nil {
		return Result{}, err
	}
	absSource, err := filepath.Abs(sourceRoot)
	if err != nil {
		return Result{}, err
	}
	absOutput, err := pathguard.ResolveOutputOutside(absSource, output)
	if err != nil {
		return Result{}, fmt.Errorf("resolve release archive output: %w", err)
	}
	info, err := os.Lstat(absSource)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Result{}, fmt.Errorf("release archive source must be a real directory")
	}
	if epoch.IsZero() || epoch.Unix() < 0 {
		return Result{}, fmt.Errorf("release archive epoch must be a non-negative timestamp")
	}
	epoch = epoch.UTC().Truncate(time.Second)

	files, directories, err := inventory(absSource)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, fmt.Errorf("release archive source is empty")
	}
	absOutput, err = pathguard.PrepareNewOutputOutside(absSource, absOutput, 0o755)
	if err != nil {
		return Result{}, fmt.Errorf("prepare release archive output: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(absOutput), ".pgworkbench-release-*.tmp")
	if err != nil {
		return Result{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	gzipWriter, err := gzip.NewWriterLevel(temporary, gzip.BestCompression)
	if err != nil {
		_ = temporary.Close()
		return Result{}, err
	}
	gzipWriter.Header.ModTime = epoch
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)

	writeError := writeDirectory(tarWriter, rootName, epoch)
	for _, directory := range directories {
		if writeError != nil {
			break
		}
		writeError = writeDirectory(tarWriter, rootName+"/"+directory, epoch)
	}
	for _, file := range files {
		if writeError != nil {
			break
		}
		writeError = writeFile(tarWriter, rootName, file, epoch)
	}
	if closeErr := tarWriter.Close(); writeError == nil {
		writeError = closeErr
	}
	if closeErr := gzipWriter.Close(); writeError == nil {
		writeError = closeErr
	}
	if closeErr := temporary.Close(); writeError == nil {
		writeError = closeErr
	}
	if writeError != nil {
		return Result{}, writeError
	}
	if beforeArchiveVerify != nil {
		if err := beforeArchiveVerify(temporaryPath); err != nil {
			return Result{}, fmt.Errorf("prepare staged release archive verification: %w", err)
		}
	}
	if err := verifyArchive(temporaryPath, rootName, epoch, files, directories); err != nil {
		return Result{}, fmt.Errorf("verify staged release archive: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return Result{}, err
	}
	staged, err := os.OpenFile(temporaryPath, os.O_WRONLY, 0)
	if err != nil {
		return Result{}, err
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return Result{}, err
	}
	if err := staged.Close(); err != nil {
		return Result{}, err
	}
	if err := pathguard.PublishFileExclusive(temporaryPath, absOutput); err != nil {
		return Result{}, err
	}

	digest, size, err := digestFile(absOutput)
	if err != nil {
		return Result{}, err
	}
	return Result{
		SchemaVersion: SchemaVersion,
		RootName:      rootName,
		Output:        absOutput,
		SHA256:        digest,
		Files:         len(files),
		Bytes:         size,
	}, nil
}

func inventory(root string) ([]sourceFile, []string, error) {
	var files []sourceFile
	directorySet := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release archive rejects symlink: %s", relative)
		}
		if entry.IsDir() {
			directorySet[relative] = struct{}{}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release archive accepts regular files only: %s", relative)
		}
		digest, size, err := digestFile(path)
		if err != nil {
			return err
		}
		if size != info.Size() {
			return fmt.Errorf("release archive source changed while inventorying: %s", relative)
		}
		files = append(files, sourceFile{
			path:       path,
			relative:   relative,
			executable: info.Mode()&0o111 != 0,
			size:       size,
			digest:     digest,
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	directories := make([]string, 0, len(directorySet))
	for directory := range directorySet {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	return files, directories, nil
}

func writeDirectory(writer *tar.Writer, name string, epoch time.Time) error {
	header := normalizedHeader(name+"/", 0o755, 0, tar.TypeDir, epoch)
	return writer.WriteHeader(header)
}

func writeFile(writer *tar.Writer, rootName string, file sourceFile, epoch time.Time) error {
	input, err := os.Open(file.path)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	mode := int64(0o644)
	if file.executable {
		mode = 0o755
	}
	if !info.Mode().IsRegular() || info.Size() != file.size {
		return fmt.Errorf("release archive source changed while archiving: %s", file.relative)
	}
	header := normalizedHeader(rootName+"/"+file.relative, mode, file.size, tar.TypeReg, epoch)
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(writer, hash), input)
	if err != nil {
		return err
	}
	if written != file.size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != file.digest {
		return fmt.Errorf("release archive source changed while archiving: %s", file.relative)
	}
	return nil
}

func verifyArchive(path string, rootName string, epoch time.Time, files []sourceFile, directories []string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	buffered := bufio.NewReader(input)
	gzipReader, err := gzip.NewReader(buffered)
	if err != nil {
		return err
	}
	gzipReader.Multistream(false)
	canonicalGzipTime := gzipReader.ModTime.Equal(epoch) || (epoch.Unix() == 0 && gzipReader.ModTime.IsZero())
	if !canonicalGzipTime || gzipReader.OS != 255 || gzipReader.Name != "" || gzipReader.Comment != "" {
		_ = gzipReader.Close()
		return fmt.Errorf("gzip header is not canonical")
	}
	tarReader := tar.NewReader(gzipReader)
	type expectedEntry struct {
		name     string
		mode     int64
		typeFlag byte
		file     *sourceFile
	}
	expected := make([]expectedEntry, 0, 1+len(directories)+len(files))
	expected = append(expected, expectedEntry{name: rootName + "/", mode: 0o755, typeFlag: tar.TypeDir})
	for _, directory := range directories {
		expected = append(expected, expectedEntry{name: rootName + "/" + directory + "/", mode: 0o755, typeFlag: tar.TypeDir})
	}
	for index := range files {
		mode := int64(0o644)
		if files[index].executable {
			mode = 0o755
		}
		expected = append(expected, expectedEntry{name: rootName + "/" + files[index].relative, mode: mode, typeFlag: tar.TypeReg, file: &files[index]})
	}
	for index, want := range expected {
		header, nextErr := tarReader.Next()
		if nextErr != nil {
			_ = gzipReader.Close()
			return fmt.Errorf("archive entry %d is unavailable: %w", index+1, nextErr)
		}
		if header.Name != want.name || header.Typeflag != want.typeFlag || header.Mode != want.mode || header.Uid != 0 || header.Gid != 0 || header.Uname != "root" || header.Gname != "root" || !header.ModTime.Equal(epoch) || !header.AccessTime.IsZero() || !header.ChangeTime.IsZero() {
			_ = gzipReader.Close()
			return fmt.Errorf("archive entry %d header mismatch: %s", index+1, header.Name)
		}
		if want.file == nil {
			if header.Size != 0 {
				_ = gzipReader.Close()
				return fmt.Errorf("archive directory has non-zero size: %s", header.Name)
			}
			continue
		}
		if header.Size != want.file.size {
			_ = gzipReader.Close()
			return fmt.Errorf("archive file size mismatch: %s", header.Name)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, tarReader)
		if copyErr != nil {
			_ = gzipReader.Close()
			return fmt.Errorf("read archive file %s: %w", header.Name, copyErr)
		}
		if written != want.file.size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != want.file.digest {
			_ = gzipReader.Close()
			return fmt.Errorf("archive file digest mismatch: %s", header.Name)
		}
	}
	if header, nextErr := tarReader.Next(); nextErr != io.EOF {
		_ = gzipReader.Close()
		if nextErr != nil {
			return fmt.Errorf("read trailing archive entry: %w", nextErr)
		}
		return fmt.Errorf("archive contains unexpected entry: %s", header.Name)
	}
	if _, err := io.Copy(io.Discard, gzipReader); err != nil {
		_ = gzipReader.Close()
		return fmt.Errorf("validate gzip payload: %w", err)
	}
	if err := gzipReader.Close(); err != nil {
		return err
	}
	if _, err := buffered.ReadByte(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("inspect trailing archive bytes: %w", err)
		}
		return fmt.Errorf("archive contains trailing bytes")
	}
	return nil
}

func normalizedHeader(name string, mode int64, size int64, typeFlag byte, epoch time.Time) *tar.Header {
	return &tar.Header{
		Name:       filepath.ToSlash(name),
		Mode:       mode,
		Uid:        0,
		Gid:        0,
		Size:       size,
		ModTime:    epoch,
		Typeflag:   typeFlag,
		Uname:      "root",
		Gname:      "root",
		Format:     tar.FormatPAX,
		AccessTime: time.Time{},
		ChangeTime: time.Time{},
	}
}

func validateRootName(value string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return fmt.Errorf("release archive root name must be one safe path segment")
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-", character) {
			continue
		}
		return fmt.Errorf("release archive root name contains unsafe character %q", character)
	}
	return nil
}

func digestFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}
