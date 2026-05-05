package bootstrap

import (
	jobshandler "velox/go-master/internal/api/handlers/jobs"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

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

	mod := module.NewJobsModule(cfg, log, coreDeps.JobsService, handler)
	log.Info("created Jobs module")

	return &JobsWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
