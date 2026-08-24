// Package stockbatches — HTTP capability module for batch stock runs.
package assets

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
)

// Dependencies is the typed narrow input to Build.
type Dependencies struct {
	Coordinator *stockplan.Coordinator
	EnabledFunc func() bool
	Logger      *zap.Logger
	ModuleOpts  []api.RouteModuleOption
}

// StockBatchesDescriptor is the concrete capability Descriptor.
type StockBatchesDescriptor struct {
	Module api.Module
}

// Name returns the module name.
func (d *StockBatchesDescriptor) Name() string { return d.Module.Name() }

// Enabled forwards to the Module's closure.
func (d *StockBatchesDescriptor) Enabled() bool { return d.Module.Enabled() }

// RegisterRoutes forwards to the Module.
func (d *StockBatchesDescriptor) RegisterRoutes(rg *gin.RouterGroup) { d.Module.RegisterRoutes(rg) }

// Build composes the stock-batches HTTP capability.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Coordinator == nil {
		return nil, fmt.Errorf("stockbatches.Build: Coordinator is required")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("stockbatches.Build: EnabledFunc is required")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewHandler(deps.Coordinator, log)
	mod := api.NewRouteModule(
		"stock-batches",
		deps.EnabledFunc,
		"/stock-batches",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &StockBatchesDescriptor{Module: mod}, nil
}
