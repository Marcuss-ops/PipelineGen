// ── POST /api/stock/search-and-run ──────────────────────────────────────
//
// Body binds directly to the canonical stockpipeline.StockSearchAndRunRequest
// rather than a local mirror — that way the api request type and the
// application command type stay in lockstep (renames propagate via Go
// compile errors rather than via drift in two json-tag sets).

package stock

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

func (h *Handler) SearchAndRun(c *gin.Context) {
	// Default Async=true so existing clients (no "async" field in payload)
	// preserve the canonical jobs-broker path. Operators that want
	// in-process sync set "async": false on the wire. Sync mode also
	// flips Persist=true so the runner uses the resilient path and
	// completes upload + finalization + indexing instead of stopping
	// at the legacy manifest-only flow.
	req := stockpipeline.StockSearchAndRunRequest{Async: true}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("stock search-and-run request received",
		zap.Int("queries", len(req.Queries)),
		zap.Int("direct_urls", len(req.DirectURLs)),
		zap.Int("drive_urls", len(req.DriveURLs)),
		zap.Int("clips", len(req.Clips)),
		zap.Int("total_minutes", req.TotalMinutes),
		zap.Int("chunk_duration", req.ChunkDuration),
		zap.Int("clip_duration", req.ClipDuration),
		zap.Bool("no_audio", req.NoAudio),
		zap.Bool("no_effects", req.NoEffects),
		zap.Bool("no_transitions", req.NoTransitions),
		zap.Int("max_videos", req.MaxVideos),
		zap.String("subfolder", req.Subfolder),
		zap.String("folder_name", req.FolderName),
		zap.String("folder_id", req.FolderID),
	)

	adjusted, validateErr := applyStockDefaults("queries, direct_urls, drive_urls, or clips required", stockValidationInput{
		SearchSourceCount: len(req.Queries),
		DirectURLsCount:   len(req.DirectURLs),
		DriveURLsCount:    len(req.DriveURLs),
		Clips:             req.Clips,
		TotalMinutes:      req.TotalMinutes,
		ClipDuration:      req.ClipDuration,
		Async:             req.Async,
	})
	if validateErr != nil {
		apiutil.BadRequest(c, validateErr.Error())
		return
	}
	req.TotalMinutes = adjusted.TotalMinutes
	req.ClipDuration = adjusted.ClipDuration
	req.Persist = adjusted.Persist

	cmd, err := stockpipeline.FromSearchAndRunRequest(&req)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("from search-and-run request: %w", err))
		return
	}

	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, req.Async)
	if err != nil {
		if errors.Is(err, stockpipeline.ErrJobsServiceRequired) {
			apiutil.Error(c, http.StatusServiceUnavailable,
				"stock async submit requires jobs service (no sync fallback — use /search-and-run with async flag=false on wire jobsSvc)")
			return
		}
		h.log.Error("stock search-and-run failed", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("submit: %w", err))
		return
	}

	resp := gin.H{"job_id": jobID}
	if jobID != "" {
		resp["message"] = "Stock search-and-run job enqueued"
		resp["status_url"] = "/api/jobs/" + jobID + "/full"
	} else {
		resp["message"] = "Stock pipeline run completed"
	}
	apiutil.OK(c, resp)
}
