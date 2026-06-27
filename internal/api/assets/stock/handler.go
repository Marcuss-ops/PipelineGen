package stock

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the api-layer adapter for the stock pipeline endpoints.
// After S2b it holds ONLY the use case + logger — neither the
// stockpipeline.Service nor the jobs service are referenced directly.
// All dispatch logic lives in stockpipeline.StockUseCase.
type Handler struct {
	useCase *stockpipeline.StockUseCase
	log     *zap.Logger
}

// NewHandler constructs the api handler. Production wire-up builds a
// *stockpipeline.StockUseCase first (composition root, module_sources.go)
// and passes it in; test fixtures may pass nil for either dependency.
func NewHandler(useCase *stockpipeline.StockUseCase, log *zap.Logger) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		useCase: useCase,
		log:     log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Stock Pipeline routes")

	r.POST("/run", h.RunStockPipeline)
	r.POST("/search-and-run", h.SearchAndRun)
}

// ── POST /api/stock/search-and-run ──────────────────────────────────────
//
// Body binds directly to the canonical stockpipeline.StockSearchAndRunRequest
// rather than a local mirror — that way the api request type and the
// application command type stay in lockstep (renames propagate via Go
// compile errors rather than via drift in two json-tag sets).

func (h *Handler) SearchAndRun(c *gin.Context) {
	var req stockpipeline.StockSearchAndRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("stock search-and-run request received",
		zap.Int("queries", len(req.Queries)),
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

	// HTTP validation — must run before FromSearchAndRunRequest so the
	// converter sees a valid shape (per the S2b design: validation in
	// the api layer, defaulting in the api layer).
	if len(req.Queries) == 0 {
		apiutil.BadRequest(c, "queries required")
		return
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}
	if req.ClipDuration < 0 {
		apiutil.BadRequest(c, "clip_duration must be >= 0")
		return
	}
	if req.ClipDuration > 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		apiutil.BadRequest(c, "clip_duration must be between 3 and 30 seconds")
		return
	}

	cmd, err := stockpipeline.FromSearchAndRunRequest(&req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, true /* async */)
	if err != nil {
		if errors.Is(err, stockpipeline.ErrJobsServiceRequired) {
			apiutil.Error(c, http.StatusServiceUnavailable,
				"stock async submit requires jobs service (no sync fallback — use /search-and-run with async flag=false on wire jobsSvc)")
			return
		}
		h.log.Error("stock search-and-run failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.Accepted(c, gin.H{
		"job_id":     jobID,
		"message":    "Stock search-and-run job enqueued",
		"status_url": "/api/jobs/" + jobID + "/full",
	})
}

// ── POST /api/stock/run ────────────────────────────────────────────────

func (h *Handler) RunStockPipeline(c *gin.Context) {
	var req stockpipeline.StockRunPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	h.log.Info("stock run request received",
		zap.Int("search_queries", len(req.SearchQueries)),
		zap.Int("direct_urls", len(req.DirectURLs)),
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

	// HTTP validation (same shape as SearchAndRun).
	if len(req.SearchQueries) == 0 && len(req.DirectURLs) == 0 {
		apiutil.BadRequest(c, "search_queries or direct_urls required")
		return
	}
	if req.TotalMinutes <= 0 {
		req.TotalMinutes = 5
	}
	if req.ClipDuration < 0 {
		apiutil.BadRequest(c, "clip_duration must be >= 0")
		return
	}
	if req.ClipDuration > 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		apiutil.BadRequest(c, "clip_duration must be between 3 and 30 seconds")
		return
	}

	cmd, err := stockpipeline.FromRunPayload(&req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	jobID, err := h.useCase.Submit(c.Request.Context(), cmd, true /* async */)
	if err != nil {
		if errors.Is(err, stockpipeline.ErrJobsServiceRequired) {
			apiutil.Error(c, http.StatusServiceUnavailable,
				"stock async submit requires jobs service (no sync fallback — use /run with async flag=false or wire jobsSvc)")
			return
		}
		h.log.Error("stock run failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.Accepted(c, gin.H{
		"job_id":     jobID,
		"message":    "Stock pipeline job enqueued",
		"status_url": "/api/jobs/" + jobID + "/full",
	})
}

// StockPipelineResponse is the public response shape returned ONLY when
// the handler is invoked synchronously (S2b no longer has an inline
// sync fallback, but other entrypoints such as the admin `benchmark`
// subcommand or the worker may emit a sync result body that this
// shape matches).
type StockPipelineResponse struct {
	Status      string                      `json:"status"`
	TotalClips  int                         `json:"total_clips"`
	TotalChunks int                         `json:"total_chunks"`
	Chunks      []stockpipeline.ChunkResult `json:"chunks"`
	Error       string                      `json:"error,omitempty"`
	JobID       string                      `json:"job_id,omitempty"`
	StatusURL   string                      `json:"status_url,omitempty"`
}
