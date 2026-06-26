package script

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	pkgmw "github.com/Marcuss-ops/PipelineGen/pkg/middleware"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
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

// fakeGenerationService is a testing double for GenerationService.
type fakeGenerationService struct {
	nextJobID string
	err       error
}

func (f *fakeGenerationService) EnqueueFromClips(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.nextJobID == "" {
		f.nextJobID = "j-123"
	}
	return &scripts.FromClipsResult{OK: true, JobID: f.nextJobID, JobStatus: "queued"}, nil
}

func (f *fakeGenerationService) EnqueueWithImages(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.nextJobID == "" {
		f.nextJobID = "j-123"
	}
	return &scripts.FromClipsResult{OK: true, JobID: f.nextJobID, JobStatus: "queued"}, nil
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
	handler := NewHandler(NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc}))
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
		{"POST", "/api/script/generate-from-catalog"},
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

	removedRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/script/generate-from-clips"},
		{"POST", "/api/script/generate-with-images"},
		{"POST", "/api/script/generate-batch"},
		{"GET", "/api/script/generate-batch/progress"},
	}

	for _, removed := range removedRoutes {
		key := fmt.Sprintf("%s %s", removed.method, removed.path)
		assert.False(t, routeMap[key], "removed route %s %s must NOT be registered when all gates are off", removed.method, removed.path)
	}
}

func TestScriptRoutes_ImagesEnabled(t *testing.T) {
	t.Parallel()

	handler := NewScriptFlowHandler(ScriptFlowDeps{GenService: &fakeGenerationService{}, Gates: FeatureGates{ScriptImagesEnabled: true}})
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	routes := router.Routes()
	routeMap := make(map[string]bool)
	for _, r := range routes {
		routeMap[r.Method+" "+r.Path] = true
	}

	assert.True(t, routeMap["POST /api/script/generate-with-images"], "generate-with-images must stay mounted when only image scripts are enabled")
	assert.False(t, routeMap["POST /api/script/generate-from-clips"], "generate-from-clips must stay disabled when clips feature is off")
	assert.False(t, routeMap["POST /api/script/generate-batch"], "generate-batch must stay disabled when docs feature is off")
	assert.False(t, routeMap["GET /api/script/generate-batch/progress"], "generate-batch/progress must stay disabled when docs feature is off")
}

func TestScriptRoutes_DocsOnlyKeepsGenerateBatch(t *testing.T) {
	t.Parallel()

	handler := NewScriptFlowHandler(ScriptFlowDeps{Gates: FeatureGates{ScriptDocsEnabled: true}})
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	routes := router.Routes()
	routeMap := make(map[string]bool)
	for _, r := range routes {
		routeMap[r.Method+" "+r.Path] = true
	}

	assert.True(t, routeMap["POST /api/script/generate-batch"], "generate-batch must stay mounted when only docs scripts are enabled")
	assert.True(t, routeMap["GET /api/script/generate-batch/progress"], "generate-batch/progress must stay mounted when only docs scripts are enabled")
	assert.False(t, routeMap["POST /api/script/generate-with-images"], "generate-with-images must stay disabled when images feature is off")
	assert.False(t, routeMap["POST /api/script/generate-from-clips"], "generate-from-clips must stay disabled when clips feature is off")
}

func TestScriptRoutes_ClipsOnlyKeepsGenerateFromClips(t *testing.T) {
	t.Parallel()

	handler := NewScriptFlowHandler(ScriptFlowDeps{GenService: &fakeGenerationService{}, Gates: FeatureGates{ScriptClipsEnabled: true}})
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	routes := router.Routes()
	routeMap := make(map[string]bool)
	for _, r := range routes {
		routeMap[r.Method+" "+r.Path] = true
	}

	assert.True(t, routeMap["POST /api/script/generate-from-clips"], "generate-from-clips must stay mounted when only clip scripts are enabled")
	assert.False(t, routeMap["POST /api/script/generate-with-images"], "generate-with-images must stay disabled when images feature is off")
	assert.False(t, routeMap["POST /api/script/generate-batch"], "generate-batch must stay disabled when docs feature is off")
	assert.False(t, routeMap["GET /api/script/generate-batch/progress"], "generate-batch/progress must stay disabled when docs feature is off")
}

func TestScriptFlowAsyncRoutes_EnqueueJobs(t *testing.T) {
	t.Parallel()

	jobsSvc, fake := newTestJobsService(t)
	handler := NewHandler(NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc}))
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	req := httptest.NewRequest("POST", "/api/script/curate", strings.NewReader(`{"query":"why observability matters","language":"it"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, fake.lastReq)
	assert.Equal(t, "media.curate", fake.lastReq.Type)
	payloadBytes, err := json.Marshal(fake.lastReq.Payload)
	assert.NoError(t, err)
	assert.Contains(t, string(payloadBytes), `"language":"en"`)
	assert.Contains(t, w.Body.String(), `"async":true`)
	assert.Contains(t, w.Body.String(), `"job_id":"job-123"`)
}

func TestScriptFlowCatalogRoute_EnqueuesCatalogJob(t *testing.T) {
	t.Parallel()

	jobsSvc, fake := newTestJobsService(t)
	handler := NewHandler(NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc}))
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	body := `{"topic":"observability","max_clips":4,"min_coverage":0.5,"title":"Catalog Script","languages":["it","en"]}`
	req := httptest.NewRequest("POST", "/api/script/generate-from-catalog", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, fake.lastReq)
	assert.Equal(t, "script.generate_from_catalog", fake.lastReq.Type)
	payloadBytes, err := json.Marshal(fake.lastReq.Payload)
	assert.NoError(t, err)
	assert.Contains(t, string(payloadBytes), `"topic":"observability"`)
	assert.Contains(t, string(payloadBytes), `"language":"en"`)
	assert.Contains(t, string(payloadBytes), `"languages":["it","en"]`)
	assert.Contains(t, w.Body.String(), `"async":true`)
}

// ── RequireAdminToken middleware ──────────────────────────────────────────

func TestRequireAdminToken_NoToken_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	provider := &pkgmw.TokenSecurityAdapter{Enable: true, Admin: "secret"}
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

	provider := &pkgmw.TokenSecurityAdapter{Enable: true, Admin: "secret"}
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

	provider := &pkgmw.TokenSecurityAdapter{Enable: false}
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

// ── Valid request → 200 ─────────────────────────────────────────────────────

func TestHandler_ValidRequest_Returns200(t *testing.T) {
	t.Parallel()

	gen := &fakeGenerationService{}
	handler := NewScriptFlowHandler(ScriptFlowDeps{GenService: gen})
	router := gin.New()
	router.POST("/test", handler.GenerateFromClips)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"topic":"test","language":"en"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestHandler_GenerateBatch_DirectHandler verifies that the handler
// correctly routes to the use case when h.genBatchUC is wired but
// downstream dependencies (batch service + ollama engine + jobs facade)
// are nil. The use case fails with an internal error and the handler
// maps it to 500; this test pins the routing surface so future
// refactors cannot regress it to a silent nil-deref.
//
// PR-A (June 2026): ScriptFlowDeps dropped the Batch field (the
// handler no longer talks to BatchService directly — the use case
// does). Tests now construct the use case explicitly and pass it via
// GenBatchUC so the routing surface reflects the post-PR-A wiring.
func TestHandler_GenerateBatch_DirectHandler(t *testing.T) {
	t.Parallel()

	uc := scripts.NewGenerateBatchUseCase(
		nil, zap.NewNop(), nil,
		scripts.NewBatchService(nil, nil, nil, nil, nil, nil, nil),
		"",
	)
	handler := NewScriptFlowHandler(ScriptFlowDeps{
		GenBatchUC: uc,
		Gates:      FeatureGates{ScriptDocsEnabled: true},
		Log:        zap.NewNop(),
	})
	router := gin.New()
	router.POST("/test", handler.GenerateBatch)

	// Body intentionally omits `items` + `batch_topics` so the request
	// path reaches the use case without engine-driven batch building.
	body := `{"doc_title":"Batch probe","async":false}`
	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	// Engineer-engine-missing response: use case returns non-nil error
	// that mapBatchError translates to 5xx. We assert the status class
	// (5xx) rather than exact code because error-mapping is delegated
	// to scripts.GenerateBatchUseCase.Run and the handler's
	// mapBatchError helper, and those can refine the status code over
	// time without breaking this regression test.
	assert.True(t, w.Code >= 500 && w.Code < 600, "expected 5xx from nil-batch-service path, got %d body=%s", w.Code, w.Body.String())
}

// TestHandler_GetBatchProgress_DirectHandler verifies the progress
// endpoint short-circuits to 503 when jobs dependency is unwired
// (production wires root.Jobs.Service; this test mirrors that
// dependency via ScriptFlowDeps.Jobs and exercises the nil-route).
func TestHandler_GetBatchProgress_DirectHandler(t *testing.T) {
	t.Parallel()

	handler := NewScriptFlowHandler(ScriptFlowDeps{Gates: FeatureGates{ScriptDocsEnabled: true}})
	router := gin.New()
	router.GET("/test", handler.GetBatchProgress)

	req := httptest.NewRequest("GET", "/test?job_id=job-123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── /generate-with-images parity coverage ─────────────────────────────────

func TestHandler_GenerateWithImages_ErrorMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"ErrInvalidPayload → 400", scriptpkg.ErrInvalidPayload, http.StatusBadRequest},
		{"ErrValidation → 400", scriptpkg.ErrValidation, http.StatusBadRequest},
		{"ErrUnavailable → 503", scriptpkg.ErrUnavailable, http.StatusServiceUnavailable},
		{"ErrConflict → 409", scriptpkg.ErrConflict, http.StatusConflict},
		{"unexpected → 500", errors.New("unexpected boom"), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gen := &fakeGenerationService{err: tc.err}
			handler := NewScriptFlowHandler(ScriptFlowDeps{GenService: gen})
			router := gin.New()
			router.POST("/test", handler.GenerateWithImages)

			req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"topic":"test","language":"en"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)
			assert.Equal(t, tc.wantStatus, w.Code, "wrong HTTP status for error: %v", tc.err)
		})
	}
}

func TestHandler_GenerateWithImages_ValidRequest_Returns200(t *testing.T) {
	t.Parallel()

	gen := &fakeGenerationService{}
	handler := NewScriptFlowHandler(ScriptFlowDeps{GenService: gen})
	router := gin.New()
	router.POST("/test", handler.GenerateWithImages)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"topic":"test","language":"en"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"job_id":"j-123"`)
	assert.Contains(t, w.Body.String(), `"status":"queued"`)
	assert.Contains(t, w.Body.String(), `"ok":true`)
}

func TestHandler_NilGenerationService_GenerateWithImages_Returns503(t *testing.T) {
	t.Parallel()

	handler := NewScriptFlowHandler(ScriptFlowDeps{})
	router := gin.New()
	router.POST("/test", handler.GenerateWithImages)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"topic":"test","language":"en"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
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
