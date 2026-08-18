package utilityrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityplan"
)

const (
	ExperimentSpecScopeEnv = "PGWORKBENCH_EXPERIMENT_SPEC_SCOPE"
	DerivedExperimentIDEnv = "PGWORKBENCH_DERIVED_EXPERIMENT_ID"
	SourceSpecKindEnv      = "PGWORKBENCH_SOURCE_SPEC_KIND"
	SourceSpecIDEnv        = "PGWORKBENCH_SOURCE_SPEC_ID"
	SourceSpecRefEnv       = "PGWORKBENCH_SOURCE_SPEC_REF"
	SourceSpecDigestEnv    = "PGWORKBENCH_SOURCE_SPEC_DIGEST"

	UtilityDerivedSpecScope = "utility-derived"
	UtilityTestSourceKind   = "utility-test"
)

type LookupEnv func(string) (string, bool)

// CLILookupEnvironment projects the small, documented set of ambient values
// that a utility-derived experiment is allowed to consume. The prepared
// experiment runner uses ExactEnvironment, so workbench/protocol values outside
// this list are removed before the shell control plane starts. The conventional
// process-bootstrap allow-list (including PATH) is retained separately and is
// not utility evidence identity.
//
// Native toolchain digests are deliberately absent. A utility run does not
// independently inspect and bind a native toolchain, so inheriting such a
// digest would turn an ambient claim into evidence identity.
func CLILookupEnvironment(lookup LookupEnv) []string {
	if lookup == nil {
		return nil
	}
	keys := []string{
		"COMPOSE",
		"PGWORKBENCH_RUNTIME",
		"PGWORKBENCH_NATIVE_BINDIR",
		"PGWORKBENCH_NATIVE_WAIT_SECONDS",
		"PG_INSTALL_DIR",
		"POSTGRES_HOST",
		"POSTGRES_PORT",
		"POSTGRES_DB",
		"POSTGRES_USER",
		"POSTGRES_PASSWORD",
		"PROFILE_SIZE",
		"PROFILE_SECONDS",
		"METRICS_INTERVAL",
		"METRICS_DURATION",
		"METRICS_SAMPLES",
		"UTILITY_TEST_SNAPSHOT",
	}
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, present := lookup(key); present {
			result = append(result, key+"="+value)
		}
	}
	return result
}

type CommandResult struct {
	ExitCode int
	Err      error
}

type CommandRunner func(root string, command []string, env []string, stdout io.Writer, stderr io.Writer) CommandResult

type ExperimentRunner func(root string, spec speccatalog.Spec, options experimentrun.Options) (experimentrun.Result, error)

type Env func(string) string

type Options struct {
	Runtime          string
	RunID            string
	Stdout           io.Writer
	Stderr           io.Writer
	Env              []string
	Now              func() time.Time
	Getenv           Env
	EngineVersion    string
	EngineCommit     string
	BinaryPath       string
	ExecutionTimeout time.Duration
	CleanupGrace     time.Duration
	RunExperiment    ExperimentRunner
	// RunCommand is retained as a narrow test seam. Production execution uses
	// RunExperiment so the Go runner owns the complete process group.
	RunCommand CommandRunner
}

type Result struct {
	UtilityTestSpec string   `json:"utility_test_spec"`
	UtilityTestName string   `json:"utility_test_name"`
	SpecPath        string   `json:"spec_path"`
	ExperimentSpec  string   `json:"experiment_spec"`
	Runtime         string   `json:"runtime"`
	RunID           string   `json:"run_id"`
	Command         []string `json:"command"`
	StartedAt       string   `json:"started_at"`
	FinishedAt      string   `json:"finished_at"`
	DurationMS      int64    `json:"duration_ms"`
	ExitCode        int      `json:"exit_code"`
	Status          string   `json:"status"`
}

func (r Result) Passed() bool {
	return r.Status == "passed"
}

func Run(root string, catalog speccatalog.Catalog, input string, options Options) (Result, error) {
	options = withDefaults(options)
	sourceSpec, source, err := selectSourceSpec(root, catalog, input)
	if err != nil {
		return Result{}, err
	}
	plan, err := utilityplan.BuildPrepared(catalog, sourceSpec)
	if err != nil {
		return Result{}, err
	}

	runtime := strings.TrimSpace(options.Runtime)
	if runtime == "" {
		runtime = strings.TrimSpace(envValue(options.Env, "PGWORKBENCH_RUNTIME"))
	}
	if runtime == "" {
		runtime = strings.TrimSpace(options.Getenv("PGWORKBENCH_RUNTIME"))
	}
	if runtime == "" {
		runtime = "docker"
	}
	if runtime != "docker" && runtime != "native" {
		return Result{}, fmt.Errorf("unsupported runtime %q: expected docker or native", runtime)
	}
	started := options.Now().UTC()
	runID := strings.TrimSpace(options.RunID)
	if runID == "" {
		runID = strings.TrimSpace(envValue(options.Env, "UTILITY_TEST_RUN_ID"))
	}
	if runID == "" {
		runID = strings.TrimSpace(options.Getenv("UTILITY_TEST_RUN_ID"))
	}
	if runID == "" {
		runID = fmt.Sprintf("utility-%s-%s", sanitizeID(plan.Spec.ID), started.Format("20060102_150405"))
	}
	if !validRunID(runID) {
		return Result{}, fmt.Errorf("invalid run id %q", runID)
	}
	if runtime == "native" && strings.TrimSpace(envValue(options.Env, "PGWORKBENCH_NATIVE_BINDIR")) == "" && strings.TrimSpace(envValue(options.Env, "PG_INSTALL_DIR")) == "" {
		return Result{}, fmt.Errorf("native utility execution requires PGWORKBENCH_NATIVE_BINDIR or PG_INSTALL_DIR")
	}

	experimentSpec, err := writeExperimentSpec(root, plan, runID)
	if err != nil {
		return Result{}, err
	}

	command := []string{filepath.Join(root, "scripts", "run_experiment.sh"), "run", experimentSpec}
	result := Result{
		UtilityTestSpec: plan.Spec.ID,
		UtilityTestName: plan.Fields["name"],
		SpecPath:        plan.Spec.Path,
		ExperimentSpec:  experimentSpec,
		Runtime:         runtime,
		RunID:           runID,
		Command:         append([]string(nil), command...),
		StartedAt:       started.Format(time.RFC3339),
	}

	commandEnv := mergeEnv(options.Env, []string{
		"ENV_FILE=.env.example",
		"PGWORKBENCH_RUNTIME=" + runtime,
		"UTILITY_TEST_RUN_ID=" + runID,
		ExperimentSpecScopeEnv + "=" + UtilityDerivedSpecScope,
		DerivedExperimentIDEnv + "=" + path.Join("utility", source.ID),
		SourceSpecKindEnv + "=" + UtilityTestSourceKind,
		SourceSpecIDEnv + "=" + source.ID,
		SourceSpecRefEnv + "=" + source.Ref,
		SourceSpecDigestEnv + "=" + source.Digest,
		"PGWORKBENCH_PACK_ID=",
		"PGWORKBENCH_PACK_VERSION=",
		"PGWORKBENCH_PACK_DIGEST=",
	})
	commandResult := CommandResult{ExitCode: -1}
	if options.RunCommand != nil {
		commandResult = options.RunCommand(root, command, commandEnv, options.Stdout, options.Stderr)
	} else {
		values, parseErr := envfile.Parse(experimentSpec)
		if parseErr != nil {
			return result, fmt.Errorf("parse generated utility experiment spec: %w", parseErr)
		}
		prepared := speccatalog.Spec{
			Kind:   "experiment",
			ID:     path.Join("utility", source.ID),
			Path:   experimentSpec,
			Values: values,
		}
		experimentResult, runErr := options.RunExperiment(root, prepared, experimentrun.Options{
			Runtime:          runtime,
			RunID:            runID,
			Env:              commandEnv,
			ExactEnvironment: true,
			EngineVersion:    options.EngineVersion,
			EngineCommit:     options.EngineCommit,
			BinaryPath:       options.BinaryPath,
			Stdout:           options.Stdout,
			Stderr:           options.Stderr,
			ExecutionTimeout: options.ExecutionTimeout,
			CleanupGrace:     options.CleanupGrace,
			Now:              options.Now,
			Getenv:           options.Getenv,
		})
		// Report the command that the prepared runner actually executed. The
		// public shell form above is retained only for the legacy test seam; in
		// production it would re-enter catalog resolution and is deliberately not
		// the prepared-spec execution path.
		if len(experimentResult.Command) > 0 {
			result.Command = append([]string(nil), experimentResult.Command...)
		}
		if experimentResult.SchemaVersion != "" {
			commandResult.ExitCode = experimentResult.ExitCode
		}
		commandResult.Err = runErr
		if runErr == nil && !experimentResult.Passed() {
			commandResult.Err = fmt.Errorf("prepared utility experiment did not pass")
		}
	}
	finished := options.Now().UTC()
	result.FinishedAt = finished.Format(time.RFC3339)
	result.DurationMS = maxDurationMS(finished.Sub(started))
	result.ExitCode = commandResult.ExitCode
	if commandResult.Err == nil && commandResult.ExitCode == 0 {
		result.Status = "passed"
		return result, nil
	}
	result.Status = "failed"
	if commandResult.Err != nil {
		return result, commandResult.Err
	}
	return result, fmt.Errorf("utility test command exited with code %d", commandResult.ExitCode)
}

func envValue(values []string, target string) string {
	for index := len(values) - 1; index >= 0; index-- {
		key, value, ok := strings.Cut(values[index], "=")
		if ok && key == target {
			return value
		}
	}
	return ""
}

func Render(w io.Writer, result Result) error {
	status := "FAIL"
	if result.Passed() {
		status = "PASS"
	}
	if _, err := fmt.Fprintf(w, "%s: utility %s runtime=%s run_id=%s exit=%d duration_ms=%d\n", status, result.UtilityTestSpec, result.Runtime, result.RunID, result.ExitCode, result.DurationMS); err != nil {
		return err
	}
	if result.ExperimentSpec != "" {
		if _, err := fmt.Fprintf(w, "experiment_spec=%s\n", result.ExperimentSpec); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, result Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func withDefaults(options Options) Options {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.RunExperiment == nil {
		options.RunExperiment = experimentrun.RunPrepared
	}
	return options
}

func mergeEnv(base []string, overrides []string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	indexes := make(map[string]int, len(base)+len(overrides))
	merge := func(entries []string) {
		for _, entry := range entries {
			key, _, _ := strings.Cut(entry, "=")
			if index, ok := indexes[key]; ok {
				result[index] = entry
				continue
			}
			indexes[key] = len(result)
			result = append(result, entry)
		}
	}
	merge(base)
	merge(overrides)
	return result
}

func writeExperimentSpec(root string, plan utilityplan.Plan, runID string) (string, error) {
	dir, err := secureGeneratedSpecDir(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, sanitizeID(runID)+".env")

	var out strings.Builder
	out.WriteString("# Generated from utility-test spec. This file is ignored local runtime state.\n")
	writeEnv(&out, "EXPERIMENT_NAME", "utility: "+plan.Fields["name"])
	writeEnv(&out, "EXPERIMENT_RUN_ID", runID)
	writeEnv(&out, "EXPERIMENT_STATE_WRITER", "go")
	writeEnv(&out, "EXPERIMENT_PROFILE", plan.Fields["profile"])
	writeEnv(&out, "EXPERIMENT_PROFILE_SIZE", plan.Fields["profile_size"])
	writeEnv(&out, "EXPERIMENT_PROFILE_SECONDS", plan.Fields["profile_seconds"])
	writeEnv(&out, "EXPERIMENT_DATASET_SPEC", plan.Fields["dataset"])
	writeEnv(&out, "EXPERIMENT_DATASET_SIZE", plan.Fields["dataset_size"])
	writeEnv(&out, "EXPERIMENT_BACKGROUND_SPECS", plan.Fields["backgrounds"])
	writeEnv(&out, "EXPERIMENT_BACKGROUND_WARMUP", plan.Fields["background_warmup"])
	writeEnv(&out, "EXPERIMENT_BACKGROUND_WAIT", plan.Fields["background_wait"])
	writeEnv(&out, "EXPERIMENT_WORKLOAD_SPEC", plan.Fields["workload"])
	writeEnv(&out, "EXPERIMENT_METRICS", plan.Fields["metrics"])
	writeEnv(&out, "EXPERIMENT_METRICS_INTERVAL", plan.Fields["metrics_interval"])
	writeEnv(&out, "EXPERIMENT_METRICS_DURATION", plan.Fields["metrics_duration"])
	writeEnv(&out, "EXPERIMENT_METRICS_SAMPLES", plan.Fields["metrics_samples"])
	writeEnv(&out, "EXPERIMENT_ASSERT_SQL_FILES", plan.Fields["assert_sql_files"])
	writeEnv(&out, "EXPERIMENT_ASSERT_SQL", plan.Fields["assert_sql"])
	writeEnv(&out, "EXPERIMENT_ASSERT_TRUE_SQL", plan.Fields["assert_true_sql"])
	writeEnv(&out, "EXPERIMENT_TRUSTED_SHELL", plan.Fields["trusted_shell"])
	writeEnv(&out, "EXPERIMENT_ASSERT_SHELL", combinedAssertShell(plan.Fields))
	writeEnv(&out, "EXPERIMENT_CAPTURE_FILES", plan.Fields["expect_files"])
	writeEnv(&out, "EXPERIMENT_SCAN_PATHS", plan.Fields["scan_paths"])
	writeEnv(&out, "EXPERIMENT_SNAPSHOT", "${UTILITY_TEST_SNAPSHOT:-1}")

	if err := writeGeneratedSpec(path, []byte(out.String())); err != nil {
		return "", err
	}
	return path, nil
}

type sourceSpecIdentity struct {
	ID     string
	Ref    string
	Digest string
}

func selectSourceSpec(root string, catalog speccatalog.Catalog, input string) (speccatalog.Spec, sourceSpecIdentity, error) {
	specPath, resolvedID, err := catalog.Resolve("utility-test", input)
	if err != nil {
		return speccatalog.Spec{}, sourceSpecIdentity{}, err
	}
	canonicalRoot, err := canonicalDirectory(root, "repository root")
	if err != nil {
		return speccatalog.Spec{}, sourceSpecIdentity{}, err
	}
	id := filepath.ToSlash(strings.TrimSpace(resolvedID))
	if !validSourceSpecID(id) {
		return speccatalog.Spec{}, sourceSpecIdentity{}, fmt.Errorf("utility-test source id is not a canonical portable id: %s", resolvedID)
	}
	ref := path.Join("utility-tests", id+".env")
	if !evidence.IsPortablePath(ref) {
		return speccatalog.Spec{}, sourceSpecIdentity{}, fmt.Errorf("utility-test source ref is not portable: %s", ref)
	}

	expected, err := secureExistingFile(canonicalRoot, ref)
	if err != nil {
		return speccatalog.Spec{}, sourceSpecIdentity{}, fmt.Errorf("resolve utility-test source %s: %w", ref, err)
	}
	candidate := specPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(canonicalRoot, candidate)
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return speccatalog.Spec{}, sourceSpecIdentity{}, fmt.Errorf("resolve utility-test source path: %w", err)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return speccatalog.Spec{}, sourceSpecIdentity{}, fmt.Errorf("resolve utility-test source path: %w", err)
	}
	if filepath.Clean(candidate) != expected {
		return speccatalog.Spec{}, sourceSpecIdentity{}, fmt.Errorf("utility-test source path does not match %s", ref)
	}
	content, err := readStableSourceSpec(expected)
	if err != nil {
		return speccatalog.Spec{}, sourceSpecIdentity{}, fmt.Errorf("read utility-test source %s: %w", ref, err)
	}
	values, err := envfile.ParseBytes(expected, content)
	if err != nil {
		return speccatalog.Spec{}, sourceSpecIdentity{}, err
	}
	spec := speccatalog.Spec{Kind: "utility-test", ID: id, Path: expected, Values: values}
	identity := sourceSpecIdentity{ID: id, Ref: ref, Digest: evidence.DigestBytes(content)}
	return spec, identity, nil
}

func readStableSourceSpec(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source is not a regular non-symlink file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	openedBefore, err := file.Stat()
	if err != nil || !os.SameFile(before, openedBefore) {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("source changed while it was opened: %s", path)
	}
	content, readErr := io.ReadAll(file)
	openedAfter, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, closeErr); err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedAfter, after) || openedBefore.Size() != openedAfter.Size() || !openedBefore.ModTime().Equal(openedAfter.ModTime()) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("source changed while it was read: %s", path)
	}
	return content, nil
}

func secureGeneratedSpecDir(root string) (string, error) {
	canonicalRoot, err := canonicalDirectory(root, "repository root")
	if err != nil {
		return "", err
	}
	current := canonicalRoot
	for _, component := range []string{".tmp", "utility-tests"} {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return "", fmt.Errorf("create generated utility spec directory %s: %w", current, err)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return "", fmt.Errorf("inspect generated utility spec directory %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("generated utility spec directory must not contain symlinks: %s", current)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("generated utility spec path is not a directory: %s", current)
		}
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve generated utility spec directory: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(current) || !pathWithinRoot(canonicalRoot, resolved) {
		return "", fmt.Errorf("generated utility spec directory escapes repository root: %s", current)
	}
	return current, nil
}

func writeGeneratedSpec(target string, content []byte) error {
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated utility spec must not be a symlink: %s", target)
		}
		return fmt.Errorf("%w: generated utility spec is immutable: %s", pathguard.ErrOutputExists, target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect generated utility spec %s: %w", target, err)
	}

	dir := filepath.Dir(target)
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve generated utility spec directory: %w", err)
	}
	if filepath.Clean(resolvedDir) != filepath.Clean(dir) {
		return fmt.Errorf("generated utility spec directory must not resolve through symlinks: %s", dir)
	}
	file, err := os.CreateTemp(dir, ".pgworkbench-utility-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary generated utility spec: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write generated utility spec: %w", err)
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return fmt.Errorf("set generated utility spec mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync generated utility spec: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close generated utility spec: %w", err)
	}
	if err := pathguard.PublishFileExclusive(tempPath, target); err != nil {
		return fmt.Errorf("install generated utility spec: %w", err)
	}
	return nil
}

func canonicalDirectory(dir string, label string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory: %s", label, resolved)
	}
	return filepath.Clean(resolved), nil
}

func secureExistingFile(root string, relative string) (string, error) {
	if !evidence.IsPortablePath(relative) {
		return "", fmt.Errorf("path is not portable: %s", relative)
	}
	current := root
	components := strings.Split(relative, "/")
	for index, component := range components {
		current = filepath.Join(current, filepath.FromSlash(component))
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path must not contain symlinks: %s", current)
		}
		if index < len(components)-1 && !info.IsDir() {
			return "", fmt.Errorf("path component is not a directory: %s", current)
		}
		if index == len(components)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("path is not a regular file: %s", current)
		}
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(root, resolved) {
		return "", fmt.Errorf("path escapes repository root: %s", relative)
	}
	return filepath.Clean(resolved), nil
}

func pathWithinRoot(root string, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validSourceSpecID(value string) bool {
	if !evidence.IsPortablePath(value) {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		for index, character := range component {
			alphaNumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
			if alphaNumeric || index > 0 && (character == '.' || character == '_' || character == '-') {
				continue
			}
			return false
		}
	}
	return true
}

func writeEnv(out *strings.Builder, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	out.WriteString(key)
	out.WriteByte('=')
	out.WriteString(shellValue(value))
	out.WriteByte('\n')
}

func shellValue(value string) string {
	if strings.Contains(value, "$") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(value) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func combinedAssertShell(fields map[string]string) string {
	var parts []string
	if fields["assert_shell"] != "" {
		parts = append(parts, fields["assert_shell"])
	}
	for _, path := range strings.Fields(fields["expect_files"]) {
		parts = append(parts, "test -s "+shellPath(path))
	}
	return strings.Join(parts, "; ")
}

func shellPath(path string) string {
	if filepath.IsAbs(path) || strings.Contains(path, "$") {
		return shellQuote(path)
	}
	return `"$REPO_DIR/` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(path) + `"`
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func sanitizeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "utility-test"
	}
	var out strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '.', ch == '-':
			out.WriteRune(ch)
		case ch == '/' || ch == ' ' || ch == '_':
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "utility-test"
	}
	return out.String()
}

func validRunID(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= 200 && sanitizeID(value) == value
}

func maxDurationMS(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}
