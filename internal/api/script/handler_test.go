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

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	pkgmw "github.com/Marcuss-ops/PipelineGen/pkg/middleware"
)

func init() { gin.SetMode(gin.TestMode) }

// ── Error mapping (section 10) ──────────────────────────────────────────────

type fakeGenerationService struct {
	err error
}

func (f *fakeGenerationService) EnqueueFromClips(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &scripts.FromClipsResult{OK: true, JobID: "j-123", JobStatus: "queued"}, nil
}

func (f *fakeGenerationService) EnqueueWithImages(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error) {
	return f.EnqueueFromClips(ctx, spec)
}

// fakeJobsService is a testing double for job.Service. Only Enqueue is
// functional (records lastReq and returns a canned Job so async-enqueue
// HTTP paths return 200); the remaining methods return errors so the
// handler's nil/503 short-circuits are exercised without touching real
// infrastructure. Replaces the legacy fakeJobEnqueuer + job.NewService
// construction that previously pointed at a non-existent
// job.ErrNotWired + pointer-to-interface (*job.Service) which the
// domain layer never exposed.
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

func TestHandler_ErrorMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"ErrInvalidPayload → 400", scriptpkg.ErrInvalidPayload, http.StatusBadRequest},
		{"ErrValidation → 400", scriptpkg.ErrValidation, http.StatusBadRequest},
		{"ErrUnsupportedVersion → 400", scriptpkg.ErrUnsupportedVersion, http.StatusBadRequest},
		{"ErrUnavailable → 503", scriptpkg.ErrUnavailable, http.StatusServiceUnavailable},
		{"ErrConflict → 409", scriptpkg.ErrConflict, http.StatusConflict},
		{"unexpected → 500", errors.New("unexpected boom"), http.StatusInternalServerError},
		// TODO (spec §10): add context.Canceled, context.DeadlineExceeded,
		// ErrGenerationBusy (429/503), ErrFolderResolution (502/500)
		// once mapErrorToHTTP is updated to handle them.
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gen := &fakeGenerationService{err: tc.err}
			handler := NewHandler(nil, gen, FeatureGates{})
			router := gin.New()
			router.POST("/test", handler.GenerateFromClips)

			req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"topic":"test","language":"en"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)
			assert.Equal(t, tc.wantStatus, w.Code, "wrong HTTP status for error: %v", tc.err)
		})
	}
}

func TestHandler_NilGenerationService_Returns503(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, nil, FeatureGates{})
	router := gin.New()
	router.POST("/test", handler.GenerateFromClips)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"topic":"test","language":"en"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandler_NilScriptFlowHandler_GeneratesBatch503(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, &fakeGenerationService{}, FeatureGates{})
	router := gin.New()
	router.POST("/test", handler.GenerateBatch)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// ── Route compatibility (section 12) ────────────────────────────────────────

func TestScriptRoutes_Compatibility(t *testing.T) {
	t.Parallel()

	jobsSvc, _ := newTestJobsService(t)
	handler := NewHandler(NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc}), &fakeGenerationService{}, FeatureGates{ScriptClipsEnabled: true, ScriptDocsEnabled: true, ScriptImagesEnabled: true})
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	routes := router.Routes()

	routeMap := make(map[string]string)
	for _, r := range routes {
		key := fmt.Sprintf("%s %s", r.Method, r.Path)
		routeMap[key] = r.Method
	}

	expectedRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/script/generate-from-clips"},
		{"POST", "/api/script/generate-with-images"},
		{"POST", "/api/script/generate-batch"},
		{"GET", "/api/script/generate-batch/progress"},
		{"POST", "/api/script/generate-from-catalog"},
		{"POST", "/api/script/curate"},
		{"GET", "/api/script/jobs/:job_id"},
		{"GET", "/api/script/jobs/:job_id/full"},
		{"POST", "/api/script/:id/sections/:section_id/regenerate"},
		{"POST", "/api/script/cache/evict"},
	}

	for _, want := range expectedRoutes {
		key := fmt.Sprintf("%s %s", want.method, want.path)
		_, exists := routeMap[key]
		assert.True(t, exists, "required route %s %s must be registered", want.method, want.path)
	}
}

func TestScriptRoutes_ImagesOnlyKeepsGenerateWithImages(t *testing.T) {
	t.Parallel()

	handler := NewHandler(nil, &fakeGenerationService{}, FeatureGates{ScriptImagesEnabled: true})
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
}

func TestScriptFlowAsyncRoutes_EnqueueJobs(t *testing.T) {
	t.Parallel()

	jobsSvc, fake := newTestJobsService(t)
	handler := NewHandler(NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc}), &fakeGenerationService{}, FeatureGates{})
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
	handler := NewHandler(NewScriptFlowHandler(ScriptFlowDeps{Jobs: jobsSvc}), &fakeGenerationService{}, FeatureGates{})
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

	// PG-006.1 (round-2): pass-through semantics now come from the
	// Enable=false flag, not from Admin emptiness (which admin_token.go
	// uses for the fail-closed 500 misconfig path). Combinations of
	// Admin content + Enable=false must short-circuit to pass-through
	// even with admin_token-content present.
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
	handler := NewHandler(nil, gen, FeatureGates{})
	router := gin.New()
	router.POST("/test", handler.GenerateFromClips)

	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"topic":"test","language":"en"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandler_GenerateBatch_DelegatesToInner(t *testing.T) {
	t.Parallel()

	inner := NewScriptFlowHandler(ScriptFlowDeps{
		GenerateBatch: scripts.NewGenerateBatchUseCase(
			nil,
			nil,
			nil,
			scripts.NewBatchService(nil, nil, nil, nil, nil, nil, nil),
			"",
		),
	})
	handler := NewHandler(inner, &fakeGenerationService{}, FeatureGates{ScriptDocsEnabled: true})
	router := gin.New()
	router.POST("/test", handler.GenerateBatch)

	body := `{"doc_title":"Batch probe","async":false,"items":[{"topic":"t1"}],"batch_topics":[{"topic":"t1"}]}`
	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"doc_title":"Batch probe"`)
}

func TestHandler_GetBatchProgress_DelegatesToInner(t *testing.T) {
	t.Parallel()

	inner := NewScriptFlowHandler(ScriptFlowDeps{})
	handler := NewHandler(inner, &fakeGenerationService{}, FeatureGates{ScriptDocsEnabled: true})
	router := gin.New()
	router.GET("/test", handler.GetBatchProgress)

	req := httptest.NewRequest("GET", "/test?job_id=job-123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
