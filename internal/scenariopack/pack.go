package scenariopack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	SchemaVersion = "pgworkbench.scenario-pack/v1"
	ManifestName  = "pgworkbench-pack.json"
)

type Manifest struct {
	SchemaVersion    string   `json:"schema_version"`
	ID               string   `json:"id"`
	Version          string   `json:"version"`
	EngineConstraint string   `json:"engine_constraint"`
	Description      string   `json:"description,omitempty"`
	Assets           []string `json:"assets"`
}

type File struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}

type Inspection struct {
	SchemaVersion       string               `json:"schema_version"`
	ID                  string               `json:"id"`
	Version             string               `json:"version"`
	Root                string               `json:"root"`
	Digest              string               `json:"digest"`
	Files               []File               `json:"files"`
	Manifest            Manifest             `json:"manifest"`
	EngineCompatibility *EngineCompatibility `json:"engine_compatibility,omitempty"`
}

func Load(root string) (Manifest, error) {
	canonicalRoot, err := canonicalPackRoot(root)
	if err != nil {
		return Manifest{}, err
	}
	path, info, err := securePackPath(canonicalRoot, ManifestName)
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("scenario pack manifest is not a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return manifest, nil
}

func Validate(root string) (Inspection, error) {
	absRoot, err := canonicalPackRoot(root)
	if err != nil {
		return Inspection{}, err
	}
	manifest, err := Load(absRoot)
	if err != nil {
		return Inspection{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return Inspection{}, err
	}
	files, err := inventory(absRoot, manifest.Assets)
	if err != nil {
		return Inspection{}, err
	}
	if len(files) == 0 {
		return Inspection{}, fmt.Errorf("scenario pack has no files")
	}
	digest, err := digestInventory(manifest, files)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{
		SchemaVersion: manifest.SchemaVersion,
		ID:            manifest.ID,
		Version:       manifest.Version,
		Root:          absRoot,
		Digest:        digest,
		Files:         files,
		Manifest:      manifest,
	}, nil
}

// VerifyInventory validates a retained, portable scenario-pack inventory and
// independently recomputes its deterministic digest. It deliberately does not
// require access to the original pack root; callers can therefore use it for
// relocated evidence bundles.
func VerifyInventory(manifest Manifest, files []File, digest string) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("scenario pack has no files")
	}
	for index, file := range files {
		clean, err := cleanRelative(file.Path)
		if err != nil || clean != file.Path || filepath.ToSlash(file.Path) != file.Path {
			return fmt.Errorf("scenario pack inventory path is not canonical: %q", file.Path)
		}
		if index > 0 && files[index-1].Path >= file.Path {
			return fmt.Errorf("scenario pack inventory files must be strictly sorted by path")
		}
		decoded, err := hex.DecodeString(file.SHA256)
		if err != nil || len(decoded) != sha256.Size || strings.ToLower(file.SHA256) != file.SHA256 {
			return fmt.Errorf("scenario pack inventory file %s has an invalid SHA-256 digest", file.Path)
		}
		if file.Size < 0 {
			return fmt.Errorf("scenario pack inventory file %s has a negative size", file.Path)
		}
	}
	want, err := digestInventory(manifest, files)
	if err != nil {
		return err
	}
	if digest != want {
		return fmt.Errorf("scenario pack inventory digest mismatch: got %s want %s", digest, want)
	}
	return nil
}

func ValidateForEngine(root string, engineVersion string) (Inspection, error) {
	inspection, err := Validate(root)
	if err != nil {
		return Inspection{}, err
	}
	return InspectForEngine(inspection, engineVersion)
}

func InspectForEngine(inspection Inspection, engineVersion string) (Inspection, error) {
	compatibility, err := CheckEngineCompatibility(inspection.Manifest, engineVersion)
	inspection.EngineCompatibility = &compatibility
	if err != nil {
		return inspection, err
	}
	return inspection, nil
}

func Copy(sourceRoot string, destinationRoot string) (Inspection, error) {
	return CopyAs(sourceRoot, destinationRoot, "", "")
}

func CopyVersion(sourceRoot string, destinationRoot string, version string) (Inspection, error) {
	return CopyAs(sourceRoot, destinationRoot, "", version)
}

func CopyAs(sourceRoot string, destinationRoot string, id string, version string) (Inspection, error) {
	inspection, err := Validate(sourceRoot)
	if err != nil {
		return Inspection{}, err
	}
	absDestination, err := canonicalDestination(destinationRoot)
	if err != nil {
		return Inspection{}, err
	}
	if filepath.Clean(absDestination) == filepath.Clean(inspection.Root) {
		return Inspection{}, fmt.Errorf("destination must differ from source")
	}
	if err := requireEmptyDestination(absDestination); err != nil {
		return Inspection{}, err
	}
	if err := os.MkdirAll(absDestination, 0o755); err != nil {
		return Inspection{}, err
	}

	for _, file := range inspection.Files {
		source, info, err := securePackPath(inspection.Root, file.Path)
		if err != nil {
			return Inspection{}, fmt.Errorf("copy %s: %w", file.Path, err)
		}
		if !info.Mode().IsRegular() {
			return Inspection{}, fmt.Errorf("copy %s: source is not a regular file", file.Path)
		}
		destination, err := safeJoin(absDestination, file.Path)
		if err != nil {
			return Inspection{}, err
		}
		if err := copyFile(source, destination, file.Executable); err != nil {
			return Inspection{}, fmt.Errorf("copy %s: %w", file.Path, err)
		}
	}
	if strings.TrimSpace(version) == "" && strings.TrimSpace(id) == "" {
		manifestSource, info, err := securePackPath(inspection.Root, ManifestName)
		if err != nil {
			return Inspection{}, fmt.Errorf("copy %s: %w", ManifestName, err)
		}
		if !info.Mode().IsRegular() {
			return Inspection{}, fmt.Errorf("copy %s: source is not a regular file", ManifestName)
		}
		if err := copyFile(manifestSource, filepath.Join(absDestination, ManifestName), false); err != nil {
			return Inspection{}, fmt.Errorf("copy %s: %w", ManifestName, err)
		}
	} else {
		manifest := inspection.Manifest
		if strings.TrimSpace(id) != "" {
			manifest.ID = strings.TrimSpace(id)
		}
		if strings.TrimSpace(version) != "" {
			manifest.Version = strings.TrimSpace(version)
		}
		if err := validateManifest(manifest); err != nil {
			return Inspection{}, err
		}
		content, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return Inspection{}, err
		}
		content = append(content, '\n')
		if err := os.WriteFile(filepath.Join(absDestination, ManifestName), content, 0o644); err != nil {
			return Inspection{}, err
		}
	}

	copied, err := Validate(absDestination)
	if err != nil {
		return Inspection{}, err
	}
	if strings.TrimSpace(version) == "" && strings.TrimSpace(id) == "" && copied.Digest != inspection.Digest {
		return Inspection{}, fmt.Errorf("copied pack digest mismatch: got %s want %s", copied.Digest, inspection.Digest)
	}
	return copied, nil
}

func RenderJSON(w io.Writer, inspection Inspection) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(inspection)
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported scenario pack schema: %q", manifest.SchemaVersion)
	}
	if !validID(manifest.ID) {
		return fmt.Errorf("invalid scenario pack id: %q", manifest.ID)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("scenario pack version is required")
	}
	if !validEngineConstraint(manifest.EngineConstraint) {
		return fmt.Errorf("invalid engine_constraint: %q", manifest.EngineConstraint)
	}
	if len(manifest.Assets) == 0 {
		return fmt.Errorf("scenario pack assets are required")
	}
	seen := make(map[string]struct{}, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		clean, err := cleanRelative(asset)
		if err != nil {
			return fmt.Errorf("invalid scenario pack asset %q: %w", asset, err)
		}
		if clean == ManifestName {
			return fmt.Errorf("%s is included automatically", ManifestName)
		}
		if _, ok := seen[clean]; ok {
			return fmt.Errorf("duplicate scenario pack asset: %s", clean)
		}
		seen[clean] = struct{}{}
	}
	return nil
}

func inventory(root string, assets []string) ([]File, error) {
	filesByPath := make(map[string]File)
	for _, asset := range assets {
		clean, err := cleanRelative(asset)
		if err != nil {
			return nil, err
		}
		path, info, err := securePackPath(root, clean)
		if err != nil {
			return nil, fmt.Errorf("scenario pack asset %s: %w", clean, err)
		}
		if info.Mode().IsRegular() {
			entry, err := inspectFile(root, path, info)
			if err != nil {
				return nil, err
			}
			filesByPath[entry.Path] = entry
			continue
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("scenario pack asset is not a file or directory: %s", clean)
		}
		err = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			candidateInfo, err := entry.Info()
			if err != nil {
				return err
			}
			if candidateInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("scenario pack must not contain symlink: %s", candidate)
			}
			if !candidateInfo.Mode().IsRegular() {
				return fmt.Errorf("scenario pack must contain regular files only: %s", candidate)
			}
			item, err := inspectFile(root, candidate, candidateInfo)
			if err != nil {
				return err
			}
			filesByPath[item.Path] = item
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	files := make([]File, 0, len(filesByPath))
	for _, file := range filesByPath {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func inspectFile(root string, path string, info os.FileInfo) (File, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return File{}, err
	}
	rel, err = cleanRelative(filepath.ToSlash(rel))
	if err != nil {
		return File{}, err
	}
	digest, err := hashFile(path)
	if err != nil {
		return File{}, err
	}
	return File{
		Path:       filepath.ToSlash(rel),
		SHA256:     digest,
		Size:       info.Size(),
		Executable: info.Mode()&0o111 != 0,
	}, nil
}

func digestInventory(manifest Manifest, files []File) (string, error) {
	payload := struct {
		SchemaVersion    string `json:"schema_version"`
		ID               string `json:"id"`
		Version          string `json:"version"`
		EngineConstraint string `json:"engine_constraint"`
		Files            []File `json:"files"`
	}{
		SchemaVersion:    manifest.SchemaVersion,
		ID:               manifest.ID,
		Version:          manifest.Version,
		EngineConstraint: manifest.EngineConstraint,
		Files:            files,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func requireEmptyDestination(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("destination is not empty: %s", path)
	}
	return nil
}

// canonicalDestination resolves symlinks in an existing parent but refuses a
// destination that is itself a symlink. This preserves normal paths such as
// macOS /tmp while preventing pack export from being redirected through a
// pre-positioned destination link.
func canonicalDestination(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	probe := filepath.Clean(absPath)
	missing := make([]string, 0)
	for {
		info, statErr := os.Lstat(probe)
		if statErr == nil {
			if probe == filepath.Clean(absPath) && info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("destination must not be a symlink: %s", absPath)
			}
			resolved, resolveErr := filepath.EvalSymlinks(probe)
			if resolveErr != nil {
				return "", resolveErr
			}
			resolvedInfo, inspectErr := os.Stat(resolved)
			if inspectErr != nil {
				return "", inspectErr
			}
			if !resolvedInfo.IsDir() {
				return "", fmt.Errorf("destination ancestor is not a directory: %s", probe)
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("could not resolve destination ancestor: %s", absPath)
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func copyFile(source string, destination string, executable bool) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}

func cleanRelative(value string) (string, error) {
	value = strings.TrimSpace(filepath.ToSlash(value))
	if value == "" || value == "." {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("path must be relative")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes pack root")
	}
	return clean, nil
}

func safeJoin(root string, relative string) (string, error) {
	clean, err := cleanRelative(relative)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(root, joined)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes pack root: %s", relative)
	}
	return joined, nil
}

func canonicalPackRoot(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve scenario pack root %s: %w", absRoot, err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("inspect scenario pack root %s: %w", resolvedRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("scenario pack root is not a directory: %s", resolvedRoot)
	}
	return filepath.Clean(resolvedRoot), nil
}

func securePackPath(root string, relative string) (string, os.FileInfo, error) {
	joined, err := safeJoin(root, relative)
	if err != nil {
		return "", nil, err
	}
	clean, err := cleanRelative(relative)
	if err != nil {
		return "", nil, err
	}

	current := filepath.Clean(root)
	var info os.FileInfo
	for _, component := range strings.Split(clean, "/") {
		current = filepath.Join(current, filepath.FromSlash(component))
		info, err = os.Lstat(current)
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("scenario pack path must not contain symlink: %s", current)
		}
	}

	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", nil, err
	}
	if !pathWithinRoot(root, resolved) {
		return "", nil, fmt.Errorf("scenario pack path escapes canonical root: %s", relative)
	}
	return resolved, info, nil
}

func pathWithinRoot(root string, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validID(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return false
	}
	return true
}

func validEngineConstraint(value string) bool {
	_, err := parseConstraint(value)
	return err == nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("multiple JSON values")
}
