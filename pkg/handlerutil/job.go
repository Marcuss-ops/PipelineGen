package handlerutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/ptrutil"
	"velox/go-master/pkg/timeutil"
)

// ── Async Response ──────────────────────────────────────────────────────

// AsyncJobResponse builds the standard async job response used by all
// handlers that support background processing.
func AsyncJobResponse(c *gin.Context, job *models.Job, message string) {
	apiutil.OK(c, gin.H{
		"ok":         true,
		"async":      true,
		"job_id":     job.ID,
		"status":     string(job.Status),
		"message":    message + " Poll /api/jobs/" + job.ID + "/full for status.",
		"status_url": "/api/jobs/" + job.ID + "/full",
	})
}

// Enqueuer is the minimal interface consumed by EnqueueAsync.
type Enqueuer interface {
	Enqueue(ctx context.Context, req *jobs.EnqueueRequest) (*models.Job, error)
}

// EnqueueInput parameterises EnqueueAsync.
type EnqueueInput struct {
	Type          models.JobType
	Payload       map[string]any
	Priority      int
	ActiveKey     string
	MaxRetries    int
	CorrelationID string
}

// EnqueueAsync enqueues a job and writes the standard async response. The
// boolean return is false when the job service is nil or enqueuing fails, so
// the caller can return early.
func EnqueueAsync(c *gin.Context, enqueuer Enqueuer, in *EnqueueInput, message string) bool {
	if enqueuer == nil {
		apiutil.InternalError(c, fmt.Errorf("job system not available"))
		return false
	}

	req := &jobs.EnqueueRequest{
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

	job, err := enqueuer.Enqueue(c.Request.Context(), req)
	if err != nil {
		apiutil.InternalError(c, err)
		return false
	}

	if message == "" {
		message = "Job enqueued."
	}
	AsyncJobResponse(c, job, message)
	return true
}

// ── Pagination ──────────────────────────────────────────────────────────

// Pagination holds parsed limit/offset values from query parameters.
type Pagination struct {
	Limit  int
	Offset int
}

// ParsePagination parses limit and offset from query parameters with defaults.
func ParsePagination(c *gin.Context, defaultLimit, maxLimit int) Pagination {
	limit := defaultLimit
	offset := 0
	if v := c.Query("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if v := c.Query("offset"); v != "" {
		fmt.Sscanf(v, "%d", &offset)
	}
	if limit <= 0 || limit > maxLimit {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	return Pagination{Limit: limit, Offset: offset}
}

// ── Job Listing ─────────────────────────────────────────────────────────

// JobSummary is the standard job summary struct used by /api/*/jobs endpoints.
type JobSummary struct {
	ID          string           `json:"id"`
	Type        models.JobType   `json:"type"`
	Status      models.JobStatus `json:"status"`
	Progress    int              `json:"progress"`
	Payload     map[string]any   `json:"payload,omitempty"`
	Result      map[string]any   `json:"result,omitempty"`
	Error       string           `json:"error,omitempty"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
	CompletedAt *string          `json:"completed_at,omitempty"`
}

// BuildJobSummaries converts a slice of Job models into the standard
// JobSummary response format.
func BuildJobSummaries(jobsList []*models.Job) []JobSummary {
	summaries := make([]JobSummary, 0, len(jobsList))
	for _, j := range jobsList {
		s := JobSummary{
			ID:        j.ID,
			Type:      j.Type,
			Status:    j.Status,
			Progress:  j.Progress,
			Result:    j.Result,
			Error:     j.Error,
			CreatedAt: timeutil.FormatRFC3339(j.CreatedAt),
			UpdatedAt: timeutil.FormatRFC3339(j.UpdatedAt),
		}
		if j.CompletedAt != nil {
			s.CompletedAt = ptrutil.Str(timeutil.FormatRFC3339(*j.CompletedAt))
		}
		if len(j.Payload) > 0 {
			var payload map[string]any
			if json.Unmarshal(j.Payload, &payload) == nil {
				s.Payload = payload
			}
		}
		summaries = append(summaries, s)
	}
	return summaries
}

// ListJobsResponse writes the standard job listing response.
func ListJobsResponse(c *gin.Context, summaries []JobSummary) {
	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"count": len(summaries),
		"jobs":  summaries,
	})
}

// ParseJobStatusFilter parses an optional status query parameter.
func ParseJobStatusFilter(c *gin.Context) *models.JobStatus {
	if status := c.Query("status"); status != "" {
		s := models.JobStatus(strings.TrimSpace(status))
		return &s
	}
	return nil
}
