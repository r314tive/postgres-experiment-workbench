package workloadplan

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

type Plan struct {
	ID               string
	Path             string
	Name             string
	Kind             string
	RequiresPostgres bool
	Logging          bool
	Steps            []Step
}

type Step struct {
	Name    string
	Command []string
	Notes   []string
}

func Build(root string, catalog speccatalog.Catalog, id string) (Plan, error) {
	if strings.TrimSpace(id) == "" {
		return Plan{}, fmt.Errorf("workload spec is required")
	}
	if errs := catalog.Validate("workload", []string{id}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}
	spec, err := catalog.Show("workload", id)
	if err != nil {
		return Plan{}, err
	}

	values := spec.Values
	kind := values["WORKLOAD_KIND"]
	plan := Plan{
		ID:               spec.ID,
		Path:             spec.Path,
		Name:             values["WORKLOAD_NAME"],
		Kind:             kind,
		RequiresPostgres: requiresPostgres(kind, values["WORKLOAD_REQUIRES_POSTGRES"]),
		Logging:          valueOr(values["WORKLOAD_RUN_LOG"], "1") != "0",
	}

	switch kind {
	case "profile-sql":
		profile := values["PROFILE"]
		sqlName := valueOr(values["WORKLOAD_SQL"], "10_run.sql")
		plan.Steps = append(plan.Steps, Step{
			Name: "Run profile SQL",
			Command: []string{
				"PROFILE_SIZE=" + valueOr(values["PROFILE_SIZE"], "${PROFILE_SIZE:-small}"),
				"PROFILE_SECONDS=" + valueOr(values["PROFILE_SECONDS"], "${PROFILE_SECONDS:-30}"),
				"./scripts/run_profile_sql.sh",
				profile,
				sqlName,
			},
			Notes: []string{filepath.ToSlash(filepath.Join("profiles", profile, "sql", sqlName))},
		})
	case "sql":
		sqlPath := firstValue(values, "SQL", "WORKLOAD_SQL")
		plan.Steps = append(plan.Steps, Step{
			Name: "Run SQL file",
			Command: []string{
				"./scripts/psql.sh",
				"-v", "profile=" + values["PROFILE"],
				"-v", "profile_size=" + valueOr(values["PROFILE_SIZE"], "${PROFILE_SIZE:-small}"),
				"-v", "profile_seconds=" + valueOr(values["PROFILE_SECONDS"], "${PROFILE_SECONDS:-30}"),
				"-f", displayPath(root, sqlPath),
			},
		})
	case "pgbench":
		reset := valueOr(values["PGBENCH_RESET"], "0")
		init := valueOr(values["PGBENCH_INIT"], "1")
		scale := valueOr(values["PGBENCH_SCALE"], "1")
		clients := valueOr(values["PGBENCH_CLIENTS"], "2")
		threads := valueOr(values["PGBENCH_THREADS"], "1")
		timeSeconds := valueOr(values["PGBENCH_TIME"], "30")
		transactions := values["PGBENCH_TRANSACTIONS"]
		mode := valueOr(values["PGBENCH_MODE"], "builtin")
		script := values["PGBENCH_SCRIPT"]
		if reset == "1" {
			plan.Steps = append(plan.Steps, Step{
				Name:    "Reset pgbench tables",
				Command: []string{"./scripts/psql.sh", "-v", "ON_ERROR_STOP=1", "-c", "DROP TABLE IF EXISTS public.pgbench_accounts, public.pgbench_branches, public.pgbench_history, public.pgbench_tellers;"},
			})
		}
		if init == "1" {
			plan.Steps = append(plan.Steps, Step{
				Name:    "Initialize pgbench",
				Command: []string{"docker", "compose", "exec", "-T", "postgres", "pgbench", "-h", "127.0.0.1", "-p", "5432", "-U", "${POSTGRES_USER:-postgres}", "-i", "-s", scale, "${POSTGRES_DB:-pg_experiment_workbench}"},
			})
		}
		command := []string{"docker", "compose", "exec", "-T", "postgres", "pgbench", "-h", "127.0.0.1", "-p", "5432", "-U", "${POSTGRES_USER:-postgres}", "-c", clients, "-j", threads}
		if transactions != "" {
			command = append(command, "-t", transactions)
		} else {
			command = append(command, "-T", timeSeconds)
		}
		switch {
		case script != "":
			command = append(command, "-f", displayPath(root, script))
		case mode != "builtin":
			command = append(command, "-b", mode)
		}
		if extra := values["PGBENCH_EXTRA_ARGS"]; extra != "" {
			command = append(command, extra)
		}
		command = append(command, "${POSTGRES_DB:-pg_experiment_workbench}")
		plan.Steps = append(plan.Steps, Step{Name: "Run pgbench", Command: command})
	case "pg-source-check":
		plan.Steps = append(plan.Steps, Step{
			Name:    "Run PostgreSQL source check",
			Command: []string{"./scripts/run_pg_source_check.sh", valueOr(values["PG_SOURCE_ACTION"], "run")},
			Notes: []string{
				"PG_REPO_URL=" + valueOr(values["PG_REPO_URL"], "${PG_REPO_URL:-https://github.com/postgres/postgres.git}"),
				"PG_REF=" + valueOr(values["PG_REF"], "${PG_REF:-master}"),
				"PG_PATCHSET=" + values["PG_PATCHSET"],
			},
		})
	case "noisia":
		command := []string{"./scripts/run_noisia.sh", valueOr(values["NOISIA_WORKLOAD"], values["WORKLOAD"])}
		if extra := values["NOISIA_EXTRA_ARGS"]; extra != "" {
			command = append(command, extra)
		}
		plan.Steps = append(plan.Steps, Step{Name: "Run noisia", Command: command})
	case "shell":
		plan.Steps = append(plan.Steps, Step{
			Name:    "Run shell command",
			Command: []string{"bash", "-lc", shellQuote(values["WORKLOAD_CMD"])},
			Notes:   []string{"DATABASE_URL and PG* environment variables are exported before execution."},
		})
	case "compose-run":
		plan.Steps = append(plan.Steps, Step{
			Name:    "Run Compose workload container",
			Command: []string{"docker", "compose", "run", "--rm", "workload"},
			Notes: []string{
				"WORKLOAD_IMAGE=" + values["WORKLOAD_IMAGE"],
				"WORKLOAD_COMMAND=" + values["WORKLOAD_COMMAND"],
			},
		})
	default:
		return Plan{}, fmt.Errorf("unsupported WORKLOAD_KIND: %s", kind)
	}

	return plan, nil
}

func Render(w io.Writer, plan Plan) error {
	if _, err := fmt.Fprintln(w, "# Workload Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	rows := []struct {
		key   string
		value string
	}{
		{"Workload", plan.ID},
		{"Name", plan.Name},
		{"Kind", plan.Kind},
		{"Spec path", plan.Path},
		{"Requires PostgreSQL", fmt.Sprintf("%t", plan.RequiresPostgres)},
		{"Workbench logging", fmt.Sprintf("%t", plan.Logging)},
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
	if _, err := fmt.Fprintln(w, "| Step | Name | Command | Notes |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| ---: | --- | --- | --- |"); err != nil {
		return err
	}
	for index, step := range plan.Steps {
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

func requiresPostgres(kind string, value string) bool {
	if value == "0" {
		return false
	}
	return kind != "pg-source-check"
}

func displayPath(root string, path string) string {
	if path == "" || strings.HasPrefix(path, "${") || filepath.IsAbs(path) {
		return path
	}
	return filepath.ToSlash(filepath.Join(root, path))
}

func firstValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if strings.TrimSpace(values[key]) != "" {
			return values[key]
		}
	}
	return ""
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

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
