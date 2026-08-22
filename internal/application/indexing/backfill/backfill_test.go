package backfill

import (
	"context"
	"reflect"
	"testing"

	"go.uber.org/zap"
)

// recordingEnqueuer captures (assetID, force) pairs for assertions.
type recordingEnqueuer struct {
	calls []struct {
		id    string
		force bool
	}
	err error
}

func (r *recordingEnqueuer) EnqueueReindex(_ context.Context, assetID, _ string, force bool) error {
	if r.err != nil {
		return r.err
	}
	r.calls = append(r.calls, struct {
		id    string
		force bool
	}{assetID, force})
	return nil
}

func (r *recordingEnqueuer) ids() []string {
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c.id
	}
	return out
}

func nopLog() *zap.Logger { return zap.NewNop() }

// TestRun_OnlyMissingEnqueuesOnlyIncompleteAssets pins the core contract of
// the embedding backfill: in --only-missing mode (the default), fully-embedded
// assets are skipped and only assets with at least one missing channel are
// enqueued, always with force=true.
func TestRun_OnlyMissingEnqueuesOnlyIncompleteAssets(t *testing.T) {
	candidates := []Candidate{
		{ID: "a1", HasText: true, HasTranscript: true, HasVisual: true, HasAudio: true},     // complete → skip
		{ID: "a2", HasText: true, HasTranscript: true, HasVisual: true, HasAudio: false},    // missing audio → enqueue
		{ID: "a3", HasText: false, HasTranscript: false, HasVisual: false, HasAudio: false}, // all missing → enqueue
	}
	fetch := func(_ context.Context, _ Deps, _ *Checkpoint) ([]Candidate, error) {
		return candidates, nil
	}
	enq := &recordingEnqueuer{}

	report, _, err := Run(context.Background(), Deps{Apply: true, OnlyMissing: true, Progress: 50}, fetch, enq, nopLog())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if want := []string{"a2", "a3"}; !reflect.DeepEqual(enq.ids(), want) {
		t.Fatalf("enqueued = %v, want %v", enq.ids(), want)
	}
	for _, c := range enq.calls {
		if !c.force {
			t.Fatalf("enqueue for %s must carry force=true (admin repair opt-in), got force=%v", c.id, c.force)
		}
	}
	if report.Processed != 2 || report.Succeeded != 2 || report.Skipped != 1 {
		t.Fatalf("report = %+v, want processed=2 succeeded=2 skipped=1", report)
	}
	if report.Mode != "apply" {
		t.Fatalf("mode = %q, want apply", report.Mode)
	}
	// a2 misses only audio; a3 misses all four channels.
	if report.MissingAudio != 2 || report.MissingText != 1 || report.MissingTranscript != 1 || report.MissingVisual != 1 {
		t.Fatalf("missing channel counts = %+v, want text=1 transcript=1 visual=1 audio=2", report)
	}
}

// TestRun_AllModeEnqueuesCompleteAssets pins that --all (OnlyMissing=false)
// processes every candidate, including fully-embedded ones.
func TestRun_AllModeEnqueuesCompleteAssets(t *testing.T) {
	candidates := []Candidate{
		{ID: "a1", HasText: true, HasTranscript: true, HasVisual: true, HasAudio: true},
		{ID: "a2", HasText: false, HasTranscript: false, HasVisual: false, HasAudio: false},
	}
	fetch := func(_ context.Context, _ Deps, _ *Checkpoint) ([]Candidate, error) {
		return candidates, nil
	}
	enq := &recordingEnqueuer{}

	report, _, err := Run(context.Background(), Deps{Apply: true, OnlyMissing: false, Progress: 50}, fetch, enq, nopLog())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"a1", "a2"}; !reflect.DeepEqual(enq.ids(), want) {
		t.Fatalf("enqueued = %v, want %v", enq.ids(), want)
	}
	if report.Skipped != 0 || report.Processed != 2 {
		t.Fatalf("report = %+v, want skipped=0 processed=2", report)
	}
}

// TestRun_DryRunEnqueuesNothing pins that dry-run computes counts but never
// dispatches an outbox event.
func TestRun_DryRunEnqueuesNothing(t *testing.T) {
	candidates := []Candidate{
		{ID: "a1", HasText: false, HasTranscript: false, HasVisual: false, HasAudio: false},
	}
	fetch := func(_ context.Context, _ Deps, _ *Checkpoint) ([]Candidate, error) {
		return candidates, nil
	}
	enq := &recordingEnqueuer{}

	report, _, err := Run(context.Background(), Deps{Apply: false, OnlyMissing: true, Progress: 50}, fetch, enq, nopLog())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(enq.calls) != 0 {
		t.Fatalf("dry-run must not enqueue, got %v", enq.ids())
	}
	if report.Mode != "dry-run" || report.AnyMissing != 1 {
		t.Fatalf("report = %+v, want mode=dry-run any_missing=1", report)
	}
}

// TestRun_EnqueueFailureRecorded pins that a failed enqueue is counted as
// failed and surfaced in Errors, while the run continues past the failure.
// (FailedIDs is checkpoint-scoped: it is only populated when a checkpoint
// path is configured.)
func TestRun_EnqueueFailureRecorded(t *testing.T) {
	candidates := []Candidate{
		{ID: "a1", HasText: false, HasTranscript: false, HasVisual: false, HasAudio: false},
		{ID: "a2", HasText: false, HasTranscript: false, HasVisual: false, HasAudio: false},
	}
	fetch := func(_ context.Context, _ Deps, _ *Checkpoint) ([]Candidate, error) {
		return candidates, nil
	}
	enq := &recordingEnqueuer{}
	enq.err = context.DeadlineExceeded

	report, _, err := Run(context.Background(), Deps{Apply: true, OnlyMissing: true, Progress: 50}, fetch, enq, nopLog())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Failed != 2 || len(report.Errors) != 2 {
		t.Fatalf("report = %+v, want failed=2 errors=2", report)
	}
}
