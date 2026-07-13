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
	"go.uber.org/zap"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
)

type slowSubmissionService struct {
	blockDuration time.Duration
}

var _ interface {
	Submit(context.Context, opsapp.SubmitRequest) (*opsapp.SubmitResult, error)
} = (*slowSubmissionService)(nil)

func (s *slowSubmissionService) Submit(ctx context.Context, req opsapp.SubmitRequest) (*opsapp.SubmitResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.blockDuration):
		return &opsapp.SubmitResult{
			Operation: &domainops.Operation{JobID: "job-slow"},
		}, nil
	}
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

	handler := NewHandlerGenerate(
		&slowSubmissionService{blockDuration: 5 * time.Second},
		zap.NewNop(),
		nil,
	)
	router := gin.New()
	rg := router.Group("/api/script")
	handler.GenerateRoute(rg)

	req := httptest.NewRequest("POST", "/api/script/generate", strings.NewReader(`{"version":2,"preset":"custom","items":[{"id":"timeout-test","title":"Timeout Test","language":"en","script_params":{"target_words":150},"source":{"type":"text","topic":"timeout","source_text":"timeout fixture"}}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-timeout-1")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code, "expected 503 JOB_ENQUEUE_TIMEOUT")
	assert.Contains(t, w.Body.String(), `"ok":false`)
	assert.Contains(t, w.Body.String(), `"error":"JOB_ENQUEUE_TIMEOUT"`)
}
