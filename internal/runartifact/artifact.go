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

func FormatMetric(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	if value == math.Trunc(value) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
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
