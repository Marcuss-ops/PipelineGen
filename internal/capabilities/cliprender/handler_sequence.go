package cliprender

// handler_sequence.go owns POST /api/clips/render/sequence: the transport that
// turns a sequence request into a sequence-phase clip.render job. It is the
// same program the batch endpoint enqueues next to its render items, exposed on
// its own so a caller that already holds the clips can assemble them without
// the CLI.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// RenderSequence handles POST /api/clips/render/sequence.
//
// Validation mirrors POST /render: strict JSON, Normalize + Validate, then one
// canonical Master enqueue. The job renders nothing — it waits for the clips it
// names (if they are still rendering) and assembles them — so it is enqueued on
// the same job type and lane as a render.
func (h *Handler) RenderSequence(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxClipRenderRequestBytes)

	var req SequenceRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, renderResponse{
				Status:    StatusError,
				Error:     fmt.Sprintf("request body exceeds the %d byte limit", MaxClipRenderRequestBytes),
				ErrorCode: ErrCodePayloadTooLarge,
			})
			return
		}
		code := ErrCodeInvalidPayload
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			code = ErrCodeUnknownField
		}
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     "invalid JSON payload: " + err.Error(),
			ErrorCode: code,
		})
		return
	}

	req.Normalize()
	req.RenderPhase = RenderPhaseSequence
	if err := req.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status:    StatusError,
			Error:     err.Error(),
			ErrorCode: ErrCodeInvalidPayload,
		})
		return
	}
	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, renderResponse{
			Status:    StatusError,
			Error:     "jobs service not configured",
			ErrorCode: ErrCodeJobsUnavailable,
		})
		return
	}

	enqueued, err := h.jobsSvc.Enqueue(c.Request.Context(), &job.EnqueueRequest{
		Type:       TypeClipRender,
		Payload:    &req,
		Scheduling: &job.SchedulingDescriptor{JobType: TypeClipRender, ResourceClass: SchedulingResourceClass, ExecutionLane: ExecutionLaneSubmit},
	})
	if err != nil {
		h.log.Error("clip.render sequence enqueue failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	h.log.Info("clip.render sequence enqueued",
		zap.String("job_id", enqueued.ID),
		zap.Int("clips", len(req.Clips)),
		zap.String("output_dir", req.Output.Dir),
	)

	apiutil.Accepted(c, renderResponse{
		JobID:  enqueued.ID,
		Status: StatusQueued,
	})
}
