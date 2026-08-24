package jobs

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	capreplay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// replayConfig is the wired replay surface of the JobsHandler: the engine
// (pure prepare), the current execution environment (for exact/current mode
// resolution) and the dispatcher (the single enqueue side effect). Nil until
// the composition root calls SetReplay.
type replayConfig struct {
	engine     *capreplay.Engine
	env        capreplay.Environment
	dispatcher capreplay.Dispatcher
}

// SetReplay wires the replay endpoint. engine and dispatcher are required
// (a nil value leaves the endpoint at 503); env is the current execution
// environment used to resolve exact/current mode.
func (h *JobsHandler) SetReplay(engine *capreplay.Engine, env capreplay.Environment, dispatcher capreplay.Dispatcher) {
	h.replay = &replayConfig{engine: engine, env: env, dispatcher: dispatcher}
}

// replayRequestBody is the POST body. Mode is optional: absent/empty defaults
// to "exact" (the fail-closed default).
type replayRequestBody struct {
	Mode string `json:"mode"`
}

// Replay handles POST /api/jobs/:id/replay. It prepares the replay (load
// bundle → resolve mode → materialize+verify assets → resolve strategy),
// dispatches it as a NEW job (never the original id), and returns
// 202 Accepted with the replay job id and the resolved execution mode.
func (h *JobsHandler) Replay(c *gin.Context) {
	if h.replay == nil || h.replay.engine == nil || h.replay.dispatcher == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "replay is not configured")
		return
	}
	id := c.Param("id")

	var body replayRequestBody
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			apiutil.BadRequest(c, "invalid request body: "+err.Error())
			return
		}
	}
	mode, err := capreplay.ParseMode(body.Mode)
	if err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	prepared, err := h.replay.engine.Prepare(c.Request.Context(), id, mode, h.replay.env)
	if err != nil {
		h.mapReplayError(c, err)
		return
	}

	status := "queued"
	if status, err = h.replay.dispatcher.Dispatch(c.Request.Context(), *prepared); err != nil {
		h.log.Error("failed to dispatch replay", zap.String("original_job_id", prepared.OriginalJobID), zap.String("replay_job_id", prepared.ReplayJobID), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	if status == "" {
		status = "queued"
	}

	apiutil.Accepted(c, gin.H{
		"original_job_id": prepared.OriginalJobID,
		"replay_job_id":   prepared.ReplayJobID,
		"plan_sha256":     prepared.PlanSHA256,
		"mode":            prepared.Mode,
		"execution_mode":  prepared.Strategy.Mode,
		"status":          status,
	})
}

// mapReplayError translates engine errors to the canonical HTTP surface:
// missing bundle → 404, exact-version mismatch → 409 (fail, never silent),
// invalid mode → 400, everything else → 500.
func (h *JobsHandler) mapReplayError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, capreplay.ErrBundleNotFound):
		apiutil.NotFound(c, err.Error())
	case errors.Is(err, capreplay.ErrExactVersionMismatch):
		apiutil.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, capreplay.ErrInvalidMode):
		apiutil.BadRequest(c, err.Error())
	default:
		h.log.Error("replay failed", zap.Error(err))
		apiutil.InternalError(c, err)
	}
}
