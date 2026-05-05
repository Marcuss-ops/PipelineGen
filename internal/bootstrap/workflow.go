package bootstrap

import (
	workflowhandler "velox/go-master/internal/api/handlers/workflow"
	"velox/go-master/internal/module"
	"velox/go-master/internal/service/workflowrunner"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// WorkflowWiring holds the Workflow module wiring
type WorkflowWiring struct {
	Handler *workflowhandler.Handler
	Module  module.Module
}

// WireWorkflow creates the Workflow handler and module
func WireWorkflow(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*WorkflowWiring, error) {
	handler := workflowhandler.NewHandler(workflowrunner.NewService(coreDeps.JobsService, log), log)
	mod := module.NewWorkflowModule(cfg, log, handler)
	log.Info("created Workflow module")

	return &WorkflowWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
