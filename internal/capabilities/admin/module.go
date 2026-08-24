// Package admin — module.go: the canonical Build entrypoint for the
// Admin HTTP capability (Drive canary).
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// godlike/06 SSOT (one canonical owner per fact): the admin module is
// the SOLE owner of the admin route surface. The Publisher is the
// SOLE owner of the Drive write seam.
package admin

import (
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clipfolder"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build.
type Dependencies struct {
	// Publisher is the canonical delivery.Publisher for Drive upload.
	// MANDATORY — Build returns an error when nil.
	Publisher delivery.Publisher

	// FolderAliasResolver is the canonical YAML-backed alias resolver.
	// MANDATORY — Build returns an error when nil.
	FolderAliasResolver *clipfolder.FolderAliasResolver

	// EnabledFunc is the closure that decides whether the module's
	// routes are mounted. MANDATORY.
	EnabledFunc func() bool

	// ModuleOpts are variadic api.RouteModuleOption decorators
	// (typically api.WithMiddleware(...)). OPTIONAL.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil → zap.NewNop().
	Logger *zap.Logger
}

// AdminDescriptor is the concrete capability Descriptor returned by Build.
type AdminDescriptor struct {
	Module api.Module
}

func (d *AdminDescriptor) Name() string                       { return d.Module.Name() }
func (d *AdminDescriptor) Enabled() bool                      { return d.Module.Enabled() }
func (d *AdminDescriptor) RegisterRoutes(rg *gin.RouterGroup) { d.Module.RegisterRoutes(rg) }

// Build composes the Admin HTTP capability from the typed dependencies.
func Build(deps Dependencies) (api.Descriptor, error) {
	if deps.Publisher == nil {
		return nil, fmt.Errorf("admin.Build: Publisher is required (composition root must wire delivery.Publisher before calling Build)")
	}
	if deps.FolderAliasResolver == nil {
		return nil, fmt.Errorf("admin.Build: FolderAliasResolver is required (composition root must wire the canonical folder alias resolver)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("admin.Build: EnabledFunc is required")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	handler := NewDriveCanaryHandler(deps.Publisher, deps.FolderAliasResolver, log)

	mod := api.NewRouteModule(
		"admin",
		deps.EnabledFunc,
		"/drive",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &AdminDescriptor{Module: mod}, nil
}
