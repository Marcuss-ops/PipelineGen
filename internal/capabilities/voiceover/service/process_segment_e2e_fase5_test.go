package voiceover

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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
