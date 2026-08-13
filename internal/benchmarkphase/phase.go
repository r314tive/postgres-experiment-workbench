// Package benchmarkphase defines and verifies the lifecycle timeline of one
// benchmark trial. The timeline is evidence about orchestration boundaries;
// it does not turn wall-clock timestamps into independent performance samples.
package benchmarkphase

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const (
	SchemaVersion            = "pgworkbench.benchmark-phase-timeline/v3"
	BoundLegacySchemaVersion = "pgworkbench.benchmark-phase-timeline/v2"
	LegacySchemaVersion      = "pgworkbench.benchmark-phase-timeline/v1"
	ArtifactType             = "pgworkbench.benchmark-phase-timeline"

	PreflightName         = "preflight"
	PrepareName           = "prepare"
	StabilizeName         = "stabilize"
	PreWarmupControlName  = "pre-warmup-control"
	WarmupName            = "warmup"
	PreMeasureControlName = "pre-measure-control"
	MeasureName           = "measure"
	CooldownName          = "cooldown"
	ValidateName          = "validate"
	CollectName           = "collect"
	CleanupName           = "cleanup"
)

const (
	PreflightIndex = iota
	PrepareIndex
	StabilizeIndex
	PreWarmupControlIndex
	WarmupIndex
	PreMeasureControlIndex
	MeasureIndex
	CooldownIndex
	ValidateIndex
	CollectIndex
	CleanupIndex
)

var OrderedNames = []string{
	PreflightName,
	PrepareName,
	StabilizeName,
	PreWarmupControlName,
	WarmupName,
	PreMeasureControlName,
	MeasureName,
	CooldownName,
	ValidateName,
	CollectName,
	CleanupName,
}

var legacyOrderedNames = []string{
	PreflightName,
	PrepareName,
	StabilizeName,
	WarmupName,
	MeasureName,
	CooldownName,
	ValidateName,
	CollectName,
	CleanupName,
}

type Event struct {
	Sequence   int    `json:"sequence"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	DurationNS int64  `json:"duration_ns"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason,omitempty"`
}

type Timeline struct {
	SchemaVersion string  `json:"schema_version"`
	ArtifactType  string  `json:"artifact_type"`
	RunID         string  `json:"run_id"`
	Trial         int     `json:"trial"`
	StartedAt     string  `json:"started_at"`
	FinishedAt    string  `json:"finished_at"`
	DurationNS    int64   `json:"duration_ns"`
	DurationMS    int64   `json:"duration_ms"`
	Status        string  `json:"status"`
	Events        []Event `json:"events"`
	Digest        string  `json:"digest"`
}

// ParseTSV parses the append-only shell/runner phase journal. It deliberately
// accepts no header or comments so truncation and foreign output fail closed.
// New journals have eight columns: run_id, trial, sequence, name, status,
// started_at, finished_at, reason. When expectedRunID is supplied, legacy
// six-column v1 journals are rejected because they cannot prove run binding.
func ParseTSV(r io.Reader, trial int, expectedRunID ...string) (Timeline, error) {
	if len(expectedRunID) > 1 {
		return Timeline{}, fmt.Errorf("benchmark phase parser accepts at most one expected run id")
	}
	wantRunID := ""
	if len(expectedRunID) == 1 {
		wantRunID = expectedRunID[0]
		if !validRunID(wantRunID) {
			return Timeline{}, fmt.Errorf("benchmark phase expected run id is invalid")
		}
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var events []Event
	journalFormat := ""
	runID := ""
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSuffix(scanner.Text(), "\r")
		fields := strings.Split(text, "\t")
		format := "legacy-unbound"
		if len(fields) == 8 {
			format = "bound"
			rowTrial, trialErr := strconv.Atoi(fields[1])
			if !validRunID(fields[0]) || trialErr != nil || rowTrial <= 0 || strconv.Itoa(rowTrial) != fields[1] {
				return Timeline{}, fmt.Errorf("benchmark phase journal line %d has invalid run/trial binding", line)
			}
			if rowTrial != trial {
				return Timeline{}, fmt.Errorf("benchmark phase journal line %d trial mismatch: got %d want %d", line, rowTrial, trial)
			}
			if wantRunID != "" && fields[0] != wantRunID {
				return Timeline{}, fmt.Errorf("benchmark phase journal line %d run id mismatch: got %q want %q", line, fields[0], wantRunID)
			}
			if runID != "" && fields[0] != runID {
				return Timeline{}, fmt.Errorf("benchmark phase journal line %d changes run id binding", line)
			}
			runID = fields[0]
			fields = fields[2:]
		} else if len(fields) == 6 {
			if wantRunID != "" {
				return Timeline{}, fmt.Errorf("legacy benchmark phase journal line %d has no run/trial binding", line)
			}
		} else {
			return Timeline{}, fmt.Errorf("benchmark phase journal line %d has %d columns, want 8", line, len(fields))
		}
		if journalFormat != "" && format != journalFormat {
			return Timeline{}, fmt.Errorf("benchmark phase journal mixes schema versions at line %d", line)
		}
		journalFormat = format
		sequence, err := strconv.Atoi(fields[0])
		if err != nil || sequence <= 0 || strconv.Itoa(sequence) != fields[0] {
			return Timeline{}, fmt.Errorf("benchmark phase journal line %d has invalid sequence %q", line, fields[0])
		}
		if strings.ContainsAny(fields[5], "\r\n\t") {
			return Timeline{}, fmt.Errorf("benchmark phase journal line %d has invalid reason", line)
		}
		events = append(events, Event{
			Sequence:   sequence,
			Name:       fields[1],
			Status:     fields[2],
			StartedAt:  fields[3],
			FinishedAt: fields[4],
			Reason:     fields[5],
		})
	}
	if err := scanner.Err(); err != nil {
		return Timeline{}, fmt.Errorf("read benchmark phase journal: %w", err)
	}
	if journalFormat == "bound" {
		if len(events) == len(legacyOrderedNames) {
			return build(BoundLegacySchemaVersion, runID, trial, events, legacyOrderedNames)
		}
		return BuildForRun(runID, trial, events)
	}
	return Build(trial, events)
}

// Build validates a legacy v1 lifecycle. New producers must use BuildForRun.
func Build(trial int, events []Event) (Timeline, error) {
	return build(LegacySchemaVersion, "", trial, events, legacyOrderedNames)
}

// BuildForRun validates a v3 lifecycle bound to one exact linked run/trial.
func BuildForRun(runID string, trial int, events []Event) (Timeline, error) {
	if !validRunID(runID) {
		return Timeline{}, fmt.Errorf("benchmark phase run id is invalid")
	}
	return build(SchemaVersion, runID, trial, events, OrderedNames)
}

func build(schemaVersion string, runID string, trial int, events []Event, orderedNames []string) (Timeline, error) {
	if trial <= 0 {
		return Timeline{}, fmt.Errorf("benchmark phase trial must be positive")
	}
	if len(events) != len(orderedNames) {
		return Timeline{}, fmt.Errorf("benchmark phase timeline has %d events, want %d", len(events), len(orderedNames))
	}
	copyEvents := append([]Event(nil), events...)
	var previousFinish time.Time
	status := "passed"
	seenFailure := false
	for index := range copyEvents {
		event := &copyEvents[index]
		wantSequence := index + 1
		if event.Sequence != wantSequence || event.Name != orderedNames[index] {
			return Timeline{}, fmt.Errorf("benchmark phase %d identity mismatch: got %d/%q want %d/%q", index+1, event.Sequence, event.Name, wantSequence, orderedNames[index])
		}
		if event.Status != "passed" && event.Status != "failed" && event.Status != "skipped" {
			return Timeline{}, fmt.Errorf("benchmark phase %s has unsupported status %q", event.Name, event.Status)
		}
		if (event.Status == "failed" || event.Status == "skipped") && event.Reason == "" {
			return Timeline{}, fmt.Errorf("benchmark phase %s was %s without a reason", event.Name, event.Status)
		}
		if event.Name == CleanupName {
			if event.Status == "skipped" {
				return Timeline{}, fmt.Errorf("benchmark cleanup phase must run and cannot be skipped")
			}
		} else if seenFailure {
			if event.Status != "skipped" {
				return Timeline{}, fmt.Errorf("benchmark phase %s must be skipped after an earlier phase failed", event.Name)
			}
		} else if required(event.Name) && event.Status == "skipped" {
			return Timeline{}, fmt.Errorf("required benchmark phase %s cannot be skipped", event.Name)
		}
		started, err := time.Parse(time.RFC3339Nano, event.StartedAt)
		if err != nil {
			return Timeline{}, fmt.Errorf("benchmark phase %s started_at: %w", event.Name, err)
		}
		finished, err := time.Parse(time.RFC3339Nano, event.FinishedAt)
		if err != nil {
			return Timeline{}, fmt.Errorf("benchmark phase %s finished_at: %w", event.Name, err)
		}
		started, finished = started.UTC(), finished.UTC()
		if finished.Before(started) {
			return Timeline{}, fmt.Errorf("benchmark phase %s finishes before it starts", event.Name)
		}
		if index > 0 && started.Before(previousFinish) {
			return Timeline{}, fmt.Errorf("benchmark phase %s overlaps the previous phase", event.Name)
		}
		event.StartedAt = started.Format(time.RFC3339Nano)
		event.FinishedAt = finished.Format(time.RFC3339Nano)
		duration := finished.Sub(started)
		event.DurationNS = duration.Nanoseconds()
		event.DurationMS = duration.Milliseconds()
		if event.Name == MeasureName && event.Status == "passed" && event.DurationNS <= 0 {
			return Timeline{}, fmt.Errorf("passed benchmark measure phase must have positive duration")
		}
		previousFinish = finished
		if event.Status == "failed" {
			status = "failed"
			seenFailure = true
		}
	}
	started, _ := time.Parse(time.RFC3339Nano, copyEvents[0].StartedAt)
	finished, _ := time.Parse(time.RFC3339Nano, copyEvents[len(copyEvents)-1].FinishedAt)
	timeline := Timeline{
		SchemaVersion: schemaVersion,
		ArtifactType:  ArtifactType,
		RunID:         runID,
		Trial:         trial,
		StartedAt:     started.Format(time.RFC3339Nano),
		FinishedAt:    finished.Format(time.RFC3339Nano),
		DurationNS:    finished.Sub(started).Nanoseconds(),
		DurationMS:    finished.Sub(started).Milliseconds(),
		Status:        status,
		Events:        copyEvents,
	}
	digest, err := digestTimeline(timeline)
	if err != nil {
		return Timeline{}, err
	}
	timeline.Digest = digest
	return timeline, nil
}

func Verify(timeline Timeline) error {
	if (timeline.SchemaVersion != SchemaVersion && timeline.SchemaVersion != BoundLegacySchemaVersion && timeline.SchemaVersion != LegacySchemaVersion) || timeline.ArtifactType != ArtifactType {
		return fmt.Errorf("unsupported benchmark phase timeline schema or artifact type")
	}
	var rebuilt Timeline
	var err error
	if timeline.SchemaVersion == SchemaVersion {
		rebuilt, err = BuildForRun(timeline.RunID, timeline.Trial, timeline.Events)
	} else if timeline.SchemaVersion == BoundLegacySchemaVersion {
		if !validRunID(timeline.RunID) {
			return fmt.Errorf("legacy bound benchmark phase timeline has an invalid run id")
		}
		rebuilt, err = build(BoundLegacySchemaVersion, timeline.RunID, timeline.Trial, timeline.Events, legacyOrderedNames)
	} else {
		if timeline.RunID != "" {
			return fmt.Errorf("legacy benchmark phase timeline unexpectedly has a run id")
		}
		rebuilt, err = Build(timeline.Trial, timeline.Events)
	}
	if err != nil {
		return err
	}
	if rebuilt.StartedAt != timeline.StartedAt || rebuilt.FinishedAt != timeline.FinishedAt || rebuilt.DurationNS != timeline.DurationNS || rebuilt.DurationMS != timeline.DurationMS || rebuilt.Status != timeline.Status {
		return fmt.Errorf("benchmark phase timeline aggregate does not match events")
	}
	if rebuilt.Digest != timeline.Digest {
		return fmt.Errorf("benchmark phase timeline digest mismatch")
	}
	return nil
}

func validRunID(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for index, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (index > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

func required(name string) bool {
	switch name {
	case PreflightName, PrepareName, MeasureName, ValidateName, CollectName, CleanupName:
		return true
	default:
		return false
	}
}

// EventByName returns the uniquely named phase from a validated lifecycle.
// Callers should still Verify the timeline before treating the event as
// evidence; this helper only removes brittle numeric phase indexing.
func EventByName(timeline Timeline, name string) (Event, bool) {
	for _, event := range timeline.Events {
		if event.Name == name {
			return event, true
		}
	}
	return Event{}, false
}

func digestTimeline(timeline Timeline) (string, error) {
	timeline.Digest = ""
	content, err := json.Marshal(timeline)
	if err != nil {
		return "", fmt.Errorf("marshal benchmark phase timeline: %w", err)
	}
	return evidence.DigestBytes(content), nil
}

func JSON(timeline Timeline) ([]byte, error) {
	encoded, err := json.MarshalIndent(timeline, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// SortEvents is intentionally not used by Build: journals must already be in
// execution order. It is exported only for deterministic test/probe assembly.
func SortEvents(events []Event) {
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
}
