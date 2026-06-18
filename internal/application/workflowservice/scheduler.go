package workflowservice

import "context"

func (s *Service) EvaluateReadySteps(ctx context.Context, workflowID string) error {
	return nil
}

func (s *Service) RetryStep(ctx context.Context, workflowID, stepID string) error {
	return nil
}

func (s *Service) CancelWorkflow(ctx context.Context, workflowID string) error {
	return s.repo.UpdateWorkflowStatus(ctx, workflowID, "cancelled")
}
