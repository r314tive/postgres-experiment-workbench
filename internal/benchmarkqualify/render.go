package benchmarkqualify

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func RenderJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func Render(w io.Writer, artifact Artifact) error {
	_, err := fmt.Fprintf(w, "Host inspection: verdict=%s digest=%s evidence=%s signed=%t\n", artifact.Verdict, artifact.Digest, artifact.Assurance.EvidenceOrigin, artifact.Assurance.Signed)
	return err
}

func RenderVerification(w io.Writer, verification Verification) error {
	if verification.Valid {
		_, err := fmt.Fprintf(w, "PASS: operator-recorded unsigned host artifact is structurally valid; recorded verdict=%s (not host identity or current-state attestation)\n", verification.RecordedVerdict)
		return err
	}
	if _, err := fmt.Fprintln(w, "FAIL: host qualification artifact is invalid"); err != nil {
		return err
	}
	for _, issue := range verification.Issues {
		if _, err := fmt.Fprintf(w, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

// WriteFile publishes an artifact atomically without replacing an existing
// path. The temporary inode is linked into place only after a complete write.
func WriteFile(path string, artifact Artifact) error {
	if path == "" || path == "-" {
		return fmt.Errorf("output must be a file path")
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".host-qualification-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := RenderJSON(temporary, artifact); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("publish host qualification artifact: %w", err)
	}
	return nil
}
