package benchmarkexternal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExternalExecutionSchemasTrackClosedGoContracts(t *testing.T) {
	tests := []struct {
		file, schemaVersion, artifactType string
		value                             any
	}{
		{"benchmark-driver-execution.schema.json", SchemaVersion, ArtifactType, Artifact{}},
		{"benchmark-driver-execution-inventory.schema.json", InventorySchemaVersion, InventoryArtifactType, Inventory{}},
		{"benchmark-driver-sysbench-config.schema.json", SysbenchConfigSchema, SysbenchConfigArtifact, SysbenchConfig{}},
		{"benchmark-driver-hammerdb-config.schema.json", HammerDBConfigSchema, HammerDBConfigArtifact, HammerDBConfig{}},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", "schemas", test.file))
			if err != nil {
				t.Fatal(err)
			}
			var schema map[string]any
			if err := json.Unmarshal(content, &schema); err != nil {
				t.Fatal(err)
			}
			if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
				t.Fatal("schema root must be a closed draft 2020-12 object")
			}
			properties := schema["properties"].(map[string]any)
			if properties["schema_version"].(map[string]any)["const"] != test.schemaVersion || properties["artifact_type"].(map[string]any)["const"] != test.artifactType {
				t.Fatal("schema identity does not match Go constants")
			}
			assertSchemaFields(t, properties, reflect.TypeOf(test.value))
		})
	}
}

func TestExternalExecutionSchemaClosesNestedObjects(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "benchmark-driver-execution.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["$defs"].(map[string]any)
	for _, name := range []string{"file_ref", "runtime_file_ref", "driver_runtime", "driver", "registry_binding", "invocation", "target_safety", "inputs", "outputs", "normalized_import", "assurance"} {
		definition := definitions[name].(map[string]any)
		if definition["additionalProperties"] != false {
			t.Fatalf("definition %s must be closed", name)
		}
	}
}

func assertSchemaFields(t *testing.T, properties map[string]any, value reflect.Type) {
	t.Helper()
	for index := 0; index < value.NumField(); index++ {
		name := strings.Split(value.Field(index).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, exists := properties[name]; !exists {
			t.Errorf("schema is missing Go JSON field %q", name)
		}
	}
}
