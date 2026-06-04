package runstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	content := fmt.Sprintf(
		"run_id=%s\nstarted_at=%s\nexperiment_spec=%s\nexperiment_spec_id=%s\nexperiment_name=%s\nexperiment_topology=%s\nexperiment_pg_config=%s\nprofile=%s\ndataset_spec=%s\nprofile_size=%s\nworkload_spec=%s\nbackground_specs=%s\nrun_dir=%s\n",
		manifest.RunID,
		manifest.StartedAt,
		manifest.ExperimentSpec,
		manifest.ExperimentSpecID,
		manifest.ExperimentName,
		manifest.ExperimentTopology,
		manifest.ExperimentPGConfig,
		manifest.Profile,
		manifest.DatasetSpec,
		manifest.ProfileSize,
		manifest.WorkloadSpec,
		manifest.BackgroundSpecs,
		manifest.RunDir,
	)
	return os.WriteFile(path, []byte(content), 0o644)
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

	envContent := fmt.Sprintf(
		"status=%s\nmessage=%s\nfinished_at=%s\nworkload_exit=%d\nassert_exit=%d\nscan_exit=%d\n",
		verdict.Status,
		verdict.Message,
		verdict.FinishedAt,
		verdict.WorkloadExit,
		verdict.AssertExit,
		verdict.ScanExit,
	)
	if err := os.WriteFile(filepath.Join(runDir, "verdict.env"), []byte(envContent), 0o644); err != nil {
		return err
	}

	jsonBytes, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return err
	}
	jsonBytes = append(jsonBytes, '\n')
	return os.WriteFile(filepath.Join(runDir, "verdict.json"), jsonBytes, 0o644)
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
