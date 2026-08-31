// Package system — module.go is the composition root for the
// system module's /system + /drive routes.
//
// Wave 14 close (June 2026): the system Module absorbed the
// standalone internal/platform/drive/handler.go as a second receiver
// (DriveHandler). The system Module now mounts two sub-groups
// sharing the same protected router group:
//
//	/system/doctor     — admin/doctor diagnostics
//	/drive/{reconcile,
//	        cleanup,
//	        folders,
//	        move,
//	        resolve-by-id}
//
// Both sub-groups inherit Auth + RateLimit + WorkspaceScope from
// the protected group mounted in routes.go.
//
// PR4-cleanup delta (June 24, 2026): NewModule signature dropped
// the three concrete infrastructure deps (`*config.Config`,
// `*drive.Uploader`, `Reconciler`) and now relies on
// the typed port surface (DoctorConfig + Reconciler + DriveAdminOps)
// wired at the composition root. No more `internal/platform/*`
// imports in the api/system subtree (AGENTS.md Pattern 8).
package system

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
)

// Module handles system diagnostic routes plus Drive admin ops.
type Module struct {
	name         string
	log          *zap.Logger
	handler      *SystemHandler
	driveHandler *DriveHandler
}

// NewModule creates a new system module.
//
// driveOps is optional; when nil, drive routes return 503 with
// "drive uploader not configured". reconciler is also optional; the
// reconcile/cleanup routes return 503 when it is nil. toolChecker /
// processRunner / dbHealthChecker feed only SystemHandler (the /doctor
// route) and are themselves application-layer ports.
func NewModule(
	cfg DoctorConfig,
	log *zap.Logger,
	toolChecker appassets.ToolChecker,
	processRunner appassets.ProcessRunner,
	dbHealthChecker appassets.DBHealthChecker,
	driveOps DriveAdminOps,
	reconciler Reconciler,
) *Module {
	return &Module{
		name: "system",
		log:  log,
		handler: NewSystemHandler(
			cfg, log,
			toolChecker, processRunner, dbHealthChecker,
		),
		driveHandler: NewDriveHandler(
			reconciler,
			driveOps,
		),
	}
}

// Name returns the module name.
func (m *Module) Name() string { return m.name }

// Enabled always returns true for the system module.
func (m *Module) Enabled() bool { return true }

// RegisterRoutes registers /system/* and /drive/* routes.
//
// Both sub-groups live under the same protected router group, so
// they share Auth + RateLimit + WorkspaceScope. Public callers
// only see /api/system/doctor if explicitly granted admin via
// the workspace scope middleware.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	systemGroup := rg.Group("/system")
	{
		systemGroup.GET("/doctor", m.handler.Doctor)
	}

	// /internal/slug — absorbed from the retired UtilityModule
	// (2026-08-23 Cleanup Day). The canonical slug endpoint lives
	// under System, not as a standalone module.
	internalGroup := rg.Group("/internal")
	{
		internalGroup.GET("/slug", m.handler.Slugify)
	}

	driveGroup := rg.Group("/drive")
	{
		m.driveHandler.RegisterRoutes(driveGroup)
	}
}

type Dependencies struct {
	Config        DoctorConfig
	Logger        *zap.Logger
	ToolChecker   appassets.ToolChecker
	ProcessRunner appassets.ProcessRunner
	DBHealth      appassets.DBHealthChecker
	DriveOps      DriveAdminOps
	Reconciler    Reconciler
}

func (m *Module) Build(ctx httpserver.BuildContext) (httpserver.RuntimeModule, error) {
	return httpserver.RuntimeModuleFor("system", "/api/system", m)
}
