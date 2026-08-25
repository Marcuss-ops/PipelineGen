// Package images (api/images) — upload_handler_test.go locks the
// PR-IMG-LEGACY-2 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, EXPAND phase,
// deadline 2026-08-08) contract: when ingestSvc is unwired, POST
// /api/images/upload MUST fail-closed with HTTP 503 + an actionable
// error message naming the missing dependency.
//
// godlike/07 NO-FAKE-AVAILABILITY regression lock: the pre-PR
// implementation silently fell back to h.service.SearchAndDownload
// when ingestSvc was nil — a cross-domain silent semantic switch that
// returned a SearchAndDownload response shape to a POST /upload
// caller. This file locks the new contract that RETIRES the silent
// fallback per godlike/07 fail-closed-at-composition discipline.
package images

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestUpload_NoIngestSvc_Returns503 — locks the new fail-closed 503
// contract. The handler SIGNATURE stays unchanged; the new behaviour
// surfaces typed NACK when the composition-root fails to wire
// ingestSvc (composition-root caller discipline per AGENTS.md §DL-007).
//
// godlike/06 SSOT: this test is the canonical contract surface for
// the 503 path. A future refactor that re-introduces a fallback to
// h.service.SearchAndDownload would surface as a test failure — the
// test asserts BOTH status code AND body message strings so a quiet
// return 200 with broken shape doesn't pass.
func TestUpload_NoIngestSvc_Returns503(t *testing.T) {
	// ARRANGE: handler with ingestSvc explicitly nil (composition
	// failure path). service + jobsSvc fields are nil too — they're
	// never reached on the /upload 503 fast-path, but constructing
	// them nil-safe here also pins the nil-tolerant constructor
	// contract (NewImagesHandler accepts all-nil args).
	h := NewImagesHandler(nil, nil, nil)
	if h == nil {
		t.Fatal("NewImagesHandler returned nil")
	}

	// Setup router at the canonical /api/images prefix (mirrors the
	// composition-root wiring in internal/app/registry.go).
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group("/api/images")
	h.RegisterRoutes(group)

	// Prepare a structurally valid UploadRequest (binding:"required"
	// on URL means an empty URL would 400 at BindJSON before reaching
	// the 503 path; we want to exercise the ingestSvc==nil path
	// specifically, so URL is non-empty).
	body := `{"subject":"albert_einstein","image_url":"https://example.com/img.jpg"}`
	req := httptest.NewRequest(http.MethodPost, "/api/images/upload",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// ACT
	r.ServeHTTP(rec, req)

	// ASSERT
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	// 503.1 — actionable canonical message: ingest service required
	if !strings.Contains(rec.Body.String(), "image upload requires the ingest service") {
		t.Fatalf("body must contain 'image upload requires the ingest service' substring; got %q",
			rec.Body.String())
	}
	// 503.2 — actionable canonical message: NOT silently falling back
	if !strings.Contains(rec.Body.String(), "cannot fallback to search") {
		t.Fatalf("body must contain 'cannot fallback to search' substring; got %q",
			rec.Body.String())
	}
	// 503.3 — silent-success regression lock: body MUST NOT contain
	// SearchAndDownload signal. A pre-PR silent-fallback would have
	// dispatched to h.service.SearchAndDownload and returned 200 OK
	// with SearchAndDownload-shape payload. This assertion guarantees
	// that contract never silently resurrects.
	if strings.Contains(strings.ToLower(rec.Body.String()),
		strings.ToLower("SearchAndDownload")) {
		t.Fatalf("body must NOT contain SearchAndDownload caller path (silent-fallback smoke); got %q",
			rec.Body.String())
	}
}

// TestUpload_EmptyURL_Returns400 — locks the binding:"required" fence
// on URL. Important regression lock because the new PR-IMG-LEGACY-2
// 503 path was placed AFTER ShouldBindJSON — empty URL must still
// return 400 (binding catches it), not 503. This guarantees the test
// suite doesn't accidentally regress the BindJSON contract.
func TestUpload_EmptyURL_Returns400(t *testing.T) {
	h := NewImagesHandler(nil, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group("/api/images")
	h.RegisterRoutes(group)

	// URL field is empty — binding:"required" must reject before
	// the ingestSvc==nil check fires.
	body := `{"subject":"abc","image_url":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/images/upload",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty URL: want 400, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

// TestUpload_InvalidJSON_Returns400 — locks the JSON binding fence.
// Per godlike/07 fail-fast-at-input: malformed JSON must NOT silently
// proceed to the ingestSvc path. binding error before composition
// wiring check.
func TestUpload_InvalidJSON_Returns400(t *testing.T) {
	h := NewImagesHandler(nil, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group("/api/images")
	h.RegisterRoutes(group)

	body := `{"subject":"abc"` // truncated JSON
	req := httptest.NewRequest(http.MethodPost, "/api/images/upload",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: want 400, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}
