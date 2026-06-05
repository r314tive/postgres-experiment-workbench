package speccatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
)

type Catalog struct {
	Root string
}

type Spec struct {
	Kind   string
	ID     string
	Path   string
	Values map[string]string
}

type Kind struct {
	Name string
	Root string
}

var Kinds = []Kind{
	{Name: "workload", Root: "workloads"},
	{Name: "experiment", Root: "experiments"},
	{Name: "matrix", Root: "matrices"},
	{Name: "topology", Root: "topologies"},
	{Name: "dataset", Root: "datasets"},
}

var kindRoots = map[string]string{
	"workload":   "workloads",
	"experiment": "experiments",
	"matrix":     "matrices",
	"topology":   "topologies",
	"dataset":    "datasets",
}

func New(root string) Catalog {
	return Catalog{Root: root}
}

func (c Catalog) List(kind string) ([]string, error) {
	root, err := c.kindRoot(kind)
	if err != nil {
		return nil, err
	}
	var specs []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".env" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		specs = append(specs, strings.TrimSuffix(filepath.ToSlash(rel), ".env"))
		return nil
	}); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(specs)
	return specs, nil
}

func (c Catalog) ListRaw(kind string) ([]string, error) {
	root, err := c.kindRoot(kind)
	if err != nil {
		return nil, err
	}
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".env" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(paths)
	specs := make([]string, 0, len(paths))
	for _, path := range paths {
		specs = append(specs, strings.TrimSuffix(path, ".env"))
	}
	return specs, nil
}

func (c Catalog) Show(kind string, id string) (Spec, error) {
	path, resolvedID, err := c.Resolve(kind, id)
	if err != nil {
		return Spec{}, err
	}
	values, err := envfile.Parse(path)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Kind: kind, ID: resolvedID, Path: path, Values: values}, nil
}

func (c Catalog) ShowRaw(kind string, id string) ([]byte, error) {
	path, _, err := c.Resolve(kind, id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (c Catalog) Resolve(kind string, input string) (string, string, error) {
	root, err := c.kindRoot(kind)
	if err != nil {
		return "", "", err
	}

	candidates := []string{input}
	if !filepath.IsAbs(input) {
		candidates = append(candidates,
			filepath.Join(c.Root, input),
			filepath.Join(root, input),
			filepath.Join(root, input+".env"),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			id, err := specID(root, candidate)
			return candidate, id, err
		}
	}

	list, err := c.List(kind)
	if err != nil {
		return "", "", err
	}
	var matches []string
	for _, id := range list {
		if id == input || filepath.Base(id) == input {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		path := filepath.Join(root, filepath.FromSlash(matches[0])+".env")
		return path, matches[0], nil
	}
	if len(matches) > 1 {
		return "", "", fmt.Errorf("ambiguous %s spec: %s: %s", kind, input, strings.Join(matches, ", "))
	}

	return "", "", fmt.Errorf("%s spec not found: %s", kind, input)
}

func (c Catalog) Validate(kind string, ids []string) []error {
	if kind == "all" || kind == "" {
		var errs []error
		for _, item := range Kinds {
			errs = append(errs, c.Validate(item.Name, nil)...)
		}
		return errs
	}

	var specs []Spec
	if len(ids) == 0 {
		list, err := c.List(kind)
		if err != nil {
			return []error{err}
		}
		for _, id := range list {
			spec, err := c.Show(kind, id)
			if err != nil {
				return []error{err}
			}
			specs = append(specs, spec)
		}
	} else {
		for _, id := range ids {
			spec, err := c.Show(kind, id)
			if err != nil {
				return []error{err}
			}
			specs = append(specs, spec)
		}
	}

	var errs []error
	for _, spec := range specs {
		errs = append(errs, c.validateSpec(spec)...)
	}
	return errs
}

func (c Catalog) validateSpec(spec Spec) []error {
	switch spec.Kind {
	case "workload":
		return c.validateWorkload(spec)
	case "experiment":
		return c.validateExperiment(spec)
	case "matrix":
		return c.validateMatrix(spec)
	case "topology":
		return c.validateTopology(spec)
	case "dataset":
		return c.validateDataset(spec)
	default:
		return []error{fmt.Errorf("unsupported spec kind: %s", spec.Kind)}
	}
}

func (c Catalog) validateWorkload(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "WORKLOAD_NAME")
	kind := requireValue(&errs, spec, "WORKLOAD_KIND")
	if kind != "" && !oneOf(kind, "profile-sql", "sql", "pgbench", "pg-source-check", "noisia", "shell", "compose-run") {
		errs = append(errs, specError(spec, "unsupported WORKLOAD_KIND: %s", kind))
	}

	switch kind {
	case "profile-sql":
		profile := requireValue(&errs, spec, "PROFILE")
		if profile != "" {
			if !c.dirExists("profiles", profile) {
				errs = append(errs, specError(spec, "PROFILE not found: %s", profile))
			}
			sqlName := valueOr(spec.Values["WORKLOAD_SQL"], "10_run.sql")
			if !isDynamic(sqlName) && !c.fileExists("profiles", profile, "sql", sqlName) {
				errs = append(errs, specError(spec, "profile SQL not found: profiles/%s/sql/%s", profile, sqlName))
			}
		}
	case "sql":
		sqlPath := firstValue(spec.Values, "SQL", "WORKLOAD_SQL")
		if sqlPath == "" {
			errs = append(errs, specError(spec, "SQL or WORKLOAD_SQL is required for WORKLOAD_KIND=sql"))
		} else if !isDynamic(sqlPath) && !c.pathExists(sqlPath) {
			errs = append(errs, specError(spec, "SQL file not found: %s", sqlPath))
		}
	case "pgbench":
		script := spec.Values["PGBENCH_SCRIPT"]
		if script != "" && !isDynamic(script) && !c.pathExists(script) {
			errs = append(errs, specError(spec, "PGBENCH_SCRIPT not found: %s", script))
		}
	case "pg-source-check":
		action := valueOr(spec.Values["PG_SOURCE_ACTION"], "run")
		if !isDynamic(action) && !oneOf(action, "plan", "run", "scan") {
			errs = append(errs, specError(spec, "unsupported PG_SOURCE_ACTION: %s", action))
		}
		patchset := spec.Values["PG_PATCHSET"]
		if patchset != "" && !isDynamic(patchset) && !c.fileExists("patchsets", filepath.FromSlash(patchset), "patchset.env") {
			errs = append(errs, specError(spec, "PG_PATCHSET not found: %s", patchset))
		}
		patchDir := spec.Values["PG_PATCH_DIR"]
		if patchDir != "" && !isDynamic(patchDir) && !c.pathExists(patchDir) {
			errs = append(errs, specError(spec, "PG_PATCH_DIR not found: %s", patchDir))
		}
	case "noisia":
		workload := requireValue(&errs, spec, "NOISIA_WORKLOAD")
		if workload != "" && !oneOf(workload, "wait-xacts", "temp-files") {
			errs = append(errs, specError(spec, "unsupported NOISIA_WORKLOAD: %s", workload))
		}
	case "shell":
		requireValue(&errs, spec, "WORKLOAD_CMD")
	case "compose-run":
		requireValue(&errs, spec, "WORKLOAD_IMAGE")
		requireValue(&errs, spec, "WORKLOAD_COMMAND")
	}
	return errs
}

func (c Catalog) validateExperiment(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "EXPERIMENT_NAME")

	topology := valueOr(spec.Values["EXPERIMENT_TOPOLOGY"], "single")
	if !isDynamic(topology) && !oneOf(topology, "single", "primary-replica", "logical-replication", "pgbouncer", "multi-version-upgrade", "source-tree") {
		errs = append(errs, specError(spec, "unsupported EXPERIMENT_TOPOLOGY: %s", topology))
	}
	if topology != "source-tree" && !isDynamic(topology) && !c.specExists("topology", topology) {
		errs = append(errs, specError(spec, "topology spec not found: %s", topology))
	}

	pgConfig := valueOr(spec.Values["EXPERIMENT_PG_CONFIG"], "default")
	if !isDynamic(pgConfig) && !c.fileExists("configs", pgConfig, "postgresql.conf") {
		errs = append(errs, specError(spec, "PostgreSQL config not found: %s", pgConfig))
	}

	stateWriter := valueOr(spec.Values["EXPERIMENT_STATE_WRITER"], "go")
	if !isDynamic(stateWriter) && !oneOf(stateWriter, "auto", "go", "shell") {
		errs = append(errs, specError(spec, "unsupported EXPERIMENT_STATE_WRITER: %s", stateWriter))
	}

	profile := spec.Values["EXPERIMENT_PROFILE"]
	if profile != "" && !isDynamic(profile) && !c.dirExists("profiles", profile) {
		errs = append(errs, specError(spec, "profile not found: %s", profile))
	}

	dataset := spec.Values["EXPERIMENT_DATASET_SPEC"]
	if dataset != "" && !isDynamic(dataset) && !c.specExists("dataset", dataset) {
		errs = append(errs, specError(spec, "dataset spec not found: %s", dataset))
	}

	workload := spec.Values["EXPERIMENT_WORKLOAD_SPEC"]
	if workload != "" && !isDynamic(workload) && !c.specExists("workload", workload) {
		errs = append(errs, specError(spec, "workload spec not found: %s", workload))
	}

	for _, background := range splitWords(spec.Values["EXPERIMENT_BACKGROUND_SPECS"]) {
		if !isDynamic(background) && !c.specExists("workload", background) {
			errs = append(errs, specError(spec, "background workload spec not found: %s", background))
		}
	}

	for _, sqlPath := range splitWords(spec.Values["EXPERIMENT_ASSERT_SQL_FILES"]) {
		if !isDynamic(sqlPath) && !c.pathExists(sqlPath) {
			errs = append(errs, specError(spec, "assert SQL file not found: %s", sqlPath))
		}
	}
	return errs
}

func (c Catalog) validateMatrix(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "MATRIX_NAME")
	for _, experiment := range splitWords(valueOr(spec.Values["MATRIX_EXPERIMENTS"], "smoke")) {
		if !isDynamic(experiment) && !c.specExists("experiment", experiment) {
			errs = append(errs, specError(spec, "experiment spec not found: %s", experiment))
		}
	}
	for _, pgConfig := range splitWords(valueOr(spec.Values["MATRIX_PG_CONFIGS"], "default")) {
		if !isDynamic(pgConfig) && !c.fileExists("configs", pgConfig, "postgresql.conf") {
			errs = append(errs, specError(spec, "PostgreSQL config not found: %s", pgConfig))
		}
	}
	for _, profileSize := range splitWords(valueOr(spec.Values["MATRIX_PROFILE_SIZES"], "small")) {
		if !isDynamic(profileSize) && !oneOf(profileSize, "small", "medium", "large") {
			errs = append(errs, specError(spec, "unsupported MATRIX_PROFILE_SIZE: %s", profileSize))
		}
	}
	repeats := valueOr(spec.Values["MATRIX_REPEATS"], "1")
	if !isDynamic(repeats) && !positiveInt(repeats) {
		errs = append(errs, specError(spec, "MATRIX_REPEATS must be a positive integer: %s", repeats))
	}
	return errs
}

func (c Catalog) validateTopology(spec Spec) []error {
	var errs []error
	name := requireValue(&errs, spec, "TOPOLOGY_NAME")
	requireValue(&errs, spec, "TOPOLOGY_DESCRIPTION")
	if name != "" && name != spec.ID {
		errs = append(errs, specError(spec, "TOPOLOGY_NAME must match spec id %q, got %q", spec.ID, name))
	}
	if name != "" && !oneOf(name, "single", "primary-replica", "logical-replication", "pgbouncer", "multi-version-upgrade") {
		errs = append(errs, specError(spec, "unsupported TOPOLOGY_NAME: %s", name))
	}
	return errs
}

func (c Catalog) validateDataset(spec Spec) []error {
	var errs []error
	requireValue(&errs, spec, "DATASET_NAME")
	kind := requireValue(&errs, spec, "DATASET_KIND")
	if kind != "" && !oneOf(kind, "sql", "profile", "pgbench") {
		errs = append(errs, specError(spec, "unsupported DATASET_KIND: %s", kind))
	}
	switch kind {
	case "sql":
		sqlPath := requireValue(&errs, spec, "DATASET_SQL")
		if sqlPath != "" && !isDynamic(sqlPath) && !c.pathExists(sqlPath) {
			errs = append(errs, specError(spec, "DATASET_SQL not found: %s", sqlPath))
		}
	case "profile":
		profile := requireValue(&errs, spec, "DATASET_PROFILE")
		if profile != "" && !isDynamic(profile) && !c.dirExists("profiles", profile) {
			errs = append(errs, specError(spec, "DATASET_PROFILE not found: %s", profile))
		}
	}
	return errs
}

func (c Catalog) kindRoot(kind string) (string, error) {
	root, ok := kindRoots[kind]
	if !ok {
		return "", fmt.Errorf("unsupported spec kind: %s", kind)
	}
	return filepath.Join(c.Root, root), nil
}

func (c Catalog) specExists(kind string, id string) bool {
	_, _, err := c.Resolve(kind, id)
	return err == nil
}

func (c Catalog) dirExists(parts ...string) bool {
	info, err := os.Stat(filepath.Join(append([]string{c.Root}, parts...)...))
	return err == nil && info.IsDir()
}

func (c Catalog) fileExists(parts ...string) bool {
	info, err := os.Stat(filepath.Join(append([]string{c.Root}, parts...)...))
	return err == nil && !info.IsDir()
}

func (c Catalog) pathExists(path string) bool {
	if filepath.IsAbs(path) {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	info, err := os.Stat(filepath.Join(c.Root, path))
	return err == nil && !info.IsDir()
}

func specID(root string, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), ".env"), nil
}

func requireValue(errs *[]error, spec Spec, key string) string {
	value := spec.Values[key]
	if value == "" {
		*errs = append(*errs, specError(spec, "%s is required", key))
	}
	return value
}

func specError(spec Spec, format string, args ...interface{}) error {
	return fmt.Errorf("%s:%s: %s", spec.Kind, spec.ID, fmt.Sprintf(format, args...))
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func firstValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values[key] != "" {
			return values[key]
		}
	}
	return ""
}

func splitWords(value string) []string {
	if value == "" || isDynamic(value) {
		return nil
	}
	return strings.Fields(value)
}

func isDynamic(value string) bool {
	return strings.Contains(value, "$")
}

func positiveInt(value string) bool {
	if value == "" {
		return false
	}
	for i, ch := range value {
		if ch < '0' || ch > '9' || (i == 0 && ch == '0') {
			return false
		}
	}
	return true
}
