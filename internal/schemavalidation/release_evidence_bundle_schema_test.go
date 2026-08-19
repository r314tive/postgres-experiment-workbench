package schemavalidation

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseEvidenceBundleInventorySchema(t *testing.T) {
	registry, err := CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	const schema = "release-evidence-bundle-inventory.schema.json"
	if err := registry.ValidateJSON(schema, marshalBundleInventoryFixture(t, validBundleInventoryFixture())); err != nil {
		t.Fatalf("valid release evidence bundle inventory was rejected: %v", err)
	}
	passed := validBundleInventoryFixture()
	passedOutcome := passed["outcome"].(map[string]any)
	passedOutcome["status"] = "passed"
	passedOutcome["decision"] = "go"
	passedOutcome["readiness_status"] = "passed"
	passedOutcome["readiness_decision"] = "go"
	passedOutcome["assurance_status"] = "authorization-eligible"
	passedOutcome["authorization_eligible"] = true
	if err := registry.ValidateJSON(schema, marshalBundleInventoryFixture(t, passed)); err != nil {
		t.Fatalf("valid authorization-eligible bundle outcome was rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "unknown top-level property",
			mutate: func(inventory map[string]any) {
				inventory["unbounded"] = true
			},
		},
		{
			name: "noncanonical index path",
			mutate: func(inventory map[string]any) {
				inventory["files"].([]any)[0].(map[string]any)["path"] = "index-r01.json"
			},
		},
		{
			name: "mode drift",
			mutate: func(inventory map[string]any) {
				inventory["files"].([]any)[0].(map[string]any)["mode"] = 0o600
			},
		},
		{
			name: "revision above bundle bound",
			mutate: func(inventory map[string]any) {
				inventory["head_revision"] = 256
			},
		},
		{
			name: "index above byte bound",
			mutate: func(inventory map[string]any) {
				inventory["files"].([]any)[0].(map[string]any)["size"] = 2097153
			},
		},
		{
			name: "total above byte bound",
			mutate: func(inventory map[string]any) {
				inventory["total_size_bytes"] = 67108865
			},
		},
		{
			name: "more than 256 revisions",
			mutate: func(inventory map[string]any) {
				files := make([]any, 257)
				for revision := range files {
					files[revision] = map[string]any{
						"path":     "index-r" + strconv.Itoa(revision) + ".json",
						"revision": revision,
						"size":     4096,
						"digest":   "sha256:" + strings.Repeat("f", 64),
						"mode":     0o644,
					}
				}
				inventory["file_count"] = len(files)
				inventory["files"] = files
			},
		},
		{
			name: "effective outcome mismatch",
			mutate: func(inventory map[string]any) {
				inventory["outcome"].(map[string]any)["status"] = "passed"
			},
		},
		{
			name: "ineligible assurance claims authorization",
			mutate: func(inventory map[string]any) {
				inventory["outcome"].(map[string]any)["authorization_eligible"] = true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := validBundleInventoryFixture()
			test.mutate(fixture)
			if err := registry.ValidateJSON(schema, marshalBundleInventoryFixture(t, fixture)); err == nil {
				t.Fatal("invalid release evidence bundle inventory was accepted")
			}
		})
	}
}

func validBundleInventoryFixture() map[string]any {
	digest := func(character string) string { return "sha256:" + strings.Repeat(character, 64) }
	return map[string]any{
		"schema_version": "pgworkbench.release-evidence-bundle/v1",
		"artifact_type":  "pgworkbench.release-evidence-bundle",
		"candidate": map[string]any{
			"version":           "0.2.6",
			"tag":               "v0.2.6",
			"git_commit":        strings.Repeat("a", 40),
			"asset_fingerprint": strings.Repeat("b", 64),
			"scenario_pack": map[string]any{
				"id":      "postgres-experiment-workbench",
				"version": "0.2.6",
				"digest":  digest("c"),
			},
		},
		"head_index":       "index-r0.json",
		"head_revision":    0,
		"head_digest":      digest("d"),
		"file_count":       1,
		"total_size_bytes": 4096,
		"tree_digest":      digest("e"),
		"outcome": map[string]any{
			"status":                 "open",
			"decision":               "no-go",
			"readiness_status":       "open",
			"readiness_decision":     "no-go",
			"assurance_status":       "operator-attested-not-verified",
			"authorization_eligible": false,
		},
		"files": []any{map[string]any{
			"path":     "index-r0.json",
			"revision": 0,
			"size":     4096,
			"digest":   digest("f"),
			"mode":     0o644,
		}},
	}
}

func marshalBundleInventoryFixture(t *testing.T, fixture map[string]any) []byte {
	t.Helper()
	content, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
