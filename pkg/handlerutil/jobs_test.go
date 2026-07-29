// Package handlerutil_test verifies the pagination helpers and job-summary
// converters used by /api/*/jobs endpoints throughout internal/api.
package handlerutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/handlerutil"
)

// ── ParsePagination ─────────────────────────────────────────────────────

func TestParsePagination_Defaults(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs", nil)

	p := handlerutil.ParsePagination(c, 20, 100)
	assert.Equal(t, 20, p.Limit)
	assert.Equal(t, 0, p.Offset)
}

func TestParsePagination_Override(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs?limit=42&offset=10", nil)

	p := handlerutil.ParsePagination(c, 20, 100)
	assert.Equal(t, 42, p.Limit)
	assert.Equal(t, 10, p.Offset)
}

func TestParsePagination_LimitOutOfBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs?limit=999", nil)

	p := handlerutil.ParsePagination(c, 20, 100)
	assert.Equal(t, 20, p.Limit, "out-of-bounds limit should fall back to default")
}

func TestParsePagination_NegativeOffset(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs?offset=-5", nil)

	p := handlerutil.ParsePagination(c, 20, 100)
	assert.Equal(t, 0, p.Offset, "negative offset should clamp to 0")
}

func TestParsePagination_UnparsableLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs?limit=abc", nil)

	p := handlerutil.ParsePagination(c, 20, 100)
	assert.Equal(t, 20, p.Limit, "unparsable limit should fall back to default (Sscanf silently fails)")
}

// ── BuildJobSummaries ───────────────────────────────────────────────────

func TestBuildJobSummaries_Empty(t *testing.T) {
	out := handlerutil.BuildJobSummaries(nil)
	assert.Empty(t, out)

	out2 := handlerutil.BuildJobSummaries([]job.Job{})
	assert.Empty(t, out2)
}

func TestBuildJobSummaries_RendersCompletedAtAndTimestamps(t *testing.T) {
	ts := time.Date(2026, 6, 27, 10, 30, 0, 0, time.UTC)
	completedAt := ts.Add(time.Minute)

	out := handlerutil.BuildJobSummaries([]job.Job{{
		ID:          "j1",
		Type:        "test.type",
		Status:      job.StatusSucceeded,
		CreatedAt:   ts,
		UpdatedAt:   ts,
		CompletedAt: &completedAt,
	}})

	require := assert.New(t)
	require.Len(out, 1)
	require.Equal("j1", out[0].ID)
	require.Equal("test.type", out[0].Type)
	require.Equal(job.StatusSucceeded, out[0].Status)
	require.NotNil(out[0].CompletedAt, "completedAt pointer should be set")
	require.NotEmpty(*out[0].CompletedAt, "completedAt should be RFC3339-formatted")
	require.NotEmpty(out[0].CreatedAt)
	require.NotEmpty(out[0].UpdatedAt)
}

func TestBuildJobSummaries_OmitsCompletedAtWhenNil(t *testing.T) {
	ts := time.Date(2026, 6, 27, 10, 30, 0, 0, time.UTC)
	out := handlerutil.BuildJobSummaries([]job.Job{{
		ID:        "j1",
		Status:    job.StatusRunning, // StatusRunning is the canonical "in flight" status
		CreatedAt: ts,
		UpdatedAt: ts,
	}})
	assert.Nil(t, out[0].CompletedAt)
}

// ── ListJobsResponse ────────────────────────────────────────────────────

func TestListJobsResponse_WritesCanonicalShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	handlerutil.ListJobsResponse(c, []handlerutil.JobSummary{
		{ID: "j1", Type: "test", Status: job.StatusSucceeded},
	})
	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, true, body["ok"])
	assert.EqualValues(t, 1, body["count"])

	jobs, ok := body["jobs"].([]any)
	assert.True(t, ok, "jobs field should be a JSON array")
	assert.Len(t, jobs, 1)
}

func TestListJobsResponse_EmptyListStillWritesShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	handlerutil.ListJobsResponse(c, nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, true, body["ok"])
	assert.EqualValues(t, 0, body["count"])
}

// ── ParseJobStatusFilter ────────────────────────────────────────────────

func TestParseJobStatusFilter_NilWhenMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs", nil)

	assert.Nil(t, handlerutil.ParseJobStatusFilter(c))
}

func TestParseJobStatusFilter_TrimsWhitespace(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs?status=%20running%20", nil)

	got := handlerutil.ParseJobStatusFilter(c)
	if assert.NotNil(t, got) {
		assert.Equal(t, job.Status("running"), *got)
	}
}

func TestParseJobStatusFilter_PreservesValue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/jobs?status=completed", nil)

	got := handlerutil.ParseJobStatusFilter(c)
	if assert.NotNil(t, got) {
		assert.Equal(t, job.Status("completed"), *got)
	}
}
