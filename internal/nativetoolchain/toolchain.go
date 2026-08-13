// Package nativetoolchain records the exact executable bytes used by a native
// PostgreSQL benchmark arm. The manifest is byte identity evidence only: it
// deliberately does not infer a source commit or build provenance from a
// version string.
package nativetoolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	SchemaVersion = "pgworkbench.native-toolchain/v1"
	ArtifactType  = "pgworkbench.native-toolchain"
	Unattested    = "unattested"
	ManifestName  = "manifest.json"
	// Version probes run trusted local PostgreSQL files before reservation.
	// Keep them bounded while allowing scheduler headroom in concurrent CI.
	versionTimeout = 30 * time.Second
)

var requiredExecutables = []string{"createdb", "initdb", "pg_ctl", "pg_isready", "pgbench", "postgres", "psql"}

func RequiredExecutableNames() []string { return append([]string(nil), requiredExecutables...) }

func Version(manifest Manifest, name string) string {
	for _, binary := range manifest.Binaries {
		if binary.Name == name {
			return binary.Version
		}
	}
	return ""
}

// RequireComparableVersions prevents this byte-set subject from silently
// becoming a cross-major/version benchmark. It still does not prove that
// sibling share/lib files, dynamic libraries, build flags, or source commits
// are identical; those remain outside this bounded manifest.
func RequireComparableVersions(left, right Manifest) error {
	for _, name := range requiredExecutables {
		leftVersion, rightVersion := Version(left, name), Version(right, name)
		if leftVersion == "" || rightVersion == "" || leftVersion != rightVersion {
			return fmt.Errorf("%s version identity differs: %q != %q", name, leftVersion, rightVersion)
		}
	}
	return nil
}

type Binary struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
}

type Manifest struct {
	SchemaVersion   string   `json:"schema_version"`
	ArtifactType    string   `json:"artifact_type"`
	SourceCommit    string   `json:"source_commit"`
	BuildProvenance string   `json:"build_provenance"`
	Binaries        []Binary `json:"binaries"`
	Digest          string   `json:"digest"`
}

// Installation retains the local execution path outside the serialized
// contract. Only Manifest is portable and may enter evidence artifacts.
type Installation struct {
	Bindir   string   `json:"-"`
	Manifest Manifest `json:"manifest"`
}

func Inspect(bindir string) (Installation, error) {
	if bindir == "" || !filepath.IsAbs(bindir) || filepath.Clean(bindir) != bindir {
		return Installation{}, fmt.Errorf("native PostgreSQL bindir must be a clean absolute path")
	}
	info, err := os.Lstat(bindir)
	if err != nil {
		return Installation{}, fmt.Errorf("inspect native PostgreSQL bindir: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Installation{}, fmt.Errorf("native PostgreSQL bindir must be a real directory: %s", bindir)
	}
	manifest := Manifest{
		SchemaVersion:   SchemaVersion,
		ArtifactType:    ArtifactType,
		SourceCommit:    Unattested,
		BuildProvenance: Unattested,
		Binaries:        make([]Binary, 0, len(requiredExecutables)),
	}
	for _, name := range requiredExecutables {
		path := filepath.Join(bindir, name)
		binaryInfo, err := os.Lstat(path)
		if err != nil {
			return Installation{}, fmt.Errorf("inspect required native PostgreSQL executable %s: %w", name, err)
		}
		if !binaryInfo.Mode().IsRegular() || binaryInfo.Mode()&os.ModeSymlink != 0 || binaryInfo.Mode().Perm()&0o111 == 0 {
			return Installation{}, fmt.Errorf("required native PostgreSQL executable must be an executable regular non-symlink file: %s", path)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return Installation{}, fmt.Errorf("digest native PostgreSQL executable %s: %w", name, err)
		}
		version, err := observeVersion(path)
		if err != nil {
			return Installation{}, fmt.Errorf("observe native PostgreSQL executable %s version: %w", name, err)
		}
		postVersionInfo, err := os.Lstat(path)
		if err != nil || !postVersionInfo.Mode().IsRegular() || postVersionInfo.Mode()&os.ModeSymlink != 0 || postVersionInfo.Mode().Perm()&0o111 == 0 || postVersionInfo.Size() != binaryInfo.Size() {
			return Installation{}, fmt.Errorf("native PostgreSQL executable %s changed while observing its version", name)
		}
		postVersionDigest, err := evidence.DigestFile(path)
		if err != nil || postVersionDigest != digest {
			return Installation{}, fmt.Errorf("native PostgreSQL executable %s changed while observing its version", name)
		}
		manifest.Binaries = append(manifest.Binaries, Binary{
			Name: name, Path: filepath.ToSlash(filepath.Join("bin", name)),
			Digest: digest, Size: binaryInfo.Size(), Version: version,
		})
	}
	manifest.Digest, err = manifestDigest(manifest)
	if err != nil {
		return Installation{}, err
	}
	if err := VerifyManifest(manifest); err != nil {
		return Installation{}, err
	}
	return Installation{Bindir: bindir, Manifest: manifest}, nil
}

// Revalidate proves that the local installation still has the exact byte and
// observed-version identity captured before reservation.
func Revalidate(installation Installation) error {
	current, err := Inspect(installation.Bindir)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current.Manifest, installation.Manifest) {
		return fmt.Errorf("native PostgreSQL toolchain changed after protocol construction")
	}
	return nil
}

// Snapshot copies only the seven identity-bearing executable bytes. It is a
// portable verification closure, not a relocatable PostgreSQL installation.
func Snapshot(installation Installation, destination string) error {
	if err := Revalidate(installation); err != nil {
		return err
	}
	if info, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("native toolchain snapshot already exists: %s (%s)", destination, info.Mode())
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(destination, "bin"), 0o755); err != nil {
		return err
	}
	// Do not let a caller's umask silently make an artifact that the strict
	// verifier will reject. Snapshot modes are part of the closed identity-only
	// representation, so set them explicitly after creation.
	if err := os.Chmod(destination, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(destination, "bin"), 0o755); err != nil {
		return err
	}
	for _, binary := range installation.Manifest.Binaries {
		source := filepath.Join(installation.Bindir, binary.Name)
		target := filepath.Join(destination, filepath.FromSlash(binary.Path))
		if err := copyExact(source, target, binary); err != nil {
			return fmt.Errorf("snapshot native PostgreSQL executable %s: %w", binary.Name, err)
		}
	}
	content, err := json.MarshalIndent(installation.Manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileExclusive(filepath.Join(destination, ManifestName), append(content, '\n'), 0o644); err != nil {
		return err
	}
	_, err = VerifySnapshot(destination, installation.Manifest.Digest)
	return err
}

func VerifySnapshot(directory, expectedDigest string) (Manifest, error) {
	info, err := os.Lstat(directory)
	if err != nil || info.Mode() != os.ModeDir|0o755 {
		return Manifest{}, fmt.Errorf("native toolchain snapshot is missing or unsafe: %s", directory)
	}
	manifestPath := filepath.Join(directory, ManifestName)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || manifestInfo.Mode() != 0o644 {
		return Manifest{}, fmt.Errorf("native toolchain manifest is missing or unsafe")
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	if err := rejectDuplicateKeys(content); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse native toolchain manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("native toolchain manifest contains trailing JSON value")
		}
		return Manifest{}, fmt.Errorf("native toolchain manifest trailing JSON: %w", err)
	}
	if err := VerifyManifest(manifest); err != nil {
		return Manifest{}, err
	}
	if expectedDigest != "" && manifest.Digest != expectedDigest {
		return Manifest{}, fmt.Errorf("native toolchain manifest identity mismatch")
	}
	expected := map[string]Binary{}
	for _, binary := range manifest.Binaries {
		expected[binary.Path] = binary
		path := filepath.Join(directory, filepath.FromSlash(binary.Path))
		binaryInfo, err := os.Lstat(path)
		if err != nil || binaryInfo.Mode() != 0o644 || binaryInfo.Size() != binary.Size {
			return Manifest{}, fmt.Errorf("native toolchain snapshot binary is missing or unsafe: %s", binary.Path)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil || digest != binary.Digest {
			return Manifest{}, fmt.Errorf("native toolchain snapshot binary digest mismatch: %s", binary.Path)
		}
	}
	seen := map[string]struct{}{ManifestName: {}}
	for path := range expected {
		seen[path] = struct{}{}
	}
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil || relative == "." {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("native toolchain snapshot contains symlink: %s", relative)
		}
		if entry.IsDir() {
			entryInfo, infoErr := os.Lstat(path)
			if relative != "bin" || infoErr != nil || entryInfo.Mode() != os.ModeDir|0o755 {
				return fmt.Errorf("native toolchain snapshot contains unexpected directory: %s", relative)
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("native toolchain snapshot contains non-regular file: %s", relative)
		}
		if _, ok := seen[relative]; !ok {
			return fmt.Errorf("native toolchain snapshot contains unexpected file: %s", relative)
		}
		return nil
	})
	return manifest, err
}

func VerifyManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion || manifest.ArtifactType != ArtifactType {
		return fmt.Errorf("unsupported native toolchain manifest schema or artifact type")
	}
	if manifest.SourceCommit != Unattested || manifest.BuildProvenance != Unattested {
		return fmt.Errorf("native toolchain source and build provenance must remain unattested")
	}
	if len(manifest.Binaries) != len(requiredExecutables) {
		return fmt.Errorf("native toolchain manifest requires exactly %d executables", len(requiredExecutables))
	}
	wantNames := append([]string(nil), requiredExecutables...)
	sort.Strings(wantNames)
	for index, binary := range manifest.Binaries {
		if binary.Name != wantNames[index] || binary.Path != filepath.ToSlash(filepath.Join("bin", binary.Name)) || !evidence.IsPortablePath(binary.Path) || !evidence.IsDigest(binary.Digest) || binary.Size <= 0 || binary.Version == "" || strings.ContainsAny(binary.Version, "\r\n\x00") {
			return fmt.Errorf("invalid native toolchain binary identity at position %d", index+1)
		}
	}
	digest, err := manifestDigest(manifest)
	if err != nil || digest != manifest.Digest {
		return fmt.Errorf("native toolchain manifest digest mismatch")
	}
	return nil
}

func observeVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("version command timed out")
	}
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(output))
	if version == "" || len(version) > 1024 || strings.ContainsAny(version, "\r\n\x00") {
		return "", fmt.Errorf("version output is empty, multiline, or too large")
	}
	return version, nil
}

func manifestDigest(manifest Manifest) (string, error) {
	copy := manifest
	copy.Digest = ""
	content, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func copyExact(source, destination string, expected Binary) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("source changed type or permissions")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		_ = input.Close()
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		_ = input.Close()
		return err
	}
	if err := output.Chmod(0o644); err != nil {
		_ = input.Close()
		_ = output.Close()
		return err
	}
	_, copyErr := io.Copy(output, input)
	inputCloseErr := input.Close()
	outputCloseErr := output.Close()
	if copyErr != nil || inputCloseErr != nil || outputCloseErr != nil {
		return fmt.Errorf("copy failed: %v", []error{copyErr, inputCloseErr, outputCloseErr})
	}
	copiedInfo, err := os.Lstat(destination)
	if err != nil || copiedInfo.Size() != expected.Size {
		return fmt.Errorf("copied size mismatch")
	}
	digest, err := evidence.DigestFile(destination)
	if err != nil || digest != expected.Digest {
		return fmt.Errorf("copied digest mismatch")
	}
	currentDigest, err := evidence.DigestFile(source)
	if err != nil || currentDigest != expected.Digest {
		return fmt.Errorf("source changed while snapshotting")
	}
	return nil
}

func writeFileExclusive(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(content)
	return errorsJoin(writeErr, file.Close())
}

func errorsJoin(errors ...error) error {
	var messages []string
	for _, err := range errors {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}

func rejectDuplicateKeys(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid JSON object closing token")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array closing token")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}
