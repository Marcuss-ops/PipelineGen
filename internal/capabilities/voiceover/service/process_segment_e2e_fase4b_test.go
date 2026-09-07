package voiceover

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

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
