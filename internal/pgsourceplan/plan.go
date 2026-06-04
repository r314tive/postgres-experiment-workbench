package pgsourceplan

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/patchsetcatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

const (
	defaultRepoURL       = "https://github.com/postgres/postgres.git"
	defaultConfigureArgs = "--enable-debug --enable-cassert --enable-tap-tests"
	defaultBuildCflags   = "-O0 -g"
)

type Options struct {
	Action       string
	WorkloadSpec string
	Env          map[string]string
	Now          func() time.Time
	CPUCount     func() int
}

type Plan struct {
	Action              string
	RepoURL             string
	Ref                 string
	SourceRunDir        string
	SourceDir           string
	InstallDir          string
	ArtifactDir         string
	Patchset            string
	PatchsetSpecFile    string
	PatchsetDescription string
	PatchDir            string
	CheckTarget         string
	MakeJobs            string
	CloneDepth          string
	ConfigureArgs       string
	BuildCflags         string
	TestInitdbExtraOpts string
	SourceKeepGoing     string
	PatchFiles          []string
}

func Build(root string, options Options) (Plan, error) {
	env := copyMap(options.Env)
	if env == nil {
		env = EnvFromOS()
	}
	values := copyMap(env)
	if values == nil {
		values = make(map[string]string)
	}

	if options.WorkloadSpec != "" {
		if err := mergeWorkloadSpec(root, values, env, options.WorkloadSpec); err != nil {
			return Plan{}, err
		}
	}

	patchsetID := values["PG_PATCHSET"]
	patchCatalog := patchsetcatalog.New(root)
	patchset := patchsetcatalog.Metadata{}
	if patchsetID != "" {
		metadata, err := patchCatalog.Show(patchsetID)
		if err != nil {
			return Plan{}, err
		}
		patchset = metadata
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}
	cpuCount := options.CPUCount
	if cpuCount == nil {
		cpuCount = runtime.NumCPU
	}

	action := valueOr(values["PG_SOURCE_ACTION"], "run")
	if options.Action != "" {
		action = options.Action
	}
	ref := valueOr(values["PG_REF"], valueOr(patchset.PgRef, "master"))
	runID := valueOr(values["PG_SOURCE_RUN_ID"], fmt.Sprintf("pg-%s-%s", ref, now().UTC().Format("20060102_150405")))
	sourceRunDir := resolvePath(root, valueOr(values["PG_SOURCE_RUN_DIR"], filepath.Join("generated", "pg-source", runID)))
	sourceDir := resolvePath(root, valueOr(values["PG_SOURCE_DIR"], filepath.Join(sourceRunDir, "src")))
	installDir := resolvePath(root, valueOr(values["PG_INSTALL_DIR"], filepath.Join(sourceRunDir, "install")))
	artifactDir := resolvePath(root, valueOr(values["PG_ARTIFACT_DIR"], filepath.Join(sourceRunDir, "artifacts")))
	patchDir := resolvePath(root, valueOr(values["PG_PATCH_DIR"], patchset.Dir))
	makeJobs := valueOr(values["PG_MAKE_JOBS"], fmt.Sprintf("%d", cpuCount()))

	var patchFiles []string
	if patchDir != "" {
		files, err := patchsetcatalog.ResolveEntries(patchDir, patchset.Files)
		if err != nil {
			return Plan{}, err
		}
		patchFiles = files
	}

	return Plan{
		Action:              action,
		RepoURL:             valueOr(values["PG_REPO_URL"], defaultRepoURL),
		Ref:                 ref,
		SourceRunDir:        sourceRunDir,
		SourceDir:           sourceDir,
		InstallDir:          installDir,
		ArtifactDir:         artifactDir,
		Patchset:            patchsetID,
		PatchsetSpecFile:    patchset.SpecFile,
		PatchsetDescription: patchset.Description,
		PatchDir:            patchDir,
		CheckTarget:         valueOr(values["PG_CHECK_TARGET"], "check"),
		MakeJobs:            makeJobs,
		CloneDepth:          valueOr(values["PG_CLONE_DEPTH"], "1"),
		ConfigureArgs:       valueOr(values["PG_CONFIGURE_ARGS"], valueOr(patchset.ConfigureArgs, defaultConfigureArgs)),
		BuildCflags:         valueOr(values["PG_BUILD_CFLAGS"], valueOr(patchset.BuildCflags, defaultBuildCflags)),
		TestInitdbExtraOpts: valueOr(values["PG_TEST_INITDB_EXTRA_OPTS"], patchset.TestInitdbExtraOpts),
		SourceKeepGoing:     valueOr(values["PG_SOURCE_KEEP_GOING"], "1"),
		PatchFiles:          patchFiles,
	}, nil
}

func Render(w io.Writer, plan Plan) error {
	lines := []string{
		fmt.Sprintf("PG_SOURCE_ACTION=%s", plan.Action),
		fmt.Sprintf("PG_REPO_URL=%s", plan.RepoURL),
		fmt.Sprintf("PG_REF=%s", plan.Ref),
		fmt.Sprintf("PG_SOURCE_RUN_DIR=%s", plan.SourceRunDir),
		fmt.Sprintf("PG_SOURCE_DIR=%s", plan.SourceDir),
		fmt.Sprintf("PG_INSTALL_DIR=%s", plan.InstallDir),
		fmt.Sprintf("PG_ARTIFACT_DIR=%s", plan.ArtifactDir),
		fmt.Sprintf("PG_PATCHSET=%s", plan.Patchset),
		fmt.Sprintf("PATCHSET_SPEC_FILE=%s", plan.PatchsetSpecFile),
		fmt.Sprintf("PATCHSET_DESCRIPTION=%s", plan.PatchsetDescription),
		fmt.Sprintf("PG_PATCH_DIR=%s", plan.PatchDir),
		fmt.Sprintf("PG_CHECK_TARGET=%s", plan.CheckTarget),
		fmt.Sprintf("PG_MAKE_JOBS=%s", plan.MakeJobs),
		fmt.Sprintf("PG_CLONE_DEPTH=%s", plan.CloneDepth),
		fmt.Sprintf("PG_CONFIGURE_ARGS=%s", plan.ConfigureArgs),
		fmt.Sprintf("PG_BUILD_CFLAGS=%s", plan.BuildCflags),
		fmt.Sprintf("PG_TEST_INITDB_EXTRA_OPTS=%s", plan.TestInitdbExtraOpts),
		fmt.Sprintf("PG_SOURCE_KEEP_GOING=%s", plan.SourceKeepGoing),
	}
	if _, err := fmt.Fprintln(w, strings.Join(lines, "\n")); err != nil {
		return err
	}
	if len(plan.PatchFiles) == 0 {
		_, err := fmt.Fprintln(w, "PG_PATCH_FILES=(none)")
		return err
	}
	_, err := fmt.Fprintf(w, "PG_PATCH_FILES=%s\n", strings.Join(plan.PatchFiles, " "))
	return err
}

func EnvFromOS() map[string]string {
	env := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func mergeWorkloadSpec(root string, values map[string]string, env map[string]string, workloadSpec string) error {
	spec, err := speccatalog.New(root).Show("workload", workloadSpec)
	if err != nil {
		return err
	}
	kind := expandDefault(spec.Values["WORKLOAD_KIND"], env)
	if kind != "pg-source-check" {
		return fmt.Errorf("workload spec is not WORKLOAD_KIND=pg-source-check: %s", workloadSpec)
	}

	for key, raw := range spec.Values {
		if isSourceOverride(key) {
			if _, ok := env[key]; ok {
				continue
			}
		}
		values[key] = expandDefault(raw, env)
	}
	return nil
}

func expandDefault(value string, env map[string]string) string {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return value
	}
	body := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	name, fallback, ok := strings.Cut(body, ":-")
	if !ok || name == "" {
		return value
	}
	if envValue := env[name]; envValue != "" {
		return envValue
	}
	return fallback
}

func isSourceOverride(key string) bool {
	return strings.HasPrefix(key, "PG_")
}

func resolvePath(root string, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func valueOr(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func copyMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
