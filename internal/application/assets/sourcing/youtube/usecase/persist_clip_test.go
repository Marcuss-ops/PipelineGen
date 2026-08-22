package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubClipPersister records the last upsert and returns configurable values.
type stubClipPersister struct {
	lastClip ClipRecord
	clipID   string
	err      error
}

func (s *stubClipPersister) Upsert(_ context.Context, clip ClipRecord) (string, error) {
	s.lastClip = clip
	return s.clipID, s.err
}

// stubOutboxEmitter records the last emit and returns a configurable error.
type stubOutboxEmitter struct {
	lastClipID string
	lastHash   string
	err        error
}

func (s *stubOutboxEmitter) EmitIndexEvent(_ context.Context, clipID, contentHash string) error {
	s.lastClipID = clipID
	s.lastHash = contentHash
	return s.err
}

// ── Test 1: happy path — both ports succeed, full result populated ───────

func TestPersistClipAndEmitEvent_HappyPath(t *testing.T) {
	persister := &stubClipPersister{clipID: "yt_dQw4w9WgXcQ_a1b2c3d4"}
	emitter := &stubOutboxEmitter{}

	cmd := PersistAndEmitCommand{
		ClipID:          "yt_dQw4w9WgXcQ_a1b2c3d4",
		Name:            "My Video",
		Filename:        "dQw4w9WgXcQ - My Video.mp4",
		Source:          "youtube-manual",
		Category:        "sports",
		Tags:            []string{"boxing", "training"},
		DurationSec:     30,
		LocalPath:       "/tmp/clip.mp4",
		LegacyFileMD5:        "a1b2c3d4e5f6a7b8",
		DriveLink:       "https://drive.google.com/file/d/xyz123/view",
		DriveFileID:     "xyz123",
		Summary:         "A boxing training video",
		Topics:          []string{"boxing"},
		Speakers:        []string{"Coach Smith"},
		MentionedPeople: []string{"Mike Tyson"},
		Hook:            "Watch this knockout!",
	}

	result, err := PersistClipAndEmitEvent(context.Background(), persister, emitter, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.Persisted {
		t.Error("expected Persisted=true")
	}
	if !result.EventEmitted {
		t.Error("expected EventEmitted=true")
	}
	if result.ClipID != "yt_dQw4w9WgXcQ_a1b2c3d4" {
		t.Errorf("expected ClipID from persister, got %q", result.ClipID)
	}

	// Verify all fields flowed through to the persister.
	clip := persister.lastClip
	if clip.ID != cmd.ClipID {
		t.Errorf("expected ID %q, got %q", cmd.ClipID, clip.ID)
	}
	if clip.Name != cmd.Name {
		t.Errorf("expected Name %q, got %q", cmd.Name, clip.Name)
	}
	if clip.Duration != time.Duration(cmd.DurationSec)*time.Second {
		t.Errorf("expected Duration %v, got %v", time.Duration(cmd.DurationSec)*time.Second, clip.Duration)
	}

	// Verify emitter received the persister's clipID, not the command's.
	if emitter.lastClipID != "yt_dQw4w9WgXcQ_a1b2c3d4" {
		t.Errorf("expected emitter clipID %q, got %q", "yt_dQw4w9WgXcQ_a1b2c3d4", emitter.lastClipID)
	}
	if emitter.lastHash != cmd.LegacyFileMD5 {
		t.Errorf("expected emitter hash %q, got %q", cmd.LegacyFileMD5, emitter.lastHash)
	}
}

// ── Test 2: nil persister → skip upsert, emitter still fires ─────────────

func TestPersistClipAndEmitEvent_NilPersister_SkipsUpsert(t *testing.T) {
	emitter := &stubOutboxEmitter{}

	cmd := PersistAndEmitCommand{
		ClipID:   "yt_test_id",
		LegacyFileMD5: "feedface",
	}

	result, err := PersistClipAndEmitEvent(context.Background(), nil, emitter, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.Persisted {
		t.Error("expected Persisted=false when persister is nil")
	}
	if !result.EventEmitted {
		t.Error("expected EventEmitted=true — emitter should still fire")
	}
	// ClipID falls back to the command value.
	if result.ClipID != "yt_test_id" {
		t.Errorf("expected ClipID from command, got %q", result.ClipID)
	}
	// Emitter received the command's clipID.
	if emitter.lastClipID != "yt_test_id" {
		t.Errorf("expected emitter clipID %q, got %q", "yt_test_id", emitter.lastClipID)
	}
}

// ── Test 3: nil emitter → persist succeeds, event skipped ────────────────

func TestPersistClipAndEmitEvent_NilEmitter_SkipsEvent(t *testing.T) {
	persister := &stubClipPersister{clipID: "yt_persisted_id"}

	cmd := PersistAndEmitCommand{
		ClipID:   "yt_test_id",
		LegacyFileMD5: "cafebabe",
	}

	result, err := PersistClipAndEmitEvent(context.Background(), persister, nil, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.Persisted {
		t.Error("expected Persisted=true — persister should still fire")
	}
	if result.EventEmitted {
		t.Error("expected EventEmitted=false when emitter is nil")
	}
	if result.ClipID != "yt_persisted_id" {
		t.Errorf("expected ClipID from persister, got %q", result.ClipID)
	}
}

// ── Test 4: both ports nil → no-op, both flags false, no error ───────────

func TestPersistClipAndEmitEvent_BothNil_NoOp(t *testing.T) {
	cmd := PersistAndEmitCommand{
		ClipID:   "yt_fallback_id",
		LegacyFileMD5: "deadbeef",
	}

	result, err := PersistClipAndEmitEvent(context.Background(), nil, nil, cmd)
	if err != nil {
		t.Fatalf("expected nil error for dual-nil ports, got %v", err)
	}
	if result.Persisted {
		t.Error("expected Persisted=false when persister is nil")
	}
	if result.EventEmitted {
		t.Error("expected EventEmitted=false when emitter is nil")
	}
	if result.ClipID != "yt_fallback_id" {
		t.Errorf("expected ClipID from command, got %q", result.ClipID)
	}
}

// ── Test 5: emitter error — persister succeeds, emit fails, partial result ──

func TestPersistClipAndEmitEvent_EmitterError_PartialSuccess(t *testing.T) {
	sentinel := errors.New("outbox: dispatch queue full")
	persister := &stubClipPersister{clipID: "yt_persisted_ok"}
	emitter := &stubOutboxEmitter{err: sentinel}

	cmd := PersistAndEmitCommand{
		ClipID:   "yt_test_id",
		LegacyFileMD5: "cafef00d",
	}

	result, err := PersistClipAndEmitEvent(context.Background(), persister, emitter, cmd)
	if err == nil {
		t.Fatal("expected non-nil error when emitter fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "usecase.PersistClipAndEmitEvent") {
		t.Errorf("expected error to wrap with usecase prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "emit event") {
		t.Errorf("expected 'emit event' in error chain, got %v", err)
	}
	// Persister succeeded — partial state reflected in result.
	if !result.Persisted {
		t.Error("expected Persisted=true — upsert succeeded before emit failed")
	}
	if result.EventEmitted {
		t.Error("expected EventEmitted=false when emitter fails")
	}
	if result.ClipID != "yt_persisted_ok" {
		t.Errorf("expected ClipID from persister, got %q", result.ClipID)
	}
}

// ── Test 6: persister error → fail-closed, emitter never called ──────────

func TestPersistClipAndEmitEvent_PersisterError_Aborts(t *testing.T) {
	sentinel := errors.New("media_assets: UNIQUE constraint failed")
	persister := &stubClipPersister{err: sentinel}
	emitter := &stubOutboxEmitter{}

	cmd := PersistAndEmitCommand{
		ClipID:   "yt_test_id",
		LegacyFileMD5: "baadf00d",
	}

	result, err := PersistClipAndEmitEvent(context.Background(), persister, emitter, cmd)
	if err == nil {
		t.Fatal("expected non-nil error when persister fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "usecase.PersistClipAndEmitEvent") {
		t.Errorf("expected error to wrap with usecase prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "upsert") {
		t.Errorf("expected 'upsert' in error chain, got %v", err)
	}
	// Emitter must NOT have been called.
	if emitter.lastClipID != "" {
		t.Errorf("expected emitter to NOT be called after persister error, got clipID=%q", emitter.lastClipID)
	}
	// Result is still returned (partial state).
	if result.Persisted {
		t.Error("expected Persisted=false on persister error")
	}
}
