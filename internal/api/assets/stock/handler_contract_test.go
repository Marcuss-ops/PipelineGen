// Package stock — handler_contract_test.go
//
// Tests the HTTP contract of POST /api/stock-pipeline/run. No real
// database, Drive, or yt-dlp — all dependencies are stubbed.
package stock

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Response shape shared between contract + validation tests ──────
//
// Tests use a local mirror of runResponse so json.Unmarshal into this
// struct can round-trip the new fields (`deduplicated` bool,
// `error_code` string). The struct is kept in lock-step with the
// handler's runResponse by lints if either side drifts.
type testRunResponse struct {
	Status       string `json:"status"`
	JobID        string `json:"job_id"`
	RunID        string `json:"run_id"`
	Deduplicated bool   `json:"deduplicated"`
	Error        string `json:"error"`
	ErrorCode    string `json:"error_code"`
}

// ── Fake service runner + jobs enqueuer ──────────────────────────────

type fakeServiceRunner struct {
	runErr error
}

func (f *fakeServiceRunner) Run(_ context.Context, _ *stockpipeline.RunInput) (*stockpipeline.PipelineResult, error) {
	return &stockpipeline.PipelineResult{}, f.runErr
}

type fakeJobsEnqueuer struct {
	jobID string
	err   error
}

func (f *fakeJobsEnqueuer) Enqueue(_ context.Context, _ *job.EnqueueRequest) (*job.Job, error) {
	return &job.Job{ID: f.jobID}, f.err
}

// ── Helper: build test router ────────────────────────────────────────

func newStockHandler(runErr error, jobID string) (*StockHandler, *gin.Engine) {
	gin.SetMode(gin.TestMode)

	runner := &fakeServiceRunner{runErr: runErr}
	enqueuer := &fakeJobsEnqueuer{jobID: jobID}
	uc := stockpipeline.NewStockUseCase(runner, enqueuer, zap.NewNop())
	handler := NewStockHandler(uc, zap.NewNop())

	router := gin.New()
	rg := router.Group("/")
	handler.RegisterRoutes(rg)
	return handler, router
}

// ── Contract tests ──────────────────────────────────────────────────

func TestStockHandler_RoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, router := newStockHandler(nil, "job-1")

	routes := router.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d: %+v", len(routes), routes)
	}
	if routes[0].Method != "POST" || routes[0].Path != "/run" {
		t.Errorf("expected POST /run, got %s %s", routes[0].Method, routes[0].Path)
	}
}

func TestStockHandler_EmptyBody_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	body := bytes.NewBufferString("")
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "error" {
		t.Errorf("expected status=error, got %q", resp.Status)
	}
}

func TestStockHandler_NoSources_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"clip_duration":4}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "error" {
		t.Errorf("expected status=error, got %q", resp.Status)
	}
	if resp.ErrorCode != ErrCodeInvalidPayload {
		t.Errorf("expected error_code=%s, got %q", ErrCodeInvalidPayload, resp.ErrorCode)
	}
}

func TestStockHandler_ClipDurationTooLow_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":1}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "error" {
		t.Errorf("expected status=error, got %q", resp.Status)
	}
	if resp.ErrorCode != ErrCodeInvalidPayload {
		t.Errorf("expected error_code=%s, got %q", ErrCodeInvalidPayload, resp.ErrorCode)
	}
}

func TestStockHandler_ClipDurationTooHigh_Returns400(t *testing.T) {
	_, router := newStockHandler(nil, "job-1")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":60}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "error" {
		t.Errorf("expected status=error, got %q", resp.Status)
	}
	if resp.ErrorCode != ErrCodeInvalidPayload {
		t.Errorf("expected error_code=%s, got %q", ErrCodeInvalidPayload, resp.ErrorCode)
	}
}

func TestStockHandler_ValidPayload_Async_Returns202(t *testing.T) {
	_, router := newStockHandler(nil, "job-test-123")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test","async":true}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Definition of Done §2: status is "QUEUED" for async-broker-routed
	// runs (canonical production path).
	if resp.Status != "QUEUED" {
		t.Errorf("expected status=QUEUED, got %q", resp.Status)
	}
	if resp.JobID != "job-test-123" {
		t.Errorf("expected job_id=job-test-123, got %q", resp.JobID)
	}
	// Definition of Done §2: deduplicated is always present in the
	// response (false here — the idempotency followup flips it true).
	if resp.Deduplicated != false {
		t.Errorf("expected deduplicated=false on first submission, got %v", resp.Deduplicated)
	}
}

func TestStockHandler_ValidPayload_Sync_Returns202(t *testing.T) {
	_, router := newStockHandler(nil, "job-test-123")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test"}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Even sync (no jobs wired) returns 202 — the handler always emits
	// 202; status="completed" differentiates from "QUEUED".
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("expected status=completed (sync mode), got %q", resp.Status)
	}
}

func TestStockHandler_WithClips_Returns202(t *testing.T) {
	_, router := newStockHandler(nil, "job-clips-456")

	// ClipSpec JSON keys are url / start_sec / end_sec (omitempty)
	// per internal/application/assets/providers/stock/stockpipeline/types_run.go.
	payload := `{"clips":[{"url":"https://example.com/video.mp4","start_sec":0,"end_sec":4}],"folder_name":"test"}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

func TestStockHandler_SearchQueries_Returns202(t *testing.T) {
	_, router := newStockHandler(nil, "job-search-789")

	payload := `{"search_queries":["boxing match"],"clip_duration":5,"folder_name":"search-test"}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}
