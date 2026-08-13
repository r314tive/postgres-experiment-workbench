package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
)

type Status string

const (
	Pass Status = "PASS"
	Warn Status = "WARN"
	Fail Status = "FAIL"
)

type Check struct {
	Status Status
	Name   string
	Detail string
}

type Result struct {
	Checks []Check
}

func (r Result) Valid() bool {
	for _, check := range r.Checks {
		if check.Status == Fail {
			return false
		}
	}
	return true
}

type Options struct {
	SkipDockerDaemon bool
	Runtime          string
	NativeBindir     string
}

type Deps struct {
	LookupPath func(string) (string, error)
	RunCommand func(string, ...string) (string, error)
	Stat       func(string) (os.FileInfo, error)
}

func Run(root string, options Options, deps Deps) Result {
	deps = withDefaults(deps)
	result := Result{}
	runtime := strings.TrimSpace(options.Runtime)
	if runtime == "" {
		runtime = "docker"
	}
	if runtime != "docker" && runtime != "native" {
		add(&result, Fail, "runtime", fmt.Sprintf("unsupported %q; expected docker or native", runtime))
		return result
	}
	add(&result, Pass, "runtime", runtime)

	checkWorkspace(&result, deps, root)
	checkFile(&result, deps, filepath.Join(root, "scripts", "runtime.sh"), "runtime dispatcher")
	if runtime == "docker" {
		checkFile(&result, deps, filepath.Join(root, "compose.yaml"), "compose.yaml")
	} else {
		checkFile(&result, deps, filepath.Join(root, "scripts", "native_runtime.sh"), "native runtime")
	}

	envPath := filepath.Join(root, ".env")
	envLabel := ".env"
	if _, err := deps.Stat(envPath); err != nil {
		envPath = filepath.Join(root, ".env.example")
		envLabel = ".env.example"
	}

	envValues := map[string]string{}
	if _, err := deps.Stat(envPath); err != nil {
		add(&result, Fail, "env file", "missing .env and .env.example")
	} else {
		parsed, parseErr := envfile.Parse(envPath)
		if parseErr != nil {
			add(&result, Fail, "env file", parseErr.Error())
		} else {
			envValues = parsed
			add(&result, Pass, "env file", envLabel)
		}
	}

	checkLocalTarget(&result, envValues)

	requiredCommands := []string{"bash", "awk", "sed", "realpath"}
	if runtime == "docker" {
		requiredCommands = append(requiredCommands, "docker", "psql")
	} else {
		for _, command := range []string{"initdb", "pg_ctl", "createdb", "pg_isready", "psql"} {
			checkNativeCommand(&result, deps, options.NativeBindir, command)
		}
	}
	for _, command := range requiredCommands {
		checkCommand(&result, deps, command, true)
	}
	checkBashVersion(&result, deps)
	for _, command := range []string{"go", "make", "rg", "gh"} {
		checkCommand(&result, deps, command, false)
	}

	checkOptionalCommandOutput(&result, deps, "go version", "go", "version")
	if runtime == "docker" {
		checkCommandOutput(&result, deps, "docker version", "docker", "--version")
		checkCommandOutput(&result, deps, "docker compose version", "docker", "compose", "version")
		checkCommandOutput(&result, deps, "docker compose config", "docker", "compose", "--env-file", envPath, "config", "--quiet")
		if options.SkipDockerDaemon {
			add(&result, Warn, "docker daemon", "skipped")
		} else {
			checkCommandOutput(&result, deps, "docker daemon", "docker", "info", "--format", "{{.ServerVersion}}")
		}
	}

	return result
}

func checkBashVersion(result *Result, deps Deps) {
	if _, err := deps.LookupPath("bash"); err != nil {
		return
	}
	output, err := deps.RunCommand("bash", "--version")
	line := firstLine(output)
	if err != nil {
		add(result, Fail, "bash version", valueOrDetail(line, err.Error()))
		return
	}
	marker := "version "
	index := strings.Index(line, marker)
	if index < 0 {
		add(result, Fail, "bash version", "could not parse: "+line)
		return
	}
	versionText := strings.Fields(line[index+len(marker):])
	if len(versionText) == 0 {
		add(result, Fail, "bash version", "could not parse: "+line)
		return
	}
	majorText, _, _ := strings.Cut(strings.TrimSuffix(versionText[0], "("), ".")
	major, parseErr := strconv.Atoi(majorText)
	if parseErr != nil {
		add(result, Fail, "bash version", "could not parse: "+line)
		return
	}
	if major < 4 {
		add(result, Fail, "bash version", fmt.Sprintf("%s; Bash 4 or newer is required", line))
		return
	}
	add(result, Pass, "bash version", line)
}

func valueOrDetail(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func checkNativeCommand(result *Result, deps Deps, bindir string, command string) {
	if strings.TrimSpace(bindir) == "" {
		checkCommand(result, deps, command, true)
		return
	}
	path := filepath.Join(bindir, command)
	info, err := deps.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		add(result, Fail, "command "+command, "not executable: "+path)
		return
	}
	add(result, Pass, "command "+command, path)
}

func checkWorkspace(result *Result, deps Deps, root string) {
	for _, marker := range []string{"pgworkbench-pack.json", "Makefile"} {
		info, err := deps.Stat(filepath.Join(root, marker))
		if err == nil && info.Mode().IsRegular() {
			add(result, Pass, "workspace", marker)
			return
		}
	}
	add(result, Fail, "workspace", "missing pgworkbench-pack.json or Makefile")
}

func Render(w io.Writer, result Result) error {
	passCount := 0
	warnCount := 0
	failCount := 0

	if _, err := fmt.Fprintln(w, "# Workbench Doctor"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, check := range result.Checks {
		switch check.Status {
		case Pass:
			passCount++
		case Warn:
			warnCount++
		case Fail:
			failCount++
		}
		if _, err := fmt.Fprintf(w, "%s %-24s %s\n", check.Status, check.Name, check.Detail); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "summary: pass=%d warn=%d fail=%d\n", passCount, warnCount, failCount)
	return err
}

func withDefaults(deps Deps) Deps {
	if deps.LookupPath == nil {
		deps.LookupPath = exec.LookPath
	}
	if deps.RunCommand == nil {
		deps.RunCommand = runCommand
	}
	if deps.Stat == nil {
		deps.Stat = os.Stat
	}
	return deps
}

func runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("%s timed out", name)
	}
	return string(output), err
}

func add(result *Result, status Status, name string, detail string) {
	result.Checks = append(result.Checks, Check{
		Status: status,
		Name:   name,
		Detail: strings.TrimSpace(detail),
	})
}

func checkFile(result *Result, deps Deps, path string, name string) {
	info, err := deps.Stat(path)
	if err != nil {
		add(result, Fail, name, "missing")
		return
	}
	if !info.Mode().IsRegular() {
		add(result, Fail, name, "not a regular file")
		return
	}
	add(result, Pass, name, filepath.Base(path))
}

func checkCommand(result *Result, deps Deps, command string, required bool) {
	path, err := deps.LookupPath(command)
	if err != nil {
		if required {
			add(result, Fail, "command "+command, "not found")
		} else {
			add(result, Warn, "command "+command, "not found")
		}
		return
	}
	add(result, Pass, "command "+command, path)
}

func checkCommandOutput(result *Result, deps Deps, name string, command string, args ...string) {
	output, err := deps.RunCommand(command, args...)
	output = firstLine(output)
	if err != nil {
		if output == "" {
			output = err.Error()
		} else {
			output = output + "; " + err.Error()
		}
		add(result, Fail, name, output)
		return
	}
	if output == "" {
		output = "ok"
	}
	add(result, Pass, name, output)
}

func checkOptionalCommandOutput(result *Result, deps Deps, name string, command string, args ...string) {
	if _, err := deps.LookupPath(command); err != nil {
		return
	}
	checkCommandOutput(result, deps, name, command, args...)
}

func firstLine(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	line, _, _ := strings.Cut(output, "\n")
	return strings.TrimSpace(line)
}

func checkLocalTarget(result *Result, values map[string]string) {
	host := value(values, "POSTGRES_HOST", "127.0.0.1")
	db := value(values, "POSTGRES_DB", "pg_experiment_workbench")
	allowNonlocal := value(values, "ALLOW_NONLOCAL_PG", "0")
	allowSystemDB := value(values, "ALLOW_SYSTEM_DB", "0")

	if allowNonlocal == "1" {
		add(result, Warn, "local target guard", "ALLOW_NONLOCAL_PG=1")
	} else if !isLocalHost(host) {
		add(result, Fail, "local target guard", "POSTGRES_HOST="+host)
	} else {
		add(result, Pass, "local target guard", "POSTGRES_HOST="+host)
	}

	if allowSystemDB == "1" {
		add(result, Warn, "system db guard", "ALLOW_SYSTEM_DB=1")
	} else if db == "postgres" || db == "template0" || db == "template1" {
		add(result, Fail, "system db guard", "POSTGRES_DB="+db)
	} else {
		add(result, Pass, "system db guard", "POSTGRES_DB="+db)
	}
}

func value(values map[string]string, key string, fallback string) string {
	if values == nil {
		return fallback
	}
	value := strings.TrimSpace(values[key])
	if value == "" {
		return fallback
	}
	return value
}

func isLocalHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
