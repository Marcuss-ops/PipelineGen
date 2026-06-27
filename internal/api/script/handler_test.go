package script

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

func init() { gin.SetMode(gin.TestMode) }

// fakeJobsService is a testing double for job.Service. Only Enqueue is
// functional (records lastReq and returns a canned Job so async-enqueue
// HTTP paths return 200); the remaining methods return errors so the
// handler's nil/503 short-circuits are exercised without touching real
// infrastructure.
type fakeJobsService struct {
	lastReq   *job.EnqueueRequest
	nextJobID string
}

// Compile-time assertion: fakeJobsService satisfies job.Service.
var _ job.Service = (*fakeJobsService)(nil)

func (f *fakeJobsService) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	f.lastReq = req
	if f.nextJobID == "" {
		f.nextJobID = "job-123"
	}
	return &job.Job{ID: f.nextJobID, Status: job.StatusQueued, Type: req.Type}, nil
}

func (f *fakeJobsService) Get(ctx context.Context, id string) (*job.Job, error) {
	return nil, errors.New("fakeJobsService: Get not implemented")
}

func (f *fakeJobsService) Cancel(ctx context.Context, id string) error {
	return errors.New("fakeJobsService: Cancel not implemented")
}

func (f *fakeJobsService) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	return nil, errors.New("fakeJobsService: List not implemented")
}

func (f *fakeJobsService) IsTerminal(status job.Status) bool {
	return status.IsTerminal()
}

func (f *fakeJobsService) RegisterHandler(jobType string, handler any) error {
	return errors.New("fakeJobsService: RegisterHandler not implemented")
}

func (f *fakeJobsService) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	return nil, errors.New("fakeJobsService: ListEvents not implemented")
}

// newTestJobsService returns the same struct value as both job.Service
// (the iface placeholder for ScriptFlowDeps.Jobs) and *fakeJobsService
// (for tests that need to introspect the captured lastReq after Enqueue).
func newTestJobsService(t *testing.T) (job.Service, *fakeJobsService) {
	t.Helper()
	fake := &fakeJobsService{}
	return fake, fake
}

// ── Route compatibility ────────────────────────────────────────

func TestScriptRoutes_Compatibility(t *testing.T) {
	t.Parallel()

	jobsSvc, _ := newTestJobsService(t)
	handler := NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc})
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	routes := router.Routes()

	routeMap := make(map[string]bool)
	for _, r := range routes {
		key := fmt.Sprintf("%s %s", r.Method, r.Path)
		routeMap[key] = true
	}

	expectedRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/script/generate"},
		// Legacy routes — now registered as deprecated adapters (PR 11).
		{"POST", "/api/script/generate-from-clips"},
		{"POST", "/api/script/generate-with-images"},
		{"POST", "/api/script/generate-batch"},
		{"POST", "/api/script/curate"},
		{"GET", "/api/script/jobs/:job_id"},
		{"GET", "/api/script/jobs/:job_id/full"},
		{"POST", "/api/script/:id/sections/:section_id/regenerate"},
		{"POST", "/api/script/cache/evict"},
	}

	for _, want := range expectedRoutes {
		key := fmt.Sprintf("%s %s", want.method, want.path)
		assert.True(t, routeMap[key], "required route %s %s must be registered", want.method, want.path)
	}
}

func TestScriptFlowAsyncRoutes_EnqueueJobs(t *testing.T) {
	t.Parallel()

	jobsSvc, fake := newTestJobsService(t)
	handler := NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc})
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	req := httptest.NewRequest("POST", "/api/script/curate", strings.NewReader(`{"query":"why observability matters","language":"it"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// PR 11: /curate now enqueues as script.generate (deprecation adapter).
	assert.NotNil(t, fake.lastReq)
	assert.Equal(t, "script.generate", fake.lastReq.Type)
	assert.Contains(t, w.Header().Get("X-Deprecated"), "true")
	assert.Contains(t, w.Body.String(), `"job_id":"job-123"`)
	assert.Contains(t, w.Body.String(), `"status_url":"/api/jobs/job-123/full"`)
}

// ── RequireAdminToken middleware ──────────────────────────────────────────

func TestRequireAdminToken_NoToken_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	provider := &middleware.TokenSecurityAdapter{Enable: true, Admin: "secret"}
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAdminToken_CorrectToken_Succeeds(t *testing.T) {
	t.Parallel()

	provider := &middleware.TokenSecurityAdapter{Enable: true, Admin: "secret"}
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-Velox-Admin-Token", "secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAdminToken_DisabledByEnableFlag_NoAuth(t *testing.T) {
	t.Parallel()

	provider := &middleware.TokenSecurityAdapter{Enable: false}
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAdminToken_NilProvider_AllowAll(t *testing.T) {
	t.Parallel()

	var provider AdminTokenProvider = nil
	router := gin.New()
	router.Use(RequireAdminToken(provider))
	router.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ── NewScriptFlowHandler defensive-constructor regression ─────────────────

// TestNewScriptFlowHandler_NilLog_DefaultsToNoopLogger pins the
// defensive-constructor contract: when ScriptFlowDeps.Log is nil, the
// constructor must NOT propagate a nil logger (which would panic on any
// downstream log call). zap.NewNop() is the silent-drop contract that
// keeps tests + dev-mode wiring crash-free.
func TestNewScriptFlowHandler_NilLog_DefaultsToNoopLogger(t *testing.T) {
	t.Parallel()

	handler := NewScriptFlowHandler(ScriptFlowDeps{})
	assert.NotNil(t, handler.log, "log must default to a no-op logger when ScriptFlowDeps.Log is nil")
	// Touch a Warn call to confirm the no-op logger is functional.
	handler.log.Warn("ping — defensive-constructor smoke test")
}
