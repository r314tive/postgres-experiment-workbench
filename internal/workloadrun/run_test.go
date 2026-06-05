package workloadrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestRunBuildsStructuredResult(t *testing.T) {
	root := t.TempDir()
	writeWorkload(t, root, "workloads/shell/hello.env", strings.Join([]string{
		`WORKLOAD_NAME="hello workload"`,
		`WORKLOAD_KIND="shell"`,
		`WORKLOAD_REQUIRES_POSTGRES=0`,
		`WORKLOAD_CMD='printf hello'`,
		"",
	}, "\n"))

	var seenCommand []string
	var seenEnv []string
	times := fixedTimes(
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		time.Date(2026, 1, 2, 3, 4, 6, 250_000_000, time.UTC),
	)
	result, err := Run(root, speccatalog.New(root), "shell/hello", Options{
		AdapterArgs: []string{"--adapter-arg"},
		Now:         times,
		RunCommand: func(root string, command []string, env []string, stdout, stderr io.Writer) CommandResult {
			seenCommand = append([]string(nil), command...)
			seenEnv = append([]string(nil), env...)
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != "passed" || !result.Passed() || result.ExitCode != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.WorkloadSpec != "shell/hello" || result.WorkloadKind != "shell" || result.RequiresPostgres {
		t.Fatalf("unexpected workload metadata: %#v", result)
	}
	if result.DurationMS != 1250 {
		t.Fatalf("unexpected duration: %d", result.DurationMS)
	}
	wantCommand := []string{filepath.Join(root, "scripts", "run_workload.sh"), "run", filepath.Join(root, "workloads", "shell", "hello.env"), "--adapter-arg"}
	if strings.Join(seenCommand, "\x00") != strings.Join(wantCommand, "\x00") {
		t.Fatalf("unexpected command:\nwant %#v\n got %#v", wantCommand, seenCommand)
	}
	if len(seenEnv) != 1 || !strings.HasPrefix(seenEnv[0], "WORKLOAD_LOG_FILE="+filepath.Join(root, "logs", "workloads", "shell_hello.20260102_030405.log")) {
		t.Fatalf("unexpected env: %#v", seenEnv)
	}
	if result.LogFile == "" || !strings.Contains(result.LogFile, "shell_hello.20260102_030405.log") {
		t.Fatalf("unexpected log file: %s", result.LogFile)
	}
}

func TestRunRecordsFailure(t *testing.T) {
	root := t.TempDir()
	writeWorkload(t, root, "workloads/shell/fail.env", strings.Join([]string{
		`WORKLOAD_NAME="fail workload"`,
		`WORKLOAD_KIND="shell"`,
		`WORKLOAD_REQUIRES_POSTGRES=0`,
		`WORKLOAD_CMD='exit 7'`,
		"",
	}, "\n"))

	result, err := Run(root, speccatalog.New(root), "shell/fail", Options{
		Now: fixedTimes(
			time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		),
		RunCommand: func(root string, command []string, env []string, stdout, stderr io.Writer) CommandResult {
			return CommandResult{ExitCode: 7, Err: fmt.Errorf("exit status 7")}
		},
	})
	if err == nil {
		t.Fatal("expected workload error")
	}
	if result.Status != "failed" || result.ExitCode != 7 || result.Passed() {
		t.Fatalf("unexpected result: %#v", result)
	}

	var out bytes.Buffer
	if err := Render(&out, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "FAIL: workload shell/fail exit=7") {
		t.Fatalf("unexpected render output: %s", out.String())
	}
}

func TestRunHonorsNoLogEnv(t *testing.T) {
	root := t.TempDir()
	writeWorkload(t, root, "workloads/shell/no-log.env", strings.Join([]string{
		`WORKLOAD_NAME="no log workload"`,
		`WORKLOAD_KIND="shell"`,
		`WORKLOAD_REQUIRES_POSTGRES=0`,
		`WORKLOAD_CMD='true'`,
		"",
	}, "\n"))

	var seenEnv []string
	result, err := Run(root, speccatalog.New(root), "shell/no-log", Options{
		Getenv: func(name string) string {
			if name == "WORKLOAD_RUN_LOG" {
				return "0"
			}
			return ""
		},
		RunCommand: func(root string, command []string, env []string, stdout, stderr io.Writer) CommandResult {
			seenEnv = append([]string(nil), env...)
			return CommandResult{ExitCode: 0}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Logging || result.LogFile != "" || len(seenEnv) != 0 {
		t.Fatalf("unexpected no-log result/env: %#v env=%#v", result, seenEnv)
	}
}

func TestRenderJSON(t *testing.T) {
	result := Result{
		WorkloadSpec: "shell/hello",
		WorkloadKind: "shell",
		Status:       "passed",
		ExitCode:     0,
	}

	var out bytes.Buffer
	if err := RenderJSON(&out, result); err != nil {
		t.Fatal(err)
	}
	var payload Result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.WorkloadSpec != result.WorkloadSpec || payload.Status != "passed" {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func fixedTimes(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}

func writeWorkload(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
