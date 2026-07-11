package script

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// slowJobsService is a test double that blocks until the provided
// context is cancelled or the timeout fires. It is used to exercise
// the JOB_ENQUEUE_TIMEOUT path.
type slowJobsService struct {
	blockDuration time.Duration
}

var _ job.Service = (*slowJobsService)(nil)

func (s *slowJobsService) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.blockDuration):
		return &job.Job{ID: "job-slow", Status: job.StatusQueued, Type: req.Type}, nil
	}
}

func (s *slowJobsService) Get(ctx context.Context, id string) (*job.Job, error) {
	return nil, nil
}

func (s *slowJobsService) Cancel(ctx context.Context, id string) error {
	return nil
}

func (s *slowJobsService) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	return nil, nil
}

func (s *slowJobsService) IsTerminal(status job.Status) bool {
	return status.IsTerminal()
}

func (s *slowJobsService) RegisterHandler(jobType string, handler any) error {
	return nil
}

func (s *slowJobsService) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	return nil, nil
}

func (s *slowJobsService) Retry(ctx context.Context, id string) (*job.Job, error) {
	return nil, nil
}

// TestEnqueueEnvelopeFn_JobEnqueueTimeout_Returns503 pins the P0 async
// contract: when the job broker cannot enqueue within the configured
// timeout, the endpoint must return HTTP 503 with error JOB_ENQUEUE_TIMEOUT
// instead of hanging.
func TestEnqueueEnvelopeFn_JobEnqueueTimeout_Returns503(t *testing.T) {
	// Not parallel: mutates the package-level enqueueTimeout.

	// Use a very short timeout so the test runs quickly.
	originalTimeout := enqueueTimeout
	enqueueTimeout = 50 * time.Millisecond
	defer func() { enqueueTimeout = originalTimeout }()

	jobsSvc := &slowJobsService{blockDuration: 5 * time.Second}
	handler := NewScriptFlowHandler(newMinimalScriptFlowDepsForTest(jobsSvc))
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	req := httptest.NewRequest("POST", "/api/script/generate", strings.NewReader(`{"version":2,"preset":"custom","items":[{"id":"timeout-test","title":"Timeout Test","language":"en","script_params":{"target_words":150},"source":{"type":"text","topic":"timeout","source_text":"timeout fixture"}}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-timeout-1")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code, "expected 503 JOB_ENQUEUE_TIMEOUT")
	assert.Contains(t, w.Body.String(), `"ok":false`)
	assert.Contains(t, w.Body.String(), `"error":"JOB_ENQUEUE_TIMEOUT"`)
}
