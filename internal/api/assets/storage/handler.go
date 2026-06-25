// Package storage provides the thin HTTP transport for storage operations
// (list, move, create-folder, rename). All business logic is delegated
// to application/assets/storage.Service.
package storage

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the thin HTTP transport for storage operations.
type Handler struct {
	svc         *appstorage.Service
	log         *zap.Logger
	jobsSvc     jobdomain.Service
	catalogSync *catalogsync.Service
}

// NewHandler creates a storage Handler.
// jobs and catalogSync are accepted for forward-compatibility with
// future route handlers (sync-after-move, job-enqueue-on-rename)
// but are not yet consumed by the current route set.
func NewHandler(svc *appstorage.Service, jobs jobdomain.Service, catalogSync *catalogsync.Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log, jobsSvc: jobs, catalogSync: catalogSync}
}

// RegisterRoutes registers storage routes under the given group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/drive/files", h.ListFiles)
	r.POST("/drive/move-file", h.MoveFile)
	r.POST("/drive/create-folder", h.CreateFolder)
	r.POST("/drive/rename", h.RenameFile)
	r.POST("/sync-drive-folder", h.SyncDriveFolder)
}

// ── ListFiles (GET /drive/files) ─────────────────────────────────

// listFilesRequest is the query for listing Drive folder contents.
type listFilesRequest struct {
	FolderID string `form:"folder_id" json:"folder_id"`
}

func (h *Handler) ListFiles(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "storage service not wired")
		return
	}
	var req listFilesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid query parameters: "+err.Error())
		return
	}
	if req.FolderID == "" {
		apiutil.BadRequest(c, "folder_id is required")
		return
	}
	result, err := h.svc.ListFiles(c.Request.Context(), req.FolderID)
	if err != nil {
		h.log.Error("list files failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}

// ── MoveFile (POST /drive/move) ──────────────────────────────────

// moveFileRequest is the payload for moving a Drive file.
type moveFileRequest struct {
	FileID       string `json:"file_id"`
	FromFolderID string `json:"from_folder_id"`
	ToFolderID   string `json:"to_folder_id"`
}

func (h *Handler) MoveFile(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "storage service not wired")
		return
	}
	req, ok := apiutil.BindJSON[moveFileRequest](c)
	if !ok {
		return
	}
	if err := h.svc.MoveFile(c.Request.Context(), req.FileID, req.FromFolderID, req.ToFolderID); err != nil {
		h.log.Error("move file failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, map[string]string{"status": "moved"})
}

// ── CreateFolder (POST /drive/create-folder) ─────────────────────

// createFolderRequest is the payload for creating a Drive folder.
type createFolderRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

func (h *Handler) CreateFolder(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "storage service not wired")
		return
	}
	req, ok := apiutil.BindJSON[createFolderRequest](c)
	if !ok {
		return
	}
	folderID, err := h.svc.CreateFolder(c.Request.Context(), req.Name, req.ParentID)
	if err != nil {
		h.log.Error("create folder failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, map[string]string{"folder_id": folderID})
}

// ── RenameFile (POST /drive/rename) ──────────────────────────────

// renameFileRequest is the payload for renaming a Drive file.
type renameFileRequest struct {
	FileID  string `json:"file_id"`
	NewName string `json:"new_name"`
}

func (h *Handler) RenameFile(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "storage service not wired")
		return
	}
	req, ok := apiutil.BindJSON[renameFileRequest](c)
	if !ok {
		return
	}
	if err := h.svc.RenameFile(c.Request.Context(), req.FileID, req.NewName); err != nil {
		h.log.Error("rename file failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, map[string]string{"status": "renamed"})
}
