package benchmarkab

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcompare"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkqualify"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksettings"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
)

func BuildProtocol(runID, runtimeName, baselineSubject, candidateSubject string, baseline, candidate benchmarkplan.Plan, options Options) (Protocol, error) {
	subjectDimension := options.SubjectDimension
	if subjectDimension == "" {
		subjectDimension = SubjectPGConfig
	}
	if err := validatePlanPair(baseline, candidate, subjectDimension); err != nil {
		return Protocol{}, err
	}
	if !benchmarkqualify.DecisionProfileComplete(options.Qualification.Policy) {
		return Protocol{}, fmt.Errorf("counterbalanced performance decisions require strict memory, storage, load, clocksource, governor, and client-placement qualification gates")
	}
	if options.Qualification.ClientPlacement == "" || options.Qualification.ClientPlacement != options.Qualification.Policy.RequiredClientPlacement {
		return Protocol{}, fmt.Errorf("client placement must be explicit and equal the required client-placement gate")
	}
	if baseline.ClientPlacement != options.Qualification.ClientPlacement || candidate.ClientPlacement != options.Qualification.ClientPlacement {
		return Protocol{}, fmt.Errorf("declared benchmark client placement must equal the qualified client-placement gate")
	}
	if options.Qualification.StorageLabel == "" {
		return Protocol{}, fmt.Errorf("qualification storage label is required")
	}
	if runID == "" || !benchmarkrun.ValidRunID(runID) {
		return Protocol{}, fmt.Errorf("invalid counterbalanced benchmark run id %q", runID)
	}
	if baselineSubject == "" || candidateSubject == "" || baselineSubject == candidateSubject {
		return Protocol{}, fmt.Errorf("baseline and candidate subject labels must be non-empty and distinct")
	}
	if runtimeName != "docker" && runtimeName != "native" {
		return Protocol{}, fmt.Errorf("unsupported runtime %q: expected docker or native", runtimeName)
	}
	var baselineToolchain, candidateToolchain *NativeToolchainIdentity
	if subjectDimension == SubjectNativeToolchain {
		if runtimeName != "native" {
			return Protocol{}, fmt.Errorf("native_toolchain A/B requires the native runtime")
		}
		baselineInstallation, candidateInstallation, err := inspectNativeToolchains(options)
		if err != nil {
			return Protocol{}, err
		}
		if baselineInstallation.Manifest.Digest == candidateInstallation.Manifest.Digest {
			return Protocol{}, fmt.Errorf("native_toolchain A/B requires distinct toolchain byte identities")
		}
		if err := nativetoolchain.RequireComparableVersions(baselineInstallation.Manifest, candidateInstallation.Manifest); err != nil {
			return Protocol{}, fmt.Errorf("native_toolchain A/B version compatibility: %w", err)
		}
		baselineToolchain = toolchainIdentity("baseline", baselineInstallation.Manifest)
		candidateToolchain = toolchainIdentity("candidate", candidateInstallation.Manifest)
	} else if options.BaselineNativeBindir != "" || options.CandidateNativeBindir != "" {
		return Protocol{}, fmt.Errorf("native bindir inputs require native_toolchain subject dimension")
	}
	if options.MaxBookendGapSeconds <= 0 {
		return Protocol{}, fmt.Errorf("maximum qualification bookend gap must be positive")
	}
	policyDigest, err := benchmarkqualify.PolicyDigest(options.Qualification.Policy)
	if err != nil {
		return Protocol{}, err
	}
	effectiveSettingNames, err := benchmarksettings.UnionConfigSettingNames(baseline.PGConfigPath, candidate.PGConfigPath)
	if err != nil {
		return Protocol{}, fmt.Errorf("derive counterbalanced effective pg_settings protocol: %w", err)
	}
	orders := make([]string, baseline.Trials)
	for index := range orders {
		if index%2 == 0 {
			orders[index] = "AB"
		} else {
			orders[index] = "BA"
		}
	}
	minimumUnits := (baseline.MinValidTrials + 1) / 2
	if minimumUnits < benchmarkcompare.MinimumPairedUnits {
		minimumUnits = benchmarkcompare.MinimumPairedUnits
	}
	protocol := Protocol{
		SchemaVersion:       ProtocolSchemaVersion,
		ArtifactType:        ProtocolArtifactType,
		SchedulerVersion:    SchedulerVersion,
		RunID:               runID,
		Runtime:             runtimeName,
		SubjectDimension:    subjectDimension,
		Baseline:            subject("baseline", baselineSubject, baseline, baselineToolchain),
		Candidate:           subject("candidate", candidateSubject, candidate, candidateToolchain),
		ComparisonKeyDigest: baseline.ComparisonKeyDigest,
		BlocksPlanned:       baseline.Trials,
		MinValidUnits:       minimumUnits,
		Orders:              orders,
		PrimaryMetric:       baseline.PrimaryMetric,
		Direction:           baseline.Direction,
		Analysis: AnalysisProtocol{
			BootstrapMethod:    "percentile-cluster-bootstrap-of-block-median",
			BootstrapResamples: options.BootstrapResamples,
			ConfidenceLevel:    options.ConfidenceLevel,
			Seed:               options.Seed,
		},
		Qualification: QualificationProtocol{
			Policy:          options.Qualification.Policy,
			PolicyDigest:    policyDigest,
			StorageLabel:    options.Qualification.StorageLabel,
			ClientPlacement: options.Qualification.ClientPlacement,
		},
		EffectiveSettings: EffectiveSettingsProtocol{
			ParserVersion:             benchmarksettings.ParserVersion,
			SourcePath:                benchmarksettings.SourcePath,
			Names:                     effectiveSettingNames,
			RequireCrossArmDifference: subjectDimension == SubjectPGConfig,
		},
		MaxBookendGapSeconds: options.MaxBookendGapSeconds,
	}
	protocol.RegressionThresholdPct = *baseline.RegressionThresholdPct
	digest, err := protocolDigest(protocol)
	if err != nil {
		return Protocol{}, err
	}
	protocol.Digest = digest
	if err := VerifyProtocol(protocol); err != nil {
		return Protocol{}, err
	}
	return protocol, nil
}

func VerifyProtocol(protocol Protocol) error {
	if protocol.SchemaVersion != ProtocolSchemaVersion || protocol.ArtifactType != ProtocolArtifactType || protocol.SchedulerVersion != SchedulerVersion {
		return fmt.Errorf("unsupported counterbalanced benchmark protocol schema, artifact type, or scheduler")
	}
	if protocol.RunID == "" || !benchmarkrun.ValidRunID(protocol.RunID) {
		return fmt.Errorf("invalid counterbalanced benchmark run id")
	}
	if protocol.Runtime != "docker" && protocol.Runtime != "native" {
		return fmt.Errorf("unsupported protocol runtime")
	}
	if protocol.SubjectDimension != SubjectPGConfig && protocol.SubjectDimension != SubjectNativeToolchain {
		return fmt.Errorf("unsupported counterbalanced subject dimension")
	}
	if protocol.SubjectDimension == SubjectNativeToolchain && protocol.Runtime != "native" {
		return fmt.Errorf("native_toolchain A/B requires native runtime")
	}
	if protocol.Baseline.Role != "baseline" || protocol.Candidate.Role != "candidate" || protocol.Baseline.Subject == protocol.Candidate.Subject {
		return fmt.Errorf("invalid protocol subject roles or labels")
	}
	for _, subject := range []Subject{protocol.Baseline, protocol.Candidate} {
		if subject.Benchmark == "" || subject.Subject == "" || !evidence.IsPortablePath(subject.Benchmark) || !evidence.IsPortablePath(subject.PGConfig) || !evidence.IsDigest(subject.ProtocolDigest) || !evidence.IsDigest(subject.PGConfigDigest) {
			return fmt.Errorf("invalid protocol subject identity")
		}
		if protocol.SubjectDimension == SubjectPGConfig {
			if subject.NativeToolchain != nil {
				return fmt.Errorf("pg_config protocol must not contain native toolchain identity")
			}
		} else if err := verifyToolchainIdentity(subject.Role, subject.NativeToolchain); err != nil {
			return err
		}
	}
	if !evidence.IsDigest(protocol.ComparisonKeyDigest) {
		return fmt.Errorf("protocol comparison identity is invalid")
	}
	if protocol.SubjectDimension == SubjectPGConfig {
		if protocol.Baseline.PGConfig == protocol.Candidate.PGConfig || protocol.Baseline.PGConfigDigest == protocol.Candidate.PGConfigDigest || protocol.Baseline.ProtocolDigest == protocol.Candidate.ProtocolDigest {
			return fmt.Errorf("protocol subjects do not contain one real configuration difference")
		}
	} else {
		if protocol.Baseline.PGConfig != protocol.Candidate.PGConfig || protocol.Baseline.PGConfigDigest != protocol.Candidate.PGConfigDigest || protocol.Baseline.ProtocolDigest != protocol.Candidate.ProtocolDigest || protocol.Baseline.NativeToolchain.Digest == protocol.Candidate.NativeToolchain.Digest {
			return fmt.Errorf("protocol subjects do not contain exactly one real native toolchain difference")
		}
		if protocol.Baseline.NativeToolchain.PostgresVersion != protocol.Candidate.NativeToolchain.PostgresVersion || protocol.Baseline.NativeToolchain.PgbenchVersion != protocol.Candidate.NativeToolchain.PgbenchVersion || protocol.Baseline.NativeToolchain.PsqlVersion != protocol.Candidate.NativeToolchain.PsqlVersion {
			return fmt.Errorf("native toolchain subject versions differ across arms")
		}
	}
	if protocol.BlocksPlanned < 2*benchmarkcompare.MinimumPairedUnits || protocol.BlocksPlanned%2 != 0 || len(protocol.Orders) != protocol.BlocksPlanned {
		return fmt.Errorf("protocol requires an even number of at least %d blocks", 2*benchmarkcompare.MinimumPairedUnits)
	}
	for index, order := range protocol.Orders {
		want := "AB"
		if index%2 == 1 {
			want = "BA"
		}
		if order != want {
			return fmt.Errorf("protocol order %d is %q, want %q", index+1, order, want)
		}
	}
	if protocol.MinValidUnits < benchmarkcompare.MinimumPairedUnits || protocol.MinValidUnits > protocol.BlocksPlanned/2 {
		return fmt.Errorf("invalid minimum valid counterbalance units")
	}
	if protocol.Direction != "higher" && protocol.Direction != "lower" || protocol.PrimaryMetric != "pgbench.tps" && protocol.PrimaryMetric != "pgbench.latency_mean_us" || protocol.RegressionThresholdPct < 0 {
		return fmt.Errorf("invalid protocol metric, direction, or threshold")
	}
	if protocol.Analysis.BootstrapMethod != "percentile-cluster-bootstrap-of-block-median" {
		return fmt.Errorf("unsupported paired bootstrap method")
	}
	paired := benchmarkcompare.AnalyzePaired(nil, benchmarkcompare.PairedOptions{
		Direction:              protocol.Direction,
		RegressionThresholdPct: &protocol.RegressionThresholdPct,
		MinUnits:               protocol.MinValidUnits,
		BootstrapResamples:     protocol.Analysis.BootstrapResamples,
		ConfidenceLevel:        protocol.Analysis.ConfidenceLevel,
		Seed:                   protocol.Analysis.Seed,
	})
	if paired.Status == "invalid" && len(paired.Reasons) > 0 {
		return fmt.Errorf("invalid paired analysis protocol: %v", paired.Reasons)
	}
	if !benchmarkqualify.DecisionProfileComplete(protocol.Qualification.Policy) {
		return fmt.Errorf("qualification decision profile is incomplete")
	}
	policyDigest, err := benchmarkqualify.PolicyDigest(protocol.Qualification.Policy)
	if err != nil || policyDigest != protocol.Qualification.PolicyDigest {
		return fmt.Errorf("qualification policy digest mismatch")
	}
	if protocol.Qualification.ClientPlacement == "" || protocol.Qualification.ClientPlacement != protocol.Qualification.Policy.RequiredClientPlacement || protocol.Qualification.StorageLabel == "" || protocol.MaxBookendGapSeconds <= 0 {
		return fmt.Errorf("invalid qualification protocol")
	}
	if protocol.EffectiveSettings.ParserVersion != benchmarksettings.ParserVersion || protocol.EffectiveSettings.SourcePath != benchmarksettings.SourcePath {
		return fmt.Errorf("unsupported effective pg_settings collection protocol")
	}
	if protocol.EffectiveSettings.RequireCrossArmDifference != (protocol.SubjectDimension == SubjectPGConfig) {
		return fmt.Errorf("effective pg_settings cross-arm gate does not match subject dimension")
	}
	if err := benchmarksettings.ValidateNames(protocol.EffectiveSettings.Names); err != nil {
		return fmt.Errorf("invalid effective pg_settings protocol: %w", err)
	}
	digest, err := protocolDigest(protocol)
	if err != nil || digest != protocol.Digest {
		return fmt.Errorf("counterbalanced benchmark protocol digest mismatch")
	}
	return nil
}

func validatePlanPair(baseline, candidate benchmarkplan.Plan, subjectDimension string) error {
	if baseline.Class != "measurement" || candidate.Class != "measurement" {
		return fmt.Errorf("counterbalanced A/B requires measurement-class benchmark plans")
	}
	if baseline.ComparisonKeyDigest == "" || baseline.ComparisonKeyDigest != candidate.ComparisonKeyDigest {
		return fmt.Errorf("baseline and candidate comparison keys differ")
	}
	switch subjectDimension {
	case SubjectPGConfig:
		if baseline.ProtocolDigest == candidate.ProtocolDigest || baseline.PGConfigDigest == candidate.PGConfigDigest || baseline.PGConfig == candidate.PGConfig {
			return fmt.Errorf("pg_config counterbalanced A/B requires a real PostgreSQL configuration difference")
		}
		if !reflect.DeepEqual(baseline.AllowedSubjectDifferences, []string{SubjectPGConfig}) || !reflect.DeepEqual(candidate.AllowedSubjectDifferences, []string{SubjectPGConfig}) {
			return fmt.Errorf("pg_config counterbalanced A/B permits exactly the pg_config subject dimension")
		}
	case SubjectNativeToolchain:
		if baseline.ProtocolDigest != candidate.ProtocolDigest || baseline.PGConfigDigest != candidate.PGConfigDigest || baseline.PGConfig != candidate.PGConfig {
			return fmt.Errorf("native_toolchain counterbalanced A/B requires identical benchmark and PostgreSQL configuration protocols")
		}
		if !reflect.DeepEqual(baseline.AllowedSubjectDifferences, []string{SubjectNativeToolchain}) || !reflect.DeepEqual(candidate.AllowedSubjectDifferences, []string{SubjectNativeToolchain}) {
			return fmt.Errorf("native_toolchain counterbalanced A/B permits exactly the native_toolchain subject dimension")
		}
	default:
		return fmt.Errorf("unsupported counterbalanced subject dimension %q", subjectDimension)
	}
	if baseline.Trials != candidate.Trials || baseline.Trials < 2*benchmarkcompare.MinimumPairedUnits || baseline.Trials%2 != 0 {
		return fmt.Errorf("baseline and candidate require the same even trial count of at least %d", 2*benchmarkcompare.MinimumPairedUnits)
	}
	if baseline.MinValidTrials != candidate.MinValidTrials {
		return fmt.Errorf("baseline and candidate minimum-valid trial counts differ")
	}
	if baseline.RegressionThresholdPct == nil || candidate.RegressionThresholdPct == nil || *baseline.RegressionThresholdPct != *candidate.RegressionThresholdPct {
		return fmt.Errorf("matching predeclared regression thresholds are required")
	}
	return nil
}

func subject(role, label string, plan benchmarkplan.Plan, toolchain *NativeToolchainIdentity) Subject {
	return Subject{Role: role, Benchmark: plan.Spec, Subject: label, ProtocolDigest: plan.ProtocolDigest, PGConfig: plan.PGConfig, PGConfigDigest: plan.PGConfigDigest, NativeToolchain: toolchain}
}

func inspectNativeToolchains(options Options) (nativetoolchain.Installation, nativetoolchain.Installation, error) {
	baseline, err := nativetoolchain.Inspect(options.BaselineNativeBindir)
	if err != nil {
		return nativetoolchain.Installation{}, nativetoolchain.Installation{}, fmt.Errorf("inspect baseline native toolchain: %w", err)
	}
	candidate, err := nativetoolchain.Inspect(options.CandidateNativeBindir)
	if err != nil {
		return nativetoolchain.Installation{}, nativetoolchain.Installation{}, fmt.Errorf("inspect candidate native toolchain: %w", err)
	}
	return baseline, candidate, nil
}

func toolchainIdentity(role string, manifest nativetoolchain.Manifest) *NativeToolchainIdentity {
	return &NativeToolchainIdentity{
		ManifestRef: filepath.ToSlash(filepath.Join("toolchains", role, nativetoolchain.ManifestName)),
		Digest:      manifest.Digest, PostgresVersion: nativetoolchain.Version(manifest, "postgres"), PgbenchVersion: nativetoolchain.Version(manifest, "pgbench"), PsqlVersion: nativetoolchain.Version(manifest, "psql"),
		SourceCommit: manifest.SourceCommit, BuildProvenance: manifest.BuildProvenance,
	}
}

func verifyToolchainIdentity(role string, identity *NativeToolchainIdentity) error {
	if identity == nil || identity.ManifestRef != filepath.ToSlash(filepath.Join("toolchains", role, nativetoolchain.ManifestName)) || !evidence.IsPortablePath(identity.ManifestRef) || !evidence.IsDigest(identity.Digest) || !validToolchainVersion(identity.PostgresVersion) || !validToolchainVersion(identity.PgbenchVersion) || !validToolchainVersion(identity.PsqlVersion) || identity.SourceCommit != nativetoolchain.Unattested || identity.BuildProvenance != nativetoolchain.Unattested {
		return fmt.Errorf("invalid %s native toolchain identity", role)
	}
	return nil
}

func validToolchainVersion(value string) bool {
	return value != "" && len(value) <= 1024 && !strings.ContainsAny(value, "\r\n\x00")
}

func protocolDigest(protocol Protocol) (string, error) {
	copy := protocol
	copy.Digest = ""
	content, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}
