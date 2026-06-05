package datasetplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

type Plan struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	RequiresPostgres bool   `json:"requires_postgres"`
	Steps            []Step `json:"steps"`
}

type Step struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	Notes   []string `json:"notes,omitempty"`
}

func Build(root string, catalog speccatalog.Catalog, id string) (Plan, error) {
	if strings.TrimSpace(id) == "" {
		return Plan{}, fmt.Errorf("dataset spec is required")
	}
	if errs := catalog.Validate("dataset", []string{id}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}
	spec, err := catalog.Show("dataset", id)
	if err != nil {
		return Plan{}, err
	}

	values := spec.Values
	kind := values["DATASET_KIND"]
	plan := Plan{
		ID:               spec.ID,
		Path:             spec.Path,
		Name:             values["DATASET_NAME"],
		Kind:             kind,
		RequiresPostgres: true,
	}

	switch kind {
	case "sql":
		sqlPath := values["DATASET_SQL"]
		plan.Steps = append(plan.Steps, Step{
			Name: "Run dataset SQL",
			Command: []string{
				"./scripts/psql.sh",
				"-v", "dataset_schema=" + valueOr(values["DATASET_SCHEMA"], "${DATASET_SCHEMA:-dataset_synthetic}"),
				"-v", "dataset_size=" + valueOr(values["DATASET_SIZE"], "${DATASET_SIZE:-small}"),
				"-v", "dataset_rows=" + valueOr(values["DATASET_ROWS"], "${DATASET_ROWS:-10000}"),
				"-v", "dataset_seed=" + valueOr(values["DATASET_SEED"], "${DATASET_SEED:-1}"),
				"-f", displayPath(root, sqlPath),
			},
		})
	case "profile":
		profile := values["DATASET_PROFILE"]
		plan.Steps = append(plan.Steps, Step{
			Name: "Run profile setup SQL",
			Command: []string{
				"PROFILE_SIZE=" + valueOr(values["DATASET_SIZE"], "${DATASET_SIZE:-small}"),
				"./scripts/run_profile_sql.sh",
				profile,
				"00_setup.sql",
			},
			Notes: []string{filepath.ToSlash(filepath.Join("profiles", profile, "sql", "00_setup.sql"))},
		})
	case "pgbench":
		plan.Steps = append(plan.Steps, Step{
			Name: "Initialize pgbench dataset",
			Command: []string{
				"PGBENCH_RESET=${PGBENCH_RESET:-1}",
				"PGBENCH_INIT=1",
				"PGBENCH_SCALE=" + valueOr(values["DATASET_SCALE"], "${DATASET_SCALE:-1}"),
				"PGBENCH_TIME=1",
				"PGBENCH_CLIENTS=1",
				"PGBENCH_THREADS=1",
				"WORKLOAD_RUN_LOG=0",
				"./scripts/run_workload.sh",
				"run",
				"workloads/pgbench/tiny.env",
			},
		})
	default:
		return Plan{}, fmt.Errorf("unsupported DATASET_KIND: %s", kind)
	}

	return plan, nil
}

func Render(w io.Writer, plan Plan) error {
	if _, err := fmt.Fprintln(w, "# Dataset Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	rows := []struct {
		key   string
		value string
	}{
		{"Dataset", plan.ID},
		{"Name", plan.Name},
		{"Kind", plan.Kind},
		{"Spec path", plan.Path},
		{"Requires PostgreSQL", fmt.Sprintf("%t", plan.RequiresPostgres)},
		{"Steps", fmt.Sprintf("%d", len(plan.Steps))},
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
	if _, err := fmt.Fprintln(w, "## Steps"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return renderSteps(w, plan.Steps)
}

func RenderRaw(w io.Writer, plan Plan) error {
	if _, err := fmt.Fprintln(w, "# Dataset Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return renderSteps(w, plan.Steps)
}

func RenderJSON(w io.Writer, plan Plan) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

func renderSteps(w io.Writer, steps []Step) error {
	if _, err := fmt.Fprintln(w, "| Step | Name | Command | Notes |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| ---: | --- | --- | --- |"); err != nil {
		return err
	}
	for index, step := range steps {
		if _, err := fmt.Fprintf(
			w,
			"| %d | %s | `%s` | %s |\n",
			index+1,
			tableCell(step.Name),
			tableCell(strings.Join(step.Command, " ")),
			tableCell(defaultValue(strings.Join(step.Notes, "<br>"), "-")),
		); err != nil {
			return err
		}
	}
	return nil
}

func displayPath(root string, path string) string {
	if path == "" || strings.HasPrefix(path, "${") || filepath.IsAbs(path) {
		return path
	}
	return filepath.ToSlash(filepath.Join(root, path))
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
