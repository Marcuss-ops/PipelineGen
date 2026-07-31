package processmetrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingRepository struct {
	metrics []*Metric
	err     error
}

func (r *recordingRepository) Insert(_ context.Context, metric *Metric) error {
	copyMetric := *metric
	r.metrics = append(r.metrics, &copyMetric)
	return r.err
}

type retryingRepository struct {
	metrics []*Metric
	calls   int
}

func (r *retryingRepository) Insert(_ context.Context, metric *Metric) error {
	r.calls++
	if r.calls == 1 {
		return errors.New("temporary sqlite failure")
	}
	copyMetric := *metric
	r.metrics = append(r.metrics, &copyMetric)
	return nil
}

func TestRecorder_EndPersistsSuccessAndDetails(t *testing.T) {
	repo := &recordingRepository{}
	recorder := NewRecorder(repo)
	clock := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	recorder.now = func() time.Time { return clock }

	handle := recorder.Start(context.Background(), StartInput{
		ProcessType: "stock",
		JobID:       "job-1",
		ParentJobID: "run-1",
		Phase:       "stock.youtube_download",
		Provider:    "youtube",
	})
	handle.SetItems(3, 3)
	handle.SetBytes(0, 285000000)
	handle.SetQueueWait(1500 * time.Millisecond)
	handle.SetRetryCount(1)
	handle.SetDetails(map[string]any{
		"videos_found":            float64(3),
		"download_bytes":          float64(285000000),
		"output_duration_seconds": float64(15),
	})

	if err := handle.End(nil); err != nil {
		t.Fatalf("End: %v", err)
	}
	if len(repo.metrics) != 1 {
		t.Fatalf("persisted metrics = %d, want 1", len(repo.metrics))
	}
	got := repo.metrics[0]
	if got.Status != "success" || got.ErrorCode != "" {
		t.Fatalf("status/error_code = %q/%q, want success/empty", got.Status, got.ErrorCode)
	}
	if got.Phase != "stock.youtube_download" || got.ItemsIn != 3 || got.ItemsOut != 3 {
		t.Fatalf("phase/items = %q/%d/%d", got.Phase, got.ItemsIn, got.ItemsOut)
	}
	if got.BytesOut != 285000000 || got.QueueWaitMs != 1500 || got.RetryCount != 1 {
		t.Fatalf("bytes/queue/retry = %d/%d/%d", got.BytesOut, got.QueueWaitMs, got.RetryCount)
	}
	if got.Details["download_bytes"] != float64(285000000) {
		t.Fatalf("details = %#v", got.Details)
	}

	if err := handle.End(nil); err != nil {
		t.Fatalf("second End: %v", err)
	}
	if len(repo.metrics) != 1 {
		t.Fatalf("second End persisted %d rows, want 1", len(repo.metrics))
	}
}

func TestRecorder_EndPersistsFailureAndErrorCode(t *testing.T) {
	repo := &recordingRepository{}
	recorder := NewRecorder(repo)
	handle := recorder.Start(context.Background(), StartInput{ProcessType: "stock", JobID: "job-1", Phase: "stock.extract"})

	if err := handle.End(errors.New("ffmpeg failed")); err != nil {
		t.Fatalf("End: %v", err)
	}
	if got := repo.metrics[0]; got.Status != "failure" || got.ErrorCode != "phase_error" {
		t.Fatalf("status/error_code = %q/%q, want failure/phase_error", got.Status, got.ErrorCode)
	}
}

func TestRecorder_EndCanRetryAfterPersistenceFailure(t *testing.T) {
	repo := &retryingRepository{}
	recorder := NewRecorder(repo)
	handle := recorder.Start(context.Background(), StartInput{ProcessType: "stock", JobID: "job-1", Phase: "stock.extract"})

	if err := handle.End(nil); err == nil {
		t.Fatal("first End error = nil, want persistence failure")
	}
	if err := handle.End(nil); err != nil {
		t.Fatalf("retry End: %v", err)
	}
	if repo.calls != 2 || len(repo.metrics) != 1 {
		t.Fatalf("repository calls/rows = %d/%d, want 2/1", repo.calls, len(repo.metrics))
	}
	if err := handle.End(nil); err != nil {
		t.Fatalf("third End: %v", err)
	}
	if repo.calls != 2 || len(repo.metrics) != 1 {
		t.Fatalf("post-success calls/rows = %d/%d, want 2/1", repo.calls, len(repo.metrics))
	}
}

func TestRecorder_NilRepositoryIsNoOp(t *testing.T) {
	handle := NewRecorder(nil).Start(nil, StartInput{ProcessType: "stock", JobID: "job-1", Phase: "stock.plan"})
	if err := handle.End(nil); err != nil {
		t.Fatalf("no-op End: %v", err)
	}
}

func TestRecorder_NegativeValuesAreClamped(t *testing.T) {
	repo := &recordingRepository{}
	handle := NewRecorder(repo).Start(context.Background(), StartInput{ProcessType: "stock", JobID: "job-1", Phase: "stock.plan"})
	handle.SetItems(-1, -2)
	handle.SetBytes(-1, -2)
	handle.SetQueueWait(-time.Second)
	handle.SetRetryCount(-1)
	if err := handle.End(nil); err != nil {
		t.Fatalf("End: %v", err)
	}
	got := repo.metrics[0]
	if got.ItemsIn != 0 || got.ItemsOut != 0 || got.BytesIn != 0 || got.BytesOut != 0 || got.QueueWaitMs != 0 || got.RetryCount != 0 {
		t.Fatalf("negative values were not clamped: %#v", got)
	}
}
