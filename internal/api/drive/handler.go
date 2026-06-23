package drive

import (
	"fmt"
	"strings"
	"sync"

	clipsources "github.com/Marcuss-ops/PipelineGen/internal/api/clips"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type DriveHandler struct {
	reconcileSvc  *drivecleanup.Service
	driveUploader *drive.Uploader
}

func NewDriveHandler(reconcileSvc *drivecleanup.Service, driveUploader *drive.Uploader) *DriveHandler {
	return &DriveHandler{reconcileSvc: reconcileSvc, driveUploader: driveUploader}
}

func (h *DriveHandler) RegisterRoutes(r *gin.RouterGroup) {
	zap.L().Info("RegisterRoutes called", zap.String("handler_addr", fmt.Sprintf("%p", h)))
	r.POST("/reconcile", h.Reconcile)
	r.POST("/cleanup", h.Cleanup)
	r.POST("/folders", h.CreateFolders)
	r.POST("/move", h.MoveFile)
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
	if h.driveUploader == nil {
		api.Error(c, 500, "drive uploader not configured")
		return
	}

	var req CreateFoldersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if len(req.Folders) == 0 {
		api.BadRequest(c, "folders list is empty")
		return
	}

	parentID := clipsources.ExtractDriveFolderID(strings.TrimSpace(req.ParentID))
	if parentID == "" {
		api.BadRequest(c, "parent_id is required")
		return
	}

	ctx := c.Request.Context()
	created := make(map[string]string, len(req.Folders))
	var errors []string

	for _, folderName := range req.Folders {
		if folderName == "" {
			continue
		}
		folderID, err := h.driveUploader.GetOrCreateFolder(ctx, folderName, parentID)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", folderName, err))
			continue
		}
		created[folderName] = folderID
	}

	api.OK(c, gin.H{
		"ok":            true,
		"parent_id":     req.ParentID,
		"created":       created,
		"created_count": len(created),
		"errors":        errors,
		"error_count":   len(errors),
	})
}

// Reconcile checks for mismatches between SQLite and Google Drive.
// Body: { "source": "artlist", "root_folder_id": "xxx", "dry_run": true }
func (h *DriveHandler) Reconcile(c *gin.Context) {
	if h.reconcileSvc == nil {
		api.Error(c, 500, "reconcile service not configured")
		return
	}

	var req struct {
		Source       string `json:"source"`
		RootFolderID string `json:"root_folder_id"`
		DryRun       bool   `json:"dry_run"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid request body")
		return
	}

	ctx := c.Request.Context()
	result, err := h.reconcileSvc.Reconcile(ctx, req.Source, req.RootFolderID, req.DryRun)
	if err != nil {
		api.Error(c, 500, err.Error())
		return
	}

	api.OK(c, result)
}

// Cleanup performs orphan removal.
// Body: { "source": "artlist", "root_folder_id": "xxx" }
func (h *DriveHandler) Cleanup(c *gin.Context) {
	if h.reconcileSvc == nil {
		api.Error(c, 500, "reconcile service not configured")
		return
	}

	var req struct {
		Source       string `json:"source"`
		RootFolderID string `json:"root_folder_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid request body")
		return
	}

	ctx := c.Request.Context()
	result, err := h.reconcileSvc.Reconcile(ctx, req.Source, req.RootFolderID, false)
	if err != nil {
		api.Error(c, 500, err.Error())
		return
	}

	api.OK(c, result)
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
	if h.driveUploader == nil {
		api.Error(c, 500, "drive uploader not configured")
		return
	}
	var req MoveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	ctx := c.Request.Context()
	var moved int
	var errs []string
	for _, fid := range req.FileIDs {
		if err := h.driveUploader.MoveFile(ctx, fid, req.FromFolderID, req.ToFolderID); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", fid, err))
			continue
		}
		moved++
	}
	api.OK(c, gin.H{
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
	if h.driveUploader == nil || h.driveUploader.Service == nil {
		api.Error(c, 500, "drive uploader not configured")
		return
	}

	var req ResolveByIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	if len(req.IDs) > resolveMaxBatchSize {
		api.BadRequest(c, fmt.Sprintf("ids list exceeds max batch size of %d", resolveMaxBatchSize))
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
		id := clipsources.ExtractDriveFolderID(strings.TrimSpace(raw))
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

			file, err := h.driveUploader.Service.Files.Get(folderID).
				Fields("id, name, mimeType, parents, trashed, webViewLink, size").
				Context(ctx).Do()
			if err != nil {
				errorsByIdx[idx] = fmt.Sprintf("%s: %v", folderID, err)
				return
			}
			resolved[idx] = ResolveByIDsItem{
				ID:          file.Id,
				Name:        file.Name,
				MimeType:    file.MimeType,
				Parents:     file.Parents,
				WebViewLink: file.WebViewLink,
				Size:        file.Size,
				Trashed:     file.Trashed,
			}
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

	api.OK(c, gin.H{
		"ok":             true,
		"resolved":       out,
		"resolved_count": len(out),
		"errors":         errList,
		"error_count":    len(errList),
	})
}
