package metricsplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildDefaults(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	plan, err := Build(root, "", mapEnv(nil), now)
	if err != nil {
		t.Fatal(err)
	}

	wantOutput := filepath.Join(root, "logs", "metrics", "metrics.20260102_030405.csv")
	if plan.Output != wantOutput {
		t.Fatalf("unexpected output: %s", plan.Output)
	}
	if plan.IntervalSeconds != 1 || plan.DurationSeconds != 30 || plan.Mode != "duration" || plan.Samples != 0 {
		t.Fatalf("unexpected timing plan: %#v", plan)
	}
	if plan.Query != "sql/metrics_sample.sql" {
		t.Fatalf("unexpected query path: %s", plan.Query)
	}
	if len(plan.Header) != 25 || plan.Header[0] != "sampled_at" || plan.Header[len(plan.Header)-1] != "current_wal_lsn" {
		t.Fatalf("unexpected header: %#v", plan.Header)
	}
}

func TestBuildUsesEnvAndOutputArg(t *testing.T) {
	plan, err := Build("/repo", "logs/custom.csv", mapEnv(map[string]string{
		"METRICS_INTERVAL": "2",
		"METRICS_DURATION": "0",
		"METRICS_SAMPLES":  "3",
		"METRICS_APPEND":   "1",
		"METRICS_OUT":      "ignored.csv",
	}), time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Output != "logs/custom.csv" || !plan.Append {
		t.Fatalf("unexpected output/append: %#v", plan)
	}
	if plan.Mode != "samples" || plan.Samples != 3 || plan.IntervalSeconds != 2 || plan.DurationSeconds != 0 {
		t.Fatalf("unexpected timing plan: %#v", plan)
	}

	var out bytes.Buffer
	if err := Render(&out, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Metrics Sampling Plan", "| Mode | `samples` |", "```csv", "current_wal_lsn"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("rendered output missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := RenderJSON(&out, plan); err != nil {
		t.Fatal(err)
	}
	var payload Plan
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Output != plan.Output || payload.Samples != plan.Samples || len(payload.Header) != len(plan.Header) {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestBuildValidatesIntegers(t *testing.T) {
	cases := []map[string]string{
		{"METRICS_INTERVAL": "0"},
		{"METRICS_DURATION": "-1"},
		{"METRICS_SAMPLES": "0"},
	}
	for _, env := range cases {
		if _, err := Build("/repo", "", mapEnv(env), time.Time{}); err == nil {
			t.Fatalf("expected validation error for env %#v", env)
		}
	}
}

func TestShellSamplerHeaderMatchesPlan(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "scripts", "sample_metrics.sh"))
	if err != nil {
		t.Fatal(err)
	}
	header := ""
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "HEADER=") {
			header = strings.Trim(strings.TrimPrefix(line, "HEADER="), `"`)
			break
		}
	}
	if header == "" {
		t.Fatal("sample_metrics.sh HEADER assignment not found")
	}
	if got, want := header, strings.Join(Header, ","); got != want {
		t.Fatalf("shell sampler header drifted from Go metrics plan\nwant: %s\n got: %s", want, got)
	}
}

func mapEnv(values map[string]string) Env {
	return func(name string) string {
		return values[name]
	}
}
