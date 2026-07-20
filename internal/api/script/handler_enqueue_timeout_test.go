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

// TestEnqueueEnvelopeFn_JobEnqueueTimeout_Returns504 pins the P0 async
// contract: when the job broker cannot enqueue within the configured
// timeout, the endpoint must return HTTP 504 with error JOB_ENQUEUE_TIMEOUT
// instead of hanging.
//
// Status code mapping rationale (P0 verdetto): godlike/07 fail-closed requires
// a context timeout to surface as 504 (gateway timeout), not 503 (service
// unavailable). The submitter MAY be congested but it is still a runtime
// timeout; clients should retry with backoff on 504. The 503 status is
// reserved for opsapp.ErrSubmitQueueFull / ErrUnavailable (see
// handler_generate_response.go:45-52,59,63).
func TestEnqueueEnvelopeFn_JobEnqueueTimeout_Returns504(t *testing.T) {
	// Not parallel: mutates the package-level enqueueTimeout.

	// Use a very short timeout so the test runs quickly.
	originalTimeout := enqueueTimeout
	enqueueTimeout = 50 * time.Millisecond
	defer func() { enqueueTimeout = originalTimeout }()

	handler := NewHandlerGenerate(
		&slowSubmissionService{blockDuration: 5 * time.Second},
		nil, // scriptgenSvc (GenerationRunStarter) — legacy direct-submit fallback path
		nil, // factory — NewHandlerGenerate defaults to NewSubmitRequestFactory()
		zap.NewNop(),
		nil, // validator falls back to NewDefaultPayloadValidator() inside NewHandlerGenerate
	)
	router := gin.New()
	rg := router.Group("/api/script")
	handler.GenerateRoute(rg)

	req := httptest.NewRequest("POST", "/api/script/generate", strings.NewReader(`{"version":2,"preset":"custom","items":[{"id":"timeout-test","title":"Timeout Test","language":"en","script_params":{"target_words":150},"source":{"type":"text","topic":"timeout","source_text":"timeout fixture"}}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-timeout-1")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusGatewayTimeout, w.Code,
		"expected 504 JOB_ENQUEUE_TIMEOUT per P0 verdetto (godlike/07 fail-closed; see handler_generate_response.go:45-59)")
	assert.Contains(t, w.Body.String(), `"ok":false`)
	assert.Contains(t, w.Body.String(), `"error":"JOB_ENQUEUE_TIMEOUT"`)
}
