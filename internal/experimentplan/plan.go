package experimentplan

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

type Plan struct {
	Spec   speccatalog.Spec
	Fields map[string]string
	Phases []Phase
}

type Phase struct {
	Name    string
	Enabled bool
	Details string
}

func Build(catalog speccatalog.Catalog, input string) (Plan, error) {
	spec, err := catalog.Show("experiment", input)
	if err != nil {
		return Plan{}, err
	}
	if errs := catalog.Validate("experiment", []string{spec.ID}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}

	values := spec.Values
	fields := map[string]string{
		"name":              defaultValue(values["EXPERIMENT_NAME"], spec.ID),
		"topology":          defaultValue(values["EXPERIMENT_TOPOLOGY"], "single"),
		"pg_config":         defaultValue(values["EXPERIMENT_PG_CONFIG"], "default"),
		"profile":           shellDefault(values["EXPERIMENT_PROFILE"]),
		"profile_size":      defaultValue(values["EXPERIMENT_PROFILE_SIZE"], "small"),
		"profile_seconds":   defaultValue(values["EXPERIMENT_PROFILE_SECONDS"], "30"),
		"dataset":           shellDefault(values["EXPERIMENT_DATASET_SPEC"]),
		"dataset_size":      defaultValue(values["EXPERIMENT_DATASET_SIZE"], "small"),
		"workload":          shellDefault(values["EXPERIMENT_WORKLOAD_SPEC"]),
		"backgrounds":       shellDefault(values["EXPERIMENT_BACKGROUND_SPECS"]),
		"background_warmup": defaultValue(values["EXPERIMENT_BACKGROUND_WARMUP"], "0"),
		"background_wait":   defaultValue(values["EXPERIMENT_BACKGROUND_WAIT"], "0"),
		"metrics":           defaultValue(values["EXPERIMENT_METRICS"], "1"),
		"metrics_interval":  defaultValue(values["EXPERIMENT_METRICS_INTERVAL"], "1"),
		"metrics_duration":  defaultValue(values["EXPERIMENT_METRICS_DURATION"], "30"),
		"metrics_samples":   shellDefault(values["EXPERIMENT_METRICS_SAMPLES"]),
		"snapshot":          defaultValue(values["EXPERIMENT_SNAPSHOT"], "1"),
		"docker_reset":      defaultValue(values["EXPERIMENT_DOCKER_RESET"], "0"),
		"state_writer":      defaultValue(values["EXPERIMENT_STATE_WRITER"], "go"),
		"scan_paths":        shellDefault(values["EXPERIMENT_SCAN_PATHS"]),
		"run_id":            shellDefault(values["EXPERIMENT_RUN_ID"]),
	}

	phases := []Phase{
		{Name: "runtime", Enabled: true, Details: fmt.Sprintf("start topology `%s` with PostgreSQL config `%s`; docker reset `%s`", fields["topology"], fields["pg_config"], fields["docker_reset"])},
		{Name: "dataset", Enabled: fields["dataset"] != "", Details: detailOr(fields["dataset"], "no dataset spec")},
		{Name: "profile setup", Enabled: fields["profile"] != "" && defaultValue(values["EXPERIMENT_PROFILE_SETUP"], "1") == "1", Details: profileSetupDetails(fields)},
		{Name: "profile run", Enabled: fields["profile"] != "" && defaultValue(values["EXPERIMENT_PROFILE_RUN"], "0") == "1", Details: profileRunDetails(fields, values)},
		{Name: "before hooks", Enabled: hasAny(values, "EXPERIMENT_BEFORE_SQL_FILES", "EXPERIMENT_BEFORE_SQL", "EXPERIMENT_BEFORE_SHELL"), Details: hookDetails(values, "EXPERIMENT_BEFORE_SQL_FILES", "EXPERIMENT_BEFORE_SQL", "EXPERIMENT_BEFORE_SHELL")},
		{Name: "snapshot before", Enabled: fields["snapshot"] == "1", Details: "capture PostgreSQL snapshot before workload"},
		{Name: "metrics", Enabled: fields["metrics"] == "1", Details: metricsDetails(fields)},
		{Name: "background workloads", Enabled: fields["backgrounds"] != "", Details: backgroundDetails(fields)},
		{Name: "foreground workload", Enabled: fields["workload"] != "", Details: detailOr(fields["workload"], "no foreground workload")},
		{Name: "background wait", Enabled: fields["background_wait"] == "1", Details: "wait for background workload processes before after-hooks"},
		{Name: "after hooks", Enabled: hasAny(values, "EXPERIMENT_AFTER_SQL_FILES", "EXPERIMENT_AFTER_SQL", "EXPERIMENT_AFTER_SHELL"), Details: hookDetails(values, "EXPERIMENT_AFTER_SQL_FILES", "EXPERIMENT_AFTER_SQL", "EXPERIMENT_AFTER_SHELL")},
		{Name: "snapshot after", Enabled: fields["snapshot"] == "1", Details: "capture PostgreSQL snapshot after workload"},
		{Name: "assertions", Enabled: hasAny(values, "EXPERIMENT_ASSERT_SQL_FILES", "EXPERIMENT_ASSERT_SQL", "EXPERIMENT_ASSERT_SHELL"), Details: hookDetails(values, "EXPERIMENT_ASSERT_SQL_FILES", "EXPERIMENT_ASSERT_SQL", "EXPERIMENT_ASSERT_SHELL")},
		{Name: "failure scan", Enabled: true, Details: scanDetails(fields)},
		{Name: "verdict", Enabled: true, Details: fmt.Sprintf("write `verdict.env` and `verdict.json` using state writer `%s`", fields["state_writer"])},
	}

	return Plan{Spec: spec, Fields: fields, Phases: phases}, nil
}

func Render(w io.Writer, plan Plan) error {
	fmt.Fprintln(w, "# Experiment Plan")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Field | Value |")
	fmt.Fprintln(w, "| --- | --- |")
	writeRow(w, "Spec", plan.Spec.ID)
	writeRow(w, "Name", plan.Fields["name"])
	writeRow(w, "Topology", plan.Fields["topology"])
	writeRow(w, "PostgreSQL config", plan.Fields["pg_config"])
	writeRow(w, "Profile", plan.Fields["profile"])
	writeRow(w, "Profile size", plan.Fields["profile_size"])
	writeRow(w, "Dataset", plan.Fields["dataset"])
	writeRow(w, "Workload", plan.Fields["workload"])
	writeRow(w, "Background workloads", plan.Fields["backgrounds"])
	writeRow(w, "Metrics", plan.Fields["metrics"])
	writeRow(w, "Snapshots", plan.Fields["snapshot"])
	writeRow(w, "State writer", plan.Fields["state_writer"])
	writeRow(w, "Run id override", plan.Fields["run_id"])

	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Execution Phases")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Phase | Enabled | Details |")
	fmt.Fprintln(w, "| --- | --- | --- |")
	for _, phase := range plan.Phases {
		enabled := "no"
		if phase.Enabled {
			enabled = "yes"
		}
		writePhase(w, phase.Name, enabled, phase.Details)
	}
	return nil
}

func writeRow(w io.Writer, field string, value string) {
	fmt.Fprintf(w, "| %s | %s |\n", tableCell(field), tableCell(valueOr(value, "-")))
}

func writePhase(w io.Writer, phase string, enabled string, details string) {
	fmt.Fprintf(w, "| %s | %s | %s |\n", tableCell(phase), tableCell(enabled), tableCell(valueOr(details, "-")))
}

func profileSetupDetails(fields map[string]string) string {
	if fields["profile"] == "" {
		return "no profile"
	}
	return fmt.Sprintf("run `%s` `00_setup.sql` with size `%s` and seconds `%s`", fields["profile"], fields["profile_size"], fields["profile_seconds"])
}

func profileRunDetails(fields map[string]string, values map[string]string) string {
	if fields["profile"] == "" {
		return "no profile"
	}
	sql := defaultValue(values["EXPERIMENT_PROFILE_RUN_SQL"], "10_run.sql")
	return fmt.Sprintf("run `%s` `%s` with size `%s` and seconds `%s`", fields["profile"], sql, fields["profile_size"], fields["profile_seconds"])
}

func metricsDetails(fields map[string]string) string {
	if fields["metrics_samples"] != "" {
		return fmt.Sprintf("sample every `%s`s for `%s` samples", fields["metrics_interval"], fields["metrics_samples"])
	}
	return fmt.Sprintf("sample every `%s`s for `%s`s", fields["metrics_interval"], fields["metrics_duration"])
}

func backgroundDetails(fields map[string]string) string {
	if fields["backgrounds"] == "" {
		return "no background workloads"
	}
	return fmt.Sprintf("start `%s`; warmup `%s`s", fields["backgrounds"], fields["background_warmup"])
}

func scanDetails(fields map[string]string) string {
	if fields["scan_paths"] == "" {
		return "scan run directory"
	}
	return fmt.Sprintf("scan run directory plus `%s`", fields["scan_paths"])
}

func hookDetails(values map[string]string, fileKey string, inlineKey string, shellKey string) string {
	var parts []string
	if values[fileKey] != "" {
		parts = append(parts, fmt.Sprintf("%s=`%s`", fileKey, values[fileKey]))
	}
	if values[inlineKey] != "" {
		parts = append(parts, fmt.Sprintf("%s=`%s`", inlineKey, values[inlineKey]))
	}
	if values[shellKey] != "" {
		parts = append(parts, fmt.Sprintf("%s=`%s`", shellKey, values[shellKey]))
	}
	if len(parts) == 0 {
		return "no hooks"
	}
	return strings.Join(parts, "; ")
}

func hasAny(values map[string]string, keys ...string) bool {
	for _, key := range keys {
		if values[key] != "" {
			return true
		}
	}
	return false
}

func detailOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func defaultValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return shellDefault(value)
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func shellDefault(value string) string {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return value
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	_, fallback, ok := strings.Cut(inner, ":-")
	if !ok {
		return value
	}
	return fallback
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return value
}
