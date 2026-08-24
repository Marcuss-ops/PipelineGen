// ── POST /api/clips/process ──────
//
// Extract enqueues a YouTube clip extraction job. When Destination.Group or
// Destination.SubfolderName is set, the caller's root folder is rewritten to
// the requested child folder. An explicit FolderID without a requested child
// remains a flat destination; the handler must not derive a child from the
// source video ID.
//
// selection.mode (explicit | important) is resolved inside the canonical
// extraction pipeline by the SegmentSelectionResolver — the HTTP handler
// only enqueues the request; it never runs a second download/upload/hash/
// commit pipeline.

package assets

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	transport "github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/shared/pathutil"
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
	// An explicit FolderID is already a complete destination. Do not derive a
	// child folder from videoID: callers that want one must name it through
	// group, subfolder_name, or folder_path/create_subfolder.
	createSubfolder = subfolderName != "" || strings.TrimSpace(dest.FolderID) == "" || groupName != "" || dest.CreateSubfolder
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
