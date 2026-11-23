package stockpipeline

import (
	"context"
	"sync"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type canonicalStageRecorder struct {
	mu         sync.Mutex
	stages     []kernobs.StageReport
	operations []kernobs.OperationReport
}

func (r *canonicalStageRecorder) SaveReport(_ context.Context, _ *kernobs.RunReport) error {
	return nil
}
func (r *canonicalStageRecorder) StartReport(_ context.Context, _ *kernobs.RunReport) error {
	return nil
}
func (r *canonicalStageRecorder) AppendStage(_ context.Context, _ string, st kernobs.StageReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages = append(r.stages, st)
	return nil
}
func (r *canonicalStageRecorder) AppendOperation(_ context.Context, _ string, op kernobs.OperationReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations = append(r.operations, op)
	return nil
}
func (r *canonicalStageRecorder) RecordChild(_ context.Context, _ *kernobs.RunReport) error {
	return nil
}
