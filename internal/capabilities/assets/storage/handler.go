// Package storage provides the thin HTTP transport for Drive folder sync
// operations. All business logic is delegated to catalogsync.Service.
//
// QDRANT-001 (June 2026) closure: this package also exposes
// RegisterInternalMediaRoutes (a separate RegisterRoutes surface that
// mounts under /internal/v1/media/ for the server-to-server variant).
// The api.Router uses a narrow MediaInternalRouter interface to keep
// the registration surface minimal and avoid leaking storage.Handler
// into non-storage callers.
//
// Drive CRUD operations (list, create-folder, move, rename) were moved
// to the unified /api/drive/* admin surface in api/system/handler_drive.go
// (Blocco A1 consolidation, June 2026).
package storage

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Handler is the thin HTTP transport for Drive folder sync operations.
type Handler struct {
	log         *zap.Logger
	jobsSvc     jobs.Service
	catalogSync *catalogsync.Service
}

// NewHandler creates a storage Handler for Drive folder sync.
func NewHandler(jobs jobs.Service, catalogSync *catalogsync.Service, log *zap.Logger) *Handler {
	return &Handler{log: log, jobsSvc: jobs, catalogSync: catalogSync}
}

// RegisterRoutes registers storage routes under the given group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/sync", h.SyncDriveFolder)
}

// RegisterInternalMediaRoutes registers the QDRANT-001 server-to-server
// surface under /internal/v1/media/*. Currently exposes:
//
//	POST /internal/v1/media/sync
//
// Idempotency-Key header is REQUIRED. The auth surface comes from
// the upstream middleware.WorkerAuth mounted by api.Router.Setup() —
// callers authenticate as services/workers (AdminBearer is rejected).
func (h *Handler) RegisterInternalMediaRoutes(r *gin.RouterGroup) {
	if r == nil {
		return
	}
	mediaGroup := r.Group("/media")
	mediaGroup.POST("/sync", h.InternalSyncDriveFolder)
}
