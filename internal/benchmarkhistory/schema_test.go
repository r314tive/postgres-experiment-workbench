package benchmarkhistory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHistorySchemasTrackGoContracts(t *testing.T) {
	tests := []struct {
		file, version, artifact string
		goType                  reflect.Type
	}{
		{"benchmark-history.schema.json", SchemaVersion, ArtifactType, reflect.TypeOf(Result{})},
		{"benchmark-history-bundle-inventory.schema.json", BundleSchemaVersion, BundleArtifactType, reflect.TypeOf(BundleInventory{})},
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
			assertConst(t, properties, "schema_version", test.version)
			assertConst(t, properties, "artifact_type", test.artifact)
			for index := 0; index < test.goType.NumField(); index++ {
				field := test.goType.Field(index)
				name := field.Tag.Get("json")
				if comma := indexByte(name, ','); comma >= 0 {
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

func TestHistorySchemaRequiresEnvironmentPopulationDigest(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "benchmark-history.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("history schema required list is missing")
	}
	for _, field := range required {
		if field == "environment_digest" {
			return
		}
	}
	t.Fatal("history schema does not require its environment population digest")
}

func assertConst(t *testing.T, properties map[string]any, field, want string) {
	t.Helper()
	property, ok := properties[field].(map[string]any)
	if !ok || property["const"] != want {
		t.Fatalf("%s const = %#v, want %q", field, property["const"], want)
	}
}

func indexByte(value string, target byte) int {
	for index := range value {
		if value[index] == target {
			return index
		}
	}
	return -1
}
