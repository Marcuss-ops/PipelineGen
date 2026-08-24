// Package stock — handler_validation_test.go
//
// Negative-test coverage for POST /api/stock-pipeline/run (Definition
// of Done §2 / §16). The contract tests live in handler_contract_test.go;
// this file is the dedicated surface for input-rejection paths that
// must NOT return 200/202 — payload rejection, scheme rejection,
// path-traversal rejection, and capacity-cap rejection.
//
// All tests share the same testRunResponse struct + newStockHandler
// helper from handler_contract_test.go (same package, same test binary).
package stock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── 1. UNKNOWN_FIELD rejection ─────────────────────────────────────────
//
// Definition of Done §2: campo fake → 400 con error_code=UNKNOWN_FIELD.
// The handler uses json.NewDecoder with DisallowUnknownFields so the
// stdlib encoder produces a "json: unknown field" error message that
// the handler maps to the canonical machine-readable code.
func TestStockHandler_UnknownField_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"fake_param_not_in_struct":true}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "error" {
		t.Errorf("expected status=error, got %q", resp.Status)
	}
	if resp.ErrorCode != ErrCodeUnknownField {
		t.Errorf("expected error_code=%s, got %q", ErrCodeUnknownField, resp.ErrorCode)
	}
}

// ── 2. file:// scheme accepted for local hermetic runs ───────────────
//
// PR-FILE-SCHEME-ACCEPT (July 2026): file:// URLs are accepted for
// local hermetic stock pipeline runs (Mike Tyson pilot). The path
// portion is validated for null bytes, backslash escapes, and path
// traversal — but absolute paths to media files (e.g. /data/media/)
// are explicitly allowed. SSRF is not a concern for local-only file
// paths handled by the orchestrator's source-stager chain.
func TestStockHandler_FileSchemeURL_Accepted(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	// Valid file:// URL pointing to a media file (portable path).
	payload := `{"direct_urls":["file:///data/media/test-video.mp4"]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// file:// URLs are now accepted; the handler returns 200 (sync)
	// or 202 (async) depending on the use-case path.
	if w.Code != http.StatusOK && w.Code != http.StatusAccepted {
		t.Fatalf("expected 200 or 202, got %d: %s", w.Code, w.Body.String())
	}
}

// ── 2b. file:// path traversal rejection ─────────────────────────────
//
// PR-FILE-SCHEME-ACCEPT (July 2026): file:// URLs with path traversal
// patterns (../) are STILL rejected — same as folder_name / subfolder
// fields. This is the defense boundary: file:// is accepted for local
// stock runs but the path must stay within the intended tree.
func TestStockHandler_FileSchemeURL_PathTraversal_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"direct_urls":["file:///home/user/../../../etc/passwd"]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ErrorCode != ErrCodeInvalidURL {
		t.Errorf("expected error_code=%s, got %q", ErrCodeInvalidURL, resp.ErrorCode)
	}
}

// ── 3. RFC1918 private IP rejection ───────────────────────────────────
//
// Definition of Done §16: URL verso IP privati (192.168.x.x, 10.x.x.x,
// 172.16-31.x.x) → 400. SSRF mitigation at the HTTP boundary.
func TestStockHandler_PrivateIPURL_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"drive_urls":["https://192.168.1.1/video.mp4"]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ErrorCode != ErrCodeInvalidURL {
		t.Errorf("expected error_code=%s, got %q", ErrCodeInvalidURL, resp.ErrorCode)
	}
}

// ── 4. Malformed URL rejection ────────────────────────────────────────
//
// Definition of Done §2: URL non valida (es. "not-a-url") → 400.
// url.ParseRequestURI requires a scheme; "not-a-url" has none → fail.
func TestStockHandler_NotAURL_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"direct_urls":["not-a-url"]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ErrorCode != ErrCodeInvalidURL {
		t.Errorf("expected error_code=%s, got %q", ErrCodeInvalidURL, resp.ErrorCode)
	}
}

// ── 5. Path traversal (forward-slash ../) ─────────────────────────────
//
// Definition of Done §16: path traversal "../../folder" → 400. The
// handler canonicalizes with path.Clean and rejects any cleaned
// component that still starts with "../" or contains "/../".
func TestStockHandler_PathTraversal_FolderName_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"folder_name":"../../secrets"}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ErrorCode != ErrCodePathTraversal {
		t.Errorf("expected error_code=%s, got %q", ErrCodePathTraversal, resp.ErrorCode)
	}
}

// ── 6. Path traversal (Windows backslash escape) ─────────────────────
//
// Definition of Done §16: backslash-prefixed paths target Windows-
// style traversal. The handler rejects any folder field containing
// "\\" regardless of how it's otherwise encoded.
func TestStockHandler_PathTraversal_Backslash_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"subfolder":"..\\..\\windows"}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ErrorCode != ErrCodePathTraversal {
		t.Errorf("expected error_code=%s, got %q", ErrCodePathTraversal, resp.ErrorCode)
	}
}

// ── 7. Max clips per request cap ──────────────────────────────────────
//
// Definition of Done §2 / §16: limite massimo di clip per richiesta → 400.
// The handler caps single-run submissions at MaxClipsPerRun=100 to
// prevent resource exhaustion at the HTTP boundary. Larger jobs MUST
// be split client-side into multiple runs.
func TestStockHandler_MaxClipsExceeded_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	// Build 101 minimal clip specs (over by one).
	clips := make([]map[string]any, MaxClipsPerRun+1)
	for i := range clips {
		clips[i] = map[string]any{
			"url":       "https://example.com/video.mp4",
			"start_sec": 0,
			"end_sec":   4,
		}
	}
	payload := map[string]any{"clips": clips, "folder_name": "test"}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ErrorCode != ErrCodeMaxClips {
		t.Errorf("expected error_code=%s, got %q", ErrCodeMaxClips, resp.ErrorCode)
	}
	// Sanity: also assert that 1-error message references the cap.
	if !strings.Contains(resp.Error, fmt.Sprintf("%d", MaxClipsPerRun)) {
		t.Errorf("expected error message to mention cap %d, got %q", MaxClipsPerRun, resp.Error)
	}
}

// ── 8. Boundary: max-clips at exactly the cap is allowed ──────────────
//
// Companion to #7: 100 clips (== MaxClipsPerRun) must NOT be rejected.
// This catches off-by-one errors at the cap boundary.
//
// Per agent feedback (godlike/06 SSOT decoupling): handler now
// returns HTTP 200 (acknowledgement-of-receipt) with the endpoint-
// acknowledgement status enum (pending|completed), decoupled from
// the broker job state enum. Boundary test confirms the cap does
// not flip the response class.
func TestStockHandler_MaxClipsExact_Returns200(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	clips := make([]map[string]any, MaxClipsPerRun)
	for i := range clips {
		clips[i] = map[string]any{
			"url":       "https://example.com/video.mp4",
			"start_sec": 0,
			"end_sec":   4,
		}
	}
	payload := map[string]any{"clips": clips, "folder_name": "test"}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ── 9. Happy path: deduplicated field always present ──────────────────
//
// Definition of Done §2: response MUST always include
// `deduplicated:false` on first submission. This is the field the
// idempotency followup flips to true on a hash collision.
//
// Per agent feedback (godlike/06 SSOT decoupling): endpoint-
// acknowledgement status is "pending" on async path — INDEPENDENT
// of the broker job state enum (which is QUEUED at this moment).
func TestStockHandler_DeduplicatedFieldAlwaysPresent(t *testing.T) {
	_, router := newStockHandler(nil, "job-test-123")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test","async":true}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	// The response body MUST contain the literal key "deduplicated"
	// (no omitempty on the json tag). Verify by raw byte scan rather
	// than go through the struct (which could mask a structural drift
	// between handler and the test mirror).
	if !strings.Contains(w.Body.String(), `"deduplicated":false`) {
		t.Errorf("expected response to contain `\"deduplicated\":false`, got %s", w.Body.String())
	}
	// And parse to assert the "pending" endpoint-acknowledgement status
	// (decoupled from broker state).
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != StatusPending {
		t.Errorf("expected status=%s on async path, got %q", StatusPending, resp.Status)
	}
	if resp.Deduplicated != false {
		t.Errorf("expected deduplicated=false on first submission, got %v", resp.Deduplicated)
	}
}
