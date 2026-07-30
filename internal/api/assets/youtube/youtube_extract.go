// ── POST /api/clips/extract + POST /api/clips/extract-important ──────
//
// Extract enqueues a YouTube clip extraction job. When Destination.Group
// is set the caller's root folder is rewritten to a per-group channel
// subfolder so clips land in Root/<Group>/video-title/.
//
// ExtractImportant enqueues an LLM-driven YouTube clip extraction job
// (PR-GEMMA-EXTRACT-IMPORTANT Step 5).

package youtube

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	transport "github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Extract enqueues a YouTube clip extraction job. When Destination.Group
// is set the caller's root folder is rewritten to a per-group channel
// subfolder so clips land in Root/<Group>/video-title/.
func (h *YouTubeClipHandler) Extract(c *gin.Context) {
	req, ok := apiutil.BindJSON[yttypes.ExtractRequest](c)
	if !ok {
		return
	}

	// Normalize the destination payload without performing Drive I/O.
	// The worker resolves folder creation asynchronously; the HTTP
	// handler must stay fast and fail-closed on enqueue only.
	if req.Destination != nil && req.Destination.FolderID != "" {
		videoID, _ := urlutil.ExtractVideoID(req.URL)
		groupName, subfolderName, folderPath, createSubfolder := normalizeExtractionDestination(req.Destination, videoID)
		req.Destination.CreateSubfolder = createSubfolder
		if folderPath != "" {
			req.Destination.FolderPath = folderPath
		}
		if groupName != "" {
			req.Destination.Group = groupName
		}
		if subfolderName != "" {
			req.Destination.SubfolderName = subfolderName
		}
	}

	payloadBytes, err := json.Marshal(req)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to marshal request: %w", err))
		return
	}
	var payloadMap map[string]any
	if err := json.Unmarshal(payloadBytes, &payloadMap); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to prepare payload: %w", err))
		return
	}

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:    appjobs.TypeYouTubeClipExtract,
		Payload: payloadMap,
	}, "YouTube clip extraction job enqueued."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

// ExtractImportant enqueues an LLM-driven YouTube clip extraction job.
//
// PR-GEMMA-EXTRACT-IMPORTANT Step 5: POST /api/clips/extract-important.
// The jobType dispatches to ExtractImportantClipsJobHandler via the broker.
//
// Payload mapping: the handler uses the canonical yttypes.ExtractRequest DTO
// but builds a payload map compatible with ExtractImportantClipsCommand:
//   - url             → req.URL
//   - video_id        → extracted from req.URL via urlutil.ExtractVideoID
//   - max_segments    → default 5 (the DTO has no segments field; LLM decides)
//   - drive_root_folder → req.Destination.FolderID (or empty string)
//   - language        → "en" (forward-pointer: add a query param)
//
// godlike/07 NO-FAKE-AVAILABILITY: nil jobsSvc returns 503 BEFORE dispatch;
// nil Destination or empty FolderID will cause the use case to fail with
// ErrInvalidInput (drive_root_folder required).
func (h *YouTubeClipHandler) ExtractImportant(c *gin.Context) {
	req, ok := apiutil.BindJSON[yttypes.ExtractRequest](c)
	if !ok {
		return
	}

	// Extract video_id from URL.
	videoID, err := urlutil.ExtractVideoID(req.URL)
	if err != nil || videoID == "" {
		apiutil.BadRequest(c, "could not extract video_id from url (use a standard youtube.com/watch?v=... URL)")
		return
	}

	driveRootFolder := ""
	if req.Destination != nil {
		driveRootFolder = req.Destination.FolderID
	}

	payloadMap := map[string]any{
		"video_id":          videoID,
		"url":               req.URL,
		"max_segments":      5,
		"drive_root_folder": driveRootFolder,
		"language":          "en",
	}

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:    appjobs.TypeYouTubeClipExtractImportant,
		Payload: payloadMap,
	}, "YouTube clip extraction (LLM-driven) job enqueued."); ok {
		return
	}
}

// normalizeExtractionDestination resolves the canonical folder naming
// pieces used by the clip extraction endpoint.
func normalizeExtractionDestination(dest *yttypes.DestinationRequest, videoID string) (groupName, subfolderName, folderPath string, createSubfolder bool) {
	if dest == nil {
		return "", "", "", false
	}
	if rawGroup := strings.TrimSpace(dest.Group); rawGroup != "" {
		groupName = pathutil.SafeFolderName(rawGroup)
	}
	if rawSubfolder := strings.TrimPrefix(strings.TrimSpace(dest.SubfolderName), "yt_"); rawSubfolder != "" {
		subfolderName = pathutil.SafeFolderName(rawSubfolder)
	}
	// An explicit folder path with subfolder creation disabled is the
	// canonical flat-batch escape hatch. It is used by batch callers that
	// resolve one destination folder once and publish clips from multiple
	// source videos into that same folder. Preserve the path verbatim after
	// the transport-level path validation; do not infer a video subfolder.
	explicitFlatFolder := strings.TrimSpace(dest.FolderPath) != "" &&
		!dest.CreateSubfolder && groupName == "" && subfolderName == ""
	if explicitFlatFolder {
		return "", "", strings.TrimSpace(dest.FolderPath), false
	}
	// Default to a child folder when we have a concrete video ID.
	// This keeps direct clip extraction runs from dumping multiple
	// clips into the same Drive root when the caller only supplied
	// folder_id. Callers that want a flat upload can still force it
	// by omitting videoID at this layer (not possible for /process)
	// or by providing an explicit folder_path.
	createSubfolder = subfolderName != "" || strings.TrimSpace(dest.FolderID) == "" || groupName != "" || dest.CreateSubfolder || strings.TrimSpace(videoID) != ""
	if createSubfolder && subfolderName == "" && strings.TrimSpace(videoID) != "" {
		subfolderName = pathutil.SafeFolderName(strings.TrimPrefix(videoID, "yt_"))
	}
	parts := make([]string, 0, 2)
	if groupName != "" {
		parts = append(parts, groupName)
	}
	if subfolderName != "" && createSubfolder {
		parts = append(parts, subfolderName)
	}
	if explicit := strings.TrimSpace(dest.FolderPath); explicit != "" {
		folderPath = explicit
	} else if len(parts) > 0 {
		folderPath = path.Join(parts...)
	}
	return groupName, subfolderName, folderPath, createSubfolder
}

// _ silences the unused import linter when no function references it.
var _ = zap.L
