package performance

import (
	"context"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

// seedColdWarmBattery inserts a 5-attempt battery: run-1 is cold (slow
// startup/render), runs 2-5 are warm (progressively faster). Each attempt has
// two measured phases (startup + render_loop) sharing the attempt's run_id.
func seedColdWarmBattery(t *testing.T, store *OperationStore, jobID string, base time.Time) {
	t.Helper()
	rows := [][]any{
		// run-1 (cold #1)
		{"cw-1s", "run-1", jobID, "", "chronon.startup", 5000, 0, 0, 0, 0, 0, 0, 0, base.Add(0 * time.Second).Format(time.RFC3339Nano)},
		{"cw-1r", "run-1", jobID, "", "chronon.render_loop", 25000, 45000, 0, 0, 0, 0, 0, 0, base.Add(0 * time.Second).Format(time.RFC3339Nano)},
		// run-2 (warm #2)
		{"cw-2s", "run-2", jobID, "", "chronon.startup", 1000, 0, 0, 0, 0, 0, 0, 0, base.Add(1 * time.Second).Format(time.RFC3339Nano)},
		{"cw-2r", "run-2", jobID, "", "chronon.render_loop", 13000, 45000, 0, 0, 0, 0, 0, 0, base.Add(1 * time.Second).Format(time.RFC3339Nano)},
		// run-3 (warm #3)
		{"cw-3s", "run-3", jobID, "", "chronon.startup", 900, 0, 0, 0, 0, 0, 0, 0, base.Add(2 * time.Second).Format(time.RFC3339Nano)},
		{"cw-3r", "run-3", jobID, "", "chronon.render_loop", 12000, 45000, 0, 0, 0, 0, 0, 0, base.Add(2 * time.Second).Format(time.RFC3339Nano)},
		// run-4 (warm #4)
		{"cw-4s", "run-4", jobID, "", "chronon.startup", 800, 0, 0, 0, 0, 0, 0, 0, base.Add(3 * time.Second).Format(time.RFC3339Nano)},
		{"cw-4r", "run-4", jobID, "", "chronon.render_loop", 11000, 45000, 0, 0, 0, 0, 0, 0, base.Add(3 * time.Second).Format(time.RFC3339Nano)},
		// run-5 (warm #5)
		{"cw-5s", "run-5", jobID, "", "chronon.startup", 700, 0, 0, 0, 0, 0, 0, 0, base.Add(4 * time.Second).Format(time.RFC3339Nano)},
		{"cw-5r", "run-5", jobID, "", "chronon.render_loop", 10000, 45000, 0, 0, 0, 0, 0, 0, base.Add(4 * time.Second).Format(time.RFC3339Nano)},
	}
	seedWorkHistory(t, store, rows)
}

func bucketByOp(buckets []capperformance.OperationBucket) map[string]capperformance.OperationBucket {
	m := map[string]capperformance.OperationBucket{}
	for _, b := range buckets {
		m[b.Operation] = b
	}
	return m
}

func TestColdWarmComparisonSplitsCold1VsWarm2To5(t *testing.T) {
	store := newOperationsStore(t)
	seedColdWarmBattery(t, store, "job-cw", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))

	got, err := store.ColdWarmComparison(context.Background(), capperformance.ColdWarmOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 5 || got.ColdAttempts != 1 || got.WarmAttempts != 4 {
		t.Fatalf("attempts = %d (cold %d, warm %d), want 5 (1, 4)", got.Attempts, got.ColdAttempts, got.WarmAttempts)
	}
	cold := bucketByOp(got.Cold)
	warm := bucketByOp(got.Warm)

	cr := cold["chronon.render_loop"]
	if cr.Runs != 1 || cr.AvgElapsedMS != 25000 || cr.MinElapsedMS != 25000 || cr.MaxElapsedMS != 25000 {
		t.Fatalf("cold render_loop = %+v, want runs=1 avg=25000 min=25000 max=25000", cr)
	}
	wr := warm["chronon.render_loop"]
	if wr.Runs != 4 || wr.MinElapsedMS != 10000 || wr.MaxElapsedMS != 13000 {
		t.Fatalf("warm render_loop = %+v, want runs=4 min=10000 max=13000", wr)
	}
	if wr.AvgElapsedMS != 11500 {
		t.Fatalf("warm render_loop avg = %v, want 11500", wr.AvgElapsedMS)
	}
	ws := warm["chronon.startup"]
	if ws.Runs != 4 || ws.AvgElapsedMS != 850 || ws.MinElapsedMS != 700 || ws.MaxElapsedMS != 1000 {
		t.Fatalf("warm startup = %+v, want runs=4 avg=850 min=700 max=1000", ws)
	}
	if _, ok := cold["chronon.startup"]; !ok {
		t.Fatal("cold startup bucket missing")
	}
}

func TestColdWarmComparisonHonorsMaxAttempts(t *testing.T) {
	store := newOperationsStore(t)
	seedColdWarmBattery(t, store, "job-cw", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))

	got, err := store.ColdWarmComparison(context.Background(), capperformance.ColdWarmOptions{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 3 || got.ColdAttempts != 1 || got.WarmAttempts != 2 {
		t.Fatalf("attempts = %d (cold %d, warm %d), want 3 (1, 2)", got.Attempts, got.ColdAttempts, got.WarmAttempts)
	}
	warm := bucketByOp(got.Warm)
	wr := warm["chronon.render_loop"]
	// Warm #2..3: 13000 + 12000.
	if wr.Runs != 2 || wr.AvgElapsedMS != 12500 || wr.MaxElapsedMS != 13000 {
		t.Fatalf("warm render_loop = %+v, want runs=2 avg=12500 max=13000", wr)
	}
}

func TestColdWarmComparisonFiltersByJob(t *testing.T) {
	store := newOperationsStore(t)
	seedColdWarmBattery(t, store, "job-a", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	// A single earlier attempt of a different job must not shift job-a's
	// cold/warm positions when the filter is applied.
	seedWorkHistory(t, store, [][]any{
		{"other-1", "run-other", "job-other", "", "probe", 999, 0, 0, 0, 0, 0, 0, 0, "2026-07-01T00:00:00Z"},
	})

	got, err := store.ColdWarmComparison(context.Background(), capperformance.ColdWarmOptions{JobID: "job-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got.JobID != "job-a" || got.Attempts != 5 || got.ColdAttempts != 1 {
		t.Fatalf("filtered report = job %q attempts %d (cold %d), want job-a 5 (1)", got.JobID, got.Attempts, got.ColdAttempts)
	}
	cold := bucketByOp(got.Cold)
	if cr := cold["chronon.render_loop"]; cr.AvgElapsedMS != 25000 {
		t.Fatalf("filtered cold render_loop avg = %v, want 25000 (other job must not rank first)", cr.AvgElapsedMS)
	}
}

func TestColdWarmComparisonExcludesZeroElapsedAttempts(t *testing.T) {
	store := newOperationsStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	seedWorkHistory(t, store, [][]any{
		{"z-1", "run-zero", "job-z", "", "chronon.startup", 0, 0, 0, 0, 0, 0, 0, 0, now},
		{"m-1", "run-measured", "job-z", "", "chronon.render_loop", 8000, 45000, 0, 0, 0, 0, 0, 0, now},
	})

	got, err := store.ColdWarmComparison(context.Background(), capperformance.ColdWarmOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Only run-measured ranks as an attempt; the zero-elapsed run does not.
	if got.Attempts != 1 || got.ColdAttempts != 1 || got.WarmAttempts != 0 {
		t.Fatalf("attempts = %d (cold %d, warm %d), want 1 (1, 0)", got.Attempts, got.ColdAttempts, got.WarmAttempts)
	}
	cold := bucketByOp(got.Cold)
	if cr := cold["chronon.render_loop"]; cr.Runs != 1 || cr.AvgElapsedMS != 8000 {
		t.Fatalf("cold render_loop = %+v, want runs=1 avg=8000", cr)
	}
	if _, ok := cold["chronon.startup"]; ok {
		t.Fatal("zero-elapsed startup must not produce a bucket row")
	}
}

func TestColdWarmComparisonEmptyHistoryIsValid(t *testing.T) {
	store := newOperationsStore(t)
	got, err := store.ColdWarmComparison(context.Background(), capperformance.ColdWarmOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 0 || got.ColdAttempts != 0 || got.WarmAttempts != 0 {
		t.Fatalf("empty history attempts = %d, want 0", got.Attempts)
	}
	if len(got.Cold) != 0 || len(got.Warm) != 0 {
		t.Fatalf("empty history buckets = %d/%d, want empty", len(got.Cold), len(got.Warm))
	}
}
