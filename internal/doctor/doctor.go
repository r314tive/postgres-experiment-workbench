package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
}

type Deps struct {
	LookupPath func(string) (string, error)
	RunCommand func(string, ...string) (string, error)
	Stat       func(string) (os.FileInfo, error)
}

func Run(root string, options Options, deps Deps) Result {
	deps = withDefaults(deps)
	result := Result{}

	checkFile(&result, deps, filepath.Join(root, "Makefile"), "repo Makefile")
	checkFile(&result, deps, filepath.Join(root, "compose.yaml"), "compose.yaml")

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

	for _, command := range []string{"bash", "make", "docker", "go", "psql", "awk", "sed", "realpath", "rg"} {
		checkCommand(&result, deps, command, true)
	}
	checkCommand(&result, deps, "gh", false)

	checkCommandOutput(&result, deps, "go version", "go", "version")
	checkCommandOutput(&result, deps, "docker version", "docker", "--version")
	checkCommandOutput(&result, deps, "docker compose version", "docker", "compose", "version")
	checkCommandOutput(&result, deps, "docker compose config", "docker", "compose", "--env-file", envPath, "config", "--quiet")
	if options.SkipDockerDaemon {
		add(&result, Warn, "docker daemon", "skipped")
	} else {
		checkCommandOutput(&result, deps, "docker daemon", "docker", "info", "--format", "{{.ServerVersion}}")
	}

	return result
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
