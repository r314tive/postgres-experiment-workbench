package benchmarkexternal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkimport"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (buffer *boundedBuffer) Write(content []byte) (int, error) {
	remaining := buffer.limit - int64(buffer.buffer.Len())
	if remaining <= 0 || int64(len(content)) > remaining {
		return 0, fmt.Errorf("external driver output exceeded %d bytes", buffer.limit)
	}
	return buffer.buffer.Write(content)
}

func (buffer *boundedBuffer) Bytes() []byte { return buffer.buffer.Bytes() }

// Run executes one pinned native driver through an adapter-owned argv. There
// is no arbitrary argument or shell-string escape hatch. Only successful,
// strictly normalized executions are published.
func Run(options Options) (Artifact, error) {
	options = withDefaults(options)
	if !options.AcknowledgeExternalDisposableTarget {
		return Artifact{}, fmt.Errorf("external driver execution requires --acknowledge-external-disposable-target: the operator must assert that the loopback non-system target is disposable and non-production")
	}
	if options.Timeout < time.Second || options.Timeout > 24*time.Hour || options.Timeout%time.Second != 0 {
		return Artifact{}, fmt.Errorf("external driver timeout must be a whole number of seconds between 1s and 24h")
	}
	runtimeRootInput := options.RuntimeRoot
	var err error
	options.RuntimeRoot, err = resolveDriverRuntimeRoot(runtimeRootInput)
	if err != nil {
		return Artifact{}, err
	}
	for label, target := range map[string]*string{
		"binary": &options.BinaryPath, "config": &options.ConfigPath, "script": &options.ScriptPath,
	} {
		absolute, err := filepath.Abs(*target)
		if err != nil {
			return Artifact{}, fmt.Errorf("resolve external driver %s: %w", label, err)
		}
		*target = absolute
	}
	for label, target := range map[string]*string{"binary": &options.BinaryPath, "config": &options.ConfigPath, "script": &options.ScriptPath} {
		info, err := os.Lstat(*target)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return Artifact{}, fmt.Errorf("external driver %s must be a regular non-symlink file", label)
		}
		if label != "config" {
			for _, root := range []string{runtimeRootInput, options.RuntimeRoot} {
				if isPathWithin(root, *target) {
					if err := ensureNoSymlinkPath(root, *target); err != nil {
						return Artifact{}, fmt.Errorf("external driver %s runtime path must not contain symlinks: %w", label, err)
					}
					break
				}
			}
		}
		resolved, err := filepath.EvalSymlinks(*target)
		if err != nil {
			return Artifact{}, fmt.Errorf("resolve external driver %s: %w", label, err)
		}
		*target = resolved
	}
	inspection, err := benchmarkdrivers.Load(options.Root)
	if err != nil {
		return Artifact{}, err
	}
	driver, err := inspection.Registry.Find(options.DriverID)
	if err != nil {
		return Artifact{}, err
	}
	if err := validateExecutionDriver(driver); err != nil {
		return Artifact{}, err
	}
	if driver.DecisionEligible || driver.SourceToBinaryAttested || driver.BinaryDistributedByProject {
		return Artifact{}, fmt.Errorf("external driver registry escaped its false-assurance boundary")
	}
	if !contains(driver.RuntimeSupport, RuntimeNative) {
		return Artifact{}, fmt.Errorf("driver %q does not declare native runtime support", driver.ID)
	}

	lockContent, err := readRegular(filepath.Join(options.Root, filepath.FromSlash(benchmarkdrivers.LockPath)), maxJSONBytes, false, "benchmark driver lock")
	if err != nil {
		return Artifact{}, err
	}
	if evidence.DigestBytes(lockContent) != inspection.Digest {
		return Artifact{}, fmt.Errorf("benchmark driver lock changed after registry load")
	}
	binary, err := readRegular(options.BinaryPath, maxInputBytes, false, "external driver executable")
	if err != nil {
		return Artifact{}, err
	}
	config, err := readRegular(options.ConfigPath, maxInputBytes, false, "external driver config")
	if err != nil {
		return Artifact{}, err
	}
	script, err := readRegular(options.ScriptPath, maxInputBytes, false, "external driver script")
	if err != nil {
		return Artifact{}, err
	}
	if sameFile(options.BinaryPath, options.ConfigPath) || sameFile(options.BinaryPath, options.ScriptPath) || sameFile(options.ConfigPath, options.ScriptPath) {
		return Artifact{}, fmt.Errorf("external driver binary, config, and script must be different files")
	}
	binaryInfo, err := os.Lstat(options.BinaryPath)
	if err != nil || binaryInfo.Mode()&0o111 == 0 {
		return Artifact{}, fmt.Errorf("external driver binary must have an executable mode")
	}

	finalDir, err := prepareOutput(options.OutputDir, options.RuntimeRoot)
	if err != nil {
		return Artifact{}, err
	}
	parent := filepath.Dir(finalDir)
	stage, err := os.MkdirTemp(parent, ".pgworkbench-driver-execution-*.tmp")
	if err != nil {
		return Artifact{}, fmt.Errorf("create external driver staging directory: %w", err)
	}
	defer func() {
		_ = thawStagedRuntime(stage)
		_ = os.RemoveAll(stage)
	}()

	password := options.Getenv(SecretPasswordEnv)
	if err := validateSecret(password); err != nil {
		return Artifact{}, err
	}
	if password != "" && (bytes.Contains(binary, []byte(password)) || bytes.Contains(config, []byte(password)) || bytes.Contains(script, []byte(password)) || bytes.Contains(lockContent, []byte(password))) {
		return Artifact{}, fmt.Errorf("external driver retained inputs must not contain bytes from %s", SecretPasswordEnv)
	}
	runtime, err := stageDriverRuntime(stage, driver, options, binary, script, password)
	if err != nil {
		return Artifact{}, err
	}
	invocation, err := prepareInvocationWithRuntime(stage, driver, options.Workload, config, script, password, runtime.binding)
	if err != nil {
		return Artifact{}, err
	}
	defer invocation.cleanup()
	for _, directory := range []string{filepath.Join(stage, "inputs"), filepath.Join(stage, "raw")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return Artifact{}, err
		}
	}
	if err := writeBytesExclusive(filepath.Join(stage, filepath.FromSlash(LockFile)), lockContent, 0o644); err != nil {
		return Artifact{}, fmt.Errorf("retain benchmark driver lock: %w", err)
	}
	if runtime.sourceExecutor {
		if err := writeBytesExclusive(filepath.Join(stage, filepath.FromSlash(BinaryFile)), binary, 0o555); err != nil {
			return Artifact{}, fmt.Errorf("retain external driver executable: %w", err)
		}
	}
	configRefPath, err := relativePortable(stage, invocation.configPath)
	if err != nil {
		return Artifact{}, err
	}
	scriptRefPath, err := relativePortable(stage, invocation.scriptPath)
	if err != nil {
		return Artifact{}, err
	}
	if err := writeBytesExclusive(invocation.configPath, config, 0o644); err != nil {
		return Artifact{}, fmt.Errorf("retain external driver config: %w", err)
	}
	if !strings.HasPrefix(scriptRefPath, DriverRuntimeDir+"/") {
		if err := writeBytesExclusive(invocation.scriptPath, script, 0o644); err != nil {
			return Artifact{}, fmt.Errorf("retain external driver script: %w", err)
		}
	}

	workDir := filepath.Join(stage, ".driver-work")
	if err := os.MkdirAll(filepath.Join(workDir, "home"), 0o700); err != nil {
		return Artifact{}, err
	}
	if err := os.MkdirAll(filepath.Join(workDir, "tmp"), 0o700); err != nil {
		return Artifact{}, err
	}
	if len(invocation.generatedTcl) != 0 {
		if err := writeBytesExclusive(filepath.Join(workDir, "execute.tcl"), invocation.generatedTcl, 0o600); err != nil {
			return Artifact{}, fmt.Errorf("materialize ephemeral adapter-generated Tcl: %w", err)
		}
	}
	if driver.Adapter == benchmarkimport.AdapterBenchBase {
		if err := os.MkdirAll(filepath.Join(workDir, "results"), 0o700); err != nil {
			return Artifact{}, err
		}
		realized, err := realizeBenchBaseConfig(config, password)
		if err != nil {
			return Artifact{}, fmt.Errorf("realize ephemeral BenchBase config: %w", err)
		}
		if err := writeBytesExclusive(filepath.Join(workDir, "config.xml"), realized, 0o600); err != nil {
			return Artifact{}, fmt.Errorf("materialize ephemeral BenchBase config: %w", err)
		}
	}

	runContext, cancel := context.WithTimeout(options.Context, options.Timeout)
	defer cancel()
	if err := freezeStagedRuntime(stage); err != nil {
		return Artifact{}, fmt.Errorf("make staged driver runtime read-only: %w", err)
	}
	if err := verifyStagedRuntimeIdentity(stage, runtime.binding); err != nil {
		return Artifact{}, fmt.Errorf("verify staged external driver runtime before execution: %w", err)
	}
	command := exec.CommandContext(runContext, runtime.commandPath, invocation.argv...)
	if err := configureProcessGroup(command); err != nil {
		return Artifact{}, fmt.Errorf("configure external driver process group: %w", err)
	}
	command.Cancel = func() error { return killProcessGroup(command) }
	command.WaitDelay = time.Second
	command.Dir = workDir
	if runtime.commandDir != "" {
		command.Dir = runtime.commandDir
	}
	command.Env = []string{
		"HOME=" + filepath.Join(workDir, "home"),
		"TMPDIR=" + filepath.Join(workDir, "tmp"),
		"TMP=" + filepath.Join(workDir, "tmp"),
		"TEMP=" + filepath.Join(workDir, "tmp"),
		"LANG=C", "LC_ALL=C", "TZ=UTC",
	}
	command.Env = append(command.Env, runtime.extraEnv...)
	if driver.Adapter == benchmarkimport.AdapterSysbench1 && password != "" {
		pgpassPath := filepath.Join(workDir, "pgpass")
		pgpass := "*:*:*:*:" + escapePGPass(password) + "\n"
		if err := writeBytesExclusive(pgpassPath, []byte(pgpass), 0o600); err != nil {
			return Artifact{}, fmt.Errorf("materialize ephemeral sysbench pgpass: %w", err)
		}
		command.Env = append(command.Env, "PGPASSFILE="+pgpassPath)
	}
	if driver.Adapter == benchmarkimport.AdapterHammerDB6 && password != "" {
		command.Env = append(command.Env, SecretPasswordEnv+"="+password)
	}
	var stdout, stderr boundedBuffer
	stdout.limit = maxOutputBytes
	stderr.limit = maxOutputBytes
	command.Stdout = &stdout
	command.Stderr = &stderr
	startedAt := options.Now().UTC()
	runErr := command.Run()
	finishedAt := options.Now().UTC()
	if !finishedAt.After(startedAt) {
		finishedAt = startedAt.Add(time.Nanosecond)
	}
	residual, groupErr := closeProcessGroup(command)
	if groupErr != nil {
		return Artifact{}, fmt.Errorf("verify external driver process group: %w", groupErr)
	}
	if residual {
		return Artifact{}, fmt.Errorf("external driver exited with live descendants; residual process group was killed")
	}
	if runtime.sourceExecutor {
		postBinary, postErr := readRegular(options.BinaryPath, maxInputBytes, false, "external driver executable after execution")
		if postErr != nil || evidence.DigestBytes(postBinary) != evidence.DigestBytes(binary) || !bytes.Equal(postBinary, binary) {
			return Artifact{}, fmt.Errorf("external driver executable changed during execution")
		}
	}
	if err := verifyStagedRuntimeIdentity(stage, runtime.binding); err != nil {
		return Artifact{}, err
	}
	if runContext.Err() != nil {
		return Artifact{}, fmt.Errorf("external driver exceeded timeout of %s", options.Timeout)
	}
	if runErr != nil {
		var exitError *exec.ExitError
		if errors.As(runErr, &exitError) {
			return Artifact{}, fmt.Errorf("external driver exited with code %d", exitError.ExitCode())
		}
		return Artifact{}, fmt.Errorf("execute external driver: %w", runErr)
	}
	if command.ProcessState == nil || command.ProcessState.ExitCode() != 0 {
		return Artifact{}, fmt.Errorf("external driver did not report a successful exit")
	}
	if driver.Adapter == benchmarkimport.AdapterSysbench1 && len(stderr.Bytes()) != 0 {
		return Artifact{}, fmt.Errorf("sysbench wrote stderr bytes outside its strictly normalized console result")
	}
	if driver.Adapter == benchmarkimport.AdapterHammerDB6 {
		if len(stderr.Bytes()) != 0 {
			return Artifact{}, fmt.Errorf("HammerDB wrote stderr bytes outside its strictly normalized saved job report")
		}
		if err := validateHammerDBStdout(stdout.Bytes()); err != nil {
			return Artifact{}, err
		}
	}

	result, err := collectDriverResult(stage, driver, stdout.Bytes())
	if err != nil {
		return Artifact{}, err
	}
	if driver.Adapter == benchmarkimport.AdapterHammerDB6 {
		if err := crossCheckHammerDBResult(options.Workload, config, stdout.Bytes(), result); err != nil {
			return Artifact{}, err
		}
	}
	for label, content := range map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes(), "driver result": result} {
		if password != "" && bytes.Contains(content, []byte(password)) {
			return Artifact{}, fmt.Errorf("refusing to retain %s because it contains bytes from %s", label, SecretPasswordEnv)
		}
	}
	if err := os.RemoveAll(workDir); err != nil {
		return Artifact{}, fmt.Errorf("remove ephemeral driver workspace: %w", err)
	}
	if err := writeBytesExclusive(filepath.Join(stage, filepath.FromSlash(StdoutFile)), append([]byte(nil), stdout.Bytes()...), 0o644); err != nil {
		return Artifact{}, err
	}
	if err := writeBytesExclusive(filepath.Join(stage, filepath.FromSlash(StderrFile)), append([]byte(nil), stderr.Bytes()...), 0o644); err != nil {
		return Artifact{}, err
	}
	resultRefPath, err := relativePortable(stage, invocation.resultPath)
	if err != nil {
		return Artifact{}, err
	}
	if err := writeBytesExclusive(invocation.resultPath, result, 0o644); err != nil {
		return Artifact{}, err
	}

	importAdapter, importOptions, err := strictImportContract(driver, options.Workload)
	if err != nil {
		return Artifact{}, err
	}
	normalized, err := benchmarkimport.Create(importAdapter, invocation.resultPath, filepath.Join(stage, NormalizedImportDir), importOptions)
	if err != nil {
		return Artifact{}, fmt.Errorf("strictly normalize external driver result: %w", err)
	}
	if err := crossCheckImport(driver, options.Workload, result, normalized); err != nil {
		return Artifact{}, err
	}
	importVerification, err := benchmarkimport.Verify(filepath.Join(stage, NormalizedImportDir))
	if err != nil || !importVerification.IsValid() {
		if err != nil {
			return Artifact{}, fmt.Errorf("verify normalized external driver import: %w", err)
		}
		return Artifact{}, fmt.Errorf("verify normalized external driver import: %s", strings.Join(importVerification.Issues, "; "))
	}
	importResult, err := readRegular(filepath.Join(stage, filepath.FromSlash(NormalizedImportResult)), maxJSONBytes, false, "normalized import result")
	if err != nil {
		return Artifact{}, err
	}

	artifact := Artifact{
		SchemaVersion: SchemaVersion, ArtifactType: ArtifactType, ContractVersion: ContractVersion,
		Classification: Classification, AnalysisDesign: AnalysisDesign, Conclusion: Conclusion,
		Status: StatusCompleted, Runtime: RuntimeNative, Workload: options.Workload,
		Registry: RegistryBinding{Lock: fileRef(LockFile, lockContent), Driver: driver},
		Invocation: Invocation{
			ExecutableMode: BinaryExecutionMode, DriverRuntimeMode: DriverRuntimeMode, Argv: invocation.recordedArgv,
			EnvironmentPolicy: EnvironmentPolicy, SecretEnvironment: invocation.secretNames,
			TimeoutSeconds: int64(options.Timeout / time.Second),
		},
		TargetSafety: targetSafety(invocation.target),
		Inputs: Inputs{
			Binary: fileRef(runtime.binaryPath, binary), Config: fileRef(configRefPath, config), Script: fileRef(scriptRefPath, script), DriverRuntime: runtime.binding,
		},
		Outputs: Outputs{
			Stdout: fileRef(StdoutFile, stdout.Bytes()), Stderr: fileRef(StderrFile, stderr.Bytes()), DriverResult: fileRef(resultRefPath, result),
		},
		StartedAt: startedAt.Format(time.RFC3339Nano), FinishedAt: finishedAt.Format(time.RFC3339Nano), ExitCode: 0,
		Normalized: NormalizedImport{Result: fileRef(NormalizedImportResult, importResult), ArtifactDigest: normalized.Digest},
		Assurance: Assurance{
			EvidenceOrigin: "native-driver-execution", VerificationScope: "retained-runtime-closure-fixed-argv-and-strict-result-rederivation",
			BinaryDistributedByProject: false, SourceToBinaryAttested: false, DecisionEligible: false,
			PGbenchSeriesEligible: false, CrossSystemComparisonEligible: false, TPCComplianceClaim: false,
			DriverRuntimeClosureAttested: true, HostRuntimeDependenciesAttested: false,
		},
	}
	artifact.Digest, err = artifactDigest(artifact)
	if err != nil {
		return Artifact{}, err
	}
	if err := writeJSONExclusive(filepath.Join(stage, ExecutionFile), artifact); err != nil {
		return Artifact{}, fmt.Errorf("write external driver execution artifact: %w", err)
	}
	inventory, err := buildInventory(stage, artifact.Digest)
	if err != nil {
		return Artifact{}, err
	}
	if err := writeJSONExclusive(filepath.Join(stage, InventoryFile), inventory); err != nil {
		return Artifact{}, fmt.Errorf("write external driver execution inventory: %w", err)
	}
	verification, err := Verify(stage)
	if err != nil {
		return Artifact{}, fmt.Errorf("verify staged external driver execution: %w", err)
	}
	if !verification.IsValid() {
		return Artifact{}, fmt.Errorf("staged external driver execution is invalid: %s", strings.Join(verification.Issues, "; "))
	}
	if err := os.Rename(stage, finalDir); err != nil {
		return Artifact{}, fmt.Errorf("publish immutable external driver execution: %w", err)
	}
	artifact.ArtifactDir = finalDir
	return artifact, nil
}

func targetSafety(target targetIdentity) TargetSafety {
	return TargetSafety{
		Policy: TargetSafetyPolicy, Acknowledged: true, Acknowledgement: TargetAcknowledgement,
		EndpointSource: TargetEndpointSource, Host: target.host, Port: target.port, Database: target.database,
		LoopbackOnly: true, SystemDatabasesDenied: true,
		TargetOwnershipVerified: false, TargetIdentityAttested: false,
	}
}

func withDefaults(options Options) Options {
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Timeout == 0 {
		options.Timeout = time.Hour
	}
	return options
}

func prepareOutput(output, runtimeRoot string) (string, error) {
	if strings.TrimSpace(output) == "" {
		return "", fmt.Errorf("external driver output directory is required")
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("resolve external driver output: %w", err)
	}
	if filepath.Base(absolute) == "." || filepath.Base(absolute) == string(filepath.Separator) {
		return "", fmt.Errorf("external driver output must name a new directory")
	}
	if info, err := os.Lstat(absolute); err == nil {
		return "", fmt.Errorf("refusing to overwrite immutable external driver execution: %s (%s)", absolute, info.Mode())
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect external driver output: %w", err)
	}
	future, err := resolveFuturePath(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve external driver output: %w", err)
	}
	if isPathWithin(runtimeRoot, future) {
		return "", fmt.Errorf("external driver output must be outside --runtime-root")
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create external driver output parent: %w", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("resolve external driver output parent: %w", err)
	}
	info, err := os.Lstat(resolvedParent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("external driver output parent must resolve to a real directory")
	}
	final := filepath.Join(resolvedParent, filepath.Base(absolute))
	if isPathWithin(runtimeRoot, final) {
		return "", fmt.Errorf("external driver output must be outside --runtime-root")
	}
	return final, nil
}

func resolveFuturePath(value string) (string, error) {
	cursor := value
	missing := []string{}
	for {
		if _, err := os.Lstat(cursor); err == nil {
			resolved, err := filepath.EvalSymlinks(cursor)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", fmt.Errorf("no existing output ancestor")
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}
}

func collectDriverResult(stage string, driver benchmarkdrivers.Driver, stdout []byte) ([]byte, error) {
	switch driver.Adapter {
	case benchmarkimport.AdapterSysbench1:
		if len(stdout) == 0 {
			return nil, fmt.Errorf("sysbench produced an empty console result")
		}
		return append([]byte(nil), stdout...), nil
	case benchmarkimport.AdapterBenchBase:
		matches, err := filepath.Glob(filepath.Join(stage, ".driver-work", "results", "*.summary.json"))
		if err != nil || len(matches) != 1 {
			return nil, fmt.Errorf("BenchBase must produce exactly one *.summary.json result")
		}
		return readRegular(matches[0], maxOutputBytes, false, "BenchBase summary result")
	case benchmarkimport.AdapterHammerDB6:
		jobID, err := hammerDBJobIDFromStdout(stdout)
		if err != nil {
			return nil, err
		}
		matches, err := filepath.Glob(filepath.Join(stage, ".driver-work", "tmp", "hdb_*.json"))
		if err != nil || len(matches) != 1 {
			return nil, fmt.Errorf("HammerDB must produce exactly one hdb_<jobid>.json saved job report")
		}
		if filepath.Base(matches[0]) != "hdb_"+jobID+".json" {
			return nil, fmt.Errorf("HammerDB saved report filename does not match the parsed vurun job id")
		}
		return readRegular(matches[0], maxOutputBytes, false, "HammerDB saved job report")
	default:
		return nil, fmt.Errorf("result collection is not implemented for adapter %q", driver.Adapter)
	}
}

func strictImportContract(driver benchmarkdrivers.Driver, workload string) (string, benchmarkimport.Options, error) {
	switch driver.Adapter {
	case benchmarkimport.AdapterSysbench1:
		return benchmarkimport.AdapterSysbench1, benchmarkimport.Options{Workload: workload}, nil
	case benchmarkimport.AdapterBenchBase:
		return benchmarkimport.AdapterBenchBase33c0047, benchmarkimport.Options{}, nil
	case benchmarkimport.AdapterHammerDB6:
		return benchmarkimport.AdapterHammerDB6Report, benchmarkimport.Options{}, nil
	default:
		return "", benchmarkimport.Options{}, fmt.Errorf("strict import is not implemented for adapter %q", driver.Adapter)
	}
}

func crossCheckImport(driver benchmarkdrivers.Driver, workload string, raw []byte, imported benchmarkimport.Artifact) error {
	expectedVersion := driver.DisplayVersion
	if driver.Adapter == benchmarkimport.AdapterHammerDB6 {
		expectedVersion = "v6.0"
	}
	if imported.DriverVersion != expectedVersion || imported.Workload != workload || imported.SourceFormat != driver.ResultFormat || imported.ParserVersion != driver.Parser {
		return fmt.Errorf("normalized import does not match the pinned driver/version/workload/parser contract")
	}
	if imported.DriverCommit != "" && imported.DriverCommit != driver.Commit {
		return fmt.Errorf("normalized import driver commit does not match the pinned driver commit")
	}
	if imported.RawInput.Digest != evidence.DigestBytes(raw) || imported.RawInput.SizeBytes != int64(len(raw)) {
		return fmt.Errorf("normalized import raw source does not match the executed driver result")
	}
	if imported.DecisionEligible || imported.PGbenchSeriesEligible || imported.Conclusion != benchmarkimport.ConclusionDescriptive {
		return fmt.Errorf("normalized import escaped the descriptive-only assurance boundary")
	}
	return nil
}

func buildInventory(root, executionDigest string) (Inventory, error) {
	files, err := scanInventoryFiles(root, false)
	if err != nil {
		return Inventory{}, err
	}
	return Inventory{SchemaVersion: InventorySchemaVersion, ArtifactType: InventoryArtifactType, ExecutionDigest: executionDigest, Files: files}, nil
}

func relativePortable(root, path string) (string, error) {
	reference, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	reference = filepath.ToSlash(reference)
	if !evidence.IsPortablePath(reference) {
		return "", fmt.Errorf("external driver artifact path is not portable: %s", reference)
	}
	return reference, nil
}

func sameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func validateSecret(value string) error {
	if value == "" {
		return nil
	}
	if len(value) < 8 || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s must contain 8..4096 bytes without NUL or newlines", SecretPasswordEnv)
	}
	return nil
}

func escapePGPass(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, ":", "\\:")
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func sameDriver(left, right benchmarkdrivers.Driver) bool { return reflect.DeepEqual(left, right) }

var _ io.Writer = (*boundedBuffer)(nil)
