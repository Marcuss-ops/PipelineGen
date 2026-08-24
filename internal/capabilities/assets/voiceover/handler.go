// Package voiceover provides thin HTTP handlers for voiceover operations.
//
// Slim canonical surface (P0.2 wire-shape, June 2026): only
// POST /api/media/voiceover/generate. The canonical wire-format is
// the typed GenerateVoiceoversRequest (this package's types.go):
//
//   - request_id (correlation ID propagated to job.CorrelationID)
//   - items[]    — per-language (text, language, voice, filename)
//   - destination — Group/FolderID routing (PR-VO-C1 invariant)
//   - options    — remove_silence/strategy/parallelism
//
// Async-only (Blocco 4 EXPAND, June 2026): the /generate handler
// enqueues a voiceover.generate job and returns 202 Accepted with
// the canonical body shape:
//
//	{"ok": true, "job_id": "...", "request_id": "...", "status":
//	"queued", "total_outputs": N}.
//
// The legacy /generate-with-group /batch /promo /sync /groups routes
// were removed from RegisterRoutes PR-VOICEOVER-RECOVERY (V1..V7,
// Wave 21); their Sunset machinery has been retired.
package assets

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler is the slim canonical handler for the voiceover surface.
//
// P0.2 (June 2026): the Handler struct holds ONLY the dependencies
// required by the canonical /generate flow — the jobs.Dispatcher
// (for enqueuing the voiceover.generate job) and a logger. The
// legacy service/syncService/groupsResolver/defaultVoiceoverRoot
// fields were removed because:
//   - Execution is async via the job queue; no in-process Service
//     shortcut is reachable from this handler.
//   - Sync (Drive-backed voiceover reconciliation) is OUT of scope
//     for the canonical surface (revived separately if needed).
//   - Routing is Handled by DestinationRequest (handler pre-resolves
//     OR voiceover.execute dest-resolves inside the use case).
//   - The handler is THIN transport — wire-shape / payload translation
//     is delegated to types.go::GenerateVoiceoversRequest (Pattern 6
//     of AGENTS.md) so the API contract is independent of the
//     internal Command field set.
type Handler struct {
	jobsSvc job.Service
	log     *zap.Logger
}

// NewHandler constructs the slim canonical handler. ALL callers must
// pass a non-nil jobsSvc — fail-fast on nil via panic so a
// misconfigured composition root surfaces at startup rather than
// at the first request.
func NewHandler(jobsSvc job.Service, log *zap.Logger) *Handler {
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
// P0.2 wire shape (June 2026): the handler binds the typed
// GenerateVoiceoversRequest (this package's types.go), validates it,
// and translates it into the canonical *GenerateVoiceoversCommand
// for the job payload. The handler is THIN transport — no business
// logic beyond:
//  1. JSON binding (BindJSON → 400 on failure).
//  2. Request-level validation (Validate → 400 with canonical
//     "godlike/07 no fake availability" errors).
//  3. EnqueueRequest construction with CorrelationID from
//     request_id (so worker-side log stream and dispatcher audit
//     carry the original caller intent).
//  4. jobsSvc.Enqueue (canonical 5xx error path).
//
// The worker side (jobs/generate_handler.go) is unchanged: it
// unmarshals the Payload as *GenerateVoiceoversCommand from JSON,
// exactly as it did before P0.2. The wire-shape split (Pattern 6
// of AGENTS.md) keeps the API contract independent of the internal
// Command field set so a future rename does not leak.
//
// Errors:
//   - JSON binding failure / req.Validate failure → 400 Bad Request.
//   - jobsSvc.Enqueue failure → 500 Internal Server Error (the
//     dispatcher / SQLite store is the canonical owner of the
//     queue-idempotency contract).
func (h *Handler) Generate(c *gin.Context) {
	req, ok := apiutil.BindJSON[GenerateVoiceoversRequest](c)
	if !ok {
		return
	}

	if err := req.Validate(); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	jobReq := req.ToEnqueueRequest()

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
		zap.Int("total_outputs", len(req.Items)))

	apiutil.Accepted(c, gin.H{
		"ok":            true,
		"job_id":        enqueuedJob.ID,
		"request_id":    enqueuedJob.CorrelationID,
		"status":        "queued",
		"total_outputs": len(req.Items),
	})
}
