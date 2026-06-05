package runreport

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
)

var reportMetrics = []string{
	"active_sessions",
	"waiting_sessions",
	"lock_waiting_sessions",
	"blocked_sessions",
	"locks_total",
	"locks_waiting",
	"xact_commit",
	"xact_rollback",
	"blks_read",
	"blks_hit",
	"tup_inserted",
	"tup_updated",
	"tup_deleted",
	"deadlocks",
	"temp_files",
	"temp_bytes",
	"wal_records",
	"wal_fpi",
	"wal_bytes",
}

var compareMetrics = []struct {
	label  string
	metric string
}{
	{label: "WAL bytes delta", metric: "wal_bytes"},
	{label: "Temp bytes delta", metric: "temp_bytes"},
	{label: "Tuples inserted delta", metric: "tup_inserted"},
	{label: "Tuples updated delta", metric: "tup_updated"},
	{label: "Tuples deleted delta", metric: "tup_deleted"},
}

func RenderRun(root string, input string, w io.Writer) error {
	runDir, err := runartifact.ResolveRunDir(root, input)
	if err != nil {
		return err
	}

	manifest, err := runartifact.LoadOptionalEnv(filepath.Join(runDir, "manifest.env"))
	if err != nil {
		return err
	}
	verdict, err := runartifact.LoadOptionalEnv(filepath.Join(runDir, "verdict.env"))
	if err != nil {
		return err
	}

	metricsPath := filepath.Join(runDir, "metrics.csv")
	samples, err := runartifact.MetricStat(metricsPath, "wal_bytes")
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "# Experiment Run Report")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Field | Value |")
	fmt.Fprintln(w, "| --- | --- |")
	fmt.Fprintf(w, "| Run id | `%s` |\n", manifest.Value("run_id", filepath.Base(runDir)))
	fmt.Fprintf(w, "| Status | `%s` |\n", verdict.Value("status", "missing"))
	fmt.Fprintf(w, "| Message | `%s` |\n", verdict.Value("message", ""))
	fmt.Fprintf(w, "| Started | `%s` |\n", manifest.Value("started_at", "unknown"))
	fmt.Fprintf(w, "| Finished | `%s` |\n", verdict.Value("finished_at", "unknown"))
	fmt.Fprintf(w, "| Experiment | `%s` |\n", manifest.Value("experiment_spec_id", "unknown"))
	fmt.Fprintf(w, "| Topology | `%s` |\n", manifest.Value("experiment_topology", "unknown"))
	fmt.Fprintf(w, "| PostgreSQL config | `%s` |\n", manifest.Value("experiment_pg_config", "unknown"))
	fmt.Fprintf(w, "| Profile | `%s` |\n", manifest.Value("profile", ""))
	fmt.Fprintf(w, "| Dataset | `%s` |\n", manifest.Value("dataset_spec", ""))
	fmt.Fprintf(w, "| Workload | `%s` |\n", manifest.Value("workload_spec", ""))
	fmt.Fprintf(w, "| Background workloads | `%s` |\n", manifest.Value("background_specs", ""))
	fmt.Fprintf(w, "| Workload exit | `%s` |\n", verdict.Value("workload_exit", "0"))
	fmt.Fprintf(w, "| Assertion exit | `%s` |\n", verdict.Value("assert_exit", "0"))
	fmt.Fprintf(w, "| Scan exit | `%s` |\n", verdict.Value("scan_exit", "0"))
	fmt.Fprintf(w, "| Run dir | `%s` |\n\n", runDir)

	fmt.Fprintln(w, "## Metrics")
	fmt.Fprintln(w)
	if !samples.Valid {
		fmt.Fprintln(w, "No metrics.csv samples were found.")
		fmt.Fprintln(w)
	} else {
		fmt.Fprintf(w, "Samples: `%d`\n\n", samples.Count)
		fmt.Fprintln(w, "| Metric | First | Last | Delta | Min | Max |")
		fmt.Fprintln(w, "| --- | ---: | ---: | ---: | ---: | ---: |")
		for _, metric := range reportMetrics {
			stat, err := runartifact.MetricStat(metricsPath, metric)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
				metric,
				formatStat(stat, "first"),
				formatStat(stat, "last"),
				formatStat(stat, "delta"),
				formatStat(stat, "min"),
				formatStat(stat, "max"),
			)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "## Artifacts")
	fmt.Fprintln(w)
	if err := renderArtifactList(w, runDir, "snapshots", "Snapshots"); err != nil {
		return err
	}
	if err := renderArtifactList(w, runDir, "background", "Background Logs"); err != nil {
		return err
	}
	if err := renderArtifactList(w, runDir, "artifacts", "Extra Artifacts"); err != nil {
		return err
	}
	fmt.Fprintln(w, "- `stdout.log`")
	if exists(filepath.Join(runDir, "workload.log")) {
		fmt.Fprintln(w, "- `workload.log`")
	}
	if exists(filepath.Join(runDir, "scan.log")) {
		fmt.Fprintln(w, "- `scan.log`")
	}
	if exists(filepath.Join(runDir, "verdict.json")) {
		fmt.Fprintln(w, "- `verdict.json`")
	}
	fmt.Fprintln(w)
	return nil
}

type ComparisonOptions struct {
	BaselineLabel  string
	CandidateLabel string
}

func RenderComparison(root string, baselineInput string, candidateInput string, w io.Writer) error {
	return RenderComparisonWithOptions(root, baselineInput, candidateInput, ComparisonOptions{}, w)
}

func RenderComparisonWithOptions(root string, baselineInput string, candidateInput string, options ComparisonOptions, w io.Writer) error {
	baselineDir, err := runartifact.ResolveRunDir(root, baselineInput)
	if err != nil {
		return err
	}
	candidateDir, err := runartifact.ResolveRunDir(root, candidateInput)
	if err != nil {
		return err
	}

	baselineVerdict, err := runartifact.LoadOptionalEnv(filepath.Join(baselineDir, "verdict.env"))
	if err != nil {
		return err
	}
	candidateVerdict, err := runartifact.LoadOptionalEnv(filepath.Join(candidateDir, "verdict.env"))
	if err != nil {
		return err
	}

	baselineLabel := baselineDir
	if options.BaselineLabel != "" {
		baselineLabel = options.BaselineLabel
	}
	candidateLabel := candidateDir
	if options.CandidateLabel != "" {
		candidateLabel = options.CandidateLabel
	}

	fmt.Fprintln(w, "# Run Comparison")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Field | Baseline | Candidate |")
	fmt.Fprintln(w, "| --- | --- | --- |")
	fmt.Fprintf(w, "| Run dir | `%s` | `%s` |\n", baselineLabel, candidateLabel)
	fmt.Fprintf(w, "| Status | `%s` | `%s` |\n", baselineVerdict.Value("status", "missing"), candidateVerdict.Value("status", "missing"))
	fmt.Fprintf(w, "| Message | `%s` | `%s` |\n", baselineVerdict.Value("message", ""), candidateVerdict.Value("message", ""))

	for _, metric := range compareMetrics {
		baseline, err := metricDelta(filepath.Join(baselineDir, "metrics.csv"), metric.metric)
		if err != nil {
			return err
		}
		candidate, err := metricDelta(filepath.Join(candidateDir, "metrics.csv"), metric.metric)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "| %s | `%s` | `%s` |\n", metric.label, baseline, candidate)
	}
	return nil
}

func metricDelta(path string, metric string) (string, error) {
	stat, err := runartifact.MetricStat(path, metric)
	if err != nil {
		return "", err
	}
	if !stat.Valid {
		return "n/a", nil
	}
	return runartifact.FormatMetric(stat.Delta), nil
}

func formatStat(stat runartifact.MetricStats, field string) string {
	if !stat.Valid {
		return "n/a"
	}
	switch field {
	case "first":
		return runartifact.FormatMetric(stat.First)
	case "last":
		return runartifact.FormatMetric(stat.Last)
	case "delta":
		return runartifact.FormatMetric(stat.Delta)
	case "min":
		return runartifact.FormatMetric(stat.Min)
	case "max":
		return runartifact.FormatMetric(stat.Max)
	default:
		return "n/a"
	}
}

func renderArtifactList(w io.Writer, runDir string, subdir string, label string) error {
	files, err := runartifact.ListRelativeFiles(runDir, subdir, 20)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	fmt.Fprintf(w, "### %s\n\n", label)
	for _, file := range files {
		fmt.Fprintf(w, "- `%s`\n", file)
	}
	fmt.Fprintln(w)
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
