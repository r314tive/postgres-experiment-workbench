package matrixplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

type Plan struct {
	Spec         string      `json:"spec"`
	Name         string      `json:"name"`
	Experiments  []string    `json:"experiments"`
	PGConfigs    []string    `json:"pg_configs"`
	ProfileSizes []string    `json:"profile_sizes"`
	Repeats      int         `json:"repeats"`
	TotalRuns    int         `json:"total_runs"`
	Runs         []PlanEntry `json:"runs"`
}

type PlanEntry struct {
	Experiment  string `json:"experiment"`
	PGConfig    string `json:"pg_config"`
	ProfileSize string `json:"profile_size"`
	Repeat      int    `json:"repeat"`
}

func Build(catalog speccatalog.Catalog, input string) (Plan, error) {
	spec, err := catalog.Show("matrix", input)
	if err != nil {
		return Plan{}, err
	}
	if errs := catalog.Validate("matrix", []string{spec.ID}); len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}

	values := spec.Values
	experiments := wordsOr(values["MATRIX_EXPERIMENTS"], []string{"smoke"})
	pgConfigs := wordsOr(values["MATRIX_PG_CONFIGS"], []string{"default"})
	profileSizes := wordsOr(values["MATRIX_PROFILE_SIZES"], []string{"small"})
	repeats := positiveIntOr(values["MATRIX_REPEATS"], 1)

	runs := make([]PlanEntry, 0, len(experiments)*len(pgConfigs)*len(profileSizes)*repeats)
	for _, experiment := range experiments {
		for _, pgConfig := range pgConfigs {
			for _, profileSize := range profileSizes {
				for repeat := 1; repeat <= repeats; repeat++ {
					runs = append(runs, PlanEntry{
						Experiment:  experiment,
						PGConfig:    pgConfig,
						ProfileSize: profileSize,
						Repeat:      repeat,
					})
				}
			}
		}
	}

	return Plan{
		Spec:         spec.ID,
		Name:         defaultValue(values["MATRIX_NAME"], spec.ID),
		Experiments:  experiments,
		PGConfigs:    pgConfigs,
		ProfileSizes: profileSizes,
		Repeats:      repeats,
		TotalRuns:    len(runs),
		Runs:         runs,
	}, nil
}

func Render(w io.Writer, plan Plan) error {
	if _, err := fmt.Fprintln(w, "# Experiment Matrix Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Matrix: `%s`\n\n", tableCell(plan.Name)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Total runs: `%d`\n\n", plan.TotalRuns); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Experiment | PG config | Profile size | Repeat |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- | ---: |"); err != nil {
		return err
	}
	for _, run := range plan.Runs {
		if _, err := fmt.Fprintf(
			w,
			"| `%s` | `%s` | `%s` | `%d` |\n",
			tableCell(run.Experiment),
			tableCell(run.PGConfig),
			tableCell(run.ProfileSize),
			run.Repeat,
		); err != nil {
			return err
		}
	}
	return nil
}

func RenderRaw(w io.Writer, plan Plan) error {
	if _, err := fmt.Fprintln(w, "# Experiment Matrix Plan"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Experiment | PG config | Profile size | Repeat |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- | ---: |"); err != nil {
		return err
	}
	for _, run := range plan.Runs {
		if _, err := fmt.Fprintf(
			w,
			"| `%s` | `%s` | `%s` | `%d` |\n",
			tableCell(run.Experiment),
			tableCell(run.PGConfig),
			tableCell(run.ProfileSize),
			run.Repeat,
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

func wordsOr(value string, fallback []string) []string {
	words := strings.Fields(shellDefault(value))
	if len(words) == 0 {
		return append([]string(nil), fallback...)
	}
	return words
}

func positiveIntOr(value string, fallback int) int {
	value = shellDefault(value)
	if value == "" {
		return fallback
	}
	parsed := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return fallback
		}
		parsed = parsed*10 + int(ch-'0')
	}
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

func defaultValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return shellDefault(value)
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
