package pgsourcecheck

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/r314tive/postgres-experiment-workbench/internal/failurescan"
)

type Summary struct {
	InputPath       string
	ResolvedPath    string
	ArtifactDir     string
	FilesSeen       int
	LogFiles        []string
	DiffFiles       []string
	CoreFiles       []string
	FailureEvidence []Evidence
	Found           bool
}

type Evidence struct {
	Label   string
	Matches int
}

func Classify(root string, inputPath string) (Summary, error) {
	if inputPath == "" {
		return Summary{}, fmt.Errorf("source check path is required")
	}

	resolved, err := resolveExistingPath(root, inputPath)
	if err != nil {
		return Summary{}, err
	}
	artifactDir := resolveArtifactDir(resolved)
	files, err := collectFiles(artifactDir)
	if err != nil {
		return Summary{}, err
	}
	logFiles, diffFiles, coreFiles := classifyFiles(files)

	scanResult, err := failurescan.Scan(root, failurescan.Options{
		Paths:        []string{artifactDir},
		ContextLines: 0,
	})
	if err != nil {
		return Summary{}, err
	}

	return Summary{
		InputPath:       inputPath,
		ResolvedPath:    resolved,
		ArtifactDir:     artifactDir,
		FilesSeen:       len(files),
		LogFiles:        logFiles,
		DiffFiles:       diffFiles,
		CoreFiles:       coreFiles,
		FailureEvidence: summarizeEvidence(scanResult),
		Found:           scanResult.Found,
	}, nil
}

func Render(w io.Writer, summary Summary) error {
	if _, err := fmt.Fprintln(w, "== PostgreSQL source check artifacts =="); err != nil {
		return err
	}
	lines := []string{
		fmt.Sprintf("input_path=%s", summary.InputPath),
		fmt.Sprintf("resolved_path=%s", summary.ResolvedPath),
		fmt.Sprintf("artifact_dir=%s", summary.ArtifactDir),
		fmt.Sprintf("files_seen=%d", summary.FilesSeen),
		fmt.Sprintf("log_files=%d", len(summary.LogFiles)),
		fmt.Sprintf("diff_files=%d", len(summary.DiffFiles)),
		fmt.Sprintf("core_files=%d", len(summary.CoreFiles)),
	}
	if _, err := fmt.Fprintln(w, strings.Join(lines, "\n")); err != nil {
		return err
	}

	if err := renderPathSection(w, "diff files", summary.DiffFiles); err != nil {
		return err
	}
	if err := renderPathSection(w, "core files", summary.CoreFiles); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "\n== failure evidence =="); err != nil {
		return err
	}
	for _, evidence := range summary.FailureEvidence {
		if _, err := fmt.Fprintf(w, "%s=%d\n", sanitizeLabel(evidence.Label), evidence.Matches); err != nil {
			return err
		}
	}
	if summary.Found {
		_, err := fmt.Fprintln(w, "result=failure-evidence-found")
		return err
	}
	_, err := fmt.Fprintln(w, "result=clean")
	return err
}

func resolveExistingPath(root string, inputPath string) (string, error) {
	candidates := []string{inputPath}
	if !filepath.IsAbs(inputPath) {
		candidates = append(candidates, filepath.Join(root, inputPath))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				return candidate, nil
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("source check path not found: %s", inputPath)
}

func resolveArtifactDir(path string) string {
	if filepath.Base(path) == "artifacts" {
		return path
	}
	artifactDir := filepath.Join(path, "artifacts")
	if info, err := os.Stat(artifactDir); err == nil && info.IsDir() {
		return artifactDir
	}
	return path
}

func collectFiles(root string) ([]string, error) {
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func classifyFiles(files []string) ([]string, []string, []string) {
	var logFiles []string
	var diffFiles []string
	var coreFiles []string
	for _, path := range files {
		base := filepath.Base(path)
		if base == "core" || strings.HasPrefix(base, "core.") {
			coreFiles = append(coreFiles, path)
		}
		if strings.HasSuffix(base, ".diffs") {
			diffFiles = append(diffFiles, path)
		}
		if isLogFile(path) {
			logFiles = append(logFiles, path)
		}
	}
	return logFiles, diffFiles, coreFiles
}

func isLogFile(path string) bool {
	base := filepath.Base(path)
	if base == "postmaster.log" || base == "regression.out" {
		return true
	}
	switch filepath.Ext(base) {
	case ".log", ".out", ".diffs":
		return true
	default:
		slashPath := filepath.ToSlash(path)
		return strings.Contains(slashPath, "/tmp_check/") &&
			strings.Contains(slashPath, "/log/") &&
			strings.HasSuffix(base, ".log")
	}
}

func summarizeEvidence(result failurescan.Result) []Evidence {
	evidence := []Evidence{{Label: "core files", Matches: len(result.CoreFiles)}}
	for _, section := range result.Sections {
		evidence = append(evidence, Evidence{
			Label:   section.Label,
			Matches: len(section.Matches),
		})
	}
	return evidence
}

func renderPathSection(w io.Writer, label string, paths []string) error {
	if _, err := fmt.Fprintf(w, "\n== %s ==\n", label); err != nil {
		return err
	}
	if len(paths) == 0 {
		_, err := fmt.Fprintln(w, "clean")
		return err
	}
	for index, path := range paths {
		if index >= 40 {
			_, err := fmt.Fprintf(w, "... %d more\n", len(paths)-index)
			return err
		}
		if _, err := fmt.Fprintln(w, path); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeLabel(label string) string {
	label = strings.ToLower(label)
	var out strings.Builder
	lastUnderscore := false
	for _, ch := range label {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			out.WriteRune(ch)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			out.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(out.String(), "_")
}
