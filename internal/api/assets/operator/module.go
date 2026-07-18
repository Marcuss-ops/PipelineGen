package operator

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// OperatorDescriptor is the api.Descriptor for the operator console API module.
type OperatorDescriptor struct {
	module  *moduleWrapper
	handler *Handler
}

// Build constructs the OperatorDescriptor from the given dependencies.
func Build(deps Dependencies, log *zap.Logger) (*OperatorDescriptor, error) {
	handler := NewHandler(deps, log)
	mod := NewModule(handler)
	return &OperatorDescriptor{module: mod, handler: handler}, nil
}

func (d *OperatorDescriptor) Name() string  { return d.module.Name() }
func (d *OperatorDescriptor) Enabled() bool { return d.module.Enabled() }
func (d *OperatorDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.module.RegisterRoutes(rg)
}

// Handler returns the underlying handler for non-HTTP consumers.
func (d *OperatorDescriptor) Handler() *Handler {
	return d.handler
}

// Compile-time check that OperatorDescriptor satisfies api.Descriptor.
var _ api.Descriptor = (*OperatorDescriptor)(nil)
