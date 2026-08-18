package releaseevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/r314tive/postgres-experiment-workbench/internal/strictjson"
)

const (
	maxGateRecordBytes = 64 << 10
)

type GateAttachOptions struct {
	IndexPath    string
	Gate         string
	EvidenceFile string
	EvidenceRef  string
	Output       string
}

type GateAttachResult struct {
	Output               string       `json:"output"`
	Digest               string       `json:"digest"`
	PreviousIndexDigest  string       `json:"previous_index_digest"`
	Revision             int64        `json:"revision"`
	Gate                 string       `json:"gate"`
	GateStatus           string       `json:"gate_status"`
	EvidenceDigest       string       `json:"evidence_digest"`
	EvidenceDurability   string       `json:"evidence_durability"`
	EvidenceAuthenticity string       `json:"evidence_authenticity"`
	RecordSchemaVersion  string       `json:"record_schema_version"`
	RecordArtifactType   string       `json:"record_artifact_type"`
	RecordAdapter        string       `json:"record_adapter"`
	IndexVerification    Verification `json:"index_verification"`
}

type artifactHeader struct {
	SchemaVersion string
	ArtifactType  string
}

type derivedGateAttachment struct {
	Gate          string
	Status        string
	CapturedAt    string
	RunID         *string
	RunAttempt    *int64
	SchemaVersion string
	ArtifactType  string
	Adapter       string
}

type gateAttachHooks struct {
	beforePublication func()
}

type pinnedAttachmentChain struct {
	directory       *os.File
	predecessor     *os.File
	predecessorInfo os.FileInfo
	predecessorName string
	outputName      string
	displayOutput   string
	indexParent     string
	outputParent    string
}

// AttachGate consumes one exact predecessor byte snapshot and one exact typed
// record byte snapshot. A closed adapter derives both the only legal gate and
// its outcome; callers can assert the gate name but cannot supply a status.
// The new revision is exclusively published at the canonical adjacent
// index-r<N+1>.json path so concurrent local writers have one destination.
func AttachGate(options GateAttachOptions) (GateAttachResult, error) {
	return attachGate(options, gateAttachHooks{})
}

func attachGate(options GateAttachOptions, hooks gateAttachHooks) (GateAttachResult, error) {
	if options.IndexPath == "" || options.Gate == "" || options.EvidenceFile == "" || options.EvidenceRef == "" || options.Output == "" {
		return GateAttachResult{}, fmt.Errorf("index, gate, evidence file, evidence ref, and output are required")
	}
	if !validDurableRef(options.EvidenceRef) {
		return GateAttachResult{}, fmt.Errorf("evidence ref must be an absolute durable https, s3, gs, or urn URI; durability and authenticity are operator assertions")
	}

	chain, err := openPinnedAttachmentChain(options.IndexPath, options.Output)
	if err != nil {
		return GateAttachResult{}, err
	}
	defer chain.close()

	indexBytes, err := strictjson.ReadOpenedFile(chain.predecessor, maxIndexBytes)
	if err != nil {
		return GateAttachResult{}, fmt.Errorf("read predecessor release evidence index: %w", err)
	}
	chain.predecessorInfo, err = chain.predecessor.Stat()
	if err != nil {
		return GateAttachResult{}, fmt.Errorf("inspect pinned predecessor release evidence index: %w", err)
	}
	index, err := Parse(indexBytes)
	if err != nil {
		return GateAttachResult{}, fmt.Errorf("parse predecessor release evidence index: %w", err)
	}
	verification := Verify(index)
	if !verification.Valid {
		return GateAttachResult{}, fmt.Errorf("predecessor release evidence index is invalid: %s", joinIssues(verification.Issues))
	}
	if !oneOf(index.SchemaVersion, SchemaVersionV2, SchemaVersionV3) || index.Lineage == nil {
		return GateAttachResult{}, fmt.Errorf("gate attachment requires a v2 or v3 predecessor with lineage")
	}
	if index.RecordStatus != RecordStatusActive {
		return GateAttachResult{}, fmt.Errorf("gate attachment requires an active predecessor, got %q", index.RecordStatus)
	}
	if index.Lineage.Revision >= maxJSONSafeInteger {
		return GateAttachResult{}, fmt.Errorf("predecessor lineage revision cannot be incremented safely")
	}

	evidenceBytes, err := strictjson.ReadFile(options.EvidenceFile, maxGateRecordBytes)
	if err != nil {
		return GateAttachResult{}, fmt.Errorf("read gate evidence record: %w", err)
	}
	header, err := inspectArtifactHeader(evidenceBytes)
	if err != nil {
		return GateAttachResult{}, fmt.Errorf("inspect gate evidence record: %w", err)
	}
	derived, err := adaptGateRecord(evidenceBytes, header, index)
	if err != nil {
		return GateAttachResult{}, fmt.Errorf("verify gate evidence record: %w", err)
	}
	if options.Gate != derived.Gate {
		return GateAttachResult{}, fmt.Errorf("--gate %q does not match typed adapter gate %q", options.Gate, derived.Gate)
	}
	if captured, ok := parseDateTime(derived.CapturedAt); ok {
		if created, createdOK := parseDateTime(index.CreatedAt); createdOK && captured.Before(created) {
			return GateAttachResult{}, fmt.Errorf("gate evidence captured_at must not precede the candidate index created_at")
		}
	}

	targetGate, err := gatePointer(&index.Gates, derived.Gate)
	if err != nil {
		return GateAttachResult{}, err
	}
	if targetGate.Status != GateStatusOpen || targetGate.Evidence != nil {
		return GateAttachResult{}, fmt.Errorf("gate %q is not open; attachment does not supersede existing outcomes", derived.Gate)
	}
	target, err := canonicalNextOutput(chain.predecessorName, chain.outputName, chain.displayOutput, index.Lineage.Revision)
	if err != nil {
		return GateAttachResult{}, err
	}

	beforeCandidate := index.Candidate
	beforeControls := index.PreventiveControls
	beforeGates := index.Gates
	previousDigest := digestExactBytes(indexBytes)
	evidenceDigest := digestExactBytes(evidenceBytes)
	*targetGate = Gate{
		Status: derived.Status,
		Evidence: &Evidence{
			Ref:        options.EvidenceRef,
			Digest:     evidenceDigest,
			CapturedAt: derived.CapturedAt,
			RunID:      derived.RunID,
			RunAttempt: derived.RunAttempt,
			Record: &EvidenceRecord{
				SchemaVersion: derived.SchemaVersion,
				ArtifactType:  derived.ArtifactType,
				Adapter:       derived.Adapter,
			},
			Assurance: &EvidenceAssurance{
				Durability:   EvidenceDurabilityAsserted,
				Authenticity: EvidenceAuthenticityUnverified,
			},
		},
	}
	// A v2 predecessor is migrated by derivation, never rewritten. Missing
	// trust metadata on inherited evidence remains visible and unqualified in
	// the v3 successor.
	index.SchemaVersion = SchemaVersionV3
	index.Lineage = &Lineage{Revision: index.Lineage.Revision + 1, PreviousIndexDigest: &previousDigest}
	if err := finalizeDerivedDecision(&index, derived.CapturedAt); err != nil {
		return GateAttachResult{}, err
	}
	if index.Candidate != beforeCandidate || !reflect.DeepEqual(index.PreventiveControls, beforeControls) || !onlyGateChanged(beforeGates, index.Gates, derived.Gate) {
		return GateAttachResult{}, fmt.Errorf("internal gate adapter changed fields outside its authorized gate and derived lifecycle metadata")
	}

	if hooks.beforePublication != nil {
		hooks.beforePublication()
	}
	written, writeErr := WriteNewAt(chain.directory, chain.outputName, target, index)
	writeErr = chain.finishWrite(written, writeErr, indexBytes)
	result := GateAttachResult{
		Output:               written.Output,
		Digest:               written.Digest,
		PreviousIndexDigest:  previousDigest,
		Revision:             index.Lineage.Revision,
		Gate:                 derived.Gate,
		GateStatus:           derived.Status,
		EvidenceDigest:       evidenceDigest,
		EvidenceDurability:   targetGate.Evidence.Assurance.Durability,
		EvidenceAuthenticity: targetGate.Evidence.Assurance.Authenticity,
		RecordSchemaVersion:  targetGate.Evidence.Record.SchemaVersion,
		RecordArtifactType:   targetGate.Evidence.Record.ArtifactType,
		RecordAdapter:        targetGate.Evidence.Record.Adapter,
		IndexVerification:    written.Verification,
	}
	return result, writeErr
}

func openPinnedAttachmentChain(indexPath, output string) (*pinnedAttachmentChain, error) {
	indexAbs, err := filepath.Abs(indexPath)
	if err != nil {
		return nil, fmt.Errorf("resolve predecessor index path: %w", err)
	}
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}
	indexParent := filepath.Dir(indexAbs)
	outputParent := filepath.Dir(outputAbs)
	directory, err := openDirectoryPath(indexParent)
	if err != nil {
		return nil, fmt.Errorf("open predecessor index directory: %w", err)
	}
	keepDirectory := false
	defer func() {
		if !keepDirectory {
			_ = directory.Close()
		}
	}()
	directoryInfo, err := directory.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect predecessor index directory: %w", err)
	}
	if !directoryInfo.IsDir() {
		return nil, fmt.Errorf("predecessor index parent is not a directory")
	}
	canonicalParent, err := filepath.EvalSymlinks(indexParent)
	if err != nil {
		return nil, fmt.Errorf("resolve predecessor index directory: %w", err)
	}
	canonicalDirectory, err := openDirectoryPath(canonicalParent)
	if err != nil {
		return nil, fmt.Errorf("open resolved predecessor index directory: %w", err)
	}
	canonicalInfo, inspectCanonicalErr := canonicalDirectory.Stat()
	closeCanonicalErr := canonicalDirectory.Close()
	if inspectCanonicalErr != nil {
		return nil, fmt.Errorf("inspect resolved predecessor index directory: %w", inspectCanonicalErr)
	}
	if closeCanonicalErr != nil {
		return nil, fmt.Errorf("close resolved predecessor index directory identity check: %w", closeCanonicalErr)
	}
	if !canonicalInfo.IsDir() || !os.SameFile(directoryInfo, canonicalInfo) {
		return nil, fmt.Errorf("resolved predecessor index directory changed while it was being pinned")
	}

	outputDirectory, err := openDirectoryPath(outputParent)
	if err != nil {
		return nil, fmt.Errorf("open output directory: %w", err)
	}
	outputInfo, inspectErr := outputDirectory.Stat()
	closeErr := outputDirectory.Close()
	if inspectErr != nil {
		return nil, fmt.Errorf("inspect output directory: %w", inspectErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close output directory identity check: %w", closeErr)
	}
	if !outputInfo.IsDir() || !os.SameFile(directoryInfo, outputInfo) {
		return nil, fmt.Errorf("output must be in the predecessor index directory")
	}

	predecessorName := filepath.Base(indexAbs)
	predecessor, err := openReadOnlyEntryAt(directory, predecessorName, "predecessor release evidence index")
	if err != nil {
		return nil, err
	}
	keepDirectory = true
	return &pinnedAttachmentChain{
		directory:       directory,
		predecessor:     predecessor,
		predecessorName: predecessorName,
		outputName:      filepath.Base(outputAbs),
		displayOutput:   outputAbs,
		indexParent:     indexParent,
		outputParent:    outputParent,
	}, nil
}

func (chain *pinnedAttachmentChain) finishWrite(written WriteResult, writeErr error, predecessorBytes []byte) error {
	var committed *CommittedError
	committedWrite := writeErr == nil || errors.As(writeErr, &committed)
	var confirmationErr error
	if committedWrite {
		confirmationErr = errors.Join(
			chain.confirmPredecessor(predecessorBytes),
			confirmPinnedDirectoryPath(chain.directory, chain.indexParent, "predecessor index directory"),
			confirmPinnedDirectoryPath(chain.directory, chain.outputParent, "output directory"),
		)
	}
	closeErr := chain.closePredecessor()
	if closeErr != nil {
		closeErr = fmt.Errorf("close pinned predecessor release evidence index: %w", closeErr)
	}
	postCommitErr := errors.Join(confirmationErr, closeErr)
	if !committedWrite {
		return errors.Join(writeErr, closeErr)
	}
	if writeErr == nil && postCommitErr == nil {
		return nil
	}
	if committed != nil {
		postCommitErr = errors.Join(committed.Err, postCommitErr)
	}
	return &CommittedError{Result: written, Err: postCommitErr}
}

func (chain *pinnedAttachmentChain) confirmPredecessor(expected []byte) error {
	current, err := openReadOnlyEntryAt(chain.directory, chain.predecessorName, "published predecessor release evidence index")
	if err != nil {
		return fmt.Errorf("confirm predecessor release evidence index: %w", err)
	}
	currentInfo, inspectErr := current.Stat()
	if inspectErr != nil {
		inspectErr = fmt.Errorf("inspect published predecessor release evidence index: %w", inspectErr)
	} else if chain.predecessorInfo == nil || !os.SameFile(chain.predecessorInfo, currentInfo) {
		inspectErr = fmt.Errorf("predecessor release evidence index no longer identifies the parsed and hashed inode")
	}
	var contentErr error
	if inspectErr == nil {
		var content []byte
		content, contentErr = strictjson.ReadOpenedFile(current, maxIndexBytes)
		if contentErr != nil {
			contentErr = fmt.Errorf("read published predecessor release evidence index: %w", contentErr)
		} else if !bytes.Equal(content, expected) {
			contentErr = fmt.Errorf("predecessor release evidence index bytes changed after parsing and hashing")
		}
	}
	closeErr := current.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close predecessor release evidence confirmation: %w", closeErr)
	}
	return errors.Join(inspectErr, contentErr, closeErr)
}

func confirmPinnedDirectoryPath(pinned *os.File, path, description string) error {
	current, err := openDirectoryPath(path)
	if err != nil {
		return fmt.Errorf("reopen %s: %w", description, err)
	}
	pinnedInfo, pinnedErr := pinned.Stat()
	currentInfo, currentErr := current.Stat()
	closeErr := current.Close()
	if pinnedErr != nil {
		pinnedErr = fmt.Errorf("inspect pinned %s: %w", description, pinnedErr)
	}
	if currentErr != nil {
		currentErr = fmt.Errorf("inspect reopened %s: %w", description, currentErr)
	}
	var identityErr error
	if pinnedErr == nil && currentErr == nil && (!currentInfo.IsDir() || !os.SameFile(pinnedInfo, currentInfo)) {
		identityErr = fmt.Errorf("%s path no longer identifies the pinned chain directory", description)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close reopened %s: %w", description, closeErr)
	}
	return errors.Join(pinnedErr, currentErr, identityErr, closeErr)
}

func (chain *pinnedAttachmentChain) closePredecessor() error {
	if chain.predecessor == nil {
		return nil
	}
	err := chain.predecessor.Close()
	chain.predecessor = nil
	return err
}

func (chain *pinnedAttachmentChain) close() {
	_ = chain.closePredecessor()
	if chain.directory != nil {
		_ = chain.directory.Close()
		chain.directory = nil
	}
}

func inspectArtifactHeader(content []byte) (artifactHeader, error) {
	var object map[string]json.RawMessage
	if err := strictjson.Parse(content, &object); err != nil {
		return artifactHeader{}, err
	}
	var header artifactHeader
	for name, destination := range map[string]*string{
		"schema_version": &header.SchemaVersion,
		"artifact_type":  &header.ArtifactType,
	} {
		raw, ok := object[name]
		if !ok {
			return artifactHeader{}, fmt.Errorf("%s is required", name)
		}
		if err := json.Unmarshal(raw, destination); err != nil || *destination == "" {
			return artifactHeader{}, fmt.Errorf("%s must be a non-empty string", name)
		}
	}
	return header, nil
}

func adaptGateRecord(content []byte, header artifactHeader, index Index) (derivedGateAttachment, error) {
	switch {
	case header.SchemaVersion == ExternalDriverVerificationSchema && header.ArtifactType == ExternalDriverVerificationType:
		var record ExternalDriverVerification
		if err := strictjson.Parse(content, &record); err != nil {
			return derivedGateAttachment{}, err
		}
		if err := validateExternalDriverVerification(record, index.Candidate); err != nil {
			return derivedGateAttachment{}, err
		}
		return derivedGateAttachment{
			Gate:          "draft_external_drivers",
			Status:        GateStatusPassed,
			CapturedAt:    record.CapturedAt,
			RunID:         stringPointer(record.WorkflowRun.ID),
			RunAttempt:    int64Pointer(record.WorkflowRun.Attempt),
			SchemaVersion: record.SchemaVersion,
			ArtifactType:  record.ArtifactType,
			Adapter:       ExternalDriverVerificationAdapter,
		}, nil
	case header.SchemaVersion == ReleaseAssetVerificationSchema && header.ArtifactType == ReleaseAssetVerificationType:
		var record ReleaseAssetVerification
		if err := strictjson.Parse(content, &record); err != nil {
			return derivedGateAttachment{}, err
		}
		if err := validateReleaseAssetVerification(record, index.Candidate); err != nil {
			return derivedGateAttachment{}, err
		}
		gate := "draft_asset_verification"
		adapter := ReleaseAssetDraftAdapter
		if record.QualificationMode == releaseQualificationPublished {
			gate = "public_asset_verification"
			adapter = ReleaseAssetPublishedAdapter
		}
		return derivedGateAttachment{
			Gate:          gate,
			Status:        GateStatusPassed,
			CapturedAt:    record.CapturedAt,
			RunID:         stringPointer(record.WorkflowRun.ID),
			RunAttempt:    int64Pointer(record.WorkflowRun.Attempt),
			SchemaVersion: record.SchemaVersion,
			ArtifactType:  record.ArtifactType,
			Adapter:       adapter,
		}, nil
	case header.SchemaVersion == ReleasePublicationSchema && header.ArtifactType == ReleasePublicationType:
		var record ReleasePublicationVerification
		if err := strictjson.Parse(content, &record); err != nil {
			return derivedGateAttachment{}, err
		}
		if err := validateReleasePublicationVerification(record, index.Candidate); err != nil {
			return derivedGateAttachment{}, err
		}
		return derivedGateAttachment{
			Gate:          "publication",
			Status:        GateStatusPassed,
			CapturedAt:    record.CapturedAt,
			RunID:         stringPointer(record.WorkflowRun.ID),
			RunAttempt:    int64Pointer(record.WorkflowRun.Attempt),
			SchemaVersion: record.SchemaVersion,
			ArtifactType:  record.ArtifactType,
			Adapter:       ReleasePublicationAdapter,
		}, nil
	case header.SchemaVersion == CompatibilityVerificationSchema && header.ArtifactType == CompatibilityVerificationType:
		var record CompatibilityVerification
		if err := strictjson.Parse(content, &record); err != nil {
			return derivedGateAttachment{}, err
		}
		if err := validateCompatibilityVerification(record, index.Candidate); err != nil {
			return derivedGateAttachment{}, err
		}
		gate, adapter := "source_compatibility", CompatibilitySourceAdapter
		switch record.QualificationMode {
		case releaseQualificationDraft:
			gate, adapter = "draft_compatibility_7_cells", CompatibilityDraftAdapter
		case releaseQualificationPublished:
			gate, adapter = "published_compatibility_7_cells", CompatibilityPublishedAdapter
		}
		return derivedGateAttachment{Gate: gate, Status: GateStatusPassed, CapturedAt: record.CapturedAt, RunID: stringPointer(record.WorkflowRun.ID), RunAttempt: int64Pointer(record.WorkflowRun.Attempt), SchemaVersion: record.SchemaVersion, ArtifactType: record.ArtifactType, Adapter: adapter}, nil
	case header.SchemaVersion == AggregateVerificationSchema && header.ArtifactType == AggregateVerificationType:
		var record AggregateVerification
		if err := strictjson.Parse(content, &record); err != nil {
			return derivedGateAttachment{}, err
		}
		if err := validateAggregateVerification(record, index.Candidate, index); err != nil {
			return derivedGateAttachment{}, err
		}
		gate, adapter := "aggregate_attempt_1", AggregateAttempt1Adapter
		if record.AggregateAttempt == 2 {
			gate, adapter = "aggregate_attempt_2", AggregateAttempt2Adapter
		}
		return derivedGateAttachment{Gate: gate, Status: GateStatusPassed, CapturedAt: record.CapturedAt, RunID: stringPointer(record.WorkflowRun.ID), RunAttempt: int64Pointer(record.WorkflowRun.Attempt), SchemaVersion: record.SchemaVersion, ArtifactType: record.ArtifactType, Adapter: adapter}, nil
	case header.SchemaVersion == CriticalFindingReviewSchema && header.ArtifactType == CriticalFindingReviewType:
		var record CriticalFindingReview
		if err := strictjson.Parse(content, &record); err != nil {
			return derivedGateAttachment{}, err
		}
		if err := validateCriticalFindingReview(record, index.Candidate); err != nil {
			return derivedGateAttachment{}, err
		}
		status := GateStatusFailed
		if record.Decision.Status == DecisionGo {
			status = GateStatusPassed
		}
		return derivedGateAttachment{Gate: "critical_finding_review", Status: status, CapturedAt: record.Decision.RecordedAt, SchemaVersion: record.SchemaVersion, ArtifactType: record.ArtifactType, Adapter: CriticalFindingReviewAdapter}, nil
	default:
		return derivedGateAttachment{}, fmt.Errorf("unsupported typed gate record %q with schema %q", header.ArtifactType, header.SchemaVersion)
	}
}

func stringPointer(value string) *string { return &value }

func int64Pointer(value int64) *int64 { return &value }

func canonicalNextOutput(indexName, outputName, displayOutput string, currentRevision int64) (string, error) {
	wantInputBase := fmt.Sprintf("index-r%d.json", currentRevision)
	if indexName != wantInputBase {
		return "", fmt.Errorf("predecessor basename must be %q for revision %d", wantInputBase, currentRevision)
	}
	nextRevision := currentRevision + 1
	wantBase := fmt.Sprintf("index-r%d.json", nextRevision)
	if outputName != wantBase {
		return "", fmt.Errorf("output basename must be %q for revision %d", wantBase, nextRevision)
	}
	return displayOutput, nil
}

func gatePointer(gates *Gates, name string) (*Gate, error) {
	switch name {
	case "source_compatibility":
		return &gates.SourceCompatibility, nil
	case "aggregate_attempt_1":
		return &gates.AggregateAttempt1, nil
	case "aggregate_attempt_2":
		return &gates.AggregateAttempt2, nil
	case "draft_asset_verification":
		return &gates.DraftAssetVerification, nil
	case "draft_external_drivers":
		return &gates.DraftExternalDrivers, nil
	case "draft_compatibility_7_cells":
		return &gates.DraftCompatibility7Cells, nil
	case "publication":
		return &gates.Publication, nil
	case "public_asset_verification":
		return &gates.PublicAssetVerification, nil
	case "published_compatibility_7_cells":
		return &gates.PublishedCompatibility7Cells, nil
	case "critical_finding_review":
		return &gates.CriticalFindingReview, nil
	default:
		return nil, fmt.Errorf("gate %q has no typed attachment adapter", name)
	}
}

func onlyGateChanged(before, after Gates, name string) bool {
	beforeTarget, err := gatePointer(&before, name)
	if err != nil {
		return false
	}
	afterTarget, err := gatePointer(&after, name)
	if err != nil {
		return false
	}
	*beforeTarget = *afterTarget
	return reflect.DeepEqual(before, after)
}

func finalizeDerivedDecision(index *Index, recordedAt string) error {
	recordedAt = monotonicDecisionTimestamp(index.Decision.RecordedAt, recordedAt)
	issues := make([]string, 0)
	add := func(format string, args ...any) {
		issues = append(issues, fmt.Sprintf(format, args...))
	}
	states, qualifications, valid := validateReadinessRequirements(add, index.Gates, index.PreventiveControls)
	if !valid {
		return fmt.Errorf("derive attached release evidence readiness: %s", joinIssues(issues))
	}
	assurancePending := len(qualifications) != 0 && aggregateAssuranceStatus(qualifications) != AssuranceAuthorizationEligible
	_, decision := readinessOutcome(true, states, assurancePending)
	index.RecordStatus = RecordStatusActive
	if decision == DecisionGo {
		index.RecordStatus = RecordStatusComplete
	}
	index.Decision = Decision{
		Scope:      DecisionScope,
		Status:     decision,
		RecordedAt: recordedAt,
		Reasons:    []string{"Readiness decision pending independent recomputation."},
	}
	derived := Verify(*index)
	if !derived.Valid {
		return fmt.Errorf("derive attached release evidence decision: %s", joinIssues(derived.Issues))
	}
	index.Decision.Reasons = append([]string(nil), derived.Reasons...)
	final := Verify(*index)
	if !final.Valid {
		return fmt.Errorf("verify attached release evidence decision: %s", joinIssues(final.Issues))
	}
	return nil
}

func monotonicDecisionTimestamp(previous, captured string) string {
	previousTime, previousOK := parseDateTime(previous)
	capturedTime, capturedOK := parseDateTime(captured)
	if previousOK && capturedOK && previousTime.After(capturedTime) {
		return previous
	}
	return captured
}

func digestExactBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}
