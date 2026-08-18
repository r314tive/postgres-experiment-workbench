package benchmarkartifact

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const metricsControlSourceLimit = int64(2 << 20)

func artifactMetricsOptions(runDir string, number int, trial benchmarkrun.Trial, plan *benchmarkplan.Plan, postgresMajor string, measure benchmarkphase.Event) (benchmarkmetrics.DeriveOptions, error) {
	if plan == nil {
		return benchmarkmetrics.DeriveOptions{}, fmt.Errorf("valid protocol plan is required")
	}
	options := benchmarkmetrics.DeriveOptions{
		Path: filepath.Join(runDir, benchmarkmetrics.SourcePath), PostgresServerMajor: postgresMajor,
		MeasureStartedAt: measure.StartedAt, MeasureFinishedAt: measure.FinishedAt,
		CollectorIntervalSeconds: plan.CollectorIntervalSeconds, ContractVersion: plan.ContractVersion,
		StatisticsResetPolicy: plan.StatisticsResetPolicy, StatisticsResetBoundary: plan.StatisticsResetBoundary,
	}
	if plan.ContractVersion != "2" {
		return options, nil
	}
	if trial.Controls == nil {
		return benchmarkmetrics.DeriveOptions{}, fmt.Errorf("protocol v2 trial has no control references")
	}
	binding := benchmarkcontrol.Binding{RunID: trial.RunID, ProtocolDigest: plan.ProtocolDigest, Trial: number}
	reset, resetRaw, err := readArtifactMetricsControl(runDir, trial.Controls.StatisticsReset, benchmarkcontrol.StatisticsResetFile, benchmarkcontrol.StatisticsResetSourceFile, benchmarkcontrol.ParseStatisticsReset)
	if err != nil {
		return benchmarkmetrics.DeriveOptions{}, fmt.Errorf("statistics-reset control: %w", err)
	}
	overhead, overheadRaw, err := readArtifactMetricsControl(runDir, trial.Controls.CollectorOverhead, benchmarkcontrol.CollectorOverheadFile, benchmarkcontrol.CollectorOverheadSourceFile, benchmarkcontrol.ParseCollectorOverhead)
	if err != nil {
		return benchmarkmetrics.DeriveOptions{}, fmt.Errorf("collector-overhead control: %w", err)
	}
	options.ControlBinding = &binding
	options.StatisticsReset = &reset
	options.StatisticsResetSource = resetRaw
	options.CollectorOverhead = &overhead
	options.CollectorOverheadSource = overheadRaw
	return options, nil
}

func readArtifactMetricsControl[T any](runDir string, ref benchmarkrun.ArtifactRef, artifactName, sourceName string, parse func([]byte) (T, error)) (T, []byte, error) {
	var zero T
	want := filepath.ToSlash(filepath.Join("artifacts", "benchmark", "controls", artifactName))
	if ref.Path != want {
		return zero, nil, fmt.Errorf("reference path %q, want %q", ref.Path, want)
	}
	artifactPath, err := safeExistingJoin(runDir, want)
	if err != nil {
		return zero, nil, err
	}
	content, err := readArtifactMetricsFile(artifactPath)
	if err != nil {
		return zero, nil, err
	}
	if int64(len(content)) != ref.Size || evidence.DigestBytes(content) != ref.Digest {
		return zero, nil, fmt.Errorf("normalized control reference digest or size mismatch")
	}
	artifact, err := parse(content)
	if err != nil {
		return zero, nil, err
	}
	sourcePath, err := safeExistingJoin(runDir, filepath.ToSlash(filepath.Join("artifacts", "benchmark", "controls", sourceName)))
	if err != nil {
		return zero, nil, err
	}
	source, err := readArtifactMetricsFile(sourcePath)
	if err != nil {
		return zero, nil, err
	}
	return artifact, source, nil
}

func readArtifactMetricsFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > metricsControlSourceLimit {
		return nil, fmt.Errorf("%s is not a bounded non-empty regular file", filepath.Base(path))
	}
	return os.ReadFile(path)
}
