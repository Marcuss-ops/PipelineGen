// Package voiceover provides thin HTTP handlers for voiceover operations.
//
// Slim canonical surface (Blocco 4 EXPAND, June 2026): only
// POST /api/voiceover/generate. The canonical wire-format is the
// typed GenerateVoiceoversCommand (internal/application/voiceover/command.go):
//   - async-only via the voiceover.generate job type (introduced in
//     Blocco 4 EXPAND). The /generate handler enqueues a job and
//     returns 202 Accepted with the canonical body shape
//     { "ok": true, "job_id": "...", "status": "queued", "total_outputs": N }.
//   - the legacy /generate-with-group /batch /promo /sync /groups
//     routes were removed from RegisterRoutes at the Blocco 4 EXPAND
//     CUTOVER step (godlike/07 EXPAND/BACKFILL/CUTOVER/CONTRACT
//     migration sequence); their Sunset machinery has now been
//     retired per Wave 21 / PR-VOICEOVER-RECOVERY (V1..V7).
package voiceover

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the slim canonical handler for the voiceover surface.
//
// Blocco 4 EXPAND (June 2026): the Handler struct holds ONLY the
// dependencies required by the canonical /generate flow — the
// jobs.Dispatcher (for enqueuing the voiceover.generate job) and a
// logger. The legacy service/syncService/groupsResolver/defaultVoiceoverRoot
// fields were removed because:
//   - Execution is async via the job queue; no in-process Service
//     shortcut is reachable from this handler.
//   - Sync (Drive-backed voiceover reconciliation) is OUT of scope
//     for the canonical surface (revived separately if needed).
//   - Routing is Handled by DestinationRequest (handler pre-resolves
//     OR voiceover.execute dest-resolves inside the use case).
type Handler struct {
	jobsSvc jobservice.Service
	log     *zap.Logger
}

// NewHandler constructs the slim canonical handler. ALL callers must
// pass a non-nil jobsSvc — fail-fast on nil via panic so a
// misconfigured composition root surfaces at startup rather than
// at the first request.
func NewHandler(jobsSvc jobservice.Service, log *zap.Logger) *Handler {
	if jobsSvc == nil {
		panic("voiceover.NewHandler: jobsSvc is required (jobs.Service for voiceover.generate enqueue)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Handler{
		jobsSvc: jobsSvc,
		log:     log,
	}
}

// RegisterRoutes registers the slim canonical surface. Only
// POST /generate is exposed.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
}

// Generate enqueues a voiceover.generate job (Blocco 4 EXPAND job
// type) and returns 202 Accepted with the canonical body shape.
//
// Dispatch contract (per Pattern 0 / AGENTS.md): the handler is
// THIN transport. The wire shape is the typed
// voiceover.GenerateVoiceoversCommand; the bound struct is unmarshalled
// by jobs/generate_handler.go on the worker side via the same
// json tags added to command.go (snake_case + omitempty). The handler
// runs the synchronous envelope ONLY — validation + enqueue + canonical
// 202 — and the heavy per-language fan-out lives in the worker
// (bounded parallel executor per Blocco 3).
//
// Errors:
//   - JSON binding failure / cmd.Validate failure → 400 Bad Request.
//   - jobsSvc.Enqueue failure → 500 Internal Server Error (the
//     dispatcher / SQLite store is the canonical owner of the
//     queue-idempotency contract).
func (h *Handler) Generate(c *gin.Context) {
	cmd, ok := apiutil.BindJSON[voiceover.GenerateVoiceoversCommand](c)
	if !ok {
		return
	}

	if err := cmd.Validate(); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	jobReq := &jobservice.EnqueueRequest{
		Type:    jobservice.TypeVoiceoverGenerate,
		Payload: cmd,
	}

	enqueuedJob, enqueueErr := h.jobsSvc.Enqueue(c.Request.Context(), jobReq)
	if enqueueErr != nil {
		h.log.Error("voiceover.generate enqueue failed",
			zap.Error(enqueueErr))
		apiutil.InternalError(c, enqueueErr)
		return
	}

	h.log.Info("voiceover.generate enqueued",
		zap.String("job_id", enqueuedJob.ID),
		zap.String("request_id", enqueuedJob.CorrelationID),
		zap.Int("total_outputs", len(cmd.Languages)))

	apiutil.Accepted(c, gin.H{
		"ok":            true,
		"job_id":        enqueuedJob.ID,
		"status":        "queued",
		"total_outputs": len(cmd.Languages),
	})
}
