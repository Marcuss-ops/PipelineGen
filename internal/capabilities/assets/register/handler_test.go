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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestHandler returns a handler built via the canonical NewHandler
// ctor so Idempotency middleware is wired (no-op pass-through when
// nil is passed). DO NOT use &Handler{...} directly — Gin panics on
// nil middleware installed via r.POST("/path", h.Idempotency, handler).
//
// PR-DRIVE-AVAILABILITY-GATE (2026-07-04): a 4th-arg nil driveChecker
// is passed intentionally — NewHandler's nil-tolerant default returns
// an always-fail closure, so any folder_id traffic in these tests
// surfaces 503 (the canonical godlike/07 fail-closed contract). The
// separate newTestHandlerWithDriveChecker helper below wires a real
// (pass-through) closure for the preflight-pass tests.
func newTestHandler() *Handler {
	return NewHandler(nil, zap.NewNop(), nil, nil)
}

// newTestHandlerWithSvc returns a handler with a non-nil svc. Used by
// the invariant-pin test where svc=nil would suppress the gate that
// fires AFTER the svc call (a regression would NOT panic on svc=nil).
func newTestHandlerWithSvc(svc *sourcing.Service) *Handler {
	return NewHandler(svc, zap.NewNop(), nil, nil)
}

// newTestHandlerWithChecker returns a handler with a real driveChecker
// closure (the gate passes when returned-error is nil). Used by the
// preflight-pass TDD tests so the test can drive the underlying svc
// surface (svc=nil in tests → 503 svc-not-wired, NOT drive-gate).
func newTestHandlerWithChecker(svc *sourcing.Service, driveChecker func() error) *Handler {
	return NewHandler(svc, zap.NewNop(), nil, driveChecker)
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

// ── BatchRegisterFromYouTube pre-flight TDD tests (PR-DRIVE-AVAILABILITY-GATE) ──
//
// These tests pin the handler-level defense-in-depth gate added on top
// of the composition-root validateDriveServiceAvailability. They prove:
//  1. folder_id at request-level default triggers the gate.
//  2. per-clip folder_id override triggers the gate (effectiveFolderID
//     scans the clip list to defeat the backfill-bypass attack surface).
//  3. no folder_id → no gate call (preserves the pre-existing media-only
//     batch traffic).
//  4. driveChecker returns nil → preflight passes; svc=nil surfaces the
//     canonical 503 svc-not-wired (proves the gate did NOT suppress the
//     downstream svc dispatch when wiring is correct).

// TestBatchRegister_RequestLevelFolderID_NilDriveChecker_Returns503:
// the canonical godlike/07 no-fake-availability case. Request-level
// folder_id is non-empty AND driveChecker is nil (handler constructed
// via NewHandler with 4th-arg nil → defensive always-fail closure).
// The handler must surface 503 with the actionable error envelope
// instead of 500 nil-panic. The body MUST contain the canonical
// "Drive service not configured" header AND the PR-DRIVE-...
// audit-pin so future log-scanners can grep it.
func TestBatchRegister_RequestLevelFolderID_NilDriveChecker_Returns503(t *testing.T) {
	h := newTestHandler()
	r := newTestRouter(h)

	body := `{
		"folder_id": "1Bxv-LdrldSkYu4jOfYQ3j4TnPvA3Rv0x",
		"clips": [{"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU"}]
	}`
	req := httptest.NewRequest("POST", "/api/media/register-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"PR-DRIVE-AVAILABILITY-GATE: folder_id non-empty + nil driveChecker MUST be 503, NEVER 500 nil-panic")
	require.Contains(t, w.Body.String(), "Drive service not configured",
		"error body must surface the gateway diagnostic message")
	require.Contains(t, w.Body.String(), "PR-DRIVE-AVAILABILITY-GATE",
		"error body must cite the wave-tracker anchor for audit-trail grep-ability")
}

// TestBatchRegister_PerClipFolderID_NilDriveChecker_Returns503:
// same fail-closed contract for the per-clip override path. An
// attacker cannot bypass the gate by spreading folder_ids across
// individual clips while leaving the request-level default empty —
// effectiveFolderID scans the clip list to defeat this attack
// surface (godlike/06 SSOT one-canonical-owner-per-fact).
func TestBatchRegister_PerClipFolderID_NilDriveChecker_Returns503(t *testing.T) {
	h := newTestHandler()
	r := newTestRouter(h)

	body := `{
		"clips": [
			{"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU", "folder_id": "1Bxv-LdrldSkYu4jOfYQ3j4TnPvA3Rv0x"}
		]
	}`
	req := httptest.NewRequest("POST", "/api/media/register-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"PR-DRIVE-AVAILABILITY-GATE: per-clip folder_id override MUST also fail-closed")
	require.Contains(t, w.Body.String(), "Drive service not configured")
}

// TestBatchRegister_NoFolderID_DoesNotInvokeDriveChecker: the inverse
// canonical case. With folder_id empty at every level, the preflight
// gate MUST NOT fire — the gate is invoked ONLY when folder_id is
// non-empty. We prove the gate is bypassed by wiring a driveChecker
// that panics if called; srv=nil surfaces 503 svc-not-wired REACHABLE
// because the preflight was bypassed.
func TestBatchRegister_NoFolderID_DoesNotInvokeDriveChecker(t *testing.T) {
	// panicIfCalled: driveChecker must NOT be reached when folder_id
	// is empty. The test relies on this surface to prove the
	// preflight gating is conditional on folder_id.
	panicIfCalled := func() error {
		panic("driveChecker called for folder_id-empty request — preflight is over-gating non-Drive traffic")
	}
	h := newTestHandlerWithChecker(nil /*svc=nil*/, panicIfCalled)
	r := newTestRouter(h)

	body := `{"clips": [{"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU"}]}`
	req := httptest.NewRequest("POST", "/api/media/register-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// NotPanics PROVES the preflight was bypassed (panicIfCalled
	// would panic if reached). The svc=nil check fires AFTER the
	// driveChecker path so we surface the canonical 503.
	require.NotPanics(t, func() {
		r.ServeHTTP(w, req)
	}, "driveChecker must NOT fire for folder_id-empty requests")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "register service not wired")
}

// TestBatchRegister_PreflightPasses_SvcNilReturns503: driveChecker
// returns nil (canonical wired-Drive success path) AND svc is nil.
// The handler MUST surface the canonical 503 svc-not-wired (NOT the
// gate's 503, NOT 500). This pin proves the upstream gate does
// NOT suppress the downstream svc dispatch when wiring is correct.
func TestBatchRegister_PreflightPasses_SvcNilReturns503(t *testing.T) {
	passThrough := func() error { return nil } // Drive wired
	h := newTestHandlerWithChecker(nil /*svc=nil*/, passThrough)
	r := newTestRouter(h)

	body := `{
		"folder_id": "1Bxv-LdrldSkYu4jOfYQ3j4TnPvA3Rv0x",
		"clips": [{"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU"}]
	}`
	req := httptest.NewRequest("POST", "/api/media/register-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"driveChecker returned nil: preflight passes, downstream svc=nil short-circuits to 503")
	require.Contains(t, w.Body.String(), "register service not wired",
		"svc=nil surface (NOT drive-gate surface) MUST reach the client when wiring is correct")
}

// ── PR-W6-LOCATION-FIELD (July 2026, Wave 6) — Group B regression tests ─────

// TestBatchRegister_AcceptsLocationField_WithoutFolderID: the canonical
// godlike/07 NO-FAKE-AVAILABILITY test pin. A request carrying only the
// new typed `location` field (no `folder_id`) MUST NOT trigger the
// PR-DRIVE-AVAILABILITY-GATE driveChecker probe — the gate fires ONLY
// on FolderID per godlike/07 fail-fast-at-input semantics; the typed
// Location field is accepted at the handler seam without committing to
// Drive routing until Wave 7 attaches the resolver port. svc=nil
// returns the canonical 503 (NOT a BindJSON 400).
func TestBatchRegister_AcceptsLocationField_WithoutFolderID(t *testing.T) {
	h := newTestHandlerWithChecker(nil /*svc=nil*/, panicIfCalledDriveChecker(t, "driveChecker must NOT fire for location-only requests"))
	r := newTestRouter(h)

	body := `{
		"location": {"category": "Boxe", "subject": "mike-tyson"},
		"clips": [{"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU"}]
	}`
	req := httptest.NewRequest("POST", "/api/media/register-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"location-only request: preflight skipped, downstream svc=nil short-circuits to 503 register service not wired")
	require.Contains(t, w.Body.String(), "register service not wired",
		"svc=nil surface MUST reach the client; the new Location field is accepted at BindJSON without 400")
}

// TestBatchRegister_BothFolderIDAndLocation_FolderIDGateFires: pinned
// backward-compat contract. When BOTH the legacy `folder_id` AND the
// new `location` field are non-empty, the Drive-availability gate MUST
// still fire on FolderID (per godlike/07 input-fail-closed semantics);
// the resolver's Location-wins precedence is a Wave 7 composition-root
// concern. The test pins today's pre-Wave-7 behavior so future Location
// precedence logic lands as a behavior change rather than silent drift.
func TestBatchRegister_BothFolderIDAndLocation_FolderIDGateFires(t *testing.T) {
	h := newTestHandler() // canonical fail-closed default checker
	r := newTestRouter(h)

	body := `{
		"folder_id": "1Bxv-LdrldSkYu4jOfYQ3j4TnPvA3Rv0x",
		"location": {"category": "Boxe", "subject": "mike-tyson"},
		"clips": [{"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU"}]
	}`
	req := httptest.NewRequest("POST", "/api/media/register-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"folder_id non-empty: preflight gate MUST fail-closed 503 (forward-pointer: Location precedence lands in Wave 7)")
	require.Contains(t, w.Body.String(), "Drive service not configured",
		"the canonical PR-DRIVE-AVAILABILITY-GATE message MUST surface when FolderID is non-empty + driveChecker fails")
	require.NotContains(t, w.Body.String(), "register service not wired",
		"preflight fires BEFORE svc=nil check; svc surface MUST NOT win here")
}

// TestRegisterFromYouTube_AcceptsLocationField: single-clip endpoint
// in Wave 6 also accepts the typed `location` field. The new field
// threads through toRegisterClipCommand -> sourcing.RegisterClipCommand
// without breaking BindJSON. svc=nil returns the canonical 503 (NOT
// 400 from BindJSON failure on the new field).
func TestRegisterFromYouTube_AcceptsLocationField(t *testing.T) {
	h := newTestHandler() // svc=nil default
	r := newTestRouter(h)

	body := `{
		"url": "https://www.youtube.com/watch?v=9u4T_o3FxOU",
		"location": {"category": "Boxe", "subject": "mike-tyson"}
	}`
	req := httptest.NewRequest("POST", "/api/media/register-from-youtube", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"location-only single-clip: tv preflight passes, downstream svc=nil short-circuits to 503")
	require.Contains(t, w.Body.String(), "register service not wired",
		"svc=nil surface MUST reach the client; the new Location field is accepted at BindJSON without 400")
}

// panicIfCalledDriveChecker is the typed-stub helper for the
// PR-DRIVE-AVAILABILITY-GATE forward-prevention audit-pin tests. It
// returns a closure that calls t.Fatal with the given message if
// invoked. Used in TestBatchRegister_AcceptsLocationField_WithoutFolderID
// (PR-W6-LOCATION-FIELD) to assert that the gate is NEVER reached
// for location-only requests — a defensive fail-fast at the test
// surface mirrors the production fail-closed contract.
func panicIfCalledDriveChecker(t *testing.T, msg string) func() error {
	return func() error {
		t.Helper()
		t.Fatal(msg)
		return nil
	}
}
