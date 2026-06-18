package app

import (
	jobshandler "github.com/Marcuss-ops/PipelineGen/internal/api/handlers/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/module"

	"go.uber.org/zap"
)

// JobsWiring holds the Jobs module wiring
type JobsWiring struct {
	Handler *jobshandler.Handler
	Module  module.Module
}

// WireJobs creates the Jobs handler and module
func WireJobs(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*JobsWiring, error) {
	handler := jobshandler.NewHandler(coreDeps.JobsService, log)

	mod := module.NewRouteModule("jobs", nil, "/jobs", handler, log)
	log.Info("created Jobs module using RouteModule")

	return &JobsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
