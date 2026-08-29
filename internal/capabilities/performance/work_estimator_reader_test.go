package performance

import (
	"context"
	"errors"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type fakeWorkHistorySource struct {
	rows []WorkHistoryRow
	err  error
}

func (f *fakeWorkHistorySource) ListWorkHistory(_ context.Context, _ int) ([]WorkHistoryRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func TestWorkHistoryReaderMapsChrononRenderLoopToRenderKind(t *testing.T) {
	r := NewWorkHistoryReader(&fakeWorkHistorySource{rows: []WorkHistoryRow{
		{Operation: "chronon.render_loop", ElapsedMS: 24971, SourceDurationMS: 120000, FPS: 30.0},
		{Operation: "probe", ElapsedMS: 375, SourceDurationMS: 120000, FPS: 30.0},
	}})

	obs, err := r.ListPreparationWorkObservations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("returned %d observations, want 2", len(obs))
	}
	// The fine-grained render loop feeds the render kind (frames-scaled),
	// so the scheduler's expected_work_ms for a render unit is the real
	// render cost — not startup/prepare/drain overhead.
	if obs[0].Kind != "chronon.render" {
		t.Fatalf("kind = %q, want chronon.render (mapped from chronon.render_loop)", obs[0].Kind)
	}
	if obs[0].WallMS != 24971 {
		t.Fatalf("wall_ms = %d, want 24971", obs[0].WallMS)
	}
	if obs[0].Dimension != job.WorkloadFrames {
		t.Fatalf("dimension = %q, want frames (duration×fps)", obs[0].Dimension)
	}
	if want := 120000.0 / 1000.0 * 30.0; obs[0].Amount != want {
		t.Fatalf("amount = %v, want %v", obs[0].Amount, want)
	}
	// Unmapped operations keep the operation name as the unit kind.
	if obs[1].Kind != "probe" || obs[1].WallMS != 375 {
		t.Fatalf("identity mapping broken: %+v", obs[1])
	}
	if obs[1].Dimension != job.WorkloadNone {
		t.Fatalf("probe dimension = %q, want none (no scaling axis for probe)", obs[1].Dimension)
	}
}

func TestWorkHistoryReaderDerivesBytesAxisForDownloadKinds(t *testing.T) {
	r := NewWorkHistoryReader(&fakeWorkHistorySource{rows: []WorkHistoryRow{
		{Operation: "asset.download", ElapsedMS: 5000, SourceSizeBytes: 64_000_000},
	}})

	obs, err := r.ListPreparationWorkObservations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].Kind != "asset.download" || obs[0].Dimension != job.WorkloadBytes || obs[0].Amount != 64_000_000 {
		t.Fatalf("download observation = %+v, want bytes axis 64000000", obs[0])
	}
}

func TestWorkHistoryReaderNoScalingAxisWithoutMeasuredFacts(t *testing.T) {
	// A render-kind row with no duration/fps carries no scaling amount:
	// the canonical Driver() falls back to none, so the estimator uses the
	// per-kind average instead of a bogus scaled estimate.
	r := NewWorkHistoryReader(&fakeWorkHistorySource{rows: []WorkHistoryRow{
		{Operation: "chronon.render_loop", ElapsedMS: 1000},
	}})

	obs, err := r.ListPreparationWorkObservations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].Dimension != job.WorkloadNone || obs[0].Amount != 0 {
		t.Fatalf("observation = %+v, want dimension none", obs[0])
	}
}

func TestWorkHistoryReaderToleratesNilSourceAndPropagatesErrors(t *testing.T) {
	if obs, err := NewWorkHistoryReader(nil).ListPreparationWorkObservations(context.Background(), 10); err != nil || len(obs) != 0 {
		t.Fatalf("nil source: obs=%v err=%v, want empty + nil", obs, err)
	}
	r := NewWorkHistoryReader(&fakeWorkHistorySource{err: errors.New("read failed")})
	if _, err := r.ListPreparationWorkObservations(context.Background(), 10); err == nil {
		t.Fatal("source error must propagate (estimator Bootstrap is fail-open)")
	}
}

// Compile-time guard: the adapter structurally satisfies the jobs-side
// WorkObservationsReader port without importing the jobs package.
var _ interface {
	ListPreparationWorkObservations(context.Context, int) ([]job.WorkObservation, error)
} = (*WorkHistoryReader)(nil)
