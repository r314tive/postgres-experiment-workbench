package experimentrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestExperimentRunResultSchemaTracksDeadlineEvidence(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "schemas", "experiment-run-result.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
		AllOf      []struct {
			If struct {
				Required []string `json:"required"`
			} `json:"if"`
			Then struct {
				Required []string `json:"required"`
			} `json:"then"`
		} `json:"allOf"`
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
	if _, ok := schema.Properties["containment_status"]; !ok {
		t.Fatal("experiment result schema missing containment_status")
	}
	for _, implication := range []struct{ from, to string }{
		{from: "termination_signal", to: "containment_status"},
		{from: "containment_status", to: "termination_signal"},
	} {
		found := false
		for _, condition := range schema.AllOf {
			if containsSchemaString(condition.If.Required, implication.from) && containsSchemaString(condition.Then.Required, implication.to) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("experiment result schema must require %q when %q is present", implication.to, implication.from)
		}
	}
}

func TestExperimentRunResultSchemaRejectsPassedCleanupResult(t *testing.T) {
	schemaDir := filepath.Join("..", "..", "schemas")
	registry, err := schemavalidation.CompileDir(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	result := Result{
		SchemaVersion:      SchemaVersion,
		ExperimentSpec:     "smoke",
		ExperimentName:     "smoke",
		SpecPath:           "/tmp/smoke.env",
		SpecSHA256:         strings.Repeat("0", 64),
		Runtime:            "native",
		Topology:           "single",
		RunID:              "schema-cleanup",
		RunDir:             "/tmp/schema-cleanup",
		EngineVersion:      "unverified",
		EngineCommit:       "unverified",
		Command:            []string{"run"},
		StartedAt:          "2026-08-14T00:00:00Z",
		FinishedAt:         "2026-08-14T00:00:01Z",
		DurationMS:         1000,
		ExitCode:           0,
		ExecutionTimeoutMS: 1000,
		CleanupGraceMS:     100,
		TimedOut:           false,
		TerminationSignal:  "SIGTERM",
		ContainmentStatus:  ContainmentStatusConfirmed,
		Status:             "passed",
	}
	content, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("experiment-run-result.schema.json", content); err == nil {
		t.Fatalf("passed result with cleanup metadata unexpectedly validated: %s", content)
	}
	result.Status = "failed"
	result.ExitCode = -1
	content, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateJSON("experiment-run-result.schema.json", content); err != nil {
		t.Fatalf("failed result with cleanup metadata was rejected: %v", err)
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
