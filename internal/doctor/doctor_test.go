package doctor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorPassesForDisposableLocalEnvironment(t *testing.T) {
	root := testRoot(t, `POSTGRES_HOST=127.0.0.1
POSTGRES_DB=pg_experiment_workbench
ALLOW_NONLOCAL_PG=0
ALLOW_SYSTEM_DB=0
`)

	result := Run(root, Options{}, fakeDeps(t, root, map[string]string{
		"go version":             "go version go1.23 linux/amd64",
		"docker --version":       "Docker version 27.0.0",
		"docker compose version": "Docker Compose version v2.29.0",
		"docker compose --env-file " + filepath.Join(root, ".env.example") + " config --quiet": "",
		"docker info --format {{.ServerVersion}}":                                              "27.0.0",
	}))

	if !result.Valid() {
		t.Fatalf("expected valid doctor result, got %#v", result.Checks)
	}
	assertCheck(t, result, Pass, "local target guard")
	assertCheck(t, result, Pass, "docker daemon")

	var out bytes.Buffer
	if err := Render(&out, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "summary:") {
		t.Fatalf("expected summary in output:\n%s", out.String())
	}
}

func TestDoctorFailsForMissingRequiredCommand(t *testing.T) {
	root := testRoot(t, `POSTGRES_HOST=127.0.0.1
POSTGRES_DB=pg_experiment_workbench
`)

	deps := fakeDeps(t, root, map[string]string{
		"go version":             "go version go1.23 linux/amd64",
		"docker --version":       "Docker version 27.0.0",
		"docker compose version": "Docker Compose version v2.29.0",
		"docker compose --env-file " + filepath.Join(root, ".env.example") + " config --quiet": "",
		"docker info --format {{.ServerVersion}}":                                              "27.0.0",
	})
	deps.LookupPath = func(command string) (string, error) {
		if command == "docker" {
			return "", errors.New("missing")
		}
		return "/fake/bin/" + command, nil
	}

	result := Run(root, Options{}, deps)
	if result.Valid() {
		t.Fatalf("expected invalid doctor result")
	}
	assertCheck(t, result, Fail, "command docker")
}

func TestDoctorFailsForNonLocalTarget(t *testing.T) {
	root := testRoot(t, `POSTGRES_HOST=db.example.internal
POSTGRES_DB=pg_experiment_workbench
ALLOW_NONLOCAL_PG=0
`)

	result := Run(root, Options{SkipDockerDaemon: true}, fakeDeps(t, root, map[string]string{
		"go version":             "go version go1.23 linux/amd64",
		"docker --version":       "Docker version 27.0.0",
		"docker compose version": "Docker Compose version v2.29.0",
		"docker compose --env-file " + filepath.Join(root, ".env.example") + " config --quiet": "",
	}))

	if result.Valid() {
		t.Fatalf("expected invalid doctor result")
	}
	assertCheck(t, result, Fail, "local target guard")
}

func testRoot(t *testing.T, env string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Makefile"), ".DEFAULT_GOAL := help\n")
	writeFile(t, filepath.Join(root, "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(root, ".env.example"), env)
	return root
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeDeps(t *testing.T, root string, outputs map[string]string) Deps {
	t.Helper()
	return Deps{
		LookupPath: func(command string) (string, error) {
			return "/fake/bin/" + command, nil
		},
		RunCommand: func(command string, args ...string) (string, error) {
			key := strings.Join(append([]string{command}, args...), " ")
			if output, ok := outputs[key]; ok {
				return output, nil
			}
			return "", errors.New("unexpected command: " + key)
		},
		Stat: os.Stat,
	}
}

func assertCheck(t *testing.T, result Result, status Status, name string) {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("expected %s to be %s, got %s", name, status, check.Status)
			}
			return
		}
	}
	t.Fatalf("missing check: %s", name)
}
