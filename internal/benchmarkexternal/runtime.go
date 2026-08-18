package benchmarkexternal

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkimport"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

type stagedDriverRuntime struct {
	binding        DriverRuntime
	commandPath    string
	commandDir     string
	binaryPath     string
	scriptPath     string
	extraEnv       []string
	sourceExecutor bool
}

func resolveDriverRuntimeRoot(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("external driver --runtime-root is required")
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", fmt.Errorf("external driver runtime root must be an absolute clean path")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve external driver runtime root: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect external driver runtime root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("external driver runtime root must be a non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve external driver runtime root: %w", err)
	}
	return resolved, nil
}

func stageDriverRuntime(stage string, driver benchmarkdrivers.Driver, options Options, binary, script []byte, secret string) (stagedDriverRuntime, error) {
	switch driver.Adapter {
	case benchmarkimport.AdapterBenchBase:
		return stageBenchBaseRuntime(stage, options, binary, script, secret)
	case benchmarkimport.AdapterHammerDB6:
		return stageHammerDBRuntime(stage, options, binary, secret)
	case benchmarkimport.AdapterSysbench1:
		return stageSysbenchRuntime(stage, options, binary, script, secret)
	default:
		return stagedDriverRuntime{}, fmt.Errorf("runtime staging is not implemented for adapter %q", driver.Adapter)
	}
}

func stageBenchBaseRuntime(stage string, options Options, binary, script []byte, secret string) (stagedDriverRuntime, error) {
	entryRelative, err := pathWithinRoot(options.RuntimeRoot, options.ScriptPath, "BenchBase JAR")
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	if filepath.ToSlash(entryRelative) != "benchbase.jar" {
		return stagedDriverRuntime{}, fmt.Errorf("BenchBase --script must be <runtime-root>/benchbase.jar")
	}
	files, err := stageBenchBaseManifestClosure(stage, options.RuntimeRoot, entryRelative, script, secret)
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	pluginSource := filepath.Join(options.RuntimeRoot, "config", "plugin.xml")
	if err := ensureNoSymlinkPath(options.RuntimeRoot, pluginSource); err != nil {
		return stagedDriverRuntime{}, fmt.Errorf("unsafe BenchBase config/plugin.xml: %w", err)
	}
	plugin, err := readRegular(pluginSource, maxInputBytes, false, "BenchBase config/plugin.xml")
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	if secret != "" && bytes.Contains(plugin, []byte(secret)) {
		return stagedDriverRuntime{}, fmt.Errorf("BenchBase config/plugin.xml contains bytes from %s", SecretPasswordEnv)
	}
	pluginRef, err := writeRuntimeFile(stage, path.Join(DriverRuntimeDir, "config/plugin.xml"), plugin, 0o444)
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	files = append(files, pluginRef)
	if err := requireExactSourceRuntime(options.RuntimeRoot, files, DriverRuntimeDir); err != nil {
		return stagedDriverRuntime{}, fmt.Errorf("BenchBase runtime root is not the exact curated closure: %w", err)
	}
	entrypoint := path.Join(DriverRuntimeDir, "benchbase.jar")
	binding, err := finalizeDriverRuntime(BenchBaseRuntimeStrategy, entrypoint, files)
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	return stagedDriverRuntime{
		binding: binding, commandPath: options.BinaryPath,
		commandDir: filepath.Join(stage, filepath.FromSlash(DriverRuntimeDir)),
		binaryPath: BinaryFile, scriptPath: entrypoint, sourceExecutor: true,
	}, nil
}

func stageBenchBaseManifestClosure(stage, sourceRoot, entryRelative string, entryContent []byte, secret string) ([]RuntimeFileRef, error) {
	queue := []string{filepath.ToSlash(entryRelative)}
	discovered := map[string]struct{}{filepath.ToSlash(entryRelative): {}}
	seen := map[string]struct{}{}
	files := []RuntimeFileRef{}
	var total int64
	for len(queue) != 0 {
		relative := queue[0]
		queue = queue[1:]
		if _, exists := seen[relative]; exists {
			continue
		}
		seen[relative] = struct{}{}
		if len(seen) > maxDriverRuntimeFiles {
			return nil, fmt.Errorf("BenchBase manifest closure exceeds %d files", maxDriverRuntimeFiles)
		}
		source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
		if err := ensureNoSymlinkPath(sourceRoot, source); err != nil {
			return nil, fmt.Errorf("unsafe BenchBase classpath entry %q: %w", relative, err)
		}
		content := entryContent
		if relative != filepath.ToSlash(entryRelative) {
			var err error
			content, err = readRegular(source, maxInputBytes, false, "BenchBase classpath JAR")
			if err != nil {
				return nil, err
			}
		}
		if relative == filepath.ToSlash(entryRelative) && !bytes.Equal(content, entryContent) {
			return nil, fmt.Errorf("BenchBase entrypoint changed while constructing its runtime closure")
		}
		if secret != "" && bytes.Contains(content, []byte(secret)) {
			return nil, fmt.Errorf("BenchBase runtime closure contains bytes from %s", SecretPasswordEnv)
		}
		total += int64(len(content))
		if total > maxDriverRuntimeBytes {
			return nil, fmt.Errorf("BenchBase manifest closure exceeds %d bytes", maxDriverRuntimeBytes)
		}
		artifactPath := path.Join(DriverRuntimeDir, relative)
		ref, err := writeRuntimeFile(stage, artifactPath, content, 0o444)
		if err != nil {
			return nil, fmt.Errorf("stage BenchBase runtime %s: %w", relative, err)
		}
		files = append(files, ref)
		entries, err := jarManifestClassPath(content)
		if err != nil {
			return nil, fmt.Errorf("parse BenchBase runtime JAR %s: %w", relative, err)
		}
		if relative == filepath.ToSlash(entryRelative) && len(entries) == 0 {
			return nil, fmt.Errorf("BenchBase entrypoint JAR must declare a non-empty manifest Class-Path")
		}
		for _, entry := range entries {
			resolved, err := resolveManifestClassPath(relative, entry)
			if err != nil {
				return nil, fmt.Errorf("BenchBase runtime JAR %s: %w", relative, err)
			}
			if !strings.HasPrefix(resolved, "lib/") {
				return nil, fmt.Errorf("BenchBase manifest dependency %q is outside the curated lib tree", resolved)
			}
			if _, exists := discovered[resolved]; !exists {
				if len(discovered) >= maxDriverRuntimeFiles {
					return nil, fmt.Errorf("BenchBase manifest closure exceeds %d files", maxDriverRuntimeFiles)
				}
				discovered[resolved] = struct{}{}
				queue = append(queue, resolved)
			}
		}
	}
	return files, nil
}

func stageSysbenchRuntime(stage string, options Options, binary, script []byte, secret string) (stagedDriverRuntime, error) {
	if _, err := pathWithinRoot(options.RuntimeRoot, options.BinaryPath, "sysbench executable"); err != nil {
		return stagedDriverRuntime{}, err
	}
	if _, err := pathWithinRoot(options.RuntimeRoot, options.ScriptPath, "sysbench workload"); err != nil {
		return stagedDriverRuntime{}, err
	}
	expectedScript, err := sysbenchScriptName(options.Workload)
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	commonPath := filepath.Join(filepath.Dir(options.ScriptPath), "oltp_common.lua")
	if err := ensureNoSymlinkPath(options.RuntimeRoot, commonPath); err != nil {
		return stagedDriverRuntime{}, fmt.Errorf("unsafe sysbench sibling runtime: %w", err)
	}
	common, err := readRegular(commonPath, maxInputBytes, false, "sysbench oltp_common.lua")
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	for label, content := range map[string][]byte{"binary": binary, "workload": script, "oltp_common.lua": common} {
		if secret != "" && bytes.Contains(content, []byte(secret)) {
			return stagedDriverRuntime{}, fmt.Errorf("sysbench runtime %s contains bytes from %s", label, SecretPasswordEnv)
		}
	}
	binaryPath := path.Join(DriverRuntimeDir, "bin/sysbench")
	scriptPath := path.Join(DriverRuntimeDir, "share/sysbench", expectedScript)
	commonRuntimePath := path.Join(DriverRuntimeDir, "share/sysbench/oltp_common.lua")
	files := make([]RuntimeFileRef, 0, 3)
	for _, input := range []struct {
		path    string
		content []byte
		mode    os.FileMode
	}{{binaryPath, binary, 0o555}, {scriptPath, script, 0o444}, {commonRuntimePath, common, 0o444}} {
		ref, err := writeRuntimeFile(stage, input.path, input.content, input.mode)
		if err != nil {
			return stagedDriverRuntime{}, fmt.Errorf("stage sysbench runtime %s: %w", input.path, err)
		}
		files = append(files, ref)
	}
	if err := requireExactSourceRuntime(options.RuntimeRoot, files, DriverRuntimeDir); err != nil {
		return stagedDriverRuntime{}, fmt.Errorf("sysbench runtime root is not the exact curated closure: %w", err)
	}
	binding, err := finalizeDriverRuntime(SysbenchRuntimeStrategy, binaryPath, files)
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	return stagedDriverRuntime{
		binding: binding, commandPath: filepath.Join(stage, filepath.FromSlash(binaryPath)),
		binaryPath: binaryPath, scriptPath: scriptPath,
		extraEnv: []string{"LUA_PATH=" + filepath.Join(stage, filepath.FromSlash(DriverRuntimeDir), "share", "sysbench", "?.lua")},
	}, nil
}

func stageHammerDBRuntime(stage string, options Options, binary []byte, secret string) (stagedDriverRuntime, error) {
	binaryRelative, err := pathWithinRoot(options.RuntimeRoot, options.BinaryPath, "HammerDB launcher")
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	if filepath.ToSlash(binaryRelative) != "hammerdbcli" {
		return stagedDriverRuntime{}, fmt.Errorf("HammerDB --binary must be <runtime-root>/hammerdbcli")
	}
	count := 0
	err = filepath.WalkDir(options.RuntimeRoot, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if source == options.RuntimeRoot {
			return nil
		}
		count++
		if filepath.ToSlash(strings.TrimPrefix(source, options.RuntimeRoot+string(filepath.Separator))) != "hammerdbcli" || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("HammerDB runtime root must contain exactly one regular file named hammerdbcli")
		}
		return nil
	})
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	if count != 1 {
		return stagedDriverRuntime{}, fmt.Errorf("HammerDB runtime root must contain exactly one regular file named hammerdbcli")
	}
	if secret != "" && bytes.Contains(binary, []byte(secret)) {
		return stagedDriverRuntime{}, fmt.Errorf("HammerDB launcher contains bytes from %s", SecretPasswordEnv)
	}
	entrypoint := path.Join(DriverRuntimeDir, "hammerdbcli")
	entryRef, err := writeRuntimeFile(stage, entrypoint, binary, 0o555)
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	files := []RuntimeFileRef{entryRef}
	binding, err := finalizeDriverRuntime(HammerDBRuntimeStrategy, entrypoint, files)
	if err != nil {
		return stagedDriverRuntime{}, err
	}
	entryRef, exists := runtimeFileByPath(binding.Files, entrypoint)
	if !exists || entryRef.Digest != evidence.DigestBytes(binary) || entryRef.SizeBytes != int64(len(binary)) || entryRef.Mode != 0o555 {
		return stagedDriverRuntime{}, fmt.Errorf("HammerDB launcher changed while staging its distribution")
	}
	runtimeRoot := filepath.Join(stage, filepath.FromSlash(DriverRuntimeDir))
	return stagedDriverRuntime{
		binding: binding, commandPath: filepath.Join(stage, filepath.FromSlash(entrypoint)),
		commandDir: runtimeRoot, binaryPath: entrypoint, scriptPath: "inputs/adapter-template.txt",
	}, nil
}

func writeRuntimeFile(stage, artifactPath string, content []byte, mode os.FileMode) (RuntimeFileRef, error) {
	if !evidence.IsPortablePath(artifactPath) || !strings.HasPrefix(artifactPath, DriverRuntimeDir+"/") {
		return RuntimeFileRef{}, fmt.Errorf("invalid staged runtime path %q", artifactPath)
	}
	destination := filepath.Join(stage, filepath.FromSlash(artifactPath))
	if err := writeBytesExclusive(destination, content, mode); err != nil {
		return RuntimeFileRef{}, err
	}
	if err := os.Chmod(destination, mode); err != nil {
		return RuntimeFileRef{}, err
	}
	return RuntimeFileRef{Path: artifactPath, Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content)), Mode: uint32(mode.Perm())}, nil
}

func finalizeDriverRuntime(strategy, entrypoint string, files []RuntimeFileRef) (DriverRuntime, error) {
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	var total int64
	seen := map[string]struct{}{}
	for _, file := range files {
		if _, exists := seen[file.Path]; exists {
			return DriverRuntime{}, fmt.Errorf("driver runtime contains duplicate path %s", file.Path)
		}
		seen[file.Path] = struct{}{}
		if file.SizeBytes < 0 || file.SizeBytes > maxInputBytes || total > maxDriverRuntimeBytes-file.SizeBytes {
			return DriverRuntime{}, fmt.Errorf("driver runtime violates per-file or aggregate-size bounds")
		}
		total += file.SizeBytes
	}
	if len(files) == 0 || len(files) > maxDriverRuntimeFiles || total > maxDriverRuntimeBytes {
		return DriverRuntime{}, fmt.Errorf("driver runtime violates file-count or aggregate-size bounds")
	}
	if _, exists := seen[entrypoint]; !exists {
		return DriverRuntime{}, fmt.Errorf("driver runtime entrypoint %s is absent", entrypoint)
	}
	digest, err := runtimeTreeDigest(files)
	if err != nil {
		return DriverRuntime{}, err
	}
	return DriverRuntime{
		Strategy: strategy, Root: DriverRuntimeDir, Entrypoint: entrypoint,
		Files: files, FileCount: len(files), TotalSizeBytes: total, TreeDigest: digest,
	}, nil
}

func runtimeTreeDigest(files []RuntimeFileRef) (string, error) {
	content, err := json.Marshal(files)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func verifyStagedRuntimeIdentity(stage string, binding DriverRuntime) error {
	actual, err := scanRuntimeTree(stage, binding.Root)
	if err != nil {
		return err
	}
	derived, err := finalizeDriverRuntime(binding.Strategy, binding.Entrypoint, actual)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(derived, binding) {
		return fmt.Errorf("staged driver runtime changed during execution")
	}
	return nil
}

func freezeStagedRuntime(stage string) error {
	// Files are already canonicalized to read-only 0444/0555. Directories stay
	// traversable/writable so ordinary artifact cleanup remains portable; any
	// addition or replacement is rejected by the post-execution tree identity.
	return verifyRuntimeFileModes(stage)
}

func thawStagedRuntime(stage string) error {
	return nil
}

func verifyRuntimeFileModes(stage string) error {
	files, err := scanRuntimeTree(stage, DriverRuntimeDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.Mode != 0o444 && file.Mode != 0o555 {
			return fmt.Errorf("staged runtime file %s is not read-only", file.Path)
		}
	}
	return nil
}

func scanRuntimeTree(artifactRoot, runtimeRoot string) ([]RuntimeFileRef, error) {
	if runtimeRoot != DriverRuntimeDir {
		return nil, fmt.Errorf("driver runtime root must be %s", DriverRuntimeDir)
	}
	root := filepath.Join(artifactRoot, filepath.FromSlash(runtimeRoot))
	files := []RuntimeFileRef{}
	var total int64
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("driver runtime contains unsafe entry: %s", filePath)
		}
		if len(files) >= maxDriverRuntimeFiles {
			return fmt.Errorf("driver runtime exceeds %d files", maxDriverRuntimeFiles)
		}
		content, err := readRegular(filePath, maxInputBytes, true, "driver runtime file")
		if err != nil {
			return err
		}
		total += int64(len(content))
		if total > maxDriverRuntimeBytes {
			return fmt.Errorf("driver runtime exceeds %d bytes", maxDriverRuntimeBytes)
		}
		reference, err := relativePortable(artifactRoot, filePath)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, RuntimeFileRef{
			Path: reference, Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content)), Mode: uint32(info.Mode().Perm()),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

func deriveRetainedRuntime(artifactRoot string, driver benchmarkdrivers.Driver, workload string, recorded DriverRuntime) (DriverRuntime, error) {
	if recorded.Root != DriverRuntimeDir {
		return DriverRuntime{}, fmt.Errorf("driver runtime root must be %s", DriverRuntimeDir)
	}
	var files []RuntimeFileRef
	var expectedStrategy, expectedEntrypoint string
	var err error
	switch driver.Adapter {
	case benchmarkimport.AdapterBenchBase:
		expectedStrategy = BenchBaseRuntimeStrategy
		expectedEntrypoint = recorded.Entrypoint
		files, err = deriveRetainedBenchBaseClosure(artifactRoot, recorded.Entrypoint)
	case benchmarkimport.AdapterHammerDB6:
		expectedStrategy = HammerDBRuntimeStrategy
		expectedEntrypoint = path.Join(DriverRuntimeDir, "hammerdbcli")
		files, err = deriveRetainedHammerDBRuntime(artifactRoot)
	case benchmarkimport.AdapterSysbench1:
		expectedStrategy = SysbenchRuntimeStrategy
		expectedEntrypoint = path.Join(DriverRuntimeDir, "bin/sysbench")
		files, err = deriveRetainedSysbenchRuntime(artifactRoot, workload)
	default:
		return DriverRuntime{}, fmt.Errorf("unsupported driver runtime adapter %q", driver.Adapter)
	}
	if err != nil {
		return DriverRuntime{}, err
	}
	if recorded.Strategy != expectedStrategy || recorded.Entrypoint != expectedEntrypoint {
		return DriverRuntime{}, fmt.Errorf("driver runtime strategy or entrypoint does not match its pinned adapter")
	}
	return finalizeDriverRuntime(expectedStrategy, expectedEntrypoint, files)
}

func deriveRetainedBenchBaseClosure(artifactRoot, entrypoint string) ([]RuntimeFileRef, error) {
	if entrypoint != path.Join(DriverRuntimeDir, "benchbase.jar") {
		return nil, fmt.Errorf("invalid retained BenchBase entrypoint")
	}
	entryRelative := strings.TrimPrefix(entrypoint, DriverRuntimeDir+"/")
	queue := []string{entryRelative}
	discovered := map[string]struct{}{entryRelative: {}}
	seen := map[string]struct{}{}
	files := []RuntimeFileRef{}
	var total int64
	for len(queue) != 0 {
		relative := queue[0]
		queue = queue[1:]
		if _, exists := seen[relative]; exists {
			continue
		}
		seen[relative] = struct{}{}
		artifactPath := path.Join(DriverRuntimeDir, relative)
		absolute := filepath.Join(artifactRoot, filepath.FromSlash(artifactPath))
		content, err := readRegular(absolute, maxInputBytes, false, "retained BenchBase runtime JAR")
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			return nil, err
		}
		files = append(files, RuntimeFileRef{Path: artifactPath, Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content)), Mode: uint32(info.Mode().Perm())})
		total += int64(len(content))
		if total > maxDriverRuntimeBytes {
			return nil, fmt.Errorf("retained BenchBase manifest closure exceeds %d bytes", maxDriverRuntimeBytes)
		}
		entries, err := jarManifestClassPath(content)
		if err != nil {
			return nil, err
		}
		if relative == entryRelative && len(entries) == 0 {
			return nil, fmt.Errorf("retained BenchBase entrypoint has no manifest Class-Path")
		}
		for _, entry := range entries {
			resolved, err := resolveManifestClassPath(relative, entry)
			if err != nil {
				return nil, err
			}
			if !strings.HasPrefix(resolved, "lib/") {
				return nil, fmt.Errorf("retained BenchBase manifest dependency %q is outside the curated lib tree", resolved)
			}
			if _, exists := discovered[resolved]; !exists {
				if len(discovered) >= maxDriverRuntimeFiles {
					return nil, fmt.Errorf("retained BenchBase manifest closure exceeds %d files", maxDriverRuntimeFiles)
				}
				discovered[resolved] = struct{}{}
				queue = append(queue, resolved)
			}
		}
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	pluginPath := path.Join(DriverRuntimeDir, "config/plugin.xml")
	pluginAbsolute := filepath.Join(artifactRoot, filepath.FromSlash(pluginPath))
	plugin, err := readRegular(pluginAbsolute, maxInputBytes, false, "retained BenchBase config/plugin.xml")
	if err != nil {
		return nil, err
	}
	pluginInfo, err := os.Lstat(pluginAbsolute)
	if err != nil {
		return nil, err
	}
	files = append(files, RuntimeFileRef{Path: pluginPath, Digest: evidence.DigestBytes(plugin), SizeBytes: int64(len(plugin)), Mode: uint32(pluginInfo.Mode().Perm())})
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

func deriveRetainedHammerDBRuntime(artifactRoot string) ([]RuntimeFileRef, error) {
	reference := path.Join(DriverRuntimeDir, "hammerdbcli")
	absolute := filepath.Join(artifactRoot, filepath.FromSlash(reference))
	content, err := readRegular(absolute, maxInputBytes, false, "retained HammerDB launcher")
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	return []RuntimeFileRef{{Path: reference, Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content)), Mode: uint32(info.Mode().Perm())}}, nil
}

func deriveRetainedSysbenchRuntime(artifactRoot, workload string) ([]RuntimeFileRef, error) {
	script, err := sysbenchScriptName(workload)
	if err != nil {
		return nil, err
	}
	paths := []string{
		path.Join(DriverRuntimeDir, "bin/sysbench"),
		path.Join(DriverRuntimeDir, "share/sysbench/oltp_common.lua"),
		path.Join(DriverRuntimeDir, "share/sysbench", script),
	}
	files := make([]RuntimeFileRef, 0, len(paths))
	for _, reference := range paths {
		absolute := filepath.Join(artifactRoot, filepath.FromSlash(reference))
		content, err := readRegular(absolute, maxInputBytes, false, "retained sysbench runtime file")
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			return nil, err
		}
		files = append(files, RuntimeFileRef{Path: reference, Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content)), Mode: uint32(info.Mode().Perm())})
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

func sysbenchScriptName(workload string) (string, error) {
	name := strings.TrimSuffix(workload, "/postgresql") + ".lua"
	if filepath.Base(name) != name || name == ".lua" {
		return "", fmt.Errorf("sysbench workload does not map to one portable Lua script")
	}
	return name, nil
}

func runtimeFileByPath(files []RuntimeFileRef, value string) (RuntimeFileRef, bool) {
	for _, file := range files {
		if file.Path == value {
			return file, true
		}
	}
	return RuntimeFileRef{}, false
}

func pathWithinRoot(root, candidate, label string) (string, error) {
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%s must be a regular non-symlink file below --runtime-root: %w", label, err)
	}
	if err := ensureNoSymlinkPath(root, resolvedCandidate); err != nil {
		return "", fmt.Errorf("%s must be a regular non-symlink file below --runtime-root: %w", label, err)
	}
	relative, err := filepath.Rel(root, resolvedCandidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s must be below --runtime-root", label)
	}
	if !evidence.IsPortablePath(filepath.ToSlash(relative)) {
		return "", fmt.Errorf("%s path below --runtime-root is not portable", label)
	}
	return relative, nil
}

func isPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func requireExactSourceRuntime(sourceRoot string, staged []RuntimeFileRef, stagedRoot string) error {
	expected := map[string]struct{}{}
	expectedDirectories := map[string]struct{}{}
	for _, file := range staged {
		relative := strings.TrimPrefix(file.Path, stagedRoot+"/")
		expected[relative] = struct{}{}
		for directory := path.Dir(relative); directory != "."; directory = path.Dir(directory) {
			expectedDirectories[directory] = struct{}{}
		}
	}
	actual := map[string]struct{}{}
	actualDirectories := map[string]struct{}{}
	err := filepath.WalkDir(sourceRoot, func(candidate string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if candidate == sourceRoot {
			return nil
		}
		reference, err := relativePortable(sourceRoot, candidate)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			actualDirectories[reference] = struct{}{}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("unsafe entry %s", candidate)
		}
		actual[reference] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("expected exactly %v, got %v", sortedStringSet(expected), sortedStringSet(actual))
	}
	if !reflect.DeepEqual(actualDirectories, expectedDirectories) {
		return fmt.Errorf("expected exactly directories %v, got %v", sortedStringSet(expectedDirectories), sortedStringSet(actualDirectories))
	}
	return nil
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func ensureNoSymlinkPath(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes runtime root")
	}
	current := root
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component is a symlink: %s", current)
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("path is not a regular file")
		}
	}
	return nil
}

func jarManifestClassPath(content []byte) ([]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("invalid JAR/ZIP: %w", err)
	}
	if len(reader.File) > maxJAREntries {
		return nil, fmt.Errorf("JAR contains more than %d entries", maxJAREntries)
	}
	var manifest *zip.File
	for _, file := range reader.File {
		if strings.EqualFold(file.Name, "META-INF/MANIFEST.MF") {
			if file.Name != "META-INF/MANIFEST.MF" {
				return nil, fmt.Errorf("JAR contains a non-canonical case variant of META-INF/MANIFEST.MF")
			}
			if manifest != nil {
				return nil, fmt.Errorf("JAR contains duplicate META-INF/MANIFEST.MF entries")
			}
			manifest = file
		}
	}
	if manifest == nil {
		return nil, fmt.Errorf("JAR has no canonical META-INF/MANIFEST.MF")
	}
	if manifest.UncompressedSize64 > uint64(maxManifestBytes) {
		return nil, fmt.Errorf("JAR manifest exceeds %d bytes", maxManifestBytes)
	}
	stream, err := manifest.Open()
	if err != nil {
		return nil, err
	}
	manifestContent, readErr := io.ReadAll(io.LimitReader(stream, maxManifestBytes+1))
	closeErr := stream.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(manifestContent)) > maxManifestBytes {
		return nil, fmt.Errorf("JAR manifest exceeds %d bytes", maxManifestBytes)
	}
	attributes, err := parseManifestMainSection(manifestContent)
	if err != nil {
		return nil, err
	}
	value, exists := attributes["class-path"]
	if !exists {
		return nil, nil
	}
	if value == "" || strings.TrimSpace(value) != value || strings.Contains(value, "  ") || strings.ContainsAny(value, "\t\v\f\r\n") {
		return nil, fmt.Errorf("manifest Class-Path must be a non-empty single-ASCII-space-delimited list")
	}
	entries := strings.Split(value, " ")
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if _, exists := seen[entry]; exists {
			return nil, fmt.Errorf("manifest Class-Path contains duplicate entry %q", entry)
		}
		seen[entry] = struct{}{}
		if _, err := resolveManifestClassPath("entrypoint.jar", entry); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func parseManifestMainSection(content []byte) (map[string]string, error) {
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, fmt.Errorf("manifest must be UTF-8 without NUL bytes")
	}
	lines := splitManifestLines(content)
	attributes := map[string]string{}
	lastName := ""
	terminated := false
	for index, line := range lines {
		if len(line) > 72 {
			return nil, fmt.Errorf("manifest physical line %d exceeds 72 bytes", index+1)
		}
		if len(line) == 0 {
			terminated = true
			break
		}
		if line[0] == ' ' {
			if lastName == "" {
				return nil, fmt.Errorf("manifest continuation line has no preceding header")
			}
			value := attributes[lastName] + string(line[1:])
			if len(value) > 65535 {
				return nil, fmt.Errorf("manifest header value exceeds 65535 bytes")
			}
			attributes[lastName] = value
			continue
		}
		colon := bytes.IndexByte(line, ':')
		if colon < 1 || colon+1 >= len(line) || line[colon+1] != ' ' {
			return nil, fmt.Errorf("manifest line %d is not a strict name-colon-space header", index+1)
		}
		nameBytes := line[:colon]
		for _, character := range nameBytes {
			if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
				return nil, fmt.Errorf("manifest header name is invalid")
			}
		}
		name := strings.ToLower(string(nameBytes))
		if _, exists := attributes[name]; exists {
			return nil, fmt.Errorf("manifest repeats header %q in its main section", string(nameBytes))
		}
		attributes[name] = string(line[colon+2:])
		lastName = name
	}
	if !terminated {
		return nil, fmt.Errorf("manifest main section is not terminated by an empty line")
	}
	if len(lines) == 0 || attributes["manifest-version"] != "1.0" || string(lines[0]) != "Manifest-Version: 1.0" {
		return nil, fmt.Errorf("manifest must begin with Manifest-Version")
	}
	return attributes, nil
}

func splitManifestLines(content []byte) [][]byte {
	lines := [][]byte{}
	start := 0
	for index := 0; index < len(content); index++ {
		if content[index] != '\r' && content[index] != '\n' {
			continue
		}
		lines = append(lines, content[start:index])
		if content[index] == '\r' && index+1 < len(content) && content[index+1] == '\n' {
			index++
		}
		start = index + 1
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func resolveManifestClassPath(contextRelative, entry string) (string, error) {
	if len(entry) == 0 || len(entry) > 512 || strings.ContainsAny(entry, "\\:%?#") || strings.HasPrefix(entry, "/") || strings.HasSuffix(entry, "/") {
		return "", fmt.Errorf("manifest Class-Path entry %q is outside the accepted relative JAR subset", entry)
	}
	parts := strings.Split(entry, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("manifest Class-Path entry %q contains an unsafe path segment", entry)
		}
		for _, character := range part {
			if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-') {
				return "", fmt.Errorf("manifest Class-Path entry %q is not a portable Maven-style path", entry)
			}
		}
	}
	if !strings.HasSuffix(strings.ToLower(entry), ".jar") {
		return "", fmt.Errorf("manifest Class-Path entry %q is not a JAR", entry)
	}
	resolved := path.Join(path.Dir(contextRelative), entry)
	if resolved == "." || strings.HasPrefix(resolved, "../") || strings.HasPrefix(resolved, "/") || !evidence.IsPortablePath(resolved) {
		return "", fmt.Errorf("manifest Class-Path entry %q escapes the runtime root", entry)
	}
	return resolved, nil
}
