package benchmarkab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type abSchemaDocument struct {
	Schema               string                     `json:"$schema"`
	ID                   string                     `json:"$id"`
	AdditionalProperties *bool                      `json:"additionalProperties"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
}

func TestABSchemasTrackRootGoContracts(t *testing.T) {
	tests := []struct {
		file, version, artifactType string
		goType                      reflect.Type
	}{
		{"benchmark-ab-protocol.schema.json", ProtocolSchemaVersion, ProtocolArtifactType, reflect.TypeOf(Protocol{})},
		{"benchmark-ab-run.schema.json", RunSchemaVersion, RunArtifactType, reflect.TypeOf(Result{})},
		{"benchmark-ab-bundle-inventory.schema.json", BundleSchemaVersion, BundleArtifactType, reflect.TypeOf(BundleInventory{})},
	}
	ids := make(map[string]struct{})
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", "schemas", test.file))
			if err != nil {
				t.Fatal(err)
			}
			var schema abSchemaDocument
			if err := json.Unmarshal(content, &schema); err != nil {
				t.Fatal(err)
			}
			if schema.Schema != "https://json-schema.org/draft/2020-12/schema" || schema.ID == "" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
				t.Fatalf("schema root is not a closed draft 2020-12 contract: %#v", schema)
			}
			if _, exists := ids[schema.ID]; exists {
				t.Fatalf("duplicate schema id: %s", schema.ID)
			}
			ids[schema.ID] = struct{}{}
			if got := abPropertyConst(t, schema, "schema_version"); got != test.version {
				t.Fatalf("schema version = %q, want %q", got, test.version)
			}
			if got := abPropertyConst(t, schema, "artifact_type"); got != test.artifactType {
				t.Fatalf("artifact type = %q, want %q", got, test.artifactType)
			}
			assertABSchemaFields(t, schema, test.goType)
		})
	}
}

func assertABSchemaFields(t *testing.T, schema abSchemaDocument, value reflect.Type) {
	t.Helper()
	fields := make(map[string]bool)
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		parts := strings.Split(field.Tag.Get("json"), ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		omitempty := false
		for _, option := range parts[1:] {
			if option == "omitempty" {
				omitempty = true
			}
		}
		fields[name] = !omitempty
		if _, exists := schema.Properties[name]; !exists {
			t.Errorf("schema is missing Go JSON field %q", name)
		}
		if !omitempty && !abContains(schema.Required, name) {
			t.Errorf("schema required is missing non-omitempty Go field %q", name)
		}
		if omitempty && abContains(schema.Required, name) {
			t.Errorf("schema requires omitempty Go field %q", name)
		}
	}
	for name := range schema.Properties {
		if _, exists := fields[name]; !exists {
			t.Errorf("schema property %q has no Go JSON field", name)
		}
	}
}

func abPropertyConst(t *testing.T, schema abSchemaDocument, name string) string {
	t.Helper()
	var property struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(schema.Properties[name], &property); err != nil {
		t.Fatal(err)
	}
	return property.Const
}

func abContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
