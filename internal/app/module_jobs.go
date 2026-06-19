package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// JobsWiring holds the Jobs module wiring.
type JobsWiring struct {
	Handler *jobs.JobsHandler
	Module  module.Module
}

// WireJobs creates the Jobs handler and module.
func WireJobs(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*JobsWiring, error) {
	handler := jobs.NewJobsHandler(coreDeps.JobsService, log)
	mod := jobs.NewModule(cfg, log, handler)
	log.Info("created Jobs module using api/jobs")
	return &JobsWiring{Handler: handler, Module: mod}, nil
}
