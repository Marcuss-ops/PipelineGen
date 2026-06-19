package app

import (
	jobshandler "github.com/Marcuss-ops/PipelineGen/internal/api"
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

	mod := module.NewRouteModule("jobs", nil, "/jobs", handler, log)
	log.Info("created Jobs module using RouteModule")

	return &JobsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
