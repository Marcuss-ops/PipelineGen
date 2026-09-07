package voiceover

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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
