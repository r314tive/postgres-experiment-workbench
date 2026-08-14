// Package matrixartifact verifies that every run referenced by one experiment
// matrix is a complete, successful artifact produced by one exact candidate.
package matrixartifact

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
)

var matrixHeader = []string{
	"experiment", "pg_config", "profile_size", "repeat", "run_id",
	"exit_code", "status", "message", "run_dir",
}

// Options binds verification to both the expected candidate and the binary
// performing the verification. ExpectedRuns prevents an empty, truncated, or
// differently-sized matrix from satisfying the release gate.
type Options struct {
	ExpectedVersion string
	ExpectedCommit  string
	ExpectedRuns    int
	VerifierVersion string
	VerifierCommit  string
}

// Result is the machine-readable outcome of candidate matrix verification.
// Evidence mismatches are accumulated in Issues so callers can render all
// failures from one pass; invalid invocation or an unresolvable matrix path is
// returned as an error by VerifyCandidate.
type Result struct {
	Dir                 string   `json:"dir"`
	ExpectedVersion     string   `json:"expected_version"`
	ExpectedCommit      string   `json:"expected_commit"`
	ExpectedRuns        int      `json:"expected_runs"`
	VerifierVersion     string   `json:"verifier_version"`
	VerifierCommit      string   `json:"verifier_commit"`
	ExpectedPackID      string   `json:"expected_pack_id"`
	ExpectedPackVersion string   `json:"expected_pack_version"`
	ExpectedPackDigest  string   `json:"expected_pack_digest"`
	Rows                int      `json:"rows"`
	VerifiedRuns        int      `json:"verified_runs"`
	Issues              []string `json:"issues"`
}

// Valid reports whether the matrix and every referenced run passed all gates.
func (result Result) Valid() bool {
	return len(result.Issues) == 0
}

type matrixRow struct {
	experiment  string
	pgConfig    string
	profileSize string
	repeat      string
	runID       string
	exitCode    string
	status      string
	message     string
	runDir      string
}

// VerifyCandidate verifies one direct child of root/runs/matrices. Input may
// be that child's id, an absolute path, or a root-relative path. All run paths
// retained in runs.tsv must be clean absolute direct children of root/runs.
func VerifyCandidate(root string, input string, options Options) (Result, error) {
	result := Result{
		ExpectedVersion: options.ExpectedVersion,
		ExpectedCommit:  options.ExpectedCommit,
		ExpectedRuns:    options.ExpectedRuns,
		VerifierVersion: options.VerifierVersion,
		VerifierCommit:  options.VerifierCommit,
	}
	if err := validateOptions(options); err != nil {
		return result, err
	}

	canonicalRoot, runsRoot, matrixDir, err := resolveMatrixDir(root, input)
	if err != nil {
		return result, err
	}
	result.Dir = matrixDir
	pack, err := scenariopack.ValidateForEngine(canonicalRoot, options.ExpectedVersion)
	if err != nil {
		return result, fmt.Errorf("validate current checkout scenario pack: %w", err)
	}
	result.ExpectedPackID = pack.ID
	result.ExpectedPackVersion = pack.Version
	result.ExpectedPackDigest = pack.Digest
	packFiles := make(map[string]scenariopack.File, len(pack.Files))
	for _, file := range pack.Files {
		packFiles[file.Path] = file
	}

	content, err := readRegularFile(filepath.Join(matrixDir, "runs.tsv"))
	if err != nil {
		addIssue(&result, "runs.tsv: %v", err)
		return result, nil
	}
	rows, parseIssues := parseMatrixRows(content)
	result.Rows = len(rows)
	for _, issue := range parseIssues {
		addIssue(&result, "%s", issue)
	}
	if len(rows) != options.ExpectedRuns {
		addIssue(&result, "runs.tsv row count is %d, expected exactly %d", len(rows), options.ExpectedRuns)
	}
	if len(parseIssues) != 0 {
		return result, nil
	}

	seenRunIDs := make(map[string]int, len(rows))
	seenRunDirs := make(map[string]int, len(rows))
	for index, row := range rows {
		rowNumber := index + 2
		issuesBefore := len(result.Issues)
		verifyRow(&result, canonicalRoot, runsRoot, rowNumber, row, options, packFiles, seenRunIDs, seenRunDirs)
		if len(result.Issues) == issuesBefore {
			result.VerifiedRuns++
		}
	}
	return result, nil
}

func validateOptions(options Options) error {
	if options.ExpectedRuns <= 0 {
		return fmt.Errorf("expected matrix run count must be positive, got %d", options.ExpectedRuns)
	}
	for _, identity := range []struct {
		label   string
		version string
		commit  string
	}{
		{label: "expected candidate", version: options.ExpectedVersion, commit: options.ExpectedCommit},
		{label: "verifier", version: options.VerifierVersion, commit: options.VerifierCommit},
	} {
		if !runstate.IsEngineVersion(identity.version) || identity.version == runstate.EngineIdentityUnverified || strings.Contains(strings.ToLower(identity.version), "dev") {
			return fmt.Errorf("%s version must be a non-development canonical SemVer, got %q", identity.label, identity.version)
		}
		if !runstate.IsEngineCommit(identity.commit) || identity.commit == runstate.EngineIdentityUnverified {
			return fmt.Errorf("%s commit must be a full lowercase Git object ID, got %q", identity.label, identity.commit)
		}
	}
	if options.VerifierVersion != options.ExpectedVersion || options.VerifierCommit != options.ExpectedCommit {
		return fmt.Errorf(
			"verifier identity %s/%s does not match expected candidate %s/%s",
			options.VerifierVersion, options.VerifierCommit,
			options.ExpectedVersion, options.ExpectedCommit,
		)
	}
	return nil
}

func resolveMatrixDir(root string, input string) (canonicalRoot string, runsRoot string, matrixDir string, err error) {
	if root == "" {
		return "", "", "", fmt.Errorf("repository root is required")
	}
	if input == "" {
		return "", "", "", fmt.Errorf("matrix run id or directory is required")
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve repository root: %w", err)
	}
	canonicalRoot, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	if err := requireRealDirectory(canonicalRoot); err != nil {
		return "", "", "", fmt.Errorf("repository root: %w", err)
	}

	runsRoot = filepath.Join(canonicalRoot, "runs")
	if err := requireRealDirectory(runsRoot); err != nil {
		return "", "", "", fmt.Errorf("runs root: %w", err)
	}
	matricesRoot := filepath.Join(runsRoot, "matrices")
	if err := requireRealDirectory(matricesRoot); err != nil {
		return "", "", "", fmt.Errorf("matrix runs root: %w", err)
	}

	var matrixInput string
	switch {
	case filepath.IsAbs(input):
		matrixInput = input
	case filepath.Clean(input) == filepath.Base(input):
		matrixInput = filepath.Join(matricesRoot, input)
	default:
		if filepath.Clean(input) != input {
			return "", "", "", fmt.Errorf("matrix path must be clean: %q", input)
		}
		matrixInput = filepath.Join(canonicalRoot, input)
	}
	if filepath.Clean(matrixInput) != matrixInput {
		return "", "", "", fmt.Errorf("matrix path must be clean: %q", matrixInput)
	}
	if err := requireRealDirectory(matrixInput); err != nil {
		return "", "", "", fmt.Errorf("matrix directory: %w", err)
	}
	matrixDir, err = filepath.EvalSymlinks(matrixInput)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve matrix directory: %w", err)
	}
	if filepath.Dir(matrixDir) != matricesRoot || filepath.Base(matrixDir) == "." || filepath.Base(matrixDir) == ".." {
		return "", "", "", fmt.Errorf("matrix must be a direct child of %s: %s", matricesRoot, matrixDir)
	}
	return canonicalRoot, runsRoot, matrixDir, nil
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("must not be a symlink: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("is not a directory: %s", path)
	}
	return nil
}

func readRegularFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("must not be a symlink: %s", path)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("is not a regular file: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(file)
	opened, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if statErr != nil {
		return nil, statErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("changed while being opened: %s", path)
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("changed while being read: %s", path)
	}
	return content, nil
}

func parseMatrixRows(content []byte) ([]matrixRow, []string) {
	var issues []string
	if len(content) == 0 {
		return nil, []string{"runs.tsv is empty"}
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, []string{"runs.tsv contains a NUL byte"}
	}
	if bytes.IndexByte(content, '\r') >= 0 {
		return nil, []string{"runs.tsv must use LF line endings"}
	}

	firstLine := content
	if index := bytes.IndexByte(content, '\n'); index >= 0 {
		firstLine = content[:index]
	}
	expectedHeader := strings.Join(matrixHeader, "\t")
	if string(firstLine) != expectedHeader {
		issues = append(issues, fmt.Sprintf("runs.tsv header does not match the exact 9-column contract: got %q", string(firstLine)))
		return nil, issues
	}
	physicalLines := bytes.Split(content, []byte{'\n'})
	for index, line := range physicalLines {
		if len(line) == 0 && index == len(physicalLines)-1 {
			continue
		}
		if len(line) == 0 {
			issues = append(issues, fmt.Sprintf("runs.tsv contains a blank line at line %d", index+1))
			return nil, issues
		}
	}

	reader := csv.NewReader(bytes.NewReader(content))
	reader.Comma = '\t'
	reader.FieldsPerRecord = len(matrixHeader)
	reader.ReuseRecord = false
	records, err := reader.ReadAll()
	if err != nil {
		return nil, []string{fmt.Sprintf("runs.tsv parse failed: %v", err)}
	}
	if len(records) == 0 {
		return nil, []string{"runs.tsv is missing its header"}
	}
	if len(records) == 1 {
		return nil, nil
	}

	rows := make([]matrixRow, 0, len(records)-1)
	for index, record := range records[1:] {
		for _, field := range record {
			if strings.ContainsAny(field, "\x00\r\n") {
				return nil, []string{fmt.Sprintf("runs.tsv row %d contains a forbidden control character", index+2)}
			}
		}
		rows = append(rows, matrixRow{
			experiment: record[0], pgConfig: record[1], profileSize: record[2], repeat: record[3],
			runID: record[4], exitCode: record[5], status: record[6], message: record[7], runDir: record[8],
		})
	}
	return rows, issues
}

func verifyRow(
	result *Result,
	root string,
	runsRoot string,
	rowNumber int,
	row matrixRow,
	options Options,
	packFiles map[string]scenariopack.File,
	seenRunIDs map[string]int,
	seenRunDirs map[string]int,
) {
	prefix := fmt.Sprintf("runs.tsv row %d", rowNumber)
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "experiment", value: row.experiment},
		{name: "pg_config", value: row.pgConfig},
		{name: "profile_size", value: row.profileSize},
		{name: "run_id", value: row.runID},
		{name: "message", value: row.message},
		{name: "run_dir", value: row.runDir},
	} {
		if field.value == "" {
			addIssue(result, "%s has empty %s", prefix, field.name)
		}
	}
	if repeat, err := strconv.Atoi(row.repeat); err != nil || repeat <= 0 || strconv.Itoa(repeat) != row.repeat {
		addIssue(result, "%s repeat must be a canonical positive integer, got %q", prefix, row.repeat)
	}
	if row.exitCode != "0" {
		addIssue(result, "%s exit_code must be 0, got %q", prefix, row.exitCode)
	}
	if row.status != runstate.VerdictStatusPassed {
		addIssue(result, "%s status must be %s, got %q", prefix, runstate.VerdictStatusPassed, row.status)
	}
	if row.runID == "" || filepath.Base(row.runID) != row.runID || filepath.Clean(row.runID) != row.runID || row.runID == "." || row.runID == ".." {
		addIssue(result, "%s run_id is not a clean basename: %q", prefix, row.runID)
	}
	if first, exists := seenRunIDs[row.runID]; exists {
		addIssue(result, "%s duplicates run_id from row %d: %s", prefix, first, row.runID)
	} else {
		seenRunIDs[row.runID] = rowNumber
	}
	if !filepath.IsAbs(row.runDir) {
		addIssue(result, "%s run_dir must be absolute: %q", prefix, row.runDir)
		return
	}
	if filepath.Clean(row.runDir) != row.runDir {
		addIssue(result, "%s run_dir must be clean: %q", prefix, row.runDir)
		return
	}
	if filepath.Base(row.runDir) != row.runID {
		addIssue(result, "%s run_dir basename %q does not match run_id %q", prefix, filepath.Base(row.runDir), row.runID)
		return
	}
	if err := requireRealDirectory(row.runDir); err != nil {
		addIssue(result, "%s run_dir: %v", prefix, err)
		return
	}
	resolved, err := filepath.EvalSymlinks(row.runDir)
	if err != nil {
		addIssue(result, "%s run_dir resolve failed: %v", prefix, err)
		return
	}
	if filepath.Dir(resolved) != runsRoot {
		addIssue(result, "%s run_dir must be a direct child of %s: %s", prefix, runsRoot, row.runDir)
		return
	}
	if first, exists := seenRunDirs[resolved]; exists {
		addIssue(result, "%s duplicates run_dir from row %d: %s", prefix, first, row.runDir)
	} else {
		seenRunDirs[resolved] = rowNumber
	}

	runResult, err := runverify.Verify(root, resolved)
	if err != nil {
		addIssue(result, "%s run verification failed to execute: %v", prefix, err)
	} else {
		for _, issue := range runResult.Issues {
			addIssue(result, "%s run verification: %s", prefix, issue)
		}
	}

	manifest, err := loadStrictEnv(filepath.Join(resolved, "manifest.env"))
	if err != nil {
		addIssue(result, "%s manifest.env: %v", prefix, err)
		return
	}
	verdict, err := loadStrictEnv(filepath.Join(resolved, "verdict.env"))
	if err != nil {
		addIssue(result, "%s verdict.env: %v", prefix, err)
		return
	}
	checkField(result, prefix, "manifest.env run_id", manifest["run_id"], row.runID)
	checkField(result, prefix, "manifest.env experiment_spec_id", manifest["experiment_spec_id"], row.experiment)
	checkField(result, prefix, "manifest.env experiment_pg_config", manifest["experiment_pg_config"], row.pgConfig)
	checkField(result, prefix, "manifest.env profile_size", manifest["profile_size"], row.profileSize)
	checkField(result, prefix, "manifest.env engine_version", manifest["engine_version"], options.ExpectedVersion)
	checkField(result, prefix, "manifest.env engine_commit", manifest["engine_commit"], options.ExpectedCommit)
	checkField(result, prefix, "manifest.env pack_id", manifest["pack_id"], result.ExpectedPackID)
	checkField(result, prefix, "manifest.env pack_version", manifest["pack_version"], result.ExpectedPackVersion)
	checkField(result, prefix, "manifest.env pack_digest", manifest["pack_digest"], result.ExpectedPackDigest)
	checkField(result, prefix, "verdict.env run_id", verdict["run_id"], row.runID)
	checkField(result, prefix, "verdict.env status", verdict["status"], row.status)
	checkField(result, prefix, "verdict.env message", verdict["message"], row.message)

	// This is deliberately live-artifact verification, not bundle verification:
	// a matrix run has no bundle inventory yet. Its retained experiment-spec
	// snapshot is still mandatory and must bind both to the manifest digest and
	// to the file selected by experiment_spec_ref in the current checkout pack.
	provenance, err := readRegularFile(filepath.Join(resolved, "artifacts", "provenance", "experiment-spec.env"))
	if err != nil {
		addIssue(result, "%s experiment spec provenance: %v", prefix, err)
		return
	}
	provenanceDigest := evidence.DigestBytes(provenance)
	checkField(
		result,
		prefix,
		"experiment spec provenance digest",
		provenanceDigest,
		manifest["experiment_spec_digest"],
	)
	packFile, ok := packFiles[manifest["experiment_spec_ref"]]
	if !ok {
		addIssue(result, "%s manifest.env experiment_spec_ref is not in the current checkout pack: %q", prefix, manifest["experiment_spec_ref"])
		return
	}
	expectedPackDigest := "sha256:" + packFile.SHA256
	checkField(result, prefix, "manifest.env experiment_spec_digest for current pack", manifest["experiment_spec_digest"], expectedPackDigest)
	checkField(result, prefix, "experiment spec provenance digest for current pack", provenanceDigest, expectedPackDigest)
	if int64(len(provenance)) != packFile.Size {
		addIssue(result, "%s experiment spec provenance size is %d, expected current pack size %d", prefix, len(provenance), packFile.Size)
	}
}

func loadStrictEnv(path string) (map[string]string, error) {
	content, err := readRegularFile(path)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for index, rawLine := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=value", index+1)
		}
		key = strings.TrimSpace(key)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = struct{}{}
	}
	values, err := envfile.ParseBytes(path, content)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func checkField(result *Result, prefix string, label string, actual string, expected string) {
	if actual != expected {
		addIssue(result, "%s %s is %q, expected %q", prefix, label, actual, expected)
	}
}

func addIssue(result *Result, format string, args ...any) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, args...))
}

// Render writes a concise human-readable verification result.
func Render(writer io.Writer, result Result) error {
	status := "PASS"
	if !result.Valid() {
		status = "FAIL"
	}
	if _, err := fmt.Fprintf(
		writer,
		"%s: live candidate matrix %s rows=%d/%d verified=%d version=%s commit=%s pack=%s/%s digest=%s\n",
		status, result.Dir, result.Rows, result.ExpectedRuns, result.VerifiedRuns,
		result.ExpectedVersion, result.ExpectedCommit,
		result.ExpectedPackID, result.ExpectedPackVersion, result.ExpectedPackDigest,
	); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(writer, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

// RenderJSON writes the stable JSON representation of a verification result.
func RenderJSON(writer io.Writer, result Result) error {
	if result.Issues == nil {
		result.Issues = []string{}
	}
	payload := struct {
		Dir                 string   `json:"dir"`
		ExpectedVersion     string   `json:"expected_version"`
		ExpectedCommit      string   `json:"expected_commit"`
		ExpectedRuns        int      `json:"expected_runs"`
		VerifierVersion     string   `json:"verifier_version"`
		VerifierCommit      string   `json:"verifier_commit"`
		ExpectedPackID      string   `json:"expected_pack_id"`
		ExpectedPackVersion string   `json:"expected_pack_version"`
		ExpectedPackDigest  string   `json:"expected_pack_digest"`
		Rows                int      `json:"rows"`
		VerifiedRuns        int      `json:"verified_runs"`
		Valid               bool     `json:"valid"`
		Issues              []string `json:"issues"`
	}{
		Dir: result.Dir, ExpectedVersion: result.ExpectedVersion, ExpectedCommit: result.ExpectedCommit,
		ExpectedRuns: result.ExpectedRuns, VerifierVersion: result.VerifierVersion, VerifierCommit: result.VerifierCommit,
		ExpectedPackID: result.ExpectedPackID, ExpectedPackVersion: result.ExpectedPackVersion, ExpectedPackDigest: result.ExpectedPackDigest,
		Rows: result.Rows, VerifiedRuns: result.VerifiedRuns, Valid: result.Valid(), Issues: result.Issues,
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}
