package runstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Manifest struct {
	RunID              string
	StartedAt          string
	ExperimentSpec     string
	ExperimentSpecID   string
	ExperimentName     string
	ExperimentTopology string
	ExperimentPGConfig string
	Profile            string
	DatasetSpec        string
	ProfileSize        string
	WorkloadSpec       string
	BackgroundSpecs    string
	RunDir             string
}

type Verdict struct {
	RunID            string `json:"run_id"`
	Status           string `json:"status"`
	Message          string `json:"message"`
	StartedAt        string `json:"started_at"`
	FinishedAt       string `json:"finished_at"`
	ExperimentSpecID string `json:"experiment_spec"`
	RunDir           string `json:"run_dir"`
	WorkloadExit     int    `json:"workload_exit"`
	AssertExit       int    `json:"assert_exit"`
	ScanExit         int    `json:"scan_exit"`
}

func ManifestFromEnv(getenv func(string) string) Manifest {
	experimentSpecID := getenv("EXPERIMENT_SPEC_ID")
	return Manifest{
		RunID:              getenv("RUN_ID"),
		StartedAt:          getenv("STARTED_AT"),
		ExperimentSpec:     getenv("EXPERIMENT_SPEC_FILE"),
		ExperimentSpecID:   experimentSpecID,
		ExperimentName:     valueOr(getenv("EXPERIMENT_NAME"), experimentSpecID),
		ExperimentTopology: valueOr(getenv("EXPERIMENT_TOPOLOGY"), "single"),
		ExperimentPGConfig: valueOr(getenv("EXPERIMENT_PG_CONFIG"), valueOr(getenv("PG_CONFIG"), "default")),
		Profile:            getenv("EXPERIMENT_PROFILE"),
		DatasetSpec:        getenv("EXPERIMENT_DATASET_SPEC"),
		ProfileSize:        valueOr(getenv("EXPERIMENT_PROFILE_SIZE"), valueOr(getenv("PROFILE_SIZE"), "small")),
		WorkloadSpec:       getenv("EXPERIMENT_WORKLOAD_SPEC"),
		BackgroundSpecs:    getenv("EXPERIMENT_BACKGROUND_SPECS"),
		RunDir:             getenv("RUN_DIR"),
	}
}

func VerdictFromEnv(getenv func(string) string, status string, message string, finishedAt string) Verdict {
	if finishedAt == "" {
		finishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return Verdict{
		RunID:            getenv("RUN_ID"),
		Status:           status,
		Message:          message,
		StartedAt:        getenv("STARTED_AT"),
		FinishedAt:       finishedAt,
		ExperimentSpecID: getenv("EXPERIMENT_SPEC_ID"),
		RunDir:           getenv("RUN_DIR"),
		WorkloadExit:     intFromEnv(getenv, "WORKLOAD_EXIT"),
		AssertExit:       intFromEnv(getenv, "ASSERT_EXIT"),
		ScanExit:         intFromEnv(getenv, "SCAN_EXIT"),
	}
}

func WriteManifest(runDir string, manifest Manifest) error {
	if runDir == "" {
		runDir = manifest.RunDir
	}
	if runDir == "" {
		return fmt.Errorf("run dir is required")
	}
	manifest.RunDir = runDir
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(runDir, "manifest.env")
	manifestLines := []string{
		"run_id=" + quoteEnvValue(manifest.RunID),
		"started_at=" + quoteEnvValue(manifest.StartedAt),
		"experiment_spec=" + quoteEnvValue(manifest.ExperimentSpec),
		"experiment_spec_id=" + quoteEnvValue(manifest.ExperimentSpecID),
		"experiment_name=" + quoteEnvValue(manifest.ExperimentName),
		"experiment_topology=" + quoteEnvValue(manifest.ExperimentTopology),
		"experiment_pg_config=" + quoteEnvValue(manifest.ExperimentPGConfig),
		"profile=" + quoteEnvValue(manifest.Profile),
		"dataset_spec=" + quoteEnvValue(manifest.DatasetSpec),
		"profile_size=" + quoteEnvValue(manifest.ProfileSize),
		"workload_spec=" + quoteEnvValue(manifest.WorkloadSpec),
		"background_specs=" + quoteEnvValue(manifest.BackgroundSpecs),
		"run_dir=" + quoteEnvValue(manifest.RunDir),
	}
	content := strings.Join(manifestLines, "\n") + "\n"
	return writeEnvFile(path, content)
}

func WriteVerdict(runDir string, verdict Verdict) error {
	if runDir == "" {
		runDir = verdict.RunDir
	}
	if runDir == "" {
		return fmt.Errorf("run dir is required")
	}
	verdict.RunDir = runDir
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}

	verdictLines := []string{
		"status=" + quoteEnvValue(verdict.Status),
		"message=" + quoteEnvValue(verdict.Message),
		"finished_at=" + quoteEnvValue(verdict.FinishedAt),
		"workload_exit=" + quoteEnvValue(fmt.Sprintf("%d", verdict.WorkloadExit)),
		"assert_exit=" + quoteEnvValue(fmt.Sprintf("%d", verdict.AssertExit)),
		"scan_exit=" + quoteEnvValue(fmt.Sprintf("%d", verdict.ScanExit)),
	}
	if err := writeEnvFile(filepath.Join(runDir, "verdict.env"), strings.Join(verdictLines, "\n")+"\n"); err != nil {
		return err
	}

	jsonBytes, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return err
	}
	jsonBytes = append(jsonBytes, '\n')
	return os.WriteFile(filepath.Join(runDir, "verdict.json"), jsonBytes, 0o644)
}

func writeEnvFile(path string, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func quoteEnvValue(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"':
			out.WriteString("\\\"")
		case '\\':
			out.WriteString("\\\\")
		case '`':
			out.WriteString("\\`")
		case '$':
			out.WriteString("\\$")
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		default:
			out.WriteByte(value[i])
		}
	}
	out.WriteByte('"')
	return out.String()
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func intFromEnv(getenv func(string) string, key string) int {
	value := getenv(key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}
