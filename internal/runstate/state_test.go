package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
)

func TestWriteManifest(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-a")
	specPath := filepath.Join(root, "experiments", "smoke.env")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specPath, []byte("EXPERIMENT_NAME=smoke\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"RUN_ID":                                      "run-a",
		"STARTED_AT":                                  "2026-01-01T00:00:00Z",
		"REPO_DIR":                                    root,
		"EXPERIMENT_SPEC_FILE":                        specPath,
		"EXPERIMENT_SPEC_ID":                          "smoke",
		"EXPERIMENT_NAME":                             "smoke experiment",
		"EXPERIMENT_TOPOLOGY":                         "single",
		"EXPERIMENT_PG_CONFIG":                        "default",
		"EXPERIMENT_PROFILE":                          "smoke",
		"EXPERIMENT_PROFILE_SIZE":                     "small",
		"EXPERIMENT_WORKLOAD_SPEC":                    "sql/smoke-run",
		"EXPERIMENT_METRICS_SAMPLES":                  "2",
		"PGWORKBENCH_RUNTIME":                         "native",
		"PGWORKBENCH_ENGINE_VERSION":                  "0.2.0-rc.1",
		"PGWORKBENCH_ENGINE_COMMIT":                   "0123456789abcdef0123456789abcdef01234567",
		"PGWORKBENCH_PACK_ID":                         "builtin",
		"PGWORKBENCH_PACK_VERSION":                    "0.2.0",
		"PGWORKBENCH_PACK_DIGEST":                     "sha256:3af170c611171b745da544efb90d388db6498664c71ea45a31f41c6e64dbaed1",
		"PGWORKBENCH_RUNTIME_FINGERPRINT_STATUS":      RuntimeFingerprintObserved,
		"PGWORKBENCH_RUNTIME_OS":                      "linux",
		"PGWORKBENCH_RUNTIME_ARCH":                    "amd64",
		"PGWORKBENCH_POSTGRES_SERVER_VERSION_NUM":     "160004",
		"PGWORKBENCH_POSTGRES_SERVER_MAJOR":           "16",
		"PGWORKBENCH_RUNTIME_FINGERPRINT_OBSERVED_AT": "2026-01-01T00:00:01Z",
		"RUN_DIR": runDir,
	}

	if err := WriteManifest(runDir, ManifestFromEnv(mapGetter(env))); err != nil {
		t.Fatal(err)
	}
	manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{
		"schema_version":                  ManifestSchemaVersion,
		"artifact_type":                   ManifestArtifactType,
		"run_id":                          "run-a",
		"experiment_spec_id":              "smoke",
		"experiment_spec_ref":             "experiments/smoke.env",
		"workload_spec":                   "sql/smoke-run",
		"runtime":                         "native",
		"engine_version":                  "0.2.0-rc.1",
		"engine_commit":                   "0123456789abcdef0123456789abcdef01234567",
		"pack_id":                         "builtin",
		"pack_version":                    "0.2.0",
		"runtime_fingerprint_status":      RuntimeFingerprintObserved,
		"runtime_fingerprint_target":      "primary",
		"runtime_os":                      "linux",
		"runtime_arch":                    "amd64",
		"postgres_server_version_num":     "160004",
		"postgres_server_major":           "16",
		"runtime_fingerprint_observed_at": "2026-01-01T00:00:01Z",
		"metrics_enabled":                 "1",
		"metrics_samples":                 "2",
		"artifact_root":                   ".",
	}
	for key, want := range wants {
		if got := manifest[key]; got != want {
			t.Fatalf("manifest[%q] = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"experiment_spec_digest", "experiment_identity_digest"} {
		if !strings.HasPrefix(manifest[key], "sha256:") {
			t.Fatalf("manifest[%q] = %q, want sha256 digest", key, manifest[key])
		}
	}
	if got := ManifestFromEnv(mapGetter(env)).Identity().MetricsSamples; got != "2" {
		t.Fatalf("identity metrics_samples = %q, want 2", got)
	}
	for _, key := range []string{"source_spec_kind", "source_spec_id", "source_spec_ref", "source_spec_digest"} {
		if got, ok := manifest[key]; !ok || got != "" {
			t.Fatalf("manifest[%q] = %q (present=%v), want present empty field", key, got, ok)
		}
	}
	if _, ok := manifest["runtime_ports_digest"]; ok {
		t.Fatal("run manifest v1 serialized the v2-only runtime_ports_digest field")
	}
}

func TestExecutionParametersDigestIncludesPgbenchProtocol(t *testing.T) {
	base := map[string]string{
		"EXPERIMENT_WORKLOAD_SPEC": "pgbench/read-only",
		"PGBENCH_SCALE":            "10",
		"PGBENCH_CLIENTS":          "4",
		"PGBENCH_THREADS":          "2",
		"PGBENCH_TIME":             "30",
		"PGBENCH_WARMUP_TIME":      "10",
	}
	first := ManifestFromEnv(mapGetter(base)).ExecutionParametersDigest
	changed := make(map[string]string, len(base))
	for key, value := range base {
		changed[key] = value
	}
	changed["PGBENCH_CLIENTS"] = "64"
	second := ManifestFromEnv(mapGetter(changed)).ExecutionParametersDigest
	if first == second {
		t.Fatalf("pgbench client change did not change execution parameter digest: %s", first)
	}
	changed = make(map[string]string, len(base)+1)
	for key, value := range base {
		changed[key] = value
	}
	changed["PGBENCH_CONNECT_PER_TRANSACTION"] = "1"
	if connected := ManifestFromEnv(mapGetter(changed)).ExecutionParametersDigest; first == connected {
		t.Fatalf("pgbench connect-per-transaction change did not change execution parameter digest: %s", first)
	}
}

func TestRuntimePortsDigestBindsIdentityWithoutChangingLegacyExecutionDigest(t *testing.T) {
	base := map[string]string{
		"EXPERIMENT_SPEC_ID":      "bulk",
		"PGWORKBENCH_RUNTIME":     "docker",
		"PGWORKBENCH_PACK_DIGEST": "sha256:" + strings.Repeat("a", 64),
	}
	legacy := ManifestFromEnv(mapGetter(base))
	withPorts := make(map[string]string, len(base)+1)
	for key, value := range base {
		withPorts[key] = value
	}
	withPorts["PGWORKBENCH_RUNTIME_PORTS_DIGEST"] = "sha256:" + strings.Repeat("b", 64)
	bound := ManifestFromEnv(mapGetter(withPorts))
	if legacy.SchemaVersion != ManifestSchemaVersion || bound.SchemaVersion != ManifestSchemaVersionV2 {
		t.Fatalf("manifest schema selection = %q/%q, want v1/v2", legacy.SchemaVersion, bound.SchemaVersion)
	}
	if bound.ExecutionParametersDigest != legacy.ExecutionParametersDigest {
		t.Fatalf("optional runtime port binding changed legacy execution digest: %s != %s", bound.ExecutionParametersDigest, legacy.ExecutionParametersDigest)
	}
	if bound.RuntimePortsDigest != withPorts["PGWORKBENCH_RUNTIME_PORTS_DIGEST"] {
		t.Fatalf("runtime ports digest = %q", bound.RuntimePortsDigest)
	}
	if bound.Identity().Digest() == legacy.Identity().Digest() {
		t.Fatal("runtime port binding did not change experiment identity")
	}
	invalid := make(map[string]string, len(base)+1)
	for key, value := range base {
		invalid[key] = value
	}
	invalid["PGWORKBENCH_RUNTIME_PORTS_DIGEST"] = "hostile-not-a-digest"
	invalidManifest := ManifestFromEnv(mapGetter(invalid))
	if invalidManifest.SchemaVersion != ManifestSchemaVersionV2 {
		t.Fatalf("non-empty invalid runtime port digest silently selected %q", invalidManifest.SchemaVersion)
	}
	if err := WriteManifest(t.TempDir(), invalidManifest); err == nil {
		t.Fatal("non-empty invalid runtime port digest silently downgraded to v1")
	}
}

func TestWriteManifestRejectsSchemaAndRuntimePortDigestConfusion(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	for _, test := range []struct {
		name     string
		manifest Manifest
	}{
		{name: "v1-with-v2-field", manifest: Manifest{SchemaVersion: ManifestSchemaVersion, RuntimePortsDigest: digest}},
		{name: "v2-missing-field", manifest: Manifest{SchemaVersion: ManifestSchemaVersionV2}},
		{name: "v2-invalid-field", manifest: Manifest{SchemaVersion: ManifestSchemaVersionV2, RuntimePortsDigest: "sha256:not-canonical"}},
		{name: "unsupported", manifest: Manifest{SchemaVersion: "pgworkbench.run-manifest/v3"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := WriteManifest(t.TempDir(), test.manifest); err == nil {
				t.Fatal("WriteManifest accepted a confused schema/runtime-port binding")
			}
		})
	}
}

func TestManifestRecordsUtilitySourceSpecIdentity(t *testing.T) {
	runDir := t.TempDir()
	rawDigest := strings.Repeat("b", 64)
	env := map[string]string{
		"RUN_ID":                         "utility-run",
		"EXPERIMENT_SPEC_ID":             "utility/pg-dump/smoke",
		"EXPERIMENT_SPEC_REF":            ".tmp/utility-tests/utility-run.env",
		"PGWORKBENCH_SOURCE_SPEC_KIND":   "utility-test",
		"PGWORKBENCH_SOURCE_SPEC_ID":     "pg-dump/smoke",
		"PGWORKBENCH_SOURCE_SPEC_REF":    "utility-tests/pg-dump/smoke.env",
		"PGWORKBENCH_SOURCE_SPEC_DIGEST": rawDigest,
		"RUN_DIR":                        runDir,
	}
	manifest := ManifestFromEnv(mapGetter(env))
	if manifest.SourceSpecDigest != "sha256:"+rawDigest {
		t.Fatalf("source spec digest = %q", manifest.SourceSpecDigest)
	}
	if err := WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	values, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"source_spec_kind":   "utility-test",
		"source_spec_id":     "pg-dump/smoke",
		"source_spec_ref":    "utility-tests/pg-dump/smoke.env",
		"source_spec_digest": "sha256:" + rawDigest,
	} {
		if got := values[key]; got != want {
			t.Fatalf("manifest[%q] = %q, want %q", key, got, want)
		}
	}
	if manifest.Identity().SourceSpecDigest != "sha256:"+rawDigest {
		t.Fatalf("identity source digest = %q", manifest.Identity().SourceSpecDigest)
	}
}

func TestWriteManifestUsesOutputDirWhenEnvRunDirIsMissing(t *testing.T) {
	runDir := t.TempDir()
	env := map[string]string{
		"RUN_ID":             "run-a",
		"EXPERIMENT_SPEC_ID": "smoke",
	}

	if err := WriteManifest(runDir, ManifestFromEnv(mapGetter(env))); err != nil {
		t.Fatal(err)
	}
	manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest["run_dir"]; got != "." {
		t.Fatalf("manifest run_dir = %q, want portable root", got)
	}
}

func TestWriteVerdict(t *testing.T) {
	runDir := t.TempDir()
	env := map[string]string{
		"RUN_ID":             "run-a",
		"STARTED_AT":         "2026-01-01T00:00:00Z",
		"EXPERIMENT_SPEC_ID": "smoke",
		"RUN_DIR":            runDir,
		"WORKLOAD_EXIT":      "0",
		"ASSERT_EXIT":        "0",
		"SCAN_EXIT":          "0",
	}
	if err := WriteManifest(runDir, Manifest{
		RunID:                    "run-a",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpecID:         "smoke",
		ExperimentTopology:       "single",
		ExperimentPGConfig:       "default",
		ProfileSize:              "small",
		MetricsEnabled:           "0",
		RuntimeFingerprintStatus: RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-01-01T00:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}

	verdict := VerdictFromEnv(mapGetter(env), "passed", "experiment passed", "2026-01-01T00:00:02Z")
	if err := WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}

	verdictEnv, err := envfile.Parse(filepath.Join(runDir, "verdict.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := verdictEnv["status"]; got != "passed" {
		t.Fatalf("verdict_env status=%q, want passed", got)
	}
	if got := verdictEnv["message"]; got != "experiment passed" {
		t.Fatalf("verdict_env message=%q, want 'experiment passed'", got)
	}
	for key, want := range map[string]string{
		"schema_version": VerdictSchemaVersion,
		"artifact_type":  VerdictArtifactType,
		"run_id":         "run-a",
		"artifact_root":  ".",
	} {
		if got := verdictEnv[key]; got != want {
			t.Fatalf("verdict_env %s=%q, want %q", key, got, want)
		}
	}
	if !strings.HasPrefix(verdictEnv["manifest_digest"], "sha256:") {
		t.Fatalf("manifest digest = %q", verdictEnv["manifest_digest"])
	}
	jsonContent := readFile(t, filepath.Join(runDir, "verdict.json"))
	for _, want := range []string{`"schema_version": "` + VerdictSchemaVersion + `"`, `"status": "passed"`, `"experiment_spec": "smoke"`, `"manifest_digest": "sha256:`, `"scan_exit": 0`} {
		if !strings.Contains(jsonContent, want) {
			t.Fatalf("verdict.json missing %q:\n%s", want, jsonContent)
		}
	}
}

func TestManifestIdentityDigestChangesWithResolvedInputs(t *testing.T) {
	base := Manifest{
		ExperimentSpecID:   "smoke",
		ExperimentTopology: "single",
		ExperimentPGConfig: "default",
		Profile:            "smoke",
		ProfileSize:        "small",
		WorkloadSpec:       "sql/smoke-run",
		MetricsEnabled:     "1",
	}
	changed := base
	changed.ExperimentPGConfig = "conservative"
	if base.Identity().Digest() == changed.Identity().Digest() {
		t.Fatal("identity digest did not change with pg config")
	}
	if base.Identity().Digest() != base.Identity().Digest() {
		t.Fatal("identity digest is not deterministic")
	}
	fingerprintChanged := base
	fingerprintChanged.RuntimeFingerprintStatus = RuntimeFingerprintObserved
	fingerprintChanged.RuntimeOS = "linux"
	fingerprintChanged.RuntimeArch = "amd64"
	fingerprintChanged.PostgresServerVersionNum = "160004"
	fingerprintChanged.PostgresServerMajor = "16"
	if base.Identity().Digest() == fingerprintChanged.Identity().Digest() {
		t.Fatal("identity digest did not change with observed runtime fingerprint")
	}
	engineChanged := base
	engineChanged.EngineVersion = "0.2.0"
	engineChanged.EngineCommit = "0123456789abcdef0123456789abcdef01234567"
	if base.Identity().Digest() == engineChanged.Identity().Digest() {
		t.Fatal("identity digest did not change with engine candidate identity")
	}
	sourceChanged := base
	sourceChanged.SourceSpecKind = "utility-test"
	sourceChanged.SourceSpecID = "pg-dump/smoke"
	sourceChanged.SourceSpecRef = "utility-tests/pg-dump/smoke.env"
	sourceChanged.SourceSpecDigest = "sha256:" + strings.Repeat("a", 64)
	if base.Identity().Digest() == sourceChanged.Identity().Digest() {
		t.Fatal("identity digest did not change with source utility-test identity")
	}
}

func TestEngineIdentityNormalization(t *testing.T) {
	fullCommit := "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
	fullSHA256Commit := strings.Repeat("a1", 32)
	if got := NormalizeEngineVersion(" 0.2.0-rc.1+build.7 "); got != "0.2.0-rc.1+build.7" {
		t.Fatalf("normalized engine version = %q", got)
	}
	if got := NormalizeEngineCommit(fullCommit); got != strings.ToLower(fullCommit) {
		t.Fatalf("normalized engine commit = %q", got)
	}
	if got := NormalizeEngineCommit(fullSHA256Commit); got != fullSHA256Commit {
		t.Fatalf("normalized SHA-256 engine commit = %q", got)
	}
	for _, value := range []string{"", "dev", "0.2", "01.2.3", "1.2.3-01"} {
		if got := NormalizeEngineVersion(value); got != EngineIdentityUnverified {
			t.Errorf("NormalizeEngineVersion(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"", "unknown", "0123456", "dirty"} {
		if got := NormalizeEngineCommit(value); got != EngineIdentityUnverified {
			t.Errorf("NormalizeEngineCommit(%q) = %q", value, got)
		}
	}

	manifest := ManifestFromEnv(mapGetter(map[string]string{"EXPERIMENT_SPEC_ID": "smoke"}))
	if manifest.EngineVersion != EngineIdentityUnverified || manifest.EngineCommit != EngineIdentityUnverified {
		t.Fatalf("default engine identity = %q/%q", manifest.EngineVersion, manifest.EngineCommit)
	}
}

func TestManifestFromEnvRecordsDisabledMetrics(t *testing.T) {
	rawSpecDigest := strings.Repeat("a", 64)
	manifest := ManifestFromEnv(mapGetter(map[string]string{
		"EXPERIMENT_SPEC_ID":     "pg-source-plan",
		"EXPERIMENT_SPEC_SHA256": rawSpecDigest,
		"EXPERIMENT_METRICS":     "0",
		"PGWORKBENCH_RUNTIME":    "native",
	}))
	if manifest.MetricsEnabled != "0" {
		t.Fatalf("metrics enabled = %q, want 0", manifest.MetricsEnabled)
	}
	if manifest.ExperimentSpecRef != "experiments/pg-source-plan.env" {
		t.Fatalf("spec ref = %q", manifest.ExperimentSpecRef)
	}
	if manifest.ExperimentSpecDigest != "sha256:"+rawSpecDigest {
		t.Fatalf("spec digest = %q", manifest.ExperimentSpecDigest)
	}
	if manifest.Runtime != "native" {
		t.Fatalf("runtime = %q", manifest.Runtime)
	}
	if manifest.RuntimeFingerprintStatus != RuntimeFingerprintUnavailable {
		t.Fatalf("runtime fingerprint status = %q", manifest.RuntimeFingerprintStatus)
	}
	if manifest.RuntimeFingerprintTarget != "primary" {
		t.Fatalf("runtime fingerprint target = %q, want primary", manifest.RuntimeFingerprintTarget)
	}
}

func TestManifestDefaultsMultiVersionFingerprintToUpgradeTarget(t *testing.T) {
	manifest := ManifestFromEnv(mapGetter(map[string]string{
		"EXPERIMENT_SPEC_ID":  "multi-version-upgrade-smoke",
		"EXPERIMENT_TOPOLOGY": "multi-version-upgrade",
	}))
	if manifest.RuntimeFingerprintTarget != "upgrade-new" {
		t.Fatalf("runtime fingerprint target = %q, want upgrade-new", manifest.RuntimeFingerprintTarget)
	}
	if manifest.Identity().RuntimeFingerprintTarget != "upgrade-new" {
		t.Fatalf("identity runtime fingerprint target = %q, want upgrade-new", manifest.Identity().RuntimeFingerprintTarget)
	}
}

func TestWriteVerdictRejectsPassedRunWithoutObservedRuntimeFingerprint(t *testing.T) {
	runDir := t.TempDir()
	if err := WriteManifest(runDir, Manifest{
		RunID:                    "run-a",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpecID:         "smoke",
		MetricsEnabled:           "0",
		RuntimeFingerprintStatus: RuntimeFingerprintUnavailable,
	}); err != nil {
		t.Fatal(err)
	}
	verdict := Verdict{
		RunID:            "run-a",
		Status:           "passed",
		Message:          "experiment passed",
		StartedAt:        "2026-01-01T00:00:00Z",
		FinishedAt:       "2026-01-01T00:00:02Z",
		ExperimentSpecID: "smoke",
	}
	if err := WriteVerdict(runDir, verdict); err == nil || !strings.Contains(err.Error(), "observed runtime fingerprint") {
		t.Fatalf("expected passed fingerprint error, got %v", err)
	}
}

func TestWriteVerdictRequiresManifestBinding(t *testing.T) {
	runDir := t.TempDir()
	verdict := Verdict{
		RunID:            "run-a",
		Status:           "failed",
		Message:          "setup failed",
		StartedAt:        "2026-01-01T00:00:00Z",
		FinishedAt:       "2026-01-01T00:00:02Z",
		ExperimentSpecID: "smoke",
		WorkloadExit:     1,
	}
	err := WriteVerdict(runDir, verdict)
	if err == nil || !strings.Contains(err.Error(), "manifest.env") {
		t.Fatalf("expected missing manifest binding error, got %v", err)
	}
	for _, name := range []string{"verdict.env", "verdict.json"} {
		if _, statErr := os.Lstat(filepath.Join(runDir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("unbound verdict file was published: %s (stat=%v)", name, statErr)
		}
	}
}

func TestValidateVerdictOutcome(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		workloadExit int
		assertExit   int
		scanExit     int
		wantError    string
	}{
		{name: "passed", status: VerdictStatusPassed},
		{name: "failed without recorded stage", status: VerdictStatusFailed, wantError: "failed verdict requires at least one nonzero exit code"},
		{name: "failed workload", status: VerdictStatusFailed, workloadExit: 7},
		{name: "unknown status", status: "inconclusive", wantError: `verdict status must be passed or failed, got "inconclusive"`},
		{name: "passed workload failure", status: VerdictStatusPassed, workloadExit: 7, wantError: "passed verdict requires zero exit codes: workload_exit=7 assert_exit=0 scan_exit=0"},
		{name: "passed assertion failure", status: VerdictStatusPassed, assertExit: 2, wantError: "passed verdict requires zero exit codes: workload_exit=0 assert_exit=2 scan_exit=0"},
		{name: "passed scan failure", status: VerdictStatusPassed, scanExit: 1, wantError: "passed verdict requires zero exit codes: workload_exit=0 assert_exit=0 scan_exit=1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateVerdictOutcome(test.status, test.workloadExit, test.assertExit, test.scanExit)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestWriteVerdictRejectsInvalidOutcomeBeforeWritingFiles(t *testing.T) {
	for _, verdict := range []Verdict{
		{Status: "inconclusive"},
		{Status: VerdictStatusPassed, WorkloadExit: 7},
		{Status: VerdictStatusFailed},
	} {
		runDir := filepath.Join(t.TempDir(), "run")
		if err := WriteVerdict(runDir, verdict); err == nil {
			t.Fatalf("WriteVerdict(%#v) unexpectedly succeeded", verdict)
		}
		if _, err := os.Stat(runDir); !os.IsNotExist(err) {
			t.Fatalf("invalid verdict created run directory: %v", err)
		}
	}
}

func TestWriteVerdictUsesOutputDirWhenEnvRunDirIsMissing(t *testing.T) {
	runDir := t.TempDir()
	env := map[string]string{
		"RUN_ID":             "run-a",
		"STARTED_AT":         "2026-01-01T00:00:00Z",
		"EXPERIMENT_SPEC_ID": "smoke",
	}
	if err := WriteManifest(runDir, Manifest{
		RunID:                    "run-a",
		StartedAt:                "2026-01-01T00:00:00Z",
		ExperimentSpecID:         "smoke",
		MetricsEnabled:           "0",
		RuntimeFingerprintStatus: RuntimeFingerprintObserved,
		RuntimeOS:                "linux",
		RuntimeArch:              "amd64",
		PostgresServerVersionNum: "160004",
		PostgresServerMajor:      "16",
		RuntimeFingerprintAt:     "2026-01-01T00:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}

	verdict := VerdictFromEnv(mapGetter(env), "passed", "experiment passed", "2026-01-01T00:00:02Z")
	if err := WriteVerdict(runDir, verdict); err != nil {
		t.Fatal(err)
	}
	jsonContent := readFile(t, filepath.Join(runDir, "verdict.json"))
	if !strings.Contains(jsonContent, `"run_dir": "."`) {
		t.Fatalf("verdict did not use output dir:\n%s", jsonContent)
	}
}

func mapGetter(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
