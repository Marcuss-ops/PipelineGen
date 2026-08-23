// Package voiceover — usecase/process_segment_e2e_test.go
//
// E2E-phase tests for the SHARED per-item pipeline runner
// (usecase/process_segment.go). These tests cover the late-FASE and
// E2E contracts — audio format validation, transactional outbox
// orphan-cleanup, real-finalizer happy paths, real-finalizer failure
// rollback — WITHOUT re-testing the construction / execution /
// idempotency gates (those live in the construction + execution +
// idempotency files respectively).
//
// godlike/06 SSOT (one canonical owner per fact): each test pins
// exactly one E2E capability concern:
//
//  14. TestIsValidAudioFormat_MP3FrameSync — FASE 2 audio format
//     validation helper (MP3 frame sync + WAV RIFF header).
//
//  15. TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyProject
//     — FASE 2 contract #5 (Publisher empty Project verbatim).
//
//  16. TestProcessSegmentUseCase_Execute_FASE4_DriveUploadOK_FinalizeFail_EmitsCleanup
//     — FASE 4 happy-orphan-cleanup contract (cleanup event in separate tx).
//
//  17. TestProcessSegmentUseCase_Execute_FASE4_Stage0Failure_NoCleanupEvent
//     — FASE 4 nil-guard (Stage 0 short-circuit, no Drive upload, no cleanup).
//
//  18. TestProcessSegmentUseCase_Execute_FASE4_NilOutboxEnqueuer_NoPanic
//     — FASE 4 nil-safe (orphan-cleanup path silently skipped).
//
//  19. TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupBeginTxFail_Warns
//     — FASE 4 BeginTx-failure path (Warn log, no cleanup event).
//
//  20. TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupEnqueueFail_Warns
//     — FASE 4 EnqueueCleanupEvent-failure path (Warn log, tx rolled back).
//
//  21. TestProcessSegmentUseCase_Execute_FASE5_E2E_RealFinalizer_HappyPath
//     — FASE 5 E2E with REAL voiceoverFinalizer (row in SQLite).
//
//  22. TestProcessSegmentUseCase_Execute_FASE5_E2E_IdempotencyReplay
//     — FASE 5 E2E idempotency replay with real finalizer.
//
//  23. TestProcessSegmentUseCase_Execute_FASE6_E2E_OrphanCleanup_RealFinalizer
//     — FASE 6 E2E orphan-cleanup with real finalizer + InsertTx failure.
//
// godlike/07 minimum-blast-radius: zero production code changes.
// Inline test-only helpers (mp3SyncPatterns, wavRIFFHeader,
// isValidAudioFormat) are kept here rather than promoted to the
// shared helpers file because they are hermetic test-internal
// constants, not stubs for production ports. Promoting them to the
// helpers surface would amplify a non-port surface for one test
// (Test 14), which is the wrong side of the SSOT tradeoff.
package voiceover

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 14: FASE 2 audio format validation — mp3/wav header detection
// ─────────────────────────────────────────────────────────────────────────

// mp3SyncPatterns are the valid MPEG audio frame sync bytes for Layer III.
// The first byte must be 0xFF and the second byte's top 3 bits must be
// 111 (0xE0 mask). Common values: 0xFF 0xFB (MPEG1 Layer3), 0xFF 0xF3
// (MPEG2 Layer3), 0xFF 0xF2 (MPEG2.5 Layer3).
var mp3SyncPatterns = [][]byte{
	{0xFF, 0xFB},
	{0xFF, 0xF3},
	{0xFF, 0xF2},
}

// isValidAudioFormat reads the first bytes of a file and checks for
// MP3 frame sync or WAV RIFF header. Returns nil on valid audio,
// a descriptive error otherwise.
func isValidAudioFormat(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open audio file: %w", err)
	}
	defer f.Close()

	// Read first 12 bytes to cover both MP3 (2 bytes) and WAV (12 bytes).
	buf := make([]byte, 12)
	n, err := f.Read(buf)
	if err != nil || n < 2 {
		return fmt.Errorf("audio file too short (%d bytes): %w", n, err)
	}

	// MP3 check: first byte 0xFF, second byte top 3 bits = 111 (0xE0 mask).
	if buf[0] == 0xFF && (buf[1]&0xE0) == 0xE0 {
		return nil
	}

	// WAV check: first 4 bytes = "RIFF", bytes 8-11 = "WAVE".
	if n >= 12 && string(buf[0:4]) == "RIFF" && string(buf[8:12]) == "WAVE" {
		return nil
	}

	return fmt.Errorf("not a valid audio format: first 2 bytes = %02X %02X", buf[0], buf[1])
}

// TestIsValidAudioFormat_MP3FrameSync pinc the FASE 2 contract #1 extension:
// the isValidAudioFormat helper must correctly detect MP3 files (frame sync
// pattern 0xFF 0xFB/0xF3/0xF2) and WAV files (RIFF header). Rejects empty
// files, non-audio bytes, and missing files.
//
// godlike/07 NO-FAKE-AVAILABILITY: each sub-test writes a real temp file
// with the advertised bytes — the helper reads real disk bytes, not
// stub strings.
func TestIsValidAudioFormat_MP3FrameSync(t *testing.T) {
	tests := []struct {
		name    string
		bytes   []byte
		wantErr bool
		errText string
	}{
		{
			name:    "MPEG1 Layer3 (0xFF 0xFB)",
			bytes:   []byte{0xFF, 0xFB, 0x90, 0x00}, // minimal valid frame header
			wantErr: false,
		},
		{
			name:    "MPEG2 Layer3 (0xFF 0xF3)",
			bytes:   []byte{0xFF, 0xF3, 0x90, 0x00},
			wantErr: false,
		},
		{
			name:    "MPEG2.5 Layer3 (0xFF 0xF2)",
			bytes:   []byte{0xFF, 0xF2, 0x90, 0x00},
			wantErr: false,
		},
		{
			name:    "WAV RIFF header",
			bytes:   append([]byte("RIFF\x00\x00\x00\x00WAVE"), 0x00),
			wantErr: false,
		},
		{
			name:    "empty file (too short)",
			bytes:   []byte{},
			wantErr: true,
			errText: "too short",
		},
		{
			name:    "HTML error page (non-audio bytes)",
			bytes:   []byte("<!DOCTYPE html>\n"),
			wantErr: true,
			errText: "not a valid audio format",
		},
		{
			name:    "missing file",
			bytes:   nil, // special case: don't create the file
			wantErr: true,
			errText: "cannot open",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.bytes != nil {
				f, err := os.CreateTemp(t.TempDir(), "audio-test-*")
				require.NoError(t, err)
				_, err = f.Write(tt.bytes)
				require.NoError(t, err)
				require.NoError(t, f.Close())
				path = f.Name()
			} else {
				path = "/nonexistent/path/for/audio/validation/test"
			}

			err := isValidAudioFormat(path)
			if tt.wantErr {
				require.Error(t, err, "FASE 2 contract: invalid audio must return error")
				if tt.errText != "" {
					assert.Contains(t, err.Error(), tt.errText)
				}
			} else {
				assert.NoError(t, err, "FASE 2 contract: valid audio must return nil error")
			}
		})
	}

	// Cross-reference: canonical MP3 sync patterns must match the spec.
	for i, pat := range mp3SyncPatterns {
		assert.Equal(t, byte(0xFF), pat[0],
			"mp3SyncPatterns[%d] first byte must be 0xFF (MPEG frame sync)", i)
		assert.True(t, (pat[1]&0xE0) == 0xE0,
			"mp3SyncPatterns[%d] second byte top 3 bits must be 111 (0xE0 mask)", i)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 15: FASE 2 — Stage 3 Publisher with empty Project propagates correctly
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyProject
// pins the FASE 2 contract #5: when ProcessSegmentCommand carries an
// empty Project, the VoiceoverPublishCommand must forward the empty
// value verbatim. The Publisher adapter then routes empty Project →
// no ProjectID in the PublishRequest (semantic-first path).
//
// This is the companion to Test 9 which tests non-empty Project.
// Together they lock the full propagation contract.
func TestProcessSegmentUseCase_Execute_Stage3_Publisher_EmptyProject(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/stage3-empty-proj.mp3",
			Voice:         "it-IT-ElsaNeural",
			LegacyFileMD5: "hash-stage3-ep-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-stage3-empty-proj"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-stage3-empty-proj", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-stage3-ep"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-stage3-ep"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-stage3-empty-proj",
		Language: "it-IT",
		Text:     "Testo con Project vuoto.",
		Voice:    "it-IT-ElsaNeural",
		Filename: "stage3-empty-proj.mp3",
		Project:  "", // empty — Publisher sees empty string verbatim
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, out.Status,
		"empty Project must NOT prevent pipeline completion")

	// FASE 2 contract #5: empty Project MUST be forwarded verbatim.
	require.Len(t, pub.published, 1,
		"Publisher.Publish must be invoked exactly once")
	got := pub.published[0]
	assert.Equal(t, "", got.Project,
		"FASE 2 contract #5: empty cmd.Project MUST be forwarded as empty VoiceoverPublishCommand.Project (verbatim propagation)")
	assert.Equal(t, "it-IT", got.Language,
		"Language must still be forwarded correctly even when Project is empty")

	// Finalizer MUST be invoked exactly once (symmetry with Test 9).
	require.Len(t, finalizer.calls, 1,
		"Finalizer.Finalize must be invoked exactly once even when Project is empty")
}

// ────────────────────────────────────────────────────────────────────────────
// Test 16: FASE 4 — Drive upload OK + Finalize FAIL → cleanup event emitted
// ────────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE4_DriveUploadOK_FinalizeFail_EmitsCleanup
// pins the FASE 4 transaction-boundary contract: when Stage 3 (Drive
// upload) succeeds but Stage 4 (Finalize inside a caller-owned tx)
// fails, the use case MUST emit a voiceover.cleanup.requested outbox
// event in a SEPARATE tx (the Finalize tx was rolled back) so the
// orphaned Drive file is eventually cleaned up.
func TestProcessSegmentUseCase_Execute_FASE4_DriveUploadOK_FinalizeFail_EmitsCleanup(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/fase4-orphan.mp3",
			CleanedPath:   "/tmp/vo/fase4-orphan-cleaned.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-fase4-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-fase4-orphan"}
	finalizer := &stubProcessFinalizer{
		cannedErr: fmt.Errorf("finalizer: simulated DB write failure"),
	}
	outboxStub := &stubTxOutboxEnqueuer{}

	dest := &stubProcessDestResolver{folderID: "dest-fase4"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-fase4"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-fase4-orphan",
		Language: "en",
		Text:     "This voiceover will be orphaned.",
		Voice:    "en-US-RogerNeural",
		Filename: "fase4-orphan.mp3",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.Error(t, err, "FASE 4: Finalize failure MUST return error")
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Contains(t, out.Error, "finalize_failed:")

	// Stage 3 succeeded: DriveFileID is set.
	assert.Equal(t, "drive-fase4-orphan", out.DriveFileID)

	// Publisher + Finalizer were both invoked.
	require.Len(t, pub.published, 1)
	require.Len(t, finalizer.calls, 1)

	// FASE 4 contract: cleanup event emitted in a SEPARATE tx.
	require.Len(t, outboxStub.cleanupEvents, 1,
		"FASE 4: EnqueueCleanupEvent MUST be called exactly once for the orphaned Drive file")
	ce := outboxStub.cleanupEvents[0]
	assert.Equal(t, "vo-fase4-orphan", ce.voiceoverID)
	assert.Equal(t, "drive-fase4-orphan", ce.oldDriveFileID,
		"FASE 4: oldDriveFileID is the cleanup target when no row was finalized")
	assert.Equal(t, "", ce.newDriveFileID,
		"FASE 4: newDriveFileID must be empty because no replacement was finalized")
	assert.Contains(t, ce.oldLocalPaths, "/tmp/vo/fase4-orphan.mp3",
		"FASE 4: oldLocalPaths must contain the TTS local path")
	assert.Contains(t, ce.oldLocalPaths, "/tmp/vo/fase4-orphan-cleaned.mp3",
		"FASE 4: oldLocalPaths must contain the cleaned path")

	// The cleanup tx is separate from the Finalize tx
	// (guaranteed by the production code opening a fresh BeginTx).
	require.NotNil(t, ce.tx, "FASE 4: cleanup event must carry a non-nil tx (fresh BeginTx)")

	// Index events must NOT have been emitted.
	assert.Len(t, outboxStub.indexEvents, 0,
		"FASE 4: EnqueueIndexEvent must NOT be called on orphan-cleanup path")
}

// TestProcessSegmentUseCase_Execute_FASE4_Stage0Failure_NoCleanupEvent
// pins the FASE 4 nil-guard: when Execute fails BEFORE Stage 3
// (e.g. Stage 0 missing-folder), DriveFileID is empty and NO cleanup
// event is emitted (no orphaned Drive file exists).
func TestProcessSegmentUseCase_Execute_FASE4_Stage0Failure_NoCleanupEvent(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{cannedOut: TTSOutput{LocalPath: "/tmp/unused.mp3"}}
	pub := &stubProcessPublisher{fileID: "unused"}
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "unused"}}
	outboxStub := &stubTxOutboxEnqueuer{}

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-fase4-stage0",
		Language: "en",
		Filename: "stage0.mp3",
		Dest:     nil, // Stage 0 short-circuit
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.Error(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Empty(t, out.DriveFileID)

	assert.Len(t, outboxStub.cleanupEvents, 0,
		"FASE 4: Stage 0 failure must NOT emit cleanup (no Drive upload)")
	assert.Len(t, outboxStub.indexEvents, 0)
}

// TestProcessSegmentUseCase_Execute_FASE4_NilOutboxEnqueuer_NoPanic
// pins the FASE 4 nil-safe contract: when TxOutboxEnqueuer is nil
// (pre-FASE-4 callers), the orphan-cleanup path is silently skipped.
func TestProcessSegmentUseCase_Execute_FASE4_NilOutboxEnqueuer_NoPanic(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/fase4-nil.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-fase4-nil",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-fase4-nil"}
	finalizer := &stubProcessFinalizer{
		cannedErr: fmt.Errorf("finalizer: simulated failure"),
	}

	dest := &stubProcessDestResolver{folderID: "dest-fase4-nil"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-fase4-nil"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    nil, // pre-FASE-4: not wired
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-fase4-nil",
		Language: "en",
		Text:     "Nil outbox test.",
		Filename: "fase4-nil.mp3",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.Error(t, err, "Finalize failure must still return error")
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Equal(t, "drive-fase4-nil", out.DriveFileID)
	assert.Contains(t, out.Error, "finalize_failed:")
	// No panic — nil TxOutboxEnqueuer is handled gracefully.
}

// ────────────────────────────────────────────────────────────────────────────
// Test 19: FASE 4 — enqueueOrphanCleanup BeginTx failure → Warn log
// ────────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupBeginTxFail_Warns
// pins the forward-pointer contract for the BeginTx-failure path in
// enqueueOrphanCleanup: when Stage 3 (Drive upload) succeeds and Stage 4
// (Finalize) fails, the orphan-cleanup path opens a SEPARATE tx. If that
// BeginTx also fails (e.g. DB unreachable), the method logs a Warn and
// returns WITHOUT enqueuing a cleanup event. The original Finalize error
// is still the canonical job outcome.
//
// godlike/07 NO-FAKE-AVAILABILITY: this path was previously untested —
// a regression that silently swallowed the BeginTx error would mask
// operator-visible Warn signals.
func TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupBeginTxFail_Warns(t *testing.T) {
	repo := newStubVoRepoFailSecondBeginTx(t, fmt.Errorf("sqlite: disk I/O error"))
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/fase4-begintx-fail.mp3",
			CleanedPath:   "/tmp/vo/fase4-begintx-fail-cleaned.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-fase4-begintx",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-fase4-begintx"}
	finalizer := &stubProcessFinalizer{
		cannedErr: fmt.Errorf("finalizer: simulated constraint violation"),
	}
	outboxStub := &stubTxOutboxEnqueuer{}

	dest := &stubProcessDestResolver{folderID: "dest-fase4-begintx"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-fase4-begintx"})
	require.NoError(t, err)

	// Capture log output to verify the Warn message.
	core, observed := observer.New(zap.WarnLevel)
	log := zap.New(core)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              log,
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-fase4-begintx",
		Language: "en",
		Text:     "BeginTx failure in orphan cleanup.",
		Voice:    "en-US-RogerNeural",
		Filename: "fase4-begintx.mp3",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	// Stage 4 Finalize failed → error propagated.
	require.Error(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Contains(t, out.Error, "finalize_failed:")
	assert.Equal(t, "drive-fase4-begintx", out.DriveFileID)

	// Publisher + Finalizer were invoked.
	require.Len(t, pub.published, 1)
	require.Len(t, finalizer.calls, 1)

	// enqueueOrphanCleanup was invoked (2 BeginTx calls = 1st for Finalize tx + 2nd for cleanup tx).
	assert.Equal(t, 2, repo.callCount,
		"FASE 4: enqueueOrphanCleanup must attempt a 2nd BeginTx for the cleanup tx")

	// Cleanup event was NOT enqueued (BeginTx failed before EnqueueCleanupEvent).
	assert.Len(t, outboxStub.cleanupEvents, 0,
		"FASE 4: cleanup event must NOT be enqueued when BeginTx fails")

	// Warn log emitted with the canonical message fragment.
	logs := observed.FilterMessageSnippet("orphan-cleanup: BeginTx failed").All()
	assert.Len(t, logs, 1,
		"FASE 4: BeginTx failure in enqueueOrphanCleanup MUST emit a Warn log")
	// Verify canonical log fields.
	if len(logs) > 0 {
		entry := logs[0]
		assert.Equal(t, "vo-fase4-begintx", entry.ContextMap()["voiceover_id"])
		assert.Equal(t, "drive-fase4-begintx", entry.ContextMap()["drive_file_id"])
		assert.Contains(t, fmt.Sprint(entry.ContextMap()["error"]), "disk I/O error")
	}

	// The original Finalize error is still the canonical job outcome.
	assert.Contains(t, out.Error, "finalize_failed:")
}

// ────────────────────────────────────────────────────────────────────────────
// Test 20: FASE 4 — enqueueOrphanCleanup EnqueueCleanupEvent failure → Warn log
// ────────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupEnqueueFail_Warns
// pins the forward-pointer contract for the EnqueueCleanupEvent-failure path
// in enqueueOrphanCleanup: when Stage 3 succeeds and Stage 4 fails, the
// orphan-cleanup tx opens successfully, but EnqueueCleanupEvent itself
// fails (e.g. outbox DB write error). The method logs a Warn and returns
// WITHOUT committing the cleanup tx (the defer Rollback fires). The
// original Finalize error is still the canonical job outcome.
func TestProcessSegmentUseCase_Execute_FASE4_OrphanCleanupEnqueueFail_Warns(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/fase4-enqueue-fail.mp3",
			CleanedPath:   "/tmp/vo/fase4-enqueue-fail-cleaned.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-fase4-enqueue",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-fase4-enqueue"}
	finalizer := &stubProcessFinalizer{
		cannedErr: fmt.Errorf("finalizer: simulated UNIQUE constraint violation"),
	}
	outboxStub := &stubTxOutboxEnqueuer{
		cleanupErr: fmt.Errorf("outbox: disk full"),
	}

	dest := &stubProcessDestResolver{folderID: "dest-fase4-enqueue"}
	resolvedDest, err := dest.Resolve(context.Background(),
		&DestinationRequest{FolderID: "dest-fase4-enqueue"})
	require.NoError(t, err)

	// Capture log output.
	core, observed := observer.New(zap.WarnLevel)
	log := zap.New(core)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              log,
	})

	cmd := &ProcessSegmentCommand{
		ID:       "vo-fase4-enqueue",
		Language: "en",
		Text:     "EnqueueCleanupEvent failure test.",
		Voice:    "en-US-RogerNeural",
		Filename: "fase4-enqueue.mp3",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	// Stage 4 Finalize failed → error propagated.
	require.Error(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Contains(t, out.Error, "finalize_failed:")
	assert.Equal(t, "drive-fase4-enqueue", out.DriveFileID)

	// Publisher + Finalizer were invoked.
	require.Len(t, pub.published, 1)
	require.Len(t, finalizer.calls, 1)

	// EnqueueCleanupEvent WAS attempted (the stub records the call even on error).
	require.Len(t, outboxStub.cleanupEvents, 1,
		"FASE 4: EnqueueCleanupEvent must be attempted (BeginTx succeeded)")
	ce := outboxStub.cleanupEvents[0]
	assert.Equal(t, "vo-fase4-enqueue", ce.voiceoverID)
	assert.Equal(t, "drive-fase4-enqueue", ce.oldDriveFileID)
	assert.Equal(t, "", ce.newDriveFileID)

	// Warn log emitted with the canonical message fragment.
	logs := observed.FilterMessageSnippet("orphan-cleanup: EnqueueCleanupEvent failed").All()
	assert.Len(t, logs, 1,
		"FASE 4: EnqueueCleanupEvent failure in enqueueOrphanCleanup MUST emit a Warn log")
	if len(logs) > 0 {
		entry := logs[0]
		assert.Equal(t, "vo-fase4-enqueue", entry.ContextMap()["voiceover_id"])
		assert.Equal(t, "drive-fase4-enqueue", entry.ContextMap()["drive_file_id"])
		assert.Contains(t, fmt.Sprint(entry.ContextMap()["error"]), "disk full")
	}

	// The original Finalize error is still the canonical job outcome.
	assert.Contains(t, out.Error, "finalize_failed:")
}

// ──────────────────────────────────────────────────────────────────────────────
// Test 21: FASE 5 — E2E full 4-stage pipeline with real finalizer + SQLite tx
// ──────────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE5_E2E_RealFinalizer_HappyPath
// exercises the full 4-stage pipeline (TTS → Publish → Finalize → Commit)
// with the REAL voiceoverFinalizer wired against an in-memory SQLite DB.
// The prior tests (1-20) use stubProcessFinalizer which records calls
// but never touches the DB. This test pins the end-to-end happy path:
// the finalizer actually INSERTs a row into voiceovers, enqueues an
// index outbox event via the TxOutboxEnqueuer, and the tx commits.
//
// Asserted invariants:
//  1. Execute returns StatusCompleted.
//  2. The voiceover row is durably present in SQLite (post-commit SELECT).
//  3. The row carries all canonical fields (id, drive_file_id, file_hash, etc.).
//  4. The index outbox event was emitted exactly once.
//  5. No cleanup event (happy path, ShouldSwap=false).
//  6. The media_assets projection was invoked exactly once.
func TestProcessSegmentUseCase_Execute_FASE5_E2E_RealFinalizer_HappyPath(t *testing.T) {
	db := openProcessTestDB(t)
	repo := &stubProcessVoRepo{db: db}
	outboxStub := &stubTxOutboxEnqueuer{}
	lifecycleStub := &stubLifecycleProjectionUpserter{}

	finalizer := NewVoiceoverFinalizer(
		repo,
		outboxStub,
		lifecycleStub,
		nil, // committer — pre-Cutover (PR-ASSET-COMMITTER-COMMITASSET Phase 2)
		zap.NewNop(),
	)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/e2e-happy.mp3",
			CleanedPath:   "/tmp/vo/e2e-happy-cleaned.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-e2e-happy-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-e2e-happy-001"}

	dest := &stubProcessDestResolver{folderID: "dest-e2e-happy"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-e2e-happy"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		JobID:    "job-e2e-happy",
		ID:       "vo-e2e-happy",
		Language: "en",
		Text:     "E2E test with the real voiceover finalizer.",
		TextHash: "hash-e2e-text-001",
		Voice:    "en-US-RogerNeural",
		Filename: "e2e-happy.mp3",
		Strategy: "replace",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	require.NoError(t, err, "FASE 5 E2E: Execute must succeed with real finalizer")
	require.NotNil(t, out)
	assert.Equal(t, StatusCompleted, out.Status, "FASE 5 E2E: happy path must end with StatusCompleted")
	assert.Equal(t, "vo-e2e-happy", out.ID)
	assert.Equal(t, "drive-e2e-happy-001", out.DriveFileID)

	// ── Assertion 1: voiceover row durably present in SQLite ──────────────
	var (
		rowID          string
		rowDriveFileID string
		rowFileHash    string
		rowLanguage    string
		rowIdemKey     string
		rowJobID       string
	)
	err = db.QueryRow(`SELECT id, drive_file_id, file_hash, language, idempotency_key, job_id FROM voiceovers WHERE id = ?`, "vo-e2e-happy").
		Scan(&rowID, &rowDriveFileID, &rowFileHash, &rowLanguage, &rowIdemKey, &rowJobID)
	require.NoError(t, err, "FASE 5 E2E: voiceover row must be durably present in SQLite after commit")
	assert.Equal(t, "vo-e2e-happy", rowID)
	assert.Equal(t, "drive-e2e-happy-001", rowDriveFileID)
	assert.Equal(t, "hash-e2e-happy-001", rowFileHash)
	assert.Equal(t, "en", rowLanguage)
	assert.NotEmpty(t, rowIdemKey, "idempotency_key must be populated when JobID is non-empty")
	assert.Equal(t, "job-e2e-happy", rowJobID)

	// ── Assertion 2: index outbox event emitted ──────────────────
	require.Len(t, outboxStub.indexEvents, 1,
		"FASE 5 E2E: index outbox event must be emitted exactly once")
	assert.Equal(t, "vo-e2e-happy", outboxStub.indexEvents[0].assetID)
	assert.Equal(t, "hash-e2e-happy-001", outboxStub.indexEvents[0].contentHash)
	assert.NotNil(t, outboxStub.indexEvents[0].tx)

	// ── Assertion 3: NO cleanup event (happy path, ShouldSwap=false) ────
	assert.Len(t, outboxStub.cleanupEvents, 0,
		"FASE 5 E2E: no cleanup event on happy path (ShouldSwap=false)")

	// ── Assertion 4: media_assets projection invoked ────────────
	require.Len(t, lifecycleStub.calls, 1,
		"FASE 5 E2E: media_assets projection must be invoked exactly once")
	assert.Equal(t, "vo-e2e-happy", lifecycleStub.calls[0].ID)
	assert.Equal(t, "voiceover", lifecycleStub.calls[0].Source)
	assert.Equal(t, "audio", lifecycleStub.calls[0].MediaType)

	// ── Assertion 5: Publisher invoked with CleanedPath (production
	// priority: CleanedPath > LocalPath when both are non-empty).
	require.Len(t, pub.published, 1)
	assert.Equal(t, "/tmp/vo/e2e-happy-cleaned.mp3", pub.published[0].LocalPath)
	assert.Equal(t, "e2e-happy.mp3", pub.published[0].Filename)
}

// ──────────────────────────────────────────────────────────────────────────────
// Test 22: FASE 5 — E2E idempotency replay with real finalizer
// ──────────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE5_E2E_IdempotencyReplay
// pins the FASE 3 idempotency contract end-to-end: when the same job
// is retried with the same (JobID + Language + TextHash), the second
// invocation triggers the real finalizer's Step 0 idempotency gate
// (FindByIdempotencyKeyTx), short-circuits Steps 1-6, and returns
// Reused=true. The test uses the real finalizer wired against in-memory
// SQLite so the idempotency lookup hits the actual DB row.
//
// Asserted invariants:
//  1. First invocation: StatusCompleted, row inserted, index event emitted.
//  2. Second invocation: StatusCompleted, same row ID.
//  3. Exactly 1 row in SQLite (no duplicate).
//  4. Exactly 1 index event across both invocations.
//  5. Publisher was invoked twice (Stage 3 runs before the idempotency gate).
func TestProcessSegmentUseCase_Execute_FASE5_E2E_IdempotencyReplay(t *testing.T) {
	db := openProcessTestDB(t)
	repo := &stubProcessVoRepo{db: db}
	outboxStub := &stubTxOutboxEnqueuer{}
	lifecycleStub := &stubLifecycleProjectionUpserter{}

	finalizer := NewVoiceoverFinalizer(
		repo,
		outboxStub,
		lifecycleStub,
		nil, // committer — pre-Cutover (PR-ASSET-COMMITTER-COMMITASSET Phase 2)
		zap.NewNop(),
	)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/e2e-idem.mp3",
			CleanedPath:   "/tmp/vo/e2e-idem-cleaned.mp3",
			Voice:         "it-IT-ElsaNeural",
			LegacyFileMD5: "hash-e2e-idem-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-e2e-idem-001"}

	dest := &stubProcessDestResolver{folderID: "dest-e2e-idem"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-e2e-idem"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		JobID:    "job-e2e-idem",
		ID:       "vo-e2e-idem",
		Language: "it-IT",
		Text:     "E2E idempotency replay test.",
		TextHash: "hash-e2e-idem-text",
		Voice:    "it-IT-ElsaNeural",
		Filename: "e2e-idem.mp3",
		Strategy: "replace",
		Dest:     resolvedDest,
	}

	// ── First invocation ──────────────────────────────────
	out1, err1 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err1)
	assert.Equal(t, StatusCompleted, out1.Status)
	assert.Equal(t, "vo-e2e-idem", out1.ID)

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM voiceovers WHERE id = ?`, "vo-e2e-idem").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "first invocation must insert exactly 1 row")

	indexBefore := len(outboxStub.indexEvents)
	require.Equal(t, 1, indexBefore, "first invocation emits 1 index event")

	pubBefore := len(pub.published)
	require.Equal(t, 1, pubBefore, "first invocation publishes to Drive once")

	// ── Second invocation (replay) ─────────────────────────
	out2, err2 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err2)
	assert.Equal(t, StatusCompleted, out2.Status)
	assert.Equal(t, "vo-e2e-idem", out2.ID, "idempotency gate must return the matched row ID")

	// ── Assertions ──────────────────────────────────

	// Exactly 1 row still in SQLite.
	err = db.QueryRow(`SELECT COUNT(*) FROM voiceovers WHERE id = ?`, "vo-e2e-idem").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "FASE 5 E2E idempotency: exactly 1 row after replay (no duplicate)")

	// Exactly 1 index event across both invocations.
	assert.Len(t, outboxStub.indexEvents, 1,
		"FASE 5 E2E idempotency: exactly 1 index event (replay short-circuits at Step 0)")

	// Publisher was invoked twice (Stage 3 runs before Finalize).
	assert.Len(t, pub.published, 2,
		"FASE 5 E2E: Publisher invoked twice (Stage 3 is outside the idempotency gate)")

	// Media_assets projection was invoked only once.
	assert.Len(t, lifecycleStub.calls, 1,
		"FASE 5 E2E idempotency: media_assets projection invoked once (replay short-circuits)")
}

// ──────────────────────────────────────────────────────────────────────────────
// Test 23: FASE 6 — E2E orphan-cleanup with real finalizer + InsertTx failure
// ──────────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE6_E2E_OrphanCleanup_RealFinalizer
// exercises the full orphan-cleanup path end-to-end with the REAL
// voiceoverFinalizer wired against in-memory SQLite. Stage 3 (Drive
// upload) succeeds → DriveFileID is set. Stage 4 (Finalize) fails
// because InsertTx returns an error. The use case's orphan-cleanup
// path opens a SEPARATE tx (the Finalize tx was rolled back) and
// enqueues a voiceover.cleanup.requested outbox event.
//
// Asserted invariants:
//  1. Execute returns error with "finalize_failed:" prefix.
//  2. DriveFileID is set (Stage 3 succeeded before Stage 4 failed).
//  3. Exactly 1 cleanup event was emitted (in a separate tx).
//  4. Cleanup event carries the correct voiceoverID + driveFileID.
//  5. oldLocalPaths contains both localPath and cleanedPath.
//  6. The voiceover row is NOT present in SQLite (Finalize tx rolled back).
//  7. No index event was emitted (index event requires successful InsertTx).
//  8. Media_assets projection was NOT invoked (Step 4 runs after InsertTx).
//  9. Publisher was invoked exactly once (Stage 3 ran successfully).
func TestProcessSegmentUseCase_Execute_FASE6_E2E_OrphanCleanup_RealFinalizer(t *testing.T) {
	db := openProcessTestDB(t)
	baseRepo := &stubProcessVoRepo{db: db}
	repo := &stubFailingInsertRepo{
		stubProcessVoRepo: baseRepo,
		insertErr:         fmt.Errorf("sqlite: simulated UNIQUE constraint violation on voiceovers"),
	}
	outboxStub := &stubTxOutboxEnqueuer{}
	lifecycleStub := &stubLifecycleProjectionUpserter{}

	finalizer := NewVoiceoverFinalizer(
		repo,
		outboxStub,
		lifecycleStub,
		nil, // committer — pre-Cutover (PR-ASSET-COMMITTER-COMMITASSET Phase 2)
		zap.NewNop(),
	)

	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/fase6-orphan.mp3",
			CleanedPath:   "/tmp/vo/fase6-orphan-cleaned.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "hash-fase6-001",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-fase6-orphan"}

	dest := &stubProcessDestResolver{folderID: "dest-fase6"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-fase6"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		TxOutboxEnqueuer:    outboxStub,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		JobID:    "job-fase6-orphan",
		ID:       "vo-fase6-orphan",
		Language: "en",
		Text:     "This voiceover will be orphaned by a failing InsertTx.",
		TextHash: "hash-fase6-text-001",
		Voice:    "en-US-RogerNeural",
		Filename: "fase6-orphan.mp3",
		Strategy: "replace",
		Dest:     resolvedDest,
	}

	out, err := uc.Execute(context.Background(), cmd)

	// ── Assertion 1: Stage 4 Failed ────────────────────────────
	require.Error(t, err, "FASE 6 E2E: Finalize failure (InsertTx) MUST return error")
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Contains(t, out.Error, "finalize_failed:")

	// ── Assertion 2: Stage 3 Succeeded ─────────────────────────
	assert.Equal(t, "drive-fase6-orphan", out.DriveFileID,
		"FASE 6 E2E: DriveFileID must be set (Stage 3 succeeded before Stage 4 failed)")

	// ── Assertion 3: Cleanup event emitted ─────────────────────
	require.Len(t, outboxStub.cleanupEvents, 1,
		"FASE 6 E2E: exactly 1 cleanup event must be emitted for the orphaned Drive file")
	ce := outboxStub.cleanupEvents[0]
	assert.Equal(t, "vo-fase6-orphan", ce.voiceoverID)
	assert.Equal(t, "drive-fase6-orphan", ce.oldDriveFileID,
		"FASE 6 E2E: oldDriveFileID is the cleanup target when no row was finalized")
	assert.Equal(t, "", ce.newDriveFileID,
		"FASE 6 E2E: newDriveFileID must be empty because no replacement was finalized")
	assert.NotNil(t, ce.tx, "FASE 6 E2E: cleanup event must carry a non-nil tx (separate BeginTx)")

	// ── Assertion 4: oldLocalPaths contain both paths ───────────
	assert.Contains(t, ce.oldLocalPaths, "/tmp/vo/fase6-orphan.mp3",
		"FASE 6 E2E: oldLocalPaths must contain the TTS local path")
	assert.Contains(t, ce.oldLocalPaths, "/tmp/vo/fase6-orphan-cleaned.mp3",
		"FASE 6 E2E: oldLocalPaths must contain the cleaned path")

	// ── Assertion 5: Row NOT in SQLite (tx rolled back) ────────
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM voiceovers WHERE id = ?`, "vo-fase6-orphan").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count,
		"FASE 6 E2E: voiceover row must NOT be present in SQLite (Finalize tx was rolled back)")

	// ── Assertion 6: No index event ────────────────────────────
	assert.Len(t, outboxStub.indexEvents, 0,
		"FASE 6 E2E: no index event must be emitted (InsertTx failed before Step 5)")

	// ── Assertion 7: Media_assets NOT invoked ───────────────────
	assert.Len(t, lifecycleStub.calls, 0,
		"FASE 6 E2E: media_assets projection must NOT be invoked (UpsertVoiceoverProjectionTx runs after InsertTx)")

	// ── Assertion 8: Publisher invoked exactly once ─────────────
	require.Len(t, pub.published, 1,
		"FASE 6 E2E: Publisher must be invoked exactly once (Stage 3 succeeded)")
}
