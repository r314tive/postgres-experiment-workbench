package releaseevidence

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/schemavalidation"
)

func TestV2LineageVerification(t *testing.T) {
	zero := int64(0)
	previous := "sha256:" + strings.Repeat("b", 64)
	tests := []struct {
		name    string
		lineage *Lineage
		valid   bool
		issue   string
	}{
		{name: "revision zero", lineage: &Lineage{Revision: zero}, valid: true},
		{name: "missing", lineage: nil, issue: "lineage is required"},
		{name: "negative", lineage: &Lineage{Revision: -1}, issue: "must be non-negative"},
		{name: "zero with predecessor", lineage: &Lineage{Revision: zero, PreviousIndexDigest: &previous}, issue: "must be absent"},
		{name: "later without predecessor", lineage: &Lineage{Revision: 1}, issue: "must be a lowercase sha256 digest"},
		{name: "later revision", lineage: &Lineage{Revision: 1, PreviousIndexDigest: &previous}, valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := openIndex(RecordStatusActive)
			index.SchemaVersion = SchemaVersionV2
			index.Lineage = test.lineage
			verification := Verify(index)
			if verification.Valid != test.valid {
				t.Fatalf("valid = %v, want %v: %+v", verification.Valid, test.valid, verification)
			}
			if test.issue != "" && !sliceContainsSubstring(verification.Issues, test.issue) {
				t.Fatalf("issues %v do not contain %q", verification.Issues, test.issue)
			}
		})
	}
}

func TestV1RejectsLineage(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.Lineage = &Lineage{Revision: 0}
	verification := Verify(index)
	if verification.Valid || !sliceContainsSubstring(verification.Issues, "lineage must be absent") {
		t.Fatalf("v1 lineage was not rejected: %+v", verification)
	}
}

func TestSchemaVersionsAcceptPrereleaseAndBuildTag(t *testing.T) {
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, schemaVersion := range []string{SchemaVersionV1, SchemaVersionV2} {
		t.Run(schemaVersion, func(t *testing.T) {
			index := openIndex(RecordStatusActive)
			index.SchemaVersion = schemaVersion
			if schemaVersion == SchemaVersionV2 {
				index.Lineage = &Lineage{Revision: 0}
			}
			index.Candidate.Version = "1.2.3-rc.1+build.7"
			index.Candidate.Tag = "v" + index.Candidate.Version
			verification := Verify(index)
			if !verification.Valid {
				t.Fatalf("prerelease/build candidate issues: %v", verification.Issues)
			}
			if err := registry.ValidateJSON("release-evidence-index.schema.json", []byte(marshalIndex(t, index))); err != nil {
				t.Fatalf("index does not conform to repository schema: %v", err)
			}
		})
	}
}

func TestLineageSchemaAndReaderRejectRevisionAboveJSONSafeInteger(t *testing.T) {
	registry, err := schemavalidation.CompileDir(filepath.Join("..", "..", "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	previous := "sha256:" + strings.Repeat("b", 64)
	index.Lineage = &Lineage{Revision: 1, PreviousIndexDigest: &previous}
	original := marshalIndex(t, index)
	content := strings.Replace(original, `"revision":1`, `"revision":9007199254740992`, 1)
	if content == original {
		t.Fatal("oversized revision test vector was not injected")
	}
	if err := registry.ValidateJSON("release-evidence-index.schema.json", []byte(content)); err == nil {
		t.Fatal("release evidence schema accepted revision above int64")
	}
	parsed, err := Parse([]byte(content))
	if err != nil {
		t.Fatalf("bounded semantic reader could not decode safe-integer overflow vector: %v", err)
	}
	if verification := Verify(parsed); verification.Valid || !sliceContainsSubstring(verification.Issues, "no greater than") {
		t.Fatalf("semantic verifier accepted oversized revision: %+v", verification)
	}
}

func TestWriteNewPublishesVerifiedImmutableIndex(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	output := filepath.Join(t.TempDir(), "evidence", "index-r0.json")

	result, err := WriteNew(output, index)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(result.Output) != filepath.Base(output) || !strings.HasPrefix(result.Digest, "sha256:") || !result.Verification.Valid {
		t.Fatalf("write result = %+v", result)
	}
	if info, err := os.Lstat(output); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		t.Fatalf("published output: info=%v err=%v", info, err)
	}
	loaded, err := VerifyFile(output)
	if err != nil || !loaded.Valid {
		t.Fatalf("VerifyFile(output) = %+v, %v", loaded, err)
	}

	if _, err := WriteNew(output, index); !errors.Is(err, pathguard.ErrOutputExists) {
		t.Fatalf("second WriteNew error = %v, want ErrOutputExists", err)
	}
}

func TestWriteNewRefusesInvalidIndexWithoutOutput(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	output := filepath.Join(t.TempDir(), "index.json")
	if _, err := WriteNew(output, index); err == nil {
		t.Fatal("WriteNew accepted v2 index without lineage")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid write left output: %v", err)
	}
}

func TestWriteNewDetectsStagedPathSwapBeforePublication(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	output := filepath.Join(t.TempDir(), "index.json")
	publisher := func(temporary, destination string) error {
		if err := os.Remove(temporary); err != nil {
			return err
		}
		if err := os.WriteFile(temporary, []byte("{}\n"), 0o644); err != nil {
			return err
		}
		return pathguard.PublishFileExclusive(temporary, destination)
	}
	result, err := writeNew(output, "", index, publisher, syncDirectory)
	var committed *CommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("writeNew swap error = %v, want CommittedError", err)
	}
	if result.Output == "" || committed.Result.Output != result.Output || !strings.Contains(err.Error(), "staged inode") {
		t.Fatalf("committed swap result=%+v error=%v", result, err)
	}
}

func TestWriteNewReportsCommittedButUnconfirmedDirectorySync(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	output := filepath.Join(t.TempDir(), "index.json")
	syncFailure := errors.New("injected directory sync failure")
	result, err := writeNew(output, "", index, pathguard.PublishFileExclusive, func(string) error { return syncFailure })
	var committed *CommittedError
	if !errors.As(err, &committed) || !errors.Is(err, syncFailure) {
		t.Fatalf("writeNew sync error = %v, want typed committed error", err)
	}
	if result.Output == "" || committed.Result.Digest != result.Digest {
		t.Fatalf("committed sync result=%+v error=%+v", result, committed)
	}
	verification, verifyErr := VerifyFile(output)
	if verifyErr != nil || !verification.Valid {
		t.Fatalf("committed output not independently verifiable: %+v, %v", verification, verifyErr)
	}
	if _, retryErr := WriteNew(output, index); !errors.Is(retryErr, pathguard.ErrOutputExists) {
		t.Fatalf("retry error = %v, want ErrOutputExists", retryErr)
	}
}

func TestWriteNewReportsCommittedWhenStagingNameCleanupFails(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	output := filepath.Join(t.TempDir(), "index.json")
	cleanupFailure := errors.New("injected staging-name cleanup failure")
	publisher := func(temporary, destination string) error {
		if err := os.Link(temporary, destination); err != nil {
			return err
		}
		return cleanupFailure
	}
	result, err := writeNew(output, "", index, publisher, syncDirectory)
	var committed *CommittedError
	if !errors.As(err, &committed) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("writeNew cleanup error = %v, want typed committed error", err)
	}
	if filepath.Base(result.Output) != filepath.Base(output) || committed.Result.Digest != result.Digest {
		t.Fatalf("committed cleanup result=%+v error=%+v", result, committed)
	}
	verification, verifyErr := VerifyFile(output)
	if verifyErr != nil || !verification.Valid {
		t.Fatalf("committed output not independently verifiable: %+v, %v", verification, verifyErr)
	}
	if _, retryErr := WriteNew(output, index); !errors.Is(retryErr, pathguard.ErrOutputExists) {
		t.Fatalf("retry error = %v, want ErrOutputExists", retryErr)
	}
}

func TestWriteNewOutsideRepeatsContainmentAfterParentRedirect(t *testing.T) {
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	source := t.TempDir()
	container := t.TempDir()
	originalParent := filepath.Join(container, "outside")
	if err := os.Mkdir(originalParent, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := pathguard.ResolveOutputOutside(source, filepath.Join(originalParent, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(originalParent, filepath.Join(container, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, originalParent); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := WriteNewOutside(source, resolved, index); !errors.Is(err, pathguard.ErrOutputWithinSource) {
		t.Fatalf("WriteNewOutside redirected error = %v, want ErrOutputWithinSource", err)
	}
	if _, err := os.Lstat(filepath.Join(source, "index.json")); !os.IsNotExist(err) {
		t.Fatalf("redirected writer mutated immutable source: %v", err)
	}
}
