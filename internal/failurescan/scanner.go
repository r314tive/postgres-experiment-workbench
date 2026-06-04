package failurescan

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var ErrEvidenceFound = errors.New("failure evidence found")

type Options struct {
	Paths        []string
	ContextLines int
}

type Result struct {
	RequestedPaths []string
	FilesSeen      int
	NoPaths        bool
	CoreFiles      []string
	Sections       []Section
	Found          bool
}

type Section struct {
	Label   string
	Matches []FileMatch
}

type FileMatch struct {
	Path  string
	Lines []LineMatch
}

type LineMatch struct {
	Number int
	Text   string
}

type patternSpec struct {
	label string
	re    *regexp.Regexp
	diffs bool
}

var patternSpecs = []patternSpec{
	{
		label: "crash and assertion patterns",
		re: regexp.MustCompile(
			`TRAP:|PANIC:|server process .* was terminated by signal|terminating any other active server processes|segmentation fault|segfault|SIGSEGV|SIGBUS|SIGABRT|SIGILL|core dumped`,
		),
	},
	{
		label: "regression diff error patterns",
		re:    regexp.MustCompile(`^\+ERROR|unrecognized node type|server closed the connection unexpectedly|could not find pathkey item to sort`),
		diffs: true,
	},
	{
		label: "sanitizer patterns",
		re:    regexp.MustCompile(`AddressSanitizer|UndefinedBehaviorSanitizer|LeakSanitizer|ThreadSanitizer|runtime error:`),
	},
	{
		label: "valgrind patterns",
		re:    regexp.MustCompile(`ERROR SUMMARY: [1-9][0-9]* errors|Invalid read|Invalid write|Use of uninitialised|Conditional jump or move depends on uninitialised`),
	},
}

func Scan(root string, options Options) (Result, error) {
	requested := options.Paths
	if len(requested) == 0 {
		requested = []string{"logs", "generated"}
	}
	if options.ContextLines < 0 {
		options.ContextLines = 0
	}

	result := Result{RequestedPaths: append([]string(nil), requested...)}
	existing := resolveExistingPaths(root, requested)
	if len(existing) == 0 {
		result.NoPaths = true
		return result, nil
	}

	allFiles, err := collectFiles(existing)
	if err != nil {
		return Result{}, err
	}
	result.FilesSeen = len(allFiles)

	logFiles, diffFiles, coreFiles := classifyFiles(allFiles)
	result.CoreFiles = coreFiles
	if len(coreFiles) > 0 {
		result.Found = true
	}

	for _, spec := range patternSpecs {
		files := logFiles
		if spec.diffs {
			files = diffFiles
		}
		matches, err := findMatches(files, spec.re, options.ContextLines)
		if err != nil {
			return Result{}, err
		}
		if len(matches) > 0 {
			result.Found = true
		}
		result.Sections = append(result.Sections, Section{
			Label:   spec.label,
			Matches: matches,
		})
	}

	return result, nil
}

func Render(w io.Writer, result Result) error {
	if result.NoPaths {
		_, err := fmt.Fprintf(w, "No scan paths exist: %s\nresult=clean\n", strings.Join(result.RequestedPaths, " "))
		return err
	}

	if err := renderSectionHeader(w, "core files"); err != nil {
		return err
	}
	if len(result.CoreFiles) == 0 {
		if _, err := fmt.Fprintln(w, "clean"); err != nil {
			return err
		}
	} else {
		for _, path := range result.CoreFiles {
			if _, err := fmt.Fprintln(w, path); err != nil {
				return err
			}
		}
	}

	for _, section := range result.Sections {
		if err := renderSectionHeader(w, section.Label); err != nil {
			return err
		}
		if len(section.Matches) == 0 {
			if _, err := fmt.Fprintln(w, "clean"); err != nil {
				return err
			}
			continue
		}
		for _, match := range section.Matches {
			if _, err := fmt.Fprintf(w, "-- %s --\n", match.Path); err != nil {
				return err
			}
			for i, line := range match.Lines {
				if i >= 120 {
					break
				}
				if _, err := fmt.Fprintf(w, "%d:%s\n", line.Number, line.Text); err != nil {
					return err
				}
			}
		}
	}

	if err := renderSectionHeader(w, "summary"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "paths=%s\nfiles_seen=%d\n", strings.Join(result.RequestedPaths, " "), result.FilesSeen); err != nil {
		return err
	}
	if result.Found {
		_, err := fmt.Fprintln(w, "result=failure-evidence-found")
		return err
	}
	_, err := fmt.Fprintln(w, "result=clean")
	return err
}

func renderSectionHeader(w io.Writer, label string) error {
	_, err := fmt.Fprintf(w, "\n== %s ==\n", label)
	return err
}

func resolveExistingPaths(root string, requested []string) []string {
	var existing []string
	seen := make(map[string]struct{})
	for _, path := range requested {
		if path == "" {
			continue
		}
		var resolved string
		if filepath.IsAbs(path) {
			if _, err := os.Stat(path); err != nil {
				continue
			}
			resolved = path
		} else if candidate := filepath.Join(root, path); exists(candidate) {
			resolved = candidate
		} else if exists(path) {
			resolved = path
		} else {
			continue
		}
		abs, err := filepath.Abs(resolved)
		if err != nil {
			abs = resolved
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		existing = append(existing, resolved)
	}
	return existing
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func collectFiles(paths []string) ([]string, error) {
	var files []string
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type().IsRegular() {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
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
		return strings.Contains(filepath.ToSlash(path), "/tmp_check/") &&
			strings.Contains(filepath.ToSlash(path), "/log/") &&
			strings.HasSuffix(base, ".log")
	}
}

func findMatches(files []string, re *regexp.Regexp, contextLines int) ([]FileMatch, error) {
	var matches []FileMatch
	for _, path := range files {
		lines, err := readTextLines(path)
		if err != nil {
			return nil, err
		}
		if len(lines) == 0 {
			continue
		}

		selected := make(map[int]struct{})
		for index, line := range lines {
			if re.MatchString(line) {
				start := index - contextLines
				if start < 0 {
					start = 0
				}
				end := index + contextLines
				if end >= len(lines) {
					end = len(lines) - 1
				}
				for i := start; i <= end; i++ {
					selected[i] = struct{}{}
				}
			}
		}
		if len(selected) == 0 {
			continue
		}

		indexes := make([]int, 0, len(selected))
		for index := range selected {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)

		fileMatch := FileMatch{Path: path}
		for _, index := range indexes {
			fileMatch.Lines = append(fileMatch.Lines, LineMatch{
				Number: index + 1,
				Text:   lines[index],
			})
		}
		matches = append(matches, fileMatch)
	}
	return matches, nil
}

func readTextLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.ContainsRune(line, '\x00') {
			return nil, nil
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}
