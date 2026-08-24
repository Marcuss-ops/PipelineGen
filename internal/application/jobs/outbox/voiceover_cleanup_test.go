// Package outbox — voiceover_cleanup_test.go
// (FASE 4 — VO-OPERATIONAL-READINESS, July 2026)
//
// Hermetic regression tests pinning the VoiceoverCleanupHandler contract:
//
//   - Happy path: valid v1 envelope → Drive.DeleteFile called → success.
//     Simulates the orphan-cleanup case where Drive upload succeeded but
//     the DB finalize tx was rolled back, leaving the cleanup event in
//     the outbox to asynchronously delete the orphan Drive file.
//
//   - Retryable transient: Drive.DeleteFile returns non-404 error →
//     handler returns a NON-terminal error so the outbox pool applies
//     jittered exponential backoff (retryable).
//
//   - 404 idempotent: Drive.DeleteFile returns 404 → handler folds to
//     success (the orphan file was already cleaned by a prior retry).
//
//   - Terminal errors: schema mismatch, missing event_id, missing
//     voiceover_id, missing idempotency_key → outboxevents.NewTerminalError
//     so the pool dead-letters immediately (retry cannot conjure the
//     missing field).
//
//   - old==new gate: old_drive_file_id == new_drive_file_id → no
//     Drive.DeleteFile call, success (swap landed on same file, no orphan).
//
//   - nil driver: driver not wired → Warn, skip Drive delete, success
//     (composition root misconfig; operator dashboards surface via log).
//
// godlike/07 NO-FAKE-AVAILABILITY: every test exercises the production
// handler's error-classification logic, NOT a stub re-implementation.
// The VoiceoverCleanupHandler is the SOLE canonical owner of the
// cleanup decision surface.
package outbox

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ── Mock surfaces ─────────────────────────────────────────────────────

// mockCleanupDriver records DeleteFile calls and returns a pre-programmed
// error. Does NOT pre-fold 404 → success — the handler's own 404 fold
// logic is the unit under test.
type mockCleanupDriver struct {
	deleteCalls []string
	deleteErr   error
}

func (m *mockCleanupDriver) DeleteFile(_ context.Context, fileID string) error {
	m.deleteCalls = append(m.deleteCalls, fileID)
	return m.deleteErr
}

// ── Helpers ──────────────────────────────────────────────────────────

// buildVoiceoverCleanupEvent builds a canonical outboxevents.Event with
// the given voiceover_cleanup_requested_v1 envelope fields.
func buildVoiceoverCleanupEvent(t *testing.T, fields map[string]any) outboxevents.Event {
	t.Helper()

	// Default a valid v1 envelope so each test only overrides the
	// fields it cares about.
	defaults := map[string]any{
		"schema_version":    VoiceoverCleanupSchemaVersion,
		"event_id":          "evt-test-0001",
		"voiceover_id":      "vo-test-0001",
		"old_drive_file_id": "drive-old-abc123",
		"new_drive_file_id": "drive-new-xyz789",
		"idempotency_key":   "voiceover_cleanup:vo-test-0001:drive-new-xyz789",
	}
	for k, v := range fields {
		defaults[k] = v
	}

	payload, err := json.Marshal(defaults)
	if err != nil {
		t.Fatalf("marshal cleanup fixture: %v", err)
	}

	return outboxevents.Event{
		ID:            1,
		PayloadJSON:   string(payload),
		AggregateID:   "vo-test-0001",
		AggregateType: "voiceover",
		EventType:     outboxevents.EventVoiceoverCleanupRequested,
		AttemptCount:  0,
		EventKey:      "voiceover_cleanup:vo-test-0001:drive-new-xyz789",
	}
}

// ── Test 1: Happy path — valid event, Drive.DeleteFile succeeds ──────

func TestVoiceoverCleanupHandler_HappyPath_DeletesOrphanDriveFile(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, nil)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("happy-path: Handle returned %v", err)
	}

	if len(driver.deleteCalls) != 1 {
		t.Fatalf("expected 1 DeleteFile call, got %d", len(driver.deleteCalls))
	}
	if driver.deleteCalls[0] != "drive-old-abc123" {
		t.Fatalf("DeleteFile called with %q, want %q", driver.deleteCalls[0], "drive-old-abc123")
	}
}

// ── Test 2: Transient Drive failure → retryable (not terminal) ───────

func TestVoiceoverCleanupHandler_DriveTransientFailure_Retryable(t *testing.T) {
	driver := &mockCleanupDriver{
		deleteErr: &googleapi.Error{
			Code:    http.StatusServiceUnavailable,
			Message: "Service Unavailable",
		},
	}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, nil)

	err := h.Handle(context.Background(), evt)
	if err == nil {
		t.Fatalf("transient Drive failure must return a non-nil error")
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("transient Drive error must NOT be classified as terminal (outbox pool would dead-letter): %v", err)
	}
	if len(driver.deleteCalls) != 1 {
		t.Fatalf("expected 1 DeleteFile call on transient failure, got %d", len(driver.deleteCalls))
	}
}

// ── Test 3: 404 idempotent → folded to success ──────────────────────

func TestVoiceoverCleanupHandler_Drive404_FoldedToSuccess(t *testing.T) {
	driver := &mockCleanupDriver{
		deleteErr: &googleapi.Error{
			Code:    http.StatusNotFound,
			Message: "File not found: drive-old-abc123",
		},
	}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, nil)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("404 tolerance: Handle returned %v — must fold to success", err)
	}
	if len(driver.deleteCalls) != 1 {
		t.Fatalf("expected 1 DeleteFile call (404 folded AFTER the call returns), got %d", len(driver.deleteCalls))
	}
}

// ── Test 4: old==new gate — no orphan, skip Drive delete ────────────

func TestVoiceoverCleanupHandler_OldEqualsNew_SkipsDriveDelete(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"old_drive_file_id": "drive-same-111",
		"new_drive_file_id": "drive-same-111",
	})

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("old==new gate: Handle returned %v", err)
	}
	if len(driver.deleteCalls) != 0 {
		t.Fatalf("expected 0 DeleteFile calls for old==new, got %d", len(driver.deleteCalls))
	}
}

// ── Test 5: Schema mismatch → terminal (dead-letter) ─────────────────

func TestVoiceoverCleanupHandler_SchemaMismatch_Terminal(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"schema_version": "voiceover.cleanup.requested.BOGUS",
	})

	err := h.Handle(context.Background(), evt)
	if err == nil {
		t.Fatal("schema mismatch must return a non-nil error")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("schema mismatch must be terminal (so pool dead-letters): %v", err)
	}
	if len(driver.deleteCalls) != 0 {
		t.Fatalf("terminal error must skip ALL side effects; got %d DeleteFile calls", len(driver.deleteCalls))
	}
}

// ── Test 6: Missing event_id → terminal ──────────────────────────────

func TestVoiceoverCleanupHandler_MissingEventID_Terminal(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"event_id": "",
	})

	err := h.Handle(context.Background(), evt)
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("missing event_id must be terminal: %v", err)
	}
}

// ── Test 7: Missing voiceover_id → terminal ─────────────────────────

func TestVoiceoverCleanupHandler_MissingVoiceoverID_Terminal(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"voiceover_id": "",
	})

	err := h.Handle(context.Background(), evt)
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("missing voiceover_id must be terminal: %v", err)
	}
}

// ── Test 8: Missing idempotency_key → terminal ──────────────────────

func TestVoiceoverCleanupHandler_MissingIdempotencyKey_Terminal(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"idempotency_key": "",
	})

	err := h.Handle(context.Background(), evt)
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("missing idempotency_key must be terminal: %v", err)
	}
}

// ── Test 9: Nil driver → skip Drive delete, success ──────────────────

func TestVoiceoverCleanupHandler_NilDriver_SkipsDriveDelete_Succeeds(t *testing.T) {
	h := NewVoiceoverCleanupHandler(nil, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, nil)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("nil-driver: Handle returned %v — must skip Drive but succeed", err)
	}
}

// ── Test 10: Both old and new empty → no-op success ─────────────────

func TestVoiceoverCleanupHandler_BothDriveIDsEmpty_Succeeds(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"old_drive_file_id": "",
		"new_drive_file_id": "",
	})

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("both-empty: Handle returned %v", err)
	}
	if len(driver.deleteCalls) != 0 {
		t.Fatalf("expected 0 DeleteFile calls for both-empty, got %d", len(driver.deleteCalls))
	}
}

// ── Test 11: Parse failure → terminal ───────────────────────────────

func TestVoiceoverCleanupHandler_ParseFailure_Terminal(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := outboxevents.Event{
		ID:          1,
		PayloadJSON: `{this is not json}`,
		EventType:   outboxevents.EventVoiceoverCleanupRequested,
	}

	err := h.Handle(context.Background(), evt)
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("parse failure must be terminal: %v", err)
	}
	if len(driver.deleteCalls) != 0 {
		t.Fatalf("parse failure must skip ALL side effects")
	}
}

// ── Test 12: Outbox retry classification — retryable vs terminal ────
// Verifies that the outbox pool's retry logic correctly classifies
// VoiceoverCleanupHandler errors:
//   - Terminal errors (NewTerminalError) → pool dead-letters (no retry)
//   - Non-terminal errors (Drive 503) → pool retries with backoff
//   - Success (nil) → pool marks completed

func TestVoiceoverCleanupHandler_OutboxRetryClassification(t *testing.T) {
	// Verify that the handler's terminal errors ARE wrapped with
	// outboxevents.NewTerminalError so the pool's IsTerminal probe
	// (processEvent in pool.go) correctly dead-letters them.
	//
	// The pool's classification logic:
	//   if outboxevents.IsTerminal(err) → MarkDeadLetter
	//   else → MarkFailed + computeNextAttempt (retry)

	// Case A: Schema mismatch → terminal.
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"schema_version": "voiceover.cleanup.requested.BOGUS",
	})
	errA := h.Handle(context.Background(), evt)
	if !outboxevents.IsTerminal(errA) {
		t.Fatalf("schema mismatch: outboxevents.IsTerminal must return true → pool dead-letters")
	}

	// Case B: Drive 503 → NOT terminal.
	driver503 := &mockCleanupDriver{
		deleteErr: &googleapi.Error{Code: http.StatusServiceUnavailable, Message: "503"},
	}
	h503 := NewVoiceoverCleanupHandler(driver503, zap.NewNop())
	evt503 := buildVoiceoverCleanupEvent(t, nil)
	errB := h503.Handle(context.Background(), evt503)
	if outboxevents.IsTerminal(errB) {
		t.Fatalf("Drive 503: outboxevents.IsTerminal must return false → pool retries")
	}

	// Case C: Success → nil error → pool marks completed.
	driverOk := &mockCleanupDriver{}
	hOk := NewVoiceoverCleanupHandler(driverOk, zap.NewNop())
	evtOk := buildVoiceoverCleanupEvent(t, nil)
	errC := hOk.Handle(context.Background(), evtOk)
	if errC != nil {
		t.Fatalf("success path: Handle returned %v — want nil so pool marks completed", errC)
	}
}

// ── Test 13: EventType returns the canonical constant ────────────────

func TestVoiceoverCleanupHandler_EventType_ParityWithRegistry(t *testing.T) {
	h := NewVoiceoverCleanupHandler(nil, zap.NewNop())
	if h.EventType() != outboxevents.EventVoiceoverCleanupRequested {
		t.Fatalf("EventType=%s != outboxevents.EventVoiceoverCleanupRequested=%s",
			h.EventType(), outboxevents.EventVoiceoverCleanupRequested)
	}
}

// ── Test 14: Local file cleanup — old_local_paths present ────────────
// The handler removes each path via os.Remove; os.IsNotExist is silently
// swallowed for idempotency. Since the test sandbox has no real audio
// files, all paths will trigger ErrNotExist → handler succeeds anyway.
// This test verifies the local-cleanup branch is reachable, doesn't crash,
// and doesn't return an error when files are already gone.

func TestVoiceoverCleanupHandler_LocalFileCleanup_PathsAlreadyGone(t *testing.T) {
	driver := &mockCleanupDriver{}
	h := NewVoiceoverCleanupHandler(driver, zap.NewNop())
	evt := buildVoiceoverCleanupEvent(t, map[string]any{
		"old_drive_file_id": "drive-old-abc123",
		"new_drive_file_id": "drive-new-xyz789",
		"old_local_paths":   []string{"/tmp/vo/dead-audio-1.mp3", "/tmp/vo/dead-audio-2.wav"},
	})

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("local-cleanup: Handle returned %v — os.IsNotExist should be swallowed", err)
	}
	if len(driver.deleteCalls) != 1 {
		t.Fatalf("Drive delete should still happen alongside local cleanup, got %d calls", len(driver.deleteCalls))
	}
}
