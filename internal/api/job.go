package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	ptrutil "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

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
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Status      job.Status     `json:"status"`
	Progress    int            `json:"progress"`
	Payload     map[string]any `json:"payload,omitempty"`
	Result      map[string]any `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	CompletedAt *string        `json:"completed_at,omitempty"`
}

// BuildJobSummaries converts a slice of Job models into the standard
// JobSummary response format.
func BuildJobSummaries(jobsList []job.Job) []JobSummary {
	summaries := make([]JobSummary, 0, len(jobsList))
	for _, j := range jobsList {
		s := JobSummary{
			ID:        j.ID,
			Type:      j.Type,
			Status:    j.Status,
			Progress:  j.Progress,
			Error:     j.Error,
			CreatedAt: timeutil.FormatRFC3339(j.CreatedAt),
			UpdatedAt: timeutil.FormatRFC3339(j.UpdatedAt),
		}
		if j.CompletedAt != nil {
			s.CompletedAt = ptrutil.Str(timeutil.FormatRFC3339(*j.CompletedAt))
		}
		if len(j.Result) > 0 {
			var result map[string]any
			if json.Unmarshal(j.Result, &result) == nil {
				s.Result = result
			}
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
func ParseJobStatusFilter(c *gin.Context) *job.Status {
	if status := c.Query("status"); status != "" {
		s := job.Status(strings.TrimSpace(status))
		return &s
	}
	return nil
}
