package pgdrillbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaTracksClosedGoContract(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "pgdrill-baseline.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatal("root schema is not closed")
	}
	properties := mustObject(t, schema, "properties")
	assertSchemaConst(t, properties, "schema_version", SchemaVersion)
	assertSchemaConst(t, properties, "artifact_type", ArtifactType)
	assertSchemaConst(t, properties, "contract_version", ContractVersion)
	assertSchemaConst(t, properties, "classification", Classification)
	assertFields(t, properties, reflect.TypeOf(Artifact{}))
	assertRequiredFields(t, schema, reflect.TypeOf(Artifact{}))

	definitions := mustObject(t, schema, "$defs")
	types := map[string]reflect.Type{
		"file_binding":        reflect.TypeOf(FileBinding{}),
		"source_verification": reflect.TypeOf(SourceVerification{}),
		"run":                 reflect.TypeOf(RunIdentity{}),
		"scenario_pack":       reflect.TypeOf(ScenarioPackIdentity{}),
		"experiment_spec":     reflect.TypeOf(ExperimentSpecIdentity{}),
		"postgres":            reflect.TypeOf(PostgresIdentity{}),
		"predicate":           reflect.TypeOf(ReviewedPredicate{}),
		"assurance_boundary":  reflect.TypeOf(AssuranceBoundary{}),
	}
	for name, goType := range types {
		definition := mustObject(t, definitions, name)
		if definition["additionalProperties"] != false {
			t.Fatalf("schema definition %s is not closed", name)
		}
		assertFields(t, mustObject(t, definition, "properties"), goType)
		assertRequiredFields(t, definition, goType)
	}
}

func assertFields(t *testing.T, properties map[string]any, goType reflect.Type) {
	t.Helper()
	for index := 0; index < goType.NumField(); index++ {
		field := goType.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, exists := properties[name]; !exists {
			t.Fatalf("Go JSON field %s.%s (%q) is absent from schema", goType.Name(), field.Name, name)
		}
	}
}

func assertRequiredFields(t *testing.T, schema map[string]any, goType reflect.Type) {
	t.Helper()
	requiredValues, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema for %s has no required field list", goType.Name())
	}
	required := make(map[string]struct{}, len(requiredValues))
	for _, value := range requiredValues {
		name, ok := value.(string)
		if !ok {
			t.Fatalf("schema for %s has non-string required field %#v", goType.Name(), value)
		}
		required[name] = struct{}{}
	}
	for index := 0; index < goType.NumField(); index++ {
		name := strings.Split(goType.Field(index).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, exists := required[name]; !exists {
			t.Fatalf("Go JSON field %s.%s (%q) is not required by the closed schema", goType.Name(), goType.Field(index).Name, name)
		}
	}
}

func mustObject(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("schema object %q is missing: %#v", key, parent[key])
	}
	return value
}

func assertSchemaConst(t *testing.T, properties map[string]any, field, want string) {
	t.Helper()
	property := mustObject(t, properties, field)
	if property["const"] != want {
		t.Fatalf("%s const = %#v, want %q", field, property["const"], want)
	}
}
