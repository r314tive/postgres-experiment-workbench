package releaseevidence

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/releaseassets"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasemanifest"
)

func TestCandidateFromArtifactsAndNewIndex(t *testing.T) {
	manifest := candidateManifestFixture()
	inventory := candidateInventoryFixture(t, manifest)
	candidate, err := CandidateFromArtifacts(manifest, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Version != manifest.Version || candidate.Tag != inventory.Tag || candidate.GitCommit != manifest.GitCommit || candidate.AssetFingerprint != inventory.AssetFingerprint {
		t.Fatalf("derived candidate = %+v", candidate)
	}
	if candidate.ScenarioPack.ID != manifest.ScenarioPack.ID || candidate.ScenarioPack.Version != manifest.ScenarioPack.Version || candidate.ScenarioPack.Digest != manifest.ScenarioPack.Digest {
		t.Fatalf("derived scenario pack = %+v", candidate.ScenarioPack)
	}

	index, err := NewIndex(candidate, inventory.CapturedAt)
	if err != nil {
		t.Fatal(err)
	}
	verification := Verify(index)
	if !verification.Valid || verification.Status != StatusOpen || verification.Decision != DecisionNoGo {
		t.Fatalf("new index verification = %+v", verification)
	}
	if index.SchemaVersion != SchemaVersionV3 || index.Lineage == nil || index.Lineage.Revision != 0 || index.Lineage.PreviousIndexDigest != nil {
		t.Fatalf("new index lineage = %+v", index.Lineage)
	}
	if len(verification.OpenGates) != 16 || len(verification.PassedGates) != 0 || len(verification.FailedGates) != 0 {
		t.Fatalf("new index requirements: open=%v passed=%v failed=%v", verification.OpenGates, verification.PassedGates, verification.FailedGates)
	}
	if verification.RecordedDecision != DecisionNoGo || verification.ReadinessDecision != DecisionNoGo || verification.AssuranceStatus != AssuranceNotApplicable || verification.AuthorizationEligible || len(verification.UnqualifiedEvidence) != 0 {
		t.Fatalf("new index authorization boundary = %+v", verification)
	}
}

func TestCandidateFromArtifactsRejectsIdentityAndTimeMismatch(t *testing.T) {
	manifest := candidateManifestFixture()
	tests := []struct {
		name   string
		mutate func(*releaseassets.Inventory)
		want   string
	}{
		{name: "tag", mutate: func(value *releaseassets.Inventory) { value.Tag = "v9.9.9" }, want: "tag"},
		{name: "commit", mutate: func(value *releaseassets.Inventory) { value.GitCommit = strings.Repeat("d", 40) }, want: "commit"},
		{name: "captured before manifest", mutate: func(value *releaseassets.Inventory) { value.CapturedAt = "2026-08-13T23:59:59Z" }, want: "must not precede"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventory := candidateInventoryFixture(t, manifest)
			test.mutate(&inventory)
			if _, err := CandidateFromArtifacts(manifest, inventory); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CandidateFromArtifacts error = %v, want %q", err, test.want)
			}
		})
	}
}

func candidateManifestFixture() releasemanifest.Manifest {
	version := "1.2.3-rc.1+build.7"
	platforms := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"}
	archives := make([]releasemanifest.Archive, 0, len(platforms))
	sboms := make([]releasemanifest.SBOM, 0, len(platforms))
	for index, platform := range platforms {
		archive := "pgworkbench-" + version + "-" + platform + ".tar.gz"
		archives = append(archives, releasemanifest.Archive{
			Path: archive, SHA256: fmt.Sprintf("%064x", index+1), Size: int64(index + 1),
		})
		sboms = append(sboms, releasemanifest.SBOM{
			Path: strings.TrimSuffix(archive, ".tar.gz") + ".spdx.json", SHA256: fmt.Sprintf("%064x", index+11), Size: int64(index + 11), Subject: archive,
		})
	}
	return releasemanifest.Manifest{
		SchemaVersion: releasemanifest.SchemaVersion,
		Version:       version,
		GitCommit:     strings.Repeat("c", 40),
		GoToolchain:   "go1.26.5",
		ScenarioPack: releasemanifest.ScenarioPack{
			ID:      "builtin",
			Version: version,
			Digest:  "sha256:" + strings.Repeat("a", 64),
		},
		GeneratedAt: "2026-08-14T00:00:00Z",
		Archives:    archives,
		SBOMs:       sboms,
		ChecksumFile: releasemanifest.ChecksumFile{
			Path: "pgworkbench-" + version + "-SHA256SUMS.txt", Digest: "sha256:" + strings.Repeat("3", 64),
		},
	}
}

func candidateInventoryFixture(t *testing.T, manifest releasemanifest.Manifest) releaseassets.Inventory {
	t.Helper()
	names := make([]string, 0, 16)
	for _, archive := range manifest.Archives {
		names = append(names, archive.Path)
	}
	for _, sbom := range manifest.SBOMs {
		names = append(names, sbom.Path, strings.TrimSuffix(sbom.Path, ".spdx.json")+"-sbom.sigstore.json")
	}
	names = append(names,
		manifest.ChecksumFile.Path,
		releasemanifest.DefaultManifestPath(manifest.Version),
		"pgworkbench-"+manifest.Version+"-METADATA-SHA256SUMS.txt",
		"pgworkbench-"+manifest.Version+"-provenance.sigstore.json",
	)
	sort.Strings(names)
	assets := make([]releaseassets.Asset, 0, len(names))
	for index, name := range names {
		id, err := releaseassets.NewIntegerAssetID(uint64(index + 1))
		if err != nil {
			t.Fatal(err)
		}
		assets = append(assets, releaseassets.Asset{
			ID: id, Name: name, Size: int64(index + 1), Digest: "sha256:" + fmt.Sprintf("%064x", index+1),
		})
	}
	fingerprint, err := releaseassets.ComputeFingerprint(assets)
	if err != nil {
		t.Fatal(err)
	}
	return releaseassets.Inventory{
		SchemaVersion:        releaseassets.SchemaVersion,
		ArtifactType:         releaseassets.ArtifactType,
		ReleaseState:         releaseassets.ReleaseStateDraft,
		Tag:                  "v" + manifest.Version,
		GitCommit:            manifest.GitCommit,
		CapturedAt:           "2026-08-14T00:00:01Z",
		FingerprintAlgorithm: releaseassets.FingerprintAlgorithm,
		AssetFingerprint:     fingerprint,
		Assets:               assets,
	}
}
