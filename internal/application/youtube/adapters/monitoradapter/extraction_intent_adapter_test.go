// Package monitoradapter — extraction_intent_adapter_test.go: pinned
// contract tests for the canonical concrete JobEnqueuer binding
// (extraction_intent_adapter.go). 5 tests moved verbatim (with
// input/output surface adaptation) from
// internal/application/assets/monitor/extraction_enqueuer_test.go
// where they covered the pre-Fase-8 ExtractionEnqueuer.
//
// Phase 8 (July 2026, Spina Dorsale — monitoradapter consolidation):
// the marshal concern `monitor.EnqueueExtractRequest →
// youtubetypes.ExtractRequest` (extraction_enqueuer.go line ~168
// per the pre-Fase-8 source) moves into the youtube domain as part
// of `monitoradapter`, so the monitor package no longer imports
// youtubetypes. The contract tests follow the marshal site:
//
//   - Input shape: monitor.ExtractionIntent (monitor-package DTO).
//     monitor.ExtractionSegment is a type alias for ytdomain.Segment
//     (godlike/06 SSOT), so the input fixture drops the explicit
//     ytdomain import in favour of the canonical monitor-owned alias.
//   - Output shape: youtubetypes.ExtractRequest (the marshaled
//     payload the broker hands to the youtube_clip.extract job
//     handler). The marshal correctness is verified by asserting on
//     the captured EnqueueRequest.Payload.

package monitoradapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	monitor "github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

// ── Compile-time assertion (Test-side mirror of production pin) ─────────────
//
// extraction_intent_adapter.go already carries `var _ monitor.JobEnqueuer =
// (*ExtractionIntentAdapter)(nil)` at line ~265 (production-side pin). The
// duplicate here in the test file is intentional: the test-side pin
// establishes the contract from the test-consumer's perspective. A future
// refactor that drifts monitor.JobEnqueuer's signature AND accidentally
// re-imports youtubetypes into monitor will fail to build this test file
// before it ever reaches production (matches the
// internal/application/assets/monitor/monitor_scheduler_test.go:201 pattern
// which pins stubJobEnqueuer's monitor.JobEnqueuer conformance from the
// test side).
var _ monitor.JobEnqueuer = (*ExtractionIntentAdapter)(nil)

// ── Test fakes (port interfaces JobsEnqueuerSvc + ChannelsCursorSvc) ────────

// fakeJobsSvc is a minimal JobsEnqueuerSvc stub. Captures the
// Enqueue-recording surfaces the test cases assert on, and lets the
// collision pre-check be driven via findActive.
type fakeJobsSvc struct {
	findActiveResp   *job.Job
	findActiveErr    error
	findActiveCalls  int
	findActiveKeyArg string

	enqueueErr     error
	enqueueCalls   int
	lastEnqueueReq *job.EnqueueRequest
}

func (f *fakeJobsSvc) FindActiveByKey(_ context.Context, activeKey string) (*job.Job, error) {
	f.findActiveCalls++
	f.findActiveKeyArg = activeKey
	return f.findActiveResp, f.findActiveErr
}

func (f *fakeJobsSvc) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	f.enqueueCalls++
	f.lastEnqueueReq = req
	if f.enqueueErr != nil {
		return nil, f.enqueueErr
	}
	return &job.Job{ID: "job-" + req.ActiveKey, Status: job.StatusQueued}, nil
}

// fakeChannelsSvc is a minimal ChannelsCursorSvc stub. Records every
// UpdateCursor invocation so tests can assert the exact (id, cursor)
// tuple sent. Post-Commit-D the adapter does NOT call UpdateCursor
// per-video (cycle-end MAX(discovered_at) is the new path); the stub
// exists for the CursorUpdateFailureTolerance contract test + the
// pre-Commit-D documented assertion shape.
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

// ── Test fixtures (input / monitor-side DTO) ──────────────────────────────

func extractIntentChannelsFixture() channels.Channel {
	return channels.Channel{
		ID:            "ch-1",
		ChannelURL:    "https://www.youtube.com/@Test",
		Category:      "test-cat",
		DriveFolderID: "fld-1",
	}
}

// extractIntentFixture builds the canonical monitor.ExtractionIntent input
// (the user-facing DTO type post-Fase 8). Segments use monitor.ExtractionSegment
// (alias for youtubetypes.Segment per godlike/06 SSOT) — the ytdomain import
// is dropped in favour of the monitor-owned alias.
func extractIntentFixture() monitor.ExtractionIntent {
	return monitor.ExtractionIntent{
		VideoID:       "vid-1",
		Title:         "Test Title",
		URL:           "https://www.youtube.com/watch?v=vid-1",
		Group:         "test-cat",
		DriveFolderID: "fld-1",
		Segments: []monitor.ExtractionSegment{
			{Start: "00:01", End: "00:30", Name: "Segment One"},
		},
		Channel: extractIntentChannelsFixture(),
	}
}

// ── Contract tests (5 pinned, moved from monitor/extraction_enqueuer_test.go) ──

// TestExtractionIntentAdapter_CollisionNoOp verifies contract 1: when a
// non-terminal job already exists for ActiveKey="channel_sync_<VideoID>",
// the adapter returns nil WITHOUT calling Enqueue or UpdateCursor. The
// broker-recorded enqueue is unchanged; the channel cursor stays where
// it was; the monitor sees no error.
//
// Shape pinned: enqueueCalls=0 && updateCursorCalls=0 && returnErr==nil.
// The pre-check is the load-bearing piece — without it, a duplicate
// enqueue would post a second durable job + advance the cursor past a
// video we didn't actually process in this tick.
func TestExtractionIntentAdapter_CollisionNoOp(t *testing.T) {
	jsvc := &fakeJobsSvc{
		findActiveResp: &job.Job{
			ID:     "job-existing",
			Status: job.StatusQueued,
		},
	}
	csvc := &fakeChannelsSvc{}
	a := NewExtractionIntentAdapter(jsvc, csvc, zap.NewNop())

	intent := extractIntentFixture()
	err := a.EnqueueExtract(context.Background(), intent)
	if err != nil {
		t.Fatalf("expected nil on collision, got %v", err)
	}
	if jsvc.findActiveCalls != 1 {
		t.Errorf("FindActiveByKey should be called exactly once, got %d", jsvc.findActiveCalls)
	}
	if jsvc.findActiveKeyArg != monitor.ActiveKeyPrefix+intent.VideoID {
		t.Errorf("FindActiveByKey activeKey arg = %q, want %q",
			jsvc.findActiveKeyArg, monitor.ActiveKeyPrefix+intent.VideoID)
	}
	if jsvc.enqueueCalls != 0 {
		t.Errorf("Enqueue must NOT be called on collision, got %d calls", jsvc.enqueueCalls)
	}
	if csvc.updateCursorCalls != 0 {
		t.Errorf("UpdateCursor must NOT be called on collision, got %d calls", csvc.updateCursorCalls)
	}
}

// TestExtractionIntentAdapter_HappyPath_TranslationLocksAndCursorOmits
// verifies contract 2 (cursor on success) + locks the canonical
// ActiveKey prefix + ExtractRequest PAYLOAD SHAPE produced by the
// adapter's marshal.
//
// Shape pinned:
//   - FindActiveByKey called once with ActiveKey="channel_sync_vid-1"
//   - Enqueue called once with type="youtube_clip.extract", ActiveKey
//     matches, VideoName == intent.Title
//   - Captured Payload is youtubetypes.ExtractRequest VALUE (NOT
//     pointer and NOT map[string]any) — the job handler unmarshals
//     into a value form (see
//     internal/application/youtube/jobs/job_handler.go).
//   - Payload.URL matches intent.URL
//   - Payload.Segments len + element field-level preservation
//     (translateSegments field-by-field copy invariant)
//   - Payload.Destination.Group == intent.Group
//   - Payload.Destination.FolderID == intent.DriveFolderID
//   - UpdateCursor called 0 times (Commit D removed per-video cursor
//     update; cycle-end MAX(discovered_at) is the new path)
//   - returnErr == nil
func TestExtractionIntentAdapter_HappyPath_TranslationLocksAndCursorOmits(t *testing.T) {
	jsvc := &fakeJobsSvc{} // no collision
	csvc := &fakeChannelsSvc{}
	a := NewExtractionIntentAdapter(jsvc, csvc, zap.NewNop())

	intent := extractIntentFixture()
	if err := a.EnqueueExtract(context.Background(), intent); err != nil {
		t.Fatalf("expected nil on success, got %v", err)
	}
	if jsvc.findActiveCalls != 1 {
		t.Errorf("FindActiveByKey should be called exactly once, got %d", jsvc.findActiveCalls)
	}
	if want := monitor.ActiveKeyPrefix + intent.VideoID; jsvc.findActiveKeyArg != want {
		t.Errorf("FindActiveByKey activeKey = %q, want %q", jsvc.findActiveKeyArg, want)
	}
	if jsvc.enqueueCalls != 1 {
		t.Fatalf("Enqueue should be called exactly once, got %d", jsvc.enqueueCalls)
	}
	if jsvc.lastEnqueueReq == nil {
		t.Fatal("Enqueue was called but lastEnqueueReq is nil (test fixture bug)")
	}
	if jsvc.lastEnqueueReq.Type != job.TypeYouTubeClipExtract {
		t.Errorf("Enqueue Type = %q, want %q",
			jsvc.lastEnqueueReq.Type, job.TypeYouTubeClipExtract)
	}
	if want := monitor.ActiveKeyPrefix + intent.VideoID; jsvc.lastEnqueueReq.ActiveKey != want {
		t.Errorf("Enqueue ActiveKey = %q, want %q", jsvc.lastEnqueueReq.ActiveKey, want)
	}
	if jsvc.lastEnqueueReq.VideoName != intent.Title {
		t.Errorf("Enqueue VideoName = %q, want %q (intent.Title)",
			jsvc.lastEnqueueReq.VideoName, intent.Title)
	}

	// OUTPUT ASSERTION: the adapter's marshal target. youtubetypes
	// alias points to the same package as the pre-Fase-8 ytdomain
	// alias (internal/application/youtube/dto) — same type, drop-in.
	payload, ok := jsvc.lastEnqueueReq.Payload.(youtubetypes.ExtractRequest)
	if !ok {
		t.Fatalf("Enqueue Payload type = %T, want youtubetypes.ExtractRequest value (job handler unmarshals this exact shape)",
			jsvc.lastEnqueueReq.Payload)
	}
	if payload.URL != intent.URL {
		t.Errorf("Payload.URL = %q, want %q", payload.URL, intent.URL)
	}
	if len(payload.Segments) != len(intent.Segments) {
		t.Errorf("Payload.Segments len = %d, want %d", len(payload.Segments), len(intent.Segments))
	}
	// translateSegments invariant: field-by-field copy preserves
	// every readable field on the segment (alias identity makes
	// this trivially true today; the assert pins a future drift
	// where ExtractionSegment and youtubetypes.Segment diverge).
	for i, seg := range intent.Segments {
		if payload.Segments[i].Start != seg.Start {
			t.Errorf("Payload.Segments[%d].Start = %q, want %q",
				i, payload.Segments[i].Start, seg.Start)
		}
		if payload.Segments[i].End != seg.End {
			t.Errorf("Payload.Segments[%d].End = %q, want %q",
				i, payload.Segments[i].End, seg.End)
		}
		if payload.Segments[i].Name != seg.Name {
			t.Errorf("Payload.Segments[%d].Name = %q, want %q",
				i, payload.Segments[i].Name, seg.Name)
		}
	}
	if payload.Destination == nil {
		t.Fatal("Payload.Destination is nil — adapter must wrap Group+FolderID into the Destination struct")
	}
	if payload.Destination.Group != intent.Group {
		t.Errorf("Payload.Destination.Group = %q, want %q", payload.Destination.Group, intent.Group)
	}
	if payload.Destination.FolderID != intent.DriveFolderID {
		t.Errorf("Payload.Destination.FolderID = %q, want %q",
			payload.Destination.FolderID, intent.DriveFolderID)
	}

	// Commit D (PR-D YouTube Channel Monitor cutover, June 2026):
	// per-video UpdateCursor REMOVED. Cycle-end
	// MAX(discovered_at) → category_channels.last_cursor replaced it
	// (see monitor/discovery.go::recordCycleEndWatermark).
	if csvc.updateCursorCalls != 0 {
		t.Errorf("UpdateCursor MUST NOT be called post-Commit-D (cycle-end watermark replaced it), got %d calls", csvc.updateCursorCalls)
	}
}

// TestExtractionIntentAdapter_CursorUpdateFailureTolerance verifies
// contract 3: when channels.UpdateCursor fails (e.g. SQLite transient
// lock, network blip), the adapter SIGHS + logs warn but returns nil.
// The durable-jobs broker-recorded enqueue IS the source of truth;
// cursor updates are an observability convenience, not a correctness
// gate.
//
// Committee D replumbed the per-video cursor path: UpdateCursor is
// NEVER called (cycle-end MAX(discovered_at) is the new path). The
// contract now reduces to: cursor side-effect is always omitted; the
// fakeChannelsSvc.updateCursorErr is set to verify the field is even
// wired through but never reached. Test still locks the broker
// primary-source-of-truth semantic (broker enqueue MUST land even
// when the cursor field is misconfigured).
//
// Shape pinned: enqueueCalls=1 && updateCursorCalls=0 && returnErr==nil.
func TestExtractionIntentAdapter_CursorUpdateFailureTolerance(t *testing.T) {
	jsvc := &fakeJobsSvc{}
	csvc := &fakeChannelsSvc{
		updateCursorErr: errors.New("sqlite: database is locked (test simulation)"),
	}
	a := NewExtractionIntentAdapter(jsvc, csvc, zap.NewNop())

	intent := extractIntentFixture()
	if err := a.EnqueueExtract(context.Background(), intent); err != nil {
		t.Fatalf("expected nil on cursor failure (contract 3 best-effort tolerance), got %v", err)
	}
	if jsvc.enqueueCalls != 1 {
		t.Errorf("Enqueue must be called (broker is source of truth), got %d calls", jsvc.enqueueCalls)
	}
	if jsvc.lastEnqueueReq == nil {
		t.Fatal("Enqueue was called but lastEnqueueReq is nil")
	}
	// Post-Commit-D: per-video UpdateCursor is REMOVED. The cursor
	// fields on the adapter struct are retained for back-compat
	// with the legacy pre-Commit-D composition wiring; the runtime
	// no longer exercises them on the happy path. The fake's
	// updateCursorCalls MUST remain 0 even though updateCursorErr is
	// wired (proves the omitempty is structural, not conditional).
	if csvc.updateCursorCalls != 0 {
		t.Errorf("UpdateCursor MUST NOT be called post-Commit-D (replaced by cycle-end watermark), got %d calls", csvc.updateCursorCalls)
	}
}

// TestExtractionIntentAdapter_EnqueueErrorSurfaces verifies that
// genuine broker-side enqueue errors (NOT best-effort) DO surface to
// the caller. The monitor's per-video error log captures them; the next
// scheduler tick retries via cursor advancement NOT having happened.
//
// This is the inverse of contract 3 — proving the best-effort
// tolerance is scoped to (now-obsolete) cursor updates only, not to
// broker enqueue.
//
// Shape pinned: enqueueCalls=1 && updateCursorCalls=0 && returnErr !=
// nil (carrying the original error wrapped).
func TestExtractionIntentAdapter_EnqueueErrorSurfaces(t *testing.T) {
	underlying := errors.New("sqlite: UNIQUE constraint failed: jobs.active_key (simulated)")
	jsvc := &fakeJobsSvc{enqueueErr: underlying}
	csvc := &fakeChannelsSvc{}
	a := NewExtractionIntentAdapter(jsvc, csvc, zap.NewNop())

	intent := extractIntentFixture()
	err := a.EnqueueExtract(context.Background(), intent)
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

// TestExtractionIntentAdapter_FindActiveByKeyErrorIsConservative
// verifies the conservative degradation when the ActiveKey collision
// pre-check itself fails (e.g. SQLite transient lock on jobs.Active
// rows); the adapter surfaces a wrapped error rather than guessing
// collision-vs-non-collision. A silent fall-through could cause
// duplicate durable-job posts.
//
// Note on naming: the test exercises the *pre-check failure* on the
// DERIVED ActiveKey, NOT derivation failure itself —
// monitor.ActiveKeyPrefix + intent.VideoID is an unconditional string
// concat (adapter line ~142) that cannot fail. The "ActiveKey
// derivation" contract the user spec references is honored through
// the FindActiveByKey arg assertion in tests 1 + 2 above; this test
// locks the conservative error path on that derived key.
//
// Shape pinned: enqueueCalls=0 && updateCursorCalls=0 && returnErr !=
// nil carrying the wrapped underlying + the canonical active_key
// substring for log-grep anchor integrity.
func TestExtractionIntentAdapter_FindActiveByKeyErrorIsConservative(t *testing.T) {
	underlying := errors.New("sqlite: database is locked (simulated ActiveKey pre-check failure)")
	jsvc := &fakeJobsSvc{findActiveErr: underlying}
	csvc := &fakeChannelsSvc{}
	a := NewExtractionIntentAdapter(jsvc, csvc, zap.NewNop())

	err := a.EnqueueExtract(context.Background(), extractIntentFixture())
	if err == nil {
		t.Fatal("expected error when ActiveKey pre-check fails (conservative), got nil — would silently double-post")
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
