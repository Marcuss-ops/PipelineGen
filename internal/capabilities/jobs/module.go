// Package jobs owns construction of the public jobs HTTP capability.
package jobs

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type Dependencies struct {
	Service     job.Service
	Stats       appjobs.JobStatsReader
	EnabledFunc func() bool
	Logger      *zap.Logger
}

type Module struct{ deps Dependencies }

func NewModule(deps Dependencies) Module { return Module{deps: deps} }
func (m Module) Name() string            { return "jobs" }

func (m Module) Build(_ api.BuildContext) (api.RuntimeModule, error) {
	transport, err := Build(m.deps)
	if err != nil {
		return api.RuntimeModule{}, err
	}
	return api.RuntimeModuleFor(m.Name(), "register.Jobs", transport)
}

func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Service == nil {
		return nil, fmt.Errorf("jobs.Build: Service is required")
	}
	if deps.Stats == nil {
		return nil, fmt.Errorf("jobs.Build: Stats is required")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("jobs.Build: EnabledFunc is required")
	}
	return jobsapi.Build(jobsapi.Dependencies{
		Service: deps.Service, Stats: deps.Stats,
		EnabledFunc: deps.EnabledFunc, Logger: deps.Logger,
	})
}

var _ api.CapabilityModule = Module{}
