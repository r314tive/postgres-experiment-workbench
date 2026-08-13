package schemavalidation

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	Draft202012       = "https://json-schema.org/draft/2020-12/schema"
	canonicalIDPrefix = "https://github.com/r314tive/postgres-experiment-workbench/schemas/"
)

// Registry is a fully compiled, network-independent set of repository schemas.
type Registry struct {
	schemas map[string]*jsonschema.Schema
	names   []string
}

// CompileDir parses, metaschema-validates, resolves, and compiles every
// *.schema.json file in schemaDir as JSON Schema Draft 2020-12.
func CompileDir(schemaDir string) (*Registry, error) {
	entries, err := os.ReadDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("read schema directory: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseRegexpEngine(compileECMAScriptRegexp)

	type resource struct {
		name string
		id   string
		doc  any
	}
	resources := make([]resource, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}

		path := filepath.Join(schemaDir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(content))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		object, ok := doc.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: schema root must be an object", entry.Name())
		}
		dialect, ok := object["$schema"].(string)
		if !ok || dialect != Draft202012 {
			return nil, fmt.Errorf("%s: $schema must be %q", entry.Name(), Draft202012)
		}
		id, ok := object["$id"].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("%s: non-empty $id is required", entry.Name())
		}
		wantID := canonicalIDPrefix + entry.Name()
		if id != wantID {
			return nil, fmt.Errorf("%s: $id is %q, want %q", entry.Name(), id, wantID)
		}
		resources = append(resources, resource{name: entry.Name(), id: id, doc: doc})
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("no *.schema.json files found in %s", schemaDir)
	}

	sort.Slice(resources, func(i, j int) bool { return resources[i].name < resources[j].name })
	for _, resource := range resources {
		if err := compiler.AddResource(resource.id, resource.doc); err != nil {
			return nil, fmt.Errorf("register %s: %w", resource.name, err)
		}
	}

	registry := &Registry{
		schemas: make(map[string]*jsonschema.Schema, len(resources)),
		names:   make([]string, 0, len(resources)),
	}
	for _, resource := range resources {
		compiled, err := compiler.Compile(resource.id)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", resource.name, err)
		}
		registry.schemas[resource.name] = compiled
		registry.names = append(registry.names, resource.name)
	}
	return registry, nil
}

type ecmaScriptRegexp regexp2.Regexp

func (regexp *ecmaScriptRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(regexp).MatchString(value)
	return err == nil && matched
}

func (regexp *ecmaScriptRegexp) String() string {
	return (*regexp2.Regexp)(regexp).String()
}

func compileECMAScriptRegexp(pattern string) (jsonschema.Regexp, error) {
	compiled, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*ecmaScriptRegexp)(compiled), nil
}

// Names returns the sorted names of all compiled schemas.
func (r *Registry) Names() []string {
	return append([]string(nil), r.names...)
}

// Validate validates a decoded JSON value against a compiled schema.
func (r *Registry) Validate(schemaName string, value any) error {
	schema, ok := r.schemas[schemaName]
	if !ok {
		return fmt.Errorf("unknown schema %q", schemaName)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate against %s: %w", schemaName, err)
	}
	return nil
}

// ValidateJSON decodes JSON without losing number precision and validates it.
func (r *Registry) ValidateJSON(schemaName string, content []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("parse artifact for %s: %w", schemaName, err)
	}
	return r.Validate(schemaName, doc)
}
