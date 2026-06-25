package storage

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// SyncDriveFolderRequest is the JSON body for POST /api/media/sync-drive-folder.
type SyncDriveFolderRequest struct {
	DriveFolderID string `json:"drive_folder_id"` // Google Drive folder ID (required)
	Source        string `json:"source"`          // "youtube", "stock", "artlist", "drive" (default "drive")
	Name          string `json:"name,omitempty"`  // Human-readable name (optional, falls back to folder ID)
	MediaType     string `json:"media_type"`      // "video", "clip", "stock" (default "clip")
}

// SyncDriveFolder handles POST /api/media/sync-drive-folder.
//
// Dispatches an async Drive folder sync job and returns 202 with job_id.
// The job scans the folder recursively, creates media_assets records, and
// triggers automatic vector indexing. Clients poll GET /api/jobs/{id} for status.
//
// Body:
//
//	{
//	  "drive_folder_id": "1ll2RlTaAbhnaLkAjEDBg41lAXUyo-zJ2",
//	  "source": "youtube",
//	  "name": "My YouTube Clips",
//	  "media_type": "video"
//	}
//
// Returns 202 with job_id for async tracking.
func (h *Handler) SyncDriveFolder(c *gin.Context) {
	var req SyncDriveFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	folderID := strings.TrimSpace(req.DriveFolderID)
	if folderID == "" {
		apiutil.BadRequest(c, "drive_folder_id is required")
		return
	}

	if h.catalogSync == nil {
		apiutil.InternalError(c, fmt.Errorf("catalog sync service not configured"))
		return
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "drive"
	}

	mediaType := strings.TrimSpace(req.MediaType)
	if mediaType == "" {
		mediaType = "clip"
	}

	h.log.Info("dispatching async drive folder sync",
		zap.String("drive_folder_id", folderID),
		zap.String("source", source),
		zap.String("name", req.Name),
		zap.String("media_type", mediaType),
	)

	// QDRANT-001 closure: route through the shared dispatch helper so the
	// admin (/api) and server-to-server (/internal/v1) variants stay in
	// lockstep on payload schema, job type, and error envelope.
	out, err := h.dispatchDriveFolderSync(c, &DispatchSyncInput{
		FolderID:  folderID,
		Source:    source,
		Name:      req.Name,
		MediaType: mediaType,
		IdemKey:   "", // admin variant: no client-supplied Idempotency-Key
		Caller:    "api_admin",
	})
	if err != nil {
		h.log.Error("failed to enqueue drive folder sync job",
			zap.String("drive_folder_id", folderID),
			zap.Error(err),
		)
		apiutil.InternalError(c, fmt.Errorf("failed to enqueue drive folder sync: %w", err))
		return
	}

	c.JSON(202, gin.H{
		"ok":              true,
		"job_id":          out.JobID,
		"drive_folder_id": folderID,
		"source":          source,
		"name":            req.Name,
		"message":         "Drive folder sync dispatched. Poll GET /api/jobs/" + out.JobID + " for status.",
	})
}
