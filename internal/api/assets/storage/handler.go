// Package storage provides thin HTTP transport for Drive file/folder
// management, local-to-drive uploads, and Drive folder sync.
// All business logic is delegated to application/assets/storage.Service.
//
// Route summary (mounted under /api/media by the Assets module):
//
//	POST /storage/files          – list files in a Drive folder
//	POST /storage/files/move     – move files between Drive folders
//	POST /storage/files/rename   – rename a Drive file or folder
//	POST /storage/folders        – create a Drive folder
//	POST /storage/local-to-drive – scan a local MP4 tree and enqueue bulk upload
//	POST /storage/sync-drive-folder – dispatch an async Drive folder sync job
package storage

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Handler is the thin HTTP transport for storage operations.
type Handler struct {
	svc         *appstorage.Service
	jobsSvc     *jobservice.Service
	catalogSync *catalogsync.Service
	log         *zap.Logger
}

// NewHandler creates a storage Handler.
func NewHandler(svc *appstorage.Service, jobsSvc *jobservice.Service, catalogSync *catalogsync.Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, jobsSvc: jobsSvc, catalogSync: catalogSync, log: log}
}

// RegisterRoutes registers storage routes under the given group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Drive file/folder management
	r.POST("/storage/files", h.ListFiles)
	r.POST("/storage/files/move", h.MoveFiles)
	r.POST("/storage/files/rename", h.RenameFile)
	r.POST("/storage/folders", h.CreateFolder)

	// Bulk operations (pre-existing handlers)
	r.POST("/storage/local-to-drive", h.LocalToDrive)
	r.POST("/storage/sync-drive-folder", h.SyncDriveFolder)
}

// ── ListFiles (POST /storage/files) ────────────────────────────────

type listFilesRequest struct {
	FolderID string `json:"folder_id" binding:"required"`
}

func (h *Handler) ListFiles(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "storage service not wired")
		return
	}
	req, ok := apiutil.BindJSON[listFilesRequest](c)
	if !ok {
		return
	}
	result, err := h.svc.ListFiles(c.Request.Context(), appstorage.ListFilesRequest{
		FolderID: req.FolderID,
	})
	if err != nil {
		h.log.Error("list files failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}

// ── MoveFiles (POST /storage/files/move) ───────────────────────────

type moveFilesRequest struct {
	FileIDs      []string `json:"file_ids" binding:"required"`
	FromFolderID string   `json:"from_folder_id"`
	ToFolderID   string   `json:"to_folder_id" binding:"required"`
}

func (h *Handler) MoveFiles(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "storage service not wired")
		return
	}
	req, ok := apiutil.BindJSON[moveFilesRequest](c)
	if !ok {
		return
	}
	result, err := h.svc.MoveFiles(c.Request.Context(), appstorage.MoveFilesRequest{
		FileIDs:      req.FileIDs,
		FromFolderID: req.FromFolderID,
		ToFolderID:   req.ToFolderID,
	})
	if err != nil {
		h.log.Error("move files failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	// After moving files, trigger a catalog sync in the background so
	// downstream consumers (voiceover, search, clips) see the new layout.
	if h.catalogSync != nil {
		syncCtx := context.WithoutCancel(c.Request.Context())
		concurrent.SafeGo("storage-catalog-sync", func() {
			if _, err := h.catalogSync.SyncAll(syncCtx); err != nil {
				h.log.Warn("post-move catalog sync failed", zap.Error(err))
			}
		})
	}

	apiutil.OK(c, result)
}

// ── RenameFile (POST /storage/files/rename) ────────────────────────

type renameFileRequest struct {
	FileID  string `json:"file_id" binding:"required"`
	NewName string `json:"new_name" binding:"required"`
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
	result, err := h.svc.RenameFile(c.Request.Context(), appstorage.RenameFileRequest{
		FileID:  req.FileID,
		NewName: req.NewName,
	})
	if err != nil {
		h.log.Error("rename file failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}

// ── CreateFolder (POST /storage/folders) ───────────────────────────

type createFolderRequest struct {
	Name     string `json:"name" binding:"required"`
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
	result, err := h.svc.CreateFolder(c.Request.Context(), appstorage.CreateFolderRequest{
		Name:     req.Name,
		ParentID: req.ParentID,
	})
	if err != nil {
		h.log.Error("create folder failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}
