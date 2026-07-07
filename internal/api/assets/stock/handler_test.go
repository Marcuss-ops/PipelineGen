package stock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Test stubs ────────────────────────────────────────────────────────

// fakeJobsEnqueuer implements jobsEnqueuer (defined in stockpipeline
// package) with a controllable job ID and error. Used by the happy
// path tests so the handler can call Submit without panicking on nil.
type fakeJobsEnqueuer struct {
	jobID string
	err   error
}

func (f *fakeJobsEnqueuer) Enqueue(_ context.Context, _ *jobservice.EnqueueRequest) (*jobservice.Job, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &jobservice.Job{ID: f.jobID}, nil
}

type fakeStockServiceRunner struct {
	lastInput *stockpipeline.RunInput
	err       error
}

func (f *fakeStockServiceRunner) Run(_ context.Context, input *stockpipeline.RunInput) (*stockpipeline.PipelineResult, error) {
	f.lastInput = input
	if f.err != nil {
		return nil, f.err
	}
	return &stockpipeline.PipelineResult{}, nil
}

// newTestHandler builds a Handler with a stub use case wired so the
// happy path (validation passes → Submit called) doesn't nil-deref.
// For the negative tests (validation → 400), the use case is never
// invoked.
//
// NewStockUseCase's third-party constructor invocation IS the
// compile-time contract check — *fakeJobsEnqueuer satisfies
// stockpipeline.jobsEnqueuer structurally (the interface is
// unexported, so a `var _` assertion can't live in this external
// test package). Future maintainers: signature drift on
// EnqueueRequest/Job surfaces here as build failure.
func newTestHandler(jobID string) *Handler {
	usecase := stockpipeline.NewStockUseCase(nil, &fakeJobsEnqueuer{jobID: jobID}, nil)
	return NewHandler(usecase, nil)
}

// runPOST sends a POST request to the given gin handler and returns
// the response recorder + decoded body.
func runPOST(t *testing.T, h gin.HandlerFunc, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/test", h)

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

// ── Negative path: clips without URL → 400 ───────────────────────────

func TestSearchAndRun_ClipsWithoutURL_Returns400(t *testing.T) {
	handler := newTestHandler("test-job")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries":       []map[string]any{{"q": "boxing-training-gym", "limit": 1}},
		"clips":         []map[string]any{{"start_sec": 5, "end_sec": 30}}, // no URL
		"total_minutes": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	errMsg, _ := body["error"].(string)
	if errMsg != "clips require at least one clip with a non-empty url" {
		t.Errorf("expected exact validation error, got %q", errMsg)
	}
}

func TestSearchAndRun_ClipsEmptyURLString_Returns400(t *testing.T) {
	handler := newTestHandler("test-job")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries":       []map[string]any{{"q": "boxing"}},
		"clips":         []map[string]any{{"url": "", "start_sec": 5, "end_sec": 30}},
		"total_minutes": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	errMsg, _ := body["error"].(string)
	if errMsg != "clips require at least one clip with a non-empty url" {
		t.Errorf("expected clip URL validation error, got %q", errMsg)
	}
}

// ── Negative path: empty payload → 400 ───────────────────────────────

func TestSearchAndRun_EmptyPayload_Returns400(t *testing.T) {
	handler := newTestHandler("test-job")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	errMsg, _ := body["error"].(string)
	if errMsg != "queries, direct_urls, drive_urls, or clips required" {
		t.Errorf("expected combined empty-input guard error, got %q", errMsg)
	}
}

func TestSearchAndRun_EmptyArrays_Returns400(t *testing.T) {
	handler := newTestHandler("test-job")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries":       []map[string]any{},
		"direct_urls":   []string{},
		"drive_urls":    []string{},
		"clips":         []map[string]any{},
		"total_minutes": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	errMsg, _ := body["error"].(string)
	if errMsg != "queries, direct_urls, drive_urls, or clips required" {
		t.Errorf("expected combined guard error, got %q", errMsg)
	}
}

// ── Negative path: clip_duration out of range ───────────────────────

func TestSearchAndRun_NegativeClipDuration_Returns400(t *testing.T) {
	handler := newTestHandler("test-job")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries":       []map[string]any{{"q": "boxing"}},
		"clip_duration": -5,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	errMsg, _ := body["error"].(string)
	if errMsg != "clip_duration must be >= 0" {
		t.Errorf("expected clip_duration validation error, got %q", errMsg)
	}
}

func TestSearchAndRun_OutOfRangeClipDuration_Returns400(t *testing.T) {
	handler := newTestHandler("test-job")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries":       []map[string]any{{"q": "boxing"}},
		"clip_duration": 100,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for clip_duration>30, got %d", rec.Code)
	}
	errMsg, _ := body["error"].(string)
	if errMsg != "clip_duration must be between 3 and 30 seconds" {
		t.Errorf("expected range error, got %q", errMsg)
	}
}

// ── Happy paths ──────────────────────────────────────────────────────

func TestSearchAndRun_AcceptsClipsOnly_Returns200(t *testing.T) {
	// Clip-only payload (the user's request: no queries, no URLs,
	// only clips with URLs). Verifies the old standalone
	// queries-required gate is gone.
	handler := newTestHandler("job_test_clips_12345")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"clips": []map[string]any{
			{"url": "https://www.youtube.com/watch?v=test123"},
		},
		"total_minutes":  1,
		"clip_duration":  10,
		"chunk_duration": 10,
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %v)", rec.Code, body)
	}
	gotID, _ := body["job_id"].(string)
	if gotID != "job_test_clips_12345" {
		t.Errorf("expected job_id from stub enqueuer, got %q", gotID)
	}
	msg, _ := body["message"].(string)
	if msg == "" {
		t.Errorf("expected non-empty message field")
	}
}

func TestSearchAndRun_AcceptsQueriesOnly_Returns200(t *testing.T) {
	// Regression guard: the legacy queries-only path still works.
	handler := newTestHandler("job_test_queries_12345")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries":        []map[string]any{{"q": "boxing-training-gym", "limit": 1}},
		"total_minutes":  1,
		"clip_duration":  10,
		"chunk_duration": 10,
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %v)", rec.Code, body)
	}
	gotID, _ := body["job_id"].(string)
	if gotID != "job_test_queries_12345" {
		t.Errorf("expected job_id from stub enqueuer, got %q", gotID)
	}
}

func TestRunStockPipeline_SyncMode_EnablesPersist(t *testing.T) {
	runner := &fakeStockServiceRunner{}
	usecase := stockpipeline.NewStockUseCase(runner, nil, nil)
	handler := NewHandler(usecase, nil)

	rec, _ := runPOST(t, handler.RunStockPipeline, map[string]any{
		"direct_urls":    []string{"https://example.com/video.mp4"},
		"total_minutes":  1,
		"clip_duration":  10,
		"chunk_duration": 10,
		"async":          false,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if runner.lastInput == nil {
		t.Fatal("expected sync runner to be invoked")
	}
	if !runner.lastInput.Persist {
		t.Fatal("expected sync stock request to enable Persist so the resilient path can upload, finalize, and index")
	}
}

func TestSearchAndRun_AcceptsDirectURLsOnly_Returns200(t *testing.T) {
	// DirectURLs-only path (no queries, no clips).
	handler := newTestHandler("job_test_direct_12345")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"direct_urls":    []string{"https://example.com/video.mp4"},
		"total_minutes":  1,
		"clip_duration":  10,
		"chunk_duration": 10,
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %v)", rec.Code, body)
	}
	gotID, _ := body["job_id"].(string)
	if gotID != "job_test_direct_12345" {
		t.Errorf("expected job_id, got %q", gotID)
	}
}

func TestSearchAndRun_AcceptsDriveURLsOnly_Returns200(t *testing.T) {
	// DriveURLs-only path.
	handler := newTestHandler("job_test_drive_12345")
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"drive_urls":     []string{"https://drive.google.com/file/d/abc123/view"},
		"total_minutes":  1,
		"clip_duration":  10,
		"chunk_duration": 10,
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %v)", rec.Code, body)
	}
	gotID, _ := body["job_id"].(string)
	if gotID != "job_test_drive_12345" {
		t.Errorf("expected job_id, got %q", gotID)
	}
}

// ── Defaulting behaviour ────────────────────────────────────────────

func TestSearchAndRun_DefaultsTotalMinutesToFive(t *testing.T) {
	// When total_minutes is 0/missing, the handler defaults it to 5.
	// We can't directly observe the value in the response (it goes to
	// the job payload), so we assert the request is accepted with
	// status 200 — total_minutes defaulting doesn't block validation.
	handler := newTestHandler("job_test_totalmin")
	rec, _ := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries": []map[string]any{{"q": "boxing"}},
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on defaulting total_minutes, got %d", rec.Code)
	}
}

func TestSearchAndRun_DefaultsClipDurationZeroTo10(t *testing.T) {
	// When clip_duration is 0/missing, the handler defaults to 10s.
	// Acceptance (status 200) is the indirect proof that the default
	// ran without triggering the range check.
	handler := newTestHandler("job_test_clipdur")
	rec, _ := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries":       []map[string]any{{"q": "boxing"}},
		"clip_duration": 0,
	})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when clip_duration=0 (default 10s), got %d", rec.Code)
	}
}

// ── Submit error propagation ────────────────────────────────────────

func TestSearchAndRun_SubmitError_ReturnsInternalError(t *testing.T) {
	// When the jobs enqueuer fails, the handler should propagate as
	// 500 (no special branching for arbitrary enqueue errors).
	// Note: NewHandler's nil-log default to zap.NewNop() is what
	// keeps the production code path safe on log call sites;
	// direct struct-literal construction must replicate that.
	handler := &Handler{
		useCase: stockpipeline.NewStockUseCase(nil, &fakeJobsEnqueuer{
			err: errors.New("mocked enqueue failure"),
		}, nil),
		log: zap.NewNop(),
	}
	rec, _ := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries": []map[string]any{{"q": "boxing"}},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on enqueue failure, got %d", rec.Code)
	}
}

func TestSearchAndRun_JobsServiceMissing_Returns503(t *testing.T) {
	// S2b contract mapping: when jobsSvc is nil and async=true, the
	// use case returns ErrJobsServiceRequired → handler maps to 503.
	// This test verifies the S2b API-boundary mapping, NOT the
	// underlying nil-check (that's stockpipeline's contract).
	handler := &Handler{
		useCase: stockpipeline.NewStockUseCase(nil, nil, nil),
		log:     zap.NewNop(),
	}
	rec, body := runPOST(t, handler.SearchAndRun, map[string]any{
		"queries": []map[string]any{{"q": "boxing"}},
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 on missing jobs service, got %d", rec.Code)
	}
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "stock async submit requires jobs service") {
		t.Errorf("expected jobs-service-required error message, got %q", errMsg)
	}
}
