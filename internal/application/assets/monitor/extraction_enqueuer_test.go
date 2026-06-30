package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// extraction_enqueuer_test.go — Step 9 follow-up (PR-MONITOR-ENQUEUER-WIRE,
// June 2026): end-to-end verification of the concrete JobEnqueuer adapter
// (extraction_enqueuer.go) through its port interfaces.
//
// The 3 cases below are the pinned contracts from monitor_enqueue_test.go
// (enqueueFromAnalysis) re-verified through the real binding rather than
// the unbound stub. Two bonus cases (broker-error + nil-jobsSvc) lock the
// contract surface that the port contract documents:
//
//   - Contract 1 (collision NO-OP):  pre-check sees an active job, adapter
//     returns nil without touching the broker or the cursor.
//   - Contract 2 (cursor on success):  broker enqueue + cursor update both
//     fire with correct ActiveKey prefix + payload shape + cursor fields.
//   - Contract 3 (cursor tolerance):  cursor update fails but adapter
//     returns nil (broker-recorded enqueue is the source of truth).
//   - Bonus A (broker surface):  broker-side enqueue error propagates so
//     the monitor's per-video error log captures it.
//   - Bonus B (nil-jobsSvc):  hard wiring failure surfaces immediately so
//     composition bugs cannot silently no-op the per-tick retry path.
//
// The fakes satisfy the adapter's port interfaces
// (JobsEnqueuerSvc, ChannelsCursorSvc) via implicit method-set matching
// — no production wiring churn is required, since *jobtools.Service +
// *channels.Service already satisfy the same interface shapes.

// ── Test fakes (ports JobsEnqueuerSvc + ChannelsCursorSvc) ─────────────────

// fakeJobsSvc is a minimal JobsEnqueuerSvc stub. Captures the
// Enqueue-recording surfaces the test cases assert on, and lets the
// collision pre-check be driven via findActive.
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

// fakeChannelsSvc is a minimal ChannelsCursorSvc stub. Records every
// UpdateCursor invocation so tests can assert the exact (id, cursor)
// tuple sent.
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

// ── Test contracts ─────────────────────────────────────────────────────────

// TestExtractionEnqueuer_CollisionNoOp verifies contract 1: when a
// non-terminal job already exists for ActiveKey="channel_sync_<VideoID>",
// the adapter returns nil WITHOUT calling Enqueue or UpdateCursor. The
// broker-recorded enqueue is unchanged; the channel cursor stays where
// it was; the monitor sees no error.
//
// Shape pinned: enqueueCalls=0 && updateCursorCalls=0 && returnErr==nil.
// The pre-check is the load-bearing piece — without it, a duplicate
// enqueue would post a second durable job + advance the cursor past a
// video we didn't actually process in this tick.
func TestExtractionEnqueuer_CollisionNoOp(t *testing.T) {
	jsvc := &fakeJobsSvc{
		findActiveResp: &jobservice.Job{
			ID:     "job-existing",
			Status: jobservice.StatusQueued,
		},
	}
	csvc := &fakeChannelsSvc{}
	e := NewExtractionEnqueuer(jsvc, csvc, zap.NewNop())

	req := extractEnqRequestFixture()
	err := e.EnqueueExtract(context.Background(), req)
	if err != nil {
		t.Fatalf("expected nil on collision, got %v", err)
	}
	if jsvc.findActiveCalls != 1 {
		t.Errorf("FindActiveByKey should be called exactly once, got %d", jsvc.findActiveCalls)
	}
	if jsvc.findActiveKeyArg != ActiveKeyPrefix+req.VideoID {
		t.Errorf("FindActiveByKey activeKey arg = %q, want %q",
			jsvc.findActiveKeyArg, ActiveKeyPrefix+req.VideoID)
	}
	if jsvc.enqueueCalls != 0 {
		t.Errorf("Enqueue must NOT be called on collision, got %d calls", jsvc.enqueueCalls)
	}
	if csvc.updateCursorCalls != 0 {
		t.Errorf("UpdateCursor must NOT be called on collision, got %d calls", csvc.updateCursorCalls)
	}
}

// TestExtractionEnqueuer_HappyPath_CursorOnSuccess verifies contract 2
// + locks the canonical ActiveKey prefix + ExtractRequest payload
// shape + cursor command fields.
//
// Shape pinned:
//   - FindActiveByKey called once with ActiveKey="channel_sync_vid-1"
//   - Enqueue called once with type="youtube_clip.extract", ActiveKey
//     matches, Payload is *youtubetypes.ExtractRequest with the right
//     URL, Segments, Destination{Group, FolderID}
//   - UpdateCursor called once with (channel.ID, videoID)
//   - returnErr == nil
func TestExtractionEnqueuer_HappyPath_CursorOnSuccess(t *testing.T) {
	jsvc := &fakeJobsSvc{} // no collision
	csvc := &fakeChannelsSvc{}
	e := NewExtractionEnqueuer(jsvc, csvc, zap.NewNop())

	req := extractEnqRequestFixture()
	if err := e.EnqueueExtract(context.Background(), req); err != nil {
		t.Fatalf("expected nil on success, got %v", err)
	}
	if jsvc.findActiveCalls != 1 {
		t.Errorf("FindActiveByKey should be called exactly once, got %d", jsvc.findActiveCalls)
	}
	if want := ActiveKeyPrefix + req.VideoID; jsvc.findActiveKeyArg != want {
		t.Errorf("FindActiveByKey activeKey = %q, want %q", jsvc.findActiveKeyArg, want)
	}
	if jsvc.enqueueCalls != 1 {
		t.Fatalf("Enqueue should be called exactly once, got %d", jsvc.enqueueCalls)
	}
	if jsvc.lastEnqueueReq == nil {
		t.Fatal("Enqueue was called but lastEnqueueReq is nil (test fixture bug)")
	}
	if jsvc.lastEnqueueReq.Type != jobservice.TypeYouTubeClipExtract {
		t.Errorf("Enqueue Type = %q, want %q",
			jsvc.lastEnqueueReq.Type, jobservice.TypeYouTubeClipExtract)
	}
	if want := ActiveKeyPrefix + req.VideoID; jsvc.lastEnqueueReq.ActiveKey != want {
		t.Errorf("Enqueue ActiveKey = %q, want %q", jsvc.lastEnqueueReq.ActiveKey, want)
	}
	if jsvc.lastEnqueueReq.VideoName != req.Title {
		t.Errorf("Enqueue VideoName = %q, want %q (req.Title)",
			jsvc.lastEnqueueReq.VideoName, req.Title)
	}
	// Payload shape: ytdomain.ExtractRequest (value, NOT pointer and NOT
	// map[string]any). The job handler unmarshals into a value form
	// (see internal/application/youtube/jobs/job_handler.go:47 —
	// `var req youtubetypes.ExtractRequest`), so a value assertion
	// matches the consumer-side decode contract.
	payload, ok := jsvc.lastEnqueueReq.Payload.(ytdomain.ExtractRequest)
	if !ok {
		t.Fatalf("Enqueue Payload type = %T, want ytdomain.ExtractRequest value (job handler unmarshals this exact shape)",
			jsvc.lastEnqueueReq.Payload)
	}
	if payload.URL != req.URL {
		t.Errorf("Payload.URL = %q, want %q", payload.URL, req.URL)
	}
	if len(payload.Segments) != len(req.Segments) {
		t.Errorf("Payload.Segments len = %d, want %d", len(payload.Segments), len(req.Segments))
	}
	if payload.Destination == nil {
		t.Fatal("Payload.Destination is nil — adapter must wrap Group+FolderID into the Destination struct")
	}
	if payload.Destination.Group != req.Group {
		t.Errorf("Payload.Destination.Group = %q, want %q", payload.Destination.Group, req.Group)
	}
	if payload.Destination.FolderID != req.DriveFolderID {
		t.Errorf("Payload.Destination.FolderID = %q, want %q",
			payload.Destination.FolderID, req.DriveFolderID)
	}
	// Cursor update exactness
	if csvc.updateCursorCalls != 1 {
		t.Errorf("UpdateCursor should be called exactly once on success, got %d", csvc.updateCursorCalls)
	}
	if csvc.lastCursorCommand.ID != req.Channel.ID {
		t.Errorf("UpdateCursor.ID = %q, want %q", csvc.lastCursorCommand.ID, req.Channel.ID)
	}
	if csvc.lastCursorCommand.Cursor != req.VideoID {
		t.Errorf("UpdateCursor.Cursor = %q, want %q", csvc.lastCursorCommand.Cursor, req.VideoID)
	}
}

// TestExtractionEnqueuer_CursorUpdateFailureTolerance verifies
// contract 3: when channels.UpdateCursor fails (e.g. SQLite transient
// lock, network blip), the adapter SIGHS + logs warn but returns nil.
// The durable-jobs broker-recorded enqueue IS the source of truth;
// cursor updates are an observability convenience, not a correctness
// gate.
//
// Shape pinned: enqueueCalls=1 && updateCursorCalls=1 && returnErr==nil.
// Verifies the contract by asserting both that the broker enqueue
// DID land (otherwise the per-video recovery would be lost) AND that
// the cursor was attempted (otherwise the failure-tolerance decision
// becomes ambiguous vs. a no-op).
func TestExtractionEnqueuer_CursorUpdateFailureTolerance(t *testing.T) {
	jsvc := &fakeJobsSvc{}
	csvc := &fakeChannelsSvc{
		updateCursorErr: errors.New("sqlite: database is locked (test simulation)"),
	}
	e := NewExtractionEnqueuer(jsvc, csvc, zap.NewNop())

	req := extractEnqRequestFixture()
	if err := e.EnqueueExtract(context.Background(), req); err != nil {
		t.Fatalf("expected nil on cursor failure (contract 3 best-effort tolerance), got %v", err)
	}
	if jsvc.enqueueCalls != 1 {
		t.Errorf("Enqueue must be called (broker is source of truth), got %d calls", jsvc.enqueueCalls)
	}
	if jsvc.lastEnqueueReq == nil {
		t.Fatal("Enqueue was called but lastEnqueueReq is nil")
	}
	if csvc.updateCursorCalls != 1 {
		t.Errorf("UpdateCursor should be ATTEMPTED once even when it will fail, got %d", csvc.updateCursorCalls)
	}
	if csvc.lastCursorCommand.ID != req.Channel.ID || csvc.lastCursorCommand.Cursor != req.VideoID {
		t.Errorf("UpdateCursor command = (%q, %q), want (%q, %q)",
			csvc.lastCursorCommand.ID, csvc.lastCursorCommand.Cursor,
			req.Channel.ID, req.VideoID)
	}
}

// TestExtractionEnqueuer_EnqueueErrorSurfaces verifies that genuine
// broker-side enqueue errors (NOT best-effort) DO surface to the
// caller. The monitor's per-video error log captures them; the next
// scheduler tick retries via cursor advancement NOT having happened.
//
// This is the inverse of contract 3 — proving the best-effort
// tolerance is scoped to cursor updates only, not to broker enqueue.
// Shape pinned: enqueueCalls=1 && updateCursorCalls=0 && returnErr !=
// nil (carrying the original error wrapped).
func TestExtractionEnqueuer_EnqueueErrorSurfaces(t *testing.T) {
	underlying := errors.New("sqlite: UNIQUE constraint failed: jobs.active_key (simulated)")
	jsvc := &fakeJobsSvc{enqueueErr: underlying}
	csvc := &fakeChannelsSvc{}
	e := NewExtractionEnqueuer(jsvc, csvc, zap.NewNop())

	req := extractEnqRequestFixture()
	err := e.EnqueueExtract(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error when broker enqueue fails (NOT best-effort), got nil")
	}
	if jsvc.enqueueCalls != 1 {
		t.Errorf("Enqueue should be called (test fixture), got %d calls", jsvc.enqueueCalls)
	}
	if csvc.updateCursorCalls != 0 {
		t.Errorf("UpdateCursor MUST NOT be called when broker enqueue fails (cursor advances only on success), got %d calls",
			csvc.updateCursorCalls)
	}
	if !errors.Is(err, underlying) && err.Error() != "enqueue channel_sync job (active_key=\"channel_sync_vid-1\"): sqlite: UNIQUE constraint failed: jobs.active_key (simulated)" {
		t.Errorf("error should wrap the underlying broker error; got: %v", err)
	}
}

// TestExtractionEnqueuer_NilJobsSvcFailsLoudly verifies the
// composition-bug safety net: a nil jobsSvc is treated as a hard
// wiring failure that returns an error immediately, so the monitor's
// per-video error log captures the gap rather than silently dropping
// the durable-job emission.
//
// A nil channelsSvc (the other half of the wiring) degrades to a
// cursor-update no-op without failing the call — that's tested in
// the bonus test below.
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

// TestExtractionEnqueuer_FindActiveByKeyErrorIsConservative verifies
// the conservative degradation when FindActiveByKey itself fails
// (e.g. SQLite transient lock); the adapter surfaces a wrapped error
// rather than guessing collision-vs-non-collision. A silent fall-through
// could cause duplicate durable-job posts.
func TestExtractionEnqueuer_FindActiveByKeyErrorIsConservative(t *testing.T) {
	underlying := errors.New("sqlite: database is locked (simulated FindActiveByKey failure)")
	jsvc := &fakeJobsSvc{findActiveErr: underlying}
	csvc := &fakeChannelsSvc{}
	e := NewExtractionEnqueuer(jsvc, csvc, zap.NewNop())

	err := e.EnqueueExtract(context.Background(), extractEnqRequestFixture())
	if err == nil {
		t.Fatal("expected error when FindActiveByKey fails (conservative), got nil — would silently double-post")
	}
	if jsvc.enqueueCalls != 0 {
		t.Errorf("Enqueue must NOT be called when collision check is broken, got %d calls", jsvc.enqueueCalls)
	}
	if csvc.updateCursorCalls != 0 {
		t.Errorf("UpdateCursor must NOT be called when collision check is broken, got %d calls", csvc.updateCursorCalls)
	}
	// Sanity check the error wording (helps grep-ability in logs).
	if want := `"channel_sync_vid-1"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error message should embed the active_key %q, got: %v", want, err)
	}
}
