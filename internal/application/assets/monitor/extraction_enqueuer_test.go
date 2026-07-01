package monitor

import (
	"context"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// extraction_enqueuer_test.go — Step 9 follow-up (PR-MONITOR-ENQUEUER-WIRE,
// June 2026): NIL-GUARD contract coverage for the legacy
// (pre-Fase-8) ExtractionEnqueuer constructor at
// internal/application/assets/monitor/extraction_enqueuer.go.
//
// Phase 8 (July 2026, Spina Dorsale — monitoradapter consolidation):
// the marshal concern `monitor.EnqueueExtractRequest →
// youtubetypes.ExtractRequest` (extraction_enqueuer.go line ~168 in
// the pre-Fase-8 source) and the canonical-bind path moved into the
// youtube domain as `monitoradapter` (see
// internal/application/youtube/adapters/monitoradapter/extraction_intent_adapter.go).
//
// The 5 pinned contract tests that were here (CollisionNoOp,
// HappyPath_CursorOnSuccess, CursorUpdateFailureTolerance,
// EnqueueErrorSurfaces, FindActiveByKeyErrorIsConservative) moved
// to monitoradapter to live alongside the canonical concrete
// adapter — the canonical bind now lives at the marshal site. The
// 2 remaining tests below lock the LEGACY constructor's nil-guard
// safety net so a future regression on ExtractionEnqueuer(nil, ...)
// that fires "composition bug — extraction wrapper was not wired"
// errors is caught by tests even after the monitor-side
// `extraction_enqueuer.go` ships only as a back-compat surface.
//
// The fakes satisfy the adapter's port interfaces
// (JobsEnqueuerSvc, ChannelsCursorSvc) via implicit method-set
// matching — no production wiring churn is required, since
// *jobtools.Service + *channels.Service already satisfy the same
// interface shapes.
//
// See also: internal/application/youtube/adapters/monitoradapter/
// extraction_intent_adapter_test.go (canonical 5-contract test
// surface for the new adapter).

// ── Test fakes (ports JobsEnqueuerSvc + ChannelsCursorSvc) ─────────────────

// fakeJobsSvc is a minimal JobsEnqueuerSvc stub. The CollisionNoOp
// + ActiveKeyDerivationError + HappyPath + EnqueueErrorSurfaces
// contracts that consumed the pre-migration surfaces (FindActiveByKey
// + Enqueue capture) have been moved verbatim into the monitoradapter
// test file; the fields are retained here to keep the nil-guard
// test surface self-contained (the constructor pinning is the affordance).
type fakeJobsSvc struct {
	findActiveResp   *jobservice.Job
	findActiveErr    error
	findActiveCalls  int
	findActiveKeyArg string

	enqueueErr     error
	enqueueCalls   int
	lastEnqueueReq *jobservice.EnqueueRequest
}

func (f *fakeJobsSvc) FindActiveByKey(_ context.Context, activeKey string) (*jobservice.Job, error) {
	f.findActiveCalls++
	f.findActiveKeyArg = activeKey
	return f.findActiveResp, f.findActiveErr
}

func (f *fakeJobsSvc) Enqueue(_ context.Context, req *jobservice.EnqueueRequest) (*jobservice.Job, error) {
	f.enqueueCalls++
	f.lastEnqueueReq = req
	if f.enqueueErr != nil {
		return nil, f.enqueueErr
	}
	return &jobservice.Job{ID: "job-" + req.ActiveKey, Status: jobservice.StatusQueued}, nil
}

// fakeChannelsSvc is a minimal ChannelsCursorSvc stub. Symmetric
// retention rationale as fakeJobsSvc above — the CollisionNoOp +
// CursorUpdateFailureTolerance contracts moved into the monitoradapter
// test file; the cursor field is retained for NilChannelsSvcDegrades.
type fakeChannelsSvc struct {
	updateCursorErr   error
	updateCursorCalls int
	lastCursorCommand channels.UpdateCursorCommand
}

func (f *fakeChannelsSvc) UpdateCursor(_ context.Context, cmd channels.UpdateCursorCommand) error {
	f.updateCursorCalls++
	f.lastCursorCommand = cmd
	return f.updateCursorErr
}

// ── Test fixtures ──────────────────────────────────────────────────────────

func extractEnqChannelFixture() channels.Channel {
	return channels.Channel{
		ID:            "ch-1",
		ChannelURL:    "https://www.youtube.com/@Test",
		Category:      "test-cat",
		DriveFolderID: "fld-1",
	}
}

func extractEnqRequestFixture() EnqueueExtractRequest {
	return EnqueueExtractRequest{
		VideoID:       "vid-1",
		Title:         "Test Title",
		URL:           "https://www.youtube.com/watch?v=vid-1",
		Group:         "test-cat",
		DriveFolderID: "fld-1",
		Segments: []ytdomain.Segment{
			{Start: "00:01", End: "00:30", Name: "Segment One"},
		},
		Channel: extractEnqChannelFixture(),
	}
}

// ── Test contracts (legacy ExtractionEnqueuer nil-guard coverage) ─────────

// TestExtractionEnqueuer_NilJobsSvcFailsLoudly verifies the
// composition-bug safety net: a nil jobsSvc is treated as a hard
// wiring failure that returns an error immediately, so the monitor's
// per-video error log captures the gap rather than silently dropping
// the durable-job emission.
//
// A nil channelsSvc (the other half of the wiring) degrades to a
// cursor-update no-op without failing the call — that's tested in
// the test below.
func TestExtractionEnqueuer_NilJobsSvcFailsLoudly(t *testing.T) {
	e := NewExtractionEnqueuer(nil, &fakeChannelsSvc{}, zap.NewNop())
	err := e.EnqueueExtract(context.Background(), extractEnqRequestFixture())
	if err == nil {
		t.Fatal("expected error when jobsSvc is nil, got nil")
	}
}

// TestExtractionEnqueuer_NilChannelsSvcDegrades verifies the
// best-effort degrade path: a nil channelsSvc means the broker-side
// enqueue IS still recorded (the durable-jobs system is the source
// of truth), but the channel cursor stays where it was. The next
// scheduler tick will retry and complete the cursor update.
//
// Shape pinned: enqueueCalls=1 && returnErr==nil (no panic, no
// surfaced error). The adapter logs a warn instead.
func TestExtractionEnqueuer_NilChannelsSvcDegrades(t *testing.T) {
	jsvc := &fakeJobsSvc{}
	e := NewExtractionEnqueuer(jsvc, nil, zap.NewNop())

	req := extractEnqRequestFixture()
	if err := e.EnqueueExtract(context.Background(), req); err != nil {
		t.Fatalf("expected nil when channelsSvc is nil (best-effort degrade), got %v", err)
	}
	if jsvc.enqueueCalls != 1 {
		t.Errorf("Enqueue must still be called when channelsSvc is nil (broker is source of truth), got %d calls",
			jsvc.enqueueCalls)
	}
}
