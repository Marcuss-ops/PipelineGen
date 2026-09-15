// Package cliprender — handler_test.go: POST /api/clips/render wire
// contract tests.
//
// The stub job.Service records the EnqueueRequest so each test can
// assert on:
//   - EnqueueRequest.Type (must equal cliprender.TypeClipRender)
//   - EnqueueRequest.Payload (normalized *RenderRequest, field by field)
//   - HTTP response status + body shape ({job_id, status: "QUEUED"})
package cliprender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stubJobsSvc captures the EnqueueRequest so tests can assert on the
// canonical wire shape. All unused Service methods return zero values.
type stubJobsSvc struct {
	enqueued  *job.EnqueueRequest
	returnJob *job.Job
	returnErr error
}

func (s *stubJobsSvc) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.enqueued = req
	if s.returnJob == nil {
		s.returnJob = &job.Job{ID: "job_default"}
	}
	return s.returnJob, s.returnErr
}
func (s *stubJobsSvc) Get(_ context.Context, _ string) (*job.Job, error) { return nil, nil }
func (s *stubJobsSvc) Cancel(_ context.Context, _ string) error          { return nil }
func (s *stubJobsSvc) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *stubJobsSvc) IsTerminal(status job.Status) bool {
	return status == job.StatusSucceeded || status == job.StatusFailed || status == job.StatusCancelled
}
func (s *stubJobsSvc) RegisterHandler(_ string, _ any) error { return nil }
func (s *stubJobsSvc) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (s *stubJobsSvc) Retry(_ context.Context, _ string) (*job.Job, error) { return nil, nil }

// compile-time assertion: stubJobsSvc satisfies job.Service.
var _ job.Service = (*stubJobsSvc)(nil)

// newTestRouter wires the canonical Handler on a gin engine rooted at
// /clips, mirroring the production RegisterRoutes bound under
// /api/clips/*.
func newTestRouter(svc job.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewHandler(svc, zap.NewNop())
	rg := gin.New()
	h.RegisterRoutes(&rg.RouterGroup)
	return rg
}

// doRender performs a POST /render with the given JSON body.
func doRender(r *gin.Engine, rawBody string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/render", bytes.NewReader([]byte(rawBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestRenderHandler_RejectsOversizedBody pins item 19: the request body is
// bounded BEFORE decoding, so a caller streaming an unbounded JSON document
// fails at the transport (413 PAYLOAD_TOO_LARGE) and never occupies a Master
// worker slot.
func TestRenderHandler_RejectsOversizedBody(t *testing.T) {
	jobsSvc := &stubJobsSvc{}
	r := newTestRouter(jobsSvc)

	huge := strings.Repeat("a", int(MaxClipRenderRequestBytes)+1)
	rec := doRender(r, `{"source_asset_id":"`+huge+`"}`)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	var resp renderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ErrorCode != ErrCodePayloadTooLarge {
		t.Fatalf("error_code = %q, want %q", resp.ErrorCode, ErrCodePayloadTooLarge)
	}
	if jobsSvc.enqueued != nil {
		t.Fatal("oversized body must never reach the Master enqueue path")
	}
}

// TestRenderHandler_HappyPath_CanonicalWireShape verifies the spec
// request round-trips into a normalized RenderRequest payload and the
// endpoint responds 202 {job_id, status: QUEUED}.
func TestRenderHandler_HappyPath_CanonicalWireShape(t *testing.T) {
	jobsSvc := &stubJobsSvc{
		returnJob: &job.Job{ID: "job_render_1"},
	}
	r := newTestRouter(jobsSvc)

	body := `{
		"source_asset_id": "asset-123",
		"background": {"mode": "blur_source", "asset_id": ""},
		"watermark": {"enabled": true, "asset_id": "watermark-main", "position": "top_right", "opacity": 0.85, "margin_px": 40},
		"transcript": {"mode": "reuse", "language": "en", "persist": true},
		"subtitles": {"enabled": true, "mode": "burn", "style_id": "shorts-v1"},
		"output": {"contract": "VELOX_ASSEMBLY_READY_V1", "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1},
		"audio": {"mode": "copy_if_compatible"},
		"destination": {"drive_folder_id": "1Ay0swz9xkwPoJErvpE_qkYowHCf1OSwC"}
	}`

	rec := doRender(r, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued == nil {
		t.Fatal("expected Enqueue to be called")
	}
	if jobsSvc.enqueued.Type != TypeClipRender {
		t.Errorf("Type: got %q, want %q", jobsSvc.enqueued.Type, TypeClipRender)
	}

	req, ok := jobsSvc.enqueued.Payload.(*RenderRequest)
	if !ok {
		t.Fatalf("Payload type: got %T, want *RenderRequest", jobsSvc.enqueued.Payload)
	}
	if req.SourceAssetID != "asset-123" {
		t.Errorf("SourceAssetID: got %q, want asset-123", req.SourceAssetID)
	}
	if req.Background.Mode != BackgroundModeBlurSource {
		t.Errorf("Background.Mode: got %q, want blur_source", req.Background.Mode)
	}
	if !req.Watermark.Enabled || req.Watermark.AssetID != "watermark-main" ||
		req.Watermark.Position != PositionTopRight ||
		req.Watermark.Opacity != 0.85 || req.Watermark.MarginPX != 40 {
		t.Errorf("Watermark: got %+v", req.Watermark)
	}
	if req.Transcript.Mode != TranscriptModeReuse ||
		req.Transcript.Language != "en" || !req.Transcript.Persist {
		t.Errorf("Transcript: got %+v", req.Transcript)
	}
	if !req.Subtitles.Enabled || req.Subtitles.Mode != SubtitlesModeBurn ||
		req.Subtitles.StyleID != "shorts-v1" {
		t.Errorf("Subtitles: got %+v", req.Subtitles)
	}
	if req.Output.Contract != OutputContractVeloxAssemblyReadyV1 ||
		req.Output.Width != 1920 || req.Output.Height != 1080 || req.Output.FPSNum != 24 || req.Output.FPSDen != 1 {
		t.Errorf("Output: got %+v", req.Output)
	}
	if req.Audio.Mode != AudioModeCopyIfCompatible {
		t.Errorf("Audio.Mode: got %q, want copy_if_compatible", req.Audio.Mode)
	}
	if req.Destination.DriveFolderID != "1Ay0swz9xkwPoJErvpE_qkYowHCf1OSwC" {
		t.Errorf("Destination.DriveFolderID: got %q", req.Destination.DriveFolderID)
	}

	// Verify 202 body contains the canonical fields.
	var resp renderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("resp unmarshal: %v body=%s", err, rec.Body.String())
	}
	if resp.JobID != "job_render_1" {
		t.Errorf("resp.job_id: got %q, want job_render_1", resp.JobID)
	}
	if resp.Status != StatusQueued {
		t.Errorf("resp.status: got %q, want QUEUED", resp.Status)
	}
}

// TestRenderHandler_MinimalRequest_AppliesDefaults verifies an empty
// body (except source_asset_id) normalizes to the canonical defaults
// and enqueues.
func TestRenderHandler_MinimalRequest_AppliesDefaults(t *testing.T) {
	jobsSvc := &stubJobsSvc{}
	r := newTestRouter(jobsSvc)

	rec := doRender(r, `{"source_asset_id": "asset-min"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	req := jobsSvc.enqueued.Payload.(*RenderRequest)
	if req.Background.Mode != BackgroundModeNone {
		t.Errorf("Background.Mode default: got %q, want none", req.Background.Mode)
	}
	if req.Watermark.Enabled {
		t.Error("Watermark default: must be disabled")
	}
	if req.Transcript.Mode != TranscriptModeReuse ||
		req.Transcript.Language != "en" || !req.Transcript.Persist {
		t.Errorf("Transcript defaults: got %+v (persist must default to true so a batch reuses one ASR pass per source)", req.Transcript)
	}
	if req.Subtitles.Enabled {
		t.Error("Subtitles default: must be disabled")
	}
	if req.Output.Contract != OutputContractVeloxAssemblyReadyV1 ||
		req.Output.Width != 1920 || req.Output.Height != 1080 || req.Output.FPSNum != 24 || req.Output.FPSDen != 1 {
		t.Errorf("Output defaults: got %+v", req.Output)
	}
	if req.Audio.Mode != AudioModeCopyIfCompatible {
		t.Errorf("Audio.Mode default: got %q", req.Audio.Mode)
	}
	if req.Destination.DriveFolderID != DefaultDriveRootFolderID {
		t.Errorf("Destination default: got %q, want %q", req.Destination.DriveFolderID, DefaultDriveRootFolderID)
	}
}

// TestRenderHandler_RejectsMissingSourceAsset verifies the mandatory
// source_asset_id gate (400, no enqueue).
func TestRenderHandler_RejectsMissingSourceAsset(t *testing.T) {
	jobsSvc := &stubJobsSvc{}
	r := newTestRouter(jobsSvc)

	rec := doRender(r, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp renderResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != StatusError || resp.ErrorCode != ErrCodeInvalidPayload {
		t.Errorf("resp: got %+v, want status=error error_code=INVALID_PAYLOAD", resp)
	}
	if !strings.Contains(resp.Error, "source_asset_id") {
		t.Errorf("error should mention source_asset_id; got %q", resp.Error)
	}
	if jobsSvc.enqueued != nil {
		t.Error("enqueue must not be called on validation failure")
	}
}

// TestRenderHandler_RejectsUnknownField verifies the strict decoder
// rejects undeclared JSON fields (UNKNOWN_FIELD).
func TestRenderHandler_RejectsUnknownField(t *testing.T) {
	jobsSvc := &stubJobsSvc{}
	r := newTestRouter(jobsSvc)

	rec := doRender(r, `{"source_asset_id": "x", "not_a_field": true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp renderResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ErrorCode != ErrCodeUnknownField {
		t.Errorf("error_code: got %q, want UNKNOWN_FIELD", resp.ErrorCode)
	}
	if jobsSvc.enqueued != nil {
		t.Error("enqueue must not be called on unknown field")
	}
}

// TestRenderHandler_RejectsMalformedJSON verifies syntax errors map
// to 400 INVALID_PAYLOAD.
func TestRenderHandler_RejectsMalformedJSON(t *testing.T) {
	jobsSvc := &stubJobsSvc{}
	r := newTestRouter(jobsSvc)

	rec := doRender(r, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp renderResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ErrorCode != ErrCodeInvalidPayload {
		t.Errorf("error_code: got %q, want INVALID_PAYLOAD", resp.ErrorCode)
	}
}

// TestRenderHandler_RejectsWatermarkWithoutAsset verifies
// watermark.enabled=true requires a canonical asset_id (never a raw
// path in the payload).
func TestRenderHandler_RejectsWatermarkWithoutAsset(t *testing.T) {
	jobsSvc := &stubJobsSvc{}
	r := newTestRouter(jobsSvc)

	rec := doRender(r, `{"source_asset_id": "x", "watermark": {"enabled": true}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued != nil {
		t.Error("enqueue must not be called on validation failure")
	}
}

// TestRenderHandler_RejectsInvalidBackgroundMode verifies the
// background mode enum gate.
func TestRenderHandler_RejectsInvalidBackgroundMode(t *testing.T) {
	jobsSvc := &stubJobsSvc{}
	r := newTestRouter(jobsSvc)

	rec := doRender(r, `{"source_asset_id": "x", "background": {"mode": "mosaic"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = doRender(r, `{"source_asset_id": "x", "background": {"mode": "asset"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("background mode=asset without asset_id: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if jobsSvc.enqueued != nil {
		t.Error("enqueue must not be called on validation failure")
	}
}

// TestRenderHandler_EnqueueErrorReturns500 verifies a failure inside
// the Master maps to 500 (never a silent success — godlike/07).
func TestRenderHandler_EnqueueErrorReturns500(t *testing.T) {
	jobsSvc := &stubJobsSvc{
		returnErr: errors.New("sqlite: database is locked"),
	}
	r := newTestRouter(jobsSvc)

	rec := doRender(r, `{"source_asset_id": "x"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRenderHandler_NilJobsSvcReturns503 verifies the fail-closed
// contract: an unavailable Master is never a successful no-op.
func TestRenderHandler_NilJobsSvcReturns503(t *testing.T) {
	r := newTestRouter(nil)

	rec := doRender(r, `{"source_asset_id": "x"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var resp renderResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ErrorCode != ErrCodeJobsUnavailable {
		t.Errorf("error_code: got %q, want JOBS_UNAVAILABLE", resp.ErrorCode)
	}
}

// TestRenderHandler_RegistersOnlyRenderRoute verifies the slim surface:
// only POST /render is exposed under the /clips group.
func TestRenderHandler_RegistersOnlyRenderRoute(t *testing.T) {
	r := newTestRouter(&stubJobsSvc{})
	hasRender := false
	for _, route := range r.Routes() {
		if route.Path == "/render" && route.Method == "POST" {
			hasRender = true
		}
	}
	if !hasRender {
		t.Fatal("expected POST /render to be registered")
	}
}

// TestRenderHandler_ClassicPaleOliveBackgroundSubsWatermark verifies the
// canonical "classic pale olive" use case:
//
//   - background mode=asset pointing at the Pale Olive Classic plate
//   - subtitles enabled (burn mode, default style)
//   - watermark TEXT in top_right (no asset_id — text watermark path)
//
// This is the day-1 benchmark scenario: the handler must accept the
// payload, normalise it, and enqueue without touching the GPU. The
// wall-clock budget at the HTTP transport layer (stub jobs service, no
// DB) is < 5 ms for a single request; repeated calls must be stable.
func TestRenderHandler_ClassicPaleOliveBackgroundSubsWatermark(t *testing.T) {
	jobsSvc := &stubJobsSvc{
		returnJob: &job.Job{ID: "job-pale-olive-1"},
	}
	r := newTestRouter(jobsSvc)

	// "classic pale olive" IS the canonical asset id `classic1`: the Pale Olive
	// Classic plate registered in the media DB (`media_assets.id='classic1'`,
	// `media_type='video'`) and stored at data/backgrounds/classic1.mp4. It is
	// a VIDEO plate, so the declared kind must be video — declaring `image`
	// would sample the wrong render layer. The id is the stable catalog
	// identifier; the worker resolves the bytes at render time via the
	// PreparedAssetResolver.
	const paleOliveAssetID = "classic1"
	const paleOliveMediaType = "video"
	const watermarkText = "VeloxEditing"

	// Guard the pair: the kind this test declares must be exactly what the
	// canonical taxonomy derives from the plate's media type. Without this the
	// test could pin a family the catalog disagrees with (which is how the
	// original `bg-classic-pale-olive` + `image` payload shipped).
	if kind, ok := BackgroundKindFromMediaType(paleOliveMediaType); !ok || kind != BackgroundKindVideo {
		t.Fatalf("BackgroundKindFromMediaType(%q) = (%q, %v), want (%q, true)", paleOliveMediaType, kind, ok, BackgroundKindVideo)
	}

	body := `{
		"source_asset_id": "asset-clip-001",
		"background": {
			"mode": "asset",
			"asset_id": "` + paleOliveAssetID + `",
			"kind": "` + BackgroundKindVideo + `"
		},
		"subtitles": {
			"enabled": true,
			"mode": "burn"
		},
		"watermark": {
			"enabled": true,
			"text": "` + watermarkText + `",
			"position": "top_right"
		}
	}`

	start := time.Now()
	rec := doRender(r, body)
	elapsed := time.Since(start)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	// ── timing gate ────────────────────────────────────────────────────
	// The handler has zero I/O (stub jobs, no DB, no GPU): wall clock
	// must be well under 5 ms even on a loaded CI host.
	const maxHandlerLatency = 5 * time.Millisecond
	if elapsed > maxHandlerLatency {
		t.Errorf("handler latency %v exceeds budget %v (check for unexpected I/O or lock)", elapsed, maxHandlerLatency)
	}
	t.Logf("handler latency: %v", elapsed)

	// ── payload assertions ─────────────────────────────────────────────
	if jobsSvc.enqueued == nil {
		t.Fatal("expected Enqueue to be called")
	}
	req, ok := jobsSvc.enqueued.Payload.(*RenderRequest)
	if !ok {
		t.Fatalf("Payload type: got %T, want *RenderRequest", jobsSvc.enqueued.Payload)
	}

	// Background: Pale Olive Classic video plate.
	if req.Background.Mode != BackgroundModeAsset {
		t.Errorf("Background.Mode: got %q, want %q", req.Background.Mode, BackgroundModeAsset)
	}
	if req.Background.AssetID != paleOliveAssetID {
		t.Errorf("Background.AssetID: got %q, want %q", req.Background.AssetID, paleOliveAssetID)
	}
	if req.Background.Kind != BackgroundKindVideo {
		t.Errorf("Background.Kind: got %q, want %q (classic1 Pale Olive is a video plate)", req.Background.Kind, BackgroundKindVideo)
	}

	// Subtitles: burned into the video.
	if !req.Subtitles.Enabled {
		t.Error("Subtitles.Enabled: must be true")
	}
	if req.Subtitles.Mode != SubtitlesModeBurn {
		t.Errorf("Subtitles.Mode: got %q, want %q", req.Subtitles.Mode, SubtitlesModeBurn)
	}
	// Normalize() must have injected the default subtitle style (58px).
	if req.Subtitles.Style == nil {
		t.Error("Subtitles.Style: Normalize() must inject default style when enabled and style is nil")
	} else if req.Subtitles.Style.FontSizePX != 58 {
		t.Errorf("Subtitles.Style.FontSizePX: got %v, want 58 (shorts-v1 preset)", req.Subtitles.Style.FontSizePX)
	}

	// Watermark: text overlay in top_right.
	if !req.Watermark.Enabled {
		t.Error("Watermark.Enabled: must be true")
	}
	if req.Watermark.Text != watermarkText {
		t.Errorf("Watermark.Text: got %q, want %q", req.Watermark.Text, watermarkText)
	}
	if req.Watermark.Position != PositionTopRight {
		t.Errorf("Watermark.Position: got %q, want %q", req.Watermark.Position, PositionTopRight)
	}
	// Normalize() defaults text-watermark margin to 100 px.
	if req.Watermark.MarginPX != 100 {
		t.Errorf("Watermark.MarginPX: got %d, want 100 (text-watermark default)", req.Watermark.MarginPX)
	}
	// Normalize() must inject the default text-watermark style (34px).
	if req.Watermark.Style == nil {
		t.Error("Watermark.Style: Normalize() must inject default text style when text is non-empty")
	} else if req.Watermark.Style.FontSizePX != 34 {
		t.Errorf("Watermark.Style.FontSizePX: got %v, want 34 (text watermark default)", req.Watermark.Style.FontSizePX)
	}
	if req.Watermark.AssetID != "" {
		t.Errorf("Watermark.AssetID: expected empty for text watermark, got %q", req.Watermark.AssetID)
	}

	// Defaults that must still be applied.
	if req.Output.Contract != OutputContractVeloxAssemblyReadyV1 {
		t.Errorf("Output.Contract default: got %q", req.Output.Contract)
	}
	if req.Output.Width != 1920 || req.Output.Height != 1080 {
		t.Errorf("Output resolution defaults: got %dx%d, want 1920x1080", req.Output.Width, req.Output.Height)
	}
	if req.Transcript.Mode != TranscriptModeReuse || !req.Transcript.Persist {
		t.Errorf("Transcript defaults: got %+v", req.Transcript)
	}

	// Response envelope.
	var resp renderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("resp unmarshal: %v body=%s", err, rec.Body.String())
	}
	if resp.JobID != "job-pale-olive-1" {
		t.Errorf("resp.job_id: got %q, want job-pale-olive-1", resp.JobID)
	}
	if resp.Status != StatusQueued {
		t.Errorf("resp.status: got %q, want QUEUED", resp.Status)
	}
}

// TestRenderHandler_ClassicPaleOliveBackground_RepeatedCallsStable stress-tests
// the handler for the pale-olive+subs+watermark combination over 100 repeated
// calls and verifies the AVERAGE latency stays under 2 ms (stub jobs service,
// zero I/O, measures the pure Go hot-path).
func TestRenderHandler_ClassicPaleOliveBackground_RepeatedCallsStable(t *testing.T) {
	jobsSvc := &stubJobsSvc{returnJob: &job.Job{ID: "job-repeat"}}
	r := newTestRouter(jobsSvc)

	body := `{
		"source_asset_id": "asset-clip-stress",
		"background": {"mode": "asset", "asset_id": "classic1", "kind": "video"},
		"subtitles": {"enabled": true, "mode": "burn"},
		"watermark": {"enabled": true, "text": "VeloxEditing", "position": "top_right"}
	}`

	const N = 100
	var total time.Duration
	for i := 0; i < N; i++ {
		start := time.Now()
		rec := doRender(r, body)
		total += time.Since(start)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("iteration %d: status %d, want 202; body=%s", i, rec.Code, rec.Body.String())
		}
	}
	avg := total / N
	t.Logf("avg latency over %d calls: %v (total %v)", N, avg, total)
	const maxAvg = 2 * time.Millisecond
	if avg > maxAvg {
		t.Errorf("avg latency %v exceeds %v budget; check for unexpected allocation or lock", avg, maxAvg)
	}
}

// TestBuild_FailsClosedOnMissingDeps verifies the composition-time
// fail-closed contract: nil Jobs or nil EnabledFunc abort Build.
func TestBuild_FailsClosedOnMissingDeps(t *testing.T) {
	if _, err := Build(Dependencies{}); err == nil {
		t.Fatal("Build with nil Jobs must fail")
	}
	if _, err := Build(Dependencies{Jobs: &stubJobsSvc{}}); err == nil {
		t.Fatal("Build with nil EnabledFunc must fail")
	}
	d, err := Build(Dependencies{Jobs: &stubJobsSvc{}, EnabledFunc: func() bool { return true }})
	if err != nil {
		t.Fatalf("Build with all deps must succeed: %v", err)
	}
	if d.Name() != "clip-render" {
		t.Errorf("module name: got %q, want clip-render", d.Name())
	}
	if !d.Enabled() {
		t.Error("module must be enabled")
	}
}
