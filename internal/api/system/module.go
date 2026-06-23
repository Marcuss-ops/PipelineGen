// Package system — module.go is the composition root for the
// system module's /system + /drive routes.
//
// Wave 14 close (June 2026): the system Module absorbed the
// standalone internal/api/drive/handler.go as a second receiver
// (DriveHandler). The system Module now mounts two sub-groups
// sharing the same protected router group:
//
//   /system/doctor     — admin/doctor diagnostics
//   /drive/{reconcile,
//           cleanup,
//           folders,
//           move,
//           resolve-by-id}
//
// Both sub-groups inherit Auth + RateLimit + WorkspaceScope from
// the protected group mounted in routes.go.
package system

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// Module handles system diagnostic routes plus Drive admin ops.
type Module struct {
	name         string
	cfg          *config.Config
	log          *zap.Logger
	handler      *SystemHandler
	driveHandler *DriveHandler
}

// NewModule creates a new system module.
//
// driveUploader is optional; when nil, drive routes return 503 with
// "drive uploader not configured". reconcileSvc is also optional; the
// reconcile/cleanup routes return 503 when it is nil.
func NewModule(
	cfg *config.Config,
	log *zap.Logger,
	toolChecker appassets.ToolChecker,
	processRunner appassets.ProcessRunner,
	dbHealthChecker appassets.DBHealthChecker,
	driveUploader *drive.Uploader,
	reconcileSvc *drivecleanup.Service,
) *Module {
	return &Module{
		name: "system",
		cfg:  cfg,
		log:  log,
		handler: NewSystemHandler(cfg, log, toolChecker, processRunner, dbHealthChecker),
		driveHandler: NewDriveHandler(
			reconcileSvc,
			driveUploader,
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

	driveGroup := rg.Group("/drive")
	{
		m.driveHandler.RegisterRoutes(driveGroup)
	}
}
