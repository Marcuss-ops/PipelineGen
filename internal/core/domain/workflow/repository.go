package workflow

import "context"

type Repository interface {
	Create(ctx context.Context, wf *Workflow) error
	Get(ctx context.Context, id string) (*Workflow, error)
	ListSteps(ctx context.Context, workflowID string) ([]Step, error)
	CreateSteps(ctx context.Context, steps []Step) error
	CreateDependencies(ctx context.Context, deps []Dependency) error
	AttachJob(ctx context.Context, stepID, jobID string) error
	UpdateWorkflowStatus(ctx context.Context, id string, status Status) error
	UpdateStepStatus(ctx context.Context, id string, status StepStatus) error
}
