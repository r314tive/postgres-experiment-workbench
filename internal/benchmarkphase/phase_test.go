package benchmarkphase

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseBuildVerifyTimeline(t *testing.T) {
	journal := validV3Journal("bench-run-t003", 3)
	timeline, err := ParseTSV(strings.NewReader(journal), 3, "bench-run-t003")
	if err != nil {
		t.Fatal(err)
	}
	if timeline.SchemaVersion != SchemaVersion || timeline.RunID != "bench-run-t003" || timeline.Trial != 3 || timeline.Status != "passed" || len(timeline.Events) != len(OrderedNames) || timeline.Digest == "" {
		t.Fatalf("unexpected timeline: %#v", timeline)
	}
	if timeline.Events[StabilizeIndex].Status != "skipped" || timeline.Events[MeasureIndex].DurationNS != int64(time.Second) || timeline.Events[MeasureIndex].DurationMS != 1000 {
		t.Fatalf("unexpected normalized events: %#v", timeline.Events)
	}
	if timeline.DurationNS != int64(9*time.Second) || timeline.DurationMS != 9000 {
		t.Fatalf("unexpected normalized aggregate duration: %#v", timeline)
	}
	if err := Verify(timeline); err != nil {
		t.Fatal(err)
	}
	first, _ := JSON(timeline)
	second, _ := JSON(timeline)
	if string(first) != string(second) || first[len(first)-1] != '\n' {
		t.Fatal("timeline JSON is not deterministic")
	}
}

func TestV3JournalRejectsRunAndTrialRebinding(t *testing.T) {
	valid := validV3Journal("bench-run-t001", 1)
	tests := []struct {
		name, journal, runID, want string
		trial                      int
	}{
		{name: "expected run", journal: valid, runID: "other-run-t001", trial: 1, want: "run id mismatch"},
		{name: "expected trial", journal: valid, runID: "bench-run-t001", trial: 2, want: "trial mismatch"},
		{name: "row run", journal: strings.Replace(valid, "bench-run-t001\t1\t7\t", "other-run-t001\t1\t7\t", 1), runID: "bench-run-t001", trial: 1, want: "run id mismatch"},
		{name: "row trial", journal: strings.Replace(valid, "bench-run-t001\t1\t7\t", "bench-run-t001\t2\t7\t", 1), runID: "bench-run-t001", trial: 1, want: "trial mismatch"},
		{name: "mixed format", journal: strings.Replace(valid, "bench-run-t001\t1\t7\t", "7\t", 1), trial: 1, want: "mixes schema versions"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseTSV(strings.NewReader(test.journal), test.trial, optionalRunID(test.runID)...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestExpectedRunRejectsLegacyJournal(t *testing.T) {
	_, err := ParseTSV(strings.NewReader(validJournal()), 1, "bench-run-t001")
	if err == nil || !strings.Contains(err.Error(), "has no run/trial binding") {
		t.Fatalf("expected fail-closed legacy rejection, got %v", err)
	}
	legacy, err := ParseTSV(strings.NewReader(validJournal()), 1)
	if err != nil || legacy.SchemaVersion != LegacySchemaVersion || legacy.RunID != "" {
		t.Fatalf("legacy read compatibility failed: timeline=%#v err=%v", legacy, err)
	}
}

func TestV2BoundJournalRemainsVerifiable(t *testing.T) {
	timeline, err := ParseTSV(strings.NewReader(validV2Journal("bench-run-t001", 1)), 1, "bench-run-t001")
	if err != nil {
		t.Fatal(err)
	}
	if timeline.SchemaVersion != BoundLegacySchemaVersion || timeline.RunID != "bench-run-t001" || len(timeline.Events) != 9 {
		t.Fatalf("unexpected legacy bound timeline: %#v", timeline)
	}
	if err := Verify(timeline); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineRejectsStructuralContradictions(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{"missing", "9\tcleanup\tpassed\t2026-08-12T00:00:08Z\t2026-08-12T00:00:09Z\t\n", "", "has 8 events"},
		{"order", "2\tprepare", "2\twarmup", "identity mismatch"},
		{"required skip", "5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t", "5\tmeasure\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\tnot declared", "required benchmark phase measure"},
		{"skip reason", "3\tstabilize\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tnot declared", "3\tstabilize\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\t", "without a reason"},
		{"failure reason", "5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t", "5\tmeasure\tfailed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t", "without a reason"},
		{"cleanup skip", "9\tcleanup\tpassed\t2026-08-12T00:00:08Z\t2026-08-12T00:00:09Z\t", "9\tcleanup\tskipped\t2026-08-12T00:00:08Z\t2026-08-12T00:00:09Z\tnot reached", "cannot be skipped"},
		{"zero measure", "5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t", "5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\t", "positive duration"},
		{"overlap", "6\tcooldown\tpassed\t2026-08-12T00:00:05Z", "6\tcooldown\tpassed\t2026-08-12T00:00:02Z", "overlaps"},
		{"bad timestamp", "2026-08-12T00:00:05Z\t2026-08-12T00:00:06Z", "bad\t2026-08-12T00:00:06Z", "started_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseTSV(strings.NewReader(strings.Replace(validJournal(), test.old, test.new, 1)), 1)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestTimelinePreservesPositiveSubmillisecondMeasureDuration(t *testing.T) {
	journal := strings.Replace(validJournal(),
		"5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t",
		"5\tmeasure\tpassed\t2026-08-12T00:00:02.000100Z\t2026-08-12T00:00:02.000350Z\t", 1)
	timeline, err := ParseTSV(strings.NewReader(journal), 1)
	if err != nil {
		t.Fatal(err)
	}
	measure, ok := EventByName(timeline, MeasureName)
	if !ok {
		t.Fatal("measure event is missing")
	}
	if measure.DurationNS != 250_000 || measure.DurationMS != 0 {
		t.Fatalf("submillisecond measure duration was lost: %#v", measure)
	}
	if err := Verify(timeline); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	timeline, err := ParseTSV(strings.NewReader(validV3Journal("bench-run-t001", 1)), 1, "bench-run-t001")
	if err != nil {
		t.Fatal(err)
	}
	timeline.Events[MeasureIndex].FinishedAt = time.Date(2026, 8, 12, 0, 0, 7, 0, time.UTC).Format(time.RFC3339Nano)
	if err := Verify(timeline); err == nil {
		t.Fatal("tampered timeline verified")
	}
}

func TestVerifyRejectsTimelineRunIDTampering(t *testing.T) {
	timeline, err := ParseTSV(strings.NewReader(validV3Journal("bench-run-t001", 1)), 1, "bench-run-t001")
	if err != nil {
		t.Fatal(err)
	}
	timeline.RunID = "other-run-t001"
	if err := Verify(timeline); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered run binding verified: %v", err)
	}
}

func TestTimelineRetainsCompleteFailureLifecycle(t *testing.T) {
	journal := strings.Replace(validJournal(),
		"5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t\n6\tcooldown\tpassed",
		"5\tmeasure\tfailed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\tpgbench exited 1\n6\tcooldown\tskipped", 1)
	journal = strings.NewReplacer(
		"6\tcooldown\tskipped\t2026-08-12T00:00:05Z\t2026-08-12T00:00:06Z\t", "6\tcooldown\tskipped\t2026-08-12T00:00:05Z\t2026-08-12T00:00:06Z\tnot reached after failed measure phase",
		"7\tvalidate\tpassed\t2026-08-12T00:00:06Z\t2026-08-12T00:00:07Z\t", "7\tvalidate\tskipped\t2026-08-12T00:00:06Z\t2026-08-12T00:00:07Z\tnot reached after failed measure phase",
		"8\tcollect\tpassed\t2026-08-12T00:00:07Z\t2026-08-12T00:00:08Z\t", "8\tcollect\tskipped\t2026-08-12T00:00:07Z\t2026-08-12T00:00:08Z\tnot reached after failed measure phase",
	).Replace(journal)
	timeline, err := ParseTSV(strings.NewReader(journal), 1)
	if err != nil {
		t.Fatal(err)
	}
	measure, _ := EventByName(timeline, MeasureName)
	cooldown, _ := EventByName(timeline, CooldownName)
	if timeline.Status != "failed" || measure.Status != "failed" || cooldown.Status != "skipped" {
		t.Fatalf("unexpected failed timeline: %#v", timeline)
	}
}

func TestTimelineRejectsNonSkippedPhaseAfterFailure(t *testing.T) {
	journal := strings.Replace(validJournal(),
		"3\tstabilize\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tnot declared",
		"3\tstabilize\tfailed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tstabilization probe failed", 1)
	_, err := ParseTSV(strings.NewReader(journal), 1)
	if err == nil || !strings.Contains(err.Error(), "measure must be skipped after an earlier phase failed") {
		t.Fatalf("expected strict post-failure transition rejection, got %v", err)
	}
}

func TestTimelineAllowsCleanupFailureAfterEarlierFailure(t *testing.T) {
	journal := strings.Replace(validJournal(),
		"5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t",
		"5\tmeasure\tfailed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\tpgbench exited 1", 1)
	journal = strings.NewReplacer(
		"6\tcooldown\tpassed\t2026-08-12T00:00:05Z\t2026-08-12T00:00:06Z\t", "6\tcooldown\tskipped\t2026-08-12T00:00:05Z\t2026-08-12T00:00:06Z\tnot reached after failed measure phase",
		"7\tvalidate\tpassed\t2026-08-12T00:00:06Z\t2026-08-12T00:00:07Z\t", "7\tvalidate\tskipped\t2026-08-12T00:00:06Z\t2026-08-12T00:00:07Z\tnot reached after failed measure phase",
		"8\tcollect\tpassed\t2026-08-12T00:00:07Z\t2026-08-12T00:00:08Z\t", "8\tcollect\tskipped\t2026-08-12T00:00:07Z\t2026-08-12T00:00:08Z\tnot reached after failed measure phase",
		"9\tcleanup\tpassed\t2026-08-12T00:00:08Z\t2026-08-12T00:00:09Z\t", "9\tcleanup\tfailed\t2026-08-12T00:00:08Z\t2026-08-12T00:00:09Z\tcleanup exited 1",
	).Replace(journal)
	timeline, err := ParseTSV(strings.NewReader(journal), 1)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, _ := EventByName(timeline, CleanupName)
	if timeline.Status != "failed" || cleanup.Status != "failed" {
		t.Fatalf("cleanup failure was not retained: %#v", timeline)
	}
}

func validJournal() string {
	return strings.Join([]string{
		"1\tpreflight\tpassed\t2026-08-12T00:00:00Z\t2026-08-12T00:00:01Z\t",
		"2\tprepare\tpassed\t2026-08-12T00:00:01Z\t2026-08-12T00:00:02Z\t",
		"3\tstabilize\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tnot declared",
		"4\twarmup\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tzero duration",
		"5\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t",
		"6\tcooldown\tpassed\t2026-08-12T00:00:05Z\t2026-08-12T00:00:06Z\t",
		"7\tvalidate\tpassed\t2026-08-12T00:00:06Z\t2026-08-12T00:00:07Z\t",
		"8\tcollect\tpassed\t2026-08-12T00:00:07Z\t2026-08-12T00:00:08Z\t",
		"9\tcleanup\tpassed\t2026-08-12T00:00:08Z\t2026-08-12T00:00:09Z\t",
	}, "\n") + "\n"
}

func validV2Journal(runID string, trial int) string {
	var rows []string
	for _, row := range strings.Split(strings.TrimSuffix(validJournal(), "\n"), "\n") {
		rows = append(rows, runID+"\t"+strconv.Itoa(trial)+"\t"+row)
	}
	return strings.Join(rows, "\n") + "\n"
}

func validV3Journal(runID string, trial int) string {
	rows := []string{
		"1\tpreflight\tpassed\t2026-08-12T00:00:00Z\t2026-08-12T00:00:01Z\t",
		"2\tprepare\tpassed\t2026-08-12T00:00:01Z\t2026-08-12T00:00:02Z\t",
		"3\tstabilize\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tnot declared",
		"4\tpre-warmup-control\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tno before-warmup control declared",
		"5\twarmup\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tzero duration",
		"6\tpre-measure-control\tskipped\t2026-08-12T00:00:02Z\t2026-08-12T00:00:02Z\tno pre-measure control declared",
		"7\tmeasure\tpassed\t2026-08-12T00:00:02Z\t2026-08-12T00:00:03Z\t",
		"8\tcooldown\tpassed\t2026-08-12T00:00:05Z\t2026-08-12T00:00:06Z\t",
		"9\tvalidate\tpassed\t2026-08-12T00:00:06Z\t2026-08-12T00:00:07Z\t",
		"10\tcollect\tpassed\t2026-08-12T00:00:07Z\t2026-08-12T00:00:08Z\t",
		"11\tcleanup\tpassed\t2026-08-12T00:00:08Z\t2026-08-12T00:00:09Z\t",
	}
	for index := range rows {
		rows[index] = runID + "\t" + strconv.Itoa(trial) + "\t" + rows[index]
	}
	return strings.Join(rows, "\n") + "\n"
}

func optionalRunID(runID string) []string {
	if runID == "" {
		return nil
	}
	return []string{runID}
}
