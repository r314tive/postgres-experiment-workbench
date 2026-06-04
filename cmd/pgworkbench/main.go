package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/r314tive/postgres-experiment-workbench/internal/failurescan"
	"github.com/r314tive/postgres-experiment-workbench/internal/profilecatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/runreport"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
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
	case "profile":
		return runProfile(profilecatalog.New(root), args[1:])
	case "scan":
		return runScan(root, args[1:])
	case "report":
		return runReport(root, args[1:])
	case "spec":
		return runSpec(speccatalog.New(root), args[1:])
	default:
		return fmt.Errorf("unsupported command: %s", args[0])
	}
}

func usage() {
	fmt.Println(`Usage:
  pgworkbench version
  pgworkbench profile list
  pgworkbench profile show <profile>
  pgworkbench profile validate [profile...]
  pgworkbench scan failures [path...]
  pgworkbench report run <run-dir-or-id> [output.md]
  pgworkbench report compare <baseline-run-dir> <candidate-run-dir>
  pgworkbench report summary [--output output.md] <series-dir|run-dir> [run-dir...]
  pgworkbench report history [--output output.md] <series-dir|run-dir> [series-dir|run-dir...]
  pgworkbench spec list <workload|experiment|matrix|topology|dataset>
  pgworkbench spec show <kind> <spec>
  pgworkbench spec validate [kind] [spec...]`)
}

func runProfile(catalog profilecatalog.Catalog, args []string) error {
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
	default:
		return fmt.Errorf("unsupported profile action: %s", args[0])
	}
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
		return nil
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
		if len(args) != 3 {
			return fmt.Errorf("usage: pgworkbench report compare <baseline-run-dir> <candidate-run-dir>")
		}
		return runreport.RenderComparison(root, args[1], args[2], os.Stdout)
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
