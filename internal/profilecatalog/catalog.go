package profilecatalog

import (
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
	Tags                string
	Schemas             string
	Sizes               string
	DefaultSize         string
	RequiresTopology    string
	BackgroundWorkloads string
	DiagnosticSQL       string
}

type Catalog struct {
	Root string
}

func New(root string) Catalog {
	return Catalog{Root: root}
}

func (c Catalog) List() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(c.Root, "profiles"))
	if err != nil {
		return nil, err
	}

	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			profiles = append(profiles, entry.Name())
		}
	}
	sort.Strings(profiles)
	return profiles, nil
}

func (c Catalog) Show(profile string) (Metadata, error) {
	dir := filepath.Join(c.Root, "profiles", profile)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return Metadata{}, fmt.Errorf("profile not found: %s: %w", profile, err)
	}

	metadata := Metadata{
		Name:             profile,
		Sizes:            "small medium large",
		DefaultSize:      "small",
		RequiresTopology: "single",
	}

	metaPath := filepath.Join(dir, "profile.env")
	if _, err := os.Stat(metaPath); err == nil {
		values, err := envfile.Parse(metaPath)
		if err != nil {
			return Metadata{}, err
		}
		metadata.Apply(values)
	} else if !os.IsNotExist(err) {
		return Metadata{}, err
	}

	return metadata, nil
}

func (m *Metadata) Apply(values map[string]string) {
	if value := values["PROFILE_NAME"]; value != "" {
		m.Name = value
	}
	m.Description = values["PROFILE_DESCRIPTION"]
	m.Tags = values["PROFILE_TAGS"]
	m.Schemas = values["PROFILE_SCHEMAS"]
	if value := values["PROFILE_SIZES"]; value != "" {
		m.Sizes = value
	}
	if value := values["PROFILE_DEFAULT_SIZE"]; value != "" {
		m.DefaultSize = value
	}
	if value := values["PROFILE_REQUIRES_TOPOLOGY"]; value != "" {
		m.RequiresTopology = value
	}
	m.BackgroundWorkloads = values["PROFILE_BACKGROUND_WORKLOADS"]
	m.DiagnosticSQL = values["PROFILE_DIAGNOSTIC_SQL"]
}

func (c Catalog) Validate(profiles []string) []error {
	if len(profiles) == 0 {
		list, err := c.List()
		if err != nil {
			return []error{err}
		}
		profiles = list
	}

	var errs []error
	for _, profile := range profiles {
		errs = append(errs, c.validateOne(profile)...)
	}
	return errs
}

func (c Catalog) validateOne(profile string) []error {
	dir := filepath.Join(c.Root, "profiles", profile)
	var errs []error

	required := []string{
		"README.md",
		filepath.Join("sql", "00_setup.sql"),
		filepath.Join("sql", "10_run.sql"),
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			errs = append(errs, fmt.Errorf("missing %s for profile: %s", rel, profile))
		}
	}

	metadata, err := c.Show(profile)
	if err != nil {
		errs = append(errs, err)
		return errs
	}

	metaPath := filepath.Join(dir, "profile.env")
	if _, err := os.Stat(metaPath); err == nil {
		if metadata.Name != profile {
			errs = append(errs, fmt.Errorf("PROFILE_NAME mismatch in %s: %s", metaPath, metadata.Name))
		}
		if metadata.Description == "" {
			errs = append(errs, fmt.Errorf("PROFILE_DESCRIPTION is required in %s", metaPath))
		}
		if !wordContains(metadata.Sizes, metadata.DefaultSize) {
			errs = append(errs, fmt.Errorf("PROFILE_DEFAULT_SIZE must be listed in PROFILE_SIZES for %s", profile))
		}
	}

	return errs
}

func wordContains(words string, needle string) bool {
	for _, word := range strings.Fields(words) {
		if word == needle {
			return true
		}
	}
	return false
}
