package benchmarkexternal

import (
	"fmt"
	"io"
)

func Render(writer io.Writer, artifact Artifact) error {
	_, err := fmt.Fprintf(
		writer,
		"PASS: external benchmark driver=%s workload=%s runtime=%s conclusion=%s\nexecution_dir=%s\nnormalized_import=%s\ntarget_policy=%s acknowledgement=%s endpoint=%s:%d/%s target_ownership_verified=false target_identity_attested=false\nassurance=descriptive-only decision_eligible=false source_to_binary_attested=false tpc_compliance=false\n",
		artifact.Registry.Driver.ID,
		artifact.Workload,
		artifact.Runtime,
		artifact.Conclusion,
		artifact.ArtifactDir,
		artifact.Normalized.ArtifactDigest,
		artifact.TargetSafety.Policy,
		artifact.TargetSafety.Acknowledgement,
		artifact.TargetSafety.Host,
		artifact.TargetSafety.Port,
		artifact.TargetSafety.Database,
	)
	return err
}

func RenderVerification(writer io.Writer, verification Verification) error {
	status := "PASS"
	if !verification.IsValid() {
		status = "FAIL"
	}
	if _, err := fmt.Fprintf(writer, "%s: external benchmark driver execution %s\n", status, verification.Dir); err != nil {
		return err
	}
	for _, issue := range verification.Issues {
		if _, err := fmt.Fprintf(writer, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}
