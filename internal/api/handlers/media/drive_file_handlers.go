package media

import (
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/apiutil"
	driveutil "velox/go-master/pkg/drive"
)

// DeleteDriveFileRequest represents a request to delete/trash a clip by Drive file ID or link.
type DeleteDriveFileRequest struct {
	Source    string `json:"source,omitempty"`
	DriveLink string `json:"drive_link"`
	FileID    string `json:"file_id"`
	DryRun    bool   `json:"dry_run"`
	Confirm   bool   `json:"confirm"`
	Mode      string `json:"mode"` // "trash" or "delete"
}

// DeleteDriveFileResult represents the result of a drive file delete/trash operation.
type DeleteDriveFileResult struct {
	OK           bool   `json:"ok"`
	Source       string `json:"source,omitempty"`
	ClipID       string `json:"clip_id,omitempty"`
	FileID       string `json:"file_id"`
	DriveLink    string `json:"drive_link,omitempty"`
	FoundDB      bool   `json:"found_db"`
	DryRun       bool   `json:"dry_run"`
	Action       string `json:"action,omitempty"`
	DriveDeleted bool   `json:"drive_deleted,omitempty"`
	DBDeleted    bool   `json:"db_deleted,omitempty"`
	Error        string `json:"error,omitempty"`
}

// TrashByDriveFile trashes a clip by Drive file ID or link.
func (h *CommonHandler) TrashByDriveFile(c *gin.Context) {
	source := c.Param("source")

	var req DeleteDriveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body")
		return
	}
	
	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" && req.DriveLink != "" {
		fileID = driveutil.FileIDFromLink(req.DriveLink)
	}

	if err := h.deletionSvc.DeleteByDriveFile(c.Request.Context(), fileID, source, false); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true, "action": "trashed", "file_id": fileID})
}

// DeleteByDriveFile permanently deletes a clip by Drive file ID or link.
func (h *CommonHandler) DeleteByDriveFile(c *gin.Context) {
	source := c.Param("source")

	var req DeleteDriveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body")
		return
	}

	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" && req.DriveLink != "" {
		fileID = driveutil.FileIDFromLink(req.DriveLink)
	}

	if err := h.deletionSvc.DeleteByDriveFile(c.Request.Context(), fileID, source, true); err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"ok": true, "action": "deleted", "file_id": fileID})
}
