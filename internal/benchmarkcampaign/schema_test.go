package benchmarkcampaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const (
	benchmarkRunIDPattern        = "^[A-Za-z0-9][A-Za-z0-9._-]*$"
	benchmarkPortablePathPattern = "^(?!/)(?!.*(^|/)\\.\\.(/|$))(?!.*\\\\)(?!.*:).+$"
)

func TestCampaignSchemasTrackGoContracts(t *testing.T) {
	tests := []struct {
		file, version, artifact string
		goType                  reflect.Type
	}{
		{"benchmark-campaign-protocol.schema.json", ProtocolSchemaVersion, ProtocolArtifactType, reflect.TypeOf(Protocol{})},
		{"benchmark-campaign-execution.schema.json", ExecutionSchemaVersion, ExecutionArtifactType, reflect.TypeOf(Execution{})},
		{"benchmark-campaign-run.schema.json", RunSchemaVersion, RunArtifactType, reflect.TypeOf(Result{})},
		{"benchmark-campaign-bundle-inventory.schema.json", BundleSchemaVersion, BundleArtifactType, reflect.TypeOf(BundleInventory{})},
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
				if comma := schemaIndexByte(name, ','); comma >= 0 {
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

func TestCampaignExecutionSchemaForbidsClaimsForUnverifiedRows(t *testing.T) {
	schema := readCampaignSchema(t, "benchmark-campaign-execution.schema.json")
	allOf := mustSchemaArray(t, schema, "allOf")
	var unverified map[string]any
	for _, raw := range allOf {
		conditional, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ifSchema, ok := conditional["if"].(map[string]any)
		if !ok {
			continue
		}
		properties, _ := ifSchema["properties"].(map[string]any)
		evidenceStatus, _ := properties["evidence_status"].(map[string]any)
		if evidenceStatus["const"] == "unverified" {
			unverified = conditional
			break
		}
	}
	if unverified == nil {
		t.Fatal("unverified evidence conditional is missing")
	}
	then := mustSchemaMap(t, unverified, "then")
	properties := mustSchemaMap(t, then, "properties")
	assertSchemaConst(t, properties, "status", "unavailable")
	for _, field := range []string{"trials_planned", "trials_valid", "trials_failed", "trials_invalid"} {
		property := mustSchemaMap(t, properties, field)
		if property["const"] != float64(0) {
			t.Fatalf("unverified %s const = %#v, want 0", field, property["const"])
		}
	}

	not := mustSchemaMap(t, then, "not")
	anyOf := mustSchemaArray(t, not, "anyOf")
	forbidden := make([]string, 0, len(anyOf))
	for _, raw := range anyOf {
		clause, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("unverified forbidden clause has type %T", raw)
		}
		required := mustSchemaArray(t, clause, "required")
		if len(required) != 1 {
			t.Fatalf("unverified forbidden clause required = %#v, want exactly one key", required)
		}
		name, ok := required[0].(string)
		if !ok {
			t.Fatalf("unverified forbidden key has type %T", required[0])
		}
		forbidden = append(forbidden, name)
	}
	sort.Strings(forbidden)
	want := []string{"cv_pct", "finished_at", "median", "result_digest", "series_ref", "started_at"}
	if !reflect.DeepEqual(forbidden, want) {
		t.Fatalf("unverified forbidden keys = %v, want %v", forbidden, want)
	}
}

func TestCampaignBundleSchemaUsesStrictPortableFilePath(t *testing.T) {
	schema := readCampaignSchema(t, "benchmark-campaign-bundle-inventory.schema.json")
	definitions := mustSchemaMap(t, schema, "$defs")
	portablePath := mustSchemaMap(t, definitions, "portable_path")
	if portablePath["pattern"] != benchmarkPortablePathPattern {
		t.Fatalf("portable path pattern = %#v, want %q", portablePath["pattern"], benchmarkPortablePathPattern)
	}
	file := mustSchemaMap(t, definitions, "file")
	properties := mustSchemaMap(t, file, "properties")
	path := mustSchemaMap(t, properties, "path")
	if path["$ref"] != "#/$defs/portable_path" {
		t.Fatalf("bundle file path ref = %#v", path["$ref"])
	}
}

func TestCampaignSchemasUseCanonicalBenchmarkRunIDPattern(t *testing.T) {
	for _, file := range []string{
		"benchmark-campaign-protocol.schema.json",
		"benchmark-campaign-execution.schema.json",
		"benchmark-campaign-run.schema.json",
		"benchmark-campaign-bundle-inventory.schema.json",
	} {
		t.Run(file, func(t *testing.T) {
			schema := readCampaignSchema(t, file)
			var runID map[string]any
			if definitions, ok := schema["$defs"].(map[string]any); ok {
				runID, _ = definitions["run_id"].(map[string]any)
			}
			if runID == nil {
				properties := mustSchemaMap(t, schema, "properties")
				runID = mustSchemaMap(t, properties, "campaign_id")
			}
			if runID["pattern"] != benchmarkRunIDPattern {
				t.Fatalf("run id pattern = %#v, want %q", runID["pattern"], benchmarkRunIDPattern)
			}
		})
	}
}

func readCampaignSchema(t *testing.T, file string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", file))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func mustSchemaMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("schema %s has type %T, want object", key, parent[key])
	}
	return value
}

func mustSchemaArray(t *testing.T, parent map[string]any, key string) []any {
	t.Helper()
	value, ok := parent[key].([]any)
	if !ok {
		t.Fatalf("schema %s has type %T, want array", key, parent[key])
	}
	return value
}

func assertSchemaConst(t *testing.T, properties map[string]any, field, want string) {
	t.Helper()
	property, ok := properties[field].(map[string]any)
	if !ok || property["const"] != want {
		t.Fatalf("%s const = %#v, want %q", field, property["const"], want)
	}
}

func schemaIndexByte(value string, target byte) int {
	for index := range value {
		if value[index] == target {
			return index
		}
	}
	return -1
}
