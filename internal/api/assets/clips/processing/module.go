// Package processing is the clips sub-descriptor that owns media
// processing routes: reprocess and enrich.
package processing

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/submodule"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
type Dependencies struct {
	Handler     submodule.RouteRegistrar
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	ModuleOpts  []api.RouteModuleOption
}

// Build composes the processing sub-descriptor.
func Build(deps Dependencies) (api.Descriptor, error) {
	return submodule.Build(submodule.Deps{
		Name:        "clips-processing",
		Handler:     deps.Handler,
		EnabledFunc: deps.EnabledFunc,
		Idempotency: deps.Idempotency,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}
