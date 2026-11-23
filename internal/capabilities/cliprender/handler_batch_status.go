package cliprender

// handler_batch_status.go owns GET and DELETE /api/clips/render/batch/{id}.
//
// The batch has no separate table: its identity is the deterministic
// BatchFingerprint, and every item/sequence job carries that identity as the
// prefix of its durable correlation_id (`<batch>:<fp>` / `<batch>:sequence:<fp>`).
// A prefix query reconstructs the batch from the job rows, so a restart does
// not lose it. A cache-only batch creates no job and is not addressable here;
// its assets are already returned by the POST response.

import (
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type batchJobStatus struct {
	JobID         string `json:"job_id"`
	Status        string `json:"status"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Error         string `json:"error,omitempty"`
}

type batchStatusResponse struct {
	BatchID       string           `json:"batch_id"`
	Status        string           `json:"status"`
	Accepted      int              `json:"accepted"`
	Jobs          []batchJobStatus `json:"jobs"`
	SequenceJobID string           `json:"sequence_job_id,omitempty"`
	SequenceState string           `json:"sequence_status,omitempty"`
	Error         string           `json:"error,omitempty"`
}

type batchCancelResponse struct {
	BatchID      string   `json:"batch_id"`
	Status       string   `json:"status"`
	Cancelled    int      `json:"cancelled"`
	CancelledIDs []string `json:"cancelled_job_ids,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func validBatchID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (h *Handler) listBatchJobs(c *gin.Context, batchID string) ([]job.Job, bool) {
	if !validBatchID(batchID) {
		c.JSON(http.StatusBadRequest, renderResponse{
			Status: StatusError, Error: "batch_id must be a 64-character hex digest", ErrorCode: ErrCodeInvalidPayload,
		})
		return nil, false
	}
	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, renderResponse{
			Status: StatusError, Error: "jobs service not configured", ErrorCode: ErrCodeJobsUnavailable,
		})
		return nil, false
	}
	jobType := TypeClipRender
	jobs, err := h.jobsSvc.List(c.Request.Context(), job.Filter{
		Type:              &jobType,
		CorrelationPrefix: batchID + ":",
		Limit:             MaxClipRenderBatchItems*2 + 2,
	})
	if err != nil {
		h.log.Error("clip.render batch list failed", zap.String("batch_id", batchID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, renderResponse{
			Status: StatusError, Error: "batch lookup failed", ErrorCode: ErrCodeInternal,
		})
		return nil, false
	}
	if len(jobs) == 0 {
		c.JSON(http.StatusNotFound, renderResponse{
			Status: StatusError, Error: "batch not found", ErrorCode: ErrCodeNotFound,
		})
		return nil, false
	}
	return jobs, true
}

// GetRenderBatch handles GET /api/clips/render/batch/{batch_id}.
func (h *Handler) GetRenderBatch(c *gin.Context) {
	batchID := c.Param("batch_id")
	jobs, ok := h.listBatchJobs(c, batchID)
	if !ok {
		return
	}
	response := batchStatusResponse{BatchID: batchID, Status: "COMPLETED"}
	prefix := batchID + ":sequence:"
	allTerminal := true
	anyFailed := false
	for _, j := range jobs {
		entry := batchJobStatus{JobID: j.ID, Status: string(j.Status), CorrelationID: j.CorrelationID, Error: j.Error}
		if strings.HasPrefix(j.CorrelationID, prefix) {
			response.SequenceJobID = j.ID
			response.SequenceState = string(j.Status)
		} else {
			response.Jobs = append(response.Jobs, entry)
		}
		if !j.Status.IsTerminal() {
			allTerminal = false
		}
		if j.Status == job.StatusFailed {
			anyFailed = true
		}
		if j.Status != job.StatusFailed && j.Status != job.StatusCancelled {
			response.Accepted++
		}
	}
	switch {
	case !allTerminal:
		response.Status = "RUNNING"
	case anyFailed:
		response.Status = "PARTIAL"
	default:
		response.Status = "COMPLETED"
	}
	c.JSON(http.StatusOK, response)
}

// CancelRenderBatch handles DELETE /api/clips/render/batch/{batch_id}. It
// cancels the assembly first (so it stops waiting), then every item that is not
// already terminal. Terminal rows are reported as-is; cancellation is
// idempotent and never rewrites a completed render.
func (h *Handler) CancelRenderBatch(c *gin.Context) {
	batchID := c.Param("batch_id")
	jobs, ok := h.listBatchJobs(c, batchID)
	if !ok {
		return
	}
	response := batchCancelResponse{BatchID: batchID, Status: "CANCELLED"}
	var failures []string
	prefix := batchID + ":sequence:"
	cancel := func(j job.Job) {
		if j.Status.IsTerminal() {
			return
		}
		if err := h.jobsSvc.Cancel(c.Request.Context(), j.ID); err != nil {
			failures = append(failures, j.ID+": "+err.Error())
			return
		}
		response.Cancelled++
		response.CancelledIDs = append(response.CancelledIDs, j.ID)
	}
	// Sequence first: cancelling it before its children prevents the
	// assembly from waking on a child that is about to disappear.
	for _, j := range jobs {
		if strings.HasPrefix(j.CorrelationID, prefix) {
			cancel(j)
		}
	}
	for _, j := range jobs {
		if !strings.HasPrefix(j.CorrelationID, prefix) {
			cancel(j)
		}
	}
	if len(failures) > 0 {
		response.Status = "PARTIAL"
		response.Error = strings.Join(failures, "; ")
		c.JSON(http.StatusConflict, response)
		return
	}
	c.JSON(http.StatusOK, response)
}
