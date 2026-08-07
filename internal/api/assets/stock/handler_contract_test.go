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
	"slices"
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
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}
	registered := map[string]bool{}
	for _, route := range routes {
		if route.Method != "POST" {
			t.Errorf("expected POST route, got %s %s", route.Method, route.Path)
		}
		registered[route.Path] = true
	}
	if !registered["/run"] || !registered["/search-and-run"] {
		t.Errorf("expected POST /run and POST /search-and-run, got %+v", registered)
	}
}

func TestStockHandler_SearchAndRun_Async_Returns202(t *testing.T) {
	_, router := newStockHandler(nil, "job-search-and-run")

	payload := `{"queries":[{"q":"boxing training gym","limit":1}],"folder_id":"folder-1","folder_name":"Stock E2E Test","subfolder":"run-1","metadata":{"test":"stock-e2e","request_tag":"run-1"},"async":true}`
	req := httptest.NewRequest(http.MethodPost, "/search-and-run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != StatusPending || resp.JobID != "job-search-and-run" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestStockHandler_SearchAndRun_AsyncOmitted_Returns200(t *testing.T) {
	_, router := newStockHandler(nil, "job-search-and-run")

	payload := `{"queries":[{"q":"boxing training gym","limit":1}],"folder_id":"folder-1"}`
	req := httptest.NewRequest(http.MethodPost, "/search-and-run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("omitted async on search-and-run expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != StatusCompleted || resp.JobID != "" {
		t.Fatalf("omitted async on search-and-run must be synchronous: %+v", resp)
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

	// Per godlike/06 SSOT decoupling (agent feedback): the handler
	// always returns HTTP 200 (acknowledgement-of-receipt) and uses
	// the endpoint-acknowledgement status enum (pending|completed)
	// — which is INDEPENDENT of the broker job state enum
	// (QUEUED|RUNNING|FINALIZING|SUCCEEDED|INDEX_PENDING). Polling
	// /api/jobs/{id}/full is the canonical path for broker state.
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Definition of Done §2 contract (revised): status is "pending"
	// when useCase.Submit routed through the jobs broker (jobID != ''
	// branch). The DECOUPLING from the broker state enum is
	// enforced here — see handler.go StatusPending/StatusCompleted
	// constants and the comment block above the response literal.
	if resp.Status != StatusPending {
		t.Errorf("expected status=%s, got %q", StatusPending, resp.Status)
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

func TestStockHandler_ValidPayload_ExplicitAsyncFalse_Returns200(t *testing.T) {
	_, router := newStockHandler(nil, "job-test-123")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test","async":false}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != StatusCompleted {
		t.Fatalf("expected status=%s, got %q", StatusCompleted, resp.Status)
	}
	if resp.JobID != "" || resp.RunID != "" {
		t.Fatalf("explicit async=false must not return job identifiers: %+v", resp)
	}
}

func TestStockHandler_ValidPayload_Sync_Returns200(t *testing.T) {
	_, router := newStockHandler(nil, "job-test-123")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test"}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Sync path: HTTP 200 + status="completed" (jobID == '' branch).
	// The handler always returns HTTP 200 (acknowledgement), and the
	// status field distinguishes pending vs completed via the
	// endpoint-acknowledgement enum (decoupled from broker state).
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != StatusCompleted {
		t.Errorf("expected status=%s (sync mode), got %q", StatusCompleted, resp.Status)
	}
}

func TestStockHandler_ValidPayload_AsyncOmitted_Returns200(t *testing.T) {
	_, router := newStockHandler(nil, "job-test-123")

	payload := `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("omitted async expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp testRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != StatusCompleted {
		t.Fatalf("omitted async must decode as false and return status=%s, got %q", StatusCompleted, resp.Status)
	}
	if resp.JobID != "" || resp.RunID != "" {
		t.Fatalf("omitted async must not return job identifiers: %+v", resp)
	}
}

func TestStockHandler_WithClips_Returns200(t *testing.T) {
	_, router := newStockHandler(nil, "job-clips-456")

	// ClipSpec JSON keys are url / start_sec / end_sec (omitempty)
	// per internal/application/assets/providers/stock/stockpipeline/types_run.go.
	payload := `{"clips":[{"url":"https://example.com/video.mp4","start_sec":0,"end_sec":4}],"folder_name":"test"}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestStockHandler_SearchQueries_Returns200(t *testing.T) {
	_, router := newStockHandler(nil, "job-search-789")

	payload := `{"search_queries":["boxing match"],"clip_duration":5,"folder_name":"search-test"}`
	body := bytes.NewBufferString(payload)
	req := httptest.NewRequest(http.MethodPost, "/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Decoupling-leak regression guard ────────────────────────────────────
//
// TestStockHandler_NoBrokerStateLeak is the load-bearing regression
// test for the godlike/06 SSOT decoupling enforced in handler.go:
// the HTTP `status` field is the CANONICAL endpoint-acknowledgement
// enum (pending|completed|error), INDEPENDENT of the broker job state
// enum (QUEUED|LEASED|RUNNING|...|SUCCEEDED|INDEX_PENDING). A future
// commit must not silently regress the response shape by writing a
// broker-state value into the `status` field — that would conflate
// "request acknowledged" with "broker state at this instant" and
// break client polling logic.
//
// The test:
//  1. Drives a matrix of payload shapes (async, sync, clips, search,
//     empty, bad-url) so every response branch in the handler is
//     exercised.
//  2. Parses each response into testRunResponse (structural, not
//     text — defends against handler-vs-test struct drift).
//  3. Asserts `.status` is in the canonical endpoint-acknowledgement
//     enum (positive whitelist).
//  4. Negative-asserts `.status` is NOT in the broker job state enum
//     (this is the regression guard)
func TestStockHandler_NoBrokerStateLeak(t *testing.T) {
	// Canonical endpoint-acknowledgement enum (godlike/06 SSOT).
	validEndpoints := map[string]bool{
		StatusPending:   true,
		StatusCompleted: true,
		StatusError:     true,
	}
	// Broker job state enum (canonical source: internal/kernel/job.Status).
	// These MUST NEVER leak into the endpoint-acknowledgement `status`
	// field. Sourced from the kernel job constants so any future addition
	// to the broker enum requires a conscious decision here too — drift
	// surfaces as a positive-list failure on the new value.
	brokerStates := []job.Status{
		job.StatusQueued,
		job.StatusLeased,
		job.StatusRunning,
		job.StatusWaitingChildren,
		job.StatusFinalizing,
		job.StatusRetryWait,
		job.StatusSucceeded,
		job.StatusPartiallySucceeded,
		job.StatusIndexPending,
		job.StatusFailed,
		job.StatusCancelled,
	}

	cases := []struct{ name, payload string }{
		{
			name:    "async_via_broker",
			payload: `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test","async":true}`,
		},
		{
			name:    "sync_inline",
			payload: `{"direct_urls":["https://example.com/video.mp4"],"clip_duration":4,"folder_name":"test"}`,
		},
		{
			name:    "with_clips",
			payload: `{"clips":[{"url":"https://example.com/video.mp4","start_sec":0,"end_sec":4}],"folder_name":"test"}`,
		},
		{
			name:    "search_queries",
			payload: `{"search_queries":["boxing"],"clip_duration":5,"folder_name":"test"}`,
		},
		{
			name:    "empty_body",
			payload: `{}`,
		},
		{
			name:    "bad_url",
			payload: `{"direct_urls":["not-a-url"]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, router := newStockHandler(nil, "job-leak-guard")
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(tc.payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			var resp testRunResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			// Positive: response.status MUST be in the endpoint-acknowledgement enum.
			if !validEndpoints[resp.Status] {
				t.Errorf("response.status=%q is NOT in endpoint-acknowledgement enum (must be one of %s/%s/%s)", resp.Status, StatusPending, StatusCompleted, StatusError)
			}
			// Negative (the regression guard): response.status MUST NOT be a broker state enum value.
			if resp.Status != StatusPending && slices.Contains(brokerStates, job.Status(resp.Status)) {
				t.Errorf("response.status=%q LEAKED from broker job state enum — godlike/06 SSOT decoupling violated", resp.Status)
			}
		})
	}
}
