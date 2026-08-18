package benchmarkdrivers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuiltinRegistryIsPinnedAndValid(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inspection.Digest, "sha256:") || len(inspection.Registry.Drivers) != 3 {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}
	benchbase, err := inspection.Registry.Find("benchbase-postgresql-33c0047")
	if err != nil {
		t.Fatal(err)
	}
	if benchbase.RefType != "commit" || benchbase.Commit != "33c00473807ebd49304d114a6d769d2d2b2bbb34" || benchbase.SourceToBinaryAttested {
		t.Fatalf("unexpected BenchBase pin: %#v", benchbase)
	}
	sysbench, err := inspection.Registry.Find("sysbench-postgresql-1.0.20")
	if err != nil {
		t.Fatal(err)
	}
	if sysbench.TagObject == sysbench.Commit || sysbench.TagObject == "" {
		t.Fatalf("annotated tag identity was not retained: %#v", sysbench)
	}
}

func TestRegistryRejectsUnknownFieldsAndFalseAssurance(t *testing.T) {
	valid := `{
  "schema_version":"pgworkbench.benchmark-driver-lock/v1",
  "artifact_type":"pgworkbench.benchmark-driver-lock",
  "drivers":[{
    "id":"sysbench-postgresql-1.0.20","adapter":"sysbench1","display_version":"1.0.20",
    "repository":"https://github.com/akopytov/sysbench","ref_type":"tag","ref":"1.0.20",
    "tag_object":"f3da4313f8177d072b7150be5d00e4adfd15945c",
    "commit":"ebf1c90da05dea94648165e4f149abc20c979557","entrypoint":"sysbench",
    "result_format":"sysbench-1.0-console-summary","parser":"sysbench-console-summary/v1.1",
    "runtime_support":["native"],"workloads":["oltp_read_write/postgresql"],
    "binary_distributed_by_project":false,"source_to_binary_attested":false,"decision_eligible":false
  }]
}`
	if _, err := Parse([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(strings.Replace(valid, `"decision_eligible":false`, `"decision_eligible":true`, 1))); err == nil || !strings.Contains(err.Error(), "decision eligibility") {
		t.Fatalf("expected false-assurance rejection, got %v", err)
	}
	if _, err := Parse([]byte(strings.Replace(valid, `"schema_version"`, `"unknown":1,"schema_version"`, 1))); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if _, err := Parse([]byte(strings.Replace(valid, `"adapter":"sysbench1"`, `"adapter":"sysbench1","adapter":"hammerdb6"`, 1))); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate key rejection, got %v", err)
	}
}

func TestLoadRejectsSymlinkLock(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "compatibility"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(LockPath))); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestDriverLockSchemaTracksRegistry(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "benchmark-driver-lock.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
		t.Fatal("driver lock schema root must be a closed draft 2020-12 object")
	}
	properties := schema["properties"].(map[string]any)
	if properties["schema_version"].(map[string]any)["const"] != SchemaVersion || properties["artifact_type"].(map[string]any)["const"] != ArtifactType {
		t.Fatal("driver lock schema identity does not match Go constants")
	}
	assertSchemaFields(t, properties, reflect.TypeOf(Registry{}))
	definitions := schema["$defs"].(map[string]any)
	driverSchema := definitions["driver"].(map[string]any)
	if driverSchema["additionalProperties"] != false {
		t.Fatal("driver item schema is not closed")
	}
	assertSchemaFields(t, driverSchema["properties"].(map[string]any), reflect.TypeOf(Driver{}))
}

func TestRenderSurfacesAssuranceBoundaryAndExactIdentity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var list bytes.Buffer
	if err := Render(&list, inspection); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"benchbase-postgresql-33c0047",
		"source identities and parser contracts",
		"does not attest installed binaries",
	} {
		if !strings.Contains(list.String(), expected) {
			t.Fatalf("rendered lock is missing %q: %s", expected, list.String())
		}
	}
	hammerDB, err := inspection.Registry.Find("hammerdb-postgresql-6.0")
	if err != nil {
		t.Fatal(err)
	}
	var detail bytes.Buffer
	if err := RenderDriver(&detail, inspection, hammerDB); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Tag object: 18f3e075f4d94fa1dcc3b9f11e743928ef0f7694",
		"Commit: d33f879aec858063edd17aa2daa46db03abb2bae",
		"Source-to-binary attested: false",
		"Decision eligible: false",
	} {
		if !strings.Contains(detail.String(), expected) {
			t.Fatalf("rendered driver is missing %q: %s", expected, detail.String())
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
