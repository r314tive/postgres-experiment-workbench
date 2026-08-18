package runverify

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
)

type Result struct {
	Dir                     string
	BundleInventoryRequired bool
	Issues                  []string
	Verdict                 *runstate.Verdict
	Metrics                 *MetricsCoverage
}

// MetricsCoverage exposes the independently parsed timestamp extent without
// making the generic run verifier assume which inner phase is authoritative.
// Benchmark verification binds this extent to its measure phase.
type MetricsCoverage struct {
	Samples int
	First   string
	Last    string
}

func (r Result) Valid() bool {
	return len(r.Issues) == 0
}

func Verify(root string, input string) (Result, error) {
	return VerifyWithOptions(root, input, Options{})
}

type Options struct {
	RequireBundleInventory bool
}

func VerifyBundle(root string, input string) (Result, error) {
	return VerifyWithOptions(root, input, Options{RequireBundleInventory: true})
}

func VerifyWithOptions(root string, input string, options Options) (Result, error) {
	dir, err := runartifact.ResolveRunDir(root, input)
	if err != nil {
		return Result{}, err
	}

	result := Result{Dir: dir, BundleInventoryRequired: options.RequireBundleInventory}
	manifestPath := filepath.Join(dir, "manifest.env")
	verdictEnvPath := filepath.Join(dir, "verdict.env")
	verdictJSONPath := filepath.Join(dir, "verdict.json")

	manifest, manifestOK := loadEnv(&result, manifestPath, "manifest.env")
	verdict, verdictOK := loadEnv(&result, verdictEnvPath, "verdict.env")
	verdictJSON, verdictJSONOK := loadVerdictJSON(&result, verdictJSONPath)
	if verdictJSONOK {
		result.Verdict = &verdictJSON
	}

	if manifestOK {
		checkRequiredEnv(&result, "manifest.env", manifest, []string{
			"run_id",
			"started_at",
			"experiment_spec_id",
			"experiment_topology",
			"experiment_pg_config",
			"profile_size",
			"run_dir",
		})
		if checkSchema(&result, "manifest.env", manifest.Value("schema_version", ""), runstate.ManifestSchemaVersion) {
			checkAllowedEnvKeys(&result, "manifest.env", manifest, []string{
				"schema_version", "artifact_type", "run_id", "started_at", "experiment_spec",
				"experiment_spec_id", "experiment_spec_ref", "experiment_spec_digest", "execution_parameters_digest",
				"source_spec_kind", "source_spec_id", "source_spec_ref", "source_spec_digest",
				"experiment_identity_digest", "runtime", "engine_version", "engine_commit",
				"pack_id", "pack_version", "pack_digest",
				"runtime_fingerprint_status", "runtime_fingerprint_target", "runtime_os", "runtime_arch", "postgres_server_version_num",
				"postgres_server_major", "runtime_fingerprint_observed_at",
				"experiment_name", "experiment_topology", "experiment_pg_config", "profile",
				"dataset_spec", "profile_size", "workload_spec", "background_specs", "metrics_enabled", "metrics_samples",
				"artifact_root", "run_dir",
			})
			checkRequiredEnv(&result, "manifest.env", manifest, []string{
				"artifact_type",
				"experiment_spec_ref",
				"execution_parameters_digest",
				"experiment_identity_digest",
				"runtime",
				"engine_version",
				"engine_commit",
				"runtime_fingerprint_status",
				"runtime_fingerprint_target",
				"metrics_enabled",
				"artifact_root",
			})
			checkValue(&result, "manifest.env", "artifact_type", manifest.Value("artifact_type", ""), runstate.ManifestArtifactType)
			checkValue(&result, "manifest.env", "artifact_root", manifest.Value("artifact_root", ""), ".")
			checkPresentEnvKeys(&result, "manifest.env", manifest, []string{
				"source_spec_kind", "source_spec_id", "source_spec_ref", "source_spec_digest",
				"runtime_os", "runtime_arch", "postgres_server_version_num", "postgres_server_major",
				"runtime_fingerprint_observed_at", "metrics_samples",
			})
			checkPortablePath(&result, "manifest.env", "experiment_spec_ref", manifest.Value("experiment_spec_ref", ""))
			checkOptionalDigest(&result, "manifest.env", "experiment_spec_digest", manifest.Value("experiment_spec_digest", ""))
			checkSourceSpecIdentity(&result, manifest)
			checkDigest(&result, "manifest.env", "execution_parameters_digest", manifest.Value("execution_parameters_digest", ""))
			checkManifestIdentity(&result, manifest)
			checkEngineIdentity(&result, manifest)
			checkPackIdentity(&result, manifest)
			checkRuntime(&result, manifest.Value("runtime", ""))
			checkRuntimeFingerprint(&result, manifest)
			checkMetricsFlag(&result, manifest.Value("metrics_enabled", ""))
			checkMetricsSamples(&result, manifest.Value("metrics_samples", ""))
		}
	}

	metricsRequired := true
	if (manifestOK && manifest.Value("metrics_enabled", "") == "0") || (verdictJSONOK && verdictJSON.Status == "failed") {
		metricsRequired = false
	}
	expectedMetricsSamples := ""
	if manifestOK {
		expectedMetricsSamples = manifest.Value("metrics_samples", "")
	}
	metricsTimestampRequired := manifestOK && manifest.Value("source_spec_kind", "") == "benchmark"
	result.Metrics = checkMetrics(&result, filepath.Join(dir, "metrics.csv"), metricsRequired, expectedMetricsSamples, metricsTimestampRequired)

	if verdictOK {
		checkRequiredEnv(&result, "verdict.env", verdict, []string{
			"status",
			"message",
			"finished_at",
			"workload_exit",
			"assert_exit",
			"scan_exit",
		})
		checkExitCode(&result, verdict, "workload_exit")
		checkExitCode(&result, verdict, "assert_exit")
		checkExitCode(&result, verdict, "scan_exit")
		checkVerdictEnvOutcome(&result, verdict)
		if checkSchema(&result, "verdict.env", verdict.Value("schema_version", ""), runstate.VerdictSchemaVersion) {
			checkAllowedEnvKeys(&result, "verdict.env", verdict, []string{
				"schema_version", "artifact_type", "run_id", "status", "message", "started_at",
				"finished_at", "experiment_spec_id", "experiment_spec_digest",
				"experiment_identity_digest", "manifest_digest", "artifact_root", "run_dir",
				"workload_exit", "assert_exit", "scan_exit",
			})
			checkRequiredEnv(&result, "verdict.env", verdict, []string{
				"artifact_type",
				"run_id",
				"started_at",
				"experiment_spec_id",
				"experiment_identity_digest",
				"manifest_digest",
				"artifact_root",
				"run_dir",
			})
			checkValue(&result, "verdict.env", "artifact_type", verdict.Value("artifact_type", ""), runstate.VerdictArtifactType)
			checkValue(&result, "verdict.env", "artifact_root", verdict.Value("artifact_root", ""), ".")
			checkOptionalDigest(&result, "verdict.env", "experiment_spec_digest", verdict.Value("experiment_spec_digest", ""))
			checkDigest(&result, "verdict.env", "experiment_identity_digest", verdict.Value("experiment_identity_digest", ""))
			checkDigest(&result, "verdict.env", "manifest_digest", verdict.Value("manifest_digest", ""))
		}
	}

	if verdictJSONOK {
		checkVerdictJSONKeys(&result, verdictJSON)
		checkVerdictOutcome(&result, "verdict.json", verdictJSON.Status, verdictJSON.WorkloadExit, verdictJSON.AssertExit, verdictJSON.ScanExit)
		if verdictJSON.SchemaVersion != "" {
			if verdictJSON.SchemaVersion != runstate.VerdictSchemaVersion {
				addIssue(&result, "verdict.json unsupported schema_version: %s", verdictJSON.SchemaVersion)
			} else {
				checkVerdictJSONV1(&result, verdictJSON)
			}
		}
		checkVerdictConsistency(&result, manifest, verdict, verdictJSON)
	}

	if manifestOK && verdictOK && verdictJSONOK {
		checkSchemaCohesion(&result, manifest.Value("schema_version", ""), verdict.Value("schema_version", ""), verdictJSON.SchemaVersion)
		checkRecordedRunDirConsistency(&result, manifest.Value("run_dir", ""), verdict.Value("run_dir", ""), verdictJSON.RunDir)
		checkManifestBindings(&result, manifestPath, manifest, verdict, verdictJSON)
		if manifest.Value("schema_version", "") == runstate.ManifestSchemaVersion && verdictJSON.Status == "passed" && manifest.Value("runtime_fingerprint_status", "") != runstate.RuntimeFingerprintObserved {
			addIssue(&result, "passed versioned run requires an observed runtime fingerprint")
		}
	}
	if options.RequireBundleInventory && manifestOK && manifest.Value("schema_version", "") == runstate.ManifestSchemaVersion {
		checkExperimentSpecSnapshot(&result, dir, manifest)
		switch manifest.Value("source_spec_kind", "") {
		case "utility-test":
			checkUtilityProvenanceSnapshots(&result, dir, manifest)
		case "benchmark":
			checkSourceSpecSnapshot(&result, dir, manifest, "benchmark")
		}
	}

	checkBundleInventory(&result, dir, manifest, options.RequireBundleInventory)
	return result, nil
}

func checkExperimentSpecSnapshot(result *Result, dir string, manifest runartifact.Env) {
	generatedSpecPath := filepath.Join(dir, "artifacts", "provenance", "experiment-spec.env")
	if !checkRequiredRegularFile(result, generatedSpecPath, "experiment spec snapshot") {
		return
	}
	actual, err := evidence.DigestFile(generatedSpecPath)
	if err != nil {
		addIssue(result, "experiment spec snapshot digest failed: %v", err)
		return
	}
	if digest := manifest.Value("experiment_spec_digest", ""); digest == "" || actual != digest {
		addIssue(result, "experiment spec snapshot digest does not match manifest.env")
	}
}

func checkUtilityProvenanceSnapshots(result *Result, dir string, manifest runartifact.Env) {
	generatedSpecPath := filepath.Join(dir, "artifacts", "provenance", "experiment-spec.env")
	sourceSpecPath := filepath.Join(dir, "artifacts", "provenance", "source-utility-test.env")
	if checkRequiredRegularFile(result, sourceSpecPath, "utility source spec snapshot") {
		actual, err := evidence.DigestFile(sourceSpecPath)
		if err != nil {
			addIssue(result, "utility source spec snapshot digest failed: %v", err)
		} else if digest := manifest.Value("source_spec_digest", ""); digest == "" || actual != digest {
			addIssue(result, "utility source spec snapshot digest does not match manifest.env")
		}
	}

	generatedSpec, err := runartifact.LoadOptionalEnv(generatedSpecPath)
	if err != nil {
		addIssue(result, "utility generated spec snapshot parse failed: %v", err)
		return
	}
	seen := make(map[string]struct{})
	for _, expected := range strings.Fields(generatedSpec.Value("EXPERIMENT_CAPTURE_FILES", "")) {
		if !validCapturedUtilityPath(expected) {
			addIssue(result, "utility generated spec declares unsupported captured output: %s", expected)
			continue
		}
		if _, ok := seen[expected]; ok {
			addIssue(result, "utility generated spec declares duplicate captured output: %s", expected)
			continue
		}
		seen[expected] = struct{}{}
		capturedPath := filepath.Join(dir, "artifacts", "utility", filepath.FromSlash(expected))
		checkRequiredRegularFile(result, capturedPath, "captured utility output "+expected)
	}
}

func checkSourceSpecSnapshot(result *Result, dir string, manifest runartifact.Env, kind string) {
	sourceSpecPath := filepath.Join(dir, "artifacts", "provenance", "source-"+kind+".env")
	if !checkRequiredRegularFile(result, sourceSpecPath, kind+" source spec snapshot") {
		return
	}
	actual, err := evidence.DigestFile(sourceSpecPath)
	if err != nil {
		addIssue(result, "%s source spec snapshot digest failed: %v", kind, err)
		return
	}
	if digest := manifest.Value("source_spec_digest", ""); digest == "" || actual != digest {
		addIssue(result, "%s source spec snapshot digest does not match manifest.env", kind)
	}
}

func validCapturedUtilityPath(value string) bool {
	if !evidence.IsPortablePath(value) || !(strings.HasPrefix(value, "logs/utility/") || strings.HasPrefix(value, ".tmp/utility-output/")) {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
		for _, character := range component {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func Render(w io.Writer, result Result) error {
	artifactLabel := "run artifact"
	if result.BundleInventoryRequired {
		artifactLabel = "complete run bundle"
	}
	if result.Valid() {
		_, err := fmt.Fprintf(w, "PASS: %s %s\n", artifactLabel, result.Dir)
		return err
	}

	if _, err := fmt.Fprintf(w, "FAIL: %s %s\n", artifactLabel, result.Dir); err != nil {
		return err
	}
	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(w, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, result Result) error {
	issues := result.Issues
	if issues == nil {
		issues = []string{}
	}
	payload := struct {
		Dir                     string   `json:"dir"`
		BundleInventoryRequired bool     `json:"bundle_inventory_required"`
		Valid                   bool     `json:"valid"`
		Issues                  []string `json:"issues"`
	}{
		Dir:                     result.Dir,
		BundleInventoryRequired: result.BundleInventoryRequired,
		Valid:                   result.Valid(),
		Issues:                  issues,
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func loadEnv(result *Result, path string, label string) (runartifact.Env, bool) {
	if !checkRequiredRegularFile(result, path, label) {
		return runartifact.Env{}, false
	}
	values, err := runartifact.LoadOptionalEnv(path)
	if err != nil {
		addIssue(result, "%s parse failed: %v", label, err)
		return runartifact.Env{}, false
	}
	return values, true
}

func loadVerdictJSON(result *Result, path string) (runstate.Verdict, bool) {
	if !checkRequiredRegularFile(result, path, "verdict.json") {
		return runstate.Verdict{}, false
	}

	content, err := os.ReadFile(path)
	if err != nil {
		addIssue(result, "verdict.json read failed: %v", err)
		return runstate.Verdict{}, false
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		addIssue(result, "verdict.json parse failed: %v", err)
		return runstate.Verdict{}, false
	}

	var verdict runstate.Verdict
	if err := json.Unmarshal(content, &verdict); err != nil {
		addIssue(result, "verdict.json schema failed: %v", err)
		return runstate.Verdict{}, false
	}

	for _, key := range []string{
		"run_id",
		"status",
		"message",
		"started_at",
		"finished_at",
		"experiment_spec",
		"run_dir",
		"workload_exit",
		"assert_exit",
		"scan_exit",
	} {
		if _, ok := raw[key]; !ok {
			addIssue(result, "verdict.json missing key: %s", key)
		}
	}
	if verdict.SchemaVersion == runstate.VerdictSchemaVersion {
		allowed := map[string]struct{}{
			"schema_version": {}, "artifact_type": {}, "run_id": {}, "status": {}, "message": {},
			"started_at": {}, "finished_at": {}, "experiment_spec": {}, "experiment_spec_digest": {},
			"experiment_identity_digest": {}, "manifest_digest": {}, "artifact_root": {}, "run_dir": {},
			"workload_exit": {}, "assert_exit": {}, "scan_exit": {},
		}
		var unknown []string
		for key := range raw {
			if _, ok := allowed[key]; !ok {
				unknown = append(unknown, key)
			}
		}
		sort.Strings(unknown)
		for _, key := range unknown {
			addIssue(result, "verdict.json unknown key for schema v1: %s", key)
		}
		for _, key := range []string{
			"schema_version",
			"artifact_type",
			"experiment_identity_digest",
			"manifest_digest",
			"artifact_root",
		} {
			if _, ok := raw[key]; !ok {
				addIssue(result, "verdict.json missing key: %s", key)
			}
		}
	}

	return verdict, true
}

func checkRequiredRegularFile(result *Result, path string, label string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			addIssue(result, "missing %s", label)
			return false
		}
		addIssue(result, "%s stat failed: %v", label, err)
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		addIssue(result, "%s must not be a symlink", label)
		return false
	}
	if !info.Mode().IsRegular() {
		addIssue(result, "%s is not a regular file", label)
		return false
	}
	if info.Size() == 0 {
		addIssue(result, "%s is empty", label)
		return false
	}
	return true
}

func checkRequiredEnv(result *Result, label string, values runartifact.Env, keys []string) {
	for _, key := range keys {
		if values.Value(key, "") == "" {
			addIssue(result, "%s missing key: %s", label, key)
		}
	}
}

func checkPresentEnvKeys(result *Result, label string, values runartifact.Env, keys []string) {
	for _, key := range keys {
		if _, ok := values[key]; !ok {
			addIssue(result, "%s missing key: %s", label, key)
		}
	}
}

func checkAllowedEnvKeys(result *Result, label string, values runartifact.Env, allowedKeys []string) {
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	var unknown []string
	for key := range values {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		addIssue(result, "%s unknown key for schema v1: %s", label, key)
	}
}

func checkSchema(result *Result, label string, value string, supported string) bool {
	if value == "" {
		return false
	}
	if value != supported {
		addIssue(result, "%s unsupported schema_version: %s", label, value)
		return false
	}
	return true
}

func checkValue(result *Result, label string, key string, value string, expected string) {
	if value != "" && value != expected {
		addIssue(result, "%s %s must be %s, got %s", label, key, expected, value)
	}
}

func checkPortablePath(result *Result, label string, key string, value string) {
	if value != "" && !evidence.IsPortablePath(value) {
		addIssue(result, "%s %s is not a portable relative path: %s", label, key, value)
	}
}

func checkDigest(result *Result, label string, key string, value string) {
	if value != "" && !evidence.IsDigest(value) {
		addIssue(result, "%s %s is not a canonical sha256 digest", label, key)
	}
}

func checkOptionalDigest(result *Result, label string, key string, value string) {
	if value != "" {
		checkDigest(result, label, key, value)
	}
}

func checkManifestIdentity(result *Result, manifest runartifact.Env) {
	digest := manifest.Value("experiment_identity_digest", "")
	checkDigest(result, "manifest.env", "experiment_identity_digest", digest)
	if digest == "" || !evidence.IsDigest(digest) {
		return
	}
	identity := runstate.ExperimentIdentity{
		SpecID:                    manifest.Value("experiment_spec_id", ""),
		Topology:                  manifest.Value("experiment_topology", "single"),
		PGConfig:                  manifest.Value("experiment_pg_config", "default"),
		Profile:                   manifest.Value("profile", ""),
		DatasetSpec:               manifest.Value("dataset_spec", ""),
		ProfileSize:               manifest.Value("profile_size", "small"),
		WorkloadSpec:              manifest.Value("workload_spec", ""),
		BackgroundSpecs:           manifest.Value("background_specs", ""),
		MetricsEnabled:            manifest.Value("metrics_enabled", "1"),
		MetricsSamples:            manifest.Value("metrics_samples", ""),
		Runtime:                   manifest.Value("runtime", "docker"),
		EngineVersion:             manifest.Value("engine_version", runstate.EngineIdentityUnverified),
		EngineCommit:              manifest.Value("engine_commit", runstate.EngineIdentityUnverified),
		PackID:                    manifest.Value("pack_id", ""),
		PackVersion:               manifest.Value("pack_version", ""),
		PackDigest:                manifest.Value("pack_digest", ""),
		SourceSpecKind:            manifest.Value("source_spec_kind", ""),
		SourceSpecID:              manifest.Value("source_spec_id", ""),
		SourceSpecRef:             manifest.Value("source_spec_ref", ""),
		SourceSpecDigest:          manifest.Value("source_spec_digest", ""),
		RuntimeFingerprintStatus:  manifest.Value("runtime_fingerprint_status", runstate.RuntimeFingerprintUnavailable),
		RuntimeFingerprintTarget:  manifest.Value("runtime_fingerprint_target", "primary"),
		RuntimeOS:                 manifest.Value("runtime_os", ""),
		RuntimeArch:               manifest.Value("runtime_arch", ""),
		PostgresServerVersionNum:  manifest.Value("postgres_server_version_num", ""),
		PostgresServerMajor:       manifest.Value("postgres_server_major", ""),
		ExecutionParametersDigest: manifest.Value("execution_parameters_digest", ""),
	}
	if expected := identity.Digest(); digest != expected {
		addIssue(result, "manifest.env experiment_identity_digest does not match resolved experiment identity")
	}
}

func checkSourceSpecIdentity(result *Result, manifest runartifact.Env) {
	values := []struct {
		key   string
		value string
	}{
		{"source_spec_kind", manifest.Value("source_spec_kind", "")},
		{"source_spec_id", manifest.Value("source_spec_id", "")},
		{"source_spec_ref", manifest.Value("source_spec_ref", "")},
		{"source_spec_digest", manifest.Value("source_spec_digest", "")},
	}
	configured := false
	for _, value := range values {
		if value.value != "" {
			configured = true
			break
		}
	}
	if !configured {
		return
	}
	for _, value := range values {
		if value.value == "" {
			addIssue(result, "manifest.env missing key for source spec identity: %s", value.key)
		}
	}

	kind := manifest.Value("source_spec_kind", "")
	if kind != "" && kind != "utility-test" && kind != "benchmark" {
		addIssue(result, "manifest.env source_spec_kind must be utility-test or benchmark, got %s", kind)
	}
	id := manifest.Value("source_spec_id", "")
	if id != "" && !validSourceSpecID(id) {
		addIssue(result, "manifest.env source_spec_id is not a canonical portable id: %s", id)
	}
	ref := manifest.Value("source_spec_ref", "")
	checkPortablePath(result, "manifest.env", "source_spec_ref", ref)
	if id != "" && validSourceSpecID(id) && ref != "" {
		root := "utility-tests"
		if kind == "benchmark" {
			root = "benchmarks"
		}
		expected := path.Join(root, id+".env")
		if ref != expected {
			addIssue(result, "manifest.env source_spec_ref must match source_spec_id: want %s, got %s", expected, ref)
		}
		if kind == "utility-test" {
			experimentID := manifest.Value("experiment_spec_id", "")
			wantExperimentID := path.Join("utility", id)
			if experimentID != wantExperimentID {
				addIssue(result, "manifest.env experiment_spec_id must match utility source identity: want %s, got %s", wantExperimentID, experimentID)
			}
		}
	}
	generatedRef := manifest.Value("experiment_spec_ref", "")
	if kind == "utility-test" && generatedRef != "" && (path.Dir(generatedRef) != ".tmp/utility-tests" || path.Ext(generatedRef) != ".env") {
		addIssue(result, "manifest.env experiment_spec_ref must identify a generated utility spec under .tmp/utility-tests: %s", generatedRef)
	}
	if kind == "utility-test" {
		for _, key := range []string{"pack_id", "pack_version", "pack_digest"} {
			if value := manifest.Value(key, ""); value != "" {
				addIssue(result, "manifest.env %s must be empty for a derived utility-test run", key)
			}
		}
	}
	checkDigest(result, "manifest.env", "source_spec_digest", manifest.Value("source_spec_digest", ""))
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

func checkEngineIdentity(result *Result, manifest runartifact.Env) {
	version := manifest.Value("engine_version", "")
	if version != "" && !runstate.IsEngineVersion(version) {
		addIssue(result, "manifest.env engine_version must be canonical SemVer or %s, got %s", runstate.EngineIdentityUnverified, version)
	}
	commit := manifest.Value("engine_commit", "")
	if commit != "" && !runstate.IsEngineCommit(commit) {
		addIssue(result, "manifest.env engine_commit must be a full lowercase Git object ID or %s, got %s", runstate.EngineIdentityUnverified, commit)
	}
}

func checkPackIdentity(result *Result, manifest runartifact.Env) {
	values := []struct {
		key   string
		value string
	}{
		{"pack_id", manifest.Value("pack_id", "")},
		{"pack_version", manifest.Value("pack_version", "")},
		{"pack_digest", manifest.Value("pack_digest", "")},
	}
	configured := false
	for _, value := range values {
		if value.value != "" {
			configured = true
			break
		}
	}
	if !configured {
		return
	}
	for _, value := range values {
		if value.value == "" {
			addIssue(result, "manifest.env missing key for versioned scenario pack identity: %s", value.key)
		}
	}
	checkDigest(result, "manifest.env", "pack_digest", manifest.Value("pack_digest", ""))
}

func checkMetricsFlag(result *Result, value string) {
	if value != "" && value != "0" && value != "1" {
		addIssue(result, "manifest.env metrics_enabled must be 0 or 1, got %s", value)
	}
}

func checkMetricsSamples(result *Result, value string) {
	if value == "" {
		return
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || strconv.Itoa(parsed) != value {
		addIssue(result, "manifest.env metrics_samples must be empty or a canonical positive integer, got %s", value)
	}
}

func checkRuntime(result *Result, value string) {
	if value != "" && value != "docker" && value != "native" {
		addIssue(result, "manifest.env runtime must be docker or native, got %s", value)
	}
}

func checkRuntimeFingerprint(result *Result, manifest runartifact.Env) {
	status := manifest.Value("runtime_fingerprint_status", "")
	target := manifest.Value("runtime_fingerprint_target", "")
	wantTarget := "primary"
	if manifest.Value("experiment_topology", "single") == "multi-version-upgrade" {
		wantTarget = "upgrade-new"
	}
	if target != wantTarget {
		addIssue(result, "manifest.env runtime_fingerprint_target must be %s for topology %s, got %s", wantTarget, manifest.Value("experiment_topology", "single"), target)
	}
	details := []struct {
		key   string
		value string
	}{
		{"runtime_os", manifest.Value("runtime_os", "")},
		{"runtime_arch", manifest.Value("runtime_arch", "")},
		{"postgres_server_version_num", manifest.Value("postgres_server_version_num", "")},
		{"postgres_server_major", manifest.Value("postgres_server_major", "")},
		{"runtime_fingerprint_observed_at", manifest.Value("runtime_fingerprint_observed_at", "")},
	}

	switch status {
	case runstate.RuntimeFingerprintUnavailable:
		for _, detail := range details {
			if detail.value != "" {
				addIssue(result, "manifest.env %s must be empty when runtime fingerprint is unavailable", detail.key)
			}
		}
	case runstate.RuntimeFingerprintObserved:
		for _, detail := range details {
			if detail.value == "" {
				addIssue(result, "manifest.env %s is required for an observed runtime fingerprint", detail.key)
			}
		}
		if value := manifest.Value("runtime_os", ""); value != "" && !isRuntimeToken(value) {
			addIssue(result, "manifest.env runtime_os is not a canonical runtime token: %s", value)
		}
		if value := manifest.Value("runtime_arch", ""); value != "" && !isRuntimeToken(value) {
			addIssue(result, "manifest.env runtime_arch is not a canonical runtime token: %s", value)
		}
		versionNum := manifest.Value("postgres_server_version_num", "")
		if versionNum != "" {
			numericVersion, err := strconv.Atoi(versionNum)
			if err != nil || numericVersion < 10000 || strconv.Itoa(numericVersion) != versionNum {
				addIssue(result, "manifest.env postgres_server_version_num is not a canonical positive integer: %s", versionNum)
			} else if major := manifest.Value("postgres_server_major", ""); major != postgresMajor(numericVersion) {
				addIssue(result, "manifest.env postgres_server_major does not match postgres_server_version_num")
			}
		}
		if observedAt := manifest.Value("runtime_fingerprint_observed_at", ""); observedAt != "" {
			if _, err := time.Parse(time.RFC3339, observedAt); err != nil {
				addIssue(result, "manifest.env runtime_fingerprint_observed_at is not RFC3339: %s", observedAt)
			}
		}
	default:
		if status != "" {
			addIssue(result, "manifest.env runtime_fingerprint_status must be unavailable or observed, got %s", status)
		}
	}
}

func isRuntimeToken(value string) bool {
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || index > 0 && (char == '_' || char == '-' || char == '.') {
			continue
		}
		return false
	}
	return value != ""
}

func postgresMajor(versionNum int) string {
	if versionNum >= 100000 {
		return strconv.Itoa(versionNum / 10000)
	}
	return fmt.Sprintf("%d.%d", versionNum/10000, (versionNum/100)%100)
}

func checkExitCode(result *Result, verdict runartifact.Env, key string) {
	value := verdict.Value(key, "")
	if value == "" {
		return
	}
	if _, err := strconv.Atoi(value); err != nil {
		addIssue(result, "verdict.env %s is not an integer: %s", key, value)
	}
}

func checkVerdictEnvOutcome(result *Result, verdict runartifact.Env) {
	status := verdict.Value("status", "")
	if status == "" {
		return
	}
	if err := runstate.ValidateVerdictStatus(status); err != nil {
		addIssue(result, "verdict.env %v", err)
		return
	}

	exitCodes := make([]int, 0, 3)
	for _, key := range []string{"workload_exit", "assert_exit", "scan_exit"} {
		value := verdict.Value(key, "")
		if value == "" {
			return
		}
		exitCode, err := strconv.Atoi(value)
		if err != nil {
			return
		}
		exitCodes = append(exitCodes, exitCode)
	}
	checkVerdictOutcome(result, "verdict.env", status, exitCodes[0], exitCodes[1], exitCodes[2])
}

func checkVerdictOutcome(result *Result, label string, status string, workloadExit int, assertExit int, scanExit int) {
	if status == "" {
		return
	}
	if err := runstate.ValidateVerdictOutcome(status, workloadExit, assertExit, scanExit); err != nil {
		addIssue(result, "%s %v", label, err)
	}
}

func checkMetrics(result *Result, path string, required bool, expectedSamples string, timestampRequired bool) *MetricsCoverage {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return nil
		}
		if os.IsNotExist(err) {
			addIssue(result, "missing metrics.csv")
		} else {
			addIssue(result, "metrics.csv stat failed: %v", err)
		}
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		addIssue(result, "metrics.csv must not be a symlink")
		return nil
	}
	if !info.Mode().IsRegular() {
		addIssue(result, "metrics.csv is not a regular file")
		return nil
	}
	if info.Size() == 0 {
		addIssue(result, "metrics.csv is empty")
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		addIssue(result, "metrics.csv read failed: %v", err)
		return nil
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		addIssue(result, "metrics.csv header read failed: %v", err)
		return nil
	}
	if !contains(header, "sampled_at") {
		addIssue(result, "metrics.csv missing sampled_at column")
		return nil
	}
	timestampIndex := 0
	for index, name := range header {
		if name == "sampled_at" {
			timestampIndex = index
			break
		}
	}

	rows := 0
	var first, last time.Time
	for {
		record, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			addIssue(result, "metrics.csv row read failed: %v", err)
			return nil
		}
		if len(record) > 0 {
			rows++
			if timestampIndex >= len(record) {
				addIssue(result, "metrics.csv row %d has no sampled_at value", rows+1)
				continue
			}
			sampledAt, parseErr := time.Parse(time.RFC3339Nano, record[timestampIndex])
			if parseErr != nil || !strings.HasSuffix(record[timestampIndex], "Z") {
				if timestampRequired {
					addIssue(result, "metrics.csv row %d sampled_at is not UTC RFC3339", rows+1)
				}
				continue
			}
			sampledAt = sampledAt.UTC()
			if !last.IsZero() && sampledAt.Before(last) {
				addIssue(result, "metrics.csv sampled_at values are not monotonic")
			}
			if first.IsZero() {
				first = sampledAt
			}
			last = sampledAt
		}
	}
	if rows == 0 {
		addIssue(result, "metrics.csv has no samples")
	}
	if expectedSamples != "" {
		expected, err := strconv.Atoi(expectedSamples)
		if err == nil && expected > 0 && strconv.Itoa(expected) == expectedSamples && rows != expected {
			addIssue(result, "metrics.csv sample count does not match manifest.env metrics_samples: got %d, want %d", rows, expected)
		}
	}
	if rows == 0 || first.IsZero() || last.IsZero() {
		return nil
	}
	return &MetricsCoverage{Samples: rows, First: first.Format(time.RFC3339Nano), Last: last.Format(time.RFC3339Nano)}
}

func checkVerdictJSONKeys(result *Result, verdict runstate.Verdict) {
	if verdict.RunID == "" {
		addIssue(result, "verdict.json run_id is empty")
	}
	if verdict.Status == "" {
		addIssue(result, "verdict.json status is empty")
	}
	if verdict.Message == "" {
		addIssue(result, "verdict.json message is empty")
	}
	if verdict.StartedAt == "" {
		addIssue(result, "verdict.json started_at is empty")
	}
	if verdict.FinishedAt == "" {
		addIssue(result, "verdict.json finished_at is empty")
	}
	if verdict.ExperimentSpecID == "" {
		addIssue(result, "verdict.json experiment_spec is empty")
	}
	if verdict.RunDir == "" {
		addIssue(result, "verdict.json run_dir is empty")
	}
}

func checkVerdictJSONV1(result *Result, verdict runstate.Verdict) {
	checkValue(result, "verdict.json", "artifact_type", verdict.ArtifactType, runstate.VerdictArtifactType)
	checkValue(result, "verdict.json", "artifact_root", verdict.ArtifactRoot, ".")
	checkOptionalDigest(result, "verdict.json", "experiment_spec_digest", verdict.ExperimentSpecDigest)
	checkDigest(result, "verdict.json", "experiment_identity_digest", verdict.ExperimentIdentityDigest)
	checkDigest(result, "verdict.json", "manifest_digest", verdict.ManifestDigest)
	if verdict.ArtifactType == "" {
		addIssue(result, "verdict.json artifact_type is empty")
	}
	if verdict.ExperimentIdentityDigest == "" {
		addIssue(result, "verdict.json experiment_identity_digest is empty")
	}
	if verdict.ManifestDigest == "" {
		addIssue(result, "verdict.json manifest_digest is empty")
	}
	if verdict.ArtifactRoot == "" {
		addIssue(result, "verdict.json artifact_root is empty")
	}
}

func checkVerdictConsistency(result *Result, manifest runartifact.Env, verdict runartifact.Env, verdictJSON runstate.Verdict) {
	checkStringMatch(result, "verdict.json", "run_id", verdictJSON.RunID, "manifest.env", "run_id", manifest.Value("run_id", ""))
	checkStringMatch(result, "verdict.json", "started_at", verdictJSON.StartedAt, "manifest.env", "started_at", manifest.Value("started_at", ""))
	checkStringMatch(result, "verdict.json", "experiment_spec", verdictJSON.ExperimentSpecID, "manifest.env", "experiment_spec_id", manifest.Value("experiment_spec_id", ""))
	checkStringMatch(result, "verdict.json", "status", verdictJSON.Status, "verdict.env", "status", verdict.Value("status", ""))
	checkStringMatch(result, "verdict.json", "message", verdictJSON.Message, "verdict.env", "message", verdict.Value("message", ""))
	checkStringMatch(result, "verdict.json", "finished_at", verdictJSON.FinishedAt, "verdict.env", "finished_at", verdict.Value("finished_at", ""))
	checkStringMatch(result, "verdict.json", "schema_version", verdictJSON.SchemaVersion, "verdict.env", "schema_version", verdict.Value("schema_version", ""))
	checkStringMatch(result, "verdict.json", "artifact_type", verdictJSON.ArtifactType, "verdict.env", "artifact_type", verdict.Value("artifact_type", ""))
	checkStringMatch(result, "verdict.json", "run_id", verdictJSON.RunID, "verdict.env", "run_id", verdict.Value("run_id", ""))
	checkStringMatch(result, "verdict.json", "started_at", verdictJSON.StartedAt, "verdict.env", "started_at", verdict.Value("started_at", ""))
	checkStringMatch(result, "verdict.json", "experiment_spec", verdictJSON.ExperimentSpecID, "verdict.env", "experiment_spec_id", verdict.Value("experiment_spec_id", ""))
	checkStringMatch(result, "verdict.json", "experiment_spec_digest", verdictJSON.ExperimentSpecDigest, "verdict.env", "experiment_spec_digest", verdict.Value("experiment_spec_digest", ""))
	checkStringMatch(result, "verdict.json", "experiment_identity_digest", verdictJSON.ExperimentIdentityDigest, "verdict.env", "experiment_identity_digest", verdict.Value("experiment_identity_digest", ""))
	checkStringMatch(result, "verdict.json", "manifest_digest", verdictJSON.ManifestDigest, "verdict.env", "manifest_digest", verdict.Value("manifest_digest", ""))
	checkStringMatch(result, "verdict.json", "artifact_root", verdictJSON.ArtifactRoot, "verdict.env", "artifact_root", verdict.Value("artifact_root", ""))
	checkStringMatch(result, "manifest.env", "run_id", manifest.Value("run_id", ""), "verdict.env", "run_id", verdict.Value("run_id", ""))
	checkStringMatch(result, "manifest.env", "started_at", manifest.Value("started_at", ""), "verdict.env", "started_at", verdict.Value("started_at", ""))
	checkStringMatch(result, "manifest.env", "experiment_spec_id", manifest.Value("experiment_spec_id", ""), "verdict.env", "experiment_spec_id", verdict.Value("experiment_spec_id", ""))
	checkStringMatch(result, "manifest.env", "experiment_spec_digest", manifest.Value("experiment_spec_digest", ""), "verdict.env", "experiment_spec_digest", verdict.Value("experiment_spec_digest", ""))
	checkStringMatch(result, "manifest.env", "experiment_identity_digest", manifest.Value("experiment_identity_digest", ""), "verdict.env", "experiment_identity_digest", verdict.Value("experiment_identity_digest", ""))
	checkIntMatch(result, "workload_exit", verdictJSON.WorkloadExit, verdict.Value("workload_exit", ""))
	checkIntMatch(result, "assert_exit", verdictJSON.AssertExit, verdict.Value("assert_exit", ""))
	checkIntMatch(result, "scan_exit", verdictJSON.ScanExit, verdict.Value("scan_exit", ""))
}

func checkSchemaCohesion(result *Result, manifestSchema string, verdictEnvSchema string, verdictJSONSchema string) {
	values := []struct {
		label string
		value string
	}{
		{"manifest.env", manifestSchema},
		{"verdict.env", verdictEnvSchema},
		{"verdict.json", verdictJSONSchema},
	}
	versioned := false
	for _, value := range values {
		if value.value != "" {
			versioned = true
			break
		}
	}
	if !versioned {
		return
	}
	for _, value := range values {
		if value.value == "" {
			addIssue(result, "%s missing schema_version in versioned artifact", value.label)
		}
	}
}

func checkRecordedRunDirConsistency(result *Result, manifestDir string, verdictEnvDir string, verdictJSONDir string) {
	values := []struct {
		label string
		value string
	}{
		{"manifest.env", manifestDir},
		{"verdict.env", verdictEnvDir},
		{"verdict.json", verdictJSONDir},
	}
	var reference string
	var referenceLabel string
	for _, value := range values {
		if value.value == "" {
			continue
		}
		cleaned := filepath.Clean(value.value)
		if reference == "" {
			reference = cleaned
			referenceLabel = value.label
			continue
		}
		if cleaned != reference {
			addIssue(result, "%s run_dir does not match %s run_dir", value.label, referenceLabel)
		}
	}
}

func checkManifestBindings(result *Result, manifestPath string, manifest runartifact.Env, verdict runartifact.Env, verdictJSON runstate.Verdict) {
	actualDigest, err := evidence.DigestFile(manifestPath)
	if err != nil {
		addIssue(result, "manifest.env digest failed: %v", err)
		return
	}
	for _, binding := range []struct {
		label string
		value string
	}{
		{"verdict.env", verdict.Value("manifest_digest", "")},
		{"verdict.json", verdictJSON.ManifestDigest},
	} {
		if binding.value != "" && binding.value != actualDigest {
			addIssue(result, "%s manifest_digest does not match manifest.env", binding.label)
		}
	}
}

func checkStringMatch(result *Result, leftLabel string, leftKey string, leftValue string, rightLabel string, rightKey string, rightValue string) {
	if leftValue == "" || rightValue == "" {
		return
	}
	if leftValue != rightValue {
		addIssue(result, "%s %s does not match %s %s", leftLabel, leftKey, rightLabel, rightKey)
	}
}

func checkIntMatch(result *Result, key string, jsonValue int, envValue string) {
	if envValue == "" {
		return
	}
	parsed, err := strconv.Atoi(envValue)
	if err != nil {
		return
	}
	if jsonValue != parsed {
		addIssue(result, "verdict.json %s does not match verdict.env %s", key, key)
	}
}

func checkBundleInventory(result *Result, dir string, manifest runartifact.Env, required bool) {
	inventoryPath := filepath.Join(dir, evidence.BundleInventoryName)
	info, err := os.Lstat(inventoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			if required {
				addIssue(result, "missing %s: bundle verification requires a complete inventory", evidence.BundleInventoryName)
			}
			return
		}
		addIssue(result, "%s stat failed: %v", evidence.BundleInventoryName, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		addIssue(result, "%s must not be a symlink", evidence.BundleInventoryName)
		return
	}
	if !info.Mode().IsRegular() {
		addIssue(result, "%s is not a regular file", evidence.BundleInventoryName)
		return
	}
	if info.Size() == 0 {
		addIssue(result, "%s is empty", evidence.BundleInventoryName)
		return
	}
	content, err := os.ReadFile(inventoryPath)
	if err != nil {
		addIssue(result, "%s read failed: %v", evidence.BundleInventoryName, err)
		return
	}
	inventory, err := evidence.ParseBundleInventory(content)
	if err != nil {
		addIssue(result, "%s parse failed: %v", evidence.BundleInventoryName, err)
		return
	}
	if inventory.SchemaVersion != evidence.BundleInventorySchema {
		addIssue(result, "%s unsupported schema_version: %s", evidence.BundleInventoryName, inventory.SchemaVersion)
	}
	if inventory.ArtifactType != evidence.BundleInventoryArtifactType {
		addIssue(result, "%s artifact_type must be %s, got %s", evidence.BundleInventoryName, evidence.BundleInventoryArtifactType, inventory.ArtifactType)
	}
	if inventory.RunID == "" {
		addIssue(result, "%s run_id is empty", evidence.BundleInventoryName)
	}
	if len(inventory.Files) == 0 {
		addIssue(result, "%s files are empty", evidence.BundleInventoryName)
	}
	if runID := manifest.Value("run_id", ""); runID != "" && inventory.RunID != runID {
		addIssue(result, "%s run_id does not match manifest.env run_id", evidence.BundleInventoryName)
	}

	actual := make(map[string]os.FileInfo)
	err = filepath.WalkDir(dir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, filePath)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == evidence.BundleInventoryName {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			addIssue(result, "%s contains unsupported non-regular path: %s", evidence.BundleInventoryName, rel)
			return nil
		}
		actual[rel] = info
		return nil
	})
	if err != nil {
		addIssue(result, "%s filesystem scan failed: %v", evidence.BundleInventoryName, err)
		return
	}

	listed := make(map[string]struct{})
	previous := ""
	for index, file := range inventory.Files {
		if !evidence.IsPortablePath(file.Path) {
			addIssue(result, "%s file %d has non-portable path: %s", evidence.BundleInventoryName, index+1, file.Path)
			continue
		}
		if previous != "" && file.Path <= previous {
			addIssue(result, "%s files are not strictly sorted by path", evidence.BundleInventoryName)
		}
		previous = file.Path
		if _, ok := listed[file.Path]; ok {
			addIssue(result, "%s contains duplicate path: %s", evidence.BundleInventoryName, file.Path)
			continue
		}
		listed[file.Path] = struct{}{}
		checkDigest(result, evidence.BundleInventoryName, file.Path+" digest", file.Digest)
		info, ok := actual[file.Path]
		if !ok {
			addIssue(result, "%s missing inventoried file: %s", evidence.BundleInventoryName, file.Path)
			continue
		}
		if info.Size() != file.Size {
			addIssue(result, "%s size mismatch for %s", evidence.BundleInventoryName, file.Path)
		}
		digest, digestErr := evidence.DigestFile(filepath.Join(dir, filepath.FromSlash(file.Path)))
		if digestErr != nil {
			addIssue(result, "%s digest failed for %s: %v", evidence.BundleInventoryName, file.Path, digestErr)
			continue
		}
		if digest != file.Digest {
			addIssue(result, "%s digest mismatch for %s", evidence.BundleInventoryName, file.Path)
		}
	}

	var actualPaths []string
	for filePath := range actual {
		actualPaths = append(actualPaths, filePath)
	}
	sort.Strings(actualPaths)
	for _, filePath := range actualPaths {
		if _, ok := listed[filePath]; !ok {
			addIssue(result, "%s missing inventory entry for %s", evidence.BundleInventoryName, filePath)
		}
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func addIssue(result *Result, format string, args ...interface{}) {
	result.Issues = append(result.Issues, fmt.Sprintf(format, args...))
}
