package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if _, ok := schema.Properties["runtime_ports_digest"]; !ok {
		t.Fatal("run manifest schema missing v2 runtime_ports_digest property")
	}
	if containsString(schema.Required, "runtime_ports_digest") {
		t.Fatal("run manifest root requires runtime_ports_digest and would reject v1 manifests")
	}

	var schemaVersion struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(schema.Properties["schema_version"], &schemaVersion); err != nil {
		t.Fatal(err)
	}
	if len(schemaVersion.Enum) != 2 || schemaVersion.Enum[0] != ManifestSchemaVersion || schemaVersion.Enum[1] != ManifestSchemaVersionV2 {
		t.Fatalf("schema versions = %q, want v1/v2", schemaVersion.Enum)
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(string(content))
	if !strings.Contains(compact, `"then":{"required":["runtime_ports_digest"]}`) ||
		!strings.Contains(compact, `"else":{"not":{"required":["runtime_ports_digest"]}}`) {
		t.Fatal("run manifest schema does not require the port digest only for v2")
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
