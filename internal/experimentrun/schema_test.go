package experimentrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExperimentRunResultSchemaTracksDeadlineEvidence(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "experiment-run-result.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"execution_timeout_ms", "cleanup_grace_ms", "timed_out"} {
		if _, ok := schema.Properties[key]; !ok || !containsSchemaString(schema.Required, key) {
			t.Fatalf("experiment result schema must require %q", key)
		}
	}
	if _, ok := schema.Properties["termination_signal"]; !ok {
		t.Fatal("experiment result schema missing termination_signal")
	}
}

func containsSchemaString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
