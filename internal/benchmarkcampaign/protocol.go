package benchmarkcampaign

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func BuildProtocol(catalog speccatalog.Catalog, campaignID, runtimeName, subject string, inputs []string) (Protocol, []benchmarkplan.Plan, error) {
	if !benchmarkrun.ValidRunID(campaignID) {
		return Protocol{}, nil, fmt.Errorf("invalid benchmark campaign id %q", campaignID)
	}
	if runtimeName != "docker" && runtimeName != "native" {
		return Protocol{}, nil, fmt.Errorf("unsupported runtime %q: expected docker or native", runtimeName)
	}
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > 256 || strings.ContainsAny(subject, "\r\n") {
		return Protocol{}, nil, fmt.Errorf("benchmark campaign subject must be a non-empty single line of at most 256 bytes")
	}
	if len(inputs) == 0 {
		return Protocol{}, nil, fmt.Errorf("benchmark campaign requires at least one benchmark spec")
	}
	if len(inputs) > 1000 {
		return Protocol{}, nil, fmt.Errorf("benchmark campaign accepts at most 1000 benchmark specs")
	}

	plans := make([]benchmarkplan.Plan, 0, len(inputs))
	ordered := make([]PlannedSeries, 0, len(inputs))
	for index, input := range inputs {
		plan, err := benchmarkplan.Build(catalog, input)
		if err != nil {
			return Protocol{}, nil, fmt.Errorf("build campaign benchmark %d %q: %w", index+1, input, err)
		}
		runID := fmt.Sprintf("%s-s%03d", campaignID, index+1)
		if !benchmarkrun.ValidRunID(runID) {
			return Protocol{}, nil, fmt.Errorf("campaign id %q cannot derive a valid series run id", campaignID)
		}
		plans = append(plans, plan)
		ordered = append(ordered, PlannedSeries{
			Position:            index + 1,
			Benchmark:           plan.Spec,
			SeriesRunID:         runID,
			SpecRef:             filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(plan.Spec)+".env")),
			SpecDigest:          plan.SpecDigest,
			ProtocolDigest:      plan.ProtocolDigest,
			ComparisonKeyDigest: plan.ComparisonKeyDigest,
			Class:               plan.Class,
			PrimaryMetric:       plan.PrimaryMetric,
			Direction:           plan.Direction,
		})
	}
	protocol := Protocol{
		SchemaVersion:    ProtocolSchemaVersion,
		ArtifactType:     ProtocolArtifactType,
		SchedulerVersion: SchedulerVersion,
		Design:           AnalysisDesign,
		CampaignID:       campaignID,
		Runtime:          runtimeName,
		Subject:          subject,
		OrderedSeries:    ordered,
	}
	digest, err := protocolDigest(protocol)
	if err != nil {
		return Protocol{}, nil, err
	}
	protocol.Digest = digest
	return protocol, plans, nil
}

func validateProtocol(protocol Protocol) error {
	if protocol.SchemaVersion != ProtocolSchemaVersion || protocol.ArtifactType != ProtocolArtifactType || protocol.SchedulerVersion != SchedulerVersion || protocol.Design != AnalysisDesign {
		return fmt.Errorf("unsupported campaign protocol schema, artifact type, scheduler, or design")
	}
	if !benchmarkrun.ValidRunID(protocol.CampaignID) {
		return fmt.Errorf("invalid campaign id")
	}
	if protocol.Runtime != "docker" && protocol.Runtime != "native" {
		return fmt.Errorf("invalid campaign runtime")
	}
	if strings.TrimSpace(protocol.Subject) != protocol.Subject || protocol.Subject == "" || len(protocol.Subject) > 256 || strings.ContainsAny(protocol.Subject, "\r\n") {
		return fmt.Errorf("invalid campaign subject")
	}
	if len(protocol.OrderedSeries) == 0 || len(protocol.OrderedSeries) > 1000 {
		return fmt.Errorf("campaign protocol must contain 1 to 1000 ordered series")
	}
	seenRunIDs := make(map[string]struct{}, len(protocol.OrderedSeries))
	for index, item := range protocol.OrderedSeries {
		wantPosition := index + 1
		wantRunID := fmt.Sprintf("%s-s%03d", protocol.CampaignID, wantPosition)
		if item.Position != wantPosition || item.SeriesRunID != wantRunID || !benchmarkrun.ValidRunID(item.SeriesRunID) {
			return fmt.Errorf("campaign protocol item %d has invalid ordered identity", wantPosition)
		}
		if _, exists := seenRunIDs[item.SeriesRunID]; exists {
			return fmt.Errorf("campaign protocol contains duplicate series run id")
		}
		seenRunIDs[item.SeriesRunID] = struct{}{}
		if strings.TrimSpace(item.Benchmark) == "" || item.SpecRef != filepath.ToSlash(filepath.Join("benchmarks", filepath.FromSlash(item.Benchmark)+".env")) || !evidence.IsPortablePath(item.SpecRef) {
			return fmt.Errorf("campaign protocol item %d has invalid benchmark reference", wantPosition)
		}
		if !evidence.IsDigest(item.SpecDigest) || !evidence.IsDigest(item.ProtocolDigest) || !evidence.IsDigest(item.ComparisonKeyDigest) {
			return fmt.Errorf("campaign protocol item %d has invalid identity digest", wantPosition)
		}
		if item.Class != "smoke" && item.Class != "measurement" {
			return fmt.Errorf("campaign protocol item %d has invalid class", wantPosition)
		}
		if item.PrimaryMetric == "" || (item.Direction != "higher" && item.Direction != "lower") {
			return fmt.Errorf("campaign protocol item %d has invalid metric contract", wantPosition)
		}
	}
	digest, err := protocolDigest(protocol)
	if err != nil {
		return err
	}
	if digest != protocol.Digest {
		return fmt.Errorf("campaign protocol digest mismatch")
	}
	return nil
}

func protocolDigest(protocol Protocol) (string, error) {
	protocol.Digest = ""
	content, err := json.Marshal(protocol)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}
