// Package system owns construction of the system HTTP capability.
package system

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"go.uber.org/zap"
)

type Dependencies struct {
	Config        systemapi.DoctorConfig
	Logger        *zap.Logger
	ToolChecker   appassets.ToolChecker
	ProcessRunner appassets.ProcessRunner
	DBHealth      appassets.DBHealthChecker
	DriveOps      systemapi.DriveAdminOps
	Reconciler    systemapi.Reconciler
}

// Module is a native runtime capability builder. Its dependencies are fixed
// at composition time and it returns an executable runtime descriptor.
type Module struct{ deps Dependencies }

func NewModule(deps Dependencies) Module { return Module{deps: deps} }
func (m Module) Name() string            { return "system" }

func (m Module) Build(_ api.BuildContext) (api.RuntimeModule, error) {
	if m.deps.ToolChecker == nil {
		return api.RuntimeModule{}, fmt.Errorf("system.Build: ToolChecker is required")
	}
	if m.deps.ProcessRunner == nil {
		return api.RuntimeModule{}, fmt.Errorf("system.Build: ProcessRunner is required")
	}
	if m.deps.DBHealth == nil {
		return api.RuntimeModule{}, fmt.Errorf("system.Build: DBHealth is required")
	}
	log := m.deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	transport, err := Build(Dependencies{
		Config: m.deps.Config, Logger: log, ToolChecker: m.deps.ToolChecker,
		ProcessRunner: m.deps.ProcessRunner, DBHealth: m.deps.DBHealth,
		DriveOps: m.deps.DriveOps, Reconciler: m.deps.Reconciler,
	})
	if err != nil {
		return api.RuntimeModule{}, err
	}
	return api.RuntimeModuleFor(m.Name(), "register.System", transport)
}

func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.ToolChecker == nil {
		return nil, fmt.Errorf("system.Build: ToolChecker is required")
	}
	if deps.ProcessRunner == nil {
		return nil, fmt.Errorf("system.Build: ProcessRunner is required")
	}
	if deps.DBHealth == nil {
		return nil, fmt.Errorf("system.Build: DBHealth is required")
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return systemapi.NewModule(deps.Config, log, deps.ToolChecker, deps.ProcessRunner, deps.DBHealth, deps.DriveOps, deps.Reconciler), nil
}

var _ api.CapabilityModule = Module{}
