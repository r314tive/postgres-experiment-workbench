package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunManifestJSONSchemaTracksRuntimeFingerprintContract(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "run-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"engine_version",
		"engine_commit",
		"source_spec_kind",
		"source_spec_id",
		"source_spec_ref",
		"source_spec_digest",
		"runtime_fingerprint_status",
		"runtime_fingerprint_target",
		"runtime_os",
		"runtime_arch",
		"postgres_server_version_num",
		"postgres_server_major",
		"runtime_fingerprint_observed_at",
		"metrics_samples",
	} {
		if _, ok := schema.Properties[key]; !ok {
			t.Fatalf("run manifest schema missing property %q", key)
		}
		if !containsString(schema.Required, key) {
			t.Fatalf("run manifest schema does not require %q", key)
		}
	}

	var schemaVersion struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion.Const != ManifestSchemaVersion {
		t.Fatalf("schema const = %q, want %q", schemaVersion.Const, ManifestSchemaVersion)
	}

	var sourceSpecKind struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(schema.Properties["source_spec_kind"], &sourceSpecKind); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "utility-test", "benchmark"} {
		if !containsString(sourceSpecKind.Enum, value) {
			t.Fatalf("source_spec_kind schema does not admit %q: %v", value, sourceSpecKind.Enum)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
