package operationbench

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

var fixturePackDigest = "sha256:" + strings.Repeat("a", 64)

func TestResolveRuntimePortEnvironmentIsCanonicalDistinctAndComplete(t *testing.T) {
	values := map[string]string{
		"POSTGRES_PORT":                    "45433",
		"POSTGRES_REPLICA_PORT":            "45434",
		"POSTGRES_LOGICAL_SUBSCRIBER_PORT": "45435",
		"PGBOUNCER_PORT":                   "46432",
		"POSTGRES_UPGRADE_OLD_PORT":        "45436",
		"POSTGRES_UPGRADE_NEW_PORT":        "45437",
	}
	ports, err := resolveRuntimePorts(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	got := ports.environment()
	if len(got) != 6 {
		t.Fatalf("resolved ports = %#v, want all six assignments", got)
	}
	for _, assignment := range got {
		name, value, ok := strings.Cut(assignment, "=")
		if !ok || values[name] != value {
			t.Fatalf("unexpected resolved port assignment %q", assignment)
		}
	}

	for name, value := range map[string]string{
		"non-canonical": "045433",
		"below-range":   "1023",
		"above-range":   "65536",
		"hostile":       "55433; touch /tmp/hostile",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveRuntimePorts(func(key string) string {
				if key == "POSTGRES_PORT" {
					return value
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), "POSTGRES_PORT") {
				t.Fatalf("invalid port %q error = %v", value, err)
			}
		})
	}
	_, err = resolveRuntimePorts(func(key string) string {
		if key == "POSTGRES_REPLICA_PORT" {
			return "55433"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("duplicate runtime ports error = %v", err)
	}
}

func TestRunRejectsInvalidRuntimePortBeforeReservingEvidence(t *testing.T) {
	root := t.TempDir()
	writeFixtureInputs(t, root, 2)
	runID := "invalid-runtime-port"
	_, err := Run(root, "bulk/indexed", Options{
		Runtime: "docker",
		RunID:   runID,
		Getenv: func(key string) string {
			if key == "POSTGRES_LOGICAL_SUBSCRIBER_PORT" {
				return "not-a-port"
			}
			return ""
		},
	})
	if err == nil || !strings.Contains(err.Error(), "POSTGRES_LOGICAL_SUBSCRIBER_PORT") {
		t.Fatalf("Run() error = %v, want strict runtime port rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "runs", "operation-benchmarks", runID)); !os.IsNotExist(statErr) {
		t.Fatalf("invalid runtime port reserved operation evidence: %v", statErr)
	}
}

func TestOperationSeriesRuntimePortVersionMatrix(t *testing.T) {
	ports := RuntimePorts{Postgres: 45433, Replica: 45434, LogicalSubscriber: 45435, PgBouncer: 46432, UpgradeOld: 45436, UpgradeNew: 45437}
	digest := digestRuntimePorts(ports)
	for _, test := range []struct {
		name     string
		series   Series
		manifest map[string]string
		valid    bool
	}{
		{name: "legacy-v1", series: Series{SchemaVersion: SeriesSchemaVersion}, manifest: map[string]string{"schema_version": runstate.ManifestSchemaVersion}, valid: true},
		{name: "current-v2", series: Series{SchemaVersion: SeriesSchemaVersionV2, RuntimePorts: &ports, RuntimePortsDigest: digest, RuntimePortsPresent: true, RuntimePortsDigestPresent: true}, manifest: map[string]string{"schema_version": runstate.ManifestSchemaVersionV2, "runtime_ports_digest": digest}, valid: true},
		{name: "v1-series-with-v2-manifest", series: Series{SchemaVersion: SeriesSchemaVersion}, manifest: map[string]string{"schema_version": runstate.ManifestSchemaVersionV2, "runtime_ports_digest": digest}},
		{name: "v2-series-with-v1-manifest", series: Series{SchemaVersion: SeriesSchemaVersionV2, RuntimePorts: &ports, RuntimePortsDigest: digest, RuntimePortsPresent: true, RuntimePortsDigestPresent: true}, manifest: map[string]string{"schema_version": runstate.ManifestSchemaVersion}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := VerifyResult{Issues: []string{}}
			checkLinkedRuntimePortBinding(&result, test.series, test.manifest, 1)
			if got := len(result.Issues) == 0; got != test.valid {
				t.Fatalf("runtime-port version pairing valid=%v, want %v: %v", got, test.valid, result.Issues)
			}
		})
	}

	for _, content := range []string{
		`{"schema_version":"pgworkbench.operation-benchmark-series/v1","runtime_ports":null}`,
		`{"schema_version":"pgworkbench.operation-benchmark-series/v1","runtime_ports_digest":""}`,
	} {
		var series Series
		if err := decodeStrictBytes([]byte(content), &series); err != nil {
			if strings.Contains(content, `"runtime_ports":null`) && strings.Contains(err.Error(), "null is not allowed") {
				continue
			}
			t.Fatal(err)
		}
		result := VerifyResult{Issues: []string{}}
		checkRuntimePorts(&result, series)
		if len(result.Issues) == 0 {
			t.Fatalf("legacy operation series accepted a present v2-only field: %s", content)
		}
	}
	portsJSON, err := json.Marshal(ports)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{
		`{"schema_version":"pgworkbench.operation-benchmark-series/v2"}`,
		`{"schema_version":"pgworkbench.operation-benchmark-series/v2","runtime_ports":null,"runtime_ports_digest":"` + digest + `"}`,
		`{"schema_version":"pgworkbench.operation-benchmark-series/v2","runtime_ports":` + string(portsJSON) + `}`,
		`{"schema_version":"pgworkbench.operation-benchmark-series/v2","runtime_ports_digest":"` + digest + `"}`,
	} {
		var series Series
		if err := decodeStrictBytes([]byte(content), &series); err != nil {
			if strings.Contains(content, `"runtime_ports":null`) && strings.Contains(err.Error(), "null is not allowed") {
				continue
			}
			t.Fatal(err)
		}
		result := VerifyResult{Issues: []string{}}
		checkRuntimePorts(&result, series)
		if len(result.Issues) == 0 {
			t.Fatalf("operation series v2 accepted a missing/null runtime port binding: %s", content)
		}
	}
}

func TestOperationSchemasTrackTopLevelGoContracts(t *testing.T) {
	tests := []struct {
		file     string
		value    any
		version  string
		versions []string
		artifact string
	}{
		{file: "operation-benchmark-spec.schema.json", value: Spec{}, version: SpecSchemaVersion},
		{file: "operation-result.schema.json", value: OperationResult{}, version: ResultSchemaVersion, artifact: ResultArtifactType},
		{file: "operation-benchmark-series.schema.json", value: Series{}, versions: []string{SeriesSchemaVersion, SeriesSchemaVersionV2}, artifact: SeriesArtifactType},
		{file: "operation-benchmark-bundle-inventory.schema.json", value: BundleInventory{}, version: BundleSchemaVersion, artifact: BundleArtifactType},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", "schemas", test.file))
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(content, &document); err != nil {
				t.Fatal(err)
			}
			if document["$schema"] != "https://json-schema.org/draft/2020-12/schema" || document["additionalProperties"] != false {
				t.Fatal("schema must use draft 2020-12 and close root properties")
			}
			properties := document["properties"].(map[string]any)
			versionProperty := properties["schema_version"].(map[string]any)
			if len(test.versions) > 0 {
				got, ok := versionProperty["enum"].([]any)
				if !ok || len(got) != len(test.versions) {
					t.Fatal("schema versions drift")
				}
				for index, want := range test.versions {
					if got[index] != want {
						t.Fatal("schema versions drift")
					}
				}
			} else if versionProperty["const"] != test.version {
				t.Fatal("schema version drift")
			}
			if test.artifact != "" && properties["artifact_type"].(map[string]any)["const"] != test.artifact {
				t.Fatal("artifact type drift")
			}
			typeOf := reflect.TypeOf(test.value)
			for index := 0; index < typeOf.NumField(); index++ {
				name := strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
				if name == "" || name == "-" {
					continue
				}
				if _, ok := properties[name]; !ok {
					t.Fatalf("Go JSON field %q is missing from schema", name)
				}
			}
		})
	}
}

func TestCatalogAndOperationResultAreStrict(t *testing.T) {
	root := t.TempDir()
	writeFixtureInputs(t, root, 2)
	spec, err := NewCatalog(root).Load("bulk/indexed")
	if err != nil {
		t.Fatal(err)
	}
	if spec.ID != "bulk/indexed" || spec.DecisionEligible || spec.Classification != Classification || !evidence.IsDigest(spec.Digest) {
		t.Fatalf("unexpected spec: %#v", spec)
	}
	path := filepath.Join(root, "benchmarks", "operations", "bulk", "indexed.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = []byte(strings.Replace(string(content), `"trials": 2`, `"trials": 2, "trials": 3`, 1))
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCatalog(root).Load("bulk/indexed"); err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
		t.Fatalf("duplicate JSON key was accepted: %v", err)
	}
}

func TestOperationSeriesJSONRejectsSchemaInvalidNulls(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "root", content: `null`, want: "$: null is not allowed"},
		{name: "reasons", content: `{"reasons":null}`, want: "$.reasons: null is not allowed"},
		{name: "nested operation result", content: `{"trials":[{"operation_result":null}]}`, want: "$.trials[0].operation_result: null is not allowed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var series Series
			err := decodeStrictBytes([]byte(test.content), &series)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decodeStrictBytes() error = %v, want substring %q", err, test.want)
			}
		})
	}

	var nullableStats Series
	if err := decodeStrictBytes([]byte(`{"stats":{"cv_pct":null,"robust_cv_pct":null}}`), &nullableStats); err != nil {
		t.Fatalf("schema-authorized nullable statistics rejected: %v", err)
	}
}

func TestBuiltinOperationInputClosuresAreCompleteAndStable(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	specs, err := NewCatalog(root).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) < 3 {
		t.Fatalf("expected at least three built-in operation packs, got %d", len(specs))
	}
	for _, spec := range specs {
		t.Run(spec.ID, func(t *testing.T) {
			inputs, err := collectInputClosure(root, spec)
			if err != nil {
				t.Fatal(err)
			}
			if len(inputs) < 6 {
				t.Fatalf("input closure is unexpectedly small: %#v", inputs)
			}
			plan, err := experimentplan.Build(speccatalog.New(root), spec.ExperimentSpec)
			if err != nil {
				t.Fatal(err)
			}
			paths := map[string]bool{}
			var totalBytes int64
			for _, input := range inputs {
				if operationInputPathForbidden(input.Path) {
					t.Fatalf("runtime/private path entered operation input closure: %s", input.Path)
				}
				paths[input.Path] = true
				totalBytes += input.Size
			}
			if len(inputs) > maxOperationInputFiles || totalBytes > maxOperationInputBytes {
				t.Fatalf("input closure exceeded budget: files=%d bytes=%d", len(inputs), totalBytes)
			}
			for _, want := range []string{
				spec.Path,
				filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(spec.ExperimentSpec)+".env")),
				filepath.ToSlash(filepath.Join("topologies", filepath.FromSlash(plan.Fields["topology"])+".env")),
				".env.example", "configs/default/postgresql.conf", "scripts/run_experiment.sh", "scripts/run_workload.sh",
			} {
				if !paths[want] {
					t.Fatalf("input closure is missing %s", want)
				}
			}
			digest, err := inputClosureDigest(inputs)
			if err != nil || !evidence.IsDigest(digest) {
				t.Fatalf("invalid closure digest %q: %v", digest, err)
			}
			if err := verifyLiveInputs(root, inputs); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOperationInputClosureDoesNotCaptureRuntimeTreesOrPrivateEnv(t *testing.T) {
	root := t.TempDir()
	writeFixtureInputs(t, root, 2)
	writeTestFile(t, filepath.Join(root, ".env"), "POSTGRES_PASSWORD=must-not-enter-a-bundle\n")
	writeTestFile(t, filepath.Join(root, "scripts", "run_experiment.sh"), strings.Join([]string{
		`run_dir="$REPO_DIR/runs/$RUN_ID"`,
		`cache="$REPO_DIR/.tmp/go-cache"`,
		`binary="$REPO_DIR/generated/bin/pgworkbench"`,
		`log="$REPO_DIR/logs/workloads/$RUN_ID.log"`,
		`spec_root="$REPO_DIR/experiments"`,
	}, "\n")+"\n")
	for _, relative := range []string{
		"runs/previous/result.json",
		".tmp/go-cache/private.bin",
		"generated/bin/pgworkbench",
		"logs/workloads/private.log",
		"experiments/unrelated.env",
	} {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(relative)), "sentinel\n")
	}
	spec, err := NewCatalog(root).Load("bulk/indexed")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := collectInputClosure(root, spec)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, input := range inputs {
		paths[input.Path] = true
		if operationInputPathForbidden(input.Path) {
			t.Fatalf("forbidden path entered closure: %s", input.Path)
		}
	}
	if paths[".env"] || paths["experiments/unrelated.env"] {
		t.Fatalf("private or unrelated input entered closure: %#v", paths)
	}
	if !paths[".env.example"] || !paths["scripts/run_experiment.sh"] {
		t.Fatalf("declared deterministic inputs are missing: %#v", paths)
	}
}

func TestOperationInputClosureRejectsProfileTreeOverBudget(t *testing.T) {
	root := t.TempDir()
	writeFixtureInputs(t, root, 2)
	writeTestFile(t, filepath.Join(root, "experiments", "bulk.env"), "EXPERIMENT_NAME=bulk\nEXPERIMENT_TOPOLOGY=single\nEXPERIMENT_PROFILE=oversized\n")
	path := filepath.Join(root, "profiles", "oversized", "payload.bin")
	writeTestFile(t, path, "")
	if err := os.Truncate(path, maxOperationInputBytes+1); err != nil {
		t.Fatal(err)
	}
	spec, err := NewCatalog(root).Load("bulk/indexed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collectInputClosure(root, spec); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized profile closure was accepted: %v", err)
	}
}

func TestFinishSeriesRejectsCrossTrialExperimentIdentityDrift(t *testing.T) {
	value1, value2 := 100.0, 101.0
	executionDigest := "sha256:" + strings.Repeat("a", 64)
	series := Series{
		Status:                    "passed",
		TrialsPlanned:             2,
		TrialsValid:               2,
		MaxCVPct:                  100,
		ExecutionParametersDigest: executionDigest,
		Reasons:                   []string{},
		Trials: []Trial{
			{Status: "passed", ExecutionParametersDigest: executionDigest, ExperimentIdentityDigest: "sha256:" + strings.Repeat("b", 64), PrimaryValue: &value1},
			{Status: "passed", ExecutionParametersDigest: executionDigest, ExperimentIdentityDigest: "sha256:" + strings.Repeat("c", 64), PrimaryValue: &value2},
		},
	}
	finishSeries(&series, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC))
	if series.Status != "failed" || !strings.Contains(strings.Join(series.Reasons, "; "), "experiment identity changed") {
		t.Fatalf("cross-trial experiment identity drift was accepted: %#v", series)
	}
}

func TestFinishSeriesExpandsEnvelopeToLinkedTrialPrecision(t *testing.T) {
	series := Series{
		Status:    "failed",
		Reasons:   []string{"fixture"},
		StartedAt: "2026-08-13T00:00:00.5Z",
		Trials: []Trial{{
			StartedAt:  "2026-08-13T00:00:00Z",
			FinishedAt: "2026-08-13T00:00:02Z",
		}},
	}
	finishSeries(&series, time.Date(2026, 8, 13, 0, 0, 1, 500_000_000, time.UTC))
	if series.StartedAt != "2026-08-13T00:00:00Z" || series.FinishedAt != "2026-08-13T00:00:02Z" {
		t.Fatalf("series does not contain its linked trial: %s - %s", series.StartedAt, series.FinishedAt)
	}
}

func TestRunVerifyBundleRelocateAndDetectTampering(t *testing.T) {
	root := t.TempDir()
	writeFixtureInputs(t, root, 2)
	runtimePorts := map[string]string{
		"POSTGRES_PORT":                    "45433",
		"POSTGRES_REPLICA_PORT":            "45434",
		"POSTGRES_LOGICAL_SUBSCRIBER_PORT": "45435",
		"PGBOUNCER_PORT":                   "46432",
		"POSTGRES_UPGRADE_OLD_PORT":        "45436",
		"POSTGRES_UPGRADE_NEW_PORT":        "45437",
	}
	experimentDigest, err := evidence.DigestFile(filepath.Join(root, "experiments", "bulk.env"))
	if err != nil {
		t.Fatal(err)
	}
	trial := 0
	runner := func(root string, _ speccatalog.Catalog, input string, options experimentrun.Options) (experimentrun.Result, error) {
		if !options.ExactEnvironment || !contains(options.Env, "ENV_FILE=.env.example") || !containsPrefix(options.Env, "PGWORKBENCH_NATIVE_BINDIR=") || !containsPrefix(options.Env, "PGWORKBENCH_NATIVE_TOOLCHAIN_DIGEST=sha256:") {
			t.Fatalf("operation runner did not receive an exact native environment: %#v", options)
		}
		for key, want := range runtimePorts {
			if !contains(options.Env, key+"="+want) {
				t.Fatalf("operation exact environment omitted %s=%s: %#v", key, want, options.Env)
			}
		}
		runtimePortsDigest := ""
		for _, assignment := range options.Env {
			if key, value, ok := strings.Cut(assignment, "="); ok && key == "PGWORKBENCH_RUNTIME_PORTS_DIGEST" {
				runtimePortsDigest = value
			}
		}
		if !evidence.IsDigest(runtimePortsDigest) {
			t.Fatalf("operation exact environment omitted canonical runtime ports digest: %#v", options.Env)
		}
		trial++
		started := time.Date(2026, 8, 13, 0, 0, trial*2-1, 0, time.UTC)
		finished := started.Add(time.Second)
		runDir := filepath.Join(root, "runs", options.RunID)
		writeVerifiedRun(t, runDir, options.RunID, input, experimentDigest, runtimePortsDigest, started, finished, float64(100+trial))
		return experimentrun.Result{SchemaVersion: experimentrun.SchemaVersion, ExperimentSpec: input, SpecSHA256: strings.TrimPrefix(experimentDigest, "sha256:"), Runtime: options.Runtime, Topology: "single", RunID: options.RunID, RunDir: runDir, StartedAt: started.Format(time.RFC3339), FinishedAt: finished.Format(time.RFC3339), DurationMS: 1000, ExitCode: 0, Status: "passed"}, nil
	}
	times := []time.Time{
		time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 13, 0, 0, 5, 0, time.UTC),
	}
	series, err := Run(root, "bulk/indexed", Options{Runtime: "native", RunID: "operation-a", PackID: "fixture", PackVersion: "0.2.0", PackDigest: fixturePackDigest, EngineVersion: "0.2.0", EngineCommit: strings.Repeat("a", 40), BinaryPath: fakeOperationEngine(t), NativeBindir: fakeOperationToolchain(t), Getenv: func(key string) string { return runtimePorts[key] }, RunExperiment: runner, Now: func() time.Time { value := times[0]; times = times[1:]; return value }})
	if err != nil {
		t.Fatal(err)
	}
	if !series.Passed() || series.TrialsValid != 2 || series.Stats == nil || series.DecisionEligible {
		t.Fatalf("unexpected series: %#v", series)
	}
	if series.RuntimePorts == nil || series.RuntimePorts.Postgres != 45433 || series.RuntimePorts.PgBouncer != 46432 || series.RuntimePortsDigest != digestRuntimePorts(*series.RuntimePorts) {
		t.Fatalf("operation series omitted bound runtime port snapshot: %#v", series)
	}
	verified, err := Verify(root, series.ArtifactDir)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Valid {
		t.Fatalf("series failed independent verification: %v", verified.Issues)
	}
	verifyLegacyOperationSeriesV1Compatibility(t, root, series)

	archive := filepath.Join(root, "generated", "operation.tar.gz")
	bundle, err := CreateBundle(root, series.ArtifactDir, archive, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.LinkedRuns != 2 || !evidence.IsDigest(bundle.Digest) {
		t.Fatalf("unexpected bundle: %#v", bundle)
	}
	extracted := t.TempDir()
	rootName := extractArchive(t, archive, extracted)
	bundleRoot := filepath.Join(extracted, rootName)
	relocatedSeries := filepath.Join(bundleRoot, "runs", "operation-benchmarks", series.RunID)
	// Bundle mode must derive its artifact root from the relocated series, not
	// prefer the caller's live repository where the original trial IDs exist.
	relocated, err := VerifyBundle(root, relocatedSeries)
	if err != nil {
		t.Fatal(err)
	}
	if !relocated.Valid {
		t.Fatalf("relocated bundle failed: %v", relocated.Issues)
	}
	outsideSeries := filepath.Join(t.TempDir(), "series-copy")
	if err := copyTree(relocatedSeries, outsideSeries); err != nil {
		t.Fatal(err)
	}
	outside, err := VerifyBundle(bundleRoot, outsideSeries)
	if err != nil {
		t.Fatal(err)
	}
	if outside.Valid || !issuesContain(outside.Issues, "canonical ref") {
		t.Fatalf("series outside its inventory root was accepted: %#v", outside)
	}

	tamperCases := []struct {
		name      string
		mutate    func(t *testing.T, bundleRoot, seriesDir string)
		wantIssue string
	}{
		{name: "series root null", wantIssue: "result.json parse failed", mutate: func(t *testing.T, _ string, seriesDir string) {
			writeTestFile(t, filepath.Join(seriesDir, "result.json"), "null\n")
		}},
		{name: "series reasons null", wantIssue: "result.json parse failed", mutate: func(t *testing.T, _ string, seriesDir string) {
			path := filepath.Join(seriesDir, "result.json")
			var document map[string]any
			if err := json.Unmarshal(readTestFile(t, path), &document); err != nil {
				t.Fatal(err)
			}
			document["reasons"] = nil
			writeTestJSON(t, path, document)
		}},
		{name: "trial operation result null", wantIssue: "result.json parse failed", mutate: func(t *testing.T, _ string, seriesDir string) {
			path := filepath.Join(seriesDir, "result.json")
			var document map[string]any
			if err := json.Unmarshal(readTestFile(t, path), &document); err != nil {
				t.Fatal(err)
			}
			trials := document["trials"].([]any)
			trials[0].(map[string]any)["operation_result"] = nil
			writeTestJSON(t, path, document)
		}},
		{name: "runtime port snapshot and digest", wantIssue: "linked v2 runtime ports digest does not match series", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.RuntimePorts.PgBouncer = 46442
			value.RuntimePortsDigest = digestRuntimePorts(*value.RuntimePorts)
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
		}},
		{name: "source result bytes", mutate: func(t *testing.T, bundleRoot, _ string) {
			path := linkedResultPath(bundleRoot, series.Trials[0].RunID)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(content, ' '), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "result measurement scope", mutate: func(t *testing.T, bundleRoot, _ string) {
			path := linkedResultPath(bundleRoot, series.Trials[0].RunID)
			var operation OperationResult
			if err := decodeStrictFile(path, &operation); err != nil {
				t.Fatal(err)
			}
			operation.Measurement.Scope = "forged scope"
			writeTestJSON(t, path, operation)
		}},
		{name: "series primary value", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			*value.Trials[0].PrimaryValue += 1000
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
		}},
		{name: "series statistics", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.Stats.Mean += 1000
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
		}},
		{name: "series status", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.Status = "failed"
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
		}},
		{name: "series execution identity", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.ExecutionParametersDigest = "sha256:" + strings.Repeat("0", 64)
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
		}},
		{name: "trial experiment identity", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.Trials[0].ExperimentIdentityDigest = "sha256:" + strings.Repeat("0", 64)
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
			writeTestJSON(t, filepath.Join(seriesDir, "trials", "001.json"), value.Trials[0])
		}},
		{name: "cross-trial experiment identity drift", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.Trials[1].ExperimentIdentityDigest = "sha256:" + strings.Repeat("0", 64)
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
			writeTestJSON(t, filepath.Join(seriesDir, "trials", "002.json"), value.Trials[1])
		}},
		{name: "series pack digest", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.PackDigest = "sha256:" + strings.Repeat("0", 64)
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
		}},
		{name: "input capsule bytes", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			appendTestFile(t, filepath.Join(seriesDir, "inputs", filepath.FromSlash(value.Inputs[0].Path)), " ")
		}},
		{name: "input capsule extra", mutate: func(t *testing.T, _ string, seriesDir string) {
			writeTestFile(t, filepath.Join(seriesDir, "inputs", "unexpected.txt"), "unexpected\n")
		}},
		{name: "extra trial file", mutate: func(t *testing.T, _ string, seriesDir string) {
			writeTestFile(t, filepath.Join(seriesDir, "trials", "999.json"), "{}\n")
		}},
		{name: "duplicate linked run", mutate: func(t *testing.T, _ string, seriesDir string) {
			value := loadTestSeries(t, seriesDir)
			value.Trials[1].RunID = value.Trials[0].RunID
			value.Trials[1].RunRef = value.Trials[0].RunRef
			writeTestJSON(t, filepath.Join(seriesDir, "result.json"), value)
		}},
		{name: "operation spec snapshot", mutate: func(t *testing.T, _ string, seriesDir string) {
			appendTestFile(t, filepath.Join(seriesDir, "operation-spec.json"), " ")
		}},
		{name: "experiment spec snapshot", mutate: func(t *testing.T, _ string, seriesDir string) {
			appendTestFile(t, filepath.Join(seriesDir, "experiment-spec.env"), "# forged\n")
		}},
		{name: "symlinked result", mutate: func(t *testing.T, bundleRoot, seriesDir string) {
			path := linkedResultPath(bundleRoot, series.Trials[0].RunID)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(seriesDir, "operation-spec.json"), path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "inventory missing", mutate: func(t *testing.T, bundleRoot, _ string) {
			inventory := loadTestInventory(t, bundleRoot)
			inventory.Files = inventory.Files[:len(inventory.Files)-1]
			writeTestJSON(t, filepath.Join(bundleRoot, BundleInventoryName), inventory)
		}},
		{name: "inventory extra", mutate: func(t *testing.T, bundleRoot, _ string) {
			inventory := loadTestInventory(t, bundleRoot)
			inventory.Files = append(inventory.Files, BundleFile{Path: "zz-extra", Size: 1, Digest: "sha256:" + strings.Repeat("0", 64)})
			writeTestJSON(t, filepath.Join(bundleRoot, BundleInventoryName), inventory)
		}},
		{name: "inventory order", mutate: func(t *testing.T, bundleRoot, _ string) {
			inventory := loadTestInventory(t, bundleRoot)
			inventory.Files[0], inventory.Files[1] = inventory.Files[1], inventory.Files[0]
			writeTestJSON(t, filepath.Join(bundleRoot, BundleInventoryName), inventory)
		}},
		{name: "inventory duplicate", mutate: func(t *testing.T, bundleRoot, _ string) {
			inventory := loadTestInventory(t, bundleRoot)
			inventory.Files = append(inventory.Files, inventory.Files[len(inventory.Files)-1])
			writeTestJSON(t, filepath.Join(bundleRoot, BundleInventoryName), inventory)
		}},
	}
	for _, test := range tamperCases {
		t.Run(test.name, func(t *testing.T) {
			extracted := t.TempDir()
			rootName := extractArchive(t, archive, extracted)
			bundleRoot := filepath.Join(extracted, rootName)
			seriesDir := filepath.Join(bundleRoot, "runs", "operation-benchmarks", series.RunID)
			test.mutate(t, bundleRoot, seriesDir)
			tampered, err := VerifyBundle(bundleRoot, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if tampered.Valid || len(tampered.Issues) == 0 {
				t.Fatalf("tampering was not detected")
			}
			if test.wantIssue != "" && !issuesContain(tampered.Issues, test.wantIssue) {
				t.Fatalf("tampering omitted %q: %v", test.wantIssue, tampered.Issues)
			}
		})
	}
}

func verifyLegacyOperationSeriesV1Compatibility(t *testing.T, sourceRoot string, current Series) {
	t.Helper()
	legacyRoot := filepath.Join(t.TempDir(), "legacy-root")
	if err := copyTree(sourceRoot, legacyRoot); err != nil {
		t.Fatal(err)
	}
	seriesDir := filepath.Join(legacyRoot, "runs", "operation-benchmarks", current.RunID)
	series := loadTestSeries(t, seriesDir)
	series.SchemaVersion = SeriesSchemaVersion
	series.RuntimePorts = nil
	series.RuntimePortsDigest = ""
	series.RuntimePortsPresent = false
	series.RuntimePortsDigestPresent = false
	for index := range series.Trials {
		trial := &series.Trials[index]
		started, err := time.Parse(time.RFC3339Nano, trial.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		finished, err := time.Parse(time.RFC3339Nano, trial.FinishedAt)
		if err != nil {
			t.Fatal(err)
		}
		if trial.PrimaryValue == nil {
			t.Fatal("legacy compatibility fixture trial has no primary value")
		}
		runDir := filepath.Join(legacyRoot, filepath.FromSlash(trial.RunRef))
		writeVerifiedRun(t, runDir, trial.RunID, series.ExperimentSpec, series.ExperimentDigest, "", started, finished, *trial.PrimaryValue)
		manifest, err := envfile.Parse(filepath.Join(runDir, "manifest.env"))
		if err != nil {
			t.Fatal(err)
		}
		if manifest["schema_version"] != runstate.ManifestSchemaVersion {
			t.Fatalf("legacy compatibility fixture manifest schema = %q", manifest["schema_version"])
		}
		trial.ExecutionParametersDigest = manifest["execution_parameters_digest"]
		trial.ExperimentIdentityDigest = manifest["experiment_identity_digest"]
		trial.RunDigest, err = digestTree(runDir)
		if err != nil {
			t.Fatal(err)
		}
		writeTestJSON(t, filepath.Join(seriesDir, "trials", fmt.Sprintf("%03d.json", index+1)), *trial)
	}
	series.ExecutionParametersDigest = series.Trials[0].ExecutionParametersDigest
	writeTestJSON(t, filepath.Join(seriesDir, "result.json"), series)
	writeTestFile(t, filepath.Join(seriesDir, "summary.md"), string(SummaryBytes(series)))
	verified, err := Verify(legacyRoot, seriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Valid {
		t.Fatalf("legacy operation-series-v1/run-manifest-v1 pair was rejected: %v", verified.Issues)
	}
}

func writeFixtureInputs(t *testing.T, root string, trials int) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, ".env.example"), "POSTGRES_HOST=127.0.0.1\n")
	writeTestFile(t, filepath.Join(root, "scripts", "run_experiment.sh"), "#!/usr/bin/env bash\n")
	writeTestFile(t, filepath.Join(root, "scripts", "run_workload.sh"), "#!/usr/bin/env bash\n")
	writeTestFile(t, filepath.Join(root, "experiments", "bulk.env"), "EXPERIMENT_NAME=bulk\nEXPERIMENT_TOPOLOGY=single\n")
	writeTestFile(t, filepath.Join(root, "topologies", "single.env"), "TOPOLOGY_NAME=single\n")
	writeTestFile(t, filepath.Join(root, "configs", "default", "postgresql.conf"), "# default\n")
	scope := "PostgreSQL server clock around the tested operation."
	content := fmt.Sprintf(`{
  "schema_version": %q,
  "id": "bulk/indexed",
  "name": "Bulk indexed",
  "description": "Synthetic strict operation fixture.",
  "classification": %q,
  "decision_eligible": false,
  "experiment_spec": "bulk",
  "trials": %d,
  "max_cv_pct": 100,
  "supported_runtimes": ["native", "docker"],
  "measurement": {"basis":"operation-result","result_path":"artifacts/operation-result.json","metric":"total_ms","unit":"milliseconds","direction":"lower-is-better","scope":%q},
  "assurance": "Fixture assurance remains descriptive."
}
`, SpecSchemaVersion, Classification, trials, scope)
	writeTestFile(t, filepath.Join(root, "benchmarks", "operations", "bulk", "indexed.json"), content)
}

func fakeOperationEngine(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pgworkbench")
	writeTestFile(t, path, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeOperationToolchain(t *testing.T) string {
	t.Helper()
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"createdb", "initdb", "pg_ctl", "pg_isready", "pgbench", "postgres", "psql"} {
		path := filepath.Join(bindir, name)
		writeTestFile(t, path, "#!/bin/sh\necho '"+name+" (PostgreSQL) 19.0'\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bindir
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeVerifiedRun(t *testing.T, runDir, runID, experiment string, experimentDigest string, runtimePortsDigest string, started, finished time.Time, value float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := runstate.Manifest{
		RunID: runID, StartedAt: started.Format(time.RFC3339), ExperimentSpec: "experiments/" + experiment + ".env", ExperimentSpecID: experiment,
		ExperimentSpecRef: "experiments/" + experiment + ".env", ExperimentSpecDigest: experimentDigest, Runtime: "native",
		EngineVersion: "0.2.0", EngineCommit: strings.Repeat("a", 40), RuntimeFingerprintStatus: runstate.RuntimeFingerprintObserved,
		RuntimePortsDigest:       runtimePortsDigest,
		RuntimeFingerprintTarget: "primary", RuntimeOS: "linux", RuntimeArch: "amd64", PostgresServerVersionNum: "190000", PostgresServerMajor: "19",
		RuntimeFingerprintAt: started.Format(time.RFC3339), ExperimentName: "bulk", ExperimentTopology: "single", ExperimentPGConfig: "default",
		ProfileSize: "small", MetricsEnabled: "0", PackID: "fixture", PackVersion: "0.2.0", PackDigest: fixturePackDigest, RunDir: runDir,
	}
	if err := runstate.WriteManifest(runDir, manifest); err != nil {
		t.Fatal(err)
	}
	if err := runstate.WriteVerdict(runDir, runstate.Verdict{RunID: runID, Status: runstate.VerdictStatusPassed, Message: "passed", StartedAt: started.Format(time.RFC3339), FinishedAt: finished.Format(time.RFC3339), ExperimentSpecID: experiment, RunDir: runDir}); err != nil {
		t.Fatal(err)
	}
	operation := OperationResult{SchemaVersion: ResultSchemaVersion, ArtifactType: ResultArtifactType, OperationID: "bulk/indexed", Variant: "indexed", PrimaryMetric: ResultMetric{Name: "total_ms", Unit: "milliseconds", Direction: "lower-is-better", Value: value}, Measurement: ResultMeasure{Basis: "postgres-server-clock", Scope: "PostgreSQL server clock around the tested operation."}}
	if err := writeJSON(filepath.Join(runDir, "artifacts", "operation-result.json"), operation); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func extractArchive(t *testing.T, archivePath, destination string) string {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	rootName := ""
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" {
			continue
		}
		if rootName == "" {
			rootName = strings.Split(name, "/")[0]
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(output, reader); err != nil {
				_ = output.Close()
				t.Fatal(err)
			}
			if err := output.Close(); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unsupported archive entry type %d", header.Typeflag)
		}
	}
	return rootName
}

func issuesContain(issues []string, fragment string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, fragment) {
			return true
		}
	}
	return false
}

func linkedResultPath(bundleRoot, runID string) string {
	return filepath.Join(bundleRoot, "runs", runID, "artifacts", "operation-result.json")
}

func loadTestSeries(t *testing.T, seriesDir string) Series {
	t.Helper()
	var series Series
	if err := decodeStrictFile(filepath.Join(seriesDir, "result.json"), &series); err != nil {
		t.Fatal(err)
	}
	return series
}

func loadTestInventory(t *testing.T, bundleRoot string) BundleInventory {
	t.Helper()
	var inventory BundleInventory
	if err := decodeStrictFile(filepath.Join(bundleRoot, BundleInventoryName), &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := writeJSON(path, value); err != nil {
		t.Fatal(err)
	}
}

func appendTestFile(t *testing.T, path, suffix string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, suffix...), 0o644); err != nil {
		t.Fatal(err)
	}
}
