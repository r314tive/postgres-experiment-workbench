package compatibility

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = "pgworkbench.compatibility-matrix/v2"

const DefaultPath = "compatibility/matrix.json"

type Matrix struct {
	SchemaVersion    string            `json:"schema_version"`
	ArchivePlatforms []ArchivePlatform `json:"archive_platforms"`
	Cells            []Cell            `json:"cells"`
}

// ArchivePlatform classifies what the release process proves for one built
// OS/architecture archive. A runtime-gated entry names the exact compatibility
// cells that must execute that archive; compile-package-only is deliberately
// bounded to cross-compilation and package/supply-chain checks.
type ArchivePlatform struct {
	OS                string   `json:"os"`
	Arch              string   `json:"arch"`
	VerificationScope string   `json:"verification_scope"`
	RuntimeCells      []string `json:"runtime_cells"`
}

type Cell struct {
	ID           string `json:"id"`
	Runtime      string `json:"runtime"`
	Topology     string `json:"topology"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Postgres     string `json:"postgres"`
	SupportLevel string `json:"support_level"`
	Gate         string `json:"gate"`
}

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	postgresPattern = regexp.MustCompile(`^[1-9][0-9]*(?:->[1-9][0-9]*)?$`)

	allowedRuntime  = stringSet("docker", "native")
	allowedTopology = stringSet(
		"single",
		"primary-replica",
		"logical-replication",
		"pgbouncer",
		"multi-version-upgrade",
	)
	allowedOS           = stringSet("linux", "darwin")
	allowedArch         = stringSet("amd64", "arm64")
	allowedSupportLevel = stringSet("candidate", "unsupported")
	allowedGate         = stringSet("docker-integration", "native-integration", "manual", "not-applicable")
	allowedArchiveScope = stringSet("runtime-gated", "compile-package-only")
)

func Load(path string) (Matrix, error) {
	file, err := os.Open(path)
	if err != nil {
		return Matrix{}, err
	}
	defer file.Close()

	matrix, err := Decode(file)
	if err != nil {
		return Matrix{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return matrix, nil
}

func Decode(reader io.Reader) (Matrix, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var matrix Matrix
	if err := decoder.Decode(&matrix); err != nil {
		return Matrix{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Matrix{}, err
	}
	if err := Validate(matrix); err != nil {
		return Matrix{}, err
	}
	return matrix, nil
}

func Validate(matrix Matrix) error {
	if matrix.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported compatibility matrix schema: %q", matrix.SchemaVersion)
	}
	if len(matrix.Cells) == 0 {
		return fmt.Errorf("compatibility matrix must contain at least one cell")
	}
	if len(matrix.ArchivePlatforms) == 0 {
		return fmt.Errorf("compatibility matrix must classify at least one release archive platform")
	}

	ids := make(map[string]struct{}, len(matrix.Cells))
	cellsByID := make(map[string]Cell, len(matrix.Cells))
	coordinates := make(map[string]string, len(matrix.Cells))
	for index, cell := range matrix.Cells {
		if err := validateCell(cell); err != nil {
			return fmt.Errorf("cell %d (%q): %w", index, cell.ID, err)
		}
		if _, exists := ids[cell.ID]; exists {
			return fmt.Errorf("duplicate compatibility cell id: %s", cell.ID)
		}
		ids[cell.ID] = struct{}{}
		cellsByID[cell.ID] = cell

		coordinate := strings.Join([]string{cell.Runtime, cell.Topology, cell.OS, cell.Arch, cell.Postgres}, "\x00")
		if previous, exists := coordinates[coordinate]; exists {
			return fmt.Errorf("duplicate compatibility coordinates: %s and %s", previous, cell.ID)
		}
		coordinates[coordinate] = cell.ID
	}

	platforms := make(map[string]struct{}, len(matrix.ArchivePlatforms))
	referencedCells := make(map[string]string, len(matrix.Cells))
	for index, platform := range matrix.ArchivePlatforms {
		if err := validateArchivePlatform(platform); err != nil {
			return fmt.Errorf("archive platform %d (%s/%s): %w", index, platform.OS, platform.Arch, err)
		}
		coordinate := platform.OS + "\x00" + platform.Arch
		if _, exists := platforms[coordinate]; exists {
			return fmt.Errorf("duplicate release archive platform: %s/%s", platform.OS, platform.Arch)
		}
		platforms[coordinate] = struct{}{}

		seenRuntimeCells := make(map[string]struct{}, len(platform.RuntimeCells))
		for _, cellID := range platform.RuntimeCells {
			if _, exists := seenRuntimeCells[cellID]; exists {
				return fmt.Errorf("archive platform %s/%s has duplicate runtime cell: %s", platform.OS, platform.Arch, cellID)
			}
			seenRuntimeCells[cellID] = struct{}{}
			cell, exists := cellsByID[cellID]
			if !exists {
				return fmt.Errorf("archive platform %s/%s references unknown runtime cell: %s", platform.OS, platform.Arch, cellID)
			}
			if cell.SupportLevel != "candidate" {
				return fmt.Errorf("archive platform %s/%s runtime cell %s is not a candidate", platform.OS, platform.Arch, cellID)
			}
			if cell.OS != platform.OS || cell.Arch != platform.Arch {
				return fmt.Errorf("archive platform %s/%s runtime cell %s has coordinates %s/%s", platform.OS, platform.Arch, cellID, cell.OS, cell.Arch)
			}
			if previous, exists := referencedCells[cellID]; exists {
				return fmt.Errorf("runtime cell %s is assigned to multiple archive platforms: %s and %s/%s", cellID, previous, platform.OS, platform.Arch)
			}
			referencedCells[cellID] = platform.OS + "/" + platform.Arch
		}
	}
	for _, cell := range matrix.Cells {
		if cell.SupportLevel != "candidate" {
			continue
		}
		if _, exists := referencedCells[cell.ID]; !exists {
			return fmt.Errorf("candidate compatibility cell is not assigned to a runtime-gated release archive platform: %s", cell.ID)
		}
	}
	return nil
}

func RenderJSON(writer io.Writer, matrix Matrix) error {
	if err := Validate(matrix); err != nil {
		return err
	}
	normalized := normalizedMatrix(matrix)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(normalized)
}

func RenderMarkdown(writer io.Writer, matrix Matrix) error {
	if err := Validate(matrix); err != nil {
		return err
	}
	normalized := normalizedMatrix(matrix)
	if _, err := fmt.Fprintln(writer, "# Compatibility support ledger"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "\nSchema: `"+SchemaVersion+"`"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "\nA `candidate` cell declares a target and its required gate; it does not claim that the gate has passed."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "\nA `runtime-gated` archive has the listed execution gates; it is not runtime-qualified until those gates pass for the exact candidate. `compile-package-only` archives have no runtime support claim."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "\n| Release archive platform | Verification scope | Required runtime cells |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "| --- | --- | --- |"); err != nil {
		return err
	}
	for _, platform := range normalized.ArchivePlatforms {
		runtimeCells := "none"
		if len(platform.RuntimeCells) != 0 {
			quoted := make([]string, len(platform.RuntimeCells))
			for index, cellID := range platform.RuntimeCells {
				quoted[index] = "`" + cellID + "`"
			}
			runtimeCells = strings.Join(quoted, ", ")
		}
		if _, err := fmt.Fprintf(writer, "| `%s/%s` | `%s` | %s |\n", platform.OS, platform.Arch, platform.VerificationScope, runtimeCells); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer, "\n| ID | Runtime | Topology | OS | Arch | PostgreSQL | Support level | Required gate |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "| --- | --- | --- | --- | --- | --- | --- | --- |"); err != nil {
		return err
	}
	for _, cell := range normalized.Cells {
		if _, err := fmt.Fprintf(
			writer,
			"| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
			cell.ID,
			cell.Runtime,
			cell.Topology,
			cell.OS,
			cell.Arch,
			cell.Postgres,
			cell.SupportLevel,
			cell.Gate,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateArchivePlatform(platform ArchivePlatform) error {
	if !allowedOS[platform.OS] {
		return fmt.Errorf("invalid os: %q", platform.OS)
	}
	if !allowedArch[platform.Arch] {
		return fmt.Errorf("invalid arch: %q", platform.Arch)
	}
	if !allowedArchiveScope[platform.VerificationScope] {
		return fmt.Errorf("invalid verification_scope: %q", platform.VerificationScope)
	}
	switch platform.VerificationScope {
	case "runtime-gated":
		if len(platform.RuntimeCells) == 0 {
			return fmt.Errorf("runtime-gated archive platform must name at least one runtime cell")
		}
	case "compile-package-only":
		if len(platform.RuntimeCells) != 0 {
			return fmt.Errorf("compile-package-only archive platform cannot name runtime cells")
		}
	}
	return nil
}

func validateCell(cell Cell) error {
	if !idPattern.MatchString(cell.ID) {
		return fmt.Errorf("invalid id: %q", cell.ID)
	}
	if !allowedRuntime[cell.Runtime] {
		return fmt.Errorf("invalid runtime: %q", cell.Runtime)
	}
	if !allowedTopology[cell.Topology] {
		return fmt.Errorf("invalid topology: %q", cell.Topology)
	}
	if !allowedOS[cell.OS] {
		return fmt.Errorf("invalid os: %q", cell.OS)
	}
	if !allowedArch[cell.Arch] {
		return fmt.Errorf("invalid arch: %q", cell.Arch)
	}
	if !postgresPattern.MatchString(cell.Postgres) {
		return fmt.Errorf("invalid postgres major or upgrade pair: %q", cell.Postgres)
	}
	if !allowedSupportLevel[cell.SupportLevel] {
		return fmt.Errorf("invalid support_level: %q", cell.SupportLevel)
	}
	if !allowedGate[cell.Gate] {
		return fmt.Errorf("invalid gate: %q", cell.Gate)
	}
	if cell.Runtime == "native" && cell.Topology != "single" {
		return fmt.Errorf("native runtime supports only the single topology")
	}
	if cell.SupportLevel == "unsupported" {
		if cell.Gate != "not-applicable" {
			return fmt.Errorf("unsupported cells must use the not-applicable gate")
		}
		return nil
	}
	if cell.Gate == "not-applicable" {
		return fmt.Errorf("candidate cells must name a verification gate")
	}
	if cell.Runtime == "docker" && cell.Gate == "native-integration" {
		return fmt.Errorf("docker cells cannot use the native-integration gate")
	}
	if cell.Runtime == "native" && cell.Gate == "docker-integration" {
		return fmt.Errorf("native cells cannot use the docker-integration gate")
	}
	return nil
}

func normalizedMatrix(matrix Matrix) Matrix {
	normalized := Matrix{
		SchemaVersion:    matrix.SchemaVersion,
		ArchivePlatforms: append([]ArchivePlatform(nil), matrix.ArchivePlatforms...),
		Cells:            append([]Cell(nil), matrix.Cells...),
	}
	for index := range normalized.ArchivePlatforms {
		normalized.ArchivePlatforms[index].RuntimeCells = append([]string{}, normalized.ArchivePlatforms[index].RuntimeCells...)
		sort.Strings(normalized.ArchivePlatforms[index].RuntimeCells)
	}
	sort.Slice(normalized.ArchivePlatforms, func(left, right int) bool {
		if normalized.ArchivePlatforms[left].OS != normalized.ArchivePlatforms[right].OS {
			return normalized.ArchivePlatforms[left].OS < normalized.ArchivePlatforms[right].OS
		}
		return normalized.ArchivePlatforms[left].Arch < normalized.ArchivePlatforms[right].Arch
	})
	sort.Slice(normalized.Cells, func(left, right int) bool {
		return normalized.Cells[left].ID < normalized.Cells[right].ID
	})
	return normalized
}

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
