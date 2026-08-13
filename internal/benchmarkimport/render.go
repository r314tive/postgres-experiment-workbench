package benchmarkimport

import (
	"encoding/json"
	"fmt"
	"io"
)

func RenderJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func Render(writer io.Writer, artifact Artifact) error {
	_, err := fmt.Fprintf(writer, "PASS: imported %s %s workload=%s metric=%s %.12g %s conclusion=%s\nimport_dir=%s\n", artifact.Driver, artifact.DriverVersion, artifact.Workload, artifact.PrimaryMetric.Name, artifact.PrimaryMetric.Value, artifact.PrimaryMetric.Unit, artifact.Conclusion, artifact.ArtifactDir)
	return err
}

func RenderVerification(writer io.Writer, verification Verification) error {
	status := "PASS"
	if !verification.IsValid() {
		status = "FAIL"
	}
	if _, err := fmt.Fprintf(writer, "%s: descriptive benchmark import %s\n", status, verification.Dir); err != nil {
		return err
	}
	for _, issue := range verification.Issues {
		if _, err := fmt.Fprintf(writer, "- %s\n", issue); err != nil {
			return err
		}
	}
	return nil
}
