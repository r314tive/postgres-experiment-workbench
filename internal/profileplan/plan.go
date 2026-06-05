package profileplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/profilecatalog"
)

type Options struct {
	Size    string
	Seconds string
	SQL     []string
}

type Plan struct {
	Profile     string    `json:"profile"`
	Description string    `json:"description"`
	Size        string    `json:"size"`
	Seconds     string    `json:"seconds"`
	SQL         []SQLStep `json:"sql"`
}

type SQLStep struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Command []string `json:"command"`
}

func Build(root string, catalog profilecatalog.Catalog, profile string, options Options) (Plan, error) {
	if profile == "" {
		return Plan{}, fmt.Errorf("profile is required")
	}
	if errs := catalog.Validate([]string{profile}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}

	metadata, err := catalog.Show(profile)
	if err != nil {
		return Plan{}, err
	}

	sqlFiles := options.SQL
	if len(sqlFiles) == 0 {
		sqlFiles = []string{"00_setup.sql", "10_run.sql"}
	}
	size := defaultValue(options.Size, metadata.DefaultSize)
	seconds := defaultValue(options.Seconds, "30")

	steps := make([]SQLStep, 0, len(sqlFiles))
	for _, sqlName := range sqlFiles {
		if strings.TrimSpace(sqlName) == "" {
			continue
		}
		sqlPath := filepath.Join(root, "profiles", profile, "sql", sqlName)
		if _, err := filepath.Abs(sqlPath); err != nil {
			return Plan{}, err
		}
		if !catalogFileExists(sqlPath) {
			return Plan{}, fmt.Errorf("profile SQL not found: %s", sqlPath)
		}
		steps = append(steps, SQLStep{
			Name: sqlName,
			Path: sqlPath,
			Command: []string{
				"PROFILE_SIZE=" + size,
				"PROFILE_SECONDS=" + seconds,
				"./scripts/run_profile_sql.sh",
				profile,
				sqlName,
			},
		})
	}

	return Plan{
		Profile:     profile,
		Description: metadata.Description,
		Size:        size,
		Seconds:     seconds,
		SQL:         steps,
	}, nil
}

func Render(w io.Writer, plan Plan) error {
	if _, err := fmt.Fprintln(w, "# Profile Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	rows := []struct {
		key   string
		value string
	}{
		{"Profile", plan.Profile},
		{"Description", plan.Description},
		{"Profile size", plan.Size},
		{"Profile seconds", plan.Seconds},
		{"SQL steps", fmt.Sprintf("%d", len(plan.SQL))},
	}
	if _, err := fmt.Fprintln(w, "| Field | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "| %s | %s |\n", tableCell(row.key), tableCell(defaultValue(row.value, "-"))); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "## SQL"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Step | SQL file | Command |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| ---: | --- | --- |"); err != nil {
		return err
	}
	for index, step := range plan.SQL {
		if _, err := fmt.Fprintf(
			w,
			"| %d | `%s` | `%s` |\n",
			index+1,
			tableCell(step.Path),
			tableCell(strings.Join(step.Command, " ")),
		); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, plan Plan) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

func catalogFileExists(path string) bool {
	_, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func defaultValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return value
}
