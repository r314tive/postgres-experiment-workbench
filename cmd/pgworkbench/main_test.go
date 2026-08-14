package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkab"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkexternal"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestReportCommandsRejectExistingOutputWithoutMutation(t *testing.T) {
	commands := []struct {
		name string
		args func(string) []string
	}{
		{name: "run", args: func(output string) []string { return []string{"run", "missing-run", output} }},
		{name: "summary", args: func(output string) []string { return []string{"summary", "--output", output, "missing-run"} }},
		{name: "history", args: func(output string) []string { return []string{"history", "--output", output, "missing-run"} }},
	}
	types := []string{"regular", "symlink", "directory"}
	for _, command := range commands {
		for _, outputType := range types {
			t.Run(command.name+"/"+outputType, func(t *testing.T) {
				root := t.TempDir()
				output := filepath.Join(root, "reports", "report.md")
				if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
					t.Fatal(err)
				}
				const sentinel = "do not replace\n"
				var sentinelPath string
				switch outputType {
				case "regular":
					sentinelPath = output
					if err := os.WriteFile(output, []byte(sentinel), 0o600); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					sentinelPath = filepath.Join(root, "sentinel.md")
					if err := os.WriteFile(sentinelPath, []byte(sentinel), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(sentinelPath, output); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				case "directory":
					if err := os.Mkdir(output, 0o755); err != nil {
						t.Fatal(err)
					}
				}

				if err := runReport(root, command.args(output)); err == nil {
					t.Fatal("existing output was accepted")
				}
				if outputType == "directory" {
					info, err := os.Lstat(output)
					if err != nil || !info.IsDir() {
						t.Fatalf("existing directory was replaced: info=%v err=%v", info, err)
					}
					return
				}
				content, err := os.ReadFile(sentinelPath)
				if err != nil {
					t.Fatal(err)
				}
				if string(content) != sentinel {
					t.Fatalf("existing sentinel changed: %q", content)
				}
				if outputType == "symlink" {
					info, err := os.Lstat(output)
					if err != nil || info.Mode()&os.ModeSymlink == 0 {
						t.Fatalf("existing symlink was replaced: info=%v err=%v", info, err)
					}
				}
			})
		}
	}
}

func TestReportCommandsRejectOutputInsideImmutableInput(t *testing.T) {
	commands := []struct {
		name string
		args func(string, string) []string
	}{
		{name: "run", args: func(input, output string) []string { return []string{"run", input, output} }},
		{name: "summary", args: func(input, output string) []string { return []string{"summary", "--output", output, input} }},
		{name: "history", args: func(input, output string) []string { return []string{"history", "--output", output, input} }},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			root := t.TempDir()
			runDir := filepath.Join(root, "runs", "run-a")
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runDir, "manifest.env"), []byte("run_id=run-a\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(runDir, "derived", "report.md")
			if err := runReport(root, command.args(runDir, output)); err == nil {
				t.Fatal("report output inside immutable input was accepted")
			}
			if _, err := os.Lstat(filepath.Join(runDir, "derived")); !os.IsNotExist(err) {
				t.Fatalf("report command modified immutable input: %v", err)
			}
		})
	}
}

func TestRenderMaybeFileDoesNotPublishPartialOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join("reports", "failed.md")
	wantErr := errors.New("render failed")
	err := renderMaybeFile(root, output, "test report", func(file *os.File) error {
		if _, err := io.WriteString(file, "partial output\n"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("renderMaybeFile() error = %v, want %v", err, wantErr)
	}
	absolute := filepath.Join(root, output)
	if _, err := os.Lstat(absolute); !os.IsNotExist(err) {
		t.Fatalf("failed render published output: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(absolute))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed render left temporary files: %#v", entries)
	}
}

func TestRenderMaybeFilePublishesWithoutReplacingConcurrentOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "reports", "report.md")
	const sentinel = "concurrent writer wins\n"
	err := renderMaybeFile(root, output, "test report", func(file *os.File) error {
		if _, err := io.WriteString(file, "candidate output\n"); err != nil {
			return err
		}
		return os.WriteFile(output, []byte(sentinel), 0o600)
	})
	if err == nil {
		t.Fatal("concurrent output creation was overwritten")
	}
	content, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != sentinel {
		t.Fatalf("concurrent output changed: %q", content)
	}
	entries, readDirErr := os.ReadDir(filepath.Dir(output))
	if readDirErr != nil {
		t.Fatal(readDirErr)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(output) {
		t.Fatalf("exclusive publication left temporary files: %#v", entries)
	}
}

func TestRenderMaybeFilePublishesNewRegularFile(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "nested", "report.md")
	if err := renderMaybeFile(root, output, "test report", func(file *os.File) error {
		_, err := io.WriteString(file, "complete report\n")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		t.Fatalf("published output mode = %v, want regular 0644", info.Mode())
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "complete report\n" {
		t.Fatalf("published output = %q", content)
	}
}

func TestPackExportRejectsIncompatibleEngineBeforeCreatingDestination(t *testing.T) {
	root := testScenarioPack(t, ">=0.3.0")
	destination := filepath.Join(t.TempDir(), "export")

	err := runPack(root, []string{"export", "--engine-version", "0.2.9", destination})
	if err == nil || !strings.Contains(err.Error(), "migrate and retest") {
		t.Fatalf("expected migration diagnostic, got %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("incompatible export mutated destination: %v", statErr)
	}
}

func TestPackExportRejectsExplicitDevelopmentEngineGate(t *testing.T) {
	root := testScenarioPack(t, ">=0.2.0")
	destination := filepath.Join(t.TempDir(), "export")

	err := runPack(root, []string{"export", "--engine-version", "0.2.0-dev", destination})
	if err == nil || !strings.Contains(err.Error(), "cannot use development engine version") {
		t.Fatalf("expected development release-gate rejection, got %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("development-gated export mutated destination: %v", statErr)
	}
}

func TestReleaseSnapshotPinsCandidateAsEngineVersion(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	want := `pack export --engine-version "$(VERSION)" "$$out_dir"`
	if !strings.Contains(string(content), want) {
		t.Fatalf("release-snapshot must validate the pack against the exact candidate: missing %s", want)
	}
	if !strings.Contains(string(content), `sort -k2,2 -o "$(RELEASE_CHECKSUM_FILE)"`) {
		t.Fatal("release checksum file must be sorted by artifact path, not digest")
	}
}

func TestExperimentRunRejectsIncompatiblePackBeforeRuntime(t *testing.T) {
	root := testScenarioPack(t, "^0.3.0")
	previousVersion := version
	version = "0.2.9"
	t.Cleanup(func() { version = previousVersion })

	err := runExperiment(root, speccatalog.New(root), []string{"run", "smoke"})
	if err == nil || !strings.Contains(err.Error(), "scenario pack test-pack requires pgworkbench ^0.3.0") {
		t.Fatalf("expected pre-runtime engine incompatibility, got %v", err)
	}
}

func TestExperimentRunJSONOmitsUninitializedResult(t *testing.T) {
	var output strings.Builder
	if err := renderExperimentRunResult(&output, true, experimentrun.Result{}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("pre-result failure emitted invalid schema JSON: %q", output.String())
	}

	initialized := experimentrun.Result{SchemaVersion: experimentrun.SchemaVersion}
	if err := renderExperimentRunResult(&output, true, initialized); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"schema_version": "`+experimentrun.SchemaVersion+`"`) {
		t.Fatalf("initialized experiment result was not rendered: %q", output.String())
	}
}

func TestReleaseArchiveCreateCLI(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "pgworkbench-1.0.0-linux-amd64.tar.gz")
	err := runRelease("", []string{
		"archive", "create",
		"--source", source,
		"--output", output,
		"--root-name", "pgworkbench-1.0.0-linux-amd64",
		"--epoch", "1700000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatalf("release archive was not created: info=%v err=%v", info, err)
	}
}

func TestParseRunVerifyArgs(t *testing.T) {
	jsonOutput, requireBundleInventory, inputs, err := parseRunVerifyArgs([]string{"--bundle", "--json", "imported/run-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonOutput || !requireBundleInventory || len(inputs) != 1 || inputs[0] != "imported/run-a" {
		t.Fatalf("unexpected parsed verification args: json=%t bundle=%t inputs=%#v", jsonOutput, requireBundleInventory, inputs)
	}

	if _, _, _, err := parseRunVerifyArgs([]string{"--inventory-optional", "run-a"}); err == nil {
		t.Fatal("unknown verification option was accepted")
	}
}

func TestParseBenchmarkHostInspectArgs(t *testing.T) {
	options, err := parseBenchmarkHostInspectArgs([]string{
		"--json",
		"--output", "host.json",
		"--storage-path", "/data/postgres",
		"--storage-label", "postgres-data",
		"--client-placement", "separate-host",
		"--strict",
		"--min-logical-cpus", "8",
		"--min-memory-available-pct", "25",
		"--min-storage-available-pct", "30",
		"--max-load-1m-per-cpu", "0.5",
		"--required-clocksource", "tsc",
		"--required-governor", "performance",
		"--max-temperature-celsius", "70",
		"--required-client-placement", "separate-host",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := options.Inspect.Policy
	if !options.JSON || options.Output != "host.json" || options.Inspect.StoragePath != "/data/postgres" || options.Inspect.StorageLabel != "postgres-data" || options.Inspect.ClientPlacement != "separate-host" || !policy.Strict {
		t.Fatalf("unexpected host inspection options: %#v", options)
	}
	if policy.MinLogicalCPUs == nil || *policy.MinLogicalCPUs != 8 || policy.MinMemoryAvailablePct == nil || *policy.MinMemoryAvailablePct != 25 || policy.MinStorageAvailablePct == nil || *policy.MinStorageAvailablePct != 30 || policy.MaxLoad1PerCPU == nil || *policy.MaxLoad1PerCPU != 0.5 || policy.MaxTemperatureCelsius == nil || *policy.MaxTemperatureCelsius != 70 {
		t.Fatalf("numeric host qualification gates were not parsed: %#v", policy)
	}
	if policy.RequiredClocksource != "tsc" || policy.RequiredGovernor != "performance" || policy.RequiredClientPlacement != "separate-host" {
		t.Fatalf("string host qualification gates were not parsed: %#v", policy)
	}
}

func TestParseBenchmarkImportArgs(t *testing.T) {
	options, inputs, err := parseBenchmarkImportArgs([]string{
		"hammerdb6", "--json", "--manifest", "mapping.json", "source.json", "imported",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !options.JSON || options.Adapter != "hammerdb6" || options.Import.MappingPath != "mapping.json" || len(inputs) != 2 || inputs[0] != "source.json" || inputs[1] != "imported" {
		t.Fatalf("unexpected benchmark import options: options=%#v inputs=%#v", options, inputs)
	}
	for _, adapter := range []string{"hammerdb6report", "benchbase33c0047"} {
		options, inputs, err := parseBenchmarkImportArgs([]string{adapter, "source.json", "imported"})
		if err != nil {
			t.Fatalf("parse pinned adapter %s: %v", adapter, err)
		}
		if options.Adapter != adapter || len(inputs) != 2 {
			t.Fatalf("unexpected pinned adapter options: options=%#v inputs=%#v", options, inputs)
		}
	}

	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"unknown", "source", "out"}, "unsupported benchmark import adapter"},
		{[]string{"sysbench1", "--manifest"}, "requires a value"},
		{[]string{"sysbench1", "--json", "--json", "source", "out"}, "duplicate option"},
		{[]string{"sysbench1", "--execute", "source", "out"}, "unknown option"},
	} {
		if _, _, err := parseBenchmarkImportArgs(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("args %#v: got %v, want %q", test.args, err, test.want)
		}
	}
}

func TestParseBenchmarkDriverRunArgs(t *testing.T) {
	options, jsonOutput, err := parseBenchmarkDriverRunArgs([]string{
		"--json", "--acknowledge-external-disposable-target", "--driver", "sysbench-postgresql-1.0.20",
		"--runtime-root", "/opt/sysbench-runtime",
		"--binary", "/opt/sysbench", "--config", "run.json",
		"--script", "oltp.lua", "--workload", "oltp_read_write/postgresql",
		"--timeout", "45m", "execution",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonOutput || !options.AcknowledgeExternalDisposableTarget || options.DriverID != "sysbench-postgresql-1.0.20" || options.RuntimeRoot != "/opt/sysbench-runtime" || options.BinaryPath != "/opt/sysbench" || options.ConfigPath != "run.json" || options.ScriptPath != "oltp.lua" || options.Workload != "oltp_read_write/postgresql" || options.Timeout != 45*time.Minute || options.OutputDir != "execution" {
		t.Fatalf("unexpected external driver run options: %#v json=%t", options, jsonOutput)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--driver", "id", "--binary", "bin", "--config", "cfg", "--script", "script", "out"}, "usage"},
		{[]string{"--driver", "id", "--runtime-root", "root", "--binary", "bin", "--config", "cfg", "--script", "script", "--workload", "work", "out"}, "usage"},
		{[]string{"--driver", "id", "--driver", "other", "--runtime-root", "root", "--binary", "bin", "--config", "cfg", "--script", "script", "--workload", "work", "out"}, "duplicate option"},
		{[]string{"--driver", "id", "--runtime-root", "root", "--binary", "bin", "--config", "cfg", "--script", "script", "--workload", "work", "--timeout", "later", "out"}, "invalid --timeout"},
		{[]string{"--driver", "id", "--runtime-root", "root", "--binary", "bin", "--config", "cfg", "--script", "script", "--workload", "work", "--arg", "unsafe", "out"}, "unknown option"},
	} {
		if _, _, err := parseBenchmarkDriverRunArgs(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("args %#v: got %v, want %q", test.args, err, test.want)
		}
	}
}

func TestBenchmarkDriverRunCLIProducesVerifiableExecution(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	runtimeRoot := filepath.Join(fixture, "sysbench-runtime")
	binary := filepath.Join(runtimeRoot, "bin", "sysbench")
	config := filepath.Join(fixture, "config.json")
	script := filepath.Join(runtimeRoot, "share", "sysbench", "oltp_read_write.lua")
	common := filepath.Join(runtimeRoot, "share", "sysbench", "oltp_common.lua")
	output := filepath.Join(fixture, "execution")
	fake := `#!/bin/sh
cat <<'PGWORKBENCH_RESULT'
sysbench 1.0.20
SQL statistics:
    transactions:                        1000   (100.00 per sec.)
    ignored errors:                      0      (0.00 per sec.)
General statistics:
    total time:                          10.0000s
    total number of events:              1000
Latency (ms):
    avg:                                 1.00
    95th percentile:                     1.10
PGWORKBENCH_RESULT
`
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	configuration := `{
  "schema_version":"pgworkbench.sysbench-native-run-config/v1",
  "artifact_type":"pgworkbench.sysbench-native-run-config",
  "threads":1,"duration_seconds":10,"report_interval_seconds":1,"rate":0,"random_seed":7,
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"postgres","database":"bench"}
}`
	if err := os.WriteFile(config, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("-- retained workload\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(common, []byte("-- pinned common runtime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(benchmarkexternal.SecretPasswordEnv, "")
	err = runBenchmark(root, speccatalog.New(root), []string{
		"driver-run", "--acknowledge-external-disposable-target", "--driver", "sysbench-postgresql-1.0.20",
		"--runtime-root", runtimeRoot, "--binary", binary, "--config", config, "--script", script,
		"--workload", "oltp_read_write/postgresql", "--timeout", "30s", output,
	})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := benchmarkexternal.Verify(output)
	if err != nil || !verification.IsValid() {
		t.Fatalf("driver-run CLI produced an invalid artifact: verification=%#v err=%v", verification, err)
	}
	if err := runBenchmark(root, speccatalog.New(root), []string{"driver-run-verify", output}); err != nil {
		t.Fatalf("driver-run-verify CLI rejected its artifact: %v", err)
	}
}

func TestParseBenchmarkSamplerV2Args(t *testing.T) {
	options, err := parseBenchmarkSamplerV2Args([]string{
		"--run-dir", "/pack/runs/series-t001",
		"--interval-seconds", "2",
		"--duration-seconds", "30",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.RunDir != "/pack/runs/series-t001" || options.Interval != 2*time.Second || options.Duration != 30*time.Second || options.Samples != 0 {
		t.Fatalf("unexpected sampler options: %#v", options)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--run-dir", "relative", "--interval-seconds", "1", "--samples", "1"}, "must be absolute"},
		{[]string{"--run-dir", "/pack/runs/id", "--interval-seconds", "1"}, "usage:"},
		{[]string{"--run-dir", "/pack/runs/id", "--interval-seconds", "1", "--samples", "1", "--duration-seconds", "1"}, "usage:"},
		{[]string{"--run-dir", "/pack/runs/id", "--interval-seconds", "0", "--samples", "1"}, "between 1 and 3600"},
		{[]string{"--run-dir", "/pack/runs/id", "--interval-seconds", "1", "--samples", "1", "extra"}, "unknown option"},
	} {
		if _, err := parseBenchmarkSamplerV2Args(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("args %#v: error=%v, want %q", test.args, err, test.want)
		}
	}
}

func TestParseBenchmarkControlMaterializerV2Args(t *testing.T) {
	runDir, err := parseBenchmarkControlMaterializerV2Args([]string{"--run-dir", "/pack/runs/series-t001"})
	if err != nil {
		t.Fatal(err)
	}
	if runDir != "/pack/runs/series-t001" {
		t.Fatalf("unexpected materializer run directory: %q", runDir)
	}
	for _, args := range [][]string{
		{"--run-dir", "relative"},
		{"--run-dir"},
		{"--run-dir", "/pack/runs/id", "extra"},
		{"--output", "/pack/runs/id"},
	} {
		if _, err := parseBenchmarkControlMaterializerV2Args(args); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("args %#v: error=%v, want exact usage rejection", args, err)
		}
	}
}

func TestBenchmarkControlMaterializerV2CLIDispatchesToBoundProducer(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"PGWORKBENCH_BENCHMARK_RUN_ID", "PGWORKBENCH_BENCHMARK_SERIES_ID", "PGWORKBENCH_BENCHMARK_TRIAL",
	} {
		t.Setenv(key, "")
	}
	err = runBenchmark(root, speccatalog.New(root), []string{"materialize-controls-v2", "--run-dir", root})
	if err == nil || !strings.Contains(err.Error(), "canonical series/run/trial bindings") {
		t.Fatalf("materializer CLI dispatch error = %v, want producer binding rejection", err)
	}
}

func TestParsePGDrillBridgeArgs(t *testing.T) {
	exportOptions, exportInputs, err := parsePGDrillBridgeExportArgs([]string{
		"--json", "--bundle", "--reviewed-predicate-file", "predicate.sql", "run-a", "baseline.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exportOptions["json"] != "1" || exportOptions["bundle"] != "1" || exportOptions["reviewed-predicate-file"] != "predicate.sql" || len(exportInputs) != 2 {
		t.Fatalf("unexpected pgdrill bridge export parse: options=%#v inputs=%#v", exportOptions, exportInputs)
	}
	verifyOptions, verifyInputs, err := parsePGDrillBridgeVerifyArgs([]string{"--json", "--source", "run-a", "baseline.json"})
	if err != nil {
		t.Fatal(err)
	}
	if verifyOptions["json"] != "1" || verifyOptions["source"] != "run-a" || len(verifyInputs) != 1 || verifyInputs[0] != "baseline.json" {
		t.Fatalf("unexpected pgdrill bridge verify parse: options=%#v inputs=%#v", verifyOptions, verifyInputs)
	}

	for _, args := range [][]string{
		{"--bundle", "--bundle", "run-a", "baseline.json"},
		{"--reviewed-predicate-file"},
		{"--predicate", "SELECT true", "run-a", "baseline.json"},
	} {
		if _, _, err := parsePGDrillBridgeExportArgs(args); err == nil {
			t.Fatalf("unsafe or ambiguous bridge export options were accepted: %#v", args)
		}
	}
	for _, args := range [][]string{
		{"--source", "a", "--source", "b", "baseline.json"},
		{"--source"},
		{"--execute", "baseline.json"},
	} {
		if _, _, err := parsePGDrillBridgeVerifyArgs(args); err == nil {
			t.Fatalf("unsafe or ambiguous bridge verify options were accepted: %#v", args)
		}
	}
}

func TestParseBenchmarkCampaignRunArgs(t *testing.T) {
	options, inputs, err := parseBenchmarkCampaignRunArgs([]string{
		"--json", "--runtime", "native", "--campaign-id", "client-sweep", "--subject", "postgres-17", "clients-1", "clients-8",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !options.JSON || options.Run.Runtime != "native" || options.Run.CampaignID != "client-sweep" || options.Run.Subject != "postgres-17" || len(inputs) != 2 || inputs[0] != "clients-1" || inputs[1] != "clients-8" {
		t.Fatalf("unexpected benchmark campaign options: options=%#v inputs=%#v", options, inputs)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--runtime", "remote", "one"}, "unsupported runtime"},
		{[]string{"--campaign-id", strings.Repeat("a", 176), "one"}, "invalid benchmark campaign id"},
		{[]string{"--subject"}, "requires a value"},
		{[]string{"--json", "--json", "one"}, "duplicate option"},
		{[]string{"--score", "one"}, "unknown option"},
	} {
		if _, _, err := parseBenchmarkCampaignRunArgs(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("args %#v: got %v, want %q", test.args, err, test.want)
		}
	}
}

func TestBenchmarkImportWorksOutsideScenarioPack(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "..", "internal", "benchmarkimport", "testdata", "sysbench-1.0-oltp.txt"))
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDirectory) })
	output := filepath.Join(outside, "sysbench-import")
	if err := run([]string{"benchmark", "import", "sysbench1", "--workload", "oltp_read_write/postgresql", source, output}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"benchmark", "import-verify", filepath.Join(output, "result.json")}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"benchmark", "import-verify", "--bundle", filepath.Join(output, "result.json")}); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("--bundle did not require the enclosing portable inventory: %v", err)
	}
	bundle := filepath.Join(outside, "sysbench-import.tar.gz")
	if err := run([]string{"benchmark", "import-bundle", filepath.Join(output, "result.json"), bundle}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(bundle); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("benchmark import bundle was not created: info=%v err=%v", info, err)
	}
}

func TestParseBenchmarkHostInspectArgsRejectsAmbiguity(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "duplicate", args: []string{"--output", "a.json", "--output", "b.json"}, want: "duplicate option"},
		{name: "missing value", args: []string{"--storage-label"}, want: "requires a value"},
		{name: "unknown", args: []string{"--attest"}, want: "unknown option"},
		{name: "positional", args: []string{"host-a"}, want: "does not accept positional"},
		{name: "trailing positional", args: []string{"--", "host-a"}, want: "does not accept positional"},
		{name: "zero cpus", args: []string{"--min-logical-cpus", "0"}, want: "positive integer"},
		{name: "bad number", args: []string{"--max-load-1m-per-cpu", "many"}, want: "must be a number"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseBenchmarkHostInspectArgs(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestParseBenchmarkABRunArgsBindsAnalysisAndQualification(t *testing.T) {
	options, inputs, err := parseBenchmarkABRunArgs([]string{
		"--json",
		"--runtime", "native",
		"--run-id", "ab-contract",
		"--baseline-subject", "default",
		"--candidate-subject", "tuned",
		"--bootstrap-resamples", "2000",
		"--confidence", "0.99",
		"--seed", "42",
		"--max-bookend-gap-seconds", "1800",
		"--storage-path", "/data/postgres",
		"--storage-label", "postgres-data",
		"--client-placement", "separate-host",
		"--strict",
		"--min-memory-available-pct", "20",
		"--min-storage-available-pct", "25",
		"--max-load-1m-per-cpu", "0.5",
		"--required-clocksource", "tsc",
		"--required-governor", "performance",
		"--required-client-placement", "separate-host",
		"pgbench/default", "pgbench/tuned",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !options.JSON || options.Run.Runtime != "native" || options.Run.RunID != "ab-contract" || options.Run.BaselineSubject != "default" || options.Run.CandidateSubject != "tuned" || options.Run.BootstrapResamples != 2000 || options.Run.ConfidenceLevel != 0.99 || options.Run.Seed != 42 || options.Run.MaxBookendGapSeconds != 1800 {
		t.Fatalf("unexpected A/B options: %#v", options)
	}
	if len(inputs) != 2 || inputs[0] != "pgbench/default" || inputs[1] != "pgbench/tuned" {
		t.Fatalf("unexpected A/B inputs: %#v", inputs)
	}
	policy := options.Run.Qualification.Policy
	if !policy.Strict || policy.MinMemoryAvailablePct == nil || *policy.MinMemoryAvailablePct != 20 || policy.MinStorageAvailablePct == nil || *policy.MinStorageAvailablePct != 25 || policy.MaxLoad1PerCPU == nil || *policy.MaxLoad1PerCPU != 0.5 || policy.RequiredClocksource != "tsc" || policy.RequiredGovernor != "performance" || policy.RequiredClientPlacement != "separate-host" {
		t.Fatalf("A/B qualification policy was not bound: %#v", policy)
	}
}

func TestParseBenchmarkABRunArgsRejectsUnsafeOrAmbiguousOptions(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--runtime", "remote"}, "expected docker or native"},
		{[]string{"--run-id", "../escape"}, "invalid counterbalanced"},
		{[]string{"--confidence", "1"}, "greater than 0.5"},
		{[]string{"--bootstrap-resamples", "10"}, "between 1000"},
		{[]string{"--max-bookend-gap-seconds", "0"}, "positive integer"},
		{[]string{"--seed", "negative"}, "uint64"},
		{[]string{"--strict", "--strict"}, "duplicate option"},
		{[]string{"--subject-dimension", "native_toolchain", "--runtime", "native"}, "requires both"},
		{[]string{"--subject-dimension", "native_toolchain", "--runtime", "docker", "--baseline-native-bindir", "/a", "--candidate-native-bindir", "/b"}, "requires --runtime native"},
		{[]string{"--baseline-native-bindir", "/a"}, "require --subject-dimension"},
		{[]string{"--subject-dimension", "native_toolchain", "--runtime", "native", "--baseline-native-bindir", "relative", "--candidate-native-bindir", "/b"}, "clean absolute"},
		{[]string{"--attest"}, "unknown option"},
	} {
		if _, _, err := parseBenchmarkABRunArgs(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("args %#v: got %v, want %q", test.args, err, test.want)
		}
	}
}

func TestParseBenchmarkABRunArgsBindsNativeToolchains(t *testing.T) {
	options, inputs, err := parseBenchmarkABRunArgs([]string{
		"--runtime", "native", "--subject-dimension", "native_toolchain",
		"--baseline-native-bindir", "/opt/postgres-a/bin",
		"--candidate-native-bindir", "/opt/postgres-b/bin",
		"pgbench/source-patch", "pgbench/source-patch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Run.SubjectDimension != benchmarkab.SubjectNativeToolchain || options.Run.BaselineNativeBindir != "/opt/postgres-a/bin" || options.Run.CandidateNativeBindir != "/opt/postgres-b/bin" {
		t.Fatalf("native toolchain options were not bound: %#v", options.Run)
	}
	if len(inputs) != 2 || inputs[0] != inputs[1] {
		t.Fatalf("unexpected native toolchain inputs: %#v", inputs)
	}
}

func TestParseBenchmarkHistoryCreateArgs(t *testing.T) {
	jsonOutput, historyID, inputs, err := parseBenchmarkHistoryCreateArgs([]string{
		"--json", "--history-id", "nightly-history", "series-a", "series-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !jsonOutput || historyID != "nightly-history" || len(inputs) != 2 || inputs[0] != "series-a" || inputs[1] != "series-b" {
		t.Fatalf("unexpected benchmark history options: json=%t id=%q inputs=%#v", jsonOutput, historyID, inputs)
	}
	for _, args := range [][]string{
		{"--history-id"},
		{"--history-id", "../escape"},
		{"--history-id", "one", "--history-id", "two"},
		{"--publish"},
	} {
		if _, _, _, err := parseBenchmarkHistoryCreateArgs(args); err == nil {
			t.Fatalf("unsafe or ambiguous history options were accepted: %#v", args)
		}
	}
}

func TestReleaseArchiveTopLevelDoesNotRequireWorkspace(t *testing.T) {
	outside := t.TempDir()
	source := filepath.Join(outside, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(outside, "pgworkbench-1.0.0-linux-amd64.tar.gz")
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDirectory) })
	err = run([]string{
		"release", "archive", "create",
		"--source", source,
		"--output", output,
		"--root-name", "pgworkbench-1.0.0-linux-amd64",
		"--epoch", "1700000000",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReleaseSBOMCreateAndVerifyCLI(t *testing.T) {
	root := t.TempDir()
	addCLISupplyChainFixture(t, root)
	output := filepath.Join(t.TempDir(), "pgworkbench.spdx.json")
	err := runRelease("", []string{
		"sbom", "create",
		"--root", root,
		"--output", output,
		"--name", "pgworkbench-1.0.0-linux-amd64",
		"--version", "1.0.0",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--epoch", "1700000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"spdxVersion": "SPDX-2.3"`) {
		t.Fatalf("unexpected SPDX document: %s", content)
	}
	if err := runRelease("", []string{"sbom", "verify", "--package-root", root, output}); err != nil {
		t.Fatal(err)
	}
	if err := runRelease("", []string{"sbom", "verify", output}); err == nil || !strings.Contains(err.Error(), "--package-root") {
		t.Fatalf("SBOM verify unexpectedly accepted an unbound document: %v", err)
	}
}

func TestReleaseManifestCreateAndVerifyCLI(t *testing.T) {
	root := testScenarioPack(t, ">=0.2.0")
	addCLISupplyChainFixture(t, root)
	releaseDir := t.TempDir()
	archiveName := "pgworkbench-1.0.0-linux-amd64.tar.gz"
	archivePath := filepath.Join(releaseDir, archiveName)
	rootName := strings.TrimSuffix(archiveName, ".tar.gz")
	sbomPath := filepath.Join(releaseDir, rootName+".spdx.json")
	if err := runRelease("", []string{
		"sbom", "create",
		"--root", root,
		"--output", sbomPath,
		"--name", rootName,
		"--version", "1.0.0",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--epoch", "1700000000",
	}); err != nil {
		t.Fatal(err)
	}
	if err := runRelease("", []string{
		"archive", "create",
		"--source", root,
		"--output", archivePath,
		"--root-name", rootName,
		"--epoch", "1700000000",
	}); err != nil {
		t.Fatal(err)
	}
	archiveContent, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archiveContent)
	checksum := hex.EncodeToString(sum[:]) + "  " + archiveName + "\n"
	if err := os.WriteFile(filepath.Join(releaseDir, "pgworkbench-1.0.0-SHA256SUMS.txt"), []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestName := "release.json"
	err = runRelease(root, []string{
		"manifest", "create",
		"--release-dir", releaseDir,
		"--version", "1.0.0",
		"--commit", "0123456789abcdef0123456789abcdef01234567",
		"--source-date-epoch", "1700000000",
		"--output", manifestName,
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(releaseDir, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"schema_version": "pgworkbench.release-manifest/v1"`, `"go_toolchain": "go`, `"source_date_epoch": 1700000000`, archiveName} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("release manifest missing %s: %s", expected, content)
		}
	}
	if err := runRelease(root, []string{"manifest", "verify", "--release-dir", releaseDir, "--manifest", manifestName}); err != nil {
		t.Fatal(err)
	}
}

func TestUtilityRunCLIForwardsRuntimeAndRunID(t *testing.T) {
	root := testUtilityCLIWorkspace(t)
	previousVersion, previousCommit := version, commit
	version = "0.2.0"
	commit = "0123456789abcdef0123456789abcdef01234567"
	t.Cleanup(func() {
		version, commit = previousVersion, previousCommit
	})
	t.Setenv("PGWORKBENCH_RUNTIME", "docker")
	t.Setenv("UTILITY_TEST_RUN_ID", "ambient-run")
	t.Setenv("ENV_FILE", ".env.example")
	t.Setenv("COMPOSE", "docker compose --ansi never")
	t.Setenv("PGWORKBENCH_NATIVE_BINDIR", "/opt/postgres/bin")
	t.Setenv("PG_INSTALL_DIR", "/opt/postgres")
	t.Setenv("POSTGRES_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_PORT", "59433")
	t.Setenv("POSTGRES_DB", "pg_experiment_workbench")
	t.Setenv("POSTGRES_USER", "postgres")
	t.Setenv("POSTGRES_PASSWORD", "test-password")
	t.Setenv("PROFILE_SIZE", "medium")
	t.Setenv("PROFILE_SECONDS", "45")
	t.Setenv("METRICS_INTERVAL", "2")
	t.Setenv("METRICS_DURATION", "10")
	t.Setenv("METRICS_SAMPLES", "3")
	t.Setenv("UTILITY_TEST_SNAPSHOT", "0")
	t.Setenv("PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST", "sha256:"+strings.Repeat("a", 64))
	t.Setenv("PGWORKBENCH_BENCHMARK_CAPSULE_ROOT", "/tmp/hostile-capsule")

	err := runUtility(root, speccatalog.New(root), []string{
		"run", "--runtime", "native", "--run-id", "cli-run", "smoke",
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, "seen.env"))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"PGWORKBENCH_RUNTIME=native\n",
		"UTILITY_TEST_RUN_ID=cli-run\n",
		"PGWORKBENCH_ENGINE_VERSION=0.2.0\n",
		"PGWORKBENCH_ENGINE_COMMIT=0123456789abcdef0123456789abcdef01234567\n",
		"PGWORKBENCH_BIN=" + executable + "\n",
		"ENV_FILE=.env.example\n",
		"COMPOSE=docker compose --ansi never\n",
		"PGWORKBENCH_NATIVE_BINDIR=/opt/postgres/bin\n",
		"PG_INSTALL_DIR=/opt/postgres\n",
		"POSTGRES_HOST=127.0.0.1\n",
		"POSTGRES_PORT=59433\n",
		"POSTGRES_DB=pg_experiment_workbench\n",
		"POSTGRES_USER=postgres\n",
		"POSTGRES_PASSWORD=test-password\n",
		"PROFILE_SIZE=medium\n",
		"PROFILE_SECONDS=45\n",
		"METRICS_INTERVAL=2\n",
		"METRICS_DURATION=10\n",
		"METRICS_SAMPLES=3\n",
		"UTILITY_TEST_SNAPSHOT=0\n",
		"PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST=\n",
		"PGWORKBENCH_BENCHMARK_CAPSULE_ROOT=\n",
	} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("utility CLI did not forward %q: %s", expected, content)
		}
	}
	generated, err := os.ReadFile(filepath.Join(root, ".tmp", "utility-tests", "cli-run.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "EXPERIMENT_RUN_ID='cli-run'") {
		t.Fatalf("generated experiment did not receive CLI run id: %s", generated)
	}
}

func TestUtilityRunCLIRejectsUnsafeOptionsBeforeGeneratingSpec(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--runtime", "remote", "smoke"},
		{"run", "--runtime", "native", "--run-id", "../escape", "smoke"},
	} {
		root := testUtilityCLIWorkspace(t)
		err := runUtility(root, speccatalog.New(root), args)
		if err == nil {
			t.Fatalf("unsafe utility CLI options were accepted: %#v", args)
		}
		if _, statErr := os.Lstat(filepath.Join(root, ".tmp")); !os.IsNotExist(statErr) {
			t.Fatalf("rejected utility CLI options created .tmp: %v", statErr)
		}
		if _, statErr := os.Lstat(filepath.Join(root, "seen.env")); !os.IsNotExist(statErr) {
			t.Fatalf("rejected utility CLI options invoked runner: %v", statErr)
		}
	}
}

func TestExperimentRunTimingOptions(t *testing.T) {
	options, inputs, err := parseExperimentRunArgs([]string{"--timeout", "45m", "--cleanup-grace", "20s", "smoke"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0] != "smoke" || options["timeout"] != "45m" || options["cleanup-grace"] != "20s" {
		t.Fatalf("unexpected parsed timing options: options=%#v inputs=%#v", options, inputs)
	}
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "--timeout", value: "0s"},
		{name: "--cleanup-grace", value: "-1s"},
		{name: "--timeout", value: "later"},
	} {
		if _, err := parsePositiveDurationFlag(test.name, test.value); err == nil {
			t.Fatalf("accepted invalid %s %q", test.name, test.value)
		}
	}
	if _, _, err := parseUtilityRunArgs([]string{"--timeout", "1m", "smoke"}); err == nil {
		t.Fatal("utility parser unexpectedly accepted experiment-only timeout flag")
	}
}

func testUtilityCLIWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"utility-tests/smoke.env": {
			content: "UTILITY_TEST_NAME=smoke\nUTILITY_TEST_WORKLOAD_SPEC=utility/smoke\n",
			mode:    0o644,
		},
		"workloads/utility/smoke.env": {
			content: "WORKLOAD_NAME=smoke\nWORKLOAD_KIND=shell\nWORKLOAD_CMD='echo smoke'\n",
			mode:    0o644,
		},
		"scripts/run_experiment.sh": {
			content: `#!/bin/sh
printf 'PGWORKBENCH_RUNTIME=%s\nUTILITY_TEST_RUN_ID=%s\nPGWORKBENCH_ENGINE_VERSION=%s\nPGWORKBENCH_ENGINE_COMMIT=%s\nPGWORKBENCH_BIN=%s\nENV_FILE=%s\nCOMPOSE=%s\nPGWORKBENCH_NATIVE_BINDIR=%s\nPG_INSTALL_DIR=%s\nPOSTGRES_HOST=%s\nPOSTGRES_PORT=%s\nPOSTGRES_DB=%s\nPOSTGRES_USER=%s\nPOSTGRES_PASSWORD=%s\nPROFILE_SIZE=%s\nPROFILE_SECONDS=%s\nMETRICS_INTERVAL=%s\nMETRICS_DURATION=%s\nMETRICS_SAMPLES=%s\nUTILITY_TEST_SNAPSHOT=%s\nPGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST=%s\nPGWORKBENCH_BENCHMARK_CAPSULE_ROOT=%s\n' "$PGWORKBENCH_RUNTIME" "$UTILITY_TEST_RUN_ID" "$PGWORKBENCH_ENGINE_VERSION" "$PGWORKBENCH_ENGINE_COMMIT" "$PGWORKBENCH_BIN" "$ENV_FILE" "$COMPOSE" "$PGWORKBENCH_NATIVE_BINDIR" "$PG_INSTALL_DIR" "$POSTGRES_HOST" "$POSTGRES_PORT" "$POSTGRES_DB" "$POSTGRES_USER" "$POSTGRES_PASSWORD" "$PROFILE_SIZE" "$PROFILE_SECONDS" "$METRICS_INTERVAL" "$METRICS_DURATION" "$METRICS_SAMPLES" "$UTILITY_TEST_SNAPSHOT" "$PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST" "$PGWORKBENCH_BENCHMARK_CAPSULE_ROOT" > seen.env
`,
			mode: 0o755,
		},
	}
	for name, file := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(file.content), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func testScenarioPack(t *testing.T, constraint string) string {
	t.Helper()
	root := t.TempDir()
	manifest := `{
  "schema_version": "pgworkbench.scenario-pack/v1",
  "id": "test-pack",
  "version": "1.0.0",
  "engine_constraint": "` + constraint + `",
  "assets": ["profiles"]
}`
	if err := os.WriteFile(filepath.Join(root, "pgworkbench-pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "profiles", "fixture.txt"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func addCLISupplyChainFixture(t *testing.T, root string) {
	t.Helper()
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, relative := range []string{
		"go.mod",
		"go.sum",
		"third_party/go-modules.json",
		"third_party/licenses/github.com/dlclark/regexp2/v1.11.0/ATTRIB",
		"third_party/licenses/github.com/dlclark/regexp2/v1.11.0/LICENSE",
		"third_party/licenses/github.com/santhosh-tekuri/jsonschema/v6/v6.0.2/LICENSE",
		"third_party/licenses/golang.org/x/text/v0.14.0/LICENSE",
		"third_party/licenses/golang.org/x/text/v0.14.0/PATENTS",
	} {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.OpenFile(filepath.Join(root, "pgworkbench"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
}
