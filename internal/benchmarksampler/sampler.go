// Package benchmarksampler owns the PostgreSQL sampler-v2 execution loop.
// It keeps command selection and output locations fixed to the scenario pack,
// records query durations with Go's monotonic clock, and never accepts an
// arbitrary executable or output path from the caller.
package benchmarksampler

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MetricsRelativePath = "metrics.csv"
	TimingRelativePath  = "artifacts/benchmark/controls/collector-overhead.tsv"

	maxDuration       = 24 * time.Hour
	maxInterval       = time.Hour
	maxSamples        = 10_000
	maxSampleBytes    = 1 << 20
	defaultSampleWait = 30 * time.Second
)

const metricsHeader = "sampled_at,database_name,active_sessions,waiting_sessions,lock_waiting_sessions,blocked_sessions,locks_total,locks_waiting,xact_commit,xact_rollback,blks_read,blks_hit,tup_returned,tup_fetched,tup_inserted,tup_updated,tup_deleted,conflicts,deadlocks,temp_files,temp_bytes,wal_records,wal_fpi,wal_bytes,current_wal_lsn"

// Keep this header private until benchmarkcontrol freezes its strict raw-source
// parser. The columns already retain both wall-clock containment and the
// monotonic duration used for duty-cycle derivation.
const timingHeader = "sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus"

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

type Options struct {
	Root          string
	RunDir        string
	ExpectedRunID string
	Interval      time.Duration
	Duration      time.Duration
	Samples       int
	RecordTiming  bool
	Context       context.Context
	Now           func() time.Time
	RunSample     func(context.Context) ([]byte, error)
}

type Result struct {
	MetricsPath string
	TimingPath  string
	Samples     int
}

// Run samples immediately and then on a monotonic interval. Duration mode is
// stopped by either its bound or Context cancellation; cancellation records
// one final boundary sample before returning successfully.
func Run(options Options) (result Result, err error) {
	options, err = withDefaults(options)
	if err != nil {
		return Result{}, err
	}
	metricsPath, timingPath, err := validateOwnedPaths(options.Root, options.RunDir, options.ExpectedRunID)
	if err != nil {
		return Result{}, err
	}
	metrics, err := createEvidenceFile(metricsPath)
	if err != nil {
		return Result{}, fmt.Errorf("create sampler metrics evidence: %w", err)
	}
	defer func() { err = errors.Join(err, metrics.Close()) }()
	var timing *os.File
	if options.RecordTiming {
		timing, err = createEvidenceFile(timingPath)
		if err != nil {
			return Result{}, fmt.Errorf("create sampler timing evidence: %w", err)
		}
		defer func() { err = errors.Join(err, timing.Close()) }()
	}
	if _, err := io.WriteString(metrics, metricsHeader+"\n"); err != nil {
		return Result{}, err
	}
	if timing != nil {
		if _, err := io.WriteString(timing, timingHeader+"\n"); err != nil {
			return Result{}, err
		}
	}

	result = Result{MetricsPath: metricsPath}
	if timing != nil {
		result.TimingPath = timingPath
	}
	loopStarted := time.Now()
	firstWall := options.Now().UTC()
	lastScheduled := time.Time{}
	for sequence := 1; ; sequence++ {
		scheduled := firstWall.Add(time.Duration(sequence-1) * options.Interval)
		if sequence > 1 {
			deadline := loopStarted.Add(time.Duration(sequence-1) * options.Interval)
			timer := time.NewTimer(time.Until(deadline))
			select {
			case <-timer.C:
			case <-options.Context.Done():
				if !timer.Stop() {
					<-timer.C
				}
				scheduled = nextWallTimestamp(options.Now().UTC(), lastScheduled)
				if err := recordSample(context.Background(), options, metrics, nil, sequence, scheduled); err != nil {
					return result, fmt.Errorf("record final sampler boundary: %w", err)
				}
				lastScheduled = scheduled
				result.Samples++
				return result, nil
			}
		}

		// A termination request must not interrupt the SQL process halfway and
		// leave an orphan. Let the currently bounded sample finish, then observe
		// cancellation below and record the final boundary sample.
		if err := recordSample(context.WithoutCancel(options.Context), options, metrics, timing, sequence, scheduled); err != nil {
			return result, err
		}
		lastScheduled = scheduled
		result.Samples++
		if options.Samples > 0 && result.Samples >= options.Samples {
			return result, nil
		}
		if options.Samples == 0 && time.Since(loopStarted) >= options.Duration {
			return result, nil
		}
		select {
		case <-options.Context.Done():
			scheduled = nextWallTimestamp(options.Now().UTC(), lastScheduled)
			if err := recordSample(context.Background(), options, metrics, nil, sequence+1, scheduled); err != nil {
				return result, fmt.Errorf("record final sampler boundary: %w", err)
			}
			result.Samples++
			return result, nil
		default:
		}
	}
}

func recordSample(parent context.Context, options Options, metrics, timing *os.File, sequence int, scheduled time.Time) error {
	ctx, cancel := context.WithTimeout(parent, defaultSampleWait)
	defer cancel()
	startedWall := options.Now().UTC()
	startedMono := time.Now()
	content, sampleErr := options.RunSample(ctx)
	duration := time.Since(startedMono)
	finishedWall := options.Now().UTC()
	status := "succeeded"
	if sampleErr != nil {
		status = "failed"
	}
	if timing != nil {
		if _, err := fmt.Fprintf(timing, "%d\t%s\t%s\t%s\t%d\t%s\n",
			sequence, canonicalTime(scheduled), canonicalTime(startedWall), canonicalTime(finishedWall), duration.Nanoseconds(), status); err != nil {
			return fmt.Errorf("write sampler timing evidence: %w", err)
		}
	}
	if sampleErr != nil {
		return fmt.Errorf("PostgreSQL sampler query %d: %w", sequence, sampleErr)
	}
	row, err := validateSampleRow(content)
	if err != nil {
		return fmt.Errorf("PostgreSQL sampler query %d output: %w", sequence, err)
	}
	if _, err := metrics.Write(row); err != nil {
		return fmt.Errorf("write sampler metrics evidence: %w", err)
	}
	if _, err := metrics.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("write sampler metrics newline: %w", err)
	}
	return nil
}

func withDefaults(options Options) (Options, error) {
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Interval <= 0 || options.Interval > maxInterval {
		return Options{}, fmt.Errorf("sampler interval must be between 1ns and %s", maxInterval)
	}
	if options.Samples < 0 || options.Samples > maxSamples {
		return Options{}, fmt.Errorf("sampler samples must be between 0 and %d", maxSamples)
	}
	if options.Samples == 0 && (options.Duration < 0 || options.Duration > maxDuration) {
		return Options{}, fmt.Errorf("sampler duration must be between 0 and %s", maxDuration)
	}
	if options.Samples > 0 && options.Duration != 0 {
		return Options{}, fmt.Errorf("sampler duration and fixed sample count are mutually exclusive")
	}
	if options.Samples == 0 {
		estimatedSamples := int64(1)
		if options.Duration > 0 {
			estimatedSamples = int64(options.Duration/options.Interval) + 2
		}
		if estimatedSamples > maxSamples {
			return Options{}, fmt.Errorf("sampler duration/interval can produce more than %d bounded timing rows", maxSamples)
		}
	}
	if options.RunSample == nil {
		root := options.Root
		options.RunSample = func(ctx context.Context) ([]byte, error) { return runOwnedSample(ctx, root) }
	}
	return options, nil
}

func validateOwnedPaths(root, runDir, expectedRunID string) (string, string, error) {
	if root == "" || runDir == "" || !filepath.IsAbs(root) || !filepath.IsAbs(runDir) {
		return "", "", fmt.Errorf("sampler root and linked run directory must be absolute")
	}
	if !runIDPattern.MatchString(expectedRunID) {
		return "", "", fmt.Errorf("sampler expected linked run id is missing or invalid")
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", "", fmt.Errorf("resolve scenario-pack root: %w", err)
	}
	if canonicalRoot != filepath.Clean(root) {
		return "", "", fmt.Errorf("sampler scenario-pack root must be canonical")
	}
	runsRoot := filepath.Join(canonicalRoot, "runs")
	relative, err := filepath.Rel(runsRoot, filepath.Clean(runDir))
	if err != nil || relative == "." || filepath.Dir(relative) != "." || !runIDPattern.MatchString(relative) {
		return "", "", fmt.Errorf("sampler linked run must be one canonical child of %s", runsRoot)
	}
	if relative != expectedRunID {
		return "", "", fmt.Errorf("sampler linked run %q does not match bound run id %q", relative, expectedRunID)
	}
	wantRunDir := filepath.Join(runsRoot, relative)
	for _, directory := range []string{
		canonicalRoot,
		runsRoot,
		wantRunDir,
		filepath.Join(wantRunDir, "artifacts"),
		filepath.Join(wantRunDir, "artifacts", "benchmark"),
		filepath.Join(wantRunDir, "artifacts", "benchmark", "controls"),
	} {
		info, statErr := os.Lstat(directory)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("sampler requires a pre-created non-symlink directory %s", directory)
		}
	}
	for _, source := range []string{filepath.Join(canonicalRoot, "scripts", "psql.sh"), filepath.Join(canonicalRoot, "sql", "metrics_sample.sql")} {
		info, statErr := os.Lstat(source)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("sampler owned source must be a regular non-symlink file: %s", source)
		}
	}
	return filepath.Join(wantRunDir, MetricsRelativePath), filepath.Join(wantRunDir, filepath.FromSlash(TimingRelativePath)), nil
}

func createEvidenceFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("refusing to overwrite sampler evidence %s (%s)", path, info.Mode())
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

func runOwnedSample(ctx context.Context, root string) ([]byte, error) {
	command := filepath.Join(root, "scripts", "psql.sh")
	query := filepath.Join(root, "sql", "metrics_sample.sql")
	cmd := exec.Command(command, "-q", "-f", query)
	if err := configureProcessGroup(cmd); err != nil {
		return nil, err
	}
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, boundedMessage(stderr.String()))
		}
		return stdout.Bytes(), nil
	case <-ctx.Done():
		if err := terminateProcessGroup(cmd, wait, 2*time.Second); err != nil {
			return nil, errors.Join(ctx.Err(), err)
		}
		return nil, ctx.Err()
	}
}

func validateSampleRow(content []byte) ([]byte, error) {
	if len(content) == 0 || len(content) > maxSampleBytes {
		return nil, fmt.Errorf("sample row must be between 1 and %d bytes", maxSampleBytes)
	}
	trimmed := bytes.TrimSuffix(content, []byte{'\n'})
	trimmed = bytes.TrimSuffix(trimmed, []byte{'\r'})
	if len(trimmed) == 0 || bytes.ContainsAny(trimmed, "\r\n") {
		return nil, fmt.Errorf("sample command must return exactly one CSV row")
	}
	if bytes.Count(trimmed, []byte{','}) != strings.Count(metricsHeader, ",") {
		return nil, fmt.Errorf("sample CSV row has an unexpected field count")
	}
	return append([]byte(nil), trimmed...), nil
}

func canonicalTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func nextWallTimestamp(candidate, previous time.Time) time.Time {
	candidate = candidate.UTC()
	if !candidate.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return candidate
}

func boundedMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 4096 {
		value = value[:4096]
	}
	return strconv.QuoteToASCII(value)
}

// ParseTimingSource is intentionally small and strict so tests can assert the
// raw source remains bounded while benchmarkcontrol owns semantic derivation.
func ParseTimingSource(reader io.Reader) (int, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 2<<20))
	if !scanner.Scan() || scanner.Text() != timingHeader {
		return 0, fmt.Errorf("collector timing header mismatch")
	}
	count := 0
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 6 {
			return 0, fmt.Errorf("collector timing row %d has %d fields", count+2, len(fields))
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return count, nil
}
