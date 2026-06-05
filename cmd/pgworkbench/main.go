package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/datasetplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/diagnosticcatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/doctor"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/failurescan"
	"github.com/r314tive/postgres-experiment-workbench/internal/matrixplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/metricsplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/patchsetcatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgsourcecheck"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgsourceplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/profilecatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/profileplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/runbundle"
	"github.com/r314tive/postgres-experiment-workbench/internal/runcatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/runreport"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/topologyinspect"
	"github.com/r314tive/postgres-experiment-workbench/internal/workloadbg"
	"github.com/r314tive/postgres-experiment-workbench/internal/workloadplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/workloadrun"
)

var version = "dev"
var commit = "unknown"
var builtAt = "unknown"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, failurescan.ErrEvidenceFound) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
		return nil
	}

	root, err := findRepoRoot()
	if err != nil {
		return err
	}

	switch args[0] {
	case "version":
		fmt.Printf("pgworkbench version=%s commit=%s built_at=%s\n", version, commit, builtAt)
		return nil
	case "doctor":
		return runDoctor(root, args[1:])
	case "dataset":
		return runDataset(root, speccatalog.New(root), args[1:])
	case "diagnostics":
		return runDiagnostics(root, args[1:])
	case "patchset":
		return runPatchset(patchsetcatalog.New(root), args[1:])
	case "profile":
		return runProfile(root, profilecatalog.New(root), args[1:])
	case "workload":
		return runWorkload(root, speccatalog.New(root), args[1:])
	case "experiment":
		return runExperiment(root, speccatalog.New(root), args[1:])
	case "matrix":
		return runMatrix(speccatalog.New(root), args[1:])
	case "metrics":
		return runMetrics(root, args[1:])
	case "source":
		return runSource(root, args[1:])
	case "topology":
		return runTopology(root, speccatalog.New(root), args[1:])
	case "scan":
		return runScan(root, args[1:])
	case "report":
		return runReport(root, args[1:])
	case "run":
		return runState(root, args[1:])
	case "spec":
		return runSpec(speccatalog.New(root), args[1:])
	default:
		return fmt.Errorf("unsupported command: %s", args[0])
	}
}

func usage() {
	fmt.Println(`Usage:
  pgworkbench version
  pgworkbench doctor [--skip-docker-daemon]
  pgworkbench dataset list [--raw]
  pgworkbench dataset show [--raw] <dataset>
  pgworkbench dataset validate [dataset...]
  pgworkbench dataset plan [--json|--raw] <dataset>
  pgworkbench diagnostics list
  pgworkbench diagnostics show <diagnostic>
  pgworkbench patchset list
  pgworkbench patchset show <patchset>
  pgworkbench patchset validate [patchset...]
  pgworkbench profile list
  pgworkbench profile show <profile>
  pgworkbench profile validate [profile...]
  pgworkbench profile plan [--json] [--size <size>] [--seconds <seconds>] <profile> [sql-file...]
  pgworkbench workload list [--raw]
  pgworkbench workload show [--raw] <workload>
  pgworkbench workload validate [workload...]
  pgworkbench workload plan [--json|--raw] <workload>
  pgworkbench workload run [--json] <workload> [adapter-arg...]
  pgworkbench workload bg status [--json]
  pgworkbench experiment list [--raw]
  pgworkbench experiment show [--raw] <experiment-spec>
  pgworkbench experiment plan [--json] [--expanded] <experiment-spec>
  pgworkbench matrix list [--raw]
  pgworkbench matrix show [--raw] <matrix-spec>
  pgworkbench matrix plan [--json|--raw] <matrix-spec>
  pgworkbench metrics plan [--json] [output.csv]
  pgworkbench topology list [--raw]
  pgworkbench topology show [--raw] <topology>
  pgworkbench topology inspect <topology>
  pgworkbench topology ps <topology>
  pgworkbench source plan [workload-spec]
  pgworkbench source classify <pg-source-run-dir-or-artifact-dir>
  pgworkbench scan failures [path...]
  pgworkbench report run <run-dir-or-id> [output.md]
  pgworkbench report compare [--raw] <baseline-run-dir> <candidate-run-dir>
  pgworkbench report summary [--output output.md] <series-dir|run-dir> [run-dir...]
  pgworkbench report history [--output output.md] <series-dir|run-dir> [series-dir|run-dir...]
  pgworkbench run list [--json] [--status status] [--limit n] [path...]
  pgworkbench run show [--json] <run-dir-or-id>
  pgworkbench run bundle [--json] <run-dir-or-id> [output.tar.gz]
  pgworkbench run verify [--json] <run-dir-or-id>
  pgworkbench run write-manifest --run-dir <run-dir>
  pgworkbench run write-verdict --run-dir <run-dir> --status <status> --message <message> [--finished-at <time>]
  pgworkbench spec list <workload|experiment|matrix|topology|dataset>
  pgworkbench spec show <kind> <spec>
  pgworkbench spec reference [workload|experiment|matrix|topology|dataset|all]
  pgworkbench spec schema [workload|experiment|matrix|topology|dataset|all]
  pgworkbench spec validate [kind] [spec...]`)
}

func runDoctor(root string, args []string) error {
	options := doctor.Options{}
	for _, arg := range args {
		switch arg {
		case "--skip-docker-daemon":
			options.SkipDockerDaemon = true
		default:
			return fmt.Errorf("usage: pgworkbench doctor [--skip-docker-daemon]")
		}
	}

	result := doctor.Run(root, options, doctor.Deps{})
	if err := doctor.Render(os.Stdout, result); err != nil {
		return err
	}
	if !result.Valid() {
		return fmt.Errorf("doctor found failed checks")
	}
	return nil
}

func runKindCatalog(kind string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s action is required", kind)
	}

	switch args[0] {
	case "list":
		raw, inputs, err := parseRawArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 0 {
			return fmt.Errorf("usage: pgworkbench %s list [--raw]", kind)
		}
		var specs []string
		if raw {
			specs, err = catalog.ListRaw(kind)
		} else {
			specs, err = catalog.List(kind)
		}
		if err != nil {
			return err
		}
		for _, spec := range specs {
			fmt.Println(spec)
		}
		return nil
	case "show":
		raw, inputs, err := parseRawArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench %s show [--raw] <%s>", kind, kind)
		}
		if raw {
			content, err := catalog.ShowRaw(kind, inputs[0])
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(content)
			return err
		}
		spec, err := catalog.Show(kind, inputs[0])
		if err != nil {
			return err
		}
		printSpec(spec)
		return nil
	case "validate":
		errs := catalog.Validate(kind, args[1:])
		if len(errs) > 0 {
			for _, err := range errs {
				fmt.Fprintln(os.Stderr, err)
			}
			return fmt.Errorf("%s catalog validation failed", kind)
		}
		fmt.Printf("PASS: %s catalog\n", kind)
		return nil
	default:
		return fmt.Errorf("unsupported %s action: %s", kind, args[0])
	}
}

func parseRawArgs(args []string) (bool, []string, error) {
	raw := false
	var inputs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--raw":
			raw = true
		case "--":
			inputs = append(inputs, args[i+1:]...)
			return raw, inputs, nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return false, nil, fmt.Errorf("unknown option: %s", args[i])
			}
			inputs = append(inputs, args[i])
		}
	}
	return raw, inputs, nil
}

func parseJSONOptionArgs(args []string) (bool, []string, error) {
	jsonOutput := false
	var inputs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--":
			inputs = append(inputs, args[i+1:]...)
			return jsonOutput, inputs, nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return false, nil, fmt.Errorf("unknown option: %s", args[i])
			}
			inputs = append(inputs, args[i])
		}
	}
	return jsonOutput, inputs, nil
}

func parseRunListArgs(args []string) (bool, runcatalog.ListOptions, error) {
	jsonOutput := false
	options := runcatalog.ListOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--status":
			if i+1 >= len(args) {
				return false, options, fmt.Errorf("--status requires a value")
			}
			options.Status = args[i+1]
			i++
		case "--limit":
			if i+1 >= len(args) {
				return false, options, fmt.Errorf("--limit requires a value")
			}
			limit, err := strconv.Atoi(args[i+1])
			if err != nil || limit <= 0 {
				return false, options, fmt.Errorf("--limit must be a positive integer")
			}
			options.Limit = limit
			i++
		case "--":
			options.Inputs = append(options.Inputs, args[i+1:]...)
			return jsonOutput, options, nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return false, options, fmt.Errorf("unknown option: %s", args[i])
			}
			options.Inputs = append(options.Inputs, args[i])
		}
	}
	return jsonOutput, options, nil
}

func runWorkload(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workload action is required")
	}

	switch args[0] {
	case "bg":
		return runWorkloadBG(root, args[1:])
	case "plan":
		jsonOutput := false
		rawOutput := false
		inputs := args[1:]
		for len(inputs) > 0 && strings.HasPrefix(inputs[0], "-") {
			switch inputs[0] {
			case "--json":
				jsonOutput = true
			case "--raw":
				rawOutput = true
			default:
				return fmt.Errorf("unknown option: %s", inputs[0])
			}
			inputs = inputs[1:]
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench workload plan [--json|--raw] <workload>")
		}
		if jsonOutput && rawOutput {
			return fmt.Errorf("--json and --raw cannot be used together")
		}
		plan, err := workloadplan.Build(root, catalog, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return workloadplan.RenderJSON(os.Stdout, plan)
		}
		if rawOutput {
			return workloadplan.RenderRaw(os.Stdout, plan)
		}
		return workloadplan.Render(os.Stdout, plan)
	case "run":
		jsonOutput, spec, adapterArgs, err := parseWorkloadRunArgs(args[1:])
		if err != nil {
			return err
		}
		commandStdout := io.Writer(os.Stdout)
		if jsonOutput {
			commandStdout = os.Stderr
		}
		result, runErr := workloadrun.Run(root, catalog, spec, workloadrun.Options{
			AdapterArgs: adapterArgs,
			Stdout:      commandStdout,
			Stderr:      os.Stderr,
		})
		if result.WorkloadSpec != "" {
			if jsonOutput {
				if err := workloadrun.RenderJSON(os.Stdout, result); err != nil {
					return err
				}
			} else if err := workloadrun.Render(os.Stdout, result); err != nil {
				return err
			}
		}
		if runErr != nil {
			return fmt.Errorf("workload run failed: %w", runErr)
		}
		return nil
	default:
		return runKindCatalog("workload", catalog, args)
	}
}

func parseWorkloadRunArgs(args []string) (bool, string, []string, error) {
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--":
			if i+1 >= len(args) {
				return false, "", nil, fmt.Errorf("usage: pgworkbench workload run [--json] <workload> [adapter-arg...]")
			}
			return jsonOutput, args[i+1], append([]string(nil), args[i+2:]...), nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return false, "", nil, fmt.Errorf("unknown option: %s", args[i])
			}
			return jsonOutput, args[i], append([]string(nil), args[i+1:]...), nil
		}
	}
	return false, "", nil, fmt.Errorf("usage: pgworkbench workload run [--json] <workload> [adapter-arg...]")
}

func runWorkloadBG(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("background workload action is required")
	}
	switch args[0] {
	case "status":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 0 {
			return fmt.Errorf("usage: pgworkbench workload bg status [--json]")
		}
		status := workloadbg.Inspect(root)
		if jsonOutput {
			return workloadbg.RenderJSON(os.Stdout, status)
		}
		return workloadbg.Render(os.Stdout, status)
	default:
		return fmt.Errorf("unsupported background workload action: %s", args[0])
	}
}

func runDataset(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("dataset action is required")
	}

	switch args[0] {
	case "plan":
		jsonOutput := false
		rawOutput := false
		inputs := args[1:]
		for len(inputs) > 0 && strings.HasPrefix(inputs[0], "-") {
			switch inputs[0] {
			case "--json":
				jsonOutput = true
			case "--raw":
				rawOutput = true
			default:
				return fmt.Errorf("unknown option: %s", inputs[0])
			}
			inputs = inputs[1:]
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench dataset plan [--json|--raw] <dataset>")
		}
		if jsonOutput && rawOutput {
			return fmt.Errorf("--json and --raw cannot be used together")
		}
		plan, err := datasetplan.Build(root, catalog, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return datasetplan.RenderJSON(os.Stdout, plan)
		}
		if rawOutput {
			return datasetplan.RenderRaw(os.Stdout, plan)
		}
		return datasetplan.Render(os.Stdout, plan)
	default:
		return runKindCatalog("dataset", catalog, args)
	}
}

func runDiagnostics(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("diagnostics action is required")
	}
	catalog := diagnosticcatalog.New(root)

	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: pgworkbench diagnostics list")
		}
		diagnostics, err := catalog.List()
		if err != nil {
			return err
		}
		for _, diagnostic := range diagnostics {
			fmt.Println(diagnostic)
		}
		return nil
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: pgworkbench diagnostics show <diagnostic>")
		}
		content, err := catalog.Show(args[1])
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(content)
		return err
	default:
		return fmt.Errorf("unsupported diagnostics action: %s", args[0])
	}
}

func runPatchset(catalog patchsetcatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("patchset action is required")
	}

	switch args[0] {
	case "list":
		patchsets, err := catalog.List()
		if err != nil {
			return err
		}
		for _, patchset := range patchsets {
			fmt.Println(patchset)
		}
		return nil
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: pgworkbench patchset show <patchset>")
		}
		metadata, err := catalog.Show(args[1])
		if err != nil {
			return err
		}
		printPatchsetMetadata(metadata)
		return nil
	case "validate":
		errs := catalog.Validate(args[1:])
		if len(errs) > 0 {
			for _, err := range errs {
				fmt.Fprintln(os.Stderr, err)
			}
			return fmt.Errorf("patchset catalog validation failed")
		}
		fmt.Println("PASS: patchset catalog")
		return nil
	default:
		return fmt.Errorf("unsupported patchset action: %s", args[0])
	}
}

func runProfile(root string, catalog profilecatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("profile action is required")
	}

	switch args[0] {
	case "list":
		profiles, err := catalog.List()
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			fmt.Println(profile)
		}
		return nil
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: pgworkbench profile show <profile>")
		}
		metadata, err := catalog.Show(args[1])
		if err != nil {
			return err
		}
		printMetadata(metadata)
		return nil
	case "validate":
		errs := catalog.Validate(args[1:])
		if len(errs) > 0 {
			for _, err := range errs {
				fmt.Fprintln(os.Stderr, err)
			}
			return fmt.Errorf("profile catalog validation failed")
		}
		fmt.Println("PASS: profile catalog")
		return nil
	case "plan":
		options, inputs, err := parseProfilePlanArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) == 0 {
			return fmt.Errorf("usage: pgworkbench profile plan [--json] [--size <size>] [--seconds <seconds>] <profile> [sql-file...]")
		}
		plan, err := profileplan.Build(root, catalog, inputs[0], profileplan.Options{
			Size:    valueOr(options["size"], os.Getenv("PROFILE_SIZE")),
			Seconds: valueOr(options["seconds"], os.Getenv("PROFILE_SECONDS")),
			SQL:     inputs[1:],
		})
		if err != nil {
			return err
		}
		if options["json"] == "1" {
			return profileplan.RenderJSON(os.Stdout, plan)
		}
		return profileplan.Render(os.Stdout, plan)
	default:
		return fmt.Errorf("unsupported profile action: %s", args[0])
	}
}

func parseProfilePlanArgs(args []string) (map[string]string, []string, error) {
	options := make(map[string]string)
	var inputs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			options["json"] = "1"
		case "--size", "--seconds":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a value", args[i])
			}
			options[strings.TrimPrefix(args[i], "--")] = args[i+1]
			i++
		case "--":
			inputs = append(inputs, args[i+1:]...)
			return options, inputs, nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return nil, nil, fmt.Errorf("unknown option: %s", args[i])
			}
			inputs = append(inputs, args[i])
		}
	}
	return options, inputs, nil
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func runExperiment(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("experiment action is required")
	}

	switch args[0] {
	case "plan":
		expanded := false
		jsonOutput := false
		inputs := args[1:]
		for len(inputs) > 0 && strings.HasPrefix(inputs[0], "-") {
			switch inputs[0] {
			case "--expanded":
				expanded = true
			case "--json":
				jsonOutput = true
			default:
				return fmt.Errorf("unknown option: %s", inputs[0])
			}
			inputs = inputs[1:]
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench experiment plan [--json] [--expanded] <experiment-spec>")
		}
		var (
			plan experimentplan.Plan
			err  error
		)
		if expanded {
			plan, err = experimentplan.BuildExpanded(root, catalog, inputs[0])
		} else {
			plan, err = experimentplan.Build(catalog, inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			return experimentplan.RenderJSON(os.Stdout, plan)
		}
		return experimentplan.Render(os.Stdout, plan)
	default:
		return runKindCatalog("experiment", catalog, args)
	}
}

func runMatrix(catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("matrix action is required")
	}

	switch args[0] {
	case "plan":
		jsonOutput := false
		rawOutput := false
		inputs := args[1:]
		for len(inputs) > 0 && strings.HasPrefix(inputs[0], "-") {
			switch inputs[0] {
			case "--json":
				jsonOutput = true
			case "--raw":
				rawOutput = true
			default:
				return fmt.Errorf("unknown option: %s", inputs[0])
			}
			inputs = inputs[1:]
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench matrix plan [--json|--raw] <matrix-spec>")
		}
		if jsonOutput && rawOutput {
			return fmt.Errorf("--json and --raw cannot be used together")
		}
		plan, err := matrixplan.Build(catalog, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return matrixplan.RenderJSON(os.Stdout, plan)
		}
		if rawOutput {
			return matrixplan.RenderRaw(os.Stdout, plan)
		}
		return matrixplan.Render(os.Stdout, plan)
	default:
		return runKindCatalog("matrix", catalog, args)
	}
}

func runMetrics(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("metrics action is required")
	}

	switch args[0] {
	case "plan":
		jsonOutput := false
		inputs := args[1:]
		for len(inputs) > 0 && strings.HasPrefix(inputs[0], "-") {
			switch inputs[0] {
			case "--json":
				jsonOutput = true
			default:
				return fmt.Errorf("unknown option: %s", inputs[0])
			}
			inputs = inputs[1:]
		}
		if len(inputs) > 1 {
			return fmt.Errorf("usage: pgworkbench metrics plan [--json] [output.csv]")
		}
		output := ""
		if len(inputs) == 1 {
			output = inputs[0]
		}
		plan, err := metricsplan.Build(root, output, os.Getenv, time.Now())
		if err != nil {
			return err
		}
		if jsonOutput {
			return metricsplan.RenderJSON(os.Stdout, plan)
		}
		return metricsplan.Render(os.Stdout, plan)
	default:
		return fmt.Errorf("unsupported metrics action: %s", args[0])
	}
}

func runSource(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("source action is required")
	}

	switch args[0] {
	case "plan":
		if len(args) > 2 {
			return fmt.Errorf("usage: pgworkbench source plan [workload-spec]")
		}
		workloadSpec := ""
		if len(args) == 2 {
			workloadSpec = args[1]
		}
		plan, err := pgsourceplan.Build(root, pgsourceplan.Options{
			Action:       "plan",
			WorkloadSpec: workloadSpec,
		})
		if err != nil {
			return err
		}
		return pgsourceplan.Render(os.Stdout, plan)
	case "classify":
		if len(args) != 2 {
			return fmt.Errorf("usage: pgworkbench source classify <pg-source-run-dir-or-artifact-dir>")
		}
		summary, err := pgsourcecheck.Classify(root, args[1])
		if err != nil {
			return err
		}
		if err := pgsourcecheck.Render(os.Stdout, summary); err != nil {
			return err
		}
		if summary.Found {
			return failurescan.ErrEvidenceFound
		}
		return nil
	default:
		return fmt.Errorf("unsupported source action: %s", args[0])
	}
}

func runTopology(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("topology action is required")
	}

	switch args[0] {
	case "inspect":
		if len(args) != 2 {
			return fmt.Errorf("usage: pgworkbench topology inspect <topology>")
		}
		inspection, err := topologyinspect.Inspect(root, args[1], topologyinspect.Options{
			Env: topologyinspect.EnvFromOS(),
		})
		if err != nil {
			return err
		}
		return topologyinspect.Render(os.Stdout, inspection)
	case "ps":
		if len(args) != 2 {
			return fmt.Errorf("usage: pgworkbench topology ps <topology>")
		}
		status, err := topologyinspect.Runtime(root, args[1], topologyinspect.RuntimeOptions{
			Env: topologyinspect.EnvFromOS(),
		})
		if err != nil {
			return err
		}
		return topologyinspect.RenderRuntime(os.Stdout, status)
	default:
		return runKindCatalog("topology", catalog, args)
	}
}

func runState(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("run action is required")
	}

	switch args[0] {
	case "list":
		jsonOutput, options, err := parseRunListArgs(args[1:])
		if err != nil {
			return err
		}
		summaries, err := runcatalog.ListWithOptions(root, options)
		if err != nil {
			return err
		}
		if jsonOutput {
			return runcatalog.RenderJSON(os.Stdout, summaries)
		}
		return runcatalog.RenderList(os.Stdout, summaries)
	case "show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench run show [--json] <run-dir-or-id>")
		}
		summary, err := runcatalog.Show(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return runcatalog.RenderJSON(os.Stdout, summary)
		}
		return runcatalog.RenderShow(os.Stdout, summary)
	case "bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench run bundle [--json] <run-dir-or-id> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		result, err := runbundle.Create(root, inputs[0], output)
		if err != nil {
			return err
		}
		if jsonOutput {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		}
		fmt.Printf("Wrote bundle: %s files=%d bytes=%d\n", result.Output, result.Files, result.Bytes)
		return nil
	case "verify":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench run verify [--json] <run-dir-or-id>")
		}
		result, err := runverify.Verify(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := runverify.RenderJSON(os.Stdout, result); err != nil {
				return err
			}
		} else {
			if err := runverify.Render(os.Stdout, result); err != nil {
				return err
			}
		}
		if !result.Valid() {
			return fmt.Errorf("run verification failed")
		}
		return nil
	case "write-manifest":
		options, err := parseFlagArgs(args[1:])
		if err != nil {
			return err
		}
		runDir := options["run-dir"]
		if runDir == "" {
			return fmt.Errorf("usage: pgworkbench run write-manifest --run-dir <run-dir>")
		}
		return runstate.WriteManifest(runDir, runstate.ManifestFromEnv(os.Getenv))
	case "write-verdict":
		options, err := parseFlagArgs(args[1:])
		if err != nil {
			return err
		}
		runDir := options["run-dir"]
		status := options["status"]
		message := options["message"]
		if runDir == "" || status == "" || message == "" {
			return fmt.Errorf("usage: pgworkbench run write-verdict --run-dir <run-dir> --status <status> --message <message> [--finished-at <time>]")
		}
		verdict := runstate.VerdictFromEnv(os.Getenv, status, message, options["finished-at"])
		return runstate.WriteVerdict(runDir, verdict)
	default:
		return fmt.Errorf("unsupported run action: %s", args[0])
	}
}

func parseFlagArgs(args []string) (map[string]string, error) {
	options := make(map[string]string)
	for i := 0; i < len(args); i++ {
		if len(args[i]) < 3 || args[i][:2] != "--" {
			return nil, fmt.Errorf("unexpected argument: %s", args[i])
		}
		key := args[i][2:]
		if i+1 >= len(args) {
			return nil, fmt.Errorf("%s requires a value", args[i])
		}
		options[key] = args[i+1]
		i++
	}
	return options, nil
}

func runSpec(catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("spec action is required")
	}

	switch args[0] {
	case "list":
		if len(args) != 2 {
			return fmt.Errorf("usage: pgworkbench spec list <workload|experiment|matrix|topology|dataset>")
		}
		specs, err := catalog.List(args[1])
		if err != nil {
			return err
		}
		for _, spec := range specs {
			fmt.Println(spec)
		}
		return nil
	case "show":
		if len(args) != 3 {
			return fmt.Errorf("usage: pgworkbench spec show <kind> <spec>")
		}
		spec, err := catalog.Show(args[1], args[2])
		if err != nil {
			return err
		}
		printSpec(spec)
		return nil
	case "reference":
		kind := "all"
		if len(args) > 2 {
			return fmt.Errorf("usage: pgworkbench spec reference [workload|experiment|matrix|topology|dataset|all]")
		}
		if len(args) == 2 {
			kind = args[1]
		}
		return speccatalog.RenderReference(os.Stdout, kind)
	case "schema":
		kind := "all"
		if len(args) > 2 {
			return fmt.Errorf("usage: pgworkbench spec schema [workload|experiment|matrix|topology|dataset|all]")
		}
		if len(args) == 2 {
			kind = args[1]
		}
		return speccatalog.RenderSchema(os.Stdout, kind)
	case "validate":
		kind := "all"
		ids := []string(nil)
		if len(args) >= 2 {
			kind = args[1]
			ids = args[2:]
		}
		errs := catalog.Validate(kind, ids)
		if len(errs) > 0 {
			for _, err := range errs {
				fmt.Fprintln(os.Stderr, err)
			}
			return fmt.Errorf("spec validation failed")
		}
		fmt.Println("PASS: specs")
		return nil
	default:
		return fmt.Errorf("unsupported spec action: %s", args[0])
	}
}

func runReport(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("report action is required")
	}

	switch args[0] {
	case "run":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: pgworkbench report run <run-dir-or-id> [output.md]")
		}
		if len(args) == 3 {
			outPath := args[2]
			if !filepath.IsAbs(outPath) {
				outPath = filepath.Join(root, outPath)
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			file, err := os.Create(outPath)
			if err != nil {
				return err
			}
			defer file.Close()

			if err := runreport.RenderRun(root, args[1], file); err != nil {
				return err
			}
			fmt.Printf("Wrote report: %s\n", outPath)
			return nil
		}
		return runreport.RenderRun(root, args[1], os.Stdout)
	case "compare":
		raw, inputs, err := parseRawArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 2 {
			return fmt.Errorf("usage: pgworkbench report compare [--raw] <baseline-run-dir> <candidate-run-dir>")
		}
		if raw {
			return runreport.RenderComparisonWithOptions(root, inputs[0], inputs[1], runreport.ComparisonOptions{
				BaselineLabel:  inputs[0],
				CandidateLabel: inputs[1],
			}, os.Stdout)
		}
		return runreport.RenderComparison(root, inputs[0], inputs[1], os.Stdout)
	case "summary":
		outPath, inputs, err := parseOutputArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) == 0 {
			return fmt.Errorf("usage: pgworkbench report summary [--output output.md] <series-dir|run-dir> [run-dir...]")
		}
		return renderMaybeFile(root, outPath, "summary", func(w *os.File) error {
			return runreport.RenderSummary(root, inputs, w)
		})
	case "history":
		outPath, inputs, err := parseOutputArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) == 0 {
			return fmt.Errorf("usage: pgworkbench report history [--output output.md] <series-dir|run-dir> [series-dir|run-dir...]")
		}
		return renderMaybeFile(root, outPath, "run history comparison", func(w *os.File) error {
			return runreport.RenderHistory(root, inputs, w)
		})
	default:
		return fmt.Errorf("unsupported report action: %s", args[0])
	}
}

func parseOutputArgs(args []string) (string, []string, error) {
	var outPath string
	var inputs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--output requires a path")
			}
			outPath = args[i+1]
			i++
		case "--":
			inputs = append(inputs, args[i+1:]...)
			return outPath, inputs, nil
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return "", nil, fmt.Errorf("unknown option: %s", args[i])
			}
			inputs = append(inputs, args[i])
		}
	}
	return outPath, inputs, nil
}

func renderMaybeFile(root string, outPath string, label string, render func(*os.File) error) error {
	if outPath == "" {
		return render(os.Stdout)
	}
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(root, outPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	file, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := render(file); err != nil {
		return err
	}
	fmt.Printf("Wrote %s: %s\n", label, outPath)
	return nil
}

func runScan(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("scan action is required")
	}

	switch args[0] {
	case "failures":
		contextLines := 2
		if value := os.Getenv("SCAN_CONTEXT_LINES"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				return fmt.Errorf("SCAN_CONTEXT_LINES must be a non-negative integer")
			}
			contextLines = parsed
		}

		result, err := failurescan.Scan(root, failurescan.Options{
			Paths:        args[1:],
			ContextLines: contextLines,
		})
		if err != nil {
			return err
		}
		if err := failurescan.Render(os.Stdout, result); err != nil {
			return err
		}
		if result.Found {
			return failurescan.ErrEvidenceFound
		}
		return nil
	default:
		return fmt.Errorf("unsupported scan action: %s", args[0])
	}
}

func printSpec(spec speccatalog.Spec) {
	fmt.Printf("SPEC_KIND=\"%s\"\n", spec.Kind)
	fmt.Printf("SPEC_ID=\"%s\"\n", spec.ID)
	fmt.Printf("SPEC_FILE=\"%s\"\n", spec.Path)
	keys := make([]string, 0, len(spec.Values))
	for key := range spec.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Printf("%s=\"%s\"\n", key, spec.Values[key])
	}
}

func printMetadata(metadata profilecatalog.Metadata) {
	fmt.Printf("PROFILE_NAME=\"%s\"\n", metadata.Name)
	fmt.Printf("PROFILE_DESCRIPTION=\"%s\"\n", metadata.Description)
	fmt.Printf("PROFILE_TAGS=\"%s\"\n", metadata.Tags)
	fmt.Printf("PROFILE_SCHEMAS=\"%s\"\n", metadata.Schemas)
	fmt.Printf("PROFILE_SIZES=\"%s\"\n", metadata.Sizes)
	fmt.Printf("PROFILE_DEFAULT_SIZE=\"%s\"\n", metadata.DefaultSize)
	fmt.Printf("PROFILE_REQUIRES_TOPOLOGY=\"%s\"\n", metadata.RequiresTopology)
	fmt.Printf("PROFILE_BACKGROUND_WORKLOADS=\"%s\"\n", metadata.BackgroundWorkloads)
	fmt.Printf("PROFILE_DIAGNOSTIC_SQL=\"%s\"\n", metadata.DiagnosticSQL)
}

func printPatchsetMetadata(metadata patchsetcatalog.Metadata) {
	fmt.Printf("PATCHSET_NAME=\"%s\"\n", metadata.Name)
	fmt.Printf("PATCHSET_DESCRIPTION=\"%s\"\n", metadata.Description)
	fmt.Printf("PATCHSET_PG_REF=\"%s\"\n", metadata.PgRef)
	fmt.Printf("PATCHSET_FILES=\"%s\"\n", metadata.Files)
	fmt.Printf("PATCHSET_ALLOW_EMPTY=\"%s\"\n", metadata.AllowEmpty)
	fmt.Printf("PATCHSET_CONFIGURE_ARGS=\"%s\"\n", metadata.ConfigureArgs)
	fmt.Printf("PATCHSET_BUILD_CFLAGS=\"%s\"\n", metadata.BuildCflags)
	fmt.Printf("PATCHSET_TEST_INITDB_EXTRA_OPTS=\"%s\"\n", metadata.TestInitdbExtraOpts)
	fmt.Printf("PATCHSET_DIR=\"%s\"\n", metadata.Dir)
	fmt.Printf("PATCHSET_RESOLVED_FILES=\"%s\"\n", strings.Join(metadata.ResolvedFiles, " "))
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "profiles")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root")
		}
		dir = parent
	}
}
