package topologyinspect

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectSingleTopology(t *testing.T) {
	root := t.TempDir()
	writeTopologyFile(t, root, ".env.example", "COMPOSE=docker compose\n")
	writeTopologyFile(t, root, "topologies/single.env", strings.Join([]string{
		`TOPOLOGY_NAME="single"`,
		`TOPOLOGY_DESCRIPTION="One PostgreSQL container."`,
		"",
	}, "\n"))

	inspection, err := Inspect(root, "single", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Name != "single" {
		t.Fatalf("unexpected topology name: %#v", inspection)
	}
	if strings.Join(inspection.Services, " ") != "postgres" {
		t.Fatalf("unexpected services: %#v", inspection.Services)
	}
	if len(inspection.Profiles) != 0 {
		t.Fatalf("unexpected profiles: %#v", inspection.Profiles)
	}
	if !strings.Contains(strings.Join(inspection.UpCommand, " "), "up -d postgres") {
		t.Fatalf("unexpected up command: %#v", inspection.UpCommand)
	}
}

func TestInspectSourceTreeTopologyWithoutSpecFile(t *testing.T) {
	root := t.TempDir()
	writeTopologyFile(t, root, ".env.example", "COMPOSE=docker compose\n")

	inspection, err := Inspect(root, "source-tree", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Name != "source-tree" {
		t.Fatalf("unexpected topology name: %#v", inspection.Name)
	}
	if strings.Join(inspection.Services, " ") != "postgres" {
		t.Fatalf("unexpected services: %#v", inspection.Services)
	}
	if len(inspection.Profiles) != 0 {
		t.Fatalf("unexpected profiles: %#v", inspection.Profiles)
	}
	if !strings.Contains(strings.Join(inspection.UpCommand, " "), "up -d postgres") {
		t.Fatalf("unexpected up command: %#v", inspection.UpCommand)
	}
}

func TestInspectPgbouncerResolvesEnvDefaults(t *testing.T) {
	root := t.TempDir()
	writeTopologyFile(t, root, ".env.example", "COMPOSE=docker compose\nPGBOUNCER_PORT=56432\n")
	writeTopologyFile(t, root, "topologies/pgbouncer.env", strings.Join([]string{
		`TOPOLOGY_NAME="pgbouncer"`,
		`TOPOLOGY_DESCRIPTION="PostgreSQL plus PgBouncer."`,
		`TOPOLOGY_SERVICES="postgres pgbouncer"`,
		`TOPOLOGY_POOL_PORT="${PGBOUNCER_PORT:-56432}"`,
		"",
	}, "\n"))

	inspection, err := Inspect(root, "pgbouncer", Options{
		Env: map[string]string{"PGBOUNCER_PORT": "65432"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(inspection.Profiles, " ") != "pgbouncer" {
		t.Fatalf("unexpected profiles: %#v", inspection.Profiles)
	}
	if strings.Join(inspection.Services, " ") != "postgres pgbouncer" {
		t.Fatalf("unexpected services: %#v", inspection.Services)
	}
	if inspection.ResolvedValues["TOPOLOGY_POOL_PORT"] != "65432" {
		t.Fatalf("env default was not resolved: %#v", inspection.ResolvedValues)
	}

	var out bytes.Buffer
	if err := Render(&out, inspection); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"TOPOLOGY_NAME=pgbouncer",
		"TOPOLOGY_PROFILES=pgbouncer",
		"TOPOLOGY_SERVICES=postgres pgbouncer",
		"RESOLVED_TOPOLOGY_POOL_PORT=65432",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered inspection missing %q:\n%s", want, out.String())
		}
	}
}

func TestInspectMissingTopology(t *testing.T) {
	if _, err := Inspect(t.TempDir(), "missing", Options{}); err == nil {
		t.Fatal("expected missing topology error")
	}
}

func writeTopologyFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
