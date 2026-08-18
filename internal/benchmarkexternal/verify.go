package benchmarkexternal

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkimport"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

// Verify closes the artifact tree, rehashes every retained file, re-parses the
// retained registry, reconstructs the fixed argv from retained inputs, and
// independently re-verifies the nested strict benchmark import.
func Verify(input string) (Verification, error) {
	dir, err := resolveExecution(input)
	if err != nil {
		return Verification{}, err
	}
	verification := Verification{Dir: dir, Issues: []string{}}
	add := func(format string, values ...any) {
		verification.Issues = append(verification.Issues, fmt.Sprintf(format, values...))
	}

	executionContent, executionErr := readRegular(filepath.Join(dir, ExecutionFile), maxJSONBytes, false, "external driver execution")
	if executionErr != nil {
		add("execution.json: %v", executionErr)
		return finishVerification(verification), nil
	}
	var artifact Artifact
	if err := decodeClosedJSON(executionContent, &artifact, "external driver execution"); err != nil {
		add("execution.json parse failed: %v", err)
		return finishVerification(verification), nil
	}
	artifact.ArtifactDir = dir
	verification.Artifact = &artifact
	validateArtifactContract(add, artifact)
	checkDirectoryTree(add, dir, artifact)

	inventoryContent, inventoryErr := readRegular(filepath.Join(dir, InventoryFile), maxJSONBytes, false, "external driver execution inventory")
	if inventoryErr != nil {
		add("inventory.json: %v", inventoryErr)
	} else {
		var inventory Inventory
		if err := decodeClosedJSON(inventoryContent, &inventory, "external driver execution inventory"); err != nil {
			add("inventory.json parse failed: %v", err)
		} else {
			verifyInventory(add, dir, artifact, inventory)
		}
	}

	lockContent, lockErr := readBoundFile(add, dir, artifact.Registry.Lock, maxJSONBytes, false, "retained benchmark driver lock")
	var driver benchmarkdrivers.Driver
	if lockErr == nil {
		if artifact.Registry.Lock.Path != LockFile {
			add("registry lock path must be %s", LockFile)
		}
		registry, err := benchmarkdrivers.Parse(lockContent)
		if err != nil {
			add("retained benchmark driver lock is invalid: %v", err)
		} else {
			driver, err = registry.Find(artifact.Registry.Driver.ID)
			if err != nil {
				add("retained benchmark driver binding failed: %v", err)
			} else if !sameDriver(driver, artifact.Registry.Driver) {
				add("execution driver does not exactly match its retained lock record")
			} else if err := validateExecutionDriver(driver); err != nil {
				add("retained benchmark driver execution pin is invalid: %v", err)
				driver = benchmarkdrivers.Driver{}
			}
		}
	}
	if driver.ID != "" {
		derivedRuntime, err := deriveRetainedRuntime(dir, driver, artifact.Workload, artifact.Inputs.DriverRuntime)
		if err != nil {
			add("independently derive retained driver runtime: %v", err)
		} else if !reflect.DeepEqual(derivedRuntime, artifact.Inputs.DriverRuntime) {
			add("retained driver runtime does not match its independently derived closure")
		}
	}

	_, binaryErr := readBoundFile(add, dir, artifact.Inputs.Binary, maxInputBytes, false, "retained external driver executable")
	if binaryErr == nil {
		binaryInfo, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(artifact.Inputs.Binary.Path)))
		if err != nil || binaryInfo.Mode()&0o111 == 0 {
			add("retained external driver executable has no executable mode")
		}
	}
	config, configErr := readBoundFile(add, dir, artifact.Inputs.Config, maxInputBytes, false, "retained external driver config")
	script, scriptErr := readBoundFile(add, dir, artifact.Inputs.Script, maxInputBytes, false, "retained external driver script")
	stdout, stdoutErr := readBoundFile(add, dir, artifact.Outputs.Stdout, maxOutputBytes, true, "retained external driver stdout")
	stderr, stderrErr := readBoundFile(add, dir, artifact.Outputs.Stderr, maxOutputBytes, true, "retained external driver stderr")
	result, resultErr := readBoundFile(add, dir, artifact.Outputs.DriverResult, maxOutputBytes, false, "retained external driver result")
	_, normalizedErr := readBoundFile(add, dir, artifact.Normalized.Result, maxJSONBytes, false, "retained normalized import result")
	if artifact.Outputs.Stdout.Path != StdoutFile || artifact.Outputs.Stderr.Path != StderrFile || artifact.Normalized.Result.Path != NormalizedImportResult {
		add("execution file references do not use their canonical portable paths")
	}
	validateRuntimeInputBindings(add, artifact, driver)

	if lockErr == nil && configErr == nil && scriptErr == nil && driver.ID != "" {
		secretMarker := ""
		switch {
		case len(artifact.Invocation.SecretEnvironment) == 0:
		case reflect.DeepEqual(artifact.Invocation.SecretEnvironment, []string{SecretPasswordEnv}):
			secretMarker = "verification-only-placeholder"
		default:
			add("secret_environment must be empty or contain only %s", SecretPasswordEnv)
		}
		verificationStage := filepath.Join(dir, ".verification-reconstruction")
		prepared, err := prepareInvocationWithRuntime(verificationStage, driver, artifact.Workload, config, script, secretMarker, artifact.Inputs.DriverRuntime)
		if err != nil {
			add("reconstruct fixed driver invocation: %v", err)
		} else {
			expectedConfig, configPathErr := relativePortable(verificationStage, prepared.configPath)
			expectedScript, scriptPathErr := relativePortable(verificationStage, prepared.scriptPath)
			expectedResult, resultPathErr := relativePortable(verificationStage, prepared.resultPath)
			if configPathErr != nil || scriptPathErr != nil || resultPathErr != nil {
				add("reconstructed driver paths are not portable")
			} else if artifact.Inputs.Config.Path != expectedConfig || artifact.Inputs.Script.Path != expectedScript || artifact.Outputs.DriverResult.Path != expectedResult {
				add("execution input/result paths do not match the pinned adapter contract")
			}
			if !reflect.DeepEqual(artifact.Invocation.Argv, prepared.recordedArgv) || !reflect.DeepEqual(artifact.Invocation.SecretEnvironment, prepared.secretNames) {
				add("recorded argv/environment does not match independent fixed invocation reconstruction")
			}
			if artifact.TargetSafety != targetSafety(prepared.target) {
				add("recorded target-safety acknowledgement does not match the independently extracted retained driver target")
			}
		}
	}
	if driver.Adapter == benchmarkimport.AdapterHammerDB6 && stdoutErr == nil && stderrErr == nil && resultErr == nil && configErr == nil {
		if len(stderr) != 0 {
			add("HammerDB retained stderr must be empty")
		}
		if err := validateHammerDBStdout(stdout); err != nil {
			add("HammerDB retained stdout binding failed: %v", err)
		}
		if err := crossCheckHammerDBResult(artifact.Workload, config, stdout, result); err != nil {
			add("HammerDB retained report binding failed: %v", err)
		}
	}

	if stdoutErr == nil && stderrErr == nil && resultErr == nil && normalizedErr == nil {
		importVerification, err := benchmarkimport.Verify(filepath.Join(dir, NormalizedImportDir))
		if err != nil {
			add("normalized benchmark import verification failed: %v", err)
		} else {
			verification.Import = &importVerification
			if !importVerification.IsValid() || importVerification.Artifact == nil {
				add("normalized benchmark import is invalid: %s", strings.Join(importVerification.Issues, "; "))
			} else {
				if driver.ID != "" {
					if err := crossCheckImport(driver, artifact.Workload, result, *importVerification.Artifact); err != nil {
						add("normalized benchmark import binding failed: %v", err)
					}
				}
				if artifact.Normalized.ArtifactDigest != importVerification.Artifact.Digest {
					add("normalized import artifact digest binding mismatch")
				}
			}
		}
	}

	return finishVerification(verification), nil
}

func validateArtifactContract(add func(string, ...any), artifact Artifact) {
	if artifact.SchemaVersion != SchemaVersion || artifact.ArtifactType != ArtifactType || artifact.ContractVersion != ContractVersion {
		add("unsupported external driver execution schema, artifact type, or contract version")
	}
	if artifact.Classification != Classification || artifact.AnalysisDesign != AnalysisDesign || artifact.Conclusion != Conclusion || artifact.Status != StatusCompleted || artifact.Runtime != RuntimeNative || artifact.ExitCode != 0 {
		add("external driver execution must remain a completed descriptive native single trial")
	}
	if artifact.Registry.Driver.DecisionEligible || artifact.Registry.Driver.SourceToBinaryAttested || artifact.Registry.Driver.BinaryDistributedByProject {
		add("retained driver record escaped the external false-assurance boundary")
	}
	wantAssurance := Assurance{
		EvidenceOrigin: "native-driver-execution", VerificationScope: "retained-runtime-closure-fixed-argv-and-strict-result-rederivation",
		BinaryDistributedByProject: false, SourceToBinaryAttested: false, DecisionEligible: false,
		PGbenchSeriesEligible: false, CrossSystemComparisonEligible: false, TPCComplianceClaim: false,
		DriverRuntimeClosureAttested: true, HostRuntimeDependenciesAttested: false,
	}
	if artifact.Assurance != wantAssurance {
		add("external driver assurance boundary was changed")
	}
	if artifact.Invocation.ExecutableMode != BinaryExecutionMode || artifact.Invocation.DriverRuntimeMode != DriverRuntimeMode || artifact.Invocation.EnvironmentPolicy != EnvironmentPolicy || artifact.Invocation.TimeoutSeconds < 1 || artifact.Invocation.TimeoutSeconds > 86400 || len(artifact.Invocation.Argv) < 2 {
		add("external driver invocation policy is invalid")
	}
	if artifact.TargetSafety.Policy != TargetSafetyPolicy || !artifact.TargetSafety.Acknowledged || artifact.TargetSafety.Acknowledgement != TargetAcknowledgement || artifact.TargetSafety.EndpointSource != TargetEndpointSource || !artifact.TargetSafety.LoopbackOnly || !artifact.TargetSafety.SystemDatabasesDenied || artifact.TargetSafety.TargetOwnershipVerified || artifact.TargetSafety.TargetIdentityAttested {
		add("external driver target-safety policy or acknowledgement is invalid")
	}
	if err := validateExternalTarget(artifact.TargetSafety.Host, artifact.TargetSafety.Port, artifact.TargetSafety.Database); err != nil {
		add("external driver retained target violates the target-safety policy: %v", err)
	}
	started, startErr := parseCanonicalUTC(artifact.StartedAt)
	finished, finishErr := parseCanonicalUTC(artifact.FinishedAt)
	if startErr != nil || finishErr != nil || !finished.After(started) {
		add("external driver timestamps must be canonical UTC and strictly chronological")
	} else if finished.Sub(started) > time.Duration(artifact.Invocation.TimeoutSeconds)*time.Second+time.Second {
		add("external driver wall duration exceeds its recorded timeout")
	}
	digest, err := artifactDigest(artifact)
	if err != nil || !evidence.IsDigest(artifact.Digest) || digest != artifact.Digest {
		add("external driver execution artifact digest mismatch")
	}
}

func validateRuntimeInputBindings(add func(string, ...any), artifact Artifact, driver benchmarkdrivers.Driver) {
	runtime := artifact.Inputs.DriverRuntime
	if runtime.Root != DriverRuntimeDir || runtime.FileCount != len(runtime.Files) || runtime.FileCount < 1 || runtime.FileCount > maxDriverRuntimeFiles || runtime.TotalSizeBytes < 1 || runtime.TotalSizeBytes > maxDriverRuntimeBytes || !evidence.IsDigest(runtime.TreeDigest) {
		add("external driver runtime summary is invalid")
	}
	if !sort.SliceIsSorted(runtime.Files, func(left, right int) bool { return runtime.Files[left].Path < runtime.Files[right].Path }) {
		add("external driver runtime files are not sorted")
	}
	var total int64
	seen := map[string]struct{}{}
	for _, file := range runtime.Files {
		if !evidence.IsPortablePath(file.Path) || !strings.HasPrefix(file.Path, DriverRuntimeDir+"/") || !evidence.IsDigest(file.Digest) || file.SizeBytes < 0 || file.SizeBytes > maxInputBytes || file.Mode != 0o444 && file.Mode != 0o555 {
			add("external driver runtime contains an invalid file reference: %s", file.Path)
		}
		if _, exists := seen[file.Path]; exists {
			add("external driver runtime contains duplicate path: %s", file.Path)
		}
		seen[file.Path] = struct{}{}
		if file.SizeBytes < 0 || file.SizeBytes > maxInputBytes {
			// The invalid reference was reported above; exclude it from arithmetic.
		} else if file.SizeBytes > maxDriverRuntimeBytes-total {
			add("external driver runtime aggregate size overflows its bound")
		} else {
			total += file.SizeBytes
		}
	}
	digest, err := runtimeTreeDigest(runtime.Files)
	if err != nil || digest != runtime.TreeDigest || total != runtime.TotalSizeBytes {
		add("external driver runtime tree digest or aggregate size mismatch")
	}
	binary, binaryExists := runtimeFileByPath(runtime.Files, artifact.Inputs.Binary.Path)
	script, scriptExists := runtimeFileByPath(runtime.Files, artifact.Inputs.Script.Path)
	switch driver.Adapter {
	case benchmarkimport.AdapterBenchBase:
		if artifact.Inputs.Binary.Path != BinaryFile || binaryExists || artifact.Inputs.Script.Path != runtime.Entrypoint || !scriptExists || script.Digest != artifact.Inputs.Script.Digest || script.SizeBytes != artifact.Inputs.Script.SizeBytes {
			add("BenchBase binary/script runtime bindings are invalid")
		}
	case benchmarkimport.AdapterHammerDB6:
		if artifact.Inputs.Binary.Path != runtime.Entrypoint || !binaryExists || binary.Digest != artifact.Inputs.Binary.Digest || binary.SizeBytes != artifact.Inputs.Binary.SizeBytes || artifact.Inputs.Script.Path != "inputs/adapter-template.txt" || scriptExists {
			add("staged driver binary/script runtime bindings are invalid")
		}
	case benchmarkimport.AdapterSysbench1:
		if artifact.Inputs.Binary.Path != runtime.Entrypoint || !binaryExists || binary.Digest != artifact.Inputs.Binary.Digest || binary.SizeBytes != artifact.Inputs.Binary.SizeBytes || !scriptExists || script.Digest != artifact.Inputs.Script.Digest || script.SizeBytes != artifact.Inputs.Script.SizeBytes {
			add("staged driver binary/script runtime bindings are invalid")
		}
	}
}

func verifyInventory(add func(string, ...any), dir string, artifact Artifact, inventory Inventory) {
	if inventory.SchemaVersion != InventorySchemaVersion || inventory.ArtifactType != InventoryArtifactType || inventory.ExecutionDigest != artifact.Digest {
		add("external driver inventory identity or execution binding mismatch")
	}
	if !sort.SliceIsSorted(inventory.Files, func(left, right int) bool { return inventory.Files[left].Path < inventory.Files[right].Path }) {
		add("external driver inventory files are not sorted")
	}
	recorded := map[string]FileRef{}
	expected := map[string]struct{}{
		ExecutionFile: {}, artifact.Registry.Lock.Path: {}, artifact.Inputs.Binary.Path: {},
		artifact.Inputs.Config.Path: {}, artifact.Inputs.Script.Path: {}, artifact.Outputs.Stdout.Path: {},
		artifact.Outputs.Stderr.Path: {}, artifact.Outputs.DriverResult.Path: {}, artifact.Normalized.Result.Path: {},
		NormalizedImportDir + "/raw/source": {},
	}
	for _, file := range artifact.Inputs.DriverRuntime.Files {
		expected[file.Path] = struct{}{}
	}
	for _, file := range inventory.Files {
		if !evidence.IsPortablePath(file.Path) || !evidence.IsDigest(file.Digest) || file.SizeBytes < 0 {
			add("external driver inventory contains an invalid file reference: %s", file.Path)
			continue
		}
		if _, exists := recorded[file.Path]; exists {
			add("external driver inventory contains duplicate path: %s", file.Path)
		}
		recorded[file.Path] = file
		if _, exists := expected[file.Path]; !exists {
			add("external driver inventory contains unexpected file: %s", file.Path)
		}
	}
	for path := range expected {
		if _, exists := recorded[path]; !exists {
			add("external driver inventory omits required canonical file: %s", path)
		}
	}
	actual, err := scanInventoryFiles(dir, true)
	if err != nil {
		add("scan external driver artifact inventory: %v", err)
		return
	}
	if len(recorded) != len(actual) {
		add("external driver inventory file count mismatch: recorded %d actual %d", len(recorded), len(actual))
	}
	for _, file := range actual {
		want, exists := recorded[file.Path]
		if !exists {
			add("external driver inventory is missing file: %s", file.Path)
		} else if want != file {
			add("external driver inventory digest or size mismatch: %s", file.Path)
		}
		delete(recorded, file.Path)
	}
	for path := range recorded {
		add("external driver inventory references missing file: %s", path)
	}
}

func readBoundFile(add func(string, ...any), dir string, reference FileRef, limit int64, allowEmpty bool, label string) ([]byte, error) {
	if !evidence.IsPortablePath(reference.Path) || !evidence.IsDigest(reference.Digest) || reference.SizeBytes < 0 {
		add("%s reference is invalid", label)
		return nil, fmt.Errorf("invalid file reference")
	}
	path := filepath.Join(dir, filepath.FromSlash(reference.Path))
	relative, err := filepath.Rel(dir, path)
	if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		add("%s path escapes the artifact", label)
		return nil, fmt.Errorf("path escapes artifact")
	}
	content, err := readRegular(path, limit, allowEmpty, label)
	if err != nil {
		add("%s: %v", reference.Path, err)
		return nil, err
	}
	if reference.Digest != evidence.DigestBytes(content) || reference.SizeBytes != int64(len(content)) {
		add("%s digest or size mismatch", reference.Path)
		return content, fmt.Errorf("digest or size mismatch")
	}
	return content, nil
}

func resolveExecution(input string) (string, error) {
	absolute, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve external driver execution: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect external driver execution: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("external driver execution path must not be a symlink")
	}
	if info.IsDir() {
		return absolute, nil
	}
	if info.Mode().IsRegular() && filepath.Base(absolute) == ExecutionFile {
		parent := filepath.Dir(absolute)
		parentInfo, parentErr := os.Lstat(parent)
		if parentErr == nil && parentInfo.IsDir() && parentInfo.Mode()&os.ModeSymlink == 0 {
			return parent, nil
		}
	}
	return "", fmt.Errorf("external driver execution must be a non-symlink directory or its execution.json")
}

func checkDirectoryTree(add func(string, ...any), root string, artifact Artifact) {
	allowedDirectories := map[string]struct{}{
		".": {}, "inputs": {}, "raw": {}, NormalizedImportDir: {}, NormalizedImportDir + "/raw": {},
	}
	for _, file := range artifact.Inputs.DriverRuntime.Files {
		directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(file.Path)))
		for directory != "." && directory != "/" {
			allowedDirectories[directory] = struct{}{}
			next := filepath.ToSlash(filepath.Dir(filepath.FromSlash(directory)))
			if next == directory {
				break
			}
			directory = next
		}
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		reference := "."
		if path != root {
			var err error
			reference, err = relativePortable(root, path)
			if err != nil {
				return err
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			add("external driver artifact contains symlink: %s", reference)
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, exists := allowedDirectories[reference]; !exists {
				add("external driver artifact contains unexpected directory: %s", reference)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			add("external driver artifact contains unsafe file: %s", reference)
		}
		return nil
	})
	if err != nil {
		add("walk external driver artifact: %v", err)
	}
}

func scanInventoryFiles(root string, skipInventory bool) ([]FileRef, error) {
	files := []FileRef{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || entry.IsDir() {
			return nil
		}
		if len(files) >= maxArtifactFiles {
			return fmt.Errorf("external driver artifact exceeds %d files", maxArtifactFiles)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("external driver artifact contains unsafe entry: %s", path)
		}
		reference, err := relativePortable(root, path)
		if err != nil {
			return err
		}
		if skipInventory && reference == InventoryFile {
			return nil
		}
		content, err := readRegular(path, maxInputBytes, true, "external driver artifact file")
		if err != nil {
			return err
		}
		total += int64(len(content))
		if total > maxArtifactBytes {
			return fmt.Errorf("external driver artifact exceeds %d bytes", maxArtifactBytes)
		}
		files = append(files, fileRef(reference, content))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

func finishVerification(verification Verification) Verification {
	verification.Issues = uniqueSorted(verification.Issues)
	verification.Valid = verification.IsValid()
	return verification
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
