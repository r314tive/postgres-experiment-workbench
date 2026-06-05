package workloadbg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectNoState(t *testing.T) {
	root := t.TempDir()

	status := Inspect(root)
	if status.State != "not_running" || status.PID != 0 {
		t.Fatalf("unexpected status: %#v", status)
	}
	if len(status.Issues) != 0 {
		t.Fatalf("unexpected issues: %#v", status.Issues)
	}
}

func TestInspectRunningProcess(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, ".tmp", "workloads")
	writeStateFile(t, filepath.Join(stateDir, "current.pid"), os.Getpid())
	writeStateFile(t, filepath.Join(stateDir, "current.cmd"), "run-workload pgbench/tiny")
	logPath := filepath.Join(root, "logs", "workloads", "current.log")
	writeStateFile(t, logPath, "started\n")
	writeStateFile(t, filepath.Join(stateDir, "current.log"), logPath)

	status := Inspect(root)
	if status.State != "running" || status.PID != os.Getpid() || !status.LogExists {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.Command != "run-workload pgbench/tiny" || status.Log != logPath {
		t.Fatalf("unexpected status metadata: %#v", status)
	}

	var out bytes.Buffer
	if err := RenderJSON(&out, status); err != nil {
		t.Fatal(err)
	}
	var payload Status
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.State != "running" || payload.Command == "" {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestInspectInvalidPID(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, ".tmp", "workloads")
	writeStateFile(t, filepath.Join(stateDir, "current.pid"), "not-a-pid")

	status := Inspect(root)
	if status.State != "unknown" {
		t.Fatalf("unexpected status: %#v", status)
	}
	if !hasIssue(status, "invalid pid: not-a-pid") {
		t.Fatalf("missing invalid pid issue: %#v", status.Issues)
	}
}

func TestRenderHumanStatus(t *testing.T) {
	status := Status{
		State:   "stopped",
		PID:     123,
		Command: "run-workload pgbench/tiny",
		Log:     "/tmp/workload.log",
		Issues:  []string{"log missing"},
	}

	var out bytes.Buffer
	if err := Render(&out, status); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stopped pid=123", "command=run-workload pgbench/tiny", "log=/tmp/workload.log", "issue=log missing"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered status missing %q:\n%s", want, out.String())
		}
	}
}

func writeStateFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(fmt.Sprint(value))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasIssue(status Status, issue string) bool {
	for _, candidate := range status.Issues {
		if candidate == issue {
			return true
		}
	}
	return false
}
