// Package nonops — handler_reprocess.go: ReprocessClip endpoint
// extracted from clips/handler_reprocess.go per PR-CLIPS-NONOPS-EXTRACT
// (July 2026). The method's body is byte-equivalent with the
// pre-extraction version — only the receiver type changed from
// *Handler to *NonOpsHandler.
package nonops

import (
	"strings"

	"github.com/gin-gonic/gin"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ReprocessClip reprocesses a clip (download/process/upload).
func (h *NonOpsHandler) ReprocessClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	var req struct {
		Force       bool  `json:"force"`
		UploadDrive bool  `json:"upload_drive"`
		Normalize   *bool `json:"normalize"`
	}
	_ = c.ShouldBindJSON(&req)

	result, err := h.reprocessUC.Execute(c.Request.Context(), appclips.ReprocessRequest{
		ClipID:      clipID,
		Source:      source,
		Force:       req.Force,
		UploadDrive: req.UploadDrive,
		Normalize:   req.Normalize,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			apiutil.NotFound(c, err.Error())
		} else {
			apiutil.InternalError(c, err)
		}
		return
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"source":        result.Source,
		"clip_id":       result.ClipID,
		"status":        result.Status,
		"local_path":    result.LocalPath,
		"legacy_file_md5":     result.LegacyFileMD5,
		"drive_link":    result.DriveLink,
		"download_link": result.DownloadLink,
		"processed_at":  result.ProcessedAt,
	})
}
