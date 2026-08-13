package pgbenchresult

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestSummarizeIndependentTrials(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	stats, err := Summarize(values)
	if err != nil {
		t.Fatal(err)
	}

	if stats.SchemaVersion != StatsSchemaVersion || stats.StatsVersion != StatsVersion || stats.N != 5 {
		t.Fatalf("unexpected contract identity: %#v", stats)
	}
	assertClose(t, stats.Mean, 3)
	assertClose(t, stats.Median, 3)
	assertClose(t, stats.SampleStddev, math.Sqrt(2.5))
	assertClose(t, stats.MAD, 1)
	assertClose(t, stats.Min, 1)
	assertClose(t, stats.Max, 5)
	if stats.CVPct == nil || stats.RobustCVPct == nil {
		t.Fatalf("expected defined CV values: %#v", stats)
	}
	assertClose(t, *stats.CVPct, math.Sqrt(2.5)/3*100)
	assertClose(t, *stats.RobustCVPct, 1.4826/3*100)

	// Summarization and percentile calculation must not reorder caller data.
	for index, want := range []float64{1, 2, 3, 4, 5} {
		if values[index] != want {
			t.Fatalf("input mutated at %d: got %v want %v", index, values[index], want)
		}
	}
}

func TestPercentileType7(t *testing.T) {
	values := []float64{30, 0, 20, 10}
	tests := []struct {
		probability float64
		want        float64
	}{
		{0, 0},
		{0.25, 7.5},
		{0.5, 15},
		{0.95, 28.5},
		{1, 30},
	}
	for _, test := range tests {
		got, err := PercentileType7(values, test.probability)
		if err != nil {
			t.Fatal(err)
		}
		assertClose(t, got, test.want)
	}
}

func TestCVFieldsAreNullWhenCenterIsZero(t *testing.T) {
	stats, err := Summarize([]float64{-1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if stats.CVPct != nil || stats.RobustCVPct != nil {
		t.Fatalf("zero-denominator CVs must be null: %#v", stats)
	}
	encoded, err := stats.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"cv_pct": null`, `"robust_cv_pct": null`} {
		if !bytes.Contains(encoded, []byte(want)) {
			t.Fatalf("JSON missing %q:\n%s", want, encoded)
		}
	}
}

func TestFlagRobustZDoesNotDeleteTrials(t *testing.T) {
	values := []float64{10, 11, 10, 12, 100}
	flags, err := FlagRobustZ(values, 3.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 1 || flags[0].Index != 4 || flags[0].Value != 100 || flags[0].RobustZ == nil {
		t.Fatalf("unexpected flags: %#v", flags)
	}
	if flags[0].Reason != "robust_z_exceeds_threshold" || math.Abs(*flags[0].RobustZ) <= 3.5 {
		t.Fatalf("unexpected robust-z flag: %#v", flags[0])
	}
	stats, err := Summarize(values)
	if err != nil {
		t.Fatal(err)
	}
	if stats.N != len(values) || stats.Max != 100 {
		t.Fatalf("outlier was removed from statistics: %#v", stats)
	}
}

func TestFlagRobustZHandlesZeroMADWithoutInfinity(t *testing.T) {
	flags, err := FlagRobustZ([]float64{1, 1, 1, 100}, 3.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 1 || flags[0].Index != 3 || flags[0].RobustZ != nil || flags[0].Reason != "mad_zero_nonmedian" {
		t.Fatalf("unexpected zero-MAD flags: %#v", flags)
	}
}

func TestStatisticsRejectInvalidInputs(t *testing.T) {
	if _, err := Summarize([]float64{1}); err == nil || !strings.Contains(err.Error(), "at least two") {
		t.Fatalf("unexpected one-trial error: %v", err)
	}
	if _, err := Summarize([]float64{1, math.NaN()}); err == nil || !strings.Contains(err.Error(), "not finite") {
		t.Fatalf("unexpected NaN error: %v", err)
	}
	if _, err := PercentileType7(nil, 0.5); err == nil {
		t.Fatal("empty percentile input was accepted")
	}
	if _, err := PercentileType7([]float64{1, 2}, 1.1); err == nil {
		t.Fatal("out-of-range percentile was accepted")
	}
	if _, err := FlagRobustZ([]float64{1, 2}, 0); err == nil {
		t.Fatal("non-positive robust-z threshold was accepted")
	}
	if _, err := Summarize([]float64{-math.MaxFloat64, math.MaxFloat64}); err == nil {
		t.Fatal("overflowing statistics were accepted")
	}
}

func TestStatsJSONIsDeterministic(t *testing.T) {
	stats, err := Summarize([]float64{10, 11, 9, 10})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stats.JSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := stats.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("stats JSON changed between calls")
	}
	if !bytes.Contains(first, []byte(`"sample_stddev"`)) || first[len(first)-1] != '\n' {
		t.Fatalf("unexpected deterministic JSON:\n%s", first)
	}
}

func assertClose(t *testing.T, got float64, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
		t.Fatalf("got %.15g, want %.15g", got, want)
	}
}
