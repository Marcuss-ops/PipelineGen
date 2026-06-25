// Package mediasearch (api) — handler_test.go covers the HTTP
// transport against an in-process gin engine. The real
// application-layer orchestrator is stubbed via a Searcher
// interface (defined in handler.go) so this file does not depend
// on VectorSearchPort / MediaReadRepository.
//
// What is covered:
//   - missing workspace in context → 403
//   - default workspace in context → 403
//   - bad mode → 400
//   - empty query → 400 (gin binding)
//   - happy path → 200 with the Searcher's response echoed verbatim
//   - service error → 500
package mediasearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job/workspace"
	"github.com/gin-gonic/gin"
)

// ── Stub Searcher ──────────────────────────────────────────────────────

type stubSearcher struct {
	resp *mediasearch.MediaSearchResponse
	err  error
	got  mediasearch.MediaSearchRequest
}

func (s *stubSearcher) Search(ctx context.Context, req mediasearch.MediaSearchRequest) (*mediasearch.MediaSearchResponse, error) {
	s.got = req
	return s.resp, s.err
}

// ── Test harness ──────────────────────────────────────────────────────

func newTestRouter(h *Handler, scope workspace.Scope) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Inject the scope directly so we don't need the full middleware
	// chain here; the handler reads it via middleware.ScopeFromContext.
	deps := []gin.HandlerFunc{
		func(c *gin.Context) {
			c.Set("workspace_scope", scope)
			c.Next()
		},
	}
	r.POST("/internal/v1/media/search", append(deps, h.Search)...)
	return r
}

// ── Tests ──────────────────────────────────────────────────────────────

func TestSearch_403WhenWorkspaceMissing(t *testing.T) {
	svc := &stubSearcher{}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.Scope{}) // empty workspace
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query": "test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing workspace)", w.Code)
	}
	if svc.got.Query != "" {
		t.Errorf("service should not be called, but got query %q", svc.got.Query)
	}
}

func TestSearch_403WhenWorkspaceIsDefault(t *testing.T) {
	svc := &stubSearcher{}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.DefaultScope())
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query": "test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (default workspace)", w.Code)
	}
}

func TestSearch_400OnInvalidMode(t *testing.T) {
	svc := &stubSearcher{}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.NewScope("ws-1", "proj-1"))
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query": "test", "mode": "fancy"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (invalid mode)", w.Code)
	}
}

func TestSearch_400OnMissingQuery(t *testing.T) {
	// gin binding rules: query is `binding:"required"`.
	svc := &stubSearcher{}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.NewScope("ws-1", "proj-1"))
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing query)", w.Code)
	}
}

func TestSearch_HappyPath(t *testing.T) {
	svc := &stubSearcher{
		resp: &mediasearch.MediaSearchResponse{
			OK:    true,
			Count: 1,
			Hits: []mediasearch.SearchHit{{
				AssetID:     "asset-1",
				Score:       0.92,
				DeliveryURL: "https://signed.example/asset-1?sig=...",
			}},
			Query: mediasearch.QueryEcho{Normalized: "test", Mode: "hybrid"},
		},
	}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.NewScope("ws-1", "proj-1"))
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query":"test","limit":5,"filters":{"source":"stock","tags":["nature"]}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// Decode and check the response shape is the orchestrator's.
	var got mediasearch.MediaSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Count != 1 || got.Hits[0].AssetID != "asset-1" {
		t.Errorf("response echo broken: %+v", got)
	}
	// Cross-check the service received the right DTO.
	if svc.got.Query != "test" ||
		svc.got.Workspace.WorkspaceID != "ws-1" ||
		svc.got.Workspace.ProjectID != "proj-1" ||
		svc.got.Limit != 5 ||
		svc.got.Filters.Source != "stock" ||
		len(svc.got.Filters.Tags) != 1 ||
		svc.got.Filters.Tags[0] != "nature" {
		t.Errorf("service received: %+v", svc.got)
	}
}

func TestSearch_500OnServiceError(t *testing.T) {
	svc := &stubSearcher{err: errors.New("boom")}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.NewScope("ws-1", "proj-1"))
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestSearch_403WhenServiceReturnsErrMissingWorkspace(t *testing.T) {
	// Even if a non-default scope is supplied, a defensive
	// service that returns ErrMissingWorkspace should map to 403.
	svc := &stubSearcher{err: mediasearch.ErrMissingWorkspace}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.NewScope("ws-1", "proj-1"))
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 on ErrMissingWorkspace", w.Code)
	}
}

func TestSearch_DefaultModeIsHybrid(t *testing.T) {
	svc := &stubSearcher{resp: &mediasearch.MediaSearchResponse{}}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.NewScope("ws-1", "proj-1"))
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.got.Mode != mediasearch.SearchModeHybrid {
		t.Errorf("default mode = %q, want hybrid", svc.got.Mode)
	}
}

func TestSearch_ExplicitANNMode(t *testing.T) {
	svc := &stubSearcher{resp: &mediasearch.MediaSearchResponse{}}
	h := NewHandler(svc, nil)
	r := newTestRouter(h, workspace.NewScope("ws-1", "proj-1"))
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/media/search",
		strings.NewReader(`{"query":"test","mode":"ann"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.got.Mode != mediasearch.SearchModeANN {
		t.Errorf("mode = %q, want ann", svc.got.Mode)
	}
}
