package operationbench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

var repoReferencePattern = regexp.MustCompile(`\$\{?REPO_DIR\}?/([A-Za-z0-9._/-]+)`)

const (
	maxOperationInputFiles = 1024
	maxOperationInputBytes = int64(64 << 20)
)

var operationRuntimeRoots = map[string]bool{
	".git":      true,
	".tmp":      true,
	"generated": true,
	"logs":      true,
	"releases":  true,
	"runs":      true,
}

func collectInputClosure(root string, spec Spec) ([]InputFile, error) {
	plan, err := experimentplan.Build(speccatalog.New(root), spec.ExperimentSpec)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	queue := []string{}
	var totalBytes int64
	var add func(string, bool, bool) error
	add = func(relative string, required bool, recursive bool) error {
		relative = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(relative))))
		if relative == "." || !evidence.IsPortablePath(relative) {
			if required {
				return fmt.Errorf("operation input path is not portable: %q", relative)
			}
			return nil
		}
		if operationInputPathForbidden(relative) {
			if required {
				return fmt.Errorf("operation input path belongs to a runtime-output root: %s", relative)
			}
			return nil
		}
		if paths[relative] {
			return nil
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			if !required && os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("inspect operation input %s: %w", relative, err)
		}
		if info.IsDir() {
			// Textual REPO_DIR references often stop at a dynamic runtime
			// suffix, for example $REPO_DIR/runs/$RUN_ID. Treating that
			// static prefix as a source dependency recursively captured the
			// entire mutable workspace. Directory traversal is therefore an
			// explicit capability granted only to the declared profile tree.
			if !recursive {
				if required {
					return fmt.Errorf("operation input reference must name a regular file: %s", relative)
				}
				return nil
			}
			return filepath.WalkDir(path, func(child string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				childRel, err := filepath.Rel(root, child)
				if err != nil {
					return err
				}
				return add(filepath.ToSlash(childRel), true, false)
			})
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("operation input must be a regular non-symlink file: %s", relative)
		}
		if len(paths) >= maxOperationInputFiles {
			return fmt.Errorf("operation input closure exceeds %d files", maxOperationInputFiles)
		}
		if info.Size() > maxOperationInputBytes-totalBytes {
			return fmt.Errorf("operation input closure exceeds %d bytes", maxOperationInputBytes)
		}
		paths[relative] = true
		totalBytes += info.Size()
		queue = append(queue, relative)
		return nil
	}
	if err := add(spec.Path, true, false); err != nil {
		return nil, err
	}
	if err := add(filepath.ToSlash(filepath.Join("experiments", filepath.FromSlash(spec.ExperimentSpec)+".env")), true, false); err != nil {
		return nil, err
	}
	for _, fixed := range []string{".env.example", "compose.yaml", "scripts/run_experiment.sh", "scripts/run_workload.sh", "scripts/runtime.sh", "scripts/psql.sh", "scripts/apply_pg_config.sh"} {
		if err := add(fixed, false, false); err != nil {
			return nil, err
		}
	}
	if value := plan.Fields["topology"]; value != "" && !strings.Contains(value, "$") {
		if err := add(filepath.ToSlash(filepath.Join("topologies", filepath.FromSlash(value)+".env")), true, false); err != nil {
			return nil, err
		}
	}
	if value := plan.Fields["pg_config"]; value != "" && !strings.Contains(value, "$") {
		if err := add(filepath.ToSlash(filepath.Join("configs", filepath.FromSlash(value), "postgresql.conf")), true, false); err != nil {
			return nil, err
		}
	}
	if value := plan.Fields["profile"]; value != "" && !strings.Contains(value, "$") {
		if err := add(filepath.ToSlash(filepath.Join("profiles", filepath.FromSlash(value))), true, true); err != nil {
			return nil, err
		}
	}
	workloads := append([]string{plan.Fields["workload"]}, strings.Fields(plan.Fields["backgrounds"])...)
	for _, workload := range workloads {
		if workload == "" || strings.Contains(workload, "$") {
			continue
		}
		if err := add(filepath.ToSlash(filepath.Join("workloads", filepath.FromSlash(workload)+".env")), true, false); err != nil {
			return nil, err
		}
	}
	if value := plan.Fields["dataset"]; value != "" && !strings.Contains(value, "$") {
		if err := add(filepath.ToSlash(filepath.Join("datasets", filepath.FromSlash(value)+".env")), true, false); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"EXPERIMENT_BEFORE_SQL_FILES", "EXPERIMENT_AFTER_SQL_FILES", "EXPERIMENT_ASSERT_SQL_FILES"} {
		for _, relative := range strings.Fields(plan.Spec.Values[key]) {
			if strings.Contains(relative, "$") {
				return nil, fmt.Errorf("dynamic %s cannot enter operation input closure", key)
			}
			if err := add(relative, true, false); err != nil {
				return nil, err
			}
		}
	}
	for cursor := 0; cursor < len(queue); cursor++ {
		path := filepath.Join(root, filepath.FromSlash(queue[cursor]))
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, match := range repoReferencePattern.FindAllSubmatch(content, -1) {
			if err := add(string(match[1]), false, false); err != nil {
				return nil, err
			}
		}
		if filepath.Ext(path) == ".env" {
			values, err := envfile.Parse(path)
			if err != nil {
				return nil, err
			}
			if values["WORKLOAD_KIND"] == "shell" {
				matches := repoReferencePattern.FindAllStringSubmatch(values["WORKLOAD_CMD"], -1)
				if len(matches) == 0 {
					return nil, fmt.Errorf("shell workload has no canonical REPO_DIR executable reference: %s", queue[cursor])
				}
				for _, match := range matches {
					if err := add(match[1], true, false); err != nil {
						return nil, err
					}
				}
			}
			for _, key := range []string{"SQL", "WORKLOAD_SQL", "PGBENCH_SCRIPT", "DATASET_SQL"} {
				value := strings.TrimSpace(values[key])
				if value == "" || strings.Contains(value, "$") {
					continue
				}
				if key == "WORKLOAD_SQL" && values["WORKLOAD_KIND"] == "profile-sql" {
					value = filepath.ToSlash(filepath.Join("profiles", values["PROFILE"], "sql", value))
				}
				if err := add(value, true, false); err != nil {
					return nil, err
				}
			}
		}
	}
	relatives := make([]string, 0, len(paths))
	for relative := range paths {
		relatives = append(relatives, relative)
	}
	sort.Strings(relatives)
	inputs := make([]InputFile, 0, len(relatives))
	for _, relative := range relatives {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		digest, err := evidence.DigestFile(path)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, InputFile{Path: relative, Size: info.Size(), Digest: digest})
	}
	return inputs, nil
}

func operationInputPathForbidden(relative string) bool {
	first, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
	return first == ".env" || operationRuntimeRoots[first]
}

func inputClosureDigest(inputs []InputFile) (string, error) {
	content, err := json.Marshal(inputs)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func snapshotInputs(root, seriesDir string, inputs []InputFile) error {
	for _, input := range inputs {
		destination := filepath.Join(seriesDir, "inputs", filepath.FromSlash(input.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := snapshotFile(filepath.Join(root, filepath.FromSlash(input.Path)), destination); err != nil {
			return err
		}
	}
	return nil
}

func verifyLiveInputs(root string, inputs []InputFile) error {
	for _, input := range inputs {
		path := filepath.Join(root, filepath.FromSlash(input.Path))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != input.Size {
			return fmt.Errorf("operation input changed or became unsafe: %s", input.Path)
		}
		digest, err := evidence.DigestFile(path)
		if err != nil || digest != input.Digest {
			return fmt.Errorf("operation input digest changed: %s", input.Path)
		}
	}
	return nil
}
