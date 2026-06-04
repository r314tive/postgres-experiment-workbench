package patchsetcatalog

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
)

type Metadata struct {
	Name                string
	Description         string
	PgRef               string
	Files               string
	AllowEmpty          string
	ConfigureArgs       string
	BuildCflags         string
	TestInitdbExtraOpts string
	Dir                 string
	SpecFile            string
	ResolvedFiles       []string
}

type Catalog struct {
	Root string
}

func New(root string) Catalog {
	return Catalog{Root: root}
}

func (c Catalog) List() ([]string, error) {
	root := filepath.Join(c.Root, "patchsets")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var patchsets []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "patchset.env" {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		if rel != "." {
			patchsets = append(patchsets, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(patchsets)
	return patchsets, nil
}

func (c Catalog) Show(patchset string) (Metadata, error) {
	dir := filepath.Join(c.Root, "patchsets", filepath.FromSlash(patchset))
	specFile := filepath.Join(dir, "patchset.env")
	if info, err := os.Stat(specFile); err != nil || info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a file")
		}
		return Metadata{}, fmt.Errorf("patchset spec not found: %s: %w", patchset, err)
	}

	values, err := envfile.Parse(specFile)
	if err != nil {
		return Metadata{}, err
	}

	metadata := Metadata{
		Name:       patchset,
		AllowEmpty: "0",
		Dir:        dir,
		SpecFile:   specFile,
	}
	metadata.Apply(values)

	resolved, err := ResolveEntries(dir, metadata.Files)
	if err != nil {
		return Metadata{}, err
	}
	metadata.ResolvedFiles = resolved
	return metadata, nil
}

func (m *Metadata) Apply(values map[string]string) {
	if value := values["PATCHSET_NAME"]; value != "" {
		m.Name = value
	}
	m.Description = values["PATCHSET_DESCRIPTION"]
	m.PgRef = values["PATCHSET_PG_REF"]
	m.Files = values["PATCHSET_FILES"]
	if value := values["PATCHSET_ALLOW_EMPTY"]; value != "" {
		m.AllowEmpty = value
	}
	m.ConfigureArgs = values["PATCHSET_CONFIGURE_ARGS"]
	m.BuildCflags = values["PATCHSET_BUILD_CFLAGS"]
	m.TestInitdbExtraOpts = values["PATCHSET_TEST_INITDB_EXTRA_OPTS"]
}

func (c Catalog) Validate(patchsets []string) []error {
	if len(patchsets) == 0 {
		list, err := c.List()
		if err != nil {
			return []error{err}
		}
		patchsets = list
	}

	var errs []error
	for _, patchset := range patchsets {
		errs = append(errs, c.validateOne(patchset)...)
	}
	return errs
}

func (c Catalog) validateOne(patchset string) []error {
	metadata, err := c.Show(patchset)
	if err != nil {
		return []error{err}
	}

	var errs []error
	if metadata.Name != patchset {
		errs = append(errs, fmt.Errorf("PATCHSET_NAME mismatch for %s: %s", patchset, metadata.Name))
	}
	if metadata.Description == "" {
		errs = append(errs, fmt.Errorf("PATCHSET_DESCRIPTION is required for %s", patchset))
	}
	if metadata.PgRef == "" {
		errs = append(errs, fmt.Errorf("PATCHSET_PG_REF is required for %s", patchset))
	}

	for _, entry := range metadata.ResolvedFiles {
		if err := validateEntry(metadata.Dir, entry); err != nil {
			errs = append(errs, err)
		}
	}
	if len(metadata.ResolvedFiles) == 0 && metadata.AllowEmpty != "1" {
		errs = append(errs, fmt.Errorf("patchset has no patch files and PATCHSET_ALLOW_EMPTY is not 1: %s", patchset))
	}
	return errs
}

func ResolveEntries(dir string, explicitFiles string) ([]string, error) {
	if explicitFiles != "" {
		return strings.Fields(explicitFiles), nil
	}

	seriesPath := filepath.Join(dir, "series")
	if _, err := os.Stat(seriesPath); err == nil {
		return readSeries(seriesPath)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext == ".patch" || ext == ".diff" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

func readSeries(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if index := strings.Index(line, "#"); index >= 0 {
			line = line[:index]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			entries = append(entries, line)
		}
	}
	return entries, scanner.Err()
}

func validateEntry(dir string, entry string) error {
	if filepath.IsAbs(entry) || strings.Contains(entry, "..") {
		return fmt.Errorf("patch entries must be relative filenames under %s: %s", dir, entry)
	}

	path := filepath.Join(dir, filepath.FromSlash(entry))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("patch file not found: %s", path)
	}
	return nil
}
