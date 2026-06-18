package workflowservice

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/workflow"
)

type Repository interface {
	Create(ctx context.Context, wf *workflow.Workflow) error
	Get(ctx context.Context, id string) (*workflow.Workflow, error)
	ListSteps(ctx context.Context, workflowID string) ([]workflow.Step, error)
	CreateSteps(ctx context.Context, steps []workflow.Step) error
	CreateDependencies(ctx context.Context, deps []workflow.Dependency) error
	AttachJob(ctx context.Context, stepID, jobID string) error
	UpdateWorkflowStatus(ctx context.Context, id string, status workflow.Status) error
	UpdateStepStatus(ctx context.Context, id string, status workflow.StepStatus) error
}

type Service struct {
	repo       Repository
	definitions Registry
}

func New(repo Repository, defs Registry) *Service {
	return &Service{repo: repo, definitions: defs}
}

func (s *Service) CreateWorkflow(ctx context.Context, cmd CreateWorkflowCommand) (*workflow.Workflow, error) {
	if s.repo == nil {
		return nil, workflow.ErrWorkflowNotFound
	}
	wf := &workflow.Workflow{
		Type:           cmd.Type,
		Version:        cmd.Version,
		Status:         workflow.StatusPending,
		CorrelationID:  cmd.CorrelationID,
		IdempotencyKey: cmd.IdempotencyKey,
		InputJSON:      cmd.InputJSON,
	}
	if err := s.repo.Create(ctx, wf); err != nil {
		return nil, err
	}
	return wf, nil
}

func (s *Service) StartWorkflow(ctx context.Context, workflowID string) error {
	return s.repo.UpdateWorkflowStatus(ctx, workflowID, workflow.StatusRunning)
}

func (s *Service) AttachJob(ctx context.Context, cmd AttachJobCommand) error {
	return s.repo.AttachJob(ctx, cmd.StepID, cmd.JobID)
}

func (s *Service) HandleJobCompleted(ctx context.Context, cmd StepResultCommand) error {
	return s.repo.UpdateStepStatus(ctx, cmd.StepID, workflow.StepCompleted)
}

func (s *Service) HandleJobFailed(ctx context.Context, workflowID, stepID string, output json.RawMessage) error {
	_ = output
	return s.repo.UpdateStepStatus(ctx, stepID, workflow.StepFailed)
}
