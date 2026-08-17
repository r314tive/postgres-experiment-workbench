package schemavalidation

import (
	"fmt"
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

	validExternalDriverVerification := externalDriverVerification(digest)
	t.Run("positive/release-external-driver-verification", func(t *testing.T) {
		if err := registry.Validate("release-external-driver-verification.schema.json", validExternalDriverVerification); err != nil {
			t.Fatal(err)
		}
	})
	wideExternalDriverIDs := externalDriverVerification(digest)
	wideWorkflowRun := cloneMap(wideExternalDriverIDs["workflow_run"].(map[string]any))
	wideWorkflowRun["id"] = "12345678901234567890123456789012"
	wideExternalDriverIDs["workflow_run"] = wideWorkflowRun
	wideSource := cloneMap(wideExternalDriverIDs["source"].(map[string]any))
	wideProviderArtifact := cloneMap(wideSource["provider_artifact"].(map[string]any))
	wideProviderArtifact["id"] = "98765432109876543210987654321098"
	wideSource["provider_artifact"] = wideProviderArtifact
	wideExternalDriverIDs["source"] = wideSource
	t.Run("positive/release-external-driver-verification-32-digit-ids", func(t *testing.T) {
		if err := registry.Validate("release-external-driver-verification.schema.json", wideExternalDriverIDs); err != nil {
			t.Fatal(err)
		}
	})

	validDraftAssetVerification := releaseAssetVerification(digest, "draft")
	t.Run("positive/release-draft-asset-verification", func(t *testing.T) {
		if err := registry.Validate("release-asset-verification.schema.json", validDraftAssetVerification); err != nil {
			t.Fatal(err)
		}
	})
	validPublicAssetVerification := releaseAssetVerification(digest, "published")
	t.Run("positive/release-public-asset-verification", func(t *testing.T) {
		if err := registry.Validate("release-asset-verification.schema.json", validPublicAssetVerification); err != nil {
			t.Fatal(err)
		}
	})
	validPublicationVerification := releasePublicationVerification(validPublicAssetVerification)
	t.Run("positive/release-publication-verification", func(t *testing.T) {
		if err := registry.Validate("release-publication-verification.schema.json", validPublicationVerification); err != nil {
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

	userSelectedExternalDriverOutcome := externalDriverVerification(digest)
	userSelectedExternalDriverOutcome["status"] = "passed"
	assertRejected(t, registry, "release-external-driver-verification.schema.json", "external-driver-user-selected-outcome", userSelectedExternalDriverOutcome)
	userSelectedExternalDriverBoolean := externalDriverVerification(digest)
	userSelectedExternalDriverBoolean["passed"] = true
	assertRejected(t, registry, "release-external-driver-verification.schema.json", "external-driver-user-selected-passed-flag", userSelectedExternalDriverBoolean)
	userSelectedAssetOutcome := releaseAssetVerification(digest, "draft")
	userSelectedAssetOutcome["status"] = "passed"
	assertRejected(t, registry, "release-asset-verification.schema.json", "release-asset-user-selected-outcome", userSelectedAssetOutcome)
	publicClaim := releaseAssetVerification(digest, "published")
	publicAssurance := cloneMap(publicClaim["assurance"].(map[string]any))
	publicAssurance["performance_claim"] = true
	publicClaim["assurance"] = publicAssurance
	assertRejected(t, registry, "release-asset-verification.schema.json", "release-asset-performance-claim", publicClaim)
	mutatingPublication := releasePublicationVerification(validPublicAssetVerification)
	publicationObservation := cloneMap(mutatingPublication["observation"].(map[string]any))
	publicationObservation["mutation_performed_by_verifier"] = true
	mutatingPublication["observation"] = publicationObservation
	assertRejected(t, registry, "release-publication-verification.schema.json", "publication-mutating-verifier", mutatingPublication)
	tooLongExternalDriverRunID := externalDriverVerification(digest)
	tooLongWorkflowRun := cloneMap(tooLongExternalDriverRunID["workflow_run"].(map[string]any))
	tooLongWorkflowRun["id"] = strings.Repeat("9", 33)
	tooLongExternalDriverRunID["workflow_run"] = tooLongWorkflowRun
	assertRejected(t, registry, "release-external-driver-verification.schema.json", "external-driver-33-digit-run-id", tooLongExternalDriverRunID)

	wrongExternalDriverSet := externalDriverVerification(digest)
	wrongExternalDriverSet["drivers"] = []any{
		"benchbase-postgresql-33c0047",
		"hammerdb-postgresql-6.0",
		"sysbench-postgresql-latest",
	}
	assertRejected(t, registry, "release-external-driver-verification.schema.json", "external-driver-wrong-driver-set", wrongExternalDriverSet)

	missingExternalDriverPack := externalDriverVerification(digest)
	candidate := cloneMap(missingExternalDriverPack["candidate"].(map[string]any))
	delete(candidate, "scenario_pack")
	missingExternalDriverPack["candidate"] = candidate
	assertRejected(t, registry, "release-external-driver-verification.schema.json", "external-driver-missing-scenario-pack", missingExternalDriverPack)

	unverifiedExternalDriverCandidate := externalDriverVerification(digest)
	assurance := cloneMap(unverifiedExternalDriverCandidate["assurance"].(map[string]any))
	assurance["candidate_identity_reverified"] = false
	unverifiedExternalDriverCandidate["assurance"] = assurance
	assertRejected(t, registry, "release-external-driver-verification.schema.json", "external-driver-unverified-candidate", unverifiedExternalDriverCandidate)

	missingExternalDriverBoundary := externalDriverVerification(digest)
	boundaryAssurance := cloneMap(missingExternalDriverBoundary["assurance"].(map[string]any))
	delete(boundaryAssurance, "third_party_runtime_bytes_uploaded")
	missingExternalDriverBoundary["assurance"] = boundaryAssurance
	assertRejected(t, registry, "release-external-driver-verification.schema.json", "external-driver-missing-false-assurance", missingExternalDriverBoundary)

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

func externalDriverVerification(digest string) map[string]any {
	return map[string]any{
		"schema_version":     "pgworkbench.release-external-driver-verification/v1",
		"artifact_type":      "pgworkbench.release-external-driver-verification",
		"qualification_mode": "draft-release-smoke",
		"candidate": map[string]any{
			"version":           "1.2.3",
			"tag":               "v1.2.3",
			"git_commit":        strings.Repeat("a", 40),
			"asset_fingerprint": strings.Repeat("b", 64),
			"scenario_pack": map[string]any{
				"id":      "builtin",
				"version": "1.2.3",
				"digest":  digest,
			},
		},
		"captured_at": "2026-08-13T00:00:01Z",
		"workflow_run": map[string]any{
			"id":         "123456789",
			"attempt":    1,
			"head_sha":   strings.Repeat("a", 40),
			"repository": "r314tive/postgres-experiment-workbench",
		},
		"source": map[string]any{
			"gate_digest":             digest,
			"metadata_archive_digest": digest,
			"provider_artifact": map[string]any{
				"id":     "987654321",
				"name":   "draft-external-driver-metadata-v1.2.3-candidate-1",
				"digest": digest,
			},
			"release_archive_digest":  digest,
			"release_manifest_digest": digest,
		},
		"drivers": []any{
			"benchbase-postgresql-33c0047",
			"hammerdb-postgresql-6.0",
			"sysbench-postgresql-1.0.20",
		},
		"assurance": map[string]any{
			"purpose":                                 "adapter-compatibility-release-smoke",
			"artifact_payload":                        "metadata-only-no-third-party-runtime-bytes",
			"verification_scope":                      "workflow-local-content-and-semantics",
			"third_party_runtime_bytes_uploaded":      false,
			"performance_claim":                       false,
			"production_decision_eligible":            false,
			"source_to_binary_attested":               false,
			"driver_runtime_closure_attested":         true,
			"host_runtime_dependencies_attested":      false,
			"benchmark_comparability_claim":           false,
			"project_redistribution":                  false,
			"all_executions_locally_verified":         true,
			"exact_source_to_staged_file_match":       true,
			"disposable_loopback_target_acknowledged": true,
			"system_databases_denied":                 true,
			"candidate_identity_reverified":           true,
			"provider_artifact_reverified":            true,
			"release_archive_provenance_verified":     true,
			"release_manifest_provenance_verified":    true,
		},
	}
}

func releaseAssetVerification(digest, mode string) map[string]any {
	commit := strings.Repeat("a", 40)
	fingerprint := strings.Repeat("b", 64)
	assets := make([]any, 16)
	for index := range assets {
		assets[index] = map[string]any{
			"id": index + 1, "name": fmt.Sprintf("asset-%02d.json", index+1),
			"size": index + 1, "digest": digest,
		}
	}
	job := "draft-verify"
	isDraft := true
	publicOnly := "not-applicable"
	provider := map[string]any{
		"tag": "v1.2.3", "tag_target_sha": commit, "release_state": mode,
		"is_draft": isDraft, "asset_count": 16, "asset_fingerprint": fingerprint,
	}
	source := map[string]any{"asset_inventory_digest": digest, "release_manifest_digest": digest}
	if mode == "published" {
		job = "public-verify"
		isDraft = false
		provider["is_draft"] = isDraft
		provider["is_immutable"] = true
		publicOnly = "verified"
		source["release_attestation_digest"] = digest
	}
	return map[string]any{
		"schema_version":     "pgworkbench.release-asset-verification/v1",
		"artifact_type":      "pgworkbench.release-asset-verification",
		"qualification_mode": mode,
		"candidate": map[string]any{
			"version": "1.2.3", "tag": "v1.2.3", "git_commit": commit,
			"asset_fingerprint": fingerprint,
			"scenario_pack":     map[string]any{"id": "builtin", "version": "1.2.3", "digest": digest},
		},
		"captured_at": "2026-08-13T00:00:01Z",
		"workflow_run": map[string]any{
			"id": "123456789", "attempt": 1, "head_sha": commit,
			"repository": "r314tive/postgres-experiment-workbench", "workflow": "release-snapshot",
			"job": job, "ref": "refs/tags/v1.2.3",
		},
		"inventory": map[string]any{
			"schema_version": "pgworkbench.release-asset-inventory/v1",
			"artifact_type":  "pgworkbench.release-asset-inventory", "release_state": mode,
			"tag": "v1.2.3", "git_commit": commit, "captured_at": "2026-08-13T00:00:00Z",
			"fingerprint_algorithm": "github-release-assets-jq-cS/v1",
			"asset_fingerprint":     fingerprint, "assets": assets,
		},
		"provider_observation": provider,
		"source":               source,
		"checks": map[string]any{
			"tag_target": "verified", "closed_asset_set": "verified", "downloaded_asset_bytes": "verified",
			"archive_checksums": "verified", "metadata_checksums": "verified", "release_manifest": "verified",
			"candidate_binary_identity": "verified", "provenance_attestations": "verified",
			"sbom_attestations": "verified", "sbom_contents": "verified",
			"immutable_release": publicOnly, "release_attestation": publicOnly,
		},
		"assurance": map[string]any{
			"purpose":            "release-asset-authenticity-and-integrity",
			"verification_scope": "workflow-local-provider-and-content", "actions_artifact_durable": false,
			"candidate_identity_reverified": true, "provider_asset_set_recomputed": true,
			"all_downloaded_bytes_verified": true, "performance_claim": false,
			"benchmark_comparability_claim": false, "recovery_claim": false,
			"production_decision_eligible": false,
		},
	}
}

func releasePublicationVerification(publicAsset map[string]any) map[string]any {
	commit := strings.Repeat("a", 40)
	fingerprint := strings.Repeat("b", 64)
	return map[string]any{
		"schema_version":            "pgworkbench.release-publication-verification/v1",
		"artifact_type":             "pgworkbench.release-publication-verification",
		"candidate":                 publicAsset["candidate"],
		"captured_at":               "2026-08-13T00:00:01Z",
		"workflow_run":              publicAsset["workflow_run"],
		"public_asset_verification": publicAsset,
		"observation": map[string]any{
			"post_publication_observation": true, "mutation_performed_by_verifier": false,
			"draft_public_fingerprint_equal": true, "release_state": "published",
			"is_draft": false, "is_immutable": true, "tag_target_sha": commit,
			"asset_count": 16, "asset_fingerprint": fingerprint, "release_attestation": "verified",
		},
		"assurance": map[string]any{
			"purpose":            "post-publication-read-only-observation",
			"verification_scope": "workflow-local-provider-and-content", "actions_artifact_durable": false,
			"candidate_identity_reverified": true, "performance_claim": false,
			"benchmark_comparability_claim": false, "recovery_claim": false,
			"production_decision_eligible": false,
		},
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
