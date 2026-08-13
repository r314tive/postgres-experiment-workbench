// Package benchmarkcampaign executes an immutable ordered set of independent
// benchmark protocols. A campaign is an orchestration and presentation layer,
// not a comparison design: its rows may intentionally be non-comparable and it
// never emits an aggregate score, winner, regression verdict, or causal claim.
package benchmarkcampaign

import (
	"io"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

const (
	ProtocolSchemaVersion  = "pgworkbench.benchmark-campaign-protocol/v1"
	ProtocolArtifactType   = "pgworkbench.benchmark-campaign-protocol"
	ExecutionSchemaVersion = "pgworkbench.benchmark-campaign-execution/v1"
	ExecutionArtifactType  = "pgworkbench.benchmark-campaign-execution"
	RunSchemaVersion       = "pgworkbench.benchmark-campaign-run/v1"
	RunArtifactType        = "pgworkbench.benchmark-campaign-run"
	SchedulerVersion       = "1.0.0"
	AnalysisDesign         = "ordered-independent-series-campaign"
)

type PlannedSeries struct {
	Position            int    `json:"position"`
	Benchmark           string `json:"benchmark"`
	SeriesRunID         string `json:"series_run_id"`
	SpecRef             string `json:"spec_ref"`
	SpecDigest          string `json:"spec_digest"`
	ProtocolDigest      string `json:"protocol_digest"`
	ComparisonKeyDigest string `json:"comparison_key_digest"`
	Class               string `json:"class"`
	PrimaryMetric       string `json:"primary_metric"`
	Direction           string `json:"direction"`
}

type Protocol struct {
	SchemaVersion    string          `json:"schema_version"`
	ArtifactType     string          `json:"artifact_type"`
	SchedulerVersion string          `json:"scheduler_version"`
	Design           string          `json:"design"`
	CampaignID       string          `json:"campaign_id"`
	Runtime          string          `json:"runtime"`
	Subject          string          `json:"subject"`
	OrderedSeries    []PlannedSeries `json:"ordered_series"`
	Digest           string          `json:"digest"`
}

type FileRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// Execution is written immediately after its series attempt. Verified rows
// bind to an independently verifiable benchmark series. An unavailable row is
// an honest orchestration record and deliberately carries no performance value.
type Execution struct {
	SchemaVersion       string   `json:"schema_version"`
	ArtifactType        string   `json:"artifact_type"`
	Position            int      `json:"position"`
	Benchmark           string   `json:"benchmark"`
	SeriesRunID         string   `json:"series_run_id"`
	SeriesRef           string   `json:"series_ref,omitempty"`
	ResultDigest        string   `json:"result_digest,omitempty"`
	SpecDigest          string   `json:"spec_digest"`
	ProtocolDigest      string   `json:"protocol_digest"`
	ComparisonKeyDigest string   `json:"comparison_key_digest"`
	Class               string   `json:"class"`
	PrimaryMetric       string   `json:"primary_metric"`
	Direction           string   `json:"direction"`
	StartedAt           string   `json:"started_at,omitempty"`
	FinishedAt          string   `json:"finished_at,omitempty"`
	Status              string   `json:"status"`
	EvidenceStatus      string   `json:"evidence_status"`
	TrialsPlanned       int      `json:"trials_planned"`
	TrialsValid         int      `json:"trials_valid"`
	TrialsFailed        int      `json:"trials_failed"`
	TrialsInvalid       int      `json:"trials_invalid"`
	Median              *float64 `json:"median,omitempty"`
	CVPct               *float64 `json:"cv_pct,omitempty"`
	Reasons             []string `json:"reasons"`
	Digest              string   `json:"digest"`
}

type Result struct {
	SchemaVersion    string      `json:"schema_version"`
	ArtifactType     string      `json:"artifact_type"`
	SchedulerVersion string      `json:"scheduler_version"`
	Design           string      `json:"design"`
	CampaignID       string      `json:"campaign_id"`
	RunDir           string      `json:"run_dir"`
	Protocol         FileRef     `json:"protocol"`
	Runtime          string      `json:"runtime"`
	Subject          string      `json:"subject"`
	StartedAt        string      `json:"started_at"`
	FinishedAt       string      `json:"finished_at"`
	Status           string      `json:"status"`
	Conclusion       string      `json:"conclusion"`
	Decision         string      `json:"decision"`
	Reasons          []string    `json:"reasons"`
	Executions       []Execution `json:"executions"`
	Digest           string      `json:"digest"`
	ArtifactDir      string      `json:"-"`
}

type VerifyResult struct {
	Dir      string    `json:"dir"`
	Valid    bool      `json:"valid"`
	Issues   []string  `json:"issues"`
	Campaign *Result   `json:"campaign,omitempty"`
	Protocol *Protocol `json:"protocol,omitempty"`
}

func (result VerifyResult) IsValid() bool { return len(result.Issues) == 0 }

type SeriesRunner func(root string, catalog speccatalog.Catalog, plan benchmarkplan.Plan, options benchmarkrun.Options) (benchmarkrun.Series, error)

type Options struct {
	Runtime       string
	CampaignID    string
	Subject       string
	SeriesOptions benchmarkrun.Options
	RunSeries     SeriesRunner
	Now           func() time.Time
	Getenv        func(string) string
	Stdout        io.Writer
	Stderr        io.Writer
}
