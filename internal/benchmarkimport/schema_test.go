package benchmarkimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSchemasTrackGoContracts(t *testing.T) {
	tests := []struct {
		file, version, artifact string
		goType                  reflect.Type
	}{
		{"benchmark-import.schema.json", SchemaVersion, ArtifactType, reflect.TypeOf(Artifact{})},
		{"benchmark-import-mapping.schema.json", MappingSchemaVersion, MappingArtifactType, reflect.TypeOf(Mapping{})},
		{"benchmark-import-bundle-inventory.schema.json", BundleSchemaVersion, BundleArtifactType, reflect.TypeOf(BundleInventory{})},
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
			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatal("schema properties are missing")
			}
			assertSchemaConst(t, properties, "schema_version", test.version)
			assertSchemaConst(t, properties, "artifact_type", test.artifact)
			for index := 0; index < test.goType.NumField(); index++ {
				field := test.goType.Field(index)
				name := field.Tag.Get("json")
				if comma := stringsIndexByte(name, ','); comma >= 0 {
					name = name[:comma]
				}
				if name == "" || name == "-" {
					continue
				}
				if _, exists := properties[name]; !exists {
					t.Fatalf("Go JSON field %q is absent from %s", name, test.file)
				}
			}
		})
	}
}

func assertSchemaConst(t *testing.T, properties map[string]any, field, want string) {
	t.Helper()
	property, ok := properties[field].(map[string]any)
	if !ok || property["const"] != want {
		t.Fatalf("%s const = %#v, want %q", field, property["const"], want)
	}
}

func stringsIndexByte(value string, target byte) int {
	for index := range value {
		if value[index] == target {
			return index
		}
	}
	return -1
}
