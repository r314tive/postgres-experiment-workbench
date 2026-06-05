package runcatalog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
)

type Summary struct {
	RunID        string `json:"run_id"`
	Dir          string `json:"dir"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	Experiment   string `json:"experiment"`
	Topology     string `json:"topology"`
	PGConfig     string `json:"pg_config"`
	Profile      string `json:"profile"`
	ProfileSize  string `json:"profile_size"`
	Dataset      string `json:"dataset"`
	Workload     string `json:"workload"`
	SampleCount  int    `json:"sample_count"`
	HasMetrics   bool   `json:"has_metrics"`
	WorkloadExit string `json:"workload_exit"`
	AssertExit   string `json:"assert_exit"`
	ScanExit     string `json:"scan_exit"`
}

func List(root string, inputs []string) ([]Summary, error) {
	if len(inputs) == 0 {
		inputs = []string{"runs"}
	}

	var dirs []string
	seen := make(map[string]struct{})
	for _, input := range inputs {
		resolved, err := resolveInput(root, input)
		if err != nil {
			if input == "runs" && os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		discovered, err := discoverRuns(root, resolved)
		if err != nil {
			return nil, err
		}
		for _, dir := range discovered {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[abs]; ok {
				continue
			}
			seen[abs] = struct{}{}
			dirs = append(dirs, abs)
		}
	}

	summaries := make([]Summary, 0, len(dirs))
	for _, dir := range dirs {
		summary, err := Load(root, dir)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	sortSummaries(summaries)
	return summaries, nil
}

func Show(root string, input string) (Summary, error) {
	dir, err := runartifact.ResolveRunDir(root, input)
	if err != nil {
		return Summary{}, err
	}
	return Load(root, dir)
}

func Load(root string, dir string) (Summary, error) {
	manifest, err := runartifact.LoadOptionalEnv(filepath.Join(dir, "manifest.env"))
	if err != nil {
		return Summary{}, err
	}
	verdict, err := runartifact.LoadOptionalEnv(filepath.Join(dir, "verdict.env"))
	if err != nil {
		return Summary{}, err
	}
	samples, hasMetrics, err := countMetricSamples(filepath.Join(dir, "metrics.csv"))
	if err != nil {
		return Summary{}, err
	}

	return Summary{
		RunID:        manifest.Value("run_id", filepath.Base(dir)),
		Dir:          displayPath(root, dir),
		Status:       verdict.Value("status", "missing"),
		Message:      verdict.Value("message", ""),
		StartedAt:    manifest.Value("started_at", ""),
		FinishedAt:   verdict.Value("finished_at", ""),
		Experiment:   manifest.Value("experiment_spec_id", "unknown"),
		Topology:     manifest.Value("experiment_topology", "unknown"),
		PGConfig:     manifest.Value("experiment_pg_config", "unknown"),
		Profile:      manifest.Value("profile", ""),
		ProfileSize:  manifest.Value("profile_size", ""),
		Dataset:      manifest.Value("dataset_spec", ""),
		Workload:     manifest.Value("workload_spec", ""),
		SampleCount:  samples,
		HasMetrics:   hasMetrics,
		WorkloadExit: verdict.Value("workload_exit", ""),
		AssertExit:   verdict.Value("assert_exit", ""),
		ScanExit:     verdict.Value("scan_exit", ""),
	}, nil
}

func RenderList(w io.Writer, summaries []Summary) error {
	if _, err := fmt.Fprintln(w, "# Experiment Runs"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if len(summaries) == 0 {
		_, err := fmt.Fprintln(w, "No run directories were found.")
		return err
	}
	if _, err := fmt.Fprintln(w, "| Run | Status | Experiment | Profile | Workload | Samples | Started | Dir |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- | --- | --- | ---: | --- | --- |"); err != nil {
		return err
	}
	for _, summary := range summaries {
		if _, err := fmt.Fprintf(
			w,
			"| `%s` | `%s` | `%s` | `%s` | `%s` | `%d` | `%s` | `%s` |\n",
			tableCell(summary.RunID),
			tableCell(summary.Status),
			tableCell(summary.Experiment),
			tableCell(defaultValue(summary.Profile, "-")),
			tableCell(defaultValue(summary.Workload, "-")),
			summary.SampleCount,
			tableCell(defaultValue(summary.StartedAt, "-")),
			tableCell(summary.Dir),
		); err != nil {
			return err
		}
	}
	return nil
}

func RenderShow(w io.Writer, summary Summary) error {
	if _, err := fmt.Fprintln(w, "# Experiment Run"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Field | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}
	rows := []struct {
		key   string
		value string
	}{
		{"Run", summary.RunID},
		{"Status", summary.Status},
		{"Message", summary.Message},
		{"Started", summary.StartedAt},
		{"Finished", summary.FinishedAt},
		{"Experiment", summary.Experiment},
		{"Topology", summary.Topology},
		{"PostgreSQL config", summary.PGConfig},
		{"Profile", summary.Profile},
		{"Profile size", summary.ProfileSize},
		{"Dataset", summary.Dataset},
		{"Workload", summary.Workload},
		{"Metrics samples", fmt.Sprintf("%d", summary.SampleCount)},
		{"Has metrics", fmt.Sprintf("%t", summary.HasMetrics)},
		{"Workload exit", summary.WorkloadExit},
		{"Assertion exit", summary.AssertExit},
		{"Scan exit", summary.ScanExit},
		{"Run dir", summary.Dir},
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "| %s | `%s` |\n", tableCell(row.key), tableCell(defaultValue(row.value, "-"))); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func resolveInput(root string, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates, filepath.Join(root, input), filepath.Join(root, "runs", input))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return filepath.Abs(candidate)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", os.ErrNotExist
}

func discoverRuns(root string, dir string) ([]string, error) {
	if runartifact.IsRunDir(dir) {
		return []string{dir}, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "runs.tsv")); err == nil {
		return runDirsFromSeriesIndex(root, filepath.Join(dir, "runs.tsv"))
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	var dirs []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path == dir {
			return nil
		}
		if runartifact.IsRunDir(path) {
			dirs = append(dirs, path)
			return filepath.SkipDir
		}
		runsPath := filepath.Join(path, "runs.tsv")
		if _, err := os.Stat(runsPath); err == nil {
			collected, err := runDirsFromSeriesIndex(root, runsPath)
			if err != nil {
				return err
			}
			dirs = append(dirs, collected...)
			return filepath.SkipDir
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

func runDirsFromSeriesIndex(root string, path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var dirs []string
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if lineNumber == 1 {
			continue
		}
		record := strings.Split(scanner.Text(), "\t")
		if len(record) == 0 {
			continue
		}
		ref := strings.TrimSpace(record[len(record)-1])
		if ref == "" {
			continue
		}
		resolved, ok, err := resolveRunReference(root, ref)
		if err != nil {
			return nil, err
		}
		if ok {
			dirs = append(dirs, resolved)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return dirs, nil
}

func resolveRunReference(root string, ref string) (string, bool, error) {
	candidates := []string{ref}
	if !filepath.IsAbs(ref) {
		candidates = append(candidates, filepath.Join(root, ref), filepath.Join(root, "runs", ref))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() && runartifact.IsRunDir(candidate) {
				abs, err := filepath.Abs(candidate)
				if err != nil {
					return "", false, err
				}
				return abs, true, nil
			}
			continue
		}
		if !os.IsNotExist(err) {
			return "", false, err
		}
	}
	return "", false, nil
}

func countMetricSamples(path string) (int, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, true, err
		}
		return 0, true, nil
	}
	count := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, true, err
	}
	return count, true, nil
}

func sortSummaries(summaries []Summary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].StartedAt != summaries[j].StartedAt {
			return summaries[i].StartedAt > summaries[j].StartedAt
		}
		return summaries[i].RunID < summaries[j].RunID
	})
}

func displayPath(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func defaultValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func tableCell(value string) string {
	return strings.ReplaceAll(value, "|", `\|`)
}
