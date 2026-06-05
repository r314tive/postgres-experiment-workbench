package utilityplan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/datasetplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/workloadplan"
)

type Plan struct {
	Spec     speccatalog.Spec  `json:"-"`
	Fields   map[string]string `json:"fields"`
	Phases   []Phase           `json:"phases"`
	Previews []Preview         `json:"previews,omitempty"`
}

type Phase struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Details string `json:"details"`
}

type Preview struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

func Build(catalog speccatalog.Catalog, input string) (Plan, error) {
	spec, err := catalog.Show("utility-test", input)
	if err != nil {
		return Plan{}, err
	}
	if errs := catalog.Validate("utility-test", []string{spec.ID}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}

	values := spec.Values
	fields := map[string]string{
		"name":              defaultValue(values["UTILITY_TEST_NAME"], spec.ID),
		"profile":           shellDefault(values["UTILITY_TEST_PROFILE"]),
		"profile_size":      defaultValue(values["UTILITY_TEST_PROFILE_SIZE"], "small"),
		"profile_seconds":   defaultValue(values["UTILITY_TEST_PROFILE_SECONDS"], "30"),
		"dataset":           shellDefault(values["UTILITY_TEST_DATASET_SPEC"]),
		"dataset_size":      defaultValue(values["UTILITY_TEST_DATASET_SIZE"], "small"),
		"workload":          shellDefault(values["UTILITY_TEST_WORKLOAD_SPEC"]),
		"backgrounds":       shellDefault(values["UTILITY_TEST_BACKGROUND_SPECS"]),
		"background_warmup": defaultValue(values["UTILITY_TEST_BACKGROUND_WARMUP"], "0"),
		"background_wait":   defaultValue(values["UTILITY_TEST_BACKGROUND_WAIT"], "0"),
		"metrics":           defaultValue(values["UTILITY_TEST_METRICS"], "1"),
		"metrics_interval":  defaultValue(values["UTILITY_TEST_METRICS_INTERVAL"], "1"),
		"metrics_duration":  defaultValue(values["UTILITY_TEST_METRICS_DURATION"], "30"),
		"metrics_samples":   shellDefault(values["UTILITY_TEST_METRICS_SAMPLES"]),
		"expect_files":      shellDefault(values["UTILITY_TEST_EXPECT_FILES"]),
		"assert_sql_files":  shellDefault(values["UTILITY_TEST_ASSERT_SQL_FILES"]),
		"assert_sql":        shellDefault(values["UTILITY_TEST_ASSERT_SQL"]),
		"assert_shell":      shellDefault(values["UTILITY_TEST_ASSERT_SHELL"]),
		"scan_paths":        shellDefault(values["UTILITY_TEST_SCAN_PATHS"]),
		"notes":             shellDefault(values["UTILITY_TEST_NOTES"]),
	}

	phases := []Phase{
		{Name: "profile setup", Enabled: fields["profile"] != "", Details: profileSetupDetails(fields)},
		{Name: "dataset load", Enabled: fields["dataset"] != "", Details: datasetDetails(fields)},
		{Name: "metrics", Enabled: fields["metrics"] == "1", Details: metricsDetails(fields)},
		{Name: "background workloads", Enabled: fields["backgrounds"] != "", Details: backgroundDetails(fields)},
		{Name: "utility workload", Enabled: fields["workload"] != "", Details: detailOr(fields["workload"], "no utility workload")},
		{Name: "background wait", Enabled: fields["background_wait"] == "1", Details: "wait for background workload processes after utility workload"},
		{Name: "assertions", Enabled: hasAssertions(fields), Details: assertionDetails(fields)},
		{Name: "failure scan", Enabled: true, Details: scanDetails(fields)},
		{Name: "evidence", Enabled: true, Details: evidenceDetails(fields)},
	}

	return Plan{Spec: spec, Fields: fields, Phases: phases}, nil
}

func BuildExpanded(root string, catalog speccatalog.Catalog, input string) (Plan, error) {
	plan, err := Build(catalog, input)
	if err != nil {
		return Plan{}, err
	}

	if datasetID := plan.Fields["dataset"]; datasetID != "" && !isDynamic(datasetID) {
		dataset, err := datasetplan.Build(root, catalog, datasetID)
		if err != nil {
			return Plan{}, err
		}
		var out bytes.Buffer
		if err := datasetplan.Render(&out, dataset); err != nil {
			return Plan{}, err
		}
		plan.Previews = append(plan.Previews, Preview{
			Kind:    "dataset",
			ID:      datasetID,
			Title:   "Dataset Preview",
			Content: strings.TrimSpace(out.String()),
		})
	}

	if workloadID := plan.Fields["workload"]; workloadID != "" && !isDynamic(workloadID) {
		workload, err := workloadplan.Build(root, catalog, workloadID)
		if err != nil {
			return Plan{}, err
		}
		var out bytes.Buffer
		if err := workloadplan.Render(&out, workload); err != nil {
			return Plan{}, err
		}
		plan.Previews = append(plan.Previews, Preview{
			Kind:    "workload",
			ID:      workloadID,
			Title:   "Utility Workload Preview",
			Content: strings.TrimSpace(out.String()),
		})
	}

	for _, workloadID := range staticWords(plan.Fields["backgrounds"]) {
		workload, err := workloadplan.Build(root, catalog, workloadID)
		if err != nil {
			return Plan{}, err
		}
		var out bytes.Buffer
		if err := workloadplan.Render(&out, workload); err != nil {
			return Plan{}, err
		}
		plan.Previews = append(plan.Previews, Preview{
			Kind:    "background workload",
			ID:      workloadID,
			Title:   "Background Workload Preview",
			Content: strings.TrimSpace(out.String()),
		})
	}

	return plan, nil
}

func Render(w io.Writer, plan Plan) error {
	fmt.Fprintln(w, "# Utility Test Plan")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Field | Value |")
	fmt.Fprintln(w, "| --- | --- |")
	writeRow(w, "Spec", plan.Spec.ID)
	writeRow(w, "Name", plan.Fields["name"])
	writeRow(w, "Profile", plan.Fields["profile"])
	writeRow(w, "Profile size", plan.Fields["profile_size"])
	writeRow(w, "Dataset", plan.Fields["dataset"])
	writeRow(w, "Dataset size", plan.Fields["dataset_size"])
	writeRow(w, "Utility workload", plan.Fields["workload"])
	writeRow(w, "Background workloads", plan.Fields["backgrounds"])
	writeRow(w, "Metrics", plan.Fields["metrics"])
	writeRow(w, "Expected files", plan.Fields["expect_files"])
	writeRow(w, "Assert SQL files", plan.Fields["assert_sql_files"])
	writeRow(w, "Assert SQL", plan.Fields["assert_sql"])
	writeRow(w, "Assert shell", plan.Fields["assert_shell"])
	writeRow(w, "Scan paths", plan.Fields["scan_paths"])
	writeRow(w, "Notes", plan.Fields["notes"])

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

	if len(plan.Previews) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Embedded Previews")
		for _, preview := range plan.Previews {
			fmt.Fprintln(w)
			fmt.Fprintf(w, "### %s: %s\n", tableCell(preview.Title), tableCell(preview.ID))
			fmt.Fprintln(w)
			fmt.Fprintln(w, "```text")
			fmt.Fprintln(w, preview.Content)
			fmt.Fprintln(w, "```")
		}
	}
	return nil
}

func RenderJSON(w io.Writer, plan Plan) error {
	payload := struct {
		Spec     string            `json:"spec"`
		SpecPath string            `json:"spec_path"`
		Name     string            `json:"name"`
		Fields   map[string]string `json:"fields"`
		Phases   []Phase           `json:"phases"`
		Previews []Preview         `json:"previews,omitempty"`
	}{
		Spec:     plan.Spec.ID,
		SpecPath: plan.Spec.Path,
		Name:     plan.Fields["name"],
		Fields:   plan.Fields,
		Phases:   plan.Phases,
		Previews: plan.Previews,
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
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

func datasetDetails(fields map[string]string) string {
	if fields["dataset"] == "" {
		return "no dataset spec"
	}
	return fmt.Sprintf("load `%s` with size `%s`", fields["dataset"], fields["dataset_size"])
}

func metricsDetails(fields map[string]string) string {
	detail := fmt.Sprintf("sample every `%s`s for `%s`s", fields["metrics_interval"], fields["metrics_duration"])
	if fields["metrics_samples"] != "" {
		detail += fmt.Sprintf(" or `%s` samples", fields["metrics_samples"])
	}
	return detail
}

func backgroundDetails(fields map[string]string) string {
	if fields["backgrounds"] == "" {
		return "no background workloads"
	}
	return fmt.Sprintf("start `%s`; warmup `%s`s", fields["backgrounds"], fields["background_warmup"])
}

func evidenceDetails(fields map[string]string) string {
	parts := []string{"capture utility workload result and logs"}
	if fields["metrics"] == "1" {
		parts = append(parts, "metrics CSV")
	}
	if fields["notes"] != "" {
		parts = append(parts, "operator notes")
	}
	return strings.Join(parts, "; ")
}

func hasAssertions(fields map[string]string) bool {
	return fields["expect_files"] != "" || fields["assert_sql_files"] != "" || fields["assert_sql"] != "" || fields["assert_shell"] != ""
}

func assertionDetails(fields map[string]string) string {
	var parts []string
	if fields["expect_files"] != "" {
		parts = append(parts, "expect files: "+fields["expect_files"])
	}
	if fields["assert_sql_files"] != "" {
		parts = append(parts, "SQL files: "+fields["assert_sql_files"])
	}
	if fields["assert_sql"] != "" {
		parts = append(parts, "inline SQL")
	}
	if fields["assert_shell"] != "" {
		parts = append(parts, "shell")
	}
	if len(parts) == 0 {
		return "no assertions"
	}
	return strings.Join(parts, "; ")
}

func scanDetails(fields map[string]string) string {
	if fields["scan_paths"] == "" {
		return "scan run directory"
	}
	return "scan run directory plus " + fields["scan_paths"]
}

func detailOr(value string, fallback string) string {
	if value == "" {
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

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func shellDefault(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return value
}

func isDynamic(value string) bool {
	return strings.Contains(value, "$")
}

func staticWords(value string) []string {
	if value == "" || isDynamic(value) {
		return nil
	}
	return strings.Fields(value)
}

func tableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return value
}
