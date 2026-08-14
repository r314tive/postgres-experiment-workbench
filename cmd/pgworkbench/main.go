package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkab"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcampaign"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkcompare"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkexternal"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkhistory"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkimport"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkqualify"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarksampler"
	"github.com/r314tive/postgres-experiment-workbench/internal/compatibility"
	"github.com/r314tive/postgres-experiment-workbench/internal/datasetplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/diagnosticcatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/doctor"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/experimentrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/failurescan"
	"github.com/r314tive/postgres-experiment-workbench/internal/matrixartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/matrixplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/metricsplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/operationbench"
	"github.com/r314tive/postgres-experiment-workbench/internal/patchsetcatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgdrillbridge"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgsourcecheck"
	"github.com/r314tive/postgres-experiment-workbench/internal/pgsourceplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/profilecatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/profileplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasearchive"
	"github.com/r314tive/postgres-experiment-workbench/internal/releaseevidence"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasemanifest"
	"github.com/r314tive/postgres-experiment-workbench/internal/releasesbom"
	"github.com/r314tive/postgres-experiment-workbench/internal/runartifact"
	"github.com/r314tive/postgres-experiment-workbench/internal/runbundle"
	"github.com/r314tive/postgres-experiment-workbench/internal/runcatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/runreport"
	"github.com/r314tive/postgres-experiment-workbench/internal/runstate"
	"github.com/r314tive/postgres-experiment-workbench/internal/runverify"
	"github.com/r314tive/postgres-experiment-workbench/internal/scenariopack"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
	"github.com/r314tive/postgres-experiment-workbench/internal/topologyinspect"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilityrun"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilitysuite"
	"github.com/r314tive/postgres-experiment-workbench/internal/utilitysuiteartifact"
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

	if args[0] == "version" {
		fmt.Printf("pgworkbench version=%s commit=%s built_at=%s\n", version, commit, builtAt)
		return nil
	}
	// Release archive/SBOM creation and artifact verification must also work in
	// clean staging directories that are not scenario-pack workspaces. Manifest
	// creation still discovers the current pack when --pack-root is omitted.
	if args[0] == "release" {
		root, _ := findRepoRoot()
		return runRelease(root, args[1:])
	}
	// Release evidence is deliberately verifiable outside a scenario-pack
	// checkout. A durable index commonly lives in a separate protected evidence
	// repository or object-store download directory.
	if args[0] == "evidence" {
		return runEvidence(args[1:])
	}
	if args[0] == "bridge" && len(args) > 2 && args[1] == "pgdrill" && args[2] == "verify" {
		root, rootErr := findRepoRoot()
		if rootErr != nil {
			root, rootErr = os.Getwd()
		}
		if rootErr != nil {
			return rootErr
		}
		return runPGDrillBridge(root, args[2:])
	}
	if args[0] == "benchmark" && len(args) > 1 && (args[1] == "operation" || oneOfString(args[1], "import", "import-verify", "import-bundle", "driver-run-verify", "run-show", "run-verify", "run-bundle", "compare", "history-create", "history-show", "history-verify", "history-bundle", "campaign-show", "campaign-verify", "campaign-bundle", "host-inspect", "host-verify", "ab-show", "ab-verify", "ab-bundle")) {
		root, rootErr := findRepoRoot()
		if rootErr != nil {
			root, rootErr = os.Getwd()
		}
		if rootErr != nil {
			return rootErr
		}
		return runBenchmark(root, speccatalog.New(root), args[1:])
	}

	root, err := findRepoRoot()
	if err != nil {
		return err
	}

	switch args[0] {
	case "doctor":
		return runDoctor(root, args[1:])
	case "compatibility":
		return runCompatibility(root, args[1:])
	case "pack":
		return runPack(root, args[1:])
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
	case "benchmark":
		return runBenchmark(root, speccatalog.New(root), args[1:])
	case "bridge":
		if len(args) < 2 || args[1] != "pgdrill" {
			return fmt.Errorf("usage: pgworkbench bridge pgdrill <export|verify> [options]")
		}
		return runPGDrillBridge(root, args[2:])
	case "utility":
		return runUtility(root, speccatalog.New(root), args[1:])
	case "utility-suite":
		return runUtilitySuite(root, speccatalog.New(root), args[1:])
	case "experiment":
		return runExperiment(root, speccatalog.New(root), args[1:])
	case "matrix":
		return runMatrix(root, speccatalog.New(root), args[1:])
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
	pgworkbench doctor [--runtime docker|native] [--skip-docker-daemon]
	pgworkbench compatibility validate [--json]
	pgworkbench compatibility show [--json]
	pgworkbench pack validate [--json] [--engine-version version]
	pgworkbench pack inspect [--json] [--engine-version version]
	pgworkbench pack init [--json] [--engine-version version] [--id id] [--version version] <destination>
	pgworkbench pack export [--json] [--engine-version version] [--version version] <destination>
	pgworkbench release archive create --source dir --output archive.tar.gz --root-name name --epoch seconds [--json]
	pgworkbench release manifest create --release-dir dir --version version --commit full-commit [--pack-root dir] [--checksum-file name] [--output name] [--source-date-epoch seconds] [--json]
	pgworkbench release manifest verify --release-dir dir --manifest name [--json]
	pgworkbench release sbom create --root dir --output document.spdx.json --name name --version version --commit full-commit --epoch seconds [--json]
	pgworkbench release sbom verify --package-root dir <document.spdx.json>
	pgworkbench evidence release verify [--json] <release-evidence-index.json>
	pgworkbench evidence release status [--json] <release-evidence-index.json>
	pgworkbench bridge pgdrill export [--json] [--bundle] [--reviewed-predicate-file file] <run-or-bundle> <output.json>
	pgworkbench bridge pgdrill verify [--json] [--source run-or-bundle] <baseline.json>
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
  pgworkbench benchmark list [--raw]
  pgworkbench benchmark show [--raw] <benchmark>
  pgworkbench benchmark validate [benchmark...]
  pgworkbench benchmark plan [--json] <benchmark>
  pgworkbench benchmark drivers [--json]
  pgworkbench benchmark driver-show [--json] <driver-id>
  pgworkbench benchmark driver-run [--json] --acknowledge-external-disposable-target --driver id --runtime-root dir --binary file --config file --script file --workload id [--timeout duration] <output-dir>
  pgworkbench benchmark driver-run-verify [--json] <execution-dir-or-execution.json>
  pgworkbench benchmark operation list [--json]
  pgworkbench benchmark operation show [--json] <operation>
  pgworkbench benchmark operation run [--json] [--runtime docker|native] [--run-id id] <operation>
  pgworkbench benchmark operation run-show [--json] <series-dir-or-id>
  pgworkbench benchmark operation verify [--json] [--bundle] <series-dir-or-id>
  pgworkbench benchmark operation bundle [--json] <series-dir-or-id> [output.tar.gz]
  pgworkbench benchmark import <hammerdb6|hammerdb6report|sysbench1|benchbase|benchbase33c0047> [--json] [--manifest mapping.json] [--workload id] <source> <output-dir>
  pgworkbench benchmark import-verify [--json] [--bundle] <import-dir-or-result.json>
  pgworkbench benchmark import-bundle [--json] <import-dir-or-result.json> [output.tar.gz]
  pgworkbench benchmark run [--json] [--runtime docker|native] [--native-bindir absolute-path] [--run-id id] [--subject label] <benchmark>
  pgworkbench benchmark materialize-controls-v2 --run-dir absolute-linked-run  # internal runner command
  pgworkbench benchmark sample-metrics-v2 --run-dir absolute-linked-run --interval-seconds n (--duration-seconds n|--samples n)  # internal runner command
  pgworkbench benchmark run-show [--json] <benchmark-series-dir-or-id>
  pgworkbench benchmark run-verify [--json] [--bundle] <benchmark-series-dir-or-id>
  pgworkbench benchmark run-bundle [--json] <benchmark-series-dir-or-id> [output.tar.gz]
  pgworkbench benchmark compare [--json] [--bootstrap-resamples n] [--confidence value] [--seed n] <baseline-series> <candidate-series>
  pgworkbench benchmark history-create [--json] [--history-id id] <benchmark-series> <benchmark-series> [...]
  pgworkbench benchmark history-show [--json] <benchmark-history-dir-or-id>
  pgworkbench benchmark history-verify [--json] [--bundle] <benchmark-history-dir-or-id>
  pgworkbench benchmark history-bundle [--json] <benchmark-history-dir-or-id> [output.tar.gz]
  pgworkbench benchmark campaign-run [--json] [--runtime docker|native] [--native-bindir absolute-path] [--campaign-id id] [--subject label] <benchmark> [benchmark...]
  pgworkbench benchmark campaign-show [--json] <benchmark-campaign-dir-or-id>
  pgworkbench benchmark campaign-verify [--json] [--bundle] <benchmark-campaign-dir-or-id>
  pgworkbench benchmark campaign-bundle [--json] <benchmark-campaign-dir-or-id> [output.tar.gz]
  pgworkbench benchmark ab-run [--json] [--runtime docker|native] [--run-id id] [--subject-dimension pg_config|native_toolchain] [--native-bindir absolute-path] [--baseline-native-bindir absolute-path] [--candidate-native-bindir absolute-path] [--baseline-subject label] [--candidate-subject label] [analysis-options] [qualification-options] <baseline-benchmark> <candidate-benchmark>
  pgworkbench benchmark ab-show [--json] <ab-run-dir-or-id>
  pgworkbench benchmark ab-verify [--json] [--bundle] <ab-run-dir-or-id>
  pgworkbench benchmark ab-bundle [--json] <ab-run-dir-or-id> [output.tar.gz]
  pgworkbench benchmark host-inspect [--json] [--output file] [--storage-path path] [--storage-label label] [--client-placement same-host|separate-host|remote-host] [--strict] [qualification-gates...]
  pgworkbench benchmark host-verify [--json] <host-qualification.json>
  pgworkbench utility list [--raw]
  pgworkbench utility show [--raw] <utility-test>
  pgworkbench utility validate [utility-test...]
  pgworkbench utility plan [--json] [--expanded] <utility-test>
  pgworkbench utility run [--json] [--runtime docker|native] [--run-id id] <utility-test>
  pgworkbench utility-suite list [--raw]
  pgworkbench utility-suite show [--raw] <utility-suite>
  pgworkbench utility-suite validate [utility-suite...]
  pgworkbench utility-suite plan [--json] <utility-suite>
  pgworkbench utility-suite run [--json] <utility-suite>
  pgworkbench utility-suite run-list [--json] [path...]
  pgworkbench utility-suite run-show [--json] <suite-run-dir-or-id>
  pgworkbench utility-suite run-bundle [--json] <suite-run-dir-or-id> [output.tar.gz]
  pgworkbench utility-suite run-verify [--json] <suite-run-dir-or-id>
  pgworkbench experiment list [--raw]
	pgworkbench experiment show [--raw] <experiment-spec>
	pgworkbench experiment plan [--json] [--expanded] <experiment-spec>
	pgworkbench experiment run [--json] [--runtime docker|native] [--run-id id] [--timeout duration] [--cleanup-grace duration] <experiment-spec>
  pgworkbench matrix list [--raw]
  pgworkbench matrix show [--raw] <matrix-spec>
  pgworkbench matrix plan [--json|--raw] <matrix-spec>
  pgworkbench matrix verify-candidate [--json] --version version --commit full-commit --expected-runs count <matrix-dir>
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
  pgworkbench run verify [--json] [--bundle] <run-dir-or-id>
  pgworkbench run write-manifest --run-dir <run-dir>
  pgworkbench run write-verdict --run-dir <run-dir> --status <status> --message <message> [--finished-at <time>]
  pgworkbench spec list <workload|experiment|benchmark|matrix|topology|dataset|utility-test|utility-suite>
  pgworkbench spec show <kind> <spec>
  pgworkbench spec reference [workload|experiment|benchmark|matrix|topology|dataset|utility-test|utility-suite|all]
  pgworkbench spec schema [workload|experiment|benchmark|matrix|topology|dataset|utility-test|utility-suite|all]
  pgworkbench spec validate [kind] [spec...]`)
}

func runDoctor(root string, args []string) error {
	nativeBindir := strings.TrimSpace(os.Getenv("PGWORKBENCH_NATIVE_BINDIR"))
	if nativeBindir == "" {
		if installDir := strings.TrimSpace(os.Getenv("PG_INSTALL_DIR")); installDir != "" {
			nativeBindir = filepath.Join(installDir, "bin")
		}
	}
	options := doctor.Options{NativeBindir: nativeBindir}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--skip-docker-daemon":
			options.SkipDockerDaemon = true
		case "--runtime":
			if i+1 >= len(args) {
				return fmt.Errorf("--runtime requires a value")
			}
			options.Runtime = args[i+1]
			i++
		default:
			return fmt.Errorf("usage: pgworkbench doctor [--runtime docker|native] [--skip-docker-daemon]")
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

func runCompatibility(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("compatibility action is required")
	}
	jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
	if err != nil {
		return err
	}
	if len(inputs) != 0 {
		return fmt.Errorf("usage: pgworkbench compatibility %s [--json]", args[0])
	}
	matrix, err := compatibility.Load(filepath.Join(root, compatibility.DefaultPath))
	if err != nil {
		return err
	}
	switch args[0] {
	case "validate":
		if jsonOutput {
			return compatibility.RenderJSON(os.Stdout, matrix)
		}
		fmt.Printf("PASS: compatibility matrix archive_platforms=%d cells=%d schema=%s\n", len(matrix.ArchivePlatforms), len(matrix.Cells), matrix.SchemaVersion)
		return nil
	case "show":
		if jsonOutput {
			return compatibility.RenderJSON(os.Stdout, matrix)
		}
		return compatibility.RenderMarkdown(os.Stdout, matrix)
	default:
		return fmt.Errorf("unsupported compatibility action: %s", args[0])
	}
}

func runPack(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pack action is required")
	}
	jsonOutput := false
	versionOverride := ""
	idOverride := ""
	engineVersion := version
	engineVersionExplicit := false
	var inputs []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		if arg == "--version" {
			if i+1 >= len(args) {
				return fmt.Errorf("--version requires a value")
			}
			versionOverride = args[i+1]
			i++
			continue
		}
		if arg == "--engine-version" {
			if i+1 >= len(args) {
				return fmt.Errorf("--engine-version requires a value")
			}
			engineVersion = args[i+1]
			engineVersionExplicit = true
			i++
			continue
		}
		if arg == "--id" {
			if i+1 >= len(args) {
				return fmt.Errorf("--id requires a value")
			}
			idOverride = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("unknown option: %s", arg)
		}
		inputs = append(inputs, arg)
	}

	var (
		inspection scenariopack.Inspection
		err        error
	)
	switch args[0] {
	case "validate", "inspect":
		if len(inputs) != 0 || versionOverride != "" || idOverride != "" {
			return fmt.Errorf("usage: pgworkbench pack %s [--json] [--engine-version version]", args[0])
		}
		inspection, err = scenariopack.ValidateForEngine(root, engineVersion)
	case "export":
		if idOverride != "" {
			return fmt.Errorf("--id is supported only by pack init")
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench pack export [--json] [--engine-version version] [--version version] <destination>")
		}
		sourceInspection, sourceErr := scenariopack.ValidateForEngine(root, engineVersion)
		if sourceErr != nil {
			err = sourceErr
			break
		}
		if engineVersionExplicit && sourceInspection.EngineCompatibility != nil && sourceInspection.EngineCompatibility.Status == scenariopack.EngineUnverifiedDevelopment {
			err = fmt.Errorf(
				"pack export cannot use development engine version %s as a release gate; provide the exact SemVer release or prerelease candidate",
				engineVersion,
			)
			break
		}
		inspection, err = scenariopack.CopyVersion(root, inputs[0], versionOverride)
	case "init":
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench pack init [--json] [--engine-version version] [--id id] [--version version] <destination>")
		}
		if idOverride == "" {
			idOverride = starterPackID(filepath.Base(filepath.Clean(inputs[0])))
		}
		if versionOverride == "" {
			versionOverride = "0.1.0"
		}
		if _, err = scenariopack.ValidateForEngine(root, engineVersion); err != nil {
			break
		}
		inspection, err = scenariopack.CopyAs(root, inputs[0], idOverride, versionOverride)
	default:
		return fmt.Errorf("unsupported pack action: %s", args[0])
	}
	if err != nil {
		return err
	}
	if inspection.EngineCompatibility == nil {
		inspection, err = scenariopack.InspectForEngine(inspection, engineVersion)
		if err != nil {
			return err
		}
	}
	if jsonOutput || args[0] == "inspect" {
		return scenariopack.RenderJSON(os.Stdout, inspection)
	}
	fmt.Printf("PASS: scenario pack structure %s@%s files=%d digest=%s\n", inspection.ID, inspection.Version, len(inspection.Files), inspection.Digest)
	printEngineCompatibility(os.Stdout, inspection.EngineCompatibility)
	if args[0] == "export" || args[0] == "init" {
		fmt.Printf("root=%s\n", inspection.Root)
	}
	return nil
}

func printEngineCompatibility(writer io.Writer, compatibility *scenariopack.EngineCompatibility) {
	if compatibility == nil {
		return
	}
	switch compatibility.Status {
	case scenariopack.EngineCompatibleRelease:
		fmt.Fprintf(writer, "PASS: %s\n", compatibility.Diagnostic)
	case scenariopack.EngineCompatiblePrerelease:
		fmt.Fprintf(writer, "NOTICE: %s\n", compatibility.Diagnostic)
	case scenariopack.EngineUnverifiedDevelopment:
		fmt.Fprintf(writer, "UNVERIFIED: %s\n", compatibility.Diagnostic)
	default:
		fmt.Fprintf(writer, "%s: %s\n", strings.ToUpper(compatibility.Status), compatibility.Diagnostic)
	}
}

func runRelease(root string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: pgworkbench release <archive|manifest|sbom> <action> [options]")
	}
	switch args[0] {
	case "archive":
		if args[1] != "create" {
			return fmt.Errorf("unsupported release archive action: %s", args[1])
		}
		return runReleaseArchiveCreate(args[2:])
	case "manifest":
		switch args[1] {
		case "create":
			return runReleaseManifestCreate(root, args[2:])
		case "verify":
			return runReleaseManifestVerify(args[2:])
		default:
			return fmt.Errorf("unsupported release manifest action: %s", args[1])
		}
	case "sbom":
		switch args[1] {
		case "create":
			return runReleaseSBOMCreate(args[2:])
		case "verify":
			return runReleaseSBOMVerify(args[2:])
		default:
			return fmt.Errorf("unsupported release SBOM action: %s", args[1])
		}
	default:
		return fmt.Errorf("unsupported release artifact: %s", args[0])
	}
}

func runEvidence(args []string) error {
	return runEvidenceTo(os.Stdout, args)
}

func runEvidenceTo(writer io.Writer, args []string) error {
	if len(args) < 2 || args[0] != "release" || (args[1] != "verify" && args[1] != "status") {
		return fmt.Errorf("usage: pgworkbench evidence release <verify|status> [--json] <release-evidence-index.json>")
	}
	action := args[1]
	jsonOutput, inputs, err := parseJSONOptionArgs(args[2:])
	if err != nil {
		return err
	}
	if len(inputs) != 1 {
		return fmt.Errorf("usage: pgworkbench evidence release %s [--json] <release-evidence-index.json>", action)
	}
	verification, err := releaseevidence.VerifyFile(inputs[0])
	if err != nil {
		return err
	}
	if jsonOutput {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(verification); err != nil {
			return err
		}
	} else if action == "verify" {
		if verification.Valid {
			fmt.Fprintf(writer, "VALID: release evidence index status=%s decision=%s open=%d failed=%d passed=%d\n", verification.Status, verification.Decision, len(verification.OpenGates), len(verification.FailedGates), len(verification.PassedGates))
		} else {
			fmt.Fprintf(writer, "INVALID: release evidence index issues=%d\n", len(verification.Issues))
			for _, issue := range verification.Issues {
				fmt.Fprintf(writer, "- %s\n", issue)
			}
		}
	} else {
		fmt.Fprintf(writer, "release evidence status=%s decision=%s valid=%t open=%d failed=%d passed=%d\n", verification.Status, verification.Decision, verification.Valid, len(verification.OpenGates), len(verification.FailedGates), len(verification.PassedGates))
		for _, gate := range verification.FailedGates {
			fmt.Fprintf(writer, "failed: %s\n", gate)
		}
		for _, gate := range verification.OpenGates {
			fmt.Fprintf(writer, "open: %s\n", gate)
		}
		for _, gate := range verification.PassedGates {
			fmt.Fprintf(writer, "passed: %s\n", gate)
		}
		for _, reason := range verification.Reasons {
			fmt.Fprintf(writer, "reason: %s\n", reason)
		}
		for _, issue := range verification.Issues {
			fmt.Fprintf(writer, "issue: %s\n", issue)
		}
	}
	if !verification.Valid {
		return fmt.Errorf("release evidence index verification failed: %s", strings.Join(verification.Issues, "; "))
	}
	return nil
}

func runPGDrillBridge(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pgdrill bridge action is required")
	}
	switch args[0] {
	case "export":
		options, inputs, err := parsePGDrillBridgeExportArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 2 {
			return fmt.Errorf("usage: pgworkbench bridge pgdrill export [--json] [--bundle] [--reviewed-predicate-file file] <run-or-bundle> <output.json>")
		}
		predicateSQL := ""
		if options["reviewed-predicate-file"] != "" {
			predicateSQL, err = pgdrillbridge.LoadReviewedPredicateSQL(options["reviewed-predicate-file"])
			if err != nil {
				return err
			}
		}
		artifact, err := pgdrillbridge.Create(root, inputs[0], inputs[1], pgdrillbridge.Options{
			RequireBundle:        options["bundle"] == "1",
			ReviewedPredicateSQL: predicateSQL,
		})
		if err != nil {
			return err
		}
		if options["json"] == "1" {
			return pgdrillbridge.RenderJSON(os.Stdout, artifact)
		}
		fmt.Printf("PASS: exported pgdrill baseline provenance %s digest=%s\n", artifact.ArtifactPath, artifact.Digest)
		return nil
	case "verify":
		options, inputs, err := parsePGDrillBridgeVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench bridge pgdrill verify [--json] [--source run-or-bundle] <baseline.json>")
		}
		var verification pgdrillbridge.Verification
		if options["source"] != "" {
			verification, err = pgdrillbridge.VerifyAgainstSource(root, inputs[0], options["source"])
		} else {
			verification, err = pgdrillbridge.Verify(inputs[0])
		}
		if err != nil {
			return err
		}
		if options["json"] == "1" {
			if err := pgdrillbridge.RenderJSON(os.Stdout, verification); err != nil {
				return err
			}
		} else if err := pgdrillbridge.Render(os.Stdout, verification); err != nil {
			return err
		}
		if !verification.IsValid() {
			return fmt.Errorf("pgdrill baseline verification failed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported pgdrill bridge action: %s", args[0])
	}
}

func parsePGDrillBridgeExportArgs(args []string) (map[string]string, []string, error) {
	options := make(map[string]string)
	var inputs []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json", "--bundle":
			key := strings.TrimPrefix(args[index], "--")
			if options[key] != "" {
				return nil, nil, fmt.Errorf("duplicate option: %s", args[index])
			}
			options[key] = "1"
		case "--reviewed-predicate-file":
			if options["reviewed-predicate-file"] != "" {
				return nil, nil, fmt.Errorf("duplicate option: %s", args[index])
			}
			if index+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a value", args[index])
			}
			options["reviewed-predicate-file"] = args[index+1]
			index++
		case "--":
			inputs = append(inputs, args[index+1:]...)
			return options, inputs, nil
		default:
			if strings.HasPrefix(args[index], "-") {
				return nil, nil, fmt.Errorf("unknown option: %s", args[index])
			}
			inputs = append(inputs, args[index])
		}
	}
	return options, inputs, nil
}

func parsePGDrillBridgeVerifyArgs(args []string) (map[string]string, []string, error) {
	options := make(map[string]string)
	var inputs []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			if options["json"] != "" {
				return nil, nil, fmt.Errorf("duplicate option: %s", args[index])
			}
			options["json"] = "1"
		case "--source":
			if options["source"] != "" {
				return nil, nil, fmt.Errorf("duplicate option: %s", args[index])
			}
			if index+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a value", args[index])
			}
			options["source"] = args[index+1]
			index++
		case "--":
			inputs = append(inputs, args[index+1:]...)
			return options, inputs, nil
		default:
			if strings.HasPrefix(args[index], "-") {
				return nil, nil, fmt.Errorf("unknown option: %s", args[index])
			}
			inputs = append(inputs, args[index])
		}
	}
	return options, inputs, nil
}

func runReleaseSBOMCreate(args []string) error {
	values := make(map[string]string)
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--root", "--output", "--name", "--version", "--commit", "--epoch":
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a value", args[index])
			}
			key := strings.TrimPrefix(args[index], "--")
			if _, exists := values[key]; exists {
				return fmt.Errorf("duplicate option: %s", args[index])
			}
			values[key] = args[index+1]
			index++
		default:
			return fmt.Errorf("unknown option: %s", args[index])
		}
	}
	for _, required := range []string{"root", "output", "name", "version", "commit", "epoch"} {
		if values[required] == "" {
			return fmt.Errorf("--%s is required", required)
		}
	}
	epoch, err := strconv.ParseInt(values["epoch"], 10, 64)
	if err != nil || epoch < 0 {
		return fmt.Errorf("epoch must be a non-negative integer")
	}
	result, err := releasesbom.Create(releasesbom.Options{
		Root:    values["root"],
		Output:  values["output"],
		Name:    values["name"],
		Version: values["version"],
		Commit:  values["commit"],
		Created: time.Unix(epoch, 0).UTC(),
	})
	if err != nil {
		return err
	}
	if jsonOutput {
		return releasemanifest.RenderJSON(os.Stdout, result)
	}
	fmt.Printf("PASS: release SBOM %s files=%d digest=%s\n", result.Output, result.Files, result.SHA256)
	return nil
}

func runReleaseSBOMVerify(args []string) error {
	packageRoot := ""
	document := ""
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--package-root":
			if packageRoot != "" {
				return fmt.Errorf("duplicate option: %s", args[index])
			}
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a value", args[index])
			}
			packageRoot = args[index+1]
			index++
		default:
			if strings.HasPrefix(args[index], "-") {
				return fmt.Errorf("unknown option: %s", args[index])
			}
			if document != "" {
				return fmt.Errorf("release SBOM verify accepts exactly one document")
			}
			document = args[index]
		}
	}
	if packageRoot == "" || document == "" {
		return fmt.Errorf("usage: pgworkbench release sbom verify --package-root dir <document.spdx.json>")
	}
	if err := releasesbom.ValidatePackageRoot(document, packageRoot); err != nil {
		return err
	}
	fmt.Printf("PASS: release SBOM verified %s against package root %s\n", document, packageRoot)
	return nil
}

func runReleaseArchiveCreate(args []string) error {
	values := make(map[string]string)
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--source", "--output", "--root-name", "--epoch":
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a value", args[index])
			}
			key := strings.TrimPrefix(args[index], "--")
			if _, exists := values[key]; exists {
				return fmt.Errorf("duplicate option: %s", args[index])
			}
			values[key] = args[index+1]
			index++
		default:
			return fmt.Errorf("unknown option: %s", args[index])
		}
	}
	for _, required := range []string{"source", "output", "root-name", "epoch"} {
		if values[required] == "" {
			return fmt.Errorf("--%s is required", required)
		}
	}
	epoch, err := strconv.ParseInt(values["epoch"], 10, 64)
	if err != nil || epoch < 0 {
		return fmt.Errorf("epoch must be a non-negative integer")
	}
	result, err := releasearchive.Create(values["source"], values["output"], values["root-name"], time.Unix(epoch, 0).UTC())
	if err != nil {
		return err
	}
	if jsonOutput {
		return releasemanifest.RenderJSON(os.Stdout, result)
	}
	fmt.Printf("PASS: release archive %s files=%d digest=%s\n", result.Output, result.Files, result.SHA256)
	return nil
}

func runReleaseManifestCreate(root string, args []string) error {
	values := make(map[string]string)
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--release-dir", "--version", "--commit", "--pack-root", "--checksum-file", "--output", "--source-date-epoch":
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a value", args[index])
			}
			key := strings.TrimPrefix(args[index], "--")
			if _, exists := values[key]; exists {
				return fmt.Errorf("duplicate option: %s", args[index])
			}
			values[key] = args[index+1]
			index++
		default:
			return fmt.Errorf("unknown option: %s", args[index])
		}
	}
	for _, required := range []string{"release-dir", "version", "commit"} {
		if values[required] == "" {
			return fmt.Errorf("--%s is required", required)
		}
	}
	packRoot := values["pack-root"]
	if packRoot == "" {
		packRoot = root
	}
	if packRoot == "" {
		return fmt.Errorf("--pack-root is required outside a pgworkbench scenario-pack workspace")
	}
	outputPath := values["output"]
	if outputPath == "" {
		outputPath = releasemanifest.DefaultManifestPath(values["version"])
	}

	var sourceDateEpoch *int64
	epochValue := values["source-date-epoch"]
	if epochValue == "" {
		epochValue = strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH"))
	}
	if epochValue != "" {
		epoch, err := strconv.ParseInt(epochValue, 10, 64)
		if err != nil || epoch < 0 {
			return fmt.Errorf("source date epoch must be a non-negative integer")
		}
		sourceDateEpoch = &epoch
	}
	manifest, err := releasemanifest.Create(releasemanifest.CreateOptions{
		ReleaseDir:      values["release-dir"],
		Version:         values["version"],
		GitCommit:       values["commit"],
		PackRoot:        packRoot,
		ChecksumPath:    values["checksum-file"],
		SourceDateEpoch: sourceDateEpoch,
	})
	if err != nil {
		return err
	}
	if err := releasemanifest.Write(values["release-dir"], outputPath, manifest); err != nil {
		return err
	}
	if jsonOutput {
		return releasemanifest.RenderJSON(os.Stdout, manifest)
	}
	fmt.Printf("PASS: release manifest %s archives=%d sboms=%d\n", filepath.Join(values["release-dir"], outputPath), len(manifest.Archives), len(manifest.SBOMs))
	return nil
}

func runReleaseManifestVerify(args []string) error {
	values := make(map[string]string)
	jsonOutput := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--release-dir", "--manifest":
			if index+1 >= len(args) {
				return fmt.Errorf("%s requires a value", args[index])
			}
			key := strings.TrimPrefix(args[index], "--")
			if _, exists := values[key]; exists {
				return fmt.Errorf("duplicate option: %s", args[index])
			}
			values[key] = args[index+1]
			index++
		default:
			return fmt.Errorf("unknown option: %s", args[index])
		}
	}
	for _, required := range []string{"release-dir", "manifest"} {
		if values[required] == "" {
			return fmt.Errorf("--%s is required", required)
		}
	}
	manifest, err := releasemanifest.Verify(values["release-dir"], values["manifest"])
	if err != nil {
		return err
	}
	if jsonOutput {
		return releasemanifest.RenderJSON(os.Stdout, manifest)
	}
	fmt.Printf("PASS: release manifest verified %s archives=%d sboms=%d\n", filepath.Join(values["release-dir"], values["manifest"]), len(manifest.Archives), len(manifest.SBOMs))
	return nil
}

func starterPackID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' {
			out.WriteRune(ch)
		} else {
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-._")
}

func runKindCatalog(kind string, catalog speccatalog.Catalog, args []string) error {
	return runNamedKindCatalog(kind, kind, catalog, args)
}

func runNamedKindCatalog(command string, kind string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s action is required", command)
	}

	switch args[0] {
	case "list":
		raw, inputs, err := parseRawArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 0 {
			return fmt.Errorf("usage: pgworkbench %s list [--raw]", command)
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
			return fmt.Errorf("usage: pgworkbench %s show [--raw] <%s>", command, command)
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
			return fmt.Errorf("%s catalog validation failed", command)
		}
		fmt.Printf("PASS: %s catalog\n", command)
		return nil
	default:
		return fmt.Errorf("unsupported %s action: %s", command, args[0])
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

func runUtility(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("utility action is required")
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
			return fmt.Errorf("usage: pgworkbench utility plan [--json] [--expanded] <utility-test>")
		}
		var (
			plan utilityplan.Plan
			err  error
		)
		if expanded {
			plan, err = utilityplan.BuildExpanded(root, catalog, inputs[0])
		} else {
			plan, err = utilityplan.Build(catalog, inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			return utilityplan.RenderJSON(os.Stdout, plan)
		}
		return utilityplan.Render(os.Stdout, plan)
	case "run":
		options, inputs, err := parseUtilityRunArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench utility run [--json] [--runtime docker|native] [--run-id id] <utility-test>")
		}
		commandStdout := io.Writer(os.Stdout)
		if options["json"] == "1" {
			commandStdout = os.Stderr
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pgworkbench executable: %w", err)
		}
		result, runErr := utilityrun.Run(root, catalog, inputs[0], utilityrun.Options{
			Runtime:       options["runtime"],
			RunID:         options["run-id"],
			EngineVersion: version,
			EngineCommit:  commit,
			BinaryPath:    binaryPath,
			Env:           utilityrun.CLILookupEnvironment(os.LookupEnv),
			Stdout:        commandStdout,
			Stderr:        os.Stderr,
		})
		if result.UtilityTestSpec != "" {
			if options["json"] == "1" {
				if err := utilityrun.RenderJSON(os.Stdout, result); err != nil {
					return err
				}
			} else if err := utilityrun.Render(os.Stdout, result); err != nil {
				return err
			}
		}
		if runErr != nil {
			return fmt.Errorf("utility run failed: %w", runErr)
		}
		return nil
	default:
		return runNamedKindCatalog("utility", "utility-test", catalog, args)
	}
}

func parseUtilityRunArgs(args []string) (map[string]string, []string, error) {
	return parseRunArgs(args, false)
}

func parseExperimentRunArgs(args []string) (map[string]string, []string, error) {
	return parseRunArgs(args, true)
}

func parseRunArgs(args []string, timing bool) (map[string]string, []string, error) {
	options := make(map[string]string)
	var inputs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			options["json"] = "1"
		case "--runtime", "--run-id":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a value", args[i])
			}
			options[strings.TrimPrefix(args[i], "--")] = args[i+1]
			i++
		case "--timeout", "--cleanup-grace":
			if !timing {
				return nil, nil, fmt.Errorf("unknown option: %s", args[i])
			}
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

func runUtilitySuite(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("utility-suite action is required")
	}

	switch args[0] {
	case "plan":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench utility-suite plan [--json] <utility-suite>")
		}
		plan, err := utilitysuite.Build(catalog, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return utilitysuite.RenderJSON(os.Stdout, plan)
		}
		return utilitysuite.Render(os.Stdout, plan)
	case "run":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench utility-suite run [--json] <utility-suite>")
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pgworkbench executable: %w", err)
		}
		result, runErr := utilitysuite.Run(root, catalog, inputs[0], utilitysuite.RunOptions{
			EngineVersion: version,
			EngineCommit:  commit,
			BinaryPath:    binaryPath,
			Env:           utilityrun.CLILookupEnvironment(os.LookupEnv),
			Stdout:        os.Stdout,
			Stderr:        os.Stderr,
		})
		if result.Suite != "" {
			if jsonOutput {
				if err := utilitysuite.RenderRunJSON(os.Stdout, result); err != nil {
					return err
				}
			} else if err := utilitysuite.RenderRun(os.Stdout, result); err != nil {
				return err
			}
		}
		if runErr != nil {
			return fmt.Errorf("utility-suite run failed: %w", runErr)
		}
		return nil
	case "run-list":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		summaries, err := utilitysuiteartifact.List(root, inputs)
		if err != nil {
			return err
		}
		if jsonOutput {
			return utilitysuiteartifact.RenderJSON(os.Stdout, summaries)
		}
		return utilitysuiteartifact.RenderList(os.Stdout, summaries)
	case "run-show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench utility-suite run-show [--json] <suite-run-dir-or-id>")
		}
		summary, err := utilitysuiteartifact.Show(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return utilitysuiteartifact.RenderJSON(os.Stdout, summary)
		}
		return utilitysuiteartifact.RenderShow(os.Stdout, summary)
	case "run-bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench utility-suite run-bundle [--json] <suite-run-dir-or-id> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		result, err := utilitysuiteartifact.CreateBundle(root, inputs[0], output)
		if err != nil {
			return err
		}
		if jsonOutput {
			return utilitysuiteartifact.RenderJSON(os.Stdout, result)
		}
		return utilitysuiteartifact.RenderBundle(os.Stdout, result)
	case "run-verify":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench utility-suite run-verify [--json] <suite-run-dir-or-id>")
		}
		result, err := utilitysuiteartifact.Verify(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := utilitysuiteartifact.RenderJSON(os.Stdout, result); err != nil {
				return err
			}
		} else if err := utilitysuiteartifact.RenderVerify(os.Stdout, result); err != nil {
			return err
		}
		if !result.IsValid() {
			return fmt.Errorf("utility-suite run verification failed")
		}
		return nil
	default:
		return runKindCatalog("utility-suite", catalog, args)
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
	case "run":
		options, inputs, err := parseExperimentRunArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench experiment run [--json] [--runtime docker|native] [--run-id id] [--timeout duration] [--cleanup-grace duration] <experiment-spec>")
		}
		executionTimeout, err := parsePositiveDurationFlag("--timeout", options["timeout"])
		if err != nil {
			return err
		}
		cleanupGrace, err := parsePositiveDurationFlag("--cleanup-grace", options["cleanup-grace"])
		if err != nil {
			return err
		}
		pack, err := scenariopack.ValidateForEngine(root, version)
		if err != nil {
			return fmt.Errorf("validate scenario pack: %w", err)
		}
		if pack.EngineCompatibility != nil && pack.EngineCompatibility.Status != scenariopack.EngineCompatibleRelease {
			printEngineCompatibility(os.Stderr, pack.EngineCompatibility)
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pgworkbench executable: %w", err)
		}
		commandStdout := io.Writer(os.Stdout)
		if options["json"] == "1" {
			commandStdout = os.Stderr
		}
		result, runErr := experimentrun.Run(root, catalog, inputs[0], experimentrun.Options{
			Runtime:          options["runtime"],
			RunID:            options["run-id"],
			EngineVersion:    version,
			EngineCommit:     commit,
			PackID:           pack.ID,
			PackVersion:      pack.Version,
			PackDigest:       pack.Digest,
			BinaryPath:       binaryPath,
			Stdout:           commandStdout,
			Stderr:           os.Stderr,
			ExecutionTimeout: executionTimeout,
			CleanupGrace:     cleanupGrace,
		})
		if renderErr := renderExperimentRunResult(os.Stdout, options["json"] == "1", result); renderErr != nil {
			return renderErr
		}
		if runErr != nil {
			return fmt.Errorf("experiment run failed: %w", runErr)
		}
		return nil
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

func renderExperimentRunResult(w io.Writer, jsonOutput bool, result experimentrun.Result) error {
	// Planning and option validation can fail before experimentrun has created a
	// v1 result. Emitting the Go zero value in that case would put invalid data on
	// a machine-readable stream while claiming the v1 schema. The command error
	// remains on stderr; a result is rendered only once its schema identity exists.
	if result.SchemaVersion == "" {
		return nil
	}
	if jsonOutput {
		return experimentrun.RenderJSON(w, result)
	}
	return experimentrun.Render(w, result)
}

func parsePositiveDurationFlag(name string, value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration such as 30s or 2h: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return duration, nil
}

func parseBenchmarkSamplerV2Args(args []string) (benchmarksampler.Options, error) {
	options := benchmarksampler.Options{}
	seen := map[string]bool{}
	durationSet := false
	samplesSet := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--run-dir", "--interval-seconds", "--duration-seconds", "--samples":
			if seen[argument] {
				return options, fmt.Errorf("duplicate option: %s", argument)
			}
			seen[argument] = true
			if index+1 >= len(args) {
				return options, fmt.Errorf("%s requires a value", argument)
			}
			value := args[index+1]
			index++
			switch argument {
			case "--run-dir":
				if !filepath.IsAbs(value) {
					return options, fmt.Errorf("--run-dir must be absolute")
				}
				options.RunDir = value
			case "--interval-seconds":
				seconds, err := strconv.Atoi(value)
				if err != nil || seconds <= 0 || seconds > 3600 {
					return options, fmt.Errorf("--interval-seconds must be an integer between 1 and 3600")
				}
				options.Interval = time.Duration(seconds) * time.Second
			case "--duration-seconds":
				seconds, err := strconv.Atoi(value)
				if err != nil || seconds < 0 || seconds > 86400 {
					return options, fmt.Errorf("--duration-seconds must be an integer between 0 and 86400")
				}
				options.Duration = time.Duration(seconds) * time.Second
				durationSet = true
			case "--samples":
				count, err := strconv.Atoi(value)
				if err != nil || count <= 0 || count > 10000 {
					return options, fmt.Errorf("--samples must be an integer between 1 and 10000")
				}
				options.Samples = count
				samplesSet = true
			}
		default:
			return options, fmt.Errorf("unknown option: %s", argument)
		}
	}
	if options.RunDir == "" || options.Interval == 0 || durationSet == samplesSet {
		return options, fmt.Errorf("usage: pgworkbench benchmark sample-metrics-v2 --run-dir absolute-linked-run --interval-seconds n (--duration-seconds n|--samples n)")
	}
	return options, nil
}

func parseBenchmarkControlMaterializerV2Args(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--run-dir" || !filepath.IsAbs(args[1]) {
		return "", fmt.Errorf("usage: pgworkbench benchmark materialize-controls-v2 --run-dir absolute-linked-run")
	}
	return args[1], nil
}

func runBenchmark(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("benchmark action is required")
	}

	switch args[0] {
	case "operation":
		return runOperationBenchmark(root, args[1:])
	case "drivers":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 0 {
			return fmt.Errorf("usage: pgworkbench benchmark drivers [--json]")
		}
		inspection, err := benchmarkdrivers.Load(root)
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkdrivers.RenderJSON(os.Stdout, inspection)
		}
		return benchmarkdrivers.Render(os.Stdout, inspection)
	case "driver-show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark driver-show [--json] <driver-id>")
		}
		inspection, err := benchmarkdrivers.Load(root)
		if err != nil {
			return err
		}
		driver, err := inspection.Registry.Find(inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkdrivers.RenderJSON(os.Stdout, struct {
				Path   string                  `json:"path"`
				Digest string                  `json:"digest"`
				Driver benchmarkdrivers.Driver `json:"driver"`
			}{Path: inspection.Path, Digest: inspection.Digest, Driver: driver})
		}
		return benchmarkdrivers.RenderDriver(os.Stdout, inspection, driver)
	case "driver-run":
		options, jsonOutput, err := parseBenchmarkDriverRunArgs(args[1:])
		if err != nil {
			return err
		}
		options.Root = root
		artifact, err := benchmarkexternal.Run(options)
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkexternal.RenderJSON(os.Stdout, artifact)
		}
		return benchmarkexternal.Render(os.Stdout, artifact)
	case "driver-run-verify":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark driver-run-verify [--json] <execution-dir-or-execution.json>")
		}
		verification, err := benchmarkexternal.Verify(inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkexternal.RenderJSON(os.Stdout, verification); err != nil {
				return err
			}
		} else if err := benchmarkexternal.RenderVerification(os.Stdout, verification); err != nil {
			return err
		}
		if !verification.IsValid() {
			return fmt.Errorf("external benchmark driver execution verification failed")
		}
		return nil
	case "materialize-controls-v2":
		runDir, err := parseBenchmarkControlMaterializerV2Args(args[1:])
		if err != nil {
			return err
		}
		controls, err := benchmarkrun.MaterializeControlsV2FromEnvironment(root, runDir, os.Getenv)
		if err != nil {
			return err
		}
		fmt.Printf("Wrote benchmark protocol-v2 controls: cache=%s reset=%s overhead=%s resource=%s\n",
			controls.CacheState.Path, controls.StatisticsReset.Path, controls.CollectorOverhead.Path, controls.ResourceBudget.Path)
		return nil
	case "sample-metrics-v2":
		options, err := parseBenchmarkSamplerV2Args(args[1:])
		if err != nil {
			return err
		}
		options.Root = root
		options.ExpectedRunID = os.Getenv("PGWORKBENCH_BENCHMARK_RUN_ID")
		switch os.Getenv("PGWORKBENCH_BENCHMARK_COLLECTOR_OVERHEAD_MODE") {
		case "runner-calibrated-duty-cycle":
			options.RecordTiming = true
		case "included-unquantified":
			options.RecordTiming = false
		default:
			return fmt.Errorf("sampler-v2 requires an exact benchmark collector overhead mode")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
		defer stop()
		options.Context = ctx
		result, err := benchmarksampler.Run(options)
		if err != nil {
			return err
		}
		fmt.Printf("Wrote PostgreSQL sampler-v2 evidence: metrics=%s timing=%s samples=%d\n", result.MetricsPath, result.TimingPath, result.Samples)
		return nil
	case "import":
		options, inputs, err := parseBenchmarkImportArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 2 {
			return fmt.Errorf("usage: pgworkbench benchmark import <hammerdb6|hammerdb6report|sysbench1|benchbase|benchbase33c0047> [--json] [--manifest mapping.json] [--workload id] <source> <output-dir>")
		}
		artifact, err := benchmarkimport.Create(options.Adapter, inputs[0], inputs[1], options.Import)
		if err != nil {
			return err
		}
		if options.JSON {
			return benchmarkimport.RenderJSON(os.Stdout, artifact)
		}
		return benchmarkimport.Render(os.Stdout, artifact)
	case "import-verify":
		jsonOutput, requireBundle, inputs, err := parseBenchmarkVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark import-verify [--json] [--bundle] <import-dir-or-result.json>")
		}
		var verification benchmarkimport.Verification
		if requireBundle {
			verification, err = benchmarkimport.VerifyBundle(inputs[0])
		} else {
			verification, err = benchmarkimport.Verify(inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkimport.RenderJSON(os.Stdout, verification); err != nil {
				return err
			}
		} else if err := benchmarkimport.RenderVerification(os.Stdout, verification); err != nil {
			return err
		}
		if !verification.IsValid() {
			return fmt.Errorf("benchmark import verification failed")
		}
		return nil
	case "import-bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench benchmark import-bundle [--json] <import-dir-or-result.json> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		bundle, err := benchmarkimport.CreateBundle(inputs[0], output, time.Unix(0, 0).UTC())
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkimport.RenderJSON(os.Stdout, bundle)
		}
		return benchmarkimport.RenderBundle(os.Stdout, bundle)
	case "plan":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark plan [--json] <benchmark>")
		}
		plan, err := benchmarkplan.Build(catalog, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkplan.RenderJSON(os.Stdout, plan)
		}
		return benchmarkplan.Render(os.Stdout, plan)
	case "run":
		options, inputs, err := parseBenchmarkRunArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark run [--json] [--runtime docker|native] [--native-bindir absolute-path] [--run-id id] [--subject label] <benchmark>")
		}
		pack, err := scenariopack.ValidateForEngine(root, version)
		if err != nil {
			return fmt.Errorf("validate scenario pack: %w", err)
		}
		if pack.EngineCompatibility != nil && pack.EngineCompatibility.Status != scenariopack.EngineCompatibleRelease {
			printEngineCompatibility(os.Stderr, pack.EngineCompatibility)
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pgworkbench executable: %w", err)
		}
		commandStdout := io.Writer(os.Stdout)
		if options["json"] == "1" {
			commandStdout = os.Stderr
		}
		nativeBindir, err := resolveCLINativeBindir(options["runtime"], options["native-bindir"])
		if err != nil {
			return err
		}
		result, runErr := benchmarkrun.Run(root, catalog, inputs[0], benchmarkrun.Options{
			Runtime:       options["runtime"],
			RunID:         options["run-id"],
			Subject:       options["subject"],
			EngineVersion: version,
			EngineCommit:  commit,
			PackID:        pack.ID,
			PackVersion:   pack.Version,
			PackDigest:    pack.Digest,
			BinaryPath:    binaryPath,
			NativeBindir:  nativeBindir,
			Stdout:        commandStdout,
			Stderr:        os.Stderr,
		})
		if options["json"] == "1" {
			if renderErr := benchmarkrun.RenderJSON(os.Stdout, result); renderErr != nil {
				return renderErr
			}
		} else if result.Benchmark != "" {
			if renderErr := benchmarkrun.Render(os.Stdout, result); renderErr != nil {
				return renderErr
			}
		}
		if runErr != nil {
			return fmt.Errorf("benchmark run failed: %w", runErr)
		}
		return nil
	case "run-show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark run-show [--json] <benchmark-series-dir-or-id>")
		}
		series, err := benchmarkartifact.Load(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkrun.RenderJSON(os.Stdout, series)
		}
		return benchmarkrun.Render(os.Stdout, series)
	case "run-verify":
		jsonOutput, requireBundle, inputs, err := parseBenchmarkVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark run-verify [--json] [--bundle] <benchmark-series-dir-or-id>")
		}
		var result benchmarkartifact.VerifyResult
		if requireBundle {
			result, err = benchmarkartifact.VerifyBundle(root, inputs[0])
		} else {
			result, err = benchmarkartifact.Verify(root, inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkartifact.RenderJSON(os.Stdout, result); err != nil {
				return err
			}
		} else if err := benchmarkartifact.RenderVerify(os.Stdout, result); err != nil {
			return err
		}
		if !result.IsValid() {
			return fmt.Errorf("benchmark series verification failed")
		}
		return nil
	case "run-bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench benchmark run-bundle [--json] <benchmark-series-dir-or-id> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		result, err := benchmarkartifact.CreateBundle(root, inputs[0], output, time.Unix(0, 0).UTC())
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkartifact.RenderJSON(os.Stdout, result)
		}
		return benchmarkartifact.RenderBundle(os.Stdout, result)
	case "compare":
		compareOptions, jsonOutput, inputs, err := parseBenchmarkCompareArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 2 {
			return fmt.Errorf("usage: pgworkbench benchmark compare [--json] [--bootstrap-resamples n] [--confidence value] [--seed n] <baseline-series> <candidate-series>")
		}
		comparison, err := benchmarkcompare.Compare(root, inputs[0], inputs[1], compareOptions)
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkcompare.RenderJSON(os.Stdout, comparison); err != nil {
				return err
			}
		} else if err := benchmarkcompare.Render(os.Stdout, comparison); err != nil {
			return err
		}
		if comparison.Status != "passed" {
			return fmt.Errorf("benchmark comparison ended with status %s", comparison.Status)
		}
		return nil
	case "history-create":
		jsonOutput, historyID, inputs, err := parseBenchmarkHistoryCreateArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 2 {
			return fmt.Errorf("usage: pgworkbench benchmark history-create [--json] [--history-id id] <benchmark-series> <benchmark-series> [...]")
		}
		result, err := benchmarkhistory.Create(root, inputs, benchmarkhistory.Options{HistoryID: historyID})
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkhistory.RenderJSON(os.Stdout, result)
		}
		return benchmarkhistory.Render(os.Stdout, result)
	case "history-show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark history-show [--json] <benchmark-history-dir-or-id>")
		}
		result, err := benchmarkhistory.Load(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkhistory.RenderJSON(os.Stdout, result)
		}
		return benchmarkhistory.Render(os.Stdout, result)
	case "history-verify":
		jsonOutput, requireBundle, inputs, err := parseBenchmarkVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark history-verify [--json] [--bundle] <benchmark-history-dir-or-id>")
		}
		var verification benchmarkhistory.VerifyResult
		if requireBundle {
			verification, err = benchmarkhistory.VerifyBundle(root, inputs[0])
		} else {
			verification, err = benchmarkhistory.Verify(root, inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkhistory.RenderJSON(os.Stdout, verification); err != nil {
				return err
			}
		} else if err := benchmarkhistory.RenderVerify(os.Stdout, verification); err != nil {
			return err
		}
		if !verification.IsValid() {
			return fmt.Errorf("benchmark history verification failed")
		}
		return nil
	case "history-bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench benchmark history-bundle [--json] <benchmark-history-dir-or-id> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		bundle, err := benchmarkhistory.CreateBundle(root, inputs[0], output, time.Unix(0, 0).UTC())
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkhistory.RenderJSON(os.Stdout, bundle)
		}
		return benchmarkhistory.RenderBundle(os.Stdout, bundle)
	case "campaign-run":
		options, inputs, err := parseBenchmarkCampaignRunArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) == 0 {
			return fmt.Errorf("usage: pgworkbench benchmark campaign-run [--json] [--runtime docker|native] [--native-bindir absolute-path] [--campaign-id id] [--subject label] <benchmark> [benchmark...]")
		}
		pack, err := scenariopack.ValidateForEngine(root, version)
		if err != nil {
			return fmt.Errorf("validate scenario pack: %w", err)
		}
		if pack.EngineCompatibility != nil && pack.EngineCompatibility.Status != scenariopack.EngineCompatibleRelease {
			printEngineCompatibility(os.Stderr, pack.EngineCompatibility)
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pgworkbench executable: %w", err)
		}
		nativeBindir, err := resolveCLINativeBindir(options.Run.Runtime, options.Run.SeriesOptions.NativeBindir)
		if err != nil {
			return err
		}
		commandStdout := io.Writer(os.Stdout)
		if options.JSON {
			commandStdout = os.Stderr
		}
		options.Run.SeriesOptions = benchmarkrun.Options{
			EngineVersion: version,
			EngineCommit:  commit,
			PackID:        pack.ID,
			PackVersion:   pack.Version,
			PackDigest:    pack.Digest,
			BinaryPath:    binaryPath,
			NativeBindir:  nativeBindir,
			Stdout:        commandStdout,
			Stderr:        os.Stderr,
		}
		options.Run.Stdout = commandStdout
		options.Run.Stderr = os.Stderr
		result, runErr := benchmarkcampaign.Run(root, catalog, inputs, options.Run)
		if options.JSON {
			if renderErr := benchmarkcampaign.RenderJSON(os.Stdout, result); renderErr != nil {
				return renderErr
			}
		} else if result.CampaignID != "" {
			if renderErr := benchmarkcampaign.Render(os.Stdout, result); renderErr != nil {
				return renderErr
			}
		}
		if runErr != nil {
			return fmt.Errorf("benchmark campaign failed: %w", runErr)
		}
		return nil
	case "campaign-show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark campaign-show [--json] <benchmark-campaign-dir-or-id>")
		}
		result, err := benchmarkcampaign.Load(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkcampaign.RenderJSON(os.Stdout, result)
		}
		return benchmarkcampaign.Render(os.Stdout, result)
	case "campaign-verify":
		jsonOutput, requireBundle, inputs, err := parseBenchmarkVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark campaign-verify [--json] [--bundle] <benchmark-campaign-dir-or-id>")
		}
		var verification benchmarkcampaign.VerifyResult
		if requireBundle {
			verification, err = benchmarkcampaign.VerifyBundle(root, inputs[0])
		} else {
			verification, err = benchmarkcampaign.Verify(root, inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkcampaign.RenderJSON(os.Stdout, verification); err != nil {
				return err
			}
		} else if err := benchmarkcampaign.RenderVerify(os.Stdout, verification); err != nil {
			return err
		}
		if !verification.IsValid() {
			return fmt.Errorf("benchmark campaign verification failed")
		}
		return nil
	case "campaign-bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench benchmark campaign-bundle [--json] <benchmark-campaign-dir-or-id> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		bundle, err := benchmarkcampaign.CreateBundle(root, inputs[0], output, time.Unix(0, 0).UTC())
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkcampaign.RenderJSON(os.Stdout, bundle)
		}
		return benchmarkcampaign.RenderBundle(os.Stdout, bundle)
	case "ab-run":
		options, inputs, err := parseBenchmarkABRunArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 2 {
			return fmt.Errorf("usage: pgworkbench benchmark ab-run [options] <baseline-benchmark> <candidate-benchmark>")
		}
		pack, err := scenariopack.ValidateForEngine(root, version)
		if err != nil {
			return fmt.Errorf("validate scenario pack: %w", err)
		}
		if pack.EngineCompatibility != nil && pack.EngineCompatibility.Status != scenariopack.EngineCompatibleRelease {
			printEngineCompatibility(os.Stderr, pack.EngineCompatibility)
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pgworkbench executable: %w", err)
		}
		dimension := options.Run.SubjectDimension
		if dimension == "" {
			dimension = benchmarkab.SubjectPGConfig
		}
		nativeBindir := ""
		if dimension != benchmarkab.SubjectNativeToolchain {
			nativeBindir, err = resolveCLINativeBindir(options.Run.Runtime, options.Run.SeriesOptions.NativeBindir)
			if err != nil {
				return err
			}
		}
		commandStdout := io.Writer(os.Stdout)
		if options.JSON {
			commandStdout = os.Stderr
		}
		options.Run.SeriesOptions = benchmarkrun.Options{
			EngineVersion: version,
			EngineCommit:  commit,
			PackID:        pack.ID,
			PackVersion:   pack.Version,
			PackDigest:    pack.Digest,
			BinaryPath:    binaryPath,
			NativeBindir:  nativeBindir,
			Stdout:        commandStdout,
			Stderr:        os.Stderr,
		}
		options.Run.Stdout = commandStdout
		options.Run.Stderr = os.Stderr
		result, runErr := benchmarkab.Run(root, catalog, inputs[0], inputs[1], options.Run)
		if options.JSON {
			if renderErr := benchmarkab.RenderJSON(os.Stdout, result); renderErr != nil {
				return renderErr
			}
		} else if result.RunID != "" {
			if renderErr := benchmarkab.Render(os.Stdout, result); renderErr != nil {
				return renderErr
			}
		}
		if runErr != nil {
			return fmt.Errorf("counterbalanced benchmark failed: %w", runErr)
		}
		return nil
	case "ab-show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark ab-show [--json] <ab-run-dir-or-id>")
		}
		result, err := benchmarkab.Load(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkab.RenderJSON(os.Stdout, result)
		}
		return benchmarkab.Render(os.Stdout, result)
	case "ab-verify":
		jsonOutput, requireBundle, inputs, err := parseBenchmarkVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark ab-verify [--json] [--bundle] <ab-run-dir-or-id>")
		}
		var verification benchmarkab.VerifyResult
		if requireBundle {
			verification, err = benchmarkab.VerifyBundle(root, inputs[0])
		} else {
			verification, err = benchmarkab.Verify(root, inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkab.RenderJSON(os.Stdout, verification); err != nil {
				return err
			}
		} else if err := benchmarkab.RenderVerify(os.Stdout, verification); err != nil {
			return err
		}
		if !verification.IsValid() {
			return fmt.Errorf("counterbalanced benchmark verification failed")
		}
		return nil
	case "ab-bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench benchmark ab-bundle [--json] <ab-run-dir-or-id> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		bundle, err := benchmarkab.CreateBundle(root, inputs[0], output, time.Unix(0, 0).UTC())
		if err != nil {
			return err
		}
		if jsonOutput {
			return benchmarkab.RenderJSON(os.Stdout, bundle)
		}
		return benchmarkab.RenderBundle(os.Stdout, bundle)
	case "host-inspect":
		options, err := parseBenchmarkHostInspectArgs(args[1:])
		if err != nil {
			return err
		}
		artifact, err := benchmarkqualify.Inspect(options.Inspect)
		if err != nil {
			return fmt.Errorf("inspect benchmark host: %w", err)
		}
		if options.Output != "" {
			if err := benchmarkqualify.WriteFile(options.Output, artifact); err != nil {
				return fmt.Errorf("write benchmark host artifact: %w", err)
			}
		}
		if options.JSON {
			return benchmarkqualify.RenderJSON(os.Stdout, artifact)
		}
		if err := benchmarkqualify.Render(os.Stdout, artifact); err != nil {
			return err
		}
		if options.Output != "" {
			_, err = fmt.Fprintf(os.Stdout, "Artifact: %s\n", options.Output)
		}
		return err
	case "host-verify":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark host-verify [--json] <host-qualification.json>")
		}
		verification, err := benchmarkqualify.VerifyFile(inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := benchmarkqualify.RenderJSON(os.Stdout, verification); err != nil {
				return err
			}
		} else if err := benchmarkqualify.RenderVerification(os.Stdout, verification); err != nil {
			return err
		}
		if !verification.Valid {
			return fmt.Errorf("benchmark host verification failed")
		}
		return nil
	default:
		return runKindCatalog("benchmark", catalog, args)
	}
}

type benchmarkHostInspectCLIOptions struct {
	JSON    bool
	Output  string
	Inspect benchmarkqualify.InspectOptions
}

type benchmarkImportCLIOptions struct {
	JSON    bool
	Adapter string
	Import  benchmarkimport.Options
}

func parseBenchmarkDriverRunArgs(args []string) (benchmarkexternal.Options, bool, error) {
	var options benchmarkexternal.Options
	var jsonOutput bool
	var inputs []string
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			inputs = append(inputs, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") {
			inputs = append(inputs, argument)
			continue
		}
		if seen[argument] {
			return options, false, fmt.Errorf("duplicate option: %s", argument)
		}
		seen[argument] = true
		if argument == "--json" {
			jsonOutput = true
			continue
		}
		if argument == "--acknowledge-external-disposable-target" {
			options.AcknowledgeExternalDisposableTarget = true
			continue
		}
		if !oneOfString(argument, "--driver", "--runtime-root", "--binary", "--config", "--script", "--workload", "--timeout") {
			return options, false, fmt.Errorf("unknown option: %s", argument)
		}
		if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
			return options, false, fmt.Errorf("%s requires a value", argument)
		}
		value := args[index+1]
		index++
		switch argument {
		case "--driver":
			options.DriverID = value
		case "--runtime-root":
			options.RuntimeRoot = value
		case "--binary":
			options.BinaryPath = value
		case "--config":
			options.ConfigPath = value
		case "--script":
			options.ScriptPath = value
		case "--workload":
			options.Workload = value
		case "--timeout":
			duration, err := time.ParseDuration(value)
			if err != nil {
				return options, false, fmt.Errorf("invalid --timeout duration %q: %w", value, err)
			}
			options.Timeout = duration
		}
	}
	if len(inputs) != 1 || options.DriverID == "" || options.RuntimeRoot == "" || options.BinaryPath == "" || options.ConfigPath == "" || options.ScriptPath == "" || options.Workload == "" || !options.AcknowledgeExternalDisposableTarget {
		return options, false, fmt.Errorf("usage: pgworkbench benchmark driver-run [--json] --acknowledge-external-disposable-target --driver id --runtime-root dir --binary file --config file --script file --workload id [--timeout duration] <output-dir>")
	}
	options.OutputDir = inputs[0]
	return options, jsonOutput, nil
}

type benchmarkCampaignRunCLIOptions struct {
	JSON bool
	Run  benchmarkcampaign.Options
}

func parseBenchmarkCampaignRunArgs(args []string) (benchmarkCampaignRunCLIOptions, []string, error) {
	var options benchmarkCampaignRunCLIOptions
	var inputs []string
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			inputs = append(inputs, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") {
			inputs = append(inputs, argument)
			continue
		}
		if seen[argument] {
			return options, nil, fmt.Errorf("duplicate option: %s", argument)
		}
		seen[argument] = true
		switch argument {
		case "--json":
			options.JSON = true
		case "--runtime", "--native-bindir", "--campaign-id", "--subject":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return options, nil, fmt.Errorf("%s requires a value", argument)
			}
			value := args[index+1]
			index++
			switch argument {
			case "--runtime":
				if value != "docker" && value != "native" {
					return options, nil, fmt.Errorf("unsupported runtime %q: expected docker or native", value)
				}
				options.Run.Runtime = value
			case "--native-bindir":
				if !filepath.IsAbs(value) || filepath.Clean(value) != value {
					return options, nil, fmt.Errorf("--native-bindir must be a clean absolute path")
				}
				options.Run.SeriesOptions.NativeBindir = value
			case "--campaign-id":
				if !benchmarkrun.ValidRunID(value) || !benchmarkrun.ValidRunID(value+"-s001") {
					return options, nil, fmt.Errorf("invalid benchmark campaign id %q", value)
				}
				options.Run.CampaignID = value
			case "--subject":
				if strings.TrimSpace(value) != value || value == "" || strings.ContainsAny(value, "\r\n") {
					return options, nil, fmt.Errorf("--subject must be a non-empty single line")
				}
				options.Run.Subject = value
			}
		default:
			return options, nil, fmt.Errorf("unknown option: %s", argument)
		}
	}
	return options, inputs, nil
}

func parseBenchmarkImportArgs(args []string) (benchmarkImportCLIOptions, []string, error) {
	var options benchmarkImportCLIOptions
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return options, nil, fmt.Errorf("benchmark import adapter is required before options")
	}
	options.Adapter = args[0]
	if !oneOfString(
		options.Adapter,
		benchmarkimport.AdapterHammerDB6,
		benchmarkimport.AdapterHammerDB6Report,
		benchmarkimport.AdapterSysbench1,
		benchmarkimport.AdapterBenchBase,
		benchmarkimport.AdapterBenchBase33c0047,
	) {
		return options, nil, fmt.Errorf(
			"unsupported benchmark import adapter %q; expected hammerdb6, hammerdb6report, sysbench1, benchbase, or benchbase33c0047",
			options.Adapter,
		)
	}
	seen := make(map[string]bool)
	var inputs []string
	for index := 1; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			inputs = append(inputs, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") {
			inputs = append(inputs, argument)
			continue
		}
		if seen[argument] {
			return options, nil, fmt.Errorf("duplicate option: %s", argument)
		}
		seen[argument] = true
		switch argument {
		case "--json":
			options.JSON = true
		case "--manifest", "--workload":
			if index+1 >= len(args) {
				return options, nil, fmt.Errorf("%s requires a value", argument)
			}
			value := args[index+1]
			if strings.HasPrefix(value, "-") {
				return options, nil, fmt.Errorf("%s requires a value", argument)
			}
			index++
			if argument == "--manifest" {
				options.Import.MappingPath = value
			} else {
				options.Import.Workload = value
			}
		default:
			return options, nil, fmt.Errorf("unknown option: %s", argument)
		}
	}
	return options, inputs, nil
}

type benchmarkABRunCLIOptions struct {
	JSON bool
	Run  benchmarkab.Options
}

func parseBenchmarkABRunArgs(args []string) (benchmarkABRunCLIOptions, []string, error) {
	var options benchmarkABRunCLIOptions
	var inputs []string
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			inputs = append(inputs, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") {
			inputs = append(inputs, argument)
			continue
		}
		if seen[argument] {
			return options, nil, fmt.Errorf("duplicate option: %s", argument)
		}
		seen[argument] = true
		switch argument {
		case "--json":
			options.JSON = true
		case "--strict":
			options.Run.Qualification.Policy.Strict = true
		case "--runtime", "--run-id", "--subject-dimension", "--native-bindir", "--baseline-native-bindir", "--candidate-native-bindir", "--baseline-subject", "--candidate-subject", "--bootstrap-resamples", "--confidence", "--seed", "--max-bookend-gap-seconds", "--storage-path", "--storage-label", "--client-placement", "--min-logical-cpus", "--min-memory-available-pct", "--min-storage-available-pct", "--max-load-1m-per-cpu", "--required-clocksource", "--required-governor", "--max-temperature-celsius", "--required-client-placement":
			if index+1 >= len(args) {
				return options, nil, fmt.Errorf("%s requires a value", argument)
			}
			value := args[index+1]
			index++
			switch argument {
			case "--runtime":
				if value != "docker" && value != "native" {
					return options, nil, fmt.Errorf("unsupported runtime %q: expected docker or native", value)
				}
				options.Run.Runtime = value
			case "--run-id":
				if !benchmarkrun.ValidRunID(value) {
					return options, nil, fmt.Errorf("invalid counterbalanced benchmark run id %q", value)
				}
				options.Run.RunID = value
			case "--subject-dimension":
				if value != benchmarkab.SubjectPGConfig && value != benchmarkab.SubjectNativeToolchain {
					return options, nil, fmt.Errorf("--subject-dimension must be pg_config or native_toolchain")
				}
				options.Run.SubjectDimension = value
			case "--native-bindir":
				if !filepath.IsAbs(value) || filepath.Clean(value) != value {
					return options, nil, fmt.Errorf("--native-bindir must be a clean absolute path")
				}
				options.Run.SeriesOptions.NativeBindir = value
			case "--baseline-native-bindir", "--candidate-native-bindir":
				if !filepath.IsAbs(value) || filepath.Clean(value) != value {
					return options, nil, fmt.Errorf("%s must be a clean absolute path", argument)
				}
				if argument == "--baseline-native-bindir" {
					options.Run.BaselineNativeBindir = value
				} else {
					options.Run.CandidateNativeBindir = value
				}
			case "--baseline-subject":
				options.Run.BaselineSubject = value
			case "--candidate-subject":
				options.Run.CandidateSubject = value
			case "--bootstrap-resamples":
				parsed, err := strconv.Atoi(value)
				if err != nil || parsed < 1000 || parsed > 1000000 {
					return options, nil, fmt.Errorf("--bootstrap-resamples must be between 1000 and 1000000")
				}
				options.Run.BootstrapResamples = parsed
			case "--confidence":
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed <= 0.5 || parsed >= 1 {
					return options, nil, fmt.Errorf("--confidence must be greater than 0.5 and less than 1")
				}
				options.Run.ConfidenceLevel = parsed
			case "--seed":
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return options, nil, fmt.Errorf("--seed must be a uint64")
				}
				options.Run.Seed = parsed
			case "--max-bookend-gap-seconds":
				parsed, err := strconv.ParseInt(value, 10, 64)
				if err != nil || parsed <= 0 {
					return options, nil, fmt.Errorf("--max-bookend-gap-seconds must be a positive integer")
				}
				options.Run.MaxBookendGapSeconds = parsed
			case "--storage-path":
				options.Run.Qualification.StoragePath = value
			case "--storage-label":
				options.Run.Qualification.StorageLabel = value
			case "--client-placement":
				options.Run.Qualification.ClientPlacement = value
			case "--min-logical-cpus":
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err != nil || parsed == 0 {
					return options, nil, fmt.Errorf("--min-logical-cpus must be a positive integer")
				}
				options.Run.Qualification.Policy.MinLogicalCPUs = &parsed
			case "--min-memory-available-pct", "--min-storage-available-pct", "--max-load-1m-per-cpu", "--max-temperature-celsius":
				parsed, err := parseQualificationFloat(argument, value)
				if err != nil {
					return options, nil, err
				}
				switch argument {
				case "--min-memory-available-pct":
					options.Run.Qualification.Policy.MinMemoryAvailablePct = &parsed
				case "--min-storage-available-pct":
					options.Run.Qualification.Policy.MinStorageAvailablePct = &parsed
				case "--max-load-1m-per-cpu":
					options.Run.Qualification.Policy.MaxLoad1PerCPU = &parsed
				case "--max-temperature-celsius":
					options.Run.Qualification.Policy.MaxTemperatureCelsius = &parsed
				}
			case "--required-clocksource":
				options.Run.Qualification.Policy.RequiredClocksource = value
			case "--required-governor":
				options.Run.Qualification.Policy.RequiredGovernor = value
			case "--required-client-placement":
				options.Run.Qualification.Policy.RequiredClientPlacement = value
			}
		default:
			return options, nil, fmt.Errorf("unknown option: %s", argument)
		}
	}
	dimension := options.Run.SubjectDimension
	if dimension == "" {
		dimension = benchmarkab.SubjectPGConfig
	}
	if dimension == benchmarkab.SubjectNativeToolchain {
		if options.Run.Runtime != "native" {
			return options, nil, fmt.Errorf("native_toolchain subject dimension requires --runtime native")
		}
		if options.Run.BaselineNativeBindir == "" || options.Run.CandidateNativeBindir == "" {
			return options, nil, fmt.Errorf("native_toolchain subject dimension requires both --baseline-native-bindir and --candidate-native-bindir")
		}
		if options.Run.SeriesOptions.NativeBindir != "" {
			return options, nil, fmt.Errorf("--native-bindir is for pg_config subjects; native_toolchain requires the two arm-specific bindirs")
		}
	} else if options.Run.BaselineNativeBindir != "" || options.Run.CandidateNativeBindir != "" {
		return options, nil, fmt.Errorf("native bindir options require --subject-dimension native_toolchain")
	}
	return options, inputs, nil
}

func parseBenchmarkHostInspectArgs(args []string) (benchmarkHostInspectCLIOptions, error) {
	var options benchmarkHostInspectCLIOptions
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			if index+1 != len(args) {
				return options, fmt.Errorf("host-inspect does not accept positional arguments")
			}
			return options, nil
		}
		if !strings.HasPrefix(argument, "-") {
			return options, fmt.Errorf("host-inspect does not accept positional arguments: %s", argument)
		}
		if seen[argument] {
			return options, fmt.Errorf("duplicate option: %s", argument)
		}
		seen[argument] = true
		switch argument {
		case "--json":
			options.JSON = true
		case "--strict":
			options.Inspect.Policy.Strict = true
		case "--output", "--storage-path", "--storage-label", "--client-placement", "--min-logical-cpus", "--min-memory-available-pct", "--min-storage-available-pct", "--max-load-1m-per-cpu", "--required-clocksource", "--required-governor", "--max-temperature-celsius", "--required-client-placement":
			if index+1 >= len(args) {
				return options, fmt.Errorf("%s requires a value", argument)
			}
			value := args[index+1]
			index++
			switch argument {
			case "--output":
				options.Output = value
			case "--storage-path":
				options.Inspect.StoragePath = value
			case "--storage-label":
				options.Inspect.StorageLabel = value
			case "--client-placement":
				options.Inspect.ClientPlacement = value
			case "--min-logical-cpus":
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err != nil || parsed == 0 {
					return options, fmt.Errorf("--min-logical-cpus must be a positive integer")
				}
				options.Inspect.Policy.MinLogicalCPUs = &parsed
			case "--min-memory-available-pct":
				parsed, err := parseQualificationFloat(argument, value)
				if err != nil {
					return options, err
				}
				options.Inspect.Policy.MinMemoryAvailablePct = &parsed
			case "--min-storage-available-pct":
				parsed, err := parseQualificationFloat(argument, value)
				if err != nil {
					return options, err
				}
				options.Inspect.Policy.MinStorageAvailablePct = &parsed
			case "--max-load-1m-per-cpu":
				parsed, err := parseQualificationFloat(argument, value)
				if err != nil {
					return options, err
				}
				options.Inspect.Policy.MaxLoad1PerCPU = &parsed
			case "--required-clocksource":
				options.Inspect.Policy.RequiredClocksource = value
			case "--required-governor":
				options.Inspect.Policy.RequiredGovernor = value
			case "--max-temperature-celsius":
				parsed, err := parseQualificationFloat(argument, value)
				if err != nil {
					return options, err
				}
				options.Inspect.Policy.MaxTemperatureCelsius = &parsed
			case "--required-client-placement":
				options.Inspect.Policy.RequiredClientPlacement = value
			}
		default:
			return options, fmt.Errorf("unknown option: %s", argument)
		}
	}
	return options, nil
}

func parseQualificationFloat(option, value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", option)
	}
	return parsed, nil
}

func runOperationBenchmark(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("operation benchmark action is required")
	}
	catalog := operationbench.NewCatalog(root)
	switch args[0] {
	case "list":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 0 {
			return fmt.Errorf("usage: pgworkbench benchmark operation list [--json]")
		}
		specs, err := catalog.List()
		if err != nil {
			return err
		}
		if jsonOutput {
			return operationbench.RenderJSON(os.Stdout, specs)
		}
		return operationbench.RenderCatalog(os.Stdout, specs)
	case "show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark operation show [--json] <operation>")
		}
		spec, err := catalog.Load(inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return operationbench.RenderJSON(os.Stdout, spec)
		}
		return operationbench.RenderSpec(os.Stdout, spec)
	case "run":
		options, inputs, err := parseBenchmarkRunArgs(args[1:])
		if err != nil {
			return err
		}
		if options["subject"] != "" || len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark operation run [--json] [--runtime docker|native] [--run-id id] <operation>")
		}
		pack, err := scenariopack.ValidateForEngine(root, version)
		if err != nil {
			return fmt.Errorf("validate scenario pack: %w", err)
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pgworkbench executable: %w", err)
		}
		commandStdout := io.Writer(os.Stdout)
		if options["json"] == "1" {
			commandStdout = os.Stderr
		}
		nativeBindir, err := resolveCLINativeBindir(options["runtime"], options["native-bindir"])
		if err != nil {
			return err
		}
		series, runErr := operationbench.Run(root, inputs[0], operationbench.Options{
			Runtime:       options["runtime"],
			RunID:         options["run-id"],
			PackID:        pack.ID,
			PackVersion:   pack.Version,
			PackDigest:    pack.Digest,
			EngineVersion: version,
			EngineCommit:  commit,
			BinaryPath:    binaryPath,
			NativeBindir:  nativeBindir,
			Stdout:        commandStdout,
			Stderr:        os.Stderr,
		})
		if runErr != nil {
			return runErr
		}
		if options["json"] == "1" {
			if err := operationbench.RenderJSON(os.Stdout, series); err != nil {
				return err
			}
		} else if series.Operation != "" {
			if err := operationbench.RenderSeries(os.Stdout, series); err != nil {
				return err
			}
		}
		if series.Status != "passed" {
			return fmt.Errorf("operation benchmark ended with status %s", series.Status)
		}
		return nil
	case "run-show":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark operation run-show [--json] <series-dir-or-id>")
		}
		series, err := operationbench.Load(root, inputs[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return operationbench.RenderJSON(os.Stdout, series)
		}
		return operationbench.RenderSeries(os.Stdout, series)
	case "verify":
		jsonOutput, requireBundle, inputs, err := parseBenchmarkVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench benchmark operation verify [--json] [--bundle] <series-dir-or-id>")
		}
		var result operationbench.VerifyResult
		if requireBundle {
			result, err = operationbench.VerifyBundle(root, inputs[0])
		} else {
			result, err = operationbench.Verify(root, inputs[0])
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := operationbench.RenderJSON(os.Stdout, result); err != nil {
				return err
			}
		} else if err := operationbench.RenderVerify(os.Stdout, result); err != nil {
			return err
		}
		if !result.Valid {
			return fmt.Errorf("operation benchmark verification failed")
		}
		return nil
	case "bundle":
		jsonOutput, inputs, err := parseJSONOptionArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) < 1 || len(inputs) > 2 {
			return fmt.Errorf("usage: pgworkbench benchmark operation bundle [--json] <series-dir-or-id> [output.tar.gz]")
		}
		output := ""
		if len(inputs) == 2 {
			output = inputs[1]
		}
		bundle, err := operationbench.CreateBundle(root, inputs[0], output, time.Unix(0, 0).UTC())
		if err != nil {
			return err
		}
		if jsonOutput {
			return operationbench.RenderJSON(os.Stdout, bundle)
		}
		return operationbench.RenderBundle(os.Stdout, bundle)
	default:
		return fmt.Errorf("unsupported operation benchmark action: %s", args[0])
	}
}

func resolveCLINativeBindir(runtimeName, explicit string) (string, error) {
	runtimeName = valueOr(strings.TrimSpace(runtimeName), valueOr(strings.TrimSpace(os.Getenv("PGWORKBENCH_RUNTIME")), "docker"))
	explicit = strings.TrimSpace(explicit)
	if runtimeName != "native" {
		if explicit != "" {
			return "", fmt.Errorf("--native-bindir requires --runtime native")
		}
		return "", nil
	}
	candidate := explicit
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv("PGWORKBENCH_NATIVE_BINDIR"))
	}
	if candidate == "" {
		if installDir := strings.TrimSpace(os.Getenv("PG_INSTALL_DIR")); installDir != "" {
			candidate = filepath.Join(installDir, "bin")
		}
	}
	if candidate == "" {
		pgConfig, err := exec.LookPath("pg_config")
		if err != nil {
			return "", fmt.Errorf("native benchmark requires --native-bindir, PGWORKBENCH_NATIVE_BINDIR, PG_INSTALL_DIR/bin, or pg_config on PATH")
		}
		candidate = filepath.Dir(pgConfig)
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve native PostgreSQL bindir: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve native PostgreSQL bindir: %w", err)
	}
	if !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		return "", fmt.Errorf("native PostgreSQL bindir must resolve to a clean absolute path")
	}
	return canonical, nil
}

func parseBenchmarkRunArgs(args []string) (map[string]string, []string, error) {
	options := make(map[string]string)
	var inputs []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			options["json"] = "1"
		case "--runtime", "--native-bindir", "--run-id", "--subject":
			if index+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a value", args[index])
			}
			options[strings.TrimPrefix(args[index], "--")] = args[index+1]
			index++
		case "--":
			inputs = append(inputs, args[index+1:]...)
			return options, inputs, nil
		default:
			if strings.HasPrefix(args[index], "-") {
				return nil, nil, fmt.Errorf("unknown option: %s", args[index])
			}
			inputs = append(inputs, args[index])
		}
	}
	if runtimeName := options["runtime"]; runtimeName != "" && runtimeName != "docker" && runtimeName != "native" {
		return nil, nil, fmt.Errorf("unsupported runtime %q: expected docker or native", runtimeName)
	}
	if bindir := options["native-bindir"]; bindir != "" && (!filepath.IsAbs(bindir) || filepath.Clean(bindir) != bindir) {
		return nil, nil, fmt.Errorf("--native-bindir must be a clean absolute path")
	}
	return options, inputs, nil
}

func parseBenchmarkVerifyArgs(args []string) (bool, bool, []string, error) {
	jsonOutput := false
	requireBundle := false
	var inputs []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--bundle":
			requireBundle = true
		case "--":
			inputs = append(inputs, args[index+1:]...)
			return jsonOutput, requireBundle, inputs, nil
		default:
			if strings.HasPrefix(args[index], "-") {
				return false, false, nil, fmt.Errorf("unknown option: %s", args[index])
			}
			inputs = append(inputs, args[index])
		}
	}
	return jsonOutput, requireBundle, inputs, nil
}

func parseBenchmarkHistoryCreateArgs(args []string) (bool, string, []string, error) {
	jsonOutput := false
	historyID := ""
	var inputs []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--history-id":
			if index+1 >= len(args) {
				return false, "", nil, fmt.Errorf("--history-id requires a value")
			}
			if historyID != "" {
				return false, "", nil, fmt.Errorf("duplicate option: --history-id")
			}
			historyID = args[index+1]
			if !benchmarkrun.ValidRunID(historyID) {
				return false, "", nil, fmt.Errorf("invalid benchmark history id %q", historyID)
			}
			index++
		case "--":
			inputs = append(inputs, args[index+1:]...)
			return jsonOutput, historyID, inputs, nil
		default:
			if strings.HasPrefix(args[index], "-") {
				return false, "", nil, fmt.Errorf("unknown option: %s", args[index])
			}
			inputs = append(inputs, args[index])
		}
	}
	return jsonOutput, historyID, inputs, nil
}

func parseBenchmarkCompareArgs(args []string) (benchmarkcompare.Options, bool, []string, error) {
	options := benchmarkcompare.Options{}
	jsonOutput := false
	var inputs []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--bootstrap-resamples":
			if index+1 >= len(args) {
				return options, false, nil, fmt.Errorf("--bootstrap-resamples requires a value")
			}
			value, err := strconv.Atoi(args[index+1])
			if err != nil || value < 1000 || value > 1000000 {
				return options, false, nil, fmt.Errorf("--bootstrap-resamples must be between 1000 and 1000000")
			}
			options.BootstrapResamples = value
			index++
		case "--confidence":
			if index+1 >= len(args) {
				return options, false, nil, fmt.Errorf("--confidence requires a value")
			}
			value, err := strconv.ParseFloat(args[index+1], 64)
			if err != nil || value <= 0.5 || value >= 1 {
				return options, false, nil, fmt.Errorf("--confidence must be greater than 0.5 and less than 1")
			}
			options.ConfidenceLevel = value
			index++
		case "--seed":
			if index+1 >= len(args) {
				return options, false, nil, fmt.Errorf("--seed requires a value")
			}
			value, err := strconv.ParseUint(args[index+1], 10, 64)
			if err != nil {
				return options, false, nil, fmt.Errorf("--seed must be a uint64")
			}
			options.Seed = value
			index++
		case "--":
			inputs = append(inputs, args[index+1:]...)
			return options, jsonOutput, inputs, nil
		default:
			if strings.HasPrefix(args[index], "-") {
				return options, false, nil, fmt.Errorf("unknown option: %s", args[index])
			}
			inputs = append(inputs, args[index])
		}
	}
	return options, jsonOutput, inputs, nil
}

func oneOfString(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func runMatrix(root string, catalog speccatalog.Catalog, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("matrix action is required")
	}

	switch args[0] {
	case "verify-candidate":
		options, jsonOutput, input, err := parseMatrixCandidateVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		options.VerifierVersion = version
		options.VerifierCommit = commit
		result, err := matrixartifact.VerifyCandidate(root, input, options)
		if err != nil {
			return err
		}
		if jsonOutput {
			err = matrixartifact.RenderJSON(os.Stdout, result)
		} else {
			err = matrixartifact.Render(os.Stdout, result)
		}
		if err != nil {
			return err
		}
		if !result.Valid() {
			return fmt.Errorf("matrix candidate verification failed")
		}
		return nil
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

func parseMatrixCandidateVerifyArgs(args []string) (matrixartifact.Options, bool, string, error) {
	var options matrixartifact.Options
	jsonOutput := false
	var inputs []string
	seen := make(map[string]bool)

	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			inputs = append(inputs, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") {
			inputs = append(inputs, argument)
			continue
		}
		if seen[argument] {
			return options, false, "", fmt.Errorf("duplicate option: %s", argument)
		}
		seen[argument] = true
		if argument == "--json" {
			jsonOutput = true
			continue
		}
		if !oneOfString(argument, "--version", "--commit", "--expected-runs") {
			return options, false, "", fmt.Errorf("unknown option: %s", argument)
		}
		if index+1 >= len(args) {
			return options, false, "", fmt.Errorf("%s requires a value", argument)
		}
		value := args[index+1]
		index++
		switch argument {
		case "--version":
			options.ExpectedVersion = value
		case "--commit":
			options.ExpectedCommit = value
		case "--expected-runs":
			count, err := strconv.Atoi(value)
			if err != nil || count < 1 || strconv.Itoa(count) != value {
				return options, false, "", fmt.Errorf("--expected-runs must be a canonical positive integer")
			}
			options.ExpectedRuns = count
		}
	}

	if len(inputs) != 1 || options.ExpectedVersion == "" || options.ExpectedCommit == "" || options.ExpectedRuns == 0 {
		return options, false, "", fmt.Errorf("usage: pgworkbench matrix verify-candidate [--json] --version version --commit full-commit --expected-runs count <matrix-dir>")
	}
	return options, jsonOutput, inputs[0], nil
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
		jsonOutput, requireBundleInventory, inputs, err := parseRunVerifyArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("usage: pgworkbench run verify [--json] [--bundle] <run-dir-or-id>")
		}
		result, err := runverify.VerifyWithOptions(root, inputs[0], runverify.Options{
			RequireBundleInventory: requireBundleInventory,
		})
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

func parseRunVerifyArgs(args []string) (bool, bool, []string, error) {
	jsonOutput := false
	requireBundleInventory := false
	var inputs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--bundle":
			requireBundleInventory = true
		case "--":
			inputs = append(inputs, args[i+1:]...)
			return jsonOutput, requireBundleInventory, inputs, nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return false, false, nil, fmt.Errorf("unknown option: %s", args[i])
			}
			inputs = append(inputs, args[i])
		}
	}
	return jsonOutput, requireBundleInventory, inputs, nil
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
			return fmt.Errorf("usage: pgworkbench spec list <workload|experiment|benchmark|matrix|topology|dataset|utility-test|utility-suite>")
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
			return fmt.Errorf("usage: pgworkbench spec reference [workload|experiment|benchmark|matrix|topology|dataset|utility-test|utility-suite|all]")
		}
		if len(args) == 2 {
			kind = args[1]
		}
		return speccatalog.RenderReference(os.Stdout, kind)
	case "schema":
		kind := "all"
		if len(args) > 2 {
			return fmt.Errorf("usage: pgworkbench spec schema [workload|experiment|benchmark|matrix|topology|dataset|utility-test|utility-suite|all]")
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
		outPath := ""
		if len(args) == 3 {
			outPath = args[2]
		}
		runDir, err := runartifact.ResolveRunDir(root, args[1])
		if err != nil {
			return err
		}
		return renderMaybeFile(root, outPath, "report", func(w *os.File) error {
			return runreport.RenderRun(root, args[1], w)
		}, runDir)
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
		protected, err := resolveReportInputRoots(root, inputs)
		if err != nil {
			return err
		}
		return renderMaybeFile(root, outPath, "summary", func(w *os.File) error {
			return runreport.RenderSummary(root, inputs, w)
		}, protected...)
	case "history":
		outPath, inputs, err := parseOutputArgs(args[1:])
		if err != nil {
			return err
		}
		if len(inputs) == 0 {
			return fmt.Errorf("usage: pgworkbench report history [--output output.md] <series-dir|run-dir> [series-dir|run-dir...]")
		}
		protected, err := resolveReportInputRoots(root, inputs)
		if err != nil {
			return err
		}
		return renderMaybeFile(root, outPath, "run history comparison", func(w *os.File) error {
			return runreport.RenderHistory(root, inputs, w)
		}, protected...)
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

func resolveReportInputRoots(root string, inputs []string) ([]string, error) {
	seen := make(map[string]struct{})
	protected := make([]string, 0, len(inputs)*2)
	add := func(path string) {
		if _, exists := seen[path]; !exists {
			seen[path] = struct{}{}
			protected = append(protected, path)
		}
	}
	for _, input := range inputs {
		dir, err := runartifact.ResolveDir(root, input)
		if err != nil {
			return nil, err
		}
		add(dir)
	}
	runDirs, err := runartifact.CollectRunDirs(root, inputs)
	if err != nil {
		return nil, err
	}
	for _, runDir := range runDirs {
		add(runDir)
	}
	return protected, nil
}

func renderMaybeFile(root string, outPath string, label string, render func(*os.File) error, protectedRoots ...string) error {
	if outPath == "" {
		return render(os.Stdout)
	}
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(root, outPath)
	}
	var err error
	for _, protected := range protectedRoots {
		outPath, err = pathguard.ResolveOutputOutside(protected, outPath)
		if err != nil {
			return fmt.Errorf("resolve report output outside immutable input %s: %w", protected, err)
		}
	}
	canonical, err := pathguard.PrepareNewOutput(outPath, 0o755)
	if err != nil {
		return err
	}
	for _, protected := range protectedRoots {
		if _, err := pathguard.ResolveOutputOutside(protected, canonical); err != nil {
			return fmt.Errorf("recheck report output outside immutable input %s: %w", protected, err)
		}
	}
	file, err := os.CreateTemp(filepath.Dir(canonical), ".pgworkbench-report-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := render(file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := pathguard.PublishFileExclusive(temporary, canonical); err != nil {
		return err
	}
	fmt.Printf("Wrote %s: %s\n", label, canonical)
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
	if configured := strings.TrimSpace(os.Getenv("PGWORKBENCH_ROOT")); configured != "" {
		root, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		if isWorkspaceRoot(root) {
			return root, nil
		}
		return "", fmt.Errorf("PGWORKBENCH_ROOT is not a scenario-pack or repository root: %s", root)
	}

	var starts []string
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
		starts = append(starts, filepath.Dir(executable))
	}

	seen := make(map[string]struct{})
	for _, start := range starts {
		dir, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		for {
			if _, ok := seen[dir]; !ok {
				seen[dir] = struct{}{}
				if isWorkspaceRoot(dir) {
					return dir, nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("could not find a pgworkbench scenario pack; set PGWORKBENCH_ROOT")
}

func isWorkspaceRoot(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, scenariopack.ManifestName)); err == nil && info.Mode().IsRegular() {
		return true
	}
	profiles, profilesErr := os.Stat(filepath.Join(dir, "profiles"))
	makefile, makefileErr := os.Stat(filepath.Join(dir, "Makefile"))
	return profilesErr == nil && profiles.IsDir() && makefileErr == nil && makefile.Mode().IsRegular()
}
