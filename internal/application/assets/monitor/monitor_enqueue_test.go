package monitor

import (
	"context"
	"errors"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"go.uber.org/zap"
)

// monitor_enqueue_test.go — Step 9 (June 2026) durable-emission tests.
//
// enqueueFromAnalysis (enqueue.go) is the post-AI-gate dispatch to the
// JobEnqueuer port. The 3 cases below pin the canonical contract:
//
//   1. ActiveKey collision → NO-OP: an enqueue for a VideoID already
//      in flight silently returns nil without recording or cursor-updating
//      (durable-jobs semantics: duplicate enqueues from the channel
//      monitor's per-tick retry window must not double-post jobs).
//
//   2. Cursor update on success: when EnqueueExtract records the enqueue
//      AND attempts the cursor update, the monitor receives a populated
//      EnqueueExtractRequest with all fields echoed correctly
//      (VideoID, Title, URL, Group, DriveFolderID, Segments, Channel).
//
//   3. Cursor update failure tolerance: when the cursor update fails
//      internally but EnqueueExtract returns nil (best-effort), the
//      monitor MUST NOT propagate the cursor error. The contract is:
//      the broker-side enqueue is the source of truth; cursor updates
//      are an operator-observability convenience, not a correctness gate.
//
// The stub JobEnqueuer (in monitor_scheduler_test.go) simulates the
// ActiveKey collision short-circuit + cursor-update phase that the
// concrete *jobtools.Service binding will own in production.

func enqueueChannelFixture() channels.Channel {
	return channels.Channel{
		ID:            "ch-1",
		ChannelURL:    "https://www.youtube.com/@Test",
		Category:      "test-cat",
		DriveFolderID: "test-folder",
	}
}

func enqueueVideoFixture() downloader.VideoInfo {
	return downloader.VideoInfo{ID: "vid-1", Title: "Test Title"}
}

func enqueueAnalysisFixture() Analysis {
	return Analysis{
		Score:    85,
		Category: "test-cat",
		Segments: []ytdomain.Segment{
			{Start: "00:01", End: "00:30", Name: "Segment One"},
			{Start: "00:30", End: "01:00", Name: "Segment Two"},
		},
	}
}

// TestEnqueueFromAnalysis_ActiveKeyCollisionNoOp verifies that a
// configured collision (JobEnqueuer knows this VideoID is already in
// flight from a prior tick) is a hard NO-OP: EnqueueExtract IS still
// called (the canonical no-op path lives inside the port), but nothing
// is recorded and the cursor is NOT advanced.
//
// Why this matters: the channel monitor's per-tick ClaimDue lease can
// re-emit a video multiple times across exp-backoff retries. Without
// the collision short-circuit, every retry would re-post the durable
// job → broker fan-out blow-up under transient failure storms.
func TestEnqueueFromAnalysis_ActiveKeyCollisionNoOp(t *testing.T) {
	stub := &stubJobEnqueuer{
		collisions: map[string]bool{"vid-collision": true},
	}
	m := &ChannelMonitor{
		log:     zap.NewNop(),
		enqueuer: stub,
	}
	ch := enqueueChannelFixture()
	info := downloader.VideoInfo{ID: "vid-collision", Title: "Title"}
	analysis := enqueueAnalysisFixture()

	m.enqueueFromAnalysis(context.Background(), info, ch, analysis)

	// EnqueueExtract IS called once (the stub itself decides whether
	// to record or no-op); the CANONICAL behavior of the stub is
	// "EnqueueExtract returns nil, no recording, no cursor update".
	if stub.enqueueCalls != 1 {
		t.Errorf("EnqueueExtract should be invoked exactly once on collision, got %d", stub.enqueueCalls)
	}
	if len(stub.enqueuedRequests) != 0 {
		t.Errorf("enqueuedRequests should be EMPTY on collision (no-op), got %d entries", len(stub.enqueuedRequests))
	}
	if stub.cursorUpdates != 0 {
		t.Errorf("cursorUpdates should be 0 on collision (no-op path skips cursor), got %d", stub.cursorUpdates)
	}
}

// TestEnqueueFromAnalysis_CursorUpdateOnSuccess verifies that on the
// happy path (no collision, EnqueueExtract succeeds), the monitor
// passes through a fully-populated EnqueueExtractRequest to the port:
//
//   VideoID = info.ID
//   Title = info.Title
//   URL = "https://www.youtube.com/watch?v=<VideoID>"
//   Group = analysis.Category
//   DriveFolderID = channel.DriveFolderID (or cfg.Drive.ClipsFolder() fallback)
//   Segments = analysis.Segments
//   Channel = channel (back-reference for downstream port impls)
//
// And the cursor is updated exactly once.
func TestEnqueueFromAnalysis_CursorUpdateOnSuccess(t *testing.T) {
	stub := &stubJobEnqueuer{
		returnErr: nil, // best-effort happy path
	}
	m := &ChannelMonitor{
		log:     zap.NewNop(),
		enqueuer: stub,
	}
	ch := enqueueChannelFixture()
	info := enqueueVideoFixture()
	analysis := enqueueAnalysisFixture()

	m.enqueueFromAnalysis(context.Background(), info, ch, analysis)

	if stub.enqueueCalls != 1 {
		t.Fatalf("EnqueueExtract should be invoked exactly once, got %d", stub.enqueueCalls)
	}
	if stub.cursorUpdates != 1 {
		t.Errorf("cursorUpdates should be 1 on success, got %d", stub.cursorUpdates)
	}
	if len(stub.enqueuedRequests) != 1 {
		t.Fatalf("enqueuedRequests should have 1 entry, got %d", len(stub.enqueuedRequests))
	}
	got := stub.enqueuedRequests[0]
	if got.VideoID != info.ID {
		t.Errorf("VideoID = %q, want %q", got.VideoID, info.ID)
	}
	if got.Title != info.Title {
		t.Errorf("Title = %q, want %q", got.Title, info.Title)
	}
	if want := "https://www.youtube.com/watch?v=" + info.ID; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
	if got.Group != analysis.Category {
		t.Errorf("Group = %q, want %q (analysis.Category)", got.Group, analysis.Category)
	}
	if got.DriveFolderID != ch.DriveFolderID {
		t.Errorf("DriveFolderID = %q, want %q (channel.DriveFolderID precedence)", got.DriveFolderID, ch.DriveFolderID)
	}
	if len(got.Segments) != len(analysis.Segments) {
		t.Errorf("Segments len = %d, want %d", len(got.Segments), len(analysis.Segments))
	}
	if got.Channel.ID != ch.ID {
		t.Errorf("Channel back-reference lost: Channel.ID = %q, want %q", got.Channel.ID, ch.ID)
	}
}

// TestEnqueueFromAnalysis_CursorUpdateFailureTolerance verifies that
// when the cursor update FAILS but the broker-side enqueue succeeds,
// the monitor does NOT surface the cursor error.
//
// The contract (per enqueue.go header comment): "Errors from the
// JobEnqueuer port are logged and swallowed: the channel-monitor's
// contract is best-effort per video, with retry driven by the next
// scheduler tick." A cursor-update failure must therefore not poison
// the per-video success state.
//
// The stub is configured so EnqueueExtract returns nil even though
// the cursor attempt failed internally — this is the production
// semantic the concrete *jobtools.Service binding must implement.
// The monitor sees nil, treats it as success, and the orchestrator's
// next scheduler tick resumes naturally.
func TestEnqueueFromAnalysis_CursorUpdateFailureTolerance(t *testing.T) {
	stub := &stubJobEnqueuer{
		returnErr: nil,                                                       // best-effort: monitor sees no error
		cursorErr: errors.New("sqlite: database is locked (test simulation)"), // simulated cursor failure
	}
	m := &ChannelMonitor{
		log:     zap.NewNop(),
		enqueuer: stub,
	}
	ch := enqueueChannelFixture()
	info := downloader.VideoInfo{ID: "vid-cursor-fail", Title: "Title"}
	analysis := enqueueAnalysisFixture()

	// The monitor must NOT panic, NOT log error, NOT return any
	// outside-facing error indication — best-effort semantics.
	m.enqueueFromAnalysis(context.Background(), info, ch, analysis)

	if stub.enqueueCalls != 1 {
		t.Errorf("EnqueueExtract should be invoked exactly once, got %d", stub.enqueueCalls)
	}
	if len(stub.enqueuedRequests) != 1 {
		t.Errorf("enqueuedRequests should have 1 entry (enqueue succeeded despite cursor failure), got %d", len(stub.enqueuedRequests))
	}
	if stub.cursorUpdates != 1 {
		t.Errorf("cursorUpdates should be 1 (cursor was ATTEMPTED despite simulated failure), got %d", stub.cursorUpdates)
	}
	// The consistent contract: the EnqueueExtractRequest IS queued
	// for the broker (stub recorded it), even though the cursor
	// attempt failed. The monitor's contract is per-video best-effort,
	// not strong consistency — the next scheduler tick will retry.
	if got := stub.enqueuedRequests[0].VideoID; got != "vid-cursor-fail" {
		t.Errorf("recorded request VideoID = %q, want vid-cursor-fail", got)
	}
}
