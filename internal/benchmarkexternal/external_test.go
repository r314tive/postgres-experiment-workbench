package benchmarkexternal

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
)

const fakeSysbenchResult = `sysbench 1.0.20 (using system LuaJIT 2.1.0-beta3)

SQL statistics:
    transactions:                        1000   (100.00 per sec.)
    ignored errors:                      0      (0.00 per sec.)

General statistics:
    total time:                          10.0000s
    total number of events:              1000

Latency (ms):
         avg:                                    1.00
         95th percentile:                        1.10
`

const fakeBenchBaseResult = `{
  "Start timestamp (milliseconds)": 1700000000000,
  "Current Timestamp (milliseconds)": 1700000010000,
  "Elapsed Time (nanoseconds)": 10000000000,
  "DBMS Type": "POSTGRES",
  "DBMS Version": "16.4",
  "Benchmark Type": "tpcc",
  "Final State": "DONE",
  "Measured Requests": 1000,
  "isolation": "TRANSACTION_READ_COMMITTED",
  "scalefactor": "1.0",
  "terminals": "2",
  "Latency Distribution": {
    "Minimum Latency (microseconds)": 100,
    "25th Percentile Latency (microseconds)": 200,
    "Median Latency (microseconds)": 300,
    "Average Latency (microseconds)": 350,
    "75th Percentile Latency (microseconds)": 400,
    "90th Percentile Latency (microseconds)": 500,
    "95th Percentile Latency (microseconds)": 600,
    "99th Percentile Latency (microseconds)": 700,
    "Maximum Latency (microseconds)": 800
  },
  "Throughput (requests/second)": 100.0,
  "Goodput (requests/second)": 99.0
}`

const benchBaseXMLMetacharPassword = `db&<safe>"quote'secret`

func TestRunAndVerifyFakePinnedDrivers(t *testing.T) {
	root := repositoryRoot(t)
	tests := []struct {
		name, driverID, workload, binary, config string
		script                                   []byte
	}{
		{
			name: "sysbench fixed argv and ephemeral pgpass", driverID: "sysbench-postgresql-1.0.20",
			workload: "oltp_read_write/postgresql", binary: fakeSysbenchExecutable(),
			config: `{
  "schema_version":"pgworkbench.sysbench-native-run-config/v1",
  "artifact_type":"pgworkbench.sysbench-native-run-config",
  "threads":2,"duration_seconds":10,"report_interval_seconds":1,
  "rate":0,"random_seed":42,
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"postgres","database":"bench"}
}`,
			script: []byte("-- retained pinned workload script\n"),
		},
		{
			name: "BenchBase strict summary and ephemeral realized config", driverID: "benchbase-postgresql-33c0047",
			workload: "tpcc", binary: fakeJavaExecutable(),
			config: `<parameters><url>jdbc:postgresql://127.0.0.1/bench</url><username>postgres</username><password>{{PGWORKBENCH_DRIVER_PASSWORD}}</password></parameters>`,
			script: []byte("PK\x03\x04fake-jar"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := t.TempDir()
			binaryPath := filepath.Join(fixture, "driver")
			configPath := filepath.Join(fixture, "config")
			scriptPath := filepath.Join(fixture, "script")
			writeFile(t, configPath, []byte(test.config), 0o600)
			var runtimeRoot string
			if test.driverID == "sysbench-postgresql-1.0.20" {
				runtimeRoot, binaryPath, scriptPath = createSysbenchRuntime(t, fixture, []byte(test.binary), test.script)
			} else {
				writeFile(t, binaryPath, []byte(test.binary), 0o755)
				runtimeRoot, scriptPath = createBenchBaseRuntime(t, fixture)
			}
			output := filepath.Join(fixture, "execution")
			now := sequenceClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC), time.Second)
			artifact, err := Run(Options{
				Root: root, DriverID: test.driverID, BinaryPath: binaryPath,
				ConfigPath: configPath, ScriptPath: scriptPath, RuntimeRoot: runtimeRoot, Workload: test.workload,
				OutputDir: output, Timeout: 30 * time.Second, Now: now,
				AcknowledgeExternalDisposableTarget: true,
				Getenv: func(name string) string {
					if name == SecretPasswordEnv {
						return "s3cret:value-123"
					}
					return ""
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Assurance.DecisionEligible || artifact.Assurance.SourceToBinaryAttested || !artifact.Assurance.DriverRuntimeClosureAttested || artifact.Assurance.HostRuntimeDependenciesAttested || artifact.Classification != Classification || artifact.Conclusion != Conclusion {
				t.Fatalf("external execution escaped assurance boundary: %#v", artifact)
			}
			if test.driverID == "benchbase-postgresql-33c0047" {
				if _, exists := runtimeFileByPath(artifact.Inputs.DriverRuntime.Files, "inputs/runtime/lib/leaf.jar"); !exists {
					t.Fatal("recursive BenchBase manifest dependency was not retained")
				}
			}
			for _, argument := range artifact.Invocation.Argv {
				if strings.Contains(argument, "s3cret:value-123") || filepath.IsAbs(argument) {
					t.Fatalf("secret or host-specific absolute path entered recorded argv: %q", argument)
				}
			}
			if !reflect.DeepEqual(artifact.Invocation.SecretEnvironment, []string{SecretPasswordEnv}) {
				t.Fatalf("unexpected secret provenance: %#v", artifact.Invocation.SecretEnvironment)
			}
			if artifact.Normalized.ArtifactDigest == "" {
				t.Fatal("strict normalized import was not bound")
			}
			result := readFile(t, filepath.Join(output, filepath.FromSlash(artifact.Outputs.DriverResult.Path)))
			normalizedRaw := readFile(t, filepath.Join(output, NormalizedImportDir, "raw", "source"))
			if !bytes.Equal(result, normalizedRaw) {
				t.Fatal("strict normalized import source is not the exact executed driver result")
			}
			verification, err := Verify(output)
			if err != nil || !verification.IsValid() {
				t.Fatalf("valid external execution rejected: verification=%#v err=%v", verification, err)
			}
			if verification.Import == nil || !verification.Import.IsValid() {
				t.Fatal("independent nested strict import verification did not run")
			}
			if findBytes(t, output, []byte("s3cret:value-123")) != "" {
				t.Fatal("secret bytes were retained in immutable external execution")
			}
			relocated := filepath.Join(fixture, "relocated")
			if err := os.Rename(output, relocated); err != nil {
				t.Fatal(err)
			}
			verification, err = Verify(filepath.Join(relocated, ExecutionFile))
			if err != nil || !verification.IsValid() {
				t.Fatalf("relocated external execution rejected: verification=%#v err=%v", verification, err)
			}
		})
	}
}

func TestRunAndVerifyFakeHammerDBExecuteOnly(t *testing.T) {
	root := repositoryRoot(t)
	tests := []struct {
		name, workload, reportFixture, jobID, config string
		sequence                                     []string
	}{
		{
			name: "TPROC-C", workload: "tprocc/postgresql",
			reportFixture: "hammerdb-6.0-tprocc-job-report.json", jobID: "67FD3C792EF803E253533323",
			config: `{
  "schema_version":"pgworkbench.hammerdb-v6-native-run-config/v1",
  "artifact_type":"pgworkbench.hammerdb-v6-native-run-config",
  "mode":"execute-only-prepared-schema",
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"tpcc","database":"tpcc","sslmode":"prefer"},
  "tprocc":{"warehouses":100,"virtual_users":32,"rampup_minutes":2,"duration_minutes":5,"total_iterations":10000000}
}`,
			sequence: []string{
				"dbset db pg", "dbset bm TPC-C", "diset connection pg_host {127.0.0.1}",
				"diset tpcc pg_user {tpcc}", "dict set configpostgresql tpcc pg_pass $pgw_password",
				"diset tpcc pg_count_ware 100", "diset tpcc pg_driver timed",
				"diset tpcc pg_total_iterations 10000000", "diset tpcc pg_rampup 2",
				"diset tpcc pg_duration 5", "vuset vu 32", "loadscript", "vucreate",
				"set pgw_run_result [vurun]", "vudestroy", "^Benchmark Run jobid=([0-9A-F]{24})$",
				"jobs $pgw_jobid save", "PGWORKBENCH_HAMMERDB_REPORT=hdb_${pgw_jobid}.json",
			},
		},
		{
			name: "TPROC-H", workload: "tproch/postgresql",
			reportFixture: "hammerdb-6.0-tproch-job-report.json", jobID: "67FD3C792EF803E253533324",
			config: `{
  "schema_version":"pgworkbench.hammerdb-v6-native-run-config/v1",
  "artifact_type":"pgworkbench.hammerdb-v6-native-run-config",
  "mode":"execute-only-prepared-schema",
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"tpch","database":"tpch","sslmode":"prefer"},
  "tproch":{"scale_factor":10,"virtual_users":1,"query_sets":1,"degree_of_parallelism":2}
}`,
			sequence: []string{
				"dbset db pg", "dbset bm TPC-H", "diset connection pg_host {127.0.0.1}",
				"diset tpch pg_tpch_user {tpch}", "dict set configpostgresql tpch pg_tpch_pass $pgw_password",
				"diset tpch pg_scale_fact 10", "diset tpch pg_total_querysets 1",
				"diset tpch pg_degree_of_parallel 2", "vuset vu 1", "loadscript", "vucreate",
				"set pgw_run_result [vurun]", "vudestroy", "^Benchmark Run jobid=([0-9A-F]{24})$",
				"jobs $pgw_jobid save", "PGWORKBENCH_HAMMERDB_REPORT=hdb_${pgw_jobid}.json",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := t.TempDir()
			report := readFile(t, filepath.Join("..", "benchmarkimport", "testdata", test.reportFixture))
			runtimeRoot, binaryPath := createHammerDBRuntime(t, fixture, []byte(fakeHammerDBExecutable(string(report), test.jobID)))
			configPath := filepath.Join(fixture, "config.json")
			templatePath := filepath.Join(fixture, "execute-only.template")
			writeFile(t, configPath, []byte(test.config), 0o600)
			writeFile(t, templatePath, []byte(HammerDBTemplate), 0o600)
			output := filepath.Join(fixture, "execution")
			secret := "hammer-secret:123"
			artifact, err := Run(Options{
				Root: root, DriverID: "hammerdb-postgresql-6.0", BinaryPath: binaryPath,
				ConfigPath: configPath, ScriptPath: templatePath, RuntimeRoot: runtimeRoot, Workload: test.workload,
				OutputDir: output, Timeout: 30 * time.Second,
				AcknowledgeExternalDisposableTarget: true,
				Now:                                 sequenceClock(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), time.Second),
				Getenv: func(name string) string {
					if name == SecretPasswordEnv {
						return secret
					}
					return ""
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Registry.Driver.Commit != "d33f879aec858063edd17aa2daa46db03abb2bae" || artifact.Registry.Driver.Parser != "hammerdb6-job-report/v1" {
				t.Fatalf("wrong HammerDB execution pin: %#v", artifact.Registry.Driver)
			}
			if artifact.Inputs.Script.Path != "inputs/adapter-template.txt" || artifact.Outputs.DriverResult.Path != "raw/driver-result.json" {
				t.Fatalf("wrong HammerDB canonical paths: %#v %#v", artifact.Inputs, artifact.Outputs)
			}
			if !reflect.DeepEqual(artifact.Invocation.Argv, []string{"inputs/runtime/hammerdbcli", "auto", "<ephemeral-adapter-generated-tcl-from:inputs/adapter-template.txt>"}) {
				t.Fatalf("HammerDB argv is not fixed: %#v", artifact.Invocation.Argv)
			}
			if !reflect.DeepEqual(artifact.Invocation.SecretEnvironment, []string{SecretPasswordEnv}) || artifact.Assurance.TPCComplianceClaim || artifact.Assurance.SourceToBinaryAttested {
				t.Fatalf("HammerDB execution escaped its bounded assurance: %#v %#v", artifact.Invocation, artifact.Assurance)
			}
			var human bytes.Buffer
			if err := Render(&human, artifact); err != nil || !strings.Contains(human.String(), "tpc_compliance=false") || !strings.Contains(human.String(), "source_to_binary_attested=false") {
				t.Fatalf("human output omitted the bounded assurance: %s err=%v", human.String(), err)
			}
			var parsed HammerDBConfig
			if err := decodeClosedJSON([]byte(test.config), &parsed, "test HammerDB config"); err != nil {
				t.Fatal(err)
			}
			generatedBytes, err := renderHammerDBTcl(parsed, test.workload)
			if err != nil {
				t.Fatal(err)
			}
			generated := string(generatedBytes)
			previous := -1
			for _, command := range test.sequence {
				index := strings.Index(generated, command)
				if index <= previous {
					t.Fatalf("generated Tcl command %q is missing or out of order:\n%s", command, generated)
				}
				previous = index
			}
			for _, forbidden := range []string{secret, "buildschema", "deleteschema", "source ", "pg_superuserpass"} {
				if strings.Contains(generated, forbidden) {
					t.Fatalf("generated Tcl contains forbidden material %q", forbidden)
				}
			}
			if found := findBytes(t, output, []byte(secret)); found != "" {
				t.Fatalf("HammerDB secret bytes were retained in %s", found)
			}
			verification, err := Verify(output)
			if err != nil || !verification.IsValid() || verification.Import == nil || verification.Import.Artifact == nil {
				t.Fatalf("valid HammerDB execution rejected: verification=%#v err=%v", verification, err)
			}
			if verification.Import.Artifact.Errors.Complete || verification.Import.Artifact.Assurance.TPCComplianceClaim {
				t.Fatalf("HammerDB import fabricated complete errors or TPC compliance: %#v", verification.Import.Artifact)
			}
			relocated := filepath.Join(fixture, "relocated")
			if err := os.Rename(output, relocated); err != nil {
				t.Fatal(err)
			}
			verification, err = Verify(filepath.Join(relocated, ExecutionFile))
			if err != nil || !verification.IsValid() {
				t.Fatalf("relocated HammerDB execution rejected: verification=%#v err=%v", verification, err)
			}
			resultPath := filepath.Join(relocated, "raw", "driver-result.json")
			writeFile(t, resultPath, append(readFile(t, resultPath), []byte("\n")...), 0o644)
			verification, err = Verify(relocated)
			if err != nil || verification.IsValid() || !issuesContain(verification.Issues, "digest or size mismatch") {
				t.Fatalf("tampered HammerDB report passed: verification=%#v err=%v", verification, err)
			}
		})
	}
}

func TestShippedHammerDBConfigsAreClosedAndTemplateMarkerIsNotShipped(t *testing.T) {
	root := repositoryRoot(t)
	tests := []struct {
		file, workload string
	}{
		{"hammerdb-v6-tprocc-postgresql.json", "tprocc/postgresql"},
		{"hammerdb-v6-tproch-postgresql.json", "tproch/postgresql"},
	}
	for _, test := range tests {
		content := readFile(t, filepath.Join(root, "configs", "benchmark-drivers", test.file))
		var config HammerDBConfig
		if err := decodeClosedJSON(content, &config, "shipped HammerDB config"); err != nil {
			t.Fatal(err)
		}
		if err := validateHammerDBConfig(config, test.workload); err != nil {
			t.Fatal(err)
		}
	}
	template := filepath.Join(root, "configs", "benchmark-drivers", "hammerdb-v6-execute-only.template")
	if _, err := os.Lstat(template); !os.IsNotExist(err) {
		t.Fatalf("HammerDB template marker must be ephemeral and absent from the shipped config tree: %v", err)
	}
}

func TestHammerDBAdapterRejectsArbitraryTclAndAmbiguousConfig(t *testing.T) {
	driver, err := canonicalExecutionDriver("hammerdb-postgresql-6.0")
	if err != nil {
		t.Fatal(err)
	}
	valid := []byte(`{
  "schema_version":"pgworkbench.hammerdb-v6-native-run-config/v1",
  "artifact_type":"pgworkbench.hammerdb-v6-native-run-config",
  "mode":"execute-only-prepared-schema",
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"tpcc","database":"tpcc","sslmode":"prefer"},
  "tprocc":{"warehouses":1,"virtual_users":1,"rampup_minutes":0,"duration_minutes":1,"total_iterations":1}
}`)
	tests := []struct {
		name, workload string
		config         []byte
		template       []byte
		want           string
	}{
		{name: "caller Tcl", workload: "tprocc/postgresql", config: valid, template: []byte("exec touch /tmp/escaped\n"), want: "not caller-supplied Tcl"},
		{name: "unknown config key", workload: "tprocc/postgresql", config: bytes.Replace(valid, []byte(`"mode":`), []byte(`"unknown":true,"mode":`), 1), template: []byte(HammerDBTemplate), want: "unknown field"},
		{name: "unknown nested key", workload: "tprocc/postgresql", config: bytes.Replace(valid, []byte(`"warehouses":1`), []byte(`"warehouses":1,"build_schema":true`), 1), template: []byte(HammerDBTemplate), want: "unknown field"},
		{name: "both workload sections", workload: "tprocc/postgresql", config: bytes.Replace(valid, []byte(`}`), []byte(`,"tproch":{"scale_factor":1,"virtual_users":1,"query_sets":1,"degree_of_parallelism":1}}`), 1), template: []byte(HammerDBTemplate), want: "decode"},
		{name: "unsupported workload", workload: "tpcc/postgresql", config: valid, template: []byte(HammerDBTemplate), want: "not pinned"},
		{name: "unsafe token", workload: "tprocc/postgresql", config: bytes.Replace(valid, []byte(`"user":"tpcc"`), []byte(`"user":"tpcc;exec"`), 1), template: []byte(HammerDBTemplate), want: "safe portable token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := prepareInvocation(t.TempDir(), driver, test.workload, test.config, test.template, "password-123")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

func TestHammerDBRunRejectsJobIDAndExposedConfigMismatch(t *testing.T) {
	root := repositoryRoot(t)
	baseReport := string(readFile(t, filepath.Join("..", "benchmarkimport", "testdata", "hammerdb-6.0-tproch-job-report.json")))
	config := `{
  "schema_version":"pgworkbench.hammerdb-v6-native-run-config/v1",
  "artifact_type":"pgworkbench.hammerdb-v6-native-run-config",
  "mode":"execute-only-prepared-schema",
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"tpch","database":"tpch","sslmode":"prefer"},
  "tproch":{"scale_factor":10,"virtual_users":1,"query_sets":1,"degree_of_parallelism":2}
}`
	tests := []struct {
		name, report, marker, want string
	}{
		{name: "report job id", report: baseReport, marker: "AAAAAAAAAAAAAAAAAAAAAAAA", want: "saved report identity"},
		{name: "reported query sets", report: strings.Replace(baseReport, `"query_sets": 1`, `"query_sets": 2`, 1), marker: "67FD3C792EF803E253533324", want: "result.query_sets"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := t.TempDir()
			runtimeRoot, binary := createHammerDBRuntime(t, fixture, []byte(fakeHammerDBExecutable(test.report, test.marker)))
			configPath := filepath.Join(fixture, "config.json")
			template := filepath.Join(fixture, "execute-only.template")
			output := filepath.Join(fixture, "execution")
			writeFile(t, configPath, []byte(config), 0o600)
			writeFile(t, template, []byte(HammerDBTemplate), 0o600)
			_, err := Run(Options{
				Root: root, DriverID: "hammerdb-postgresql-6.0", BinaryPath: binary,
				ConfigPath: configPath, ScriptPath: template, RuntimeRoot: runtimeRoot, Workload: "tproch/postgresql",
				OutputDir: output, Timeout: 30 * time.Second,
				AcknowledgeExternalDisposableTarget: true,
				Getenv: func(name string) string {
					if name == SecretPasswordEnv {
						return "hammer-secret:123"
					}
					return ""
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
			if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
				t.Fatal("invalid HammerDB execution was published")
			}
		})
	}
}

func TestJARManifestClassPathIsStrictAndSupportsFolding(t *testing.T) {
	valid := testJAR(t, "Manifest-Version: 1.0\r\nClass-Path: lib/a.jar lib/b\r\n .jar\r\n\r\n")
	entries, err := jarManifestClassPath(valid)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entries, []string{"lib/a.jar", "lib/b.jar"}) {
		t.Fatalf("unexpected folded manifest classpath: %#v", entries)
	}
	var duplicate bytes.Buffer
	duplicateWriter := zip.NewWriter(&duplicate)
	for range 2 {
		entry, err := duplicateWriter.Create("META-INF/MANIFEST.MF")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("Manifest-Version: 1.0\r\n\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := duplicateWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := jarManifestClassPath(duplicate.Bytes()); err == nil || !strings.Contains(err.Error(), "duplicate META-INF/MANIFEST.MF") {
		t.Fatalf("duplicate manifest was not rejected: %v", err)
	}

	for _, test := range []struct {
		name, manifest, want string
	}{
		{name: "wrong manifest version", manifest: "Manifest-Version: 2.0\r\nClass-Path: lib/a.jar\r\n\r\n", want: "Manifest-Version"},
		{name: "duplicate classpath", manifest: "Manifest-Version: 1.0\r\nClass-Path: lib/a.jar\r\nClass-Path: lib/b.jar\r\n\r\n", want: "repeats header"},
		{name: "parent traversal", manifest: "Manifest-Version: 1.0\r\nClass-Path: ../a.jar\r\n\r\n", want: "unsafe path segment"},
		{name: "encoded path", manifest: "Manifest-Version: 1.0\r\nClass-Path: lib/a%20.jar\r\n\r\n", want: "accepted relative JAR subset"},
		{name: "URL scheme", manifest: "Manifest-Version: 1.0\r\nClass-Path: file:lib/a.jar\r\n\r\n", want: "accepted relative JAR subset"},
		{name: "noncanonical separators", manifest: "Manifest-Version: 1.0\r\nClass-Path: lib/a.jar  lib/b.jar\r\n\r\n", want: "single-ASCII-space-delimited"},
		{name: "unterminated main section", manifest: "Manifest-Version: 1.0\r\nClass-Path: lib/a.jar\r\n", want: "not terminated"},
		{name: "oversized physical line", manifest: "Manifest-Version: 1.0\r\nX-Long: " + strings.Repeat("a", 70) + "\r\n\r\n", want: "exceeds 72 bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := jarManifestClassPath(testJAR(t, test.manifest)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

func TestSourceRuntimeRootsMustBeExactCuratedClosures(t *testing.T) {
	t.Run("sysbench extra file", func(t *testing.T) {
		fixture := t.TempDir()
		runtimeRoot, binaryPath, scriptPath := createSysbenchRuntime(t, fixture, []byte(fakeSysbenchExecutable()), []byte("-- fixture\n"))
		runtimeRoot = canonicalExistingPath(t, runtimeRoot)
		binaryPath = canonicalExistingPath(t, binaryPath)
		scriptPath = canonicalExistingPath(t, scriptPath)
		writeFile(t, filepath.Join(runtimeRoot, "share", "sysbench", "surprise.lua"), []byte("-- extra\n"), 0o644)
		_, err := stageSysbenchRuntime(t.TempDir(), Options{
			RuntimeRoot: runtimeRoot, BinaryPath: binaryPath, ScriptPath: scriptPath, Workload: "oltp_read_write/postgresql",
		}, readFile(t, binaryPath), readFile(t, scriptPath), "")
		if err == nil || !strings.Contains(err.Error(), "exact curated closure") {
			t.Fatalf("extra sysbench runtime file was not rejected: %v", err)
		}
	})

	t.Run("BenchBase extra directory", func(t *testing.T) {
		fixture := t.TempDir()
		runtimeRoot, entrypoint := createBenchBaseRuntime(t, fixture)
		if err := os.Mkdir(filepath.Join(runtimeRoot, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		runtimeRoot = canonicalExistingPath(t, runtimeRoot)
		entrypoint = canonicalExistingPath(t, entrypoint)
		_, err := stageBenchBaseRuntime(t.TempDir(), Options{
			RuntimeRoot: runtimeRoot, BinaryPath: filepath.Join(fixture, "java"), ScriptPath: entrypoint,
		}, []byte(fakeJavaExecutable()), readFile(t, entrypoint), "")
		if err == nil || !strings.Contains(err.Error(), "exact curated closure") {
			t.Fatalf("extra BenchBase runtime directory was not rejected: %v", err)
		}
	})

	t.Run("HammerDB extra entry", func(t *testing.T) {
		fixture := t.TempDir()
		runtimeRoot, launcher := createHammerDBRuntime(t, fixture, []byte(fakeHammerDBExecutable("{}", "67FD3C792EF803E253533323")))
		writeFile(t, filepath.Join(runtimeRoot, "README"), []byte("extra\n"), 0o644)
		runtimeRoot = canonicalExistingPath(t, runtimeRoot)
		launcher = canonicalExistingPath(t, launcher)
		_, err := stageHammerDBRuntime(t.TempDir(), Options{RuntimeRoot: runtimeRoot, BinaryPath: launcher}, readFile(t, launcher), "")
		if err == nil || !strings.Contains(err.Error(), "exactly one regular file") {
			t.Fatalf("extra HammerDB runtime entry was not rejected: %v", err)
		}
	})
}

func TestRuntimeRootCanonicalizesAncestorAliasesAndRejectsInternalSymlinks(t *testing.T) {
	t.Run("relative root", func(t *testing.T) {
		if _, err := resolveDriverRuntimeRoot("relative/runtime"); err == nil || !strings.Contains(err.Error(), "absolute clean path") {
			t.Fatalf("relative runtime root was not rejected before canonicalization: %v", err)
		}
	})

	t.Run("ancestor alias", func(t *testing.T) {
		root := t.TempDir()
		resolved, err := resolveDriverRuntimeRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		if resolved != want {
			t.Fatalf("runtime root was not canonicalized once: got %q want %q", resolved, want)
		}
	})

	t.Run("internal symlink", func(t *testing.T) {
		fixture := t.TempDir()
		runtimeRoot, binaryPath, scriptPath := createSysbenchRuntime(t, fixture, []byte(fakeSysbenchExecutable()), []byte("-- fixture\n"))
		realShare := filepath.Join(runtimeRoot, "real-share")
		if err := os.Rename(filepath.Join(runtimeRoot, "share"), realShare); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("real-share", filepath.Join(runtimeRoot, "share")); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(fixture, "config.json")
		writeFile(t, configPath, []byte(`{
  "schema_version":"pgworkbench.sysbench-native-run-config/v1",
  "artifact_type":"pgworkbench.sysbench-native-run-config",
  "threads":1,"duration_seconds":10,"report_interval_seconds":1,"rate":0,"random_seed":1,
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"postgres","database":"bench"}
}`), 0o644)
		_, err := Run(Options{
			Root: repositoryRoot(t), DriverID: "sysbench-postgresql-1.0.20", RuntimeRoot: runtimeRoot,
			BinaryPath: binaryPath, ConfigPath: configPath, ScriptPath: scriptPath,
			Workload: "oltp_read_write/postgresql", OutputDir: filepath.Join(fixture, "execution"), Timeout: 30 * time.Second,
			Getenv: func(string) string { return "" }, AcknowledgeExternalDisposableTarget: true,
		})
		if err == nil || !strings.Contains(err.Error(), "runtime path must not contain symlinks") {
			t.Fatalf("internal runtime symlink was not rejected: %v", err)
		}
	})
}

func TestVerifyRejectsTamperingExtraFilesAndClosedJSON(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{
			name: "driver result tampering",
			mutate: func(t *testing.T, root string) {
				path := filepath.Join(root, "raw", "driver-result.txt")
				writeFile(t, path, append(readFile(t, path), []byte("tampered\n")...), 0o644)
			},
			want: "digest or size mismatch",
		},
		{
			name: "retained driver runtime tampering",
			mutate: func(t *testing.T, root string) {
				path := filepath.Join(root, filepath.FromSlash(DriverRuntimeDir), "share", "sysbench", "oltp_common.lua")
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, append(readFile(t, path), []byte("-- tampered\n")...), 0o444)
			},
			want: "digest or size mismatch",
		},
		{
			name: "unexpected file",
			mutate: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "raw", "surprise"), []byte("x"), 0o644)
			},
			want: "inventory is missing file",
		},
		{
			name: "unknown execution field",
			mutate: func(t *testing.T, root string) {
				path := filepath.Join(root, ExecutionFile)
				content := readFile(t, path)
				content = bytes.Replace(content, []byte(`"schema_version"`), []byte(`"unexpected":true,"schema_version"`), 1)
				writeFile(t, path, content, 0o644)
			},
			want: "unknown field",
		},
		{
			name: "coherently redigested target acknowledgement",
			mutate: func(t *testing.T, root string) {
				var artifact Artifact
				if err := decodeClosedJSON(readFile(t, filepath.Join(root, ExecutionFile)), &artifact, "fixture execution"); err != nil {
					t.Fatal(err)
				}
				artifact.TargetSafety.Database = "different_bench"
				artifact.Digest, _ = artifactDigest(artifact)
				executionContent, err := json.MarshalIndent(artifact, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, ExecutionFile), append(executionContent, '\n'), 0o644)
				if err := os.Remove(filepath.Join(root, InventoryFile)); err != nil {
					t.Fatal(err)
				}
				inventory, err := buildInventory(root, artifact.Digest)
				if err != nil {
					t.Fatal(err)
				}
				inventoryContent, err := json.MarshalIndent(inventory, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, InventoryFile), append(inventoryContent, '\n'), 0o644)
			},
			want: "does not match the independently extracted retained driver target",
		},
		{
			name: "coherently redigested false acknowledgement",
			mutate: func(t *testing.T, root string) {
				var artifact Artifact
				if err := decodeClosedJSON(readFile(t, filepath.Join(root, ExecutionFile)), &artifact, "fixture execution"); err != nil {
					t.Fatal(err)
				}
				artifact.TargetSafety.Acknowledged = false
				artifact.Digest, _ = artifactDigest(artifact)
				executionContent, err := json.MarshalIndent(artifact, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, ExecutionFile), append(executionContent, '\n'), 0o644)
				if err := os.Remove(filepath.Join(root, InventoryFile)); err != nil {
					t.Fatal(err)
				}
				inventory, err := buildInventory(root, artifact.Digest)
				if err != nil {
					t.Fatal(err)
				}
				inventoryContent, err := json.MarshalIndent(inventory, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, InventoryFile), append(inventoryContent, '\n'), 0o644)
			},
			want: "target-safety policy or acknowledgement is invalid",
		},
		{
			name: "coherently redigested registry pin",
			mutate: func(t *testing.T, root string) {
				lockPath := filepath.Join(root, filepath.FromSlash(LockFile))
				registry, err := benchmarkdrivers.Parse(readFile(t, lockPath))
				if err != nil {
					t.Fatal(err)
				}
				for index := range registry.Drivers {
					if registry.Drivers[index].ID == "sysbench-postgresql-1.0.20" {
						registry.Drivers[index].Commit = strings.Repeat("a", 40)
					}
				}
				lockContent, err := json.MarshalIndent(registry, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				lockContent = append(lockContent, '\n')
				writeFile(t, lockPath, lockContent, 0o644)

				var artifact Artifact
				if err := decodeClosedJSON(readFile(t, filepath.Join(root, ExecutionFile)), &artifact, "fixture execution"); err != nil {
					t.Fatal(err)
				}
				artifact.Registry.Lock = fileRef(LockFile, lockContent)
				artifact.Registry.Driver.Commit = strings.Repeat("a", 40)
				artifact.Digest, err = artifactDigest(artifact)
				if err != nil {
					t.Fatal(err)
				}
				executionContent, err := json.MarshalIndent(artifact, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, ExecutionFile), append(executionContent, '\n'), 0o644)
				if err := os.Remove(filepath.Join(root, InventoryFile)); err != nil {
					t.Fatal(err)
				}
				inventory, err := buildInventory(root, artifact.Digest)
				if err != nil {
					t.Fatal(err)
				}
				inventoryContent, err := json.MarshalIndent(inventory, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, InventoryFile), append(inventoryContent, '\n'), 0o644)
			},
			want: "does not exactly match the execution adapter pin",
		},
		{
			name: "coherently redigested expanded driver runtime",
			mutate: func(t *testing.T, root string) {
				extraPath := path.Join(DriverRuntimeDir, "share/sysbench/surprise.lua")
				extraContent := []byte("-- coherently injected runtime file\n")
				writeFile(t, filepath.Join(root, filepath.FromSlash(extraPath)), extraContent, 0o444)
				var artifact Artifact
				if err := decodeClosedJSON(readFile(t, filepath.Join(root, ExecutionFile)), &artifact, "fixture execution"); err != nil {
					t.Fatal(err)
				}
				files := append([]RuntimeFileRef(nil), artifact.Inputs.DriverRuntime.Files...)
				files = append(files, RuntimeFileRef{Path: extraPath, Digest: fileRef(extraPath, extraContent).Digest, SizeBytes: int64(len(extraContent)), Mode: 0o444})
				runtime, err := finalizeDriverRuntime(artifact.Inputs.DriverRuntime.Strategy, artifact.Inputs.DriverRuntime.Entrypoint, files)
				if err != nil {
					t.Fatal(err)
				}
				artifact.Inputs.DriverRuntime = runtime
				rewriteExecutionAndInventory(t, root, artifact)
			},
			want: "does not match its independently derived closure",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := createSysbenchExecution(t)
			test.mutate(t, output)
			verification, err := Verify(output)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !issuesContain(verification.Issues, test.want) {
				t.Fatalf("tampering passed: issues=%v want=%q", verification.Issues, test.want)
			}
		})
	}
}

func TestRunRejectsSymlinksOverwriteDangerousConfigAndBinaryMutation(t *testing.T) {
	root := repositoryRoot(t)
	fixture := t.TempDir()
	validConfig := []byte(`{
  "schema_version":"pgworkbench.sysbench-native-run-config/v1",
  "artifact_type":"pgworkbench.sysbench-native-run-config",
  "threads":1,"duration_seconds":10,"report_interval_seconds":1,"rate":0,"random_seed":1,
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"postgres","database":"bench"}
}`)
	runtimeRoot, binaryPath, scriptPath := createSysbenchRuntime(t, fixture, []byte(fakeSysbenchExecutable()), []byte("-- fixture\n"))
	configPath := filepath.Join(fixture, "config.json")
	writeFile(t, configPath, validConfig, 0o644)
	base := Options{
		Root: root, DriverID: "sysbench-postgresql-1.0.20", BinaryPath: binaryPath,
		ConfigPath: configPath, ScriptPath: scriptPath, RuntimeRoot: runtimeRoot, Workload: "oltp_read_write/postgresql",
		Timeout: 30 * time.Second, Getenv: func(string) string { return "" },
		AcknowledgeExternalDisposableTarget: true,
	}

	t.Run("missing explicit disposable target acknowledgement", func(t *testing.T) {
		options := base
		options.AcknowledgeExternalDisposableTarget = false
		options.OutputDir = filepath.Join(fixture, "missing-ack-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "--acknowledge-external-disposable-target") {
			t.Fatalf("missing target acknowledgement was not rejected: %v", err)
		}
		if _, err := os.Lstat(options.OutputDir); !os.IsNotExist(err) {
			t.Fatal("missing acknowledgement mutated the output destination")
		}
	})

	t.Run("missing runtime root", func(t *testing.T) {
		options := base
		options.RuntimeRoot = ""
		options.OutputDir = filepath.Join(fixture, "missing-runtime-root-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "--runtime-root is required") {
			t.Fatalf("missing runtime root was not rejected: %v", err)
		}
		if _, err := os.Lstat(options.OutputDir); !os.IsNotExist(err) {
			t.Fatal("missing runtime root mutated the output destination")
		}
	})

	t.Run("relative runtime root", func(t *testing.T) {
		options := base
		options.RuntimeRoot = "relative/runtime"
		options.OutputDir = filepath.Join(fixture, "relative-runtime-root-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "absolute clean path") {
			t.Fatalf("relative runtime root was not rejected by Run: %v", err)
		}
		if _, err := os.Lstat(options.OutputDir); !os.IsNotExist(err) {
			t.Fatal("relative runtime root mutated the output destination")
		}
	})

	t.Run("symlink input", func(t *testing.T) {
		alias := filepath.Join(fixture, "config-link")
		if err := os.Symlink(configPath, alias); err != nil {
			t.Fatal(err)
		}
		options := base
		options.ConfigPath = alias
		options.OutputDir = filepath.Join(fixture, "symlink-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("symlink input was not rejected: %v", err)
		}
	})

	t.Run("immutable overwrite", func(t *testing.T) {
		options := base
		options.OutputDir = filepath.Join(fixture, "existing")
		if err := os.Mkdir(options.OutputDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
			t.Fatalf("existing destination was not rejected: %v", err)
		}
	})

	t.Run("output inside runtime root", func(t *testing.T) {
		options := base
		parent := filepath.Join(runtimeRoot, "must-not-be-created")
		options.OutputDir = filepath.Join(parent, "execution")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "outside --runtime-root") {
			t.Fatalf("output inside runtime root was not rejected: %v", err)
		}
		if _, err := os.Lstat(parent); !os.IsNotExist(err) {
			t.Fatal("rejected output path mutated the curated runtime root")
		}
	})

	t.Run("shell metacharacters", func(t *testing.T) {
		var config SysbenchConfig
		if err := json.Unmarshal(validConfig, &config); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(fixture, "must-not-exist")
		config.PostgreSQL.User = "postgres;touch" + sentinel
		content, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		dangerous := filepath.Join(fixture, "dangerous.json")
		writeFile(t, dangerous, content, 0o644)
		options := base
		options.ConfigPath = dangerous
		options.OutputDir = filepath.Join(fixture, "dangerous-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "safe portable token") {
			t.Fatalf("dangerous token was not rejected: %v", err)
		}
		if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
			t.Fatal("shell metacharacters caused an external side effect")
		}
	})

	t.Run("binary changes during run", func(t *testing.T) {
		writeFile(t, binaryPath, []byte(fakeMutatingSysbenchExecutable()), 0o755)
		options := base
		options.OutputDir = filepath.Join(fixture, "mutating-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "changed during execution") {
			t.Fatalf("binary mutation was not rejected: %v", err)
		}
		if _, err := os.Lstat(options.OutputDir); !os.IsNotExist(err) {
			t.Fatal("failed execution was published")
		}
	})

	t.Run("sysbench secondary stderr channel", func(t *testing.T) {
		content := "#!/bin/sh\nprintf '%s\\n' 'ERROR: secondary channel' >&2\n" +
			"cat <<'PGWORKBENCH_RESULT'\n" + fakeSysbenchResult + "PGWORKBENCH_RESULT\n"
		writeFile(t, binaryPath, []byte(content), 0o755)
		options := base
		options.OutputDir = filepath.Join(fixture, "stderr-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "outside its strictly normalized") {
			t.Fatalf("secondary sysbench error channel was not rejected: %v", err)
		}
	})

	t.Run("timeout kills process group", func(t *testing.T) {
		writeFile(t, binaryPath, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o755)
		options := base
		options.Timeout = time.Second
		options.OutputDir = filepath.Join(fixture, "timeout-output")
		started := time.Now()
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "exceeded timeout") {
			t.Fatalf("timeout was not enforced: %v", err)
		}
		if time.Since(started) > 10*time.Second {
			t.Fatal("timed-out external driver was not terminated promptly")
		}
		if _, err := os.Lstat(options.OutputDir); !os.IsNotExist(err) {
			t.Fatal("timed-out execution was published")
		}
	})

	t.Run("live descendant is killed and rejected", func(t *testing.T) {
		writeFile(t, binaryPath, []byte("#!/bin/sh\n/bin/sleep 30 &\nexit 0\n"), 0o755)
		options := base
		options.OutputDir = filepath.Join(fixture, "descendant-output")
		if _, err := Run(options); err == nil || !strings.Contains(err.Error(), "live descendants") {
			t.Fatalf("live descendant was not rejected: %v", err)
		}
		if _, err := os.Lstat(options.OutputDir); !os.IsNotExist(err) {
			t.Fatal("execution with a live descendant was published")
		}
	})
}

func TestAllExternalAdaptersRejectRemoteAndSystemTargets(t *testing.T) {
	tests := []struct {
		name, driverID, workload string
		config, script           []byte
		want                     string
	}{
		{
			name: "sysbench remote", driverID: "sysbench-postgresql-1.0.20", workload: "oltp_read_write/postgresql",
			config: []byte(`{"schema_version":"pgworkbench.sysbench-native-run-config/v1","artifact_type":"pgworkbench.sysbench-native-run-config","threads":1,"duration_seconds":1,"report_interval_seconds":1,"rate":0,"random_seed":1,"postgresql":{"host":"db.example.com","port":5432,"user":"bench","database":"bench"}}`),
			script: []byte("-- workload\n"), want: "remote targets are not supported",
		},
		{
			name: "sysbench system database", driverID: "sysbench-postgresql-1.0.20", workload: "oltp_read_write/postgresql",
			config: []byte(`{"schema_version":"pgworkbench.sysbench-native-run-config/v1","artifact_type":"pgworkbench.sysbench-native-run-config","threads":1,"duration_seconds":1,"report_interval_seconds":1,"rate":0,"random_seed":1,"postgresql":{"host":"127.0.0.1","port":5432,"user":"bench","database":"POSTGRES"}}`),
			script: []byte("-- workload\n"), want: "protected PostgreSQL system database",
		},
		{
			name: "HammerDB remote", driverID: "hammerdb-postgresql-6.0", workload: "tprocc/postgresql",
			config: []byte(`{"schema_version":"pgworkbench.hammerdb-v6-native-run-config/v1","artifact_type":"pgworkbench.hammerdb-v6-native-run-config","mode":"execute-only-prepared-schema","postgresql":{"host":"10.0.0.2","port":5432,"user":"tpcc","database":"tpcc","sslmode":"prefer"},"tprocc":{"warehouses":1,"virtual_users":1,"rampup_minutes":0,"duration_minutes":1,"total_iterations":1}}`),
			script: []byte(HammerDBTemplate), want: "remote targets are not supported",
		},
		{
			name: "HammerDB system database", driverID: "hammerdb-postgresql-6.0", workload: "tprocc/postgresql",
			config: []byte(`{"schema_version":"pgworkbench.hammerdb-v6-native-run-config/v1","artifact_type":"pgworkbench.hammerdb-v6-native-run-config","mode":"execute-only-prepared-schema","postgresql":{"host":"127.0.0.1","port":5432,"user":"tpcc","database":"Template1","sslmode":"prefer"},"tprocc":{"warehouses":1,"virtual_users":1,"rampup_minutes":0,"duration_minutes":1,"total_iterations":1}}`),
			script: []byte(HammerDBTemplate), want: "protected PostgreSQL system database",
		},
		{
			name: "BenchBase remote", driverID: "benchbase-postgresql-33c0047", workload: "tpcc",
			config: []byte(`<parameters><url>jdbc:postgresql://db.example.com:5432/bench</url><password>{{PGWORKBENCH_DRIVER_PASSWORD}}</password></parameters>`),
			script: []byte("PK\x03\x04jar"), want: "remote targets are not supported",
		},
		{
			name: "BenchBase system database", driverID: "benchbase-postgresql-33c0047", workload: "tpcc",
			config: []byte(`<parameters><url>jdbc:postgresql://127.0.0.1:5432/template0</url><password>{{PGWORKBENCH_DRIVER_PASSWORD}}</password></parameters>`),
			script: []byte("PK\x03\x04jar"), want: "protected PostgreSQL system database",
		},
		{
			name: "BenchBase JDBC parameters", driverID: "benchbase-postgresql-33c0047", workload: "tpcc",
			config: []byte(`<parameters><url>jdbc:postgresql://127.0.0.1:5432/bench?sslmode=disable</url><password>{{PGWORKBENCH_DRIVER_PASSWORD}}</password></parameters>`),
			script: []byte("PK\x03\x04jar"), want: "must not contain userinfo, parameters",
		},
		{
			name: "BenchBase ambiguous URL", driverID: "benchbase-postgresql-33c0047", workload: "tpcc",
			config: []byte(`<parameters><url>jdbc:postgresql://127.0.0.1/bench</url><url>jdbc:postgresql://[::1]/bench</url><password>{{PGWORKBENCH_DRIVER_PASSWORD}}</password></parameters>`),
			script: []byte("PK\x03\x04jar"), want: "exactly one plain url",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			driver, err := canonicalExecutionDriver(test.driverID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = prepareInvocation(t.TempDir(), driver, test.workload, test.config, test.script, "password-123")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want an error containing %q", err, test.want)
			}
		})
	}
}

func TestBenchBaseJDBCTargetAcceptsOnlyCanonicalLoopbackForms(t *testing.T) {
	for _, test := range []struct {
		name, jdbc, host string
		port             uint16
		wantError        string
	}{
		{name: "IPv4", jdbc: "jdbc:postgresql://127.0.0.1:5433/bench", host: "127.0.0.1", port: 5433},
		{name: "IPv6", jdbc: "jdbc:postgresql://[::1]:5434/bench", host: "::1", port: 5434},
		{name: "hostname rejected", jdbc: "jdbc:postgresql://localhost/bench", wantError: "hostnames and remote targets"},
		{name: "unbracketed IPv6", jdbc: "jdbc:postgresql://::1:5432/bench", wantError: "target"},
		{name: "escaped host", jdbc: "jdbc:postgresql://local%68ost/bench", wantError: "unescaped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := []byte("<parameters><url>" + test.jdbc + "</url></parameters>")
			target, err := extractBenchBaseTarget(content)
			if test.wantError != "" {
				if err == nil {
					t.Fatalf("unsafe JDBC target passed: %#v", target)
				}
				return
			}
			if err != nil || target.host != test.host || target.port != test.port || target.database != "bench" {
				t.Fatalf("canonical JDBC target rejected or misparsed: target=%#v err=%v", target, err)
			}
		})
	}
}

func TestBenchBaseTemplateRestrictsPasswordPlaceholderLocations(t *testing.T) {
	prefix := `<parameters><url>jdbc:postgresql://127.0.0.1/bench</url>`
	suffix := `</parameters>`
	for _, test := range []struct {
		name, body, want string
	}{
		{name: "comment", body: `<!-- ` + passwordPlaceholder + ` -->`, want: "comments"},
		{name: "non-sensitive element", body: `<note>` + passwordPlaceholder + `</note>`, want: "only as the entire value"},
		{name: "mixed sensitive element", body: `<password>prefix-` + passwordPlaceholder + `</password>`, want: "mixed secret-like element"},
		{name: "wrapped sensitive element", body: `<password> ` + passwordPlaceholder + ` </password>`, want: "mixed secret-like element"},
		{name: "non-sensitive attribute", body: `<connection label="` + passwordPlaceholder + `"></connection>`, want: "only as the entire value"},
		{name: "mixed sensitive attribute", body: `<connection password="prefix-` + passwordPlaceholder + `"></connection>`, want: "mixed secret-like attribute"},
		{name: "nested sensitive element", body: `<password><value>` + passwordPlaceholder + `</value></password>`, want: "sensitive element must contain only"},
		{name: "retained sensitive value", body: `<password>literal-secret</password>`, want: "retained or mixed secret-like element"},
	} {
		t.Run(test.name, func(t *testing.T) {
			usesPassword, err := validateBenchBaseTemplate([]byte(prefix + test.body + suffix))
			if err == nil || usesPassword || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unsafe placeholder location was not rejected: uses=%t err=%v want=%q", usesPassword, err, test.want)
			}
		})
	}

	valid := []byte(prefix + `<password>` + passwordPlaceholder + `</password><connection credential="` + passwordPlaceholder + `"></connection>` + suffix)
	usesPassword, err := validateBenchBaseTemplate(valid)
	if err != nil || !usesPassword {
		t.Fatalf("entire sensitive element/attribute placeholders were rejected: uses=%t err=%v", usesPassword, err)
	}
	realized, err := realizeBenchBaseConfig(valid, benchBaseXMLMetacharPassword)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Password   string `xml:"password"`
		Connection struct {
			Credential string `xml:"credential,attr"`
		} `xml:"connection"`
	}
	if err := xml.Unmarshal(realized, &parsed); err != nil {
		t.Fatalf("realized config is not valid XML: %v\n%s", err, realized)
	}
	if parsed.Password != benchBaseXMLMetacharPassword || parsed.Connection.Credential != benchBaseXMLMetacharPassword {
		t.Fatalf("realized XML values were not decoded losslessly: %#v", parsed)
	}
	for _, escaped := range []string{"&amp;", "&lt;", "&gt;", "&#34;", "&#39;"} {
		if !bytes.Contains(realized, []byte(escaped)) {
			t.Fatalf("realized XML does not contain required escaping %q: %s", escaped, realized)
		}
	}
}

func TestRunBenchBaseEscapesEphemeralPasswordAndRetainsNoSecret(t *testing.T) {
	fixture := t.TempDir()
	binaryPath := filepath.Join(fixture, "java-helper")
	writeFile(t, binaryPath, []byte(fakeJavaXMLCheckingExecutable(t)), 0o755)
	runtimeRoot, scriptPath := createBenchBaseRuntime(t, fixture)
	configPath := filepath.Join(fixture, "config.xml")
	writeFile(t, configPath, []byte(`<parameters><url>jdbc:postgresql://127.0.0.1/bench</url><username>postgres</username><password>`+passwordPlaceholder+`</password><connection credential="`+passwordPlaceholder+`"></connection></parameters>`), 0o600)
	output := filepath.Join(fixture, "execution")
	artifact, err := Run(Options{
		Root: repositoryRoot(t), DriverID: "benchbase-postgresql-33c0047", BinaryPath: binaryPath,
		ConfigPath: configPath, ScriptPath: scriptPath, RuntimeRoot: runtimeRoot, Workload: "tpcc",
		OutputDir: output, Timeout: 30 * time.Second, AcknowledgeExternalDisposableTarget: true,
		Getenv: func(name string) string {
			if name == SecretPasswordEnv {
				return benchBaseXMLMetacharPassword
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := Verify(output)
	if err != nil || !verification.IsValid() {
		t.Fatalf("BenchBase XML-metachar execution is invalid: verification=%#v err=%v", verification, err)
	}
	if !reflect.DeepEqual(artifact.Invocation.SecretEnvironment, []string{SecretPasswordEnv}) {
		t.Fatalf("unexpected BenchBase secret provenance: %#v", artifact.Invocation.SecretEnvironment)
	}
	if retainedAt := findBytes(t, output, []byte(benchBaseXMLMetacharPassword)); retainedAt != "" {
		t.Fatalf("decoded password was retained at %s", retainedAt)
	}
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(benchBaseXMLMetacharPassword)); err != nil {
		t.Fatal(err)
	}
	if retainedAt := findBytes(t, output, escaped.Bytes()); retainedAt != "" {
		t.Fatalf("XML-escaped password was retained at %s", retainedAt)
	}
}

func TestBenchBaseJavaHelperProcess(t *testing.T) {
	if os.Getenv("PGWORKBENCH_TEST_BENCHBASE_JAVA_HELPER") != "1" {
		return
	}
	fail := func(message string) {
		_, _ = os.Stderr.WriteString(message + "\n")
		os.Exit(2)
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		fail("missing helper argument separator")
	}
	arguments := os.Args[separator+1:]
	if len(arguments) != 11 || arguments[0] != "-jar" || arguments[2] != "-b" || arguments[3] != "tpcc" || arguments[4] != "-c" || arguments[6] != "-d" || arguments[8] != "--create=false" || arguments[9] != "--load=false" || arguments[10] != "--execute=true" {
		fail("unexpected fixed BenchBase argv")
	}
	content, err := os.ReadFile(arguments[5])
	if err != nil {
		fail("read realized BenchBase config: " + err.Error())
	}
	var parsed struct {
		URL        string `xml:"url"`
		Password   string `xml:"password"`
		Connection struct {
			Credential string `xml:"credential,attr"`
		} `xml:"connection"`
	}
	if err := xml.Unmarshal(content, &parsed); err != nil {
		fail("realized BenchBase config is invalid XML: " + err.Error())
	}
	if parsed.URL != "jdbc:postgresql://127.0.0.1/bench" || parsed.Password != benchBaseXMLMetacharPassword || parsed.Connection.Credential != benchBaseXMLMetacharPassword {
		fail("realized BenchBase config did not decode to the expected target and sensitive values")
	}
	if err := os.WriteFile(filepath.Join(arguments[7], "fake.summary.json"), []byte(fakeBenchBaseResult), 0o600); err != nil {
		fail("write fake BenchBase summary: " + err.Error())
	}
	os.Exit(0)
}

func createSysbenchExecution(t *testing.T) string {
	t.Helper()
	fixture := t.TempDir()
	config := filepath.Join(fixture, "config.json")
	runtimeRoot, binary, script := createSysbenchRuntime(t, fixture, []byte(fakeSysbenchExecutable()), []byte("-- fixture\n"))
	writeFile(t, config, []byte(`{
  "schema_version":"pgworkbench.sysbench-native-run-config/v1",
  "artifact_type":"pgworkbench.sysbench-native-run-config",
  "threads":1,"duration_seconds":10,"report_interval_seconds":1,"rate":0,"random_seed":1,
  "postgresql":{"host":"127.0.0.1","port":5432,"user":"postgres","database":"bench"}
}`), 0o644)
	output := filepath.Join(fixture, "execution")
	if _, err := Run(Options{
		Root: repositoryRoot(t), DriverID: "sysbench-postgresql-1.0.20", BinaryPath: binary,
		ConfigPath: config, ScriptPath: script, RuntimeRoot: runtimeRoot, Workload: "oltp_read_write/postgresql",
		OutputDir: output, Timeout: 30 * time.Second, Getenv: func(string) string { return "" },
		AcknowledgeExternalDisposableTarget: true,
	}); err != nil {
		t.Fatal(err)
	}
	return output
}

func fakeSysbenchExecutable() string {
	return `#!/bin/sh
set -eu
case "$LUA_PATH" in
  */inputs/runtime/share/sysbench/\?.lua) ;;
  *) exit 1 ;;
esac
[ "$#" -eq 13 ]
[ "$1" = "--db-driver=pgsql" ]
[ "$2" = "--threads=2" ] || [ "$2" = "--threads=1" ]
[ "$3" = "--time=10" ]
[ "$4" = "--report-interval=1" ]
[ "$5" = "--rate=0" ]
[ "$6" = "--rand-seed=42" ] || [ "$6" = "--rand-seed=1" ]
[ "$7" = "--events=0" ]
[ "$8" = "--pgsql-host=127.0.0.1" ]
[ "$9" = "--pgsql-port=5432" ]
[ "${10}" = "--pgsql-user=postgres" ]
[ "${11}" = "--pgsql-db=bench" ]
[ "${12##*/}" = "oltp_read_write.lua" ]
[ "${13}" = "run" ]
if [ -n "${PGPASSFILE:-}" ]; then
  [ -f "$PGPASSFILE" ]
  IFS= read -r pgpass_line < "$PGPASSFILE"
  [ -n "$pgpass_line" ]
fi
` + "cat <<'PGWORKBENCH_RESULT'\n" + fakeSysbenchResult + "PGWORKBENCH_RESULT\n"
}

func fakeJavaExecutable() string {
	return `#!/bin/sh
set -eu
[ "$#" -eq 11 ]
[ "$1" = "-jar" ]
[ "${2##*/}" = "benchbase.jar" ]
[ "$3" = "-b" ]
[ "$4" = "tpcc" ]
[ "$5" = "-c" ]
[ "${6##*/}" = "config.xml" ]
[ "$7" = "-d" ]
[ "$9" = "--create=false" ]
[ "${10}" = "--load=false" ]
[ "${11}" = "--execute=true" ]
cat > "$8/fake.summary.json" <<'PGWORKBENCH_RESULT'
` + fakeBenchBaseResult + `
PGWORKBENCH_RESULT
`
}

func fakeJavaXMLCheckingExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(executable, "'", `'"'"'`) + "'"
	return "#!/bin/sh\nset -eu\nexport PGWORKBENCH_TEST_BENCHBASE_JAVA_HELPER=1\nexec " + quoted + " -test.run='^TestBenchBaseJavaHelperProcess$' -- \"$@\"\n"
}

func fakeHammerDBExecutable(report, jobID string) string {
	return `#!/bin/sh
set -eu
[ "$#" -eq 2 ]
[ "$1" = "auto" ]
[ "${2##*/}" = "execute.tcl" ]
[ "${PGWORKBENCH_DRIVER_PASSWORD:-}" = "hammer-secret:""123" ]
cat > "$TMP/hdb_` + jobID + `.json" <<'PGWORKBENCH_RESULT'
` + report + `
PGWORKBENCH_RESULT
printf '%s\n' 'HammerDB CLI v6.0'
printf '%s\n' 'PGWORKBENCH_HAMMERDB_JOBID=` + jobID + `'
printf '%s\n' 'PGWORKBENCH_HAMMERDB_REPORT=hdb_` + jobID + `.json'
`
}

func fakeMutatingSysbenchExecutable() string {
	return `#!/bin/sh
chmod u+w "$0"
printf '%s\n' '# mutation' >> "$0"
` + "cat <<'PGWORKBENCH_RESULT'\n" + fakeSysbenchResult + "PGWORKBENCH_RESULT\n"
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func createSysbenchRuntime(t *testing.T, parent string, binary, workload []byte) (string, string, string) {
	t.Helper()
	root := filepath.Join(parent, "sysbench-runtime")
	binaryPath := filepath.Join(root, "bin", "sysbench")
	scriptPath := filepath.Join(root, "share", "sysbench", "oltp_read_write.lua")
	commonPath := filepath.Join(root, "share", "sysbench", "oltp_common.lua")
	for _, directory := range []string{filepath.Dir(binaryPath), filepath.Dir(scriptPath)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, binaryPath, binary, 0o755)
	writeFile(t, scriptPath, workload, 0o644)
	writeFile(t, commonPath, []byte("-- pinned common runtime\n"), 0o644)
	return root, binaryPath, scriptPath
}

func createHammerDBRuntime(t *testing.T, parent string, launcher []byte) (string, string) {
	t.Helper()
	root := filepath.Join(parent, "hammerdb-runtime")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	launcherPath := filepath.Join(root, "hammerdbcli")
	writeFile(t, launcherPath, launcher, 0o755)
	return root, launcherPath
}

func createBenchBaseRuntime(t *testing.T, parent string) (string, string) {
	t.Helper()
	root := filepath.Join(parent, "benchbase-runtime")
	for _, directory := range []string{filepath.Join(root, "lib"), filepath.Join(root, "config")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	entrypoint := filepath.Join(root, "benchbase.jar")
	writeFile(t, entrypoint, testJAR(t, "Manifest-Version: 1.0\r\nClass-Path: lib/dependency.jar\r\n\r\n"), 0o644)
	writeFile(t, filepath.Join(root, "lib", "dependency.jar"), testJAR(t, "Manifest-Version: 1.0\r\nClass-Path: leaf.jar\r\n\r\n"), 0o644)
	writeFile(t, filepath.Join(root, "lib", "leaf.jar"), testJAR(t, "Manifest-Version: 1.0\r\n\r\n"), 0o644)
	writeFile(t, filepath.Join(root, "config", "plugin.xml"), []byte("<plugins/>\n"), 0o644)
	return root, entrypoint
}

func testJAR(t *testing.T, manifest string) []byte {
	t.Helper()
	var result bytes.Buffer
	archive := zip.NewWriter(&result)
	entry, err := archive.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func sequenceClock(start time.Time, step time.Duration) func() time.Time {
	value := start.Add(-step)
	return func() time.Time {
		value = value.Add(step)
		return value
	}
}

func writeFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func canonicalExistingPath(t *testing.T, value string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func rewriteExecutionAndInventory(t *testing.T, root string, artifact Artifact) {
	t.Helper()
	digest, err := artifactDigest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	artifact.Digest = digest
	execution, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ExecutionFile), append(execution, '\n'), 0o644)
	if err := os.Remove(filepath.Join(root, InventoryFile)); err != nil {
		t.Fatal(err)
	}
	inventory, err := buildInventory(root, artifact.Digest)
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, InventoryFile), append(content, '\n'), 0o644)
}

func findBytes(t *testing.T, root string, needle []byte) string {
	t.Helper()
	var found string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if found != "" || entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(content, needle) {
			found = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

func issuesContain(issues []string, expected string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, expected) {
			return true
		}
	}
	return false
}
