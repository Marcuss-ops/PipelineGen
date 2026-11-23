package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type recordingJobStageSink struct {
	mu   sync.Mutex
	recs []job.JobStageStatus
}

func (s *recordingJobStageSink) UpsertJobStageStatus(_ context.Context, rec job.JobStageStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
	return nil
}

func (s *recordingJobStageSink) ListJobStageStatuses(_ context.Context, _ string) ([]job.JobStageStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]job.JobStageStatus(nil), s.recs...), nil
}

func (s *recordingJobStageSink) last() job.JobStageStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.recs) == 0 {
		return job.JobStageStatus{}
	}
	return s.recs[len(s.recs)-1]
}

func newProgressTestRunner(t *testing.T) (*Runner, *recordingJobStageSink) {
	t.Helper()
	sink := &recordingJobStageSink{}
	r := &Runner{}
	r.SetJobStageSink(sink)
	r.bindJobStage("run-1", "job-1")
	return r, sink
}

// TestWriteJobStageCompletedReportsFullProgress pins the terminal projection:
// a completed stage must report 100, not the 0 that was frozen on every
// successful job in the live job_stage_status table.
func TestWriteJobStageCompletedReportsFullProgress(t *testing.T) {
	r, sink := newProgressTestRunner(t)
	r.writeJobStage(context.Background(), "job-1", job.StageVoiceover, job.StageCompleted, time.Now())

	got := sink.last()
	if got.Status != job.StageCompleted || got.Progress != 100 {
		t.Fatalf("completed stage = status %q progress %d, want completed/100", got.Status, got.Progress)
	}
}

// TestWriteJobStageProgressReportsFanOut pins the running projection used by
// the TTS pool: 1 of 4 voiceovers is 25%, with the counter in Detail.
func TestWriteJobStageProgressReportsFanOut(t *testing.T) {
	r, sink := newProgressTestRunner(t)
	r.writeJobStageProgress(context.Background(), "run-1", job.StageVoiceover, 1, 4)

	got := sink.last()
	if got.Stage != job.StageVoiceover || got.Status != job.StageRunning {
		t.Fatalf("progress row = stage %q status %q, want voiceover/running", got.Stage, got.Status)
	}
	if got.Progress != 25 {
		t.Fatalf("progress = %d, want 25", got.Progress)
	}
	if got.Detail != "1/4" {
		t.Fatalf("detail = %q, want 1/4", got.Detail)
	}
}
