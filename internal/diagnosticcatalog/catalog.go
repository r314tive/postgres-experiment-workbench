package diagnosticcatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Catalog struct {
	Root string
}

func New(root string) Catalog {
	return Catalog{Root: root}
}

func (c Catalog) List() ([]string, error) {
	root := c.diagnosticsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var diagnostics []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		diagnostics = append(diagnostics, strings.TrimSuffix(entry.Name(), ".sql"))
	}
	sort.Strings(diagnostics)
	return diagnostics, nil
}

func (c Catalog) Show(input string) ([]byte, error) {
	path, err := c.Resolve(input)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (c Catalog) Resolve(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("diagnostic is required")
	}

	root := c.diagnosticsRoot()
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates,
			filepath.Join(c.Root, input),
			filepath.Join(root, input),
			filepath.Join(root, input+".sql"),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("diagnostic not found: %s", input)
}

func (c Catalog) diagnosticsRoot() string {
	return filepath.Join(c.Root, "sql", "diagnostics")
}
