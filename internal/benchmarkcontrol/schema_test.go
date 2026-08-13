package benchmarkcontrol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestControlSchemasTrackGoContractsAndCloseEveryObject(t *testing.T) {
	tests := []struct {
		file, version, artifactType string
		goType                      reflect.Type
	}{
		{"benchmark-cache-state.schema.json", CacheStateSchemaVersion, CacheStateArtifactType, reflect.TypeOf(CacheState{})},
		{"benchmark-statistics-reset.schema.json", StatisticsResetSchemaVersion, StatisticsResetArtifactType, reflect.TypeOf(StatisticsReset{})},
		{"benchmark-collector-overhead.schema.json", CollectorOverheadSchemaVersion, CollectorOverheadArtifactType, reflect.TypeOf(CollectorOverhead{})},
		{"benchmark-resource-budget.schema.json", ResourceBudgetSchemaVersion, ResourceBudgetArtifactType, reflect.TypeOf(ResourceBudget{})},
	}
	ids := map[string]struct{}{}
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
			if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["$id"] == "" || schema["additionalProperties"] != false {
				t.Fatalf("schema root is not a closed draft 2020-12 contract: %#v", schema)
			}
			id := schema["$id"].(string)
			if _, exists := ids[id]; exists {
				t.Fatalf("duplicate schema id: %s", id)
			}
			ids[id] = struct{}{}
			properties := schema["properties"].(map[string]any)
			if propertyConstForControl(t, properties, "schema_version") != test.version || propertyConstForControl(t, properties, "artifact_type") != test.artifactType {
				t.Fatal("schema identity does not match Go constants")
			}
			assertSchemaTracksControlType(t, schema, test.goType)
			assertClosedSchemaObjects(t, "$", schema)
		})
	}
}

func assertSchemaTracksControlType(t *testing.T, schema map[string]any, value reflect.Type) {
	t.Helper()
	requiredValues := schema["required"].([]any)
	required := map[string]bool{}
	for _, item := range requiredValues {
		required[item.(string)] = true
	}
	properties := schema["properties"].(map[string]any)
	fields := map[string]bool{}
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
		optional := false
		for _, option := range parts[1:] {
			optional = optional || option == "omitempty"
		}
		fields[name] = true
		if _, exists := properties[name]; !exists {
			t.Errorf("schema missing Go JSON field %q", name)
		}
		if optional == required[name] {
			t.Errorf("schema required mismatch for Go field %q", name)
		}
	}
	for name := range properties {
		if !fields[name] {
			t.Errorf("schema property %q has no Go JSON field", name)
		}
	}
}

func assertClosedSchemaObjects(t *testing.T, path string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "object" && typed["additionalProperties"] != false {
			t.Errorf("object schema %s is not closed", path)
		}
		for key, child := range typed {
			assertClosedSchemaObjects(t, path+"."+key, child)
		}
	case []any:
		for index, child := range typed {
			assertClosedSchemaObjects(t, path+"["+strconv.Itoa(index)+"]", child)
		}
	}
}

func propertyConstForControl(t *testing.T, properties map[string]any, name string) string {
	t.Helper()
	property := properties[name].(map[string]any)
	value, ok := property["const"].(string)
	if !ok {
		t.Fatalf("schema property %s has no string const", name)
	}
	return value
}
