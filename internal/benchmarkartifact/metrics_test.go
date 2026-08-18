package benchmarkartifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcontrol"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics"
	metricstest "github.com/r314tive/postgres-experiment-workbench/internal/benchmarkmetrics/testfixture"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkphase"
)

func TestArtifactMetricsRederiveReopensTypedControlsAndRawSources(t *testing.T) {
	runDir, plan, trial := writeControlVerificationFixture(t)
	measure, ok := benchmarkphase.EventByName(*trial.PhaseTimeline, benchmarkphase.MeasureName)
	if !ok {
		t.Fatal("control fixture has no measure phase")
	}
	if err := os.WriteFile(filepath.Join(runDir, benchmarkmetrics.SourcePath), metricstest.CSV([]string{
		measure.StartedAt, measure.FinishedAt,
	}, "postgres"), 0o600); err != nil {
		t.Fatal(err)
	}
	options, err := artifactMetricsOptions(runDir, 1, trial, &plan, "17", measure)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := benchmarkmetrics.DeriveFile(options)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Cadence.ControlDigest == "" || summary.StatisticsReset.ControlDigest == "" {
		t.Fatalf("metrics summary did not bind both typed controls: %#v", summary)
	}

	resetSource := filepath.Join(runDir, "artifacts", "benchmark", "controls", benchmarkcontrol.StatisticsResetSourceFile)
	if err := os.WriteFile(resetSource, []byte("record\tscope\tvalue\trows\tcommand_completed\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options, err = artifactMetricsOptions(runDir, 1, trial, &plan, "17", measure)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := benchmarkmetrics.DeriveFile(options); err == nil || !strings.Contains(err.Error(), "statistics-reset evidence") {
		t.Fatalf("tampered reset raw source passed independent metrics derivation: %v", err)
	}
}
