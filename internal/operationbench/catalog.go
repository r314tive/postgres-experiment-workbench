package operationbench

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,159}$`)

type Catalog struct{ Root string }

func NewCatalog(root string) Catalog { return Catalog{Root: root} }

func (catalog Catalog) List() ([]Spec, error) {
	base := filepath.Join(catalog.Root, "benchmarks", "operations")
	var specs []Spec
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return fmt.Errorf("operation benchmark catalog contains non-JSON file: %s", path)
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		id := strings.TrimSuffix(filepath.ToSlash(rel), ".json")
		spec, err := catalog.loadPath(path, id)
		if err != nil {
			return err
		}
		specs = append(specs, spec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	return specs, nil
}

func (catalog Catalog) Load(input string) (Spec, error) {
	if !idPattern.MatchString(input) || strings.Contains(input, "..") {
		return Spec{}, fmt.Errorf("invalid operation benchmark id %q", input)
	}
	path := filepath.Join(catalog.Root, "benchmarks", "operations", filepath.FromSlash(input)+".json")
	return catalog.loadPath(path, input)
}

func (catalog Catalog) loadPath(path, expectedID string) (Spec, error) {
	var spec Spec
	if err := decodeStrictFile(path, &spec); err != nil {
		return Spec{}, fmt.Errorf("parse operation benchmark %s: %w", expectedID, err)
	}
	if err := validateSpec(spec, expectedID); err != nil {
		return Spec{}, fmt.Errorf("operation benchmark %s: %w", expectedID, err)
	}
	digest, err := evidence.DigestFile(path)
	if err != nil {
		return Spec{}, err
	}
	spec.Path = filepath.ToSlash(filepath.Join("benchmarks", "operations", filepath.FromSlash(expectedID)+".json"))
	spec.Digest = digest
	return spec, nil
}

func validateSpec(spec Spec, expectedID string) error {
	if spec.SchemaVersion != SpecSchemaVersion {
		return fmt.Errorf("schema_version must be %q", SpecSchemaVersion)
	}
	if spec.ID != expectedID || !idPattern.MatchString(spec.ID) || strings.Contains(spec.ID, "..") {
		return fmt.Errorf("id must match canonical catalog path %q", expectedID)
	}
	if strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Description) == "" || strings.TrimSpace(spec.Assurance) == "" {
		return fmt.Errorf("name, description, and assurance must be non-empty")
	}
	if spec.Classification != Classification || spec.DecisionEligible {
		return fmt.Errorf("classification must be %q and decision_eligible must be false", Classification)
	}
	if !idPattern.MatchString(spec.ExperimentSpec) || strings.Contains(spec.ExperimentSpec, "..") {
		return fmt.Errorf("experiment_spec is not a canonical id")
	}
	if spec.Trials < 2 || spec.Trials > 100 {
		return fmt.Errorf("trials must be between 2 and 100")
	}
	if math.IsNaN(spec.MaxCVPct) || math.IsInf(spec.MaxCVPct, 0) || spec.MaxCVPct <= 0 || spec.MaxCVPct > 1000 {
		return fmt.Errorf("max_cv_pct must be finite and in (0,1000]")
	}
	if len(spec.SupportedRuntime) == 0 || len(spec.SupportedRuntime) > 2 {
		return fmt.Errorf("supported_runtimes must be a non-empty unique subset of docker,native")
	}
	seen := map[string]bool{}
	for _, runtimeName := range spec.SupportedRuntime {
		if runtimeName != "docker" && runtimeName != "native" || seen[runtimeName] {
			return fmt.Errorf("supported_runtimes must be a unique subset of docker,native")
		}
		seen[runtimeName] = true
	}
	measurement := spec.Measurement
	if measurement.Basis != "operation-result" && measurement.Basis != "linked-run-wall-clock" {
		return fmt.Errorf("measurement basis must be operation-result or linked-run-wall-clock")
	}
	if measurement.Basis == "operation-result" {
		if !evidence.IsPortablePath(measurement.ResultPath) || !strings.HasPrefix(measurement.ResultPath, "artifacts/") {
			return fmt.Errorf("operation-result basis requires an artifacts/ result_path")
		}
	} else if measurement.ResultPath != "" {
		return fmt.Errorf("linked-run-wall-clock basis must not declare result_path")
	}
	if !idPattern.MatchString(measurement.Metric) || strings.TrimSpace(measurement.Unit) == "" || strings.TrimSpace(measurement.Scope) == "" {
		return fmt.Errorf("measurement metric, unit, and scope must be non-empty")
	}
	if measurement.Direction != "lower-is-better" && measurement.Direction != "higher-is-better" {
		return fmt.Errorf("measurement direction must be lower-is-better or higher-is-better")
	}
	return nil
}

func supportsRuntime(spec Spec, runtimeName string) bool {
	for _, supported := range spec.SupportedRuntime {
		if supported == runtimeName {
			return true
		}
	}
	return false
}
