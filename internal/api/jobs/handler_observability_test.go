// Package jobs — handler_observability_test.go (P0 observability, July 2026).
//
// Pins the contract that GET /api/jobs/{id} and GET /api/jobs/{id}/full
// return an enriched status shape (current_stage, timeline, warnings,
// correlation_id, timestamps) and that GET /api/jobs supports filtering
// by correlation_id.
package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// observabilityStub is a minimal job.Service that records the
// last filter passed to List and returns a configurable job + events.
type observabilityStub struct {
	job            *job.Job
	events         []job.Event
	lastFilter     job.Filter
	lastListEvents string
}

func (s *observabilityStub) Enqueue(_ context.Context, _ *job.EnqueueRequest) (*job.Job, error) {
	return nil, nil
}
func (s *observabilityStub) Get(_ context.Context, _ string) (*job.Job, error) {
	return s.job, nil
}
func (s *observabilityStub) Cancel(_ context.Context, _ string) error { return nil }
func (s *observabilityStub) List(_ context.Context, filter job.Filter) ([]job.Job, error) {
	s.lastFilter = filter
	if s.job == nil {
		return nil, nil
	}
	return []job.Job{*s.job}, nil
}
func (s *observabilityStub) IsTerminal(_ job.Status) bool          { return false }
func (s *observabilityStub) RegisterHandler(_ string, _ any) error { return nil }
func (s *observabilityStub) ListEvents(_ context.Context, id string) ([]job.Event, error) {
	s.lastListEvents = id
	return s.events, nil
}
func (s *observabilityStub) Retry(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}

var _ job.Service = (*observabilityStub)(nil)

func TestGet_EnrichedResponseShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	j := &job.Job{
		ID:            "job-obs-1",
		Type:          scriptpkg.TypeGenerate,
		Status:        job.StatusRunning,
		CorrelationID: "corr-abc-123",
		Progress:      42,
		Error:         "",
	}
	events := []job.Event{
		{ID: "evt-1", JobID: j.ID, Type: "request.validated", Message: "validated"},
		{ID: "evt-2", JobID: j.ID, Type: "clips.hydrated", Message: "clips ok"},
		{ID: "evt-3", JobID: j.ID, Type: "warning", Message: "slow clip"},
	}
	stub := &observabilityStub{job: j, events: events}
	h := NewJobsHandler(stub, nil, zap.NewNop())
	router := gin.New()
	rg := router.Group("/jobs")
	h.RegisterRoutes(rg)

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/jobs/job-obs-1", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "job-obs-1", body["id"])
	assert.Equal(t, string(job.StatusRunning), body["status"])
	assert.Equal(t, "corr-abc-123", body["correlation_id"])
	assert.Equal(t, "clips.hydrated", body["current_stage"])
	assert.Equal(t, float64(42), body["progress"])

	timeline, ok := body["timeline"].([]any)
	require.True(t, ok, "timeline must be an array")
	require.Len(t, timeline, 3)

	warnings, ok := body["warnings"].([]any)
	require.True(t, ok, "warnings must be an array")
	require.Len(t, warnings, 1)
	warn := warnings[0].(map[string]any)
	assert.Equal(t, "slow clip", warn["message"])

	assert.Equal(t, "job-obs-1", stub.lastListEvents)
}

func TestList_CorrelationIDFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	j := &job.Job{
		ID:            "job-obs-2",
		Type:          scriptpkg.TypeGenerate,
		Status:        job.StatusQueued,
		CorrelationID: "corr-filter-xyz",
	}
	stub := &observabilityStub{job: j}
	h := NewJobsHandler(stub, nil, zap.NewNop())
	router := gin.New()
	rg := router.Group("/jobs")
	h.RegisterRoutes(rg)

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/jobs?correlation_id=corr-filter-xyz", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, stub.lastFilter.CorrelationID)
	assert.Equal(t, "corr-filter-xyz", *stub.lastFilter.CorrelationID)
}
