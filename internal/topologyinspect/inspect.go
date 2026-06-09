package topologyinspect

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/envfile"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

var topologyProfiles = map[string][]string{
	"single":                nil,
	"primary-replica":       {"replica"},
	"logical-replication":   {"logical"},
	"pgbouncer":             {"pgbouncer"},
	"multi-version-upgrade": {"upgrade"},
	"source-tree":           nil,
}

type Options struct {
	Env map[string]string
}

type Inspection struct {
	ID              string
	Name            string
	Description     string
	SpecFile        string
	EnvFile         string
	ComposeCommand  []string
	Services        []string
	Profiles        []string
	ResolvedValues  map[string]string
	UpCommand       []string
	StatusCommand   []string
	DownAllProfiles []string
}

func Inspect(root string, topology string, options Options) (Inspection, error) {
	if topology == "" {
		topology = "single"
	}

	spec, err := speccatalog.New(root).Show("topology", topology)
	if err != nil {
		if topology != "source-tree" {
			return Inspection{}, err
		}
		_, specErr := os.Stat(filepath.Join(root, "topologies", topology+".env"))
		if !os.IsNotExist(specErr) {
			return Inspection{}, err
		}
		spec = speccatalog.Spec{
			Kind: "topology",
			ID:   topology,
			Values: map[string]string{
				"TOPOLOGY_NAME":        "source-tree",
				"TOPOLOGY_DESCRIPTION": "Source-tree topology.",
			},
		}
	}

	envFile, envValues, err := loadRepoEnv(root, options.Env)
	if err != nil {
		return Inspection{}, err
	}
	composeCommand := strings.Fields(valueOr(envValues["COMPOSE"], "docker compose"))
	composeArgs := []string(nil)
	if envFile != "" {
		composeArgs = append(composeArgs, "--env-file", envFile)
	}

	resolved := make(map[string]string, len(spec.Values))
	for key, value := range spec.Values {
		resolved[key] = expandDefault(value, envValues)
	}

	name := valueOr(resolved["TOPOLOGY_NAME"], topology)
	profiles, ok := topologyProfiles[name]
	if !ok {
		return Inspection{}, fmt.Errorf("unsupported topology: %s", name)
	}
	services := strings.Fields(resolved["TOPOLOGY_SERVICES"])
	if len(services) == 0 {
		services = []string{"postgres"}
	}

	base := append([]string(nil), composeCommand...)
	base = append(base, composeArgs...)
	profileArgs := profileFlags(profiles)

	return Inspection{
		ID:              topology,
		Name:            name,
		Description:     resolved["TOPOLOGY_DESCRIPTION"],
		SpecFile:        spec.Path,
		EnvFile:         envFile,
		ComposeCommand:  append([]string(nil), composeCommand...),
		Services:        services,
		Profiles:        profiles,
		ResolvedValues:  resolved,
		UpCommand:       append(append(append([]string(nil), base...), profileArgs...), append([]string{"up", "-d"}, services...)...),
		StatusCommand:   append(append(append([]string(nil), base...), profileArgs...), append([]string{"ps"}, services...)...),
		DownAllProfiles: []string{"replica", "logical", "pgbouncer", "upgrade", "workload"},
	}, nil
}

func Render(w io.Writer, inspection Inspection) error {
	lines := []string{
		fmt.Sprintf("TOPOLOGY_ID=%s", inspection.ID),
		fmt.Sprintf("TOPOLOGY_NAME=%s", inspection.Name),
		fmt.Sprintf("TOPOLOGY_DESCRIPTION=%s", inspection.Description),
		fmt.Sprintf("TOPOLOGY_SPEC_FILE=%s", inspection.SpecFile),
		fmt.Sprintf("TOPOLOGY_ENV_FILE=%s", inspection.EnvFile),
		fmt.Sprintf("TOPOLOGY_COMPOSE=%s", strings.Join(inspection.ComposeCommand, " ")),
		fmt.Sprintf("TOPOLOGY_PROFILES=%s", strings.Join(inspection.Profiles, " ")),
		fmt.Sprintf("TOPOLOGY_SERVICES=%s", strings.Join(inspection.Services, " ")),
		fmt.Sprintf("TOPOLOGY_UP_COMMAND=%s", strings.Join(inspection.UpCommand, " ")),
		fmt.Sprintf("TOPOLOGY_STATUS_COMMAND=%s", strings.Join(inspection.StatusCommand, " ")),
		fmt.Sprintf("TOPOLOGY_DOWN_ALL_PROFILES=%s", strings.Join(inspection.DownAllProfiles, " ")),
	}
	if _, err := fmt.Fprintln(w, strings.Join(lines, "\n")); err != nil {
		return err
	}

	keys := make([]string, 0, len(inspection.ResolvedValues))
	for key := range inspection.ResolvedValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(w, "RESOLVED_%s=%s\n", key, inspection.ResolvedValues[key]); err != nil {
			return err
		}
	}
	return nil
}

func loadRepoEnv(root string, overrides map[string]string) (string, map[string]string, error) {
	envPath := ""
	if value := overrides["ENV_FILE"]; value != "" {
		envPath = value
		if !filepath.IsAbs(envPath) {
			envPath = filepath.Join(root, envPath)
		}
	} else if exists(filepath.Join(root, ".env")) {
		envPath = filepath.Join(root, ".env")
	} else if exists(filepath.Join(root, ".env.example")) {
		envPath = filepath.Join(root, ".env.example")
	}

	values := map[string]string{}
	if envPath != "" && exists(envPath) {
		parsed, err := envfile.Parse(envPath)
		if err != nil {
			return "", nil, err
		}
		values = parsed
	}
	for key, value := range overrides {
		values[key] = value
	}
	return envPath, values, nil
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

func profileFlags(profiles []string) []string {
	var args []string
	for _, profile := range profiles {
		args = append(args, "--profile", profile)
	}
	return args
}

func EnvFromOS() map[string]string {
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func valueOr(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
