package topologyinspect

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseComposePSArray(t *testing.T) {
	records, err := parseComposePS(`[
  {"Name":"workbench-postgres-1","Service":"postgres","State":"running","Health":"healthy","ExitCode":0},
  {"Name":"workbench-pgbouncer-1","Service":"pgbouncer","State":"running","ExitCode":0}
]`)
	if err != nil {
		t.Fatal(err)
	}
	services := summarizeServices([]string{"postgres", "pgbouncer"}, records)
	if classifyRuntime(services) != "running" {
		t.Fatalf("unexpected runtime classification: %#v", services)
	}
}

func TestParseComposePSNewlineJSON(t *testing.T) {
	records, err := parseComposePS(strings.Join([]string{
		`{"Name":"workbench-postgres-1","Service":"postgres","State":"running","ExitCode":0}`,
		`{"Name":"workbench-replica-1","Service":"replica","State":"exited","ExitCode":1}`,
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	services := summarizeServices([]string{"postgres", "replica"}, records)
	if classifyRuntime(services) != "partial" {
		t.Fatalf("unexpected runtime classification: %#v", services)
	}
}

func TestSummarizeMissingServices(t *testing.T) {
	services := summarizeServices([]string{"postgres"}, nil)
	if len(services) != 1 || !services[0].Missing || classifyRuntime(services) != "stopped" {
		t.Fatalf("unexpected missing service summary: %#v", services)
	}
}

func TestRuntimeUsesInjectedRunner(t *testing.T) {
	root := t.TempDir()
	writeTopologyFile(t, root, ".env.example", "COMPOSE=docker compose\n")
	writeTopologyFile(t, root, "topologies/primary-replica.env", strings.Join([]string{
		`TOPOLOGY_NAME="primary-replica"`,
		`TOPOLOGY_DESCRIPTION="Primary plus replica."`,
		`TOPOLOGY_SERVICES="postgres replica"`,
		"",
	}, "\n"))

	status, err := Runtime(root, "primary-replica", RuntimeOptions{
		RunCommand: func(command []string) (string, error) {
			if !strings.Contains(strings.Join(command, " "), "--profile replica ps --format json postgres replica") {
				t.Fatalf("unexpected command: %#v", command)
			}
			return `[
  {"Name":"workbench-postgres-1","Service":"postgres","State":"running","ExitCode":0},
  {"Name":"workbench-replica-1","Service":"replica","State":"running","ExitCode":0}
]`, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Result != "running" {
		t.Fatalf("unexpected runtime result: %#v", status)
	}

	var out bytes.Buffer
	if err := RenderRuntime(&out, status); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"TOPOLOGY_NAME=primary-replica",
		"SERVICE postgres",
		"SERVICE replica",
		"result=running",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered runtime status missing %q:\n%s", want, out.String())
		}
	}
}
