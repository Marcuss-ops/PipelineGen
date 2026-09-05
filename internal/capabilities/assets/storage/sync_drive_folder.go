package storage

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/transport"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// SyncDriveFolderRequest is the JSON body for POST /api/media/sync.
type SyncDriveFolderRequest struct {
	DriveFolderID string `json:"drive_folder_id"` // Google Drive folder ID (required)
	Source        string `json:"source"`          // "youtube", "stock", "artlist", "drive" (default "drive")
	Name          string `json:"name,omitempty"`  // Human-readable name (optional, falls back to folder ID)
	MediaType     string `json:"media_type"`      // "video", "clip", "stock" (default "clip")
}

// SyncDriveFolder handles POST /api/media/sync.
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

	// QDRANT-001 closure: build payload via shared helper so admin
	// (/api) and server-to-server (/internal/v1) variants stay in
	// lockstep on payload schema and job type.
	payload, _ := buildSyncPayload(&SyncPayloadInput{
		FolderID:  folderID,
		Source:    source,
		Name:      req.Name,
		MediaType: mediaType,
		Caller:    "api_admin",
	})

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:       "drive.folder.sync",
		Payload:    payload,
		MaxRetries: 2,
	}, "Drive folder sync dispatched."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}
