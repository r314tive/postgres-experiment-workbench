package speccatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type schemaDocument map[string]interface{}

func RenderSchema(w io.Writer, kind string) error {
	references, err := References(kind)
	if err != nil {
		return err
	}

	var document schemaDocument
	if kind == "" || kind == "all" {
		defs := make(map[string]interface{}, len(references))
		for _, reference := range references {
			defs[reference.Kind] = schemaForReference(reference)
		}
		document = schemaDocument{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id":     "https://github.com/r314tive/postgres-experiment-workbench/schemas/env-specs.schema.json",
			"title":   "PostgreSQL Experiment Workbench env spec schemas",
			"$defs":   defs,
		}
	} else if len(references) == 1 {
		document = schemaForReference(references[0])
	} else {
		return fmt.Errorf("expected one schema reference, got %d", len(references))
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(document)
}

func schemaForReference(reference KindReference) schemaDocument {
	properties := make(map[string]interface{}, len(reference.Fields))
	var required []string
	for _, field := range reference.Fields {
		properties[field.Key] = schemaForField(field)
		if field.Requirement == "required" {
			required = append(required, field.Key)
		}
	}

	schema := schemaDocument{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                fmt.Sprintf("%s env spec", reference.Kind),
		"description":          reference.Summary,
		"type":                 "object",
		"additionalProperties": true,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	if len(reference.Notes) > 0 {
		schema["$comment"] = strings.Join(reference.Notes, " ")
	}
	return schema
}

func schemaForField(field FieldReference) schemaDocument {
	schema := schemaDocument{
		"type":        "string",
		"description": field.Description,
	}
	if field.Default != "" {
		schema["default"] = field.Default
	}
	if field.Requirement != "" {
		schema["x-workbench-requirement"] = field.Requirement
	}
	if allowed := enumValues(field.Allowed); len(allowed) > 0 {
		schema["enum"] = allowed
	}
	return schema
}

func enumValues(allowed string) []string {
	if allowed == "" || strings.Contains(allowed, "positive integer") {
		return nil
	}
	values := strings.Split(allowed, ",")
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.Contains(value, " ") {
			return nil
		}
		out = append(out, value)
	}
	return out
}
