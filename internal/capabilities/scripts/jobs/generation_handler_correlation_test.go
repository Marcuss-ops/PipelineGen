package jobs

import (
	"context"
	"encoding/json"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type correlationRunRepo struct {
	run             *scriptgen.GenerationRun
	jobLookupCalls  int
	keyLookupCalls  int
	setJobIDCalls   int
	stageUpdateCall int
}

func (r *correlationRunRepo) Create(context.Context, *scriptgen.GenerationRun) error { return nil }
func (r *correlationRunRepo) Get(context.Context, string) (*scriptgen.GenerationRun, error) {
	return r.run, nil
}
func (r *correlationRunRepo) GetByJobID(context.Context, string) (*scriptgen.GenerationRun, error) {
	r.jobLookupCalls++
	return nil, nil
}
func (r *correlationRunRepo) GetByIdempotencyKey(context.Context, string) (*scriptgen.GenerationRun, error) {
	r.keyLookupCalls++
	return r.run, nil
}
func (r *correlationRunRepo) SetJobID(_ context.Context, _, jobID string) error {
	r.setJobIDCalls++
	r.run.JobID = jobID
	return nil
}
func (r *correlationRunRepo) UpdateStage(context.Context, string, scriptgen.RunStatus, scriptgen.Stage) error {
	r.stageUpdateCall++
	return nil
}
func (r *correlationRunRepo) FailRun(context.Context, scriptgen.FailRunInput) error { return nil }
func (r *correlationRunRepo) SavePartialResult(context.Context, string, *scriptgen.GenerateResult) error {
	return nil
}

func TestGenerateJobHandlerCorrelatesRunWhenJobIDBindingRacesWorker(t *testing.T) {
	runRepo := &correlationRunRepo{run: &scriptgen.GenerationRun{ID: "run-1"}}
	handler := NewGenerateJobHandler(nil, nil, zap.NewNop())
	handler.SetRunRepository(runRepo)

	result, err := handler.Handle(context.Background(), &job.Job{
		ID:            "job-1",
		CorrelationID: "idem-1",
		Payload:       json.RawMessage(`{}`),
	}, nil)
	if err == nil || result != nil {
		t.Fatalf("invalid payload should fail after correlation lookup: result=%v err=%v", result, err)
	}
	if runRepo.jobLookupCalls != 1 || runRepo.keyLookupCalls != 1 || runRepo.setJobIDCalls != 1 || runRepo.stageUpdateCall != 1 {
		t.Fatalf("race fallback did not self-heal run correlation: %#v", runRepo)
	}
	if runRepo.run.JobID != "job-1" {
		t.Fatalf("self-healed job ID = %q, want job-1", runRepo.run.JobID)
	}
}

var _ scriptgen.RunRepository = (*correlationRunRepo)(nil)
