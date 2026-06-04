package runreport

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
)

type SeriesClock func() time.Time

func RenderSummary(root string, inputs []string, w io.Writer) error {
	return RenderSummaryWithClock(root, inputs, w, time.Now)
}

func RenderSummaryWithClock(root string, inputs []string, w io.Writer, clock SeriesClock) error {
	runDirs, err := runartifact.CollectRunDirs(root, inputs)
	if err != nil {
		return err
	}

	runs, err := loadRunInfos(runDirs)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "# Run Series Summary")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Field | Value |")
	fmt.Fprintln(w, "| --- | --- |")
	fmt.Fprintf(w, "| Runs | `%d` |\n", len(runDirs))
	fmt.Fprintf(w, "| Generated at | `%s` |\n\n", clock().UTC().Format(time.RFC3339))

	fmt.Fprintln(w, "## Status Counts")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Status | Runs |")
	fmt.Fprintln(w, "| --- | ---: |")
	for _, status := range sortedStatusCounts(runs) {
		fmt.Fprintf(w, "| `%s` | `%d` |\n", status.Name, status.Count)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "## Runs")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Run id | Status | Experiment | PG config | Profile size | Workload |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- |")
	for _, run := range runs {
		fmt.Fprintf(w, "| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
			run.RunID, run.Status, run.Experiment, run.PGConfig, run.ProfileSize, run.Workload)
	}
	fmt.Fprintln(w)

	if err := printSummaryMetricTable(w, "Cumulative Metric Deltas", runDirs, "delta", runartifact.CumulativeMetrics); err != nil {
		return err
	}
	if err := printSummaryMetricTable(w, "Gauge Metric Maximums", runDirs, "max", runartifact.GaugeMetrics); err != nil {
		return err
	}

	fmt.Fprintln(w, "## Input Directories")
	fmt.Fprintln(w)
	for _, runDir := range runDirs {
		fmt.Fprintf(w, "- `%s`\n", runDir)
	}
	fmt.Fprintln(w)
	return nil
}

func RenderHistory(root string, inputs []string, w io.Writer) error {
	return RenderHistoryWithClock(root, inputs, w, time.Now)
}

func RenderHistoryWithClock(root string, inputs []string, w io.Writer, clock SeriesClock) error {
	if len(inputs) == 0 {
		return fmt.Errorf("at least one history input is required")
	}

	series, err := collectSeries(root, inputs)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "# Run History Comparison")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Field | Value |")
	fmt.Fprintln(w, "| --- | --- |")
	fmt.Fprintf(w, "| Series | `%d` |\n", len(series))
	fmt.Fprintf(w, "| Generated at | `%s` |\n\n", clock().UTC().Format(time.RFC3339))

	fmt.Fprintln(w, "## Series")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Series | Runs | Passed | Failed | Other | Directory |")
	fmt.Fprintln(w, "| --- | ---: | ---: | ---: | ---: | --- |")
	for _, item := range series {
		fmt.Fprintf(w, "| `%s` | `%d` | `%d` | `%d` | `%d` | `%s` |\n",
			item.Label, len(item.RunDirs), item.Passed, item.Failed, item.Other, item.Dir)
	}
	fmt.Fprintln(w)

	if err := printHistoryMetricTable(w, "Cumulative Metric Delta Averages", series, "delta", runartifact.CumulativeMetrics); err != nil {
		return err
	}
	if err := printHistoryMetricTable(w, "Gauge Metric Maximum Averages", series, "max", runartifact.GaugeMetrics); err != nil {
		return err
	}
	return nil
}

type statusCount struct {
	Name  string
	Count int
}

type seriesItem struct {
	Label   string
	Dir     string
	RunDirs []string
	Passed  int
	Failed  int
	Other   int
	Stats   map[string]runartifact.AggregateStats
}

func loadRunInfos(runDirs []string) ([]runartifact.RunInfo, error) {
	runs := make([]runartifact.RunInfo, 0, len(runDirs))
	for _, runDir := range runDirs {
		run, err := runartifact.LoadRunInfo(runDir)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func sortedStatusCounts(runs []runartifact.RunInfo) []statusCount {
	counts := make(map[string]int)
	for _, run := range runs {
		counts[run.Status]++
	}
	statuses := make([]statusCount, 0, len(counts))
	for status, count := range counts {
		statuses = append(statuses, statusCount{Name: status, Count: count})
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	return statuses
}

func printSummaryMetricTable(w io.Writer, title string, runDirs []string, mode string, metrics []string) error {
	fmt.Fprintf(w, "## %s\n\n", title)
	fmt.Fprintln(w, "| Metric | Runs | Min | Avg | Max | Stddev |")
	fmt.Fprintln(w, "| --- | ---: | ---: | ---: | ---: | ---: |")
	for _, metric := range metrics {
		stats, err := runartifact.AggregateMetric(runDirs, metric, mode)
		if err != nil {
			return err
		}
		if !stats.Valid {
			fmt.Fprintf(w, "| `%s` | `0` | `n/a` | `n/a` | `n/a` | `n/a` |\n", metric)
			continue
		}
		fmt.Fprintf(w, "| `%s` | `%d` | `%s` | `%s` | `%s` | `%s` |\n",
			metric,
			stats.Count,
			runartifact.FormatMetricFixed(stats.Min, false),
			runartifact.FormatMetricFixed(stats.Avg, true),
			runartifact.FormatMetricFixed(stats.Max, false),
			runartifact.FormatMetricFixed(stats.Stddev, true),
		)
	}
	fmt.Fprintln(w)
	return nil
}

func collectSeries(root string, inputs []string) ([]seriesItem, error) {
	var series []seriesItem
	for _, input := range inputs {
		dir, err := runartifact.ResolveDir(root, input)
		if err != nil {
			return nil, err
		}
		runDirs, err := runartifact.CollectRunDirs(root, []string{input})
		if err != nil {
			return nil, err
		}
		item := seriesItem{
			Label:   seriesLabel(dir),
			Dir:     dir,
			RunDirs: runDirs,
			Stats:   make(map[string]runartifact.AggregateStats),
		}
		runs, err := loadRunInfos(runDirs)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			switch run.Status {
			case "passed":
				item.Passed++
			case "failed":
				item.Failed++
			default:
				item.Other++
			}
		}
		for _, metric := range runartifact.CumulativeMetrics {
			stats, err := runartifact.AggregateMetric(runDirs, metric, "delta")
			if err != nil {
				return nil, err
			}
			item.Stats[metric+":delta"] = stats
		}
		for _, metric := range runartifact.GaugeMetrics {
			stats, err := runartifact.AggregateMetric(runDirs, metric, "max")
			if err != nil {
				return nil, err
			}
			item.Stats[metric+":max"] = stats
		}
		series = append(series, item)
	}
	return series, nil
}

func seriesLabel(dir string) string {
	parent := filepath.Base(filepath.Dir(dir))
	switch parent {
	case "repeats", "matrices", "runs":
		return filepath.Base(dir)
	default:
		return parent + "/" + filepath.Base(dir)
	}
}

func printHistoryMetricTable(w io.Writer, title string, series []seriesItem, mode string, metrics []string) error {
	fmt.Fprintf(w, "## %s\n\n", title)
	fmt.Fprint(w, "| Metric |")
	for _, item := range series {
		fmt.Fprintf(w, " `%s` |", item.Label)
	}
	fmt.Fprintln(w, " Trend |")

	fmt.Fprint(w, "| --- |")
	for range series {
		fmt.Fprint(w, " ---: |")
	}
	fmt.Fprintln(w, " ---: |")

	for _, metric := range metrics {
		fmt.Fprintf(w, "| `%s` |", metric)
		for _, item := range series {
			stats := item.Stats[metric+":"+mode]
			if stats.Valid {
				fmt.Fprintf(w, " `%s` |", formatHistoryNumber(stats.Avg))
			} else {
				fmt.Fprint(w, " `n/a` |")
			}
		}
		fmt.Fprintf(w, " `%s` |\n", historyTrend(series, metric, mode))
	}
	fmt.Fprintln(w)
	return nil
}

func historyTrend(series []seriesItem, metric string, mode string) string {
	if len(series) == 0 {
		return "n/a"
	}
	first := series[0].Stats[metric+":"+mode]
	last := series[len(series)-1].Stats[metric+":"+mode]
	if !first.Valid || !last.Valid {
		return "n/a"
	}
	return formatHistoryNumber(last.Avg - first.Avg)
}

func formatHistoryNumber(value float64) string {
	return runartifact.FormatMetricFixed(value, false)
}
