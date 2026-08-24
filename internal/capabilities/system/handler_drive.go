// Package system (api/system) — handler_drive.go holds the DriveHandler
// (reconcile/cleanup/folders/move/resolve-by-id) as a second receiver in
// the system package. Wave 14 PR4 close (June 24, 2026): this file absorbed
// the legacy internal/api/drive/handler.go when the standalone
// internal/api/drive/ directory was eliminated.
//
// The route prefix `/drive` is mounted on the system Module's group
// in module.go::Module.RegisterRoutes, sibling to /system/doctor.
// All exports are reconciled with the canonical system handler:
// package `system` now owns both SystemHandler (doctor) and
// DriveHandler (admin/Drive ops).
//
// PR4-cleanup delta (June 24, 2026): the previous concrete deps
// (*drive.Uploader) were replaced with `Reconciler` +
// `DriveAdminOps` port interfaces declared right here.
// The concrete adapters live in `internal/app/system_adapters.go`
// (composition root). This keeps the api layer free of
// `internal/infrastructure/*` imports per AGENTS.md Pattern 8.
package system

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clips" // clips.ExtractDriveFolderID — URL/ID parsing helper
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ── Port interfaces (AGENTS.md Pattern 0 / Wiki §14) ─────────────────────────

// ReconcileResult is the JSON-shaped summary returned by Reconciler.Reconcile.
// Defined here to keep the api package free of infrastructure imports
// (AGENTS.md Pattern 8).
type ReconcileResult struct {
	Deleted int `json:"deleted"`
	Kept    int `json:"kept"`
}

// Reconciler is the port for Drive → SQLite reconciliation flows.
// Wired at composition time in internal/app.
type Reconciler interface {
	Reconcile(ctx context.Context, source, rootFolderID string, dryRun bool) (*ReconcileResult, error)
}

// ErrReconcilerNotWired is returned at the HTTP boundary when the real
// Drive-to-SQLite reconciler is unavailable. The route remains explicit but
// fail-closed instead of reporting a successful empty reconciliation.
var ErrReconcilerNotWired = errors.New("system: drive reconciler not wired (godlike/07 fail-closed)")

// DriveAdminOps is the port for the small set of Drive operations the
// admin handlers need: create folders, move files, list files, rename,
// resolve ID metadata.
// It is satisfied at composition time by the adapter wrapping
// `*drive.Uploader`. The Google Files.Get round-trip (which the previous
// code spelled out inline) is encapsulated inside ResolveFileInfo so the
// api package never reaches into the Google Drive SDK directly.
type DriveAdminOps interface {
	GetOrCreateFolder(ctx context.Context, folderName, parentID string) (string, error)
	MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error
	ListFiles(ctx context.Context, folderID string) ([]DriveFileInfoDTO, error)
	RenameFile(ctx context.Context, fileID, newName string) error
	ResolveFileInfo(ctx context.Context, fileID string) (ResolveByIDsItem, error)
}

// DriveFileInfoDTO is the canonical file descriptor returned by ListFiles
// on the DriveAdminOps port. Mirrors drive.DriveFileInfo sans infrastructure
// imports (AGENTS.md Pattern 8).
type DriveFileInfoDTO struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	MimeType       string   `json:"mime_type"`
	WebViewLink    string   `json:"web_view_link,omitempty"`
	WebContentLink string   `json:"web_content_link,omitempty"`
	Parents        []string `json:"parents,omitempty"`
}

// ── Handler ─────────────────────────────────────────────────────────────────

// DriveHandler handles Drive admin ops (files, folders, move, rename,
// reconcile, cleanup, resolve-by-id). It is constructed in app.WireRegistry
// and mounted by system.Module on the /drive sub-group.
type DriveHandler struct {
	reconciler Reconciler
	driveOps   DriveAdminOps
}

// NewDriveHandler creates a new DriveHandler.
func NewDriveHandler(reconciler Reconciler, driveOps DriveAdminOps) *DriveHandler {
	return &DriveHandler{
		reconciler: reconciler,
		driveOps:   driveOps,
	}
}

// RegisterRoutes registers the Drive routes on the supplied RouterGroup.
// The system Module mounts this handler on a `/drive` sub-group so the
// resulting URLs are /api/drive/...
func (h *DriveHandler) RegisterRoutes(r *gin.RouterGroup) {
	zap.L().Info("DriveHandler.RegisterRoutes called", zap.String("handler_addr", fmt.Sprintf("%p", h)))
	r.GET("/files", h.ListFiles)
	r.POST("/folders", h.CreateFolders)
	r.POST("/move", h.MoveFile)
	r.POST("/rename", h.RenameFile)
	r.POST("/reconcile", h.Reconcile)
	r.POST("/cleanup", h.Cleanup)
	r.POST("/resolve-by-id", h.ResolveByIDs)
}

// CreateFoldersRequest represents a request to create multiple folders on Drive.
type CreateFoldersRequest struct {
	ParentID string   `json:"parent_id" binding:"required"`
	Folders  []string `json:"folders" binding:"required"`
}

// CreateFolders creates multiple subfolders inside a parent Drive folder.
// POST /api/drive/folders
// Body: { "parent_id": "root-folder-id", "folders": ["ziwe", "TeamCoco"] }
// Response: { "ok": true, "created": {"ziwe": "folder-id-1", "TeamCoco": "folder-id-2"} }
func (h *DriveHandler) CreateFolders(c *gin.Context) {
	if h.driveOps == nil {
		apiutil.Error(c, 500, "drive uploader not configured")
		return
	}

	var req CreateFoldersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if len(req.Folders) == 0 {
		apiutil.BadRequest(c, "folders list is empty")
		return
	}

	parentID := clips.ExtractDriveFolderID(strings.TrimSpace(req.ParentID))
	if parentID == "" {
		apiutil.BadRequest(c, "parent_id is required")
		return
	}

	ctx := c.Request.Context()
	created := make(map[string]string, len(req.Folders))
	var errs []string

	for _, folderName := range req.Folders {
		if folderName == "" {
			continue
		}
		folderID, err := h.driveOps.GetOrCreateFolder(ctx, folderName, parentID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", folderName, err))
			continue
		}
		created[folderName] = folderID
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"parent_id":     req.ParentID,
		"created":       created,
		"created_count": len(created),
		"errors":        errs,
		"error_count":   len(errs),
	})
}

// Reconcile checks for mismatches between SQLite and Google Drive.
// Body: { "source": "artlist", "root_folder_id": "xxx", "dry_run": true }
func (h *DriveHandler) Reconcile(c *gin.Context) {
	if h.reconciler == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, ErrReconcilerNotWired.Error())
		return
	}

	var req struct {
		Source       string `json:"source"`
		RootFolderID string `json:"root_folder_id"`
		DryRun       bool   `json:"dry_run"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body")
		return
	}

	ctx := c.Request.Context()
	result, err := h.reconciler.Reconcile(ctx, req.Source, req.RootFolderID, req.DryRun)
	if err != nil {
		apiutil.Error(c, 500, err.Error())
		return
	}

	apiutil.OK(c, result)
}

// Cleanup performs orphan removal.
// Body: { "source": "artlist", "root_folder_id": "xxx" }
func (h *DriveHandler) Cleanup(c *gin.Context) {
	if h.reconciler == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, ErrReconcilerNotWired.Error())
		return
	}

	var req struct {
		Source       string `json:"source"`
		RootFolderID string `json:"root_folder_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body")
		return
	}

	ctx := c.Request.Context()
	result, err := h.reconciler.Reconcile(ctx, req.Source, req.RootFolderID, false)
	if err != nil {
		apiutil.Error(c, 500, err.Error())
		return
	}

	apiutil.OK(c, result)
}

// MoveFileRequest represents a request to move files between Drive folders.
type MoveFileRequest struct {
	FileIDs      []string `json:"file_ids" binding:"required"`
	FromFolderID string   `json:"from_folder_id"`
	ToFolderID   string   `json:"to_folder_id" binding:"required"`
}

// MoveFile moves one or more files from one Drive folder to another.
// POST /api/drive/move
func (h *DriveHandler) MoveFile(c *gin.Context) {
	if h.driveOps == nil {
		apiutil.Error(c, 500, "drive uploader not configured")
		return
	}
	var req MoveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	ctx := c.Request.Context()
	var moved int
	var errs []string
	for _, fid := range req.FileIDs {
		if err := h.driveOps.MoveFile(ctx, fid, req.FromFolderID, req.ToFolderID); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", fid, err))
			continue
		}
		moved++
	}
	apiutil.OK(c, gin.H{
		"ok":           true,
		"moved":        moved,
		"errors":       errs,
		"error_count":  len(errs),
		"to_folder_id": req.ToFolderID,
	})
}

// ResolveByIDsRequest represents a request to resolve one or more Drive folder/file IDs
// (or full Drive URLs) into their human-readable metadata on Google Drive.
type ResolveByIDsRequest struct {
	IDs []string `json:"ids" binding:"required,min=1"`
}

// ResolveByIDsItem is a single resolved entry in the response.
type ResolveByIDsItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	MimeType    string   `json:"mime_type,omitempty"`
	Parents     []string `json:"parents,omitempty"`
	WebViewLink string   `json:"web_view_link,omitempty"`
	Size        int64    `json:"size,omitempty"`
	Trashed     bool     `json:"trashed,omitempty"`
}

// resolveBatchConcurrency caps parallel Files.Get calls. Drive's Files.Get is
// rate-limited at ~10 req/s per user (see core/maintenance/service.go); 8 stays
// safely below that ceiling while still being faster than serial for 9-100 IDs.
const resolveBatchConcurrency = 8
const resolveMaxBatchSize = 100

// ResolveByIDs resolves one or more Drive folder/file IDs (or full Drive URLs) to their metadata.
// Useful for debugging: given a list of IDs, returns name + parent chain + trashed status.
// Fans out Files.Get calls with bounded concurrency (resolveBatchConcurrency) to stay
// under Drive's per-user rate limit while staying responsive for 9-100 ID batches.
//
//	POST /api/drive/resolve-by-id
//	Body: { "ids": ["1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo", "https://drive.google.com/drive/folders/..."] }
//	Response: { "ok": true, "resolved": [{id,name,mime_type,parents,trashed,...}], "errors": ["id: msg"], "resolved_count": N, "error_count": M }
func (h *DriveHandler) ResolveByIDs(c *gin.Context) {
	if h.driveOps == nil {
		apiutil.Error(c, 500, "drive uploader not configured")
		return
	}

	var req ResolveByIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if len(req.IDs) > resolveMaxBatchSize {
		apiutil.BadRequest(c, fmt.Sprintf("ids list exceeds max batch size of %d", resolveMaxBatchSize))
		return
	}

	ctx := c.Request.Context()

	// Bounded concurrency via buffered semaphore channel — Drive is
	// rate-limited at ~10 req/s per user, so 8 keeps us safely below it.
	sem := make(chan struct{}, resolveBatchConcurrency)

	// Pre-allocate result slices keyed by original index so concurrent writes
	// (each goroutine owns one slot) never race. errorsByIdx[i] == "" means
	// success for that slot.
	resolved := make([]ResolveByIDsItem, len(req.IDs))
	errorsByIdx := make([]string, len(req.IDs))
	var wg sync.WaitGroup

	for i, raw := range req.IDs {
		id := clips.ExtractDriveFolderID(strings.TrimSpace(raw))
		if id == "" {
			errorsByIdx[i] = fmt.Sprintf("empty id in input: %q", raw)
			continue
		}

		wg.Add(1)
		go func(idx int, folderID string) {
			defer wg.Done()

			// Acquire concurrency slot; release on exit.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errorsByIdx[idx] = fmt.Sprintf("%s: %v", folderID, ctx.Err())
				return
			}

			item, err := h.driveOps.ResolveFileInfo(ctx, folderID)
			if err != nil {
				errorsByIdx[idx] = fmt.Sprintf("%s: %v", folderID, err)
				return
			}
			resolved[idx] = item
		}(i, id)
	}

	wg.Wait()

	// Compact resolved/errors: skip empty rows so the response only
	// contains real entries (preserving original index order).
	out := make([]ResolveByIDsItem, 0, len(req.IDs))
	errList := make([]string, 0, len(req.IDs))
	for i, item := range resolved {
		if errorsByIdx[i] != "" {
			errList = append(errList, errorsByIdx[i])
			continue
		}
		// Skip slots that were never filled because of empty extraction
		// (handled by errorsByIdx above) or context cancellation.
		if item.ID == "" {
			continue
		}
		out = append(out, item)
	}

	apiutil.OK(c, gin.H{
		"ok":             true,
		"resolved":       out,
		"resolved_count": len(out),
		"errors":         errList,
		"error_count":    len(errList),
	})
}

// ── ListFiles (GET /drive/files) ────────────────────────────────────────────

// listFilesQuery is the query for listing Drive folder contents.
type listFilesQuery struct {
	FolderID string `form:"folder_id" json:"folder_id"`
}

// ListFiles lists all non-trashed files in a Drive folder.
// GET /api/drive/files?folder_id=xxx
func (h *DriveHandler) ListFiles(c *gin.Context) {
	if h.driveOps == nil {
		apiutil.Error(c, 500, "drive uploader not configured")
		return
	}
	var req listFilesQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid query parameters: "+err.Error())
		return
	}
	if req.FolderID == "" {
		apiutil.BadRequest(c, "folder_id is required")
		return
	}
	files, err := h.driveOps.ListFiles(c.Request.Context(), req.FolderID)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{
		"ok":    true,
		"files": files,
		"count": len(files),
	})
}

// ── RenameFile (POST /drive/rename) ─────────────────────────────────────────

// RenameFileRequest is the payload for renaming a Drive file or folder.
type RenameFileRequest struct {
	FileID  string `json:"file_id" binding:"required"`
	NewName string `json:"new_name" binding:"required"`
}

// RenameFile renames a file or folder on Drive.
// POST /api/drive/rename
func (h *DriveHandler) RenameFile(c *gin.Context) {
	if h.driveOps == nil {
		apiutil.Error(c, 500, "drive uploader not configured")
		return
	}
	var req RenameFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	if err := h.driveOps.RenameFile(c.Request.Context(), req.FileID, req.NewName); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true, "status": "renamed"})
}
