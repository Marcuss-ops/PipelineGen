// Package common provides cross-cutting HTTP helpers shared by
// internal/api/ sub-packages. It is intentionally NOT the parent
// api package — sub-packages import common/ rather than the
// parent, keeping the parent api package free of sub-package
// import cycles.
//
// Wave 14 follow-up (June 2026): EnqueueAsync and its dependencies
// moved here from internal/api/job.go so sub-packages (e.g.
// api/script, api/jobs) can use them without importing the parent
// api package. The parent api package now re-exports via a type
// alias for backward compatibility.
package common

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// ── Async job enqueue ─────────────────────────────────────────────

// Enqueuer is the minimal interface consumed by EnqueueAsync.
type Enqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// EnqueueInput parameterises EnqueueAsync.
type EnqueueInput struct {
	Type          string
	Payload       any
	Priority      int
	ActiveKey     string
	MaxRetries    int
	CorrelationID string
}

// EnqueueAsync enqueues a job and writes the standard async response.
func EnqueueAsync(c *gin.Context, enqueuer Enqueuer, in *EnqueueInput, message string) bool {
	if enqueuer == nil {
		apiutil.InternalError(c, fmt.Errorf("job system not available"))
		return false
	}

	req := &job.EnqueueRequest{
		Type:          in.Type,
		Payload:       in.Payload,
		Priority:      in.Priority,
		MaxRetries:    in.MaxRetries,
		CorrelationID: in.CorrelationID,
	}
	if in.ActiveKey != "" {
		req.ActiveKey = in.ActiveKey
	}
	if in.Priority <= 0 {
		req.Priority = 5
	}

	j, err := enqueuer.Enqueue(c.Request.Context(), req)
	if err != nil {
		apiutil.InternalError(c, err)
		return false
	}

	if message == "" {
		message = "Job enqueued."
	}
	AsyncJobResponse(c, j, message)
	return true
}

// AsyncJobResponse builds the standard async job response used by all
// handlers that support background processing.
func AsyncJobResponse(c *gin.Context, j *job.Job, message string) {
	apiutil.OK(c, gin.H{
		"ok":         true,
		"async":      true,
		"job_id":     j.ID,
		"status":     string(j.Status),
		"message":    message + " Poll /api/jobs/" + j.ID + "/full for status.",
		"status_url": "/api/jobs/" + j.ID + "/full",
	})
}
