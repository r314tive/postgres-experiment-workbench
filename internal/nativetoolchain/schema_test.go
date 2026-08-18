package nativetoolchain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaTracksManifestContract(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "native-toolchain.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema               string                     `json:"$schema"`
		ID                   string                     `json:"$id"`
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" || schema.ID == "" || schema.AdditionalProperties {
		t.Fatalf("schema root is not a closed draft 2020-12 contract: %#v", schema)
	}
	fields := reflect.TypeOf(Manifest{})
	for index := 0; index < fields.NumField(); index++ {
		name := strings.Split(fields.Field(index).Tag.Get("json"), ",")[0]
		if _, ok := schema.Properties[name]; !ok || !containsString(schema.Required, name) {
			t.Fatalf("schema does not require manifest field %q", name)
		}
	}
	for name := range schema.Properties {
		found := false
		for index := 0; index < fields.NumField(); index++ {
			found = found || strings.Split(fields.Field(index).Tag.Get("json"), ",")[0] == name
		}
		if !found {
			t.Fatalf("schema property %q has no Go manifest field", name)
		}
	}
	var binarySchema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema.Defs["binary"], &binarySchema); err != nil {
		t.Fatal(err)
	}
	if binarySchema.AdditionalProperties {
		t.Fatal("binary schema is not closed")
	}
	binaryFields := reflect.TypeOf(Binary{})
	if len(binarySchema.Properties) != binaryFields.NumField() {
		t.Fatalf("binary schema field count = %d, want %d", len(binarySchema.Properties), binaryFields.NumField())
	}
	for index := 0; index < binaryFields.NumField(); index++ {
		name := strings.Split(binaryFields.Field(index).Tag.Get("json"), ",")[0]
		if _, ok := binarySchema.Properties[name]; !ok || !containsString(binarySchema.Required, name) {
			t.Fatalf("binary schema does not require Go field %q", name)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
