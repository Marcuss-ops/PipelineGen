package script

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
)

func writeGenerateSubmitError(c *gin.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "JOB_ENQUEUE_TIMEOUT",
		})
		return
	}
	if errors.Is(err, domainops.ErrIdempotencyConflict) {
		c.JSON(http.StatusConflict, gin.H{
			"ok":    false,
			"error": "Idempotency-Key reused with different payload",
			"code":  "IDEMPOTENCY_KEY_CONFLICT",
		})
		return
	}
	c.JSON(mapErrorToHTTP(err), gin.H{
		"ok":    false,
		"error": "operations submission failed",
	})
}

func writeGenerateSubmitSuccess(c *gin.Context, res *opsapp.SubmitResult) {
	status := "PENDING"
	if res != nil && res.IsIdempotencyHit {
		c.Writer.Header().Set("X-Idempotency-Replay", "true")
	}
	if res != nil && res.Job != nil && res.Job.Status != "" {
		status = string(res.Job.Status)
	}

	jobID := ""
	if res != nil && res.Operation != nil {
		jobID = res.Operation.JobID
	}
	if jobID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "operations submission returned empty job_id",
		})
		return
	}

	resp := GenerateResponse{}
	resp.async(jobID, status, "/api/jobs/"+jobID+"/full", "")
	c.JSON(http.StatusAccepted, resp)
}
