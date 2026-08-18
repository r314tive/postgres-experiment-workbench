package benchmarkimport

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

// Verify independently re-parses the retained source and, where applicable,
// the retained mapping. A valid result confirms byte integrity and contract
// rederivation only; it does not attest that an upstream run was fair or valid.
func Verify(input string) (Verification, error) {
	dir, err := resolve(input)
	if err != nil {
		return Verification{}, err
	}
	verification := Verification{Dir: dir, Issues: []string{}}
	add := func(format string, values ...any) {
		verification.Issues = append(verification.Issues, fmt.Sprintf(format, values...))
	}

	checkEntries(add, dir, map[string]bool{ResultFile: false, "raw": true})
	resultContent, resultErr := readRegularLimited(filepath.Join(dir, ResultFile), maxArtifactBytes, "benchmark import result")
	if resultErr != nil {
		add("result.json: %v", resultErr)
		sort.Strings(verification.Issues)
		return verification, nil
	}
	stored, parseErr := parseArtifact(resultContent)
	if parseErr != nil {
		add("result.json parse failed: %v", parseErr)
		sort.Strings(verification.Issues)
		return verification, nil
	}
	stored.ArtifactDir = dir
	verification.Artifact = &stored

	rawDir := filepath.Join(dir, "raw")
	rawInfo, rawDirErr := os.Lstat(rawDir)
	if rawDirErr != nil || rawInfo.Mode()&os.ModeSymlink != 0 || !rawInfo.IsDir() {
		add("raw must be a non-symlink directory")
		sort.Strings(verification.Issues)
		return verification, nil
	}
	adapter, expectsMapping, adapterErr := adapterForArtifact(stored)
	if adapterErr != nil {
		add("result.json adapter identity: %v", adapterErr)
	}
	allowedRaw := map[string]bool{"source": false}
	if expectsMapping {
		allowedRaw["mapping.json"] = false
	}
	checkEntries(add, rawDir, allowedRaw)

	source, sourceErr := readRegularLimited(filepath.Join(dir, filepath.FromSlash(RawSourceFile)), maxRawBytes, "retained raw source")
	if sourceErr != nil {
		add("raw/source: %v", sourceErr)
	}
	var mapping []byte
	if expectsMapping {
		mapping, err = readRegularLimited(filepath.Join(dir, filepath.FromSlash(MappingFile)), maxMappingBytes, "retained mapping")
		if err != nil {
			add("raw/mapping.json: %v", err)
		}
	}
	if sourceErr != nil || expectsMapping && mapping == nil || adapterErr != nil {
		sort.Strings(verification.Issues)
		return verification, nil
	}

	if stored.RawInput != fileEvidence(RawSourceFile, source) {
		add("raw_input does not match retained raw/source bytes")
	}
	if expectsMapping {
		wantMapping := fileEvidence(MappingFile, mapping)
		if stored.MappingInput == nil || *stored.MappingInput != wantMapping {
			add("mapping_input does not match retained raw/mapping.json bytes")
		}
	} else if stored.MappingInput != nil {
		add("sysbench import must not declare mapping_input")
	}

	deriveWorkload := ""
	if adapter == AdapterSysbench1 {
		deriveWorkload = stored.Workload
	}
	want, deriveErr := derive(adapter, source, mapping, deriveWorkload)
	if deriveErr != nil {
		add("independent normalization failed: %v", deriveErr)
	} else {
		want.RawInput = fileEvidence(RawSourceFile, source)
		if expectsMapping {
			value := fileEvidence(MappingFile, mapping)
			want.MappingInput = &value
		}
		want.Digest, deriveErr = artifactDigest(want)
		if deriveErr != nil {
			add("recompute artifact digest: %v", deriveErr)
		} else {
			want.ArtifactDir = dir
			if !reflect.DeepEqual(stored, want) {
				add("result.json does not match independently re-derived import contract")
			}
		}
	}
	if !evidence.IsDigest(stored.Digest) {
		add("digest is not a lowercase sha256 digest")
	} else if digest, digestErr := artifactDigest(stored); digestErr != nil || digest != stored.Digest {
		add("artifact digest mismatch")
	}
	if stored.DecisionEligible || stored.PGbenchSeriesEligible || stored.Classification != ClassificationImported || stored.AnalysisDesign != AnalysisDesignImported || stored.Conclusion != ConclusionDescriptive {
		add("import must remain descriptive/imported and ineligible for pgbench series or decisions")
	}

	verification.Issues = uniqueSorted(verification.Issues)
	verification.Valid = verification.IsValid()
	return verification, nil
}

func adapterForArtifact(artifact Artifact) (string, bool, error) {
	switch {
	case artifact.Driver == DriverHammerDB && artifact.SourceFormat == HammerDBSourceFormat && artifact.ParserVersion == MappingParserVersion:
		return AdapterHammerDB6, true, nil
	case artifact.Driver == DriverHammerDB && artifact.SourceFormat == HammerDB6ReportSourceFormat && artifact.ParserVersion == HammerDB6ReportParserVersion:
		return AdapterHammerDB6Report, false, nil
	case artifact.Driver == DriverSysbench && artifact.SourceFormat == SysbenchSourceFormat && artifact.ParserVersion == SysbenchParserVersion:
		return AdapterSysbench1, false, nil
	case artifact.Driver == DriverBenchBase && artifact.SourceFormat == BenchBaseSourceFormat && artifact.ParserVersion == MappingParserVersion:
		return AdapterBenchBase, true, nil
	case artifact.Driver == DriverBenchBase && artifact.SourceFormat == BenchBase33c0047SourceFormat && artifact.ParserVersion == BenchBase33c0047ParserVersion:
		return AdapterBenchBase33c0047, false, nil
	default:
		return "", false, fmt.Errorf("unsupported driver/source_format/parser_version tuple")
	}
}

func resolve(input string) (string, error) {
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve benchmark import: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("inspect benchmark import: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("benchmark import path must not be a symlink")
	}
	if info.IsDir() {
		return abs, nil
	}
	if info.Mode().IsRegular() && filepath.Base(abs) == ResultFile {
		parent := filepath.Dir(abs)
		parentInfo, parentErr := os.Lstat(parent)
		if parentErr == nil && parentInfo.IsDir() && parentInfo.Mode()&os.ModeSymlink == 0 {
			return parent, nil
		}
	}
	return "", fmt.Errorf("benchmark import must be a non-symlink directory or its result.json")
}

func checkEntries(add func(string, ...any), dir string, expected map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		add("read artifact directory %s: %v", dir, err)
		return
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		wantDirectory, exists := expected[name]
		if !exists {
			add("unexpected artifact entry %s", filepath.ToSlash(filepath.Join(filepath.Base(dir), name)))
			continue
		}
		seen[name] = struct{}{}
		info, statErr := os.Lstat(filepath.Join(dir, name))
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || wantDirectory != info.IsDir() || !wantDirectory && !info.Mode().IsRegular() {
			add("artifact entry %s has an unsafe type", name)
		}
	}
	for name := range expected {
		if _, exists := seen[name]; !exists {
			add("required artifact entry %s is missing", name)
		}
	}
}

func renderIssueSummary(issues []string) string { return strings.Join(issues, "; ") }
