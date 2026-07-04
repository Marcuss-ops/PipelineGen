// Package register (test) — handler_test.go
//
// TDD coverage for the pre-flight URL validation gate added to
// RegisterFromYouTube (POST /api/media/register-from-youtube).
//
// The gate uses the canonical pkg/urlutil::ExtractVideoID typed-error
// contract so an invalid URL returns HTTP 400 (client-bad) BEFORE the
// service layer launches the yt-dlp subprocess (which would otherwise
// bubble exit-1 as HTTP 500 / server-crash).
//
// godlike/07 minimum-blast-radius: this PR locks the SINGLE-endpoint
// gate only. The BatchRegisterFromYouTube per-item gate is forward-
// pointer (separate PR to surface per-item preflight errors in
// results[] without invoking the orchestrator on junk input).
//
// Test coverage rationale:
//   - Empty URL is NOT tested here because Gin's `binding:"required"`
//     validator intercepts `{"url": ""}` BEFORE the preflight gate, so
//     the preflight's ExtractVideoID empty-URL branch is unreachable
//     via the standard request flow. Gin's response surfaces the
//     "Field validation for 'URL' failed on the 'required' tag" message
//     (covered at the body-binding layer; not the preflight gate).
//   - Each invalid-URL test below exercises a distinct branch of
//     pkg/urlutil/urlutil.go::ExtractVideoID that IS reachable when the
//     URL field is non-empty but malformed.
package register

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestHandler returns a handler built via the canonical NewHandler
// ctor so Idempotency middleware is wired (no-op pass-through when
// nil is passed). DO NOT use &Handler{...} directly — Gin panics on
// nil middleware installed via r.POST("/path", h.Idempotency, handler).
func newTestHandler() *Handler {
	return NewHandler(nil, zap.NewNop(), nil)
}

// newTestHandlerWithSvc returns a handler with a non-nil svc. Used by
// the invariant-pin test where svc=nil would suppress the gate that
// fires AFTER the svc call (a regression would NOT panic on svc=nil).
func newTestHandlerWithSvc(svc *sourcing.Service) *Handler {
	return NewHandler(svc, zap.NewNop(), nil)
}

// newTestRouter mounts the handler's canonical routes on a fresh
// gin engine for in-memory httptest serving. Uses /api/media prefix
// matching production routes.
func newTestRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/media")
	h.RegisterRoutes(g)
	return r
}

// ── RegisterFromYouTube preflight TDD tests ────────────────────────────────

// TestRegisterFromYouTube_InvalidURL_NotYouTubeDomain_Returns400 locks
// the canonical bug case: a URL pointing at a non-YouTube domain
// returns HTTP 400 (client-bad) instead of HTTP 500 (server-crash
// from downstream yt-dlp exit-1).
func TestRegisterFromYouTube_InvalidURL_NotYouTubeDomain_Returns400(t *testing.T) {
	h := newTestHandler()
	r := newTestRouter(h)

	body := `{"url": "https://not-a-real-url.example.com/no-vid"}`
	req := httptest.NewRequest("POST", "/api/media/register-from-youtube", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid YouTube URL")
}

// TestRegisterFromYouTube_InvalidURL_UnrecognizedYouTubePath_Returns400
// covers ExtractVideoID "unrecognized youtube.com URL path" branch.
func TestRegisterFromYouTube_InvalidURL_UnrecognizedYouTubePath_Returns400(t *testing.T) {
	h := newTestHandler()
	r := newTestRouter(h)

	body := `{"url": "https://youtube.com/some/random/path"}`
	req := httptest.NewRequest("POST", "/api/media/register-from-youtube", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid YouTube URL")
}

// TestRegisterFromYouTube_InvalidURL_WatchMissingVParam_Returns400
// covers ExtractVideoID "no video ID in watch URL" branch.
func TestRegisterFromYouTube_InvalidURL_WatchMissingVParam_Returns400(t *testing.T) {
	h := newTestHandler()
	r := newTestRouter(h)

	body := `{"url": "https://www.youtube.com/watch"}`
	req := httptest.NewRequest("POST", "/api/media/register-from-youtube", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid YouTube URL")
}

// TestRegisterFromYouTube_ValidURL_ShortsURL_ReturnsSVCPassthrough
// covers the happy-path branch: a canonical /shorts/ URL passes
// the preflight gate and reaches the svc layer (which returns 503
// because svc is nil — but that is BELOW the gate). Proves the gate
// does NOT reject canonical valid URLs.
func TestRegisterFromYouTube_ValidURL_ShortsURL_ReturnsSVCPassthrough(t *testing.T) {
	h := newTestHandler()
	r := newTestRouter(h)

	body := `{"url": "https://www.youtube.com/shorts/9u4T_o3FxOU"}`
	req := httptest.NewRequest("POST", "/api/media/register-from-youtube", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// svc=nil → canonical 503 (svc-not-wired). 503 PROVES the preflight
	// gate passed (it would have been 400 if ExtractVideoID rejected).
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "not wired")
}

// TestRegisterFromYouTube_ValidURL_YouTuBeShortURL_ReturnsSVCPassthrough
// covers the youtu.be canonical URL branch.
func TestRegisterFromYouTube_ValidURL_YouTuBeShortURL_ReturnsSVCPassthrough(t *testing.T) {
	h := newTestHandler()
	r := newTestRouter(h)

	body := `{"url": "https://youtu.be/9u4T_o3FxOU"}`
	req := httptest.NewRequest("POST", "/api/media/register-from-youtube", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "not wired")
}

// TestRegisterFromYouTube_PreflightGated_BeforeSVCCall pins the canonical
// bug surface for the pre-fix vs post-fix behavior change. The empty svc
// is LOAD-BEARING — if a future agent regresses by moving the preflight
// AFTER svc call, the empty Service panics on its nil deps so NotPanics
// fails. DO NOT replace newTestHandlerWithSvc with newTestHandler (svc=nil
// would suppress the regression signal).
func TestRegisterFromYouTube_PreflightGated_BeforeSVCCall(t *testing.T) {
	h := newTestHandlerWithSvc(&sourcing.Service{})
	r := newTestRouter(h)

	body := `{"url": "https://youtube.com/junk/path/that/looks/canonical"}`
	req := httptest.NewRequest("POST", "/api/media/register-from-youtube", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// NotPanics PROVES the preflight gate short-circuited BEFORE reaching
	// svc.RegisterFromYouTube. If the gate ever regresses (e.g. moved after
	// svc call), the test will FAIL with a panic from the empty service.
	require.NotPanics(t, func() {
		r.ServeHTTP(w, req)
	}, "preflight must gate svc dispatch; regression would panic on dummySvc")

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "invalid YouTube URL")
}
