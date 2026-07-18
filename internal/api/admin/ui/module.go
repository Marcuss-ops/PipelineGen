// Package adminui provides the Admin UI HTTP capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
package adminui

import (
	"fmt"
	"io/fs"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
type Dependencies struct {
	// StaticFS is the filesystem containing the built React app.
	// Required.
	StaticFS fs.FS

	// EnabledFunc decides whether the module's routes are mounted.
	// Required.
	EnabledFunc func() bool

	// ModuleOpts are variadic api.RouteModuleOption decorators
	// (typically api.WithMiddleware(...)). Optional.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil → zap.NewNop().
	Logger *zap.Logger
}

// UIDescriptor is the concrete capability Descriptor returned by Build.
type UIDescriptor struct {
	Module api.Module
}

func (d *UIDescriptor) Name() string                       { return d.Module.Name() }
func (d *UIDescriptor) Enabled() bool                      { return d.Module.Enabled() }
func (d *UIDescriptor) RegisterRoutes(rg *gin.RouterGroup) { d.Module.RegisterRoutes(rg) }

// Build composes the Admin UI HTTP capability from the typed dependencies.
// Returns a fail-closed error when any mandatory dep is nil/empty.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.StaticFS == nil {
		return nil, fmt.Errorf("adminui.Build: StaticFS is required")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("adminui.Build: EnabledFunc is required")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewHandler(deps.StaticFS, log)

	mod := api.NewRouteModule(
		"admin-ui",
		deps.EnabledFunc,
		"/ui",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &UIDescriptor{Module: mod}, nil
}

// HealthResponse is the JSON shape returned by /api/admin/ui/health.
type HealthResponse struct {
	OK bool   `json:"ok"`
	UI string `json:"ui"`
}

// WriteHealth writes the health response.
func WriteHealth(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{OK: true, UI: "admin-ui"})
}
