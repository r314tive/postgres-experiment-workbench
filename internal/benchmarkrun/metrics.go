package benchmarkrun

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

func benchmarkMetricsOptions(runDir string, plan benchmarkplan.Plan, runID string, trial int, postgresMajor string, measure benchmarkphase.Event, controls *ControlEvidence) (benchmarkmetrics.DeriveOptions, error) {
	options := benchmarkmetrics.DeriveOptions{
		Path: filepath.Join(runDir, benchmarkmetrics.SourcePath), PostgresServerMajor: postgresMajor,
		MeasureStartedAt: measure.StartedAt, MeasureFinishedAt: measure.FinishedAt,
		CollectorIntervalSeconds: plan.CollectorIntervalSeconds, ContractVersion: plan.ContractVersion,
		StatisticsResetPolicy: plan.StatisticsResetPolicy, StatisticsResetBoundary: plan.StatisticsResetBoundary,
	}
	if plan.ContractVersion != "2" {
		return options, nil
	}
	if controls == nil {
		return benchmarkmetrics.DeriveOptions{}, fmt.Errorf("protocol v2 metrics require normalized benchmark controls")
	}
	binding := benchmarkcontrol.Binding{RunID: runID, ProtocolDigest: plan.ProtocolDigest, Trial: trial}
	reset, resetRaw, err := readMetricsControl(runDir, controls.StatisticsReset, benchmarkcontrol.StatisticsResetFile, benchmarkcontrol.StatisticsResetSourceFile, benchmarkcontrol.ParseStatisticsReset)
	if err != nil {
		return benchmarkmetrics.DeriveOptions{}, fmt.Errorf("load statistics-reset metrics control: %w", err)
	}
	overhead, overheadRaw, err := readMetricsControl(runDir, controls.CollectorOverhead, benchmarkcontrol.CollectorOverheadFile, benchmarkcontrol.CollectorOverheadSourceFile, benchmarkcontrol.ParseCollectorOverhead)
	if err != nil {
		return benchmarkmetrics.DeriveOptions{}, fmt.Errorf("load collector-overhead metrics control: %w", err)
	}
	options.ControlBinding = &binding
	options.StatisticsReset = &reset
	options.StatisticsResetSource = resetRaw
	options.CollectorOverhead = &overhead
	options.CollectorOverheadSource = overheadRaw
	return options, nil
}

func readMetricsControl[T any](runDir string, ref ArtifactRef, artifactName, sourceName string, parse func([]byte) (T, error)) (T, []byte, error) {
	var zero T
	want := filepath.ToSlash(filepath.Join("artifacts", "benchmark", "controls", artifactName))
	if ref.Path != want {
		return zero, nil, fmt.Errorf("control reference path %q, want %q", ref.Path, want)
	}
	artifactPath, err := resolveMetricsControlPath(runDir, want)
	if err != nil {
		return zero, nil, err
	}
	artifactContent, err := readBoundedRegularFile(artifactPath, controlSourceLimit)
	if err != nil {
		return zero, nil, err
	}
	if int64(len(artifactContent)) != ref.Size || evidence.DigestBytes(artifactContent) != ref.Digest {
		return zero, nil, fmt.Errorf("control reference digest or size mismatch")
	}
	artifact, err := parse(artifactContent)
	if err != nil {
		return zero, nil, err
	}
	sourcePath, err := resolveMetricsControlPath(runDir, filepath.ToSlash(filepath.Join("artifacts", "benchmark", "controls", sourceName)))
	if err != nil {
		return zero, nil, err
	}
	source, err := readBoundedRegularFile(sourcePath, controlSourceLimit)
	if err != nil {
		return zero, nil, err
	}
	return artifact, source, nil
}

func resolveMetricsControlPath(runDir, relative string) (string, error) {
	root, err := filepath.Abs(runDir)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil || resolvedRoot != root {
		return "", fmt.Errorf("linked run directory is missing, symlinked, or non-canonical")
	}
	want := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.EvalSymlinks(want)
	if err != nil || resolved != want {
		return "", fmt.Errorf("metrics control path is missing, symlinked, or non-canonical: %s", relative)
	}
	inside, err := filepath.Rel(root, resolved)
	if err != nil || inside == ".." || filepath.IsAbs(inside) || len(inside) >= 3 && inside[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("metrics control path escapes linked run: %s", relative)
	}
	return resolved, nil
}

func readBoundedRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("%s is not a bounded non-empty regular file", filepath.Base(path))
	}
	return os.ReadFile(path)
}
