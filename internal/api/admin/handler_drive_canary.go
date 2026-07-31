// Package admin — handler_drive_canary.go: drive canary handler for
// operational readiness verification (Step 3 of YouTube Clips Deploy
// Readiness action plan, July 2026).
//
// POST /canary-upload accepts exactly one of folder_id or folder_alias and
// uploads a small dummy file to Drive via the canonical delivery.Publisher.
// The endpoint MUST succeed before any YouTube-based test is attempted — it
// proves that Drive credentials are valid and the target folder is writable.
//
// godlike/06 SSOT (one canonical owner per fact): the canary handler
// is the SOLE owner of the Drive canary surface. FolderAliasResolver is the
// SOLE owner of alias/YAML resolution. The Publisher is the SOLE owner of
// the Drive write seam. No other endpoint or CLI may duplicate this logic.
//
// godlike/07 NO-FAKE-AVAILABILITY: the canary actually calls
// Publisher.Publish and returns the real file_id + drive_link. It
// does NOT mock the upload or return hardcoded success.
package admin

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clipfolder"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// CanaryUploadRequest is the JSON body for POST /canary-upload.
// Exactly one of FolderID and FolderAlias must be non-empty.
type CanaryUploadRequest struct {
	// FolderID is the explicit Drive folder to upload the canary file into.
	FolderID string `json:"folder_id"`

	// FolderAlias is resolved through the canonical FolderAliasResolver.
	FolderAlias string `json:"folder_alias"`
}

// CanaryUploadResponse is the JSON response for a successful canary upload.
type CanaryUploadResponse struct {
	OK        bool   `json:"ok"`
	FileID    string `json:"file_id"`
	DriveLink string `json:"drive_link"`
	FolderID  string `json:"folder_id"`
}

// DriveCanaryHandler serves POST /canary-upload.
type DriveCanaryHandler struct {
	publisher     delivery.Publisher
	aliasResolver *clipfolder.FolderAliasResolver
	log           *zap.Logger
}

// NewDriveCanaryHandler constructs the handler with its mandatory deps.
func NewDriveCanaryHandler(pub delivery.Publisher, aliasResolver *clipfolder.FolderAliasResolver, log *zap.Logger) *DriveCanaryHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &DriveCanaryHandler{
		publisher:     pub,
		aliasResolver: aliasResolver,
		log:           log,
	}
}

// RegisterRoutes mounts the canary-upload endpoint.
// Caller is responsible for attaching RequireAdminToken middleware.
func (h *DriveCanaryHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/canary-upload", h.CanaryUpload)
}

// CanaryUpload handles POST /canary-upload.
//
// It creates a temporary canary file with the content "PipelineGen Drive
// canary", uploads it to the selected folder via the canonical Publisher,
// and returns the Drive file metadata. The temp file is cleaned up afterward.
func (h *DriveCanaryHandler) CanaryUpload(c *gin.Context) {
	req, ok := apiutil.BindJSON[CanaryUploadRequest](c)
	if !ok {
		return
	}

	folderID := strings.TrimSpace(req.FolderID)
	folderAlias := strings.TrimSpace(req.FolderAlias)
	if (folderID == "") == (folderAlias == "") {
		apiutil.BadRequest(c, "exactly one of folder_id or folder_alias is required")
		return
	}

	if h.publisher == nil {
		h.log.Error("Drive canary: publisher not wired")
		apiutil.Error(c, http.StatusServiceUnavailable, "Drive publisher not wired — check composition root")
		return
	}

	publishRequest := delivery.PublishRequest{Destination: delivery.DestinationAdmin}
	if folderID != "" {
		publishRequest.RootFolderOverride = folderID
	} else {
		if h.aliasResolver == nil {
			h.log.Error("Drive canary: folder alias resolver not wired")
			apiutil.Error(c, http.StatusServiceUnavailable, "Drive folder alias resolver not wired — check composition root")
			return
		}
		ref, err := h.aliasResolver.Resolve(folderAlias)
		if err != nil {
			apiutil.BadRequest(c, fmt.Sprintf("invalid folder_alias: %v", err))
			return
		}

		if ref.ID != "" {
			publishRequest.RootFolderOverride = ref.ID
		} else {
			// Production aliases intentionally leave folder_id empty. Resolve
			// the canonical path through Publisher, exactly as the resolver
			// contract requires; the handler never reads or re-implements YAML.
			resolvedFolderID, err := h.publisher.ResolveFolder(c.Request.Context(), delivery.PublishRequest{
				Destination: delivery.DestinationYouTubeClip,
				Group:       ref.Path,
				Subject:     "pipelinegen-canary",
			})
			if err != nil {
				h.log.Error("Drive canary: alias folder resolution failed",
					zap.String("folder_alias", folderAlias),
					zap.Error(err),
				)
				apiutil.InternalError(c, fmt.Errorf("drive canary folder resolution failed: %w", err))
				return
			}
			publishRequest.RootFolderOverride = resolvedFolderID
		}
	}

	// Create a temporary canary file.
	tmpDir, err := os.MkdirTemp("", "drive-canary-*")
	if err != nil {
		h.log.Error("Drive canary: failed to create temp dir", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	defer os.RemoveAll(tmpDir)

	canaryPath := filepath.Join(tmpDir, "pipelinegen-canary.txt")
	if err := os.WriteFile(canaryPath, []byte("PipelineGen Drive canary\n"), 0644); err != nil {
		h.log.Error("Drive canary: failed to write temp file", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	// Upload via the canonical Publisher. Uses the request context
	// so the caller can cancel/timeout the upload (consistent with
	// every other handler in the codebase).
	result, err := h.publisher.Publish(c.Request.Context(), delivery.PublishRequest{
		Destination:        publishRequest.Destination,
		LocalPath:          canaryPath,
		Filename:           "pipelinegen-canary.txt",
		Description:        "PipelineGen Drive canary — operational readiness smoke test",
		RootFolderOverride: publishRequest.RootFolderOverride,
	})
	if err != nil {
		h.log.Error("Drive canary: publish failed",
			zap.String("folder_id", folderID),
			zap.String("folder_alias", folderAlias),
			zap.Error(err),
		)
		apiutil.InternalError(c, fmt.Errorf("drive canary upload failed: %w", err))
		return
	}
	if result == nil {
		h.log.Error("Drive canary: publish returned nil result")
		apiutil.Error(c, http.StatusInternalServerError, "drive canary: publish returned nil result")
		return
	}

	h.log.Info("Drive canary: upload succeeded",
		zap.String("file_id", result.FileID),
		zap.String("folder_id", result.FolderID),
		zap.String("drive_link", result.WebViewLink),
	)

	apiutil.OK(c, CanaryUploadResponse{
		OK:        true,
		FileID:    result.FileID,
		DriveLink: result.WebViewLink,
		FolderID:  result.FolderID,
	})
}
