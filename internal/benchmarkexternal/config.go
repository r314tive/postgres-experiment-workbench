package benchmarkexternal

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkimport"
)

const passwordPlaceholder = "{{PGWORKBENCH_DRIVER_PASSWORD}}"

var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@/+:-]{0,254}$`)
var safeDatabase = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}$`)

type targetIdentity struct {
	host     string
	port     uint16
	database string
}

type preparedInvocation struct {
	configPath   string
	scriptPath   string
	resultPath   string
	generatedTcl []byte
	target       targetIdentity
	argv         []string
	recordedArgv []string
	secretNames  []string
	secretValue  string
	cleanup      func() error
}

func prepareInvocation(stage string, driver benchmarkdrivers.Driver, workload string, config, script []byte, password string) (preparedInvocation, error) {
	runtime := DriverRuntime{}
	switch driver.Adapter {
	case benchmarkimport.AdapterBenchBase:
		runtime.Entrypoint = DriverRuntimeDir + "/benchbase.jar"
	case benchmarkimport.AdapterHammerDB6:
		runtime.Entrypoint = DriverRuntimeDir + "/hammerdbcli"
	case benchmarkimport.AdapterSysbench1:
		runtime.Entrypoint = DriverRuntimeDir + "/bin/sysbench"
	}
	return prepareInvocationWithRuntime(stage, driver, workload, config, script, password, runtime)
}

func prepareInvocationWithRuntime(stage string, driver benchmarkdrivers.Driver, workload string, config, script []byte, password string, runtime DriverRuntime) (preparedInvocation, error) {
	if !slices.Contains(driver.Workloads, workload) {
		return preparedInvocation{}, fmt.Errorf("workload %q is not pinned for driver %q", workload, driver.ID)
	}
	switch driver.Adapter {
	case benchmarkimport.AdapterSysbench1:
		return prepareSysbenchInvocation(stage, driver, workload, config, script, password, runtime)
	case benchmarkimport.AdapterBenchBase:
		return prepareBenchBaseInvocation(stage, driver, workload, config, script, password, runtime)
	case benchmarkimport.AdapterHammerDB6:
		return prepareHammerDBInvocation(stage, driver, workload, config, script, password, runtime)
	default:
		return preparedInvocation{}, fmt.Errorf("unsupported external benchmark adapter %q", driver.Adapter)
	}
}

func prepareHammerDBInvocation(stage string, driver benchmarkdrivers.Driver, workload string, config, template []byte, password string, runtime DriverRuntime) (preparedInvocation, error) {
	if string(template) != HammerDBTemplate {
		return preparedInvocation{}, fmt.Errorf("HammerDB script input must be the exact reviewed execute-only template marker %q, not caller-supplied Tcl", strings.TrimSpace(HammerDBTemplate))
	}
	var parsed HammerDBConfig
	if err := decodeClosedJSON(config, &parsed, "HammerDB v6 native run config"); err != nil {
		return preparedInvocation{}, err
	}
	if err := validateHammerDBConfig(parsed, workload); err != nil {
		return preparedInvocation{}, err
	}
	generated, err := renderHammerDBTcl(parsed, workload)
	if err != nil {
		return preparedInvocation{}, err
	}
	configPath := filepath.Join(stage, "inputs", "config.json")
	templatePath := filepath.Join(stage, "inputs", "adapter-template.txt")
	workDir := filepath.Join(stage, ".driver-work")
	generatedPath := filepath.Join(workDir, "execute.tcl")
	resultPath := filepath.Join(stage, "raw", "driver-result.json")
	secretNames := []string{}
	if password != "" {
		secretNames = []string{SecretPasswordEnv}
	}
	return preparedInvocation{
		configPath: configPath, scriptPath: templatePath, resultPath: resultPath,
		generatedTcl: generated, target: targetIdentity{host: parsed.PostgreSQL.Host, port: parsed.PostgreSQL.Port, database: parsed.PostgreSQL.Database},
		argv:         []string{"auto", generatedPath},
		recordedArgv: []string{runtime.Entrypoint, "auto", "<ephemeral-adapter-generated-tcl-from:inputs/adapter-template.txt>"},
		secretNames:  secretNames, secretValue: password,
		cleanup: func() error { return nil },
	}, nil
}

func validateHammerDBConfig(config HammerDBConfig, workload string) error {
	if config.SchemaVersion != HammerDBConfigSchema || config.ArtifactType != HammerDBConfigArtifact || config.Mode != HammerDBExecutionMode {
		return fmt.Errorf("HammerDB config must use schema %q, artifact type %q, and mode %q", HammerDBConfigSchema, HammerDBConfigArtifact, HammerDBExecutionMode)
	}
	if config.PostgreSQL.Port == 0 {
		return fmt.Errorf("HammerDB PostgreSQL port must be non-zero")
	}
	if err := validateExternalTarget(config.PostgreSQL.Host, config.PostgreSQL.Port, config.PostgreSQL.Database); err != nil {
		return fmt.Errorf("HammerDB PostgreSQL target: %w", err)
	}
	for label, value := range map[string]string{
		"host": config.PostgreSQL.Host, "user": config.PostgreSQL.User, "database": config.PostgreSQL.Database,
	} {
		if !safeIdentifier.MatchString(value) || strings.Contains(value, "..") {
			return fmt.Errorf("HammerDB PostgreSQL %s is not a safe portable token", label)
		}
	}
	if !slices.Contains([]string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}, config.PostgreSQL.SSLMode) {
		return fmt.Errorf("HammerDB PostgreSQL sslmode is unsupported")
	}
	switch workload {
	case "tprocc/postgresql":
		if config.TPROCC == nil || config.TPROCH != nil {
			return fmt.Errorf("HammerDB TPROC-C config must contain tprocc and omit tproch")
		}
		value := config.TPROCC
		if value.Warehouses == 0 || value.Warehouses > 1_000_000 {
			return fmt.Errorf("HammerDB TPROC-C warehouses must be between 1 and 1000000")
		}
		if value.VirtualUsers == 0 || value.VirtualUsers > 100_000 {
			return fmt.Errorf("HammerDB TPROC-C virtual_users must be between 1 and 100000")
		}
		if value.RampupMinutes > 1440 || value.DurationMinutes == 0 || value.DurationMinutes > 1440 {
			return fmt.Errorf("HammerDB TPROC-C rampup_minutes/duration_minutes are outside the supported 0..1440/1..1440 ranges")
		}
		if value.TotalIterations == 0 || value.TotalIterations > 1_000_000_000_000 {
			return fmt.Errorf("HammerDB TPROC-C total_iterations must be between 1 and 1000000000000")
		}
	case "tproch/postgresql":
		if config.TPROCH == nil || config.TPROCC != nil {
			return fmt.Errorf("HammerDB TPROC-H config must contain tproch and omit tprocc")
		}
		value := config.TPROCH
		if value.ScaleFactor == 0 || value.ScaleFactor > 1_000_000 {
			return fmt.Errorf("HammerDB TPROC-H scale_factor must be between 1 and 1000000")
		}
		if value.VirtualUsers == 0 || value.VirtualUsers > 100_000 {
			return fmt.Errorf("HammerDB TPROC-H virtual_users must be between 1 and 100000")
		}
		if value.QuerySets == 0 || value.QuerySets > 100_000 {
			return fmt.Errorf("HammerDB TPROC-H query_sets must be between 1 and 100000")
		}
		if value.DegreeOfParallelism == 0 || value.DegreeOfParallelism > 100_000 {
			return fmt.Errorf("HammerDB TPROC-H degree_of_parallelism must be between 1 and 100000")
		}
	default:
		return fmt.Errorf("HammerDB workload %q has no fixed PostgreSQL Tcl adapter", workload)
	}
	return nil
}

func renderHammerDBTcl(config HammerDBConfig, workload string) ([]byte, error) {
	var builder strings.Builder
	write := func(format string, values ...any) { fmt.Fprintf(&builder, format, values...) }
	write("# Generated by pgworkbench; execute-only against a prepared schema.\n")
	write("dbset db pg\n")
	benchmark := "TPC-C"
	if workload == "tproch/postgresql" {
		benchmark = "TPC-H"
	}
	write("dbset bm %s\n", benchmark)
	write("diset connection pg_host {%s}\n", config.PostgreSQL.Host)
	write("diset connection pg_port %d\n", config.PostgreSQL.Port)
	write("diset connection pg_sslmode {%s}\n", config.PostgreSQL.SSLMode)
	write("if {[info exists ::env(%s)]} { set pgw_password $::env(%s) } else { set pgw_password {} }\n", SecretPasswordEnv, SecretPasswordEnv)
	if workload == "tprocc/postgresql" {
		value := config.TPROCC
		write("diset tpcc pg_user {%s}\n", config.PostgreSQL.User)
		write("dict set configpostgresql tpcc pg_pass $pgw_password\n")
		write("diset tpcc pg_dbase {%s}\n", config.PostgreSQL.Database)
		write("diset tpcc pg_count_ware %d\n", value.Warehouses)
		write("diset tpcc pg_driver timed\n")
		write("diset tpcc pg_total_iterations %d\n", value.TotalIterations)
		write("diset tpcc pg_rampup %d\n", value.RampupMinutes)
		write("diset tpcc pg_duration %d\n", value.DurationMinutes)
		write("diset tpcc pg_vacuum false\n")
		write("diset tpcc pg_timeprofile false\n")
		write("diset tpcc pg_allwarehouse true\n")
		write("vuset vu %d\n", value.VirtualUsers)
	} else {
		value := config.TPROCH
		write("diset tpch pg_tpch_user {%s}\n", config.PostgreSQL.User)
		write("dict set configpostgresql tpch pg_tpch_pass $pgw_password\n")
		write("diset tpch pg_tpch_dbase {%s}\n", config.PostgreSQL.Database)
		write("diset tpch pg_scale_fact %d\n", value.ScaleFactor)
		write("diset tpch pg_total_querysets %d\n", value.QuerySets)
		write("diset tpch pg_degree_of_parallel %d\n", value.DegreeOfParallelism)
		write("diset tpch pg_raise_query_error true\n")
		write("diset tpch pg_verbose false\n")
		write("diset tpch pg_refresh_on false\n")
		write("vuset vu %d\n", value.VirtualUsers)
	}
	write("unset -nocomplain ::env(%s)\n", SecretPasswordEnv)
	write("unset pgw_password\n")
	write("loadscript\n")
	write("vucreate\n")
	write("set pgw_run_result [vurun]\n")
	write("vudestroy\n")
	write("if {![regexp {^Benchmark Run jobid=([0-9A-F]{24})$} $pgw_run_result -> pgw_jobid]} { puts stderr {PGWORKBENCH_HAMMERDB_ERROR invalid vurun job id}; exit 1 }\n")
	write("puts \"PGWORKBENCH_HAMMERDB_JOBID=$pgw_jobid\"\n")
	write("jobs $pgw_jobid save\n")
	write("set pgw_report [file join [findtempdir] \"hdb_${pgw_jobid}.json\"]\n")
	write("if {![file isfile $pgw_report]} { puts stderr {PGWORKBENCH_HAMMERDB_ERROR missing saved report}; exit 1 }\n")
	write("puts \"PGWORKBENCH_HAMMERDB_REPORT=hdb_${pgw_jobid}.json\"\n")
	write("exit\n")
	return []byte(builder.String()), nil
}

func prepareSysbenchInvocation(stage string, driver benchmarkdrivers.Driver, workload string, config, script []byte, password string, runtime DriverRuntime) (preparedInvocation, error) {
	var parsed SysbenchConfig
	if err := decodeClosedJSON(config, &parsed, "sysbench native run config"); err != nil {
		return preparedInvocation{}, err
	}
	if err := validateSysbenchConfig(parsed); err != nil {
		return preparedInvocation{}, err
	}
	expectedScript, err := sysbenchScriptName(workload)
	if err != nil {
		return preparedInvocation{}, err
	}
	if len(script) == 0 || bytes.IndexByte(script, 0) >= 0 || !utf8.Valid(script) {
		return preparedInvocation{}, fmt.Errorf("sysbench Lua script must be non-empty UTF-8 text without NUL bytes")
	}
	configPath := filepath.Join(stage, "inputs", "config.json")
	scriptRef := filepath.ToSlash(filepath.Join(DriverRuntimeDir, "share", "sysbench", expectedScript))
	scriptPath := filepath.Join(stage, filepath.FromSlash(scriptRef))
	resultPath := filepath.Join(stage, "raw", "driver-result.txt")
	args := []string{
		"--db-driver=pgsql",
		"--threads=" + strconv.FormatUint(uint64(parsed.Threads), 10),
		"--time=" + strconv.FormatUint(uint64(parsed.DurationSeconds), 10),
		"--report-interval=" + strconv.FormatUint(uint64(parsed.ReportIntervalSeconds), 10),
		"--rate=" + strconv.FormatUint(uint64(parsed.Rate), 10),
		"--rand-seed=" + strconv.FormatUint(parsed.RandomSeed, 10),
		"--events=0",
		"--pgsql-host=" + parsed.PostgreSQL.Host,
		"--pgsql-port=" + strconv.FormatUint(uint64(parsed.PostgreSQL.Port), 10),
		"--pgsql-user=" + parsed.PostgreSQL.User,
		"--pgsql-db=" + parsed.PostgreSQL.Database,
	}
	recorded := append([]string{runtime.Entrypoint}, args...)
	secretNames := []string{}
	if password != "" {
		secretNames = []string{SecretPasswordEnv}
	}
	args = append(args, scriptPath, "run")
	recorded = append(recorded, scriptRef, "run")
	return preparedInvocation{
		configPath: configPath, scriptPath: scriptPath, resultPath: resultPath,
		target: targetIdentity{host: parsed.PostgreSQL.Host, port: parsed.PostgreSQL.Port, database: parsed.PostgreSQL.Database},
		argv:   args, recordedArgv: recorded, secretNames: secretNames, secretValue: password,
		cleanup: func() error { return nil },
	}, nil
}

func validateSysbenchConfig(config SysbenchConfig) error {
	if config.SchemaVersion != SysbenchConfigSchema || config.ArtifactType != SysbenchConfigArtifact {
		return fmt.Errorf("sysbench config must use schema %q and artifact type %q", SysbenchConfigSchema, SysbenchConfigArtifact)
	}
	if config.Threads == 0 || config.Threads > 100000 {
		return fmt.Errorf("sysbench threads must be between 1 and 100000")
	}
	if config.DurationSeconds == 0 || config.DurationSeconds > 86400 {
		return fmt.Errorf("sysbench duration_seconds must be between 1 and 86400")
	}
	if config.ReportIntervalSeconds == 0 || config.ReportIntervalSeconds > config.DurationSeconds {
		return fmt.Errorf("sysbench report_interval_seconds must be between 1 and duration_seconds")
	}
	if config.RandomSeed == 0 {
		return fmt.Errorf("sysbench random_seed must be non-zero")
	}
	if config.PostgreSQL.Port == 0 {
		return fmt.Errorf("sysbench PostgreSQL port must be non-zero")
	}
	if err := validateExternalTarget(config.PostgreSQL.Host, config.PostgreSQL.Port, config.PostgreSQL.Database); err != nil {
		return fmt.Errorf("sysbench PostgreSQL target: %w", err)
	}
	for label, value := range map[string]string{
		"host": config.PostgreSQL.Host, "user": config.PostgreSQL.User, "database": config.PostgreSQL.Database,
	} {
		if !safeIdentifier.MatchString(value) || strings.Contains(value, "..") {
			return fmt.Errorf("sysbench PostgreSQL %s is not a safe portable token", label)
		}
	}
	return nil
}

func prepareBenchBaseInvocation(stage string, driver benchmarkdrivers.Driver, workload string, config, script []byte, password string, runtime DriverRuntime) (preparedInvocation, error) {
	if len(script) < 4 || !bytes.Equal(script[:4], []byte("PK\x03\x04")) {
		return preparedInvocation{}, fmt.Errorf("BenchBase driver script must be a non-empty JAR/ZIP file")
	}
	usesPassword, err := validateBenchBaseTemplate(config)
	if err != nil {
		return preparedInvocation{}, err
	}
	target, err := extractBenchBaseTarget(config)
	if err != nil {
		return preparedInvocation{}, err
	}
	if usesPassword && password == "" {
		return preparedInvocation{}, fmt.Errorf("BenchBase config uses %s but %s is empty", passwordPlaceholder, SecretPasswordEnv)
	}
	if !usesPassword && password != "" {
		return preparedInvocation{}, fmt.Errorf("%s is set but the BenchBase config has no password placeholder", SecretPasswordEnv)
	}
	configPath := filepath.Join(stage, "inputs", "config.xml")
	scriptPath := filepath.Join(stage, filepath.FromSlash(runtime.Entrypoint))
	workDir := filepath.Join(stage, ".driver-work")
	realizedConfig := filepath.Join(workDir, "config.xml")
	resultPath := filepath.Join(stage, "raw", "driver-result.json")
	outputDir := filepath.Join(workDir, "results")
	args := []string{"-jar", scriptPath, "-b", workload, "-c", realizedConfig, "-d", outputDir, "--create=false", "--load=false", "--execute=true"}
	recorded := []string{BinaryFile, "-jar", runtime.Entrypoint, "-b", workload, "-c", "<ephemeral-config-from:inputs/config.xml>", "-d", "<ephemeral-output-dir>", "--create=false", "--load=false", "--execute=true"}
	secretNames := []string{}
	if usesPassword {
		secretNames = []string{SecretPasswordEnv}
	}
	return preparedInvocation{
		configPath: configPath, scriptPath: scriptPath, resultPath: resultPath,
		target: target,
		argv:   args, recordedArgv: recorded, secretNames: secretNames, secretValue: password,
		cleanup: func() error { return nil },
	}, nil
}

func validateExternalTarget(host string, port uint16, database string) error {
	if !slices.Contains([]string{"127.0.0.1", "::1"}, host) {
		return fmt.Errorf("host must be exactly numeric loopback 127.0.0.1 or ::1; hostnames and remote targets are not supported")
	}
	if port == 0 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if !safeDatabase.MatchString(database) {
		return fmt.Errorf("database must be a simple portable PostgreSQL identifier")
	}
	switch strings.ToLower(database) {
	case "postgres", "template0", "template1":
		return fmt.Errorf("database %q is a protected PostgreSQL system database", database)
	}
	return nil
}

func extractBenchBaseTarget(content []byte) (targetIdentity, error) {
	if bytes.Count(content, []byte("<url>")) != 1 || bytes.Count(content, []byte("</url>")) != 1 {
		return targetIdentity{}, fmt.Errorf("BenchBase config must contain exactly one plain url element without attributes or namespaces")
	}
	rawStart := bytes.Index(content, []byte("<url>")) + len("<url>")
	rawEndOffset := bytes.Index(content[rawStart:], []byte("</url>"))
	if rawEndOffset < 0 {
		return targetIdentity{}, fmt.Errorf("BenchBase config url element is incomplete")
	}
	rawElementValue := string(content[rawStart : rawStart+rawEndOffset])
	if strings.ContainsAny(rawElementValue, "&<>") {
		return targetIdentity{}, fmt.Errorf("BenchBase JDBC target must be literal unescaped text")
	}
	decoder := xml.NewDecoder(bytes.NewReader(content))
	insideURL := false
	urlCount := 0
	var value strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return targetIdentity{}, fmt.Errorf("parse BenchBase config target XML: %w", err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if insideURL {
				return targetIdentity{}, fmt.Errorf("BenchBase JDBC url element must contain text only")
			}
			if typed.Name.Local == "url" {
				if len(typed.Attr) != 0 {
					return targetIdentity{}, fmt.Errorf("BenchBase JDBC url element must not have attributes")
				}
				urlCount++
				insideURL = true
			}
		case xml.CharData:
			if insideURL {
				value.Write([]byte(typed))
			}
		case xml.EndElement:
			if typed.Name.Local == "url" && insideURL {
				insideURL = false
			}
		}
	}
	if insideURL || urlCount != 1 {
		return targetIdentity{}, fmt.Errorf("BenchBase config must contain exactly one text-only url element")
	}
	raw := strings.TrimSpace(value.String())
	if raw != value.String() || raw != rawElementValue || !strings.HasPrefix(raw, "jdbc:postgresql://") || strings.Contains(raw, "%") {
		return targetIdentity{}, fmt.Errorf("BenchBase JDBC target must be an unescaped jdbc:postgresql:// loopback URL")
	}
	parsed, err := url.Parse(strings.TrimPrefix(raw, "jdbc:"))
	if err != nil || parsed.Scheme != "postgresql" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.RawPath != "" {
		return targetIdentity{}, fmt.Errorf("BenchBase JDBC target must not contain userinfo, parameters, fragments, or opaque content")
	}
	host := parsed.Hostname()
	if host == "" || parsed.Path == "" || strings.Count(parsed.Path, "/") != 1 {
		return targetIdentity{}, fmt.Errorf("BenchBase JDBC target must contain exactly one host and database")
	}
	if host == "::1" && !strings.HasPrefix(parsed.Host, "[::1]") {
		return targetIdentity{}, fmt.Errorf("BenchBase JDBC IPv6 loopback must use canonical brackets")
	}
	port := uint64(5432)
	if parsed.Port() != "" {
		port, err = strconv.ParseUint(parsed.Port(), 10, 16)
		if err != nil || port == 0 {
			return targetIdentity{}, fmt.Errorf("BenchBase JDBC target port is invalid")
		}
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if err := validateExternalTarget(host, uint16(port), database); err != nil {
		return targetIdentity{}, fmt.Errorf("BenchBase PostgreSQL target: %w", err)
	}
	return targetIdentity{host: host, port: uint16(port), database: database}, nil
}

func validateBenchBaseTemplate(content []byte) (bool, error) {
	if len(content) == 0 || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return false, fmt.Errorf("BenchBase config template must be non-empty UTF-8 XML without NUL bytes")
	}
	locations, err := inspectBenchBaseSensitiveValues(content, passwordPlaceholder)
	if err != nil {
		return false, err
	}
	return locations > 0, nil
}

type benchBaseSensitiveFrame struct {
	sensitive  bool
	structured bool
	text       strings.Builder
}

func inspectBenchBaseSensitiveValues(content []byte, allowedSensitiveValue string) (int, error) {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	frames := []benchBaseSensitiveFrame{}
	locations := 0
	rootCount := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("parse BenchBase config XML: %w", err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if len(frames) == 0 {
				rootCount++
				if rootCount != 1 {
					return 0, fmt.Errorf("BenchBase config must contain exactly one root element")
				}
			} else if frames[len(frames)-1].sensitive {
				frames[len(frames)-1].structured = true
			}
			for _, attribute := range typed.Attr {
				if sensitiveName(attribute.Name.Local) {
					switch attribute.Value {
					case "":
					case allowedSensitiveValue:
						locations++
					default:
						return 0, fmt.Errorf("BenchBase config contains a retained or mixed secret-like attribute; use %s as its entire value", passwordPlaceholder)
					}
				} else if strings.Contains(attribute.Value, passwordPlaceholder) {
					return 0, fmt.Errorf("BenchBase password placeholder is allowed only as the entire value of a sensitive attribute or element")
				}
			}
			frames = append(frames, benchBaseSensitiveFrame{sensitive: sensitiveName(typed.Name.Local)})
		case xml.CharData:
			if len(frames) == 0 {
				if strings.TrimSpace(string(typed)) != "" {
					return 0, fmt.Errorf("BenchBase config contains text outside its root element")
				}
				continue
			}
			frames[len(frames)-1].text.Write([]byte(typed))
		case xml.Comment:
			if bytes.Contains([]byte(typed), []byte(passwordPlaceholder)) {
				return 0, fmt.Errorf("BenchBase password placeholder must not occur in comments")
			}
			if len(frames) != 0 && frames[len(frames)-1].sensitive {
				frames[len(frames)-1].structured = true
			}
		case xml.Directive:
			if bytes.Contains([]byte(typed), []byte(passwordPlaceholder)) {
				return 0, fmt.Errorf("BenchBase password placeholder must not occur in directives")
			}
			if len(frames) != 0 && frames[len(frames)-1].sensitive {
				frames[len(frames)-1].structured = true
			}
		case xml.ProcInst:
			if strings.Contains(typed.Target, passwordPlaceholder) || bytes.Contains(typed.Inst, []byte(passwordPlaceholder)) {
				return 0, fmt.Errorf("BenchBase password placeholder must not occur in processing instructions")
			}
			if len(frames) != 0 && frames[len(frames)-1].sensitive {
				frames[len(frames)-1].structured = true
			}
		case xml.EndElement:
			if len(frames) == 0 {
				return 0, fmt.Errorf("BenchBase config contains an unmatched closing element")
			}
			frame := frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			value := frame.text.String()
			if frame.sensitive {
				if frame.structured {
					return 0, fmt.Errorf("BenchBase sensitive element must contain only an empty value or %s as its entire text value", passwordPlaceholder)
				}
				switch value {
				case "":
				case allowedSensitiveValue:
					locations++
				default:
					return 0, fmt.Errorf("BenchBase config contains a retained or mixed secret-like element value; use %s as its entire value", passwordPlaceholder)
				}
			} else if strings.Contains(value, passwordPlaceholder) {
				insideSensitiveAncestor := false
				for _, ancestor := range frames {
					insideSensitiveAncestor = insideSensitiveAncestor || ancestor.sensitive
				}
				if !insideSensitiveAncestor {
					return 0, fmt.Errorf("BenchBase password placeholder is allowed only as the entire value of a sensitive attribute or element")
				}
			}
		}
	}
	if len(frames) != 0 || rootCount != 1 {
		return 0, fmt.Errorf("BenchBase config must contain exactly one complete root element")
	}
	if locations > 16 {
		return 0, fmt.Errorf("BenchBase config contains too many password placeholders")
	}
	return locations, nil
}

func realizeBenchBaseConfig(template []byte, password string) ([]byte, error) {
	expectedLocations, err := inspectBenchBaseSensitiveValues(template, passwordPlaceholder)
	if err != nil {
		return nil, err
	}
	if expectedLocations == 0 {
		if password != "" {
			return nil, fmt.Errorf("%s is set but the BenchBase config has no password placeholder", SecretPasswordEnv)
		}
	} else if password == "" {
		return nil, fmt.Errorf("BenchBase config uses %s but %s is empty", passwordPlaceholder, SecretPasswordEnv)
	}
	if strings.Contains(password, passwordPlaceholder) {
		return nil, fmt.Errorf("%s must not contain the reserved BenchBase password placeholder", SecretPasswordEnv)
	}

	expectedTarget, err := extractBenchBaseTarget(template)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(template))
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	type realizationFrame struct {
		sensitive bool
		replaced  bool
	}
	frames := []realizationFrame{}
	replacements := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse BenchBase config template XML for realization: %w", err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			for index := range typed.Attr {
				if sensitiveName(typed.Attr[index].Name.Local) && typed.Attr[index].Value == passwordPlaceholder {
					typed.Attr[index].Value = password
					replacements++
				}
			}
			if err := encoder.EncodeToken(typed); err != nil {
				return nil, fmt.Errorf("encode realized BenchBase config: %w", err)
			}
			frames = append(frames, realizationFrame{sensitive: sensitiveName(typed.Name.Local)})
		case xml.CharData:
			if len(frames) != 0 && frames[len(frames)-1].sensitive {
				frame := &frames[len(frames)-1]
				if !frame.replaced {
					if err := encoder.EncodeToken(xml.CharData([]byte(password))); err != nil {
						return nil, fmt.Errorf("encode realized BenchBase sensitive value: %w", err)
					}
					frame.replaced = true
					replacements++
				}
				continue
			}
			if err := encoder.EncodeToken(typed); err != nil {
				return nil, fmt.Errorf("encode realized BenchBase config: %w", err)
			}
		case xml.EndElement:
			if len(frames) == 0 {
				return nil, fmt.Errorf("realize BenchBase config: unmatched closing element")
			}
			frames = frames[:len(frames)-1]
			if err := encoder.EncodeToken(typed); err != nil {
				return nil, fmt.Errorf("encode realized BenchBase config: %w", err)
			}
		default:
			if err := encoder.EncodeToken(token); err != nil {
				return nil, fmt.Errorf("encode realized BenchBase config: %w", err)
			}
		}
	}
	if err := encoder.Flush(); err != nil {
		return nil, fmt.Errorf("flush realized BenchBase config: %w", err)
	}
	if replacements != expectedLocations {
		return nil, fmt.Errorf("realized BenchBase config replaced %d sensitive values, expected %d", replacements, expectedLocations)
	}
	realized := output.Bytes()
	realizedLocations, err := inspectBenchBaseSensitiveValues(realized, password)
	if err != nil {
		return nil, fmt.Errorf("validate realized BenchBase config: %w", err)
	}
	if realizedLocations != expectedLocations {
		return nil, fmt.Errorf("realized BenchBase config contains %d sensitive values, expected %d", realizedLocations, expectedLocations)
	}
	realizedTarget, err := extractBenchBaseTarget(realized)
	if err != nil {
		return nil, fmt.Errorf("validate realized BenchBase target: %w", err)
	}
	if realizedTarget != expectedTarget {
		return nil, fmt.Errorf("realized BenchBase config changed its PostgreSQL target")
	}
	return append([]byte(nil), realized...), nil
}

func sensitiveName(value string) bool {
	value = strings.Map(func(char rune) rune {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			return unicode.ToLower(char)
		}
		return -1
	}, value)
	for _, marker := range []string{"password", "passwd", "secret", "token", "credential", "privatekey"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
