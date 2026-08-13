package schemavalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositorySchemaGate(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	registry, err := CompileDir(filepath.Join(repoRoot, "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(registry.Names()); got < 1 {
		t.Fatal("schema registry is empty")
	}

	validateTrackedArtifacts(t, registry, repoRoot)
	validateGoModuleLicenseContract(t, registry, repoRoot)
	validateRepresentativeContracts(t, registry)
}

func validateGoModuleLicenseContract(t *testing.T, registry *Registry, repoRoot string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot, "third_party", "go-modules.json"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(content), `"license": "Apache-2.0"`, `"license": "MIT"`, 1)
	if tampered == string(content) {
		t.Fatal("jsonschema audited license fixture was not found")
	}
	if err := registry.ValidateJSON("go-module-inventory.schema.json", []byte(tampered)); err == nil {
		t.Fatal("allowed-but-wrong jsonschema SPDX license unexpectedly passed the exact inventory schema")
	}
}

func validateTrackedArtifacts(t *testing.T, registry *Registry, repoRoot string) {
	t.Helper()
	tests := []struct {
		schema string
		paths  []string
	}{
		{"scenario-pack.schema.json", []string{"pgworkbench-pack.json"}},
		{"compatibility-matrix.schema.json", []string{"compatibility/matrix.json"}},
		{"go-module-inventory.schema.json", []string{"third_party/go-modules.json"}},
		{"release-evidence-index.schema.json", []string{"evidence/templates/release-evidence-index.json"}},
		{"adoption-pilot-record.schema.json", []string{"evidence/templates/adoption-pilot-record.json"}},
		{"critical-finding-review.schema.json", []string{"evidence/templates/critical-finding-review.json"}},
		{"operation-benchmark-spec.schema.json", mustGlob(t, repoRoot, "benchmarks/operations/*/*.json")},
		{"benchmark-driver-sysbench-config.schema.json", []string{"configs/benchmark-drivers/sysbench-postgresql.json"}},
		{"benchmark-driver-hammerdb-config.schema.json", mustGlob(t, repoRoot, "configs/benchmark-drivers/hammerdb-*.json")},
	}

	for _, test := range tests {
		for _, relativePath := range test.paths {
			relativePath := relativePath
			t.Run("tracked/"+filepath.ToSlash(relativePath), func(t *testing.T) {
				content, err := os.ReadFile(filepath.Join(repoRoot, relativePath))
				if err != nil {
					t.Fatal(err)
				}
				if err := registry.ValidateJSON(test.schema, content); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func validateRepresentativeContracts(t *testing.T, registry *Registry) {
	t.Helper()
	digest := "sha256:" + strings.Repeat("0", 64)
	validVerdict := map[string]any{
		"schema_version":             "pgworkbench.run-verdict/v1",
		"artifact_type":              "pgworkbench.run-verdict",
		"run_id":                     "schema-gate",
		"status":                     "passed",
		"message":                    "verified",
		"started_at":                 "2026-08-13T00:00:00Z",
		"finished_at":                "2026-08-13T00:00:01Z",
		"experiment_spec":            "smoke",
		"experiment_identity_digest": digest,
		"manifest_digest":            digest,
		"artifact_root":              ".",
		"run_dir":                    ".",
		"workload_exit":              0,
		"assert_exit":                0,
		"scan_exit":                  0,
	}
	t.Run("positive/passed-verdict", func(t *testing.T) {
		if err := registry.Validate("run-verdict.schema.json", validVerdict); err != nil {
			t.Fatal(err)
		}
	})

	validNativeEnvironment := benchmarkEnvironment(digest, "native")
	validNativeEnvironment["native_toolchain_digest"] = digest
	validNativeEnvironment["native_toolchain_manifest_ref"] = "protocol/native-toolchain.json"
	validNativeEnvironment["native_toolchain_provenance"] = "unattested"
	t.Run("positive/native-environment", func(t *testing.T) {
		if err := registry.Validate("benchmark-environment.schema.json", validNativeEnvironment); err != nil {
			t.Fatal(err)
		}
	})

	validDockerEnvironment := benchmarkEnvironment(digest, "docker")
	validDockerEnvironment["docker_driver_image_id"] = digest
	validDockerEnvironment["docker_target_image_id"] = digest
	t.Run("positive/docker-environment", func(t *testing.T) {
		if err := registry.Validate("benchmark-environment.schema.json", validDockerEnvironment); err != nil {
			t.Fatal(err)
		}
	})

	validPGConfigSeries := benchmarkSeries(digest, "pg_config")
	t.Run("positive/benchmark-series-pg-config", func(t *testing.T) {
		if err := registry.Validate("benchmark-series.schema.json", validPGConfigSeries); err != nil {
			t.Fatal(err)
		}
	})

	validNativeToolchainSeries := benchmarkSeries(digest, "native_toolchain")
	validNativeToolchainSeries["runtime"] = "native"
	t.Run("positive/benchmark-series-native-toolchain", func(t *testing.T) {
		if err := registry.Validate("benchmark-series.schema.json", validNativeToolchainSeries); err != nil {
			t.Fatal(err)
		}
	})

	validDescriptiveComparison := benchmarkComparison(digest)
	t.Run("positive/descriptive-benchmark-comparison", func(t *testing.T) {
		if err := registry.Validate("benchmark-comparison.schema.json", validDescriptiveComparison); err != nil {
			t.Fatal(err)
		}
	})

	invalidVerdict := cloneMap(validVerdict)
	invalidVerdict["workload_exit"] = 1
	assertRejected(t, registry, "run-verdict.schema.json", "passed-verdict-with-failed-workload", invalidVerdict)

	invalidTimestamp := cloneMap(validVerdict)
	invalidTimestamp["started_at"] = "not-a-date"
	assertRejected(t, registry, "run-verdict.schema.json", "invalid-date-time", invalidTimestamp)

	invalidNativeEnvironment := cloneMap(validNativeEnvironment)
	delete(invalidNativeEnvironment, "native_toolchain_digest")
	assertRejected(t, registry, "benchmark-environment.schema.json", "native-without-toolchain", invalidNativeEnvironment)

	invalidDockerEnvironment := cloneMap(validDockerEnvironment)
	invalidDockerEnvironment["docker_target_image_id"] = "not-applicable"
	assertRejected(t, registry, "benchmark-environment.schema.json", "docker-without-image-identity", invalidDockerEnvironment)

	invalidSeriesDimension := cloneMap(validPGConfigSeries)
	invalidSeriesDimension["allowed_subject_differences"] = []any{"unknown_dimension"}
	assertRejected(t, registry, "benchmark-series.schema.json", "benchmark-series-unknown-subject-difference", invalidSeriesDimension)

	duplicateSeriesDimension := cloneMap(validNativeToolchainSeries)
	duplicateSeriesDimension["allowed_subject_differences"] = []any{"native_toolchain", "native_toolchain"}
	assertRejected(t, registry, "benchmark-series.schema.json", "benchmark-series-duplicate-subject-difference", duplicateSeriesDimension)

	decisionEligibleComparison := cloneMap(validDescriptiveComparison)
	decisionEligibleComparison["status"] = "passed"
	decisionEligibleComparison["decision"] = "no-regression"
	assertRejected(t, registry, "benchmark-comparison.schema.json", "independent-comparison-performance-verdict", decisionEligibleComparison)

	mismatchedComparison := cloneMap(validDescriptiveComparison)
	mismatchedComparison["decision"] = "invalid"
	assertRejected(t, registry, "benchmark-comparison.schema.json", "independent-comparison-status-decision-mismatch", mismatchedComparison)

	invalidFixtures := []struct {
		name       string
		schemaName string
		filename   string
	}{
		{
			name:       "release-evidence-index-go-with-open-gates",
			schemaName: "release-evidence-index.schema.json",
			filename:   "release-evidence-index-go-with-open-gates.json",
		},
		{
			name:       "adoption-pilot-maintainer-shell",
			schemaName: "adoption-pilot-record.schema.json",
			filename:   "adoption-pilot-maintainer-shell.json",
		},
		{
			name:       "critical-review-go-open-critical",
			schemaName: "critical-finding-review.schema.json",
			filename:   "critical-review-go-open-critical.json",
		},
	}
	for _, fixture := range invalidFixtures {
		fixture := fixture
		t.Run("negative/"+fixture.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("testdata", fixture.filename))
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.ValidateJSON(fixture.schemaName, content); err == nil {
				t.Fatal("negative fixture unexpectedly passed validation")
			}
		})
	}

	t.Run("negative/malformed-json", func(t *testing.T) {
		if err := registry.ValidateJSON("run-verdict.schema.json", []byte(`{"status":`)); err == nil {
			t.Fatal("malformed JSON unexpectedly passed validation")
		}
	})
}

func benchmarkEnvironment(digest, runtime string) map[string]any {
	imageID := "not-applicable"
	return map[string]any{
		"schema_version":              "pgworkbench.benchmark-environment/v1",
		"artifact_type":               "pgworkbench.benchmark-environment",
		"runtime":                     runtime,
		"runtime_os":                  "linux",
		"runtime_arch":                "amd64",
		"driver":                      "pgbench",
		"target":                      "direct-postgres",
		"target_endpoint_contract":    "pgworkbench.pgbench-target/direct-postgres/v1",
		"target_endpoint_host":        "127.0.0.1",
		"target_endpoint_port":        5432,
		"docker_driver_image_id":      imageID,
		"docker_target_image_id":      imageID,
		"target_topology":             "single",
		"driver_version":              "pgbench (PostgreSQL) 19devel",
		"parser_version":              "1.2.0",
		"postgres_server_version_num": "190000",
		"postgres_server_major":       "19",
		"pg_config":                   "default",
		"pg_config_digest":            digest,
		"subject_dimension":           "pg_config",
		"native_toolchain_provenance": "not-applicable",
		"engine_version":              "dev",
		"engine_commit":               "unknown",
		"engine_binary_digest":        digest,
		"pack_id":                     "builtin",
		"pack_version":                "1",
		"pack_digest":                 digest,
		"qualification":               "unqualified-local",
		"digest":                      digest,
	}
}

func benchmarkSeries(digest, subjectDimension string) map[string]any {
	return map[string]any{
		"schema_version":              "pgworkbench.benchmark-series/v1",
		"artifact_type":               "pgworkbench.benchmark-series",
		"benchmark":                   "schema-gate",
		"name":                        "Schema gate representative series",
		"class":                       "smoke",
		"driver":                      "pgbench",
		"target":                      "direct-postgres",
		"target_endpoint_contract":    "pgworkbench.pgbench-target/direct-postgres/v1",
		"target_topology":             "single",
		"subject":                     "schema-gate-subject",
		"run_id":                      "schema-gate-series",
		"run_dir":                     ".",
		"spec_ref":                    "benchmarks/schema-gate.env",
		"spec_digest":                 digest,
		"protocol_digest":             digest,
		"comparison_key_digest":       digest,
		"engine_binary_ref":           "protocol/engine/pgworkbench",
		"engine_binary_digest":        digest,
		"allowed_subject_differences": []any{subjectDimension},
		"runtime":                     "docker",
		"evidence_class":              "local-smoke",
		"primary_metric":              "pgbench.tps",
		"direction":                   "higher",
		"max_cv_pct":                  10,
		"reset_policy":                "rebuild-per-trial",
		"started_at":                  "2026-08-13T00:00:00Z",
		"finished_at":                 "2026-08-13T00:00:01Z",
		"status":                      "invalid",
		"reasons":                     []any{"no trials recorded in schema fixture"},
		"trials_planned":              1,
		"trials_valid":                0,
		"trials_failed":               0,
		"trials_invalid":              0,
		"trials":                      []any{},
	}
}

func benchmarkComparison(digest string) map[string]any {
	return map[string]any{
		"schema_version":        "pgworkbench.benchmark-comparison/v1",
		"artifact_type":         "pgworkbench.benchmark-comparison",
		"analysis_version":      "1.0.0",
		"baseline_run_id":       "baseline-series",
		"candidate_run_id":      "candidate-series",
		"baseline_subject":      "baseline",
		"candidate_subject":     "candidate",
		"benchmark":             "pgbench/schema-gate",
		"comparison_key_digest": digest,
		"primary_metric":        "pgbench.tps",
		"direction":             "higher",
		"design":                "independent-series-unpaired",
		"confidence_level":      0.95,
		"bootstrap_method":      "percentile-bootstrap-of-median-ratio",
		"bootstrap_resamples":   10000,
		"bootstrap_seed":        42,
		"baseline_n":            5,
		"candidate_n":           5,
		"differences":           []any{"pg_config"},
		"status":                "inconclusive",
		"decision":              "inconclusive",
		"reasons":               []any{"independent series are descriptive only"},
	}
}

func assertRejected(t *testing.T, registry *Registry, schemaName, name string, value any) {
	t.Helper()
	t.Run("negative/"+name, func(t *testing.T) {
		if err := registry.Validate(schemaName, value); err == nil {
			t.Fatal("invalid artifact unexpectedly passed validation")
		}
	})
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func mustGlob(t *testing.T, repoRoot, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot, pattern))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("glob %q matched no files", pattern)
	}
	for index, match := range matches {
		relative, err := filepath.Rel(repoRoot, match)
		if err != nil {
			t.Fatal(err)
		}
		matches[index] = relative
	}
	return matches
}
