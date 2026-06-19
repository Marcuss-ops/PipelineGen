package app

import (
	jobshandler "github.com/Marcuss-ops/PipelineGen/internal/api"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"

	"go.uber.org/zap"
)

// JobsWiring holds the Jobs module wiring
type JobsWiring struct {
	Handler *jobshandler.JobsHandler
	Module  module.Module
}

// WireJobs creates the Jobs handler and module
func WireJobs(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*JobsWiring, error) {
	handler := jobshandler.NewJobsHandler(coreDeps.JobsService, log)

	mod := jobsapi.NewModule(cfg, log, jobsapi.NewHandler(handler))
	log.Info("created Jobs module using api/jobs")

	return &JobsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
