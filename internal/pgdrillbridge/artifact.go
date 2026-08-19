package pgdrillbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
)

const maxArtifactBytes = int64(1 << 20)
const maxSourceBindingBytes = int64(16 << 20)
const maxPredicateBytes = 16 << 10

var (
	runIDPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)
	packIDPattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,159}$`)
	portableSpecIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,159}$`)
	portableVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,127}$`)
	runtimeTokenPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// Create exports one immutable baseline record. The destination is published
// with create-if-absent semantics and is never overwritten.
func Create(root, source, output string, options Options) (Artifact, error) {
	artifact, sourceDir, err := deriveFromSource(root, source, options, "")
	if err != nil {
		return Artifact{}, err
	}
	artifact.Digest, err = artifactDigest(artifact)
	if err != nil {
		return Artifact{}, err
	}
	if issues := validateArtifact(artifact); len(issues) != 0 {
		return Artifact{}, fmt.Errorf("derived pgdrill baseline is invalid: %s", strings.Join(issues, "; "))
	}

	outputPath, err := resolveOutput(output)
	if err != nil {
		return Artifact{}, err
	}
	canonicalSource, expectedOutput, err := resolveOutputContainment(sourceDir, outputPath)
	if err != nil {
		return Artifact{}, err
	}
	if pathWithin(canonicalSource, expectedOutput) {
		return Artifact{}, fmt.Errorf("baseline output must not be inside the source run: %s", outputPath)
	}
	if info, statErr := os.Lstat(outputPath); statErr == nil {
		return Artifact{}, fmt.Errorf("refusing to overwrite immutable pgdrill baseline: %s (%s)", outputPath, info.Mode())
	} else if !os.IsNotExist(statErr) {
		return Artifact{}, fmt.Errorf("inspect baseline output: %w", statErr)
	}
	parent := filepath.Dir(outputPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Artifact{}, fmt.Errorf("create baseline output parent: %w", err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return Artifact{}, fmt.Errorf("baseline output parent must be a non-symlink directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return Artifact{}, fmt.Errorf("resolve created baseline output parent: %w", err)
	}
	resolvedOutput := filepath.Join(resolvedParent, filepath.Base(outputPath))
	if filepath.Clean(resolvedOutput) != filepath.Clean(expectedOutput) {
		return Artifact{}, fmt.Errorf("baseline output parent changed while preparing publication")
	}
	if pathWithin(canonicalSource, resolvedOutput) {
		return Artifact{}, fmt.Errorf("baseline output must not be inside the source run: %s", outputPath)
	}

	content, err := marshalArtifact(artifact)
	if err != nil {
		return Artifact{}, err
	}
	temporary, err := os.CreateTemp(parent, ".pgworkbench-pgdrill-baseline-*.tmp")
	if err != nil {
		return Artifact{}, fmt.Errorf("create baseline staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return Artifact{}, fmt.Errorf("write baseline staging file: %w", err)
	}
	if err := temporary.Chmod(0o444); err != nil {
		_ = temporary.Close()
		return Artifact{}, fmt.Errorf("make baseline read-only: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close baseline staging file: %w", err)
	}
	staged, err := Verify(temporaryPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("verify baseline staging file: %w", err)
	}
	if !staged.IsValid() {
		return Artifact{}, fmt.Errorf("verify baseline staging file: %s", strings.Join(staged.Issues, "; "))
	}
	// A hard link in the same directory publishes the fully-written inode and
	// fails atomically if another writer created the destination first.
	if err := os.Link(temporaryPath, outputPath); err != nil {
		return Artifact{}, fmt.Errorf("publish immutable pgdrill baseline: %w", err)
	}
	artifact.ArtifactPath = outputPath
	return artifact, nil
}

// Verify validates a baseline record without trusting its self-declared
// digest. It intentionally does not claim that the unsigned producer is
// authentic or that the source run is still available.
func Verify(input string) (Verification, error) {
	path, err := resolveArtifact(input)
	if err != nil {
		return Verification{}, err
	}
	verification := Verification{Path: path, Issues: []string{}}
	content, err := readRegularLimited(path, maxArtifactBytes, "pgdrill baseline")
	if err != nil {
		return Verification{}, err
	}
	artifact, err := parseArtifact(content)
	if err != nil {
		verification.Issues = append(verification.Issues, fmt.Sprintf("baseline JSON: %v", err))
		return verification, nil
	}
	artifact.ArtifactPath = path
	verification.Artifact = &artifact
	verification.Issues = append(verification.Issues, validateArtifact(artifact)...)
	verification.Valid = verification.IsValid()
	return verification, nil
}

// VerifyAgainstSource additionally re-runs the workbench run verifier and
// independently re-derives every exported field from the supplied source. The
// source path is command input only and is never serialized into the record.
func VerifyAgainstSource(root, input, source string) (Verification, error) {
	verification, err := Verify(input)
	if err != nil || verification.Artifact == nil || !verification.IsValid() {
		return verification, err
	}
	artifact := *verification.Artifact
	options := Options{}
	if artifact.Predicate != nil {
		options.ReviewedPredicateSQL = artifact.Predicate.SQL
	}
	forceMode := artifact.SourceVerification.Mode
	rederived, _, deriveErr := deriveFromSource(root, source, options, forceMode)
	if deriveErr != nil {
		verification.Issues = append(verification.Issues, "source re-verification failed: "+deriveErr.Error())
		verification.Valid = false
		return verification, nil
	}
	rederived.Digest, err = artifactDigest(rederived)
	if err != nil {
		return verification, err
	}
	artifact.ArtifactPath = ""
	if !reflect.DeepEqual(artifact, rederived) {
		verification.Issues = append(verification.Issues, "baseline does not match independently re-derived source provenance")
	}
	verification.Valid = verification.IsValid()
	return verification, nil
}

func deriveFromSource(root, source string, options Options, forceMode string) (Artifact, string, error) {
	mode := forceMode
	if mode == "" {
		resolved, err := resolveSourceDir(root, source)
		if err != nil {
			return Artifact{}, "", err
		}
		if _, err := os.Lstat(filepath.Join(resolved, evidence.BundleInventoryName)); err == nil {
			mode = VerificationModeBundle
		} else if !os.IsNotExist(err) {
			return Artifact{}, "", fmt.Errorf("inspect source bundle inventory: %w", err)
		} else {
			mode = VerificationModeRun
		}
	}
	if options.RequireBundle && mode != VerificationModeBundle {
		return Artifact{}, "", fmt.Errorf("a complete run bundle is required")
	}
	if mode != VerificationModeRun && mode != VerificationModeBundle {
		return Artifact{}, "", fmt.Errorf("unsupported source verification mode %q", mode)
	}

	var verified runverify.Result
	var err error
	if mode == VerificationModeBundle {
		verified, err = runverify.VerifyBundle(root, source)
	} else {
		verified, err = runverify.Verify(root, source)
	}
	if err != nil {
		return Artifact{}, "", fmt.Errorf("verify source experiment: %w", err)
	}
	if !verified.Valid() {
		return Artifact{}, "", fmt.Errorf("source experiment verification failed: %s", strings.Join(verified.Issues, "; "))
	}
	sourceInfo, err := os.Lstat(verified.Dir)
	if err != nil || sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
		return Artifact{}, "", fmt.Errorf("source experiment root must be a non-symlink directory")
	}

	manifestPath := filepath.Join(verified.Dir, "manifest.env")
	manifest, err := envfile.Parse(manifestPath)
	if err != nil {
		return Artifact{}, "", fmt.Errorf("parse verified manifest.env: %w", err)
	}
	if !runstate.IsManifestSchemaVersion(manifest["schema_version"]) {
		return Artifact{}, "", fmt.Errorf("source must use a supported versioned run manifest")
	}
	if manifest["source_spec_kind"] != "" {
		return Artifact{}, "", fmt.Errorf("source must be an ordinary experiment run; source_spec_kind %q is not eligible", manifest["source_spec_kind"])
	}
	if verified.Verdict == nil || verified.Verdict.SchemaVersion != runstate.VerdictSchemaVersion || verified.Verdict.Status != runstate.VerdictStatusPassed {
		return Artifact{}, "", fmt.Errorf("source must have a versioned passed verdict")
	}
	if manifest["runtime_fingerprint_status"] != runstate.RuntimeFingerprintObserved {
		return Artifact{}, "", fmt.Errorf("source must contain an observed PostgreSQL runtime fingerprint")
	}

	manifestBinding, err := bindFile(verified.Dir, "manifest.env")
	if err != nil {
		return Artifact{}, "", err
	}
	verdictEnvBinding, err := bindFile(verified.Dir, "verdict.env")
	if err != nil {
		return Artifact{}, "", err
	}
	verdictJSONBinding, err := bindFile(verified.Dir, "verdict.json")
	if err != nil {
		return Artifact{}, "", err
	}
	var bundleInventory *FileBinding
	if mode == VerificationModeBundle {
		binding, err := bindFile(verified.Dir, evidence.BundleInventoryName)
		if err != nil {
			return Artifact{}, "", err
		}
		bundleInventory = &binding
	}

	artifact := Artifact{
		SchemaVersion:   SchemaVersion,
		ArtifactType:    ArtifactType,
		ContractVersion: ContractVersion,
		Classification:  Classification,
		SourceVerification: SourceVerification{
			Mode: mode, VerifierContract: RunVerifierContract, Verified: true,
			BundleInventory: bundleInventory,
		},
		Run: RunIdentity{
			ID: manifest["run_id"], StartedAt: manifest["started_at"], FinishedAt: verified.Verdict.FinishedAt,
			Manifest: manifestBinding, VerdictEnv: verdictEnvBinding, VerdictJSON: verdictJSONBinding,
		},
		ScenarioPack:   ScenarioPackIdentity{ID: manifest["pack_id"], Version: manifest["pack_version"], Digest: manifest["pack_digest"]},
		ExperimentSpec: ExperimentSpecIdentity{ID: manifest["experiment_spec_id"], Ref: manifest["experiment_spec_ref"], Digest: manifest["experiment_spec_digest"]},
		Postgres: PostgresIdentity{
			Runtime: manifest["runtime"], RuntimeOS: manifest["runtime_os"], RuntimeArch: manifest["runtime_arch"],
			ServerVersionNum: manifest["postgres_server_version_num"], ServerMajor: manifest["postgres_server_major"],
			FingerprintObservedAt: manifest["runtime_fingerprint_observed_at"],
		},
		AssuranceBoundary: AssuranceBoundary{
			Scope: ProvenanceScope, Authenticity: Authenticity, ConsumerValidationStatus: ConsumerValidationStatus,
		},
	}
	if strings.TrimSpace(options.ReviewedPredicateSQL) != "" {
		predicate, err := newReviewedPredicate(options.ReviewedPredicateSQL)
		if err != nil {
			return Artifact{}, "", err
		}
		artifact.Predicate = &predicate
	}
	return artifact, verified.Dir, nil
}

func validateArtifact(artifact Artifact) []string {
	var issues []string
	add := func(format string, values ...any) { issues = append(issues, fmt.Sprintf(format, values...)) }
	if artifact.SchemaVersion != SchemaVersion {
		add("unsupported schema_version %q", artifact.SchemaVersion)
	}
	if artifact.ArtifactType != ArtifactType {
		add("artifact_type must be %q", ArtifactType)
	}
	if artifact.ContractVersion != ContractVersion {
		add("contract_version must be %q", ContractVersion)
	}
	if artifact.Classification != Classification {
		add("classification must be %q", Classification)
	}
	if artifact.SourceVerification.VerifierContract != RunVerifierContract || !artifact.SourceVerification.Verified {
		add("source verification contract is invalid")
	}
	switch artifact.SourceVerification.Mode {
	case VerificationModeRun:
		if artifact.SourceVerification.BundleInventory != nil {
			add("run verification mode must not bind a bundle inventory")
		}
	case VerificationModeBundle:
		if artifact.SourceVerification.BundleInventory == nil {
			add("complete-bundle verification mode requires a bundle inventory binding")
		}
	default:
		add("unsupported source verification mode %q", artifact.SourceVerification.Mode)
	}
	if artifact.SourceVerification.BundleInventory != nil {
		validateFileBinding(add, "source_verification.bundle_inventory", *artifact.SourceVerification.BundleInventory, evidence.BundleInventoryName)
	}
	if !runIDPattern.MatchString(artifact.Run.ID) {
		add("run.id is not a portable identifier")
	}
	startedAt, startErr := parseCanonicalTime(artifact.Run.StartedAt)
	finishedAt, finishErr := parseCanonicalTime(artifact.Run.FinishedAt)
	if startErr != nil {
		add("run.started_at is not canonical UTC RFC3339")
	}
	if finishErr != nil {
		add("run.finished_at is not canonical UTC RFC3339")
	}
	if startErr == nil && finishErr == nil && finishedAt.Before(startedAt) {
		add("run.finished_at precedes run.started_at")
	}
	validateFileBinding(add, "run.manifest", artifact.Run.Manifest, "manifest.env")
	validateFileBinding(add, "run.verdict_env", artifact.Run.VerdictEnv, "verdict.env")
	validateFileBinding(add, "run.verdict_json", artifact.Run.VerdictJSON, "verdict.json")
	if !packIDPattern.MatchString(artifact.ScenarioPack.ID) {
		add("scenario_pack.id is not a portable identifier")
	}
	if !portableVersionPattern.MatchString(artifact.ScenarioPack.Version) {
		add("scenario_pack.version is not a portable version token")
	}
	if !evidence.IsDigest(artifact.ScenarioPack.Digest) {
		add("scenario_pack.digest is not a canonical sha256 digest")
	}
	if !portableSpecIDPattern.MatchString(artifact.ExperimentSpec.ID) {
		add("experiment_spec.id is not a portable identifier")
	}
	if !evidence.IsPortablePath(artifact.ExperimentSpec.Ref) || !strings.HasPrefix(artifact.ExperimentSpec.Ref, "experiments/") || !strings.HasSuffix(artifact.ExperimentSpec.Ref, ".env") {
		add("experiment_spec.ref must be a portable path under experiments ending in .env")
	} else if strings.TrimSuffix(strings.TrimPrefix(artifact.ExperimentSpec.Ref, "experiments/"), ".env") != artifact.ExperimentSpec.ID {
		add("experiment_spec.ref does not match experiment_spec.id")
	}
	if !evidence.IsDigest(artifact.ExperimentSpec.Digest) {
		add("experiment_spec.digest is not a canonical sha256 digest")
	}
	if artifact.Postgres.Runtime != "docker" && artifact.Postgres.Runtime != "native" {
		add("postgres.runtime must be docker or native")
	}
	if !runtimeTokenPattern.MatchString(artifact.Postgres.RuntimeOS) {
		add("postgres.runtime_os is not a canonical runtime token")
	}
	if !runtimeTokenPattern.MatchString(artifact.Postgres.RuntimeArch) {
		add("postgres.runtime_arch is not a canonical runtime token")
	}
	versionNum, versionErr := strconv.Atoi(artifact.Postgres.ServerVersionNum)
	if versionErr != nil || versionNum < 10000 || strconv.Itoa(versionNum) != artifact.Postgres.ServerVersionNum {
		add("postgres.server_version_num is not a canonical PostgreSQL server version number")
	} else if postgresMajor(versionNum) != artifact.Postgres.ServerMajor {
		add("postgres.server_major does not match server_version_num")
	}
	if _, err := parseCanonicalTime(artifact.Postgres.FingerprintObservedAt); err != nil {
		add("postgres.fingerprint_observed_at is not canonical UTC RFC3339")
	}
	if artifact.Predicate != nil {
		issues = append(issues, validatePredicate(*artifact.Predicate)...)
	}
	if artifact.AssuranceBoundary.Scope != ProvenanceScope || artifact.AssuranceBoundary.Authenticity != Authenticity || artifact.AssuranceBoundary.ConsumerValidationStatus != ConsumerValidationStatus {
		add("assurance_boundary is outside the baseline provenance contract")
	}
	expectedDigest, err := artifactDigest(artifact)
	if err != nil {
		add("derive artifact digest: %v", err)
	} else if artifact.Digest != expectedDigest {
		add("digest does not match canonical baseline content")
	}
	return issues
}

func validateFileBinding(add func(string, ...any), label string, binding FileBinding, expectedFile string) {
	if binding.File != expectedFile || !evidence.IsPortablePath(binding.File) {
		add("%s.file must be %q", label, expectedFile)
	}
	if !evidence.IsDigest(binding.Digest) {
		add("%s.digest is not a canonical sha256 digest", label)
	}
}

func bindFile(root, relative string) (FileBinding, error) {
	if !evidence.IsPortablePath(relative) {
		return FileBinding{}, fmt.Errorf("refuse non-portable source binding %q", relative)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if _, err := readRegularLimited(path, maxSourceBindingBytes, "source "+relative); err != nil {
		return FileBinding{}, err
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return FileBinding{}, fmt.Errorf("digest source %s: %w", relative, err)
	}
	return FileBinding{File: relative, Digest: digest}, nil
}

func artifactDigest(artifact Artifact) (string, error) {
	artifact.Digest = ""
	artifact.ArtifactPath = ""
	content, err := json.Marshal(artifact)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func marshalArtifact(artifact Artifact) ([]byte, error) {
	content, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func parseArtifact(content []byte) (Artifact, error) {
	if err := validateJSONDocument(content); err != nil {
		return Artifact{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var artifact Artifact
	if err := decoder.Decode(&artifact); err != nil {
		return Artifact{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Artifact{}, fmt.Errorf("multiple JSON values")
		}
		return Artifact{}, err
	}
	return artifact, nil
}

func resolveSourceDir(root, input string) (string, error) {
	result, err := runverify.Verify(root, input)
	if err != nil {
		return "", fmt.Errorf("resolve source run: %w", err)
	}
	return result.Dir, nil
}

func resolveOutput(output string) (string, error) {
	if strings.TrimSpace(output) == "" {
		return "", fmt.Errorf("baseline output path is required")
	}
	if strings.HasSuffix(output, string(filepath.Separator)) {
		output = filepath.Join(output, DefaultFileName)
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("resolve baseline output: %w", err)
	}
	return abs, nil
}

// resolveOutputContainment resolves the physical source root and the nearest
// existing output ancestor before Create performs any filesystem mutation.
// Missing parent components are appended to that canonical ancestor, so a
// symlink anywhere in the existing prefix cannot hide a destination inside
// the source run.
func resolveOutputContainment(sourceDir, outputPath string) (string, string, error) {
	canonicalSource, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve physical source experiment root: %w", err)
	}
	canonicalSource, err = filepath.Abs(canonicalSource)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize physical source experiment root: %w", err)
	}

	parent := filepath.Dir(outputPath)
	existing := parent
	var missing []string
	for {
		info, statErr := os.Lstat(existing)
		if statErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(existing)
			if resolveErr != nil {
				return "", "", fmt.Errorf("resolve baseline output ancestor %s: %w", existing, resolveErr)
			}
			resolvedInfo, infoErr := os.Stat(resolved)
			if infoErr != nil || !resolvedInfo.IsDir() {
				return "", "", fmt.Errorf("baseline output ancestor must resolve to a directory: %s", existing)
			}
			if info.Mode()&os.ModeSymlink != 0 && filepath.Clean(existing) == filepath.Clean(parent) {
				return "", "", fmt.Errorf("baseline output parent must be a non-symlink directory")
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return "", "", fmt.Errorf("canonicalize baseline output ancestor: %w", err)
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(canonicalSource), filepath.Join(resolved, filepath.Base(outputPath)), nil
		}
		if !os.IsNotExist(statErr) {
			return "", "", fmt.Errorf("inspect baseline output ancestor %s: %w", existing, statErr)
		}
		parentDir := filepath.Dir(existing)
		if parentDir == existing {
			return "", "", fmt.Errorf("could not resolve an existing baseline output ancestor for %s", outputPath)
		}
		missing = append(missing, filepath.Base(existing))
		existing = parentDir
	}
}

func resolveArtifact(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("pgdrill baseline path is required")
	}
	path, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect pgdrill baseline: %w", err)
	}
	if info.IsDir() {
		path = filepath.Join(path, DefaultFileName)
		info, err = os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("inspect pgdrill baseline: %w", err)
		}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("pgdrill baseline must be a non-symlink regular file")
	}
	return path, nil
}

func readRegularLimited(path string, limit int64, label string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a non-symlink regular file", label)
	}
	if info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("%s size must be between 1 and %d bytes", label, limit)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return content, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.UTC().Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("not canonical UTC RFC3339")
	}
	return parsed.UTC(), nil
}

func postgresMajor(versionNum int) string {
	if versionNum >= 100000 {
		return strconv.Itoa(versionNum / 10000)
	}
	return fmt.Sprintf("%d.%d", versionNum/10000, (versionNum/100)%100)
}

func newReviewedPredicate(sql string) (ReviewedPredicate, error) {
	sql = strings.TrimSpace(sql)
	predicate := ReviewedPredicate{
		ReviewStatus: PredicateReviewStatus, Language: PredicateLanguage, Kind: PredicateKind,
		SQL: sql, ExpectedBoolean: true, Execution: PredicateExecution, SafetyBasis: PredicateSafetyBasis,
	}
	predicate.Digest = evidence.DigestBytes([]byte(sql))
	if issues := validatePredicate(predicate); len(issues) != 0 {
		return ReviewedPredicate{}, fmt.Errorf("reviewed predicate is invalid: %s", strings.Join(issues, "; "))
	}
	return predicate, nil
}

// LoadReviewedPredicateSQL reads a predicate review input without following a
// symlink. Calling this function is still an explicit human-review assertion;
// lexical filtering cannot prove PostgreSQL read-only semantics.
func LoadReviewedPredicateSQL(path string) (string, error) {
	content, err := readRegularLimited(path, maxPredicateBytes, "reviewed predicate SQL")
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(content))
	if err := validateReviewedSQL(value); err != nil {
		return "", err
	}
	return value, nil
}

func validatePredicate(predicate ReviewedPredicate) []string {
	var issues []string
	if predicate.ReviewStatus != PredicateReviewStatus {
		issues = append(issues, "predicate.review_status must be reviewed")
	}
	if predicate.Language != PredicateLanguage || predicate.Kind != PredicateKind {
		issues = append(issues, "predicate language or kind is invalid")
	}
	if predicate.Execution != PredicateExecution || predicate.SafetyBasis != PredicateSafetyBasis {
		issues = append(issues, "predicate execution boundary is invalid")
	}
	if !predicate.ExpectedBoolean {
		issues = append(issues, "predicate.expected_boolean must be true")
	}
	if err := validateReviewedSQL(predicate.SQL); err != nil {
		issues = append(issues, err.Error())
	}
	if predicate.Digest != evidence.DigestBytes([]byte(predicate.SQL)) {
		issues = append(issues, "predicate.digest does not match SQL bytes")
	}
	return issues
}

func validateReviewedSQL(sql string) error {
	if sql == "" || len(sql) > maxPredicateBytes || !utf8.ValidString(sql) {
		return fmt.Errorf("predicate.sql must be non-empty UTF-8 with at most %d bytes", maxPredicateBytes)
	}
	for _, character := range sql {
		if character < 0x20 && character != '\n' && character != '\r' && character != '\t' || character == 0x7f {
			return fmt.Errorf("predicate.sql contains control characters")
		}
	}
	lower := strings.ToLower(sql)
	trimmed := strings.TrimSpace(lower)
	if !strings.HasPrefix(trimmed, "select ") && !strings.HasPrefix(trimmed, "select\n") && !strings.HasPrefix(trimmed, "select\t") && !strings.HasPrefix(trimmed, "select\r") {
		return fmt.Errorf("predicate.sql must be one explicit SELECT statement")
	}
	if strings.ContainsAny(sql, ";\\") || strings.Contains(lower, "--") || strings.Contains(lower, "/*") || strings.Contains(lower, "*/") || strings.Contains(lower, "$$") {
		return fmt.Errorf("predicate.sql must not contain statement separators, comments, psql commands, or dollar quoting")
	}
	for _, token := range []string{
		"insert", "update", "delete", "merge", "copy", "call", "do", "create", "alter", "drop", "truncate",
		"grant", "revoke", "vacuum", "analyze", "cluster", "reindex", "refresh", "checkpoint", "discard", "listen",
		"notify", "load", "set", "reset", "lock", "prepare", "execute", "deallocate", "begin", "commit", "rollback",
		"savepoint", "release", "into", "program", "pg_read_file", "pg_read_binary_file", "pg_ls_dir", "pg_stat_file",
		"pg_terminate_backend", "pg_cancel_backend", "pg_reload_conf", "pg_rotate_logfile", "set_config", "nextval", "setval",
		"lo_import", "lo_export",
	} {
		if containsSQLToken(lower, token) {
			return fmt.Errorf("predicate.sql contains forbidden token %q", token)
		}
	}
	for _, sensitive := range []string{"password", "passwd", "credential", "authorization", "api_key", "apikey", "private_key", "access_token", "secret"} {
		if strings.Contains(lower, sensitive) {
			return fmt.Errorf("predicate.sql contains credential-like material")
		}
	}
	if strings.Contains(lower, "://") || regexp.MustCompile(`(?:^|[[:space:]'\"])/(?:[^[:space:]'\"]+)`).MatchString(sql) {
		return fmt.Errorf("predicate.sql must not contain mutable absolute paths or connection URIs")
	}
	return nil
}

func containsSQLToken(sql, token string) bool {
	for offset := 0; ; {
		index := strings.Index(sql[offset:], token)
		if index < 0 {
			return false
		}
		index += offset
		beforeOK := index == 0 || !isIdentifierByte(sql[index-1])
		after := index + len(token)
		afterOK := after == len(sql) || !isIdentifierByte(sql[after])
		if beforeOK && afterOK {
			return true
		}
		offset = index + 1
	}
}

func isIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '$'
}

func validateJSONDocument(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := validateJSONValue(decoder, "$", true); err != nil {
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

func validateJSONValue(decoder *json.Decoder, location string, root bool) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%s: %w", location, err)
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		if root {
			return fmt.Errorf("%s: root must be an object", location)
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s: object key is not text", location)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%s: duplicate object key %q", location, key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, location+"."+key, false); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("%s: invalid object closing token", location)
		}
		return nil
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := validateJSONValue(decoder, fmt.Sprintf("%s[%d]", location, index), false); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("%s: invalid array closing token", location)
		}
		return nil
	default:
		return fmt.Errorf("%s: unexpected delimiter %q", location, delimiter)
	}
}

func RenderJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func Render(writer io.Writer, verification Verification) error {
	if verification.IsValid() {
		_, err := fmt.Fprintf(writer, "PASS: pgdrill baseline provenance %s\n", verification.Path)
		return err
	}
	if _, err := fmt.Fprintf(writer, "FAIL: pgdrill baseline provenance %s\n", verification.Path); err != nil {
		return err
	}
	for _, issue := range verification.Issues {
		if _, err := fmt.Fprintf(writer, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}
