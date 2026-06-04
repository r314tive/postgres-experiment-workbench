package runartifact

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
)

type Env map[string]string

type MetricStats struct {
	First float64
	Last  float64
	Delta float64
	Min   float64
	Max   float64
	Count int
	Valid bool
}

type AggregateStats struct {
	Count  int
	Min    float64
	Avg    float64
	Max    float64
	Stddev float64
	Valid  bool
}

type RunInfo struct {
	Dir          string
	RunID        string
	Status       string
	WorkloadExit string
	Experiment   string
	PGConfig     string
	ProfileSize  string
	Workload     string
}

var CumulativeMetrics = []string{
	"xact_commit",
	"xact_rollback",
	"blks_read",
	"blks_hit",
	"tup_inserted",
	"tup_updated",
	"tup_deleted",
	"conflicts",
	"deadlocks",
	"temp_files",
	"temp_bytes",
	"wal_records",
	"wal_fpi",
	"wal_bytes",
}

var GaugeMetrics = []string{
	"active_sessions",
	"waiting_sessions",
	"lock_waiting_sessions",
	"blocked_sessions",
	"locks_total",
	"locks_waiting",
}

func ResolveRunDir(root string, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates, filepath.Join(root, input), filepath.Join(root, "runs", input))
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return filepath.Abs(candidate)
		}
	}

	return "", fmt.Errorf("run directory not found: %s", input)
}

func ResolveDir(root string, input string) (string, error) {
	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates, filepath.Join(root, input), filepath.Join(root, "runs", input))
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return filepath.Abs(candidate)
		}
	}

	return "", fmt.Errorf("directory not found: %s", input)
}

func IsRunDir(dir string) bool {
	for _, name := range []string{"manifest.env", "verdict.env", "metrics.csv"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func CollectRunDirs(root string, inputs []string) ([]string, error) {
	var runDirs []string
	seen := make(map[string]struct{})
	for _, input := range inputs {
		dir, err := ResolveDir(root, input)
		if err != nil {
			return nil, err
		}

		collected, err := collectRunDirsFromDir(root, dir)
		if err != nil {
			return nil, err
		}
		for _, runDir := range collected {
			abs, err := filepath.Abs(runDir)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[abs]; ok {
				continue
			}
			seen[abs] = struct{}{}
			runDirs = append(runDirs, abs)
		}
	}
	if len(runDirs) == 0 {
		return nil, fmt.Errorf("no run directories found")
	}
	return runDirs, nil
}

func collectRunDirsFromDir(root string, dir string) ([]string, error) {
	runsPath := filepath.Join(dir, "runs.tsv")
	if _, err := os.Stat(runsPath); err == nil {
		return runDirsFromTSV(root, runsPath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	if IsRunDir(dir) {
		return []string{dir}, nil
	}
	return nil, fmt.Errorf("not a series or run directory: %s", dir)
}

func runDirsFromTSV(root string, path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) <= 1 {
		return nil, nil
	}

	var runDirs []string
	for _, record := range records[1:] {
		if len(record) == 0 {
			continue
		}
		runDir := strings.TrimSpace(record[len(record)-1])
		if runDir == "" {
			continue
		}
		resolved, err := ResolveDir(root, runDir)
		if err != nil {
			return nil, err
		}
		if !IsRunDir(resolved) {
			return nil, fmt.Errorf("not an experiment run directory: %s", resolved)
		}
		runDirs = append(runDirs, resolved)
	}
	return runDirs, nil
}

func LoadRunInfo(dir string) (RunInfo, error) {
	manifest, err := LoadOptionalEnv(filepath.Join(dir, "manifest.env"))
	if err != nil {
		return RunInfo{}, err
	}
	verdict, err := LoadOptionalEnv(filepath.Join(dir, "verdict.env"))
	if err != nil {
		return RunInfo{}, err
	}
	return RunInfo{
		Dir:          dir,
		RunID:        manifest.Value("run_id", filepath.Base(dir)),
		Status:       verdict.Value("status", "missing"),
		WorkloadExit: verdict.Value("workload_exit", "0"),
		Experiment:   manifest.Value("experiment_spec_id", "unknown"),
		PGConfig:     manifest.Value("experiment_pg_config", "unknown"),
		ProfileSize:  manifest.Value("profile_size", "unknown"),
		Workload:     manifest.Value("workload_spec", ""),
	}, nil
}

func LoadOptionalEnv(path string) (Env, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Env{}, nil
		}
		return nil, err
	}
	values, err := envfile.Parse(path)
	if err != nil {
		return nil, err
	}
	return Env(values), nil
}

func (e Env) Value(key string, defaultValue string) string {
	if value, ok := e[key]; ok {
		return value
	}
	return defaultValue
}

func MetricStat(path string, column string) (MetricStats, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MetricStats{}, nil
		}
		return MetricStats{}, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		if err != io.EOF {
			return MetricStats{}, err
		}
		return MetricStats{}, nil
	}

	index := -1
	for i, name := range header {
		if name == column {
			index = i
			break
		}
	}
	if index < 0 {
		return MetricStats{}, nil
	}

	var stats MetricStats
	for {
		record, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return MetricStats{}, err
		}
		if index >= len(record) || record[index] == "" {
			continue
		}
		value, err := strconv.ParseFloat(record[index], 64)
		if err != nil {
			continue
		}
		if stats.Count == 0 {
			stats.First = value
			stats.Min = value
			stats.Max = value
		}
		stats.Last = value
		if value < stats.Min {
			stats.Min = value
		}
		if value > stats.Max {
			stats.Max = value
		}
		stats.Count++
	}

	if stats.Count == 0 {
		return MetricStats{}, nil
	}
	stats.Valid = true
	stats.Delta = stats.Last - stats.First
	return stats, nil
}

func RunMetricValue(runDir string, metric string, mode string) (float64, bool, error) {
	stats, err := MetricStat(filepath.Join(runDir, "metrics.csv"), metric)
	if err != nil {
		return 0, false, err
	}
	if !stats.Valid {
		return 0, false, nil
	}
	switch mode {
	case "delta":
		return stats.Delta, true, nil
	case "max":
		return stats.Max, true, nil
	default:
		return 0, false, fmt.Errorf("unsupported metric mode: %s", mode)
	}
}

func AggregateMetric(runDirs []string, metric string, mode string) (AggregateStats, error) {
	var values []float64
	for _, runDir := range runDirs {
		value, ok, err := RunMetricValue(runDir, metric, mode)
		if err != nil {
			return AggregateStats{}, err
		}
		if ok {
			values = append(values, value)
		}
	}
	return AggregateValues(values), nil
}

func AggregateValues(values []float64) AggregateStats {
	if len(values) == 0 {
		return AggregateStats{}
	}

	stats := AggregateStats{Count: len(values), Min: values[0], Max: values[0], Valid: true}
	var sum float64
	var sumSquares float64
	for _, value := range values {
		if value < stats.Min {
			stats.Min = value
		}
		if value > stats.Max {
			stats.Max = value
		}
		sum += value
		sumSquares += value * value
	}
	stats.Avg = sum / float64(len(values))
	variance := (sumSquares / float64(len(values))) - (stats.Avg * stats.Avg)
	if variance < 0 {
		variance = 0
	}
	stats.Stddev = math.Sqrt(variance)
	return stats
}

func FormatMetric(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	if value == math.Trunc(value) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func FormatMetricFixed(value float64, decimals bool) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	if decimals {
		return fmt.Sprintf("%.3f", value)
	}
	if value == math.Trunc(value) {
		return strconv.FormatInt(int64(value), 10)
	}
	return fmt.Sprintf("%.3f", value)
}

func ListRelativeFiles(root string, subdir string, limit int) ([]string, error) {
	dir := filepath.Join(root, subdir)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	var files []string
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Strings(files)
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	return files, nil
}
