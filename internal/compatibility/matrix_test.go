package compatibility

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatibilityJSONSchemaMatchesContract(t *testing.T) {
	path := filepath.Join("..", "..", "schemas", "compatibility-matrix.schema.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("unexpected JSON Schema dialect: %#v", schema["$schema"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema does not define properties")
	}
	cells, ok := properties["cells"].(map[string]any)
	if !ok || cells["uniqueItems"] != true {
		t.Fatalf("schema must reject byte-identical duplicate cells: %#v", cells)
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema does not define $defs")
	}
	cell, ok := definitions["cell"].(map[string]any)
	if !ok || cell["additionalProperties"] != false {
		t.Fatalf("compatibility cells must reject unknown fields: %#v", cell)
	}
	required, ok := cell["required"].([]any)
	if !ok || len(required) != 8 {
		t.Fatalf("compatibility cell must require exactly eight contract fields: %#v", cell["required"])
	}
	archivePlatform, ok := definitions["archive_platform"].(map[string]any)
	if !ok || archivePlatform["additionalProperties"] != false {
		t.Fatalf("archive platform must reject unknown fields: %#v", archivePlatform)
	}
	archiveRequired, ok := archivePlatform["required"].([]any)
	if !ok || len(archiveRequired) != 4 {
		t.Fatalf("archive platform must require exactly four contract fields: %#v", archivePlatform["required"])
	}
}

func TestRepositoryMatrixIsValidAndCanonical(t *testing.T) {
	path := filepath.Join("..", "..", DefaultPath)
	matrix, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix.Cells) != 7 {
		t.Fatalf("unexpected repository compatibility cell count: %d", len(matrix.Cells))
	}
	expectedPlatforms := []ArchivePlatform{
		{OS: "darwin", Arch: "amd64", VerificationScope: "compile-package-only", RuntimeCells: []string{}},
		{OS: "darwin", Arch: "arm64", VerificationScope: "runtime-gated", RuntimeCells: []string{"native-darwin-arm64-pg16-single"}},
		{OS: "linux", Arch: "amd64", VerificationScope: "runtime-gated", RuntimeCells: []string{
			"docker-linux-amd64-pg15-to-pg16-multi-version-upgrade",
			"docker-linux-amd64-pg16-logical-replication",
			"docker-linux-amd64-pg16-pgbouncer",
			"docker-linux-amd64-pg16-primary-replica",
			"docker-linux-amd64-pg16-single",
			"native-linux-amd64-pg16-single",
		}},
		{OS: "linux", Arch: "arm64", VerificationScope: "compile-package-only", RuntimeCells: []string{}},
	}
	if !archivePlatformsEqual(matrix.ArchivePlatforms, expectedPlatforms) {
		t.Fatalf("repository release archive platform contract drifted: got %#v want %#v", matrix.ArchivePlatforms, expectedPlatforms)
	}
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makefile), "RELEASE_PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64\n") {
		t.Fatal("Makefile release targets drifted from the machine-readable archive platform contract")
	}

	var rendered bytes.Buffer
	if err := RenderJSON(&rendered, matrix); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.String() != string(content) {
		t.Fatalf("repository matrix is not in canonical order; render it with RenderJSON")
	}
}

func archivePlatformsEqual(left, right []ArchivePlatform) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].OS != right[index].OS || left[index].Arch != right[index].Arch || left[index].VerificationScope != right[index].VerificationScope {
			return false
		}
		if strings.Join(left[index].RuntimeCells, "\x00") != strings.Join(right[index].RuntimeCells, "\x00") {
			return false
		}
	}
	return true
}

func TestDecodeAndDeterministicRender(t *testing.T) {
	matrix, err := Decode(strings.NewReader(`{
  "schema_version": "pgworkbench.compatibility-matrix/v2",
  "archive_platforms": [
    {"os":"darwin","arch":"arm64","verification_scope":"runtime-gated","runtime_cells":["native-darwin-arm64-pg16-single"]},
    {"os":"linux","arch":"amd64","verification_scope":"runtime-gated","runtime_cells":["docker-linux-amd64-pg16-single"]}
  ],
  "cells": [
    {"id":"native-darwin-arm64-pg16-single","runtime":"native","topology":"single","os":"darwin","arch":"arm64","postgres":"16","support_level":"candidate","gate":"native-integration"},
    {"id":"docker-linux-amd64-pg16-single","runtime":"docker","topology":"single","os":"linux","arch":"amd64","postgres":"16","support_level":"candidate","gate":"docker-integration"}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}

	var first, second bytes.Buffer
	if err := RenderJSON(&first, matrix); err != nil {
		t.Fatal(err)
	}
	if err := RenderJSON(&second, matrix); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("JSON rendering is not deterministic:\n%s\n%s", first.String(), second.String())
	}
	cellsJSON := first.String()[strings.Index(first.String(), "\n  \"cells\": ["):]
	if strings.Index(cellsJSON, "docker-linux") > strings.Index(cellsJSON, "native-darwin") {
		t.Fatalf("JSON cells were not sorted by id: %s", cellsJSON)
	}

	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, matrix); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "does not claim that the gate has passed") {
		t.Fatalf("missing assurance boundary: %s", markdown.String())
	}
	if !strings.Contains(markdown.String(), "`compile-package-only` archives have no runtime support claim") {
		t.Fatalf("missing release archive assurance boundary: %s", markdown.String())
	}
	cellTableMarker := "\n| ID | Runtime | Topology | OS | Arch | PostgreSQL | Support level | Required gate |"
	cellTableIndex := strings.Index(markdown.String(), cellTableMarker)
	if cellTableIndex < 0 {
		t.Fatalf("Markdown cell table is missing: %s", markdown.String())
	}
	cellTable := markdown.String()[cellTableIndex:]
	if strings.Index(cellTable, "docker-linux") > strings.Index(cellTable, "native-darwin") {
		t.Fatalf("Markdown cells were not sorted by id: %s", cellTable)
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	tests := []string{
		`{"schema_version":"pgworkbench.compatibility-matrix/v2","archive_platforms":[{"os":"linux","arch":"amd64","verification_scope":"runtime-gated","runtime_cells":["docker-linux-amd64-pg16-single"]}],"cells":[{"id":"docker-linux-amd64-pg16-single","runtime":"docker","topology":"single","os":"linux","arch":"amd64","postgres":"16","support_level":"candidate","gate":"docker-integration","passed":true}]}`,
		`{"schema_version":"pgworkbench.compatibility-matrix/v2","archive_platforms":[{"os":"linux","arch":"amd64","verification_scope":"runtime-gated","runtime_cells":["docker-linux-amd64-pg16-single"]}],"cells":[{"id":"docker-linux-amd64-pg16-single","runtime":"docker","topology":"single","os":"linux","arch":"amd64","postgres":"16","support_level":"candidate","gate":"docker-integration"}]} {}`,
	}
	for _, input := range tests {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatalf("expected strict JSON rejection: %s", input)
		}
	}
}

func TestValidateRejectsDuplicateIDAndCoordinates(t *testing.T) {
	base := Cell{
		ID:           "docker-linux-amd64-pg16-single",
		Runtime:      "docker",
		Topology:     "single",
		OS:           "linux",
		Arch:         "amd64",
		Postgres:     "16",
		SupportLevel: "candidate",
		Gate:         "docker-integration",
	}

	duplicateID := base
	duplicateID.Topology = "pgbouncer"
	if err := Validate(matrixWithCells(base, duplicateID)); err == nil || !strings.Contains(err.Error(), "duplicate compatibility cell id") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}

	duplicateCoordinates := base
	duplicateCoordinates.ID = "duplicate-coordinates"
	if err := Validate(matrixWithCells(base, duplicateCoordinates)); err == nil || !strings.Contains(err.Error(), "duplicate compatibility coordinates") {
		t.Fatalf("expected duplicate coordinates error, got %v", err)
	}
}

func TestValidateRejectsInvalidEnumsAndNativeTopology(t *testing.T) {
	valid := Cell{
		ID:           "native-linux-amd64-pg16-single",
		Runtime:      "native",
		Topology:     "single",
		OS:           "linux",
		Arch:         "amd64",
		Postgres:     "16",
		SupportLevel: "candidate",
		Gate:         "native-integration",
	}
	tests := []struct {
		name string
		edit func(*Cell)
		want string
	}{
		{name: "runtime", edit: func(cell *Cell) { cell.Runtime = "podman" }, want: "invalid runtime"},
		{name: "topology", edit: func(cell *Cell) { cell.Topology = "cluster" }, want: "invalid topology"},
		{name: "os", edit: func(cell *Cell) { cell.OS = "windows" }, want: "invalid os"},
		{name: "arch", edit: func(cell *Cell) { cell.Arch = "386" }, want: "invalid arch"},
		{name: "postgres", edit: func(cell *Cell) { cell.Postgres = "latest" }, want: "invalid postgres major"},
		{name: "support", edit: func(cell *Cell) { cell.SupportLevel = "supported" }, want: "invalid support_level"},
		{name: "gate", edit: func(cell *Cell) { cell.Gate = "passed" }, want: "invalid gate"},
		{name: "native topology", edit: func(cell *Cell) { cell.Topology = "primary-replica" }, want: "native runtime supports only"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cell := valid
			test.edit(&cell)
			err := Validate(matrixWithCells(cell))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestValidateSupportLevelGateContract(t *testing.T) {
	unsupported := Cell{
		ID:           "docker-linux-amd64-pg16-single",
		Runtime:      "docker",
		Topology:     "single",
		OS:           "linux",
		Arch:         "amd64",
		Postgres:     "16",
		SupportLevel: "unsupported",
		Gate:         "docker-integration",
	}
	if err := Validate(matrixWithCells(unsupported)); err == nil || !strings.Contains(err.Error(), "not-applicable") {
		t.Fatalf("expected unsupported/gate mismatch, got %v", err)
	}

	unsupported.Gate = "not-applicable"
	if err := Validate(matrixWithCells(unsupported)); err != nil {
		t.Fatalf("valid unsupported cell rejected: %v", err)
	}

	candidate := unsupported
	candidate.SupportLevel = "candidate"
	if err := Validate(matrixWithCells(candidate)); err == nil || !strings.Contains(err.Error(), "must name a verification gate") {
		t.Fatalf("expected candidate/gate mismatch, got %v", err)
	}
}

func TestValidateArchivePlatformContract(t *testing.T) {
	cell := Cell{
		ID: "native-linux-amd64-pg16-single", Runtime: "native", Topology: "single",
		OS: "linux", Arch: "amd64", Postgres: "16", SupportLevel: "candidate", Gate: "native-integration",
	}
	tests := []struct {
		name     string
		platform ArchivePlatform
		want     string
	}{
		{name: "runtime gate without cell", platform: ArchivePlatform{OS: "linux", Arch: "amd64", VerificationScope: "runtime-gated"}, want: "must name at least one runtime cell"},
		{name: "compile only with cell", platform: ArchivePlatform{OS: "linux", Arch: "amd64", VerificationScope: "compile-package-only", RuntimeCells: []string{cell.ID}}, want: "cannot name runtime cells"},
		{name: "unknown cell", platform: ArchivePlatform{OS: "linux", Arch: "amd64", VerificationScope: "runtime-gated", RuntimeCells: []string{"missing-cell"}}, want: "references unknown runtime cell"},
		{name: "wrong platform", platform: ArchivePlatform{OS: "darwin", Arch: "amd64", VerificationScope: "runtime-gated", RuntimeCells: []string{cell.ID}}, want: "has coordinates linux/amd64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(Matrix{SchemaVersion: SchemaVersion, ArchivePlatforms: []ArchivePlatform{test.platform}, Cells: []Cell{cell}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func matrixWithCells(cells ...Cell) Matrix {
	platforms := make([]ArchivePlatform, 0, len(cells))
	byPlatform := make(map[string]int)
	for _, cell := range cells {
		if cell.SupportLevel != "candidate" {
			continue
		}
		key := cell.OS + "/" + cell.Arch
		index, exists := byPlatform[key]
		if !exists {
			index = len(platforms)
			byPlatform[key] = index
			platforms = append(platforms, ArchivePlatform{OS: cell.OS, Arch: cell.Arch, VerificationScope: "runtime-gated"})
		}
		platforms[index].RuntimeCells = append(platforms[index].RuntimeCells, cell.ID)
	}
	if len(platforms) == 0 {
		platforms = append(platforms, ArchivePlatform{OS: "linux", Arch: "arm64", VerificationScope: "compile-package-only", RuntimeCells: []string{}})
	}
	return Matrix{SchemaVersion: SchemaVersion, ArchivePlatforms: platforms, Cells: cells}
}
