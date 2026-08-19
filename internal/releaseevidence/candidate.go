package releaseevidence

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/releaseassets"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasemanifest"
)

// CandidateInitOptions describes the two independently inspected inputs used
// to create revision zero. No candidate identity field is accepted as free
// text: it is derived from the verified release manifest and asset inventory.
type CandidateInitOptions struct {
	ReleaseManifestPath string
	AssetInventoryPath  string
	Output              string
}

// CandidateInitResult reports the immutable revision written by
// InitializeCandidate. AssetVerification proves local byte/content binding;
// it does not authenticate GitHub, Sigstore, or the inventory producer.
type CandidateInitResult struct {
	Output            string                     `json:"output"`
	Digest            string                     `json:"digest"`
	ReleaseState      string                     `json:"release_state"`
	Candidate         Candidate                  `json:"candidate"`
	AssetVerification releaseassets.Verification `json:"asset_verification"`
	IndexVerification Verification               `json:"index_verification"`
}

// InitializeCandidate verifies a downloaded release directory and a typed
// provider asset inventory, derives one distribution identity, then writes an
// active v3 evidence index with every readiness requirement still open.
func InitializeCandidate(options CandidateInitOptions) (result CandidateInitResult, returnErr error) {
	if options.ReleaseManifestPath == "" {
		return CandidateInitResult{}, fmt.Errorf("release manifest path is required")
	}
	if options.AssetInventoryPath == "" {
		return CandidateInitResult{}, fmt.Errorf("asset inventory path is required")
	}
	if options.Output == "" {
		return CandidateInitResult{}, fmt.Errorf("output path is required")
	}
	releaseDir := filepath.Dir(options.ReleaseManifestPath)
	output, err := pathguard.ResolveOutputOutside(releaseDir, options.Output)
	if err != nil {
		return CandidateInitResult{}, fmt.Errorf("resolve candidate index output outside release assets: %w", err)
	}

	inventory, err := releaseassets.LoadFile(options.AssetInventoryPath)
	if err != nil {
		return CandidateInitResult{}, fmt.Errorf("load release asset inventory: %w", err)
	}
	snapshot, err := releaseassets.CreateVerifiedSnapshot(releaseDir, inventory)
	if err != nil {
		return CandidateInitResult{}, fmt.Errorf("snapshot release asset directory: %w", err)
	}
	defer func() {
		if snapshot == nil {
			return
		}
		if cleanupErr := snapshot.Close(); cleanupErr != nil {
			cleanupErr = fmt.Errorf("remove verified release asset snapshot: %w", cleanupErr)
			result = CandidateInitResult{}
			returnErr = errors.Join(returnErr, cleanupErr)
		}
	}()
	manifestBasename := filepath.Base(options.ReleaseManifestPath)
	manifest, err := releasemanifest.VerifyPath(filepath.Join(snapshot.Root(), manifestBasename))
	if err != nil {
		return CandidateInitResult{}, fmt.Errorf("verify snapshotted release manifest: %w", err)
	}
	assetVerification, err := releaseassets.VerifyDirectory(
		snapshot.Root(),
		inventory,
		manifest,
		manifestBasename,
	)
	if err != nil {
		return CandidateInitResult{}, fmt.Errorf("verify release asset directory: %w", err)
	}
	if !assetVerification.Valid {
		return CandidateInitResult{}, fmt.Errorf("verify release asset directory: %s", joinIssues(assetVerification.Issues))
	}

	candidate, err := CandidateFromArtifacts(manifest, inventory)
	if err != nil {
		return CandidateInitResult{}, err
	}
	index, err := NewIndex(candidate, inventory.CapturedAt)
	if err != nil {
		return CandidateInitResult{}, err
	}
	if err := snapshot.Close(); err != nil {
		snapshot = nil
		return CandidateInitResult{}, fmt.Errorf("remove verified release asset snapshot before publishing index: %w", err)
	}
	snapshot = nil
	written, err := WriteNewOutside(releaseDir, output, index)
	result = CandidateInitResult{
		Output:            written.Output,
		Digest:            written.Digest,
		ReleaseState:      inventory.ReleaseState,
		Candidate:         candidate,
		AssetVerification: assetVerification,
		IndexVerification: written.Verification,
	}
	if err != nil {
		// WriteNew returns a populated result together with CommittedError when
		// the immutable destination exists but final durability confirmation
		// failed. Preserve that identity so library callers can recover without
		// blindly retrying a path which must now reject replacement.
		return result, err
	}
	return result, nil
}

// CandidateFromArtifacts derives the complete distribution identity without
// accepting independent version, tag, commit, pack, or fingerprint values.
func CandidateFromArtifacts(manifest releasemanifest.Manifest, inventory releaseassets.Inventory) (Candidate, error) {
	if err := releasemanifest.Validate(manifest); err != nil {
		return Candidate{}, fmt.Errorf("validate release manifest: %w", err)
	}
	assetVerification := releaseassets.Verify(inventory)
	if !assetVerification.Valid {
		return Candidate{}, fmt.Errorf("validate release asset inventory: %s", joinIssues(assetVerification.Issues))
	}
	if inventory.Tag != "v"+manifest.Version {
		return Candidate{}, fmt.Errorf("asset inventory tag %q does not match release manifest version %q", inventory.Tag, manifest.Version)
	}
	if inventory.GitCommit != manifest.GitCommit {
		return Candidate{}, fmt.Errorf("asset inventory commit %q does not match release manifest commit %q", inventory.GitCommit, manifest.GitCommit)
	}
	manifestTime, err := time.Parse(time.RFC3339, manifest.GeneratedAt)
	if err != nil {
		return Candidate{}, fmt.Errorf("parse release manifest generated_at: %w", err)
	}
	inventoryTime, err := time.Parse(time.RFC3339Nano, inventory.CapturedAt)
	if err != nil {
		return Candidate{}, fmt.Errorf("parse asset inventory captured_at: %w", err)
	}
	if inventoryTime.Before(manifestTime) {
		return Candidate{}, fmt.Errorf("asset inventory captured_at must not precede release manifest generated_at")
	}

	return Candidate{
		Version:          manifest.Version,
		Tag:              inventory.Tag,
		GitCommit:        manifest.GitCommit,
		AssetFingerprint: inventory.AssetFingerprint,
		ScenarioPack: ScenarioPack{
			ID:      manifest.ScenarioPack.ID,
			Version: manifest.ScenarioPack.Version,
			Digest:  manifest.ScenarioPack.Digest,
		},
	}, nil
}

// NewIndex creates a semantically valid, authorization-safe revision zero.
// It is intentionally active/no-go; candidate initialization closes no gate.
func NewIndex(candidate Candidate, createdAt string) (Index, error) {
	index := Index{
		SchemaVersion:      SchemaVersionV3,
		ArtifactType:       ArtifactType,
		Lineage:            &Lineage{Revision: 0},
		RecordStatus:       RecordStatusActive,
		CreatedAt:          createdAt,
		Candidate:          candidate,
		PreventiveControls: canonicalOpenPreventiveControls(),
		Gates:              openGates(),
		Decision:           Decision{Scope: DecisionScope, Status: DecisionNoGo, RecordedAt: createdAt, Reasons: []string{"Readiness requirements remain open."}},
	}
	verification := Verify(index)
	if !verification.Valid {
		return Index{}, fmt.Errorf("derive release evidence index: %s", joinIssues(verification.Issues))
	}
	return index, nil
}

func canonicalOpenPreventiveControls() PreventiveControls {
	return PreventiveControls{
		TagRuleset: TagRuleset{
			Status:             ControlStatusOpen,
			Target:             "tag",
			Enforcement:        "active",
			IncludePattern:     "refs/tags/v*",
			Excludes:           []string{},
			CreationRestricted: boolPointer(true),
			UpdateProhibited:   boolPointer(true),
			DeletionProhibited: boolPointer(true),
			BypassReview:       AdminReview{Status: ReviewStatusOpen},
		},
		ImmutableReleases: ImmutableReleases{
			Status:  ControlStatusOpen,
			Enabled: boolPointer(false),
		},
	}
}

func openGates() Gates {
	open := func() Gate { return Gate{Status: GateStatusOpen} }
	return Gates{
		SourceCompatibility:              open(),
		AggregateAttempt1:                open(),
		AggregateAttempt2:                open(),
		DraftAssetVerification:           open(),
		DraftCompatibility7Cells:         open(),
		DraftExternalDrivers:             open(),
		Publication:                      open(),
		PublicAssetVerification:          open(),
		PublishedCompatibility7Cells:     open(),
		CriticalFindingReview:            open(),
		AdoptionPilot1:                   open(),
		AdoptionPilot2:                   open(),
		IndependentAuthoringReproduction: open(),
	}
}

func boolPointer(value bool) *bool {
	return &value
}

// GateNames returns the stable set of readiness gate names. A gate becomes
// attachable only when a closed typed adapter is implemented for its producer
// record. Preventive controls intentionally use separate typed adapters.
func GateNames() []string {
	names := make([]string, 0, len(gateRequirements(Gates{})))
	for _, item := range gateRequirements(Gates{}) {
		names = append(names, item.name)
	}
	sort.Strings(names)
	return names
}
