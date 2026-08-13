package benchmarkdrivers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	SchemaVersion = "pgworkbench.benchmark-driver-lock/v1"
	ArtifactType  = "pgworkbench.benchmark-driver-lock"
	LockPath      = "compatibility/benchmark-drivers.json"
)

var (
	idPattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	workloadPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,159}$`)
	repositoryPattern = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	contractPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+\-]{0,127}$`)
)

type Registry struct {
	SchemaVersion string   `json:"schema_version"`
	ArtifactType  string   `json:"artifact_type"`
	Drivers       []Driver `json:"drivers"`
}

type Driver struct {
	ID                         string   `json:"id"`
	Adapter                    string   `json:"adapter"`
	DisplayVersion             string   `json:"display_version"`
	Repository                 string   `json:"repository"`
	RefType                    string   `json:"ref_type"`
	Ref                        string   `json:"ref"`
	TagObject                  string   `json:"tag_object,omitempty"`
	Commit                     string   `json:"commit"`
	Entrypoint                 string   `json:"entrypoint"`
	ResultFormat               string   `json:"result_format"`
	Parser                     string   `json:"parser"`
	RuntimeSupport             []string `json:"runtime_support"`
	Workloads                  []string `json:"workloads"`
	BinaryDistributedByProject bool     `json:"binary_distributed_by_project"`
	SourceToBinaryAttested     bool     `json:"source_to_binary_attested"`
	DecisionEligible           bool     `json:"decision_eligible"`
}

type Inspection struct {
	Path     string   `json:"path"`
	Digest   string   `json:"digest"`
	Registry Registry `json:"registry"`
}

func Load(root string) (Inspection, error) {
	path := filepath.Join(root, filepath.FromSlash(LockPath))
	info, err := os.Lstat(path)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect benchmark driver lock: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Inspection{}, fmt.Errorf("benchmark driver lock must be a regular non-symlink file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Inspection{}, fmt.Errorf("read benchmark driver lock: %w", err)
	}
	registry, err := Parse(content)
	if err != nil {
		return Inspection{}, fmt.Errorf("parse benchmark driver lock: %w", err)
	}
	sum := sha256.Sum256(content)
	return Inspection{
		Path:     filepath.ToSlash(LockPath),
		Digest:   "sha256:" + hex.EncodeToString(sum[:]),
		Registry: registry,
	}, nil
}

func Parse(content []byte) (Registry, error) {
	if err := rejectDuplicateKeys(content); err != nil {
		return Registry{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Registry{}, fmt.Errorf("benchmark driver lock contains trailing JSON values")
		}
		return Registry{}, err
	}
	if err := Validate(registry); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func Validate(registry Registry) error {
	if registry.SchemaVersion != SchemaVersion || registry.ArtifactType != ArtifactType {
		return fmt.Errorf("benchmark driver lock must use schema %q and artifact type %q", SchemaVersion, ArtifactType)
	}
	if len(registry.Drivers) == 0 {
		return fmt.Errorf("benchmark driver lock must contain at least one driver")
	}
	for index, driver := range registry.Drivers {
		if index > 0 && registry.Drivers[index-1].ID >= driver.ID {
			return fmt.Errorf("benchmark drivers must be sorted by unique id")
		}
		if err := validateDriver(driver); err != nil {
			return fmt.Errorf("driver %q: %w", driver.ID, err)
		}
	}
	return nil
}

func (registry Registry) Find(id string) (Driver, error) {
	index, found := slices.BinarySearchFunc(registry.Drivers, id, func(driver Driver, target string) int {
		return strings.Compare(driver.ID, target)
	})
	if !found {
		return Driver{}, fmt.Errorf("pinned benchmark driver not found: %s", id)
	}
	return registry.Drivers[index], nil
}

func RenderJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func Render(writer io.Writer, inspection Inspection) error {
	if err := Validate(inspection.Registry); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Benchmark driver lock: %s\nDigest: %s\n\n", inspection.Path, inspection.Digest); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "ID\tAdapter\tVersion\tRuntime\tDecision eligible"); err != nil {
		return err
	}
	for _, driver := range inspection.Registry.Drivers {
		if _, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%t\n",
			driver.ID,
			driver.Adapter,
			driver.DisplayVersion,
			strings.Join(driver.RuntimeSupport, ","),
			driver.DecisionEligible,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(writer, "\nThe lock pins source identities and parser contracts; it does not attest installed binaries or make imported trials decision-eligible.")
	return err
}

func RenderDriver(writer io.Writer, inspection Inspection, driver Driver) error {
	if err := validateDriver(driver); err != nil {
		return err
	}
	fields := [][2]string{
		{"ID", driver.ID},
		{"Adapter", driver.Adapter},
		{"Version", driver.DisplayVersion},
		{"Repository", driver.Repository},
		{"Ref", driver.RefType + ":" + driver.Ref},
		{"Commit", driver.Commit},
		{"Entrypoint", driver.Entrypoint},
		{"Result format", driver.ResultFormat},
		{"Parser", driver.Parser},
		{"Runtime support", strings.Join(driver.RuntimeSupport, ",")},
		{"Workloads", strings.Join(driver.Workloads, ",")},
		{"Lock digest", inspection.Digest},
		{"Binary distributed", strconv.FormatBool(driver.BinaryDistributedByProject)},
		{"Source-to-binary attested", strconv.FormatBool(driver.SourceToBinaryAttested)},
		{"Decision eligible", strconv.FormatBool(driver.DecisionEligible)},
	}
	if driver.TagObject != "" {
		fields = append(fields[:5], append([][2]string{{"Tag object", driver.TagObject}}, fields[5:]...)...)
	}
	for _, field := range fields {
		if _, err := fmt.Fprintf(writer, "%s: %s\n", field[0], field[1]); err != nil {
			return err
		}
	}
	return nil
}

func validateDriver(driver Driver) error {
	if !idPattern.MatchString(driver.ID) {
		return fmt.Errorf("invalid id")
	}
	if !oneOf(driver.Adapter, "benchbase", "hammerdb6", "sysbench1") {
		return fmt.Errorf("unsupported adapter %q", driver.Adapter)
	}
	if strings.TrimSpace(driver.DisplayVersion) != driver.DisplayVersion || driver.DisplayVersion == "" || len(driver.DisplayVersion) > 128 {
		return fmt.Errorf("invalid display version")
	}
	if !repositoryPattern.MatchString(driver.Repository) {
		return fmt.Errorf("repository must identify one GitHub repository")
	}
	if !shaPattern.MatchString(driver.Commit) {
		return fmt.Errorf("commit must be a full lowercase SHA-1 object id")
	}
	switch driver.RefType {
	case "commit":
		if driver.Ref != driver.Commit || driver.TagObject != "" {
			return fmt.Errorf("commit ref must equal commit and omit tag_object")
		}
	case "tag":
		if driver.Ref == "" || !shaPattern.MatchString(driver.TagObject) {
			return fmt.Errorf("tag ref requires a full tag_object and dereferenced commit")
		}
	default:
		return fmt.Errorf("ref_type must be commit or tag")
	}
	if strings.TrimSpace(driver.Entrypoint) != driver.Entrypoint || driver.Entrypoint == "" || len(driver.Entrypoint) > 128 {
		return fmt.Errorf("invalid entrypoint")
	}
	for label, value := range map[string]string{"result_format": driver.ResultFormat, "parser": driver.Parser} {
		if !contractPattern.MatchString(value) {
			return fmt.Errorf("invalid %s", label)
		}
	}
	if !sortedUnique(driver.RuntimeSupport) || !allIn(driver.RuntimeSupport, "native", "docker") {
		return fmt.Errorf("runtime_support must be sorted, unique, and limited to native/docker")
	}
	if !sortedUnique(driver.Workloads) {
		return fmt.Errorf("workloads must be sorted and unique")
	}
	for _, workload := range driver.Workloads {
		if !workloadPattern.MatchString(workload) {
			return fmt.Errorf("invalid workload %q", workload)
		}
	}
	if driver.BinaryDistributedByProject || driver.SourceToBinaryAttested || driver.DecisionEligible {
		return fmt.Errorf("external driver lock cannot claim bundled binary, source-to-binary attestation, or decision eligibility")
	}
	return nil
}

func sortedUnique(values []string) bool {
	if len(values) == 0 || !sort.StringsAreSorted(values) {
		return false
	}
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return false
		}
	}
	return true
}

func allIn(values []string, allowed ...string) bool {
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return false
		}
	}
	return true
}

func oneOf(value string, allowed ...string) bool { return slices.Contains(allowed, value) }

func rejectDuplicateKeys(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("benchmark driver lock contains trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("benchmark driver lock object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate benchmark driver lock object key %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid benchmark driver lock object closing token")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid benchmark driver lock array closing token")
		}
	default:
		return fmt.Errorf("unexpected benchmark driver lock delimiter %q", delimiter)
	}
	return nil
}
