package benchmarkrun_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcompare"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksettings"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchlog"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgbenchresult"
)

const draft202012 = "https://json-schema.org/draft/2020-12/schema"

type schemaDocument struct {
	Schema               string                     `json:"$schema"`
	ID                   string                     `json:"$id"`
	AdditionalProperties *bool                      `json:"additionalProperties"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
}

func TestBenchmarkArtifactSchemasTrackGoContracts(t *testing.T) {
	tests := []struct {
		file         string
		goType       reflect.Type
		versionField string
		version      string
		versions     []string
		artifactType string
		keyRequired  []string
	}{
		{
			file:         "benchmark-plan.schema.json",
			goType:       reflect.TypeOf(benchmarkplan.Plan{}),
			versionField: "protocol_schema_version",
			versions:     []string{benchmarkplan.ProtocolSchemaVersion, benchmarkplan.ProtocolSchemaVersionV2},
			keyRequired: []string{
				"spec", "target", "target_endpoint_contract", "target_topology", "target_topology_path", "target_topology_digest",
				"cache_regime", "statistics_reset_policy", "statistics_reset_boundary",
				"collectors", "collector_interval_seconds", "collector_overhead_mode",
				"client_placement", "resource_budget_mode", "random_seed_semantics",
				"protocol_digest", "comparison_key_digest",
			},
		},
		{
			file:         "benchmark-pgbench-result.schema.json",
			goType:       reflect.TypeOf(pgbenchresult.Result{}),
			versionField: "schema_version",
			version:      pgbenchresult.ResultSchemaVersion,
			keyRequired:  []string{"parser_version", "mode", "transactions_processed", "latency_mean_ms"},
		},
		{
			file:         "benchmark-pgbench-log-result.schema.json",
			goType:       reflect.TypeOf(pgbenchlog.Result{}),
			versionField: "schema_version",
			version:      pgbenchlog.ResultSchemaVersion,
			keyRequired:  []string{"parser_version", "logged", "completed", "latency_us"},
		},
		{
			file:         "benchmark-postgres-metrics.schema.json",
			goType:       reflect.TypeOf(benchmarkmetrics.Summary{}),
			versionField: "schema_version",
			version:      benchmarkmetrics.SchemaVersion,
			artifactType: benchmarkmetrics.ArtifactType,
			keyRequired:  []string{"parser_version", "postgres_server_major", "source", "measure", "coverage", "cadence", "statistics_reset", "database", "counter_deltas", "gauges", "digest"},
		},
		{
			file:         "benchmark-phase-timeline.schema.json",
			goType:       reflect.TypeOf(benchmarkphase.Timeline{}),
			versionField: "schema_version",
			version:      benchmarkphase.SchemaVersion,
			artifactType: benchmarkphase.ArtifactType,
			keyRequired:  []string{"run_id", "trial", "duration_ns", "events", "digest"},
		},
		{
			file:         "benchmark-effective-settings.schema.json",
			goType:       reflect.TypeOf(benchmarksettings.Evidence{}),
			versionField: "schema_version",
			version:      benchmarksettings.SchemaVersion,
			artifactType: benchmarksettings.ArtifactType,
			keyRequired:  []string{"parser_version", "run_id", "protocol_digest", "trial", "server_version_num", "names", "settings", "source", "phase", "digest"},
		},
		{
			file:         "benchmark-series.schema.json",
			goType:       reflect.TypeOf(benchmarkrun.Series{}),
			versionField: "schema_version",
			versions:     []string{benchmarkrun.SeriesSchemaVersion, benchmarkrun.SeriesSchemaVersionV2},
			artifactType: benchmarkrun.SeriesArtifactType,
			keyRequired:  []string{"benchmark", "target", "target_endpoint_contract", "target_topology", "protocol_digest", "trials"},
		},
		{
			file:         "benchmark-scenario-pack.schema.json",
			goType:       reflect.TypeOf(benchmarkrun.ScenarioPackInventory{}),
			versionField: "schema_version",
			version:      benchmarkrun.ScenarioPackSchemaVersion,
			artifactType: benchmarkrun.ScenarioPackArtifactType,
			keyRequired:  []string{"id", "version", "digest", "manifest", "files"},
		},
		{
			file:         "benchmark-trial.schema.json",
			goType:       reflect.TypeOf(benchmarkrun.Trial{}),
			versionField: "schema_version",
			version:      benchmarkrun.TrialSchemaVersion,
			artifactType: benchmarkrun.TrialArtifactType,
			keyRequired:  []string{"trial", "run_ref", "raw_logs", "phase_timeline", "primary_metric"},
		},
		{
			file:         "benchmark-environment.schema.json",
			goType:       reflect.TypeOf(benchmarkrun.Environment{}),
			versionField: "schema_version",
			version:      benchmarkrun.EnvironmentSchemaVersion,
			artifactType: benchmarkrun.EnvironmentArtifactType,
			keyRequired:  []string{"runtime", "target", "target_endpoint_contract", "target_topology", "pg_config_digest", "qualification", "digest"},
		},
		{
			file:         "benchmark-comparison.schema.json",
			goType:       reflect.TypeOf(benchmarkcompare.Comparison{}),
			versionField: "schema_version",
			version:      benchmarkcompare.SchemaVersion,
			artifactType: benchmarkcompare.ArtifactType,
			keyRequired:  []string{"analysis_version", "comparison_key_digest", "status", "decision"},
		},
		{
			file:         "benchmark-bundle-inventory.schema.json",
			goType:       reflect.TypeOf(benchmarkartifact.BundleInventory{}),
			versionField: "schema_version",
			version:      benchmarkartifact.BundleSchemaVersion,
			artifactType: benchmarkartifact.BundleArtifactType,
			keyRequired:  []string{"series_run_id", "series_ref", "files"},
		},
	}

	seenIDs := make(map[string]string, len(tests))
	seenVersions := make(map[string]string, len(tests))
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", "schemas", test.file))
			if err != nil {
				t.Fatal(err)
			}
			var schema schemaDocument
			if err := json.Unmarshal(content, &schema); err != nil {
				t.Fatalf("parse JSON Schema: %v", err)
			}
			if schema.Schema != draft202012 {
				t.Fatalf("$schema = %q, want %q", schema.Schema, draft202012)
			}
			if schema.ID == "" {
				t.Fatal("$id must not be empty")
			}
			if previous, exists := seenIDs[schema.ID]; exists {
				t.Fatalf("duplicate $id also used by %s: %s", previous, schema.ID)
			}
			seenIDs[schema.ID] = test.file
			if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
				t.Fatal("root additionalProperties must be false")
			}

			versions := test.versions
			if len(versions) == 0 {
				versions = []string{test.version}
			}
			versionValues := propertyConstOrEnum(t, schema, test.versionField)
			if !reflect.DeepEqual(versionValues, versions) {
				t.Fatalf("%s values = %q, want %q", test.versionField, versionValues, versions)
			}
			for _, version := range versionValues {
				if previous, exists := seenVersions[version]; exists {
					t.Fatalf("duplicate schema version also used by %s: %s", previous, version)
				}
				seenVersions[version] = test.file
			}
			if test.artifactType != "" {
				if got := propertyConst(t, schema, "artifact_type"); got != test.artifactType {
					t.Fatalf("artifact_type const = %q, want %q", got, test.artifactType)
				}
			}

			assertSchemaFieldsMatchGoType(t, schema, test.goType)
			for _, key := range append([]string{test.versionField}, test.keyRequired...) {
				if !contains(schema.Required, key) {
					t.Errorf("required does not contain key contract field %q", key)
				}
			}
		})
	}
}

func TestBenchmarkPhaseTimelineSchemaDefinesExactLifecycle(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "benchmark-phase-timeline.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       map[string]json.RawMessage `json:"$defs"`
		AllOf      []json.RawMessage          `json:"allOf"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse benchmark phase timeline schema: %v", err)
	}

	var events struct {
		MinItems    int `json:"minItems"`
		MaxItems    int `json:"maxItems"`
		PrefixItems []struct {
			Ref string `json:"$ref"`
		} `json:"prefixItems"`
		Items *bool `json:"items"`
	}
	if err := json.Unmarshal(document.Properties["events"], &events); err != nil {
		t.Fatalf("parse events schema: %v", err)
	}
	if events.MinItems != len(benchmarkphase.OrderedNames) || events.MaxItems != len(benchmarkphase.OrderedNames) || len(events.PrefixItems) != len(benchmarkphase.OrderedNames) {
		t.Fatalf("events schema is not an exact lifecycle tuple: %#v", events)
	}
	if events.Items == nil || *events.Items {
		t.Fatal("events schema must reject entries after the ordered prefixItems")
	}
	if len(document.AllOf) != 15 {
		t.Fatalf("phase schema has %d lifecycle transition constraints, want 15", len(document.AllOf))
	}

	for index, name := range benchmarkphase.OrderedNames {
		wantDefinition := name + "Event"
		if name == "preflight" {
			wantDefinition = "preflightEvent"
		}
		wantRef := "#/$defs/" + wantDefinition
		if events.PrefixItems[index].Ref != wantRef {
			t.Fatalf("phase %d schema ref = %q, want %q", index+1, events.PrefixItems[index].Ref, wantRef)
		}
		definition, exists := document.Defs[wantDefinition]
		if !exists {
			t.Fatalf("phase schema definition %q is missing", wantDefinition)
		}
		sequence, phaseName := phaseIdentity(t, definition)
		if sequence != index+1 || phaseName != name {
			t.Fatalf("phase definition %q identity = %d/%q, want %d/%q", wantDefinition, sequence, phaseName, index+1, name)
		}
	}

	var eventContract struct {
		Required []string `json:"required"`
		AllOf    []any    `json:"allOf"`
	}
	if err := json.Unmarshal(document.Defs["event"], &eventContract); err != nil {
		t.Fatalf("parse shared phase event schema: %v", err)
	}
	for _, field := range []string{"duration_ns", "duration_ms"} {
		if !contains(eventContract.Required, field) {
			t.Fatalf("shared phase event schema does not require %q", field)
		}
	}
	if len(eventContract.AllOf) == 0 {
		t.Fatal("shared phase event schema does not conditionally require failure/skip reasons")
	}

	compact := strings.NewReplacer(" ", "", "\n", "", "\t", "")
	cleanup := compact.Replace(string(document.Defs["cleanupEvent"]))
	if !strings.Contains(cleanup, `"enum":["passed","failed"]`) {
		t.Fatal("cleanup schema must allow only passed or failed")
	}
	measure := compact.Replace(string(document.Defs["measureEvent"]))
	if !strings.Contains(measure, `"duration_ns":{"minimum":1}`) {
		t.Fatal("passed measure schema must require a positive nanosecond duration")
	}
}

func TestPassedBenchmarkTrialSchemaRequiresPostgresMetricsAndPhaseJournal(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "benchmark-trial.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(string(content))
	if !strings.Contains(compact, `"postgres_metrics":{"$ref":"benchmark-postgres-metrics.schema.json"}`) ||
		!strings.Contains(compact, `"phase_journal":{"$ref":"#/$defs/artifactRef"}`) ||
		!strings.Contains(compact, `"then":{"required":["postgres_metrics","phase_journal"]}`) {
		t.Fatal("passed trial schema does not bind the normalized PostgreSQL sampler summary and linked phase journal")
	}
}

func phaseIdentity(t *testing.T, definition json.RawMessage) (int, string) {
	t.Helper()
	var phase struct {
		AllOf []json.RawMessage `json:"allOf"`
	}
	if err := json.Unmarshal(definition, &phase); err != nil {
		t.Fatalf("parse phase definition: %v", err)
	}
	for _, fragment := range phase.AllOf {
		var identity struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(fragment, &identity); err != nil {
			t.Fatalf("parse phase identity fragment: %v", err)
		}
		sequenceRaw, hasSequence := identity.Properties["sequence"]
		nameRaw, hasName := identity.Properties["name"]
		if !hasSequence || !hasName {
			continue
		}
		var sequence struct {
			Const int `json:"const"`
		}
		var name struct {
			Const string `json:"const"`
		}
		if err := json.Unmarshal(sequenceRaw, &sequence); err != nil {
			t.Fatalf("parse phase sequence const: %v", err)
		}
		if err := json.Unmarshal(nameRaw, &name); err != nil {
			t.Fatalf("parse phase name const: %v", err)
		}
		return sequence.Const, name.Const
	}
	t.Fatal("phase definition has no sequence/name identity")
	return 0, ""
}

func assertSchemaFieldsMatchGoType(t *testing.T, schema schemaDocument, goType reflect.Type) {
	t.Helper()
	fields := make(map[string]bool, goType.NumField())
	for index := 0; index < goType.NumField(); index++ {
		field := goType.Field(index)
		tag := field.Tag.Get("json")
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		omitempty := false
		for _, option := range parts[1:] {
			omitempty = omitempty || option == "omitempty"
		}
		fields[name] = !omitempty
		if _, exists := schema.Properties[name]; !exists {
			t.Errorf("schema properties missing Go JSON field %q", name)
		}
		if !omitempty && !contains(schema.Required, name) {
			t.Errorf("required missing non-omitempty Go JSON field %q", name)
		}
		if omitempty && contains(schema.Required, name) {
			t.Errorf("required contains omitempty Go JSON field %q", name)
		}
	}
	for name := range schema.Properties {
		if _, exists := fields[name]; !exists {
			t.Errorf("schema property %q has no Go JSON field", name)
		}
	}
}

func propertyConst(t *testing.T, schema schemaDocument, name string) string {
	t.Helper()
	content, exists := schema.Properties[name]
	if !exists {
		t.Fatalf("schema properties missing %q", name)
	}
	var property struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(content, &property); err != nil {
		t.Fatalf("parse %s property: %v", name, err)
	}
	if property.Const == "" {
		t.Fatalf("%s property must declare a non-empty const", name)
	}
	return property.Const
}

func propertyConstOrEnum(t *testing.T, schema schemaDocument, name string) []string {
	t.Helper()
	content, exists := schema.Properties[name]
	if !exists {
		t.Fatalf("schema properties missing %q", name)
	}
	var property struct {
		Const string   `json:"const"`
		Enum  []string `json:"enum"`
	}
	if err := json.Unmarshal(content, &property); err != nil {
		t.Fatalf("parse %s property: %v", name, err)
	}
	if property.Const != "" && len(property.Enum) == 0 {
		return []string{property.Const}
	}
	if property.Const == "" && len(property.Enum) > 0 {
		return property.Enum
	}
	t.Fatalf("%s property must declare exactly one non-empty const or enum", name)
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
