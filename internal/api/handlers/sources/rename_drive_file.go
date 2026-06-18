package sources

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// RenameDriveFileRequest is the JSON body for POST /api/media/rename-drive-file.
type RenameDriveFileRequest struct {
	FileID  string `json:"file_id"`
	NewName string `json:"new_name"`
}

// RenameDriveFile handles POST /api/media/rename-drive-file.
//
// Renames a file or folder on Google Drive.
func (h *Handler) RenameDriveFile(c *gin.Context) {
	var req RenameDriveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" {
		apiutil.BadRequest(c, "file_id is required")
		return
	}
	newName := strings.TrimSpace(req.NewName)
	if newName == "" {
		apiutil.BadRequest(c, "new_name is required")
		return
	}

	if h.driveUploader == nil {
		apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
		return
	}

	if err := h.driveUploader.RenameFile(c.Request.Context(), fileID, newName); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"file_id":  fileID,
		"new_name": newName,
		"message":  fmt.Sprintf("renamed to %q", newName),
	})
}
