package stock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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
	return NewHandler(usecase, nil, nil, nil)
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
	handler := NewHandler(usecase, nil, nil, nil)

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

func TestRunStockPipeline_PreservesFolderPayload(t *testing.T) {
	runner := &fakeStockServiceRunner{}
	usecase := stockpipeline.NewStockUseCase(runner, nil, nil)
	handler := NewHandler(usecase, nil, nil, nil)

	rec, _ := runPOST(t, handler.RunStockPipeline, map[string]any{
		"direct_urls":    []string{"https://example.com/video.mp4"},
		"total_minutes":  1,
		"clip_duration":  10,
		"chunk_duration": 10,
		"async":          false,
		"folder_name":    "Pacquiao Vs Broner",
		"subfolder":      "Press Conference",
		"folder_id":      "drive-root-123",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if runner.lastInput == nil {
		t.Fatal("expected sync runner to be invoked")
	}
	if runner.lastInput.FolderName != "Pacquiao Vs Broner" {
		t.Fatalf("FolderName = %q, want %q", runner.lastInput.FolderName, "Pacquiao Vs Broner")
	}
	if runner.lastInput.Subfolder != "Press Conference" {
		t.Fatalf("Subfolder = %q, want %q", runner.lastInput.Subfolder, "Press Conference")
	}
	if runner.lastInput.FolderID != "drive-root-123" {
		t.Fatalf("FolderID = %q, want %q", runner.lastInput.FolderID, "drive-root-123")
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

// ── DownloadStockClip test stubs ─────────────────────────────────────

// fakeStockAssetLookup implements StockAssetLookup with deterministic
// per-test asset + error injection. Future maintainers: signature drift
// on StockAssetLookup surfaces here as build failure.
type fakeStockAssetLookup struct {
	asset  *asset.Asset
	err    error
	calls  int
	lastID string
}

var _ StockAssetLookup = (*fakeStockAssetLookup)(nil)

func (f *fakeStockAssetLookup) Get(_ context.Context, id string) (*asset.Asset, error) {
	f.calls++
	f.lastID = id
	if f.err != nil {
		return nil, f.err
	}
	return f.asset, nil
}

// fakeStockDriveReader implements StockDriveReader with deterministic
// per-test meta/body/error injection. Future maintainers: signature
// drift on StockDriveReader (the 2 port methods) surfaces here as
// build failure on the `var _` pin below.
type fakeStockDriveReader struct {
	metaResp   *DriveFileMeta
	metaErr    error
	dlResp     io.ReadCloser
	dlCT       string
	dlErr      error
	calls      int
	lastFileID string
}

var _ StockDriveReader = (*fakeStockDriveReader)(nil)

func (f *fakeStockDriveReader) GetFileMeta(_ context.Context, fileID string) (*DriveFileMeta, error) {
	f.calls++
	f.lastFileID = fileID
	if f.metaErr != nil {
		return nil, f.metaErr
	}
	return f.metaResp, nil
}

func (f *fakeStockDriveReader) DownloadFile(_ context.Context, fileID string) (io.ReadCloser, string, error) {
	f.calls++
	f.lastFileID = fileID
	if f.dlErr != nil {
		return nil, "", f.dlErr
	}
	return f.dlResp, f.dlCT, nil
}

// newDownloadHandler builds a Handler wired with the given lookup + reader
// (or all-nil for the nil-deps test). useCase is nil because
// DownloadStockClip doesn't dispatch through the broker.
func newDownloadHandler(lookup StockAssetLookup, reader StockDriveReader) *Handler {
	return &Handler{
		log:       zap.NewNop(),
		assetRepo: lookup,
		driveRead: reader,
	}
}

// runDownloadPOST sends a POST to /clips/<id>/download and returns the
// recorder. The handler reads clipID from the route param (NOT the body),
// so the request has no JSON payload.
func runDownloadPOST(t *testing.T, h *Handler, clipID string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/clips/:id/download", h.DownloadStockClip)
	req := httptest.NewRequest(http.MethodPost, "/clips/"+clipID+"/download", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// newStockAssetFixture builds a minimally-populated asset.Asset with
// optional driveFileID and localPath (both empty by default — most tests
// use SetDriveFileID directly to construct the specific failure mode).
func newStockAssetFixture(driveFileID, localPath string) *asset.Asset {
	a := &asset.Asset{
		ID:        "stock-asset-test",
		Source:    "stock",
		Name:      "test-stock-clip",
		Filename:  "test.mp4",
		MediaType: asset.MediaTypeStock,
	}
	if driveFileID != "" {
		a.SetDriveFileID(driveFileID)
	}
	if localPath != "" {
		a.SetLocalPath(localPath)
	}
	return a
}

// ── TestDownloadStockClip_NilDeps_Returns503 ─────────────────────────

func TestDownloadStockClip_NilDeps_Returns503(t *testing.T) {
	// godlike/07 fail-closed: when assetRepo or driveRead is nil
	// (composition-time wiring gap), the handler must NOT panic and
	// must NOT silently serve from cache. It must return 503 so the
	// operator sees the wiring miss.
	handler := newDownloadHandler(nil, nil)
	rec := runDownloadPOST(t, handler, "clip-xyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "stock download not available") {
		t.Errorf("expected 503 message to mention wiring failure, got %q", errMsg)
	}
}

// ── TestDownloadStockClip_AssetNotFound_Returns404 ────────────────────

func TestDownloadStockClip_AssetNotFound_Returns404(t *testing.T) {
	// Lookup returns (nil, nil) — Canonical "asset does not exist"
	// sentinel. Handler must return 404 with the clip id echo in the
	// error message.
	lookup := &fakeStockAssetLookup{asset: nil, err: nil}
	handler := newDownloadHandler(lookup, &fakeStockDriveReader{})
	rec := runDownloadPOST(t, handler, "missing-clip-123")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "stock asset not found: missing-clip-123") {
		t.Errorf("expected 404 message to echo clip id, got %q", errMsg)
	}
	if lookup.lastID != "missing-clip-123" {
		t.Errorf("expected lookup to receive clip id %q, got %q", "missing-clip-123", lookup.lastID)
	}
}

// ── TestDownloadStockClip_DriveFileIDAndLocalPathEmpty_Returns404 ─────

func TestDownloadStockClip_DriveFileIDAndLocalPathEmpty_Returns404(t *testing.T) {
	// Asset exists (non-nil) but has no drive_file_id AND no local_path.
	// Handler must return 404 with diagnostic of the missing location.
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("", "")}
	handler := newDownloadHandler(lookup, &fakeStockDriveReader{})
	rec := runDownloadPOST(t, handler, "lonely-clip")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "no drive_file_id and no local path") {
		t.Errorf("expected 404 message naming the missing location, got %q", errMsg)
	}
}

// ── TestDownloadStockClip_NonMediaMime_Returns400 ─────────────────────

func TestDownloadStockClip_NonMediaMime_Returns400(t *testing.T) {
	// Drive.GetFileMeta returns a non-media MIME (e.g. application/pdf).
	// Handler must return 400, NOT silently proxy the file as "video/mp4"
	// (the prior behavior that mislabeled PDFs as videos).
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("drive-fake-pdf", "")}
	reader := &fakeStockDriveReader{
		metaResp: &DriveFileMeta{MimeType: "application/pdf"},
	}
	handler := newDownloadHandler(lookup, reader)
	rec := runDownloadPOST(t, handler, "clip-pdf")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-media mime, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "drive file is not media") {
		t.Errorf("expected 400 message to surface mime rejection, got %q", errMsg)
	}
	if !strings.Contains(errMsg, "application/pdf") {
		t.Errorf("expected 400 message to echo the rejected mime, got %q", errMsg)
	}
}

// ── TestDownloadStockClip_AudioMime_PreservesAudioContentType ─────────

func TestDownloadStockClip_AudioMime_PreservesAudioContentType(t *testing.T) {
	// CRITICAL: post-fix, an audio stock clip served via
	// Drive.DownloadFile should return Content-Type: audio/mpeg, NOT
	// the pre-fix default of "video/mp4".
	//
	// This test forces the new fallback chain by setting `dlCT: ""`
	// (the Drive DownloadFile response contentType is empty — the
	// canonical "I don't know my own MIME" opaque response). The
	// handler must then fall back to meta.MimeType = "audio/mpeg"
	// (Step 1 of the 2-step chain). A future regression that reverts
	// the fix to "video/mp4" hard-coded fallback would surface here
	// as a test failure (the assertion `gotCT != "video/mp4"`).
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("drive-audio-1", "")}
	reader := &fakeStockDriveReader{
		metaResp: &DriveFileMeta{MimeType: "audio/mpeg"},
		dlResp:   io.NopCloser(strings.NewReader("fake-mp3-bytes")),
		dlCT:     "", // empty → triggers Step 1 fallback to meta.MimeType
	}
	handler := newDownloadHandler(lookup, reader)
	rec := runDownloadPOST(t, handler, "clip-audio-stock")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for audio mime happy path, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	gotCT := rec.Header().Get("Content-Type")
	if gotCT != "audio/mpeg" {
		t.Errorf("expected Content-Type=audio/mpeg preserved via fallback, got %q", gotCT)
	}
	if gotCT == "video/mp4" {
		t.Errorf("CRITICAL: audio file mislabeled as video/mp4 (regression of pre-fix behavior)")
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "fake-mp3-bytes" {
		t.Errorf("expected streamed bytes to round-trip, got %q", string(body))
	}
}

// ── TestDownloadStockClip_VideoMime_HappyPath ─────────────────────────

func TestDownloadStockClip_VideoMime_HappyPath(t *testing.T) {
	// Canonical happy path: video/mp4 drive file → 200 + Content-Type
	// echo + body round-trip. This is the most common case for stock
	// footage (provider like Pexels / Pixabay returns MP4).
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("drive-video-1", "")}
	reader := &fakeStockDriveReader{
		metaResp: &DriveFileMeta{MimeType: "video/mp4"},
		dlResp:   io.NopCloser(strings.NewReader("fake-mp4-bytes")),
		dlCT:     "video/mp4",
	}
	handler := newDownloadHandler(lookup, reader)
	rec := runDownloadPOST(t, handler, "clip-video-stock")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for video mime happy path, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	gotCT := rec.Header().Get("Content-Type")
	if gotCT != "video/mp4" {
		t.Errorf("expected Content-Type=video/mp4 preserved, got %q", gotCT)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "fake-mp4-bytes" {
		t.Errorf("expected streamed bytes to round-trip, got %q", string(body))
	}
	if reader.lastFileID != "drive-video-1" {
		t.Errorf("expected reader to receive drive_file_id %q, got %q", "drive-video-1", reader.lastFileID)
	}
}

// ── TestDownloadStockClip_DriveDownloadError_Returns500 ───────────────

func TestDownloadStockClip_DriveDownloadError_Returns500(t *testing.T) {
	// Driver returns (nil, "", err) on DownloadFile. The MIME preflight
	// passes (video/mp4), so the handler reaches the stream call and
	// surfaces the error as 500 Internal Server Error — NOT a 502/503
	// (the underlying Drive API technically returned a service error,
	// but the canonical mapping is 500 for unknown transport errors
	// inside the handler; future maintainers can refine to 502 if a
	// canonical ErrBrowserDownloadFailed sentinel is added).
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("drive-fail-1", "")}
	reader := &fakeStockDriveReader{
		metaResp: &DriveFileMeta{MimeType: "video/mp4"},
		dlErr:    errors.New("simulated drive transport error"),
	}
	handler := newDownloadHandler(lookup, reader)
	rec := runDownloadPOST(t, handler, "clip-broken")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on drive download error, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "drive download failed") {
		t.Errorf("expected 500 message to surface transport error, got %q", errMsg)
	}
}

// ── TestDownloadStockClip_OversizedFile_Returns413 ───────────────────
//
// PR-STOCK-OVERSIZED-DOWNLOAD-GUARD (2026-07-08): size guard fires
// BEFORE DownloadFile streaming. A 3 GiB file flagged as video/mp4 via
// MIME bypass MUST be rejected with HTTP 413 and MUST NOT invoke the
// Drive.DownloadFile reader (godlike/07 NO-FAKE-AVAILABILITY: never
// open the streaming connection for content we've already pre-decided
// to reject — avoids wasted Drive bandwidth).
func TestDownloadStockClip_OversizedFile_Returns413(t *testing.T) {
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("drive-oversized-1", "")}
	reader := &fakeStockDriveReader{
		metaResp: &DriveFileMeta{
			MimeType: "video/mp4",
			Size:     MaxStockDownloadSize + 1, // 1 byte over the 2 GiB cap
		},
		// dlResp / dlErr deliberately unset — the size guard must fire
		// BEFORE DownloadFile is invoked, so reading these fields would
		// fail the test (not just the assertion).
	}
	handler := newDownloadHandler(lookup, reader)
	rec := runDownloadPOST(t, handler, "clip-oversized")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized file, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "exceeds") {
		t.Errorf("expected 413 message to surface size-guard rejection, got %q", errMsg)
	}
	if !strings.Contains(errMsg, strconv.FormatInt(MaxStockDownloadSize, 10)) {
		t.Errorf("expected 413 message to surface cap value, got %q", errMsg)
	}

	// godlike/07 NO-FAKE-AVAILABILITY pin: the size guard fires BEFORE
	// DownloadFile, so the reader.calls counter MUST be exactly 1
	// (GetFileMeta was invoked once for the mime + size lookup) and
	// MUST NOT have advanced further. The fake's `calls` counter
	// increments on EVERY reader method call (both GetFileMeta AND
	// DownloadFile), so this assertion is the canonical pin that the
	// size guard prevented the wasted streaming connection.
	if reader.calls != 1 {
		t.Errorf("expected exactly 1 reader call (GetFileMeta only); got %d (DownloadFile may have been invoked despite rejection -- wasted bandwidth)", reader.calls)
	}
}

// ── TestDownloadStockClip_ExactlyCapSize_PassesGuard ──────────────────
//
// Boundary test: a file exactly at MaxStockDownloadSize (2 GiB exactly)
// MUST pass the size guard (the check is `>` strict-inequality, NOT
// `>=`). Pre-PR literal uses of `>=` would reject the boundary case
// empirically — the canonical invariant is inclusive at the cap.
func TestDownloadStockClip_ExactlyCapSize_PassesGuard(t *testing.T) {
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("drive-exactly-2gib", "")}
	reader := &fakeStockDriveReader{
		metaResp: &DriveFileMeta{
			MimeType: "video/mp4",
			Size:     MaxStockDownloadSize, // EXACTLY 2 GiB
		},
		dlResp: io.NopCloser(strings.NewReader("zero-bytes-2gib-stub")),
		dlCT:   "video/mp4",
	}
	handler := newDownloadHandler(lookup, reader)
	rec := runDownloadPOST(t, handler, "clip-2gib-exact")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for exactly-at-cap file (inclusive boundary), got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// ── TestDownloadStockClip_UndersizedFile_HappyPath ─────────────────────
//
// Regression guard: a small file (100 MiB) flows through the size guard
// cleanly — DownloadFile is invoked, body streams, Content-Type echoes.
// This pins the contract that the size guard is a no-op for in-bound
// files (NOT a transform of the happy path), preserving the pre-PR
// streaming behavior for legitimate stock clips.
func TestDownloadStockClip_UndersizedFile_HappyPath(t *testing.T) {
	const stubSize = 100 * 1024 * 1024 // 100 MiB
	lookup := &fakeStockAssetLookup{asset: newStockAssetFixture("drive-small-1", "")}
	reader := &fakeStockDriveReader{
		metaResp: &DriveFileMeta{
			MimeType: "video/mp4",
			Size:     stubSize,
		},
		dlResp: io.NopCloser(strings.NewReader("fake-mp4-bytes")),
		dlCT:   "video/mp4",
	}
	handler := newDownloadHandler(lookup, reader)
	rec := runDownloadPOST(t, handler, "clip-small-stock")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for 100 MiB happy path, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	gotCT := rec.Header().Get("Content-Type")
	if gotCT != "video/mp4" {
		t.Errorf("expected Content-Type=video/mp4 preserved, got %q", gotCT)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "fake-mp4-bytes" {
		t.Errorf("expected streamed bytes to round-trip, got %q", string(body))
	}
}
