package adminconsoleapi

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	adminapp "github.com/Marcuss-ops/PipelineGen/internal/application/adminconsole"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor is the api.Descriptor for the admin console API.
type Descriptor struct {
	handler *Handler
	name    string
	enabled bool
}

// Build constructs the admin console descriptor from the given service and logger.
func Build(service *adminapp.Service, log *zap.Logger) *Descriptor {
	return &Descriptor{
		handler: NewHandler(service, log),
		name:    "adminconsole",
		enabled: true,
	}
}

// Name returns the capability name.
func (d *Descriptor) Name() string { return d.name }

// Enabled reports whether the capability is enabled.
func (d *Descriptor) Enabled() bool { return d.enabled }

// RegisterRoutes mounts admin console routes under /api/admin.
func (d *Descriptor) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/admin")
	d.handler.RegisterRoutes(group)
}

// Handler returns the underlying handler for non-HTTP consumers.
func (d *Descriptor) Handler() *Handler { return d.handler }

// Compile-time check that Descriptor satisfies api.Descriptor.
var _ api.Descriptor = (*Descriptor)(nil)
