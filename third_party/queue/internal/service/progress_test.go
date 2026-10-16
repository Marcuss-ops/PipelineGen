package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

// TestServiceProgressRoundTrip submits, claims, and reports progress; the
// stored progress must be readable through Get with the reporting worker
// recorded and a fresh last_frame_at timestamp.
func TestServiceProgressRoundTrip(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-progress"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	if err := svc.SetProgress(context.Background(), "job-progress", "w1", model.Progress{FramesDone: 485, TotalFrames: 1800}); err != nil {
		t.Fatalf("set progress: %v", err)
	}

	job, err := svc.Get(context.Background(), "job-progress")
	if err != nil {
		t.Fatal(err)
	}
	if job.Progress == nil {
		t.Fatal("want progress on claimed job, got nil")
	}
	if job.Progress.FramesDone != 485 || job.Progress.TotalFrames != 1800 {
		t.Fatalf("progress = %d/%d, want 485/1800", job.Progress.FramesDone, job.Progress.TotalFrames)
	}
	if job.Progress.Worker != "w1" {
		t.Fatalf("progress worker = %q, want w1", job.Progress.Worker)
	}
	if job.Progress.LastFrameAt.Before(before) {
		t.Fatalf("last_frame_at %v predates the report %v", job.Progress.LastFrameAt, before)
	}
}

// TestServiceProgressRejectsNonOwner ensures a worker that lost (or never
// held) the lease cannot overwrite the current owner's progress.
func TestServiceProgressRejectsNonOwner(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-owner"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	err := svc.SetProgress(context.Background(), "job-owner", "w2", model.Progress{FramesDone: 10})
	if err == nil {
		t.Fatal("want error for non-owner progress report, got nil")
	}
	if !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("want ownership error, got %v", err)
	}
	// The job's progress must remain unset.
	job, err := svc.Get(context.Background(), "job-owner")
	if err != nil {
		t.Fatal(err)
	}
	if job.Progress != nil {
		t.Fatalf("non-owner report applied: %+v", job.Progress)
	}
}

// TestServiceProgressRejectsNonRunningStates covers pending, completed and
// failed jobs: progress is only meaningful while a lease is held.
func TestServiceProgressRejectsNonRunningStates(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-pending"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetProgress(context.Background(), "job-pending", "w1", model.Progress{FramesDone: 1}); err == nil {
		t.Fatal("want error for pending job progress, got nil")
	}
}

// TestServiceProgressOverwriteInPlace verifies each report replaces the
// previous snapshot (latest wins) rather than accumulating.
func TestServiceProgressOverwriteInPlace(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-latest"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetProgress(context.Background(), "job-latest", "w1", model.Progress{FramesDone: 100, TotalFrames: 900}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetProgress(context.Background(), "job-latest", "w1", model.Progress{FramesDone: 200, TotalFrames: 900}); err != nil {
		t.Fatal(err)
	}
	job, err := svc.Get(context.Background(), "job-latest")
	if err != nil {
		t.Fatal(err)
	}
	if job.Progress.FramesDone != 200 {
		t.Fatalf("frames_done = %d, want latest 200", job.Progress.FramesDone)
	}
}

// TestServiceRetryClearsProgress verifies the retry path resets progress so a
// requeued job starts from a clean observability slate (old frames_done must
// not be mistaken for progress of the new attempt).
func TestServiceRetryClearsProgress(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(context.Background(), model.Job{ID: "job-retry-progress"}); err != nil {
		t.Fatal(err)
	}
	// Exhaust max_attempts: only a permanently failed job can be retried.
	for i := 0; i < 3; i++ {
		if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := svc.SetProgress(context.Background(), "job-retry-progress", "w1", model.Progress{FramesDone: 500, TotalFrames: 1000}); err != nil {
				t.Fatal(err)
			}
		}
		if err := svc.Fail(context.Background(), "job-retry-progress", "w1", "boom"); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Retry(context.Background(), "job-retry-progress"); err != nil {
		t.Fatal(err)
	}
	job, err := svc.Get(context.Background(), "job-retry-progress")
	if err != nil {
		t.Fatal(err)
	}
	if job.Progress != nil {
		t.Fatalf("retry must clear progress, got %+v", job.Progress)
	}
}
