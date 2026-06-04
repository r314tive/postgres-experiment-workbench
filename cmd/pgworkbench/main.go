package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/r314tive/postgres-experiment-workbench/internal/failurescan"
	"github.com/r314tive/postgres-experiment-workbench/internal/profilecatalog"
)

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
	case "profile":
		return runProfile(profilecatalog.New(root), args[1:])
	case "scan":
		return runScan(root, args[1:])
	default:
		return fmt.Errorf("unsupported command: %s", args[0])
	}
}

func usage() {
	fmt.Println(`Usage:
  pgworkbench profile list
  pgworkbench profile show <profile>
  pgworkbench profile validate [profile...]
  pgworkbench scan failures [path...]`)
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
