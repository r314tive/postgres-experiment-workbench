package benchmarksettings

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

func TestConfigSettingNamesMatchesAssignmentConsumer(t *testing.T) {
	content := []byte(strings.Join([]string{
		"# comment only",
		" shared_buffers = '128MB' # trailing comment",
		"include 'ignored.conf'",
		"work_mem='8MB'",
		" shared_buffers = '256MB'",
		"custom . setting = 'on'",
		"quoted = 'value#discarded-by-shell-consumer'",
	}, "\n"))
	names, err := ConfigSettingNames(content)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"custom.setting", "quoted", "shared_buffers", "work_mem"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestConfigSettingNamesRejectsPotentialSecretOrCommandValues(t *testing.T) {
	for _, name := range []string{"primary_conninfo", "ssl_passphrase_command", "extension.api_token"} {
		t.Run(name, func(t *testing.T) {
			_, err := ConfigSettingNames([]byte(name + " = 'do-not-capture'\n"))
			if err == nil || !strings.Contains(err.Error(), "denied") {
				t.Fatalf("sensitive setting was accepted: %v", err)
			}
		})
	}
}

func TestParseFileBindsRawRowsAndPrepareWindow(t *testing.T) {
	path, source := writeSettingsSource(t, [][]string{
		settingsRow("ab-a-t001", testSettingsDigest("a"), "1", "2026-08-12T00:00:01.120000Z", "170009", "shared_buffers", "16384", "8kB", "configuration file", "f", "postmaster"),
		settingsRow("ab-a-t001", testSettingsDigest("a"), "1", "2026-08-12T00:00:01.120000Z", "170009", "work_mem", "4096", "kB", "default", "f", "user"),
	})
	expected := settingsExpectation(source)
	parsed, err := ParseFile(path, expected)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CapturedAt != "2026-08-12T00:00:01.12Z" || parsed.ServerVersionNum != "170009" || len(parsed.Settings) != 2 || parsed.Settings[0].PendingRestart || parsed.Digest == "" {
		t.Fatalf("unexpected normalized evidence: %#v", parsed)
	}
	if err := Verify(parsed); err != nil {
		t.Fatalf("normalized evidence does not verify: %v", err)
	}
}

func TestParseFileRejectsTransplantMissingExtraAndTamper(t *testing.T) {
	protocol := testSettingsDigest("a")
	base := [][]string{
		settingsRow("ab-a-t001", protocol, "1", "2026-08-12T00:00:01Z", "170009", "shared_buffers", "16384", "8kB", "configuration file", "f", "postmaster"),
		settingsRow("ab-a-t001", protocol, "1", "2026-08-12T00:00:01Z", "170009", "work_mem", "4096", "kB", "default", "f", "user"),
	}
	tests := []struct {
		name   string
		mutate func([][]string) [][]string
		want   string
	}{
		{"transplanted run", func(rows [][]string) [][]string { rows[0][0] = "other-t001"; return rows }, "binding mismatch"},
		{"transplanted protocol", func(rows [][]string) [][]string { rows[0][1] = testSettingsDigest("b"); return rows }, "binding mismatch"},
		{"transplanted trial", func(rows [][]string) [][]string { rows[0][2] = "2"; return rows }, "binding mismatch"},
		{"missing row", func(rows [][]string) [][]string { return rows[:1] }, "row count"},
		{"extra row", func(rows [][]string) [][]string { return append(rows, append([]string(nil), rows[1]...)) }, "row count"},
		{"out of order", func(rows [][]string) [][]string { rows[0], rows[1] = rows[1], rows[0]; return rows }, "exactly match"},
		{"pending restart", func(rows [][]string) [][]string { rows[0][9] = "yes"; return rows }, "pending_restart"},
		{"missing source", func(rows [][]string) [][]string { rows[0][8] = ""; return rows }, "incomplete"},
		{"outside phase", func(rows [][]string) [][]string {
			rows[0][3], rows[1][3] = "2026-08-12T00:00:04Z", "2026-08-12T00:00:04Z"
			return rows
		}, "outside"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows := cloneRows(base)
			path, source := writeSettingsSource(t, test.mutate(rows))
			_, err := ParseFile(path, settingsExpectation(source))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	path, source := writeSettingsSource(t, base)
	source.Digest = testSettingsDigest("f")
	if _, err := ParseFile(path, settingsExpectation(source)); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("coordinated source-reference tamper passed: %v", err)
	}
}

func TestEffectiveDifferenceUsesValueAndUnitOnly(t *testing.T) {
	left := Evidence{
		ServerVersionNum: "170009",
		Names:            []string{"shared_buffers", "work_mem"},
		Settings: []Setting{
			{Name: "shared_buffers", Setting: "16384", Unit: "8kB", Source: "default", Context: "postmaster"},
			{Name: "work_mem", Setting: "4096", Unit: "kB", Source: "default", Context: "user"},
		},
	}
	right := left
	right.Settings = append([]Setting(nil), left.Settings...)
	right.Settings[0].Source = "configuration file"
	if differences := EffectiveDifferenceNames(left, right); len(differences) != 0 {
		t.Fatalf("source-only difference counted as effective: %v", differences)
	}
	right.Settings[0].Setting = "32768"
	if differences := EffectiveDifferenceNames(left, right); !reflect.DeepEqual(differences, []string{"shared_buffers"}) {
		t.Fatalf("value difference was not retained: %v", differences)
	}
	right.Settings[0] = left.Settings[0]
	right.Settings[1].Unit = "8kB"
	if differences := EffectiveDifferenceNames(left, right); !reflect.DeepEqual(differences, []string{"work_mem"}) {
		t.Fatalf("unit difference was not retained: %v", differences)
	}
}

func writeSettingsSource(t *testing.T, rows [][]string) (string, SourceRef) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "effective-pg-settings.tsv")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := csv.NewWriter(file)
	writer.Comma = '\t'
	if err := writer.Write(sourceHeader); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, SourceRef{Path: SourcePath, Digest: digest, Size: info.Size()}
}

func settingsExpectation(source SourceRef) Expectation {
	return Expectation{
		RunID: "ab-a-t001", ProtocolDigest: testSettingsDigest("a"), Trial: 1,
		Names:  []string{"shared_buffers", "work_mem"},
		Source: source,
		Phase: PhaseBinding{
			Name: "prepare", JournalDigest: testSettingsDigest("c"),
			StartedAt: "2026-08-12T00:00:00Z", FinishedAt: "2026-08-12T00:00:03Z",
		},
	}
}

func settingsRow(runID, protocol, trial, captured, version, name, setting, unit, source, pending, context string) []string {
	return []string{runID, protocol, trial, captured, version, name, setting, unit, source, pending, context}
}

func cloneRows(rows [][]string) [][]string {
	result := make([][]string, len(rows))
	for index := range rows {
		result[index] = append([]string(nil), rows[index]...)
	}
	return result
}

func testSettingsDigest(character string) string { return "sha256:" + strings.Repeat(character, 64) }
